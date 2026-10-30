package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// The fixtures follow the JSON examples of the Funnels reference (getSalesFunnelForSite, getAllActivatedFunnelsForSite).
// Assumptions, as the reference gives none: the answer of testUrlMatchesSteps and of getAvailablePatternMatches
// (shape only described in words), hasAnyActivatedFunnelForSite as {"value":true} (documented), and an error for
// a missing plugin or licence in the common Matomo form.
const salesFunnel = `{"idfunnel":99,"idsite":1,"idgoal":0,"revision":0,"name":"Sales","description":null,` +
	`"created_date":"2018-05-09 00:33:10","activated":true,"steps":[` +
	`{"position":1,"name":"Homepage","pattern_type":"path_equals","pattern":"\\/","required":false,"patternComparison":"path"},` +
	`{"position":2,"name":"Item added to cart","pattern_type":"eventname_equals","pattern":"added - diving mask","required":true,"patternComparison":"eventname"}],` +
	`"isSalesFunnel":true,"final_step_position":3}`

const jobFunnel = `{"idfunnel":"7","idsite":"1","idgoal":"4","revision":"0","name":"New Job Application","description":"",` +
	`"created_date":"2016-11-18 00:40:28","activated":"1","steps":[` +
	`{"position":"1","name":"Job board","pattern_type":"path_equals","pattern":"\\/jobs","required":"1","patternComparison":"path"}],` +
	`"isSalesFunnel":"0","final_step_position":"2"}`

const foreignFunnel = `{"idfunnel":55,"idsite":3,"idgoal":9,"name":"Foreign","steps":[],"activated":true}`

func funnelAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"Funnels.hasAnyActivatedFunnelForSite": fixed(`{"value":true}`),
		"Funnels.getAllActivatedFunnelsForSite": func(call) *http.Response {
			return ok("[" + salesFunnel + "," + jobFunnel + "," + foreignFunnel + "]")
		},
		"Funnels.getFunnel": func(c call) *http.Response {
			switch c.form.Get("idFunnel") {
			case "99":
				return ok(salesFunnel)
			case "7":
				return ok(jobFunnel)
			case "55":
				return ok(foreignFunnel)
			case "8":
				return ok(jobFunnel)
			case "6":
				return ok(`{"idfunnel":6,"name":"No site"}`)
			}
			return ok(`null`)
		},
		"Funnels.getGoalFunnel": func(c call) *http.Response {
			if c.form.Get("idGoal") == "4" {
				return ok(jobFunnel)
			}
			return ok(`null`)
		},
		"Funnels.getSalesFunnelForSite": fixed(salesFunnel),
		"Funnels.getAvailablePatternMatches": fixed(`{"path":{"path_equals":"Path equals","path_startswith":"Path starts with"},` +
			`"eventname":{"eventname_equals":"Event name equals"}}`),
		"Funnels.testUrlMatchesSteps": fixed(`{"url":"https://example.org/pricing","steps":[{"pattern_type":"path_equals","matches":true}]}`),
	}, extra))
}

var funnelCalls = []struct{ tool, arguments string }{
	{"matomo.funnels.list", `{"idSite":1}`},
	{"matomo.funnels.get", `{"idSite":1,"idFunnel":7}`},
	{"matomo.funnels.test-steps", `{"url":"https://example.org/a","steps":[{"pattern_type":"path_equals","pattern":"/a"}]}`},
	{"matomo.funnel-options.list", `{}`},
}

func TestFunnelToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, id := range []string{"matomo.funnels.list", "matomo.funnels.get", "matomo.funnels.test-steps", "matomo.funnel-options.list"} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk != readRisk(funnelsSensitive) || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", id, banned)
			}
		}
		if strings.Contains(string(d.InputSchema), `"idSite"`) != (id == "matomo.funnels.list" || id == "matomo.funnels.get") {
			t.Errorf("%s idSite in schema = %s", id, d.InputSchema)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "funnel") {
			t.Errorf("profile %s selects a funnel tool", profile.ID)
		}
	}
	for _, id := range []string{"save", "set", "delete", "add"} {
		if _, _, found := reg.Lookup("matomo.funnels." + id); found {
			t.Errorf("matomo.funnels.%s exists", id)
		}
	}
}

func TestFunnelsListChecksThenReadsAndKeepsOnlyTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
	result, err := env.invoke("matomo.funnels.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated Funnels.hasAnyActivatedFunnelForSite Funnels.getAllActivatedFunnelsForSite" {
		t.Errorf("requests = %s", methods(calls))
	}
	var out FunnelsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || !out.HasActivatedFunnel || len(out.Funnels) != 2 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	sales, job := out.Funnels[0], out.Funnels[1]
	if sales.IDFunnel != 99 || sales.IDGoal == nil || *sales.IDGoal != 0 || sales.IsSalesFunnel == nil || !*sales.IsSalesFunnel ||
		sales.Description != "" || len(sales.Steps) != 2 || sales.Steps[0].PatternComparison != "path" ||
		sales.Steps[1].Required == nil || !*sales.Steps[1].Required || sales.FinalStepPosition == nil || *sales.FinalStepPosition != 3 {
		t.Errorf("sales = %+v", sales)
	}
	// Matomo versions that answer numbers as strings give the same values.
	if job.IDFunnel != 7 || job.IDGoal == nil || *job.IDGoal != 4 || job.Activated == nil || !*job.Activated ||
		job.IsSalesFunnel == nil || *job.IsSalesFunnel || job.Steps[0].Position == nil || *job.Steps[0].Position != 1 {
		t.Errorf("job = %+v", job)
	}
	for _, c := range calls {
		if c.form.Get("idSite") != "1" && strings.HasPrefix(c.api(), "Funnels.") {
			t.Errorf("%s idSite = %q", c.api(), c.form.Get("idSite"))
		}
	}

	page, err := env.invoke("matomo.funnels.list", "two", `{"idSite":1,"limit":1}`)
	var first FunnelsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.Funnels) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.funnels.list", "two", `{"idSite":1,"limit":1,"cursor":"`+first.NextCursor+`"}`)
	var second FunnelsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.Funnels) != 1 || second.HasMore ||
		second.Funnels[0].IDFunnel != 7 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	if _, err := env.invoke("matomo.funnels.list", "two", `{"idSite":2,"limit":1,"cursor":"`+first.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another website was accepted")
	}
}

func TestFunnelsListWithoutAnyFunnelAsksOnce(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(answers{"Funnels.hasAnyActivatedFunnelForSite": fixed(`{"value":false}`)}).handler(t))
	result, err := env.invoke("matomo.funnels.list", "one", `{}`)
	if err != nil || !strings.Contains(result, `"has_activated_funnel":false`) ||
		countMethod(calls, "Funnels.getAllActivatedFunnelsForSite") != 0 || lastCall(calls, "Funnels.hasAnyActivatedFunnelForSite").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
}

func TestFunnelsGetChoosesTheMethodAndProvesTheWebsite(t *testing.T) {
	for _, tc := range []struct {
		name, arguments, method string
		id                      int
	}{
		{"by funnel", `{"idSite":1,"idFunnel":7}`, "Funnels.getFunnel", 7},
		{"by goal", `{"idSite":1,"idGoal":4}`, "Funnels.getGoalFunnel", 7},
		{"sales funnel", `{"idSite":1,"idGoal":0}`, "Funnels.getSalesFunnelForSite", 99},
	} {
		var calls []call
		env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
		result, err := env.invoke("matomo.funnels.get", "two", tc.arguments)
		var funnel Funnel
		if err != nil || json.Unmarshal([]byte(result), &funnel) != nil || funnel.IDFunnel != tc.id || funnel.IDSite != 1 ||
			methods(calls) != "API.getMatomoVersion API.isPluginActivated "+tc.method {
			t.Errorf("%s: result = %s, err = %v, requests = %s", tc.name, result, err, methods(calls))
			continue
		}
		sent := lastCall(calls, tc.method).form
		if sent.Get("idSite") != "1" || tc.method == "Funnels.getSalesFunnelForSite" && sent.Has("idGoal") {
			t.Errorf("%s: form = %v", tc.name, sent)
		}
	}
}

func TestFunnelsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"funnel of another website": {`{"idSite":1,"idFunnel":55}`, provider.ClassNotFound},
		"no funnel":                 {`{"idSite":1,"idFunnel":3}`, provider.ClassNotFound},
		"no funnel for the goal":    {`{"idSite":1,"idGoal":5}`, provider.ClassNotFound},
		"answer without a website":  {`{"idSite":1,"idFunnel":6}`, provider.ClassInvalidResponse},
		"another funnel":            {`{"idSite":1,"idFunnel":8}`, provider.ClassInvalidResponse},
		"another goal":              {`{"idSite":1,"idGoal":4}`, ""},
	} {
		var calls []call
		extra := answers{}
		if name == "another goal" {
			extra["Funnels.getGoalFunnel"] = fixed(strings.Replace(jobFunnel, `"idgoal":"4"`, `"idgoal":"5"`, 1))
			tc.class = provider.ClassInvalidResponse
		}
		env := newEnvironment(t, &calls, funnelAnswers(extra).handler(t))
		_, err := env.invoke("matomo.funnels.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "55") {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "Funnels.getFunnel")+countMethod(calls, "Funnels.getGoalFunnel") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestFunnelToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.funnels.list", "two", `{"idSite":3}`},
		{"matomo.funnels.list", "two", `{}`},
		{"matomo.funnels.list", "instance", `{}`},
		{"matomo.funnels.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.funnels.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.funnels.get", "two", `{"idSite":3,"idFunnel":7}`},
		{"matomo.funnels.get", "two", `{"idFunnel":7}`},
		{"matomo.funnels.get", "one", `{"idSite":1,"idFunnel":7}`},
		{"matomo.funnels.get", "two", `{"idSite":1}`},
		{"matomo.funnels.get", "two", `{"idSite":1,"idFunnel":7,"idGoal":4}`},
		{"matomo.funnels.get", "two", `{"idSite":1,"idFunnel":0}`},
		{"matomo.funnels.get", "two", `{"idSite":1,"idGoal":-1}`},
		{"matomo.funnels.get", "two", `{"idSite":1,"idGoal":1000000000}`},
		{"matomo.funnels.get", "two", `{"idSite":1,"idFunnel":"7"}`},
		{"matomo.funnels.get", "instance", `{"idSite":1,"idFunnel":7}`},
		{"matomo.funnels.test-steps", "instance", funnelCalls[2].arguments},
		{"matomo.funnels.test-steps", "two", `{"url":"","steps":[{"pattern_type":"path_equals","pattern":"/a"}]}`},
		{"matomo.funnels.test-steps", "two", `{"url":"/a","steps":[]}`},
		{"matomo.funnels.test-steps", "two", `{"url":"/a","steps":[{"pattern_type":"Path-Equals","pattern":"/a"}]}`},
		{"matomo.funnels.test-steps", "two", `{"url":"/a","steps":[{"pattern_type":"path_equals","pattern":""}]}`},
		{"matomo.funnels.test-steps", "two", `{"url":"/a","steps":[{"pattern_type":"path_equals","pattern":"/a","idSite":1}]}`},
		{"matomo.funnels.test-steps", "two", `{"url":"/a","steps":[{"pattern_type":"path_equals","pattern":"/a"}],"idSite":1}`},
		{"matomo.funnels.test-steps", "two", `{"url":"` + strings.Repeat("a", 2049) + `","steps":[{"pattern_type":"path_equals","pattern":"/a"}]}`},
		{"matomo.funnel-options.list", "instance", `{}`},
		{"matomo.funnel-options.list", "two", `{"idSite":1}`},
	} {
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=3") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestFunnelsTestStepsSendsNestedStepsAndBoundsTheAnswer(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(answers{
		"Funnels.testUrlMatchesSteps": fixed(`{"url":"` + long + `","rows":[` + strings.Repeat("1,", 200) + `1]}`),
	}).handler(t))
	result, err := env.invoke("matomo.funnels.test-steps", "two", `{"url":"https://example.org/pricing","steps":[`+
		`{"pattern_type":"path_equals","pattern":"/pricing","name":"Pricing","required":true},`+
		`{"pattern_type":"url_contains","pattern":"shop"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	form := lastCall(calls, "Funnels.testUrlMatchesSteps").form
	for key, want := range map[string]string{"url": "https://example.org/pricing", "steps[0][pattern_type]": "path_equals",
		"steps[0][pattern]": "/pricing", "steps[0][name]": "Pricing", "steps[0][required]": "1",
		"steps[1][pattern_type]": "url_contains", "steps[1][pattern]": "shop"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	if form.Has("steps[1][name]") || form.Has("idSite") || len(result) > 4000 {
		t.Errorf("form = %v, result length %d", form, len(result))
	}
	var out struct {
		Result struct {
			URL  string `json:"url"`
			Rows []int  `json:"rows"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Result.URL) != maxUrlLength || len(out.Result.Rows) != maxResultEntries {
		t.Errorf("result = %.200s", result)
	}
}

func TestFunnelOptionsListReturnsTheGroupsMatomoGives(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
	result, err := env.invoke("matomo.funnel-options.list", "two", `{}`)
	if err != nil || !strings.Contains(result, `"path_startswith":"Path starts with"`) || !strings.Contains(result, `"eventname"`) ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated Funnels.getAvailablePatternMatches" {
		t.Errorf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
}

func TestFunnelRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
	for _, c := range funnelCalls {
		if _, err := env.invoke(c.tool, "two", c.arguments); err != nil {
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

func TestFunnelToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	methodsOf := map[string]string{"matomo.funnels.list": "Funnels.hasAnyActivatedFunnelForSite",
		"matomo.funnels.get": "Funnels.getFunnel", "matomo.funnels.test-steps": "Funnels.testUrlMatchesSteps",
		"matomo.funnel-options.list": "Funnels.getAvailablePatternMatches"}
	for _, c := range funnelCalls {
		method := methodsOf[c.tool]
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure("An unexpected website was found in the request: website id was set to '1' ." + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'Funnels.x' does not exist or is not available in the module 'Funnels'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin Funnels is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == method {
					return tc.answer(in)
				}
				return funnelAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
		}
	}
}

func TestFunnelToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range funnelCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(funnelAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != funnelsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestFunnelsListEndsUnsupportedWhenTheSecondMethodIsMissing(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(answers{
		"Funnels.getAllActivatedFunnelsForSite": func(call) *http.Response {
			return failure("The method 'Funnels.getAllActivatedFunnelsForSite' does not exist or is not available in the module 'Funnels'.")
		},
	}).handler(t))
	if _, err := env.invoke("matomo.funnels.list", "two", `{"idSite":1}`); !isUnsupported(err) {
		t.Errorf("err = %v", err)
	}
}

func TestFunnelsListRejectsAnInvalidFlagAndList(t *testing.T) {
	for name, extra := range map[string]answers{
		"flag":      {"Funnels.hasAnyActivatedFunnelForSite": fixed(`{"value":["x"]}`)},
		"list text": {"Funnels.getAllActivatedFunnelsForSite": fixed(`"text"`)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, funnelAnswers(extra).handler(t))
		if _, err := env.invoke("matomo.funnels.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestFunnelTextIsShortened(t *testing.T) {
	long := strings.Repeat("n", 3000)
	body := `{"idfunnel":7,"idsite":1,"name":"` + long + `","description":{"a":1},"steps":[{"name":"` + long + `","pattern":"` +
		strings.Repeat("p", 5000) + `"}]}`
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(answers{"Funnels.getFunnel": fixed(body)}).handler(t))
	result, err := env.invoke("matomo.funnels.get", "two", `{"idSite":1,"idFunnel":7}`)
	var funnel Funnel
	if err != nil || json.Unmarshal([]byte(result), &funnel) != nil || len(funnel.Name) != maxStringLength ||
		funnel.Description != "" || len(funnel.Steps) != 1 || len(funnel.Steps[0].Name) != maxStringLength ||
		len(funnel.Steps[0].Pattern) != maxUrlLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
}
