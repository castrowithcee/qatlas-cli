package forge

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	actionsBase  = "/api/v1/repos/acme/widget/actions"
	forgejoRun   = `{"id":42,"title":"Fix build","status":"success","event":"push","trigger_event":"push","commit_sha":"abc1234","prettyref":"main","index_in_repo":5,"workflow_id":"build.yml","html_url":"https://forge.example.test/acme/widget/actions/runs/5","started":"2030-01-01T00:00:00Z","stopped":"2030-01-01T00:05:00Z","created":"2029-12-31T00:00:00Z","updated":"2030-01-01T00:06:00Z","duration":300000000000,"need_approval":false,"is_fork_pull_request":true,"ScheduleID":0,"approved_by":0,"is_ref_deleted":false,"event_payload":"{\"inputs\":\"payload-canary\"}","repository":{"id":9,"name":"widget","full_name":"acme/widget","owner":{"id":1,"login":"acme"}},"trigger_user":{"id":3,"login":"alice"}}`
	giteaRun     = `{"id":42,"display_title":"Fix build","status":"completed","conclusion":"success","event":"push","head_sha":"abc1234","head_branch":"main","run_number":5,"run_attempt":2,"path":"build.yml@refs/heads/main","started_at":"2030-01-01T00:00:00Z","completed_at":"2030-01-01T00:05:00Z","created_at":"2029-12-31T00:00:00Z","html_url":"https://forge.example.test/acme/widget/actions/runs/5","url":"https://forge.example.test/api/v1/repos/acme/widget/actions/runs/42","repository_id":9,"repository":{"id":9,"name":"widget","full_name":"acme/widget","owner":{"id":1,"login":"acme"}},"actor":{"id":3,"login":"alice"},"pull_requests":[{"id":1,"number":3,"url":"u","base":{"ref":"main","sha":"abc","repo":{"id":9,"name":"widget","url":"u"}}}]}`
	forgejoJob   = `{"id":7,"run_id":42,"name":"build","status":"success","attempt":1,"handle":"build","needs":[],"runs_on":["ubuntu"],"owner_id":1,"repo_id":9,"task_id":11,"html_url":"https://forge.example.test/acme/widget/actions/runs/5/jobs/0","steps":[{"number":0,"name":"Set up job","status":"success","started":"2030-01-01T00:00:00Z","stopped":"2030-01-01T00:00:01Z"}]}`
	giteaJob     = `{"id":7,"run_id":42,"name":"build","status":"completed","conclusion":"success","run_attempt":1,"head_sha":"abc1234","head_branch":"main","labels":["ubuntu"],"runner_id":2,"runner_name":"runner-1","url":"https://forge.example.test/api/v1/repos/acme/widget/actions/jobs/7","html_url":"https://forge.example.test/acme/widget/actions/runs/5/jobs/0","run_url":"https://forge.example.test/api/v1/repos/acme/widget/actions/runs/42","steps":[{"name":"checkout","number":1,"status":"completed","conclusion":"success"}]}`
	giteaForeign = `{"id":99,"display_title":"x","status":"completed","repository":{"id":10,"name":"foo","full_name":"other/foo","owner":{"id":2,"login":"other"}}}`
)

func runFixture(p *product) string {
	if p == gitea {
		return giteaRun
	}
	return forgejoRun
}

func jobFixture(p *product) string {
	if p == gitea {
		return giteaJob
	}
	return forgejoJob
}

// newActionsEnvironment answers the version endpoints like an instance of the product in the given version and
// every other path with api. It adds the connection actions-delete, which names the run deletion.
func newActionsEnvironment(t *testing.T, p *product, v string, api func(call) *http.Response) (*environment, assetDirs) {
	t.Helper()
	instanceHandler := giteaAt(v, api)
	if p == forgejo {
		instanceHandler = forgejoAt(v, api)
	}
	dirs := assetDirs{read: t.TempDir(), write: t.TempDir(), outside: t.TempDir()}
	calls := []call{}
	serve(t, &calls, instanceHandler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	cfg.Connections["files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Files: config.Files{Read: []string{dirs.read}, Write: []string{dirs.write}}}
	cfg.Connections["actions-delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".workflow-runs.delete", p.id + ".workflow-artifacts.delete"}}
	for name, connection := range cfg.Connections {
		connection.Effects = append(slices.Clone(connection.Effects), config.EffectExecute)
		cfg.Connections[name] = connection
	}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dirs
}

func defaultVersion(p *product) string {
	if p == forgejo {
		return "17.0.0"
	}
	return "28.2.0"
}

// apiCalls are the requests below the API, without the version reads.
func apiCalls(e *environment) []call {
	var out []call
	for _, c := range *e.calls {
		if strings.HasPrefix(c.path, "/api/v1/") && c.path != "/api/v1/version" {
			out = append(out, c)
		}
	}
	return out
}

func apiMethods(e *environment) []string {
	var out []string
	for _, c := range apiCalls(e) {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

func queryOf(t *testing.T, c call) url.Values {
	t.Helper()
	values, err := url.ParseQuery(c.query)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func rowsOf(t *testing.T, result map[string]any, key string) []map[string]any {
	t.Helper()
	list, _ := result[key].([]any)
	var out []map[string]any
	for _, row := range list {
		out = append(out, row.(map[string]any))
	}
	return out
}

func TestWorkflowRunsListAnswersBothShapes(t *testing.T) {
	for _, p := range products {
		wrapper := `{"total_count":1,"workflow_runs":[` + runFixture(p) + `]}`
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			return jsonResponse(200, wrapper, "Link", `<https://forge.example.test/next>; rel="next"`)
		})
		result, err := e.invoke(t, workflowRunsList, "repo", `{"limit":5}`)
		runs := rowsOf(t, result, "workflow_runs")
		if err != nil || len(runs) != 1 || result["total_count"] != float64(1) || result["has_more"] != true ||
			result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		run := runs[0]
		for key, want := range map[string]any{"id": float64(42), "title": "Fix build", "commit": "abc1234",
			"branch": "main", "run_number": float64(5), "started_at": "2030-01-01T00:00:00Z",
			"completed_at": "2030-01-01T00:05:00Z"} {
			if run[key] != want {
				t.Errorf("%s: %s = %v, want %v", p.id, key, run[key], want)
			}
		}
		if _, ok := run["event_payload"]; ok || strings.Contains(encode(t, run), "payload-canary") {
			t.Errorf("%s: event_payload is shown: %v", p.id, run)
		}
		native := map[string]string{forgejo.id: "need_approval", gitea.id: "run_attempt"}[p.id]
		foreign := map[string]string{forgejo.id: "run_attempt", gitea.id: "need_approval"}[p.id]
		if _, ok := run[native]; !ok {
			t.Errorf("%s: native member %s is missing: %v", p.id, native, run)
		}
		if _, ok := run[foreign]; ok {
			t.Errorf("%s: member %s of the other product: %v", p.id, foreign, run)
		}
		if got := apiMethods(e); !reflect.DeepEqual(got, []string{"GET " + actionsBase + "/runs"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		query := queryOf(t, apiCalls(e)[0])
		if query.Get("page") != "1" || query.Get("limit") != "5" {
			t.Errorf("%s query = %v", p.id, query)
		}
	}
}

func TestWorkflowRunsListFiltersBelongToTheirProduct(t *testing.T) {
	e, _ := newActionsEnvironment(t, forgejo, "17.0.0", func(call) *http.Response {
		return jsonResponse(200, `{"total_count":0,"workflow_runs":[]}`)
	})
	_, err := e.invoke(t, workflowRunsList, "repo", `{"event":["push","schedule"],"status":["failure"],"run_number":4,`+
		`"head_sha":"abc1234","ref":"refs/heads/main","workflow_id":"build.yml"}`)
	query := queryOf(t, apiCalls(e)[0])
	if err != nil || !reflect.DeepEqual(query["event"], []string{"push", "schedule"}) || query.Get("status") != "failure" ||
		query.Get("run_number") != "4" || query.Get("ref") != "refs/heads/main" || query.Get("workflow_id") != "build.yml" ||
		query.Get("head_sha") != "abc1234" {
		t.Fatalf("forgejo: %v, %v", query, err)
	}
	g, _ := newActionsEnvironment(t, gitea, "28.2.0", func(call) *http.Response {
		return jsonResponse(200, `{"total_count":0,"workflow_runs":[]}`)
	})
	_, err = g.invoke(t, workflowRunsList, "repo", `{"event":"push","status":"failure","branch":"main","actor":"alice",`+
		`"head_sha":"abc1234","exclude_pull_requests":true}`)
	query = queryOf(t, apiCalls(g)[0])
	if err != nil || query.Get("event") != "push" || query.Get("branch") != "main" || query.Get("actor") != "alice" ||
		query.Get("exclude_pull_requests") != "true" {
		t.Fatalf("gitea: %v, %v", query, err)
	}
	_, err = g.invoke(t, workflowRunsList, "repo", `{"workflow_id":"build.yml","scoped_workflow_source_repo_id":0}`)
	if got := apiMethods(g); err != nil || got[len(got)-1] != "GET "+actionsBase+"/workflows/build.yml/runs" {
		t.Errorf("gitea workflow path: %v, %v", got, err)
	}
	// A filter of the other product is refused before the secret and before any request.
	for _, tc := range []struct {
		p         *product
		arguments string
	}{
		{forgejo, `{"branch":"main"}`}, {forgejo, `{"actor":"alice"}`}, {forgejo, `{"exclude_pull_requests":true}`},
		{forgejo, `{"status":["bogus"]}`}, {forgejo, `{"owner":"orgs/acme"}`},
		{gitea, `{"ref":"refs/heads/main"}`}, {gitea, `{"run_number":3}`}, {gitea, `{"scoped_workflow_source_repo_id":1}`},
		{gitea, `{"workflow_id":"a/b.yml"}`},
	} {
		env, _ := newActionsEnvironment(t, tc.p, defaultVersion(tc.p), func(call) *http.Response { return notFoundResponse() })
		if _, err := env.invoke(t, workflowRunsList, "repo", tc.arguments); !isInvalidRequest(err) || *env.reads != 0 ||
			len(*env.calls) != 0 {
			t.Errorf("%s %s: %v, reads %d, calls %d", tc.p.id, tc.arguments, err, *env.reads, len(*env.calls))
		}
	}
}

func TestWorkflowRunsListOwnerLevelNeedsTheOwnerTarget(t *testing.T) {
	foreignOrg := `{"total_count":2,"workflow_runs":[` + giteaRun + `,` + giteaForeign + `]}`
	e, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		switch c.path {
		case "/api/v1/orgs/acme/actions/runs", "/api/v1/user/actions/runs":
			return jsonResponse(200, foreignOrg)
		case "/api/v1/user":
			return jsonResponse(200, `{"id":3,"login":"Alice"}`)
		}
		return notFoundResponse()
	})
	result, err := e.invoke(t, workflowRunsList, "org", `{"owner":"orgs/acme"}`)
	runs := rowsOf(t, result, "workflow_runs")
	if err != nil || len(runs) != 1 || runs[0]["id"] != float64(42) || result["owner"] != "orgs/acme" ||
		result["repository"] != nil {
		t.Fatalf("org: %v, %v", result, err)
	}
	if got := apiMethods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/orgs/acme/actions/runs"}) {
		t.Errorf("org calls = %v", got)
	}
	// A connection without targets reaches the owner level; Forgejo has none.
	if _, err := e.invoke(t, workflowRunsList, "open", `{"owner":"orgs/acme"}`); err != nil {
		t.Errorf("open connection: %v", err)
	}
	// A user is named by users/NAME and proven to be the account behind the token.
	e2, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		switch c.path {
		case "/api/v1/user/actions/runs":
			return jsonResponse(200, `{"total_count":1,"workflow_runs":[`+strings.ReplaceAll(giteaRun, "acme", "Alice")+`]}`)
		case "/api/v1/user":
			return jsonResponse(200, `{"id":3,"login":"Alice"}`)
		}
		return notFoundResponse()
	})
	result, err = e2.invoke(t, workflowRunsList, "owner", `{"owner":"users/alice"}`)
	if err != nil || len(rowsOf(t, result, "workflow_runs")) != 1 ||
		!reflect.DeepEqual(apiMethods(e2), []string{"GET /api/v1/user", "GET /api/v1/user/actions/runs"}) {
		t.Fatalf("user: %v, %v, %v", result, err, apiMethods(e2))
	}
	e3, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		if c.path == "/api/v1/user" {
			return jsonResponse(200, `{"id":4,"login":"bob"}`)
		}
		return notFoundResponse()
	})
	if _, err := e3.invoke(t, workflowRunsList, "owner", `{"owner":"users/alice"}`); !isInvalidRequest(err) ||
		!reflect.DeepEqual(apiMethods(e3), []string{"GET /api/v1/user"}) {
		t.Errorf("another account: %v, %v", err, apiMethods(e3))
	}
	// Without the matching owner target nothing is read.
	for _, tc := range []struct{ connection, arguments string }{
		{"repo", `{"owner":"orgs/acme"}`}, {"org", `{"owner":"orgs/other"}`}, {"org", `{"owner":"users/acme"}`},
		{"owner", `{"owner":"orgs/Alice"}`}, {"org", `{"owner":"orgs/acme","repository":"acme/widget"}`},
		{"org", `{"owner":"orgs/acme","workflow_id":"build.yml"}`}, {"org", `{"owner":"orgs/acme","exclude_pull_requests":true}`},
	} {
		env, _ := newActionsEnvironment(t, gitea, "28.2.0", func(call) *http.Response { return notFoundResponse() })
		if _, err := env.invoke(t, workflowRunsList, tc.connection, tc.arguments); !isInvalidRequest(err) ||
			*env.reads != 0 || len(*env.calls) != 0 {
			t.Errorf("%s %s: %v, reads %d, calls %d", tc.connection, tc.arguments, err, *env.reads, len(*env.calls))
		}
	}
	f, _ := newActionsEnvironment(t, forgejo, "17.0.0", func(call) *http.Response { return notFoundResponse() })
	if _, err := f.invoke(t, workflowRunsList, "org", `{"owner":"orgs/acme"}`); !isInvalidRequest(err) || *f.reads != 0 ||
		len(*f.calls) != 0 {
		t.Errorf("forgejo owner: %v, reads %d, calls %d", err, *f.reads, len(*f.calls))
	}
}

func TestWorkflowRunGetBindsTheIDAndTheRepository(t *testing.T) {
	for _, p := range products {
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			switch c.path {
			case actionsBase + "/runs/42":
				return jsonResponse(200, runFixture(p))
			case actionsBase + "/runs/43":
				return jsonResponse(200, runFixture(p))
			case actionsBase + "/runs/44":
				return jsonResponse(200, strings.ReplaceAll(runFixture(p), "acme/widget", "other/foo"))
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, workflowRunsGet, "repo", `{"run_id":42}`)
		if err != nil || result["id"] != float64(42) || result["title"] != "Fix build" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if _, err := e.invoke(t, workflowRunsGet, "repo", `{"run_id":43}`); classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s: another ID: %v", p.id, err)
		}
		if _, err := e.invoke(t, workflowRunsGet, "repo", `{"run_id":44}`); classOfError(err) != "invalid-provider-response" ||
			strings.Contains(err.Error(), "other/foo") {
			t.Errorf("%s: a run of another repository: %v", p.id, err)
		}
		reads := *e.reads
		if _, err := e.invoke(t, workflowRunsGet, "repo", `{"run_id":42,"repository":"acme/other"}`); !isInvalidRequest(err) ||
			*e.reads != reads {
			t.Errorf("%s: repository outside the targets: %v", p.id, err)
		}
		if _, err := e.invoke(t, workflowRunsGet, "repo", `{"run_id":0}`); !isInvalidRequest(err) {
			t.Errorf("%s: run_id 0: %v", p.id, err)
		}
	}
}

func TestWorkflowRunAttemptsAreGiteaOnly(t *testing.T) {
	reg := registry(t)
	for _, only := range []*tool{workflowRunAttemptsGet, workflowRunAttemptsListJobs, workflowRunsForceCancel,
		workflowRunsRerunFailed, workflowRunsApprove} {
		if _, _, ok := reg.Lookup(forgejo.toolID(only)); ok {
			t.Errorf("%s is registered for Forgejo", forgejo.toolID(only))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(only)); !ok {
			t.Errorf("%s is missing", gitea.toolID(only))
		}
	}
	e, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		switch c.path {
		case actionsBase + "/runs/42/attempts/2":
			return jsonResponse(200, giteaRun)
		case actionsBase + "/runs/42/attempts/2/jobs":
			return jsonResponse(200, `{"total_count":1,"jobs":[`+giteaJob+`]}`)
		}
		return notFoundResponse()
	})
	result, err := e.invoke(t, workflowRunAttemptsGet, "repo", `{"run_id":42,"attempt":2}`)
	if err != nil || result["run_attempt"] != float64(2) {
		t.Fatalf("attempt: %v, %v", result, err)
	}
	result, err = e.invoke(t, workflowRunAttemptsListJobs, "repo", `{"run_id":42,"attempt":2,"status":"success"}`)
	jobs := rowsOf(t, result, "jobs")
	if err != nil || len(jobs) != 1 || jobs[0]["attempt"] != float64(1) || result["total_count"] != float64(1) ||
		queryOf(t, apiCalls(e)[1]).Get("status") != "success" {
		t.Fatalf("attempt jobs: %v, %v", result, err)
	}
}

func TestWorkflowJobsListAnswersBothShapes(t *testing.T) {
	f, _ := newActionsEnvironment(t, forgejo, "17.0.0", func(c call) *http.Response {
		if c.path == actionsBase+"/runs/42/jobs" {
			return jsonResponse(200, `[`+forgejoJob+`]`)
		}
		return notFoundResponse()
	})
	result, err := f.invoke(t, workflowJobsList, "repo", `{"run_id":42}`)
	jobs := rowsOf(t, result, "jobs")
	if err != nil || len(jobs) != 1 || result["has_more"] != false || result["total_count"] != nil ||
		jobs[0]["handle"] != "build" || jobs[0]["attempt"] != float64(1) || jobs[0]["task_id"] != float64(11) ||
		jobs[0]["conclusion"] != nil {
		t.Fatalf("forgejo: %v, %v", result, err)
	}
	for _, arguments := range []string{`{}`, `{"run_id":42,"status":"success"}`, `{"run_id":42,"limit":5}`,
		`{"run_id":42,"owner":"orgs/acme"}`} {
		if _, err := f.invoke(t, workflowJobsList, "repo", arguments); !isInvalidRequest(err) {
			t.Errorf("forgejo %s: %v", arguments, err)
		}
	}
	g, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		switch c.path {
		case actionsBase + "/jobs", actionsBase + "/runs/42/jobs", "/api/v1/orgs/acme/actions/jobs":
			return jsonResponse(200, `{"total_count":2,"jobs":[`+giteaJob+`,`+
				strings.ReplaceAll(strings.ReplaceAll(giteaJob, "acme/widget", "other/foo"), `"id":7`, `"id":8`)+`]}`)
		}
		return notFoundResponse()
	})
	result, err = g.invoke(t, workflowJobsList, "repo", `{"status":"failure","sort":"id","order":"desc"}`)
	query := queryOf(t, apiCalls(g)[0])
	if err != nil || len(rowsOf(t, result, "jobs")) != 1 || result["total_count"] != float64(2) ||
		query.Get("status") != "failure" || query.Get("order") != "desc" {
		t.Fatalf("gitea repository: %v, %v, %v", result, err, query)
	}
	if _, err = g.invoke(t, workflowJobsList, "repo", `{"run_id":42}`); err != nil {
		t.Errorf("gitea run: %v", err)
	}
	// The organization level keeps only the rows of that organization; it has no sort.
	g2, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		return jsonResponse(200, `{"total_count":2,"jobs":[`+giteaJob+`,`+
			strings.ReplaceAll(strings.ReplaceAll(giteaJob, "acme/widget", "other/foo"), `"id":7`, `"id":8`)+`]}`)
	})
	result, err = g2.invoke(t, workflowJobsList, "org", `{"owner":"orgs/acme"}`)
	if err != nil || len(rowsOf(t, result, "jobs")) != 1 || result["owner"] != "orgs/acme" {
		t.Fatalf("gitea org: %v, %v", result, err)
	}
	for _, arguments := range []string{`{"owner":"orgs/acme","sort":"id"}`, `{"owner":"orgs/acme","run_id":42}`} {
		if _, err := g2.invoke(t, workflowJobsList, "org", arguments); !isInvalidRequest(err) {
			t.Errorf("gitea %s: %v", arguments, err)
		}
	}
}

func TestWorkflowJobGetBindsTheID(t *testing.T) {
	for _, p := range products {
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			switch c.path {
			case actionsBase + "/jobs/7", actionsBase + "/jobs/8":
				return jsonResponse(200, jobFixture(p))
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, workflowJobsGet, "repo", `{"job_id":7}`)
		steps, _ := result["steps"].([]any)
		if err != nil || result["id"] != float64(7) || len(steps) != 1 || result["run_id"] != float64(42) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if _, err := e.invoke(t, workflowJobsGet, "repo", `{"job_id":8}`); classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s: another ID: %v", p.id, err)
		}
	}
}

func TestActionsTasksListHasTheForgejoStatusFilterOnly(t *testing.T) {
	for _, p := range products {
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `{"total_count":1,"workflow_runs":[{"id":5,"name":"build","display_title":"t",`+
				`"status":"success","head_sha":"abc","run_number":3,"workflow_id":"build.yml"}]}`)
		})
		result, err := e.invoke(t, actionsTasksList, "repo", `{}`)
		tasks := rowsOf(t, result, "tasks")
		if err != nil || len(tasks) != 1 || tasks[0]["workflow_id"] != "build.yml" || result["total_count"] != float64(1) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		_, err = e.invoke(t, actionsTasksList, "repo", `{"status":["failure"]}`)
		if p == forgejo && (err != nil || queryOf(t, apiCalls(e)[1]).Get("status") != "failure") {
			t.Errorf("forgejo status: %v", err)
		}
		if p == gitea && !isInvalidRequest(err) {
			t.Errorf("gitea status: %v", err)
		}
	}
}

// The operations of each tool exist from the versions of the API descriptions on; an older instance is refused
// before any request below the API.
func TestVersionsOfTheActionsTools(t *testing.T) {
	type instanceVersion struct {
		p *product
		v string
	}
	instances := []instanceVersion{{forgejo, "15.0.10"}, {forgejo, "16.0.6"}, {forgejo, "17.0.0"},
		{gitea, "1.27.3"}, {gitea, "28.2.0"}}
	// available lists the instances that have the tool: the first Forgejo and Gitea version.
	table := []struct {
		tool      *tool
		forgejo   string
		gitea     string
		arguments func(p *product) string
	}{
		{workflowRunsList, "15.0.10", "1.27.3", nil}, {workflowRunsGet, "15.0.10", "1.27.3", nil},
		{actionsTasksList, "15.0.10", "1.27.3", nil},
		{workflowRunsDelete, "16.0.6", "1.27.3", nil}, {workflowRunsCancel, "16.0.6", "28.2.0", nil},
		{workflowRunsForceCancel, "", "28.2.0", nil}, {workflowRunsApprove, "", "28.2.0", nil},
		{workflowRunsRerun, "17.0.0", "1.27.3", nil}, {workflowRunsRerunFailed, "", "1.27.3", nil},
		{workflowRunAttemptsGet, "", "1.27.3", nil}, {workflowRunAttemptsListJobs, "", "1.27.3", nil},
		{workflowJobsList, "16.0.6", "1.27.3", nil}, {workflowJobsGet, "17.0.0", "1.27.3", nil},
		{workflowJobsRerun, "17.0.0", "1.27.3", nil}, {workflowJobLogsGet, "16.0.6", "1.27.3", nil},
	}
	for _, tc := range table {
		for _, in := range instances {
			wantFrom := map[string]string{forgejo.id: tc.forgejo, gitea.id: tc.gitea}[in.p.id]
			if wantFrom == "" {
				continue
			}
			available := !parseOrZero(in.v).less(parseOrZero(wantFrom))
			e, _ := newActionsEnvironment(t, in.p, in.v, func(call) *http.Response { return jsonResponse(200, `{}`) })
			arguments := `{"run_id":42}`
			switch tc.tool {
			case workflowRunsList, workflowRunsGet, actionsTasksList:
				arguments = `{}`
				if tc.tool == workflowRunsGet {
					arguments = `{"run_id":42}`
				}
			case workflowJobsGet, workflowJobLogsGet:
				arguments = `{"job_id":7}`
			case workflowJobsRerun:
				arguments = `{"job_id":7}`
				if in.p == gitea {
					arguments = `{"run_id":42,"job_id":7}`
				}
			case workflowRunAttemptsGet, workflowRunAttemptsListJobs:
				arguments = `{"run_id":42,"attempt":1}`
			}
			connection := "repo"
			if tc.tool == workflowRunsDelete {
				connection = "actions-delete"
			}
			_, err := e.invoke(t, tc.tool, connection, arguments)
			refused := err != nil && strings.Contains(err.Error(), "not available")
			if refused == available || (refused && len(apiCalls(e)) != 0) {
				t.Errorf("%s %s on %s: available %t, err %v, calls %v", in.p.id, tc.tool.action, in.v, available, err,
					apiMethods(e))
			}
			if refused && !strings.Contains(err.Error(), in.p.name+" "+parseOrZero(wantFrom).String()[:2]) &&
				!strings.Contains(err.Error(), in.p.name+" 1.27") && !strings.Contains(err.Error(), in.p.name+" 28") {
				t.Errorf("%s %s: message %q does not name the version", in.p.id, tc.tool.action, err)
			}
		}
	}
	// The run logs need Forgejo 16 and Gitea 28.
	for _, in := range instances {
		e, dirs := newActionsEnvironment(t, in.p, in.v, func(call) *http.Response { return jsonResponse(200, `{}`) })
		available := (in.p == forgejo && in.v != "15.0.10") || (in.p == gitea && in.v == "28.2.0")
		_, err := e.invoke(t, workflowRunLogsGet, "files", `{"run_id":42,"local_path":`+quote(dirs.write+"/logs.zip")+`}`)
		refused := err != nil && strings.Contains(err.Error(), "not available")
		if refused == available || (refused && len(apiCalls(e)) != 0) {
			t.Errorf("%s run logs on %s: available %t, err %v", in.p.id, in.v, available, err)
		}
	}
	// The Forgejo 17 log filters and the Forgejo 16 attempt filter.
	for _, tc := range []struct {
		v         string
		arguments string
		refused   bool
	}{
		{"16.0.6", `{"job_id":7,"attempt":2}`, false}, {"16.0.6", `{"job_id":7,"step":1}`, true},
		{"16.0.6", `{"job_id":7,"q":"x"}`, true}, {"16.0.6", `{"job_id":7,"format":"ndjson"}`, true},
		{"17.0.0", `{"job_id":7,"step":1,"q":"x","qi":true,"format":"ndjson"}`, false},
	} {
		e, _ := newActionsEnvironment(t, forgejo, tc.v, func(call) *http.Response { return jsonResponse(200, "line") })
		_, err := e.invoke(t, workflowJobLogsGet, "repo", tc.arguments)
		refused := err != nil && strings.Contains(err.Error(), "not available") && strings.Contains(err.Error(), "17.0.0")
		if refused != tc.refused || (refused && len(apiCalls(e)) != 0) || (!refused && err != nil) {
			t.Errorf("forgejo %s %s: refused %t, %v", tc.v, tc.arguments, refused, err)
		}
	}
}

func parseOrZero(raw string) version {
	v, _ := parseVersion(raw)
	return v
}

func TestRunAndJobChangesSendOneRequestAfterConfirmation(t *testing.T) {
	type change struct {
		tool      *tool
		arguments string
		method    string
		path      func(p *product) string
		onlyGitea bool
		answer    func(p *product) string
	}
	runs := actionsBase + "/runs/42"
	cases := []change{
		{workflowRunsCancel, `{"run_id":42}`, "POST", func(*product) string { return runs + "/cancel" }, false,
			func(p *product) string { return map[bool]string{true: giteaRun}[p == gitea] }},
		{workflowRunsForceCancel, `{"run_id":42}`, "POST", func(*product) string { return runs + "/force-cancel" }, true,
			func(*product) string { return giteaRun }},
		{workflowRunsRerun, `{"run_id":42}`, "POST", func(*product) string { return runs + "/rerun" }, false,
			func(p *product) string { return map[bool]string{true: giteaRun}[p == gitea] }},
		{workflowRunsRerunFailed, `{"run_id":42}`, "POST", func(*product) string { return runs + "/rerun-failed-jobs" }, true,
			func(*product) string { return "" }},
		{workflowRunsApprove, `{"run_id":42}`, "POST", func(*product) string { return runs + "/approve" }, true,
			func(*product) string { return giteaRun }},
		{workflowJobsRerun, `{"job_id":7}`, "POST", func(p *product) string {
			if p == gitea {
				return runs + "/jobs/7/rerun"
			}
			return actionsBase + "/jobs/7/rerun"
		}, false, func(p *product) string { return map[bool]string{true: giteaJob}[p == gitea] }},
	}
	for _, tc := range cases {
		for _, p := range products {
			if tc.onlyGitea && p == forgejo {
				continue
			}
			arguments := tc.arguments
			if tc.tool == workflowJobsRerun && p == gitea {
				arguments = `{"run_id":42,"job_id":7}`
			}
			status := 200
			if p == forgejo || tc.tool == workflowRunsRerun || tc.tool == workflowRunsRerunFailed || tc.tool == workflowJobsRerun {
				status = 204
				if p == gitea {
					status = 201
				}
			}
			answer := tc.answer(p)
			e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
				if answer == "" || status == 204 {
					return &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}
				}
				return jsonResponse(status, answer)
			})
			d, _, _ := registry(t).Lookup(p.toolID(tc.tool))
			if d.Risk.Effect != "execute" || d.Risk.Confirmation != "required" || d.RequiresToolAllowList {
				t.Errorf("%s risk = %+v", d.ID, d)
			}
			var confirm *application.ConfirmationRequiredError
			if _, err := e.invokeAs(t, tc.tool, "repo", arguments, false); !errors.As(err, &confirm) || len(*e.calls) != 0 ||
				*e.reads != 0 {
				t.Errorf("%s: unconfirmed: %v, calls %d", d.ID, err, len(*e.calls))
			}
			result, err := e.invoke(t, tc.tool, "repo", arguments)
			if err != nil || result["accepted"] != true {
				t.Fatalf("%s: %v, %v", d.ID, result, err)
			}
			if got := apiMethods(e); !reflect.DeepEqual(got, []string{tc.method + " " + tc.path(p)}) {
				t.Errorf("%s calls = %v", d.ID, got)
			}
			_, hasRun := result["run"]
			_, hasJob := result["job"]
			if (hasRun || hasJob) != (answer != "" && p == gitea) {
				t.Errorf("%s: answer members %v", d.ID, result)
			}
			// An outcome that stays open is reported and the request is not repeated.
			for _, failure := range []int{500, 503} {
				f, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
					return jsonResponse(failure, `{"message":"`+bodyCanary+`"}`)
				})
				if _, err := f.invoke(t, tc.tool, "repo", arguments); err == nil ||
					!strings.Contains(err.Error(), "may have been applied") || len(apiCalls(f)) != 1 {
					t.Errorf("%s %d: %v, calls %v", d.ID, failure, err, apiMethods(f))
				}
			}
		}
	}
	// Forgejo names a job only, Gitea its run as well.
	f, _ := newActionsEnvironment(t, forgejo, "17.0.0", func(call) *http.Response { return notFoundResponse() })
	g, _ := newActionsEnvironment(t, gitea, "28.2.0", func(call) *http.Response { return notFoundResponse() })
	if _, err := f.invoke(t, workflowJobsRerun, "repo", `{"run_id":42,"job_id":7}`); !isInvalidRequest(err) || *f.reads != 0 {
		t.Errorf("forgejo run_id: %v", err)
	}
	if _, err := g.invoke(t, workflowJobsRerun, "repo", `{"job_id":7}`); !isInvalidRequest(err) || *g.reads != 0 {
		t.Errorf("gitea without run_id: %v", err)
	}
	if _, err := g.invoke(t, workflowRunsCancel, "repo", `{"run_id":42,"repository":"acme/other"}`); !isInvalidRequest(err) ||
		*g.reads != 0 {
		t.Errorf("repository outside the targets: %v", err)
	}
}

func TestWorkflowRunDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		d, _, ok := reg.Lookup(p.toolID(workflowRunsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
			}
		}
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		if _, err := e.invoke(t, workflowRunsDelete, "repo", `{"run_id":42}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, workflowRunsDelete, "actions-delete", `{"run_id":42}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, workflowRunsDelete, "actions-delete", `{"run_id":42}`)
		if err != nil || result["deleted"] != true || !reflect.DeepEqual(apiMethods(e), []string{"DELETE " + actionsBase + "/runs/42"}) {
			t.Errorf("%s: %v, %v, %v", p.id, result, err, apiMethods(e))
		}
	}
}

func TestExclusionsKeepExcludedRunsAndOwnersOut(t *testing.T) {
	foreignOrg := `{"total_count":2,"workflow_runs":[` + giteaRun + `,` + giteaForeign + `]}`
	e, _ := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
		switch c.path {
		case "/api/v1/orgs/acme/actions/runs":
			return jsonResponse(200, foreignOrg)
		case actionsBase + "/runs/42":
			return jsonResponse(200, giteaRun)
		case actionsBase + "/runs/44":
			return jsonResponse(200, strings.Replace(strings.ReplaceAll(giteaRun, "acme/widget", "Other/foo"), `{"id":42,`, `{"id":44,`, 1))
		}
		return notFoundResponse()
	})
	// The owner level drops the run of the excluded owner and a row that names no repository.
	result, err := e.invoke(t, workflowRunsList, "exclude", `{"owner":"orgs/acme"}`)
	if runs := rowsOf(t, result, "workflow_runs"); err != nil || len(runs) != 1 || runs[0]["id"] != float64(42) {
		t.Fatalf("owner level: %v, %v", result, err)
	}
	// An excluded owner or repository is refused before any secret or request.
	reads := *e.reads
	for _, tc := range []struct {
		tl                    *tool
		connection, arguments string
	}{
		{workflowRunsList, "exclude", `{"owner":"orgs/other"}`},
		{workflowRunsList, "exclude", `{"owner":"users/OTHER"}`},
		{workflowRunsList, "owner-exclude", `{"repository":"acme/gadget"}`},
		{workflowRunsGet, "exclude", `{"repository":"acme/gadget","run_id":42}`},
	} {
		*e.calls = (*e.calls)[:0]
		if _, err := e.invoke(t, tc.tl, tc.connection, tc.arguments); !isInvalidRequest(err) || len(*e.calls) != 0 ||
			*e.reads != reads || strings.Contains(err.Error(), "gadget") {
			t.Errorf("%s %s: %v, calls %v", tc.tl.action, tc.arguments, err, e.paths())
		}
	}
	// A run whose canonical repository lies in an exclusion reads as not found, whatever the path said.
	if _, err := e.invoke(t, workflowRunsGet, "exclude", `{"repository":"acme/widget","run_id":42}`); err != nil {
		t.Errorf("reachable run: %v", err)
	}
	_, err = e.invoke(t, workflowRunsGet, "exclude", `{"repository":"acme/widget","run_id":44}`)
	if classOfError(err) != "not-found" || strings.Contains(err.Error(), "foo") {
		t.Errorf("run of an excluded repository: %v", err)
	}
}
