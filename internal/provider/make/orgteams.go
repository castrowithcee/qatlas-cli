package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file work on the teams of the organization a connection in organization mode is
// bound to; the organization is always the connection's target, never an argument. A team tool of the other
// mode refuses before any secret or request, and these tools refuse on a team connection the same way.
// API (checked 2026-10-06 against developers.make.com's published API reference, section Teams, not a live
// account):
//   - GET /teams?organizationId=, teams:read, cols[] and pg[limit]; answering {"teams":[...]}. Personal
//     spaces are left out (includePrivateSpaces defaults to false).
//   - POST /teams, teams:write, body name (at most 128 characters), organizationId, optional operationsLimit
//     (0 to 2,000,000,000); answering {"team":{...},"userTeamRole":{...}}.
//   - PATCH /teams/{teamId}, teams:write, optional name and operationsLimit; answering {"team":{...}}.
//   - DELETE /teams/{teamId}?confirmed=true, teams:write, answering {"team":id}. Make also deletes all data
//     of the team, for example scenarios, webhooks, and custom team variables.
// Update and delete first read the team (GET /teams/{teamId}) and bind it to the bound organization through
// its organizationId; a team of another organization, and a personal space, is refused without naming it.
// Narrower reading: a team's operations limit is changeable, but never its other fields.

const (
	maxTeamNameLength = 128
	maxOperationsCap  = 2000000000
	// maxTeamsListed bounds the teams one list returns.
	maxTeamsListed = 200

	teamsUncertain = "; this change may have taken effect, list the teams before repeating it"
	foreignTeamMsg = "team_id is outside the organization this connection is bound to"
)

var teamsCols = []string{"id", "name", "organizationId", "operationsLimit", "transferLimit", "consumedOperations",
	"consumedTransfer", "isPaused", "type"}

var teamViewSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"organization_id":{"type":"integer"},"type":{"type":"string"},"is_paused":{"type":"boolean"},` +
	`"operations_limit":{"type":"integer"},"transfer_limit":{"type":"integer"},` +
	`"consumed_operations":{"type":"integer"},"consumed_transfer":{"type":"integer"}},` +
	`"required":["id","name","organization_id"],"additionalProperties":false}`

var teamViewFields = []capability.Field{
	{Name: "id", Description: "Team identifier"},
	{Name: "name", Description: "Team name, untrusted data"},
	{Name: "organization_id", Description: "Always the connection's bound organization"},
	{Name: "type", Description: "Team type as Make reports it, such as standard"},
	{Name: "is_paused", Description: "True when Make reports the team as paused"},
	{Name: "operations_limit", Description: "Operations limit, as Make reports it"},
	{Name: "transfer_limit", Description: "Data transfer limit in bytes, as Make reports it"},
	{Name: "consumed_operations", Description: "Operations consumed in the current period"},
	{Name: "consumed_transfer", Description: "Data transferred in the current period, in bytes"},
}

var teamIDArgument = capability.Argument{Name: "team_id", Required: true,
	Description: "Team of the bound organization; read live and refused when it belongs to another organization"}
var teamNameArgument = capability.Argument{Name: "name", Description: "Team name, 1 to " +
	strconv.Itoa(maxTeamNameLength) + " characters"}
var teamLimitArgument = capability.Argument{Name: "operations_limit",
	Description: "Operations limit, 0 to " + strconv.Itoa(maxOperationsCap)}

const teamIDSchema = `{"type":"integer","minimum":1}`
const teamNameSchema = `{"type":"string","minLength":1,"maxLength":128}`
const teamLimitSchema = `{"type":"integer","minimum":0,"maximum":2000000000}`

var teamsList = capability.Descriptor{
	ID: Provider + ".teams.list", Version: 1, Title: "List the teams of the bound Make organization",
	Description: "List the teams of the organization this connection is bound to, without personal spaces; " +
		"takes no arguments. Only on an organization connection; a team connection refuses it. Needs the " +
		"teams:read scope",
	Tags: []string{"make", "teams", "list", "organization", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"teams":{"type":"array","items":` + teamViewSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["organization_id","teams","count","truncated"],` +
		`"additionalProperties":false}`),
	Fields: append(append([]capability.Field{}, teamViewFields...),
		capability.Field{Name: "teams", Description: "Teams as Make reports them, capped"},
		capability.Field{Name: "count", Description: "Teams returned, at most " + strconv.Itoa(maxTeamsListed)},
		capability.Field{Name: "truncated", Description: "True when the page was full and more teams may exist"}),
	Examples: []capability.Example{{Description: "List the organization's teams", Arguments: json.RawMessage(`{}`)}},
}

var teamsCreate = capability.Descriptor{
	ID: Provider + ".teams.create", Version: 1, Title: "Create a team in the bound Make organization",
	Description: "Create one team in the organization this connection is bound to; the organization is always " +
		"the connection's own. The token's owner becomes a member as Make decides. Sends one request and never " +
		"repeats it. Only on an organization connection. Needs the teams:write scope",
	Tags: []string{"make", "teams", "create", "organization", "automation"},
	Risk: makeChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + teamNameSchema +
		`,"operations_limit":` + teamLimitSchema + `},"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team":` + teamViewSchema +
		`},"required":["team"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "name", Required: true, Description: teamNameArgument.Description},
		teamLimitArgument},
	Fields:   []capability.Field{{Name: "team", Description: "The created team; fields: id, name, organization_id, type, is_paused, operations_limit, transfer_limit, consumed_operations, consumed_transfer"}},
	Examples: []capability.Example{{Description: "Create a team", Arguments: json.RawMessage(`{"name":"Customer A"}`)}},
}

var teamsUpdate = capability.Descriptor{
	ID: Provider + ".teams.update", Version: 1, Title: "Update a team of the bound Make organization",
	Description: "Rename a team or change its operations limit; at least one of name and operations_limit. The " +
		"team is read first and bound to the connection's organization; a team of another organization or a " +
		"personal space is refused without changing anything. Sends one changing request and never repeats it. " +
		"Only on an organization connection. Needs the teams:write and teams:read scopes",
	Tags: []string{"make", "teams", "update", "organization", "automation"},
	Risk: makeChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + teamIDSchema + `,"name":` +
		teamNameSchema + `,"operations_limit":` + teamLimitSchema + `},"required":["team_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team":` + teamViewSchema +
		`},"required":["team"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, teamNameArgument, teamLimitArgument},
	Fields:    []capability.Field{{Name: "team", Description: "The updated team; fields as in teams.create"}},
	Examples: []capability.Example{{Description: "Rename a team",
		Arguments: json.RawMessage(`{"team_id":123,"name":"Customer B"}`)}},
}

var teamsDelete = capability.Descriptor{
	ID: Provider + ".teams.delete", Version: 1, Title: "Delete a team of the bound Make organization",
	Description: "Delete one team of the bound organization for good. Make also deletes all data of the team: " +
		"its scenarios, webhooks, and custom team variables; Qatlas does not look them up, check them first. " +
		"The team is read first and bound to the connection's organization; a team of another organization or " +
		"a personal space is refused. Make requires its confirmed flag, which is sent only when confirmed is " +
		"true. Sends one request and never repeats it. Offered only when a connection's tools list names it, " +
		"in no profile. Only on an organization connection. Needs the teams:write and teams:read scopes",
	Tags: []string{"make", "teams", "delete", "organization", "automation"},
	Risk: makeChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + teamIDSchema +
		`,"confirmed":{"type":"boolean"}},"required":["team_id","confirmed"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["organization_id","team_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, {Name: "confirmed", Required: true,
		Description: "Must be true: it sets Make's own confirmed flag and acknowledges that the team's " +
			"scenarios, webhooks, and variables are deleted with it"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "team_id", Description: "The team that was deleted"},
		{Name: "deleted", Description: "True when Make accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete a team",
		Arguments: json.RawMessage(`{"team_id":123,"confirmed":true}`)}},
}

// TeamView is one team of the bound organization.
type TeamView struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	OrganizationID     int64  `json:"organization_id"`
	Type               string `json:"type,omitempty"`
	IsPaused           bool   `json:"is_paused,omitempty"`
	OperationsLimit    int64  `json:"operations_limit,omitempty"`
	TransferLimit      int64  `json:"transfer_limit,omitempty"`
	ConsumedOperations int64  `json:"consumed_operations,omitempty"`
	ConsumedTransfer   int64  `json:"consumed_transfer,omitempty"`
}

type orgTeamJSON struct {
	teamJSON
	Type string `json:"type"`
}

type teamsPageJSON struct {
	Teams []orgTeamJSON `json:"teams"`
}

func teamViewOf(t orgTeamJSON) TeamView {
	return TeamView{ID: t.ID, Name: bounded(t.Name), OrganizationID: t.OrganizationID, Type: bounded(t.Type),
		IsPaused: t.IsPaused, OperationsLimit: t.OperationsLimit, TransferLimit: t.TransferLimit,
		ConsumedOperations: t.ConsumedOperations, ConsumedTransfer: t.ConsumedTransfer}
}

func (c *Client) teamsQuery() url.Values {
	return url.Values{"cols[]": teamsCols}
}

// belongsToOrg reports whether Make's answer for a team names the bound organization.
func (c *Client) belongsToOrg(t orgTeamJSON) bool {
	return t.OrganizationID == c.scope.orgID
}

type TeamsList struct {
	OrganizationID int64      `json:"organization_id"`
	Teams          []TeamView `json:"teams"`
	Count          int        `json:"count"`
	Truncated      bool       `json:"truncated"`
}

func invokeTeamsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	query := client.teamsQuery()
	query.Set("organizationId", strconv.FormatInt(client.scope.orgID, 10))
	query.Set("pg[limit]", strconv.Itoa(maxTeamsListed))
	var page teamsPageJSON
	if err := client.get(ctx, "list teams", "/teams", query, &page, needTeamsRead); err != nil {
		return nil, err
	}
	result := &TeamsList{OrganizationID: client.scope.orgID, Teams: make([]TeamView, 0, len(page.Teams)),
		Truncated: len(page.Teams) >= maxTeamsListed}
	for _, t := range page.Teams {
		if t.ID <= 0 || !client.belongsToOrg(t) || len(result.Teams) >= maxTeamsListed {
			continue
		}
		result.Teams = append(result.Teams, teamViewOf(t))
	}
	result.Count = len(result.Teams)
	return result, nil
}

// teamBody is the validated name and limit of create and update.
type teamArguments struct {
	TeamID          int64   `json:"team_id"`
	Name            *string `json:"name"`
	OperationsLimit *int64  `json:"operations_limit"`
	Confirmed       bool    `json:"confirmed"`
}

func readTeamArguments(raw json.RawMessage, op string) (teamArguments, error) {
	var input teamArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if input.Name != nil {
		n := utf8.RuneCountInString(*input.Name)
		if n < 1 || n > maxTeamNameLength || !utf8.ValidString(*input.Name) {
			return input, invalidRequest("name must be 1 to " + strconv.Itoa(maxTeamNameLength) + " characters")
		}
	}
	if input.OperationsLimit != nil && (*input.OperationsLimit < 0 || *input.OperationsLimit > maxOperationsCap) {
		return input, invalidRequest("operations_limit must be between 0 and " + strconv.Itoa(maxOperationsCap))
	}
	return input, nil
}

func teamPath(teamID int64) string { return "/teams/" + strconv.FormatInt(teamID, 10) }

// bindTeam reads one team live and binds it to the bound organization. A team of another organization, an
// unreadable organization, and a personal space are all refused with the same message that names nothing.
func (c *Client) bindTeam(ctx context.Context, op string, teamID int64) error {
	var wrapper struct {
		Team orgTeamJSON `json:"team"`
	}
	if err := c.get(ctx, op, teamPath(teamID), c.teamsQuery(), &wrapper, needTeamsWrite); err != nil {
		return err
	}
	t := wrapper.Team
	if t.ID != teamID || !c.belongsToOrg(t) || t.Type == "personal" {
		return invalidRequest(foreignTeamMsg)
	}
	return nil
}

// TeamChange is the answer of teams.create and teams.update.
type TeamChange struct {
	Team TeamView `json:"team"`
}

func (c *Client) changeTeam(ctx context.Context, op, method, path string, body map[string]any,
	wantID int64) (any, error) {
	var answer struct {
		Team orgTeamJSON `json:"team"`
	}
	if err := c.change(ctx, op, method, path, c.teamsQuery(), body, &answer, needTeamsWrite,
		teamsUncertain); err != nil {
		return nil, err
	}
	t := answer.Team
	if t.ID <= 0 || (wantID != 0 && t.ID != wantID) || !c.belongsToOrg(t) {
		return nil, invalidResponse(op, "Make did not report the changed team in the bound organization"+teamsUncertain)
	}
	return &TeamChange{Team: teamViewOf(t)}, nil
}

func invokeTeamsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create team"
	input, err := readTeamArguments(raw, op)
	if err != nil {
		return nil, err
	}
	if input.Name == nil {
		return nil, invalidRequest("name is required")
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"name": *input.Name, "organizationId": client.scope.orgID}
	if input.OperationsLimit != nil {
		body["operationsLimit"] = *input.OperationsLimit
	}
	return client.changeTeam(ctx, op, http.MethodPost, "/teams", body, 0)
}

func invokeTeamsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update team"
	input, err := readTeamArguments(raw, op)
	if err != nil {
		return nil, err
	}
	if input.TeamID <= 0 {
		return nil, invalidRequest("team_id must be a positive integer")
	}
	if input.Name == nil && input.OperationsLimit == nil {
		return nil, invalidRequest("name or operations_limit is required")
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.bindTeam(ctx, op, input.TeamID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if input.Name != nil {
		body["name"] = *input.Name
	}
	if input.OperationsLimit != nil {
		body["operationsLimit"] = *input.OperationsLimit
	}
	return client.changeTeam(ctx, op, http.MethodPatch, teamPath(input.TeamID), body, input.TeamID)
}

// TeamDeletion is the answer of teams.delete.
type TeamDeletion struct {
	OrganizationID int64 `json:"organization_id"`
	TeamID         int64 `json:"team_id"`
	Deleted        bool  `json:"deleted"`
}

func invokeTeamsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete team"
	input, err := readTeamArguments(raw, op)
	if err != nil {
		return nil, err
	}
	if input.TeamID <= 0 {
		return nil, invalidRequest("team_id must be a positive integer")
	}
	if !input.Confirmed {
		return nil, invalidRequest("confirmed must be true: deleting a team also deletes its scenarios, " +
			"webhooks, and variables")
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.bindTeam(ctx, op, input.TeamID); err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodDelete, teamPath(input.TeamID), url.Values{"confirmed": {"true"}},
		nil, nil, needTeamsWrite, teamsUncertain); err != nil {
		return nil, err
	}
	return &TeamDeletion{OrganizationID: client.scope.orgID, TeamID: input.TeamID, Deleted: true}, nil
}
