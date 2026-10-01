package penpot

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	metadataSensitive = "penpot-metadata"
	designSensitive   = "penpot-design"
	maxTeamsListed    = 100
)

const uuidSchema = `{"type":"string","pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}`

var metadataRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: metadataSensitive}

var teamsList = capability.Descriptor{
	ID: Provider + ".teams.list", Version: 1, Title: "List Penpot teams",
	Description: "List the teams of the access token's account, restricted to the teams of the connection's " +
		"targets; names are untrusted provider data",
	Tags: []string{"penpot", "teams", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"teams":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"is_default":{"type":"boolean"}},"required":["id","name"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["teams","count","truncated"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "teams", Description: "Teams with id (used as team_id by penpot.projects.list), name, and is_default"},
		{Name: "count", Description: "Number of teams in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at " + "100 teams"},
	},
	Examples: []capability.Example{{Description: "List the bound teams", Arguments: json.RawMessage(`{}`)}},
}

// Team is one team of the connection's targets.
type Team struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default,omitempty"`
}

// TeamsResult is the answer of teams.list.
type TeamsResult struct {
	Teams     []Team `json:"teams"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
}

func invokeTeamsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTeams(ctx, "list teams")
}

// ListTeams reads every team of the token and keeps only those of the connection's targets.
func (c *Client) ListTeams(ctx context.Context, op string) (*TeamsResult, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdTeams, nil, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	result := &TeamsResult{Teams: []Team{}}
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" || !c.scope.allowsTeam(id) {
			continue
		}
		if len(result.Teams) >= maxTeamsListed {
			result.Truncated = true
			break
		}
		result.Teams = append(result.Teams, Team{ID: id, Name: entry.str("name"), IsDefault: entry.boolean("isdefault")})
	}
	result.Count = len(result.Teams)
	return result, nil
}
