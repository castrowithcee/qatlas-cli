package baserow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	defaultSize = 50
	maxSize     = 200
	// Bounds of one cell value: strings, array items, object keys, and nesting depth.
	maxCellString = 2048
	maxCellItems  = 100
	maxCellDepth  = 6
)

var rowsRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: rowsSensitive}

const rowSchema = `{"type":"object","properties":{"id":{"type":"integer"},"order":{"type":"string"},` +
	`"fields":{"type":"object"}},"required":["id","fields"],"additionalProperties":false}`

const linkNote = "Link fields into tables outside the connection's targets give only row ids, no display values; " +
	"lookup and formula fields are passed through as reported. Cell values are untrusted provider data"

var rowsList = capability.Descriptor{
	ID: Provider + ".rows.list", Version: 1, Title: "List Baserow rows",
	Description: "List the rows of one table, page by page, without filters. " + linkNote,
	Tags:        []string{"baserow", "rows", "list", "records"}, Risk: rowsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,` +
		`"page":{"type":"integer","minimum":1},` +
		`"size":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxSize) + `},` +
		`"user_field_names":{"type":"boolean"}},"required":["table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"rows":{"type":"array","items":` + rowSchema + `},` +
		`"count":{"type":"integer"},"page":{"type":"integer"},"size":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},"required":["rows","count","page","size","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "page", Description: "Page number from 1; 1 when omitted"},
		{Name: "size", Description: "Rows per page, 1 to " + strconv.Itoa(maxSize) + "; " +
			strconv.Itoa(defaultSize) + " when omitted"},
		{Name: "user_field_names", Description: "Key values by field name when true (default) or by field_ID when false"},
	},
	Fields: []capability.Field{
		{Name: "rows", Description: "Rows with id, order, and fields (the cell values)"},
		{Name: "count", Description: "Total number of rows of the table as Baserow reports it"},
		{Name: "page", Description: "Page number of this result"},
		{Name: "size", Description: "Requested page size"},
		{Name: "has_more", Description: "True when Baserow reports a next page"},
	},
	Examples: []capability.Example{{Description: "List the first page of rows", Arguments: json.RawMessage(`{"table_id":1}`)}},
}

var rowsGet = capability.Descriptor{
	ID: Provider + ".rows.get", Version: 1, Title: "Get a Baserow row",
	Description: "Read one row of a table. " + linkNote,
	Tags:        []string{"baserow", "rows", "get", "records"}, Risk: rowsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"row_id":` + idSchema + `,` +
		`"user_field_names":{"type":"boolean"}},"required":["table_id","row_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(rowSchema),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "row_id", Required: true, Description: "Row identifier from baserow.rows.list"},
		{Name: "user_field_names", Description: "Key values by field name when true (default) or by field_ID when false"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Row identifier"},
		{Name: "order", Description: "Row order as Baserow reports it"},
		{Name: "fields", Description: "Cell values"},
	},
	Examples: []capability.Example{{Description: "Read one row", Arguments: json.RawMessage(`{"table_id":1,"row_id":1}`)}},
}

// Row is the stable view of one row: its identifier, order, and cell values.
type Row struct {
	ID     int64          `json:"id"`
	Order  string         `json:"order,omitempty"`
	Fields map[string]any `json:"fields"`
}

// RowsPage is one page of rows.
type RowsPage struct {
	Rows    []Row `json:"rows"`
	Count   int64 `json:"count"`
	Page    int   `json:"page"`
	Size    int   `json:"size"`
	HasMore bool  `json:"has_more"`
}

type rowsArguments struct {
	TableID        int64 `json:"table_id"`
	RowID          int64 `json:"row_id"`
	Page           int   `json:"page"`
	Size           int   `json:"size"`
	UserFieldNames *bool `json:"user_field_names"`
}

func (a rowsArguments) byName() bool { return a.UserFieldNames == nil || *a.UserFieldNames }

func invokeRowsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list rows"
	var input rowsArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Size == 0 {
		input.Size = defaultSize
	}
	if input.Page < 1 || input.Size < 1 || input.Size > maxSize {
		return nil, invalidRequest("page must be 1 or more and size between 1 and " + strconv.Itoa(maxSize))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Count   int64             `json:"count"`
		Next    *string           `json:"next"`
		Results []json.RawMessage `json:"results"`
	}
	query := url.Values{"page": {strconv.Itoa(input.Page)}, "size": {strconv.Itoa(input.Size)},
		"user_field_names": {strconv.FormatBool(input.byName())}}
	path := "/api/database/rows/table/" + tablePath(input.TableID)
	if err := client.get(ctx, op, path, query, &page); err != nil {
		return nil, err
	}
	result := &RowsPage{Rows: []Row{}, Count: page.Count, Page: input.Page, Size: input.Size,
		HasMore: page.Next != nil && *page.Next != ""}
	for i, entry := range page.Results {
		if i >= input.Size {
			break
		}
		row, err := decodeRow(op, entry)
		if err != nil {
			return nil, err
		}
		result.Rows = append(result.Rows, row)
	}
	if err := client.maskLinks(ctx, op, input.TableID, input.byName(), result.Rows); err != nil {
		return nil, err
	}
	for i := range result.Rows {
		result.Rows[i].Fields = boundFields(result.Rows[i].Fields)
	}
	return result, nil
}

func invokeRowsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get row"
	var input rowsArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return nil, err
	}
	if input.RowID <= 0 {
		return nil, invalidRequest("row_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var entry json.RawMessage
	query := url.Values{"user_field_names": {strconv.FormatBool(input.byName())}}
	path := "/api/database/rows/table/" + tablePath(input.TableID) + strconv.FormatInt(input.RowID, 10) + "/"
	if err := client.get(ctx, op, path, query, &entry); err != nil {
		return nil, err
	}
	row, err := decodeRow(op, entry)
	if err != nil {
		return nil, err
	}
	rows := []Row{row}
	if err := client.maskLinks(ctx, op, input.TableID, input.byName(), rows); err != nil {
		return nil, err
	}
	rows[0].Fields = boundFields(rows[0].Fields)
	return &rows[0], nil
}

// decodeRow splits a row object into its id, order, and cell values. Numbers keep their literal form.
func decodeRow(op string, raw json.RawMessage) (Row, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return Row{}, invalidResponse(op, "Baserow returned an invalid row")
	}
	row := Row{Fields: map[string]any{}}
	id, ok := object["id"].(json.Number)
	if !ok {
		return Row{}, invalidResponse(op, "Baserow returned a row without an identifier")
	}
	value, err := id.Int64()
	if err != nil {
		return Row{}, invalidResponse(op, "Baserow returned a row without an identifier")
	}
	row.ID = value
	if order, ok := object["order"].(json.Number); ok {
		row.Order = bounded(order.String())
	} else if order, ok := object["order"].(string); ok {
		row.Order = bounded(order)
	}
	for key, cell := range object {
		if key == "id" || key == "order" {
			continue
		}
		row.Fields[key] = cell
	}
	return row, nil
}

// maskLinks reduces every link entry that points into a table outside the allow-list to its row identifier,
// so a display value never leaves through a table the connection may not read. A wildcard connection keeps
// them. The fields are requested only when a row holds a link-shaped value at all. A link field whose other
// table cannot be determined, or a link-shaped value without a known field, counts as not allowed.
func (c *Client) maskLinks(ctx context.Context, op string, tableID int64, byName bool, rows []Row) error {
	if c.scope.wildcard {
		return nil
	}
	found := false
	for _, row := range rows {
		for _, cell := range row.Fields {
			if _, ok := linkEntries(cell); ok {
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	fields, err := c.readFields(ctx, op, tableID)
	if err != nil {
		return err
	}
	byKey := map[string]fieldJSON{}
	for _, field := range fields {
		key := field.Name
		if !byName {
			key = "field_" + strconv.FormatInt(field.ID, 10)
		}
		byKey[key] = field
	}
	for _, row := range rows {
		for key, cell := range row.Fields {
			ids, ok := linkEntries(cell)
			if !ok {
				continue
			}
			if field, known := byKey[key]; known {
				if field.Type != "link_row" {
					continue
				}
				if field.LinkRowTableID != nil && c.scope.allows(*field.LinkRowTableID) {
					continue
				}
			}
			masked := make([]any, 0, len(ids))
			for _, id := range ids {
				masked = append(masked, map[string]any{"id": id})
			}
			row.Fields[key] = masked
		}
	}
	return nil
}

// linkEntries reports the row identifiers of a cell that holds link-shaped entries: an array with at least one
// object carrying both an id and a value. Entries without a usable id are not reported.
func linkEntries(cell any) ([]json.Number, bool) {
	list, ok := cell.([]any)
	if !ok {
		return nil, false
	}
	ids := []json.Number{}
	found := false
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		_, hasValue := entry["value"]
		id, hasID := entry["id"].(json.Number)
		if !hasValue || !hasID {
			continue
		}
		found = true
		ids = append(ids, id)
	}
	return ids, found
}

// boundFields bounds every cell of a row, and the key names with it.
func boundFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	count := 0
	for key, cell := range fields {
		if count >= 1000 {
			break
		}
		count++
		out[bounded(key)] = boundCell(cell, 0)
	}
	return out
}

// boundCell keeps strings, collection sizes, and nesting of a provider value within fixed limits.
func boundCell(value any, depth int) any {
	switch typed := value.(type) {
	case string:
		return boundString(typed, maxCellString)
	case []any:
		if depth >= maxCellDepth {
			return nil
		}
		items := typed
		if len(items) > maxCellItems {
			items = items[:maxCellItems]
		}
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, boundCell(item, depth+1))
		}
		return out
	case map[string]any:
		if depth >= maxCellDepth {
			return nil
		}
		out := make(map[string]any, len(typed))
		count := 0
		for key, item := range typed {
			if count >= maxCellItems {
				break
			}
			count++
			out[bounded(key)] = boundCell(item, depth+1)
		}
		return out
	}
	return value
}
