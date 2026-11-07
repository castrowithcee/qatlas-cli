package gen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that keeps the order of its members. The Smithy JSON AST lists the members of a
// structure in model order, and that order becomes the order of arguments and fields, so it must survive
// decoding and re-encoding.
type object struct {
	keys []string
	vals map[string]any
}

func (o *object) get(key string) any {
	if o == nil {
		return nil
	}
	return o.vals[key]
}

func (o *object) obj(key string) *object {
	v, _ := o.get(key).(*object)
	return v
}

func (o *object) str(key string) string {
	v, _ := o.get(key).(string)
	return v
}

func (o *object) has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.vals[key]
	return ok
}

func (o *object) remove(key string) {
	if !o.has(key) {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// decodeOrdered reads one JSON value: objects become *object, arrays []any, numbers json.Number. A repeated
// member name is an error, as keeping either value would be a guess.
func decodeOrdered(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := decodeValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

func decodeValue(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &object{vals: map[string]any{}}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				if _, dup := o.vals[key]; dup {
					return nil, fmt.Errorf("member %q appears twice", key)
				}
				v, err := decodeValue(d)
				if err != nil {
					return nil, err
				}
				o.keys = append(o.keys, key)
				o.vals[key] = v
			}
			_, err := d.Token()
			return o, err
		case '[':
			list := []any{}
			for d.More() {
				v, err := decodeValue(d)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			_, err := d.Token()
			return list, err
		}
		return nil, errors.New("unexpected delimiter")
	default:
		return t, nil
	}
}

// encodeOrdered writes v with two-space indentation, members in their kept order, and a final newline. HTML
// characters are not escaped, so the text stays readable and the output depends on nothing but v.
func encodeOrdered(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeValue(&b, v, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v any, depth int) error {
	indent := func(n int) { b.WriteString(strings.Repeat("  ", n)) }
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			indent(depth + 1)
			writeString(b, k)
			b.WriteString(": ")
			if err := encodeValue(b, t.vals[k], depth+1); err != nil {
				return err
			}
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(depth)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, item := range t {
			indent(depth + 1)
			if err := encodeValue(b, item, depth+1); err != nil {
				return err
			}
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(depth)
		b.WriteByte(']')
	case string:
		writeString(b, t)
	case json.Number:
		b.WriteString(t.String())
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		return fmt.Errorf("cannot encode %T", v)
	}
	return nil
}

func writeString(b *bytes.Buffer, s string) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// rawJSON is written verbatim by encodeCompact; the generator uses it for a marker it later replaces.
type rawJSON string

// newObject builds an object from key, value pairs in order.
func newObject(pairs ...any) *object {
	o := &object{vals: map[string]any{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// set adds or replaces a member; a new member goes last.
func (o *object) set(key string, v any) {
	if !o.has(key) {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// encodeCompact writes v without whitespace, members in order. Numbers may be Go ints.
func encodeCompact(v any) string {
	var b bytes.Buffer
	compact(&b, v)
	return b.String()
}

func compact(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case *object:
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			compact(b, t.vals[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			compact(b, item)
		}
		b.WriteByte(']')
	case []string:
		items := make([]any, len(t))
		for i, s := range t {
			items[i] = s
		}
		compact(b, items)
	case rawJSON:
		b.WriteString(string(t))
	case string:
		writeString(b, t)
	case int:
		fmt.Fprintf(b, "%d", t)
	case int64:
		fmt.Fprintf(b, "%d", t)
	case json.Number:
		b.WriteString(t.String())
	case bool:
		fmt.Fprintf(b, "%t", t)
	case nil:
		b.WriteString("null")
	default:
		panic(fmt.Sprintf("encodeCompact: unsupported %T", v))
	}
}
