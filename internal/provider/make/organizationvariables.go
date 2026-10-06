package makeapi

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

// The four tools of this file list, create, update, and delete the variables of the organization mode
// connection's own bound organization, mirroring teamvariables.go and reusing its validation and views. None
// takes an organization argument. API (checked 2026-10-06 against developers.make.com's published API
// reference, section Organizations, not a live account):
//   - GET /organizations/{organizationId}/variables, organization-variables:read, answering
//     {"organizationVariables":[{typeId,name,value,isSystem}]} (the array shape is assumed from the team
//     variables; the reference calls the key an object).
//   - POST /organizations/{organizationId}/variables, organization-variables:write, body typeId, name, value,
//     answering {"organizationVariable":{...}}; needs the customVariables license.
//   - PATCH /organizations/{organizationId}/variables/{variableName}, organization-variables:write, optional
//     typeId and value, answering {"organizationVariable":{...}}.
//   - DELETE /organizations/{organizationId}/variables/{variableName}?confirmed=true,
//     organization-variables:write, answering {"ok":n}; confirmed=true is required.
// Narrower reading: update and delete first list the variables and refuse a system variable or an unknown
// name locally; renaming and the history endpoint are not offered.

const organizationVariablesSensitivity = "make-organization-variables-may-hold-secrets"

var organizationVariablesReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: organizationVariablesSensitivity}

func organizationVariablesChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: organizationVariablesSensitivity}
}

const (
	needOrganizationVariablesRead  = "the organization-variables:read scope"
	needOrganizationVariablesWrite = "the organization-variables:write scope (and organization-variables:read, which checks the variable first)"
	noCustomOrganizationVariables  = "the bound organization has no custom variable of this name; without the " +
		"customVariables license Make reports only system variables"
)

var organizationVariablesList = capability.Descriptor{
	ID: Provider + ".organizationvariables.list", Version: 1, Title: "List the variables of the bound Make organization",
	Description: "List the custom and system variables of the bound organization, each with name, type, capped value, " +
		"and is_system; takes no arguments, the organization is always the connection's own. Values may be secret. " +
		"Without the customVariables license Make reports only system variables, so custom_count 0 can mean " +
		"that the feature is unavailable. Offered only on an organization connection. Needs the " +
		"organization-variables:read scope",
	Tags: []string{"make", "organization", "variables", "list", "automation"}, Risk: organizationVariablesReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"variables":{"type":"array","items":` + variableSchema + `},"count":{"type":"integer"},` +
		`"custom_count":{"type":"integer"},"system_count":{"type":"integer"},"omitted":{"type":"integer"}},` +
		`"required":["organization_id","variables","count","custom_count","system_count"],"additionalProperties":false}`),
	Fields: append(append([]capability.Field{}, variableFields...),
		capability.Field{Name: "organization_id", Description: "The bound organization"},
		capability.Field{Name: "variables", Description: "Variables as Make reports them, capped; fields: name, " +
			"type, value, is_system, truncated"},
		capability.Field{Name: "count", Description: "Number of variables returned"},
		capability.Field{Name: "custom_count", Description: "Custom variables among the returned ones; 0 can also " +
			"mean that the organization's license has no custom variables"},
		capability.Field{Name: "system_count", Description: "System variables among the returned ones"},
		capability.Field{Name: "omitted", Description: "Variables beyond the " + strconv.Itoa(maxVariablesListed) +
			" returned ones, left out"}),
	Examples: []capability.Example{{Description: "List the bound organization's variables", Arguments: json.RawMessage(`{}`)}},
}

var organizationVariablesCreate = capability.Descriptor{
	ID: Provider + ".organizationvariables.create", Version: 1, Title: "Create a variable in the bound Make organization",
	Description: "Create one custom variable in the bound organization; the organization is always the connection's own. Needs " +
		"the customVariables license, which Make's reference ties to the plan. Sends one request and never " +
		"repeats it. Offered only on an organization connection. Needs the organization-variables:write scope",
	Tags: []string{"make", "organization", "variables", "create", "automation"},
	Risk: organizationVariablesChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema + `,"type":` +
		variableTypeSchema + `,"value":` + variableValueSchema + `},"required":["name","type","value"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},"variable":` +
		variableSchema + `},"required":["organization_id","variable"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, variableTypeArgument, variableValueArgument},
	Fields: append([]capability.Field{{Name: "organization_id", Description: "The bound organization"},
		{Name: "variable", Description: "The created variable as Make reports it; fields: name, type, value, " +
			"is_system, truncated"}}, variableFields...),
	Examples: []capability.Example{{Description: "Create a text variable",
		Arguments: json.RawMessage(`{"name":"region","type":"text","value":"eu"}`)}},
}

var organizationVariablesUpdate = capability.Descriptor{
	ID: Provider + ".organizationvariables.update", Version: 1, Title: "Update a variable of the bound Make organization",
	Description: "Set the type and value of one existing custom variable of the bound organization; the name cannot be " +
		"changed. The variable is listed first and a system variable or an unknown name is refused without " +
		"changing anything. Sends one changing request and never repeats it. Offered only on an organization " +
		"connection. Needs the organization-variables:write and organization-variables:read scopes",
	Tags: []string{"make", "organization", "variables", "update", "automation"},
	Risk: organizationVariablesChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema + `,"type":` +
		variableTypeSchema + `,"value":` + variableValueSchema + `},"required":["name","type","value"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},"variable":` +
		variableSchema + `},"required":["organization_id","variable"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, variableTypeArgument, variableValueArgument},
	Fields: append([]capability.Field{{Name: "organization_id", Description: "The bound organization"},
		{Name: "variable", Description: "The updated variable as Make reports it; fields: name, type, value, " +
			"is_system, truncated"}}, variableFields...),
	Examples: []capability.Example{{Description: "Change a number variable",
		Arguments: json.RawMessage(`{"name":"limit","type":"number","value":10}`)}},
}

var organizationVariablesDelete = capability.Descriptor{
	ID: Provider + ".organizationvariables.delete", Version: 1, Title: "Delete a variable of the bound Make organization",
	Description: "Delete one custom variable of the bound organization for good. The variable is listed first and a " +
		"system variable or an unknown name is refused. Make requires its confirmed flag, which is sent only " +
		"when confirmed is true. Scenarios of the organization that read the variable by name lose its value; Qatlas " +
		"does not look them up, check them first. Sends one request and never repeats it. Offered only when a " +
		"connection's tools list names it, in no profile. Needs the organization-variables:write and " +
		"organization-variables:read scopes",
	Tags: []string{"make", "organization", "variables", "delete", "automation"},
	Risk: organizationVariablesChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + variableNameSchema +
		`,"confirmed":{"type":"boolean"}},"required":["name","confirmed"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["organization_id","name","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{variableNameArgument, {Name: "confirmed", Required: true,
		Description: "Must be true: it sets Make's own confirmed flag and acknowledges that scenarios using " +
			"the variable by name lose its value"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "name", Description: "The variable that was deleted"},
		{Name: "deleted", Description: "True when Make accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete a variable",
		Arguments: json.RawMessage(`{"name":"region","confirmed":true}`)}},
}

func organizationVariablesPath(orgID int64) string {
	return "/organizations/" + strconv.FormatInt(orgID, 10) + "/variables"
}

func organizationVariablePath(orgID int64, name string) string {
	return organizationVariablesPath(orgID) + "/" + url.PathEscape(name)
}

func (c *Client) fetchOrganizationVariables(ctx context.Context, op string) ([]variableJSON, error) {
	var answer struct {
		Variables []variableJSON `json:"organizationVariables"`
	}
	if err := c.get(ctx, op, organizationVariablesPath(c.scope.orgID), nil, &answer,
		needOrganizationVariablesRead); err != nil {
		return nil, err
	}
	return answer.Variables, nil
}

// customOrganizationVariable refuses a system variable or an unknown name.
func (c *Client) customOrganizationVariable(ctx context.Context, op, name string) error {
	variables, err := c.fetchOrganizationVariables(ctx, op)
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
	return invalidRequest(noCustomOrganizationVariables)
}

// OrganizationVariables is the answer of organizationvariables.list.
type OrganizationVariables struct {
	OrganizationID int64          `json:"organization_id"`
	Variables      []VariableView `json:"variables"`
	Count          int            `json:"count"`
	CustomCount    int            `json:"custom_count"`
	SystemCount    int            `json:"system_count"`
	Omitted        int            `json:"omitted,omitempty"`
}

func invokeOrganizationVariablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	variables, err := client.fetchOrganizationVariables(ctx, "list organization variables")
	if err != nil {
		return nil, err
	}
	result := &OrganizationVariables{OrganizationID: client.scope.orgID,
		Variables: make([]VariableView, 0, len(variables))}
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

// OrganizationVariableChange is the answer of organizationvariables.create and .update.
type OrganizationVariableChange struct {
	OrganizationID int64        `json:"organization_id"`
	Variable       VariableView `json:"variable"`
}

func (c *Client) changeOrganizationVariable(ctx context.Context, op, method, path string, body map[string]any,
	name string) (any, error) {
	var answer struct {
		Variable variableJSON `json:"organizationVariable"`
	}
	if err := c.change(ctx, op, method, path, nil, body, &answer, needOrganizationVariablesWrite,
		variablesUncertain); err != nil {
		return nil, err
	}
	if answer.Variable.Name != name || answer.Variable.IsSystem {
		return nil, invalidResponse(op, "Make did not report the changed variable"+variablesUncertain)
	}
	return &OrganizationVariableChange{OrganizationID: c.scope.orgID, Variable: variableViewOf(answer.Variable)}, nil
}

func invokeOrganizationVariablesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create organization variable"
	input, value, err := variableInput(raw, op)
	if err != nil {
		return nil, err
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"typeId": variableTypeIDs[input.Type], "name": input.Name, "value": value}
	return client.changeOrganizationVariable(ctx, op, http.MethodPost,
		organizationVariablesPath(client.scope.orgID), body, input.Name)
}

func invokeOrganizationVariablesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update organization variable"
	input, value, err := variableInput(raw, op)
	if err != nil {
		return nil, err
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.customOrganizationVariable(ctx, op, input.Name); err != nil {
		return nil, err
	}
	body := map[string]any{"typeId": variableTypeIDs[input.Type], "value": value}
	return client.changeOrganizationVariable(ctx, op, http.MethodPatch,
		organizationVariablePath(client.scope.orgID, input.Name), body, input.Name)
}

// OrganizationVariableDeletion is the answer of organizationvariables.delete.
type OrganizationVariableDeletion struct {
	OrganizationID int64  `json:"organization_id"`
	Name           string `json:"name"`
	Deleted        bool   `json:"deleted"`
}

func invokeOrganizationVariablesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete organization variable"
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
			"scenario of the organization that reads it by name")
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.customOrganizationVariable(ctx, op, input.Name); err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodDelete, organizationVariablePath(client.scope.orgID, input.Name),
		url.Values{"confirmed": {"true"}}, nil, nil, needOrganizationVariablesWrite, variablesUncertain); err != nil {
		return nil, err
	}
	return &OrganizationVariableDeletion{OrganizationID: client.scope.orgID, Name: input.Name, Deleted: true}, nil
}
