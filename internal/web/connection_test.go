package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// bookMetadata and otherMetadata are two synthetic test providers, large enough to exercise a connection's
// target, effects, and tools, without pulling in a real provider package this test has no business
// depending on. book's target must not contain a space, so a malformed target has something concrete to be
// rejected for.
var bookMetadata = config.ProviderMetadata{
	ID: "book", Name: "BookStack", Description: "a synthetic test provider with tools",
	DefaultBaseURL:   "https://books.example.test",
	DefaultEffects:   []config.Effect{config.EffectRead},
	SupportedEffects: []config.Effect{config.EffectRead, config.EffectCreate},
	SecretRoles: []config.SecretRole{
		{Name: "token-id", Description: "the token id"},
		{Name: "token-secret", Description: "the token secret"},
	},
	Target: config.TargetMetadata{
		Label: "book", Description: "the book to reach", Multiple: true,
		Validate: func(v string) error {
			if strings.Contains(v, " ") {
				return fmt.Errorf("a book target must not contain a space")
			}
			return nil
		},
		// book.admin acts on a whole library and is reached only by a shelf target.
		Reaches: func(tool string, targets []string) (bool, []string) {
			if tool != "book.admin" {
				return true, nil
			}
			for _, target := range targets {
				if strings.HasPrefix(target, "shelf/") {
					return true, nil
				}
			}
			return false, []string{"shelf/NAME"}
		},
		Kinds: []config.TargetKind{{Name: "shelf", Forms: []string{"shelf/NAME"}}},
	},
	Tools: []config.ToolMetadata{
		{ID: "book.admin", Title: "Administer", Effect: config.EffectRead},
		{ID: "book.read", Title: "Read a book", Effect: config.EffectRead},
		{ID: "book.create", Title: "Create a book", Effect: config.EffectCreate},
	},
	Profiles: []config.ToolProfile{
		{ID: "reader", Title: "Reader", Recommended: true, Tools: []string{"book.read"}},
	},
	// book has a tool that reads local files, and none that writes them.
	LocalFiles: config.LocalFilesSupport{Read: true},
}

var otherMetadata = config.ProviderMetadata{
	ID: "other", Name: "Other", Description: "a second synthetic test provider",
	SecretRoles: []config.SecretRole{{Name: "api-key", Description: "the api key"}},
}

type bookCatalog struct{}

func (bookCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	switch id {
	case bookMetadata.ID:
		return bookMetadata, true
	case otherMetadata.ID:
		return otherMetadata, true
	}
	return config.ProviderMetadata{}, false
}

func (bookCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{bookMetadata, otherMetadata}
}

// newConnectionTestServer returns a server wired for the guided connection routes, over a temporary
// configuration file and an in-process credential store: never the real keyring, and never the real vault
// under a person's home directory.
func newConnectionTestServer(t *testing.T, v *vault.Vault, tester ...Tester) (*Server, *config.Store, *secret.MemoryStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write starter config: %v", err)
	}
	store := config.NewStore(path, bookCatalog{})
	if _, err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}

	mem := secret.NewMemoryStore()
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, mem, nil, red)
	if v != nil {
		resolver.WithVault(v, nil)
	}

	var tst Tester
	if len(tester) > 0 {
		tst = tester[0]
	}
	s, err := New(testOverview(), v, defaultTestAdminTimeout, manage.New(store, resolver, connlog.SurfaceWeb, red), resolver, red, tst)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	return s, store, mem, path
}

// newConnCanary and newConnCanaryValue stand in for a real credential secret in every test of this file.
const (
	newConnCanaryID    = "canary-conn-id-4d2e"
	newConnCanaryValue = "canary-conn-secret-9a1b"
)

// TestGuidedConnectionCreatesNewServiceAndCredential proves the acceptance criterion directly: from an
// empty configuration, the guided flow creates a service, a keyring credential, and the connection that
// binds them, with the credential's secrets landing in the store and nowhere else.
func TestGuidedConnectionCreatesNewServiceAndCredential(t *testing.T) {
	s, store, mem, path := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	build := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil)
	if build.Code != http.StatusOK {
		t.Fatalf("GET build page status = %d, want 200, body: %s", build.Code, build.Body.String())
	}

	reviewURL := "/connections/new/review?" + url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"keyring"},
		"connname": {"book-conn"}, "targets": {"lib1,lib2"}, "description": {"a test connection"},
		"permmode": {"custom"}, "perm": {"read"},
		"access": {"tools"}, "tool": {"book.read"},
	}.Encode()
	review := s.request(t, http.MethodGet, reviewURL, s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review page status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	body := review.Body.String()
	if !strings.Contains(body, "book-conn") || !strings.Contains(body, "lib1, lib2") {
		t.Fatalf("review page does not show the connection's own summary:\n%s", body)
	}
	cfgver := extractHiddenValue(t, body, "cfgver")

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"keyring"},
		"connname": {"book-conn"}, "targets": {"lib1,lib2"}, "description": {"a test connection"},
		"permmode": {"custom"}, "perm": {"read"},
		"access": {"tools"}, "tool": {"book.read"},
		"cfgver":              {cfgver},
		"secret_token-id":     {newConnCanaryID},
		"secret_token-secret": {newConnCanaryValue},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /connections/new/review status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/connections/book-conn") {
		t.Fatalf("redirect location = %q, want /connections/book-conn...", location)
	}
	assertNoCanaryValueIn(t, "the redirect location", location)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	svc, ok := cfg.Services["book-cloud"]
	if !ok || svc.Provider != "book" {
		t.Fatalf("service not created as expected: %+v, ok=%v", svc, ok)
	}
	cred, ok := cfg.Credentials["book-reader"]
	if !ok || cred.Type != config.CredentialTypeKeyring || len(cred.Values) != 0 {
		t.Fatalf("credential not created as expected: %+v, ok=%v", cred, ok)
	}
	conn, ok := cfg.Connections["book-conn"]
	if !ok || conn.Service != "book-cloud" || conn.Credential != "book-reader" {
		t.Fatalf("connection not created as expected: %+v, ok=%v", conn, ok)
	}
	if len(conn.Targets) != 2 || conn.Targets[0] != "lib1" || conn.Targets[1] != "lib2" {
		t.Fatalf("connection targets = %v, want [lib1 lib2]", conn.Targets)
	}
	if len(conn.Tools) != 1 || conn.Tools[0] != "book.read" {
		t.Fatalf("connection tools = %v, want [book.read]", conn.Tools)
	}

	for _, role := range []string{"token-id", "token-secret"} {
		want := newConnCanaryID
		if role == "token-secret" {
			want = newConnCanaryValue
		}
		got, err := mem.Get(t.Context(), secret.StoreKey("book-reader", role))
		if err != nil || got != want {
			t.Fatalf("mem.Get(%s) = %q, %v, want %q", role, got, err, want)
		}
	}
	assertConfigFileHasNoCanary2(t, path)
}

// TestGuidedConnectionReusesExistingServiceAndCredential proves an existing service and credential of the
// same provider are selectable without creating a duplicate.
func TestGuidedConnectionReusesExistingServiceAndCredential(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetService("book-cloud", config.Service{Provider: "book", BaseURL: "https://books.example.test"}); err != nil {
		t.Fatalf("SetService: %v", err)
	}
	if err := cfg.SetCredential("book-reader", config.Credential{Provider: "book", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	reviewURL := "/connections/new/review?" + url.Values{
		"provider": {"book"}, "service": {"book-cloud"}, "credential": {"book-reader"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
	}.Encode()
	review := s.request(t, http.MethodGet, reviewURL, s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	cfgver := extractHiddenValue(t, review.Body.String(), "cfgver")

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {"book-cloud"}, "credential": {"book-reader"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"}, "cfgver": {cfgver},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	cfg2, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if len(cfg2.Services) != 1 {
		t.Fatalf("a duplicate service was created: %v", cfg2.Services)
	}
	if len(cfg2.Credentials) != 1 {
		t.Fatalf("a duplicate credential was created: %v", cfg2.Credentials)
	}
	conn, ok := cfg2.Connections["book-conn"]
	if !ok || conn.Service != "book-cloud" || conn.Credential != "book-reader" {
		t.Fatalf("connection = %+v, ok=%v", conn, ok)
	}
}

// TestGuidedConnectionRejectsForeignCredentialProvider proves a credential of another provider is refused
// before any write.
func TestGuidedConnectionRejectsForeignCredentialProvider(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetCredential("other-cred", config.Credential{Provider: "other", Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"}, "credential": {"other-cred"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"}, "cfgver": {"stale-or-whatever"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (rejected back onto the build page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown credential") {
		t.Fatalf("body does not report the rejection:\n%s", rec.Body.String())
	}
	assertStoreUnchanged(t, store, 0, 1, 0)
}

// TestGuidedConnectionRejectsInvalidTarget proves a target the provider's own Validate rejects never
// reaches the file.
func TestGuidedConnectionRejectsInvalidTarget(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"}, "targets": {"has a space"},
		"permmode": {"default"}, "access": {"effects"}, "cfgver": {"whatever"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (rejected)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "space") {
		t.Fatalf("body does not report the invalid target:\n%s", rec.Body.String())
	}
	assertStoreUnchanged(t, store, 0, 0, 0)
}

// TestGuidedConnectionStoresSelectedToolsWithoutEffects proves a selected tool is saved as the tools alone,
// whatever effects the form ticks: the effects follow from the tools, so a connection never carries both.
func TestGuidedConnectionStoresSelectedToolsWithoutEffects(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"},
		"permmode": {"custom"}, "perm": {"read"},
		"access": {"tools"}, "tool": {"book.create"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review page status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	conn := cfg.Connections["book-conn"]
	if conn.Effects != nil || len(conn.Tools) != 1 || conn.Tools[0] != "book.create" {
		t.Fatalf("connection = %+v, want only the tool book.create", conn)
	}
}

// TestGuidedConnectionStoresEffectsWithoutToolsWhenAccessIsByEffect proves the access radio decides the
// stored field: ticked tools of the other mode are ignored.
func TestGuidedConnectionStoresEffectsWithoutToolsWhenAccessIsByEffect(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"},
		"permmode": {"custom"}, "perm": {"read"},
		"access": {"effects"}, "tool": {"book.create"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review page status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	conn := cfg.Connections["book-conn"]
	if conn.Tools != nil || len(conn.Effects) != 1 || conn.Effects[0] != config.EffectRead {
		t.Fatalf("connection = %+v, want only the effect read", conn)
	}
}

// TestGuidedConnectionWithoutAdminApprovalHasNoEffect proves the admin guard actually protects the guided
// connection route.
func TestGuidedConnectionWithoutAdminApprovalHasNoEffect(t *testing.T) {
	v := encryptedTestVault(t)
	s, store, mem, _ := newConnectionTestServer(t, v)
	link, err := url.Parse(s.URL())
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	redeem := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	cookies := redeem.Result().Cookies()
	cookie, csrf := cookies[0], s.csrf

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"keyring"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
		"secret_token-id": {newConnCanaryID}, "secret_token-secret": {newConnCanaryValue},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	assertStoreUnchanged(t, store, 0, 0, 0)
	if _, err := mem.Get(t.Context(), secret.StoreKey("book-reader", "token-id")); err == nil {
		t.Fatal("a secret was written without admin approval")
	}
}

// TestGuidedConnectionStaleConfigConflict proves a configuration changed since the review page was shown is
// refused as a conflict instead of silently overwritten.
func TestGuidedConnectionStaleConfigConflict(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	reviewURL := "/connections/new/review?" + url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
	}.Encode()
	review := s.request(t, http.MethodGet, reviewURL, s.addr, cookie, nil)
	staleCfgver := extractHiddenValue(t, review.Body.String(), "cfgver")

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetProviderNote("book", "changed after the review page was shown"); err != nil {
		t.Fatalf("SetProviderNote: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"}, "cfgver": {staleCfgver},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (conflict, not a redirect)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "changed since this form was opened") {
		t.Fatalf("body does not report the conflict:\n%s", rec.Body.String())
	}
	assertStoreUnchanged(t, store, 0, 0, 0)
}

// TestGuidedConnectionBuildPageKeepsInputsOnError proves a rejected build-page submission keeps every
// non-secret field the person already typed, so the correction does not start over.
func TestGuidedConnectionBuildPageKeepsInputsOnError(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)

	reviewURL := "/connections/new/review?" + url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {""}, // empty service name is refused
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"keyring"},
		"connname": {"book-conn"}, "description": {"kept across the error"},
		"permmode": {"default"}, "access": {"effects"},
	}.Encode()
	rec := s.request(t, http.MethodGet, reviewURL, s.addr, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (rejected back onto the build page)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "book-reader") || !strings.Contains(body, "kept across the error") {
		t.Fatalf("build page did not keep the person's own input on error:\n%s", body)
	}
}

// TestGuidedConnectionNoSecretInAnyResponse proves a secret typed on the review page never appears in the
// build page it fails back to, the redirect, or the configuration file, mirroring the credential form's own
// same proof.
func TestGuidedConnectionNoSecretInAnyResponse(t *testing.T) {
	s, store, _, path := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	// A missing role secret fails the commit, but the request still carried a value for the other role: it
	// must never be echoed back.
	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"keyring"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
		"secret_token-id": {newConnCanaryValue}, // token-secret left empty on purpose
	})
	assertNoCanaryValueIn(t, "the failed commit's response", rec.Body.String())
	assertConfigFileHasNoCanary2(t, path)
	assertStoreUnchanged(t, store, 0, 0, 0)
}

func assertNoCanaryValueIn(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, newConnCanaryID) || strings.Contains(text, newConnCanaryValue) {
		t.Fatalf("%s leaked a secret value:\n%s", what, text)
	}
}

func assertConfigFileHasNoCanary2(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	assertNoCanaryValueIn(t, "the configuration file", string(data))
}

func assertStoreUnchanged(t *testing.T, store *config.Store, wantServices, wantCredentials, wantConnections int) {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if len(cfg.Services) != wantServices || len(cfg.Credentials) != wantCredentials || len(cfg.Connections) != wantConnections {
		t.Fatalf("configuration changed: services=%d credentials=%d connections=%d, want %d/%d/%d",
			len(cfg.Services), len(cfg.Credentials), len(cfg.Connections), wantServices, wantCredentials, wantConnections)
	}
}

// filesFormValues is a complete, valid guided connection form for the book provider over an env credential,
// which needs no secret page, with extra values added on top.
func filesFormValues(extra url.Values) url.Values {
	v := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
	}
	for key, vals := range extra {
		v[key] = vals
	}
	return v
}

// TestGuidedConnectionOffersFilesFieldsByDirection proves a directory field appears only for a direction the
// provider has a tool for.
func TestGuidedConnectionOffersFilesFieldsByDirection(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)

	body := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil).Body.String()
	if !strings.Contains(body, `name="filesread"`) {
		t.Errorf("build page of a provider that reads local files has no upload field:\n%s", body)
	}
	if strings.Contains(body, `name="fileswrite"`) {
		t.Errorf("build page of a provider that writes no local files offers a download field:\n%s", body)
	}
	body = s.request(t, http.MethodGet, "/connections/new?provider=other", s.addr, cookie, nil).Body.String()
	if strings.Contains(body, `name="filesread"`) || strings.Contains(body, `name="fileswrite"`) {
		t.Errorf("build page of a provider without local files offers a directory field:\n%s", body)
	}
}

// TestGuidedConnectionFilesRoundTrip proves the directories typed on the build page reach the review page
// and the saved connection, one per line, and show on the result page.
func TestGuidedConnectionFilesRoundTrip(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	// An absolute path needs a drive on Windows.
	drive := map[bool]string{true: "C:"}[runtime.GOOS == "windows"]
	a, b := drive+"/srv/in-a", drive+"/srv/in-b"
	extra := url.Values{"filesread": {a + "\r\n\n " + b + " "}}

	review := s.request(t, http.MethodGet, "/connections/new/review?"+filesFormValues(extra).Encode(),
		s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("review status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	body := review.Body.String()
	if !strings.Contains(body, "read: "+a+" "+b) {
		t.Errorf("review page does not summarise the files release:\n%s", body)
	}
	form := filesFormValues(extra)
	form.Set("cfgver", extractHiddenValue(t, body, "cfgver"))
	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	got := cfg.Connections["book-conn"].Files
	if want := []string{a, b}; len(got.Read) != 2 || got.Read[0] != want[0] || got.Read[1] != want[1] || len(got.Write) != 0 {
		t.Fatalf("saved files = %+v, want read %v and no write", got, want)
	}
	result := s.request(t, http.MethodGet, rec.Header().Get("Location"), s.addr, cookie, nil).Body.String()
	if !strings.Contains(result, "read: "+a+" "+b) {
		t.Errorf("result page does not show the files release:\n%s", result)
	}
}

// TestGuidedConnectionRejectsFilesWithoutDirection proves a directory for a direction the provider has no
// tool for is refused before any write, however the request was made, and that the refusal names no path.
func TestGuidedConnectionRejectsFilesWithoutDirection(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)

	for name, extra := range map[string]url.Values{
		"write on a read-only provider": {"fileswrite": {"/srv/secret-out"}},
		"too broad an entry":            {"filesread": {"/"}},
	} {
		form := filesFormValues(extra)
		form.Set("cfgver", "whatever")
		rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (rejected)", name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "files.") {
			t.Errorf("%s: body does not report the refused files entry:\n%s", name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret-out") {
			t.Errorf("%s: body quotes the refused path", name)
		}
	}
	assertStoreUnchanged(t, store, 0, 0, 0)
}

// TestGuidedConnectionCredentialChoiceUsesDerivedProvider proves the build page does not offer a credential
// that names no provider but is already used by a connection of another provider.
func TestGuidedConnectionCredentialChoiceUsesDerivedProvider(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetService("other-svc", config.Service{Provider: "other", BaseURL: "https://other.example.test"}); err != nil {
		t.Fatalf("SetService: %v", err)
	}
	for _, name := range []string{"used-by-other", "still-open"} {
		if err := cfg.SetCredential(name, config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
			t.Fatalf("SetCredential: %v", err)
		}
	}
	if err := cfg.SetConnection("other-conn", config.Connection{Service: "other-svc", Credential: "used-by-other"}); err != nil {
		t.Fatalf("SetConnection: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	rec := s.request(t, http.MethodGet, "/connections/new?provider=book", s.addr, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "used-by-other") {
		t.Errorf("a credential of another provider's connection is offered:\n%s", body)
	}
	if !strings.Contains(body, "still-open") {
		t.Errorf("a credential no connection places is not offered:\n%s", body)
	}
}

// The warning of a saved setup keeps the one "warning: " it already starts with.
func TestCreatedNoticeKeepsTheWarningsOwnPrefix(t *testing.T) {
	const warning = "warning: the vault holds the change"
	tests := []struct{ notice, warning, want string }{
		{"", "", ""},
		{"approved", "", "approved"},
		{"", warning, warning},
		{"approved", warning, warning + "; approved"},
	}
	for _, tt := range tests {
		if got := createdNotice(tt.notice, tt.warning); got != tt.want {
			t.Errorf("createdNotice(%q, %q) = %q, want %q", tt.notice, tt.warning, got, tt.want)
		}
	}
}

// A schema-2 file keeps the ids of the entries a guided connection reuses and gives the new connection one.
func TestGuidedConnectionKeepsStableIDs(t *testing.T) {
	s, store, _, path := newConnectionTestServer(t, nil)
	const idSvc, idCred = "00000000000000000000000000000001", "00000000000000000000000000000002"
	file := "version: 3\nservices:\n  book-cloud:\n    id: " + idSvc + "\n    provider: book\n" +
		"    base_url: https://books.example.test\ncredentials:\n  book-reader:\n    id: " + idCred +
		"\n    provider: book\n    type: keyring\n"
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := coupleAndApprove(t, s, nil)

	values := url.Values{
		"provider": {"book"}, "service": {"book-cloud"}, "credential": {"book-reader"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+values.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review status = %d, body: %s", review.Code, review.Body.String())
	}
	values.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, values)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["book-cloud"].ID != idSvc || cfg.Credentials["book-reader"].ID != idCred || cfg.Connections["book-conn"].ID == "" {
		t.Errorf("ids = %q %q %q", cfg.Services["book-cloud"].ID, cfg.Credentials["book-reader"].ID, cfg.Connections["book-conn"].ID)
	}
}

func labelForm(extra url.Values) url.Values {
	form := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"env"},
		"envname_token-id": {"BOOK_TOKEN_ID"}, "envname_token-secret": {"BOOK_TOKEN_SECRET"},
		"connname": {"book-conn"}, "permmode": {"default"}, "access": {"effects"},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

func saveLabelForm(t *testing.T, s *Server, cookie *http.Cookie, csrf string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("review status = %d, body: %s", review.Code, review.Body.String())
	}
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	return s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
}

func TestGuidedConnectionTargetLabelsRoundTrip(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := labelForm(url.Values{"targets": {"a, b, c"},
		"label_a": {"Front"}, "label_b": {"  "}, "label_c": {"Back"}, "label_gone": {"Old"}})
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	body := review.Body.String()
	if !strings.Contains(body, `name="label_a" value="Front"`) || strings.Contains(body, "label_gone") {
		t.Fatalf("review does not carry the labels of the current targets: %s", body)
	}
	if !strings.Contains(body, "a (Front), b, c (Back)") {
		t.Fatalf("review summary lacks the labels: %s", body)
	}
	rec := saveLabelForm(t, s, cookie, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "Front", "c": "Back"}
	if got := cfg.Connections["book-conn"].TargetLabels; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	result := s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil)
	if !strings.Contains(result.Body.String(), "a (Front), b, c (Back)") {
		t.Fatalf("result page lacks the labels: %s", result.Body.String())
	}
}

func TestGuidedConnectionTargetLabelIsEscaped(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	form := labelForm(url.Values{"targets": {"a"}, "label_a": {"<script>x</script>"}})
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if strings.Contains(review.Body.String(), "<script>x") {
		t.Fatalf("label is not escaped: %s", review.Body.String())
	}
}

func TestGuidedConnectionRefusesInvalidTargetLabel(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	form := labelForm(url.Values{"targets": {"a"}, "label_a": {"two\nlines"}})
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if !strings.Contains(review.Body.String(), "notice-error") {
		t.Fatalf("invalid label shows no error: %s", review.Body.String())
	}
	if cfg, _ := store.Load(); len(cfg.Connections) != 0 {
		t.Fatal("a connection was saved")
	}
}

func TestGuidedConnectionShowsAndDropsToolsOutsideTheTargets(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := labelForm(url.Values{"targets": {"a"}, "access": {"tools"}, "tool": {"book.admin", "book.read"}})
	build := s.request(t, http.MethodGet, "/connections/new?"+form.Encode(), s.addr, cookie, nil)
	body := build.Body.String()
	if !strings.Contains(body, "not available, outside the targets: needs shelf/NAME") ||
		!strings.Contains(body, "disabled") {
		t.Fatalf("build page does not explain the unreached tool: %s", body)
	}
	if rec := saveLabelForm(t, s, cookie, csrf, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if got := cfg.Connections["book-conn"].Tools; !reflect.DeepEqual(got, []string{"book.read"}) {
		t.Fatalf("tools = %v, want only book.read", got)
	}
}

func TestGuidedConnectionBuildPageKeepsTargetLabels(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	// An invalid target returns to the build page; the labels of the other targets must survive it.
	bad := labelForm(url.Values{"targets": {"a, b c"}, "label_a": {"<i>Front</i>"}})
	build := s.request(t, http.MethodGet, "/connections/new/review?"+bad.Encode(), s.addr, cookie, nil)
	body := build.Body.String()
	if !strings.Contains(body, `name="label_a" value="&lt;i&gt;Front&lt;/i&gt;"`) || strings.Contains(body, "<i>Front") {
		t.Fatalf("build page lacks the escaped label: %s", body)
	}
	good := labelForm(url.Values{"targets": {"a"}, "label_a": {"<i>Front</i>"}})
	if rec := saveLabelForm(t, s, cookie, csrf, good); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	if got := cfg.Connections["book-conn"].TargetLabels["a"]; got != "<i>Front</i>" {
		t.Fatalf("label = %q", got)
	}
}

// The review summary shows the base URL of a new or reused service without the credentials in it.
func TestGuidedConnectionSummaryMasksTheBaseURLCredentials(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	const host = "books.example.test"
	baseURL := "https://user:canary-url-pass@" + host + "/api?token=canary-url-query&page=1"

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if err := cfg.SetService("book-old", config.Service{Provider: "book", BaseURL: "https://canary-url-user@" + host}); err != nil {
		t.Fatalf("SetService: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	for name, values := range map[string]url.Values{
		"new service": {"service": {newServiceChoice}, "svcname": {"book-cloud"}, "svcbaseurl": {baseURL}},
		"reused":      {"service": {"book-old"}},
	} {
		values.Set("provider", "book")
		values.Set("credential", newCredentialChoice)
		values.Set("credname", "book-reader")
		values.Set("credstorage", "keyring")
		values.Set("connname", "book-conn")
		values.Set("permmode", "default")
		values.Set("access", "effects")
		review := s.request(t, http.MethodGet, "/connections/new/review?"+values.Encode(), s.addr, cookie, nil)
		if review.Code != http.StatusOK {
			t.Fatalf("%s: GET review status = %d, body: %s", name, review.Code, review.Body.String())
		}
		body := review.Body.String()
		// The hidden field that carries the typed value on to the commit is no summary row.
		summary := body[:strings.Index(body, `type="hidden"`)]
		for _, canary := range []string{"canary-url-pass", "canary-url-query", "canary-url-user"} {
			if strings.Contains(summary, canary) {
				t.Errorf("%s: the summary shows %q", name, canary)
			}
		}
		if !strings.Contains(summary, "***") {
			t.Errorf("%s: the summary lacks the mask:\n%s", name, summary)
		}
	}
}
