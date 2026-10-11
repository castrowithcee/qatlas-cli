package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
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
	viewFieldsPath = "/rest/metadata/viewFields"

	viewColumnSizeMax     = 2000
	viewColumnPositionMax = 1000000

	viewColumnUncertain = "; this change may have taken effect, read the view with twentycrm.views.get before repeating it"

	errViewWritePermission = "the workspace role of this API key may not change views; check the role in Twenty, " +
		"the right is called Views"
	errViewNotChangeable = "only custom views of the whole workspace can be changed: standard views and index views are refused"
	errViewColumnInput   = "size must be an integer from 1 through 2000, position a number from 0 through 1000000"
)

// viewColumnStatusTexts answers an unknown column, and a column of a view that is not readable, alike.
var viewColumnStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read views; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this view column",
}

const (
	viewPartWriteNote = "Changes a view that every user of the whole Twenty workspace sees. Only custom views of the " +
		"whole workspace can be changed: personal views, standard and index views, and widget views are refused. " +
		"Needs the Twenty right Views and a connection without object targets, and is offered only by a connection " +
		"whose tools list names it. Qatlas reads the view first and sends the change once; after an unclear result " +
		"read the view before repeating it"

	viewColumnSizeSchema     = `{"type":"integer","minimum":1,"maximum":2000}`
	viewColumnPositionSchema = `{"type":"number","minimum":0,"maximum":1000000}`

	viewColumnSchema = `{"type":"object","properties":{"id":{"type":"string"},"view_id":{"type":"string"},` +
		`"field_id":{"type":"string"},"field":{"type":"string"},"is_visible":{"type":"boolean"},` +
		`"size":{"type":"number"},"position":{"type":"number"}},` +
		`"required":["id","view_id","field_id"],"additionalProperties":false}`
)

var viewColumnFields = []capability.Field{
	{Name: "id", Description: "The column; use it with twentycrm.viewfields.update"},
	{Name: "view_id", Description: "The view that holds the column"},
	{Name: "field_id", Description: "The field shown by the column"},
	{Name: "field", Description: "API name of the field, untrusted data"},
}

var viewfieldsCreate = capability.Descriptor{
	ID: Provider + ".viewfields.create", Version: 1, Title: "Add a column to a Twenty CRM view",
	Description: "Add one field as a column to a view of the Twenty workspace of a connection. The field must belong " +
		"to the object of the view, be active, and not be a system field, and must not be a column of the view yet. " +
		"The new column is visible unless is_visible is false. " + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "columns", "create"},
	Risk:     viewWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"view_id":` + uuidSchema + `,"field_id":` + uuidSchema +
		`,"is_visible":{"type":"boolean"},"size":` + viewColumnSizeSchema + `,"position":` + viewColumnPositionSchema +
		`},"required":["view_id","field_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewColumnSchema),
	Arguments: []capability.Argument{
		{Name: "view_id", Required: true, Description: "UUID of the view, as returned by twentycrm.views.list"},
		{Name: "field_id", Required: true, Description: "UUID of the field, as returned by twentycrm.metaobjects.get"},
		{Name: "is_visible", Description: "Whether the column is shown; true when omitted"},
		{Name: "size", Description: "Width of the column, an integer from 1 through 2000"},
		{Name: "position", Description: "Position among the columns, a number from 0 through 1000000"},
	},
	Fields: viewColumnFields,
	Examples: []capability.Example{{Description: "Add a hidden column",
		Arguments: json.RawMessage(`{"view_id":"123e4567-e89b-42d3-a456-426614174000",` +
			`"field_id":"123e4567-e89b-42d3-a456-426614174001","is_visible":false}`)}},
}

var viewfieldsUpdate = capability.Descriptor{
	ID: Provider + ".viewfields.update", Version: 1, Title: "Update a column of a Twenty CRM view",
	Description: "Show or hide one column of a view of the Twenty workspace of a connection, or change its position " +
		"or width; only the given values change and at least one is required. " + viewPartWriteNote,
	Tags:     []string{"twentycrm", "views", "columns", "update"},
	Risk:     viewWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"is_visible":{"type":"boolean"},` +
		`"size":` + viewColumnSizeSchema + `,"position":` + viewColumnPositionSchema + `},"required":["id"],` +
		`"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(viewColumnSchema),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the column, as returned by twentycrm.viewfields.create"},
		{Name: "is_visible", Description: "Whether the column is shown"},
		{Name: "size", Description: "Width of the column, an integer from 1 through 2000"},
		{Name: "position", Description: "Position among the columns, a number from 0 through 1000000"},
	},
	Fields: viewColumnFields,
	Examples: []capability.Example{{Description: "Hide a column",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174002","is_visible":false}`)}},
}

// ViewColumn is one column of a view as the column tools report it.
type ViewColumn struct {
	ID        string  `json:"id"`
	ViewID    string  `json:"view_id"`
	FieldID   string  `json:"field_id"`
	Field     string  `json:"field,omitempty"`
	IsVisible *bool   `json:"is_visible,omitempty"`
	Size      float64 `json:"size"`
	Position  float64 `json:"position"`
}

// viewColumnWire is the positive projection of Twenty's view field: the creator and application are never decoded.
type viewColumnWire struct {
	ID              string   `json:"id"`
	ViewID          string   `json:"viewId"`
	FieldMetadataID string   `json:"fieldMetadataId"`
	IsVisible       *bool    `json:"isVisible"`
	Size            *float64 `json:"size"`
	Position        *float64 `json:"position"`
	DeletedAt       *string  `json:"deletedAt"`
}

// viewColumnPatch is the whole body of a column update. Aggregation and field groups are never sent.
type viewColumnPatch struct {
	IsVisible *bool    `json:"isVisible,omitempty"`
	Size      *int     `json:"size,omitempty"`
	Position  *float64 `json:"position,omitempty"`
}

// viewColumnCreate is the whole body of a column create.
type viewColumnCreate struct {
	ViewID          string   `json:"viewId"`
	FieldMetadataID string   `json:"fieldMetadataId"`
	IsVisible       *bool    `json:"isVisible,omitempty"`
	Size            *int     `json:"size,omitempty"`
	Position        *float64 `json:"position,omitempty"`
}

func validColumnShape(size *int, position *float64) bool {
	return (size == nil || (*size >= 1 && *size <= viewColumnSizeMax)) &&
		(position == nil || (*position >= 0 && *position <= viewColumnPositionMax))
}

// changeableView is a parent view that passed the checks every view change shares.
type changeableView struct {
	record viewRecord
}

// fetchChangeableView reads a view by identifier before a change to it or to one of its parts. A view that is
// not workspace-wide or not of a visible type answers exactly like an unknown one, through texts; a view that
// is readable but not custom or an index view is refused.
func (c *Client) fetchChangeableView(ctx context.Context, op, id string, texts provider.StatusTexts) (*changeableView, error) {
	record, err := c.readView(ctx, op, id, texts)
	if err != nil {
		return nil, err
	}
	if !validUUID(record.ObjectMetadataID) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view than the requested one")
	}
	if !isTrue(record.IsCustom) || (record.Key != nil && *record.Key != "") {
		return nil, invalidRequest(errViewNotChangeable)
	}
	return &changeableView{record: *record}, nil
}

// columnOf returns the identifier of the live column that shows the field, if there is one.
func (v *changeableView) columnOf(op, fieldID string) (string, bool, error) {
	return liveChildOf(op, v.record.ViewFields, fieldID, "column")
}

// sortOf returns the identifier of the live sort on the field, if there is one.
func (v *changeableView) sortOf(op, fieldID string) (string, bool, error) {
	return liveChildOf(op, v.record.ViewSorts, fieldID, "sort")
}

// liveChildOf finds the live child of a view layout that is bound to the field.
func liveChildOf(op string, raw json.RawMessage, fieldID, kind string) (string, bool, error) {
	items, err := children(op, raw)
	if err != nil {
		return "", false, err
	}
	for _, item := range items {
		var child struct {
			ID              string  `json:"id"`
			FieldMetadataID string  `json:"fieldMetadataId"`
			DeletedAt       *string `json:"deletedAt"`
		}
		if json.Unmarshal(item, &child) != nil || !validUUID(child.FieldMetadataID) {
			return "", false, provider.InvalidResponse(op, "Twenty returned an unusable view "+kind)
		}
		if child.DeletedAt == nil && equalUUID(child.FieldMetadataID, fieldID) {
			return child.ID, true, nil
		}
	}
	return "", false, nil
}

func mapViewWritePermission(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = errViewWritePermission
	}
	return err
}

// parseColumn checks the answer to a column change: it must name the requested column in the requested view.
func parseColumn(op string, raw json.RawMessage, wantID, wantView, wantField string) (*ViewColumn, error) {
	var wire viewColumnWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.ViewID) ||
		!validUUID(wire.FieldMetadataID) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view column")
	}
	if (wantID != "" && !equalUUID(wire.ID, wantID)) || (wantView != "" && !equalUUID(wire.ViewID, wantView)) ||
		(wantField != "" && !equalUUID(wire.FieldMetadataID, wantField)) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view column than the requested one")
	}
	return &ViewColumn{ID: wire.ID, ViewID: wire.ViewID, FieldID: wire.FieldMetadataID, IsVisible: wire.IsVisible,
		Size: number(wire.Size), Position: number(wire.Position)}, nil
}

// CreateViewColumn reads the view and its object, then sends exactly one POST.
func (c *Client) CreateViewColumn(ctx context.Context, body viewColumnCreate) (*ViewColumn, error) {
	const op = "add view column"
	view, err := c.fetchChangeableView(ctx, op, body.ViewID, viewStatusTexts)
	if err != nil {
		return nil, err
	}
	object, err := c.GetMetaObject(ctx, view.record.ObjectMetadataID)
	if err != nil {
		return nil, err
	}
	fieldName, usable := "", false
	for _, field := range object.Fields {
		if equalUUID(field.ID, body.FieldMetadataID) {
			fieldName, usable = field.Name, isTrue(field.IsActive) && !isTrue(field.IsSystem)
		}
	}
	if !usable {
		return nil, invalidRequest("the field is not an active, non-system field of the object of this view")
	}
	existing, found, err := view.columnOf(op, body.FieldMetadataID)
	if err != nil {
		return nil, err
	}
	if found {
		return nil, invalidRequest("this field is already a column of the view (column " + existing +
			"); change it with twentycrm.viewfields.update")
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewColumnUncertain, http.MethodPost, viewFieldsPath, body, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	column, err := parseColumn(op, answer, "", body.ViewID, body.FieldMetadataID)
	if err != nil {
		return nil, uncertainly(err, viewColumnUncertain)
	}
	column.Field = metaText(&fieldName, metaNameMax)
	return column, nil
}

// UpdateViewColumn reads the column and its view, then sends exactly one PATCH. The view of the column, not
// one named by the caller, decides what may change.
func (c *Client) UpdateViewColumn(ctx context.Context, id string, patch viewColumnPatch) (*ViewColumn, error) {
	const op = "update view column"
	var current json.RawMessage
	path := viewFieldsPath + "/" + url.PathEscape(id)
	if err := c.getWith(ctx, op, path, nil, viewResponseBytes, &current, viewColumnStatusTexts); err != nil {
		return nil, err
	}
	column, err := parseColumn(op, current, id, "", "")
	if err != nil {
		return nil, err
	}
	var wire viewColumnWire
	if json.Unmarshal(current, &wire) != nil || wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, viewColumnStatusTexts)
	}
	if _, err := c.fetchChangeableView(ctx, op, column.ViewID, viewColumnStatusTexts); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, viewColumnUncertain, http.MethodPatch, path, patch, &answer); err != nil {
		return nil, mapViewWritePermission(err)
	}
	updated, err := parseColumn(op, answer, id, column.ViewID, column.FieldID)
	if err != nil {
		return nil, uncertainly(err, viewColumnUncertain)
	}
	return updated, nil
}

func invokeViewFieldsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ViewID    string   `json:"view_id"`
		FieldID   string   `json:"field_id"`
		IsVisible *bool    `json:"is_visible"`
		Size      *int     `json:"size"`
		Position  *float64 `json:"position"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("add view column", "the validated arguments could not be read")
	}
	if !validUUID(args.ViewID) || !validUUID(args.FieldID) {
		return nil, invalidRequest("view_id and field_id must be UUIDs")
	}
	if !validColumnShape(args.Size, args.Position) {
		return nil, invalidRequest(errViewColumnInput)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateViewColumn(ctx, viewColumnCreate{ViewID: strings.ToLower(args.ViewID),
		FieldMetadataID: strings.ToLower(args.FieldID), IsVisible: args.IsVisible, Size: args.Size, Position: args.Position})
}

func invokeViewFieldsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID        string   `json:"id"`
		IsVisible *bool    `json:"is_visible"`
		Size      *int     `json:"size"`
		Position  *float64 `json:"position"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("update view column", "the validated arguments could not be read")
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the column id must be a UUID")
	}
	if args.IsVisible == nil && args.Size == nil && args.Position == nil {
		return nil, invalidRequest("is_visible, size, or position is required")
	}
	if !validColumnShape(args.Size, args.Position) {
		return nil, invalidRequest(errViewColumnInput)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateViewColumn(ctx, strings.ToLower(args.ID),
		viewColumnPatch{IsVisible: args.IsVisible, Size: args.Size, Position: args.Position})
}
