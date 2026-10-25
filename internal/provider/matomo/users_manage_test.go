package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var userManageCases = []struct{ tool, method, field string }{
	{"matomo.users.delete", "UsersManager.deleteUser", "deleted"},
	{"matomo.users.logout", "UsersManager.logoutUser", "logged_out"},
}

func userAnswers(handler func(call) *http.Response) answers {
	return siteManageAnswers(answers{"UsersManager.deleteUser": handler, "UsersManager.logoutUser": handler})
}

func TestUserManageToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect capability.Effect
		listed bool
	}{
		"matomo.users.delete": {capability.EffectDelete, true},
		"matomo.users.logout": {capability.EffectExecute, false},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity != usersSensitive ||
			d.RequiresToolAllowList != want.listed {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := " " + strings.Join(profile.Tools, " ") + " "
		if strings.Contains(joined, " matomo.users.delete ") || strings.Contains(joined, " matomo.users.logout ") {
			t.Errorf("profile %s selects a user change", profile.ID)
		}
	}
}

func TestUserManageToolsSendOneFixedRequest(t *testing.T) {
	for _, c := range userManageCases {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, userAnswers(func(call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		}).handler(t))
		connection := "sm-writer"
		if c.tool == "matomo.users.delete" {
			connection = "um-deleter"
		}
		if _, err := env.invoke(c.tool, connection, `{"login":"alice"}`); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", c.tool, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("unconfirmed call: %d requests, %d reads", len(calls), *env.reads)
		}
		if c.tool == "matomo.users.delete" {
			if _, err := env.change(c.tool, "sm-writer", `{"login":"alice"}`); err == nil {
				t.Fatal("an effects connection offered users.delete")
			}
			if len(calls) != 0 {
				t.Fatalf("a tool that is not offered caused %d requests", len(calls))
			}
		}
		result, err := env.change(c.tool, connection, `{"login":"alice"}`)
		if err != nil || result != `{"`+c.field+`":true,"login":"alice"}` {
			t.Fatalf("%s: result = %s, err = %v", c.tool, result, err)
		}
		sent := calls[len(calls)-1]
		if countMethod(calls, c.method) != 1 || sent.api() != c.method || sent.auth != "Bearer "+tokenValue ||
			sent.form.Has("token_auth") || sent.form.Has("passwordConfirmation") || sent.form.Get("userLogin") != "alice" {
			t.Errorf("%s: request = %+v", c.tool, sent)
		}
		for name := range sent.form {
			if name != "method" && name != "userLogin" && name != "module" && name != "format" && name != "serialize" {
				t.Errorf("%s: unexpected form field %s", c.tool, name)
			}
		}
	}
}

func TestUserManageRefusesBeforeSecretAndRequest(t *testing.T) {
	for _, c := range userManageCases {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, userAnswers(func(call) *http.Response { return ok(`{}`) }).handler(t))
		connection := "sm-writer"
		if c.tool == "matomo.users.delete" {
			connection = "um-deleter"
		}
		for _, args := range []string{`{}`, `{"login":""}`, `{"login":"` + strings.Repeat("a", 101) + `"}`,
			`{"login":"a\nb"}`, `{"login":"Anonymous"}`, `{"login":"anonymous"}`} {
			_, err := env.change(c.tool, connection, args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "anonymous") {
				t.Errorf("%s %s: err = %v", c.tool, args, err)
			}
		}
		if _, err := env.change(c.tool, "sm-noinst", `{"login":"alice"}`); err == nil {
			t.Errorf("%s ran without the instance", c.tool)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d reads before a refusal", c.tool, len(calls), *env.reads)
		}
		for targets, want := range map[string]bool{"site/1": false, "site": false, "instance": true, "site,instance": true} {
			if ok, _ := reaches(c.tool, strings.Split(targets, ",")); ok != want {
				t.Errorf("reaches(%s, %s) = %t", c.tool, targets, ok)
			}
		}
	}
}

func TestUserManageErrorsAndUnclearResults(t *testing.T) {
	for _, c := range userManageCases {
		connection := "sm-writer"
		if c.tool == "matomo.users.delete" {
			connection = "um-deleter"
		}
		for name, tc := range map[string]struct {
			answer func(call) *http.Response
			check  func(error) bool
		}{
			"permission": {func(call) *http.Response {
				return failure("You can't access this resource as it requires 'superuser' access.")
			},
				func(err error) bool { return classOf(err) == provider.ClassPermission }},
			"not found": {func(call) *http.Response { return failure("User 'x' doesn't exist.") },
				func(err error) bool { return classOf(err) == provider.ClassNotFound }},
			"missing": {func(call) *http.Response {
				return failure("The method 'deleteUser' does not exist or is not available in the module 'UsersManager'.")
			}, isUnsupported},
		} {
			var calls []call
			env := newSiteManageEnvironment(t, &calls, userAnswers(tc.answer).handler(t))
			_, err := env.change(c.tool, connection, `{"login":"alice"}`)
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
			env := newSiteManageEnvironment(t, &calls, userAnswers(handler).handler(t))
			_, err := env.change(c.tool, connection, `{"login":"alice"}`)
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
		if _, err := env.change(c.tool, connection, `{"login":"alice"}`); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.tool, err)
		}
	}
}
