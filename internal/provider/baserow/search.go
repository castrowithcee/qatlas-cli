package baserow

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxFilters     = 10
	maxOrderFields = 5
	maxSearchLen   = 256
	maxFilterValue = 256
	maxViews       = 1000
)

// filterTypes is the fixed allow-list of Baserow filter types; the value says whether the type takes a value.
var filterTypes = map[string]bool{
	"equal": true, "not_equal": true, "contains": true, "contains_not": true,
	"higher_than": true, "higher_than_or_equal": true, "lower_than": true, "lower_than_or_equal": true,
	"empty": false, "not_empty": false,
}

var searchModes = []string{"full-text", "full-text-with-count", "compat"}

// searchableFieldTypes are the field types a filter or an ordering may name. Link, lookup, formula, rollup,
// and count fields are left out: they can reach rows of other tables, so a filter on them would tell whether
// a value exists there.
var searchableFieldTypes = map[string]bool{
	"text": true, "long_text": true, "number": true, "boolean": true, "date": true, "rating": true,
	"single_select": true, "email": true, "url": true, "phone_number": true, "autonumber": true,
	"uuid": true, "created_on": true, "last_modified": true,
}

// indirectFieldTypes can carry values of other tables.
var indirectFieldTypes = map[string]bool{"link_row": true, "lookup": true, "formula": true, "rollup": true, "count": true}

var rowsSearch = capability.Descriptor{
	ID: Provider + ".rows.search", Version: 1, Title: "Search Baserow rows",
	Description: "Search, filter, and sort the rows of one table, page by page. Filters use a fixed list of types " +
		"on fields the table's schema confirms; link, lookup, and formula fields cannot be filtered or sorted. " +
		"A view applies only when it belongs to the table. " + linkNote,
	Tags: []string{"baserow", "rows", "search", "filter", "sort", "records"}, Risk: rowsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,` +
		`"search":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSearchLen) + `},` +
		`"search_mode":{"type":"string","enum":["full-text","full-text-with-count","compat"]},` +
		`"filters":{"type":"array","maxItems":` + strconv.Itoa(maxFilters) + `,"items":{"type":"object","properties":{` +
		`"field":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxStringLength) + `},` +
		`"type":{"type":"string","enum":["equal","not_equal","contains","contains_not","higher_than",` +
		`"higher_than_or_equal","lower_than","lower_than_or_equal","empty","not_empty"]},` +
		`"value":{"type":"string","maxLength":` + strconv.Itoa(maxFilterValue) + `}},` +
		`"required":["field","type"],"additionalProperties":false}},` +
		`"filter_type":{"type":"string","enum":["AND","OR"]},` +
		`"order_by":{"type":"array","maxItems":` + strconv.Itoa(maxOrderFields) + `,"items":{"type":"object","properties":{` +
		`"field":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxStringLength) + `},` +
		`"direction":{"type":"string","enum":["asc","desc"]}},"required":["field"],"additionalProperties":false}},` +
		`"view_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"size":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxSize) + `}},` +
		`"required":["table_id"],"additionalProperties":false}`),
	OutputSchema: rowsList.OutputSchema,
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "search", Description: "Text to search for in the table, up to " + strconv.Itoa(maxSearchLen) + " characters; refused on a table with link, lookup, or formula fields unless the connection targets *"},
		{Name: "search_mode", Description: "full-text (default of Baserow), full-text-with-count, or compat"},
		{Name: "filters", Description: "Up to " + strconv.Itoa(maxFilters) + " objects with field (name), type (equal, not_equal, contains, contains_not, higher_than, higher_than_or_equal, lower_than, lower_than_or_equal, empty, not_empty), and value (omitted for empty and not_empty)"},
		{Name: "filter_type", Description: "AND (default) or OR across the filters"},
		{Name: "order_by", Description: "Up to " + strconv.Itoa(maxOrderFields) + " objects with field (name) and direction asc (default) or desc"},
		{Name: "view_id", Description: "A view of this table whose filters and sorting apply"},
		{Name: "page", Description: "Page number from 1; 1 when omitted"},
		{Name: "size", Description: "Rows per page, 1 to " + strconv.Itoa(maxSize) + "; " + strconv.Itoa(defaultSize) + " when omitted"},
	},
	Fields:   rowsList.Fields,
	Examples: []capability.Example{{Description: "Open rows of a customer table, newest first", Arguments: json.RawMessage(`{"table_id":1,"filters":[{"field":"Status","type":"equal","value":"open"}],"order_by":[{"field":"Created","direction":"desc"}]}`)}},
}

type searchFilter struct {
	Field string  `json:"field"`
	Type  string  `json:"type"`
	Value *string `json:"value"`
}

type searchOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type searchArguments struct {
	TableID    int64          `json:"table_id"`
	Search     string         `json:"search"`
	SearchMode string         `json:"search_mode"`
	Filters    []searchFilter `json:"filters"`
	FilterType string         `json:"filter_type"`
	OrderBy    []searchOrder  `json:"order_by"`
	ViewID     int64          `json:"view_id"`
	Page       int            `json:"page"`
	Size       int            `json:"size"`
}

// validateShape checks everything that needs no schema, before any secret is resolved.
func (a *searchArguments) validateShape() error {
	if a.Page == 0 {
		a.Page = 1
	}
	if a.Size == 0 {
		a.Size = defaultSize
	}
	if a.Page < 1 || a.Size < 1 || a.Size > maxSize {
		return invalidRequest("page must be 1 or more and size between 1 and " + strconv.Itoa(maxSize))
	}
	if a.ViewID < 0 || len(strconv.FormatInt(a.ViewID, 10)) > maxIDDigits {
		return invalidRequest("view_id must be a positive integer")
	}
	if utf8.RuneCountInString(a.Search) > maxSearchLen {
		return invalidRequest("search is too long")
	}
	if a.SearchMode != "" {
		ok := false
		for _, mode := range searchModes {
			ok = ok || mode == a.SearchMode
		}
		if !ok || a.Search == "" {
			return invalidRequest("search_mode must be one of the listed modes and needs a search text")
		}
	}
	if a.FilterType != "" && a.FilterType != "AND" && a.FilterType != "OR" {
		return invalidRequest("filter_type must be AND or OR")
	}
	if len(a.Filters) > maxFilters || len(a.OrderBy) > maxOrderFields {
		return invalidRequest("too many filters or order_by entries")
	}
	seen := map[string]bool{}
	for _, f := range a.Filters {
		takesValue, known := filterTypes[f.Type]
		if !known {
			return invalidRequest("a filter type is not in the allowed list")
		}
		if takesValue != (f.Value != nil) {
			return invalidRequest("a filter needs a value exactly when its type takes one")
		}
		if f.Value != nil && utf8.RuneCountInString(*f.Value) > maxFilterValue {
			return invalidRequest("a filter value is too long")
		}
		if !usableFieldName(f.Field) {
			return invalidRequest("a filter field name cannot be used")
		}
		key := f.Field + "\x00" + f.Type
		if seen[key] {
			return invalidRequest("a filter repeats the same field and type")
		}
		seen[key] = true
	}
	ordered := map[string]bool{}
	for _, o := range a.OrderBy {
		if !usableFieldName(o.Field) || (o.Direction != "" && o.Direction != "asc" && o.Direction != "desc") || ordered[o.Field] {
			return invalidRequest("an order_by entry cannot be used")
		}
		ordered[o.Field] = true
	}
	return nil
}

// usableFieldName refuses names that would break the parameter syntax: "__" separates filter parts, "," and a
// leading sign belong to order_by.
func usableFieldName(name string) bool {
	return name != "" && len(name) <= maxStringLength && utf8.ValidString(name) && !strings.Contains(name, "__") &&
		!strings.ContainsAny(name, ",\x00") && name[0] != '-' && name[0] != '+'
}

func invokeRowsSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "search rows"
	var input searchArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return nil, err
	}
	if err := input.validateShape(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// The schema read is the one request before a schema-based refusal; it also serves the link masking.
	fields, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	byName := map[string]fieldJSON{}
	for _, field := range fields {
		byName[field.Name] = field
	}
	for _, f := range input.Filters {
		if field, ok := byName[f.Field]; !ok || !searchableFieldTypes[field.Type] {
			return nil, invalidRequest("a filter names a field that is unknown or cannot be filtered")
		}
	}
	for _, o := range input.OrderBy {
		if field, ok := byName[o.Field]; !ok || !searchableFieldTypes[field.Type] {
			return nil, invalidRequest("order_by names a field that is unknown or cannot be sorted")
		}
	}
	if input.Search != "" && !client.scope.wildcard {
		for _, field := range fields {
			if indirectFieldTypes[field.Type] {
				return nil, invalidRequest("search is not available on a table with link, lookup, or formula fields for a connection limited to tables")
			}
		}
	}
	if input.ViewID > 0 {
		if err := client.checkView(ctx, op, input.TableID, input.ViewID); err != nil {
			return nil, err
		}
	}
	query := url.Values{"page": {strconv.Itoa(input.Page)}, "size": {strconv.Itoa(input.Size)},
		"user_field_names": {"true"}}
	if input.Search != "" {
		query.Set("search", input.Search)
		if input.SearchMode != "" {
			query.Set("search_mode", input.SearchMode)
		}
	}
	for _, f := range input.Filters {
		value := ""
		if f.Value != nil {
			value = *f.Value
		}
		query.Set("filter__"+f.Field+"__"+f.Type, value)
	}
	if len(input.Filters) > 1 && input.FilterType != "" {
		query.Set("filter_type", input.FilterType)
	}
	if len(input.OrderBy) > 0 {
		parts := make([]string, 0, len(input.OrderBy))
		for _, o := range input.OrderBy {
			if o.Direction == "desc" {
				parts = append(parts, "-"+o.Field)
			} else {
				parts = append(parts, o.Field)
			}
		}
		query.Set("order_by", strings.Join(parts, ","))
	}
	if input.ViewID > 0 {
		query.Set("view_id", strconv.FormatInt(input.ViewID, 10))
	}
	var page struct {
		Count   int64             `json:"count"`
		Next    *string           `json:"next"`
		Results []json.RawMessage `json:"results"`
	}
	if err := client.get(ctx, op, "/api/database/rows/table/"+tablePath(input.TableID), query, &page); err != nil {
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
	client.maskWith(fields, true, result.Rows)
	for i := range result.Rows {
		result.Rows[i].Fields = boundFields(result.Rows[i].Fields)
	}
	return result, nil
}

// checkView confirms through the table's own view list that the view belongs to the table. A view of another
// table is refused without naming it.
func (c *Client) checkView(ctx context.Context, op string, tableID, viewID int64) error {
	var views []struct {
		ID int64 `json:"id"`
	}
	if err := c.get(ctx, op, "/api/database/views/table/"+tablePath(tableID), nil, &views); err != nil {
		return err
	}
	for i, view := range views {
		if i >= maxViews {
			break
		}
		if view.ID == viewID {
			return nil
		}
	}
	return invalidRequest("view_id is not a view of this table")
}
