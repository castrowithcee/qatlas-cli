package gen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
)

// RestBinding is one operation of a REST service model, bound to the httpbind runtime.
type RestBinding struct {
	Operation *httpbind.Operation
	// Pager is set when the operation is paginated. Its members are bound under the key "~"+name in Operation,
	// so that they never collide with an argument.
	Pager *httpbind.Pager
	// UnboundedPageSize names the page size member of the paginated trait when the model gives it no range
	// maximum; the Pager then carries no SizeParam.
	UnboundedPageSize string
	// Code is the success status of the http trait. The runtime accepts every 2xx status; the code is kept
	// for tests and for the generated tool's documentation.
	Code int
}

// Traits that change what goes on the wire in a way httpbind does not model. A model that uses one of them
// on an operation or on a member of its input or output stops the binding instead of being sent wrongly.
var unsupportedTraits = []string{
	"smithy.api#streaming", "smithy.api#requiresLength", "smithy.api#mediaType", "smithy.api#httpChecksumRequired",
	"aws.protocols#httpChecksum", "smithy.api#endpoint", "smithy.api#hostLabel", "smithy.api#requestCompression",
	"smithy.api#eventPayload", "smithy.api#eventHeader",
}

// The binding traits of a top-level member, mapped to their place.
var placeTraits = []struct {
	trait string
	loc   httpbind.Loc
}{
	{"smithy.api#httpLabel", httpbind.LocLabel}, {"smithy.api#httpQuery", httpbind.LocQuery},
	{"smithy.api#httpQueryParams", httpbind.LocQueryParams}, {"smithy.api#httpHeader", httpbind.LocHeader},
	{"smithy.api#httpPrefixHeaders", httpbind.LocPrefixHeaders}, {"smithy.api#httpPayload", httpbind.LocPayload},
	{"smithy.api#httpResponseCode", httpbind.LocResponseCode},
}

var timestampFormats = map[string]httpbind.Format{
	"epoch-seconds": httpbind.FormatEpochSeconds, "date-time": httpbind.FormatDateTime, "http-date": httpbind.FormatHTTPDate,
}

// BindRest reads the http trait, the member traits and the paginated trait of an operation of a restJson1
// model and returns the description the runtime encodes and decodes with. It knows no operation names. What
// the model does not bind unambiguously, or binds in a way the runtime does not carry, is an error that
// names the operation and the member; it is never guessed.
func (m *Model) BindRest(name string) (*RestBinding, error) {
	if m.Protocol != ProtocolRestJSON {
		return nil, fmt.Errorf("operation %s: the service does not speak restJson1", name)
	}
	b := &restBinder{m: m, op: name}
	shape, err := m.operation(name)
	if err != nil {
		return nil, b.failf("%v", err)
	}
	traits := shape.obj("traits")
	if err := b.refuseTraits(traits, "the operation"); err != nil {
		return nil, err
	}
	h := traits.obj("smithy.api#http")
	if h == nil || h.str("method") == "" || h.str("uri") == "" {
		return nil, b.failf("the operation has no http trait with a method and a uri")
	}
	code := 200
	if n, ok := number(h, "code"); ok {
		code = int(n)
	}
	if code < 200 || code > 299 {
		return nil, b.failf("the success status %d is not a 2xx status", code)
	}
	plan, err := b.paging(traits)
	if err != nil {
		return nil, err
	}
	b.plan = plan
	in, err := b.top(shape.obj("input").str("target"), true)
	if err != nil {
		return nil, err
	}
	out, err := b.top(shape.obj("output").str("target"), false)
	if err != nil {
		return nil, err
	}
	o := &httpbind.Operation{Method: h.str("method"), URI: h.str("uri"), Input: in, Output: out}
	if err := o.Validate(); err != nil {
		return nil, b.failf("%v", err)
	}
	res := &RestBinding{Operation: o, Code: code}
	if plan != nil {
		if res.Pager, err = b.pager(plan, in, out); err != nil {
			return nil, err
		}
		if plan.sizeMember != "" && res.Pager.SizeParam == "" {
			res.UnboundedPageSize = plan.sizeMember
		}
	}
	return res, nil
}

type restBinder struct {
	m     *Model
	op    string
	plan  *pagingPlan
	stack []string
}

func (b *restBinder) failf(format string, args ...any) error {
	return fmt.Errorf("restJson1 binding of operation %s: %s", b.op, fmt.Sprintf(format, args...))
}

func (b *restBinder) refuseTraits(traits *object, where string) error {
	for _, t := range unsupportedTraits {
		if traits.has(t) {
			return b.failf("%s uses %s, which the runtime does not carry", where, t)
		}
	}
	return nil
}

// paging reads the paginated trait of the operation over the one of the service, member by member.
func (b *restBinder) paging(traits *object) (*pagingPlan, error) {
	trait := traits.obj("smithy.api#paginated")
	if trait == nil {
		return nil, nil
	}
	base := b.m.service.obj("traits").obj("smithy.api#paginated")
	pick := func(k string) string {
		if trait.has(k) {
			return trait.str(k)
		}
		return base.str(k)
	}
	p := &pagingPlan{tokenIn: pick("inputToken"), tokenOut: pick("outputToken"), sizeMember: pick("pageSize")}
	if p.tokenIn == "" || p.tokenOut == "" {
		return nil, b.failf("the paginated trait names no input or output token")
	}
	for _, n := range []string{p.tokenIn, p.tokenOut, p.sizeMember} {
		if strings.Contains(n, ".") {
			return nil, b.failf("paging member %s is nested", n)
		}
	}
	return p, nil
}

// pager builds the Pager once the shapes are bound, and checks the paging members against them.
func (b *restBinder) pager(p *pagingPlan, in, out *httpbind.Shape) (*httpbind.Pager, error) {
	tokenIn := fieldByKey(in, "~"+p.tokenIn)
	tokenOut := fieldByKey(out, "~"+p.tokenOut)
	if tokenIn == nil || tokenIn.Shape.Kind != httpbind.KindString || (tokenIn.Loc != httpbind.LocBody && tokenIn.Loc != httpbind.LocQuery) {
		return nil, b.failf("paging input token %s is not a string member of the body or the query", p.tokenIn)
	}
	if tokenOut == nil || tokenOut.Shape.Kind != httpbind.KindString {
		return nil, b.failf("paging output token %s is not a string member", p.tokenOut)
	}
	pager := &httpbind.Pager{TokenIn: tokenIn.Name, TokenOut: tokenOut.Key}
	if p.sizeMember != "" {
		size := fieldByKey(in, "~"+p.sizeMember)
		if size == nil || size.Shape.Kind != httpbind.KindInteger || (size.Loc != httpbind.LocBody && size.Loc != httpbind.LocQuery) {
			return nil, b.failf("page size %s is not an integer member of the body or the query", p.sizeMember)
		}
		// The Pager refuses a page size above MaxSize, and the model's range maximum is the only bound this
		// layer may use. Without one the member stays bound but the tool offers no page_size: no bound is
		// guessed, and the service package can set one with a curated cap.
		if max, ok := b.pageSizeMax(p.sizeMember); ok {
			pager.SizeParam, pager.MaxSize = size.Name, max
		}
	}
	return pager, nil
}

func (b *restBinder) pageSizeMax(member string) (int, bool) {
	shape, _ := b.m.operation(b.op)
	ins, err := b.members(shape.obj("input").str("target"))
	if err != nil {
		return 0, false
	}
	mm, ok := find(ins, member)
	if !ok {
		return 0, false
	}
	n, ok := number(mm.traits.obj("smithy.api#range"), "max")
	return int(n), ok && n > 0
}

func fieldByKey(s *httpbind.Shape, key string) *httpbind.Field {
	if s == nil {
		return nil
	}
	for i := range s.Fields {
		if s.Fields[i].Key == key {
			return &s.Fields[i]
		}
	}
	return nil
}

// members returns the members of a structure target with their merged traits; Unit or none is empty.
func (b *restBinder) members(target string) ([]member, error) {
	if target == "" || target == "smithy.api#Unit" {
		return nil, nil
	}
	kind, s, err := b.m.resolve(target)
	if err != nil || kind != "structure" {
		return nil, b.failf("%s is not a structure", target)
	}
	ms := s.obj("members")
	var out []member
	for _, name := range ms.keys {
		mo := ms.obj(name)
		k, ts, err := b.m.resolve(mo.str("target"))
		if err != nil {
			return nil, b.failf("member %s: %v", name, err)
		}
		out = append(out, member{name: name, obj: mo, target: mo.str("target"), kind: k, traits: mergedTraits(mo, ts)})
	}
	return out, nil
}

// top binds the top level of an input or an output: each member gets its place from its binding trait, a
// member without one lives in the document body. Unit or no shape is no description at all.
func (b *restBinder) top(target string, input bool) (*httpbind.Shape, error) {
	if target == "" || target == "smithy.api#Unit" {
		return nil, nil
	}
	ms, err := b.members(target)
	if err != nil {
		return nil, err
	}
	s := &httpbind.Shape{Kind: httpbind.KindStruct}
	for _, mm := range ms {
		if err := b.refuseTraits(mm.traits, "member "+mm.name); err != nil {
			return nil, err
		}
		f := httpbind.Field{Key: mm.name, Name: mm.name}
		var places []string
		for _, p := range placeTraits {
			if mm.traits.has(p.trait) {
				places = append(places, p.trait)
				f.Loc = p.loc
			}
		}
		if len(places) > 1 {
			return nil, b.failf("member %s carries more than one binding trait", mm.name)
		}
		switch f.Loc {
		case httpbind.LocQuery:
			f.Name = mm.traits.str("smithy.api#httpQuery")
		case httpbind.LocHeader:
			f.Name = mm.traits.str("smithy.api#httpHeader")
		case httpbind.LocPrefixHeaders:
			f.Name = mm.traits.str("smithy.api#httpPrefixHeaders")
		case httpbind.LocBody:
			if j := mm.traits.str("smithy.api#jsonName"); j != "" {
				f.Name = j
			}
		}
		if f.Name == "" && f.Loc != httpbind.LocPrefixHeaders {
			return nil, b.failf("member %s: the binding trait names no wire name", mm.name)
		}
		if input {
			// A required member the model also gives a default stays required: omitting it would send
			// nothing where the model says a value is always present, and the runtime refuses that call.
			f.Required = mm.traits.has("smithy.api#required")
			f.IdempotencyToken = mm.traits.has("smithy.api#idempotencyToken")
		}
		if f.Shape, err = b.shape(mm.target, mm.traits, 1); err != nil {
			return nil, b.failf("member %s: %v", mm.name, err)
		}
		if b.plan != nil {
			paging := (input && (mm.name == b.plan.tokenIn || (b.plan.sizeMember != "" && mm.name == b.plan.sizeMember))) ||
				(!input && mm.name == b.plan.tokenOut)
			if paging {
				f.Key = "~" + mm.name
				f.Required = false
			}
		}
		s.Fields = append(s.Fields, f)
	}
	return s, nil
}

// shape converts a model shape. Only the top level has binding traits; below it every member is a body
// member, as the Smithy HTTP binding rules say.
func (b *restBinder) shape(target string, traits *object, depth int) (*httpbind.Shape, error) {
	if depth > httpbind.MaxDepth || slices.Contains(b.stack, target) {
		return nil, fmt.Errorf("shape %s is recursive or nested too deep", target)
	}
	b.stack = append(b.stack, target)
	defer func() { b.stack = b.stack[:len(b.stack)-1] }()
	kind, s, err := b.m.resolve(target)
	if err != nil {
		return nil, err
	}
	length := traits.obj("smithy.api#length")
	switch kind {
	case "string", "enum":
		return &httpbind.Shape{Kind: httpbind.KindString, MaxLen: tighter(length, httpbind.DefaultMaxString)}, nil
	case "byte", "short", "integer", "long", "intEnum":
		return &httpbind.Shape{Kind: httpbind.KindInteger}, nil
	case "float", "double":
		return &httpbind.Shape{Kind: httpbind.KindFloat}, nil
	case "boolean":
		return &httpbind.Shape{Kind: httpbind.KindBoolean}, nil
	case "timestamp":
		var f httpbind.Format
		if name := traits.str("smithy.api#timestampFormat"); name != "" {
			var ok bool
			if f, ok = timestampFormats[name]; !ok {
				return nil, fmt.Errorf("timestamp format %q is unknown", name)
			}
		}
		return &httpbind.Shape{Kind: httpbind.KindTimestamp, Format: f}, nil
	case "blob":
		return &httpbind.Shape{Kind: httpbind.KindBlob, MaxLen: tighter(length, httpbind.DefaultMaxString)}, nil
	case "document":
		return &httpbind.Shape{Kind: httpbind.KindDocument}, nil
	case "structure", "union":
		out := &httpbind.Shape{Kind: httpbind.KindStruct, Union: kind == "union"}
		ms := s.obj("members")
		for _, name := range ms.keys {
			mo := ms.obj(name)
			_, ts, err := b.m.resolve(mo.str("target"))
			if err != nil {
				return nil, fmt.Errorf("member %s: %w", name, err)
			}
			merged := mergedTraits(mo, ts)
			if err := b.refuseTraits(merged, "member "+name); err != nil {
				return nil, err
			}
			wire := name
			if j := merged.str("smithy.api#jsonName"); j != "" {
				wire = j
			}
			inner, err := b.shape(mo.str("target"), merged, depth+1)
			if err != nil {
				return nil, fmt.Errorf("member %s: %w", name, err)
			}
			out.Fields = append(out.Fields, httpbind.Field{Key: name, Name: wire, Shape: inner})
		}
		return out, nil
	case "list", "set":
		mo := s.obj("member")
		_, ts, err := b.m.resolve(mo.str("target"))
		if err != nil {
			return nil, err
		}
		elem, err := b.shape(mo.str("target"), mergedTraits(mo, ts), depth+1)
		if err != nil {
			return nil, err
		}
		return &httpbind.Shape{Kind: httpbind.KindList, Elem: elem, MaxItems: tighter(length, httpbind.DefaultMaxItems)}, nil
	case "map":
		ko, vo := s.obj("key"), s.obj("value")
		if kk, _, err := b.m.resolve(ko.str("target")); err != nil || (kk != "string" && kk != "enum") {
			return nil, fmt.Errorf("a map key is not a string")
		}
		_, vs, err := b.m.resolve(vo.str("target"))
		if err != nil {
			return nil, err
		}
		elem, err := b.shape(vo.str("target"), mergedTraits(vo, vs), depth+1)
		if err != nil {
			return nil, err
		}
		return &httpbind.Shape{Kind: httpbind.KindMap, Elem: elem, MaxItems: tighter(length, httpbind.DefaultMaxItems)}, nil
	}
	return nil, fmt.Errorf("shape type %s has no restJson1 binding here", kind)
}

// tighter returns the model's length maximum when it is below the runtime default, and zero (the default)
// otherwise: the model may lower a cap, never raise it.
func tighter(length *object, def int) int {
	if max, ok := number(length, "max"); ok && max > 0 && max < int64(def) {
		return int(max)
	}
	return 0
}
