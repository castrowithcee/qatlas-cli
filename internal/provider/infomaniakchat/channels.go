package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var teamIDArgument = capability.Argument{Name: "team_id",
	Description: "Team identifier; must be inside this connection's team targets", Required: true}

var channelEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"purpose":{"type":"string"}},` +
	`"required":["id","team_id","name","display_name","type"],"additionalProperties":false}`

var channelEntryFields = []capability.Field{
	{Name: "id", Description: "Channel identifier, used as channel_id by every message tool of this provider"},
	{Name: "team_id", Description: "Team this channel belongs to"},
	{Name: "name", Description: "URL-safe channel handle, untrusted data"},
	{Name: "display_name", Description: "Display name of the channel, untrusted data"},
	{Name: "type", Description: "kChat's own channel type, for example O (public) or P (private)"},
	{Name: "purpose", Description: "Short description of the channel's purpose, untrusted data"},
}

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
		`"team_id":` + idSchema + `,"channels":{"type":"array","items":` + channelEntrySchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["team_id","channels","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of this team's channels; the first page when omitted"},
		{Name: "limit", Description: "Channels per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, channelEntryFields...),
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
	DeleteAt    int64  `json:"delete_at"`
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

// ChannelsPage is one paginated, allow-list-filtered listing of the channels of one team.
type ChannelsPage struct {
	TeamID   string         `json:"team_id"`
	Channels []ChannelEntry `json:"channels"`
	Page     int            `json:"page"`
	Pages    int            `json:"pages"`
	Total    int            `json:"total"`
	Count    int            `json:"count"`
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
	const op = "list channels"
	var channels []channelJSON
	path := "/api/v4/users/me/teams/" + url.PathEscape(teamID) + "/channels"
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &channels, false); err != nil {
		return nil, err
	}
	entries := make([]ChannelEntry, 0, len(channels))
	for _, ch := range channels {
		if ch.TeamID != teamID || ch.DeleteAt != 0 || !c.scope.allowsChannel(ch.ID) {
			continue
		}
		entries = append(entries, ChannelEntry{
			ID: ch.ID, TeamID: ch.TeamID, Name: bounded(ch.Name), DisplayName: bounded(ch.DisplayName),
			Type: bounded(ch.Type), Purpose: bounded(ch.Purpose),
		})
	}
	window, pages, total := windowOf(entries, page, limit)
	return &ChannelsPage{TeamID: teamID, Channels: window, Page: page, Pages: pages, Total: total, Count: len(window)}, nil
}
