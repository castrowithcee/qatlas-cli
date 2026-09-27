package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const newWorkflow = "WORKFLOW_NEW_0003CCCC"

// minimalCreateArguments is the smallest valid workflows.create body: one node, no wiring, default settings.
func minimalCreateArguments(extra string) string {
	body := `{"name":"My workflow","nodes":[{"id":"1","name":"Manual Trigger",` +
		`"type":"n8n-nodes-base.manualTrigger"}],"connections":{},"settings":{}`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

// Every one of this provider's six change tools is refused before any request when it is not confirmed, and
// no secret is ever read for it either.
func TestChangeToolsRequireConfirmationAndSendNoRequestWithoutIt(t *testing.T) {
	cases := []struct {
		operation, arguments string
	}{
		{workflowsCreate.ID, minimalCreateArguments("")},
		{workflowsUpdate.ID, fmt.Sprintf(`{"workflow_id":%q,%s`, ownWorkflow, minimalCreateArguments("")[1:])},
		{workflowsActivate.ID, fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow)},
		{workflowsDeactivate.ID, fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow)},
		{executionsRetry.ID, `{"execution_id":1}`},
		{executionsStop.ID, `{"execution_id":1}`},
	}
	for _, tt := range cases {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			_, err := env.invoke(tt.operation, "open", tt.arguments)
			if !isConfirmationRequired(err) {
				t.Fatalf("err = %v, want a confirmation-required refusal", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none without confirmation", calls, *env.reads)
			}
		})
	}
}

// workflows.create is refused locally, before any request, when this connection's own configuration
// already decides against it: a workflow allow-list can never admit a workflow that does not exist yet, a
// project-restricted connection needs an explicit project_id, and that project_id must itself be allowed.
func TestWorkflowsCreateRefusesOutOfScopeConnectionsLocally(t *testing.T) {
	noRequest := func(t *testing.T) func(*http.Request) (*http.Response, error) {
		return func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	}
	cases := []struct {
		name, connection, arguments string
	}{
		{"workflow allow-list forbids create", "workflow", minimalCreateArguments("")},
		{"project restriction without project_id", "project", minimalCreateArguments("")},
		{"project_id outside the allow-list", "project",
			minimalCreateArguments(fmt.Sprintf(`"project_id":%q`, foreignProject))},
		{"both allow-lists still forbid create", "both", minimalCreateArguments("")},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(workflowsCreate.ID, tt.connection, tt.arguments)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if len(calls) != 0 {
				t.Fatalf("calls = %+v, want none before the restriction was refused", calls)
			}
		})
	}
}

// A successful create sends exactly one POST, then re-reads the new workflow to answer with the same
// scope-checked detail workflows.get would, and, on a project-restricted connection, to confirm n8n actually
// placed it inside the requested, allowed project.
func TestWorkflowsCreateSucceedsAndVerifiesPlacement(t *testing.T) {
	var calls []call
	var sentBody map[string]any
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/workflows":
			_ = json.NewDecoder(r.Body).Decode(&sentBody)
			return jsonResponse(201, fmt.Sprintf(`{"id":%q}`, newWorkflow)), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/workflows/"+newWorkflow:
			return jsonResponse(200, workflowJSONOf(newWorkflow, "My workflow", ownProject)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(workflowsCreate.ID, "project",
		minimalCreateArguments(fmt.Sprintf(`"project_id":%q`, ownProject)))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail WorkflowDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail.ID != newWorkflow {
		t.Fatalf("result = %s, %v, want the re-read new workflow", result, err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodPost || calls[1].method != http.MethodGet {
		t.Fatalf("calls = %+v, want exactly one POST then one GET", calls)
	}
	if sentBody["projectId"] != ownProject {
		t.Fatalf("sent body = %+v, want projectId forwarded", sentBody)
	}
	if _, ok := sentBody["id"]; ok {
		t.Fatalf("sent body = %+v, want no readOnly field of the workflow forwarded", sentBody)
	}
}

// When n8n does not place a newly created workflow inside the project this connection requested and
// allows, the deviation is reported as a provider error, never as an invalid request: the one changing POST
// has already reached n8n and cannot be undone by a tool this milestone does not offer.
func TestWorkflowsCreateReportsMisplacementAsAProviderErrorAfterTheRequestAlreadySent(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/workflows":
			return jsonResponse(201, fmt.Sprintf(`{"id":%q}`, newWorkflow)), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/workflows/"+newWorkflow:
			// n8n reports the new workflow inside a project this connection never allowed, despite the
			// requested, allowed project_id.
			return jsonResponse(200, workflowJSONOf(newWorkflow, "My workflow", foreignProject)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(workflowsCreate.ID, "project",
		minimalCreateArguments(fmt.Sprintf(`"project_id":%q`, ownProject)))
	if classOf(err) != "provider-error" {
		t.Fatalf("class = %q, want provider-error for a misplaced create", classOf(err))
	}
	if isInvalidRequest(err) {
		t.Fatalf("err = %v, want it not classified as an invalid request: the change already happened", err)
	}
	posts := 0
	for _, c := range calls {
		if c.method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("calls = %+v, want exactly the one POST that already reached n8n", calls)
	}
}

// workflows.update sends its one changing PUT only after the workflow's current scope is verified live, and
// re-reads it afterwards to answer with the same scope-checked detail workflows.get would.
func TestWorkflowsUpdateVerifiesScopeBeforeAndAfterItsOnePUT(t *testing.T) {
	var calls []call
	var query string
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/workflows/"+ownWorkflow:
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", ownProject)), nil
		case r.Method == http.MethodPut && r.URL.Path == apiPath+"/workflows/"+ownWorkflow:
			query = r.URL.RawQuery
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(workflowsUpdate.ID, "both",
		fmt.Sprintf(`{"workflow_id":%q,`, ownWorkflow)+minimalCreateArguments("")[1:])
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail WorkflowDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail.ID != ownWorkflow {
		t.Fatalf("result = %s, %v, want the re-read workflow", result, err)
	}
	if len(calls) != 3 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPut ||
		calls[2].method != http.MethodGet {
		t.Fatalf("calls = %+v, want a pre-check GET, one PUT, and a post-check GET", calls)
	}
	if strings.Contains(query, "publishIfActive") {
		t.Fatalf("query = %q, want no publishIfActive query when publish_if_active was not given", query)
	}

	// A workflow outside the workflow allow-list is refused before any request, PUT included.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(workflowsUpdate.ID, "workflow",
		fmt.Sprintf(`{"workflow_id":%q,`, foreignWorkflow)+minimalCreateArguments("")[1:])
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a workflow outside the allow-list", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the foreign workflow was refused", calls)
	}
}

// workflows.activate and workflows.deactivate each send their one changing POST only after the workflow's
// scope is verified live, then re-read the workflow to confirm the active state actually changed.
func TestWorkflowsActivateAndDeactivateChangeStateAfterScopeVerification(t *testing.T) {
	var calls []call
	// posted tracks whether the activating POST has already been sent: the pre-check GET (before it) reads
	// the workflow as still inactive, and the post-check GET (after it) reads it as active, exactly as one
	// real n8n instance would report the state before and after the change.
	var posted bool
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/workflows/"+ownWorkflow:
			return jsonResponse(200, activeWorkflowJSONOf(ownWorkflow, posted)), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/workflows/"+ownWorkflow+"/activate":
			posted = true
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(workflowsActivate.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail WorkflowDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || !detail.Active {
		t.Fatalf("result = %s, %v, want the workflow reported active", result, err)
	}
	if len(calls) != 3 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost ||
		calls[2].method != http.MethodGet {
		t.Fatalf("calls = %+v, want the pre-check GET, one activating POST, and the post-check GET", calls)
	}

	// A workflow that n8n still reports inactive after the activate POST succeeded is an invalid provider
	// response, not a silent success.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet:
			return jsonResponse(200, activeWorkflowJSONOf(ownWorkflow, false)), nil
		case r.Method == http.MethodPost:
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(workflowsActivate.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if classOf(err) != "invalid-provider-response" {
		t.Fatalf("class = %q, want invalid-provider-response when the active state never changed", classOf(err))
	}
}

// activeWorkflowJSONOf is workflowJSONOf with an explicit active flag, for the activate/deactivate tests.
func activeWorkflowJSONOf(id string, active bool) string {
	raw := workflowJSONOf(id, "Own")
	return strings.Replace(raw, `"active":true`, fmt.Sprintf(`"active":%t`, active), 1)
}

// executions.retry and executions.stop each verify the execution's workflow scope, with the same live
// checks GetExecution applies, before either one sends its one changing POST.
func TestExecutionsRetryAndStopVerifyScopeBeforeTheirOnePOST(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/executions/1" && r.Method == http.MethodGet:
			return jsonResponse(200, executionJSONOf(1, ownWorkflow, "error")), nil
		case r.URL.Path == apiPath+"/executions/1/retry":
			return jsonResponse(200, executionJSONOf(2, ownWorkflow, "success")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err := env.confirmed(executionsRetry.ID, "workflow", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var summary ExecutionSummary
	if err := json.Unmarshal([]byte(result), &summary); err != nil || summary.ID != 2 {
		t.Fatalf("result = %s, %v, want the new execution's own id", result, err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPost {
		t.Fatalf("calls = %+v, want the base read then exactly one retry POST", calls)
	}

	// An execution of a foreign workflow is refused after the one base read, before any retry POST.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/executions/1" {
			return jsonResponse(200, executionJSONOf(1, foreignWorkflow, "error")), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(executionsRetry.ID, "workflow", `{"execution_id":1}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a foreign workflow's execution", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want only the base read, no retry POST", calls)
	}

	// stop succeeds the same way, combining the base read's id and workflowId with the stop answer's fresh
	// status, since stopExecution's own answer carries neither.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/executions/1" && r.Method == http.MethodGet:
			return jsonResponse(200, executionJSONOf(1, ownWorkflow, "running")), nil
		case r.URL.Path == apiPath+"/executions/1/stop":
			return jsonResponse(200, `{"mode":"manual","startedAt":"2026-01-01T00:00:00.000Z",`+
				`"stoppedAt":"2026-01-01T00:00:05.000Z","finished":false,"status":"canceled"}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	result, err = env.confirmed(executionsStop.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &summary); err != nil || summary.ID != 1 ||
		summary.WorkflowID != ownWorkflow || summary.Status != "canceled" {
		t.Fatalf("result = %s, %v, want the base id/workflow_id with the fresh status", result, err)
	}

	// A project-restricted connection refuses a foreign-project execution after the base read and the
	// workflow's live project check, before any stop POST.
	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/executions/1":
			return jsonResponse(200, executionJSONOf(1, ownWorkflow, "running")), nil
		case r.URL.Path == apiPath+"/workflows/"+ownWorkflow:
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", foreignProject)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(executionsStop.ID, "project", `{"execution_id":1}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a foreign project's execution", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the base read and the workflow's project check, no stop POST", calls)
	}
}

// An unclear outcome of the one changing request, here a 5xx after workflows.create's request already
// reached n8n, and a timeout after executions.stop's request may have reached n8n, is reported as uncertain
// and never repeated by this provider itself.
func TestChangeRequestsAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	var calls []call
	posts := 0
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == apiPath+"/workflows" {
			posts++
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+foreignCanary+`"}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.confirmed(workflowsCreate.ID, "open", minimalCreateArguments(""))
	if classOf(err) != "provider-error" {
		t.Fatalf("class = %q, want provider-error", classOf(err))
	}
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v, want it to name the change as uncertain", err)
	}
	if err != nil && strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("error leaked the provider body: %v", err)
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want exactly one attempt, never repeated by Qatlas itself", posts)
	}

	calls = nil
	stops := 0
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/executions/1" && r.Method == http.MethodGet:
			return jsonResponse(200, executionJSONOf(1, ownWorkflow, "running")), nil
		case r.URL.Path == apiPath+"/executions/1/stop":
			stops++
			return nil, errUnclearTransport{}
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.confirmed(executionsStop.ID, "open", `{"execution_id":1}`)
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v, want the stop's own transport failure named as uncertain", err)
	}
	if stops != 1 {
		t.Fatalf("stops = %d, want exactly one attempt, never repeated by Qatlas itself", stops)
	}
}

// errUnclearTransport stands for a connection reset after the request left this process: net/http reports
// it as a plain, untyped error, which (*Client).do's own transport classification cannot prove any more
// specific cause for, so it is marked uncertain for a change request all the same.
type errUnclearTransport struct{}

func (errUnclearTransport) Error() string { return "connection lost after the request was sent" }

// 401 and 403 during a change's one request are classified the same stable way every read classifies them,
// and never carry n8n's own response body into the error.
func TestChangeToolsClassifyAuthAndPermissionFailuresWithoutLeakingContent(t *testing.T) {
	for _, tt := range []struct {
		status int
		class  string
	}{{http.StatusUnauthorized, "auth"}, {http.StatusForbidden, "permission"}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(tt.status, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(workflowsCreate.ID, "open", minimalCreateArguments(""))
		if string(classOf(err)) != tt.class {
			t.Fatalf("status %d: class = %q, want %q", tt.status, classOf(err), tt.class)
		}
		if err != nil && strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("status %d: error %v leaked the provider body", tt.status, err)
		}
	}
}
