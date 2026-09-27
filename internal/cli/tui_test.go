package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// 'qatlas tui' must never let its resolver ask this process's own terminal for the vault passphrase:
// bubbletea holds that terminal in raw mode for its own screen, so a term.ReadPassword prompt there would
// corrupt it instead of being seen, and typed input would race between the prompt and the editor. This is
// checked behaviourally, never by inspecting a private field: the ask this test attaches would fail it at
// once if tuiSecrets ever let it through.
func TestTUISecretsNeverAsksForTheVaultPassphrase(t *testing.T) {
	dir := t.TempDir()

	// One vault, encrypted with a real passphrase and holding one entry, built with a resolver of its own
	// so the one under test below never unlocks it itself before the test does.
	setup := secret.NewWith(nil, nil, nil, nil).WithVault(vault.New(dir), nil)
	if err := setup.SetVault("reader", "role", "canary-value",
		func(string) (string, error) { return "hunter2", nil }); err != nil {
		t.Fatalf("SetVault() = %v", err)
	}

	asked := false
	failIfAsked := func(string) (string, error) {
		asked = true
		return "", errors.New("the vault passphrase was asked on this process's terminal")
	}
	// A fresh vault.Vault over the same directory starts locked in this process, the way 'qatlas tui'
	// would find one a previous run encrypted. Its ask is one that fails the test the moment it runs, so a
	// regression that lets tuiSecrets keep it shows up here instead of hanging a real terminal.
	base := secret.NewWith(nil, nil, nil, nil).WithVault(vault.New(dir), failIfAsked)
	opts := &Options{Secrets: base}

	secrets, err := tuiSecrets(opts)
	if err != nil {
		t.Fatalf("tuiSecrets() = %v", err)
	}

	if err := secrets.DeleteVault("reader", "role"); !isVaultLockedError(err) {
		t.Errorf("DeleteVault() = %v, want a VaultLockedError since the vault was never unlocked", err)
	}
	if _, err := secrets.Resolve(context.Background(), "reader",
		config.Credential{Type: config.CredentialTypeVault}, "role"); !isVaultLockedError(err) {
		t.Errorf("Resolve() = %v, want a VaultLockedError", err)
	}
	if asked {
		t.Fatal("the vault passphrase was asked on this process's terminal")
	}

	// A second call to opts.resolver(), the way connectionTester reaches it from inside the editor's own
	// test command, returns the very same instance: it stays free of the ask too.
	again, err := opts.resolver()
	if err != nil {
		t.Fatalf("resolver() = %v", err)
	}
	if _, err := again.Resolve(context.Background(), "reader",
		config.Credential{Type: config.CredentialTypeVault}, "role"); !isVaultLockedError(err) {
		t.Errorf("Resolve() via opts.resolver() = %v, want a VaultLockedError", err)
	}
	if asked {
		t.Fatal("the vault passphrase was asked on this process's terminal via opts.resolver()")
	}
}

// A resolver with no vault at all is untouched by tuiSecrets, and it never claims a lock error where there
// is no vault to be locked.
func TestTUISecretsWithoutAVault(t *testing.T) {
	opts := &Options{Secrets: secret.NewWith(nil, nil, nil, nil)}
	secrets, err := tuiSecrets(opts)
	if err != nil {
		t.Fatalf("tuiSecrets() = %v", err)
	}
	if secrets.Vault() != nil {
		t.Errorf("Vault() = %v, want none", secrets.Vault())
	}
}

func isVaultLockedError(err error) bool {
	var locked *secret.VaultLockedError
	return errors.As(err, &locked)
}
