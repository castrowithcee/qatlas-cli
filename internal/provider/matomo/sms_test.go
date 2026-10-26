package matomo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// smsNumber is a made-up number in the unassigned +999 range.
const smsNumber = "+" + "99900011122"

// smsOther is a second made-up number in the same range.
const smsOther = "+" + "99900011123"

const smsConnections = `
  sms-inst: {service: matomo, credential: token, tools: [matomo.sms-settings.get, matomo.sms-phone-numbers.remove, matomo.sms-credentials.delete, matomo.sms-delegation.set], targets: [instance]}
  sms-site: {service: matomo, credential: token, tools: [matomo.sms-settings.get, matomo.sms-phone-numbers.remove, matomo.sms-credentials.delete, matomo.sms-delegation.set], targets: [site/1]}
  sms-nolist: {service: matomo, credential: token, effects: [read, update, delete], targets: [instance]}
`

func newSMSEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	serve(t, calls, with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) }}, a)).handler(t))
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(smsConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func TestSMSSettingsGetReadsOneSectionPerCall(t *testing.T) {
	reply := func(body string) func(call) *http.Response { return func(call) *http.Response { return ok(body) } }
	var calls []call
	env := newSMSEnvironment(t, &calls, answers{
		"MobileMessaging.areSMSAPICredentialProvided": reply(`{"value":false}`),
		"MobileMessaging.getSMSProvider":              reply(`{"value":"Clockwork"}`),
		"MobileMessaging.getCreditLeft":               reply(`{"value":"12.5"}`),
		"MobileMessaging.getDelegatedManagement":      reply(`{"value":true}`),
		"MobileMessaging.getPhoneNumbers": reply(`{"` + smsNumber + `":{"verified":true,"verificationTime":1,"requestTime":1},` +
			`"` + smsOther + `":{"verified":false,"verificationTime":null,"requestTime":2}}`),
	})
	for section, want := range map[string]string{
		"credential-provided":  `{"credentialProvided":false,"section":"credential-provided"}`,
		"provider":             `{"provider":"Clockwork","section":"provider"}`,
		"credit-left":          `{"creditLeft":"12.5","section":"credit-left"}`,
		"delegated-management": `{"delegatedManagement":true,"section":"delegated-management"}`,
		"phone-numbers": `{"phoneNumbers":[{"phoneNumber":"` + smsNumber +
			`","verified":true},{"phoneNumber":"` + smsOther + `","verified":false}],"section":"phone-numbers"}`,
	} {
		result, err := env.invoke("matomo.sms-settings.get", "sms-inst", `{"section":"`+section+`"}`)
		sent := calls[len(calls)-1]
		if err != nil || result != want || sent.api() != smsSections[section] || sent.auth != "Bearer "+tokenValue ||
			sent.rawQuery != "" || strings.Contains(result, tokenValue) {
			t.Errorf("%s: result = %s, err = %v, request = %+v", section, result, err, sent)
		}
	}
	// An empty list arrives as [].
	env = newSMSEnvironment(t, &calls, answers{"MobileMessaging.getPhoneNumbers": reply(`[]`)})
	if result, err := env.invoke("matomo.sms-settings.get", "sms-inst", `{"section":"phone-numbers"}`); err != nil ||
		result != `{"section":"phone-numbers"}` {
		t.Errorf("empty: %s, %v", result, err)
	}
	// An unreadable flag is no answer.
	env = newSMSEnvironment(t, &calls, answers{"MobileMessaging.getDelegatedManagement": reply(`{"value":null}`)})
	if _, err := env.invoke("matomo.sms-settings.get", "sms-inst", `{"section":"delegated-management"}`); err == nil {
		t.Error("an empty flag was accepted")
	}
}

func TestSMSToolsRefuseBeforeSecretsAndIO(t *testing.T) {
	var calls []call
	env := newSMSEnvironment(t, &calls, answers{})
	valid := map[string]string{"matomo.sms-settings.get": `{"section":"provider"}`,
		"matomo.sms-phone-numbers.remove": `{"phoneNumber":"` + smsNumber + `"}`,
		"matomo.sms-credentials.delete":   `{}`, "matomo.sms-delegation.set": `{"delegatedManagement":true}`}
	for id, args := range valid {
		if _, err := env.change(id, "sms-site", args); err == nil || !isInvalidRequest(err) && !isUnsupported(err) {
			t.Errorf("%s ran without the instance target: %v", id, err)
		}
		if id != "matomo.sms-settings.get" {
			if _, err := env.invoke(id, "sms-inst", args); err == nil || !strings.Contains(err.Error(), "confirm") {
				t.Errorf("%s: err = %v, want a confirmation request", id, err)
			}
		}
		if ok, _ := reaches(id, []string{"site/1"}); ok {
			t.Errorf("%s reaches a website binding", id)
		}
		if ok, _ := reaches(id, []string{"instance"}); !ok {
			t.Errorf("%s does not reach an instance binding", id)
		}
	}
	for _, args := range []string{`{}`, `{"section":"other"}`, `{"section":"provider","x":1}`} {
		if _, err := env.invoke("matomo.sms-settings.get", "sms-inst", args); !isInvalidRequest(err) {
			t.Errorf("get %s: %v", args, err)
		}
	}
	for _, number := range []string{"", "99900011122", "+" + "999 000 111 22", "+9990", "+" + strings.Repeat("9", 31),
		smsNumber + "a", "+" + "999-0001-1122", "(" + smsNumber + ")", smsNumber + "\n"} {
		args := `{"phoneNumber":"` + strings.ReplaceAll(number, "\n", `\n`) + `"}`
		if _, err := env.change("matomo.sms-phone-numbers.remove", "sms-inst", args); !isInvalidRequest(err) {
			t.Errorf("remove %q: %v", number, err)
		}
	}
	for _, args := range []string{`{}`, `{"phoneNumber":1}`, `{"phoneNumber":"` + smsNumber + `","x":1}`} {
		if _, err := env.change("matomo.sms-phone-numbers.remove", "sms-inst", args); err == nil {
			t.Errorf("remove %s accepted", args)
		}
	}
	for _, args := range []string{`{}`, `{"delegatedManagement":"yes"}`, `{"delegatedManagement":null}`} {
		if _, err := env.change("matomo.sms-delegation.set", "sms-inst", args); err == nil {
			t.Errorf("set %s accepted", args)
		}
	}
	if _, err := env.change("matomo.sms-credentials.delete", "sms-inst", `{"x":1}`); err == nil {
		t.Error("delete accepted an argument")
	}
	// The listed-only delete is not offered by effects alone.
	if _, err := env.change("matomo.sms-credentials.delete", "sms-nolist", `{}`); err == nil {
		t.Error("sms-credentials.delete ran without a tools list")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls), *env.reads)
	}
}

func TestSMSChangesSendOneFixedRequest(t *testing.T) {
	var calls []call
	env := newSMSEnvironment(t, &calls, answers{
		"MobileMessaging.removePhoneNumber":      func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"MobileMessaging.deleteSMSAPICredential": func(call) *http.Response { return ok(`{"result":"success"}`) },
		"MobileMessaging.setDelegatedManagement": func(call) *http.Response { return ok(`{"value":true}`) },
	})
	for _, c := range []struct{ id, args, want, method, field, sent string }{
		{"matomo.sms-phone-numbers.remove", `{"phoneNumber":"` + smsNumber + `"}`, `{"removed":true}`,
			"MobileMessaging.removePhoneNumber", "phoneNumber", smsNumber},
		{"matomo.sms-credentials.delete", `{}`, `{"deleted":true}`, "MobileMessaging.deleteSMSAPICredential", "", ""},
		{"matomo.sms-delegation.set", `{"delegatedManagement":false}`, `{"delegatedManagement":false,"updated":true}`,
			"MobileMessaging.setDelegatedManagement", "delegatedManagement", "0"},
	} {
		result, err := env.change(c.id, "sms-inst", c.args)
		sent := calls[len(calls)-1]
		if err != nil || result != c.want || sent.api() != c.method || countMethod(calls, c.method) != 1 ||
			sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Has("token_auth") ||
			sent.form.Has("passwordConfirmation") || c.field != "" && sent.form.Get(c.field) != c.sent {
			t.Errorf("%s: result = %s, err = %v, request = %+v", c.id, result, err, sent)
		}
	}
}

func TestSMSChangesAreSentOnceAndMapErrors(t *testing.T) {
	tools := []struct{ id, args, method string }{
		{"matomo.sms-phone-numbers.remove", `{"phoneNumber":"` + smsNumber + `"}`, "MobileMessaging.removePhoneNumber"},
		{"matomo.sms-credentials.delete", `{}`, "MobileMessaging.deleteSMSAPICredential"},
		{"matomo.sms-delegation.set", `{"delegatedManagement":true}`, "MobileMessaging.setDelegatedManagement"},
	}
	for name, tc := range map[string]struct {
		answer  func(call) (*http.Response, error)
		class   provider.Class
		unclear bool
	}{
		"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, true},
		"502":             {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", true},
		"unreadable":      {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, true},
		"no confirmation": {func(call) (*http.Response, error) { return ok(`[]`), nil }, provider.ClassInvalidResponse, true},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
		"200 error": {func(call) (*http.Response, error) {
			return ok2(failure("Something failed " + bodyCanary))
		}, "", false},
		"429": {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false},
	} {
		for _, tool := range tools {
			var calls []call
			serve(t, &calls, func(c call) (*http.Response, error) {
				switch c.api() {
				case tool.method:
					return tc.answer(c)
				case "API.isPluginActivated":
					return ok(`{"value":true}`), nil
				}
				return versionOnly("5.14.2").handler(t)(c)
			})
			reads := 0
			red := &redact.Redactor{}
			reg := registry(t)
			document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(smsConnections, "\n"), 1)
			cfg, err := config.Decode(strings.NewReader(document), reg)
			if err != nil {
				t.Fatal(err)
			}
			env := &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
			_, err = env.change(tool.id, "sms-inst", tool.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.class != "" && classOf(err) != tc.class ||
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

func TestSMSSettingsGetMapsErrorsAndHidesProviderText(t *testing.T) {
	for name, answer := range map[string]func(call) *http.Response{
		"permission": func(call) *http.Response { return failure(permissionMsg + bodyCanary) },
		"error":      func(call) *http.Response { return failure("The SMS provider failed " + bodyCanary) },
		"429":        func(call) *http.Response { return respond(http.StatusTooManyRequests, "") },
		"unreadable": func(call) *http.Response { return ok("<html>" + bodyCanary + "</html>") },
	} {
		var calls []call
		env := newSMSEnvironment(t, &calls, answers{"MobileMessaging.getCreditLeft": answer})
		_, err := env.invoke("matomo.sms-settings.get", "sms-inst", `{"section":"credit-left"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
		if name == "429" && classOf(err) != provider.ClassRateLimited {
			t.Errorf("429: class = %s", classOf(err))
		}
	}
}
