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

// The fixtures follow the return shapes of TagManager.getContainer (the draft is version 8, see containerRow),
// addContainerTrigger (the new ID as a scalar), and updateContainerTrigger and deleteContainerTrigger (no value,
// which the API reports as a success).

const triggerWriteConnections = `
  tmt: {service: matomo, credential: token, tools: [matomo.tag-manager-triggers.add, matomo.tag-manager-triggers.update, matomo.tag-manager-triggers.delete], targets: [site]}
  tmt-two: {service: matomo, credential: token, tools: [matomo.tag-manager-triggers.add, matomo.tag-manager-triggers.update, matomo.tag-manager-triggers.delete], targets: [site/2]}
  tmt-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
`

func newTriggerWriteEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(triggerWriteConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

var triggerWriteTools = []struct{ id, method, arguments string }{
	{"matomo.tag-manager-triggers.add", "TagManager.addContainerTrigger", `{"idSite":1,"idContainer":"6OMh6taM","type":"PageView","name":"All pages"}`},
	{"matomo.tag-manager-triggers.update", "TagManager.updateContainerTrigger", `{"idSite":1,"idContainer":"6OMh6taM","idTrigger":4,"name":"All pages","parameters":{},"conditions":[],"description":""}`},
	{"matomo.tag-manager-triggers.delete", "TagManager.deleteContainerTrigger", `{"idSite":1,"idContainer":"6OMh6taM","idTrigger":4}`},
}

func triggerWriteAnswers(extra answers) answers {
	return tagManagerAnswers(with(answers{
		"TagManager.getContainer":           func(call) *http.Response { return ok(containerRow) },
		"TagManager.addContainerTrigger":    func(call) *http.Response { return ok(`{"value":12}`) },
		"TagManager.updateContainerTrigger": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"TagManager.deleteContainerTrigger": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func indexOfMethod(calls []call, method string) int {
	for i, c := range calls {
		if c.api() == method {
			return i
		}
	}
	return -1
}

func TestTagManagerTriggerWriteToolsAreListedOnlyConfirmedInNoProfileAndTakeNoVersion(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.tag-manager-triggers.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-triggers.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.tag-manager-triggers.delete": {capability.EffectDelete, capability.IdempotencyIdempotent},
	}
	for _, tool := range triggerWriteTools {
		d, _, found := reg.Lookup(tool.id)
		w := want[tool.id]
		if !found || d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != tagManagerSensitive || !d.RequiresToolAllowList {
			t.Fatalf("%s: found %t, %+v", tool.id, found, d)
		}
		if strings.Contains(string(d.InputSchema), "idContainerVersion") {
			t.Errorf("%s takes a version", tool.id)
		}
		for _, argument := range d.Arguments {
			if argument.Name == "idContainerVersion" {
				t.Errorf("%s documents a version argument", tool.id)
			}
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
	env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
	for _, tool := range triggerWriteTools {
		if _, err := env.change(tool.id, "tmt-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
		// A version is no argument: the schema refuses it before any request.
		withVersion := strings.TrimSuffix(tool.arguments, "}") + `,"idContainerVersion":7}`
		if _, err := env.change(tool.id, "tmt", withVersion); err == nil {
			t.Errorf("%s took a version", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTagManagerTriggerWriteReadsTheDraftOnceThenSendsOneConfirmedRequestIntoIt(t *testing.T) {
	for _, tool := range triggerWriteTools {
		var calls []call
		env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
		if _, err := env.invoke(tool.id, "tmt", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: an unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "tmt", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if countMethod(calls, "TagManager.getContainer") != 1 || countMethod(calls, tool.method) != 1 ||
			indexOfMethod(calls, "TagManager.getContainer") > indexOfMethod(calls, tool.method) {
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
		read := calls[indexOfMethod(calls, "TagManager.getContainer")]
		if read.form.Get("idSite") != "1" || read.form.Get("idContainer") != "6OMh6taM" || read.form.Has("idContainerVersion") {
			t.Errorf("%s: read = %v", tool.id, read.form)
		}
		sent := calls[indexOfMethod(calls, tool.method)]
		if sent.form.Get("idSite") != "1" || sent.form.Get("idContainer") != "6OMh6taM" ||
			sent.form.Get("idContainerVersion") != "8" || sent.form.Get("format") != "JSON" {
			t.Errorf("%s: request = %v", tool.id, sent.form)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "filter_limit"} {
			if sent.form.Has(name) {
				t.Errorf("%s carries %s", tool.id, name)
			}
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(result), &out)
		if out["idContainerVersion"] != float64(8) || out["idSite"] != float64(1) || out["idContainer"] != "6OMh6taM" {
			t.Errorf("%s: %s", tool.id, result)
		}
		switch tool.id {
		case "matomo.tag-manager-triggers.add":
			if out["created"] != true || out["idTrigger"] != float64(12) || sent.form.Get("type") != "PageView" ||
				sent.form.Get("name") != "All pages" || sent.form.Get("description") != "" || sent.form.Has("idTrigger") {
				t.Errorf("add: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-triggers.update":
			if out["updated"] != true || out["idTrigger"] != float64(4) || sent.form.Get("idTrigger") != "4" ||
				sent.form.Get("name") != "All pages" || !sent.form.Has("description") || sent.form.Has("type") {
				t.Errorf("update: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-triggers.delete":
			if out["deleted"] != true || out["idTrigger"] != float64(4) || sent.form.Get("idTrigger") != "4" {
				t.Errorf("delete: %s, %v", result, sent.form)
			}
		}
	}
}

func TestTagManagerTriggerWriteSerialisesParametersAndConditionsAsFormArrays(t *testing.T) {
	arguments := `{"idSite":1,"idContainer":"6OMh6taM","type":"CustomEvent","name":" Buy ","description":"d",` +
		`"parameters":{"eventName":"buy","delay":250,"ratio":1.5,"enabled":true,"off":false,"names":["a","b"],` +
		`"rows":[{"key":"k","value":1}]},` +
		`"conditions":[{"actual":"PagePath","comparison":"starts_with","expected":"/shop"},` +
		`{"actual":"MyVar","comparison":"equals","expected":"0"}]}`
	var calls []call
	env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-triggers.add", "tmt", arguments); err != nil {
		t.Fatal(err)
	}
	sent := calls[indexOfMethod(calls, "TagManager.addContainerTrigger")]
	want := map[string]string{
		"type": "CustomEvent", "name": " Buy ", "description": "d",
		"parameters[eventName]": "buy", "parameters[delay]": "250", "parameters[ratio]": "1.5",
		"parameters[enabled]": "1", "parameters[off]": "0", "parameters[names][0]": "a", "parameters[names][1]": "b",
		"parameters[rows][0][key]": "k", "parameters[rows][0][value]": "1",
		"conditions[0][actual]": "PagePath", "conditions[0][comparison]": "starts_with", "conditions[0][expected]": "/shop",
		"conditions[1][actual]": "MyVar", "conditions[1][comparison]": "equals", "conditions[1][expected]": "0",
	}
	known := map[string]bool{"module": true, "method": true, "format": true, "idSite": true, "idContainer": true,
		"idContainerVersion": true}
	for name, value := range want {
		if sent.form.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), value)
		}
	}
	for name := range sent.form {
		if _, listed := want[name]; !listed && !known[name] {
			t.Errorf("unexpected field %s", name)
		}
	}
}

func TestTagManagerTriggerWriteRefusesWithoutAnUsableDraftAndSendsNoWrite(t *testing.T) {
	foreign := strings.Replace(containerRow, `"idcontainer":"6OMh6taM","idsite":1,"context"`, `"idcontainer":"6OMh6taM","idsite":9,"context"`, 1)
	otherID := strings.Replace(containerRow, `"idcontainer":"6OMh6taM","idsite":1,"context"`, `"idcontainer":"bbbbbbbb","idsite":1,"context"`, 1)
	noDraft := containerRow[:strings.Index(containerRow, `,
"draft":`)] + `}`
	for name, body := range map[string]string{
		"draft empty list": strings.Replace(containerRow, `"draft":{`, `"draft":[{`, 1)[:0] + otherContainerRow("6OMh6taM", "x", 1),
		"no draft member":  noDraft,
		"draft is a saved": strings.Replace(containerRow, `"revision":0,"name":"draft",
 "description":"","created_date":"2026-01-04 03:04:05","updated_date":"2026-01-04 03:04:05"}}`, `"revision":2,"name":"draft",
 "description":"","created_date":"2026-01-04 03:04:05","updated_date":"2026-01-04 03:04:05"}}`, 1),
		"draft without ID":  strings.Replace(containerRow, `"draft":{"idcontainerversion":8,`, `"draft":{"idcontainerversion":0,`, 1),
		"another website":   foreign,
		"another container": otherID,
		"false":             `false`,
		"list":              `[]`,
		"not an object":     `"x"`,
	} {
		for _, tool := range triggerWriteTools {
			var calls []call
			env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{
				"TagManager.getContainer": func(call) *http.Response { return ok(body) },
			}).handler(t))
			_, err := env.change(tool.id, "tmt", tool.arguments)
			if err == nil || countMethod(calls, tool.method) != 0 || countMethod(calls, "TagManager.getContainer") != 1 {
				t.Errorf("%s %s: err = %v, writes = %d", tool.id, name, err, countMethod(calls, tool.method))
			}
		}
	}
}

func TestTagManagerTriggerWriteRefusesBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM"`
	manyParameters := make([]string, 101)
	for i := range manyParameters {
		manyParameters[i] = `"p` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":"x"`
	}
	manyConditions := strings.Repeat(`{"actual":"a","comparison":"equals","expected":"b"},`, 50) + `{"actual":"a","comparison":"equals","expected":"b"}`
	bigTexts := `"parameters":{` + strings.Repeat(`"k":"`+strings.Repeat("x", 100)+`",`, 0) + func() string {
		var parts []string
		for i := 0; i < 20; i++ {
			parts = append(parts, `"p`+string(rune('a'+i))+`":"`+strings.Repeat("x", 16384)+`"`)
		}
		return strings.Join(parts, ",")
	}() + `}`
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign website add":     {"add", "tmt-two", `{` + base + `,"type":"PageView","name":"n"}`},
		"foreign website update":  {"update", "tmt-two", `{` + base + `,"idTrigger":4,"name":"n","parameters":{},"conditions":[],"description":""}`},
		"foreign website delete":  {"delete", "tmt-two", `{"idSite":3,"idContainer":"6OMh6taM","idTrigger":4}`},
		"no website":              {"add", "tmt", `{"idContainer":"6OMh6taM","type":"PageView","name":"n"}`},
		"bad container":           {"delete", "tmt", `{"idSite":1,"idContainer":"a b","idTrigger":4}`},
		"missing type":            {"add", "tmt", `{` + base + `,"name":"n"}`},
		"bad type":                {"add", "tmt", `{` + base + `,"type":"Page View","name":"n"}`},
		"bad type quote":          {"add", "tmt", `{` + base + `,"type":"a\"b","name":"n"}`},
		"missing name":            {"add", "tmt", `{` + base + `,"type":"PageView"}`},
		"empty name":              {"add", "tmt", `{` + base + `,"type":"PageView","name":""}`},
		"blank name":              {"update", "tmt", `{` + base + `,"idTrigger":4,"name":"  ","parameters":{},"conditions":[],"description":""}`},
		"long name":               {"add", "tmt", `{` + base + `,"type":"PageView","name":"` + strings.Repeat("ä", 256) + `"}`},
		"long description":        {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","description":"` + strings.Repeat("x", 1001) + `"}`},
		"update without trigger":  {"update", "tmt", `{` + base + `,"name":"n","parameters":{},"conditions":[],"description":""}`},
		"update without text":     {"update", "tmt", `{` + base + `,"idTrigger":4,"name":"n","parameters":{},"conditions":[]}`},
		"update without params":   {"update", "tmt", `{` + base + `,"idTrigger":4,"name":"n","conditions":[],"description":""}`},
		"update without cond":     {"update", "tmt", `{` + base + `,"idTrigger":4,"name":"n","parameters":{},"description":""}`},
		"update null params":      {"update", "tmt", `{` + base + `,"idTrigger":4,"name":"n","parameters":null,"conditions":[],"description":""}`},
		"delete without trigger":  {"delete", "tmt", `{` + base + `}`},
		"delete trigger zero":     {"delete", "tmt", `{` + base + `,"idTrigger":0}`},
		"delete trigger text":     {"delete", "tmt", `{` + base + `,"idTrigger":"4"}`},
		"extra argument":          {"delete", "tmt", `{` + base + `,"idTrigger":4,"method":"TagManager.publishContainerVersion"}`},
		"parameters not object":   {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":["a"]}`},
		"parameter null":          {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":null}}`},
		"parameter object":        {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":{"b":"c"}}}`},
		"parameter name":          {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a[0]":"c"}}`},
		"parameter empty list":    {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":[]}}`},
		"parameter nested list":   {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":[["x"]]}}`},
		"parameter deep row":      {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":[{"b":["x"]}]}}`},
		"parameter cell name":     {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":[{"b c":"x"}]}}`},
		"parameter long text":     {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{"a":"` + strings.Repeat("x", 16385) + `"}}`},
		"parameters too many":     {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","parameters":{` + strings.Join(manyParameters, ",") + `}}`},
		"texts too long together": {"add", "tmt", `{` + base + `,"type":"PageView","name":"n",` + bigTexts + `}`},
		"conditions too many":     {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[` + manyConditions + `]}`},
		"conditions not list":     {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":{"actual":"a"}}`},
		"condition missing":       {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[{"actual":"a","comparison":"equals"}]}`},
		"condition empty":         {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[{"actual":"a","comparison":"equals","expected":""}]}`},
		"condition extra":         {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[{"actual":"a","comparison":"equals","expected":"b","x":"y"}]}`},
		"condition comparison":    {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[{"actual":"a","comparison":"Equals ","expected":"b"}]}`},
		"condition number":        {"add", "tmt", `{` + base + `,"type":"PageView","name":"n","conditions":[{"actual":"a","comparison":"equals","expected":5}]}`},
	} {
		var calls []call
		env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-triggers."+tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "publishContainerVersion")) {
			t.Errorf("%s: the refusal quotes the value: %v", name, err)
		}
	}
	// Name, description, and a condition at their caps pass.
	var calls []call
	env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-triggers.add", "tmt", `{`+base+`,"type":"PageView","name":"`+
		strings.Repeat("ä", 255)+`","description":"`+strings.Repeat("x", 1000)+`","conditions":[{"actual":"a",`+
		`"comparison":"equals","expected":"`+strings.Repeat("x", 16384)+`"}]}`); err != nil {
		t.Errorf("values at the caps: %v", err)
	}
}

func TestTagManagerTriggerWriteIsUnsupportedWithoutThePluginAndSendsNoMethod(t *testing.T) {
	for _, tool := range triggerWriteTools {
		var calls []call
		env := newTriggerWriteEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response { return ok(`{"value":false}`) },
		}).handler(t))
		if _, err := env.change(tool.id, "tmt", tool.arguments); !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerTriggerWriteMapsErrorsWithoutTheMessageAndNeverRepeats(t *testing.T) {
	for _, tool := range triggerWriteTools {
		// The same texts come from the evidence read and from the write; neither may repeat or quote them.
		for _, method := range []string{"TagManager.getContainer", tool.method} {
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
				env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{
					method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
				}).handler(t))
				_, err := env.change(tool.id, "tmt", tool.arguments)
				if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
					isUnsupported(err) != tc.missing || classOf(err) != tc.class ||
					countMethod(calls, method) != 1 || (method != tool.method && countMethod(calls, tool.method) != 0) {
					t.Errorf("%s %s %s: err = %v (class %q)", tool.id, method, name, err, classOf(err))
				}
			}
		}
	}
}

func TestTagManagerTriggerWriteRefusalsAreClearAndKeepNoProviderText(t *testing.T) {
	for _, tc := range []struct {
		tool    int
		message string
		want    string
	}{
		{0, "This name is already in use.", "another trigger"},
		{1, "This name is already in use.", "another trigger"},
		{2, "This trigger cannot be deleted as it is used in a tag. To remove this trigger, update any referenced tag first.", "tag of the container version"},
		{0, `The trigger "PageViewX" is not supported`, "trigger type"},
		{0, `The variable "MyVar" in the condition at position "0" cannot be found.`, "condition"},
		{1, `The comparison "token" is not supported.`, "condition"},
		{0, `Missing value for array key "expected" in "Conditions" at position "1".`, "condition"},
	} {
		tool := triggerWriteTools[tc.tool]
		var calls []call
		env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{
			tool.method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
		}).handler(t))
		_, err := env.change(tool.id, "tmt", tool.arguments)
		if err == nil || !isInvalidRequest(err) || classOf(err) == provider.ClassInvalidResponse ||
			strings.Contains(err.Error(), "never repeated") || strings.Contains(err.Error(), bodyCanary) ||
			strings.Contains(err.Error(), "MyVar") || strings.Contains(err.Error(), "PageViewX") ||
			!strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), tc.want) ||
			countMethod(calls, tool.method) != 1 {
			t.Errorf("%s %q: err = %v, sent %d times", tool.id, tc.message, err, countMethod(calls, tool.method))
		}
	}
}

func TestTagManagerTriggerWriteUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
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
	for _, tool := range triggerWriteTools {
		for name, outcome := range outcomes {
			var calls []call
			env := newTriggerWriteEnvironment(t, &calls, func(c call) (*http.Response, error) {
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
			_, err := env.change(tool.id, "tmt", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 ||
				countMethod(calls, "TagManager.getContainer") != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", tool.id, name, err, countMethod(calls, tool.method))
				continue
			}
			if name != "429 without Retry-After" && !strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: no uncertainty message: %v", tool.id, name, err)
			}
			if name != "429 without Retry-After" && !strings.Contains(err.Error(), "matomo.tag-manager-triggers.list") {
				t.Errorf("%s %s: no hint for the read: %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerTriggerAddWithoutAnIDIsUnclearAndNotRepeated(t *testing.T) {
	for name, body := range map[string]string{"none": `{"result":"success"}`, "zero": `{"value":0}`, "text": `{"value":"x"}`} {
		var calls []call
		env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{
			"TagManager.addContainerTrigger": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := env.change("matomo.tag-manager-triggers.add", "tmt", triggerWriteTools[0].arguments)
		if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls, "TagManager.addContainerTrigger") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerTriggerWriteTokenIsInTheRedactor(t *testing.T) {
	var calls []call
	env := newTriggerWriteEnvironment(t, &calls, triggerWriteAnswers(answers{}).handler(t))
	if _, err := env.change(triggerWriteTools[2].id, "tmt", triggerWriteTools[2].arguments); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}
