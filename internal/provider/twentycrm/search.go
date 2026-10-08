package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Twenty's filter grammar is field[operator]:value, joined by commas, with in-lists as [a,b]. It has no
// escaping: a quote, bracket, colon, comma, or backslash in a value changes the meaning of the expression
// (packages/twenty-server/src/engine/api/rest/input-request-parsers/filter-parser-utils/ of twentyhq/twenty,
// commit 46fc01c38374c2b489b0d4755719da2d1e5408ac: parse-base-filter, parse-filter-content,
// format-field-values). Qatlas therefore builds the expression only from checked parts: a field of the
// schema, an operator of a fixed list per field kind, and a value that is typed (UUID, number, boolean,
// ISO date, enum value of the schema) or plain text in a character set without any grammar character.
const (
	maxConditions    = 5
	maxInValues      = 20
	maxConditionText = 64
	errConditionForm = "conditions must be between 1 and 5 entries of field, operator, and a value that fits the operator"
	errConditionUse  = "a condition names a field, operator, or value that cannot be used on this object"
)

var (
	numberPattern   = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,14})(\.[0-9]{1,8})?$`)
	datePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	dateTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,3})?Z$`)
)

// conditionOperators is the fixed list of filter comparators; which of them a field accepts follows its kind.
var conditionOperators = []string{"eq", "neq", "in", "gt", "gte", "lt", "lte", "ilike", "startsWith", "endsWith", "is"}

const conditionSchema = `{"type":"object","properties":{` +
	`"field":{"type":"string","minLength":1,"maxLength":129,"pattern":` +
	`"^[A-Za-z_][A-Za-z0-9_]{0,63}(\\.[A-Za-z_][A-Za-z0-9_]{0,63})?$"},` +
	`"operator":{"type":"string","enum":["eq","neq","in","gt","gte","lt","lte","ilike","startsWith","endsWith","is"]},` +
	`"value":{"anyOf":[` + scalarSchema + `,{"type":"array","minItems":1,"maxItems":20,"items":{"anyOf":[` +
	scalarSchema + `]}}]}},"required":["field","operator","value"],"additionalProperties":false}`

const scalarSchema = `{"type":"string","minLength":1,"maxLength":64},{"type":"number"},{"type":"boolean"}`

const conditionNote = "Conditions are AND-linked and use fixed operators per field kind: text (eq, neq, in, ilike for " +
	"contains, startsWith, endsWith, is), selection (eq, neq, in, is), number and date (eq, neq, in, gt, gte, lt, lte, " +
	"is), boolean (eq, is), identifier and relation identifier (eq, neq, in, is). is takes NULL or NOT_NULL; in takes " +
	"up to 20 values. Dates are YYYY-MM-DD, date-times YYYY-MM-DDTHH:MM:SSZ. Text values use letters, digits, " +
	"spaces, and . _ + @ & - only"

const conditionsDescription = "objects with field (a field of twentycrm.objects.get, or field.subfield of a " +
	"composite field), operator, and value (a list for in)"

var recordsSearch = capability.Descriptor{
	ID:      Provider + ".records.search",
	Version: 1,
	Title:   "Search Twenty CRM records",
	Description: "List one page of the records of one reachable object of the Twenty workspace of a connection " +
		"that meet up to 5 structured conditions on fields of the object's schema, optionally sorted by one field " +
		"and limited to chosen fields. " + conditionNote + ". " + recordNote,
	Tags:     []string{"twentycrm", "crm", "records", "search", "filter"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,` + pageProperties +
		`,"conditions":{"type":"array","minItems":1,"maxItems":5,"items":` + conditionSchema +
		`}},"required":["object","conditions"],"additionalProperties":false}`),
	OutputSchema: recordsList.OutputSchema,
	// The trash selection belongs to records.list alone; its argument is the last one of the list.
	Arguments: append(append([]capability.Argument(nil), recordsList.Arguments[:len(recordsList.Arguments)-1]...), capability.Argument{
		Name: "conditions", Required: true, Description: "1 to 5 AND-linked " + conditionsDescription}),
	Fields: recordsList.Fields,
	Examples: []capability.Example{{
		Description: "Find people of a city whose first name starts with A",
		Arguments: json.RawMessage(`{"object":"person","conditions":[{"field":"city","operator":"eq","value":"Berlin"},` +
			`{"field":"name.firstName","operator":"startsWith","value":"A"}]}`),
	}},
}

func invokeRecordsSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	query, err := newRecordQuery(resolved, "search records", raw, modeSearch)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRecords(ctx, query)
}

type conditionArgument struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

type valueKind byte

const (
	valueString valueKind = 's'
	valueNumber valueKind = 'n'
	valueBool   valueKind = 'b'
)

// condition is one normalized condition: its parts are syntactically checked and free of grammar
// characters, but not yet checked against the schema.
type condition struct {
	Field    string
	Operator string
	Kind     valueKind
	List     bool
	Values   []string
}

// normalizeConditions checks the conditions without the schema, so a value that could break the grammar never
// reaches a request. The result is sorted and without repeats, which keeps the filter and the cursor binding
// independent of the order the conditions were given in.
func normalizeConditions(args []conditionArgument, min int) ([]condition, error) {
	if len(args) < min || len(args) > maxConditions {
		return nil, invalidRequest(errConditionForm)
	}
	result := make([]condition, 0, len(args))
	seen := map[string]bool{}
	for _, arg := range args {
		cond, ok := normalizeCondition(arg)
		if !ok {
			return nil, invalidRequest(errConditionForm)
		}
		if key := conditionKey(cond); !seen[key] {
			seen[key] = true
			result = append(result, cond)
		}
	}
	sort.Slice(result, func(i, j int) bool { return conditionKey(result[i]) < conditionKey(result[j]) })
	return result, nil
}

func normalizeCondition(arg conditionArgument) (condition, bool) {
	known := false
	for _, operator := range conditionOperators {
		known = known || operator == arg.Operator
	}
	if !known || !orderPattern.MatchString(arg.Field) {
		return condition{}, false
	}
	cond := condition{Field: arg.Field, Operator: arg.Operator}
	decoder := json.NewDecoder(bytes.NewReader(arg.Value))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.More() {
		return condition{}, false
	}
	items, isList := value.([]any)
	if isList != (arg.Operator == "in") || (isList && (len(items) == 0 || len(items) > maxInValues)) {
		return condition{}, false
	}
	if !isList {
		items = []any{value}
	}
	cond.List = isList
	for i, item := range items {
		text, kind, ok := scalarText(item)
		if !ok || (i > 0 && kind != cond.Kind) {
			return condition{}, false
		}
		cond.Kind = kind
		cond.Values = append(cond.Values, text)
	}
	if arg.Operator == "is" && (cond.Kind != valueString || (cond.Values[0] != "NULL" && cond.Values[0] != "NOT_NULL")) {
		return condition{}, false
	}
	return cond, true
}

// scalarText returns the canonical text of one value. A string is accepted only without any character of the
// filter grammar; the one exception is a complete ISO date-time, whose colons the grammar tolerates after the
// first one.
func scalarText(value any) (string, valueKind, bool) {
	switch v := value.(type) {
	case string:
		return v, valueString, plainText(v) || dateTimePattern.MatchString(v)
	case json.Number:
		return v.String(), valueNumber, numberPattern.MatchString(v.String())
	case bool:
		return strconv.FormatBool(v), valueBool, true
	}
	return "", 0, false
}

// plainText is the search character set of the companies tool without the apostrophe, which Twenty's
// splitter treats as a quote that hides the comma between two conditions.
func plainText(value string) bool {
	return value != "" && utf8.RuneCountInString(value) <= maxConditionText && safeSearchTerm(value, maxConditionText) &&
		!strings.ContainsRune(value, '\'')
}

func conditionKey(cond condition) string {
	list := "0"
	if cond.List {
		list = "1"
	}
	return cond.Field + "\x00" + cond.Operator + "\x00" + string(cond.Kind) + list + "\x00" + strings.Join(cond.Values, "\x01")
}

// conditionKeys is the cursor binding part of the normalized conditions.
func conditionKeys(conditions []condition) string {
	keys := make([]string, len(conditions))
	for i, cond := range conditions {
		keys[i] = conditionKey(cond)
	}
	return strings.Join(keys, "\x02")
}

// fieldKind is what a field accepts in a condition or as a group, derived from the schema of the response shape:
// a string with enum values is a selection, a string with format uuid an identifier, date or date-time a date,
// a plain string text, integer and number a number, boolean a boolean. A field that points to another schema,
// is composite (has subfields), is an array, or has an enum Qatlas could not take is none. Subfields of a
// composite field are text or number by their JSON type.
type fieldKind int

const (
	kindNone fieldKind = iota
	kindText
	kindSelection
	kindNumber
	kindBoolean
	kindDate
	kindDateTime
	kindUUID
)

func (f catalogField) kind() fieldKind {
	switch {
	case f.Reference || len(f.Subfields) > 0:
		return kindNone
	case f.Enumerated:
		if f.Type == "string" && len(f.Enum) > 0 {
			return kindSelection
		}
		return kindNone
	case f.Type == "boolean":
		return kindBoolean
	case f.Type == "number" || f.Type == "integer":
		return kindNumber
	case f.Type == "string":
		switch f.Format {
		case "":
			return kindText
		case "uuid":
			return kindUUID
		case "date":
			return kindDate
		case "date-time":
			return kindDateTime
		}
	}
	return kindNone
}

var kindOperators = map[fieldKind][]string{
	kindText:      {"eq", "neq", "in", "ilike", "startsWith", "endsWith", "is"},
	kindSelection: {"eq", "neq", "in", "is"},
	kindNumber:    {"eq", "neq", "in", "gt", "gte", "lt", "lte", "is"},
	kindBoolean:   {"eq", "is"},
	kindDate:      {"eq", "neq", "in", "gt", "gte", "lt", "lte", "is"},
	kindDateTime:  {"eq", "neq", "in", "gt", "gte", "lt", "lte", "is"},
	kindUUID:      {"eq", "neq", "in", "is"},
}

func (k fieldKind) allows(operator string) bool {
	for _, candidate := range kindOperators[k] {
		if candidate == operator {
			return true
		}
	}
	return false
}

// target is a field a condition or a group may name: its kind and, for a selection, its enum values.
type target struct {
	kind fieldKind
	enum []string
}

// target resolves a field path against the response shape. deletedAt, relation fields, composite fields
// without a subfield the schema names, rich text, and subfields that carry identifiers are not targets. An
// identifier field named like a relation (<name>Id) is a target only when the relation field of the schema
// names a target object that the connection reaches, so a condition cannot probe an object it may not see.
func (o *recordObject) target(bound scope, path string) (target, bool) {
	name, sub, dotted := strings.Cut(path, ".")
	field, ok := o.readable[name]
	if !ok || name == "deletedAt" {
		return target{}, false
	}
	if dotted {
		if isRichText(field) {
			return target{}, false
		}
		for _, candidate := range field.Subfields {
			if candidate.Name != sub || strings.HasSuffix(sub, "Id") {
				continue
			}
			switch candidate.Type {
			case "string":
				return target{kind: kindText}, true
			case "number", "integer":
				return target{kind: kindNumber}, true
			case "boolean":
				return target{kind: kindBoolean}, true
			}
		}
		return target{}, false
	}
	kind := field.kind()
	if kind == kindNone {
		return target{}, false
	}
	if kind == kindUUID && name != "id" && strings.HasSuffix(name, "Id") && !o.reachableRelationID(bound, name) {
		return target{}, false
	}
	return target{kind: kind, enum: field.Enum}, true
}

// reachableRelationID reports whether name is the identifier of a relation field whose target object is known
// and reachable.
func (o *recordObject) reachableRelationID(bound scope, name string) bool {
	relation := strings.TrimSuffix(name, "Id")
	for _, field := range o.Fields {
		if field.Name == relation && field.Reference {
			return field.Relation != "" && bound.allows(field.Relation)
		}
	}
	return false
}

// filter checks the conditions against the schema and builds the filter parameter from them.
func (o *recordObject) filter(bound scope, conditions []condition) (string, error) {
	parts := make([]string, 0, len(conditions))
	for _, cond := range conditions {
		found, ok := o.target(bound, cond.Field)
		if !ok || !found.kind.allows(cond.Operator) {
			return "", invalidRequest(errConditionUse)
		}
		values := make([]string, len(cond.Values))
		for i, text := range cond.Values {
			value, ok := filterValue(found, cond, text)
			if !ok {
				return "", invalidRequest(errConditionUse)
			}
			values[i] = value
		}
		value := values[0]
		if cond.List {
			value = "[" + strings.Join(values, ",") + "]"
		}
		parts = append(parts, cond.Field+"["+cond.Operator+"]:"+value)
	}
	return strings.Join(parts, ","), nil
}

// filterValue checks one value against the kind of its field and returns it as it goes into the filter.
func filterValue(found target, cond condition, text string) (string, bool) {
	if cond.Operator == "is" {
		return text, true
	}
	switch found.kind {
	case kindText:
		if cond.Kind != valueString || !plainText(text) {
			return "", false
		}
		if cond.Operator == "ilike" {
			return "%" + text + "%", true
		}
		return text, true
	case kindSelection:
		for _, candidate := range found.enum {
			if cond.Kind == valueString && candidate == text {
				return text, true
			}
		}
	case kindNumber:
		return text, cond.Kind == valueNumber
	case kindBoolean:
		return text, cond.Kind == valueBool
	case kindUUID:
		return text, cond.Kind == valueString && validUUID(text)
	case kindDate:
		_, err := time.Parse("2006-01-02", text)
		return text, cond.Kind == valueString && datePattern.MatchString(text) && err == nil
	case kindDateTime:
		_, err := time.Parse(time.RFC3339Nano, text)
		return text, cond.Kind == valueString && dateTimePattern.MatchString(text) && err == nil
	}
	return "", false
}
