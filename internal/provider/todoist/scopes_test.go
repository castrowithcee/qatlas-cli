package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The places of the scope fake: a project tree, a project outside it, and the sections of the outside project.
const (
	treeRoot   = "projTree"
	treeKid    = "projKid"
	treeGrand  = "projGrand"
	outside    = "projOutside"
	boundSect  = "sectBound"
	otherSect  = "sectOther"
	kidSect    = "sectKid"
	outsideTag = "outside-canary-91ab"
)

type scopeTask struct{ id, project, section string }

// scopeTodoist is a Todoist whose tree and sections are known and whose project list grows when a project is
// created. Like the fake of the other tests it ignores every filter parameter.
type scopeTodoist struct {
	fakeTodoist
	state    sync.Mutex
	projects [][2]string
}

func newScopeTodoist() *scopeTodoist {
	return &scopeTodoist{projects: [][2]string{{treeRoot, ""}, {treeKid, treeRoot}, {treeGrand, treeKid},
		{outside, ""}}}
}

var (
	scopeSections = map[string]string{boundSect: outside, otherSect: outside, kidSect: treeKid}
	scopeTasks    = []scopeTask{{"taskRoot", treeRoot, ""}, {"taskKid", treeKid, kidSect}, {"taskOutside", outside, ""},
		{"taskBound", outside, boundSect}, {"taskOtherSect", outside, otherSect}}
)

func scopeProjectJSON(id, parent string) string {
	parentJSON := "null"
	if parent != "" {
		parentJSON = `"` + parent + `"`
	}
	name := "Project " + id
	if id == outside {
		name = outsideTag
	}
	return `{"id":"` + id + `","name":"` + name + `","color":"blue","parent_id":` + parentJSON + `,"is_favorite":false,` +
		`"is_shared":false,"is_archived":false,"inbox_project":false,"view_style":"list"}`
}

func scopeTaskJSON(id, project, section string) string {
	sectionJSON := "null"
	if section != "" {
		sectionJSON = `"` + section + `"`
	}
	return `{"id":"` + id + `","project_id":"` + project + `","section_id":` + sectionJSON + `,"parent_id":null,` +
		`"content":"Task ` + id + `","description":"","labels":[],"priority":1,"due":null,"note_count":0,` +
		`"is_deleted":false}`
}

func (f *scopeTodoist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	record := recorded{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
	var body map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		data, _ := io.ReadAll(r.Body)
		record.body = string(data)
		_ = json.Unmarshal(data, &body)
	}
	f.fakeTodoist.mu.Lock()
	f.requests = append(f.requests, record)
	f.fakeTodoist.mu.Unlock()
	f.state.Lock()
	defer f.state.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	value := func(key string) string {
		text, _ := body[key].(string)
		return text
	}
	w.Header().Set("Content-Type", "application/json")
	var entries []string
	switch {
	case r.Method == http.MethodGet && path == "/projects":
		for _, project := range f.projects {
			entries = append(entries, scopeProjectJSON(project[0], project[1]))
		}
		fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, strings.Join(entries, ","))
	case r.Method == http.MethodGet && path == "/projects/archived":
		fmt.Fprint(w, `{"results":[],"next_cursor":null}`)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/projects/"):
		id := strings.TrimPrefix(path, "/projects/")
		for _, project := range f.projects {
			if project[0] == id {
				fmt.Fprint(w, scopeProjectJSON(project[0], project[1]))
				return
			}
		}
		notFound(w)
	case r.Method == http.MethodPost && path == "/projects":
		f.projects = append(f.projects, [2]string{"projNew", value("parent_id")})
		fmt.Fprint(w, scopeProjectJSON("projNew", value("parent_id")))
	case r.Method == http.MethodGet && path == "/sections":
		for id, project := range scopeSections {
			entries = append(entries, sectionJSON(id, project))
		}
		slices.Sort(entries)
		fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, strings.Join(entries, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/sections/"):
		id := strings.TrimPrefix(path, "/sections/")
		if project, ok := scopeSections[id]; ok {
			fmt.Fprint(w, sectionJSON(id, project))
			return
		}
		notFound(w)
	case r.Method == http.MethodGet && (path == "/tasks" || path == "/tasks/filter"):
		for _, task := range scopeTasks {
			entries = append(entries, scopeTaskJSON(task.id, task.project, task.section))
		}
		fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, strings.Join(entries, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/tasks/"):
		id := strings.TrimPrefix(path, "/tasks/")
		for _, task := range scopeTasks {
			if task.id == id {
				fmt.Fprint(w, scopeTaskJSON(task.id, task.project, task.section))
				return
			}
		}
		notFound(w)
	case r.Method == http.MethodPost && path == "/tasks":
		fmt.Fprint(w, scopeTaskJSON("taskNew", value("project_id"), value("section_id")))
	case r.Method == http.MethodPost && path == "/sync":
		fmt.Fprint(w, `{"filters":[]}`)
	case r.Method == http.MethodGet && path == "/comments":
		fmt.Fprint(w, `{"results":[{"id":"noteBound","item_id":"taskBound","content":"hello","is_deleted":false},`+
			`{"id":"noteOutside","item_id":"taskOutside","content":"`+outsideTag+`","is_deleted":false}],"next_cursor":null}`)
	default:
		notFound(w)
	}
}

func scopeEnvironment(t *testing.T, f *scopeTodoist) *environment {
	t.Helper()
	serve(t, f)
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"td": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"td-reader": credential},
		Connections: map[string]config.Connection{
			"tree":    {Service: "td", Credential: "td-reader", Target: treePrefix + treeRoot, Effects: changePermissions},
			"bare":    {Service: "td", Credential: "td-reader", Target: treeRoot, Effects: changePermissions},
			"section": {Service: "td", Credential: "td-reader", Target: sectionPrefix + boundSect, Effects: changePermissions},
			"union": {Service: "td", Credential: "td-reader", Effects: changePermissions,
				Targets: []string{treePrefix + treeRoot, sectionPrefix + boundSect}},
			"account": {Service: "td", Credential: "td-reader", Target: wildcard, Effects: changePermissions},
		},
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{fake: &f.fakeTodoist, core: application.New(registry(t), cfg, resolver(red, &reads), red),
		red: red, reads: &reads}
}

func (e *environment) run(t *testing.T, operation, connection, arguments string, confirmed bool) (string, error) {
	t.Helper()
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{Operation: operation,
		Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: confirmed})
	return string(response.Result), err
}

// A project with its subprojects is one scope; a bare project ID stays exactly that project.
func TestProjectTreeReachesSubprojectsAndBareIDDoesNot(t *testing.T) {
	env := scopeEnvironment(t, newScopeTodoist())
	for _, task := range []struct {
		id      string
		tree    bool
		bare    bool
		section bool
	}{{"taskRoot", true, true, false}, {"taskKid", true, false, false}, {"taskOutside", false, false, false},
		{"taskBound", false, false, true}} {
		for connection, want := range map[string]bool{"tree": task.tree, "bare": task.bare, "section": task.section} {
			result, err := env.run(t, "todoist.tasks.get", connection, `{"task_id":"`+task.id+`"}`, false)
			if (err == nil) != want {
				t.Errorf("tasks.get %s on %s = %v, want allowed %v", task.id, connection, err, want)
			}
			if err != nil && (!isInvalidRequest(err) || strings.Contains(err.Error(), outside) ||
				strings.Contains(result, outsideTag)) {
				t.Errorf("tasks.get %s on %s refused with %v, want a refusal that names no target", task.id,
					connection, err)
			}
		}
	}

	// Lists hold only rows of the scope, although the server answers with every task and project.
	for _, tt := range []struct {
		operation, connection, want, notWant string
	}{
		{"todoist.tasks.list", "tree", `"taskKid"`, "taskOutside"},
		{"todoist.tasks.list", "bare", `"taskRoot"`, "taskKid"},
		{"todoist.tasks.list", "section", `"taskBound"`, "taskOtherSect"},
		{"todoist.tasks.list", "union", `"taskRoot"`, "taskOutside"},
		{"todoist.tasks.filter", "tree", `"taskKid"`, "taskBound"},
		{"todoist.projects.list", "tree", `"projGrand"`, "projOutside"},
		{"todoist.projects.list", "bare", `"projTree"`, treeKid},
		{"todoist.sections.list", "tree", `"sectKid"`, "sectBound"},
	} {
		args := `{}`
		if tt.operation == "todoist.tasks.filter" {
			args = `{"query":"all"}`
		}
		result, err := env.run(t, tt.operation, tt.connection, args, false)
		if err != nil || !strings.Contains(result, tt.want) || strings.Contains(result, tt.notWant) {
			t.Errorf("%s on %s = %s, %v; want %s and not %s", tt.operation, tt.connection, result, err, tt.want,
				tt.notWant)
		}
	}
	if result, err := env.run(t, "todoist.tasks.list", "union", `{}`, false); err != nil ||
		!strings.Contains(result, "taskBound") || strings.Contains(result, "taskOtherSect") {
		t.Errorf("union list = %s, %v, want the tree and the bound section", result, err)
	}
}

// A project below a tree is resolved when it is used: one created during the connection's life joins at
// once, and nothing is remembered across calls.
func TestNewSubprojectsJoinTheTreeImmediately(t *testing.T) {
	f := newScopeTodoist()
	env := scopeEnvironment(t, f)
	if _, err := env.run(t, "todoist.tasks.create", "tree", `{"content":"x","project_id":"projNew"}`, true); err == nil {
		t.Fatal("a project that does not exist yet was accepted")
	}
	if result, err := env.run(t, "todoist.projects.create", "tree", `{"name":"New","parent_id":"`+treeGrand+`"}`, true); err != nil ||
		!strings.Contains(result, "projNew") {
		t.Fatalf("create below a descendant = %s, %v", result, err)
	}
	if result, err := env.run(t, "todoist.tasks.create", "tree", `{"content":"x","project_id":"projNew"}`, true); err != nil ||
		!strings.Contains(result, "taskNew") {
		t.Errorf("task in the new subproject = %s, %v", result, err)
	}
	if result, err := env.run(t, "todoist.projects.list", "tree", `{}`, false); err != nil ||
		!strings.Contains(result, "projNew") {
		t.Errorf("project list = %s, %v", result, err)
	}

	// Below the bare project, outside the tree, and at top level a project is not created.
	before := len(writes(f.recorded()))
	for _, tt := range []struct{ connection, arguments string }{
		{"tree", `{"name":"Top"}`},
		{"tree", `{"name":"Out","parent_id":"` + outside + `"}`},
		{"bare", `{"name":"Kid","parent_id":"` + treeRoot + `"}`},
		{"section", `{"name":"Kid","parent_id":"` + treeRoot + `"}`},
	} {
		if _, err := env.run(t, "todoist.projects.create", tt.connection, tt.arguments, true); err == nil {
			t.Errorf("projects.create on %s %s was accepted", tt.connection, tt.arguments)
		}
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused project creation reached Todoist")
	}
}

// A section binding reaches the tasks of that section and nothing of the project around it.
func TestSectionBindingReachesOnlyItsSection(t *testing.T) {
	f := newScopeTodoist()
	env := scopeEnvironment(t, f)
	for _, tt := range []struct{ operation, arguments string }{
		{"todoist.tasks.get", `{"task_id":"taskOtherSect"}`},
		{"todoist.tasks.get", `{"task_id":"taskOutside"}`},
		{"todoist.tasks.list", `{"section_id":"` + otherSect + `"}`},
		{"todoist.tasks.list", `{"project_id":"` + outside + `"}`},
		{"todoist.sections.get", `{"section_id":"` + otherSect + `"}`},
		{"todoist.projects.get", `{"project_id":"` + outside + `"}`},
		{"todoist.projects.list", `{}`},
		{"todoist.sections.list", `{}`},
		{"todoist.comments.list", `{"project_id":"` + outside + `"}`},
		{"todoist.comments.list", `{"task_id":"taskOutside"}`},
		{"todoist.reminders.list", `{"task_id":"taskOutside"}`},
	} {
		result, err := env.run(t, tt.operation, "section", tt.arguments, false)
		if err == nil || strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), otherSect) ||
			strings.Contains(result, outsideTag) {
			t.Errorf("%s %s = %q, %v, want a refusal that names no target", tt.operation, tt.arguments, result, err)
		}
	}
	for _, tt := range []struct{ operation, arguments string }{
		{"todoist.tasks.get", `{"task_id":"taskBound"}`},
		{"todoist.sections.get", `{"section_id":"` + boundSect + `"}`},
		{"todoist.tasks.list", `{"section_id":"` + boundSect + `"}`},
		{"todoist.comments.list", `{"task_id":"taskBound"}`},
	} {
		if _, err := env.run(t, tt.operation, "section", tt.arguments, false); err != nil {
			t.Errorf("%s %s = %v", tt.operation, tt.arguments, err)
		}
	}

	// Without arguments the list narrows to the one section at Todoist.
	before := len(f.recorded())
	if _, err := env.run(t, "todoist.tasks.list", "section", `{}`, false); err != nil {
		t.Fatal(err)
	}
	if sent := f.recorded()[before:]; len(sent) != 1 || sent[0].query.Get("section_id") != boundSect ||
		sent[0].query.Get("project_id") != "" {
		t.Errorf("list request = %+v, want the one bound section as filter", sent)
	}

	// A task is created in the bound section by default, and nowhere else.
	before = len(f.recorded())
	if _, err := env.run(t, "todoist.tasks.create", "section", `{"content":"x"}`, true); err != nil {
		t.Fatalf("create in the bound section = %v", err)
	}
	created := writes(f.recorded()[before:])
	if len(created) != 1 || bodyOf(t, created[0])["section_id"] != boundSect {
		t.Errorf("create requests = %+v, want one in the bound section", created)
	}
	before = len(writes(f.recorded()))
	for _, tt := range []struct{ operation, arguments string }{
		{"todoist.tasks.create", `{"content":"x","section_id":"` + otherSect + `"}`},
		{"todoist.tasks.create", `{"content":"x","project_id":"` + outside + `"}`},
		{"todoist.tasks.move", `{"task_id":"taskBound","section_id":"` + otherSect + `"}`},
		{"todoist.tasks.move", `{"task_id":"taskBound","project_id":"` + outside + `"}`},
		{"todoist.tasks.update", `{"task_id":"taskOtherSect","content":"y"}`},
		{"todoist.tasks.close", `{"task_id":"taskOutside"}`},
		{"todoist.comments.create", `{"project_id":"` + outside + `","content":"y"}`},
		{"todoist.comments.create", `{"task_id":"taskOutside","content":"y"}`},
		{"todoist.sections.create", `{"name":"S","project_id":"` + outside + `"}`},
		{"todoist.sections.reorder", `{"section_id":"` + boundSect + `","order":1}`},
		{"todoist.sections.delete", `{"section_id":"` + boundSect + `"}`},
		{"todoist.sections.move", `{"section_id":"` + boundSect + `","project_id":"` + outside + `"}`},
		{"todoist.projects.update", `{"project_id":"` + outside + `","name":"y"}`},
		{"todoist.projects.create", `{"name":"y","parent_id":"` + outside + `"}`},
		{"todoist.labels.create", `{"name":"y"}`},
	} {
		if _, err := env.run(t, tt.operation, "section", tt.arguments, true); err == nil {
			t.Errorf("%s %s was accepted", tt.operation, tt.arguments)
		}
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused change reached Todoist")
	}
}

// The tools a binding does not serve are not offered, and each says which forms would reach it.
func TestReachesNamesTheBindingsAToolNeeds(t *testing.T) {
	target := func() config.TargetMetadata {
		metadata, _ := registry(t).ProviderMetadata(Provider)
		return metadata.Target
	}()
	if err := target.ValidateKindParents(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		tool    string
		targets []string
		reached bool
		hint    string
	}{
		{"todoist.filters.list", []string{"*"}, true, ""},
		{"todoist.filters.list", []string{treePrefix + treeRoot}, false, "needs *"},
		{"todoist.labels.create", []string{treeRoot}, false, "needs *"},
		{"todoist.labels.delete", []string{sectionPrefix + boundSect}, false, "needs *"},
		{"todoist.labels.list", []string{treeRoot}, true, ""},
		{"todoist.projects.create", []string{treePrefix + treeRoot}, true, ""},
		{"todoist.projects.create", []string{treeRoot}, false, "needs project/PROJECT_ID or *"},
		{"todoist.projects.update", []string{treeRoot}, true, ""},
		{"todoist.projects.update", []string{sectionPrefix + boundSect}, false, "needs PROJECT_ID or project/PROJECT_ID or *"},
		{"todoist.sections.list", []string{sectionPrefix + boundSect}, false, "needs PROJECT_ID or project/PROJECT_ID or *"},
		{"todoist.sections.create", []string{sectionPrefix + boundSect, treeRoot}, true, ""},
		{"todoist.sections.delete", []string{sectionPrefix + boundSect}, false, "needs PROJECT_ID or project/PROJECT_ID or *"},
		{"todoist.sections.get", []string{sectionPrefix + boundSect}, true, ""},
		{"todoist.sections.update", []string{sectionPrefix + boundSect}, true, ""},
		{"todoist.tasks.create", []string{sectionPrefix + boundSect}, true, ""},
		{"todoist.comments.create", []string{sectionPrefix + boundSect}, true, ""},
		{"todoist.reminders.create", []string{sectionPrefix + boundSect}, true, ""},
	} {
		reached, hint := target.Reach(tt.tool, tt.targets)
		if reached != tt.reached || hint != tt.hint {
			t.Errorf("Reach(%s, %v) = %v, %q, want %v, %q", tt.tool, tt.targets, reached, hint, tt.reached, tt.hint)
		}
	}

	// The account-wide tools stay out of a project, tree, or section connection and reach a * connection.
	env := scopeEnvironment(t, newScopeTodoist())
	for connection, want := range map[string]bool{"tree": false, "bare": false, "section": false, "account": true} {
		_, err := env.run(t, "todoist.filters.list", connection, `{}`, false)
		if (err == nil) != want {
			t.Errorf("filters.list on %s = %v, want allowed %v", connection, err, want)
		}
	}
}

// Targets keep their old spellings and gain the tree and section forms; the wildcard stays alone.
func TestTargetSpellings(t *testing.T) {
	for _, values := range [][]string{{treePrefix + treeRoot}, {sectionPrefix + boundSect},
		{ownProject, treePrefix + otherProject, sectionPrefix + boundSect}, {treeRoot, treePrefix + treeRoot}} {
		if _, err := parseScope(values); err != nil {
			t.Errorf("parseScope(%v) = %v", values, err)
		}
	}
	for _, values := range [][]string{{treePrefix}, {sectionPrefix + "a/b"}, {"project/a/b"}, {"Section/a"},
		{"*", treePrefix + treeRoot}, {treePrefix + treeRoot, treePrefix + treeRoot}, {"project/*"}} {
		if _, err := parseScope(values); err == nil {
			t.Errorf("parseScope(%v) accepted", values)
		} else if strings.Contains(err.Error(), treeRoot) {
			t.Errorf("parseScope(%v) quotes a value: %v", values, err)
		}
	}
	bound, _ := parseScope([]string{ownProject, otherProject})
	if bound.String() != ownProject+","+otherProject || !bound.exact(ownProject) || bound.root(ownProject) ||
		bound.hasSection(ownProject) {
		t.Errorf("bare scope = %+v %q, want the unchanged exact meaning", bound, bound)
	}
	if one, ok := bound.single(); ok {
		t.Errorf("single() = %q for two projects", one)
	}
	for _, kind := range []string{"project", "section"} {
		metadata, _ := registry(t).ProviderMetadata(Provider)
		if !metadata.Target.ValidTarget(kind, map[string]string{"project": treePrefix + treeRoot,
			"section": sectionPrefix + boundSect}[kind]) {
			t.Errorf("kind %s does not accept its form", kind)
		}
	}
}
