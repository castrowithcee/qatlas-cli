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

// Assumptions, as the Funnels reference gives no answers for the write methods: setGoalFunnel and saveNonGoalFunnel
// answer the funnel ID (bare or as {"value":N}), and the two delete methods answer the generic success form
// {"result":"success"}. A plain funnel is a funnel without idgoal.
const plainFunnel = `{"idfunnel":8,"idsite":1,"name":"Pricing","description":"","revision":1,"steps":[],"activated":true}`

const funnelConnections = `
  funnel-writer: {service: matomo, credential: token, effects: [read, update]}
  funnel-writer-two: {service: matomo, credential: token, effects: [read, update], targets: [site/2]}
  funnel-deleter: {service: matomo, credential: token, tools: [matomo.funnels.delete-for-goal, matomo.funnels.delete], targets: [site/1]}
  funnel-deleter-two: {service: matomo, credential: token, tools: [matomo.funnels.delete-for-goal, matomo.funnels.delete], targets: [site/2]}
`

func newFunnelChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(funnelConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func funnelChangeAnswers(extra answers) answers {
	return funnelAnswers(with(answers{
		"Goals.getGoal": func(c call) *http.Response {
			if c.form.Get("idGoal") == "3" {
				return ok(goalOne)
			}
			if c.form.Get("idGoal") == "9" {
				return ok(strings.Replace(goalOne, `"idsite":"1"`, `"idsite":"3"`, 1))
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
		"Funnels.getFunnel": func(c call) *http.Response {
			switch c.form.Get("idFunnel") {
			case "8":
				return ok(plainFunnel)
			case "7":
				return ok(jobFunnel)
			case "55":
				return ok(foreignFunnel)
			}
			return ok(`null`)
		},
		"Funnels.setGoalFunnel":       fixed(`{"value":12}`),
		"Funnels.saveNonGoalFunnel":   fixed(`21`),
		"Funnels.deleteGoalFunnel":    fixed(`{"result":"success","message":"ok"}`),
		"Funnels.deleteNonGoalFunnel": fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

const twoSteps = `[{"pattern_type":"path_equals","pattern":"/jobs","name":"Jobs","required":true},` +
	`{"pattern_type":"path_equals","pattern":"/jobs/apply"}]`

var funnelChanges = []struct{ tool, connection, arguments, method string }{
	{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3,"isActivated":true,"steps":` + twoSteps + `}`, "Funnels.setGoalFunnel"},
	{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":8,"funnelName":"Pricing","steps":` + twoSteps + `}`, "Funnels.saveNonGoalFunnel"},
	{"matomo.funnels.delete-for-goal", "funnel-deleter", `{"idSite":1,"idGoal":4}`, "Funnels.deleteGoalFunnel"},
	{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":8}`, "Funnels.deleteNonGoalFunnel"},
}

func TestFunnelChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		"matomo.funnels.set-for-goal":    {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.funnels.save":            {capability.EffectUpdate, capability.IdempotencyNonIdempotent, false},
		"matomo.funnels.delete-for-goal": {capability.EffectDelete, capability.IdempotencyIdempotent, true},
		"matomo.funnels.delete":          {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != funnelsSensitive || d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s: risk = %+v, allow list = %t", id, d.Risk, d.RequiresToolAllowList)
		}
		if !strings.Contains(d.Description, "never repeated") || !strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s: description %q, schema %s", id, d.Description, d.InputSchema)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "idSegment"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", id, banned)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "funnels.") {
			t.Errorf("profile %s selects a funnel tool", profile.ID)
		}
	}
}

func TestFunnelSetForGoalBindsTheGoalAndSendsOneRequest(t *testing.T) {
	var calls []call
	env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.funnels.set-for-goal", "funnel-writer", funnelChanges[0].arguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.funnels.set-for-goal", "funnel-writer", funnelChanges[0].arguments)
	var out FunnelGoalSaved
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDGoal != 3 || out.IDFunnel == nil ||
		*out.IDFunnel != 12 || !out.Saved {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated Goals.getGoal Funnels.setGoalFunnel" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "Funnels.setGoalFunnel").form
	for key, want := range map[string]string{"idSite": "1", "idGoal": "3", "isActivated": "1",
		"steps[0][pattern_type]": "path_equals", "steps[0][pattern]": "/jobs", "steps[0][name]": "Jobs", "steps[0][required]": "1",
		"steps[1][pattern_type]": "path_equals", "steps[1][pattern]": "/jobs/apply"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}

	// The sales funnel is no goal: no goal read, and a deactivated funnel may have no steps. The generic success
	// form (assumed) is accepted without an ID.
	calls = nil
	env = newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(answers{
		"Funnels.setGoalFunnel": fixed(`{"result":"success","message":"ok"}`),
	}).handler(t))
	result, err = env.change("matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":0,"isActivated":false}`)
	if err != nil || strings.Contains(result, "idFunnel") || countMethod(calls, "Goals.getGoal") != 0 ||
		lastCall(calls, "Funnels.setGoalFunnel").form.Get("isActivated") != "0" || lastCall(calls, "Funnels.setGoalFunnel").form.Has("steps[0][pattern]") {
		t.Errorf("sales funnel: result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
}

func TestFunnelSaveCreatesWithoutReadAndUpdatesAfterOneProofRead(t *testing.T) {
	var calls []call
	env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
	create := `{"idSite":1,"idFunnel":0,"funnelName":"Pricing signup","description":"Pricing to signup","steps":` + twoSteps + `}`
	result, err := env.change("matomo.funnels.save", "funnel-writer", create)
	var out FunnelSaved
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDFunnel == nil || *out.IDFunnel != 21 || !out.Saved ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated Funnels.saveNonGoalFunnel" {
		t.Fatalf("create: result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	form := lastCall(calls, "Funnels.saveNonGoalFunnel").form
	for key, want := range map[string]string{"idSite": "1", "idFunnel": "0", "funnelName": "Pricing signup",
		"description": "Pricing to signup", "steps[0][pattern]": "/jobs", "steps[1][pattern]": "/jobs/apply"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}

	calls = nil
	env = newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(answers{
		"Funnels.saveNonGoalFunnel": fixed(`{"result":"success","message":"ok"}`),
	}).handler(t))
	result, err = env.change("matomo.funnels.save", "funnel-writer", funnelChanges[1].arguments)
	out = FunnelSaved{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDFunnel == nil || *out.IDFunnel != 8 ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated Funnels.getFunnel Funnels.saveNonGoalFunnel" ||
		lastCall(calls, "Funnels.saveNonGoalFunnel").form.Has("description") {
		t.Errorf("update: result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
}

func TestFunnelChangeRefusesObjectsOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		tool, connection, arguments, mutation string
		class                                 provider.Class
	}{
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":9,"isActivated":false}`, "Funnels.setGoalFunnel", provider.ClassInvalidResponse},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":8,"isActivated":false}`, "Funnels.setGoalFunnel", provider.ClassNotFound},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":55,"funnelName":"x","steps":` + twoSteps + `}`, "Funnels.saveNonGoalFunnel", provider.ClassNotFound},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":3,"funnelName":"x","steps":` + twoSteps + `}`, "Funnels.saveNonGoalFunnel", provider.ClassNotFound},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":7,"funnelName":"x","steps":` + twoSteps + `}`, "Funnels.saveNonGoalFunnel", ""},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":55}`, "Funnels.deleteNonGoalFunnel", provider.ClassNotFound},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":3}`, "Funnels.deleteNonGoalFunnel", provider.ClassNotFound},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":7}`, "Funnels.deleteNonGoalFunnel", ""},
		{"matomo.funnels.delete-for-goal", "funnel-deleter", `{"idSite":1,"idGoal":5}`, "Funnels.deleteGoalFunnel", provider.ClassNotFound},
	} {
		var calls []call
		env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
		_, err := env.change(tc.tool, tc.connection, tc.arguments)
		if err == nil || tc.class != "" && classOf(err) != tc.class || tc.class == "" && !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v", tc.tool, tc.arguments, err)
			continue
		}
		reads := countMethod(calls, "Goals.getGoal") + countMethod(calls, "Funnels.getFunnel") + countMethod(calls, "Funnels.getGoalFunnel")
		if countMethod(calls, tc.mutation) != 0 || reads != 1 || strings.Contains(err.Error(), "55") {
			t.Errorf("%s %s: requests = %s, err = %v", tc.tool, tc.arguments, methods(calls), err)
		}
	}
}

func TestFunnelDeleteToolsAreOnlyOfferedByTheToolsListAndNameTheFunnel(t *testing.T) {
	var calls []call
	env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
	for _, c := range funnelChanges[2:] {
		if _, err := env.change(c.tool, "funnel-writer", c.arguments); err == nil {
			t.Errorf("an effects connection offered %s", c.tool)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("a tool that is not offered caused %d requests", len(calls))
	}
	for _, c := range funnelChanges[2:] {
		if _, err := env.invoke(c.tool, c.connection, c.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s without confirm: err = %v", c.tool, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("an unconfirmed delete caused %d requests", len(calls))
	}
	result, err := env.change("matomo.funnels.delete-for-goal", "funnel-deleter", funnelChanges[2].arguments)
	var forGoal FunnelGoalDeleted
	if err != nil || json.Unmarshal([]byte(result), &forGoal) != nil || forGoal.IDFunnel != 7 || forGoal.IDGoal != 4 ||
		forGoal.Name != "New Job Application" || !forGoal.Deleted ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated Funnels.getGoalFunnel Funnels.deleteGoalFunnel" {
		t.Fatalf("delete-for-goal: result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if form := lastCall(calls, "Funnels.deleteGoalFunnel").form; form.Get("idSite") != "1" || form.Get("idGoal") != "4" || form.Has("idFunnel") {
		t.Errorf("form = %v", form)
	}
	calls = nil
	result, err = env.change("matomo.funnels.delete", "funnel-deleter", funnelChanges[3].arguments)
	var plain FunnelDeleted
	if err != nil || json.Unmarshal([]byte(result), &plain) != nil || plain.IDFunnel != 8 || plain.Name != "Pricing" || !plain.Deleted ||
		methods(calls) != "Funnels.getFunnel Funnels.deleteNonGoalFunnel" {
		t.Fatalf("delete: result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if form := lastCall(calls, "Funnels.deleteNonGoalFunnel").form; form.Get("idSite") != "1" || form.Get("idFunnel") != "8" || form.Has("idGoal") {
		t.Errorf("form = %v", form)
	}
}

func TestFunnelChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
	step := `{"pattern_type":"path_equals","pattern":"/a"}`
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.funnels.set-for-goal", "funnel-writer-two", `{"idSite":1,"idGoal":3,"isActivated":false}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idGoal":3,"isActivated":false}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"isActivated":false}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":-1,"isActivated":false}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3,"isActivated":true}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3,"isActivated":true,"steps":[]}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3,"isActivated":true,"steps":[{"pattern_type":"Path","pattern":"/a"}]}`},
		{"matomo.funnels.set-for-goal", "funnel-writer", `{"idSite":1,"idGoal":3,"isActivated":false,"callback":"x"}`},
		{"matomo.funnels.save", "funnel-writer-two", `{"idSite":1,"idFunnel":0,"funnelName":"a","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idFunnel":0,"funnelName":"a","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":-1,"funnelName":"a","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"` + strings.Repeat("n", 256) + `","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"a","description":"` + strings.Repeat("d", 256) + `","steps":[` + step + `]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"a","steps":[]}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"a"}`},
		{"matomo.funnels.save", "funnel-writer", `{"idSite":1,"idFunnel":0,"funnelName":"a","steps":[` + step + `],"idGoal":1}`},
		{"matomo.funnels.delete-for-goal", "funnel-deleter-two", `{"idSite":1,"idGoal":4}`},
		{"matomo.funnels.delete-for-goal", "funnel-deleter", `{"idSite":1}`},
		{"matomo.funnels.delete-for-goal", "funnel-deleter", `{"idSite":1,"idGoal":1000000000}`},
		{"matomo.funnels.delete", "funnel-deleter-two", `{"idSite":1,"idFunnel":8}`},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":0}`},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1}`},
		{"matomo.funnels.delete", "funnel-deleter", `{"idSite":1,"idFunnel":8,"idGoal":4}`},
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

func TestFunnelChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newFunnelChangeEnvironment(t, &calls, funnelChangeAnswers(nil).handler(t))
	for _, c := range funnelChanges {
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

func TestFunnelChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range funnelChanges {
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
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'Funnels'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin Funnels is not activated." + bodyCanary))
			}, "", true, false},
			"429":             {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
			"server":          {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, true},
			"unreadable":      {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, true},
			"no confirmation": {func(call) (*http.Response, error) { return ok2(ok(`{"value":"none"}`)) }, provider.ClassInvalidResponse, false, true},
		} {
			var calls []call
			env := newFunnelChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return funnelChangeAnswers(nil).handler(t)(in)
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

func TestFunnelChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range funnelChanges {
		var calls []call
		env := newFunnelChangeEnvironment(t, &calls, with(funnelChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
