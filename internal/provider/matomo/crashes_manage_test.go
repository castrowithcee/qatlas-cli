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

// Assumptions, as the CrashAnalytics reference gives no example for the write methods and the public demo allows
// only reads: setIgnoreCrash, mergeCrashes, and unmergeCrashGroup answer the generic success form
// {"result":"success","message":"ok"} of the core convention. getIgnoredCrashes is read as a list of rows that carry
// idlogcrash and idsite like getAllCrashes (the demo holds no ignored crash, so its rows were never seen), and
// getCrashGroups as the object keyed by the parent crash ID that crashes.go already assumes. The list format of
// idLogCrashes is comma-separated; the reference names a single ID, an array, and a comma-separated list.
const crashConnections = `
  crash-writer: {service: matomo, credential: token, effects: [read, update]}
  crash-writer-one: {service: matomo, credential: token, effects: [read, update], targets: [site/1]}
  crash-writer-two: {service: matomo, credential: token, effects: [read, update], targets: [site/2]}
  crash-reader: {service: matomo, credential: token, effects: [read]}
`

func newCrashChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(crashConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

const crashSuccess = `{"result":"success","message":"ok"}`

func crashChangeAnswers(extra answers) answers {
	return crashAnswers(with(answers{
		"CrashAnalytics.getIgnoredCrashes": fixed(`[{"idlogcrash":105,"idsite":1},{"idlogcrash":300,"idsite":3},{"idlogcrash":301}]`),
		"CrashAnalytics.setIgnoreCrash":    fixed(crashSuccess),
		"CrashAnalytics.mergeCrashes":      fixed(crashSuccess),
		"CrashAnalytics.unmergeCrashGroup": fixed(crashSuccess),
	}, extra))
}

// crashChanges lists each change with the one read that binds its crashes to the website and the one mutation.
var crashChanges = []struct{ name, tool, arguments, read, method, want string }{
	{"ignore", "matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":105}`, "CrashAnalytics.getAllCrashes",
		"CrashAnalytics.setIgnoreCrash", `{"idSite":1,"idLogCrash":105,"ignored":true}`},
	{"ignore explicitly", "matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":105,"ignore":true}`, "CrashAnalytics.getAllCrashes",
		"CrashAnalytics.setIgnoreCrash", `{"idSite":1,"idLogCrash":105,"ignored":true}`},
	{"stop ignoring", "matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":105,"ignore":false}`, "CrashAnalytics.getIgnoredCrashes",
		"CrashAnalytics.setIgnoreCrash", `{"idSite":1,"idLogCrash":105,"ignored":false}`},
	{"merge", "matomo.crashes.merge", `{"idSite":1,"idLogCrashes":[105,119,44]}`, "CrashAnalytics.getAllCrashes",
		"CrashAnalytics.mergeCrashes", `{"idSite":1,"idLogCrashes":[105,119,44],"merged":true}`},
	{"unmerge", "matomo.crashes.unmerge", `{"idSite":1,"idLogCrash":112}`, "CrashAnalytics.getCrashGroups",
		"CrashAnalytics.unmergeCrashGroup", `{"idSite":1,"idLogCrash":112,"unmerged":true}`},
}

var crashChangeTools = []string{"matomo.crashes.set-ignored", "matomo.crashes.merge", "matomo.crashes.unmerge"}

func TestCrashChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]capability.Idempotency{"matomo.crashes.set-ignored": capability.IdempotencyIdempotent,
		"matomo.crashes.merge": capability.IdempotencyNonIdempotent, "matomo.crashes.unmerge": capability.IdempotencyIdempotent}
	for _, tool := range crashChangeTools {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		if d.Risk.Effect != capability.EffectUpdate || d.Risk.Idempotency != want[tool] ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != crashesSensitive || d.RequiresToolAllowList ||
			!strings.Contains(d.Description, "never repeated") || !strings.Contains(d.Description, "CrashAnalytics") {
			t.Errorf("%s: risk = %+v, description %q", tool, d.Risk, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "disable_generic_filters"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", tool, banned)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, selected := range profile.Tools {
				if selected == tool {
					t.Errorf("profile %s selects %s", profile.ID, tool)
				}
			}
		}
	}
}

func TestCrashChangesProveTheCrashesOnceThenSendOneRequest(t *testing.T) {
	for _, c := range crashChanges {
		var calls []call
		env := newCrashChangeEnvironment(t, &calls, crashChangeAnswers(nil).handler(t))
		if _, err := env.invoke(c.tool, "crash-writer", c.arguments); err == nil || !strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s %s without confirm: err = %v, requests = %d", c.tool, c.name, err, len(calls))
		}
		result, err := env.change(c.tool, "crash-writer", c.arguments)
		if err != nil || !sameJSON(result, c.want) {
			t.Fatalf("%s %s: result = %s, err = %v", c.tool, c.name, result, err)
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated "+c.read+" "+c.method {
			t.Errorf("%s %s: requests = %s", c.tool, c.name, got)
		}
		if countMethod(calls, c.read) != 1 || countMethod(calls, c.method) != 1 {
			t.Errorf("%s %s: requests = %s", c.tool, c.name, methods(calls))
		}
		if read := lastCall(calls, c.read).form; read.Get("idSite") != "1" || read.Has("filter_sort_column") ||
			c.read == "CrashAnalytics.getAllCrashes" && (read.Get("filter_limit") != "10000" || read.Get("filter_offset") != "0") ||
			c.read != "CrashAnalytics.getAllCrashes" && (read.Has("filter_limit") || read.Has("filter_offset")) {
			t.Errorf("%s %s: proof form = %v", c.tool, c.name, read)
		}
		form := lastCall(calls, c.method).form
		if form.Get("idSite") != "1" || form.Get("format") != "JSON" || form.Get("module") != "API" || form.Get("method") != c.method {
			t.Errorf("%s %s: form = %v", c.tool, c.name, form)
		}
		switch c.tool {
		case "matomo.crashes.set-ignored":
			flag := "1"
			if strings.Contains(c.arguments, `"ignore":false`) {
				flag = "0"
			}
			if form.Get("idLogCrash") != "105" || form.Get("ignore") != flag || len(form) != 6 {
				t.Errorf("%s %s: form = %v", c.tool, c.name, form)
			}
		case "matomo.crashes.merge":
			if form.Get("idLogCrashes") != "105,119,44" || len(form) != 5 {
				t.Errorf("%s: form = %v", c.tool, form)
			}
		default:
			if form.Get("idLogCrash") != "112" || len(form) != 5 {
				t.Errorf("%s: form = %v", c.tool, form)
			}
		}
		for _, in := range calls {
			if in.auth != "Bearer "+tokenValue || in.rawQuery != "" || in.method != http.MethodPost ||
				strings.Contains(in.body, tokenValue) || in.form.Has("token_auth") {
				t.Errorf("%s %s: auth %q, query %q, body %q", c.tool, in.api(), in.auth, in.rawQuery, in.body)
			}
			for _, banned := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
				"disable_root_datatable_post_processor"} {
				if in.form.Has(banned) {
					t.Errorf("%s sends %s", in.api(), banned)
				}
			}
			if in.form.Get("filter_limit") == "-1" {
				t.Errorf("%s asks for every row", in.api())
			}
		}
		if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
			t.Errorf("redacted text = %q", got)
		}
	}
}

func TestCrashChangesRefuseWhatTheReadDoesNotShowWithoutMutation(t *testing.T) {
	for name, tc := range map[string]struct{ tool, arguments, read string }{
		"ignore crash of another website":     {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":300}`, "CrashAnalytics.getAllCrashes"},
		"ignore crash of list of another":     {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":106}`, "CrashAnalytics.getAllCrashes"},
		"ignore crash without website":        {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":301}`, "CrashAnalytics.getAllCrashes"},
		"ignore missing crash":                {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":999}`, "CrashAnalytics.getAllCrashes"},
		"unignore crash that is not ignored":  {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":119,"ignore":false}`, "CrashAnalytics.getIgnoredCrashes"},
		"unignore crash of another website":   {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":300,"ignore":false}`, "CrashAnalytics.getIgnoredCrashes"},
		"unignore crash without website":      {"matomo.crashes.set-ignored", `{"idSite":1,"idLogCrash":301,"ignore":false}`, "CrashAnalytics.getIgnoredCrashes"},
		"merge with one crash of another":     {"matomo.crashes.merge", `{"idSite":1,"idLogCrashes":[105,119,300]}`, "CrashAnalytics.getAllCrashes"},
		"merge with one crash missing":        {"matomo.crashes.merge", `{"idSite":1,"idLogCrashes":[105,999]}`, "CrashAnalytics.getAllCrashes"},
		"merge with one crash without a site": {"matomo.crashes.merge", `{"idSite":1,"idLogCrashes":[301,105]}`, "CrashAnalytics.getAllCrashes"},
		"unmerge crash that is no parent":     {"matomo.crashes.unmerge", `{"idSite":1,"idLogCrash":44}`, "CrashAnalytics.getCrashGroups"},
		"unmerge group of another website":    {"matomo.crashes.unmerge", `{"idSite":1,"idLogCrash":30}`, "CrashAnalytics.getCrashGroups"},
		"unmerge group of website 2":          {"matomo.crashes.unmerge", `{"idSite":1,"idLogCrash":7}`, "CrashAnalytics.getCrashGroups"},
		"unmerge missing group":               {"matomo.crashes.unmerge", `{"idSite":1,"idLogCrash":999}`, "CrashAnalytics.getCrashGroups"},
	} {
		var calls []call
		env := newCrashChangeEnvironment(t, &calls, crashChangeAnswers(nil).handler(t))
		_, err := env.change(tc.tool, "crash-writer", tc.arguments)
		if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "300") || strings.Contains(err.Error(), "Foreign") ||
			strings.Contains(err.Error(), "999") {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, tc.read) != 1 || strings.Contains(methods(calls), "setIgnoreCrash") ||
			strings.Contains(methods(calls), "mergeCrashes") || strings.Contains(methods(calls), "unmergeCrashGroup") {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestCrashChangesRefuseInvalidAndForeignInputBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newCrashChangeEnvironment(t, &calls, crashChangeAnswers(nil).handler(t))
	many := make([]string, 101)
	for i := range many {
		many[i] = itoa(i + 1)
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.crashes.set-ignored", "crash-writer-two", `{"idSite":1,"idLogCrash":105}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idLogCrash":105}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1,"idLogCrash":0}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1,"idLogCrash":2147483648}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1,"idLogCrash":105,"ignore":"yes"}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1,"idLogCrash":105,"ignore":1}`},
		{"matomo.crashes.set-ignored", "crash-writer", `{"idSite":1,"idLogCrash":105,"module":"API"}`},
		{"matomo.crashes.set-ignored", "crash-reader", `{"idSite":1,"idLogCrash":105}`},
		{"matomo.crashes.merge", "crash-writer-two", `{"idSite":1,"idLogCrashes":[105,119]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idLogCrashes":[105,119]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[105]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[105,105]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[105,0]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[105,"119"]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":"105,119"}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[` + strings.Join(many, ",") + `]}`},
		{"matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[105,119],"method":"x"}`},
		{"matomo.crashes.unmerge", "crash-writer-two", `{"idSite":1,"idLogCrash":112}`},
		{"matomo.crashes.unmerge", "crash-writer", `{"idLogCrash":112}`},
		{"matomo.crashes.unmerge", "crash-writer", `{"idSite":1}`},
		{"matomo.crashes.unmerge", "crash-writer", `{"idSite":1,"idLogCrash":0}`},
		{"matomo.crashes.unmerge", "crash-writer", `{"idSite":1,"idLogCrash":112,"token_auth":"x"}`},
	} {
		if _, err := env.change(c.tool, c.connection, c.arguments); err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=1") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s names a value: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestCrashChangesMapMatomoErrorsAndNeverRepeat(t *testing.T) {
	for _, c := range crashChanges {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
			"object not found": {func(call) (*http.Response, error) { return ok2(failure(crashMissing + bodyCanary)) },
				provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'CrashAnalytics'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin CrashAnalytics is not activated." + bodyCanary))
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
			env := newCrashChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return crashChangeAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, "crash-writer", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), crashMissing) ||
				isUnsupported(err) != tc.unsupported || countMethod(calls, c.method) != 1 ||
				strings.Contains(err.Error(), "never repeated") != tc.unclear || tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s %s: err = %v (class %q), %s sent %d times", c.tool, c.name, name, err, classOf(err), c.method,
					countMethod(calls, c.method))
			}
			if name == "429" && strings.Contains(strings.ToLower(err.Error()), "retry after") {
				t.Errorf("%s: err = %v", c.tool, err)
			}
		}
	}
}

func TestCrashChangeProofErrorsMapAndSendNoMutation(t *testing.T) {
	for _, c := range crashChanges {
		for name, tc := range map[string]struct {
			answer func(call) *http.Response
			class  provider.Class
		}{
			"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission},
			"unreadable": {func(call) *http.Response { return ok(`"x"`) }, provider.ClassInvalidResponse},
		} {
			var calls []call
			env := newCrashChangeEnvironment(t, &calls, crashChangeAnswers(answers{c.read: tc.answer}).handler(t))
			_, err := env.change(c.tool, "crash-writer", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || classOf(err) != tc.class || countMethod(calls, c.method) != 0 {
				t.Errorf("%s %s %s: err = %v, requests = %s", c.tool, c.name, name, err, methods(calls))
			}
		}
	}
}

func TestCrashChangesAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range crashChanges {
		var calls []call
		env := newCrashChangeEnvironment(t, &calls, with(crashChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "crash-writer", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s %s: err = %v, requests = %s", c.tool, c.name, err, methods(calls))
		}
	}
}

func TestCrashMergeKeepsTheOrderGivenAndAcceptsTheLimit(t *testing.T) {
	ids := make([]string, 0, maxCrashMerge)
	rows := make([]string, 0, maxCrashMerge)
	for i := maxCrashMerge; i >= 1; i-- {
		ids = append(ids, itoa(i))
		rows = append(rows, crashRow(i, "1"))
	}
	var calls []call
	env := newCrashChangeEnvironment(t, &calls, crashChangeAnswers(answers{
		"CrashAnalytics.getAllCrashes": fixed("[" + strings.Join(rows, ",") + "]"),
	}).handler(t))
	result, err := env.change("matomo.crashes.merge", "crash-writer", `{"idSite":1,"idLogCrashes":[`+strings.Join(ids, ",")+`]}`)
	var out CrashesMerged
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.IDLogCrashes) != maxCrashMerge || !out.Merged ||
		lastCall(calls, "CrashAnalytics.mergeCrashes").form.Get("idLogCrashes") != strings.Join(ids, ",") {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
}
