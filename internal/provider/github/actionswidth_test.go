package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const widthHeadSHA = "009b8a3a9ccbb128af87f9b1c0f4c62e8a304f6d"

const widthAPI = "https://api.github.com/repos/octo-org/example/"

// widthAccount is a simple-user as the API documentation shows it.
const widthAccount = `{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","avatar_url":"https://github.com/images/error/octocat_happy.gif",` +
	`"gravatar_id":"","url":"https://api.github.com/users/octocat","html_url":"https://github.com/octocat",` +
	`"type":"User","site_admin":false}`

// widthRun is a workflow run with every member the API documentation lists, a fork as head repository, and a
// logs_url that must never reach an answer.
const widthRun = `{"id":30433642,"name":"Build","node_id":"MDEyOldvcmtmbG93IFJ1bjI2OTI4OQ==","check_suite_id":42,` +
	`"check_suite_node_id":"MDEwOkNoZWNrU3VpdGU0Mg==","head_branch":"main","head_sha":"` + widthHeadSHA + `",` +
	`"path":".github/workflows/build.yml@main","run_number":562,"event":"push","display_title":"Update README.md",` +
	`"status":"queued","conclusion":null,"workflow_id":159038,` +
	`"url":"` + widthAPI + `actions/runs/30433642","html_url":"https://github.com/octo-org/example/actions/runs/30433642",` +
	`"pull_requests":[{"id":1934,"number":3456,"url":"` + widthAPI + `pulls/3456",` +
	`"head":{"ref":"feature","sha":"` + widthHeadSHA + `","repo":{"id":1296269,"url":"` + widthAPI + `","name":"example"}},` +
	`"base":{"ref":"main","sha":"d2a7a1b0","repo":{"id":1296269,"url":"` + widthAPI + `","name":"example"}}}],` +
	`"created_at":"2020-01-22T19:33:08Z","updated_at":"2020-01-22T19:33:08Z","actor":` + widthAccount + `,` +
	`"triggering_actor":{"login":"hubot","id":2,"type":"Bot","url":"https://api.github.com/users/hubot"},` +
	`"run_attempt":1,"referenced_workflows":[{"path":"octocat/Hello-World/.github/workflows/deploy.yml@main",` +
	`"sha":"86e8bc9","ref":"refs/heads/main"}],"run_started_at":"2020-01-22T19:33:08Z",` +
	`"jobs_url":"` + widthAPI + `actions/runs/30433642/jobs","logs_url":"` + widthAPI + `actions/runs/30433642/logs",` +
	`"check_suite_url":"` + widthAPI + `check-suites/42","artifacts_url":"` + widthAPI + `actions/runs/30433642/artifacts",` +
	`"cancel_url":"` + widthAPI + `actions/runs/30433642/cancel","rerun_url":"` + widthAPI + `actions/runs/30433642/rerun",` +
	`"previous_attempt_url":null,"workflow_url":"` + widthAPI + `actions/workflows/159038",` +
	`"head_commit":{"id":"` + widthHeadSHA + `","tree_id":"d23f6eedb1e1b9610bbc754ddb5197bfe7271223",` +
	`"message":"Create linter.yaml\n\nbody","timestamp":"2020-01-22T19:33:05Z",` +
	`"author":{"name":"Monalisa Octocat","email":"monalisa@example.invalid"},` +
	`"committer":{"name":"GitHub","email":"noreply@example.invalid"}},` +
	`"repository":{"id":1296269,"full_name":"octo-org/example"},` +
	`"head_repository":{"id":217723378,"node_id":"R_kgDOA","name":"example","full_name":"fork-owner/example",` +
	`"private":false,"description":"fork description","html_url":"https://github.com/fork-owner/example",` +
	`"url":"https://api.github.com/repos/fork-owner/example","owner":` + widthAccount + `},` +
	`"head_repository_id":217723378}`

const widthWorkflow = `{"id":161335,"node_id":"MDg6V29ya2Zsb3cxNjEzMzU=","name":"CI","path":".github/workflows/blank.yaml",` +
	`"state":"active","created_at":"2020-01-08T23:48:37.000-08:00","updated_at":"2020-01-08T23:50:21.000-08:00",` +
	`"deleted_at":"2020-02-08T23:50:21.000-08:00","url":"` + widthAPI + `actions/workflows/161335",` +
	`"html_url":"https://github.com/octo-org/example/blob/master/.github/workflows/161335",` +
	`"badge_url":"https://github.com/octo-org/example/workflows/CI/badge.svg"}`

func widthJob(labels string) string {
	return `{"id":399444496,"run_id":29679449,"run_url":"` + widthAPI + `actions/runs/29679449","node_id":"MDg6Q2hlY2tSdW4zOTk0NDQ0OTY=",` +
		`"head_sha":"f83a356604ae3c5d03e1b46ef4d1ca77d64a90b0","url":"` + widthAPI + `actions/jobs/399444496",` +
		`"html_url":"https://github.com/octo-org/example/actions/runs/29679449/job/399444496","status":"completed",` +
		`"conclusion":"success","created_at":"2020-01-20T17:40:18Z","started_at":"2020-01-20T17:42:40Z",` +
		`"completed_at":"2020-01-20T17:44:39Z","name":"build","run_attempt":1,` +
		`"steps":[{"name":"Set up job","status":"completed","conclusion":"success","number":1,` +
		`"started_at":"2020-01-20T09:42:40.000-08:00","completed_at":"2020-01-20T09:42:41.000-08:00"}],` +
		`"check_run_url":"` + widthAPI + `check-runs/399444496","labels":` + labels + `,"runner_id":1,` +
		`"runner_name":"my runner","runner_group_id":2,"runner_group_name":"my runner group",` +
		`"workflow_name":"CI","head_branch":"main"}`
}

const widthArtifact = `{"id":11,"node_id":"MDg6QXJ0aWZhY3QxMQ==","name":"Rails","size_in_bytes":556,` +
	`"url":"` + widthAPI + `actions/artifacts/11","archive_download_url":"` + widthAPI + `actions/artifacts/11/zip",` +
	`"expired":false,"created_at":"2020-01-10T14:59:22Z","expires_at":"2020-03-21T14:59:22Z",` +
	`"updated_at":"2020-02-21T14:59:22Z","digest":"sha256:abc","workflow_run":{"id":2332938,` +
	`"repository_id":1296269,"head_repository_id":1296269,"head_branch":"main","head_sha":"` + widthHeadSHA + `"}}`

// widthRoutes answers the Actions reads with the documented example objects.
func widthRoutes(labels string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
		if !ok {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		switch rest {
		case "actions/workflows":
			fmt.Fprintf(w, `{"total_count":1,"workflows":[%s]}`, widthWorkflow)
		case "actions/workflows/161335":
			fmt.Fprint(w, widthWorkflow)
		case "actions/runs":
			fmt.Fprintf(w, `{"total_count":120,"workflow_runs":[%s]}`, widthRun)
		case "actions/runs/30433642":
			fmt.Fprint(w, widthRun)
		case "actions/runs/30433642/jobs":
			fmt.Fprintf(w, `{"total_count":1,"jobs":[%s]}`, widthJob(labels))
		case "actions/jobs/399444496":
			fmt.Fprint(w, widthJob(labels))
		case "actions/runs/30433642/artifacts":
			fmt.Fprintf(w, `{"total_count":120,"artifacts":[%s]}`, widthArtifact)
		case "actions/permissions":
			fmt.Fprint(w, `{"enabled":true,"allowed_actions":"selected","sha_pinning_required":true,`+
				`"selected_actions_url":"`+widthAPI+`actions/permissions/selected-actions"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return true
	}
}

func widthCore(t *testing.T, routes func(http.ResponseWriter, *http.Request) bool) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{failure: routes}
	base := serve(t, f)
	red := &redact.Redactor{}
	reads := 0
	return f, application.New(registry(t), maintainConfig(base), resolver(red, &reads), red), &reads
}

func widthDecoded(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func widthAt(t *testing.T, value any, path ...string) any {
	t.Helper()
	for _, step := range path {
		switch v := value.(type) {
		case map[string]any:
			value = v[step]
		case []any:
			var i int
			fmt.Sscanf(step, "%d", &i)
			value = v[i]
		default:
			t.Fatalf("no %q below %v", step, value)
		}
	}
	return value
}

func widthExpect(t *testing.T, value any, want map[string]any) {
	t.Helper()
	for path, expected := range want {
		if got := widthAt(t, value, strings.Split(path, ".")...); !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", path, got, expected)
		}
	}
}

func TestWorkflowWidth(t *testing.T) {
	_, core, _ := widthCore(t, widthRoutes("[]"))
	want := map[string]any{
		"node_id": "MDg6V29ya2Zsb3cxNjEzMzU=", "created_at": "2020-01-08T23:48:37.000-08:00",
		"updated_at": "2020-01-08T23:50:21.000-08:00", "deleted_at": "2020-02-08T23:50:21.000-08:00",
		"url":             "https://github.com/octo-org/example/blob/master/.github/workflows/161335",
		"links.url":       widthAPI + "actions/workflows/161335",
		"links.badge_url": "https://github.com/octo-org/example/workflows/CI/badge.svg",
	}
	result, err := invoke(t, core, "github.workflows.get", "observer", `{"workflow":"161335"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	widthExpect(t, widthDecoded(t, result), want)
	result, err = invoke(t, core, "github.workflows.list", "observer", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	list := map[string]any{}
	for path, value := range want {
		list["workflows.0."+path] = value
	}
	widthExpect(t, widthDecoded(t, result), list)
}

func TestRunWidth(t *testing.T) {
	f, core, _ := widthCore(t, widthRoutes("[]"))
	result, err := invoke(t, core, "github.workflow-runs.get", "observer", `{"run_id":30433642}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"logs_url", "/logs", "temp_clone_token", `"repository":{`} {
		if strings.Contains(string(result), forbidden) {
			t.Errorf("a run carries %q: %s", forbidden, result)
		}
	}
	run := widthDecoded(t, result)
	widthExpect(t, run, map[string]any{
		"actor": "octocat", "actor_detail.login": "octocat", "actor_detail.id": float64(1),
		"actor_detail.url": "https://github.com/octocat", "actor_detail.links.url": "https://api.github.com/users/octocat",
		"triggering_actor.login": "hubot", "triggering_actor.type": "Bot",
		"url": "https://github.com/octo-org/example/actions/runs/30433642", "node_id": "MDEyOldvcmtmbG93IFJ1bjI2OTI4OQ==",
		"check_suite_id": float64(42), "check_suite_node_id": "MDEwOkNoZWNrU3VpdGU0Mg==",
		"path":                                ".github/workflows/build.yml@main",
		"referenced_workflows.0.path":         "octocat/Hello-World/.github/workflows/deploy.yml@main",
		"referenced_workflows.0.sha":          "86e8bc9",
		"referenced_workflows.0.ref":          "refs/heads/main",
		"head_commit.id":                      widthHeadSHA,
		"head_commit.tree_id":                 "d23f6eedb1e1b9610bbc754ddb5197bfe7271223",
		"head_commit.message":                 "Create linter.yaml\n\nbody",
		"head_commit.timestamp":               "2020-01-22T19:33:05Z",
		"head_commit.author.name":             "Monalisa Octocat",
		"head_commit.author.email":            "monalisa@example.invalid",
		"head_commit.committer.name":          "GitHub",
		"head_commit.committer.email":         "noreply@example.invalid",
		"head_repository_id":                  float64(217723378),
		"pull_requests.0.id":                  float64(1934),
		"pull_requests.0.number":              float64(3456),
		"pull_requests.0.links.url":           widthAPI + "pulls/3456",
		"pull_requests.0.head.ref":            "feature",
		"pull_requests.0.head.sha":            widthHeadSHA,
		"pull_requests.0.head.repo.id":        float64(1296269),
		"pull_requests.0.head.repo.name":      "example",
		"pull_requests.0.head.repo.links.url": widthAPI,
		"pull_requests.0.base.ref":            "main",
		"pull_requests.0.base.repo.name":      "example",
		"links.url":                           widthAPI + "actions/runs/30433642",
		"links.jobs_url":                      widthAPI + "actions/runs/30433642/jobs",
		"links.check_suite_url":               widthAPI + "check-suites/42",
		"links.artifacts_url":                 widthAPI + "actions/runs/30433642/artifacts",
		"links.cancel_url":                    widthAPI + "actions/runs/30433642/cancel",
		"links.rerun_url":                     widthAPI + "actions/runs/30433642/rerun",
		"links.workflow_url":                  widthAPI + "actions/workflows/159038",
	})
	if _, ok := widthAt(t, run, "links").(map[string]any)["previous_attempt_url"]; ok {
		t.Error("a null previous_attempt_url is answered")
	}
	// A fork outside the targets shows only identifiers.
	head := widthAt(t, run, "head_repository").(map[string]any)
	if !reflect.DeepEqual(head, map[string]any{"id": float64(217723378), "node_id": "R_kgDOA", "full_name": "fork-owner/example"}) {
		t.Errorf("head_repository = %v, want identifiers only", head)
	}

	// The list carries the same members, the address included; pull requests only on request.
	for _, include := range []bool{false, true} {
		before := len(f.recorded())
		arguments := fmt.Sprintf(`{"include_pull_requests":%t,"check_suite_id":42,"head_sha":%q}`, include,
			strings.ToUpper(widthHeadSHA))
		result, err = invoke(t, core, "github.workflow-runs.list", "observer", arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		query, _ := url.ParseQuery(f.recorded()[before].query)
		wantExclude := map[bool]string{false: "true", true: "false"}[include]
		if query.Get("exclude_pull_requests") != wantExclude || query.Get("check_suite_id") != "42" ||
			query.Get("head_sha") != widthHeadSHA {
			t.Errorf("query = %v, want exclude_pull_requests=%s, check_suite_id=42, head_sha", query, wantExclude)
		}
		entry := widthAt(t, widthDecoded(t, result), "runs", "0").(map[string]any)
		if _, has := entry["pull_requests"]; has != include {
			t.Errorf("include_pull_requests %t: pull_requests present = %t", include, has)
		}
		widthExpect(t, entry, map[string]any{"url": "https://github.com/octo-org/example/actions/runs/30433642",
			"head_commit.message": "Create linter.yaml\n\nbody", "check_suite_id": float64(42),
			"head_repository.full_name": "fork-owner/example", "links.rerun_url": widthAPI + "actions/runs/30433642/rerun"})
		if strings.Contains(string(result), "logs_url") {
			t.Errorf("a run list carries logs_url: %s", result)
		}
	}

	// A request without the new arguments stays as it was.
	before := len(f.recorded())
	if _, err := invoke(t, core, "github.workflow-runs.list", "observer", `{}`, false); err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery(f.recorded()[before].query)
	if query.Encode() != "exclude_pull_requests=true&page=1&per_page=30" {
		t.Errorf("query = %s, want the request without new filters", query.Encode())
	}
}

func TestRunFiltersAreBoundAndInTheCursor(t *testing.T) {
	f, core, reads := widthCore(t, widthRoutes("[]"))
	first := func(arguments string) string {
		result, err := invoke(t, core, "github.workflow-runs.list", "observer", arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		cursor, _ := widthDecoded(t, result)["next_cursor"].(string)
		if cursor == "" {
			t.Fatalf("%s: no next_cursor in %s", arguments, result)
		}
		return cursor
	}
	for _, tt := range []struct{ name, arguments, other string }{
		{"check_suite_id", `{"limit":1,"check_suite_id":42}`, `{"limit":1,"check_suite_id":43}`},
		{"head_sha", `{"limit":1,"head_sha":"` + widthHeadSHA + `"}`, `{"limit":1}`},
		{"include_pull_requests", `{"limit":1,"include_pull_requests":true}`, `{"limit":1}`},
	} {
		cursor := first(tt.arguments)
		other := strings.TrimSuffix(tt.other, "}") + `,"cursor":"` + cursor + `"}`
		if _, err := invoke(t, core, "github.workflow-runs.list", "observer", other, false); !isInvalidRequest(err) {
			t.Errorf("%s: a cursor of another filter = %v, want an invalid request", tt.name, err)
		}
		same := strings.TrimSuffix(tt.arguments, "}") + `,"cursor":"` + cursor + `"}`
		if _, err := invoke(t, core, "github.workflow-runs.list", "observer", same, false); err != nil {
			t.Errorf("%s: the cursor of the same filter = %v", tt.name, err)
		}
	}
	// A cursor of the unfiltered list still matches the unfiltered request, as before the filters existed.
	cursor := first(`{"limit":1}`)
	if parts := (&actionsArguments{}).widthFilters(); len(parts) != 0 {
		t.Errorf("widthFilters of a request without them = %v, want nothing added to the fingerprint", parts)
	}
	if _, err := invoke(t, core, "github.workflow-runs.list", "observer", `{"limit":1,"cursor":"`+cursor+`"}`, false); err != nil {
		t.Errorf("the cursor of the unfiltered list = %v", err)
	}

	*reads, f.requests = 0, nil
	for _, arguments := range []string{`{"check_suite_id":0}`, `{"check_suite_id":-1}`, `{"head_sha":"abc"}`,
		`{"head_sha":"` + strings.Repeat("g", 40) + `"}`, `{"include_pull_requests":"yes"}`} {
		if _, err := invoke(t, core, "github.workflow-runs.list", "observer", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("reads = %d, requests = %d, want refusals before any credential is resolved", *reads, len(f.recorded()))
	}
}

func TestJobWidth(t *testing.T) {
	_, core, _ := widthCore(t, widthRoutes(`["self-hosted","foo","bar"]`))
	want := map[string]any{
		"url":     "https://github.com/octo-org/example/actions/runs/29679449/job/399444496",
		"node_id": "MDg6Q2hlY2tSdW4zOTk0NDQ0OTY=", "head_sha": "f83a356604ae3c5d03e1b46ef4d1ca77d64a90b0",
		"head_branch": "main", "created_at": "2020-01-20T17:40:18Z",
		"labels.0": "self-hosted", "labels.2": "bar", "runner_id": float64(1), "runner_name": "my runner",
		"runner_group_id": float64(2), "runner_group_name": "my runner group", "workflow_name": "CI",
		"links.url": widthAPI + "actions/jobs/399444496", "links.run_url": widthAPI + "actions/runs/29679449",
		"links.check_run_url": widthAPI + "check-runs/399444496", "steps.0.name": "Set up job",
	}
	result, err := invoke(t, core, "github.workflow-jobs.get", "observer", `{"job_id":399444496}`, false)
	if err != nil {
		t.Fatal(err)
	}
	widthExpect(t, widthDecoded(t, result), want)
	result, err = invoke(t, core, "github.workflow-jobs.list", "observer", `{"run_id":30433642}`, false)
	if err != nil {
		t.Fatal(err)
	}
	list := map[string]any{}
	for path, value := range want {
		list["jobs.0."+path] = value
	}
	widthExpect(t, widthDecoded(t, result), list)

	// At most 50 labels are answered, with a marker.
	many := make([]string, 60)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprintf("label-%d", i))
	}
	_, core, _ = widthCore(t, widthRoutes("["+strings.Join(many, ",")+"]"))
	result, err = invoke(t, core, "github.workflow-jobs.get", "observer", `{"job_id":399444496}`, false)
	if err != nil {
		t.Fatal(err)
	}
	job := widthDecoded(t, result)
	if labels := job["labels"].([]any); len(labels) != 50 || job["labels_truncated"] != true || labels[49] != "label-49" {
		t.Errorf("labels = %d, truncated = %v, want the first 50 with a marker", len(labels), job["labels_truncated"])
	}
}

func TestArtifactWidthAndFilters(t *testing.T) {
	f, core, reads := widthCore(t, widthRoutes("[]"))
	result, err := invoke(t, core, "github.workflow-artifacts.list", "observer",
		`{"run_id":30433642,"name":"Rails","direction":"asc","limit":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "archive_download_url") || strings.Contains(string(result), "/zip") {
		t.Errorf("an artifact carries its download address: %s", result)
	}
	out := widthDecoded(t, result)
	widthExpect(t, out, map[string]any{
		"artifacts.0.node_id": "MDg6QXJ0aWZhY3QxMQ==", "artifacts.0.updated_at": "2020-02-21T14:59:22Z",
		"artifacts.0.links.url": widthAPI + "actions/artifacts/11", "artifacts.0.workflow_run.id": float64(2332938),
		"artifacts.0.workflow_run.repository_id": float64(1296269), "artifacts.0.workflow_run.head_repository_id": float64(1296269),
		"artifacts.0.workflow_run.head_branch": "main", "artifacts.0.workflow_run.head_sha": widthHeadSHA,
	})
	query, _ := url.ParseQuery(f.recorded()[0].query)
	if query.Get("name") != "Rails" || query.Get("direction") != "asc" {
		t.Errorf("query = %v, want name and direction", query)
	}
	cursor, _ := out["next_cursor"].(string)
	for _, other := range []string{`"name":"Other","direction":"asc"`, `"name":"Rails","direction":"desc"`, `"name":"Rails"`} {
		if _, err := invoke(t, core, "github.workflow-artifacts.list", "observer",
			`{"run_id":30433642,`+other+`,"cursor":"`+cursor+`"}`, false); !isInvalidRequest(err) {
			t.Errorf("a cursor of other filters (%s) = %v, want an invalid request", other, err)
		}
	}
	if _, err := invoke(t, core, "github.workflow-artifacts.list", "observer",
		`{"run_id":30433642,"name":"Rails","direction":"asc","cursor":"`+cursor+`"}`, false); err != nil {
		t.Errorf("the cursor of the same filters = %v", err)
	}

	*reads, f.requests = 0, nil
	for _, arguments := range []string{`{"direction":"asc"}`, `{"run_id":1,"direction":"up"}`,
		`{"run_id":1,"name":"` + strings.Repeat("a", 257) + `"}`, `{"run_id":1,"name":""}`} {
		if _, err := invoke(t, core, "github.workflow-artifacts.list", "observer", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("reads = %d, requests = %d, want refusals before any credential is resolved", *reads, len(f.recorded()))
	}
	// Without the new filters the request stays as it was.
	if _, err := invoke(t, core, "github.workflow-artifacts.list", "observer", `{"run_id":30433642}`, false); err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery(f.recorded()[0].query)
	if query.Encode() != "page=1&per_page=30" {
		t.Errorf("query = %s, want the request without new filters", query.Encode())
	}
}

func TestActionsPermissionsWidth(t *testing.T) {
	_, core, _ := widthCore(t, widthRoutes("[]"))
	result, err := invoke(t, core, "github.actions-permissions.get", "admin", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	widthExpect(t, widthDecoded(t, result), map[string]any{"enabled": true, "allowed_actions": "selected",
		"sha_pinning_required": true, "links.selected_actions_url": widthAPI + "actions/permissions/selected-actions"})
}

// A response without the new members answers exactly as before.
func TestActionsWidthLeavesAbsentMembersOut(t *testing.T) {
	_, core, _ := widthCore(t, func(w http.ResponseWriter, r *http.Request) bool {
		rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
		if !ok {
			return false
		}
		switch rest {
		case "actions/workflows/1":
			fmt.Fprint(w, `{"id":1,"name":"CI","path":"p.yml","state":"active"}`)
		case "actions/runs/9":
			fmt.Fprint(w, `{"id":9,"status":"queued","head_commit":null,"head_repository":null,"pull_requests":[],`+
				`"referenced_workflows":null,"actor":null}`)
		case "actions/jobs/8":
			fmt.Fprint(w, `{"id":8,"name":"b","status":"queued","labels":[],"runner_id":null,"runner_name":null}`)
		case "actions/runs/9/artifacts":
			fmt.Fprint(w, `{"total_count":1,"artifacts":[{"id":2,"name":"n","size_in_bytes":3,"expired":true,"workflow_run":null}]}`)
		case "actions/permissions":
			fmt.Fprint(w, `{"enabled":false}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return true
	})
	for _, tt := range []struct{ operation, connection, arguments, want string }{
		{"github.workflows.get", "observer", `{"workflow":"1"}`,
			`{"id":1,"name":"CI","path":"p.yml","repository":"octo-org/example","state":"active"}`},
		{"github.workflow-runs.get", "observer", `{"run_id":9}`,
			`{"id":9,"repository":"octo-org/example","status":"queued"}`},
		{"github.workflow-jobs.get", "observer", `{"job_id":8}`,
			`{"id":8,"name":"b","repository":"octo-org/example","status":"queued"}`},
		{"github.workflow-artifacts.list", "observer", `{"run_id":9}`,
			`{"artifacts":[{"expired":true,"id":2,"name":"n","size_bytes":3}],"has_more":false,"repository":"octo-org/example"}`},
		{"github.actions-permissions.get", "admin", `{}`,
			`{"enabled":false,"repository":"octo-org/example"}`},
	} {
		result, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, false)
		if err != nil {
			t.Errorf("%s = %v", tt.operation, err)
			continue
		}
		if string(result) != tt.want {
			t.Errorf("%s = %s, want %s", tt.operation, result, tt.want)
		}
	}
}

// A head repository inside the targets is shown in full.
func TestRunHeadRepositoryInsideTheTargetsIsFull(t *testing.T) {
	_, core, _ := widthCore(t, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "actions/runs/9") {
			return false
		}
		fmt.Fprint(w, `{"id":9,"status":"queued","head_repository_id":1,"head_repository":{"id":1,"name":"example",`+
			`"full_name":"octo-org/example","private":true,"temp_clone_token":"secret-clone-token"}}`)
		return true
	})
	result, err := invoke(t, core, "github.workflow-runs.get", "observer", `{"run_id":9}`, false)
	if err != nil {
		t.Fatal(err)
	}
	widthExpect(t, widthDecoded(t, result), map[string]any{"head_repository.name": "example", "head_repository.private": true})
	if strings.Contains(string(result), "secret-clone-token") {
		t.Errorf("the clone token reached the answer: %s", result)
	}
}
