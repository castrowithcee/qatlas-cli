package n8n

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// targetIDSchema is the JSON Schema of one n8n project or workflow identifier: an opaque path or query
// value, never a free-form string that could be mistaken for a path or a URL. Its pattern mirrors
// validTargetID exactly.
var targetIDSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxTargetIDLength) +
	`,"pattern":"^[A-Za-z0-9_-]{1,` + strconv.Itoa(maxTargetIDLength) + `}$"}`

var workflowIDArgument = capability.Argument{Name: "workflow_id",
	Description: "n8n workflow identifier; must be inside this connection's workflow allow-list when it " +
		"has one, and its project membership is always re-checked live when this connection also holds a " +
		"project allow-list", Required: true}

var tagSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

var credentialRefSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

// nodeSchema deliberately has no "parameters" length limit of its own: a node's parameters are the
// workflow's own logic, already bounded by the overall response size limit, and credentials never carries
// more than the id/name pair n8n's own API reports, see the package doc.
var nodeSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
	`"type_version":{"type":"number"},"position":{"type":"array","items":{"type":"number"}},` +
	`"disabled":{"type":"boolean"},"parameters":{"type":"object"},` +
	`"credentials":{"type":"object","additionalProperties":` + credentialRefSchema + `}},` +
	`"required":["id","name","type","disabled"],"additionalProperties":false}`

var workflowSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"active":{"type":"boolean"},` +
	`"is_archived":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"trigger_count":{"type":"integer"},"node_count":{"type":"integer"},` +
	`"tags":{"type":"array","items":` + tagSchema + `},` +
	`"project_ids":{"type":"array","items":{"type":"string"}}},` +
	`"required":["id","name","active","is_archived","created_at","updated_at","trigger_count","node_count",` +
	`"tags","project_ids"],"additionalProperties":false}`

var workflowSummaryFields = []capability.Field{
	{Name: "id", Description: "Workflow identifier, used as workflow_id by every other tool of this provider"},
	{Name: "name", Description: "Workflow name, untrusted data"},
	{Name: "active", Description: "True when the workflow is currently active (its triggers are live)"},
	{Name: "is_archived", Description: "True when the workflow is archived"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last change time, as n8n reports it"},
	{Name: "trigger_count", Description: "Number of trigger nodes n8n reports for this workflow"},
	{Name: "node_count", Description: "Number of nodes in this workflow"},
	{Name: "tags", Description: "Tags attached to this workflow, untrusted data"},
	{Name: "project_ids", Description: "Projects this workflow is shared into, when this n8n instance and " +
		"Public API version report project membership at all; empty when it does not"},
}

var n8nReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

var workflowsList = capability.Descriptor{
	ID:      Provider + ".workflows.list",
	Version: 1,
	Title:   "List n8n workflows",
	Description: "List the workflows of the bound n8n instance, restricted to its project and workflow " +
		"allow-lists when it has them; page by page with an opaque cursor",
	Tags:                       []string{"n8n", "workflows", "list", "automation"},
	Risk:                       n8nReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"active":{"type":"boolean"},"name":{"type":"string","minLength":1,"maxLength":128},` +
		`"tags":{"type":"string","minLength":1,"maxLength":256},` +
		`"project_id":` + targetIDSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"workflows":{"type":"array","items":` + workflowSummarySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["workflows","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Workflows per page, 1 to 250; 100 when omitted, matching n8n's own default"},
		{Name: "active", Description: "When set, list only active or only inactive workflows"},
		{Name: "name", Description: "When set, list only workflows n8n matches by this name"},
		{Name: "tags", Description: "When set, list only workflows n8n matches by this comma-separated tag list"},
		{Name: "project_id", Description: "When set, list only this project's workflows; must be inside " +
			"this connection's project allow-list when it has one"},
	},
	Fields: append(append([]capability.Field{}, workflowSummaryFields...),
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains"},
		capability.Field{Name: "count", Description: "Number of workflows reported on this page after this connection's allow-lists were applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of reachable workflows", Arguments: json.RawMessage(`{}`)}},
}

var workflowDetailSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"active":{"type":"boolean"},` +
	`"is_archived":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"trigger_count":{"type":"integer"},"node_count":{"type":"integer"},` +
	`"tags":{"type":"array","items":` + tagSchema + `},` +
	`"project_ids":{"type":"array","items":{"type":"string"}},` +
	`"nodes":{"type":"array","items":` + nodeSchema + `},` +
	`"connections":{"type":"object"}},` +
	`"required":["id","name","active","is_archived","created_at","updated_at","trigger_count","node_count",` +
	`"tags","project_ids","nodes","connections"],"additionalProperties":false}`

var workflowsGet = capability.Descriptor{
	ID:      Provider + ".workflows.get",
	Version: 1,
	Title:   "Get an n8n workflow",
	Description: "Read one workflow of the bound n8n instance, including its nodes and their connections; " +
		"never a credential's value, only the id and name n8n itself attaches to a node",
	Tags:                       []string{"n8n", "workflows", "get", "automation"},
	Risk:                       n8nReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":` + targetIDSchema + `},` +
		`"required":["workflow_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(workflowDetailSchema),
	Arguments:    []capability.Argument{workflowIDArgument},
	Fields:       workflowDetailFields,
	Examples:     []capability.Example{{Description: "Read one workflow", Arguments: json.RawMessage(`{"workflow_id":"1"}`)}},
}

// workflowDetailFields describes the two fields workflows.get, workflows.create, workflows.update,
// workflows.activate, and workflows.deactivate all answer with beyond the summary, since every one of them
// returns the same re-read, scope-checked workflow.
var workflowDetailFields = append(append([]capability.Field{}, workflowSummaryFields...),
	capability.Field{Name: "nodes", Description: "Every node of the workflow; parameters are the workflow's " +
		"own untrusted content, credentials show only a referenced credential's id and name"},
	capability.Field{Name: "connections", Description: "How the nodes are wired together, keyed by source " +
		"node name, untrusted data"},
)

// tagJSON, credentialRefJSON, nodeJSON, sharedJSON, and workflowJSON mirror the subset of n8n's
// workflowPublicDto this provider reads (packages/cli/src/public-api/v1/shared/spec/schemas).
type tagJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type credentialRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type nodeJSON struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Type        string                       `json:"type"`
	TypeVersion float64                      `json:"typeVersion"`
	Position    []float64                    `json:"position"`
	Disabled    bool                         `json:"disabled"`
	Parameters  json.RawMessage              `json:"parameters"`
	Credentials map[string]credentialRefJSON `json:"credentials"`
}

// sharedJSON is one entry of a workflow's "shared" array: which project it is shared into, and with which
// role. The role is read but not surfaced; membership alone decides the project allow-list check, see
// (*Client).verifyWorkflowScope.
type sharedJSON struct {
	Role      string `json:"role"`
	ProjectID string `json:"projectId"`
}

type workflowJSON struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Active       bool            `json:"active"`
	IsArchived   bool            `json:"isArchived"`
	CreatedAt    string          `json:"createdAt"`
	UpdatedAt    string          `json:"updatedAt"`
	TriggerCount int             `json:"triggerCount"`
	Tags         []tagJSON       `json:"tags"`
	Nodes        []nodeJSON      `json:"nodes"`
	Connections  json.RawMessage `json:"connections"`
	Shared       []sharedJSON    `json:"shared"`
}

// projectIDs collects the distinct, non-empty project IDs a workflow reports through "shared". An instance
// or Public API version that never sends "shared" reports none, which is exactly the case
// (*Client).verifyWorkflowScope treats as unprovable.
func (w workflowJSON) projectIDs() []string {
	seen := map[string]bool{}
	// ids starts as a non-nil, empty slice, never nil: the output schema declares project_ids a required
	// array, and a nil slice marshals to JSON null, which is not an array. A workflow with no reported
	// project at all, the common case on a non-Enterprise instance or a personal-project workflow, must
	// still answer with [], not null.
	ids := []string{}
	for _, entry := range w.Shared {
		if entry.ProjectID == "" || seen[entry.ProjectID] {
			continue
		}
		seen[entry.ProjectID] = true
		ids = append(ids, entry.ProjectID)
	}
	return ids
}

// Tag is the stable Qatlas view of one workflow tag.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CredentialRef is the stable Qatlas view of one node's reference to a credential: never its value, only
// what n8n's own API reports for it.
type CredentialRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Node is the stable Qatlas view of one workflow node. Parameters are the workflow's own untrusted content;
// Credentials never carries a secret.
type Node struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Type        string                   `json:"type"`
	TypeVersion float64                  `json:"type_version,omitempty"`
	Position    []float64                `json:"position,omitempty"`
	Disabled    bool                     `json:"disabled"`
	Parameters  json.RawMessage          `json:"parameters,omitempty"`
	Credentials map[string]CredentialRef `json:"credentials,omitempty"`
}

// WorkflowSummary is the stable, allow-list-filtered listing view of one workflow.
type WorkflowSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Active       bool     `json:"active"`
	IsArchived   bool     `json:"is_archived"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	TriggerCount int      `json:"trigger_count"`
	NodeCount    int      `json:"node_count"`
	Tags         []Tag    `json:"tags"`
	ProjectIDs   []string `json:"project_ids"`
}

// WorkflowDetail is the stable, scope-checked read view of one workflow.
type WorkflowDetail struct {
	WorkflowSummary
	Nodes       []Node          `json:"nodes"`
	Connections json.RawMessage `json:"connections"`
}

func summaryOf(w workflowJSON) WorkflowSummary {
	tags := make([]Tag, 0, len(w.Tags))
	for _, t := range w.Tags {
		tags = append(tags, Tag{ID: bounded(t.ID), Name: bounded(t.Name)})
	}
	return WorkflowSummary{
		ID: bounded(w.ID), Name: bounded(w.Name), Active: w.Active, IsArchived: w.IsArchived,
		CreatedAt: bounded(w.CreatedAt), UpdatedAt: bounded(w.UpdatedAt), TriggerCount: w.TriggerCount,
		NodeCount: len(w.Nodes), Tags: tags, ProjectIDs: w.projectIDs(),
	}
}

func detailOf(w workflowJSON) WorkflowDetail {
	nodes := make([]Node, 0, len(w.Nodes))
	for _, n := range w.Nodes {
		var refs map[string]CredentialRef
		if len(n.Credentials) > 0 {
			refs = make(map[string]CredentialRef, len(n.Credentials))
			for kind, ref := range n.Credentials {
				refs[kind] = CredentialRef{ID: bounded(ref.ID), Name: bounded(ref.Name)}
			}
		}
		nodes = append(nodes, Node{
			ID: bounded(n.ID), Name: bounded(n.Name), Type: bounded(n.Type), TypeVersion: n.TypeVersion,
			Position: n.Position, Disabled: n.Disabled, Parameters: n.Parameters, Credentials: refs,
		})
	}
	connections := w.Connections
	if len(connections) == 0 {
		connections = json.RawMessage(`{}`)
	}
	return WorkflowDetail{WorkflowSummary: summaryOf(w), Nodes: nodes, Connections: connections}
}

type workflowsPageJSON struct {
	Data       []workflowJSON `json:"data"`
	NextCursor string         `json:"nextCursor"`
}

// WorkflowsPage is one paginated, allow-list-filtered listing of workflows.
type WorkflowsPage struct {
	Workflows []WorkflowSummary `json:"workflows"`
	Cursor    string            `json:"cursor,omitempty"`
	HasMore   bool              `json:"has_more"`
	Count     int               `json:"count"`
}

type workflowsListArguments struct {
	Cursor    string `json:"cursor"`
	Limit     int    `json:"limit"`
	Active    *bool  `json:"active"`
	Name      string `json:"name"`
	Tags      string `json:"tags"`
	ProjectID string `json:"project_id"`
}

func invokeWorkflowsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input workflowsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list workflows", "the validated arguments could not be read")
	}
	if input.ProjectID != "" {
		if err := selectProjectFilter(resolved, input.ProjectID); err != nil {
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
	return client.ListWorkflows(ctx, input.Cursor, limit, input.Active, input.Name, input.Tags, input.ProjectID)
}

// ListWorkflows reads one page of workflows, filtering server-side by every argument n8n's own "GET
// /workflows" accepts, and defensively re-applying this connection's workflow and project allow-lists to
// the page it answers with: the "shared" array of each returned workflow is already part of that same
// answer, so this needs no extra request, unlike a single workflow's live re-check, see
// (*Client).verifyWorkflowScope.
func (c *Client) ListWorkflows(ctx context.Context, cursor string, limit int, active *bool, name, tags,
	projectID string) (*WorkflowsPage, error) {
	const op = "list workflows"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if active != nil {
		query.Set("active", strconv.FormatBool(*active))
	}
	if name != "" {
		query.Set("name", name)
	}
	if tags != "" {
		query.Set("tags", tags)
	}
	if projectID != "" {
		query.Set("projectId", projectID)
	}
	var page workflowsPageJSON
	if err := c.get(ctx, op, "/workflows", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	summaries := make([]WorkflowSummary, 0, len(page.Data))
	for _, w := range page.Data {
		if !c.scope.allowsWorkflow(w.ID) || !c.scope.allowsAnyProject(w.projectIDs()) {
			continue
		}
		summaries = append(summaries, summaryOf(w))
	}
	return &WorkflowsPage{
		Workflows: summaries, Cursor: page.NextCursor, HasMore: page.NextCursor != "", Count: len(summaries),
	}, nil
}

type workflowArguments struct {
	WorkflowID string `json:"workflow_id"`
}

func invokeWorkflowsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input workflowArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get workflow", "the validated arguments could not be read")
	}
	if err := selectWorkflow(resolved, input.WorkflowID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	workflow, err := client.fetchWorkflow(ctx, "get workflow", input.WorkflowID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyWorkflowScope(workflow); err != nil {
		return nil, err
	}
	detail := detailOf(*workflow)
	return &detail, nil
}

// fetchWorkflow reads exactly one workflow by ID, the same read both workflows.get and the executions
// provider's live project check use.
func (c *Client) fetchWorkflow(ctx context.Context, op, workflowID string) (*workflowJSON, error) {
	var workflow workflowJSON
	if err := c.get(ctx, op, "/workflows/"+url.PathEscape(workflowID), nil, &workflow, maxResponseBytes); err != nil {
		return nil, err
	}
	if workflow.ID != workflowID {
		return nil, invalidResponse(op, "n8n answered with a workflow other than the one requested")
	}
	return &workflow, nil
}

// verifyWorkflowScope confirms, against the workflow n8n itself just answered with, that it belongs to a
// project this connection's project allow-list admits. Local configuration alone proves nothing about what
// a workflow_id really resolves to; this is the live check the package doc describes. A workflow outside
// the allow-list, and one whose instance or Public API version reports no project membership at all while a
// project allow-list is configured, are refused the same way: an invalid request, never a provider error,
// and the failure never names the workflow's project.
func (c *Client) verifyWorkflowScope(workflow *workflowJSON) error {
	if len(c.scope.projects) == 0 {
		return nil
	}
	ids := workflow.projectIDs()
	if len(ids) == 0 {
		return invalidRequest("this n8n instance or Public API version did not report this workflow's " +
			"project membership, which a project-restricted connection cannot verify")
	}
	if !c.scope.allowsAnyProject(ids) {
		return invalidRequest("workflow_id belongs to a project outside the targets of this connection")
	}
	return nil
}

// verifyPlacement re-applies this connection's project allow-list to a workflow a create or update already
// produced, once the one changing request has already been sent. Unlike verifyWorkflowScope's pre-request
// refusal, a mismatch here can no longer mean "never sent": it is reported as a provider error, not an
// invalid request, because the change has already taken effect and this milestone offers no delete tool to
// undo it; the caller must remove the misplaced workflow directly in n8n.
func (c *Client) verifyPlacement(op string, workflow *workflowJSON) error {
	if len(c.scope.projects) == 0 {
		return nil
	}
	if ids := workflow.projectIDs(); len(ids) == 0 || !c.scope.allowsAnyProject(ids) {
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "n8n did not keep the result inside this connection's allowed projects; the change " +
				"already took effect and this milestone has no delete tool to undo it"}
	}
	return nil
}

// n8nChangeRisk is the contract every confirmed change of this provider shares: it always needs its own
// confirmation, whatever its idempotency, and it reaches the open world of one n8n instance's workflows and
// executions.
func n8nChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

// nodeCredentialsSchema restricts a node's credentials to the id/name reference pair n8n's own workflow
// reads already report, the same shape credentialRefSchema and CredentialRef give a read node: n8n resolves
// and validates the credential itself from that reference, so this provider never carries anything else of
// it through a create or update.
var nodeCredentialsSchema = `{"type":"object","additionalProperties":` + credentialRefSchema + `}`

// nodeWriteSchema is the JSON Schema of one node a caller may create or replace with workflows.create or
// workflows.update. It mirrors the fields workflows.get already exposes for a node (id, name, type,
// type_version, position, disabled, parameters, credentials): n8n's own create/update schemas additionally
// accept execution-behaviour fields such as notes, onError, retryOnFail, maxTries, and webhookId.
// qatlas-dev: this is a deliberate simplification, not a spec limit; extend nodeWriteSchema and nodeInput
// together, in step, if a caller needs one of those fields.
var nodeWriteSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string","minLength":1,"maxLength":128},` +
	`"name":{"type":"string","minLength":1,"maxLength":128},` +
	`"type":{"type":"string","minLength":1,"maxLength":256},` +
	`"type_version":{"type":"number"},` +
	`"position":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2},` +
	`"disabled":{"type":"boolean"},"parameters":{"type":"object"},` +
	`"credentials":` + nodeCredentialsSchema + `},` +
	`"required":["id","name","type"],"additionalProperties":false}`

// nodeInput is one node argument of workflows.create or workflows.update, in this provider's own snake_case
// input shape; wire converts it to the camelCase shape n8n's own API reads.
type nodeInput struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Type        string                       `json:"type"`
	TypeVersion float64                      `json:"type_version"`
	Position    []float64                    `json:"position"`
	Disabled    bool                         `json:"disabled"`
	Parameters  json.RawMessage              `json:"parameters"`
	Credentials map[string]credentialRefJSON `json:"credentials"`
}

// wire converts one node argument to the shape n8n's createWorkflow and updateWorkflow requests read. It
// never carries anything of a node's credentials beyond the id/name reference n8n itself resolves.
func (n nodeInput) wire() map[string]any {
	out := map[string]any{"id": n.ID, "name": n.Name, "type": n.Type, "disabled": n.Disabled}
	if n.TypeVersion != 0 {
		out["typeVersion"] = n.TypeVersion
	}
	if len(n.Position) > 0 {
		out["position"] = n.Position
	}
	if len(n.Parameters) > 0 {
		out["parameters"] = n.Parameters
	}
	if len(n.Credentials) > 0 {
		credentials := make(map[string]map[string]string, len(n.Credentials))
		for kind, ref := range n.Credentials {
			credentials[kind] = map[string]string{"id": ref.ID, "name": ref.Name}
		}
		out["credentials"] = credentials
	}
	return out
}

// settingsWriteSchema is the JSON Schema of the workflow settings this provider offers, the subset of
// n8n's own settings object (createWorkflow.generated.yml) a caller may reasonably set programmatically. It
// excludes binaryMode and credentialResolverId, which that same spec documents as derived, internal
// settings whose value is ignored on a create or an update, and it excludes customTelemetryTags and the
// top-level nodeGroups, which are canvas and telemetry decoration with no execution effect.
// qatlas-dev: extend settingsWriteSchema and settingsInput together, in step, if a caller needs one of those.
var settingsWriteSchema = `{"type":"object","properties":{` +
	`"save_execution_progress":{"type":"boolean"},"save_manual_executions":{"type":"boolean"},` +
	`"save_data_error_execution":{"type":"string","enum":["all","none"]},` +
	`"save_data_success_execution":{"type":"string","enum":["all","none"]},` +
	`"execution_timeout":{"type":"number"},"error_workflow":` + targetIDSchema + `,` +
	`"timezone":{"type":"string","minLength":1,"maxLength":64},` +
	`"execution_order":{"type":"string","minLength":1,"maxLength":16},` +
	`"caller_policy":{"type":"string","enum":["any","none","workflowsFromAList","workflowsFromSameOwner"]},` +
	`"caller_ids":{"type":"string","maxLength":2048},` +
	`"time_saved_mode":{"type":"string","enum":["fixed","dynamic"]},` +
	`"time_saved_per_execution":{"type":"number"},` +
	`"redaction_policy":{"type":"string","enum":["none","non-manual","manual-only","all"]},` +
	`"available_in_mcp":{"type":"boolean"}},"additionalProperties":false}`

// settingsInput is the settings argument of workflows.create or workflows.update, in this provider's own
// snake_case input shape. Every value is optional; wire sends only the ones a caller actually set, and n8n
// applies its own defaults to everything else.
type settingsInput struct {
	SaveExecutionProgress    *bool    `json:"save_execution_progress"`
	SaveManualExecutions     *bool    `json:"save_manual_executions"`
	SaveDataErrorExecution   string   `json:"save_data_error_execution"`
	SaveDataSuccessExecution string   `json:"save_data_success_execution"`
	ExecutionTimeout         *float64 `json:"execution_timeout"`
	ErrorWorkflow            string   `json:"error_workflow"`
	Timezone                 string   `json:"timezone"`
	ExecutionOrder           string   `json:"execution_order"`
	CallerPolicy             string   `json:"caller_policy"`
	CallerIDs                string   `json:"caller_ids"`
	TimeSavedMode            string   `json:"time_saved_mode"`
	TimeSavedPerExecution    *float64 `json:"time_saved_per_execution"`
	RedactionPolicy          string   `json:"redaction_policy"`
	AvailableInMCP           *bool    `json:"available_in_mcp"`
}

// wire converts the settings argument to the camelCase shape n8n's own settings object reads.
func (s settingsInput) wire() map[string]any {
	out := map[string]any{}
	if s.SaveExecutionProgress != nil {
		out["saveExecutionProgress"] = *s.SaveExecutionProgress
	}
	if s.SaveManualExecutions != nil {
		out["saveManualExecutions"] = *s.SaveManualExecutions
	}
	if s.SaveDataErrorExecution != "" {
		out["saveDataErrorExecution"] = s.SaveDataErrorExecution
	}
	if s.SaveDataSuccessExecution != "" {
		out["saveDataSuccessExecution"] = s.SaveDataSuccessExecution
	}
	if s.ExecutionTimeout != nil {
		out["executionTimeout"] = *s.ExecutionTimeout
	}
	if s.ErrorWorkflow != "" {
		out["errorWorkflow"] = s.ErrorWorkflow
	}
	if s.Timezone != "" {
		out["timezone"] = s.Timezone
	}
	if s.ExecutionOrder != "" {
		out["executionOrder"] = s.ExecutionOrder
	}
	if s.CallerPolicy != "" {
		out["callerPolicy"] = s.CallerPolicy
	}
	if s.CallerIDs != "" {
		out["callerIds"] = s.CallerIDs
	}
	if s.TimeSavedMode != "" {
		out["timeSavedMode"] = s.TimeSavedMode
	}
	if s.TimeSavedPerExecution != nil {
		out["timeSavedPerExecution"] = *s.TimeSavedPerExecution
	}
	if s.RedactionPolicy != "" {
		out["redactionPolicy"] = s.RedactionPolicy
	}
	if s.AvailableInMCP != nil {
		out["availableInMCP"] = *s.AvailableInMCP
	}
	return out
}

// workflowWriteArguments are the four fields workflows.create and workflows.update share: n8n's own
// createWorkflow and updateWorkflow schemas both require exactly name, nodes, connections, and settings, and
// reject every other top-level property (additionalProperties: false), so this provider sends only those
// four, plus, for create only, projectId.
var workflowWriteInputProperties = `"name":{"type":"string","minLength":1,"maxLength":128},` +
	`"nodes":{"type":"array","maxItems":` + strconv.Itoa(maxWorkflowNodes) + `,"items":` + nodeWriteSchema + `},` +
	`"connections":{"type":"object"},"settings":` + settingsWriteSchema

var workflowWriteArguments = []capability.Argument{
	{Name: "name", Description: "Workflow name, 1 to 128 characters", Required: true},
	{Name: "nodes", Description: "Every node of the workflow, replacing whatever it held before; the id, " +
		"name, and type n8n itself validates are required, and a credential reference carries only the id and " +
		"name n8n itself resolves", Required: true},
	{Name: "connections", Description: "How the nodes are wired together, keyed by source node name, " +
		"replacing whatever the workflow held before", Required: true},
	{Name: "settings", Description: "Execution and behaviour settings of the workflow, replacing whatever it " +
		"held before; {} keeps n8n's own defaults", Required: true},
}

// workflowWriteBody validates one create or update argument set and builds the shared request body: name,
// nodes, connections, and settings, the exact four properties n8n's own schema requires and allows.
func workflowWriteBody(name string, nodes []nodeInput, connections json.RawMessage, settings settingsInput) (map[string]any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, invalidRequest("name must not be empty")
	}
	if len(nodes) > maxWorkflowNodes {
		return nil, invalidRequest(fmt.Sprintf("nodes holds at most %d entries", maxWorkflowNodes))
	}
	seen := make(map[string]bool, len(nodes))
	wireNodes := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if n.ID == "" || n.Name == "" || n.Type == "" {
			return nil, invalidRequest("every node needs id, name, and type")
		}
		if seen[n.ID] {
			return nil, invalidRequest("node ids must be distinct")
		}
		seen[n.ID] = true
		wireNodes = append(wireNodes, n.wire())
	}
	if len(connections) == 0 {
		connections = json.RawMessage(`{}`)
	}
	body := map[string]any{
		"name": name, "nodes": wireNodes, "connections": connections, "settings": settings.wire(),
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > maxWorkflowWriteBytes {
		return nil, invalidRequest(fmt.Sprintf("the workflow definition is larger than the %d byte limit",
			maxWorkflowWriteBytes))
	}
	return body, nil
}

var workflowsCreate = capability.Descriptor{
	ID:      Provider + ".workflows.create",
	Version: 1,
	Title:   "Create an n8n workflow",
	Description: "Create one workflow of the bound n8n instance from its name, nodes, connections, and " +
		"settings; a repeated call creates a second workflow. There is no tool to start the created workflow: " +
		"the Public API documents no endpoint for that",
	Tags:                       []string{"n8n", "workflows", "create", "automation"},
	Risk:                       n8nChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + workflowWriteInputProperties + `,` +
		`"project_id":` + targetIDSchema + `},"required":["name","nodes","connections","settings"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(workflowDetailSchema),
	Arguments: append(append([]capability.Argument{}, workflowWriteArguments...),
		capability.Argument{Name: "project_id", Description: "Project to create the workflow in; required, " +
			"and must be inside this connection's project allow-list, when this connection restricts projects; " +
			"otherwise optional, defaulting to the API key owner's personal project"},
	),
	Fields: workflowDetailFields,
	Examples: []capability.Example{{
		Description: "Create a minimal manually-triggered workflow",
		Arguments: json.RawMessage(`{"name":"My workflow","nodes":[{"id":"1","name":"Manual Trigger",` +
			`"type":"n8n-nodes-base.manualTrigger"}],"connections":{},"settings":{}}`),
	}},
}

type workflowsCreateArguments struct {
	Name        string          `json:"name"`
	Nodes       []nodeInput     `json:"nodes"`
	Connections json.RawMessage `json:"connections"`
	Settings    settingsInput   `json:"settings"`
	ProjectID   string          `json:"project_id"`
}

func invokeWorkflowsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create workflow"
	var input workflowsCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.workflows) > 0 {
		return nil, invalidRequest("this connection restricts workflows by an allow-list, so it cannot " +
			"create one: a newly created workflow can never already be on that list")
	}
	switch {
	case len(bound.projects) > 0 && input.ProjectID == "":
		return nil, invalidRequest("this connection restricts projects, so workflows.create requires " +
			"project_id naming one of the allowed projects")
	case input.ProjectID != "":
		if err := selectProjectFilter(resolved, input.ProjectID); err != nil {
			return nil, err
		}
	}
	body, err := workflowWriteBody(input.Name, input.Nodes, input.Connections, input.Settings)
	if err != nil {
		return nil, err
	}
	if input.ProjectID != "" {
		body["projectId"] = input.ProjectID
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateWorkflow(ctx, body)
}

// CreateWorkflow sends the one changing POST /workflows request, then re-reads the workflow it created to
// answer with the same scope-checked detail workflows.get would, and to re-apply this connection's project
// allow-list to where n8n actually placed it, see (*Client).verifyPlacement.
func (c *Client) CreateWorkflow(ctx context.Context, body map[string]any) (*WorkflowDetail, error) {
	const op = "create workflow"
	var created struct {
		ID string `json:"id"`
	}
	if err := c.change(ctx, op, http.MethodPost, "/workflows", nil, body, &created, maxWorkflowWriteBytes); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "n8n did not report the ID of the created workflow" + uncertain}
	}
	workflow, err := c.fetchWorkflow(ctx, op, created.ID)
	if err != nil {
		return nil, err
	}
	if err := c.verifyPlacement(op, workflow); err != nil {
		return nil, err
	}
	detail := detailOf(*workflow)
	return &detail, nil
}

var workflowsUpdate = capability.Descriptor{
	ID:      Provider + ".workflows.update",
	Version: 1,
	Title:   "Replace an n8n workflow",
	Description: "Replace one workflow's name, nodes, connections, and settings of the bound n8n instance " +
		"with a full PUT, exactly as n8n's own updateWorkflow endpoint is: fields left out are not kept, they " +
		"are cleared. Never changes the active state; workflows.activate and workflows.deactivate own that",
	Tags:                       []string{"n8n", "workflows", "update", "automation"},
	Risk:                       n8nChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":` + targetIDSchema + `,` +
		workflowWriteInputProperties + `,"publish_if_active":{"type":"boolean"}},` +
		`"required":["workflow_id","name","nodes","connections","settings"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(workflowDetailSchema),
	Arguments: append(append([]capability.Argument{workflowIDArgument}, workflowWriteArguments...),
		capability.Argument{Name: "publish_if_active", Description: "When the workflow is currently active, " +
			"whether n8n republishes it with this new content right away; true (n8n's own default) when " +
			"omitted. false saves the change as a draft on the currently active version instead. Has no " +
			"effect on a workflow that is not active"},
	),
	Fields: workflowDetailFields,
	Examples: []capability.Example{{
		Description: "Replace one workflow's content",
		Arguments: json.RawMessage(`{"workflow_id":"1","name":"My workflow","nodes":[{"id":"1",` +
			`"name":"Manual Trigger","type":"n8n-nodes-base.manualTrigger"}],"connections":{},"settings":{}}`),
	}},
}

type workflowsUpdateArguments struct {
	WorkflowID      string          `json:"workflow_id"`
	Name            string          `json:"name"`
	Nodes           []nodeInput     `json:"nodes"`
	Connections     json.RawMessage `json:"connections"`
	Settings        settingsInput   `json:"settings"`
	PublishIfActive *bool           `json:"publish_if_active"`
}

func invokeWorkflowsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update workflow"
	var input workflowsUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectWorkflow(resolved, input.WorkflowID); err != nil {
		return nil, err
	}
	body, err := workflowWriteBody(input.Name, input.Nodes, input.Connections, input.Settings)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	workflow, err := client.fetchWorkflow(ctx, op, input.WorkflowID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyWorkflowScope(workflow); err != nil {
		return nil, err
	}
	return client.UpdateWorkflow(ctx, input.WorkflowID, body, input.PublishIfActive)
}

// UpdateWorkflow sends the one changing PUT /workflows/{id} request, then re-reads the workflow to answer
// with the same scope-checked detail workflows.get would, and to defensively re-apply this connection's
// project allow-list, see (*Client).verifyPlacement, even though update's own request body cannot move a
// workflow between projects.
func (c *Client) UpdateWorkflow(ctx context.Context, workflowID string, body map[string]any,
	publishIfActive *bool) (*WorkflowDetail, error) {
	const op = "update workflow"
	var query url.Values
	if publishIfActive != nil {
		query = url.Values{"publishIfActive": {strconv.FormatBool(*publishIfActive)}}
	}
	if err := c.change(ctx, op, http.MethodPut, "/workflows/"+url.PathEscape(workflowID), query, body, nil,
		maxWorkflowWriteBytes); err != nil {
		return nil, err
	}
	workflow, err := c.fetchWorkflow(ctx, op, workflowID)
	if err != nil {
		return nil, err
	}
	if err := c.verifyPlacement(op, workflow); err != nil {
		return nil, err
	}
	detail := detailOf(*workflow)
	return &detail, nil
}

// workflowActivationDescriptor builds the shared shape of workflows.activate and workflows.deactivate: both
// take only workflow_id and answer the same re-read WorkflowDetail workflows.get would.
func workflowActivationDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID:                         Provider + ".workflows." + action,
		Version:                    1,
		Title:                      title,
		Description:                description,
		Tags:                       []string{"n8n", "workflows", action, "automation"},
		Risk:                       n8nChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider:                   Provider,
		RequiresExplicitConnection: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":` + targetIDSchema + `},` +
			`"required":["workflow_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(workflowDetailSchema),
		Arguments:    []capability.Argument{workflowIDArgument},
		Fields:       workflowDetailFields,
		Examples: []capability.Example{{Description: title,
			Arguments: json.RawMessage(`{"workflow_id":"1"}`)}},
	}
}

var workflowsActivate = workflowActivationDescriptor("activate", "Activate an n8n workflow",
	"Activate one workflow of the bound n8n instance, turning its triggers live; repeating it on an already "+
		"active workflow leaves it active. Uses n8n's own activate endpoint, deprecated in favour of publish, "+
		"which is out of this milestone's scope")

var workflowsDeactivate = workflowActivationDescriptor("deactivate", "Deactivate an n8n workflow",
	"Deactivate one workflow of the bound n8n instance, turning its triggers off; repeating it on an already "+
		"inactive workflow leaves it inactive. Uses n8n's own deactivate endpoint, deprecated in favour of "+
		"unpublish, which is out of this milestone's scope")

func invokeWorkflowsActivate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeWorkflowActivation(ctx, resolved, secrets, red, raw, true)
}

func invokeWorkflowsDeactivate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeWorkflowActivation(ctx, resolved, secrets, red, raw, false)
}

func invokeWorkflowActivation(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, active bool) (any, error) {
	op := "deactivate workflow"
	if active {
		op = "activate workflow"
	}
	var input workflowArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectWorkflow(resolved, input.WorkflowID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	workflow, err := client.fetchWorkflow(ctx, op, input.WorkflowID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyWorkflowScope(workflow); err != nil {
		return nil, err
	}
	return client.SetWorkflowActive(ctx, input.WorkflowID, active)
}

// SetWorkflowActive sends the one changing POST to n8n's activate or deactivate endpoint, then re-reads the
// workflow to confirm the active state actually changed and to answer with the same scope-checked detail
// workflows.get would. deactivateWorkflow's own spec declares no request body at all, so none is sent for
// it; activateWorkflow's optional versionId/name/description fields are not offered by this provider, so an
// empty object is sent, which n8n reads as "activate the latest version".
func (c *Client) SetWorkflowActive(ctx context.Context, workflowID string, active bool) (*WorkflowDetail, error) {
	op, suffix := "deactivate workflow", "/deactivate"
	var body any
	if active {
		op, suffix, body = "activate workflow", "/activate", map[string]any{}
	}
	if err := c.change(ctx, op, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+suffix, nil, body, nil,
		maxResponseBytes); err != nil {
		return nil, err
	}
	workflow, err := c.fetchWorkflow(ctx, op, workflowID)
	if err != nil {
		return nil, err
	}
	if workflow.Active != active {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "n8n did not report the requested active state after the change" + uncertain}
	}
	detail := detailOf(*workflow)
	return &detail, nil
}
