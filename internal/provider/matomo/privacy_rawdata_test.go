package matomo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// confirmedMethods are the two methods that carry the password confirmation.
var confirmedMethods = []struct{ id, method, connection, noPassword, args string }{
	{"matomo.raw-data.anonymize", "PrivacyManager.anonymizeSomeRawData", "pe-anon", "pe-anon-nopw", `{"idSites":[1],"date":"2026-01-01","anonymizeIp":true}`},
	{"matomo.data-purge.run", "PrivacyManager.executeDataPurge", "pe-purge", "pe-purge-nopw", `{}`},
}

func successAnswer(string) func(call) *http.Response {
	return func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) }
}

func TestPasswordConfirmationTravelsOnlyInThePostBody(t *testing.T) {
	for _, c := range confirmedMethods {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, successAnswer(c.method)).handler(t))
		result, err := env.change(c.id, c.connection, c.args)
		if err != nil || strings.Contains(result, confirmValue) {
			t.Fatalf("%s: result = %s, err = %v", c.id, result, err)
		}
		sent := calls[len(calls)-1]
		if countMethod(calls, c.method) != 1 || sent.form.Get("passwordConfirmation") != confirmValue ||
			sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") ||
			strings.Contains(sent.rawQuery, confirmValue) || strings.Contains(sent.rawQuery, tokenValue) {
			t.Errorf("%s: request = %+v", c.id, sent)
		}
		for _, other := range calls {
			if other.api() != c.method && (other.form.Has("passwordConfirmation") || strings.Contains(other.rawQuery, confirmValue)) {
				t.Errorf("%s: %s carried the password", c.id, other.api())
			}
		}
		if got := env.red.Apply("seen " + confirmValue); strings.Contains(got, confirmValue) {
			t.Errorf("%s: the redactor does not know the password", c.id)
		}
	}
}

func TestPasswordConfirmationNeverReachesAnErrorOrAnotherMethod(t *testing.T) {
	for _, c := range confirmedMethods {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response {
			return failure("Wrong password " + confirmValue)
		}).handler(t))
		_, err := env.change(c.id, c.connection, c.args)
		if err == nil || strings.Contains(err.Error(), confirmValue) || countMethod(calls, c.method) != 1 {
			t.Errorf("%s: err = %v", c.id, err)
		}
	}
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.deleteDataSubjects", func(call) *http.Response {
		return ok(`{"log_visit":2}`)
	}).handler(t))
	if _, err := env.change("matomo.data-subjects.delete", "pe-delete-pw", deleteArgs); err != nil {
		t.Fatal(err)
	}
	for _, sent := range calls {
		if sent.form.Has("passwordConfirmation") || strings.Contains(sent.rawQuery, confirmValue) {
			t.Errorf("%s carried the password", sent.api())
		}
	}
	if *env.reads != 1 {
		t.Errorf("delete read %d variables, want the token only", *env.reads)
	}
}

func TestMissingPasswordRoleRefusesBeforeAnyRequest(t *testing.T) {
	for _, c := range confirmedMethods {
		var calls []call
		env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, successAnswer(c.method)).handler(t))
		_, err := env.change(c.id, c.noPassword, c.args)
		if err == nil || !strings.Contains(err.Error(), rolePasswordConfirmation) || len(calls) != 0 {
			t.Errorf("%s: err = %v, %d requests", c.id, err, len(calls))
		}
	}
}

func TestLockedStoreForThePasswordRoleIsPassedOn(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.executeDataPurge", successAnswer("")).handler(t))
	_, err := env.change("matomo.data-purge.run", "pe-purge-locked", `{}`)
	if err == nil || strings.Contains(err.Error(), "is not set") || len(calls) != 0 {
		t.Errorf("err = %v, %d requests", err, len(calls))
	}
}

func TestRawDataAnonymizeSendsOneFixedRequestWithExplicitSites(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.anonymizeSomeRawData", successAnswer("")).handler(t))
	result, err := env.change("matomo.raw-data.anonymize", "pe-anon", anonymizeArgs)
	if err != nil || result != `{"date":"2026-01-01,2026-01-31","idSites":[1,2],"scheduled":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("idSites") != "1,2" || sent.form.Get("date") != "2026-01-01,2026-01-31" ||
		sent.form.Get("anonymizeIp") != "1" || sent.form.Get("anonymizeLocation") != "0" ||
		sent.form.Get("anonymizeUserId") != "0" || strings.Join(sent.form["unsetVisitColumns[]"], ",") != "location_city" ||
		sent.form.Has("unsetLinkVisitActionColumns[]") {
		t.Errorf("request = %+v", sent)
	}
	for name := range sent.form {
		switch name {
		case "method", "module", "format", "serialize", "idSites", "date", "anonymizeIp", "anonymizeLocation",
			"anonymizeUserId", "unsetVisitColumns[]", "passwordConfirmation":
		default:
			t.Errorf("unexpected form field %s", name)
		}
	}
}

func TestRawDataAnonymizeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.anonymizeSomeRawData", successAnswer("")).handler(t))
	for name, args := range map[string]string{
		"foreign website":  `{"idSites":[3],"date":"2026-01-01","anonymizeIp":true}`,
		"mixed list":       `{"idSites":[1,3],"date":"2026-01-01","anonymizeIp":true}`,
		"all":              `{"idSites":"all","date":"2026-01-01","anonymizeIp":true}`,
		"no sites":         `{"date":"2026-01-01","anonymizeIp":true}`,
		"empty sites":      `{"idSites":[],"date":"2026-01-01","anonymizeIp":true}`,
		"repeat":           `{"idSites":[1,1],"date":"2026-01-01","anonymizeIp":true}`,
		"no date":          `{"idSites":[1],"anonymizeIp":true}`,
		"relative date":    `{"idSites":[1],"date":"yesterday","anonymizeIp":true}`,
		"impossible date":  `{"idSites":[1],"date":"2026-02-30","anonymizeIp":true}`,
		"reversed range":   `{"idSites":[1],"date":"2026-02-01,2026-01-01","anonymizeIp":true}`,
		"nothing chosen":   `{"idSites":[1],"date":"2026-01-01","anonymizeIp":false}`,
		"bad column":       `{"idSites":[1],"date":"2026-01-01","unsetVisitColumns":["a b"]}`,
		"too many columns": `{"idSites":[1],"date":"2026-01-01","unsetVisitColumns":["` + strings.Repeat(`a","`, 50) + `a"]}`,
	} {
		_, err := env.change("matomo.raw-data.anonymize", "pe-anon", args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	valid := `{"idSites":[1],"date":"2026-01-01","anonymizeIp":true}`
	for _, connection := range []string{"pe-anon-noinst", "pe-anon-instonly"} {
		if _, err := env.change("matomo.raw-data.anonymize", connection, valid); err == nil {
			t.Errorf("%s ran without the instance and a website", connection)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d reads before a refusal", len(calls), *env.reads)
	}
	for targets, want := range map[string]bool{"site/1": false, "instance": false, "site,instance": true, "site/1,instance": true} {
		if ok, _ := reaches("matomo.raw-data.anonymize", strings.Split(targets, ",")); ok != want {
			t.Errorf("reaches(%s) = %t", targets, ok)
		}
	}
}

func TestDataPurgeRunSendsOneRequestWithoutOtherParameters(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.executeDataPurge", successAnswer("")).handler(t))
	result, err := env.change("matomo.data-purge.run", "pe-purge", `{}`)
	if err != nil || result != `{"executed":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	for name := range sent.form {
		switch name {
		case "method", "module", "format", "serialize", "passwordConfirmation":
		default:
			t.Errorf("unexpected form field %s", name)
		}
	}
	before, reads := len(calls), *env.reads
	for name, c := range map[string]struct{ connection, args string }{
		"no instance": {"pe-purge-noinst", `{}`}, "argument": {"pe-purge", `{"idSite":1}`},
	} {
		if _, err := env.change("matomo.data-purge.run", c.connection, c.args); err == nil {
			t.Errorf("%s: purge ran", name)
		}
	}
	if len(calls) != before || *env.reads != reads {
		t.Errorf("%d requests and %d reads before a refusal", len(calls)-before, *env.reads-reads)
	}
	for targets, want := range map[string]bool{"site/1": false, "site": false, "instance": true, "site,instance": true} {
		if ok, _ := reaches("matomo.data-purge.run", strings.Split(targets, ",")); ok != want {
			t.Errorf("reaches(%s) = %t", targets, ok)
		}
	}
}

func TestConfirmedMethodsClassifyRoleErrors(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.executeDataPurge", func(call) *http.Response {
		return failure("The password confirmation is wrong" + bodyCanary)
	}).handler(t))
	_, err := env.change("matomo.data-purge.run", "pe-purge", `{}`)
	if classOf(err) == "" || classOf(err) == provider.ClassOK || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}
