package awsquery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/idempotency"
)

// maxParams bounds the number of form keys of one request before the body limit would.
const maxParams = 20000

// Request is an encoded input: the form parameters and the idempotency token that was sent.
type Request struct {
	// Params are the flattened form keys for Call.Params.
	Params map[string]string
	// Token is the idempotency token sent, empty when the input has no idempotencyToken member. Generated
	// says the tool made it; a token the caller gave is sent unchanged and not marked.
	Token     string
	Generated bool
}

// Encode serializes args by the rules of the query protocol: structure members as Name, lists as
// Name.member.N, maps as Name.entry.N.key and Name.entry.N.value, the xmlName and xmlFlattened traits
// through the Shape. args are the values of a JSON object, so structures and maps are map[string]any, lists
// are []any, numbers are json.Number or any Go number, and timestamps are RFC 3339 strings. A key the Shape
// does not know, a value of the wrong type and a collection that has no defined form are errors, never a
// guess. A nil value is not serialized. rnd feeds a generated idempotency token and is crypto/rand when nil.
func Encode(input *Shape, args map[string]any, rnd io.Reader) (Request, error) {
	if input == nil || input.Kind != KindStruct || input.validate(0, true) != nil {
		return Request{}, ErrShape
	}
	e := encoder{params: map[string]string{}}
	req := Request{Params: e.params}
	known := map[string]bool{}
	for _, f := range input.Fields {
		known[f.Key] = true
		if !f.IdempotencyToken {
			continue
		}
		var provided string
		if raw := args[f.Key]; raw != nil {
			text, isString := raw.(string)
			if !isString {
				return Request{}, fmt.Errorf("%w: %s must be a string", ErrValue, f.Key)
			}
			provided = text
		}
		token, generated, err := idempotency.Resolve(provided, rnd)
		if err != nil {
			return Request{}, err
		}
		if req.Token != "" {
			return Request{}, ErrShape
		}
		e.params[f.Name] = token
		req.Token, req.Generated = token, generated
	}
	for key := range args {
		if !known[key] {
			return Request{}, fmt.Errorf("%w: unknown argument %q", ErrValue, key)
		}
	}
	if err := e.fields(input, "", args); err != nil {
		return Request{}, err
	}
	return req, nil
}

// ErrValue reports a value that does not fit its Shape. It names the member, never the value.
var ErrValue = errors.New("awsquery: invalid value")

type encoder struct{ params map[string]string }

func (e *encoder) put(key, value string) error {
	if _, dup := e.params[key]; dup || len(e.params) >= maxParams {
		return fmt.Errorf("%w: key collision or too many parameters at %s", ErrValue, key)
	}
	e.params[key] = value
	return nil
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func (e *encoder) fields(s *Shape, prefix string, values map[string]any) error {
	for _, f := range s.Fields {
		v, set := values[f.Key]
		if !set || v == nil || f.IdempotencyToken {
			continue
		}
		if err := e.value(f.Shape, join(prefix, f.Name), f.Flattened, f.Key, v, 0); err != nil {
			return err
		}
	}
	return nil
}

func (e *encoder) value(s *Shape, key string, flat bool, member string, v any, depth int) error {
	if depth > maxDepth {
		return ErrShape
	}
	bad := func() error { return fmt.Errorf("%w: %s has the wrong type or form", ErrValue, member) }
	switch s.Kind {
	case KindStruct:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		known := map[string]bool{}
		for _, f := range s.Fields {
			known[f.Key] = true
		}
		for k := range m {
			if !known[k] {
				return fmt.Errorf("%w: unknown member %q in %s", ErrValue, k, member)
			}
		}
		for _, f := range s.Fields {
			if fv, set := m[f.Key]; set && fv != nil {
				if err := e.value(f.Shape, join(key, f.Name), f.Flattened, f.Key, fv, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	case KindList:
		items, ok := v.([]any)
		if !ok {
			return bad()
		}
		if len(items) == 0 {
			// The protocol tests define ListArg= for an empty list, but nothing for a flattened one.
			if flat {
				return fmt.Errorf("%w: %s is an empty flattened list; leave it out instead", ErrValue, member)
			}
			return e.put(key, "")
		}
		for i, item := range items {
			name := key + "." + strconv.Itoa(i+1)
			if !flat {
				name = key + "." + s.elemName() + "." + strconv.Itoa(i+1)
			}
			if item == nil {
				return bad()
			}
			if err := e.value(s.Elem, name, false, member, item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case KindMap:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		// An empty map is not serialized at all.
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			entry := key + "." + strconv.Itoa(i+1)
			if !flat {
				entry = key + ".entry." + strconv.Itoa(i+1)
			}
			if m[k] == nil {
				return bad()
			}
			if err := e.put(entry+"."+s.keyName(), k); err != nil {
				return err
			}
			if err := e.value(s.Elem, entry+"."+s.valueName(), false, member, m[k], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	text, ok := scalar(s.Kind, v)
	if !ok {
		return bad()
	}
	return e.put(key, text)
}

// scalar renders a simple value the way the protocol writes it.
func scalar(kind Kind, v any) (string, bool) {
	switch kind {
	case KindString:
		s, ok := v.(string)
		return s, ok
	case KindBoolean:
		b, ok := v.(bool)
		return strconv.FormatBool(b), ok
	case KindInteger:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n), true
		case int64:
			return strconv.FormatInt(n, 10), true
		case float64:
			if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
				return "", false
			}
			return strconv.FormatInt(int64(n), 10), true
		case json.Number:
			i, err := strconv.ParseInt(n.String(), 10, 64)
			return strconv.FormatInt(i, 10), err == nil
		}
	case KindFloat:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n), true
		case int64:
			return strconv.FormatInt(n, 10), true
		case float64:
			return formatFloat(n)
		case json.Number:
			f, err := strconv.ParseFloat(n.String(), 64)
			if err != nil {
				return "", false
			}
			return formatFloat(f)
		}
	case KindTimestamp:
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return "", false
		}
		return t.UTC().Format(time.RFC3339Nano), true
	case KindBlob:
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		_, err := base64.StdEncoding.DecodeString(s)
		return s, err == nil
	}
	return "", false
}

// formatFloat refuses NaN and infinities, whose form depends on the service.
func formatFloat(f float64) (string, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", false
	}
	return strconv.FormatFloat(f, 'f', -1, 64), true
}
