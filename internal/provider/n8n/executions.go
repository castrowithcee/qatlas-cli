package n8n

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// executionSummaryProperties are the members of the execution view the changing tools share; the read
// views below add to them, the changing tools never see the additions.
const executionSummaryProperties = `"id":{"type":"integer"},"workflow_id":{"type":"string"},"status":{"type":"string"},` +
	`"mode":{"type":"string"},"finished":{"type":"boolean"},"started_at":{"type":"string"},` +
	`"stopped_at":{"type":"string"},"wait_till":{"type":"string"},"retry_of":{"type":"string"},` +
	`"retry_success_id":{"type":"string"}`

const executionSummaryRequired = `"required":["id","workflow_id","status","mode","finished"],"additionalProperties":false}`

var executionSummarySchema = `{"type":"object","properties":{` + executionSummaryProperties + `},` + executionSummaryRequired

// timestampSchema bounds an RFC 3339 time argument; its value is parsed locally before any request.
const timestampSchema = `{"type":"string","minLength":20,"maxLength":40}`

// storedAtValues mirrors the "storedAt" enum the execution schemas document; any other value is left out.
var storedAtValues = []string{"db", "fs", "s3", "az"}

// executionReadProperties are the read-only additions shared by the list entry and the detail.
const executionReadProperties = `"stored_at":{"type":"string","enum":["db","fs","s3","az"]},` +
	`"json_size_bytes":{"type":"integer"},"workflow_version_id":{"type":"string"},` +
	`"data_too_large_to_display":{"type":"boolean"}`

var executionListEntrySchema = `{"type":"object","properties":{` + executionSummaryProperties + `,` +
	executionReadProperties + `},` + executionSummaryRequired

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

// executionReadFields describe the members the read views add to the shared execution fields.
var executionReadFields = []capability.Field{
	{Name: "stored_at", Description: "Where n8n stores the execution data: " + joinValues(storedAtValues) +
		"; absent for any other value"},
	{Name: "json_size_bytes", Description: "Size of the stored execution data in bytes; absent when n8n reports 0 (unknown)"},
	{Name: "workflow_version_id", Description: "ID of the workflow version this execution ran, untrusted text"},
	{Name: "data_too_large_to_display", Description: "True when n8n holds execution data above its display size limit; the data itself is never read"},
}

var executionsList = capability.Descriptor{
	ID:      Provider + ".executions.list",
	Version: 1,
	Title:   "List n8n executions",
	Description: "List the executions of the bound n8n instance, restricted to its workflow allow-list " +
		"when it has one; page by page with an opaque cursor. When this connection also holds a project " +
		"allow-list, workflow_id is required, so this connection's project membership can be verified once " +
		"for the whole page instead of guessed: an execution carries no project of its own. Where the connection " +
		"excludes projects, each row is placed through its workflow, which costs one read per workflow",
	Tags:     []string{"n8n", "executions", "list", "automation"},
	Risk:     n8nReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"workflow_id":` + targetIDSchema + `,` +
		`"status":{"type":"string","enum":` + enumOf(statusValues) + `},` +
		`"project_id":` + targetIDSchema + `,` +
		`"started_after":` + timestampSchema + `,"started_before":` + timestampSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"executions":{"type":"array","items":` + executionListEntrySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["executions","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page. " +
			"A follow-up call must pass the same filter values (workflow_id, status, project_id, started_after, " +
			"started_before) as the call that returned the cursor"},
		{Name: "limit", Description: "Executions per page, 1 to 250; 100 when omitted, matching n8n's own default"},
		{Name: "workflow_id", Description: "Restrict the list to this workflow; must be inside this connection's " +
			"workflow allow-list when it has one, and is required when this connection also holds a project allow-list"},
		{Name: "status", Description: "Restrict the list to this status"},
		{Name: "project_id", Description: "Restrict the list to executions n8n attributes to this project; must be " +
			"inside this connection's project allow-list when it has one, which still requires workflow_id"},
		{Name: "started_after", Description: "Only executions that started after this time, RFC 3339; sent to n8n as UTC"},
		{Name: "started_before", Description: "Only executions that started before this time, RFC 3339; sent to n8n as UTC; " +
			"must not be earlier than started_after"},
	},
	Fields: append(append(append([]capability.Field{}, executionSummaryFields...), executionReadFields...),
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

var executionTracingContextSchema = `{"type":"object","properties":{"traceparent":{"type":"string"},` +
	`"tracestate":{"type":"string"}},"additionalProperties":false}`

var executionDetailSchema = `{"type":"object","properties":{` + executionSummaryProperties + `,` +
	executionReadProperties + `,"created_at":{"type":"string"},"tracing_context":` + executionTracingContextSchema + `,` +
	`"deduplication_key":{"type":"string"},"binary_data_size_bytes":{"type":"integer"},` +
	`"used_private_credentials":{"type":"boolean"},"error":` + executionErrorSchema + `},` + executionSummaryRequired

var executionsGet = capability.Descriptor{
	ID:      Provider + ".executions.get",
	Version: 1,
	Title:   "Get an n8n execution",
	Description: "Read the status, timestamps, and, for a failed execution, a bounded error (message and " +
		"the node it occurred on) of one execution; never its full run data",
	Tags:     []string{"n8n", "executions", "get", "automation"},
	Risk:     n8nReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":` + executionIDSchema + `},` +
		`"required":["execution_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(executionDetailSchema),
	Arguments: []capability.Argument{
		{Name: "execution_id", Description: "n8n execution identifier; its workflow must be inside this " +
			"connection's workflow allow-list when it has one, and its project membership is re-checked live " +
			"when this connection also holds a project allow-list", Required: true},
	},
	Fields: append(append(append([]capability.Field{}, executionSummaryFields...), executionReadFields...),
		capability.Field{Name: "created_at", Description: "When n8n created the execution record, as n8n reports it"},
		capability.Field{Name: "tracing_context", Description: "W3C trace context propagated with the execution, untrusted text"},
		capability.Field{Name: "traceparent", Description: "W3C traceparent of the execution, untrusted text"},
		capability.Field{Name: "tracestate", Description: "W3C tracestate of the execution, untrusted text; absent when n8n sends none"},
		capability.Field{Name: "deduplication_key", Description: "Key n8n uses to stop a duplicate execution from being created, untrusted text"},
		capability.Field{Name: "binary_data_size_bytes", Description: "Size of the stored binary data in bytes; absent when n8n reports 0 (unknown)"},
		capability.Field{Name: "used_private_credentials", Description: "Whether the execution used credentials that are not shared with the project"},
		capability.Field{Name: "error", Description: "Present only when status is error or crashed: the " +
			"result error's message and, when n8n names it, the failing node's name and type; never the " +
			"execution's run data"},
	),
	Examples: []capability.Example{{Description: "Read one execution", Arguments: json.RawMessage(`{"execution_id":123}`)}},
}

// executionID reads n8n's execution id from a JSON number or a decimal string: the public API spec types it
// as a string while the service emits a number. It accepts positive int64 values only.
type executionID int64

func (id *executionID) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		return nil
	}
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		text = text[1 : len(text)-1]
	}
	if text == "" || strings.Trim(text, "0123456789") != "" {
		return fmt.Errorf("invalid execution id")
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil || v <= 0 {
		return fmt.Errorf("invalid execution id")
	}
	*id = executionID(v)
	return nil
}

// executionJSON mirrors the subset of n8n's Execution resource this provider reads when includeData is
// false (packages/cli/src/public-api/v1/handlers/executions/spec).
type executionJSON struct {
	ID             executionID `json:"id"`
	WorkflowID     string      `json:"workflowId"`
	Status         string      `json:"status"`
	Mode           string      `json:"mode"`
	Finished       bool        `json:"finished"`
	StartedAt      string      `json:"startedAt"`
	StoppedAt      string      `json:"stoppedAt"`
	WaitTill       string      `json:"waitTill"`
	RetryOf        string      `json:"retryOf"`
	RetrySuccessID string      `json:"retrySuccessId"`
}

// executionReadJSON adds what the two read tools take from the same answer. It is separate from
// executionJSON so that the answers of the changing tools are decoded exactly as before. Sizes stay raw
// numbers until parsed as integers, and a member of an unexpected type costs only that member.
type executionReadJSON struct {
	executionJSON
	CreatedAt              string          `json:"createdAt"`
	StoredAt               string          `json:"storedAt"`
	TracingContext         json.RawMessage `json:"tracingContext"`
	DeduplicationKey       string          `json:"deduplicationKey"`
	JSONSizeBytes          json.RawMessage `json:"jsonSizeBytes"`
	BinaryDataSizeBytes    json.RawMessage `json:"binaryDataSizeBytes"`
	WorkflowVersionID      string          `json:"workflowVersionId"`
	UsedPrivateCredentials json.RawMessage `json:"usedPrivateCredentials"`
	DataTooLargeToDisplay  bool            `json:"dataTooLargeToDisplay"`
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

// ExecutionListEntry is the listing view of one execution: the summary plus the read-only additions.
type ExecutionListEntry struct {
	ExecutionSummary
	StoredAt              string `json:"stored_at,omitempty"`
	JSONSizeBytes         int64  `json:"json_size_bytes,omitempty"`
	WorkflowVersionID     string `json:"workflow_version_id,omitempty"`
	DataTooLargeToDisplay bool   `json:"data_too_large_to_display,omitempty"`
}

// ExecutionTracingContext is the W3C trace context of an execution.
type ExecutionTracingContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// ExecutionDetail is the stable, scope-checked read view of one execution.
type ExecutionDetail struct {
	ExecutionListEntry
	CreatedAt              string                   `json:"created_at,omitempty"`
	TracingContext         *ExecutionTracingContext `json:"tracing_context,omitempty"`
	DeduplicationKey       string                   `json:"deduplication_key,omitempty"`
	BinaryDataSizeBytes    int64                    `json:"binary_data_size_bytes,omitempty"`
	UsedPrivateCredentials *bool                    `json:"used_private_credentials,omitempty"`
	Error                  *ExecutionError          `json:"error,omitempty"`
}

// storedAtOf keeps only a value of the documented storage enum.
func storedAtOf(value string) string {
	for _, known := range storedAtValues {
		if value == known {
			return value
		}
	}
	return ""
}

// byteSize reads a non-negative integer count; anything else, including 0 (unknown) and null, is absent.
func byteSize(raw json.RawMessage) int64 {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func executionListEntryOf(e executionReadJSON) ExecutionListEntry {
	return ExecutionListEntry{ExecutionSummary: executionSummaryOf(e.executionJSON),
		StoredAt: storedAtOf(e.StoredAt), JSONSizeBytes: byteSize(e.JSONSizeBytes),
		WorkflowVersionID: cappedText(e.WorkflowVersionID), DataTooLargeToDisplay: e.DataTooLargeToDisplay}
}

func executionDetailOf(e executionReadJSON) ExecutionDetail {
	detail := ExecutionDetail{ExecutionListEntry: executionListEntryOf(e), CreatedAt: bounded(e.CreatedAt),
		DeduplicationKey: cappedText(e.DeduplicationKey), BinaryDataSizeBytes: byteSize(e.BinaryDataSizeBytes)}
	var tracing struct {
		Traceparent string `json:"traceparent"`
		Tracestate  string `json:"tracestate"`
	}
	if json.Unmarshal(e.TracingContext, &tracing) == nil {
		context := ExecutionTracingContext{Traceparent: cappedText(tracing.Traceparent),
			Tracestate: cappedText(tracing.Tracestate)}
		if context != (ExecutionTracingContext{}) {
			detail.TracingContext = &context
		}
	}
	switch string(e.UsedPrivateCredentials) {
	case "true", "false":
		used := string(e.UsedPrivateCredentials) == "true"
		detail.UsedPrivateCredentials = &used
	}
	return detail
}

func executionSummaryOf(e executionJSON) ExecutionSummary {
	return ExecutionSummary{
		ID: int64(e.ID), WorkflowID: bounded(e.WorkflowID), Status: bounded(e.Status), Mode: bounded(e.Mode),
		Finished: e.Finished, StartedAt: bounded(e.StartedAt), StoppedAt: bounded(e.StoppedAt),
		WaitTill: bounded(e.WaitTill), RetryOf: bounded(e.RetryOf), RetrySuccessID: bounded(e.RetrySuccessID),
	}
}

type executionsPageJSON struct {
	Data       []executionReadJSON `json:"data"`
	NextCursor string              `json:"nextCursor"`
}

// ExecutionsPage is one paginated, allow-list-filtered listing of executions.
type ExecutionsPage struct {
	Executions []ExecutionListEntry `json:"executions"`
	Cursor     string               `json:"cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
	Count      int                  `json:"count"`
}

type executionsListArguments struct {
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	ProjectID  string `json:"project_id"`
	// StartedAfter and StartedBefore are RFC 3339 times.
	StartedAfter  string `json:"started_after"`
	StartedBefore string `json:"started_before"`
}

// executionFilter carries the validated optional filters of executions.list.
type executionFilter struct {
	WorkflowID, Status, ProjectID string
	StartedAfter, StartedBefore   time.Time
}

// parseExecutionTimes reads the two time filters as RFC 3339 and refuses an inverted range.
func parseExecutionTimes(after, before string) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	if after != "" {
		if from, err = time.Parse(time.RFC3339, after); err != nil {
			return from, to, invalidRequest("started_after must be an RFC 3339 time")
		}
	}
	if before != "" {
		if to, err = time.Parse(time.RFC3339, before); err != nil {
			return from, to, invalidRequest("started_before must be an RFC 3339 time")
		}
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return from, to, invalidRequest("started_after must not be later than started_before")
	}
	return from, to, nil
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
	// A filter on an excluded workflow or project is an empty page, but only after every other argument has
	// been checked, so the answer does not depend on what is excluded.
	emptyPage := false
	if input.WorkflowID != "" {
		excluded, err := selectWorkflowFilter(resolved, input.WorkflowID)
		if err != nil {
			return nil, err
		}
		emptyPage = emptyPage || excluded
	}
	if input.ProjectID != "" {
		excluded, err := selectProjectFilter(resolved, input.ProjectID)
		if err != nil {
			return nil, err
		}
		emptyPage = emptyPage || excluded
	}
	startedAfter, startedBefore, err := parseExecutionTimes(input.StartedAfter, input.StartedBefore)
	if err != nil {
		return nil, err
	}
	if emptyPage {
		return &ExecutionsPage{Executions: []ExecutionListEntry{}}, nil
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if input.WorkflowID != "" && bound.restrictsProjects() {
		workflow, err := client.fetchWorkflow(ctx, "list executions", input.WorkflowID)
		if err != nil {
			return nil, err
		}
		if err := client.verifyWorkflowScope(workflow); err != nil {
			return nil, err
		}
	}
	return client.ListExecutions(ctx, input.Cursor, limit, executionFilter{WorkflowID: input.WorkflowID,
		Status: input.Status, ProjectID: input.ProjectID, StartedAfter: startedAfter, StartedBefore: startedBefore})
}

// ListExecutions reads one page of executions, filtering server-side by every argument n8n's own "GET
// /executions" accepts, and defensively re-checking this connection's workflow allow-list against the
// workflowId of every returned row: the server-side filter is a courtesy, not the boundary.
func (c *Client) ListExecutions(ctx context.Context, cursor string, limit int,
	filter executionFilter) (*ExecutionsPage, error) {
	const op = "list executions"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	workflowID := filter.WorkflowID
	if workflowID != "" {
		query.Set("workflowId", workflowID)
	}
	if filter.Status != "" {
		query.Set("status", filter.Status)
	}
	if filter.ProjectID != "" {
		query.Set("projectId", filter.ProjectID)
	}
	if !filter.StartedAfter.IsZero() {
		query.Set("startedAfter", filter.StartedAfter.UTC().Format(time.RFC3339Nano))
	}
	if !filter.StartedBefore.IsZero() {
		query.Set("startedBefore", filter.StartedBefore.UTC().Format(time.RFC3339Nano))
	}
	var page executionsPageJSON
	if err := c.get(ctx, op, "/executions", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	summaries := make([]ExecutionListEntry, 0, len(page.Data))
	// An execution carries no project, so without a workflow filter each row is placed through its workflow.
	// The answer is kept per workflow for this call only; a workflow named by the filter was proven by the
	// caller already.
	placed := map[string]bool{}
	for _, e := range page.Data {
		if !c.scope.allowsWorkflow(e.WorkflowID) {
			continue
		}
		if workflowID != "" && e.WorkflowID != workflowID {
			continue
		}
		if workflowID == "" && c.scope.restrictsProjects() {
			reachable, known := placed[e.WorkflowID]
			if !known {
				var err error
				if reachable, err = c.workflowReachable(ctx, op, e.WorkflowID); err != nil {
					return nil, err
				}
				placed[e.WorkflowID] = reachable
			}
			if !reachable {
				continue
			}
		}
		summaries = append(summaries, executionListEntryOf(e))
	}
	return &ExecutionsPage{
		Executions: summaries, Cursor: page.NextCursor, HasMore: page.NextCursor != "", Count: len(summaries),
	}, nil
}

// workflowReachable proves through the workflow itself that it lies in a reachable project. A workflow that
// n8n does not return, refuses, or answers for another ID cannot be placed and counts as unreachable; any
// other failure ends the call instead of silently emptying the page.
func (c *Client) workflowReachable(ctx context.Context, op, workflowID string) (bool, error) {
	if !validTargetID(workflowID) {
		return false, nil
	}
	workflow, err := c.fetchWorkflow(ctx, op, workflowID)
	if err != nil {
		if failure, ok := err.(*provider.Error); ok && (failure.Class == provider.ClassNotFound ||
			failure.Class == provider.ClassPermission || failure.Class == provider.ClassInvalidResponse) {
			return false, nil
		}
		return false, err
	}
	return c.verifyWorkflowScope(workflow) == nil, nil
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
	var raw executionReadJSON
	if err := c.get(ctx, op, "/executions/"+strconv.FormatInt(executionID, 10), nil, &raw, maxResponseBytes); err != nil {
		return nil, err
	}
	if int64(raw.ID) != executionID {
		return nil, invalidResponse(op, "n8n answered with an execution other than the one requested")
	}
	if err := c.checkExecutionScope(ctx, op, raw.executionJSON); err != nil {
		return nil, err
	}
	detail := executionDetailOf(raw)
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
	if c.scope.restrictsProjects() {
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
	Tags:     []string{"n8n", "executions", "retry", "automation"},
	Risk:     n8nChangeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider: Provider,
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
	Tags:     []string{"n8n", "executions", "stop", "automation"},
	Risk:     n8nChangeRisk(capability.EffectExecute, capability.IdempotencyIdempotent),
	Provider: Provider,
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
	if int64(base.ID) != executionID {
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
	if int64(base.ID) != executionID {
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
