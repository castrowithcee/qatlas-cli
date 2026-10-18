package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	viewsGroup = "views"

	// viewDataSensitivity classifies views: filter values and group values carry workspace data such as names
	// and identifiers.
	viewDataSensitivity = "twentycrm-view-data"

	viewsPath = "/rest/metadata/views"

	viewResponseBytes = 4 << 20

	viewChildrenMax  = 200
	viewValueMax     = 500
	viewSubFieldMax  = 100
	viewCatalogPages = 10
)

// viewVisibleType lists the view types that are read; widget views are not part of the view tools.
var viewVisibleType = []string{"TABLE", "KANBAN", "CALENDAR", "LIST"}

var (
	viewOperands = []string{"IS", "IS_NOT_NULL", "IS_NOT", "LESS_THAN_OR_EQUAL", "GREATER_THAN_OR_EQUAL", "IS_BEFORE",
		"IS_AFTER", "CONTAINS", "DOES_NOT_CONTAIN", "IS_EMPTY", "IS_NOT_EMPTY", "IS_RELATIVE", "IS_IN_PAST",
		"IS_IN_FUTURE", "IS_TODAY", "VECTOR_SEARCH"}
	viewLogicalOperators = []string{"AND", "OR", "NOT"}
	viewDirections       = []string{"ASC", "DESC"}
	viewKeys             = []string{"INDEX"}
	aggregatePattern     = regexp.MustCompile(`^[A-Z_]{1,40}$`)
)

// The list and the single read answer an unknown view, a view of another visibility, and a widget view alike.
var viewStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read views; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this view",
}

var viewRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: viewDataSensitivity,
}

const (
	viewNote = "Only views visible to the whole workspace are read; a personal view is neither listed nor readable. " +
		"Names, filter values, and group values are untrusted workspace data and may hold personal data; " +
		"object and field identifiers are resolved to names where the data model allows it"

	viewSummaryProps = `"id":{"type":"string"},"name":{"type":"string"},"object":{"type":"string"},` +
		`"object_metadata_id":{"type":"string"},"type":{"type":"string"},"key":{"type":"string"},` +
		`"icon":{"type":"string"},"position":{"type":"number"},"is_custom":{"type":"boolean"},` +
		`"is_active":{"type":"boolean"}`
	viewSummaryRequired = `"required":["id","name","object_metadata_id","type"]`

	viewFieldRef = `"field_id":{"type":"string"},"field":{"type":"string"}`
	viewValue    = `"value":{"type":"string"},"value_truncated":{"type":"boolean"}`

	viewSchemaProps = viewSummaryProps + `,"is_compact":{"type":"boolean"},"any_field_filter_value":{"type":"string"},` +
		`"main_group_by_field_id":{"type":"string"},"main_group_by_field":{"type":"string"},` +
		`"fields":{"type":"array","items":{"type":"object","properties":{` + viewFieldRef + `,` +
		`"is_visible":{"type":"boolean"},"size":{"type":"number"},"position":{"type":"number"},` +
		`"aggregate_operation":{"type":"string"}},"additionalProperties":false}},` +
		`"filters":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` + viewFieldRef + `,` +
		`"operand":{"type":"string"},` + viewValue + `,"filter_group_id":{"type":"string"},"position":{"type":"number"},` +
		`"sub_field_name":{"type":"string"},"relation_target_field_id":{"type":"string"}},"additionalProperties":false}},` +
		`"filter_groups":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"parent_id":{"type":"string"},"logical_operator":{"type":"string"},"position":{"type":"number"}},` +
		`"additionalProperties":false}},` +
		`"sorts":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` + viewFieldRef + `,` +
		`"direction":{"type":"string"},"sub_field_name":{"type":"string"}},"additionalProperties":false}},` +
		`"groups":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"field_value":{"type":"string"},"field_value_truncated":{"type":"boolean"},"is_visible":{"type":"boolean"},` +
		`"position":{"type":"number"}},"additionalProperties":false}}`
)

var viewsList = capability.Descriptor{
	ID:      Provider + ".views.list",
	Version: 1,
	Title:   "List Twenty CRM views",
	Description: "List one page of the workspace-wide views of the Twenty workspace of a connection without object " +
		"targets, optionally of one object: id, name, object, type, and flags, without the layout. " + viewNote,
	Tags:     []string{"twentycrm", "views", "metadata", "list"},
	Risk:     viewRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"object_id":` +
		uuidSchema + `,"limit":` + workflowLimitSchema + `,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"views":{"type":"array","items":{"type":"object",` +
		`"properties":{` + viewSummaryProps + `},` + viewSummaryRequired + `,"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["views","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of an object whose views are listed; not together with object_id"},
		{Name: "object_id", Description: "UUID of an object whose views are listed, as returned by twenty-crm.meta-objects.list"},
		{Name: "limit", Description: "Views per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "views", Description: "The views on this page; a page may hold fewer than limit, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
	},
	Examples: []capability.Example{{Description: "List the views of the person object", Arguments: json.RawMessage(`{"object":"person"}`)}},
}

var viewsGet = capability.Descriptor{
	ID:      Provider + ".views.get",
	Version: 1,
	Title:   "Get a Twenty CRM view",
	Description: "Read one workspace-wide view of the Twenty workspace of a connection without object targets with its " +
		"layout: columns, filters, filter groups, sorts, and groups. " + viewNote,
	Tags:     []string{"twentycrm", "views", "metadata", "get"},
	Risk:     viewRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + viewSchemaProps + `},` +
		`"required":["id","name","object_metadata_id","type","fields","filters","filter_groups","sorts","groups"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the view, as returned by twenty-crm.views.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "fields", Description: "The columns in the view, at most 200"},
		{Name: "filters", Description: "The filters; values are untrusted data, capped at 500 characters"},
		{Name: "filter_groups", Description: "The nesting of filters by AND, OR, or NOT"},
	},
	Examples: []capability.Example{{
		Description: "Read a view",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

// ViewSummary is a view without its layout.
type ViewSummary struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Object           string  `json:"object,omitempty"`
	ObjectMetadataID string  `json:"object_metadata_id"`
	Type             string  `json:"type"`
	Key              string  `json:"key,omitempty"`
	Icon             string  `json:"icon,omitempty"`
	Position         float64 `json:"position"`
	IsCustom         *bool   `json:"is_custom,omitempty"`
	IsActive         *bool   `json:"is_active,omitempty"`
}

// ViewList is one page of views.
type ViewList struct {
	Views      []ViewSummary `json:"views"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// ViewField is one column of a view.
type ViewField struct {
	FieldID            string  `json:"field_id"`
	Field              string  `json:"field,omitempty"`
	IsVisible          *bool   `json:"is_visible,omitempty"`
	Size               float64 `json:"size"`
	Position           float64 `json:"position"`
	AggregateOperation string  `json:"aggregate_operation,omitempty"`
}

// ViewFilter is one filter of a view. Its value is workspace data.
type ViewFilter struct {
	ID                    string  `json:"id"`
	FieldID               string  `json:"field_id"`
	Field                 string  `json:"field,omitempty"`
	Operand               string  `json:"operand"`
	Value                 string  `json:"value,omitempty"`
	ValueTruncated        bool    `json:"value_truncated,omitempty"`
	FilterGroupID         string  `json:"filter_group_id,omitempty"`
	Position              float64 `json:"position"`
	SubFieldName          string  `json:"sub_field_name,omitempty"`
	RelationTargetFieldID string  `json:"relation_target_field_id,omitempty"`
}

// ViewFilterGroup nests filters by a logical operator.
type ViewFilterGroup struct {
	ID              string  `json:"id"`
	ParentID        string  `json:"parent_id,omitempty"`
	LogicalOperator string  `json:"logical_operator"`
	Position        float64 `json:"position"`
}

// ViewSort is one sort of a view.
type ViewSort struct {
	ID           string `json:"id"`
	FieldID      string `json:"field_id"`
	Field        string `json:"field,omitempty"`
	Direction    string `json:"direction"`
	SubFieldName string `json:"sub_field_name,omitempty"`
}

// ViewGroup is one group value of a grouped view. Its value is workspace data.
type ViewGroup struct {
	ID                  string  `json:"id"`
	FieldValue          string  `json:"field_value,omitempty"`
	FieldValueTruncated bool    `json:"field_value_truncated,omitempty"`
	IsVisible           *bool   `json:"is_visible,omitempty"`
	Position            float64 `json:"position"`
}

// View is a view with its layout. It carries neither the creator nor the application of the view.
type View struct {
	ViewSummary
	IsCompact          *bool             `json:"is_compact,omitempty"`
	AnyFieldFilter     string            `json:"any_field_filter_value,omitempty"`
	MainGroupByFieldID string            `json:"main_group_by_field_id,omitempty"`
	MainGroupByField   string            `json:"main_group_by_field,omitempty"`
	Fields             []ViewField       `json:"fields"`
	Filters            []ViewFilter      `json:"filters"`
	FilterGroups       []ViewFilterGroup `json:"filter_groups"`
	Sorts              []ViewSort        `json:"sorts"`
	Groups             []ViewGroup       `json:"groups"`
}

type viewRecord struct {
	ID                 string          `json:"id"`
	Name               *string         `json:"name"`
	ObjectMetadataID   string          `json:"objectMetadataId"`
	Type               string          `json:"type"`
	Key                *string         `json:"key"`
	Icon               *string         `json:"icon"`
	Position           *float64        `json:"position"`
	IsCompact          *bool           `json:"isCompact"`
	IsCustom           *bool           `json:"isCustom"`
	IsActive           *bool           `json:"isActive"`
	Visibility         string          `json:"visibility"`
	DeletedAt          *string         `json:"deletedAt"`
	AnyFieldFilter     *string         `json:"anyFieldFilterValue"`
	MainGroupByFieldID *string         `json:"mainGroupByFieldMetadataId"`
	ViewFields         json.RawMessage `json:"viewFields"`
	ViewFilters        json.RawMessage `json:"viewFilters"`
	ViewFilterGroups   json.RawMessage `json:"viewFilterGroups"`
	ViewSorts          json.RawMessage `json:"viewSorts"`
	ViewGroups         json.RawMessage `json:"viewGroups"`
}

// listed reports whether the view is read at all. Twenty hides other visibilities from a key without a user
// on the list, but not on the single read, so this check decides.
func (r viewRecord) listed() bool {
	if r.Visibility != "WORKSPACE" {
		return false
	}
	for _, kind := range viewVisibleType {
		if r.Type == kind {
			return true
		}
	}
	return false
}

func number(value *float64) float64 {
	if value == nil || *value < -1e9 || *value > 1e9 {
		return 0
	}
	return *value
}

func (r viewRecord) summary(op string, objects map[string]string) (ViewSummary, error) {
	if !validUUID(r.ID) || !validUUID(r.ObjectMetadataID) {
		return ViewSummary{}, provider.InvalidResponse(op, "Twenty returned a view without a usable identifier")
	}
	summary := ViewSummary{ID: r.ID, Name: metaText(r.Name, metaNameMax), Object: objects[strings.ToLower(r.ObjectMetadataID)],
		ObjectMetadataID: r.ObjectMetadataID, Type: enumToken(viewVisibleType, r.Type), Icon: metaText(r.Icon, metaNameMax),
		Position: number(r.Position), IsCustom: r.IsCustom, IsActive: r.IsActive}
	if r.Key != nil {
		summary.Key = enumToken(viewKeys, *r.Key)
	}
	return summary, nil
}

// viewData cleans a workspace value and cuts it to the cap; the flag tells that it was cut.
func viewData(value string, max int) (string, bool) {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, ""))
	if utf8.RuneCountInString(clean) > max {
		return capLabelTo(clean, max), true
	}
	return clean, false
}

// jsonValueText shows a filter value as text: a string as itself, anything else as compact JSON.
func jsonValueText(raw json.RawMessage) string {
	if isNull(raw) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return ""
	}
	return compact.String()
}

func children(op string, raw json.RawMessage) ([]json.RawMessage, error) {
	items, ok := rawItems(raw)
	if !ok || len(items) > viewChildrenMax {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable or oversized view layout")
	}
	return items, nil
}

func optionalUUID(op string, value *string) (string, error) {
	if value == nil || *value == "" {
		return "", nil
	}
	if !validUUID(*value) {
		return "", provider.InvalidResponse(op, "Twenty returned a view with an unusable identifier")
	}
	return *value, nil
}

func fieldName(fields map[string]string, id string) string {
	return fields[strings.ToLower(id)]
}

// project builds the layout. Entries Twenty reports as deleted are skipped.
func (r viewRecord) project(op string, summary ViewSummary, fields map[string]string) (*View, error) {
	view := &View{ViewSummary: summary, IsCompact: r.IsCompact,
		Fields: []ViewField{}, Filters: []ViewFilter{}, FilterGroups: []ViewFilterGroup{}, Sorts: []ViewSort{}, Groups: []ViewGroup{}}
	view.AnyFieldFilter, _ = viewData(deref(r.AnyFieldFilter), viewValueMax)
	main, err := optionalUUID(op, r.MainGroupByFieldID)
	if err != nil {
		return nil, err
	}
	view.MainGroupByFieldID, view.MainGroupByField = main, fieldName(fields, main)

	items, err := children(op, r.ViewFields)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		var record struct {
			FieldMetadataID    string   `json:"fieldMetadataId"`
			IsVisible          *bool    `json:"isVisible"`
			Size               *float64 `json:"size"`
			Position           *float64 `json:"position"`
			AggregateOperation *string  `json:"aggregateOperation"`
			DeletedAt          *string  `json:"deletedAt"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.FieldMetadataID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view column")
		}
		if record.DeletedAt != nil {
			continue
		}
		field := ViewField{FieldID: record.FieldMetadataID, Field: fieldName(fields, record.FieldMetadataID),
			IsVisible: record.IsVisible, Size: number(record.Size), Position: number(record.Position)}
		field.AggregateOperation = shapedText(record.AggregateOperation, aggregatePattern)
		view.Fields = append(view.Fields, field)
	}

	if items, err = children(op, r.ViewFilters); err != nil {
		return nil, err
	}
	for _, item := range items {
		var record struct {
			ID                    string          `json:"id"`
			FieldMetadataID       string          `json:"fieldMetadataId"`
			Operand               string          `json:"operand"`
			Value                 json.RawMessage `json:"value"`
			ViewFilterGroupID     *string         `json:"viewFilterGroupId"`
			Position              *float64        `json:"positionInViewFilterGroup"`
			SubFieldName          *string         `json:"subFieldName"`
			RelationTargetFieldID *string         `json:"relationTargetFieldMetadataId"`
			DeletedAt             *string         `json:"deletedAt"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) || !validUUID(record.FieldMetadataID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view filter")
		}
		if record.DeletedAt != nil {
			continue
		}
		group, groupErr := optionalUUID(op, record.ViewFilterGroupID)
		target, targetErr := optionalUUID(op, record.RelationTargetFieldID)
		if groupErr != nil || targetErr != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned a view filter with an unusable identifier")
		}
		filter := ViewFilter{ID: record.ID, FieldID: record.FieldMetadataID, Field: fieldName(fields, record.FieldMetadataID),
			Operand: enumToken(viewOperands, record.Operand), FilterGroupID: group, Position: number(record.Position),
			SubFieldName: metaText(record.SubFieldName, viewSubFieldMax), RelationTargetFieldID: target}
		filter.Value, filter.ValueTruncated = viewData(jsonValueText(record.Value), viewValueMax)
		view.Filters = append(view.Filters, filter)
	}

	if items, err = children(op, r.ViewFilterGroups); err != nil {
		return nil, err
	}
	for _, item := range items {
		var record struct {
			ID              string   `json:"id"`
			ParentID        *string  `json:"parentViewFilterGroupId"`
			LogicalOperator string   `json:"logicalOperator"`
			Position        *float64 `json:"positionInViewFilterGroup"`
			DeletedAt       *string  `json:"deletedAt"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view filter group")
		}
		if record.DeletedAt != nil {
			continue
		}
		parent, parentErr := optionalUUID(op, record.ParentID)
		if parentErr != nil {
			return nil, parentErr
		}
		view.FilterGroups = append(view.FilterGroups, ViewFilterGroup{ID: record.ID, ParentID: parent,
			LogicalOperator: enumToken(viewLogicalOperators, record.LogicalOperator), Position: number(record.Position)})
	}

	if items, err = children(op, r.ViewSorts); err != nil {
		return nil, err
	}
	for _, item := range items {
		var record struct {
			ID              string  `json:"id"`
			FieldMetadataID string  `json:"fieldMetadataId"`
			Direction       string  `json:"direction"`
			SubFieldName    *string `json:"subFieldName"`
			DeletedAt       *string `json:"deletedAt"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) || !validUUID(record.FieldMetadataID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view sort")
		}
		if record.DeletedAt != nil {
			continue
		}
		view.Sorts = append(view.Sorts, ViewSort{ID: record.ID, FieldID: record.FieldMetadataID,
			Field: fieldName(fields, record.FieldMetadataID), Direction: enumToken(viewDirections, record.Direction),
			SubFieldName: metaText(record.SubFieldName, viewSubFieldMax)})
	}

	if items, err = children(op, r.ViewGroups); err != nil {
		return nil, err
	}
	for _, item := range items {
		var record struct {
			ID         string   `json:"id"`
			IsVisible  *bool    `json:"isVisible"`
			FieldValue *string  `json:"fieldValue"`
			Position   *float64 `json:"position"`
			DeletedAt  *string  `json:"deletedAt"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view group")
		}
		if record.DeletedAt != nil {
			continue
		}
		group := ViewGroup{ID: record.ID, IsVisible: record.IsVisible, Position: number(record.Position)}
		group.FieldValue, group.FieldValueTruncated = viewData(deref(record.FieldValue), viewValueMax)
		view.Groups = append(view.Groups, group)
	}
	return view, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// objectCatalog maps object identifiers to singular names from the metadata API, page by page up to a bound.
func (c *Client) objectCatalog(ctx context.Context, connection string) (map[string]string, error) {
	names := map[string]string{}
	cursor := ""
	for i := 0; i < viewCatalogPages; i++ {
		page, err := c.ListMetaObjects(ctx, connection, maxPageSize, cursor)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Objects {
			names[strings.ToLower(object.ID)] = object.NameSingular
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return names, nil
}

// ListViews reads one page of workspace-wide views. object filters by singular name, objectID by identifier.
func (c *Client) ListViews(ctx context.Context, connection, object, objectID string, limit int, cursor string) (*ViewList, error) {
	const op = "list views"
	if object != "" && objectID != "" {
		return nil, invalidRequest("give either object or object_id, not both")
	}
	if objectID != "" && !validUUID(objectID) {
		return nil, invalidRequest("object_id must be a UUID")
	}
	binding := provider.CursorBinding("views.list", connection, strings.ToLower(object), strings.ToLower(objectID))
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	if after != "" && !validUUID(after) {
		return nil, invalidRequest("cursor is not a next_cursor of this request; start again without cursor")
	}
	objects, catalogErr := c.objectCatalog(ctx, connection)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if object != "" {
		if catalogErr != nil {
			return nil, catalogErr
		}
		for id, name := range objects {
			if name == object {
				objectID = id
			}
		}
		if objectID == "" {
			return nil, invalidRequest("the object is not known in this workspace")
		}
	}
	values := url.Values{"limit": {strconv.Itoa(limit)}}
	if objectID != "" {
		values.Set("objectMetadataId", objectID)
	}
	if after != "" {
		values.Set("starting_after", after)
	}
	var page metaPage
	if err := c.getWith(ctx, op, viewsPath, values, viewResponseBytes, &page, viewStatusTexts); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data, &items) != nil || items == nil || len(items) > limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of views")
	}
	result := &ViewList{Views: make([]ViewSummary, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		var record viewRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable view")
		}
		if !record.listed() {
			continue
		}
		summary, err := record.summary(op, objects)
		if err != nil {
			return nil, err
		}
		result.Views = append(result.Views, summary)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

// readView reads one workspace-wide view. A view of another visibility or type reads exactly like an unknown one.
func (c *Client) readView(ctx context.Context, op, id string, texts provider.StatusTexts) (*viewRecord, error) {
	var body json.RawMessage
	if err := c.getWith(ctx, op, viewsPath+"/"+url.PathEscape(id), nil, viewResponseBytes, &body, texts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "view")
	if err != nil {
		return nil, err
	}
	var record viewRecord
	if json.Unmarshal(entity, &record) != nil || !equalUUID(record.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view than the requested one")
	}
	if !record.listed() {
		// Twenty serves a view of any visibility by identifier; it must read exactly like an unknown one.
		return nil, provider.ClassifyStatus(op, 404, texts)
	}
	return &record, nil
}

// GetView reads one workspace-wide view with its layout.
func (c *Client) GetView(ctx context.Context, connection, id string) (*View, error) {
	const op = "get view"
	if !validUUID(id) {
		return nil, invalidRequest("the view id must be a UUID")
	}
	record, err := c.readView(ctx, op, id, viewStatusTexts)
	if err != nil {
		return nil, err
	}
	objects, fields := map[string]string{}, map[string]string{}
	if validUUID(record.ObjectMetadataID) {
		object, objectErr := c.GetMetaObject(ctx, record.ObjectMetadataID)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if objectErr == nil {
			objects[strings.ToLower(object.ID)] = object.NameSingular
			for _, field := range object.Fields {
				fields[strings.ToLower(field.ID)] = field.Name
			}
		}
	}
	summary, err := record.summary(op, objects)
	if err != nil {
		return nil, err
	}
	return record.project(op, summary, fields)
}

func invokeViewsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Object   string `json:"object"`
		ObjectID string `json:"object_id"`
		Limit    int    `json:"limit"`
		Cursor   string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list views", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if args.Object != "" && args.ObjectID != "" {
		return nil, invalidRequest("give either object or object_id, not both")
	}
	if args.Object != "" && !objectNamePattern.MatchString(args.Object) {
		return nil, invalidRequest("object must be a singular API name")
	}
	if args.ObjectID != "" && !validUUID(args.ObjectID) {
		return nil, invalidRequest("object_id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListViews(ctx, resolved.Name, args.Object, args.ObjectID, args.Limit, args.Cursor)
}

func invokeViewsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get view", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the view id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetView(ctx, resolved.Name, args.ID)
}
