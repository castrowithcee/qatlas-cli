package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	actionsPrefix = "/api/v3/repos/octo-org/example/"
	logCanary     = "job-log-canary-7c2d"
	failedRun     = 5000
	runningRun    = 5001
	failedJob     = 7001
)

// releaseWorkflow declares workflow_dispatch inputs of every type the dispatch checks.
const releaseWorkflow = `name: Release
on:
  push:
    tags: ["v*"]
  workflow_dispatch:
    inputs:
      channel:
        type: choice
        required: true
        options: [beta, stable]
      dry_run:
        type: boolean
        default: true
      count:
        type: number
      note:
        description: free text
jobs: {}
`

// ciWorkflow runs on pushes only.
const ciWorkflow = "name: CI\non: [push, pull_request]\njobs: {}\n"

// fakeRun is one workflow run of the fake repository.
type fakeRun struct {
	id                 int
	status, conclusion string
}

// fakeActions answers the Actions and contents routes of the bound repository through the failure hook of
// fakeGitHub, which records every request first. Every other repository is unknown to it.
type fakeActions struct {
	storage string
	runs    []fakeRun
}

func actionsRuns() []fakeRun {
	runs := []fakeRun{}
	for i := 0; i < 75; i++ {
		run := fakeRun{id: 1000 + i, status: "completed", conclusion: "success"}
		switch i % 3 {
		case 0:
			run.conclusion = "failure"
		case 2:
			run.status, run.conclusion = "in_progress", ""
		}
		runs = append(runs, run)
	}
	return append(runs, fakeRun{id: failedRun, status: "completed", conclusion: "failure"},
		fakeRun{id: runningRun, status: "in_progress"})
}

func runJSONOf(run fakeRun) string {
	conclusion := "null"
	if run.conclusion != "" {
		conclusion = strconv.Quote(run.conclusion)
	}
	return fmt.Sprintf(`{"id":%d,"name":"CI","display_title":"Fix %d","workflow_id":1,"run_number":%d,`+
		`"run_attempt":1,"event":"push","status":%q,"conclusion":%s,"head_branch":"main","head_sha":"abc",`+
		`"actor":{"login":"octocat"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z",`+
		`"run_started_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/octo-org/example/actions/runs/%d",`+
		`"pull_requests":[{"number":1}],"repository":{"full_name":"octo-org/example"},`+
		`"head_commit":{"message":"%s"}}`, run.id, run.id, run.id, run.status, conclusion, run.id, bodyCanary)
}

// page answers one REST page of entries the way GitHub does, with the total count of the list.
func page(w http.ResponseWriter, r *http.Request, key string, entries []string) {
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	number, _ := strconv.Atoi(r.URL.Query().Get("page"))
	start := min((number-1)*perPage, len(entries))
	end := min(start+perPage, len(entries))
	fmt.Fprintf(w, `{"total_count":%d,%q:[%s]}`, len(entries), key, strings.Join(entries[start:end], ","))
}

func (a *fakeActions) route(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok || !(strings.HasPrefix(rest, "actions/") || strings.HasPrefix(rest, "contents/")) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	workflows := map[string]string{
		"1": `{"id":1,"name":"CI","path":".github/workflows/ci.yml","state":"active","html_url":"https://github.com/w/1"}`,
		"2": `{"id":2,"name":"Release","path":".github/workflows/release.yml","state":"active","html_url":"https://github.com/w/2"}`,
		"3": `{"id":3,"name":"CodeQL","path":"dynamic/github-code-scanning/codeql","state":"active"}`,
		"4": `{"id":4,"name":"Old","path":".github/workflows/old.yml","state":"disabled_manually"}`,
	}
	workflows["ci.yml"], workflows["release.yml"], workflows["old.yml"] = workflows["1"], workflows["2"], workflows["4"]
	switch {
	case r.Method == http.MethodGet && rest == "actions/workflows":
		page(w, r, "workflows", []string{workflows["1"], workflows["2"], workflows["3"], workflows["4"]})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "actions/workflows/") && !strings.Contains(rest, "/runs"):
		if workflow, ok := workflows[strings.TrimPrefix(rest, "actions/workflows/")]; ok {
			fmt.Fprint(w, workflow)
			return true
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && (rest == "actions/runs" || rest == "actions/workflows/ci.yml/runs"):
		status := r.URL.Query().Get("status")
		entries := []string{}
		for i := len(a.runs) - 1; i >= 0; i-- {
			if run := a.runs[i]; status == "" || run.status == status || run.conclusion == status {
				entries = append(entries, runJSONOf(run))
			}
		}
		page(w, r, "workflow_runs", entries)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "actions/runs/") && strings.HasSuffix(rest, "/jobs"):
		jobs := []string{}
		for i := 0; i < 3; i++ {
			jobs = append(jobs, fmt.Sprintf(`{"id":%d,"run_id":%d,"name":"build %d","status":"completed",`+
				`"conclusion":"failure","run_attempt":1,"started_at":"2026-01-01T00:00:00Z",`+
				`"completed_at":"2026-01-01T00:01:00Z","html_url":"https://github.com/j/%d","runner_name":"r",`+
				`"steps":[{"number":1,"name":"checkout","status":"completed","conclusion":"success"}]}`,
				failedJob+i, failedRun, i, i))
		}
		page(w, r, "jobs", jobs)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "actions/runs/") && strings.HasSuffix(rest, "/artifacts"):
		page(w, r, "artifacts", []string{
			`{"id":1,"name":"coverage","size_in_bytes":2048,"expired":false,"created_at":"2026-01-01T00:00:00Z",` +
				`"expires_at":"2026-04-01T00:00:00Z","digest":"sha256:abc",` +
				`"archive_download_url":"https://api.github.com/repos/octo-org/example/actions/artifacts/1/zip"}`,
			`{"id":2,"name":"binary","size_in_bytes":99999999,"expired":true}`,
		})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "actions/runs/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(rest, "actions/runs/"))
		for _, run := range a.runs {
			if run.id == id {
				fmt.Fprint(w, runJSONOf(run))
				return true
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && rest == "actions/jobs/"+strconv.Itoa(failedJob):
		fmt.Fprintf(w, `{"id":%d,"run_id":%d,"name":"build","status":"completed","conclusion":"failure",`+
			`"run_attempt":2,"started_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/j/1",`+
			`"labels":["ubuntu-latest"],"steps":[{"number":1,"name":"checkout","status":"completed",`+
			`"conclusion":"success","started_at":"2026-01-01T00:00:00Z","completed_at":"2026-01-01T00:00:05Z"},`+
			`{"number":2,"name":"test","status":"completed","conclusion":"failure"}]}`, failedJob, failedRun)
	case r.Method == http.MethodGet && rest == "actions/jobs/"+strconv.Itoa(failedJob)+"/logs":
		w.Header().Set("Location", a.storage+"/logs/"+strconv.Itoa(failedJob)+"?sig=signed")
		w.WriteHeader(http.StatusFound)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "contents/.github/workflows/"):
		files := map[string]string{"release.yml": releaseWorkflow, "ci.yml": ciWorkflow}
		file, ok := files[strings.TrimPrefix(rest, "contents/.github/workflows/")]
		if !ok || r.URL.Query().Get("ref") != "main" {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(file))
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","size":%d,"content":"%s\n"}`, len(file), encoded)
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/dispatches"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && (strings.HasSuffix(rest, "/rerun") || strings.HasSuffix(rest, "/rerun-failed-jobs")):
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/cancel"):
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

// fakeStorage is the log storage GitHub redirects to. It records the headers it receives and serves the
// last bytes of the log a range asks for, unless ignoreRange makes it send the whole log.
type fakeStorage struct {
	mu          sync.Mutex
	headers     []http.Header
	log         string
	ignoreRange bool
	answer      func(http.ResponseWriter) bool
}

func (s *fakeStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.headers = append(s.headers, r.Header.Clone())
	s.mu.Unlock()
	if s.answer != nil && s.answer(w) {
		return
	}
	suffix, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes=-")
	if s.ignoreRange || !ok {
		fmt.Fprint(w, s.log)
		return
	}
	if s.log == "" {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	n, _ := strconv.Atoi(suffix)
	start := max(len(s.log)-n, 0)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(s.log)-1, len(s.log)))
	w.WriteHeader(http.StatusPartialContent)
	fmt.Fprint(w, s.log[start:])
}

func (s *fakeStorage) received() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...)
}

// serveActions starts the fake GitHub with its Actions routes and a log storage on another host.
func serveActions(t *testing.T) (*fakeGitHub, *fakeStorage, string) {
	t.Helper()
	storage := &fakeStorage{}
	storageServer := httptest.NewTLSServer(storage)
	t.Cleanup(storageServer.Close)
	actions := &fakeActions{storage: storageServer.URL, runs: actionsRuns()}
	f := &fakeGitHub{failure: actions.route}
	return f, storage, serve(t, f)
}

func logLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("2026-01-01T00:00:00.0000000Z line %04d", i)
	}
	return strings.Join(lines, "\n") + "\n"
}

// actionsConfig binds one repository to connections that differ only in what they may do with Actions.
func actionsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	observer := append([]string{}, observerTools...)
	all := append(append([]string{}, observerTools...), operatorTools...)
	execute := []config.Permission{config.PermissionRead, config.PermissionExecute}
	repo := func(permissions []config.Permission, tools []string) config.Connection {
		return config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget, Permissions: permissions,
			Tools: tools}
	}
	cfg.Connections["observer"] = repo([]config.Permission{config.PermissionRead}, observer)
	cfg.Connections["reader"] = repo(nil, nil)
	cfg.Connections["planner"] = repo([]config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate}, nil)
	cfg.Connections["listed"] = repo(execute, observer)
	cfg.Connections["operator"] = repo(execute, all)
	cfg.Connections["project"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: execute}
	return cfg
}

func invoke(t *testing.T, core *application.Core, operation, connection, arguments string, confirmed bool) (json.RawMessage, error) {
	t.Helper()
	response, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: operation,
		Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: confirmed})
	return response.Result, err
}

func TestActionsProfilesSeparateObserverAndOperator(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	profiles := map[string]config.ToolProfile{}
	for _, profile := range metadata.Profiles {
		profiles[profile.ID] = profile
	}
	observer, operator, read := profiles["actions-observer"], profiles["actions-operator"], profiles["read"]
	if observer.Recommended || operator.Recommended || !read.Recommended || len(read.Tools) != 5 {
		t.Errorf("profiles = %+v, want the read profile unchanged and both Actions profiles not recommended", profiles)
	}
	if got := metadata.ProfilePermissions(observer); len(got) != 1 || got[0] != config.PermissionRead ||
		len(observer.Tools) != 8 {
		t.Errorf("observer = %v with %v, want the eight reads only", observer.Tools, got)
	}
	got := metadata.ProfilePermissions(operator)
	if len(got) != 2 || got[1] != config.PermissionExecute || len(operator.Tools) != 12 {
		t.Errorf("operator = %v with %v, want the reads and the four executions", operator.Tools, got)
	}
}

// A connection without execute, or with a tools list without the operator tools, neither discovers nor runs
// them; nor does a project connection. Every refusal ends before a secret is read and before GitHub is
// contacted, and so do unconfirmed executions and arguments outside the bounds.
func TestActionsOperatorToolsNeedExecuteAndTheirTools(t *testing.T) {
	f, _, base := serveActions(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), actionsConfig(base), resolver(red, &reads), red)

	for connection, want := range map[string]int{"observer": 0, "reader": 0, "planner": 0, "listed": 0, "operator": 4} {
		found, err := core.Search(application.SearchRequest{Provider: Provider, Connection: connection,
			Effect: capability.EffectExecute})
		if err != nil || len(found.Operations) != want {
			t.Errorf("%s discovers %d executions (%v), want %d", connection, len(found.Operations), err, want)
		}
	}
	found, _ := core.Search(application.SearchRequest{Provider: Provider, Connection: "observer"})
	if len(found.Operations) != 8 {
		t.Errorf("observer discovers %d tools, want the eight observer tools", len(found.Operations))
	}

	dispatch := `{"workflow":"release.yml","ref":"main","inputs":{"channel":"beta"}}`
	tests := []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
	}{
		{"reads only", "github.workflows.dispatch", "reader", dispatch, true, &capability.UnsupportedError{}},
		{"planning rights", "github.workflowruns.rerun", "planner", `{"run_id":5000}`, true, &capability.UnsupportedError{}},
		{"observer", "github.workflowruns.cancel", "observer", `{"run_id":5001}`, true, &capability.UnsupportedError{}},
		{"tools without operators", "github.workflowruns.rerunfailed", "listed", `{"run_id":5000}`, true,
			&capability.UnsupportedError{}},
		{"project connection", "github.workflows.dispatch", "project", dispatch, true, &capability.UnsupportedError{}},
		{"project observer", "github.workflowruns.list", "project", `{}`, false, &capability.UnsupportedError{}},
		{"unconfirmed dispatch", "github.workflows.dispatch", "operator", dispatch, false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed cancel", "github.workflowruns.cancel", "operator", `{"run_id":5001}`, false,
			&application.ConfirmationRequiredError{}},
		{"an owner argument", "github.workflowruns.rerun", "operator", `{"run_id":5000,"owner":"other"}`, true,
			&application.InvalidRequestError{}},
		{"a repository argument", "github.workflowruns.list", "operator", `{"repo":"other/repo"}`, false,
			&application.InvalidRequestError{}},
		{"a path as workflow", "github.workflows.dispatch", "operator", `{"workflow":"../x.yml","ref":"main"}`, true,
			&application.InvalidRequestError{}},
		{"a ref with two dots", "github.workflows.dispatch", "operator", `{"workflow":"release.yml","ref":"a..b"}`,
			true, &application.InvalidRequestError{}},
		{"a number as input", "github.workflows.dispatch", "operator",
			`{"workflow":"release.yml","ref":"main","inputs":{"count":3}}`, true, &application.InvalidRequestError{}},
		{"an unusable input name", "github.workflows.dispatch", "operator",
			`{"workflow":"release.yml","ref":"main","inputs":{"a b":"x"}}`, true, &application.InvalidRequestError{}},
		{"too many inputs", "github.workflows.dispatch", "operator",
			`{"workflow":"release.yml","ref":"main","inputs":{` + manyInputs(26) + `}}`, true,
			&application.InvalidRequestError{}},
		{"an unknown status", "github.workflowruns.list", "observer", `{"status":"broken"}`, false,
			&application.InvalidRequestError{}},
		{"a free created filter", "github.workflowruns.list", "observer", `{"created_from":">=2026-01-01"}`, false,
			&application.InvalidRequestError{}},
		{"a reversed time range", "github.workflowruns.list", "observer",
			`{"created_from":"2026-02-01","created_to":"2026-01-01"}`, false, &application.InvalidRequestError{}},
		{"a log beyond the bound", "github.workflowjobs.log", "observer", `{"job_id":7001,"max_bytes":1048576}`, false,
			&application.InvalidRequestError{}},
		{"a foreign cursor", "github.workflowruns.list", "observer", `{"cursor":"AAAAAAAAAAAAAAAAMjoxMA"}`, false,
			&application.InvalidRequestError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, tt.confirmed)
			if want := fmt.Sprintf("%T", tt.want); fmt.Sprintf("%T", err) != want {
				t.Errorf("err = %T %v, want %s", err, err, want)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

func manyInputs(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf(`"i%02d":"x"`, i)
	}
	return strings.Join(names, ",")
}

// A status-filtered run list is read page by page with a cursor bound to its filters; every batch is
// compact and every request stays below the bound repository.
func TestRunsAreFilteredPagedAndCompact(t *testing.T) {
	f, _, base := serveActions(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), actionsConfig(base), resolver(red, nil), red)

	var ids []int64
	arguments := map[string]any{"status": "failure", "limit": 10}
	for batch := 0; ; batch++ {
		if batch > 10 {
			t.Fatal("the list did not end")
		}
		document, _ := json.Marshal(arguments)
		result, err := invoke(t, core, "github.workflowruns.list", "observer", string(document), false)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"pull_requests", "repository", "head_commit", bodyCanary} {
			if strings.Contains(string(result), forbidden) {
				t.Fatalf("a run list carries %q: %s", forbidden, result)
			}
		}
		var listed struct {
			Runs []struct {
				ID         int64  `json:"id"`
				Conclusion string `json:"conclusion"`
				Title      string `json:"title"`
			} `json:"runs"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(result, &listed); err != nil {
			t.Fatal(err)
		}
		for _, run := range listed.Runs {
			if run.Conclusion != "failure" || run.Title == "" {
				t.Errorf("run = %+v, want a compact failed run", run)
			}
			ids = append(ids, run.ID)
		}
		if !listed.HasMore {
			break
		}
		// A continuation keeps the page size of its first batch.
		arguments = map[string]any{"status": "failure", "limit": 3, "cursor": listed.NextCursor}
	}
	if len(ids) != 26 || ids[0] != failedRun || ids[25] != 1000 {
		t.Errorf("ids = %v, want every failed run once, newest first", ids)
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("run %d was listed twice", id)
		}
		seen[id] = true
	}
	for _, request := range f.recorded() {
		query, _ := url.ParseQuery(request.query)
		if request.path != actionsPrefix+"actions/runs" || query.Get("status") != "failure" ||
			query.Get("per_page") != "10" || query.Get("exclude_pull_requests") != "true" ||
			request.auth != "Bearer "+tokenValue {
			t.Errorf("request = %+v, want a filtered page of the bound repository", request)
		}
	}

	// A cursor belongs to its filters; the workflow and time filters travel as GitHub parameters.
	first, _ := invoke(t, core, "github.workflowruns.list", "observer", `{"status":"success","limit":5}`, false)
	var listed struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(first, &listed)
	if _, err := invoke(t, core, "github.workflowruns.list", "observer",
		`{"status":"failure","cursor":"`+listed.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, "github.workflowruns.list", "observer", `{"workflow":"ci.yml","branch":"main",`+
		`"event":"push","actor":"dependabot[bot]","created_from":"2026-01-01","created_to":"2026-01-31T23:59:59Z"}`,
		false); err != nil {
		t.Fatal(err)
	}
	request := f.recorded()[before]
	query, _ := url.ParseQuery(request.query)
	if request.path != actionsPrefix+"actions/workflows/ci.yml/runs" || query.Get("branch") != "main" ||
		query.Get("event") != "push" || query.Get("actor") != "dependabot[bot]" ||
		query.Get("created") != "2026-01-01..2026-01-31T23:59:59Z" {
		t.Errorf("request = %+v, want the structured filters as GitHub parameters", request)
	}

	// GitHub answers at most 1000 runs of a filtered list, whatever total it reports.
	capped := &actionsArguments{page: 10, perPage: 100, binding: []byte("b")}
	if more, _ := capped.more(100, min(5000, maxFilteredRuns)); more {
		t.Error("a filtered list continues beyond the runs GitHub answers")
	}
}

// Workflows, jobs, and artifacts are listed and read as compact metadata; only a job read on its own
// carries its steps, and an artifact never its download address.
func TestActionsMetadataIsCompact(t *testing.T) {
	_, _, base := serveActions(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), actionsConfig(base), resolver(red, nil), red)

	for _, tt := range []struct {
		operation, arguments string
		want, forbidden      []string
	}{
		{"github.workflows.list", `{"limit":3}`, []string{`"path":".github/workflows/ci.yml"`, `"has_more":true`},
			nil},
		{"github.workflows.get", `{"workflow":"release.yml"}`, []string{`"id":2`, `"state":"active"`}, nil},
		{"github.workflowruns.get", `{"run_id":5000}`, []string{`"conclusion":"failure"`, `"actor":"octocat"`},
			[]string{"pull_requests", bodyCanary}},
		{"github.workflowjobs.list", `{"run_id":5000}`, []string{`"name":"build 0"`, `"has_more":false`},
			[]string{"steps", "runner_name"}},
		{"github.workflowjobs.get", `{"job_id":7001}`, []string{`"steps":[`, `"name":"test"`, `"attempt":2`},
			[]string{"labels"}},
		{"github.workflowartifacts.list", `{"run_id":5000}`, []string{`"size_bytes":99999999`, `"expired":true`},
			[]string{"archive_download_url", "/zip"}},
	} {
		result, err := invoke(t, core, tt.operation, "observer", tt.arguments, false)
		if err != nil {
			t.Errorf("%s = %v", tt.operation, err)
			continue
		}
		for _, want := range tt.want {
			if !strings.Contains(string(result), want) {
				t.Errorf("%s = %s, want %s", tt.operation, result, want)
			}
		}
		for _, forbidden := range tt.forbidden {
			if strings.Contains(string(result), forbidden) {
				t.Errorf("%s = %s, carries %s", tt.operation, result, forbidden)
			}
		}
	}
	if _, err := invoke(t, core, "github.workflowruns.get", "observer", `{"run_id":42}`, false); classOf(err) !=
		provider.ClassProviderError {
		t.Errorf("an unknown run = %v, want a provider error", err)
	}
}

// A job log is read from the signed storage address without the token or any other GitHub header, only its
// end is asked for, the answer holds at most the requested lines and bytes as plain text, and nothing
// reaches an audit event or the disk.
func TestJobLogIsBoundedAndLeavesTheTokenBehind(t *testing.T) {
	f, storage, base := serveActions(t)
	storage.log = logLines(1000) + "\x1b[31mError:\x1b[0m failed\x00\r\n" + logCanary + "\n"
	red := &redact.Redactor{}
	var audit strings.Builder
	core := application.New(registry(t), actionsConfig(base), resolver(red, nil), red)
	core.SetAudit(&audit)
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)

	var excerpt JobLog
	for _, ignoreRange := range []bool{false, true} {
		storage.ignoreRange = ignoreRange
		result, err := invoke(t, core, "github.workflowjobs.log", "observer", `{"job_id":7001,"lines":5}`, false)
		if err != nil {
			t.Fatalf("range ignored %v: %v", ignoreRange, err)
		}
		if err := json.Unmarshal(result, &excerpt); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(excerpt.Log, "\n")
		if excerpt.Lines != 5 || len(lines) != 5 || !excerpt.Truncated || lines[3] != "Error: failed" ||
			lines[4] != logCanary || lines[0] != "2026-01-01T00:00:00.0000000Z line 0997" {
			t.Errorf("range ignored %v: excerpt = %+v", ignoreRange, excerpt)
		}
	}
	headers := storage.received()
	if len(headers) != 2 || headers[0].Get("Range") != "bytes=-8192" {
		t.Fatalf("storage requests = %v, want one ranged request per read", headers)
	}
	for _, header := range headers {
		if header.Get("Authorization") != "" || header.Get("X-GitHub-Api-Version") != "" ||
			strings.Contains(fmt.Sprint(header), tokenValue) {
			t.Errorf("the storage received GitHub credentials or headers: %v", header)
		}
	}
	for _, request := range f.recorded() {
		if request.path != actionsPrefix+"actions/jobs/7001/logs" || request.auth != "Bearer "+tokenValue {
			t.Errorf("GitHub request = %+v, want only the log route of the bound repository", request)
		}
	}
	if audit.Len() != 0 {
		t.Errorf("audit = %q, want no event for a read", audit.String())
	}
	if entries, _ := os.ReadDir(temp); len(entries) != 0 {
		t.Errorf("the log read left files behind: %v", entries)
	}

	// The byte bound holds whatever the line bound allows.
	result, err := invoke(t, core, "github.workflowjobs.log", "observer", `{"job_id":7001,"lines":500,"max_bytes":1024}`,
		false)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result, &excerpt); err != nil || len(excerpt.Log) > 1024 || excerpt.Lines < 10 {
		t.Errorf("excerpt = %d bytes in %d lines, %v", len(excerpt.Log), excerpt.Lines, err)
	}

	// A short log comes back whole, and an empty one as no line.
	storage.ignoreRange = false
	storage.log = "only line\n"
	c := client(t, base, repoTarget)
	if got, err := c.jobLog(context.Background(), failedJob, 50, defaultLogBytes); err != nil || got.Log != "only line" ||
		got.Truncated || got.Lines != 1 {
		t.Errorf("short log = %+v, %v", got, err)
	}
	storage.log = ""
	if got, err := c.jobLog(context.Background(), failedJob, 50, defaultLogBytes); err != nil || got.Log != "" ||
		got.Lines != 0 {
		t.Errorf("empty log = %+v, %v", got, err)
	}

	// A storage that ignores the range is read only up to the download bound.
	storage.ignoreRange = true
	storage.log = strings.Repeat("x", maxLogDownload+1)
	if _, err := c.jobLog(context.Background(), failedJob, 50, defaultLogBytes); classOf(err) !=
		provider.ClassInvalidResponse {
		t.Errorf("oversized log = %v, want an invalid response", err)
	}

	// A further redirect of the storage is not followed.
	storage.log = "x"
	storage.answer = func(w http.ResponseWriter) bool {
		w.Header().Set("Location", "https://elsewhere.example.invalid/")
		w.WriteHeader(http.StatusFound)
		return true
	}
	before := len(storage.received())
	if _, err := c.jobLog(context.Background(), failedJob, 50, defaultLogBytes); err == nil ||
		!strings.Contains(err.Error(), "HTTP 302") || len(storage.received()) != before+1 {
		t.Errorf("a storage redirect = %v, want a refusal after one storage request", err)
	}
}

// A log address that is not https is never requested.
func TestJobLogRefusesAnUnsafeAddress(t *testing.T) {
	storage := &fakeStorage{log: "secret"}
	plain := httptest.NewServer(storage)
	t.Cleanup(plain.Close)
	actions := &fakeActions{storage: plain.URL, runs: actionsRuns()}
	f := &fakeGitHub{failure: actions.route}
	base := serve(t, f)
	c := client(t, base, repoTarget)
	if _, err := c.jobLog(context.Background(), failedJob, 50, defaultLogBytes); err == nil ||
		!strings.Contains(err.Error(), "does not read") {
		t.Errorf("jobLog() = %v, want a refused address", err)
	}
	if len(storage.received()) != 0 {
		t.Error("the plain http address was requested")
	}
}

func TestDispatchInputsAreReadFromEveryTriggerForm(t *testing.T) {
	for _, tt := range []struct {
		file   string
		ok     bool
		inputs int
	}{
		{"on: workflow_dispatch\n", true, 0},
		{"on: push\n", false, 0},
		{"on: [push, workflow_dispatch]\n", true, 0},
		{"on:\n  workflow_dispatch:\n", true, 0},
		{"on:\n  push:\n  workflow_dispatch:\n    inputs:\n      a: {}\n      b: {required: true}\n", true, 2},
		{releaseWorkflow, true, 4},
		{ciWorkflow, false, 0},
	} {
		inputs, ok, err := dispatchInputs([]byte(tt.file))
		if err != nil || ok != tt.ok || len(inputs) != tt.inputs {
			t.Errorf("dispatchInputs(%q) = %v, %v, %v", tt.file, inputs, ok, err)
		}
	}
	if _, _, err := dispatchInputs([]byte("on: [unclosed\n")); err == nil {
		t.Error("an unreadable file was accepted")
	}
}

// A dispatch reads the workflow and its file at ref, checks the inputs against the declared ones, and
// sends one request to the bound repository; a refused input sends none.
func TestDispatchChecksTheDeclaredInputs(t *testing.T) {
	f, _, base := serveActions(t)
	c := client(t, base, repoTarget)

	answer, err := c.dispatchWorkflow(context.Background(), "release.yml", "main",
		map[string]string{"channel": "beta", "count": "3", "note": "hello " + bodyCanary})
	if err != nil || answer.WorkflowID != 2 || answer.Path != ".github/workflows/release.yml" || !answer.Accepted {
		t.Fatalf("dispatchWorkflow() = %+v, %v", answer, err)
	}
	requests := f.recorded()
	if len(requests) != 3 || requests[0].path != actionsPrefix+"actions/workflows/release.yml" ||
		requests[1].path != actionsPrefix+"contents/.github/workflows/release.yml" || requests[1].query != "ref=main" ||
		requests[2].method != http.MethodPost || requests[2].path != actionsPrefix+"actions/workflows/2/dispatches" {
		t.Fatalf("requests = %+v, want the workflow, its file, and one dispatch", requests)
	}
	inputs, _ := requests[2].body["inputs"].(map[string]any)
	if requests[2].body["ref"] != "main" || len(inputs) != 3 || inputs["channel"] != "beta" {
		t.Errorf("dispatch body = %v", requests[2].body)
	}

	for _, tt := range []struct {
		name, workflow, ref string
		inputs              map[string]string
		detail              string
	}{
		{"an undeclared input", "release.yml", "main", map[string]string{"channel": "beta", "debug": "1"},
			"declared inputs: channel, count, dry_run, note"},
		{"a missing required input", "release.yml", "main", nil, "channel is required"},
		{"an unknown option", "release.yml", "main", map[string]string{"channel": "nightly"}, "beta, stable"},
		{"a boolean", "release.yml", "main", map[string]string{"channel": "beta", "dry_run": "yes"}, "true or false"},
		{"a number", "release.yml", "main", map[string]string{"channel": "beta", "count": "many"}, "takes a number"},
		{"no dispatch trigger", "ci.yml", "main", nil, "does not declare workflow_dispatch"},
		{"a disabled workflow", "old.yml", "main", nil, "not active"},
		{"a dynamic workflow", "3", "main", nil, "no workflow file"},
		{"a ref without the file", "release.yml", "other", map[string]string{"channel": "beta"}, "does not hold"},
	} {
		before := len(f.recorded())
		_, err := c.dispatchWorkflow(context.Background(), tt.workflow, tt.ref, tt.inputs)
		if err == nil || !strings.Contains(err.Error(), tt.detail) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.detail)
		}
		if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPost] != 0 {
			t.Errorf("%s: a dispatch was sent", tt.name)
		}
	}
}

// Re-runs need a completed run and a cancel one that has not completed; each change is one request to the
// run of the bound repository, and the next request of the token waits for the mutation interval.
func TestRunChangesAreCheckedAndSentOnce(t *testing.T) {
	f, _, base := serveActions(t)
	var (
		mu     sync.Mutex
		waited []time.Duration
	)
	limited := ratelimit.New(0, time.Now, func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waited = append(waited, d)
		return nil
	})
	red := &redact.Redactor{}
	c, err := open(resolvedConnection("gh", base, repoTarget), resolver(red, nil), red, limited)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		op, action, path string
		run              int64
	}{
		{"rerun", "rerun", "actions/runs/5000/rerun", failedRun},
		{"rerun failed", "rerun-failed-jobs", "actions/runs/5000/rerun-failed-jobs", failedRun},
		{"cancel", "cancel", "actions/runs/5001/cancel", runningRun},
	} {
		before := len(f.recorded())
		answer, err := c.changeRun(context.Background(), tt.op, tt.run, tt.action)
		if err != nil || answer.RunID != tt.run || !answer.Accepted {
			t.Fatalf("%s = %+v, %v", tt.op, answer, err)
		}
		requests := f.recorded()[before:]
		if len(requests) != 2 || requests[0].method != http.MethodGet || requests[1].method != http.MethodPost ||
			requests[1].path != actionsPrefix+tt.path {
			t.Errorf("%s requests = %+v, want the run read and one change", tt.op, requests)
		}
	}
	if len(waited) < 2 || waited[len(waited)-1] < mutationInterval/2 {
		t.Errorf("waits = %v, want the request after a change to wait", waited)
	}

	for _, tt := range []struct {
		action string
		run    int64
		detail string
	}{
		{"rerun", runningRun, "not completed"},
		{"rerun-failed-jobs", runningRun, "not completed"},
		{"cancel", failedRun, "already completed"},
	} {
		before := len(f.recorded())
		if _, err := c.changeRun(context.Background(), "change", tt.run, tt.action); err == nil ||
			!strings.Contains(err.Error(), tt.detail) {
			t.Errorf("%s of %d = %v, want %q", tt.action, tt.run, err, tt.detail)
		}
		if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPost] != 0 {
			t.Errorf("%s of %d was sent", tt.action, tt.run)
		}
	}
}

// An execution whose outcome is unclear is reported as such and never repeated; a refusal is clear and
// names what an Actions change needs without claiming what the token holds.
func TestUnclearExecutionsAreNeverRepeated(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter){
		"server error":    func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"gateway timeout": func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) },
		"dropped connection": func(w http.ResponseWriter) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, base := serveActions(t)
			route := f.failure
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodPost {
					answer(w)
					return true
				}
				return route(w, r)
			}
			c := client(t, base, repoTarget)
			calls := []func() error{
				func() error {
					_, err := c.dispatchWorkflow(context.Background(), "release.yml", "main",
						map[string]string{"channel": "beta"})
					return err
				},
				func() error { _, err := c.changeRun(context.Background(), "rerun", failedRun, "rerun"); return err },
				func() error { _, err := c.changeRun(context.Background(), "cancel", runningRun, "cancel"); return err },
			}
			for i, call := range calls {
				err := call()
				if err == nil || !strings.Contains(err.Error(), "may have been applied") {
					t.Errorf("call %d = %v, want an unclear outcome", i, err)
				}
				if _, _, rest := split(f.recorded()); rest[http.MethodPost] != i+1 {
					t.Errorf("call %d: POST requests = %d, want one per call", i, rest[http.MethodPost])
				}
			}
		})
	}

	f, _, base := serveActions(t)
	route := f.failure
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost || strings.HasSuffix(r.URL.Path, "/actions/runs") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
			return true
		}
		return route(w, r)
	}
	c := client(t, base, repoTarget)
	_, err := c.changeRun(context.Background(), "rerun", failedRun, "rerun")
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
		strings.Contains(err.Error(), "may have been applied") || !strings.Contains(err.Error(), "Actions: read and write") {
		t.Errorf("a refused re-run = %v, want a clear Actions permission refusal", err)
	}
	options := &actionsArguments{}
	if err := listCheck("runs")(options, c.target); err != nil {
		t.Fatal(err)
	}
	if _, err := c.listRuns(context.Background(), options); classOf(err) != provider.ClassPermission ||
		!strings.Contains(err.Error(), "Actions: read") {
		t.Errorf("a refused run list = %v, want an Actions read permission refusal", err)
	}
}

// Every Actions tool satisfies its output contract through the application core, and a confirmed
// execution writes one audit event without any provider content.
func TestActionsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	_, storage, base := serveActions(t)
	storage.log = logLines(20) + logCanary + "\n"
	red := &redact.Redactor{}
	var audit strings.Builder
	core := application.New(registry(t), actionsConfig(base), resolver(red, nil), red)
	core.SetAudit(&audit)

	for _, request := range []struct {
		operation, arguments string
		confirmed            bool
	}{
		{"github.workflows.list", `{}`, false},
		{"github.workflows.get", `{"workflow":"2"}`, false},
		{"github.workflowruns.list", `{"limit":2}`, false},
		{"github.workflowruns.get", `{"run_id":5001}`, false},
		{"github.workflowjobs.list", `{"run_id":5000,"filter":"all","limit":2}`, false},
		{"github.workflowjobs.get", `{"job_id":7001}`, false},
		{"github.workflowjobs.log", `{"job_id":7001}`, false},
		{"github.workflowartifacts.list", `{"run_id":5000}`, false},
		{"github.workflows.dispatch", `{"workflow":"release.yml","ref":"main","inputs":{"channel":"stable"}}`, true},
		{"github.workflowruns.rerun", `{"run_id":5000}`, true},
		{"github.workflowruns.rerunfailed", `{"run_id":5000}`, true},
		{"github.workflowruns.cancel", `{"run_id":5001}`, true},
	} {
		result, err := invoke(t, core, request.operation, "operator", request.arguments, request.confirmed)
		if err != nil {
			t.Errorf("%s %s = %v", request.operation, request.arguments, err)
			continue
		}
		if strings.Contains(string(result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.operation, result)
		}
	}
	if strings.Count(audit.String(), `"result":"success"`) != 4 || strings.Contains(audit.String(), logCanary) ||
		strings.Contains(audit.String(), "stable") {
		t.Errorf("audit = %s, want one content-free event per execution", audit.String())
	}
}
