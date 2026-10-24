package forge

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var runnerTools = []*tool{actionsRunnersList, actionsRunnersGet, actionsRunnersUpdate, actionsRunnersDelete,
	actionsRunnerJobsSearch, workflowsEnable, workflowsDisable}

func isUnknownTool(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown tool")
}

func TestRunnerToolsRegisterPerProductWithTheirRisk(t *testing.T) {
	reg := registry(t)
	giteaOnlyTools := []*tool{actionsRunnersUpdate, workflowsEnable, workflowsDisable}
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range runnerTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			wantOffered := !(p == forgejo && slices.Contains(giteaOnlyTools, tl)) &&
				!(p == gitea && tl == actionsRunnerJobsSearch)
			if ok != wantOffered {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			if d.RequiresToolAllowList != (tl == actionsRunnersDelete) {
				t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
			}
			if d.Risk.Effect != capability.EffectRead && (d.Risk.Confirmation != capability.ConfirmationRequired ||
				d.Risk.Idempotency != capability.IdempotencyIdempotent || !d.Risk.OpenWorld) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
		// Only Gitea knows the paused state and offers no paging; only Forgejo pages.
		d, _, _ := reg.Lookup(p.toolID(actionsRunnersList))
		if strings.Contains(string(d.InputSchema), `"disabled"`) != (p == gitea) ||
			strings.Contains(string(d.InputSchema), `"cursor"`) != (p == forgejo) {
			t.Errorf("%s list schema = %s", p.id, d.InputSchema)
		}
	}
}

const (
	forgejoRunner = `{"id":7,"name":"r1","status":"idle","ephemeral":false,"description":"d","uuid":"u-1",` +
		`"version":"v9","owner_id":0,"repo_id":5,"labels":["docker","linux"],"token":"leak-tok"}`
	giteaRunner = `{"id":7,"name":"r1","status":"idle","ephemeral":false,"busy":true,"disabled":false,` +
		`"labels":[{"id":1,"name":"docker","type":"custom"}],"token":"leak-tok"}`
)

func runnerFixture(p *product) string {
	if p == gitea {
		return giteaRunner
	}
	return forgejoRunner
}

func TestRunnersListAndGetProjectTheMembersOfTheProduct(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/runners/7") {
				return jsonResponse(200, runnerFixture(p))
			}
			if p == gitea {
				return jsonResponse(200, `{"runners":[`+giteaRunner+`,null],"total_count":1}`)
			}
			return jsonResponse(200, `[`+forgejoRunner+`,null]`, "X-Total-Count", "31")
		})
		arguments := `{"limit":30}`
		if p == gitea {
			arguments = `{"disabled":false}`
		}
		list, err := e.call(t, actionsRunnersList, "admin-repo", arguments, false)
		rows := rowsOf(t, list, "runners")
		if err != nil || len(rows) != 1 || list["repository"] != "acme/widget" {
			t.Fatalf("%s list: %v, %v", p.id, list, err)
		}
		get, err := e.call(t, actionsRunnersGet, "admin-repo", `{"runner_id":7}`, false)
		if err != nil || !reflect.DeepEqual(get, rows[0]) {
			t.Fatalf("%s get: %v, %v", p.id, get, err)
		}
		if _, leaked := get["token"]; leaked || strings.Contains(strings.Join(e.raw, ""), "leak-tok") {
			t.Errorf("%s: the runner answer carries a token", p.id)
		}
		_, forgejoMember := get["uuid"]
		_, giteaMember := get["busy"]
		if forgejoMember != (p == forgejo) || giteaMember != (p == gitea) {
			t.Errorf("%s members = %v", p.id, get)
		}
		c := apiCalls(e.environment)
		if p == forgejo {
			if list["total_count"] != float64(31) || list["has_more"] != true ||
				queryOf(t, c[0]).Get("limit") != "30" || queryOf(t, c[0]).Get("page") != "1" {
				t.Errorf("%s paging: %v, %+v", p.id, list, c[0])
			}
		} else if queryOf(t, c[0]).Get("disabled") != "false" || queryOf(t, c[0]).Has("page") || list["has_more"] != false {
			t.Errorf("gitea query = %q", c[0].query)
		}
		if c[0].path != actionsBase+"/runners" || c[1].path != actionsBase+"/runners/7" {
			t.Errorf("%s paths = %s, %s", p.id, c[0].path, c[1].path)
		}
		// An answer for another runner is refused.
		other := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, strings.Replace(runnerFixture(p), `"id":7`, `"id":8`, 1))
		})
		if _, err := other.call(t, actionsRunnersGet, "admin-repo", `{"runner_id":7}`, false); err == nil {
			t.Errorf("%s: a foreign runner was returned", p.id)
		}
	}
}

func TestRunnersCapTheUnpagedListOfGitea(t *testing.T) {
	var rows []string
	for i := 0; i < maxRunnerRows+5; i++ {
		rows = append(rows, `{"id":1}`)
	}
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response {
		return jsonResponse(200, `{"runners":[`+strings.Join(rows, ",")+`],"total_count":105}`)
	})
	result, err := e.call(t, actionsRunnersList, "admin-repo", `{}`, false)
	if err != nil || len(rowsOf(t, result, "runners")) != maxRunnerRows || result["has_more"] != true ||
		result["total_count"] != float64(105) {
		t.Errorf("capped list: %v", err)
	}
}

func TestRunnersRefuseBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tc := range []struct {
			tl                    *tool
			connection, arguments string
		}{
			{actionsRunnersGet, "admin-repo", `{"owner":"orgs/acme","runner_id":7}`},
			{actionsRunnersGet, "admin-repo", `{"repository":"acme/widget","owner":"orgs/acme","runner_id":7}`},
			{actionsRunnersGet, "admin-org", `{"owner":"users/alice","runner_id":7}`},
			{actionsRunnersGet, "admin-org", `{"owner":"orgs/other","runner_id":7}`},
			{actionsRunnersGet, "admin-repo", `{"runner_id":0}`},
			{actionsRunnersDelete, "admin-repo", `{"repository":"acme/other","runner_id":7}`},
			{actionsRunnersDelete, "admin-repo", `{"owner":"orgs/acme","runner_id":7}`},
			{actionsRunnersList, "admin-repo", `{"disabled":true,"limit":5}`},
		} {
			before, reads := len(*e.calls), *e.reads
			if _, err := e.call(t, tc.tl, tc.connection, tc.arguments, true); err == nil || len(*e.calls) != before ||
				*e.reads != reads || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s %s: %v after %d calls", p.id, tc.tl.action, tc.arguments, err, len(*e.calls)-before)
			}
		}
	}
}

func TestRunnersOnTheOwnerLevels(t *testing.T) {
	for _, p := range products {
		for _, login := range []string{"alice", "bob"} {
			e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
				if c.path == "/api/v1/user" {
					return jsonResponse(200, `{"id":3,"login":"`+login+`"}`)
				}
				return jsonResponse(204, ``)
			})
			result, err := e.call(t, actionsRunnersDelete, "admin-user", `{"owner":"users/Alice","runner_id":7}`, true)
			if login == "bob" {
				if !isInvalidRequest(err) || !reflect.DeepEqual(apiMethods(e.environment), []string{"GET /api/v1/user"}) ||
					strings.Contains(err.Error(), "bob") {
					t.Errorf("%s foreign login: %v, %v", p.id, err, apiMethods(e.environment))
				}
				continue
			}
			if err != nil || result["owner"] != "users/Alice" || result["deleted"] != true ||
				!reflect.DeepEqual(apiMethods(e.environment), []string{"GET /api/v1/user",
					"DELETE /api/v1/user/actions/runners/7"}) {
				t.Errorf("%s user: %v, %v, %v", p.id, result, err, apiMethods(e.environment))
			}
		}
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		for _, connection := range []string{"admin-org", "admin-open"} {
			if _, err := e.call(t, actionsRunnersDelete, connection, `{"owner":"orgs/acme","runner_id":7}`, true); err != nil {
				t.Errorf("%s %s: %v", p.id, connection, err)
			}
		}
		if got := apiMethods(e.environment); !reflect.DeepEqual(got, []string{"DELETE /api/v1/orgs/acme/actions/runners/7",
			"DELETE /api/v1/orgs/acme/actions/runners/7"}) {
			t.Errorf("%s org calls = %v", p.id, got)
		}
	}
}

func TestRunnersUpdateSendsOnlyDisabledOnGitea(t *testing.T) {
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response {
		return jsonResponse(200, giteaRunner)
	})
	result, err := e.call(t, actionsRunnersUpdate, "admin-repo", `{"runner_id":7,"disabled":true}`, true)
	calls := apiCalls(e.environment)
	if err != nil || result["runner_id"] != float64(7) || len(calls) != 1 || calls[0].method != "PATCH" ||
		calls[0].path != actionsBase+"/runners/7" || calls[0].body != `{"disabled":true}` {
		t.Fatalf("update: %v, %v, %+v", result, err, calls)
	}
	for _, arguments := range []string{`{"runner_id":7}`, `{"runner_id":7,"disabled":true,"name":"x"}`} {
		before := len(*e.calls)
		if _, err := e.call(t, actionsRunnersUpdate, "admin-repo", arguments, true); err == nil || len(*e.calls) != before {
			t.Errorf("update %s: %v", arguments, err)
		}
	}
	var confirm *application.ConfirmationRequiredError
	if _, err := e.call(t, actionsRunnersUpdate, "admin-repo", `{"runner_id":7,"disabled":true}`, false); !errors.As(err, &confirm) {
		t.Errorf("unconfirmed update: %v", err)
	}
	// Forgejo has no such tool.
	f := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(200, `{}`) })
	if _, err := f.call(t, actionsRunnersUpdate, "admin-repo", `{"runner_id":7,"disabled":true}`, true); !isUnknownTool(err) ||
		len(*f.calls) != 0 {
		t.Errorf("forgejo update: %v", err)
	}
}

func TestRunnerChangesAreNeverRepeatedAfterAnUnclearOutcome(t *testing.T) {
	for _, p := range products {
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{actionsRunnersDelete, `{"runner_id":7}`}, {actionsRunnersUpdate, `{"runner_id":7,"disabled":true}`},
			{workflowsEnable, `{"workflow_id":"build.yml"}`}, {workflowsDisable, `{"workflow_id":"build.yml"}`}} {
			if _, ok := tc.tl.since[p.id]; !ok {
				continue
			}
			before := len(apiCalls(f.environment))
			_, err := f.call(t, tc.tl, "admin-repo", tc.arguments, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				strings.Contains(err.Error(), bodyCanary) || len(apiCalls(f.environment)) != before+1 {
				t.Errorf("%s %s after 500: %v", p.id, tc.tl.action, err)
			}
		}
	}
}

func TestRunnerDeleteIsListedOnly(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, forgejoAt("17.0.0", nil))
		var unsupported *capability.UnsupportedError
		if _, err := e.invoke(t, actionsRunnersDelete, "open", `{"runner_id":7}`); !errors.As(err, &unsupported) ||
			len(*e.calls) != 0 {
			t.Errorf("%s without a tools list: %v", p.id, err)
		}
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := f.call(t, actionsRunnersDelete, "admin-repo", `{"runner_id":7}`, false); !errors.As(err, &confirm) ||
			len(*f.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
	}
}

func TestRunnerJobsSearchSendsValidatedLabelsOnForgejo(t *testing.T) {
	jobs := `[{"id":9,"name":"build","status":"waiting","runs_on":["docker"],"repo_id":5,"handle":"h"},null]`
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(200, jobs) })
	result, err := e.call(t, actionsRunnerJobsSearch, "admin-repo", `{"labels":["docker","ubuntu-latest"]}`, false)
	rows := rowsOf(t, result, "jobs")
	c := apiCalls(e.environment)
	if err != nil || len(rows) != 1 || rows[0]["id"] != float64(9) || result["has_more"] != false ||
		c[0].path != actionsBase+"/runners/jobs" || queryOf(t, c[0]).Get("labels") != "docker,ubuntu-latest" {
		t.Fatalf("search: %v, %v, %+v", result, err, c)
	}
	for _, arguments := range []string{`{"labels":["a,b"]}`, `{"labels":[""]}`, `{"labels":["a b"]}`,
		`{"labels":["` + strings.Repeat("a", 101) + `"]}`, `{"labels":"docker"}`,
		`{"labels":["1","2","3","4","5","6","7","8","9","10","11","12","13","14","15","16","17","18","19","20","21"]}`} {
		before := len(*e.calls)
		if _, err := e.call(t, actionsRunnerJobsSearch, "admin-repo", arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("search %s: %v", arguments, err)
		}
	}
	if _, err := e.call(t, actionsRunnerJobsSearch, "admin-org", `{"owner":"orgs/acme"}`, false); err != nil ||
		apiCalls(e.environment)[len(apiCalls(e.environment))-1].path != "/api/v1/orgs/acme/actions/runners/jobs" ||
		apiCalls(e.environment)[len(apiCalls(e.environment))-1].query != "" {
		t.Errorf("org search: %v", err)
	}
	g := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response { return jsonResponse(200, `[]`) })
	if _, err := g.call(t, actionsRunnerJobsSearch, "admin-repo", `{}`, false); !isUnknownTool(err) {
		t.Errorf("gitea search: %v", err)
	}
}

func TestWorkflowsEnableAndDisableSendOnePutOnGitea(t *testing.T) {
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response { return jsonResponse(204, ``) })
	for _, tl := range []*tool{workflowsEnable, workflowsDisable} {
		result, err := e.call(t, tl, "admin-repo", `{"workflow_id":"build.yml"}`, true)
		if err != nil || result["accepted"] != true || result["workflow_id"] != "build.yml" ||
			result["repository"] != "acme/widget" {
			t.Errorf("%s: %v, %v", tl.action, result, err)
		}
	}
	if got := apiMethods(e.environment); !reflect.DeepEqual(got, []string{"PUT " + actionsBase + "/workflows/build.yml/enable",
		"PUT " + actionsBase + "/workflows/build.yml/disable"}) {
		t.Errorf("calls = %v", got)
	}
	for _, arguments := range []string{`{"workflow_id":"../x.yml"}`, `{"workflow_id":"build.txt"}`, `{"workflow_id":"a/b.yml"}`,
		`{"workflow_id":"build.yml","owner":"orgs/acme"}`, `{"repository":"acme/other","workflow_id":"build.yml"}`, `{}`} {
		before := len(*e.calls)
		if _, err := e.call(t, workflowsEnable, "admin-repo", arguments, true); err == nil || len(*e.calls) != before {
			t.Errorf("enable %s: %v", arguments, err)
		}
	}
	var confirm *application.ConfirmationRequiredError
	if _, err := e.call(t, workflowsDisable, "admin-repo", `{"workflow_id":"build.yml"}`, false); !errors.As(err, &confirm) {
		t.Errorf("unconfirmed: %v", err)
	}
	f := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(204, ``) })
	for _, tl := range []*tool{workflowsEnable, workflowsDisable} {
		if _, err := f.call(t, tl, "admin-repo", `{"workflow_id":"build.yml"}`, true); !isUnknownTool(err) {
			t.Errorf("forgejo %s: %v", tl.action, err)
		}
	}
	if len(*f.calls) != 0 {
		t.Errorf("forgejo calls = %d", len(*f.calls))
	}
}
