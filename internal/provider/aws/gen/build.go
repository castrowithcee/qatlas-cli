package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/idempotency"
)

// Names the generator adds to tools. A model member that would take one of them stops the generation.
const (
	argRegion      = "region"
	argClientToken = "client_token"
	fieldNext      = "next_cursor"
	fieldHasMore   = "has_more"
	fieldTruncated = "truncated"
)

var reservedNames = []string{argRegion, awsquery.ArgPageSize, awsquery.ArgCursor, argClientToken, fieldNext, fieldHasMore, fieldTruncated}

// regionMarker stands for the region schema in a rendered schema; the renderer replaces it with the
// provider's regionSchema constant, so the schema has one source.
const regionMarker = "\x00region\x00"

// Default caps of what an answer may hold when the model sets none.
const (
	defaultMaxString = awsquery.DefaultMaxString
	defaultMaxItems  = awsquery.DefaultMaxItems
)

// accountCheckMembers are the members the account check answers with (STS GetCallerIdentity).
var accountCheckMembers = []string{"UserId", "Account", "Arn"}

// credentialName matches the name of a text output member that holds a credential. Such a member must be
// hidden even when the model does not mark it sensitive.
var credentialName = regexp.MustCompile(`(?i)(secret|secretaccesskey|secretkey|password|privatekey|sessiontoken)$`)

// formSamples are values that pass a form check, for generated tests.
var formSamples = map[string]string{"account-id": "123456789012"}

// tool is one operation of a table, bound to its model.
type tool struct {
	table   *Table
	op      Operation
	id      string
	opName  string
	action  string
	version string
	global  bool

	input, output *awsquery.Shape
	pager         *awsquery.Pager
	hasToken      bool

	inputSchema, outputSchema string
	args                      []descArg
	fields                    []descField
	rules                     []outputRule
	targetArgs, policyArgs    []string

	// Generated test: the arguments, the form parameters they encode to, the answer, and the result keys.
	sampleArgs   string
	sampleForm   map[string]string
	sampleAnswer string
	resultKeys   []string
}

type descArg struct {
	name, description string
	required          bool
	region            bool
}

type descField struct {
	name, description string
	region            bool
}

// outputRule is a check the runtime makes on a top-level result member.
type outputRule struct {
	key, form string
	required  bool
}

// failf names the table, the operation, and the reason.
func (t *tool) failf(format string, args ...any) error {
	return fmt.Errorf("%s %s: %s", t.table.Service, t.op.Operation, fmt.Sprintf(format, args...))
}

// build binds one table operation to the model.
func build(m *Model, table *Table, op Operation) (*tool, error) {
	t := &tool{table: table, op: op, action: op.Operation, version: m.Version, global: table.Geltung == Global,
		id: "aws." + table.Segment + "." + op.Object + "." + op.Action, opName: words(op.Operation)}
	shape, err := m.operation(op.Operation)
	if err != nil {
		return nil, t.failf("%v", err)
	}
	if shape.obj("traits").has("smithy.api#deprecated") {
		return nil, t.failf("the operation is deprecated")
	}
	if op.AccountCheck && (table.Service != "sts" || op.Operation != "GetCallerIdentity") {
		return nil, t.failf("only STS GetCallerIdentity is the account check")
	}
	if op.TargetCheck == "bucket-owner" {
		return nil, t.failf("target_check bucket-owner needs the S3 protocol package, not awsQuery")
	}
	in, err := t.structOf(m, shape.obj("input").str("target"))
	if err != nil {
		return nil, err
	}
	out, err := t.structOf(m, shape.obj("output").str("target"))
	if err != nil {
		return nil, err
	}
	pag, err := t.paging(m, shape, in, out)
	if err != nil {
		return nil, err
	}
	if err := t.bindInput(m, in, pag); err != nil {
		return nil, err
	}
	if err := t.bindOutput(m, out, pag); err != nil {
		return nil, err
	}
	if err := t.makeSamples(m); err != nil {
		return nil, err
	}
	for _, s := range []*awsquery.Shape{t.input, t.output} {
		if err := s.Validate(); err != nil {
			return nil, t.failf("the bound shape is invalid: %v", err)
		}
	}
	return t, nil
}

// words turns GetAccessKeyInfo into "get access key info", the operation name of errors.
func words(name string) string {
	return strings.ReplaceAll(snake(name), "_", " ")
}

// snake turns a model member name into snake_case: AccessKeyId into access_key_id, SAMLAssertion into
// saml_assertion.
func snake(name string) string {
	var b strings.Builder
	r := []rune(name)
	for i, c := range r {
		upper := c >= 'A' && c <= 'Z'
		if upper && i > 0 {
			prev := r[i-1]
			nextLower := i+1 < len(r) && r[i+1] >= 'a' && r[i+1] <= 'z'
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') || (prev >= 'A' && prev <= 'Z' && nextLower) {
				b.WriteByte('_')
			}
		}
		if upper {
			c += 'a' - 'A'
		}
		b.WriteRune(c)
	}
	return b.String()
}

// member is one top-level member of an input or output structure.
type member struct {
	name   string
	obj    *object
	target string
	kind   string
	traits *object
}

// structOf returns the members of an operation's input or output structure; Unit or none is empty.
func (t *tool) structOf(m *Model, target string) ([]member, error) {
	if target == "" || target == "smithy.api#Unit" {
		return nil, nil
	}
	kind, s, err := m.resolve(target)
	if err != nil || kind != "structure" {
		return nil, t.failf("%s is not a structure", target)
	}
	ms := s.obj("members")
	var out []member
	for _, name := range ms.keys {
		mo := ms.obj(name)
		k, ts, err := m.resolve(mo.str("target"))
		if err != nil {
			return nil, t.failf("member %s: %v", name, err)
		}
		out = append(out, member{name: name, obj: mo, target: mo.str("target"), kind: k, traits: mergedTraits(mo, ts)})
	}
	return out, nil
}

func find(ms []member, name string) (member, bool) {
	for _, mm := range ms {
		if mm.name == name {
			return mm, true
		}
	}
	return member{}, false
}

func deprecated(mm member) bool { return mm.traits.has("smithy.api#deprecated") }

// pagingPlan is what paging takes out of the public arguments and fields.
type pagingPlan struct {
	tokenIn, tokenOut, sizeMember, truncated string
}

// paging derives the pager from the paginated trait, or from Marker with IsTruncated without one.
func (t *tool) paging(m *Model, shape *object, in, out []member) (*pagingPlan, error) {
	var p pagingPlan
	trait := shape.obj("traits").obj("smithy.api#paginated")
	if trait != nil {
		base := m.service.obj("traits").obj("smithy.api#paginated")
		pick := func(k string) string {
			if trait.has(k) {
				return trait.str(k)
			}
			return base.str(k)
		}
		p.tokenIn, p.tokenOut, p.sizeMember = pick("inputToken"), pick("outputToken"), pick("pageSize")
		if p.tokenIn == "" || p.tokenOut == "" {
			return nil, t.failf("the paginated trait names no input or output token")
		}
	} else {
		_, inMarker := find(in, "Marker")
		_, outMarker := find(out, "Marker")
		_, truncated := find(out, "IsTruncated")
		if !inMarker || !outMarker || !truncated {
			return nil, nil
		}
		p.tokenIn, p.tokenOut = "Marker", "Marker"
		if _, ok := find(in, "MaxItems"); ok {
			p.sizeMember = "MaxItems"
		}
	}
	for _, name := range []string{p.tokenIn, p.tokenOut, p.sizeMember} {
		if strings.Contains(name, ".") {
			return nil, t.failf("paging member %s is nested", name)
		}
	}
	if mm, ok := find(in, p.tokenIn); !ok || mm.kind != "string" {
		return nil, t.failf("paging input token %s is not a string member", p.tokenIn)
	}
	if mm, ok := find(out, p.tokenOut); !ok || mm.kind != "string" {
		return nil, t.failf("paging output token %s is not a string member", p.tokenOut)
	}
	if mm, ok := find(out, "IsTruncated"); ok {
		if mm.kind != "boolean" {
			return nil, t.failf("IsTruncated is not a boolean")
		}
		p.truncated = "IsTruncated"
	}
	if p.sizeMember != "" {
		mm, ok := find(in, p.sizeMember)
		if !ok || !isInteger(mm.kind) {
			return nil, t.failf("page size %s is not an integer member", p.sizeMember)
		}
		if _, ok := number(mm.traits.obj("smithy.api#range"), "max"); !ok {
			return nil, t.failf("page size %s has no range maximum", p.sizeMember)
		}
	}
	return &p, nil
}

func isInteger(kind string) bool {
	return slices.Contains([]string{"byte", "short", "integer", "long", "intEnum"}, kind)
}

// number reads an integer member of a trait object.
func number(o *object, key string) (int64, bool) {
	n, ok := o.get(key).(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Int64()
	return v, err == nil
}

func hiddenSet(list []Hidden) map[string]bool {
	out := map[string]bool{}
	for _, h := range list {
		out[h.Member] = true
	}
	return out
}

// bindInput builds the input shape, the input schema, and the arguments.
func (t *tool) bindInput(m *Model, in []member, pag *pagingPlan) error {
	hidden := hiddenSet(t.op.HiddenInputs)
	for name := range hidden {
		if _, ok := find(in, name); !ok {
			return t.failf("hidden input %s is not an input member", name)
		}
	}
	shape := &awsquery.Shape{Kind: awsquery.KindStruct}
	props := newObject()
	var required []string
	used := map[string]bool{}
	offered := map[string]bool{}
	for _, mm := range in {
		if deprecated(mm) || hidden[mm.name] {
			continue
		}
		if pag != nil && (mm.name == pag.tokenIn || mm.name == pag.sizeMember) {
			continue
		}
		if mm.traits.has("smithy.api#idempotencyToken") {
			if t.hasToken || mm.kind != "string" || !idempotency.IsParam(mm.name) {
				return t.failf("idempotency token %s is not one known string member", mm.name)
			}
			t.hasToken = true
			shape.Fields = append(shape.Fields, awsquery.Field{Key: argClientToken, Name: wire(mm), Shape: &awsquery.Shape{Kind: awsquery.KindString}, IdempotencyToken: true})
			continue
		}
		a, ok := t.op.Arguments[mm.name]
		if !ok {
			return t.failf("input member %s has no argument entry in the table", mm.name)
		}
		offered[mm.name] = true
		name := orDefault(a.Name, snake(mm.name))
		if slices.Contains(reservedNames, name) || used[name] {
			return t.failf("argument name %s is reserved or used twice", name)
		}
		used[name] = true
		c := converter{m: m, t: t, path: mm.name}
		s, schema, err := c.convert(mm.target, mm.traits, false, 0)
		if err != nil {
			return err
		}
		shape.Fields = append(shape.Fields, awsquery.Field{Key: name, Name: wire(mm), Shape: s, Flattened: mm.traits.has("smithy.api#xmlFlattened")})
		props.set(name, schema)
		isRequired := mm.traits.has("smithy.api#required")
		if isRequired {
			required = append(required, name)
		}
		t.args = append(t.args, descArg{name: name, description: a.Description, required: isRequired})
	}
	for name := range t.op.Arguments {
		if !offered[name] {
			return t.failf("argument entry %s is not an offered input member", name)
		}
	}
	for _, list := range []struct {
		names []string
		out   *[]string
	}{{t.op.TargetArgs, &t.targetArgs}, {t.op.PolicyArgs, &t.policyArgs}} {
		for _, name := range list.names {
			mm, ok := find(in, name)
			if !ok || !offered[name] || mm.kind != "string" {
				return t.failf("checked argument %s is not an offered top-level string member", name)
			}
			*list.out = append(*list.out, orDefault(t.op.Arguments[name].Name, snake(name)))
		}
	}
	if pag != nil {
		if pag.sizeMember != "" {
			mm, _ := find(in, pag.sizeMember)
			max, _ := number(mm.traits.obj("smithy.api#range"), "max")
			props.set(awsquery.ArgPageSize, newObject("type", "integer", "minimum", 1, "maximum", max))
			t.args = append(t.args, descArg{name: awsquery.ArgPageSize, description: fmt.Sprintf(
				"Entries per page, 1 to %d; the service's default when left out", max)})
			t.pager = &awsquery.Pager{SizeParam: wire(mm), MaxSize: int(max)}
		} else {
			t.pager = &awsquery.Pager{}
		}
		tokenIn, _ := find(in, pag.tokenIn)
		t.pager.TokenIn = wire(tokenIn)
		props.set(awsquery.ArgCursor, newObject("type", "string", "maxLength", awsquery.MaxCursorLength))
		t.args = append(t.args, descArg{name: awsquery.ArgCursor, description: "next_cursor of the previous page, to " +
			"continue the list; valid only with the same arguments"})
	}
	if t.hasToken {
		props.set(argClientToken, newObject("type", "string", "minLength", 1, "maxLength", 128, "pattern", `^[\x21-\x7e]{1,128}$`))
		t.args = append(t.args, descArg{name: argClientToken, description: "Idempotency token to send instead of a " +
			"generated one, 1 to 128 printable ASCII characters; pass the client_token of an earlier result to repeat " +
			"that request after a timeout"})
	}
	if !t.global {
		props.set(argRegion, rawJSON(regionMarker))
		t.args = append(t.args, descArg{name: argRegion, region: true})
	}
	schema := newObject("type", "object", "properties", props)
	if len(required) > 0 {
		schema.set("required", required)
	}
	schema.set("additionalProperties", false)
	t.input = shape
	t.inputSchema = encodeCompact(schema)
	return nil
}

func wire(mm member) string {
	if name := mm.obj.obj("traits").str("smithy.api#xmlName"); name != "" {
		return name
	}
	return mm.name
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// bindOutput builds the output shape, the output schema, the fields, and the runtime rules.
func (t *tool) bindOutput(m *Model, out []member, pag *pagingPlan) error {
	hidden := hiddenSet(t.op.HiddenOutputs)
	for name := range hidden {
		if _, ok := find(out, name); !ok {
			return t.failf("hidden output %s is not an output member", name)
		}
	}
	shape := &awsquery.Shape{Kind: awsquery.KindStruct}
	props := newObject()
	required := []string{}
	used := map[string]bool{}
	returned := map[string]bool{}
	if !t.global {
		props.set(argRegion, rawJSON(regionMarker))
		required = append(required, argRegion)
		t.fields = append(t.fields, descField{name: argRegion, region: true})
	}
	for _, mm := range out {
		if deprecated(mm) || hidden[mm.name] {
			continue
		}
		if t.op.AccountCheck && !slices.Contains(accountCheckMembers, mm.name) {
			return t.failf("the account check answers only %v, not %s", accountCheckMembers, mm.name)
		}
		if pag != nil && (mm.name == pag.tokenOut || mm.name == pag.truncated) {
			// Kept in the shape for the pager, never returned.
			c := converter{m: m, t: t, path: mm.name, output: true}
			s, _, err := c.convert(mm.target, mm.traits, true, 0)
			if err != nil {
				return err
			}
			shape.Fields = append(shape.Fields, awsquery.Field{Key: "~" + mm.name, Name: wire(mm), Shape: s})
			continue
		}
		f, ok := t.op.Fields[mm.name]
		if !ok {
			return t.failf("output member %s has no field entry in the table", mm.name)
		}
		returned[mm.name] = true
		name := orDefault(f.Name, snake(mm.name))
		if slices.Contains(reservedNames, name) || used[name] {
			return t.failf("field name %s is reserved or used twice", name)
		}
		used[name] = true
		if f.Form != "" && mm.kind != "string" {
			return t.failf("field %s has a form but is not a string", mm.name)
		}
		if f.MaxLen > 0 && mm.kind != "string" {
			return t.failf("field %s has a cap but is not a string", mm.name)
		}
		c := converter{m: m, t: t, path: mm.name, output: true, maxLen: f.MaxLen}
		s, schema, err := c.convert(mm.target, mm.traits, true, 0)
		if err != nil {
			return err
		}
		if f.Form != "" {
			// A checked value is decoded whole: cut to its cap first, a longer value could pass the check.
			s.MaxLen = defaultMaxString
		}
		shape.Fields = append(shape.Fields, awsquery.Field{Key: name, Name: wire(mm), Shape: s, Flattened: mm.traits.has("smithy.api#xmlFlattened")})
		props.set(name, schema)
		if f.Required {
			required = append(required, name)
		}
		if f.Required || f.Form != "" {
			t.rules = append(t.rules, outputRule{key: name, form: f.Form, required: f.Required})
		}
		t.fields = append(t.fields, descField{name: name, description: f.Description})
	}
	for name := range t.op.Fields {
		if !returned[name] {
			return t.failf("field entry %s is not a returned output member", name)
		}
	}
	if pag != nil {
		for _, f := range shape.Fields {
			switch f.Name {
			case pagingWire(out, pag.tokenOut):
				t.pager.TokenOut = f.Key
			case pagingWire(out, pag.truncated):
				t.pager.Truncated = f.Key
			}
		}
		props.set(fieldNext, newObject("type", "string", "maxLength", awsquery.MaxCursorLength))
		props.set(fieldHasMore, newObject("type", "boolean"))
		required = append(required, fieldHasMore)
		t.fields = append(t.fields,
			descField{name: fieldNext, description: "Cursor of the next page; absent at the end of the list"},
			descField{name: fieldHasMore, description: "True when more entries follow; a full page alone never says so"})
	}
	if t.hasToken {
		props.set(argClientToken, newObject("type", "string", "maxLength", 128))
		required = append(required, argClientToken)
		t.fields = append(t.fields, descField{name: argClientToken, description: "Idempotency token the request " +
			"was sent with; pass it as client_token to repeat this request after a timeout"})
	}
	if t.op.Truncated != "" {
		props.set(fieldTruncated, newObject("type", "boolean"))
		required = append(required, fieldTruncated)
		t.fields = append(t.fields, descField{name: fieldTruncated, description: t.op.Truncated})
	}
	schema := newObject("type", "object", "properties", props, "required", required, "additionalProperties", false)
	t.resultKeys = required
	t.output = shape
	t.outputSchema = encodeCompact(schema)
	return nil
}

func pagingWire(out []member, name string) string {
	if name == "" {
		return "\x00"
	}
	mm, _ := find(out, name)
	return wire(mm)
}

// converter turns one model shape into a runtime shape and a JSON schema.
type converter struct {
	m      *Model
	t      *tool
	path   string
	output bool
	// maxLen is the table cap of a top-level output string.
	maxLen int
	stack  []string
}

func (c *converter) failf(format string, args ...any) error {
	return c.t.failf("%s: %s", c.path, fmt.Sprintf(format, args...))
}

// pattern translates a model pattern into the RE2 syntax of the schema validator. Smithy patterns are
// ECMA-262 expressions, whose \uXXXX is one UTF-16 code unit and which RE2 writes \x{XXXX}; that is exact
// outside the surrogate range, so a surrogate escape, and any pattern RE2 then refuses, stops the generation
// instead of being guessed at.
func (c *converter) pattern(p string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '\\' || i+1 >= len(p) {
			b.WriteByte(p[i])
			continue
		}
		if p[i+1] == 'u' {
			code, err := strconv.ParseUint(p[min(i+2, len(p)):min(i+6, len(p))], 16, 32)
			if err != nil || i+6 > len(p) || (code >= 0xD800 && code <= 0xDFFF) {
				return "", c.failf("pattern uses a \\u escape RE2 cannot express")
			}
			b.WriteString(`\x{` + p[i+2:i+6] + `}`)
			i += 5
			continue
		}
		b.WriteString(p[i : i+2])
		i++
	}
	out := b.String()
	if _, err := regexp.Compile(out); err != nil {
		return "", c.failf("pattern is not a valid RE2 expression")
	}
	return out, nil
}

func (c *converter) convert(target string, traits *object, output bool, depth int) (*awsquery.Shape, *object, error) {
	if depth > 10 || slices.Contains(c.stack, target) {
		return nil, nil, c.failf("shape %s is recursive or nested too deep", target)
	}
	c.stack = append(c.stack, target)
	defer func() { c.stack = c.stack[:len(c.stack)-1] }()
	kind, s, err := c.m.resolve(target)
	if err != nil {
		return nil, nil, c.failf("%v", err)
	}
	if traits.has("smithy.api#sensitive") {
		return nil, nil, c.failf("the member is sensitive; hide it with a reason, or exclude the operation")
	}
	if output && (kind == "string" || kind == "blob") && credentialName.MatchString(lastSegment(c.path)) {
		return nil, nil, c.failf("the member looks like a credential; hide it with a reason, or exclude the operation")
	}
	length := traits.obj("smithy.api#length")
	switch kind {
	case "string", "enum":
		schema := newObject("type", "string")
		shape := &awsquery.Shape{Kind: awsquery.KindString}
		if output {
			limit := c.maxLen
			if limit == 0 {
				if max, ok := number(length, "max"); ok {
					limit = int(max)
				} else {
					limit = defaultMaxString
				}
			}
			shape.MaxLen = limit
			schema.set("maxLength", limit)
			c.maxLen = 0
			return shape, schema, nil
		}
		if min, ok := number(length, "min"); ok {
			schema.set("minLength", min)
		}
		if max, ok := number(length, "max"); ok {
			schema.set("maxLength", max)
		}
		if p := traits.str("smithy.api#pattern"); p != "" {
			p, err := c.pattern(p)
			if err != nil {
				return nil, nil, err
			}
			schema.set("pattern", p)
		}
		if values := enumValues(kind, s, traits); len(values) > 0 {
			schema.set("enum", values)
		}
		return shape, schema, nil
	case "byte", "short", "integer", "long", "intEnum":
		schema := newObject("type", "integer")
		if !output {
			r := traits.obj("smithy.api#range")
			if min, ok := number(r, "min"); ok {
				schema.set("minimum", min)
			}
			if max, ok := number(r, "max"); ok {
				schema.set("maximum", max)
			}
		}
		return &awsquery.Shape{Kind: awsquery.KindInteger}, schema, nil
	case "float", "double":
		return &awsquery.Shape{Kind: awsquery.KindFloat}, newObject("type", "number"), nil
	case "boolean":
		return &awsquery.Shape{Kind: awsquery.KindBoolean}, newObject("type", "boolean"), nil
	case "timestamp":
		if f := traits.str("smithy.api#timestampFormat"); f != "" && f != "date-time" {
			return nil, nil, c.failf("timestamp format %s is not the query protocol's date-time", f)
		}
		return &awsquery.Shape{Kind: awsquery.KindTimestamp}, newObject("type", "string", "maxLength", 64), nil
	case "blob":
		shape := &awsquery.Shape{Kind: awsquery.KindBlob}
		schema := newObject("type", "string")
		if output {
			shape.MaxLen = defaultMaxString
			schema.set("maxLength", defaultMaxString)
		}
		return shape, schema, nil
	case "structure":
		return c.structure(s, output, depth)
	case "list", "set":
		mo := s.obj("member")
		_, ts, err := c.m.resolve(mo.str("target"))
		if err != nil {
			return nil, nil, c.failf("%v", err)
		}
		elem, items, err := c.convert(mo.str("target"), mergedTraits(mo, ts), output, depth+1)
		if err != nil {
			return nil, nil, err
		}
		shape := &awsquery.Shape{Kind: awsquery.KindList, Elem: elem, ElemName: mo.obj("traits").str("smithy.api#xmlName")}
		schema := newObject("type", "array", "items", items)
		c.items(shape, schema, length, output)
		return shape, schema, nil
	case "map":
		ko, vo := s.obj("key"), s.obj("value")
		kk, _, err := c.m.resolve(ko.str("target"))
		if err != nil || (kk != "string" && kk != "enum") {
			return nil, nil, c.failf("map key is not a string")
		}
		_, vs, err := c.m.resolve(vo.str("target"))
		if err != nil {
			return nil, nil, c.failf("%v", err)
		}
		elem, values, err := c.convert(vo.str("target"), mergedTraits(vo, vs), output, depth+1)
		if err != nil {
			return nil, nil, err
		}
		shape := &awsquery.Shape{Kind: awsquery.KindMap, Elem: elem, KeyName: ko.obj("traits").str("smithy.api#xmlName"),
			ValueName: vo.obj("traits").str("smithy.api#xmlName")}
		schema := newObject("type", "object", "additionalProperties", values)
		if output {
			shape.MaxItems = defaultMaxItems
			schema.set("maxProperties", defaultMaxItems)
		}
		return shape, schema, nil
	}
	return nil, nil, c.failf("shape type %s has no query-protocol binding here", kind)
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (c *converter) items(shape *awsquery.Shape, schema, length *object, output bool) {
	if output {
		limit := defaultMaxItems
		if max, ok := number(length, "max"); ok {
			limit = int(max)
		}
		shape.MaxItems = limit
		schema.set("maxItems", limit)
		return
	}
	if min, ok := number(length, "min"); ok {
		schema.set("minItems", min)
	}
	if max, ok := number(length, "max"); ok {
		schema.set("maxItems", max)
	}
}

func (c *converter) structure(s *object, output bool, depth int) (*awsquery.Shape, *object, error) {
	shape := &awsquery.Shape{Kind: awsquery.KindStruct}
	props := newObject()
	var required []string
	ms := s.obj("members")
	parent := c.path
	defer func() { c.path = parent }()
	for _, name := range ms.keys {
		mo := ms.obj(name)
		_, ts, err := c.m.resolve(mo.str("target"))
		if err != nil {
			return nil, nil, c.failf("%v", err)
		}
		traits := mergedTraits(mo, ts)
		if traits.has("smithy.api#deprecated") {
			continue
		}
		c.path = parent + "." + name
		if traits.has("smithy.api#idempotencyToken") {
			return nil, nil, c.failf("an idempotency token below the top level has no binding")
		}
		key := snake(name)
		elem, schema, err := c.convert(mo.str("target"), traits, output, depth+1)
		if err != nil {
			return nil, nil, err
		}
		shape.Fields = append(shape.Fields, awsquery.Field{Key: key, Name: wire(member{obj: mo, name: name}), Shape: elem,
			Flattened: traits.has("smithy.api#xmlFlattened")})
		props.set(key, schema)
		if !output && traits.has("smithy.api#required") {
			required = append(required, key)
		}
	}
	schema := newObject("type", "object", "properties", props)
	if len(required) > 0 {
		schema.set("required", required)
	}
	schema.set("additionalProperties", false)
	return shape, schema, nil
}

// enumValues lists the values of an enum shape or of the legacy enum trait.
func enumValues(kind string, s, traits *object) []any {
	var values []any
	if kind == "enum" {
		ms := s.obj("members")
		for _, name := range ms.keys {
			v := ms.obj(name).obj("traits").str("smithy.api#enumValue")
			if v == "" {
				v = name
			}
			values = append(values, v)
		}
		return values
	}
	list, _ := traits.get("smithy.api#enum").([]any)
	for _, item := range list {
		if o, ok := item.(*object); ok {
			values = append(values, o.str("value"))
		}
	}
	return values
}

// makeSamples derives the generated test of the tool from its shapes and schema: argument values that
// pass the schema, the form parameters they encode to, and an answer the output shape decodes.
func (t *tool) makeSamples(m *Model) error {
	var schema map[string]any
	d := json.NewDecoder(strings.NewReader(strings.ReplaceAll(t.inputSchema, regionMarker, "{}")))
	d.UseNumber()
	if err := d.Decode(&schema); err != nil {
		return t.failf("input schema does not parse: %v", err)
	}
	args := newObject()
	props, _ := schema["properties"].(map[string]any)
	for _, f := range t.input.Fields {
		if f.IdempotencyToken {
			continue
		}
		v, err := t.sample(f.Key, f.Shape, props[f.Key])
		if err != nil {
			return err
		}
		args.set(f.Key, v)
	}
	t.sampleArgs = encodeCompact(args)
	var plain map[string]any
	pd := json.NewDecoder(strings.NewReader(t.sampleArgs))
	pd.UseNumber()
	if err := pd.Decode(&plain); err != nil {
		return t.failf("sample arguments do not parse: %v", err)
	}
	req, err := awsquery.Encode(t.input, plain, bytes.NewReader(make([]byte, 16)))
	if err != nil {
		return t.failf("sample arguments do not encode: %v", err)
	}
	t.sampleForm = map[string]string{}
	for k, v := range req.Params {
		if !t.hasToken || v != req.Token {
			t.sampleForm[k] = v
		}
	}
	var body strings.Builder
	if err := t.answer(&body, t.output); err != nil {
		return err
	}
	ns := ""
	if m.Namespace != "" {
		ns = ` xmlns="` + m.Namespace + `"`
	}
	t.sampleAnswer = "<" + t.action + "Response" + ns + "><" + t.action + "Result>" + body.String() + "</" + t.action +
		"Result></" + t.action + "Response>"
	return nil
}

// sampleChars are the characters a sample string is made of, tried in order until one fits the pattern.
var sampleChars = []string{"a", "A", "0", "x", "-", "_", "/", " "}

func (t *tool) sample(path string, s *awsquery.Shape, rawSchema any) (any, error) {
	schema, _ := rawSchema.(map[string]any)
	switch s.Kind {
	case awsquery.KindString:
		if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
			return values[0], nil
		}
		n := intOf(schema["minLength"], 1)
		if n < 1 {
			n = 1
		}
		var re *regexp.Regexp
		if p, ok := schema["pattern"].(string); ok {
			re = regexp.MustCompile(p)
		}
		for _, ch := range sampleChars {
			v := strings.Repeat(ch, n)
			if max := intOf(schema["maxLength"], len(v)); len(v) <= max && (re == nil || re.MatchString(v)) {
				return v, nil
			}
		}
		return nil, t.failf("%s: no sample value fits its pattern", path)
	case awsquery.KindInteger:
		n := intOf(schema["minimum"], 1)
		if max := intOf(schema["maximum"], n); n > max {
			n = max
		}
		return n, nil
	case awsquery.KindFloat:
		return json.Number("1.5"), nil
	case awsquery.KindBoolean:
		return true, nil
	case awsquery.KindTimestamp:
		return "2026-01-02T03:04:05Z", nil
	case awsquery.KindBlob:
		return "AA==", nil
	case awsquery.KindStruct:
		props, _ := schema["properties"].(map[string]any)
		o := newObject()
		for _, f := range s.Fields {
			v, err := t.sample(path+"."+f.Key, f.Shape, props[f.Key])
			if err != nil {
				return nil, err
			}
			o.set(f.Key, v)
		}
		return o, nil
	case awsquery.KindList:
		v, err := t.sample(path+"[]", s.Elem, schema["items"])
		return []any{v}, err
	case awsquery.KindMap:
		v, err := t.sample(path+"{}", s.Elem, schema["additionalProperties"])
		return newObject("k", v), err
	}
	return nil, t.failf("%s: no sample for this shape", path)
}

func intOf(v any, fallback int) int {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return fallback
}

// answer writes the members of a struct shape as the XML of an answer. Paging ends on the first page.
func (t *tool) answer(b *strings.Builder, s *awsquery.Shape) error {
	for _, f := range s.Fields {
		if t.pager != nil && f.Key == t.pager.TokenOut {
			continue
		}
		var inner strings.Builder
		if err := t.answerValue(&inner, f); err != nil {
			return err
		}
		b.WriteString(inner.String())
	}
	return nil
}

func (t *tool) answerValue(b *strings.Builder, f awsquery.Field) error {
	el := func(name, text string) { b.WriteString("<" + name + ">" + text + "</" + name + ">") }
	switch f.Shape.Kind {
	case awsquery.KindStruct:
		var inner strings.Builder
		if err := t.answer(&inner, f.Shape); err != nil {
			return err
		}
		el(f.Name, inner.String())
	case awsquery.KindList:
		var inner strings.Builder
		elemName := orDefault(f.Shape.ElemName, "member")
		if f.Flattened {
			elemName = f.Name
		}
		if err := t.answerValue(&inner, awsquery.Field{Name: elemName, Shape: f.Shape.Elem}); err != nil {
			return err
		}
		if f.Flattened {
			b.WriteString(inner.String())
		} else {
			el(f.Name, inner.String())
		}
	case awsquery.KindMap:
		var inner strings.Builder
		el2 := func(name, text string) { inner.WriteString("<" + name + ">" + text + "</" + name + ">") }
		el2(orDefault(f.Shape.KeyName, "key"), "k")
		var value strings.Builder
		if err := t.answerValue(&value, awsquery.Field{Name: orDefault(f.Shape.ValueName, "value"), Shape: f.Shape.Elem}); err != nil {
			return err
		}
		inner.WriteString(value.String())
		if f.Flattened {
			el(f.Name, inner.String())
		} else {
			el(f.Name, "<entry>"+inner.String()+"</entry>")
		}
	default:
		el(f.Name, t.scalarSample(f))
	}
	return nil
}

func (t *tool) scalarSample(f awsquery.Field) string {
	switch f.Shape.Kind {
	case awsquery.KindInteger:
		return "1"
	case awsquery.KindFloat:
		return "1.5"
	case awsquery.KindBoolean:
		if t.pager != nil && f.Key == t.pager.Truncated {
			return "false"
		}
		return "true"
	case awsquery.KindTimestamp:
		return "2026-01-02T03:04:05Z"
	case awsquery.KindBlob:
		return "AA=="
	}
	for _, r := range t.rules {
		if r.key == f.Key && r.form != "" {
			return formSamples[r.form]
		}
	}
	return "x"
}

// sortedKeys returns the keys of a form parameter map in order, for a deterministic rendering.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
