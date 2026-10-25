package matomo

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// securityMembers are the members of a Matomo user row that concern the security of the account. The
// fixtures carry them, plus one member no release has, and no result may.
const securityMembers = `"password":"$2y$hash-canary","token_auth":"token-canary","twofactor_secret":"secret-canary",` +
	`"uses_2fa":true,"ts_password_modified":"2025-01-01 00:00:00","invite_token":"invite-canary",` +
	`"invite_link_token":"link-canary","invite_expired_at":"2026-01-01 00:00:00","invite_accept_at":"2026-01-02 00:00:00",` +
	`"invite_status":"expired","invited_by":"inviter-canary","idchange_last_viewed":"7","ts_changes_shown":"x",` +
	`"ts_inactivity_notified":"y","failed_attempts":"3","brand_new_member":"new-canary"`

var securityCanaries = []string{"hash-canary", "token-canary", "secret-canary", "invite-canary", "link-canary",
	"inviter-canary", "new-canary", "uses_2fa", "invite_status", "failed_attempts", "ts_password_modified",
	"2026-01-01", "2026-01-02"}

func userRow(login string, extra string) string {
	return `{"login":"` + login + `","email":"` + login + `@example.test","superuser_access":false,` +
		`"date_registered":"2024-02-03 04:05:06","last_seen":"2026-05-06 07:08:09","last_seen_ago":"2 days",` +
		securityMembers + extra + `}`
}

func noCanary(t *testing.T, result string) {
	t.Helper()
	for _, canary := range securityCanaries {
		if strings.Contains(result, canary) {
			t.Errorf("result carries %q: %s", canary, result)
		}
	}
}

func usersAnswers() answers {
	return with(versionOnly("5.14.2"), answers{
		"UsersManager.getUsersPlusRole": func(c call) *http.Response {
			rows := []string{userRow("alice", `,"role":"admin","capabilities":["tagmanager_write"]`),
				userRow("bob", `,"role":"view","capabilities":[]`), userRow("carol", `,"role":"write","capabilities":[]`)}
			return ok("[" + strings.Join(rows, ",") + "]")
		},
		"UsersManager.getUsers": func(c call) *http.Response {
			return ok("[" + userRow("zed", "") + "," + userRow("alice", "") + "," + userRow("bob", "") + "]")
		},
		"UsersManager.getUsersLogin": func(call) *http.Response { return ok(`["zed","alice","bob"]`) },
	})
}

func TestUsersListSiteModeUsesGetUsersPlusRole(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, usersAnswers().handler(t))
	result, err := env.invoke("matomo.users.list", "two", `{"idSite":1,"limit":2,"search":"a b","access":"admin"}`)
	if err != nil {
		t.Fatal(err)
	}
	noCanary(t, result)
	for _, want := range []string{`"login":"alice"`, `"role":"admin"`, `"capabilities":["tagmanager_write"]`,
		`"date_registered":"2024-02-03 04:05:06"`, `"email":"alice@example.test"`, `"has_more":true`, `"next_cursor"`} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %s in %s", want, result)
		}
	}
	if strings.Contains(result, "carol") {
		t.Errorf("the extra row is shown: %s", result)
	}
	got := calls[len(calls)-1]
	if got.api() != "UsersManager.getUsersPlusRole" || got.form.Get("idSite") != "1" || got.form.Get("limit") != "3" ||
		got.form.Get("offset") != "0" || got.form.Get("filter_search") != "a b" || got.form.Get("filter_access") != "admin" ||
		got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") || strings.Contains(got.rawQuery, tokenValue) {
		t.Errorf("call = %+v", got)
	}
}

func TestUsersListPagesAndBindsTheCursor(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, usersAnswers().handler(t))
	first, err := env.invoke("matomo.users.list", "mixed", `{"limit":2}`)
	if err != nil || !strings.Contains(first, `"login":"alice"`) || !strings.Contains(first, `"login":"bob"`) ||
		strings.Contains(first, "zed") || !strings.Contains(first, `"has_more":true`) {
		t.Fatalf("first = %s, err = %v", first, err)
	}
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsers" || got.form.Has("userLogins") {
		t.Errorf("call = %+v", got)
	}
	cursor := between(first, `"next_cursor":"`, `"`)
	last, err := env.invoke("matomo.users.list", "mixed", `{"cursor":"`+cursor+`"}`)
	if err != nil || !strings.Contains(last, `"login":"zed"`) || strings.Contains(last, "next_cursor") {
		t.Fatalf("last = %s, err = %v", last, err)
	}
	for name, arguments := range map[string]string{
		"other mode":  `{"only_logins":true,"cursor":"` + cursor + `"}`,
		"other limit": `{"limit":3,"cursor":"` + cursor + `"}`,
		"altered":     `{"cursor":"` + cursor[:len(cursor)-2] + `AA"}`,
	} {
		if _, err := env.invoke("matomo.users.list", "mixed", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func between(text, start, end string) string {
	_, rest, _ := strings.Cut(text, start)
	value, _, _ := strings.Cut(rest, end)
	return value
}

func TestUsersListInstanceModes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, usersAnswers().handler(t))
	result, err := env.invoke("matomo.users.list", "mixed", `{"logins":["bob","alice"]}`)
	if err != nil {
		t.Fatal(err)
	}
	noCanary(t, result)
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsers" || got.form.Get("userLogins") != "bob,alice" {
		t.Errorf("call = %+v", got)
	}
	if strings.Contains(result, `"role"`) {
		t.Errorf("an instance-wide user carries a role: %s", result)
	}
	result, err = env.invoke("matomo.users.list", "mixed", `{"only_logins":true}`)
	if err != nil || result != `{"has_more":false,"logins":["alice","bob","zed"]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsersLogin" {
		t.Errorf("call = %+v", got)
	}
}

func TestUsersListRefusalsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, usersAnswers().handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"open", `{}`}, {"general", `{"only_logins":true}`}, {"one", `{"logins":["a"]}`}, {"two", `{}`},
		{"one", `{"idSite":1}`}, {"two", `{"idSite":3}`}, {"mixed", `{"idSite":1}`}, {"mixed", `{"idSite":3}`},
		{"two", `{"idSite":1,"logins":["a"]}`}, {"two", `{"idSite":1,"only_logins":true}`},
		{"two", `{"idSite":1,"access":"root"}`}, {"mixed", `{"search":"a"}`}, {"mixed", `{"access":"view"}`},
		{"mixed", `{"only_logins":true,"logins":["a"]}`}, {"mixed", `{"logins":["a,b"]}`},
		{"mixed", `{"logins":[]}`},
	} {
		_, err := env.invoke("matomo.users.list", tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s: accepted", tc.connection, tc.arguments)
		} else if strings.Contains(err.Error(), "site/") && strings.Contains(tc.arguments, "3") {
			t.Errorf("%s %s: refusal names a target: %v", tc.connection, tc.arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	if ok, _ := reaches(usersList.ID, []string{"instance"}); ok {
		t.Error("an instance-only binding is offered the user list")
	}
}

func TestUsersGetKinds(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getUser":        func(call) *http.Response { return ok(userRow("alice", "")) },
		"UsersManager.getUserByEmail": func(call) *http.Response { return ok(userRow("bob", "")) },
		"UsersManager.userExists":     func(call) *http.Response { return ok(`{"value":true}`) },
		"UsersManager.userEmailExists": func(call) *http.Response {
			return ok(`false`)
		},
		"UsersManager.getUsersHavingSuperUserAccess": func(call) *http.Response {
			return ok(`[{"login":"root2","email":"r2@example.test","superuser_access":"1",` + securityMembers + `},` +
				`{"login":"root","email":"r@example.test","superuser_access":1}]`)
		},
		"UsersManager.hasSuperUserAccess": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	for _, tc := range []struct{ arguments, method, param, want string }{
		{`{"login":"alice"}`, "UsersManager.getUser", "userLogin", `"user":{"date_registered":"2024-02-03 04:05:06","email":"alice@example.test"`},
		{`{"email":"bob@example.test"}`, "UsersManager.getUserByEmail", "userEmail", `"login":"bob"`},
		{`{"kind":"exists","login":"alice"}`, "UsersManager.userExists", "userLogin", `"exists":true`},
		{`{"kind":"exists","email":"x@example.test"}`, "UsersManager.userEmailExists", "userEmail", `"exists":false`},
		{`{"kind":"superusers"}`, "UsersManager.getUsersHavingSuperUserAccess", "", `"users":[{"email":"r@example.test","login":"root"`},
		{`{"kind":"token-superuser"}`, "UsersManager.hasSuperUserAccess", "", `"token_superuser":false`},
	} {
		result, err := env.invoke("matomo.users.get", "mixed", tc.arguments)
		if err != nil || !strings.Contains(result, tc.want) {
			t.Errorf("%s: result = %s, err = %v", tc.arguments, result, err)
		}
		noCanary(t, result)
		got := calls[len(calls)-1]
		if got.api() != tc.method || got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") ||
			tc.param != "" && got.form.Get(tc.param) == "" {
			t.Errorf("%s: call = %+v", tc.arguments, got)
		}
	}
}

func TestUsersGetRefusalsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, connection := range []string{"open", "general", "one", "two"} {
		if _, err := env.invoke("matomo.users.get", connection, `{"login":"alice"}`); err == nil {
			t.Errorf("connection %s read a user", connection)
		}
	}
	for _, arguments := range []string{`{}`, `{"login":"a","email":"b"}`, `{"kind":"nope","login":"a"}`,
		`{"kind":"superusers","login":"a"}`, `{"kind":"token-superuser","email":"a"}`, `{"login":"a","limit":2}`,
		`{"login":"a\nb"}`, `{"kind":"exists"}`} {
		if _, err := env.invoke("matomo.users.get", "instance", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
	if ok, forms := reaches(usersGet.ID, []string{"site"}); ok || !slices.Equal(forms, []string{"instance"}) {
		t.Errorf("reaches = %v %v", ok, forms)
	}
}

func TestUsersGetEmptyUserIsNotFound(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getUser": func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	_, err := env.invoke("matomo.users.get", "instance", `{"login":"ghost"}`)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestUsersGetSuperusersPage(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getUsersHavingSuperUserAccess": func(call) *http.Response {
			return ok(`[{"login":"c"},{"login":"a"},{"login":"b"}]`)
		},
	}).handler(t))
	first, err := env.invoke("matomo.users.get", "instance", `{"kind":"superusers","limit":2}`)
	if err != nil || !strings.Contains(first, `"login":"a"`) || !strings.Contains(first, `"has_more":true`) {
		t.Fatalf("first = %s, err = %v", first, err)
	}
	last, err := env.invoke("matomo.users.get", "instance",
		`{"kind":"superusers","cursor":"`+between(first, `"next_cursor":"`, `"`)+`"}`)
	if err != nil || !strings.Contains(last, `"login":"c"`) || strings.Contains(last, "has_more") {
		t.Fatalf("last = %s, err = %v", last, err)
	}
}

func accessAnswers() answers {
	return with(versionOnly("5.14.2"), answers{
		"UsersManager.getSitesAccessForUser": func(c call) *http.Response {
			return ok(`[{"idsite":"1","site_name":"One","role":"view","capabilities":[]},` +
				`{"idsite":"2","site_name":"Two","role":"admin","capabilities":["tagmanager_write"]},` +
				`{"idsite":"3","site_name":"Three","role":"write","capabilities":[]}]`)
		},
		"UsersManager.getSitesAccessFromUser": func(call) *http.Response {
			return ok(`[{"site":"3","access":"admin"},{"site":"2","access":"write"},{"site":2,"access":"tagmanager_write"}]`)
		},
		"UsersManager.getUsersAccessFromSite": func(call) *http.Response {
			return ok(`{"carol":"write","alice":"admin"}`)
		},
		"UsersManager.getUsersWithSiteAccess": func(call) *http.Response {
			return ok("[" + userRow("alice", "") + "," + userRow("bob", "") + "]")
		},
		"UsersManager.getUsersSitesFromAccess": func(call) *http.Response {
			return ok(`{"bob":["3","1"],"alice":[2,"3"],"dave":["3"]}`)
		},
	})
}

func TestUsersGetAccessForUserDropsRowsOutsideTheTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, accessAnswers().handler(t))
	result, err := env.invoke("matomo.users.get-access", "two", `{"login":"alice","role":"view","search":"x"}`)
	if err != nil || strings.Contains(result, "Three") || !strings.Contains(result, `"name":"One"`) ||
		!strings.Contains(result, `"capabilities":["tagmanager_write"]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	got := calls[len(calls)-1]
	if got.api() != "UsersManager.getSitesAccessForUser" || got.form.Get("userLogin") != "alice" ||
		got.form.Get("filter_access") != "view" || got.form.Get("filter_search") != "x" || got.form.Get("limit") != "1000" {
		t.Errorf("call = %+v", got)
	}
	// With every website in scope Matomo pages the list.
	result, err = env.invoke("matomo.users.get-access", "open", `{"login":"alice","limit":3}`)
	if err != nil || !strings.Contains(result, `"name":"Three"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	got = calls[len(calls)-1]
	if got.form.Get("limit") != "4" || got.form.Get("offset") != "0" {
		t.Errorf("call = %+v", got)
	}
}

func TestUsersGetAccessRawNeedsTheInstanceAndDropsRows(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, accessAnswers().handler(t))
	if _, err := env.invoke("matomo.users.get-access", "two", `{"login":"alice","raw":true}`); !isInvalidRequest(err) {
		t.Errorf("err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatal("a refused call reached the secret or Matomo")
	}
	result, err := env.invoke("matomo.users.get-access", "mixed", `{"login":"alice","raw":true}`)
	want := `{"entries":[{"access":"tagmanager_write","id":2},{"access":"write","id":2}],"has_more":false}`
	if err != nil || result != want {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getSitesAccessFromUser" {
		t.Errorf("call = %+v", got)
	}
}

func TestUsersGetAccessBySite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, accessAnswers().handler(t))
	result, err := env.invoke("matomo.users.get-access", "two", `{"idSite":1}`)
	if err != nil || result != `{"has_more":false,"users":[{"access":"admin","login":"alice"},{"access":"write","login":"carol"}]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsersAccessFromSite" || got.form.Get("idSite") != "1" {
		t.Errorf("call = %+v", got)
	}
	result, err = env.invoke("matomo.users.get-access", "two", `{"idSite":2,"access":"admin"}`)
	if err != nil || !strings.Contains(result, `"login":"alice"`) || !strings.Contains(result, `"access":"admin"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	noCanary(t, result)
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsersWithSiteAccess" || got.form.Get("idSite") != "2" ||
		got.form.Get("access") != "admin" {
		t.Errorf("call = %+v", got)
	}
	page, err := env.invoke("matomo.users.get-access", "two", `{"idSite":1,"limit":1}`)
	if err != nil || !strings.Contains(page, `"has_more":true`) || strings.Contains(page, "carol") {
		t.Fatalf("page = %s, err = %v", page, err)
	}
}

func TestUsersGetAccessFromAccessAlone(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, accessAnswers().handler(t))
	// Rows of websites outside the targets drop, and a user left without a website drops with them.
	result, err := env.invoke("matomo.users.get-access", "mixed", `{"access":"admin"}`)
	if err != nil || result != `{"has_more":false,"users":[{"login":"alice","sites":[2]}]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "UsersManager.getUsersSitesFromAccess" || got.form.Get("access") != "admin" {
		t.Errorf("call = %+v", got)
	}
	result, err = env.invoke("matomo.users.get-access", "open", `{"access":"admin"}`)
	if !isInvalidRequest(err) {
		t.Errorf("a binding without instance: %s, %v", result, err)
	}
}

func TestUsersGetAccessRefusalsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, accessAnswers().handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"instance", `{"login":"a"}`}, {"two", `{}`}, {"two", `{"login":"a","idSite":1}`}, {"two", `{"idSite":3}`},
		{"one", `{"idSite":1}`}, {"two", `{"idSite":1,"role":"view"}`}, {"two", `{"idSite":1,"raw":true}`},
		{"two", `{"login":"a","access":"admin"}`}, {"mixed", `{"login":"a","raw":true,"role":"view"}`},
		{"two", `{"login":"a","role":"root"}`}, {"two", `{"idSite":1,"access":"a;b"}`}, {"mixed", `{"access":"x y"}`},
		{"mixed", `{"role":"view"}`}, {"two", `{"login":"a\u0000"}`},
	} {
		if _, err := env.invoke("matomo.users.get-access", tc.connection, tc.arguments); err == nil {
			t.Errorf("%s %s: accepted", tc.connection, tc.arguments)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
	if _, err := env.invoke("matomo.users.get-access", "two", `{"idSite":3}`); err == nil || strings.Contains(err.Error(), "3") {
		t.Errorf("refusal names the website: %v", err)
	}
}

func TestUsersGetAccessEmptyMapsAndTooManyRows(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getUsersAccessFromSite":  func(call) *http.Response { return ok(`[]`) },
		"UsersManager.getUsersSitesFromAccess": func(call) *http.Response { return ok(`[]`) },
		"UsersManager.getSitesAccessForUser": func(call) *http.Response {
			rows := make([]string, scanLimit)
			for i := range rows {
				rows[i] = `{"idsite":"9","site_name":"n","role":"view","capabilities":[]}`
			}
			return ok("[" + strings.Join(rows, ",") + "]")
		},
	}).handler(t))
	for _, tc := range []struct{ connection, arguments string }{{"two", `{"idSite":1}`}, {"mixed", `{"access":"admin"}`}} {
		result, err := env.invoke("matomo.users.get-access", tc.connection, tc.arguments)
		if err != nil || result != `{"has_more":false}` {
			t.Errorf("%s: result = %s, err = %v", tc.arguments, result, err)
		}
	}
	if _, err := env.invoke("matomo.users.get-access", "two", `{"login":"alice"}`); !isInvalidRequest(err) {
		t.Errorf("too many rows: err = %v", err)
	}
}

func TestRolesList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"UsersManager.getAvailableRoles": func(call) *http.Response {
			return ok(`[{"id":"view","name":"View","description":"Can view","helpUrl":"https://help.example.test/view"},` +
				`{"id":"admin","name":"Admin","description":"Can admin","helpUrl":""}]`)
		},
		"UsersManager.getAvailableCapabilities": func(call) *http.Response {
			return ok(`[{"id":"tagmanager_write","name":"Write","description":"d","helpUrl":"","includedInRoles":["admin"],` +
				`"category":"Tag Manager"}]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.roles.list", "one", `{}`)
	for _, want := range []string{`"id":"view"`, `"help_url":"https://help.example.test/view"`, `"id":"tagmanager_write"`,
		`"included_in_roles":["admin"]`, `"category":"Tag Manager"`} {
		if err != nil || !strings.Contains(result, want) {
			t.Errorf("missing %s in %s, err = %v", want, result, err)
		}
	}
	if len(calls) != 3 { // version, roles, capabilities
		t.Errorf("calls = %d", len(calls))
	}
	before := len(calls)
	result, err = env.invoke("matomo.roles.list", "one", `{"section":"roles"}`)
	if err != nil || strings.Contains(result, "capabilities") || len(calls)-before != 1 ||
		calls[len(calls)-1].api() != "UsersManager.getAvailableRoles" {
		t.Errorf("result = %s, err = %v", result, err)
	}
	for _, bad := range []struct{ connection, arguments string }{{"one", `{"section":"x"}`}, {"instance", `{}`}} {
		if _, err := env.invoke("matomo.roles.list", bad.connection, bad.arguments); err == nil {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
}

func TestUsersToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	invocations := []struct{ tool, connection, arguments, method string }{
		{"matomo.users.list", "two", `{"idSite":1}`, "UsersManager.getUsersPlusRole"},
		{"matomo.users.get", "instance", `{"login":"alice"}`, "UsersManager.getUser"},
		{"matomo.users.get-access", "two", `{"login":"alice"}`, "UsersManager.getSitesAccessForUser"},
		{"matomo.roles.list", "two", `{}`, "UsersManager.getAvailableRoles"},
	}
	for _, in := range invocations {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission": {"You can't access this resource as it requires 'superuser' access.", provider.ClassPermission, false},
			"no user":    {"User 'alice' doesn't exist.", provider.ClassNotFound, false},
			"no method":  {"The method 'UsersManager.x' does not exist or is not available in the module 'UsersManager'.", "", true},
			"no plugin":  {"The plugin UsersManager is not activated.", "", true},
		} {
			var calls []call
			env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
				in.method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.invoke(in.tool, in.connection, in.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || classOf(err) != tc.class || isUnsupported(err) != tc.missing {
				t.Errorf("%s %s: err = %v", in.tool, name, err)
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(429, bodyCanary), nil })
		if _, err := env.invoke(in.tool, in.connection, in.arguments); classOf(err) != provider.ClassRateLimited ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s 429: err = %v", in.tool, err)
		}
		env = newEnvironment(t, &calls, func(call) (*http.Response, error) { return nil, context.DeadlineExceeded })
		if _, err := env.invoke(in.tool, in.connection, in.arguments); classOf(err) != provider.ClassTimeout {
			t.Errorf("%s timeout: err = %v", in.tool, err)
		}
		for name, body := range map[string]string{"html": `<html>` + bodyCanary + `</html>`, "wrong shape": `{"x":1}`, "scalar": `7`} {
			env = newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == "API.getMatomoVersion" {
					return ok(versionAnswer), nil
				}
				return ok(body), nil
			})
			if _, err := env.invoke(in.tool, in.connection, in.arguments); classOf(err) != provider.ClassInvalidResponse ||
				strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v", in.tool, name, err)
			}
		}
	}
}
