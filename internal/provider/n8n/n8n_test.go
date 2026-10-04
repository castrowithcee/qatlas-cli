package n8n

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

// The canaries stand for the API key, for a project and workflow outside a restricted connection, and for a
// name a provider body should never leak. No test reaches a real n8n instance: every request is answered by
// the package's own transport seam.
const (
	apiKeyValue = "canary0n8n0api0key0canary0n8n0api0key0"
	apiKeyEnv   = "TEST_N8N_API_KEY"
	baseURL     = "https://n8n.example.invalid"
	apiHost     = "n8n.example.invalid"

	ownProject      = "PROJECT_OWN_0001AAAA"
	foreignProject  = "PROJECT_FOREIGN_0002B"
	ownWorkflow     = "WORKFLOW_OWN_0001AAAA"
	foreignWorkflow = "WORKFLOW_FOREIGN_0002B"
	foreignCanary   = "foreign-body-canary-9f3c"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// call is one request the adapter produced.
type call struct {
	method, host, path, apiKey string
	query                      url.Values
}

// serve replaces the package transport for one test and records every request. Every request must reach
// apiHost; anything else fails the test outright, since this provider is never supposed to build one.
func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, call{
			method: request.Method, host: request.URL.Host, path: request.URL.Path,
			apiKey: request.Header.Get("X-N8N-API-KEY"), query: request.URL.Query(),
		})
		if request.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + request.URL.Host)
		}
		return handler(request)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(apiKeyValue, freeLimiter()))
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
		if name == apiKeyEnv {
			return apiKeyValue
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

// allPermissions grants every registered effect to the test connections, so both the read tools of
// Milestone A and the six change tools of this milestone can be invoked through the same connections; no
// test relies on a permission refusal, which the shared conformance suite already covers generically.
var allPermissions = config.Permissions()

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: apiKeyEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"n8n": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"n8n-reader": credential},
		Connections: map[string]config.Connection{
			"open": {Service: "n8n", Credential: "n8n-reader", Permissions: allPermissions},
			"workflow": {Service: "n8n", Credential: "n8n-reader", Target: "workflow/" + ownWorkflow,
				Permissions: allPermissions},
			"project": {Service: "n8n", Credential: "n8n-reader", Target: "project/" + ownProject,
				Permissions: allPermissions},
			"pdelete": {Service: "n8n", Credential: "n8n-reader", Target: "project/" + ownProject,
				Permissions: allPermissions, Tools: allProjectTools},
			"pdelete-open": {Service: "n8n", Credential: "n8n-reader", Permissions: allPermissions,
				Tools: allProjectTools},
			"pdelete-workflow": {Service: "n8n", Credential: "n8n-reader", Target: "workflow/" + ownWorkflow,
				Permissions: allPermissions, Tools: allProjectTools},
			"both": {Service: "n8n", Credential: "n8n-reader", Permissions: allPermissions,
				Targets: []string{"project/" + ownProject, "workflow/" + ownWorkflow}},
		},
	}
}

var allProjectTools = []string{"n8n.projects.list", "n8n.projects.create", "n8n.projects.update",
	"n8n.projects.delete", "n8n.projectmembers.list", "n8n.projectmembers.add",
	"n8n.projectmembers.setrole", "n8n.projectmembers.remove", "n8n.datatables.list", "n8n.datatables.get",
	"n8n.datatables.create", "n8n.datatables.rename", "n8n.datatables.delete",
	"n8n.datacolumns.list", "n8n.datacolumns.add", "n8n.datacolumns.update", "n8n.datacolumns.delete",
	"n8n.datarows.list", "n8n.datarows.insert", "n8n.datarows.update", "n8n.datarows.upsert", "n8n.datarows.delete",
	"n8n.variables.list", "n8n.variables.create", "n8n.variables.update", "n8n.variables.delete",
	"n8n.tags.list", "n8n.tags.get", "n8n.tags.create", "n8n.tags.update", "n8n.tags.delete"}

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

// confirmed invokes a change operation with the confirmation every one of this provider's six change tools
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
	if !ok || metadata.Name != "n8n" || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleAPIKey || metadata.Target.Required ||
		!metadata.Target.Multiple || len(metadata.Target.Kinds) != 2 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(metadata.Tools) != 41 {
		t.Fatalf("tools = %+v, want 41", metadata.Tools)
	}
}

// Both allow-lists are optional and independent, may each be repeated, and a malformed or duplicated value
// is refused before any connection can use it.
func TestTargetValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"one project", []string{"project/p1"}, false},
		{"one workflow", []string{"workflow/w1"}, false},
		{"project and workflow", []string{"project/p1", "workflow/w1"}, false},
		{"two projects", []string{"project/p1", "project/p2"}, false},
		{"duplicate project", []string{"project/p1", "project/p1"}, true},
		{"duplicate workflow", []string{"workflow/w1", "workflow/w1"}, true},
		{"malformed kind", []string{"team/p1"}, true},
		{"free-form value", []string{"p1"}, true},
		{"path traversal attempt", []string{"project/../p1"}, true},
		{"percent encoded", []string{"project/p%31"}, true},
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

// resolvedConnection builds a *config.Resolved for the package-level TestConnection function, the same way
// this repository's other providers test it directly, below the application core.
func resolvedConnection(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "n8n", Provider: Provider, BaseURL: baseURL, Targets: targets, Credential: "n8n-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: apiKeyEnv}}}
}

// TestConnection proves the API key is accepted with the smallest safe read, and reports the classified
// failure of a rejected key without ever leaking the request body.
func TestTestConnection(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/workflows" {
			return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
		}
		return jsonResponse(404, `{}`), nil
	})
	red := &redact.Redactor{}
	class, err := TestConnection(context.Background(), resolvedConnection(), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v, want ok", class, err)
	}
	if len(calls) != 1 || calls[0].apiKey != apiKeyValue {
		t.Fatalf("calls = %+v", calls)
	}

	calls = nil
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(401, `{"message":"`+foreignCanary+`"}`), nil
	})
	class, err = TestConnection(context.Background(), resolvedConnection(), resolver(red, nil), red)
	if err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() with a rejected key = %q, %v, want auth", class, err)
	}
}

// A base URL must be https, without user, query, or fragment; this provider trusts no local-http exception,
// see the package doc.
func TestParseInstanceRejectsAnythingButAPlainHTTPSOrigin(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool // want valid
	}{
		{"https origin", "https://n8n.example.invalid", true},
		{"https with install path", "https://host.example.invalid/n8n", true},
		{"http", "http://n8n.example.invalid", false},
		{"user info", "https://user:pass@n8n.example.invalid", false},
		{"query", "https://n8n.example.invalid?token=x", false},
		{"fragment", "https://n8n.example.invalid#x", false},
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

// The API key never reaches an error message or a redaction gap: it is registered with the redactor as soon
// as it is resolved, and a provider failure never echoes the response body it was classified from.
func TestAPIKeyIsRedactedAndNeverLeaksInAnError(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	_, err := env.invoke(workflowsList.ID, "open", `{}`)
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("class = %q, want permission", classOf(err))
	}
	if err != nil && (strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), apiKeyValue)) {
		t.Fatalf("error %v leaked the provider body or the API key", err)
	}
	if env.red.Apply("prefix "+apiKeyValue+" suffix") == "prefix "+apiKeyValue+" suffix" {
		t.Fatalf("the API key was not registered with the redactor")
	}
}

// 401 and 404 are classified the same way every other request classifies them, and a redirect is refused
// rather than followed, since no endpoint this provider calls is documented to redirect.
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
			_, err := env.invoke(workflowsList.ID, "open", `{}`)
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
	if _, err := env.invoke(workflowsList.ID, "open", `{}`); classOf(err) != provider.ClassProviderError {
		t.Fatalf("class = %q, want provider-error for a redirect", classOf(err))
	}
}
