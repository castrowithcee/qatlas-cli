package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Switching a stored vault credential to the system keyring or to env while the vault still holds a secret
// of it would orphan that secret the same way guardTypeChange already refuses for the keyring: the save is
// refused, naming the role and how to remove it first, and nothing about the configuration or the vault
// changes.
func TestVaultTypeChangeRefusedWhileASecretIsStored(t *testing.T) {
	for _, newStorage := range []string{storageKeyring, storageEnv} {
		t.Run(newStorage, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			path := filepath.Join(dir, "config.yaml")
			store := newTestStore(t, path)
			secrets, _ := newVaultResolver(t, dir)

			cfg := newTestConfig(t)
			mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
			mustNoError(t, store.Save(cfg))
			mustNoError(t, secrets.SetVault("reader", "token-secret", "canary-orphan-vault-9a1c", nil))

			m, err := New(store, nil, secrets, nil)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			editEntry(t, m, "reader")
			focusField(t, m, storageLabel)
			selectChoice(t, m, newStorage)
			pump(t, m, "f2")

			if !strings.Contains(m.fail, "token-secret") || !strings.Contains(m.fail, "still stored in the vault") {
				t.Fatalf("error = %q, want it to name the stored role", m.fail)
			}
			saved, err := loadTestConfig(t, path)
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			if got := saved.Credentials["reader"].Type; got != config.CredentialTypeVault {
				t.Errorf("the type changed to %q although a secret is stored in the vault", got)
			}
			got, found, _, err := vault.New(dir).Get("reader", "token-secret", nil)
			if err != nil || !found || got != "canary-orphan-vault-9a1c" {
				t.Errorf("the vault secret is gone: %q, %v, %v", got, found, err)
			}

			// Removing the secret first is what unblocks the change.
			focusField(t, m, storageLabel)
			selectChoice(t, m, storageVault)
			focusRole(t, m, "token-secret")
			press(t, m, "x")
			pump(t, m, "y")
			if m.fail != "" {
				t.Fatalf("removing the vault secret reported %q", m.fail)
			}
			focusField(t, m, storageLabel)
			selectChoice(t, m, newStorage)
			if newStorage == storageEnv {
				focusRole(t, m, "token-id")
				typeText(t, m, "WIKI_ID")
			}
			pump(t, m, "f2")
			if m.fail != "" {
				t.Fatalf("the type change still fails once the vault secret is removed: %q", m.fail)
			}
			saved, err = loadTestConfig(t, path)
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			wantType := config.CredentialTypeKeyring
			if newStorage == storageEnv {
				wantType = config.CredentialTypeEnv
			}
			if saved.Credentials["reader"].Type != wantType {
				t.Errorf("type = %q, want %q", saved.Credentials["reader"].Type, wantType)
			}
		})
	}
}

// A vault that is encrypted and locked cannot be checked for what it still holds, and this editor never
// asks its own terminal for the passphrase to do so: the save is refused with that explained, rather than
// silently guessing or leaving the secret orphaned.
func TestVaultTypeChangeRefusedWhileTheVaultIsLockedAndCannotBeChecked(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)

	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-locked-typechange-3f0a",
		func(string) (string, error) { return "hunter2", nil }))

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	// A fresh resolver over the same directory starts genuinely locked.
	locked, _ := newVaultResolver(t, dir)
	m, err := New(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	editEntry(t, m, "reader")
	focusField(t, m, storageLabel)
	selectChoice(t, m, storageKeyring)
	pump(t, m, "f2")

	if !strings.Contains(m.fail, "locked") || !strings.Contains(m.fail, "qatlas vault unlock") {
		t.Fatalf("error = %q, want the locked vault explained with its way out", m.fail)
	}
	saved, err := loadTestConfig(t, path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Credentials["reader"].Type; got != config.CredentialTypeVault {
		t.Errorf("the type changed to %q although the vault could not be checked", got)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want the vault untouched", state, err)
	}
}

// A vault credential may switch to another storage freely once the vault holds nothing of it: the vault's
// very first secret, still unwritten.
func TestVaultTypeChangeAllowedWithNothingStoredYet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	editEntry(t, m, "reader")
	focusField(t, m, storageLabel)
	selectChoice(t, m, storageKeyring)
	pump(t, m, "f2")

	if m.fail != "" {
		t.Fatalf("switching an empty vault credential's storage reported %q", m.fail)
	}
	saved, err := loadTestConfig(t, path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Credentials["reader"].Type; got != config.CredentialTypeKeyring {
		t.Errorf("type = %q, want keyring", got)
	}
}
