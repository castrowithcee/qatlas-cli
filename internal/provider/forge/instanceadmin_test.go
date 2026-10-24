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

var instanceTools = []*tool{adminRunnersList, adminRunnersGet, adminRunnersUpdate, adminRunnersDelete,
	adminRunnerJobsSearch, adminWorkflowRunsList, adminWorkflowJobsList, adminPackagesList}

func TestInstanceToolsRegisterPerProductWithTheirRisk(t *testing.T) {
	reg := registry(t)
	forgejoTools := []*tool{adminRunnersList, adminRunnersGet, adminRunnersDelete, adminRunnerJobsSearch}
	giteaTools := []*tool{adminRunnersList, adminRunnersGet, adminRunnersUpdate, adminRunnersDelete,
		adminWorkflowRunsList, adminWorkflowJobsList, adminPackagesList}
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		want := forgejoTools
		if p == gitea {
			want = giteaTools
		}
		for _, tl := range instanceTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if ok != slices.Contains(want, tl) {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			if d.RequiresToolAllowList != (tl == adminRunnersDelete) {
				t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
			}
			if (tl == adminRunnersDelete) != (d.Risk.Effect == capability.EffectDelete) {
				t.Errorf("%s effect = %s", d.ID, d.Risk.Effect)
			}
			if d.Risk.Effect != capability.EffectRead && (d.Risk.Confirmation != capability.ConfirmationRequired ||
				d.Risk.Idempotency != capability.IdempotencyIdempotent || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "") {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func TestInstanceToolsRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range instanceTools {
			if _, ok := tl.since[p.id]; !ok {
				continue
			}
			arguments := `{}`
			if tl == adminRunnersGet || tl == adminRunnersUpdate || tl == adminRunnersDelete {
				arguments = `{"runner_id":7,"disabled":true}`
				if tl != adminRunnersUpdate {
					arguments = `{"runner_id":7}`
				}
			}
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, arguments, true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads {
					t.Errorf("%s %s on %s: %v after %d calls", p.id, tl.group+"."+tl.action, connection, err, len(*e.calls))
				}
			}
		}
	}
}

const giteaRunnersBody = `{"runners":[` + giteaRunner + `,null],"total_count":1}`

func TestAdminRunnersListGetAndUpdate(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/runners/7") {
				return jsonResponse(200, runnerFixture(p))
			}
			if p == gitea {
				return jsonResponse(200, giteaRunnersBody)
			}
			return jsonResponse(200, `[`+forgejoRunner+`,null]`, "X-Total-Count", "31")
		})
		arguments := `{"visible":true,"limit":20}`
		if p == gitea {
			arguments = `{"disabled":false}`
		}
		list, err := e.call(t, adminRunnersList, "admin-open", arguments, false)
		rows := rowsOf(t, list, "runners")
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s list: %v, %v", p.id, list, err)
		}
		get, err := e.call(t, adminRunnersGet, "admin-open", `{"runner_id":7}`, false)
		if err != nil || !reflect.DeepEqual(get, rows[0]) {
			t.Fatalf("%s get: %v, %v", p.id, get, err)
		}
		if _, leaked := get["token"]; leaked || strings.Contains(strings.Join(e.raw, ""), "leak-tok") {
			t.Errorf("%s: the runner answer carries a token", p.id)
		}
		c := apiCalls(e.environment)
		if c[0].path != "/api/v1/admin/actions/runners" || c[1].path != "/api/v1/admin/actions/runners/7" {
			t.Errorf("%s paths = %s, %s", p.id, c[0].path, c[1].path)
		}
		if p == forgejo {
			q := queryOf(t, c[0])
			if list["total_count"] != float64(31) || list["has_more"] != true || q.Get("visible") != "true" ||
				q.Get("limit") != "20" || q.Get("page") != "1" {
				t.Errorf("forgejo paging: %v, %+v", list, c[0])
			}
		} else if queryOf(t, c[0]).Get("disabled") != "false" || queryOf(t, c[0]).Has("page") || list["has_more"] != false {
			t.Errorf("gitea query = %q", c[0].query)
		}
		// An answer for another runner is refused.
		other := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, strings.Replace(runnerFixture(p), `"id":7`, `"id":8`, 1))
		})
		if _, err := other.call(t, adminRunnersGet, "admin-open", `{"runner_id":7}`, false); err == nil {
			t.Errorf("%s: a foreign runner was returned", p.id)
		}
		// The product-specific list arguments are refused on the other product before any request.
		wrong := `{"disabled":true}`
		if p == gitea {
			wrong = `{"visible":true}`
		}
		before := len(*e.calls)
		if _, err := e.call(t, adminRunnersList, "admin-open", wrong, false); err == nil || len(*e.calls) != before {
			t.Errorf("%s list %s: %v", p.id, wrong, err)
		}
	}

	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response { return jsonResponse(200, giteaRunner) })
	result, err := e.call(t, adminRunnersUpdate, "admin-open", `{"runner_id":7,"disabled":true}`, true)
	calls := apiCalls(e.environment)
	if err != nil || result["runner_id"] != float64(7) || len(calls) != 1 || calls[0].method != "PATCH" ||
		calls[0].path != "/api/v1/admin/actions/runners/7" || calls[0].body != `{"disabled":true}` {
		t.Fatalf("update: %v, %v, %+v", result, err, calls)
	}
	for _, arguments := range []string{`{"runner_id":7}`, `{"runner_id":7,"disabled":true,"name":"x"}`, `{"disabled":true}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminRunnersUpdate, "admin-open", arguments, true); err == nil || len(*e.calls) != before {
			t.Errorf("update %s: %v", arguments, err)
		}
	}
	var confirm *application.ConfirmationRequiredError
	before := len(*e.calls)
	if _, err := e.call(t, adminRunnersUpdate, "admin-open", `{"runner_id":7,"disabled":true}`, false); !errors.As(err, &confirm) ||
		len(*e.calls) != before {
		t.Errorf("unconfirmed update: %v", err)
	}
	f := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(200, `{}`) })
	if _, err := f.call(t, adminRunnersUpdate, "admin-open", `{"runner_id":7,"disabled":true}`, true); !isUnknownTool(err) ||
		len(*f.calls) != 0 {
		t.Errorf("forgejo update: %v", err)
	}
}

func TestAdminRunnersCapTheUnpagedListOfGitea(t *testing.T) {
	var rows []string
	for i := 0; i < maxRunnerRows+5; i++ {
		rows = append(rows, `{"id":1}`)
	}
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response {
		return jsonResponse(200, `{"runners":[`+strings.Join(rows, ",")+`],"total_count":105}`)
	})
	result, err := e.call(t, adminRunnersList, "admin-open", `{}`, false)
	if err != nil || len(rowsOf(t, result, "runners")) != maxRunnerRows || result["has_more"] != true ||
		result["total_count"] != float64(105) {
		t.Errorf("capped list: %v", err)
	}
}

func TestAdminRunnerDeleteIsListedOnlyAndSendsOneRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, instance(defaultVersion(p), defaultVersion(p), nil))
		var unsupported *capability.UnsupportedError
		if _, err := e.invoke(t, adminRunnersDelete, "open", `{"runner_id":7}`); !errors.As(err, &unsupported) ||
			len(*e.calls) != 0 {
			t.Errorf("%s without a tools list: %v", p.id, err)
		}
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := f.call(t, adminRunnersDelete, "admin-open", `{"runner_id":7}`, false); !errors.As(err, &confirm) ||
			len(*f.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
		result, err := f.call(t, adminRunnersDelete, "admin-open", `{"runner_id":7}`, true)
		if err != nil || result["deleted"] != true || !reflect.DeepEqual(apiMethods(f.environment),
			[]string{"DELETE /api/v1/admin/actions/runners/7"}) || apiCalls(f.environment)[0].body != "" {
			t.Errorf("%s delete: %v, %v, %v", p.id, result, err, apiMethods(f.environment))
		}
	}
}

func TestAdminRunnerChangesAreNeverRepeatedAfterAnUnclearOutcome(t *testing.T) {
	for _, p := range products {
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{adminRunnersDelete, `{"runner_id":7}`}, {adminRunnersUpdate, `{"runner_id":7,"disabled":true}`}} {
			if _, ok := tc.tl.since[p.id]; !ok {
				continue
			}
			before := len(apiCalls(f.environment))
			_, err := f.call(t, tc.tl, "admin-open", tc.arguments, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				strings.Contains(err.Error(), bodyCanary) || len(apiCalls(f.environment)) != before+1 {
				t.Errorf("%s %s after 500: %v", p.id, tc.tl.action, err)
			}
		}
	}
}

func TestAdminRunnerJobsSearchSendsValidatedLabelsOnForgejo(t *testing.T) {
	jobs := `[{"id":9,"name":"build","status":"waiting","runs_on":["docker"],"repo_id":5,"handle":"h"},null]`
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(200, jobs) })
	result, err := e.call(t, adminRunnerJobsSearch, "admin-open", `{"labels":["docker","ubuntu-latest"]}`, false)
	rows := rowsOf(t, result, "jobs")
	c := apiCalls(e.environment)
	if err != nil || len(rows) != 1 || rows[0]["id"] != float64(9) || result["has_more"] != false ||
		c[0].path != "/api/v1/admin/actions/runners/jobs" || queryOf(t, c[0]).Get("labels") != "docker,ubuntu-latest" {
		t.Fatalf("search: %v, %v, %+v", result, err, c)
	}
	for _, arguments := range []string{`{"labels":["a,b"]}`, `{"labels":[""]}`, `{"labels":["a b"]}`, `{"labels":"docker"}`,
		`{"labels":["` + strings.Repeat("a", 101) + `"]}`, `{"repository":"acme/widget"}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminRunnerJobsSearch, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("search %s: %v", arguments, err)
		}
	}
	g := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response { return jsonResponse(200, `[]`) })
	if _, err := g.call(t, adminRunnerJobsSearch, "admin-open", `{}`, false); !isUnknownTool(err) {
		t.Errorf("gitea search: %v", err)
	}
}

func TestAdminWorkflowRunsAndJobsListOnGitea(t *testing.T) {
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(c call) *http.Response {
		if strings.HasSuffix(c.path, "/runs") {
			return jsonResponse(200, `{"workflow_runs":[{"id":3,"display_title":"t","head_sha":"abc1234","status":"success"},null],"total_count":9}`,
				"Link", `<https://x/?page=2>; rel="next"`)
		}
		return jsonResponse(200, `{"jobs":[{"id":4,"name":"build","run_attempt":2,"status":"queued"}],"total_count":1}`)
	})
	runs, err := e.call(t, adminWorkflowRunsList, "admin-open",
		`{"event":"push","branch":"main","status":"success","actor":"alice","head_sha":"abc1234","limit":10}`, false)
	rows := rowsOf(t, runs, "workflow_runs")
	c := apiCalls(e.environment)
	if err != nil || len(rows) != 1 || rows[0]["title"] != "t" || runs["has_more"] != true || runs["next_cursor"] == nil ||
		runs["total_count"] != float64(9) || c[0].path != "/api/v1/admin/actions/runs" {
		t.Fatalf("runs: %v, %v, %+v", runs, err, c)
	}
	q := queryOf(t, c[0])
	if q.Get("event") != "push" || q.Get("branch") != "main" || q.Get("status") != "success" || q.Get("actor") != "alice" ||
		q.Get("head_sha") != "abc1234" || q.Get("limit") != "10" || q.Get("page") != "1" {
		t.Errorf("runs query = %q", c[0].query)
	}
	jobs, err := e.call(t, adminWorkflowJobsList, "admin-open", `{"status":"queued","sort":"id","order":"desc"}`, false)
	c = apiCalls(e.environment)
	if err != nil || len(rowsOf(t, jobs, "jobs")) != 1 || rowsOf(t, jobs, "jobs")[0]["attempt"] != float64(2) ||
		c[1].path != "/api/v1/admin/actions/jobs" || queryOf(t, c[1]).Get("order") != "desc" ||
		queryOf(t, c[1]).Get("sort") != "id" || queryOf(t, c[1]).Get("status") != "queued" {
		t.Fatalf("jobs: %v, %v, %+v", jobs, err, c)
	}
	for _, tc := range []struct {
		tl        *tool
		arguments string
	}{{adminWorkflowRunsList, `{"head_sha":"zz"}`}, {adminWorkflowRunsList, `{"repository":"acme/widget"}`},
		{adminWorkflowJobsList, `{"sort":"name"}`}, {adminWorkflowJobsList, `{"order":"up"}`},
		{adminWorkflowJobsList, `{"owner":"orgs/acme"}`}, {adminWorkflowJobsList, `{"limit":51}`}} {
		before := len(*e.calls)
		if _, err := e.call(t, tc.tl, "admin-open", tc.arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("%s %s: %v", tc.tl.group, tc.arguments, err)
		}
	}
	f := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(200, `{}`) })
	for _, tl := range []*tool{adminWorkflowRunsList, adminWorkflowJobsList, adminPackagesList} {
		if _, err := f.call(t, tl, "admin-open", `{}`, false); !isUnknownTool(err) || len(*f.calls) != 0 {
			t.Errorf("forgejo %s: %v", tl.group, err)
		}
	}
}

func TestAdminPackagesListNeedsGitea28(t *testing.T) {
	body := `[{"id":1,"name":"lib","type":"npm","version":"1.0.0","html_url":"https://u:p@host/x","created_at":"2030-01-01T00:00:00Z",` +
		`"owner":{"id":2,"login":"acme"},"repository":{"id":3,"full_name":"acme/widget"}}]`
	g := newAdminEnvironment(t, gitea, "1.27.3", func(call) *http.Response { return jsonResponse(200, body) })
	_, err := g.call(t, adminPackagesList, "admin-open", `{}`, false)
	if err == nil || !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "28") ||
		len(apiCalls(g.environment)) != 0 {
		t.Errorf("gitea 1.27: %v, %v", err, apiMethods(g.environment))
	}
	e := newAdminEnvironment(t, gitea, "28.2.0", func(call) *http.Response {
		return jsonResponse(200, body, "X-Total-Count", "40")
	})
	result, err := e.call(t, adminPackagesList, "admin-open", `{"type":"npm","q":"lib","limit":1}`, false)
	rows := rowsOf(t, result, "packages")
	c := apiCalls(e.environment)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "lib" || result["has_more"] != true ||
		result["total_count"] != float64(40) || c[0].path != "/api/v1/admin/packages" ||
		queryOf(t, c[0]).Get("type") != "npm" || queryOf(t, c[0]).Get("q") != "lib" {
		t.Fatalf("packages: %v, %v, %+v", result, err, c)
	}
	if _, has := rows[0]["html_url"]; has {
		t.Error("an address with credentials was returned")
	}
	for _, arguments := range []string{`{"type":"rar"}`, `{"q":"a\u0000b"}`, `{"repository":"acme/widget"}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminPackagesList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("packages %s: %v", arguments, err)
		}
	}
}
