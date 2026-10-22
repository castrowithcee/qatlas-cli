package forge

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// A projection copies the documented members of one API object into a result: each member keeps its name and
// JSON type, a member the instance does not send stays absent, and a member of the wrong type is dropped.
// Nested objects carry their own members; that is where a projection leaves out what a result must not show.

type memberKind int

const (
	kindText memberKind = iota
	kindInteger
	kindFlag
	kindTexts
	kindObject
	kindObjects
)

// maxListItems bounds a list inside one object, such as the topics of a repository.
const maxListItems = 100

// member is one documented member. only limits it to one product; address marks a URL that is left out when it
// carries user info or a query, since either may hold a credential.
type member struct {
	name    string
	kind    memberKind
	only    *product
	address bool
	sub     []member
	note    string
}

func (m member) offered(p *product) bool { return m.only == nil || m.only == p }

// schemaOf is the JSON schema of an object with the members the product offers.
func schemaOf(p *product, members []member, required ...string) string {
	var properties []string
	for _, m := range members {
		if m.offered(p) {
			properties = append(properties, `"`+m.name+`":`+m.schema(p))
		}
	}
	names := make([]string, len(required))
	for i, name := range required {
		names[i] = `"` + name + `"`
	}
	schema := `{"type":"object","properties":{` + strings.Join(properties, ",") + `}`
	if len(names) > 0 {
		schema += `,"required":[` + strings.Join(names, ",") + `]`
	}
	return schema + `,"additionalProperties":false}`
}

func (m member) schema(p *product) string {
	switch m.kind {
	case kindInteger:
		return `{"type":"integer"}`
	case kindFlag:
		return `{"type":"boolean"}`
	case kindTexts:
		return `{"type":"array","items":{"type":"string"}}`
	case kindObject:
		return schemaOf(p, m.sub)
	case kindObjects:
		return `{"type":"array","items":` + schemaOf(p, m.sub) + `}`
	}
	return `{"type":"string"}`
}

// fieldsOf describes the members the product offers, for discovery.
func fieldsOf(p *product, members []member) []capability.Field {
	var fields []capability.Field
	for _, m := range members {
		if m.offered(p) {
			fields = append(fields, capability.Field{Name: m.name, Description: m.note})
		}
	}
	return fields
}

// project copies the members the product offers from one object.
func project(p *product, members []member, o object) map[string]any {
	out := map[string]any{}
	for _, m := range members {
		if !m.offered(p) {
			continue
		}
		if value, ok := m.read(p, o); ok {
			out[m.name] = value
		}
	}
	return out
}

func (m member) read(p *product, o object) (any, bool) {
	switch m.kind {
	case kindInteger:
		return o.integer(m.name)
	case kindFlag:
		return o.flag(m.name)
	case kindTexts:
		var values []string
		if json.Unmarshal(o[m.name], &values) != nil {
			return nil, false
		}
		if len(values) > maxListItems {
			values = values[:maxListItems]
		}
		for i := range values {
			values[i] = bounded(values[i])
		}
		return values, true
	case kindObject:
		child, ok := o.child(m.name)
		if !ok {
			return nil, false
		}
		return project(p, m.sub, child), true
	case kindObjects:
		var children []object
		if json.Unmarshal(o[m.name], &children) != nil {
			return nil, false
		}
		if len(children) > maxListItems {
			children = children[:maxListItems]
		}
		out := make([]map[string]any, 0, len(children))
		for _, child := range children {
			if child != nil {
				out = append(out, project(p, m.sub, child))
			}
		}
		return out, true
	}
	value, ok := o.text(m.name)
	if ok && m.address && !plainAddress(value) {
		return nil, false
	}
	return value, ok
}

// plainAddress reports an address without user info and query, or an empty one.
func plainAddress(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.User == nil && parsed.RawQuery == "" && !parsed.ForceQuery
}
