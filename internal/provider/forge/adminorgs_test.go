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

var adminOrgTools = []*tool{adminOrgsList, adminOrgsCreate, adminRepositoriesCreate}

const (
	adminOrgFixture  = `{"id":7,"name":"Example-Org","full_name":"Example","visibility":"private"}`
	adminRepoFixture = `{"id":9,"name":"project","full_name":"Octo/project","private":true,"owner":{"login":"Octo"}}`
)

func adminOrgArguments(tl *tool) string {
	return map[*tool]string{adminOrgsList: `{}`,
		adminOrgsCreate:         `{"username":"octo","organization":"example-org","visibility":"private"}`,
		adminRepositoriesCreate: `{"username":"octo","name":"project","private":true}`}[tl]
}

func TestAdminOrgToolsRegisterWithTheirRiskInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminOrgTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			want := p.id + orgSensitivity
			if tl == adminRepositoriesCreate {
				want = p.id + repositorySensitivity
			}
			if d.RequiresToolAllowList || d.Risk.DataSensitivity != want {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			if tl == adminOrgsList {
				if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			} else if d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
				d.Risk.Confirmation != capability.ConfirmationRequired {
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

func TestAdminOrgToolsRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminOrgTools {
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, adminOrgArguments(tl), true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v", p.id, tl.action, connection, err)
				}
			}
		}
	}
}

func TestAdminOrgsListPagesByHeader(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+adminOrgFixture+`,null]`, "X-Total-Count", "31")
		})
		result, err := e.call(t, adminOrgsList, "admin-open", `{"limit":20}`, false)
		rows := rowsOf(t, result, "organizations")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != 1 || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/orgs" ||
			result["has_more"] != true || result["next_cursor"] == nil || rows[0]["name"] != "Example-Org" {
			t.Fatalf("%s: %v, %v, %+v", p.id, result, err, c)
		}
		if q := queryOf(t, c[0]); q.Get("limit") != "20" || q.Get("page") != "1" {
			t.Errorf("%s query = %q", p.id, c[0].query)
		}
		last := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+adminOrgFixture+`]`, "X-Total-Count", "1")
		})
		end, err := last.call(t, adminOrgsList, "admin-open", `{"limit":1}`, false)
		if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
			t.Errorf("%s last page: %v, %v", p.id, end, err)
		}
		for _, arguments := range []string{`{"limit":51}`, `{"sudo":"x"}`, `{"q":"a"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminOrgsList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestAdminOrgsCreateSendsOneRequestAndChecksTheAnswer(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(201, adminOrgFixture) })
		var confirm *application.ConfirmationRequiredError
		if _, err := e.call(t, adminOrgsCreate, "admin-open", adminOrgArguments(adminOrgsCreate), false); !errors.As(err, &confirm) ||
			len(*e.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
		result, err := e.call(t, adminOrgsCreate, "admin-open",
			`{"username":"Octo","organization":"example-org","visibility":"private","description":"x"}`, true)
		c := apiCalls(e.environment)
		if err != nil || result["name"] != "Example-Org" || len(c) != 1 || c[0].method != "POST" ||
			c[0].path != "/api/v1/admin/users/Octo/orgs" ||
			c[0].body != `{"description":"x","username":"example-org","visibility":"private"}` {
			t.Fatalf("%s create: %v, %v, %+v", p.id, result, err, c)
		}
		for _, arguments := range []string{`{"organization":"o"}`, `{"username":"octo"}`, `{"username":"a/b","organization":"o"}`,
			`{"username":"octo","organization":"a/b"}`, `{"username":"octo","organization":"o","visibility":"secret"}`,
			`{"username":"octo","organization":"o","sudo":"root"}`, `{"username":"octo","organization":"o","template":true}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminOrgsCreate, "admin-open", arguments, true); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
		other := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(201, `{"id":1,"name":"someone-else"}`)
		})
		if _, err := other.call(t, adminOrgsCreate, "admin-open", adminOrgArguments(adminOrgsCreate), true); err == nil ||
			!strings.Contains(err.Error(), "another organization") {
			t.Errorf("%s other organization: %v", p.id, err)
		}
	}
}

func TestAdminOrgsCreateSettingsOfTheOtherProductAreRefused(t *testing.T) {
	for _, p := range products {
		for _, f := range orgFields {
			if f.offered(p) || f.only == nil {
				continue
			}
			e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(201, adminOrgFixture) })
			reads := *e.reads
			_, err := e.call(t, adminOrgsCreate, "admin-open",
				`{"username":"octo","organization":"o","`+f.name+`":true}`, true)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads {
				t.Errorf("%s %s: %v", p.id, f.name, err)
			}
		}
	}
}

func TestAdminRepositoriesCreateSendsOneRequestAndChecksTheAnswer(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(201, adminRepoFixture) })
		var confirm *application.ConfirmationRequiredError
		if _, err := e.call(t, adminRepositoriesCreate, "admin-open", adminOrgArguments(adminRepositoriesCreate), false); !errors.As(err, &confirm) ||
			len(*e.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
		result, err := e.call(t, adminRepositoriesCreate, "admin-open",
			`{"username":"Octo","name":"project","private":true,"auto_init":true,"license":"MIT"}`, true)
		c := apiCalls(e.environment)
		if err != nil || result["full_name"] != "Octo/project" || len(c) != 1 || c[0].method != "POST" ||
			c[0].path != "/api/v1/admin/users/Octo/repos" ||
			c[0].body != `{"auto_init":true,"license":"MIT","name":"project","private":true}` {
			t.Fatalf("%s create: %v, %v, %+v", p.id, result, err, c)
		}
		for _, arguments := range []string{`{"name":"project"}`, `{"username":"octo"}`, `{"username":"a/b","name":"project"}`,
			`{"username":"octo","name":"a/b"}`, `{"username":"octo","name":"project","trust_model":"x"}`,
			`{"username":"octo","name":"project","organization":"acme"}`,
			`{"username":"octo","name":"project","template_owner":"x"}`, `{"username":"octo","name":"project","sudo":"x"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminRepositoriesCreate, "admin-open", arguments, true); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
		other := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(201, `{"id":1,"name":"project","full_name":"other/project"}`)
		})
		if _, err := other.call(t, adminRepositoriesCreate, "admin-open", adminOrgArguments(adminRepositoriesCreate), true); err == nil ||
			!strings.Contains(err.Error(), "another repository") {
			t.Errorf("%s other repository: %v", p.id, err)
		}
	}
}

func TestAdminOrgCreatesAreNeverRepeatedAfterAServerError(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{adminOrgsCreate, adminRepositoriesCreate} {
			g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			})
			_, err := g.call(t, tl, "admin-open", adminOrgArguments(tl), true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
				len(apiCalls(g.environment)) != 1 {
				t.Errorf("%s %s after 500: %v", p.id, tl.action, err)
			}
		}
	}
}
