package github

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var queryTokenPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// norm compares a GraphQL member name and an output name regardless of case and underscores.
func norm(name string) string { return strings.ToLower(strings.ReplaceAll(name, "_", "")) }

// selectionMembers returns the members a GraphQL selection asks for, without type names and __typename.
func selectionMembers(selection string) map[string]bool {
	members := map[string]bool{}
	tokens := queryTokenPattern.FindAllString(selection, -1)
	for i, token := range tokens {
		if token == "on" || token == "__typename" || (i > 0 && tokens[i-1] == "on") {
			continue
		}
		members[norm(token)] = true
	}
	return members
}

// outputMemberNames collects the names of every object member at any depth.
func outputMemberNames(value any, into map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, member := range v {
			into[key] = true
			outputMemberNames(member, into)
		}
	case []any:
		for _, item := range v {
			outputMemberNames(item, into)
		}
	}
}

// checkSchemaCovers reports every member path of value that schema lacks.
func checkSchemaCovers(t *testing.T, path string, value any, schema map[string]any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, member := range v {
			child, ok := properties[key].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: not in the output schema", path, key)
				continue
			}
			checkSchemaCovers(t, path+"."+key, member, child)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			checkSchemaCovers(t, path+"[]", item, items)
		}
	}
}

// assertSelectionMatchesOutput checks, for a fully populated answer, that the selection asks for every
// member the output carries and that every member asked for reaches the output. outputOnly names output
// members that no query member of the same name feeds; queryOnly names query members that feed no member
// of the same name (counts, which become <name>_total_count, and renamed ones).
func assertSelectionMatchesOutput(t *testing.T, selection string, output, schema json.RawMessage,
	outputOnly, queryOnly []string) {
	t.Helper()
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatal(err)
	}
	raw := map[string]bool{}
	outputMemberNames(value, raw)
	members := selectionMembers(fullDocument(selection))
	skipOutput, skipQuery := map[string]bool{}, map[string]bool{}
	for _, name := range outputOnly {
		skipOutput[norm(name)] = true
	}
	for _, name := range queryOnly {
		skipQuery[norm(name)] = true
	}
	var problems []string
	normalized := map[string]bool{}
	for name := range raw {
		normalized[norm(name)] = true
	}
	for name := range raw {
		if members[norm(name)] || skipOutput[norm(name)] {
			continue
		}
		problems = append(problems, "output member "+name+" is not selected")
	}
	for member := range members {
		if normalized[member] || skipQuery[member] {
			continue
		}
		problems = append(problems, "selected member "+member+" reaches no output member")
	}
	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", value, parsed)
}

// entrySchema returns the schema of the entries of the top-level list property name.
func entrySchema(t *testing.T, schema json.RawMessage, name string) json.RawMessage {
	t.Helper()
	var parsed struct {
		Properties map[string]struct {
			Items json.RawMessage `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil || len(parsed.Properties[name].Items) == 0 {
		t.Fatalf("no list %s in the schema: %v", name, err)
	}
	return parsed.Properties[name].Items
}
