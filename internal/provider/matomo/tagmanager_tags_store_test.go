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
// getContainerTag and getContainerVariables (database rows with parameters as objects), addContainerTag (the new
// ID as a scalar), and updateContainerTag and resumeContainerTag (no value, which the API reports as a success).

const tagStoreConnections = `
  tms: {service: matomo, credential: token, tools: [matomo.tag-manager-tags.add, matomo.tag-manager-tags.update, matomo.tag-manager-tags.resume], targets: [site]}
  tms-two: {service: matomo, credential: token, tools: [matomo.tag-manager-tags.add, matomo.tag-manager-tags.update, matomo.tag-manager-tags.resume], targets: [site/2]}
  tms-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
`

func newTagStoreEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(tagStoreConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func storedTag(tagType, parameters string) string {
	return `{"idtag":5,"idsite":1,"idcontainerversion":8,"type":"` + tagType + `","name":"Stored","description":"",` +
		`"parameters":` + parameters + `,"fire_trigger_ids":[3],"status":"paused"}`
}

func configVariable(name, variableType, parameters string) string {
	return `{"idvariable":2,"idsite":1,"idcontainerversion":8,"type":"` + variableType + `","name":"` + name + `",` +
		`"parameters":` + parameters + `}`
}

const (
	goodConfigParameters = `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1"}`
	matomoParameters     = `{"matomoConfig":"{{Main}}","trackingType":"pageview"}`
)

var tagStoreTools = []struct{ id, method, arguments string }{
	{"matomo.tag-manager-tags.add", "TagManager.addContainerTag", `{"idSite":1,"idContainer":"6OMh6taM","type":"Matomo","name":"Tracking",` +
		`"parameters":{"matomoConfig":"{{Main}}","trackingType":"pageview"},"fireTriggerIds":[3],"blockTriggerIds":[4],` +
		`"fireLimit":"once_page","fireDelay":5,"priority":10,"startDate":"2026-01-01","endDate":"2026-12-31 10:00:00",` +
		`"description":"d","status":"paused"}`},
	{"matomo.tag-manager-tags.update", "TagManager.updateContainerTag", `{"idSite":1,"idContainer":"6OMh6taM","idTag":5,"name":"Tracking",` +
		`"parameters":{"matomoConfig":"{{Main}}","trackingType":"pageview"},"fireTriggerIds":[3],"blockTriggerIds":[],` +
		`"fireLimit":"unlimited","fireDelay":0,"priority":999,"description":""}`},
	{"matomo.tag-manager-tags.resume", "TagManager.resumeContainerTag", `{"idSite":1,"idContainer":"6OMh6taM","idTag":5}`},
}

func tagStoreAnswers(extra answers) answers {
	return tagManagerAnswers(with(answers{
		"TagManager.getContainer":    func(call) *http.Response { return ok(containerRow) },
		"TagManager.getContainerTag": func(call) *http.Response { return ok(storedTag("Matomo", matomoParameters)) },
		"TagManager.getContainerVariables": func(call) *http.Response {
			return ok(`[` + configVariable("Main", "MatomoConfiguration", goodConfigParameters) + `]`)
		},
		"TagManager.addContainerTag":    func(call) *http.Response { return ok(`{"value":12}`) },
		"TagManager.updateContainerTag": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"TagManager.resumeContainerTag": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func noMutation(calls []call) bool {
	for _, tool := range tagStoreTools {
		if countMethod(calls, tool.method) != 0 {
			return false
		}
	}
	return true
}

func TestTagManagerTagStoreToolsAreListedOnlyConfirmedInNoProfileAndTakeNoVersion(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.tag-manager-tags.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.tag-manager-tags.update": {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.tag-manager-tags.resume": {capability.EffectUpdate, capability.IdempotencyIdempotent},
	}
	for _, tool := range tagStoreTools {
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
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool.id {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
	var calls []call
	env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
	for _, tool := range tagStoreTools {
		if _, err := env.change(tool.id, "tms-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
		withVersion := strings.TrimSuffix(tool.arguments, "}") + `,"idContainerVersion":7}`
		if _, err := env.change(tool.id, "tms", withVersion); err == nil {
			t.Errorf("%s took a version", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTagManagerTagStoreReadsEvidenceOnceThenSendsOneConfirmedRequestIntoTheDraft(t *testing.T) {
	for _, tool := range tagStoreTools {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
		if _, err := env.invoke(tool.id, "tms", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: an unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "tms", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		wantTag := 1
		if tool.id == "matomo.tag-manager-tags.add" {
			wantTag = 0
		}
		if countMethod(calls, "TagManager.getContainer") != 1 || countMethod(calls, tool.method) != 1 ||
			countMethod(calls, "TagManager.getContainerTag") != wantTag || countMethod(calls, "TagManager.getContainerVariables") != 1 ||
			indexOfMethod(calls, "TagManager.getContainerVariables") > indexOfMethod(calls, tool.method) {
			t.Fatalf("%s: unexpected calls %d", tool.id, len(calls))
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") && (c.auth != "Bearer "+tokenValue || c.form.Has("token_auth") ||
				strings.Contains(c.rawQuery+c.path, tokenValue) || c.method != http.MethodPost) {
				t.Errorf("%s: token handling: %+v", tool.id, c)
			}
		}
		variables := calls[indexOfMethod(calls, "TagManager.getContainerVariables")]
		if variables.form.Get("idContainerVersion") != "8" || variables.form.Get("idSite") != "1" {
			t.Errorf("%s: variables read = %v", tool.id, variables.form)
		}
		sent := calls[indexOfMethod(calls, tool.method)]
		if sent.form.Get("idSite") != "1" || sent.form.Get("idContainer") != "6OMh6taM" ||
			sent.form.Get("idContainerVersion") != "8" {
			t.Errorf("%s: request = %v", tool.id, sent.form)
		}
		switch tool.id {
		case "matomo.tag-manager-tags.add":
			f := sent.form
			if !strings.Contains(result, `"idTag":12`) || f.Get("type") != "Matomo" || f.Get("name") != "Tracking" ||
				f.Get("parameters[matomoConfig]") != "{{Main}}" || f.Get("fireTriggerIds[0]") != "3" ||
				f.Get("blockTriggerIds[0]") != "4" || f.Get("fireLimit") != "once_page" || f.Get("fireDelay") != "5" ||
				f.Get("priority") != "10" || f.Get("startDate") != "2026-01-01" || f.Get("status") != "paused" ||
				f.Get("endDate") != "2026-12-31 10:00:00" || f.Has("idTag") {
				t.Errorf("add: %s, %v", result, f)
			}
		case "matomo.tag-manager-tags.update":
			if !strings.Contains(result, `"updated":true`) || sent.form.Get("idTag") != "5" || sent.form.Has("type") ||
				sent.form.Has("status") || !sent.form.Has("description") {
				t.Errorf("update: %s, %v", result, sent.form)
			}
		case "matomo.tag-manager-tags.resume":
			if !strings.Contains(result, `"resumed":true`) || sent.form.Get("idTag") != "5" {
				t.Errorf("resume: %s, %v", result, sent.form)
			}
		}
	}
}

func TestTagManagerTagStoreThemeColorNeedsNoVariableRead(t *testing.T) {
	arguments := map[string]string{
		"matomo.tag-manager-tags.add": `{"idSite":1,"idContainer":"6OMh6taM","type":"ThemeColor","name":"Color",` +
			`"parameters":{"themeColor":"#336699"},"fireTriggerIds":[3]}`,
		"matomo.tag-manager-tags.update": `{"idSite":1,"idContainer":"6OMh6taM","idTag":5,"name":"Color","parameters":` +
			`{"themeColor":"#336699"},"fireTriggerIds":[3],"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,` +
			`"priority":999,"description":""}`,
		"matomo.tag-manager-tags.resume": tagStoreTools[2].arguments,
	}
	for _, tool := range tagStoreTools {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
			"TagManager.getContainerTag": func(call) *http.Response { return ok(storedTag("ThemeColor", `{"themeColor":"#112233"}`)) },
		}).handler(t))
		if _, err := env.change(tool.id, "tms", arguments[tool.id]); err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if countMethod(calls, tool.method) != 1 || countMethod(calls, "TagManager.getContainerVariables") != 0 {
			t.Errorf("%s: writes %d, variable reads %d", tool.id, countMethod(calls, tool.method), countMethod(calls, "TagManager.getContainerVariables"))
		}
	}
}

func TestTagManagerTagAddRefusesOtherAndUnknownTypesAndForeignParametersBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM","name":"n","fireTriggerIds":[3]`
	manyParameters := make([]string, 101)
	for i := range manyParameters {
		manyParameters[i] = `"p` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":"x"`
	}
	for name, tc := range map[string]struct{ connection, arguments string }{
		"custom html":      {"tms", `{` + base + `,"type":"CustomHtml","parameters":{"customHtml":"<script src=https://evil.example.org></script>"}}`},
		"custom image":     {"tms", `{` + base + `,"type":"CustomImage"}`},
		"facebook pixel":   {"tms", `{` + base + `,"type":"FacebookPixel"}`},
		"unknown type":     {"tms", `{` + base + `,"type":"BrandNewTag"}`},
		"lower case":       {"tms", `{` + base + `,"type":"matomo","parameters":{"matomoConfig":"{{Main}}"}}`},
		"missing type":     {"tms", `{` + base + `}`},
		"foreign website":  {"tms-two", `{` + base + `,"type":"ThemeColor"}`},
		"no website":       {"tms", `{"idContainer":"6OMh6taM","name":"n","fireTriggerIds":[3],"type":"ThemeColor"}`},
		"no config":        {"tms", `{` + base + `,"type":"Matomo","parameters":{"trackingType":"pageview"}}`},
		"no parameters":    {"tms", `{` + base + `,"type":"Matomo"}`},
		"config literal":   {"tms", `{` + base + `,"type":"Matomo","parameters":{"matomoConfig":"https://evil.example.org"}}`},
		"config two refs":  {"tms", `{` + base + `,"type":"Matomo","parameters":{"matomoConfig":"{{A}}{{B}}"}}`},
		"config suffix":    {"tms", `{` + base + `,"type":"Matomo","parameters":{"matomoConfig":"{{A}}x"}}`},
		"config list":      {"tms", `{` + base + `,"type":"Matomo","parameters":{"matomoConfig":["{{A}}"]}}`},
		"no fire trigger":  {"tms", `{"idSite":1,"idContainer":"6OMh6taM","name":"n","type":"ThemeColor"}`},
		"empty fire list":  {"tms", `{"idSite":1,"idContainer":"6OMh6taM","name":"n","type":"ThemeColor","fireTriggerIds":[]}`},
		"bad trigger id":   {"tms", `{` + strings.Replace(base, "[3]", "[0]", 1) + `,"type":"ThemeColor"}`},
		"bad fire limit":   {"tms", `{` + base + `,"type":"ThemeColor","fireLimit":"always"}`},
		"bad date":         {"tms", `{` + base + `,"type":"ThemeColor","startDate":"tomorrow"}`},
		"bad status":       {"tms", `{` + base + `,"type":"ThemeColor","status":"deleted"}`},
		"negative delay":   {"tms", `{` + base + `,"type":"ThemeColor","fireDelay":-1}`},
		"empty name":       {"tms", `{"idSite":1,"idContainer":"6OMh6taM","name":"","fireTriggerIds":[3],"type":"ThemeColor"}`},
		"long description": {"tms", `{` + base + `,"type":"ThemeColor","description":"` + strings.Repeat("x", 1001) + `"}`},
		"id with add":      {"tms", `{` + base + `,"type":"ThemeColor","idTag":5}`},
		"parameter name":   {"tms", `{` + base + `,"type":"ThemeColor","parameters":{"a[0]":"c"}}`},
		"parameter long":   {"tms", `{` + base + `,"type":"ThemeColor","parameters":{"themeColor":"` + strings.Repeat("x", 16385) + `"}}`},
		"parameters many":  {"tms", `{` + base + `,"type":"ThemeColor","parameters":{` + strings.Join(manyParameters, ",") + `}}`},
		"passthrough":      {"tms", `{` + base + `,"type":"ThemeColor","method":"TagManager.publishContainerVersion"}`},
	} {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-tags.add", tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "evil.example.org") || strings.Contains(err.Error(), "site/")) {
			t.Errorf("%s: the refusal quotes a value: %v", name, err)
		}
	}
}

func TestTagManagerTagUpdateAndResumeRefuseBadArgumentsAndForeignWebsiteBeforeSecretAndRequest(t *testing.T) {
	const base = `"idSite":1,"idContainer":"6OMh6taM","idTag":5`
	const full = `"name":"n","parameters":{},"fireTriggerIds":[3],"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,"priority":1,"description":""`
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign update":         {"update", "tms-two", `{` + base + `,` + full + `}`},
		"foreign resume":         {"resume", "tms-two", `{` + base + `}`},
		"resume without tag":     {"resume", "tms", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"resume tag zero":        {"resume", "tms", `{"idSite":1,"idContainer":"6OMh6taM","idTag":0}`},
		"update without tag":     {"update", "tms", `{"idSite":1,"idContainer":"6OMh6taM",` + full + `}`},
		"update with type":       {"update", "tms", `{` + base + `,"type":"CustomHtml",` + full + `}`},
		"update without params":  {"update", "tms", `{` + base + `,"name":"n","fireTriggerIds":[3],"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,"priority":1,"description":""}`},
		"update without fire":    {"update", "tms", `{` + base + `,"name":"n","parameters":{},"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,"priority":1,"description":""}`},
		"update without limit":   {"update", "tms", `{` + base + `,"name":"n","parameters":{},"fireTriggerIds":[3],"blockTriggerIds":[],"fireDelay":0,"priority":1,"description":""}`},
		"update with status":     {"update", "tms", `{` + base + `,` + full + `,"status":"active"}`},
		"update null parameters": {"update", "tms", `{` + base + `,"name":"n","parameters":null,"fireTriggerIds":[3],"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,"priority":1,"description":""}`},
		"resume extra":           {"resume", "tms", `{` + base + `,"status":"active"}`},
	} {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
		_, err := env.change("matomo.tag-manager-tags."+tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
	}
}

func TestTagManagerTagUpdateAndResumeRefuseAStoredTypeOutsideTheAllowListWithoutMutation(t *testing.T) {
	for _, stored := range []string{"CustomHtml", "CustomImage", "FacebookPixel", "BrandNewTag", "matomo", ""} {
		for _, tool := range tagStoreTools[1:] {
			var calls []call
			env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
				"TagManager.getContainerTag": func(call) *http.Response {
					return ok(storedTag(stored, `{"customHtml":"<script src=https://evil.example.org></script>"}`))
				},
			}).handler(t))
			_, err := env.change(tool.id, "tms", tool.arguments)
			if err == nil || !isInvalidRequest(err) || !noMutation(calls) || countMethod(calls, "TagManager.getContainerTag") != 1 ||
				countMethod(calls, "TagManager.getContainerVariables") != 0 || strings.Contains(err.Error(), "evil.example.org") {
				t.Errorf("%s stored %q: err = %v, calls = %d", tool.id, stored, err, len(calls))
			}
		}
	}
}

func TestTagManagerTagMatomoTagIsRefusedWithoutMutationForAnyOtherTrackingTarget(t *testing.T) {
	good := goodConfigParameters
	for name, variable := range map[string]string{
		"foreign host":       configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://evil.example.org/matomo/","idSite":"1"}`),
		"other host in path": configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test.evil.example.org/matomo/","idSite":"1"}`),
		"other path":         configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/other/","idSite":"1"}`),
		"no path":            configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/","idSite":"1"}`),
		"other port":         configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test:8443/matomo/","idSite":"1"}`),
		"http":               configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"http://analytics.example.test/matomo/","idSite":"1"}`),
		"no scheme":          configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"//analytics.example.test/matomo/","idSite":"1"}`),
		"user info":          configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://x:@analytics.example.test/matomo/","idSite":"1"}`),
		"query":              configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/?a=b","idSite":"1"}`),
		"reference url":      configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"{{TrackerUrl}}","idSite":"1"}`),
		"embedded reference": configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/{{Path}}/","idSite":"1"}`),
		"missing url":        configVariable("Main", "MatomoConfiguration", `{"idSite":"1"}`),
		"foreign site":       configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"2"}`),
		"reference site":     configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"{{Site}}"}`),
		"missing site":       configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/"}`),
		"empty parameters":   configVariable("Main", "MatomoConfiguration", `[]`),
		"foreign api url":    configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"https://evil.example.org/matomo/"}`),
		"request processing": configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","customRequestProcessing":"return 1;"}`),
		"protocol relative":  configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpoint":"//evil.example.org/"}`),
		"absolute endpoint":  configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpointCustom":"https://evil.example.org/m.php"}`),
		"leading slash":      configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpointCustom":"/evil.example.org/m.php"}`),
		"parent path":        configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","jsEndpointCustom":"../evil.example.org/m.js"}`),
		"numeric endpoint":   configVariable("Main", "MatomoConfiguration", `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","jsEndpoint":5}`),
		"wrong type":         configVariable("Main", "Constant", good),
		"other name":         configVariable("Other", "MatomoConfiguration", good),
		"other site row":     strings.Replace(configVariable("Main", "MatomoConfiguration", good), `"idsite":1`, `"idsite":9`, 1),
		"other version row":  strings.Replace(configVariable("Main", "MatomoConfiguration", good), `"idcontainerversion":8`, `"idcontainerversion":7`, 1),
	} {
		for _, tool := range tagStoreTools {
			var calls []call
			env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
				"TagManager.getContainerVariables": func(call) *http.Response { return ok(`[` + variable + `]`) },
			}).handler(t))
			_, err := env.change(tool.id, "tms", tool.arguments)
			if err == nil || !isInvalidRequest(err) || !noMutation(calls) || strings.Contains(err.Error(), "evil.example.org") ||
				countMethod(calls, "TagManager.getContainerVariables") != 1 {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
	}
	for name, variables := range map[string]string{
		"no variables":   `[]`,
		"duplicate name": `[` + configVariable("Main", "MatomoConfiguration", good) + `,` + configVariable("Main", "MatomoConfiguration", good) + `]`,
		"false":          `false`,
	} {
		for _, tool := range tagStoreTools {
			var calls []call
			env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
				"TagManager.getContainerVariables": func(call) *http.Response { return ok(variables) },
			}).handler(t))
			if _, err := env.change(tool.id, "tms", tool.arguments); err == nil || !noMutation(calls) {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerTagResumeChecksTheStoredConfigAndUpdateTheNewOne(t *testing.T) {
	for name, stored := range map[string]string{
		"literal address": `{"matomoConfig":"https://evil.example.org/"}`,
		"no config":       `{"trackingType":"pageview"}`,
		"empty":           `[]`,
		"other variable":  `{"matomoConfig":"{{Other}}"}`,
	} {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
			"TagManager.getContainerTag": func(call) *http.Response { return ok(storedTag("Matomo", stored)) },
		}).handler(t))
		if _, err := env.change("matomo.tag-manager-tags.resume", "tms", tagStoreTools[2].arguments); err == nil || !noMutation(calls) {
			t.Errorf("resume %s: err = %v", name, err)
		}
	}
	// A stored tag that was fine may not be updated to a configuration that is not.
	var calls []call
	env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
	bad := strings.Replace(tagStoreTools[1].arguments, "{{Main}}", "{{Other}}", 1)
	if _, err := env.change("matomo.tag-manager-tags.update", "tms", bad); err == nil || !noMutation(calls) {
		t.Errorf("update to another variable: err = %v", err)
	}
}

func TestTagManagerTagMatomoTargetAcceptsTheSameAddressInOtherSpellings(t *testing.T) {
	for name, parameters := range map[string]string{
		"no trailing slash": `{"matomoUrl":"https://analytics.example.test/matomo","idSite":"1"}`,
		"upper case host":   `{"matomoUrl":"HTTPS://Analytics.Example.Test/matomo/","idSite":"1"}`,
		"default port":      `{"matomoUrl":"https://analytics.example.test:443/matomo/","idSite":"1"}`,
		"numeric site":      `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":1}`,
		"empty code fields": `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"","customRequestProcessing":"","trackingEndpoint":"","jsEndpointCustom":""}`,
		"plain endpoints":   `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","trackingEndpoint":"matomo.php","trackingEndpointCustom":"tracker/m.php","jsEndpoint":"matomo.js","jsEndpointCustom":"js/m.js"}`,
		"other settings":    `{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","enableLinkTracking":true,"cookieDomain":"*.example.test","requireConsent":"1"}`,
	} {
		for _, tool := range tagStoreTools {
			var calls []call
			env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
				"TagManager.getContainerVariables": func(call) *http.Response {
					return ok(`[` + configVariable("Main", "MatomoConfiguration", parameters) + `]`)
				},
			}).handler(t))
			if _, err := env.change(tool.id, "tms", tool.arguments); err != nil {
				t.Errorf("%s %s: %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerTagResumeRefusesAStoredValidTagWhoseVariableNowSetsAnApiUrl(t *testing.T) {
	var calls []call
	env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
		"TagManager.getContainerVariables": func(call) *http.Response {
			return ok(`[` + configVariable("Main", "MatomoConfiguration",
				`{"matomoUrl":"https://analytics.example.test/matomo/","idSite":"1","setApiUrl":"https://evil.example.org/"}`) + `]`)
		},
	}).handler(t))
	_, err := env.change("matomo.tag-manager-tags.resume", "tms", tagStoreTools[2].arguments)
	if err == nil || !isInvalidRequest(err) || !noMutation(calls) || strings.Contains(err.Error(), "evil.example.org") ||
		countMethod(calls, "TagManager.getContainerVariables") != 1 {
		t.Errorf("resume: err = %v", err)
	}
}

func TestTagManagerTagStoreIsUnsupportedWithoutThePluginAndSendsNoMethod(t *testing.T) {
	for _, tool := range tagStoreTools {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response { return ok(`{"value":false}`) },
		}).handler(t))
		if _, err := env.change(tool.id, "tms", tool.arguments); !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerTagStoreMapsErrorsWithoutTheMessageAndNeverRepeats(t *testing.T) {
	for _, tool := range tagStoreTools {
		reads := []string{"TagManager.getContainer", "TagManager.getContainerVariables", tool.method}
		if tool.id != "matomo.tag-manager-tags.add" {
			reads = append(reads, "TagManager.getContainerTag")
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
				env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
					method: func(call) *http.Response { return failure(tc.message + bodyCanary) },
				}).handler(t))
				_, err := env.change(tool.id, "tms", tool.arguments)
				if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
					isUnsupported(err) != tc.missing || classOf(err) != tc.class ||
					countMethod(calls, method) != 1 || (method != tool.method && countMethod(calls, tool.method) != 0) {
					t.Errorf("%s %s %s: err = %v (class %q)", tool.id, method, name, err, classOf(err))
				}
			}
		}
	}
}

func TestTagManagerTagStoreNameInUseIsAClearRefusal(t *testing.T) {
	for _, tool := range tagStoreTools[:2] {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
			tool.method: func(call) *http.Response { return failure("This name is already in use." + bodyCanary) },
		}).handler(t))
		_, err := env.change(tool.id, "tms", tool.arguments)
		if err == nil || !isInvalidRequest(err) || !strings.Contains(err.Error(), "another tag") ||
			strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "never repeated") ||
			countMethod(calls, tool.method) != 1 {
			t.Errorf("%s: err = %v", tool.id, err)
		}
	}
}

func TestTagManagerTagStoreUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
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
	for _, tool := range tagStoreTools {
		for name, outcome := range outcomes {
			var calls []call
			known := tagStoreAnswers(answers{}).handler(t)
			env := newTagStoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == tool.method {
					return outcome(c)
				}
				return known(c)
			})
			_, err := env.change(tool.id, "tms", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", tool.id, name, err, countMethod(calls, tool.method))
				continue
			}
			if name != "429 without Retry-After" && (!strings.Contains(err.Error(), "never repeated") ||
				!strings.Contains(err.Error(), "matomo.tag-manager-tags.list")) {
				t.Errorf("%s %s: no uncertainty message or hint: %v", tool.id, name, err)
			}
		}
	}
}

func TestTagManagerTagStoreReadsThatTimeOutOrCannotBeReadSendNoWrite(t *testing.T) {
	for _, tool := range tagStoreTools {
		for _, read := range []string{"TagManager.getContainerTag", "TagManager.getContainerVariables"} {
			if read == "TagManager.getContainerTag" && tool.id == "matomo.tag-manager-tags.add" {
				continue
			}
			for name, outcome := range map[string]func(call) (*http.Response, error){
				"timeout":  func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
				"html":     func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
				"missing":  func(call) (*http.Response, error) { return ok(`false`), nil },
				"not rows": func(call) (*http.Response, error) { return ok(`"x"`), nil },
			} {
				var calls []call
				known := tagStoreAnswers(answers{}).handler(t)
				env := newTagStoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
					if c.api() == read {
						return outcome(c)
					}
					return known(c)
				})
				_, err := env.change(tool.id, "tms", tool.arguments)
				if name == "missing" && read == "TagManager.getContainerVariables" {
					continue
				}
				if err == nil || !noMutation(calls) || strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("%s %s %s: err = %v", tool.id, read, name, err)
				}
			}
		}
	}
}

func TestTagManagerTagAddWithoutAnIDIsUnclearAndNotRepeated(t *testing.T) {
	for name, body := range map[string]string{"none": `{"result":"success"}`, "zero": `{"value":0}`, "text": `{"value":"x"}`} {
		var calls []call
		env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{
			"TagManager.addContainerTag": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := env.change("matomo.tag-manager-tags.add", "tms", tagStoreTools[0].arguments)
		if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls, "TagManager.addContainerTag") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTagManagerTagStoreTokenIsInTheRedactor(t *testing.T) {
	var calls []call
	env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
	if _, err := env.change(tagStoreTools[2].id, "tms", tagStoreTools[2].arguments); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}

func TestTagManagerTagStorePassesOnParametersTheTemplateDefines(t *testing.T) {
	var calls []call
	env := newTagStoreEnvironment(t, &calls, tagStoreAnswers(answers{}).handler(t))
	arguments := `{"idSite":1,"idContainer":"6OMh6taM","type":"Matomo","name":"n","fireTriggerIds":[3],` +
		`"parameters":{"matomoConfig":"{{Main}}","documentTitle":"T","customDimensions":[{"index":"1","value":"v"}]}}`
	if _, err := env.change("matomo.tag-manager-tags.add", "tms", arguments); err != nil {
		t.Fatal(err)
	}
	sent := calls[indexOfMethod(calls, "TagManager.addContainerTag")]
	if sent.form.Get("parameters[documentTitle]") != "T" || sent.form.Get("parameters[customDimensions][0][index]") != "1" {
		t.Errorf("form = %v", sent.form)
	}
}
