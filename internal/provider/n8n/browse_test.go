package n8n

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browse(t *testing.T, handler func(*http.Request) (*http.Response, error), req config.TargetListRequest,
	targets ...string) (config.TargetPage, []call, error) {
	t.Helper()
	var calls []call
	serve(t, &calls, handler)
	page, err := listTargets(context.Background(), resolvedConnection(targets...), resolver(&redact.Redactor{}, nil),
		&redact.Redactor{}, req)
	return page, calls, err
}

func TestBrowseProjectsPagesWithoutTargetFilter(t *testing.T) {
	page, calls, err := browse(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("cursor") == "c2" {
			return jsonResponse(200, `{"data":[`+projectJSONOf("p3", "Third")+`],"nextCursor":null}`), nil
		}
		return jsonResponse(200, `{"data":[`+projectJSONOf("p1", "One\nline")+`,`+projectJSONOf("bad id", "x")+
			`,`+projectJSONOf("p2", "Two")+`],"nextCursor":"c2"}`), nil
	}, config.TargetListRequest{Kind: "project"}, "project/"+ownProject, "workflow/"+ownWorkflow)
	if err != nil || len(calls) != 1 || calls[0].path != apiPath+"/projects" || calls[0].query.Get("cursor") != "" {
		t.Fatalf("first page = %v, %v", calls, err)
	}
	if page.Next != "c2" || page.ServerSearch || len(page.Entries) != 2 ||
		page.Entries[0] != (config.TargetEntry{Target: "project/p1", Name: "One line", Kind: "project", HasChildren: true}) {
		t.Fatalf("page = %+v", page)
	}
	page, _, err = browse(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("cursor") != "c2" {
			t.Fatalf("cursor = %q", r.URL.Query().Get("cursor"))
		}
		return jsonResponse(200, `{"data":[`+projectJSONOf("p3", "Third")+`],"nextCursor":null}`), nil
	}, config.TargetListRequest{Kind: "project", Cursor: "c2"})
	if err != nil || page.Next != "" || len(page.Entries) != 1 || page.Entries[0].Target != "project/p3" {
		t.Fatalf("second page = %+v, %v", page, err)
	}
}

func workflowRow(id, name string, archived bool, projects ...string) string {
	shared := make([]string, 0, len(projects))
	for _, p := range projects {
		shared = append(shared, fmt.Sprintf(`{"role":"workflow:owner","projectId":%q}`, p))
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"active":false,"isArchived":%t,"shared":[%s]}`, id, name, archived,
		strings.Join(shared, ","))
}

func TestBrowseWorkflowsUsesServerFilters(t *testing.T) {
	page, calls, err := browse(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[`+workflowRow("w1", "Invoice", false, "p1")+`,`+
			workflowRow("w2", "Foreign", false, "p9")+`,`+workflowRow("w3", "Old", true, "p1")+`,`+
			workflowRow("w4", "NoShared", false)+`],"nextCursor":"n"}`), nil
	}, config.TargetListRequest{Kind: "workflow", Parent: "project/p1", Query: "inv", Cursor: "c1"}, "workflow/"+ownWorkflow)
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls = %v, %v", calls, err)
	}
	q := calls[0].query
	if calls[0].path != apiPath+"/workflows" || q.Get("projectId") != "p1" || q.Get("name") != "inv" ||
		q.Get("cursor") != "c1" {
		t.Fatalf("request = %v", calls[0])
	}
	var targets []string
	for _, e := range page.Entries {
		targets = append(targets, e.Target)
	}
	if !slices.Equal(targets, []string{"workflow/w1", "workflow/w4"}) || page.Next != "n" || !page.ServerSearch {
		t.Fatalf("page = %+v", page)
	}
	page, _, _ = browse(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	}, config.TargetListRequest{Kind: "workflow", Parent: "project/p1"})
	if page.ServerSearch {
		t.Fatal("ServerSearch set without a query")
	}
}

func TestBrowseRefusesBadRequestsWithoutRequest(t *testing.T) {
	for _, req := range []config.TargetListRequest{
		{Kind: "workflow"},
		{Kind: "workflow", Parent: "workflow/w1"},
		{Kind: "workflow", Parent: "project/../x"},
		{Kind: "folder", Parent: "project/p1"},
		{Kind: "project", Cursor: strings.Repeat("a", maxCursorLength+1)},
	} {
		_, calls, err := browse(t, noRequest(t), req)
		if !isInvalidRequest(err) || len(calls) != 0 {
			t.Fatalf("%+v: err = %v, calls = %v", req, err, calls)
		}
	}
}

func TestBrowseClassifiesFailuresWithoutProviderText(t *testing.T) {
	_, _, err := browse(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	}, config.TargetListRequest{Kind: "project"})
	if classOf(err) == "" || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveTargetURL(t *testing.T) {
	tests := []struct {
		name, origin, raw string
		want              []string
	}{
		{"workflow", baseURL, baseURL + "/workflow/abc123", []string{"workflow/abc123"}},
		{"workflow with node", baseURL, baseURL + "/workflow/abc123/node-1?x=1", []string{"workflow/abc123"}},
		{"new workflow", baseURL, baseURL + "/workflow/new", nil},
		{"project workflows", baseURL, baseURL + "/projects/p1/workflows", []string{"project/p1"}},
		{"project folder", baseURL, baseURL + "/projects/p1/folders/f1/workflows", []string{"project/p1"}},
		{"project settings", baseURL, baseURL + "/projects/p1/settings", []string{"project/p1"}},
		{"host case", baseURL, "https://N8N.Example.invalid/workflow/w1", []string{"workflow/w1"}},
		{"path prefix", baseURL + "/n8n", baseURL + "/n8n/workflow/w1", []string{"workflow/w1"}},
		{"path prefix project", baseURL + "/n8n/", baseURL + "/n8n/projects/p1/workflows", []string{"project/p1"}},
		{"missing prefix", baseURL + "/n8n", baseURL + "/workflow/w1", nil},
		{"prefix boundary", baseURL + "/n8n", baseURL + "/n8nx/workflow/w1", nil},
		{"foreign origin", baseURL, "https://other.example.invalid/workflow/w1", nil},
		{"http", baseURL, "http://n8n.example.invalid/workflow/w1", nil},
		{"missing workflow id", baseURL, baseURL + "/workflow", nil},
		{"missing workflow id slash", baseURL, baseURL + "/workflow/", nil},
		{"missing project id", baseURL, baseURL + "/projects", nil},
		{"bad id", baseURL, baseURL + "/workflow/a%20b", nil},
		{"other route", baseURL, baseURL + "/home/workflows", nil},
		{"no origin", "", baseURL + "/workflow/w1", nil},
		{"not a url", baseURL, "%%%", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			serve(t, &calls, noRequest(t))
			entries, err := resolveTargetURL(context.Background(), nil, nil, nil, tt.origin, tt.raw)
			if err != nil || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %v", err, calls)
			}
			var got []string
			for _, e := range entries {
				got = append(got, e.Target)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	if _, err := url.Parse(baseURL); err != nil {
		t.Fatal(err)
	}
}

func TestKindsAreTwoLevelsAndBrowserIsRegistered(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	workflow, _ := metadata.Target.KindByName("workflow")
	project, _ := metadata.Target.KindByName("project")
	if strings.Join(workflow.Parents, ",") != "project" || strings.Join(project.Parents, ",") != "" {
		t.Fatalf("kinds = %+v, %+v", project, workflow)
	}
	serve(t, &[]call{}, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	})
	if _, err := reg.ListTargets(context.Background(), resolvedConnection(), resolver(&redact.Redactor{}, nil),
		&redact.Redactor{}, config.TargetListRequest{Kind: "project"}); err != nil {
		t.Fatalf("ListTargets() = %v", err)
	}
}

func TestReachesFollowsWhatTheCallRefuses(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	instanceWide := []string{"n8n.tags.list", "n8n.tags.get", "n8n.tags.create", "n8n.tags.update", "n8n.tags.delete",
		"n8n.users.list", "n8n.users.get", "n8n.users.set-role", "n8n.users.delete", "n8n.audit.generate",
		"n8n.source-control.status", "n8n.source-control.pull", "n8n.source-control.push", "n8n.projects.create"}
	projectOnly := []string{"n8n.credentials.list", "n8n.credentials.get", "n8n.credentials.test",
		"n8n.credentials.schema", "n8n.credentials.create", "n8n.credentials.update", "n8n.credentials.transfer",
		"n8n.credentials.delete", "n8n.variables.list", "n8n.variables.create", "n8n.variables.update",
		"n8n.variables.delete", "n8n.data-tables.list", "n8n.data-tables.get", "n8n.data-tables.create",
		"n8n.data-tables.rename", "n8n.data-tables.delete", "n8n.data-columns.list", "n8n.data-columns.add",
		"n8n.data-columns.update", "n8n.data-columns.delete", "n8n.data-rows.list", "n8n.data-rows.insert",
		"n8n.data-rows.update", "n8n.data-rows.upsert", "n8n.data-rows.delete", "n8n.project-members.list",
		"n8n.project-members.add", "n8n.project-members.set-role", "n8n.project-members.remove",
		"n8n.projects.update", "n8n.projects.delete", "n8n.workflows.create"}
	always := []string{"n8n.workflows.list", "n8n.workflows.get", "n8n.workflows.update", "n8n.workflows.activate",
		"n8n.workflows.deactivate", "n8n.executions.list", "n8n.executions.get", "n8n.executions.retry",
		"n8n.executions.stop", "n8n.projects.list"}
	if len(instanceWide)+len(projectOnly)+len(always) != len(metadata.Tools) {
		t.Fatalf("table covers %d tools, provider has %d", len(instanceWide)+len(projectOnly)+len(always),
			len(metadata.Tools))
	}
	for _, tool := range metadata.Tools {
		if !slices.Contains(instanceWide, tool.ID) && !slices.Contains(projectOnly, tool.ID) &&
			!slices.Contains(always, tool.ID) {
			t.Fatalf("%s is in no reach class", tool.ID)
		}
	}
	check := func(targets []string, tool string, wantReached bool, wantHint string) {
		t.Helper()
		reached, hint := metadata.Target.Reach(tool, targets)
		if reached != wantReached || hint != wantHint {
			t.Errorf("%v %s = %t %q, want %t %q", targets, tool, reached, hint, wantReached, wantHint)
		}
	}
	project, workflow := []string{"project/p1"}, []string{"workflow/w1"}
	both := []string{"project/p1", "workflow/w1"}
	for _, tool := range append(append(instanceWide, projectOnly...), always...) {
		check(nil, tool, true, "")
		check([]string{"project/bad id"}, tool, true, "")
	}
	for _, tool := range instanceWide {
		for _, targets := range [][]string{project, workflow, both} {
			check(targets, tool, false, "")
		}
	}
	for _, tool := range projectOnly {
		check(project, tool, true, "")
		check(workflow, tool, false, "needs project/PROJECT_ID")
		check(both, tool, false, "")
	}
	for _, tool := range always {
		for _, targets := range [][]string{project, workflow, both} {
			check(targets, tool, true, "")
		}
	}
}

// Every tool reported as not reached is also refused at the call, locally, before any request.
func TestNotReachedToolsAreStillRefusedAtTheCall(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	for _, tool := range []string{"n8n.tags.list", "n8n.users.list", "n8n.audit.generate", "n8n.source-control.status",
		"n8n.projects.create"} {
		for _, connection := range []string{"project", "workflow", "both"} {
			if _, err := env.invoke(tool, connection, `{}`); err == nil {
				t.Errorf("%s on %s did not fail", tool, connection)
			}
		}
	}
	for _, tool := range []string{"n8n.credentials.list", "n8n.variables.list", "n8n.data-tables.list",
		"n8n.projects.update", "n8n.project-members.list", "n8n.workflows.create"} {
		if _, err := env.invoke(tool, "workflow", `{}`); err == nil {
			t.Errorf("%s on workflow did not fail", tool)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("requests sent: %v", calls)
	}
}
