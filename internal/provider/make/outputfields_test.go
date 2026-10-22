package makeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// assertOutputDeclared fails when a member of the tool output out is missing from the output schema or, on
// the top level and in the entries of a top-level object or list, from the fields; and when a top-level
// schema member has no field. A top-level container (list or object with declared members) is named by its
// entry members, as Fields do. Deeper levels are checked against the schema only. It returns the number of
// members seen, so a caller can guard against an empty answer.
func assertOutputDeclared(t *testing.T, toolID string, out []byte) int {
	t.Helper()
	descriptor, _, ok := registry(t).Lookup(toolID)
	if !ok {
		t.Fatalf("%s is not registered", toolID)
	}
	var schema map[string]any
	if err := json.Unmarshal(descriptor.OutputSchema, &schema); err != nil {
		t.Fatalf("%s: output schema unreadable: %v", toolID, err)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatalf("%s: output is not an object: %v", toolID, err)
	}
	fields := map[string]bool{}
	for _, field := range descriptor.Fields {
		fields[field.Name] = true
	}
	seen := 0
	declared := map[string]bool{}
	var walk func(path string, value map[string]any, schema map[string]any, depth int)
	walk = func(path string, value map[string]any, schema map[string]any, depth int) {
		properties, _ := schema["properties"].(map[string]any)
		for name, member := range value {
			seen++
			full := path + name
			property, ok := properties[name].(map[string]any)
			if !ok {
				t.Errorf("%s: %s is not in the output schema", toolID, full)
				continue
			}
			entry := entrySchema(property)
			// A top-level container is documented by its entry members, not by a field of its own.
			container := depth == 0 && entry != nil
			if !container {
				declared[name] = true
				if depth <= 1 && !fields[name] {
					t.Errorf("%s: %s is not in fields", toolID, full)
				}
			}
			if entry == nil {
				continue
			}
			switch typed := member.(type) {
			case map[string]any:
				walk(full+".", typed, entry, depth+1)
			case []any:
				for _, item := range typed {
					if object, ok := item.(map[string]any); ok {
						walk(full+"[].", object, entry, depth+1)
					}
				}
			}
		}
		if depth != 0 {
			return
		}
		for name, member := range properties {
			property, _ := member.(map[string]any)
			if entrySchema(property) == nil && !fields[name] {
				t.Errorf("%s: schema member %s has no field", toolID, name)
			}
		}
	}
	walk("", value, schema, 0)
	var unnamed []string
	for _, field := range descriptor.Fields {
		if !declared[field.Name] && !hasSchemaMember(schema, field.Name) {
			unnamed = append(unnamed, field.Name)
		}
	}
	sort.Strings(unnamed)
	if len(unnamed) > 0 {
		t.Errorf("%s: fields without schema member: %s", toolID, strings.Join(unnamed, ", "))
	}
	return seen
}

// hasSchemaMember reports whether name is a top-level member or a member of the entries of a top-level
// object or list, which is where Fields name them.
func hasSchemaMember(schema map[string]any, name string) bool {
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties[name]; ok {
		return true
	}
	return isEntryMember(properties, name)
}

func isEntryMember(properties map[string]any, name string) bool {
	for _, property := range properties {
		node, _ := property.(map[string]any)
		members, _ := entrySchema(node)["properties"].(map[string]any)
		if _, ok := members[name]; ok {
			return true
		}
	}
	return false
}

// entrySchema returns the schema of the entries of a list of objects or of an object with declared members,
// and nil for any other node, which is a leaf as far as the fields are concerned.
func entrySchema(node map[string]any) map[string]any {
	if items, ok := node["items"].(map[string]any); ok {
		node = items
	}
	if _, ok := node["properties"]; ok {
		return node
	}
	return nil
}

const richScenario = `{"id":10,"name":"Own","teamId":1001,"description":"d","folderId":300,"isActive":true,` +
	`"islocked":true,"isPaused":true,"isinvalid":true,"scheduling":{"type":"indefinitely","interval":900},` +
	`"operations":5,"centicredits":6,"transfer":7,"dlqCount":2,"created":"2026-01-01T00:00:00Z",` +
	`"lastEdit":"2026-01-02T00:00:00Z","nextExec":"2026-01-03T00:00:00Z",` +
	`"createdByUser":{"id":1,"name":"A","email":"a@example.test"},` +
	`"updatedByUser":{"id":2,"name":"B","email":"b@example.test"}}`

const richRun = `{"id":"run-1","teamId":1001,"organizationId":501,"status":3,"type":"execution","eventType":"scheduled",` +
	`"timestamp":1700000000000,"duration":1200,"operations":3,"transfer":4,"centicredits":5,` +
	`"interruptReason":"hitl","interruptModuleId":"7","authorId":9,"authorName":"C",` +
	`"detail":{"error":{"message":"boom"}}}`

// richBlueprint masks more credential slots than the masked list holds, so masked_truncated is set.
func richBlueprint() string {
	modules := make([]string, maxMaskedPointers+1)
	for i := range modules {
		modules[i] = fmt.Sprintf(`{"id":%d,"module":"http:ActionSendData","mapper":{"authPass":"secret"}}`, i+1)
	}
	return `{"code":"OK","response":{"blueprint":{"name":"Own","flow":[` + strings.Join(modules, ",") + `]}}}`
}

// The output schema is enforced on the way through the core, so a member it lacks fails the call; the
// helper then also demands the matching field.
func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	scenarioPath := apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/scenarios":
			return jsonResponse(200, `{"scenarios":[`+richScenario+`]}`), nil
		case scenarioPath:
			return jsonResponse(200, `{"scenario":`+richScenario+`}`), nil
		case scenarioPath + "/blueprint":
			return jsonResponse(200, richBlueprint()), nil
		case scenarioPath + "/logs":
			return jsonResponse(200, `{"scenarioLogs":[`+richRun+`]}`), nil
		case scenarioPath + "/logs/run-1":
			return jsonResponse(200, `{"scenarioLog":`+richRun+`}`), nil
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
		return jsonResponse(404, `{}`), nil
	})
	id := strconv.FormatInt(ownScenario, 10)
	cases := []struct {
		tool, arguments string
		minimum         int
	}{
		{scenariosList.ID, `{}`, 25},
		{scenariosGet.ID, `{"scenario_id":` + id + `}`, 19},
		{scenariosBlueprint.ID, `{"scenario_id":` + id + `}`, 4},
		{runsList.ID, `{"scenario_id":` + id + `}`, 20},
		{runsGet.ID, `{"scenario_id":` + id + `,"execution_id":"run-1"}`, 17},
	}
	for _, tt := range cases {
		t.Run(tt.tool, func(t *testing.T) {
			out, err := env.invoke(tt.tool, "open", tt.arguments)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if seen := assertOutputDeclared(t, tt.tool, []byte(out)); seen < tt.minimum {
				t.Errorf("checked %d members, want at least %d: %s", seen, tt.minimum, out)
			}
		})
	}
}
