package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The fixtures follow the return shapes of TagManager.createContainerVersion (the new ID as a scalar) and of
// updateContainerVersion and deleteContainerVersion (no value, which the API reports as a success).

const versionWriteConnections = `
  tmw: {service: matomo, credential: token, tools: [matomo.tag-manager-versions.create, matomo.tag-manager-versions.update, matomo.tag-manager-versions.delete], targets: [site]}
  tmw-two: {service: matomo, credential: token, tools: [matomo.tag-manager-versions.create, matomo.tag-manager-versions.update, matomo.tag-manager-versions.delete], targets: [site/2]}
  tmw-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
  tmw-reads: {service: matomo, credential: token, tools: [matomo.tag-manager-versions.list, matomo.tag-manager-versions.get], targets: [site]}
`

func newVersionWriteEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(versionWriteConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

var versionWriteTools = []struct{ id, method, arguments string }{
	{"matomo.tag-manager-versions.create", "TagManager.createContainerVersion", `{"idSite":1,"idContainer":"6OMh6taM","name":"v2"}`},
	{"matomo.tag-manager-versions.update", "TagManager.updateContainerVersion", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"name":"v2","description":""}`},
	{"matomo.tag-manager-versions.delete", "TagManager.deleteContainerVersion", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`},
}

func versionWriteAnswers(extra answers) answers {
	return tagManagerAnswers(with(answers{
		"TagManager.createContainerVersion": func(call) *http.Response { return ok(`{"value":12}`) },
		"TagManager.updateContainerVersion": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"TagManager.deleteContainerVersion": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func TestTagManagerVersionWriteToolsAreListedOnlyConfirmedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.tag-manager-versions.create": {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-versions.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.tag-manager-versions.delete": {capability.EffectDelete, capability.IdempotencyIdempotent},
	}
	for _, tool := range versionWriteTools {
		d, _, found := reg.Lookup(tool.id)
		w := want[tool.id]
		if !found || d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != tagManagerSensitive || !d.RequiresToolAllowList {
			t.Fatalf("%s: found %t, %+v", tool.id, found, d)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool.id {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
	// A connection that only allows effects never offers them: the tools list must name each one.
	var calls []call
	env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
	for _, tool := range versionWriteTools {
		if _, err := env.change(tool.id, "tmw-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
		if _, err := env.change(tool.id, "tmw-reads", tool.arguments); err == nil {
			t.Errorf("%s was offered by a connection that lists reads only", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTagManagerVersionWriteSendsOneConfirmedRequest(t *testing.T) {
	for _, tool := range versionWriteTools {
		var calls []call
		env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
		if _, err := env.invoke(tool.id, "tmw", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: an unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "tmw", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if countMethod(calls, tool.method) != 1 {
			t.Fatalf("%s sent %d times", tool.method, countMethod(calls, tool.method))
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") && c.api() != tool.method {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
		sent := calls[len(calls)-1]
		if sent.api() != tool.method || sent.form.Get("idSite") != "1" || sent.form.Get("idContainer") != "6OMh6taM" ||
			sent.form.Get("format") != "JSON" || sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") ||
			strings.Contains(sent.rawQuery, tokenValue) || strings.Contains(sent.path, tokenValue) {
			t.Errorf("%s: request = %+v", tool.id, sent)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "filter_limit"} {
			if sent.form.Has(name) {
				t.Errorf("%s carries %s", tool.id, name)
			}
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(result), &out)
		switch tool.id {
		case "matomo.tag-manager-versions.create":
			if out["created"] != true || out["idContainerVersion"] != float64(12) || out["idSite"] != float64(1) ||
				out["idContainer"] != "6OMh6taM" || sent.form.Get("name") != "v2" || sent.form.Get("description") != "" ||
				sent.form.Has("idContainerVersion") {
				t.Errorf("create: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-versions.update":
			if out["updated"] != true || out["idContainerVersion"] != float64(7) || sent.form.Get("idContainerVersion") != "7" ||
				sent.form.Get("name") != "v2" || !sent.form.Has("description") {
				t.Errorf("update: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-versions.delete":
			if out["deleted"] != true || out["idContainerVersion"] != float64(7) || sent.form.Get("idContainerVersion") != "7" {
				t.Errorf("delete: %s, %v", result, sent.form)
			}
		}
	}
}

func TestTagManagerVersionsCreateCopiesAVersionAndSendsTheDescription(t *testing.T) {
	var calls []call
	env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-versions.create", "tmw",
		`{"idSite":1,"idContainer":"6OMh6taM","name":"copy","description":"from 7","idContainerVersion":7}`); err != nil {
		t.Fatal(err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("idContainerVersion") != "7" || sent.form.Get("description") != "from 7" || sent.form.Get("name") != "copy" {
		t.Errorf("form = %v", sent.form)
	}
	// A scalar without the value wrapper is read as well; an answer without an ID is unclear and not repeated.
	for name, body := range map[string]string{"bare": `12`, "none": `{"result":"success"}`, "zero": `{"value":0}`, "text": `{"value":"x"}`} {
		var c []call
		e := newVersionWriteEnvironment(t, &c, versionWriteAnswers(answers{
			"TagManager.createContainerVersion": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := e.change("matomo.tag-manager-versions.create", "tmw", versionWriteTools[0].arguments)
		if (name == "bare") != (err == nil) || countMethod(c, "TagManager.createContainerVersion") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(c, "TagManager.createContainerVersion"))
		}
		if name != "bare" && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: no uncertainty message: %v", name, err)
		}
	}
}

func TestTagManagerVersionWriteRefusesBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	long := strings.Repeat("ä", 51)
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign website create":   {"create", "tmw-two", `{"idSite":1,"idContainer":"6OMh6taM","name":"v"}`},
		"foreign website update":   {"update", "tmw-two", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"name":"v","description":""}`},
		"foreign website delete":   {"delete", "tmw-two", `{"idSite":3,"idContainer":"6OMh6taM","idContainerVersion":7}`},
		"no website":               {"create", "tmw", `{"idContainer":"6OMh6taM","name":"v"}`},
		"missing container":        {"create", "tmw", `{"idSite":1,"name":"v"}`},
		"bad container":            {"delete", "tmw", `{"idSite":1,"idContainer":"a b","idContainerVersion":7}`},
		"missing name":             {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"empty name":               {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":""}`},
		"blank name":               {"update", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"name":"  ","description":""}`},
		"long name":                {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"` + long + `"}`},
		"long description":         {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"v","description":"` + strings.Repeat("x", 1001) + `"}`},
		"update without version":   {"update", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"v","description":""}`},
		"update without text":      {"update", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"name":"v"}`},
		"delete without version":   {"delete", "tmw", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"delete version zero":      {"delete", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":0}`},
		"create version too large": {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"v","idContainerVersion":2147483648}`},
		"create version zero":      {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"v","idContainerVersion":0}`},
		"create version text":      {"create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"v","idContainerVersion":"7"}`},
		"extra argument":           {"delete", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"method":"TagManager.publishContainerVersion"}`},
	} {
		var calls []call
		env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-versions."+tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "publishContainerVersion")) {
			t.Errorf("%s: the refusal quotes the value: %v", name, err)
		}
	}
	// Name and description at their caps pass.
	var calls []call
	env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-versions.create", "tmw", `{"idSite":1,"idContainer":"6OMh6taM","name":"`+
		strings.Repeat("ä", 50)+`","description":"`+strings.Repeat("x", 1000)+`"}`); err != nil {
		t.Errorf("name and description at the caps: %v", err)
	}
}

func TestTagManagerVersionWriteIsUnsupportedWithoutThePluginAndSendsNoMethod(t *testing.T) {
	for _, tool := range versionWriteTools {
		var calls []call
		env := newVersionWriteEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response { return ok(`{"value":false}`) },
		}).handler(t))
		if _, err := env.change(tool.id, "tmw", tool.arguments); !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerVersionWriteMapsErrorsWithoutTheMessageAndNeverRepeats(t *testing.T) {
	for _, tool := range versionWriteTools {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission":        {"You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
			"container missing": {`The requested container "token123" does not exist.`, provider.ClassNotFound, false},
			"version missing":   {"The requested container version does not exist.", provider.ClassNotFound, false},
			"site not found":    {"An unexpected website was found in the request: website id = 1 not found.", provider.ClassNotFound, false},
			"method missing":    {"The method 'TagManager.x' does not exist or is not available in the module 'TagManager'.", "", true},
			"plugin off":        {"The plugin TagManager is not activated.", "", true},
			"other":             {"Something went wrong", provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{
				tool.method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change(tool.id, "tmw", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
				isUnsupported(err) != tc.missing || classOf(err) != tc.class || countMethod(calls, tool.method) > 1 {
				t.Errorf("%s %s: err = %v (class %q)", tool.id, name, err, classOf(err))
			}
		}
	}
}

func TestTagManagerVersionWriteRefusalsAreClearAndKeepNoProviderText(t *testing.T) {
	for _, tc := range []struct {
		tool    int
		message string
	}{
		{0, "This name is already in use."},
		{1, "This name is already in use."},
		{2, `The "Draft" version of a container cannot be deleted.`},
		{2, "This version cannot be deleted as it is published to at least one environment."},
	} {
		tool := versionWriteTools[tc.tool]
		var calls []call
		env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{
			tool.method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
		}).handler(t))
		_, err := env.change(tool.id, "tmw", tool.arguments)
		if err == nil || !isInvalidRequest(err) || classOf(err) == provider.ClassInvalidResponse ||
			strings.Contains(err.Error(), "never repeated") || strings.Contains(err.Error(), bodyCanary) ||
			strings.Contains(err.Error(), "Draft") || !strings.Contains(err.Error(), "nothing was changed") ||
			countMethod(calls, tool.method) != 1 {
			t.Errorf("%s %q: err = %v, sent %d times", tool.id, tc.message, err, countMethod(calls, tool.method))
		}
	}
}

func TestTagManagerVersionWriteUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
	outcomes := map[string]func(call) (*http.Response, error){
		"429 without Retry-After": func(call) (*http.Response, error) { return respond(429, bodyCanary), nil },
		"bad gateway":             func(call) (*http.Response, error) { return respond(http.StatusBadGateway, bodyCanary), nil },
		"timeout":                 func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":                   func(call) (*http.Response, error) { return nil, context.Canceled },
		"html":                    func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
		"unreadable": func(call) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: errReader{}, Header: http.Header{}}, nil
		},
		"no confirmation": func(call) (*http.Response, error) { return ok(`[]`), nil },
	}
	for _, tool := range versionWriteTools {
		for name, outcome := range outcomes {
			var calls []call
			env := newVersionWriteEnvironment(t, &calls, func(c call) (*http.Response, error) {
				switch c.api() {
				case "API.getMatomoVersion":
					return ok(versionAnswer), nil
				case "API.isPluginActivated":
					return ok(`{"value":true}`), nil
				}
				return outcome(c)
			})
			_, err := env.change(tool.id, "tmw", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", tool.id, name, err, countMethod(calls, tool.method))
				continue
			}
			if name != "429 without Retry-After" && !strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: no uncertainty message: %v", tool.id, name, err)
			}
			for _, c := range calls {
				if strings.Contains(c.rawQuery+c.path, tokenValue) || (strings.HasPrefix(c.api(), "TagManager.") && c.auth != "Bearer "+tokenValue) {
					t.Errorf("%s %s: token handling: %+v", tool.id, name, c)
				}
			}
		}
	}
}

func TestTagManagerVersionWriteTokenIsInTheRedactor(t *testing.T) {
	var calls []call
	env := newVersionWriteEnvironment(t, &calls, versionWriteAnswers(answers{}).handler(t))
	if _, err := env.change(versionWriteTools[2].id, "tmw", versionWriteTools[2].arguments); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}
