package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The Public API's own limits for a variable (createVariable and updateVariable request bodies): a key of 1
// to 50 characters and a value of at most 1000 characters.
const (
	maxVariableKeyLength   = 50
	maxVariableValueLength = 1000
	// maxVariableLookupPages bounds the pages update and delete read to find one variable by ID, because the
	// Public API has no GET /variables/{id}.
	maxVariableLookupPages = 20
)

// variableDataSensitivity marks variables: plain-text configuration values that are returned in clear.
const variableDataSensitivity = "n8n-variables"

// variablePermissionMessage is the one message of a 403 on a variables endpoint. n8n answers 403 for a
// missing license (feat:variables), a missing API key scope (variable:*), and a role without the right to
// manage variables; the body is never read into a message.
const variablePermissionMessage = "n8n refused this variables operation: license or role missing (the " +
	"instance's feat:variables license, the API key's variable scope, or its owner's role); Qatlas cannot " +
	"tell which"

var variableReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: variableDataSensitivity}

func variableChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: variableDataSensitivity}
}

const variableKeySchema = `{"type":"string","minLength":1,"maxLength":50,"pattern":"^[A-Za-z0-9_]+$"}`

var variableValueSchema = `{"type":"string","maxLength":` + strconv.Itoa(maxVariableValueLength) + `}`

var variableItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"key":{"type":"string"},` +
	`"value":{"type":"string"},"scope":{"type":"string","enum":["project","global"]},` +
	`"project_id":{"type":"string"}},"required":["id","key","value","scope"],"additionalProperties":false}`

var variableIDArgument = capability.Argument{Name: "variable_id",
	Description: "n8n variable identifier; the variable is read first and must be a variable of this " +
		"connection's project allow-list, or a global one on a connection without project and workflow targets",
	Required: true}

var variableKeyArgument = capability.Argument{Name: "key",
	Description: "Variable key, 1 to 50 characters of letters, digits, and '_'; a new key must not start with a digit",
	Required:    true}

var variableValueArgument = capability.Argument{Name: "value",
	Description: "Variable value, a string of at most 1000 characters; stored and read back in plain text",
	Required:    true}

var variablesList = capability.Descriptor{
	ID: Provider + ".variables.list", Version: 1, Title: "List n8n variables",
	Description: "List the variables (key and plain-text value, untrusted data) of the bound n8n instance; page " +
		"by page with an opaque cursor, optionally of one project. With a project allow-list only variables of " +
		"those projects are shown, never global ones. Refused on a connection that restricts workflows by an " +
		"allow-list",
	Tags: []string{"n8n", "variables", "list", "automation"}, Risk: variableReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"variables":{"type":"array","items":` + variableItemSchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["variables","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "project_id", Description: "Only the variables of this project; must be inside the project allow-list"},
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Variables per page, 1 to 250; 100 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "variables", Description: "Variables on this page after the allow-list was applied; id, key, value, " +
			"scope (project or global), and project_id for a project variable; key and value are untrusted data"},
		{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		{Name: "has_more", Description: "True when a further page remains, even when this page is empty after filtering"},
		{Name: "count", Description: "Number of variables on this page"},
	},
	Examples: []capability.Example{{Description: "List the first page of reachable variables", Arguments: json.RawMessage(`{}`)}},
}

var variablesCreate = capability.Descriptor{
	ID: Provider + ".variables.create", Version: 1, Title: "Create an n8n variable",
	Description: "Create one variable from a key and a value: with project_id a project variable, without it a " +
		"global one. With a project allow-list project_id is required and must be on it; a global variable can " +
		"only be created on a connection without project and workflow targets. n8n reports no ID of the new " +
		"variable; list to find it. A repeated call may be refused as a key conflict. Refused on a connection " +
		"that restricts workflows by an allow-list",
	Tags: []string{"n8n", "variables", "create", "automation"},
	Risk: variableChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"key":` + variableKeySchema + `,` +
		`"value":` + variableValueSchema + `,"project_id":` + targetIDSchema + `},` +
		`"required":["key","value"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"key":{"type":"string"},"scope":{"type":"string","enum":["project","global"]},` +
		`"project_id":{"type":"string"},"created":{"type":"boolean"}},` +
		`"required":["key","scope","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableKeyArgument, variableValueArgument,
		{Name: "project_id", Description: "Project of the variable; omitted means global, which is refused with a " +
			"project allow-list"}},
	Fields: []capability.Field{
		{Name: "key", Description: "The key that was sent"},
		{Name: "scope", Description: "project or global"},
		{Name: "project_id", Description: "Project of the variable; absent for a global variable"},
		{Name: "created", Description: "True when n8n accepted the creation"},
	},
	Examples: []capability.Example{{Description: "Create a project variable",
		Arguments: json.RawMessage(`{"key":"API_BASE","value":"https://example.invalid","project_id":"VmwOO9HeTEj20kxM"}`)}},
}

var variablesUpdate = capability.Descriptor{
	ID: Provider + ".variables.update", Version: 1, Title: "Update an n8n variable",
	Description: "Replace the key and value of one variable, keeping its project or global status; it cannot be " +
		"moved. The variable is read first (by paging the list) and must be a variable of the project " +
		"allow-list, or a global one on a connection without project and workflow targets. Refused on a " +
		"connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "variables", "update", "automation"},
	Risk: variableChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"variable_id":` + targetIDSchema + `,` +
		`"key":` + variableKeySchema + `,"value":` + variableValueSchema + `},` +
		`"required":["variable_id","key","value"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"key":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["id","key","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableIDArgument, variableKeyArgument, variableValueArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the updated variable"},
		{Name: "key", Description: "The key that was sent"},
		{Name: "updated", Description: "True when n8n accepted the change"},
	},
	Examples: []capability.Example{{Description: "Update a variable",
		Arguments: json.RawMessage(`{"variable_id":"abcd1234efgh5678","key":"API_BASE","value":"https://example.invalid"}`)}},
}

var variablesDelete = capability.Descriptor{
	ID: Provider + ".variables.delete", Version: 1, Title: "Delete an n8n variable",
	Description: "Delete one variable of the bound n8n instance; workflows that use it then fail to resolve it. " +
		"The variable is read first (by paging the list) and must be a variable of the project allow-list, or " +
		"a global one on a connection without project and workflow targets. Refused on a connection that " +
		"restricts workflows by an allow-list",
	Tags: []string{"n8n", "variables", "delete", "automation"},
	Risk: variableChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"variable_id":` + targetIDSchema + `},` +
		`"required":["variable_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{variableIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted variable"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a variable",
		Arguments: json.RawMessage(`{"variable_id":"abcd1234efgh5678"}`)}},
}

type variableJSON struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Project *struct {
		ID string `json:"id"`
	} `json:"project"`
}

func (v variableJSON) projectID() string {
	if v.Project == nil {
		return ""
	}
	return v.Project.ID
}

type variablesPageJSON struct {
	Data       []variableJSON `json:"data"`
	NextCursor *string        `json:"nextCursor"`
}

// Variable is the stable Qatlas view of one variable. Key and Value are untrusted data.
type Variable struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
}

// VariablesPage is one paginated, allow-list-filtered listing of variables.
type VariablesPage struct {
	Variables []Variable `json:"variables"`
	Cursor    string     `json:"cursor,omitempty"`
	HasMore   bool       `json:"has_more"`
	Count     int        `json:"count"`
}

// VariableCreated, VariableUpdated, and VariableDeleted are what the changing tools report.
type VariableCreated struct {
	Key       string `json:"key"`
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
	Created   bool   `json:"created"`
}

type VariableUpdated struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Updated bool   `json:"updated"`
}

type VariableDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// variableError replaces the generic 403 message with the neutral license, scope, or role message.
func variableError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = variablePermissionMessage
	}
	return err
}

// selectVariables decides locally, before any secret or request, whether this connection may use variables
// at all. A connection that restricts workflows by an allow-list refuses every variable tool: a variable is
// not a workflow and Qatlas cannot tie it to one (narrower reading, as for data tables).
func selectVariables(resolved *config.Resolved) (scope, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return scope{}, err
	}
	if len(bound.workflows) > 0 {
		return scope{}, invalidRequest("this connection restricts workflows by an allow-list, so it cannot use variables")
	}
	return bound, nil
}

// allowsVariable applies the binding: with a project allow-list only variables of those projects, never a
// global one; without it every variable. Workflow targets were refused by selectVariables.
func (s scope) allowsVariable(projectID string) bool {
	if len(s.projects) == 0 {
		return true
	}
	return projectID != "" && s.allowsProject(projectID)
}

// validVariableKey checks a key by n8n's rule: 1 to 50 characters of letters, digits, and '_'; a new key
// must not start with a digit (createVariable), an updated key only needs the character class.
func validVariableKey(key string, isNew bool) error {
	if len(key) == 0 || len(key) > maxVariableKeyLength {
		return invalidRequest("key must be 1 to " + strconv.Itoa(maxVariableKeyLength) + " characters")
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && !(isNew && i == 0):
		default:
			if isNew {
				return invalidRequest("key must be letters, digits, and '_' only, not starting with a digit")
			}
			return invalidRequest("key must be letters, digits, and '_' only")
		}
	}
	return nil
}

func validVariableValue(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxVariableValueLength {
		return invalidRequest("value must be valid text of at most " + strconv.Itoa(maxVariableValueLength) + " characters")
	}
	for _, r := range value {
		if r == 0 || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			return invalidRequest("value must not contain control characters")
		}
	}
	return nil
}

func summarizeVariable(v variableJSON) Variable {
	out := Variable{ID: bounded(v.ID), Key: bounded(v.Key), Value: bounded(v.Value), Scope: "global"}
	if p := v.projectID(); p != "" {
		out.Scope, out.ProjectID = "project", bounded(p)
	}
	return out
}

type variableArguments struct {
	VariableID string `json:"variable_id"`
	ProjectID  string `json:"project_id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
}

func readVariableArguments(op string, raw json.RawMessage) (variableArguments, error) {
	var input variableArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeVariablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readVariableArguments("list variables", raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectVariables(resolved)
	if err != nil {
		return nil, err
	}
	if input.ProjectID != "" {
		if !validTargetID(input.ProjectID) {
			return nil, invalidRequest("project_id must be a usable n8n identifier")
		}
		if !bound.allowsProject(input.ProjectID) {
			return nil, invalidRequest("project_id is outside the targets of this connection")
		}
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListVariables(ctx, input.ProjectID, input.Cursor, limit)
	return page, variableError(err)
}

func (c *Client) variablesPage(ctx context.Context, op, projectID, cursor string, limit int) (*variablesPageJSON, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if projectID != "" {
		query.Set("projectId", projectID)
	}
	var page variablesPageJSON
	if err := c.get(ctx, op, "/variables", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	if page.NextCursor != nil && len(*page.NextCursor) > maxCursorLength {
		return nil, invalidResponse(op, "n8n reported an oversized page cursor")
	}
	return &page, nil
}

// ListVariables reads one page of GET /variables, with n8n's projectId filter when a project is named, and
// keeps only the variables this connection's binding allows.
func (c *Client) ListVariables(ctx context.Context, projectID, cursor string, limit int) (*VariablesPage, error) {
	page, err := c.variablesPage(ctx, "list variables", projectID, cursor, limit)
	if err != nil {
		return nil, err
	}
	variables := make([]Variable, 0, len(page.Data))
	for _, v := range page.Data {
		if c.scope.allowsVariable(v.projectID()) {
			variables = append(variables, summarizeVariable(v))
		}
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return &VariablesPage{Variables: variables, Cursor: next, HasMore: next != "", Count: len(variables)}, nil
}

// readVariable finds one variable by ID through the paged list (the Public API has no GET /variables/{id})
// and checks it against the binding. A variable that is absent within the bounded lookup and a variable
// outside the binding are refused alike, without naming its project.
func (c *Client) readVariable(ctx context.Context, op, id string) (*variableJSON, error) {
	cursor := ""
	for i := 0; i < maxVariableLookupPages; i++ {
		page, err := c.variablesPage(ctx, op, "", cursor, maxListLimit)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Data {
			if v.ID != id {
				continue
			}
			if !c.scope.allowsVariable(v.projectID()) {
				return nil, invalidRequest("the variable is outside the targets of this connection")
			}
			return &v, nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	return nil, invalidRequest("the variable was not found among the variables this connection can reach")
}

func invokeVariablesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create variable"
	input, err := readVariableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectVariables(resolved)
	if err != nil {
		return nil, err
	}
	if err := validVariableKey(input.Key, true); err != nil {
		return nil, err
	}
	if err := validVariableValue(input.Value); err != nil {
		return nil, err
	}
	if input.ProjectID != "" && !validTargetID(input.ProjectID) {
		return nil, invalidRequest("project_id must be a usable n8n identifier")
	}
	if len(bound.projects) > 0 {
		if input.ProjectID == "" {
			return nil, invalidRequest("this connection restricts projects by an allow-list, so project_id is " +
				"required and global variables are refused")
		}
		if !bound.allowsProject(input.ProjectID) {
			return nil, invalidRequest("project_id is outside the targets of this connection")
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"key": input.Key, "value": input.Value}
	if input.ProjectID != "" {
		body["projectId"] = input.ProjectID
	}
	if err := client.change(ctx, op, http.MethodPost, "/variables", nil, body, nil, maxResponseBytes); err != nil {
		return nil, variableError(err)
	}
	created := &VariableCreated{Key: input.Key, Scope: "global", Created: true}
	if input.ProjectID != "" {
		created.Scope, created.ProjectID = "project", input.ProjectID
	}
	return created, nil
}

func invokeVariablesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update variable"
	input, err := readVariableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectVariables(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.VariableID) {
		return nil, invalidRequest("variable_id must be a usable n8n identifier")
	}
	if err := validVariableKey(input.Key, false); err != nil {
		return nil, err
	}
	if err := validVariableValue(input.Value); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	existing, err := client.readVariable(ctx, op, input.VariableID)
	if err != nil {
		return nil, variableError(err)
	}
	// The variable keeps its binding: its own project, or null for a global one, is sent back unchanged.
	var projectID any
	if p := existing.projectID(); p != "" {
		projectID = p
	}
	body := map[string]any{"key": input.Key, "value": input.Value, "projectId": projectID}
	if err := client.change(ctx, op, http.MethodPut, "/variables/"+url.PathEscape(input.VariableID), nil, body, nil,
		maxResponseBytes); err != nil {
		return nil, variableError(err)
	}
	return &VariableUpdated{ID: input.VariableID, Key: input.Key, Updated: true}, nil
}

func invokeVariablesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete variable"
	input, err := readVariableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectVariables(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.VariableID) {
		return nil, invalidRequest("variable_id must be a usable n8n identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.readVariable(ctx, op, input.VariableID); err != nil {
		return nil, variableError(err)
	}
	if err := client.change(ctx, op, http.MethodDelete, "/variables/"+url.PathEscape(input.VariableID), nil, nil, nil,
		maxResponseBytes); err != nil {
		return nil, variableError(err)
	}
	return &VariableDeleted{ID: input.VariableID, Deleted: true}, nil
}
