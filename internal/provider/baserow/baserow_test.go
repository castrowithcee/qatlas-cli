package baserow

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

// No test reaches a real Baserow instance: every request is answered by the package's transport seam.
const (
	tokenValue = "canary0baserow0database0token0canary0"
	tokenEnv   = "TEST_BASEROW_DATABASE_TOKEN"
	baseURL    = "https://baserow.example.test"
	apiHost    = "baserow.example.test"
	bodyCanary = "provider-body-canary-5b2e"
	ownTable   = 11
	otherOwn   = 12
	foreign    = 99
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, host, path, auth string
	query                    url.Values
}

func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls = append(*calls, call{method: r.Method, host: r.URL.Host, path: r.URL.Path,
			auth: r.Header.Get("Authorization"), query: r.URL.Query()})
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
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleDatabaseToken: tokenEnv}}
	read := []config.Permission{config.PermissionRead}
	allRights := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	writeTools := []string{rowsCreate.ID, rowsUpdate.ID, rowsDelete.ID, rowsMove.ID, rowsBatchCreate.ID, rowsBatchUpdate.ID, rowsBatchDelete.ID, fieldsList.ID, rowsGet.ID}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"baserow": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"token": credential},
		Connections: map[string]config.Connection{
			"one":  {Service: "baserow", Credential: "token", Permissions: read, Target: "table/11"},
			"list": {Service: "baserow", Credential: "token", Permissions: read, Targets: []string{"table/11", "table/12"}},
			"all":  {Service: "baserow", Credential: "token", Permissions: read, Target: "*"},
			"write": {Service: "baserow", Credential: "token", Permissions: allRights, Targets: []string{"table/11", "table/12"},
				Tools: writeTools},
			"writeall": {Service: "baserow", Credential: "token", Permissions: allRights, Target: "*", Tools: writeTools},
			"writenodelete": {Service: "baserow", Credential: "token", Permissions: allRights, Target: "table/11",
				Tools: []string{rowsCreate.ID, rowsUpdate.ID, rowsMove.ID}},
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

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
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

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleDatabaseToken ||
		metadata.DefaultBaseURL != cloudOrigin || !metadata.Target.Required || !metadata.Target.Multiple ||
		metadata.Target.Wildcard != "*" || metadata.Target.WildcardWarning == "" {
		t.Fatalf("metadata = %+v", metadata)
	}
	want := map[string]config.Permission{tablesList.ID: config.PermissionRead, fieldsList.ID: config.PermissionRead,
		rowsList.ID: config.PermissionRead, rowsGet.ID: config.PermissionRead, rowsSearch.ID: config.PermissionRead,
		rowsCreate.ID: config.PermissionCreate, rowsUpdate.ID: config.PermissionUpdate,
		rowsDelete.ID: config.PermissionDelete, rowsMove.ID: config.PermissionUpdate,
		rowsBatchCreate.ID: config.PermissionCreate, rowsBatchUpdate.ID: config.PermissionUpdate,
		rowsBatchDelete.ID: config.PermissionDelete}
	if len(metadata.Tools) != len(want) {
		t.Fatalf("tools = %+v", metadata.Tools)
	}
	for _, tool := range metadata.Tools {
		effect, ok := want[tool.ID]
		if !ok || tool.Effect != effect || tool.RequiresToolAllowList != (tool.ID == rowsDelete.ID || tool.ID == rowsBatchDelete.ID) {
			t.Fatalf("tool %+v", tool)
		}
	}
	for _, id := range []string{Provider + ".rows.history", Provider + ".rows.comments"} {
		if _, ok := want[id]; ok {
			t.Fatalf("%s must not be offered: its endpoint accepts no database token", id)
		}
		for _, tool := range metadata.Tools {
			if tool.ID == id {
				t.Fatalf("%s is registered", id)
			}
		}
	}
	profile, ok := metadata.RecommendedProfile()
	if !ok || len(profile.Tools) != 5 {
		t.Fatalf("recommended profile = %+v", profile)
	}
}

func TestTargetValidation(t *testing.T) {
	for raw, valid := range map[string]bool{
		"table/1": true, "table/123456": true, "*": true,
		"": false, "table/": false, "table/0": false, "table/01": false, "table/-1": false, "table/x": false,
		"table/1/2": false, "table/1 2": false, "tables/1": false, "table/" + strings.Repeat("9", 30): false,
		"database/1": false,
	} {
		if err := validateTarget(raw); (err == nil) != valid {
			t.Errorf("validateTarget(%q) = %v, want valid=%t", raw, err, valid)
		}
	}
	for values, valid := range map[string]bool{
		"table/1,table/2": true, "*": true, "": false, "table/1,table/1": false, "*,table/1": false, "*,*": false,
	} {
		var list []string
		if values != "" {
			list = strings.Split(values, ",")
		}
		if err := validateSet(list); (err == nil) != valid {
			t.Errorf("validateSet(%q) = %v, want valid=%t", values, err, valid)
		}
	}
}

func TestOriginValidation(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.baserow.io": "https://api.baserow.io", "https://self.example.test:8443/": "https://self.example.test:8443",
		"https://self.example.test/baserow/": "https://self.example.test/baserow",
		"http://api.baserow.io":              "", "https://u:p@api.baserow.io": "", "https://api.baserow.io?x=1": "",
		"https://api.baserow.io#f": "", "": "", "https://api.baserow.io?": "",
	} {
		got, err := originOf(raw)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("originOf(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
}

func TestConnectionTest(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	red := &redact.Redactor{}
	resolved := &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Target: "table/11", Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleDatabaseToken: tokenEnv}}}
	class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v", class, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != allTablesPath ||
		calls[0].auth != "Token "+tokenValue {
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
		500: provider.ClassProviderError, 400: provider.ClassProviderError, 302: provider.ClassProviderError}
	for status, want := range cases {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			response := jsonResponse(status, `{"error":"`+bodyCanary+tokenValue+`"}`)
			response.Header.Set("Location", "https://elsewhere.invalid/")
			return response, nil
		})
		_, err := env.invoke(rowsGet.ID, "one", `{"table_id":11,"row_id":1}`)
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

func TestInvalidAndOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not json": "<html>" + bodyCanary, "oversized": `[` + strings.Repeat(" ", maxResponseSize+10) + `]`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
		_, err := env.invoke(tablesList.ID, "one", `{}`)
		if classOf(err) != provider.ClassInvalidResponse || (err != nil && strings.Contains(err.Error(), bodyCanary)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTokenIsRedactedAndSentAsTokenScheme(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	if _, err := env.invoke(tablesList.ID, "one", `{}`); err != nil {
		t.Fatal(err)
	}
	if calls[0].auth != "Token "+tokenValue {
		t.Fatalf("auth = %q", calls[0].auth)
	}
	for _, text := range []string{tokenValue, "Token " + tokenValue} {
		if env.red.Apply("x "+text+" y") == "x "+text+" y" {
			t.Fatalf("%q was not registered with the redactor", text)
		}
	}
}

func TestBadOriginAndTargetFailBeforeSecret(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	env.core = application.New(registry(t), &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"baserow": {Provider: Provider, BaseURL: "http://insecure.example.test"}},
		Credentials: testConfig().Credentials,
		Connections: map[string]config.Connection{"bad": {Service: "baserow", Credential: "token",
			Permissions: []config.Permission{config.PermissionRead}, Target: "table/11"}},
	}, resolver(env.red, env.reads), env.red)
	if _, err := env.invoke(tablesList.ID, "bad", `{}`); err == nil {
		t.Fatal("an http base URL must be refused")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}
