package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const widthAppRaw = `{"id":1,"slug":"octoapp","node_id":"MDExOkludGVncmF0aW9uMQ==","client_id":"Iv1.8a61f9b3a7aba766",` +
	`"owner":{"login":"github","id":37,"type":"Organization","html_url":"https://github.com/github",` +
	`"url":"https://api.github.com/orgs/github"},"name":"Octocat App","description":"An app.",` +
	`"external_url":"https://example.invalid","html_url":"https://github.com/apps/octoapp",` +
	`"created_at":"2017-07-08T16:18:44Z","updated_at":"2017-07-08T16:18:44Z",` +
	`"permissions":{"metadata":"read","checks":"write"},"events":["check_run"]}`

const widthAppView = `{"id":1,"slug":"octoapp","node_id":"MDExOkludGVncmF0aW9uMQ==","client_id":"Iv1.8a61f9b3a7aba766",` +
	`"owner":{"login":"github","id":37,"type":"Organization","url":"https://github.com/github",` +
	`"links":{"url":"https://api.github.com/orgs/github"}},"name":"Octocat App","description":"An app.",` +
	`"external_url":"https://example.invalid","url":"https://github.com/apps/octoapp",` +
	`"created_at":"2017-07-08T16:18:44Z","updated_at":"2017-07-08T16:18:44Z",` +
	`"permissions":{"checks":"write","metadata":"read"},"events":["check_run"]}`

// widthCheckRun is a check run as the Get a check run example of GitHub's REST API describes it, with a
// deployment added.
const widthCheckRun = `{"id":4,"head_sha":"ce587453ced02b1526dfb4cb910479d431683101","node_id":"MDg6Q2hlY2tSdW40",` +
	`"external_id":"42","url":"https://api.github.com/repos/octo-org/example/check-runs/4",` +
	`"html_url":"https://github.com/octo-org/example/runs/4","details_url":"https://example.invalid/details",` +
	`"status":"completed","conclusion":"neutral","started_at":"2018-05-04T01:14:52Z","completed_at":"2018-05-04T01:14:53Z",` +
	`"output":{"title":"Mighty Readme report","summary":"There are 0 failures, 2 warnings, and 1 notices.",` +
	`"text":"You may have some misspelled words on lines 2 and 4.","annotations_count":2,` +
	`"annotations_url":"https://api.github.com/repos/octo-org/example/check-runs/4/annotations"},` +
	`"name":"mighty_readme","check_suite":{"id":5},"app":` + widthAppRaw + `,"pull_requests":[{` +
	`"url":"https://api.github.com/repos/octo-org/example/pulls/1","id":1934,"number":3,` +
	`"head":{"ref":"say-hello","sha":"3dca65830a8db1f2c7cdf0beb31a7ff3a1ba0d83","repo":{"id":526,` +
	`"url":"https://api.github.com/repos/octo-org/example","name":"example"}},` +
	`"base":{"ref":"master","sha":"e7fdf7640066d71ad16a86fbcbb9c6a10a18af4f","repo":{"id":526,` +
	`"url":"https://api.github.com/repos/octo-org/example","name":"example"}}}],` +
	`"deployment":{"url":"https://api.github.com/repos/octo-org/example/deployments/326191728","id":326191728,` +
	`"node_id":"MDEwOkRlcGxveW1lbnQzMjYxOTE3Mjg=","task":"deploy","original_environment":"lab","environment":"lab",` +
	`"description":"Deploy request from hubot","created_at":"2021-02-18T08:22:52Z","updated_at":"2021-02-18T08:22:54Z",` +
	`"statuses_url":"https://api.github.com/repos/octo-org/example/deployments/326191728/statuses",` +
	`"repository_url":"https://api.github.com/repos/octo-org/example","transient_environment":false,` +
	`"production_environment":false,"performed_via_github_app":` + widthAppRaw + `}}`

const widthCheckRunView = `{"id":4,"head_sha":"ce587453ced02b1526dfb4cb910479d431683101","node_id":"MDg6Q2hlY2tSdW40",` +
	`"external_id":"42","url":"https://github.com/octo-org/example/runs/4",` +
	`"links":{"url":"https://api.github.com/repos/octo-org/example/check-runs/4","details_url":"https://example.invalid/details"},` +
	`"status":"completed","conclusion":"neutral","started_at":"2018-05-04T01:14:52Z","completed_at":"2018-05-04T01:14:53Z",` +
	`"output":{"title":"Mighty Readme report","summary":"There are 0 failures, 2 warnings, and 1 notices.",` +
	`"text":"You may have some misspelled words on lines 2 and 4.","annotations_count":2,` +
	`"links":{"annotations_url":"https://api.github.com/repos/octo-org/example/check-runs/4/annotations"}},` +
	`"name":"mighty_readme","check_suite":{"id":5},"app":` + widthAppView + `,"pull_requests":[{` +
	`"id":1934,"number":3,"links":{"url":"https://api.github.com/repos/octo-org/example/pulls/1"},` +
	`"head":{"ref":"say-hello","sha":"3dca65830a8db1f2c7cdf0beb31a7ff3a1ba0d83","repo":{"id":526,` +
	`"name":"example","links":{"url":"https://api.github.com/repos/octo-org/example"}}},` +
	`"base":{"ref":"master","sha":"e7fdf7640066d71ad16a86fbcbb9c6a10a18af4f","repo":{"id":526,` +
	`"name":"example","links":{"url":"https://api.github.com/repos/octo-org/example"}}}}],` +
	`"deployment":{"id":326191728,"node_id":"MDEwOkRlcGxveW1lbnQzMjYxOTE3Mjg=","task":"deploy",` +
	`"original_environment":"lab","environment":"lab","description":"Deploy request from hubot",` +
	`"created_at":"2021-02-18T08:22:52Z","updated_at":"2021-02-18T08:22:54Z","transient_environment":false,` +
	`"production_environment":false,"performed_via_github_app":` + widthAppView + `,"links":{` +
	`"url":"https://api.github.com/repos/octo-org/example/deployments/326191728",` +
	`"statuses_url":"https://api.github.com/repos/octo-org/example/deployments/326191728/statuses",` +
	`"repository_url":"https://api.github.com/repos/octo-org/example"}}}`

// widthCombined is the combined status as the Get the combined status example of GitHub's REST API describes it.
const widthCombined = `{"state":"success","statuses":[{"url":"https://api.github.com/repos/octo-org/example/statuses/1",` +
	`"avatar_url":"https://avatars.example.invalid/hubot.gif","id":1,"node_id":"MDY6U3RhdHVzMQ==","state":"success",` +
	`"description":"Build has completed successfully","target_url":"https://ci.example.invalid/1000/output",` +
	`"context":"continuous-integration/jenkins","required":true,"created_at":"2012-07-20T01:19:13Z",` +
	`"updated_at":"2012-07-20T01:19:14Z"}],"sha":"6dcb09b5b57875f334f61aebed695e2e4193db5e","total_count":2,` +
	`"repository":{"id":1296269,"name":"example"},"commit_url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5",` +
	`"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5/status"}`

const widthStatusView = `{"id":1,"node_id":"MDY6U3RhdHVzMQ==","state":"success","context":"continuous-integration/jenkins",` +
	`"description":"Build has completed successfully","required":true,"created_at":"2012-07-20T01:19:13Z",` +
	`"updated_at":"2012-07-20T01:19:14Z","links":{"url":"https://api.github.com/repos/octo-org/example/statuses/1",` +
	`"target_url":"https://ci.example.invalid/1000/output","avatar_url":"https://avatars.example.invalid/hubot.gif"}}`

// serveWidthChecks serves one pull request whose check run and status answers are the given documents.
func serveWidthChecks(t *testing.T, runs, combined func(*http.Request) string) (*fakeGitHub, string) {
	t.Helper()
	f, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 7, title: "wide", state: "open", headSHA: "aaa7"})
	inner := f.failure
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits/aaa7/check-runs"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, runs(r))
			return true
		case strings.HasSuffix(r.URL.Path, "/commits/aaa7/status"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, combined(r))
			return true
		}
		return inner(w, r)
	}
	return f, base
}

func constant(document string) func(*http.Request) string {
	return func(*http.Request) string { return document }
}

func TestChecksCarryTheFullCheckRunAndStatus(t *testing.T) {
	old := `{"name":"lint","status":"in_progress","started_at":"2026-01-01T00:00:00Z"}`
	f, base := serveWidthChecks(t, constant(`{"total_count":2,"check_runs":[`+widthCheckRun+`,`+old+`]}`),
		constant(widthCombined))
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, pullChecksList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(result), `{"number":7,"head_sha":"aaa7","checks":[`+
		`{"name":"mighty_readme","status":"completed","conclusion":"neutral","updated_at":"2018-05-04T01:14:53Z",`+
		`"url":"https://example.invalid/details"},`+
		`{"name":"lint","status":"in_progress","updated_at":"2026-01-01T00:00:00Z"},`+
		`{"name":"continuous-integration/jenkins","status":"completed","conclusion":"success",`+
		`"updated_at":"2012-07-20T01:19:14Z","url":"https://ci.example.invalid/1000/output"}],`+
		`"overall":"pending","truncated":true,"check_runs":[`+widthCheckRunView+`,`+
		`{"name":"lint","status":"in_progress","started_at":"2026-01-01T00:00:00Z"}],"check_runs_total_count":2,`+
		`"statuses":[`+widthStatusView+`],"state":"success","total_count":2,`+
		`"sha":"6dcb09b5b57875f334f61aebed695e2e4193db5e","links":{`+
		`"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5/status",`+
		`"commit_url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5"},`+
		`"repository":"octo-org/example"}`)

	// The permissions of an app are a free map the schema bounds with additionalProperties.
	top := decodeAny(t, result).(map[string]any)
	run := top["check_runs"].([]any)[0].(map[string]any)
	delete(run["app"].(map[string]any), "permissions")
	delete(run["deployment"].(map[string]any)["performed_via_github_app"].(map[string]any), "permissions")
	checkSchemaCovers(t, "$.check_runs[]", run, decodeAny(t, entrySchema(t, pullChecksList.OutputSchema, "check_runs")).(map[string]any))
	checkSchemaCovers(t, "$.statuses[]", top["statuses"].([]any)[0],
		decodeAny(t, entrySchema(t, pullChecksList.OutputSchema, "statuses")).(map[string]any))
	// Three requests as before: the pull request, the check runs, and the combined status.
	if got := len(f.recorded()); got != 3 {
		t.Errorf("sent %d requests, want 3", got)
	}
}

func TestChecksWithoutTheNewMembersKeepTheirOutput(t *testing.T) {
	_, base := serveWidthChecks(t, constant(`{"total_count":1,"check_runs":[{"name":"build","status":"completed",`+
		`"conclusion":"success","completed_at":"2026-01-01T00:00:00Z"}]}`), constant(`{"total_count":0,"statuses":[]}`))
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	result, err := invoke(t, core, pullChecksList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(result), `{"number":7,"head_sha":"aaa7","checks":[{"name":"build",`+
		`"status":"completed","conclusion":"success","updated_at":"2026-01-01T00:00:00Z"}],"overall":"success",`+
		`"truncated":false,"check_runs":[{"name":"build","status":"completed","conclusion":"success",`+
		`"completed_at":"2026-01-01T00:00:00Z"}],"check_runs_total_count":1,"total_count":0,`+
		`"repository":"octo-org/example"}`)
}

// The filters reach only the check run request; overall follows the filtered check runs and every status.
func TestCheckFiltersReachOnlyTheCheckRuns(t *testing.T) {
	failing := `{"name":"build","status":"completed","conclusion":"failure"}`
	running := `{"name":"deploy","status":"in_progress"}`
	f, base := serveWidthChecks(t, func(r *http.Request) string {
		if r.URL.Query().Get("status") == "in_progress" {
			return `{"total_count":1,"check_runs":[` + running + `]}`
		}
		return `{"total_count":2,"check_runs":[` + failing + `,` + running + `]}`
	}, constant(`{"state":"success","total_count":1,"statuses":[{"context":"ci","state":"success"}]}`))
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	overall := func(arguments string) string {
		t.Helper()
		result, err := invoke(t, core, pullChecksList.ID, "repo", arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Overall string `json:"overall"`
		}
		_ = json.Unmarshal(result, &got)
		return got.Overall
	}
	if got := overall(`{"number":7}`); got != "failure" {
		t.Errorf("overall without filter = %s, want failure", got)
	}
	if got := overall(`{"number":7,"check_name":"deploy","status":"in_progress","filter":"all","app_id":15368}`); got != "pending" {
		t.Errorf("overall with a filter = %s, want pending", got)
	}
	var runs, statuses []recorded
	for _, request := range f.recorded() {
		switch {
		case strings.HasSuffix(request.path, "/check-runs"):
			runs = append(runs, request)
		case strings.HasSuffix(request.path, "/status"):
			statuses = append(statuses, request)
		}
	}
	if len(runs) != 2 || runs[0].query != "per_page=100" ||
		runs[1].query != "app_id=15368&check_name=deploy&filter=all&per_page=100&status=in_progress" {
		t.Errorf("check run queries = %+v", runs)
	}
	for _, request := range statuses {
		if request.query != "per_page=100" {
			t.Errorf("status query = %q, want only per_page", request.query)
		}
	}
}

func TestCheckFiltersAreRefusedBeforeACredentialIsRead(t *testing.T) {
	f, base := serveWidthChecks(t, constant(`{}`), constant(`{}`))
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)
	for _, arguments := range []string{
		`{"number":7,"check_name":"` + strings.Repeat("x", 101) + `"}`,
		`{"number":7,"check_name":"bad\u0000name"}`,
		`{"number":7,"status":"waiting"}`,
		`{"number":7,"filter":"newest"}`,
		`{"number":7,"app_id":0}`,
		`{"number":7,"app_id":-5}`,
	} {
		if _, err := invoke(t, core, pullChecksList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("a refused filter read %d credentials and sent %d requests", reads, len(f.recorded()))
	}
}
