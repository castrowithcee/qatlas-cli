package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Activation is allowed only for versions that open no lasting path to the outside through Qatlas. The lists
// are positive: a type that is not named here is refused, whatever it is.
var (
	activatableTriggerTypes = []string{"MANUAL", "DATABASE_EVENT", "CRON"}
	activatableStepTypes    = []string{"CREATE_RECORD", "UPDATE_RECORD", "DELETE_RECORD", "UPSERT_RECORD",
		"FIND_RECORDS", "PICK_RECORD", "FORM", "FILTER", "IF_ELSE", "ITERATOR", "EMPTY", "DELAY", "WAIT_FOR_EVENT",
		"DRAFT_EMAIL", "CREATE_CALENDAR_EVENT"}
)

// The fixed documents of the four tools, sent to the core endpoint. Only a validated UUID is a variable.
var (
	activateVersionDocument = graphqlDocument{path: graphqlPath, text: "mutation ActivateWorkflowVersion(" +
		"$workflowVersionId: UUID!) { activateWorkflowVersion(workflowVersionId: $workflowVersionId) }"}
	deactivateVersionDocument = graphqlDocument{path: graphqlPath, text: "mutation DeactivateWorkflowVersion(" +
		"$workflowVersionId: UUID!) { deactivateWorkflowVersion(workflowVersionId: $workflowVersionId) }"}
	stopRunDocument = graphqlDocument{path: graphqlPath, text: "mutation StopWorkflowRun($workflowRunId: UUID!) " +
		"{ stopWorkflowRun(workflowRunId: $workflowRunId) { id status } }"}
	retryRunDocument = graphqlDocument{path: graphqlPath, text: "mutation RetryWorkflowRun($workflowRunId: UUID!) " +
		"{ retryWorkflowRun(workflowRunId: $workflowRunId) { id status } }"}
)

const (
	workflowUncertain = "; this change may have taken effect, read the version with twentycrm.workflows.get or " +
		"the run with twentycrm.workflowruns.list before repeating it"

	errWorkflowPermission = "the workspace role of this API key may not control workflows; check the role in " +
		"Twenty, the right is called Workflows"

	errWorkflowVersionID = "workflow_version_id must be a workflow version identifier in UUID form"
	errWorkflowRunID     = "workflow_run_id must be a workflow run identifier in UUID form"

	workflowControlNote = "Needs the Twenty settings right Workflows and a connection without object targets, and " +
		"is offered only by a connection whose tools list names it. Sent once; after an unclear result read before repeating"

	versionIDInput = `{"type":"object","properties":{"workflow_version_id":` + uuidSchema +
		`},"required":["workflow_version_id"],"additionalProperties":false}`
	runIDInput = `{"type":"object","properties":{"workflow_run_id":` + uuidSchema +
		`},"required":["workflow_run_id"],"additionalProperties":false}`
	runResultSchema = `{"type":"object","properties":{"id":{"type":"string"},"status":` +
		`{"type":"string"}},"required":["id","status"],"additionalProperties":false}`
)

func workflowControlRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: workflowDataSensitivity}
}

var versionIDArgument = capability.Argument{Name: "workflow_version_id", Required: true,
	Description: "UUID of the workflow version, as returned by twentycrm.workflows.get"}

var runIDArgument = capability.Argument{Name: "workflow_run_id", Required: true,
	Description: "UUID of the workflow run, as returned by twentycrm.workflowruns.list"}

var workflowVersionsActivate = capability.Descriptor{
	ID: Provider + ".workflowversions.activate", Version: 1, Title: "Activate a Twenty CRM workflow version",
	Description: "Activate one workflow version of the Twenty workspace of a connection. Qatlas reads the version " +
		"first and activates it only when its trigger is one of " + strings.Join(activatableTriggerTypes, ", ") +
		" and every step is one of " + strings.Join(activatableStepTypes, ", ") + "; any other version is refused " +
		"without a change, because it would open a lasting path to the outside. The version is read once before " +
		"the change and can differ at activation. " + workflowControlNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "activate"},
	Risk:     workflowControlRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(versionIDInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"activated":{"type":"boolean"}},` +
		`"required":["activated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{versionIDArgument},
	Examples: []capability.Example{{Description: "Activate a version",
		Arguments: json.RawMessage(`{"workflow_version_id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

var workflowVersionsDeactivate = capability.Descriptor{
	ID: Provider + ".workflowversions.deactivate", Version: 1, Title: "Deactivate a Twenty CRM workflow version",
	Description: "Deactivate one workflow version of the Twenty workspace of a connection so that its trigger " +
		"no longer starts runs. " + workflowControlNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "deactivate"},
	Risk:     workflowControlRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(versionIDInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deactivated":{"type":"boolean"}},` +
		`"required":["deactivated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{versionIDArgument},
	Examples: []capability.Example{{Description: "Deactivate a version",
		Arguments: json.RawMessage(`{"workflow_version_id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

var workflowRunsStop = capability.Descriptor{
	ID: Provider + ".workflowruns.stop", Version: 1, Title: "Stop a Twenty CRM workflow run",
	Description: "Stop one workflow run of the Twenty workspace of a connection. Returns the run id and its " +
		"status; steps that already ran stay done. " + workflowControlNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "runs", "stop"},
	Risk:     workflowControlRisk(capability.EffectExecute, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(runIDInput), OutputSchema: json.RawMessage(runResultSchema),
	Arguments: []capability.Argument{runIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "The run that was stopped"},
		{Name: "status", Description: "Status after the request, one of " + strings.Join(workflowRunStatuses, ", ") +
			", or unknown"},
	},
	Examples: []capability.Example{{Description: "Stop a run",
		Arguments: json.RawMessage(`{"workflow_run_id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

var workflowRunsRetry = capability.Descriptor{
	ID: Provider + ".workflowruns.retry", Version: 1, Title: "Retry a Twenty CRM workflow run",
	Description: "Retry one workflow run of the Twenty workspace of a connection. The run executes its steps " +
		"again, including steps with effects outside Twenty such as sent emails or HTTP requests, which are " +
		"repeated. Not idempotent. " + workflowControlNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "runs", "retry"},
	Risk:     workflowControlRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(runIDInput), OutputSchema: json.RawMessage(runResultSchema),
	Arguments: []capability.Argument{runIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "The run that was retried"},
		{Name: "status", Description: "Status after the request, one of " + strings.Join(workflowRunStatuses, ", ") +
			", or unknown"},
	},
	Examples: []capability.Example{{Description: "Retry a run",
		Arguments: json.RawMessage(`{"workflow_run_id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

// workflowControlPermission names the settings right on a permission failure.
func workflowControlPermission(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = errWorkflowPermission
	}
	return err
}

// checkActivatable decides from the types alone, never from names or settings. It returns a refusal that
// names at most a fixed Twenty type (typeOf reads unknown otherwise), or nil.
func checkActivatable(trigger, steps json.RawMessage) error {
	refuse := func(kind, name string) error {
		return invalidRequest("this version is not activated through Qatlas: its " + kind + " (" + name +
			") is not on the list of internal types; activate it in Twenty if it is meant to reach the outside")
	}
	if trimmed := strings.TrimSpace(string(trigger)); trimmed == "" || trimmed == "null" {
		return refuse("trigger", workflowUnknown)
	}
	var typed struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(trigger, &typed) != nil || !contains(activatableTriggerTypes, typed.Type) {
		return refuse("trigger", typeOf(trigger, workflowTriggerTypes))
	}
	var list []json.RawMessage
	if trimmed := strings.TrimSpace(string(steps)); trimmed == "" || trimmed == "null" ||
		json.Unmarshal(steps, &list) != nil {
		return refuse("steps", workflowUnknown)
	}
	for _, step := range list {
		var t struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(step, &t) != nil || !contains(activatableStepTypes, t.Type) {
			return refuse("step", typeOf(step, workflowStepTypes))
		}
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// ActivateWorkflowVersion reads the version once, refuses anything but an internal one, and then sends exactly
// one mutation.
func (c *Client) ActivateWorkflowVersion(ctx context.Context, id string) error {
	const op = "activate workflow version"
	var one struct {
		Data struct {
			Version struct {
				ID      string          `json:"id"`
				Trigger json.RawMessage `json:"trigger"`
				Steps   json.RawMessage `json:"steps"`
			} `json:"workflowVersion"`
		} `json:"data"`
	}
	if err := c.get(ctx, op, workflowVersionsPath+"/"+url.PathEscape(id), url.Values{"depth": {noRelations}},
		maxResponseBytes, &one); err != nil {
		return workflowControlPermission(err)
	}
	if !equalUUID(one.Data.Version.ID, id) {
		return provider.InvalidResponse(op, "Twenty answered with a different version than the requested one")
	}
	if err := checkActivatable(one.Data.Version.Trigger, one.Data.Version.Steps); err != nil {
		return err
	}
	var data struct {
		Activated *bool `json:"activateWorkflowVersion"`
	}
	if err := c.graphqlWith(ctx, op, workflowUncertain, activateVersionDocument,
		map[string]any{"workflowVersionId": id}, &data); err != nil {
		return workflowControlPermission(err)
	}
	if data.Activated == nil || !*data.Activated {
		return provider.InvalidResponse(op, "Twenty did not confirm the activation"+workflowUncertain)
	}
	return nil
}

// DeactivateWorkflowVersion sends exactly one mutation.
func (c *Client) DeactivateWorkflowVersion(ctx context.Context, id string) error {
	const op = "deactivate workflow version"
	var data struct {
		Deactivated *bool `json:"deactivateWorkflowVersion"`
	}
	if err := c.graphqlWith(ctx, op, workflowUncertain, deactivateVersionDocument,
		map[string]any{"workflowVersionId": id}, &data); err != nil {
		return workflowControlPermission(err)
	}
	if data.Deactivated == nil || !*data.Deactivated {
		return provider.InvalidResponse(op, "Twenty did not confirm the deactivation"+workflowUncertain)
	}
	return nil
}

// WorkflowRunState is the answer of a stop or retry: the run and its status only.
type WorkflowRunState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type runStateWire struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (c *Client) controlRun(ctx context.Context, op string, doc graphqlDocument, field, id string) (*WorkflowRunState, error) {
	var data map[string]*runStateWire
	if err := c.graphqlWith(ctx, op, workflowUncertain, doc, map[string]any{"workflowRunId": id}, &data); err != nil {
		return nil, workflowControlPermission(err)
	}
	run := data[field]
	if run == nil || !equalUUID(run.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different run than the requested one"+
			workflowUncertain)
	}
	return &WorkflowRunState{ID: id, Status: enumToken(workflowRunStatuses, run.Status)}, nil
}

// StopWorkflowRun sends exactly one mutation.
func (c *Client) StopWorkflowRun(ctx context.Context, id string) (*WorkflowRunState, error) {
	return c.controlRun(ctx, "stop workflow run", stopRunDocument, "stopWorkflowRun", id)
}

// RetryWorkflowRun sends exactly one mutation; the run repeats its steps, outside effects included.
func (c *Client) RetryWorkflowRun(ctx context.Context, id string) (*WorkflowRunState, error) {
	return c.controlRun(ctx, "retry workflow run", retryRunDocument, "retryWorkflowRun", id)
}

// workflowControl gates the connection, checks the one id argument, and opens the client.
func workflowControl(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, key, message string) (*Client, string, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, "", err
	}
	var args map[string]string
	if json.Unmarshal(raw, &args) != nil {
		return nil, "", providerError("workflow control", "the validated arguments could not be read")
	}
	id := args[key]
	if !validUUID(id) {
		return nil, "", invalidRequest(message)
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, id, err
}

func invokeWorkflowVersionsActivate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, id, err := workflowControl(ctx, resolved, secrets, red, raw, "workflow_version_id", errWorkflowVersionID)
	if err != nil {
		return nil, err
	}
	if err := client.ActivateWorkflowVersion(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"activated": true}, nil
}

func invokeWorkflowVersionsDeactivate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, id, err := workflowControl(ctx, resolved, secrets, red, raw, "workflow_version_id", errWorkflowVersionID)
	if err != nil {
		return nil, err
	}
	if err := client.DeactivateWorkflowVersion(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"deactivated": true}, nil
}

func invokeWorkflowRunsStop(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, id, err := workflowControl(ctx, resolved, secrets, red, raw, "workflow_run_id", errWorkflowRunID)
	if err != nil {
		return nil, err
	}
	return client.StopWorkflowRun(ctx, id)
}

func invokeWorkflowRunsRetry(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, id, err := workflowControl(ctx, resolved, secrets, red, raw, "workflow_run_id", errWorkflowRunID)
	if err != nil {
		return nil, err
	}
	return client.RetryWorkflowRun(ctx, id)
}
