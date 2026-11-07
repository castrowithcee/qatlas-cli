package restjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
)

var errInvalid = errors.New("restjson: the body is not valid JSON of the expected form")

// node is one JSON value. Strings keep their decoded text, numbers their literal.
type node struct {
	kind byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' boolean, 'z' null
	text string
	keys []string
	kids []*node
}

// parse reads exactly one JSON value, bounded in nodes and depth. A repeated key of an object is refused:
// keeping one of two values would be a guess.
func parse(data []byte) (*node, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	budget := maxNodes
	root, err := read(d, 0, &budget)
	if err != nil {
		return nil, errInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errInvalid
	}
	return root, nil
}

func read(d *json.Decoder, depth int, budget *int) (*node, error) {
	if depth > maxJSONDepth {
		return nil, errInvalid
	}
	if *budget--; *budget < 0 {
		return nil, errInvalid
	}
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := &node{kind: 'a'}
		if t == '{' {
			n.kind = 'o'
			seen := map[string]bool{}
			for d.More() {
				keyTok, err := d.Token()
				key, ok := keyTok.(string)
				if err != nil || !ok || seen[key] {
					return nil, errInvalid
				}
				seen[key] = true
				kid, err := read(d, depth+1, budget)
				if err != nil {
					return nil, err
				}
				n.keys, n.kids = append(n.keys, key), append(n.kids, kid)
			}
		} else {
			for d.More() {
				kid, err := read(d, depth+1, budget)
				if err != nil {
					return nil, err
				}
				n.kids = append(n.kids, kid)
			}
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return n, nil
	case string:
		return &node{kind: 's', text: t}, nil
	case json.Number:
		return &node{kind: 'n', text: t.String()}, nil
	case bool:
		if t {
			return &node{kind: 'b', text: "true"}, nil
		}
		return &node{kind: 'b', text: "false"}, nil
	case nil:
		return &node{kind: 'z'}, nil
	}
	return nil, errInvalid
}

// Unmarshal implements httpbind.Codec. A structure comes back as map[string]any under the Field.Key names;
// members the shape does not know are ignored, so a service may add members, and a null member is absent.
// A list or map above its cap, a repeated key and a value of the wrong JSON type are errors. A union with
// two known members is an error; one with none (a member this description does not know) is empty.
func (Codec) Unmarshal(s *httpbind.Shape, data []byte) (any, bool, error) {
	root, err := parse(data)
	if err != nil {
		return nil, false, err
	}
	var dec decoder
	v, err := dec.value(s, root, 0)
	return v, dec.cut, err
}

type decoder struct{ cut bool }

func (dec *decoder) value(s *httpbind.Shape, n *node, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errInvalid
	}
	if n.kind == 'z' {
		return nil, nil
	}
	switch s.Kind {
	case httpbind.KindStruct:
		if n.kind != 'o' {
			return nil, errInvalid
		}
		return dec.structure(s, n, depth)
	case httpbind.KindList:
		if n.kind != 'a' || len(n.kids) > httpbind.OrInt(s.MaxItems, httpbind.DefaultMaxItems) {
			return nil, errInvalid
		}
		out := make([]any, 0, len(n.kids))
		for _, kid := range n.kids {
			v, err := dec.value(s.Elem, kid, depth+1)
			if err != nil || v == nil {
				return nil, errInvalid
			}
			out = append(out, v)
		}
		return out, nil
	case httpbind.KindMap:
		if n.kind != 'o' || len(n.kids) > httpbind.OrInt(s.MaxItems, httpbind.DefaultMaxItems) {
			return nil, errInvalid
		}
		out := make(map[string]any, len(n.kids))
		for i, kid := range n.kids {
			if len(n.keys[i]) > httpbind.OrInt(s.MaxLen, httpbind.DefaultMaxString) || !utf8.ValidString(n.keys[i]) {
				return nil, errInvalid
			}
			v, err := dec.value(s.Elem, kid, depth+1)
			if err != nil || v == nil {
				return nil, errInvalid
			}
			out[n.keys[i]] = v
		}
		return out, nil
	case httpbind.KindDocument:
		budget := maxNodes
		return dec.document(s, n, depth, &budget)
	}
	return dec.scalar(s, n)
}

func (dec *decoder) structure(s *httpbind.Shape, n *node, depth int) (any, error) {
	out := map[string]any{}
	byName := make(map[string]int, len(n.keys))
	for i, k := range n.keys {
		byName[k] = i
	}
	for _, f := range s.Fields {
		i, ok := byName[f.Name]
		if !ok {
			continue
		}
		v, err := dec.value(f.Shape, n.kids[i], depth+1)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out[f.Key] = v
		}
	}
	if s.Union && len(out) > 1 {
		return nil, errInvalid
	}
	return out, nil
}

func (dec *decoder) scalar(s *httpbind.Shape, n *node) (any, error) {
	want := byte('s')
	switch s.Kind {
	case httpbind.KindInteger, httpbind.KindFloat:
		want = 'n'
	case httpbind.KindBoolean:
		want = 'b'
	case httpbind.KindTimestamp:
		if s.TimeFormat(httpbind.FormatEpochSeconds) == httpbind.FormatEpochSeconds {
			want = 'n'
		}
	}
	if n.kind != want {
		return nil, errInvalid
	}
	v, cut, err := httpbind.ParseText(s, n.text, httpbind.FormatEpochSeconds)
	if err != nil {
		return nil, errInvalid
	}
	dec.cut = dec.cut || cut
	return v, nil
}

// document converts any JSON value into Go values. Strings are capped like any string, collections like
// any collection, numbers stay exact as json.Number, and the nodes of the whole document are bounded.
func (dec *decoder) document(s *httpbind.Shape, n *node, depth int, budget *int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errInvalid
	}
	if *budget--; *budget < 0 {
		return nil, errInvalid
	}
	switch n.kind {
	case 'z':
		return nil, nil
	case 'b':
		return n.text == "true", nil
	case 'n':
		if f, err := json.Number(n.text).Float64(); err != nil || math.IsInf(f, 0) {
			return nil, errInvalid
		}
		return json.Number(n.text), nil
	case 's':
		v, cut, err := httpbind.ParseText(&httpbind.Shape{Kind: httpbind.KindString, MaxLen: s.MaxLen}, n.text, 0)
		dec.cut = dec.cut || cut
		return v, err
	case 'a':
		if len(n.kids) > httpbind.OrInt(s.MaxItems, httpbind.DefaultMaxItems) {
			return nil, errInvalid
		}
		out := make([]any, 0, len(n.kids))
		for _, kid := range n.kids {
			v, err := dec.document(s, kid, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	if len(n.kids) > httpbind.OrInt(s.MaxItems, httpbind.DefaultMaxItems) {
		return nil, errInvalid
	}
	out := make(map[string]any, len(n.kids))
	for i, kid := range n.kids {
		if len(n.keys[i]) > httpbind.OrInt(s.MaxLen, httpbind.DefaultMaxString) {
			return nil, errInvalid
		}
		v, err := dec.document(s, kid, depth+1, budget)
		if err != nil {
			return nil, err
		}
		out[n.keys[i]] = v
	}
	return out, nil
}
