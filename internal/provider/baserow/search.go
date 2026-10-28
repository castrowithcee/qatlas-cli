package baserow

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
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

// filterTypeList is the fixed list of the filter types Baserow documents for listing rows; nothing outside it
// reaches the request.
var filterTypeList = []string{
	"equal", "not_equal", "filename_contains", "files_lower_than", "has_file_type", "contains", "contains_not",
	"contains_word", "doesnt_contain_word", "starts_with", "length_is_lower_than", "higher_than",
	"higher_than_or_equal", "lower_than", "lower_than_or_equal", "is_even_and_whole", "date_equal",
	"date_before", "date_before_or_equal", "date_after_days_ago", "date_after", "date_after_or_equal",
	"date_not_equal", "date_equals_today", "date_before_today", "date_after_today", "date_within_days",
	"date_within_weeks", "date_within_months", "date_equals_days_ago", "date_equals_months_ago",
	"date_equals_years_ago", "date_equals_week", "date_equals_month", "date_equals_day_of_month",
	"date_equals_year", "date_is", "date_is_not", "date_is_before", "date_is_on_or_before", "date_is_after",
	"date_is_on_or_after", "date_is_within", "single_select_equal", "single_select_not_equal",
	"single_select_is_any_of", "single_select_is_none_of", "link_row_has", "link_row_has_not",
	"link_row_contains", "link_row_not_contains", "boolean", "empty", "not_empty", "multiple_select_has",
	"multiple_select_has_not", "multiple_collaborators_has", "multiple_collaborators_has_not", "user_is",
	"user_is_not", "has_value_equal", "has_not_value_equal", "has_value_contains", "has_not_value_contains",
	"has_value_contains_word", "has_not_value_contains_word", "has_value_length_is_lower_than",
	"has_all_values_equal", "has_empty_value", "has_not_empty_value", "has_any_select_option_equal",
	"has_none_select_option_equal", "has_value_lower", "has_value_lower_or_equal", "has_value_higher",
	"has_value_higher_or_equal", "has_not_value_higher_or_equal", "has_not_value_higher",
	"has_not_value_lower_or_equal", "has_not_value_lower", "has_date_equal", "has_not_date_equal",
	"has_date_before", "has_not_date_before", "has_date_on_or_before", "has_not_date_on_or_before",
	"has_date_on_or_after", "has_not_date_on_or_after", "has_date_after", "has_not_date_after",
	"has_date_within", "has_not_date_within",
}

// valuedFilterTypes are the long-standing types for which the call itself still checks that a value is given
// exactly when the type takes one; for every other type Baserow decides.
var valuedFilterTypes = map[string]bool{
	"equal": true, "not_equal": true, "contains": true, "contains_not": true,
	"higher_than": true, "higher_than_or_equal": true, "lower_than": true, "lower_than_or_equal": true,
	"empty": false, "not_empty": false,
}

// maskedLinkFilterTypes are the only filters on a link field whose other table is outside the targets: they act
// on row ids or emptiness and show nothing the output does not show.
var maskedLinkFilterTypes = map[string]bool{"empty": true, "not_empty": true, "link_row_has": true, "link_row_has_not": true}

func filterTypeEnum() string {
	out, _ := json.Marshal(filterTypeList)
	return string(out)
}

var searchModes = []string{"full-text", "full-text-with-count", "compat"}

var rowsSearch = capability.Descriptor{
	ID: Provider + ".rows.search", Version: 1, Title: "Search Baserow rows",
	Description: "Search, filter, and sort the rows of one table, page by page. Filters use a fixed list of types " +
		"on fields the table's schema confirms, named by field or field_id; link fields into tables outside the " +
		"connection's targets or excluded from them allow only empty, not_empty, link_row_has, and link_row_has_not and cannot be sorted. " +
		"A view applies only when it belongs to the table. " + linkNote,
	Tags: []string{"baserow", "rows", "search", "filter", "sort", "records"}, Risk: rowsRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `,` +
		`"search":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSearchLen) + `},` +
		`"search_mode":{"type":"string","enum":["full-text","full-text-with-count","compat"]},` +
		`"filters":{"type":"array","maxItems":` + strconv.Itoa(maxFilters) + `,"items":{"type":"object","properties":{` +
		`"field":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxStringLength) + `},` +
		`"field_id":` + idSchema + `,` +
		`"type":{"type":"string","enum":` + filterTypeEnum() + `},` +
		`"value":{"type":"string","maxLength":` + strconv.Itoa(maxFilterValue) + `}},` +
		`"required":["type"],"additionalProperties":false}},` +
		`"filter_type":{"type":"string","enum":["AND","OR"]},` +
		`"order_by":{"type":"array","maxItems":` + strconv.Itoa(maxOrderFields) + `,"items":{"type":"object","properties":{` +
		`"field":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxStringLength) + `},` +
		`"field_id":` + idSchema + `,` +
		`"direction":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}},` +
		`"include":` + columnKeysSchema + `,"exclude":` + columnKeysSchema + `,` +
		`"view_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"size":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxSize) + `}},` +
		`"required":["table_id"],"additionalProperties":false}`),
	OutputSchema: rowsList.OutputSchema,
	Arguments: []capability.Argument{tableIDArgument,
		{Name: "search", Description: "Text to search for in the table, up to " + strconv.Itoa(maxSearchLen) + " characters; refused on a table with a link field into a table outside the connection's targets or excluded from them unless the connection targets * without exclusions"},
		{Name: "search_mode", Description: "full-text (default of Baserow), full-text-with-count, or compat"},
		{Name: "filters", Description: "Up to " + strconv.Itoa(maxFilters) + " objects with field (name) or field_id (exactly one), type (one of the filter types Baserow documents for listing rows, see the input schema), and value (up to " + strconv.Itoa(maxFilterValue) + " characters; whether a type needs one is up to Baserow, except that equal, not_equal, contains, contains_not, higher_than, higher_than_or_equal, lower_than, and lower_than_or_equal need one and empty and not_empty take none)"},
		{Name: "filter_type", Description: "AND (default) or OR across the filters"},
		{Name: "order_by", Description: "Up to " + strconv.Itoa(maxOrderFields) + " objects with field (name) or field_id (exactly one) and direction asc (default) or desc"},
		{Name: "include", Description: includeDescription},
		{Name: "exclude", Description: excludeDescription},
		{Name: "view_id", Description: "A view of this table whose filters and sorting apply"},
		{Name: "page", Description: "Page number from 1; 1 when omitted"},
		{Name: "size", Description: "Rows per page, 1 to " + strconv.Itoa(maxSize) + "; " + strconv.Itoa(defaultSize) + " when omitted"},
	},
	Fields:   rowsList.Fields,
	Examples: []capability.Example{{Description: "Open rows of a customer table, newest first", Arguments: json.RawMessage(`{"table_id":1,"filters":[{"field":"Status","type":"equal","value":"open"}],"order_by":[{"field":"Created","direction":"desc"}]}`)}},
}

type searchFilter struct {
	Field   string  `json:"field"`
	FieldID *int64  `json:"field_id"`
	Type    string  `json:"type"`
	Value   *string `json:"value"`
}

type searchOrder struct {
	Field     string `json:"field"`
	FieldID   *int64 `json:"field_id"`
	Direction string `json:"direction"`
}

// fieldRef is how a filter or ordering names its field: a name, or an id the schema translates.
func fieldRef(name string, id *int64) (string, error) {
	if (name == "") == (id == nil) {
		return "", invalidRequest("a filter or order_by entry needs exactly one of field and field_id")
	}
	if id != nil {
		if *id < 1 || len(strconv.FormatInt(*id, 10)) > maxIDDigits {
			return "", invalidRequest("field_id must be a positive integer")
		}
		return "id\x00" + strconv.FormatInt(*id, 10), nil
	}
	if !usableFieldName(name) {
		return "", invalidRequest("a field name cannot be used")
	}
	return "name\x00" + name, nil
}

type searchArguments struct {
	TableID    int64          `json:"table_id"`
	Search     string         `json:"search"`
	SearchMode string         `json:"search_mode"`
	Filters    []searchFilter `json:"filters"`
	FilterType string         `json:"filter_type"`
	OrderBy    []searchOrder  `json:"order_by"`
	Include    []string       `json:"include"`
	Exclude    []string       `json:"exclude"`
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
	if err := validateColumnKeys("include", a.Include); err != nil {
		return err
	}
	if err := validateColumnKeys("exclude", a.Exclude); err != nil {
		return err
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
		if !slices.Contains(filterTypeList, f.Type) {
			return invalidRequest("a filter type is not in the allowed list")
		}
		if takesValue, checked := valuedFilterTypes[f.Type]; checked && takesValue != (f.Value != nil) {
			return invalidRequest("a filter needs a value exactly when its type takes one")
		}
		if f.Value != nil && utf8.RuneCountInString(*f.Value) > maxFilterValue {
			return invalidRequest("a filter value is too long")
		}
		ref, err := fieldRef(f.Field, f.FieldID)
		if err != nil {
			return err
		}
		key := ref + "\x00" + f.Type
		if seen[key] {
			return invalidRequest("a filter repeats the same field and type")
		}
		seen[key] = true
	}
	ordered := map[string]bool{}
	for _, o := range a.OrderBy {
		ref, err := fieldRef(o.Field, o.FieldID)
		if err != nil {
			return err
		}
		if (o.Direction != "" && o.Direction != "asc" && o.Direction != "desc") || ordered[ref] {
			return invalidRequest("an order_by entry cannot be used")
		}
		ordered[ref] = true
	}
	return nil
}

// resolve names the field of a filter or an ordering by the table's schema.
func resolveField(name string, id *int64, byName map[string]fieldJSON, byID map[int64]fieldJSON) (fieldJSON, bool) {
	if id != nil {
		field, ok := byID[*id]
		return field, ok
	}
	field, ok := byName[name]
	return field, ok
}

// linkOutside reports a link field whose other table is outside the targets or cannot be determined; its
// display values are masked in the output, so a filter, sort, or search must not act on them. The test is the
// one of maskWith.
func (c *Client) linkOutside(field fieldJSON) bool {
	if c.scope.unrestricted() || field.Type != "link_row" {
		return false
	}
	return field.LinkRowTableID == nil || !c.reaches(*field.LinkRowTableID)
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
	client, err := openTable(ctx, resolved, secrets, red, op, input.TableID)
	if err != nil {
		return nil, err
	}
	// The schema read is the one request before a schema-based refusal; it also serves the link masking.
	fields, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	byName := map[string]fieldJSON{}
	byID := map[int64]fieldJSON{}
	for _, field := range fields {
		byName[field.Name] = field
		byID[field.ID] = field
	}
	filterNames := make([]string, len(input.Filters))
	seen := map[string]bool{}
	for i, f := range input.Filters {
		field, ok := resolveField(f.Field, f.FieldID, byName, byID)
		if !ok || !usableFieldName(field.Name) {
			return nil, invalidRequest("a filter names a field that is unknown or cannot be used")
		}
		if client.lookupOutside(field, byID) {
			return nil, invalidRequest("a lookup field through a link into a table outside the targets cannot be filtered")
		}
		if client.linkOutside(field) && !maskedLinkFilterTypes[f.Type] {
			return nil, invalidRequest("a filter type on a link field into a table outside the targets is limited to empty, not_empty, link_row_has, and link_row_has_not")
		}
		filterNames[i] = field.Name
		key := field.Name + "\x00" + f.Type
		if seen[key] {
			return nil, invalidRequest("a filter repeats the same field and type")
		}
		seen[key] = true
	}
	orderNames := make([]string, len(input.OrderBy))
	ordered := map[string]bool{}
	for i, o := range input.OrderBy {
		field, ok := resolveField(o.Field, o.FieldID, byName, byID)
		if !ok || !usableFieldName(field.Name) {
			return nil, invalidRequest("order_by names a field that is unknown or cannot be used")
		}
		if client.lookupOutside(field, byID) {
			return nil, invalidRequest("a lookup field through a link into a table outside the targets cannot be sorted")
		}
		if client.linkOutside(field) {
			return nil, invalidRequest("a link field into a table outside the targets cannot be sorted")
		}
		if ordered[field.Name] {
			return nil, invalidRequest("order_by repeats a field")
		}
		ordered[field.Name] = true
		orderNames[i] = field.Name
	}
	for _, key := range append(append([]string{}, input.Include...), input.Exclude...) {
		if _, ok := byName[key]; !ok {
			return nil, invalidRequest("include or exclude names a field the table does not have")
		}
	}
	if input.Search != "" {
		for _, field := range fields {
			if client.linkOutside(field) || client.lookupOutside(field, byID) {
				return nil, invalidRequest("search is not available on a table with a link or lookup field into a table outside the targets")
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
	for i, f := range input.Filters {
		value := ""
		if f.Value != nil {
			value = *f.Value
		}
		query.Set("filter__"+filterNames[i]+"__"+f.Type, value)
	}
	if len(input.Filters) > 1 && input.FilterType != "" {
		query.Set("filter_type", input.FilterType)
	}
	if len(input.OrderBy) > 0 {
		parts := make([]string, 0, len(input.OrderBy))
		for i, o := range input.OrderBy {
			if o.Direction == "desc" {
				parts = append(parts, "-"+orderNames[i])
			} else {
				parts = append(parts, orderNames[i])
			}
		}
		query.Set("order_by", strings.Join(parts, ","))
	}
	setColumnKeys(query, input.Include, input.Exclude)
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
