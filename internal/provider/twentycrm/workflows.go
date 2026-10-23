package twentycrm

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	workflowsGroup = "workflows"

	// workflowDataSensitivity classifies automation metadata; step configuration and run data are never read out.
	workflowDataSensitivity = "twentycrm-workflow-data"

	workflowsPath        = "/rest/workflows"
	workflowVersionsPath = "/rest/workflowVersions"
	workflowRunsPath     = "/rest/workflowRuns"

	// workflowVersionsMax bounds the versions of workflows.get; older ones are reported by has_more_versions.
	workflowVersionsMax = 50
	workflowStepTypeMax = 100
	workflowNameMax     = 200

	// workflowNameFilterMax bounds the name filter of workflows.list in characters.
	workflowNameFilterMax = 100
	workflowUnknown       = "unknown"
)

// The enum values below are those of Twenty's standard workflow objects (workflow.workspace-entity.ts,
// workflow-version.workspace-entity.ts, workflow-run.workspace-entity.ts), of workflow-trigger.type.ts, and of
// WorkflowActionType in twenty-shared (twentyhq/twenty, commit 46fc01c38374c2b489b0d4755719da2d1e5408ac). Any
// other value read from Twenty is reported as "unknown", never passed through.
var (
	workflowStatuses        = []string{"DRAFT", "ACTIVE", "DEACTIVATED"}
	workflowVersionStatuses = []string{"DRAFT", "ACTIVE", "DEACTIVATED", "ARCHIVED"}
	workflowRunStatuses     = []string{"NOT_STARTED", "ENQUEUED", "RUNNING", "COMPLETED", "FAILED", "STOPPING", "STOPPED"}
	workflowTriggerTypes    = []string{"DATABASE_EVENT", "MANUAL", "CRON", "WEBHOOK"}
	workflowStepTypes       = []string{"CODE", "LOGIC_FUNCTION", "SEND_EMAIL", "SEND_CHAT_MESSAGE", "DRAFT_EMAIL",
		"CREATE_CALENDAR_EVENT", "CREATE_RECORD", "UPDATE_RECORD", "DELETE_RECORD", "UPSERT_RECORD", "FIND_RECORDS",
		"PICK_RECORD", "FORM", "FILTER", "IF_ELSE", "HTTP_REQUEST", "AI_AGENT", "CLASSIFY", "ITERATOR", "EMPTY",
		"DELAY", "WAIT_FOR_EVENT"}
)

var cursorSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxBoundCursorLen) +
	`,"pattern":"^[A-Za-z0-9_-]+$"}`

var workflowRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: workflowDataSensitivity,
}

func enumSchema(values []string) string {
	encoded, _ := json.Marshal(values)
	return `{"type":"string","enum":` + string(encoded) + `}`
}

const (
	workflowNote = "Names, order, origin (created_by, updated_by), statuses, timestamps, trigger and step types, and " +
		"the number of steps are read; never step or trigger settings, run outputs, run context, run state, step " +
		"logs, or error texts. Values are untrusted workspace data"
	uuidSchema          = `{"type":"string","format":"uuid","minLength":36,"maxLength":36}`
	workflowNameSchema  = `{"type":"string","minLength":1,"maxLength":100}`
	workflowLimitSchema = `{"type":"integer","minimum":1,"maximum":100}`
)

var workflowsList = capability.Descriptor{
	ID:      Provider + ".workflows.list",
	Version: 1,
	Title:   "List Twenty CRM workflows",
	Description: "List one page of the workflows of the Twenty workspace of a connection without object targets, " +
		"optionally those whose name contains a text: name, position, statuses, last published version, origin, " +
		"and timestamps. " + workflowNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "list"},
	Risk:     workflowRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + workflowNameSchema + `,"limit":` +
		workflowLimitSchema + `,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"workflows":{"type":"array","items":` +
		workflowItemSchema + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"},` + totalCountSchema + `},` +
		`"required":["workflows","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Only workflows whose name contains this text, case-insensitive; 1 to 100 characters without control characters or backslash; all workflows when omitted"},
		{Name: "limit", Description: "Workflows per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same name filter; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "workflows", Description: "The workflows on this page, newest first, with id, name, position, " +
			"core_workflow_id, statuses, last_published_version_id, created_by, updated_by, created_at, and " +
			"updated_at, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
		totalCountField,
	},
	Examples: []capability.Example{{Description: "List the workflows", Arguments: json.RawMessage(`{}`)}},
}

// workflowProperties describes a workflow in workflows.list and, flat, in workflows.get.
const workflowProperties = `"id":{"type":"string"},"name":{"type":"string"},"position":{"type":"number"},` +
	`"core_workflow_id":{"type":"string"},"statuses":{"type":"array","items":{"type":"string"}},` +
	`"last_published_version_id":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}`

const workflowItemSchema = `{"type":"object","properties":{` + workflowProperties + `},` +
	`"required":["id","name","statuses"],"additionalProperties":false}`

var workflowsGet = capability.Descriptor{
	ID:      Provider + ".workflows.get",
	Version: 1,
	Title:   "Get a Twenty CRM workflow",
	Description: "Read one workflow of the Twenty workspace of a connection without object targets with its " +
		"latest versions: per version name, status, trigger type, step types, and number of steps. " + workflowNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "get", "versions"},
	Risk:     workflowRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + workflowProperties + `,"versions":{"type":"array","items":{` +
		`"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"position":{"type":"number"},"workflow_id":{"type":"string"},"core_workflow_version_id":{"type":"string"},` +
		`"status":` + enumSchema(append(append([]string{}, workflowVersionStatuses...), workflowUnknown)) + `,` +
		`"trigger_type":{"type":"string"},"step_types":{"type":"array","items":{"type":"string"}},` +
		`"step_count":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
		`"required":["id","name","status","step_count"],"additionalProperties":false}},` +
		`"has_more_versions":{"type":"boolean"}},"required":["id","name","statuses","versions",` +
		`"has_more_versions"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the workflow, as returned by twenty-crm.workflows.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Workflow identifier"},
		{Name: "name", Description: "Workflow name, untrusted data"},
		{Name: "statuses", Description: "Statuses of the workflow versions"},
		{Name: "last_published_version_id", Description: "Identifier of the last published version"},
		{Name: "created_at", Description: "Creation timestamp of the workflow"},
		{Name: "updated_at", Description: "Last change timestamp of the workflow"},
		{Name: "position", Description: "Sort position of the workflow"},
		{Name: "core_workflow_id", Description: "Identifier of the workflow in Twenty's core schema"},
		{Name: "created_by", Description: "Source and member that created the workflow: source, workspace_member_id, name, context; untrusted data"},
		{Name: "updated_by", Description: "Source and member that last changed the workflow: source, workspace_member_id, name, context; untrusted data"},
		{Name: "versions", Description: "At most 50 newest versions, newest first, with id, name, position, " +
			"workflow_id, core_workflow_version_id, status, trigger_type, step_types, step_count, and timestamps, untrusted data"},
		{Name: "has_more_versions", Description: "True when the workflow holds older versions that are not listed"},
	},
	Examples: []capability.Example{{
		Description: "Read a workflow with its versions",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

var workflowRunsList = capability.Descriptor{
	ID:      Provider + ".workflow-runs.list",
	Version: 1,
	Title:   "List Twenty CRM workflow runs",
	Description: "List one page of the runs of one workflow of the Twenty workspace of a connection without " +
		"object targets, newest first, optionally of one status or one workflow version: status, version, " +
		"origin, and timestamps. " + workflowNote,
	Tags:     []string{"twentycrm", "workflows", "automation", "runs", "list"},
	Risk:     workflowRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":` + uuidSchema + `,` +
		`"workflow_version_id":` + uuidSchema + `,"status":` + enumSchema(workflowRunStatuses) + `,"limit":` +
		workflowLimitSchema + `,` +
		`"cursor":` + cursorSchema + `},"required":["workflow_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"runs":{"type":"array","items":{` +
		`"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"position":{"type":"number"},"status":{"type":"string"},"workflow_id":{"type":"string"},` +
		`"core_workflow_id":{"type":"string"},"workflow_version_id":{"type":"string"},` +
		`"core_workflow_version_id":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},` +
		`"enqueued_at":{"type":"string"},"started_at":{"type":"string"},"ended_at":{"type":"string"},` +
		`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
		`"required":["id","status"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"},` + totalCountSchema + `},` +
		`"required":["runs","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "workflow_id", Description: "UUID of the workflow, as returned by twenty-crm.workflows.list", Required: true},
		{Name: "workflow_version_id", Description: "Only runs of this workflow version, as a UUID; all versions when omitted"},
		{Name: "status", Description: "Only runs of this status: " + strings.Join(workflowRunStatuses, ", ") + "; all statuses when omitted"},
		{Name: "limit", Description: "Runs per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same workflow, version, and status; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "runs", Description: "The runs on this page, newest first, with id, name, position, status, " +
			"workflow_id, core_workflow_id, workflow_version_id, core_workflow_version_id, created_by, updated_by, " +
			"and timestamps, without outputs, context, state, or errors, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workflow holds a following page"},
		totalCountField,
	},
	Examples: []capability.Example{{
		Description: "Find failed runs of a workflow",
		Arguments:   json.RawMessage(`{"workflow_id":"123e4567-e89b-42d3-a456-426614174000","status":"FAILED"}`),
	}},
}

// WorkflowSummary is the stable view of one workflow.
type WorkflowSummary struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Position               json.Number    `json:"position,omitempty"`
	CoreWorkflowID         string         `json:"core_workflow_id,omitempty"`
	Statuses               []string       `json:"statuses"`
	LastPublishedVersionID string         `json:"last_published_version_id,omitempty"`
	CreatedBy              map[string]any `json:"created_by,omitempty"`
	UpdatedBy              map[string]any `json:"updated_by,omitempty"`
	CreatedAt              string         `json:"created_at,omitempty"`
	UpdatedAt              string         `json:"updated_at,omitempty"`
}

// WorkflowList is one page of workflows.
type WorkflowList struct {
	Workflows  []WorkflowSummary `json:"workflows"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
	TotalCount *int64            `json:"total_count,omitempty"`
}

// WorkflowVersion carries no trigger or step settings, only their types.
type WorkflowVersion struct {
	ID                    string      `json:"id"`
	Name                  string      `json:"name"`
	Position              json.Number `json:"position,omitempty"`
	WorkflowID            string      `json:"workflow_id,omitempty"`
	CoreWorkflowVersionID string      `json:"core_workflow_version_id,omitempty"`
	Status                string      `json:"status"`
	TriggerType           string      `json:"trigger_type,omitempty"`
	StepTypes             []string    `json:"step_types,omitempty"`
	StepCount             int         `json:"step_count"`
	CreatedAt             string      `json:"created_at,omitempty"`
	UpdatedAt             string      `json:"updated_at,omitempty"`
}

// WorkflowDetail is a workflow with its newest versions.
type WorkflowDetail struct {
	WorkflowSummary
	Versions        []WorkflowVersion `json:"versions"`
	HasMoreVersions bool              `json:"has_more_versions"`
}

// WorkflowRun carries no output, context, state, or error text.
type WorkflowRun struct {
	ID                    string         `json:"id"`
	Name                  string         `json:"name,omitempty"`
	Position              json.Number    `json:"position,omitempty"`
	Status                string         `json:"status"`
	WorkflowID            string         `json:"workflow_id,omitempty"`
	CoreWorkflowID        string         `json:"core_workflow_id,omitempty"`
	WorkflowVersionID     string         `json:"workflow_version_id,omitempty"`
	CoreWorkflowVersionID string         `json:"core_workflow_version_id,omitempty"`
	CreatedBy             map[string]any `json:"created_by,omitempty"`
	UpdatedBy             map[string]any `json:"updated_by,omitempty"`
	EnqueuedAt            string         `json:"enqueued_at,omitempty"`
	StartedAt             string         `json:"started_at,omitempty"`
	EndedAt               string         `json:"ended_at,omitempty"`
	CreatedAt             string         `json:"created_at,omitempty"`
	UpdatedAt             string         `json:"updated_at,omitempty"`
}

// WorkflowRunList is one page of runs.
type WorkflowRunList struct {
	Runs       []WorkflowRun `json:"runs"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
	TotalCount *int64        `json:"total_count,omitempty"`
}

// workflowPage is the Twenty page envelope; the items stay raw until projected.
type workflowPage struct {
	Data       map[string]json.RawMessage `json:"data"`
	TotalCount json.RawMessage            `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool    `json:"hasNextPage"`
		EndCursor   *string `json:"endCursor"`
	} `json:"pageInfo"`
}

type workflowRecord struct {
	ID                     string          `json:"id"`
	Name                   *string         `json:"name"`
	Position               json.RawMessage `json:"position"`
	CoreWorkflowID         *string         `json:"coreWorkflowId"`
	Statuses               []string        `json:"statuses"`
	LastPublishedVersionID *string         `json:"lastPublishedVersionId"`
	CreatedBy              json.RawMessage `json:"createdBy"`
	UpdatedBy              json.RawMessage `json:"updatedBy"`
	CreatedAt              *string         `json:"createdAt"`
	UpdatedAt              *string         `json:"updatedAt"`
}

func (r workflowRecord) summary(op string) (WorkflowSummary, error) {
	if !validUUID(r.ID) {
		return WorkflowSummary{}, provider.InvalidResponse(op, "Twenty returned a workflow without a usable identifier")
	}
	summary := WorkflowSummary{ID: r.ID, Name: capName(r.Name), Statuses: []string{},
		Position: plainNumber(r.Position), CoreWorkflowID: uuidText(r.CoreWorkflowID),
		CreatedBy: actorObject(r.CreatedBy), UpdatedBy: actorObject(r.UpdatedBy),
		CreatedAt: timestamp(r.CreatedAt), UpdatedAt: timestamp(r.UpdatedAt)}
	for _, status := range r.Statuses {
		summary.Statuses = append(summary.Statuses, enumToken(workflowStatuses, status))
	}
	if r.LastPublishedVersionID != nil && validUUID(*r.LastPublishedVersionID) {
		summary.LastPublishedVersionID = *r.LastPublishedVersionID
	}
	return summary, nil
}

func capName(name *string) string {
	if name == nil {
		return ""
	}
	return capLabelTo(*name, workflowNameMax)
}

func capLabelTo(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

// enumToken maps a Twenty enum value onto the known values; anything else cannot reach a result.
func enumToken(known []string, value string) string {
	for _, candidate := range known {
		if candidate == value {
			return value
		}
	}
	return workflowUnknown
}

// timestamp keeps only a well-formed RFC 3339 time.
func timestamp(value *string) string {
	if value == nil {
		return ""
	}
	if _, err := time.Parse(time.RFC3339Nano, *value); err != nil {
		return ""
	}
	return *value
}

// workflowPaging checks the arguments every paged workflow tool shares.
func workflowPaging(limit int, cursor string, binding []byte) (int, string, error) {
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > maxPageSize {
		return 0, "", invalidRequest("limit must be between 1 and " + strconv.Itoa(maxPageSize))
	}
	after := ""
	if cursor != "" {
		inner, ok := provider.DecodeCursor(binding, cursor, maxBoundCursorLen)
		if !ok || len(inner) > maxCursorLength || !safeCursor(inner) {
			return 0, "", invalidRequest("cursor is not a next_cursor of this request; start again without cursor")
		}
		after = inner
	}
	return limit, after, nil
}

func (p *workflowPage) nextCursor(op string, binding []byte) (string, error) {
	if !p.PageInfo.HasNextPage {
		return "", nil
	}
	end := ""
	if p.PageInfo.EndCursor != nil {
		end = *p.PageInfo.EndCursor
	}
	if end == "" || len(end) > maxCursorLength || !safeCursor(end) {
		return "", provider.InvalidResponse(op, "Twenty returned an unusable cursor")
	}
	return provider.EncodeCursor(binding, end), nil
}

func (p *workflowPage) items(op, plural string, limit int) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if json.Unmarshal(p.Data[plural], &items) != nil || items == nil || len(items) > limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of "+plural)
	}
	return items, nil
}

func workflowQuery(limit int, order, after string) url.Values {
	values := url.Values{}
	values.Set("limit", strconv.Itoa(limit))
	values.Set("depth", noRelations)
	values.Set("order_by", order)
	if after != "" {
		values.Set("starting_after", after)
	}
	return values
}

const newestFirst = "createdAt[DescNullsLast]"

// workflowNameFilter checks the name filter of workflows.list and builds the ilike filter. The wildcards of
// the pattern and the quote are masked, so the name is always matched as plain text; a backslash is refused
// because Twenty's treatment of an escaped backslash is not documented.
func workflowNameFilter(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if utf8.RuneCountInString(name) > workflowNameFilterMax || strings.TrimSpace(name) == "" ||
		!cleanFilterString(name) || strings.ContainsRune(name, '\\') {
		return "", invalidRequest("name must be 1 to 100 characters, not blank, without control characters or backslash")
	}
	masked := strings.NewReplacer(`%`, `\%`, `_`, `\_`, `"`, `\"`).Replace(name)
	return `name[ilike]:"%` + masked + `%"`, nil
}

// ListWorkflows reads one page of workflows.
func (c *Client) ListWorkflows(ctx context.Context, connection, name string, limit int, cursor string) (*WorkflowList, error) {
	const op = "list workflows"
	filter, err := workflowNameFilter(name)
	if err != nil {
		return nil, err
	}
	parts := []any{"workflows.list", connection}
	if name != "" {
		parts = append(parts, name)
	}
	binding := provider.CursorBinding(parts...)
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	values := workflowQuery(limit, newestFirst, after)
	if filter != "" {
		values.Set("filter", filter)
	}
	var page workflowPage
	if err := c.get(ctx, op, workflowsPath, values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	items, err := page.items(op, "workflows", limit)
	if err != nil {
		return nil, err
	}
	total, err := pageTotal(op, page.TotalCount)
	if err != nil {
		return nil, err
	}
	result := &WorkflowList{Workflows: make([]WorkflowSummary, 0, len(items)), HasMore: page.PageInfo.HasNextPage, TotalCount: total}
	for _, item := range items {
		var record workflowRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable workflow")
		}
		summary, err := record.summary(op)
		if err != nil {
			return nil, err
		}
		result.Workflows = append(result.Workflows, summary)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

type versionRecord struct {
	ID                    string          `json:"id"`
	Name                  *string         `json:"name"`
	Position              json.RawMessage `json:"position"`
	CoreWorkflowVersionID *string         `json:"coreWorkflowVersionId"`
	Status                string          `json:"status"`
	WorkflowID            string          `json:"workflowId"`
	Trigger               json.RawMessage `json:"trigger"`
	Steps                 json.RawMessage `json:"steps"`
	CreatedAt             *string         `json:"createdAt"`
	UpdatedAt             *string         `json:"updatedAt"`
}

// typeOf reads only the type member of a trigger or step; its settings are decoded into nothing.
func typeOf(raw json.RawMessage, known []string) string {
	var typed struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &typed) != nil {
		return workflowUnknown
	}
	return enumToken(known, typed.Type)
}

func (r versionRecord) project(op, workflowID string) (WorkflowVersion, error) {
	if !validUUID(r.ID) || !equalUUID(r.WorkflowID, workflowID) {
		return WorkflowVersion{}, provider.InvalidResponse(op, "Twenty returned a version of another workflow or without a usable identifier")
	}
	version := WorkflowVersion{ID: r.ID, Name: capName(r.Name), Status: enumToken(workflowVersionStatuses, r.Status),
		Position: plainNumber(r.Position), WorkflowID: r.WorkflowID,
		CoreWorkflowVersionID: uuidText(r.CoreWorkflowVersionID),
		CreatedAt:             timestamp(r.CreatedAt), UpdatedAt: timestamp(r.UpdatedAt)}
	if trimmed := strings.TrimSpace(string(r.Trigger)); trimmed != "" && trimmed != "null" {
		version.TriggerType = typeOf(r.Trigger, workflowTriggerTypes)
	}
	if trimmed := strings.TrimSpace(string(r.Steps)); trimmed != "" && trimmed != "null" {
		var steps []json.RawMessage
		if json.Unmarshal(r.Steps, &steps) != nil {
			return WorkflowVersion{}, provider.InvalidResponse(op, "Twenty returned unusable steps")
		}
		version.StepCount = len(steps)
		for i, step := range steps {
			if i == workflowStepTypeMax {
				break
			}
			version.StepTypes = append(version.StepTypes, typeOf(step, workflowStepTypes))
		}
	}
	return version, nil
}

func equalUUID(a, b string) bool { return strings.EqualFold(a, b) }

// GetWorkflow reads one workflow and its newest versions with two fixed requests.
func (c *Client) GetWorkflow(ctx context.Context, id string) (*WorkflowDetail, error) {
	const op = "get workflow"
	if !validUUID(id) {
		return nil, invalidRequest("the workflow id must be a UUID")
	}
	var one struct {
		Data struct {
			Workflow workflowRecord `json:"workflow"`
		} `json:"data"`
	}
	if err := c.get(ctx, op, workflowsPath+"/"+url.PathEscape(id), url.Values{"depth": {noRelations}},
		maxResponseBytes, &one); err != nil {
		return nil, err
	}
	if !equalUUID(one.Data.Workflow.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different workflow than the requested one")
	}
	summary, err := one.Data.Workflow.summary(op)
	if err != nil {
		return nil, err
	}
	values := workflowQuery(workflowVersionsMax, newestFirst, "")
	values.Set("filter", "workflowId[eq]:"+id)
	var page workflowPage
	if err := c.get(ctx, op, workflowVersionsPath, values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	items, err := page.items(op, "workflowVersions", workflowVersionsMax)
	if err != nil {
		return nil, err
	}
	detail := &WorkflowDetail{WorkflowSummary: summary, Versions: make([]WorkflowVersion, 0, len(items)),
		HasMoreVersions: page.PageInfo.HasNextPage}
	for _, item := range items {
		var record versionRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable version")
		}
		version, err := record.project(op, id)
		if err != nil {
			return nil, err
		}
		detail.Versions = append(detail.Versions, version)
	}
	return detail, nil
}

type runRecord struct {
	ID                    string          `json:"id"`
	Name                  *string         `json:"name"`
	Position              json.RawMessage `json:"position"`
	Status                string          `json:"status"`
	WorkflowID            string          `json:"workflowId"`
	CoreWorkflowID        *string         `json:"coreWorkflowId"`
	WorkflowVersionID     *string         `json:"workflowVersionId"`
	CoreWorkflowVersionID *string         `json:"coreWorkflowVersionId"`
	CreatedBy             json.RawMessage `json:"createdBy"`
	UpdatedBy             json.RawMessage `json:"updatedBy"`
	EnqueuedAt            *string         `json:"enqueuedAt"`
	StartedAt             *string         `json:"startedAt"`
	EndedAt               *string         `json:"endedAt"`
	CreatedAt             *string         `json:"createdAt"`
	UpdatedAt             *string         `json:"updatedAt"`
}

// runFields keeps Twenty from sending outputs, context, and state at all; the projection is a second line.
const runFields = "id,name,position,status,workflowId,coreWorkflowId,workflowVersionId,coreWorkflowVersionId," +
	"createdBy,updatedBy,enqueuedAt,startedAt,endedAt,createdAt,updatedAt"

// ListWorkflowRuns reads one page of the runs of one workflow.
func (c *Client) ListWorkflowRuns(ctx context.Context, connection, workflowID, versionID, status string, limit int, cursor string) (*WorkflowRunList, error) {
	const op = "list workflow runs"
	if !validUUID(workflowID) {
		return nil, invalidRequest("the workflow id must be a UUID")
	}
	if versionID != "" && !validUUID(versionID) {
		return nil, invalidRequest("the workflow version id must be a UUID")
	}
	if status != "" && enumToken(workflowRunStatuses, status) == workflowUnknown {
		return nil, invalidRequest("status must be one of " + strings.Join(workflowRunStatuses, ", "))
	}
	parts := []any{"workflow-runs.list", connection, strings.ToLower(workflowID), status}
	if versionID != "" {
		parts = append(parts, strings.ToLower(versionID))
	}
	binding := provider.CursorBinding(parts...)
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	values := workflowQuery(limit, newestFirst, after)
	filter := "workflowId[eq]:" + workflowID
	if versionID != "" {
		filter += ",workflowVersionId[eq]:" + versionID
	}
	if status != "" {
		filter += ",status[eq]:" + status
	}
	values.Set("filter", filter)
	values.Set("fields", runFields)
	var page workflowPage
	if err := c.get(ctx, op, workflowRunsPath, values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	items, err := page.items(op, "workflowRuns", limit)
	if err != nil {
		return nil, err
	}
	total, err := pageTotal(op, page.TotalCount)
	if err != nil {
		return nil, err
	}
	result := &WorkflowRunList{Runs: make([]WorkflowRun, 0, len(items)), HasMore: page.PageInfo.HasNextPage, TotalCount: total}
	for _, item := range items {
		var record runRecord
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) || !equalUUID(record.WorkflowID, workflowID) {
			return nil, provider.InvalidResponse(op, "Twenty returned a run of another workflow or without a usable identifier")
		}
		run := WorkflowRun{ID: record.ID, Name: capName(record.Name), Status: enumToken(workflowRunStatuses, record.Status),
			Position: plainNumber(record.Position), WorkflowID: record.WorkflowID,
			CoreWorkflowID: uuidText(record.CoreWorkflowID), CoreWorkflowVersionID: uuidText(record.CoreWorkflowVersionID),
			CreatedBy: actorObject(record.CreatedBy), UpdatedBy: actorObject(record.UpdatedBy),
			EnqueuedAt: timestamp(record.EnqueuedAt), StartedAt: timestamp(record.StartedAt),
			EndedAt: timestamp(record.EndedAt), CreatedAt: timestamp(record.CreatedAt), UpdatedAt: timestamp(record.UpdatedAt)}
		if record.WorkflowVersionID != nil && validUUID(*record.WorkflowVersionID) {
			run.WorkflowVersionID = *record.WorkflowVersionID
		}
		result.Runs = append(result.Runs, run)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

func invokeWorkflowsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Name   string `json:"name"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list workflows", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if _, err := workflowNameFilter(args.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListWorkflows(ctx, resolved.Name, args.Name, args.Limit, args.Cursor)
}

func invokeWorkflowsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get workflow", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the workflow id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetWorkflow(ctx, args.ID)
}

func invokeWorkflowRunsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		WorkflowID        string `json:"workflow_id"`
		WorkflowVersionID string `json:"workflow_version_id"`
		Status            string `json:"status"`
		Limit             int    `json:"limit"`
		Cursor            string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list workflow runs", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.WorkflowID) {
		return nil, invalidRequest("the workflow id must be a UUID")
	}
	if args.WorkflowVersionID != "" && !validUUID(args.WorkflowVersionID) {
		return nil, invalidRequest("the workflow version id must be a UUID")
	}
	if args.Status != "" && enumToken(workflowRunStatuses, args.Status) == workflowUnknown {
		return nil, invalidRequest("status must be one of " + strings.Join(workflowRunStatuses, ", "))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListWorkflowRuns(ctx, resolved.Name, args.WorkflowID, args.WorkflowVersionID, args.Status, args.Limit, args.Cursor)
}
