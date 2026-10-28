package n8n

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The core accepts `exclude` only where a provider declares it excludable, so these tests call the provider
// functions with a resolved connection that carries targets and exclusions directly.
const (
	exclProject  = "PROJECT_EXCL_0003CCCC"
	exclWorkflow = "WORKFLOW_EXCL_0003CC"
	exclCanary   = "excluded-name-canary-51ab"
)

type invokeFunc func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)

// excludingConnection is an n8n connection with the given targets and exclusions.
func excludingConnection(targets []string, excludes ...string) *config.Resolved {
	resolved := resolvedConnection(targets...)
	resolved.Exclude = excludes
	return resolved
}

// bothExclusions excludes one project and one workflow.
var bothExclusions = []string{"project/" + exclProject, "workflow/" + exclWorkflow}

// run invokes one provider function on a connection with exclusions and returns its JSON result.
func run(t *testing.T, fn invokeFunc, resolved *config.Resolved, arguments string,
	handler func(*http.Request) (*http.Response, error)) (string, []call, error) {
	t.Helper()
	var calls []call
	if handler == nil {
		handler = noRequest(t)
	}
	serve(t, &calls, handler)
	red := &redact.Redactor{}
	result, err := fn(context.Background(), resolved, resolver(red, nil), red, json.RawMessage(arguments))
	encoded, _ := json.Marshal(result)
	return string(encoded), calls, err
}

// refused expects a refusal that names neither the object nor the exclusion, with no change request sent.
func refused(t *testing.T, err error, calls []call, wantRequests int) {
	t.Helper()
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	for _, secretValue := range []string{exclProject, exclWorkflow, exclCanary} {
		if strings.Contains(err.Error(), secretValue) {
			t.Fatalf("refusal names an excluded value: %v", err)
		}
	}
	if len(calls) != wantRequests {
		t.Fatalf("calls = %+v, want %d requests", calls, wantRequests)
	}
	for _, c := range calls {
		if c.method != http.MethodGet {
			t.Fatalf("a change was sent: %+v", c)
		}
	}
}

// workflowSharedJSON is a workflow whose shares carry a project name, for the shared array.
func workflowSharedJSON(id string, projects ...string) string {
	shared := make([]string, len(projects))
	for i, p := range projects {
		shared[i] = fmt.Sprintf(`{"role":"workflow:owner","workflowId":%q,"projectId":%q,`+
			`"project":{"id":%q,"name":%q,"type":"team"}}`, id, p, p, exclCanary)
	}
	return fmt.Sprintf(`{"id":%q,"name":"W","active":true,"nodes":[],"connections":{},"shared":[%s]}`,
		id, strings.Join(shared, ","))
}

// sharedName is the project name a share exposes; empty when it is reduced to the ID.
func sharedName(s SharedWorkflow) string {
	project, _ := s.Project.(map[string]any)
	name, _ := project["name"].(string)
	return name
}

func TestExcludeParsing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		targets  []string
		excludes []string
		wantErr  bool
	}{
		{"none", nil, nil, false},
		{"project and workflow", nil, bothExclusions, false},
		{"outside every target", []string{"project/p1"}, []string{"project/p2", "workflow/w1"}, false},
		{"repeated project", nil, []string{"project/p1", "project/p1"}, true},
		{"repeated workflow", nil, []string{"workflow/w1", "workflow/w1"}, true},
		{"equal to a project target", []string{"project/p1"}, []string{"project/p1"}, true},
		{"equal to a workflow target", []string{"workflow/w1"}, []string{"workflow/w1"}, true},
		{"unknown form", nil, []string{"team/p1"}, true},
		{"empty", nil, []string{""}, true},
		{"wildcard", nil, []string{"project/*"}, true},
		{"free form", nil, []string{"p1"}, true},
		{"traversal", nil, []string{"workflow/../x"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bound, err := parseScope(tt.targets, tt.excludes...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
			if err != nil {
				for _, e := range tt.excludes {
					if e != "" && strings.Contains(err.Error(), e) {
						t.Fatalf("error quotes %q: %v", e, err)
					}
				}
				return
			}
			if len(bound.excludedProjects)+len(bound.excludedWorkflows) != len(tt.excludes) {
				t.Fatalf("scope = %+v", bound)
			}
		})
	}
	// An invalid exclusion fails the connection before a secret is read.
	var calls []call
	serve(t, &calls, noRequest(t))
	reads := 0
	red := &redact.Redactor{}
	_, err := Open(context.Background(), excludingConnection(nil, "project/p1", "project/p1"), resolver(red, &reads), red)
	if err == nil || reads != 0 {
		t.Fatalf("Open() = %v, reads = %d", err, reads)
	}
}

// Exclusions never change the target-only gates, reach, or the connection's instance-wide character.
func TestExcludeLeavesGatesAndReachUnchanged(t *testing.T) {
	resolved := excludingConnection(nil, bothExclusions...)
	if err := requireInstanceScope(resolved, "x"); err != nil {
		t.Fatalf("requireInstanceScope() = %v, want an instance-wide connection to stay one", err)
	}
	bound, err := boundScope(resolved)
	if err != nil || !bound.instanceWide() || !bound.restrictsProjects() {
		t.Fatalf("scope = %+v, %v", bound, err)
	}
	if _, err := selectCredentials(resolved); err != nil {
		t.Fatalf("selectCredentials() = %v", err)
	}
	// executions.list needs workflow_id only because of a project target, never because of an exclusion.
	_, calls, err := run(t, invokeExecutionsList, resolved, `{}`, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, executionsPageJSONOf()), nil
	})
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// A project target keeps the workflow_id gate, a workflow target keeps refusing credentials and creates.
	if _, _, err := run(t, invokeExecutionsList, excludingConnection([]string{"project/" + ownProject},
		bothExclusions...), `{}`, nil); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the workflow_id gate", err)
	}
	withWorkflow := excludingConnection([]string{"workflow/" + ownWorkflow}, "project/"+exclProject)
	if _, err := selectCredentials(withWorkflow); !isInvalidRequest(err) {
		t.Fatalf("selectCredentials() = %v, want a refusal for a workflow target", err)
	}
	if _, _, err := run(t, invokeWorkflowsCreate, withWorkflow, createWorkflowArguments(""), nil); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want the create gate", err)
	}
	for _, tool := range []string{workflowsList.ID, credentialsList.ID, projectsDelete.ID, tagsList.ID, usersList.ID} {
		onTargets, missing := reaches(tool, []string{"project/" + ownProject})
		withExcl := excludingConnection([]string{"project/" + ownProject}, bothExclusions...)
		again, missingAgain := reaches(tool, provider.TargetsOf(withExcl))
		if onTargets != again || len(missing) != len(missingAgain) {
			t.Fatalf("reaches(%s) changed", tool)
		}
	}
}

func createWorkflowArguments(project string) string {
	extra := ""
	if project != "" {
		extra = fmt.Sprintf(`,"project_id":%q`, project)
	}
	return `{"name":"W","nodes":[{"id":"1","name":"Start","type":"n8n-nodes-base.manualTrigger"}],` +
		`"connections":{},"settings":{}` + extra + `}`
}

func TestWorkflowsListHidesExcludedAndKeepsSharedOnlyAsID(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s,%s,%s],"nextCursor":null}`,
		workflowSharedJSON("W_IN", ownProject),
		workflowSharedJSON("W_OUT", exclProject),
		workflowSharedJSON("W_SHARED", exclProject, ownProject),
		workflowSharedJSON(exclWorkflow, ownProject))
	result, calls, err := run(t, invokeWorkflowsList, excludingConnection(nil, bothExclusions...), `{}`,
		func(r *http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	var page WorkflowsPage
	if json.Unmarshal([]byte(result), &page) != nil || page.Count != 2 || page.Workflows[0].ID != "W_IN" ||
		page.Workflows[1].ID != "W_SHARED" {
		t.Fatalf("page = %s", result)
	}
	// The excluded project of the shared workflow is reduced to its ID; the reachable one keeps its name.
	var shared []SharedWorkflow
	for _, w := range page.Workflows {
		if w.ID == "W_SHARED" {
			shared = w.Shared
		}
	}
	if len(shared) != 2 || shared[0].ProjectID != exclProject || sharedName(shared[0]) != "" ||
		sharedName(shared[1]) == "" {
		t.Fatalf("shared = %+v", shared)
	}
}

func TestWorkflowsListFilterOnExcludedIsAnEmptyPage(t *testing.T) {
	result, calls, err := run(t, invokeWorkflowsList, excludingConnection(nil, bothExclusions...),
		fmt.Sprintf(`{"project_id":%q}`, exclProject), nil)
	var page WorkflowsPage
	if err != nil || len(calls) != 0 || json.Unmarshal([]byte(result), &page) != nil || page.Count != 0 ||
		page.HasMore || !strings.Contains(result, `"workflows":[]`) {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

func TestWorkflowsGetRefusesExcludedWorkflowAndProject(t *testing.T) {
	resolved := excludingConnection(nil, bothExclusions...)
	// An excluded workflow is refused before any request, even when it is shared into a reachable project.
	_, calls, err := run(t, invokeWorkflowsGet, resolved, fmt.Sprintf(`{"workflow_id":%q}`, exclWorkflow), nil)
	refused(t, err, calls, 0)

	handler := func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/workflows/W_OUT":
			return jsonResponse(200, workflowSharedJSON("W_OUT", exclProject)), nil
		case apiPath + "/workflows/W_SHARED":
			return jsonResponse(200, workflowSharedJSON("W_SHARED", exclProject, ownProject)), nil
		case apiPath + "/workflows/W_NONE":
			return jsonResponse(200, `{"id":"W_NONE","name":"W","nodes":[],"connections":{}}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}
	_, calls, err = run(t, invokeWorkflowsGet, resolved, `{"workflow_id":"W_OUT"}`, handler)
	refused(t, err, calls, 1)
	// Without any reported project the membership cannot be proven, so it is refused.
	_, calls, err = run(t, invokeWorkflowsGet, resolved, `{"workflow_id":"W_NONE"}`, handler)
	refused(t, err, calls, 1)
	// Shared into a reachable project: readable, the excluded project only as an ID.
	result, _, err := run(t, invokeWorkflowsGet, resolved, `{"workflow_id":"W_SHARED"}`, handler)
	if err != nil || !strings.Contains(result, `"W_SHARED"`) || !strings.Contains(result, exclProject) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var detail WorkflowRead
	if json.Unmarshal([]byte(result), &detail) != nil || sharedName(detail.Shared[0]) != "" ||
		sharedName(detail.Shared[1]) == "" {
		t.Fatalf("shared = %+v", detail.Shared)
	}
}

func TestWorkflowChangesRefuseExcludedWorkflowsBeforeAnyRequest(t *testing.T) {
	resolved := excludingConnection(nil, bothExclusions...)
	update := `{"workflow_id":"` + exclWorkflow + `","name":"W","nodes":[{"id":"1","name":"S","type":"t"}],` +
		`"connections":{},"settings":{}}`
	for name, tt := range map[string]struct {
		fn        invokeFunc
		arguments string
	}{
		"update":     {invokeWorkflowsUpdate, update},
		"activate":   {invokeWorkflowsActivate, fmt.Sprintf(`{"workflow_id":%q}`, exclWorkflow)},
		"deactivate": {invokeWorkflowsDeactivate, fmt.Sprintf(`{"workflow_id":%q}`, exclWorkflow)},
	} {
		t.Run(name, func(t *testing.T) {
			_, calls, err := run(t, tt.fn, resolved, tt.arguments, nil)
			refused(t, err, calls, 0)
		})
	}
	// A workflow of an excluded project is refused after the one read, and nothing is changed.
	handler := func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, workflowSharedJSON("W_OUT", exclProject)), nil
	}
	_, calls, err := run(t, invokeWorkflowsActivate, resolved, `{"workflow_id":"W_OUT"}`, handler)
	refused(t, err, calls, 1)
}

func TestWorkflowCreateRefusesExcludedAndUnprovenTargets(t *testing.T) {
	_, calls, err := run(t, invokeWorkflowsCreate, excludingConnection(nil, bothExclusions...),
		createWorkflowArguments(exclProject), nil)
	refused(t, err, calls, 0)
	// No project_id would place it in the key owner's personal project, which cannot be proven reachable.
	_, calls, err = run(t, invokeWorkflowsCreate, excludingConnection(nil, bothExclusions...),
		createWorkflowArguments(""), nil)
	refused(t, err, calls, 0)
	// An excluded workflow alone does not need project_id.
	handler := func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(200, `{"id":"W_NEW"}`), nil
		}
		return jsonResponse(200, workflowSharedJSON("W_NEW", ownProject)), nil
	}
	if _, calls, err = run(t, invokeWorkflowsCreate, excludingConnection(nil, "workflow/"+exclWorkflow),
		createWorkflowArguments(""), handler); err != nil || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// A result that lands in an excluded project is a provider error, as for a target.
	landed := func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(200, `{"id":"W_NEW"}`), nil
		}
		return jsonResponse(200, workflowSharedJSON("W_NEW", exclProject)), nil
	}
	_, _, err = run(t, invokeWorkflowsCreate, excludingConnection(nil, bothExclusions...),
		createWorkflowArguments(ownProject), landed)
	if classOf(err) != provider.ClassProviderError {
		t.Fatalf("err = %v, want a provider error", err)
	}
}

func TestExecutionsListPlacesRowsThroughTheirWorkflowOncePerCall(t *testing.T) {
	handler := func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/executions":
			return jsonResponse(200, executionsPageJSONOf(
				executionJSONOf(1, "W_IN", "success"), executionJSONOf(2, "W_OUT", "success"),
				executionJSONOf(3, "W_IN", "success"), executionJSONOf(4, "W_GONE", "success"),
				executionJSONOf(5, exclWorkflow, "success"), executionJSONOf(6, "W_OUT", "success"))), nil
		case apiPath + "/workflows/W_IN":
			return jsonResponse(200, workflowSharedJSON("W_IN", ownProject)), nil
		case apiPath + "/workflows/W_OUT":
			return jsonResponse(200, workflowSharedJSON("W_OUT", exclProject)), nil
		case apiPath + "/workflows/W_GONE":
			return jsonResponse(404, `{}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}
	result, calls, err := run(t, invokeExecutionsList, excludingConnection(nil, bothExclusions...), `{}`, handler)
	var page ExecutionsPage
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 2 ||
		page.Executions[0].ID != 1 || page.Executions[1].ID != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	reads := map[string]int{}
	for _, c := range calls {
		reads[c.path]++
	}
	if len(calls) != 4 || reads[apiPath+"/workflows/W_IN"] != 1 || reads[apiPath+"/workflows/W_OUT"] != 1 {
		t.Fatalf("calls = %+v, want one read per distinct workflow", calls)
	}
	// An error other than "unknown workflow" ends the call instead of emptying the page.
	failing := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/executions" {
			return jsonResponse(200, executionsPageJSONOf(executionJSONOf(1, "W_IN", "success"))), nil
		}
		return jsonResponse(401, `{}`), nil
	}
	if _, _, err = run(t, invokeExecutionsList, excludingConnection(nil, bothExclusions...), `{}`, failing); classOf(err) != provider.ClassAuth {
		t.Fatalf("err = %v, want auth", err)
	}
	// Only a workflow exclusion needs no per-row read.
	_, calls, err = run(t, invokeExecutionsList, excludingConnection(nil, "workflow/"+exclWorkflow), `{}`,
		func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, executionsPageJSONOf(executionJSONOf(1, "W_IN", "success"),
				executionJSONOf(2, exclWorkflow, "success"))), nil
		})
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestExecutionsListFilterOnExcludedIsAnEmptyPage(t *testing.T) {
	for _, arguments := range []string{fmt.Sprintf(`{"workflow_id":%q}`, exclWorkflow),
		fmt.Sprintf(`{"project_id":%q}`, exclProject)} {
		result, calls, err := run(t, invokeExecutionsList, excludingConnection(nil, bothExclusions...), arguments, nil)
		if err != nil || len(calls) != 0 || !strings.Contains(result, `"executions":[]`) {
			t.Fatalf("%s: result = %s, err = %v, calls = %+v", arguments, result, err, calls)
		}
	}
	// A bad argument still wins over the exclusion.
	_, _, err := run(t, invokeExecutionsList, excludingConnection(nil, bothExclusions...),
		fmt.Sprintf(`{"workflow_id":%q,"started_after":"x"}`, exclWorkflow), nil)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutionGetRetryAndStopRefuseExcludedWorkflows(t *testing.T) {
	for name, workflowID := range map[string]string{"workflow": exclWorkflow, "project": "W_OUT"} {
		handler := func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case apiPath + "/executions/1":
				return jsonResponse(200, executionJSONOf(1, workflowID, "error")), nil
			case apiPath + "/workflows/W_OUT":
				return jsonResponse(200, workflowSharedJSON("W_OUT", exclProject)), nil
			}
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
		wantRequests := 1
		if name == "project" {
			wantRequests = 2
		}
		for tool, fn := range map[string]invokeFunc{"get": invokeExecutionsGet, "retry": invokeExecutionsRetry,
			"stop": invokeExecutionsStop} {
			t.Run(name+" "+tool, func(t *testing.T) {
				_, calls, err := run(t, fn, excludingConnection(nil, bothExclusions...), `{"execution_id":1}`, handler)
				refused(t, err, calls, wantRequests)
			})
		}
	}
}

func credentialOwnedJSON(id, owner string, shared ...string) string {
	entries := []string{fmt.Sprintf(`{"id":%q,"name":%q,"role":"credential:owner"}`, owner, exclCanary)}
	for _, p := range shared {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"name":"p","role":"credential:user"}`, p))
	}
	return fmt.Sprintf(`{"id":%q,"name":"c","type":"httpBasicAuth","shared":[%s]}`, id, strings.Join(entries, ","))
}

func TestCredentialsFollowSharingIntoReachableProjects(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s,%s],"nextCursor":null}`,
		credentialOwnedJSON("C_IN", ownProject),
		credentialOwnedJSON("C_OUT", exclProject),
		credentialOwnedJSON("C_SHARED", exclProject, ownProject))
	handler := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/credentials" {
			return jsonResponse(200, body), nil
		}
		if r.Method == http.MethodGet {
			return jsonResponse(200, credentialOwnedJSON(strings.TrimPrefix(r.URL.Path, apiPath+"/credentials/"),
				exclProject, ownProject)), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
	resolved := excludingConnection(nil, bothExclusions...)
	result, _, err := run(t, invokeCredentialsList, resolved, `{}`, handler)
	var page CredentialsPage
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 2 ||
		page.Credentials[0].ID != "C_IN" || page.Credentials[1].ID != "C_SHARED" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// The excluded owner of the shared credential is reduced to its ID and role.
	if first := page.Credentials[1].Shared[0]; first.ID != exclProject || first.Name != "" {
		t.Fatalf("shared = %+v", page.Credentials[1].Shared)
	}
	// Readable through the share, refused when only the excluded project holds it.
	if _, _, err = run(t, invokeCredentialsGet, resolved, `{"credential_id":"C_SHARED"}`, handler); err != nil {
		t.Fatalf("get shared = %v", err)
	}
	_, calls, err := run(t, invokeCredentialsGet, resolved, `{"credential_id":"C_OUT"}`, handler)
	refused(t, err, calls, 1)
	_, calls, err = run(t, invokeCredentialsTest, resolved, `{"credential_id":"C_OUT"}`, handler)
	refused(t, err, calls, 1)
}

func TestCredentialChangesNeedAReachableOwner(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":null}`,
		credentialOwnedJSON("C_SHARED", exclProject, ownProject), credentialOwnedJSON("C_OWN", ownProject))
	handler := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/credentials" {
			return jsonResponse(200, body), nil
		}
		return jsonResponse(200, `{}`), nil
	}
	resolved := excludingConnection([]string{"project/" + ownProject}, bothExclusions...)
	for name, tt := range map[string]struct {
		fn        invokeFunc
		arguments string
	}{
		"update":   {invokeCredentialsUpdate, `{"credential_id":"C_SHARED","name":"n"}`},
		"delete":   {invokeCredentialsDelete, `{"credential_id":"C_SHARED"}`},
		"transfer": {invokeCredentialsTransfer, `{"credential_id":"C_SHARED","destination_project_id":"` + ownProject + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, calls, err := run(t, tt.fn, resolved, tt.arguments, handler)
			refused(t, err, calls, 1)
		})
	}
	// A reachable owner can still be changed.
	if _, calls, err := run(t, invokeCredentialsDelete, resolved, `{"credential_id":"C_OWN"}`, handler); err != nil ||
		len(calls) != 2 || calls[1].method != http.MethodDelete {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestCredentialTargetsAreRefusedBeforeTheRequest(t *testing.T) {
	resolved := excludingConnection([]string{"project/" + ownProject}, bothExclusions...)
	create := func(project string) string {
		extra := ""
		if project != "" {
			extra = fmt.Sprintf(`,"project_id":%q`, project)
		}
		return `{"name":"n","credential_type":"httpBasicAuth","data":{"user":"u"}` + extra + `}`
	}
	_, calls, err := run(t, invokeCredentialsCreate, resolved, create(exclProject), nil)
	refused(t, err, calls, 0)
	_, calls, err = run(t, invokeCredentialsTransfer, resolved,
		`{"credential_id":"C","destination_project_id":"`+exclProject+`"}`, nil)
	refused(t, err, calls, 0)
	// Without a project allow-list, no project_id would mean the key owner's personal project.
	_, calls, err = run(t, invokeCredentialsCreate, excludingConnection(nil, bothExclusions...), create(""), nil)
	refused(t, err, calls, 0)
	_, calls, err = run(t, invokeCredentialsCreate, excludingConnection(nil, "workflow/"+exclWorkflow), create(""),
		func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `{"id":"C","name":"n"}`), nil })
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestVariablesHideRefuseAndKeepGlobals(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s,%s],"nextCursor":null}`,
		variableJSONOf("V_IN", "IN", "v", ownProject), variableJSONOf("V_OUT", "OUT", exclCanary, exclProject),
		variableJSONOf("V_GLOBAL", "GLOBAL", "g", ""))
	handler := func(r *http.Request) (*http.Response, error) { return jsonResponse(200, body), nil }
	resolved := excludingConnection(nil, bothExclusions...)
	result, _, err := run(t, invokeVariablesList, resolved, `{}`, handler)
	var page VariablesPage
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 2 ||
		strings.Contains(result, exclCanary) || page.Variables[1].Scope != "global" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	result, calls, err := run(t, invokeVariablesList, resolved, fmt.Sprintf(`{"project_id":%q}`, exclProject), nil)
	if err != nil || len(calls) != 0 || !strings.Contains(result, `"variables":[]`) {
		t.Fatalf("filter: %s, %v, %+v", result, err, calls)
	}
	for name, tt := range map[string]struct {
		fn        invokeFunc
		arguments string
		requests  int
	}{
		"create": {invokeVariablesCreate, fmt.Sprintf(`{"key":"K","value":"v","project_id":%q}`, exclProject), 0},
		"update": {invokeVariablesUpdate, `{"variable_id":"V_OUT","key":"K","value":"v"}`, 1},
		"delete": {invokeVariablesDelete, `{"variable_id":"V_OUT"}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, calls, err := run(t, tt.fn, resolved, tt.arguments, handler)
			refused(t, err, calls, tt.requests)
		})
	}
	// Global variables stay creatable.
	if _, calls, err = run(t, invokeVariablesCreate, resolved, `{"key":"K","value":"v"}`,
		func(r *http.Request) (*http.Response, error) { return jsonResponse(201, ``), nil }); err != nil || len(calls) != 1 {
		t.Fatalf("global create: %v, %+v", err, calls)
	}
}

func TestDataTablesColumnsAndRowsFollowTheProject(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":null}`, tableJSONOf("T_IN", "in", ownProject),
		tableJSONOf("T_OUT", exclCanary, exclProject))
	handler := func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/data-tables":
			return jsonResponse(200, body), nil
		case strings.HasPrefix(r.URL.Path, apiPath+"/data-tables/T_OUT"):
			return jsonResponse(200, tableJSONOf("T_OUT", exclCanary, exclProject)), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
	resolved := excludingConnection(nil, bothExclusions...)
	result, _, err := run(t, invokeDataTablesList, resolved, `{}`, handler)
	var page DataTablesPage
	if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Count != 1 || strings.Contains(result, exclCanary) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	result, calls, err := run(t, invokeDataTablesList, resolved, fmt.Sprintf(`{"project_id":%q}`, exclProject), nil)
	if err != nil || len(calls) != 0 || !strings.Contains(result, `"data_tables":[]`) {
		t.Fatalf("filter: %s, %v, %+v", result, err, calls)
	}
	for name, tt := range map[string]struct {
		fn        invokeFunc
		arguments string
	}{
		"get":          {invokeDataTablesGet, `{"data_table_id":"T_OUT"}`},
		"rename":       {invokeDataTablesRename, `{"data_table_id":"T_OUT","name":"x"}`},
		"delete":       {invokeDataTablesDelete, `{"data_table_id":"T_OUT"}`},
		"columns list": {invokeDataColumnsList, `{"data_table_id":"T_OUT"}`},
		"columns add":  {invokeDataColumnsAdd, `{"data_table_id":"T_OUT","name":"c","type":"string"}`},
		"rows list":    {invokeDataRowsList, `{"data_table_id":"T_OUT"}`},
		"rows insert":  {invokeDataRowsInsert, `{"data_table_id":"T_OUT","rows":[{"email":"a"}]}`},
		"rows delete":  {invokeDataRowsDelete, `{"data_table_id":"T_OUT","filter":{"conditions":[{"column":"email","operator":"eq","value":"a"}]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, calls, err := run(t, tt.fn, resolved, tt.arguments, handler)
			refused(t, err, calls, 1)
		})
	}
	// create: an excluded project, or no project at all, is refused before the request.
	_, calls, err = run(t, invokeDataTablesCreate, resolved, fmt.Sprintf(`{"name":"t","project_id":%q}`, exclProject), nil)
	refused(t, err, calls, 0)
	_, calls, err = run(t, invokeDataTablesCreate, resolved, `{"name":"t"}`, nil)
	refused(t, err, calls, 0)
}

func TestProjectsListUpdateDeleteAndMembers(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":null}`, projectJSONOf(ownProject, "own"),
		projectJSONOf(exclProject, exclCanary))
	resolved := excludingConnection([]string{"project/" + ownProject}, bothExclusions...)
	open := excludingConnection(nil, bothExclusions...)
	result, _, err := run(t, invokeProjectsList, open, `{}`, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, body), nil
	})
	if err != nil || strings.Contains(result, exclCanary) || !strings.Contains(result, ownProject) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// An excluded project is refused for every change, members included, before any request. The gates need
	// a project target, which an excluded project is never part of.
	withTarget := excludingConnection([]string{"project/" + ownProject}, "project/"+exclProject)
	for name, tt := range map[string]struct {
		fn        invokeFunc
		arguments string
	}{
		"update":      {invokeProjectsUpdate, fmt.Sprintf(`{"project_id":%q,"name":"n"}`, exclProject)},
		"delete":      {invokeProjectsDelete, fmt.Sprintf(`{"project_id":%q}`, exclProject)},
		"members":     {invokeMembersList, fmt.Sprintf(`{"project_id":%q}`, exclProject)},
		"member add":  {invokeMembersAdd, fmt.Sprintf(`{"project_id":%q,"user_id":"U","role":"project:viewer"}`, exclProject)},
		"member role": {invokeMembersSetRole, fmt.Sprintf(`{"project_id":%q,"user_id":"U","role":"project:viewer"}`, exclProject)},
		"member drop": {invokeMembersRemove, fmt.Sprintf(`{"project_id":%q,"user_id":"U"}`, exclProject)},
	} {
		t.Run(name, func(t *testing.T) {
			// The excluded project sits outside the targets here, which is refused just the same.
			_, calls, err := run(t, tt.fn, withTarget, tt.arguments, nil)
			refused(t, err, calls, 0)
		})
	}
	_ = resolved
}

func TestProjectDeleteRefusesAProjectOwningAnExcludedWorkflow(t *testing.T) {
	resolved := excludingConnection([]string{"project/" + ownProject}, "workflow/"+exclWorkflow)
	arguments := fmt.Sprintf(`{"project_id":%q}`, ownProject)
	serveWorkflow := func(status int, body string) func(*http.Request) (*http.Response, error) {
		return func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == apiPath+"/workflows/"+exclWorkflow:
				return jsonResponse(status, body), nil
			case r.Method == http.MethodDelete && r.URL.Path == apiPath+"/projects/"+ownProject:
				return jsonResponse(200, `{}`), nil
			}
			t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	// Owned by the project: no transfer target exists, so the deletion is refused and nothing is deleted.
	_, calls, err := run(t, invokeProjectsDelete, resolved, arguments, serveWorkflow(200, workflowSharedJSON(exclWorkflow, ownProject)))
	refused(t, err, calls, 1)
	// No owner reported: cannot be proven, refused.
	_, calls, err = run(t, invokeProjectsDelete, resolved, arguments,
		serveWorkflow(200, `{"id":"`+exclWorkflow+`","name":"W","nodes":[],"connections":{}}`))
	refused(t, err, calls, 1)
	// Owned elsewhere and only shared into this project: the deletion does not reach it.
	shared := fmt.Sprintf(`{"id":%q,"name":"W","nodes":[],"connections":{},"shared":[`+
		`{"role":"workflow:owner","projectId":%q},{"role":"workflow:editor","projectId":%q}]}`,
		exclWorkflow, foreignProject, ownProject)
	if _, calls, err = run(t, invokeProjectsDelete, resolved, arguments, serveWorkflow(200, shared)); err != nil ||
		len(calls) != 2 || calls[1].method != http.MethodDelete {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// An excluded workflow that does not exist is owned by nobody.
	if _, calls, err = run(t, invokeProjectsDelete, resolved, arguments, serveWorkflow(404, `{}`)); err != nil ||
		len(calls) != 2 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestTagsStayAvailableWithAWarning(t *testing.T) {
	for _, d := range []string{tagsList.Description, tagsGet.Description, tagsCreate.Description,
		tagsUpdate.Description, tagsDelete.Description} {
		if !strings.Contains(d, "Exclusions do not apply") {
			t.Fatalf("description lacks the exclusion warning: %q", d)
		}
	}
	_, calls, err := run(t, invokeTagsList, excludingConnection(nil, bothExclusions...), `{}`,
		func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
		})
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}
