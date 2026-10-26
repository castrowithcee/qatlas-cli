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

const aiConnections = `
  ai-admin: {service: matomo, credential: token, tools: [matomo.ai-providers.disconnect, matomo.ai-providers.get-settings], targets: [instance]}
  ai-site: {service: matomo, credential: token, tools: [matomo.ai-providers.disconnect, matomo.ai-providers.get-settings], targets: [site/1]}
  ai-open: {service: matomo, credential: token, effects: [read, delete], targets: [instance]}
`

// aiKey is built at run time so the fixture holds no key-like literal.
var aiKey = "sk-" + "fixture" + "-0123456789abcdef"

var aiSettingsFixture = `{"defaultProviderId":"openai","defaultCapabilityLevel":"instant","canEditProviderConfiguration":true,
"canEditCapabilityLevel":true,"capabilityLevels":{"thinking":{"label":"x"},"instant":{"label":"y"}},
"providers":[{"id":"openai","name":"OpenAI","description":"d","supportsCustomEndpoint":true,"supportsFipsEndpoint":false,
"defaultEndpointUrl":"https://api.example.test/v1","endpointFieldTitle":"t","defaultModel":"m1",
"configuration":{"hasApiKey":true,"apiKey":"` + aiKey + `","endpointUrl":"https://user:pw@example.test/v1?key=abc#f",
"model":"m2","useFipsEndpoint":false,"isUsable":true}},
{"id":"other","name":"Other","configuration":{"hasApiKey":false,"endpointUrl":"","model":"","useFipsEndpoint":false,"isUsable":false}}]}`

func newAIEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	serve(t, calls, a.handler(t))
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(aiConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func aiAnswers(extra answers) answers {
	return with(pluginsOn(), extra)
}

func TestAIProvidersGetSettingsNeverShowsTheKey(t *testing.T) {
	var calls []call
	env := newAIEnvironment(t, &calls, aiAnswers(answers{
		"AIProviders.getSettings": func(call) *http.Response { return ok(aiSettingsFixture) },
	}))
	result, err := env.invoke("matomo.ai-providers.get-settings", "ai-admin", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{aiKey, "sk-", "user:pw", "key=abc", "apiKey", "endpointFieldTitle"} {
		if strings.Contains(result, leak) {
			t.Errorf("result contains %q: %s", leak, result)
		}
	}
	for _, want := range []string{`"api_key_set":true`, `"endpointUrl":"https://example.test/v1"`,
		`"capabilityLevels":["instant","thinking"]`, `"defaultProviderId":"openai"`, `"api_key_set":false`} {
		if !strings.Contains(result, want) {
			t.Errorf("result lacks %q: %s", want, result)
		}
	}
	sent := calls[len(calls)-1]
	if sent.api() != "AIProviders.getSettings" || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
		sent.form.Get("format") != "JSON" || sent.form.Has("token_auth") || sent.form.Has("callback") {
		t.Errorf("request = %+v", sent)
	}
	if got := env.red.Apply("x " + aiKey); strings.Contains(got, aiKey) {
		t.Errorf("a key in the answer is not registered: %s", got)
	}
}

func TestAIProvidersRefusalsSendNothing(t *testing.T) {
	var calls []call
	env := newAIEnvironment(t, &calls, aiAnswers(answers{
		"AIProviders.getSettings":        func(call) *http.Response { return ok(aiSettingsFixture) },
		"AIProviders.disconnectProvider": func(call) *http.Response { return ok(`{"providers":[]}`) },
	}))
	before, readsBefore := len(calls), *env.reads
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.ai-providers.get-settings", "ai-site", `{}`},
		{"matomo.ai-providers.get-settings", "open", `{}`},
		{"matomo.ai-providers.disconnect", "ai-site", `{"providerId":"openai"}`},
		{"matomo.ai-providers.disconnect", "ai-open", `{"providerId":"openai"}`},
		{"matomo.ai-providers.disconnect", "ai-admin", `{}`},
		{"matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"a b"}`},
		{"matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"../x"}`},
		{"matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"` + strings.Repeat("a", 65) + `"}`},
		{"matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"openai","extra":1}`},
	} {
		if _, err := env.change(tc.tool, tc.connection, tc.arguments); err == nil {
			t.Errorf("%s %s %s ran", tc.tool, tc.connection, tc.arguments)
		}
	}
	if _, err := env.invoke("matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"openai"}`); err == nil {
		t.Error("disconnect ran without confirm")
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-readsBefore)
	}
}

func TestAIProvidersDisconnectSendsOnce(t *testing.T) {
	var calls []call
	env := newAIEnvironment(t, &calls, aiAnswers(answers{
		"AIProviders.disconnectProvider": func(call) *http.Response { return ok(aiSettingsFixture) },
	}))
	result, err := env.change("matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"openai"}`)
	if err != nil || result != `{"disconnected":true,"providerId":"openai"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("providerId") != "openai" || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
		sent.form.Has("passwordConfirmation") || countMethod(calls, "AIProviders.disconnectProvider") != 1 {
		t.Errorf("request = %+v", sent)
	}
	if listed, _, _ := registry(t).Lookup("matomo.ai-providers.disconnect"); !listed.RequiresToolAllowList {
		t.Error("disconnect is not listed-only")
	}
	// A connection that lists no tool offers only reads and may not disconnect.
	if _, err := env.change("matomo.ai-providers.disconnect", "instance", `{"providerId":"openai"}`); err == nil {
		t.Error("disconnect ran on a connection that did not list it")
	}
}

func TestAIProvidersMapErrorsAndPluginChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) (*http.Response, error)
		class       provider.Class
		unsupported bool
		unclear     bool
	}{
		"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
		"502":             {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", false, true},
		"unreadable":      {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, false, true},
		"no confirmation": {func(call) (*http.Response, error) { return ok(`[]`), nil }, provider.ClassInvalidResponse, false, true},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
		"unknown":         {func(call) (*http.Response, error) { return ok2(failure("unknown provider " + bodyCanary)) }, "", false, false},
		"429":             {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false, false},
	} {
		var calls []call
		serve(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "AIProviders.disconnectProvider" {
				return tc.answer(c)
			}
			return aiAnswers(nil).handler(t)(c)
		})
		reads := 0
		red := &redact.Redactor{}
		reg := registry(t)
		document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(aiConnections, "\n"), 1)
		cfg, err := config.Decode(strings.NewReader(document), reg)
		if err != nil {
			t.Fatal(err)
		}
		env := &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
		_, err = env.change("matomo.ai-providers.disconnect", "ai-admin", `{"providerId":"openai"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) ||
			tc.class != "" && classOf(err) != tc.class || countMethod(calls, "AIProviders.disconnectProvider") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "AIProviders.disconnectProvider"))
			continue
		}
		if tc.unclear && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v does not say that the outcome is unclear", name, err)
		}
	}

	// The plugin inactive or the release too old ends before any AIProviders method is called.
	for name, handler := range map[string]answers{
		"inactive": with(versionOnly("5.14.2"), answers{"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) }}),
		"too old":  with(versionOnly("5.12.9"), answers{"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) }}),
	} {
		for _, tool := range []struct{ id, arguments string }{
			{"matomo.ai-providers.get-settings", `{}`}, {"matomo.ai-providers.disconnect", `{"providerId":"openai"}`}} {
			var calls []call
			env := newAIEnvironment(t, &calls, handler)
			_, err := env.change(tool.id, "ai-admin", tool.arguments)
			if !isUnsupported(err) || countMethod(calls, "AIProviders.getSettings")+countMethod(calls, "AIProviders.disconnectProvider") != 0 {
				t.Errorf("%s %s: err = %v", name, tool.id, err)
			}
		}
	}
}
