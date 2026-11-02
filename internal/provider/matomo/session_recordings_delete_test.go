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

// Assumption: the reference documents the three deletions only as returning GenericSuccess, whose form
// {"result":"success","message":"ok"} follows the core convention and is not shown.
var recordingDeleteTools = []struct {
	tool, method, arguments, subID string
	personal                       bool
}{
	{"matomo.session-recordings.delete", "HeatmapSessionRecording.deleteSessionRecording",
		`{"idSite":1,"idSiteHsr":13}`, "", false},
	{"matomo.recorded-sessions.delete-session", "HeatmapSessionRecording.deleteRecordedSession",
		`{"idSite":1,"idSiteHsr":13,"idVisit":2001}`, "idVisit", true},
	{"matomo.recorded-sessions.delete-pageview", "HeatmapSessionRecording.deleteRecordedPageview",
		`{"idSite":1,"idSiteHsr":13,"idLogHsr":3001}`, "idLogHsr", true},
}

const recordingDeleteConnections = `
  recording-deleter: {service: matomo, credential: token, tools: [matomo.session-recordings.delete, matomo.recorded-sessions.delete-session, matomo.recorded-sessions.delete-pageview]}
  recording-deleter-two: {service: matomo, credential: token, tools: [matomo.session-recordings.delete, matomo.recorded-sessions.delete-session, matomo.recorded-sessions.delete-pageview], targets: [site/2]}
  recording-deleter-both: {service: matomo, credential: token, tools: [matomo.session-recordings.delete, matomo.recorded-sessions.delete-session, matomo.recorded-sessions.delete-pageview], targets: [site/1, site/2]}
  recording-effects-delete: {service: matomo, credential: token, effects: [read, create, update, delete]}
`

func newRecordingDeleteEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(recordingDeleteConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func recordingDeleteAnswers(extra answers) answers {
	base := answers{}
	for _, c := range recordingDeleteTools {
		base[c.method] = fixed(`{"result":"success","message":"ok"}`)
	}
	return recordingAnswers(with(base, extra))
}

func TestRecordingDeleteToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, c := range recordingDeleteTools {
		d, _, found := reg.Lookup(c.tool)
		if !found {
			t.Fatalf("%s is not registered", c.tool)
		}
		sensitivity := sessionRecordingsSensitive
		if c.personal {
			sensitivity = recordedSessionsSensitive
		}
		if d.Risk.Effect != capability.EffectDelete || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != sensitivity || !d.RequiresToolAllowList ||
			!strings.Contains(d.Description, "never repeated") ||
			!strings.Contains(string(d.InputSchema), `"idSiteHsr"`) ||
			c.subID != "" && !strings.Contains(string(d.InputSchema), `"required":["idSiteHsr","`+c.subID+`"]`) {
			t.Errorf("%s: risk = %+v, description %q, schema %s", c.tool, d.Risk, d.Description, d.InputSchema)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, tool := range profile.Tools {
				if tool == c.tool {
					t.Errorf("profile %s selects %s", profile.ID, tool)
				}
			}
		}
	}
}

func TestRecordingDeleteReadsTheRecordingThenSendsOneRequest(t *testing.T) {
	for _, c := range recordingDeleteTools {
		var calls []call
		env := newRecordingDeleteEnvironment(t, &calls, recordingDeleteAnswers(nil).handler(t))
		if _, err := env.invoke(c.tool, "recording-deleter", c.arguments); err == nil ||
			!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", c.tool, err, len(calls))
		}
		result, err := env.change(c.tool, "recording-deleter", c.arguments)
		var out map[string]any
		wantLen := 3
		if c.subID != "" {
			wantLen = 4
		}
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || out["idSite"] != float64(1) ||
			out["idSiteHsr"] != float64(13) || out["deleted"] != true || len(out) != wantLen {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecording "+c.method {
			t.Errorf("%s: requests = %s", c.tool, got)
		}
		form := lastCall(calls, c.method).form
		if form.Get("idSite") != "1" || form.Get("idSiteHsr") != "13" || form.Get("format") != "JSON" ||
			form.Get("module") != "API" || form.Get("method") != c.method || len(form) != 4+(wantLen-3)+1 {
			t.Errorf("%s: form = %v", c.tool, form)
		}
		switch c.subID {
		case "idVisit":
			if form.Get("idVisit") != "2001" || form.Has("idLogHsr") {
				t.Errorf("%s: form = %v", c.tool, form)
			}
		case "idLogHsr":
			if form.Get("idLogHsr") != "3001" || form.Has("idVisit") {
				t.Errorf("%s: form = %v", c.tool, form)
			}
		}
		for _, in := range calls {
			if in.auth != "Bearer "+tokenValue || in.rawQuery != "" || in.method != http.MethodPost ||
				strings.Contains(in.body, tokenValue) || in.form.Has("token_auth") {
				t.Errorf("%s %s: auth %q, query %q, body %q", c.tool, in.api(), in.auth, in.rawQuery, in.body)
			}
			for _, banned := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
				"disable_root_datatable_post_processor", "filter_limit"} {
				if in.form.Has(banned) {
					t.Errorf("%s sends %s", in.api(), banned)
				}
			}
		}
		if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
			t.Errorf("redacted text = %q", got)
		}
	}
}

func TestRecordingDeleteRefusesBadArgumentsAndForeignRecordingsWithoutMutation(t *testing.T) {
	for _, c := range recordingDeleteTools {
		var calls []call
		env := newRecordingDeleteEnvironment(t, &calls, recordingDeleteAnswers(nil).handler(t))
		refused := []struct{ connection, arguments string }{
			{"recording-deleter-two", c.arguments},
			{"recording-deleter-both", strings.Replace(c.arguments, `"idSite":1,`, "", 1)},
			{"recording-deleter", `{"idSite":1}`},
			{"recording-deleter", `{"idSite":1,"idSiteHsr":0}`},
			{"recording-deleter", strings.Replace(c.arguments, `"idSiteHsr":13`, `"idSiteHsr":13,"module":"API"`, 1)},
			{"recording-deleter", strings.Replace(c.arguments, `"idSiteHsr":13`, `"idSiteHsr":13,"idVisit":5,"idLogHsr":6`, 1)},
		}
		if c.subID == "" {
			refused = append(refused, struct{ connection, arguments string }{
				"recording-deleter", `{"idSite":1,"idSiteHsr":13,"idVisit":2001}`})
		} else {
			missing := strings.Replace(c.arguments, `,"`+c.subID+`":`, `,"x":`, 1)
			refused = append(refused,
				struct{ connection, arguments string }{"recording-deleter", `{"idSite":1,"idSiteHsr":13}`},
				struct{ connection, arguments string }{"recording-deleter", missing},
				struct{ connection, arguments string }{"recording-deleter", `{"idSite":1,"idSiteHsr":13,"` + c.subID + `":0}`},
				struct{ connection, arguments string }{"recording-deleter", `{"idSite":1,"idSiteHsr":13,"` + c.subID + `":9007199254740992}`},
				struct{ connection, arguments string }{"recording-deleter", `{"idSite":1,"idSiteHsr":13,"` + c.subID + `":"5"}`})
		}
		for _, tc := range refused {
			if _, err := env.change(c.tool, tc.connection, tc.arguments); err == nil ||
				strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite=1") {
				t.Errorf("%s %s %s: err = %v", c.tool, tc.connection, tc.arguments, err)
			}
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d secret reads before the refusal", c.tool, len(calls), *env.reads)
		}
		for _, id := range []string{"55", "6", "99"} {
			calls = nil
			arguments := strings.Replace(c.arguments, `"idSiteHsr":13`, `"idSiteHsr":`+id, 1)
			_, err := env.change(c.tool, "recording-deleter", arguments)
			if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse ||
				countMethod(calls, c.method) != 0 || countMethod(calls, "HeatmapSessionRecording.getSessionRecording") != 1 ||
				strings.Contains(err.Error(), "site/") {
				t.Errorf("%s recording %s: requests = %s, err = %v", c.tool, id, methods(calls), err)
			}
		}
	}
}

func TestRecordingDeleteMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range recordingDeleteTools {
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
				return ok2(ok(`{"value":true}`))
			}, provider.ClassInvalidResponse, false, true},
		} {
			var calls []call
			env := newRecordingDeleteEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return recordingDeleteAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, "recording-deleter", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestRecordingDeleteToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range recordingDeleteTools {
		var calls []call
		env := newRecordingDeleteEnvironment(t, &calls, with(recordingDeleteAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "recording-deleter", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestRecordingDeleteToolsAreReachableOnlyThroughTheToolsList(t *testing.T) {
	for _, c := range recordingDeleteTools {
		var calls []call
		env := newRecordingDeleteEnvironment(t, &calls, recordingDeleteAnswers(nil).handler(t))
		if _, err := env.change(c.tool, "recording-effects-delete", c.arguments); err == nil ||
			len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s through effects only: err = %v, requests = %d", c.tool, err, len(calls))
		}
		if _, err := env.change(c.tool, "recording-deleter", c.arguments); err != nil {
			t.Errorf("%s through the tools list: %v", c.tool, err)
		}
	}
}
