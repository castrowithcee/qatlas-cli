package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var globalSetCases = []struct{ op, method, args, want, field, sent string }{
	{"matomo.global-settings.set-excluded-ips", "SitesManager.setGlobalExcludedIps",
		`{"excludedIps":["192.0.2.1"," 198.51.100.0/24","10.1.*.*","2001:db8::1"]}`,
		`{"excludedIps":["192.0.2.1","198.51.100.0/24","10.1.*.*","2001:db8::1"],"updated":true}`,
		"excludedIps", "192.0.2.1,198.51.100.0/24,10.1.*.*,2001:db8::1"},
	{"matomo.global-settings.set-excluded-user-agents", "SitesManager.setGlobalExcludedUserAgents",
		`{"excludedUserAgents":["Example Bot","crawler"]}`, `{"excludedUserAgents":["Example Bot","crawler"],"updated":true}`,
		"excludedUserAgents", "Example Bot,crawler"},
	{"matomo.global-settings.set-excluded-referrers", "SitesManager.setGlobalExcludedReferrers",
		`{"excludedReferrers":["spam.example.test",".t.example.test"]}`,
		`{"excludedReferrers":["spam.example.test",".t.example.test"],"updated":true}`,
		"excludedReferrers", "spam.example.test,.t.example.test"},
}

func globalSetAnswer(body string) answers {
	reply := func(call) *http.Response { return ok(body) }
	return answers{"SitesManager.setGlobalExcludedIps": reply, "SitesManager.setGlobalExcludedUserAgents": reply,
		"SitesManager.setGlobalExcludedReferrers": reply, "SitesManager.setGlobalQueryParamExclusion": reply}
}

func TestGlobalExclusionSettersSendOneFixedRequest(t *testing.T) {
	for _, c := range globalSetCases {
		var calls []call
		env := newSiteURLEnvironment(t, &calls, globalSetAnswer(`{"result":"success"}`))
		result, err := env.change(c.op, "sw-inst", c.args)
		if err != nil || result != c.want {
			t.Fatalf("%s: %s, %v", c.op, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.api() != c.method || countMethod(calls, c.method) != 1 || sent.form.Get(c.field) != c.sent ||
			!strings.Contains(sent.auth, "Bearer") || strings.Contains(sent.rawQuery, c.field) ||
			sent.form.Has("token_auth") || len(sent.form) > 4 {
			t.Errorf("%s: request = %v auth=%q query=%q", c.op, sent.form, sent.auth, sent.rawQuery)
		}
		// Matomo's bool answer is read too, and an empty list clears the setting.
		env = newSiteURLEnvironment(t, &calls, globalSetAnswer(`{"value":true}`))
		if _, err := env.change(c.op, "sw-inst", `{"`+c.field+`":[]}`); err != nil {
			t.Errorf("%s empty: %v", c.op, err)
		}
		if got := calls[len(calls)-1]; !got.form.Has(c.field) || got.form.Get(c.field) != "" {
			t.Errorf("%s empty: form = %v", c.op, got.form)
		}
	}
}

func TestGlobalQueryParameterSetterSendsTheTypeAndTheList(t *testing.T) {
	const op = "matomo.global-settings.set-excluded-query-parameters"
	var calls []call
	env := newSiteURLEnvironment(t, &calls, globalSetAnswer(`{"result":"success"}`))
	result, err := env.change(op, "sw-inst", `{"exclusionType":"custom","queryParameters":["sid"," token"]}`)
	if err != nil || result != `{"exclusionType":"custom","queryParameters":["sid","token"],"updated":true}` {
		t.Fatalf("%s, %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.api() != "SitesManager.setGlobalQueryParamExclusion" || sent.form.Get("exclusionType") != "custom" ||
		sent.form.Get("queryParamsToExclude") != "sid,token" || !strings.Contains(sent.auth, "Bearer") ||
		sent.rawQuery != "" && strings.Contains(sent.rawQuery, "queryParams") {
		t.Errorf("request = %v", sent.form)
	}
	result, err = env.change(op, "sw-inst", `{"exclusionType":"matomo_recommended_pii"}`)
	if err != nil || result != `{"exclusionType":"matomo_recommended_pii","updated":true}` {
		t.Fatalf("%s, %v", result, err)
	}
	if got := calls[len(calls)-1]; got.form.Has("queryParamsToExclude") {
		t.Errorf("list sent for a Matomo type: %v", got.form)
	}
}

func TestGlobalExclusionSettersRefuseBeforeSecretsAndIO(t *testing.T) {
	var calls []call
	env := newSiteURLEnvironment(t, &calls, globalSetAnswer(`{"result":"success"}`))
	ops := []string{"matomo.global-settings.set-excluded-ips", "matomo.global-settings.set-excluded-user-agents",
		"matomo.global-settings.set-excluded-referrers", "matomo.global-settings.set-excluded-query-parameters"}
	valid := map[string]string{ops[0]: `{"excludedIps":[]}`, ops[1]: `{"excludedUserAgents":[]}`,
		ops[2]: `{"excludedReferrers":[]}`, ops[3]: `{"exclusionType":"common_session_parameters"}`}
	for _, op := range ops {
		if _, err := env.change(op, "sw-only-inst", valid[op]); err == nil {
			t.Errorf("%s: accepted without the instance target", op)
		}
		if _, err := env.invoke(op, "sw-inst", valid[op]); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s: err = %v, want a confirmation request", op, err)
		}
	}
	bad := map[string][]string{
		ops[0]: {`{"excludedIps":["not-an-ip"]}`, `{"excludedIps":["1.2.3.4,5.6.7.8"]}`, `{"excludedIps":["300.1.1.*"]}`,
			`{"excludedIps":["*.1.1.1"]}`, `{"excludedIps":[""]}`, `{"excludedIps":null}`, `{}`,
			`{"excludedIps":["fe80::1%eth0"]}`, `{"excludedIps":["1.1.1.1"],"x":1}`},
		ops[1]: {`{"excludedUserAgents":["a,b"]}`, `{"excludedUserAgents":["a\nb"]}`, `{"excludedUserAgents":["  "]}`},
		ops[2]: {`{"excludedReferrers":["a b"]}`, `{"excludedReferrers":["a,b"]}`},
		ops[3]: {`{"exclusionType":"custom"}`, `{"exclusionType":"custom","queryParameters":[]}`,
			`{"exclusionType":"custom","queryParameters":["a,b"]}`, `{"exclusionType":"matomo_recommended_pii","queryParameters":["a"]}`,
			`{"exclusionType":"other"}`, `{}`},
	}
	long := `"` + strings.Repeat("a", 257) + `"`
	bad[ops[1]] = append(bad[ops[1]], `{"excludedUserAgents":[`+long+`]}`)
	many := make([]string, 501)
	for i := range many {
		many[i] = `"a"`
	}
	bad[ops[2]] = append(bad[ops[2]], `{"excludedReferrers":[`+strings.Join(many, ",")+`]}`)
	wide := make([]string, 40)
	for i := range wide {
		wide[i] = `"` + strings.Repeat("b", 250) + `"`
	}
	bad[ops[1]] = append(bad[ops[1]], `{"excludedUserAgents":[`+strings.Join(wide, ",")+`]}`)
	for op, list := range bad {
		for _, args := range list {
			_, err := env.change(op, "sw-inst", args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "not-an-ip") || strings.Contains(err.Error(), "300.1") {
				t.Errorf("%s %.60s: err = %v", op, args, err)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls), *env.reads)
	}
	for _, id := range ops {
		if ok, _ := reaches(id, []string{"site"}); ok {
			t.Errorf("%s reaches a connection without instance", id)
		}
		if ok, _ := reaches(id, []string{"instance"}); !ok {
			t.Errorf("%s does not reach an instance connection", id)
		}
	}
}

func TestGlobalExclusionSettersMapErrorsAndNeverRetry(t *testing.T) {
	for _, c := range globalSetCases {
		args := c.args
		for name, body := range map[string]string{
			"permission": "You can't access this resource as it requires 'superuser' access." + bodyCanary,
		} {
			var calls []call
			env := newSiteURLEnvironment(t, &calls, answers{c.method: func(call) *http.Response { return failure(body) }})
			_, err := env.change(c.op, "sw-inst", args)
			if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v", c.op, name, err)
			}
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
			_, err := env.change(c.op, "sw-inst", args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", c.op, name, err, countMethod(calls, c.method))
			}
			if name != "rate" && (err == nil || !strings.Contains(err.Error(), "never repeated")) {
				t.Errorf("%s %s: no uncertainty reported: %v", c.op, name, err)
			}
		}
		var calls []call
		env := newSiteManageEnvironment(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return siteManageAnswers(answers{}).handler(t)(x)
		})
		if _, err := env.change(c.op, "sm-writer", args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.op, err)
		}
	}
}

func TestGlobalExclusionSettersMapMissingMethods(t *testing.T) {
	var calls []call
	env := newSiteURLEnvironment(t, &calls, answers{
		"SitesManager.setGlobalQueryParamExclusion": func(call) *http.Response {
			return failure("The method 'setGlobalQueryParamExclusion' does not exist or is not available in the module 'SitesManager'.")
		},
	})
	_, err := env.change("matomo.global-settings.set-excluded-query-parameters", "sw-inst",
		`{"exclusionType":"common_session_parameters"}`)
	if !isUnsupported(err) || countMethod(calls, "SitesManager.setGlobalQueryParamExclusion") != 1 {
		t.Errorf("err = %v", err)
	}
}

func TestGlobalExclusionSettersAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, op := range globalSetOperations() {
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
