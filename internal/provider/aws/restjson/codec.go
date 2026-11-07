// Package restjson implements the JSON body and the error shape of the AWS restJson1 protocol. Everything
// that is HTTP, the method and URI pattern, labels, query, headers, payload and the exchange itself, lives
// in package httpbind; this package is its Codec for JSON. An operation is bound by data: an
// httpbind.Operation whose Fields carry the member names of the model or their jsonName.
//
// Body rules of the specification that this codec follows: members are written under their wire name;
// timestamps are epoch-seconds numbers unless the shape names a format; blobs are base64 strings; unions
// are objects with exactly one member; the document type is any JSON value; a null member is absent.
// Errors come from the X-Amzn-Errortype header or the __type member, cleaned of namespace and suffix, and
// the message member is never read.
package restjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awserr"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
)

// ContentType is the media type of a JSON body. Accept is what every request asks for.
const (
	ContentType = "application/json"
	Accept      = "application/json"
)

// maxNodes bounds the values of one document before any is bound, maxJSONDepth their nesting. The size
// limit of the answer bounds the bytes; these bound the work and the memory of the tree.
const (
	maxNodes     = 100000
	maxJSONDepth = 32
	// maxErrorType bounds the length of an error type before it is cleaned.
	maxErrorType = 256
)

// Codec is the restJson1 body format.
type Codec struct{}

// ContentType implements httpbind.Codec.
func (Codec) ContentType() string { return ContentType }

// Accept implements httpbind.Codec.
func (Codec) Accept() string { return Accept }

// Marshal implements httpbind.Codec. v is a map[string]any for a structure, any JSON value for a document.
// A key the shape does not know, a value of the wrong type and a union without exactly one member are
// errors that name the member, never the value.
func (Codec) Marshal(s *httpbind.Shape, v any) ([]byte, error) {
	e := &encoder{budget: maxNodes}
	if err := e.value(s, v, "body", 0); err != nil {
		return nil, err
	}
	return e.buf.Bytes(), nil
}

type encoder struct {
	buf    bytes.Buffer
	budget int
}

func bad(member string) error {
	return fmt.Errorf("%w: %s has the wrong type or form", httpbind.ErrValue, member)
}

func (e *encoder) quote(s string) error {
	enc := json.NewEncoder(&e.buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	// Encode ends the value with a newline.
	e.buf.Truncate(e.buf.Len() - 1)
	return nil
}

func (e *encoder) value(s *httpbind.Shape, v any, member string, depth int) error {
	if depth > maxJSONDepth {
		return httpbind.ErrShape
	}
	if e.budget--; e.budget < 0 {
		return fmt.Errorf("%w: %s is too large", httpbind.ErrValue, member)
	}
	switch s.Kind {
	case httpbind.KindStruct:
		m, ok := v.(map[string]any)
		if !ok {
			return bad(member)
		}
		return e.structure(s, m, member, depth)
	case httpbind.KindList:
		items, ok := v.([]any)
		if !ok {
			return bad(member)
		}
		e.buf.WriteByte('[')
		for i, item := range items {
			if item == nil {
				return bad(member)
			}
			if i > 0 {
				e.buf.WriteByte(',')
			}
			if err := e.value(s.Elem, item, member, depth+1); err != nil {
				return err
			}
		}
		e.buf.WriteByte(']')
		return nil
	case httpbind.KindMap:
		m, ok := v.(map[string]any)
		if !ok {
			return bad(member)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		e.buf.WriteByte('{')
		for i, k := range keys {
			if m[k] == nil {
				return bad(member)
			}
			if i > 0 {
				e.buf.WriteByte(',')
			}
			if err := e.quote(k); err != nil {
				return bad(member)
			}
			e.buf.WriteByte(':')
			if err := e.value(s.Elem, m[k], member, depth+1); err != nil {
				return err
			}
		}
		e.buf.WriteByte('}')
		return nil
	case httpbind.KindDocument:
		return e.document(v, member, depth)
	}
	text, ok := httpbind.Text(s, v, httpbind.FormatEpochSeconds)
	if !ok {
		return bad(member)
	}
	// Numbers, booleans and epoch-seconds are JSON literals; everything else is a string.
	switch {
	case s.Kind == httpbind.KindInteger || s.Kind == httpbind.KindFloat || s.Kind == httpbind.KindBoolean,
		s.Kind == httpbind.KindTimestamp && s.TimeFormat(httpbind.FormatEpochSeconds) == httpbind.FormatEpochSeconds:
		e.buf.WriteString(text)
		return nil
	}
	if err := e.quote(text); err != nil {
		return bad(member)
	}
	return nil
}

func (e *encoder) structure(s *httpbind.Shape, m map[string]any, member string, depth int) error {
	known := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		known[f.Key] = true
	}
	for k := range m {
		if !known[k] {
			return fmt.Errorf("%w: unknown member %q in %s", httpbind.ErrValue, k, member)
		}
	}
	set := 0
	for _, f := range s.Fields {
		if v, ok := m[f.Key]; ok && v != nil {
			set++
		} else if f.Required {
			return fmt.Errorf("%w: %s is required in %s", httpbind.ErrValue, f.Key, member)
		}
	}
	if s.Union && set != 1 {
		return fmt.Errorf("%w: %s must set exactly one member", httpbind.ErrValue, member)
	}
	e.buf.WriteByte('{')
	first := true
	for _, f := range s.Fields {
		v, ok := m[f.Key]
		if !ok || v == nil {
			continue
		}
		if !first {
			e.buf.WriteByte(',')
		}
		first = false
		if err := e.quote(f.Name); err != nil {
			return bad(f.Key)
		}
		e.buf.WriteByte(':')
		if err := e.value(f.Shape, v, f.Key, depth+1); err != nil {
			return err
		}
	}
	e.buf.WriteByte('}')
	return nil
}

// document writes an arbitrary JSON value of the caller: null, booleans, numbers, strings, lists and
// objects with sorted keys. It is bounded by the node budget and the nesting limit; other Go types are
// refused instead of being serialized by reflection.
func (e *encoder) document(v any, member string, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("%w: %s is nested too deep", httpbind.ErrValue, member)
	}
	if e.budget--; e.budget < 0 {
		return fmt.Errorf("%w: %s is too large", httpbind.ErrValue, member)
	}
	switch x := v.(type) {
	case nil:
		e.buf.WriteString("null")
	case bool:
		e.buf.WriteString(strconv.FormatBool(x))
	case string:
		return e.quote(x)
	case int:
		e.buf.WriteString(strconv.Itoa(x))
	case int64:
		e.buf.WriteString(strconv.FormatInt(x, 10))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return bad(member)
		}
		e.buf.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
	case json.Number:
		if f, err := strconv.ParseFloat(x.String(), 64); err != nil || math.IsInf(f, 0) || !json.Valid([]byte(x)) {
			return bad(member)
		}
		e.buf.WriteString(x.String())
	case []any:
		e.buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				e.buf.WriteByte(',')
			}
			if err := e.document(item, member, depth+1); err != nil {
				return err
			}
		}
		e.buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		e.buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				e.buf.WriteByte(',')
			}
			if err := e.quote(k); err != nil {
				return bad(member)
			}
			e.buf.WriteByte(':')
			if err := e.document(x[k], member, depth+1); err != nil {
				return err
			}
		}
		e.buf.WriteByte('}')
	default:
		return bad(member)
	}
	return nil
}

// ErrorCode implements httpbind.Codec: the X-Amzn-Errortype header wins, the __type member of the body is
// the fallback. Both are cleaned by awserr.NormalizeCode (namespace prefix and a ":..." suffix). A body
// that is not a JSON object, or whose __type is not a string, gives no code, and the status decides alone.
func (Codec) ErrorCode(header http.Header, body []byte) string {
	if raw := header.Get("X-Amzn-Errortype"); raw != "" && len(raw) <= maxErrorType {
		if code := awserr.NormalizeCode(raw); code != "" {
			return code
		}
	}
	root, err := parse(body)
	if err != nil || root.kind != 'o' {
		return ""
	}
	for i, k := range root.keys {
		if k == "__type" && root.kids[i].kind == 's' && len(root.kids[i].text) <= maxErrorType {
			return awserr.NormalizeCode(root.kids[i].text)
		}
	}
	return ""
}
