package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var userGroupSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"description":{"type":"string"},"source":{"type":"string"},"member_count":{"type":"integer"},` +
	`"allow_reference":{"type":"boolean"}},` +
	`"required":["id","name","display_name","description","source","member_count","allow_reference"],` +
	`"additionalProperties":false}`

var usergroupsList = capability.Descriptor{
	ID:      Provider + ".usergroups.list",
	Version: 1,
	Title:   "List Infomaniak kChat user groups",
	Description: "List the user groups of one bound team or of one channel this connection may reach, one page at " +
		"a time, with name, display name, description, source, and member count; exactly one of team_id and " +
		"channel_id; no members",
	Tags:     []string{"infomaniak", "kchat", "usergroups", "users", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"groups":{"type":"array","items":` +
		userGroupSchema + `},"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["groups","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "team_id", Description: "Bound team to list; exactly one of team_id and channel_id"},
		{Name: "channel_id", Description: "Channel to list, inside this connection's channel targets; exactly " +
			"one of team_id and channel_id"},
		{Name: "page", Description: "1-based page of the user groups; the first page when omitted"},
		{Name: "limit", Description: "User groups per page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the user group"},
		{Name: "name", Description: "Mention name, untrusted data"},
		{Name: "display_name", Description: "Display name, untrusted data"},
		{Name: "description", Description: "Description, untrusted data"},
		{Name: "source", Description: "Origin of the group as kChat reports it, untrusted data"},
		{Name: "member_count", Description: "Number of members as kChat reports it"},
		{Name: "allow_reference", Description: "True when the group may be mentioned"},
		{Name: "team_id", Description: "Team that was listed, when listed by team"},
		{Name: "channel_id", Description: "Channel that was listed, when listed by channel"},
		{Name: "page", Description: "Page that was read"},
		{Name: "has_more", Description: "True when kChat may hold a further page"},
		{Name: "count", Description: "Number of user groups reported on this page"},
	},
	Examples: []capability.Example{{Description: "List the user groups of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

type userGroupJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Source         string `json:"source"`
	MemberCount    int    `json:"member_count"`
	AllowReference bool   `json:"allow_reference"`
}

// UserGroup is the stable Qatlas view of one user group.
type UserGroup struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Source         string `json:"source"`
	MemberCount    int    `json:"member_count"`
	AllowReference bool   `json:"allow_reference"`
}

// UserGroupsPage is one page of the user groups of a team or channel.
type UserGroupsPage struct {
	TeamID    string      `json:"team_id,omitempty"`
	ChannelID string      `json:"channel_id,omitempty"`
	Groups    []UserGroup `json:"groups"`
	Page      int         `json:"page"`
	HasMore   bool        `json:"has_more"`
	Count     int         `json:"count"`
}

func invokeUserGroupsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list user groups"
	var input usersListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	filter, err := checkUserFilter(resolved, input.TeamID, input.ChannelID)
	if err != nil {
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
	if err := filter.confirm(ctx, client, op); err != nil {
		return nil, err
	}
	return client.ListUserGroups(ctx, filter, page, limit)
}

// ListUserGroups reads one page of the user groups of a team or channel and never follows a further page.
// kChat answers with a bare array or with an object holding a groups array; both are read.
func (c *Client) ListUserGroups(ctx context.Context, filter userFilter, page, limit int) (*UserGroupsPage, error) {
	const op = "list user groups"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	path := "/api/v4/teams/" + url.PathEscape(filter.teamID) + "/groups"
	if filter.teamID == "" {
		path = "/api/v4/channels/" + url.PathEscape(filter.channelID) + "/groups"
	}
	var raw json.RawMessage
	if err := c.do(ctx, op, http.MethodGet, path, query, nil, &raw, false); err != nil {
		return nil, err
	}
	var groups []userGroupJSON
	if err := json.Unmarshal(raw, &groups); err != nil {
		var wrapped struct {
			Groups []userGroupJSON `json:"groups"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, providerError(op, "the user groups could not be read")
		}
		groups = wrapped.Groups
	}
	entries := make([]UserGroup, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		if !validMattermostID(g.ID) {
			continue
		}
		entries = append(entries, UserGroup{ID: g.ID, Name: bounded(g.Name), DisplayName: bounded(g.DisplayName),
			Description: bounded(g.Description), Source: bounded(g.Source), MemberCount: g.MemberCount,
			AllowReference: g.AllowReference})
	}
	return &UserGroupsPage{TeamID: filter.teamID, ChannelID: filter.channelID, Groups: entries, Page: page,
		HasMore: len(groups) >= limit, Count: len(entries)}, nil
}
