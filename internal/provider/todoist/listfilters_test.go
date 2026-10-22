package todoist

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// The workspace and folder filters reach Todoist under their exact names, and the connection still holds
// every returned row.
func TestWorkspaceAndFolderFiltersReachTodoistAndStayBound(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct {
		operation, arguments, path string
		want                       url.Values
	}{
		{"todoist.projects.list", `{"workspace_id":42,"folder_id":7}`, "/projects",
			url.Values{"workspace_id": {"42"}, "folder_id": {"7"}}},
		{"todoist.sections.list", `{"workspace_id":42}`, "/sections", url.Values{"workspace_id": {"42"}}},
		{"todoist.sections.list", `{"workspace_id":42,"search":"x"}`, "/sections/search",
			url.Values{"workspace_id": {"42"}, "query": {"x"}}},
	} {
		before := len(env.fake.recorded())
		result, err := env.invoke(tt.operation, "multi", tt.arguments)
		if err != nil {
			t.Fatalf("%s %s = %v", tt.operation, tt.arguments, err)
		}
		requests := env.fake.recorded()
		if len(requests) != before+1 {
			t.Fatalf("%s sent %d requests, want one", tt.operation, len(requests)-before)
		}
		if !strings.HasSuffix(requests[before].path, tt.path) {
			t.Errorf("%s %s sent to %s, want %s", tt.operation, tt.arguments, requests[before].path, tt.path)
		}
		for name, values := range tt.want {
			if got := requests[before].query.Get(name); got != values[0] {
				t.Errorf("%s sent %s=%q, want %q", tt.operation, name, got, values[0])
			}
		}
		if strings.Contains(result, "Forgn") || strings.Contains(result, "sectForeign") ||
			strings.Contains(result, foreignCanary) {
			t.Errorf("%s = %s; want rows outside the connection dropped even with a filter", tt.operation, result)
		}
	}
}

// Invalid or contradicting filters are refused before any request.
func TestWorkspaceAndFolderFiltersAreCheckedBeforeTheRequest(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct{ operation, connection, arguments string }{
		{"todoist.projects.list", "multi", `{"search":"x","workspace_id":1}`},
		{"todoist.projects.list", "multi", `{"search":"x","folder_id":1}`},
		{"todoist.projects.list", "multi", `{"workspace_id":0}`},
		{"todoist.projects.list", "multi", `{"folder_id":-1}`},
		{"todoist.projects.list", "multi", `{"folder_id":"7"}`},
		{"todoist.sections.list", "multi", `{"project_id":"` + ownProject + `","workspace_id":1}`},
		{"todoist.sections.list", "one", `{"workspace_id":1}`},
		{"todoist.sections.list", "multi", `{"workspace_id":0}`},
		{"todoist.sections.list", "multi", `{"workspace_id":-3,"search":"x"}`},
		{"todoist.sections.list", "multi", `{"folder_id":1}`},
	} {
		if _, err := env.invoke(tt.operation, tt.connection, tt.arguments); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.operation, tt.arguments, err)
		}
	}
	if got := env.fake.recorded(); len(got) != 0 {
		t.Errorf("a refused filter reached Todoist: %+v", got)
	}
}

// A cursor is bound to the workspace and folder filters.
func TestCursorIsBoundToWorkspaceAndFolder(t *testing.T) {
	f := &fakeTodoist{next: followCursor}
	env := newEnvironment(t, f)
	for _, tt := range []struct {
		operation, base string
		changed         []string
	}{
		{"todoist.projects.list", `{"workspace_id":1,"folder_id":2`,
			[]string{`{"workspace_id":3,"folder_id":2`, `{"workspace_id":1,"folder_id":3`, `{"workspace_id":1`,
				`{"folder_id":2`, `{`}},
		{"todoist.sections.list", `{"workspace_id":1`, []string{`{"workspace_id":2`, `{`}},
	} {
		result, err := env.invoke(tt.operation, "multi", tt.base+`}`)
		var listed struct {
			NextCursor string `json:"next_cursor"`
		}
		if err != nil || json.Unmarshal([]byte(result), &listed) != nil || listed.NextCursor == "" {
			t.Fatalf("%s = %s, %v; want a cursor", tt.operation, result, err)
		}
		if _, err := env.invoke(tt.operation, "multi", tt.base+`,"cursor":"`+listed.NextCursor+`"}`); err != nil {
			t.Errorf("%s with its own cursor = %v", tt.operation, err)
		}
		before := len(f.recorded())
		for _, changed := range tt.changed {
			if _, err := env.invoke(tt.operation, "multi", changed+`,"cursor":"`+listed.NextCursor+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s %s with a foreign cursor = %v, want an invalid request", tt.operation, changed, err)
			}
		}
		if len(f.recorded()) != before {
			t.Errorf("%s: a refused cursor reached Todoist", tt.operation)
		}
	}
}
