package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// Assumptions, as the ActivityLog reference gives no example and the plugin source is not public: getEntries
// answers a list of objects with id, dateTime, userLogin, activityType, and description (members matched without
// regard to case); getEntryCount answers a number, bare or as {"value":N}; getAllActivityTypes answers a list of
// objects with id and name, or of bare texts.
const activityRows = `[{"id":7,"dateTime":"2026-10-24 10:00:00","userLogin":"alice","activityType":"site_added",` +
	`"description":"Added a site","apiToken":"secret-token-canary","ip":"192.0.2.1"},` +
	`{"id":"6","datetime":"2026-10-24 09:00:00","userlogin":"bob","type":"user_added","description":"` +
	`Added a user"},{"id":5,"description":"Third"}]`

func activityAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"ActivityLog.getEntries": func(c call) *http.Response {
			// Matomo answers the window from the offset on; the rows are 7, 6, and 5.
			if c.form.Get("offset") == "2" {
				return ok(`[{"id":5,"description":"Third"}]`)
			}
			return ok(activityRows)
		},
		"ActivityLog.getEntryCount":       fixed(`{"value":12}`),
		"ActivityLog.getAllActivityTypes": fixed(`[{"id":"site_added","name":"Site added"},"user_added"]`),
	}, extra))
}

var activityTools = []struct{ tool, arguments, method string }{
	{"matomo.activity-log.list", `{}`, "ActivityLog.getEntries"},
	{"matomo.activity-log.count", `{}`, "ActivityLog.getEntryCount"},
	{"matomo.activity-types.list", `{}`, "ActivityLog.getAllActivityTypes"},
}

func TestActivityLogToolsAreRegisteredAsInstanceReads(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, c := range activityTools {
		d, _, found := reg.Lookup(c.tool)
		if !found || d.Risk.DataSensitivity == "" || !d.Risk.OpenWorld || d.RequiresToolAllowList || !instanceTools[c.tool] ||
			strings.Contains(string(d.InputSchema), `"idSite"`) || strings.Contains(string(d.InputSchema), "filter_limit") {
			t.Errorf("%s: %+v", c.tool, d)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == c.tool {
					t.Errorf("profile %s selects %s", profile.ID, c.tool)
				}
			}
		}
	}
}

func TestActivityLogListPagesAndSendsFilters(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, activityAnswers(nil).handler(t))
	result, err := env.invoke("matomo.activity-log.list", "instance",
		`{"limit":2,"userLogin":"alice","activityType":"site_added","period":"day","date":"today"}`)
	if err != nil || !strings.Contains(result, `"has_more":true`) || !strings.Contains(result, "next_cursor") ||
		!strings.Contains(result, `"userLogin":"alice"`) || !strings.Contains(result, `"userLogin":"bob"`) ||
		strings.Contains(result, "secret-token-canary") || strings.Contains(result, "192.0.2.1") || strings.Contains(result, "Third") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	want := map[string]string{"filterByUserLogin": "alice", "filterByActivityType": "site_added", "period": "day",
		"date": "today", "offset": "0", "limit": "3", "format": "JSON"}
	for key, value := range want {
		if sent.form.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, sent.form.Get(key), value)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Has("token_auth") || sent.form.Has("callback") ||
		sent.form.Has("filter_limit") || strings.Contains(sent.body, tokenValue) {
		t.Errorf("request = %+v", sent)
	}
	if got := env.red.Apply("x " + tokenValue); strings.Contains(got, tokenValue) {
		t.Error("the token is not registered with the redactor")
	}
	cursor := result[strings.Index(result, `"next_cursor":"`)+len(`"next_cursor":"`):]
	cursor = cursor[:strings.Index(cursor, `"`)]
	same := `{"limit":2,"userLogin":"alice","activityType":"site_added","period":"day","date":"today","cursor":"` + cursor + `"}`
	result, err = env.invoke("matomo.activity-log.list", "instance", same)
	if err != nil || !strings.Contains(result, `"has_more":false`) {
		t.Errorf("continuation = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1].form; got.Get("offset") != "2" || got.Get("limit") != "3" {
		t.Errorf("continuation window = %v", got)
	}
	for name, arguments := range map[string]string{
		"other filter":  `{"limit":2,"userLogin":"bob","cursor":"` + cursor + `"}`,
		"other limit":   `{"limit":3,"userLogin":"alice","activityType":"site_added","period":"day","date":"today","cursor":"` + cursor + `"}`,
		"changed":       `{"cursor":"` + cursor[:len(cursor)-2] + `AA"}`,
		"foreign":       `{"cursor":"abcdefgh"}`,
		"limit too big": `{"limit":101}`,
		"limit -1":      `{"limit":-1}`,
		"period alone":  `{"period":"day"}`,
		"bad login":     `{"userLogin":"a\nb"}`,
		"bad date":      `{"period":"day","date":"x y"}`,
	} {
		before := len(calls)
		if _, err := env.invoke("matomo.activity-log.list", "instance", arguments); !isInvalidRequest(err) || len(calls) != before {
			t.Errorf("%s: err = %v, requests +%d", name, err, len(calls)-before)
		}
	}
}

func TestActivityLogCountAndTypes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, activityAnswers(nil).handler(t))
	result, err := env.invoke("matomo.activity-log.count", "instance", `{"userLogin":"alice","period":"month","date":"2026-10-01"}`)
	sent := calls[len(calls)-1]
	if err != nil || result != `{"count":12}` || sent.api() != "ActivityLog.getEntryCount" ||
		sent.form.Get("filterByUserLogin") != "alice" || sent.form.Get("period") != "month" || sent.auth != "Bearer "+tokenValue ||
		sent.form.Has("limit") || sent.form.Has("offset") {
		t.Errorf("count = %s, err = %v, request = %+v", result, err, sent)
	}
	result, err = env.invoke("matomo.activity-types.list", "instance", `{}`)
	sent = calls[len(calls)-1]
	if err != nil || !sameJSON(result, `{"has_more":false,"types":[{"id":"site_added","name":"Site added"},{"id":"user_added"}]}`) ||
		sent.form.Get("filterLimit") != "26" || sent.rawQuery != "" {
		t.Errorf("types = %s, err = %v, request = %+v", result, err, sent)
	}
	result, err = env.invoke("matomo.activity-types.list", "instance", `{"limit":1}`)
	if err != nil || !strings.Contains(result, `"has_more":true`) || calls[len(calls)-1].form.Get("filterLimit") != "2" {
		t.Errorf("paged types = %s, err = %v", result, err)
	}
	for _, c := range calls {
		for _, form := range []string{"limit", "filterLimit"} {
			if c.form.Get(form) == "-1" {
				t.Errorf("%s sends %s=-1", c.api(), form)
			}
		}
	}
	env = newEnvironment(t, &calls, activityAnswers(answers{"ActivityLog.getEntryCount": fixed(`"many"`)}).handler(t))
	if _, err := env.invoke("matomo.activity-log.count", "instance", `{}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("invalid count: %v", err)
	}
}

func TestActivityLogToolsNeedTheInstanceTarget(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, activityAnswers(nil).handler(t))
	for _, c := range activityTools {
		for _, connection := range []string{"open", "general", "one"} {
			if _, err := env.invoke(c.tool, connection, c.arguments); err == nil {
				t.Errorf("%s on %s was accepted", c.tool, connection)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestActivityLogToolsMapMatomoErrors(t *testing.T) {
	for _, c := range activityTools {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"not found": {func(call) (*http.Response, error) {
				return ok2(failure("The requested website does not exist." + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'ActivityLog'." + bodyCanary))
			}, "", true},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin ActivityLog is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return activityAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "instance", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
		}
	}
}

func TestActivityLogToolsAreUnsupportedWithoutThePlugin(t *testing.T) {
	for _, c := range activityTools {
		var calls []call
		env := newEnvironment(t, &calls, with(activityAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "instance", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}
