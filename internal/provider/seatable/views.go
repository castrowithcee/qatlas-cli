package seatable

import (
	"context"
	"encoding/json"
	"errors"
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

// viewsPath is the view route of the API gateway. The table and the view are addressed by name; Qatlas
// resolves both from the base metadata, never from a free URL or method.
const viewsPath = "/views/"

// The bounds of the view operations.
const (
	maxViews       = 200
	maxViewFilters = 10
	maxViewSorts   = 3
	maxViewHidden  = 100
	maxViewText    = 4096

	// viewUncertain is appended to a failure of a view change whose request may have reached SeaTable.
	// Qatlas never repeats such a request by itself.
	viewUncertain = "; this change may have taken effect, read the view before repeating it"
)

const (
	viewRefSchema  = `{"type":"string","minLength":1,"maxLength":260}`
	viewNameSchema = `{"type":"string","minLength":1,"maxLength":255}`
	viewColSchema  = `{"type":"string","minLength":1,"maxLength":255}`

	viewFiltersSchema = `{"type":"array","maxItems":10,"items":{"type":"object","properties":{` +
		`"column":` + viewColSchema + `,"predicate":{"type":"string","pattern":"^[a-z_]{1,40}$"},` +
		`"term":{"type":["string","number","boolean"]}},"required":["column","predicate"],"additionalProperties":false}}`
	viewSortsSchema = `{"type":"array","maxItems":3,"items":{"type":"object","properties":{` +
		`"column":` + viewColSchema + `,"direction":{"type":"string","enum":["asc","desc"]}},` +
		`"required":["column"],"additionalProperties":false}}`
	viewHiddenSchema = `{"type":"array","maxItems":100,"items":` + viewColSchema + `}`

	viewObjectSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"type":{"type":"string"},"filter_conjunction":{"type":"string"},` +
		`"filters":{"type":"array","items":{"type":"object","properties":{"column":{"type":"string"},` +
		`"predicate":{"type":"string"},"term":{}},"required":["column"],"additionalProperties":false}},` +
		`"sorts":{"type":"array","items":{"type":"object","properties":{"column":{"type":"string"},` +
		`"direction":{"type":"string"}},"required":["column"],"additionalProperties":false}},` +
		`"hidden_columns":{"type":"array","items":{"type":"string"}}},"required":["name"],"additionalProperties":false}`
)

var viewsRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
}

var (
	viewTableArg = capability.Argument{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"}
	viewRefArg   = capability.Argument{Name: "view", Description: "Name of a view of the table, or id:VIEWID, as returned by seatable.views.list", Required: true}
)

var viewsList = capability.Descriptor{
	ID: Provider + ".views.list", Version: 1, Title: "List SeaTable views",
	Description: "List the views of a table allowed by the connection with their filters, sorts and hidden " +
		"columns, referring to columns by key",
	Tags: []string{"seatable", "base", "views", "list", "table"}, Risk: viewsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table":` + tableSelectionSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"table":{"type":"string"},` +
		`"views":{"type":"array","items":` + viewObjectSchema + `},"truncated":{"type":"boolean"}},` +
		`"required":["table","views","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{viewTableArg},
	Fields: []capability.Field{
		{Name: "table", Description: "The selected table reference"},
		{Name: "views", Description: "The views with identifier, name, type, filters, sorts and hidden column keys, untrusted data"},
		{Name: "truncated", Description: "True when the table holds more views than one answer carries"},
	},
	Examples: []capability.Example{{Description: "List the views of the only configured table", Arguments: json.RawMessage(`{}`)}},
}

var viewsGet = capability.Descriptor{
	ID: Provider + ".views.get", Version: 1, Title: "Get a SeaTable view",
	Description: "Read one view of a table allowed by the connection with its filters, sorts and hidden columns",
	Tags:        []string{"seatable", "base", "views", "get", "table"}, Risk: viewsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table":` + tableSelectionSchema +
		`,"view":` + viewRefSchema + `},"required":["view"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"table":{"type":"string"},"view":` +
		viewObjectSchema + `},"required":["table","view"],"additionalProperties":false}`),
	Arguments: []capability.Argument{viewTableArg, viewRefArg},
	Fields: []capability.Field{
		{Name: "table", Description: "The selected table reference"},
		{Name: "view", Description: "The view with identifier, name, type, filters, sorts and hidden column keys, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Read one view by name", Arguments: json.RawMessage(`{"view":"Aktive"}`)}},
}

var viewsCreate = viewMutationDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent, false,
	"Create an empty view with a name in a table allowed by the connection; set filters, sorts and hidden columns with update",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"name":`+viewNameSchema+
		`},"required":["name"],"additionalProperties":false}`,
	`{"type":"object","properties":{"created":{"type":"boolean"}},"required":["created"],"additionalProperties":false}`,
	[]capability.Argument{viewTableArg, {Name: "name", Description: "Name of the new view", Required: true}},
	json.RawMessage(`{"name":"Offene Posten"}`))

var viewsUpdate = viewMutationDescriptor("update", capability.EffectUpdate, capability.IdempotencyIdempotent, false,
	"Rename a view or replace its filters, sorts, filter conjunction or hidden columns; a view that is a "+
		"connection target cannot be changed",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"view":`+viewRefSchema+`,"name":`+viewNameSchema+
		`,"filters":`+viewFiltersSchema+`,"filter_conjunction":{"type":"string","enum":["and","or"]},`+
		`"sorts":`+viewSortsSchema+`,"hidden_columns":`+viewHiddenSchema+`},"required":["view"],"additionalProperties":false}`,
	`{"type":"object","properties":{"updated":{"type":"boolean"}},"required":["updated"],"additionalProperties":false}`,
	[]capability.Argument{viewTableArg, viewRefArg,
		{Name: "name", Description: "New name of the view"},
		{Name: "filters", Description: "Replaces the filters: up to 10 of column (name or key of the table), predicate (lowercase SeaTable filter predicate) and term"},
		{Name: "filter_conjunction", Description: "and or or"},
		{Name: "sorts", Description: "Replaces the sorts: up to 3 of column and direction asc (default) or desc"},
		{Name: "hidden_columns", Description: "Replaces the hidden columns: up to 100 names or keys of columns of the table"}},
	json.RawMessage(`{"view":"Aktive","hidden_columns":["Notiz"]}`))

var viewsDelete = viewMutationDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent, true,
	"Delete a view of a table allowed by the connection; a view that is a connection target cannot be deleted",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"view":`+viewRefSchema+
		`},"required":["view"],"additionalProperties":false}`,
	`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`,
	[]capability.Argument{viewTableArg, viewRefArg},
	json.RawMessage(`{"view":"Offene Posten"}`))

func viewMutationDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency,
	allowList bool, what, input, output string, args []capability.Argument, example json.RawMessage) capability.Descriptor {
	done := action + "d"
	return capability.Descriptor{
		ID: Provider + ".views." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a SeaTable view",
		Description: what, Tags: []string{"seatable", "base", "views", action, "table"},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		Arguments: args,
		Fields:    []capability.Field{{Name: done, Description: "True when SeaTable accepted the change"}},
		Examples:  []capability.Example{{Description: what, Arguments: example}},
	}
}

// ViewFilterInput is one filter of a view change. Term is one scalar.
type ViewFilterInput struct {
	Column    string          `json:"column"`
	Predicate string          `json:"predicate"`
	Term      json.RawMessage `json:"term,omitempty"`
}

// ViewSortInput is one sort key of a view change.
type ViewSortInput struct {
	Column    string `json:"column"`
	Direction string `json:"direction,omitempty"`
}

// ViewInput carries the arguments of all five view tools. A nil list leaves that part of the view as it is.
type ViewInput struct {
	Table             string             `json:"table"`
	View              string             `json:"view"`
	Name              string             `json:"name"`
	Filters           *[]ViewFilterInput `json:"filters"`
	FilterConjunction string             `json:"filter_conjunction"`
	Sorts             *[]ViewSortInput   `json:"sorts"`
	HiddenColumns     *[]string          `json:"hidden_columns"`
}

// openForView decodes the arguments and settles the connection's own table boundary and the shape of the
// request before the credential is resolved.
func openForView(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, kind string) (*Client, ViewInput, error) {
	var input ViewInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, input, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(input.Table); err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if err := checkViewRequest(op, kind, input); err != nil {
		return nil, input, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

func invokeViewsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForView(ctx, "list views", resolved, secrets, red, raw, "list")
	if err != nil {
		return nil, err
	}
	return client.ListViews(ctx, input)
}

func invokeViewsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForView(ctx, "get view", resolved, secrets, red, raw, "get")
	if err != nil {
		return nil, err
	}
	return client.GetView(ctx, input)
}

func invokeViewsChange(op, kind string, change func(*Client, context.Context, string, ViewInput) error,
	done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		client, input, err := openForView(ctx, op, resolved, secrets, red, raw, kind)
		if err != nil {
			return nil, err
		}
		if err := change(client, ctx, op, input); err != nil {
			return nil, err
		}
		return map[string]bool{done: true}, nil
	}
}

// checkViewRequest validates the shape of a request without any I/O, so a malformed request is refused
// before the credential is resolved.
func checkViewRequest(op, kind string, input ViewInput) error {
	switch kind {
	case "get", "delete":
		if _, _, err := parseReference(input.View); err != nil {
			return providerError(op, "the selected SeaTable view is unusable")
		}
	case "create":
		if !validName(input.Name) || strings.Contains(input.Name, "/") {
			return providerError(op, "a view name has 1 to 255 printable characters without '/'")
		}
	case "update":
		if _, _, err := parseReference(input.View); err != nil {
			return providerError(op, "the selected SeaTable view is unusable")
		}
		if input.Name == "" && input.Filters == nil && input.FilterConjunction == "" && input.Sorts == nil &&
			input.HiddenColumns == nil {
			return providerError(op, "a view update needs at least one change")
		}
		if input.Name != "" && (!validName(input.Name) || strings.Contains(input.Name, "/")) {
			return providerError(op, "a view name has 1 to 255 printable characters without '/'")
		}
		if err := checkViewShape(input); err != nil {
			return providerError(op, err.Error())
		}
	}
	return nil
}

// viewScope is the table a view operation works on, resolved from the base metadata.
type viewScope struct {
	selected target
	table    tableJSON
	access   *baseAccess
}

// resolveViewTable finds the selected table in the base metadata. The token exchange and the metadata read
// are the only provider I/O that precedes a refusal based on the metadata.
func (c *Client) resolveViewTable(ctx context.Context, op, table string) (*viewScope, error) {
	selected, err := c.scope.selectTarget(table)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, err
	}
	document, err := c.metadata(ctx, op)
	if err != nil {
		return nil, err
	}
	for _, candidate := range document.Metadata.Tables {
		if matchesTable(selected, candidate) {
			if !validName(candidate.Name) {
				return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
					Message: "SeaTable returned a table without a usable name"}
			}
			return &viewScope{selected: selected, table: candidate, access: access}, nil
		}
	}
	return nil, providerError(op, "the selected SeaTable table no longer exists")
}

// find returns the identifier and name of the view a reference names in this table.
func (s *viewScope) find(op, reference string) (id, name string, err error) {
	ref, byID, err := parseReference(reference)
	if err != nil {
		return "", "", providerError(op, "the selected SeaTable view is unusable")
	}
	for _, view := range s.table.Views {
		if (byID && view.ID == ref) || (!byID && view.Name == ref) {
			return view.ID, view.Name, nil
		}
	}
	return "", "", providerError(op, "the selected SeaTable table has no view with this name or identifier")
}

// inBinding reports whether a view lies inside the view a selected target narrows to.
func (s *viewScope) inBinding(id, name string) bool {
	return s.selected.view == "" || (s.selected.viewParam == "view_id" && s.selected.view == id) ||
		(s.selected.viewParam == "view_name" && s.selected.view == name)
}

// isTarget reports whether a view is configured as a connection target of this table.
func (c *Client) isTarget(s *viewScope, id, name string) bool {
	for _, configured := range c.scope.targets {
		if configured.view == "" || !matchesTable(configured, s.table) {
			continue
		}
		if (configured.viewParam == "view_id" && configured.view == id) ||
			(configured.viewParam == "view_name" && configured.view == name) {
			return true
		}
	}
	return false
}

func (s *viewScope) viewPath(name string) string {
	return gatewayPath + url.PathEscape(s.access.uuid) + viewsPath + url.PathEscape(name) + "/"
}

func (s *viewScope) query() url.Values { return url.Values{"table_name": {s.table.Name}} }

// View is one view of a table. Filter terms and names are untrusted provider content.
type View struct {
	ID                string       `json:"id,omitempty"`
	Name              string       `json:"name"`
	Type              string       `json:"type,omitempty"`
	FilterConjunction string       `json:"filter_conjunction,omitempty"`
	Filters           []ViewFilter `json:"filters,omitempty"`
	Sorts             []ViewSort   `json:"sorts,omitempty"`
	HiddenColumns     []string     `json:"hidden_columns,omitempty"`
}

// ViewFilter is one filter of a view, its column named by key.
type ViewFilter struct {
	Column    string          `json:"column"`
	Predicate string          `json:"predicate,omitempty"`
	Term      json.RawMessage `json:"term,omitempty"`
}

// ViewSort is one sort key of a view, its column named by key.
type ViewSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction,omitempty"`
}

// ViewsResult is the normalised answer of views.list.
type ViewsResult struct {
	Table     string `json:"table"`
	Views     []View `json:"views"`
	Truncated bool   `json:"truncated"`
}

// ViewResult is the normalised answer of views.get.
type ViewResult struct {
	Table string `json:"table"`
	View  View   `json:"view"`
}

type viewWire struct {
	ID                string          `json:"_id"`
	Name              string          `json:"name"`
	Type              string          `json:"type"`
	FilterConjunction string          `json:"filter_conjunction"`
	Filters           json.RawMessage `json:"filters"`
	Sorts             json.RawMessage `json:"sorts"`
	HiddenColumns     json.RawMessage `json:"hidden_columns"`
}

func shortText(value string) string {
	if len(value) > maxViewText {
		return ""
	}
	return value
}

// normalizeView keeps the fields of the stable envelope within their bounds and drops everything else.
func normalizeView(op string, wire viewWire) (View, error) {
	if wire.Name == "" || len(wire.Name) > maxViewText {
		return View{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned a view without a usable name"}
	}
	view := View{ID: shortText(wire.ID), Name: wire.Name, Type: shortText(wire.Type),
		FilterConjunction: shortText(wire.FilterConjunction)}
	var filters []struct {
		ColumnKey string          `json:"column_key"`
		Predicate string          `json:"filter_predicate"`
		Term      json.RawMessage `json:"filter_term"`
	}
	if json.Unmarshal(wire.Filters, &filters) == nil {
		for i, filter := range filters {
			if i >= maxViewFilters*10 {
				break
			}
			entry := ViewFilter{Column: shortText(filter.ColumnKey), Predicate: shortText(filter.Predicate)}
			if term := strings.TrimSpace(string(filter.Term)); term != "" && term != "null" && len(term) <= maxViewText {
				entry.Term = json.RawMessage(term)
			}
			view.Filters = append(view.Filters, entry)
		}
	}
	var sorts []struct {
		ColumnKey string `json:"column_key"`
		SortType  string `json:"sort_type"`
	}
	if json.Unmarshal(wire.Sorts, &sorts) != nil {
		var wrapped struct {
			Sorts []struct {
				ColumnKey string `json:"column_key"`
				SortType  string `json:"sort_type"`
			} `json:"sorts"`
		}
		if json.Unmarshal(wire.Sorts, &wrapped) == nil {
			sorts = wrapped.Sorts
		}
	}
	for i, sortKey := range sorts {
		if i >= maxViewHidden {
			break
		}
		view.Sorts = append(view.Sorts, ViewSort{Column: shortText(sortKey.ColumnKey), Direction: shortText(sortKey.SortType)})
	}
	var hidden []string
	if json.Unmarshal(wire.HiddenColumns, &hidden) == nil {
		for i, key := range hidden {
			if i >= maxViewHidden*10 {
				break
			}
			view.HiddenColumns = append(view.HiddenColumns, shortText(key))
		}
	}
	return view, nil
}

// ListViews reads the views of the selected table. A target narrowed to one view lists only that view.
func (c *Client) ListViews(ctx context.Context, input ViewInput) (*ViewsResult, error) {
	const op = "list views"
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Views []viewWire `json:"views"`
	}
	if err := c.get(ctx, op, gatewayPath+url.PathEscape(scoped.access.uuid)+viewsPath, scoped.query(),
		scoped.access.token, maxResponseBytes, &answer); err != nil {
		return nil, err
	}
	result := &ViewsResult{Table: formatTarget(scoped.selected), Views: []View{}}
	for _, wire := range answer.Views {
		if !scoped.inBinding(wire.ID, wire.Name) {
			continue
		}
		if len(result.Views) >= maxViews {
			result.Truncated = true
			break
		}
		view, err := normalizeView(op, wire)
		if err != nil {
			return nil, err
		}
		result.Views = append(result.Views, view)
	}
	return result, nil
}

// GetView reads one view after the metadata confirmed that it belongs to the selected table and lies
// inside the connection's binding.
func (c *Client) GetView(ctx context.Context, input ViewInput) (*ViewResult, error) {
	const op = "get view"
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return nil, err
	}
	id, name, err := scoped.find(op, input.View)
	if err != nil {
		return nil, err
	}
	if !scoped.inBinding(id, name) {
		return nil, providerError(op, "the selected SeaTable view is outside this connection's allow-list")
	}
	var answer json.RawMessage
	if err := c.get(ctx, op, scoped.viewPath(name), scoped.query(), scoped.access.token, maxResponseBytes, &answer); err != nil {
		return nil, err
	}
	var wire viewWire
	var wrapped struct {
		View *viewWire `json:"view"`
	}
	if json.Unmarshal(answer, &wrapped) == nil && wrapped.View != nil {
		wire = *wrapped.View
	} else if json.Unmarshal(answer, &wire) != nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "SeaTable returned an invalid response"}
	}
	if wire.Name == "" {
		wire.Name = name
	}
	if wire.ID == "" {
		wire.ID = id
	}
	view, err := normalizeView(op, wire)
	if err != nil {
		return nil, err
	}
	return &ViewResult{Table: formatTarget(scoped.selected), View: view}, nil
}

// CreateView sends exactly one request that creates an empty view.
func (c *Client) CreateView(ctx context.Context, op string, input ViewInput) error {
	if err := checkViewRequest(op, "create", input); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	if scoped.selected.view != "" {
		return providerError(op, "a table selection narrowed to one view cannot create views")
	}
	body, err := json.Marshal(map[string]string{"name": input.Name})
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	path := gatewayPath + url.PathEscape(scoped.access.uuid) + viewsPath + "?" + scoped.query().Encode()
	return c.changeOnce(ctx, op, http.MethodPost, path, scoped.access.token, body, viewUncertain)
}

// changeTarget resolves the view of an update or delete and refuses a view outside the binding and a view
// that is itself a connection target.
func (c *Client) changeTarget(ctx context.Context, op string, input ViewInput) (*viewScope, string, error) {
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return nil, "", err
	}
	id, name, err := scoped.find(op, input.View)
	if err != nil {
		return nil, "", err
	}
	if !scoped.inBinding(id, name) {
		return nil, "", providerError(op, "the selected SeaTable view is outside this connection's allow-list")
	}
	if c.isTarget(scoped, id, name) {
		return nil, "", providerError(op, "a view that is configured as a connection target cannot be changed or deleted")
	}
	return scoped, name, nil
}

// DeleteView sends exactly one request that deletes the view.
func (c *Client) DeleteView(ctx context.Context, op string, input ViewInput) error {
	scoped, name, err := c.changeTarget(ctx, op, input)
	if err != nil {
		return err
	}
	return c.changeOnce(ctx, op, http.MethodDelete, scoped.viewPath(name)+"?"+scoped.query().Encode(),
		scoped.access.token, nil, viewUncertain)
}

// UpdateView sends exactly one request that changes the view. Every column is resolved against the table's
// metadata and sent as its key.
func (c *Client) UpdateView(ctx context.Context, op string, input ViewInput) error {
	if err := checkViewRequest(op, "update", input); err != nil {
		return err
	}
	scoped, name, err := c.changeTarget(ctx, op, input)
	if err != nil {
		return err
	}
	keys := func(column string) (string, bool) { return columnKey(scoped.table, column) }
	payload := map[string]any{}
	if input.Name != "" {
		payload["name"] = input.Name
	}
	if input.FilterConjunction != "" {
		payload["filter_conjunction"] = map[string]string{"and": "And", "or": "Or"}[input.FilterConjunction]
	}
	if input.Filters != nil {
		filters := make([]map[string]any, 0, len(*input.Filters))
		for _, filter := range *input.Filters {
			key, ok := keys(filter.Column)
			if !ok {
				return providerError(op, "the selected SeaTable table does not define every requested column")
			}
			entry := map[string]any{"column_key": key, "filter_predicate": filter.Predicate}
			if len(filter.Term) > 0 {
				term, _ := scalar(filter.Term)
				entry["filter_term"] = term
			}
			filters = append(filters, entry)
		}
		payload["filters"] = filters
	}
	if input.Sorts != nil {
		sorts := make([]map[string]string, 0, len(*input.Sorts))
		for _, sortKey := range *input.Sorts {
			key, ok := keys(sortKey.Column)
			if !ok {
				return providerError(op, "the selected SeaTable table does not define every requested column")
			}
			direction := "up"
			if sortKey.Direction == "desc" {
				direction = "down"
			}
			sorts = append(sorts, map[string]string{"column_key": key, "sort_type": direction})
		}
		payload["sorts"] = sorts
	}
	if input.HiddenColumns != nil {
		hidden := make([]string, 0, len(*input.HiddenColumns))
		seen := map[string]bool{}
		for _, column := range *input.HiddenColumns {
			key, ok := keys(column)
			if !ok {
				return providerError(op, "the selected SeaTable table does not define every requested column")
			}
			if seen[key] {
				return providerError(op, "a hidden column is listed twice")
			}
			seen[key] = true
			hidden = append(hidden, key)
		}
		payload["hidden_columns"] = hidden
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > maxRequestBytes {
		return providerError(op, "the request exceeds the size limit")
	}
	return c.changeOnce(ctx, op, http.MethodPut, scoped.viewPath(name)+"?"+scoped.query().Encode(),
		scoped.access.token, body, viewUncertain)
}

// checkViewShape validates the counts and values of a view update without any I/O.
func checkViewShape(input ViewInput) error {
	if input.FilterConjunction != "" && input.FilterConjunction != "and" && input.FilterConjunction != "or" {
		return errors.New("the filter conjunction must be and or or")
	}
	if input.Filters != nil {
		if len(*input.Filters) > maxViewFilters {
			return errors.New("a view accepts at most " + strconv.Itoa(maxViewFilters) + " filters")
		}
		for _, filter := range *input.Filters {
			if filter.Column == "" || !validName(filter.Column) || !validPredicate(filter.Predicate) {
				return errors.New("a view filter needs a column and a lowercase predicate")
			}
			if len(filter.Term) > 0 {
				if _, err := scalar(filter.Term); err != nil {
					return err
				}
			}
		}
	}
	if input.Sorts != nil {
		if len(*input.Sorts) > maxViewSorts {
			return errors.New("a view accepts at most " + strconv.Itoa(maxViewSorts) + " sort keys")
		}
		for _, sortKey := range *input.Sorts {
			if !validName(sortKey.Column) || (sortKey.Direction != "" && sortKey.Direction != "asc" && sortKey.Direction != "desc") {
				return errors.New("a view sort key needs a column and the direction asc or desc")
			}
		}
	}
	if input.HiddenColumns != nil {
		if len(*input.HiddenColumns) > maxViewHidden {
			return errors.New("a view hides at most " + strconv.Itoa(maxViewHidden) + " columns")
		}
		for _, column := range *input.HiddenColumns {
			if !validName(column) {
				return errors.New("a hidden column needs a usable name")
			}
		}
	}
	return nil
}

func validPredicate(value string) bool {
	if value == "" || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

// columnKey resolves a column name, or failing that a column key, of the table to its key.
func columnKey(table tableJSON, column string) (string, bool) {
	for _, byKey := range []bool{false, true} {
		for _, candidate := range table.Columns {
			if ((!byKey && candidate.Name == column) || (byKey && candidate.Key == column)) && validID(candidate.Key) {
				return candidate.Key, true
			}
		}
	}
	return "", false
}
