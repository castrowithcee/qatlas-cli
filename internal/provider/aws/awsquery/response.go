package awsquery

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxNodes bounds the elements of one result before any value is bound. The response size limit already
// bounds the bytes; this bounds the work and the memory of the tree.
const maxNodes = 100000

var errInvalidResult = errors.New("awsquery: the result does not fit its shape")

// node is one element of the result. Only a leaf keeps its text.
type node struct {
	name string
	text []byte
	kids []*node
}

// readNode reads the element that start opened. Namespaces are ignored: the Smithy xmlNamespace trait puts
// xmlns attributes on structures, list members and flattened members, and a name is matched by its local
// part only.
func readNode(d *xml.Decoder, start xml.StartElement, depth int, budget *int) (*node, error) {
	if depth > maxDepth*2 {
		return nil, errInvalidResult
	}
	if *budget--; *budget < 0 {
		return nil, errInvalidResult
	}
	n := &node{name: start.Name.Local}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			kid, err := readNode(d, t, depth+1, budget)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, kid)
		case xml.CharData:
			n.text = append(n.text, t...)
		case xml.EndElement:
			return n, nil
		}
	}
}

// decoded carries what binding noticed on the way.
type decoded struct{ truncated bool }

// bindResult reads the <Action>Result element into the map of the output shape.
func bindResult(d *xml.Decoder, start xml.StartElement, shape *Shape) (map[string]any, bool, error) {
	if shape == nil || shape.Kind != KindStruct || shape.validate(0, true) != nil {
		return nil, false, ErrShape
	}
	budget := maxNodes
	root, err := readNode(d, start, 0, &budget)
	if err != nil {
		return nil, false, err
	}
	var dec decoded
	out, err := dec.structure(shape, root, 0)
	return out, dec.truncated, err
}

func children(n *node, name string) []*node {
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

// one returns the single child called name. More than one is an error, as choosing one would be a guess.
func one(n *node, name string) (*node, bool, error) {
	found := children(n, name)
	switch len(found) {
	case 0:
		return nil, false, nil
	case 1:
		return found[0], true, nil
	}
	return nil, false, errInvalidResult
}

// structure binds the members of a struct node. A member the answer lacks is left out, except lists and
// maps, which are empty: a flattened list cannot tell absent from empty, so none can. Elements the shape
// does not know are ignored, so a service may add members.
func (dec *decoded) structure(s *Shape, n *node, depth int) (map[string]any, error) {
	if depth > maxDepth {
		return nil, ErrShape
	}
	out := map[string]any{}
	for _, f := range s.Fields {
		switch f.Shape.Kind {
		case KindList:
			items, err := dec.list(f, n, depth)
			if err != nil {
				return nil, err
			}
			out[f.Key] = items
		case KindMap:
			entries, err := dec.mapping(f, n, depth)
			if err != nil {
				return nil, err
			}
			out[f.Key] = entries
		default:
			kid, ok, err := one(n, f.Name)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			v, err := dec.value(f.Shape, kid, depth+1)
			if err != nil {
				return nil, err
			}
			out[f.Key] = v
		}
	}
	return out, nil
}

func (dec *decoded) value(s *Shape, n *node, depth int) (any, error) {
	if s.Kind == KindStruct {
		return dec.structure(s, n, depth)
	}
	if s.Kind == KindList || s.Kind == KindMap {
		// A collection inside a collection: the element node is the container itself.
		return dec.container(s, n, depth)
	}
	return dec.scalar(s, n.text)
}

// container binds a nested, never flattened collection held by n.
func (dec *decoded) container(s *Shape, n *node, depth int) (any, error) {
	if depth > maxDepth {
		return nil, ErrShape
	}
	if s.Kind == KindList {
		return dec.items(s, children(n, s.elemName()), depth)
	}
	return dec.entries(s, children(n, "entry"), depth)
}

func (dec *decoded) list(f Field, n *node, depth int) ([]any, error) {
	if f.Flattened {
		return dec.items(f.Shape, children(n, f.Name), depth)
	}
	holder, ok, err := one(n, f.Name)
	if err != nil || !ok {
		return []any{}, err
	}
	return dec.items(f.Shape, children(holder, f.Shape.elemName()), depth)
}

func (dec *decoded) items(s *Shape, nodes []*node, depth int) ([]any, error) {
	if len(nodes) > orInt(s.MaxItems, DefaultMaxItems) {
		return nil, errInvalidResult
	}
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		v, err := dec.value(s.Elem, n, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (dec *decoded) mapping(f Field, n *node, depth int) (map[string]any, error) {
	if f.Flattened {
		return dec.entries(f.Shape, children(n, f.Name), depth)
	}
	holder, ok, err := one(n, f.Name)
	if err != nil || !ok {
		return map[string]any{}, err
	}
	return dec.entries(f.Shape, children(holder, "entry"), depth)
}

// entries binds map entries. A repeated key is refused: keeping one of two values would be a guess.
func (dec *decoded) entries(s *Shape, nodes []*node, depth int) (map[string]any, error) {
	if len(nodes) > orInt(s.MaxItems, DefaultMaxItems) {
		return nil, errInvalidResult
	}
	out := make(map[string]any, len(nodes))
	for _, entry := range nodes {
		k, kok, kerr := one(entry, s.keyName())
		v, vok, verr := one(entry, s.valueName())
		if kerr != nil || verr != nil || !kok || !vok || len(k.text) > orInt(s.MaxLen, DefaultMaxString) {
			return nil, errInvalidResult
		}
		key := string(k.text)
		if _, dup := out[key]; dup || !utf8.ValidString(key) {
			return nil, errInvalidResult
		}
		val, err := dec.value(s.Elem, v, depth+1)
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func orInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// scalar parses a leaf. A value that does not parse as its kind is an error, not a zero value.
func (dec *decoded) scalar(s *Shape, raw []byte) (any, error) {
	limit := orInt(s.MaxLen, DefaultMaxString)
	if !utf8.Valid(raw) {
		return nil, errInvalidResult
	}
	text := string(raw)
	switch s.Kind {
	case KindString:
		if len(text) > limit {
			text = text[:limit]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
			dec.truncated = true
		}
		return text, nil
	case KindBoolean:
		switch strings.TrimSpace(text) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	case KindInteger:
		if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
			return n, nil
		}
	case KindFloat:
		if f, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, nil
		}
	case KindTimestamp:
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil && len(text) <= limit {
			return t.UTC().Format(time.RFC3339Nano), nil
		}
	case KindBlob:
		text = strings.TrimSpace(text)
		if _, err := base64.StdEncoding.DecodeString(text); err == nil && len(text) <= limit {
			return text, nil
		}
	}
	return nil, errInvalidResult
}
