package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Assumptions, as the CustomReports reference gives no example for the metric write methods: addCustomMetric
// answers the ID of the new metric (bare or as {"value":N}), updateCustomMetric and deleteCustomMetric answer the
// generic success form {"result":"success","message":"ok"}. The reference names no allowed formatType values
// beyond the default number and no range for decimals, so the tool bounds their form and Matomo decides.
const customMetricConnections = `
  cm-admin: {service: matomo, credential: token, effects: [read, create], targets: [instance]}
  cm-admin-sites: {service: matomo, credential: token, effects: [read, create], targets: [instance, site/1]}
  cm-deleter: {service: matomo, credential: token, tools: [matomo.custom-metrics.delete], targets: [instance]}
  cm-websites-only: {service: matomo, credential: token, effects: [read, create], targets: [site/1]}
  cm-websites-deleter: {service: matomo, credential: token, tools: [matomo.custom-metrics.delete], targets: [site/1]}
  cm-effects-delete: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [instance]}
`

func newCustomMetricChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(customMetricConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func customMetricChangeAnswers(extra answers) answers {
	return customReportAnswers(with(answers{
		"CustomReports.addCustomMetric":    fixed(`{"value":7}`),
		"CustomReports.updateCustomMetric": fixed(`{"result":"success","message":"ok"}`),
		"CustomReports.deleteCustomMetric": fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

const metricDefinition = `"name":"Pages per visit","formula":"pageviews / nb_visits"`

var customMetricChanges = []struct{ tool, connection, arguments, method, reads string }{
	{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `}`, "CustomReports.addCustomMetric", ""},
	{"matomo.custom-metrics.save", "cm-admin", `{"idCustomMetric":1,` + metricDefinition + `}`,
		"CustomReports.updateCustomMetric", "CustomReports.getCustomMetrics"},
	{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":1}`,
		"CustomReports.deleteCustomMetric", "CustomReports.getCustomMetrics"},
}

func TestCustomMetricChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		listed      bool
	}{
		"matomo.custom-metrics.save":   {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"matomo.custom-metrics.delete": {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	}
	for tool, w := range want {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != customReportsSensitive || d.RequiresToolAllowList != w.listed ||
			!strings.Contains(d.Description, "never repeated") || !instanceTools[tool] {
			t.Errorf("%s: risk = %+v, description %q", tool, d.Risk, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "idSite"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", tool, banned)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool {
					t.Errorf("profile %s selects %s", profile.ID, tool)
				}
			}
		}
	}
}

func TestCustomMetricSaveCallsTheMethodForAddOrUpdateWithTheGivenParametersOnly(t *testing.T) {
	var calls []call
	env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(nil).handler(t))
	full := `"name":"Pages per visit","formula":"pageviews / nb_visits","formatType":"number","decimals":2,"description":"Ratio"`
	for _, tc := range []struct {
		tool, connection, arguments, method, reads string
		want                                       map[string]string
		result                                     string
	}{
		{"matomo.custom-metrics.save", "cm-admin", `{` + full + `}`, "CustomReports.addCustomMetric", "",
			map[string]string{"name": "Pages per visit", "formula": "pageviews / nb_visits", "formatType": "number", "decimals": "2",
				"description": "Ratio"}, `{"created":true,"idCustomMetric":7,"saved":true}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `}`, "CustomReports.addCustomMetric", "",
			map[string]string{"name": "Pages per visit", "formula": "pageviews / nb_visits"},
			`{"created":true,"idCustomMetric":7,"saved":true}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"idCustomMetric":2,` + full + `}`, "CustomReports.updateCustomMetric",
			"CustomReports.getCustomMetrics", map[string]string{"idCustomMetric": "2", "name": "Pages per visit",
				"formula": "pageviews / nb_visits", "formatType": "number", "decimals": "2", "description": "Ratio"},
			`{"created":false,"idCustomMetric":2,"saved":true}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"idCustomMetric":1,` + metricDefinition + `,"decimals":0,"description":""}`,
			"CustomReports.updateCustomMetric", "CustomReports.getCustomMetrics", map[string]string{"idCustomMetric": "1",
				"name": "Pages per visit", "formula": "pageviews / nb_visits", "decimals": "0", "description": ""},
			`{"created":false,"idCustomMetric":1,"saved":true}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":2}`, "CustomReports.deleteCustomMetric",
			"CustomReports.getCustomMetrics", map[string]string{"idCustomMetric": "2"}, `{"deleted":true,"idCustomMetric":2}`},
	} {
		calls = nil
		if _, err := env.invoke(tc.tool, tc.connection, tc.arguments); err == nil || !strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", tc.tool, err, len(calls))
		}
		result, err := env.change(tc.tool, tc.connection, tc.arguments)
		if err != nil || result != tc.result {
			t.Fatalf("%s %s: result = %s, err = %v", tc.tool, tc.arguments, result, err)
		}
		got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated ")
		if want := strings.TrimSpace(tc.reads + " " + tc.method); got != want {
			t.Errorf("%s: requests = %s, want %s", tc.tool, got, want)
		}
		for _, other := range []string{"CustomReports.addCustomMetric", "CustomReports.updateCustomMetric", "CustomReports.deleteCustomMetric"} {
			if n := countMethod(calls, other); n != map[bool]int{true: 1, false: 0}[other == tc.method] {
				t.Errorf("%s: %s sent %d times", tc.tool, other, n)
			}
		}
		form := lastCall(calls, tc.method).form
		for key, value := range tc.want {
			if !form.Has(key) || form.Get(key) != value {
				t.Errorf("%s: %s = %q, want %q (form %v)", tc.tool, key, form.Get(key), value, form)
			}
		}
		if len(form) != len(tc.want)+3 || form.Get("format") != "JSON" || form.Get("module") != "API" || form.Get("method") != tc.method {
			t.Errorf("%s: form = %v", tc.tool, form)
		}
	}
}

func TestCustomMetricChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.custom-metrics.save", "cm-websites-only", `{` + metricDefinition + `}`},
		{"matomo.custom-metrics.save", "cm-websites-only", `{"idCustomMetric":1,` + metricDefinition + `}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"formula":"a"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"a"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"","formula":"a"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"a","formula":" "}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"` + strings.Repeat("n", 256) + `","formula":"a"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"a","formula":"` + strings.Repeat("f", 2049) + `"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"a\u0000b","formula":"a"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"name":"a","formula":"a\nb"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"formatType":"a b"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"formatType":""}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"formatType":"` + strings.Repeat("f", 33) + `"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"decimals":-1}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"decimals":11}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"decimals":"2"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"description":"` + strings.Repeat("d", 2049) + `"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"idCustomMetric":0,` + metricDefinition + `}`},
		{"matomo.custom-metrics.save", "cm-admin", `{"idCustomMetric":2147483648,` + metricDefinition + `}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"idSite":1}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"method":"CustomReports.deleteCustomMetric"}`},
		{"matomo.custom-metrics.save", "cm-admin", `{` + metricDefinition + `,"callback":"x"}`},
		{"matomo.custom-metrics.delete", "cm-websites-deleter", `{"idCustomMetric":1}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":0}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":2147483648}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":1,"idSite":1}`},
		{"matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":"1"}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "instance") && strings.Contains(c.connection, "websites") &&
			strings.Contains(err.Error(), "site/1") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestCustomMetricChangeRefusesAnUnknownMetricAfterOneReadWithoutMutation(t *testing.T) {
	for _, c := range customMetricChanges {
		if c.reads == "" {
			continue
		}
		var calls []call
		env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(nil).handler(t))
		for _, id := range []string{"3", "99", "2147483647"} {
			calls = nil
			arguments := strings.Replace(c.arguments, `"idCustomMetric":1`, `"idCustomMetric":`+id, 1)
			_, err := env.change(c.tool, c.connection, arguments)
			if err == nil || classOf(err) != provider.ClassNotFound || countMethod(calls, c.method) != 0 ||
				countMethod(calls, c.reads) != 1 || strings.Contains(err.Error(), id) {
				t.Errorf("%s metric %s: requests = %s, err = %v", c.tool, id, methods(calls), err)
			}
		}
		// A list that is not a list of metrics is no proof.
		calls = nil
		env = newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(answers{
			"CustomReports.getCustomMetrics": fixed(`"nope"`)}).handler(t))
		if _, err := env.change(c.tool, c.connection, c.arguments); err == nil || countMethod(calls, c.method) != 0 {
			t.Errorf("%s with an invalid list: requests = %s, err = %v", c.tool, methods(calls), err)
		}
	}
}

func TestCustomMetricChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(nil).handler(t))
	for _, c := range customMetricChanges {
		if _, err := env.change(c.tool, c.connection, c.arguments); err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
	}
	for _, c := range calls {
		if c.auth != "Bearer "+tokenValue || c.rawQuery != "" || c.method != http.MethodPost ||
			strings.Contains(c.body, tokenValue) || c.form.Has("token_auth") || c.form.Get("format") != "JSON" {
			t.Errorf("%s: auth %q, query %q, body %q", c.api(), c.auth, c.rawQuery, c.body)
		}
		for _, banned := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "filter_limit"} {
			if c.form.Has(banned) {
				t.Errorf("%s sends %s", c.api(), banned)
			}
		}
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redacted text = %q", got)
	}
}

func TestCustomMetricChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range customMetricChanges {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
			"not found": {func(call) (*http.Response, error) {
				return ok2(failure("The requested custom metric does not exist." + bodyCanary))
			}, provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'CustomReports'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin CustomReports is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
			"server":     {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, true},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, true},
			"no confirmation": {func(call) (*http.Response, error) {
				return ok2(ok(`{"value":"none"}`))
			}, provider.ClassInvalidResponse, false, true},
		} {
			var calls []call
			env := newCustomMetricChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return customMetricChangeAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, c.connection, c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s %s: err = %v (class %q), %s sent %d times", c.tool, c.method, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestCustomMetricSaveAcceptsABareIDAndRefusesNone(t *testing.T) {
	for answer, want := range map[string]string{`21`: `{"created":true,"idCustomMetric":21,"saved":true}`, `{"value":0}`: "", `true`: ""} {
		var calls []call
		env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(answers{"CustomReports.addCustomMetric": fixed(answer)}).handler(t))
		result, err := env.change("matomo.custom-metrics.save", "cm-admin", customMetricChanges[0].arguments)
		if want != "" && (err != nil || result != want) || want == "" && (err == nil || !strings.Contains(err.Error(), "never repeated")) ||
			countMethod(calls, "CustomReports.addCustomMetric") != 1 {
			t.Errorf("answer %s: result = %s, err = %v", answer, result, err)
		}
	}
}

func TestCustomMetricChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range customMetricChanges {
		var calls []call
		env := newCustomMetricChangeEnvironment(t, &calls, with(customMetricChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestCustomMetricDeleteIsReachableOnlyThroughTheToolsListAndSaveNotWithoutInstance(t *testing.T) {
	var calls []call
	env := newCustomMetricChangeEnvironment(t, &calls, customMetricChangeAnswers(nil).handler(t))
	if _, err := env.change("matomo.custom-metrics.delete", "cm-effects-delete", `{"idCustomMetric":1}`); err == nil ||
		len(calls) != 0 || *env.reads != 0 {
		t.Errorf("through effects only: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change("matomo.custom-metrics.delete", "cm-deleter", `{"idCustomMetric":1}`); err != nil {
		t.Errorf("through the tools list: %v", err)
	}
	// A connection that binds the instance and a website keeps the instance tool: the target settles it.
	if _, err := env.change("matomo.custom-metrics.save", "cm-admin-sites", `{`+metricDefinition+`}`); err != nil {
		t.Errorf("instance and website: %v", err)
	}
}
