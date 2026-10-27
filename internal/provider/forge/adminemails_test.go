package forge

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var adminEmailTools = []*tool{adminEmailsList, adminEmailsSearch, adminUserEmailsList, adminUserEmailsDelete}

const adminEmailFixture = `{"email":"o@example.test","primary":true,"verified":true,"user_id":5,"username":"octo"}`

func emailArguments(tl *tool) string {
	return map[*tool]string{adminEmailsList: `{}`, adminEmailsSearch: `{"q":"oc"}`,
		adminUserEmailsList:   `{"username":"octo"}`,
		adminUserEmailsDelete: `{"username":"octo","emails":["o@example.test"]}`}[tl]
}

func TestAdminEmailsRegisterPerProductWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminEmailTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if ok != (p == forgejo || tl == adminEmailsList || tl == adminEmailsSearch) {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			if d.RequiresToolAllowList != (tl == adminUserEmailsDelete) ||
				!strings.HasSuffix(d.Risk.DataSensitivity, adminAccountsSensitivity) {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			if tl == adminUserEmailsDelete {
				if d.Risk.Effect != capability.EffectDelete || d.Risk.Confirmation != capability.ConfirmationRequired {
					t.Errorf("%s risk = %+v", d.ID, d.Risk)
				}
			} else if d.Risk.Effect != capability.EffectRead {
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

func TestAdminEmailsRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminEmailTools {
			if _, ok := tl.since[p.id]; !ok {
				continue
			}
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, emailArguments(tl), true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v", p.id, tl.action, connection, err)
				}
			}
		}
	}
}

func TestAdminEmailsListAndSearchPageByHeader(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{adminEmailsList, adminEmailsSearch} {
			e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
				return jsonResponse(200, `[`+adminEmailFixture+`,null]`, "X-Total-Count", "31")
			})
			result, err := e.call(t, tl, "admin-open", map[bool]string{true: `{"q":"oc","limit":20}`, false: `{"limit":20}`}[tl == adminEmailsSearch], false)
			rows := rowsOf(t, result, "emails")
			c := apiCalls(e.environment)
			wantPath := map[*tool]string{adminEmailsList: "/api/v1/admin/emails", adminEmailsSearch: "/api/v1/admin/emails/search"}[tl]
			if err != nil || len(rows) != 1 || len(c) != 1 || c[0].method != "GET" || c[0].path != wantPath ||
				result["has_more"] != true || result["next_cursor"] == nil || result["total_count"] != float64(31) {
				t.Fatalf("%s %s: %v, %v, %+v", p.id, tl.action, result, err, c)
			}
			for _, key := range []string{"email", "primary", "verified", "user_id", "username"} {
				if _, has := rows[0][key]; !has {
					t.Errorf("%s %s lacks %s", p.id, tl.action, key)
				}
			}
			q := queryOf(t, c[0])
			if q.Get("limit") != "20" || q.Get("page") != "1" || (tl == adminEmailsSearch) != (q.Get("q") == "oc") {
				t.Errorf("%s %s query = %q", p.id, tl.action, c[0].query)
			}
			last := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
				return jsonResponse(200, `[`+adminEmailFixture+`]`, "X-Total-Count", "1")
			})
			end, err := last.call(t, tl, "admin-open", `{"limit":1}`, false)
			if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
				t.Errorf("%s %s last page: %v, %v", p.id, tl.action, end, err)
			}
			for _, arguments := range []string{`{"limit":51}`, `{"sudo":"x"}`, `{"q":"a\u0000b"}`} {
				before := len(*e.calls)
				if _, err := e.call(t, tl, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
					t.Errorf("%s %s %s: %v", p.id, tl.action, arguments, err)
				}
			}
		}
	}
}

func TestAdminUserEmailsListIsUnpagedAndCapped(t *testing.T) {
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		rows := make([]string, maxRunnerRows+5)
		for i := range rows {
			rows[i] = adminEmailFixture
		}
		return jsonResponse(200, `[`+strings.Join(rows, ",")+`]`)
	})
	result, err := e.call(t, adminUserEmailsList, "admin-open", `{"username":"Octo"}`, false)
	c := apiCalls(e.environment)
	if err != nil || len(rowsOf(t, result, "emails")) != maxRunnerRows || result["has_more"] != true ||
		len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/users/Octo/emails" || c[0].query != "" {
		t.Fatalf("list: %d rows, %v, %+v", len(rowsOf(t, result, "emails")), err, c)
	}
	for _, arguments := range []string{`{}`, `{"username":"a/b"}`, `{"username":"octo","limit":5}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminUserEmailsList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("list %s: %v", arguments, err)
		}
	}
}

func TestAdminUserEmailsAreAbsentOnGitea(t *testing.T) {
	reg := registry(t)
	for _, tl := range []*tool{adminUserEmailsList, adminUserEmailsDelete} {
		if _, _, ok := reg.Lookup(gitea.toolID(tl)); ok {
			t.Errorf("%s is registered", gitea.toolID(tl))
		}
	}
}

func TestAdminUserEmailsDeleteIsListedOnlySendsOneBodyAndIsNeverRepeated(t *testing.T) {
	e := newEnvironment(t, forgejo, instance(defaultVersion(forgejo), defaultVersion(forgejo), nil))
	var unsupported *capability.UnsupportedError
	if _, err := e.invoke(t, adminUserEmailsDelete, "open", emailArguments(adminUserEmailsDelete)); !errors.As(err, &unsupported) ||
		len(*e.calls) != 0 {
		t.Errorf("without a tools list: %v", err)
	}
	f := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response { return jsonResponse(204, ``) })
	var confirm *application.ConfirmationRequiredError
	if _, err := f.call(t, adminUserEmailsDelete, "admin-open", emailArguments(adminUserEmailsDelete), false); !errors.As(err, &confirm) ||
		len(*f.calls) != 0 {
		t.Errorf("unconfirmed: %v", err)
	}
	result, err := f.call(t, adminUserEmailsDelete, "admin-open",
		`{"username":"octo","emails":["a@example.test","b@example.test"]}`, true)
	c := apiCalls(f.environment)
	if err != nil || result["deleted"] != true || result["count"] != float64(2) || len(c) != 1 || c[0].method != "DELETE" ||
		c[0].path != "/api/v1/admin/users/octo/emails" || c[0].query != "" ||
		c[0].body != `{"emails":["a@example.test","b@example.test"]}` {
		t.Fatalf("delete: %v, %v, %+v", result, err, c)
	}
	tooMany := make([]string, maxDeleteMails+1)
	for i := range tooMany {
		tooMany[i] = `"u` + strconv.Itoa(i) + `@example.test"`
	}
	for _, arguments := range []string{`{"username":"octo"}`, `{"username":"octo","emails":[]}`,
		`{"username":"a/b","emails":["a@example.test"]}`, `{"username":"octo","emails":["nomail"]}`,
		`{"username":"octo","emails":["a b@example.test"]}`,
		`{"username":"octo","emails":["a@example.test","A@example.test"]}`,
		`{"username":"octo","emails":["` + strings.Repeat("a", 250) + `@example.test"]}`,
		`{"username":"octo","emails":[` + strings.Join(tooMany, ",") + `]}`,
		`{"username":"octo","emails":["a@example.test"],"add":["b@example.test"]}`,
		`{"username":"octo","emails":["a@example.test"],"sudo":"root"}`} {
		before := len(*f.calls)
		if _, err := f.call(t, adminUserEmailsDelete, "admin-open", arguments, true); err == nil || len(*f.calls) != before {
			t.Errorf("delete %s: %v", arguments, err)
		}
	}
	g := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
	})
	_, err = g.call(t, adminUserEmailsDelete, "admin-open", emailArguments(adminUserEmailsDelete), true)
	if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
		len(apiCalls(g.environment)) != 1 {
		t.Errorf("after 500: %v", err)
	}
}
