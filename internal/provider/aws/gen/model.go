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
	// Version is the API version the query protocol sends.
	Version string
	// Namespace is the xmlNamespace of the service, used for the answers of generated tests.
	Namespace string
}

// LoadModel reads an extract. It must hold exactly one service, speak awsQuery, and list its operations
// directly: operations bound through resources are not supported, since this package has no way to bind
// their identifiers.
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
	if !traits.has("aws.protocols#awsQuery") {
		return nil, fmt.Errorf("service %s does not speak awsQuery", m.serviceID)
	}
	if m.service.has("resources") {
		return nil, fmt.Errorf("service %s binds operations through resources", m.serviceID)
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
	list, _ := m.service.get("operations").([]any)
	names := make([]string, 0, len(list))
	for _, item := range list {
		target := item.(*object).str("target")
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

func (m *Model) operationIDs() []string {
	list, _ := m.service.get("operations").([]any)
	ids := make([]string, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.(*object).str("target"))
	}
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
	"smithy.api#Timestamp": "timestamp", "smithy.api#Blob": "blob",
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
