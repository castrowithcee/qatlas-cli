package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The status update tools read the status updates of a project, newest first, and create, change, and
// delete them. A status update is addressed by its identifier, which is resolved together with the project in
// one query and must belong to it before the one change is sent.

// statusValues maps the statuses of a status update to GitHub's.
var statusValues = map[string]string{"inactive": "INACTIVE", "on_track": "ON_TRACK", "at_risk": "AT_RISK",
	"off_track": "OFF_TRACK", "complete": "COMPLETE"}

// Input and output schemas of the status update tools. The bounds mirror the checks below, which apply them
// again for a direct caller.
const (
	statusSchema         = `{"type":"string","enum":["inactive","on_track","at_risk","off_track","complete"]}`
	statusDateSchema     = `{"type":"string","pattern":"^([0-9]{4}-[0-9]{2}-[0-9]{2})?$"}`
	statusUpdateIDSchema = itemIDSchema
	statusUpdateOutput   = `{"type":"object","properties":{"id":{"type":"string"},"status":{"type":"string"},` +
		`"start_date":{"type":"string"},"target_date":{"type":"string"},"body":{"type":"string"},` +
		`"author":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
		`"required":["id","status","start_date","target_date","body"],"additionalProperties":false}`
	statusUpdateKeys = `"status":` + statusSchema + `,"start_date":` + statusDateSchema + `,` +
		`"target_date":` + statusDateSchema + `,"body":` + bodySchema
	changedStatusOutput = `{"type":"object","properties":{"status_update":` + statusUpdateOutput + `},` +
		`"required":["status_update"],"additionalProperties":false}`
)

var statusUpdateArgument = capability.Argument{Name: "status_update_id", Description: "Identifier of the " +
	"status update, as github.projectstatus.list names it", Required: true}

var statusUpdateArguments = []capability.Argument{
	{Name: "status", Description: "inactive, on_track, at_risk, off_track, or complete"},
	{Name: "start_date", Description: "Start date as YYYY-MM-DD"},
	{Name: "target_date", Description: "Target date as YYYY-MM-DD"},
	{Name: "body", Description: "Body in Markdown, at most 65536 characters; stored as given"},
}

var changedStatusUpdate = capability.Field{Name: "status_update", Description: "The status update after the " +
	"change: id, status, start_date, target_date, body (untrusted data), author, created_at, updated_at"}

var statusList = capability.Descriptor{
	ID:      Provider + ".projectstatus.list",
	Version: 1,
	Title:   "List the status updates of a GitHub project",
	Description: "Read one bounded batch of the status updates of a GitHub project an explicit connection " +
		"allows, newest first: status, start and target date, body, and author",
	Tags:                       []string{"github", "projects", "status", "updates", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,` +
		`"maximum":100},"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"status_updates":{"type":"array","items":` +
		statusUpdateOutput + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["status_updates","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Status updates per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch; the first batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "status_updates", Description: "Status updates, newest first: id, status (inactive, on_track, " +
			"at_risk, off_track, complete, or empty without one), start_date and target_date (YYYY-MM-DD or " +
			"empty), body, author, created_at, updated_at; bodies are untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the project holds older status updates"},
	},
	Examples: []capability.Example{{
		Description: "Read the latest status update",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","limit":1}`),
	}},
}

var statusCreate = capability.Descriptor{
	ID:      Provider + ".projectstatus.create",
	Version: 1,
	Title:   "Post a GitHub project status update",
	Description: "Post one status update with status, start and target date, and body to a GitHub project an " +
		"explicit connection allows; a repeated call posts a second status update",
	Tags:                       []string{"github", "projects", "status", "updates", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + statusUpdateKeys + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(changedStatusOutput),
	Arguments:    statusUpdateArguments,
	Fields:       []capability.Field{changedStatusUpdate},
	Examples: []capability.Example{{
		Description: "Report a project at risk",
		Arguments: json.RawMessage(`{"project":"orgs/octo-org/projects/7","status":"at_risk",` +
			`"target_date":"2026-12-18","body":"The migration slips by one sprint."}`),
	}},
}

var statusUpdate = capability.Descriptor{
	ID:      Provider + ".projectstatus.update",
	Version: 1,
	Title:   "Update a GitHub project status update",
	Description: "Change the status, start or target date, or body of one status update of a GitHub project " +
		"an explicit connection allows; settings left out stay unchanged",
	Tags:                       []string{"github", "projects", "status", "updates", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"status_update_id":` + statusUpdateIDSchema +
		`,` + statusUpdateKeys + `},"required":["status_update_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(changedStatusOutput),
	Arguments: append([]capability.Argument{statusUpdateArgument}, statusUpdateArguments[0],
		capability.Argument{Name: "start_date", Description: "Start date as YYYY-MM-DD; \"\" removes it"},
		capability.Argument{Name: "target_date", Description: "Target date as YYYY-MM-DD; \"\" removes it"},
		capability.Argument{Name: "body", Description: "Body in Markdown, at most 65536 characters; stored as " +
			"given, \"\" empties it"}),
	Fields: []capability.Field{changedStatusUpdate},
	Examples: []capability.Example{{
		Description: "Mark a status update as back on track",
		Arguments:   json.RawMessage(`{"status_update_id":"PVTSU_lADOAAAAAAAAAAAAzgAAAAA","status":"on_track"}`),
	}},
}

var statusDelete = capability.Descriptor{
	ID:      Provider + ".projectstatus.delete",
	Version: 1,
	Title:   "Delete a GitHub project status update",
	Description: "Delete one status update of a GitHub project an explicit connection allows. Offered only by " +
		"a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "status", "updates", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"status_update_id":` + statusUpdateIDSchema +
		`},"required":["status_update_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"status_update_id":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["status_update_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{statusUpdateArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True once GitHub deleted the status update"}},
	Examples: []capability.Example{{
		Description: "Delete a status update",
		Arguments:   json.RawMessage(`{"status_update_id":"PVTSU_lADOAAAAAAAAAAAAzgAAAAA"}`),
	}},
}

// statusOperations are the status update tools.
func statusOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: statusList, Handler: capability.Handler(invokeStatusList)},
		{Descriptor: statusCreate, Handler: capability.Handler(invokeStatusCreate)},
		{Descriptor: statusUpdate, Handler: capability.Handler(invokeStatusUpdate)},
		{Descriptor: statusDelete, Handler: capability.Handler(invokeStatusDelete)},
	}
}

// StatusUpdate is one status update of a project. Status and the dates are empty when it has none.
type StatusUpdate struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	StartDate  string `json:"start_date"`
	TargetDate string `json:"target_date"`
	Body       string `json:"body"`
	Author     string `json:"author,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// StatusUpdateList is one batch of status updates, newest first.
type StatusUpdateList struct {
	StatusUpdates []StatusUpdate `json:"status_updates"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	HasMore       bool           `json:"has_more"`
}

// ChangedStatusUpdate is the answer of a created or updated status update.
type ChangedStatusUpdate struct {
	StatusUpdate StatusUpdate `json:"status_update"`
}

// DeletedStatusUpdate is the answer of a deleted status update.
type DeletedStatusUpdate struct {
	StatusUpdateID string `json:"status_update_id"`
	Deleted        bool   `json:"deleted"`
}

// statusUpdateSelection reads one status update.
const statusUpdateSelection = `id status startDate targetDate body createdAt updatedAt creator{login}`

type statusUpdateJSON struct {
	ID         string  `json:"id"`
	Status     *string `json:"status"`
	StartDate  *string `json:"startDate"`
	TargetDate *string `json:"targetDate"`
	Body       *string `json:"body"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
	Creator    *struct {
		Login string `json:"login"`
	} `json:"creator"`
}

// schema is the Qatlas view of one status update.
func (node statusUpdateJSON) schema() StatusUpdate {
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	out := StatusUpdate{ID: node.ID, Status: strings.ToLower(text(node.Status)), StartDate: text(node.StartDate),
		TargetDate: text(node.TargetDate), Body: text(node.Body), CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt}
	if node.Creator != nil {
		out.Author = node.Creator.Login
	}
	return out
}

// StatusListOptions are the paging of one status update list.
type StatusListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// normalize applies the bounds of one status update list request and returns the GitHub cursor it continues
// after. A cursor is bound to the project.
func (o *StatusListOptions) normalize(bound target) (string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return "", err
	}
	o.Limit = limit
	return decodeCursor(o.binding(bound), o.Cursor)
}

func (o *StatusListOptions) binding(bound target) []byte {
	return fingerprint("statusupdates", strings.ToLower(bound.String()))
}

// StatusSpec is a new status update; a nil setting is left out.
type StatusSpec struct {
	Status     *string `json:"status"`
	StartDate  *string `json:"start_date"`
	TargetDate *string `json:"target_date"`
	Body       *string `json:"body"`
}

// StatusChanges are the settings a status update change writes; a nil setting stays unchanged, and an empty
// date removes it.
type StatusChanges struct {
	ID string `json:"status_update_id"`
	StatusSpec
}

// statusUpdateRef is the argument of a status update delete.
type statusUpdateRef struct {
	ID string `json:"status_update_id"`
}

func checkStatusUpdateID(id string) error {
	if id == "" || len(id) > 200 {
		return invalidRequest("status_update_id is not a status update identifier")
	}
	return nil
}

// check checks the settings of a new status update.
func (spec StatusSpec) check() error { return spec.checkSettings(false) }

// checkSettings checks the settings of a status update. A new one takes no empty date, since it has none to
// remove.
func (spec StatusSpec) checkSettings(update bool) error {
	if spec.Status == nil && spec.StartDate == nil && spec.TargetDate == nil && spec.Body == nil {
		return invalidRequest("name at least one of status, start_date, target_date, or body")
	}
	if spec.Status != nil {
		if _, ok := statusValues[*spec.Status]; !ok {
			return invalidRequest("status must be inactive, on_track, at_risk, off_track, or complete")
		}
	}
	for _, date := range []struct {
		name  string
		value *string
	}{{"start_date", spec.StartDate}, {"target_date", spec.TargetDate}} {
		if date.value == nil || (update && *date.value == "") {
			continue
		}
		if err := checkDate(date.name, *date.value); err != nil {
			return err
		}
	}
	if spec.Body != nil && utf8.RuneCountInString(*spec.Body) > maxBodyLength {
		return invalidRequest(fmt.Sprintf("body must hold at most %d characters", maxBodyLength))
	}
	return nil
}

func (changes StatusChanges) check() error {
	if err := checkStatusUpdateID(changes.ID); err != nil {
		return err
	}
	return changes.StatusSpec.checkSettings(true)
}

func (ref statusUpdateRef) check() error { return checkStatusUpdateID(ref.ID) }

// inputs names the settings a status create or update sends, each as a variable of its GraphQL type. An empty
// date is sent as null, which removes it.
func (spec StatusSpec) inputs() (declarations, inputs []string, variables map[string]any) {
	variables = map[string]any{}
	set := func(name, input, kind string, value any) {
		declarations = append(declarations, "$"+name+":"+kind)
		inputs = append(inputs, input+":$"+name)
		variables[name] = value
	}
	if spec.Status != nil {
		set("status", "status", "ProjectV2StatusUpdateStatus", statusValues[*spec.Status])
	}
	for _, date := range []struct {
		name, input string
		value       *string
	}{{"start", "startDate", spec.StartDate}, {"target", "targetDate", spec.TargetDate}} {
		if date.value == nil {
			continue
		}
		var value any
		if *date.value != "" {
			value = *date.value
		}
		set(date.name, date.input, "Date", value)
	}
	if spec.Body != nil {
		set("body", "body", "String", *spec.Body)
	}
	return declarations, inputs, variables
}

// statusAnswerJSON is the answer of a status create or update.
type statusAnswerJSON struct {
	Status *struct {
		StatusUpdate *statusUpdateJSON `json:"statusUpdate"`
	} `json:"status"`
}

// changed reads the status update a change answered with. The change already happened, so an answer without
// it, or with another one than the changed one, leaves the outcome open.
func (answer statusAnswerJSON) changed(op, id string) (*ChangedStatusUpdate, error) {
	if answer.Status == nil || answer.Status.StatusUpdate == nil || answer.Status.StatusUpdate.ID == "" ||
		(id != "" && answer.Status.StatusUpdate.ID != id) {
		return nil, invalidResponse(op, true)
	}
	return &ChangedStatusUpdate{StatusUpdate: answer.Status.StatusUpdate.schema()}, nil
}

// ListStatusUpdates reads one batch of the status updates of the bound project, newest first.
func (c *Client) ListStatusUpdates(ctx context.Context, options StatusListOptions) (*StatusUpdateList, error) {
	if c.target.kind != kindProject {
		return nil, providerError("list project status updates", "this connection is not bound to a project")
	}
	after, err := options.normalize(c.target)
	if err != nil {
		return nil, err
	}
	return c.listStatusUpdates(ctx, options, after)
}

// listStatusUpdates reads exactly one server page of status updates of the bound project.
func (c *Client) listStatusUpdates(ctx context.Context, options StatusListOptions, after string) (*StatusUpdateList, error) {
	const op = "list project status updates"
	query := `query($owner:String!,$number:Int!,$first:Int!,$after:String){owner:` + c.target.ownerField() +
		`(login:$owner){projectV2(number:$number){statusUpdates(first:$first,after:$after,` +
		`orderBy:{field:CREATED_AT,direction:DESC}){pageInfo{hasNextPage endCursor} nodes{` +
		statusUpdateSelection + `}}}}}`
	variables := c.projectVariables()
	variables["first"], variables["after"] = options.Limit, nil
	if after != "" {
		variables["after"] = after
	}
	var page struct {
		Owner *struct {
			Project *struct {
				StatusUpdates struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []statusUpdateJSON `json:"nodes"`
				} `json:"statusUpdates"`
			} `json:"projectV2"`
		} `json:"owner"`
	}
	if err := c.graphql(ctx, op, query, variables, &page); err != nil {
		return nil, err
	}
	if page.Owner == nil || page.Owner.Project == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	updates := page.Owner.Project.StatusUpdates
	result := &StatusUpdateList{StatusUpdates: make([]StatusUpdate, 0, len(updates.Nodes))}
	for _, node := range updates.Nodes {
		if node.ID == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a status update without a usable identifier"}
		}
		result.StatusUpdates = append(result.StatusUpdates, node.schema())
	}
	if updates.PageInfo.HasNextPage {
		if updates.PageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further status updates without a cursor"}
		}
		result.HasMore, result.NextCursor = true, encodeCursor(options.binding(c.target), updates.PageInfo.EndCursor)
	}
	return result, nil
}

// CreateStatusUpdate posts one status update to the bound project.
func (c *Client) CreateStatusUpdate(ctx context.Context, spec StatusSpec) (*ChangedStatusUpdate, error) {
	const op = "create project status update"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := spec.check(); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{})
	if err != nil {
		return nil, err
	}
	declarations, inputs, variables := spec.inputs()
	variables["project"] = info.id
	document := "mutation(" + strings.Join(append([]string{"$project:ID!"}, declarations...), ",") +
		"){status:createProjectV2StatusUpdate(input:{" +
		strings.Join(append([]string{"projectId:$project"}, inputs...), ",") + "}){statusUpdate{" +
		statusUpdateSelection + "}}}"
	var answer statusAnswerJSON
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return nil, err
	}
	return answer.changed(op, "")
}

// UpdateStatusUpdate changes the named settings of one status update of the bound project in one mutation.
func (c *Client) UpdateStatusUpdate(ctx context.Context, changes StatusChanges) (*ChangedStatusUpdate, error) {
	const op = "update project status update"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := changes.check(); err != nil {
		return nil, err
	}
	if _, _, err := c.resolve(ctx, op, planningRequest{statusUpdate: changes.ID}); err != nil {
		return nil, err
	}
	declarations, inputs, variables := changes.inputs()
	variables["statusUpdate"] = changes.ID
	document := "mutation(" + strings.Join(append([]string{"$statusUpdate:ID!"}, declarations...), ",") +
		"){status:updateProjectV2StatusUpdate(input:{" +
		strings.Join(append([]string{"statusUpdateId:$statusUpdate"}, inputs...), ",") + "}){statusUpdate{" +
		statusUpdateSelection + "}}}"
	var answer statusAnswerJSON
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return nil, err
	}
	return answer.changed(op, changes.ID)
}

const deleteStatusMutation = `mutation($statusUpdate:ID!){status:deleteProjectV2StatusUpdate(` +
	`input:{statusUpdateId:$statusUpdate}){deletedStatusUpdateId}}`

// DeleteStatusUpdate deletes one status update of the bound project. A deleted status update is gone, so a
// repeated delete is refused before any change.
func (c *Client) DeleteStatusUpdate(ctx context.Context, id string) (*DeletedStatusUpdate, error) {
	const op = "delete project status update"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkStatusUpdateID(id); err != nil {
		return nil, err
	}
	if _, _, err := c.resolve(ctx, op, planningRequest{statusUpdate: id}); err != nil {
		return nil, err
	}
	var answer struct {
		Status *struct {
			ID string `json:"deletedStatusUpdateId"`
		} `json:"status"`
	}
	if err := c.mutate(ctx, op, deleteStatusMutation, map[string]any{"statusUpdate": id}, &answer); err != nil {
		return nil, err
	}
	if answer.Status == nil || answer.Status.ID != id {
		return nil, invalidResponse(op, true)
	}
	return &DeletedStatusUpdate{StatusUpdateID: id, Deleted: true}, nil
}

// The handlers check the target and the arguments before a credential is resolved, so a refused request
// never becomes a secret read or a provider call.

func invokeStatusList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options StatusListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list project status updates")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listStatusUpdates(ctx, options, after))
}

var (
	invokeStatusCreate = projectHandler("create project status update",
		func(c *Client, ctx context.Context, spec StatusSpec) (any, error) {
			return c.CreateStatusUpdate(ctx, spec)
		})
	invokeStatusUpdate = projectHandler("update project status update",
		func(c *Client, ctx context.Context, changes StatusChanges) (any, error) {
			return c.UpdateStatusUpdate(ctx, changes)
		})
	invokeStatusDelete = projectHandler("delete project status update",
		func(c *Client, ctx context.Context, ref statusUpdateRef) (any, error) {
			return c.DeleteStatusUpdate(ctx, ref.ID)
		})
)
