package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// withheldNodes holds every value the fixed rules withhold, each marked with "canary" so a test can prove
// none of them reaches any output, plus a harmless path of a node type the trigger rule does not cover.
const withheldNodes = `[
 {"id":"w1","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
  "webhookId":"webhook-id-canary-1","parameters":{"httpMethod":"POST","path":"hook-path-canary"}},
 {"id":"f1","name":"Form","type":"n8n-nodes-base.formTrigger","typeVersion":2.2,"position":[0,200],
  "webhookId":"webhook-id-canary-2",
  "parameters":{"formTitle":"Contact","path":"form-path-canary","options":{"path":"form-options-path-canary"}}},
 {"id":"m1","name":"MCP","type":"@n8n/n8n-nodes-langchain.mcpTrigger","typeVersion":2,"position":[0,400],
  "webhookId":"webhook-id-canary-3","parameters":{"path":"mcp-path-canary"}},
 {"id":"h1","name":"HTTP","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[200,0],
  "parameters":{"url":"https://user:pw-canary@example.invalid/v1","sendHeaders":true,
   "headerParameters":{"parameters":[{"name":"Authorization","value":"Bearer header-canary"}]},
   "sendQuery":true,"queryParameters":{"parameters":[{"name":"api_key","value":"query-canary"},
   {"name":"page","value":"2"}]},
   "jsonHeaders":"{\"X-Custom\":\"json-headers-canary\"}",
   "jsonBody":"{\"token\": \"json-body-canary\", \"limit\": 5}","timeout":10000}},
 {"id":"h2","name":"HTTP query","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[200,200],
  "parameters":{"url":"https://api.example.invalid/v1?page=1&access_token=url-query-canary"}},
 {"id":"p1","name":"WordPress","type":"n8n-nodes-base.wordpress","typeVersion":1,"position":[400,0],
  "parameters":{"additionalFields":{"password":"wp-canary","title":"Post"}}},
 {"id":"r1","name":"Read file","type":"n8n-nodes-base.readWriteFile","typeVersion":1,"position":[400,200],
  "parameters":{"path":"/data/harmless.txt"}}
]`

var wantWithheld = map[string][]string{
	"w1": {"/path"},
	"f1": {"/options/path", "/path"},
	"m1": {"/path"},
	"h1": {"/headerParameters/parameters/0/value", "/jsonBody", "/jsonHeaders",
		"/queryParameters/parameters/0/value", "/url"},
	"h2": {"/url"},
	"p1": {"/additionalFields/password"},
}

func withheldWorkflowJSON(id, nodes string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Hooks","active":false,"isArchived":false,`+
		`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z","triggerCount":3,`+
		`"tags":[],"nodes":%s,"connections":{},"shared":[{"role":"workflow:owner","projectId":%q}]}`,
		id, nodes, ownProject)
}

// readNodes decodes the fixture the way a test compares it with what was sent.
func readNodes(t *testing.T, nodes string) map[string]map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal([]byte(nodes), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, n := range list {
		byID[n["id"].(string)] = n
	}
	return byID
}

func assertNoCanary(t *testing.T, label, text string) {
	t.Helper()
	if strings.Contains(text, "canary") || strings.Contains(text, "webhookId") {
		t.Fatalf("%s leaked a withheld value: %s", label, text)
	}
}

func TestWorkflowsGetWithholdsTriggerPathsAndCredentials(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, withheldWorkflowJSON(ownWorkflow, withheldNodes)), nil
	})
	result, err := env.invoke(workflowsGet.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	assertNoCanary(t, "result", result)
	var detail WorkflowDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil {
		t.Fatal(err)
	}
	for _, node := range detail.Nodes {
		if !reflect.DeepEqual(node.WithheldParameters, wantWithheld[node.ID]) {
			t.Errorf("node %s withheld %v, want %v", node.ID, node.WithheldParameters, wantWithheld[node.ID])
		}
		params, err := decodeParameters(node.Parameters)
		if err != nil {
			t.Fatalf("node %s parameters = %s", node.ID, node.Parameters)
		}
		for _, pointer := range node.WithheldParameters {
			if value, _ := valueAt(params, pointer); value != withheldPlaceholder {
				t.Errorf("node %s at %s = %v, want the placeholder", node.ID, pointer, value)
			}
		}
		if node.ID == "r1" && string(node.Parameters) != `{"path":"/data/harmless.txt"}` {
			t.Errorf("harmless path changed: %s", node.Parameters)
		}
		if node.ID == "h1" {
			if value, _ := valueAt(params, "/queryParameters/parameters/1/value"); value != "2" {
				t.Errorf("harmless query value changed: %s", node.Parameters)
			}
			if value, _ := valueAt(params, "/timeout"); value != json.Number("10000") {
				t.Errorf("number changed: %s", node.Parameters)
			}
		}
	}
}

func updateArgumentsFrom(t *testing.T, result string, edit func(nodes []map[string]any) []map[string]any) string {
	t.Helper()
	var detail struct {
		Name        string           `json:"name"`
		Nodes       []map[string]any `json:"nodes"`
		Connections json.RawMessage  `json:"connections"`
	}
	if err := json.Unmarshal([]byte(result), &detail); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		detail.Nodes = edit(detail.Nodes)
	}
	// The core leaves an empty object out of a result.
	if len(detail.Connections) == 0 {
		detail.Connections = json.RawMessage(`{}`)
	}
	encoded, _ := json.Marshal(map[string]any{"workflow_id": ownWorkflow, "name": detail.Name,
		"nodes": detail.Nodes, "connections": detail.Connections, "settings": map[string]any{}})
	return string(encoded)
}

// updateEnvironment answers every read with nodes and records the one PUT body.
func updateEnvironment(t *testing.T, nodes string, calls *[]call, put *map[string]any) *environment {
	return newEnvironment(t, calls, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodGet:
			return jsonResponse(200, withheldWorkflowJSON(ownWorkflow, nodes)), nil
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(put)
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
}

func readOutput(t *testing.T, env *environment) string {
	t.Helper()
	result, err := env.invoke(workflowsGet.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The unchanged output of workflows.get sent to workflows.update restores every withheld value, path, and
// webhook id, with the same read, PUT, re-read requests as before.
func TestWorkflowsUpdateRestoresWithheldValuesFromItsRead(t *testing.T) {
	var calls []call
	var put map[string]any
	env := updateEnvironment(t, withheldNodes, &calls, &put)
	arguments := updateArgumentsFrom(t, readOutput(t, env), nil)
	calls = nil
	result, err := env.confirmed(workflowsUpdate.ID, "open", arguments)
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	assertNoCanary(t, "update result", result)
	if len(calls) != 3 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPut ||
		calls[2].method != http.MethodGet {
		t.Fatalf("calls = %+v, want read, one PUT, re-read", calls)
	}
	original := readNodes(t, withheldNodes)
	sent, _ := put["nodes"].([]any)
	if len(sent) != len(original) {
		t.Fatalf("PUT nodes = %v", put["nodes"])
	}
	for _, item := range sent {
		node := item.(map[string]any)
		want := original[node["id"].(string)]
		if !reflect.DeepEqual(node["parameters"], want["parameters"]) {
			t.Errorf("node %s sent %v, want %v", node["id"], node["parameters"], want["parameters"])
		}
		if node["webhookId"] != want["webhookId"] {
			t.Errorf("node %s sent webhookId %v, want %v", node["id"], node["webhookId"], want["webhookId"])
		}
	}
}

// A webhook path left out is kept from the read, so leaving it out never changes the trigger URL.
func TestWorkflowsUpdateKeepsAnOmittedWebhookPath(t *testing.T) {
	var calls []call
	var put map[string]any
	env := updateEnvironment(t, withheldNodes, &calls, &put)
	arguments := updateArgumentsFrom(t, readOutput(t, env), func(nodes []map[string]any) []map[string]any {
		for _, n := range nodes {
			switch n["id"] {
			case "w1":
				n["parameters"] = map[string]any{"httpMethod": "GET"}
			case "f1":
				n["parameters"] = map[string]any{"formTitle": "Contact"}
			case "m1":
				delete(n, "parameters")
			}
		}
		return nodes
	})
	if _, err := env.confirmed(workflowsUpdate.ID, "open", arguments); err != nil {
		t.Fatalf("update = %v", err)
	}
	want := map[string]string{
		"w1": `{"httpMethod":"GET","path":"hook-path-canary"}`,
		"f1": `{"formTitle":"Contact","options":{"path":"form-options-path-canary"},"path":"form-path-canary"}`,
		"m1": `{"path":"mcp-path-canary"}`,
	}
	for _, item := range put["nodes"].([]any) {
		node := item.(map[string]any)
		id := node["id"].(string)
		if expected, ok := want[id]; ok {
			if got, _ := json.Marshal(node["parameters"]); string(got) != expected {
				t.Errorf("node %s sent %s, want %s", id, got, expected)
			}
		}
	}
}

// A placeholder that cannot be resolved from the read is refused after the read, before any PUT, and the
// refusal never carries a withheld value.
func TestWorkflowsUpdateRefusesUnresolvablePlaceholdersBeforeThePUT(t *testing.T) {
	cases := map[string]func(nodes []map[string]any) []map[string]any{
		"new node": func(nodes []map[string]any) []map[string]any {
			return append(nodes, map[string]any{"id": "new", "name": "New", "type": "n8n-nodes-base.httpRequest",
				"parameters": map[string]any{"url": withheldPlaceholder}})
		},
		"changed type": func(nodes []map[string]any) []map[string]any {
			for _, n := range nodes {
				if n["id"] == "p1" {
					n["type"] = "n8n-nodes-base.ghost"
				}
			}
			return nodes
		},
		"renamed pair": func(nodes []map[string]any) []map[string]any {
			for _, n := range nodes {
				if n["id"] == "h1" {
					pair := n["parameters"].(map[string]any)["queryParameters"].(map[string]any)["parameters"].([]any)[0]
					pair.(map[string]any)["name"] = "page"
				}
			}
			return nodes
		},
		"placeholder inside text": func(nodes []map[string]any) []map[string]any {
			for _, n := range nodes {
				if n["id"] == "r1" {
					n["parameters"] = map[string]any{"path": "/data/" + withheldPlaceholder}
				}
			}
			return nodes
		},
		"parameters withheld on a new node": func(nodes []map[string]any) []map[string]any {
			return append(nodes, map[string]any{"id": "new", "name": "New", "type": "n8n-nodes-base.set",
				"parameters_withheld": true})
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []call
			var put map[string]any
			env := updateEnvironment(t, withheldNodes, &calls, &put)
			arguments := updateArgumentsFrom(t, readOutput(t, env), edit)
			calls = nil
			_, err := env.confirmed(workflowsUpdate.ID, "open", arguments)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			assertNoCanary(t, "error", err.Error())
			if len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("calls = %+v, want only the read, no PUT", calls)
			}
		})
	}
}

// workflows.create has no read to restore from, so a placeholder anywhere is refused before any request.
func TestWorkflowsCreateRefusesThePlaceholder(t *testing.T) {
	for name, node := range map[string]string{
		"nested": `"parameters":{"options":{"items":[{"secret":"` + withheldPlaceholder + `"}]}}`,
		"whole":  `"parameters_withheld":true`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			_, err := env.confirmed(workflowsCreate.ID, "open", `{"name":"W","nodes":[{"id":"1","name":"Set",`+
				`"type":"n8n-nodes-base.set",`+node+`}],"connections":{},"settings":{}}`)
			if !isInvalidRequest(err) || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, want a local refusal", err, calls)
			}
		})
	}
}

// More than the pointer limit withholds the parameters as a whole; parameters_withheld sent back keeps them.
func TestTooManyWithheldValuesWithholdTheParametersWhole(t *testing.T) {
	items := make([]string, 0, maxWithheldPointers+1)
	for i := 0; i <= maxWithheldPointers; i++ {
		items = append(items, fmt.Sprintf(`{"password":"canary-%d"}`, i))
	}
	nodes := `[{"id":"b1","name":"Bulk","type":"n8n-nodes-base.set","parameters":{"items":[` +
		strings.Join(items, ",") + `]}}]`
	var calls []call
	var put map[string]any
	env := updateEnvironment(t, nodes, &calls, &put)
	read := readOutput(t, env)
	assertNoCanary(t, "result", read)
	var detail WorkflowDetail
	_ = json.Unmarshal([]byte(read), &detail)
	if node := detail.Nodes[0]; node.Parameters != nil || !node.ParametersWithheld || node.WithheldParameters != nil {
		t.Fatalf("node = %+v, want parameters withheld whole", node)
	}
	if _, err := env.confirmed(workflowsUpdate.ID, "open", updateArgumentsFrom(t, read, nil)); err != nil {
		t.Fatalf("update = %v", err)
	}
	sent := put["nodes"].([]any)[0].(map[string]any)["parameters"]
	if !reflect.DeepEqual(sent, readNodes(t, nodes)["b1"]["parameters"]) {
		t.Fatalf("sent %v, want the read's parameters", sent)
	}

	// Exactly the limit is still listed.
	masked, pointers, all := maskParameters("n8n-nodes-base.set",
		json.RawMessage(`{"items":[`+strings.Join(items[:maxWithheldPointers], ",")+`]}`))
	if all || len(pointers) != maxWithheldPointers || strings.Contains(string(masked), "canary") {
		t.Fatalf("pointers = %d, all = %t", len(pointers), all)
	}
}

func TestWithheldPointersEscapeKeys(t *testing.T) {
	_, pointers, _ := maskParameters("x", json.RawMessage(`{"a/b~c":{"token":"t"}}`))
	if !reflect.DeepEqual(pointers, []string{"/a~1b~0c/token"}) {
		t.Fatalf("pointers = %v", pointers)
	}
}

func TestWorkflowToolsWithRawNodeParametersAreVersion2(t *testing.T) {
	for _, d := range registry(t).Provider(Provider) {
		switch d.ID {
		case workflowsGet.ID, workflowsCreate.ID, workflowsUpdate.ID, workflowsActivate.ID, workflowsDeactivate.ID:
			if d.Version != 2 {
				t.Errorf("%s version = %d, want 2", d.ID, d.Version)
			}
			if !strings.Contains(string(d.OutputSchema), `"withheld_parameters"`) ||
				!strings.Contains(string(d.OutputSchema), `"parameters_withheld"`) {
				t.Errorf("%s output schema lacks the withheld fields", d.ID)
			}
		}
	}
}

// Every member of a workflow detail and of its nodes is in the output schema, and every top-level member
// is in Fields; the node members are described by the nodes field.
func TestEveryWorkflowOutputMemberIsInSchemaAndFields(t *testing.T) {
	nodes := strings.Replace(withheldNodes, `"position":[0,0],`,
		`"position":[0,0],"disabled":true,"credentials":{"httpBasicAuth":{"id":"c1","name":"n"}},`, 1)
	var calls []call
	env := updateEnvironment(t, nodes, &calls, new(map[string]any))
	var out map[string]any
	if err := json.Unmarshal([]byte(readOutput(t, env)), &out); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal([]byte(workflowDetailSchema), &schema)
	fields := map[string]string{}
	for _, f := range workflowDetailFields {
		fields[f.Name] = f.Description
	}
	for key := range out {
		if _, ok := schema.Properties[key]; !ok || fields[key] == "" {
			t.Errorf("%s not in schema or fields", key)
		}
	}
	var node struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal([]byte(nodeSchema), &node)
	seen := map[string]bool{}
	for _, item := range out["nodes"].([]any) {
		for key := range item.(map[string]any) {
			seen[key] = true
			if _, ok := node.Properties[key]; !ok {
				t.Errorf("node member %s not in schema", key)
			}
		}
	}
	for _, key := range []string{"withheld_parameters", "parameters", "credentials"} {
		if !seen[key] {
			t.Errorf("rich answer lacks node member %s", key)
		}
	}
	for _, key := range []string{"withheld_parameters", "parameters_withheld"} {
		if !strings.Contains(fields["nodes"], key) {
			t.Errorf("nodes field does not describe %s", key)
		}
	}
}
