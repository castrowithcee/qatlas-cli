package twentycrm

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	filterTextMax           = 500
	filterOptionsMax        = 20
	filterNumberMax         = 1e15
	filterRelativeAmountMax = 1000

	errFilterOperand = "operand is not one of the supported filter operands"
)

// Filter values are checked here, by field type and operand, and independent of any endpoint: Twenty takes
// every value as a string, and the agent never supplies that string itself.
var (
	// filterOperands are the operands Qatlas writes. VECTOR_SEARCH and IS_NOT_NULL are not among them.
	filterOperands = []string{"IS", "IS_NOT", "GREATER_THAN_OR_EQUAL", "LESS_THAN_OR_EQUAL", "IS_BEFORE", "IS_AFTER",
		"CONTAINS", "DOES_NOT_CONTAIN", "IS_EMPTY", "IS_NOT_EMPTY", "IS_RELATIVE", "IS_IN_PAST", "IS_IN_FUTURE",
		"IS_TODAY"}

	// filterEmptyTypes are the field types that take IS_EMPTY and IS_NOT_EMPTY. Relation fields are left out.
	filterEmptyTypes = []string{"TEXT", "EMAILS", "FULL_NAME", "ADDRESS", "LINKS", "PHONES", "CURRENCY", "NUMBER",
		"RAW_JSON", "FILES", "DATE_TIME", "DATE", "RATING", "MULTI_SELECT", "SELECT", "ACTOR", "ARRAY", "UUID"}

	filterRelativeDirections = []string{"PAST", "NEXT", "THIS"}
	filterRelativeUnits      = []string{"SECOND", "MINUTE", "HOUR", "DAY", "WEEK", "MONTH", "QUARTER", "YEAR"}

	filterDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// FilterRelative is a relative date: a direction, an amount of units, and a unit. THIS takes no amount.
type FilterRelative struct {
	Direction string `json:"direction"`
	Amount    *int   `json:"amount"`
	Unit      string `json:"unit"`
}

// FilterValue holds the typed value arguments of a filter. At most one is set.
type FilterValue struct {
	Text     *string         `json:"text"`
	Options  []string        `json:"options"`
	Number   *float64        `json:"number"`
	Date     *string         `json:"date"`
	Flag     *bool           `json:"flag"`
	Relative *FilterRelative `json:"relative"`
}

func (v FilterValue) count() int {
	n := 0
	for _, set := range []bool{v.Text != nil, v.Options != nil, v.Number != nil, v.Date != nil, v.Flag != nil,
		v.Relative != nil} {
		if set {
			n++
		}
	}
	return n
}

type filterValueKind int

const (
	filterNone filterValueKind = iota
	filterText
	filterOptions
	filterNumber
	filterDate
	filterDateOrInstant
	filterInstant
	filterRelative
	filterFlag
)

var filterKindNames = map[filterValueKind]string{filterText: "text", filterOptions: "options", filterNumber: "number",
	filterDate: "date as YYYY-MM-DD", filterDateOrInstant: "date as YYYY-MM-DD or a UTC timestamp ending in Z",
	filterInstant: "date as a UTC timestamp ending in Z", filterRelative: "relative", filterFlag: "flag"}

// filterRule returns the kind of value that the operand takes on the field type, and whether the pair exists.
func filterRule(fieldType, operand string) (filterValueKind, bool) {
	if operand == "IS_EMPTY" || operand == "IS_NOT_EMPTY" {
		return filterNone, containsName(filterEmptyTypes, fieldType)
	}
	switch fieldType {
	case "TEXT":
		return filterText, operand == "CONTAINS" || operand == "DOES_NOT_CONTAIN"
	case "SELECT":
		return filterOptions, operand == "IS" || operand == "IS_NOT"
	case "MULTI_SELECT":
		return filterOptions, operand == "CONTAINS" || operand == "DOES_NOT_CONTAIN"
	case "NUMBER":
		return filterNumber, containsName([]string{"IS", "IS_NOT", "GREATER_THAN_OR_EQUAL", "LESS_THAN_OR_EQUAL"}, operand)
	case "DATE":
		switch operand {
		case "IS", "IS_BEFORE", "IS_AFTER":
			return filterDate, true
		case "IS_RELATIVE":
			return filterRelative, true
		case "IS_IN_PAST", "IS_IN_FUTURE", "IS_TODAY":
			return filterNone, true
		}
	case "DATE_TIME":
		switch operand {
		case "IS":
			return filterDateOrInstant, true
		case "IS_BEFORE", "IS_AFTER":
			return filterInstant, true
		}
	case "BOOLEAN":
		return filterFlag, operand == "IS"
	}
	return filterNone, false
}

// filterFieldSupported reports whether Qatlas writes any filter on a field of the type.
func filterFieldSupported(fieldType string) bool {
	return fieldType == "BOOLEAN" || containsName(filterEmptyTypes, fieldType)
}

// checkFilterValue checks operand and value against the field type and returns the string Twenty stores.
// options are the options of a SELECT or MULTI_SELECT field as read from the object schema.
func checkFilterValue(fieldType string, options []MetaOption, operand string, in FilterValue) (string, error) {
	if !containsName(filterOperands, operand) {
		return "", invalidRequest(errFilterOperand)
	}
	kind, ok := filterRule(fieldType, operand)
	if !ok {
		return "", invalidRequest("the operand " + operand + " is not supported for a field of type " + fieldType +
			"; see the operands by field type in the tool description")
	}
	if kind == filterNone {
		if in.count() != 0 {
			return "", invalidRequest("the operand " + operand + " takes no value")
		}
		return "", nil
	}
	bad := invalidRequest("the operand " + operand + " on a field of type " + fieldType + " takes exactly one value: " +
		filterKindNames[kind])
	if in.count() != 1 {
		return "", bad
	}
	switch kind {
	case filterText:
		if in.Text == nil {
			return "", bad
		}
		return checkFilterText(*in.Text)
	case filterOptions:
		if in.Options == nil {
			return "", bad
		}
		return checkFilterOptions(in.Options, options)
	case filterNumber:
		if in.Number == nil {
			return "", bad
		}
		if math.IsNaN(*in.Number) || math.Abs(*in.Number) > filterNumberMax {
			return "", invalidRequest("number must be finite and at most 1e15 in size")
		}
		return strconv.FormatFloat(*in.Number, 'f', -1, 64), nil
	case filterDate, filterDateOrInstant, filterInstant:
		if in.Date == nil {
			return "", bad
		}
		return checkFilterDate(kind, *in.Date)
	case filterRelative:
		if in.Relative == nil {
			return "", bad
		}
		return checkFilterRelative(*in.Relative)
	default:
		if in.Flag == nil {
			return "", bad
		}
		return strconv.FormatBool(*in.Flag), nil
	}
}

func cleanFilterString(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

func checkFilterText(text string) (string, error) {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > filterTextMax || !cleanFilterString(text) {
		return "", invalidRequest("text must be 1 to 500 characters, not blank, and free of control characters")
	}
	return text, nil
}

func checkFilterOptions(chosen []string, schema []MetaOption) (string, error) {
	if len(chosen) == 0 || len(chosen) > filterOptionsMax {
		return "", invalidRequest("options must hold 1 to 20 option values")
	}
	seen := map[string]bool{}
	for _, value := range chosen {
		if value == "" || utf8.RuneCountInString(value) > metaNameMax || !cleanFilterString(value) || seen[value] {
			return "", invalidRequest("options must be unique option values without control characters")
		}
		seen[value] = true
		found := false
		for _, option := range schema {
			found = found || option.Value == value
		}
		if !found {
			return "", invalidRequest("an option is not among the options of this field in its schema")
		}
	}
	encoded, err := json.Marshal(chosen)
	if err != nil {
		return "", invalidRequest("options could not be encoded")
	}
	return string(encoded), nil
}

func checkFilterDate(kind filterValueKind, value string) (string, error) {
	bad := invalidRequest("date must be a " + filterKindNames[kind])
	if filterDatePattern.MatchString(value) {
		if kind == filterInstant {
			return "", bad
		}
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return "", bad
		}
		return value, nil
	}
	if kind == filterDate || !strings.HasSuffix(value, "Z") || len(value) > 40 {
		return "", bad
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return "", bad
	}
	return value, nil
}

func checkFilterRelative(rel FilterRelative) (string, error) {
	if !containsName(filterRelativeDirections, rel.Direction) || !containsName(filterRelativeUnits, rel.Unit) {
		return "", invalidRequest("relative needs a direction of PAST, NEXT, or THIS and a unit of SECOND, MINUTE, " +
			"HOUR, DAY, WEEK, MONTH, QUARTER, or YEAR")
	}
	// Twenty's own interface writes THIS with the amount 1, and reads no zone or weekday from Qatlas.
	if rel.Direction == "THIS" {
		if rel.Amount != nil {
			return "", invalidRequest("relative THIS takes no amount")
		}
		return "THIS_1_" + rel.Unit, nil
	}
	if rel.Amount == nil || *rel.Amount < 1 || *rel.Amount > filterRelativeAmountMax {
		return "", invalidRequest("relative PAST and NEXT need an amount from 1 through 1000")
	}
	return rel.Direction + "_" + strconv.Itoa(*rel.Amount) + "_" + rel.Unit, nil
}
