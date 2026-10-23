package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var teamIDArgument = capability.Argument{Name: "team_id",
	Description: "Team identifier; must be inside this connection's team targets", Required: true}

var channelEntryFields = []capability.Field{
	{Name: "id", Description: "Channel identifier, used as channel_id by every message tool of this provider"},
	{Name: "team_id", Description: "Team this channel belongs to"},
	{Name: "name", Description: "URL-safe channel handle, untrusted data"},
	{Name: "display_name", Description: "Display name of the channel, untrusted data"},
	{Name: "type", Description: "kChat's own channel type, for example O (public) or P (private)"},
	{Name: "purpose", Description: "Short description of the channel's purpose, untrusted data"},
}

// channelActivityProperties are the schema members that channel entries carry besides the identity members;
// created_at and header differ between the entry and the detail.
const channelActivityProperties = `"updated_at":{"type":"string"},"deleted_at":{"type":"string"},` +
	`"last_post_at":{"type":"string"},"total_msg_count":{"type":"integer"},"creator_id":{"type":"string"}`

var channelListEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"purpose":{"type":"string"},"header":{"type":"string"},"created_at":{"type":"string"},` +
	channelActivityProperties + `},"required":["id","team_id","name","display_name","type"],` +
	`"additionalProperties":false}`

var channelActivityFields = []capability.Field{
	{Name: "updated_at", Description: "Time of the last change of the channel, normalised to RFC 3339 in UTC"},
	{Name: "deleted_at", Description: "Time the channel was archived, normalised to RFC 3339 in UTC"},
	{Name: "last_post_at", Description: "Time of the last message in the channel, normalised to RFC 3339 in UTC"},
	{Name: "total_msg_count", Description: "Number of messages ever posted in the channel"},
	{Name: "creator_id", Description: "User identifier of the channel's creator"},
}

var channelListEntryFields = append(append(append([]capability.Field{}, channelEntryFields...),
	capability.Field{Name: "header", Description: "Header text of the channel, untrusted data"},
	capability.Field{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC"}),
	channelActivityFields...)

var channelsList = capability.Descriptor{
	ID:      Provider + ".channels.list",
	Version: 1,
	Title:   "List Infomaniak kChat channels",
	Description: "List the current token's channels of one team this connection is bound to, restricted to " +
		"this connection's channel allow-list when it has one, page by page, transparently",
	Tags:     []string{"infomaniak", "kchat", "channels", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channels":{"type":"array","items":` + channelListEntrySchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["team_id","channels","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of this team's channels; the first page when omitted"},
		{Name: "limit", Description: "Channels per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, channelListEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Total number of pages of this listing"},
		capability.Field{Name: "total", Description: "Total number of channels reported after the allow-list was applied"},
		capability.Field{Name: "count", Description: "Number of channels reported on this page"},
	),
	Examples: []capability.Example{{Description: "List the channels of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

// channelJSON is the subset of the kChat Channel resource this provider reads.
type channelJSON struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Header      string `json:"header"`
	CreateAt    int64  `json:"create_at"`
	DeleteAt    int64  `json:"delete_at"`

	UpdateAt      flexInt          `json:"update_at"`
	LastPostAt    flexInt          `json:"last_post_at"`
	TotalMsgCount flexInt          `json:"total_msg_count"`
	CreatorID     tolerant[string] `json:"creator_id"`
}

// ChannelEntry is the stable Qatlas view of one kChat channel.
type ChannelEntry struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Purpose     string `json:"purpose,omitempty"`
}

// ChannelActivity is what kChat reports about the life of a channel; a value kChat leaves out stays out.
type ChannelActivity struct {
	UpdatedAt     string `json:"updated_at,omitempty"`
	DeletedAt     string `json:"deleted_at,omitempty"`
	LastPostAt    string `json:"last_post_at,omitempty"`
	TotalMsgCount int64  `json:"total_msg_count,omitempty"`
	CreatorID     string `json:"creator_id,omitempty"`
}

// ChannelListEntry is the view of one channel in a listing or search.
type ChannelListEntry struct {
	ChannelEntry
	Header    string `json:"header,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ChannelActivity
}

// ChannelsPage is one paginated, allow-list-filtered listing of the channels of one team.
type ChannelsPage struct {
	TeamID   string             `json:"team_id"`
	Channels []ChannelListEntry `json:"channels"`
	Page     int                `json:"page"`
	Pages    int                `json:"pages"`
	Total    int                `json:"total"`
	Count    int                `json:"count"`
}

type channelsListArguments struct {
	TeamID string `json:"team_id"`
	Page   int    `json:"page"`
	Limit  int    `json:"limit"`
}

func invokeChannelsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input channelsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list channels", "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListChannels(ctx, input.TeamID, page, limit)
}

// ListChannels reads every channel of one bound team the current token is a member of, live against the
// instance, and pages the allow-list-filtered result itself: kChat's own listing answers as one complete
// array with no pagination of its own. A channel kChat reports under a team other than the one requested is
// dropped, a defence in depth against a team_id whose channels reach further than the request named.
func (c *Client) ListChannels(ctx context.Context, teamID string, page, limit int) (*ChannelsPage, error) {
	channels, err := c.teamChannels(ctx, "list channels", teamID)
	if err != nil {
		return nil, err
	}
	entries := make([]ChannelListEntry, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		if ch.TeamID != teamID || ch.DeleteAt != 0 || !c.scope.allowsChannel(ch.ID) {
			continue
		}
		entries = append(entries, listEntryOf(ch))
	}
	window, pages, total := windowOf(entries, page, limit)
	return &ChannelsPage{TeamID: teamID, Channels: window, Page: page, Pages: pages, Total: total, Count: len(window)}, nil
}

// teamChannels reads the channels of one team the token is a member of with exactly one request.
func (c *Client) teamChannels(ctx context.Context, op, teamID string) ([]channelJSON, error) {
	var channels []channelJSON
	path := "/api/v4/users/me/teams/" + url.PathEscape(teamID) + "/channels"
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &channels, false); err != nil {
		return nil, err
	}
	return channels, nil
}

// reachableChannels reads, with one request, the channels of one bound team that this connection may
// reach: live, in that team, inside the channel allow-list, and neither deleted nor a direct or group
// channel. The result maps the channel ID to its resource. It is the filter for every search and any other
// tool that must not return content from channels outside the connection's boundary.
func (c *Client) reachableChannels(ctx context.Context, op, teamID string) (map[string]*channelJSON, error) {
	channels, err := c.teamChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	reachable := make(map[string]*channelJSON, len(channels))
	for i := range channels {
		ch := &channels[i]
		if !validMattermostID(ch.ID) || ch.TeamID != teamID || ch.DeleteAt != 0 || ch.Type == "D" || ch.Type == "G" ||
			!c.scope.allowsChannel(ch.ID) {
			continue
		}
		reachable[ch.ID] = ch
	}
	return reachable, nil
}

// channelDetailSchema is the answer of channels.get.
var channelDetailSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"purpose":{"type":"string"},"header":{"type":"string"},` +
	`"member_count":{"type":"integer"},"created_at":{"type":"string"},"archived":{"type":"boolean"},` +
	channelActivityProperties + `},` +
	`"required":["id","team_id","name","display_name","type","member_count","archived"],` +
	`"additionalProperties":false}`

var channelDetailFields = append(append(append([]capability.Field{}, channelEntryFields...),
	capability.Field{Name: "header", Description: "Header text of the channel, untrusted data"},
	capability.Field{Name: "member_count", Description: "Number of members of the channel"},
	capability.Field{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC"},
	capability.Field{Name: "archived", Description: "True when the channel is archived"}),
	channelActivityFields...)

var channelsGet = capability.Descriptor{
	ID:      Provider + ".channels.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat channel",
	Description: "Read the details of exactly one channel of a team this connection is bound to, by channel_id " +
		"or by team_id and name, restricted to this connection's channel allow-list when it has one",
	Tags:     []string{"infomaniak", "kchat", "channels", "get"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"team_id":` +
		idSchema + `,"name":` + channelNameSchema + `,"include_deleted":{"type":"boolean"}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(channelDetailSchema),
	Arguments: []capability.Argument{
		{Name: "channel_id", Description: "Channel identifier; either this alone or team_id with name"},
		{Name: "team_id", Description: "Team identifier inside this connection's team targets; with name"},
		{Name: "name", Description: "URL-safe channel handle inside team_id; with team_id"},
		{Name: "include_deleted", Description: "With team_id and name only: also find an archived channel; refused " +
			"with channel_id, which reads archived channels anyway"},
	},
	Fields: channelDetailFields,
	Examples: []capability.Example{{Description: "Read one channel by its handle",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","name":"town-square"}`)}},
}

var channelsBrowse = capability.Descriptor{
	ID:      Provider + ".channels.browse",
	Version: 1,
	Title:   "Browse public Infomaniak kChat channels",
	Description: "List the public channels of one team this connection is bound to, whether or not the token is " +
		"a member, one kChat page at a time, restricted to this connection's channel allow-list when it has one",
	Tags:     []string{"infomaniak", "kchat", "channels", "browse"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channels":{"type":"array","items":` + channelListEntrySchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["team_id","channels","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Channels per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, channelListEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "per_page", Description: "Page size that was requested"},
		capability.Field{Name: "count", Description: "Number of channels reported on this page, after the allow-list"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
			"pages are never read on by themselves"},
	),
	Examples: []capability.Example{{Description: "Browse the public channels of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","per_page":100}`)}},
}

// ChannelDetail is the stable Qatlas view of one kChat channel with its header and statistics.
type ChannelDetail struct {
	ChannelEntry
	Header      string `json:"header,omitempty"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at,omitempty"`
	Archived    bool   `json:"archived"`
	ChannelActivity
}

// ChannelsBrowsePage is one kChat page of the public channels of one team, filtered by the allow-list.
type ChannelsBrowsePage struct {
	TeamID   string             `json:"team_id"`
	Channels []ChannelListEntry `json:"channels"`
	Page     int                `json:"page"`
	PerPage  int                `json:"per_page"`
	Count    int                `json:"count"`
	HasMore  bool               `json:"has_more"`
}

type channelsGetArguments struct {
	ChannelID string `json:"channel_id"`
	TeamID    string `json:"team_id"`
	Name      string `json:"name"`
	// IncludeDeleted is a pointer so that a given false is told apart from an omitted value.
	IncludeDeleted *bool `json:"include_deleted"`
}

func entryOf(ch *channelJSON) ChannelEntry {
	return ChannelEntry{
		ID: ch.ID, TeamID: ch.TeamID, Name: bounded(ch.Name), DisplayName: bounded(ch.DisplayName),
		Type: bounded(ch.Type), Purpose: bounded(ch.Purpose),
	}
}

// activityOf reports the activity members of a channel; absent values stay empty.
func activityOf(ch *channelJSON) ChannelActivity {
	return ChannelActivity{UpdatedAt: msToRFC3339(int64(ch.UpdateAt)), DeletedAt: msToRFC3339(ch.DeleteAt),
		LastPostAt: msToRFC3339(int64(ch.LastPostAt)), TotalMsgCount: int64(ch.TotalMsgCount),
		CreatorID: cleanText(ch.CreatorID.Value)}
}

func listEntryOf(ch *channelJSON) ChannelListEntry {
	return ChannelListEntry{ChannelEntry: entryOf(ch), Header: cleanText(ch.Header),
		CreatedAt: msToRFC3339(ch.CreateAt), ChannelActivity: activityOf(ch)}
}

func invokeChannelsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input channelsGetArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get channel", "the validated arguments could not be read")
	}
	byID := input.ChannelID != ""
	byName := input.TeamID != "" || input.Name != ""
	if byID == byName || (byName && (input.TeamID == "" || input.Name == "")) {
		return nil, invalidRequest("give exactly one of channel_id, or team_id together with name")
	}
	if byID && input.IncludeDeleted != nil {
		return nil, invalidRequest("include_deleted needs team_id together with name; a channel_id reads archived channels anyway")
	}
	if byID {
		if err := selectChannel(resolved, input.ChannelID); err != nil {
			return nil, err
		}
	} else {
		if err := selectTeam(resolved, input.TeamID); err != nil {
			return nil, err
		}
		if !channelNamePattern.MatchString(input.Name) {
			return nil, invalidRequest("name must be a channel handle of lowercase letters, digits, hyphen, and underscore")
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetChannel(ctx, input.ChannelID, input.TeamID, input.Name, input.IncludeDeleted != nil && *input.IncludeDeleted)
}

// GetChannel reads one channel and its statistics. Whatever form named it, the channel kChat answers with
// must belong to a bound team and be inside the channel allow-list before anything is returned, an archived
// one included. includeDeleted applies to the handle form only.
func (c *Client) GetChannel(ctx context.Context, channelID, teamID, name string, includeDeleted bool) (*ChannelDetail, error) {
	const op = "get channel"
	var ch channelJSON
	var query url.Values
	path := "/api/v4/channels/" + url.PathEscape(channelID)
	if channelID == "" {
		path = "/api/v4/teams/" + url.PathEscape(teamID) + "/channels/name/" + url.PathEscape(name)
		if includeDeleted {
			query = url.Values{"include_deleted": {"true"}}
		}
	}
	if err := c.do(ctx, op, http.MethodGet, path, query, nil, &ch, false); err != nil {
		return nil, err
	}
	if !validMattermostID(ch.ID) || (channelID != "" && ch.ID != channelID) || (teamID != "" && ch.TeamID != teamID) ||
		!c.scope.allowsTeam(ch.TeamID) || !c.scope.allowsChannel(ch.ID) {
		return nil, invalidRequest("the channel is outside the targets of this connection")
	}
	var stats struct {
		ChannelID   string `json:"channel_id"`
		MemberCount int    `json:"member_count"`
	}
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(ch.ID)+"/stats", nil, nil, &stats,
		false); err != nil {
		return nil, err
	}
	if stats.ChannelID != ch.ID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "kChat returned an invalid response"}
	}
	return &ChannelDetail{ChannelEntry: entryOf(&ch), Header: cleanText(ch.Header), MemberCount: stats.MemberCount,
		CreatedAt: msToRFC3339(ch.CreateAt), Archived: ch.DeleteAt != 0, ChannelActivity: activityOf(&ch)}, nil
}

type channelsBrowseArguments struct {
	TeamID  string `json:"team_id"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}

func invokeChannelsBrowse(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input channelsBrowseArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("browse channels", "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.BrowseChannels(ctx, input.TeamID, input.Page, input.PerPage)
}

// BrowseChannels reads exactly one page of kChat's own listing of the public channels of one bound team and
// never reads on by itself. A channel under another team, an archived one, a non-public one, or one outside
// the channel allow-list is dropped.
func (c *Client) BrowseChannels(ctx context.Context, teamID string, page, perPage int) (*ChannelsBrowsePage, error) {
	var channels []channelJSON
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}}
	if err := c.do(ctx, "browse channels", http.MethodGet, "/api/v4/teams/"+url.PathEscape(teamID)+"/channels", query,
		nil, &channels, false); err != nil {
		return nil, err
	}
	entries := make([]ChannelListEntry, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		if ch.TeamID != teamID || ch.Type != "O" || ch.DeleteAt != 0 || !validMattermostID(ch.ID) ||
			!c.scope.allowsChannel(ch.ID) {
			continue
		}
		entries = append(entries, listEntryOf(ch))
	}
	return &ChannelsBrowsePage{TeamID: teamID, Channels: entries, Page: page, PerPage: perPage, Count: len(entries),
		HasMore: len(channels) >= perPage}, nil
}
