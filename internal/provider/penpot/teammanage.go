package penpot

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const teamNameSchema = `{"type":"string","minLength":1,"maxLength":250}`

var teamsCreate = capability.Descriptor{
	ID: Provider + ".teams.create", Version: 1, Title: "Create a Penpot team",
	Description: "Create a new team owned by the token's account. The connection stays bound to its configured teams: " +
		"the new team can be managed only after its identifier is added as a team/ID target of a connection, which " +
		"Qatlas never does by itself. The name may not contain '.', ':', or '/'. Penpot limits the teams per account. " +
		manageNote,
	Tags: []string{"penpot", "teams", "create", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:         manageRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema:  schemaOf(`"name":`+teamNameSchema, `"name"`),
	OutputSchema: schemaOf(`"team_id":{"type":"string"}`, `"team_id"`),
	Arguments:    []capability.Argument{{Name: "name", Required: true, Description: "Team name, 1 to 250 characters without '.', ':', or '/'"}},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Identifier of the new team; it is not a target of any connection until the user adds it"},
	},
	Examples: []capability.Example{{Description: "Create a team", Arguments: json.RawMessage(`{"name":"Customer C"}`)}},
}

var teamsDelete = capability.Descriptor{
	ID: Provider + ".teams.delete", Version: 1, Title: "Delete a Penpot team",
	Description: "Delete one bound team with all its projects and files. Penpot marks the team as deleted and removes it " +
		"after its deletion delay; only the owner may do it and the default team of an account is refused. " + manageNote,
	Tags: []string{"penpot", "teams", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:         manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema:  schemaOf(`"team_id":`+uuidSchema, `"team_id"`),
	OutputSchema: schemaOf(`"deleted":{"type":"boolean"},"team_id":{"type":"string"}`, `"deleted","team_id"`),
	Arguments:    []capability.Argument{teamIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Penpot accepted the deletion"},
		{Name: "team_id", Description: "Identifier of the team"},
	},
	Examples: []capability.Example{{Description: "Delete a team",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001"}`)}},
}

// TeamCreated is the answer of teams.create; it carries the identifier only.
type TeamCreated struct {
	TeamID string `json:"team_id"`
}

// TeamDeleted is the answer of teams.delete.
type TeamDeleted struct {
	Deleted bool   `json:"deleted"`
	TeamID  string `json:"team_id"`
}

// checkTeamName is checkName plus Penpot's own rule that a team name has none of '.', ':', '/'.
func checkTeamName(name string) (string, error) {
	const reason = "name must be valid text of 1 to 250 characters without control characters, '.', ':', or '/'"
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameLength || strings.ContainsAny(name, ".:/") {
		return "", invalidRequest(reason)
	}
	blank := true
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", invalidRequest(reason)
		}
		if !unicode.IsSpace(r) {
			blank = false
		}
	}
	if blank {
		return "", invalidRequest(reason)
	}
	return name, nil
}

func invokeTeamsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create team"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	if err := refuseProjectAllowList(resolved); err != nil {
		return nil, err
	}
	name, err := checkTeamName(input.Name)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	data, err := client.change(ctx, op, cmdCreateTeam, map[string]any{"name": name})
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok || answer.id("id") == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return &TeamCreated{TeamID: answer.id("id")}, nil
}

func invokeTeamsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete team"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, err := selectTeamForTeamWideChange(resolved, input.TeamID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdDeleteTeam, map[string]any{"id": teamID}); err != nil {
		return nil, err
	}
	return &TeamDeleted{Deleted: true, TeamID: teamID}, nil
}
