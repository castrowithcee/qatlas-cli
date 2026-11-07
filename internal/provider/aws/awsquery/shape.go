package awsquery

import (
	"errors"
	"regexp"
)

// Kind is the Smithy type of a Shape, reduced to what the query protocol serializes.
type Kind uint8

// Kinds a Shape can have. Integer covers byte, short, integer and long; Float covers float and double.
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
)

// Default caps of what a decoded answer may hold. Every value of an answer is provider content.
const (
	DefaultMaxString = 128 << 10
	DefaultMaxItems  = 1000
	// maxDepth bounds the nesting of shapes and of answer elements.
	maxDepth = 16
)

// Shape describes one input or output type as data, so a generator binds an operation by emitting a Shape
// instead of hand-written encoding. The wire names are the member names of the Smithy model or their
// xmlName trait.
type Shape struct {
	Kind Kind
	// Fields are the members of a KindStruct.
	Fields []Field
	// Elem is the element of a KindList and the value of a KindMap.
	Elem *Shape
	// ElemName is the xmlName of the list member; "member" when empty. A flattened list ignores it, as the
	// specification says: the name of the structure member is used.
	ElemName string
	// KeyName and ValueName are the xmlName of the map key and value; "key" and "value" when empty.
	KeyName, ValueName string
	// MaxLen caps a decoded string in bytes, DefaultMaxString when zero. A longer string is cut at a rune
	// boundary and reported in Meta.Truncated. A blob, a timestamp and a map key above the cap are refused,
	// as a cut value would no longer mean what it said.
	MaxLen int
	// MaxItems caps a decoded list or map, DefaultMaxItems when zero. A longer one is refused: cutting it
	// would present a partial list as complete.
	MaxItems int
}

// Field is one member of a struct Shape.
type Field struct {
	// Key is the name of the member in the argument and result maps, the Qatlas-facing name.
	Key string
	// Name is the wire name: the member name, or its xmlName.
	Name  string
	Shape *Shape
	// Flattened is the xmlFlattened trait of a list or map member.
	Flattened bool
	// IdempotencyToken marks the idempotencyToken member of a top-level input: Encode fills it when the
	// caller gave none and reports the value it sent. Only a string member of the top level may carry it.
	IdempotencyToken bool
}

// ErrShape reports a Shape that does not describe a valid type. It is a defect of the description.
var ErrShape = errors.New("awsquery: invalid shape description")

var wireName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

// Validate checks a Shape the way Encode and Decode need it, so a generator or a test can reject a faulty
// description once instead of on a call.
func (s *Shape) Validate() error { return s.validate(0, true) }

func (s *Shape) validate(depth int, top bool) error {
	if s == nil || depth > maxDepth {
		return ErrShape
	}
	switch s.Kind {
	case KindString, KindInteger, KindFloat, KindBoolean, KindTimestamp, KindBlob:
	case KindStruct:
		keys, names := map[string]bool{}, map[string]bool{}
		for _, f := range s.Fields {
			if f.Key == "" || !wireName.MatchString(f.Name) || keys[f.Key] || names[f.Name] {
				return ErrShape
			}
			keys[f.Key], names[f.Name] = true, true
			if f.Flattened && f.Shape != nil && f.Shape.Kind != KindList && f.Shape.Kind != KindMap {
				return ErrShape
			}
			if f.IdempotencyToken && (!top || f.Shape == nil || f.Shape.Kind != KindString) {
				return ErrShape
			}
			if err := f.Shape.validate(depth+1, false); err != nil {
				return err
			}
		}
	case KindList, KindMap:
		for _, n := range []string{s.ElemName, s.KeyName, s.ValueName} {
			if n != "" && !wireName.MatchString(n) {
				return ErrShape
			}
		}
		return s.Elem.validate(depth+1, false)
	default:
		return ErrShape
	}
	return nil
}

func (s *Shape) elemName() string  { return orDefault(s.ElemName, "member") }
func (s *Shape) keyName() string   { return orDefault(s.KeyName, "key") }
func (s *Shape) valueName() string { return orDefault(s.ValueName, "value") }

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
