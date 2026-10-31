package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// Assumptions, as the HeatmapSessionRecording reference gives no example for the write methods: addHeatmap
// answers the ID of the new heatmap (bare or as {"value":N}), updateHeatmap answers the generic success form
// {"result":"success","message":"ok"}, and duplicateHeatmap answers an object whose form is only described in
// words, here with a member success and the IDs of the new heatmaps.
const heatmapConnections = `
  heatmap-writer: {service: matomo, credential: token, effects: [read, create, update]}
  heatmap-writer-one: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1]}
  heatmap-writer-two: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  heatmap-writer-both: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2]}
  heatmap-reader: {service: matomo, credential: token, effects: [read]}
`

func newHeatmapChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(heatmapConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func heatmapChangeAnswers(extra answers) answers {
	return heatmapAnswers(with(answers{
		"HeatmapSessionRecording.addHeatmap":       fixed(`{"value":15}`),
		"HeatmapSessionRecording.updateHeatmap":    fixed(`{"result":"success","message":"ok"}`),
		"HeatmapSessionRecording.duplicateHeatmap": fixed(`{"success":true,"ids":[31,32]}`),
	}, extra))
}

const homeRules = `[{"attribute":"url","type":"equals_simple","value":"https://example.org/","inverted":false}]`

var heatmapChanges = []struct{ tool, arguments, method, reads string }{
	{"matomo.heatmaps.add", `{"idSite":1,"name":"Home","matchPageRules":` + homeRules + `}`,
		"HeatmapSessionRecording.addHeatmap", ""},
	{"matomo.heatmaps.update", `{"idSite":1,"idSiteHsr":11,"name":"Home","matchPageRules":` + homeRules + `}`,
		"HeatmapSessionRecording.updateHeatmap", "HeatmapSessionRecording.getHeatmap"},
	{"matomo.heatmaps.duplicate", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[1,2]}`,
		"HeatmapSessionRecording.duplicateHeatmap", "HeatmapSessionRecording.getHeatmap"},
}

func TestHeatmapChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.heatmaps.add":       {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.heatmaps.update":    {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.heatmaps.duplicate": {capability.EffectCreate, capability.IdempotencyNonIdempotent},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != heatmapsSensitive || d.RequiresToolAllowList {
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
	update, _, _ := reg.Lookup("matomo.heatmaps.update")
	if !strings.Contains(update.Description, "defaults") || !strings.Contains(string(update.InputSchema), `"required":["idSiteHsr","name","matchPageRules"]`) {
		t.Errorf("update: description %q, schema %s", update.Description, update.InputSchema)
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

func TestHeatmapAddSendsOneRequestWithTheGivenSettingsOnly(t *testing.T) {
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.heatmaps.add", "heatmap-writer", heatmapChanges[0].arguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change("matomo.heatmaps.add", "heatmap-reader", heatmapChanges[0].arguments); err == nil || len(calls) != 0 {
		t.Fatalf("a read connection offered the tool: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.heatmaps.add", "heatmap-writer", heatmapChanges[0].arguments)
	var out HeatmapCreated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDSiteHsr != 15 || !out.Created {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.addHeatmap" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "HeatmapSessionRecording.addHeatmap").form
	for key, want := range map[string]string{"idSite": "1", "name": "Home", "matchPageRules[0][attribute]": "url",
		"matchPageRules[0][type]": "equals_simple", "matchPageRules[0][value]": "https://example.org/",
		"matchPageRules[0][inverted]": "0"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	for _, absent := range []string{"sampleLimit", "sampleRate", "excludedElements", "screenshotUrl", "breakpointMobile",
		"breakpointTablet", "captureDomManually", "description", "autoRepeat"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}

	calls = nil
	full := `{"idSite":1,"name":"Pricing","matchPageRules":[{"attribute":"url","type":"contains","value":"pricing","inverted":true},` +
		`{"attribute":"path","type":"equals_simple","value":"/plans"}],"sampleLimit":2000,"sampleRate":12.5,` +
		`"excludedElements":"#popup, .banner","screenshotUrl":"https://example.org/pricing","breakpointMobile":600,` +
		`"breakpointTablet":960,"captureDomManually":true,"description":"Test","autoRepeat":false}`
	if _, err := env.change("matomo.heatmaps.add", "heatmap-writer", full); err != nil {
		t.Fatal(err)
	}
	form = lastCall(calls, "HeatmapSessionRecording.addHeatmap").form
	for key, want := range map[string]string{"matchPageRules[0][inverted]": "1", "matchPageRules[1][value]": "/plans",
		"sampleLimit": "2000", "sampleRate": "12.5", "excludedElements": "#popup, .banner",
		"screenshotUrl": "https://example.org/pricing", "breakpointMobile": "600", "breakpointTablet": "960",
		"captureDomManually": "1", "description": "Test", "autoRepeat": "0"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	if form.Has("matchPageRules[1][inverted]") {
		t.Error("an inverted flag that was not given was sent")
	}

	// A bare ID is accepted as well.
	calls = nil
	env = newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(answers{
		"HeatmapSessionRecording.addHeatmap": fixed(`21`)}).handler(t))
	result, err = env.change("matomo.heatmaps.add", "heatmap-writer", heatmapChanges[0].arguments)
	out = HeatmapCreated{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSiteHsr != 21 {
		t.Errorf("bare ID: result = %s, err = %v", result, err)
	}
}

func TestHeatmapUpdateReadsTheHeatmapBeforeTheOneReplacingRequest(t *testing.T) {
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
	arguments := `{"idSite":1,"idSiteHsr":11,"name":"Home","matchPageRules":` + homeRules + `,"sampleLimit":2000,` +
		`"sampleRate":100,"breakpointMobile":600}`
	result, err := env.change("matomo.heatmaps.update", "heatmap-writer", arguments)
	var out HeatmapSaved
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDSiteHsr != 11 || !out.Saved {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmap HeatmapSessionRecording.updateHeatmap" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "HeatmapSessionRecording.updateHeatmap").form
	for key, want := range map[string]string{"idSite": "1", "idSiteHsr": "11", "name": "Home", "sampleLimit": "2000",
		"sampleRate": "100", "breakpointMobile": "600", "matchPageRules[0][value]": "https://example.org/"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	// Nothing is filled in from the heatmap that was read: a setting that is not given is not sent.
	for _, absent := range []string{"excludedElements", "screenshotUrl", "breakpointTablet", "captureDomManually", "description", "autoRepeat"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}
}

func TestHeatmapChangeRefusesAHeatmapOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, c := range []struct{ tool, arguments, mutation string }{
		{"matomo.heatmaps.update", `{"idSite":1,"idSiteHsr":55,"name":"x","matchPageRules":` + homeRules + `}`, "HeatmapSessionRecording.updateHeatmap"},
		{"matomo.heatmaps.update", `{"idSite":1,"idSiteHsr":6,"name":"x","matchPageRules":` + homeRules + `}`, "HeatmapSessionRecording.updateHeatmap"},
		{"matomo.heatmaps.update", `{"idSite":1,"idSiteHsr":99,"name":"x","matchPageRules":` + homeRules + `}`, "HeatmapSessionRecording.updateHeatmap"},
		{"matomo.heatmaps.duplicate", `{"idSite":1,"idSiteHsr":55,"idDestinationSites":[2]}`, "HeatmapSessionRecording.duplicateHeatmap"},
		{"matomo.heatmaps.duplicate", `{"idSite":1,"idSiteHsr":99,"idDestinationSites":[2]}`, "HeatmapSessionRecording.duplicateHeatmap"},
	} {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
		_, err := env.change(c.tool, "heatmap-writer", c.arguments)
		if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s %s: err = %v", c.tool, c.arguments, err)
			continue
		}
		if countMethod(calls, c.mutation) != 0 || countMethod(calls, "HeatmapSessionRecording.getHeatmap") != 1 ||
			strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), "site/3") {
			t.Errorf("%s %s: requests = %s, err = %v", c.tool, c.arguments, methods(calls), err)
		}
	}
}

func TestHeatmapDuplicateSendsEveryDestinationInOneRequest(t *testing.T) {
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.heatmaps.duplicate", "heatmap-writer-both", heatmapChanges[2].arguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.heatmaps.duplicate", "heatmap-writer-both", heatmapChanges[2].arguments)
	var out HeatmapDuplicated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDSiteHsr != 11 ||
		len(out.IDDestinationSites) != 2 || out.IDDestinationSites[1] != 2 || !out.Duplicated || out.Answer == nil {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmap HeatmapSessionRecording.duplicateHeatmap" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "HeatmapSessionRecording.duplicateHeatmap").form
	for key, want := range map[string]string{"idSite": "1", "idSiteHsr": "11", "idDestinationSites[0]": "1", "idDestinationSites[1]": "2"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
}

func TestHeatmapChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
	rules := `"matchPageRules":` + homeRules
	manyRules := strings.TrimSuffix(strings.Repeat(`{"attribute":"url","type":"contains","value":"a"},`, 21), ",")
	var tooMany []string
	for i := 1; i <= 21; i++ {
		tooMany = append(tooMany, strconv.Itoa(i))
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.heatmaps.add", "heatmap-writer-two", `{"idSite":1,"name":"a",` + rules + `}`},
		{"matomo.heatmaps.add", "heatmap-writer-both", `{"name":"a",` + rules + `}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,` + rules + `}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"","matchPageRules":` + homeRules + `}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"` + strings.Repeat("n", 256) + `",` + rules + `}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a"}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[]}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[` + manyRules + `]}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[{"attribute":"Url","type":"contains","value":"a"}]}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[{"attribute":"url","type":"contains"}]}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"sampleRate":0}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"sampleRate":100.5}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"sampleRate":5.55}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"sampleLimit":0}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"breakpointMobile":0}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"breakpointTablet":100000}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"description":"` + strings.Repeat("d", 256) + `"}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"screenshotUrl":"` + strings.Repeat("u", 2049) + `"}`},
		{"matomo.heatmaps.add", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `,"callback":"x"}`},
		{"matomo.heatmaps.update", "heatmap-writer-two", `{"idSite":1,"idSiteHsr":11,"name":"a",` + rules + `}`},
		{"matomo.heatmaps.update", "heatmap-writer", `{"idSite":1,"idSiteHsr":0,"name":"a",` + rules + `}`},
		{"matomo.heatmaps.update", "heatmap-writer", `{"idSite":1,"name":"a",` + rules + `}`},
		{"matomo.heatmaps.update", "heatmap-writer", `{"idSite":1,"idSiteHsr":11,` + rules + `}`},
		{"matomo.heatmaps.update", "heatmap-writer", `{"idSite":1,"idSiteHsr":11,"name":"a"}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-two", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[2]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-one", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[1,2]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-one", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[2]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-both", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[2,3]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-both", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[2,2]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-both", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-both", `{"idSite":1,"idSiteHsr":11}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer-both", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[0]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer", `{"idSite":1,"idSiteHsr":11,"idDestinationSites":[` + strings.Join(tooMany, ",") + `]}`},
		{"matomo.heatmaps.duplicate", "heatmap-writer", `{"idSite":1,"idSiteHsr":0,"idDestinationSites":[2]}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite=1") ||
			strings.Contains(c.arguments, `[2,3]`) && strings.Contains(err.Error(), "3") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestHeatmapChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapChangeAnswers(nil).handler(t))
	for _, c := range heatmapChanges {
		if _, err := env.change(c.tool, "heatmap-writer-both", c.arguments); err != nil {
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

func TestHeatmapChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range heatmapChanges {
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
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'HeatmapSessionRecording'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin HeatmapSessionRecording is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
			"server":     {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, true},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, true},
			"no confirmation": {func(call) (*http.Response, error) {
				if c.tool == "matomo.heatmaps.duplicate" {
					return ok2(ok(`[]`))
				}
				return ok2(ok(`{"value":"none"}`))
			}, provider.ClassInvalidResponse, false, true},
			"reported failure": {func(call) (*http.Response, error) {
				return ok2(ok(`{"success":false,"message":"` + bodyCanary + `"}`))
			}, provider.ClassInvalidResponse, false, true},
		} {
			if name == "reported failure" && c.tool != "matomo.heatmaps.duplicate" {
				continue
			}
			var calls []call
			env := newHeatmapChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return heatmapChangeAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, "heatmap-writer-both", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestHeatmapChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range heatmapChanges {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, with(heatmapChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "heatmap-writer-both", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
