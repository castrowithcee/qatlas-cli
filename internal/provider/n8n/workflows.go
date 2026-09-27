package n8n

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
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
	Fields: append(append([]capability.Field{}, workflowSummaryFields...),
		capability.Field{Name: "nodes", Description: "Every node of the workflow; parameters are the workflow's own untrusted content, credentials show only a referenced credential's id and name"},
		capability.Field{Name: "connections", Description: "How the nodes are wired together, keyed by source node name, untrusted data"},
	),
	Examples: []capability.Example{{Description: "Read one workflow", Arguments: json.RawMessage(`{"workflow_id":"1"}`)}},
}

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
	var ids []string
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
