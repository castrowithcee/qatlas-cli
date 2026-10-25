package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var rightsCases = []struct{ tool, method, args string }{
	{"matomo.access.set", "UsersManager.setUserAccess", `{"login":"alice","idSites":[1,2],"role":"view"}`},
	{"matomo.capabilities.add", "UsersManager.addCapabilities", `{"login":"alice","idSites":[1,2],"capabilities":["tagmanager_write"]}`},
	{"matomo.capabilities.remove", "UsersManager.removeCapabilities", `{"login":"alice","idSites":[1,2],"capabilities":["tagmanager_write"]}`},
}

func rightsAnswers(handler func(call) *http.Response) answers {
	return siteManageAnswers(answers{"UsersManager.setUserAccess": handler, "UsersManager.addCapabilities": handler,
		"UsersManager.removeCapabilities": handler})
}

func TestRightsToolsAreListedOnlyUpdatesWithFullRisk(t *testing.T) {
	reg := registry(t)
	for _, c := range rightsCases {
		d, _, found := reg.Lookup(c.tool)
		if !found || d.Risk.Effect != capability.EffectUpdate || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity != usersSensitive ||
			!d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		if strings.Contains(string(d.InputSchema), "passwordConfirmation") || strings.Contains(string(d.InputSchema), "superuser") {
			t.Errorf("%s offers a password or Super User input", c.tool)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, c := range rightsCases {
			if strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+c.tool+" ") {
				t.Errorf("profile %s selects %s", profile.ID, c.tool)
			}
		}
	}
}

func TestRightsToolsSendOneFixedRequest(t *testing.T) {
	for _, c := range rightsCases {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, rightsAnswers(func(call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		}).handler(t))
		if _, err := env.invoke(c.tool, "um-rights", c.args); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", c.tool, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("unconfirmed call: %d requests, %d reads", len(calls), *env.reads)
		}
		// A profile connection with the update effect does not offer a listed-only tool.
		if _, err := env.change(c.tool, "sm-writer", c.args); err == nil || len(calls) != 0 {
			t.Fatalf("%s offered without a tools list: %v, %d requests", c.tool, err, len(calls))
		}
		result, err := env.change(c.tool, "um-rights", c.args)
		if err != nil || !strings.Contains(result, `"updated":true`) || !strings.Contains(result, `"idSites":[1,2]`) {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		sent := calls[len(calls)-1]
		if countMethod(calls, c.method) != 1 || sent.api() != c.method || sent.auth != "Bearer "+tokenValue ||
			sent.rawQuery != "" || sent.form.Has("token_auth") || sent.form.Has("passwordConfirmation") ||
			sent.form.Get("userLogin") != "alice" || sent.form.Get("idSites") != "1,2" || sent.form.Get("format") != "JSON" {
			t.Errorf("%s: request = %+v", c.tool, sent)
		}
		for name := range sent.form {
			switch name {
			case "method", "userLogin", "idSites", "module", "format", "serialize", "access", "access[]", "capabilities[]":
			default:
				t.Errorf("%s: unexpected form field %s", c.tool, name)
			}
		}
		if c.tool == "matomo.access.set" && sent.form.Get("access") != "view" {
			t.Errorf("access = %v", sent.form)
		}
		if c.tool != "matomo.access.set" && strings.Join(sent.form["capabilities[]"], ",") != "tagmanager_write" {
			t.Errorf("capabilities = %v", sent.form)
		}
	}
}

func TestAccessSetSendsRoleWithCapabilitiesAndNoAccess(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, rightsAnswers(func(call) *http.Response {
		return ok(`{"result":"success"}`)
	}).handler(t))
	if _, err := env.change("matomo.access.set", "um-rights",
		`{"login":"alice","idSites":[1],"role":"write","capabilities":["tagmanager_write","tagmanager_publish_live_container"]}`); err != nil {
		t.Fatal(err)
	}
	sent := calls[len(calls)-1]
	if strings.Join(sent.form["access[]"], ",") != "write,tagmanager_write,tagmanager_publish_live_container" || sent.form.Has("access") {
		t.Errorf("form = %v", sent.form)
	}
	if _, err := env.change("matomo.access.set", "um-rights", `{"login":"alice","idSites":[2],"role":"noaccess"}`); err != nil {
		t.Fatal(err)
	}
	if sent = calls[len(calls)-1]; sent.form.Get("access") != "noaccess" || sent.form.Get("idSites") != "2" {
		t.Errorf("form = %v", sent.form)
	}
}

func TestRightsRefuseBeforeSecretAndRequest(t *testing.T) {
	long := `"` + strings.Repeat("a", 101) + `"`
	bad := map[string][]string{
		"matomo.access.set": {`{}`, `{"login":"alice","idSites":[1]}`, `{"login":"alice","idSites":[1],"role":"superuser"}`,
			`{"login":"alice","idSites":[1],"role":"Admin"}`, `{"login":"alice","idSites":[1],"role":"view,admin"}`,
			`{"login":"alice","idSites":[1],"role":"noaccess","capabilities":["tagmanager_write"]}`,
			`{"login":"alice","idSites":[1],"role":"view","capabilities":["admin"]}`,
			`{"login":"alice","idSites":[1],"role":"view","capabilities":["SuperUser"]}`,
			`{"login":"alice","idSites":[1],"role":"view","capabilities":["a","a"]}`,
			`{"login":"alice","idSites":[1],"role":"view","passwordConfirmation":"x"}`,
			`{"login":"anonymous","idSites":[1],"role":"view"}`, `{"login":"Anonymous","idSites":[1],"role":"noaccess"}`},
		"matomo.capabilities.add": {`{"login":"alice","idSites":[1]}`, `{"login":"alice","idSites":[1],"capabilities":[]}`,
			`{"login":"alice","idSites":[1],"capabilities":["view"]}`, `{"login":"alice","idSites":[1],"capabilities":["a b"]}`,
			`{"login":"alice","idSites":[1],"capabilities":["x"],"method":"UsersManager.setSuperUserAccess"}`,
			`{"login":"anonymous","idSites":[1],"capabilities":["x"]}`},
		"matomo.capabilities.remove": {`{"login":"alice","idSites":[1],"capabilities":["noaccess"]}`},
	}
	for _, c := range rightsCases {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, rightsAnswers(func(call) *http.Response { return ok(`{}`) }).handler(t))
		role := ""
		if c.tool == "matomo.access.set" {
			role = `"role":"view",`
		}
		caps := `"capabilities":["tagmanager_write"],`
		if c.tool == "matomo.access.set" {
			caps = ""
		}
		args := bad[c.tool]
		for _, tail := range []string{`"idSites":[3]`, `"idSites":[1,3]`, `"idSites":[]`, `"idSites":[1,1]`, `"idSites":[0]`,
			`"idSites":"all"`, `"idSites":[1000000000]`} {
			args = append(args, `{"login":"alice",`+role+caps+tail+`}`)
		}
		args = append(args, `{"login":`+long+`,`+role+caps+`"idSites":[1]}`, `{"login":"a\nb",`+role+caps+`"idSites":[1]}`)
		for _, a := range args {
			_, err := env.change(c.tool, "um-rights", a)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "anonymous") || strings.Contains(err.Error(), "3") {
				t.Errorf("%s %s: err = %v", c.tool, a, err)
			}
		}
		if _, err := env.change(c.tool, "um-rights-instance", c.args); err == nil {
			t.Errorf("%s ran on an instance-only binding", c.tool)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d reads before a refusal", c.tool, len(calls), *env.reads)
		}
	}
}

func TestRightsErrorsAndUnclearResults(t *testing.T) {
	for _, c := range rightsCases {
		for name, tc := range map[string]struct {
			answer func(call) *http.Response
			check  func(error) bool
		}{
			"permission": {func(call) *http.Response {
				return failure("You can't access this resource as it requires 'admin' access for the website id = 2.")
			}, func(err error) bool { return classOf(err) == provider.ClassPermission }},
			"not found": {func(call) *http.Response { return failure("User 'x' doesn't exist.") },
				func(err error) bool { return classOf(err) == provider.ClassNotFound }},
			"missing": {func(call) *http.Response {
				return failure("The method '" + c.method + "' does not exist or is not available in the module 'UsersManager'.")
			}, isUnsupported},
			"plugin inactive": {func(call) *http.Response {
				return failure("The plugin UsersManager is not enabled.")
			}, func(err error) bool { return err != nil }},
		} {
			var calls []call
			env := newSiteManageEnvironment(t, &calls, rightsAnswers(tc.answer).handler(t))
			_, err := env.change(c.tool, "um-rights", c.args)
			if err == nil || !tc.check(err) || strings.Contains(err.Error(), "'x'") || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v", c.tool, name, err)
			}
		}
		for name, handler := range map[string]func(call) *http.Response{
			"no confirmation": func(call) *http.Response { return ok(`{"value":1}`) },
			"server":          func(call) *http.Response { return respond(500, bodyCanary) },
			"rate":            func(call) *http.Response { return respond(429, bodyCanary) },
			"html":            func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
		} {
			var calls []call
			env := newSiteManageEnvironment(t, &calls, rightsAnswers(handler).handler(t))
			_, err := env.change(c.tool, "um-rights", c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 ||
				name != "rate" && !strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: err = %v, sent %d", c.tool, name, err, countMethod(calls, c.method))
			}
		}
		var calls []call
		env := newSiteManageEnvironment(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return siteManageAnswers(answers{}).handler(t)(x)
		})
		if _, err := env.change(c.tool, "um-rights", c.args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.tool, err)
		}
	}
}
