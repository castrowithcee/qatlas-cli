package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxDataColumnNameLength and dataColumnNamePattern mirror the Public API's own column name rule.
const (
	maxDataColumnNameLength = 63
	dataColumnNamePattern   = `^[a-zA-Z][a-zA-Z0-9_]*$`
)

var dataColumnNameRegexp = regexp.MustCompile(dataColumnNamePattern)

// dataColumnTypes are the column types of the Public API's specification.
var dataColumnTypes = []string{"string", "number", "boolean", "date"}

const dataColumnTypesSchema = `{"type":"string","enum":["string","number","boolean","date"]}`

var dataColumnNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxDataColumnNameLength) +
	`,"pattern":"` + dataColumnNamePattern + `"}`

// dataColumnIndexSchema is narrower than the API (any integer from 0): it stops at the column count this
// provider reads.
var dataColumnIndexSchema = `{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxDataTableColumns-1) + `}`

var dataColumnSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},"index":{"type":"integer"}},` +
	`"required":["id","name","type","index"],"additionalProperties":false}`

var dataColumnIDArgument = capability.Argument{Name: "column_id",
	Description: "Identifier of a column of that data table; unknown columns are refused before any change",
	Required:    true}

var dataColumnNameArgument = capability.Argument{Name: "name",
	Description: "Column name, 1 to " + strconv.Itoa(maxDataColumnNameLength) +
		" characters, a letter first, then letters, digits, or underscores"}

var dataColumnIndexArgument = capability.Argument{Name: "index",
	Description: "Zero-based column position, 0 to " + strconv.Itoa(maxDataTableColumns-1)}

var dataColumnFields = []capability.Field{
	{Name: "id", Description: "Column identifier"},
	{Name: "name", Description: "Column name, untrusted data"},
	{Name: "type", Description: "Column type: string, number, boolean, or date"},
	{Name: "index", Description: "Zero-based column position"},
}

var dataColumnsList = capability.Descriptor{
	ID: Provider + ".datacolumns.list", Version: 1, Title: "List the columns of an n8n data table",
	Description: "List the columns (id, name, type, position) of one data table of the project allow-list; the " +
		"table is read first and never its rows. Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datacolumns", "list", "automation"}, Risk: dataTableReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `},` +
		`"required":["data_table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"columns":{"type":"array","items":` + dataColumnSchema + `},"count":{"type":"integer"}},` +
		`"required":["columns","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument},
	Fields: []capability.Field{
		{Name: "columns", Description: "Columns of the table, at most 200"},
		{Name: "count", Description: "Number of columns reported"},
	},
	Examples: []capability.Example{{Description: "List the columns of a data table",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678"}`)}},
}

var dataColumnsAdd = capability.Descriptor{
	ID: Provider + ".datacolumns.add", Version: 1, Title: "Add a column to an n8n data table",
	Description: "Add one column to a data table of the project allow-list, from a name and a type, appended " +
		"unless an index is given. The table is read first. A repeated call may add a second column or be " +
		"refused as a name conflict. Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datacolumns", "add", "automation"},
	Risk: dataTableChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"name":` + dataColumnNameSchema + `,"type":` + dataColumnTypesSchema + `,"index":` + dataColumnIndexSchema + `},` +
		`"required":["data_table_id","name","type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataColumnSchema),
	Arguments: []capability.Argument{dataTableIDArgument,
		{Name: "name", Description: dataColumnNameArgument.Description, Required: true},
		{Name: "type", Description: "Column type: string, number, boolean, or date", Required: true},
		dataColumnIndexArgument},
	Fields: dataColumnFields,
	Examples: []capability.Example{{Description: "Add a text column",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","name":"email","type":"string"}`)}},
}

var dataColumnsUpdate = capability.Descriptor{
	ID: Provider + ".datacolumns.update", Version: 1, Title: "Rename or move a column of an n8n data table",
	Description: "Rename and/or move one column of a data table of the project allow-list; at least one of name " +
		"and index is required, the type cannot change. The table is read first and the column must belong to " +
		"it. Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datacolumns", "update", "automation"},
	Risk: dataTableChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"column_id":` + targetIDSchema + `,"name":` + dataColumnNameSchema + `,"index":` + dataColumnIndexSchema + `},` +
		`"required":["data_table_id","column_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"column_id":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["data_table_id","column_id","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, dataColumnIDArgument, dataColumnNameArgument,
		dataColumnIndexArgument},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the column"},
		{Name: "column_id", Description: "Identifier of the changed column"},
		{Name: "updated", Description: "True when n8n accepted the change"},
	},
	Examples: []capability.Example{{Description: "Rename a column",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","column_id":"wxyz1234abcd5678","name":"mail"}`)}},
}

var dataColumnsDelete = capability.Descriptor{
	ID: Provider + ".datacolumns.delete", Version: 1, Title: "Delete a column of an n8n data table",
	Description: "Delete one column of a data table of the project allow-list together with all its data in " +
		"every row; the deletion cannot be undone. The table is read first and the column must belong to it. " +
		"Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datacolumns", "delete", "automation"},
	Risk: dataTableChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"column_id":` + targetIDSchema + `},"required":["data_table_id","column_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_table_id":{"type":"string"},"column_id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["data_table_id","column_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, dataColumnIDArgument},
	Fields: []capability.Field{
		{Name: "data_table_id", Description: "Table of the column"},
		{Name: "column_id", Description: "Identifier of the deleted column"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a column and its data",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","column_id":"wxyz1234abcd5678"}`)}},
}

// DataTableColumns, DataTableColumnChanged, and DataTableColumnDeleted are what the column tools report.
type DataTableColumns struct {
	Columns []DataTableColumn `json:"columns"`
	Count   int               `json:"count"`
}

type DataTableColumnChanged struct {
	DataTableID string `json:"data_table_id"`
	ColumnID    string `json:"column_id"`
	Updated     bool   `json:"updated"`
}

type DataTableColumnDeleted struct {
	DataTableID string `json:"data_table_id"`
	ColumnID    string `json:"column_id"`
	Deleted     bool   `json:"deleted"`
}

type dataColumnArguments struct {
	DataTableID string `json:"data_table_id"`
	ColumnID    string `json:"column_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Index       *int   `json:"index"`
}

func readDataColumnArguments(op string, raw json.RawMessage) (dataColumnArguments, error) {
	var input dataColumnArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func validDataColumnName(name string) error {
	if len(name) == 0 || len(name) > maxDataColumnNameLength || !dataColumnNameRegexp.MatchString(name) {
		return invalidRequest("name must be 1 to " + strconv.Itoa(maxDataColumnNameLength) +
			" characters, a letter first, then letters, digits, or underscores")
	}
	return nil
}

func validDataColumnType(kind string) error {
	for _, allowed := range dataColumnTypes {
		if kind == allowed {
			return nil
		}
	}
	return invalidRequest("type must be string, number, boolean, or date")
}

func validDataColumnIndex(index *int) error {
	if index != nil && (*index < 0 || *index >= maxDataTableColumns) {
		return invalidRequest("index must be 0 to " + strconv.Itoa(maxDataTableColumns-1))
	}
	return nil
}

func columnsPath(tableID string) string {
	return "/data-tables/" + url.PathEscape(tableID) + "/columns"
}

// openBoundTable validates the table ID locally, opens the client, and reads and binds the table.
func openBoundTable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, tableID string, validate func() error) (*Client, *dataTableJSON, error) {
	if _, err := selectDataTables(resolved); err != nil {
		return nil, nil, err
	}
	if !validTargetID(tableID) {
		return nil, nil, invalidRequest("data_table_id must be a usable n8n identifier")
	}
	if validate != nil {
		if err := validate(); err != nil {
			return nil, nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	table, err := client.readDataTable(ctx, op, tableID)
	if err != nil {
		return nil, nil, dataTableError(err)
	}
	return client, table, nil
}

// requireColumn refuses a column ID that the already read table does not contain, before any change.
func requireColumn(table *dataTableJSON, columnID string) error {
	for _, col := range table.Columns {
		if col.ID == columnID {
			return nil
		}
	}
	return invalidRequest("the column does not belong to this data table")
}

func invokeDataColumnsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list data table columns"
	input, err := readDataColumnArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, _, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, nil)
	if err != nil {
		return nil, err
	}
	var columns []dataTableColumnJSON
	if err := client.get(ctx, op, columnsPath(input.DataTableID), nil, &columns, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	result := make([]DataTableColumn, 0, len(columns))
	for i, col := range columns {
		if i >= maxDataTableColumns {
			break
		}
		result = append(result, DataTableColumn{ID: bounded(col.ID), Name: bounded(col.Name),
			Type: bounded(col.Type), Index: col.Index})
	}
	return &DataTableColumns{Columns: result, Count: len(result)}, nil
}

func invokeDataColumnsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add data table column"
	input, err := readDataColumnArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, _, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		if err := validDataColumnName(input.Name); err != nil {
			return err
		}
		if err := validDataColumnType(input.Type); err != nil {
			return err
		}
		return validDataColumnIndex(input.Index)
	})
	if err != nil {
		return nil, err
	}
	body := map[string]any{"name": input.Name, "type": input.Type}
	if input.Index != nil {
		body["index"] = *input.Index
	}
	var created dataTableColumnJSON
	if err := client.change(ctx, op, http.MethodPost, columnsPath(input.DataTableID), nil, body, &created,
		maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	if !validTargetID(created.ID) {
		return nil, invalidResponse(op, "n8n did not report a usable ID of the added column"+uncertain)
	}
	return &DataTableColumn{ID: created.ID, Name: bounded(created.Name), Type: bounded(created.Type),
		Index: created.Index}, nil
}

func invokeDataColumnsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update data table column"
	input, err := readDataColumnArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		if !validTargetID(input.ColumnID) {
			return invalidRequest("column_id must be a usable n8n identifier")
		}
		if input.Name == "" && input.Index == nil {
			return invalidRequest("name or index is required")
		}
		if input.Name != "" {
			if err := validDataColumnName(input.Name); err != nil {
				return err
			}
		}
		return validDataColumnIndex(input.Index)
	})
	if err != nil {
		return nil, err
	}
	if err := requireColumn(table, input.ColumnID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if input.Name != "" {
		body["name"] = input.Name
	}
	if input.Index != nil {
		body["index"] = *input.Index
	}
	if err := client.change(ctx, op, http.MethodPatch, columnsPath(input.DataTableID)+"/"+url.PathEscape(input.ColumnID),
		nil, body, nil, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	return &DataTableColumnChanged{DataTableID: input.DataTableID, ColumnID: input.ColumnID, Updated: true}, nil
}

func invokeDataColumnsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data table column"
	input, err := readDataColumnArguments(op, raw)
	if err != nil {
		return nil, err
	}
	client, table, err := openBoundTable(ctx, resolved, secrets, red, op, input.DataTableID, func() error {
		if !validTargetID(input.ColumnID) {
			return invalidRequest("column_id must be a usable n8n identifier")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := requireColumn(table, input.ColumnID); err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodDelete, columnsPath(input.DataTableID)+"/"+url.PathEscape(input.ColumnID),
		nil, nil, nil, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	return &DataTableColumnDeleted{DataTableID: input.DataTableID, ColumnID: input.ColumnID, Deleted: true}, nil
}
