package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeViewFields names the fields of project 7 a view may show.
var fakeViewFields = map[string]string{"F_title": "Title", "F_status": "Status", "F_prio": "Priority",
	"F_tags": "Tags", "F_note": "Note", "F_assignees": "Assignees"}

// fakeViews are the views of project 7: a board with a filter, columns, and a sort, a table grouped by
// priority, and a roadmap.
var fakeViews = []string{
	`{"id":"V_1","number":1,"name":"Board","layout":"BOARD_LAYOUT","filter":"status:Todo",` +
		`"configuration":{"visibleFields":{"nodes":[{"id":"F_title","name":"Title"},{"id":"F_status","name":"Status"}]}},` +
		`"groupByFields":{"nodes":[]},"verticalGroupByFields":{"nodes":[{"id":"F_status","name":"Status"}]},` +
		`"sortByFields":{"nodes":[{"direction":"DESC","field":{"id":"F_prio","name":"Priority"}}]}}`,
	`{"id":"V_2","number":2,"name":"Table","layout":"TABLE_LAYOUT","filter":null,` +
		`"configuration":{"visibleFields":{"nodes":[{"id":"F_title","name":"Title"},` +
		`{"id":"F_prio","name":"Priority"},{}]}},` +
		`"groupByFields":{"nodes":[{"id":"F_prio","name":"Priority"}]},"verticalGroupByFields":{"nodes":[]},` +
		`"sortByFields":{"nodes":[]}}`,
	`{"id":"V_3","number":3,"name":"Plan","layout":"ROADMAP_LAYOUT","filter":"",` +
		`"configuration":{"visibleFields":{"nodes":[{"id":"F_title","name":"Title"}]}},"groupByFields":{"nodes":[]},` +
		`"verticalGroupByFields":{"nodes":[]},"sortByFields":{"nodes":[]}}`,
}

func (f *fakeGitHub) viewsJSON() string {
	if f.views != "" {
		return f.views
	}
	return strings.Join(fakeViews, ",")
}

// viewChange answers the view and template mutations and reports whether it did. A created or changed view
// is answered as GitHub does: with the settings the request sent and the visible fields it named.
func (f *fakeGitHub) viewChange(w http.ResponseWriter, document string, variables map[string]any) bool {
	var id, name, layout string
	number := 0
	filter, fields := any(nil), []any{map[string]any{"id": "F_title", "name": "Title"}}
	switch {
	case strings.Contains(document, "createProjectV2View("):
		if variables["project"] != projectID {
			fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","path":["view"],"message":"no project"}]}`)
			return true
		}
		id, number, name, layout = "V_new", 4, fmt.Sprint(variables["name"]), fmt.Sprint(variables["layout"])
	case strings.Contains(document, "updateProjectV2View("):
		id = fmt.Sprint(variables["view"])
		existing := map[string][2]string{"V_1": {"Board", "BOARD_LAYOUT"}, "V_2": {"Table", "TABLE_LAYOUT"},
			"V_3": {"Plan", "ROADMAP_LAYOUT"}, "V_new": {"New", "TABLE_LAYOUT"}}[id]
		number, name, layout = map[string]int{"V_1": 1, "V_2": 2, "V_3": 3, "V_new": 4}[id], existing[0], existing[1]
		if value, ok := variables["name"].(string); ok {
			name = value
		}
		if value, ok := variables["layout"].(string); ok {
			layout = value
		}
		filter = variables["filter"]
	case strings.Contains(document, "deleteProjectV2View("):
		fmt.Fprint(w, `{"data":{"view":{"clientMutationId":null}}}`)
		return true
	case strings.Contains(document, "markProjectV2AsTemplate("):
		fmt.Fprintf(w, `{"data":{"template":{"projectV2":{"number":7,"title":"Plan 7",`+
			`"url":"https://github.com/orgs/octo-org/projects/7","closed":false,"public":false,"template":%t}}}}`,
			!strings.Contains(document, "unmarkProjectV2AsTemplate("))
		return true
	default:
		return false
	}
	if configuration, ok := variables["configuration"].(map[string]any); ok {
		for _, field := range configuration["visibleFieldIds"].([]any) {
			fields = append(fields, map[string]any{"id": field, "name": fakeViewFields[fmt.Sprint(field)]})
		}
	}
	node := map[string]any{"id": id, "number": number, "name": name, "layout": layout, "filter": filter,
		"configuration":         map[string]any{"visibleFields": map[string]any{"nodes": fields}},
		"groupByFields":         map[string]any{"nodes": []any{}},
		"verticalGroupByFields": map[string]any{"nodes": []any{}}, "sortByFields": map[string]any{"nodes": []any{}}}
	answer, _ := json.Marshal(map[string]any{"data": map[string]any{"view": map[string]any{"projectV2View": node}}})
	_, _ = w.Write(answer)
	return true
}

var viewTools = []string{viewsList.ID, viewsCreate.ID, viewsUpdate.ID, viewsDelete.ID, templatesMark.ID,
	templatesUnmark.ID}

// viewsConfig holds the connections the view tools meet: one that may create and update, an administrator
// that lists every view tool and may delete, the same without the delete permission, one with every
// permission but without a tools list, and one bound to a project of a user.
func viewsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	changes := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	add := func(name string, targets []string, permissions []config.Permission, tools []string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: permissions, Tools: tools}
	}
	add("views", planningTargets, changes, nil)
	add("admin", planningTargets, config.Permissions(), viewTools)
	add("nodelete", planningTargets, changes, viewTools)
	add("unlisted", planningTargets, config.Permissions(), nil)
	add("user", []string{userTarget}, changes, nil)
	return cfg
}

func viewsCore(t *testing.T, f *fakeGitHub, reads *int) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), viewsConfig(serve(t, f)), resolver(red, reads), red)
}

// Every refusal the target, the permissions, the tools list, the confirmation, or the arguments settle ends
// before a secret is read and before GitHub is contacted; a delete needs the effect delete and the tool's
// name in the tools list, and GitHub marks no project of a user as a template.
func TestProjectViewRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	core := viewsCore(t, f, &reads)

	invalid := &application.InvalidRequestError{}
	unsupported := &capability.UnsupportedError{}
	unconfirmed := &application.ConfirmationRequiredError{}
	long := strings.Repeat("a", 506)
	for _, tt := range []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
		message                                string
	}{
		{"a list outside the targets", viewsList.ID, "views", `{"project":"orgs/octo-org/projects/8"}`, false,
			invalid, "outside the targets"},
		{"an unconfirmed create", viewsCreate.ID, "views", `{"name":"Bugs","layout":"board"}`, false,
			unconfirmed, ""},
		{"a create outside the targets", viewsCreate.ID, "views",
			`{"project":"users/octocat/projects/3","name":"Bugs","layout":"board"}`, true, invalid, "outside the targets"},
		{"an unknown layout", viewsCreate.ID, "views", `{"name":"Bugs","layout":"gallery"}`, true, invalid, ""},
		{"fields of a new roadmap", viewsCreate.ID, "views", `{"name":"Plan","layout":"roadmap","fields":["Status"]}`,
			true, invalid, "roadmap view takes no fields"},
		{"a field named twice", viewsCreate.ID, "views",
			`{"name":"Bugs","layout":"table","fields":["Status","status"]}`, true, invalid, "more than once"},
		{"a filter over two lines", viewsCreate.ID, "views",
			`{"name":"Bugs","layout":"table","filter":"label:bug\nstatus:Todo"}`, true, invalid, "control characters"},
		{"a filter GitHub would refuse as too long", viewsCreate.ID, "views",
			`{"name":"Bugs","layout":"table","filter":"label:` + long + `x"}`, true, invalid, "512"},
		{"an update without a change", viewsUpdate.ID, "views", `{"view":1}`, true, invalid, "name at least one"},
		{"fields of a view turned into a roadmap", viewsUpdate.ID, "views",
			`{"view":1,"layout":"roadmap","fields":["Status"]}`, true, invalid, "roadmap"},
		{"view zero", viewsUpdate.ID, "views", `{"view":0,"name":"x"}`, true, invalid, ""},
		{"a delete without the delete permission", viewsDelete.ID, "views", `{"view":2}`, true, unsupported, ""},
		{"a delete with the tools list but without the permission", viewsDelete.ID, "nodelete", `{"view":2}`, true,
			unsupported, ""},
		{"a delete without a tools list", viewsDelete.ID, "unlisted", `{"view":2}`, true, unsupported, ""},
		{"an unconfirmed delete", viewsDelete.ID, "admin", `{"view":2}`, false, unconfirmed, ""},
		{"a template of a user", templatesMark.ID, "user", `{}`, true, invalid,
			"only a project of an organization"},
		{"a template outside the targets", templatesMark.ID, "views", `{"project":"orgs/octo-org/projects/8"}`, true,
			invalid, "outside the targets"},
		{"an unconfirmed unmark", templatesUnmark.ID, "views", `{}`, false, unconfirmed, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, tt.confirmed)
			if want := fmt.Sprintf("%T", tt.want); fmt.Sprintf("%T", err) != want ||
				!strings.Contains(fmt.Sprint(err), tt.message) {
				t.Errorf("err = %T %v, want %s with %q", err, err, want, tt.message)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

// The view list reads every view in one query: its layout, filter, visible fields, and the grouping, board
// columns, and sorting GitHub alone sets.
func TestProjectViewsListEveryView(t *testing.T) {
	f := &fakeGitHub{}
	core := viewsCore(t, f, nil)

	result, err := invoke(t, core, viewsList.ID, "views", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Views   []ProjectView `json:"views"`
		Project string        `json:"project"`
	}
	if err := json.Unmarshal(result, &list); err != nil || list.Project != projectTarget || len(list.Views) != 3 {
		t.Fatalf("result = %s, %v", result, err)
	}
	for i, want := range []string{
		"{1 Board board status:Todo [Title Status] [] [Status] [{Priority desc}]}",
		"{2 Table table  [Title Priority] [Priority] [] []}",
		"{3 Plan roadmap  [Title] [] [] []}",
	} {
		if got := fmt.Sprint(list.Views[i]); got != want {
			t.Errorf("view %d = %s, want %s", i+1, got, want)
		}
	}
	queries, mutations, _ := split(f.recorded())
	if len(queries) != 1 || len(mutations) != 0 || !strings.Contains(queries[0].document, "views(first:100)") {
		t.Errorf("requests = %d queries, %d mutations; want one query of the views", len(queries), len(mutations))
	}
}

// Every view change resolves the project, its views, and the named fields in one query and sends exactly one
// mutation, with every value as a variable. Visible fields are named and sent in their given order.
func TestProjectViewChangesSendOneMutationEach(t *testing.T) {
	f := &fakeGitHub{}
	core := viewsCore(t, f, nil)

	for _, tt := range []struct {
		name, operation, arguments string
		document                   []string
		absent                     []string
		variables                  map[string]string
		result                     string
	}{
		{"create a table with its fields", viewsCreate.ID,
			`{"name":"Triage","layout":"table","fields":["priority","Status"]}`,
			[]string{"createProjectV2View(input:{projectId:$project,name:$name,layout:$layout,configuration:$configuration})"},
			[]string{"filter:"},
			map[string]string{"project": projectID, "name": "Triage", "layout": "TABLE_LAYOUT",
				"configuration": "map[visibleFieldIds:[F_prio F_status]]"},
			`{"complete":true,"project":"orgs/octo-org/projects/7","view":{"column_by":[],"fields":["Title",` +
				`"Priority","Status"],"filter":"","group_by":[],"layout":"table","name":"Triage","number":4,"sort_by":[]}}`},
		{"create a roadmap with GitHub's fields", viewsCreate.ID, `{"name":"Plan","layout":"roadmap"}`, nil, nil,
			map[string]string{"layout": "ROADMAP_LAYOUT", "configuration": "<nil>"}, `"complete":true`},
		{"create a board with an empty filter", viewsCreate.ID, `{"name":"Bugs","layout":"board","filter":""}`, nil,
			nil, map[string]string{"layout": "BOARD_LAYOUT"}, `"layout":"board"`},
		{"rename a view, change its layout, and filter it", viewsUpdate.ID,
			`{"view":2,"name":"Open","layout":"board","filter":"-status:Done"}`,
			[]string{"updateProjectV2View(input:{viewId:$view,name:$name,layout:$layout,filter:$filter})",
				"$layout:ProjectV2ViewLayout!"},
			[]string{"configuration:"},
			map[string]string{"view": "V_2", "name": "Open", "layout": "BOARD_LAYOUT", "filter": "-status:Done"},
			`"view":{"column_by":[],"fields":["Title"],"filter":"-status:Done","group_by":[],"layout":"board",` +
				`"name":"Open","number":2,"sort_by":[]}`},
		{"remove a filter", viewsUpdate.ID, `{"view":1,"filter":""}`,
			[]string{"updateProjectV2View(input:{viewId:$view,filter:$filter})"}, nil,
			map[string]string{"view": "V_1", "filter": ""}, `"filter":""`},
		{"show only the title", viewsUpdate.ID, `{"view":1,"fields":[]}`,
			[]string{"updateProjectV2View(input:{viewId:$view,configuration:$configuration})"}, nil,
			map[string]string{"configuration": "map[visibleFieldIds:[]]"}, `"fields":["Title"]`},
		{"turn a roadmap into a table with fields", viewsUpdate.ID,
			`{"view":3,"layout":"table","fields":["Assignees","Tags"]}`, nil, nil,
			map[string]string{"view": "V_3", "layout": "TABLE_LAYOUT",
				"configuration": "map[visibleFieldIds:[F_assignees F_tags]]"}, `"fields":["Title","Assignees","Tags"]`},
		{"delete a view", viewsDelete.ID, `{"view":2}`,
			[]string{"deleteProjectV2View(input:{viewId:$view})"}, nil, map[string]string{"view": "V_2"},
			`{"deleted":true,"name":"Table","number":2,"project":"orgs/octo-org/projects/7"}`},
		{"mark a template", templatesMark.ID, `{}`, []string{"markProjectV2AsTemplate(input:{projectId:$project})"},
			nil, map[string]string{"project": projectID}, `"template":true`},
		{"unmark a template", templatesUnmark.ID, `{}`,
			[]string{"unmarkProjectV2AsTemplate(input:{projectId:$project})"}, nil,
			map[string]string{"project": projectID}, `"template":false`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			result, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if err != nil || !strings.Contains(string(result), tt.result) {
				t.Fatalf("result = %s, %v; want %s", result, err, tt.result)
			}
			queries, mutations, rest := split(f.recorded()[before:])
			if len(queries) != 1 || len(mutations) != 1 || len(rest) != 0 {
				t.Fatalf("requests = %d queries, %d mutations, %v REST; want 1, 1, none", len(queries),
					len(mutations), rest)
			}
			for _, want := range tt.document {
				if !strings.Contains(mutations[0].document, want) {
					t.Errorf("mutation %s lacks %s", mutations[0].document, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(mutations[0].document, absent) {
					t.Errorf("mutation %s names %s", mutations[0].document, absent)
				}
			}
			for name, want := range tt.variables {
				if got := fmt.Sprint(mutations[0].variables[name]); got != want {
					t.Errorf("variable %s = %s, want %s", name, got, want)
				}
			}
			if strings.ContainsAny(strings.TrimPrefix(mutations[0].document, "mutation"), `"`) {
				t.Errorf("a value reached the document: %s", mutations[0].document)
			}
		})
	}
}

// A new view takes no filter, so a create with one sets it by a second change. Once the view exists the answer
// reports it: a filter GitHub refused or answered unclearly leaves the answer incomplete, never failed, and
// nothing is sent again.
func TestProjectViewCreateWithAFilter(t *testing.T) {
	const arguments = `{"name":"Bugs","layout":"board","filter":"label:bug"}`
	for _, tt := range []struct {
		name     string
		failure  func(http.ResponseWriter)
		complete bool
		message  string
	}{
		{"a filter GitHub sets", nil, true, ""},
		{"a filter GitHub refuses", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":{"view":null},"errors":[{"type":"UNPROCESSABLE","path":["view"],`+
				`"message":"Filter is too long"}]}`)
		}, false, "the view was created, but its filter was not set: GitHub rejected the change"},
		{"a filter without an answer", func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, false,
			"may have been applied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				last := f.recorded()[len(f.recorded())-1].document
				if tt.failure == nil || !strings.Contains(last, "updateProjectV2View(") {
					return false
				}
				tt.failure(w)
				return true
			}
			core := viewsCore(t, f, nil)
			result, err := invoke(t, core, viewsCreate.ID, "views", arguments, true)
			var created CreatedView
			if err != nil || json.Unmarshal(result, &created) != nil {
				t.Fatalf("result = %s, %v; want the created view", result, err)
			}
			if created.View.Number != 4 || created.Complete != tt.complete || !strings.Contains(created.Error, tt.message) ||
				(tt.complete && created.View.Filter != "label:bug") {
				t.Errorf("created = %+v", created)
			}
			_, mutations, _ := split(f.recorded())
			if len(mutations) != 2 || fmt.Sprint(mutations[1].variables) != "map[filter:label:bug view:V_new]" {
				t.Errorf("mutations = %+v, want the create and one filter change of the new view", mutations)
			}
		})
	}
}

// A change the project refuses ends after its one query, before any mutation: an unknown field or view, visible
// fields of a roadmap, and the last view of a project.
func TestProjectViewChangesAreResolvedBeforeTheMutation(t *testing.T) {
	for _, tt := range []struct{ name, operation, arguments, views, message string }{
		{"an unknown field", viewsCreate.ID, `{"name":"Bugs","layout":"table","fields":["Owner"]}`, "",
			"a name in fields is not a field of this project; its fields are Title, Status,"},
		{"an unknown field of an update", viewsUpdate.ID, `{"view":1,"fields":["Status","Owner"]}`, "",
			"its fields are Title"},
		{"an unknown view", viewsUpdate.ID, `{"view":9,"name":"x"}`, "", "view 9 is not a view of this project; " +
			"its views are 1, 2, 3"},
		{"fields of a roadmap", viewsUpdate.ID, `{"view":3,"fields":["Status"]}`, "", "roadmap view takes no fields"},
		{"a delete of an unknown view", viewsDelete.ID, `{"view":5}`, "", "its views are 1, 2, 3"},
		{"a delete of the last view", viewsDelete.ID, `{"view":1}`, fakeViews[0], "last view of this project"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{views: tt.views}
			core := viewsCore(t, f, nil)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message) {
				t.Errorf("err = %v, want an invalid request with %q", err, tt.message)
			}
			if queries, mutations, _ := split(f.recorded()); len(queries) != 1 || len(mutations) != 0 {
				t.Errorf("requests = %d queries, %d mutations; want one query and no change", len(queries),
					len(mutations))
			}
		})
	}
}

// A view or template change whose outcome is unclear is sent once and says that it may have been applied.
func TestUnclearViewChangesAreNeverRepeated(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{viewsCreate.ID, `{"name":"Bugs","layout":"board","filter":"label:bug"}`},
		{viewsUpdate.ID, `{"view":1,"name":"Open"}`},
		{viewsDelete.ID, `{"view":2}`},
		{templatesMark.ID, `{}`},
		{templatesUnmark.ID, `{}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			f := &fakeGitHub{}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasPrefix(f.recorded()[len(f.recorded())-1].document, "mutation") {
					return false
				}
				w.WriteHeader(http.StatusBadGateway)
				return true
			}
			core := viewsCore(t, f, nil)
			_, err := invoke(t, core, tt.operation, "admin", tt.arguments, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Fatalf("err = %v, want an unclear outcome", err)
			}
			if _, mutations, _ := split(f.recorded()); len(mutations) != 1 {
				t.Errorf("mutations = %d, want exactly one", len(mutations))
			}
		})
	}
}
