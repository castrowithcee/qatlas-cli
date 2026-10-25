package matomo

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const complianceSetConnections = `
  cs-writer: {service: matomo, credential: token, effects: [read, update], targets: [site/2, instance]}
  cs-instance: {service: matomo, credential: token, effects: [read, update], targets: [instance]}
  cs-site: {service: matomo, credential: token, effects: [read, update], targets: [site/2]}
  cs-unlisted: {service: matomo, credential: token, effects: [read, update, execute], targets: [site/2, instance]}
  cs-enforce-site: {service: matomo, credential: token, tools: [matomo.compliance.enforce], targets: [site/2]}
  cs-enforce-instance: {service: matomo, credential: token, tools: [matomo.compliance.enforce], targets: [instance]}
  cs-enforce: {service: matomo, credential: token, tools: [matomo.compliance.enforce], targets: [site/2, instance]}
`

const (
	setPolicy = "matomo.compliance.set-policy-settings"
	setStatus = "matomo.compliance.set-status"
	enforceOp = "matomo.compliance.enforce"

	policySettingsAnswer = `{"policy":"cnil_v1","title":"CNIL","description":"<b>FR</b>","configControlled":false,` +
		`"policyEnforced":false,"settings":[{"id":"PrivacyManager.IPAnonymisation","name":"IP","whatItDoes":"masks",` +
		`"impact":"low","status":"enforced","enforced":true,"toggleable":true,"section":"settings"},` +
		`{"id":"Core.ThirdPartyCookies","name":"Cookies","whatItDoes":"n","impact":"","status":"manual",` +
		`"enforced":null,"toggleable":false,"section":"external"}]}`
)

var complianceSetCalls = []struct{ op, method, args string }{
	{setPolicy, "PrivacyManager.setCompliancePolicySettings",
		`{"policy":"cnil_v1","all_sites":true,"settingValues":{"PrivacyManager.IPAnonymisation":true}}`},
	{enforceOp, "PrivacyManager.enforceCompliancePolicySettings", `{"policy":"cnil_v1","idSite":2}`},
	{setStatus, "PrivacyManager.setComplianceStatus", `{"policy":"cnil_v1","all_sites":true,"enforce":true}`},
}

func complianceSetAnswers(extra answers) answers {
	reply := func(call) *http.Response { return ok(policySettingsAnswer) }
	return with(with(pluginsOn(), answers{
		"PrivacyManager.setCompliancePolicySettings":     reply,
		"PrivacyManager.enforceCompliancePolicySettings": reply,
		"PrivacyManager.setComplianceStatus": func(c call) *http.Response {
			return ok(`{"value":` + strconv.FormatBool(c.form.Get("enforce") == "1") + `}`)
		},
	}), extra)
}

func newComplianceSetEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(complianceSetConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// enforceConnections are the connections of the tools list that stand in for the connections of the effects.
var enforceConnections = map[string]string{"cs-site": "cs-enforce-site", "cs-instance": "cs-enforce-instance"}

// connectionFor is the connection that offers the tool to the tests.
func connectionFor(op string) string {
	if op == enforceOp {
		return "cs-enforce"
	}
	return "cs-writer"
}

func TestComplianceSettersSendOneFixedRequest(t *testing.T) {
	var calls []call
	env := newComplianceSetEnvironment(t, &calls, complianceSetAnswers(nil).handler(t))
	for _, c := range complianceSetCalls {
		calls = nil
		result, err := env.change(c.op, connectionFor(c.op), c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		if c.op == setStatus {
			if result != `{"enforce":true,"policy":"cnil_v1"}` {
				t.Errorf("%s: result = %s", c.op, result)
			}
		} else {
			for _, want := range []string{`"kind":"settings"`, `"policy_enforced":false`, `"id":"PrivacyManager.IPAnonymisation"`,
				`"what_it_does":"masks"`, `"toggleable":false`} {
				if !strings.Contains(result, want) {
					t.Errorf("%s: missing %s in %s", c.op, want, result)
				}
			}
			if strings.Contains(result, `"enforced":false`) {
				t.Errorf("%s: a null flag reads as false: %s", c.op, result)
			}
		}
		sent := calls[len(calls)-1]
		if sent.api() != c.method || countMethod(calls, c.method) != 1 || sent.method != http.MethodPost ||
			sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Has("token_auth") ||
			sent.form.Has("passwordConfirmation") || sent.form.Get("format") != "JSON" {
			t.Errorf("%s: request = %+v", c.op, sent)
		}
		for key := range sent.form {
			if strings.Contains(strings.ToLower(key), "password") || key == "callback" || key == "jsoncallback" ||
				strings.HasPrefix(key, "disable_") || key == "filter_limit" {
				t.Errorf("%s: parameter %s sent", c.op, key)
			}
		}
	}
	calls = nil
	if _, err := env.change(setPolicy, "cs-writer", `{"policy":"cnil_v1","idSite":2,"settingValues":{"PrivacyManager.IPAnonymisation":false,"Core.UserId":true}}`); err != nil {
		t.Fatal(err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("idSite") != "2" || sent.form.Get("compliancePolicy") != "cnil_v1" ||
		sent.form.Get("settingValues[PrivacyManager.IPAnonymisation]") != "0" || sent.form.Get("settingValues[Core.UserId]") != "1" {
		t.Errorf("set-policy-settings form = %v", sent.form)
	}
	calls = nil
	if _, err := env.change(setStatus, "cs-writer", `{"policy":"cnil_v1","idSite":2,"enforce":false}`); err != nil {
		t.Fatal(err)
	}
	sent = calls[len(calls)-1]
	if sent.form.Get("idSite") != "2" || sent.form.Get("complianceType") != "cnil_v1" || sent.form.Get("enforce") != "0" {
		t.Errorf("set-status form = %v", sent.form)
	}
}

func TestComplianceSettersRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newComplianceSetEnvironment(t, &calls, complianceSetAnswers(nil).handler(t))
	valid := map[string]string{setPolicy: `"settingValues":{"PrivacyManager.IPAnonymisation":true}`,
		setStatus: `"enforce":true`}
	for _, op := range []string{setPolicy, enforceOp, setStatus} {
		extra := ""
		if v := valid[op]; v != "" {
			extra = "," + v
		}
		connection := connectionFor(op)
		for name, tc := range map[string]struct{ connection, args string }{
			"no instance":   {"cs-site", `{"policy":"cnil_v1","idSite":2` + extra + `}`},
			"foreign site":  {connection, `{"policy":"cnil_v1","idSite":9` + extra + `}`},
			"instance only": {"cs-instance", `{"policy":"cnil_v1","idSite":2` + extra + `}`},
			"no scope":      {connection, `{"policy":"cnil_v1"` + extra + `}`},
			"both scopes":   {connection, `{"policy":"cnil_v1","idSite":2,"all_sites":true` + extra + `}`},
			"no policy":     {connection, `{"all_sites":true` + extra + `}`},
			"bad policy":    {connection, `{"policy":"a b","all_sites":true` + extra + `}`},
			"zero site":     {connection, `{"policy":"cnil_v1","idSite":0` + extra + `}`},
			"password":      {connection, `{"policy":"cnil_v1","all_sites":true,"passwordConfirmation":"x"` + extra + `}`},
		} {
			if renamed, listed := enforceConnections[tc.connection]; op == enforceOp && listed {
				tc.connection = renamed
			}
			if _, err := env.change(op, tc.connection, tc.args); err == nil {
				t.Errorf("%s %s: accepted", op, name)
			}
		}
	}
	for name, args := range map[string]string{
		"missing":      `{"policy":"p","all_sites":true}`,
		"empty":        `{"policy":"p","all_sites":true,"settingValues":{}}`,
		"bad id":       `{"policy":"p","all_sites":true,"settingValues":{"no-dot":true}}`,
		"non boolean":  `{"policy":"p","all_sites":true,"settingValues":{"A.b":"yes"}}`,
		"null":         `{"policy":"p","all_sites":true,"settingValues":{"A.b":null}}`,
		"form key":     `{"policy":"p","all_sites":true,"settingValues":{"A.b][c":true}}`,
		"unknown name": `{"policy":"p","all_sites":true,"settingValues":{"A.b":true},"x":1}`,
	} {
		if _, err := env.change(setPolicy, "cs-writer", args); err == nil {
			t.Errorf("settingValues %s: accepted", name)
		}
	}
	if _, err := env.change(setStatus, "cs-writer", `{"policy":"p","all_sites":true}`); err == nil {
		t.Error("set-status without enforce accepted")
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	for _, op := range []string{setPolicy, enforceOp, setStatus} {
		args := map[string]string{setPolicy: complianceSetCalls[0].args, enforceOp: complianceSetCalls[1].args,
			setStatus: complianceSetCalls[2].args}[op]
		if _, err := env.invoke(op, connectionFor(op), args); err == nil ||
			!strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s: err = %v, want a confirmation request", op, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("an unconfirmed call read %d secrets and sent %d requests", *env.reads, len(calls))
	}
}

func TestComplianceEnforceIsOfferedOnlyThroughTheToolsList(t *testing.T) {
	var calls []call
	env := newComplianceSetEnvironment(t, &calls, complianceSetAnswers(nil).handler(t))
	for _, connection := range []string{"cs-writer", "cs-unlisted", "writer"} {
		if _, err := env.change(enforceOp, connection, `{"policy":"cnil_v1","all_sites":true}`); err == nil {
			t.Errorf("%s: enforce was offered without a tools entry", connection)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests, %d secret reads", len(calls), *env.reads)
	}
	if _, err := env.change(enforceOp, "cs-enforce", `{"policy":"cnil_v1","all_sites":true}`); err != nil {
		t.Errorf("listed enforce: %v", err)
	}
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range metadata.Tools {
		switch tool.ID {
		case enforceOp:
			if !tool.RequiresToolAllowList || tool.Effect != config.EffectExecute {
				t.Errorf("enforce = %+v", tool)
			}
		case setPolicy, setStatus:
			if tool.RequiresToolAllowList || tool.Effect != config.EffectUpdate {
				t.Errorf("%s = %+v", tool.ID, tool)
			}
		}
	}
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == setPolicy || id == setStatus || id == enforceOp {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
	for _, id := range []string{setPolicy, setStatus, enforceOp} {
		d, _, _ := reg.Lookup(id)
		if d.Risk.Confirmation != "required" || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "" {
			t.Errorf("%s risk = %+v", id, d.Risk)
		}
		if ok, forms := reaches(id, []string{"site"}); ok || len(forms) != 1 || forms[0] != "instance" {
			t.Errorf("%s reaches = %v %v", id, ok, forms)
		}
		if ok, _ := reaches(id, []string{"site/1", "instance"}); !ok {
			t.Errorf("%s is not offered to an instance binding", id)
		}
	}
}

func TestComplianceSettersNeedTheirMatomoRelease(t *testing.T) {
	for _, tc := range []struct{ op, version string }{
		{setPolicy, "5.13.9"}, {enforceOp, "5.13.9"}, {setStatus, "5.8.9"}} {
		var calls []call
		env := newComplianceSetEnvironment(t, &calls, with(complianceSetAnswers(nil), versionOnly(tc.version)).handler(t))
		args := map[string]string{setPolicy: complianceSetCalls[0].args, enforceOp: complianceSetCalls[1].args,
			setStatus: complianceSetCalls[2].args}[tc.op]
		_, err := env.change(tc.op, connectionFor(tc.op), args)
		if !isUnsupported(err) || !strings.Contains(err.Error(), "or newer") {
			t.Errorf("%s on %s: err = %v", tc.op, tc.version, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "PrivacyManager.") {
				t.Errorf("%s on %s called %s", tc.op, tc.version, c.api())
			}
		}
	}
	// The older switch is served from 5.9.0, the granular tools from 5.14.0.
	var calls []call
	env := newComplianceSetEnvironment(t, &calls, with(complianceSetAnswers(nil), versionOnly("5.9.0")).handler(t))
	if _, err := env.change(setStatus, "cs-writer", complianceSetCalls[2].args); err != nil {
		t.Errorf("set-status on 5.9.0: %v", err)
	}
}

func TestComplianceSettersMapMatomoRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		message string
		class   provider.Class
		invalid bool
		hint    string
	}{
		"flag off":       {"Granular compliance configuration is not enabled", "unsupported", false, "GranularPrivacyCompliance_feature"},
		"config control": {"The compliance policy is controlled by the config file and cannot be changed", provider.ClassPermission, false, "configuration file"},
		"unknown id":     {`The setting "A.b" is unknown or cannot be toggled`, "", true, ""},
		"policy":         {"Invalid compliance policy", provider.ClassNotFound, false, ""},
		"not super user": {"You can't access this resource as it requires 'superuser' access.", provider.ClassPermission, false, ""},
		"site":           {"An unexpected website was found in the request: website id = 2", provider.ClassNotFound, false, ""},
	} {
		for _, c := range complianceSetCalls {
			if c.op == setStatus && (name == "flag off" || name == "config control" || name == "unknown id") {
				continue
			}
			var calls []call
			env := newComplianceSetEnvironment(t, &calls, complianceSetAnswers(answers{
				c.method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change(c.op, connectionFor(c.op), c.args)
			switch {
			case err == nil, strings.Contains(err.Error(), bodyCanary), strings.Contains(err.Error(), "A.b"):
				t.Errorf("%s %s: err = %v", c.op, name, err)
			case tc.invalid && !isInvalidRequest(err):
				t.Errorf("%s %s: err = %v, want invalid-request", c.op, name, err)
			case tc.class == "unsupported" && !isUnsupported(err):
				t.Errorf("%s %s: err = %v, want unsupported-capability", c.op, name, err)
			case tc.class != "" && tc.class != "unsupported" && classOf(err) != tc.class:
				t.Errorf("%s %s: class = %s, err = %v", c.op, name, classOf(err), err)
			case tc.hint != "" && !strings.Contains(err.Error(), tc.hint):
				t.Errorf("%s %s: no hint in %v", c.op, name, err)
			}
			if countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: sent %d", c.op, name, countMethod(calls, c.method))
			}
		}
	}
	// A method or plugin the instance lacks is no feature-flag hint.
	var calls []call
	env := newComplianceSetEnvironment(t, &calls, complianceSetAnswers(answers{
		"PrivacyManager.enforceCompliancePolicySettings": func(call) *http.Response {
			return failure("The method 'PrivacyManager.x' does not exist or is not available in the module 'PrivacyManager'.")
		},
	}).handler(t))
	_, err := env.change(enforceOp, "cs-enforce", complianceSetCalls[1].args)
	if !isUnsupported(err) || strings.Contains(err.Error(), "FeatureFlags") {
		t.Errorf("missing method: err = %v", err)
	}
}

func TestComplianceSettersSendOnceAndReportUncertainty(t *testing.T) {
	for _, c := range complianceSetCalls {
		for name, handler := range map[string]func(call) (*http.Response, error){
			"timeout":    func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"server":     func(call) (*http.Response, error) { return respond(500, bodyCanary), nil },
			"unreadable": func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
			"no echo":    func(call) (*http.Response, error) { return ok(`{"value":false}`), nil },
			"empty":      func(call) (*http.Response, error) { return ok(`[]`), nil },
		} {
			if (name == "no echo") != (c.op == setStatus) && (name == "no echo" || name == "empty") {
				continue
			}
			var calls []call
			env := newComplianceSetEnvironment(t, &calls, func(x call) (*http.Response, error) {
				if x.api() == c.method {
					return handler(x)
				}
				return complianceSetAnswers(nil).handler(t)(x)
			})
			_, err := env.change(c.op, connectionFor(c.op), c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 ||
				!strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: err = %v, sent %d", c.op, name, err, countMethod(calls, c.method))
			}
		}
		var calls []call
		env := newComplianceSetEnvironment(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return respond(429, bodyCanary), nil
			}
			return complianceSetAnswers(nil).handler(t)(x)
		})
		_, err := env.change(c.op, connectionFor(c.op), c.args)
		if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
			t.Errorf("%s 429: err = %v", c.op, err)
		}
	}
}
