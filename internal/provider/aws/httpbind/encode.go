package httpbind

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/idempotency"
)

// Bounds of one request. The AWS query protocol sends everything in the body; the REST protocols spread it
// over the URI and the headers, so those are bounded as well.
const (
	MaxBodySize     = 1 << 20
	MaxURILength    = 8192
	MaxHeaderBytes  = 16 << 10
	headerCountCap  = 200
	maxPrefixedKeys = 100
)

// Methods of the http trait.
var methods = map[string]bool{
	http.MethodGet: true, http.MethodPut: true, http.MethodPost: true, http.MethodDelete: true,
	http.MethodPatch: true, http.MethodHead: true,
}

func valueErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValue, fmt.Sprintf(format, args...))
}

// Operation is one operation as data: the http trait and the two shapes. Input and Output may be nil for an
// operation without them.
type Operation struct {
	// Method and URI are the method and the uri of the http trait, for example GET and
	// /v2/email/identities/{EmailIdentity}.
	Method, URI   string
	Input, Output *Shape

	// once guards the single check of the description: generated tools share one Operation and call it
	// concurrently. The description must not change after its first use.
	once   sync.Once
	parsed *prepared
	err    error
}

type prepared struct {
	pattern pattern
	in, out topLevel
}

// Validate checks the description once, so a generator or a test rejects a faulty one instead of a call.
// It names the rule that failed (wrapping ErrShape). GET, HEAD and DELETE carry no body in the protocols,
// so an input of one of them with a body or payload member is refused rather than sent with a body the
// service may drop, and a HEAD answer has no body to decode.
func (o *Operation) Validate() error {
	_, err := o.prepare()
	return err
}

func (o *Operation) check() (*prepared, error) {
	if o == nil || !methods[o.Method] {
		return nil, shapeErr("the method is not one of the http trait methods")
	}
	p, err := parseURI(o.URI)
	if err != nil {
		return nil, err
	}
	in, err := classify(o.Input, true)
	if err != nil {
		return nil, err
	}
	out, err := classify(o.Output, false)
	if err != nil {
		return nil, err
	}
	bodyless := o.Method == http.MethodGet || o.Method == http.MethodHead || o.Method == http.MethodDelete
	if bodyless && (len(in.body) > 0 || in.payload != nil) {
		return nil, shapeErr("a %s input cannot carry a body", o.Method)
	}
	if o.Method == http.MethodHead && (len(out.body) > 0 || out.payload != nil) {
		return nil, shapeErr("a HEAD answer has no body")
	}
	// The labels of the pattern and the label members must be the same set.
	want := p.labels()
	if len(want) != len(in.labels) {
		return nil, shapeErr("the labels of the uri pattern and the label members differ")
	}
	for name := range want {
		if _, ok := in.labels[name]; !ok {
			return nil, shapeErr("a label of the uri pattern has no member")
		}
	}
	// A literal query pair of the pattern and a query member must not share a name.
	for _, q := range p.query {
		for _, f := range in.byLoc[LocQuery] {
			if f.Name == q[0] {
				return nil, shapeErr("a query member repeats a literal query parameter of the uri pattern")
			}
		}
	}
	return &prepared{pattern: p, in: in, out: out}, nil
}

func (o *Operation) prepare() (*prepared, error) {
	if o == nil {
		return nil, shapeErr("no operation")
	}
	o.once.Do(func() { o.parsed, o.err = o.check() })
	return o.parsed, o.err
}

// Request is an encoded input. The caller joins it with the fixed endpoint: Path and RawPath go into a URL
// (Path decoded, RawPath the escaped wire form that the signature covers), never a host.
type Request struct {
	Method  string
	Path    string
	RawPath string
	// RawQuery is the encoded query: literal pairs of the pattern first, then the members by name.
	RawQuery string
	Header   http.Header
	Body     []byte
	// Token is the idempotency token sent, empty when the input has none. Generated says the tool made it.
	Token     string
	Generated bool
}

// Encode binds args (the values of a JSON object, see Text) to the URI, the query, the headers and the body
// by the traits of the description. A key the shape does not know, a value of the wrong type, a missing
// required member, a collection without a defined form and a label that is empty or would change the path
// are errors that name the member and never the value. rnd feeds a generated idempotency token and is
// crypto/rand when nil.
func (o *Operation) Encode(c Codec, args map[string]any, rnd io.Reader) (Request, error) {
	prep, err := o.prepare()
	if err != nil {
		return Request{}, err
	}
	var fields []Field
	if o.Input != nil {
		fields = o.Input.Fields
	}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true
	}
	for k := range args {
		if !known[k] {
			return Request{}, valueErr("unknown argument %q", k)
		}
	}
	vals := make(map[string]any, len(args)+1)
	for k, v := range args {
		if v != nil {
			vals[k] = v
		}
	}
	req := Request{Method: o.Method, Header: http.Header{}}
	for _, f := range fields {
		if !f.IdempotencyToken {
			continue
		}
		var provided string
		if raw := vals[f.Key]; raw != nil {
			text, ok := raw.(string)
			if !ok {
				return Request{}, valueErr("%s must be a string", f.Key)
			}
			provided = text
		}
		token, generated, err := idempotency.Resolve(provided, rnd)
		if err != nil {
			return Request{}, err
		}
		vals[f.Key] = token
		req.Token, req.Generated = token, generated
	}
	for _, f := range fields {
		if _, set := vals[f.Key]; f.Required && !set {
			return Request{}, valueErr("%s is required", f.Key)
		}
	}

	labels := map[string]string{}
	for _, f := range prep.in.labels {
		v, set := vals[f.Key]
		if !set {
			return Request{}, valueErr("%s is required", f.Key)
		}
		text, ok := Text(f.Shape, v, FormatDateTime)
		if !ok {
			return Request{}, valueErr("%s has the wrong type or form", f.Key)
		}
		var s seg
		for _, candidate := range prep.pattern.segs {
			if candidate.label == f.Name {
				s = candidate
			}
		}
		if labels[f.Name], err = labelValue(s, text, f.Key); err != nil {
			return Request{}, err
		}
	}
	if req.RawPath, req.Path, err = prep.pattern.build(labels); err != nil {
		return Request{}, valueErr("the path could not be built")
	}

	if req.RawQuery, err = prep.query(vals); err != nil {
		return Request{}, err
	}
	if len(req.RawPath)+len(req.RawQuery) > MaxURILength {
		return Request{}, valueErr("the request URI exceeds the size limit")
	}
	if err := prep.headers(vals, req.Header); err != nil {
		return Request{}, err
	}
	if req.Body, err = prep.body(c, vals, req.Header); err != nil {
		return Request{}, err
	}
	return req, nil
}

// query builds the query string: literal pairs of the pattern, then members sorted by name, a list repeated
// in order. A name set twice (a query member and a query-params key, or a literal) is an error: which of the
// two the caller meant would be a guess.
func (p *prepared) query(vals map[string]any) (string, error) {
	type pair struct{ name, value string }
	var pairs []pair
	taken := map[string]bool{}
	for _, q := range p.pattern.query {
		pairs = append(pairs, pair{q[0], q[1]})
		taken[q[0]] = true
	}
	var members []pair
	for _, f := range p.in.byLoc[LocQuery] {
		v, set := vals[f.Key]
		if !set {
			continue
		}
		texts, err := textList(f, v, FormatDateTime)
		if err != nil {
			return "", err
		}
		taken[f.Name] = true
		for _, text := range texts {
			members = append(members, pair{f.Name, text})
		}
	}
	for _, f := range p.in.byLoc[LocQueryParams] {
		v, set := vals[f.Key]
		if !set {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return "", valueErr("%s has the wrong type or form", f.Key)
		}
		if len(m) > DefaultMaxItems {
			return "", valueErr("%s has too many entries", f.Key)
		}
		for name, raw := range m {
			if !validName(name) || taken[name] {
				return "", valueErr("%s has a malformed or repeated query name", f.Key)
			}
			elem := f.Shape.Elem
			list, isList := raw.([]any)
			var texts []string
			if isList && elem.Kind == KindList {
				for _, item := range list {
					text, ok := Text(elem.Elem, item, FormatDateTime)
					if !ok {
						return "", valueErr("%s has the wrong type or form", f.Key)
					}
					texts = append(texts, text)
				}
			} else if text, ok := Text(elem, raw, FormatDateTime); ok && !isList && elem.Kind == KindString {
				texts = []string{text}
			} else {
				return "", valueErr("%s has the wrong type or form", f.Key)
			}
			for _, text := range texts {
				members = append(members, pair{name, text})
			}
		}
	}
	// A stable sort keeps the order of a list while the names come out sorted.
	sort.SliceStable(members, func(i, j int) bool { return members[i].name < members[j].name })
	pairs = append(pairs, members...)
	var b strings.Builder
	for i, q := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(Escape(q.name))
		// A literal key without a value stays without it.
		if q.value != "" || !isLiteral(p.pattern, q.name) {
			b.WriteString("=" + Escape(q.value))
		}
	}
	return b.String(), nil
}

func isLiteral(p pattern, name string) bool {
	for _, q := range p.query {
		if q[0] == name {
			return true
		}
	}
	return false
}

// textList renders a scalar or a list of scalars as the texts of repeated values.
func textList(f Field, v any, def Format) ([]string, error) {
	bad := func() ([]string, error) { return nil, valueErr("%s has the wrong type or form", f.Key) }
	if f.Shape.Kind != KindList {
		text, ok := Text(f.Shape, v, def)
		if !ok {
			return bad()
		}
		return []string{text}, nil
	}
	items, ok := v.([]any)
	if !ok || len(items) > DefaultMaxItems {
		return bad()
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := Text(f.Shape.Elem, item, def)
		if !ok {
			return bad()
		}
		out = append(out, text)
	}
	return out, nil
}

// validHeaderValue accepts printable ASCII only. A control character would split the header and anything
// else has no single meaning across the services.
func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// headers sets the header members and the prefix headers. A list is one header with comma-separated values;
// a value with a comma, a quote or an edge space is quoted, as the specification says for list headers.
func (p *prepared) headers(vals map[string]any, h http.Header) error {
	total := 0
	set := func(name, value, member string) error {
		if !validHeaderValue(value) {
			return valueErr("%s is not printable ASCII", member)
		}
		if _, dup := h[http.CanonicalHeaderKey(name)]; dup || len(h) >= headerCountCap {
			return valueErr("%s repeats a header or there are too many headers", member)
		}
		if total += len(name) + len(value); total > MaxHeaderBytes {
			return valueErr("the headers exceed the size limit")
		}
		h.Set(name, value)
		return nil
	}
	for _, f := range p.in.byLoc[LocHeader] {
		v, ok := vals[f.Key]
		if !ok {
			continue
		}
		texts, err := textList(f, v, FormatHTTPDate)
		if err != nil {
			return err
		}
		if len(texts) == 0 {
			continue
		}
		for i, text := range texts {
			if f.Shape.Kind == KindList {
				texts[i] = quoteListValue(text)
			}
		}
		if err := set(f.Name, strings.Join(texts, ","), f.Key); err != nil {
			return err
		}
	}
	for _, f := range p.in.byLoc[LocPrefixHeaders] {
		v, ok := vals[f.Key]
		if !ok {
			continue
		}
		m, isMap := v.(map[string]any)
		if !isMap || len(m) > maxPrefixedKeys {
			return valueErr("%s has the wrong type or too many entries", f.Key)
		}
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			name := f.Name + k
			text, ok := m[k].(string)
			// The whole name is checked, so a key cannot reach a reserved header through the prefix.
			if !ok || k == "" || !headerToken.MatchString(name) || reservedHeaders[strings.ToLower(name)] || !strings.HasPrefix(name, f.Name) {
				return valueErr("%s has a malformed key or a non-string value", f.Key)
			}
			if err := set(name, text, f.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

func quoteListValue(s string) string {
	if !strings.ContainsAny(s, `,"`) && s == strings.TrimSpace(s) {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// body encodes the payload member or the document members. The Content-Type goes with a body only.
func (p *prepared) body(c Codec, vals map[string]any, h http.Header) ([]byte, error) {
	var data []byte
	var contentType string
	switch {
	case p.in.payload != nil:
		f := *p.in.payload
		v, ok := vals[f.Key]
		if !ok {
			return nil, nil
		}
		switch f.Shape.Kind {
		case KindString:
			text, ok := v.(string)
			if !ok || !utf8.ValidString(text) {
				return nil, valueErr("%s has the wrong type or form", f.Key)
			}
			data, contentType = []byte(text), "text/plain"
		case KindBlob:
			text, ok := v.(string)
			if !ok {
				return nil, valueErr("%s has the wrong type or form", f.Key)
			}
			raw, err := base64.StdEncoding.DecodeString(text)
			if err != nil {
				return nil, valueErr("%s is not base64", f.Key)
			}
			data, contentType = raw, "application/octet-stream"
		default:
			var err error
			if data, err = c.Marshal(f.Shape, v); err != nil {
				return nil, err
			}
			contentType = c.ContentType()
		}
	case len(p.in.body) > 0:
		members := map[string]any{}
		for _, f := range p.in.body {
			if v, ok := vals[f.Key]; ok {
				members[f.Key] = v
			}
		}
		// An input with document members always sends a document, an empty one when nothing is set.
		var err error
		if data, err = c.Marshal(p.in.bodyShape(), members); err != nil {
			return nil, err
		}
		contentType = c.ContentType()
	}
	if len(data) > MaxBodySize {
		return nil, valueErr("the request body exceeds the size limit")
	}
	if len(data) > 0 {
		h.Set("Content-Type", contentType)
	}
	return data, nil
}
