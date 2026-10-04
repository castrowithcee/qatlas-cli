package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxDataTableNameLength mirrors the Public API's own limit for a data table name (1 to 128 characters).
const maxDataTableNameLength = 128

// maxDataTableColumns bounds the columns one data table read reports.
const maxDataTableColumns = 200

// dataTableDataSensitivity marks the data table structure the tools read and change; rows are never read.
const dataTableDataSensitivity = "n8n-data-tables"

// dataTablePermissionMessage is the one message of a 403 on a data tables endpoint. n8n answers 403 for a
// missing license, a missing API key scope (dataTable:*), and a role that may not use the table's project,
// without a field this provider reads to tell them apart; the body is never read into a message.
const dataTablePermissionMessage = "n8n refused this data tables operation: license or role missing (the " +
	"instance's license, the API key's dataTable scope, or its owner's role in the table's project); Qatlas " +
	"cannot tell which"

var dataTableReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataTableDataSensitivity}

func dataTableChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataTableDataSensitivity}
}

var dataTableNameSchemaJSON = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxDataTableNameLength) + `}`

var dataTableSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"project_id":{"type":"string"},` +
	`"column_count":{"type":"integer"},"size_bytes":{"type":"integer"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name","project_id","column_count","created_at","updated_at"],"additionalProperties":false}`

var dataTableDetailSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"project_id":{"type":"string"},` +
	`"columns":{"type":"array","items":{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},"index":{"type":"integer"}},` +
	`"required":["id","name","type","index"],"additionalProperties":false}},` +
	`"size_bytes":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name","project_id","columns","created_at","updated_at"],"additionalProperties":false}`

var dataTableIDArgument = capability.Argument{Name: "data_table_id",
	Description: "n8n data table identifier; its project must be inside this connection's project allow-list " +
		"when it has one", Required: true}

var dataTableNameArgument = capability.Argument{Name: "name",
	Description: "Data table name, 1 to " + strconv.Itoa(maxDataTableNameLength) + " characters, no control characters",
	Required:    true}

var dataTableSummaryFields = []capability.Field{
	{Name: "id", Description: "Data table identifier"},
	{Name: "name", Description: "Data table name, untrusted data"},
	{Name: "project_id", Description: "Project that owns the table"},
	{Name: "column_count", Description: "Number of columns"},
	{Name: "size_bytes", Description: "Physical storage as n8n reports it, possibly a few seconds stale"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last change time, as n8n reports it"},
}

var dataTablesList = capability.Descriptor{
	ID: Provider + ".datatables.list", Version: 1, Title: "List n8n data tables",
	Description: "List the data tables of the bound n8n instance, restricted to its project allow-list when it " +
		"has one, optionally of one project; page by page with an opaque cursor. Refused on a connection that " +
		"restricts workflows by an allow-list",
	Tags: []string{"n8n", "datatables", "list", "automation"}, Risk: dataTableReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + targetIDSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_tables":{"type":"array","items":` + dataTableSummarySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["data_tables","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "project_id", Description: "Only the tables of this project; must be inside the project allow-list"},
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Tables per page, 1 to 250; 100 when omitted"},
	},
	Fields: append(append([]capability.Field{}, capability.Field{Name: "data_tables",
		Description: "Tables on this page after the project allow-list was applied"}),
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, even when this page is empty after filtering"},
		capability.Field{Name: "count", Description: "Number of tables on this page"}),
	Examples: []capability.Example{{Description: "List the first page of reachable data tables", Arguments: json.RawMessage(`{}`)}},
}

var dataTablesGet = capability.Descriptor{
	ID: Provider + ".datatables.get", Version: 1, Title: "Read an n8n data table",
	Description: "Read one data table's name, project, and column definitions (never its rows); only tables of " +
		"the project allow-list. Refused on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datatables", "get", "automation"}, Risk: dataTableReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `},` +
		`"required":["data_table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataTableDetailSchema),
	Arguments:    []capability.Argument{dataTableIDArgument},
	Fields: append(append([]capability.Field{}, dataTableSummaryFields[:3]...),
		capability.Field{Name: "columns", Description: "Column id, name, type, and index; at most 200"},
		dataTableSummaryFields[4], dataTableSummaryFields[5], dataTableSummaryFields[6]),
	Examples: []capability.Example{{Description: "Read a data table", Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678"}`)}},
}

var dataTablesCreate = capability.Descriptor{
	ID: Provider + ".datatables.create", Version: 1, Title: "Create an n8n data table",
	Description: "Create one data table without columns from its name, in one project: with a project allow-list " +
		"project_id is required and must be on it; without one, an omitted project_id means the key owner's " +
		"personal project. A repeated call may create a second table or be refused as a name conflict. Refused " +
		"on a connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datatables", "create", "automation"},
	Risk: dataTableChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + dataTableNameSchemaJSON + `,` +
		`"project_id":` + targetIDSchema + `},"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"project_id":{"type":"string"}},` +
		`"required":["id","name","project_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableNameArgument,
		{Name: "project_id", Description: "Project to create the table in; required with a project allow-list"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the created table"},
		{Name: "name", Description: "Name as n8n reports it, untrusted data"},
		{Name: "project_id", Description: "Project that owns the table"},
	},
	Examples: []capability.Example{{Description: "Create a data table",
		Arguments: json.RawMessage(`{"name":"customers","project_id":"VmwOO9HeTEj20kxM"}`)}},
}

var dataTablesRename = capability.Descriptor{
	ID: Provider + ".datatables.rename", Version: 1, Title: "Rename an n8n data table",
	Description: "Change the name of one data table, the only field n8n's update endpoint accepts. The table is " +
		"read first and must belong to the project allow-list. Refused on a connection that restricts workflows " +
		"by an allow-list",
	Tags: []string{"n8n", "datatables", "rename", "automation"},
	Risk: dataTableChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `,` +
		`"name":` + dataTableNameSchemaJSON + `},"required":["data_table_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["id","name","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument, dataTableNameArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the renamed table"},
		{Name: "name", Description: "The name that was sent"},
		{Name: "updated", Description: "True when n8n accepted the change"},
	},
	Examples: []capability.Example{{Description: "Rename a data table",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678","name":"clients"}`)}},
}

var dataTablesDelete = capability.Descriptor{
	ID: Provider + ".datatables.delete", Version: 1, Title: "Delete an n8n data table",
	Description: "Delete one data table of the bound n8n instance together with all its rows; the deletion cannot " +
		"be undone. The table is read first and must belong to the project allow-list. Refused on a connection " +
		"that restricts workflows by an allow-list",
	Tags: []string{"n8n", "datatables", "delete", "automation"},
	Risk: dataTableChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"data_table_id":` + targetIDSchema + `},` +
		`"required":["data_table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataTableIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted table"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a data table and all its rows",
		Arguments: json.RawMessage(`{"data_table_id":"abcd1234efgh5678"}`)}},
}

type dataTableColumnJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type dataTableJSON struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Columns   []dataTableColumnJSON `json:"columns"`
	ProjectID string                `json:"projectId"`
	CreatedAt string                `json:"createdAt"`
	UpdatedAt string                `json:"updatedAt"`
	SizeBytes int64                 `json:"sizeBytes"`
}

type dataTablesPageJSON struct {
	Data       []dataTableJSON `json:"data"`
	NextCursor *string         `json:"nextCursor"`
}

// DataTableSummary is the stable Qatlas view of one data table in a listing.
type DataTableSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"project_id"`
	ColumnCount int    `json:"column_count"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// DataTablesPage is one paginated, allow-list-filtered listing of data tables.
type DataTablesPage struct {
	DataTables []DataTableSummary `json:"data_tables"`
	Cursor     string             `json:"cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
	Count      int                `json:"count"`
}

// DataTableColumn is one column definition of a data table.
type DataTableColumn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// DataTableDetail is one data table with its column definitions, never its rows.
type DataTableDetail struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	ProjectID string            `json:"project_id"`
	Columns   []DataTableColumn `json:"columns"`
	SizeBytes int64             `json:"size_bytes"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// DataTableCreated, DataTableRenamed, and DataTableDeleted are what the changing tools report.
type DataTableCreated struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
}

type DataTableRenamed struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Updated bool   `json:"updated"`
}

type DataTableDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// dataTableError replaces the generic 403 message with the neutral license, scope, or role message.
func dataTableError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = dataTablePermissionMessage
	}
	return err
}

// selectDataTables decides locally, before any secret or request, whether this connection may use data
// tables at all. A connection that restricts workflows by an allow-list refuses every data table tool: a
// table is not a workflow and Qatlas cannot tie it to one (narrower reading, as for projects and members).
func selectDataTables(resolved *config.Resolved) (scope, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return scope{}, err
	}
	if len(bound.workflows) > 0 {
		return scope{}, invalidRequest("this connection restricts workflows by an allow-list, so it cannot use data tables")
	}
	return bound, nil
}

func validDataTableName(name string) error {
	if len(name) == 0 || utf8.RuneCountInString(name) > maxDataTableNameLength {
		return invalidRequest("name must be 1 to " + strconv.Itoa(maxDataTableNameLength) + " characters")
	}
	return validProjectName(name)
}

func summarizeDataTable(t dataTableJSON) DataTableSummary {
	return DataTableSummary{ID: bounded(t.ID), Name: bounded(t.Name), ProjectID: bounded(t.ProjectID),
		ColumnCount: len(t.Columns), SizeBytes: t.SizeBytes, CreatedAt: bounded(t.CreatedAt),
		UpdatedAt: bounded(t.UpdatedAt)}
}

type dataTableArguments struct {
	DataTableID string `json:"data_table_id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Cursor      string `json:"cursor"`
	Limit       int    `json:"limit"`
}

func readDataTableArguments(op string, raw json.RawMessage) (dataTableArguments, error) {
	var input dataTableArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeDataTablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDataTableArguments("list data tables", raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectDataTables(resolved)
	if err != nil {
		return nil, err
	}
	if input.ProjectID != "" {
		if !validTargetID(input.ProjectID) {
			return nil, invalidRequest("project_id must be a usable n8n identifier")
		}
		if !bound.allowsProject(input.ProjectID) {
			return nil, invalidRequest("project_id is outside the targets of this connection")
		}
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListDataTables(ctx, input.ProjectID, input.Cursor, limit)
	return page, dataTableError(err)
}

// ListDataTables reads one page of GET /data-tables, with n8n's projectId filter when a project is named,
// and keeps only the tables of this connection's project allow-list.
func (c *Client) ListDataTables(ctx context.Context, projectID, cursor string, limit int) (*DataTablesPage, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if projectID != "" {
		filter, err := json.Marshal(map[string]string{"projectId": projectID})
		if err != nil {
			return nil, providerError("list data tables", "the request could not be built")
		}
		query.Set("filter", string(filter))
	}
	var page dataTablesPageJSON
	if err := c.get(ctx, "list data tables", "/data-tables", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	tables := make([]DataTableSummary, 0, len(page.Data))
	for _, t := range page.Data {
		if len(c.scope.projects) > 0 && !c.scope.allowsProject(t.ProjectID) {
			continue
		}
		tables = append(tables, summarizeDataTable(t))
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return &DataTablesPage{DataTables: tables, Cursor: next, HasMore: next != "", Count: len(tables)}, nil
}

// readDataTable reads one table and checks its project against the allow-list. A non-empty allow-list never
// admits a table that reports no project. The refusal never names the table's project.
func (c *Client) readDataTable(ctx context.Context, op, id string) (*dataTableJSON, error) {
	var table dataTableJSON
	if err := c.get(ctx, op, "/data-tables/"+url.PathEscape(id), nil, &table, maxResponseBytes); err != nil {
		return nil, err
	}
	if len(c.scope.projects) > 0 && (table.ProjectID == "" || !c.scope.allowsProject(table.ProjectID)) {
		return nil, invalidRequest("the data table is outside the targets of this connection")
	}
	return &table, nil
}

func invokeDataTablesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get data table"
	input, err := readDataTableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectDataTables(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.DataTableID) {
		return nil, invalidRequest("data_table_id must be a usable n8n identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	table, err := client.readDataTable(ctx, op, input.DataTableID)
	if err != nil {
		return nil, dataTableError(err)
	}
	columns := make([]DataTableColumn, 0, len(table.Columns))
	for i, col := range table.Columns {
		if i >= maxDataTableColumns {
			break
		}
		columns = append(columns, DataTableColumn{ID: bounded(col.ID), Name: bounded(col.Name),
			Type: bounded(col.Type), Index: col.Index})
	}
	return &DataTableDetail{ID: bounded(table.ID), Name: bounded(table.Name), ProjectID: bounded(table.ProjectID),
		Columns: columns, SizeBytes: table.SizeBytes, CreatedAt: bounded(table.CreatedAt),
		UpdatedAt: bounded(table.UpdatedAt)}, nil
}

func invokeDataTablesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create data table"
	input, err := readDataTableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectDataTables(resolved)
	if err != nil {
		return nil, err
	}
	if err := validDataTableName(input.Name); err != nil {
		return nil, err
	}
	if input.ProjectID != "" && !validTargetID(input.ProjectID) {
		return nil, invalidRequest("project_id must be a usable n8n identifier")
	}
	if len(bound.projects) > 0 {
		if input.ProjectID == "" {
			return nil, invalidRequest("this connection restricts projects by an allow-list, so project_id is required")
		}
		if !bound.allowsProject(input.ProjectID) {
			return nil, invalidRequest("project_id is outside the targets of this connection")
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	created, err := client.CreateDataTable(ctx, input.Name, input.ProjectID)
	return created, dataTableError(err)
}

// CreateDataTable sends the one changing POST /data-tables request with the name, an empty column list, and
// the project when given. No CSV import field (fileId, hasHeaders) is offered.
func (c *Client) CreateDataTable(ctx context.Context, name, projectID string) (*DataTableCreated, error) {
	const op = "create data table"
	body := map[string]any{"name": name, "columns": []any{}}
	if projectID != "" {
		body["projectId"] = projectID
	}
	var created dataTableJSON
	if err := c.change(ctx, op, http.MethodPost, "/data-tables", nil, body, &created, maxResponseBytes); err != nil {
		return nil, err
	}
	if !validTargetID(created.ID) {
		return nil, invalidResponse(op, "n8n did not report a usable ID of the created data table"+uncertain)
	}
	return &DataTableCreated{ID: created.ID, Name: bounded(created.Name), ProjectID: bounded(created.ProjectID)}, nil
}

func invokeDataTablesRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename data table"
	input, err := readDataTableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectDataTables(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.DataTableID) {
		return nil, invalidRequest("data_table_id must be a usable n8n identifier")
	}
	if err := validDataTableName(input.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.readDataTable(ctx, op, input.DataTableID); err != nil {
		return nil, dataTableError(err)
	}
	if err := client.change(ctx, op, http.MethodPatch, "/data-tables/"+url.PathEscape(input.DataTableID), nil,
		map[string]any{"name": input.Name}, nil, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	return &DataTableRenamed{ID: input.DataTableID, Name: input.Name, Updated: true}, nil
}

func invokeDataTablesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data table"
	input, err := readDataTableArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectDataTables(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.DataTableID) {
		return nil, invalidRequest("data_table_id must be a usable n8n identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.readDataTable(ctx, op, input.DataTableID); err != nil {
		return nil, dataTableError(err)
	}
	if err := client.change(ctx, op, http.MethodDelete, "/data-tables/"+url.PathEscape(input.DataTableID), nil,
		nil, nil, maxResponseBytes); err != nil {
		return nil, dataTableError(err)
	}
	return &DataTableDeleted{ID: input.DataTableID, Deleted: true}, nil
}
