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
	teamRoleMember = "team_user"
	teamRoleAdmin  = "team_user team_admin"

	uncertainTeamRoles = "; the role may have been changed, list the team members before changing it again"

	teamMembersOutOfScope = "user_id must be a current member of the team"
)

var teamMemberEntrySchema = `{"type":"object","properties":{` +
	`"user_id":` + idSchema + `,"roles":{"type":"string"},"scheme_admin":{"type":"boolean"},"deleted":{"type":"boolean"}},` +
	`"required":["user_id","roles","scheme_admin","deleted"],"additionalProperties":false}`

var teamsGet = capability.Descriptor{
	ID:      Provider + ".teams.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat team",
	Description: "Read the name, display name, description, type, and member counts of one team this " +
		"connection is bound to. Invitation identifiers, email addresses, and allowed domains are never returned",
	Tags:     []string{"infomaniak", "kchat", "teams", "get"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + idSchema + `,"name":{"type":"string"},` +
		`"display_name":{"type":"string"},"description":{"type":"string"},"type":{"type":"string"},` +
		`"member_count":{"type":"integer"},"active_member_count":{"type":"integer"}},` +
		`"required":["id","name","display_name","description","type","member_count","active_member_count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Team identifier"},
		{Name: "name", Description: "URL-safe team handle, untrusted data"},
		{Name: "display_name", Description: "Display name of the team, untrusted data"},
		{Name: "description", Description: "Description of the team, untrusted data"},
		{Name: "type", Description: "kChat's own team type, for example O (open) or I (invite-only)"},
		{Name: "member_count", Description: "Number of members of the team"},
		{Name: "active_member_count", Description: "Number of active members of the team"},
	},
	Examples: []capability.Example{{Description: "Read one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var teamMembersList = capability.Descriptor{
	ID:      Provider + ".teammembers.list",
	Version: 1,
	Title:   "List Infomaniak kChat team members",
	Description: "List the members of one team this connection is bound to, one page at a time; each entry " +
		"names the user, the team role, and whether the membership has ended",
	Tags:     []string{"infomaniak", "kchat", "teams", "members", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"members":{"type":"array","items":` + teamMemberEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["team_id","members","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of the members; the first page when omitted"},
		{Name: "limit", Description: "Members per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "Member user identifier"},
		{Name: "roles", Description: "Team roles of the member, for example team_user or team_user team_admin"},
		{Name: "scheme_admin", Description: "True when the member is a team administrator by the team's permission scheme"},
		{Name: "deleted", Description: "True when the membership has ended"},
		{Name: "team_id", Description: "Team that was read"},
		{Name: "page", Description: "Page that was read"},
		{Name: "has_more", Description: "True when the page is full, so a further page may remain"},
		{Name: "count", Description: "Number of members reported on this page"},
	},
	Examples: []capability.Example{{Description: "List the members of one team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var teamMembersRoles = capability.Descriptor{
	ID:      Provider + ".teammembers.roles",
	Version: 1,
	Title:   "Change an Infomaniak kChat team role",
	Description: "Set the team role of exactly one current member of one team this connection is bound to with " +
		"one confirmed request: team_user for a plain member, or team_user team_admin for a team " +
		"administrator. This changes the member's rights",
	Tags:                  []string{"infomaniak", "kchat", "teams", "members", "roles", "permissions"},
	Risk:                  memberChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"user_id":` +
		idSchema + `,"roles":{"type":"string","enum":["` + teamRoleMember + `","` + teamRoleAdmin + `"]}},` +
		`"required":["team_id","user_id","roles"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"user_id":` +
		idSchema + `,"roles":{"type":"string"}},"required":["team_id","user_id","roles"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, userIDArgument,
		{Name: "roles", Description: "team_user or team_user team_admin", Required: true}},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Team of the member"},
		{Name: "user_id", Description: "Member whose role was set"},
		{Name: "roles", Description: "Role that was set"},
	},
	Examples: []capability.Example{{Description: "Make a member a team administrator",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","user_id":"abc123user0000000000000000",` +
			`"roles":"team_user team_admin"}`)}},
}

// The subsets of the kChat team resources this provider decodes; every other field, such as the invitation
// identifier, the email address, and the allowed domains, is never read.
type teamDetailJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type teamStatsJSON struct {
	TeamID      string `json:"team_id"`
	TotalCount  int    `json:"total_member_count"`
	ActiveCount int    `json:"active_member_count"`
}

type teamMemberJSON struct {
	TeamID      string `json:"team_id"`
	UserID      string `json:"user_id"`
	Roles       string `json:"roles"`
	SchemeAdmin bool   `json:"scheme_admin"`
	DeleteAt    int64  `json:"delete_at"`
}

// TeamDetail is the stable Qatlas view of one team; the invitation identifier and the team's configuration
// are never passed on.
type TeamDetail struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	Description       string `json:"description"`
	Type              string `json:"type"`
	MemberCount       int    `json:"member_count"`
	ActiveMemberCount int    `json:"active_member_count"`
}

// TeamMemberEntry is the stable Qatlas view of one team member.
type TeamMemberEntry struct {
	UserID      string `json:"user_id"`
	Roles       string `json:"roles"`
	SchemeAdmin bool   `json:"scheme_admin"`
	Deleted     bool   `json:"deleted"`
}

// TeamMembersPage is one paginated listing of the members of one team.
type TeamMembersPage struct {
	TeamID  string            `json:"team_id"`
	Members []TeamMemberEntry `json:"members"`
	Page    int               `json:"page"`
	HasMore bool              `json:"has_more"`
	Count   int               `json:"count"`
}

// TeamMemberRoles is the answer of one confirmed team role change.
type TeamMemberRoles struct {
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
	Roles  string `json:"roles"`
}

type teamArguments struct {
	TeamID string `json:"team_id"`
	Page   int    `json:"page"`
	Limit  int    `json:"limit"`
	UserID string `json:"user_id"`
	Roles  string `json:"roles"`
}

// openTeam reads the arguments and binds the team locally before any secret is read.
func openTeam(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, op string, check func(teamArguments) error) (*Client, teamArguments, error) {
	var input teamArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, input, err
	}
	if check != nil {
		if err := check(input); err != nil {
			return nil, input, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, input, err
	}
	return client, input, nil
}

func invokeTeamsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openTeam(ctx, resolved, secrets, red, raw, "get team", nil)
	if err != nil {
		return nil, err
	}
	return client.GetTeam(ctx, input.TeamID)
}

// GetTeam reads the team and its member counts and reports only the fixed allow-list of fields.
func (c *Client) GetTeam(ctx context.Context, teamID string) (*TeamDetail, error) {
	const op = "get team"
	base := "/api/v4/teams/" + url.PathEscape(teamID)
	var team teamDetailJSON
	if err := c.do(ctx, op, http.MethodGet, base, nil, nil, &team, false); err != nil {
		return nil, err
	}
	var stats teamStatsJSON
	if err := c.do(ctx, op, http.MethodGet, base+"/stats", nil, nil, &stats, false); err != nil {
		return nil, err
	}
	if team.ID != teamID || stats.TeamID != teamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "kChat returned an invalid response"}
	}
	return &TeamDetail{ID: teamID, Name: bounded(team.Name), DisplayName: bounded(team.DisplayName),
		Description: bounded(team.Description), Type: bounded(team.Type), MemberCount: stats.TotalCount,
		ActiveMemberCount: stats.ActiveCount}, nil
}

func invokeTeamMembersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openTeam(ctx, resolved, secrets, red, raw, "list team members", nil)
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
	return client.ListTeamMembers(ctx, input.TeamID, page, limit)
}

// ListTeamMembers reads one page of a team's members exactly as kChat paginates them and never follows a
// further page itself.
func (c *Client) ListTeamMembers(ctx context.Context, teamID string, page, limit int) (*TeamMembersPage, error) {
	const op = "list team members"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	var members []teamMemberJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/teams/"+url.PathEscape(teamID)+"/members", query, nil,
		&members, false); err != nil {
		return nil, err
	}
	entries := make([]TeamMemberEntry, 0, len(members))
	for _, m := range members {
		if m.TeamID != teamID || !validMattermostID(m.UserID) {
			continue
		}
		entries = append(entries, TeamMemberEntry{UserID: m.UserID, Roles: bounded(m.Roles),
			SchemeAdmin: m.SchemeAdmin, Deleted: m.DeleteAt > 0})
	}
	return &TeamMembersPage{TeamID: teamID, Members: entries, Page: page, HasMore: len(members) >= limit,
		Count: len(entries)}, nil
}

func invokeTeamMembersRoles(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "change team member role"
	client, input, err := openTeam(ctx, resolved, secrets, red, raw, op, func(in teamArguments) error {
		if !validMattermostID(in.UserID) {
			return invalidRequest("user_id must be a kChat-style identifier")
		}
		if in.Roles != teamRoleMember && in.Roles != teamRoleAdmin {
			return invalidRequest("roles must be " + teamRoleMember + " or " + teamRoleAdmin)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Only this team counts, not every bound team. The placeholder own ID keeps verifyUsersScope from
	// admitting the token's own user without a membership, so every target must be a live member.
	scoped := *client
	scoped.scope = scope{teams: []string{input.TeamID}}
	scoped.self = "-"
	allowed, err := scoped.verifyUsersScope(ctx, op, []string{input.UserID})
	if err != nil {
		return nil, err
	}
	if len(allowed) != 1 {
		return nil, invalidRequest(teamMembersOutOfScope)
	}
	return client.SetTeamMemberRoles(ctx, input.TeamID, input.UserID, input.Roles)
}

// SetTeamMemberRoles sends exactly one PUT with one of the two allowed role values and never repeats it.
func (c *Client) SetTeamMemberRoles(ctx context.Context, teamID, userID, roles string) (*TeamMemberRoles, error) {
	const op = "change team member role"
	path := "/api/v4/teams/" + url.PathEscape(teamID) + "/members/" + url.PathEscape(userID) + "/roles"
	if err := c.doWith(ctx, op, http.MethodPut, path, nil, map[string]string{"roles": roles}, nil,
		uncertainTeamRoles); err != nil {
		return nil, err
	}
	return &TeamMemberRoles{TeamID: teamID, UserID: userID, Roles: roles}, nil
}
