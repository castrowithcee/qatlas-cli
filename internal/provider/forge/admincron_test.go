package forge

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var adminCronTools = []*tool{adminCronList, adminCronRun}

func cronArguments(tl *tool) string {
	return map[*tool]string{adminCronList: `{"limit":20}`, adminCronRun: `{"task":"archive_cleanup"}`}[tl]
}

func TestAdminCronRegisterWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminCronTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			if d.RequiresToolAllowList || d.Risk.DataSensitivity != p.id+adminCronSensitivity {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			if tl == adminCronList && (d.Risk.Effect != capability.EffectRead ||
				d.Risk.Confirmation != capability.ConfirmationNone) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			if tl == adminCronRun && (d.Risk.Effect != capability.EffectExecute ||
				d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
				d.Risk.Confirmation != capability.ConfirmationRequired) {
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

func TestAdminCronRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminCronTools {
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, cronArguments(tl), true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v", p.id, tl.action, connection, err)
				}
			}
		}
	}
}

func TestAdminCronListProjectsEveryMemberAndPagesByHeader(t *testing.T) {
	const body = `[{"name":"archive_cleanup","schedule":"@midnight","next":"2026-01-02T00:00:00Z",` +
		`"prev":"2026-01-01T00:00:00Z","exec_times":7,"extra":"x"},{"name":"b","schedule":"@every 1h"}]`
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, body, "X-Total-Count", "31")
		})
		result, err := e.call(t, adminCronList, "admin-open", `{"limit":2}`, false)
		rows := rowsOf(t, result, "tasks")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != 2 || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/cron" ||
			result["has_more"] != true || result["next_cursor"] == nil || result["total_count"] != float64(31) {
			t.Fatalf("%s: %v, %v, %+v", p.id, result, err, c)
		}
		r := rows[0]
		if r["name"] != "archive_cleanup" || r["schedule"] != "@midnight" || r["next"] != "2026-01-02T00:00:00Z" ||
			r["prev"] != "2026-01-01T00:00:00Z" || r["exec_times"] != float64(7) || r["extra"] != nil {
			t.Errorf("row = %v", r)
		}
		if q := queryOf(t, c[0]); q.Get("limit") != "2" || q.Get("page") != "1" {
			t.Errorf("query = %q", c[0].query)
		}
		// The end comes from the header, not from the row count.
		last := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, body, "X-Total-Count", "2")
		})
		end, err := last.call(t, adminCronList, "admin-open", `{"limit":2}`, false)
		if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
			t.Errorf("%s last page: %v, %v", p.id, end, err)
		}
		for _, arguments := range []string{`{"limit":51}`, `{"sudo":"x"}`, `{"path":"/x"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminCronList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestAdminCronRunSendsOneRequestAndIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := e.call(t, adminCronRun, "admin-open", cronArguments(adminCronRun), false); !errors.As(err, &confirm) ||
			len(*e.calls) != 0 {
			t.Errorf("unconfirmed: %v", err)
		}
		result, err := e.call(t, adminCronRun, "admin-open", `{"task":"update_mirrors-2"}`, true)
		c := apiCalls(e.environment)
		if err != nil || result["started"] != true || result["task"] != "update_mirrors-2" || len(c) != 1 ||
			c[0].method != "POST" || c[0].path != "/api/v1/admin/cron/update_mirrors-2" || c[0].query != "" ||
			c[0].body != "" {
			t.Fatalf("%s run: %v, %v, %+v", p.id, result, err, c)
		}
		for _, task := range []string{"", "Upper", "a/b", "a b", "..", "_x", "-x", "a.b", "a%2Fb", "a?b",
			strings.Repeat("a", maxCronName+1)} {
			before := len(*e.calls)
			args := `{"task":"` + task + `"}`
			if _, err := e.call(t, adminCronRun, "admin-open", args, true); err == nil || len(*e.calls) != before {
				t.Errorf("%s task %q: %v", p.id, task, err)
			}
		}
		g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err = g.call(t, adminCronRun, "admin-open", cronArguments(adminCronRun), true)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
			len(apiCalls(g.environment)) != 1 {
			t.Errorf("%s after 500: %v", p.id, err)
		}
	}
}
