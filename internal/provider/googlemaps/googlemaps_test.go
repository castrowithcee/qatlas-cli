package googlemaps

import (
	"bytes"
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
	keyValue     = "AIzaCanary0GoogleMapsKey0123456789ab"
	keyEnv       = "TEST_GOOGLE_MAPS_KEY"
	detailCanary = "provider-detail-canary-7c1d"
	placeID      = "ChIJj61dQgK6j4AR4GeTYWZsKWw"
)

type recorded struct {
	method, path, rawQuery, body string
	header                       http.Header
}

type fakeGoogle struct {
	mu       sync.Mutex
	requests []recorded
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(payload))
	f.mu.Lock()
	f.requests = append(f.requests, recorded{method: r.Method, path: r.URL.EscapedPath(), rawQuery: r.URL.RawQuery,
		body: string(payload), header: r.Header.Clone()})
	f.mu.Unlock()
	if f.answer != nil {
		f.answer(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	fmt.Fprint(w, `{"name":"places/`+placeID+`","id":"`+placeID+`"}`)
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
	if req.URL.Host != "places.googleapis.com" && req.URL.Host != "maps.googleapis.com" {
		return nil, errors.New("unexpected host")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("X-Test-Original-Host", req.URL.Host)
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, ""
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

// failingTransport fails every request with err.
type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

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
		Services: map[string]config.Service{"maps": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"maps-key": {Type: config.CredentialTypeEnv,
			Values: map[string]string{roleKey: keyEnv}}},
		Connections: map[string]config.Connection{
			"read":  {Service: "maps", Credential: "maps-key"},
			"bound": {Service: "maps", Credential: "maps-key", Target: "somewhere"},
		},
	}
}

type environment struct {
	fake  *fakeGoogle
	core  *application.Core
	red   *redact.Redactor
	reads *int
	t     *testing.T
}

func newEnvironment(t *testing.T, f *fakeGoogle) *environment {
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
	return &environment{fake: f, core: application.New(registry(t), coreConfig(), secret.NewWith(lookup, nil, nil, red), red),
		red: red, reads: &reads, t: t}
}

func (e *environment) invoke(connection, arguments string) (string, error) {
	return e.invokeTool(placesGetID, connection, arguments)
}

func (e *environment) invokeTool(operation, connection, arguments string) (string, error) {
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

func TestRegisterPublishesMetadataAndTool(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.DefaultBaseURL != "https://places.googleapis.com" || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != "api_key" || metadata.DocPath() != "google.maps/setup.md" {
		t.Fatalf("metadata = %+v", metadata)
	}
	var ids []string
	for _, tool := range metadata.Tools {
		ids = append(ids, tool.ID)
		if tool.Effect != config.EffectRead {
			t.Errorf("%s effect = %v", tool.ID, tool.Effect)
		}
	}
	wantTools := []string{"google.maps.geocoding.geocode", "google.maps.geocoding.reverse", "google.maps.photos.get",
		"google.maps.places.autocomplete", "google.maps.places.get", "google.maps.places.search-nearby",
		"google.maps.places.search-text"}
	if !reflect.DeepEqual(ids, wantTools) {
		t.Errorf("tools = %v", ids)
	}
	if len(metadata.Profiles) != 1 || metadata.Profiles[0].ID != "read" || !metadata.Profiles[0].Recommended ||
		!reflect.DeepEqual(metadata.Profiles[0].Tools, []string{"google.maps.places.get",
			"google.maps.places.search-text", "google.maps.places.search-nearby", "google.maps.places.autocomplete",
			"google.maps.photos.get", "google.maps.geocoding.geocode", "google.maps.geocoding.reverse"}) {
		t.Errorf("profiles = %+v", metadata.Profiles)
	}
	for _, descriptor := range reg.Provider(Provider) {
		sensitivity := dataSensitivity
		if strings.HasPrefix(descriptor.ID, "google.maps.geocoding.") {
			sensitivity = geocodingSensitivity
		}
		if descriptor.Risk != (capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sensitivity}) {
			t.Errorf("%s risk = %+v", descriptor.ID, descriptor.Risk)
		}
		for _, want := range []string{"SKU", "attribution", "untrusted", "Place IDs may be stored"} {
			if !strings.Contains(descriptor.Description, want) {
				t.Errorf("%s description lacks %q", descriptor.ID, want)
			}
		}
		if descriptor.ID == placesGetID {
			for _, want := range []string{"10000", "5000", "1000"} {
				if !strings.Contains(descriptor.Description, want) {
					t.Errorf("description lacks %q", want)
				}
			}
		} else if !strings.Contains(descriptor.Description, "charges above the free usage") {
			t.Errorf("%s description lacks the cost note", descriptor.ID)
		}
	}
	if err := metadata.Target.Validate("x"); err == nil {
		t.Error("a target was accepted")
	}
	if metadata.ValidateBaseURL("https://places.googleapis.com.evil.example") == nil ||
		metadata.ValidateBaseURL("http://places.googleapis.com") == nil || metadata.ValidateBaseURL("") != nil {
		t.Error("base URL validation does not pin the official origin")
	}
}

func TestPlacesGetWithIDMask(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{})
	result, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var got map[string]any
	if json.Unmarshal([]byte(result), &got) != nil || got["id"] != placeID {
		t.Fatalf("result = %s", result)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodGet || r.path != "/v1/places/"+placeID || r.rawQuery != "" ||
		r.header.Get("X-Goog-FieldMask") != "id" || r.header.Get("Accept") != "application/json" ||
		r.header.Get("X-Goog-Api-Key") != keyValue {
		t.Errorf("request = %+v", r)
	}
	if strings.Contains(r.path+r.rawQuery, keyValue) || strings.Contains(result, keyValue) {
		t.Error("the key reached the URL or the result")
	}
	if strings.Contains(env.red.Apply("x "+keyValue), keyValue) {
		t.Error("the key is not registered with the redactor")
	}
}

func TestPlacesGetWithMaskOverSeveralGroups(t *testing.T) {
	body := `{"id":"` + placeID + `","displayName":{"text":"Googleplex","languageCode":"en"},` +
		`"rating":4.5,"userRatingCount":10,"websiteUri":"https://example.com/","googleMapsUri":"javascript:alert(1)",` +
		`"reviews":[{"text":{"text":"` + strings.Repeat("x", 2000) + `"},"rating":5,"authorAttribution":{"displayName":"A","uri":"https://u.example/a"}}],` +
		`"unknownFutureField":{"a":1},"deprecatedThing":"x"}`
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
	result, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":["id","displayName","rating","reviews"],`+
		`"language_code":"pt-BR","region_code":"us","session_token":"abc-DEF_123"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r := env.fake.recorded()[0]
	if r.header.Get("X-Goog-FieldMask") != "id,displayName,rating,reviews" {
		t.Errorf("mask header = %q", r.header.Get("X-Goog-FieldMask"))
	}
	query, _ := url.ParseQuery(r.rawQuery)
	if query.Get("languageCode") != "pt-BR" || query.Get("regionCode") != "US" || query.Get("sessionToken") != "abc-DEF_123" ||
		len(query) != 3 {
		t.Errorf("query = %v", query)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(result), &got)
	if _, leaked := got["unknownFutureField"]; leaked || got["googleMapsUri"] != nil || got["websiteUri"] != "https://example.com/" {
		t.Errorf("result = %s", result)
	}
	review := got["reviews"].([]any)[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	if len([]rune(review)) != maxTextRunes+1 || !strings.HasSuffix(review, "…") {
		t.Errorf("review length = %d", len([]rune(review)))
	}
}

func TestFieldMaskTiers(t *testing.T) {
	for raw, want := range map[string]Tier{
		`"id"`:                                  TierIDsOnly,
		`"id,photos"`:                           TierIDsOnly,
		`["id","formattedAddress"]`:             TierEssentials,
		`["id","displayName"]`:                  TierPro,
		`"rating, id"`:                          TierEnterprise,
		`["displayName","reviews","rating"]`:    TierAtmosphere,
		`["navigationPoints","entrances","id"]`: TierPro,
	} {
		mask, err := parseFieldMask(json.RawMessage(raw))
		if err != nil || mask.tier != want {
			t.Errorf("%s: tier = %v, err = %v, want %v", raw, mask.tier, err, want)
		}
	}
	mask, _ := parseFieldMask(json.RawMessage(`"id, id ,displayName"`))
	if mask.header != "id,displayName" {
		t.Errorf("header = %q", mask.header)
	}
}

func TestFieldMaskRefusalsCarryNoValue(t *testing.T) {
	for name, raw := range map[string]string{
		"missing":         ``,
		"null":            `null`,
		"empty string":    `""`,
		"empty entry":     `"id,,name"`,
		"empty list":      `[]`,
		"wildcard":        `"*"`,
		"wildcard list":   `["id","*"]`,
		"wildcard part":   `["places.*"]`,
		"nested":          `["displayName.text"]`,
		"prefixed":        `["places.id"]`,
		"unknown":         `["secretCanaryField"]`,
		"search only":     `["routingSummaries"]`,
		"case":            `["DisplayName"]`,
		"inner space":     `"id displayName"`,
		"wrong type":      `7`,
		"non-string item": `[1]`,
	} {
		_, err := parseFieldMask(json.RawMessage(raw))
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
			continue
		}
		if strings.Contains(err.Error(), "secretCanaryField") || strings.Contains(err.Error(), "DisplayName") {
			t.Errorf("%s: message quotes the value: %v", name, err)
		}
	}
}

func TestRefusedArgumentsNeverReachTheKeyOrTheNetwork(t *testing.T) {
	for name, arguments := range map[string]string{
		"no mask":       `{"place_id":"` + placeID + `"}`,
		"wildcard":      `{"place_id":"` + placeID + `","field_mask":"*"}`,
		"unknown field": `{"place_id":"` + placeID + `","field_mask":["nope"]}`,
		"resource name": `{"place_id":"places/` + placeID + `","field_mask":"id"}`,
		"dot dot":       `{"place_id":"..","field_mask":"id"}`,
		"slash":         `{"place_id":"a/b","field_mask":"id"}`,
		"query":         `{"place_id":"a?key=1","field_mask":"id"}`,
		"encoded":       `{"place_id":"a%2Fb","field_mask":"id"}`,
		"empty id":      `{"place_id":"","field_mask":"id"}`,
		"bad language":  `{"place_id":"` + placeID + `","field_mask":"id","language_code":"en us"}`,
		"bad region":    `{"place_id":"` + placeID + `","field_mask":"id","region_code":"USA"}`,
		"bad session":   `{"place_id":"` + placeID + `","field_mask":"id","session_token":"a b"}`,
		"extra":         `{"place_id":"` + placeID + `","field_mask":"id","key":"x"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invoke("read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
}

// The core checks the input schema first; the handler checks the same rules again on its own.
func TestHandlerChecksArgumentsItself(t *testing.T) {
	resolved := &config.Resolved{BaseURL: apiRoot}
	for _, arguments := range []string{
		`{"place_id":"a/b","field_mask":"id"}`, `{"place_id":"..","field_mask":"id"}`,
		`{"place_id":"` + placeID + `","field_mask":"*"}`, `{"place_id":"` + placeID + `"}`,
		`{"place_id":"` + placeID + `","field_mask":"id","region_code":"USA"}`,
	} {
		_, err := handlePlacesGet(context.Background(), resolved, nil, nil, json.RawMessage(arguments))
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request before any secret access", arguments, err)
		}
	}
}

func TestRefusalsMapToClassesAndKeepProviderTextApart(t *testing.T) {
	for _, tc := range []struct {
		status        int
		state, reason string
		class         provider.Class
	}{
		{401, "UNAUTHENTICATED", "CREDENTIALS_MISSING", provider.ClassAuth},
		{403, "PERMISSION_DENIED", "SOMETHING_UNDOCUMENTED", provider.ClassPermission},
		{429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED", provider.ClassRateLimited},
		{400, "INVALID_ARGUMENT", "", provider.ClassInvalidResponse},
		{404, "NOT_FOUND", "", provider.ClassNotFound},
		{403, "UNDOCUMENTED", "API_KEY_INVALID", provider.ClassPermission},
		{500, "INTERNAL", "", provider.ClassProviderError},
		{503, "UNAVAILABLE", "", provider.ClassUnreachable},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", errorObject(tc.status, tc.state, tc.reason))})
		_, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`)
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != tc.class || failed.Original == nil ||
			failed.Original.Status != tc.status {
			t.Fatalf("HTTP %d %s: err = %v, want class %q with Original", tc.status, tc.state, err, tc.class)
		}
		if strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue) ||
			strings.Contains(err.Error(), tc.state) || (tc.reason != "" && strings.Contains(err.Error(), tc.reason)) {
			t.Errorf("HTTP %d: message %q carries provider text", tc.status, err.Error())
		}
		if len(failed.Original.Entries) == 0 || failed.Original.Entries[0].Code != tc.state {
			t.Errorf("HTTP %d: original = %+v", tc.status, failed.Original)
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
	client, err := open(context.Background(), &config.Resolved{BaseURL: apiRoot,
		Credential: "maps-key", Secrets: coreConfig().Credentials["maps-key"]}, resolver, &redact.Redactor{}, limiter)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := client.get(context.Background(), "get place", "/v1/places/"+placeID, nil, "id"); classOf(err) != provider.ClassRateLimited {
		t.Fatalf("err = %v", err)
	}
	if len(f.recorded()) != 1 {
		t.Fatalf("requests = %d, want one", len(f.recorded()))
	}
	if err := limiter.Wait(context.Background()); err != nil || len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("next slot waits = %v, err = %v, want one second", waits, err)
	}
}

func TestLegacyStatus(t *testing.T) {
	for _, tc := range []struct {
		body  string
		want  string
		class provider.Class
	}{
		{`{"status":"OK","results":[]}`, "OK", ""},
		{`{"status":"ZERO_RESULTS"}`, "ZERO_RESULTS", ""},
		{`{"status":"OVER_QUERY_LIMIT"}`, "OVER_QUERY_LIMIT", provider.ClassRateLimited},
		{`{"status":"REQUEST_DENIED","error_message":"` + detailCanary + ` ` + keyValue + `"}`, "REQUEST_DENIED", provider.ClassAuth},
		{`{"status":"INVALID_REQUEST"}`, "INVALID_REQUEST", provider.ClassInvalidResponse},
		{`{"status":"UNKNOWN_ERROR"}`, "UNKNOWN_ERROR", provider.ClassProviderError},
		{`{"status":"SOMETHING_NEW"}`, "SOMETHING_NEW", provider.ClassInvalidResponse},
		{`{"results":[]}`, "", provider.ClassInvalidResponse},
		{`not json`, "", provider.ClassInvalidResponse},
	} {
		status, err := legacyStatus("geocode", []byte(tc.body))
		if status != tc.want {
			t.Errorf("%s: status = %q", tc.body, status)
		}
		switch {
		case tc.class == "" && err != nil:
			t.Errorf("%s: err = %v", tc.body, err)
		case tc.class != "" && (err == nil || err.Class != tc.class):
			t.Errorf("%s: err = %v, want class %q", tc.body, err, tc.class)
		}
		if err != nil && (strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue)) {
			t.Errorf("%s: message carries provider text: %v", tc.body, err)
		}
	}
	_, err := legacyStatus("geocode", []byte(`{"status":"REQUEST_DENIED","error_message":"`+detailCanary+`"}`))
	if err.Original == nil || len(err.Original.Entries) != 1 || err.Original.Entries[0].Code != "REQUEST_DENIED" ||
		err.Original.Entries[0].Message != detailCanary {
		t.Errorf("original = %+v", err.Original)
	}
}

func TestUnusableAnswers(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"invalid json":  reply(200, "application/json", `{"id":`),
		"not an object": reply(200, "application/json", `[1,2]`),
		"null":          reply(200, "application/json", `null`),
		"html":          reply(200, "text/html", `<html>`+detailCanary+`</html>`),
		"no type":       reply(200, "", `{"id":"x"}`),
		"too large": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"a":"`+strings.Repeat("x", maxResponseBytes)+`"}`)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/", http.StatusFound)
		},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: answer})
		_, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`)
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
}

func TestTimeoutAndUnreachable(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{})
	transport = failingTransport{err: context.DeadlineExceeded}
	if _, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`); classOf(err) != provider.ClassTimeout {
		t.Errorf("timeout: err = %v", err)
	}
	transport = failingTransport{err: fmt.Errorf("dial %s failed", keyValue)}
	_, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`)
	if classOf(err) != provider.ClassUnreachable || strings.Contains(err.Error(), keyValue) {
		t.Errorf("unreachable: err = %v", err)
	}
	if defaultTimeout >= 60*time.Second {
		t.Errorf("timeout %v is not below the invoke limit", defaultTimeout)
	}
}

func TestConnectionRefusals(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{})
	if _, err := env.invoke("bound", `{"place_id":"`+placeID+`","field_mask":"id"}`); err == nil {
		t.Error("a connection with a target was served")
	}
	if len(env.fake.recorded()) != 0 {
		t.Error("a request was sent")
	}
	resolved := &config.Resolved{BaseURL: "https://places.googleapis.com.evil.example"}
	if _, err := Open(context.Background(), resolved, nil, nil); classOf(err) != provider.ClassProviderError {
		t.Errorf("foreign base URL: err = %v", err)
	}
}

func TestConnectionTest(t *testing.T) {
	resolved := &config.Resolved{BaseURL: apiRoot, Credential: "maps-key", Secrets: coreConfig().Credentials["maps-key"]}
	for name, tc := range map[string]struct {
		answer func(http.ResponseWriter, *http.Request)
		class  provider.Class
	}{
		"success":         {nil, provider.ClassOK},
		"invalid key":     {reply(401, "application/json", errorObject(401, "UNAUTHENTICATED", "CREDENTIALS_MISSING")), provider.ClassAuth},
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
		if len(requests) != 1 || requests[0].path != "/v1/places/"+probePlaceID || requests[0].rawQuery != "" ||
			requests[0].header.Get("X-Goog-FieldMask") != "id" || requests[0].header.Get("X-Goog-Api-Key") != keyValue {
			t.Errorf("%s: requests = %+v", name, requests)
		}
	}
	// A connection with a target or a foreign origin is never tested.
	if class, err := TestConnection(context.Background(), &config.Resolved{BaseURL: "https://example.com"}, nil, nil); err != nil ||
		class != provider.ClassProviderError {
		t.Errorf("foreign origin: class = %q, err = %v", class, err)
	}
}

func TestNothingLogsTheKey(t *testing.T) {
	f := &fakeGoogle{answer: reply(403, "application/json", errorObject(403, "PERMISSION_DENIED", ""))}
	env := newEnvironment(t, f)
	_, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"id"}`)
	if err == nil || strings.Contains(err.Error(), keyValue) {
		t.Fatalf("err = %v", err)
	}
	if got := env.red.Apply(keyValue); strings.Contains(got, keyValue) {
		t.Errorf("redactor leaves the key: %q", got)
	}
}
