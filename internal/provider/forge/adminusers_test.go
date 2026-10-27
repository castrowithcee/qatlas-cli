package forge

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var adminUserTools = []*tool{adminUsersList, adminUsersUpdate, adminUsersRename, adminUsersDelete}

const (
	forgejoAdminUser = `{"id":5,"login":"Octo","full_name":"Octo Cat","email":"o@example.test","is_admin":false,` +
		`"restricted":true,"active":true,"visibility":"limited","pronouns":"they","last_login":"2030-01-01T00:00:00Z",` +
		`"login_name":"ldap-canary","source_id":3,"prohibit_login":true}`
	giteaAdminUser = `{"id":5,"login":"octo","full_name":"Octo Cat","email":"o@example.test","is_admin":false,` +
		`"restricted":true,"active":true,"visibility":"limited","type":"User","last_login":"2030-01-01T00:00:00Z",` +
		`"login_name":"ldap-canary","source_id":3,"prohibit_login":true}`
)

func accountFixtureOf(p *product) string {
	if p == forgejo {
		return forgejoAdminUser
	}
	return giteaAdminUser
}

func TestAdminUsersRegisterPerProductWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminUserTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			listed := tl == adminUsersRename || tl == adminUsersDelete
			if d.RequiresToolAllowList != listed {
				t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
			}
			if !strings.HasSuffix(d.Risk.DataSensitivity, adminAccountsSensitivity) {
				t.Errorf("%s sensitivity = %q", d.ID, d.Risk.DataSensitivity)
			}
			if tl == adminUsersList {
				if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			} else if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
				(tl == adminUsersRename) != (d.Risk.Idempotency == capability.IdempotencyNonIdempotent) ||
				(tl == adminUsersDelete) != (d.Risk.Effect == capability.EffectDelete) {
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

func TestAdminUsersRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminUserTools {
			if _, ok := tl.since[p.id]; !ok {
				continue
			}
			arguments := map[*tool]string{adminUsersList: `{}`, adminUsersUpdate: `{"username":"octo","active":false}`,
				adminUsersRename: `{"username":"octo","new_username":"cat"}`, adminUsersDelete: `{"username":"octo"}`}[tl]
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, arguments, true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v after %d calls", p.id, tl.action, connection, err, len(*e.calls))
				}
			}
		}
	}
}

func TestAdminUsersListProjectsPersonMembersAndPagesByHeader(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+accountFixtureOf(p)+`,null]`, "X-Total-Count", "31")
		})
		arguments := `{"sort":"newest","limit":20}`
		if p == gitea {
			arguments = `{"q":"oc","visibility":"limited","is_active":true,"is_admin":false,"is_restricted":true,` +
				`"sort":"created","order":"desc","limit":20}`
		}
		list, err := e.call(t, adminUsersList, "admin-open", arguments, false)
		rows := rowsOf(t, list, "users")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != 1 || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/users" ||
			list["total_count"] != float64(31) || list["has_more"] != true || list["next_cursor"] == nil {
			t.Fatalf("%s list: %v, %v, %+v", p.id, list, err, c)
		}
		for _, hidden := range []string{"last_login", "login_name", "source_id", "prohibit_login"} {
			if _, has := rows[0][hidden]; has {
				t.Errorf("%s: the list returns %s", p.id, hidden)
			}
		}
		if strings.Contains(strings.Join(e.raw, ""), "ldap-canary") {
			t.Errorf("%s: a sign-in name was returned", p.id)
		}
		if (rows[0]["pronouns"] != nil) != (p == forgejo) || (rows[0]["type"] != nil) != (p == gitea) {
			t.Errorf("%s: product members = %v", p.id, rows[0])
		}
		q := queryOf(t, c[0])
		if q.Get("limit") != "20" || q.Get("page") != "1" || q.Get("sort") == "" ||
			(p == gitea && (q.Get("q") != "oc" || q.Get("visibility") != "limited" || q.Get("is_active") != "true" ||
				q.Get("is_admin") != "false" || q.Get("is_restricted") != "true" || q.Get("order") != "desc")) {
			t.Errorf("%s query = %q", p.id, c[0].query)
		}
		// The end comes from the headers, not from the row count.
		last := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+accountFixtureOf(p)+`]`, "X-Total-Count", "1")
		})
		end, err := last.call(t, adminUsersList, "admin-open", `{"limit":1}`, false)
		if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
			t.Errorf("%s last page: %v, %v", p.id, end, err)
		}
		bad := `{"visibility":"limited"}`
		if p == gitea {
			bad = `{"sort":"oldest"}`
		}
		for _, arguments := range []string{bad, `{"login_name":"x"}`, `{"source_id":1}`, `{"is_2fa_enabled":true}`,
			`{"is_prohibit_login":true}`, `{"limit":51}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminUsersList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
				t.Errorf("%s list %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestAdminUsersUpdateSendsOnlyAllowedMembers(t *testing.T) {
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		return jsonResponse(200, forgejoAdminUser)
	})
	result, err := e.call(t, adminUsersUpdate, "admin-open",
		`{"username":"octo","prohibit_login":true,"active":false,"restricted":true,"allow_create_organization":false,`+
			`"hide_email":true,"max_repo_creation":-1,"visibility":"private","full_name":"A","description":"a\nb",`+
			`"location":"L","website":"W","pronouns":"P"}`, true)
	calls := apiCalls(e.environment)
	if err != nil || len(calls) != 1 || calls[0].method != "PATCH" || calls[0].path != "/api/v1/admin/users/octo" {
		t.Fatalf("update: %v, %v, %+v", result, err, calls)
	}
	var sent map[string]any
	if json.Unmarshal([]byte(calls[0].body), &sent) != nil {
		t.Fatalf("body = %q", calls[0].body)
	}
	allowed := []string{"prohibit_login", "active", "restricted", "allow_create_organization", "hide_email",
		"max_repo_creation", "visibility", "full_name", "description", "location", "website", "pronouns"}
	for key := range sent {
		if !slices.Contains(allowed, key) {
			t.Errorf("body holds %s", key)
		}
	}
	if len(sent) != len(allowed) {
		t.Errorf("body = %v", sent)
	}
	user, _ := result["user"].(map[string]any)
	if result["username"] != "octo" || user["login"] != "Octo" || user["prohibit_login"] != nil || user["login_name"] != nil {
		t.Errorf("result = %v", result)
	}
	// Left-out members stay out of the body.
	single := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		return jsonResponse(200, forgejoAdminUser)
	})
	if _, err := single.call(t, adminUsersUpdate, "admin-open", `{"username":"octo","active":false}`, true); err != nil ||
		apiCalls(single.environment)[0].body != `{"active":false}` {
		t.Errorf("single: %v, %+v", err, apiCalls(single.environment))
	}
	// Members that hand over an account or raise it to the host never reach a request.
	for _, member := range []string{`"password":"x"`, `"email":"a@example.test"`, `"login_name":"x"`, `"source_id":1`,
		`"must_change_password":true`, `"admin":true`, `"allow_git_hook":true`, `"allow_import_local":true`,
		`"type":"Bot"`, `"sudo":"x"`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminUsersUpdate, "admin-open", `{"username":"octo","active":true,`+member+`}`, true); err == nil ||
			len(*e.calls) != before {
			t.Errorf("update with %s: %v", member, err)
		}
	}
	for _, arguments := range []string{`{"username":"octo"}`, `{"active":true}`, `{"username":"a/b","active":true}`,
		`{"username":"octo","visibility":"secret"}`, `{"username":"octo","max_repo_creation":-2}`,
		`{"username":"octo","full_name":"a\u0000b"}`, `{"username":"octo","active":"yes"}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminUsersUpdate, "admin-open", arguments, true); err == nil || len(*e.calls) != before {
			t.Errorf("update %s: %v", arguments, err)
		}
	}
	var confirm *application.ConfirmationRequiredError
	before := len(*e.calls)
	if _, err := e.call(t, adminUsersUpdate, "admin-open", `{"username":"octo","active":false}`, false); !errors.As(err, &confirm) ||
		len(*e.calls) != before {
		t.Errorf("unconfirmed update: %v", err)
	}
	other := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		return jsonResponse(200, strings.Replace(forgejoAdminUser, `"Octo"`, `"mallory"`, 1))
	})
	if _, err := other.call(t, adminUsersUpdate, "admin-open", `{"username":"octo","active":false}`, true); err == nil {
		t.Error("a foreign user was returned")
	}
}

func TestAdminUsersUpdateOnGiteaNeverSendsTheSourceOrForgejoMembers(t *testing.T) {
	g := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response {
		return jsonResponse(200, giteaAdminUser)
	})
	result, err := g.call(t, adminUsersUpdate, "admin-open",
		`{"username":"octo","prohibit_login":true,"active":false,"restricted":true,"allow_create_organization":true,`+
			`"max_repo_creation":5,"visibility":"private","full_name":"A","description":"d","location":"L","website":"W"}`, true)
	calls := apiCalls(g.environment)
	if err != nil || len(calls) != 1 || calls[0].method != "PATCH" || calls[0].path != "/api/v1/admin/users/octo" {
		t.Fatalf("gitea update: %v, %v, %+v", result, err, calls)
	}
	var sent map[string]any
	if json.Unmarshal([]byte(calls[0].body), &sent) != nil || len(sent) != 10 {
		t.Fatalf("body = %q", calls[0].body)
	}
	for _, banned := range []string{"source_id", "login_name", "password", "email", "must_change_password", "admin",
		"allow_git_hook", "allow_import_local", "type"} {
		if _, has := sent[banned]; has {
			t.Errorf("body holds %s", banned)
		}
	}
	if user, _ := result["user"].(map[string]any); user["login"] != "octo" || user["type"] != "User" || user["pronouns"] != nil {
		t.Errorf("result = %v", result)
	}
	for _, member := range []string{`"pronouns":"x"`, `"hide_email":true`, `"source_id":0`, `"type":"Bot"`, `"admin":true`} {
		before := len(*g.calls)
		if _, err := g.call(t, adminUsersUpdate, "admin-open", `{"username":"octo",`+member+`}`, true); !isInvalidRequest(err) ||
			len(*g.calls) != before {
			t.Errorf("gitea update with %s: %v", member, err)
		}
	}
}

func TestAdminUsersRenameAndDeleteAreListedOnlyAndSendOneRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, instance(defaultVersion(p), defaultVersion(p), nil))
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{adminUsersRename, `{"username":"octo","new_username":"cat"}`}, {adminUsersDelete, `{"username":"octo"}`}} {
			var unsupported *capability.UnsupportedError
			if _, err := e.invoke(t, tc.tl, "open", tc.arguments); !errors.As(err, &unsupported) || len(*e.calls) != 0 {
				t.Errorf("%s %s without a tools list: %v", p.id, tc.tl.action, err)
			}
		}
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		for _, arguments := range []string{`{"username":"octo","new_username":"cat"}`} {
			if _, err := f.call(t, adminUsersRename, "admin-open", arguments, false); !errors.As(err, &confirm) ||
				len(*f.calls) != 0 {
				t.Errorf("%s unconfirmed rename: %v", p.id, err)
			}
		}
		if _, err := f.call(t, adminUsersDelete, "admin-open", `{"username":"octo"}`, false); !errors.As(err, &confirm) ||
			len(*f.calls) != 0 {
			t.Errorf("%s unconfirmed delete: %v", p.id, err)
		}
		renamed, err := f.call(t, adminUsersRename, "admin-open", `{"username":"octo","new_username":"cat"}`, true)
		c := apiCalls(f.environment)
		if err != nil || renamed["renamed"] != true || len(c) != 1 || c[0].method != "POST" ||
			c[0].path != "/api/v1/admin/users/octo/rename" || c[0].body != `{"new_username":"cat"}` {
			t.Fatalf("%s rename: %v, %v, %+v", p.id, renamed, err, c)
		}
		deleted, err := f.call(t, adminUsersDelete, "admin-open", `{"username":"octo","purge":true}`, true)
		c = apiCalls(f.environment)
		if err != nil || deleted["deleted"] != true || len(c) != 2 || c[1].method != "DELETE" ||
			c[1].path != "/api/v1/admin/users/octo" || c[1].body != "" || queryOf(t, c[1]).Get("purge") != "true" {
			t.Fatalf("%s delete: %v, %v, %+v", p.id, deleted, err, c)
		}
		if _, err := f.call(t, adminUsersDelete, "admin-open", `{"username":"octo"}`, true); err != nil ||
			apiCalls(f.environment)[2].query != "" {
			t.Errorf("%s delete without purge: %v", p.id, err)
		}
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{adminUsersRename, `{"username":"octo"}`}, {adminUsersRename, `{"username":"octo","new_username":"a/b"}`},
			{adminUsersRename, `{"username":"..","new_username":"cat"}`},
			{adminUsersDelete, `{"username":"a/b"}`}, {adminUsersDelete, `{"username":"octo","purge":"yes"}`},
			{adminUsersDelete, `{"username":"octo","sudo":"root"}`}} {
			before := len(*f.calls)
			if _, err := f.call(t, tc.tl, "admin-open", tc.arguments, true); err == nil || len(*f.calls) != before {
				t.Errorf("%s %s %s: %v", p.id, tc.tl.action, tc.arguments, err)
			}
		}
	}
}

func TestAdminUsersChangesAreNeverRepeatedAfterAnUnclearOutcome(t *testing.T) {
	for _, p := range products {
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{adminUsersDelete, `{"username":"octo"}`}, {adminUsersRename, `{"username":"octo","new_username":"cat"}`},
			{adminUsersUpdate, `{"username":"octo","active":false}`}} {
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
