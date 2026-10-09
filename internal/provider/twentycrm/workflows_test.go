package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	wfID       = "11111111-0000-4000-8000-000000000001"
	wfVersion  = "22222222-0000-4000-8000-000000000002"
	wfRun      = "33333333-0000-4000-8000-000000000003"
	settingsCn = "canary-step-settings-5521"
	outputCn   = "canary-run-output-7710"
	errorCn    = "canary-run-error-3392"
	unknownCn  = "canary-unknown-type-9904"
)

const workflowPageBody = `{"data":{"workflows":[{"id":"` + wfID + `","name":"Onboarding","statuses":["ACTIVE","` +
	unknownCn + `"],"lastPublishedVersionId":"` + wfVersion + `","position":1,"createdAt":"2026-01-01T00:00:00.000Z",` +
	`"updatedAt":"2026-01-02T00:00:00.000Z","deletedAt":null}]},"totalCount":1,` +
	`"pageInfo":{"hasNextPage":true,"endCursor":"cursor1"}}`

const versionPageBody = `{"data":{"workflowVersions":[{"id":"` + wfVersion + `","workflowId":"` + wfID + `","name":"v1",` +
	`"status":"ACTIVE","trigger":{"type":"DATABASE_EVENT","settings":{"eventName":"person.created","token":"` + settingsCn + `"}},` +
	`"steps":[{"id":"a","name":"x","type":"CODE","valid":true,"settings":{"input":{"secret":"` + settingsCn + `"}}},` +
	`{"id":"b","name":"y","type":"` + unknownCn + `","settings":{"url":"` + settingsCn + `"}}],` +
	`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}]},` +
	`"pageInfo":{"hasNextPage":false,"endCursor":null}}`

const runPageBody = `{"data":{"workflowRuns":[{"id":"` + wfRun + `","workflowId":"` + wfID + `","workflowVersionId":"` + wfVersion +
	`","name":"Onboarding","status":"FAILED","output":{"error":"` + errorCn + `","flow":{"x":"` + outputCn + `"}},` +
	`"context":{"c":"` + outputCn + `"},"state":{"s":"` + outputCn + `"},"enqueuedAt":"2026-01-01T00:00:00.000Z",` +
	`"startedAt":"2026-01-01T00:00:01.000Z","endedAt":null,"createdAt":"2026-01-01T00:00:00.000Z"}]},` +
	`"pageInfo":{"hasNextPage":true,"endCursor":"cursor2"}}`

type wfCall struct{ Path, Query string }

func serveWorkflows(t *testing.T) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch {
		case request.URL.Path == workflowsPath:
			return jsonResponse(http.StatusOK, workflowPageBody), nil
		case request.URL.Path == workflowsPath+"/"+wfID:
			return jsonResponse(http.StatusOK, `{"data":{"workflow":`+
				strings.TrimSuffix(strings.TrimPrefix(workflowPageBody, `{"data":{"workflows":[`), `]},"totalCount":1,`+
					`"pageInfo":{"hasNextPage":true,"endCursor":"cursor1"}}`)+`}}`), nil
		case request.URL.Path == workflowVersionsPath:
			return jsonResponse(http.StatusOK, versionPageBody), nil
		case request.URL.Path == workflowRunsPath:
			return jsonResponse(http.StatusOK, runPageBody), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func runWorkflowTool(t *testing.T, handler capability.Handler, args string, targets ...string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	out, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, canary := range []string{settingsCn, outputCn, errorCn, unknownCn} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	return string(out), nil
}

func TestWorkflowsListProjectsAllowlistedFields(t *testing.T) {
	calls := serveWorkflows(t)
	out, err := runWorkflowTool(t, invokeWorkflowsList, `{"limit":10}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"Onboarding"`, `"statuses":["ACTIVE","unknown"]`, `"last_published_version_id":"` + wfVersion,
		`"has_more":true`, `"next_cursor"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if query.Get("limit") != "10" || query.Get("depth") != "0" || query.Get("filter") != "" || len(*calls) != 1 {
		t.Errorf("request = %+v", *calls)
	}
}

func TestWorkflowsGetSendsTheFixedFilterAndDropsSettings(t *testing.T) {
	calls := serveWorkflows(t)
	out, err := runWorkflowTool(t, invokeWorkflowsGet, `{"id":"`+wfID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"trigger_type":"DATABASE_EVENT"`, `"step_types":["CODE","unknown"]`, `"step_count":2`,
		`"status":"ACTIVE"`, `"has_more_versions":false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	if len(*calls) != 2 || (*calls)[0].Path != workflowsPath+"/"+wfID || (*calls)[1].Path != workflowVersionsPath {
		t.Fatalf("calls = %+v", *calls)
	}
	query, _ := url.ParseQuery((*calls)[1].Query)
	if query.Get("filter") != "workflowId[eq]:"+wfID || query.Get("limit") != "50" || query.Get("depth") != "0" {
		t.Errorf("versions query = %v", query)
	}
}

func TestWorkflowRunsListSendsTheFixedFilterAndDropsRunData(t *testing.T) {
	calls := serveWorkflows(t)
	out, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`","status":"FAILED"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"status":"FAILED"`, `"workflow_version_id":"` + wfVersion, `"started_at"`, `"next_cursor"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if (*calls)[0].Path != workflowRunsPath || query.Get("filter") != "workflowId[eq]:"+wfID+",status[eq]:FAILED" ||
		query.Get("fields") != runFields || query.Get("limit") != "25" {
		t.Errorf("request = %+v", *calls)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`"}`); err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery((*calls)[1].Query)
	if query.Get("filter") != "workflowId[eq]:"+wfID {
		t.Errorf("filter without status = %q", query.Get("filter"))
	}
}

func TestWorkflowToolsRefuseObjectTargetsBeforeSecretAndRequest(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for id, args := range map[string]string{
		"list": `{}`, "get": `{"id":"` + wfID + `"}`, "runs": `{"workflow_id":"` + wfID + `"}`,
	} {
		handler := map[string]capability.Handler{"list": invokeWorkflowsList, "get": invokeWorkflowsGet, "runs": invokeWorkflowRunsList}[id]
		// A nil resolver would fail on any secret access.
		_, err := handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s err = %v", id, err)
		}
	}
}

func TestWorkflowToolsRefuseBadArgumentsBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, test := range []struct {
		handler capability.Handler
		args    string
	}{
		{invokeWorkflowsGet, `{"id":"` + wfID + `,status[eq]:x"}`},
		{invokeWorkflowsGet, `{"id":"../etc"}`},
		{invokeWorkflowRunsList, `{"workflow_id":"not-a-uuid"}`},
		{invokeWorkflowRunsList, `{"workflow_id":"` + wfID + `","status":"failed"}`},
		{invokeWorkflowRunsList, `{"workflow_id":"` + wfID + `","status":"FAILED,id[eq]:x"}`},
		{invokeWorkflowRunsList, `{"workflow_id":"` + wfID + `","limit":101}`},
		{invokeWorkflowsList, `{"limit":101}`},
		{invokeWorkflowsList, `{"cursor":"AAAA"}`},
	} {
		if _, err := test.handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(test.args)); err == nil || classOf(err) != "" {
			t.Errorf("%s: err = %v", test.args, err)
		}
	}
}

func TestWorkflowRunCursorIsBoundToWorkflowAndStatus(t *testing.T) {
	serveWorkflows(t)
	out, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`","status":"FAILED"}`)
	if err != nil {
		t.Fatal(err)
	}
	var page WorkflowRunList
	if err := json.Unmarshal([]byte(out), &page); err != nil || page.NextCursor == "" {
		t.Fatal(out, err)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`","status":"FAILED","cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Errorf("same request: %v", err)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`","status":"COMPLETED","cursor":"`+page.NextCursor+`"}`); err == nil {
		t.Error("cursor of another status accepted")
	}
}

func TestWorkflowToolsRejectForeignVersionsAndRuns(t *testing.T) {
	other := "44444444-0000-4000-8000-000000000004"
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case workflowVersionsPath:
			return jsonResponse(http.StatusOK, strings.ReplaceAll(versionPageBody, `"workflowId":"`+wfID, `"workflowId":"`+other)), nil
		case workflowRunsPath:
			return jsonResponse(http.StatusOK, strings.ReplaceAll(runPageBody, `"workflowId":"`+wfID, `"workflowId":"`+other)), nil
		}
		return jsonResponse(http.StatusOK, `{"data":{"workflow":{"id":"`+wfID+`","name":"n"}}}`), nil
	})
	stubLimiter(t, cloudKey)
	if _, err := runWorkflowTool(t, invokeWorkflowsGet, `{"id":"`+wfID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("version err = %v", err)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("run err = %v", err)
	}
}

func TestGenericRecordToolsDoNotReachWorkflowObjects(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, object := range []string{"workflow", "workflowVersion", "workflowRun"} {
		if !systemObjects[object] || scopeAllows(object) {
			t.Errorf("%s is reachable", object)
		}
		if _, err := parseTarget("object/" + object); err == nil {
			t.Errorf("%s is a valid target", object)
		}
		for _, handler := range []capability.Handler{invokeRecordsList, invokeRecordsGet, invokeRecordsSearch, invokeRecordsGroupBy,
			invokeRecordsCreate, invokeRecordsUpdate, invokeRecordsDelete} {
			args := `{"object":"` + object + `","id":"` + wfID + `","fields":{"name":"x"},"group_by":["status"],"conditions":[{"field":"name","operator":"eq","value":"x"}]}`
			if _, err := handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args)); err == nil {
				t.Errorf("%s reachable through a generic tool", object)
			}
		}
	}
}

func scopeAllows(name string) bool { return scope{}.allows(name) }

func TestWorkflowToolsDeclareTheirRiskAndJoinTheReadProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{workflowsList, workflowsGet, workflowRunsList} {
		if d.Risk.DataSensitivity != "twentycrm-workflow-data" || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || d.Risk.Idempotency != capability.IdempotencySafe || !d.Risk.OpenWorld {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if !strings.Contains(strings.Join(metadata.Profiles[0].Tools, " "), d.ID) ||
			!strings.Contains(strings.Join(metadata.Profiles[1].Tools, " "), d.ID) {
			t.Errorf("%s missing from a profile", d.ID)
		}
	}
}
