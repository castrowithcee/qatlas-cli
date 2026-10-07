package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// testerCanarySecret stands in for a real secret a Tester's own error text might otherwise leak.
const testerCanarySecret = "canary-tester-secret-77fe"

// seedBookConnection saves a service, a keyring credential, and a connection reading it, straight through
// store: enough for the result page and the test route to find a connection by name, without this file
// having to drive the guided setup's own forms for every test of the test route.
func seedBookConnection(t *testing.T, store *config.Store, name string) {
	t.Helper()
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
	if err := cfg.SetConnection(name, config.Connection{Service: "book-cloud", Credential: "book-reader"}); err != nil {
		t.Fatalf("SetConnection: %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
}

// TestConnectionResultOffersTestButtonOnlyWithATester proves the result page's own button appears only
// when this run was given a Tester, and never claims a test is available otherwise.
func TestConnectionResultOffersTestButtonOnlyWithATester(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	seedBookConnection(t, store, "book-conn")
	cookie, _ := coupleAndApprove(t, s, nil)

	rec := s.request(t, http.MethodGet, "/connections/book-conn", s.addr, cookie, nil)
	body := rec.Body.String()
	if strings.Contains(body, "Test this connection") {
		t.Fatalf("a server with no Tester must not offer the test button:\n%s", body)
	}
	if !strings.Contains(body, "not available") {
		t.Fatalf("a server with no Tester must say so:\n%s", body)
	}
}

// TestConnectionTestButtonSucceeds proves a successful test, run through the injected Tester over the
// session-guarded route, shows a plain success line.
func TestConnectionTestButtonSucceeds(t *testing.T) {
	tester := Tester(func(_ context.Context, connection string) (provider.Class, error) {
		if connection != "book-conn" {
			t.Fatalf("tester called with connection = %q, want book-conn", connection)
		}
		return provider.ClassOK, nil
	})
	s, store, _, _ := newConnectionTestServer(t, nil, tester)
	seedBookConnection(t, store, "book-conn")
	cookie, csrf := coupleAndApprove(t, s, nil)

	rec := s.postForm(t, "/connections/book-conn/test", s.addr, cookie, "http://"+s.addr, csrf, url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Test succeeded") {
		t.Fatalf("body does not report the success:\n%s", rec.Body.String())
	}
}

// TestConnectionTestButtonRedactsError proves a failed test's own error text is redacted the same way
// every other error this package shows is, so a secret it names never reaches the response.
func TestConnectionTestButtonRedactsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write starter config: %v", err)
	}
	store := config.NewStore(path, bookCatalog{})
	if _, err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, secret.NewMemoryStore(), nil, red)

	tester := Tester(func(context.Context, string) (provider.Class, error) {
		// Stands in for a resolver that read a real secret before the provider rejected it: the redactor
		// already knows the value by the time the error carrying it reaches this run.
		red.Add(testerCanarySecret)
		return "", fmt.Errorf("auth failed with token %s", testerCanarySecret)
	})
	s, err := New(testOverview(), nil, defaultTestAdminTimeout, manage.New(store, resolver, connlog.SurfaceWeb, red), resolver, red, tester)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	seedBookConnection(t, store, "book-conn")
	cookie, csrf := coupleAndApprove(t, s, nil)

	rec := s.postForm(t, "/connections/book-conn/test", s.addr, cookie, "http://"+s.addr, csrf, url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, testerCanarySecret) {
		t.Fatalf("the test's own error leaked a secret value:\n%s", body)
	}
	if !strings.Contains(body, "could not run") {
		t.Fatalf("body does not report the test failure:\n%s", body)
	}
}

// TestConnectionTestButtonRequiresSessionAndCSRF proves the test route is refused exactly like every other
// mutating route without a coupled session or without the session's own CSRF value, even though it writes
// nothing itself.
func TestConnectionTestButtonRequiresSessionAndCSRF(t *testing.T) {
	tester := Tester(func(context.Context, string) (provider.Class, error) { return provider.ClassOK, nil })
	s, store, _, _ := newConnectionTestServer(t, nil, tester)
	seedBookConnection(t, store, "book-conn")

	noSession := s.postForm(t, "/connections/book-conn/test", s.addr, nil, "http://"+s.addr, "", url.Values{})
	if noSession.Code != http.StatusForbidden {
		t.Fatalf("without a session: status = %d, want 403", noSession.Code)
	}

	cookie, _ := coupleAndApprove(t, s, nil)
	noCSRF := s.postForm(t, "/connections/book-conn/test", s.addr, cookie, "http://"+s.addr, "", url.Values{})
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("without a CSRF value: status = %d, want 403", noCSRF.Code)
	}
}
