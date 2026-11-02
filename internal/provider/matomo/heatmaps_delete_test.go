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

// Assumptions: the reference documents deleteHeatmap only as returning GenericSuccess, whose form
// {"result":"success","message":"ok"} follows the core convention and is not shown. It describes
// deleteHeatmapScreenshot only as an indication of success, taken here as that form or a boolean, bare or as {"value":true}.
var heatmapDeleteTools = []struct{ tool, method string }{
	{"matomo.heatmaps.delete", "HeatmapSessionRecording.deleteHeatmap"},
	{"matomo.heatmaps.delete-screenshot", "HeatmapSessionRecording.deleteHeatmapScreenshot"},
}

func heatmapDeleteAnswers(extra answers) answers {
	base := answers{}
	for _, c := range heatmapDeleteTools {
		base[c.method] = fixed(`{"result":"success","message":"ok"}`)
	}
	return heatmapChangeAnswers(with(base, extra))
}

const heatmapDeleteArguments = `{"idSite":1,"idSiteHsr":11}`

func TestHeatmapDeleteToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, c := range heatmapDeleteTools {
		d, _, found := reg.Lookup(c.tool)
		if !found {
			t.Fatalf("%s is not registered", c.tool)
		}
		if d.Risk.Effect != capability.EffectDelete || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != heatmapsSensitive || !d.RequiresToolAllowList ||
			!strings.Contains(d.Description, "never repeated") ||
			!strings.Contains(string(d.InputSchema), `"required":["idSiteHsr"]`) {
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

func TestHeatmapDeleteReadsTheHeatmapThenSendsOneRequest(t *testing.T) {
	for _, c := range heatmapDeleteTools {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(nil).handler(t))
		if _, err := env.invoke(c.tool, "heatmap-deleter", heatmapDeleteArguments); err == nil ||
			!strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", c.tool, err, len(calls))
		}
		result, err := env.change(c.tool, "heatmap-deleter", heatmapDeleteArguments)
		var out map[string]any
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || out["idSite"] != float64(1) ||
			out["idSiteHsr"] != float64(11) || out["deleted"] != true || len(out) != 3 {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmap "+c.method {
			t.Errorf("%s: requests = %s", c.tool, got)
		}
		form := lastCall(calls, c.method).form
		if form.Get("idSite") != "1" || form.Get("idSiteHsr") != "11" || form.Get("format") != "JSON" || form.Get("module") != "API" ||
			form.Get("method") != c.method || len(form) != 5 {
			t.Errorf("%s: form = %v", c.tool, form)
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

func TestHeatmapDeleteRefusesAnotherWebsiteAndHeatmapWithoutMutation(t *testing.T) {
	for _, c := range heatmapDeleteTools {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(nil).handler(t))
		for _, tc := range []struct{ connection, arguments string }{
			{"heatmap-deleter-two", heatmapDeleteArguments},
			{"heatmap-deleter-both", `{"idSiteHsr":11}`},
			{"heatmap-deleter", `{"idSite":1}`},
			{"heatmap-deleter", `{"idSite":1,"idSiteHsr":0}`},
			{"heatmap-deleter", `{"idSite":1,"idSiteHsr":11,"module":"API"}`},
		} {
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
			_, err := env.change(c.tool, "heatmap-deleter", `{"idSite":1,"idSiteHsr":`+id+`}`)
			if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse ||
				countMethod(calls, c.method) != 0 || countMethod(calls, "HeatmapSessionRecording.getHeatmap") != 1 ||
				strings.Contains(err.Error(), "site/") {
				t.Errorf("%s heatmap %s: requests = %s, err = %v", c.tool, id, methods(calls), err)
			}
		}
	}
}

func TestHeatmapDeleteMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range heatmapDeleteTools {
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
			env := newHeatmapChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return heatmapDeleteAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, "heatmap-deleter", heatmapDeleteArguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestHeatmapDeleteToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range heatmapDeleteTools {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, with(heatmapDeleteAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "heatmap-deleter", heatmapDeleteArguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestHeatmapDeleteToolsAreReachableOnlyThroughTheToolsList(t *testing.T) {
	for _, c := range heatmapDeleteTools {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(nil).handler(t))
		if _, err := env.change(c.tool, "heatmap-effects-delete", heatmapDeleteArguments); err == nil ||
			len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s through effects only: err = %v, requests = %d", c.tool, err, len(calls))
		}
		if _, err := env.change(c.tool, "heatmap-deleter", heatmapDeleteArguments); err != nil {
			t.Errorf("%s through the tools list: %v", c.tool, err)
		}
	}
}

func TestHeatmapDeleteScreenshotSendsCaptureManuallyOnlyWhenGiven(t *testing.T) {
	const tool, method = "matomo.heatmaps.delete-screenshot", "HeatmapSessionRecording.deleteHeatmapScreenshot"
	for _, tc := range []struct{ arguments, want string }{
		{heatmapDeleteArguments, ""},
		{`{"idSite":1,"idSiteHsr":11,"captureManually":true}`, "1"},
		{`{"idSite":1,"idSiteHsr":11,"captureManually":false}`, "0"},
	} {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(nil).handler(t))
		if _, err := env.change(tool, "heatmap-deleter", tc.arguments); err != nil {
			t.Fatalf("%s: %v", tc.arguments, err)
		}
		form := lastCall(calls, method).form
		if form.Has("captureManually") != (tc.want != "") || form.Get("captureManually") != tc.want {
			t.Errorf("%s: form = %v", tc.arguments, form)
		}
		if tc.want == "" && len(form) != 5 || tc.want != "" && len(form) != 6 {
			t.Errorf("%s: form = %v", tc.arguments, form)
		}
	}
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(nil).handler(t))
	for _, arguments := range []string{`{"idSite":1,"idSiteHsr":11,"captureManually":"yes"}`} {
		if _, err := env.change(tool, "heatmap-deleter", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if _, err := env.change("matomo.heatmaps.delete", "heatmap-deleter",
		`{"idSite":1,"idSiteHsr":11,"captureManually":true}`); err == nil || len(calls) != 0 {
		t.Errorf("heatmaps.delete took captureManually: err = %v, requests = %d", err, len(calls))
	}
}

func TestHeatmapDeleteScreenshotReadsItsBooleanAnswer(t *testing.T) {
	const tool, method = "matomo.heatmaps.delete-screenshot", "HeatmapSessionRecording.deleteHeatmapScreenshot"
	for name, tc := range map[string]struct {
		answer  string
		deleted bool
		unclear bool
	}{
		"generic success": {`{"result":"success","message":"ok"}`, true, false},
		"value true":      {`{"value":true}`, true, false},
		"bare true":       {`true`, true, false},
		"value false":     {`{"value":false}`, false, false},
		"bare false":      {`false`, false, false},
		"other":           {`{"value":"none"}`, false, true},
	} {
		var calls []call
		env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(answers{method: fixed(tc.answer)}).handler(t))
		result, err := env.change(tool, "heatmap-deleter", heatmapDeleteArguments)
		if tc.deleted != (err == nil) || countMethod(calls, method) != 1 ||
			err != nil && strings.Contains(err.Error(), "never repeated") != tc.unclear ||
			err != nil && strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: result = %s, err = %v, sent %d times", name, result, err, countMethod(calls, method))
		}
	}
	var calls []call
	env := newHeatmapChangeEnvironment(t, &calls, heatmapDeleteAnswers(answers{
		"HeatmapSessionRecording.deleteHeatmap": fixed(`{"value":true}`)}).handler(t))
	if _, err := env.change("matomo.heatmaps.delete", "heatmap-deleter", heatmapDeleteArguments); err == nil {
		t.Error("heatmaps.delete accepted a bare boolean")
	}
}
