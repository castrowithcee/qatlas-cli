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

var adminUnadoptedTools = []*tool{adminUnadoptedList, adminUnadoptedAdopt, adminUnadoptedDelete}

func unadoptedArguments(tl *tool) string {
	return map[*tool]string{adminUnadoptedList: `{"pattern":"octo"}`,
		adminUnadoptedAdopt: `{"owner":"octo","repo":"project"}`, adminUnadoptedDelete: `{"owner":"octo","repo":"stale"}`}[tl]
}

func TestAdminUnadoptedRegisterWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminUnadoptedTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			if d.RequiresToolAllowList != (tl == adminUnadoptedDelete) ||
				d.Risk.DataSensitivity != p.id+repositorySensitivity {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			switch tl {
			case adminUnadoptedList:
				if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			case adminUnadoptedAdopt:
				if d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
					d.Risk.Confirmation != capability.ConfirmationRequired {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			default:
				if d.Risk.Effect != capability.EffectDelete || d.Risk.Confirmation != capability.ConfirmationRequired ||
					!strings.Contains(d.Description, "for good") {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func TestAdminUnadoptedRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminUnadoptedTools {
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, unadoptedArguments(tl), true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v", p.id, tl.action, connection, err)
				}
			}
		}
	}
}

func TestAdminUnadoptedListNamesTheFilterByVersionAndPagesByHeader(t *testing.T) {
	cases := []struct {
		p       *product
		version string
		filter  string
	}{{gitea, "1.27.3", "pattern"}, {gitea, "28.2.0", "query"}, {forgejo, "15.0.10", "pattern"},
		{forgejo, "17.0.0", "pattern"}}
	for _, tc := range cases {
		e := newAdminEnvironment(t, tc.p, tc.version, func(call) *http.Response {
			return jsonResponse(200, `["octo/project","bad","a/b/c","octo/","../x/y","Org-1/repo.git"]`,
				"X-Total-Count", "31")
		})
		result, err := e.call(t, adminUnadoptedList, "admin-open", `{"pattern":"octo/*","limit":20}`, false)
		rows := rowsOf(t, result, "unadopted")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != 2 || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/unadopted" ||
			result["has_more"] != true || result["next_cursor"] == nil || result["total_count"] != float64(31) {
			t.Fatalf("%s %s: %v, %v, %+v", tc.p.id, tc.version, result, err, c)
		}
		if rows[0]["owner"] != "octo" || rows[0]["repo"] != "project" || rows[0]["full_name"] != "octo/project" {
			t.Errorf("row = %v", rows[0])
		}
		q := queryOf(t, c[0])
		other := map[string]string{"pattern": "query", "query": "pattern"}[tc.filter]
		if q.Get(tc.filter) != "octo/*" || q.Has(other) || q.Get("limit") != "20" || q.Get("page") != "1" {
			t.Errorf("%s %s query = %q", tc.p.id, tc.version, c[0].query)
		}
		last := newAdminEnvironment(t, tc.p, tc.version, func(call) *http.Response {
			return jsonResponse(200, `["octo/project"]`, "X-Total-Count", "1")
		})
		end, err := last.call(t, adminUnadoptedList, "admin-open", `{"limit":1}`, false)
		if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
			t.Errorf("%s last page: %v, %v", tc.p.id, end, err)
		}
		if q := queryOf(t, apiCalls(last.environment)[0]); q.Has("pattern") || q.Has("query") {
			t.Errorf("filter sent without a pattern: %v", q)
		}
		for _, arguments := range []string{`{"pattern":"a/b/c"}`, `{"pattern":"a\\b"}`, `{"pattern":"a\u0000b"}`,
			`{"pattern":""}`, `{"limit":51}`, `{"sudo":"x"}`, `{"path":"/x"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminUnadoptedList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", tc.p.id, arguments, err)
			}
		}
	}
}

func TestAdminUnadoptedAdoptSendsOneRequestAndIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := e.call(t, adminUnadoptedAdopt, "admin-open", unadoptedArguments(adminUnadoptedAdopt), false); !errors.As(err, &confirm) ||
			len(*e.calls) != 0 {
			t.Errorf("unconfirmed: %v", err)
		}
		result, err := e.call(t, adminUnadoptedAdopt, "admin-open", `{"owner":"Octo","repo":"my.repo-1"}`, true)
		c := apiCalls(e.environment)
		if err != nil || result["adopted"] != true || result["full_name"] != "Octo/my.repo-1" || len(c) != 1 ||
			c[0].method != "POST" || c[0].path != "/api/v1/admin/unadopted/Octo/my.repo-1" || c[0].query != "" || c[0].body != "" {
			t.Fatalf("%s adopt: %v, %v, %+v", p.id, result, err, c)
		}
		g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err = g.call(t, adminUnadoptedAdopt, "admin-open", unadoptedArguments(adminUnadoptedAdopt), true)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
			len(apiCalls(g.environment)) != 1 {
			t.Errorf("%s after 500: %v", p.id, err)
		}
	}
}

func TestAdminUnadoptedDeleteIsListedOnlyAndNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, instance(defaultVersion(p), defaultVersion(p), nil))
		var unsupported *capability.UnsupportedError
		if _, err := e.invoke(t, adminUnadoptedDelete, "open", unadoptedArguments(adminUnadoptedDelete)); !errors.As(err, &unsupported) ||
			len(*e.calls) != 0 {
			t.Errorf("%s without a tools list: %v", p.id, err)
		}
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := f.call(t, adminUnadoptedDelete, "admin-open", unadoptedArguments(adminUnadoptedDelete), false); !errors.As(err, &confirm) ||
			len(*f.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
		result, err := f.call(t, adminUnadoptedDelete, "admin-open", unadoptedArguments(adminUnadoptedDelete), true)
		c := apiCalls(f.environment)
		if err != nil || result["deleted"] != true || len(c) != 1 || c[0].method != "DELETE" ||
			c[0].path != "/api/v1/admin/unadopted/octo/stale" || c[0].query != "" || c[0].body != "" {
			t.Fatalf("%s delete: %v, %v, %+v", p.id, result, err, c)
		}
		g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err = g.call(t, adminUnadoptedDelete, "admin-open", unadoptedArguments(adminUnadoptedDelete), true)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
			len(apiCalls(g.environment)) != 1 {
			t.Errorf("%s after 500: %v", p.id, err)
		}
	}
}

func TestAdminUnadoptedChangesRefuseInvalidNamesWithoutRequest(t *testing.T) {
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(204, ``) })
	for _, tl := range []*tool{adminUnadoptedAdopt, adminUnadoptedDelete} {
		for _, arguments := range []string{`{}`, `{"owner":"octo"}`, `{"repo":"x"}`, `{"owner":"a/b","repo":"x"}`,
			`{"owner":"..","repo":"x"}`, `{"owner":"octo","repo":".."}`, `{"owner":"octo","repo":"a/b"}`,
			`{"owner":"octo","repo":"a%2Fb"}`, `{"owner":"octo","repo":"x","path":"/y"}`,
			`{"owner":"octo","repo":"x","sudo":"root"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, tl, "admin-open", arguments, true); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", tl.action, arguments, err)
			}
		}
	}
}

func TestAdminUnadoptedDropsAndRefusesExclusions(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			return jsonResponse(200, `["octo/project","Acme/Gadget","other/anything","acme/widget"]`)
		})
		result, err := e.call(t, adminUnadoptedList, "admin-exclude", `{}`, false)
		var names []string
		for _, row := range rowsOf(t, result, "unadopted") {
			names = append(names, row["full_name"].(string))
		}
		if err != nil || !slices.Equal(names, []string{"octo/project", "acme/widget"}) {
			t.Errorf("%s: list with exclusions = %v, %v", p.id, names, err)
		}
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{
			{adminUnadoptedAdopt, `{"owner":"acme","repo":"GADGET"}`}, {adminUnadoptedDelete, `{"owner":"acme","repo":"gadget"}`},
			{adminUnadoptedAdopt, `{"owner":"Other","repo":"x"}`}, {adminUnadoptedDelete, `{"owner":"other","repo":"x"}`},
		} {
			before := len(*e.calls)
			_, err := e.call(t, tc.tl, "admin-exclude", tc.arguments, true)
			if err == nil || !isInvalidRequest(err) || len(*e.calls) != before || strings.Contains(err.Error(), "exclu") ||
				strings.Contains(strings.ToLower(err.Error()), "gadget") {
				t.Errorf("%s %s %s: %v", p.id, tc.tl.action, tc.arguments, err)
			}
		}
		if _, err := e.call(t, adminUnadoptedAdopt, "admin-exclude", `{"owner":"acme","repo":"widget"}`, true); err != nil {
			t.Errorf("%s: adopt of a reachable name: %v", p.id, err)
		}
	}
}
