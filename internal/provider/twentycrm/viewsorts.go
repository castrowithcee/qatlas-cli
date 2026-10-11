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
	viewSortsPath = "/rest/metadata/viewSorts"

	viewSortUncertain = "; this change may have taken effect, read the view with twentycrm.views.get before repeating it"

	errViewSortField = "the field is not an active, non-system field of the object of this view that can be sorted"
	errViewSortSub   = "a FULL_NAME field is sorted by sub_field_name firstName or lastName; other fields take no sub_field_name"
)

// viewSortStatusTexts answers an unknown sort, and a sort of a view that is not readable, alike.
var viewSortStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read views; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this view sort",
}

var (
	// viewSortScalarTypes are the field types a view can be sorted by without a sub field. Relation, array,
	// multi-select, JSON, rich text, and the other composite types are refused.
	viewSortScalarTypes = []string{"BOOLEAN", "DATE", "DATE_TIME", "NUMBER", "NUMERIC", "POSITION", "RATING", "SELECT",
		"TEXT", "UUID"}

	// viewSortSubFields are the sub fields Twenty allows for a FULL_NAME sort.
	viewSortSubFields = []string{"firstName", "lastName"}
)

const viewSortSchema = `{"type":"object","properties":{"id":{"type":"string"},"view_id":{"type":"string"},` +
	`"field_id":{"type":"string"},"field":{"type":"string"},"direction":{"type":"string"},` +
	`"sub_field_name":{"type":"string"}},"required":["id","view_id","field_id","direction"],"additionalProperties":false}`

var viewSortFields = []capability.Field{
	{Name: "id", Description: "The sort; use it with twentycrm.viewsorts.update"},
	{Name: "view_id", Description: "The view that holds the sort"},
	{Name: "field_id", Description: "The field the view is sorted by"},
	{Name: "field", Description: "API name of the field, untrusted data"},
	{Name: "direction", Description: "ASC or DESC"},
	{Name: "sub_field_name", Description: "Sub field of a FULL_NAME field, untrusted data"},
}

var viewsortsCreate = capability.Descriptor{
	ID: Provider + ".viewsorts.create", Version: 1, Title: "Add a sort to a Twenty CRM view",
	Description: "Add one sort to a view of the Twenty workspace of a connection. The field must belong to the object " +
		"of the view, be active, not be a system field, and be of a sortable type (relation, array, multi-select, " +
		"JSON, rich text, and composite fields other than FULL_NAME are refused), and must not be sorted by the view " +
		"yet. A FULL_NAME field needs sub_field_name. " + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "sorts", "create"},
	Risk:     viewWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"view_id":` + uuidSchema + `,"field_id":` + uuidSchema +
		`,"direction":` + enumSchema(viewDirections) + `,"sub_field_name":` + enumSchema(viewSortSubFields) +
		`},"required":["view_id","field_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewSortSchema),
	Arguments: []capability.Argument{
		{Name: "view_id", Required: true, Description: "UUID of the view, as returned by twentycrm.views.list"},
		{Name: "field_id", Required: true, Description: "UUID of the field, as returned by twentycrm.metaobjects.get"},
		{Name: "direction", Description: "One of " + strings.Join(viewDirections, ", ") + "; ASC when omitted"},
		{Name: "sub_field_name", Description: "One of " + strings.Join(viewSortSubFields, ", ") +
			"; required for a FULL_NAME field, refused for any other"},
	},
	Fields: viewSortFields,
	Examples: []capability.Example{{Description: "Sort a view descending",
		Arguments: json.RawMessage(`{"view_id":"123e4567-e89b-42d3-a456-426614174000",` +
			`"field_id":"123e4567-e89b-42d3-a456-426614174001","direction":"DESC"}`)}},
}

var viewsortsUpdate = capability.Descriptor{
	ID: Provider + ".viewsorts.update", Version: 1, Title: "Update a sort of a Twenty CRM view",
	Description: "Change the direction of one sort of a view of the Twenty workspace of a connection; the field of " +
		"the sort does not change. " + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "sorts", "update"},
	Risk:     viewWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"direction":` +
		enumSchema(viewDirections) + `},"required":["id","direction"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewSortSchema),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the sort, as returned by twentycrm.viewsorts.create or twentycrm.views.get"},
		{Name: "direction", Required: true, Description: "One of " + strings.Join(viewDirections, ", ")},
	},
	Fields: viewSortFields,
	Examples: []capability.Example{{Description: "Reverse a sort",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174002","direction":"ASC"}`)}},
}

// ViewSortResult is one sort of a view as the sort tools report it.
type ViewSortResult struct {
	ID           string `json:"id"`
	ViewID       string `json:"view_id"`
	FieldID      string `json:"field_id"`
	Field        string `json:"field,omitempty"`
	Direction    string `json:"direction"`
	SubFieldName string `json:"sub_field_name,omitempty"`
}

// viewSortWire is the positive projection of Twenty's view sort: the creator and application are never decoded.
type viewSortWire struct {
	ID              string  `json:"id"`
	ViewID          string  `json:"viewId"`
	FieldMetadataID string  `json:"fieldMetadataId"`
	Direction       string  `json:"direction"`
	SubFieldName    *string `json:"subFieldName"`
	DeletedAt       *string `json:"deletedAt"`
}

// viewSortCreate is the whole body of a sort create.
type viewSortCreate struct {
	ViewID          string `json:"viewId"`
	FieldMetadataID string `json:"fieldMetadataId"`
	Direction       string `json:"direction,omitempty"`
	SubFieldName    string `json:"subFieldName,omitempty"`
}

// viewSortPatch is the whole body of a sort update. The sub field is never sent.
type viewSortPatch struct {
	Direction string `json:"direction"`
}

func validDirection(direction string) bool {
	return containsName(viewDirections, direction)
}

// parseSort checks the answer to a sort change: it must name the requested sort in the requested view. The sub
// field is compared on create only, because an update never sends one.
func parseSort(op string, raw json.RawMessage, wantID, wantView, wantField, wantDirection, wantSub string) (*ViewSortResult, error) {
	var wire viewSortWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) ||
		!validUUID(wire.FieldMetadataID) || !validDirection(wire.Direction) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view sort")
	}
	sub := ""
	if wire.SubFieldName != nil {
		sub = *wire.SubFieldName
	}
	if (wantID != "" && !equalUUID(wire.ID, wantID)) || (wantView != "" && !equalUUID(wire.ViewID, wantView)) ||
		(wantField != "" && !equalUUID(wire.FieldMetadataID, wantField)) ||
		(wantDirection != "" && wire.Direction != wantDirection) || (wantID == "" && sub != wantSub) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view sort than the requested one")
	}
	return &ViewSortResult{ID: wire.ID, ViewID: wire.ViewID, FieldID: wire.FieldMetadataID,
		Direction: wire.Direction, SubFieldName: metaText(wire.SubFieldName, viewSubFieldMax)}, nil
}

// CreateViewSort reads the view and its object, then sends exactly one POST.
func (c *Client) CreateViewSort(ctx context.Context, body viewSortCreate) (*ViewSortResult, error) {
	const op = "add view sort"
	view, err := c.fetchChangeableView(ctx, op, body.ViewID, viewStatusTexts)
	if err != nil {
		return nil, err
	}
	object, err := c.GetMetaObject(ctx, view.record.ObjectMetadataID)
	if err != nil {
		return nil, err
	}
	var field *MetaField
	for i := range object.Fields {
		if equalUUID(object.Fields[i].ID, body.FieldMetadataID) {
			field = &object.Fields[i]
		}
	}
	if field == nil || !isTrue(field.IsActive) || isTrue(field.IsSystem) {
		return nil, invalidRequest(errViewSortField)
	}
	switch {
	case containsName(viewSortScalarTypes, field.Type):
		if body.SubFieldName != "" {
			return nil, invalidRequest(errViewSortSub)
		}
	case field.Type == "FULL_NAME":
		if !containsName(viewSortSubFields, body.SubFieldName) {
			return nil, invalidRequest(errViewSortSub)
		}
	default:
		return nil, invalidRequest(errViewSortField)
	}
	existing, found, err := view.sortOf(op, body.FieldMetadataID)
	if err != nil {
		return nil, err
	}
	if found {
		return nil, invalidRequest("this field is already sorted by the view (sort " + existing +
			"); change it with twentycrm.viewsorts.update")
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewSortUncertain, http.MethodPost, viewSortsPath, body, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	wantDirection := body.Direction
	sort, err := parseSort(op, answer, "", body.ViewID, body.FieldMetadataID, wantDirection, body.SubFieldName)
	if err != nil {
		return nil, uncertainly(err, viewSortUncertain)
	}
	sort.Field = metaText(&field.Name, metaNameMax)
	return sort, nil
}

// UpdateViewSort reads the sort and its view, then sends exactly one PATCH. The view of the sort, not one
// named by the caller, decides what may change.
func (c *Client) UpdateViewSort(ctx context.Context, id string, patch viewSortPatch) (*ViewSortResult, error) {
	const op = "update view sort"
	var current json.RawMessage
	path := viewSortsPath + "/" + url.PathEscape(id)
	if err := c.getWith(ctx, op, path, nil, viewResponseBytes, &current, viewSortStatusTexts); err != nil {
		return nil, err
	}
	sort, err := parseSort(op, current, id, "", "", "", "")
	if err != nil {
		return nil, err
	}
	var wire viewSortWire
	if json.Unmarshal(current, &wire) != nil || wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, viewSortStatusTexts)
	}
	if _, err := c.fetchChangeableView(ctx, op, sort.ViewID, viewSortStatusTexts); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewSortUncertain, http.MethodPatch, path, patch, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	updated, err := parseSort(op, answer, id, sort.ViewID, sort.FieldID, patch.Direction, "")
	if err != nil {
		return nil, uncertainly(err, viewSortUncertain)
	}
	return updated, nil
}

func invokeViewSortsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ViewID       string `json:"view_id"`
		FieldID      string `json:"field_id"`
		Direction    string `json:"direction"`
		SubFieldName string `json:"sub_field_name"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("add view sort", "the validated arguments could not be read")
	}
	if !validUUID(args.ViewID) || !validUUID(args.FieldID) {
		return nil, invalidRequest("view_id and field_id must be UUIDs")
	}
	if args.Direction != "" && !validDirection(args.Direction) {
		return nil, invalidRequest("direction must be ASC or DESC")
	}
	if args.SubFieldName != "" && !containsName(viewSortSubFields, args.SubFieldName) {
		return nil, invalidRequest(errViewSortSub)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateViewSort(ctx, viewSortCreate{ViewID: strings.ToLower(args.ViewID),
		FieldMetadataID: strings.ToLower(args.FieldID), Direction: args.Direction, SubFieldName: args.SubFieldName})
}

func invokeViewSortsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID        string `json:"id"`
		Direction string `json:"direction"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("update view sort", "the validated arguments could not be read")
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the sort id must be a UUID")
	}
	if !validDirection(args.Direction) {
		return nil, invalidRequest("direction must be ASC or DESC")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateViewSort(ctx, strings.ToLower(args.ID), viewSortPatch{Direction: args.Direction})
}
