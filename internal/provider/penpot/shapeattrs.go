package penpot

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of the free values of a page and its shapes. A value beyond any of them is left out whole, never cut,
// and named in omitted_fields.
const (
	maxFreeDepth   = 8
	maxFreeEntries = 200
	maxFreeString  = 4096
	// maxAttrBytes bounds the attributes of all shapes of one answer together.
	maxAttrBytes = 1 << 20
)

type attrKind int

const (
	kindAny attrKind = iota
	kindText
	kindNumber
	kindBool
	kindID
	kindList
	kindObject
)

// attrSpec is one attribute Penpot's schema defines. Verbatim values keep the keys that plugins chose.
type attrSpec struct {
	name     string
	kind     attrKind
	verbatim bool
}

type attrDef struct {
	penpot string
	kind   attrKind
}

func defs(kind attrKind, names ...string) []attrDef {
	out := make([]attrDef, len(names))
	for i, name := range names {
		out[i] = attrDef{name, kind}
	}
	return out
}

func join(groups ...[]attrDef) []attrDef {
	var out []attrDef
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func table(groups ...[]attrDef) map[string]attrSpec {
	out := map[string]attrSpec{}
	for _, def := range join(groups...) {
		out[normalizeKey(def.penpot)] = attrSpec{name: strings.ReplaceAll(def.penpot, "-", "_"), kind: def.kind,
			verbatim: def.penpot == "plugin-data"}
	}
	return out
}

// The attributes of Penpot 2.18.0 (shape.cljc, shape/layout.cljc, variant.cljc, page.cljc).
var (
	commonAttrDefs = join(
		defs(kindList, "points", "touched", "fills", "grids", "exports", "strokes", "interactions", "shadow"),
		defs(kindObject, "selrect", "blur", "background-blur", "applied-tokens", "plugin-data"),
		defs(kindAny, "transform", "transform-inverse"),
		defs(kindID, "page-id", "component-id", "component-file", "shape-ref"),
		defs(kindBool, "component-root", "main-instance", "remote-synced", "blocked", "collapsed", "locked",
			"hidden", "masked-group", "proportion-lock", "fixed-scroll"),
		defs(kindNumber, "proportion", "r1", "r2", "r3", "r4", "opacity", "rotation"),
		defs(kindText, "constraints-h", "constraints-v", "blend-mode", "grow-type"),
		// Layout of a child (layout.cljc schema:layout-child-attrs).
		defs(kindText, "layout-item-margin-type", "layout-item-h-sizing", "layout-item-v-sizing", "layout-item-align-self"),
		defs(kindObject, "layout-item-margin"),
		defs(kindNumber, "layout-item-max-h", "layout-item-min-h", "layout-item-max-w", "layout-item-min-w",
			"layout-item-z-index"),
		defs(kindBool, "layout-item-absolute"),
	)
	frameAttrDefs = join(
		defs(kindList, "shapes", "layout-grid-rows", "layout-grid-columns"),
		defs(kindBool, "hide-fill-on-export", "show-content", "hide-in-viewer", "is-variant-container"),
		defs(kindText, "variant-name", "variant-error", "layout", "layout-flex-dir", "layout-gap-type", "layout-wrap-type",
			"layout-padding-type", "layout-justify-content", "layout-justify-items", "layout-align-content",
			"layout-align-items", "layout-grid-dir"),
		defs(kindID, "variant-id"),
		defs(kindObject, "layout-gap", "layout-padding", "layout-grid-cells"),
	)

	commonAttrs = table(commonAttrDefs)
	typeAttrs   = map[string]map[string]attrSpec{
		"group":   table(defs(kindList, "shapes")),
		"frame":   table(frameAttrDefs),
		"bool":    table(defs(kindList, "shapes", "content"), defs(kindText, "bool-type")),
		"svg-raw": table(defs(kindList, "shapes"), defs(kindObject, "content")),
		"path":    table(defs(kindList, "content")),
		"text":    table(defs(kindObject, "content"), defs(kindList, "position-data")),
		"image":   table(defs(kindObject, "metadata")),
	}
	pageAttrs = table(
		defs(kindNumber, "index", "pixel-grid-opacity"),
		defs(kindText, "background", "pixel-grid-color"),
		defs(kindObject, "default-grids", "flows", "guides", "comment-thread-positions", "plugin-data"),
	)
)

// shapeAttrSpecs lists the attributes a shape of this type may carry.
func shapeAttrSpecs(shapeType string) []attrSpec {
	merged := map[string]attrSpec{}
	for key, spec := range commonAttrs {
		merged[key] = spec
	}
	for key, spec := range typeAttrs[shapeType] {
		merged[key] = spec
	}
	return sortedSpecs(merged)
}

func sortedSpecs(specs map[string]attrSpec) []attrSpec {
	out := make([]attrSpec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// attrSchemas returns the output schema members of the shape and page attributes, by output name.
func attrSchemas(specs ...map[string]attrSpec) map[string]attrKind {
	out := map[string]attrKind{}
	for _, group := range specs {
		for _, spec := range group {
			if kind, seen := out[spec.name]; seen && kind != spec.kind {
				spec.kind = kindAny
			}
			out[spec.name] = spec.kind
		}
	}
	return out
}

func allShapeSpecs() []map[string]attrSpec {
	groups := []map[string]attrSpec{commonAttrs}
	for _, group := range typeAttrs {
		groups = append(groups, group)
	}
	return groups
}

func kindSchema(kind attrKind) string {
	switch kind {
	case kindText, kindID:
		return `{"type":"string"}`
	case kindNumber:
		return `{"type":"number"}`
	case kindBool:
		return `{"type":"boolean"}`
	case kindList:
		return `{"type":"array"}`
	case kindObject:
		return `{"type":"object"}`
	}
	return `{}`
}

func schemaMembers(kinds map[string]attrKind) (string, []string) {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(`"` + name + `":` + kindSchema(kinds[name]) + `,`)
	}
	return b.String(), names
}

// attrs holds the attributes read from one page or shape.
type attrs struct {
	values  map[string]json.RawMessage
	omitted []string
	size    int
}

// readAttrs reads the listed attributes of one object. Missing and empty values stay out; a value of another
// shape than the schema's (binary-encoded content arrives as text) or beyond the bounds is left out and named.
func readAttrs(o obj, specs []attrSpec) attrs {
	var out attrs
	for _, spec := range specs {
		raw := o[normalizeKey(spec.name)]
		if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			continue
		}
		value, ok := decodeAttr(raw, spec)
		if !ok {
			out.omitted = append(out.omitted, spec.name)
			continue
		}
		if isEmptyValue(value) {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > maxAttrBytes {
			out.omitted = append(out.omitted, spec.name)
			continue
		}
		if out.values == nil {
			out.values = map[string]json.RawMessage{}
		}
		out.values[spec.name] = encoded
		out.size += len(spec.name) + len(encoded)
	}
	return out
}

func decodeAttr(raw json.RawMessage, spec attrSpec) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	switch value.(type) {
	case string:
		if spec.kind != kindText && spec.kind != kindID && spec.kind != kindAny {
			return nil, false
		}
		if spec.kind == kindID {
			id, ok := parseUUID(value.(string))
			if !ok {
				return nil, false
			}
			return id, true
		}
	case json.Number:
		if spec.kind != kindNumber && spec.kind != kindAny {
			return nil, false
		}
	case bool:
		if spec.kind != kindBool && spec.kind != kindAny {
			return nil, false
		}
	case []any:
		if spec.kind != kindList && spec.kind != kindAny {
			return nil, false
		}
	case map[string]any:
		if spec.kind != kindObject && spec.kind != kindAny {
			return nil, false
		}
	}
	return convertFree(value, 0, !spec.verbatim)
}

// convertFree cleans a decoded free value: control characters in texts become spaces, keys become snake case
// (except UUID keys, and every key of a verbatim value), and members that are null or empty stay out. It reports
// false when a bound is exceeded.
func convertFree(value any, depth int, translate bool) (any, bool) {
	switch v := value.(type) {
	case string:
		if len(v) > maxFreeString {
			return nil, false
		}
		return cleanText(v), true
	case []any:
		if depth >= maxFreeDepth || len(v) > maxFreeEntries {
			return nil, false
		}
		out := make([]any, len(v))
		for i, item := range v {
			converted, ok := convertFree(item, depth+1, translate)
			if !ok {
				return nil, false
			}
			out[i] = converted
		}
		return out, true
	case map[string]any:
		if depth >= maxFreeDepth || len(v) > maxFreeEntries {
			return nil, false
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(v))
		for _, key := range keys {
			if len(key) > maxFreeString {
				return nil, false
			}
			converted, ok := convertFree(v[key], depth+1, translate)
			if !ok {
				return nil, false
			}
			if isEmptyValue(converted) {
				continue
			}
			name := cleanText(key)
			if _, isID := parseUUID(key); isID {
				name = key
			} else if translate {
				name = snakeKey(name)
			}
			if _, taken := out[name]; !taken {
				out[name] = converted
			}
		}
		return out, true
	}
	return value, true
}

func isEmptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// snakeKey translates a kebab-case or camel-case key to snake case.
func snakeKey(key string) string {
	runes := []rune(key)
	var b strings.Builder
	for i, r := range runes {
		switch {
		case r == '-' || r == ' ':
			b.WriteByte('_')
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cleanText turns control characters into spaces; the text is provider data and never trusted.
func cleanText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value)
}

// cleanName reads a string value like str with control characters cleaned.
func (o obj) cleanName(key string) string { return cleanText(o.str(key)) }

// marshalWith appends attribute members to an encoded object.
func marshalWith(base []byte, values map[string]json.RawMessage) []byte {
	if len(values) == 0 {
		return base
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := bytes.NewBuffer(append([]byte(nil), base[:len(base)-1]...))
	for _, name := range names {
		key, _ := json.Marshal(name)
		out.WriteByte(',')
		out.Write(key)
		out.WriteByte(':')
		out.Write(values[name])
	}
	out.WriteByte('}')
	return out.Bytes()
}
