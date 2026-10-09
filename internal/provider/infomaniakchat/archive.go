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

// The suffixes of an archive, restore, and visibility request whose result is unclear.
const (
	uncertainChannelArchive = "; the channel may have been archived, read it before archiving it again"
	uncertainChannelRestore = "; the channel may have been restored, read it before restoring it again"
	uncertainChannelPrivacy = "; the visibility may have been changed, read the channel before changing it again"
)

var archivedChannelsList = capability.Descriptor{
	ID:      Provider + ".archivedchannels.list",
	Version: 1,
	Title:   "List archived Infomaniak kChat channels",
	Description: "List the archived public and private channels of one team this connection is bound to, one " +
		"kChat page at a time, restricted to this connection's channel allow-list when it has one; direct and " +
		"group channels are not reachable",
	Tags:     []string{"infomaniak", "kchat", "channels", "archived", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channels":{"type":"array","items":` + channelEntrySchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["team_id","channels","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Channels per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, channelEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "per_page", Description: "Page size that was requested"},
		capability.Field{Name: "count", Description: "Number of channels reported on this page, after the allow-list"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
			"pages are never read on by themselves"},
	),
	Examples: []capability.Example{{Description: "List the archived channels of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var channelsArchive = capability.Descriptor{
	ID:      Provider + ".channels.archive",
	Version: 1,
	Title:   "Archive an Infomaniak kChat channel",
	Description: "Archive exactly one active public or private channel this connection may reach with one " +
		"confirmed request; the channel can be restored while kChat keeps it. kChat refuses its default channel",
	Tags:                  []string{"infomaniak", "kchat", "channels", "archive", "delete"},
	Risk:                  channelChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"archived":{"type":"boolean"}},"required":["channel_id","archived"],"additionalProperties":false}`),
	Arguments: []capability.Argument{channelIDArgument},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel that was archived"},
		{Name: "archived", Description: "True once kChat confirmed the archiving"},
	},
	Examples: []capability.Example{{Description: "Archive one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000"}`)}},
}

var channelsRestore = capability.Descriptor{
	ID:      Provider + ".channels.restore",
	Version: 1,
	Title:   "Restore an archived Infomaniak kChat channel",
	Description: "Restore exactly one archived public or private channel this connection may reach with one " +
		"confirmed request; its members and messages become reachable again",
	Tags:                  []string{"infomaniak", "kchat", "channels", "restore", "archive"},
	Risk:                  channelChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(channelWrittenSchema),
	Arguments:    []capability.Argument{channelIDArgument},
	Fields:       channelWrittenFields,
	Examples: []capability.Example{{Description: "Restore one archived channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000"}`)}},
}

var channelsPrivacy = capability.Descriptor{
	ID:      Provider + ".channels.privacy",
	Version: 1,
	Title:   "Change the visibility of an Infomaniak kChat channel",
	Description: "Make exactly one active channel this connection may reach public (O) or private (P) with one " +
		"confirmed request. Making a private channel public lets every member of the instance's team join it",
	Tags:                  []string{"infomaniak", "kchat", "channels", "privacy", "permissions"},
	Risk:                  channelChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"privacy":{"type":"string","enum":["O","P"]}},"required":["channel_id","privacy"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(channelWrittenSchema),
	Arguments: []capability.Argument{channelIDArgument,
		{Name: "privacy", Description: "O for a public channel, P for a private one", Required: true}},
	Fields: channelWrittenFields,
	Examples: []capability.Example{{Description: "Make one channel private",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","privacy":"P"}`)}},
}

// ChannelArchived is the answer of one confirmed archiving.
type ChannelArchived struct {
	ChannelID string `json:"channel_id"`
	Archived  bool   `json:"archived"`
}

type archivedListArguments struct {
	TeamID  string `json:"team_id"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}

func invokeArchivedChannelsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input archivedListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list archived channels", "the validated arguments could not be read")
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
	return client.ListArchivedChannels(ctx, input.TeamID, input.Page, input.PerPage)
}

// ListArchivedChannels reads exactly one page of kChat's own listing of the archived channels of one bound
// team and never reads on by itself. A channel under another team, an active one, a direct or group one, or
// one outside the channel allow-list is dropped.
func (c *Client) ListArchivedChannels(ctx context.Context, teamID string, page, perPage int) (*ChannelsBrowsePage, error) {
	var channels []channelJSON
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}}
	if err := c.do(ctx, "list archived channels", http.MethodGet, "/api/v4/teams/"+url.PathEscape(teamID)+"/channels/deleted",
		query, nil, &channels, false); err != nil {
		return nil, err
	}
	entries := make([]ChannelEntry, 0, len(channels))
	for i := range channels {
		ch := &channels[i]
		if ch.TeamID != teamID || ch.DeleteAt == 0 || (ch.Type != "O" && ch.Type != "P") || !validMattermostID(ch.ID) ||
			!c.scope.allowsChannel(ch.ID) {
			continue
		}
		entries = append(entries, entryOf(ch))
	}
	return &ChannelsBrowsePage{TeamID: teamID, Channels: entries, Page: page, PerPage: perPage, Count: len(entries),
		HasMore: len(channels) >= perPage}, nil
}

type channelIDArguments struct {
	ChannelID string `json:"channel_id"`
}

type channelPrivacyArguments struct {
	ChannelID string `json:"channel_id"`
	Privacy   string `json:"privacy"`
}

func invokeChannelsArchive(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "archive channel"
	var input channelIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, ch, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.DeleteAt != 0 {
		return nil, invalidRequest("only an active public or private channel can be archived")
	}
	return client.ArchiveChannel(ctx, ch.ID)
}

// ArchiveChannel sends exactly one DELETE, which kChat treats as archiving, and never repeats it: a failure
// after the request may have reached kChat says so instead.
func (c *Client) ArchiveChannel(ctx context.Context, channelID string) (*ChannelArchived, error) {
	const op = "archive channel"
	if err := c.doWith(ctx, op, http.MethodDelete, "/api/v4/channels/"+url.PathEscape(channelID), nil, nil, nil,
		uncertainChannelArchive); err != nil {
		return nil, err
	}
	return &ChannelArchived{ChannelID: channelID, Archived: true}, nil
}

func invokeChannelsRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore channel"
	var input channelIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, ch, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.DeleteAt == 0 {
		return nil, invalidRequest("only an archived public or private channel can be restored")
	}
	return client.changeChannel(ctx, op, http.MethodPost, ch, "/restore", nil, uncertainChannelRestore)
}

func invokeChannelsPrivacy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "change channel visibility"
	var input channelPrivacyArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Privacy != "O" && input.Privacy != "P" {
		return nil, invalidRequest("privacy must be O (public) or P (private)")
	}
	client, ch, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.DeleteAt != 0 {
		return nil, invalidRequest("only an active public or private channel can change its visibility")
	}
	return client.changeChannel(ctx, op, http.MethodPut, ch, "/privacy", map[string]string{"privacy": input.Privacy},
		uncertainChannelPrivacy)
}

// changeChannel sends exactly one request to a fixed sub-path of the channel and never repeats it. kChat
// answers with the changed channel, which must be the one that was bound.
func (c *Client) changeChannel(ctx context.Context, op, method string, current *channelJSON, suffix string, body any,
	uncertain string) (*ChannelWritten, error) {
	var ch channelJSON
	if err := c.doWith(ctx, op, method, "/api/v4/channels/"+url.PathEscape(current.ID)+suffix, nil, body, &ch,
		uncertain); err != nil {
		return nil, err
	}
	if ch.ID != current.ID || ch.TeamID != current.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertain}
	}
	return writtenOf(&ch), nil
}
