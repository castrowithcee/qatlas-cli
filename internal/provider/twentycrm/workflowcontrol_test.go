package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const providerTextCn = "canary-provider-text-4410"

type controlCall struct{ Method, Path, Query, Body string }

// serveControl answers the version read with version and every mutation with mutate.
func serveControl(t *testing.T, version string, mutate func() (*http.Response, error)) *[]controlCall {
	t.Helper()
	calls := &[]controlCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := controlCall{Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		if request.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, version), nil
		}
		return mutate()
	})
	stubLimiter(t, cloudKey)
	return calls
}

func mutations(calls *[]controlCall) int {
	n := 0
	for _, call := range *calls {
		if call.Method == http.MethodPost {
			n++
		}
	}
	return n
}

func versionBody(id, trigger, steps string) string {
	return `{"data":{"workflowVersion":{"id":"` + id + `","workflowId":"` + wfID + `","name":"` + settingsCn +
		`","trigger":` + trigger + `,"steps":` + steps + `}}}`
}

func triggerOf(kind string) string {
	return `{"type":"` + kind + `","settings":{"token":"` + settingsCn + `"}}`
}

func stepOf(kind string) string {
	return `{"id":"s","name":"` + settingsCn + `","type":"` + kind + `","settings":{"url":"` + settingsCn + `"}}`
}

func okMutation(field string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"`+field+`":true}}`), nil
	}
}

func runControl(handler capability.Handler, args string) (any, error) {
	red := &redact.Redactor{}
	return handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args))
}

func activateArgs() string { return `{"workflow_version_id":"` + wfVersion + `"}` }
func runArgs() string      { return `{"workflow_run_id":"` + wfRun + `"}` }

func assertNoCanary(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, canary := range []string{settingsCn, providerTextCn, unknownCn} {
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: %q reached the error: %v", label, canary, err)
		}
	}
}

func TestActivateDecidesOnTriggerAndStepTypes(t *testing.T) {
	for _, trigger := range workflowTriggerTypes {
		want := trigger != "WEBHOOK"
		t.Run("trigger "+trigger, func(t *testing.T) {
			calls := serveControl(t, versionBody(wfVersion, triggerOf(trigger), `[`+stepOf("EMPTY")+`]`), okMutation("activateWorkflowVersion"))
			_, err := runControl(invokeWorkflowVersionsActivate, activateArgs())
			assertNoCanary(t, trigger, err)
			if (err == nil) != want || mutations(calls) != map[bool]int{true: 1, false: 0}[want] {
				t.Errorf("err = %v, mutations = %d, want allowed %v", err, mutations(calls), want)
			}
		})
	}
	for _, step := range workflowStepTypes {
		want := contains(activatableStepTypes, step)
		t.Run("step "+step, func(t *testing.T) {
			calls := serveControl(t, versionBody(wfVersion, triggerOf("MANUAL"), `[`+stepOf("EMPTY")+`,`+stepOf(step)+`]`),
				okMutation("activateWorkflowVersion"))
			_, err := runControl(invokeWorkflowVersionsActivate, activateArgs())
			assertNoCanary(t, step, err)
			if (err == nil) != want || mutations(calls) != map[bool]int{true: 1, false: 0}[want] {
				t.Errorf("err = %v, mutations = %d, want allowed %v", err, mutations(calls), want)
			}
			if !want && (err == nil || classOf(err) != "" || !asInvalidOK(err)) {
				t.Errorf("refusal = %v", err)
			}
		})
	}
	if len(workflowStepTypes) != 22 || len(workflowTriggerTypes) != 4 || len(activatableStepTypes) != 15 {
		t.Errorf("type lists changed: %d triggers, %d steps, %d internal", len(workflowTriggerTypes),
			len(workflowStepTypes), len(activatableStepTypes))
	}
	for _, internal := range activatableStepTypes {
		if !contains(workflowStepTypes, internal) {
			t.Errorf("%s is not a known step type", internal)
		}
	}
}

func TestActivateRefusesUnknownMissingAndUnreadableBeforeTheMutation(t *testing.T) {
	other := "44444444-0000-4000-8000-000000000004"
	for name, body := range map[string]string{
		"unknown trigger": versionBody(wfVersion, triggerOf(unknownCn), `[]`),
		"missing trigger": `{"data":{"workflowVersion":{"id":"` + wfVersion + `","steps":[]}}}`,
		"null trigger":    versionBody(wfVersion, `null`, `[]`),
		"string trigger":  versionBody(wfVersion, `"MANUAL"`, `[]`),
		"unknown step":    versionBody(wfVersion, triggerOf("MANUAL"), `[`+stepOf(unknownCn)+`]`),
		"typeless step":   versionBody(wfVersion, triggerOf("MANUAL"), `[{"name":"`+settingsCn+`"}]`),
		"lowercase step":  versionBody(wfVersion, triggerOf("MANUAL"), `[`+stepOf("empty")+`]`),
		"null steps":      versionBody(wfVersion, triggerOf("MANUAL"), `null`),
		"missing steps":   `{"data":{"workflowVersion":{"id":"` + wfVersion + `","trigger":` + triggerOf("MANUAL") + `}}}`,
		"object steps":    versionBody(wfVersion, triggerOf("MANUAL"), `{"a":1}`),
		"other version":   versionBody(other, triggerOf("MANUAL"), `[]`),
		"no version":      `{"data":{}}`,
	} {
		calls := serveControl(t, body, okMutation("activateWorkflowVersion"))
		_, err := runControl(invokeWorkflowVersionsActivate, activateArgs())
		assertNoCanary(t, name, err)
		if err == nil || mutations(calls) != 0 || len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
		}
	}
	calls := serveControl(t, versionBody(wfVersion, triggerOf("CRON"), `[]`), okMutation("activateWorkflowVersion"))
	if _, err := runControl(invokeWorkflowVersionsActivate, activateArgs()); err != nil || mutations(calls) != 1 {
		t.Errorf("a version without steps: err = %v, mutations = %d", err, mutations(calls))
	}
}

func TestActivateNamesOnlyTheRefusedFixedType(t *testing.T) {
	serveControl(t, versionBody(wfVersion, triggerOf("WEBHOOK"), `[]`), okMutation("activateWorkflowVersion"))
	_, err := runControl(invokeWorkflowVersionsActivate, activateArgs())
	if err == nil || !strings.Contains(err.Error(), "WEBHOOK") {
		t.Errorf("err = %v", err)
	}
	serveControl(t, versionBody(wfVersion, triggerOf("MANUAL"), `[`+stepOf(unknownCn)+`]`), okMutation("activateWorkflowVersion"))
	_, err = runControl(invokeWorkflowVersionsActivate, activateArgs())
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v", err)
	}
}

func TestControlSendsTheFixedDocumentAndVariablesOnce(t *testing.T) {
	runAnswer := func(field string) func() (*http.Response, error) {
		return func() (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"data":{"`+field+`":{"id":"`+wfRun+`","status":"STOPPING"}}}`), nil
		}
	}
	version := versionBody(wfVersion, triggerOf("DATABASE_EVENT"), `[`+stepOf("CREATE_RECORD")+`]`)
	for _, tt := range []struct {
		name     string
		handler  capability.Handler
		args     string
		mutate   func() (*http.Response, error)
		document string
		variable string
		value    string
		reads    int
	}{
		{"activate", invokeWorkflowVersionsActivate, activateArgs(), okMutation("activateWorkflowVersion"),
			"mutation ActivateWorkflowVersion($workflowVersionId: UUID!) { activateWorkflowVersion(workflowVersionId: $workflowVersionId) }",
			"workflowVersionId", wfVersion, 1},
		{"deactivate", invokeWorkflowVersionsDeactivate, activateArgs(), okMutation("deactivateWorkflowVersion"),
			"mutation DeactivateWorkflowVersion($workflowVersionId: UUID!) { deactivateWorkflowVersion(workflowVersionId: $workflowVersionId) }",
			"workflowVersionId", wfVersion, 0},
		{"stop", invokeWorkflowRunsStop, runArgs(), runAnswer("stopWorkflowRun"),
			"mutation StopWorkflowRun($workflowRunId: UUID!) { stopWorkflowRun(workflowRunId: $workflowRunId) { id status } }",
			"workflowRunId", wfRun, 0},
		{"retry", invokeWorkflowRunsRetry, runArgs(), runAnswer("retryWorkflowRun"),
			"mutation RetryWorkflowRun($workflowRunId: UUID!) { retryWorkflowRun(workflowRunId: $workflowRunId) { id status } }",
			"workflowRunId", wfRun, 0},
	} {
		calls := serveControl(t, version, tt.mutate)
		out, err := runControl(tt.handler, tt.args)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if len(*calls) != tt.reads+1 || mutations(calls) != 1 {
			t.Fatalf("%s: calls = %+v", tt.name, *calls)
		}
		if tt.reads == 1 {
			read := (*calls)[0]
			if read.Method != http.MethodGet || read.Path != workflowVersionsPath+"/"+wfVersion || read.Query != "depth=0" {
				t.Errorf("%s: read = %+v", tt.name, read)
			}
		}
		post := (*calls)[tt.reads]
		want, _ := json.Marshal(map[string]any{"query": tt.document, "variables": map[string]any{tt.variable: tt.value}})
		if post.Path != "/graphql" || post.Body != string(want) {
			t.Errorf("%s: request = %+v, want body %s", tt.name, post, want)
		}
		encoded, _ := json.Marshal(out)
		if tt.name == "stop" || tt.name == "retry" {
			if string(encoded) != `{"id":"`+wfRun+`","status":"STOPPING"}` {
				t.Errorf("%s: out = %s", tt.name, encoded)
			}
		}
	}
}

func TestControlRunStatusIsAKnownToken(t *testing.T) {
	serveControl(t, "", func() (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"stopWorkflowRun":{"id":"`+wfRun+`","status":"`+unknownCn+`"}}}`), nil
	})
	out, err := runControl(invokeWorkflowRunsStop, runArgs())
	encoded, _ := json.Marshal(out)
	if err != nil || strings.Contains(string(encoded), unknownCn) || !strings.Contains(string(encoded), `"status":"unknown"`) {
		t.Errorf("out = %s, err = %v", encoded, err)
	}
}

type controlTool struct {
	name    string
	handler capability.Handler
	args    string
	field   string
	version string
}

var controlTools = []controlTool{
	{"activate", invokeWorkflowVersionsActivate, activateArgs(), "activateWorkflowVersion",
		"{\"data\":{\"workflowVersion\":{\"id\":\"" + wfVersion + "\",\"trigger\":{\"type\":\"MANUAL\"},\"steps\":[]}}}"},
	{"deactivate", invokeWorkflowVersionsDeactivate, activateArgs(), "deactivateWorkflowVersion", ""},
	{"stop", invokeWorkflowRunsStop, runArgs(), "stopWorkflowRun", ""},
	{"retry", invokeWorkflowRunsRetry, runArgs(), "retryWorkflowRun", ""},
}

func TestControlNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	timeout := context.DeadlineExceeded
	for _, tool := range controlTools {
		for name, mutate := range map[string]func() (*http.Response, error){
			"5xx":        func() (*http.Response, error) { return jsonResponse(http.StatusBadGateway, providerTextCn), nil },
			"timeout":    func() (*http.Response, error) { return nil, timeout },
			"abort":      func() (*http.Response, error) { return nil, errors.New("connection reset " + providerTextCn) },
			"unreadable": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":`), nil },
			"empty data": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":null}`), nil },
		} {
			calls := serveControl(t, tool.version, mutate)
			_, err := runControl(tool.handler, tool.args)
			assertNoCanary(t, tool.name+" "+name, err)
			if err == nil || mutations(calls) != 1 || !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s %s: err = %v, mutations = %d", tool.name, name, err, mutations(calls))
			}
		}
	}
}

func TestControlTreatsGraphQLErrorsAndFalseAsFailures(t *testing.T) {
	for _, tool := range controlTools {
		errorsBody := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"BAD_USER_INPUT"}}]}`
		calls := serveControl(t, tool.version, func() (*http.Response, error) { return jsonResponse(http.StatusOK, errorsBody), nil })
		_, err := runControl(tool.handler, tool.args)
		assertNoCanary(t, tool.name, err)
		if err == nil || mutations(calls) != 1 {
			t.Errorf("%s errors at 200: err = %v", tool.name, err)
		}
		// Partial data next to errors is no success either.
		partial := `{"data":{"` + tool.field + `":true},"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		serveControl(t, tool.version, func() (*http.Response, error) { return jsonResponse(http.StatusOK, partial), nil })
		if _, err := runControl(tool.handler, tool.args); err == nil {
			t.Errorf("%s: errors next to data accepted", tool.name)
		}
	}
	for _, tool := range controlTools[:2] {
		for _, body := range []string{`{"data":{"` + tool.field + `":false}}`, `{"data":{"` + tool.field + `":null}}`, `{"data":{}}`} {
			serveControl(t, tool.version, func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
			if _, err := runControl(tool.handler, tool.args); err == nil {
				t.Errorf("%s accepted %s", tool.name, body)
			}
		}
	}
}

func TestControlRunIDMismatchIsAnInvalidResponse(t *testing.T) {
	other := "44444444-0000-4000-8000-000000000004"
	for _, tool := range controlTools[2:] {
		for _, body := range []string{`{"data":{"` + tool.field + `":{"id":"` + other + `","status":"STOPPED"}}}`,
			`{"data":{"` + tool.field + `":null}}`, `{"data":{"` + tool.field + `":{"status":"STOPPED"}}}`} {
			serveControl(t, "", func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
			_, err := runControl(tool.handler, tool.args)
			if classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("%s %s: err = %v", tool.name, body, err)
			}
		}
	}
}

func TestControlPermissionFailuresNameTheWorkflowsRight(t *testing.T) {
	forbidden := `{"data":null,"errors":[{"message":"` + providerTextCn + `","extensions":{"code":"FORBIDDEN"}}]}`
	for _, tool := range controlTools {
		for name, mutate := range map[string]func() (*http.Response, error){
			"403":       func() (*http.Response, error) { return jsonResponse(http.StatusForbidden, providerTextCn), nil },
			"FORBIDDEN": func() (*http.Response, error) { return jsonResponse(http.StatusOK, forbidden), nil },
		} {
			calls := serveControl(t, tool.version, mutate)
			_, err := runControl(tool.handler, tool.args)
			assertNoCanary(t, tool.name+" "+name, err)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Workflows") || mutations(calls) > 1 {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
		}
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, providerTextCn), nil
	})
	stubLimiter(t, cloudKey)
	_, err := runControl(invokeWorkflowVersionsActivate, activateArgs())
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Workflows") {
		t.Errorf("activate read 403: err = %v", err)
	}
}

func TestControlRefusesObjectTargetsAndBadIDsBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, tool := range controlTools {
		// A nil resolver would fail on any secret access.
		_, err := tool.handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(tool.args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s targets: err = %v", tool.name, err)
		}
		for _, bad := range []string{"not-a-uuid", wfVersion + ",x", ""} {
			args := strings.Replace(strings.Replace(tool.args, wfVersion, bad, 1), wfRun, bad, 1)
			if _, err := tool.handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args)); err == nil ||
				classOf(err) != "" {
				t.Errorf("%s id %q: err = %v", tool.name, bad, err)
			}
		}
	}
}

func TestControlDescriptorsDeclareRiskAndStayOutOfProfiles(t *testing.T) {
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{workflowVersionsActivate, capability.EffectUpdate, capability.IdempotencyIdempotent},
		{workflowVersionsDeactivate, capability.EffectUpdate, capability.IdempotencyIdempotent},
		{workflowRunsStop, capability.EffectExecute, capability.IdempotencyIdempotent},
		{workflowRunsRetry, capability.EffectExecute, capability.IdempotencyNonIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idem, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: "twentycrm-workflow-data"}
		if tt.d.Risk != want || !tt.d.RequiresToolAllowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
	}
	if !strings.Contains(workflowRunsRetry.Description, "again") || !strings.Contains(workflowRunsRetry.Description, "outside Twenty") {
		t.Errorf("retry description = %s", workflowRunsRetry.Description)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, Provider+".workflowversions.") || id == workflowRunsStop.ID || id == workflowRunsRetry.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestControlNeedsTheToolsListAndConfirmation(t *testing.T) {
	serveControl(t, controlTools[0].version, okMutation("activateWorkflowVersion"))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionUpdate, config.PermissionExecute}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	ids := []string{workflowVersionsActivate.ID, workflowVersionsDeactivate.ID, workflowRunsStop.ID, workflowRunsRetry.ID}
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: ids}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	for _, id := range ids {
		args := activateArgs()
		if strings.Contains(id, "workflowruns") {
			args = runArgs()
		}
		request := application.InvokeRequest{Operation: id, Connection: "crm", Arguments: json.RawMessage(args), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", id)
		}
		request = application.InvokeRequest{Operation: id, Connection: "crm-internal", Arguments: json.RawMessage(args)}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", id)
		}
	}
	request := application.InvokeRequest{Operation: workflowVersionsActivate.ID, Connection: "crm-internal",
		Arguments: json.RawMessage(activateArgs()), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err != nil {
		t.Errorf("confirmed activation on a connection that lists the tool: %v", err)
	}
}
