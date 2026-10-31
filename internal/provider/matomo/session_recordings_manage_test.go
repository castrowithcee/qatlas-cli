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

// Assumptions, as the HeatmapSessionRecording reference gives no example for the write methods:
// addSessionRecording answers the ID of the new recording (bare or as {"value":N}) and updateSessionRecording
// answers the generic success form {"result":"success","message":"ok"}.

func newRecordingChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
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

func recordingChangeAnswers(extra answers) answers {
	return recordingAnswers(with(answers{
		"HeatmapSessionRecording.addSessionRecording":    fixed(`{"value":17}`),
		"HeatmapSessionRecording.updateSessionRecording": fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

var recordingChanges = []struct{ tool, arguments, method, reads string }{
	{"matomo.session-recordings.add", `{"idSite":1,"name":"Home"}`,
		"HeatmapSessionRecording.addSessionRecording", ""},
	{"matomo.session-recordings.update", `{"idSite":1,"idSiteHsr":13,"name":"Home"}`,
		"HeatmapSessionRecording.updateSessionRecording", "HeatmapSessionRecording.getSessionRecording"},
}

func TestSessionRecordingChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.session-recordings.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.session-recordings.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != sessionRecordingsSensitive || d.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v, allow list = %t", id, d.Risk, d.RequiresToolAllowList)
		}
		if !strings.Contains(d.Description, "never repeated") || !strings.Contains(d.Description, "type into forms") ||
			!strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s: description %q, schema %s", id, d.Description, d.InputSchema)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", id, banned)
			}
		}
	}
	update, _, _ := reg.Lookup("matomo.session-recordings.update")
	if !strings.Contains(update.Description, "reset") || !strings.Contains(string(update.InputSchema), `"required":["idSiteHsr","name"]`) {
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

func TestSessionRecordingAddSendsOneRequestWithTheGivenSettingsOnly(t *testing.T) {
	var calls []call
	env := newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.session-recordings.add", "heatmap-writer", recordingChanges[0].arguments); err == nil ||
		!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
		t.Fatalf("without confirm: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change("matomo.session-recordings.add", "heatmap-reader", recordingChanges[0].arguments); err == nil || len(calls) != 0 {
		t.Fatalf("a read connection offered the tool: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change("matomo.session-recordings.add", "heatmap-writer", recordingChanges[0].arguments)
	var out SessionRecordingCreated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDSiteHsr != 17 || !out.Created {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.addSessionRecording" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "HeatmapSessionRecording.addSessionRecording").form
	if form.Get("idSite") != "1" || form.Get("name") != "Home" {
		t.Errorf("form = %v", form)
	}
	for _, absent := range []string{"matchPageRules[0][attribute]", "sampleLimit", "sampleRate", "minSessionTime",
		"requiresActivity", "captureKeystrokes", "description", "autoRepeat"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}

	calls = nil
	full := `{"idSite":1,"name":"Checkout","matchPageRules":[{"attribute":"url","type":"contains","value":"checkout","inverted":true},` +
		`{"attribute":"path","type":"equals_simple","value":"/plans"}],"sampleLimit":2000,"sampleRate":12.5,` +
		`"minSessionTime":5,"requiresActivity":false,"captureKeystrokes":false,"description":"Test","autoRepeat":true}`
	if _, err := env.change("matomo.session-recordings.add", "heatmap-writer", full); err != nil {
		t.Fatal(err)
	}
	form = lastCall(calls, "HeatmapSessionRecording.addSessionRecording").form
	for key, want := range map[string]string{"matchPageRules[0][inverted]": "1", "matchPageRules[1][value]": "/plans",
		"sampleLimit": "2000", "sampleRate": "12.5", "minSessionTime": "5", "requiresActivity": "0",
		"captureKeystrokes": "0", "description": "Test", "autoRepeat": "1"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}

	calls = nil
	env = newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(answers{
		"HeatmapSessionRecording.addSessionRecording": fixed(`21`)}).handler(t))
	result, err = env.change("matomo.session-recordings.add", "heatmap-writer", recordingChanges[0].arguments)
	out = SessionRecordingCreated{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSiteHsr != 21 {
		t.Errorf("bare ID: result = %s, err = %v", result, err)
	}
}

func TestSessionRecordingUpdateReadsTheRecordingBeforeTheOneReplacingRequest(t *testing.T) {
	var calls []call
	env := newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(nil).handler(t))
	arguments := `{"idSite":1,"idSiteHsr":13,"name":"Home","sampleLimit":2000,"sampleRate":100,"captureKeystrokes":false}`
	result, err := env.change("matomo.session-recordings.update", "heatmap-writer", arguments)
	var out SessionRecordingSaved
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.IDSite != 1 || out.IDSiteHsr != 13 || !out.Saved {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecording HeatmapSessionRecording.updateSessionRecording" {
		t.Errorf("requests = %s", got)
	}
	form := lastCall(calls, "HeatmapSessionRecording.updateSessionRecording").form
	for key, want := range map[string]string{"idSite": "1", "idSiteHsr": "13", "name": "Home", "sampleLimit": "2000",
		"sampleRate": "100", "captureKeystrokes": "0"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	// Nothing is filled in from the recording that was read: a setting that is not given is not sent.
	for _, absent := range []string{"matchPageRules[0][attribute]", "minSessionTime", "requiresActivity", "description", "autoRepeat"} {
		if form.Has(absent) {
			t.Errorf("%s was sent although it was not given", absent)
		}
	}
}

func TestSessionRecordingUpdateRefusesARecordingOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, id := range []string{"55", "6", "99"} {
		var calls []call
		env := newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(nil).handler(t))
		_, err := env.change("matomo.session-recordings.update", "heatmap-writer",
			`{"idSite":1,"idSiteHsr":`+id+`,"name":"x"}`)
		if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", id, err)
			continue
		}
		if countMethod(calls, "HeatmapSessionRecording.updateSessionRecording") != 0 ||
			countMethod(calls, "HeatmapSessionRecording.getSessionRecording") != 1 ||
			strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), "site/3") {
			t.Errorf("%s: requests = %s, err = %v", id, methods(calls), err)
		}
	}
}

func TestSessionRecordingChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(nil).handler(t))
	manyRules := strings.TrimSuffix(strings.Repeat(`{"attribute":"url","type":"contains","value":"a"},`, 21), ",")
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.session-recordings.add", "heatmap-writer-two", `{"idSite":1,"name":"a"}`},
		{"matomo.session-recordings.add", "heatmap-writer-both", `{"name":"a"}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":""}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"` + strings.Repeat("n", 256) + `"}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[]}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[` + manyRules + `]}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","matchPageRules":[{"attribute":"Url","type":"contains","value":"a"}]}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","sampleRate":0}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","sampleRate":5.55}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","sampleLimit":0}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","minSessionTime":-1}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","description":"` + strings.Repeat("d", 256) + `"}`},
		{"matomo.session-recordings.add", "heatmap-writer", `{"idSite":1,"name":"a","callback":"x"}`},
		{"matomo.session-recordings.update", "heatmap-writer-two", `{"idSite":1,"idSiteHsr":13,"name":"a"}`},
		{"matomo.session-recordings.update", "heatmap-writer", `{"idSite":1,"idSiteHsr":0,"name":"a"}`},
		{"matomo.session-recordings.update", "heatmap-writer", `{"idSite":1,"name":"a"}`},
		{"matomo.session-recordings.update", "heatmap-writer", `{"idSite":1,"idSiteHsr":13}`},
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

func TestSessionRecordingChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newRecordingChangeEnvironment(t, &calls, recordingChangeAnswers(nil).handler(t))
	for _, c := range recordingChanges {
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

func TestSessionRecordingChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range recordingChanges {
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
				return ok2(ok(`{"value":"none"}`))
			}, provider.ClassInvalidResponse, false, true},
		} {
			var calls []call
			env := newRecordingChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return recordingChangeAnswers(nil).handler(t)(in)
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

func TestSessionRecordingChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range recordingChanges {
		var calls []call
		env := newRecordingChangeEnvironment(t, &calls, with(recordingChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "heatmap-writer-both", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
