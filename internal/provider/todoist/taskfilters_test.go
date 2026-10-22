package todoist

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// The added list filters reach Todoist under their exact parameter names, and the connection still holds
// every returned task.
func TestTaskListFiltersReachTodoistAndStayBound(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	window := `"since":"2026-09-01T00:00:00Z","until":"2026-09-30T00:00:00Z"`
	for _, tt := range []struct {
		operation, connection, arguments string
		want                             url.Values
	}{
		{"todoist.tasks.list", "one", `{"ids":["taskDue","taskForeign"],"label":"waiting"}`,
			url.Values{"ids": {"taskDue,taskForeign"}, "label": {"waiting"}}},
		{"todoist.tasks.filter", "one", `{"query":"heute","lang":"de"}`, url.Values{"query": {"heute"}, "lang": {"de"}}},
		{"todoist.completed-tasks.list", "one",
			`{` + window + `,"parent_id":"taskDue","workspace_id":42,"query":"heute","filter_lang":"de-AT"}`,
			url.Values{"parent_id": {"taskDue"}, "workspace_id": {"42"}, "filter_query": {"heute"}, "filter_lang": {"de-AT"}}},
	} {
		before := len(env.fake.recorded())
		result, err := env.invoke(tt.operation, tt.connection, tt.arguments)
		if err != nil {
			t.Fatalf("%s %s = %v", tt.operation, tt.arguments, err)
		}
		requests := env.fake.recorded()
		if len(requests) != before+1 {
			t.Fatalf("%s sent %d requests, want one", tt.operation, len(requests)-before)
		}
		for name, values := range tt.want {
			if got := requests[before].query.Get(name); got != values[0] {
				t.Errorf("%s sent %s=%q, want %q", tt.operation, name, got, values[0])
			}
		}
		if strings.Contains(result, "Foreign") {
			t.Errorf("%s = %s; want tasks outside the connection dropped even when asked for", tt.operation, result)
		}
		var listed TaskList
		if err := json.Unmarshal([]byte(result), &listed); err != nil || len(listed.Tasks) == 0 {
			t.Errorf("%s = %s, %v; want the tasks of the connection", tt.operation, result, err)
		}
	}
}

// Invalid filter values are refused before any request.
func TestTaskListFiltersAreCheckedBeforeTheRequest(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	window := `"since":"2026-09-01T00:00:00Z","until":"2026-09-30T00:00:00Z"`
	var many []string
	for i := 0; i < 101; i++ {
		many = append(many, `"t`+strings.Repeat("a", i%5)+string(rune('A'+i%26))+strings.Repeat("b", i/26)+`"`)
	}
	for _, tt := range []struct{ operation, arguments string }{
		{"todoist.tasks.list", `{"ids":[]}`},
		{"todoist.tasks.list", `{"ids":[` + strings.Join(many, ",") + `]}`},
		{"todoist.tasks.list", `{"ids":["taskDue","taskDue"]}`},
		{"todoist.tasks.list", `{"ids":["task Due"]}`},
		{"todoist.tasks.list", `{"ids":"taskDue"}`},
		{"todoist.tasks.filter", `{"query":"today","lang":"d"}`},
		{"todoist.tasks.filter", `{"query":"today","lang":"de_DE"}`},
		{"todoist.tasks.filter", `{"query":"today","lang":"abcd"}`},
		{"todoist.tasks.filter", `{"query":"today","lang":"de-123456789"}`},
		{"todoist.completed-tasks.list", `{` + window + `,"filter_lang":"de"}`},
		{"todoist.completed-tasks.list", `{` + window + `,"query":"today","filter_lang":"de_DE"}`},
		{"todoist.completed-tasks.list", `{` + window + `,"parent_id":"bad id"}`},
		{"todoist.completed-tasks.list", `{` + window + `,"workspace_id":0}`},
		{"todoist.completed-tasks.list", `{` + window + `,"workspace_id":-3}`},
		{"todoist.completed-tasks.list", `{` + window + `,"public_key":"abc"}`},
	} {
		if _, err := env.invoke(tt.operation, "one", tt.arguments); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.operation, tt.arguments, err)
		}
	}
	if got := env.fake.recorded(); len(got) != 0 {
		t.Errorf("a refused filter reached Todoist: %+v", got)
	}
}

// A cursor is bound to every argument, the added filters included.
func TestCursorIsBoundToTheAddedFilters(t *testing.T) {
	f := &fakeTodoist{next: followCursor}
	env := newEnvironment(t, f)
	window := `"since":"2026-09-01T00:00:00Z","until":"2026-09-30T00:00:00Z"`
	for _, tt := range []struct {
		operation, base string
		changed         []string
	}{
		{"todoist.tasks.list", `{"ids":["taskDue"]`, []string{`{"ids":["taskOpen"]`, `{"ids":["taskDue","taskOpen"]`, `{`}},
		{"todoist.tasks.filter", `{"query":"today","lang":"de"`, []string{`{"query":"today","lang":"en"`, `{"query":"today"`}},
		{"todoist.completed-tasks.list", `{` + window + `,"parent_id":"taskDue","workspace_id":1,"query":"x","filter_lang":"de"`,
			[]string{
				`{` + window + `,"parent_id":"taskOpen","workspace_id":1,"query":"x","filter_lang":"de"`,
				`{` + window + `,"parent_id":"taskDue","workspace_id":2,"query":"x","filter_lang":"de"`,
				`{` + window + `,"parent_id":"taskDue","workspace_id":1,"query":"x","filter_lang":"en"`,
				`{` + window + `,"workspace_id":1,"query":"x","filter_lang":"de"`,
			}},
	} {
		result, err := env.invoke(tt.operation, "one", tt.base+`}`)
		var listed struct {
			NextCursor string `json:"next_cursor"`
		}
		if err != nil || json.Unmarshal([]byte(result), &listed) != nil || listed.NextCursor == "" {
			t.Fatalf("%s = %s, %v; want a cursor", tt.operation, result, err)
		}
		if _, err := env.invoke(tt.operation, "one", tt.base+`,"cursor":"`+listed.NextCursor+`"}`); err != nil {
			t.Errorf("%s with its own cursor = %v", tt.operation, err)
		}
		before := len(f.recorded())
		for _, changed := range tt.changed {
			if _, err := env.invoke(tt.operation, "one", changed+`,"cursor":"`+listed.NextCursor+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s %s with a foreign cursor = %v, want an invalid request", tt.operation, changed, err)
			}
		}
		if len(f.recorded()) != before {
			t.Errorf("%s: a refused cursor reached Todoist", tt.operation)
		}
	}
}
