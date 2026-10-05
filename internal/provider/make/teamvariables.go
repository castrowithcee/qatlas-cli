package makeapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file list, create, update, and delete the variables of the connection's own bound
// team. None takes a team argument: the team is always the connection's target. Variable values may hold
// secrets, tokens, or personal data that scenarios use, so they are untrusted and sensitive data.
// API (checked 2026-10-05 against developers.make.com's published API reference, section Teams, not a live
// account):
//   - GET /teams/{teamId}/variables, team-variables:read, no query, answering {"teamVariables":[{typeId,
//     name,value,isSystem}]}. Without the custom variables feature (license customVariables, see GET
//     /organizations/{id}) only Make's system variables are returned.
//   - POST /teams/{teamId}/variables, team-variables:write, body typeId (1 number, 2 string, 3 boolean,
//     4 date in ISO 8601), name (letters, digits, "$", "_"), value; answering {"teamVariable":{...}}.
//   - PATCH /teams/{teamId}/variables/{variableName}, team-variables:write, optional body typeId and value;
//     the reference asks to send the typeId whenever the value changes. Answering {"teamVariable":{...}}.
//   - DELETE /teams/{teamId}/variables/{variableName}?confirmed=true, team-variables:write, answering
//     {"ok":n}. The reference says that without confirmed=true the call fails and that without the custom
//     variables feature it answers 404.
//
// Assumed, because the reference does not state it: the maximum length of a name or a text value, the exact
// ISO 8601 forms of a date value (only RFC 3339 and a plain date are accepted here), whether a system
// variable is rejected by PATCH and DELETE (it is refused locally, after reading the list, and never sent),
// and which scenarios a deleted variable affects (the reference lists none, and none is looked up). Renaming
// a variable and the history endpoint are not offered.

const (
	teamVariablesSensitivity = "make-team-variables-may-hold-secrets"
	maxVariableNameLength    = 128
	maxVariableTextBytes     = 4096
	// maxVariablesListed bounds the variables one list returns.
	maxVariablesListed = 500

	needTeamVariablesRead  = "the team-variables:read scope"
	needTeamVariablesWrite = "the team-variables:write scope (and team-variables:read, which checks the variable first)"
	variablesUncertain     = "; this change may have taken effect, list the variables before repeating it"
	noCustomVariables      = "the bound team has no custom variable of this name; without the customVariables " +
		"license Make reports only system variables"
)

var variableNamePattern = regexp.MustCompile(`^[A-Za-z0-9$_]{1,` + strconv.Itoa(maxVariableNameLength) + `}$`)

const variableNameSchema = `{"type":"string","pattern":"^[A-Za-z0-9$_]{1,128}$"}`
const variableTypeSchema = `{"type":"string","enum":["number","text","boolean","date"]}`
const variableValueSchema = `{}`

var variableTypeIDs = map[string]int{"number": 1, "text": 2, "boolean": 3, "date": 4}
var variableTypeNames = map[int]string{1: "number", 2: "text", 3: "boolean", 4: "date"}

var variableNameArgument = capability.Argument{Name: "name", Required: true, Description: "Variable name, 1 to " +
	strconv.Itoa(maxVariableNameLength) + " letters, digits, dollar signs, or underscores"}
var variableTypeArgument = capability.Argument{Name: "type", Required: true,
	Description: "number, text, boolean, or date; the value must match it"}
var variableValueArgument = capability.Argument{Name: "value", Required: true, Description: "number, text of at " +
	"most " + strconv.Itoa(maxVariableTextBytes) + " bytes, boolean, or a date as an RFC 3339 timestamp or " +
	"YYYY-MM-DD, matching type. Do not pass secrets unless the variable is meant to hold one"}

var teamVariablesReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: teamVariablesSensitivity}

func teamVariablesChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: teamVariablesSensitivity}
}

const variableSchema = `{"type":"object","properties":{"name":{"type":"string"},"type":{"type":"string"},` +
	`"value":{},"is_system":{"type":"boolean"},"truncated":{"type":"boolean"}},` +
	`"required":["name","type","is_system","truncated"],"additionalProperties":false}`

var variableFields = []capability.Field{
	{Name: "name", Description: "Variable name, untrusted data"},
	{Name: "type", Description: "number, text, boolean, date, or other"},
	{Name: "value", Description: "Variable value, untrusted and possibly secret, capped in size; omitted when " +
		"empty or cut entirely"},
	{Name: "is_system", Description: "True for a Make system variable; those are never changed or deleted here"},
	{Name: "truncated", Description: "True when the name or value was cut by a local ceiling"},
}

var teamVariablesList = capability.Descriptor{
	ID: Provider + ".teamvariables.list", Version: 1, Title: "List the variables of the bound Make team",
	Description: "List the custom and system variables of the bound team, each with name, type, capped value, " +
		"and is_system; takes no arguments, the team is always the connection's own. Values may be secret. " +
		"Without the customVariables license Make reports only system variables, so custom_count 0 can mean " +
		"that the feature is unavailable. Refused on a connection with a scenario allow-list. Needs the " +
		"team-variables:read scope",
	Tags: []string{"make", "team", "variables", "list", "automation"}, Risk: teamVariablesReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},` +
		`"variables":{"type":"array","items":` + variableSchema + `},"count":{"type":"integer"},` +
		`"custom_count":{"type":"integer"},"system_count":{"type":"integer"},"omitted":{"type":"integer"}},` +
		`"required":["team_id","variables","count","custom_count","system_count"],"additionalProperties":false}`),
	Fields: append(append([]capability.Field{}, variableFields...),
		capability.Field{Name: "team_id", Description: "The bound team"},
		capability.Field{Name: "variables", Description: "Variables as Make reports them, capped; fields: name, " +
			"type, value, is_system, truncated"},
		capability.Field{Name: "count", Description: "Number of variables returned"},
		capability.Field{Name: "custom_count", Description: "Custom variables among the returned ones; 0 can also " +
			"mean that the team's license has no custom variables"},
		capability.Field{Name: "system_count", Description: "System variables among the returned ones"},
		capability.Field{Name: "omitted", Description: "Variables beyond the " + strconv.Itoa(maxVariablesListed) +
			" returned ones, left out"}),
	Examples: []capability.Example{{Description: "List the bound team's variables", Arguments: json.RawMessage(`{}`)}},
}

var teamVariablesCreate = capability.Descriptor{
	ID: Provider + ".teamvariables.create", Version: 1, Title: "Create a variable in the bound Make team",
	Description: "Create one custom variable in the bound team; the team is always the connection's own. Needs " +
		"the customVariables license, which Make's reference ties to the plan. Sends one request and never " +
		"repeats it. Refused on a connection with a scenario allow-list. Needs the team-variables:write scope",
	Tags: []string{"make", "team", "variables", "create", "automation"},
	Risk: teamVariablesChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema + `,"type":` +
		variableTypeSchema + `,"value":` + variableValueSchema + `},"required":["name","type","value"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},"variable":` +
		variableSchema + `},"required":["team_id","variable"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, variableTypeArgument, variableValueArgument},
	Fields: append([]capability.Field{{Name: "team_id", Description: "The bound team"},
		{Name: "variable", Description: "The created variable as Make reports it; fields: name, type, value, " +
			"is_system, truncated"}}, variableFields...),
	Examples: []capability.Example{{Description: "Create a text variable",
		Arguments: json.RawMessage(`{"name":"region","type":"text","value":"eu"}`)}},
}

var teamVariablesUpdate = capability.Descriptor{
	ID: Provider + ".teamvariables.update", Version: 1, Title: "Update a variable of the bound Make team",
	Description: "Set the type and value of one existing custom variable of the bound team; the name cannot be " +
		"changed. The variable is listed first and a system variable or an unknown name is refused without " +
		"changing anything. Sends one changing request and never repeats it. Refused on a connection with a " +
		"scenario allow-list. Needs the team-variables:write and team-variables:read scopes",
	Tags: []string{"make", "team", "variables", "update", "automation"},
	Risk: teamVariablesChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema + `,"type":` +
		variableTypeSchema + `,"value":` + variableValueSchema + `},"required":["name","type","value"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},"variable":` +
		variableSchema + `},"required":["team_id","variable"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, variableTypeArgument, variableValueArgument},
	Fields: append([]capability.Field{{Name: "team_id", Description: "The bound team"},
		{Name: "variable", Description: "The updated variable as Make reports it; fields: name, type, value, " +
			"is_system, truncated"}}, variableFields...),
	Examples: []capability.Example{{Description: "Change a number variable",
		Arguments: json.RawMessage(`{"name":"limit","type":"number","value":10}`)}},
}

var teamVariablesDelete = capability.Descriptor{
	ID: Provider + ".teamvariables.delete", Version: 1, Title: "Delete a variable of the bound Make team",
	Description: "Delete one custom variable of the bound team for good. The variable is listed first and a " +
		"system variable or an unknown name is refused. Make requires its confirmed flag, which is sent only " +
		"when confirmed is true. Scenarios of the team that read the variable by name lose its value; Qatlas " +
		"does not look them up, check them first. Sends one request and never repeats it. Offered only when a " +
		"connection's tools list names it, in no profile. Needs the team-variables:write and " +
		"team-variables:read scopes",
	Tags: []string{"make", "team", "variables", "delete", "automation"},
	Risk: teamVariablesChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema +
		`,"confirmed":{"type":"boolean"}},"required":["name","confirmed"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},` +
		`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["team_id","name","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, {Name: "confirmed", Required: true,
		Description: "Must be true: it sets Make's own confirmed flag and acknowledges that scenarios using " +
			"the variable by name lose its value"}},
	Fields: []capability.Field{
		{Name: "team_id", Description: "The bound team"},
		{Name: "name", Description: "The variable that was deleted"},
		{Name: "deleted", Description: "True when Make accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete a variable",
		Arguments: json.RawMessage(`{"name":"region","confirmed":true}`)}},
}

// VariableView is one variable with its capped value.
type VariableView struct {
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Value     json.RawMessage `json:"value,omitempty"`
	IsSystem  bool            `json:"is_system"`
	Truncated bool            `json:"truncated"`
}

type variableJSON struct {
	TypeID   int             `json:"typeId"`
	Name     string          `json:"name"`
	Value    json.RawMessage `json:"value"`
	IsSystem bool            `json:"isSystem"`
}

func variableViewOf(v variableJSON) VariableView {
	view := VariableView{Name: v.Name, IsSystem: v.IsSystem, Type: "other"}
	if name, ok := variableTypeNames[v.TypeID]; ok {
		view.Type = name
	}
	if len(view.Name) > maxVariableNameLength {
		view.Name, view.Truncated = view.Name[:maxVariableNameLength], true
	}
	capped, truncated := capPayload(v.Value, nil)
	view.Value = capped
	view.Truncated = view.Truncated || truncated
	return view
}

func variablesPath(teamID int64) string {
	return "/teams/" + strconv.FormatInt(teamID, 10) + "/variables"
}

func variablePath(teamID int64, name string) string {
	return variablesPath(teamID) + "/" + url.PathEscape(name)
}

// openTeamVariables checks the connection's scope before any secret is read: variables belong to the team,
// not to a scenario, so the narrower reading is that a connection with a scenario allow-list reaches none.
func openTeamVariables(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.scenarios) > 0 {
		return nil, invalidRequest("Make team variables are not scenario-bound; a connection with a scenario " +
			"allow-list cannot reach them")
	}
	return Open(ctx, resolved, secrets, red)
}

// fetchVariables reads the variables of the bound team.
func (c *Client) fetchVariables(ctx context.Context, op string) ([]variableJSON, error) {
	var answer struct {
		TeamVariables []variableJSON `json:"teamVariables"`
	}
	if err := c.get(ctx, op, variablesPath(c.scope.teamID), nil, &answer, needTeamVariablesRead); err != nil {
		return nil, err
	}
	return answer.TeamVariables, nil
}

// customVariable finds one custom variable by exact name, and refuses a system variable or an unknown name.
func (c *Client) customVariable(ctx context.Context, op, name string) error {
	variables, err := c.fetchVariables(ctx, op)
	if err != nil {
		return err
	}
	for _, v := range variables {
		if v.Name != name {
			continue
		}
		if v.IsSystem {
			return invalidRequest("this variable is a Make system variable and cannot be changed or deleted")
		}
		return nil
	}
	return invalidRequest(noCustomVariables)
}

// TeamVariables is the answer of teamvariables.list.
type TeamVariables struct {
	TeamID      int64          `json:"team_id"`
	Variables   []VariableView `json:"variables"`
	Count       int            `json:"count"`
	CustomCount int            `json:"custom_count"`
	SystemCount int            `json:"system_count"`
	Omitted     int            `json:"omitted,omitempty"`
}

func invokeTeamVariablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	const op = "list team variables"
	client, err := openTeamVariables(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	variables, err := client.fetchVariables(ctx, op)
	if err != nil {
		return nil, err
	}
	result := &TeamVariables{TeamID: client.scope.teamID, Variables: make([]VariableView, 0, len(variables))}
	for _, v := range variables {
		if len(result.Variables) >= maxVariablesListed {
			result.Omitted++
			continue
		}
		result.Variables = append(result.Variables, variableViewOf(v))
		if v.IsSystem {
			result.SystemCount++
		} else {
			result.CustomCount++
		}
	}
	result.Count = len(result.Variables)
	return result, nil
}

// variableArguments are the validated arguments of create and update.
type variableArguments struct {
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// variableValue checks a value against its declared type and returns the value to send.
func variableValue(kind string, raw json.RawMessage) (any, error) {
	invalid := func(message string) (any, error) { return nil, invalidRequest(message) }
	if len(raw) == 0 || string(raw) == "null" {
		return invalid("value must not be null")
	}
	switch kind {
	case "number":
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil || len(raw) == 0 || raw[0] == '"' {
			return invalid("value must be a number when type is number")
		}
		f, err := strconv.ParseFloat(n.String(), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return invalid("value must be a finite number")
		}
		return n, nil
	case "boolean":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return invalid("value must be true or false when type is boolean")
		}
		return b, nil
	case "text", "date":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return invalid("value must be a string when type is " + kind)
		}
		if len(s) > maxVariableTextBytes || !utf8.ValidString(s) {
			return invalid("value must be valid text of at most " + strconv.Itoa(maxVariableTextBytes) + " bytes")
		}
		if kind == "date" {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				if _, err := time.Parse("2006-01-02", s); err != nil {
					return invalid("value must be an RFC 3339 timestamp or YYYY-MM-DD when type is date")
				}
			}
		}
		return s, nil
	}
	return invalid("type must be number, text, boolean, or date")
}

func variableInput(raw json.RawMessage, op string) (variableArguments, any, error) {
	var input variableArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	if !variableNamePattern.MatchString(input.Name) {
		return input, nil, invalidRequest("name must be 1 to " + strconv.Itoa(maxVariableNameLength) +
			" letters, digits, dollar signs, or underscores")
	}
	value, err := variableValue(input.Type, input.Value)
	return input, value, err
}

// TeamVariableChange is the answer of teamvariables.create and teamvariables.update.
type TeamVariableChange struct {
	TeamID   int64        `json:"team_id"`
	Variable VariableView `json:"variable"`
}

func (c *Client) changeVariable(ctx context.Context, op, method, path string, body map[string]any,
	name string) (any, error) {
	var answer struct {
		TeamVariable variableJSON `json:"teamVariable"`
	}
	if err := c.change(ctx, op, method, path, nil, body, &answer, needTeamVariablesWrite,
		variablesUncertain); err != nil {
		return nil, err
	}
	if answer.TeamVariable.Name != name || answer.TeamVariable.IsSystem {
		return nil, invalidResponse(op, "Make did not report the changed variable"+variablesUncertain)
	}
	return &TeamVariableChange{TeamID: c.scope.teamID, Variable: variableViewOf(answer.TeamVariable)}, nil
}

func invokeTeamVariablesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create team variable"
	input, value, err := variableInput(raw, op)
	if err != nil {
		return nil, err
	}
	client, err := openTeamVariables(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"typeId": variableTypeIDs[input.Type], "name": input.Name, "value": value}
	return client.changeVariable(ctx, op, http.MethodPost, variablesPath(client.scope.teamID), body, input.Name)
}

func invokeTeamVariablesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update team variable"
	input, value, err := variableInput(raw, op)
	if err != nil {
		return nil, err
	}
	client, err := openTeamVariables(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.customVariable(ctx, op, input.Name); err != nil {
		return nil, err
	}
	body := map[string]any{"typeId": variableTypeIDs[input.Type], "value": value}
	return client.changeVariable(ctx, op, http.MethodPatch, variablePath(client.scope.teamID, input.Name), body,
		input.Name)
}

// TeamVariableDeletion is the answer of teamvariables.delete.
type TeamVariableDeletion struct {
	TeamID  int64  `json:"team_id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func invokeTeamVariablesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete team variable"
	var input struct {
		Name      string `json:"name"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !variableNamePattern.MatchString(input.Name) {
		return nil, invalidRequest("name must be 1 to " + strconv.Itoa(maxVariableNameLength) +
			" letters, digits, dollar signs, or underscores")
	}
	if !input.Confirmed {
		return nil, invalidRequest("confirmed must be true: deleting a variable removes its value for every " +
			"scenario of the team that reads it by name")
	}
	client, err := openTeamVariables(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.customVariable(ctx, op, input.Name); err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodDelete, variablePath(client.scope.teamID, input.Name),
		url.Values{"confirmed": {"true"}}, nil, nil, needTeamVariablesWrite, variablesUncertain); err != nil {
		return nil, err
	}
	return &TeamVariableDeletion{TeamID: client.scope.teamID, Name: input.Name, Deleted: true}, nil
}
