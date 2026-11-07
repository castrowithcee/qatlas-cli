package googlemaps

import (
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

// A shape describes one part of an answer: which members it keeps, of which JSON type, and how far its text
// and lists are cut. One shape drives both the cleaning of the answer and the output schema, so the two
// cannot drift apart. Everything Google returns is third-party text, so nothing leaves the provider unbounded.

type kind uint8

const (
	kShort kind = iota // text cut at maxShortRunes: identifiers, enumerations, dates
	kTitle             // text cut at maxTitleRunes: names
	kText              // text cut at maxTextRunes: reviews, summaries, descriptions
	kName              // text kept whole up to max bytes and dropped when longer: an identifier that is useless cut
	kAddr              // an address: bounded text that is never fetched, dropped unless safe to show
	kInt               // a whole number
	kNum               // any number
	kBool              // a boolean
	kObj               // an object with the listed members only
	kArr               // a list of one shape
)

type shape struct {
	kind  kind
	props []prop
	elem  *shape
	max   int
}

type prop struct {
	name  string
	shape *shape
}

// Bounds of what a result shows.
const (
	maxTitleRunes   = 300
	maxTextRunes    = 1500
	maxShortRunes   = 200
	maxAddressBytes = 2048
	maxListItems    = 20
)

var (
	short   = &shape{kind: kShort}
	title   = &shape{kind: kTitle}
	text    = &shape{kind: kText}
	addr    = &shape{kind: kAddr}
	integer = &shape{kind: kInt}
	decimal = &shape{kind: kNum}
	boolean = &shape{kind: kBool}
)

// keepWhole is an identifier of at most limit bytes that is shown whole or not at all.
func keepWhole(limit int) *shape { return &shape{kind: kName, max: limit} }

func p(name string, s *shape) prop { return prop{name: name, shape: s} }

func object(props ...prop) *shape { return &shape{kind: kObj, props: props} }

func list(elem *shape) *shape { return &shape{kind: kArr, elem: elem, max: maxListItems} }

func listOf(limit int, elem *shape) *shape { return &shape{kind: kArr, elem: elem, max: limit} }

// clip cuts a string to at most limit characters and marks the cut.
func clip(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

// address returns a result address when it is an absolute http or https address without user information,
// and "" otherwise. Such an address is shown and never fetched.
func address(value string) string {
	if value == "" || len(value) > maxAddressBytes {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	return value
}

// clean returns the part of value that the shape keeps, cut to the limits. It is false when nothing of the
// value is shown: a value of another JSON type than the documented one, an unsafe address, or a null. An
// answer that does not fit its documentation is not refused, because the request was billed already.
func (s *shape) clean(value any) (any, bool) {
	switch s.kind {
	case kShort, kTitle, kText:
		str, ok := value.(string)
		if !ok {
			return nil, false
		}
		return clip(str, s.limit()), true
	case kName:
		str, ok := value.(string)
		if !ok || str == "" || len(str) > s.max {
			return nil, false
		}
		return str, true
	case kAddr:
		str, ok := value.(string)
		if !ok || address(str) == "" {
			return nil, false
		}
		return str, true
	case kInt:
		number, ok := value.(float64)
		if !ok || math.Abs(math.Trunc(number)) >= 1<<53 {
			return nil, false
		}
		return math.Trunc(number), true
	case kNum:
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		return number, true
	case kBool:
		flag, ok := value.(bool)
		return flag, ok
	case kObj:
		members, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		out := map[string]any{}
		for _, property := range s.props {
			if raw, present := members[property.name]; present {
				if cleaned, keep := property.shape.clean(raw); keep {
					out[property.name] = cleaned
				}
			}
		}
		return out, true
	case kArr:
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		out := []any{}
		for _, item := range items {
			if len(out) == s.max {
				break
			}
			if cleaned, keep := s.elem.clean(item); keep {
				out = append(out, cleaned)
			}
		}
		return out, true
	}
	return nil, false
}

func (s *shape) limit() int {
	switch s.kind {
	case kTitle:
		return maxTitleRunes
	case kText:
		return maxTextRunes
	}
	return maxShortRunes
}

// schema renders the shape as the JSON schema of the cleaned value.
func (s *shape) schema() string {
	switch s.kind {
	case kShort, kTitle, kText, kName, kAddr:
		return `{"type":"string"}`
	case kInt:
		return `{"type":"integer"}`
	case kNum:
		return `{"type":"number"}`
	case kBool:
		return `{"type":"boolean"}`
	case kObj:
		var b strings.Builder
		b.WriteString(`{"type":"object","properties":{`)
		for i, property := range s.props {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"` + property.name + `":` + property.shape.schema())
		}
		b.WriteString(`},"additionalProperties":false}`)
		return b.String()
	case kArr:
		return `{"type":"array","items":` + s.elem.schema() + `}`
	}
	return `{}`
}
