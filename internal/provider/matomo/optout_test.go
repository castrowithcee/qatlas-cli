package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const optOutScript = `<div id="matomo-opt-out"></div>` + "\n" +
	`<script src="https://analytics.example.test/matomo/index.php?module=CoreAdminHome&action=optOutJS&divId=matomo-opt-out&language=auto&showIntro=1"></script>`

func optOutAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), extra)
}

func TestOptOutCodeGetJSSendsTheShapeOnce(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, optOutAnswers(answers{
		"CoreAdminHome.getOptOutJSEmbedCode": func(call) *http.Response {
			encoded, _ := json.Marshal(optOutScript)
			return ok(`{"value":` + string(encoded) + `}`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.opt-out-code.get", "open", `{"kind":"js"}`)
	var out OptOutCodeResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.Kind != "js" || out.Code != optOutScript || out.Truncated {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := lastCall(calls, "CoreAdminHome.getOptOutJSEmbedCode")
	want := map[string]string{"matomoUrl": "https://analytics.example.test/matomo", "language": "auto", "applyStyling": "0",
		"showIntro": "1", "backgroundColor": "", "fontColor": "", "fontSize": "", "fontFamily": "", "format": "JSON"}
	for name, value := range want {
		if !sent.form.Has(name) || sent.form.Get(name) != value {
			t.Errorf("%s = %q (present %t), want %q", name, sent.form.Get(name), sent.form.Has(name), value)
		}
	}
	if sent.form.Has("idSite") || sent.form.Has("passwordConfirmation") || sent.form.Has("token_auth") ||
		sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || countMethod(calls, "CoreAdminHome.getOptOutJSEmbedCode") != 1 {
		t.Errorf("request = %+v", sent)
	}
	if _, err := env.invoke("matomo.opt-out-code.get", "two",
		`{"kind":"js","matomoUrl":"https://m.example.test","language":"de-AT","applyStyling":true,"showIntro":false,`+
			`"backgroundColor":"fff","fontColor":"333333","fontSize":"1.2em","fontFamily":"Open Sans, serif"}`); err != nil {
		t.Fatal(err)
	}
	sent = lastCall(calls, "CoreAdminHome.getOptOutJSEmbedCode")
	if sent.form.Get("matomoUrl") != "https://m.example.test" || sent.form.Get("language") != "de-AT" ||
		sent.form.Get("applyStyling") != "1" || sent.form.Get("showIntro") != "0" || sent.form.Get("backgroundColor") != "fff" ||
		sent.form.Get("fontColor") != "333333" || sent.form.Get("fontSize") != "1.2em" ||
		sent.form.Get("fontFamily") != "Open Sans, serif" {
		t.Errorf("form = %v", sent.form)
	}
}

func TestOptOutCodeGetSelfContainedAndTruncation(t *testing.T) {
	var calls []call
	long := "<div>" + strings.Repeat("é", maxOptOutCodeLength) + "</div>"
	env := newEnvironment(t, &calls, optOutAnswers(answers{
		"CoreAdminHome.getOptOutSelfContainedEmbedCode": func(call) *http.Response {
			encoded, _ := json.Marshal(long)
			return ok(string(encoded))
		},
	}).handler(t))
	result, err := env.invoke("matomo.opt-out-code.get", "open",
		`{"kind":"self-contained","cookiePath":"/shop","cookieDomain":".example.test","cookieSameSite":"Strict"}`)
	var out OptOutCodeResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Truncated || len(out.Code) > maxOptOutCodeLength || !strings.HasPrefix(out.Code, "<div>") {
		t.Fatalf("truncated = %t, length = %d, err = %v", out.Truncated, len(out.Code), err)
	}
	sent := lastCall(calls, "CoreAdminHome.getOptOutSelfContainedEmbedCode")
	if sent.form.Get("cookiePath") != "/shop" || sent.form.Get("cookieDomain") != ".example.test" ||
		sent.form.Get("cookieSameSite") != "Strict" || sent.form.Has("matomoUrl") || sent.form.Has("language") ||
		sent.form.Get("applyStyling") != "0" || sent.form.Get("showIntro") != "1" {
		t.Errorf("form = %v", sent.form)
	}
}

func TestOptOutCodeGetRefusesBadArgumentsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, optOutAnswers(answers{}).handler(t))
	for _, arguments := range []string{
		`{}`, `{"kind":"iframe"}`,
		`{"kind":"js","fontColor":"333333"}`,
		`{"kind":"js","applyStyling":true,"fontColor":"#333333"}`,
		`{"kind":"js","applyStyling":true,"fontColor":"red"}`,
		`{"kind":"js","applyStyling":true,"fontSize":"13"}`,
		`{"kind":"js","applyStyling":true,"fontSize":"1;color:red"}`,
		`{"kind":"js","applyStyling":true,"fontFamily":"a\" onload=\"x"}`,
		`{"kind":"js","applyStyling":true,"fontFamily":"a'b"}`,
		`{"kind":"js","language":"de&x=1"}`,
		`{"kind":"js","matomoUrl":"http://m.example.test"}`,
		`{"kind":"js","matomoUrl":"https://u:p@example.test"}`,
		`{"kind":"js","matomoUrl":"https://m.example.test/?x=1"}`,
		`{"kind":"js","cookiePath":"/x"}`,
		`{"kind":"self-contained","language":"de"}`,
		`{"kind":"self-contained","matomoUrl":"https://m.example.test"}`,
		`{"kind":"self-contained","cookiePath":"x"}`,
		`{"kind":"self-contained","cookiePath":"/a;b"}`,
		`{"kind":"self-contained","cookieDomain":"a b"}`,
		`{"kind":"self-contained","cookieSameSite":"Other"}`,
		`{"kind":"js","callback":"x"}`,
		`{"kind":"js","passwordConfirmation":"x"}`,
	} {
		_, err := env.invoke("matomo.opt-out-code.get", "open", arguments)
		if err == nil || !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if _, err := env.invoke("matomo.opt-out-code.get", "instance", `{"kind":"js"}`); err == nil {
		t.Error("the code was served to a connection without a website")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestOptOutCodeGetMapsFailuresWithoutRetry(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) *http.Response
		class       provider.Class
		unsupported bool
	}{
		"permission":  {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission, false},
		"not found":   {func(call) *http.Response { return failure("An unexpected website was found " + bodyCanary) }, provider.ClassNotFound, false},
		"invalid url": {func(call) *http.Response { return failure("The provided URL is invalid." + bodyCanary) }, provider.ClassInvalidResponse, false},
		"method missing": {func(call) *http.Response {
			return failure("The method 'CoreAdminHome.getOptOutJSEmbedCode' does not exist or is not available in the module 'CoreAdminHome'.")
		}, "", true},
		"plugin inactive": {func(call) *http.Response { return failure("The plugin CoreAdminHome is not activated.") }, "", true},
		"429":             {func(call) *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited, false},
		"html":            {fixed("<html>" + bodyCanary + "</html>"), provider.ClassInvalidResponse, false},
		"empty code":      {fixed(`""`), provider.ClassInvalidResponse, false},
		"not text":        {fixed(`{"a":1}`), provider.ClassInvalidResponse, false},
	} {
		var calls []call
		env := newEnvironment(t, &calls, optOutAnswers(answers{"CoreAdminHome.getOptOutJSEmbedCode": tc.answer}).handler(t))
		_, err := env.invoke("matomo.opt-out-code.get", "open", `{"kind":"js"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
			classOf(err) != tc.class || countMethod(calls, "CoreAdminHome.getOptOutJSEmbedCode") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
