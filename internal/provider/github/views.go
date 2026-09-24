package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The view tools read the views of a project and create, change, and delete them. A view is addressed by its
// number in the project, and the fields it shows by their names, resolved against the field model and the
// views of the project in one query before the one change is sent. GitHub sets the name, the layout, the
// filter, and the visible fields of a view; its grouping, sorting, and board columns can be read but not set
// through its API. A new view takes no filter, so a create with a filter sends a second change, and its
// answer reports the new view even when that second change failed.

// Bounds of one view change. A project holds at most 50 fields, and GitHub takes a filter of at most 512
// characters.
const (
	maxViewFilter    = 512
	maxVisibleFields = 50
)

// viewLayouts maps the layouts a view takes to GitHub's.
var viewLayouts = map[string]string{"table": "TABLE_LAYOUT", "board": "BOARD_LAYOUT", "roadmap": "ROADMAP_LAYOUT"}

// Input and output schemas of the view tools. The bounds mirror the checks below, which apply them again for
// a direct caller.
const (
	layoutSchema        = `{"type":"string","enum":["table","board","roadmap"]}`
	viewFilterSchema    = `{"type":"string","maxLength":512}`
	visibleFieldsSchema = `{"type":"array","maxItems":50,"items":` + fieldNameSchema + `}`
	viewOutput          = `{"type":"object","properties":{"number":{"type":"integer"},"name":{"type":"string"},` +
		`"layout":{"type":"string"},"filter":{"type":"string"},"fields":` + stringListSchema + `,` +
		`"group_by":` + stringListSchema + `,"column_by":` + stringListSchema + `,"sort_by":{"type":"array",` +
		`"items":{"type":"object","properties":{"field":{"type":"string"},"direction":{"type":"string"}},` +
		`"required":["field","direction"],"additionalProperties":false}}},` +
		`"required":["number","name","layout","filter","fields","group_by","column_by","sort_by"],` +
		`"additionalProperties":false}`
)

var viewArgument = capability.Argument{Name: "view", Description: "Number of the view in the project, as " +
	"github.projectviews.list names it", Required: true}

var changedView = capability.Field{Name: "view", Description: "The view after the change: number, name, " +
	"layout, filter, visible fields, and the grouping, board columns, and sorting set in GitHub, untrusted data"}

var viewsList = capability.Descriptor{
	ID:      Provider + ".projectviews.list",
	Version: 1,
	Title:   "List the views of a GitHub project",
	Description: "Read the views of one GitHub project an explicit connection allows: number, name, layout, " +
		"filter, the visible fields, and the grouping, board columns, and sorting, which only GitHub itself sets",
	Tags:                       []string{"github", "projects", "views", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"views":{"type":"array","items":` +
		viewOutput + `}},"required":["views"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "views", Description: "Every view in project order, at most 100: number, " +
		"name, layout (table, board, or roadmap), filter (empty without one), fields (the visible fields in the " +
		"view's order, the title included), group_by and column_by (field names), and sort_by (field " +
		"and direction, asc or desc); names and filters are untrusted data"}},
	Examples: []capability.Example{{
		Description: "Read the views of a project",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var viewsCreate = capability.Descriptor{
	ID:      Provider + ".projectviews.create",
	Version: 1,
	Title:   "Create a GitHub project view",
	Description: "Create one table, board, or roadmap view with its visible fields and filter in a GitHub " +
		"project an explicit connection allows; a repeated call creates a second view",
	Tags:                       []string{"github", "projects", "views", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + fieldNameSchema + `,` +
		`"layout":` + layoutSchema + `,"fields":` + visibleFieldsSchema + `,"filter":` + viewFilterSchema + `},` +
		`"required":["name","layout"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"view":` + viewOutput + `,` +
		`"complete":{"type":"boolean"},"error":{"type":"string"}},"required":["view","complete"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Name of the new view, 1 to 100 characters", Required: true},
		{Name: "layout", Description: "table, board, or roadmap", Required: true},
		{Name: "fields", Description: "Names of the fields the view shows, in their order, compared without " +
			"case; GitHub shows the title first in every view; GitHub's default selection when omitted, and " +
			"refused for a roadmap"},
		{Name: "filter", Description: "Filter of the view in GitHub's project filter syntax, such as " +
			"status:Todo label:bug, at most 512 characters; set by a second change after the create"},
	},
	Fields: []capability.Field{changedView,
		{Name: "complete", Description: "False when the view was created but its filter could not be set"},
		{Name: "error", Description: "Why the filter was not set, or may not have been; the view exists either way"}},
	Examples: []capability.Example{{
		Description: "Create a board of the open bugs",
		Arguments: json.RawMessage(`{"name":"Bugs","layout":"board","fields":["Assignees","Priority"],` +
			`"filter":"label:bug -status:Done"}`),
	}},
}

var viewsUpdate = capability.Descriptor{
	ID:      Provider + ".projectviews.update",
	Version: 1,
	Title:   "Update a GitHub project view",
	Description: "Rename one view of a GitHub project an explicit connection allows, or change its layout, " +
		"filter, or visible fields; settings left out stay unchanged",
	Tags:                       []string{"github", "projects", "views", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"view":` + numberSchema + `,` +
		`"name":` + fieldNameSchema + `,"layout":` + layoutSchema + `,"fields":` + visibleFieldsSchema + `,` +
		`"filter":` + viewFilterSchema + `},"required":["view"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"view":` + viewOutput + `},` +
		`"required":["view"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		viewArgument,
		{Name: "name", Description: "New name of the view, 1 to 100 characters"},
		{Name: "layout", Description: "table, board, or roadmap"},
		{Name: "fields", Description: "Names of every field the view shows afterwards, in their order, compared " +
			"without case; GitHub shows the title first in every view, [] leaves only the title, and a roadmap " +
			"takes no fields"},
		{Name: "filter", Description: "New filter in GitHub's project filter syntax, at most 512 characters; " +
			"an empty filter removes it"},
	},
	Fields: []capability.Field{changedView},
	Examples: []capability.Example{{
		Description: "Show the sprint and narrow the view to open items",
		Arguments:   json.RawMessage(`{"view":2,"fields":["Status","Sprint","Assignees"],"filter":"-status:Done"}`),
	}},
}

var viewsDelete = capability.Descriptor{
	ID:      Provider + ".projectviews.delete",
	Version: 1,
	Title:   "Delete a GitHub project view",
	Description: "Delete one view of a GitHub project an explicit connection allows; the items and fields " +
		"of the project stay. Offered only by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "views", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"view":` + numberSchema + `},` +
		`"required":["view"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["number","name","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{viewArgument},
	Fields: []capability.Field{
		{Name: "number", Description: "Number of the deleted view"},
		{Name: "name", Description: "Name of the deleted view, untrusted data"},
		{Name: "deleted", Description: "True once GitHub deleted the view"},
	},
	Examples: []capability.Example{{
		Description: "Delete a view",
		Arguments:   json.RawMessage(`{"view":3}`),
	}},
}

// viewOperations are the view tools.
func viewOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: viewsList, Handler: capability.Handler(invokeViewsList)},
		{Descriptor: viewsCreate, Handler: capability.Handler(invokeViewsCreate)},
		{Descriptor: viewsUpdate, Handler: capability.Handler(invokeViewsUpdate)},
		{Descriptor: viewsDelete, Handler: capability.Handler(invokeViewsDelete)},
	}
}

// ProjectView is one view of a project as the view tools show it.
type ProjectView struct {
	Number   int        `json:"number"`
	Name     string     `json:"name"`
	Layout   string     `json:"layout"`
	Filter   string     `json:"filter"`
	Fields   []string   `json:"fields"`
	GroupBy  []string   `json:"group_by"`
	ColumnBy []string   `json:"column_by"`
	SortBy   []ViewSort `json:"sort_by"`
}

// ViewSort is one sort field of a view.
type ViewSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// ViewList is the answer of the view list.
type ViewList struct {
	Views []ProjectView `json:"views"`
}

// ChangedView is the answer of a view update.
type ChangedView struct {
	View ProjectView `json:"view"`
}

// CreatedView is the answer of a view create. Once the view exists it is reported, so a caller never creates
// it a second time to learn the outcome; Complete is false when its filter could not be set.
type CreatedView struct {
	View     ProjectView `json:"view"`
	Complete bool        `json:"complete"`
	Error    string      `json:"error,omitempty"`
}

// DeletedView is the answer of a deleted view.
type DeletedView struct {
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// The selection of one view. A visible, grouping, or sorting field is named; the union of field types is
// read through the members every field has.
const (
	viewFieldNames = `nodes{... on ProjectV2FieldCommon{id name}}`
	viewSelection  = `id number name layout filter configuration{visibleFields(first:100){` + viewFieldNames + `}} ` +
		`groupByFields(first:20){` + viewFieldNames + `} verticalGroupByFields(first:20){` + viewFieldNames + `} ` +
		`sortByFields(first:20){nodes{direction field{... on ProjectV2FieldCommon{id name}}}}`
	viewsSelection = `views(first:100){nodes{` + viewSelection + `}}`
)

type namedFieldsJSON struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

func (fields namedFieldsJSON) names() []string {
	names := []string{}
	for _, node := range fields.Nodes {
		if node.Name != "" {
			names = append(names, node.Name)
		}
	}
	return names
}

type viewJSON struct {
	ID     string  `json:"id"`
	Number int     `json:"number"`
	Name   string  `json:"name"`
	Layout string  `json:"layout"`
	Filter *string `json:"filter"`
	Config struct {
		Fields namedFieldsJSON `json:"visibleFields"`
	} `json:"configuration"`
	GroupBy  namedFieldsJSON `json:"groupByFields"`
	ColumnBy namedFieldsJSON `json:"verticalGroupByFields"`
	SortBy   struct {
		Nodes []struct {
			Direction string `json:"direction"`
			Field     struct {
				Name string `json:"name"`
			} `json:"field"`
		} `json:"nodes"`
	} `json:"sortByFields"`
}

// schema is the Qatlas view of one view.
func (view viewJSON) schema() ProjectView {
	out := ProjectView{Number: view.Number, Name: view.Name,
		Layout: strings.ToLower(strings.TrimSuffix(view.Layout, "_LAYOUT")), Fields: view.Config.Fields.names(),
		GroupBy: view.GroupBy.names(), ColumnBy: view.ColumnBy.names(), SortBy: []ViewSort{}}
	if view.Filter != nil {
		out.Filter = *view.Filter
	}
	for _, node := range view.SortBy.Nodes {
		if node.Field.Name != "" {
			out.SortBy = append(out.SortBy, ViewSort{Field: node.Field.Name, Direction: strings.ToLower(node.Direction)})
		}
	}
	return out
}

// view finds the view of one number, or names every view number of the project in its refusal.
func (info *projectInfo) view(number int) (viewJSON, error) {
	numbers := make([]string, len(info.views))
	for i, view := range info.views {
		if view.Number == number && view.ID != "" {
			return view, nil
		}
		numbers[i] = strconv.Itoa(view.Number)
	}
	return viewJSON{}, invalidRequest(fmt.Sprintf("view %d is not a view of this project; its views are %s",
		number, strings.Join(numbers, ", ")))
}

// visibleFields resolves the names of the fields a view shows to their identifiers, in the given order.
func (info *projectInfo) visibleFields(names []string) ([]string, error) {
	ids := make([]string, len(names))
	for i, name := range names {
		field, err := info.named("a name in fields", name)
		if err != nil {
			return nil, err
		}
		ids[i] = field.ID
	}
	return ids, nil
}

// ViewSpec is a new view; nil fields take GitHub's default selection.
type ViewSpec struct {
	Name   string    `json:"name"`
	Layout string    `json:"layout"`
	Fields *[]string `json:"fields"`
	Filter *string   `json:"filter"`
}

// ViewChanges are the settings a view update writes; a nil setting stays unchanged.
type ViewChanges struct {
	View   int       `json:"view"`
	Name   *string   `json:"name"`
	Layout *string   `json:"layout"`
	Fields *[]string `json:"fields"`
	Filter *string   `json:"filter"`
}

// viewNumber is the argument of a view delete.
type viewNumber struct {
	View int `json:"view"`
}

func checkView(number int) error {
	if number < 1 || number > 1000000000 {
		return invalidRequest("view must be the number of a view of the project")
	}
	return nil
}

func checkLayout(layout string) error {
	if _, ok := viewLayouts[layout]; !ok {
		return invalidRequest("layout must be table, board, or roadmap")
	}
	return nil
}

// checkFilter keeps a filter one line of text. Its grammar is GitHub's, which judges it.
func checkFilter(filter string) error {
	if utf8.RuneCountInString(filter) > maxViewFilter || strings.IndexFunc(filter, unicode.IsControl) >= 0 {
		return invalidRequest(fmt.Sprintf("filter must be at most %d characters without control characters",
			maxViewFilter))
	}
	return nil
}

// checkVisibleFields checks the fields a view of a layout shows. GitHub refuses visible fields for a roadmap.
func checkVisibleFields(layout string, fields *[]string) error {
	if fields == nil {
		return nil
	}
	if layout == "roadmap" {
		return invalidRequest("a roadmap view takes no fields; GitHub refuses visible fields for a roadmap")
	}
	return checkNames("fields", *fields, maxVisibleFields)
}

func (spec ViewSpec) check() error {
	if err := checkName("name", spec.Name); err != nil {
		return err
	}
	if err := checkLayout(spec.Layout); err != nil {
		return err
	}
	if spec.Filter != nil {
		if err := checkFilter(*spec.Filter); err != nil {
			return err
		}
	}
	return checkVisibleFields(spec.Layout, spec.Fields)
}

func (changes ViewChanges) check() error {
	if err := checkView(changes.View); err != nil {
		return err
	}
	if changes.Name == nil && changes.Layout == nil && changes.Fields == nil && changes.Filter == nil {
		return invalidRequest("name at least one of name, layout, fields, or filter to change")
	}
	if changes.Name != nil {
		if err := checkName("name", *changes.Name); err != nil {
			return err
		}
	}
	if changes.Layout != nil {
		if err := checkLayout(*changes.Layout); err != nil {
			return err
		}
	}
	if changes.Filter != nil {
		if err := checkFilter(*changes.Filter); err != nil {
			return err
		}
	}
	layout := ""
	if changes.Layout != nil {
		layout = *changes.Layout
	}
	return checkVisibleFields(layout, changes.Fields)
}

func (number viewNumber) check() error { return checkView(number.View) }

// The mutations of the views. Every identifier and value travels as a variable; an update names only the
// settings it changes.
const (
	viewAnswerSelection = `projectV2View{` + viewSelection + `}`
	createViewMutation  = `mutation($project:ID!,$name:String!,$layout:ProjectV2ViewLayout!,` +
		`$configuration:ProjectV2ViewConfigurationInput){view:createProjectV2View(input:{projectId:$project,` +
		`name:$name,layout:$layout,configuration:$configuration}){` + viewAnswerSelection + `}}`
	deleteViewMutation = `mutation($view:ID!){view:deleteProjectV2View(input:{viewId:$view}){clientMutationId}}`
)

type viewAnswerJSON struct {
	View *struct {
		View *viewJSON `json:"projectV2View"`
	} `json:"view"`
}

// changed reads the view a change answered with. The change already happened, so an answer without it, or
// with another view than the changed one, leaves the outcome open.
func (answer viewAnswerJSON) changed(op, id string) (viewJSON, error) {
	if answer.View == nil || answer.View.View == nil || answer.View.View.ID == "" || answer.View.View.Number < 1 ||
		(id != "" && answer.View.View.ID != id) {
		return viewJSON{}, invalidResponse(op, true)
	}
	return *answer.View.View, nil
}

// ListViews reads the views of the bound project.
func (c *Client) ListViews(ctx context.Context) (*ViewList, error) {
	const op = "list project views"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{views: true})
	if err != nil {
		return nil, err
	}
	list := &ViewList{Views: make([]ProjectView, len(info.views))}
	for i, view := range info.views {
		list.Views[i] = view.schema()
	}
	return list, nil
}

// updateView sends one updateProjectV2View with the settings it names and reads the view it answers.
func (c *Client) updateView(ctx context.Context, op, id string, settings map[string]any) (viewJSON, error) {
	declarations, inputs := []string{"$view:ID!"}, []string{"viewId:$view"}
	variables := map[string]any{"view": id}
	for _, setting := range []struct{ name, kind string }{
		{"name", "String!"},
		{"layout", "ProjectV2ViewLayout!"},
		{"filter", "String!"},
		{"configuration", "ProjectV2ViewConfigurationInput!"},
	} {
		if value, ok := settings[setting.name]; ok {
			declarations = append(declarations, "$"+setting.name+":"+setting.kind)
			inputs = append(inputs, setting.name+":$"+setting.name)
			variables[setting.name] = value
		}
	}
	document := "mutation(" + strings.Join(declarations, ",") + "){view:updateProjectV2View(input:{" +
		strings.Join(inputs, ",") + "}){" + viewAnswerSelection + "}}"
	var answer viewAnswerJSON
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return viewJSON{}, err
	}
	return answer.changed(op, id)
}

// CreateView creates one view in the bound project. GitHub takes no filter with a new view, so a filter is
// set by a second change; once the view exists, the answer reports it whatever that change's outcome.
func (c *Client) CreateView(ctx context.Context, spec ViewSpec) (*CreatedView, error) {
	const op = "create project view"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := spec.check(); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: spec.Fields != nil})
	if err != nil {
		return nil, err
	}
	variables := map[string]any{"project": info.id, "name": spec.Name, "layout": viewLayouts[spec.Layout],
		"configuration": nil}
	if spec.Fields != nil {
		ids, err := info.visibleFields(*spec.Fields)
		if err != nil {
			return nil, err
		}
		variables["configuration"] = map[string]any{"visibleFieldIds": ids}
	}
	var answer viewAnswerJSON
	if err := c.mutate(ctx, op, createViewMutation, variables, &answer); err != nil {
		return nil, err
	}
	created, err := answer.changed(op, "")
	if err != nil {
		return nil, err
	}
	result := &CreatedView{View: created.schema(), Complete: true}
	if spec.Filter == nil || *spec.Filter == "" {
		return result, nil
	}
	filtered, err := c.updateView(ctx, op, created.ID, map[string]any{"filter": *spec.Filter})
	if err != nil {
		result.Complete, result.Error = false, "the view was created, but its filter was not set: "+messageOf(err)
		return result, nil
	}
	result.View = filtered.schema()
	return result, nil
}

// UpdateView changes the named settings of one view of the bound project in one mutation.
func (c *Client) UpdateView(ctx context.Context, changes ViewChanges) (*ChangedView, error) {
	const op = "update project view"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := changes.check(); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: changes.Fields != nil, views: true})
	if err != nil {
		return nil, err
	}
	view, err := info.view(changes.View)
	if err != nil {
		return nil, err
	}
	if changes.Layout == nil {
		if err := checkVisibleFields(view.schema().Layout, changes.Fields); err != nil {
			return nil, err
		}
	}
	settings := map[string]any{}
	if changes.Name != nil {
		settings["name"] = *changes.Name
	}
	if changes.Layout != nil {
		settings["layout"] = viewLayouts[*changes.Layout]
	}
	if changes.Filter != nil {
		settings["filter"] = *changes.Filter
	}
	if changes.Fields != nil {
		ids, err := info.visibleFields(*changes.Fields)
		if err != nil {
			return nil, err
		}
		settings["configuration"] = map[string]any{"visibleFieldIds": ids}
	}
	changed, err := c.updateView(ctx, op, view.ID, settings)
	if err != nil {
		return nil, err
	}
	return &ChangedView{View: changed.schema()}, nil
}

// DeleteView deletes one view of the bound project.
func (c *Client) DeleteView(ctx context.Context, number int) (*DeletedView, error) {
	const op = "delete project view"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkView(number); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{views: true})
	if err != nil {
		return nil, err
	}
	view, err := info.view(number)
	if err != nil {
		return nil, err
	}
	if len(info.views) == 1 {
		return nil, invalidRequest("view " + strconv.Itoa(number) + " is the last view of this project, which " +
			"GitHub keeps")
	}
	var answer struct {
		View *json.RawMessage `json:"view"`
	}
	if err := c.mutate(ctx, op, deleteViewMutation, map[string]any{"view": view.ID}, &answer); err != nil {
		return nil, err
	}
	if answer.View == nil {
		return nil, invalidResponse(op, true)
	}
	return &DeletedView{Number: view.Number, Name: view.Name, Deleted: true}, nil
}

// The handlers check the target and the arguments before a credential is resolved, so a refused request
// never becomes a secret read or a provider call.

func invokeViewsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.ListViews(ctx))
}

var (
	invokeViewsCreate = projectHandler("create project view",
		func(c *Client, ctx context.Context, spec ViewSpec) (any, error) { return c.CreateView(ctx, spec) })
	invokeViewsUpdate = projectHandler("update project view",
		func(c *Client, ctx context.Context, changes ViewChanges) (any, error) {
			return c.UpdateView(ctx, changes)
		})
	invokeViewsDelete = projectHandler("delete project view",
		func(c *Client, ctx context.Context, number viewNumber) (any, error) {
			return c.DeleteView(ctx, number.View)
		})
)
