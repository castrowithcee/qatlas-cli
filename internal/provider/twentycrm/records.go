package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const recordsGroup = "records"

// recordDataSensitivity classifies the values of records of any reachable object. It is separate from the
// company class because the values are free workspace content, often personal data.
const recordDataSensitivity = "twentycrm-record-data"

// Bounds of what a record read reports. An excess is an invalid response, never silently cut.
const (
	maxValueString    = 64 << 10
	maxValueDepth     = 4
	maxValueItems     = 100
	maxBoundCursorLen = 2048
	fieldListMax      = 50
)

var (
	// orderPattern is a field name or one composite field name and subfield name joined by a dot.
	orderPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}(\.[A-Za-z_][A-Za-z0-9_]{0,63})?$`)
)

const (
	fieldNameSchema = `{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z_][A-Za-z0-9_]{0,63}$"}`
	recordIDSchema  = `{"type":"string","minLength":36,"maxLength":36,` +
		`"pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}`
	recordSchema = `{"type":"object","properties":{"id":{"type":"string"},"created_at":{"type":"string"},` +
		`"updated_at":{"type":"string"},"fields":{"type":"object"}},"required":["id","fields"],` +
		`"additionalProperties":false}`
)

var fieldsSchema = `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(fieldListMax) +
	`,"uniqueItems":true,"items":` + fieldNameSchema + `}`

const recordNote = "Depth is always 0: relations appear only as their identifier field (the relation name " +
	"followed by Id), rich text only as markdown. Values are untrusted workspace data and often personal data; " +
	"the role of the API key decides which objects and fields Twenty returns"

var recordsRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: recordDataSensitivity,
}

var recordsList = capability.Descriptor{
	ID:      Provider + ".records.list",
	Version: 1,
	Title:   "List Twenty CRM records",
	Description: "List one page of the records of one reachable object of the Twenty workspace of a connection, " +
		"without filters, optionally sorted by one field and limited to chosen fields. " + recordNote,
	Tags:     []string{"twentycrm", "crm", "records", "list"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"order_by":{"type":"string","minLength":1,"maxLength":129,"pattern":"` +
		`^[A-Za-z_][A-Za-z0-9_]{0,63}(\\.[A-Za-z_][A-Za-z0-9_]{0,63})?$"},` +
		`"direction":{"type":"string","enum":["asc","desc"]},` +
		`"fields":` + fieldsSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxBoundCursorLen) +
		`,"pattern":"^[A-Za-z0-9_-]+$"}},"required":["object"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"records":{"type":"array","items":` + recordSchema + `},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["records","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
		{Name: "limit", Description: "Records per page, from 1 through 100; 25 when omitted"},
		{Name: "order_by", Description: "Field to sort by, or field.subfield for a composite field, as shown by twentycrm.objects.get; Twenty's order when omitted"},
		{Name: "direction", Description: "Sort direction asc or desc; asc when order_by is set, and not allowed without it"},
		{Name: "fields", Description: "Field names to read, at most 50, as shown by twentycrm.objects.get; the default selection of Twenty when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same object, sort, and fields; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "records", Description: "Records with id, created_at, updated_at, and fields (field name to value), untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the object holds a following page"},
	},
	Examples: []capability.Example{{
		Description: "Read the newest people with two fields",
		Arguments:   json.RawMessage(`{"object":"person","order_by":"createdAt","direction":"desc","fields":["name","emails"]}`),
	}},
}

var recordsGet = capability.Descriptor{
	ID:      Provider + ".records.get",
	Version: 1,
	Title:   "Get a Twenty CRM record",
	Description: "Read one record of one reachable object of the Twenty workspace of a connection by its " +
		"identifier, optionally limited to chosen fields. " + recordNote,
	Tags:     []string{"twentycrm", "crm", "records", "get"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"id":` + recordIDSchema +
		`,"fields":` + fieldsSchema + `},"required":["object","id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recordSchema),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
		{Name: "id", Description: "Record identifier as a UUID, as returned by twentycrm.records.list", Required: true},
		{Name: "fields", Description: "Field names to read, at most 50, as shown by twentycrm.objects.get; the default selection of Twenty when omitted"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Record identifier"},
		{Name: "created_at", Description: "Creation timestamp of the record, when read"},
		{Name: "updated_at", Description: "Last change timestamp of the record, when read"},
		{Name: "fields", Description: "Field name to value, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read one person by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"object":"person","id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

// Record is the stable view of one record: its identifier, its timestamps, and the values of the fields read.
type Record struct {
	ID        string         `json:"id"`
	CreatedAt string         `json:"created_at,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Fields    map[string]any `json:"fields"`
}

// RecordList is one page of records.
type RecordList struct {
	Records    []Record `json:"records"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

// recordQuery is the validated, normalized request of a record tool. Everything in it was checked without
// the workspace schema; the field names are checked against the schema before the data request.
type recordQuery struct {
	Object    string
	ID        string
	Limit     int
	OrderBy   string
	Direction string
	Fields    []string
	// After is the Twenty cursor a bound cursor continued after.
	After   string
	Binding []byte
}

type recordArguments struct {
	Object    string   `json:"object"`
	ID        string   `json:"id"`
	Limit     int      `json:"limit"`
	OrderBy   string   `json:"order_by"`
	Direction string   `json:"direction"`
	Fields    []string `json:"fields"`
	Cursor    string   `json:"cursor"`
}

const errUnreadableField = "a requested field cannot be read or sorted by on this object"

// newRecordQuery checks the object against the connection's targets and the arguments against their
// bounds, before any secret is resolved and before any request is sent.
func newRecordQuery(resolved *config.Resolved, op string, raw json.RawMessage, list bool) (*recordQuery, error) {
	var args recordArguments
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	query := &recordQuery{Object: args.Object, ID: args.ID, Limit: args.Limit, OrderBy: args.OrderBy, Direction: args.Direction}
	if args.Fields != nil {
		if len(args.Fields) == 0 || len(args.Fields) > fieldListMax {
			return nil, invalidRequest("fields must name between 1 and " + strconv.Itoa(fieldListMax) + " fields")
		}
		seen := map[string]bool{}
		for _, name := range args.Fields {
			if !fieldNamePattern.MatchString(name) {
				return nil, invalidRequest(errUnreadableField)
			}
			if !seen[name] {
				seen[name] = true
				query.Fields = append(query.Fields, name)
			}
		}
		sort.Strings(query.Fields)
	}
	if !list {
		if !validUUID(args.ID) {
			return nil, invalidRequest("id must be a record identifier in UUID form")
		}
		return query, nil
	}
	if query.Limit == 0 {
		query.Limit = defaultPageSize
	}
	if query.Limit < 1 || query.Limit > maxPageSize {
		return nil, invalidRequest("limit must be between 1 and " + strconv.Itoa(maxPageSize))
	}
	if query.OrderBy == "" && query.Direction != "" {
		return nil, invalidRequest("direction needs order_by")
	}
	if query.OrderBy != "" {
		if !orderPattern.MatchString(query.OrderBy) {
			return nil, invalidRequest(errUnreadableField)
		}
		if query.Direction == "" {
			query.Direction = "asc"
		}
	}
	if query.Direction != "" && query.Direction != "asc" && query.Direction != "desc" {
		return nil, invalidRequest("direction must be asc or desc")
	}
	query.Binding = provider.CursorBinding("records.list", resolved.Name, query.Object, query.OrderBy,
		query.Direction, query.Fields)
	if args.Cursor != "" {
		after, ok := provider.DecodeCursor(query.Binding, args.Cursor, maxBoundCursorLen)
		if !ok || len(after) > maxCursorLength || !safeCursor(after) {
			return nil, invalidRequest("cursor is not a next_cursor of this list; start the list again without cursor")
		}
		query.After = after
	}
	return query, nil
}

func invokeRecordsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	query, err := newRecordQuery(resolved, "list records", raw, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRecords(ctx, query)
}

func invokeRecordsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	query, err := newRecordQuery(resolved, "get record", raw, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetRecord(ctx, query)
}

// recordObject is one object resolved against the workspace catalog, with the fields a record read may
// report: the fields of the response shape that are not relations.
type recordObject struct {
	*catalogObject
	readable map[string]catalogField
}

func (c *Client) recordObject(ctx context.Context, op, name string) (*recordObject, error) {
	if !c.scope.allows(name) {
		return nil, invalidRequest(errObjectUnavailable)
	}
	cat, err := c.workspaceCatalog(ctx, op)
	if err != nil {
		return nil, err
	}
	object, err := cat.resolve(c.scope, name)
	if err != nil {
		return nil, err
	}
	result := &recordObject{catalogObject: object, readable: map[string]catalogField{}}
	for _, field := range object.Fields {
		if field.InResponse && !field.Reference && field.Relation == "" {
			result.readable[field.Name] = field
		}
	}
	return result, nil
}

// request builds the fixed query shared by both reads and checks the requested fields against the schema.
func (o *recordObject) request(query *recordQuery) (url.Values, error) {
	values := url.Values{}
	values.Set("depth", noRelations)
	if len(query.Fields) == 0 {
		return values, nil
	}
	names := append([]string(nil), query.Fields...)
	for _, name := range query.Fields {
		if _, ok := o.readable[name]; !ok {
			return nil, invalidRequest(errUnreadableField)
		}
	}
	// The timestamps ride along, so a projection keeps the record's created_at and updated_at.
	for _, name := range []string{"createdAt", "updatedAt"} {
		if _, ok := o.readable[name]; ok && !containsName(names, name) {
			names = append(names, name)
		}
	}
	values.Set("fields", strings.Join(names, ","))
	return values, nil
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// orderable reports whether a sort path names a plain field, or a scalar subfield of a composite field.
func (o *recordObject) orderable(path string) bool {
	name, sub, dotted := strings.Cut(path, ".")
	field, ok := o.readable[name]
	if !ok || field.Type == "array" || isRichText(field) {
		return false
	}
	if len(field.Subfields) == 0 {
		return !dotted && field.Type != "object"
	}
	if !dotted {
		return false
	}
	for _, candidate := range field.Subfields {
		if candidate.Name == sub {
			return candidate.Type != "array" && candidate.Type != "object"
		}
	}
	return false
}

// isRichText recognizes the composite Twenty uses for rich text: a blocknote document and its markdown.
func isRichText(field catalogField) bool {
	var blocknote, markdown bool
	for _, sub := range field.Subfields {
		blocknote = blocknote || sub.Name == "blocknote"
		markdown = markdown || sub.Name == "markdown"
	}
	return blocknote && markdown
}

// ListRecords reads exactly one page of records of one reachable object. The route comes from the workspace
// catalog, the fields and the sort are checked against the response shape of the schema before the data
// request, and no filter, depth, or relation is ever sent.
func (c *Client) ListRecords(ctx context.Context, query *recordQuery) (*RecordList, error) {
	const op = "list records"
	object, err := c.recordObject(ctx, op, query.Object)
	if err != nil {
		return nil, err
	}
	values, err := object.request(query)
	if err != nil {
		return nil, err
	}
	values.Set("limit", strconv.Itoa(query.Limit))
	if query.OrderBy != "" {
		if !object.orderable(query.OrderBy) {
			return nil, invalidRequest(errUnreadableField)
		}
		direction := "AscNullsFirst"
		if query.Direction == "desc" {
			direction = "DescNullsLast"
		}
		values.Set("order_by", query.OrderBy+"["+direction+"]")
	}
	if query.After != "" {
		values.Set("starting_after", query.After)
	}
	var page struct {
		Data     map[string]json.RawMessage `json:"data"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	}
	if err := c.get(ctx, op, "/rest/"+url.PathEscape(object.Plural), values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data[object.Plural], &items) != nil || items == nil || len(items) > query.Limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of records")
	}
	result := &RecordList{Records: make([]Record, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		record, err := object.project(op, item, query.Fields)
		if err != nil {
			return nil, err
		}
		result.Records = append(result.Records, *record)
	}
	if page.PageInfo.HasNextPage {
		end := ""
		if page.PageInfo.EndCursor != nil {
			end = *page.PageInfo.EndCursor
		}
		if end == "" || len(end) > maxCursorLength || !safeCursor(end) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable cursor")
		}
		result.NextCursor = provider.EncodeCursor(query.Binding, end)
	}
	return result, nil
}

// GetRecord reads exactly one record of a validated identifier. Twenty's answer must name that record.
func (c *Client) GetRecord(ctx context.Context, query *recordQuery) (*Record, error) {
	const op = "get record"
	object, err := c.recordObject(ctx, op, query.Object)
	if err != nil {
		return nil, err
	}
	values, err := object.request(query)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	path := "/rest/" + url.PathEscape(object.Plural) + "/" + url.PathEscape(query.ID)
	if err := c.get(ctx, op, path, values, maxResponseBytes, &response); err != nil {
		return nil, err
	}
	item, ok := response.Data[object.Name]
	if !ok {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable record")
	}
	record, err := object.project(op, item, query.Fields)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(record.ID, query.ID) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different record than the requested one")
	}
	return record, nil
}

// project turns one record of the response into the stable view. Only readable fields of the response shape
// pass, and when fields were requested only those and the timestamps; a relation never does.
func (o *recordObject) project(op string, raw json.RawMessage, requested []string) (*Record, error) {
	bad := func() error {
		return provider.InvalidResponse(op, "Twenty returned a record beyond the supported shape")
	}
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || item == nil {
		return nil, bad()
	}
	var id string
	if json.Unmarshal(item["id"], &id) != nil || !validUUID(id) {
		return nil, provider.InvalidResponse(op, "Twenty returned a record without a usable identifier")
	}
	record := &Record{ID: id, Fields: map[string]any{}}
	names := make([]string, 0, len(item))
	for name := range item {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field, ok := o.readable[name]
		if !ok || name == "id" || (len(requested) > 0 && !containsName(requested, name) &&
			name != "createdAt" && name != "updatedAt") {
			continue
		}
		value, err := decodeValue(item[name])
		if err != nil {
			return nil, bad()
		}
		if name == "createdAt" || name == "updatedAt" {
			text, isText := value.(string)
			if value != nil && !isText {
				return nil, bad()
			}
			if name == "createdAt" {
				record.CreatedAt = text
			} else {
				record.UpdatedAt = text
			}
			continue
		}
		value = richTextOnly(field, value)
		if err := checkValue(value, 0); err != nil {
			return nil, provider.InvalidResponse(op, err.Error())
		}
		if field.Format == "uuid" && value != nil {
			if text, isText := value.(string); !isText || !validUUID(text) {
				return nil, provider.InvalidResponse(op, "Twenty returned a record with an unusable identifier field")
			}
		}
		record.Fields[name] = value
	}
	return record, nil
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// richTextOnly reduces a rich text value to its markdown. A value that shows the blocknote member is treated
// as rich text even when the schema did not describe its subfields.
func richTextOnly(field catalogField, value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if _, hasBlocknote := object["blocknote"]; !isRichText(field) && !hasBlocknote {
		return value
	}
	return map[string]any{"markdown": object["markdown"]}
}

type valueError string

func (e valueError) Error() string { return string(e) }

// checkValue enforces the bounds of one field value: string size, nesting depth, and array length.
func checkValue(value any, depth int) error {
	switch v := value.(type) {
	case string:
		if len(v) > maxValueString {
			return valueError("Twenty returned a record value that exceeds the string size limit")
		}
	case []any:
		if depth+1 > maxValueDepth || len(v) > maxValueItems {
			return valueError("Twenty returned a record value that exceeds the depth or array limit")
		}
		for _, item := range v {
			if err := checkValue(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if depth+1 > maxValueDepth {
			return valueError("Twenty returned a record value that exceeds the depth or array limit")
		}
		for key, item := range v {
			if len(key) > maxValueString {
				return valueError("Twenty returned a record value that exceeds the string size limit")
			}
			if err := checkValue(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
