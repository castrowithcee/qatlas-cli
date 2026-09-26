//go:build linux

package secret

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

const processPassphrase = "synthetic-passphrase-41c7"

// processFixture builds an encrypted vault holding canaryVault and a resolver over it that asks the vault
// process, with the socket in a runtime directory of the test's own. The resolver's vault stays locked; ask
// fails the test when it is consulted at all.
func processFixture(t *testing.T) (*Resolver, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := t.TempDir()
	offer := func(string) (string, error) { return processPassphrase, nil }
	if err := vault.New(dir).Set(credName, role, canaryVault, offer); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	ask := func(string) (string, error) {
		t.Errorf("the passphrase was asked for")
		return "", vault.ErrNoTerminal
	}
	r := NewWith(func(string) string { return "" }, nil, nil, &redact.Redactor{}).WithVault(vault.New(dir), ask)
	r.process = true
	return r, dir
}

// startProcess serves the vault in dir from this test process, the way 'qatlas vault unlock' hands it to a
// vault process. key replaces the vault's own key when it is not nil.
func startProcess(t *testing.T, dir string, key age.Identity) *vaultproc.Server {
	t.Helper()
	v := vault.New(dir)
	if _, err := v.Unlock(processPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if key == nil {
		if key, err = age.ParseX25519Identity(snap.Identity); err != nil {
			t.Fatalf("ParseX25519Identity() = %v", err)
		}
	}
	path, err := vaultproc.SocketPath(v.Dir())
	if err != nil {
		t.Fatalf("SocketPath() = %v", err)
	}
	l, err := vaultproc.Listen(path)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	s := vaultproc.NewServer(key, snap.Secrets)
	done := make(chan struct{})
	go func() { _ = s.Serve(l); close(done) }()
	t.Cleanup(func() { _ = s.Close(); <-done })
	return s
}

// The vault process delivers what a locked vault holds, to a resolver built before it started and one that
// never asks for a passphrase; once it locks, the vault is locked again.
func TestResolveFromTheVaultProcess(t *testing.T) {
	for _, unattended := range []bool{true, false} {
		r, dir := processFixture(t)
		if unattended {
			r.Unattended()
		} else {
			// Attended, a locked vault without a process asks; that path is not what this test is about.
			r.passphrase = func(string) (string, error) { return "", vault.ErrNoTerminal }
		}
		ctx := context.Background()

		var locked *VaultLockedError
		if _, err := r.Resolve(ctx, credName, vaultCred(), role); !errors.As(err, &locked) {
			t.Fatalf("Resolve() without a vault process = %v, want *VaultLockedError", err)
		}
		if !r.VaultLocked(ctx) {
			t.Errorf("VaultLocked() without a vault process = false")
		}

		s := startProcess(t, dir, nil)
		got, err := r.Resolve(ctx, credName, vaultCred(), role)
		if err != nil || got.Source != SourceVault || got.Secret != canaryVault {
			t.Fatalf("Resolve() from the vault process = %+v, %v, want the vault to deliver", got, err)
		}
		if r.VaultLocked(ctx) {
			t.Errorf("VaultLocked() with a vault process = true")
		}
		if _, err := r.Resolve(ctx, credName, vaultCred(), "other"); !errors.As(err, new(*MissingSecretError)) {
			t.Errorf("Resolve() of a role the process does not hold = %v, want *MissingSecretError", err)
		}
		if state, _ := r.vault.State(); state != vault.StateLocked {
			t.Errorf("the resolver's vault is %s, want it still locked in this process", state)
		}

		_ = s.Close()
		if _, err := r.Resolve(ctx, credName, vaultCred(), role); !errors.As(err, &locked) {
			t.Fatalf("Resolve() after the process locked = %v, want *VaultLockedError", err)
		}
	}
}

// A process at the vault's socket that cannot prove it holds this vault's key is an error of its own. It
// is never answered by asking for the passphrase, and it is told nothing, the credential name included.
func TestResolveRefusesAVaultProcessWithAnotherKey(t *testing.T) {
	r, dir := processFixture(t)
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	startProcess(t, dir, other)

	_, err = r.Resolve(context.Background(), credName, vaultCred(), role)
	var process *VaultProcessError
	var peer *vaultproc.PeerError
	if !errors.As(err, &process) || !errors.Is(err, vaultproc.ErrRefused) || !errors.As(err, &peer) {
		t.Fatalf("Resolve() = %v, want *VaultProcessError refusing the process", err)
	}
	if !strings.Contains(err.Error(), "kill ") || strings.Contains(err.Error(), canaryVault) {
		t.Errorf("Resolve() error = %q, want how to end the process and no secret", err)
	}
	if !r.VaultLocked(context.Background()) {
		t.Errorf("VaultLocked() with a process that fails the check = false")
	}
}

// A request that has already run out of time asks no vault process and no terminal.
func TestResolveFromTheVaultProcessEndsWithTheRequest(t *testing.T) {
	r, dir := processFixture(t)
	startProcess(t, dir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Resolve(ctx, credName, vaultCred(), role)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() with an ended request = %v, want context.Canceled", err)
	}
}
