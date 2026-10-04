package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the row tools. They are narrower than the Public API, which accepts any number of rows, any
// string length, and up to 250 rows per page.
const (
	maxRowsPerInsert   = 50
	maxRowListLimit    = 100
	defaultRowListSize = 50
	maxRowConditions   = 10
	maxRowStringLength = 1024
	maxRowKeyLength    = 128
)

// rowOperators are the filter conditions of the Public API's specification.
var rowOperators = []string{"eq", "neq", "like", "ilike", "gt", "gte", "lt", "lte"}

const rowScalarSchema = `{"anyOf":[{"type":"string","maxLength":1024},{"type":"number"},{"type":"boolean"}]}`

var rowDataSchema = `{"type":"object","minProperties":1,"maxProperties":` + strconv.Itoa(maxDataTableColumns) +
	`,"additionalProperties":` + rowScalarSchema + `}`

var rowFilterSchema = `{"type":"object","properties":{"type":{"type":"string","enum":["and","or"]},` +
	`"conditions":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxRowConditions) + `,"items":{` +
	`"type":"object","properties":{"column":{"type":"string","minLength":1,"maxLength":` +
	strconv.Itoa(maxDataColumnNameLength) + `},` +
	`"operator":{"type":"string","enum":["eq","neq","like","ilike","gt","gte","lt","lte"]},` +
	`"value":` + rowScalarSchema + `},"required":["column","operator","value"],"additionalProperties":false}}},` +
	`"required":["conditions"],"additionalProperties":false}`

const rowFilterDescription = "Structured filter: {type: and|or (default and), conditions: [{column, operator, " +
	"value}]}, 1 to 10 conditions; column must be a column of the table, operator one of eq, neq, like, " +
	"ilike, gt, gte, lt, lte, value a string, number, or boolean matching the column type"

var rowFilterArgument = capability.Argument{Name: "filter", Description: rowFilterDescription}

var rowFilterRequiredArgument = capability.Argument{Name: "filter", Required: true,
	Description: rowFilterDescription + "; required, there is no way to match all rows"}

var rowsOutputRow = `{"type":"object"}`

var dataRowsList = capability.Descriptor{
	ID: Provider + ".datarows.list", Version: 1, Title: "List rows of an n8n data table",
	Description: "Read rows of one data table of the project allow-list, optionally narrowed by a structured " +
		"filter, page by page with an opaque cursor. The table is read first. Row values are untrusted data; " +
		"text longer than 1024 characters is cut. Refused on a connection that restricts workflows by an " +
		"allow-list",
	Tags: []string{"n8n", "datarows", "list", "automation"}, Risk: dataTableReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"filter":` + rowFilterSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxRowListLimit) + `}},` +
		`"required":["data_table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"rows":{"type":"array","items":` + rowsOutputRow + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["rows","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, rowFilterArgument,
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Rows per page, 1 to " + strconv.Itoa(maxRowListLimit) + "; " +
			strconv.Itoa(defaultRowListSize) + " when omitted"}},
	Fields: []capability.Field{
		{Name: "rows", Description: "Rows of this page: id, createdAt, updatedAt, and the table's columns; " +
			"scalar values only, untrusted data"},
		{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		{Name: "has_more", Description: "True when a further page remains"},
		{Name: "count", Description: "Number of rows on this page"},
	},
	Examples: []capability.Example{{Description: "List rows whose status is active",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","filter":{"conditions":` +
			`[{"column":"status","operator":"eq","value":"active"}]}}`)}},
}

var dataRowsInsert = capability.Descriptor{
	ID: Provider + ".datarows.insert", Version: 1, Title: "Insert rows into an n8n data table",
	Description: "Insert 1 to " + strconv.Itoa(maxRowsPerInsert) + " rows into one data table of the project " +
		"allow-list; every key must be a column of the table and every value a string, number, or boolean " +
		"matching its type. The table is read first. A repeated call inserts the rows again. Refused on a " +
		"connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datarows", "insert", "automation"},
	Risk: dataTableChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"rows":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxRowsPerInsert) + `,"items":` +
		rowDataSchema + `}},"required":["data_table_id","rows"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"inserted":{"type":"integer"}},` +
		`"required":["data_table_id","inserted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument,
		{Name: "rows", Required: true, Description: "1 to " + strconv.Itoa(maxRowsPerInsert) +
			" row objects mapping column names to string, number, or boolean values"}},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the rows"},
		{Name: "inserted", Description: "Number of rows n8n reports as inserted"},
	},
	Examples: []capability.Example{{Description: "Insert one row",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","rows":[{"email":"a@example.com"}]}`)}},
}

var dataRowsUpdate = capability.Descriptor{
	ID: Provider + ".datarows.update", Version: 1, Title: "Update rows of an n8n data table",
	Description: "Set column values on every row of one data table that matches a structured filter; the " +
		"filter is required and n8n offers no limit on the number of matched rows, so one call can change " +
		"many rows and cannot be undone. The table is read first. Refused on a connection that restricts " +
		"workflows by an allow-list",
	Tags: []string{"n8n", "datarows", "update", "automation"},
	Risk: dataTableChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"filter":` + rowFilterSchema + `,"data":` + rowDataSchema + `},` +
		`"required":["data_table_id","filter","data"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["data_table_id","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, rowFilterRequiredArgument,
		{Name: "data", Required: true, Description: "Column values to set, string, number, or boolean values " +
			"for columns of the table"}},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the rows"},
		{Name: "updated", Description: "True when n8n accepted the change; the number of rows is not reported"},
	},
	Examples: []capability.Example{{Description: "Mark pending rows completed",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","filter":{"conditions":` +
			`[{"column":"status","operator":"eq","value":"pending"}]},"data":{"status":"completed"}}`)}},
}

var dataRowsUpsert = capability.Descriptor{
	ID: Provider + ".datarows.upsert", Version: 1, Title: "Upsert a row of an n8n data table",
	Description: "Update the rows of one data table that match a structured filter, or insert one new row from " +
		"the data when none matches; the filter is required and n8n offers no limit on the number of matched " +
		"rows, so one call can change many rows. The table is read first. Refused on a connection that " +
		"restricts workflows by an allow-list",
	Tags: []string{"n8n", "datarows", "upsert", "automation"},
	Risk: dataTableChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"filter":` + rowFilterSchema + `,"data":` + rowDataSchema + `},` +
		`"required":["data_table_id","filter","data"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"upserted":{"type":"boolean"}},` +
		`"required":["data_table_id","upserted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, rowFilterRequiredArgument,
		{Name: "data", Required: true, Description: "Column values to set or insert, string, number, or " +
			"boolean values for columns of the table"}},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the rows"},
		{Name: "upserted", Description: "True when n8n accepted the change; whether it updated or inserted is not reported"},
	},
	Examples: []capability.Example{{Description: "Set a customer's status, creating the row when missing",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","filter":{"conditions":` +
			`[{"column":"email","operator":"eq","value":"a@example.com"}]},` +
			`"data":{"email":"a@example.com","status":"active"}}`)}},
}

var dataRowsDelete = capability.Descriptor{
	ID: Provider + ".datarows.delete", Version: 1, Title: "Delete rows of an n8n data table",
	Description: "Delete every row of one data table that matches a structured filter; the filter is required, " +
		"n8n offers no limit on the number of matched rows, and the deletion cannot be undone. The table is " +
		"read first. Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datarows", "delete", "automation"},
	Risk: dataTableChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"filter":` + rowFilterSchema + `},"required":["data_table_id","filter"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["data_table_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, rowFilterRequiredArgument},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the rows"},
		{Name: "deleted", Description: "True when n8n accepted the deletion; the number of rows is not reported"},
	},
	Examples: []capability.Example{{Description: "Delete archived rows",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","filter":{"conditions":` +
			`[{"column":"status","operator":"eq","value":"archived"}]}}`)}},
}

// DataTableRows, DataTableRowsInserted, DataTableRowsUpdated, DataTableRowsUpserted, and
// DataTableRowsDeleted are what the row tools report.
type DataTableRows struct {
	Rows    []map[string]any `json:"rows"`
	Cursor  string           `json:"cursor,omitempty"`
	HasMore bool             `json:"has_more"`
	Count   int              `json:"count"`
}

type DataTableRowsInserted struct {
	DataTableID string `json:"data_table_id"`
	Inserted    int    `json:"inserted"`
}

type DataTableRowsUpdated struct {
	DataTableID string `json:"data_table_id"`
	Updated     bool   `json:"updated"`
}

type DataTableRowsUpserted struct {
	DataTableID string `json:"data_table_id"`
	Upserted    bool   `json:"upserted"`
}

type DataTableRowsDeleted struct {
	DataTableID string `json:"data_table_id"`
	Deleted     bool   `json:"deleted"`
}

type rowConditionArguments struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type rowFilterArguments struct {
	Type       string                  `json:"type"`
	Conditions []rowConditionArguments `json:"conditions"`
}

type dataRowArguments struct {
	DataTableID string              `json:"data_table_id"`
	Filter      *rowFilterArguments `json:"filter"`
	Rows        []map[string]any    `json:"rows"`
	Data        map[string]any      `json:"data"`
	Cursor      string              `json:"cursor"`
	Limit       int                 `json:"limit"`
}

// readDataRowArguments keeps numbers as written so they are sent exactly as given.
func readDataRowArguments(op string, raw json.RawMessage) (dataRowArguments, error) {
	var input dataRowArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// rowValueFits reports whether value is a scalar the column type accepts.
func rowValueFits(columnType string, value any) bool {
	switch v := value.(type) {
	case string:
		return (columnType == "string" || columnType == "date") && utf8.RuneCountInString(v) <= maxRowStringLength
	case json.Number:
		return columnType == "number"
	case bool:
		return columnType == "boolean"
	}
	return false
}

func columnTypes(table *dataTableJSON) map[string]string {
	types := make(map[string]string, len(table.Columns))
	for _, col := range table.Columns {
		types[col.Name] = col.Type
	}
	return types
}

// checkRowData refuses an unknown column or a value of the wrong type, before any change.
func checkRowData(types map[string]string, data map[string]any) error {
	if len(data) == 0 {
		return invalidRequest("a row needs at least one column value")
	}
	for name, value := range data {
		kind, ok := types[name]
		if !ok {
			return invalidRequest("a column of the data does not belong to this data table")
		}
		if !rowValueFits(kind, value) {
			return invalidRequest("a value does not match the type of its column")
		}
	}
	return nil
}

// checkRowFilterShape is the local part of the filter check: at least one condition, a known operator.
func checkRowFilterShape(filter *rowFilterArguments) error {
	if filter == nil || len(filter.Conditions) == 0 || len(filter.Conditions) > maxRowConditions {
		return invalidRequest("filter needs 1 to " + strconv.Itoa(maxRowConditions) + " conditions")
	}
	if filter.Type != "" && filter.Type != "and" && filter.Type != "or" {
		return invalidRequest("filter type must be and or or")
	}
	for _, c := range filter.Conditions {
		known := false
		for _, op := range rowOperators {
			known = known || c.Operator == op
		}
		if !known {
			return invalidRequest("filter operator must be one of eq, neq, like, ilike, gt, gte, lt, lte")
		}
	}
	return nil
}

// buildRowFilter turns validated conditions into the Public API's filter object.
func buildRowFilter(types map[string]string, filter *rowFilterArguments) (map[string]any, error) {
	kind := filter.Type
	if kind == "" {
		kind = "and"
	}
	filters := make([]map[string]any, 0, len(filter.Conditions))
	for _, c := range filter.Conditions {
		columnType, ok := types[c.Column]
		if !ok {
			return nil, invalidRequest("a column of the filter does not belong to this data table")
		}
		if !rowValueFits(columnType, c.Value) {
			return nil, invalidRequest("a filter value does not match the type of its column")
		}
		switch c.Operator {
		case "like", "ilike":
			if columnType != "string" {
				return nil, invalidRequest("like and ilike apply to string columns only")
			}
		case "gt", "gte", "lt", "lte":
			if columnType != "number" && columnType != "date" {
				return nil, invalidRequest("gt, gte, lt, and lte apply to number and date columns only")
			}
		}
		filters = append(filters, map[string]any{"columnName": c.Column, "condition": c.Operator, "value": c.Value})
	}
	return map[string]any{"type": kind, "filters": filters}, nil
}

func rowsPath(tableID string) string {
	return "/data-tables/" + url.PathEscape(tableID) + "/rows"
}

type dataRowsPageJSON struct {
	Data       []map[string]any `json:"data"`
	NextCursor *string          `json:"nextCursor"`
}

func boundedRowText(value string) string {
	if utf8.RuneCountInString(value) > maxRowStringLength {
		return string([]rune(value)[:maxRowStringLength])
	}
	return value
}

// scalarRow keeps scalar values only, bounded in key count, key length, and text length.
func scalarRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for key, value := range row {
		if len(out) >= maxDataTableColumns+3 || utf8.RuneCountInString(key) > maxRowKeyLength {
			continue
		}
		switch v := value.(type) {
		case string:
			out[key] = boundedRowText(v)
		case float64, bool, nil:
			out[key] = v
		}
	}
	return out
}

func invokeDataRowsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list data table rows"
	input, err := readDataRowArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		if input.Limit < 0 || input.Limit > maxRowListLimit {
			return invalidRequest("limit must be 1 to " + strconv.Itoa(maxRowListLimit))
		}
		if input.Filter != nil {
			return checkRowFilterShape(input.Filter)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultRowListSize
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if input.Cursor != "" {
		query.Set("cursor", input.Cursor)
	}
	if input.Filter != nil {
		filter, err := buildRowFilter(columnTypes(table), input.Filter)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(filter)
		if err != nil {
			return nil, providerError(op, "the request could not be built")
		}
		query.Set("filter", string(encoded))
	}
	var page dataRowsPageJSON
	if err := client.get(ctx, op, rowsPath(input.DataTableID), query, &page, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	result := &DataTableRows{Rows: make([]map[string]any, 0, len(page.Data))}
	for i, row := range page.Data {
		if i >= limit {
			break
		}
		result.Rows = append(result.Rows, scalarRow(row))
	}
	result.Count = len(result.Rows)
	if page.NextCursor != nil && *page.NextCursor != "" {
		if len(*page.NextCursor) > maxCursorLength {
			return nil, invalidResponse(op, "n8n returned a cursor that is too long")
		}
		result.Cursor, result.HasMore = *page.NextCursor, true
	}
	return result, nil
}

func invokeDataRowsInsert(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "insert data table rows"
	input, err := readDataRowArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		if len(input.Rows) == 0 || len(input.Rows) > maxRowsPerInsert {
			return invalidRequest("rows needs 1 to " + strconv.Itoa(maxRowsPerInsert) + " rows")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	types := columnTypes(table)
	for _, row := range input.Rows {
		if err := checkRowData(types, row); err != nil {
			return nil, err
		}
	}
	var answer struct {
		Count *int `json:"count"`
	}
	if err := client.change(ctx, op, http.MethodPost, rowsPath(input.DataTableID), nil,
		map[string]any{"data": input.Rows, "returnType": "count"}, &answer, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	if answer.Count == nil || *answer.Count < 0 {
		return nil, invalidResponse(op, "n8n did not report how many rows were inserted"+uncertain)
	}
	return &DataTableRowsInserted{DataTableID: input.DataTableID, Inserted: *answer.Count}, nil
}

// changeRows validates the filter and data against the read table and sends the one request of update and
// upsert. The answer must be true.
func changeRows(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, method, path string, raw json.RawMessage) (string, error) {
	input, err := readDataRowArguments(op, raw)
	if err != nil {
		return "", err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		return checkRowFilterShape(input.Filter)
	})
	if err != nil {
		return "", err
	}
	types := columnTypes(table)
	filter, err := buildRowFilter(types, input.Filter)
	if err != nil {
		return "", err
	}
	if err := checkRowData(types, input.Data); err != nil {
		return "", err
	}
	var answer any
	if err := client.change(ctx, op, method, rowsPath(input.DataTableID)+path, nil,
		map[string]any{"filter": filter, "data": input.Data}, &answer, maxResponseBytes); err != nil {
		return "", dataTableError(err)
	}
	if ok, _ := answer.(bool); !ok {
		return "", invalidResponse(op, "n8n did not confirm the change"+uncertain)
	}
	return input.DataTableID, nil
}

func invokeDataRowsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := changeRows(ctx, resolved, secrets, red, "update data table rows", http.MethodPatch, "/update", raw)
	if err != nil {
		return nil, err
	}
	return &DataTableRowsUpdated{DataTableID: id, Updated: true}, nil
}

func invokeDataRowsUpsert(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := changeRows(ctx, resolved, secrets, red, "upsert data table row", http.MethodPost, "/upsert", raw)
	if err != nil {
		return nil, err
	}
	return &DataTableRowsUpserted{DataTableID: id, Upserted: true}, nil
}

func invokeDataRowsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data table rows"
	input, err := readDataRowArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		return checkRowFilterShape(input.Filter)
	})
	if err != nil {
		return nil, err
	}
	filter, err := buildRowFilter(columnTypes(table), input.Filter)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(filter)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	var answer any
	if err := client.change(ctx, op, http.MethodDelete, rowsPath(input.DataTableID)+"/delete",
		url.Values{"filter": {string(encoded)}}, nil, &answer, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	if ok, _ := answer.(bool); !ok {
		return nil, invalidResponse(op, "n8n did not confirm the deletion"+uncertain)
	}
	return &DataTableRowsDeleted{DataTableID: input.DataTableID, Deleted: true}, nil
}
