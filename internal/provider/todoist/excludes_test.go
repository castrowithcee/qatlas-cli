package todoist

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Places of the exclusion fake. The tree is root > kid > grand, with an archived project below the root; solo
// has a child of its own, and out stands apart.
const (
	xRoot      = "xRoot"
	xKid       = "xKid"
	xGrand     = "xGrand"
	xArch      = "xArch"
	xSolo      = "xSolo"
	xSoloKid   = "xSoloKid"
	xOut       = "xOut"
	xKidSect   = "xKidSect"
	xOutSect   = "xOutSect"
	xRootSect  = "xRootSect"
	xCanary    = "excluded-canary-5d2e"
	xLabelName = "waiting"
)

type xTask struct {
	id, project, section string
	labels               []string
	completed            bool
}

type xFake struct {
	fakeTodoist
	projects  [][2]string // id, parent
	archived  [][2]string
	sections  map[string]string
	tasks     []xTask
	reminders [][2]string // id, task
	// foreverLabel makes the label scan announce a further page on every page, endless the project list.
	foreverLabel, endless bool
}

func newXFake() *xFake {
	return &xFake{
		projects: [][2]string{{xRoot, ""}, {xKid, xRoot}, {xGrand, xKid}, {xSolo, ""}, {xSoloKid, xSolo}, {xOut, ""}},
		archived: [][2]string{{xArch, xKid}},
		sections: map[string]string{xKidSect: xKid, xOutSect: xOut, xRootSect: xRoot},
		tasks: []xTask{{"tRoot", xRoot, "", nil, false}, {"tKid", xKid, "", nil, false},
			{"tKidSect", xKid, xKidSect, nil, false}, {"tGrand", xGrand, "", nil, false},
			{"tSolo", xSolo, "", nil, false}, {"tOut", xOut, "", []string{xLabelName}, false},
			{"tOutSect", xOut, xOutSect, nil, false}, {"tRootSect", xRoot, xRootSect, nil, false},
			{"tDone", xArch, "", nil, true}, {"tDoneKid", xKid, "", nil, true}},
		reminders: [][2]string{{"rRoot", "tRoot"}, {"rOut", "tOut"}, {"rKidSect", "tKidSect"}, {"rLost", "tGone"},
			{"rSolo", "tSolo"}},
	}
}

func xProject(id, parent string) string {
	return strings.Replace(scopeProjectJSON(id, parent), "Project "+id, "Name of "+id, 1)
}

func xTaskJSON(task xTask) string {
	section := "null"
	if task.section != "" {
		section = `"` + task.section + `"`
	}
	labels, _ := json.Marshal(append([]string{}, task.labels...))
	return `{"id":"` + task.id + `","project_id":"` + task.project + `","section_id":` + section + `,` +
		`"parent_id":null,"content":"Task ` + task.id + `","description":"","labels":` + string(labels) + `,` +
		`"priority":1,"due":null,"is_deleted":false}`
}

func (f *xFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	record := recorded{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
	var body map[string]any
	if r.Method != http.MethodGet {
		data, _ := io.ReadAll(r.Body)
		record.body = string(data)
		if r.URL.Path == "/api/v1/sync" {
			form, _ := url.ParseQuery(record.body)
			var commands []struct {
				UUID string `json:"uuid"`
			}
			_ = json.Unmarshal([]byte(form.Get("commands")), &commands)
			record.form = form
			w.Header().Set("Content-Type", "application/json")
			f.fakeTodoist.mu.Lock()
			f.requests = append(f.requests, record)
			f.fakeTodoist.mu.Unlock()
			if len(commands) == 0 {
				fmt.Fprint(w, `{"filters":[]}`)
				return
			}
			fmt.Fprintf(w, `{"sync_status":{%q:"ok"}}`, commands[0].UUID)
			return
		}
		_ = json.Unmarshal(data, &body)
	}
	f.fakeTodoist.mu.Lock()
	f.requests = append(f.requests, record)
	f.fakeTodoist.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	w.Header().Set("Content-Type", "application/json")
	list := func(entries []string, next string) {
		cursor := "null"
		if next != "" {
			cursor = `"` + next + `"`
		}
		fmt.Fprintf(w, `{"results":[%s],"items":[%s],"next_cursor":%s}`, strings.Join(entries, ","),
			strings.Join(entries, ","), cursor)
	}
	var entries []string
	switch {
	case r.Method != http.MethodGet:
		// A change is answered with the object it was sent for, placed where the body says.
		text := func(key, fallback string) string {
			if value, _ := body[key].(string); value != "" {
				return value
			}
			return fallback
		}
		id := path[strings.LastIndex(path, "/")+1:]
		if id == "tasks" || id == "projects" || id == "sections" || id == "comments" || id == "reminders" {
			id = "created"
		}
		parent := "null"
		if value := text("parent_id", ""); value != "" {
			parent = `"` + value + `"`
		}
		fmt.Fprintf(w, `{"id":%q,"project_id":%q,"section_id":null,"parent_id":%s,"name":"x","content":"x",`+
			`"task_id":"tRoot","labels":[]}`, id, text("project_id", xSolo), parent)
	case path == "/projects" || path == "/projects/archived":
		source := f.projects
		if path == "/projects/archived" {
			source = f.archived
		}
		for _, project := range source {
			entries = append(entries, xProject(project[0], project[1]))
		}
		next := ""
		if f.endless {
			next = "more"
		}
		list(entries, next)
	case strings.HasPrefix(path, "/projects/"):
		id := strings.TrimPrefix(path, "/projects/")
		for _, project := range append(slices.Clone(f.projects), f.archived...) {
			if project[0] == id {
				fmt.Fprint(w, xProject(id, project[1]))
				return
			}
		}
		notFound(w)
	case path == "/sections":
		for id, project := range f.sections {
			entries = append(entries, sectionJSON(id, project))
		}
		slices.Sort(entries)
		list(entries, "")
	case strings.HasPrefix(path, "/sections/"):
		id := strings.TrimPrefix(path, "/sections/")
		if project, ok := f.sections[id]; ok {
			fmt.Fprint(w, sectionJSON(id, project))
			return
		}
		notFound(w)
	case path == "/tasks" || path == "/tasks/filter" || path == "/tasks/completed/by_completion_date":
		ids := strings.Split(r.URL.Query().Get("ids"), ",")
		label := r.URL.Query().Get("label")
		for _, task := range f.tasks {
			switch {
			case task.completed != (path == "/tasks/completed/by_completion_date"):
			case r.URL.Query().Has("ids") && !slices.Contains(ids, task.id):
			case label != "" && !slices.Contains(task.labels, label):
			default:
				entries = append(entries, xTaskJSON(task))
			}
		}
		next := ""
		if label != "" && f.foreverLabel {
			next = "again"
		}
		list(entries, next)
	case strings.HasPrefix(path, "/tasks/"):
		id := strings.TrimPrefix(path, "/tasks/")
		for _, task := range f.tasks {
			if task.id == id {
				fmt.Fprint(w, xTaskJSON(task))
				return
			}
		}
		notFound(w)
	case path == "/labels":
		list([]string{`{"id":"lab1","name":"` + xLabelName + `"}`}, "")
	case path == "/comments":
		fmt.Fprint(w, `{"results":[{"id":"cmt","task_id":"`+r.URL.Query().Get("task_id")+`","content":"hi"}],"next_cursor":null}`)
	case strings.HasPrefix(path, "/comments/"):
		task := map[string]string{"cOut": "tOut", "cRoot": "tRoot"}[strings.TrimPrefix(path, "/comments/")]
		fmt.Fprint(w, `{"id":"c","task_id":"`+task+`","content":"hi"}`)
	case path == "/reminders":
		for _, reminder := range f.reminders {
			entries = append(entries, `{"id":"`+reminder[0]+`","task_id":"`+reminder[1]+`","type":"relative","minute_offset":5}`)
		}
		list(entries, "")
	case strings.HasPrefix(path, "/reminders/"):
		id := strings.TrimPrefix(path, "/reminders/")
		for _, reminder := range f.reminders {
			if reminder[0] == id {
				fmt.Fprint(w, `{"id":"`+id+`","task_id":"`+reminder[1]+`","type":"relative","minute_offset":5}`)
				return
			}
		}
		notFound(w)
	case strings.HasPrefix(path, "/labels/"):
		fmt.Fprint(w, `{"id":"`+strings.TrimPrefix(path, "/labels/")+`","name":"`+xLabelName+`"}`)
	default:
		notFound(w)
	}
}

func xEnvironment(t *testing.T, f *xFake, connections map[string]config.Connection) *environment {
	t.Helper()
	serve(t, f)
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	for name, connection := range connections {
		connection.Service, connection.Credential = "td", "td-reader"
		connection.Effects = changePermissions
		connection.Tools = allTools()
		connections[name] = connection
	}
	cfg := &config.Config{Version: 1,
		Services:    map[string]config.Service{"td": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"td-reader": credential}, Connections: connections}
	reads := 0
	red := &redact.Redactor{}
	return &environment{fake: &f.fakeTodoist, core: application.New(registry(t), cfg, resolver(red, &reads), red),
		red: red, reads: &reads}
}

func xConnections() map[string]config.Connection {
	return map[string]config.Connection{
		"account":  {Target: wildcard},
		"project":  {Target: wildcard, Exclude: []string{xOut}},
		"tree":     {Target: wildcard, Exclude: []string{treePrefix + xKid}},
		"sections": {Target: wildcard, Exclude: []string{sectionPrefix + xKidSect, sectionPrefix + xOutSect}},
		"bound":    {Target: treePrefix + xRoot, Exclude: []string{xKid}},
		"boundsub": {Target: treePrefix + xRoot, Exclude: []string{treePrefix + xKid}},
		"boundsec": {Targets: []string{treePrefix + xRoot}, Exclude: []string{sectionPrefix + xKidSect}},
	}
}

func refused(t *testing.T, label string, result string, err error) {
	t.Helper()
	if err == nil || !isInvalidRequest(err) {
		t.Errorf("%s = %q, %v, want an invalid request", label, result, err)
		return
	}
	for _, leak := range []string{xOut, xKid, xKidSect, xOutSect, xCanary, "exclu"} {
		if strings.Contains(err.Error(), leak) || strings.Contains(result, leak) {
			t.Errorf("%s refusal names %q: %v", label, leak, err)
		}
	}
}

func TestExcludeSpellingsAreParsedAndValidated(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	if !metadata.Target.Excludes() {
		t.Fatal("Todoist declares no exclusions")
	}
	for _, value := range []string{xOut, treePrefix + xKid, sectionPrefix + xKidSect} {
		if got := metadata.Target.CheckExcludes([]string{value}, []string{wildcard}); len(got) != 0 {
			t.Errorf("exclusion %q is invalid: %v", value, got)
		}
		if _, err := parseScopeExcluding([]string{wildcard}, []string{value}); err != nil {
			t.Errorf("parseScopeExcluding(%q) = %v", value, err)
		}
	}
	for _, bad := range [][]string{{wildcard}, {""}, {"a/b"}, {treePrefix}, {"project/*"}, {xOut, xOut},
		{xRoot}, {treePrefix + xRoot}, {sectionPrefix + xKidSect + "/x"}} {
		_, err := parseScopeExcluding([]string{xRoot, treePrefix + xRoot, sectionPrefix + xKidSect}[:1], bad)
		if bad[0] == treePrefix+xRoot {
			_, err = parseScopeExcluding([]string{treePrefix + xRoot}, bad)
		}
		if err == nil {
			t.Errorf("exclusion %v was accepted", bad)
		} else if strings.Contains(err.Error(), xKidSect) {
			t.Errorf("error quotes a value: %v", err)
		}
	}
	if _, err := parseScopeExcluding(nil, []string{xOut}); err == nil {
		t.Error("an exclusion without target was accepted")
	}
	bound, _ := parseScopeExcluding([]string{wildcard}, []string{xOut})
	if !bound.all || bound.unrestricted() || !bound.hasExcludes() || !bound.exact(xKid) {
		t.Errorf("wildcard with exclusion = %+v", bound)
	}
	plain, _ := parseScope([]string{wildcard})
	if !plain.unrestricted() || plain.String() != "*" || bound.String() == plain.String() {
		t.Errorf("unrestricted = %v, strings %q %q", plain.unrestricted(), plain.String(), bound.String())
	}
}

// The wildcard stays the gate of the account-wide tools, with or without exclusions.
func TestExclusionsLeaveTheToolGatesUnchanged(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	for _, tool := range []string{"todoist.filters.list", "todoist.labels.list", "todoist.projects.list"} {
		for _, connection := range []string{"account", "project", "tree", "sections"} {
			if _, err := env.run(t, tool, connection, `{}`, false); err != nil {
				t.Errorf("%s on %s = %v", tool, connection, err)
			}
		}
	}
	for _, connection := range []string{"project", "sections"} {
		if _, err := env.run(t, "todoist.projects.create", connection, `{"name":"Top"}`, true); err != nil {
			t.Errorf("top-level projects.create on %s = %v", connection, err)
		}
	}
	for _, connection := range []string{"bound", "boundsub", "boundsec"} {
		for _, tool := range []string{"todoist.filters.list", "todoist.labels.create"} {
			if _, err := env.run(t, tool, connection, `{"name":"x"}`, true); err == nil {
				t.Errorf("%s on %s was offered", tool, connection)
			}
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, tool := range []string{"todoist.filters.list", "todoist.projects.create", "todoist.labels.update"} {
		with, _ := metadata.Target.Reach(tool, []string{wildcard})
		if !with {
			t.Errorf("Reach(%s, *) is false", tool)
		}
	}
}

func TestExcludedProjectsAndTreesLeaveReadsAndRefuseDetails(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())

	// PROJECT_ID hides the project with its sections and tasks, not its subprojects.
	list, err := env.run(t, "todoist.projects.list", "project", `{}`, false)
	if err != nil || strings.Contains(list, `"id":"`+xOut+`"`) || !strings.Contains(list, xGrand) {
		t.Errorf("projects.list = %s, %v", list, err)
	}
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.projects.get", `{"project_id":"` + xOut + `"}`},
		{"todoist.sections.get", `{"section_id":"` + xOutSect + `"}`},
		{"todoist.tasks.get", `{"task_id":"tOut"}`},
		{"todoist.tasks.get", `{"task_id":"tOutSect"}`},
		{"todoist.tasks.list", `{"project_id":"` + xOut + `"}`},
		{"todoist.sections.list", `{"project_id":"` + xOut + `"}`},
		{"todoist.comments.list", `{"project_id":"` + xOut + `"}`},
		{"todoist.comments.list", `{"task_id":"tOut"}`},
		{"todoist.reminders.list", `{"task_id":"tOut"}`},
		{"todoist.reminders.get", `{"reminder_id":"rOut"}`},
		{"todoist.completed-tasks.list", `{"since":"2026-01-01T00:00:00Z","until":"2026-02-01T00:00:00Z","project_id":"` + xOut + `"}`},
	} {
		result, err := env.run(t, tt.tool, "project", tt.arguments, false)
		refused(t, tt.tool+" "+tt.arguments, result, err)
	}
	tasks, _ := env.run(t, "todoist.tasks.list", "project", `{}`, false)
	if strings.Contains(tasks, "tOut") || !strings.Contains(tasks, "tGrand") {
		t.Errorf("tasks.list = %s", tasks)
	}
	filtered, _ := env.run(t, "todoist.tasks.filter", "project", `{"query":"all"}`, false)
	if strings.Contains(filtered, "tOut") || !strings.Contains(filtered, "tSolo") {
		t.Errorf("tasks.filter = %s", filtered)
	}
	sections, _ := env.run(t, "todoist.sections.list", "project", `{}`, false)
	if strings.Contains(sections, xOutSect) || !strings.Contains(sections, xKidSect) {
		t.Errorf("sections.list = %s", sections)
	}

	// project/ID hides the whole tree, also tasks completed in an archived project below it.
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.projects.get", `{"project_id":"` + xKid + `"}`},
		{"todoist.projects.get", `{"project_id":"` + xGrand + `"}`},
		{"todoist.projects.get", `{"project_id":"` + xArch + `"}`},
		{"todoist.tasks.get", `{"task_id":"tGrand"}`},
		{"todoist.sections.get", `{"section_id":"` + xKidSect + `"}`},
	} {
		result, err := env.run(t, tt.tool, "boundsub", tt.arguments, false)
		refused(t, "boundsub "+tt.tool+" "+tt.arguments, result, err)
	}
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.projects.list", `{}`}, {"todoist.tasks.list", `{}`}, {"todoist.tasks.filter", `{"query":"all"}`},
		{"todoist.completed-tasks.list", `{"since":"2026-01-01T00:00:00Z","until":"2026-02-01T00:00:00Z"}`},
	} {
		result, err := env.run(t, tt.tool, "tree", tt.arguments, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.tool, err)
		}
		for _, hidden := range []string{xKid, xGrand, xArch, "tKid", "tGrand", "tDone"} {
			if strings.Contains(result, `"`+hidden+`"`) {
				t.Errorf("%s on tree returns %s: %s", tt.tool, hidden, result)
			}
		}
		if !strings.Contains(tt.tool, "completed") && !strings.Contains(result, xRoot) &&
			!strings.Contains(result, "tRoot") {
			t.Errorf("%s on tree returns nothing of the rest: %s", tt.tool, result)
		}
	}
	done, _ := env.run(t, "todoist.completed-tasks.list", "tree",
		`{"since":"2026-01-01T00:00:00Z","until":"2026-02-01T00:00:00Z"}`, false)
	if strings.Contains(done, "tDone") {
		t.Errorf("completed tasks of an archived project below the excluded tree: %s", done)
	}
	doneAll, _ := env.run(t, "todoist.completed-tasks.list", "account",
		`{"since":"2026-01-01T00:00:00Z","until":"2026-02-01T00:00:00Z"}`, false)
	if !strings.Contains(doneAll, "tDone") {
		t.Errorf("the unrestricted connection lost a completed task: %s", doneAll)
	}

	// A bare exclusion inside a project tree target hides that project only.
	bound, err := env.run(t, "todoist.projects.list", "bound", `{}`, false)
	if err != nil || strings.Contains(bound, `"id":"`+xKid+`"`) || !strings.Contains(bound, xGrand) ||
		!strings.Contains(bound, xRoot) {
		t.Errorf("bound projects.list = %s, %v", bound, err)
	}
	// parent_id of a reachable project below an excluded parent stays a plain ID.
	if !strings.Contains(bound, `"parent_id":"`+xKid+`"`) {
		t.Errorf("parent_id of the grandchild was dropped: %s", bound)
	}
	got, err := env.run(t, "todoist.projects.get", "bound", `{"project_id":"`+xGrand+`"}`, false)
	if err != nil || !strings.Contains(got, `"parent_id":"`+xKid+`"`) {
		t.Errorf("projects.get = %s, %v", got, err)
	}
}

func TestExcludedSectionsLeaveReadsAndWinOverTargets(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	for _, connection := range []string{"sections", "boundsec"} {
		for _, tt := range []struct{ tool, arguments string }{
			{"todoist.tasks.get", `{"task_id":"tKidSect"}`},
			{"todoist.sections.get", `{"section_id":"` + xKidSect + `"}`},
			{"todoist.tasks.list", `{"section_id":"` + xKidSect + `"}`},
			{"todoist.comments.list", `{"task_id":"tKidSect"}`},
			{"todoist.reminders.list", `{"task_id":"tKidSect"}`},
			{"todoist.reminders.get", `{"reminder_id":"rKidSect"}`},
		} {
			result, err := env.run(t, tt.tool, connection, tt.arguments, false)
			refused(t, connection+" "+tt.tool, result, err)
		}
		tasks, err := env.run(t, "todoist.tasks.list", connection, `{}`, false)
		if err != nil || strings.Contains(tasks, "tKidSect") || !strings.Contains(tasks, "tKid") {
			t.Errorf("%s tasks.list = %s, %v", connection, tasks, err)
		}
		if result, err := env.run(t, "todoist.tasks.get", connection, `{"task_id":"tKid"}`, false); err != nil {
			t.Errorf("%s: a task outside the excluded section was refused: %s %v", connection, result, err)
		}
		sections, _ := env.run(t, "todoist.sections.list", connection, `{}`, false)
		if strings.Contains(sections, `"`+xKidSect+`"`) {
			t.Errorf("%s sections.list = %s", connection, sections)
		}
	}
	// The second excluded section hides the tasks of the outside project's section only.
	tasks, _ := env.run(t, "todoist.tasks.list", "sections", `{}`, false)
	if strings.Contains(tasks, "tOutSect") || !strings.Contains(tasks, `"tOut"`) {
		t.Errorf("tasks.list = %s", tasks)
	}
}

// Without exclusions an account connection checks nothing; with them every path checks.
func TestOnlyTheUnrestrictedAccountSkipsTheMembershipCheck(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	before := len(f.recorded())
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.tasks.update", `{"task_id":"tOut","content":"y"}`},
		{"todoist.comments.update", `{"comment_id":"cOut","content":"y"}`},
		{"todoist.reminders.update", `{"reminder_id":"rOut","minute_offset":5}`},
		{"todoist.reminders.delete", `{"reminder_id":"rOut"}`},
		{"todoist.sections.update", `{"section_id":"` + xOutSect + `","name":"y"}`},
		{"todoist.comments.list", `{"task_id":"tOut"}`},
	} {
		if _, err := env.run(t, tt.tool, "account", tt.arguments, true); err != nil {
			t.Errorf("%s on account = %v", tt.tool, err)
		}
	}
	for _, request := range f.recorded()[before:] {
		if request.method == http.MethodGet && (strings.HasPrefix(request.path, "/api/v1/tasks/") ||
			strings.HasPrefix(request.path, "/api/v1/comments/") || strings.HasPrefix(request.path, "/api/v1/reminders/") ||
			strings.HasPrefix(request.path, "/api/v1/sections/")) {
			t.Errorf("the unrestricted account read first: %s", request.path)
		}
	}

	before = len(writes(f.recorded()))
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.tasks.create", `{"content":"x","project_id":"` + xOut + `"}`},
		{"todoist.tasks.create", `{"content":"x"}`},
		{"todoist.tasks.create", `{"content":"x","section_id":"` + xOutSect + `"}`},
		{"todoist.tasks.create", `{"content":"x","parent_id":"tOut"}`},
		{"todoist.tasks.update", `{"task_id":"tOut","content":"y"}`},
		{"todoist.tasks.move", `{"task_id":"tSolo","project_id":"` + xOut + `"}`},
		{"todoist.tasks.move", `{"task_id":"tSolo","section_id":"` + xOutSect + `"}`},
		{"todoist.tasks.move", `{"task_id":"tSolo","parent_id":"tOut"}`},
		{"todoist.tasks.move", `{"task_id":"tOut","project_id":"` + xSolo + `"}`},
		{"todoist.tasks.close", `{"task_id":"tOutSect"}`},
		{"todoist.tasks.reopen", `{"task_id":"tOut"}`},
		{"todoist.tasks.delete", `{"task_id":"tOut"}`},
		{"todoist.comments.create", `{"task_id":"tOut","content":"y"}`},
		{"todoist.comments.create", `{"project_id":"` + xOut + `","content":"y"}`},
		{"todoist.comments.update", `{"comment_id":"cOut","content":"y"}`},
		{"todoist.comments.delete", `{"comment_id":"cOut"}`},
		{"todoist.reminders.create", `{"task_id":"tOut","minute_offset":5}`},
		{"todoist.reminders.update", `{"reminder_id":"rOut","minute_offset":5}`},
		{"todoist.reminders.delete", `{"reminder_id":"rOut"}`},
		{"todoist.projects.create", `{"name":"x","parent_id":"` + xOut + `"}`},
		{"todoist.projects.update", `{"project_id":"` + xOut + `","name":"y"}`},
		{"todoist.projects.unarchive", `{"project_id":"` + xOut + `"}`},
		{"todoist.sections.create", `{"name":"x","project_id":"` + xOut + `"}`},
		{"todoist.sections.update", `{"section_id":"` + xOutSect + `","name":"y"}`},
		{"todoist.sections.delete", `{"section_id":"` + xOutSect + `"}`},
		{"todoist.sections.reorder", `{"section_id":"` + xOutSect + `","order":1}`},
		{"todoist.sections.move", `{"section_id":"` + xOutSect + `","project_id":"` + xSolo + `"}`},
		{"todoist.sections.move", `{"section_id":"` + xRootSect + `","project_id":"` + xOut + `"}`},
	} {
		result, err := env.run(t, tt.tool, "project", tt.arguments, true)
		refused(t, "project "+tt.tool+" "+tt.arguments, result, err)
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused change reached Todoist")
	}

	// Excluded trees and sections refuse the same paths on the other bindings.
	for _, tt := range []struct{ connection, tool, arguments string }{
		{"tree", "todoist.tasks.create", `{"content":"x","project_id":"` + xGrand + `"}`},
		{"tree", "todoist.projects.create", `{"name":"x","parent_id":"` + xGrand + `"}`},
		{"tree", "todoist.projects.update", `{"project_id":"` + xKid + `","name":"y"}`},
		{"tree", "todoist.projects.unarchive", `{"project_id":"` + xArch + `"}`},
		{"tree", "todoist.tasks.close", `{"task_id":"tGrand"}`},
		{"boundsub", "todoist.sections.create", `{"name":"x","project_id":"` + xGrand + `"}`},
		{"sections", "todoist.tasks.create", `{"content":"x","section_id":"` + xKidSect + `"}`},
		{"sections", "todoist.tasks.move", `{"task_id":"tRoot","section_id":"` + xKidSect + `"}`},
		{"sections", "todoist.sections.update", `{"section_id":"` + xKidSect + `","name":"y"}`},
		{"sections", "todoist.sections.delete", `{"section_id":"` + xKidSect + `"}`},
		{"sections", "todoist.sections.reorder", `{"section_id":"` + xKidSect + `","order":1}`},
		{"sections", "todoist.sections.move", `{"section_id":"` + xKidSect + `","project_id":"` + xSolo + `"}`},
		{"sections", "todoist.comments.create", `{"task_id":"tKidSect","content":"y"}`},
		{"sections", "todoist.reminders.create", `{"task_id":"tKidSect","minute_offset":5}`},
	} {
		result, err := env.run(t, tt.tool, tt.connection, tt.arguments, true)
		refused(t, tt.connection+" "+tt.tool+" "+tt.arguments, result, err)
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused change reached Todoist")
	}

	// A reachable place still works, and a section moves to a reachable project.
	for _, tt := range []struct{ connection, tool, arguments string }{
		{"project", "todoist.tasks.create", `{"content":"x","project_id":"` + xSolo + `"}`},
		{"project", "todoist.sections.move", `{"section_id":"` + xRootSect + `","project_id":"` + xSolo + `"}`},
		{"tree", "todoist.projects.create", `{"name":"x","parent_id":"` + xRoot + `"}`},
	} {
		if result, err := env.run(t, tt.tool, tt.connection, tt.arguments, true); err != nil {
			t.Errorf("%s %s = %s, %v", tt.connection, tt.tool, result, err)
		}
	}
}

func TestExcludedParentOrSectionOfAProjectRefusesArchiveAndDelete(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	before := len(writes(f.recorded()))
	for _, tool := range []string{"todoist.projects.archive", "todoist.projects.delete"} {
		for _, tt := range []struct{ connection, project string }{
			{"tree", xRoot},     // excluded subproject in the tree
			{"project", xOut},   // the project itself is excluded
			{"sections", xKid},  // excluded section in the project itself
			{"sections", xRoot}, // excluded section in a descendant
			{"boundsub", xRoot}, // excluded tree below a reachable project
			{"bound", xRoot},    // excluded bare project below
			{"boundsec", xRoot}, // excluded section below
		} {
			result, err := env.run(t, tool, tt.connection, `{"project_id":"`+tt.project+`"}`, true)
			refused(t, tt.connection+" "+tool+" "+tt.project, result, err)
		}
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused archive or delete reached Todoist")
	}
	// A project without anything excluded below it is archived and deleted.
	for _, tool := range []string{"todoist.projects.archive", "todoist.projects.delete"} {
		for _, connection := range []string{"project", "tree", "sections"} {
			if result, err := env.run(t, tool, connection, `{"project_id":"`+xSolo+`"}`, true); err != nil {
				t.Errorf("%s on %s = %s, %v", tool, connection, result, err)
			}
		}
	}
}

func TestTreeLargerThanTheReadBoundIsRefusedUnchecked(t *testing.T) {
	f := newXFake()
	f.endless = true
	env := xEnvironment(t, f, xConnections())
	for _, tool := range []string{"todoist.projects.delete", "todoist.projects.archive"} {
		for _, connection := range []string{"tree", "project"} {
			if result, err := env.run(t, tool, connection, `{"project_id":"`+xSolo+`"}`, true); err == nil {
				t.Errorf("%s on %s accepted an unchecked tree: %s", tool, connection, result)
			}
		}
	}
	if len(writes(f.recorded())) != 0 {
		t.Error("an unchecked tree reached Todoist")
	}
}

func TestRemindersWithoutATaskAreHeldToTheirTasks(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	before := len(f.recorded())
	result, err := env.run(t, "todoist.reminders.list", "project", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"rOut", "rLost"} {
		if strings.Contains(result, hidden) {
			t.Errorf("reminders.list returns %s: %s", hidden, result)
		}
	}
	if !strings.Contains(result, "rRoot") || !strings.Contains(result, "rSolo") {
		t.Errorf("reminders.list = %s", result)
	}
	var batches int
	for _, request := range f.recorded()[before:] {
		if request.path == "/api/v1/tasks" && request.query.Has("ids") {
			batches++
			if got := len(strings.Split(request.query.Get("ids"), ",")); got > 100 {
				t.Errorf("a batch holds %d task IDs", got)
			}
		}
	}
	if batches != 1 {
		t.Errorf("batches = %d, want 1", batches)
	}
	sectioned, _ := env.run(t, "todoist.reminders.list", "sections", `{}`, false)
	if strings.Contains(sectioned, "rKidSect") || !strings.Contains(sectioned, "rOut") {
		t.Errorf("reminders.list = %s", sectioned)
	}
	// Only an account connection lists without a task.
	if result, err := env.run(t, "todoist.reminders.list", "bound", `{}`, false); err == nil {
		t.Errorf("reminders.list without task on a bound connection = %s", result)
	}
	// The unrestricted account reads no tasks.
	before = len(f.recorded())
	if _, err := env.run(t, "todoist.reminders.list", "account", `{}`, false); err != nil {
		t.Fatal(err)
	}
	for _, request := range f.recorded()[before:] {
		if strings.HasPrefix(request.path, "/api/v1/tasks") {
			t.Errorf("the unrestricted account resolved tasks: %s", request.path)
		}
	}
}

func TestRemindersResolveManyTasksInBatchesOfAHundred(t *testing.T) {
	f := newXFake()
	f.reminders, f.tasks = nil, nil
	for i := range 150 {
		task := fmt.Sprintf("t%d", i)
		f.tasks = append(f.tasks, xTask{task, xSolo, "", nil, false})
		f.reminders = append(f.reminders, [2]string{fmt.Sprintf("r%d", i), task})
	}
	env := xEnvironment(t, f, xConnections())
	result, err := env.run(t, "todoist.reminders.list", "project", `{"limit":200}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Reminders []json.RawMessage `json:"reminders"`
	}
	if err := json.Unmarshal([]byte(result), &list); err != nil || len(list.Reminders) != 150 {
		t.Errorf("reminders = %d, %v", len(list.Reminders), err)
	}
	var sizes []int
	for _, request := range f.recorded() {
		if request.path == "/api/v1/tasks" && request.query.Has("ids") {
			sizes = append(sizes, len(strings.Split(request.query.Get("ids"), ",")))
		}
	}
	if !slices.Equal(sizes, []int{100, 50}) {
		t.Errorf("batch sizes = %v, want [100 50]", sizes)
	}
}

func TestLabelChangesCheckTheActiveTasksWithTheLabel(t *testing.T) {
	f := newXFake()
	env := xEnvironment(t, f, xConnections())
	before := len(writes(f.recorded()))
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.labels.update", `{"label_id":"lab1","name":"blocked"}`},
		{"todoist.labels.delete", `{"label_id":"lab1"}`},
	} {
		result, err := env.run(t, tt.tool, "project", tt.arguments, true)
		refused(t, tt.tool, result, err)
	}
	if len(writes(f.recorded())) != before {
		t.Error("a refused label change reached Todoist")
	}
	// No excluded task carries the label: the change goes through; a color change reads nothing first.
	for _, tt := range []struct{ tool, arguments string }{
		{"todoist.labels.update", `{"label_id":"lab1","name":"blocked"}`},
		{"todoist.labels.delete", `{"label_id":"lab1"}`},
	} {
		if result, err := env.run(t, tt.tool, "tree", tt.arguments, true); err != nil {
			t.Errorf("%s without a hit = %s, %v", tt.tool, result, err)
		}
	}
	before = len(f.recorded())
	if _, err := env.run(t, "todoist.labels.update", "project", `{"label_id":"lab1","color":"blue"}`, true); err != nil {
		t.Errorf("a color change was refused: %v", err)
	}
	for _, request := range f.recorded()[before:] {
		if request.method == http.MethodGet {
			t.Errorf("a color change read %s first", request.path)
		}
	}
	// The unrestricted account checks nothing; an incomplete check refuses.
	before = len(f.recorded())
	if _, err := env.run(t, "todoist.labels.delete", "account", `{"label_id":"lab1"}`, true); err != nil {
		t.Errorf("delete on the unrestricted account = %v", err)
	}
	for _, request := range f.recorded()[before:] {
		if request.method == http.MethodGet {
			t.Errorf("the unrestricted account read %s first", request.path)
		}
	}
	f.foreverLabel = true
	before = len(writes(f.recorded()))
	result, err := env.run(t, "todoist.labels.delete", "tree", `{"label_id":"lab1"}`, true)
	if err == nil {
		t.Errorf("an incomplete label check was accepted: %s", result)
	}
	if len(writes(f.recorded())) != before {
		t.Error("an unchecked label change reached Todoist")
	}
}

// Warnings sit in the descriptions of the tools whose result an exclusion shortens or leaves unfiltered.
func TestDescriptionsWarnAboutExclusions(t *testing.T) {
	reg := registry(t)
	for _, tool := range []string{projectsList.ID, sectionsList.ID, tasksList.ID, tasksFilter.ID, completedList.ID,
		remindersList.ID, labelsList.ID, filtersList.ID, projectsArchive.ID, projectsDelete.ID, labelsUpdate.ID,
		labelsDelete.ID, commentsList.ID} {
		descriptor, _, ok := reg.Lookup(tool)
		if !ok {
			t.Fatalf("tool %s is not registered", tool)
		}
		if !strings.Contains(descriptor.Description, "xclu") {
			t.Errorf("%s does not mention exclusions: %s", tool, descriptor.Description)
		}
	}
}
