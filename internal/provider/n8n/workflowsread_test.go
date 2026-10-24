package n8n

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const richNodeAttributes = `"notesInFlow":true,"notes":"Check this","executeOnce":false,"alwaysOutputData":true,` +
	`"retryOnFail":true,"maxTries":9007199254740993,"waitBetweenTries":5000,"onError":"continueRegularOutput",` +
	`"continueOnFail":true,"customTelemetryTags":{"tag":[{"key":"owner","value":"ops"}]},` +
	`"createdAt":"2026-01-03T00:00:00.000Z","updatedAt":"2026-01-04T00:00:00.000Z"`

// manyWithheld is a node with more withheld values than are listed, so it reports parameters_withheld.
var manyWithheld = `{"id":"m1","name":"Many","type":"n8n-nodes-base.wordpress","typeVersion":1,"position":[0,200],` +
	`"parameters":{"items":[` + strings.TrimSuffix(strings.Repeat(`{"password":"x"},`, maxWithheldPointers+1), ",") + `]}}`

var richReadNodes = `[` + manyWithheld + `,` +
	`{"id":"w1","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],` +
	`"webhookId":"webhook-id-canary","disabled":false,"parameters":{"path":"hook-path-canary"},` + richNodeAttributes + `},` +
	`{"id":"h1","name":"HTTP","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[200,0],"disabled":true,` +
	`"parameters":{"url":"https://user:pw-canary@example.invalid/v1"},` +
	`"credentials":{"httpBasicAuth":{"id":"c1","name":"prod"}},` + richNodeAttributes + `}]`

const foreignRichProject = `{"id":"PROJECT_FOREIGN_0002B","name":"foreign-name-canary","type":"team",` +
	`"icon":null,"description":"foreign-description-canary","customTelemetryTags":[],"creatorId":"u9",` +
	`"createdAt":"2026-02-01T00:00:00.000Z","updatedAt":"2026-02-02T00:00:00.000Z"}`

// richWorkflow carries every non-deprecated member of n8n's workflowPublicDto, a share into the own project
// and one into a foreign project, and a workflow-level and active-version-level copy of the nodes.
func richWorkflow() string {
	return `{"id":"` + ownWorkflow + `","name":"Rich","description":"What it does.","active":true,` +
		`"activeVersionId":"av1","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z",` +
		`"isArchived":false,"versionId":"v2","versionCounter":9007199254740995,"sourceWorkflowId":"SRC1","triggerCount":1,` +
		`"nodes":` + richReadNodes + `,"connections":{"Webhook":{"main":[[{"node":"HTTP","type":"main","index":0}]]}},` +
		`"nodeGroups":[{"id":"g1","name":"Group","description":"Frame","nodeIds":["w1","h1"]}],` +
		`"settings":{"saveExecutionProgress":true,"saveManualExecutions":"DEFAULT","saveDataErrorExecution":"all",` +
		`"saveDataSuccessExecution":"none","executionTimeout":3600,"errorWorkflow":"EW1","timezone":"Europe/Berlin",` +
		`"executionOrder":"v1","binaryMode":"separate","callerPolicy":"workflowsFromAList","callerIds":"14, 18",` +
		`"timeSavedMode":"fixed","timeSavedPerExecution":2.5,"redactionPolicy":"all","availableInMCP":false,` +
		`"customTelemetryTags":[{"key":"team","value":"ops"}],"credentialResolverId":"cr1"},` +
		`"staticData":{"lastId":"static-canary"},"pinData":{"Webhook":[{"json":{"a":"pin-canary"}}]},` +
		`"meta":{"onboardingId":"ob1","templateId":"t1","instanceId":"i1","templateCredsSetupCompleted":true},` +
		`"tags":[{"id":"t1","name":"billing","createdAt":"2026-01-05T00:00:00.000Z","updatedAt":"2026-01-06T00:00:00.000Z"}],` +
		`"shared":[{"role":"workflow:owner","workflowId":"` + ownWorkflow + `","projectId":"` + ownProject + `",` +
		`"createdAt":"2026-01-07T00:00:00.000Z","updatedAt":"2026-01-08T00:00:00.000Z","project":` + richProject + `},` +
		`{"role":"workflow:editor","workflowId":"` + ownWorkflow + `","projectId":"PROJECT_FOREIGN_0002B",` +
		`"createdAt":"2026-01-07T00:00:00.000Z","updatedAt":"2026-01-08T00:00:00.000Z","project":` + foreignRichProject + `}],` +
		`"activeVersion":{"versionId":"av1","workflowId":"` + ownWorkflow + `","nodes":` + richReadNodes + `,` +
		`"connections":{"HTTP":{"main":[[{"node":"Webhook","type":"main","index":0}]]}},"nodeGroups":[{"id":"g1","name":"Group","nodeIds":["w1"]}],` +
		`"authors":"Ada Lovelace","name":"Release 1","description":"First","autosaved":false,` +
		`"createdAt":"2026-01-09T00:00:00.000Z","updatedAt":"2026-01-10T00:00:00.000Z",` +
		`"workflowPublishHistory":[{"id":9007199254740997,"workflowId":"` + ownWorkflow + `","versionId":"av1",` +
		`"event":"activated","userId":"u1","createdAt":"2026-01-11T00:00:00.000Z"}]}}`
}

func readRichGet(t *testing.T, connection string) (string, map[string]any) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, richWorkflow()), nil
	})
	out, err := env.invoke(workflowsGet.ID, connection, fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want one read", calls)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		t.Fatal(err)
	}
	return out, value
}

// at returns the value at path (object keys and array indexes) of a decoded answer, re-encoded canonically.
func at(t *testing.T, value any, path ...any) string {
	t.Helper()
	for _, step := range path {
		switch typed := step.(type) {
		case string:
			value = value.(map[string]any)[typed]
		case int:
			value = value.([]any)[typed]
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func decodeExact(t *testing.T, text string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWorkflowsGetReportsEveryDocumentedMemberExactly(t *testing.T) {
	out, _ := readRichGet(t, "open")
	value := decodeExact(t, out)
	canonical := func(text string) string { return at(t, decodeExact(t, text)) }
	for _, tt := range []struct {
		path []any
		want string
	}{
		{[]any{"description"}, `"What it does."`}, {[]any{"active_version_id"}, `"av1"`},
		{[]any{"version_id"}, `"v2"`}, {[]any{"version_counter"}, `9007199254740995`},
		{[]any{"source_workflow_id"}, `"SRC1"`},
		{[]any{"node_groups"}, `[{"id":"g1","name":"Group","description":"Frame","node_ids":["w1","h1"]}]`},
		{[]any{"settings"}, `{"save_execution_progress":true,"save_manual_executions":"DEFAULT",` +
			`"save_data_error_execution":"all","save_data_success_execution":"none","execution_timeout":3600,` +
			`"error_workflow":"EW1","timezone":"Europe/Berlin","execution_order":"v1","binary_mode":"separate",` +
			`"caller_policy":"workflowsFromAList","caller_ids":"14, 18","time_saved_mode":"fixed",` +
			`"time_saved_per_execution":2.5,"redaction_policy":"all","available_in_mcp":false,` +
			`"custom_telemetry_tags":[{"key":"team","value":"ops"}],"credential_resolver_id":"cr1"}`},
		{[]any{"meta"}, `{"onboarding_id":"ob1","template_id":"t1","instance_id":"i1","template_creds_setup_completed":true}`},
		{[]any{"tags"}, `[{"id":"t1","name":"billing","created_at":"2026-01-05T00:00:00.000Z","updated_at":"2026-01-06T00:00:00.000Z"}]`},
		{[]any{"connections"}, `{"Webhook":{"main":[[{"node":"HTTP","type":"main","index":0}]]}}`},
		{[]any{"project_ids"}, `["` + ownProject + `","PROJECT_FOREIGN_0002B"]`},
		{[]any{"nodes", 2}, `{"id":"h1","name":"HTTP","type":"n8n-nodes-base.httpRequest","type_version":4.2,` +
			`"position":[200,0],"disabled":true,"parameters":{"url":"` + withheldPlaceholder + `"},` +
			`"withheld_parameters":["/url"],"credentials":{"httpBasicAuth":{"id":"c1","name":"prod"}},` +
			`"notes_in_flow":true,"notes":"Check this","execute_once":false,"always_output_data":true,` +
			`"retry_on_fail":true,"max_tries":9007199254740993,"wait_between_tries":5000,` +
			`"on_error":"continueRegularOutput","custom_telemetry_tags":{"tag":[{"key":"owner","value":"ops"}]},` +
			`"created_at":"2026-01-03T00:00:00.000Z","updated_at":"2026-01-04T00:00:00.000Z"}`},
		{[]any{"nodes", 0, "parameters_withheld"}, `true`},
		{[]any{"active_version", "nodes", 2}, at(t, value, "nodes", 2)},
		{[]any{"active_version", "version_id"}, `"av1"`}, {[]any{"active_version", "workflow_id"}, `"` + ownWorkflow + `"`},
		{[]any{"active_version", "connections"}, `{"HTTP":{"main":[[{"node":"Webhook","type":"main","index":0}]]}}`},
		{[]any{"active_version", "node_groups"}, `[{"id":"g1","name":"Group","node_ids":["w1"]}]`},
		{[]any{"active_version", "authors"}, `"Ada Lovelace"`}, {[]any{"active_version", "name"}, `"Release 1"`},
		{[]any{"active_version", "description"}, `"First"`}, {[]any{"active_version", "autosaved"}, `false`},
		{[]any{"active_version", "created_at"}, `"2026-01-09T00:00:00.000Z"`},
		{[]any{"active_version", "updated_at"}, `"2026-01-10T00:00:00.000Z"`},
		{[]any{"active_version", "workflow_publish_history"}, `[{"id":9007199254740997,"workflow_id":"` + ownWorkflow +
			`","version_id":"av1","event":"activated","user_id":"u1","created_at":"2026-01-11T00:00:00.000Z"}]`},
		{[]any{"shared", 0, "role"}, `"workflow:owner"`}, {[]any{"shared", 0, "workflow_id"}, `"` + ownWorkflow + `"`},
		{[]any{"shared", 0, "project_id"}, `"` + ownProject + `"`},
		{[]any{"shared", 0, "created_at"}, `"2026-01-07T00:00:00.000Z"`},
		{[]any{"shared", 0, "updated_at"}, `"2026-01-08T00:00:00.000Z"`},
		{[]any{"shared", 0, "project", "name"}, `"Marketing"`}, {[]any{"shared", 0, "project", "type"}, `"team"`},
		{[]any{"shared", 0, "project", "creator_id"}, `"f9a2cbb8-0b1e-4b64-9c1c-0d5f5f1f2a3b"`},
		{[]any{"shared", 1, "role"}, `"workflow:editor"`},
	} {
		if got, want := at(t, value, tt.path...), canonical(tt.want); got != want {
			t.Errorf("%v = %s, want %s", tt.path, got, want)
		}
	}
}

func TestWorkflowsReadNeverOutputsDeprecatedOrWithheldValues(t *testing.T) {
	out, _ := readRichGet(t, "open")
	for _, banned := range []string{"continue_on_fail", "continueOnFail", "webhookId", "webhook_id", "hook-path-canary",
		"pw-canary", "webhook-id-canary", "static-canary", "pin-canary", "static_data", "pin_data"} {
		if strings.Contains(out, banned) {
			t.Errorf("get output contains %q: %s", banned, out)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, workflowsPageJSONOf(richWorkflow())), nil
	})
	list, err := env.invoke(workflowsList.ID, "open", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"continue_on_fail", "continueOnFail", "webhookId", "webhook_id", "hook-path-canary",
		"pw-canary", "webhook-id-canary", "static-canary", "pin-canary", "static_data", "pin_data"} {
		if strings.Contains(list, banned) {
			t.Errorf("list output contains %q: %s", banned, list)
		}
	}
	// The list reports each workflow as get does.
	if got, want := at(t, decodeExact(t, list), "workflows", 0), at(t, decodeExact(t, out)); got != want {
		t.Errorf("list entry differs from get:\n%s\n%s", got, want)
	}
}

func TestSharedProjectOutsideAllowListIsReportedByIdentifierOnly(t *testing.T) {
	for _, tool := range []struct{ id, args string }{
		{workflowsGet.ID, fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow)}, {workflowsList.ID, `{}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if tool.id == workflowsList.ID {
				return jsonResponse(200, workflowsPageJSONOf(richWorkflow())), nil
			}
			return jsonResponse(200, richWorkflow()), nil
		})
		out, err := env.invoke(tool.id, "project", tool.args)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if strings.Contains(out, "foreign-name-canary") || strings.Contains(out, "foreign-description-canary") {
			t.Errorf("%s leaked a foreign project: %s", tool.id, out)
		}
		value := decodeExact(t, out)
		if tool.id == workflowsList.ID {
			value = decodeExact(t, at(t, value, "workflows", 0))
		}
		if got := at(t, value, "shared", 1, "project"); got != `{"id":"PROJECT_FOREIGN_0002B"}` {
			t.Errorf("%s: foreign project = %s, want its id only", tool.id, got)
		}
		if got := at(t, value, "shared", 0, "project", "name"); got != `"Marketing"` {
			t.Errorf("%s: own project name = %s, want the full project", tool.id, got)
		}
	}
}

func TestWorkflowsReadOmitsMissingMembersAndWriteViewIsUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", ownProject)), nil
	})
	out, err := env.invoke(workflowsGet.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"active":true,"created_at":"2026-01-01T00:00:00.000Z","id":"` + ownWorkflow +
		`","is_archived":false,"name":"Own","node_count":1,"nodes":[{"credentials":{"httpBasicAuth":{"id":"cred1",` +
		`"name":"prod-api"}},"disabled":false,"id":"n1","name":"Start","parameters":{"note":"hello world"},` +
		`"position":[0,0],"type":"n8n-nodes-base.start","type_version":1}],"project_ids":["` + ownProject + `"],` +
		`"shared":[{"project_id":"` + ownProject + `","role":"workflow:owner"}],` +
		`"tags":[{"id":"t1","name":"billing"}],"trigger_count":1,"updated_at":"2026-01-02T00:00:00.000Z"}`
	var got, expected any
	_ = json.Unmarshal([]byte(out), &got)
	_ = json.Unmarshal([]byte(want), &expected)
	if fmt.Sprint(got) != fmt.Sprint(expected) {
		t.Errorf("bare output changed:\n%s\n%s", out, want)
	}

	detail, err := json.Marshal(detailOf(workflowJSON{ID: "1", Name: "n", Description: "d", VersionID: "v",
		Tags: []tagJSON{{ID: "t", Name: "n", CreatedAt: "c"}}, Nodes: []nodeJSON{{ID: "n", Name: "n", Type: "t", Notes: "x"}}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"description", "version_id", "notes", "shared", "settings"} {
		if strings.Contains(string(detail), banned) {
			t.Errorf("write view gained %q: %s", banned, detail)
		}
	}
}

// wantAttributes is what a node carrying richNodeAttributes must send: the writable execution attributes in
// n8n's camelCase, exact numbers, and neither the deprecated continueOnFail nor the read-only timestamps.
var wantAttributes = map[string]any{
	"notesInFlow": true, "notes": "Check this", "executeOnce": false, "alwaysOutputData": true,
	"retryOnFail": true, "maxTries": json.Number("9007199254740993"), "waitBetweenTries": json.Number("5000"),
	"onError":             "continueRegularOutput",
	"customTelemetryTags": map[string]any{"tag": []any{map[string]any{"key": "owner", "value": "ops"}}},
}

func decodeRaw(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	body, _ := decodeExact(t, string(raw)).(map[string]any)
	return body
}

func assertAttributes(t *testing.T, node map[string]any) {
	t.Helper()
	for key, want := range wantAttributes {
		if !reflect.DeepEqual(node[key], want) {
			t.Errorf("node %v %s = %#v, want %#v", node["id"], key, node[key], want)
		}
	}
	for _, key := range []string{"created_at", "updated_at", "createdAt", "updatedAt", "continueOnFail",
		"notes_in_flow", "max_tries", "custom_telemetry_tags"} {
		if _, ok := node[key]; ok {
			t.Errorf("node %v carries %s", node["id"], key)
		}
	}
}

// A workflow whose nodes carry the execution attributes goes from get into update with those attributes sent
// exactly, large numbers included; the withheld values and webhook ids are still restored.
func TestWorkflowsUpdateRoundTripsReadNodeAttributes(t *testing.T) {
	nodes := strings.Replace(withheldNodes, `"position":[0,0],`, `"position":[0,0],`+richNodeAttributes+`,`, 1)
	var calls []call
	var rawPut []byte
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodGet:
			return jsonResponse(200, withheldWorkflowJSON(ownWorkflow, nodes)), nil
		case http.MethodPut:
			rawPut, _ = io.ReadAll(r.Body)
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request %s", r.Method)
		return nil, nil
	})
	read := readOutput(t, env)
	var got struct {
		Name        string            `json:"name"`
		Nodes       []json.RawMessage `json:"nodes"`
		Connections json.RawMessage   `json:"connections"`
	}
	if err := json.Unmarshal([]byte(read), &got); err != nil || !strings.Contains(read, `"max_tries":9007199254740993`) {
		t.Fatalf("get did not report the attributes: %s, %v", read, err)
	}
	if len(got.Connections) == 0 {
		got.Connections = json.RawMessage(`{}`)
	}
	arguments, _ := json.Marshal(map[string]any{"workflow_id": ownWorkflow, "name": got.Name, "nodes": got.Nodes,
		"connections": got.Connections, "settings": map[string]any{}})
	calls = nil
	if _, err := env.confirmed(workflowsUpdate.ID, "open", string(arguments)); err != nil {
		t.Fatalf("update = %v", err)
	}
	if len(calls) != 3 || calls[1].method != http.MethodPut {
		t.Fatalf("calls = %+v, want read, one PUT, re-read", calls)
	}
	original := map[string]map[string]any{}
	list, _ := decodeExact(t, nodes).([]any)
	for _, item := range list {
		node := item.(map[string]any)
		original[node["id"].(string)] = node
	}
	sent, _ := decodeRaw(t, rawPut)["nodes"].([]any)
	if len(sent) != len(original) {
		t.Fatalf("PUT nodes = %v", sent)
	}
	for _, item := range sent {
		node := item.(map[string]any)
		if node["id"] == "w1" {
			assertAttributes(t, node)
		}
		want := original[node["id"].(string)]
		if !reflect.DeepEqual(node["parameters"], want["parameters"]) || node["webhookId"] != want["webhookId"] {
			t.Errorf("node %s sent %v / %v, want %v / %v", node["id"], node["parameters"], node["webhookId"],
				want["parameters"], want["webhookId"])
		}
	}
}

// Create sends the same node attributes and settings as update, and the version of both tools is 3.
func TestWorkflowsCreateSendsNodeAttributesAndWritableSettings(t *testing.T) {
	var calls []call
	var rawPost []byte
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			rawPost, _ = io.ReadAll(r.Body)
			return jsonResponse(201, `{"id":"`+newWorkflow+`"}`), nil
		}
		return jsonResponse(200, workflowJSONOf(newWorkflow, "My workflow", ownProject)), nil
	})
	node := `{"id":"n1","name":"Set","type":"n8n-nodes-base.set","notes_in_flow":true,"notes":"Check this",` +
		`"execute_once":false,"always_output_data":true,"retry_on_fail":true,"max_tries":9007199254740993,` +
		`"wait_between_tries":5000,"on_error":"continueRegularOutput",` +
		`"custom_telemetry_tags":{"tag":[{"key":"owner","value":"ops"}]},` +
		`"created_at":"2026-01-03T00:00:00.000Z","updated_at":"2026-01-04T00:00:00.000Z"}`
	settings := `{"save_execution_progress":"DEFAULT","save_manual_executions":false,` +
		`"save_data_error_execution":"DEFAULT","save_data_success_execution":"none","binary_mode":"separate",` +
		`"credential_resolver_id":"cr1","custom_telemetry_tags":[{"key":"team","value":"ops"}]}`
	arguments := `{"name":"My workflow","nodes":[` + node + `],"connections":{},"settings":` + settings + `}`
	if _, err := env.confirmed(workflowsCreate.ID, "open", arguments); err != nil {
		t.Fatalf("create = %v", err)
	}
	body := decodeRaw(t, rawPost)
	sent, _ := body["nodes"].([]any)
	if len(sent) != 1 {
		t.Fatalf("POST nodes = %v", body["nodes"])
	}
	assertAttributes(t, sent[0].(map[string]any))
	wantSettings := map[string]any{"saveManualExecutions": false, "saveDataSuccessExecution": "none",
		"customTelemetryTags": []any{map[string]any{"key": "team", "value": "ops"}}}
	if !reflect.DeepEqual(body["settings"], wantSettings) {
		t.Errorf("POST settings = %#v, want %#v", body["settings"], wantSettings)
	}
}

func TestWorkflowCreateAndUpdateAreVersion3(t *testing.T) {
	if workflowsCreate.Version != 3 || workflowsUpdate.Version != 3 {
		t.Errorf("versions = %d, %d, want 3", workflowsCreate.Version, workflowsUpdate.Version)
	}
}

// The name, nodes, connections, and settings of a complete get answer go into update unchanged. The three
// settings that n8n ignores stay out of the PUT body, DEFAULT is dropped, and the telemetry tags are sent.
func TestWorkflowsUpdateRoundTripsReadSettings(t *testing.T) {
	settings := `{"saveExecutionProgress":"DEFAULT","saveManualExecutions":false,"saveDataErrorExecution":"all",` +
		`"saveDataSuccessExecution":"DEFAULT","executionTimeout":3600,"errorWorkflow":"EW1","timezone":"Europe/Berlin",` +
		`"executionOrder":"v1","binaryMode":"separate","callerPolicy":"workflowsFromAList","callerIds":"14, 18",` +
		`"timeSavedMode":"fixed","timeSavedPerExecution":2.5,"redactionPolicy":"all","availableInMCP":true,` +
		`"customTelemetryTags":[{"key":"team","value":"ops"}],"credentialResolverId":"cr1"}`
	body := strings.Replace(withheldWorkflowJSON(ownWorkflow, withheldNodes), `"tags":[]`, `"tags":[],"settings":`+settings, 1)
	var calls []call
	var put map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodGet:
			return jsonResponse(200, body), nil
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&put)
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request %s", r.Method)
		return nil, nil
	})
	read := readOutput(t, env)
	var got struct {
		Name        string            `json:"name"`
		Nodes       []json.RawMessage `json:"nodes"`
		Connections json.RawMessage   `json:"connections"`
		Settings    json.RawMessage   `json:"settings"`
	}
	if err := json.Unmarshal([]byte(read), &got); err != nil || len(got.Settings) == 0 {
		t.Fatalf("get = %s, %v", read, err)
	}
	if connections := got.Connections; len(connections) == 0 {
		got.Connections = json.RawMessage(`{}`)
	}
	arguments, _ := json.Marshal(map[string]any{"workflow_id": ownWorkflow, "name": got.Name, "nodes": got.Nodes,
		"connections": got.Connections, "settings": got.Settings})
	calls = nil
	if _, err := env.confirmed(workflowsUpdate.ID, "open", string(arguments)); err != nil {
		t.Fatalf("update = %v", err)
	}
	if len(calls) != 3 || calls[1].method != http.MethodPut {
		t.Fatalf("calls = %+v, want read, one PUT, re-read", calls)
	}
	want := map[string]any{"saveManualExecutions": false, "saveDataErrorExecution": "all",
		"executionTimeout": 3600.0, "errorWorkflow": "EW1",
		"timezone": "Europe/Berlin", "executionOrder": "v1", "callerPolicy": "workflowsFromAList",
		"callerIds": "14, 18", "timeSavedMode": "fixed", "timeSavedPerExecution": 2.5, "redactionPolicy": "all",
		"availableInMCP": true, "customTelemetryTags": []any{map[string]any{"key": "team", "value": "ops"}}}
	if !reflect.DeepEqual(put["settings"], want) {
		t.Errorf("PUT settings = %v, want %v", put["settings"], want)
	}
}
