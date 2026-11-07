package googlepagespeed

import (
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

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The canaries stand for the key and for text a failing Google echoes. No test reaches Google: every request
// is answered by a local test server installed through the package transport.
const (
	keyValue     = "AIzaCanary0GooglePageSpeedKey0123456"
	keyEnv       = "TEST_GOOGLE_PAGESPEED_KEY"
	detailCanary = "provider-detail-canary-7c1d"
)

type recorded struct {
	method, host, path, rawQuery, body string
	header                             http.Header
}

type fakeGoogle struct {
	mu       sync.Mutex
	requests []recorded
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recorded{method: r.Method, host: r.Host, path: r.URL.EscapedPath(),
		rawQuery: r.URL.RawQuery, body: string(body), header: r.Header.Clone()})
	f.mu.Unlock()
	if f.answer != nil {
		f.answer(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if strings.Contains(r.URL.Path, "pagespeedonline") {
		fmt.Fprint(w, `{"kind":"pagespeedonline#result","id":"https://web.dev/"}`)
		return
	}
	fmt.Fprint(w, `{"record":{"key":{"origin":"https://web.dev"},"metrics":{}}}`)
}

func (f *fakeGoogle) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func reply(status int, contentType, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// errorObject is the Google error form with message and details that echo the key and a canary.
func errorObject(code int, status, reason string) string {
	return fmt.Sprintf(`{"error":{"code":%d,"message":"%s %s","status":"%s","details":[{"@type":`+
		`"type.googleapis.com/google.rpc.ErrorInfo","reason":"%s","domain":"googleapis.com",`+
		`"metadata":{"key":"%s"}}]}}`, code, detailCanary, keyValue, status, reason, detailCanary)
}

type rewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "pagespeedonline.googleapis.com" && req.URL.Host != "chromeuxreport.googleapis.com" {
		return nil, errors.New("unexpected host")
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, req.URL.Host
	return r.base.RoundTrip(clone)
}

func serve(t *testing.T, f http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	previous := transport
	transport = rewrite{target: target, base: server.Client().Transport}
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(keyValue, ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
}

// failingTransport fails every request with the error that build returns for the request, as Go's client
// does: wrapped in a *url.Error that quotes the full URL.
type failingTransport struct{ build func(*http.Request) error }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return nil, f.build(r) }

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

func coreConfig() *config.Config {
	return &config.Config{
		Version:  1,
		Services: map[string]config.Service{"pagespeed": {Provider: Provider, BaseURL: psiRoot}},
		Credentials: map[string]config.Credential{"key": {Type: config.CredentialTypeEnv,
			Values: map[string]string{roleKey: keyEnv}}},
		Connections: map[string]config.Connection{
			"read":  {Service: "pagespeed", Credential: "key"},
			"bound": {Service: "pagespeed", Credential: "key", Target: "somewhere"},
		},
	}
}

type testEnv struct {
	fake  *fakeGoogle
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, f *fakeGoogle) *testEnv {
	t.Helper()
	serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	lookup := func(name string) string {
		reads++
		if name == keyEnv {
			return keyValue
		}
		return ""
	}
	return &testEnv{fake: f, core: application.New(registry(t), coreConfig(), secret.NewWith(lookup, nil, nil, red), red),
		red: red, reads: &reads}
}

func (e *testEnv) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments)})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var failed *provider.Error
	if errors.As(err, &failed) {
		return failed.Class
	}
	return ""
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.DefaultBaseURL != psiRoot || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != "api_key" || metadata.DocPath() != "google.pagespeed/setup.md" {
		t.Fatalf("metadata = %+v", metadata)
	}
	wantTools := []string{"google.pagespeed.crux.query-history-record", "google.pagespeed.crux.query-record",
		"google.pagespeed.pagespeed.run"}
	var gotTools []string
	for _, tool := range metadata.Tools {
		gotTools = append(gotTools, tool.ID)
		if tool.Effect != config.EffectRead {
			t.Errorf("%s effect = %s", tool.ID, tool.Effect)
		}
	}
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Errorf("tools = %v, want %v", gotTools, wantTools)
	}
	if len(metadata.Profiles) != 1 || metadata.Profiles[0].ID != "read" || !metadata.Profiles[0].Recommended ||
		len(metadata.Profiles[0].Tools) != 3 {
		t.Errorf("profiles = %+v", metadata.Profiles)
	}
	for _, descriptor := range reg.Provider(Provider) {
		if descriptor.Risk != (capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity}) {
			t.Errorf("%s risk = %+v", descriptor.ID, descriptor.Risk)
		}
		if !strings.Contains(descriptor.Description, "untrusted") {
			t.Errorf("%s description lacks the untrusted note", descriptor.ID)
		}
	}
	if !strings.Contains(pagespeedRun.Description, "Google loads the page you give from the Google network") ||
		!strings.Contains(pagespeedRun.Description, "caller decides which page Google visits") {
		t.Error("pagespeed.run description does not say that Google visits the page")
	}
	if metadata.Target.Validate("x") == nil {
		t.Error("a target was accepted")
	}
	if metadata.ValidateBaseURL("https://pagespeedonline.googleapis.com.evil.example") == nil ||
		metadata.ValidateBaseURL("http://pagespeedonline.googleapis.com") == nil ||
		metadata.ValidateBaseURL("") != nil || metadata.ValidateBaseURL(psiRoot) != nil {
		t.Error("base URL validation does not pin the default")
	}
}

func TestPagespeedRunSendsEveryArgument(t *testing.T) {
	for _, strategy := range []string{"desktop", "mobile"} {
		env := newEnvironment(t, &fakeGoogle{})
		_, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/a?b=c&d=e#frag","strategy":"`+strategy+`",`+
			`"category":["performance","accessibility","best-practices","seo","pwa","agentic-browsing"],`+
			`"locale":"pt-BR","utm_campaign":"camp-1","utm_source":"qatlas"}`)
		if err != nil {
			t.Fatalf("%s: %v", strategy, err)
		}
		r := env.fake.recorded()[0]
		query, _ := url.ParseQuery(r.rawQuery)
		if r.method != http.MethodGet || r.path != "/pagespeedonline/v5/runPagespeed" ||
			r.host != "pagespeedonline.googleapis.com" || query.Get("key") != keyValue ||
			query.Get("url") != "https://web.dev/a?b=c&d=e#frag" || query.Get("strategy") != strings.ToUpper(strategy) ||
			!reflect.DeepEqual(query["category"], []string{"PERFORMANCE", "ACCESSIBILITY", "BEST_PRACTICES", "SEO", "PWA",
				"AGENTIC_BROWSING"}) || query.Get("locale") != "pt-BR" || query.Get("utm_campaign") != "camp-1" ||
			query.Get("utm_source") != "qatlas" || len(query) != 7 {
			t.Errorf("%s: request = %+v query = %v", strategy, r, query)
		}
		if r.header.Get("X-Goog-Api-Key") != "" || r.header.Get("Authorization") != "" {
			t.Error("the key travels in a header")
		}
	}
	env := newEnvironment(t, &fakeGoogle{})
	if _, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/"}`); err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery(env.fake.recorded()[0].rawQuery)
	if len(query) != 2 || query.Get("url") == "" || query.Get("key") == "" {
		t.Errorf("minimal query = %v", query)
	}
}

func TestTargetsAreCheckedBeforeTheKeyOrTheNetwork(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", maxTargetBytes)
	for name, arguments := range map[string]string{
		"no scheme":       `{"url":"web.dev/page"}`,
		"scheme relative": `{"url":"//web.dev/page"}`,
		"file":            `{"url":"file:///etc/passwd"}`,
		"ftp":             `{"url":"ftp://example.com/"}`,
		"javascript":      `{"url":"javascript:alert(1)"}`,
		"user info":       `{"url":"https://user:pass@example.com/"}`,
		"user only":       `{"url":"https://user@example.com/"}`,
		"no host":         `{"url":"https:///path"}`,
		"opaque":          `{"url":"https:example.com"}`,
		"space":           `{"url":"https://example.com/a b"}`,
		"newline":         `{"url":"https://example.com/a\nb"}`,
		"too long":        `{"url":"` + long + `"}`,
		"empty":           `{"url":""}`,
		"strategy":        `{"url":"https://example.com/","strategy":"tablet"}`,
		"category":        `{"url":"https://example.com/","category":["speed"]}`,
		"locale":          `{"url":"https://example.com/","locale":"en us"}`,
		"utm":             `{"url":"https://example.com/","utm_source":"a&key=1"}`,
		"extra":           `{"url":"https://example.com/","key":"x"}`,
		"missing":         `{}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		_, err := env.invoke(pagespeedRunID, "read", arguments)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
		if err != nil && (strings.Contains(err.Error(), "passwd") || strings.Contains(err.Error(), "pass@")) {
			t.Errorf("%s: message quotes the value: %v", name, err)
		}
	}
	for name, arguments := range map[string]string{
		"origin with path":     `{"origin":"https://example.com/path"}`,
		"origin with query":    `{"origin":"https://example.com?x=1"}`,
		"origin with fragment": `{"origin":"https://example.com#x"}`,
		"origin with user":     `{"origin":"https://u@example.com"}`,
		"origin file":          `{"origin":"file://example.com"}`,
		"url with fragment":    `{"url":"https://example.com/#x"}`,
		"both":                 `{"url":"https://example.com/","origin":"https://example.com"}`,
		"neither":              `{"form_factor":"phone"}`,
		"form factor":          `{"origin":"https://example.com","form_factor":"watch"}`,
		"metric":               `{"origin":"https://example.com","metrics":["bogus"]}`,
		"connection history":   `{"origin":"https://example.com","effective_connection_type":"4G"}`,
		"connection":           `{"origin":"https://example.com","effective_connection_type":"5G"}`,
	} {
		for _, operation := range []string{cruxQueryRecordID, cruxQueryHistoryID} {
			if name == "connection history" && operation == cruxQueryRecordID {
				continue // queryRecord takes the connection type
			}
			env := newEnvironment(t, &fakeGoogle{})
			if _, err := env.invoke(operation, "read", arguments); err == nil {
				t.Errorf("%s %s: no error", operation, name)
			}
			if len(env.fake.recorded()) != 0 || *env.reads != 0 {
				t.Errorf("%s %s: %d requests, %d secret reads", operation, name, len(env.fake.recorded()), *env.reads)
			}
		}
	}
}

// The core checks the input schema first; the handlers check the same rules again on their own.
func TestHandlersCheckArgumentsThemselves(t *testing.T) {
	resolved := &config.Resolved{BaseURL: psiRoot}
	for name, call := range map[string]struct {
		handler   capability.Handler
		arguments string
	}{
		"run":       {handlePagespeedRun, `{"url":"file:///x"}`},
		"record":    {handleQueryRecord, `{"url":"https://a.example/","origin":"https://a.example"}`},
		"history":   {handleQueryHistory, `{"origin":"https://a.example","collection_period_count":41}`},
		"zero":      {handleQueryHistory, `{"origin":"https://a.example","collection_period_count":0}`},
		"record cp": {handleQueryRecord, `{"origin":"https://a.example","collection_period_count":5}`},
	} {
		_, err := call.handler(context.Background(), resolved, nil, nil, json.RawMessage(call.arguments))
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request before any secret access", name, err)
		}
	}
}

func TestQueryRecord(t *testing.T) {
	body := `{"record":{"key":{"origin":"https://web.dev","formFactor":"PHONE"},"collectionPeriod":` +
		`{"firstDate":{"year":2026,"month":10,"day":1},"lastDate":{"year":2026,"month":10,"day":28}},` +
		`"metrics":{"cumulative_layout_shift":{"histogram":[{"start":0,"end":0.1,"density":0.9},` +
		`{"start":0.25,"density":0.01}],"percentiles":{"p75":"0.05"}},"navigation_types":{"fractions":` +
		`{"navigate":0.9,"reload":"NaN"}}}},"urlNormalizationDetails":{"originalUrl":"http://web.dev/x#a",` +
		`"normalizedUrl":"javascript:alert(1)"},"futureMember":{"a":1}}`
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
	result, err := env.invoke(cruxQueryRecordID, "read", `{"origin":"https://web.dev/","form_factor":"phone",`+
		`"effective_connection_type":"4G","metrics":["cumulative_layout_shift","navigation_types"]}`)
	if err != nil {
		t.Fatal(err)
	}
	r := env.fake.recorded()[0]
	if r.method != http.MethodPost || r.path != "/v1/records:queryRecord" || r.host != "chromeuxreport.googleapis.com" ||
		r.header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %+v", r)
	}
	if query, _ := url.ParseQuery(r.rawQuery); len(query) != 1 || query.Get("key") != keyValue {
		t.Errorf("query = %q", r.rawQuery)
	}
	if strings.Contains(r.body, keyValue) {
		t.Error("the key is in the body")
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(r.body), &sent)
	if !reflect.DeepEqual(sent, map[string]any{"origin": "https://web.dev", "formFactor": "PHONE",
		"effectiveConnectionType": "4G", "metrics": []any{"cumulative_layout_shift", "navigation_types"}}) {
		t.Errorf("body = %s", r.body)
	}
	var got struct {
		Record struct {
			Metrics map[string]struct {
				Histogram   []map[string]any
				Percentiles map[string]any
				Fractions   map[string]any
			}
		}
		URLNormalizationDetails map[string]any `json:"urlNormalizationDetails"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	clsHistogram := got.Record.Metrics["cumulative_layout_shift"].Histogram
	if len(clsHistogram) != 2 || clsHistogram[1]["end"] != nil || got.Record.Metrics["cumulative_layout_shift"].Percentiles["p75"] != "0.05" ||
		got.Record.Metrics["navigation_types"].Fractions["reload"] != "NaN" {
		t.Errorf("result = %s", result)
	}
	if got.URLNormalizationDetails["originalUrl"] != "http://web.dev/x#a" || got.URLNormalizationDetails["normalizedUrl"] != nil ||
		strings.Contains(result, "futureMember") {
		t.Errorf("normalization = %v", got.URLNormalizationDetails)
	}
}

func TestQueryHistoryRecordBounds(t *testing.T) {
	for count, ok := range map[int]bool{-1: false, 0: false, 1: true, 25: true, 40: true, 41: false} {
		env := newEnvironment(t, &fakeGoogle{})
		_, err := env.invoke(cruxQueryHistoryID, "read", fmt.Sprintf(`{"url":"https://web.dev/","collection_period_count":%d}`, count))
		if (err == nil) != ok {
			t.Errorf("collection_period_count %d: err = %v, want ok=%t", count, err, ok)
		}
		if requests := env.fake.recorded(); ok && (len(requests) != 1 ||
			!strings.Contains(requests[0].body, fmt.Sprintf(`"collectionPeriodCount":%d`, count)) ||
			requests[0].path != "/v1/records:queryHistoryRecord") || !ok && len(requests) != 0 {
			t.Errorf("collection_period_count %d: requests = %+v", count, requests)
		}
	}
	env := newEnvironment(t, &fakeGoogle{})
	if _, err := env.invoke(cruxQueryHistoryID, "read", `{"url":"https://web.dev/"}`); err != nil ||
		strings.Contains(env.fake.recorded()[0].body, "collectionPeriodCount") {
		t.Errorf("default: err = %v body = %s", err, env.fake.recorded()[0].body)
	}
}

func TestQueryHistoryRecordResult(t *testing.T) {
	var periods, densities []string
	for i := 0; i < 50; i++ {
		periods = append(periods, `{"firstDate":{"year":2026,"month":1,"day":1},"lastDate":{"year":2026,"month":1,"day":7}}`)
		densities = append(densities, `0.5`)
	}
	body := `{"record":{"key":{"url":"https://web.dev/","formFactor":"PHONE"},"collectionPeriods":[` +
		strings.Join(periods, ",") + `],"metrics":{"largest_contentful_paint":{"histogramTimeseries":[{"start":0,` +
		`"end":2500,"densities":[` + strings.Join(densities, ",") + `]}],"percentilesTimeseries":{"p75s":[` +
		strings.Join(densities, ",") + `,"NaN"]}}}}}`
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
	result, err := env.invoke(cruxQueryHistoryID, "read", `{"url":"https://web.dev/"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Record struct {
			CollectionPeriods []any
			Metrics           map[string]struct {
				HistogramTimeseries   []struct{ Densities []any }
				PercentilesTimeseries struct{ P75s []any }
			}
		}
	}
	_ = json.Unmarshal([]byte(result), &got)
	metric := got.Record.Metrics["largest_contentful_paint"]
	if len(got.Record.CollectionPeriods) != maxSeries || len(metric.HistogramTimeseries[0].Densities) != maxSeries ||
		len(metric.PercentilesTimeseries.P75s) != maxSeries {
		t.Errorf("series lengths = %d %d %d, want %d", len(got.Record.CollectionPeriods),
			len(metric.HistogramTimeseries[0].Densities), len(metric.PercentilesTimeseries.P75s), maxSeries)
	}
}

func TestCruxNotFoundIsAResultClass(t *testing.T) {
	body := `{"error":{"code":404,"message":"chrome ux report data not found","status":"NOT_FOUND"}}`
	for _, operation := range []string{cruxQueryRecordID, cruxQueryHistoryID} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(404, "application/json", body)})
		_, err := env.invoke(operation, "read", `{"origin":"https://example.com"}`)
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != provider.ClassNotFound || failed.Original == nil ||
			len(failed.Original.Entries) == 0 || failed.Original.Entries[0].Code != "NOT_FOUND" {
			t.Fatalf("%s: err = %v", operation, err)
		}
		if strings.Contains(err.Error(), "chrome ux report data not found") {
			t.Errorf("message carries provider text: %v", err)
		}
	}
}

func TestRefusalsMapToClassesAndKeepProviderTextApart(t *testing.T) {
	for _, tc := range []struct {
		status        int
		state, reason string
		class         provider.Class
	}{
		{400, "INVALID_ARGUMENT", "API_KEY_INVALID", provider.ClassAuth},
		{400, "INVALID_ARGUMENT", "", provider.ClassProviderError},
		{401, "UNAUTHENTICATED", "CREDENTIALS_MISSING", provider.ClassAuth},
		{403, "PERMISSION_DENIED", "SERVICE_DISABLED", provider.ClassPermission},
		{429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED", provider.ClassRateLimited},
		{500, "INTERNAL", "", provider.ClassProviderError},
		{503, "UNAVAILABLE", "", provider.ClassUnreachable},
		{504, "DEADLINE_EXCEEDED", "", provider.ClassTimeout},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", errorObject(tc.status, tc.state, tc.reason))})
		_, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/"}`)
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != tc.class || failed.Original == nil || failed.Original.Status != tc.status {
			t.Fatalf("HTTP %d %s: err = %v, want class %q with Original", tc.status, tc.reason, err, tc.class)
		}
		for _, leak := range []string{detailCanary, keyValue, tc.state, tc.reason} {
			if leak != "" && strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: message %q carries %q", tc.status, err.Error(), leak)
			}
		}
		if len(env.fake.recorded()) != 1 {
			t.Errorf("HTTP %d: %d requests, a request is sent once", tc.status, len(env.fake.recorded()))
		}
	}
}

func TestRateLimitedWithoutRetryAfterHoldsOneSecondAndNeverRepeats(t *testing.T) {
	f := &fakeGoogle{answer: reply(429, "application/json", errorObject(429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED"))}
	serve(t, f)
	var waits []time.Duration
	now := time.Unix(1000, 0)
	limiter := ratelimit.New(0, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	resolver := secret.NewWith(func(string) string { return keyValue }, nil, nil, &redact.Redactor{})
	client, err := open(context.Background(), &config.Resolved{BaseURL: psiRoot,
		Credential: "key", Secrets: coreConfig().Credentials["key"]}, resolver, &redact.Redactor{}, limiter)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var out psiResult
	err = client.send(context.Background(), apiCall{op: "run pagespeed", method: "GET", root: psiRoot, path: psiPath,
		query: url.Values{"url": {"https://web.dev/"}}, limit: maxPSIBytes}, &out)
	if classOf(err) != provider.ClassRateLimited {
		t.Fatalf("err = %v", err)
	}
	if len(f.recorded()) != 1 {
		t.Fatalf("requests = %d, want one", len(f.recorded()))
	}
	if err := limiter.Wait(context.Background()); err != nil || len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("next slot waits = %v, err = %v, want one second", waits, err)
	}
}

func TestUnusableAnswers(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"invalid json":  reply(200, "application/json", `{"id":`),
		"not an object": reply(200, "application/json", `[1,2]`),
		"html":          reply(200, "text/html", `<html>`+detailCanary+`</html>`),
		"no type":       reply(200, "", `{"id":"x"}`),
		"over the cap": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"`+strings.Repeat("x", maxPSIBytes)+`"}`)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/", http.StatusFound)
		},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: answer})
		_, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/"}`)
		want := provider.ClassInvalidResponse
		if name == "redirect" {
			want = provider.ClassProviderError
		}
		if classOf(err) != want {
			t.Errorf("%s: err = %v, want class %q", name, err, want)
		}
		if err != nil && strings.Contains(err.Error(), detailCanary) {
			t.Errorf("%s: message carries provider text", name)
		}
		if len(env.fake.recorded()) != 1 {
			t.Errorf("%s: requests = %d", name, len(env.fake.recorded()))
		}
	}
	env := newEnvironment(t, &fakeGoogle{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"record":{"key":{"origin":"`+strings.Repeat("x", maxCruxBytes)+`"}}}`)
	}})
	if _, err := env.invoke(cruxQueryRecordID, "read", `{"origin":"https://web.dev"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("crux over the cap: err = %v", err)
	}
}

func TestTheKeyNeverReachesAMessageEvenWhenTheTransportQuotesTheURL(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{})
	quote := func(inner error) func(*http.Request) error {
		return func(r *http.Request) error { return &url.Error{Op: r.Method, URL: r.URL.String(), Err: inner} }
	}
	for name, tc := range map[string]struct {
		build func(*http.Request) error
		class provider.Class
	}{
		"deadline":    {quote(context.DeadlineExceeded), provider.ClassTimeout},
		"refused":     {quote(fmt.Errorf("dial tcp: connection refused")), provider.ClassUnreachable},
		"echoed text": {quote(fmt.Errorf("proxy rejected %s", keyValue)), provider.ClassUnreachable},
	} {
		transport = failingTransport{build: tc.build}
		for _, operation := range []string{pagespeedRunID, cruxQueryRecordID, cruxQueryHistoryID} {
			arguments := `{"url":"https://web.dev/"}`
			result, err := env.invoke(operation, "read", arguments)
			if classOf(err) != tc.class {
				t.Fatalf("%s %s: err = %v, want class %q", name, operation, err, tc.class)
			}
			text := fmt.Sprintf("%v %+v %s", err, err, result)
			if strings.Contains(text, keyValue) || strings.Contains(text, "key=") {
				t.Errorf("%s %s: the key or the URL reached %q", name, operation, text)
			}
		}
	}
	if timeout >= 60*time.Second {
		t.Errorf("timeout %v is not below the invoke limit", timeout)
	}
	if got := env.red.Apply("x " + keyValue); strings.Contains(got, keyValue) {
		t.Error("the key is not registered with the redactor")
	}
}

func TestConnectionRefusals(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{})
	if _, err := env.invoke(pagespeedRunID, "bound", `{"url":"https://web.dev/"}`); err == nil {
		t.Error("a connection with a target was served")
	}
	if len(env.fake.recorded()) != 0 {
		t.Error("a request was sent")
	}
	resolved := &config.Resolved{BaseURL: "https://pagespeedonline.googleapis.com.evil.example"}
	if _, err := Open(context.Background(), resolved, nil, nil); classOf(err) != provider.ClassProviderError {
		t.Errorf("foreign base URL: err = %v", err)
	}
}

func TestConnectionTest(t *testing.T) {
	resolved := &config.Resolved{BaseURL: psiRoot, Credential: "key", Secrets: coreConfig().Credentials["key"]}
	for name, tc := range map[string]struct {
		answer func(http.ResponseWriter, *http.Request)
		class  provider.Class
	}{
		"success":         {nil, provider.ClassOK},
		"not found":       {reply(404, "application/json", `{"error":{"code":404,"status":"NOT_FOUND"}}`), provider.ClassOK},
		"invalid key":     {reply(400, "application/json", errorObject(400, "INVALID_ARGUMENT", "API_KEY_INVALID")), provider.ClassAuth},
		"api not enabled": {reply(403, "application/json", errorObject(403, "PERMISSION_DENIED", "SERVICE_DISABLED")), provider.ClassPermission},
		"quota":           {reply(429, "application/json", errorObject(429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED")), provider.ClassRateLimited},
		"not json":        {reply(200, "text/html", `<html>`), provider.ClassInvalidResponse},
	} {
		f := &fakeGoogle{answer: tc.answer}
		serve(t, f)
		red := &redact.Redactor{}
		class, err := TestConnection(context.Background(), resolved,
			secret.NewWith(func(string) string { return keyValue }, nil, nil, red), red)
		if err != nil || class != tc.class {
			t.Errorf("%s: class = %q, err = %v, want %q", name, class, err, tc.class)
		}
		requests := f.recorded()
		if len(requests) != 1 || requests[0].method != http.MethodPost || requests[0].path != "/v1/records:queryRecord" ||
			requests[0].host != "chromeuxreport.googleapis.com" ||
			requests[0].body != `{"origin":"`+probeOrigin+`","metrics":["first_contentful_paint"]}` {
			t.Errorf("%s: requests = %+v", name, requests)
		}
	}
	// A connection with a foreign origin is never tested.
	if class, err := TestConnection(context.Background(), &config.Resolved{BaseURL: "https://example.com"}, nil, nil); err != nil ||
		class != provider.ClassProviderError {
		t.Errorf("foreign origin: class = %q, err = %v", class, err)
	}
}

// A realistic Lighthouse answer: the large parts must not reach the result, the texts are cut, the lists are
// capped, and what remains validates against the output schema.
func TestRunResultIsBoundedAndMatchesItsSchema(t *testing.T) {
	big := strings.Repeat("x", 3000)
	var audits, refs, entities []string
	for i := 0; i < 250; i++ {
		audits = append(audits, fmt.Sprintf(`"audit-%03d":{"id":"audit-%03d","title":"%s","description":"%s","score":0.5,`+
			`"scoreDisplayMode":"numeric","numericValue":12.5,"numericUnit":"millisecond","displayValue":"1.2 s",`+
			`"metricSavings":{"LCP":100,"FCP":0},"warnings":["w"],"details":{"type":"opportunity","overallSavingsMs":300,`+
			`"overallSavingsBytes":1024,"items":[{"url":"https://a.example/x","data":"%s"},{}],"debugData":{"x":1}}}`,
			i, i, big, big, big))
	}
	for i := 0; i < 250; i++ {
		refs = append(refs, fmt.Sprintf(`{"id":"audit-%03d","weight":1,"group":"metrics","relevantAudits":["a"]}`, i))
	}
	for i := 0; i < 150; i++ {
		entities = append(entities, fmt.Sprintf(`{"name":"e%d","origins":["https://e.example"],"isFirstParty":false}`, i))
	}
	body := `{"kind":"pagespeedonline#result","captchaResult":"CAPTCHA_NOT_NEEDED","id":"https://web.dev/",` +
		`"loadingExperience":{"id":"https://web.dev/","metrics":{"LARGEST_CONTENTFUL_PAINT_MS":{"percentile":1200,` +
		`"category":"FAST","distributions":[{"min":0,"max":2500,"proportion":0.9},{"min":2500,"proportion":0.1}]}},` +
		`"overall_category":"FAST","initial_url":"https://web.dev/"},"originLoadingExperience":{"id":"https://web.dev",` +
		`"origin_fallback":true},"analysisUTCTimestamp":"2026-11-06T12:00:00.000Z","version":{"major":"13","minor":"0"},` +
		`"lighthouseResult":{"requestedUrl":"https://web.dev/","finalUrl":"https://web.dev/","mainDocumentUrl":"javascript:x",` +
		`"lighthouseVersion":"13.0.0","userAgent":"UA","fetchTime":"2026-11-06T12:00:00.000Z","runWarnings":["` + big + `"],` +
		`"fullPageScreenshot":{"screenshot":{"data":"data:image/webp;base64,` + big + `"}},"i18n":{"rendererFormattedStrings":{"a":"b"}},` +
		`"configSettings":{"formFactor":"mobile","locale":"en-US","onlyCategories":["performance","seo"],"emulatedFormFactor":"mobile"},` +
		`"timing":{"total":4321},"environment":{"hostUserAgent":"H","benchmarkIndex":1500,"credits":{"axe-core":"4.10"}},` +
		`"categories":{"performance":{"id":"performance","title":"Performance","score":0.91,"auditRefs":[` + strings.Join(refs, ",") +
		`]},"unknown-category":{"id":"x"}},"categoryGroups":{"metrics":{"title":"Metrics"}},` +
		`"entities":[` + strings.Join(entities, ",") + `],"stackPacks":[{"id":"wordpress","title":"WordPress",` +
		`"iconDataURL":"data:image/svg+xml;base64,` + big + `","descriptions":{"a":"b"}}],"audits":{` + strings.Join(audits, ",") +
		`,"final-screenshot":{"id":"final-screenshot","title":"Screenshot","details":{"type":"screenshot","data":"data:image/jpeg;base64,` +
		big + `"}},"screenshot-thumbnails":{"id":"screenshot-thumbnails","details":{"type":"filmstrip","items":[{"data":"` + big + `"}]}}}}}`
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
	result, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/","category":["performance"]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"fullPageScreenshot", "base64", "i18n", "iconDataURL", "emulatedFormFactor", "debugData",
		"unknown-category", "javascript:"} {
		if strings.Contains(result, leak) {
			t.Errorf("result contains %q", leak)
		}
	}
	if len(result) > 700<<10 {
		t.Errorf("result is %d bytes", len(result))
	}
	var got psiResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	l := got.Lighthouse
	if len(l.Audits) != 252 || len(l.Categories["performance"].AuditRefs) != maxAuditRefs ||
		len(l.Entities) != maxEntities || len(l.RunWarnings) != 1 || len([]rune(l.RunWarnings[0])) != 501 {
		t.Errorf("caps: audits %d refs %d entities %d warnings %v", len(l.Audits), len(l.Categories["performance"].AuditRefs),
			len(l.Entities), len(l.RunWarnings))
	}
	a := l.Audits["audit-000"]
	if len([]rune(a.Title)) != maxTitleRunes+1 || len([]rune(a.Description)) != maxTextRunes+1 ||
		*a.MetricSavings.LCP != 100 {
		t.Errorf("audit = %+v", a)
	}
	// The detail summary is read from the raw result: its members are output names, not Google's.
	var raw struct {
		LighthouseResult struct {
			Audits map[string]struct{ Details map[string]any }
		}
	}
	_ = json.Unmarshal([]byte(result), &raw)
	if !reflect.DeepEqual(raw.LighthouseResult.Audits["audit-000"].Details, map[string]any{"type": "opportunity",
		"overallSavingsMs": 300.0, "overallSavingsBytes": 1024.0, "itemCount": 2.0}) ||
		!reflect.DeepEqual(raw.LighthouseResult.Audits["final-screenshot"].Details, map[string]any{"type": "screenshot"}) ||
		!reflect.DeepEqual(raw.LighthouseResult.Audits["screenshot-thumbnails"].Details,
			map[string]any{"type": "filmstrip", "itemCount": 1.0}) {
		t.Errorf("details = %v", raw.LighthouseResult.Audits["audit-000"].Details)
	}
	if got.LoadingExperience.Metrics["LARGEST_CONTENTFUL_PAINT_MS"].Distributions[1].Max != nil ||
		got.OriginLoadingExperience.OriginFallback == nil || !*got.OriginLoadingExperience.OriginFallback {
		t.Errorf("loading experience = %+v", got.LoadingExperience)
	}
	validate(t, pagespeedRun.OutputSchema, result)
}

func TestAuditsBeyondTheCapAreDropped(t *testing.T) {
	var audits []string
	for i := 0; i < maxAudits+50; i++ {
		audits = append(audits, fmt.Sprintf(`"audit-%03d":{"id":"audit-%03d"}`, i, i))
	}
	body := `{"lighthouseResult":{"audits":{` + strings.Join(audits, ",") + `}}}`
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
	result, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got psiResult
	_ = json.Unmarshal([]byte(result), &got)
	if _, ok := got.Lighthouse.Audits[fmt.Sprintf("audit-%03d", maxAudits-1)]; len(got.Lighthouse.Audits) != maxAudits || !ok ||
		got.Lighthouse.Audits[fmt.Sprintf("audit-%03d", maxAudits)] != nil {
		t.Errorf("audits kept = %d, not the first %d in key order", len(got.Lighthouse.Audits), maxAudits)
	}
}

func TestCruxResultMatchesItsSchema(t *testing.T) {
	record := `{"record":{"key":{"url":"https://web.dev/","formFactor":"PHONE","effectiveConnectionType":"4G"},` +
		`"collectionPeriod":{"firstDate":{"year":2026,"month":10,"day":1},"lastDate":{"year":2026,"month":10,"day":28}},` +
		`"metrics":{"m":{"histogram":[{"start":0,"end":1,"density":0.5}],"percentiles":{"p75":10},"fractions":{"a":0.5}}}},` +
		`"urlNormalizationDetails":{"originalUrl":"http://web.dev","normalizedUrl":"https://web.dev/"}}`
	history := `{"record":{"key":{"origin":"https://web.dev"},"collectionPeriods":[{"firstDate":{"year":2026,"month":1,"day":1},` +
		`"lastDate":{"year":2026,"month":1,"day":7}}],"metrics":{"m":{"histogramTimeseries":[{"start":0,"end":1,"densities":[0.5]}],` +
		`"percentilesTimeseries":{"p75s":[1,"0.1","NaN"]},"fractionTimeseries":{"a":{"fractions":[0.5,"NaN"]}}}}}}`
	for operation, body := range map[string]string{cruxQueryRecordID: record, cruxQueryHistoryID: history} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
		result, err := env.invoke(operation, "read", `{"url":"https://web.dev/"}`)
		if err != nil {
			t.Fatal(err)
		}
		descriptor, _, _ := registry(t).Lookup(operation)
		validate(t, descriptor.OutputSchema, result)
		if len(result) < 60 {
			t.Errorf("%s: result = %s", operation, result)
		}
	}
}

// validate checks a JSON value against the small schema vocabulary this package emits.
func validate(t *testing.T, schema json.RawMessage, value string) {
	t.Helper()
	var s, v any
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		t.Fatal(err)
	}
	if problem := check(s.(map[string]any), v, "$"); problem != "" {
		t.Errorf("result does not match its schema: %s", problem)
	}
}

func check(schema map[string]any, value any, path string) string {
	if options, ok := schema["anyOf"].([]any); ok {
		for _, option := range options {
			if check(option.(map[string]any), value, path) == "" {
				return ""
			}
		}
		return path + ": no alternative fits"
	}
	switch schema["type"] {
	case "string":
		if _, ok := value.(string); !ok {
			return path + ": not a string"
		}
	case "number", "integer":
		if _, ok := value.(float64); !ok {
			return path + ": not a number"
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return path + ": not a boolean"
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return path + ": not an array"
		}
		for i, item := range items {
			if p := check(schema["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	case "object":
		members, ok := value.(map[string]any)
		if !ok {
			return path + ": not an object"
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, member := range members {
			sub, known := properties[name].(map[string]any)
			if !known {
				extra, _ := schema["additionalProperties"].(map[string]any)
				if extra == nil {
					return path + "." + name + ": undeclared member"
				}
				sub = extra
			}
			if p := check(sub, member, path+"."+name); p != "" {
				return p
			}
		}
	}
	return ""
}

func TestNothingLogsTheKey(t *testing.T) {
	f := &fakeGoogle{answer: reply(403, "application/json", errorObject(403, "PERMISSION_DENIED", ""))}
	env := newEnvironment(t, f)
	_, err := env.invoke(pagespeedRunID, "read", `{"url":"https://web.dev/"}`)
	if err == nil || strings.Contains(err.Error(), keyValue) {
		t.Fatalf("err = %v", err)
	}
	var failed *provider.Error
	if errors.As(err, &failed) {
		for _, entry := range failed.Original.Entries {
			// The original is provider text; the core redacts it. The redactor must know the key by now.
			if got := env.red.Apply(entry.Message); strings.Contains(got, keyValue) {
				t.Errorf("redactor leaves the key in %q", got)
			}
		}
	}
}
