package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const syntheticSecret = "sk-test-synthetic-0123456789"

func testOverview() Overview {
	return Overview{
		Providers:   []ProviderRow{{Provider: "github", Description: "Code hosting", Tools: 3, Connections: 1, Configured: 1}},
		Services:    []ServiceRow{{Name: "github-cloud", Provider: "github", BaseURL: "https://api.github.com"}},
		Credentials: []CredentialRow{{Name: "github-bot", Provider: "github", Type: "env"}},
		Connections: []ConnectionRow{{Name: "github-bot", Provider: "github", Description: syntheticSecret, Permissions: "read", Tools: "all-permitted"}},
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(testOverview())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	return s
}

// request builds a request against the server's own mux, without opening a real socket, with req.Host set
// to the server's loopback address unless the test overrides it.
func (s *Server) request(t *testing.T, method, target, host string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, req)
	return rec
}

func TestCouplingThenOverview(t *testing.T) {
	s := newTestServer(t)
	link, err := url.Parse(s.URL())
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	redeem := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	if redeem.Code != http.StatusSeeOther {
		t.Fatalf("redeem status = %d, want %d", redeem.Code, http.StatusSeeOther)
	}
	if loc := redeem.Header().Get("Location"); loc != "/" {
		t.Fatalf("redirect target = %q, want %q (token must not survive in the URL)", loc, "/")
	}
	cookies := redeem.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	sessionCookie := cookies[0]
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != "/" {
		t.Fatalf("session cookie is not HttpOnly/SameSite=Strict/Path=/: %+v", sessionCookie)
	}

	overview := s.request(t, http.MethodGet, "/", s.addr, sessionCookie, nil)
	if overview.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200, body: %s", overview.Code, overview.Body.String())
	}
	if !strings.Contains(overview.Body.String(), "github") {
		t.Fatalf("overview body missing expected content: %s", overview.Body.String())
	}
	for k, want := range map[string]string{
		"Cache-Control":   "no-store",
		"Referrer-Policy": "no-referrer",
	} {
		if got := overview.Header().Get(k); got != want {
			t.Fatalf("header %s = %q, want %q", k, got, want)
		}
	}
	if csp := overview.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("missing strict CSP: %q", csp)
	}
}

func TestTokenReuseRejected(t *testing.T) {
	s := newTestServer(t)
	link, _ := url.Parse(s.URL())

	first := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("first redemption status = %d, want %d", first.Code, http.StatusSeeOther)
	}

	second := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	if second.Code != http.StatusForbidden {
		t.Fatalf("reused token status = %d, want %d", second.Code, http.StatusForbidden)
	}
}

func TestTokenExpiryRejected(t *testing.T) {
	s := newTestServer(t)
	link, _ := url.Parse(s.URL())

	// Move the clock past the token's lifetime without sleeping.
	s.now = func() time.Time { return time.Now().Add(tokenTTL + time.Second) }

	rec := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expired token status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUncoupledRequestRefused(t *testing.T) {
	s := newTestServer(t)

	rec := s.request(t, http.MethodGet, "/", s.addr, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("uncoupled status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if strings.Contains(rec.Body.String(), "github") {
		t.Fatalf("uncoupled request must never see the overview: %s", rec.Body.String())
	}
}

func TestForeignSessionCookieRejected(t *testing.T) {
	s := newTestServer(t)
	rec := s.request(t, http.MethodGet, "/", s.addr, &http.Cookie{Name: sessionCookieName, Value: "not-a-real-session"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged session status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestForeignHostRejected(t *testing.T) {
	s := newTestServer(t)
	link, _ := url.Parse(s.URL())

	rec := s.request(t, http.MethodGet, link.RequestURI(), "evil.example:80", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestForeignOriginRejected(t *testing.T) {
	s := newTestServer(t)
	link, _ := url.Parse(s.URL())

	rec := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, map[string]string{"Origin": "http://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUnknownRouteNotFound(t *testing.T) {
	s := newTestServer(t)
	rec := s.request(t, http.MethodGet, "/does-not-exist", s.addr, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestWrongMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := s.request(t, http.MethodPost, "/", s.addr, nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestNoExternalAssetsInOverview(t *testing.T) {
	s := newTestServer(t)
	link, _ := url.Parse(s.URL())
	redeem := s.request(t, http.MethodGet, link.RequestURI(), s.addr, nil, nil)
	sessionCookie := redeem.Result().Cookies()[0]
	overview := s.request(t, http.MethodGet, "/", s.addr, sessionCookie, nil)
	body := overview.Body.String()
	for _, forbidden := range []string{"<script", "<link", "http://", "https://cdn", "src=\""} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("overview body must ship no external or scripted asset, found %q in: %s", forbidden, body)
		}
	}
}

// TestServerServesOverRealLoopback proves the whole thing end to end: a real 127.0.0.1 socket, coupling
// through it, and ctx cancellation actually closing the listener.
func TestServerServesOverRealLoopback(t *testing.T) {
	s, err := New(testOverview())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(s.URL())
	if err != nil {
		t.Fatalf("GET coupling URL: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after following redirect, body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "github") {
		t.Fatalf("missing overview content: %s", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}

	if _, err := http.Get(s.URL()); err == nil {
		t.Fatal("listener still accepting connections after shutdown")
	}
}
