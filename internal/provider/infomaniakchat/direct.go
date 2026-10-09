package infomaniakchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// kChat's own channel types of a direct and a group message channel; neither belongs to a team.
const (
	channelDirect = "D"
	channelGroup  = "G"
)

const (
	// maxGroupMembers is kChat's own upper bound of a group channel, the token's own user included.
	maxGroupMembers = 8
	minGroupOthers  = 2
	maxGroupOthers  = maxGroupMembers - 1
	// maxDirectListLimit bounds the channels one direct.list page proves, because every group channel on it
	// costs one member read.
	maxDirectListLimit     = 50
	defaultDirectListLimit = 20
	allowListOpenRefusal   = "this connection has a channel allow-list, so it opens no direct or group " +
		"channel; add the channel as a channel target and address it by channel_id instead"
)

func openRisk() capability.Risk {
	return capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var openedChannelSchema = `{"type":"object","properties":{"id":` + idSchema + `,"type":{"type":"string"},` +
	`"user_ids":{"type":"array","items":` + idSchema + `}},"required":["id","type","user_ids"],` +
	`"additionalProperties":false}`

var openedChannelFields = []capability.Field{
	{Name: "id", Description: "Channel identifier, used as channel_id by every message tool of this provider"},
	{Name: "type", Description: "D for a direct channel, G for a group channel"},
	{Name: "user_ids", Description: "Participants besides the token's own user"},
}

var directOpen = capability.Descriptor{
	ID:      Provider + ".direct.open",
	Version: 1,
	Title:   "Open an Infomaniak kChat direct channel",
	Description: "Open, after confirmation, the direct message channel between the token's own user and one " +
		"member of a bound team, or return the existing one; send to it with messages.send",
	Tags:     []string{"infomaniak", "kchat", "direct", "messages", "open"},
	Risk:     openRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `},` +
		`"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(openedChannelSchema),
	Arguments: []capability.Argument{{Name: "user_id", Required: true,
		Description: "Other user; must be a live member of a team this connection is bound to"}},
	Fields: openedChannelFields,
	Examples: []capability.Example{{Description: "Open the direct channel with one team member",
		Arguments: json.RawMessage(`{"user_id":"abc123user0000000000000000a"}`)}},
}

var groupMessagesOpen = capability.Descriptor{
	ID:      Provider + ".groupmessages.open",
	Version: 1,
	Title:   "Open an Infomaniak kChat group channel",
	Description: "Open, after confirmation, the group message channel of the token's own user and " +
		itoa(minGroupOthers) + " to " + itoa(maxGroupOthers) + " members of bound teams, or return the " +
		"existing one; send to it with messages.send",
	Tags:     []string{"infomaniak", "kchat", "groupmessages", "messages", "open"},
	Risk:     openRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_ids":{"type":"array","minItems":` +
		itoa(minGroupOthers) + `,"maxItems":` + itoa(maxGroupOthers) + `,"items":` + idSchema + `}},` +
		`"required":["user_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(openedChannelSchema),
	Arguments: []capability.Argument{{Name: "user_ids", Required: true,
		Description: itoa(minGroupOthers) + " to " + itoa(maxGroupOthers) + " other users, each a live member " +
			"of a team this connection is bound to; repeated identifiers count once"}},
	Fields: openedChannelFields,
	Examples: []capability.Example{{Description: "Open the group channel with two team members",
		Arguments: json.RawMessage(`{"user_ids":["abc123user0000000000000000a","abc123user0000000000000000b"]}`)}},
}

var directList = capability.Descriptor{
	ID:      Provider + ".direct.list",
	Version: 1,
	Title:   "List Infomaniak kChat direct and group channels",
	Description: "List the token's direct and group message channels kChat reports for one bound team, one page " +
		"at a time; a channel with a participant outside the bound teams is left out",
	Tags:     []string{"infomaniak", "kchat", "direct", "groupmessages", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxDirectListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"channels":{"type":"array","items":{"type":"object","properties":{"id":` + idSchema + `,` +
		`"type":{"type":"string"},"display_name":{"type":"string"},"user_ids":{"type":"array","items":` +
		idSchema + `}},"required":["id","type","user_ids"],"additionalProperties":false}},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["team_id","channels","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of the channels; the first page when omitted"},
		{Name: "limit", Description: "Channels checked per page, 1 to " + itoa(maxDirectListLimit) + "; " +
			itoa(defaultDirectListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, openedChannelFields...),
		capability.Field{Name: "display_name", Description: "Display name kChat reports, untrusted data"},
		capability.Field{Name: "team_id", Description: "Team the listing was read through"},
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "has_more", Description: "True when a further page remains"},
		capability.Field{Name: "count", Description: "Number of channels reported on this page, after channels " +
			"outside the bound teams were left out"},
	),
	Examples: []capability.Example{{Description: "List the direct and group channels",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

// OpenedChannel is the answer of one confirmed direct or group channel open.
type OpenedChannel struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	UserIDs []string `json:"user_ids"`
}

// DirectEntry is the stable Qatlas view of one direct or group channel.
type DirectEntry struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	DisplayName string   `json:"display_name,omitempty"`
	UserIDs     []string `json:"user_ids"`
}

// DirectPage is one page of direct and group channels.
type DirectPage struct {
	TeamID   string        `json:"team_id"`
	Channels []DirectEntry `json:"channels"`
	Page     int           `json:"page"`
	HasMore  bool          `json:"has_more"`
	Count    int           `json:"count"`
}

// directPair reads the two user IDs of a direct channel name, kChat's own "idA__idB".
func directPair(name string) (string, string, bool) {
	a, b, found := strings.Cut(name, "__")
	if !found || a == b || !validMattermostID(a) || !validMattermostID(b) {
		return "", "", false
	}
	return a, b, true
}

// participants returns the users of a direct or group channel besides the token's own user. ok is false when
// they cannot be proven: a direct channel name without the own user exactly once, or a group channel with
// more than maxGroupMembers members, an unreadable member entry, or without the own user. err is a failed
// request.
func (c *Client) participants(ctx context.Context, op string, ch channelJSON) (others []string, ok bool, err error) {
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, false, err
	}
	if ch.Type == channelDirect {
		a, b, valid := directPair(ch.Name)
		switch {
		case !valid:
			return nil, false, nil
		case a == own:
			return []string{b}, true, nil
		case b == own:
			return []string{a}, true, nil
		}
		return nil, false, nil
	}
	var members []struct {
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
	}
	// One member more than allowed is enough to refuse an oversized group; no further page is read.
	query := url.Values{"page": {"0"}, "per_page": {strconv.Itoa(maxGroupMembers + 1)}}
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(ch.ID)+"/members", query, nil,
		&members, false); err != nil {
		return nil, false, err
	}
	if len(members) > maxGroupMembers {
		return nil, false, nil
	}
	seen, hasOwn := map[string]bool{}, false
	for _, m := range members {
		if m.ChannelID != ch.ID || !validMattermostID(m.UserID) || seen[m.UserID] {
			return nil, false, nil
		}
		seen[m.UserID] = true
		if m.UserID == own {
			hasOwn = true
			continue
		}
		others = append(others, m.UserID)
	}
	if !hasOwn || len(others) == 0 {
		return nil, false, nil
	}
	return others, true, nil
}

// refuseWithAllowList refuses an open locally: a channel allow-list names every reachable direct or group
// channel already, and an opened channel could fall outside it.
func refuseWithAllowList(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.channels) > 0 {
		return invalidRequest(allowListOpenRefusal)
	}
	return nil
}

type directOpenArguments struct {
	UserID string `json:"user_id"`
}

func invokeDirectOpen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "open direct channel"
	var input directOpenArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := refuseWithAllowList(resolved); err != nil {
		return nil, err
	}
	if !validMattermostID(input.UserID) {
		return nil, invalidRequest("user_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.OpenDirect(ctx, input.UserID)
}

// OpenDirect proves the other user, then sends exactly one POST and never repeats it. The answer must be a
// direct channel of exactly the own user and userID.
func (c *Client) OpenDirect(ctx context.Context, userID string) (*OpenedChannel, error) {
	const op = "open direct channel"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	if userID == own {
		return nil, invalidRequest("user_id must name another user than the token's own")
	}
	if err := c.requireUsers(ctx, op, []string{userID}); err != nil {
		return nil, err
	}
	var ch channelJSON
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/channels/direct", nil, []string{own, userID}, &ch,
		uncertainOpen); err != nil {
		return nil, err
	}
	a, b, valid := directPair(ch.Name)
	if ch.Type != channelDirect || ch.TeamID != "" || !validMattermostID(ch.ID) || !valid ||
		!((a == own && b == userID) || (a == userID && b == own)) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainOpen}
	}
	return &OpenedChannel{ID: ch.ID, Type: channelDirect, UserIDs: []string{userID}}, nil
}

type groupMessagesOpenArguments struct {
	UserIDs []string `json:"user_ids"`
}

func invokeGroupMessagesOpen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "open group channel"
	var input groupMessagesOpenArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := refuseWithAllowList(resolved); err != nil {
		return nil, err
	}
	for _, id := range input.UserIDs {
		if !validMattermostID(id) {
			return nil, invalidRequest("user_ids must be kChat-style identifiers")
		}
	}
	ids := uniqueStrings(input.UserIDs)
	if len(ids) < minGroupOthers || len(ids) > maxGroupOthers {
		return nil, invalidRequest(groupCountRefusal)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.OpenGroup(ctx, ids)
}

var groupCountRefusal = "user_ids must name " + itoa(minGroupOthers) + " to " + itoa(maxGroupOthers) +
	" distinct users besides the token's own"

// OpenGroup proves every other user, then sends exactly one POST and never repeats it. The own user is
// named once, first, whether or not ids contains it.
func (c *Client) OpenGroup(ctx context.Context, ids []string) (*OpenedChannel, error) {
	const op = "open group channel"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	others := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != own {
			others = append(others, id)
		}
	}
	if len(others) < minGroupOthers {
		return nil, invalidRequest(groupCountRefusal)
	}
	if err := c.requireUsers(ctx, op, others); err != nil {
		return nil, err
	}
	var ch channelJSON
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/channels/group", nil, append([]string{own}, others...),
		&ch, uncertainOpen); err != nil {
		return nil, err
	}
	if ch.Type != channelGroup || ch.TeamID != "" || !validMattermostID(ch.ID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainOpen}
	}
	return &OpenedChannel{ID: ch.ID, Type: channelGroup, UserIDs: others}, nil
}

type directListArguments struct {
	TeamID string `json:"team_id"`
	Page   int    `json:"page"`
	Limit  int    `json:"limit"`
}

func invokeDirectList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input directListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list direct channels", "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultDirectListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListDirect(ctx, input.TeamID, page, limit)
}

// ListDirect reads the token's channels of one bound team, keeps the direct and group channels inside the
// channel allow-list, and proves only the requested page of them: the participants of every direct channel
// on it in one batched user check, every group channel with one member read. A channel that fails the proof
// is left out, so count may be lower than limit; has_more reports a further page of unproven candidates.
func (c *Client) ListDirect(ctx context.Context, teamID string, page, limit int) (*DirectPage, error) {
	const op = "list direct channels"
	var channels []channelJSON
	path := "/api/v4/users/me/teams/" + url.PathEscape(teamID) + "/channels"
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &channels, false); err != nil {
		return nil, err
	}
	candidates := make([]channelJSON, 0, len(channels))
	for _, ch := range channels {
		if (ch.Type != channelDirect && ch.Type != channelGroup) || ch.TeamID != "" || ch.DeleteAt != 0 ||
			!validMattermostID(ch.ID) || !c.scope.allowsChannel(ch.ID) {
			continue
		}
		candidates = append(candidates, ch)
	}
	window, pages, _ := windowOf(candidates, page, limit)
	proven := make([][]string, len(window))
	var all []string
	for i, ch := range window {
		others, ok, err := c.participants(ctx, op, ch)
		if err != nil {
			// A group whose members this token may not read cannot be proven; it is left out, not fatal.
			var failure *provider.Error
			if errors.As(err, &failure) && (failure.Class == provider.ClassNotFound ||
				failure.Class == provider.ClassPermission) {
				continue
			}
			return nil, err
		}
		if ok {
			proven[i] = others
			all = append(all, others...)
		}
	}
	allowed, err := c.verifyUsersScope(ctx, op, all)
	if err != nil {
		return nil, err
	}
	reachable := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		reachable[id] = true
	}
	entries := make([]DirectEntry, 0, len(window))
	for i, ch := range window {
		if len(proven[i]) == 0 {
			continue
		}
		inScope := true
		for _, id := range proven[i] {
			inScope = inScope && reachable[id]
		}
		if inScope {
			entries = append(entries, DirectEntry{ID: ch.ID, Type: ch.Type, DisplayName: bounded(ch.DisplayName),
				UserIDs: proven[i]})
		}
	}
	return &DirectPage{TeamID: teamID, Channels: entries, Page: page, HasMore: page < pages, Count: len(entries)}, nil
}
