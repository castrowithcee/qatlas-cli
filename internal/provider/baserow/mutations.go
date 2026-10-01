package baserow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxRequestBytes bounds the body of one row change; maxChangedFields the cells of one change.
	maxRequestBytes  = 1 << 20
	maxChangedFields = 500
	maxFieldNameLen  = 255
	maxLinkValues    = 100

	// rowUncertain is appended to a failure of a row change whose request may have reached Baserow. Qatlas
	// never repeats such a request by itself.
	rowUncertain = "; this change may have taken effect, read the row before repeating it"
)

// readOnlyTypes are field types Baserow computes or maintains itself; a row change never writes them, even
// when the instance does not flag them as read-only.
var readOnlyTypes = map[string]bool{
	"formula": true, "lookup": true, "count": true, "rollup": true, "created_on": true, "last_modified": true,
	"created_by": true, "last_modified_by": true, "autonumber": true, "button": true, "ai": true,
}

const fieldsInputSchema = `{"type":"object","maxProperties":500}`

func rowChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: rowsSensitive}
}

const changeNote = "Field names come from baserow.fields.list; read-only fields (formulas, lookups, counts, " +
	"timestamps, autonumbers, and fields the instance marks read-only) are refused. A link field takes an array of " +
	"row ids of its other table and only when that table is inside the connection's targets"

const mutationOutputRow = rowSchema

var rowsCreate = capability.Descriptor{
	ID: Provider + ".rows.create", Version: 1, Title: "Create a Baserow row",
	Description: "Create one row in a table with cell values keyed by field name. " + changeNote,
	Tags:        []string{"baserow", "rows", "create", "records"}, Provider: Provider,
	Risk: rowChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"fields":` +
		fieldsInputSchema + `},"required":["table_id","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputRow),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "fields", Required: true, Description: "Cell values keyed by field name; {} creates an empty row"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the new row"},
		{Name: "order", Description: "Row order as Baserow reports it"},
		{Name: "fields", Description: "Cell values as Baserow reports them"},
	},
	Examples: []capability.Example{{Description: "Create a row",
		Arguments: json.RawMessage(`{"table_id":1,"fields":{"Name":"Acme"}}`)}},
}

var rowsUpdate = capability.Descriptor{
	ID: Provider + ".rows.update", Version: 1, Title: "Update a Baserow row",
	Description: "Change the given cells of one row; cells not named stay as they are. " + changeNote,
	Tags:        []string{"baserow", "rows", "update", "records"}, Provider: Provider,
	Risk: rowChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"row_id":` + idSchema +
		`,"fields":{"type":"object","minProperties":1,"maxProperties":500}},` +
		`"required":["table_id","row_id","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputRow),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "row_id", Required: true, Description: "Row identifier from baserow.rows.list"},
		{Name: "fields", Required: true, Description: "Cell values to change, keyed by field name; at least one"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Row identifier"},
		{Name: "order", Description: "Row order as Baserow reports it"},
		{Name: "fields", Description: "Cell values as Baserow reports them after the change"},
	},
	Examples: []capability.Example{{Description: "Change one cell",
		Arguments: json.RawMessage(`{"table_id":1,"row_id":7,"fields":{"Name":"Acme GmbH"}}`)}},
}

var rowsDelete = capability.Descriptor{
	ID: Provider + ".rows.delete", Version: 1, Title: "Delete a Baserow row",
	Description: "Delete one row of a table. Baserow may keep it in the trash for a while; Qatlas does not restore it",
	Tags:        []string{"baserow", "rows", "delete", "records"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: rowChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"row_id":` + idSchema +
		`},"required":["table_id","row_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
		`"row_id":{"type":"integer"}},"required":["deleted","row_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "row_id", Required: true, Description: "Row identifier from baserow.rows.list"}},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Baserow accepted the deletion"},
		{Name: "row_id", Description: "Identifier of the deleted row"},
	},
	Examples: []capability.Example{{Description: "Delete a row", Arguments: json.RawMessage(`{"table_id":1,"row_id":7}`)}},
}

var rowsMove = capability.Descriptor{
	ID: Provider + ".rows.move", Version: 1, Title: "Move a Baserow row",
	Description: "Change the position of one row within its table: before another row of the same table, or to the " +
		"end when before_id is omitted",
	Tags: []string{"baserow", "rows", "move", "records"}, Provider: Provider,
	Risk: rowChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,"row_id":` + idSchema +
		`,"before_id":` + idSchema + `},"required":["table_id","row_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"moved":{"type":"boolean"},` +
		`"row_id":{"type":"integer"}},"required":["moved","row_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "row_id", Required: true, Description: "Row to move"},
		{Name: "before_id", Description: "Row of the same table to place it before; the end of the table when omitted"}},
	Fields: []capability.Field{
		{Name: "moved", Description: "True when Baserow accepted the move"},
		{Name: "row_id", Description: "Identifier of the moved row"},
	},
	Examples: []capability.Example{{Description: "Move a row before another",
		Arguments: json.RawMessage(`{"table_id":1,"row_id":7,"before_id":3}`)}},
}

type changeArguments struct {
	TableID  int64                      `json:"table_id"`
	RowID    int64                      `json:"row_id"`
	BeforeID int64                      `json:"before_id"`
	Fields   map[string]json.RawMessage `json:"fields"`
}

// DeleteResult and MoveResult are the answers of rows.delete and rows.move; neither carries provider content.
type DeleteResult struct {
	Deleted bool  `json:"deleted"`
	RowID   int64 `json:"row_id"`
}

type MoveResult struct {
	Moved bool  `json:"moved"`
	RowID int64 `json:"row_id"`
}

// prepareChange decodes the arguments and settles every check that needs no provider I/O: the table boundary
// (before any secret is resolved), the identifiers, and the size of the body.
func prepareChange(op string, resolved *config.Resolved, raw json.RawMessage, needRow bool, needFields bool) (changeArguments, []byte, error) {
	var input changeArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return input, nil, err
	}
	if needRow && (input.RowID <= 0 || len(strconv.FormatInt(input.RowID, 10)) > maxIDDigits) {
		return input, nil, invalidRequest("row_id must be a positive integer")
	}
	if input.BeforeID < 0 || len(strconv.FormatInt(input.BeforeID, 10)) > maxIDDigits {
		return input, nil, invalidRequest("before_id must be a positive integer")
	}
	if !needFields {
		return input, nil, nil
	}
	if input.Fields == nil {
		input.Fields = map[string]json.RawMessage{}
	}
	if len(input.Fields) > maxChangedFields {
		return input, nil, invalidRequest("fields names too many cells")
	}
	for name := range input.Fields {
		if name == "" || len(name) > maxFieldNameLen {
			return input, nil, invalidRequest("a field name must have 1 to " + strconv.Itoa(maxFieldNameLen) + " bytes")
		}
	}
	body, err := json.Marshal(input.Fields)
	if err != nil || len(body) > maxRequestBytes {
		return input, nil, invalidRequest("the cell values exceed the request size limit")
	}
	return input, body, nil
}

// checkFields validates the named cells against the table's schema: every name must be a known, writable
// field, and a link field takes row identifiers only when its other table is inside the targets. No message
// quotes a field name or a table identifier.
func (c *Client) checkFields(fields []fieldJSON, values map[string]json.RawMessage) error {
	byName := make(map[string]fieldJSON, len(fields))
	for _, field := range fields {
		byName[field.Name] = field
	}
	for name, value := range values {
		field, ok := byName[name]
		if !ok {
			return invalidRequest("a named field does not exist in this table; use the names of baserow.fields.list")
		}
		if field.ReadOnly || readOnlyTypes[field.Type] {
			return invalidRequest("a named field is read-only and cannot be written")
		}
		if field.Type != "link_row" {
			continue
		}
		if field.LinkRowTableID == nil || !c.scope.allows(*field.LinkRowTableID) {
			return invalidRequest("a named link field leads to a table outside this connection's targets")
		}
		if !validLinkValue(value) {
			return invalidRequest("a link field takes an array of row ids")
		}
	}
	return nil
}

// validLinkValue accepts an array of up to 100 positive integers, or of text values for a primary-field
// lookup; the other table is inside the targets, so either form stays inside them.
func validLinkValue(raw json.RawMessage) bool {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil || len(items) > maxLinkValues {
		return false
	}
	for _, item := range items {
		var text string
		if json.Unmarshal(item, &text) == nil {
			continue
		}
		var id int64
		if json.Unmarshal(item, &id) != nil || id <= 0 {
			return false
		}
	}
	return true
}

func rowChangePath(tableID, rowID int64) string {
	path := "/api/database/rows/table/" + tablePath(tableID)
	if rowID > 0 {
		path += strconv.FormatInt(rowID, 10) + "/"
	}
	return path
}

func invokeRowsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeRow(ctx, "create row", http.MethodPost, "create", resolved, secrets, red, raw, false)
}

func invokeRowsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeRow(ctx, "update row", http.MethodPatch, "update", resolved, secrets, red, raw, true)
}

// changeRow runs rows.create and rows.update. The field read is the only provider I/O before a
// schema-based refusal; the change itself is one request.
func changeRow(ctx context.Context, op, method, right string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, needRow bool) (any, error) {
	input, body, err := prepareChange(op, resolved, raw, needRow, true)
	if err != nil {
		return nil, err
	}
	if needRow && len(input.Fields) == 0 {
		return nil, invalidRequest("fields must name at least one cell")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	fields, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	if err := client.checkFields(fields, input.Fields); err != nil {
		return nil, err
	}
	query := url.Values{"user_field_names": {"true"}}
	data, err := client.sendOnce(ctx, op, method, rowChangePath(input.TableID, input.RowID), query, body, right)
	if err != nil {
		return nil, err
	}
	row, err := decodeRow(op, data)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			providerErr.Message += rowUncertain
		}
		return nil, err
	}
	rows := []Row{row}
	client.maskWith(fields, true, rows)
	rows[0].Fields = boundFields(rows[0].Fields)
	return &rows[0], nil
}

func invokeRowsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete row"
	input, _, err := prepareChange(op, resolved, raw, true, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.sendOnce(ctx, op, http.MethodDelete, rowChangePath(input.TableID, input.RowID), nil, nil,
		"delete"); err != nil {
		return nil, err
	}
	return &DeleteResult{Deleted: true, RowID: input.RowID}, nil
}

func invokeRowsMove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "move row"
	input, _, err := prepareChange(op, resolved, raw, true, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if input.BeforeID > 0 {
		query.Set("before_id", strconv.FormatInt(input.BeforeID, 10))
	}
	if _, err := client.sendOnce(ctx, op, http.MethodPatch, rowChangePath(input.TableID, input.RowID)+"move/", query,
		nil, "update"); err != nil {
		return nil, err
	}
	return &MoveResult{Moved: true, RowID: input.RowID}, nil
}

// sendOnce sends one request without a retry and returns the bounded answer body. A failure that leaves the
// result open (timeout, reset, unknown transport failure, 5xx, unreadable answer) carries the uncertain hint.
// The provider's body is never copied into an error.
func (c *Client) sendOnce(ctx context.Context, op, method, path string, query url.Values, body []byte,
	right string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	return c.send(ctx, c.http, op, method, path, query, reader, "application/json", right, rowUncertain)
}

// send is sendOnce for any body: the client carries the timeout, contentType names the body, and uncertain is
// the hint of a failure whose result may be open. A body of a known length is the caller's to announce.
func (c *Client) send(ctx context.Context, hc *http.Client, op, method, path string, query url.Values, body io.Reader,
	contentType, right, uncertain string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Baserow", err)
	}
	endpoint := c.origin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if length, ok := sizedBody(body); ok {
		req.ContentLength = length
	}
	response, err := hc.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Baserow", err)
		if failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown {
			failure.Message += uncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusErrorFor(op, response, right)
		if response.StatusCode >= 500 {
			failure.Message += uncertain
		}
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return nil, invalidResponse(op, "the Baserow response could not be read within the size limit"+uncertain)
	}
	return data, nil
}

// sizedBody reports the length of a body that announces it, which is how a hand-framed upload sets its
// Content-Length.
func sizedBody(body io.Reader) (int64, bool) {
	if sized, ok := body.(interface{ announcedLength() int64 }); ok {
		return sized.announcedLength(), true
	}
	return 0, false
}
