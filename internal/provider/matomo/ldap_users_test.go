package matomo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const ldapConnections = `
  ldap-admin: {service: matomo, credential: token, tools: [matomo.ldap-users.count, matomo.ldap-users.list], targets: [instance]}
  ldap-site: {service: matomo, credential: token, tools: [matomo.ldap-users.count, matomo.ldap-users.list], targets: [site/1]}
  ldap-open: {service: matomo, credential: token, effects: [read], targets: [instance]}
`

func newLDAPEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	serve(t, calls, a.handler(t))
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(ldapConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

const ldapFilterCanary = "(uid=filter-canary-77)"

func TestLDAPUsersCountSendsOneFixedMethod(t *testing.T) {
	for _, tc := range []struct{ arguments, method, param, value string }{
		{`{"filter":"` + ldapFilterCanary + `"}`, "LoginLdap.getCountOfUsersMatchingFilter", "filter", ldapFilterCanary},
		{`{"memberOf":"cn=Matomo,dc=x"}`, "LoginLdap.getCountOfUsersMemberOf", "memberOf", "cn=Matomo,dc=x"},
	} {
		var calls []call
		env := newLDAPEnvironment(t, &calls, with(pluginsOn(), answers{
			tc.method: func(call) *http.Response { return ok(`{"value":42}`) },
		}))
		result, err := env.invoke("matomo.ldap-users.count", "ldap-admin", tc.arguments)
		if err != nil || result != `{"count":42}` {
			t.Fatalf("%s: result = %s, err = %v", tc.method, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.api() != tc.method || sent.form.Get(tc.param) != tc.value || sent.auth != "Bearer "+tokenValue ||
			sent.rawQuery != "" || sent.form.Get("format") != "JSON" || sent.form.Has("token_auth") ||
			sent.form.Has("callback") || sent.form.Has("filter_limit") || len(sent.form) > 4 {
			t.Errorf("request = %+v", sent)
		}
		if got := env.red.Apply("x " + tokenValue); strings.Contains(got, tokenValue) {
			t.Error("the token is not registered with the redactor")
		}
	}
}

func TestLDAPUsersListIsPagedAndBounded(t *testing.T) {
	var calls []call
	long := strings.Repeat("l", 300)
	env := newLDAPEnvironment(t, &calls, with(pluginsOn(), answers{
		"LoginLdap.getExistingLdapUsersFromDb": func(call) *http.Response { return ok(`["carol","alice","` + long + `","bob"]`) },
	}))
	result, err := env.invoke("matomo.ldap-users.list", "ldap-admin", `{"limit":2}`)
	if err != nil || !strings.HasPrefix(result, `{"has_more":true,"logins":["alice","bob"]`) || !strings.Contains(result, "next_cursor") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.api() != "LoginLdap.getExistingLdapUsersFromDb" || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
		sent.form.Get("format") != "JSON" || sent.form.Has("token_auth") || sent.form.Has("filter_limit") {
		t.Errorf("request = %+v", sent)
	}
	result, err = env.invoke("matomo.ldap-users.list", "ldap-admin", `{}`)
	if err != nil || strings.Contains(result, long) || !strings.Contains(result, `"has_more":false`) {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestLDAPUsersRefusalsSendNothing(t *testing.T) {
	var calls []call
	env := newLDAPEnvironment(t, &calls, with(pluginsOn(), answers{
		"LoginLdap.getCountOfUsersMatchingFilter": func(call) *http.Response { return ok(`1`) },
		"LoginLdap.getCountOfUsersMemberOf":       func(call) *http.Response { return ok(`1`) },
		"LoginLdap.getExistingLdapUsersFromDb":    func(call) *http.Response { return ok(`[]`) },
	}))
	before, readsBefore := len(calls), *env.reads
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.ldap-users.count", "ldap-site", `{"filter":"(a=b)"}`},
		{"matomo.ldap-users.count", "open", `{"filter":"(a=b)"}`},
		{"matomo.ldap-users.list", "ldap-site", `{}`},
		{"matomo.ldap-users.list", "ldap-open", `{}`},
		{"matomo.ldap-users.count", "ldap-open", `{"filter":"(a=b)"}`},
		{"matomo.ldap-users.count", "ldap-admin", `{}`},
		{"matomo.ldap-users.count", "ldap-admin", `{"filter":"(a=b)","memberOf":"x"}`},
		{"matomo.ldap-users.count", "ldap-admin", `{"filter":""}`},
		{"matomo.ldap-users.count", "ldap-admin", `{"memberOf":"a\nb"}`},
		{"matomo.ldap-users.count", "ldap-admin", `{"filter":"` + strings.Repeat("a", 501) + `"}`},
		{"matomo.ldap-users.count", "ldap-admin", `{"filter":"(a=b)","idSite":1}`},
		{"matomo.ldap-users.list", "ldap-admin", `{"limit":0,"cursor":"bad"}`},
		{"matomo.ldap-users.list", "ldap-admin", `{"idSite":1}`},
	} {
		_, err := env.invoke(tc.tool, tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s %s ran", tc.tool, tc.connection, tc.arguments)
		}
	}
	for _, args := range []string{`{}`, `{"filter":"(a=b)","memberOf":"x"}`} {
		if _, err := env.invoke("matomo.ldap-users.count", "ldap-admin", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid-request", args, err)
		}
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-readsBefore)
	}
	for _, id := range []string{"matomo.ldap-users.count", "matomo.ldap-users.list"} {
		listed, _, _ := registry(t).Lookup(id)
		if !listed.RequiresToolAllowList {
			t.Errorf("%s is not listed-only", id)
		}
		if ok, _ := reaches(id, []string{"site"}); ok {
			t.Errorf("%s reaches a site binding", id)
		}
	}
}

// The invoke log records no arguments, so a filter can only leak through a result or an error.
func TestLDAPFilterNeverReachesErrorsOrResults(t *testing.T) {
	var calls []call
	env := newLDAPEnvironment(t, &calls, with(pluginsOn(), answers{
		"LoginLdap.getCountOfUsersMatchingFilter": func(call) *http.Response { return failure("Invalid filter. " + ldapFilterCanary) },
	}))
	_, err := env.invoke("matomo.ldap-users.count", "ldap-admin", `{"filter":"`+ldapFilterCanary+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "filter-canary") {
		t.Errorf("err = %v", err)
	}
}

func TestLDAPUsersMapErrorsAndPluginChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		answer  func(call) (*http.Response, error)
		class   provider.Class
		invalid bool
	}{
		"timeout":      {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
		"unreadable":   {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, false},
		"not a number": {func(call) (*http.Response, error) { return ok(`{"value":"many"}`), nil }, provider.ClassInvalidResponse, false},
		"permission": {func(call) (*http.Response, error) {
			return ok2(failure("You can't access this resource as it requires 'superuser' access. " + bodyCanary))
		}, provider.ClassPermission, false},
		"invalid": {func(call) (*http.Response, error) { return ok2(failure("Invalid filter.")) }, "", true},
		"ldap down": {func(call) (*http.Response, error) {
			return ok2(failure("Could not connect to the LDAP server. " + bodyCanary))
		}, provider.ClassProviderError, false},
		"unknown": {func(call) (*http.Response, error) { return ok2(failure("odd " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		"429":     {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false},
	} {
		for _, tool := range []struct{ id, arguments, method string }{
			{"matomo.ldap-users.count", `{"filter":"(a=b)"}`, "LoginLdap.getCountOfUsersMatchingFilter"},
			{"matomo.ldap-users.list", `{}`, "LoginLdap.getExistingLdapUsersFromDb"},
		} {
			var calls []call
			env := newLDAPEnvironment(t, &calls, nil)
			serve(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == tool.method {
					return tc.answer(c)
				}
				return pluginsOn().handler(t)(c)
			})
			_, err := env.invoke(tool.id, "ldap-admin", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.invalid && !isInvalidRequest(err) ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", name, tool.id, err)
			}
			if name == "429" && strings.Contains(err.Error(), "Retry-After") {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	}

	for name, handler := range map[string]answers{
		"inactive": with(versionOnly("5.14.2"), answers{"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) }}),
		"missing":  with(versionOnly("5.14.2"), answers{"API.isPluginActivated": func(call) *http.Response { return failure("The plugin LoginLdap is not activated.") }}),
	} {
		for _, tool := range []struct{ id, arguments string }{
			{"matomo.ldap-users.count", `{"memberOf":"x"}`}, {"matomo.ldap-users.list", `{}`}} {
			var calls []call
			env := newLDAPEnvironment(t, &calls, handler)
			_, err := env.invoke(tool.id, "ldap-admin", tool.arguments)
			if !isUnsupported(err) || countMethod(calls, "LoginLdap.getCountOfUsersMemberOf")+
				countMethod(calls, "LoginLdap.getExistingLdapUsersFromDb") != 0 {
				t.Errorf("%s %s: err = %v", name, tool.id, err)
			}
		}
	}
}
