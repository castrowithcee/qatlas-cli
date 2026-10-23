package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
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

// No test reaches a real instance: every request is answered by the package's transport seam.
const (
	tokenValue = "canary0forge0access0token0canary0"
	tokenEnv   = "TEST_FORGE_TOKEN"
	baseURL    = "https://forge.example.test"
	apiHost    = "forge.example.test"
	bodyCanary = "provider-body-canary-5e2a"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, path, query, auth, contentType, body string
}

// serve answers every request with handler and records it. A request to another host, with a Sudo header, a
// sudo, token, or access_token parameter, or another authorization than the token header fails the test.
func serve(t *testing.T, calls *[]call, handler func(call) *http.Response) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		entry := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type")}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			entry.body = string(data)
		}
		*calls = append(*calls, entry)
		if r.URL.Host != apiHost {
			t.Errorf("request to host %q", r.URL.Host)
			return nil, errors.New("unexpected host")
		}
		for name := range r.Header {
			if strings.EqualFold(name, "Sudo") || strings.Contains(strings.ToLower(name), "otp") {
				t.Errorf("request %s carries the header %s", r.URL.Path, name)
			}
		}
		for key := range r.URL.Query() {
			if slices.Contains([]string{"sudo", "token", "access_token"}, strings.ToLower(key)) {
				t.Errorf("request %s carries the parameter %s", r.URL.Path, key)
			}
		}
		if entry.auth != "" && entry.auth != "token "+tokenValue {
			t.Errorf("request %s authorizes with %q", r.URL.Path, entry.auth)
		}
		if !slices.Contains([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, r.Method) ||
			(r.Method == http.MethodGet && entry.body != "") {
			t.Errorf("request %s uses %s", r.URL.Path, r.Method)
		}
		return handler(entry), nil
	})
	noWait := func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, time.Now, noWait)))
	t.Cleanup(limiters.Replace("origin "+baseURL, ratelimit.New(0, time.Now, noWait)))
	forgetVersions(t)
}

// forgetVersions empties the version cache for one test and again after it.
func forgetVersions(t *testing.T) {
	t.Helper()
	clear := func() {
		versions.Lock()
		versions.entries = map[string]cachedVersion{}
		versions.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func jsonResponse(status int, body string, header ...string) *http.Response {
	response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
		Header: http.Header{"Content-Type": {"application/json"}}}
	for i := 0; i+1 < len(header); i += 2 {
		response.Header.Add(header[i], header[i+1])
	}
	return response
}

func notFoundResponse() *http.Response {
	return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`","url":"https://`+apiHost+`/api/swagger"}`)
}

// instance answers the version endpoints like one product in one version; forgejoPath is the answer of
// /api/forgejo/v1/version, empty for 404. api answers every other path.
func instance(forgejoPath, apiVersion string, api func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		switch c.path {
		case "/api/forgejo/v1/version":
			if forgejoPath == "" {
				return notFoundResponse()
			}
			return jsonResponse(http.StatusOK, `{"version":"`+forgejoPath+`"}`)
		case "/api/v1/version":
			return jsonResponse(http.StatusOK, `{"version":"`+apiVersion+`"}`)
		}
		if api != nil {
			return api(c)
		}
		return notFoundResponse()
	}
}

// forgejoAt and giteaAt are instances of each product in one version.
func forgejoAt(v string, api func(call) *http.Response) func(call) *http.Response {
	return instance(v, v, api)
}

func giteaAt(v string, api func(call) *http.Response) func(call) *http.Response {
	return instance("", v, api)
}

func credential() config.Credential {
	return config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
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

// connections of the test configuration, by name, with their targets.
var connections = map[string][]string{
	"open":  nil,
	"owner": {"users/Alice"},
	"org":   {"orgs/acme"},
	"repo":  {"repos/acme/widget"},
	"mixed": {"users/alice", "repos/acme/widget"},
	"two":   {"repos/acme/widget", "repos/acme/gadget"},
}

var allEffects = []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}

func testConfig(p *product) *config.Config {
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"forge": {Provider: p.id, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"token": credential()},
		Connections: map[string]config.Connection{},
	}
	for name, targets := range connections {
		cfg.Connections[name] = config.Connection{Service: "forge", Credential: "token",
			Effects: allEffects, Targets: targets}
	}
	// A connection whose tools list names a listed-only tool is the only one that offers it.
	cfg.Connections["delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".issues.delete", p.id + ".issues.get",
			p.id + ".comments.delete", p.id + ".comments.get", p.id + ".milestones.delete", p.id + ".milestones.get",
			p.id + ".pull-requests.merge", p.id + ".pull-request-branches.update", p.id + ".pull-requests.get"}}
	cfg.Connections["release-delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".releases.delete", p.id + ".releases.delete-by-tag"}}
	cfg.Connections["times-delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".issue-times.delete", p.id + ".issue-times.reset"}}
	return cfg
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

type environment struct {
	core  *application.Core
	red   *redact.Redactor
	reads *int
	calls *[]call
	p     *product
}

func newEnvironment(t *testing.T, p *product, handler func(call) *http.Response) *environment {
	t.Helper()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), testConfig(p), resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}
}

func (e *environment) invoke(t *testing.T, tool *tool, connection, arguments string) (map[string]any, error) {
	t.Helper()
	return e.invokeAs(t, tool, connection, arguments, true)
}

// invokeAs calls a tool with or without the confirmation a change needs.
func (e *environment) invokeAs(t *testing.T, tool *tool, connection, arguments string, confirmed bool) (map[string]any, error) {
	t.Helper()
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: e.p.toolID(tool), Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: confirmed,
	})
	if err != nil {
		if strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("error leaks a secret or a provider text: %v", err)
		}
		return nil, err
	}
	if strings.Contains(string(response.Result), tokenValue) {
		t.Errorf("result leaks the token: %s", response.Result)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("result %s: %v", response.Result, err)
	}
	return result, nil
}

func (e *environment) paths() []string {
	var paths []string
	for _, c := range *e.calls {
		paths = append(paths, c.path)
	}
	return paths
}

func classOfError(err error) provider.Class {
	var failure *provider.Error
	if errors.As(err, &failure) {
		return failure.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	var core *application.InvalidRequestError
	return errors.As(err, &invalid) || errors.As(err, &core)
}

func resolvedFor(p *product, targets ...string) *config.Resolved {
	return &config.Resolved{Name: "test", Provider: p.id, BaseURL: baseURL, Credential: "token",
		Secrets: credential(), Targets: targets, Effects: []config.Effect{config.EffectRead}}
}
