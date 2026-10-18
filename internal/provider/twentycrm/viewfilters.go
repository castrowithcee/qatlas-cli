package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	viewFilterUncertain = "; this change may have taken effect, read the view with twenty-crm.views.get before repeating it"

	errViewFilterField = "the field is not an active, non-system field of the object of this view with a type that " +
		"a filter can be written for"
	errViewFilterShape = "only a simple filter on a field, without sub field or relation target, and with an operand " +
		"that Qatlas writes, can be changed"
	errViewFilterNothing = "operand or one value is required"

	// viewFilterNote holds the table of operands and values; the provider document refers to it.
	viewFilterNote = "The field type decides the operands and the value. Any field type but BOOLEAN and relation " +
		"fields takes IS_EMPTY and IS_NOT_EMPTY without a value. TEXT: CONTAINS, DOES_NOT_CONTAIN with text. SELECT: " +
		"IS, IS_NOT with options. MULTI_SELECT: CONTAINS, DOES_NOT_CONTAIN with options. Options are option values " +
		"of the field schema. NUMBER: IS, IS_NOT, GREATER_THAN_OR_EQUAL, LESS_THAN_OR_EQUAL with number. DATE: IS, " +
		"IS_BEFORE, IS_AFTER with date as YYYY-MM-DD; IS_RELATIVE with relative (direction PAST or NEXT with an " +
		"amount, or THIS without; unit SECOND to YEAR); IS_IN_PAST, IS_IN_FUTURE, IS_TODAY without a value. " +
		"DATE_TIME: IS with date as YYYY-MM-DD or a UTC timestamp ending in Z; IS_BEFORE, IS_AFTER with a UTC " +
		"timestamp ending in Z. BOOLEAN: IS with flag. Qatlas writes the value itself from exactly one of these " +
		"typed arguments. Filter groups, relation filters, and sub fields of composite fields are not supported. "
)

// viewFilterStatusTexts answers an unknown filter, and a filter of a view that is not readable, alike.
var viewFilterStatusTexts = viewPartTexts("filter")

const viewFilterValueProperties = `"text":{"type":"string","minLength":1,"maxLength":500},` +
	`"options":{"type":"array","minItems":1,"maxItems":20,"uniqueItems":true,"items":` +
	`{"type":"string","minLength":1,"maxLength":200}},` +
	`"number":{"type":"number","minimum":-1e15,"maximum":1e15},` +
	`"date":{"type":"string","minLength":10,"maxLength":40},` +
	`"flag":{"type":"boolean"},` +
	`"relative":{"type":"object","properties":{"direction":{"type":"string","enum":["PAST","NEXT","THIS"]},` +
	`"amount":{"type":"integer","minimum":1,"maximum":1000},` +
	`"unit":{"type":"string","enum":["SECOND","MINUTE","HOUR","DAY","WEEK","MONTH","QUARTER","YEAR"]}},` +
	`"required":["direction","unit"],"minProperties":2,"maxProperties":3,"additionalProperties":false}`

const viewFilterSchema = `{"type":"object","properties":{"id":{"type":"string"},"view_id":{"type":"string"},` +
	`"field_id":{"type":"string"},"field":{"type":"string"},"operand":{"type":"string"},"value":{"type":"string"}},` +
	`"required":["id","view_id","field_id","operand"],"additionalProperties":false}`

var viewFilterFields = []capability.Field{
	{Name: "id", Description: "The filter; use it with twenty-crm.view-filters.update"},
	{Name: "view_id", Description: "The view that holds the filter"},
	{Name: "field_id", Description: "The field the filter is on"},
	{Name: "field", Description: "API name of the field, untrusted data"},
	{Name: "operand", Description: "The operand of the filter"},
	{Name: "value", Description: "The value Qatlas wrote, when it wrote one"},
}

var viewFilterValueArguments = []capability.Argument{
	{Name: "text", Description: "Text for a TEXT field, 1 to 500 characters"},
	{Name: "options", Description: "Option values of a SELECT or MULTI_SELECT field, 1 to 20, each in the field schema"},
	{Name: "number", Description: "Number for a NUMBER field"},
	{Name: "date", Description: "Date as YYYY-MM-DD, or a UTC timestamp ending in Z, for a DATE or DATE_TIME field"},
	{Name: "flag", Description: "true or false for a BOOLEAN field"},
	{Name: "relative", Description: "Relative date for IS_RELATIVE: direction PAST, NEXT, or THIS, amount from 1 " +
		"through 1000 (not for THIS), and unit"},
}

var viewfiltersCreate = capability.Descriptor{
	ID: Provider + ".view-filters.create", Version: 1, Title: "Add a filter to a Twenty CRM view",
	Description: "Add one simple filter to a view of the Twenty workspace of a connection. The field must belong to " +
		"the object of the view, be active, and not be a system field. " + viewFilterNote + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "filters", "create"},
	Risk:     viewWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"view_id":` + uuidSchema + `,"field_id":` + uuidSchema +
		`,"operand":` + enumSchema(filterOperands) + `,` + viewFilterValueProperties +
		`},"required":["view_id","field_id","operand"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewFilterSchema),
	Arguments: append([]capability.Argument{
		{Name: "view_id", Required: true, Description: "UUID of the view, as returned by twenty-crm.views.list"},
		{Name: "field_id", Required: true, Description: "UUID of the field, as returned by twenty-crm.meta-objects.get"},
		{Name: "operand", Required: true, Description: "One of " + strings.Join(filterOperands, ", ")},
	}, viewFilterValueArguments...),
	Fields: viewFilterFields,
	Examples: []capability.Example{{Description: "Keep records whose name contains a text",
		Arguments: json.RawMessage(`{"view_id":"123e4567-e89b-42d3-a456-426614174000",` +
			`"field_id":"123e4567-e89b-42d3-a456-426614174001","operand":"CONTAINS","text":"Berlin"}`)}},
}

var viewfiltersUpdate = capability.Descriptor{
	ID: Provider + ".view-filters.update", Version: 1, Title: "Update a filter of a Twenty CRM view",
	Description: "Change the operand and the value of one simple filter of a view of the Twenty workspace of a " +
		"connection; the field of the filter does not change. A filter on a sub field or a relation, and a filter " +
		"with an operand Qatlas does not write, is refused. A valued operand always needs its value, also when " +
		"only the operand changes; without an operand the filter keeps its operand. " + viewFilterNote + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "filters", "update"},
	Risk:     viewWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"operand":` +
		enumSchema(filterOperands) + `,` + viewFilterValueProperties +
		`},"required":["id"],"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewFilterSchema),
	Arguments: append([]capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the filter, as returned by twenty-crm.view-filters.create or twenty-crm.views.get"},
		{Name: "operand", Description: "One of " + strings.Join(filterOperands, ", ") + "; the operand of the filter when omitted"},
	}, viewFilterValueArguments...),
	Fields: viewFilterFields,
	Examples: []capability.Example{{Description: "Change the text of a filter",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174002","text":"Hamburg"}`)}},
}

// ViewFilterResult is one filter of a view as the filter tools report it.
type ViewFilterResult struct {
	ID      string `json:"id"`
	ViewID  string `json:"view_id"`
	FieldID string `json:"field_id"`
	Field   string `json:"field,omitempty"`
	Operand string `json:"operand"`
	Value   string `json:"value,omitempty"`
}

// viewFilterWire is the positive projection of Twenty's view filter: the creator and application are never
// decoded, and neither is the stored value.
type viewFilterWire struct {
	ID                    string  `json:"id"`
	ViewID                string  `json:"viewId"`
	FieldMetadataID       string  `json:"fieldMetadataId"`
	Operand               string  `json:"operand"`
	SubFieldName          *string `json:"subFieldName"`
	RelationTargetFieldID *string `json:"relationTargetFieldMetadataId"`
	DeletedAt             *string `json:"deletedAt"`
}

// viewFilterCreate is the whole body of a filter create. The filter group, position, sub field, and relation
// target are never sent.
type viewFilterCreate struct {
	ViewID          string `json:"viewId"`
	FieldMetadataID string `json:"fieldMetadataId"`
	Operand         string `json:"operand"`
	Value           string `json:"value"`
}

// viewFilterPatch is the whole body of a filter update. The field is never sent.
type viewFilterPatch struct {
	Operand string  `json:"operand,omitempty"`
	Value   *string `json:"value,omitempty"`
}

// parseFilter checks the answer to a filter change: it must name the requested filter, view, field, and operand.
func parseFilter(op string, raw json.RawMessage, wantID, wantView, wantField, wantOperand string) (*ViewFilterResult, error) {
	var wire viewFilterWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) ||
		!validUUID(wire.FieldMetadataID) || !containsName(viewOperands, wire.Operand) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view filter")
	}
	if (wantID != "" && !equalUUID(wire.ID, wantID)) || (wantView != "" && !equalUUID(wire.ViewID, wantView)) ||
		(wantField != "" && !equalUUID(wire.FieldMetadataID, wantField)) ||
		(wantOperand != "" && wire.Operand != wantOperand) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view filter than the requested one")
	}
	return &ViewFilterResult{ID: strings.ToLower(wire.ID), ViewID: strings.ToLower(wire.ViewID),
		FieldID: strings.ToLower(wire.FieldMetadataID), Operand: wire.Operand}, nil
}

// filterFieldOf returns the usable field of the object, if the filter can be written for it.
func filterFieldOf(object *MetaObject, fieldID string) *MetaField {
	for i := range object.Fields {
		field := &object.Fields[i]
		if equalUUID(field.ID, fieldID) {
			if !isTrue(field.IsActive) || isTrue(field.IsSystem) || !filterFieldSupported(field.Type) {
				return nil
			}
			return field
		}
	}
	return nil
}

// CreateViewFilter reads the view and its object, checks operand and value, then sends exactly one POST.
func (c *Client) CreateViewFilter(ctx context.Context, viewID, fieldID, operand string, value FilterValue) (*ViewFilterResult, error) {
	const op = "add view filter"
	view, err := c.fetchChangeableView(ctx, op, viewID, viewStatusTexts)
	if err != nil {
		return nil, err
	}
	object, err := c.GetMetaObject(ctx, view.record.ObjectMetadataID)
	if err != nil {
		return nil, err
	}
	field := filterFieldOf(object, fieldID)
	if field == nil {
		return nil, invalidRequest(errViewFilterField)
	}
	stored, err := checkFilterValue(field.Type, field.Options, operand, value)
	if err != nil {
		return nil, err
	}
	body := viewFilterCreate{ViewID: viewID, FieldMetadataID: fieldID, Operand: operand, Value: stored}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewFilterUncertain, http.MethodPost, viewFiltersPath, body, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	filter, err := parseFilter(op, answer, "", viewID, fieldID, operand)
	if err != nil {
		return nil, uncertainly(err, viewFilterUncertain)
	}
	filter.Field = metaText(&field.Name, metaNameMax)
	filter.Value = stored
	return filter, nil
}

// UpdateViewFilter reads the filter, its view, and the object, then sends exactly one PATCH. The view of the
// filter, not one named by the caller, decides what may change.
func (c *Client) UpdateViewFilter(ctx context.Context, id, operand string, value FilterValue) (*ViewFilterResult, error) {
	const op = "update view filter"
	var current json.RawMessage
	path := viewFiltersPath + "/" + url.PathEscape(id)
	if err := c.getWith(ctx, op, path, nil, viewResponseBytes, &current, viewFilterStatusTexts); err != nil {
		return nil, err
	}
	var wire viewFilterWire
	if json.Unmarshal(current, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) ||
		!validUUID(wire.FieldMetadataID) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view filter")
	}
	if !equalUUID(wire.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view filter than the requested one")
	}
	if wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, viewFilterStatusTexts)
	}
	view, err := c.fetchChangeableView(ctx, op, wire.ViewID, viewFilterStatusTexts)
	if err != nil {
		return nil, err
	}
	if (wire.SubFieldName != nil && *wire.SubFieldName != "") || wire.RelationTargetFieldID != nil {
		return nil, invalidRequest(errViewFilterShape)
	}
	effective := operand
	if effective == "" {
		effective = wire.Operand
	}
	if !containsName(filterOperands, effective) {
		return nil, invalidRequest(errViewFilterShape)
	}
	object, err := c.GetMetaObject(ctx, view.record.ObjectMetadataID)
	if err != nil {
		return nil, err
	}
	field := filterFieldOf(object, wire.FieldMetadataID)
	if field == nil {
		return nil, invalidRequest(errViewFilterField)
	}
	stored, err := checkFilterValue(field.Type, field.Options, effective, value)
	if err != nil {
		return nil, err
	}
	patch := viewFilterPatch{Operand: operand}
	sent := ""
	if operand != "" || value.count() > 0 {
		patch.Value = &stored
		sent = stored
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewFilterUncertain, http.MethodPatch, path, patch, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	updated, err := parseFilter(op, answer, id, wire.ViewID, wire.FieldMetadataID, effective)
	if err != nil {
		return nil, uncertainly(err, viewFilterUncertain)
	}
	updated.Field = metaText(&field.Name, metaNameMax)
	updated.Value = sent
	return updated, nil
}

// viewFilterArgs are the arguments both filter tools share besides the identifiers.
type viewFilterArgs struct {
	Operand string `json:"operand"`
	FilterValue
}

func (a viewFilterArgs) checkShape() error {
	if a.Operand != "" && !containsName(filterOperands, a.Operand) {
		return invalidRequest(errFilterOperand)
	}
	if a.count() > 1 {
		return invalidRequest("at most one value argument is allowed")
	}
	return nil
}

func invokeViewFiltersCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ViewID  string `json:"view_id"`
		FieldID string `json:"field_id"`
		viewFilterArgs
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("add view filter", "the validated arguments could not be read")
	}
	if !validUUID(args.ViewID) || !validUUID(args.FieldID) {
		return nil, invalidRequest("view_id and field_id must be UUIDs")
	}
	if args.Operand == "" {
		return nil, invalidRequest(errFilterOperand)
	}
	if err := args.checkShape(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateViewFilter(ctx, strings.ToLower(args.ViewID), strings.ToLower(args.FieldID), args.Operand,
		args.FilterValue)
}

func invokeViewFiltersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID string `json:"id"`
		viewFilterArgs
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("update view filter", "the validated arguments could not be read")
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the filter id must be a UUID")
	}
	if args.Operand == "" && args.count() == 0 {
		return nil, invalidRequest(errViewFilterNothing)
	}
	if err := args.checkShape(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateViewFilter(ctx, strings.ToLower(args.ID), args.Operand, args.FilterValue)
}
