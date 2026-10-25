package brave

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// A shape describes one part of an answer: which members it keeps, of which JSON type, and how far its text
// and lists are cut. One shape drives both the cleaning of the answer and the output schema, so the two
// cannot drift apart. The web search answer has far more members than a hand-written struct per object could
// keep readable, and Brave adds members at will.

type kind uint8

const (
	kShort   kind = iota // text cut at maxShortRunes: identifiers, enumerations, dates
	kTitle               // text cut at maxTitleRunes: names and titles
	kText                // text cut at maxTextRunes: descriptions and answers
	kSnippet             // text cut at maxSnippetRunes: excerpts
	kAddr                // an address: bounded text that is never fetched, dropped unless safe to show
	kInt                 // a whole number
	kNum                 // any number
	kBool                // a boolean
	kObj                 // an object with the listed members only
	kArr                 // a list of one shape
	kJSON                // a value whose type Brave does not fix, kept as bounded JSON
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

var (
	short   = &shape{kind: kShort}
	title   = &shape{kind: kTitle}
	body    = &shape{kind: kText}
	snippet = &shape{kind: kSnippet}
	addr    = &shape{kind: kAddr}
	integer = &shape{kind: kInt}
	decimal = &shape{kind: kNum}
	boolean = &shape{kind: kBool}
	anyJSON = &shape{kind: kJSON}
)

// Limits of the lists of an answer and of the bounded JSON values.
const (
	maxListItems   = 20 // results of one kind and the lists inside a result
	maxMixedItems  = 50
	maxJSONDepth   = 6
	maxJSONItems   = 20
	maxJSONMembers = 30
)

func p(name string, s *shape) prop { return prop{name: name, shape: s} }

func object(props ...prop) *shape { return &shape{kind: kObj, props: props} }

func list(elem *shape) *shape { return &shape{kind: kArr, elem: elem, max: maxListItems} }

func listOf(limit int, elem *shape) *shape { return &shape{kind: kArr, elem: elem, max: limit} }

// join returns the members of several groups in one object shape.
func join(groups ...[]prop) []prop {
	var out []prop
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// clean returns the part of value that the shape keeps, cut to the limits. It is false when nothing of the
// value is shown: a value of another JSON type than the documented one, an unsafe address, or a null. An
// answer that does not fit its documentation is not refused, because the request was billed already.
func (s *shape) clean(value any) (any, bool) {
	switch s.kind {
	case kShort, kTitle, kText, kSnippet:
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		return clip(text, s.limit()), true
	case kAddr:
		text, ok := value.(string)
		if !ok || address(text) == "" {
			return nil, false
		}
		return text, true
	case kInt:
		number, ok := value.(float64)
		if !ok {
			return nil, false
		}
		whole := wholeNumber(&number)
		if whole == nil {
			return nil, false
		}
		return *whole, true
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
	default:
		return boundedJSON(value, 0)
	}
}

func (s *shape) limit() int {
	switch s.kind {
	case kTitle:
		return maxTitleRunes
	case kText:
		return maxTextRunes
	case kSnippet:
		return maxSnippetRunes
	}
	return maxShortRunes
}

// activeScheme matches the schemes that a client may run or open without a double slash.
var activeScheme = regexp.MustCompile(`(?i)^\s*(javascript|data|vbscript|file|blob):`)

// boundedJSON keeps a value of unspecified type within limits: nesting, list length, member count, and text
// length are cut, and a text that looks like an address with a scheme is shown only when it is a safe one.
func boundedJSON(value any, depth int) (any, bool) {
	switch value := value.(type) {
	case string:
		if (schemePattern.MatchString(value) || activeScheme.MatchString(value)) && address(value) == "" {
			return nil, false
		}
		return clip(value, maxTextRunes), true
	case float64, bool:
		return value, true
	case []any:
		if depth >= maxJSONDepth {
			return nil, false
		}
		out := []any{}
		for _, item := range value {
			if len(out) == maxJSONItems {
				break
			}
			if cleaned, keep := boundedJSON(item, depth+1); keep {
				out = append(out, cleaned)
			}
		}
		return out, true
	case map[string]any:
		if depth >= maxJSONDepth {
			return nil, false
		}
		names := make([]string, 0, len(value))
		for name := range value {
			names = append(names, name)
		}
		sort.Strings(names)
		out := map[string]any{}
		for _, name := range names {
			if len(out) == maxJSONMembers {
				break
			}
			if cleaned, keep := boundedJSON(value[name], depth+1); keep {
				out[clip(name, maxShortRunes)] = cleaned
			}
		}
		return out, true
	}
	return nil, false
}

// schema renders the shape as the JSON schema of the cleaned value.
func (s *shape) schema() string {
	switch s.kind {
	case kShort, kTitle, kText, kSnippet, kAddr:
		return stringSchema
	case kInt:
		return integerSchema
	case kNum:
		return `{"type":"number"}`
	case kBool:
		return booleanSchema
	case kObj:
		return `{"type":"object","properties":{` + s.propertiesSchema() + `},"additionalProperties":false}`
	case kArr:
		return `{"type":"array","items":` + s.elem.schema() + `}`
	}
	return `{}`
}

// propertiesSchema renders the members of an object shape as the inside of a "properties" schema.
func (s *shape) propertiesSchema() string {
	var b strings.Builder
	for i, property := range s.props {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + property.name + `":` + property.shape.schema())
	}
	return b.String()
}
