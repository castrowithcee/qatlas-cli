package googlepagespeed

import (
	"encoding/json"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// What Google returns is third-party content. The answer types of this package carry their bounds in the
// types of their members: a text type cuts itself while it is decoded, so a long string is never kept whole,
// and a member that the type does not list is skipped by the decoder and never held. The output schema is
// derived from the same types, so schema and result cannot drift apart.

// Bounds of what a result shows.
const (
	maxShortRunes = 200
	maxTitleRunes = 300
	maxTextRunes  = 1000
	maxLinkBytes  = 2048
	maxListItems  = 20  // run warnings, origins, list members that have no larger reason
	maxBuckets    = 20  // buckets of one distribution or histogram
	maxSeries     = 40  // collection periods and values of one history series; Google allows 40 periods
	maxMetrics    = 30  // metrics of one record or loading experience
	maxAuditRefs  = 200 // audit references of one category
	maxAudits     = 300 // audits of one Lighthouse result
	maxEntities   = 100
	maxStacks     = 20
	maxLabels     = 30 // entries of a small label map: groups, descriptions, credits, fractions
)

// Text types. Each cuts to its limit and marks the cut with …; a value that is not a string is dropped.
type (
	short  string // identifiers, enumerations, dates: 200 characters
	title  string // names and titles: 300 characters
	prose  string // descriptions and explanations: 1000 characters
	link   string // an absolute http or https address without user information, otherwise dropped
	scalar struct{ V any }
)

func (s *short) UnmarshalJSON(b []byte) error { *s = short(clipJSON(b, maxShortRunes)); return nil }
func (s *title) UnmarshalJSON(b []byte) error { *s = title(clipJSON(b, maxTitleRunes)); return nil }
func (s *prose) UnmarshalJSON(b []byte) error { *s = prose(clipJSON(b, maxTextRunes)); return nil }
func (s *link) UnmarshalJSON(b []byte) error {
	var value string
	if json.Unmarshal(b, &value) == nil {
		*s = link(address(value))
	}
	return nil
}

// UnmarshalJSON keeps a number or a short string; Google types such values loosely (a CLS percentile is a
// string, a fraction can be NaN) and anything else is dropped.
func (s *scalar) UnmarshalJSON(b []byte) error {
	var value any
	if json.Unmarshal(b, &value) != nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			s.V = v
		}
	case string:
		s.V = clip(v, maxShortRunes)
	}
	return nil
}

func (s scalar) MarshalJSON() ([]byte, error) { return json.Marshal(s.V) }

// textList is a list of short texts. A single string is a list of one and anything else is empty; it keeps at
// most maxListItems entries.
type textList []string

func (l *textList) UnmarshalJSON(b []byte) error {
	*l = nil
	var items []any
	if json.Unmarshal(b, &items) != nil {
		var one string
		if json.Unmarshal(b, &one) == nil {
			items = []any{one}
		}
	}
	for _, item := range items {
		if text, ok := item.(string); ok && len(*l) < maxListItems {
			*l = append(*l, clip(text, 500))
		}
	}
	return nil
}

// clipJSON decodes a JSON string and cuts it to limit characters; a value that is no string is empty.
func clipJSON(b []byte, limit int) string {
	var value string
	if json.Unmarshal(b, &value) != nil {
		return ""
	}
	return clip(value, limit)
}

// clip cuts a string to at most limit characters and marks the cut.
func clip(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

// address returns value when it is an absolute http or https address without user information and without
// whitespace, and "" otherwise. Such an address is shown and never fetched.
func address(value string) string {
	if value == "" || len(value) > maxLinkBytes {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	return value
}

// head returns at most n leading members of a list.
func head[T any](list []T, n int) []T {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// firstKeys returns the map with at most n entries, the ones with the first keys in sorted order.
func firstKeys[T any](m map[string]T, n int) map[string]T {
	if len(m) <= n {
		return m
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make(map[string]T, n)
	for _, key := range keys[:n] {
		out[key] = m[key]
	}
	return out
}

// schemaOf renders the JSON schema of an answer type. Maps are objects with typed members, structs are
// objects with the members their json tags name, and nothing else is allowed.
func schemaOf(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case reflect.TypeOf(scalar{}):
		return `{"anyOf":[{"type":"number"},{"type":"string"}]}`
	case reflect.TypeOf(textList{}):
		return `{"type":"array","items":{"type":"string"}}`
	}
	switch t.Kind() {
	case reflect.String:
		return `{"type":"string"}`
	case reflect.Bool:
		return `{"type":"boolean"}`
	case reflect.Int, reflect.Int32, reflect.Int64:
		return `{"type":"integer"}`
	case reflect.Float64:
		return `{"type":"number"}`
	case reflect.Slice:
		return `{"type":"array","items":` + schemaOf(t.Elem()) + `}`
	case reflect.Map:
		return `{"type":"object","additionalProperties":` + schemaOf(t.Elem()) + `}`
	case reflect.Struct:
		var b strings.Builder
		b.WriteString(`{"type":"object","properties":{`)
		first := true
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(`"` + name + `":` + schemaOf(t.Field(i).Type))
		}
		b.WriteString(`},"additionalProperties":false}`)
		return b.String()
	}
	panic("googlepagespeed: no schema for " + t.String())
}

func itoa(n int) string { return strconv.Itoa(n) }

func joinNames(names []string) string { return strings.Join(names, ", ") }
