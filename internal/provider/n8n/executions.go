package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// executionIDSchema is the JSON Schema of one execution identifier: getExecution.generated.yml declares its
// path parameter as an integer, unlike a workflow's opaque string ID, and this provider follows that.
const executionIDSchema = `{"type":"integer","minimum":1}`

// statusValues mirrors the "status" enum getExecutions.generated.yml documents.
var statusValues = []string{"canceled", "crashed", "error", "new", "running", "success", "unknown", "waiting"}

// erroredStatuses are the statuses executions.get treats as worth the one extra, bounded, filtered
// includeData request to learn what failed; every other status is reported from the base answer alone.
var erroredStatuses = map[string]bool{"error": true, "crashed": true}

var executionSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"workflow_id":{"type":"string"},"status":{"type":"string"},` +
	`"mode":{"type":"string"},"finished":{"type":"boolean"},"started_at":{"type":"string"},` +
	`"stopped_at":{"type":"string"},"wait_till":{"type":"string"},"retry_of":{"type":"string"},` +
	`"retry_success_id":{"type":"string"}},` +
	`"required":["id","workflow_id","status","mode","finished"],"additionalProperties":false}`

var executionSummaryFields = []capability.Field{
	{Name: "id", Description: "Execution identifier, used as execution_id by this tool"},
	{Name: "workflow_id", Description: "Workflow this execution ran, used as workflow_id by the workflow tools of this provider"},
	{Name: "status", Description: "One of " + joinValues(statusValues)},
	{Name: "mode", Description: "How the execution was started, as n8n reports it, for example manual, trigger, or webhook"},
	{Name: "finished", Description: "True once the execution has finished, whichever its status"},
	{Name: "started_at", Description: "Start time, as n8n reports it; absent for one that never started"},
	{Name: "stopped_at", Description: "Stop time, as n8n reports it; absent for one still running or waiting"},
	{Name: "wait_till", Description: "When set, the time this execution resumes; only meaningful for status waiting"},
	{Name: "retry_of", Description: "When set, the execution this one retries"},
	{Name: "retry_success_id", Description: "When set, the later execution that successfully retried this one"},
}

var executionsList = capability.Descriptor{
	ID:      Provider + ".executions.list",
	Version: 1,
	Title:   "List n8n executions",
	Description: "List the executions of the bound n8n instance, restricted to its workflow allow-list " +
		"when it has one; page by page with an opaque cursor. When this connection also holds a project " +
		"allow-list, workflow_id is required, so this connection's project membership can be verified once " +
		"for the whole page instead of guessed: an execution carries no project of its own",
	Tags:                       []string{"n8n", "executions", "list", "automation"},
	Risk:                       n8nReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"workflow_id":` + targetIDSchema + `,` +
		`"status":{"type":"string","enum":` + enumOf(statusValues) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"executions":{"type":"array","items":` + executionSummarySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["executions","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Executions per page, 1 to 250; 100 when omitted, matching n8n's own default"},
		{Name: "workflow_id", Description: "Restrict the list to this workflow; must be inside this connection's " +
			"workflow allow-list when it has one, and is required when this connection also holds a project allow-list"},
		{Name: "status", Description: "Restrict the list to this status"},
	},
	Fields: append(append([]capability.Field{}, executionSummaryFields...),
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains"},
		capability.Field{Name: "count", Description: "Number of executions reported on this page after this connection's workflow allow-list was applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of reachable executions of one workflow",
		Arguments: json.RawMessage(`{"workflow_id":"1"}`)}},
}

var executionErrorSchema = `{"type":"object","properties":{` +
	`"message":{"type":"string"},"node_name":{"type":"string"},"node_type":{"type":"string"}},` +
	`"required":["message"],"additionalProperties":false}`

var executionDetailSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"workflow_id":{"type":"string"},"status":{"type":"string"},` +
	`"mode":{"type":"string"},"finished":{"type":"boolean"},"started_at":{"type":"string"},` +
	`"stopped_at":{"type":"string"},"wait_till":{"type":"string"},"retry_of":{"type":"string"},` +
	`"retry_success_id":{"type":"string"},"error":` + executionErrorSchema + `},` +
	`"required":["id","workflow_id","status","mode","finished"],"additionalProperties":false}`

var executionsGet = capability.Descriptor{
	ID:      Provider + ".executions.get",
	Version: 1,
	Title:   "Get an n8n execution",
	Description: "Read the status, timestamps, and, for a failed execution, a bounded error (message and " +
		"the node it occurred on) of one execution; never its full run data",
	Tags:                       []string{"n8n", "executions", "get", "automation"},
	Risk:                       n8nReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":` + executionIDSchema + `},` +
		`"required":["execution_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(executionDetailSchema),
	Arguments: []capability.Argument{
		{Name: "execution_id", Description: "n8n execution identifier; its workflow must be inside this " +
			"connection's workflow allow-list when it has one, and its project membership is re-checked live " +
			"when this connection also holds a project allow-list", Required: true},
	},
	Fields: append(append([]capability.Field{}, executionSummaryFields...),
		capability.Field{Name: "error", Description: "Present only when status is error or crashed: the " +
			"result error's message and, when n8n names it, the failing node's name and type; never the " +
			"execution's run data"},
	),
	Examples: []capability.Example{{Description: "Read one execution", Arguments: json.RawMessage(`{"execution_id":123}`)}},
}

// executionJSON mirrors the subset of n8n's Execution resource this provider reads when includeData is
// false (packages/cli/src/public-api/v1/handlers/executions/spec).
type executionJSON struct {
	ID             int64  `json:"id"`
	WorkflowID     string `json:"workflowId"`
	Status         string `json:"status"`
	Mode           string `json:"mode"`
	Finished       bool   `json:"finished"`
	StartedAt      string `json:"startedAt"`
	StoppedAt      string `json:"stoppedAt"`
	WaitTill       string `json:"waitTill"`
	RetryOf        string `json:"retryOf"`
	RetrySuccessID string `json:"retrySuccessId"`
}

// executionErrorJSON mirrors the one path this provider reads from an includeData=true answer: the result
// error of a finished run (n8n's internal IRunExecutionData.resultData.error) and the node it names, never
// any node's actual input or output.
type executionErrorJSON struct {
	Data struct {
		ResultData struct {
			Error struct {
				Message string `json:"message"`
				Node    struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"node"`
			} `json:"error"`
		} `json:"resultData"`
	} `json:"data"`
}

// ExecutionError is the stable, bounded view of one failed execution's error.
type ExecutionError struct {
	Message  string `json:"message"`
	NodeName string `json:"node_name,omitempty"`
	NodeType string `json:"node_type,omitempty"`
}

// ExecutionSummary is the stable, allow-list-filtered listing view of one execution.
type ExecutionSummary struct {
	ID             int64  `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	Status         string `json:"status"`
	Mode           string `json:"mode"`
	Finished       bool   `json:"finished"`
	StartedAt      string `json:"started_at,omitempty"`
	StoppedAt      string `json:"stopped_at,omitempty"`
	WaitTill       string `json:"wait_till,omitempty"`
	RetryOf        string `json:"retry_of,omitempty"`
	RetrySuccessID string `json:"retry_success_id,omitempty"`
}

// ExecutionDetail is the stable, scope-checked read view of one execution.
type ExecutionDetail struct {
	ExecutionSummary
	Error *ExecutionError `json:"error,omitempty"`
}

func executionSummaryOf(e executionJSON) ExecutionSummary {
	return ExecutionSummary{
		ID: e.ID, WorkflowID: bounded(e.WorkflowID), Status: bounded(e.Status), Mode: bounded(e.Mode),
		Finished: e.Finished, StartedAt: bounded(e.StartedAt), StoppedAt: bounded(e.StoppedAt),
		WaitTill: bounded(e.WaitTill), RetryOf: bounded(e.RetryOf), RetrySuccessID: bounded(e.RetrySuccessID),
	}
}

type executionsPageJSON struct {
	Data       []executionJSON `json:"data"`
	NextCursor string          `json:"nextCursor"`
}

// ExecutionsPage is one paginated, allow-list-filtered listing of executions.
type ExecutionsPage struct {
	Executions []ExecutionSummary `json:"executions"`
	Cursor     string             `json:"cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
	Count      int                `json:"count"`
}

type executionsListArguments struct {
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
}

func invokeExecutionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input executionsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list executions", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.projects) > 0 && input.WorkflowID == "" {
		return nil, invalidRequest("this connection restricts workflows by project, so executions.list " +
			"requires workflow_id: an execution carries no project of its own, and only checking one named " +
			"workflow's project once keeps that verification affordable for a whole page")
	}
	if input.WorkflowID != "" {
		if err := selectWorkflow(resolved, input.WorkflowID); err != nil {
			return nil, err
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
	if input.WorkflowID != "" && len(bound.projects) > 0 {
		workflow, err := client.fetchWorkflow(ctx, "list executions", input.WorkflowID)
		if err != nil {
			return nil, err
		}
		if err := client.verifyWorkflowScope(workflow); err != nil {
			return nil, err
		}
	}
	return client.ListExecutions(ctx, input.Cursor, limit, input.WorkflowID, input.Status)
}

// ListExecutions reads one page of executions, filtering server-side by every argument n8n's own "GET
// /executions" accepts, and defensively re-checking this connection's workflow allow-list against the
// workflowId of every returned row: the server-side filter is a courtesy, not the boundary.
func (c *Client) ListExecutions(ctx context.Context, cursor string, limit int, workflowID,
	status string) (*ExecutionsPage, error) {
	const op = "list executions"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if workflowID != "" {
		query.Set("workflowId", workflowID)
	}
	if status != "" {
		query.Set("status", status)
	}
	var page executionsPageJSON
	if err := c.get(ctx, op, "/executions", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	summaries := make([]ExecutionSummary, 0, len(page.Data))
	for _, e := range page.Data {
		if !c.scope.allowsWorkflow(e.WorkflowID) {
			continue
		}
		if workflowID != "" && e.WorkflowID != workflowID {
			continue
		}
		summaries = append(summaries, executionSummaryOf(e))
	}
	return &ExecutionsPage{
		Executions: summaries, Cursor: page.NextCursor, HasMore: page.NextCursor != "", Count: len(summaries),
	}, nil
}

type executionArguments struct {
	ExecutionID int64 `json:"execution_id"`
}

func invokeExecutionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input executionArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get execution", "the validated arguments could not be read")
	}
	if input.ExecutionID <= 0 {
		return nil, invalidRequest("execution_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetExecution(ctx, input.ExecutionID)
}

// GetExecution reads one execution's status and timestamps, verifies that its workflow is inside this
// connection's scope exactly as (*Client).verifyWorkflowScope does for a workflow named directly, and, only
// for a failed execution, makes one further bounded request to extract its result error. That second
// request is the only one this provider ever sends with includeData=true, and only the result error is kept
// from its answer; the rest, in particular every node's actual run data, is discarded, see the package doc.
func (c *Client) GetExecution(ctx context.Context, executionID int64) (*ExecutionDetail, error) {
	const op = "get execution"
	var raw executionJSON
	if err := c.get(ctx, op, "/executions/"+strconv.FormatInt(executionID, 10), nil, &raw, maxResponseBytes); err != nil {
		return nil, err
	}
	if raw.ID != executionID {
		return nil, invalidResponse(op, "n8n answered with an execution other than the one requested")
	}
	if err := c.checkExecutionScope(ctx, op, raw); err != nil {
		return nil, err
	}
	detail := ExecutionDetail{ExecutionSummary: executionSummaryOf(raw)}
	if erroredStatuses[raw.Status] {
		execErr, err := c.fetchExecutionError(ctx, executionID)
		if err != nil {
			return nil, err
		}
		detail.Error = execErr
	}
	return &detail, nil
}

// fetchExecutionError sends the one includeData=true request executions.get ever makes, bounded by
// maxErrorResponseBytes rather than the usual maxResponseBytes because an included run can be far larger
// than any other answer this provider reads, and keeps only the result error it names.
func (c *Client) fetchExecutionError(ctx context.Context, executionID int64) (*ExecutionError, error) {
	const op = "get execution"
	var full executionErrorJSON
	query := url.Values{"includeData": {"true"}}
	if err := c.get(ctx, op, "/executions/"+strconv.FormatInt(executionID, 10), query, &full, maxErrorResponseBytes); err != nil {
		return nil, err
	}
	message := full.Data.ResultData.Error.Message
	if message == "" {
		// n8n reported a failed status but no result error this provider's parse could find, most likely a
		// Public API version whose data shape differs from the one this provider knows. The base detail is
		// still useful, so this is reported as an absent error, not a failure of the whole read.
		return nil, nil
	}
	return &ExecutionError{
		Message: bounded(message), NodeName: bounded(full.Data.ResultData.Error.Node.Name),
		NodeType: bounded(full.Data.ResultData.Error.Node.Type),
	}, nil
}

// checkExecutionScope re-applies this connection's workflow allow-list, and, when configured, its project
// allow-list, to one execution's own reported workflowId. It is the same live check GetExecution applies to
// a directly named execution, and executions.retry and executions.stop apply it themselves, with their own
// extra read of the base execution, before either ever sends its one changing request.
func (c *Client) checkExecutionScope(ctx context.Context, op string, raw executionJSON) error {
	if !c.scope.allowsWorkflow(raw.WorkflowID) {
		return invalidRequest("execution_id belongs to a workflow outside the targets of this connection")
	}
	if len(c.scope.projects) > 0 {
		workflow, err := c.fetchWorkflow(ctx, op, raw.WorkflowID)
		if err != nil {
			return err
		}
		if err := c.verifyWorkflowScope(workflow); err != nil {
			return err
		}
	}
	return nil
}

// n8nExecutionArgument names the execution a retry or a stop addresses directly.
var n8nExecutionArgument = capability.Argument{Name: "execution_id",
	Description: "n8n execution identifier; its workflow must be inside this connection's workflow allow-" +
		"list when it has one, and its project membership is re-checked live when this connection also holds " +
		"a project allow-list", Required: true}

var executionsRetry = capability.Descriptor{
	ID:      Provider + ".executions.retry",
	Version: 1,
	Title:   "Retry an n8n execution",
	Description: "Retry one execution of the bound n8n instance, starting a new execution from it; a " +
		"repeated call starts another retry every time. Runs with the workflow as it was saved when the " +
		"original execution ran, not any later change",
	Tags:                       []string{"n8n", "executions", "retry", "automation"},
	Risk:                       n8nChangeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":` + executionIDSchema + `},` +
		`"required":["execution_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(executionSummarySchema),
	Arguments:    []capability.Argument{n8nExecutionArgument},
	Fields:       executionSummaryFields,
	Examples: []capability.Example{{Description: "Retry a failed execution",
		Arguments: json.RawMessage(`{"execution_id":123}`)}},
}

var executionsStop = capability.Descriptor{
	ID:      Provider + ".executions.stop",
	Version: 1,
	Title:   "Stop an n8n execution",
	Description: "Stop one running or waiting execution of the bound n8n instance; repeating it on an " +
		"execution that has already stopped leaves it stopped",
	Tags:                       []string{"n8n", "executions", "stop", "automation"},
	Risk:                       n8nChangeRisk(capability.EffectExecute, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":` + executionIDSchema + `},` +
		`"required":["execution_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(executionSummarySchema),
	Arguments:    []capability.Argument{n8nExecutionArgument},
	Fields:       executionSummaryFields,
	Examples: []capability.Example{{Description: "Stop a running execution",
		Arguments: json.RawMessage(`{"execution_id":123}`)}},
}

func invokeExecutionsRetry(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "retry execution"
	var input executionArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.ExecutionID <= 0 {
		return nil, invalidRequest("execution_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RetryExecution(ctx, input.ExecutionID)
}

// RetryExecution reads the base execution to verify its workflow's scope, exactly as GetExecution does, then
// sends the one changing POST /executions/{id}/retry request. n8n's own answer additionally carries the new
// execution's full run data and a snapshot of the workflow it ran (data, workflowData, customData,
// annotation); this provider reads only into executionJSON's own fields, so none of that ever reaches a
// field of the result, the same run-data boundary GetExecution's includeData read keeps.
func (c *Client) RetryExecution(ctx context.Context, executionID int64) (*ExecutionSummary, error) {
	const op = "retry execution"
	var base executionJSON
	if err := c.get(ctx, op, "/executions/"+strconv.FormatInt(executionID, 10), nil, &base, maxResponseBytes); err != nil {
		return nil, err
	}
	if base.ID != executionID {
		return nil, invalidResponse(op, "n8n answered with an execution other than the one requested")
	}
	if err := c.checkExecutionScope(ctx, op, base); err != nil {
		return nil, err
	}
	var retried executionJSON
	if err := c.change(ctx, op, http.MethodPost, "/executions/"+strconv.FormatInt(executionID, 10)+"/retry", nil,
		map[string]any{}, &retried, maxErrorResponseBytes); err != nil {
		return nil, err
	}
	if retried.ID == 0 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "n8n did not report the ID of the retried execution" + uncertain}
	}
	summary := executionSummaryOf(retried)
	return &summary, nil
}

func invokeExecutionsStop(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "stop execution"
	var input executionArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.ExecutionID <= 0 {
		return nil, invalidRequest("execution_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.StopExecution(ctx, input.ExecutionID)
}

// StopExecution reads the base execution to verify its workflow's scope and to learn its id and workflowId,
// exactly as GetExecution does, then sends the one changing POST /executions/{id}/stop request.
// stopExecution's own answer carries no id or workflowId of its own, only the execution's status after the
// stop, so this combines that answer with the id and workflowId the base read already proved were in scope.
func (c *Client) StopExecution(ctx context.Context, executionID int64) (*ExecutionSummary, error) {
	const op = "stop execution"
	var base executionJSON
	if err := c.get(ctx, op, "/executions/"+strconv.FormatInt(executionID, 10), nil, &base, maxResponseBytes); err != nil {
		return nil, err
	}
	if base.ID != executionID {
		return nil, invalidResponse(op, "n8n answered with an execution other than the one requested")
	}
	if err := c.checkExecutionScope(ctx, op, base); err != nil {
		return nil, err
	}
	var stopped struct {
		Mode      string `json:"mode"`
		StartedAt string `json:"startedAt"`
		StoppedAt string `json:"stoppedAt"`
		Finished  bool   `json:"finished"`
		Status    string `json:"status"`
	}
	if err := c.change(ctx, op, http.MethodPost, "/executions/"+strconv.FormatInt(executionID, 10)+"/stop", nil,
		nil, &stopped, maxResponseBytes); err != nil {
		return nil, err
	}
	summary := ExecutionSummary{
		ID: executionID, WorkflowID: base.WorkflowID, Status: bounded(stopped.Status), Mode: bounded(stopped.Mode),
		Finished: stopped.Finished, StartedAt: bounded(stopped.StartedAt), StoppedAt: bounded(stopped.StoppedAt),
	}
	return &summary, nil
}

func joinValues(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func enumOf(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
