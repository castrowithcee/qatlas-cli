// Package web serves a local, single-run browser overview of the configured providers, services,
// credentials, and connections. It never reads or publishes a secret value: the overview it renders is
// built by its caller from configuration and registry metadata alone.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Tester runs the same safe connection test internal/tui's own editor runs, for one saved connection by
// name. It is injected by the caller (see internal/cli/web.go's own connectionTester), exactly the way
// internal/tui.Tester is: this package knows no provider and resolves nothing on its own, so the browser's
// "Test this connection" button can never diverge from the TUI's own test path. A nil Tester leaves the
// button unusable, but every earlier route unaffected: a test server built for the overview and the admin
// guard alone (see New) never has to supply one.
type Tester func(ctx context.Context, connection string) (provider.Class, error)

// tokenTTL bounds how long the one-time access token stays valid before it must be discarded and a new
// run started. It is short: the token only ever has to survive the moment between printing it and a
// browser loading it.
const tokenTTL = 5 * time.Minute

// ProviderRow is one secretfree row of the provider overview.
type ProviderRow struct {
	Provider    string
	Description string
	Note        string
	Tools       int
	Connections int
	Configured  int
}

// ServiceRow is one secretfree row of the service overview: a technical endpoint of a provider. BaseURL is
// where the service is reached, never a credential.
type ServiceRow struct {
	Name     string
	Provider string
	BaseURL  string
}

// CredentialRow is one secretfree row of the credential overview: which source a credential names, never
// what it holds. It never carries Values (environment variable names for a credential of type "env"), so
// the overview stays free of anything that names where a secret currently lives.
type CredentialRow struct {
	Name     string
	Provider string
	Type     string
}

// ConnectionRow is one secretfree row of the connection overview: what a connection may do, never where it
// leads or what it authenticates with.
type ConnectionRow struct {
	Name        string
	Provider    string
	Description string
	Permissions string
	Tools       string
}

// Overview is the secretfree snapshot a coupled browser is shown. It is built once by the caller before
// the server starts, from configuration and registry metadata alone; the server itself never resolves a
// secret and never reloads configuration.
type Overview struct {
	Providers   []ProviderRow
	Services    []ServiceRow
	Credentials []CredentialRow
	Connections []ConnectionRow
}

// Server is a local-only HTTP server that shows exactly one browser, coupled once with a one-time token,
// the overview of one qatlas run. It binds only to the IPv4 loopback interface and never accepts a
// connection whose declared Host or Origin names anything else.
type Server struct {
	listener net.Listener
	http     *http.Server
	addr     string
	tmpl     *template.Template

	// now is a seam so a test can move the clock without sleeping.
	now func() time.Time

	mu         sync.Mutex
	token      string
	tokenAt    time.Time
	tokenUsed  bool
	sessionSet bool
	session    string
	csrf       string
	// pendingNotice is a short-lived, one-time hint for this run's one coupled session: a route that just
	// mutated something (see handleCreateConnection) sets it instead of carrying its message in a redirect's
	// own URL, and the next page that shows it consumes it, so it never sits in the address bar, browser
	// history, or a referrer. It never outlives the session it was set for (see close and redeem).
	pendingNotice string

	// vault is this run's vault, or nil when none is configured. The admin approval checks its live state
	// through it and never resolves or caches a secret of its own.
	vault *vault.Vault
	// adminTimeout is vault.admin_timeout: how long an admin approval stays active without activity of the
	// coupled session, or 0, which means an approval only ever covers exactly the next mutation.
	adminTimeout time.Duration
	// adminUntil is the admin approval's idle deadline for adminTimeout > 0; the zero value means no
	// session-shaped approval is active.
	adminUntil time.Time
	// adminOnce is the one-shot admin approval adminTimeout == 0 grants: good for exactly the next mutation
	// withAdminGuard lets through, then cleared.
	adminOnce bool

	// writeMu serializes every mutation this run's coupled session may make to config, keyring, or vault, so
	// two concurrent writes from the same session can never interleave.
	writeMu sync.Mutex

	overview Overview

	// store, secrets, and redactor back the credential forms (see credential.go). They are nil in a test
	// that only exercises the overview and the admin guard, which never reaches a route that needs them.
	store    *config.Store
	secrets  *secret.Resolver
	redactor *redact.Redactor

	// tester runs the connection test the result page's own button offers (see connection.go). It is nil in
	// a test, or a run, that never wires one up, which only makes the button unusable, never any other route.
	tester Tester

	credTmpl *template.Template
}

// New starts listening on 127.0.0.1:0 (an OS-assigned port on the IPv4 loopback interface only) and
// returns a server ready to run. overview is shown to the browser that redeems the printed URL's token. v is
// this run's vault, or nil when none is configured; adminTimeout is vault.admin_timeout, read once at
// startup exactly like every other vault-facing setting this run uses.
//
// store and secrets back the credential forms (see credential.go): store loads and saves the configuration
// file this run uses, and secrets is the same resolver the rest of this run reads and writes credentials
// through. redactor removes secret values from anything a credential form's own error text might otherwise
// carry. All three may be nil, which leaves the overview and the admin approval usable and every credential
// route refusing with a fixed, generic error, never a partial write: a test that only exercises those two
// never has to build a configuration store or a resolver of its own.
//
// tester runs the connection test the result page's "Test this connection" button offers (see
// connection.go and internal/cli/web.go's own connectionTester); nil leaves the button unusable without
// affecting anything else this run serves.
func New(overview Overview, v *vault.Vault, adminTimeout time.Duration, store *config.Store,
	secrets *secret.Resolver, redactor *redact.Redactor, tester Tester) (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	tmpl, err := template.New("overview").Parse(overviewTemplate)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	credTmpl, err := template.New("credentials").Parse(credentialTemplates + payloadTemplates)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	s := &Server{
		listener:     listener,
		addr:         listener.Addr().String(),
		tmpl:         tmpl,
		credTmpl:     credTmpl,
		now:          time.Now,
		token:        token,
		overview:     overview,
		vault:        v,
		adminTimeout: adminTimeout,
		store:        store,
		secrets:      secrets,
		redactor:     redactor,
		tester:       tester,
	}
	s.tokenAt = s.now()
	s.http = &http.Server{Handler: s.mux()}
	return s, nil
}

// randomToken returns a cryptographically random, URL-safe, one-time value.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randomSession returns a cryptographically random session value, distinct from any access token so a
// leaked or logged token can never stand in for the browser session it once created.
func randomSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// randomCSRF returns a cryptographically random value, distinct from the session cookie, that a coupled
// session's own form must echo back as proof it, and not some other origin, submitted the request.
func randomCSRF() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// URL returns the one-time coupling link this run's browser must open first.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?token=%s", s.addr, s.token)
}

// Addr returns the 127.0.0.1 address, with the OS-assigned port, this run listens on.
func (s *Server) Addr() string { return s.addr }

// Run serves requests until ctx is cancelled, then closes the listener and discards the session and
// token, so nothing of this run answers again. It never terminates the process and never takes over a
// port another process already holds: New already bound the actual port before Run is called.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.Serve(s.listener) }()
	select {
	case <-ctx.Done():
		s.close()
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}

// close shuts the listener down and discards every credential of this run, so a request arriving after
// Close is never coupled and never sees the overview again. It also discards this run's admin approval,
// whatever it was: stopping the process is exactly what "stopping the process discards the approval" means.
func (s *Server) close() {
	_ = s.http.Close()
	s.mu.Lock()
	s.tokenUsed = true
	s.sessionSet = false
	s.session = ""
	s.csrf = ""
	s.adminUntil = time.Time{}
	s.adminOnce = false
	s.pendingNotice = ""
	s.mu.Unlock()
}

// setNotice stores text as the coupled session's own one-time hint, replacing whatever it held before.
func (s *Server) setNotice(text string) {
	s.mu.Lock()
	s.pendingNotice = text
	s.mu.Unlock()
}

// takeNotice returns the coupled session's one-time hint and clears it, so the next page that asks finds
// nothing left to show a second time.
func (s *Server) takeNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	notice := s.pendingNotice
	s.pendingNotice = ""
	return notice
}

const sessionCookieName = "qatlas_web_session"

func (s *Server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("POST /admin", s.withSessionGuard(s.handleAdminAuth))
	mux.HandleFunc("GET /credentials/new", s.withSession(s.handleNewCredentialForm))
	mux.HandleFunc("POST /credentials/new", s.withAdminGuard(s.handleCreateCredential))
	mux.HandleFunc("GET /credentials/new/payload", s.withSession(s.handleNewPayloadForm))
	mux.HandleFunc("POST /credentials/new/payload", s.withAdminGuard(s.handleCreatePayload))
	mux.HandleFunc("GET /credentials/{name}", s.withSession(s.handleCredentialForm))
	mux.HandleFunc("POST /credentials/{name}/payload", s.withAdminGuard(s.handleSavePayload))
	mux.HandleFunc("POST /credentials/{name}/role", s.withAdminGuard(s.handleReplaceRole))
	mux.HandleFunc("GET /connections/new", s.withSession(s.handleConnectionsNew))
	mux.HandleFunc("GET /connections/new/review", s.withSession(s.handleConnectionReview))
	mux.HandleFunc("POST /connections/new/review", s.withAdminGuard(s.handleCreateConnection))
	mux.HandleFunc("GET /connections/{name}", s.withSession(s.handleConnectionResult))
	mux.HandleFunc("POST /connections/{name}/forward", s.withAdminGuard(s.handleSetForward))
	mux.HandleFunc("POST /connections/{name}/test", s.withSessionGuard(s.handleTestConnection))
	return s.withSecurityHeaders(s.withLocalBoundary(mux))
}

// withSession is the baseline every read-only credential page needs: this run's one coupled browser
// session, and nothing beyond it. It never parses a body and never checks an Origin or a CSRF value, unlike
// withSessionGuard, because a GET carries no form and has no effect to protect. It also renews the admin
// approval's idle deadline, the same way handleRoot already does for the overview.
func (s *Server) withSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !s.validSession(cookie.Value) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.touchAdminIfActive()
		next(w, r)
	}
}

// withSecurityHeaders adds the headers every response of this server carries, whatever it answers:
// nothing is ever cached, no referrer ever leaves this response, and a content security policy keeps a
// browser from loading anything but the document itself and submitting its own forms back to it, since the
// page ships no external asset and no script.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'none'; script-src 'none'; img-src 'none'; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// withLocalBoundary refuses a request that does not name this run's own loopback address as its Host, and
// one whose Origin, when it sends one at all, names anything else. Neither check ever explains itself
// beyond "forbidden": the reason is never handed to a caller that failed either check. A mutating route
// additionally requires an Origin at all (see withSessionGuard), since an absent one there proves nothing
// about where the request came from; this boundary alone stays permissive about a missing Origin so an
// ordinary GET, and an unknown method's own 405 from the mux, are unaffected by it.
func (s *Server) withLocalBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleRoot either redeems a one-time token, or shows the overview to an already coupled browser. It
// serves exactly one route, the root path with the GET method; every other path or method is refused
// before this handler is ever reached, by the ServeMux's own routing.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && s.validSession(cookie.Value) {
		s.touchAdminIfActive()
		s.renderOverview(w, "")
		return
	}
	if token := r.URL.Query().Get("token"); token != "" {
		session, ok := s.redeem(token)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    session,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		// The token leaves the URL once it is redeemed, so it never sits in browser history, a bookmark, or
		// a referrer.
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

// redeem checks token in constant time against the one this run printed, refuses one already used or
// past tokenTTL, and on success starts this run's single browser session and consumes the token so it can
// never be redeemed again.
func (s *Server) redeem(token string) (session string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokenUsed || s.now().After(s.tokenAt.Add(tokenTTL)) {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		return "", false
	}
	newSession, err := randomSession()
	if err != nil {
		return "", false
	}
	csrf, err := randomCSRF()
	if err != nil {
		return "", false
	}
	s.tokenUsed = true
	s.session = newSession
	s.sessionSet = true
	s.csrf = csrf
	// A freshly coupled session starts every run's admin approval from scratch, whatever an earlier
	// coupling of this same run once had: close() already clears it when a session ends, but a fresh
	// redeem is the other place an approval must never carry over from.
	s.adminUntil = time.Time{}
	s.adminOnce = false
	return newSession, true
}

// validSession reports, in constant time, whether cookie is this run's current, still-open session.
func (s *Server) validSession(cookie string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionSet {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(s.session)) == 1
}

// pageData is what the overview template renders: the secretfree Overview, together with this run's
// current admin status and, only while an approval still needs proving, the CSRF value its own masked form
// carries as a hidden field. adminError is never anything but a fixed, generic string: it never carries a
// vault's own error text, so nothing about why a passphrase failed ever reaches the page.
type pageData struct {
	Overview
	AdminActive        bool
	AdminUnprotected   bool
	AdminRemainingText string
	AdminError         string
	CSRF               string
	// CredentialsUsable reports whether this run was given a configuration store and a resolver at all, so
	// the overview page can hide the credential links rather than send a browser to a route that can only
	// ever refuse.
	CredentialsUsable bool
}

// credentialRows rebuilds the Credentials section of the overview from a freshly loaded configuration, the
// way every credential route reads the configuration too (see credential.go): never from the one-time
// snapshot New was built with. It reports an error rather than a partial list when the file cannot be read
// right now, so a transient failure never replaces a real credential list with an empty one.
func (s *Server) credentialRows() ([]CredentialRow, error) {
	if s.store == nil {
		return nil, fmt.Errorf("no configuration store is configured for this run")
	}
	cfg, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	rows := make([]CredentialRow, 0, len(cfg.Credentials))
	for _, name := range sortedNames(cfg.Credentials) {
		cred := cfg.Credentials[name]
		rows = append(rows, CredentialRow{Name: name, Provider: cred.Provider, Type: cred.Type})
	}
	return rows, nil
}

// sortedNames returns the keys of m in stable, ascending order. It is web's own copy of the same one-line
// helper internal/cli keeps for the same purpose: too small to be worth a shared package for.
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// renderOverview shows the coupled browser this run's overview together with its admin status: active with
// remaining time, not active with the masked passphrase form, or unprotected because this run's vault holds
// no passphrase to prove at all. adminError, when not empty, is the one generic line a failed passphrase
// attempt on the browser form is shown.
func (s *Server) renderOverview(w http.ResponseWriter, adminError string) {
	s.mu.Lock()
	required, stateErr := s.adminRequiredLocked()
	unprotected := stateErr == nil && !required
	active := s.adminActiveLocked()
	var remaining string
	switch {
	case active && required && s.adminOnce:
		remaining = "the next change only"
	case active && required && !s.adminUntil.IsZero():
		remaining = s.adminUntil.Sub(s.now()).Round(time.Minute).String() + " left"
	}
	csrf := s.csrf
	s.mu.Unlock()

	overview := s.overview
	// The credential list is the one part of the overview that this run can change after it started (see
	// credential.go): it is rebuilt from the configuration file, freshly read, every time this page is
	// shown, so a credential just added or changed is visible in the same run without restarting it. Every
	// other section keeps the one-time snapshot New was built with, exactly as the package comment promises.
	if rows, err := s.credentialRows(); err == nil {
		overview.Credentials = rows
	}
	// The connection list is the other part of the overview this run can change after it started (see
	// connection.go): rebuilt fresh for the same reason the credential list is.
	if rows, err := s.connectionRows(); err == nil {
		overview.Connections = rows
	}

	data := pageData{
		Overview:           overview,
		AdminActive:        active,
		AdminUnprotected:   unprotected,
		AdminRemainingText: remaining,
		AdminError:         adminError,
		CSRF:               csrf,
		CredentialsUsable:  s.store != nil && s.secrets != nil,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		// The overview was already fixed at startup, so a template failure is a programming error; nothing
		// of it, or of the partially written body, ever reaches the response as a detail.
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// overviewTemplate renders the secretfree overview, together with the admin status and its masked
// passphrase form, with no external asset, no inline style, and no script, so the content security policy
// this server sends is never a compromise; the form's own submission is the one thing form-action 'self'
// loosens.
var overviewTemplate = strings.TrimSpace(`
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web</title></head>
<body>
<h1>qatlas</h1>

<h2>Admin</h2>
{{if .AdminUnprotected}}
<p>This vault holds no passphrase: every coupled browser may manage without one.</p>
{{else if .AdminActive}}
<p>Admin approval active{{if .AdminRemainingText}} ({{.AdminRemainingText}}){{end}}.</p>
{{else}}
<p>Admin approval not active.</p>
{{if .AdminError}}<p>{{.AdminError}}</p>{{end}}
<form method="post" action="/admin">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label>Vault passphrase <input type="password" name="passphrase" autocomplete="off"></label>
<button type="submit">Approve</button>
</form>
{{end}}

<h2>Providers</h2>
<table border="1" cellpadding="4">
<tr><th>Provider</th><th>Description</th><th>Note</th><th>Tools</th><th>Connections</th><th>Configured</th></tr>
{{range .Providers}}<tr><td>{{.Provider}}</td><td>{{.Description}}</td><td>{{.Note}}</td><td>{{.Tools}}</td><td>{{.Connections}}</td><td>{{.Configured}}</td></tr>
{{end}}
</table>

<h2>Services</h2>
<table border="1" cellpadding="4">
<tr><th>Name</th><th>Provider</th><th>Base URL</th></tr>
{{range .Services}}<tr><td>{{.Name}}</td><td>{{.Provider}}</td><td>{{.BaseURL}}</td></tr>
{{end}}
</table>

<h2>Credentials</h2>
{{if .CredentialsUsable}}<p><a href="/credentials/new">Add a credential</a> · <a href="/credentials/new/payload">Add a payload credential</a></p>{{end}}
<table border="1" cellpadding="4">
<tr><th>Name</th><th>Provider</th><th>Type</th></tr>
{{range .Credentials}}<tr><td>{{if $.CredentialsUsable}}<a href="/credentials/{{.Name}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</td><td>{{.Provider}}</td><td>{{.Type}}</td></tr>
{{end}}
</table>

<h2>Connections</h2>
{{if .CredentialsUsable}}<p><a href="/connections/new">Set up a connection</a></p>{{end}}
<table border="1" cellpadding="4">
<tr><th>Name</th><th>Provider</th><th>Description</th><th>Permissions</th><th>Tools</th></tr>
{{range .Connections}}<tr><td>{{.Name}}</td><td>{{.Provider}}</td><td>{{.Description}}</td><td>{{.Permissions}}</td><td>{{.Tools}}</td></tr>
{{end}}
</table>
</body>
</html>
`)
