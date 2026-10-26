package matomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const failureConnections = `
  tf-admin: {service: matomo, credential: token, tools: [matomo.tracking-failures.delete-all, matomo.tracking-failures.delete], targets: [site/1, instance]}
  tf-noinst: {service: matomo, credential: token, tools: [matomo.tracking-failures.delete-all], targets: [site/1]}
  tf-instonly: {service: matomo, credential: token, tools: [matomo.tracking-failures.delete-all], targets: [instance]}
  tf-delete: {service: matomo, credential: token, effects: [read, delete], targets: [site/1]}
`

func newFailureEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	serve(t, calls, a.handler(t))
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(failureConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func failureAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), extra)
}

// failureFixture follows CoreAdminHome.getTrackingFailures: ids as strings, rows of three websites and one
// website that does not exist, in no order.
const failureFixture = `[
{"idsite":"2","idfailure":"2","date_first_occurred":"2026-03-02 10:00:00","request_url":"idsite=2&rec=1&url=https%3A%2F%2Ftwo.example.test%2F",
 "site_name":"site2","pretty_date_first_occurred":"Mar 2, 2026 10:00","url":"https://two.example.test/","problem":"Auth problem",
 "solution":"Use a token","solution_url":"https://matomo.org/faq/how-to/faq_30835/"},
{"idsite":"1","idfailure":"2","date_first_occurred":"2026-03-01 10:00:00","request_url":"idsite=1&token_auth=__TOKEN_AUTH__",
 "site_name":"site1","pretty_date_first_occurred":"Mar 1, 2026 10:00","url":"https://one.example.test/","problem":"Auth problem","solution":"Use a token","solution_url":""},
{"idsite":"1","idfailure":"1","date_first_occurred":"2026-02-01 10:00:00","request_url":"idsite=1","site_name":"site1",
 "pretty_date_first_occurred":"Feb 1, 2026 10:00","url":"https://one.example.test/x","problem":"Invalid site","solution":"Check the ID","solution_url":""},
{"idsite":"9999","idfailure":"1","date_first_occurred":"2026-01-01 10:00:00","request_url":"idsite=9999","site_name":"Unknown",
 "pretty_date_first_occurred":"Jan 1, 2026 10:00","url":"https://gone.example.test/","problem":"","solution":"","solution_url":""}
]`

func failureIDs(t *testing.T, result string) []string {
	t.Helper()
	var out TrackingFailuresResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, f := range out.Failures {
		ids = append(ids, fmt.Sprintf("%d/%d", f.IDSite, f.IDFailure))
	}
	return ids
}

func TestTrackingFailureToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect    capability.Effect
		confirm   capability.Confirmation
		allowList bool
	}{
		"matomo.tracking-failures.list":       {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.tracking-failures.delete":     {capability.EffectDelete, capability.ConfirmationRequired, false},
		"matomo.tracking-failures.delete-all": {capability.EffectDelete, capability.ConfirmationRequired, true},
		"matomo.opt-out-code.get":             {capability.EffectRead, capability.ConfirmationNone, false},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Confirmation != want.confirm || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.RequiresToolAllowList != want.allowList {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
		if want.effect == capability.EffectDelete && d.Risk.Idempotency != capability.IdempotencyIdempotent {
			t.Errorf("%s: idempotency = %s", id, d.Risk.Idempotency)
		}
	}
	all, _, _ := reg.Lookup("matomo.tracking-failures.delete-all")
	for _, part := range []string{"beyond the websites", "Super User", "cannot be undone"} {
		if !strings.Contains(all.Description, part) {
			t.Errorf("delete-all description lacks %q", part)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		if strings.Contains(joined, "matomo.tracking-failures.delete") {
			t.Errorf("profile %s selects a delete tool", profile.ID)
		}
		if strings.Contains(joined, "matomo.opt-out-code.get") != true {
			t.Errorf("profile %s lacks the opt-out code", profile.ID)
		}
	}
}

func TestTrackingFailuresListFiltersRowsByTargetsAndPages(t *testing.T) {
	var calls []call
	env := newFailureEnvironment(t, &calls, failureAnswers(answers{
		"CoreAdminHome.getTrackingFailures": func(call) *http.Response { return ok(failureFixture) },
	}))
	result, err := env.invoke("matomo.tracking-failures.list", "open", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := failureIDs(t, result); !slices.Equal(got, []string{"1/1", "1/2", "2/2", "9999/1"}) {
		t.Fatalf("open: ids = %v", got)
	}
	var out TrackingFailuresResult
	_ = json.Unmarshal([]byte(result), &out)
	first := out.Failures[0]
	if first.SiteName != "site1" || first.DateFirstOccurred != "2026-02-01 10:00:00" || first.RequestURL != "idsite=1" ||
		first.URL != "https://one.example.test/x" || first.Problem != "Invalid site" || first.Solution != "Check the ID" ||
		first.PrettyDateFirstOccurred != "Feb 1, 2026 10:00" || out.Failures[2].SolutionURL == "" {
		t.Errorf("first = %+v", first)
	}
	for connection, want := range map[string][]string{"one": {"2/2"}, "two": {"1/1", "1/2", "2/2"}} {
		result, err := env.invoke("matomo.tracking-failures.list", connection, `{}`)
		if err != nil || !slices.Equal(failureIDs(t, result), want) {
			t.Errorf("%s: ids = %v, err = %v, want %v", connection, failureIDs(t, result), err, want)
		}
	}
	// A limit pages over the rows inside the targets, not over the rows Matomo sent.
	result, err = env.invoke("matomo.tracking-failures.list", "two", `{"limit":2}`)
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Failures) != 2 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("page one: %s, err = %v", result, err)
	}
	next := out.NextCursor
	result, err = env.invoke("matomo.tracking-failures.list", "two", `{"cursor":"`+next+`"}`)
	if err != nil || !slices.Equal(failureIDs(t, result), []string{"2/2"}) {
		t.Fatalf("page two: %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.tracking-failures.list", "one", `{"cursor":"`+next+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of another connection: err = %v", err)
	}
	result, err = env.invoke("matomo.tracking-failures.list", "open", `{"idSite":2}`)
	if err != nil || !slices.Equal(failureIDs(t, result), []string{"2/2"}) {
		t.Errorf("idSite filter: %s, err = %v", result, err)
	}
}

func TestTrackingFailuresListRefusesForeignWebsiteAndInstanceOnlyBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newFailureEnvironment(t, &calls, failureAnswers(answers{}))
	for _, c := range [][2]string{{"one", `{"idSite":1}`}, {"two", `{"idSite":3}`}, {"instance", `{}`}} {
		_, err := env.invoke("matomo.tracking-failures.list", c[0], c[1])
		if err == nil || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s %s: err = %v", c[0], c[1], err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestTrackingFailuresListMapsFailuresWithoutRetry(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) *http.Response
		class       provider.Class
		unsupported bool
	}{
		"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission, false},
		"not found":  {func(call) *http.Response { return failure("Website not found " + bodyCanary) }, provider.ClassNotFound, false},
		"method missing": {func(call) *http.Response {
			return failure("The method 'CoreAdminHome.getTrackingFailures' does not exist or is not available in the module 'CoreAdminHome'.")
		}, "", true},
		"plugin inactive": {func(call) *http.Response { return failure("The plugin CoreAdminHome is not activated.") }, "", true},
		"429":             {func(call) *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited, false},
		"html":            {fixed("<html>" + bodyCanary + "</html>"), provider.ClassInvalidResponse, false},
	} {
		var calls []call
		env := newFailureEnvironment(t, &calls, failureAnswers(answers{"CoreAdminHome.getTrackingFailures": tc.answer}))
		_, err := env.invoke("matomo.tracking-failures.list", "open", `{}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
			classOf(err) != tc.class || countMethod(calls, "CoreAdminHome.getTrackingFailures") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
		for _, got := range calls {
			if got.auth != "Bearer "+tokenValue || got.rawQuery != "" || got.form.Has("token_auth") ||
				got.form.Get("format") != "JSON" {
				t.Errorf("%s: request = %+v", name, got)
			}
		}
	}
}

func TestTrackingFailuresDelete(t *testing.T) {
	var calls []call
	env := newFailureEnvironment(t, &calls, failureAnswers(answers{
		"CoreAdminHome.deleteTrackingFailure": func(c call) *http.Response {
			if c.form.Get("idFailure") == "7" {
				return failure("The current user is not allowed to modify" + bodyCanary)
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
	}))
	result, err := env.change("matomo.tracking-failures.delete", "tf-admin", `{"idSite":1,"idFailure":2}`)
	var out TrackingFailureDeleted
	_ = json.Unmarshal([]byte(result), &out)
	sent := calls[len(calls)-1]
	if err != nil || out != (TrackingFailureDeleted{IDSite: 1, IDFailure: 2, Deleted: true}) ||
		sent.form.Get("idSite") != "1" || sent.form.Get("idFailure") != "2" || sent.form.Has("passwordConfirmation") ||
		sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || countMethod(calls, "CoreAdminHome.deleteTrackingFailure") != 1 {
		t.Fatalf("result = %s, err = %v, request = %+v", result, err, sent)
	}
	// The only bound website is the default.
	if _, err := env.change("matomo.tracking-failures.delete", "tf-delete", `{"idFailure":1}`); err != nil {
		t.Errorf("default website: err = %v", err)
	}
	if _, err := env.change("matomo.tracking-failures.delete", "tf-admin", `{"idSite":1,"idFailure":7}`); classOf(err) != provider.ClassPermission ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("permission: err = %v", err)
	}
	before, readsBefore := len(calls), *env.reads
	for _, arguments := range []string{`{"idSite":2,"idFailure":1}`, `{"idSite":1}`, `{"idSite":1,"idFailure":0}`,
		`{"idSite":1,"idFailure":"1"}`, `{"idSite":1,"idFailure":1,"passwordConfirmation":"x"}`} {
		if _, err := env.change("matomo.tracking-failures.delete", "tf-admin", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if _, err := env.invoke("matomo.tracking-failures.delete", "tf-admin", `{"idSite":1,"idFailure":1}`); err == nil {
		t.Error("a delete without confirm ran")
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-readsBefore)
	}
}

func TestTrackingFailuresDeleteIsSentOnceAndMapsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) (*http.Response, error)
		class       provider.Class
		unsupported bool
		unclear     bool
	}{
		"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
		"502":             {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", false, true},
		"unreadable":      {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, false, true},
		"no confirmation": {func(call) (*http.Response, error) { return ok(`[]`), nil }, provider.ClassInvalidResponse, false, true},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
		"not found": {func(call) (*http.Response, error) {
			return ok2(failure("An unexpected website was found " + bodyCanary))
		}, provider.ClassNotFound, false, false},
		"method missing": {func(call) (*http.Response, error) {
			return ok2(failure("The method 'X.y' does not exist or is not available in the module 'X'."))
		}, "", true, false},
		"plugin inactive": {func(call) (*http.Response, error) { return ok2(failure("The plugin CoreAdminHome is not activated.")) }, "", true, false},
		"429":             {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false, false},
	} {
		for _, tool := range []struct{ id, connection, arguments, method string }{
			{"matomo.tracking-failures.delete", "tf-admin", `{"idSite":1,"idFailure":1}`, "CoreAdminHome.deleteTrackingFailure"},
			{"matomo.tracking-failures.delete-all", "tf-admin", `{}`, "CoreAdminHome.deleteAllTrackingFailures"},
		} {
			var calls []call
			serve(t, &calls, func(c call) (*http.Response, error) {
				if c.api() != tool.method {
					return versionOnly("5.14.2").handler(t)(c)
				}
				return tc.answer(c)
			})
			reads := 0
			red := &redact.Redactor{}
			reg := registry(t)
			document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(failureConnections, "\n"), 1)
			cfg, err := config.Decode(strings.NewReader(document), reg)
			if err != nil {
				t.Fatal(err)
			}
			env := &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
			_, err = env.change(tool.id, tool.connection, tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
				tc.class != "" && classOf(err) != tc.class ||
				countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", name, tool.id, err, countMethod(calls, tool.method))
				continue
			}
			if tc.unclear && !strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: err = %v does not say that the outcome is unclear", name, tool.id, err)
			}
		}
	}
}

func TestTrackingFailuresDeleteAllNeedsToolsListAndInstance(t *testing.T) {
	var calls []call
	env := newFailureEnvironment(t, &calls, failureAnswers(answers{
		"CoreAdminHome.deleteAllTrackingFailures": func(c call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		},
	}))
	for _, connection := range []string{"tf-admin", "tf-instonly"} {
		result, err := env.change("matomo.tracking-failures.delete-all", connection, `{}`)
		if err != nil || result != `{"deleted":true}` {
			t.Errorf("%s: result = %s, err = %v", connection, result, err)
		}
	}
	if sent := calls[len(calls)-1]; sent.form.Has("idSite") || sent.form.Has("passwordConfirmation") ||
		sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" {
		t.Errorf("request = %+v", sent)
	}
	before, readsBefore := len(calls), *env.reads
	for _, connection := range []string{"tf-noinst", "tf-delete", "open", "instance", "mixed"} {
		if _, err := env.change("matomo.tracking-failures.delete-all", connection, `{}`); err == nil {
			t.Errorf("delete-all ran on %s", connection)
		}
	}
	if _, err := env.change("matomo.tracking-failures.delete-all", "tf-admin", `{"idSite":1}`); err == nil {
		t.Error("delete-all accepted an idSite")
	}
	if _, err := env.invoke("matomo.tracking-failures.delete-all", "tf-admin", `{}`); err == nil {
		t.Error("delete-all ran without confirm")
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-readsBefore)
	}
	for targets, want := range map[string]bool{"site/1": false, "site": false, "instance": true, "site/1,instance": true} {
		if got, _ := reaches("matomo.tracking-failures.delete-all", strings.Split(targets, ",")); got != want {
			t.Errorf("reaches with %s = %t, want %t", targets, got, want)
		}
	}
}

func ok2(response *http.Response) (*http.Response, error) { return response, nil }

var errTimeout error = context.DeadlineExceeded
