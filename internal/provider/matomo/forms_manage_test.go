package matomo

import (
	"context"
	"encoding/json"
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

// Assumptions, as the FormAnalytics reference shows no example for the write methods: addForm answers the ID of
// the new form (bare or as {"value":N}), and updateForm, updateFormFieldDisplayName, archiveForm, and deleteForm
// answer the generic success form {"result":"success","message":"ok"} of the core convention.
const formConnections = `
  form-writer: {service: matomo, credential: token, effects: [read, create, update]}
  form-writer-two: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  form-writer-both: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2]}
  form-reader: {service: matomo, credential: token, effects: [read]}
  form-deleter: {service: matomo, credential: token, tools: [matomo.forms.delete]}
  form-deleter-two: {service: matomo, credential: token, tools: [matomo.forms.delete], targets: [site/2]}
  form-effects-delete: {service: matomo, credential: token, effects: [read, create, update, delete]}
`

func newFormChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(formConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func formChangeAnswers(extra answers) answers {
	success := fixed(`{"result":"success","message":"ok"}`)
	return formAnswers(with(answers{
		"FormAnalytics.addForm":                    fixed(`{"value":15}`),
		"FormAnalytics.updateForm":                 success,
		"FormAnalytics.updateFormFieldDisplayName": success,
		"FormAnalytics.archiveForm":                success,
		"FormAnalytics.deleteForm":                 success,
	}, extra))
}

const signupRule = `[{"attribute":"form_name","pattern":"equals","value":"signup"}]`

var formChanges = []struct{ tool, connection, arguments, method, reads string }{
	{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"Sign-up","matchFormRules":` + signupRule + `}`,
		"FormAnalytics.addForm", ""},
	{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"name":"Sign-up","matchFormRules":` + signupRule + `}`,
		"FormAnalytics.updateForm", "FormAnalytics.getForm"},
	{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"fields":[{"name":"comment","displayName":"Text"}]}`,
		"FormAnalytics.updateFormFieldDisplayName", "FormAnalytics.getForm"},
	{"matomo.forms.archive", "form-writer", `{"idSite":1,"idForm":2}`, "FormAnalytics.archiveForm", "FormAnalytics.getForm"},
	{"matomo.forms.delete", "form-deleter", `{"idSite":1,"idForm":2}`, "FormAnalytics.deleteForm", "FormAnalytics.getForm"},
}

func TestFormChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		listed      bool
	}{
		"matomo.forms.add":     {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"matomo.forms.update":  {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.forms.archive": {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.forms.delete":  {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != formsSensitive || d.RequiresToolAllowList != tc.listed {
			t.Errorf("%s: risk = %+v, allow list = %t", id, d.Risk, d.RequiresToolAllowList)
		}
		if !strings.Contains(d.Description, "never repeated") || !strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s: description %q, schema %s", id, d.Description, d.InputSchema)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", id, banned)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if _, change := want[tool]; change {
				t.Errorf("profile %s selects %s", profile.ID, tool)
			}
		}
	}
}

func TestFormAddSendsOneRequestWithTheGivenSettingsOnly(t *testing.T) {
	var calls []call
	env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
	c := formChanges[0]
	if _, err := env.invoke(c.tool, c.connection, c.arguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change(c.tool, "form-reader", c.arguments); err == nil || len(calls) != 0 {
		t.Fatalf("a read connection offered the tool: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change(c.tool, c.connection, c.arguments)
	var out FormCreated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDForm != 15 || !out.Created {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated FormAnalytics.addForm" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, c.method).form
	for key, want := range map[string]string{"idSite": "1", "name": "Sign-up", "matchFormRules[0][attribute]": "form_name",
		"matchFormRules[0][pattern]": "equals", "matchFormRules[0][value]": "signup"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	for _, absent := range []string{"description", "matchPageRules[0][value]", "conversionRuleOption", "conversionRules[0][value]", "idGoal"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}

	calls = nil
	full := `{"idSite":1,"name":"Pricing","description":"Test","matchFormRules":` + signupRule + `,` +
		`"matchPageRules":[{"attribute":"path","pattern":"equals","value":"/sign-up"},{"attribute":"path","pattern":"contains","value":"plans"}],` +
		`"conversionRuleOption":"page_visit","conversionRules":[{"attribute":"path","pattern":"equals","value":"/done"}],"idGoal":3}`
	if _, err := env.change(c.tool, c.connection, full); err != nil {
		t.Fatal(err)
	}
	form = lastCall(calls, c.method).form
	for key, want := range map[string]string{"description": "Test", "matchPageRules[1][pattern]": "contains",
		"matchPageRules[1][value]": "plans", "conversionRuleOption": "page_visit", "conversionRules[0][value]": "/done",
		"idGoal": "3"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}

	// A bare ID is accepted as well.
	calls = nil
	env = newFormChangeEnvironment(t, &calls, formChangeAnswers(answers{"FormAnalytics.addForm": fixed(`21`)}).handler(t))
	result, err = env.change(c.tool, c.connection, c.arguments)
	out = FormCreated{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDForm != 21 {
		t.Errorf("bare ID: result = %s, err = %v", result, err)
	}
}

func TestFormUpdateReadsTheFormBeforeTheOneRequest(t *testing.T) {
	for _, c := range formChanges[1:3] {
		var calls []call
		env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
		result, err := env.change(c.tool, c.connection, c.arguments)
		var out FormSaved
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDForm != 2 || !out.Saved {
			t.Fatalf("%s: result = %s, err = %v", c.method, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getForm "+c.method {
			t.Errorf("requests = %s", got)
		}
		form := lastCall(calls, c.method).form
		if form.Get("idSite") != "1" || form.Get("idForm") != "2" {
			t.Errorf("%s: form = %v", c.method, form)
		}
		if c.method == "FormAnalytics.updateForm" {
			// Nothing is filled in from the form that was read.
			if form.Get("name") != "Sign-up" || form.Has("description") || form.Has("fields[0][name]") {
				t.Errorf("form = %v", form)
			}
		} else if form.Get("fields[0][name]") != "comment" || form.Get("fields[0][displayName]") != "Text" || form.Has("name") {
			t.Errorf("form = %v", form)
		}
	}
}

func TestFormArchiveAndDeleteReadTheFormThenSendOneRequest(t *testing.T) {
	for _, c := range formChanges[3:] {
		var calls []call
		env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
		if _, err := env.invoke(c.tool, c.connection, c.arguments); err == nil ||
			!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", c.tool, err, len(calls))
		}
		result, err := env.change(c.tool, c.connection, c.arguments)
		var out map[string]any
		flag := strings.TrimPrefix(c.tool, "matomo.forms.") + "d"
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || out["idSite"] != float64(1) ||
			out["idForm"] != float64(2) || out[flag] != true || len(out) != 3 {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getForm "+c.method {
			t.Errorf("%s: requests = %s", c.tool, got)
		}
		if form := lastCall(calls, c.method).form; len(form) != 5 || form.Get("idForm") != "2" || form.Get("idSite") != "1" {
			t.Errorf("%s: form = %v", c.tool, form)
		}
	}
}

func TestFormChangeRefusesAFormOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, c := range formChanges[1:] {
		for _, id := range []string{"55", "6", "5", "99"} {
			var calls []call
			env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
			arguments := strings.Replace(c.arguments, `"idForm":2`, `"idForm":`+id, 1)
			_, err := env.change(c.tool, c.connection, arguments)
			if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("%s %s: err = %v", c.method, id, err)
				continue
			}
			if countMethod(calls, c.method) != 0 || countMethod(calls, "FormAnalytics.getForm") != 1 ||
				strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), "site/3") {
				t.Errorf("%s %s: requests = %s, err = %v", c.method, id, methods(calls), err)
			}
		}
	}
}

func TestFormChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
	manyRules := strings.TrimSuffix(strings.Repeat(`{"attribute":"path","pattern":"equals","value":"a"},`, 101), ",")
	rules := `"matchFormRules":` + signupRule
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.forms.add", "form-writer-two", `{"idSite":1,"name":"a"}`},
		{"matomo.forms.add", "form-writer-both", `{"name":"a"}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":""}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"` + strings.Repeat("n", 256) + `"}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","description":"` + strings.Repeat("d", 2049) + `"}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","matchFormRules":[]}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","matchPageRules":[` + manyRules + `]}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","matchFormRules":[{"attribute":"Form","pattern":"equals","value":"a"}]}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","matchFormRules":[{"attribute":"form_name","pattern":"equals"}]}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","conversionRules":[{"attribute":"path","pattern":"equals","value":"` + strings.Repeat("v", 2049) + `"}]}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","conversionRuleOption":"Page Visit"}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","idGoal":0}`},
		{"matomo.forms.add", "form-writer", `{"idSite":1,"name":"a","callback":"x"}`},
		{"matomo.forms.update", "form-writer-two", `{"idSite":1,"idForm":2,"name":"a"}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":0,"name":"a"}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"name":"a"}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,` + rules + `}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"fields":[]}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"name":"a","fields":[{"name":"a","displayName":"b"}]}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"description":"d","fields":[{"name":"a","displayName":"b"}]}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"fields":[{"name":"a","displayName":""}]}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"fields":[{"name":"a","displayName":"b"},{"name":"a","displayName":"c"}]}`},
		{"matomo.forms.update", "form-writer", `{"idSite":1,"idForm":2,"fields":[{"name":"` + strings.Repeat("n", 256) + `","displayName":"b"}]}`},
		{"matomo.forms.archive", "form-writer-two", `{"idSite":1,"idForm":2}`},
		{"matomo.forms.archive", "form-writer", `{"idSite":1}`},
		{"matomo.forms.archive", "form-writer", `{"idSite":1,"idForm":0}`},
		{"matomo.forms.archive", "form-writer", `{"idSite":1,"idForm":2,"module":"API"}`},
		{"matomo.forms.delete", "form-deleter-two", `{"idSite":1,"idForm":2}`},
		{"matomo.forms.delete", "form-deleter", `{"idSite":1}`},
		{"matomo.forms.delete", "form-deleter", `{"idSite":1,"idForm":0}`},
		{"matomo.forms.delete", "form-effects-delete", `{"idSite":1,"idForm":2}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite=1") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestFormDeleteIsReachableOnlyThroughTheToolsList(t *testing.T) {
	var calls []call
	env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
	if _, err := env.change("matomo.forms.delete", "form-effects-delete", `{"idSite":1,"idForm":2}`); err == nil ||
		len(calls) != 0 || *env.reads != 0 {
		t.Errorf("through effects only: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change("matomo.forms.delete", "form-deleter", `{"idSite":1,"idForm":2}`); err != nil {
		t.Errorf("through the tools list: %v", err)
	}
}

func TestFormChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newFormChangeEnvironment(t, &calls, formChangeAnswers(nil).handler(t))
	for _, c := range formChanges {
		connection := c.connection
		if connection == "form-writer" {
			connection = "form-writer-both"
		}
		if _, err := env.change(c.tool, connection, c.arguments); err != nil {
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

func TestFormChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range formChanges {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure("An unexpected website was found in the request: website id was set to '1' ." + bodyCanary))
			}, provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'FormAnalytics'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin FormAnalytics is not activated." + bodyCanary))
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
			env := newFormChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return formChangeAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, c.connection, c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestFormChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range formChanges {
		var calls []call
		env := newFormChangeEnvironment(t, &calls, with(formChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
