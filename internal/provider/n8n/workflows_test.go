package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// workflowJSONOf builds the workflowPublicDto shape this package reads, with one node that carries a
// credential reference, so a test can prove that reference is passed on unmodified and nothing else is.
func workflowJSONOf(id, name string, projectIDs ...string) string {
	shared := "[]"
	if len(projectIDs) > 0 {
		parts := make([]string, len(projectIDs))
		for i, p := range projectIDs {
			parts[i] = fmt.Sprintf(`{"role":"workflow:owner","projectId":%q}`, p)
		}
		shared = "[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"active":true,"isArchived":false,`+
		`"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z","triggerCount":1,`+
		`"tags":[{"id":"t1","name":"billing"}],"nodes":[{"id":"n1","name":"Start","type":"n8n-nodes-base.start",`+
		`"typeVersion":1,"position":[0,0],"disabled":false,"parameters":{"note":%q},`+
		`"credentials":{"httpBasicAuth":{"id":"cred1","name":"prod-api"}}}],"connections":{"Start":{}},`+
		`"shared":%s}`, id, name, "hello world", shared)
}

func workflowsPageJSONOf(workflows ...string) string {
	return fmt.Sprintf(`{"data":[%s],"nextCursor":null}`, strings.Join(workflows, ","))
}

func TestWorkflowsListPaginatesAndDefensivelyFiltersByScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != apiPath+"/workflows" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, workflowsPageJSONOf(
			workflowJSONOf(ownWorkflow, "Own", ownProject),
			workflowJSONOf(foreignWorkflow, "Foreign", foreignProject),
		)), nil
	})

	// The open connection has no allow-list at all, so both workflows are reported.
	result, err := env.invoke(workflowsList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page WorkflowsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 {
		t.Fatalf("result = %s, %v, want 2 workflows", result, err)
	}
	if len(calls) != 1 || calls[0].query.Get("limit") != "100" {
		t.Fatalf("calls = %+v, want one server-side call with the default limit", calls)
	}

	// The project-restricted connection keeps only the workflow shared into its allowed project, even
	// though the server answered with both, because n8n's own "shared" array is already part of that same
	// answer: no extra request is needed to filter this list.
	calls = nil
	result, err = env.invoke(workflowsList.ID, "project", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 || page.Workflows[0].ID != ownWorkflow {
		t.Fatalf("result = %s, %v, want only the own-project workflow", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one request, no live re-check per item", calls)
	}

	// The workflow-restricted connection keeps only the allow-listed workflow.
	result, err = env.invoke(workflowsList.ID, "workflow", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 1 || page.Workflows[0].ID != ownWorkflow {
		t.Fatalf("result = %s, %v, want only the allow-listed workflow", result, err)
	}
}

// A project_id filter argument outside the connection's project allow-list is refused locally, before any
// request is sent.
func TestWorkflowsListRefusesForeignProjectFilterLocally(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.invoke(workflowsList.ID, "project", fmt.Sprintf(`{"project_id":%q}`, foreignProject))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a project outside the allow-list", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none before the foreign project was refused", calls, *env.reads)
	}
}

// A workflow outside a configured workflow allow-list is refused locally, before any request is sent, and
// never reaches a secret read either.
func TestWorkflowsGetRefusesForeignWorkflowLocally(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.invoke(workflowsGet.ID, "workflow", fmt.Sprintf(`{"workflow_id":%q}`, foreignWorkflow))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a workflow outside the allow-list", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none before the foreign workflow was refused", calls, *env.reads)
	}
}

// A workflow that does belong to this connection's workflow allow-list, but whose project is outside its
// project allow-list, is refused only after the live read proves it, and the workflow's content never
// reaches the result. A workflow with no reported project at all is refused the same fail-closed way,
// because a project-restricted connection cannot prove what an older instance or Public API version never
// reports.
func TestWorkflowsGetLiveChecksProjectMembership(t *testing.T) {
	cases := []struct {
		name       string
		projectIDs []string
	}{
		{"foreign project", []string{foreignProject}},
		{"no reported project at all", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", tt.projectIDs...)), nil
			})
			_, err := env.invoke(workflowsGet.ID, "project", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want exactly one live read before the refusal", calls)
			}
		})
	}
}

// A workflow inside both allow-lists is returned in full, its node's credential reference passed on as the
// id/name pair n8n itself reports, and no field in the result can ever carry a credential's value: the
// output schema has no property for one.
func TestWorkflowsGetReturnsNodesWithCredentialReferencesOnly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, workflowJSONOf(ownWorkflow, "Own", ownProject)), nil
	})
	result, err := env.invoke(workflowsGet.ID, "both", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var detail WorkflowDetail
	if err := json.Unmarshal([]byte(result), &detail); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(detail.Nodes) != 1 || detail.Nodes[0].Credentials["httpBasicAuth"].ID != "cred1" ||
		detail.Nodes[0].Credentials["httpBasicAuth"].Name != "prod-api" {
		t.Fatalf("nodes = %+v, want the credential reference passed through", detail.Nodes)
	}
	if strings.Contains(result, "\"value\"") || strings.Contains(result, "\"secret\"") ||
		strings.Contains(result, "\"data\":{\"resultData\"") {
		t.Fatalf("result = %s, want no credential value or run-data field", result)
	}
}

// n8n answering with a workflow other than the one requested, or with a permission failure, is classified
// and never leaks the provider body.
func TestWorkflowsGetClassifiesMismatchAndPermissionFailure(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, workflowJSONOf("some-other-id", "Other")), nil
	})
	_, err := env.invoke(workflowsGet.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if classOf(err) != "invalid-provider-response" {
		t.Fatalf("class = %q, want invalid-provider-response for a mismatched workflow", classOf(err))
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	_, err = env.invoke(workflowsGet.ID, "open", fmt.Sprintf(`{"workflow_id":%q}`, ownWorkflow))
	if classOf(err) != "permission" {
		t.Fatalf("class = %q, want permission", classOf(err))
	}
	if err != nil && strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("error %v leaked the provider body", err)
	}
}
