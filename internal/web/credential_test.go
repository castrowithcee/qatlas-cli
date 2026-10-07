package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// canarySecretID and canarySecretValue stand in for a real credential secret in every test of this file.
// Nothing here is a real credential; the names make a leak into a response, a log, or the configuration
// file impossible to miss.
const (
	canarySecretID    = "canary-web-id-9f8e"
	canarySecretValue = "canary-web-secret-7c6d"
)

// wikiCatalog is a synthetic provider catalog, just large enough to exercise a credential of two secret
// roles, without pulling in a real provider package this test has no business depending on.
type wikiCatalog struct{}

var wikiMetadata = config.ProviderMetadata{
	ID: "wiki", Name: "Wiki", Description: "a synthetic test provider",
	SecretRoles: []config.SecretRole{
		{Name: "token-id", Description: "the token id"},
		{Name: "token-secret", Description: "the token secret"},
	},
}

func (wikiCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	if id != wikiMetadata.ID {
		return config.ProviderMetadata{}, false
	}
	return wikiMetadata, true
}

func (wikiCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{wikiMetadata}
}

// newCredentialTestServer returns a server wired for the credential routes, over a temporary configuration
// file and an in-process credential store: never the real keyring, and never the real vault under a
// person's home directory.
func newCredentialTestServer(t *testing.T, v *vault.Vault) (*Server, *config.Store, *secret.MemoryStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write starter config: %v", err)
	}
	store := config.NewStore(path, wikiCatalog{})
	if _, err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}

	mem := secret.NewMemoryStore()
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, mem, nil, red)
	if v != nil {
		resolver.WithVault(v, nil)
	}

	s, err := New(testOverview(), v, defaultTestAdminTimeout, manage.New(store, resolver, connlog.SurfaceWeb, red), resolver, red, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	return s, store, mem, path
}

// coupleAndApprove couples a fresh browser session to s and, where v is not nil, grants its admin approval,
// the same two steps a person takes before this run lets a mutation through.
func coupleAndApprove(t *testing.T, s *Server, v *vault.Vault) (*http.Cookie, string) {
	t.Helper()
	link, err := url.Parse(s.URL())
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	redeem := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	if redeem.Code != http.StatusSeeOther {
		t.Fatalf("redeem status = %d, want %d", redeem.Code, http.StatusSeeOther)
	}
	cookies := redeem.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	if v != nil {
		if err := s.VerifyAndGrantAdmin(syntheticPassphrase); err != nil {
			t.Fatalf("VerifyAndGrantAdmin: %v", err)
		}
	}
	return cookies[0], s.csrf
}

func assertNoCanaryIn(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, canarySecretID) || strings.Contains(text, canarySecretValue) {
		t.Fatalf("%s leaked a secret value:\n%s", what, text)
	}
}

func assertConfigFileHasNoCanary(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	assertNoCanaryIn(t, "the configuration file", string(data))
}

// TestNewCredentialFormOffersPasswordInputs proves the acceptance criterion directly: a secret role is an
// ordinary, named HTML password input.
func TestNewCredentialFormOffersPasswordInputs(t *testing.T) {
	s, _, _, _ := newCredentialTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)

	rec := s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /credentials/new status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, name := range []string{"secret_token-id", "secret_token-secret"} {
		want := `type="password" name="` + name + `"`
		if !strings.Contains(body, want) {
			t.Fatalf("body does not carry a named password input %q:\n%s", want, body)
		}
	}
}

// TestCreateKeyringCredential proves a new keyring credential can be created, that its secrets land in the
// store and nowhere else, and that only their source is ever shown back.
func TestCreateKeyringCredential(t *testing.T) {
	s, store, mem, path := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil)
	cfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"wiki"}, "name": {"wiki-reader"}, "storage": {"keyring"}, "cfgver": {cfgver},
		"secret_token-id": {canarySecretID}, "secret_token-secret": {canarySecretValue},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /credentials/new status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/credentials/wiki-reader") {
		t.Fatalf("redirect location = %q, want /credentials/wiki-reader...", location)
	}
	assertNoCanaryIn(t, "the redirect location", location)

	for _, role := range []string{"token-id", "token-secret"} {
		value, err := mem.Get(t.Context(), secret.StoreKey("wiki-reader", role))
		if err != nil {
			t.Fatalf("mem.Get(%s): %v", role, err)
		}
		if role == "token-id" && value != canarySecretID {
			t.Fatalf("stored token-id = %q", value)
		}
		if role == "token-secret" && value != canarySecretValue {
			t.Fatalf("stored token-secret = %q", value)
		}
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	cred, ok := cfg.Credentials["wiki-reader"]
	if !ok || cred.Type != config.CredentialTypeKeyring || len(cred.Values) != 0 {
		t.Fatalf("credential entry = %+v, ok=%v", cred, ok)
	}
	assertConfigFileHasNoCanary(t, path)

	detail := s.request(t, http.MethodGet, "/credentials/wiki-reader", s.addr, cookie, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET /credentials/wiki-reader status = %d", detail.Code)
	}
	body := detail.Body.String()
	assertNoCanaryIn(t, "the credential detail page", body)
	if !strings.Contains(body, "in system keyring") {
		t.Fatalf("detail page does not show the stored source:\n%s", body)
	}
}

// TestCreateEnvCredentialStoresOnlyNames proves an env credential only ever writes variable names.
func TestCreateEnvCredentialStoresOnlyNames(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil)
	cfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"wiki"}, "name": {"wiki-env"}, "storage": {"env"}, "cfgver": {cfgver},
		"envname_token-id": {"WIKI_TOKEN_ID"}, "envname_token-secret": {"WIKI_TOKEN_SECRET"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	cred := cfg.Credentials["wiki-env"]
	if cred.Type != config.CredentialTypeEnv || cred.Values["token-id"] != "WIKI_TOKEN_ID" ||
		cred.Values["token-secret"] != "WIKI_TOKEN_SECRET" {
		t.Fatalf("credential entry = %+v", cred)
	}
	if _, err := mem.Get(t.Context(), secret.StoreKey("wiki-env", "token-id")); err == nil {
		t.Fatal("an env credential must never write to the credential store")
	}
}

// TestCreateVaultCredentialUnencrypted proves the vault's very first secret, offered no passphrase, leaves
// the vault unencrypted, exactly the same choice 'qatlas credential set' and the TUI already offer.
func TestCreateVaultCredentialUnencrypted(t *testing.T) {
	v := vaultAbsent(t)
	s, store, _, _ := newCredentialTestServer(t, v)
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil)
	cfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"wiki"}, "name": {"wiki-vault"}, "storage": {"vault"}, "cfgver": {cfgver},
		"secret_token-id": {canarySecretID}, "secret_token-secret": {canarySecretValue},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	state, err := v.State()
	if err != nil || state != vault.StateUnencrypted {
		t.Fatalf("vault state = %v, err = %v, want %v: leaving the passphrase empty must keep the vault "+
			"unencrypted", state, err, vault.StateUnencrypted)
	}
	value, found, _, err := v.Get("wiki-vault", "token-id", nil)
	if err != nil || !found || value != canarySecretID {
		t.Fatalf("v.Get(token-id) = %q, %v, %v, want %q, true, nil", value, found, err, canarySecretID)
	}
	_ = store
}

// TestCreateVaultCredentialEncryptsOnPassphrase proves a typed and confirmed passphrase becomes the vault's
// encryption.
func TestCreateVaultCredentialEncryptsOnPassphrase(t *testing.T) {
	v := vaultAbsent(t)
	s, _, _, _ := newCredentialTestServer(t, v)
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := s.request(t, http.MethodGet, "/credentials/new?provider=wiki", s.addr, cookie, nil)
	cfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"wiki"}, "name": {"wiki-vault"}, "storage": {"vault"}, "cfgver": {cfgver},
		"secret_token-id": {canarySecretID}, "secret_token-secret": {canarySecretValue},
		"vault_passphrase": {syntheticPassphrase}, "vault_passphrase_confirm": {syntheticPassphrase},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	if err := v.VerifyPassphrase(syntheticPassphrase); err != nil {
		t.Fatalf("the vault was not encrypted with the typed passphrase: %v", err)
	}
}

// TestReplaceRoleLeavesOtherRoleUntouched proves a role replace touches exactly the role it names.
func TestReplaceRoleLeavesOtherRoleUntouched(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetCredential("wiki-reader", config.Credential{Provider: "wiki", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	if err := mem.Set(secret.StoreKey("wiki-reader", "token-id"), "old-id-value"); err != nil {
		t.Fatalf("seed token-id: %v", err)
	}
	if err := mem.Set(secret.StoreKey("wiki-reader", "token-secret"), "old-secret-value"); err != nil {
		t.Fatalf("seed token-secret: %v", err)
	}

	form := s.request(t, http.MethodGet, "/credentials/wiki-reader", s.addr, cookie, nil)
	cfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	rec := s.postForm(t, "/credentials/wiki-reader/role", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"role": {"token-id"}, "value": {canarySecretValue}, "cfgver": {cfgver},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	got, err := mem.Get(t.Context(), secret.StoreKey("wiki-reader", "token-id"))
	if err != nil || got != canarySecretValue {
		t.Fatalf("token-id = %q, %v, want %q", got, err, canarySecretValue)
	}
	untouched, err := mem.Get(t.Context(), secret.StoreKey("wiki-reader", "token-secret"))
	if err != nil || untouched != "old-secret-value" {
		t.Fatalf("token-secret changed: %q, %v", untouched, err)
	}
}

// TestReplaceRoleConflictOnChangedConfig proves a config changed since the form was opened is refused as a
// conflict instead of silently applied.
func TestReplaceRoleConflictOnChangedConfig(t *testing.T) {
	s, store, mem, _ := newCredentialTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetCredential("wiki-reader", config.Credential{Provider: "wiki", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	form := s.request(t, http.MethodGet, "/credentials/wiki-reader", s.addr, cookie, nil)
	staleCfgver := extractHiddenValue(t, form.Body.String(), "cfgver")

	// The configuration changes after the form was shown, and before the form is submitted.
	cfg2, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg2.SetProviderNote("wiki", "changed after the form was opened"); err != nil {
		t.Fatalf("SetProviderNote: %v", err)
	}
	if err := store.Save(cfg2); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	rec := s.postForm(t, "/credentials/wiki-reader/role", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"role": {"token-id"}, "value": {canarySecretValue}, "cfgver": {staleCfgver},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered with a conflict, not a redirect)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "changed since this form was opened") {
		t.Fatalf("body does not report the conflict:\n%s", rec.Body.String())
	}
	assertNoCanaryIn(t, "the conflict page", rec.Body.String())
	if _, err := mem.Get(t.Context(), secret.StoreKey("wiki-reader", "token-id")); err == nil {
		t.Fatal("a stale save must not write anything")
	}
}

// TestCreateCredentialWithoutAdminApprovalHasNoEffect proves the admin guard actually protects the write
// route this task adds, the same way TestUnapprovedMutationHasNoEffect proves it for the guard in general.
func TestCreateCredentialWithoutAdminApprovalHasNoEffect(t *testing.T) {
	v := encryptedTestVault(t)
	s, store, mem, _ := newCredentialTestServer(t, v)
	link, err := url.Parse(s.URL())
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	redeem := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	cookies := redeem.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	cookie, csrf := cookies[0], s.csrf

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"wiki"}, "name": {"wiki-vault"}, "storage": {"vault"},
		"secret_token-id": {canarySecretID}, "secret_token-secret": {canarySecretValue},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if _, ok := cfg.Credentials["wiki-vault"]; ok {
		t.Fatal("a credential was written without admin approval")
	}
	if _, err := mem.Get(t.Context(), secret.StoreKey("wiki-vault", "token-id")); err == nil {
		t.Fatal("a secret was written without admin approval")
	}
}

// extractHiddenValue reads back the value of one hidden input this package's own templates render, so a
// test can carry a real cfgver or csrf value forward without hard-coding the template's markup.
func extractHiddenValue(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("hidden field %q not found in body:\n%s", name, body)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("hidden field %q has no closing quote in body:\n%s", name, body)
	}
	return rest[:j]
}
