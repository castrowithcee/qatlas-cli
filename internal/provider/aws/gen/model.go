package gen

import (
	"errors"
	"fmt"
	"strings"
)

// Model is one service of a Smithy JSON AST extract.
type Model struct {
	shapes    *object
	serviceID string
	service   *object
	// Protocol is ProtocolQuery or ProtocolRestJSON, read from the service's protocol trait.
	Protocol string
	// Version is the API version of the model; the query protocol sends it as Version.
	Version string
	// Namespace is the xmlNamespace of the service, used for the answers of generated tests.
	Namespace string
}

// The protocols of a service model this package reads.
const (
	ProtocolQuery    = "awsQuery"
	ProtocolRestJSON = "restJson1"
)

var protocolTraits = map[string]string{
	"aws.protocols#awsQuery": ProtocolQuery, "aws.protocols#restJson1": ProtocolRestJSON,
}

// LoadModel reads an extract. It must hold exactly one service that speaks exactly one of awsQuery and
// restJson1. Operations bound through resources count as operations of the service: the REST bindings
// carry the identifiers as ordinary members of the input, so a resource adds no binding of its own.
func LoadModel(data []byte) (*Model, error) {
	root, err := decodeOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("model is not JSON: %w", err)
	}
	ro, _ := root.(*object)
	shapes := ro.obj("shapes")
	if shapes == nil {
		return nil, errors.New("model has no shapes")
	}
	m := &Model{shapes: shapes}
	for _, id := range shapes.keys {
		if s := shapes.obj(id); s.str("type") == "service" {
			if m.service != nil {
				return nil, errors.New("model holds more than one service")
			}
			m.serviceID, m.service = id, s
		}
	}
	if m.service == nil {
		return nil, errors.New("model holds no service")
	}
	traits := m.service.obj("traits")
	for trait, protocol := range protocolTraits {
		if traits.has(trait) {
			if m.Protocol != "" {
				return nil, fmt.Errorf("service %s speaks more than one protocol", m.serviceID)
			}
			m.Protocol = protocol
		}
	}
	if m.Protocol == "" {
		return nil, fmt.Errorf("service %s speaks neither awsQuery nor restJson1", m.serviceID)
	}
	m.Version = m.service.str("version")
	m.Namespace = traits.obj("smithy.api#xmlNamespace").str("uri")
	if m.Version == "" {
		return nil, fmt.Errorf("service %s has no version", m.serviceID)
	}
	return m, nil
}

// Operations returns the operation names of the service, in model order.
func (m *Model) Operations() ([]string, error) {
	ids := m.operationIDs()
	names := make([]string, 0, len(ids))
	for _, target := range ids {
		if m.shapes.obj(target).str("type") != "operation" {
			return nil, fmt.Errorf("service operation %q is not an operation shape", target)
		}
		names = append(names, shortName(target))
	}
	return names, nil
}

// operation returns the operation shape of a name.
func (m *Model) operation(name string) (*object, error) {
	for _, id := range m.operationIDs() {
		if shortName(id) == name {
			return m.shapes.obj(id), nil
		}
	}
	return nil, fmt.Errorf("operation %s is not in the model", name)
}

// operationIDs returns the operations of the service in a fixed order: those it lists, then those of its
// resources, depth first, each once.
func (m *Model) operationIDs() []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	targets := func(o *object, key string) []string {
		list, _ := o.get(key).([]any)
		out := make([]string, 0, len(list))
		for _, item := range list {
			if t, ok := item.(*object); ok {
				out = append(out, t.str("target"))
			}
		}
		return out
	}
	var walk func(holder *object, visited map[string]bool)
	walk = func(holder *object, visited map[string]bool) {
		for _, id := range targets(holder, "operations") {
			add(id)
		}
		for _, id := range targets(holder, "resources") {
			if visited[id] {
				continue
			}
			visited[id] = true
			res := m.shapes.obj(id)
			if res == nil {
				continue
			}
			for _, key := range []string{"create", "put", "read", "update", "delete", "list"} {
				add(res.obj(key).str("target"))
			}
			for _, id := range targets(res, "operations") {
				add(id)
			}
			for _, id := range targets(res, "collectionOperations") {
				add(id)
			}
			walk(res, visited)
		}
	}
	walk(m.service, map[string]bool{})
	return ids
}

func shortName(id string) string {
	_, name, _ := strings.Cut(id, "#")
	return name
}

// prelude maps the Smithy prelude shapes the query protocol serializes to their type.
var prelude = map[string]string{
	"smithy.api#String": "string", "smithy.api#Boolean": "boolean", "smithy.api#PrimitiveBoolean": "boolean",
	"smithy.api#Byte": "byte", "smithy.api#PrimitiveByte": "byte", "smithy.api#Short": "short",
	"smithy.api#PrimitiveShort": "short", "smithy.api#Integer": "integer", "smithy.api#PrimitiveInteger": "integer",
	"smithy.api#Long": "long", "smithy.api#PrimitiveLong": "long", "smithy.api#Float": "float",
	"smithy.api#PrimitiveFloat": "float", "smithy.api#Double": "double", "smithy.api#PrimitiveDouble": "double",
	"smithy.api#Timestamp": "timestamp", "smithy.api#Blob": "blob", "smithy.api#Document": "document",
}

// resolve returns the type and the shape object of a target. A prelude shape has no object.
func (m *Model) resolve(target string) (string, *object, error) {
	if kind, ok := prelude[target]; ok {
		return kind, &object{vals: map[string]any{}}, nil
	}
	s := m.shapes.obj(target)
	if s == nil {
		return "", nil, fmt.Errorf("shape %s is not in the model", target)
	}
	return s.str("type"), s, nil
}

// mergedTraits returns the traits of a member over those of its target: a member trait wins.
func mergedTraits(member, target *object) *object {
	out := &object{vals: map[string]any{}}
	for _, src := range []*object{target.obj("traits"), member.obj("traits")} {
		if src == nil {
			continue
		}
		for _, k := range src.keys {
			if !out.has(k) {
				out.keys = append(out.keys, k)
			}
			out.vals[k] = src.vals[k]
		}
	}
	return out
}
