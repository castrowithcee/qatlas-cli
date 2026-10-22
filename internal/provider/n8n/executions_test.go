package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func executionJSONOf(id int64, workflowID, status string) string {
	return fmt.Sprintf(`{"id":%d,"workflowId":%q,"status":%q,"mode":"manual","finished":true,`+
		`"startedAt":"2026-01-01T00:00:00.000Z","stoppedAt":"2026-01-01T00:00:05.000Z"}`, id, workflowID, status)
}

func executionsPageJSONOf(executions ...string) string {
	return fmt.Sprintf(`{"data":[%s],"nextCursor":null}`, strings.Join(executions, ","))
}

// executionWithErrorJSONOf is the includeData=true answer executions.get reads to extract a failed
// execution's error. runDataCanary stands for a node's real run payload, which must never reach the result.
const runDataCanary = "run-data-canary-2c91"

func executionWithErrorJSONOf(id int64, workflowID, message, nodeName, nodeType string) string {
	return fmt.Sprintf(`{"id":%d,"workflowId":%q,"status":"error","mode":"manual","finished":true,`+
		`"data":{"resultData":{"lastNodeExecuted":%q,"runData":{%q:[{"data":{"main":[[{"json":{"secret":%q}}]]}}]},`+
		`"error":{"message":%q,"node":{"name":%q,"type":%q}}}}}`,
		id, workflowID, nodeName, nodeName, runDataCanary, message, nodeName, nodeType)
}

func TestExecutionsListRequiresWorkflowIDWhenProjectRestricted(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.invoke(executionsList.ID, "project", `{}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request when a project-restricted connection omits workflow_id", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the missing workflow_id was refused", calls)
	}
}

// The workflow-restricted connection needs no project verification, so it may list without workflow_id, and
// the server-side filter is still re-applied defensively to every returned row.
func TestExecutionsListDefensivelyFiltersByWorkflowAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionsPageJSONOf(
			executionJSONOf(1, ownWorkflow, "success"),
			executionJSONOf(2, foreignWorkflow, "success"),
		)), nil
	})
	result, err := env.invoke(executionsList.ID, "workflow", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ExecutionsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 || page.Executions[0].WorkflowID != ownWorkflow {
		t.Fatalf("result = %s, %v, want only the allow-listed workflow's execution", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one request", calls)
	}
}

// Given an explicit workflow_id, a project-restricted connection verifies that workflow's project once,
// with one extra request, before listing its executions; a workflow outside the project allow-list is
// refused before the executions endpoint is ever reached.
func TestExecutionsListVerifiesProjectOnceForTheNamedWorkflow(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/workflows/" + ownWorkflow:
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", ownProject)), nil
		case apiPath + "/executions":
			return jsonResponse(200, executionsPageJSONOf(executionJSONOf(1, ownWorkflow, "success"))), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err := env.invoke(executionsList.ID, "project", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page ExecutionsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[0].path != apiPath+"/workflows/"+ownWorkflow {
		t.Fatalf("calls = %+v, want the workflow verified once before the executions were listed", calls)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/workflows/"+ownWorkflow {
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", foreignProject)), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err = env.invoke(executionsList.ID, "project", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a workflow outside the project allow-list", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the executions endpoint never reached", calls)
	}
}

// An execution of a workflow outside the workflow allow-list is refused, and one whose workflow's project is
// outside the project allow-list is refused after exactly one extra live read of that workflow.
func TestExecutionsGetRefusesForeignWorkflowAndForeignProject(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionJSONOf(1, foreignWorkflow, "success")), nil
	})
	_, err := env.invoke(executionsGet.ID, "workflow", `{"execution_id":1}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for an execution of a foreign workflow", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the execution read once and no further request", calls)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/executions/1":
			return jsonResponse(200, executionJSONOf(1, ownWorkflow, "success")), nil
		case apiPath + "/workflows/" + ownWorkflow:
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", foreignProject)), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})
	_, err = env.invoke(executionsGet.ID, "project", `{"execution_id":1}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a workflow outside the project allow-list", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want the execution read and its workflow's project verified once", calls)
	}
}

// A successful execution never triggers the includeData request at all, and a failed one triggers it once,
// returning only the bounded error fields: the node's real run data this fake answers with never reaches
// the result.
func TestExecutionsGetExtractsBoundedErrorOnlyForFailedStatus(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionJSONOf(1, ownWorkflow, "success")), nil
	})
	result, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail ExecutionDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail.Error != nil {
		t.Fatalf("result = %s, %v, want no error field for a successful execution", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want no includeData request for a successful execution", calls)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("includeData") == "true" {
			return jsonResponse(200, executionWithErrorJSONOf(1, ownWorkflow, "node failed", "HTTP Request", "n8n-nodes-base.httpRequest")), nil
		}
		return jsonResponse(200, executionJSONOf(1, ownWorkflow, "error")), nil
	})
	result, err = env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &detail); err != nil || detail.Error == nil ||
		detail.Error.Message != "node failed" || detail.Error.NodeName != "HTTP Request" ||
		detail.Error.NodeType != "n8n-nodes-base.httpRequest" {
		t.Fatalf("result = %s, %v, want the bounded error fields", result, err)
	}
	if len(calls) != 2 || calls[1].query.Get("includeData") != "true" {
		t.Fatalf("calls = %+v, want exactly one base read and one includeData read", calls)
	}
	if strings.Contains(result, runDataCanary) || strings.Contains(result, "runData") {
		t.Fatalf("result = %s, want the node's run data never reached the output", result)
	}
}

// An includeData answer larger than this provider's local ceiling is refused rather than parsed partially.
func TestExecutionsGetRefusesAnOversizedIncludeDataAnswer(t *testing.T) {
	oversized := strings.Repeat("x", maxErrorResponseBytes+1)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("includeData") == "true" {
			return jsonResponse(200, `{"padding":"`+oversized+`"}`), nil
		}
		return jsonResponse(200, executionJSONOf(1, ownWorkflow, "error")), nil
	})
	_, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if classOf(err) != "invalid-provider-response" {
		t.Fatalf("class = %q, want invalid-provider-response for an oversized includeData answer", classOf(err))
	}
}

// n8n answering with an execution other than the one requested, and a rate limit, are classified the same
// way every other request classifies them.
func TestExecutionsGetClassifiesMismatchAndRateLimit(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionJSONOf(2, ownWorkflow, "success")), nil
	})
	_, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if classOf(err) != "invalid-provider-response" {
		t.Fatalf("class = %q, want invalid-provider-response for a mismatched execution", classOf(err))
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		response := jsonResponse(429, `{}`)
		response.Header.Set("Retry-After", "3")
		return response, nil
	})
	_, err = env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if classOf(err) != "rate-limited" {
		t.Fatalf("class = %q, want rate-limited", classOf(err))
	}
}

// specExecution is a getExecution answer as the spec describes it, sizes beyond float64's exact range.
const specExecution = `{"id":1,"finished":true,"mode":"manual","retryOf":null,"retrySuccessId":null,` +
	`"status":"success","createdAt":"2025-12-31T23:59:59.000Z","startedAt":"2026-01-01T00:00:00.000Z",` +
	`"stoppedAt":"2026-01-01T00:00:05.000Z","deletedAt":null,"workflowId":"` + ownWorkflow + `","waitTill":null,` +
	`"storedAt":"fs","tracingContext":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","tracestate":"k=v"},` +
	`"deduplicationKey":"dedup-1","jsonSizeBytes":9007199254740993,"binaryDataSizeBytes":9007199254740995,` +
	`"workflowVersionId":"ver-1","usedPrivateCredentials":false}`

func TestExecutionsGetReportsTheDocumentedReadMembers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, specExecution), nil
	})
	result, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(result))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"created_at": "2025-12-31T23:59:59.000Z", "stored_at": "fs",
		"deduplication_key": "dedup-1", "json_size_bytes": "9007199254740993",
		"binary_data_size_bytes": "9007199254740995", "workflow_version_id": "ver-1", "used_private_credentials": "false"}
	for name, value := range want {
		if fmt.Sprint(got[name]) != value {
			t.Errorf("%s = %v, want %s in %s", name, got[name], value, result)
		}
	}
	tracing, _ := got["tracing_context"].(map[string]any)
	if tracing["traceparent"] != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" || tracing["tracestate"] != "k=v" {
		t.Errorf("tracing_context = %v", got["tracing_context"])
	}
	for _, absent := range []string{"deleted_at", "retry_of", "wait_till", "data_too_large_to_display", "error"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s present in %s", absent, result)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want one base read", len(calls))
	}
}

// Unknown storage values, unknown sizes, and malformed members are left out instead of failing the read.
func TestExecutionsGetLeavesOutUnknownAndMalformedMembers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":1,"finished":true,"mode":"manual","status":"success","workflowId":"`+ownWorkflow+
			`","storedAt":"tape","jsonSizeBytes":0,"binaryDataSizeBytes":"big","tracingContext":null,`+
			`"deduplicationKey":null,"usedPrivateCredentials":null,"workflowVersionId":"a\u0007b"}`), nil
	})
	result, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, absent := range []string{"stored_at", "json_size_bytes", "binary_data_size_bytes", "tracing_context",
		"deduplication_key", "used_private_credentials"} {
		if strings.Contains(result, `"`+absent+`"`) {
			t.Errorf("%s present in %s", absent, result)
		}
	}
	if !strings.Contains(result, `"workflow_version_id":"a b"`) {
		t.Errorf("result = %s, want the control character replaced", result)
	}
}

func TestExecutionsGetKeepsItsRequestCountForFailedExecutions(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("includeData") == "true" {
			return jsonResponse(200, executionWithErrorJSONOf(1, ownWorkflow, "boom", "N", "t")), nil
		}
		return jsonResponse(200, strings.Replace(specExecution, `"status":"success"`, `"status":"crashed"`, 1)), nil
	})
	result, err := env.invoke(executionsGet.ID, "open", `{"execution_id":1}`)
	if err != nil || !strings.Contains(result, `"stored_at":"fs"`) || !strings.Contains(result, `"message":"boom"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the base read and one includeData read", len(calls))
	}
}

func TestExecutionsListReportsTheDocumentedReadMembers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[{"id":1,"finished":false,"mode":"trigger","retryOf":null,`+
			`"retrySuccessId":null,"status":"running","startedAt":"2026-01-01T00:00:00.000Z","stoppedAt":null,`+
			`"workflowId":"`+ownWorkflow+`","waitTill":null,"storedAt":"az","jsonSizeBytes":9007199254740993,`+
			`"workflowVersionId":"ver-2","dataTooLargeToDisplay":true},`+
			`{"id":2,"finished":true,"mode":"manual","status":"success","workflowId":"`+ownWorkflow+`"}],"nextCursor":null}`), nil
	})
	result, err := env.invoke(executionsList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, want := range []string{`"stored_at":"az"`, `"json_size_bytes":9007199254740993`,
		`"workflow_version_id":"ver-2"`, `"data_too_large_to_display":true`} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %s in %s", want, result)
		}
	}
	// The second row carries none of the additions and keeps its earlier shape.
	if !strings.Contains(result, `{"finished":true,"id":2,"mode":"manual","status":"success","workflow_id":"`+ownWorkflow+`"}`) {
		t.Errorf("result = %s, want the bare row unchanged", result)
	}
}

func TestExecutionsListSendsTheNewFiltersAsUTC(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionsPageJSONOf()), nil
	})
	_, err := env.invoke(executionsList.ID, "open", `{"project_id":"`+ownProject+
		`","started_after":"2026-01-01T02:00:00+02:00","started_before":"2026-01-02T00:00:00.5Z","cursor":"c"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	q := calls[0].query
	if q.Get("projectId") != ownProject || q.Get("startedAfter") != "2026-01-01T00:00:00Z" ||
		q.Get("startedBefore") != "2026-01-02T00:00:00.5Z" || q.Get("cursor") != "c" {
		t.Fatalf("query = %v", q)
	}
}

func TestExecutionsListRefusesBadFiltersBeforeAnyRequest(t *testing.T) {
	for name, tt := range map[string]struct{ connection, arguments string }{
		"project outside the allow-list": {"project", `{"workflow_id":"` + ownWorkflow + `","project_id":"` + foreignProject + `"}`},
		"project not an identifier":      {"open", `{"project_id":"a b"}`},
		"swapped range":                  {"open", `{"started_after":"2026-01-02T00:00:00Z","started_before":"2026-01-01T00:00:00Z"}`},
		"not a time":                     {"open", `{"started_after":"yesterday at noon, please"}`},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			if _, err := env.invoke(executionsList.ID, tt.connection, tt.arguments); !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if len(calls) != 0 {
				t.Fatalf("calls = %+v, want none", calls)
			}
		})
	}
}

func TestExecutionsListAcceptsAnAllowListedProject(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/workflows/"+ownWorkflow {
			return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", ownProject)), nil
		}
		return jsonResponse(200, executionsPageJSONOf(executionJSONOf(1, ownWorkflow, "success"))), nil
	})
	if _, err := env.invoke(executionsList.ID, "project",
		`{"workflow_id":"`+ownWorkflow+`","project_id":"`+ownProject+`"}`); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[1].query.Get("projectId") != ownProject {
		t.Fatalf("calls = %+v", calls)
	}
}

// The changing tools keep their output even when n8n's answer carries the read-only additions.
func TestExecutionsRetryAndStopOutputIsUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet:
			return jsonResponse(200, specExecution), nil
		case strings.HasSuffix(r.URL.Path, "/retry"):
			return jsonResponse(200, strings.Replace(specExecution, `"id":1`, `"id":2`, 1)), nil
		default:
			return jsonResponse(200, `{"mode":"manual","startedAt":"2026-01-01T00:00:00.000Z","stoppedAt":"2026-01-01T00:00:05.000Z","finished":false,"status":"canceled","storedAt":"db","jsonSizeBytes":5}`), nil
		}
	})
	retried, err := env.confirmed(executionsRetry.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("retry = %v", err)
	}
	wantRetry := `{"finished":true,"id":2,"mode":"manual","started_at":"2026-01-01T00:00:00.000Z","status":"success",` +
		`"stopped_at":"2026-01-01T00:00:05.000Z","workflow_id":"` + ownWorkflow + `"}`
	if retried != wantRetry {
		t.Errorf("retry = %s, want %s", retried, wantRetry)
	}
	stopped, err := env.confirmed(executionsStop.ID, "open", `{"execution_id":1}`)
	if err != nil {
		t.Fatalf("stop = %v", err)
	}
	wantStop := `{"finished":false,"id":1,"mode":"manual","started_at":"2026-01-01T00:00:00.000Z","status":"canceled",` +
		`"stopped_at":"2026-01-01T00:00:05.000Z","workflow_id":"` + ownWorkflow + `"}`
	if stopped != wantStop {
		t.Errorf("stop = %s, want %s", stopped, wantStop)
	}
	for _, d := range []string{executionsRetry.ID, executionsStop.ID} {
		descriptor, _, _ := registry(t).Lookup(d)
		if strings.Contains(string(descriptor.OutputSchema), "stored_at") {
			t.Errorf("%s output schema gained a read-only member", d)
		}
	}
}
