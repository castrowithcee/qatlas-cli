package application

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// dropEmpty removes, recursively, every object member whose value is null, an empty array or an empty
// object, after its own members were cleaned. Missing means empty. Strings, numbers and booleans always
// stay, including "", 0 and false. Array entries and the result itself are never removed, only cleaned, so
// positions and the shape of the root stay stable.
func dropEmpty(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			child = dropEmpty(child)
			if isEmpty(child) {
				delete(value, key)
				continue
			}
			value[key] = child
		}
	case []any:
		for i := range value {
			value[i] = dropEmpty(value[i])
		}
	}
	return value
}

func isEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	}
	return false
}

// PublishedOutputSchema derives the output schema that describe publishes from the one results are
// validated against. A result drops its empty members, so a required member that can be empty (an array
// without minItems, an object none of whose members is still required, or a member without a type) is no
// longer required in the published contract. Everything else, including the schema that validates the
// provider's own answer, stays as it is. A schema it cannot read is returned unchanged.
func PublishedOutputSchema(raw json.RawMessage) json.RawMessage {
	relaxed, _, ok := relaxSchema(raw)
	if !ok {
		return raw
	}
	return relaxed
}

// relaxSchema returns the relaxed schema and whether the schema can be empty (and then be dropped from
// its parent). ok is false where the schema cannot be read.
func relaxSchema(raw json.RawMessage) (json.RawMessage, bool, bool) {
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil {
		return raw, false, false
	}
	var kind string
	if len(schema["type"]) > 0 && json.Unmarshal(schema["type"], &kind) != nil {
		return raw, false, false
	}
	changed := false

	if items := schema["items"]; len(items) > 0 {
		if relaxed, _, ok := relaxSchema(items); ok && string(relaxed) != string(items) {
			schema["items"], changed = relaxed, true
		}
	}

	var properties map[string]json.RawMessage
	if len(schema["properties"]) > 0 && json.Unmarshal(schema["properties"], &properties) != nil {
		return raw, false, false
	}
	droppable := map[string]bool{}
	propertiesChanged := false
	for name, property := range properties {
		relaxed, canBeEmpty, ok := relaxSchema(property)
		if !ok {
			continue
		}
		droppable[name] = canBeEmpty
		if string(relaxed) != string(property) {
			properties[name], propertiesChanged = relaxed, true
		}
	}
	if propertiesChanged {
		encoded, err := json.Marshal(properties)
		if err != nil {
			return raw, false, false
		}
		schema["properties"], changed = encoded, true
	}

	var required, kept []string
	if len(schema["required"]) > 0 && json.Unmarshal(schema["required"], &required) != nil {
		return raw, false, false
	}
	for _, name := range required {
		if !droppable[name] {
			kept = append(kept, name)
		}
	}
	if len(kept) != len(required) {
		changed = true
		if len(kept) == 0 {
			delete(schema, "required")
		} else {
			schema["required"], _ = json.Marshal(kept)
		}
	}

	canBeEmpty := false
	switch kind {
	case "":
		canBeEmpty = true
	case "array":
		canBeEmpty = len(schema["minItems"]) == 0 || string(schema["minItems"]) == "0"
	case "object":
		canBeEmpty = len(kept) == 0 && (len(schema["minProperties"]) == 0 || string(schema["minProperties"]) == "0")
	}
	if !changed {
		return raw, canBeEmpty, true
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return raw, false, false
	}
	return encoded, canBeEmpty, true
}

// dropImplied removes from each entry of the declared result lists the member a filter argument fixes, but
// only where the request set that argument explicitly to one of the declared values.
func dropImplied(descriptor capability.Descriptor, arguments json.RawMessage, value any) any {
	if len(descriptor.ImpliedFields) == 0 {
		return value
	}
	var set map[string]any
	if json.Unmarshal(arguments, &set) != nil {
		return value
	}
	root, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for _, implied := range descriptor.ImpliedFields {
		given, ok := set[implied.Argument].(string)
		if !ok || !containsString(implied.Values, given) {
			continue
		}
		entries, _ := root[implied.List].([]any)
		for _, entry := range entries {
			if object, ok := entry.(map[string]any); ok {
				delete(object, implied.Field)
			}
		}
	}
	return value
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// relaxImplied drops the implied members from the required members of the published list entries, since
// they can be missing.
func relaxImplied(descriptor capability.Descriptor) json.RawMessage {
	raw := descriptor.OutputSchema
	var schema map[string]any
	if len(descriptor.ImpliedFields) == 0 || json.Unmarshal(raw, &schema) != nil {
		return raw
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, implied := range descriptor.ImpliedFields {
		list, _ := properties[implied.List].(map[string]any)
		items, _ := list["items"].(map[string]any)
		required, _ := items["required"].([]any)
		kept := make([]any, 0, len(required))
		for _, name := range required {
			if name != implied.Field {
				kept = append(kept, name)
			}
		}
		if len(kept) == 0 {
			delete(items, "required")
		} else {
			items["required"] = kept
		}
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return encoded
}

// PublishedDescriptor returns the descriptor as describe publishes it: its output schema relaxed for the
// members a result can lack.
func PublishedDescriptor(descriptor capability.Descriptor) capability.Descriptor {
	descriptor.OutputSchema = PublishedOutputSchema(relaxImplied(descriptor))
	return descriptor
}
