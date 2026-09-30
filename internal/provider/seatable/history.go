package seatable

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// historySensitivity classifies the change history of a base: old and new cell values and the people who
// made the changes. It is never written to the audit trail or the invoke log.
const historySensitivity = "seatable-base-history"

// The routes and bounds of the history operations. The page number is bounded so one call cannot ask for
// an unbounded skip, every reported string is shortened, and nesting beyond maxHistoryDepth is dropped.
const (
	activitiesPath = "/activities/"
	operationsPath = "/operations/"

	maxHistoryPage  = 1000
	maxHistoryDepth = 8
)

var seatableHistoryRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: historySensitivity,
}

const historyOutputSchema = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object"}},` +
	`"page":{"type":"integer"},"per_page":{"type":"integer"},"has_more":{"type":"boolean"}},` +
	`"required":["items","page","has_more"],"additionalProperties":false}`

var rowsActivities = capability.Descriptor{
	ID:      Provider + ".rows.activities",
	Version: 1,
	Title:   "List SeaTable row activities",
	Description: "Read the change history of one row of a table allowed by an explicit SeaTable connection; " +
		"values of links into tables outside the connection are reduced to row identifiers",
	Tags:     []string{"seatable", "base", "rows", "activities", "history", "table"},
	Risk:     seatableHistoryRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table":` + tableSelectionSchema +
		`,"row_id":` + rowIDSchema + `,"page":{"type":"integer","minimum":1,"maximum":1000},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":100}},"required":["row_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(historyOutputSchema),
	Arguments: []capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "row_id", Description: "Row identifier of 22 characters; the row must exist in the selected table", Required: true},
		{Name: "page", Description: "One-based page number, up to 1000; 1 when omitted"},
		{Name: "per_page", Description: "Activities per page, from 1 through 100; 25 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "items", Description: "Activities as SeaTable reports them, with old and new values and authors, untrusted data"},
		{Name: "page", Description: "Page number the request applied"},
		{Name: "per_page", Description: "Page size the request applied"},
		{Name: "has_more", Description: "True when the page is full and a next page may exist"},
	},
	Examples: []capability.Example{{
		Description: "Read the newest activities of one row",
		Arguments:   json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","per_page":25}`),
	}},
}

var baseOperations = capability.Descriptor{
	ID:      Provider + ".base.operations",
	Version: 1,
	Title:   "List SeaTable base operations",
	Description: "Read one page of the operation log of the whole SeaTable base; only a connection with the " +
		"* wildcard may use it",
	Tags:     []string{"seatable", "base", "operations", "log", "history"},
	Risk:     seatableHistoryRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer","minimum":1,` +
		`"maximum":1000}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(historyOutputSchema),
	Arguments: []capability.Argument{
		{Name: "page", Description: "One-based page number, up to 1000; 1 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "items", Description: "Logged operations as SeaTable reports them, with old and new values and people, untrusted data; at most 100 per call"},
		{Name: "page", Description: "Page number the request applied"},
		{Name: "has_more", Description: "True when this call left reported entries out because of its size limit"},
	},
	Examples: []capability.Example{{
		Description: "Read the newest operations of the base",
		Arguments:   json.RawMessage(`{"page":1}`),
	}},
}

// ActivitiesOptions select one row of one allowed table and one bounded page of its history.
type ActivitiesOptions struct {
	Table   string `json:"table"`
	RowID   string `json:"row_id"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}

// OperationsOptions select one page of the operation log of the base.
type OperationsOptions struct {
	Page int `json:"page"`
}

// HistoryResult is one bounded page of history entries. Entries are provider data and untrusted.
type HistoryResult struct {
	Items   []json.RawMessage `json:"items"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page,omitempty"`
	HasMore bool              `json:"has_more"`
}

func normalizePage(page int) (int, error) {
	if page == 0 {
		return 1, nil
	}
	if page < 1 || page > maxHistoryPage {
		return 0, fmt.Errorf("the page number must be between 1 and %d", maxHistoryPage)
	}
	return page, nil
}

func (o *ActivitiesOptions) normalize() error {
	if !validRowID(o.RowID) {
		return fmt.Errorf("a SeaTable row identifier has 22 letters, digits, '-' or '_'")
	}
	page, err := normalizePage(o.Page)
	if err != nil {
		return err
	}
	o.Page = page
	if o.PerPage == 0 {
		o.PerPage = defaultPageSize
	}
	if o.PerPage < 1 || o.PerPage > maxPageSize {
		return fmt.Errorf("the page size must be between 1 and %d", maxPageSize)
	}
	return nil
}

func invokeRowsActivities(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list row activities"
	var options ActivitiesOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	// The request shape and the table boundary are settled before the credential is resolved.
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(options.Table); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RowActivities(ctx, options)
}

func invokeBaseOperations(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list base operations"
	var options OperationsOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	page, err := normalizePage(options.Page)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	options.Page = page
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	// The log covers the whole base, so a connection bound to single tables is refused before the
	// credential is resolved.
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if !bound.wildcard {
		return nil, providerError(op, "the operation log covers the whole base and needs a connection with the * wildcard")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.BaseOperations(ctx, options)
}

// RowActivities reads the history of one row. Before the history is requested, the row itself is read in
// the selected table, which proves it belongs to a table the connection allows.
func (c *Client) RowActivities(ctx context.Context, options ActivitiesOptions) (*HistoryResult, error) {
	const op = "list row activities"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	selected, err := c.scope.selectTarget(options.Table)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if _, err := c.GetRowFrom(ctx, options.Table, options.RowID); err != nil {
		return nil, err
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set(selected.tableParam, selected.table)
	query.Set("row_id", options.RowID)
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("per_page", strconv.Itoa(options.PerPage))

	var body map[string]json.RawMessage
	if err := c.get(ctx, op, gatewayPath+url.PathEscape(access.uuid)+activitiesPath, query,
		access.token, maxResponseBytes, &body); err != nil {
		return nil, err
	}
	items, err := historyItems(op, body["activities"])
	if err != nil {
		return nil, err
	}
	if len(items) > options.PerPage {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned more activities than the requested page allows"}
	}
	pageFull := len(items) == options.PerPage
	var document metadataJSON
	var current *tableJSON
	loaded := false
	load := func() error {
		if loaded {
			return nil
		}
		var err error
		if document, err = c.metadata(ctx, op); err != nil {
			return err
		}
		for i := range document.Metadata.Tables {
			if matchesTable(selected, document.Metadata.Tables[i]) {
				current = &document.Metadata.Tables[i]
				break
			}
		}
		loaded = true
		return nil
	}
	// The request names the row, but an entry that names another row or table is never reported.
	kept := items[:0:0]
	for _, item := range items {
		if !entryRowMatches(item, options.RowID) {
			continue
		}
		if hasTableField(item) {
			if err := load(); err != nil {
				return nil, err
			}
			if !entryTableMatches(item, current) {
				continue
			}
		}
		kept = append(kept, item)
	}
	items = kept
	var mask *linkMask
	if !c.scope.wildcard && hasLinkShape(items) {
		if err := load(); err != nil {
			return nil, err
		}
		mask = &linkMask{client: c, document: document, current: current}
	}
	result, err := encodeItems(op, items, mask)
	if err != nil {
		return nil, err
	}
	return &HistoryResult{Items: result, Page: options.Page, PerPage: options.PerPage,
		HasMore: pageFull}, nil
}

// BaseOperations reads one page of the base operation log. Only a wildcard connection may read it.
func (c *Client) BaseOperations(ctx context.Context, options OperationsOptions) (*HistoryResult, error) {
	const op = "list base operations"
	page, err := normalizePage(options.Page)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if !c.scope.wildcard {
		return nil, providerError(op, "the operation log covers the whole base and needs a connection with the * wildcard")
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	var body map[string]json.RawMessage
	if err := c.get(ctx, op, gatewayPath+url.PathEscape(access.uuid)+operationsPath, query,
		access.token, maxResponseBytes, &body); err != nil {
		return nil, err
	}
	items, err := historyItems(op, body["operations"])
	if err != nil {
		return nil, err
	}
	more := len(items) > maxPageSize
	if more {
		items = items[:maxPageSize]
	}
	result, err := encodeItems(op, items, nil)
	if err != nil {
		return nil, err
	}
	return &HistoryResult{Items: result, Page: page, HasMore: more}, nil
}

// historyItems decodes the entry list of an answer; every entry has to be an object.
func historyItems(op string, raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned an invalid response"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var items []map[string]any
	if err := decoder.Decode(&items); err != nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned an invalid response"}
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, nil
}

func encodeItems(op string, items []map[string]any, mask *linkMask) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		encoded, err := json.Marshal(shorten(item, mask, 0))
		if err != nil {
			return nil, providerError(op, "a history entry could not be encoded")
		}
		out = append(out, encoded)
	}
	return out, nil
}

// linkMask decides which link values of a row history may keep their display values.
type linkMask struct {
	client   *Client
	document metadataJSON
	current  *tableJSON
}

// allowed reports whether the link column behind a key points into the allow-list. The key may be the
// name or the internal key of a column; an unknown key is not allowed.
func (m *linkMask) allowed(key string) bool {
	if m.current == nil {
		return false
	}
	for _, column := range m.current.Columns {
		if column.Name == key || (column.Key != "" && column.Key == key) {
			return m.client.linkAllowed(m.document, m.current, column.Name)
		}
	}
	return false
}

// historyLinkIDs reports the row identifiers of a list of link entries: at least one object carries a row_id.
func historyLinkIDs(list []any) ([]string, bool) {
	ids := []string{}
	found := false
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		raw, ok := entry["row_id"]
		if !ok {
			continue
		}
		found = true
		if id, ok := raw.(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids, found
}

func hasLinkShape(value any) bool {
	switch typed := value.(type) {
	case []map[string]any:
		for _, item := range typed {
			if hasLinkShape(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if hasLinkShape(item) {
				return true
			}
		}
	case []any:
		if _, ok := historyLinkIDs(typed); ok {
			return true
		}
		for _, item := range typed {
			if hasLinkShape(item) {
				return true
			}
		}
	}
	return false
}

// shorten bounds strings and nesting of a history entry and masks link values the connection may not
// read. Without a mask, every value stays as SeaTable reported it.
func shorten(value any, mask *linkMask, depth int) any {
	if depth > maxHistoryDepth {
		return nil
	}
	switch typed := value.(type) {
	case string:
		return clipString(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if list, ok := item.([]any); ok && mask != nil {
				if ids, found := historyLinkIDs(list); found && !mask.allowed(key) {
					masked := make([]any, 0, len(ids))
					for _, id := range ids {
						masked = append(masked, map[string]any{"row_id": clipString(id)})
					}
					out[key] = masked
					continue
				}
			}
			out[key] = shorten(item, mask, depth+1)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, shorten(item, mask, depth+1))
		}
		return out
	}
	return value
}

func clipString(value string) string {
	if len(value) <= maxDisplayBytes {
		return value
	}
	cut := maxDisplayBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.ToValidUTF8(value[:cut], "") + "..."
}

// entryRowMatches keeps an entry that carries no row identifier or exactly the requested one.
func entryRowMatches(item map[string]any, rowID string) bool {
	raw, ok := item["row_id"]
	if !ok {
		return true
	}
	id, ok := raw.(string)
	return ok && id == rowID
}

func hasTableField(item map[string]any) bool {
	_, id := item["table_id"]
	_, name := item["table_name"]
	return id || name
}

// entryTableMatches keeps an entry whose table identifier and table name, where present, name the selected
// table. A table that cannot be determined from the metadata matches nothing.
func entryTableMatches(item map[string]any, current *tableJSON) bool {
	if current == nil {
		return false
	}
	if raw, ok := item["table_id"]; ok {
		if id, ok := raw.(string); !ok || id != current.ID {
			return false
		}
	}
	if raw, ok := item["table_name"]; ok {
		if name, ok := raw.(string); !ok || name != current.Name {
			return false
		}
	}
	return true
}
