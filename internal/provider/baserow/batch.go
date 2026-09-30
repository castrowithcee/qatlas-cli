package baserow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxBatchRows is Baserow's default batch size limit; it is checked before any I/O.
const maxBatchRows = 200

const batchNote = "One to 200 rows per call, sent as one request that Qatlas never repeats. "

const batchRowsOutput = `{"type":"object","properties":{"rows":{"type":"array","items":` + rowSchema +
	`},"count":{"type":"integer"}},"required":["rows","count"],"additionalProperties":false}`

var rowsBatchCreate = capability.Descriptor{
	ID: Provider + ".rows.batchcreate", Version: 1, Title: "Create Baserow rows in a batch",
	Description: batchNote + "Create rows in a table; each entry of rows holds cell values keyed by field name. " + changeNote,
	Tags:        []string{"baserow", "rows", "batch", "create", "records"}, Provider: Provider,
	Risk: rowChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"rows":{"type":"array",` +
		`"minItems":1,"maxItems":200,"items":` + fieldsInputSchema + `}},"required":["table_id","rows"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(batchRowsOutput),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "rows", Required: true, Description: "1 to 200 objects of cell values keyed by field name"}},
	Fields: []capability.Field{
		{Name: "rows", Description: "The new rows in request order, like baserow.rows.get"},
		{Name: "count", Description: "Number of rows Baserow returned"},
	},
	Examples: []capability.Example{{Description: "Create two rows",
		Arguments: json.RawMessage(`{"table_id":1,"rows":[{"Name":"Acme"},{"Name":"Beta"}]}`)}},
}

var rowsBatchUpdate = capability.Descriptor{
	ID: Provider + ".rows.batchupdate", Version: 1, Title: "Update Baserow rows in a batch",
	Description: batchNote + "Change the given cells of several rows; cells not named stay as they are. Row ids " +
		"must be distinct, and a field named id is refused. " + changeNote,
	Tags: []string{"baserow", "rows", "batch", "update", "records"}, Provider: Provider,
	Risk: rowChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"rows":{"type":"array",` +
		`"minItems":1,"maxItems":200,"items":{"type":"object","properties":{"row_id":` + idSchema +
		`,"fields":{"type":"object","minProperties":1,"maxProperties":500}},"required":["row_id","fields"],` +
		`"additionalProperties":false}}},"required":["table_id","rows"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(batchRowsOutput),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "rows", Required: true, Description: "1 to 200 objects with row_id and fields (at least one cell)"}},
	Fields: []capability.Field{
		{Name: "rows", Description: "The changed rows as Baserow reports them"},
		{Name: "count", Description: "Number of rows Baserow returned"},
	},
	Examples: []capability.Example{{Description: "Change two rows", Arguments: json.RawMessage(
		`{"table_id":1,"rows":[{"row_id":7,"fields":{"Name":"A"}},{"row_id":8,"fields":{"Name":"B"}}]}`)}},
}

var rowsBatchDelete = capability.Descriptor{
	ID: Provider + ".rows.batchdelete", Version: 1, Title: "Delete Baserow rows in a batch",
	Description: batchNote + "Delete several rows of a table by id; ids must be distinct. Baserow may keep them in " +
		"the trash for a while; Qatlas does not restore them",
	Tags: []string{"baserow", "rows", "batch", "delete", "records"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: rowChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"row_ids":{"type":"array",` +
		`"minItems":1,"maxItems":200,"items":` + idSchema + `}},"required":["table_id","row_ids"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
		`"count":{"type":"integer"}},"required":["deleted","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "row_ids", Required: true, Description: "1 to 200 distinct row identifiers from baserow.rows.list"}},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Baserow accepted the deletion"},
		{Name: "count", Description: "Number of rows named in the request"},
	},
	Examples: []capability.Example{{Description: "Delete two rows", Arguments: json.RawMessage(`{"table_id":1,"row_ids":[7,8]}`)}},
}

// BatchResult is the answer of batchcreate and batchupdate; BatchDeleteResult the answer of batchdelete.
type BatchResult struct {
	Rows  []Row `json:"rows"`
	Count int   `json:"count"`
}

type BatchDeleteResult struct {
	Deleted bool `json:"deleted"`
	Count   int  `json:"count"`
}

type batchArguments struct {
	TableID int64             `json:"table_id"`
	Rows    []json.RawMessage `json:"rows"`
	RowIDs  []int64           `json:"row_ids"`
}

type batchUpdateItem struct {
	RowID  int64                      `json:"row_id"`
	Fields map[string]json.RawMessage `json:"fields"`
}

func validRowID(id int64) bool {
	return id > 0 && len(strconv.FormatInt(id, 10)) <= maxIDDigits
}

// prepareBatch settles every check that needs no I/O: table boundary (before any secret), row count, distinct
// identifiers, field names, and the request size. It returns the per-row cells and the request body.
func prepareBatch(op, mode string, resolved *config.Resolved, raw json.RawMessage) (int64, []map[string]json.RawMessage, []byte, error) {
	var input batchArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return 0, nil, nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return 0, nil, nil, err
	}
	count := len(input.Rows)
	if mode == "delete" {
		count = len(input.RowIDs)
	}
	if count < 1 || count > maxBatchRows {
		return 0, nil, nil, invalidRequest("a batch takes 1 to " + strconv.Itoa(maxBatchRows) + " rows")
	}
	seen := make(map[int64]bool, count)
	markSeen := func(id int64) error {
		if !validRowID(id) {
			return invalidRequest("a row id must be a positive integer")
		}
		if seen[id] {
			return invalidRequest("a row id appears more than once")
		}
		seen[id] = true
		return nil
	}
	if mode == "delete" {
		for _, id := range input.RowIDs {
			if err := markSeen(id); err != nil {
				return 0, nil, nil, err
			}
		}
		body, err := json.Marshal(map[string]any{"items": input.RowIDs})
		if err != nil {
			return 0, nil, nil, providerError(op, "the request could not be built")
		}
		return input.TableID, nil, body, nil
	}
	cells := make([]map[string]json.RawMessage, 0, count)
	items := make([]map[string]json.RawMessage, 0, count)
	for _, rawRow := range input.Rows {
		var fields map[string]json.RawMessage
		item := map[string]json.RawMessage{}
		if mode == "update" {
			var entry batchUpdateItem
			if err := json.Unmarshal(rawRow, &entry); err != nil {
				return 0, nil, nil, invalidRequest("each row needs row_id and fields")
			}
			if err := markSeen(entry.RowID); err != nil {
				return 0, nil, nil, err
			}
			if len(entry.Fields) == 0 {
				return 0, nil, nil, invalidRequest("fields must name at least one cell")
			}
			if _, clash := entry.Fields["id"]; clash {
				return 0, nil, nil, invalidRequest("a batch update cannot write a field named id")
			}
			fields = entry.Fields
			item["id"] = json.RawMessage(strconv.FormatInt(entry.RowID, 10))
		} else if err := json.Unmarshal(rawRow, &fields); err != nil || fields == nil {
			return 0, nil, nil, invalidRequest("each row must be an object of cell values")
		}
		if len(fields) > maxChangedFields {
			return 0, nil, nil, invalidRequest("fields names too many cells")
		}
		for name, value := range fields {
			if name == "" || len(name) > maxFieldNameLen {
				return 0, nil, nil, invalidRequest("a field name must have 1 to " + strconv.Itoa(maxFieldNameLen) + " bytes")
			}
			item[name] = value
		}
		cells = append(cells, fields)
		items = append(items, item)
	}
	body, err := json.Marshal(map[string]any{"items": items})
	if err != nil || len(body) > maxRequestBytes {
		return 0, nil, nil, invalidRequest("the cell values exceed the request size limit")
	}
	return input.TableID, cells, body, nil
}

func invokeRowsBatchCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeRows(ctx, "create rows", "create", http.MethodPost, "create", resolved, secrets, red, raw)
}

func invokeRowsBatchUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeRows(ctx, "update rows", "update", http.MethodPatch, "update", resolved, secrets, red, raw)
}

// changeRows runs batchcreate and batchupdate: one field read, then one request.
func changeRows(ctx context.Context, op, mode, method, right string, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	tableID, cells, body, err := prepareBatch(op, mode, resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	fields, err := client.readFields(ctx, op, tableID)
	if err != nil {
		return nil, err
	}
	for _, values := range cells {
		if err := client.checkFields(fields, values); err != nil {
			return nil, err
		}
	}
	path := "/api/database/rows/table/" + tablePath(tableID) + "batch/"
	data, err := client.sendOnce(ctx, op, method, path, url.Values{"user_field_names": {"true"}}, body, right)
	if err != nil {
		return nil, err
	}
	rows, err := decodeBatchRows(op, data, len(cells))
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			providerErr.Message += rowUncertain
		}
		return nil, err
	}
	client.maskWith(fields, true, rows)
	for i := range rows {
		rows[i].Fields = boundFields(rows[i].Fields)
	}
	return &BatchResult{Rows: rows, Count: len(rows)}, nil
}

func decodeBatchRows(op string, data []byte, want int) ([]Row, error) {
	var answer struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || len(answer.Items) != want {
		return nil, invalidResponse(op, "Baserow returned an invalid batch answer")
	}
	rows := make([]Row, 0, len(answer.Items))
	for _, item := range answer.Items {
		row, err := decodeRow(op, item)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func invokeRowsBatchDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete rows"
	tableID, _, body, err := prepareBatch(op, "delete", resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	path := "/api/database/rows/table/" + tablePath(tableID) + "batch-delete/"
	if _, err := client.sendOnce(ctx, op, http.MethodPost, path, nil, body, "delete"); err != nil {
		return nil, err
	}
	var input batchArguments
	_ = json.Unmarshal(raw, &input)
	return &BatchDeleteResult{Deleted: true, Count: len(input.RowIDs)}, nil
}
