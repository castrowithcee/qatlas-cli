package seatable

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The bounds and routes of the batch row operations and the snapshot. One call changes at most
// maxBatchRows rows and sends at most maxRequestBytes; both are checked before any I/O.
const (
	maxBatchRows = 100
	snapshotPath = "/snapshot/"
	maxNameBytes = 512

	// batchUncertain is appended to a failure of a batch change whose request may have reached SeaTable.
	// Qatlas never repeats such a request by itself.
	batchUncertain = "; this change may have taken effect, read the rows before repeating it"

	// snapshotUncertain is the same hint for a snapshot.
	snapshotUncertain = "; the snapshot may have been created, check the snapshots of the base before repeating it"

	// snapshotRefused is the message of a refused snapshot. SeaTable only creates a snapshot when the base
	// changed since the last one and at least ten minutes have passed.
	snapshotRefused = "SeaTable did not create a snapshot: a snapshot needs at least one change in the base " +
		"since the last snapshot and at least 10 minutes since it, try again later"
)

const batchRowsSchema = `"minItems":1,"maxItems":100`

func batchInput(properties, required string) string {
	return `{"type":"object","properties":{"table":` + tableSelectionSchema + `,` + properties + `},"required":[` +
		required + `],"additionalProperties":false}`
}

func batchOutput() string {
	return `{"type":"object","properties":{"requested":{"type":"integer"},"reported":{"type":"integer"}},` +
		`"required":["requested"],"additionalProperties":false}`
}

var rowsBatchCreate = batchDescriptor("batch_create", "Create up to 100 SeaTable rows", capability.EffectCreate,
	capability.IdempotencyNonIdempotent, false,
	"Create up to 100 rows in one table allowed by an explicit SeaTable connection with one request",
	batchInput(`"rows":{"type":"array",`+batchRowsSchema+`,"items":{"type":"object","minProperties":1}}`, `"rows"`),
	[]capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "rows", Description: "1 to 100 objects of column name to value; link columns are refused", Required: true},
	})
var rowsBatchUpdate = batchDescriptor("batch_update", "Update up to 100 SeaTable rows", capability.EffectUpdate,
	capability.IdempotencyIdempotent, false,
	"Update up to 100 rows by row identifier in one table allowed by an explicit SeaTable connection with one request",
	batchInput(`"rows":{"type":"array",`+batchRowsSchema+`,"items":{"type":"object","properties":{"row_id":`+rowIDSchema+
		`,"values":{"type":"object","minProperties":1}},"required":["row_id","values"],"additionalProperties":false}}`, `"rows"`),
	[]capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "rows", Description: "1 to 100 objects with a distinct row_id of 22 characters and the values to change", Required: true},
	})
var rowsBatchDelete = batchDescriptor("batch_delete", "Delete up to 100 SeaTable rows", capability.EffectDelete,
	capability.IdempotencyIdempotent, true,
	"Delete up to 100 rows by row identifier in one table allowed by an explicit SeaTable connection with one request",
	batchInput(`"row_ids":{"type":"array",`+batchRowsSchema+`,"items":`+rowIDSchema+`}`, `"row_ids"`),
	[]capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "row_ids", Description: "1 to 100 distinct row identifiers of 22 characters", Required: true},
	})

func batchDescriptor(action, title string, effect capability.Effect, idempotency capability.Idempotency,
	allowList bool, description, input string, arguments []capability.Argument) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".rows." + strings.ReplaceAll(action, "_", ""), Version: 1, Title: title, Description: description + "; SeaTable reports " +
			"no failure per row, the answer names the requested and, when SeaTable gives it, the reported count",
		Tags:     []string{"seatable", "base", "rows", "batch", action, "table"},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(batchOutput()),
		Arguments: arguments,
		Fields: []capability.Field{
			{Name: "requested", Description: "Number of rows in the request"},
			{Name: "reported", Description: "Number of rows SeaTable reported as handled, when it reports one"},
		},
	}
}

var snapshotsCreate = capability.Descriptor{
	ID: Provider + ".snapshots.create", Version: 1, Title: "Create a SeaTable snapshot",
	Description: "Create a snapshot of the whole base of the connection, for example before a mass change; " +
		"SeaTable needs at least one change since the last snapshot and 10 minutes between two snapshots",
	Tags: []string{"seatable", "base", "snapshot", "create"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"}},"required":["created"],"additionalProperties":false}`),
	Fields:       []capability.Field{{Name: "created", Description: "True when SeaTable created the snapshot"}},
}

// BatchResult names the number of rows in the request and, when SeaTable gives one, the number it reported.
type BatchResult struct {
	Requested int  `json:"requested"`
	Reported  *int `json:"reported,omitempty"`
}

// BatchUpdate is one row of a batch update.
type BatchUpdate struct {
	RowID  string                     `json:"row_id"`
	Values map[string]json.RawMessage `json:"values"`
}

// openForTable settles the connection's own table boundary before the credential is resolved: a table
// outside the allow-list is refused without a secret lookup and without provider access.
func openForTable(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, table string) (*Client, error) {
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(table); err != nil {
		return nil, providerError(op, err.Error())
	}
	return Open(ctx, resolved, secrets, red)
}

func checkBatchSize(op string, raw json.RawMessage) error {
	if len(raw) > maxRequestBytes {
		return providerError(op, "the request exceeds the size limit")
	}
	return nil
}

func checkBatchCount(op string, n int) error {
	if n < 1 || n > maxBatchRows {
		return providerError(op, "a batch holds 1 to "+strconv.Itoa(maxBatchRows)+" rows")
	}
	return nil
}

func invokeRowsBatchCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "batch create rows"
	if err := checkBatchSize(op, raw); err != nil {
		return nil, err
	}
	var input struct {
		Table string                       `json:"table"`
		Rows  []map[string]json.RawMessage `json:"rows"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkBatchCount(op, len(input.Rows)); err != nil {
		return nil, err
	}
	client, err := openForTable(ctx, op, resolved, secrets, red, input.Table)
	if err != nil {
		return nil, err
	}
	return client.BatchCreateRows(ctx, input.Table, input.Rows)
}

func invokeRowsBatchUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "batch update rows"
	if err := checkBatchSize(op, raw); err != nil {
		return nil, err
	}
	var input struct {
		Table string        `json:"table"`
		Rows  []BatchUpdate `json:"rows"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkBatchUpdates(op, input.Rows); err != nil {
		return nil, err
	}
	client, err := openForTable(ctx, op, resolved, secrets, red, input.Table)
	if err != nil {
		return nil, err
	}
	return client.BatchUpdateRows(ctx, input.Table, input.Rows)
}

func invokeRowsBatchDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "batch delete rows"
	if err := checkBatchSize(op, raw); err != nil {
		return nil, err
	}
	var input struct {
		Table  string   `json:"table"`
		RowIDs []string `json:"row_ids"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkRowIDs(op, input.RowIDs, 1, maxBatchRows); err != nil {
		return nil, err
	}
	client, err := openForTable(ctx, op, resolved, secrets, red, input.Table)
	if err != nil {
		return nil, err
	}
	return client.BatchDeleteRows(ctx, input.Table, input.RowIDs)
}

func invokeSnapshotsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.CreateSnapshot(ctx); err != nil {
		return nil, err
	}
	return map[string]bool{"created": true}, nil
}

func checkBatchUpdates(op string, rows []BatchUpdate) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.RowID
	}
	return checkRowIDs(op, ids, 1, maxBatchRows)
}

// BatchCreateRows creates 1 to 100 rows with exactly one request.
func (c *Client) BatchCreateRows(ctx context.Context, table string, rows []map[string]json.RawMessage) (*BatchResult, error) {
	const op = "batch create rows"
	if err := checkBatchCount(op, len(rows)); err != nil {
		return nil, err
	}
	return c.batchChange(ctx, op, http.MethodPost, table, map[string]any{"rows": rows}, rows, len(rows), "inserted_row_count")
}

// BatchUpdateRows updates 1 to 100 distinct rows with exactly one request.
func (c *Client) BatchUpdateRows(ctx context.Context, table string, rows []BatchUpdate) (*BatchResult, error) {
	const op = "batch update rows"
	if err := checkBatchUpdates(op, rows); err != nil {
		return nil, err
	}
	updates := make([]any, len(rows))
	sets := make([]map[string]json.RawMessage, len(rows))
	for i, row := range rows {
		updates[i] = map[string]any{"row_id": row.RowID, "row": row.Values}
		sets[i] = row.Values
	}
	return c.batchChange(ctx, op, http.MethodPut, table, map[string]any{"updates": updates}, sets, len(rows), "")
}

// BatchDeleteRows deletes 1 to 100 distinct rows with exactly one request.
func (c *Client) BatchDeleteRows(ctx context.Context, table string, rowIDs []string) (*BatchResult, error) {
	const op = "batch delete rows"
	if err := checkRowIDs(op, rowIDs, 1, maxBatchRows); err != nil {
		return nil, err
	}
	return c.batchChange(ctx, op, http.MethodDelete, table, map[string]any{"row_ids": rowIDs}, nil, len(rowIDs), "deleted_rows")
}

// batchChange validates the columns against the base metadata, sends one request, and never repeats it.
// The API reports no failure per row; countField names the answer field that carries the reported count.
func (c *Client) batchChange(ctx context.Context, op, method, table string, payload map[string]any,
	sets []map[string]json.RawMessage, requested int, countField string) (*BatchResult, error) {
	path, token, encoded, err := c.prepareRows(ctx, op, table, payload, sets, true)
	if err != nil {
		return nil, err
	}
	data, err := c.sendOnce(ctx, op, method, path, token, encoded, batchUncertain, statusError)
	if err != nil {
		return nil, err
	}
	result := &BatchResult{Requested: requested}
	if len(bytes.TrimSpace(data)) == 0 {
		return result, nil
	}
	var answer map[string]json.RawMessage
	if json.Unmarshal(data, &answer) != nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned an invalid response" + batchUncertain}
	}
	var success bool
	if raw, ok := answer["success"]; ok && json.Unmarshal(raw, &success) == nil && !success {
		return nil, providerError(op, "SeaTable did not apply the change")
	}
	if countField != "" {
		var n int
		if raw, ok := answer[countField]; ok && json.Unmarshal(raw, &n) == nil && n >= 0 && n <= maxBatchRows {
			result.Reported = &n
		}
	}
	return result, nil
}

// CreateSnapshot asks SeaTable for a snapshot of the base with exactly one request. The body carries only
// the base name the token exchange reported.
func (c *Client) CreateSnapshot(ctx context.Context) error {
	const op = "create snapshot"
	access, err := c.access(ctx, op)
	if err != nil {
		return err
	}
	body := map[string]string{}
	if access.name != "" && len(access.name) <= maxNameBytes {
		body["dtable_name"] = access.name
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	data, err := c.sendOnce(ctx, op, http.MethodPost, gatewayPath+url.PathEscape(access.uuid)+snapshotPath,
		access.token, encoded, snapshotUncertain, snapshotStatusError)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var answer map[string]json.RawMessage
	if json.Unmarshal(data, &answer) != nil {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned an invalid response" + snapshotUncertain}
	}
	return nil
}

// snapshotStatusError classifies the statuses SeaTable uses to refuse a snapshot by the status alone. The
// provider text is never read or copied.
func snapshotStatusError(op string, status int) error {
	switch status {
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: snapshotRefused}
	}
	return statusError(op, status)
}
