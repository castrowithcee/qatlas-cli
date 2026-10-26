package matomo

import (
	"context"
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
// getContainerVariable (a database row with parameters as an object), addContainerVariable (the new ID as a
// scalar), and updateContainerVariable and deleteContainerVariable (no value, which the API reports as a success).

const variableWriteConnections = `
  tmv: {service: matomo, credential: token, tools: [matomo.tag-manager-variables.add, matomo.tag-manager-variables.update, matomo.tag-manager-variables.delete], targets: [site]}
  tmv-two: {service: matomo, credential: token, tools: [matomo.tag-manager-variables.add, matomo.tag-manager-variables.update, matomo.tag-manager-variables.delete], targets: [site/2]}
  tmv-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
`

func newVariableWriteEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(variableWriteConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func storedVariable(variableType, parameters string) string {
	return `{"idvariable":4,"idsite":1,"idcontainerversion":8,"type":"` + variableType + `","name":"Stored","description":"",` +
		`"parameters":` + parameters + `,"default_value":"","lookup_table":[]}`
}

var variableWriteTools = []struct{ id, method, arguments string }{
	{"matomo.tag-manager-variables.add", "TagManager.addContainerVariable", `{"idSite":1,"idContainer":"6OMh6taM","type":"Constant",` +
		`"name":"Shop name","parameters":{"constantValue":"Example Shop"},"defaultValue":"none",` +
		`"lookupTable":[{"matchValue":"a","comparison":"equals","outValue":"b"}],"description":"d"}`},
	{"matomo.tag-manager-variables.update", "TagManager.updateContainerVariable", `{"idSite":1,"idContainer":"6OMh6taM","idVariable":4,` +
		`"name":"Shop name","parameters":{"constantValue":"Example Shop"},"defaultValue":"","lookupTable":[],"description":""}`},
	{"matomo.tag-manager-variables.delete", "TagManager.deleteContainerVariable", `{"idSite":1,"idContainer":"6OMh6taM","idVariable":4}`},
}

func variableWriteAnswers(extra answers) answers {
	return tagManagerAnswers(with(answers{
		"TagManager.getContainer":         func(call) *http.Response { return ok(containerRow) },
		"TagManager.getContainerVariable": func(call) *http.Response { return ok(storedVariable("Constant", `{"constantValue":"x"}`)) },
		"TagManager.addContainerVariable": func(call) *http.Response { return ok(`{"value":12}`) },
		"TagManager.updateContainerVariable": func(call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		},
		"TagManager.deleteContainerVariable": func(call) *http.Response {
			return ok(`{"result":"success","message":"ok"}`)
		},
	}, extra))
}

func noVariableMutation(calls []call) bool {
	for _, tool := range variableWriteTools {
		if countMethod(calls, tool.method) != 0 {
			return false
		}
	}
	return true
}

func configArguments(tool, parameters string) string {
	if tool == "add" {
		return `{"idSite":1,"idContainer":"6OMh6taM","type":"MatomoConfiguration","name":"Main","parameters":` + parameters + `}`
	}
	return `{"idSite":1,"idContainer":"6OMh6taM","idVariable":4,"name":"Main","parameters":` + parameters +
		`,"defaultValue":"","lookupTable":[],"description":""}`
}

func TestTagManagerVariableWriteToolsAreListedOnlyConfirmedInNoProfileAndTakeNoVersion(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.tag-manager-variables.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-variables.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.tag-manager-variables.delete": {capability.EffectDelete, capability.IdempotencyIdempotent},
	}
	for _, tool := range variableWriteTools {
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
	env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
	for _, tool := range variableWriteTools {
		if _, err := env.change(tool.id, "tmv-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
		withVersion := strings.TrimSuffix(tool.arguments, "}") + `,"idContainerVersion":7}`
		if _, err := env.change(tool.id, "tmv", withVersion); err == nil {
			t.Errorf("%s took a version", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTagManagerVariableWriteReadsEvidenceThenSendsOneConfirmedRequestIntoTheDraft(t *testing.T) {
	for _, tool := range variableWriteTools {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
		if _, err := env.invoke(tool.id, "tmv", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: an unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "tmv", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		wantRead := 0
		if tool.id == "matomo.tag-manager-variables.update" {
			wantRead = 1
		}
		if countMethod(calls, "TagManager.getContainer") != 1 || countMethod(calls, tool.method) != 1 ||
			countMethod(calls, "TagManager.getContainerVariable") != wantRead ||
			indexOfMethod(calls, "TagManager.getContainer") > indexOfMethod(calls, tool.method) {
			t.Fatalf("%s: unexpected calls %d", tool.id, len(calls))
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") && (c.auth != "Bearer "+tokenValue || c.form.Has("token_auth") ||
				strings.Contains(c.rawQuery+c.path, tokenValue) || c.method != http.MethodPost) {
				t.Errorf("%s: token handling: %+v", tool.id, c)
			}
		}
		sent := calls[indexOfMethod(calls, tool.method)]
		if sent.form.Get("idSite") != "1" || sent.form.Get("idContainer") != "6OMh6taM" ||
			sent.form.Get("idContainerVersion") != "8" {
			t.Errorf("%s: request = %v", tool.id, sent.form)
		}
		switch tool.id {
		case "matomo.tag-manager-variables.add":
			f := sent.form
			if !strings.Contains(result, `"idVariable":12`) || !strings.Contains(result, `"created":true`) ||
				f.Get("type") != "Constant" || f.Get("name") != "Shop name" || f.Get("defaultValue") != "none" ||
				f.Get("parameters[constantValue]") != "Example Shop" || f.Get("lookupTable[0][matchValue]") != "a" ||
				f.Get("lookupTable[0][comparison]") != "equals" || f.Get("lookupTable[0][outValue]") != "b" ||
				f.Get("description") != "d" || f.Has("idVariable") {
				t.Errorf("add: %s, %v", result, f)
			}
		case "matomo.tag-manager-variables.update":
			read := calls[indexOfMethod(calls, "TagManager.getContainerVariable")]
			if !strings.Contains(result, `"updated":true`) || sent.form.Get("idVariable") != "4" || sent.form.Has("type") ||
				!sent.form.Has("defaultValue") || !sent.form.Has("description") || read.form.Get("idContainerVersion") != "8" ||
				read.form.Get("idVariable") != "4" || indexOfMethod(calls, "TagManager.getContainerVariable") > indexOfMethod(calls, tool.method) {
				t.Errorf("update: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-variables.delete":
			if !strings.Contains(result, `"deleted":true`) || sent.form.Get("idVariable") != "4" {
				t.Errorf("delete: %s, %v", result, sent.form)
			}
		}
	}
}

func TestTagManagerVariableAddRefusesBlockedTypesBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM","name":"n"`
	const row = `{"matchValue":"a","comparison":"equals","outValue":"b"}`
	capRows := strings.TrimSuffix(strings.Repeat(row+",", 100), ",")
	manyRows := capRows + "," + row
	for name, tc := range map[string]struct{ connection, arguments string }{
		"custom js function":      {"tmv", `{` + base + `,"type":"CustomJsFunction","parameters":{"jsFunction":"function(){return 1}"}}`},
		"custom request process":  {"tmv", `{` + base + `,"type":"CustomRequestProcessing"}`},
		"blocked in lower case":   {"tmv", `{` + base + `,"type":"customjsfunction"}`},
		"missing type":            {"tmv", `{` + base + `}`},
		"bad type":                {"tmv", `{` + base + `,"type":"Con stant"}`},
		"foreign website":         {"tmv-two", `{` + base + `,"type":"Constant"}`},
		"no website":              {"tmv", `{"idContainer":"6OMh6taM","name":"n","type":"Constant"}`},
		"empty name":              {"tmv", `{"idSite":1,"idContainer":"6OMh6taM","name":"","type":"Constant"}`},
		"long description":        {"tmv", `{` + base + `,"type":"Constant","description":"` + strings.Repeat("x", 1001) + `"}`},
		"id with add":             {"tmv", `{` + base + `,"type":"Constant","idVariable":4}`},
		"parameter name":          {"tmv", `{` + base + `,"type":"Constant","parameters":{"a[0]":"c"}}`},
		"parameter long":          {"tmv", `{` + base + `,"type":"Constant","parameters":{"a":"` + strings.Repeat("x", 16385) + `"}}`},
		"default long":            {"tmv", `{` + base + `,"type":"Constant","defaultValue":"` + strings.Repeat("x", 16385) + `"}`},
		"lookup too many":         {"tmv", `{` + base + `,"type":"Constant","lookupTable":[` + manyRows + `]}`},
		"lookup not list":         {"tmv", `{` + base + `,"type":"Constant","lookupTable":{"matchValue":"a"}}`},
		"lookup missing":          {"tmv", `{` + base + `,"type":"Constant","lookupTable":[{"matchValue":"a","comparison":"equals"}]}`},
		"lookup extra":            {"tmv", `{` + base + `,"type":"Constant","lookupTable":[{"matchValue":"a","comparison":"equals","outValue":"b","x":"y"}]}`},
		"lookup comparison":       {"tmv", `{` + base + `,"type":"Constant","lookupTable":[{"matchValue":"a","comparison":"Equals ","outValue":"b"}]}`},
		"lookup number":           {"tmv", `{` + base + `,"type":"Constant","lookupTable":[{"matchValue":"a","comparison":"equals","outValue":5}]}`},
		"passthrough":             {"tmv", `{` + base + `,"type":"Constant","method":"TagManager.publishContainerVersion"}`},
		"config without params":   {"tmv", `{` + base + `,"type":"MatomoConfiguration"}`},
		"config lower case":       {"tmv", `{` + base + `,"type":"matomoconfiguration","parameters":{"matomoUrl":"https://evil.example.org/","idSite":"1"}}`},
		"config foreign host":     {"tmv", configArguments("add", `{"matomoUrl":"https://evil.example.org/matomo/","idSite":"1"}`)},
		"config other path":       {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/other/","idSite":"1"}`)},
		"config http":             {"tmv", configArguments("add", `{"matomoUrl":"http://analytics.example.test/matomo/","idSite":"1"}`)},
		"config reference url":    {"tmv", configArguments("add", `{"matomoUrl":"{{TrackerUrl}}","idSite":"1"}`)},
		"config embedded ref":     {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/{{Path}}/","idSite":"1"}`)},
		"config no url":           {"tmv", configArguments("add", `{"idSite":"1"}`)},
		"config foreign site":     {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"2"}`)},
		"config reference site":   {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"{{Site}}"}`)},
		"config no site":          {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/"}`)},
		"config flag url":         {"tmv", configArguments("add", `{"matomoUrl":true,"idSite":"1"}`)},
		"config api url":          {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"https://evil.example.org/"}`)},
		"config request code":     {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","customRequestProcessing":"{{Code}}"}`)},
		"config endpoint url":     {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpoint":"https://evil.example.org/x.php"}`)},
		"config endpoint scheme":  {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpointCustom":"//evil.example.org/x.php"}`)},
		"config endpoint up":      {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","jsEndpointCustom":"../x.js"}`)},
		"config endpoint ref":     {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","jsEndpoint":"{{Path}}"}`)},
		"config endpoint at sign": {"tmv", configArguments("add", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","jsEndpoint":"@evil.example.org/"}`)},
	} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-variables.add", tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "evil.example.org") || strings.Contains(err.Error(), "site/") ||
			strings.Contains(err.Error(), "publishContainerVersion")) {
			t.Errorf("%s: the refusal quotes a value: %v", name, err)
		}
	}
	// Values at the caps pass.
	var calls []call
	env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-variables.add", "tmv", `{`+base+`,"type":"Constant","defaultValue":"`+
		strings.Repeat("x", 16384)+`","description":"`+strings.Repeat("x", 1000)+`","lookupTable":[`+capRows+`]}`); err != nil {
		t.Errorf("values at the caps: %v", err)
	}
}

func TestTagManagerVariableUpdateAndDeleteRefuseBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM","idVariable":4`
	const full = `"name":"n","parameters":{},"defaultValue":"","lookupTable":[],"description":""`
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign update":         {"update", "tmv-two", `{` + base + `,` + full + `}`},
		"foreign delete":         {"delete", "tmv-two", `{` + base + `}`},
		"update without id":      {"update", "tmv", `{"idSite":1,"idContainer":"6OMh6taM",` + full + `}`},
		"update id zero":         {"update", "tmv", `{"idSite":1,"idContainer":"6OMh6taM","idVariable":0,` + full + `}`},
		"update with type":       {"update", "tmv", `{` + base + `,"type":"Constant",` + full + `}`},
		"update without params":  {"update", "tmv", `{` + base + `,"name":"n","defaultValue":"","lookupTable":[],"description":""}`},
		"update null params":     {"update", "tmv", `{` + base + `,"name":"n","parameters":null,"defaultValue":"","lookupTable":[],"description":""}`},
		"update without default": {"update", "tmv", `{` + base + `,"name":"n","parameters":{},"lookupTable":[],"description":""}`},
		"update without lookup":  {"update", "tmv", `{` + base + `,"name":"n","parameters":{},"defaultValue":"","description":""}`},
		"update without text":    {"update", "tmv", `{` + base + `,"name":"n","parameters":{},"defaultValue":"","lookupTable":[]}`},
		"update null lookup":     {"update", "tmv", `{` + base + `,"name":"n","parameters":{},"defaultValue":"","lookupTable":null,"description":""}`},
		"delete without id":      {"delete", "tmv", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"delete id text":         {"delete", "tmv", `{` + base[:len(base)-1] + `"4"}`},
		"delete extra":           {"delete", "tmv", `{` + base + `,"name":"n"}`},
		"delete bad container":   {"delete", "tmv", `{"idSite":1,"idContainer":"a b","idVariable":4}`},
	} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-variables."+tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
	}
}

func TestTagManagerVariableUpdateRefusesAStoredBlockedTypeAfterOneReadWithoutMutation(t *testing.T) {
	for _, stored := range []string{"CustomJsFunction", "CustomRequestProcessing", "customjsfunction", " CustomJsFunction "} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
			"TagManager.getContainerVariable": func(call) *http.Response {
				return ok(storedVariable(stored, `{"jsFunction":"function(){return 1}"}`))
			},
		}).handler(t))
		_, err := env.change("matomo.tag-manager-variables.update", "tmv", variableWriteTools[1].arguments)
		if err == nil || !isInvalidRequest(err) || !noVariableMutation(calls) ||
			countMethod(calls, "TagManager.getContainerVariable") != 1 || strings.Contains(err.Error(), "return 1") {
			t.Errorf("stored %q: err = %v, calls = %d", stored, err, len(calls))
		}
	}
	// Other types, a missing row, and a row of another variable or website do not pass as evidence.
	for name, body := range map[string]string{
		"missing":       `false`,
		"empty list":    `[]`,
		"other ID":      strings.Replace(storedVariable("Constant", `{}`), `"idvariable":4`, `"idvariable":5`, 1),
		"other website": strings.Replace(storedVariable("Constant", `{}`), `"idsite":1`, `"idsite":9`, 1),
		"other version": strings.Replace(storedVariable("Constant", `{}`), `"idcontainerversion":8`, `"idcontainerversion":7`, 1),
		"not an object": `"x"`,
	} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
			"TagManager.getContainerVariable": func(call) *http.Response { return ok(body) },
		}).handler(t))
		if _, err := env.change("matomo.tag-manager-variables.update", "tmv", variableWriteTools[1].arguments); err == nil || !noVariableMutation(calls) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerVariableConfigurationMustNameThisMatomoAndWebsite(t *testing.T) {
	const good = `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1"}`
	for name, parameters := range map[string]string{
		"foreign host":   `{"matomoUrl":"https://evil.example.org/matomo/","idSite":"1"}`,
		"reference url":  `{"matomoUrl":"{{TrackerUrl}}","idSite":"1"}`,
		"foreign site":   `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"2"}`,
		"reference site": `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"{{Site}}"}`,
		"api url":        `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"https://evil.example.org/"}`,
		"request code":   `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","customRequestProcessing":"{{Code}}"}`,
		"endpoint":       `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpoint":"//evil.example.org/"}`,
	} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
			"TagManager.getContainerVariable": func(call) *http.Response {
				return ok(storedVariable("MatomoConfiguration", good))
			},
		}).handler(t))
		_, err := env.change("matomo.tag-manager-variables.update", "tmv", configArguments("update", parameters))
		if err == nil || !isInvalidRequest(err) || !noVariableMutation(calls) || strings.Contains(err.Error(), "evil.example.org") ||
			countMethod(calls, "TagManager.getContainerVariable") != 1 {
			t.Errorf("update %s: err = %v", name, err)
		}
	}
	// The type that counts is the stored one: the same parameters pass for any other type.
	var calls []call
	env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
	if _, err := env.change("matomo.tag-manager-variables.update", "tmv",
		configArguments("update", `{"matomoUrl":"https://evil.example.org/","idSite":"2"}`)); err != nil {
		t.Errorf("update of a constant: %v", err)
	}
	for name, parameters := range map[string]string{
		"exact":             good,
		"no trailing slash": `{"matomoUrl":"https://analytics.example.test/matomo","idSite":"1"}`,
		"upper case host":   `{"matomoUrl":"HTTPS://Analytics.Example.Test/matomo/","idSite":"1"}`,
		"default port":      `{"matomoUrl":"https://analytics.example.test:443/matomo/","idSite":"1"}`,
		"numeric site":      `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":1}`,
		"empty code":        `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"","customRequestProcessing":""}`,
		"plain endpoints": `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpoint":"custom",` +
			`"trackingEndpointCustom":"tracker/custom.php","jsEndpoint":"js/","jsEndpointCustom":"custom.js"}`,
		"other settings": `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","enableLinkTracking":true,"customDimensions":[{"index":"1","value":"v"}]}`,
	} {
		for _, tool := range []string{"add", "update"} {
			var calls []call
			env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
				"TagManager.getContainerVariable": func(call) *http.Response {
					return ok(storedVariable("MatomoConfiguration", good))
				},
			}).handler(t))
			if _, err := env.change("matomo.tag-manager-variables."+tool, "tmv", configArguments(tool, parameters)); err != nil {
				t.Errorf("%s %s: %v", tool, name, err)
			}
		}
	}
}

func TestTagManagerVariableWriteIsUnsupportedWithoutThePluginAndSendsNoMethod(t *testing.T) {
	for _, tool := range variableWriteTools {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response { return ok(`{"value":false}`) },
		}).handler(t))
		if _, err := env.change(tool.id, "tmv", tool.arguments); !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerVariableWriteMapsErrorsWithoutTheMessageAndNeverRepeats(t *testing.T) {
	for _, tool := range variableWriteTools {
		reads := []string{"TagManager.getContainer", tool.method}
		if tool.id == "matomo.tag-manager-variables.update" {
			reads = append(reads, "TagManager.getContainerVariable")
		}
		for _, method := range reads {
			for name, tc := range map[string]struct {
				message string
				class   provider.Class
				missing bool
			}{
				"permission":        {"You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
				"container missing": {`The requested container "token123" does not exist.`, provider.ClassNotFound, false},
				"site not found":    {"An unexpected website was found in the request: website id = 1 not found.", provider.ClassNotFound, false},
				"method missing":    {"The method 'TagManager.x' does not exist or is not available in the module 'TagManager'.", "", true},
				"plugin off":        {"The plugin TagManager is not activated.", "", true},
				"other":             {"Something went wrong", provider.ClassInvalidResponse, false},
			} {
				var calls []call
				env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
					method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
				}).handler(t))
				_, err := env.change(tool.id, "tmv", tool.arguments)
				if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
					isUnsupported(err) != tc.missing || classOf(err) != tc.class ||
					countMethod(calls, method) != 1 || (method != tool.method && countMethod(calls, tool.method) != 0) {
					t.Errorf("%s %s %s: err = %v (class %q)", tool.id, method, name, err, classOf(err))
				}
			}
		}
	}
}

func TestTagManagerVariableWriteNameInUseIsAClearRefusal(t *testing.T) {
	for _, tool := range variableWriteTools[:2] {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
			tool.method: func(call) *http.Response { return failure("This name is already in use." + bodyCanary) },
		}).handler(t))
		_, err := env.change(tool.id, "tmv", tool.arguments)
		if err == nil || !isInvalidRequest(err) || !strings.Contains(err.Error(), "another variable") ||
			strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 {
			t.Errorf("%s: err = %v", tool.id, err)
		}
	}
}

func TestTagManagerVariableWriteUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
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
	for _, tool := range variableWriteTools {
		for name, outcome := range outcomes {
			var calls []call
			known := variableWriteAnswers(answers{}).handler(t)
			env := newVariableWriteEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == tool.method {
					return outcome(c)
				}
				return known(c)
			})
			_, err := env.change(tool.id, "tmv", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", tool.id, name, err, countMethod(calls, tool.method))
				continue
			}
			if name != "429 without Retry-After" && (!strings.Contains(err.Error(), "never repeated") ||
				!strings.Contains(err.Error(), "matomo.tag-manager-variables.list")) {
				t.Errorf("%s %s: no uncertainty message or hint: %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerVariableUpdateReadThatTimesOutOrCannotBeReadSendsNoWrite(t *testing.T) {
	for name, outcome := range map[string]func(call) (*http.Response, error){
		"timeout": func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"html":    func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
	} {
		var calls []call
		known := variableWriteAnswers(answers{}).handler(t)
		env := newVariableWriteEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "TagManager.getContainerVariable" {
				return outcome(c)
			}
			return known(c)
		})
		_, err := env.change("matomo.tag-manager-variables.update", "tmv", variableWriteTools[1].arguments)
		if err == nil || !noVariableMutation(calls) || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerVariableAddWithoutAnIDIsUnclearAndNotRepeated(t *testing.T) {
	for name, body := range map[string]string{"none": `{"result":"success"}`, "zero": `{"value":0}`, "text": `{"value":"x"}`} {
		var calls []call
		env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{
			"TagManager.addContainerVariable": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := env.change("matomo.tag-manager-variables.add", "tmv", variableWriteTools[0].arguments)
		if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls, "TagManager.addContainerVariable") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerVariableWriteTokenIsInTheRedactor(t *testing.T) {
	var calls []call
	env := newVariableWriteEnvironment(t, &calls, variableWriteAnswers(answers{}).handler(t))
	if _, err := env.change(variableWriteTools[2].id, "tmv", variableWriteTools[2].arguments); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}
