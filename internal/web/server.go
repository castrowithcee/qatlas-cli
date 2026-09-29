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
	"strings"
	"sync"
	"time"
)

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

	overview Overview
}

// New starts listening on 127.0.0.1:0 (an OS-assigned port on the IPv4 loopback interface only) and
// returns a server ready to run. overview is shown to the browser that redeems the printed URL's token.
func New(overview Overview) (*Server, error) {
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
	s := &Server{
		listener: listener,
		addr:     listener.Addr().String(),
		tmpl:     tmpl,
		now:      time.Now,
		token:    token,
		overview: overview,
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
// Close is never coupled and never sees the overview again.
func (s *Server) close() {
	_ = s.http.Close()
	s.mu.Lock()
	s.tokenUsed = true
	s.sessionSet = false
	s.session = ""
	s.mu.Unlock()
}

const sessionCookieName = "qatlas_web_session"

func (s *Server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	return s.withSecurityHeaders(s.withLocalBoundary(mux))
}

// withSecurityHeaders adds the headers every response of this server carries, whatever it answers:
// nothing is ever cached, no referrer ever leaves this response, and a strict content security policy
// keeps a browser from loading anything but the document itself, since the page ships no external asset
// and no script.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'none'; script-src 'none'; img-src 'none'; "+
				"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// withLocalBoundary refuses a request that does not name this run's own loopback address as its Host, and
// one whose Origin, when it sends one at all, names anything else. Neither check ever explains itself
// beyond "forbidden": the reason is never handed to a caller that failed either check.
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
		s.renderOverview(w)
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
	s.tokenUsed = true
	s.session = newSession
	s.sessionSet = true
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

func (s *Server) renderOverview(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, s.overview); err != nil {
		// The overview was already fixed at startup, so a template failure is a programming error; nothing
		// of it, or of the partially written body, ever reaches the response as a detail.
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// overviewTemplate renders the secretfree overview with no external asset, no inline style, and no
// script, so the strict content security policy this server sends is never a compromise.
var overviewTemplate = strings.TrimSpace(`
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web</title></head>
<body>
<h1>qatlas</h1>

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
<table border="1" cellpadding="4">
<tr><th>Name</th><th>Provider</th><th>Type</th></tr>
{{range .Credentials}}<tr><td>{{.Name}}</td><td>{{.Provider}}</td><td>{{.Type}}</td></tr>
{{end}}
</table>

<h2>Connections</h2>
<table border="1" cellpadding="4">
<tr><th>Name</th><th>Provider</th><th>Description</th><th>Permissions</th><th>Tools</th></tr>
{{range .Connections}}<tr><td>{{.Name}}</td><td>{{.Provider}}</td><td>{{.Description}}</td><td>{{.Permissions}}</td><td>{{.Tools}}</td></tr>
{{end}}
</table>
</body>
</html>
`)
