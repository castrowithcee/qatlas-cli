package twentycrm

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

var filterSchemaOptions = []MetaOption{{Value: "A"}, {Value: "B"}, {Value: "C"}}

func TestCheckFilterValueWritesTheStringTwentyStores(t *testing.T) {
	for name, tt := range map[string]struct {
		fieldType, operand string
		in                 FilterValue
		want               string
	}{
		"text":                 {"TEXT", "CONTAINS", FilterValue{Text: ptr("Berlin")}, "Berlin"},
		"text negated":         {"TEXT", "DOES_NOT_CONTAIN", FilterValue{Text: ptr("a b")}, "a b"},
		"select":               {"SELECT", "IS", FilterValue{Options: []string{"A", "B"}}, `["A","B"]`},
		"select not":           {"SELECT", "IS_NOT", FilterValue{Options: []string{"C"}}, `["C"]`},
		"multi select":         {"MULTI_SELECT", "CONTAINS", FilterValue{Options: []string{"A"}}, `["A"]`},
		"multi select negated": {"MULTI_SELECT", "DOES_NOT_CONTAIN", FilterValue{Options: []string{"B"}}, `["B"]`},
		"number":               {"NUMBER", "IS", FilterValue{Number: ptr(12.5)}, "12.5"},
		"number whole":         {"NUMBER", "GREATER_THAN_OR_EQUAL", FilterValue{Number: ptr(3.0)}, "3"},
		"number large":         {"NUMBER", "LESS_THAN_OR_EQUAL", FilterValue{Number: ptr(1e12)}, "1000000000000"},
		"number negative":      {"NUMBER", "IS_NOT", FilterValue{Number: ptr(-0.25)}, "-0.25"},
		"date":                 {"DATE", "IS", FilterValue{Date: ptr("2026-02-28")}, "2026-02-28"},
		"date before":          {"DATE", "IS_BEFORE", FilterValue{Date: ptr("2024-02-29")}, "2024-02-29"},
		"date after":           {"DATE", "IS_AFTER", FilterValue{Date: ptr("2026-01-01")}, "2026-01-01"},
		"relative past":        {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "PAST", Amount: ptr(7), Unit: "DAY"}}, "PAST_7_DAY"},
		"relative next":        {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "NEXT", Amount: ptr(2), Unit: "QUARTER"}}, "NEXT_2_QUARTER"},
		"relative this":        {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "THIS", Unit: "WEEK"}}, "THIS_1_WEEK"},
		"date in past":         {"DATE", "IS_IN_PAST", FilterValue{}, ""},
		"date in future":       {"DATE", "IS_IN_FUTURE", FilterValue{}, ""},
		"date today":           {"DATE", "IS_TODAY", FilterValue{}, ""},
		"date time day":        {"DATE_TIME", "IS", FilterValue{Date: ptr("2026-03-01")}, "2026-03-01"},
		"date time instant":    {"DATE_TIME", "IS", FilterValue{Date: ptr("2026-03-01T10:00:00Z")}, "2026-03-01T10:00:00Z"},
		"date time before":     {"DATE_TIME", "IS_BEFORE", FilterValue{Date: ptr("2026-03-01T10:00:00.5Z")}, "2026-03-01T10:00:00.5Z"},
		"date time after":      {"DATE_TIME", "IS_AFTER", FilterValue{Date: ptr("2026-03-01T10:00:00Z")}, "2026-03-01T10:00:00Z"},
		"boolean true":         {"BOOLEAN", "IS", FilterValue{Flag: ptr(true)}, "true"},
		"boolean false":        {"BOOLEAN", "IS", FilterValue{Flag: ptr(false)}, "false"},
		"empty text":           {"TEXT", "IS_EMPTY", FilterValue{}, ""},
		"not empty select":     {"SELECT", "IS_NOT_EMPTY", FilterValue{}, ""},
		"empty number":         {"NUMBER", "IS_EMPTY", FilterValue{}, ""},
		"empty date":           {"DATE", "IS_EMPTY", FilterValue{}, ""},
		"empty date time":      {"DATE_TIME", "IS_NOT_EMPTY", FilterValue{}, ""},
		"empty multi select":   {"MULTI_SELECT", "IS_EMPTY", FilterValue{}, ""},
		"empty composite":      {"FULL_NAME", "IS_EMPTY", FilterValue{}, ""},
		"empty uuid":           {"UUID", "IS_NOT_EMPTY", FilterValue{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := checkFilterValue(tt.fieldType, filterSchemaOptions, tt.operand, tt.in)
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestCheckFilterValueRefusesWhatTwentyOrQatlasDoNotAccept(t *testing.T) {
	long := strings.Repeat("x", filterTextMax+1)
	for name, tt := range map[string]struct {
		fieldType, operand string
		in                 FilterValue
		text               string
	}{
		"text blank":               {"TEXT", "CONTAINS", FilterValue{Text: ptr("  ")}, "text must be"},
		"text empty":               {"TEXT", "CONTAINS", FilterValue{Text: ptr("")}, "text must be"},
		"text control":             {"TEXT", "CONTAINS", FilterValue{Text: ptr("a\x00b")}, "control characters"},
		"text newline":             {"TEXT", "CONTAINS", FilterValue{Text: ptr("a\nb")}, "control characters"},
		"text format char":         {"TEXT", "CONTAINS", FilterValue{Text: ptr("a‮b")}, "control characters"},
		"text too long":            {"TEXT", "CONTAINS", FilterValue{Text: &long}, "text must be"},
		"text invalid utf8":        {"TEXT", "CONTAINS", FilterValue{Text: ptr("a\xffb")}, "text must be"},
		"text wrong kind":          {"TEXT", "CONTAINS", FilterValue{Number: ptr(1.0)}, "exactly one value"},
		"text missing":             {"TEXT", "CONTAINS", FilterValue{}, "exactly one value"},
		"text two values":          {"TEXT", "CONTAINS", FilterValue{Text: ptr("a"), Flag: ptr(true)}, "exactly one value"},
		"select unknown option":    {"SELECT", "IS", FilterValue{Options: []string{"Z"}}, "options of this field"},
		"select one unknown":       {"SELECT", "IS", FilterValue{Options: []string{"A", "Z"}}, "options of this field"},
		"select empty list":        {"SELECT", "IS", FilterValue{Options: []string{}}, "1 to 20"},
		"select duplicate":         {"SELECT", "IS", FilterValue{Options: []string{"A", "A"}}, "unique"},
		"select control":           {"SELECT", "IS", FilterValue{Options: []string{"A\n"}}, "unique option values"},
		"select as text":           {"SELECT", "IS", FilterValue{Text: ptr("A")}, "exactly one value"},
		"select too many":          {"SELECT", "IS", FilterValue{Options: make([]string, filterOptionsMax+1)}, "1 to 20"},
		"multi select unknown":     {"MULTI_SELECT", "CONTAINS", FilterValue{Options: []string{"Q"}}, "options of this field"},
		"number too large":         {"NUMBER", "IS", FilterValue{Number: ptr(1e16)}, "number must be"},
		"number as text":           {"NUMBER", "IS", FilterValue{Text: ptr("5")}, "exactly one value"},
		"date impossible":          {"DATE", "IS", FilterValue{Date: ptr("2026-02-30")}, "date must be"},
		"date with time":           {"DATE", "IS", FilterValue{Date: ptr("2026-02-28T00:00:00Z")}, "date must be"},
		"date slashes":             {"DATE", "IS", FilterValue{Date: ptr("28/02/2026")}, "date must be"},
		"date as number":           {"DATE", "IS", FilterValue{Number: ptr(20260228.0)}, "exactly one value"},
		"date time before day":     {"DATE_TIME", "IS_BEFORE", FilterValue{Date: ptr("2026-03-01")}, "date must be"},
		"date time offset":         {"DATE_TIME", "IS_AFTER", FilterValue{Date: ptr("2026-03-01T10:00:00+01:00")}, "date must be"},
		"date time no zone":        {"DATE_TIME", "IS", FilterValue{Date: ptr("2026-03-01T10:00:00")}, "date must be"},
		"date time garbage":        {"DATE_TIME", "IS", FilterValue{Date: ptr("tomorrow")}, "date must be"},
		"relative bad direction":   {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "LAST", Amount: ptr(1), Unit: "DAY"}}, "direction"},
		"relative bad unit":        {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "PAST", Amount: ptr(1), Unit: "DECADE"}}, "unit"},
		"relative no amount":       {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "PAST", Unit: "DAY"}}, "amount"},
		"relative zero amount":     {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "NEXT", Amount: ptr(0), Unit: "DAY"}}, "amount"},
		"relative huge amount":     {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "NEXT", Amount: ptr(1001), Unit: "DAY"}}, "amount"},
		"relative this amount":     {"DATE", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "THIS", Amount: ptr(2), Unit: "DAY"}}, "no amount"},
		"relative on date time":    {"DATE_TIME", "IS_RELATIVE", FilterValue{Relative: &FilterRelative{Direction: "PAST", Amount: ptr(1), Unit: "DAY"}}, "not supported"},
		"valueless with value":     {"DATE", "IS_TODAY", FilterValue{Date: ptr("2026-01-01")}, "takes no value"},
		"empty with value":         {"TEXT", "IS_EMPTY", FilterValue{Text: ptr("x")}, "takes no value"},
		"in past on date time":     {"DATE_TIME", "IS_IN_PAST", FilterValue{}, "not supported"},
		"operand for text":         {"TEXT", "IS", FilterValue{Text: ptr("x")}, "not supported"},
		"operand for number":       {"NUMBER", "CONTAINS", FilterValue{Number: ptr(1.0)}, "not supported"},
		"operand for select":       {"SELECT", "CONTAINS", FilterValue{Options: []string{"A"}}, "not supported"},
		"operand for multi select": {"MULTI_SELECT", "IS", FilterValue{Options: []string{"A"}}, "not supported"},
		"boolean is empty":         {"BOOLEAN", "IS_EMPTY", FilterValue{}, "not supported"},
		"boolean contains":         {"BOOLEAN", "CONTAINS", FilterValue{Flag: ptr(true)}, "not supported"},
		"boolean as text":          {"BOOLEAN", "IS", FilterValue{Text: ptr("true")}, "exactly one value"},
		"relation empty":           {"RELATION", "IS_EMPTY", FilterValue{}, "not supported"},
		"relation is":              {"RELATION", "IS", FilterValue{Text: ptr("x")}, "not supported"},
		"vector search":            {"TEXT", "VECTOR_SEARCH", FilterValue{Text: ptr("x")}, "supported filter operands"},
		"unknown operand":          {"TEXT", "STARTS_WITH", FilterValue{Text: ptr("x")}, "supported filter operands"},
		"lowercase operand":        {"TEXT", "contains", FilterValue{Text: ptr("x")}, "supported filter operands"},
		"no operand":               {"TEXT", "", FilterValue{}, "supported filter operands"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := checkFilterValue(tt.fieldType, filterSchemaOptions, tt.operand, tt.in)
			if err == nil || got != "" || !asInvalidOK(err) || !strings.Contains(err.Error(), tt.text) {
				t.Errorf("got %q, %v, want invalid-request with %q", got, err, tt.text)
			}
		})
	}
}

func TestCheckFilterValueNeedsTheOptionsOfTheSchema(t *testing.T) {
	if _, err := checkFilterValue("SELECT", nil, "IS", FilterValue{Options: []string{"A"}}); err == nil {
		t.Error("an option was accepted for a field without options")
	}
}

func TestFilterRuleCoversEveryOperandOfTheTable(t *testing.T) {
	for _, operand := range filterOperands {
		found := false
		for _, fieldType := range append([]string{"BOOLEAN"}, filterEmptyTypes...) {
			if _, ok := filterRule(fieldType, operand); ok {
				found = true
			}
		}
		if !found {
			t.Errorf("operand %s is offered but fits no field type", operand)
		}
	}
	if filterFieldSupported("RELATION") || filterFieldSupported("TS_VECTOR") || filterFieldSupported("POSITION") {
		t.Error("an unsupported field type is supported")
	}
}
