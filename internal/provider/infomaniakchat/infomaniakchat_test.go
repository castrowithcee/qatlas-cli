package infomaniakchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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

// The canaries stand for the token, the instance origin, and the teams and channels a connection is bound
// to, or not. No test reaches a real kChat instance: every request is answered by the package's own
// transport seam.
const (
	tokenValue = "canary0kchat0token0123456789abcdef012345"
	tokenEnv   = "TEST_KCHAT_TOKEN"
	origin     = "https://acme-support.kchat.infomaniak.com"

	// teamA and teamB are both teams the token belongs to, standing for a person who works across several
	// customers' kChat teams with one token. Only teamA is bound to the test connections below.
	teamA = "teama0000000000000000001a"
	teamB = "teamb0000000000000000002b"

	// chanA and chanC both belong to teamA; chanB belongs to teamB, a team no test connection is bound to.
	chanA = "chana000000000000000000a1"
	chanC = "chanc000000000000000000c3"
	chanB = "chanb000000000000000000b2"

	rootInChanA = "rootpost0000000000000a01"
	rootInChanB = "rootpost0000000000000b02"

	messageCanary = "message-content-canary-91fd"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// call is one request the adapter produced.
type call struct {
	method, host, path string
	query              url.Values
	body               string
}

// serve replaces the package transport for one test and records every request.
func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		if request.Body != nil {
			buf := make([]byte, 8192)
			n, _ := request.Body.Read(buf)
			body = string(buf[:n])
		}
		*calls = append(*calls, call{
			method: request.Method, host: request.URL.Host, path: request.URL.Path,
			query: request.URL.Query(), body: body,
		})
		return handler(request)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
}

// freeLimiter spaces nothing and never sleeps.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: httpBody(body), Header: http.Header{"Content-Type": {"application/json"}}}
}

func httpBody(body string) *httpBodyCloser { return &httpBodyCloser{strings.NewReader(body)} }

type httpBodyCloser struct{ *strings.Reader }

func (httpBodyCloser) Close() error { return nil }

func teamJSONOf(id, displayName string) string {
	return `{"id":"` + id + `","name":"` + id + `","display_name":"` + displayName + `","type":"O"}`
}

func channelJSONOf(id, teamID, displayName string) string {
	return `{"id":"` + id + `","team_id":"` + teamID + `","type":"O","display_name":"` + displayName +
		`","name":"` + id + `","purpose":"","delete_at":0}`
}

func postJSONOf(id, channelID, rootID, userID, message string, createAt int64) string {
	root, _ := json.Marshal(rootID)
	return `{"id":"` + id + `","create_at":` + strconv.FormatInt(createAt, 10) + `,"edit_at":0,"user_id":"` + userID +
		`","channel_id":"` + channelID + `","root_id":` + string(root) + `,"message":"` + message + `","type":""}`
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
	return reg
}

// sendPermissions grants both connections the create effect messages.send needs; the connection's own
// team and channel targets are what the tests below exercise, not the permission gate every other provider
// already proves generically in the shared conformance suite.
var sendPermissions = []config.Permission{config.PermissionRead, config.PermissionCreate}

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"kc": {Provider: Provider, BaseURL: origin}},
		Credentials: map[string]config.Credential{"kc-reader": credential},
		Connections: map[string]config.Connection{
			// "team" is bound to teamA alone: every channel of it the token can reach is reachable.
			"team": {Service: "kc", Credential: "kc-reader", Targets: []string{"team/" + teamA}, Permissions: sendPermissions},
			// "channel" narrows teamA further to chanA alone.
			"channel": {Service: "kc", Credential: "kc-reader", Targets: []string{"team/" + teamA, "channel/" + chanA},
				Permissions: sendPermissions},
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
	return &environment{core: application.New(registry(t), coreConfig(), resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
	})
	return string(response.Result), err
}

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

func resolvedConnection(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "kc", Provider: Provider, BaseURL: origin, Targets: targets, Credential: "kc-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}}
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Infomaniak kChat" || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleToken || !metadata.Target.Required || !metadata.Target.Multiple ||
		len(metadata.Target.Kinds) != 2 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(metadata.Tools) != 5 {
		t.Fatalf("tools = %+v, want 5", metadata.Tools)
	}
}

func TestTargetValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"one team", []string{"team/" + teamA}, false},
		{"team and channels", []string{"team/" + teamA, "channel/" + chanA, "channel/" + chanC}, false},
		{"no team", []string{"channel/" + chanA}, true},
		{"duplicate team", []string{"team/" + teamA, "team/" + teamA}, true},
		{"duplicate channel", []string{"team/" + teamA, "channel/" + chanA, "channel/" + chanA}, true},
		{"malformed team", []string{"team/ABC"}, true},
		{"free-form value", []string{teamA}, true},
		{"path traversal attempt", []string{"team/../" + teamA}, true},
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

// The instance base URL is bound strictly: only https://TEAM.kchat.infomaniak.com, exactly one DNS label
// below the fixed kChat domain, is accepted; a foreign host, a missing or doubled label, a suffix trick, a
// port, and plain http are all refused before any secret is read.
func TestParseInstanceRejectsAnythingBesidesAnExactKChatTeamHost(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{origin, false},
		{origin + "/", false},
		{"https://ACME-Support.KChat.Infomaniak.Com", false}, // case-insensitive DNS host
		{"https://foreign.example.invalid", true},
		{"https://kchat.infomaniak.com", true},                           // no team label at all
		{"https://a.b.kchat.infomaniak.com", true},                       // two labels below the domain
		{"https://acme-support.kchat.infomaniak.com.evil.example", true}, // suffix trick
		{"https://acme-support.kchat.infomaniak.com:443", true},          // no port, even the default one
		{"http://acme-support.kchat.infomaniak.com", true},               // no plain http
		{origin + "/sub", true},
		{origin + "?x=1", true},
		{origin + "#frag", true},
		{"https://user:pw@acme-support.kchat.infomaniak.com", true},
		{"https://-acme.kchat.infomaniak.com", true}, // a label may not start with a hyphen
		{"https://acme-.kchat.infomaniak.com", true}, // nor end with one
		{"https://Ac_me.kchat.infomaniak.com", true}, // nor carry an unsupported character
	}
	for _, tt := range cases {
		got, err := parseInstance(tt.raw)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseInstance(%q) = %v, wantErr=%t", tt.raw, err, tt.wantErr)
		}
		if err == nil && got != strings.ToLower(got) {
			t.Errorf("parseInstance(%q) = %q, want the host normalised to lowercase", tt.raw, got)
		}
	}
}

// TestConnection proves the token is accepted with the smallest safe read, and classifies a rejected token
// without ever reaching a second endpoint.
func TestTestConnection(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v4/users/me/teams" {
			return jsonResponse(200, "[]"), nil
		}
		return jsonResponse(404, `{}`), nil
	})
	red := &redact.Redactor{}
	class, err := TestConnection(context.Background(), resolvedConnection("team/"+teamA), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v, want ok", class, err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v4/users/me/teams" {
		t.Fatalf("calls = %+v", calls)
	}

	calls = nil
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(401, `{}`), nil })
	class, err = TestConnection(context.Background(), resolvedConnection("team/"+teamA), resolver(red, nil), red)
	if err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() with a rejected token = %q, %v, want auth", class, err)
	}
}

// No redirect is ever followed: kChat's own endpoints are never documented to redirect, so a redirect here
// can only be a mistake or an exfiltration route.
func TestNoRedirectIsEverFollowed(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound,
			Header: http.Header{"Location": {"https://foreign.example.invalid/steal"}}, Body: httpBody("")}, nil
	})
	_, err := env.invoke(teamsList.ID, "team", `{}`)
	if err == nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v, want exactly one request and a refused redirect", err, calls)
	}
}
