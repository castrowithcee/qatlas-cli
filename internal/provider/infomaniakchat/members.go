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

const (
	maxAddMembers = 20

	roleMember = "channel_user"
	roleAdmin  = "channel_user channel_admin"
)

// The suffixes of a membership change whose result is unclear.
const (
	uncertainMemberAdd    = "; the members may have been added, list the members before adding them again"
	uncertainMemberRemove = "; the member may have been removed, list the members before removing them again"
	uncertainMemberRoles  = "; the role may have been changed, list the members before changing it again"
)

const membersOutOfScope = "every user_id must be the token's own user or a current member of the team of the channel"

var userIDArgument = capability.Argument{Name: "user_id", Required: true, Description: "User identifier"}

func memberChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var memberEntrySchema = `{"type":"object","properties":{` +
	`"user_id":` + idSchema + `,"roles":{"type":"string"},"scheme_admin":{"type":"boolean"}},` +
	`"required":["user_id","roles","scheme_admin"],"additionalProperties":false}`

var channelsMembersList = capability.Descriptor{
	ID:      Provider + ".channelmembers.list",
	Version: 1,
	Title:   "List Infomaniak kChat channel members",
	Description: "List the members of one public or private channel this connection may reach, one page at a " +
		"time; each entry names the user and the channel role. Direct and group channels are not reachable",
	Tags:     []string{"infomaniak", "kchat", "channels", "members", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"members":{"type":"array","items":` + memberEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["channel_id","members","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "channel_id", Description: "Channel identifier", Required: true},
		{Name: "page", Description: "1-based page of the members; the first page when omitted"},
		{Name: "limit", Description: "Members per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "Member user identifier"},
		{Name: "roles", Description: "Channel roles of the member, for example channel_user or channel_user channel_admin"},
		{Name: "scheme_admin", Description: "True when the member is a channel administrator by the channel's permission scheme"},
		{Name: "channel_id", Description: "Channel that was read"},
		{Name: "page", Description: "Page that was read"},
		{Name: "has_more", Description: "True when the page is full, so a further page may remain"},
		{Name: "count", Description: "Number of members reported on this page"},
	},
	Examples: []capability.Example{{Description: "List the members of one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000"}`)}},
}

var channelsMembersAdd = capability.Descriptor{
	ID:      Provider + ".channelmembers.add",
	Version: 1,
	Title:   "Add Infomaniak kChat channel members",
	Description: "Add 1 to " + itoa(maxAddMembers) + " users with one confirmed request to one public or private " +
		"channel this connection may reach. Each user must be the token's own user or a current member of the " +
		"channel's team, so nobody gains access to the instance; kChat notifies the added users. Adding an " +
		"existing member changes nothing",
	Tags:     []string{"infomaniak", "kchat", "channels", "members", "add"},
	Risk:     memberChangeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"user_ids":` +
		`{"type":"array","minItems":1,"maxItems":` + itoa(maxAddMembers) + `,"uniqueItems":true,"items":` + idSchema + `}},` +
		`"required":["channel_id","user_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"user_ids":{"type":"array","items":` + idSchema + `},"count":{"type":"integer"}},` +
		`"required":["channel_id","user_ids","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "channel_id", Description: "Channel identifier", Required: true},
		{Name: "user_ids", Description: "1 to " + itoa(maxAddMembers) + " user identifiers", Required: true},
	},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel the users were added to"},
		{Name: "user_ids", Description: "Users kChat confirmed as members"},
		{Name: "count", Description: "Number of confirmed members"},
	},
	Examples: []capability.Example{{Description: "Add one team member to a channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","user_ids":["abc123user0000000000000000"]}`)}},
}

var channelsMembersRemove = capability.Descriptor{
	ID:      Provider + ".channelmembers.remove",
	Version: 1,
	Title:   "Remove an Infomaniak kChat channel member",
	Description: "Remove exactly one confirmed user from one public or private channel this connection may " +
		"reach; the user stays in the team and can be added again",
	Tags:                  []string{"infomaniak", "kchat", "channels", "members", "remove", "delete"},
	Risk:                  memberChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"user_id":` +
		idSchema + `},"required":["channel_id","user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"user_id":` +
		idSchema + `,"removed":{"type":"boolean"}},"required":["channel_id","user_id","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "channel_id", Description: "Channel identifier", Required: true}, userIDArgument},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel the user was removed from"},
		{Name: "user_id", Description: "Removed user"},
		{Name: "removed", Description: "True once kChat confirmed the removal"},
	},
	Examples: []capability.Example{{Description: "Remove one member from a channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","user_id":"abc123user0000000000000000"}`)}},
}

var channelsMembersRoles = capability.Descriptor{
	ID:      Provider + ".channelmembers.roles",
	Version: 1,
	Title:   "Change an Infomaniak kChat channel role",
	Description: "Set the channel role of exactly one member of one public or private channel this connection " +
		"may reach with one confirmed request: channel_user for a plain member, or channel_user channel_admin " +
		"for a channel administrator. This changes the member's rights",
	Tags:                  []string{"infomaniak", "kchat", "channels", "members", "roles", "permissions"},
	Risk:                  memberChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"user_id":` +
		idSchema + `,"roles":{"type":"string","enum":["` + roleMember + `","` + roleAdmin + `"]}},` +
		`"required":["channel_id","user_id","roles"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"user_id":` +
		idSchema + `,"roles":{"type":"string"}},"required":["channel_id","user_id","roles"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "channel_id", Description: "Channel identifier", Required: true},
		userIDArgument,
		{Name: "roles", Description: "channel_user or channel_user channel_admin", Required: true}},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel of the member"},
		{Name: "user_id", Description: "Member whose role was set"},
		{Name: "roles", Description: "Role that kChat confirmed"},
	},
	Examples: []capability.Example{{Description: "Make a member a channel administrator",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","user_id":"abc123user0000000000000000",` +
			`"roles":"channel_user channel_admin"}`)}},
}

type memberJSON struct {
	ChannelID   string `json:"channel_id"`
	UserID      string `json:"user_id"`
	Roles       string `json:"roles"`
	SchemeAdmin bool   `json:"scheme_admin"`
}

// MemberEntry is the stable Qatlas view of one channel member; notification settings and counters are
// never passed on.
type MemberEntry struct {
	UserID      string `json:"user_id"`
	Roles       string `json:"roles"`
	SchemeAdmin bool   `json:"scheme_admin"`
}

// MembersPage is one paginated listing of the members of one channel.
type MembersPage struct {
	ChannelID string        `json:"channel_id"`
	Members   []MemberEntry `json:"members"`
	Page      int           `json:"page"`
	HasMore   bool          `json:"has_more"`
	Count     int           `json:"count"`
}

// MembersAdded is the answer of one confirmed addition.
type MembersAdded struct {
	ChannelID string   `json:"channel_id"`
	UserIDs   []string `json:"user_ids"`
	Count     int      `json:"count"`
}

// MemberRemoved is the answer of one confirmed removal.
type MemberRemoved struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Removed   bool   `json:"removed"`
}

// MemberRoles is the answer of one confirmed role change.
type MemberRoles struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Roles     string `json:"roles"`
}

type membersListArguments struct {
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

type membersAddArguments struct {
	ChannelID string   `json:"channel_id"`
	UserIDs   []string `json:"user_ids"`
}

type memberArguments struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Roles     string `json:"roles"`
}

// openMemberChannel binds a channel for a membership operation: locally against the allow-list before any
// secret is read, then live. Unlike verifyChannelScope it never admits a direct or group channel.
func openMemberChannel(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, op, channelID string) (*Client, *channelJSON, error) {
	if err := selectChannel(resolved, channelID); err != nil {
		return nil, nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	ch, err := client.boundChannel(ctx, op, channelID)
	if err != nil {
		return nil, nil, err
	}
	if ch.Type != "O" && ch.Type != "P" {
		return nil, nil, invalidRequest(channelOutOfScope)
	}
	return client, ch, nil
}

func invokeChannelsMembersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list channel members"
	var input membersListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, _, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	return client.ListMembers(ctx, input.ChannelID, page, limit)
}

// ListMembers reads one page of a channel's members exactly as kChat paginates them and never follows a
// further page itself.
func (c *Client) ListMembers(ctx context.Context, channelID string, page, limit int) (*MembersPage, error) {
	const op = "list channel members"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	var members []memberJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID)+"/members", query, nil,
		&members, false); err != nil {
		return nil, err
	}
	entries := make([]MemberEntry, 0, len(members))
	for _, m := range members {
		if m.ChannelID != channelID || !validMattermostID(m.UserID) {
			continue
		}
		entries = append(entries, MemberEntry{UserID: m.UserID, Roles: bounded(m.Roles), SchemeAdmin: m.SchemeAdmin})
	}
	return &MembersPage{ChannelID: channelID, Members: entries, Page: page, HasMore: len(members) >= limit,
		Count: len(entries)}, nil
}

func invokeChannelsMembersAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add channel members"
	var input membersAddArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if len(input.UserIDs) < 1 || len(input.UserIDs) > maxAddMembers {
		return nil, invalidRequest("user_ids must name 1 to " + itoa(maxAddMembers) + " users")
	}
	unique := make([]string, 0, len(input.UserIDs))
	seen := map[string]bool{}
	for _, id := range input.UserIDs {
		if !validMattermostID(id) {
			return nil, invalidRequest("user_id must be a kChat-style identifier")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	client, ch, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if _, err := client.ownUserID(ctx, op); err != nil {
		return nil, err
	}
	// Only the channel's own team counts, not every bound team, so a user of another bound team is no member
	// of this channel's team.
	scoped := *client
	scoped.scope = scope{teams: []string{ch.TeamID}}
	allowed, err := scoped.verifyUsersScope(ctx, op, unique)
	if err != nil {
		return nil, err
	}
	if len(allowed) != len(unique) {
		return nil, invalidRequest(membersOutOfScope)
	}
	return client.AddMembers(ctx, input.ChannelID, unique)
}

// AddMembers sends exactly one POST built from the typed user IDs and never repeats it: a failure after the
// request may have reached kChat says so instead.
func (c *Client) AddMembers(ctx context.Context, channelID string, userIDs []string) (*MembersAdded, error) {
	const op = "add channel members"
	var answer json.RawMessage
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/channels/"+url.PathEscape(channelID)+"/members", nil,
		map[string][]string{"user_ids": userIDs}, &answer, uncertainMemberAdd); err != nil {
		return nil, err
	}
	var members []memberJSON
	if json.Unmarshal(answer, &members) != nil {
		var single memberJSON
		if json.Unmarshal(answer, &single) != nil {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "kChat returned an invalid response" + uncertainMemberAdd}
		}
		members = []memberJSON{single}
	}
	confirmed := map[string]bool{}
	for _, m := range members {
		if m.ChannelID == channelID {
			confirmed[m.UserID] = true
		}
	}
	for _, id := range userIDs {
		if !confirmed[id] {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "kChat returned an invalid response" + uncertainMemberAdd}
		}
	}
	return &MembersAdded{ChannelID: channelID, UserIDs: userIDs, Count: len(userIDs)}, nil
}

// memberTarget validates the arguments locally and binds the channel live.
func memberTarget(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, op string, withRoles bool) (*Client, memberArguments, error) {
	var input memberArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.UserID) {
		return nil, input, invalidRequest("user_id must be a kChat-style identifier")
	}
	if withRoles && input.Roles != roleMember && input.Roles != roleAdmin {
		return nil, input, invalidRequest("roles must be " + roleMember + " or " + roleAdmin)
	}
	client, _, err := openMemberChannel(ctx, resolved, secrets, red, op, input.ChannelID)
	if err != nil {
		return nil, input, err
	}
	return client, input, nil
}

func invokeChannelsMembersRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := memberTarget(ctx, resolved, secrets, red, raw, "remove channel member", false)
	if err != nil {
		return nil, err
	}
	return client.RemoveMember(ctx, input.ChannelID, input.UserID)
}

// RemoveMember sends exactly one DELETE and never repeats it.
func (c *Client) RemoveMember(ctx context.Context, channelID, userID string) (*MemberRemoved, error) {
	const op = "remove channel member"
	path := "/api/v4/channels/" + url.PathEscape(channelID) + "/members/" + url.PathEscape(userID)
	if err := c.doWith(ctx, op, http.MethodDelete, path, nil, nil, nil, uncertainMemberRemove); err != nil {
		return nil, err
	}
	return &MemberRemoved{ChannelID: channelID, UserID: userID, Removed: true}, nil
}

func invokeChannelsMembersRoles(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := memberTarget(ctx, resolved, secrets, red, raw, "change channel member role", true)
	if err != nil {
		return nil, err
	}
	return client.SetMemberRoles(ctx, input.ChannelID, input.UserID, input.Roles)
}

// SetMemberRoles sends exactly one PUT with one of the two allowed role values and never repeats it.
func (c *Client) SetMemberRoles(ctx context.Context, channelID, userID, roles string) (*MemberRoles, error) {
	const op = "change channel member role"
	path := "/api/v4/channels/" + url.PathEscape(channelID) + "/members/" + url.PathEscape(userID) + "/roles"
	if err := c.doWith(ctx, op, http.MethodPut, path, nil, map[string]string{"roles": roles}, nil,
		uncertainMemberRoles); err != nil {
		return nil, err
	}
	return &MemberRoles{ChannelID: channelID, UserID: userID, Roles: roles}, nil
}
