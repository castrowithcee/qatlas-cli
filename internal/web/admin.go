package web

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// maxAdminFormBytes bounds every mutating request's body, well beyond what a CSRF value and a typed
// passphrase need, before it is ever parsed.
const maxAdminFormBytes = 4 << 10

// errNoCoupledSession reports that no browser is currently coupled to this run. The terminal admin path
// reports it instead of granting anything: an admin approval is proof for a specific coupled browser
// session, never for the process at large, so there is nothing yet for a terminal-typed passphrase to
// approve.
var errNoCoupledSession = errors.New("no browser is coupled to this run yet")

// adminRequiredLocked reports whether this run's vault holds a passphrase an admin approval must prove,
// reading the vault's live state fresh every time, exactly as internal/tui's own requireAdmin does. An
// unreadable state fails closed: it is reported as required, and the error is returned so the caller never
// treats it as if it had actually read "no passphrase needed". Callers must hold s.mu.
func (s *Server) adminRequiredLocked() (required bool, err error) {
	required, _, err = manage.AdminRequired(s.vault)
	return required, err
}

// SessionCoupled reports whether a browser is currently coupled to this run. The terminal admin path uses
// it to refuse a passphrase prompt before any session exists to approve.
func (s *Server) SessionCoupled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionSet
}

// AdminPassphraseRequired reports whether this run's vault holds a passphrase an admin approval must
// prove, reading the vault's live state fresh. It is the exported counterpart of adminRequiredLocked for
// callers that do not already hold s.mu, such as the terminal admin path and the printed hint line.
func (s *Server) AdminPassphraseRequired() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminRequiredLocked()
}

// adminActiveLocked reports whether this run's admin approval is active right now, for the one browser
// session this run ever couples: no coupled session, no approval, whatever the vault says. Callers must
// hold s.mu.
func (s *Server) adminActiveLocked() bool {
	if !s.sessionSet {
		return false
	}
	required, err := s.adminRequiredLocked()
	if err != nil {
		// Fail closed: an unreadable vault state is never treated as either proven or unnecessary.
		return false
	}
	if !required {
		return true
	}
	return s.admin.Active()
}

// grantAdminLocked starts, or renews, this run's admin approval once a passphrase just proved it.
// vault.admin_timeout of 0 grants exactly the next mutation instead of a session, which withAdminGuard
// spends the moment it lets a mutation through. Callers must hold s.mu.
func (s *Server) grantAdminLocked() { s.admin.Grant(s.adminTimeout) }

// touchAdminIfActive renews the admin approval's idle deadline on every request the coupled session makes,
// the same rule internal/tui's touchAdminSessionIfActive applies to every key press of an active window. It
// never starts an approval on its own; only a right passphrase, through VerifyAndGrantAdmin, does that.
func (s *Server) touchAdminIfActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admin.Touch(s.adminTimeout)
}

// VerifyAndGrantAdmin checks passphrase against this run's vault exactly the way internal/tui's own admin
// session does (Unlock when locked, VerifyPassphrase when already unlocked in this process, never a second,
// separate passphrase rule), and on success grants this run's admin approval to the one browser session
// this run has coupled. It is the shared path both the browser's masked form and this process's own
// terminal use, so the two never check a passphrase two different ways, and it never grants anything
// without a coupled session already in place, whatever the passphrase.
func (s *Server) VerifyAndGrantAdmin(passphrase string) error {
	s.mu.Lock()
	if !s.sessionSet {
		s.mu.Unlock()
		return errNoCoupledSession
	}
	v := s.vault
	s.mu.Unlock()

	if err := manage.VerifyAdmin(v, passphrase); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionSet {
		return errNoCoupledSession
	}
	s.grantAdminLocked()
	return nil
}

// validCSRF reports, in constant time, whether token is the coupled session's own CSRF value.
func (s *Server) validCSRF(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionSet || s.csrf == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.csrf)) == 1
}

// withSessionGuard is the baseline every mutating request of this server must pass before it may have any
// effect at all: withLocalBoundary already required an exact Host, and rejects a declared Origin that names
// anything else; this adds that a mutating request declares an Origin at all (a POST with none proves
// nothing about where it came from, so it is refused rather than treated as same-origin by default), comes
// from this run's one coupled browser session, and carries that session's own CSRF value as a form field.
// The request body is bounded and parsed here, before any handler reads it, so a request cannot exhaust
// memory before any of these checks even run.
func (s *Server) withSessionGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !s.validSession(cookie.Value) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAdminFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !s.validCSRF(r.PostFormValue("csrf")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// withAdminGuard wraps a handler that may touch config, keyring, or vault. It runs withSessionGuard first,
// then requires this run's admin approval to be active right now, and finally serializes the mutation
// behind writeMu so two concurrent writes from the same coupled session can never interleave. Every check
// fails before next ever runs, whatever this run's admin approval otherwise looks like: nothing here is
// merely advisory. No production route uses this yet (see internal/web's own package comment); it exists so
// a future mutating route only has to wrap itself with it.
func (s *Server) withAdminGuard(next http.HandlerFunc) http.HandlerFunc {
	return s.withSessionGuard(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		active := s.adminActiveLocked()
		if active {
			s.admin.Consume(s.adminTimeout)
		}
		s.mu.Unlock()
		if !active {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		next(w, r)
	})
}

// handleAdminAuth is the coupled browser's masked passphrase form: it checks the typed passphrase through
// VerifyAndGrantAdmin, the same check the terminal path uses, and on success redirects back to "/" so the
// passphrase never sits in the address bar, browser history, or a referrer. A wrong passphrase, or any
// other failure, shows the same single generic line: nothing about why it failed is ever detailed.
func (s *Server) handleAdminAuth(w http.ResponseWriter, r *http.Request) {
	passphrase := r.PostFormValue("passphrase")
	if err := s.VerifyAndGrantAdmin(passphrase); err != nil {
		s.renderOverview(w, "wrong passphrase")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
