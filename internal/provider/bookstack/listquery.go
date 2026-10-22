package bookstack

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// listKind is the value type of a list field. It decides which operators a filter accepts, how its value is
// checked before any request, and how a row is compared on the client.
type listKind int

const (
	kindID     listKind = iota // identifier, digits only
	kindNumber                 // non-negative integer
	kindBool
	kindTime
	kindText
)

type listField struct {
	Name string
	Kind listKind
}

// listSchema names the fields a list may be sorted and filtered by. The two lists are separate so a list whose
// sortable and filterable fields differ needs no other form.
type listSchema struct {
	Sort   []listField
	Filter []listField
}

// sameFields is the schema of a list that sorts and filters by one set of fields.
func sameFields(fields []listField) listSchema { return listSchema{Sort: fields, Filter: fields} }

const (
	maxListFilters = 10
	maxFilterValue = 255
)

var (
	// listOps are the operators of a filter in the order the schema shows them; eq is the default.
	listOps = []string{"eq", "ne", "gt", "lt", "gte", "lte", "like"}

	listTimeLayout = "2006-01-02 15:04:05"
)

func names(fields []listField) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Name
	}
	return out
}

func quoted(values []string) string {
	b, _ := json.Marshal(values)
	return string(b)
}

// properties returns the schema members of the sort and filters arguments, to be spliced into an input schema.
func (s listSchema) properties() string {
	return `"sort":{"type":"string","pattern":"^[+-]?(` + strings.Join(names(s.Sort), "|") + `)$"},` +
		`"filters":{"type":"array","maxItems":` + strconv.Itoa(maxListFilters) +
		`,"items":{"type":"object","properties":{"field":{"type":"string","enum":` + quoted(names(s.Filter)) +
		`},"op":{"type":"string","enum":` + quoted(listOps) +
		`},"value":{"type":"string","maxLength":` + strconv.Itoa(maxFilterValue) +
		`}},"required":["field","value"],"additionalProperties":false}}`
}

// arguments returns the descriptions of the sort and filters arguments.
func (s listSchema) arguments() []capability.Argument {
	return []capability.Argument{
		{Name: "sort", Description: "Field to sort by, prefixed with + (ascending, default) or -; default +id. One of: " + strings.Join(names(s.Sort), ", ")},
		{Name: "filters", Description: "At most 10 conditions that all must hold, each {field, op, value}; op is eq (default), ne, gt, lt, gte, lte, or like (text fields: contains); " +
			"identifiers and numbers are digits, booleans true or false (eq, ne only), times RFC 3339 or YYYY-MM-DD. Fields: " + strings.Join(names(s.Filter), ", ")},
	}
}

// filterArg is one filter as the caller states it.
type filterArg struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// listFilter is a checked filter: the request form and the typed value the client compares rows with.
type listFilter struct {
	field string
	kind  listKind
	op    string
	sent  string

	num  int64
	flag bool
	at   time.Time
	text string
}

// listQuery is the checked sort and filter of one list call. The zero value sorts by +id without filters.
type listQuery struct {
	sort    string // "+field" or "-field"; empty keeps the default
	filters []listFilter
}

func findField(fields []listField, name string) (listField, bool) {
	for _, f := range fields {
		if f.Name == name {
			return f, true
		}
	}
	return listField{}, false
}

// parseListQuery checks sort and filters by form, before any secret access or request. The messages never
// quote a value.
func parseListQuery(schema listSchema, sort string, args []filterArg) (listQuery, error) {
	var q listQuery
	if sort != "" {
		name, dir := strings.TrimLeft(sort, "+-"), "+"
		if strings.HasPrefix(sort, "-") {
			dir = "-"
		}
		if len(sort)-len(name) > 1 {
			return q, invalidRequest("sort is not a valid sort expression")
		}
		if _, ok := findField(schema.Sort, name); !ok {
			return q, invalidRequest("sort names a field this list cannot be sorted by")
		}
		q.sort = dir + name
	}
	if len(args) > maxListFilters {
		return q, invalidRequest("filters holds at most " + strconv.Itoa(maxListFilters) + " conditions")
	}
	for i, a := range args {
		f, err := parseFilter(schema, a)
		if err != nil {
			return q, invalidRequest("filters[" + strconv.Itoa(i) + "]: " + err.Error())
		}
		q.filters = append(q.filters, f)
	}
	return q, nil
}

type filterError string

func (e filterError) Error() string { return string(e) }

func parseFilter(schema listSchema, a filterArg) (listFilter, error) {
	field, ok := findField(schema.Filter, a.Field)
	if !ok {
		return listFilter{}, filterError("field is not a filterable field of this list")
	}
	op := a.Op
	if op == "" {
		op = "eq"
	}
	known := false
	for _, o := range listOps {
		known = known || o == op
	}
	if !known {
		return listFilter{}, filterError("op is not a known operator")
	}
	if !utf8.ValidString(a.Value) || utf8.RuneCountInString(a.Value) > maxFilterValue {
		return listFilter{}, filterError("value must be valid text of at most 255 characters")
	}
	for _, r := range a.Value {
		if unicode.IsControl(r) {
			return listFilter{}, filterError("value must not contain control characters")
		}
	}
	f := listFilter{field: field.Name, kind: field.Kind, op: op}
	switch field.Kind {
	case kindID, kindNumber:
		if op == "like" {
			return f, filterError("op like applies to text fields only")
		}
		n, ok := digits(a.Value)
		if !ok {
			return f, filterError("value must be a sequence of digits")
		}
		f.num, f.sent = n, strconv.FormatInt(n, 10)
	case kindBool:
		if op != "eq" && op != "ne" {
			return f, filterError("a boolean field accepts eq and ne only")
		}
		switch a.Value {
		case "true":
			f.flag, f.sent = true, "1"
		case "false":
			f.sent = "0"
		default:
			return f, filterError("value must be true or false")
		}
	case kindTime:
		if op == "like" {
			return f, filterError("op like applies to text fields only")
		}
		t, err := time.Parse(time.RFC3339, a.Value)
		if err != nil {
			if t, err = time.Parse("2006-01-02", a.Value); err != nil {
				return f, filterError("value must be an RFC 3339 time or a date YYYY-MM-DD")
			}
		}
		f.at = t.UTC().Truncate(time.Second)
		f.sent = f.at.Format(listTimeLayout)
	default:
		f.text = strings.ToLower(a.Value)
		f.sent = a.Value
		if op == "like" {
			f.sent = likeContains(a.Value)
		}
	}
	return f, nil
}

// digits reads a sequence of ASCII digits that fits an int64.
func digits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func (q listQuery) active() bool { return len(q.filters) > 0 }

// apply adds the filters to a request query. A key that is already present stays: it belongs to the
// connection's target binding or to an earlier filter, and match checks every filter on the client anyway.
func (q listQuery) apply(query url.Values) {
	for _, f := range q.filters {
		key := "filter[" + f.field + "]"
		if f.op != "eq" {
			key = "filter[" + f.field + ":" + f.op + "]"
		}
		if _, taken := query[key]; !taken {
			query.Set(key, f.sent)
		}
	}
}

// match reports whether a row passes every filter. value reads a field from the raw row: an int64 for
// identifiers and numbers, a bool, or a string for text and times. BookStack silently ignores a filter it does
// not know, so this check is what makes a filter binding. A field the row does not give, or gives in another
// form, fails the row, so doubt drops a row instead of passing a foreign one.
func (q listQuery) match(value func(field string) (any, bool)) bool {
	for _, f := range q.filters {
		v, ok := value(f.field)
		if !ok || !f.holds(v) {
			return false
		}
	}
	return true
}

func (f listFilter) holds(v any) bool {
	switch f.kind {
	case kindID, kindNumber:
		n, ok := v.(int64)
		return ok && f.order(compareInt(n, f.num))
	case kindBool:
		b, ok := v.(bool)
		return ok && (b == f.flag) == (f.op == "eq")
	case kindTime:
		s, ok := v.(string)
		if !ok {
			return false
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return false
		}
		return f.order(t.UTC().Truncate(time.Second).Compare(f.at))
	default:
		s, ok := v.(string)
		if !ok {
			return false
		}
		s = strings.ToLower(s)
		if f.op == "like" {
			return strings.Contains(s, f.text)
		}
		return f.order(strings.Compare(s, f.text))
	}
}

func compareInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// order applies the operator to the comparison of the row value with the filter value.
func (f listFilter) order(c int) bool {
	switch f.op {
	case "eq":
		return c == 0
	case "ne":
		return c != 0
	case "gt":
		return c > 0
	case "lt":
		return c < 0
	case "gte":
		return c >= 0
	case "lte":
		return c <= 0
	}
	return false
}

// withListQuery sets the sort of the scan and, when filters are given, switches it to the filtered mode: the
// filters go into the request and every row must pass them in addition to the existing keep check. value reads
// a field of a raw row.
func (s *scanSpec[T]) withListQuery(q listQuery, value func(item T, field string) (any, bool)) {
	s.sort = q.sort
	if !q.active() {
		return
	}
	s.filtered = true
	query := url.Values{}
	for k, v := range s.query {
		query[k] = v
	}
	q.apply(query)
	s.query = query
	keep := s.keep
	s.keep = func(item T) bool {
		return keep(item) && q.match(func(field string) (any, bool) { return value(item, field) })
	}
}

func userID(u *userJSON) (any, bool) {
	if u == nil {
		return nil, false
	}
	return u.ID, true
}

// listFieldKinds of the content lists. Users are identifiers.
var (
	pageListFields = []listField{
		{"id", kindID}, {"book_id", kindID}, {"chapter_id", kindID}, {"name", kindText}, {"slug", kindText},
		{"priority", kindNumber}, {"draft", kindBool}, {"template", kindBool}, {"created_at", kindTime},
		{"updated_at", kindTime}, {"created_by", kindID}, {"updated_by", kindID}, {"owned_by", kindID},
	}
	// bookListFields also serve shelves, which BookStack lists with the same fields.
	bookListFields = []listField{
		{"id", kindID}, {"name", kindText}, {"slug", kindText}, {"description", kindText}, {"created_at", kindTime},
		{"updated_at", kindTime}, {"created_by", kindID}, {"updated_by", kindID}, {"owned_by", kindID},
	}
	chapterListFields = []listField{
		{"id", kindID}, {"book_id", kindID}, {"name", kindText}, {"slug", kindText}, {"description", kindText},
		{"priority", kindNumber}, {"created_at", kindTime}, {"updated_at", kindTime}, {"created_by", kindID},
		{"updated_by", kindID}, {"owned_by", kindID},
	}

	pageListSchema    = sameFields(pageListFields)
	bookListSchema    = sameFields(bookListFields)
	chapterListSchema = sameFields(chapterListFields)
)

// pageValue reads a filter field from a raw page row.
func pageValue(p pageJSON, field string) (any, bool) {
	switch field {
	case "id":
		return p.ID, true
	case "book_id":
		return p.BookID, true
	case "chapter_id":
		return p.ChapterID, true
	case "name":
		return p.Name, true
	case "slug":
		return p.Slug, true
	case "priority":
		return p.Priority, true
	case "draft":
		return p.Draft, true
	case "template":
		return p.Template, true
	case "created_at":
		return p.CreatedAt, true
	case "updated_at":
		return p.UpdatedAt, true
	case "created_by":
		return userID(p.CreatedBy)
	case "updated_by":
		return userID(p.UpdatedBy)
	case "owned_by":
		return userID(p.OwnedBy)
	}
	return nil, false
}

// listedValue reads a filter field from a raw book, chapter, or shelf row.
func listedValue(l listedJSON, field string) (any, bool) {
	switch field {
	case "id":
		return l.ID, true
	case "book_id":
		return l.BookID, true
	case "name":
		return l.Name, true
	case "slug":
		return l.Slug, true
	case "description":
		return l.Description, true
	case "priority":
		if l.Priority == nil {
			return nil, false
		}
		return *l.Priority, true
	case "created_at":
		return l.CreatedAt, true
	case "updated_at":
		return l.UpdatedAt, true
	case "created_by":
		return userID(l.CreatedBy)
	case "updated_by":
		return userID(l.UpdatedBy)
	case "owned_by":
		return userID(l.OwnedBy)
	}
	return nil, false
}
