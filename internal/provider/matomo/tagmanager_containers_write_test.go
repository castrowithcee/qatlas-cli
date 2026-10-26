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

// The fixtures follow the return shapes of TagManager.addContainer (the new ID as a scalar),
// createDefaultContainerForSite (the ID or no value), updateContainer and deleteContainer (no value, which the API
// reports as a success), and getContainer for the evidence read.

const containerWriteTools = "[matomo.tag-manager-containers.create-default, matomo.tag-manager-containers.add, " +
	"matomo.tag-manager-containers.update, matomo.tag-manager-containers.delete]"

const containerWriteConnections = `
  tmc: {service: matomo, credential: token, tools: ` + containerWriteTools + `, targets: [site]}
  tmc-two: {service: matomo, credential: token, tools: ` + containerWriteTools + `, targets: [site/2]}
  tmc-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
`

func newContainerWriteEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(containerWriteConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

const flagsOff = `"ignoreGtmDataLayer":false,"isTagFireLimitAllowedInPreviewMode":false,"activelySyncGtmDataLayer":false`

var containerWriteToolList = []struct {
	id, method, arguments string
	proved                bool
}{
	{"matomo.tag-manager-containers.create-default", "TagManager.createDefaultContainerForSite", `{"idSite":1}`, false},
	{"matomo.tag-manager-containers.add", "TagManager.addContainer", `{"idSite":1,"context":"web","name":"Main"}`, false},
	{"matomo.tag-manager-containers.update", "TagManager.updateContainer",
		`{"idSite":1,"idContainer":"6OMh6taM","name":"Main","description":"",` + flagsOff + `}`, true},
	{"matomo.tag-manager-containers.delete", "TagManager.deleteContainer", `{"idSite":1,"idContainer":"6OMh6taM"}`, true},
}

func containerWriteAnswers(extra answers) answers {
	return tagManagerAnswers(with(answers{
		"TagManager.getContainer":                  func(call) *http.Response { return ok(containerRow) },
		"TagManager.createDefaultContainerForSite": func(call) *http.Response { return ok(`{"value":"Ab3dEf9h"}`) },
		"TagManager.addContainer":                  func(call) *http.Response { return ok(`{"value":"Zy9xWv8u"}`) },
		"TagManager.updateContainer":               func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"TagManager.deleteContainer":               func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func TestTagManagerContainerWriteToolsAreListedOnlyConfirmedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.tag-manager-containers.create-default": {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-containers.add":            {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-containers.update":         {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.tag-manager-containers.delete":         {capability.EffectDelete, capability.IdempotencyIdempotent},
	}
	for _, tool := range containerWriteToolList {
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
	var calls []call
	env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
	for _, tool := range containerWriteToolList {
		if _, err := env.change(tool.id, "tmc-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTagManagerContainerWriteSendsOneConfirmedRequestAndProvesTheContainerFirst(t *testing.T) {
	for _, tool := range containerWriteToolList {
		var calls []call
		env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
		if _, err := env.invoke(tool.id, "tmc", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: an unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "tmc", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		wantReads := 0
		if tool.proved {
			wantReads = 1
		}
		if countMethod(calls, "TagManager.getContainer") != wantReads || countMethod(calls, tool.method) != 1 ||
			(tool.proved && indexOfMethod(calls, "TagManager.getContainer") > indexOfMethod(calls, tool.method)) {
			t.Fatalf("%s: read %d times, wrote %d times", tool.id, countMethod(calls, "TagManager.getContainer"), countMethod(calls, tool.method))
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") && c.api() != tool.method && c.api() != "TagManager.getContainer" {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
			if strings.HasPrefix(c.api(), "TagManager.") && (c.auth != "Bearer "+tokenValue || c.form.Has("token_auth") ||
				strings.Contains(c.rawQuery+c.path, tokenValue) || c.method != http.MethodPost) {
				t.Errorf("%s: token handling: %+v", tool.id, c)
			}
		}
		sent := calls[indexOfMethod(calls, tool.method)]
		if sent.form.Get("idSite") != "1" || sent.form.Get("format") != "JSON" {
			t.Errorf("%s: request = %v", tool.id, sent.form)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "filter_limit", "idContainerVersion"} {
			if sent.form.Has(name) {
				t.Errorf("%s carries %s", tool.id, name)
			}
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(result), &out)
		if out["idSite"] != float64(1) {
			t.Errorf("%s: %s", tool.id, result)
		}
		switch tool.id {
		case "matomo.tag-manager-containers.create-default":
			if out["created"] != true || out["idContainer"] != "Ab3dEf9h" || len(sent.form) != 4 {
				t.Errorf("create-default: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-containers.add":
			if out["created"] != true || out["idContainer"] != "Zy9xWv8u" || sent.form.Get("context") != "web" ||
				sent.form.Get("name") != "Main" || sent.form.Get("description") != "" ||
				sent.form.Get("ignoreGtmDataLayer") != "0" || sent.form.Get("isTagFireLimitAllowedInPreviewMode") != "0" ||
				sent.form.Get("activelySyncGtmDataLayer") != "0" || sent.form.Has("idContainer") {
				t.Errorf("add: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-containers.update":
			if out["updated"] != true || out["idContainer"] != "6OMh6taM" || sent.form.Get("idContainer") != "6OMh6taM" ||
				sent.form.Get("name") != "Main" || !sent.form.Has("description") || sent.form.Has("context") {
				t.Errorf("update: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-containers.delete":
			if out["deleted"] != true || out["idContainer"] != "6OMh6taM" || sent.form.Get("idContainer") != "6OMh6taM" {
				t.Errorf("delete: %s, %v", result, sent.form)
			}
		}
	}
}

func TestTagManagerContainerWriteSerialisesTheSettings(t *testing.T) {
	var calls []call
	env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-containers.add", "tmc", `{"idSite":1,"context":"android","name":"App",`+
		`"description":"d","ignoreGtmDataLayer":true,"isTagFireLimitAllowedInPreviewMode":true,"activelySyncGtmDataLayer":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.change("matomo.tag-manager-containers.update", "tmc", `{"idSite":1,"idContainer":"6OMh6taM","name":"App",`+
		`"description":"d","ignoreGtmDataLayer":true,"isTagFireLimitAllowedInPreviewMode":false,"activelySyncGtmDataLayer":true}`); err != nil {
		t.Fatal(err)
	}
	add := calls[indexOfMethod(calls, "TagManager.addContainer")]
	update := calls[indexOfMethod(calls, "TagManager.updateContainer")]
	for name, want := range map[string]string{"context": "android", "name": "App", "description": "d",
		"ignoreGtmDataLayer": "1", "isTagFireLimitAllowedInPreviewMode": "1", "activelySyncGtmDataLayer": "1"} {
		if add.form.Get(name) != want {
			t.Errorf("add %s = %q, want %q", name, add.form.Get(name), want)
		}
	}
	for name, want := range map[string]string{"ignoreGtmDataLayer": "1", "isTagFireLimitAllowedInPreviewMode": "0",
		"activelySyncGtmDataLayer": "1", "description": "d"} {
		if update.form.Get(name) != want {
			t.Errorf("update %s = %q, want %q", name, update.form.Get(name), want)
		}
	}
}

func TestTagManagerContainerCreateDefaultAcceptsASuccessWithoutID(t *testing.T) {
	var calls []call
	env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{
		"TagManager.createDefaultContainerForSite": func(call) *http.Response { return ok(`{"result":"success"}`) },
	}).handler(t))
	result, err := env.change("matomo.tag-manager-containers.create-default", "tmc", `{"idSite":1}`)
	if err != nil || result != `{"created":true,"idSite":1}` {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestTagManagerContainerWriteRefusesAnotherWebsiteOrContainerAndSendsNoWrite(t *testing.T) {
	foreign := strings.Replace(containerRow, `"idcontainer":"6OMh6taM","idsite":1,"context"`, `"idcontainer":"6OMh6taM","idsite":9,"context"`, 1)
	otherID := strings.Replace(containerRow, `"idcontainer":"6OMh6taM","idsite":1,"context"`, `"idcontainer":"bbbbbbbb","idsite":1,"context"`, 1)
	for name, body := range map[string]string{"another website": foreign, "another container": otherID, "false": `false`,
		"list": `[]`, "not an object": `"x"`} {
		for _, tool := range containerWriteToolList {
			if !tool.proved {
				continue
			}
			var calls []call
			env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{
				"TagManager.getContainer": func(call) *http.Response { return ok(body) },
			}).handler(t))
			_, err := env.change(tool.id, "tmc", tool.arguments)
			if err == nil || countMethod(calls, tool.method) != 0 || countMethod(calls, "TagManager.getContainer") != 1 {
				t.Errorf("%s %s: err = %v, writes = %d", tool.id, name, err, countMethod(calls, tool.method))
			}
		}
	}
}

func TestTagManagerContainerWriteRefusesBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM"`
	const settings = `"name":"n","description":"",` + flagsOff
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign website create-default": {"create-default", "tmc-two", `{"idSite":1}`},
		"foreign website add":            {"add", "tmc-two", `{"idSite":3,"context":"web","name":"n"}`},
		"foreign website update":         {"update", "tmc-two", `{"idSite":1,"idContainer":"6OMh6taM",` + settings + `}`},
		"foreign website delete":         {"delete", "tmc-two", `{"idSite":3,"idContainer":"6OMh6taM"}`},
		"no website":                     {"add", "tmc", `{"context":"web","name":"n"}`},
		"add without context":            {"add", "tmc", `{"idSite":1,"name":"n"}`},
		"add bad context":                {"add", "tmc", `{"idSite":1,"context":"We b","name":"n"}`},
		"add without name":               {"add", "tmc", `{"idSite":1,"context":"web"}`},
		"add empty name":                 {"add", "tmc", `{"idSite":1,"context":"web","name":""}`},
		"add blank name":                 {"add", "tmc", `{"idSite":1,"context":"web","name":"  "}`},
		"add long name":                  {"add", "tmc", `{"idSite":1,"context":"web","name":"` + strings.Repeat("ä", 51) + `"}`},
		"add long description":           {"add", "tmc", `{"idSite":1,"context":"web","name":"n","description":"` + strings.Repeat("x", 1001) + `"}`},
		"add flag text":                  {"add", "tmc", `{"idSite":1,"context":"web","name":"n","ignoreGtmDataLayer":"1"}`},
		"add container":                  {"add", "tmc", `{"idSite":1,"context":"web","name":"n","idContainer":"6OMh6taM"}`},
		"bad container":                  {"delete", "tmc", `{"idSite":1,"idContainer":"a b"}`},
		"delete without container":       {"delete", "tmc", `{"idSite":1}`},
		"update without name":            {"update", "tmc", `{` + base + `,"description":"",` + flagsOff + `}`},
		"update without description":     {"update", "tmc", `{` + base + `,"name":"n",` + flagsOff + `}`},
		"update without flag":            {"update", "tmc", `{` + base + `,"name":"n","description":"","ignoreGtmDataLayer":false,"activelySyncGtmDataLayer":false}`},
		"update null flag":               {"update", "tmc", `{` + base + `,"name":"n","description":"","ignoreGtmDataLayer":null,"isTagFireLimitAllowedInPreviewMode":false,"activelySyncGtmDataLayer":false}`},
		"update context":                 {"update", "tmc", `{` + base + `,"context":"web",` + settings + `}`},
		"extra argument":                 {"delete", "tmc", `{` + base + `,"method":"TagManager.publishContainerVersion"}`},
		"create-default extra":           {"create-default", "tmc", `{"idSite":1,"name":"n"}`},
	} {
		var calls []call
		env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-containers."+tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "publishContainerVersion")) {
			t.Errorf("%s: the refusal quotes the value: %v", name, err)
		}
	}
	var calls []call
	env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-containers.add", "tmc", `{"idSite":1,"context":"web","name":"`+
		strings.Repeat("ä", 50)+`","description":"`+strings.Repeat("x", 1000)+`"}`); err != nil {
		t.Errorf("values at the caps: %v", err)
	}
}

func TestTagManagerContainerWriteIsUnsupportedWithoutThePluginAndSendsNoMethod(t *testing.T) {
	for _, tool := range containerWriteToolList {
		var calls []call
		env := newContainerWriteEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response { return ok(`{"value":false}`) },
		}).handler(t))
		if _, err := env.change(tool.id, "tmc", tool.arguments); !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerContainerWriteMapsErrorsWithoutTheMessageAndNeverRepeats(t *testing.T) {
	for _, tool := range containerWriteToolList {
		methods := []string{tool.method}
		if tool.proved {
			methods = append(methods, "TagManager.getContainer")
		}
		for _, method := range methods {
			for name, tc := range map[string]struct {
				message string
				class   provider.Class
				missing bool
			}{
				"permission":        {"You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
				"publish right":     {"You are not allowed to publish this container.", provider.ClassPermission, false},
				"container missing": {`The requested container "token123" does not exist.`, provider.ClassNotFound, false},
				"site not found":    {"An unexpected website was found in the request: website id = 1 not found.", provider.ClassNotFound, false},
				"method missing":    {"The method 'TagManager.x' does not exist or is not available in the module 'TagManager'.", "", true},
				"plugin off":        {"The plugin TagManager is not activated.", "", true},
				"other":             {"Something went wrong", provider.ClassInvalidResponse, false},
			} {
				var calls []call
				env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{
					method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
				}).handler(t))
				_, err := env.change(tool.id, "tmc", tool.arguments)
				if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
					isUnsupported(err) != tc.missing || classOf(err) != tc.class ||
					countMethod(calls, method) != 1 || (method != tool.method && countMethod(calls, tool.method) != 0) {
					t.Errorf("%s %s %s: err = %v (class %q)", tool.id, method, name, err, classOf(err))
				}
			}
		}
	}
}

func TestTagManagerContainerWriteUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
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
	for _, tool := range containerWriteToolList {
		for name, outcome := range outcomes {
			var calls []call
			env := newContainerWriteEnvironment(t, &calls, func(c call) (*http.Response, error) {
				switch c.api() {
				case "API.getMatomoVersion":
					return ok(versionAnswer), nil
				case "API.isPluginActivated":
					return ok(`{"value":true}`), nil
				case "TagManager.getContainer":
					return ok(containerRow), nil
				}
				return outcome(c)
			})
			_, err := env.change(tool.id, "tmc", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", tool.id, name, err, countMethod(calls, tool.method))
				continue
			}
			if name != "429 without Retry-After" && (!strings.Contains(err.Error(), "never repeated") ||
				!strings.Contains(err.Error(), "matomo.tag-manager-containers.list")) {
				t.Errorf("%s %s: no uncertainty message or read hint: %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerContainerAddWithoutAnIDIsUnclearAndNotRepeated(t *testing.T) {
	for name, body := range map[string]string{"none": `{"result":"success"}`, "number": `{"value":0}`, "bad ID": `{"value":"a b"}`} {
		var calls []call
		env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{
			"TagManager.addContainer": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := env.change("matomo.tag-manager-containers.add", "tmc", containerWriteToolList[1].arguments)
		if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls, "TagManager.addContainer") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerContainerWriteTokenIsInTheRedactor(t *testing.T) {
	var calls []call
	env := newContainerWriteEnvironment(t, &calls, containerWriteAnswers(answers{}).handler(t))
	if _, err := env.change(containerWriteToolList[3].id, "tmc", containerWriteToolList[3].arguments); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}
