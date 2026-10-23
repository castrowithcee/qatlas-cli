package forge

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const giteaWorkflow = `{"id":"build.yml","name":"Build","path":".gitea/workflows/build.yml","state":"active","html_url":"https://forge.example.test/acme/widget/actions/workflows/build.yml","url":"https://forge.example.test/api/v1/repos/acme/widget/actions/workflows/build.yml","badge_url":"https://forge.example.test/acme/widget/actions/workflows/build.yml/badge.svg","created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z","deleted_at":"2030-01-03T00:00:00Z"}`

func TestWorkflowsAreGiteaOnly(t *testing.T) {
	reg := registry(t)
	for _, tool := range []*tool{workflowsList, workflowsGet} {
		if _, _, ok := reg.Lookup(forgejo.toolID(tool)); ok {
			t.Errorf("%s is registered for forgejo", forgejo.toolID(tool))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(tool)); !ok {
			t.Errorf("%s is not registered for gitea", gitea.toolID(tool))
		}
	}
	if _, _, ok := reg.Lookup(forgejo.toolID(workflowsDispatch)); !ok {
		t.Error("dispatch is not registered for forgejo")
	}
}

func TestWorkflowsListAndGetShowEveryMember(t *testing.T) {
	e, _ := newActionsEnvironment(t, gitea, "1.27.3", func(c call) *http.Response {
		if strings.HasSuffix(c.path, "/workflows") {
			return jsonResponse(200, `{"total_count":1,"workflows":[`+giteaWorkflow+`]}`)
		}
		return jsonResponse(200, giteaWorkflow)
	})
	result, err := e.invoke(t, workflowsList, "repo", `{}`)
	rows := rowsOf(t, result, "workflows")
	if err != nil || len(rows) != 1 || result["total_count"] != float64(1) || result["has_more"] != false {
		t.Fatalf("list: %v, %v", result, err)
	}
	for _, key := range []string{"id", "name", "path", "state", "html_url", "url", "badge_url", "created_at", "updated_at",
		"deleted_at"} {
		if _, ok := rows[0][key]; !ok {
			t.Errorf("list: member %s is missing: %v", key, rows[0])
		}
	}
	got, err := e.invoke(t, workflowsGet, "repo", `{"workflow_id":"build.yml"}`)
	if err != nil || got["id"] != "build.yml" || got["state"] != "active" {
		t.Fatalf("get: %v, %v", got, err)
	}
	want := []string{"GET " + actionsBase + "/workflows", "GET " + actionsBase + "/workflows/build.yml"}
	if !reflect.DeepEqual(apiMethods(e), want) {
		t.Errorf("calls = %v", apiMethods(e))
	}
	reads := *e.reads
	for _, arguments := range []string{`{}`, `{"workflow_id":"../x.yml"}`, `{"workflow_id":"a/b.yml"}`, `{"workflow_id":"a..b.yml"}`,
		`{"workflow_id":"build"}`, `{"workflow_id":"build.yml","repository":"acme/other"}`} {
		if _, err := e.invoke(t, workflowsGet, "repo", arguments); !isInvalidRequest(err) || *e.reads != reads {
			t.Errorf("%s: %v", arguments, err)
		}
	}
	// An answer for another workflow is refused.
	other := strings.ReplaceAll(giteaWorkflow, "build.yml", "deploy.yml")
	f, _ := newActionsEnvironment(t, gitea, "1.27.3", func(call) *http.Response { return jsonResponse(200, other) })
	if _, err := f.invoke(t, workflowsGet, "repo", `{"workflow_id":"build.yml"}`); err == nil ||
		!strings.Contains(err.Error(), "other than the requested") {
		t.Errorf("other workflow: %v", err)
	}
}

func TestWorkflowDispatchSetsTheFieldOfItsProduct(t *testing.T) {
	const arguments = `{"workflow":"release.yml","ref":"refs/heads/main","inputs":{"channel":"beta"}}`
	path := actionsBase + "/workflows/release.yml/dispatches"
	for _, p := range products {
		answer := `{"id":77,"run_number":9,"jobs":["build","test"]}`
		status := 201
		if p == gitea {
			answer, status = `{"workflow_run_id":77,"run_url":"https://forge.example.test/api/v1/repos/acme/widget/actions/runs/77","html_url":"https://forge.example.test/acme/widget/actions/runs/9"}`, 200
		}
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(status, answer) })
		d, _, _ := registry(t).Lookup(p.toolID(workflowsDispatch))
		if d.Risk.Effect != "execute" || d.Risk.Confirmation != "required" || d.Risk.Idempotency != "non_idempotent" ||
			d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		var confirmRequired bool
		if _, err := e.invokeAs(t, workflowsDispatch, "repo", arguments, false); err != nil {
			confirmRequired = true
		}
		if !confirmRequired || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: unconfirmed: calls %d", d.ID, len(*e.calls))
		}
		result, err := e.invoke(t, workflowsDispatch, "repo", arguments)
		started, _ := result["run"].(map[string]any)
		if err != nil || result["accepted"] != true || started["run_id"] != float64(77) {
			t.Fatalf("%s: %v, %v", d.ID, result, err)
		}
		if p == forgejo && (started["run_number"] != float64(9) || len(started["jobs"].([]any)) != 2) {
			t.Errorf("forgejo run = %v", started)
		}
		if p == gitea && (started["run_url"] == nil || started["html_url"] == nil) {
			t.Errorf("gitea run = %v", started)
		}
		if got := apiMethods(e); !reflect.DeepEqual(got, []string{"POST " + path}) {
			t.Fatalf("%s calls = %v", d.ID, got)
		}
		c := apiCalls(e)[0]
		var body map[string]any
		if err := json.Unmarshal([]byte(c.body), &body); err != nil {
			t.Fatal(err)
		}
		query := queryOf(t, c)
		if body["ref"] != "refs/heads/main" || !reflect.DeepEqual(body["inputs"], map[string]any{"channel": "beta"}) {
			t.Errorf("%s body = %v", d.ID, body)
		}
		if p == forgejo && (body["return_run_info"] != true || query.Has("return_run_details")) {
			t.Errorf("forgejo: body %v, query %v", body, query)
		}
		if p == gitea && (query.Get("return_run_details") != "true" || body["return_run_info"] != nil) {
			t.Errorf("gitea: body %v, query %v", body, query)
		}
		// Without the run the request carries neither field and an empty answer is accepted.
		n, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		result, err = n.invoke(t, workflowsDispatch, "repo", `{"workflow":"release.yml","ref":"main","return_run":false}`)
		if err != nil || result["accepted"] != true || result["run"] != nil {
			t.Fatalf("%s without run: %v, %v", d.ID, result, err)
		}
		c = apiCalls(n)[0]
		if strings.Contains(c.body, "return_run_info") || c.query != "" || strings.Contains(c.body, "inputs") {
			t.Errorf("%s without run: %+v", d.ID, c)
		}
		// An answer of 204 although the run was asked for is no failure.
		m, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		if result, err := m.invoke(t, workflowsDispatch, "repo", `{"workflow":"release.yml","ref":"main"}`); err != nil ||
			result["accepted"] != true {
			t.Errorf("%s 204: %v, %v", d.ID, result, err)
		}
		// An outcome that stays open is reported and the request is not repeated.
		for _, failure := range []int{500, 503} {
			f, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
				return jsonResponse(failure, `{"message":"`+bodyCanary+`"}`)
			})
			if _, err := f.invoke(t, workflowsDispatch, "repo", arguments); err == nil ||
				!strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
				len(apiCalls(f)) != 1 {
				t.Errorf("%s %d: %v, calls %v", d.ID, failure, err, apiMethods(f))
			}
		}
	}
}

func TestWorkflowDispatchChecksItsArgumentsBeforeTheSecret(t *testing.T) {
	long := strings.Repeat("x", maxDispatchInputLength+1)
	many := map[string]string{}
	for i := 0; i < maxDispatchInputs+1; i++ {
		many["in"+itoa(i)] = "v"
	}
	manyJSON, _ := json.Marshal(many)
	bad := []string{`{"ref":"main"}`, `{"workflow":"release.yml"}`, `{"workflow":"../release.yml","ref":"main"}`,
		`{"workflow":"a/release.yml","ref":"main"}`, `{"workflow":"release.yml.lock","ref":"main"}`,
		`{"workflow":"a..b.yml","ref":"main"}`, `{"workflow":"release.yml","ref":"a..b"}`,
		`{"workflow":"release.yml","ref":"-x"}`, `{"workflow":"release.yml","ref":"main","inputs":{"n":1}}`,
		`{"workflow":"release.yml","ref":"main","inputs":{"bad name":"v"}}`,
		`{"workflow":"release.yml","ref":"main","inputs":{"n":"` + long + `"}}`,
		`{"workflow":"release.yml","ref":"main","inputs":{"n":"a\u0000b"}}`,
		`{"workflow":"release.yml","ref":"main","inputs":` + string(manyJSON) + `}`,
		`{"workflow":"release.yml","ref":"main","repository":"acme/other"}`}
	for _, p := range products {
		e, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return notFoundResponse() })
		for _, arguments := range bad {
			if _, err := e.invoke(t, workflowsDispatch, "repo", arguments); err == nil || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %.80s: %v", p.id, arguments, err)
			}
		}
		// Multi-line input values are fine.
		ok, _ := newActionsEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		if _, err := ok.invoke(t, workflowsDispatch, "repo", `{"workflow":"release.yaml","ref":"v1.0","inputs":{"notes":"a\nb"}}`); err != nil {
			t.Errorf("%s multi-line: %v", p.id, err)
		}
	}
}

func TestWorkflowDispatchIsAvailableEverywhere(t *testing.T) {
	for _, in := range []struct {
		p *product
		v string
	}{{forgejo, "15.0.10"}, {forgejo, "16.0.6"}, {forgejo, "17.0.0"}, {gitea, "1.27.3"}, {gitea, "28.2.0"}} {
		e, _ := newActionsEnvironment(t, in.p, in.v, func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		if _, err := e.invoke(t, workflowsDispatch, "repo", `{"workflow":"release.yml","ref":"main"}`); err != nil {
			t.Errorf("%s %s: %v", in.p.id, in.v, err)
		}
	}
}

func TestActionsProfilesSeparateObserverAndOperator(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	reads := []string{"workflow-runs.list", "workflow-runs.get", "workflow-run-logs.get", "workflow-jobs.list",
		"workflow-jobs.get", "workflow-job-logs.get", "actions-tasks.list", "workflow-artifacts.list",
		"workflow-artifacts.get", "workflow-artifacts.download"}
	executions := []string{"workflow-runs.cancel", "workflow-runs.rerun", "workflow-jobs.rerun", "workflows.dispatch"}
	giteaReads := []string{"workflow-run-attempts.get", "workflow-run-attempts.list-jobs", "workflows.list", "workflows.get"}
	giteaExecutions := []string{"workflow-runs.force-cancel", "workflow-runs.rerun-failed", "workflow-runs.approve"}
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		profiles := map[string]config.ToolProfile{}
		for _, profile := range metadata.Profiles {
			profiles[profile.ID] = profile
		}
		observer, operator, read := profiles["actions-observer"], profiles["actions-operator"], profiles["read"]
		if observer.Recommended || operator.Recommended || !read.Recommended || len(read.Tools) != 5 {
			t.Errorf("%s: profiles = %+v", p.id, profiles)
		}
		wantObserver, wantOperator := reads, executions
		if p == gitea {
			wantObserver = append(slices.Clone(reads), giteaReads...)
			wantOperator = append(slices.Clone(executions), giteaExecutions...)
		}
		ids := func(names []string) []string {
			var out []string
			for _, name := range names {
				out = append(out, p.id+"."+name)
			}
			slices.Sort(out)
			return out
		}
		gotObserver, gotOperator := slices.Clone(observer.Tools), slices.Clone(operator.Tools)
		slices.Sort(gotObserver)
		slices.Sort(gotOperator)
		if !slices.Equal(gotObserver, ids(wantObserver)) {
			t.Errorf("%s observer = %v, want %v", p.id, gotObserver, ids(wantObserver))
		}
		if !slices.Equal(gotOperator, ids(append(slices.Clone(wantObserver), wantOperator...))) {
			t.Errorf("%s operator = %v", p.id, gotOperator)
		}
		if got := metadata.ProfileEffects(observer); len(got) != 1 || got[0] != config.EffectRead {
			t.Errorf("%s observer effects = %v", p.id, got)
		}
		if got := metadata.ProfileEffects(operator); len(got) != 2 || got[1] != config.EffectExecute {
			t.Errorf("%s operator effects = %v", p.id, got)
		}
		// No profile holds a tool a connection offers only with a tools list.
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if d, _, ok := reg.Lookup(id); !ok || d.RequiresToolAllowList {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
	}
}
