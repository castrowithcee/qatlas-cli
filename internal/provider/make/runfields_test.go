package makeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// runsEnv answers the scenario check and serves entry as both the listed and the single log entry.
func runsEnv(t *testing.T, calls *[]call, entry string) *environment {
	t.Helper()
	scenarioPath := apiPath + "/scenarios/" + strconv.FormatInt(ownScenario, 10)
	return newEnvironment(t, calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case scenarioPath:
			return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
		case scenarioPath + "/logs":
			return jsonResponse(200, `{"scenarioLogs":[`+entry+`]}`), nil
		case scenarioPath + "/logs/run-1":
			return jsonResponse(200, `{"scenarioLog":`+entry+`}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
}

func runOutputs(t *testing.T, entry string) (list, get map[string]any) {
	t.Helper()
	var calls []call
	env := runsEnv(t, &calls, entry)
	id := strconv.FormatInt(ownScenario, 10)
	out, err := env.invoke(runsList.ID, "open", `{"scenario_id":`+id+`}`)
	if err != nil {
		t.Fatalf("runs.list = %v", err)
	}
	var page struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Runs) != 1 {
		t.Fatalf("runs.list = %s, %v", out, err)
	}
	out, err = env.invoke(runsGet.ID, "open", `{"scenario_id":`+id+`,"execution_id":"run-1"}`)
	if err != nil {
		t.Fatalf("runs.get = %v", err)
	}
	if err := json.Unmarshal([]byte(out), &get); err != nil {
		t.Fatal(err)
	}
	return page.Runs[0], get
}

const runHead = `"id":"run-1","teamId":1001,"organizationId":501`

// The Make reference example fields arrive exactly, in both tools.
func TestRunsReadEveryDocumentedField(t *testing.T) {
	entry := `{` + runHead + `,"imtId":"imt-9","instant":true,"displayStatus":"WAITING","resumeAt":"2026-03-01T10:00:00Z",` +
		`"eventType":"EXECUTION_PAUSED","interruptReason":"sleep","interruptModuleId":12,"interruptModuleName":"Sleep",` +
		`"provenance":{"v":1,"channel":"mcp","auth":"api_token","services":["a","b"],"tokenLabel":"ci","oauthClientId":"cid",` +
		`"mcp":{"authMethod":"token","client":"cl","conversationId":"cv","entrypoint":"ep","tool":"tl","requestId":77},` +
		`"impersonated":true,"requestId":"rq"},` +
		`"detail":{"author":{"name":"Pat","staff":false},"extra":"x","provenance":{"v":1,"channel":"ui","auth":"session"},` +
		`"change":{"version":1,"kind":"label-assigned","origin":"cascade","label":{"id":4,"name":"L","colour":"x"},"more":1}}}`
	list, get := runOutputs(t, entry)
	want := map[string]any{
		"imt_id": "imt-9", "instant": true, "display_status": "WAITING", "resume_at": "2026-03-01T10:00:00Z",
		"interrupt_module_id": "12", "interrupt_module_name": "Sleep",
		"provenance": map[string]any{"v": float64(1), "channel": "mcp", "auth": "api_token",
			"services": []any{"a", "b"}, "token_label": "ci", "oauth_client_id": "cid",
			"mcp": map[string]any{"auth_method": "token", "client": "cl", "conversation_id": "cv",
				"entrypoint": "ep", "tool": "tl", "request_id": "77"},
			"impersonated": true, "request_id": "rq"},
		"detail": map[string]any{"author": map[string]any{"name": "Pat", "staff": false},
			"provenance": map[string]any{"v": float64(1), "channel": "ui", "auth": "session"},
			"change": map[string]any{"version": float64(1), "kind": "label-assigned", "origin": "cascade",
				"label": map[string]any{"id": float64(4), "name": "L"}}},
	}
	for name, got := range map[string]map[string]any{"list": list, "get": get} {
		for key, value := range want {
			if !reflect.DeepEqual(got[key], value) {
				t.Errorf("%s: %s = %#v, want %#v", name, key, got[key], value)
			}
		}
		if _, ok := got["status"]; ok {
			t.Errorf("%s: status present although Make sent none", name)
		}
	}
}

// A response without the new fields keeps the earlier output, and interruptModuleId reads the same as a
// number and as a string.
func TestRunsWithoutNewFieldsAndLooseInterruptID(t *testing.T) {
	for _, module := range []string{`12`, `"12"`} {
		entry := `{` + runHead + `,"status":2,"type":"execution","interruptModuleId":` + module + `}`
		list, get := runOutputs(t, entry)
		want := map[string]any{"id": "run-1", "scenario_id": float64(ownScenario), "team_id": float64(ownTeam),
			"organization_id": float64(501), "status": float64(2), "type": "execution", "interrupt_module_id": "12"}
		if !reflect.DeepEqual(list, want) || !reflect.DeepEqual(get, want) {
			t.Errorf("module %s: list = %v, get = %v, want %v", module, list, get, want)
		}
	}
}

func TestRunsChangeShapes(t *testing.T) {
	tests := []struct {
		name, change string
		want         map[string]any
	}{
		{"folder", `{"version":1,"kind":"folder-moved","origin":"direct","fromFolder":null,` +
			`"toFolder":{"id":3,"name":"N","path":"A/N","owner":"x"},"extra":1}`,
			map[string]any{"version": float64(1), "kind": "folder-moved", "origin": "direct",
				"to_folder": map[string]any{"id": float64(3), "name": "N", "path": "A/N"}}},
		{"unknown", `{"version":2,"kind":"blueprint-edited","origin":"direct","label":{"id":1,"name":"L"},` +
			`"fromFolder":{"id":1},"secret":"s"}`,
			map[string]any{"version": float64(2), "kind": "blueprint-edited", "origin": "direct"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, get := runOutputs(t, `{`+runHead+`,"detail":{"change":`+tt.change+`}}`)
			for _, got := range []map[string]any{list, get} {
				detail, _ := got["detail"].(map[string]any)
				if !reflect.DeepEqual(detail["change"], tt.want) {
					t.Errorf("change = %#v, want %#v", detail["change"], tt.want)
				}
			}
		})
	}
}

// Services are cut and flagged, and an identifier carrying the connection's own token is dropped.
func TestRunsProvenanceBoundsAndTokenGuard(t *testing.T) {
	var services []string
	for i := 0; i <= maxRunServices; i++ {
		services = append(services, fmt.Sprintf("%q", "s"+strconv.Itoa(i)))
	}
	list, _ := runOutputs(t, `{`+runHead+`,"provenance":{"v":1,"channel":"ui","auth":"x","tokenLabel":"`+apiTokenValue+`",`+
		`"services":[`+strings.Join(services, ",")+`]}}`)
	p, _ := list["provenance"].(map[string]any)
	if got, _ := p["services"].([]any); len(got) != maxRunServices || p["services_truncated"] != true {
		t.Errorf("services = %v, truncated = %v", p["services"], p["services_truncated"])
	}
	if _, ok := p["token_label"]; ok {
		t.Errorf("token_label carried the connection's token: %v", p)
	}
}

func TestRunsListFiltersReachTheRequest(t *testing.T) {
	var calls []call
	env := runsEnv(t, &calls, `{`+runHead+`}`)
	_, err := env.invoke(runsList.ID, "open", fmt.Sprintf(`{"scenario_id":%d,"display_status":"ERROR",`+
		`"duration_from":0,"duration_to":5,"operations_from":1,"operations_to":2,"transfer_from":3,"transfer_to":4,`+
		`"credits_from":5,"credits_to":6,"execution_name":"nightly","show_check_runs":false,"sort_dir":"desc"}`, ownScenario))
	if err != nil {
		t.Fatal(err)
	}
	query := calls[len(calls)-1].query
	for key, want := range map[string]string{"displayStatus": "ERROR", "durationFrom": "0", "durationTo": "5",
		"operationsFrom": "1", "operationsTo": "2", "transferFrom": "3", "transferTo": "4", "creditsFrom": "5",
		"creditsTo": "6", "executionName": "nightly", "showCheckRuns": "false", "pg[sortDir]": "desc"} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if query.Has("status") || query.Has("pg[showLast]") || query.Has("pg[last]") || query.Has("pg[sortBy]") {
		t.Errorf("query = %v, want no status and no last-based or sortBy paging", query)
	}
}

func TestRunsListRefusesInvalidFiltersBeforeAnyRequest(t *testing.T) {
	for _, filter := range []string{`"status":1,"display_status":"ERROR"`, `"display_status":"DONE"`,
		`"duration_from":5,"duration_to":4`, `"operations_from":2,"operations_to":1`,
		`"transfer_from":2,"transfer_to":1`, `"credits_from":2,"credits_to":1`, `"credits_from":-1`,
		`"execution_name":""`, `"execution_name":"  "`, `"execution_name":"` + strings.Repeat("x", 257) + `"`,
		`"sort_dir":"up"`, `"show_check_runs":"yes"`} {
		var calls []call
		env := runsEnv(t, &calls, `{`+runHead+`}`)
		_, err := env.invoke(runsList.ID, "open", fmt.Sprintf(`{"scenario_id":%d,%s}`, ownScenario, filter))
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, calls = %d, secret reads = %d, want a refusal before any request", filter, err, len(calls), *env.reads)
		}
	}
}
