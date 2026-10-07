package manage

import (
	"errors"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// ErrWrongPassphrase is the error VerifyAdmin returns for a passphrase that does not match the vault's. It
// is the vault's own error, so callers can test it without importing the vault package.
var ErrWrongPassphrase = vault.ErrWrongPassphrase

var (
	errNoAdminVault      = errors.New("no vault is configured for this run")
	errNoAdminPassphrase = errors.New("this vault holds no passphrase to approve")
)

// AdminRequired reports whether a managing action must be preceded by proof of the vault's passphrase. It is
// true only for a vault that is encrypted, whether it is locked in this process (locked) or already
// unlocked. No vault handle, a vault that does not exist yet and an unencrypted one hold no passphrase to
// prove, so nothing is required. The state is read fresh every time. When it cannot be read the check fails
// closed: required is true and the error is returned, so a caller never mistakes it for "no passphrase
// needed" and must refuse the action.
func AdminRequired(v *vault.Vault) (required, locked bool, err error) {
	if v == nil {
		return false, false, nil
	}
	state, err := v.State()
	if err != nil {
		return true, false, err
	}
	switch state {
	case vault.StateLocked:
		return true, true, nil
	case vault.StateUnlocked:
		return true, false, nil
	}
	return false, false, nil
}

// VerifyAdmin checks passphrase against the vault's live state: it unlocks a locked vault and verifies one
// that is already unlocked in this process, without any other side effect. It is the one passphrase rule
// every surface uses. A vault that is missing, absent or unencrypted has no passphrase to approve and is
// refused. The error never contains the passphrase.
func VerifyAdmin(v *vault.Vault, passphrase string) error {
	if v == nil {
		return errNoAdminVault
	}
	state, err := v.State()
	if err != nil {
		return err
	}
	switch state {
	case vault.StateLocked:
		_, err = v.Unlock(passphrase)
		return err
	case vault.StateUnlocked:
		return v.VerifyPassphrase(passphrase)
	}
	return errNoAdminPassphrase
}

// AdminSession is the proof, kept after a passphrase was verified, that a managing surface may keep acting.
// Its zero value is an inactive session on the real clock that keeps a deadline only. It is not safe for
// concurrent use; a surface that is must serialize access itself.
//
// The vault.admin_timeout is passed to each call rather than fixed, because a surface may re-read it. A
// timeout above zero keeps the session open until that long has passed without activity. A timeout of zero
// keeps no deadline: a session built with oneShot grants exactly one use (Consume), otherwise a grant is not
// active at all and the next action must ask again.
type AdminSession struct {
	oneShot bool
	now     func() time.Time
	until   time.Time
	once    bool
}

// NewAdminSession returns an inactive session. oneShot chooses what a grant with a timeout of zero means:
// true holds a single use until Consume, false keeps nothing. now is the clock, or nil for time.Now.
func NewAdminSession(oneShot bool, now func() time.Time) AdminSession {
	return AdminSession{oneShot: oneShot, now: now}
}

func (s *AdminSession) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *AdminSession) timed() bool { return !s.until.IsZero() && s.clock().Before(s.until) }

// Grant starts, or renews, the session after a passphrase just proved it.
func (s *AdminSession) Grant(timeout time.Duration) {
	if timeout <= 0 {
		s.once = s.oneShot
		s.until = time.Time{}
		return
	}
	s.once = false
	s.until = s.clock().Add(timeout)
}

// Active reports whether the session covers an action right now.
func (s *AdminSession) Active() bool { return s.once || s.timed() }

// Touch renews the idle deadline of a session that is active by deadline, and ends it when the timeout is
// zero. It never starts a session and never changes a pending single use.
func (s *AdminSession) Touch(timeout time.Duration) {
	if !s.timed() {
		return
	}
	if timeout <= 0 {
		s.until = time.Time{}
		return
	}
	s.until = s.clock().Add(timeout)
}

// Consume uses the session for one action. It reports whether the session covered it: a pending single use
// is spent, an active deadline is renewed, and anything else is refused.
func (s *AdminSession) Consume(timeout time.Duration) bool {
	if s.once {
		s.once = false
		return true
	}
	if !s.timed() {
		return false
	}
	s.Touch(timeout)
	return true
}

// End discards the session, whatever it held.
func (s *AdminSession) End() {
	s.once = false
	s.until = time.Time{}
}

// SingleUse reports whether the session holds an unspent single use.
func (s *AdminSession) SingleUse() bool { return s.once }

// Remaining reports how long the session stays active by deadline, or zero when it has none (also for a
// single use, which has no deadline).
func (s *AdminSession) Remaining() time.Duration {
	if !s.timed() {
		return 0
	}
	return s.until.Sub(s.clock())
}
