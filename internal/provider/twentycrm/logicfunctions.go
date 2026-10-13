package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	logicFunctionsGroup = "logicfunctions"

	// logicFunctionDataSensitivity classifies the logic function metadata of a workspace.
	logicFunctionDataSensitivity = "twentycrm-logic-function-data"

	logicFunctionsMax           = 200
	logicFunctionNameMax        = 100
	logicFunctionDescriptionMax = 500
	logicFunctionShortMax       = 50
)

// logicFunctionSelection lists the only fields read. Twenty exposes the trigger kinds solely as JSON fields
// that hold their settings (paths, schedules, event names, input schemas) and cannot narrow them; the answer
// is reduced to whether each is set, and the settings are never decoded. Source code, handler paths, logs,
// and application and creator identifiers are not selected.
const logicFunctionSelection = `id name description runtime timeoutSeconds executionMode cronTriggerSettings ` +
	`databaseEventTriggerSettings httpRouteTriggerSettings toolTriggerSettings workflowActionTriggerSettings`

// logicFunctionsListDocument and logicFunctionsGetDocument are the only documents of the logic function tools.
var logicFunctionsListDocument = graphqlDocument{path: metadataPath,
	text: `query FindLogicFunctions { findManyLogicFunctions { ` + logicFunctionSelection + ` } }`}

var logicFunctionsGetDocument = graphqlDocument{path: metadataPath,
	text: `query FindLogicFunction($input: LogicFunctionIdInput!) { findOneLogicFunction(input: $input) { ` +
		logicFunctionSelection + ` } }`}

const logicFunctionItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"runtime":{"type":"string"},"timeout_seconds":{"type":"integer"},` +
	`"execution_mode":{"type":"string"},"triggers":{"type":"object","properties":{"cron":{"type":"boolean"},` +
	`"database_event":{"type":"boolean"},"http_route":{"type":"boolean"},"tool":{"type":"boolean"},` +
	`"workflow_action":{"type":"boolean"}},"required":["cron","database_event","http_route","tool",` +
	`"workflow_action"],"additionalProperties":false}},"required":["id","name","triggers"],` +
	`"additionalProperties":false}`

const logicFunctionNote = "Never reads source code, handler paths, logs, or trigger settings such as routes, " +
	"schedules, event names, or input schemas: each trigger kind is only a flag. Values are untrusted workspace data"

var logicFunctionsList = capability.Descriptor{
	ID:      Provider + ".logicfunctions.list",
	Version: 1,
	Title:   "List Twenty CRM logic functions",
	Description: "List the logic functions of the Twenty workspace of a connection without object targets: name, " +
		"description, runtime, timeout, execution mode, and which trigger kinds each carries (cron, database " +
		"event, HTTP route, tool, workflow action). " + logicFunctionNote,
	Tags:        []string{"twentycrm", "logic functions", "workspace", "list"},
	Risk:        capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: logicFunctionDataSensitivity},
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"logic_functions":{"type":"array","items":` +
		logicFunctionItemSchema + `},"truncated":{"type":"boolean"}},"required":["logic_functions","truncated"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "logic_functions", Description: "At most 200 logic functions, untrusted data"},
		{Name: "truncated", Description: "True when the workspace holds more logic functions than listed"},
	},
	Examples: []capability.Example{{Description: "List the logic functions", Arguments: json.RawMessage(`{}`)}},
}

var logicFunctionsGet = capability.Descriptor{
	ID:      Provider + ".logicfunctions.get",
	Version: 1,
	Title:   "Get a Twenty CRM logic function",
	Description: "Read one logic function of the Twenty workspace of a connection without object targets: name, " +
		"description, runtime, timeout, execution mode, and which trigger kinds it carries. " + logicFunctionNote,
	Tags:     []string{"twentycrm", "logic functions", "workspace", "get"},
	Risk:     capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: logicFunctionDataSensitivity},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema +
		`},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(logicFunctionItemSchema),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "UUID of the logic function, as returned by twentycrm.logicfunctions.list"}},
	Fields: []capability.Field{{Name: "triggers", Description: "One flag per trigger kind, without its settings"}},
	Examples: []capability.Example{{Description: "Read a logic function",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// LogicFunctionTriggers tells which trigger kinds a logic function carries.
type LogicFunctionTriggers struct {
	Cron           bool `json:"cron"`
	DatabaseEvent  bool `json:"database_event"`
	HTTPRoute      bool `json:"http_route"`
	Tool           bool `json:"tool"`
	WorkflowAction bool `json:"workflow_action"`
}

// LogicFunction is the stable view of one logic function.
type LogicFunction struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Runtime        string                `json:"runtime,omitempty"`
	TimeoutSeconds int                   `json:"timeout_seconds,omitempty"`
	ExecutionMode  string                `json:"execution_mode,omitempty"`
	Triggers       LogicFunctionTriggers `json:"triggers"`
}

// LogicFunctionList is the result of ListLogicFunctions.
type LogicFunctionList struct {
	LogicFunctions []LogicFunction `json:"logic_functions"`
	Truncated      bool            `json:"truncated"`
}

type logicFunctionRecord struct {
	ID                            string          `json:"id"`
	Name                          *string         `json:"name"`
	Description                   *string         `json:"description"`
	Runtime                       *string         `json:"runtime"`
	TimeoutSeconds                float64         `json:"timeoutSeconds"`
	ExecutionMode                 *string         `json:"executionMode"`
	CronTriggerSettings           json.RawMessage `json:"cronTriggerSettings"`
	DatabaseEventTriggerSettings  json.RawMessage `json:"databaseEventTriggerSettings"`
	HTTPRouteTriggerSettings      json.RawMessage `json:"httpRouteTriggerSettings"`
	ToolTriggerSettings           json.RawMessage `json:"toolTriggerSettings"`
	WorkflowActionTriggerSettings json.RawMessage `json:"workflowActionTriggerSettings"`
}

// isSet reports whether a trigger settings field holds a value; the value itself is dropped unread.
func isSet(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func (r logicFunctionRecord) view(op string) (LogicFunction, error) {
	if !validUUID(r.ID) {
		return LogicFunction{}, provider.InvalidResponse(op, "Twenty returned a logic function without a usable identifier")
	}
	timeout := 0
	if r.TimeoutSeconds > 0 && r.TimeoutSeconds < 1e6 {
		timeout = int(r.TimeoutSeconds)
	}
	return LogicFunction{ID: r.ID, Name: cleanText(r.Name, logicFunctionNameMax),
		Description: cleanText(r.Description, logicFunctionDescriptionMax),
		Runtime:     cleanText(r.Runtime, logicFunctionShortMax), TimeoutSeconds: timeout,
		ExecutionMode: cleanText(r.ExecutionMode, logicFunctionShortMax),
		Triggers: LogicFunctionTriggers{Cron: isSet(r.CronTriggerSettings),
			DatabaseEvent: isSet(r.DatabaseEventTriggerSettings), HTTPRoute: isSet(r.HTTPRouteTriggerSettings),
			Tool: isSet(r.ToolTriggerSettings), WorkflowAction: isSet(r.WorkflowActionTriggerSettings)}}, nil
}

// ListLogicFunctions reads the logic functions with one fixed query.
func (c *Client) ListLogicFunctions(ctx context.Context) (*LogicFunctionList, error) {
	const op = "list logic functions"
	var data struct {
		Functions []logicFunctionRecord `json:"findManyLogicFunctions"`
	}
	if err := c.graphql(ctx, op, logicFunctionsListDocument, map[string]any{}, &data); err != nil {
		return nil, err
	}
	if data.Functions == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	result := &LogicFunctionList{LogicFunctions: make([]LogicFunction, 0, len(data.Functions))}
	for i, record := range data.Functions {
		view, err := record.view(op)
		if err != nil {
			return nil, err
		}
		if i == logicFunctionsMax {
			result.Truncated = true
			break
		}
		result.LogicFunctions = append(result.LogicFunctions, view)
	}
	return result, nil
}

// GetLogicFunction reads one logic function with one fixed query.
func (c *Client) GetLogicFunction(ctx context.Context, id string) (*LogicFunction, error) {
	const op = "get logic function"
	if !validUUID(id) {
		return nil, invalidRequest("the logic function id must be a UUID")
	}
	var data struct {
		Function *logicFunctionRecord `json:"findOneLogicFunction"`
	}
	variables := map[string]any{"input": map[string]any{"id": id}}
	if err := c.graphql(ctx, op, logicFunctionsGetDocument, variables, &data); err != nil {
		return nil, err
	}
	if data.Function == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an invalid response")
	}
	view, err := data.Function.view(op)
	if err != nil {
		return nil, err
	}
	if !equalUUID(view.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different logic function than the requested one")
	}
	return &view, nil
}

func invokeLogicFunctionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListLogicFunctions(ctx)
}

func invokeLogicFunctionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get logic function", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the logic function id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetLogicFunction(ctx, args.ID)
}
