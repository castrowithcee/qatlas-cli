package forge

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	projectRepoBase = "/api/v1/repos/acme/widget/projects"
	projectOrgBase  = "/api/v1/orgs/acme/projects"
	projectUserBase = "/api/v1/user/projects"
)

const projectFixture = `{"id":3,"title":"Roadmap","description":"Plan","html_url":"https://forge.example.test/acme/widget/projects/3",` +
	`"state":"open","is_closed":false,"card_type":"text_only","template_type":"basic_kanban","type":"repository",` +
	`"num_issues":2,"num_open_issues":1,"num_closed_issues":1,"owner_id":0,"repo_id":5,"creator":{"id":3,"login":"alice"},` +
	`"creator_id":3,"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z"}`

func columnFixture(id, project string) string {
	return `{"id":` + id + `,"title":"Todo","color":"#FF0000","default":true,"sorting":1,"project_id":` + project +
		`,"creator":{"id":3,"login":"alice"},"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z"}`
}

// projectLevels are the three levels with the connection that reaches each, the issue the level takes, and the
// path of its projects.
var projectLevels = []struct {
	name, connection, target, base string
	issueRepository                string // argument, empty for the default of the repository level
	issuePath                      string
	issueNumber                    string
	issueBody                      func(p *product) string
	issueID                        string
}{
	{"repository", "repo", `"repository":"acme/widget"`, projectRepoBase, "", "/api/v1/repos/acme/widget/issues/7", "7",
		func(p *product) string { return widgetIssue(p, 7, "open") }, "1007"},
	{"organization", "org", `"owner":"orgs/acme"`, projectOrgBase, "acme/widget", "/api/v1/repos/acme/widget/issues/7",
		"7", func(p *product) string { return widgetIssue(p, 7, "open") }, "1007"},
	{"user", "owner", `"owner":"users/Alice"`, projectUserBase, "alice/notes", "/api/v1/repos/alice/notes/issues/5",
		"5", func(p *product) string { return issueFixture(p, 5, "open", false, "alice/notes") }, "1005"},
}

// projectServer answers the project API of one level, the account, and the issues of the fixtures. change
// answers every request it returns a response for.
func projectServer(base string, change func(call) *http.Response) func(call) *http.Response {
	return giteaAt("28.2.0", func(c call) *http.Response {
		if change != nil {
			if response := change(c); response != nil {
				return response
			}
		}
		if c.method != http.MethodGet {
			switch {
			case strings.HasSuffix(c.path, "/move") || strings.HasSuffix(c.path, "/default") ||
				c.method == http.MethodDelete:
				return jsonResponse(204, "")
			case strings.HasSuffix(c.path, "/columns"):
				return jsonResponse(201, columnFixture("9", "3"))
			case c.method == http.MethodPost && strings.Contains(c.path, "/issues/"):
				return jsonResponse(201, "")
			case c.method == http.MethodPost:
				return jsonResponse(201, projectFixture)
			}
			return jsonResponse(200, projectFixture)
		}
		switch c.path {
		case "/api/v1/user":
			return jsonResponse(200, `{"id":3,"login":"alice"}`)
		case "/api/v1/repos/acme/widget/issues/7":
			return jsonResponse(200, widgetIssue(gitea, 7, "open"))
		case "/api/v1/repos/acme/widget/issues/9":
			return jsonResponse(200, issueFixture(gitea, 9, "open", false, "other/place"))
		case "/api/v1/repos/alice/notes/issues/5":
			return jsonResponse(200, issueFixture(gitea, 5, "open", false, "alice/notes"))
		case base:
			return jsonResponse(200, `[`+projectFixture+`,null]`, "Link", `<https://forge.example.test/next>; rel="next"`)
		case base + "/3":
			return jsonResponse(200, projectFixture)
		case base + "/4":
			return jsonResponse(200, strings.Replace(projectFixture, `"id":3`, `"id":99`, 1))
		case base + "/3/columns":
			return jsonResponse(200, `[`+columnFixture("9", "3")+`,`+columnFixture("8", "3")+`,`+columnFixture("7", "4")+`]`)
		case base + "/3/columns/9", base + "/3/columns/10":
			return jsonResponse(200, columnFixture(strings.TrimPrefix(c.path, base+"/3/columns/"), "3"))
		case base + "/3/columns/7":
			return jsonResponse(200, columnFixture("7", "4")) // a column of another project
		case base + "/3/columns/9/issues":
			return jsonResponse(200, `[`+widgetIssue(gitea, 7, "open")+`,`+issueFixture(gitea, 9, "open", true, "other/place")+`]`)
		}
		return notFoundResponse()
	})
}

// projectEnvironment adds connections that list the delete tools.
func projectEnvironment(t *testing.T, handler func(call) *http.Response) *environment {
	t.Helper()
	e := newEnvironment(t, gitea, handler)
	cfg := testConfig(gitea)
	listed := []string{gitea.toolID(projectsDelete), gitea.toolID(projectColumnsDelete)}
	cfg.Connections["listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Tools: listed}
	cfg.Connections["listed-user"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"users/Alice"}, Tools: listed}
	e.core = application.New(registry(t), cfg, resolver(e.red, e.reads), e.red)
	return e
}

// projectCalls are the calls of the operation, without the version reads.
func projectCalls(e *environment) []string {
	var out []string
	for _, c := range *e.calls {
		if !strings.HasSuffix(c.path, "/version") {
			out = append(out, c.method+" "+c.path)
		}
	}
	return out
}

func reset(e *environment) { *e.calls = nil }

var allProjectTools = []*tool{projectsList, projectsGet, projectsCreate, projectsUpdate, projectsDelete,
	projectColumnsList, projectColumnsGet, projectColumnsCreate, projectColumnsUpdate, projectColumnsDelete,
	projectColumnsMove, projectColumnsSetDefault, projectIssuesList, projectIssuesAdd, projectIssuesRemove,
	projectIssuesMove}

func TestProjectToolsAreGiteaOnly(t *testing.T) {
	reg := registry(t)
	for _, tl := range allProjectTools {
		if _, _, ok := reg.Lookup(forgejo.toolID(tl)); ok {
			t.Errorf("%s is registered for Forgejo", forgejo.toolID(tl))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(tl)); !ok {
			t.Errorf("%s is missing", gitea.toolID(tl))
		}
	}
	if len(allProjectTools) != 16 {
		t.Errorf("%d project tools", len(allProjectTools))
	}
}

// Gitea 1.27 has no project API: every tool reports it before its own request.
func TestProjectToolsNeedGitea28(t *testing.T) {
	arguments := map[*tool]string{
		projectsList: `{"repository":"acme/widget"}`, projectsGet: `{"repository":"acme/widget","id":3}`,
		projectsCreate:           `{"repository":"acme/widget","title":"x"}`,
		projectsUpdate:           `{"repository":"acme/widget","id":3,"title":"x"}`,
		projectsDelete:           `{"repository":"acme/widget","id":3}`,
		projectColumnsList:       `{"repository":"acme/widget","id":3}`,
		projectColumnsGet:        `{"repository":"acme/widget","id":3,"column_id":9}`,
		projectColumnsCreate:     `{"repository":"acme/widget","id":3,"title":"x"}`,
		projectColumnsUpdate:     `{"repository":"acme/widget","id":3,"column_id":9,"title":"x"}`,
		projectColumnsDelete:     `{"repository":"acme/widget","id":3,"column_id":9}`,
		projectColumnsMove:       `{"repository":"acme/widget","id":3,"column_ids":[9]}`,
		projectColumnsSetDefault: `{"repository":"acme/widget","id":3,"column_id":9}`,
		projectIssuesList:        `{"repository":"acme/widget","id":3,"column_id":9}`,
		projectIssuesAdd:         `{"repository":"acme/widget","id":3,"column_id":9,"number":7}`,
		projectIssuesRemove:      `{"repository":"acme/widget","id":3,"column_id":9,"number":7}`,
		projectIssuesMove:        `{"repository":"acme/widget","id":3,"column_id":9,"number":7}`,
	}
	e := projectEnvironment(t, giteaAt("1.27.3", nil))
	for _, tl := range allProjectTools {
		reset(e)
		connection := "repo"
		if tl == projectsDelete || tl == projectColumnsDelete {
			connection = "listed"
		}
		_, err := e.invoke(t, tl, connection, arguments[tl])
		if err == nil || !strings.Contains(err.Error(), "needs Gitea 28.0.0 or later") || len(projectCalls(e)) != 0 {
			t.Errorf("%s on 1.27.3: %v, calls %v", tl.group+"."+tl.action, err, projectCalls(e))
		}
	}
}

func TestProjectsListReadsEveryLevel(t *testing.T) {
	cases := []struct {
		connection, argument, path, key, value string
	}{
		{"repo", `"repository":"acme/widget"`, projectRepoBase, "repository", "acme/widget"},
		{"org", `"owner":"orgs/acme"`, projectOrgBase, "owner", "orgs/acme"},
		{"owner", `"owner":"users/Alice"`, "/api/v1/users/Alice/projects", "owner", "users/Alice"},
		{"open", `"owner":"orgs/acme"`, projectOrgBase, "owner", "orgs/acme"},
	}
	for _, c := range cases {
		e := projectEnvironment(t, projectServer(c.path, nil))
		result, err := e.invoke(t, projectsList, c.connection, `{`+c.argument+`,"state":"open","limit":2}`)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		rows, _ := result["projects"].([]any)
		if len(rows) != 1 || result[c.key] != c.value || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s result = %v", c.path, result)
		}
		first := rows[0].(map[string]any)
		for _, name := range []string{"id", "title", "description", "html_url", "state", "card_type", "template_type",
			"type", "num_issues", "num_open_issues", "num_closed_issues", "owner_id", "repo_id", "creator", "created_at",
			"updated_at"} {
			if _, ok := first[name]; !ok {
				t.Errorf("%s: member %s is missing", c.path, name)
			}
		}
		for _, deprecated := range []string{"is_closed", "creator_id"} {
			if _, ok := first[deprecated]; ok {
				t.Errorf("%s: deprecated member %s is shown", c.path, deprecated)
			}
		}
		calls := projectCalls(e)
		if !reflect.DeepEqual(calls, []string{"GET " + c.path}) {
			t.Errorf("%s calls = %v", c.path, calls)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.query != "limit=2&page=1&state=open" {
			t.Errorf("%s query = %q", c.path, last.query)
		}
		next, _ := result["next_cursor"].(string)
		if _, err := e.invoke(t, projectsList, c.connection, `{`+c.argument+`,"state":"open","cursor":"`+next+`"}`); err != nil {
			t.Fatalf("%s continuation: %v", c.path, err)
		}
		if got := (*e.calls)[len(*e.calls)-1].query; got != "limit=2&page=2&state=open" {
			t.Errorf("%s continuation query = %q", c.path, got)
		}
	}
}

// The level is exactly one of repository and owner, and the owner level needs an owner target.
func TestProjectLevelsAreRefusedBeforeTheSecret(t *testing.T) {
	e := projectEnvironment(t, projectServer(projectOrgBase, nil))
	cases := []struct{ connection, arguments string }{
		{"repo", `{"id":3}`},
		{"open", `{"repository":"acme/widget","owner":"orgs/acme","id":3}`},
		{"repo", `{"owner":"orgs/acme","id":3}`},
		{"repo", `{"owner":"users/acme","id":3}`},
		{"mixed", `{"owner":"users/bob","id":3}`},
		{"owner", `{"owner":"users/bob","id":3}`},
		{"owner", `{"owner":"orgs/Alice","id":3}`},
		{"org", `{"owner":"orgs/secret-org","id":3}`},
		{"org", `{"repository":"secret-owner/hidden","id":3}`},
		{"two", `{"owner":"orgs/acme","id":3}`},
		{"org", `{"owner":"orgs/acme","id":0}`},
	}
	for _, c := range cases {
		for _, tl := range []*tool{projectsGet, projectsList, projectColumnsList} {
			if _, err := e.invoke(t, tl, c.connection, c.arguments); err == nil || *e.reads != 0 || len(*e.calls) != 0 ||
				strings.Contains(err.Error(), "secret") {
				t.Errorf("%s %s %s: %v, reads %d, calls %d", tl.group, c.connection, c.arguments, err, *e.reads, len(*e.calls))
			}
		}
	}
	// A repository target reaches its own projects only.
	if _, err := e.invoke(t, projectsGet, "repo", `{"owner":"orgs/acme","id":3}`); !isInvalidRequest(err) {
		t.Errorf("repository target on the owner level: %v", err)
	}
}

func TestProjectGetUpdateDeleteAreBoundToTheirLevel(t *testing.T) {
	for _, l := range projectLevels {
		e := projectEnvironment(t, projectServer(l.base, nil))
		result, err := e.invoke(t, projectsGet, l.connection, `{`+l.target+`,"id":3}`)
		if err != nil || result["id"] != float64(3) || result["title"] != "Roadmap" {
			t.Fatalf("%s get: %v, %v", l.name, result, err)
		}
		// An answer for another ID is invalid; a project of another level is not found on this path.
		if _, err := e.invoke(t, projectsGet, l.connection, `{`+l.target+`,"id":4}`); classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s: answer with another ID: %v", l.name, err)
		}
		if _, err := e.invoke(t, projectsGet, l.connection, `{`+l.target+`,"id":5}`); classOfError(err) != "not-found" {
			t.Errorf("%s: foreign ID: %v", l.name, err)
		}

		reset(e)
		arguments := `{` + l.target + `,"id":3,"title":"New","description":"","state":"closed","card_type":"images_and_text"}`
		if _, err := e.invokeAs(t, projectsUpdate, l.connection, arguments, false); !errors.As(err, new(*application.ConfirmationRequiredError)) ||
			len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed update: %v", l.name, err)
		}
		if _, err := e.invoke(t, projectsUpdate, l.connection, arguments); err != nil {
			t.Fatalf("%s update: %v", l.name, err)
		}
		want := []string{"GET " + l.base + "/3", "PATCH " + l.base + "/3"}
		if l.name == "user" {
			want = append([]string{"GET /api/v1/user"}, want...)
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s update calls = %v", l.name, got)
		}
		sent := (*e.calls)[len(*e.calls)-1]
		if body := bodyOf(t, sent); !reflect.DeepEqual(body, map[string]any{"title": "New", "description": "",
			"state": "closed", "card_type": "images_and_text"}) {
			t.Errorf("%s update body = %v", l.name, body)
		}
		// A foreign ID fails on the evidence read, before any change.
		reset(e)
		if _, err := e.invoke(t, projectsUpdate, l.connection, `{`+l.target+`,"id":5,"title":"x"}`); classOfError(err) != "not-found" {
			t.Errorf("%s update foreign: %v", l.name, err)
		}
		for _, c := range *e.calls {
			if c.method != http.MethodGet {
				t.Errorf("%s: a change followed a failed read: %v", l.name, projectCalls(e))
			}
		}
		for _, bad := range []string{`{"id":3}`, `{"id":3,"state":"all"}`, `{"id":3,"title":"a\u0000b"}`,
			`{"id":3,"card_type":"cards"}`, `{"id":3,"template_type":"none"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, projectsUpdate, l.connection, strings.Replace(bad, "{", `{`+l.target+",", 1)); err == nil ||
				len(*e.calls) != before {
				t.Errorf("%s update %s: %v", l.name, bad, err)
			}
		}
	}
}

func TestProjectCreateSendsOneRequest(t *testing.T) {
	for _, l := range projectLevels {
		e := projectEnvironment(t, projectServer(l.base, nil))
		arguments := `{` + l.target + `,"title":"Roadmap","description":"Plan","card_type":"text_only","template_type":"bug_triage"}`
		if _, err := e.invokeAs(t, projectsCreate, l.connection, arguments, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", l.name, err)
		}
		result, err := e.invoke(t, projectsCreate, l.connection, arguments)
		if err != nil || result["id"] != float64(3) {
			t.Fatalf("%s create: %v, %v", l.name, result, err)
		}
		want := []string{"POST " + l.base}
		if l.name == "user" {
			want = []string{"GET /api/v1/user", "POST " + l.base}
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s create calls = %v", l.name, got)
		}
		if body := bodyOf(t, (*e.calls)[len(*e.calls)-1]); !reflect.DeepEqual(body, map[string]any{"title": "Roadmap",
			"description": "Plan", "card_type": "text_only", "template_type": "bug_triage"}) {
			t.Errorf("%s create body = %v", l.name, body)
		}
		for _, bad := range []string{`{}`, `{"title":""}`, `{"title":"x","template_type":"kanban"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, projectsCreate, l.connection, strings.Replace(bad, "{", `{`+l.target+",", 1)); err == nil ||
				len(*e.calls) != before {
				t.Errorf("%s create %s: %v", l.name, bad, err)
			}
		}
	}
}

// Another user's projects can be listed, but never changed: the user level acts on the token's account only.
func TestUserLevelChangesNeedTheAccountBehindTheToken(t *testing.T) {
	e := projectEnvironment(t, projectServer(projectUserBase, func(c call) *http.Response {
		if c.path == "/api/v1/user" {
			return jsonResponse(200, `{"id":4,"login":"carol"}`)
		}
		return nil
	}))
	for tl, arguments := range map[*tool]string{
		projectsCreate:       `{"owner":"users/Alice","title":"x"}`,
		projectsUpdate:       `{"owner":"users/Alice","id":3,"title":"x"}`,
		projectColumnsCreate: `{"owner":"users/Alice","id":3,"title":"x"}`,
		projectColumnsMove:   `{"owner":"users/Alice","id":3,"column_ids":[9]}`,
	} {
		reset(e)
		_, err := e.invoke(t, tl, "owner", arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "Alice") {
			t.Errorf("%s: %v", tl.action, err)
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, []string{"GET /api/v1/user"}) {
			t.Errorf("%s calls = %v", tl.action, got)
		}
	}
	// The public list of that user needs no account read.
	reset(e)
	if _, err := e.invoke(t, projectsList, "owner", `{"owner":"users/Alice"}`); classOfError(err) != "not-found" ||
		!reflect.DeepEqual(projectCalls(e), []string{"GET /api/v1/users/Alice/projects"}) {
		t.Errorf("list: %v, %v", err, projectCalls(e))
	}
}

func TestProjectDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(gitea.id)
	for _, tl := range []*tool{projectsDelete, projectColumnsDelete} {
		d, _, ok := reg.Lookup(gitea.toolID(tl))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
			t.Fatalf("%s descriptor = %+v", tl.group, d)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
	for _, l := range projectLevels {
		connection := "listed"
		if l.name == "user" {
			connection = "listed-user"
		}
		e := projectEnvironment(t, projectServer(l.base, nil))
		columnArguments := `{` + l.target + `,"id":3,"column_id":9}`
		if _, err := e.invoke(t, projectColumnsDelete, l.connection, columnArguments); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", l.name, err)
		}
		if _, err := e.invokeAs(t, projectsDelete, connection, `{`+l.target+`,"id":3}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", l.name, err)
		}
		result, err := e.invoke(t, projectsDelete, connection, `{`+l.target+`,"id":3}`)
		if err != nil || result["deleted"] != true || result["id"] != float64(3) {
			t.Fatalf("%s project delete: %v, %v", l.name, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodDelete || last.path != l.base+"/3" || last.body != "" {
			t.Errorf("%s project delete call %+v", l.name, last)
		}
		reset(e)
		result, err = e.invoke(t, projectColumnsDelete, connection, columnArguments)
		if err != nil || result["deleted"] != true || result["id"] != float64(9) {
			t.Fatalf("%s column delete: %v, %v", l.name, result, err)
		}
		if got := projectCalls(e); got[len(got)-2] != "GET "+l.base+"/3/columns/9" || got[len(got)-1] != "DELETE "+l.base+"/3/columns/9" {
			t.Errorf("%s column delete calls = %v", l.name, got)
		}
	}
}

func TestColumnsAreBoundToTheirProject(t *testing.T) {
	for _, l := range projectLevels {
		e := projectEnvironment(t, projectServer(l.base, nil))
		columns, err := e.invoke(t, projectColumnsList, l.connection, `{`+l.target+`,"id":3,"limit":5}`)
		rows, _ := columns["columns"].([]any)
		// The column of another project in the answer is dropped.
		if err != nil || len(rows) != 2 || rows[0].(map[string]any)["title"] != "Todo" {
			t.Fatalf("%s list: %v, %v", l.name, columns, err)
		}
		for _, name := range []string{"id", "title", "color", "default", "sorting", "project_id", "creator", "created_at",
			"updated_at"} {
			if _, ok := rows[0].(map[string]any)[name]; !ok {
				t.Errorf("%s: column member %s is missing", l.name, name)
			}
		}
		if got, err := e.invoke(t, projectColumnsGet, l.connection, `{`+l.target+`,"id":3,"column_id":9}`); err != nil ||
			got["id"] != float64(9) || got["project_id"] != float64(3) {
			t.Fatalf("%s get: %v, %v", l.name, got, err)
		}
		// A column of another project is refused, also when the instance answers it on this path.
		if _, err := e.invoke(t, projectColumnsGet, l.connection, `{`+l.target+`,"id":3,"column_id":7}`); classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s: foreign column: %v", l.name, err)
		}
		for _, tl := range []*tool{projectColumnsUpdate, projectColumnsSetDefault} {
			reset(e)
			if _, err := e.invoke(t, tl, l.connection, `{`+l.target+`,"id":3,"column_id":7,"title":"x"}`); err == nil {
				t.Errorf("%s %s: foreign column accepted", l.name, tl.action)
			}
			for _, c := range *e.calls {
				if c.method != http.MethodGet {
					t.Errorf("%s %s: a change followed a failed read", l.name, tl.action)
				}
			}
		}

		reset(e)
		if _, err := e.invoke(t, projectColumnsCreate, l.connection, `{`+l.target+`,"id":3,"title":"Review","color":"#00ff00"}`); err != nil {
			t.Fatalf("%s create: %v", l.name, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPost || last.path != l.base+"/3/columns" ||
			!reflect.DeepEqual(bodyOf(t, last), map[string]any{"title": "Review", "color": "#00ff00"}) {
			t.Errorf("%s create call %+v", l.name, last)
		}
		reset(e)
		if _, err := e.invoke(t, projectColumnsUpdate, l.connection, `{`+l.target+`,"id":3,"column_id":9,"sorting":-3,"color":"#00ff00"}`); err != nil {
			t.Fatalf("%s update: %v", l.name, err)
		}
		last = (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPatch || last.path != l.base+"/3/columns/9" ||
			!reflect.DeepEqual(bodyOf(t, last), map[string]any{"sorting": float64(-3), "color": "#00ff00"}) {
			t.Errorf("%s update call %+v", l.name, last)
		}
		reset(e)
		if result, err := e.invoke(t, projectColumnsSetDefault, l.connection, `{`+l.target+`,"id":3,"column_id":9}`); err != nil ||
			result["default"] != true {
			t.Fatalf("%s set-default: %v, %v", l.name, result, err)
		}
		last = (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPost || last.path != l.base+"/3/columns/9/default" || last.body != "" {
			t.Errorf("%s set-default call %+v", l.name, last)
		}
		for _, bad := range []string{`{"id":3,"title":"x","color":"red"}`, `{"id":3,"title":"x","color":"#abc"}`,
			`{"id":3,"column_id":9,"sorting":128}`, `{"id":3,"column_id":9}`} {
			before := len(*e.calls)
			tl := projectColumnsUpdate
			if !strings.Contains(bad, "column_id") {
				tl = projectColumnsCreate
			}
			if _, err := e.invoke(t, tl, l.connection, strings.Replace(bad, "{", `{`+l.target+",", 1)); err == nil ||
				len(*e.calls) != before {
				t.Errorf("%s %s %s: %v", l.name, tl.action, bad, err)
			}
		}
	}
}

func TestColumnMoveNeedsEveryColumnOfTheProject(t *testing.T) {
	server := func(base string) func(call) *http.Response {
		return projectServer(base, func(c call) *http.Response {
			if c.method == http.MethodGet && strings.HasSuffix(c.path, "/3/columns") {
				return jsonResponse(200, `[`+columnFixture("9", "3")+`,`+columnFixture("8", "3")+`]`)
			}
			return nil
		})
	}
	for _, l := range projectLevels {
		e := projectEnvironment(t, server(l.base))
		if _, err := e.invoke(t, projectColumnsMove, l.connection, `{`+l.target+`,"id":3,"column_ids":[8,9]}`); err != nil {
			t.Fatalf("%s move: %v", l.name, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPost || last.path != l.base+"/3/columns/move" ||
			!reflect.DeepEqual(bodyOf(t, last), map[string]any{"column_ids": []any{float64(8), float64(9)}}) {
			t.Errorf("%s move call %+v", l.name, last)
		}
		for _, ids := range []string{`[9]`, `[9,8,7]`, `[9,10]`, `[9,9]`, `[]`, `[0,9]`} {
			reset(e)
			if _, err := e.invoke(t, projectColumnsMove, l.connection, `{`+l.target+`,"id":3,"column_ids":`+ids+`}`); !isInvalidRequest(err) {
				t.Errorf("%s move %s: %v", l.name, ids, err)
			}
			for _, c := range *e.calls {
				if c.method == http.MethodPost {
					t.Errorf("%s move %s sent a change", l.name, ids)
				}
			}
		}
	}
}

func TestProjectIssuesListDropsRowsOutsideTheTargets(t *testing.T) {
	e := projectEnvironment(t, projectServer(projectRepoBase, nil))
	result, err := e.invoke(t, projectIssuesList, "repo", `{"repository":"acme/widget","id":3,"column_id":9}`)
	issues, _ := result["issues"].([]any)
	// The pull request of another repository is dropped; the issue of the repository stays.
	if err != nil || len(issues) != 1 || issues[0].(map[string]any)["number"] != float64(7) {
		t.Fatalf("list: %v, %v", result, err)
	}
	if got := projectCalls(e); !reflect.DeepEqual(got, []string{"GET " + projectRepoBase + "/3/columns/9",
		"GET " + projectRepoBase + "/3/columns/9/issues"}) {
		t.Errorf("calls = %v", got)
	}
	// Without targets the pull request stays: a project holds issues and pull requests.
	e = projectEnvironment(t, projectServer(projectOrgBase, nil))
	result, err = e.invoke(t, projectIssuesList, "open", `{"owner":"orgs/acme","id":3,"column_id":9}`)
	if issues, _ := result["issues"].([]any); err != nil || len(issues) != 2 {
		t.Fatalf("open list: %v, %v", result, err)
	}
}

func TestProjectIssuesAreBoundBeforeTheProjectIsTouched(t *testing.T) {
	for _, l := range projectLevels {
		repository := ""
		if l.issueRepository != "" {
			repository = `,"issue_repository":"` + l.issueRepository + `"`
		}
		base := `{` + l.target + `,"id":3,"column_id":9,"number":` + l.issueNumber + repository + `}`
		e := projectEnvironment(t, projectServer(l.base, nil))
		prefix := []string{}
		if l.name == "user" {
			prefix = []string{"GET /api/v1/user"}
		}
		add := append(append([]string{}, prefix...), "GET "+l.issuePath, "GET "+l.base+"/3/columns/9",
			"POST "+l.base+"/3/columns/9/issues/"+l.issueID)
		result, err := e.invoke(t, projectIssuesAdd, l.connection, base)
		if err != nil || result["issue_id"] != mustFloat(l.issueID) || result["done"] != true {
			t.Fatalf("%s add: %v, %v", l.name, result, err)
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, add) {
			t.Fatalf("%s add calls = %v, want %v", l.name, got, add)
		}
		if last := (*e.calls)[len(*e.calls)-1]; last.body != "" {
			t.Errorf("%s add sent a body", l.name)
		}

		reset(e)
		remove := append(append([]string{}, prefix...), "GET "+l.issuePath, "GET "+l.base+"/3/columns/9",
			"DELETE "+l.base+"/3/columns/9/issues/"+l.issueID)
		if _, err := e.invoke(t, projectIssuesRemove, l.connection, base); err != nil {
			t.Fatalf("%s remove: %v", l.name, err)
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, remove) {
			t.Fatalf("%s remove calls = %v", l.name, got)
		}

		reset(e)
		move := append(append([]string{}, prefix...), "GET "+l.issuePath, "GET "+l.base+"/3/columns/9",
			"POST "+l.base+"/3/issues/"+l.issueID+"/move")
		withPosition := strings.TrimSuffix(base, "}") + `,"position":-2}`
		if _, err := e.invoke(t, projectIssuesMove, l.connection, withPosition); err != nil {
			t.Fatalf("%s move: %v", l.name, err)
		}
		if got := projectCalls(e); !reflect.DeepEqual(got, move) {
			t.Fatalf("%s move calls = %v", l.name, got)
		}
		if body := bodyOf(t, (*e.calls)[len(*e.calls)-1]); !reflect.DeepEqual(body, map[string]any{"column_id": float64(9),
			"sorting": float64(-2)}) {
			t.Errorf("%s move body = %v", l.name, body)
		}
		reset(e)
		if _, err := e.invoke(t, projectIssuesMove, l.connection, base); err != nil {
			t.Fatalf("%s move without position: %v", l.name, err)
		}
		if body := bodyOf(t, (*e.calls)[len(*e.calls)-1]); !reflect.DeepEqual(body, map[string]any{"column_id": float64(9)}) {
			t.Errorf("%s move body = %v", l.name, body)
		}
		// A foreign column of the project is refused before the change.
		reset(e)
		foreign := strings.Replace(base, `"column_id":9`, `"column_id":7`, 1)
		for _, tl := range []*tool{projectIssuesAdd, projectIssuesRemove, projectIssuesMove} {
			reset(e)
			if _, err := e.invoke(t, tl, l.connection, foreign); err == nil {
				t.Errorf("%s %s: foreign column accepted", l.name, tl.action)
			}
			for _, c := range *e.calls {
				if c.method != http.MethodGet {
					t.Errorf("%s %s: a change followed a failed read", l.name, tl.action)
				}
			}
		}
	}
}

func TestProjectIssueOutsideTheTargetsIsRefused(t *testing.T) {
	e := projectEnvironment(t, projectServer(projectOrgBase, nil))
	repoEnvironment := projectEnvironment(t, projectServer(projectRepoBase, nil))
	cases := []struct {
		e                    *environment
		connection, argument string
		calls                int
	}{
		// Outside the targets, before any request and before the secret.
		{e, "org", `"owner":"orgs/acme","issue_repository":"secret-owner/hidden"`, 0},
		{e, "org", `"owner":"orgs/acme"`, 0},
		{e, "org", `"owner":"orgs/acme","issue_repository":"other/place"`, 0},
		{repoEnvironment, "repo", `"repository":"acme/widget","issue_repository":"acme/gadget"`, 0},
		{repoEnvironment, "repo", `"repository":"acme/widget","issue_repository":"secret-owner/hidden"`, 0},
	}
	for _, tl := range []*tool{projectIssuesAdd, projectIssuesMove, projectIssuesRemove} {
		for _, c := range cases {
			reset(c.e)
			*c.e.reads = 0
			_, err := c.e.invoke(t, tl, c.connection, `{`+c.argument+`,"id":3,"column_id":9,"number":7}`)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *c.e.reads != 0 ||
				len(*c.e.calls) != c.calls {
				t.Errorf("%s %s: %v, reads %d, calls %d", tl.action, c.argument, err, *c.e.reads, len(*c.e.calls))
			}
		}
	}
	// A canonical repository outside the level is found by the evidence read and stops before the project.
	reset(repoEnvironment)
	_, err := repoEnvironment.invoke(t, projectIssuesAdd, "repo", `{"repository":"acme/widget","id":3,"column_id":9,"number":9}`)
	if err == nil || !reflect.DeepEqual(projectCalls(repoEnvironment), []string{"GET /api/v1/repos/acme/widget/issues/9"}) {
		t.Errorf("renamed repository: %v, %v", err, projectCalls(repoEnvironment))
	}
	// On a connection that allows only the repository, an owner-level call is refused before anything else.
	reset(repoEnvironment)
	if _, err := repoEnvironment.invoke(t, projectIssuesAdd, "repo", `{"owner":"orgs/acme","issue_repository":"acme/widget",`+
		`"id":3,"column_id":9,"number":7}`); !isInvalidRequest(err) || len(*repoEnvironment.calls) != 0 {
		t.Errorf("repository target on the owner level: %v", err)
	}
}

// A change is sent once; a server error is reported as uncertain and not repeated.
func TestProjectChangesAreNotRetried(t *testing.T) {
	e := projectEnvironment(t, projectServer(projectRepoBase, func(c call) *http.Response {
		if c.method == http.MethodPost || c.method == http.MethodPatch {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		}
		return nil
	}))
	for _, c := range []struct {
		tl        *tool
		arguments string
	}{
		{projectsCreate, `{"repository":"acme/widget","title":"x"}`},
		{projectsUpdate, `{"repository":"acme/widget","id":3,"title":"x"}`},
		{projectColumnsCreate, `{"repository":"acme/widget","id":3,"title":"x"}`},
		{projectIssuesAdd, `{"repository":"acme/widget","id":3,"column_id":9,"number":7}`},
		{projectIssuesMove, `{"repository":"acme/widget","id":3,"column_id":9,"number":7}`},
	} {
		reset(e)
		_, err := e.invoke(t, c.tl, "repo", c.arguments)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") {
			t.Errorf("%s: %v", c.tl.action, err)
		}
		changes := 0
		for _, call := range *e.calls {
			if call.method != http.MethodGet {
				changes++
			}
		}
		if changes != 1 {
			t.Errorf("%s sent %d changes", c.tl.action, changes)
		}
	}
}

func mustFloat(s string) float64 {
	switch s {
	case "1007":
		return 1007
	case "1005":
		return 1005
	}
	return -1
}
