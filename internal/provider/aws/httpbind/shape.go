// Package httpbind implements the HTTP binding traits of Smithy (httpLabel, httpQuery, httpQueryParams,
// httpHeader, httpPrefixHeaders, httpPayload, httpResponseCode) for the AWS REST protocols. It owns
// everything that does not depend on the body format: the method and URI pattern, the places a member can
// live, scalar and timestamp text forms, the request and response walk and the HTTP exchange. The body
// itself goes through a Codec, so restJson1 (package restjson) and a later restXml share all of it.
//
// An operation is data: an Operation holds the method, the URI pattern and two Shapes whose Fields name
// where each member is bound. The layer reads these traits and knows no operation names. What the
// description does not bind unambiguously is an error, never a guess.
package httpbind

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Kind is the Smithy type of a Shape, reduced to what the REST protocols serialize. Integer covers byte,
// short, integer and long; Float covers float and double; enums are strings. A union is a KindStruct with
// Union set.
type Kind uint8

// Kinds a Shape can have.
const (
	KindString Kind = iota + 1
	KindInteger
	KindFloat
	KindBoolean
	KindTimestamp
	KindBlob
	KindStruct
	KindList
	KindMap
	// KindDocument is the Smithy document type: any JSON value, bounded.
	KindDocument
)

// Format is a timestamp format. The zero value means the default of the place: http-date in a header,
// date-time in a label or query, and the body format of the Codec in a body.
type Format uint8

// Timestamp formats of the timestampFormat trait.
const (
	FormatDefault Format = iota
	FormatEpochSeconds
	FormatDateTime
	FormatHTTPDate
)

// Loc is the place a top-level member is bound to. The zero value is the document body.
type Loc uint8

// Places of a member.
const (
	LocBody Loc = iota
	LocLabel
	LocQuery
	LocQueryParams
	LocHeader
	LocPrefixHeaders
	LocPayload
	LocResponseCode
)

// Default caps of what a decoded answer may hold. Every value of an answer is provider content.
const (
	DefaultMaxString = 128 << 10
	DefaultMaxItems  = 1000
	// MaxDepth bounds the nesting of shapes. Codecs bound the nesting of answer elements by their own limit.
	MaxDepth = 16
)

// Shape describes one input or output type as data.
type Shape struct {
	Kind Kind
	// Fields are the members of a KindStruct.
	Fields []Field
	// Union marks a KindStruct whose members exclude each other: exactly one is set.
	Union bool
	// Elem is the element of a KindList and the value of a KindMap (map keys are strings).
	Elem *Shape
	// Format is the resolved timestampFormat of a KindTimestamp: the member trait if set, else the trait of
	// the target shape. The generator resolves it, the layer does not look further.
	Format Format
	// MaxLen caps a decoded string in bytes, DefaultMaxString when zero. A longer string is cut at a rune
	// boundary and reported as truncated. A blob, a timestamp and a map key above the cap are refused, as a
	// cut value would no longer mean what it said.
	MaxLen int
	// MaxItems caps a decoded list or map, DefaultMaxItems when zero. A longer one is refused: cutting it
	// would present a partial list as complete.
	MaxItems int
}

// Field is one member of a struct Shape.
type Field struct {
	// Key is the name of the member in the argument and result maps, the Qatlas-facing name.
	Key string
	// Name is the wire name: the label name, the query parameter or header name, the header prefix of a
	// prefix-headers member, or in the body the member name or its jsonName/xmlName.
	Name  string
	Shape *Shape
	// Loc is the place of the member; only a member of the top level of an input or output has one.
	Loc Loc
	// Required rejects a call without the member before any I/O. A label is always required.
	Required bool
	// IdempotencyToken marks the idempotencyToken member of a top-level input: Encode fills it when the
	// caller gave none and reports the value it sent. Only a string member in the body, a query parameter or
	// a header may carry it.
	IdempotencyToken bool
}

// ErrShape reports a description that is not a valid operation. It is a defect of the description, and the
// wrapped text names the rule, never a value.
var ErrShape = errors.New("httpbind: invalid operation description")

func shapeErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrShape, fmt.Sprintf(format, args...))
}

var (
	labelName   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	headerToken = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$")
)

// reservedHeaders are never bound to a member: the protocol or the signature owns them, so a model cannot
// make the tool set them.
var reservedHeaders = map[string]bool{
	"host": true, "authorization": true, "content-length": true, "content-type": true, "accept": true,
	"transfer-encoding": true, "connection": true, "expect": true, "user-agent": true,
	"x-amz-date": true, "x-amz-security-token": true, "x-amz-content-sha256": true,
}

func isScalar(k Kind) bool {
	switch k {
	case KindString, KindInteger, KindFloat, KindBoolean, KindTimestamp, KindBlob:
		return true
	}
	return false
}

// textScalar is a scalar with a text form in a label, query or header (not a blob).
func textScalar(s *Shape) bool { return s != nil && isScalar(s.Kind) && s.Kind != KindBlob }

// validName reports a wire name of a body member or a query parameter.
func validName(n string) bool {
	if n == "" || len(n) > 128 || !utf8.ValidString(n) {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// validate checks a nested shape, whose members are all body members.
func (s *Shape) validate(depth int) error {
	if s == nil || depth > MaxDepth {
		return shapeErr("a shape is missing or nested too deep")
	}
	if s.MaxLen < 0 || s.MaxItems < 0 || (s.Format != FormatDefault && s.Kind != KindTimestamp) || s.Format > FormatHTTPDate {
		return shapeErr("a limit or a timestamp format is misplaced")
	}
	if s.Union && s.Kind != KindStruct {
		return shapeErr("only a structure can be a union")
	}
	switch s.Kind {
	case KindString, KindInteger, KindFloat, KindBoolean, KindTimestamp, KindBlob, KindDocument:
	case KindStruct:
		keys, names := map[string]bool{}, map[string]bool{}
		for _, f := range s.Fields {
			if f.Key == "" || !validName(f.Name) || keys[f.Key] || names[f.Name] {
				return shapeErr("member %q has no unique key or a bad wire name", f.Key)
			}
			keys[f.Key], names[f.Name] = true, true
			if f.Loc != LocBody || f.IdempotencyToken {
				return shapeErr("member %q: only a top-level member has a place or a token", f.Key)
			}
			if err := f.Shape.validate(depth + 1); err != nil {
				return err
			}
		}
		if s.Union && len(s.Fields) == 0 {
			return shapeErr("a union has no members")
		}
	case KindList, KindMap:
		return s.Elem.validate(depth + 1)
	default:
		return shapeErr("unknown kind")
	}
	return nil
}

// topLevel is what the top-level members of an input or output bind to.
type topLevel struct {
	body    []Field
	payload *Field
	labels  map[string]Field
	// byLoc keeps the members of the other places in description order.
	byLoc map[Loc][]Field
}

// classify validates the top level of an input or output and sorts its members by place.
func classify(s *Shape, input bool) (topLevel, error) {
	t := topLevel{labels: map[string]Field{}, byLoc: map[Loc][]Field{}}
	if s == nil {
		return t, nil
	}
	if s.Kind != KindStruct || s.Union || s.MaxLen < 0 || s.MaxItems < 0 {
		return t, shapeErr("the top level must be a structure")
	}
	keys := map[string]bool{}
	seen := map[string]bool{}
	tokens := 0
	for _, f := range s.Fields {
		if f.Key == "" || keys[f.Key] || f.Shape == nil {
			return t, shapeErr("member %q has no unique key or no shape", f.Key)
		}
		keys[f.Key] = true
		if f.IdempotencyToken {
			tokens++
			if !input || f.Shape.Kind != KindString || (f.Loc != LocBody && f.Loc != LocQuery && f.Loc != LocHeader) || tokens > 1 {
				return t, shapeErr("member %q cannot be the idempotency token", f.Key)
			}
		}
		// A member of a place that makes no sense in this direction is refused, not ignored.
		if (!input && (f.Loc == LocLabel || f.Loc == LocQuery || f.Loc == LocQueryParams)) || (input && f.Loc == LocResponseCode) {
			return t, shapeErr("member %q: the place does not apply to this direction", f.Key)
		}
		if err := f.Shape.validate(1); err != nil {
			return t, err
		}
		unique := func(place string) error {
			id := place + "\x00" + strings.ToLower(f.Name)
			if seen[id] {
				return shapeErr("two members bind the same %s name", place)
			}
			seen[id] = true
			return nil
		}
		switch f.Loc {
		case LocBody:
			if !validName(f.Name) {
				return t, shapeErr("member %q has a bad wire name", f.Key)
			}
			if err := unique("body"); err != nil {
				return t, err
			}
			t.body = append(t.body, f)
		case LocLabel:
			if !labelName.MatchString(f.Name) || !textScalar(f.Shape) {
				return t, shapeErr("label %q is not a string, number, boolean or timestamp", f.Key)
			}
			if err := unique("label"); err != nil {
				return t, err
			}
			f.Required = true
			t.labels[f.Name] = f
		case LocQuery:
			elem := f.Shape
			if f.Shape.Kind == KindList {
				elem = f.Shape.Elem
			}
			if !validName(f.Name) || !textScalar(elem) {
				return t, shapeErr("query member %q is not a scalar or a list of scalars", f.Key)
			}
			if err := unique("query"); err != nil {
				return t, err
			}
			t.byLoc[LocQuery] = append(t.byLoc[LocQuery], f)
		case LocQueryParams:
			ok := f.Shape.Kind == KindMap && f.Shape.Elem != nil &&
				(f.Shape.Elem.Kind == KindString || (f.Shape.Elem.Kind == KindList && f.Shape.Elem.Elem != nil && f.Shape.Elem.Elem.Kind == KindString))
			if !ok || len(t.byLoc[LocQueryParams]) > 0 {
				return t, shapeErr("query-params member %q must be the only map of strings or string lists", f.Key)
			}
			t.byLoc[LocQueryParams] = append(t.byLoc[LocQueryParams], f)
		case LocHeader:
			elem := f.Shape
			if f.Shape.Kind == KindList {
				elem = f.Shape.Elem
			}
			if !headerToken.MatchString(f.Name) || reservedHeaders[strings.ToLower(f.Name)] || !textScalar(elem) {
				return t, shapeErr("header member %q has a reserved or malformed name or an unsupported type", f.Key)
			}
			if err := unique("header"); err != nil {
				return t, err
			}
			t.byLoc[LocHeader] = append(t.byLoc[LocHeader], f)
		case LocPrefixHeaders:
			ok := f.Shape.Kind == KindMap && f.Shape.Elem != nil && f.Shape.Elem.Kind == KindString
			// The prefix is a header name stem; it may end in a dash, which the token alphabet allows.
			if !ok || !headerToken.MatchString(f.Name) || len(t.byLoc[LocPrefixHeaders]) > 0 {
				return t, shapeErr("prefix-headers member %q must be the only map of strings with a token prefix", f.Key)
			}
			t.byLoc[LocPrefixHeaders] = append(t.byLoc[LocPrefixHeaders], f)
		case LocPayload:
			switch f.Shape.Kind {
			case KindString, KindBlob, KindStruct, KindDocument:
			default:
				return t, shapeErr("payload member %q must be a string, blob, structure, union or document", f.Key)
			}
			if t.payload != nil {
				return t, shapeErr("more than one payload member")
			}
			p := f
			t.payload = &p
		case LocResponseCode:
			if f.Shape.Kind != KindInteger {
				return t, shapeErr("the response code member %q must be an integer", f.Key)
			}
			t.byLoc[LocResponseCode] = append(t.byLoc[LocResponseCode], f)
		default:
			return t, shapeErr("member %q has an unknown place", f.Key)
		}
	}
	if t.payload != nil && len(t.body) > 0 {
		return t, shapeErr("a payload member excludes members bound to the body")
	}
	if len(t.byLoc[LocResponseCode]) > 1 {
		return t, shapeErr("more than one response code member")
	}
	return t, nil
}

// bodyShape is the synthetic structure of the members that live in the document body.
func (t topLevel) bodyShape() *Shape { return &Shape{Kind: KindStruct, Fields: t.body} }
