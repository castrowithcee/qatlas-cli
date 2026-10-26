package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	anonSetMethod = "PrivacyManager.setAnonymizeIpSettings"
	anonSetArgs   = `{"anonymizeIPEnable":true,"ipAddressMaskLength":2,"useAnonymizedIpForVisitEnrichment":false,` +
		`"anonymizeUserId":false,"anonymizeOrderId":false,"anonymizeReferrer":"","forceCookielessTracking":false,` +
		`"randomizeConfigId":false}`
	randomizeArgs = `{"anonymizeIPEnable":true,"ipAddressMaskLength":3,"useAnonymizedIpForVisitEnrichment":true,` +
		`"anonymizeUserId":true,"anonymizeOrderId":true,"anonymizeReferrer":"exclude_query",` +
		`"forceCookielessTracking":true,"randomizeConfigId":true}`
)

var dntCases = []struct{ id, method, want string }{
	{"matomo.do-not-track.enable", "PrivacyManager.activateDoNotTrack", `{"doNotTrack":true,"updated":true}`},
	{"matomo.do-not-track.disable", "PrivacyManager.deactivateDoNotTrack", `{"doNotTrack":false,"updated":true}`},
}

func setterEnv(t *testing.T, calls *[]call, method string, handler func(call) *http.Response) *environment {
	return newEraseEnvironment(t, calls, eraseAnswers(method, handler).handler(t))
}

func TestAnonymizeIPSettingSendsEverythingAndNoPasswordByDefault(t *testing.T) {
	var calls []call
	env := setterEnv(t, &calls, anonSetMethod, successAnswer(anonSetMethod))
	result, err := env.change("matomo.privacy-settings.set-anonymize-ip", "pp-set", anonSetArgs)
	want := `{"anonymizeIPEnable":true,"anonymizeOrderId":false,"anonymizeReferrer":"","anonymizeUserId":false,` +
		`"forceCookielessTracking":false,"ipAddressMaskLength":2,"randomizeConfigId":false,"updated":true,` +
		`"useAnonymizedIpForVisitEnrichment":false}`
	if err != nil || result != want {
		t.Fatalf("%s, %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.api() != anonSetMethod || countMethod(calls, anonSetMethod) != 1 || sent.auth != "Bearer "+tokenValue ||
		sent.form.Has("token_auth") || sent.form.Has("idSiteSpecific") || sent.form.Get("anonymizeIPEnable") != "1" ||
		sent.form.Get("ipAddressMaskLength") != "2" || sent.form.Get("useAnonymizedIpForVisitEnrichment") != "0" ||
		sent.form.Get("randomizeConfigId") != "0" || sent.form.Get("anonymizeReferrer") != "" {
		t.Errorf("request = %v auth=%q", sent.form, sent.auth)
	}
	for _, other := range calls {
		if other.form.Has("passwordConfirmation") || strings.Contains(other.rawQuery, confirmValue) ||
			strings.Contains(other.rawQuery, tokenValue) || other.form.Has("callback") || other.form.Has("jsoncallback") {
			t.Errorf("%s carried a secret or callback", other.api())
		}
	}
	if *env.reads != 1 {
		t.Errorf("read %d variables, want the token only", *env.reads)
	}
}

func TestAnonymizeIPSettingRandomizeSendsThePasswordOnlyInThePostBody(t *testing.T) {
	var calls []call
	env := setterEnv(t, &calls, anonSetMethod, successAnswer(anonSetMethod))
	result, err := env.change("matomo.privacy-settings.set-anonymize-ip", "pp-set", randomizeArgs)
	if err != nil || strings.Contains(result, confirmValue) || !strings.Contains(result, `"randomizeConfigId":true`) {
		t.Fatalf("%s, %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("passwordConfirmation") != confirmValue || sent.form.Get("randomizeConfigId") != "1" ||
		sent.form.Get("anonymizeReferrer") != "exclude_query" || sent.form.Get("forceCookielessTracking") != "1" ||
		strings.Contains(sent.rawQuery, confirmValue) || strings.Contains(sent.rawQuery, tokenValue) ||
		sent.auth != "Bearer "+tokenValue || countMethod(calls, anonSetMethod) != 1 {
		t.Errorf("request = %v query=%q", sent.form, sent.rawQuery)
	}
	for _, other := range calls[:len(calls)-1] {
		if other.form.Has("passwordConfirmation") {
			t.Errorf("%s carried the password", other.api())
		}
	}
	if got := env.red.Apply("seen " + confirmValue); strings.Contains(got, confirmValue) {
		t.Error("the redactor does not know the password")
	}
	// A refusal of Matomo never leaks the password into the error.
	calls = nil
	env = setterEnv(t, &calls, anonSetMethod, func(call) *http.Response { return failure("Wrong " + confirmValue) })
	_, err = env.change("matomo.privacy-settings.set-anonymize-ip", "pp-set", randomizeArgs)
	if err == nil || strings.Contains(err.Error(), confirmValue) || countMethod(calls, anonSetMethod) != 1 {
		t.Errorf("err = %v", err)
	}
}

func TestAnonymizeIPSettingRandomizeWithoutRoleSendsNothing(t *testing.T) {
	var calls []call
	env := setterEnv(t, &calls, anonSetMethod, successAnswer(anonSetMethod))
	_, err := env.change("matomo.privacy-settings.set-anonymize-ip", "pp-set-nopw", randomizeArgs)
	if err == nil || !strings.Contains(err.Error(), rolePasswordConfirmation) || len(calls) != 0 {
		t.Errorf("err = %v, %d requests", err, len(calls))
	}
	// Without randomizeConfigId the same connection works and sends no password.
	if _, err := env.change("matomo.privacy-settings.set-anonymize-ip", "pp-set-nopw", anonSetArgs); err != nil {
		t.Errorf("err = %v", err)
	}
	if countMethod(calls, anonSetMethod) != 1 || calls[len(calls)-1].form.Has("passwordConfirmation") {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestDoNotTrackToolsSendOneBareRequestWithoutPassword(t *testing.T) {
	for _, c := range dntCases {
		var calls []call
		env := setterEnv(t, &calls, c.method, successAnswer(c.method))
		result, err := env.change(c.id, "pp-set", `{}`)
		if err != nil || result != c.want {
			t.Fatalf("%s: %s, %v", c.id, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.api() != c.method || countMethod(calls, c.method) != 1 || sent.auth != "Bearer "+tokenValue ||
			sent.form.Has("token_auth") || sent.form.Has("passwordConfirmation") || len(sent.form) > 3 {
			t.Errorf("%s: request = %v", c.id, sent.form)
		}
		// Matomo's plain true answer is read too.
		env = setterEnv(t, &calls, c.method, func(call) *http.Response { return ok(`{"value":true}`) })
		if _, err := env.change(c.id, "pp-set", `{}`); err != nil {
			t.Errorf("%s: %v", c.id, err)
		}
		if *env.reads != 1 {
			t.Errorf("%s: read %d variables", c.id, *env.reads)
		}
	}
}

func TestPrivacySettersRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := setterEnv(t, &calls, anonSetMethod, successAnswer(anonSetMethod))
	ids := []string{"matomo.privacy-settings.set-anonymize-ip", dntCases[0].id, dntCases[1].id}
	valid := map[string]string{ids[0]: anonSetArgs, ids[1]: `{}`, ids[2]: `{}`}
	for _, id := range ids {
		if _, err := env.change(id, "pp-set-noinst", valid[id]); err == nil {
			t.Errorf("%s: accepted without the instance target", id)
		}
		if _, err := env.invoke(id, "pp-set", valid[id]); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s: err = %v, want a confirmation request", id, err)
		}
		if ok, _ := reaches(id, []string{"site"}); ok {
			t.Errorf("%s reaches a connection without instance", id)
		}
		if ok, _ := reaches(id, []string{"instance"}); !ok {
			t.Errorf("%s does not reach an instance connection", id)
		}
	}
	bad := []string{`{}`, `{"anonymizeIPEnable":true,"ipAddressMaskLength":2}`}
	for _, name := range []string{"anonymizeIPEnable", "ipAddressMaskLength", "useAnonymizedIpForVisitEnrichment",
		"anonymizeUserId", "anonymizeOrderId", "anonymizeReferrer", "forceCookielessTracking", "randomizeConfigId"} {
		var full map[string]any
		_ = json.Unmarshal([]byte(anonSetArgs), &full)
		delete(full, name)
		raw, _ := json.Marshal(full)
		bad = append(bad, string(raw))
	}
	bad = append(bad,
		`{"anonymizeIPEnable":true,"ipAddressMaskLength":0,"useAnonymizedIpForVisitEnrichment":false}`,
		`{"anonymizeIPEnable":true,"ipAddressMaskLength":5,"useAnonymizedIpForVisitEnrichment":false}`,
		`{"anonymizeIPEnable":true,"ipAddressMaskLength":"2","useAnonymizedIpForVisitEnrichment":false}`,
		`{"anonymizeIPEnable":true,"ipAddressMaskLength":2,"useAnonymizedIpForVisitEnrichment":false,`+
			`"anonymizeUserId":false,"anonymizeOrderId":false,"anonymizeReferrer":"other","forceCookielessTracking":false,`+
			`"randomizeConfigId":false}`,
		strings.TrimSuffix(anonSetArgs, "}")+`,"idSiteSpecific":1}`,
		strings.TrimSuffix(anonSetArgs, "}")+`,"passwordConfirmation":"x"}`,
		`{"anonymizeIPEnable":"yes","ipAddressMaskLength":2,"useAnonymizedIpForVisitEnrichment":false}`)
	for _, args := range bad {
		if _, err := env.change(ids[0], "pp-set", args); !isInvalidRequest(err) {
			t.Errorf("%.80s: err = %v", args, err)
		}
	}
	for _, id := range ids[1:] {
		if _, err := env.change(id, "pp-set", `{"x":1}`); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls), *env.reads)
	}
}

func TestPrivacySettersMapErrorsAndNeverRetry(t *testing.T) {
	cases := []struct{ id, method, args string }{
		{"matomo.privacy-settings.set-anonymize-ip", anonSetMethod, randomizeArgs},
		{dntCases[0].id, dntCases[0].method, `{}`},
		{dntCases[1].id, dntCases[1].method, `{}`},
	}
	for _, c := range cases {
		var calls []call
		env := setterEnv(t, &calls, c.method, func(call) *http.Response {
			return failure("You can't access this resource as it requires 'superuser' access." + bodyCanary)
		})
		if _, err := env.change(c.id, "pp-set", c.args); classOf(err) != provider.ClassPermission ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s permission: err = %v", c.id, err)
		}
		for name, handler := range map[string]func(call) *http.Response{
			"html":     func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
			"server":   func(call) *http.Response { return respond(500, bodyCanary) },
			"rate":     func(call) *http.Response { return respond(429, bodyCanary) },
			"no ack":   func(call) *http.Response { return ok(`{"value":false}`) },
			"error200": func(call) *http.Response { return ok(`{"result":"error","message":"` + bodyCanary + `"}`) },
		} {
			var calls []call
			env := setterEnv(t, &calls, c.method, handler)
			_, err := env.change(c.id, "pp-set", c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", c.id, name, err, countMethod(calls, c.method))
			}
			if name == "rate" && classOf(err) != provider.ClassRateLimited {
				t.Errorf("%s rate: err = %v", c.id, err)
			}
			if name != "rate" && name != "error200" && (err == nil || !strings.Contains(err.Error(), "never repeated")) {
				t.Errorf("%s %s: no uncertainty reported: %v", c.id, name, err)
			}
		}
		var timed []call
		env = newEraseEnvironment(t, &timed, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return eraseAnswers(c.method, successAnswer(c.method)).handler(t)(x)
		})
		if _, err := env.change(c.id, "pp-set", c.args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(timed, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.id, err)
		}
	}
}

func TestPrivacySettersAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, op := range privacySetOperations() {
		d, _, found := reg.Lookup(op.Descriptor.ID)
		if !found || d.Risk.Effect != "update" || d.Risk.Idempotency != "idempotent" ||
			d.Risk.Confirmation != "required" || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList {
			t.Fatalf("%s: found %t, %+v", op.Descriptor.ID, found, d)
		}
		for _, profile := range metadata.Profiles {
			if strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+op.Descriptor.ID+" ") {
				t.Errorf("profile %s holds %s", profile.ID, op.Descriptor.ID)
			}
		}
	}
}
