package matomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// No test reaches a real Matomo instance: every request is answered by the package's transport seam. The
// fixtures follow the answers of the Matomo reporting API reference.
const (
	tokenValue = "canary0matomo0token0canary0abcdef01"
	tokenEnv   = "TEST_MATOMO_TOKEN"
	baseURL    = "https://analytics.example.test/matomo"
	apiHost    = "analytics.example.test"
	bodyCanary = "provider-message-canary-9d2c"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, host, path, rawQuery, auth, accept, contentType string
	form                                                    url.Values
}

func (c call) api() string { return c.form.Get("method") }

type bodyCloser struct{ io.Reader }

func (bodyCloser) Close() error { return nil }

func respond(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: bodyCloser{strings.NewReader(body)},
		Header: http.Header{"Content-Type": {"application/json"}}}
}

func ok(body string) *http.Response { return respond(http.StatusOK, body) }

// failure is Matomo's answer to an application error: HTTP 200 with result error.
func failure(message string) *http.Response {
	encoded, _ := json.Marshal(map[string]string{"result": "error", "message": message})
	return ok(string(encoded))
}

const (
	versionAnswer = `{"value":"5.14.2"}`
	permissionMsg = "You can't access this resource as it requires 'view' access for the website id = 99."
)

func siteRow(id, name string) string {
	return `{"idsite":"` + id + `","name":"` + name + `","main_url":"https://` + name + `.example.test",` +
		`"ts_created":"2024-01-01 00:00:00","ecommerce":"0","sitesearch":"1","sitesearch_keyword_parameters":"q",` +
		`"sitesearch_category_parameters":"","timezone":"Europe/Berlin","currency":"EUR","exclude_unknown_urls":"0",` +
		`"excluded_ips":"","excluded_parameters":"","excluded_user_agents":"","excluded_referrers":"","group":"",` +
		`"type":"website","keep_url_fragment":"0","creator_login":"admin","timezone_name":"Europe/Berlin",` +
		`"currency_name":"Euro"}`
}

func siteList(ids ...string) string {
	rows := make([]string, len(ids))
	for i, id := range ids {
		rows[i] = siteRow(id, "site"+id)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// answers serves the calls of one test by Matomo method; an unlisted method fails the test.
type answers map[string]func(call) *http.Response

func serve(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(data))
		entry := call{method: r.Method, host: r.URL.Host, path: r.URL.Path, rawQuery: r.URL.RawQuery,
			auth: r.Header.Get("Authorization"), accept: r.Header.Get("Accept"),
			contentType: r.Header.Get("Content-Type"), form: form}
		*calls = append(*calls, entry)
		if r.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		return handler(entry)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
	instances.mu.Lock()
	instances.known = map[instanceKey]*instanceInfo{}
	instances.mu.Unlock()
	t.Cleanup(func() {
		instances.mu.Lock()
		instances.known = map[instanceKey]*instanceInfo{}
		instances.mu.Unlock()
	})
}

func (a answers) handler(t *testing.T) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		answer, found := a[c.api()]
		if !found {
			t.Errorf("unexpected Matomo method %q", c.api())
			return failure("unexpected"), nil
		}
		return answer(c), nil
	}
}

func resolver(red *redact.Redactor, reads *int) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		if reads != nil {
			*reads++
		}
		if name == tokenEnv {
			return tokenValue
		}
		return ""
	}, nil, nil, red)
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

// testConfig decodes the connections of the tests with the provider catalog, so target rules apply.
func testConfig(t *testing.T, reg *capability.Registry) *config.Config {
	t.Helper()
	cfg, err := config.Decode(strings.NewReader(testConfigDocument), reg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const testConfigDocument = `version: 1
services:
  matomo:
    provider: matomo
    base_url: ` + baseURL + `
credentials:
  token:
    provider: matomo
    type: env
    values:
      token: ` + tokenEnv + `
connections:
  open: {service: matomo, credential: token, effects: [read]}
  general: {service: matomo, credential: token, effects: [read], targets: [site]}
  one: {service: matomo, credential: token, effects: [read], targets: [site/2]}
  two: {service: matomo, credential: token, effects: [read], targets: [site/1, site/2]}
  instance: {service: matomo, credential: token, effects: [read], targets: [instance]}
  mixed: {service: matomo, credential: token, effects: [read], targets: [site/2, instance]}
`

type environment struct {
	cfg   *config.Config
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	cfg := testConfig(t, reg)
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
	})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var failure *provider.Error
	if errors.As(err, &failure) {
		return failure.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

func isUnsupported(err error) bool {
	var unsupported *capability.UnsupportedError
	return errors.As(err, &unsupported)
}

// versionOnly answers the version call and nothing else.
func versionOnly(version string) answers {
	return answers{"API.getMatomoVersion": func(call) *http.Response { return ok(`{"value":"` + version + `"}`) }}
}

func with(base answers, extra answers) answers {
	merged := answers{}
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleToken ||
		metadata.DefaultBaseURL != "" || metadata.Target.Required || !metadata.Target.Multiple {
		t.Fatalf("metadata = %+v", metadata)
	}
	for _, tool := range metadata.Tools {
		changes := (strings.HasPrefix(tool.ID, "matomo.goals.") || strings.HasPrefix(tool.ID, "matomo.segments.")) &&
			tool.Effect != config.EffectRead
		if !changes && (tool.Effect != config.EffectRead || tool.RequiresToolAllowList) {
			t.Errorf("tool %+v, want an open read", tool)
		}
	}
	profile, found := metadata.RecommendedProfile()
	if !found || profile.ID != "read" || len(profile.Tools) != 12 || len(metadata.Tools) != 20 {
		t.Fatalf("profile = %+v, tools = %+v", profile, metadata.Tools)
	}
	for _, id := range []string{"matomo.version.get", "matomo.sites.list", "matomo.sites.get", "matomo.reports.list",
		"matomo.reports.describe", "matomo.reports.get", "matomo.segments.fields", "matomo.segments.suggest-values",
		"matomo.segments.list", "matomo.segments.get"} {
		if !strings.Contains(strings.Join(profile.Tools, " "), id) {
			t.Errorf("profile lacks %s", id)
		}
	}
}

func TestVersionGetSendsOneFixedPostWithBearerHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	result, err := env.invoke("matomo.version.get", "open", `{}`)
	if err != nil || result != `{"version":"5.14.2"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.method != http.MethodPost || got.path != "/matomo/index.php" || got.rawQuery != "" ||
		got.auth != "Bearer "+tokenValue || got.accept != "application/json" ||
		got.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("request = %+v", got)
	}
	if got.form.Get("module") != "API" || got.form.Get("format") != "JSON" || got.form.Has("token_auth") {
		t.Fatalf("form = %v", got.form)
	}
}

func TestTokenNeverInURLQueryOrArguments(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesWithAtLeastViewAccess": func(call) *http.Response { return ok(siteList("1")) },
		"SitesManager.getSiteFromId":                 func(call) *http.Response { return ok("[" + siteRow("1", "a") + "]") },
		"SitesManager.getSiteUrlsFromId":             func(call) *http.Response { return ok(`["https://a.example.test"]`) },
	}).handler(t))
	for _, c := range [][2]string{{"matomo.sites.list", `{}`}, {"matomo.sites.get", `{"idSite":1}`}} {
		if _, err := env.invoke(c[0], "open", c[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, got := range calls {
		if strings.Contains(got.rawQuery, tokenValue) || strings.Contains(got.path, tokenValue) || got.form.Has("token_auth") {
			t.Errorf("token reached the URL or the form: %+v", got)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor"} {
			if got.form.Has(name) {
				t.Errorf("request carries %s", name)
			}
		}
		if got.form.Get("filter_limit") == "-1" {
			t.Error("request carries filter_limit=-1")
		}
	}
	if got := env.red.Apply("Bearer " + tokenValue); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the bearer header: %s", got)
	}
}

func TestVersionGetWithPluginAsksTheInstance(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(c call) *http.Response {
			return ok(map[bool]string{true: `{"value":true}`, false: `{"value":false}`}[c.form.Get("pluginName") == "Goals"])
		},
	}).handler(t))
	for plugin, want := range map[string]string{"Goals": "true", "Funnels": "false"} {
		result, err := env.invoke("matomo.version.get", "open", `{"plugin":"`+plugin+`"}`)
		if err != nil || !strings.Contains(result, `"activated":`+want) {
			t.Fatalf("plugin %s: result = %s, err = %v", plugin, result, err)
		}
	}
	if _, err := env.invoke("matomo.version.get", "open", `{"plugin":"Goals;drop"}`); err == nil {
		t.Error("an invalid plugin name was accepted")
	}
}

func TestVersionBoundaries(t *testing.T) {
	for version, accepted := range map[string]bool{"4.15.1": false, "4.99.0": false, "5.0.0": true, "5.0": true,
		"5.14.2": true, "5.0.0-rc1": true, "6.0.0": true} {
		var calls []call
		env := newEnvironment(t, &calls, versionOnly(version).handler(t))
		class, err := TestConnection(context.Background(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
		if err != nil || (class == provider.ClassOK) != accepted {
			t.Errorf("version %s: class = %q err = %v, want accepted=%t", version, class, err, accepted)
		}
	}
}

func TestOldVersionIsRefusedAsUnsupportedCapabilityWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("4.15.1").handler(t))
	_, err := env.invoke("matomo.sites.list", "open", `{}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "Matomo 5.0.0 or newer") {
		t.Fatalf("err = %v, want unsupported-capability naming the version", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want only the version read", len(calls))
	}
}

func TestTokenInBodyOnlyBeforeMatomo540(t *testing.T) {
	var calls []call
	answer := func(c call) *http.Response {
		if c.form.Get("token_auth") != tokenValue {
			return failure(permissionMsg)
		}
		return ok(`{"value":"5.3.2"}`)
	}
	env := newEnvironment(t, &calls, with(answers{"API.getMatomoVersion": answer}, answers{
		"SitesManager.getSitesWithAtLeastViewAccess": func(c call) *http.Response {
			if c.form.Get("token_auth") != tokenValue {
				return failure(permissionMsg)
			}
			return ok(siteList("1"))
		},
	}).handler(t))
	if _, err := env.invoke("matomo.sites.list", "open", `{}`); err != nil {
		t.Fatal(err)
	}
	// The probe without the field failed, the probe with it decided the mode, and the list used it at once.
	if len(calls) != 3 || calls[0].form.Has("token_auth") || !calls[1].form.Has("token_auth") || !calls[2].form.Has("token_auth") {
		t.Fatalf("calls = %+v", calls)
	}
	for _, got := range calls {
		if got.rawQuery != "" || got.auth != "Bearer "+tokenValue {
			t.Errorf("request = %+v", got)
		}
	}
	// A later call on the same instance needs no probe.
	if _, err := env.invoke("matomo.sites.list", "open", `{}`); err != nil || len(calls) != 4 {
		t.Fatalf("second list: calls = %d, err = %v", len(calls), err)
	}
}

func TestModernInstanceThatOnlyAcceptsTheBodyFailsClosed(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, answers{"API.getMatomoVersion": func(c call) *http.Response {
		if c.form.Get("token_auth") != tokenValue {
			return failure(permissionMsg)
		}
		return ok(`{"value":"5.14.2"}`)
	}}.handler(t))
	_, err := env.invoke("matomo.version.get", "open", `{}`)
	if classOf(err) != provider.ClassAuth {
		t.Fatalf("err = %v, want auth", err)
	}
}

func TestVersionIsHeldUntilItExpires(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesWithAtLeastViewAccess": func(call) *http.Response { return ok(siteList("1")) },
	}).handler(t))
	clock := time.Now()
	previous := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previous })
	for i := 0; i < 3; i++ {
		if _, err := env.invoke("matomo.sites.list", "open", `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls) != 4 { // one version read, three lists
		t.Fatalf("calls = %d, want 4", len(calls))
	}
	clock = clock.Add(instanceTTL + time.Second)
	if _, err := env.invoke("matomo.sites.list", "open", `{}`); err != nil || len(calls) != 6 {
		t.Fatalf("after expiry: calls = %d, err = %v", len(calls), err)
	}
}

func TestApplicationErrorsWithHTTP200MapToClasses(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		class         provider.Class
		unsupported   bool
	}{
		{"permission", permissionMsg, provider.ClassPermission, false},
		{"permission for admin", "You can't access this resource as it requires 'admin' access for the website id = 3.",
			provider.ClassPermission, false},
		{"not found", "An unexpected website was found in the request: website id was set to '99' .",
			provider.ClassNotFound, false},
		{"method missing", "The method 'SitesManager.getFoo' does not exist or is not available in the module 'SitesManager'.",
			"", true},
		{"plugin inactive", "The plugin Goals is not activated.", "", true},
		{"unknown text", "Something " + bodyCanary + " went wrong", provider.ClassInvalidResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
				"SitesManager.getSiteFromId": func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.invoke("matomo.sites.get", "open", `{"idSite":99}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want an error without the Matomo text", err)
			}
			if tc.unsupported != isUnsupported(err) || classOf(err) != tc.class {
				t.Fatalf("err = %v (class %q, unsupported %t), want class %q unsupported %t", err, classOf(err),
					isUnsupported(err), tc.class, tc.unsupported)
			}
		})
	}
}

func TestInvalidTokenSurfacesAsAuthOnTheConnectionTest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, answers{"API.getMatomoVersion": func(call) *http.Response {
		return failure(permissionMsg)
	}}.handler(t))
	class, err := TestConnection(context.Background(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
	if err != nil || class != provider.ClassAuth {
		t.Fatalf("class = %q, err = %v", class, err)
	}
}

func TestMessagePatterns(t *testing.T) {
	for message, want := range map[string]string{
		"Unable to authenticate with the provided token.": "auth",
		"The token is not valid.":                         "auth",
		permissionMsg:                                     "permission",
		"You can't access this resource as it requires 'superuser' access.":                    "permission",
		"An unexpected website was found in the request: website id was set to '3' .":          "not-found",
		"The method 'X.y' does not exist or is not available in the module 'X'.":               "missing",
		"The plugin Goals is not activated.":                                                   "missing",
		"Requested report A.b for Website id=1 not found in the list of available reports. \n": "not-found",
		"Ein unbekannter Fehler": "",
		"":                       "",
	} {
		if got := classifyMessage(message); got != want {
			t.Errorf("classifyMessage(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestHTTPStatusesMapToClasses(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusBadRequest, provider.ClassProviderError},
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusInternalServerError, provider.ClassProviderError},
		{http.StatusServiceUnavailable, provider.ClassUnreachable},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
		{http.StatusFound, provider.ClassProviderError},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(tc.status, bodyCanary), nil })
		_, err := env.invoke("matomo.version.get", "open", `{}`)
		if classOf(err) != tc.class || err == nil || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: err = %v, want class %q", tc.status, err, tc.class)
		}
		if len(calls) != 1 {
			t.Errorf("status %d: %d requests, want no retry", tc.status, len(calls))
		}
	}
}

func TestRateLimitWithoutRetryAfterHoldsTheKeyWithoutRetrying(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(429, ""), nil })
	clock := time.Now()
	var waited []time.Duration
	limiter := ratelimit.New(0, func() time.Time { return clock },
		func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil })
	t.Cleanup(limiters.Replace(tokenValue, limiter))
	_, err := env.invoke("matomo.version.get", "open", `{}`)
	if classOf(err) != provider.ClassRateLimited || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
	if _, err := env.invoke("matomo.version.get", "open", `{}`); err == nil || len(waited) != 1 || waited[0] != rateLimitHold {
		t.Fatalf("second call: err = %v, waited = %v, want a hold of %s", err, waited, rateLimitHold)
	}
}

func TestTimeoutAndUnreadableAnswers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return nil, context.DeadlineExceeded })
	if _, err := env.invoke("matomo.version.get", "open", `{}`); classOf(err) != provider.ClassTimeout {
		t.Errorf("timeout: err = %v", err)
	}
	for name, body := range map[string]string{
		"html login page": `<!DOCTYPE html><html><body>Login ` + bodyCanary + `</body></html>`,
		"xml":             `<?xml version="1.0"?><result>` + bodyCanary + `</result>`,
		"empty":           ``,
		"oversized":       `["` + strings.Repeat("a", maxResponseSize) + `"]`,
		"wrong shape":     `{"value":["x"]}`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return ok(body), nil })
		_, err := env.invoke("matomo.version.get", "open", `{}`)
		if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v, want invalid-provider-response", name, err)
		}
	}
}

func TestClientNeverSendsReservedParameters(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	client, err := Open(context.Background(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"callback": "x", "jsoncallback": "x", "token_auth": "x", "module": "X",
		"disable_generic_filters": "1", "disable_queued_filters": "1", "disable_root_datatable_post_processor": "1",
		"filter_limit": "-1"} {
		var out json.RawMessage
		if err := client.send(context.Background(), "test", "API.getMatomoVersion", url.Values{name: {value}}, false, &out); err == nil {
			t.Errorf("parameter %s was sent", name)
		}
	}
	if len(calls) != 0 {
		t.Errorf("a refused parameter still caused %d requests", len(calls))
	}
}

func TestUnavailableByReleaseOrPluginSendsNothingFurther(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.3.0"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	client, err := Open(context.Background(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
	if err != nil {
		t.Fatal(err)
	}
	var out json.RawMessage
	err = client.call(context.Background(), "test", requirement{tool: "matomo.demo.read", minVersion: version{5, 9, 0}},
		"Demo.read", nil, &out)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "Matomo 5.9.0 or newer") || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d, want a release refusal after the version read", err, len(calls))
	}
	err = client.call(context.Background(), "test", requirement{tool: "matomo.demo.read", plugin: "Funnels"},
		"Funnels.read", nil, &out)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "Funnels") || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %d, want a plugin refusal after one plugin read", err, len(calls))
	}
	// The plugin state is held too.
	if err := client.call(context.Background(), "test", requirement{tool: "matomo.demo.read", plugin: "Funnels"},
		"Funnels.read", nil, &out); !isUnsupported(err) || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %d, want no further request", err, len(calls))
	}
}

func mustResolve(t *testing.T, env *environment, name string) *config.Resolved {
	t.Helper()
	resolved, err := env.cfg.Resolve(name, "")
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
