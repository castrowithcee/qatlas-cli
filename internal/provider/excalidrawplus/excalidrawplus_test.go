package excalidrawplus

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

// No test reaches a real Excalidraw+ workspace: every request is answered by the package's transport seam.
const (
	apiKeyValue   = "canary0excalidraw0api0key0canary0excalidraw"
	apiKeyEnv     = "TEST_EXCALIDRAWPLUS_API_KEY"
	baseURL       = "https://api.excalidraw.com"
	ownCollection = "col-own"
	otherAllowed  = "col-two"
	foreignColl   = "col-foreign"
	foreignCanary = "foreign-body-canary-9f3c"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type call struct {
	method, host, path, key string
	query                   url.Values
}

func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, call{method: request.Method, host: request.URL.Host, path: request.URL.Path,
			key: strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "), query: request.URL.Query()})
		if request.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + request.URL.Host)
		}
		return handler(request)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(apiKeyValue, ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
}

type bodyCloser struct{ *strings.Reader }

func (bodyCloser) Close() error { return nil }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: bodyCloser{strings.NewReader(body)},
		Header: http.Header{"Content-Type": {"application/json"}}}
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: apiKeyEnv}}
	perms := config.Permissions()
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"excalidraw": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"excalidraw-reader": credential},
		Connections: map[string]config.Connection{
			"one": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "collection/" + ownCollection},
			"two": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Targets: []string{"collection/" + ownCollection, "collection/" + otherAllowed}},
			"listed": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "collection/" + ownCollection,
				Tools: []string{scenesGet.ID, contentPatch.ID, contentReplace.ID, scenesDelete.ID, collectionsDelete.ID}},
			"listedAll": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "*",
				Tools: []string{collectionsDelete.ID}},
			"peopleOne": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "collection/" + ownCollection,
				Tools: peopleToolIDs},
			"peopleAll": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "*",
				Tools: peopleToolIDs},
			"every": {Service: "excalidraw", Credential: "excalidraw-reader", Permissions: perms, Target: "*"},
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
	res := secret.NewWith(func(name string) string {
		reads++
		if name == apiKeyEnv {
			return apiKeyValue
		}
		return ""
	}, nil, nil, red)
	return &environment{core: application.New(registry(t), coreConfig(), res, red), red: red, reads: &reads}
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
	var invalid *application.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	metadata, ok := registry(t).ProviderMetadata(Provider)
	if !ok || metadata.Name != "Excalidraw+" || metadata.DefaultBaseURL != baseURL || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleAPIKey || !metadata.Target.Required || len(metadata.Tools) != 16 {
		t.Fatalf("metadata = %+v", metadata)
	}
	recommended, ok := metadata.RecommendedProfile()
	if !ok || len(metadata.Profiles) != 4 || len(recommended.Tools) != 4 {
		t.Fatalf("profiles = %+v", metadata.Profiles)
	}
	for _, d := range []capability.Descriptor{collectionsList, scenesList, scenesGet, scenesContent} {
		if d.Version < 1 || d.Risk.Effect != capability.EffectRead || d.Risk.Idempotency != capability.IdempotencySafe {
			t.Errorf("%s has an unexpected contract", d.ID)
		}
	}
}

func TestTargetValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"none", nil, true},
		{"one", []string{"collection/abc"}, false},
		{"two", []string{"collection/abc", "collection/def_-1"}, false},
		{"wildcard", []string{"*"}, false},
		{"wildcard and collection", []string{"*", "collection/abc"}, true},
		{"duplicate", []string{"collection/abc", "collection/abc"}, true},
		{"empty id", []string{"collection/"}, true},
		{"wrong kind", []string{"scene/abc"}, true},
		{"bare id", []string{"abc"}, true},
		{"path traversal", []string{"collection/../x"}, true},
		{"slash", []string{"collection/a/b"}, true},
		{"percent", []string{"collection/a%2f"}, true},
		{"too long", []string{"collection/" + strings.Repeat("a", 65)}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseScope(tt.values); (err != nil) != tt.wantErr {
				t.Fatalf("parseScope(%v) = %v, wantErr=%t", tt.values, err, tt.wantErr)
			}
		})
	}
}

func TestParseInstanceOnlyAcceptsTheDocumentedOrigin(t *testing.T) {
	for raw, valid := range map[string]bool{
		"https://api.excalidraw.com": true, "https://API.excalidraw.com/": true,
		"http://api.excalidraw.com": false, "https://plus.excalidraw.com": false,
		"https://api.excalidraw.com.evil.example": false, "https://api.excalidraw.com:8443": false,
		"https://api.excalidraw.com/api/v1": false, "https://u:p@api.excalidraw.com": false,
		"https://api.excalidraw.com?x=1": false, "": false,
	} {
		if _, err := parseInstance(raw); (err == nil) != valid {
			t.Errorf("parseInstance(%q) err = %v, want valid=%t", raw, err, valid)
		}
	}
}

func resolvedConnection(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Targets: targets, Credential: "c",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: apiKeyEnv}}}
}

func TestTestConnection(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"limit":1,"offset":0,"hasNextPage":false,"data":[]}`), nil
	})
	red := &redact.Redactor{}
	res := secret.NewWith(func(string) string { return apiKeyValue }, nil, nil, red)
	class, err := TestConnection(context.Background(), resolvedConnection("*"), res, red)
	if err != nil || class != provider.ClassOK || len(calls) != 1 || calls[0].path != apiPath+"/collections" ||
		calls[0].key != apiKeyValue || calls[0].query.Get("limit") != "1" {
		t.Fatalf("class=%q err=%v calls=%+v", class, err, calls)
	}
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(401, `{}`), nil })
	if class, err = TestConnection(context.Background(), resolvedConnection("*"), res, red); err != nil || class != provider.ClassAuth {
		t.Fatalf("class=%q err=%v, want auth", class, err)
	}
}

// The key is registered with the redactor and a provider body never reaches an error.
func TestKeyIsRedactedAndBodyNeverLeaks(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+apiKeyValue+`"}`), nil
	})
	_, err := env.invoke(collectionsList.ID, "every", `{}`)
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("class = %q, want permission", classOf(err))
	}
	if strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), apiKeyValue) {
		t.Fatalf("error leaked: %v", err)
	}
	if env.red.Apply("x "+apiKeyValue+" y") == "x "+apiKeyValue+" y" {
		t.Fatal("the API key was not registered with the redactor")
	}
}

func TestStatusClassificationAndRedirectRefusal(t *testing.T) {
	for status, want := range map[int]provider.Class{401: provider.ClassAuth, 404: provider.ClassNotFound,
		429: provider.ClassRateLimited, 503: provider.ClassUnreachable, 504: provider.ClassTimeout, 500: provider.ClassProviderError} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(status, `{}`), nil })
		if _, err := env.invoke(collectionsList.ID, "every", `{}`); classOf(err) != want {
			t.Errorf("status %d class = %q, want %q", status, classOf(err), want)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://elsewhere.invalid/"}},
			Body: bodyCloser{strings.NewReader("")}}, nil
	})
	if _, err := env.invoke(collectionsList.ID, "every", `{}`); classOf(err) != provider.ClassProviderError {
		t.Fatalf("class = %q, want provider-error for a redirect", classOf(err))
	}
	if len(calls) != 1 {
		t.Fatalf("a redirect was followed: %+v", calls)
	}
}
