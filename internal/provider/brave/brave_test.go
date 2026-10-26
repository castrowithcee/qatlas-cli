package brave

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
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

// The canaries stand for the key and for text a failing Brave echoes. No test reaches Brave: every request
// is answered by a local test server installed through the package transport.
const (
	tokenValue   = "canary0brave0key0123456789abcdef0123"
	tokenEnv     = "TEST_BRAVE_KEY"
	detailCanary = "provider-detail-canary-4f2a"
)

// recorded is one request the fake Brave received.
type recorded struct {
	method, path string
	query        url.Values
	header       http.Header
	rawQuery     string
	body         string
}

// fakeBrave answers the routes this provider uses and records every request.
type fakeBrave struct {
	mu       sync.Mutex
	requests []recorded
	// answer, when set, replaces the default answer.
	answer func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeBrave) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recorded{method: r.Method, path: r.URL.Path, query: r.URL.Query(),
		header: r.Header.Clone(), rawQuery: r.URL.RawQuery, body: string(body)})
	f.mu.Unlock()
	if f.answer != nil {
		f.answer(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Limit", "1, 15000")
	w.Header().Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
	w.Header().Set("X-RateLimit-Remaining", "1, 14523")
	w.Header().Set("X-RateLimit-Reset", "1, 1234567")
	fmt.Fprint(w, newsFixture)
}

func (f *fakeBrave) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// reply answers every request with a status, rate-limit headers, and a body that echoes request values the
// way Brave's error answers may.
func reply(status int, headers map[string]string, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// errorBody is the error shape of Brave with a detail that echoes the key and a canary.
func errorBody(status int) string {
	return fmt.Sprintf(`{"type":"ErrorResponse","error":{"id":"e1","status":%d,"detail":"%s %s","meta":{"errors":`+
		`[{"input":"%s","loc":["query","q"],"msg":"bad","type":"x"}]},"code":"VALIDATION"},"time":1}`,
		status, detailCanary, tokenValue, detailCanary)
}

type rewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.search.brave.com" {
		return nil, errors.New("unexpected host")
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, ""
	return r.base.RoundTrip(clone)
}

// serve starts a local TLS server, routes the package transport to it, and replaces the spacing of the test
// key by one that never sleeps.
func serve(t *testing.T, f http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	previous := transport
	transport = rewrite{target: target, base: server.Client().Transport}
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
}

// freeLimiter spaces nothing and never sleeps.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
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
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatalf("ValidateProfiles() = %v", err)
	}
	return reg
}

func credential() config.Credential {
	return config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
}

func coreConfig() *config.Config {
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"brave": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"brave-key": credential()},
		Connections: map[string]config.Connection{
			"search":  {Service: "brave", Credential: "brave-key"},
			"listed":  {Service: "brave", Credential: "brave-key", Tools: []string{"brave.news.search"}},
			"answers": {Service: "brave", Credential: "brave-key", Tools: []string{"brave.answers.ask"}},
			"bound":   {Service: "brave", Credential: "brave-key", Target: "somewhere"},
		},
	}
}

// environment is an application core over the fake Brave with a counting credential resolver.
type environment struct {
	fake  *fakeBrave
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, f *fakeBrave) *environment {
	t.Helper()
	serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	return &environment{fake: f, core: application.New(registry(t), coreConfig(), resolver(red, &reads), red),
		red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments)})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Brave Search" || metadata.DefaultBaseURL != "https://api.search.brave.com" ||
		len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != "token" {
		t.Fatalf("metadata = %+v", metadata)
	}
	if !reflect.DeepEqual(metadata.DefaultEffects, []config.Effect{config.EffectRead}) ||
		!reflect.DeepEqual(metadata.SupportedEffects, []config.Effect{config.EffectRead}) {
		t.Errorf("effects = %v / %v, want read only", metadata.DefaultEffects, metadata.SupportedEffects)
	}
	var ids []string
	for _, tool := range metadata.Tools {
		ids = append(ids, tool.ID)
		// Answers costs by searches and tokens, so only a tools list offers it.
		if tool.Effect != config.EffectRead || tool.RequiresToolAllowList != (tool.ID == "brave.answers.ask") {
			t.Errorf("tool %+v, want a read that needs an allow-list only for answers", tool)
		}
	}
	if !reflect.DeepEqual(ids, []string{"brave.answers.ask", "brave.images.search", "brave.llm-context.get",
		"brave.news.search", "brave.places.search", "brave.poi-descriptions.get", "brave.pois.get",
		"brave.rich.get", "brave.spellcheck.check", "brave.suggestions.list", "brave.videos.search",
		"brave.web.search"}) {
		t.Errorf("tools = %v", ids)
	}
	for _, descriptor := range reg.Provider(Provider) {
		if descriptor.Risk != (capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity}) {
			t.Errorf("%s risk = %+v", descriptor.ID, descriptor.Risk)
		}
		for _, want := range []string{"billed", "terms of use", "untrusted"} {
			if !strings.Contains(descriptor.Description, want) {
				t.Errorf("%s description lacks %q: %s", descriptor.ID, want, descriptor.Description)
			}
		}
	}
	if len(metadata.Profiles) != 3 || metadata.Profiles[2].ID != "autosuggest" || metadata.Profiles[2].Recommended ||
		!reflect.DeepEqual(metadata.Profiles[2].Tools, []string{"brave.suggestions.list", "brave.spellcheck.check"}) {
		t.Errorf("profiles = %+v, want a not recommended autosuggest profile with both tools", metadata.Profiles)
	}
	if metadata.Profiles[0].ID != "search" || !metadata.Profiles[0].Recommended ||
		!reflect.DeepEqual(metadata.Profiles[0].Tools, []string{"brave.llm-context.get", "brave.news.search",
			"brave.web.search", "brave.videos.search", "brave.images.search", "brave.rich.get",
			"brave.places.search", "brave.pois.get", "brave.poi-descriptions.get"}) ||
		metadata.Profiles[1].ID != "answers" || metadata.Profiles[1].Recommended ||
		!reflect.DeepEqual(metadata.Profiles[1].Tools, []string{"brave.answers.ask"}) {
		t.Errorf("profiles = %+v, want the recommended search profile and the answers profile", metadata.Profiles)
	}
}

// A provider without targets: configuration refuses any target, and the empty target row the editors show is
// optional, single, and without kinds, wildcard, or browser.
func TestAConnectionTakesNoTarget(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	if target := metadata.Target; target.Required || target.Multiple || target.Wildcard != "" ||
		len(target.Kinds) != 0 || target.Label == "" || target.Description == "" || reg.HasTargetBrowser(Provider) {
		t.Errorf("target metadata = %+v, want an optional single target row that offers nothing", target)
	}
	document := func(target string) string {
		return "version: 1\nservices:\n  brave:\n    provider: brave\ncredentials:\n  key:\n    provider: brave\n    type: env\n" +
			"    values:\n      token: " + tokenEnv + "\nconnections:\n  search:\n    service: brave\n" +
			"    credential: key\n" + target
	}
	if _, err := config.Decode(strings.NewReader(document("")), reg); err != nil {
		t.Fatalf("a connection without a target is refused: %v", err)
	}
	for name, target := range map[string]string{
		"target":  "    target: somewhere\n",
		"targets": "    targets: [a, b]\n",
		"one":     "    targets: [a]\n",
	} {
		_, err := config.Decode(strings.NewReader(document(target)), reg)
		if err == nil || strings.Contains(err.Error(), "somewhere") {
			t.Errorf("%s: err = %v, want a refusal that does not quote the target", name, err)
		}
	}
	// The editors read the same metadata for an empty row; a runtime guard refuses a bound connection that
	// bypassed validation.
	env := newEnvironment(t, &fakeBrave{})
	_, err := env.invoke("brave.news.search", "bound", `{"q":"x"}`)
	if classOf(err) != provider.ClassProviderError || *env.reads != 0 || len(env.fake.recorded()) != 0 ||
		strings.Contains(err.Error(), "somewhere") {
		t.Errorf("bound connection: err = %v, reads = %d, want a refusal before the key is read", err, *env.reads)
	}
}

func TestOnlyTheOfficialOriginIsUsed(t *testing.T) {
	f := &fakeBrave{}
	serve(t, f)
	red := &redact.Redactor{}
	for _, base := range []string{"https://search.example.invalid/secret-path?token=canary",
		"http://api.search.brave.com", "https://api.search.brave.com.evil.example",
		"https://api.search.brave.com/res"} {
		reads := 0
		resolved := &config.Resolved{Name: "b", Provider: Provider, BaseURL: base, Credential: "k",
			Secrets: credential(), Effects: []config.Effect{config.EffectRead}}
		class, err := TestConnection(context.Background(), resolved, resolver(red, &reads), red)
		if class != provider.ClassProviderError || err != nil || reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("base %s: class = %q, err = %v, reads = %d, requests = %d, want a refusal before the key",
				base, class, err, reads, len(f.recorded()))
		}
		_, err = Open(context.Background(), resolved, resolver(red, &reads), red)
		if err == nil || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "evil") ||
			strings.Contains(err.Error(), "example.invalid") {
			t.Errorf("base %s: Open error %v must not quote the value", base, err)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	if metadata.ValidateBaseURL == nil || metadata.ValidateBaseURL("https://other.example") == nil ||
		metadata.ValidateBaseURL(apiRoot) != nil || metadata.ValidateBaseURL(apiRoot+"/") != nil ||
		metadata.ValidateBaseURL("") != nil {
		t.Error("configuration validation does not hold the official origin")
	}
}

func TestTheKeyTravelsOnlyInItsHeader(t *testing.T) {
	f := &fakeBrave{}
	env := newEnvironment(t, f)
	result, err := env.invoke("brave.news.search", "search", `{"q":"canary test","country":"DE"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	requests := f.recorded()
	if len(requests) != 1 {
		t.Fatalf("%d requests, want 1", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodGet || r.path != "/res/v1/news/search" || r.header.Get("X-Subscription-Token") != tokenValue ||
		r.header.Get("Accept") != "application/json" || r.header.Get("User-Agent") != "qatlas-cli" {
		t.Errorf("request = %+v", r)
	}
	if strings.Contains(r.rawQuery, tokenValue) || r.header.Get("Authorization") != "" {
		t.Errorf("the key reached the query or another header: %s", r.rawQuery)
	}
	if strings.Contains(result, tokenValue) {
		t.Errorf("the key reached the result")
	}
	if got := env.red.Apply("x " + tokenValue); strings.Contains(got, tokenValue) {
		t.Errorf("the key is not registered with the redactor: %q", got)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var elsewhere int
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere++ }))
	defer other.Close()
	f := &fakeBrave{answer: func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal?key=1", http.StatusFound)
	}}
	env := newEnvironment(t, f)
	_, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
	if classOf(err) != provider.ClassProviderError || elsewhere != 0 || len(f.recorded()) != 1 ||
		strings.Contains(err.Error(), other.URL) {
		t.Errorf("err = %v, followed %d, requests %d, want an unfollowed redirect", err, elsewhere, len(f.recorded()))
	}
}

func TestStatusesMapToClassesWithoutProviderText(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusBadRequest, provider.ClassInvalidResponse},
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusPaymentRequired, provider.ClassPermission},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotFound, provider.ClassNotFound},
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusInternalServerError, provider.ClassProviderError},
		{http.StatusBadGateway, provider.ClassUnreachable},
		{http.StatusServiceUnavailable, provider.ClassUnreachable},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
		{http.StatusTeapot, provider.ClassProviderError},
	} {
		f := &fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))}
		env := newEnvironment(t, f)
		_, err := env.invoke("brave.news.search", "search", `{"q":"findme"}`)
		if classOf(err) != tc.class {
			t.Errorf("HTTP %d: class = %q (%v), want %q", tc.status, classOf(err), err, tc.class)
			continue
		}
		var failed *provider.Error
		errors.As(err, &failed)
		for _, leak := range []string{detailCanary, tokenValue, "findme", "api.search.brave.com", "VALIDATION"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: error %q leaks %q", tc.status, err, leak)
			}
		}
		if failed.Original == nil || failed.Original.Status != tc.status || len(failed.Original.Entries) != 1 ||
			failed.Original.Entries[0].Code != "VALIDATION" ||
			!strings.Contains(failed.Original.Entries[0].Message, detailCanary) {
			t.Errorf("HTTP %d: original = %+v, want the status and Brave's code and detail", tc.status, failed.Original)
		}
	}
}

// The mapping of 402 and 403 is not documented, so neither message may claim a plan or a balance finding.
func TestPlanAndBalanceMessagesClaimNothing(t *testing.T) {
	for _, status := range []int{http.StatusPaymentRequired, http.StatusForbidden} {
		env := newEnvironment(t, &fakeBrave{answer: reply(status, nil, errorBody(status))})
		_, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
		text := err.Error()
		if !strings.Contains(text, "plan") || strings.Contains(text, "is not included") ||
			strings.Contains(text, "has no credit") {
			t.Errorf("HTTP %d: %q", status, text)
		}
	}
}

func TestAnswersThatCannotBeReadAreInvalid(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"results":[{"title":"`)
	big.WriteString(strings.Repeat("a", maxResponseBytes))
	big.WriteString(`"}]}`)
	truncated := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		var raw strings.Builder
		zip := gzip.NewWriter(&raw)
		fmt.Fprint(zip, newsFixture)
		zip.Close()
		// A gzip stream cut in the middle is unreadable.
		fmt.Fprint(w, raw.String()[:raw.Len()/2])
	}
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"not json":    reply(200, nil, "<html>maintenance</html>"),
		"empty":       reply(200, nil, ""),
		"an array":    reply(200, nil, `[1,2]`),
		"wrong type":  reply(200, nil, `{"results":[{"title":7}]}`),
		"too large":   reply(200, nil, big.String()),
		"cut in gzip": truncated,
	} {
		env := newEnvironment(t, &fakeBrave{answer: answer})
		_, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid-provider-response", name, err)
		}
	}
}

func TestATimeoutIsATimeout(t *testing.T) {
	serve(t, &fakeBrave{})
	transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	reg := registry(t)
	red := &redact.Redactor{}
	core := application.New(reg, coreConfig(), resolver(red, nil), red)
	_, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: "brave.news.search",
		Connection: "search", Arguments: json.RawMessage(`{"q":"x"}`)})
	if classOf(err) != provider.ClassTimeout {
		t.Errorf("err = %v, want timeout", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConnectionTest(t *testing.T) {
	resolved := func(tools []string, effects ...config.Effect) *config.Resolved {
		return &config.Resolved{Name: "b", Provider: Provider, BaseURL: apiRoot, Credential: "k", Secrets: credential(),
			Tools: tools, Effects: effects}
	}
	run := func(f *fakeBrave, r *config.Resolved) (provider.Class, error) {
		serve(t, f)
		red := &redact.Redactor{}
		return TestConnection(context.Background(), r, resolver(red, nil), red)
	}
	f := &fakeBrave{}
	class, err := run(f, resolved(nil, config.EffectRead))
	requests := f.recorded()
	if err != nil || class != provider.ClassOK || len(requests) != 1 || requests[0].path != "/res/v1/spellcheck/search" ||
		requests[0].query.Get("q") != "brave" ||
		requests[0].header.Get("X-Subscription-Token") != tokenValue {
		t.Fatalf("success: class = %q, err = %v, requests = %+v, want one minimal spellcheck request", class, err, requests)
	}
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		// Brave answers an invalid or missing key with 422 too, so a 422 never proves a valid key.
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse},
		{http.StatusInternalServerError, provider.ClassProviderError},
	} {
		class, err := run(&fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))}, resolved(nil, config.EffectRead))
		if err != nil || class != tc.class {
			t.Errorf("HTTP %d: class = %q, err = %v, want %q", tc.status, class, err, tc.class)
		}
	}
	class, err = run(&fakeBrave{answer: reply(200, nil, "not json")}, resolved(nil, config.EffectRead))
	if err != nil || class != provider.ClassInvalidResponse {
		t.Errorf("unreadable answer: class = %q, err = %v", class, err)
	}

	// A connection that offers no tool spends no request.
	f = &fakeBrave{}
	class, err = run(f, resolved([]string{}))
	if err == nil || class != "" || len(f.recorded()) != 0 {
		t.Errorf("no tool: class = %q, err = %v, requests = %d", class, err, len(f.recorded()))
	}
	class, err = run(f, resolved(nil, config.EffectCreate))
	if err == nil || len(f.recorded()) != 0 {
		t.Errorf("no read effect: class = %q, err = %v", class, err)
	}
}

func TestProbeFollowsWhatTheConnectionOffers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved config.Resolved
		want     string
	}{
		{"by effect", config.Resolved{Effects: []config.Effect{config.EffectRead}}, "brave.spellcheck.check"},
		{"news listed", config.Resolved{Tools: []string{"brave.news.search"}}, "brave.news.search"},
		{"both listed", config.Resolved{Tools: []string{"brave.llm-context.get", "brave.news.search", "brave.videos.search", "brave.images.search"}}, "brave.news.search"},
		{"spellcheck listed", config.Resolved{Tools: []string{"brave.news.search", "brave.suggestions.list", "brave.spellcheck.check"}}, "brave.spellcheck.check"},
		{"only suggestions", config.Resolved{Tools: []string{"brave.suggestions.list", "brave.web.search"}}, "brave.suggestions.list"},
		{"only llm context", config.Resolved{Tools: []string{"brave.llm-context.get"}}, "brave.llm-context.get"},
		{"only web", config.Resolved{Tools: []string{"brave.web.search"}}, "brave.web.search"},
		{"only videos", config.Resolved{Tools: []string{"brave.videos.search"}}, "brave.videos.search"},
		{"only images", config.Resolved{Tools: []string{"brave.images.search"}}, "brave.images.search"},
		{"only places", config.Resolved{Tools: []string{"brave.places.search"}}, "brave.places.search"},
		{"only place details", config.Resolved{Tools: []string{"brave.pois.get", "brave.poi-descriptions.get"}}, ""},
		{"none", config.Resolved{Tools: []string{}}, ""},
	} {
		got, ok := probeFor(&tc.resolved)
		if got.tool != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: probe = %q, %v, want %q", tc.name, got.tool, ok, tc.want)
		}
	}
	if probes[0].path != "/res/v1/spellcheck/search" || probes[0].query.Get("q") != "brave" {
		t.Errorf("spellcheck probe = %+v", probes[0])
	}
	if probes[1].path != "/res/v1/suggest/search" || probes[1].query.Get("count") != "1" || probes[1].query.Get("q") != "brave" {
		t.Errorf("suggest probe = %+v", probes[1])
	}
	if probes[3].path != "/res/v1/llm/context" || probes[3].query.Get("maximum_number_of_urls") != "1" ||
		probes[3].query.Get("count") != "1" || probes[3].query.Get("q") != "brave" {
		t.Errorf("llm context probe = %+v", probes[3])
	}
	if probes[4].path != "/res/v1/web/search" || probes[4].query.Get("count") != "1" || probes[4].query.Get("q") != "brave" {
		t.Errorf("web probe = %+v", probes[4])
	}
	for i, path := range map[int]string{5: "/res/v1/videos/search", 6: "/res/v1/images/search",
		7: "/res/v1/local/place_search"} {
		if probes[i].path != path || probes[i].query.Get("count") != "1" || probes[i].query.Get("q") != "brave" {
			t.Errorf("probe %d = %+v", i, probes[i])
		}
	}
}
