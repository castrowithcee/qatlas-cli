package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The access tools read the teams a project is linked to and change who reaches it: the collaborators and
// their roles, and the teams of its organization it is linked to, which GitHub grants read access to the
// project. The changes change rights, so they are offered only by a connection whose tools list names them. Users are named by login and teams by slug;
// every one is resolved together with the project in one query before the one change is sent, a team inside
// the organization that owns the project. GitHub's API reads neither the collaborators nor their roles, so
// there is no tool that lists them. A project of a user has no teams.

// maxCollaborators bounds the collaborators of one change.
const maxCollaborators = 20

// projectRoles maps the roles of a collaborator to GitHub's; none removes the direct access.
var projectRoles = map[string]string{"none": "NONE", "reader": "READER", "writer": "WRITER", "admin": "ADMIN"}

const (
	slugSchema          = `{"type":"string","minLength":1,"maxLength":100,"pattern":"` + loginPattern + `"}`
	collaboratorsSchema = `{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{` +
		`"user":` + slugSchema + `,"team":` + slugSchema + `,"role":{"type":"string",` +
		`"enum":["none","reader","writer","admin"]}},"required":["role"],"additionalProperties":false}}`
	collaboratorsOutput = `{"type":"object","properties":{"collaborators":{"type":"array","items":{"type":"object",` +
		`"properties":{"user":{"type":"string"},"team":{"type":"string"},"role":{"type":"string"}},` +
		`"required":["role"],"additionalProperties":false}},"updated":{"type":"boolean"}},` +
		`"required":["collaborators","updated"],"additionalProperties":false}`
	teamInput = `{"type":"object","properties":{"team":` + slugSchema + `},"required":["team"],` +
		`"additionalProperties":false}`
	teamOutput = `{"type":"object","properties":{"team":{"type":"string"},"linked":{"type":"boolean"}},` +
		`"required":["team","linked"],"additionalProperties":false}`
)

var teamsList = capability.Descriptor{
	ID:      Provider + ".projectteams.list",
	Version: 1,
	Title:   "List the teams of a GitHub project",
	Description: "Read one bounded batch of the teams one GitHub project an explicit connection allows is " +
		"linked to, in name order, with slug and name",
	Tags:                       []string{"github", "projects", "teams", "access", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,` +
		`"maximum":100},"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"teams":{"type":"array","items":{` +
		`"type":"object","properties":{"team":{"type":"string"},"name":{"type":"string"}},` +
		`"required":["team","name"],"additionalProperties":false}},"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["teams","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Teams per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch; the first batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "teams", Description: "Linked teams in name order: team (the slug github.projects.linkteam " +
			"takes) and name, untrusted data; empty for a project of a user"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the project is linked to further teams"},
	},
	Examples: []capability.Example{{
		Description: "Read the teams a project is linked to",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var collaboratorsUpdate = capability.Descriptor{
	ID:      Provider + ".projectcollaborators.update",
	Version: 1,
	Title:   "Change the collaborators of a GitHub project",
	Description: "Grant users or teams a role on one GitHub project an explicit connection allows, change it, " +
		"or remove their direct access; this changes who may read, edit, or manage the project. Offered only " +
		"by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "collaborators", "access", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"collaborators":` + collaboratorsSchema + `},` +
		`"required":["collaborators"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(collaboratorsOutput),
	Arguments: []capability.Argument{{Name: "collaborators", Description: "At most 20 entries, each naming " +
		"either user (a login) or team (the slug of a team of the project's organization) and role: none " +
		"removes the direct access, reader views, writer edits, admin also manages the project's settings; " +
		"collaborators left out stay unchanged", Required: true}},
	Fields: []capability.Field{
		{Name: "collaborators", Description: "The roles GitHub confirmed, one entry per requested collaborator"},
		{Name: "updated", Description: "True once GitHub applied every role"},
	},
	Examples: []capability.Example{{
		Description: "Let a team edit a project and take a user's access away",
		Arguments: json.RawMessage(`{"project":"orgs/octo-org/projects/7","collaborators":[` +
			`{"team":"design","role":"writer"},{"user":"octocat","role":"none"}]}`),
	}},
}

var teamsLink = capability.Descriptor{
	ID:      Provider + ".projects.linkteam",
	Version: 1,
	Title:   "Link a GitHub project to a team",
	Description: "Link one project of an organization an explicit connection allows to a team of that " +
		"organization, which grants the team read access to the project. Offered only by a connection whose " +
		"tools list names it",
	Tags:                       []string{"github", "projects", "teams", "access", "link"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                json.RawMessage(teamInput),
	OutputSchema:               json.RawMessage(teamOutput),
	Arguments: []capability.Argument{{Name: "team", Description: "Slug of a team of the organization that " +
		"owns the project", Required: true}},
	Fields: []capability.Field{
		{Name: "team", Description: "Slug of the team"},
		{Name: "linked", Description: "True after a link, false after an unlink"},
	},
	Examples: []capability.Example{{
		Description: "Link a project to a team",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","team":"design"}`),
	}},
}

var teamsUnlink = capability.Descriptor{
	ID:      Provider + ".projects.unlinkteam",
	Version: 1,
	Title:   "Unlink a GitHub project from a team",
	Description: "Remove the link between one project of an organization an explicit connection allows and a " +
		"team of that organization, with the read access it granted. Offered only by a connection whose tools " +
		"list names it",
	Tags:                       []string{"github", "projects", "teams", "access", "unlink"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                json.RawMessage(teamInput),
	OutputSchema:               json.RawMessage(teamOutput),
	Arguments:                  teamsLink.Arguments,
	Fields:                     teamsLink.Fields,
	Examples: []capability.Example{{
		Description: "Unlink a project from a team",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","team":"design"}`),
	}},
}

// accessOperations are the collaborator and team tools.
func accessOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: teamsList, Handler: capability.Handler(invokeTeamsList)},
		{Descriptor: collaboratorsUpdate, Handler: capability.Handler(invokeCollaboratorsUpdate)},
		{Descriptor: teamsLink, Handler: capability.Handler(invokeTeamsLink(true))},
		{Descriptor: teamsUnlink, Handler: capability.Handler(invokeTeamsLink(false))},
	}
}

// Collaborator is one user or team and the role it takes on a project.
type Collaborator struct {
	User string `json:"user,omitempty"`
	Team string `json:"team,omitempty"`
	Role string `json:"role"`
}

// Collaborators are the roles a collaborator change writes.
type Collaborators struct {
	Collaborators []Collaborator `json:"collaborators"`
}

// ChangedCollaborators is the answer of a collaborator change.
type ChangedCollaborators struct {
	Collaborators []Collaborator `json:"collaborators"`
	Updated       bool           `json:"updated"`
}

// TeamLink is the answer of a team link or unlink.
type TeamLink struct {
	Team   string `json:"team"`
	Linked bool   `json:"linked"`
}

// LinkedTeam is one team a project is linked to.
type LinkedTeam struct {
	Team string `json:"team"`
	Name string `json:"name"`
}

// TeamList is one batch of the teams a project is linked to.
type TeamList struct {
	Teams      []LinkedTeam `json:"teams"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

// TeamListOptions are the paging of one team list.
type TeamListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// normalize applies the bounds of one team list request and returns the GitHub cursor it continues after. A
// cursor is bound to the project.
func (o *TeamListOptions) normalize(bound target) (string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return "", err
	}
	o.Limit = limit
	return decodeCursor(o.binding(bound), o.Cursor)
}

func (o *TeamListOptions) binding(bound target) []byte {
	return fingerprint("teams", strings.ToLower(bound.String()))
}

// teamRef is the argument of a team link or unlink.
type teamRef struct {
	Team string `json:"team"`
}

func checkTeam(argument, slug string) error {
	if !validLogin(slug) {
		return invalidRequest(argument + " must be a team slug of letters, digits, hyphens, and underscores")
	}
	return nil
}

func (ref teamRef) check() error { return checkTeam("team", ref.Team) }

func (list Collaborators) check() error {
	if len(list.Collaborators) == 0 || len(list.Collaborators) > maxCollaborators {
		return invalidRequest(fmt.Sprintf("collaborators must name 1 to %d entries", maxCollaborators))
	}
	seen := map[string]bool{}
	for _, entry := range list.Collaborators {
		if (entry.User == "") == (entry.Team == "") {
			return invalidRequest("every entry of collaborators names either user or team")
		}
		key := "user:" + entry.User
		if entry.Team != "" {
			if err := checkTeam("team", entry.Team); err != nil {
				return err
			}
			key = "team:" + entry.Team
		} else if !validLogin(entry.User) {
			return invalidRequest("user must be a login")
		}
		if _, ok := projectRoles[entry.Role]; !ok {
			return invalidRequest("role must be none, reader, writer, or admin")
		}
		if seen[strings.ToLower(key)] {
			return invalidRequest("collaborators names one user or team more than once")
		}
		seen[strings.ToLower(key)] = true
	}
	return nil
}

func (list Collaborators) hasTeams() bool {
	for _, entry := range list.Collaborators {
		if entry.Team != "" {
			return true
		}
	}
	return false
}

// checkTeamOwner refuses a team for a project of a user: teams belong to an organization, and GitHub links
// and grants a team only a project of its own organization.
func checkTeamOwner(project target) error {
	if project.scope != "orgs" {
		return invalidRequest("teams belong to an organization, and " + project.argument() + " belongs to a user; " +
			"GitHub links and grants a team only a project of its own organization")
	}
	return nil
}

const (
	collaboratorsMutation = `mutation($project:ID!,$collaborators:[ProjectV2Collaborator!]!){` +
		`update:updateProjectV2Collaborators(input:{projectId:$project,collaborators:$collaborators}){` +
		`clientMutationId}}`
	linkTeamMutation = `mutation($project:ID!,$team:ID!){link:linkProjectV2ToTeam(input:{projectId:$project,` +
		`teamId:$team}){clientMutationId}}`
	unlinkTeamMutation = `mutation($project:ID!,$team:ID!){link:unlinkProjectV2FromTeam(input:{projectId:$project,` +
		`teamId:$team}){clientMutationId}}`
)

// ListTeams reads one batch of the teams the bound project is linked to, in name order.
func (c *Client) ListTeams(ctx context.Context, options TeamListOptions) (*TeamList, error) {
	if c.target.kind != kindProject {
		return nil, providerError("list project teams", "this connection is not bound to a project")
	}
	after, err := options.normalize(c.target)
	if err != nil {
		return nil, err
	}
	return c.listTeams(ctx, options, after)
}

// listTeams reads exactly one server page of the teams of the bound project.
func (c *Client) listTeams(ctx context.Context, options TeamListOptions, after string) (*TeamList, error) {
	const op = "list project teams"
	query := `query($owner:String!,$number:Int!,$first:Int!,$after:String){owner:` + c.target.ownerField() +
		`(login:$owner){projectV2(number:$number){teams(first:$first,after:$after,` +
		`orderBy:{field:NAME,direction:ASC}){pageInfo{hasNextPage endCursor} nodes{slug name}}}}}`
	variables := c.projectVariables()
	variables["first"], variables["after"] = options.Limit, nil
	if after != "" {
		variables["after"] = after
	}
	var page struct {
		Owner *struct {
			Project *struct {
				Teams struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						Slug string `json:"slug"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"teams"`
			} `json:"projectV2"`
		} `json:"owner"`
	}
	if err := c.graphql(ctx, op, query, variables, &page); err != nil {
		// GitHub shows teams only to a token that may read the organizations, even for a project of a user.
		var providerErr *provider.Error
		if errors.As(err, &providerErr) && providerErr.Class == provider.ClassPermission {
			providerErr.Message += "; reading teams needs read:org on a classic token, or Members: read of " +
				"the organization on a fine-grained one"
		}
		return nil, err
	}
	if page.Owner == nil || page.Owner.Project == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	teams := page.Owner.Project.Teams
	result := &TeamList{Teams: make([]LinkedTeam, 0, len(teams.Nodes))}
	for _, node := range teams.Nodes {
		if node.Slug == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a team without a slug"}
		}
		result.Teams = append(result.Teams, LinkedTeam{Team: node.Slug, Name: node.Name})
	}
	if teams.PageInfo.HasNextPage {
		if teams.PageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further teams without a cursor"}
		}
		result.HasMore, result.NextCursor = true, encodeCursor(options.binding(c.target), teams.PageInfo.EndCursor)
	}
	return result, nil
}

// UpdateCollaborators writes the roles of the named users and teams on the bound project in one mutation.
// Every user and team is resolved with the project first.
func (c *Client) UpdateCollaborators(ctx context.Context, list Collaborators) (*ChangedCollaborators, error) {
	const op = "update project collaborators"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := list.check(); err != nil {
		return nil, err
	}
	if list.hasTeams() {
		if err := checkTeamOwner(c.target); err != nil {
			return nil, err
		}
	}
	var request planningRequest
	for _, entry := range list.Collaborators {
		if entry.Team != "" {
			request.teams = append(request.teams, entry.Team)
		} else {
			request.assignees = append(request.assignees, entry.User)
		}
	}
	info, nodes, err := c.resolve(ctx, op, request)
	if err != nil {
		return nil, err
	}
	inputs := make([]map[string]any, len(list.Collaborators))
	users, teams := nodes.assignees, nodes.teams
	for i, entry := range list.Collaborators {
		inputs[i] = map[string]any{"role": projectRoles[entry.Role]}
		if entry.Team != "" {
			inputs[i]["teamId"], teams = teams[0], teams[1:]
		} else {
			inputs[i]["userId"], users = users[0], users[1:]
		}
	}
	var answer struct {
		Update *json.RawMessage `json:"update"`
	}
	if err := c.mutate(ctx, op, collaboratorsMutation, map[string]any{"project": info.id,
		"collaborators": inputs}, &answer); err != nil {
		return nil, err
	}
	if answer.Update == nil {
		return nil, invalidResponse(op, true)
	}
	return &ChangedCollaborators{Collaborators: list.Collaborators, Updated: true}, nil
}

// LinkTeam links the bound project of an organization to one of its teams, or unlinks it. The project and
// the team are resolved in one query.
func (c *Client) LinkTeam(ctx context.Context, slug string, link bool) (*TeamLink, error) {
	op, mutation := "link project to team", linkTeamMutation
	if !link {
		op, mutation = "unlink project from team", unlinkTeamMutation
	}
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkTeam("team", slug); err != nil {
		return nil, err
	}
	if err := checkTeamOwner(c.target); err != nil {
		return nil, err
	}
	info, nodes, err := c.resolve(ctx, op, planningRequest{teams: []string{slug}})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Link *json.RawMessage `json:"link"`
	}
	if err := c.mutate(ctx, op, mutation, map[string]any{"project": info.id, "team": nodes.teams[0]},
		&answer); err != nil {
		return nil, err
	}
	if answer.Link == nil {
		return nil, invalidResponse(op, true)
	}
	return &TeamLink{Team: slug, Linked: link}, nil
}

// The handlers check the target, the arguments, and that a team meets a project of an organization before a
// credential is resolved, so a refused request never becomes a secret read or a provider call.

func invokeTeamsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options TeamListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list project teams")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listTeams(ctx, options, after))
}

func invokeCollaboratorsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var list Collaborators
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, unreadable("update project collaborators")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	if err := list.check(); err != nil {
		return nil, err
	}
	if list.hasTeams() {
		if err := checkTeamOwner(bound); err != nil {
			return nil, err
		}
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.UpdateCollaborators(ctx, list))
}

func invokeTeamsLink(link bool) func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
	json.RawMessage) (any, error) {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var ref teamRef
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, unreadable("link project to team")
		}
		bound, err := selectTarget(resolved, kindProject, raw)
		if err != nil {
			return nil, err
		}
		if err := ref.check(); err != nil {
			return nil, err
		}
		if err := checkTeamOwner(bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(client.LinkTeam(ctx, ref.Team, link))
	}
}
