package httpbind

import (
	"encoding/base64"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Decode binds an answer to the members of the output shape: headers, prefix headers, the status, the
// payload and the document members. The body goes through c. A member the answer lacks is left out. The
// second result says that a string was cut at its cap. An answer that does not fit the description is
// ErrResponse; nothing is repaired and nothing is guessed.
func (o *Operation) Decode(c Codec, status int, header http.Header, body []byte) (map[string]any, bool, error) {
	prep, err := o.prepare()
	if err != nil {
		return nil, false, err
	}
	out := map[string]any{}
	cut := false
	note := func(truncated bool) { cut = cut || truncated }

	for _, f := range prep.out.byLoc[LocHeader] {
		values, present := header[http.CanonicalHeaderKey(f.Name)]
		if !present {
			continue
		}
		v, truncated, err := headerValue(f, values)
		if err != nil {
			return nil, false, err
		}
		note(truncated)
		out[f.Key] = v
	}
	for _, f := range prep.out.byLoc[LocPrefixHeaders] {
		m, truncated, err := prefixed(f, header)
		if err != nil {
			return nil, false, err
		}
		note(truncated)
		if len(m) > 0 {
			out[f.Key] = m
		}
	}
	for _, f := range prep.out.byLoc[LocResponseCode] {
		out[f.Key] = int64(status)
	}

	empty := len(strings.TrimSpace(string(body))) == 0
	switch {
	case prep.out.payload != nil:
		f := *prep.out.payload
		if len(body) == 0 {
			break
		}
		limit := orInt(f.Shape.MaxLen, DefaultMaxString)
		switch f.Shape.Kind {
		case KindBlob:
			// The cap applies to the text form the caller receives.
			if base64.StdEncoding.EncodedLen(len(body)) > limit {
				return nil, false, ErrResponse
			}
			out[f.Key] = base64.StdEncoding.EncodeToString(body)
		case KindString:
			v, truncated, err := ParseText(&Shape{Kind: KindString, MaxLen: f.Shape.MaxLen}, string(body), FormatDefault)
			if err != nil {
				return nil, false, err
			}
			note(truncated)
			out[f.Key] = v
		default:
			if empty {
				break
			}
			v, truncated, err := c.Unmarshal(f.Shape, body)
			if err != nil {
				return nil, false, ErrResponse
			}
			note(truncated)
			if v != nil {
				out[f.Key] = v
			}
		}
	case len(prep.out.body) > 0 && !empty:
		v, truncated, err := c.Unmarshal(prep.out.bodyShape(), body)
		if err != nil {
			return nil, false, ErrResponse
		}
		note(truncated)
		members, _ := v.(map[string]any)
		for k, m := range members {
			out[k] = m
		}
	}
	return out, cut, nil
}

// splitList splits a header into list values on commas outside quotes, undoing the quoting of
// quoteListValue. Text after a closing quote or a stray quote is malformed. An empty line is an empty list.
func splitList(line string) ([]string, bool) {
	if strings.TrimSpace(line) == "" {
		return nil, true
	}
	var out []string
	i := 0
	skipSpaces := func() {
		for i < len(line) && line[i] == ' ' {
			i++
		}
	}
	for {
		skipSpaces()
		var item string
		if i < len(line) && line[i] == '"' {
			var b strings.Builder
			i++
			closed := false
			for i < len(line) && !closed {
				ch := line[i]
				switch {
				case ch == '\\' && i+1 < len(line):
					b.WriteByte(line[i+1])
					i += 2
				case ch == '"':
					closed = true
					i++
				default:
					b.WriteByte(ch)
					i++
				}
			}
			skipSpaces()
			if !closed || (i < len(line) && line[i] != ',') {
				return nil, false
			}
			item = b.String()
		} else {
			end := len(line)
			if j := strings.IndexByte(line[i:], ','); j >= 0 {
				end = i + j
			}
			if strings.ContainsRune(line[i:end], '"') {
				return nil, false
			}
			item = strings.TrimSpace(line[i:end])
			i = end
		}
		out = append(out, item)
		if i >= len(line) {
			return out, true
		}
		i++ // the comma; a trailing one leaves a last, empty item
	}
}

func headerValue(f Field, values []string) (any, bool, error) {
	if f.Shape.Kind != KindList {
		if len(values) != 1 {
			return nil, false, ErrResponse
		}
		return ParseText(f.Shape, strings.TrimSpace(values[0]), FormatHTTPDate)
	}
	var items []string
	for _, line := range values {
		parts, ok := splitList(line)
		if !ok {
			return nil, false, ErrResponse
		}
		items = append(items, parts...)
	}
	if len(items) > orInt(f.Shape.MaxItems, DefaultMaxItems) {
		return nil, false, ErrResponse
	}
	out := make([]any, 0, len(items))
	cut := false
	for _, item := range items {
		v, truncated, err := ParseText(f.Shape.Elem, item, FormatHTTPDate)
		if err != nil {
			return nil, false, err
		}
		cut = cut || truncated
		out = append(out, v)
	}
	return out, cut, nil
}

// prefixed collects the headers that start with the prefix of f. Keys are lower case, as header names are
// case-insensitive; a header that only is the prefix has no key and is skipped.
func prefixed(f Field, header http.Header) (map[string]any, bool, error) {
	prefix := strings.ToLower(f.Name)
	out := map[string]any{}
	cut := false
	for name, values := range header {
		lower := strings.ToLower(name)
		key, ok := strings.CutPrefix(lower, prefix)
		if !ok || key == "" {
			continue
		}
		if len(out) >= orInt(f.Shape.MaxItems, DefaultMaxItems) || !utf8.ValidString(key) {
			return nil, false, ErrResponse
		}
		v, truncated, err := ParseText(f.Shape.Elem, strings.TrimSpace(strings.Join(values, ", ")), FormatDefault)
		if err != nil {
			return nil, false, err
		}
		cut = cut || truncated
		out[key] = v
	}
	return out, cut, nil
}
