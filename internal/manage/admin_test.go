package manage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const adminPassphrase = "synthetic-admin-passphrase-5d1b"

// adminVault creates an encrypted vault in dir and returns a handle that is unlocked in this process and a
// second one that is locked.
func adminVault(t *testing.T) (unlocked, locked *vault.Vault) {
	t.Helper()
	dir := t.TempDir()
	unlocked = vault.New(dir)
	if err := unlocked.Set("c", "token", "synthetic", func(string) (string, error) { return adminPassphrase, nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	return unlocked, vault.New(dir)
}

func TestAdminRequiredByVaultState(t *testing.T) {
	unlocked, locked := adminVault(t)
	plain := vault.New(t.TempDir())
	if err := plain.Set("c", "token", "synthetic", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}

	for _, tc := range []struct {
		name             string
		v                *vault.Vault
		required, locked bool
	}{
		{"no vault", nil, false, false},
		{"absent", vault.New(t.TempDir()), false, false},
		{"unencrypted", plain, false, false},
		{"unlocked", unlocked, true, false},
		{"locked", locked, true, true},
	} {
		required, isLocked, err := AdminRequired(tc.v)
		if err != nil || required != tc.required || isLocked != tc.locked {
			t.Errorf("%s: AdminRequired() = %v, %v, %v, want %v, %v, nil", tc.name, required, isLocked, err,
				tc.required, tc.locked)
		}
	}
}

// An unreadable vault state is reported as required together with the error, never as "nothing to prove".
func TestAdminRequiredFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a path below a regular file is not reported as an error on windows")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(file, "vault"))
	required, _, err := AdminRequired(v)
	if err == nil || !required {
		t.Fatalf("AdminRequired() = %v, _, %v, want required with an error", required, err)
	}
	if err := VerifyAdmin(v, adminPassphrase); err == nil {
		t.Fatal("VerifyAdmin() = nil with an unreadable vault state")
	}
}

func TestVerifyAdmin(t *testing.T) {
	unlocked, locked := adminVault(t)

	for name, v := range map[string]*vault.Vault{"unlocked": unlocked, "locked": locked} {
		err := VerifyAdmin(v, "wrong-"+adminPassphrase)
		if !errors.Is(err, ErrWrongPassphrase) {
			t.Fatalf("%s: wrong passphrase = %v, want ErrWrongPassphrase", name, err)
		}
		if err != nil && strings.Contains(err.Error(), adminPassphrase) {
			t.Fatalf("%s: the error contains the passphrase", name)
		}
	}
	if state, _ := locked.State(); state != vault.StateLocked {
		t.Fatalf("a wrong passphrase left the vault in state %v, want locked", state)
	}
	if err := VerifyAdmin(unlocked, adminPassphrase); err != nil {
		t.Fatalf("VerifyAdmin(unlocked) = %v", err)
	}
	if err := VerifyAdmin(locked, adminPassphrase); err != nil {
		t.Fatalf("VerifyAdmin(locked) = %v", err)
	}
	if state, _ := locked.State(); state != vault.StateUnlocked {
		t.Fatalf("a right passphrase left the vault in state %v, want unlocked", state)
	}

	if err := VerifyAdmin(nil, adminPassphrase); err == nil {
		t.Fatal("VerifyAdmin(nil) = nil")
	}
	if err := VerifyAdmin(vault.New(t.TempDir()), adminPassphrase); err == nil {
		t.Fatal("VerifyAdmin(absent vault) = nil, want a refusal: there is no passphrase to approve")
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// With a timeout of zero a session that is not one-shot keeps nothing, so the next action asks again; a
// one-shot session holds exactly one use until the next Consume, however long it idles.
func TestAdminSessionTimeoutZero(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}

	each := NewAdminSession(false, clock.now)
	each.Grant(0)
	if each.Active() || each.Consume(0) || each.SingleUse() {
		t.Fatal("a per-action session is active after a grant with a timeout of zero")
	}

	once := NewAdminSession(true, clock.now)
	if once.Active() || once.Consume(0) {
		t.Fatal("a fresh session is active")
	}
	once.Grant(0)
	clock.t = clock.t.Add(24 * time.Hour)
	once.Touch(0)
	if !once.Active() || !once.SingleUse() {
		t.Fatal("the single use did not survive idling")
	}
	if once.Remaining() != 0 {
		t.Fatalf("Remaining() = %v for a single use, want 0", once.Remaining())
	}
	if !once.Consume(0) {
		t.Fatal("the single use was refused")
	}
	if once.Active() || once.Consume(0) || once.SingleUse() {
		t.Fatal("the single use covered a second action")
	}
}

func TestAdminSessionTimeout(t *testing.T) {
	const timeout = 10 * time.Minute
	clock := &fakeClock{t: time.Unix(1000, 0)}
	s := NewAdminSession(true, clock.now)

	s.Touch(timeout)
	if s.Active() {
		t.Fatal("Touch started a session")
	}
	s.Grant(timeout)
	if !s.Active() || s.SingleUse() || s.Remaining() != timeout {
		t.Fatalf("after Grant: active %v, single use %v, remaining %v", s.Active(), s.SingleUse(), s.Remaining())
	}

	clock.t = clock.t.Add(9 * time.Minute)
	s.Touch(timeout)
	if s.Remaining() != timeout {
		t.Fatalf("Remaining() = %v after Touch, want %v", s.Remaining(), timeout)
	}
	clock.t = clock.t.Add(9 * time.Minute)
	if !s.Consume(timeout) || s.Remaining() != timeout {
		t.Fatalf("Consume() did not cover an action and renew: remaining %v", s.Remaining())
	}
	if !s.Active() {
		t.Fatal("Consume ended a timed session")
	}

	clock.t = clock.t.Add(timeout)
	if s.Active() || s.Consume(timeout) || s.Remaining() != 0 {
		t.Fatal("the session stays active past its deadline")
	}
	s.Touch(timeout)
	if s.Active() {
		t.Fatal("Touch revived an expired session")
	}

	s.Grant(timeout)
	s.End()
	if s.Active() {
		t.Fatal("End left the session active")
	}

	// A timeout lowered to zero while a session is open ends it at the next activity.
	s.Grant(timeout)
	s.Touch(0)
	if s.Active() {
		t.Fatal("Touch(0) left a timed session active")
	}
}

// The zero value is an inactive session on the real clock.
func TestAdminSessionZeroValue(t *testing.T) {
	var s AdminSession
	if s.Active() {
		t.Fatal("the zero value is active")
	}
	s.Grant(time.Minute)
	if !s.Active() || s.Remaining() <= 0 {
		t.Fatal("a grant on the zero value did not start a session")
	}
}
