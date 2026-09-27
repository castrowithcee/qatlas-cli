//go:build linux

// This editor hands every vault write, removal, and rekey on to a vault process that holds the vault
// unlocked outside this run, exactly the way the CLI already does (see syncVaultProcess and
// lockVaultProcessForRekey in vaultsettings.go); these tests exercise that against a real vaultproc.Server,
// the way internal/cli/vaultsync_linux_test.go already does for the CLI, so the process seam behind
// vaultmigrate.SyncChange and vaultmigrate.LockProcess is proven, not just its callers' plumbing.
package tui

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// serveVaultProcess starts a vault process serving the already-encrypted vault at dir, unlocked with
// passphrase, exactly the way a detached 'qatlas vault unlock' would; it mirrors the CLI's own
// serveVaultInProcess (internal/cli/vaultsync_linux_test.go). The server checks its clients by their
// program, which is this very test binary on both ends.
func serveVaultProcess(t *testing.T, dir, passphrase string) (*vaultproc.Server, *vaultproc.Client) {
	t.Helper()
	// t.TempDir() nests the full (sub)test name, long enough with these names to overflow a socket path;
	// os.MkdirTemp keeps this one short, the way the socket itself needs.
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	v := vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatalf("ParseX25519Identity() = %v", err)
	}
	client, err := vaultmigrate.ProcessClientOf(v)
	if err != nil {
		t.Fatalf("ProcessClientOf() = %v", err)
	}
	l, err := vaultproc.Listen(client.Path)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	server := vaultproc.NewServer(key, snap.Secrets)
	done := make(chan struct{})
	go func() { _ = server.Serve(l); close(done) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	return server, client
}

// refuseEveryClient makes a server refuse every client, as a vault process does once an update replaced its
// program; it mirrors the CLI's own helper of the same name.
func refuseEveryClient(net.Conn) error {
	return fmt.Errorf("%w: its program was removed or replaced since it started", vaultproc.ErrRefused)
}

// s on a vault credential's role hands what it just stored on to a running vault process, and x hands over
// the removal too, the same way 'qatlas credential set' and 'qatlas credential delete' already do.
func TestVaultRoleWriteAndRemoveSyncTheVaultProcess(t *testing.T) {
	const passphrase, canary = "hunter2", "canary-vault-role-sync-7c2e"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	// The vault's very first secret encrypts it, so the process below has a key to check clients with.
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", func(string) (string, error) { return passphrase, nil }))
	_, client := serveVaultProcess(t, dir, passphrase)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "enter")
	focusRole(t, m, "token-id")

	press(t, m, "s")
	typeText(t, m, canary)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("storing in the vault reported %q", m.fail)
	}
	ctx := context.Background()
	if got, found, err := client.Get(ctx, "reader", "token-id"); err != nil || !found || got != canary {
		t.Fatalf("process Get() after s = %q, %v, %v, want the value just stored", got, found, err)
	}

	press(t, m, "x")
	if m.screen != screenConfirm {
		t.Fatalf("x did not ask to confirm: screen %v", m.screen)
	}
	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("removing from the vault reported %q", m.fail)
	}
	if _, found, err := client.Get(ctx, "reader", "token-id"); err != nil || found {
		t.Fatalf("process Get() after x = %v, %v, want it removed from the process too", found, err)
	}
}

// Without a running vault process, storing or removing a vault secret needs nothing said: the platform
// keeps working exactly as it did before this editor started telling a process about its writes.
func TestVaultRoleWriteIsSilentWithoutARunningVaultProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", nil))

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "enter")
	focusRole(t, m, "token-id")

	press(t, m, "s")
	typeText(t, m, "canary-no-process-9a1b")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("storing in the vault reported %q", m.fail)
	}
	if strings.Contains(m.status, "warning") {
		t.Errorf("status = %q, want no warning without a vault process to tell", m.status)
	}
}

// A vault process that refuses this program, the way one does once an update replaced it, keeps what it
// held, and the editor says so as a warning next to the write's own success, never on a terminal and never
// naming the secret.
func TestVaultRoleWriteWarnsWhenTheVaultProcessRefuses(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", func(string) (string, error) { return passphrase, nil }))
	server, _ := serveVaultProcess(t, dir, passphrase)
	server.Verify = refuseEveryClient

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "enter")
	focusRole(t, m, "token-id")

	const canary = "canary-refused-3d4e"
	press(t, m, "s")
	typeText(t, m, canary)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("storing in the vault reported %q, want the write itself to still succeed", m.fail)
	}
	if !strings.Contains(m.status, "warning: the vault holds the change") {
		t.Errorf("status = %q, want a warning naming the process", m.status)
	}
	if strings.Contains(m.status, canary) {
		t.Errorf("status = %q, the stored secret must never appear in it", m.status)
	}
}

// The guided setup's own save hands a new vault credential's secrets on to a running vault process too,
// through commitSetup, the same syncVaultProcess helper the role rows use.
func TestCommitSetupSyncsNewVaultSecretsToARunningProcess(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", func(string) (string, error) { return passphrase, nil }))
	_, client := serveVaultProcess(t, dir, passphrase)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))

	plan := setupPlan{
		credential: "reader", storage: storageVault, roles: []string{"token-id", "token-secret"},
		secrets: map[string]string{"token-id": "canary-setup-id-1a2b", "token-secret": "canary-setup-secret-3c4d"},
	}
	warning, err := commitSetup(store, secrets, cfg, plan, nil)
	if err != nil {
		t.Fatalf("commitSetup() = %v", err)
	}
	if warning != "" {
		t.Fatalf("commitSetup() warning = %q, want none with the process reachable", warning)
	}

	ctx := context.Background()
	for role, want := range plan.secrets {
		if got, found, err := client.Get(ctx, "reader", role); err != nil || !found || got != want {
			t.Errorf("process Get(%q) = %q, %v, %v, want %q", role, got, found, err, want)
		}
	}
}

// Changing the passphrase or turning encryption off locks a running vault process first, since it would
// otherwise keep answering with an identity or a state the vault no longer has, and the editor reports it
// next to the action's own result, the same short way for both.
func TestVaultRekeyLocksTheVaultProcess(t *testing.T) {
	const passphrase = "hunter2"
	for _, tt := range []struct {
		name, role, newPassphrase string
	}{
		{"change passphrase", "change passphrase", "a-whole-new-passphrase"},
		{"turn off encryption", "turn off encryption", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			path := filepath.Join(dir, "config.yaml")
			store := newTestStore(t, path)
			setup, _ := newVaultResolver(t, dir)
			mustNoError(t, setup.SetVault("reader", "token-id", "canary-rekey-9c3d",
				func(string) (string, error) { return passphrase, nil }))
			locked, _ := newVaultResolver(t, dir)
			_, client := serveVaultProcess(t, dir, passphrase)

			m, err := New(store, nil, locked, nil)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			openVaultSection(t, m)
			focusRole(t, m, tt.role)
			press(t, m, "enter")
			typeText(t, m, passphrase)
			pump(t, m, "enter")

			if tt.role == "change passphrase" {
				typeText(t, m, tt.newPassphrase)
				pump(t, m, "enter")
				typeText(t, m, tt.newPassphrase)
				pump(t, m, "enter")
			} else {
				if m.screen != screenConfirm {
					t.Fatalf("the current passphrase did not lead to the confirmation: screen %v", m.screen)
				}
				pump(t, m, "y")
			}

			if m.fail != "" {
				t.Fatalf("%s reported %q", tt.name, m.fail)
			}
			if !strings.Contains(m.status, "the vault process was locked") {
				t.Errorf("status = %q, want the vault process reported locked", m.status)
			}
			if _, err := client.Status(context.Background()); err == nil {
				t.Error("the vault process still answers after the rekey, want it locked")
			}
		})
	}
}

// Without a running vault process there is nothing to lock, and nothing is said about one; a rekey behaves
// exactly as it did before this editor started locking a process ahead of it.
func TestVaultRekeyIsSilentWithoutARunningVaultProcess(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-no-process-4e5f",
		func(string) (string, error) { return passphrase, nil }))
	locked, _ := newVaultResolver(t, dir)

	m, err := New(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "change passphrase")
	press(t, m, "enter")
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	typeText(t, m, "another-new-passphrase")
	pump(t, m, "enter")
	typeText(t, m, "another-new-passphrase")
	pump(t, m, "enter")

	if m.fail != "" {
		t.Fatalf("changing the passphrase reported %q", m.fail)
	}
	if strings.Contains(m.status, "vault process") {
		t.Errorf("status = %q, want nothing said about a vault process that never ran", m.status)
	}
}
