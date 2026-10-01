package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// newApprovalWebFixture builds a server wired for the guided connection routes, over a temporary
// configuration and a real, synthetic encrypted vault (never the real one under a person's home directory),
// already holding one connection ("shelf-conn") whose credential's secrets are already stored and never
// approved, so a test can prove that connection is left exactly as it found it: still open.
func newApprovalWebFixture(t *testing.T) (*Server, *config.Store, *vault.Vault) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write starter config: %v", err)
	}
	store := config.NewStore(path, bookCatalog{})
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}

	v := encryptedTestVault(t)
	mem := secret.NewMemoryStore()
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, mem, nil, red).WithVault(v, nil)

	if err := cfg.SetService("shelf-svc", config.Service{Provider: "book", BaseURL: "https://shelf.example.test"}); err != nil {
		t.Fatalf("SetService: %v", err)
	}
	if err := cfg.SetCredential("shelf-cred", config.Credential{Provider: "book", Type: config.CredentialTypeVault}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if err := cfg.SetConnection("shelf-conn", config.Connection{Service: "shelf-svc", Credential: "shelf-cred"}); err != nil {
		t.Fatalf("SetConnection: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	if err := resolver.SetVault("shelf-cred", "token-id", "shelf-id", nil); err != nil {
		t.Fatalf("SetVault: %v", err)
	}
	if err := resolver.SetVault("shelf-cred", "token-secret", "shelf-secret", nil); err != nil {
		t.Fatalf("SetVault: %v", err)
	}

	s, err := New(testOverview(), v, defaultTestAdminTimeout, store, resolver, red, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	return s, store, v
}

// createBookConnForm is the review page's own POST body to create a fresh vault-backed connection
// "book-conn", reusing no service or credential of the fixture. It first fetches the review page's own
// cfgver, exactly as a browser would, so the conflict check never refuses it as stale.
func createBookConnForm(t *testing.T, s *Server, cookie *http.Cookie) url.Values {
	t.Helper()
	base := url.Values{
		"provider": {"book"}, "service": {newServiceChoice}, "svcname": {"book-cloud"},
		"svcbaseurl": {"https://books.example.test"},
		"credential": {newCredentialChoice}, "credname": {"book-reader"}, "credstorage": {"vault"},
		"connname": {"book-conn"}, "permmode": {"default"}, "toolsmode": {"all"},
	}
	review := s.request(t, http.MethodGet, "/connections/new/review?"+base.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	form := cloneValues(base)
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
	form.Set("secret_token-id", "book-id")
	form.Set("secret_token-secret", "book-secret")
	return form
}

// TestGuidedConnectionApprovesOnlyTheNewConnection proves the acceptance criterion directly: saving a new
// connection over an encrypted, unlocked vault approves that connection alone, by the very rule
// internal/tui's own guided setup uses (approval.DirectApprovable), and leaves every other connection this
// vault already found open exactly as open as it was before - no sweep of the rest, unlike the TUI's own
// autoApprove. It also proves the redirect never carries a message.
func TestGuidedConnectionApprovesOnlyTheNewConnection(t *testing.T) {
	s, store, v := newApprovalWebFixture(t)
	cookie, csrf := coupleAndApprove(t, s, v)

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, createBookConnForm(t, s, cookie))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location != "/connections/book-conn?created=1" {
		t.Fatalf("redirect location = %q, want no message carried in it", location)
	}

	result := s.request(t, http.MethodGet, location, s.addr, cookie, nil)
	if result.Code != http.StatusOK {
		t.Fatalf("GET result status = %d, want 200, body: %s", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), "approved") {
		t.Fatalf("result page does not say the new connection was approved:\n%s", result.Body.String())
	}

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		t.Fatalf("approval.Pending: %v", err)
	}
	open := map[string]bool{}
	for _, c := range report.Open {
		open[c.Connection] = true
	}
	if open["book-conn"] {
		t.Fatalf("the new connection is still open after being approved: %+v", report.Open)
	}
	if !open["shelf-conn"] {
		t.Fatal("the pre-existing connection is no longer open; the test would prove nothing")
	}
}

// TestGuidedConnectionApprovalFailureIsAWarning proves a failed approval never claims success: the new
// connection reuses the fixture's already-stored "shelf-cred" (so this save itself writes only the
// configuration file, never the vault), and the vault directory is made unwritable right after this run's
// admin approval already unlocked it (state checks and approval.Pending are read-only and still succeed;
// only the write approval.Approve makes fails), so the connection is saved but reported as a warning, never
// as approved, and stays open.
func TestGuidedConnectionApprovalFailureIsAWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode is not meaningful on Windows")
	}
	s, store, v := newApprovalWebFixture(t)
	cookie, csrf := coupleAndApprove(t, s, v)

	if err := os.Chmod(v.Dir(), 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(v.Dir(), 0o700) })

	form := url.Values{
		"provider": {"book"}, "service": {"shelf-svc"}, "credential": {"shelf-cred"},
		"connname": {"book-conn"}, "permmode": {"default"}, "toolsmode": {"all"}, "cfgver": {"stale"},
	}
	// The stale cfgver above is refreshed against the vault-unwritable configuration itself, which the
	// chmod above never touches (it only blocks the vault directory).
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if review.Code != http.StatusOK {
		t.Fatalf("GET review status = %d, want 200, body: %s", review.Code, review.Body.String())
	}
	form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))

	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (the save itself still succeeds), body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")

	result := s.request(t, http.MethodGet, location, s.addr, cookie, nil)
	if result.Code != http.StatusOK {
		t.Fatalf("GET result status = %d, want 200, body: %s", result.Code, result.Body.String())
	}
	body := result.Body.String()
	if !strings.Contains(body, "approval failed") {
		t.Fatalf("result page does not report the approval failure:\n%s", body)
	}

	if err := os.Chmod(v.Dir(), 0o700); err != nil {
		t.Fatalf("Chmod (restore): %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if _, ok := cfg.Connections["book-conn"]; !ok {
		t.Fatal("the connection itself was not saved despite the approval failure")
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		t.Fatalf("approval.Pending: %v", err)
	}
	open := map[string]bool{}
	for _, c := range report.Open {
		open[c.Connection] = true
	}
	if !open["book-conn"] {
		t.Fatal("the connection whose approval failed is not open any more; the test would prove nothing")
	}
}

// TestGuidedConnectionUnencryptedVaultShowsNoApprovalNotice proves an unencrypted vault gets no approval
// notice at all, never a false "approved".
func TestGuidedConnectionUnencryptedVaultShowsNoApprovalNotice(t *testing.T) {
	v := vaultAbsent(t)
	s, _, _, _ := newConnectionTestServer(t, v)
	cookie, csrf := coupleAndApprove(t, s, nil) // an absent vault needs no admin approval either

	form := createBookConnForm(t, s, cookie)
	rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location != "/connections/book-conn?created=1" {
		t.Fatalf("redirect location = %q, want no message carried in it", location)
	}

	result := s.request(t, http.MethodGet, location, s.addr, cookie, nil)
	body := result.Body.String()
	if strings.Contains(body, "approved") || strings.Contains(body, "stays open") || strings.Contains(body, "approval failed") {
		t.Fatalf("an unencrypted vault must show no approval notice at all:\n%s", body)
	}
	if !strings.Contains(body, "Connection created.") {
		t.Fatalf("the plain creation notice is missing:\n%s", body)
	}
}
