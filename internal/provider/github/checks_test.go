package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const checkCanary = "check-provider-text-canary-3b9a"

// checkSuiteDocument is a check suite as the Get a check suite example of GitHub's REST API describes it.
const checkSuiteDocument = `{"id":5,"node_id":"MDEwOkNoZWNrU3VpdGU1","head_branch":"master",` +
	`"head_sha":"d6fde92930d4715a2b49857d24b940956b26d2d3","status":"completed","conclusion":"neutral",` +
	`"url":"https://api.github.com/repos/octo-org/example/check-suites/5","before":"146e867f55c26428e5f9fade55a9bbf5e95a7912",` +
	`"after":"d6fde92930d4715a2b49857d24b940956b26d2d3","pull_requests":[{` +
	`"url":"https://api.github.com/repos/octo-org/example/pulls/1","id":1934,"number":3,` +
	`"head":{"ref":"say-hello","sha":"3dca65830a8db1f2c7cdf0beb31a7ff3a1ba0d83","repo":{"id":526,` +
	`"url":"https://api.github.com/repos/octo-org/example","name":"example"}},` +
	`"base":{"ref":"master","sha":"e7fdf7640066d71ad16a86fbcbb9c6a10a18af4f","repo":{"id":526,` +
	`"url":"https://api.github.com/repos/octo-org/example","name":"example"}}}],"app":` + widthAppRaw + `,` +
	`"repository":{"id":526,"node_id":"MDEwOlJlcG9zaXRvcnk1MjY=","name":"example","full_name":"octo-org/example",` +
	`"private":false,"fork":false},"created_at":"2012-07-20T01:19:13Z","updated_at":"2012-07-20T01:19:14Z",` +
	`"head_commit":{"id":"7638417db6d59f3c431d3e1f261cc637155684cd","tree_id":"d23f6eedb1e1b9610bbc754ddb5197bfe7271223",` +
	`"message":"Merge pull request #6\n\nFirst commit","timestamp":"2014-08-17T08:18:30-07:00",` +
	`"author":{"name":"The Octocat","email":"octocat@example.invalid"},"committer":{"name":"The Octocat","email":"octocat@example.invalid"}},` +
	`"latest_check_runs_count":1,"check_runs_url":"https://api.github.com/repos/octo-org/example/check-suites/5/check-runs",` +
	`"rerequestable":true,"runs_rerequestable":false}`

// checkRunAt is the check run document with its REST address below the fake server's root.
func checkRunAt(base string) string {
	return strings.ReplaceAll(widthCheckRun, "https://api.github.com/repos/octo-org/example/check-runs/4",
		base+"/repos/octo-org/example/check-runs/4")
}

// fakeChecks answers the check routes of the fake repository by the part of the path below the repository.
type fakeChecks struct {
	f    *fakeGitHub
	base string
	// answer replaces the answer of a route when it returns true.
	answer func(w http.ResponseWriter, r *http.Request, rest string) bool
}

func serveChecks(t *testing.T) (*fakeChecks, string) {
	t.Helper()
	c := &fakeChecks{}
	c.f = &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
		if !ok || !(strings.HasPrefix(rest, "check-") || strings.HasPrefix(rest, "commits/")) {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		if c.answer != nil && c.answer(w, r, rest) {
			return true
		}
		return c.route(w, r, rest)
	}}
	c.base = serve(t, c.f)
	return c, c.base
}

func (c *fakeChecks) route(w http.ResponseWriter, r *http.Request, rest string) bool {
	page := r.URL.Query().Get("page")
	switch {
	case rest == "check-runs/4":
		fmt.Fprint(w, checkRunAt(c.base))
	case rest == "check-runs/9":
		// A check run of another repository: its own address names that repository.
		fmt.Fprint(w, `{"id":9,"name":"secret-build","status":"completed","url":"`+c.base+
			`/repos/other-org/private/check-runs/9"}`)
	case rest == "check-runs/10":
		fmt.Fprint(w, `{"id":10,"name":"secret-build","status":"completed"}`)
	case rest == "check-runs/11":
		fmt.Fprint(w, `{"id":12,"name":"other-id","status":"completed","url":"`+c.base+
			`/repos/octo-org/example/check-runs/11"}`)
	case strings.HasPrefix(rest, "check-runs/4/annotations"):
		if page == "1" || page == "" {
			w.Header().Set("Link", `<`+c.base+`/x?page=2>; rel="next"`)
			fmt.Fprint(w, `[{"path":"README.md","start_line":2,"end_line":2,"start_column":5,"end_column":10,`+
				`"annotation_level":"warning","title":"Spell\u0000Checker","message":"Check your spelling for 'banaas'.",`+
				`"raw_details":"`+strings.Repeat("d", projectionBodyLimit+10)+`",`+
				`"blob_href":"http://github.com/octo-org/example/blob/abc/README.md"}]`)
			return true
		}
		fmt.Fprint(w, `[{"path":"a.go","start_line":1,"end_line":3,"start_column":null,"end_column":null,`+
			`"annotation_level":null,"title":null,"message":null,"raw_details":null,"blob_href":"http://github.com/x"}]`)
	case strings.HasPrefix(rest, "check-runs/9/annotations"):
		fmt.Fprint(w, `[{"path":"leak.go","message":"`+checkCanary+`"}]`)
	case rest == "check-suites/5":
		fmt.Fprint(w, checkSuiteDocument)
	case rest == "check-suites/6":
		fmt.Fprint(w, `{"id":6,"head_sha":"abc1","repository":{"id":7,"full_name":"other-org/private"}}`)
	case rest == "check-suites/7":
		fmt.Fprint(w, `{"id":7,"head_sha":"abc1"}`)
	case strings.HasPrefix(rest, "check-suites/5/check-runs"), strings.HasPrefix(rest, "check-suites/6/check-runs"):
		fmt.Fprint(w, `{"total_count":1,"check_runs":[`+checkRunAt(c.base)+`]}`)
	case strings.HasSuffix(rest, "/check-suites"):
		fmt.Fprint(w, `{"total_count":1,"check_suites":[`+checkSuiteDocument+`]}`)
	case strings.HasSuffix(rest, "/check-runs"):
		if page == "1" {
			w.Header().Set("Link", `<`+c.base+`/x?page=2>; rel="next"`)
			fmt.Fprint(w, `{"total_count":3,"check_runs":[{"id":1,"name":"build","status":"completed"},`+
				`{"id":2,"name":"lint","status":"queued"}]}`)
			return true
		}
		fmt.Fprint(w, `{"total_count":3,"check_runs":[{"id":3,"name":"test","status":"in_progress"}]}`)
	default:
		return false
	}
	return true
}

func checksCore(t *testing.T, base string, reads *int) *application.Core {
	t.Helper()
	red := &redact.Redactor{}
	return application.New(registry(t), coreConfig(base), resolver(red, reads), red)
}

func requestsTo(f *fakeGitHub, suffix string) []recorded {
	var found []recorded
	for _, request := range f.recorded() {
		if strings.Contains(request.path, suffix) {
			found = append(found, request)
		}
	}
	return found
}

func TestCheckRunsGetCarriesTheFullCheckRun(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	result, err := invoke(t, core, checkRunsGet.ID, "repo", `{"check_run_id":4}`, false)
	if err != nil {
		t.Fatal(err)
	}
	view := strings.ReplaceAll(widthCheckRunView, "https://api.github.com/repos/octo-org/example/check-runs/4",
		base+"/repos/octo-org/example/check-runs/4")
	assertJSON(t, json.RawMessage(result), strings.TrimSuffix(view, "}")+`,"repository":"octo-org/example"}`)

	top := decodeAny(t, result).(map[string]any)
	delete(top, "repository")
	delete(top["app"].(map[string]any), "permissions")
	delete(top["deployment"].(map[string]any)["performed_via_github_app"].(map[string]any), "permissions")
	checkSchemaCovers(t, "$", top, decodeAny(t, checkRunsGet.OutputSchema).(map[string]any))
	if got := len(c.f.recorded()); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
}

func TestCheckRunsListSendsFiltersAndPages(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	arguments := `{"ref":"feature/x","check_name":"build","status":"completed","filter":"all","app_id":15368,"limit":2`
	first, err := invoke(t, core, checkRunsList.ID, "repo", arguments+`}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Ref        string           `json:"ref"`
		CheckRuns  []map[string]any `json:"check_runs"`
		TotalCount int              `json:"total_count"`
		NextCursor string           `json:"next_cursor"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(first, &page); err != nil {
		t.Fatal(err)
	}
	if page.Ref != "feature/x" || len(page.CheckRuns) != 2 || page.TotalCount != 3 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first batch = %s", first)
	}
	second, err := invoke(t, core, checkRunsList.ID, "repo", arguments+`,"cursor":"`+page.NextCursor+`"}`, false)
	if err != nil || !strings.Contains(string(second), `"has_more":false`) || strings.Contains(string(second), "next_cursor") ||
		!strings.Contains(string(second), `"name":"test"`) {
		t.Fatalf("second batch = %s, %v", second, err)
	}
	requests := requestsTo(c.f, "/check-runs")
	if len(requests) != 2 || requests[0].path != actionsPrefix+"commits/feature/x/check-runs" ||
		requests[0].query != "app_id=15368&check_name=build&filter=all&page=1&per_page=2&status=completed" ||
		requests[1].query != "app_id=15368&check_name=build&filter=all&page=2&per_page=2&status=completed" {
		t.Errorf("requests = %+v", requests)
	}
	// A cursor belongs to the filters it was issued for.
	if _, err := invoke(t, core, checkRunsList.ID, "repo",
		`{"ref":"feature/x","check_name":"lint","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
	// Without a filter nothing but the page is sent.
	if _, err := invoke(t, core, checkRunsList.ID, "repo", `{"ref":"main"}`, false); err != nil {
		t.Fatal(err)
	}
	if last := c.f.recorded()[len(c.f.recorded())-1]; last.query != "page=1&per_page=30" {
		t.Errorf("query without filters = %q", last.query)
	}
	entry := decodeAny(t, entrySchema(t, checkRunsList.OutputSchema, "check_runs")).(map[string]any)
	checkSchemaCovers(t, "$.check_runs[]", page.CheckRuns[0], entry)
}

func TestCheckAnnotationsAreBoundToTheRepositoryAndCutHard(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	first, err := invoke(t, core, checkAnnotationsList.ID, "repo", `{"check_run_id":4,"limit":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		CheckRunID  int64            `json:"check_run_id"`
		Annotations []map[string]any `json:"annotations"`
		NextCursor  string           `json:"next_cursor"`
		HasMore     bool             `json:"has_more"`
	}
	if err := json.Unmarshal(first, &page); err != nil {
		t.Fatal(err)
	}
	note := page.Annotations[0]
	if page.CheckRunID != 4 || !page.HasMore || len(page.Annotations) != 1 || note["title"] != "Spell Checker" ||
		note["raw_details_truncated"] != true || note["path"] != "README.md" || note["start_column"] != float64(5) ||
		note["links"].(map[string]any)["blob_href"] != "http://github.com/octo-org/example/blob/abc/README.md" {
		t.Errorf("first batch = %s", first)
	}
	if got := []rune(note["raw_details"].(string)); len(got) > projectionBodyLimit+1 {
		t.Errorf("raw_details holds %d runes, want the hard cut", len(got))
	}
	checkSchemaCovers(t, "$.annotations[]", note, decodeAny(t, entrySchema(t, checkAnnotationsList.OutputSchema,
		"annotations")).(map[string]any))
	second, err := invoke(t, core, checkAnnotationsList.ID, "repo",
		`{"check_run_id":4,"limit":1,"cursor":"`+page.NextCursor+`"}`, false)
	if err != nil || !strings.Contains(string(second), `"path":"a.go"`) || !strings.Contains(string(second), `"has_more":false`) {
		t.Errorf("second batch = %s, %v", second, err)
	}
	// Every batch reads the check run first; the query holds only the page.
	var order []string
	for _, request := range c.f.recorded() {
		order = append(order, request.path[len(actionsPrefix):]+"?"+request.query)
	}
	want := []string{"check-runs/4?", "check-runs/4/annotations?page=1&per_page=1", "check-runs/4?",
		"check-runs/4/annotations?page=2&per_page=1"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Errorf("requests = %v, want %v", order, want)
	}
}

// A check run whose address names another repository, names none, or is another check run is refused before
// any value of it or of its annotations leaves the call.
func TestForeignCheckRunsAreRefusedWithoutValues(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	for _, tt := range []struct{ operation, arguments string }{
		{checkRunsGet.ID, `{"check_run_id":9}`},
		{checkRunsGet.ID, `{"check_run_id":10}`},
		{checkRunsGet.ID, `{"check_run_id":11}`},
		{checkAnnotationsList.ID, `{"check_run_id":9}`},
		{checkAnnotationsList.ID, `{"check_run_id":10}`},
	} {
		result, err := invoke(t, core, tt.operation, "repo", tt.arguments, false)
		if err == nil || len(result) != 0 {
			t.Errorf("%s %s = %s, %v, want a refusal", tt.operation, tt.arguments, result, err)
			continue
		}
		for _, leak := range []string{"secret-build", "other-org", "private", checkCanary, "other-id"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s %s: the refusal carries %q: %v", tt.operation, tt.arguments, leak, err)
			}
		}
	}
	if got := requestsTo(c.f, "/annotations"); len(got) != 0 {
		t.Errorf("annotations were requested for a foreign check run: %+v", got)
	}
}

func TestCheckSuitesListCarriesTheFullSuite(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	result, err := invoke(t, core, checkSuitesList.ID, "repo", `{"ref":"main","app_id":15368,"check_name":"build"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Ref         string           `json:"ref"`
		CheckSuites []map[string]any `json:"check_suites"`
		TotalCount  int              `json:"total_count"`
		HasMore     bool             `json:"has_more"`
	}
	if err := json.Unmarshal(result, &page); err != nil {
		t.Fatal(err)
	}
	suite := page.CheckSuites[0]
	for name, want := range map[string]any{"id": float64(5), "head_branch": "master", "conclusion": "neutral",
		"before": "146e867f55c26428e5f9fade55a9bbf5e95a7912", "latest_check_runs_count": float64(1),
		"rerequestable": true, "runs_rerequestable": false} {
		if suite[name] != want {
			t.Errorf("%s = %v, want %v", name, suite[name], want)
		}
	}
	head := suite["head_commit"].(map[string]any)
	pulls := suite["pull_requests"].([]any)
	links := suite["links"].(map[string]any)
	if page.Ref != "main" || page.TotalCount != 1 || page.HasMore || head["tree_id"] == nil ||
		head["author"].(map[string]any)["name"] != "The Octocat" || len(pulls) != 1 ||
		suite["repository"].(map[string]any)["full_name"] != "octo-org/example" ||
		suite["app"].(map[string]any)["slug"] != "octoapp" ||
		links["check_runs_url"] != "https://api.github.com/repos/octo-org/example/check-suites/5/check-runs" ||
		links["url"] != "https://api.github.com/repos/octo-org/example/check-suites/5" {
		t.Errorf("suite = %s", result)
	}
	delete(suite["app"].(map[string]any), "permissions")
	checkSchemaCovers(t, "$.check_suites[]", suite, decodeAny(t, entrySchema(t, checkSuitesList.OutputSchema,
		"check_suites")).(map[string]any))
	requests := requestsTo(c.f, "/check-suites")
	if len(requests) != 1 || requests[0].path != actionsPrefix+"commits/main/check-suites" ||
		requests[0].query != "app_id=15368&check_name=build&page=1&per_page=30" {
		t.Errorf("requests = %+v", requests)
	}
}

func TestCheckSuiteRunsReadTheSuiteFirstAndRefuseAForeignOne(t *testing.T) {
	c, base := serveChecks(t)
	core := checksCore(t, base, nil)
	result, err := invoke(t, core, checkSuiteRunsList.ID, "repo",
		`{"check_suite_id":5,"check_name":"mighty_readme","status":"completed","filter":"latest","limit":50}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		CheckSuiteID int64            `json:"check_suite_id"`
		CheckRuns    []map[string]any `json:"check_runs"`
		TotalCount   int              `json:"total_count"`
	}
	if err := json.Unmarshal(result, &page); err != nil || page.CheckSuiteID != 5 || len(page.CheckRuns) != 1 ||
		page.TotalCount != 1 || page.CheckRuns[0]["name"] != "mighty_readme" {
		t.Fatalf("result = %s, %v", result, err)
	}
	delete(page.CheckRuns[0]["app"].(map[string]any), "permissions")
	delete(page.CheckRuns[0]["deployment"].(map[string]any)["performed_via_github_app"].(map[string]any), "permissions")
	checkSchemaCovers(t, "$.check_runs[]", page.CheckRuns[0], decodeAny(t, entrySchema(t, checkSuiteRunsList.OutputSchema,
		"check_runs")).(map[string]any))
	requests := c.f.recorded()
	if len(requests) != 2 || requests[0].path != actionsPrefix+"check-suites/5" ||
		requests[1].path != actionsPrefix+"check-suites/5/check-runs" ||
		requests[1].query != "check_name=mighty_readme&filter=latest&page=1&per_page=50&status=completed" {
		t.Errorf("requests = %+v", requests)
	}

	before := len(c.f.recorded())
	for _, id := range []string{"6", "7"} {
		out, err := invoke(t, core, checkSuiteRunsList.ID, "repo", `{"check_suite_id":`+id+`}`, false)
		if err == nil || len(out) != 0 || strings.Contains(err.Error(), "other-org") {
			t.Errorf("suite %s = %s, %v, want a refusal without values", id, out, err)
		}
	}
	if got := requestsTo(c.f, "/6/check-runs"); len(got) != 0 || len(c.f.recorded())-before != 2 {
		t.Errorf("check runs of a foreign suite were requested: %+v", got)
	}
}

func TestCheckReadsMapFailuresWithoutProviderText(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		class   provider.Class
		invalid bool
		detail  string
	}{
		{"forbidden", http.StatusForbidden, provider.ClassPermission, false, "Checks: read"},
		{"not found", http.StatusNotFound, provider.ClassNotFound, false, ""},
		{"unprocessable", http.StatusUnprocessableEntity, "", true, ""},
	} {
		for _, call := range []struct{ operation, arguments string }{
			{checkRunsList.ID, `{"ref":"main"}`},
			{checkRunsGet.ID, `{"check_run_id":4}`},
			{checkAnnotationsList.ID, `{"check_run_id":4}`},
			{checkSuitesList.ID, `{"ref":"main"}`},
			{checkSuiteRunsList.ID, `{"check_suite_id":5}`},
		} {
			t.Run(tt.name+" "+call.operation, func(t *testing.T) {
				c, base := serveChecks(t)
				c.answer = func(w http.ResponseWriter, _ *http.Request, _ string) bool {
					w.WriteHeader(tt.status)
					fmt.Fprint(w, `{"message":"`+checkCanary+`"}`)
					return true
				}
				core := checksCore(t, base, nil)
				_, err := invoke(t, core, call.operation, "repo", call.arguments, false)
				if err == nil || strings.Contains(err.Error(), checkCanary) {
					t.Fatalf("err = %v, want an error without the provider text", err)
				}
				if tt.invalid && !isInvalidRequest(err) {
					t.Errorf("err = %v, want an invalid request", err)
				}
				if !tt.invalid && classOf(err) != tt.class {
					t.Errorf("class = %q, want %q (%v)", classOf(err), tt.class, err)
				}
				if tt.detail != "" && !strings.Contains(err.Error(), tt.detail) {
					t.Errorf("err = %v, want the rights named", err)
				}
			})
		}
	}
}

func TestCheckArgumentsAreRefusedBeforeACredentialIsRead(t *testing.T) {
	c, base := serveChecks(t)
	reads := 0
	core := checksCore(t, base, &reads)
	for _, tt := range []struct{ operation, arguments string }{
		{checkRunsList.ID, `{"ref":"a..b"}`},
		{checkRunsList.ID, `{"ref":"main","status":"waiting"}`},
		{checkRunsList.ID, `{"ref":"main","filter":"newest"}`},
		{checkRunsList.ID, `{"ref":"main","app_id":0}`},
		{checkRunsList.ID, `{"ref":"main","check_name":"bad\u0000name"}`},
		{checkRunsList.ID, `{"ref":"main","limit":101}`},
		{checkRunsList.ID, `{"ref":"main","cursor":"AAAAAAAAAAAAAAAAY3VyLTM"}`},
		{checkRunsGet.ID, `{"check_run_id":0}`},
		{checkRunsGet.ID, `{"check_run_id":4,"ref":"main"}`},
		{checkAnnotationsList.ID, `{"check_run_id":-3}`},
		{checkSuitesList.ID, `{"ref":"main","status":"completed"}`},
		{checkSuitesList.ID, `{"ref":""}`},
		{checkSuiteRunsList.ID, `{"check_suite_id":5,"app_id":1}`},
		{checkSuiteRunsList.ID, `{"check_suite_id":0}`},
		// A repository outside the connection's targets.
		{checkRunsList.ID, `{"ref":"main","repository":"octo-org/other"}`},
		{checkRunsGet.ID, `{"check_run_id":4,"repository":"octo-org/other"}`},
		{checkAnnotationsList.ID, `{"check_run_id":4,"repository":"octo-org/other"}`},
		{checkSuitesList.ID, `{"ref":"main","repository":"octo-org/other"}`},
		{checkSuiteRunsList.ID, `{"check_suite_id":5,"repository":"octo-org/other"}`},
		// A project connection offers no repository tool.
	} {
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.operation, tt.arguments, err)
		}
	}
	if _, err := invoke(t, core, checkRunsGet.ID, "planning", `{"check_run_id":4}`, false); err == nil {
		t.Error("a check tool ran on a project connection")
	}
	if reads != 0 || len(c.f.recorded()) != 0 {
		t.Errorf("a refused request read %d credentials and sent %d requests", reads, len(c.f.recorded()))
	}
}

func TestExcludedRepositoriesAreRefusedBeforeACredentialIsRead(t *testing.T) {
	c, base := serveChecks(t)
	cfg := coreConfig(base)
	cfg.Connections["ex"] = config.Connection{Service: "gh", Credential: "gh-reader", Exclude: []string{"repos/octo-org/example"}}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red, &reads), red)
	for _, tt := range []struct{ operation, arguments string }{
		{checkRunsList.ID, `{"repository":"octo-org/example","ref":"main"}`},
		{checkRunsGet.ID, `{"repository":"octo-org/example","check_run_id":4}`},
		{checkAnnotationsList.ID, `{"repository":"octo-org/example","check_run_id":4}`},
		{checkSuitesList.ID, `{"repository":"octo-org/example","ref":"main"}`},
		{checkSuiteRunsList.ID, `{"repository":"octo-org/example","check_suite_id":5}`},
	} {
		if _, err := invoke(t, core, tt.operation, "ex", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.operation, err)
		}
	}
	if reads != 0 || len(c.f.recorded()) != 0 {
		t.Errorf("an excluded repository read %d credentials and sent %d requests", reads, len(c.f.recorded()))
	}
}

func TestCheckToolsAreGroupedWithThePullRequests(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{checkRunsList.ID, checkRunsGet.ID, checkAnnotationsList.ID, checkSuitesList.ID,
		checkSuiteRunsList.ID} {
		if group := groupOf(id); group != "pull-requests" {
			t.Errorf("%s group = %q, want pull-requests", id, group)
		}
	}
}
