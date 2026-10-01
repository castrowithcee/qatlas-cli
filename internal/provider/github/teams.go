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
// an organization, never a project: github.projectteams.list reads the teams a project is linked to, a
// narrower and unrelated question these tools do not answer.

var organizationArgument = capability.Argument{Name: "owner", Description: "Organization as orgs/LOGIN; " +
	"optional when the connection's targets name exactly one owner; must lie inside the targets when the " +
	"connection lists any, as an owner target itself, since a project or a repository pattern of the " +
	"organization is not enough"}

var organizationField = capability.Field{Name: "owner", Description: "Organization the tool listed, as " +
	"orgs/LOGIN; the one a default chose when the argument was left out"}

const organizationTeamProperties = `"team":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"privacy":{"type":"string"}`

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
	InputSchema:  inputSchema(pagingKeys),
	OutputSchema: listOutput("teams", organizationTeamProperties, organizationTeamRequired),
	Arguments:    pagingArguments,
	Fields: append([]capability.Field{
		{Name: "teams", Description: "Teams of the organization: team (the slug github.teammembers.list " +
			"takes), name, description (untrusted data), and privacy (secret or closed)"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the teams of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org"}`),
	}},
}

var teamMembersList = capability.Descriptor{
	ID:      Provider + ".teammembers.list",
	Version: 1,
	Title:   "List the members of a GitHub team",
	Description: "List one bounded batch of the members of one team of an organization an explicit " +
		"connection allows, in the order GitHub returns them, by login",
	Tags:         []string{"github", "teams", "organization", "discovery", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"team":`+slugSchema+`,`+pagingKeys, "team"),
	OutputSchema: listOutput("members", `"login":{"type":"string"}`, `"required":["login"],"additionalProperties":false`),
	Arguments: append([]capability.Argument{
		{Name: "team", Description: "Slug of a team of the organization, as github.teams.list reports it", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "members", Description: "Members of the team by login"},
	}, pagingFields...),
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

	page, perPage int
	binding       []byte
}

func (a *organizationArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

func checkOrganizationTeamsListArguments(a *organizationArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("teams", "organization", strings.ToLower(bound.String()))
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
	a.binding = fingerprint("teammembers", strings.ToLower(bound.String()), strings.ToLower(a.Team))
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
	Team        string `json:"team"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Privacy     string `json:"privacy,omitempty"`
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
	var raw []struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Privacy     string `json:"privacy"`
	}
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
			Description: team.Description, Privacy: team.Privacy})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) listTeamMembers(ctx context.Context, a *organizationArguments) (*TeamMemberList, error) {
	const op = "list team members"
	var raw []struct {
		Login string `json:"login"`
	}
	hasNext, err := c.restPage(ctx, op, c.orgPath("teams/"+url.PathEscape(a.Team)+"/members"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, teamMembersReadPermission)
	}
	result := &TeamMemberList{Members: make([]TeamMember, 0, len(raw))}
	for _, member := range raw {
		if member.Login == "" {
			return nil, invalidEntry(op, "a team member")
		}
		result.Members = append(result.Members, TeamMember{Login: member.Login})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}
