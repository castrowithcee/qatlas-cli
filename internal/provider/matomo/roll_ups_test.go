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

// Assumptions, as the RollUpReporting reference gives no example and the plugin source is not public:
// getRollUps answers a list of website rows in the Matomo core convention (lower case idsite, name, timezone,
// currency) plus sourceIdSites as a list of IDs; addRollUp answers the ID of the new roll-up website (bare or as
// {"value":N}); updateRollUp answers the generic success form {"result":"success","message":"ok"}.
const rollUpConnections = `
  ru-admin: {service: matomo, credential: token, effects: [read, create, update], targets: [instance, site/1, site/2, site/5]}
  ru-site-1: {service: matomo, credential: token, effects: [read, create, update], targets: [instance, site/1]}
  ru-instance-only: {service: matomo, credential: token, effects: [read, create, update], targets: [instance]}
  ru-sites-only: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2, site/5]}
`

func newRollUpEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(rollUpConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

const rollUpRows = `[{"idsite":5,"name":"Shops","timezone":"Europe/Berlin","currency":"EUR","sourceIdSites":[1,2,987]},` +
	`{"idsite":6,"name":"Foreign roll-up","timezone":"UTC","currency":"USD","sourceIdSites":[1]},` +
	`{"idsite":"1","name":"Odd","sourceIdSites":["1","2"]}]`

func rollUpAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"RollUpReporting.getRollUps":   fixed(rollUpRows),
		"RollUpReporting.addRollUp":    fixed(`{"value":9}`),
		"RollUpReporting.updateRollUp": fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

const rollUpDefinition = `"name":"Shops","sourceIdSites":[1,2],"timezone":"Europe/Berlin","currency":"EUR"`

var rollUpChanges = []struct{ tool, arguments, method, reads string }{
	{"matomo.roll-ups.add", `{` + rollUpDefinition + `}`, "RollUpReporting.addRollUp", ""},
	{"matomo.roll-ups.update", `{"idSite":5,"name":"Shops"}`, "RollUpReporting.updateRollUp", "RollUpReporting.getRollUps"},
}

func TestRollUpToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.roll-ups.list":   {capability.EffectRead, capability.IdempotencySafe},
		"matomo.roll-ups.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.roll-ups.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
	}
	for tool, w := range want {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		changes := tool != "matomo.roll-ups.list"
		confirmation := capability.ConfirmationNone
		if changes {
			confirmation = capability.ConfirmationRequired
		}
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency || d.Risk.Confirmation != confirmation ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList || !instanceTools[tool] ||
			strings.Contains(d.Description, "never repeated") != changes {
			t.Errorf("%s: risk = %+v, description %q", tool, d.Risk, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", tool, banned)
			}
		}
		if tool != "matomo.roll-ups.update" && strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s schema offers idSite", tool)
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

func TestRollUpListFiltersRowsAndSourcesByTheTargets(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	result, err := env.invoke("matomo.roll-ups.list", "ru-admin", `{}`)
	want := `{"has_more":false,"rollUps":[{"currency":"EUR","idSite":5,"name":"Shops","sourceIdSites":[1,2],"timezone":"Europe/Berlin"},` +
		`{"idSite":1,"name":"Odd","sourceIdSites":[1,2]}]}`
	if err != nil || !sameJSON(result, want) || strings.Contains(result, "987") || strings.Contains(result, "Foreign") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated "); got != "RollUpReporting.getRollUps" {
		t.Errorf("requests = %s", methods(calls))
	}
	// A narrower connection drops the roll-up whose own website lies outside the targets.
	result, err = env.invoke("matomo.roll-ups.list", "ru-site-1", `{}`)
	if err != nil || !sameJSON(result, `{"has_more":false,"rollUps":[{"idSite":1,"name":"Odd","sourceIdSites":[1]}]}`) {
		t.Errorf("narrow result = %s, err = %v", result, err)
	}
	result, err = env.invoke("matomo.roll-ups.list", "ru-admin", `{"limit":1}`)
	if err != nil || !strings.Contains(result, `"has_more":true`) || !strings.Contains(result, "next_cursor") {
		t.Errorf("paged result = %s, err = %v", result, err)
	}
}

func TestRollUpToolsAreNotOfferedWithoutInstanceAndWebsites(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	for _, connection := range []string{"ru-instance-only", "ru-sites-only"} {
		if _, err := env.invoke("matomo.roll-ups.list", connection, `{}`); err == nil {
			t.Errorf("list on %s was accepted", connection)
		}
		for _, c := range rollUpChanges {
			if _, err := env.change(c.tool, connection, c.arguments); err == nil {
				t.Errorf("%s on %s was accepted", c.tool, connection)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
	if ok, _ := reaches("matomo.roll-ups.add", []string{"instance"}); ok {
		t.Error("add reaches an instance-only binding")
	}
	if ok, _ := reaches("matomo.roll-ups.list", []string{"site"}); ok {
		t.Error("list reaches a binding without instance")
	}
	if ok, _ := reaches("matomo.roll-ups.update", []string{"instance", "site/1"}); !ok {
		t.Error("update does not reach instance and website")
	}
}

func TestRollUpChangesSendOneRequestWithTheGivenFieldsOnly(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	for _, tc := range []struct {
		tool, arguments, method, reads, result string
		want                                   map[string]string
	}{
		{"matomo.roll-ups.add", `{` + rollUpDefinition + `}`, "RollUpReporting.addRollUp", "", `{"created":true,"idSite":9}`,
			map[string]string{"name": "Shops", "sourceIdSites[0]": "1", "sourceIdSites[1]": "2", "timezone": "Europe/Berlin", "currency": "EUR"}},
		{"matomo.roll-ups.update", `{"idSite":5,"name":"New"}`, "RollUpReporting.updateRollUp", "RollUpReporting.getRollUps",
			`{"idSite":5,"updated":true}`, map[string]string{"idSite": "5", "name": "New"}},
		{"matomo.roll-ups.update", `{"idSite":5,"sourceIdSites":[2]}`, "RollUpReporting.updateRollUp", "RollUpReporting.getRollUps",
			`{"idSite":5,"updated":true}`, map[string]string{"idSite": "5", "sourceIdSites[0]": "2"}},
		{"matomo.roll-ups.update", `{"idSite":5,"timezone":"UTC","currency":"USD"}`, "RollUpReporting.updateRollUp",
			"RollUpReporting.getRollUps", `{"idSite":5,"updated":true}`, map[string]string{"idSite": "5", "timezone": "UTC", "currency": "USD"}},
	} {
		calls = nil
		if _, err := env.invoke(tc.tool, "ru-admin", tc.arguments); err == nil || !strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", tc.tool, err, len(calls))
		}
		result, err := env.change(tc.tool, "ru-admin", tc.arguments)
		if err != nil || !sameJSON(result, tc.result) {
			t.Fatalf("%s %s: result = %s, err = %v", tc.tool, tc.arguments, result, err)
		}
		got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated ")
		if want := strings.TrimSpace(tc.reads + " " + tc.method); got != want {
			t.Errorf("%s: requests = %s, want %s", tc.tool, got, want)
		}
		if n := countMethod(calls, tc.method); n != 1 {
			t.Errorf("%s: %s sent %d times", tc.tool, tc.method, n)
		}
		form := lastCall(calls, tc.method).form
		for key, value := range tc.want {
			if form.Get(key) != value || !form.Has(key) {
				t.Errorf("%s: %s = %q, want %q (form %v)", tc.tool, key, form.Get(key), value, form)
			}
		}
		if form.Has("sourceIdSites") {
			t.Errorf("%s sends sourceIdSites as a single value: %v", tc.tool, form)
		}
		if len(form) != len(tc.want)+3 || form.Get("format") != "JSON" || form.Get("module") != "API" || form.Get("method") != tc.method {
			t.Errorf("%s: form = %v", tc.tool, form)
		}
	}
}

func TestRollUpChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		// foreign source or roll-up website, never quoted
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1,987],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-site-1", `{"name":"S","sourceIdSites":[1,2],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":987,"name":"S"}`},
		{"matomo.roll-ups.update", "ru-site-1", `{"idSite":5,"name":"S"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5,"sourceIdSites":[987]}`},
		// shape
		{"matomo.roll-ups.add", "ru-admin", `{"sourceIdSites":[1],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1],"currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1],"timezone":"UTC"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1,1],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":1,"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"","sourceIdSites":[1],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"` + strings.Repeat("n", 256) + `","sourceIdSites":[1],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"a\nb","sourceIdSites":[1],"timezone":"UTC","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1],"timezone":"a b","currency":"EUR"}`},
		{"matomo.roll-ups.add", "ru-admin", `{"name":"S","sourceIdSites":[1],"timezone":"UTC","currency":"eur"}`},
		{"matomo.roll-ups.add", "ru-admin", `{` + rollUpDefinition + `,"idSite":5}`},
		{"matomo.roll-ups.add", "ru-admin", `{` + rollUpDefinition + `,"method":"SitesManager.deleteSite"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"name":"S"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":0,"name":"S"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5,"name":""}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5,"sourceIdSites":[]}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5,"currency":"EURO"}`},
		{"matomo.roll-ups.update", "ru-admin", `{"idSite":5,"name":"S","callback":"x"}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "987") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s names a target or a foreign value: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestRollUpUpdateRefusesAnUnknownRollUpAfterOneReadWithoutMutation(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	// Website 2 is inside the targets but no roll-up.
	_, err := env.change("matomo.roll-ups.update", "ru-admin", `{"idSite":2,"name":"S"}`)
	if err == nil || classOf(err) != provider.ClassNotFound || countMethod(calls, "RollUpReporting.updateRollUp") != 0 ||
		countMethod(calls, "RollUpReporting.getRollUps") != 1 {
		t.Errorf("requests = %s, err = %v", methods(calls), err)
	}
	calls = nil
	env = newRollUpEnvironment(t, &calls, rollUpAnswers(answers{"RollUpReporting.getRollUps": fixed(`"nope"`)}).handler(t))
	if _, err := env.change("matomo.roll-ups.update", "ru-admin", `{"idSite":5,"name":"S"}`); err == nil ||
		countMethod(calls, "RollUpReporting.updateRollUp") != 0 {
		t.Errorf("invalid list: requests = %s, err = %v", methods(calls), err)
	}
}

func TestRollUpRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.roll-ups.list", "ru-admin", `{}`); err != nil {
		t.Fatal(err)
	}
	for _, c := range rollUpChanges {
		if _, err := env.change(c.tool, "ru-admin", c.arguments); err != nil {
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

func TestRollUpToolsMapMatomoErrorsAndChangesNeverRepeat(t *testing.T) {
	all := append([]struct{ tool, arguments, method, reads string }{
		{"matomo.roll-ups.list", `{}`, "RollUpReporting.getRollUps", ""}}, rollUpChanges...)
	for _, c := range all {
		changes := c.tool != "matomo.roll-ups.list"
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
			"not found": {func(call) (*http.Response, error) {
				return ok2(failure("The requested website does not exist." + bodyCanary))
			}, provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'RollUpReporting'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin RollUpReporting is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, changes},
			"server":     {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, changes},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, changes},
		} {
			var calls []call
			env := newRollUpEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return rollUpAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			var err error
			if changes {
				_, err = env.change(c.tool, "ru-admin", c.arguments)
			} else {
				_, err = env.invoke(c.tool, "ru-admin", c.arguments)
			}
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestRollUpAddAcceptsABareIDAndRefusesNone(t *testing.T) {
	for answer, want := range map[string]string{`21`: `{"created":true,"idSite":21}`, `{"value":0}`: "", `true`: ""} {
		var calls []call
		env := newRollUpEnvironment(t, &calls, rollUpAnswers(answers{"RollUpReporting.addRollUp": fixed(answer)}).handler(t))
		result, err := env.change("matomo.roll-ups.add", "ru-admin", rollUpChanges[0].arguments)
		if want != "" && (err != nil || !sameJSON(result, want)) || want == "" && (err == nil || !strings.Contains(err.Error(), "never repeated")) ||
			countMethod(calls, "RollUpReporting.addRollUp") != 1 {
			t.Errorf("answer %s: result = %s, err = %v", answer, result, err)
		}
	}
	var calls []call
	env := newRollUpEnvironment(t, &calls, rollUpAnswers(answers{"RollUpReporting.updateRollUp": fixed(`{"value":"none"}`)}).handler(t))
	if _, err := env.change("matomo.roll-ups.update", "ru-admin", rollUpChanges[1].arguments); err == nil ||
		!strings.Contains(err.Error(), "never repeated") || countMethod(calls, "RollUpReporting.updateRollUp") != 1 {
		t.Errorf("update without confirmation: err = %v", err)
	}
}

func TestRollUpToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range append(rollUpChanges, struct{ tool, arguments, method, reads string }{"matomo.roll-ups.list", `{}`, "", ""}) {
		var calls []call
		env := newRollUpEnvironment(t, &calls, with(rollUpAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		var err error
		if c.tool == "matomo.roll-ups.list" {
			_, err = env.invoke(c.tool, "ru-admin", c.arguments)
		} else {
			_, err = env.change(c.tool, "ru-admin", c.arguments)
		}
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
