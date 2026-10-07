package seatableaccount

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

// No test reaches a real SeaTable server: every request is answered by the package's transport seam.
const (
	tokenValue   = "canary0seatable0account0token0canary0"
	tokenEnv     = "TEST_SEATABLE_ACCOUNT_TOKEN"
	baseURL      = "https://seatable.example.test"
	apiHost      = "seatable.example.test"
	ownTarget    = "42/My Base"
	bodyCanary   = "provider-body-canary-7d1e"
	commitOne    = "0123456789abcdef0123456789abcdef01234567"
	commitTwo    = "fedcba9876543210fedcba9876543210fedcba98"
	foreignCommt = "ffffffffffffffffffffffffffffffffffffffff"
	snapshotPath = "/api/v2.1/workspace/42/dtable/My Base/snapshots/"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, host, path, escaped, auth string
	query                             url.Values
}

func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls = append(*calls, call{method: r.Method, host: r.URL.Host, path: r.URL.Path,
			escaped: r.URL.EscapedPath(), auth: r.Header.Get("Authorization"), query: r.URL.Query()})
		if r.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		return handler(r)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: &bodyCloser{strings.NewReader(body)},
		Header: http.Header{"Content-Type": {"application/json"}}}
}

type bodyCloser struct{ *strings.Reader }

func (bodyCloser) Close() error { return nil }

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

func testConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccountToken: tokenEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"account": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"token": credential},
		Connections: map[string]config.Connection{
			"reader": {Service: "account", Credential: "token", Permissions: []config.Permission{config.PermissionRead},
				Target: ownTarget},
			"restorer": {Service: "account", Credential: "token", Target: ownTarget,
				Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
				Tools:       []string{snapshotsList.ID, snapshotsRestore.ID}},
			"broad": {Service: "account", Credential: "token", Target: ownTarget, Permissions: config.Permissions()},
		},
	}
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
	return &environment{core: application.New(registry(t), testConfig(), resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string, confirmed bool) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: confirmed,
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
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleAccountToken ||
		!metadata.Target.Required || metadata.Target.Multiple || len(metadata.Target.Kinds) != 1 {
		t.Fatalf("metadata = %+v", metadata)
	}
	want := map[string]bool{snapshotsList.ID: false, snapshotsRestore.ID: true}
	if len(metadata.Tools) != len(want) {
		t.Fatalf("tools = %+v", metadata.Tools)
	}
	for _, tool := range metadata.Tools {
		if allow, ok := want[tool.ID]; !ok || allow != tool.RequiresToolAllowList {
			t.Fatalf("tool %+v, want allow-list requirement %v", tool, want[tool.ID])
		}
	}
	profile, ok := metadata.RecommendedProfile()
	if !ok || len(profile.Tools) != 1 || profile.Tools[0] != snapshotsList.ID {
		t.Fatalf("recommended profile = %+v", profile)
	}
	risk := snapshotsRestore.Risk
	if risk.Effect != capability.EffectCreate || risk.Confirmation != capability.ConfirmationRequired ||
		risk.Idempotency != capability.IdempotencyNonIdempotent || !risk.OpenWorld || risk.DataSensitivity == "" {
		t.Fatalf("restore risk = %+v", risk)
	}
}

func TestTargetValidation(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"42/My Base", false}, {"1/base", false}, {"9/Bäse", false},
		{"", true}, {"42", true}, {"42/", true}, {"0/base", true}, {"042/base", true}, {"-1/base", true},
		{"x/base", true}, {"42/a/b", true}, {"42/..", true}, {"42/.", true}, {"42/a\\b", true},
		{"42/ base", true}, {"42/ba\nse", true}, {"42/ba\x00se", true}, {"42/" + strings.Repeat("a", 300), true},
		{"4 2/base", true}, {"42/%2e%2e", false},
	}
	for _, tt := range cases {
		if err := (func() error { _, err := parseTarget(tt.raw); return err })(); (err != nil) != tt.wantErr {
			t.Errorf("parseTarget(%q) = %v, wantErr=%t", tt.raw, err, tt.wantErr)
		}
	}
	if validateSet(nil) == nil || validateSet([]string{"1/a", "1/b"}) == nil || validateSet([]string{"1/a"}) != nil {
		t.Fatalf("validateSet must demand exactly one valid target")
	}
}

func TestOriginValidation(t *testing.T) {
	for raw, valid := range map[string]bool{
		"https://cloud.seatable.io": true, "https://self.example.test:8443/": true,
		"http://cloud.seatable.io": false, "https://u:p@cloud.seatable.io": false, "https://cloud.seatable.io/x": false,
		"https://cloud.seatable.io?x=1": false, "https://cloud.seatable.io#f": false, "": false,
	} {
		if _, err := originOf(raw); (err == nil) != valid {
			t.Errorf("originOf(%q) = %v, want valid=%t", raw, err, valid)
		}
	}
}

func TestConnectionTest(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"snapshot_list":[],"page_info":{"has_next_page":false,"current_page":1}}`), nil
	})
	red := &redact.Redactor{}
	resolved := &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Target: ownTarget, Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccountToken: tokenEnv}}}
	class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v", class, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != snapshotPath ||
		calls[0].auth != "Bearer "+tokenValue || calls[0].query.Get("per_page") != "1" {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(401, bodyCanary), nil })
	if class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red); err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() rejected = %q, %v", class, err)
	}
}

func TestStatusClassificationNeverLeaksBodyOrToken(t *testing.T) {
	cases := map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission, 404: provider.ClassNotFound,
		429: provider.ClassRateLimited, 503: provider.ClassUnreachable, 504: provider.ClassTimeout,
		500: provider.ClassProviderError, 302: provider.ClassProviderError}
	for status, want := range cases {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			response := jsonResponse(status, `{"error_msg":"`+bodyCanary+tokenValue+`"}`)
			response.Header.Set("Location", "https://elsewhere.invalid/")
			return response, nil
		})
		_, err := env.invoke(snapshotsList.ID, "reader", `{}`, false)
		if classOf(err) != want {
			t.Errorf("status %d: class = %q, want %q", status, classOf(err), want)
		}
		if err != nil && (strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue)) {
			t.Errorf("status %d: error leaked provider body or token: %v", status, err)
		}
		if len(calls) != 1 {
			t.Errorf("status %d: calls = %d, want 1 (no redirect followed)", status, len(calls))
		}
	}
}

func TestTokenIsRedacted(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"snapshot_list":[],"page_info":{}}`), nil
	})
	if _, err := env.invoke(snapshotsList.ID, "reader", `{}`, false); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{tokenValue, "Bearer " + tokenValue} {
		if env.red.Apply("x "+text+" y") == "x "+text+" y" {
			t.Fatalf("%q was not registered with the redactor", text)
		}
	}
}
