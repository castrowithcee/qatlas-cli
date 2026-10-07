package seatable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// sqlPath is the query route of the API gateway. Qatlas never forwards agent-written SQL to it: the only
// statement it sends is the one buildSearch assembles from validated parts.
const sqlPath = "/sql/"

// The bounds of one structured search. The offset reaches well beyond the 10000 rows rows.list can page
// through, because SQL pages by LIMIT and OFFSET; it stays finite so one call cannot ask the base to skip an
// unbounded number of rows.
const (
	maxSearchOffset = 100000
	maxFilters      = 10
	maxSorts        = 3
	maxInValues     = 50
	maxValueLength  = 1024
)

// operators maps the readable operator names an agent uses to the SQL Qatlas emits. It is the complete
// allow-list; every other name is refused before a request is made.
var operators = map[string]string{
	"eq": "=", "ne": "!=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=",
	"like": "LIKE", "is_null": "IS NULL", "in": "IN",
}

// systemColumns are the columns SeaTable adds to every table. They are not part of the column metadata.
var systemColumns = map[string]bool{keyID: true, keyCreated: true, keyUpdated: true}

var rowsSearch = capability.Descriptor{
	ID:      Provider + ".rows.search",
	Version: 1,
	Title:   "Search SeaTable rows",
	Description: "Filter, sort and page the rows of a table allowed by an explicit SeaTable connection with " +
		"structured conditions; it searches the whole table and does not apply a configured view",
	Tags:     []string{"seatable", "base", "rows", "search", "filter", "sort", "table"},
	Risk:     seatableReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"table":` + tableSelectionSchema + `,` +
		`"filters":{"type":"array","items":{"type":"object","properties":{` +
		`"column":{"type":"string","minLength":1,"maxLength":255},` +
		`"op":{"type":"string","enum":["eq","ne","lt","lte","gt","gte","like","is_null","in"]},` +
		`"value":{},"values":{"type":"array"}},` +
		`"required":["column","op"],"additionalProperties":false}},` +
		`"sort":{"type":"array","items":{"type":"object","properties":{` +
		`"column":{"type":"string","minLength":1,"maxLength":255},` +
		`"direction":{"type":"string","enum":["asc","desc"]}},` +
		`"required":["column"],"additionalProperties":false}},` +
		`"start":{"type":"integer","minimum":0,"maximum":100000},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100}},` +
		`"additionalProperties":false}`),
	OutputSchema: rowsList.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
		{Name: "filters", Description: "Up to 10 conditions combined with AND: column, op (eq, ne, lt, lte, gt, gte, like, is_null, in) and value; in takes values (up to 50); is_null takes none"},
		{Name: "sort", Description: "Up to 3 sort keys: column and direction asc (default) or desc"},
		{Name: "start", Description: "Zero-based row offset, up to 100000"},
		{Name: "limit", Description: "Rows per page, from 1 through 100; 25 when omitted"},
	},
	Fields: rowsList.Fields,
	Examples: []capability.Example{{
		Description: "Find rows by a text value and read the newest first",
		Arguments: json.RawMessage(`{"filters":[{"column":"Name","op":"like","value":"Bike%"}],` +
			`"sort":[{"column":"_ctime","direction":"desc"}],"limit":25}`),
	}},
}

// SearchFilter is one condition of a search. Value carries one scalar, Values the list of the in operator.
type SearchFilter struct {
	Column string            `json:"column"`
	Op     string            `json:"op"`
	Value  json.RawMessage   `json:"value,omitempty"`
	Values []json.RawMessage `json:"values,omitempty"`
}

// SearchSort is one sort key of a search.
type SearchSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction,omitempty"`
}

// SearchOptions are the structured arguments of a row search. The selected table cannot escape the
// connection scope, and no member is ever a piece of SQL.
type SearchOptions struct {
	Table   string         `json:"table"`
	Filters []SearchFilter `json:"filters"`
	Sort    []SearchSort   `json:"sort"`
	Start   int            `json:"start"`
	Limit   int            `json:"limit"`
}

func invokeRowsSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "search rows"
	var options SearchOptions
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
	return client.SearchRows(ctx, options)
}

// safeIdentifier reports whether a name may be quoted into the statement. A backtick would end the quoting
// and a backslash may escape it, so neither is ever accepted.
func safeIdentifier(name string) bool {
	return validName(name) && !strings.ContainsAny(name, "`\\")
}

// normalize applies the defaults and validates everything that needs no provider knowledge. The
// application core validates the input schema first; this keeps a direct caller inside the same rules.
func (o *SearchOptions) normalize() error {
	if o.Limit == 0 {
		o.Limit = defaultPageSize
	}
	if o.Limit < 1 || o.Limit > maxPageSize {
		return fmt.Errorf("the page size must be between 1 and %d", maxPageSize)
	}
	if o.Start < 0 || o.Start > maxSearchOffset {
		return fmt.Errorf("the row offset must be between 0 and %d", maxSearchOffset)
	}
	if len(o.Filters) > maxFilters {
		return fmt.Errorf("a search accepts at most %d filters", maxFilters)
	}
	if len(o.Sort) > maxSorts {
		return fmt.Errorf("a search accepts at most %d sort keys", maxSorts)
	}
	for i, filter := range o.Filters {
		if !safeIdentifier(filter.Column) {
			return fmt.Errorf("filter %d names an unusable column", i+1)
		}
		if _, ok := operators[filter.Op]; !ok {
			return fmt.Errorf("filter %d uses an unsupported operator", i+1)
		}
		if _, err := filter.parameters(); err != nil {
			return fmt.Errorf("filter %d: %w", i+1, err)
		}
	}
	for i, key := range o.Sort {
		if !safeIdentifier(key.Column) {
			return fmt.Errorf("sort key %d names an unusable column", i+1)
		}
		if key.Direction != "" && key.Direction != "asc" && key.Direction != "desc" {
			return fmt.Errorf("sort key %d uses an unsupported direction", i+1)
		}
	}
	return nil
}

// parameters returns the values a filter binds, in placeholder order, after checking that the operator
// received exactly the value shape it needs.
func (f SearchFilter) parameters() ([]any, error) {
	hasValue := len(f.Value) > 0
	switch f.Op {
	case "is_null":
		if hasValue || len(f.Values) > 0 {
			return nil, errors.New("is_null takes no value")
		}
		return nil, nil
	case "in":
		if hasValue {
			return nil, errors.New("in takes values, not value")
		}
		if len(f.Values) < 1 || len(f.Values) > maxInValues {
			return nil, fmt.Errorf("in needs between 1 and %d values", maxInValues)
		}
		out := make([]any, 0, len(f.Values))
		for _, raw := range f.Values {
			value, err := scalar(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	}
	if len(f.Values) > 0 {
		return nil, errors.New("this operator takes value, not values")
	}
	if !hasValue {
		return nil, errors.New("this operator needs a value")
	}
	value, err := scalar(f.Value)
	if err != nil {
		return nil, err
	}
	if _, isString := value.(string); f.Op == "like" && !isString {
		return nil, errors.New("like needs a text value")
	}
	return []any{value}, nil
}

// scalar decodes one bound value: a string within the length bound, a number, or a boolean. Anything
// else, including null, objects and arrays, is refused.
func scalar(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.More() {
		return nil, errors.New("a value must be a string, a number or a boolean")
	}
	switch typed := value.(type) {
	case string:
		if utf8.RuneCountInString(typed) > maxValueLength {
			return nil, fmt.Errorf("a text value is longer than %d characters", maxValueLength)
		}
	case json.Number, bool:
	default:
		return nil, errors.New("a value must be a string, a number or a boolean")
	}
	return value, nil
}

// buildSearch assembles the one statement shape Qatlas sends. Table and column names come from the base
// metadata and passed safeIdentifier; every value is a placeholder bound through the parameters.
func buildSearch(options SearchOptions, table string) (string, []any, error) {
	if !safeIdentifier(table) {
		return "", nil, errors.New("the selected SeaTable table has a name this search cannot address")
	}
	var sql strings.Builder
	params := []any{}
	sql.WriteString("SELECT * FROM `" + table + "`")
	for i, filter := range options.Filters {
		if i == 0 {
			sql.WriteString(" WHERE ")
		} else {
			sql.WriteString(" AND ")
		}
		bound, err := filter.parameters()
		if err != nil {
			return "", nil, err
		}
		sql.WriteString("`" + filter.Column + "` " + operators[filter.Op])
		switch filter.Op {
		case "is_null":
		case "in":
			sql.WriteString(" (" + strings.TrimSuffix(strings.Repeat("?, ", len(bound)), ", ") + ")")
		default:
			sql.WriteString(" ?")
		}
		params = append(params, bound...)
	}
	for i, key := range options.Sort {
		if i == 0 {
			sql.WriteString(" ORDER BY ")
		} else {
			sql.WriteString(", ")
		}
		direction := "ASC"
		if key.Direction == "desc" {
			direction = "DESC"
		}
		sql.WriteString("`" + key.Column + "` " + direction)
	}
	sql.WriteString(" LIMIT " + strconv.Itoa(options.Limit) + " OFFSET " + strconv.Itoa(options.Start))
	return sql.String(), params, nil
}

// SearchRows filters, sorts and pages the rows of the selected table. It searches the table itself: a
// view configured on the target narrows rows.list but is not applied here. Column names are checked
// against the base metadata before the statement is sent.
func (c *Client) SearchRows(ctx context.Context, options SearchOptions) (*ListResult, error) {
	const op = "search rows"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	selected, err := c.scope.selectTarget(options.Table)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, err
	}
	document, err := c.metadata(ctx, op)
	if err != nil {
		return nil, err
	}
	var current *tableJSON
	for i := range document.Metadata.Tables {
		if matchesTable(selected, document.Metadata.Tables[i]) {
			current = &document.Metadata.Tables[i]
			break
		}
	}
	if current == nil {
		return nil, providerError(op, "the selected SeaTable table no longer exists")
	}
	if err := checkSearchColumns(options, current); err != nil {
		return nil, providerError(op, err.Error())
	}
	statement, params, err := buildSearch(options, current.Name)
	if err != nil {
		return nil, providerError(op, err.Error())
	}

	body, err := json.Marshal(map[string]any{"sql": statement, "parameters": params, "convert_keys": true})
	if err != nil || len(body) > maxRequestBytes {
		return nil, providerError(op, "the request exceeds the size limit")
	}
	var page struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := c.post(ctx, op, gatewayPath+url.PathEscape(access.uuid)+sqlPath, access.token, body, &page); err != nil {
		return nil, err
	}
	if len(page.Results) > options.Limit {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned more rows than the requested page allows",
		}
	}
	rows := make([]Row, 0, len(page.Results))
	for _, raw := range page.Results {
		row, err := normalizeRow(op, raw)
		if err != nil {
			return nil, err
		}
		rows = append(rows, *row)
	}
	if err := c.maskSearchLinks(op, document, current, rows); err != nil {
		return nil, err
	}
	// The generic pass also catches a link-shaped value in a column the metadata types differently.
	if err := c.maskLinks(ctx, op, selected, rows); err != nil {
		return nil, err
	}

	result := &ListResult{Rows: rows, Start: options.Start, Limit: options.Limit, HasMore: len(rows) == options.Limit}
	if result.HasMore {
		result.NextStart = options.Start + len(rows)
	}
	return result, nil
}

// checkSearchColumns requires every filter and sort column to be a column of the table or a system
// column. Link columns are refused for both: a condition on them would answer questions about a linked
// table this connection may not read.
func checkSearchColumns(options SearchOptions, table *tableJSON) error {
	types := make(map[string]string, len(table.Columns))
	for _, column := range table.Columns {
		types[column.Name] = column.Type
	}
	check := func(kind string, index int, name string) error {
		kindOf, known := types[name]
		if !known && !systemColumns[name] {
			return fmt.Errorf("%s %d names a column the selected SeaTable table does not define", kind, index+1)
		}
		if kindOf == "link" || kindOf == "link-formula" {
			return fmt.Errorf("%s %d names a link column, which a search cannot use", kind, index+1)
		}
		return nil
	}
	for i, filter := range options.Filters {
		if err := check("filter", i, filter.Column); err != nil {
			return err
		}
	}
	for i, key := range options.Sort {
		if err := check("sort key", i, key.Column); err != nil {
			return err
		}
	}
	return nil
}

// maskSearchLinks applies the table boundary to the link columns of a search result. The columns are
// found by their type in the metadata. A link column into a table outside the allow-list keeps only the
// row identifiers of its entries; a value that is not the expected array of entries with a row_id is
// left out of the row, never passed on.
func (c *Client) maskSearchLinks(op string, document metadataJSON, current *tableJSON, rows []Row) error {
	if c.scope.wildcard {
		return nil
	}
	for _, column := range current.Columns {
		if column.Type != "link" || c.linkAllowed(document, current, column.Name) {
			continue
		}
		for _, row := range rows {
			value, present := row.Values[column.Name]
			if !present {
				continue
			}
			ids, ok := linkRowIDs(value)
			if !ok {
				delete(row.Values, column.Name)
				continue
			}
			masked := make([]map[string]string, 0, len(ids))
			for _, id := range ids {
				masked = append(masked, map[string]string{"row_id": id})
			}
			encoded, err := json.Marshal(masked)
			if err != nil {
				return providerError(op, "a link value could not be masked")
			}
			row.Values[column.Name] = encoded
		}
	}
	return nil
}

// linkRowIDs reads the row identifiers of a link cell. An empty array is a valid empty link; a cell that
// is not an array, or a non-empty array without one usable row_id, reports false.
func linkRowIDs(value json.RawMessage) ([]string, bool) {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) != nil {
		return nil, false
	}
	ids := []string{}
	for _, item := range items {
		var entry map[string]json.RawMessage
		if json.Unmarshal(item, &entry) != nil {
			continue
		}
		if id := decodeString(entry["row_id"]); id != "" && len(id) <= maxIDLength {
			ids = append(ids, id)
		}
	}
	if len(items) > 0 && len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// post sends one bounded JSON request to a base route and decodes the answer.
func (c *Client) post(ctx context.Context, op, path, token string, body []byte, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "SeaTable", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "SeaTable", err)
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode != http.StatusOK {
		return statusError(op, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "the SeaTable response could not be read within the size limit",
		}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op, Message: "SeaTable returned an invalid response",
		}
	}
	return nil
}
