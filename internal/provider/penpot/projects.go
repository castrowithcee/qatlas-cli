package penpot

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxProjectsListed = 1000

var teamIDArgument = capability.Argument{Name: "team_id", Required: true,
	Description: "Team identifier from penpot.teams.list; must be inside this connection's team targets"}

var projectsList = capability.Descriptor{
	ID: Provider + ".projects.list", Version: 1, Title: "List Penpot projects",
	Description: "List the projects of one bound team, restricted to the connection's project allow-list when it " +
		"has one; names are untrusted provider data",
	Tags: []string{"penpot", "projects", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + uuidSchema +
		`},"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"projects":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"team_id":{"type":"string"},"file_count":{"type":"integer"},"is_pinned":{"type":"boolean"},` +
		`"modified_at":{"type":"string"}},"required":["id","name","team_id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["projects","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument},
	Fields: []capability.Field{
		{Name: "projects", Description: "Projects with id (used as project_id by the file tools), name, team_id, file_count, is_pinned, and modified_at"},
		{Name: "count", Description: "Number of projects in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 1000 projects"},
	},
	Examples: []capability.Example{{Description: "List the projects of a team",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001"}`)}},
}

// Project is one project of a bound team.
type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TeamID     string `json:"team_id"`
	FileCount  int64  `json:"file_count,omitempty"`
	IsPinned   bool   `json:"is_pinned,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// ProjectsResult is the answer of projects.list.
type ProjectsResult struct {
	Projects  []Project `json:"projects"`
	Count     int       `json:"count"`
	Truncated bool      `json:"truncated"`
}

func invokeProjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list projects"
	var input struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	teamID, err := selectTeam(resolved, input.TeamID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	projects, err := client.projectsOf(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	result := &ProjectsResult{Projects: []Project{}}
	for _, project := range projects {
		if !client.scope.allowsProject(project.ID) {
			continue
		}
		if len(result.Projects) >= maxProjectsListed {
			result.Truncated = true
			break
		}
		result.Projects = append(result.Projects, project)
	}
	result.Count = len(result.Projects)
	return result, nil
}

// projectsOf reads the projects of one team. A project that reports another team is dropped.
func (c *Client) projectsOf(ctx context.Context, op, teamID string) ([]Project, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdProjects, map[string]any{"team-id": teamID}, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	projects := make([]Project, 0, len(entries))
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if reported := entry.id("teamid"); reported != "" && reported != teamID {
			continue
		}
		projects = append(projects, Project{ID: id, Name: entry.str("name"), TeamID: teamID,
			FileCount: entry.integer("count"), IsPinned: entry.boolean("ispinned"), ModifiedAt: entry.str("modifiedat")})
	}
	return projects, nil
}

// locateProject proves that a project lies in one of the bound teams: it reads the projects of each bound team
// until the project appears. A project that is not found is refused like one outside the targets.
func (c *Client) locateProject(ctx context.Context, op, projectID string) error {
	return c.locateProjectAs(ctx, op, "project_id", projectID)
}

// locateProjectAs is locateProject for an argument with another name.
func (c *Client) locateProjectAs(ctx context.Context, op, field, projectID string) error {
	for _, teamID := range c.scope.teams {
		projects, err := c.projectsOf(ctx, op, teamID)
		if err != nil {
			return err
		}
		for _, project := range projects {
			if project.ID == projectID {
				return nil
			}
		}
	}
	return invalidRequest(field + " is outside the targets of this connection")
}
