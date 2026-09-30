package seatable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

// The routes and bounds of the link operations. A link joins rows of two tables; Qatlas takes the link
// identifier and the counterpart table from the base metadata only, never from an argument.
const (
	queryLinksPath = "/query-links/"
	linksPath      = "/links/"

	// maxLinkSourceRows bounds the rows one links.list call reads, maxLinkTargets the rows one mutation
	// links to a source row, and maxDisplayBytes one reported display value.
	maxLinkSourceRows = 10
	maxLinkTargets    = 50
	maxDisplayBytes   = 4096

	// linkUncertain is appended to a failure of a link change whose request may have reached SeaTable.
	// Qatlas never repeats such a request by itself.
	linkUncertain = "; this change may have taken effect, read the links before repeating it"
)

const (
	rowIDSchema   = `{"type":"string","minLength":22,"maxLength":22,"pattern":"^[A-Za-z0-9_-]{22}$"}`
	columnSchema  = `{"type":"string","minLength":1,"maxLength":255}`
	linkSchemaEnd = `"additionalProperties":false}`
)

func linkMutationInput(minTargets int) string {
	return `{"type":"object","properties":{"table":` + tableSelectionSchema + `,"column":` + columnSchema +
		`,"row_id":` + rowIDSchema + `,"other_row_ids":{"type":"array","minItems":` + strconv.Itoa(minTargets) +
		`,"maxItems":50,"items":` + rowIDSchema + `}},"required":["column","row_id","other_row_ids"],` +
		linkSchemaEnd
}

var linksList = capability.Descriptor{
	ID:      Provider + ".links.list",
	Version: 1,
	Title:   "List SeaTable links",
	Description: "List the rows linked to up to 10 rows of an allowed table through one link column; the " +
		"linked table must be allowed by the connection as well",
	Tags:     []string{"seatable", "base", "links", "list", "rows", "table"},
	Risk:     seatableReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table":` + tableSelectionSchema +
		`,"column":` + columnSchema + `,"row_ids":{"type":"array","minItems":1,"maxItems":10,"items":` + rowIDSchema +
		`},"start":{"type":"integer","minimum":0,"maximum":10000},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["column","row_ids"],` + linkSchemaEnd),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"table":{"type":"string"},` +
		`"column":{"type":"string"},"rows":{"type":"array","items":{"type":"object","properties":{` +
		`"row_id":{"type":"string"},"links":{"type":"array","items":{"type":"object","properties":{` +
		`"row_id":{"type":"string"},"display_value":{}},"required":["row_id"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"}},"required":["row_id","links","has_more"],"additionalProperties":false}},` +
		`"start":{"type":"integer"},"limit":{"type":"integer"}},` +
		`"required":["table","column","rows","start","limit"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "column", Description: "Name or key of a link column of the table, as returned by seatable.columns.list", Required: true},
		{Name: "row_ids", Description: "1 to 10 row identifiers of 22 characters in the table", Required: true},
		{Name: "start", Description: "Zero-based offset of the linked rows per source row, up to 10000"},
		{Name: "limit", Description: "Linked rows per source row, from 1 through 100; 25 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "table", Description: "The selected table reference"},
		{Name: "column", Description: "The selected link column"},
		{Name: "rows", Description: "Per source row the linked rows with their display values, untrusted data"},
		{Name: "start", Description: "Offset the request applied"},
		{Name: "limit", Description: "Page size the request applied per source row"},
	},
	Examples: []capability.Example{{
		Description: "Read the rows linked to one row through a link column",
		Arguments:   json.RawMessage(`{"column":"Tickets","row_ids":["Qtf7xPmoRaiFyQPO1aENTj"]}`),
	}},
}

var linksCreate = linkMutationDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent,
	false, "Add links from one row to up to 50 rows of the linked table",
	linkMutationInput(1), `{"type":"object","properties":{"created":{"type":"boolean"}},"required":["created"],"additionalProperties":false}`)
var linksUpdate = linkMutationDescriptor("update", capability.EffectUpdate, capability.IdempotencyIdempotent,
	false, "Replace all links of one row in a link column with 1 to 50 rows of the linked table",
	linkMutationInput(1), `{"type":"object","properties":{"updated":{"type":"boolean"}},"required":["updated"],"additionalProperties":false}`)
var linksDelete = linkMutationDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent,
	true, "Remove the links from one row to up to 50 rows of the linked table",
	linkMutationInput(1), `{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`)

func linkMutationDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency,
	allowList bool, what, input, output string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".links." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " SeaTable links",
		Description: what + "; the table and the linked table must both be allowed by the connection",
		Tags:        []string{"seatable", "base", "links", action, "rows", "table"},
		Provider:    Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		Arguments: []capability.Argument{
			{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
			{Name: "column", Description: "Name or key of a link column of the table", Required: true},
			{Name: "row_id", Description: "Row identifier of 22 characters of the source row", Required: true},
			{Name: "other_row_ids", Description: "1 to 50 row identifiers of 22 characters in the linked table", Required: true},
		},
		Fields: []capability.Field{{Name: action + "d", Description: "True when SeaTable accepted the change"}},
	}
}

// linkInput carries the arguments of all four link tools.
type linkInput struct {
	Table       string   `json:"table"`
	Column      string   `json:"column"`
	RowIDs      []string `json:"row_ids"`
	RowID       string   `json:"row_id"`
	OtherRowIDs []string `json:"other_row_ids"`
	Start       int      `json:"start"`
	Limit       int      `json:"limit"`
}

// openForLink decodes the arguments and settles the connection's own table boundary before the credential
// is resolved, like the search does.
func openForLink(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, linkInput, error) {
	var input linkInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, input, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(input.Table); err != nil {
		return nil, input, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

// checkLinkMutationShape validates the row identifiers of a link change. Every change needs 1 to 50 target
// rows: an empty list would make update remove links, which only delete may do.
func checkLinkMutationShape(op string, input linkInput) error {
	if !validRowID(input.RowID) {
		return providerError(op, "a SeaTable row identifier has 22 letters, digits, '-' or '_'")
	}
	return checkRowIDs(op, input.OtherRowIDs, 1, maxLinkTargets)
}

func invokeLinksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForLink(ctx, "list links", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return client.ListLinks(ctx, input)
}

func invokeLinksChange(op, method string, done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var shape linkInput
		if json.Unmarshal(raw, &shape) != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
		if err := checkLinkMutationShape(op, shape); err != nil {
			return nil, err
		}
		client, input, err := openForLink(ctx, op, resolved, secrets, red, raw)
		if err != nil {
			return nil, err
		}
		if err := client.ChangeLinks(ctx, op, method, input); err != nil {
			return nil, err
		}
		return map[string]bool{done: true}, nil
	}
}

// linkRef is the link a column stands for, resolved from the base metadata.
type linkRef struct {
	table, other, linkID, columnKey, columnName string
}

// resolveLink finds the link column in the selected table and the counterpart table it joins. Both tables
// must be inside the connection's allow-list. The metadata read is the only provider I/O that precedes a
// refusal, and the message never names the counterpart table.
func (c *Client) resolveLink(ctx context.Context, op string, selected target, column string) (*linkRef, *baseAccess, error) {
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, nil, err
	}
	document, err := c.metadata(ctx, op)
	if err != nil {
		return nil, nil, err
	}
	var current *tableJSON
	for i := range document.Metadata.Tables {
		if matchesTable(selected, document.Metadata.Tables[i]) {
			current = &document.Metadata.Tables[i]
			break
		}
	}
	if current == nil {
		return nil, nil, providerError(op, "the selected SeaTable table no longer exists")
	}
	var found *columnJSON
	for _, byKey := range []bool{false, true} {
		for i := range current.Columns {
			candidate := &current.Columns[i]
			if (!byKey && candidate.Name == column) || (byKey && candidate.Key == column) {
				found = candidate
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil || found.Type != "link" {
		return nil, nil, providerError(op, "the selected SeaTable table has no link column with this name or key")
	}
	first, second := found.Data.TableID, found.Data.OtherTableID
	var other string
	switch {
	case first == current.ID && second != "":
		other = second
	case second == current.ID && first != "":
		other = first
	default:
		return nil, nil, providerError(op, "the link column does not name the joined tables")
	}
	if !validID(current.ID) || !validID(other) || !validID(found.Data.LinkID) || !validID(found.Key) {
		return nil, nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned a link column without usable identifiers"}
	}
	if !c.tableAllowed(document, other) {
		return nil, nil, providerError(op, "the link column joins a table outside this connection's allow-list")
	}
	return &linkRef{table: current.ID, other: other, linkID: found.Data.LinkID,
		columnKey: found.Key, columnName: found.Name}, access, nil
}

// tableAllowed reports whether the table with this identifier is inside the connection's allow-list.
func (c *Client) tableAllowed(document metadataJSON, id string) bool {
	for _, table := range document.Metadata.Tables {
		if table.ID != id {
			continue
		}
		if c.scope.wildcard {
			return true
		}
		for _, allowed := range c.scope.targets {
			if matchesTable(allowed, table) {
				return true
			}
		}
	}
	return false
}

func checkRowIDs(op string, ids []string, fewest, most int) error {
	if len(ids) < fewest || len(ids) > most {
		return providerError(op, "the number of row identifiers is outside the allowed range")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validRowID(id) {
			return providerError(op, "a SeaTable row identifier has 22 letters, digits, '-' or '_'")
		}
		if seen[id] {
			return providerError(op, "a row identifier is listed twice")
		}
		seen[id] = true
	}
	return nil
}

// LinkedRow is one row reached through a link. DisplayValue is untrusted provider content.
type LinkedRow struct {
	RowID        string          `json:"row_id"`
	DisplayValue json.RawMessage `json:"display_value,omitempty"`
}

// LinkedRows are the links of one source row.
type LinkedRows struct {
	RowID   string      `json:"row_id"`
	Links   []LinkedRow `json:"links"`
	HasMore bool        `json:"has_more"`
}

// LinksResult is the normalised answer of links.list.
type LinksResult struct {
	Table  string       `json:"table"`
	Column string       `json:"column"`
	Rows   []LinkedRows `json:"rows"`
	Start  int          `json:"start"`
	Limit  int          `json:"limit"`
}

// ListLinks reads the rows linked to the given rows through one link column.
func (c *Client) ListLinks(ctx context.Context, input linkInput) (*LinksResult, error) {
	const op = "list links"
	page := PageOptions{Start: input.Start, Limit: input.Limit}
	if err := page.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	if err := checkRowIDs(op, input.RowIDs, 1, maxLinkSourceRows); err != nil {
		return nil, err
	}
	selected, err := c.scope.selectTarget(input.Table)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	ref, access, err := c.resolveLink(ctx, op, selected, input.Column)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(input.RowIDs))
	for i, id := range input.RowIDs {
		rows[i] = map[string]any{"row_id": id, "offset": page.Start, "limit": page.Limit}
	}
	body, err := json.Marshal(map[string]any{"table_id": ref.table, "link_column_key": ref.columnKey, "rows": rows})
	if err != nil || len(body) > maxRequestBytes {
		return nil, providerError(op, "the request exceeds the size limit")
	}
	var answer map[string]json.RawMessage
	if err := c.post(ctx, op, gatewayPath+url.PathEscape(access.uuid)+queryLinksPath, access.token, body, &answer); err != nil {
		return nil, err
	}
	result := &LinksResult{Table: formatTarget(selected), Column: ref.columnName,
		Rows: make([]LinkedRows, 0, len(input.RowIDs)), Start: page.Start, Limit: page.Limit}
	for _, id := range input.RowIDs {
		entries := []LinkedRow{}
		if raw, ok := answer[id]; ok {
			var items []map[string]json.RawMessage
			if json.Unmarshal(raw, &items) != nil || len(items) > page.Limit {
				return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
					Message: "SeaTable returned links in an unexpected form"}
			}
			for _, item := range items {
				linked := decodeString(item["row_id"])
				if linked == "" || len(linked) > maxIDLength {
					return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
						Message: "SeaTable returned a link without a usable row identifier"}
				}
				entry := LinkedRow{RowID: linked}
				if value := bytes.TrimSpace(item["display_value"]); len(value) > 0 && len(value) <= maxDisplayBytes &&
					string(value) != "null" {
					entry.DisplayValue = value
				}
				entries = append(entries, entry)
			}
		}
		result.Rows = append(result.Rows, LinkedRows{RowID: id, Links: entries, HasMore: len(entries) == page.Limit})
	}
	return result, nil
}

// ChangeLinks sends exactly one create, update or delete request for the links of one row. The link
// identifier and both table identifiers come from the metadata.
func (c *Client) ChangeLinks(ctx context.Context, op, method string, input linkInput) error {
	if err := checkLinkMutationShape(op, input); err != nil {
		return err
	}
	selected, err := c.scope.selectTarget(input.Table)
	if err != nil {
		return providerError(op, err.Error())
	}
	ref, access, err := c.resolveLink(ctx, op, selected, input.Column)
	if err != nil {
		return err
	}
	others := input.OtherRowIDs
	body, err := json.Marshal(map[string]any{
		"table_id": ref.table, "other_table_id": ref.other, "link_id": ref.linkID,
		"other_rows_ids_map": map[string][]string{input.RowID: others},
	})
	if err != nil || len(body) > maxRequestBytes {
		return providerError(op, "the request exceeds the size limit")
	}
	return c.changeLink(ctx, op, method, gatewayPath+url.PathEscape(access.uuid)+linksPath, access.token, body)
}

// changeLink sends one link change. It never repeats the request; a failure that leaves the result open
// is reported as uncertain.
func (c *Client) changeLink(ctx context.Context, op, method, path, token string, body []byte) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "SeaTable", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		failure := transportError(op, err)
		var providerErr *provider.Error
		if errors.As(failure, &providerErr) && (providerErr.Class == provider.ClassTimeout ||
			providerErr.Cause == provider.CauseConnectionReset || providerErr.Cause == provider.CauseUnknown) {
			providerErr.Message += linkUncertain
		}
		return failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := statusError(op, response.StatusCode)
		if response.StatusCode >= 500 {
			var providerErr *provider.Error
			if errors.As(failure, &providerErr) {
				providerErr.Message += linkUncertain
			}
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the SeaTable response could not be read within the size limit" + linkUncertain}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var answer struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(data, &answer) != nil {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned an invalid response" + linkUncertain}
	}
	if answer.Success != nil && !*answer.Success {
		return providerError(op, "SeaTable did not apply the link change")
	}
	return nil
}
