package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxProjectNameLength mirrors the Public API's own limit for a project name (projectNameSchema,
// packages/@n8n/api-types/src/schemas/project.schema.ts: 1 to 255 characters).
const maxProjectNameLength = 255

// projectPermissionMessage is the one message of a 403 on a projects endpoint. n8n answers 403 for three
// different causes without telling them apart in a way this provider reads: the Projects feature is not
// licensed (the license middleware), the API key's scopes do not include the project scope, and the key
// owner's role may not do this. The provider body is never read into a message.
const projectPermissionMessage = "n8n refused this projects operation: the instance's license may not include " +
	"the Projects feature (project roles), the API key may lack the project scope, or its owner's role may not " +
	"manage projects; Qatlas cannot tell which of the three"

// maxProjectTelemetryTags bounds the custom telemetry tags listed for one project, whatever n8n sends.
const maxProjectTelemetryTags = 20

var projectSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"icon":{"type":"object","properties":{"type":{"type":"string"},"value":{"type":"string"},` +
	`"color":{"type":"string"}},"additionalProperties":false},` +
	`"description":{"type":"string"},` +
	`"custom_telemetry_tags":{"type":"array","items":{"type":"object","properties":{` +
	`"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"],"additionalProperties":false}},` +
	`"creator_id":{"type":"string"}},` +
	`"required":["id","name","type","created_at","updated_at"],"additionalProperties":false}`

var projectSummaryFields = []capability.Field{
	{Name: "id", Description: "Project identifier, usable as project_id and as a project/PROJECT_ID target"},
	{Name: "name", Description: "Project name, untrusted data"},
	{Name: "type", Description: "personal or team, as n8n reports it"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last change time, as n8n reports it"},
	{Name: "icon", Description: "Project icon (type, value, color); absent when the project has none; untrusted data"},
	{Name: "description", Description: "Project description, untrusted data, capped"},
	{Name: "custom_telemetry_tags", Description: "Custom telemetry tags (key, value) of the project, untrusted data, " +
		"capped in number and length"},
	{Name: "creator_id", Description: "Identifier of the user who created the project, personal data, untrusted"},
}

var projectIDArgument = capability.Argument{Name: "project_id",
	Description: "n8n project identifier; must be inside this connection's project allow-list when it has one",
	Required:    true}

var projectNameSchemaJSON = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxProjectNameLength) + `}`

var projectNameArgument = capability.Argument{Name: "name",
	Description: "Project name, 1 to " + strconv.Itoa(maxProjectNameLength) + " characters, no control characters",
	Required:    true}

var projectsList = capability.Descriptor{
	ID:      Provider + ".projects.list",
	Version: 1,
	Title:   "List n8n projects",
	Description: "List the projects of the bound n8n instance (Enterprise Projects feature), restricted to its " +
		"project allow-list when it has one; page by page with an opaque cursor",
	Tags:     []string{"n8n", "projects", "list", "automation"},
	Risk:     n8nReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"projects":{"type":"array","items":` + projectSummarySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["projects","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Projects per page, 1 to 250; 100 when omitted, matching n8n's own default"},
	},
	Fields: append(append([]capability.Field{}, projectSummaryFields...),
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, even when this page is empty after filtering"},
		capability.Field{Name: "count", Description: "Number of projects on this page after this connection's project allow-list was applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of reachable projects", Arguments: json.RawMessage(`{}`)}},
}

var projectsCreate = capability.Descriptor{
	ID:      Provider + ".projects.create",
	Version: 1,
	Title:   "Create an n8n project",
	Description: "Create one team project of the bound n8n instance from its name; a repeated call creates a " +
		"second project. Refused on a connection that restricts projects or workflows by an allow-list",
	Tags:     []string{"n8n", "projects", "create", "automation"},
	Risk:     n8nChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + projectNameSchemaJSON + `},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"}},` +
		`"required":["id","name","type"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectNameArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the created project"},
		{Name: "name", Description: "Name of the created project, as n8n reports it, untrusted data"},
		{Name: "type", Description: "Project type, as n8n reports it"},
	},
	Examples: []capability.Example{{Description: "Create a project", Arguments: json.RawMessage(`{"name":"Marketing"}`)}},
}

var projectsUpdate = capability.Descriptor{
	ID:      Provider + ".projects.update",
	Version: 1,
	Title:   "Rename an n8n project",
	Description: "Change the name of one project of the bound n8n instance; the only field n8n's own " +
		"updateProject endpoint accepts. Only projects inside the project allow-list, so a connection " +
		"without one refuses it, as does one that restricts workflows by an allow-list",
	Tags:     []string{"n8n", "projects", "update", "automation"},
	Risk:     n8nChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"name":` + projectNameSchemaJSON + `},"required":["project_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["id","name","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, projectNameArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the renamed project"},
		{Name: "name", Description: "The name that was sent"},
		{Name: "updated", Description: "True when n8n accepted the change"},
	},
	Examples: []capability.Example{{Description: "Rename a project",
		Arguments: json.RawMessage(`{"project_id":"VmwOO9HeTEj20kxM","name":"Marketing"}`)}},
}

var projectsDelete = capability.Descriptor{
	ID:      Provider + ".projects.delete",
	Version: 1,
	Title:   "Delete an n8n project",
	Description: "Delete one team project of the bound n8n instance together with every workflow, credential, " +
		"and data table it owns: n8n's Public API offers no transfer, so nothing is moved elsewhere, and the " +
		"deletion cannot be undone. n8n deletes step by step, not in one transaction, so a failure part-way may " +
		"leave the project partly emptied. Only projects inside the project allow-list, so a connection " +
		"without one refuses it, as does one that restricts workflows by an allow-list",
	Tags:     []string{"n8n", "projects", "delete", "automation"},
	Risk:     n8nChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `},` +
		`"required":["project_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted project"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a project and everything it owns",
		Arguments: json.RawMessage(`{"project_id":"VmwOO9HeTEj20kxM"}`)}},
}

// projectJSON mirrors n8n's projectPublicDto.
type projectJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Icon      *struct {
		Type  string `json:"type"`
		Value string `json:"value"`
		Color string `json:"color"`
	} `json:"icon"`
	Description         string `json:"description"`
	CustomTelemetryTags []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"customTelemetryTags"`
	CreatorID string `json:"creatorId"`
}

type projectsPageJSON struct {
	Data       []projectJSON `json:"data"`
	NextCursor string        `json:"nextCursor"`
}

// ProjectIcon is the icon of a project. Its texts are untrusted data.
type ProjectIcon struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
	Color string `json:"color,omitempty"`
}

// ProjectTelemetryTag is one custom telemetry tag of a project. Both texts are untrusted data.
type ProjectTelemetryTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ProjectSummary is the stable Qatlas view of one project, shared by projects.list and the project of a
// variable. The change tools report their own types. Description, icon, tags, and creator are untrusted.
type ProjectSummary struct {
	ID                  string                `json:"id"`
	Name                string                `json:"name"`
	Type                string                `json:"type"`
	CreatedAt           string                `json:"created_at"`
	UpdatedAt           string                `json:"updated_at"`
	Icon                *ProjectIcon          `json:"icon,omitempty"`
	Description         string                `json:"description,omitempty"`
	CustomTelemetryTags []ProjectTelemetryTag `json:"custom_telemetry_tags,omitempty"`
	CreatorID           string                `json:"creator_id,omitempty"`
}

// summarizeProject is the one place that maps n8n's projectPublicDto to the Qatlas view. Missing and empty
// values stay out; foreign texts lose control characters and are capped in length and number.
func summarizeProject(p projectJSON) ProjectSummary {
	out := ProjectSummary{ID: bounded(p.ID), Name: bounded(p.Name), Type: bounded(p.Type),
		CreatedAt: bounded(p.CreatedAt), UpdatedAt: bounded(p.UpdatedAt),
		Description: cappedText(p.Description), CreatorID: cappedText(p.CreatorID)}
	if p.Icon != nil {
		icon := ProjectIcon{Type: cappedText(p.Icon.Type), Value: cappedText(p.Icon.Value),
			Color: cappedText(p.Icon.Color)}
		if icon != (ProjectIcon{}) {
			out.Icon = &icon
		}
	}
	for _, tag := range p.CustomTelemetryTags {
		if len(out.CustomTelemetryTags) == maxProjectTelemetryTags {
			break
		}
		if key := cappedText(tag.Key); key != "" {
			out.CustomTelemetryTags = append(out.CustomTelemetryTags,
				ProjectTelemetryTag{Key: key, Value: cappedText(tag.Value)})
		}
	}
	return out
}

// ProjectsPage is one paginated, allow-list-filtered listing of projects.
type ProjectsPage struct {
	Projects []ProjectSummary `json:"projects"`
	Cursor   string           `json:"cursor,omitempty"`
	HasMore  bool             `json:"has_more"`
	Count    int              `json:"count"`
}

// ProjectCreated is what projects.create reports.
type ProjectCreated struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ProjectUpdated is what projects.update reports.
type ProjectUpdated struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Updated bool   `json:"updated"`
}

// ProjectDeleted is what projects.delete reports.
type ProjectDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// projectError replaces the generic 403 message of a projects endpoint with the license, scope, and role
// hint of projectPermissionMessage. Every other failure is returned unchanged.
func projectError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = projectPermissionMessage
	}
	return err
}

// selectProjectMutation decides locally, before any secret is resolved and before any request is sent,
// whether this connection may change the project projectID names. A connection that restricts workflows by
// an allow-list refuses every project change: deleting or renaming a project reaches workflows beyond that
// list. Without a project allow-list no project is on the list, so nothing can be changed or deleted (unlike
// projects.list, which then lists all); a configured one admits only its own entries. The refusal never names the refused project.
func selectProjectMutation(resolved *config.Resolved, projectID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validTargetID(projectID) {
		return invalidRequest("project_id must be a usable n8n identifier")
	}
	if len(bound.workflows) > 0 {
		return invalidRequest("this connection restricts workflows by an allow-list, so it cannot change projects")
	}
	if len(bound.projects) == 0 {
		return invalidRequest("this connection has no project allow-list, so it cannot change or delete projects")
	}
	if !bound.reachesProject(projectID) {
		return invalidRequest("project_id is outside the targets of this connection")
	}
	return nil
}

// validProjectName keeps a project name inside n8n's own limits and free of control characters.
func validProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return invalidRequest("name must not be empty")
	}
	if utf8.RuneCountInString(name) > maxProjectNameLength {
		return invalidRequest("name is longer than " + strconv.Itoa(maxProjectNameLength) + " characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return invalidRequest("name must not contain control characters")
		}
	}
	return nil
}

type projectsListArguments struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

func invokeProjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input projectsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list projects", "the validated arguments could not be read")
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListProjects(ctx, input.Cursor, limit)
	return page, projectError(err)
}

// ListProjects reads one page of GET /projects and keeps only the projects this connection's project
// allow-list admits; n8n's own answer lists every project of the instance.
func (c *Client) ListProjects(ctx context.Context, cursor string, limit int) (*ProjectsPage, error) {
	const op = "list projects"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page projectsPageJSON
	if err := c.get(ctx, op, "/projects", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	summaries := make([]ProjectSummary, 0, len(page.Data))
	for _, p := range page.Data {
		if !c.scope.reachesProject(p.ID) {
			continue
		}
		summaries = append(summaries, summarizeProject(p))
	}
	return &ProjectsPage{Projects: summaries, Cursor: page.NextCursor, HasMore: page.NextCursor != "",
		Count: len(summaries)}, nil
}

type projectsCreateArguments struct {
	Name string `json:"name"`
}

func invokeProjectsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create project"
	var input projectsCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.projects) > 0 || len(bound.workflows) > 0 {
		return nil, invalidRequest("this connection restricts projects or workflows by an allow-list, so it " +
			"cannot create a project: a newly created project can never already be on that list")
	}
	if err := validProjectName(input.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	created, err := client.CreateProject(ctx, input.Name)
	return created, projectError(err)
}

// CreateProject sends the one changing POST /projects request with the name only. n8n's createProject schema
// also accepts a caller-chosen id, icon, description, and telemetry tags; none is offered.
func (c *Client) CreateProject(ctx context.Context, name string) (*ProjectCreated, error) {
	const op = "create project"
	var created projectJSON
	if err := c.change(ctx, op, http.MethodPost, "/projects", nil, map[string]any{"name": name}, &created,
		maxResponseBytes); err != nil {
		return nil, err
	}
	if !validTargetID(created.ID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "n8n did not report a usable ID of the created project" + uncertain}
	}
	return &ProjectCreated{ID: created.ID, Name: bounded(created.Name), Type: bounded(created.Type)}, nil
}

type projectUpdateArguments struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

func invokeProjectsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update project"
	var input projectUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectProjectMutation(resolved, input.ProjectID); err != nil {
		return nil, err
	}
	if err := validProjectName(input.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	updated, err := client.UpdateProject(ctx, input.ProjectID, input.Name)
	return updated, projectError(err)
}

// UpdateProject sends the one changing PUT /projects/{id} request with the name only, the one field n8n's
// updateProject schema accepts. The Public API has no endpoint to read a single project, so the answer is
// not re-read.
func (c *Client) UpdateProject(ctx context.Context, projectID, name string) (*ProjectUpdated, error) {
	if err := c.change(ctx, "update project", http.MethodPut, "/projects/"+url.PathEscape(projectID), nil,
		map[string]any{"name": name}, nil, maxResponseBytes); err != nil {
		return nil, err
	}
	return &ProjectUpdated{ID: projectID, Name: name, Updated: true}, nil
}

type projectDeleteArguments struct {
	ProjectID string `json:"project_id"`
}

func invokeProjectsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete project"
	var input projectDeleteArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectProjectMutation(resolved, input.ProjectID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.refuseOwnedExcludedWorkflows(ctx, op, input.ProjectID); err != nil {
		return nil, err
	}
	deleted, err := client.DeleteProject(ctx, input.ProjectID)
	return deleted, projectError(err)
}

// workflowEditorRole is the role of a workflow that is only shared into a project; every other role, an
// unknown one included, is read as ownership.
const workflowEditorRole = "workflow:editor"

// ownerProjectIDs returns the projects that own the workflow: every share with a role other than editor. An
// owner share without a project ID cannot be placed, so placed is false then, as it is without any owner.
func (w workflowJSON) ownerProjectIDs() (ids []string, placed bool) {
	placed = true
	owner := false
	for _, entry := range w.Shared {
		id := entry.ProjectID
		if id == "" && entry.Project != nil {
			id = entry.Project.ID
		}
		if entry.Role == workflowEditorRole {
			continue
		}
		owner = true
		if id == "" {
			placed = false
			continue
		}
		ids = append(ids, id)
	}
	return ids, placed && owner
}

// refuseOwnedExcludedWorkflows stops the deletion of a reachable project that owns an excluded workflow: the
// deletion removes every workflow the project owns, and this tool has no transfer target to move it to. Each
// excluded workflow is read once. One that n8n does not know cannot be owned; one that cannot be read, or
// reports no owner at all, cannot be placed and refuses the deletion. The refusal names neither workflow.
func (c *Client) refuseOwnedExcludedWorkflows(ctx context.Context, op, projectID string) error {
	for _, workflowID := range c.scope.excludedWorkflows {
		workflow, err := c.fetchWorkflow(ctx, op, workflowID)
		if err != nil {
			if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassNotFound {
				continue
			}
			return err
		}
		owners, placed := workflow.ownerProjectIDs()
		owned := contains(owners, projectID)
		owner := placed
		if owned || !owner {
			return invalidRequest("the project owns objects outside the targets of this connection, so it " +
				"cannot be deleted")
		}
	}
	return nil
}

// DeleteProject sends the one changing DELETE /projects/{id} request, without a query: the public handler
// passes no transfer target to n8n's ProjectService.deleteProject, so workflows, credentials, and data tables
// the project owns are deleted. n8n deletes them one by one before it removes the project, so a rejection
// other than the ones n8n makes before it starts (403, 404) can follow a partial deletion, for example a 409
// on a published workflow; every other provider rejection is therefore reported as uncertain too, like an
// unclear transport result. A 400 cannot be told from such a rejection by its class and is treated the same.
func (c *Client) DeleteProject(ctx context.Context, projectID string) (*ProjectDeleted, error) {
	const op = "delete project"
	if err := c.change(ctx, op, http.MethodDelete, "/projects/"+url.PathEscape(projectID), nil, nil, nil,
		maxResponseBytes); err != nil {
		if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassProviderError &&
			!strings.HasSuffix(failure.Message, uncertain) {
			failure.Message += uncertain
		}
		return nil, err
	}
	return &ProjectDeleted{ID: projectID, Deleted: true}, nil
}
