package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const eraseConnections = `
  pe-delete: {service: matomo, credential: token, tools: [matomo.data-subjects.delete], targets: [site/1, site/2]}
  pe-effects: {service: matomo, credential: token, effects: [read, update, delete, execute], targets: [site, instance]}
`

func newEraseEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(eraseConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func eraseAnswers(method string, handler func(call) *http.Response) answers {
	return with(pluginsOn(), answers{method: handler})
}

const deleteArgs = `{"visits":[{"idSite":1,"idVisit":11},{"idSite":2,"idVisit":12}]}`

// eraseCases are the tools with a connection and arguments that pass every check before a request.
var eraseCases = []struct{ id, method, connection, args string }{
	{"matomo.data-subjects.delete", "PrivacyManager.deleteDataSubjects", "pe-delete", deleteArgs},
}

func TestEraseToolsAreListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect      capability.Effect
		sensitivity string
	}{
		"matomo.data-subjects.delete": {capability.EffectDelete, dataSubjectsSensitive},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != want.sensitivity || !d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == "matomo.data-subjects.delete" {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestEraseToolsAreNotOfferedByEffectsAlone(t *testing.T) {
	for _, c := range eraseCases {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		}).handler(t))
		if _, err := env.change(c.id, "pe-effects", c.args); err == nil {
			t.Errorf("%s: an effects connection offered it", c.id)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d reads", c.id, len(calls), *env.reads)
		}
	}
}

func TestEraseToolsNeedConfirmationBeforeAnyRequest(t *testing.T) {
	for _, c := range eraseCases {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response { return ok(`{}`) }).handler(t))
		_, err := env.invoke(c.id, c.connection, c.args)
		if err == nil || !strings.Contains(err.Error(), "confirm") || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, %d requests, %d reads", c.id, err, len(calls), *env.reads)
		}
	}
}

func TestDataSubjectsDeleteSendsOneFixedRequestAndReturnsCounts(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.deleteDataSubjects", func(call) *http.Response {
		return ok(`{"log_visit":2,"log_link_visit_action":9,"log_conversion":0}`)
	}).handler(t))
	result, err := env.change("matomo.data-subjects.delete", "pe-delete", deleteArgs)
	want := `{"areas":[{"deleted_rows":0,"name":"log_conversion"},{"deleted_rows":9,"name":"log_link_visit_action"},` +
		`{"deleted_rows":2,"name":"log_visit"}],"deleted":true,"visits":[{"id_site":1,"id_visit":11},{"id_site":2,"id_visit":12}]}`
	if err != nil || result != want {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if countMethod(calls, "PrivacyManager.deleteDataSubjects") != 1 || sent.auth != "Bearer "+tokenValue ||
		sent.form.Has("token_auth") || sent.form.Has("passwordConfirmation") || strings.Contains(sent.rawQuery, tokenValue) ||
		sent.form.Get("visits[0][idsite]") != "1" || sent.form.Get("visits[0][idvisit]") != "11" ||
		sent.form.Get("visits[1][idsite]") != "2" || sent.form.Get("visits[1][idvisit]") != "12" || sent.form.Get("format") != "JSON" {
		t.Errorf("request = %+v", sent)
	}
	for name := range sent.form {
		if !strings.HasPrefix(name, "visits[") && name != "method" && name != "module" && name != "format" && name != "serialize" {
			t.Errorf("unexpected form field %s", name)
		}
	}
}

func TestDataSubjectsDeleteAcceptsAnEmptyCount(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.deleteDataSubjects", func(call) *http.Response {
		return ok(`[]`)
	}).handler(t))
	result, err := env.change("matomo.data-subjects.delete", "pe-delete", `{"visits":[{"idSite":1,"idVisit":11}]}`)
	if err != nil || strings.Contains(result, "areas") || !strings.Contains(result, `"deleted":true`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestDataSubjectsDeleteRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.deleteDataSubjects", func(call) *http.Response {
		return ok(`{}`)
	}).handler(t))
	for name, args := range map[string]string{
		"foreign website": `{"visits":[{"idSite":3,"idVisit":11}]}`,
		"mixed list":      `{"visits":[{"idSite":1,"idVisit":11},{"idSite":3,"idVisit":12}]}`,
		"empty":           `{"visits":[]}`,
		"no list":         `{}`,
		"zero visit":      `{"visits":[{"idSite":1,"idVisit":0}]}`,
		"missing visit":   `{"visits":[{"idSite":1}]}`,
		"repeat":          `{"visits":[{"idSite":1,"idVisit":11},{"idSite":1,"idVisit":11}]}`,
	} {
		_, err := env.change("matomo.data-subjects.delete", "pe-delete", args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "3") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d reads before a refusal", len(calls), *env.reads)
	}
	for targets, want := range map[string]bool{"site/1": true, "site": true, "instance": false} {
		if ok, _ := reaches("matomo.data-subjects.delete", strings.Split(targets, ",")); ok != want {
			t.Errorf("reaches(%s) = %t", targets, ok)
		}
	}
}

func TestEraseToolsMapApplicationErrorsWithoutTheMessage(t *testing.T) {
	for _, c := range eraseCases {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission":     {"You can't access this resource as it requires 'superuser' access.", provider.ClassPermission, false},
			"not found":      {"An unexpected website was found in the request.", provider.ClassNotFound, false},
			"method missing": {"The method 'PrivacyManager.x' does not exist or is not available in the module 'PrivacyManager'.", "", true},
			"plugin off":     {"The plugin PrivacyManager is not activated.", "", true},
		} {
			var calls []call
			env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response {
				return failure(tc.message + bodyCanary)
			}).handler(t))
			_, err := env.change(c.id, c.connection, c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.missing ||
				classOf(err) != tc.class || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v", c.id, name, err)
			}
		}
	}
}

func TestEraseToolsNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for _, c := range eraseCases {
		handlers := map[string]func(call) *http.Response{
			"server": func(call) *http.Response { return respond(500, bodyCanary) },
			"html":   func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
		}
		for name, handler := range handlers {
			var calls []call
			env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, handler).handler(t))
			_, err := env.change(c.id, c.connection, c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 ||
				!strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: err = %v, sent %d", c.id, name, err, countMethod(calls, c.method))
			}
		}
		var calls []call
		env := newEraseEnvironment(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return eraseAnswers(c.method, nil).handler(t)(x)
		})
		if _, err := env.change(c.id, c.connection, c.args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.id, err)
		}
		calls = nil
		env = newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response {
			return respond(429, bodyCanary)
		}).handler(t))
		if _, err := env.change(c.id, c.connection, c.args); classOf(err) != provider.ClassRateLimited ||
			strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
			t.Errorf("%s rate limit: err = %v, sent %d", c.id, err, countMethod(calls, c.method))
		}
	}
}

func TestDataSubjectsDeleteRejectsAnUnreadableCount(t *testing.T) {
	for name, body := range map[string]string{"text": `"ok"`, "word": `{"log_visit":"many"}`, "negative": `{"log_visit":-1}`} {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.deleteDataSubjects", func(call) *http.Response {
			return ok(body)
		}).handler(t))
		_, err := env.change("matomo.data-subjects.delete", "pe-delete", deleteArgs)
		if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "never repeated") ||
			countMethod(calls, "PrivacyManager.deleteDataSubjects") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
