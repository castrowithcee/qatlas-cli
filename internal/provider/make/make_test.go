package makeapi

import (
	"context"
	"encoding/json"
	"errors"
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

// The canaries stand for the API token, for a team, organization, and scenario outside a restricted
// connection, and for a name a provider body should never leak. No test reaches a real Make zone: every
// request is answered by the package's own transport seam.
const (
	apiTokenValue = "canary0make0api0token0canary0make0api0token0"
	apiTokenEnv   = "TEST_MAKE_API_TOKEN"
	baseURL       = "https://eu1.make.com"
	apiHost       = "eu1.make.com"

	ownTeam         int64 = 1001
	foreignTeam     int64 = 2002
	ownOrg          int64 = 501
	foreignOrg      int64 = 502
	ownScenario     int64 = 10
	foreignScenario int64 = 20
	foreignCanary         = "foreign-body-canary-9f3c"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// call is one request the adapter produced.
type call struct {
	method, host, path, token string
	query                     url.Values
}

// serve replaces the package transport for one test and records every request. Every request must reach
// apiHost; anything else fails the test outright, since this provider is never supposed to build one.
func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, call{
			method: request.Method, host: request.URL.Host, path: request.URL.Path,
			token: strings.TrimPrefix(request.Header.Get("Authorization"), "Token "), query: request.URL.Query(),
		})
		if request.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + request.URL.Host)
		}
		return handler(request)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(apiTokenValue, freeLimiter()))
}

// freeLimiter spaces nothing and never sleeps.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Body: httpBody(body),
		Header: http.Header{"Content-Type": {"application/json"}},
	}
}

func httpBody(body string) *httpBodyCloser { return &httpBodyCloser{strings.NewReader(body)} }

type httpBodyCloser struct{ *strings.Reader }

func (httpBodyCloser) Close() error { return nil }

func resolver(red *redact.Redactor, reads *int) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		if reads != nil {
			*reads++
		}
		if name == apiTokenEnv {
			return apiTokenValue
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

var allPermissions = config.Permissions()

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: apiTokenEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"make": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"make-reader": credential},
		Connections: map[string]config.Connection{
			"open": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Target: "team/" + itoa64(ownTeam)},
			"hookurl": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Tools: []string{Provider + ".hooks.url"}, Target: "team/" + itoa64(ownTeam)},
			"hookdelete": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Tools: []string{Provider + ".hooks.delete"}, Target: "team/" + itoa64(ownTeam)},
			"hookqueuedelete": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Tools: []string{Provider + ".hookqueue.delete"}, Target: "team/" + itoa64(ownTeam)},
			"org": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Targets: []string{"team/" + itoa64(ownTeam), "organization/" + itoa64(ownOrg)}},
			"scenario": {Service: "make", Credential: "make-reader", Permissions: allPermissions,
				Targets: []string{"team/" + itoa64(ownTeam), "scenario/" + itoa64(ownScenario)}},
		},
	}
}

func itoa64(v int64) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

type environment struct {
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), coreConfig(), resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
	})
	return string(response.Result), err
}

// confirmed invokes a change operation with the confirmation every one of this provider's five change tools
// requires.
func (e *environment) confirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true,
	})
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
	var invalid *application.InvalidRequestError
	return errors.As(err, &invalid)
}

func isConfirmationRequired(err error) bool {
	var confirmation *application.ConfirmationRequiredError
	return errors.As(err, &confirmation)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Make" || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleAPIToken || !metadata.Target.Required ||
		!metadata.Target.Multiple || len(metadata.Target.Kinds) != 3 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(metadata.Tools) != 32 {
		t.Fatalf("tools = %+v, want 32", metadata.Tools)
	}
	profiles := map[string]int{}
	for _, profile := range metadata.Profiles {
		profiles[profile.ID] = len(profile.Tools)
	}
	if len(profiles) != 9 || profiles["hookqueue-read"] != 3 || profiles["hooks-read"] != 4 || profiles["hooks-manage"] != 8 || profiles["team"] != 3 || profiles["organization"] != 1 {
		t.Fatalf("profiles = %+v, want read, manage, team (3), and organization (1)", profiles)
	}
}

// Team is required, exactly one; organization is optional, at most one; scenario is optional and may be
// repeated. A malformed or duplicated value is refused before any connection can use it.
func TestTargetValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"no team", nil, true},
		{"team only", []string{"team/1"}, false},
		{"team and org", []string{"team/1", "organization/2"}, false},
		{"team and scenario", []string{"team/1", "scenario/3"}, false},
		{"team, org, and scenarios", []string{"team/1", "organization/2", "scenario/3", "scenario/4"}, false},
		{"two teams", []string{"team/1", "team/2"}, true},
		{"two organizations", []string{"team/1", "organization/2", "organization/3"}, true},
		{"duplicate scenario", []string{"team/1", "scenario/3", "scenario/3"}, true},
		{"org and scenario without team", []string{"organization/2", "scenario/3"}, true},
		{"malformed kind", []string{"project/1"}, true},
		{"free-form value", []string{"1"}, true},
		{"leading zero", []string{"team/01"}, true},
		{"negative", []string{"team/-1"}, true},
		{"zero", []string{"team/0"}, true},
		{"path traversal attempt", []string{"team/../1"}, true},
		{"percent encoded", []string{"team/1%30"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseScope(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseScope(%v) = %v, wantErr=%t", tt.values, err, tt.wantErr)
			}
		})
	}
}

func resolvedConnection(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "make", Provider: Provider, BaseURL: baseURL, Targets: targets, Credential: "make-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: apiTokenEnv}}}
}

// TestConnection proves the API token is accepted with the smallest safe read, and reports the classified
// failure of a rejected token without ever leaking the request body.
func TestTestConnection(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/scenarios" {
			return jsonResponse(200, `{"scenarios":[]}`), nil
		}
		return jsonResponse(404, `{}`), nil
	})
	red := &redact.Redactor{}
	class, err := TestConnection(context.Background(), resolvedConnection("team/"+itoa64(ownTeam)), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v, want ok", class, err)
	}
	if len(calls) != 1 || calls[0].token != apiTokenValue || calls[0].query.Get("teamId") != itoa64(ownTeam) {
		t.Fatalf("calls = %+v", calls)
	}

	calls = nil
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(401, `{"message":"`+foreignCanary+`"}`), nil
	})
	class, err = TestConnection(context.Background(), resolvedConnection("team/"+itoa64(ownTeam)), resolver(red, nil), red)
	if err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() with a rejected token = %q, %v, want auth", class, err)
	}
}

// A base URL must be https and its host must be one of Make's own documented zones; nothing else is
// trusted, and no free-form origin is accepted.
func TestParseInstanceRejectsAnythingButAKnownZone(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool // want valid
	}{
		{"eu1", "https://eu1.make.com", true},
		{"us2", "https://us2.make.com", true},
		{"celonis eu", "https://eu1.make.celonis.com", true},
		{"http", "http://eu1.make.com", false},
		{"unknown zone", "https://eu3.make.com", false},
		{"unrelated host", "https://make.com.evil.example", false},
		{"port", "https://eu1.make.com:8443", false},
		{"path", "https://eu1.make.com/api", false},
		{"user info", "https://user:pass@eu1.make.com", false},
		{"query", "https://eu1.make.com?token=x", false},
		{"fragment", "https://eu1.make.com#x", false},
		{"empty", "", false},
		{"garbage", "not a url", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseInstance(tt.raw)
			if (err == nil) != tt.want {
				t.Fatalf("parseInstance(%q) err = %v, want valid=%t", tt.raw, err, tt.want)
			}
		})
	}
}

// The API token never reaches an error message or a redaction gap: it is registered with the redactor as
// soon as it is resolved, and a provider failure never echoes the response body it was classified from.
func TestAPITokenIsRedactedAndNeverLeaksInAnError(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	_, err := env.invoke(scenariosList.ID, "open", `{}`)
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("class = %q, want permission", classOf(err))
	}
	if err != nil && (strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), apiTokenValue)) {
		t.Fatalf("error %v leaked the provider body or the API token", err)
	}
	if env.red.Apply("prefix "+apiTokenValue+" suffix") == "prefix "+apiTokenValue+" suffix" {
		t.Fatalf("the API token was not registered with the redactor")
	}
}

// A 403 names the scope this milestone needs, so a caller can fix a token instead of only seeing "refused".
func TestForbiddenNamesTheRequiredScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{}`), nil
	})
	_, err := env.invoke(scenariosList.ID, "open", `{}`)
	if err == nil || !strings.Contains(err.Error(), "scenarios:read") {
		t.Fatalf("error = %v, want it to name the scenarios:read scope", err)
	}
}

// 401, 404, 429, 503, and 504 are classified the same way every other request classifies them, and a
// redirect is refused rather than followed, since no endpoint this provider calls is documented to redirect.
func TestStatusClassification(t *testing.T) {
	cases := []struct {
		status int
		want   provider.Class
	}{
		{401, provider.ClassAuth}, {403, provider.ClassPermission}, {404, provider.ClassNotFound},
		{503, provider.ClassUnreachable}, {504, provider.ClassTimeout}, {500, provider.ClassProviderError},
	}
	for _, tt := range cases {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(tt.status, `{}`), nil
			})
			_, err := env.invoke(scenariosList.ID, "open", `{}`)
			if classOf(err) != tt.want {
				t.Fatalf("class = %q, want %q", classOf(err), tt.want)
			}
		})
	}

	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound,
			Header: http.Header{"Location": {"https://elsewhere.invalid/"}}, Body: httpBody("")}, nil
	})
	if _, err := env.invoke(scenariosList.ID, "open", `{}`); classOf(err) != provider.ClassProviderError {
		t.Fatalf("class = %q, want provider-error for a redirect", classOf(err))
	}
}
