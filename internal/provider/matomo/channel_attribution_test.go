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

// Fixtures: the answers of getSiteAttributionGoals and getAvailableCampaignDimensionCombinations follow the read
// of Matomo's public demo (mixed number and text for idgoal and idsite, the pseudo entries -2 and 0, lower case
// member names; combinations as key/value rows). Assumed, as the reference gives no example: getGoalAttribution
// answers an object or list that is passed on bounded; setGoalAttribution answers the generic success form
// {"result":"success","message":"ok"} and takes isEnabled as 1 or 0, the core convention for flags.
const attributionConnections = `
  ca-admin: {service: matomo, credential: token, effects: [read, update], targets: [site/1, site/2]}
  ca-read: {service: matomo, credential: token, effects: [read], targets: [site/1]}
  ca-instance-only: {service: matomo, credential: token, effects: [read, update], targets: [instance]}
`

const attributionGoalRows = `[{"idgoal":"-2","name":"All Goals","idsite":"1"},` +
	`{"idsite":1,"idgoal":8,"name":"Agoda click","description":"","match_attribute":"external_website","pattern":"agoda",` +
	`"pattern_type":"contains","case_sensitive":0,"allow_multiple":0,"revenue":0,"deleted":0,"event_value_as_revenue":0},` +
	`{"name":"Ecommerce order","idsite":"1","idgoal":0},` +
	`{"idsite":1,"idgoal":"3","name":"Newsletter","match_attribute":"manually","allow_multiple":1,"revenue":"5.5"},` +
	`{"idsite":2,"idgoal":4,"name":"Foreign goal"},{"idsite":1,"name":"No ID"}]`

const attributionCombinations = `[{"key":0,"value":"Campaign Name (7 days)"},{"key":"1","value":"Campaign Name (30 days)"},` +
	`{"key":2,"value":"Campaign Name (90 days)"}]`

func newAttributionEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(attributionConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func attributionAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"Goals.getGoal": func(c call) *http.Response {
			switch c.form.Get("idGoal") {
			case "3":
				return ok(goalOne)
			case "9":
				return ok(strings.Replace(goalOne, `"idsite":"1"`, `"idsite":"3"`, 1))
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
		"MultiChannelConversionAttribution.getSiteAttributionGoals":                   fixed(attributionGoalRows),
		"MultiChannelConversionAttribution.getAvailableCampaignDimensionCombinations": fixed(attributionCombinations),
		"MultiChannelConversionAttribution.getGoalAttribution":                        fixed(`{"channels":[{"label":"Direct","conversions":4}],"note":"x"}`),
		"MultiChannelConversionAttribution.setGoalAttribution":                        fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

var attributionCases = []struct{ tool, arguments, method string }{
	{"matomo.channel-attribution.get", `{"idSite":1,"kind":"goals"}`, "MultiChannelConversionAttribution.getSiteAttributionGoals"},
	{"matomo.channel-attribution.get", `{"idSite":1,"kind":"dimensions"}`, "MultiChannelConversionAttribution.getAvailableCampaignDimensionCombinations"},
	{"matomo.channel-attribution.get", `{"idSite":1,"kind":"attribution","idGoal":3}`, "MultiChannelConversionAttribution.getGoalAttribution"},
	{"matomo.channel-attribution.set", `{"idSite":1,"idGoal":3,"isEnabled":true}`, "MultiChannelConversionAttribution.setGoalAttribution"},
}

func (e *environment) attribution(tool, connection, arguments string) (string, error) {
	if tool == "matomo.channel-attribution.set" {
		return e.change(tool, connection, arguments)
	}
	return e.invoke(tool, connection, arguments)
}

func TestChannelAttributionToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for tool, effect := range map[string]capability.Effect{
		"matomo.channel-attribution.get": capability.EffectRead, "matomo.channel-attribution.set": capability.EffectUpdate} {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		sets := effect == capability.EffectUpdate
		confirmation := capability.ConfirmationNone
		idempotency := capability.IdempotencySafe
		if sets {
			confirmation, idempotency = capability.ConfirmationRequired, capability.IdempotencyIdempotent
		}
		if d.Risk.Effect != effect || d.Risk.Idempotency != idempotency || d.Risk.Confirmation != confirmation ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList || instanceTools[tool] ||
			strings.Contains(d.Description, "never repeated") != sets || !strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s: risk = %+v, description %q", tool, d.Risk, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "token_auth"} {
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

func TestChannelAttributionGoalsKeepPseudoEntriesAndMixedTypes(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	result, err := env.invoke("matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"goals"}`)
	want := `{"idSite":1,"kind":"goals","has_more":false,"goals":[` +
		`{"idGoal":-2,"type":"all-goals","name":"All Goals"},` +
		`{"idGoal":8,"type":"goal","name":"Agoda click","match_attribute":"external_website","pattern":"agoda",` +
		`"pattern_type":"contains","case_sensitive":false,"allow_multiple":false,"revenue":0,"deleted":false,` +
		`"event_value_as_revenue":false},` +
		`{"idGoal":0,"type":"ecommerce","name":"Ecommerce order"},` +
		`{"idGoal":3,"type":"goal","name":"Newsletter","match_attribute":"manually","allow_multiple":true,"revenue":5.5}]}`
	if err != nil || !sameJSON(result, want) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := countMethod(calls, "MultiChannelConversionAttribution.getSiteAttributionGoals"); got != 1 ||
		lastCall(calls, "MultiChannelConversionAttribution.getSiteAttributionGoals").form.Get("idSite") != "1" {
		t.Errorf("requests = %s", methods(calls))
	}
	// The listing pages.
	result, err = env.invoke("matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"goals","limit":2}`)
	if err != nil || !strings.Contains(result, `"has_more":true`) || !strings.Contains(result, "next_cursor") {
		t.Errorf("paged result = %s, err = %v", result, err)
	}
}

func TestChannelAttributionDimensionsNeedAWebsiteOfTheConnection(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	result, err := env.invoke("matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"dimensions"}`)
	if err != nil || !sameJSON(result, `{"idSite":1,"kind":"dimensions","has_more":false,"combinations":[`+
		`{"key":0,"value":"Campaign Name (7 days)"},{"key":1,"value":"Campaign Name (30 days)"},`+
		`{"key":2,"value":"Campaign Name (90 days)"}]}`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if form := lastCall(calls, "MultiChannelConversionAttribution.getAvailableCampaignDimensionCombinations").form; form.Has("idSite") {
		t.Errorf("the catalog request carries a website: %v", form)
	}
	calls, *env.reads = nil, 0
	for _, c := range []struct{ connection, arguments string }{
		{"ca-admin", `{"idSite":987,"kind":"dimensions"}`},
		{"ca-admin", `{"kind":"dimensions"}`},
		{"ca-instance-only", `{"kind":"dimensions"}`},
	} {
		_, err := env.invoke("matomo.channel-attribution.get", c.connection, c.arguments)
		if err == nil || strings.Contains(err.Error(), "987") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s %s: err = %v", c.connection, c.arguments, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestChannelAttributionReadProvesTheGoalFirst(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	result, err := env.invoke("matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution","idGoal":3}`)
	if err != nil || !sameJSON(result, `{"idSite":1,"kind":"attribution","idGoal":3,"has_more":false,`+
		`"attribution":{"channels":[{"label":"Direct","conversions":4}],"note":"x"}}`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated ")
	if got != "Goals.getGoal MultiChannelConversionAttribution.getGoalAttribution" {
		t.Errorf("requests = %s", methods(calls))
	}
	form := lastCall(calls, "MultiChannelConversionAttribution.getGoalAttribution").form
	if form.Get("idSite") != "1" || form.Get("idGoal") != "3" {
		t.Errorf("form = %v", form)
	}
	// A goal the website does not have, or one of another website, ends after the one proof read.
	for goal, class := range map[string]provider.Class{"4": provider.ClassNotFound, "9": provider.ClassInvalidResponse} {
		calls = nil
		_, err := env.invoke("matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution","idGoal":`+goal+`}`)
		if err == nil || classOf(err) != class || countMethod(calls, "Goals.getGoal") != 1 ||
			countMethod(calls, "MultiChannelConversionAttribution.getGoalAttribution") != 0 {
			t.Errorf("goal %s: requests = %s, err = %v", goal, methods(calls), err)
		}
	}
}

func TestChannelAttributionRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":987,"kind":"goals"}`},
		{"matomo.channel-attribution.get", "ca-read", `{"idSite":2,"kind":"goals"}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":987,"kind":"attribution","idGoal":3}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"kind":"goals"}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"report"}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution"}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution","idGoal":0}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution","idGoal":-2}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"attribution","idGoal":3,"limit":5}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"goals","idGoal":3}`},
		{"matomo.channel-attribution.get", "ca-admin", `{"idSite":1,"kind":"goals","method":"API.getBulkRequests"}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":987,"idGoal":3,"isEnabled":true}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"isEnabled":true}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":3}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":3,"isEnabled":"yes"}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":0,"isEnabled":true}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":-2,"isEnabled":true}`},
		{"matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":3,"isEnabled":true,"callback":"x"}`},
		{"matomo.channel-attribution.set", "ca-read", `{"idSite":1,"idGoal":3,"isEnabled":true}`},
		{"matomo.channel-attribution.set", "ca-instance-only", `{"idGoal":3,"isEnabled":true}`},
	} {
		_, err := env.attribution(c.tool, c.connection, c.arguments)
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

func TestChannelAttributionSetSendsOneRequestAfterOneProofRead(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":3,"isEnabled":true}`); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	for enabled, flag := range map[string]string{"true": "1", "false": "0"} {
		calls = nil
		result, err := env.change("matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":3,"isEnabled":`+enabled+`}`)
		if err != nil || !sameJSON(result, `{"idSite":1,"idGoal":3,"isEnabled":`+enabled+`,"updated":true}`) {
			t.Fatalf("isEnabled %s: result = %s, err = %v", enabled, result, err)
		}
		got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated ")
		if got != "Goals.getGoal MultiChannelConversionAttribution.setGoalAttribution" {
			t.Errorf("requests = %s", methods(calls))
		}
		form := lastCall(calls, "MultiChannelConversionAttribution.setGoalAttribution").form
		if form.Get("idSite") != "1" || form.Get("idGoal") != "3" || form.Get("isEnabled") != flag || len(form) != 6 {
			t.Errorf("isEnabled %s: form = %v", enabled, form)
		}
	}
	// A goal the website does not have, or one of another website, is refused after the proof read, without a mutation.
	for goal := range map[string]bool{"4": true, "9": true} {
		calls = nil
		_, err := env.change("matomo.channel-attribution.set", "ca-admin", `{"idSite":1,"idGoal":`+goal+`,"isEnabled":true}`)
		if err == nil || countMethod(calls, "Goals.getGoal") != 1 ||
			countMethod(calls, "MultiChannelConversionAttribution.setGoalAttribution") != 0 {
			t.Errorf("goal %s: requests = %s, err = %v", goal, methods(calls), err)
		}
	}
	// A bare true counts as confirmation; any other answer is unclear and says so.
	for answer, clear := range map[string]bool{`true`: true, `{"value":true}`: true, `{"value":false}`: false, `"no"`: false} {
		var calls []call
		env := newAttributionEnvironment(t, &calls, attributionAnswers(answers{
			"MultiChannelConversionAttribution.setGoalAttribution": fixed(answer)}).handler(t))
		_, err := env.change("matomo.channel-attribution.set", "ca-admin", attributionCases[3].arguments)
		if clear != (err == nil) || err != nil && !strings.Contains(err.Error(), "never repeated") ||
			countMethod(calls, "MultiChannelConversionAttribution.setGoalAttribution") != 1 {
			t.Errorf("answer %s: err = %v", answer, err)
		}
	}
}

func TestChannelAttributionRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newAttributionEnvironment(t, &calls, attributionAnswers(nil).handler(t))
	for _, c := range attributionCases {
		if _, err := env.attribution(c.tool, "ca-admin", c.arguments); err != nil {
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

func TestChannelAttributionToolsMapMatomoErrorsAndChangesNeverRepeat(t *testing.T) {
	for _, c := range attributionCases {
		sets := c.tool == "matomo.channel-attribution.set"
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
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'MultiChannelConversionAttribution'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin MultiChannelConversionAttribution is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, sets},
			"server":     {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, sets},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, sets},
		} {
			var calls []call
			env := newAttributionEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return attributionAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.attribution(c.tool, "ca-admin", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.arguments, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestChannelAttributionToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range attributionCases {
		var calls []call
		env := newAttributionEnvironment(t, &calls, with(attributionAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.attribution(c.tool, "ca-admin", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.arguments, err, methods(calls))
		}
	}
}

func TestChannelAttributionRejectsUnreadableListsAndAttributions(t *testing.T) {
	for method, c := range map[string]struct{ arguments, answer string }{
		"MultiChannelConversionAttribution.getSiteAttributionGoals":                   {attributionCases[0].arguments, `"nope"`},
		"MultiChannelConversionAttribution.getAvailableCampaignDimensionCombinations": {attributionCases[1].arguments, `"nope"`},
		"MultiChannelConversionAttribution.getGoalAttribution":                        {attributionCases[2].arguments, `{`},
	} {
		var calls []call
		env := newAttributionEnvironment(t, &calls, attributionAnswers(answers{method: fixed(c.answer)}).handler(t))
		if _, err := env.invoke("matomo.channel-attribution.get", "ca-admin", c.arguments); err == nil || classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", method, err)
		}
	}
}
