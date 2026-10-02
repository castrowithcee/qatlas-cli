package penpot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// No test reaches a real Penpot instance: every request is answered by the package's transport seam.
const (
	tokenValue  = "canary0penpot0access0token0canary0"
	tokenEnv    = "TEST_PENPOT_ACCESS_TOKEN"
	baseURL     = "https://penpot.example.test"
	apiHost     = "penpot.example.test"
	bodyCanary  = "provider-body-canary-7c1d"
	teamA       = "00000000-0000-0000-0000-00000000000a"
	teamB       = "00000000-0000-0000-0000-00000000000b"
	teamForeign = "00000000-0000-0000-0000-00000000000f"
	projectA1   = "00000000-0000-0000-0000-000000000001"
	projectA2   = "00000000-0000-0000-0000-000000000002"
	projectB1   = "00000000-0000-0000-0000-000000000003"
	projectOut  = "00000000-0000-0000-0000-00000000000e"
	fileA1      = "00000000-0000-0000-0000-000000000011"
	fileOut     = "00000000-0000-0000-0000-000000000012"
	pageID      = "00000000-0000-0000-0000-000000000021"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, host, path, auth, contentType string
	body                                  map[string]any
	raw                                   []byte
}

func (c call) command() string { return strings.TrimPrefix(c.path, commandPrefix) }

func serve(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		entry := call{method: r.Method, host: r.URL.Host, path: r.URL.Path, auth: r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type")}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			entry.raw = data
			_ = json.Unmarshal(data, &entry.body)
		}
		*calls = append(*calls, entry)
		if r.URL.Host != apiHost {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		return handler(entry)
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
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccessToken: tokenEnv}}
	read := []config.Permission{config.PermissionRead}
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	connection := func(targets ...string) config.Connection {
		return config.Connection{Service: "penpot", Credential: "token", Permissions: read, Targets: targets}
	}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"penpot": {Provider: Provider, BaseURL: baseURL}},
		Credentials: map[string]config.Credential{"token": credential},
		Connections: map[string]config.Connection{
			"one":    connection("team/" + teamA),
			"two":    connection("team/"+teamA, "team/"+teamB),
			"narrow": connection("team/"+teamA, "project/"+projectA1),
			"write": {Service: "penpot", Credential: "token", Permissions: all, Targets: []string{"team/" + teamA},
				Tools: append([]string{commentsCreate.ID, commentsUpdate.ID, commentsDelete.ID, commentsThreads.ID, commentsList.ID}, append(manageTools, append(libraryTools, append(recoveryTools, transferTools...)...)...)...)},
			"writetwo": {Service: "penpot", Credential: "token", Permissions: all, Targets: []string{"team/" + teamA, "team/" + teamB},
				Tools: append(manageTools, append(libraryTools, append(recoveryTools, transferTools...)...)...)},
			"writenarrow": {Service: "penpot", Credential: "token", Permissions: all,
				Targets: []string{"team/" + teamA, "project/" + projectA1},
				Tools:   append([]string{commentsCreate.ID, commentsUpdate.ID, commentsDelete.ID, commentsThreads.ID, commentsList.ID}, append(manageTools, append(libraryTools, append(recoveryTools, transferTools...)...)...)...)},
			"nodelete": {Service: "penpot", Credential: "token", Permissions: all, Targets: []string{"team/" + teamA},
				Tools: []string{commentsCreate.ID, commentsUpdate.ID, projectsCreate.ID, projectsRename.ID, filesCreate.ID,
					filesRename.ID, filesMove.ID}},
		},
	}
}

type environment struct {
	core  *application.Core
	red   *redact.Redactor
	reads *int
	// read and write are the directories every connection releases for local files.
	read, write string
}

// withFiles gives every connection of the configuration the released directories.
func withFiles(cfg *config.Config, read, write string) *config.Config {
	for name, connection := range cfg.Connections {
		connection.Files = config.Files{Read: []string{read}, Write: []string{write}}
		cfg.Connections[name] = connection
	}
	return cfg
}

func newEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	read, write := t.TempDir(), t.TempDir()
	return &environment{core: application.New(registry(t), withFiles(testConfig(), read, write), resolver(red, &reads), red),
		red: red, reads: &reads, read: read, write: write}
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

func commands(calls []call) []string {
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.command()
	}
	return names
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleAccessToken ||
		metadata.DefaultBaseURL != cloudOrigin || !metadata.Target.Required || !metadata.Target.Multiple ||
		len(metadata.Target.Kinds) != 2 {
		t.Fatalf("metadata = %+v", metadata)
	}
	want := map[string]config.Permission{teamsList.ID: config.PermissionRead, projectsList.ID: config.PermissionRead,
		filesList.ID: config.PermissionRead, filesGet.ID: config.PermissionRead,
		commentsThreads.ID: config.PermissionRead, commentsList.ID: config.PermissionRead,
		commentsCreate.ID: config.PermissionCreate, commentsUpdate.ID: config.PermissionUpdate,
		commentsDelete.ID: config.PermissionDelete,
		projectsCreate.ID: config.PermissionCreate, projectsRename.ID: config.PermissionUpdate,
		projectsDelete.ID: config.PermissionDelete, filesCreate.ID: config.PermissionCreate,
		filesRename.ID: config.PermissionUpdate, filesMove.ID: config.PermissionUpdate,
		librariesList.ID: config.PermissionRead, librariesShare.ID: config.PermissionUpdate,
		librariesLink.ID:   config.PermissionUpdate,
		snapshotsCreate.ID: config.PermissionCreate, snapshotsRestore.ID: config.PermissionUpdate,
		filesDelete.ID: config.PermissionDelete, filesRestore.ID: config.PermissionUpdate,
		filesPurge.ID:  config.PermissionDelete,
		mediaUpload.ID: config.PermissionCreate, mediaFromURL.ID: config.PermissionCreate,
		filesExport.ID: config.PermissionRead, filesImport.ID: config.PermissionCreate}
	explicit := map[string]bool{commentsDelete.ID: true, projectsDelete.ID: true, snapshotsRestore.ID: true,
		filesDelete.ID: true, filesPurge.ID: true}
	if len(metadata.Tools) != len(want) {
		t.Fatalf("tools = %+v", metadata.Tools)
	}
	for _, tool := range metadata.Tools {
		if effect, ok := want[tool.ID]; !ok || tool.Effect != effect || tool.RequiresToolAllowList != explicit[tool.ID] {
			t.Fatalf("tool %+v", tool)
		}
	}
	profile, ok := metadata.RecommendedProfile()
	if !ok || len(profile.Tools) != 4 {
		t.Fatalf("recommended profile = %+v", profile)
	}
}

func TestTargetValidation(t *testing.T) {
	for raw, valid := range map[string]bool{
		"team/" + teamA: true, "project/" + projectA1: true, "team/" + strings.ToUpper(teamA): true,
		"": false, "team/": false, "team/1": false, "team/" + teamA + "x": false, "tem/" + teamA: false,
		"*": false, "file/" + fileA1: false, "team/" + strings.Repeat("g", 36): false,
	} {
		if err := validateTarget(raw); (err == nil) != valid {
			t.Errorf("validateTarget(%q) = %v, want valid=%t", raw, err, valid)
		}
	}
	for name, test := range map[string]struct {
		values []string
		valid  bool
	}{
		"team":          {[]string{"team/" + teamA}, true},
		"teams+project": {[]string{"team/" + teamA, "team/" + teamB, "project/" + projectA1}, true},
		"none":          {nil, false},
		"project only":  {[]string{"project/" + projectA1}, false},
		"dup team":      {[]string{"team/" + teamA, "team/" + strings.ToUpper(teamA)}, false},
		"dup project":   {[]string{"team/" + teamA, "project/" + projectA1, "project/" + projectA1}, false},
	} {
		if err := validateSet(test.values); (err == nil) != test.valid {
			t.Errorf("%s: validateSet = %v, want valid=%t", name, err, test.valid)
		}
	}
}

func TestOriginValidation(t *testing.T) {
	for raw, want := range map[string]string{
		"https://design.penpot.app": "https://design.penpot.app", "https://self.example.test:8443/": "https://self.example.test:8443",
		"https://self.example.test/penpot/": "https://self.example.test/penpot",
		"http://design.penpot.app":          "", "https://u:p@design.penpot.app": "", "https://design.penpot.app?x=1": "",
		"https://design.penpot.app#f": "", "": "", "https://design.penpot.app?": "",
	} {
		got, err := originOf(raw)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("originOf(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
}

func TestConnectionTest(t *testing.T) {
	var calls []call
	serve(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	red := &redact.Redactor{}
	resolved := &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Target: "team/" + teamA, Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccessToken: tokenEnv}}}
	class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v", class, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].command() != cmdTeams ||
		calls[0].auth != "Token "+tokenValue || calls[0].contentType != "application/json" {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	serve(t, &calls, func(call) (*http.Response, error) { return jsonResponse(401, bodyCanary), nil })
	if class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red); err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() rejected = %q, %v", class, err)
	}
}

func TestStatusClassificationHintsAtFlagAndNeverLeaks(t *testing.T) {
	cases := map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission, 404: provider.ClassNotFound,
		429: provider.ClassRateLimited, 503: provider.ClassUnreachable, 504: provider.ClassTimeout,
		500: provider.ClassProviderError, 400: provider.ClassProviderError, 302: provider.ClassProviderError}
	for status, want := range cases {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) {
			response := jsonResponse(status, `{"error":"`+bodyCanary+tokenValue+`"}`)
			response.Header.Set("Location", "https://elsewhere.invalid/")
			return response, nil
		})
		_, err := env.invoke(teamsList.ID, "one", `{}`)
		if classOf(err) != want {
			t.Errorf("status %d: class = %q, want %q", status, classOf(err), want)
		}
		if err != nil && (strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue)) {
			t.Errorf("status %d: error leaked provider body or token: %v", status, err)
		}
		if (status == 401 || status == 403) && !strings.Contains(err.Error(), "access-tokens") {
			t.Errorf("status %d: error lacks the access-tokens hint: %v", status, err)
		}
		if len(calls) != 1 {
			t.Errorf("status %d: calls = %d, want 1 (no redirect followed)", status, len(calls))
		}
	}
}

func TestInvalidAndOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not json": "<html>" + bodyCanary, "oversized": `[` + strings.Repeat(" ", maxResponseSize+10) + `]`,
		"wrong shape": `{"teams":1}`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, body), nil })
		_, err := env.invoke(teamsList.ID, "one", `{}`)
		if classOf(err) != provider.ClassInvalidResponse || (err != nil && strings.Contains(err.Error(), bodyCanary)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTokenIsRedactedAndSentAsTokenScheme(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	if _, err := env.invoke(teamsList.ID, "one", `{}`); err != nil {
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

func TestBadOriginFailsBeforeSecret(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, `[]`), nil })
	env.core = application.New(registry(t), &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"penpot": {Provider: Provider, BaseURL: "http://insecure.example.test"}},
		Credentials: testConfig().Credentials,
		Connections: map[string]config.Connection{"bad": {Service: "penpot", Credential: "token",
			Permissions: []config.Permission{config.PermissionRead}, Target: "team/" + teamA}},
	}, resolver(env.red, env.reads), env.red)
	if _, err := env.invoke(teamsList.ID, "bad", `{}`); err == nil {
		t.Fatal("an http base URL must be refused")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}

func TestOnlyFixedCommandsAreSent(t *testing.T) {
	c := &Client{}
	if err := c.do(context.Background(), "x", "update-file", nil, nil); err == nil {
		t.Fatal("a command outside the fixed set must be refused")
	}
	if _, err := c.change(context.Background(), "x", "update-file", nil); err == nil {
		t.Fatal("a change command outside the fixed set must be refused")
	}
	if err := c.do(context.Background(), "x", cmdDeleteComment, nil, nil); err == nil {
		t.Fatal("a read must not send a change command")
	}
	if _, err := c.change(context.Background(), "x", cmdThreads, nil); err == nil {
		t.Fatal("a change must not send a read command")
	}
}

func TestKeysReadInCamelAndKebabCase(t *testing.T) {
	for _, body := range []string{
		`[{"id":"` + teamA + `","name":"T","isDefault":true}]`,
		`[{"id":"` + teamA + `","name":"T","is-default":true}]`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, body), nil })
		result, err := env.invoke(teamsList.ID, "one", `{}`)
		if err != nil || !strings.Contains(result, `"is_default":true`) {
			t.Fatalf("body %s: result %s, err %v", body, result, err)
		}
	}
}
