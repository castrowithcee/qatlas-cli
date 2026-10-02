package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Canaries with every character that JSON or HTML escaping would change.
const (
	canaryUser = "us<er>&\"x\nend"
	canaryPass = "pa<ss>&\"word\nend"
)

// refStore is a credential store over a map that counts its reads.
type refStore struct {
	entries map[string]string
	gets    atomic.Int32
}

func (s *refStore) Get(_ context.Context, key string) (string, error) {
	s.gets.Add(1)
	if value, ok := s.entries[key]; ok {
		return value, nil
	}
	return "", secret.ErrNoEntry
}
func (s *refStore) Set(string, string) error { return nil }
func (s *refStore) Delete(string) error      { return nil }

// refFixture is a core over a test-only provider whose tool takes a secret reference and mirrors the
// request body it sends to a fake server back to the caller. The provider is registered nowhere else.
type refFixture struct {
	core     *Core
	cfg      *config.Config
	store    *refStore
	server   *httptest.Server
	audit    *bytes.Buffer
	logDir   string
	redactor *redact.Redactor
	handled  atomic.Int32
	bodies   [][]byte
}

func newRefFixture(t *testing.T) *refFixture {
	t.Helper()
	f := &refFixture{audit: &bytes.Buffer{}, logDir: t.TempDir(), redactor: &redact.Redactor{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.bodies = append(f.bodies, body)
		var decoded any
		_ = json.Unmarshal(body, &decoded)
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": decoded, "raw": string(body)})
	}))
	t.Cleanup(f.server.Close)

	f.store = &refStore{entries: map[string]string{
		secret.StoreKey("shared", "user"): canaryUser, secret.StoreKey("shared", "pass"): canaryPass,
		secret.StoreKey("unlisted", "user"): "unlisted-user-value", secret.StoreKey("unlisted", "pass"): "unlisted-pass-value",
	}}
	handler := func(ctx context.Context, resolved *config.Resolved, resolver *secret.Resolver,
		_ *redact.Redactor, arguments json.RawMessage) (any, error) {
		f.handled.Add(1)
		var in struct {
			Name   string `json:"name"`
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(arguments, &in); err != nil {
			return nil, err
		}
		values, err := capability.ResolveSecretRef(ctx, resolved, resolver, in.Secret)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"name": in.Name}
		if err := capability.MergeSecretFields(body, values); err != nil {
			return nil, err
		}
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		response, err := http.Post(resolved.BaseURL, "application/json", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode >= 400 {
			text, _ := io.ReadAll(response.Body)
			return nil, fmt.Errorf("the provider answered %d: %s", response.StatusCode, text)
		}
		var out map[string]any
		if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
			return nil, err
		}
		return out, nil
	}
	descriptor := capability.Descriptor{
		ID: "fake.accounts.create", Version: 1, Title: "Create account", Description: "Creates an account",
		Tags: []string{"account"}, Provider: "fake", RequiresToolAllowList: true,
		Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, DataSensitivity: "test"},
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"secret":{"type":"string","minLength":1}},"required":["name","secret"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Arguments: []capability.Argument{
			{Name: "name", Description: "Account name", Required: true},
			{Name: "secret", Description: "Forward credential to set", Required: true, SecretRef: true},
		},
	}
	registry := capability.NewRegistry()
	if err := registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}

	f.cfg = config.New()
	f.cfg.Services["svc"] = config.Service{Provider: "fake", BaseURL: f.server.URL}
	f.cfg.Credentials["own"] = config.Credential{Type: config.CredentialTypeKeyring}
	f.cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user", "pass"}, Description: "shared login"}
	f.cfg.Credentials["unlisted"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user", "pass"}}
	f.cfg.Credentials["plain"] = config.Credential{Type: config.CredentialTypeKeyring}
	f.cfg.Connections["primary"] = config.Connection{
		Service: "svc", Credential: "own", Tools: []string{"fake.accounts.create"},
		Permissions:    []config.Permission{config.PermissionRead, config.PermissionCreate},
		ForwardSecrets: []string{"shared"},
	}
	resolver := secret.NewWith(func(string) string { return "" }, f.store, nil, f.redactor)
	f.core = New(registry, f.cfg, resolver, f.redactor)
	f.core.SetAudit(f.audit)
	f.core.SetInvokeLog(invokelog.New(f.logDir, 90), "cli", nil)
	return f
}

func (f *refFixture) invoke(secretRef, name string, confirmed bool) (InvokeResponse, error) {
	arguments, _ := json.Marshal(map[string]string{"name": name, "secret": secretRef})
	return f.core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.accounts.create", Connection: "primary", Arguments: arguments, Confirmed: confirmed,
	})
}

// everywhere collects every surface a value could leak to: the audit stream, the invocation log, the saved
// configuration, and the rendered form of a result or an error.
func (f *refFixture) everywhere(t *testing.T, response InvokeResponse, err error) string {
	t.Helper()
	var all strings.Builder
	all.Write(response.Result)
	if err != nil {
		all.WriteString(f.redactor.Error(err))
	}
	all.Write(f.audit.Bytes())
	files, _ := filepath.Glob(filepath.Join(f.logDir, "logs", "*"))
	for _, file := range files {
		data, _ := os.ReadFile(file)
		all.Write(data)
	}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	cfg := f.cfg.Clone()
	if saveErr := store.Save(cfg); saveErr == nil {
		data, _ := os.ReadFile(store.Path())
		all.Write(data)
	}
	return all.String()
}

func assertNoCanary(t *testing.T, where, text string) {
	t.Helper()
	for _, canary := range []string{canaryUser, canaryPass} {
		escaped, _ := json.Marshal(canary)
		forms := []string{canary, string(escaped[1 : len(escaped)-1])}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(canary)
		forms = append(forms, strings.TrimSpace(buf.String()[1:len(buf.String())-2]))
		for _, form := range forms {
			if strings.Contains(text, form) {
				t.Errorf("%s contains a secret value (form %q)", where, form[:4])
			}
		}
	}
}

func TestSecretRefReachesTheProviderAndNeverTheOutput(t *testing.T) {
	f := newRefFixture(t)
	response, err := f.invoke("shared", "acme", true)
	if err != nil {
		t.Fatalf("Invoke() = %v", err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("the fake server got %d bodies, want 1", len(f.bodies))
	}
	var sent map[string]string
	if err := json.Unmarshal(f.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent["name"] != "acme" || sent["user"] != canaryUser || sent["pass"] != canaryPass {
		t.Fatalf("the provider body does not carry both fields and the plain argument")
	}
	if !strings.Contains(string(response.Result), redact.Marker) {
		t.Errorf("the mirrored body was not redacted: %s", response.Result)
	}
	assertNoCanary(t, "the surfaces", f.everywhere(t, response, nil))
	if !strings.Contains(f.audit.String(), `"secret_refs":["shared"]`) {
		t.Errorf("the audit event does not name the reference: %s", f.audit.String())
	}
}

func TestSecretRefWithoutConfirmationReadsNoStore(t *testing.T) {
	f := newRefFixture(t)
	_, err := f.invoke("shared", "acme", false)
	var confirmation *ConfirmationRequiredError
	if !errors.As(err, &confirmation) {
		t.Fatalf("Invoke() = %v, want confirmation-required", err)
	}
	if f.store.gets.Load() != 0 || f.handled.Load() != 0 || len(f.bodies) != 0 {
		t.Errorf("store reads = %d, handler calls = %d, want none", f.store.gets.Load(), f.handled.Load())
	}
}

func TestSecretRefRefusals(t *testing.T) {
	for _, tt := range []struct{ name, ref string }{
		{"a forward credential the connection does not list", "unlisted"},
		{"a credential that is not a forward credential", "plain"},
		{"the connection's own credential", "own"},
		{"a credential that does not exist", "missing"},
		{"a value instead of a name", "p4ssw0rd-pasted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newRefFixture(t)
			// Whether confirmed or not, the refusal comes first and no store is read.
			for _, confirmed := range []bool{false, true} {
				response, err := f.invoke(tt.ref, "acme", confirmed)
				var refused *SecretRefNotAllowedError
				if !errors.As(err, &refused) || ErrorCode(err) != output.CodeSecretRefNotAllowed {
					t.Fatalf("Invoke(confirmed=%t) = %v, want secret-ref-not-allowed", confirmed, err)
				}
				_, known := f.cfg.Credentials[tt.ref]
				if known != strings.Contains(err.Error(), `"`+tt.ref+`"`) {
					t.Errorf("message %q: names the reference = %t, want %t", err, !known, known)
				}
				assertNoCanary(t, "the surfaces", f.everywhere(t, response, err))
			}
			if f.store.gets.Load() != 0 || f.handled.Load() != 0 {
				t.Errorf("store reads = %d, handler calls = %d, want none", f.store.gets.Load(), f.handled.Load())
			}
		})
	}
}

func TestSecretRefHandlerWithoutConfirmationReadsNoStore(t *testing.T) {
	f := newRefFixture(t)
	resolved, err := f.core.connection("primary")
	if err != nil {
		t.Fatal(err)
	}
	resolver := secret.NewWith(nil, f.store, nil, f.redactor)
	if _, err := capability.ResolveSecretRef(context.Background(), resolved, resolver, "shared"); err == nil {
		t.Fatal("ResolveSecretRef() on an unconfirmed context succeeded")
	}
	if f.store.gets.Load() != 0 {
		t.Errorf("store reads = %d, want none", f.store.gets.Load())
	}
}

// A plain argument that equals a secret value is redacted wherever it comes back.
func TestSecretRefPlainArgumentCollision(t *testing.T) {
	f := newRefFixture(t)
	response, err := f.invoke("shared", canaryUser, true)
	if err != nil {
		t.Fatalf("Invoke() = %v", err)
	}
	assertNoCanary(t, "the result", string(response.Result))
}

// A failing provider that echoes the body in its error does not leak it through the redactor.
func TestSecretRefErrorIsRedacted(t *testing.T) {
	f := newRefFixture(t)
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(body)
	})
	response, err := f.invoke("shared", "acme", true)
	if err == nil || !strings.Contains(f.redactor.Error(err), redact.Marker) {
		t.Fatalf("Invoke() = %v, want the provider error with the body redacted", err)
	}
	assertNoCanary(t, "the surfaces", f.everywhere(t, response, err))
}

func TestSecretRefDiscoveryNamesOnlyAllowedCredentials(t *testing.T) {
	f := newRefFixture(t)
	if _, err := f.invoke("shared", "acme", true); err != nil {
		t.Fatal(err)
	}
	summary := f.core.Connections("", func(*config.Resolved) error { return nil })
	data, _ := json.Marshal(summary)
	var decoded struct {
		Connections []struct {
			ForwardSecrets []ForwardSecretRef `json:"forward_secrets"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded.Connections) != 1 {
		t.Fatalf("Connections() = %s, %v", data, err)
	}
	refs := decoded.Connections[0].ForwardSecrets
	if len(refs) != 1 || refs[0].Name != "shared" || strings.Join(refs[0].Fields, ",") != "user,pass" ||
		refs[0].Description != "shared login" || refs[0].Unusable != nil {
		t.Errorf("forward secrets = %+v, want only shared with its fields and description", refs)
	}
	assertNoCanary(t, "the connections", string(data))
	for _, name := range []string{"unlisted", "plain", "own"} {
		if strings.Contains(string(data), `"`+name+`"`) {
			t.Errorf("discovery names %s", name)
		}
	}

	described, err := f.core.Describe(DescribeRequest{Operation: "fake.accounts.create", Connection: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if got := described.Connections[0].ForwardSecrets; len(got) != 1 || got[0].Name != "shared" {
		t.Errorf("describe forward secrets = %+v", got)
	}
	encoded, _ := json.Marshal(described)
	assertNoCanary(t, "describe", string(encoded))
}

func TestMergeSecretFields(t *testing.T) {
	body := map[string]any{"name": "acme"}
	if err := capability.MergeSecretFields(body, map[string]string{"user": "u", "pass": "p"}); err != nil {
		t.Fatal(err)
	}
	if body["user"] != "u" || body["pass"] != "p" || body["name"] != "acme" {
		t.Errorf("body = %v", body)
	}
	err := capability.MergeSecretFields(map[string]any{"user": "plain"}, map[string]string{"user": "canary-value"})
	if err == nil || strings.Contains(err.Error(), "canary-value") || !strings.Contains(err.Error(), "user") {
		t.Errorf("collision error = %v, want the field and no value", err)
	}
}
