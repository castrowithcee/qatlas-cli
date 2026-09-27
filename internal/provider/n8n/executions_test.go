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
