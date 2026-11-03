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

// Assumptions, as the AbTesting reference gives no example for the write methods: addExperiment answers the ID of
// the new experiment (bare or as {"value":N}), updateExperiment and deleteExperiment answer the generic success
// form {"result":"success","message":"ok"} of the core convention. The member names of variations and targets in a
// request follow the members the read methods report.
const experimentConnections = `
  exp-writer: {service: matomo, credential: token, effects: [read, create, update]}
  exp-writer-one: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1]}
  exp-writer-two: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  exp-starter: {service: matomo, credential: token, tools: [matomo.experiments.start]}
  exp-starter-two: {service: matomo, credential: token, tools: [matomo.experiments.start], targets: [site/2]}
  exp-reader: {service: matomo, credential: token, effects: [read]}
  exp-deleter: {service: matomo, credential: token, tools: [matomo.experiments.delete]}
  exp-deleter-two: {service: matomo, credential: token, tools: [matomo.experiments.delete], targets: [site/2]}
  exp-effects-delete: {service: matomo, credential: token, effects: [read, create, update, delete]}
`

func newExperimentChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(experimentConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func experimentChangeAnswers(extra answers) answers {
	return experimentAnswers(with(answers{
		"AbTesting.addExperiment":    fixed(`{"value":12}`),
		"AbTesting.updateExperiment": fixed(`{"result":"success","message":"ok"}`),
		"AbTesting.deleteExperiment": fixed(`{"result":"success","message":"ok"}`),
		// Assumption: the reference documents the lifecycle methods only as GenericSuccess; the form follows the core convention.
		"AbTesting.startExperiment":   fixed(`{"result":"success","message":"ok"}`),
		"AbTesting.finishExperiment":  fixed(`{"result":"success","message":"ok"}`),
		"AbTesting.archiveExperiment": fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

const (
	experimentDefinition = `"name":"Banner","description":"Banner test","hypothesis":"A larger banner converts better",` +
		`"variations":[{"name":"Large","percentage":50,"redirect_url":"https://example.org/large"},{"name":"Small"}],` +
		`"includedTargets":[{"attribute":"url","type":"contains","value":"pricing","inverted":true},` +
		`{"attribute":"url","type":"any","value":""}],"successMetrics":["nb_conversions","nb_pageviews"]`
	experimentAddArguments    = `{"idSite":1,` + experimentDefinition + `}`
	experimentUpdateRequired  = `{"idSite":1,"idExperiment":4,` + experimentDefinition + `,"confidenceThreshold":99.5,"mdeRelative":10,"percentageParticipants":80`
	experimentUpdateArguments = experimentUpdateRequired + `}`
	experimentDeleteArguments = `{"idSite":1,"idExperiment":4}`
)

var experimentChanges = []struct{ tool, connection, arguments, method, reads string }{
	{"matomo.experiments.add", "exp-writer", experimentAddArguments, "AbTesting.addExperiment", ""},
	{"matomo.experiments.update", "exp-writer", experimentUpdateArguments, "AbTesting.updateExperiment", "AbTesting.getExperiment"},
	{"matomo.experiments.delete", "exp-deleter", experimentDeleteArguments, "AbTesting.deleteExperiment", "AbTesting.getExperiment"},
	{"matomo.experiments.start", "exp-starter", experimentDeleteArguments, "AbTesting.startExperiment", "AbTesting.getExperiment"},
	{"matomo.experiments.finish", "exp-writer", experimentDeleteArguments, "AbTesting.finishExperiment", "AbTesting.getExperiment"},
	{"matomo.experiments.archive", "exp-writer", experimentDeleteArguments, "AbTesting.archiveExperiment", "AbTesting.getExperiment"},
}

func TestExperimentChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		listedOnly  bool
	}{
		"matomo.experiments.add":     {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"matomo.experiments.update":  {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.experiments.delete":  {capability.EffectDelete, capability.IdempotencyIdempotent, true},
		"matomo.experiments.start":   {capability.EffectUpdate, capability.IdempotencyIdempotent, true},
		"matomo.experiments.finish":  {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.experiments.archive": {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != experimentsSensitive || d.RequiresToolAllowList != tc.listedOnly {
			t.Errorf("%s: risk = %+v, allow list = %t", id, d.Risk, d.RequiresToolAllowList)
		}
		if !strings.Contains(d.Description, "never repeated") {
			t.Errorf("%s: description %q", id, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", id, banned)
			}
		}
	}
	update, _, _ := reg.Lookup("matomo.experiments.update")
	for _, required := range []string{"idExperiment", "name", "description", "hypothesis", "variations", "includedTargets",
		"successMetrics", "confidenceThreshold", "mdeRelative", "percentageParticipants"} {
		if !strings.Contains(string(update.InputSchema), `"`+required+`"`) {
			t.Errorf("update lacks %s", required)
		}
	}
	if !strings.Contains(string(update.InputSchema), `"required":["idExperiment","name","description","hypothesis","variations","includedTargets","successMetrics","confidenceThreshold","mdeRelative","percentageParticipants"]`) {
		t.Errorf("update schema = %s", update.InputSchema)
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

func TestExperimentAddSendsOneRequestWithTheGivenDefinition(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.experiments.add", "exp-writer", experimentAddArguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change("matomo.experiments.add", "exp-reader", experimentAddArguments); err == nil || len(calls) != 0 {
		t.Fatalf("a read connection offered the tool: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.experiments.add", "exp-writer", experimentAddArguments)
	var out ExperimentCreated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDExperiment != 12 || !out.Created {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated AbTesting.addExperiment" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "AbTesting.addExperiment").form
	for key, want := range map[string]string{"idSite": "1", "name": "Banner", "description": "Banner test",
		"hypothesis": "A larger banner converts better", "variations[0][name]": "Large", "variations[0][percentage]": "50",
		"variations[0][redirect_url]": "https://example.org/large", "variations[1][name]": "Small",
		"includedTargets[0][attribute]": "url", "includedTargets[0][type]": "contains", "includedTargets[0][value]": "pricing",
		"includedTargets[0][inverted]": "1", "includedTargets[1][type]": "any", "includedTargets[1][value]": "",
		"successMetrics[0]": "nb_conversions", "successMetrics[1]": "nb_pageviews"} {
		if !form.Has(key) || form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	for _, absent := range []string{"variations[1][percentage]", "variations[1][redirect_url]", "includedTargets[1][inverted]",
		"excludedTargets[0][attribute]", "confidenceThreshold", "mdeRelative", "percentageParticipants", "startDate", "endDate"} {
		if form.Has(absent) {
			t.Errorf("%s was sent", absent)
		}
	}

	// A bare ID is accepted as well.
	calls = nil
	env = newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(answers{"AbTesting.addExperiment": fixed(`21`)}).handler(t))
	result, err = env.change("matomo.experiments.add", "exp-writer", experimentAddArguments)
	out = ExperimentCreated{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDExperiment != 21 {
		t.Errorf("bare ID: result = %s, err = %v", result, err)
	}
}

func TestExperimentUpdateReadsTheExperimentThenSendsTheWholeDefinition(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	result, err := env.change("matomo.experiments.update", "exp-writer", experimentUpdateArguments)
	var out ExperimentSaved
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDExperiment != 4 || !out.Saved {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated AbTesting.getExperiment AbTesting.updateExperiment" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "AbTesting.updateExperiment").form
	for key, want := range map[string]string{"idSite": "1", "idExperiment": "4", "name": "Banner",
		"confidenceThreshold": "99.5", "mdeRelative": "10", "percentageParticipants": "80", "variations[1][name]": "Small",
		"includedTargets[1][type]": "any", "successMetrics[1]": "nb_pageviews"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	// Nothing is filled in from the experiment that was read: what is not given is not sent.
	for _, absent := range []string{"excludedTargets[0][attribute]", "startDate", "endDate", "forwardUtmParams", "forwardAllQueryParams"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}

	calls = nil
	full := experimentUpdateRequired + `,"excludedTargets":[{"attribute":"url","type":"equals_exactly","value":"https://example.org/x"}],` +
		`"startDate":"2026-12-01 08:00:00","endDate":"2026-12-31 23:59:59","forwardUtmParams":true,"forwardAllQueryParams":false,"confidenceThreshold":95}`
	full = strings.Replace(full, `"confidenceThreshold":99.5,`, "", 1)
	if _, err := env.change("matomo.experiments.update", "exp-writer", full); err != nil {
		t.Fatal(err)
	}
	form = lastCall(calls, "AbTesting.updateExperiment").form
	for key, want := range map[string]string{"excludedTargets[0][value]": "https://example.org/x", "startDate": "2026-12-01 08:00:00",
		"endDate": "2026-12-31 23:59:59", "forwardUtmParams": "1", "forwardAllQueryParams": "0", "confidenceThreshold": "95"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
}

func TestExperimentDeleteReadsTheExperimentThenSendsOneRequest(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.experiments.delete", "exp-deleter", experimentDeleteArguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.experiments.delete", "exp-deleter", experimentDeleteArguments)
	var out map[string]any
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out["idSite"] != float64(1) ||
		out["idExperiment"] != float64(4) || out["deleted"] != true || len(out) != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated AbTesting.getExperiment AbTesting.deleteExperiment" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "AbTesting.deleteExperiment").form
	if form.Get("idSite") != "1" || form.Get("idExperiment") != "4" || form.Get("format") != "JSON" ||
		form.Get("module") != "API" || form.Get("method") != "AbTesting.deleteExperiment" || len(form) != 5 {
		t.Errorf("form = %v", form)
	}
}

func TestExperimentDeleteIsReachableOnlyThroughTheToolsList(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	for _, connection := range []string{"exp-effects-delete", "exp-writer"} {
		if _, err := env.change("matomo.experiments.delete", connection, experimentDeleteArguments); err == nil ||
			len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, requests = %d", connection, err, len(calls))
		}
	}
}

func TestExperimentChangeRefusesAnExperimentOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, c := range []struct{ tool, connection, mutation string }{
		{"matomo.experiments.update", "exp-writer", "AbTesting.updateExperiment"},
		{"matomo.experiments.delete", "exp-deleter", "AbTesting.deleteExperiment"},
		{"matomo.experiments.start", "exp-starter", "AbTesting.startExperiment"},
		{"matomo.experiments.finish", "exp-writer", "AbTesting.finishExperiment"},
		{"matomo.experiments.archive", "exp-writer", "AbTesting.archiveExperiment"},
	} {
		for _, id := range []string{"55", "6", "5", "99", "8"} {
			var calls []call
			env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
			arguments := `{"idSite":1,"idExperiment":` + id + `}`
			if c.tool == "matomo.experiments.update" {
				arguments = strings.Replace(experimentUpdateArguments, `"idExperiment":4`, `"idExperiment":`+id, 1)
			}
			_, err := env.change(c.tool, c.connection, arguments)
			if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse ||
				countMethod(calls, c.mutation) != 0 || countMethod(calls, "AbTesting.getExperiment") != 1 ||
				strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "55") {
				t.Errorf("%s experiment %s: requests = %s, err = %v", c.tool, id, methods(calls), err)
			}
		}
	}
}

func TestExperimentChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	without := func(arguments, member string) string {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(arguments), &object) != nil {
			t.Fatal("bad fixture")
		}
		delete(object, member)
		out, _ := json.Marshal(object)
		return string(out)
	}
	add := func(extra string) string { return strings.TrimSuffix(experimentAddArguments, "}") + extra + "}" }
	update := func(extra string) string { return experimentUpdateRequired + extra + "}" }
	many := strings.TrimSuffix(strings.Repeat(`{"name":"v"},`, 101), ",")
	manyRules := strings.TrimSuffix(strings.Repeat(`{"attribute":"url","type":"any","value":""},`, 101), ",")
	cases := []struct{ tool, connection, arguments string }{
		{"matomo.experiments.add", "exp-writer-two", experimentAddArguments},
		{"matomo.experiments.add", "exp-writer", `{"idSite":1}`},
		{"matomo.experiments.add", "exp-writer", add(`,"callback":"x"`)},
		{"matomo.experiments.add", "exp-writer", add(`,"confidenceThreshold":95`)},
		{"matomo.experiments.add", "exp-writer", add(`,"startDate":"2026-12-01 08:00:00"`)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"name":"Banner"`, `"name":""`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"name":"Banner"`, `"name":"`+strings.Repeat("n", 256)+`"`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"hypothesis":"A larger banner converts better"`, `"hypothesis":"`+strings.Repeat("h", 2049)+`"`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `[{"name":"Large"`, `[{"name":""`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"percentage":50`, `"percentage":101`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"variations":[{"name":"Large","percentage":50,"redirect_url":"https://example.org/large"},{"name":"Small"}]`, `"variations":[]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"variations":[{"name":"Large","percentage":50,"redirect_url":"https://example.org/large"},{"name":"Small"}]`, `"variations":[`+many+`]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"redirect_url":"https://example.org/large"`, `"redirect_url":"`+strings.Repeat("u", 2049)+`"`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"attribute":"url","type":"contains"`, `"attribute":"Url","type":"contains"`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"value":"pricing"`, `"value":"`+strings.Repeat("v", 2049)+`"`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"includedTargets":[{"attribute":"url","type":"contains","value":"pricing","inverted":true},{"attribute":"url","type":"any","value":""}]`, `"includedTargets":[]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `"includedTargets":[{"attribute":"url","type":"contains","value":"pricing","inverted":true},{"attribute":"url","type":"any","value":""}]`, `"includedTargets":[`+manyRules+`]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `["nb_conversions","nb_pageviews"]`, `[]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `["nb_conversions","nb_pageviews"]`, `["nb_conversions","nb_conversions"]`, 1)},
		{"matomo.experiments.add", "exp-writer", strings.Replace(experimentAddArguments, `["nb_conversions","nb_pageviews"]`, `["nb conversions"]`, 1)},
		{"matomo.experiments.add", "exp-writer", without(experimentAddArguments, "successMetrics")},
		{"matomo.experiments.add", "exp-writer", without(experimentAddArguments, "description")},
		{"matomo.experiments.update", "exp-writer-two", experimentUpdateArguments},
		{"matomo.experiments.update", "exp-writer", strings.Replace(experimentUpdateArguments, `"idExperiment":4`, `"idExperiment":0`, 1)},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "idExperiment")},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "confidenceThreshold")},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "mdeRelative")},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "percentageParticipants")},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "hypothesis")},
		{"matomo.experiments.update", "exp-writer", without(experimentUpdateArguments, "variations")},
		{"matomo.experiments.update", "exp-writer", strings.Replace(experimentUpdateArguments, `"confidenceThreshold":99.5`, `"confidenceThreshold":97`, 1)},
		{"matomo.experiments.update", "exp-writer", strings.Replace(experimentUpdateArguments, `"mdeRelative":10`, `"mdeRelative":0`, 1)},
		{"matomo.experiments.update", "exp-writer", strings.Replace(experimentUpdateArguments, `"mdeRelative":10`, `"mdeRelative":10.5`, 1)},
		{"matomo.experiments.update", "exp-writer", strings.Replace(experimentUpdateArguments, `"percentageParticipants":80`, `"percentageParticipants":101`, 1)},
		{"matomo.experiments.update", "exp-writer", update(`,"startDate":"2026-12-01"`)},
		{"matomo.experiments.update", "exp-writer", update(`,"endDate":"false"`)},
		{"matomo.experiments.update", "exp-writer", update(`,"excludedTargets":[{"attribute":"url","type":"any"}]`)},
		{"matomo.experiments.update", "exp-writer", update(`,"excludedTargets":[` + manyRules + `]`)},
		{"matomo.experiments.update", "exp-writer", update(`,"forwardUtmParams":"yes"`)},
		{"matomo.experiments.delete", "exp-deleter-two", experimentDeleteArguments},
		{"matomo.experiments.delete", "exp-deleter", `{"idSite":1}`},
		{"matomo.experiments.delete", "exp-deleter", `{"idSite":1,"idExperiment":0}`},
		{"matomo.experiments.delete", "exp-deleter", `{"idSite":1,"idExperiment":2147483648}`},
		{"matomo.experiments.delete", "exp-deleter", `{"idSite":1,"idExperiment":4,"module":"API"}`},
		{"matomo.experiments.start", "exp-starter-two", experimentDeleteArguments},
		{"matomo.experiments.start", "exp-starter", `{"idSite":1}`},
		{"matomo.experiments.start", "exp-starter", `{"idSite":1,"idExperiment":0}`},
		{"matomo.experiments.start", "exp-starter", `{"idSite":1,"idExperiment":4,"module":"API"}`},
		{"matomo.experiments.finish", "exp-writer-two", experimentDeleteArguments},
		{"matomo.experiments.finish", "exp-writer", `{"idSite":1,"idExperiment":4,"method":"x"}`},
		{"matomo.experiments.archive", "exp-writer-two", experimentDeleteArguments},
		{"matomo.experiments.archive", "exp-writer", `{"idSite":1}`},
	}
	for _, c := range cases {
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

func TestExperimentChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	for _, c := range experimentChanges {
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

func TestExperimentChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range experimentChanges {
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
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'AbTesting'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin AbTesting is not activated." + bodyCanary))
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
			env := newExperimentChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return experimentChangeAnswers(nil).handler(t)(in)
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

func TestExperimentChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range experimentChanges {
		var calls []call
		env := newExperimentChangeEnvironment(t, &calls, with(experimentChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestExperimentStateToolsSendOneRequestAfterOneRead(t *testing.T) {
	for _, c := range []struct{ tool, connection, method, flag string }{
		{"matomo.experiments.start", "exp-starter", "AbTesting.startExperiment", "started"},
		{"matomo.experiments.finish", "exp-writer", "AbTesting.finishExperiment", "finished"},
		{"matomo.experiments.archive", "exp-writer", "AbTesting.archiveExperiment", "archived"},
	} {
		var calls []call
		env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
		if _, err := env.invoke(c.tool, c.connection, experimentDeleteArguments); err == nil ||
			!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", c.tool, err, len(calls))
		}
		result, err := env.change(c.tool, c.connection, experimentDeleteArguments)
		var out map[string]any
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || out["idSite"] != float64(1) ||
			out["idExperiment"] != float64(4) || out[c.flag] != true || len(out) != 3 {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated AbTesting.getExperiment "+c.method {
			t.Errorf("%s: requests = %s", c.tool, got)
		}
		form := lastCall(calls, c.method).form
		if form.Get("idSite") != "1" || form.Get("idExperiment") != "4" || form.Get("format") != "JSON" ||
			form.Get("module") != "API" || form.Get("method") != c.method || len(form) != 5 {
			t.Errorf("%s: form = %v", c.tool, form)
		}
	}
}

func TestExperimentStartIsReachableOnlyThroughTheToolsList(t *testing.T) {
	var calls []call
	env := newExperimentChangeEnvironment(t, &calls, experimentChangeAnswers(nil).handler(t))
	for _, connection := range []string{"exp-effects-delete", "exp-writer"} {
		if _, err := env.change("matomo.experiments.start", connection, experimentDeleteArguments); err == nil ||
			len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, requests = %d", connection, err, len(calls))
		}
	}
}
