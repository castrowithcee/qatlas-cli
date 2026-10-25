package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var globalDefaultCases = []struct {
	op, method, args, want string
	sent                   map[string]string
	bad                    []string
}{
	{"matomo.global-settings.set-search-parameters", "SitesManager.setGlobalSearchParameters",
		`{"searchKeywordParameters":["q"," search"],"searchCategoryParameters":[]}`,
		`{"searchKeywordParameters":["q","search"],"updated":true}`,
		map[string]string{"searchKeywordParameters": "q,search", "searchCategoryParameters": ""},
		[]string{`{"searchKeywordParameters":["q"]}`, `{"searchKeywordParameters":["a,b"],"searchCategoryParameters":[]}`,
			`{"searchKeywordParameters":["a b"],"searchCategoryParameters":[]}`,
			`{"searchKeywordParameters":null,"searchCategoryParameters":[]}`,
			`{"searchKeywordParameters":[],"searchCategoryParameters":[""]}`,
			`{"searchKeywordParameters":[],"searchCategoryParameters":[],"x":1}`, `{}`}},
	{"matomo.global-settings.set-keep-url-fragments", "SitesManager.setKeepURLFragmentsGlobal",
		`{"enabled":true}`, `{"enabled":true,"updated":true}`, map[string]string{"enabled": "1"},
		[]string{`{}`, `{"enabled":"yes"}`, `{"enabled":1}`, `{"enabled":true,"x":1}`}},
	{"matomo.global-settings.set-default-currency", "SitesManager.setDefaultCurrency",
		`{"defaultCurrency":"EUR"}`, `{"defaultCurrency":"EUR","updated":true}`,
		map[string]string{"defaultCurrency": "EUR"},
		[]string{`{}`, `{"defaultCurrency":"eur"}`, `{"defaultCurrency":"EURO"}`, `{"defaultCurrency":"E R"}`}},
	{"matomo.global-settings.set-default-timezone", "SitesManager.setDefaultTimezone",
		`{"defaultTimezone":"Europe/Paris"}`, `{"defaultTimezone":"Europe/Paris","updated":true}`,
		map[string]string{"defaultTimezone": "Europe/Paris"},
		[]string{`{}`, `{"defaultTimezone":""}`, `{"defaultTimezone":"A B"}`, `{"defaultTimezone":"A\nB"}`,
			`{"defaultTimezone":"` + strings.Repeat("a", 65) + `"}`, `{"defaultTimezone":5}`}},
}

func globalDefaultAnswer(body string) answers {
	reply := func(call) *http.Response { return ok(body) }
	out := answers{}
	for _, c := range globalDefaultCases {
		out[c.method] = reply
	}
	return out
}

func TestGlobalDefaultSettersSendOneFixedRequest(t *testing.T) {
	for _, c := range globalDefaultCases {
		for _, body := range []string{`{"result":"success"}`, `{"value":true}`} {
			var calls []call
			env := newSiteURLEnvironment(t, &calls, globalDefaultAnswer(body))
			result, err := env.change(c.op, "sw-inst", c.args)
			if err != nil || result != c.want {
				t.Fatalf("%s: %s, %v", c.op, result, err)
			}
			sent := calls[len(calls)-1]
			if sent.api() != c.method || countMethod(calls, c.method) != 1 || !strings.Contains(sent.auth, "Bearer") ||
				sent.form.Has("token_auth") || sent.rawQuery != "" && strings.Contains(sent.rawQuery, "token") {
				t.Errorf("%s: request = %v auth=%q query=%q", c.op, sent.form, sent.auth, sent.rawQuery)
			}
			for field, value := range c.sent {
				if !sent.form.Has(field) || sent.form.Get(field) != value || strings.Contains(sent.rawQuery, field) {
					t.Errorf("%s: %s = %q in %v", c.op, field, sent.form.Get(field), sent.form)
				}
			}
		}
	}
}

func TestGlobalDefaultSettersRefuseBeforeSecretsAndIO(t *testing.T) {
	var calls []call
	env := newSiteURLEnvironment(t, &calls, globalDefaultAnswer(`{"result":"success"}`))
	for _, c := range globalDefaultCases {
		if _, err := env.change(c.op, "sw-only-inst", c.args); err == nil {
			t.Errorf("%s: accepted without the instance target", c.op)
		}
		if _, err := env.invoke(c.op, "sw-inst", c.args); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s: err = %v, want a confirmation request", c.op, err)
		}
		for _, args := range c.bad {
			_, err := env.change(c.op, "sw-inst", args)
			if !isInvalidRequest(err) {
				t.Errorf("%s %.60s: err = %v", c.op, args, err)
			}
		}
		if ok, _ := reaches(c.op, []string{"site"}); ok {
			t.Errorf("%s reaches a connection without instance", c.op)
		}
		if ok, _ := reaches(c.op, []string{"instance"}); !ok {
			t.Errorf("%s does not reach an instance connection", c.op)
		}
	}
	// A rejected value is never repeated.
	_, err := env.change("matomo.global-settings.set-default-currency", "sw-inst", `{"defaultCurrency":"zzcanaryzz"}`)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls), *env.reads)
	}
}

func TestGlobalDefaultSettersMapErrorsAndNeverRetry(t *testing.T) {
	for _, c := range globalDefaultCases {
		var calls []call
		env := newSiteURLEnvironment(t, &calls, answers{c.method: func(call) *http.Response {
			return failure("You can't access this resource as it requires 'superuser' access." + bodyCanary)
		}})
		if _, err := env.change(c.op, "sw-inst", c.args); classOf(err) != provider.ClassPermission ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s permission: err = %v", c.op, err)
		}
		calls = nil
		env = newSiteURLEnvironment(t, &calls, answers{c.method: func(call) *http.Response {
			return failure("The method '" + c.method + "' does not exist or is not available in the module 'SitesManager'.")
		}})
		if _, err := env.change(c.op, "sw-inst", c.args); !isUnsupported(err) || countMethod(calls, c.method) != 1 {
			t.Errorf("%s missing method: err = %v", c.op, err)
		}
		for name, handler := range map[string]func(call) *http.Response{
			"html":     func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
			"server":   func(call) *http.Response { return respond(500, bodyCanary) },
			"rate":     func(call) *http.Response { return respond(429, bodyCanary) },
			"no ack":   func(call) *http.Response { return ok(`{"value":false}`) },
			"no words": func(call) *http.Response { return ok(`[]`) },
		} {
			var calls []call
			env := newSiteURLEnvironment(t, &calls, answers{c.method: handler})
			_, err := env.change(c.op, "sw-inst", c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", c.op, name, err, countMethod(calls, c.method))
			}
			if name != "rate" && (err == nil || !strings.Contains(err.Error(), "never repeated")) {
				t.Errorf("%s %s: no uncertainty reported: %v", c.op, name, err)
			}
		}
		var timed []call
		manage := newSiteManageEnvironment(t, &timed, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return siteManageAnswers(answers{}).handler(t)(x)
		})
		if _, err := manage.change(c.op, "sm-writer", c.args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(timed, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.op, err)
		}
	}
}

func TestGlobalDefaultSettersAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, op := range globalDefaultOperations() {
		d, _, found := reg.Lookup(op.Descriptor.ID)
		if !found || d.Risk.Effect != "update" || d.Risk.Idempotency != "idempotent" ||
			d.Risk.Confirmation != "required" || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Fatalf("%s: found %t, %+v", op.Descriptor.ID, found, d.Risk)
		}
		for _, profile := range metadata.Profiles {
			if strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+op.Descriptor.ID+" ") {
				t.Errorf("profile %s holds %s", profile.ID, op.Descriptor.ID)
			}
		}
	}
}
