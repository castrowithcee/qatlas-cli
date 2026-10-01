//go:build linux || darwin

package secret

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/config"
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
	// t.TempDir() nests the full test name, which below the long per-user temporary directory of macOS
	// overflows a socket path; os.MkdirTemp keeps this one short.
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	dir := t.TempDir()
	offer := func(string) (string, error) { return processPassphrase, nil }
	v := vault.New(dir)
	if err := v.Set(credName, role, canaryVault, offer); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	approveWiki(t, v)
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
	s := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
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
		ctx := throughWiki()

		var locked *VaultLockedError
		if _, err := r.Resolve(ctx, credName, vaultCred(), role); !errors.As(err, &locked) {
			t.Fatalf("Resolve() without a vault process = %v, want *VaultLockedError", err)
		}
		if err := r.Usable(ctx, wikiConnection()); !errors.As(err, &locked) {
			t.Errorf("Usable() without a vault process = %v, want *VaultLockedError", err)
		}

		s := startProcess(t, dir, nil)
		got, err := r.Resolve(ctx, credName, vaultCred(), role)
		if err != nil || got.Source != SourceVault || got.Secret != canaryVault {
			t.Fatalf("Resolve() from the vault process = %+v, %v, want the vault to deliver", got, err)
		}
		if err := r.Usable(ctx, wikiConnection()); err != nil {
			t.Errorf("Usable() with a vault process = %v", err)
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

	_, err = r.Resolve(throughWiki(), credName, vaultCred(), role)
	var process *VaultProcessError
	var peer *vaultproc.PeerError
	if !errors.As(err, &process) || !errors.Is(err, vaultproc.ErrRefused) || !errors.As(err, &peer) {
		t.Fatalf("Resolve() = %v, want *VaultProcessError refusing the process", err)
	}
	if !strings.Contains(err.Error(), "kill ") || strings.Contains(err.Error(), canaryVault) {
		t.Errorf("Resolve() error = %q, want how to end the process and no secret", err)
	}
	// Listed as locked, the connection would send the user to unlock a vault that is unlocked already.
	if err := r.Usable(context.Background(), wikiConnection()); !errors.As(err, new(*VaultProcessError)) ||
		!errors.Is(err, vaultproc.ErrRefused) {
		t.Errorf("Usable() with a process that fails the check = %v, want *VaultProcessError for ErrRefused", err)
	}
}

// A request that has already run out of time asks no vault process and no terminal.
func TestResolveFromTheVaultProcessEndsWithTheRequest(t *testing.T) {
	r, dir := processFixture(t)
	startProcess(t, dir, nil)
	ctx, cancel := context.WithCancel(throughWiki())
	cancel()
	_, err := r.Resolve(ctx, credName, vaultCred(), role)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() with an ended request = %v, want context.Canceled", err)
	}
}

// The vault process hands a secret only to the connection the vault approved, as approved, the same way a
// vault unlocked in this process does; bindings handed over later take effect at once.
func TestVaultProcessNeedsApproval(t *testing.T) {
	r, dir := processFixture(t)
	r.Unattended()
	startProcess(t, dir, nil)
	ctx := context.Background()

	if got, err := r.Resolve(throughWiki(), credName, vaultCred(), role); err != nil || got.Secret != canaryVault {
		t.Fatalf("Resolve() through the approved connection = %+v, %v", got, err)
	}
	if err := r.Usable(ctx, wikiConnection()); err != nil {
		t.Errorf("Usable() of the approved connection = %v", err)
	}
	for name, change := range scopeChanges() {
		changed := wikiConnection()
		change(changed)
		_, err := r.Resolve(ForConnection(ctx, changed), credName, vaultCred(), role)
		var approval *ApprovalRequiredError
		if !errors.As(err, &approval) || approval.Connection != "wiki" || strings.Contains(err.Error(), canaryVault) {
			t.Errorf("Resolve() with a changed %s = %v, want *ApprovalRequiredError without the secret", name, err)
		}
		if err := r.Usable(ctx, changed); !errors.As(err, &approval) {
			t.Errorf("Usable() with a changed %s = %v, want *ApprovalRequiredError", name, err)
		}
	}
	if _, err := r.Resolve(ctx, credName, vaultCred(), role); !errors.As(err, new(*ApprovalRequiredError)) {
		t.Errorf("Resolve() without a connection = %v, want *ApprovalRequiredError", err)
	}

	// Approving the changed connection and handing the bindings over lets it through at once.
	changed := wikiConnection()
	changed.Permissions = append(changed.Permissions, config.PermissionCreate)
	v := vault.New(dir)
	if _, err := v.Unlock(processPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	if err := v.Approve([]vault.Scope{ScopeOf(changed)}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	bindings, err := v.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.processClient().Bind(ctx, bindings); err != nil {
		t.Fatalf("Bind() = %v", err)
	}
	if got, err := r.Resolve(ForConnection(ctx, changed), credName, vaultCred(), role); err != nil || got.Secret != canaryVault {
		t.Fatalf("Resolve() after the approval = %+v, %v", got, err)
	}
	if _, err := r.Resolve(throughWiki(), credName, vaultCred(), role); !errors.As(err, new(*ApprovalRequiredError)) {
		t.Errorf("Resolve() through the scope approved before = %v, want *ApprovalRequiredError", err)
	}
}
