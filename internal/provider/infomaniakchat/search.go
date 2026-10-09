package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSearchRunes = 512
	// maxHitLimit is the largest page a message or file search asks kChat for.
	maxHitLimit = 100
)

// searchTermSchema is a search term of 1 to 512 characters without control characters.
var searchTermSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxSearchRunes) +
	`,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`

var searchTermsArgument = capability.Argument{Name: "terms", Required: true,
	Description: "Search terms, 1 to " + itoa(maxSearchRunes) + " characters without control characters; kChat's " +
		"from: and in: syntax is allowed, hits outside the channels this connection may reach are dropped"}

var searchPagingArguments = []capability.Argument{
	{Name: "is_or_search", Description: "True to match any term instead of all terms; false when omitted"},
	{Name: "page", Description: "1-based page of kChat's own search result; the first page when omitted"},
	{Name: "limit", Description: "Hits per kChat page, 1 to " + itoa(maxHitLimit) + "; " + itoa(defaultListLimit) +
		" when omitted"},
}

var searchPagingSchema = `"is_or_search":{"type":"boolean"},"page":{"type":"integer","minimum":1},` +
	`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxHitLimit) + `}`

var searchPagingFields = []capability.Field{
	{Name: "team_id", Description: "Team that was searched"},
	{Name: "page", Description: "Page that was read"},
	{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
		"pages are never read on by themselves"},
	{Name: "count", Description: "Number of hits reported on this page, after the reachable-channel filter"},
}

var messagesSearch = capability.Descriptor{
	ID:      Provider + ".messages.search",
	Version: 1,
	Title:   "Search Infomaniak kChat messages",
	Description: "Search the messages of one team this connection is bound to, one kChat page at a time; only hits " +
		"from team channels this connection may reach are returned, never direct or group messages",
	Tags:     []string{"infomaniak", "kchat", "messages", "search", "posts"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"terms":` +
		searchTermSchema + `,` + searchPagingSchema + `},"required":["team_id","terms"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"messages":{"type":"array","items":` + messageEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["team_id","messages","page","has_more","count"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{teamIDArgument, searchTermsArgument}, searchPagingArguments...),
	Fields:    append(append([]capability.Field{}, messageEntryFields...), searchPagingFields...),
	Examples: []capability.Example{{Description: "Search the messages of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","terms":"deploy in:town-square"}`)}},
}

var filesSearch = capability.Descriptor{
	ID:      Provider + ".files.search",
	Version: 1,
	Title:   "Search Infomaniak kChat files",
	Description: "Search the file attachments of one team this connection is bound to, one kChat page at a time; " +
		"only hits from team channels this connection may reach are returned, never direct or group files",
	Tags:     []string{"infomaniak", "kchat", "files", "search"},
	Risk:     filesRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"terms":` +
		searchTermSchema + `,` + searchPagingSchema + `},"required":["team_id","terms"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"files":{"type":"array","items":` + fileEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["team_id","files","page","has_more","count"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{teamIDArgument, searchTermsArgument}, searchPagingArguments...),
	Fields:    append(append([]capability.Field{}, fileEntryFields...), searchPagingFields...),
	Examples: []capability.Example{{Description: "Search the files of one bound team by extension",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","terms":"ext:pdf"}`)}},
}

var channelsSearch = capability.Descriptor{
	ID:      Provider + ".channels.search",
	Version: 1,
	Title:   "Search Infomaniak kChat channels",
	Description: "Search the public channels of one team this connection is bound to by name, limited to channels " +
		"the token is a member of and this connection's channel allow-list; no further page",
	Tags:     []string{"infomaniak", "kchat", "channels", "search"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"term":` +
		searchTermSchema + `},"required":["team_id","term"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channels":{"type":"array","items":` + channelEntrySchema + `},` +
		`"count":{"type":"integer"}},"required":["team_id","channels","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, {Name: "term", Required: true,
		Description: "Text matched against the channel name or display name, 1 to " + itoa(maxSearchRunes) +
			" characters without control characters"}},
	Fields: append(append([]capability.Field{}, channelEntryFields...),
		capability.Field{Name: "count", Description: "Number of channels reported, after the reachable-channel filter"}),
	Examples: []capability.Example{{Description: "Search the channels of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","term":"support"}`)}},
}

// SearchedMessages is one kChat page of message hits, reduced to the reachable channels.
type SearchedMessages struct {
	TeamID   string         `json:"team_id"`
	Messages []MessageEntry `json:"messages"`
	Page     int            `json:"page"`
	HasMore  bool           `json:"has_more"`
	Count    int            `json:"count"`
}

// SearchedFiles is one kChat page of file hits, reduced to the reachable channels.
type SearchedFiles struct {
	TeamID  string      `json:"team_id"`
	Files   []FileEntry `json:"files"`
	Page    int         `json:"page"`
	HasMore bool        `json:"has_more"`
	Count   int         `json:"count"`
}

// SearchedChannels is the channel hits of one search, reduced to the reachable public channels.
type SearchedChannels struct {
	TeamID   string         `json:"team_id"`
	Channels []ChannelEntry `json:"channels"`
	Count    int            `json:"count"`
}

type searchArguments struct {
	TeamID     string `json:"team_id"`
	Terms      string `json:"terms"`
	Term       string `json:"term"`
	IsOrSearch bool   `json:"is_or_search"`
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
}

// searchBody is the typed request body of the post and file searches. include_deleted_channels is
// deliberately absent, so archived channels are never searched.
type searchBody struct {
	Terms      string `json:"terms"`
	IsOrSearch bool   `json:"is_or_search"`
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
}

// validSearchTerm checks a search term's shape; the term is never quoted in an error.
func validSearchTerm(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= 1 &&
		utf8.RuneCountInString(value) <= maxSearchRunes && strings.IndexFunc(value, unicode.IsControl) < 0
}

// prepareSearch validates the arguments locally and opens the client, so a malformed or foreign request
// never reads a secret.
func prepareSearch(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, termKey string) (*searchArguments, *Client, error) {
	var input searchArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, providerError(op, "the validated arguments could not be read")
	}
	if termKey == "term" {
		input.Terms = input.Term
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, nil, err
	}
	if !validSearchTerm(input.Terms) {
		return nil, nil, invalidRequest(termKey + " must be 1 to " + itoa(maxSearchRunes) +
			" characters without control characters")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	if input.Page < 1 || input.Limit < 1 || input.Limit > maxHitLimit {
		return nil, nil, invalidRequest("page must be at least 1 and limit 1 to " + itoa(maxHitLimit))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	return &input, client, nil
}

func (a *searchArguments) body() searchBody {
	return searchBody{Terms: a.Terms, IsOrSearch: a.IsOrSearch, Page: a.Page - 1, PerPage: a.Limit}
}

func invokeMessagesSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, client, err := prepareSearch(ctx, "search messages", resolved, secrets, red, raw, "terms")
	if err != nil {
		return nil, err
	}
	return client.SearchMessages(ctx, input)
}

// SearchMessages reads the reachable channels of the team and one kChat page of post hits, and keeps only
// hits of a live, reachable channel of that team. It never reads on to a further page.
func (c *Client) SearchMessages(ctx context.Context, input *searchArguments) (*SearchedMessages, error) {
	const op = "search messages"
	reachable, err := c.reachableChannels(ctx, op, input.TeamID)
	if err != nil {
		return nil, err
	}
	var list postListJSON
	path := "/api/v4/teams/" + url.PathEscape(input.TeamID) + "/posts/search"
	if err := c.do(ctx, op, http.MethodPost, path, nil, input.body(), &list, false); err != nil {
		return nil, err
	}
	entries := make([]MessageEntry, 0, len(list.Order))
	for _, id := range list.Order {
		post, ok := list.Posts[id]
		if !ok || post.ID != id || post.DeleteAt != 0 || reachable[post.ChannelID] == nil {
			continue
		}
		entries = append(entries, messageEntryOf(post))
	}
	return &SearchedMessages{TeamID: input.TeamID, Messages: entries, Page: input.Page,
		HasMore: len(list.Order) >= input.Limit, Count: len(entries)}, nil
}

func invokeFilesSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, client, err := prepareSearch(ctx, "search files", resolved, secrets, red, raw, "terms")
	if err != nil {
		return nil, err
	}
	return client.SearchFiles(ctx, input)
}

// SearchFiles is SearchMessages for file attachments. A hit without a channel_id cannot be bound to a
// reachable channel and is dropped.
func (c *Client) SearchFiles(ctx context.Context, input *searchArguments) (*SearchedFiles, error) {
	const op = "search files"
	reachable, err := c.reachableChannels(ctx, op, input.TeamID)
	if err != nil {
		return nil, err
	}
	var list struct {
		Order     []string            `json:"order"`
		FileInfos map[string]fileJSON `json:"file_infos"`
	}
	path := "/api/v4/teams/" + url.PathEscape(input.TeamID) + "/files/search"
	if err := c.do(ctx, op, http.MethodPost, path, nil, input.body(), &list, false); err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(list.Order))
	for _, id := range list.Order {
		file, ok := list.FileInfos[id]
		if !ok || file.ID != id || file.DeleteAt != 0 || !validMattermostID(file.PostID) ||
			reachable[file.ChannelID] == nil {
			continue
		}
		entries = append(entries, fileEntryOf(file))
	}
	return &SearchedFiles{TeamID: input.TeamID, Files: entries, Page: input.Page,
		HasMore: len(list.Order) >= input.Limit, Count: len(entries)}, nil
}

func invokeChannelsSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, client, err := prepareSearch(ctx, "search channels", resolved, secrets, red, raw, "term")
	if err != nil {
		return nil, err
	}
	return client.SearchChannels(ctx, input.TeamID, input.Terms)
}

// SearchChannels keeps only public hits that are also channels of the team the token is a member of and
// that this connection may reach: public channels the token has not joined are dropped on purpose, so the
// search never reaches beyond the channel list.
func (c *Client) SearchChannels(ctx context.Context, teamID, term string) (*SearchedChannels, error) {
	const op = "search channels"
	reachable, err := c.reachableChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	var hits []channelJSON
	path := "/api/v4/teams/" + url.PathEscape(teamID) + "/channels/search"
	if err := c.do(ctx, op, http.MethodPost, path, nil, struct {
		Term string `json:"term"`
	}{term}, &hits, false); err != nil {
		return nil, err
	}
	entries := make([]ChannelEntry, 0, len(hits))
	for i := range hits {
		hit := &hits[i]
		known := reachable[hit.ID]
		if known == nil || known.Type != "O" || hit.TeamID != teamID || hit.Type != "O" || hit.DeleteAt != 0 {
			continue
		}
		entries = append(entries, entryOf(hit))
	}
	return &SearchedChannels{TeamID: teamID, Channels: entries, Count: len(entries)}, nil
}
