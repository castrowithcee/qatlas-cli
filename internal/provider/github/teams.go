package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The organization tools read the teams of one organization and the members of one of its teams. They name
// an organization, never a project: github.project-teams.list reads the teams a project is linked to, a
// narrower and unrelated question these tools do not answer.

var organizationArgument = capability.Argument{Name: "owner", Description: "Organization as orgs/LOGIN; " +
	"optional when the connection's targets name exactly one owner; must lie inside the targets when the " +
	"connection lists any, as an owner target itself, since a project or a repository pattern of the " +
	"organization is not enough"}

var organizationField = capability.Field{Name: "owner", Description: "Organization the tool listed, as " +
	"orgs/LOGIN; the one a default chose when the argument was left out"}

var organizationTeamProperties = `"team":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"privacy":{"type":"string"},"id":{"type":"integer"},` +
	`"node_id":{"type":"string"},"slug":{"type":"string"},"notification_setting":{"type":"string"},` +
	`"permission":{"type":"string"},"permissions":` + teamPermissionsSchema + `,"type":{"type":"string"},` +
	`"access_source":{"type":"string"},"organization_id":{"type":"integer"},"enterprise_id":{"type":"integer"},` +
	`"parent":` + teamSimpleSchema + `,"url":{"type":"string"},` + teamLinksProperties

const organizationTeamRequired = `"required":["team","name"],"additionalProperties":false`

var organizationTeamsList = capability.Descriptor{
	ID:      Provider + ".teams.list",
	Version: 1,
	Title:   "List the teams of a GitHub organization",
	Description: "List one bounded batch of the teams of an organization a connection allows, in " +
		"the order GitHub returns them, with slug, name, description, and privacy",
	Tags:         []string{"github", "teams", "organization", "discovery", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"team_type":{"type":"string","enum":["all","enterprise","organization"]},` + pagingKeys),
	OutputSchema: listOutput("teams", organizationTeamProperties, organizationTeamRequired),
	Arguments: append([]capability.Argument{
		{Name: "team_type", Description: "all, enterprise, or organization; GitHub's default when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "teams", Description: "Teams of the organization: team (the slug github.team-members.list " +
			"takes), name, description (untrusted data), and privacy (secret or closed), and the other members " +
			"of GitHub's team object"},
		{Name: "id", Description: "Numeric team id"},
		{Name: "node_id", Description: "GraphQL node id of the team, untrusted data"},
		{Name: "slug", Description: "URL name of the team, untrusted data"},
		{Name: "notification_setting", Description: "Notification setting of the team, untrusted data"},
		{Name: "permission", Description: "Permission of the team, untrusted data"},
		{Name: "permissions", Description: "pull, triage, push, maintain, and admin of the team"},
		{Name: "type", Description: "Type of the team, untrusted data"},
		{Name: "access_source", Description: "Where the team's access comes from, untrusted data"},
		{Name: "organization_id", Description: "Numeric id of the organization"},
		{Name: "enterprise_id", Description: "Numeric id of the enterprise"},
		{Name: "parent", Description: "Parent team as an object, with ldap_dn; untrusted data"},
		{Name: "url", Description: "Web address of the team, untrusted data, never fetched"},
		{Name: "links", Description: "REST url, members_url, and repositories_url by their GitHub names, " +
			"untrusted data, never fetched"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the teams of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org"}`),
	}},
}

var teamMemberProperties = `"login":{"type":"string"},` + accountPersonProperties + `,"type":{"type":"string"},` +
	`"starred_at":{"type":"string"},"role":{"type":"string"},"inherited":{"type":"boolean"},"url":{"type":"string"}`

var teamMembersList = capability.Descriptor{
	ID:      Provider + ".team-members.list",
	Version: 1,
	Title:   "List the members of a GitHub team",
	Description: "List one bounded batch of the members of one team of an organization an explicit " +
		"connection allows, in the order GitHub returns them, by login",
	Tags:     []string{"github", "teams", "organization", "discovery", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"team":`+slugSchema+`,"role":{"type":"string","enum":["member","maintainer","all"]},`+
		pagingKeys, "team"),
	OutputSchema: listOutput("members", teamMemberProperties, `"required":["login"],"additionalProperties":false`),
	Arguments: append([]capability.Argument{
		{Name: "team", Description: "Slug of a team of the organization, as github.teams.list reports it", Required: true},
		{Name: "role", Description: "member, maintainer, or all; GitHub's default when omitted"},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "members", Description: "Members of the team by login, with the members of GitHub's team " +
			"member object, including a person's name and email address; untrusted data"},
		{Name: "type", Description: "Account type, untrusted data"},
		{Name: "starred_at", Description: "Star time GitHub reports for the member, untrusted data"},
		{Name: "role", Description: "Role of the member in the team, untrusted data"},
		{Name: "inherited", Description: "Whether the membership is inherited from a parent team"},
		{Name: "url", Description: "Web address of the member, untrusted data, never fetched"},
	}, accountPersonFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "List the members of a team",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org","team":"design"}`),
	}},
}

// organizationArguments holds the arguments of the organization tools; the input schema of each tool admits
// only its own. page and perPage are derived by the checks.
type organizationArguments struct {
	Team   string `json:"team"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	// Role is the team members' filter and TeamType the team list's; each tool admits only its own.
	Role     string `json:"role"`
	TeamType string `json:"team_type"`

	role, teamType string
	page, perPage  int
	binding        []byte
}

func (a *organizationArguments) query() url.Values {
	values := url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
	if a.role != "" {
		values.Set("role", a.role)
	}
	if a.teamType != "" {
		values.Set("team_type", a.teamType)
	}
	return values
}

var (
	teamTypeValues = []string{"all", "enterprise", "organization"}
	teamRoleValues = []string{"member", "maintainer", "all"}
)

func checkOrganizationTeamsListArguments(a *organizationArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	binding := []any{"teams", "organization", strings.ToLower(bound.String())}
	if a.TeamType != "" {
		if !containsFold(teamTypeValues, a.TeamType) {
			return invalidRequest("team_type must be all, enterprise, or organization")
		}
		a.teamType = strings.ToLower(a.TeamType)
		binding = append(binding, "team_type", a.teamType)
	}
	a.binding = fingerprint(binding...)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func checkTeamMembersListArguments(a *organizationArguments, bound target) error {
	if err := checkTeam("team", a.Team); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	binding := []any{"teammembers", strings.ToLower(bound.String()), strings.ToLower(a.Team)}
	if a.Role != "" {
		if !containsFold(teamRoleValues, a.Role) {
			return invalidRequest("role must be member, maintainer, or all")
		}
		a.role = strings.ToLower(a.Role)
		binding = append(binding, "role", a.role)
	}
	a.binding = fingerprint(binding...)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// organizationHandler decodes and checks the arguments and the organization before a credential is
// resolved, so a refused request never becomes a provider call.
func organizationHandler(id string, check func(*organizationArguments, target) error,
	call func(context.Context, *Client, *organizationArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments organizationArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectOrganization(resolved, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func organizationOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: organizationTeamsList, Handler: organizationHandler(organizationTeamsList.ID,
			checkOrganizationTeamsListArguments, func(ctx context.Context, c *Client, a *organizationArguments) (any, error) {
				return c.listOrganizationTeams(ctx, a)
			})},
		{Descriptor: teamMembersList, Handler: organizationHandler(teamMembersList.ID,
			checkTeamMembersListArguments, func(ctx context.Context, c *Client, a *organizationArguments) (any, error) {
				return c.listTeamMembers(ctx, a)
			})},
	}
}

// OrganizationTeam is one team of an organization.
type OrganizationTeam struct {
	Team                string           `json:"team"`
	Name                string           `json:"name"`
	Description         string           `json:"description,omitempty"`
	Privacy             string           `json:"privacy,omitempty"`
	ID                  *int64           `json:"id,omitempty"`
	NodeID              *string          `json:"node_id,omitempty"`
	Slug                *string          `json:"slug,omitempty"`
	NotificationSetting *string          `json:"notification_setting,omitempty"`
	Permission          *string          `json:"permission,omitempty"`
	Permissions         *RESTPermissions `json:"permissions,omitempty"`
	Type                *string          `json:"type,omitempty"`
	AccessSource        *string          `json:"access_source,omitempty"`
	OrganizationID      *int64           `json:"organization_id,omitempty"`
	EnterpriseID        *int64           `json:"enterprise_id,omitempty"`
	Parent              *RESTTeamSimple  `json:"parent,omitempty"`
	URL                 *string          `json:"url,omitempty"`
	Links               Links            `json:"links,omitempty"`
}

// teamRaw is one team of the list: the members the tool always answered, read as before, and the ones it
// answers in addition.
type teamRaw struct {
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Privacy     string           `json:"privacy"`
	Permissions *RESTPermissions `json:"permissions"`
	Parent      *teamCore        `json:"parent"`
	core        teamCore
}

// permissions is the permission object of the team, or nil while GitHub sent none of its members.
func (r *teamRaw) permissions() *RESTPermissions {
	if p := r.Permissions; p != nil && (p.Pull != nil || p.Triage != nil || p.Push != nil || p.Maintain != nil ||
		p.Admin != nil) {
		return p
	}
	return nil
}

func (r *teamRaw) UnmarshalJSON(data []byte) error {
	type plain teamRaw
	if err := json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	return json.Unmarshal(data, &r.core)
}

// OrganizationTeamList is one batch of the teams of an organization.
type OrganizationTeamList struct {
	Teams      []OrganizationTeam `json:"teams"`
	NextCursor string             `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}

// TeamMember is one member of a team, by login.
type TeamMember struct {
	Login string `json:"login"`
	AccountPerson
	Type      *string `json:"type,omitempty"`
	StarredAt *string `json:"starred_at,omitempty"`
	Role      *string `json:"role,omitempty"`
	Inherited *bool   `json:"inherited,omitempty"`
	URL       *string `json:"url,omitempty"`
}

type teamMemberRaw struct {
	Login     string  `json:"login"`
	Role      *string `json:"role"`
	Inherited *bool   `json:"inherited"`
	account   accountRaw
}

func (r *teamMemberRaw) UnmarshalJSON(data []byte) error {
	type plain teamMemberRaw
	if err := json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	return json.Unmarshal(data, &r.account)
}

// TeamMemberList is one batch of the members of a team.
type TeamMemberList struct {
	Members    []TeamMember `json:"members"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

// Permission messages of the organization tools. GitHub decides on every request; a message names what such
// a request needs without claiming what the configured token holds.
const (
	organizationTeamsReadPermission = "GitHub refused this token the teams of this organization; reading " +
		"them needs read:org on a classic token, or Members: read of the organization on a fine-grained token"
	teamMembersReadPermission = "GitHub refused this token the members of this team; reading them needs " +
		"read:org on a classic token, or Members: read of the organization on a fine-grained token"
)

// orgPath is the organization REST route of one path below the bound organization.
func (c *Client) orgPath(rest string) string {
	return "/orgs/" + url.PathEscape(c.target.owner) + "/" + rest
}

func (c *Client) listOrganizationTeams(ctx context.Context, a *organizationArguments) (*OrganizationTeamList, error) {
	const op = "list organization teams"
	var raw []teamRaw
	hasNext, err := c.restPage(ctx, op, c.orgPath("teams"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, organizationTeamsReadPermission)
	}
	result := &OrganizationTeamList{Teams: make([]OrganizationTeam, 0, len(raw))}
	for _, team := range raw {
		if team.Slug == "" || team.Name == "" {
			return nil, invalidEntry(op, "a team")
		}
		result.Teams = append(result.Teams, OrganizationTeam{Team: team.Slug, Name: team.Name,
			Description: team.Description, Privacy: team.Privacy, ID: team.core.ID, NodeID: shortText(team.core.NodeID),
			Slug: shortText(team.core.Slug), NotificationSetting: shortText(team.core.NotificationSetting),
			Permission: shortText(team.core.Permission), Permissions: team.permissions(),
			Type: shortText(team.core.Type), AccessSource: shortText(team.core.AccessSource),
			OrganizationID: team.core.OrganizationID, EnterpriseID: team.core.EnterpriseID,
			Parent: team.Parent.simple(), URL: address(team.core.HTMLURL), Links: team.core.links})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) listTeamMembers(ctx context.Context, a *organizationArguments) (*TeamMemberList, error) {
	const op = "list team members"
	var raw []teamMemberRaw
	hasNext, err := c.restPage(ctx, op, c.orgPath("teams/"+url.PathEscape(a.Team)+"/members"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, teamMembersReadPermission)
	}
	result := &TeamMemberList{Members: make([]TeamMember, 0, len(raw))}
	for _, member := range raw {
		if member.Login == "" {
			return nil, invalidEntry(op, "a team member")
		}
		result.Members = append(result.Members, TeamMember{Login: member.Login, AccountPerson: member.account.person(),
			Type: shortText(member.account.Type), StarredAt: shortText(member.account.StarredAt),
			Role: shortText(member.Role), Inherited: member.Inherited, URL: address(member.account.HTMLURL)})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}
