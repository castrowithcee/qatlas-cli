package cli

// The application validator reads "type" as one name and ignores oneOf, so registered schemas express
// alternatives with anyOf.

import (
	"encoding/json"
	"sort"
	"strconv"
	"testing"
)

func TestRegisteredSchemasUseAnyOfInsteadOfTypeListsAndOneOf(t *testing.T) {
	reg := sharedRegistry()
	descriptors := reg.All()
	if len(descriptors) == 0 {
		t.Fatal("the shipped registry has no tool")
	}
	for _, descriptor := range descriptors {
		for kind, raw := range map[string]json.RawMessage{"input": descriptor.InputSchema, "output": descriptor.OutputSchema} {
			if len(raw) == 0 {
				continue
			}
			var schema any
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Errorf("%s: %s schema is not valid JSON: %v", descriptor.ID, kind, err)
				continue
			}
			for _, violation := range schemaFormViolations("$", schema) {
				t.Errorf("%s: %s schema %s", descriptor.ID, kind, violation)
			}
		}
	}
}

func TestSchemaFormViolationsDetectTypeListsAndOneOf(t *testing.T) {
	for name, raw := range map[string]string{
		"type list":       `{"type":"object","properties":{"a":{"type":["integer","null"]}}}`,
		"oneOf":           `{"type":"object","oneOf":[{"required":["a"]},{"required":["b"]}]}`,
		"in items":        `{"type":"array","items":{"type":["string","number"]}}`,
		"in additional":   `{"type":"object","additionalProperties":{"type":["string","integer"]}}`,
		"in anyOf branch": `{"anyOf":[{"oneOf":[{"type":"string"}]},{"type":"null"}]}`,
		"in patternProps": `{"type":"object","patternProperties":{"^a":{"type":["string"]}}}`,
	} {
		var schema any
		if err := json.Unmarshal([]byte(raw), &schema); err != nil {
			t.Fatal(err)
		}
		if len(schemaFormViolations("$", schema)) == 0 {
			t.Errorf("%s: not detected in %s", name, raw)
		}
	}
	for name, raw := range map[string]string{
		"anyOf":           `{"anyOf":[{"type":"integer","minimum":1},{"type":"null"}]}`,
		"member names":    `{"type":"object","properties":{"oneOf":{"type":"string"},"type":{"type":"string"}}}`,
		"enum of strings": `{"type":"string","enum":["oneOf","type"]}`,
	} {
		var schema any
		if err := json.Unmarshal([]byte(raw), &schema); err != nil {
			t.Fatal(err)
		}
		if got := schemaFormViolations("$", schema); len(got) != 0 {
			t.Errorf("%s: false positive %v", name, got)
		}
	}
}

// schemaFormViolations walks the subschemas of schema, never the values of members or enums, and names each
// type list and each oneOf by its path.
func schemaFormViolations(path string, schema any) []string {
	node, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	if _, isList := node["type"].([]any); isList {
		out = append(out, path+" has a type list; use anyOf with one type per branch")
	}
	if _, has := node["oneOf"]; has {
		out = append(out, path+" uses oneOf; use anyOf")
	}
	for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		members, _ := node[keyword].(map[string]any)
		names := make([]string, 0, len(members))
		for name := range members {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, schemaFormViolations(path+"."+keyword+"."+name, members[name])...)
		}
	}
	for _, keyword := range []string{"items", "additionalProperties", "not"} {
		out = append(out, schemaFormViolations(path+"."+keyword, node[keyword])...)
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		branches, _ := node[keyword].([]any)
		for i, branch := range branches {
			out = append(out, schemaFormViolations(path+"."+keyword+"["+strconv.Itoa(i)+"]", branch)...)
		}
	}
	return out
}
