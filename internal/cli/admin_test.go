package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// TestMain runs every test of this package as if a person ran it from an interactive terminal: almost
// every test here exercises something other than the admin check itself, and a real terminal is not
// something 'go test' can be relied on to have, in a developer's shell or in CI alike. The handful of tests
// that exercise the admin check turn this back off for their own duration with withInteractive, the same
// seam a real run leaves at vault.Interactive.
func TestMain(m *testing.M) {
	checkInteractive = func() bool { return true }
	os.Exit(m.Run())
}

// withInteractive overrides checkInteractive for the duration of a test.
func withInteractive(t *testing.T, ok bool) {
	t.Helper()
	original := checkInteractive
	checkInteractive = func() bool { return ok }
	t.Cleanup(func() { checkInteractive = original })
}

// Every management command refuses to run at all without an interactive terminal, before it reads the
// secret from standard input, touches the credential store, or opens a vault file: 'credential set' and
// 'credential delete' work the same for a keyring credential as for a vault one, since the check is about
// who is running the command, not about the vault it manages, if any.
func TestManagementCommandsRequireATerminal(t *testing.T) {
	dir := keyringFixture(t)
	store := secret.NewMemoryStore()
	opts := testOptionsIn(t, dir, store)
	withInteractive(t, false)

	code, stdout, stderr := runWithInput(t, opts, canaryStored+"\n",
		"credential", "set", "vault-reader", "token-id", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("set: exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if stdout != "" {
		t.Errorf("set: stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "admin-required") {
		t.Errorf("set: stderr = %q, want the admin-required code", stderr)
	}
	if strings.Contains(stderr, canaryStored) {
		t.Errorf("set: stderr = %q, want the secret kept out", stderr)
	}
	if _, err := store.Get(context.Background(), secret.StoreKey("vault-reader", "token-id")); err == nil {
		t.Error("set: the store holds an entry, want nothing written")
	}
	if len(filesIn(t, dir)) != 1 {
		t.Errorf("set: files = %v, want only the configuration", filesIn(t, dir))
	}

	code, stdout, stderr = runWithInput(t, opts, "",
		"credential", "delete", "vault-reader", "token-id", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("delete: exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if stdout != "" {
		t.Errorf("delete: stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "admin-required") {
		t.Errorf("delete: stderr = %q, want the admin-required code", stderr)
	}
}

// The vault management commands refuse the same way, whatever state the vault is in: an absent vault needs
// no passphrase, but it still needs a person at a terminal to create one.
func TestVaultManagementCommandsRequireATerminal(t *testing.T) {
	dir := vaultCredentialFixture(t)
	withInteractive(t, false)

	for _, args := range [][]string{
		{"vault", "encrypt"},
		{"vault", "passphrase"},
		{"vault", "decrypt", "--confirm"},
		{"vault", "migrate"},
	} {
		full := append(append([]string{}, args...), "--config", configIn(dir))
		code, stdout, stderr := runWithInput(t, &Options{}, "", full...)
		if code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d (stderr: %s)", args, code, exitUsage, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q, want empty", args, stdout)
		}
		if !strings.Contains(stderr, "admin-required") {
			t.Errorf("%v: stderr = %q, want the admin-required code", args, stderr)
		}
	}
	if len(filesIn(t, dir)) != 1 {
		t.Errorf("files = %v, want only the configuration; no command may have had any effect", filesIn(t, dir))
	}
}

// With a terminal, an encrypted and locked vault asks for its passphrase before a management command has
// any effect, whatever credential the command targets: a wrong passphrase aborts before anything is
// written, and a right one lets the command through and leaves the vault unlocked, so the write that
// follows lands directly in it rather than as a pending entry.
func TestCredentialSetAsksTheVaultPassphraseEvenForAKeyringCredential(t *testing.T) {
	dir := keyringFixture(t)
	if err := vault.New(dir).Encrypt("correct-horse-battery-staple"); err != nil {
		t.Fatalf("Encrypt() = %v", err)
	}
	store := secret.NewMemoryStore()
	red := &redact.Redactor{}
	opts := &Options{Redactor: red, Secrets: secret.NewWith(os.Getenv, store,
		secret.NewFile(filepath.Join(dir, secret.FileName)), red).WithVault(vault.New(dir), vault.ReadPassphrase)}

	withInteractive(t, true)
	withVaultPassphrase(t, sequencedPassphrases("wrong-passphrase"))
	code, stdout, stderr := runWithInput(t, opts, canaryStored+"\n",
		"credential", "set", "vault-reader", "token-id", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("wrong passphrase: exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if stdout != "" {
		t.Errorf("wrong passphrase: stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "does not unlock") {
		t.Errorf("wrong passphrase: stderr = %q, want the vault to refuse it", stderr)
	}
	if _, err := store.Get(context.Background(), secret.StoreKey("vault-reader", "token-id")); err == nil {
		t.Error("wrong passphrase: the keyring holds an entry, want nothing written")
	}

	withVaultPassphrase(t, sequencedPassphrases("correct-horse-battery-staple"))
	code, stdout, stderr = runWithInput(t, opts, canaryStored+"\n",
		"credential", "set", "vault-reader", "token-id", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("right passphrase: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("right passphrase: stdout = %q, stderr = %q, want a silent success", stdout, stderr)
	}
	got, err := store.Get(context.Background(), secret.StoreKey("vault-reader", "token-id"))
	if err != nil || got != canaryStored {
		t.Errorf("right passphrase: store holds %q (%v), want the secret", got, err)
	}
}

// A vault credential written after the admin check unlocked an encrypted vault goes straight into it: no
// pending entry is left for a later unlock to merge, because the admin check already did that work.
func TestCredentialSetVaultWritesDirectlyAfterTheAdminCheckUnlocks(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", "already-there", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := v.Encrypt("correct-horse-battery-staple"); err != nil {
		t.Fatalf("Encrypt() = %v", err)
	}

	withInteractive(t, true)
	withVaultPassphrase(t, sequencedPassphrases("correct-horse-battery-staple"))
	code, stdout, stderr := runWithInput(t, &Options{}, canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-secret", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want a silent success", stdout, stderr)
	}

	if _, err := os.Stat(v.Dir() + "/pending"); err == nil {
		entries, readErr := os.ReadDir(v.Dir() + "/pending")
		if readErr == nil && len(entries) != 0 {
			t.Errorf("pending holds %d entries, want the write to have gone straight into the vault", len(entries))
		}
	}

	fresh := vault.New(dir)
	got, found, _, err := fresh.Get("wiki-vault", "token-secret", func(string) (string, error) {
		return "correct-horse-battery-staple", nil
	})
	if err != nil || !found || got != canaryVault {
		t.Fatalf("Get() = %q, %v, %v, want %q, true, nil", got, found, err, canaryVault)
	}
}

// 'vault passphrase' and 'vault decrypt' ask for the vault's current passphrase only once: their own
// prompt is the admin check's passphrase question, so requireAdminTerminal alone gates them, not the full
// requireAdmin a wrong-passphrase, double-prompt run would otherwise produce.
func TestVaultPassphraseAndDecryptAskThePassphraseOnlyOnce(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Encrypt("correct-horse-battery-staple"); err != nil {
		t.Fatalf("Encrypt() = %v", err)
	}
	withInteractive(t, true)

	asked := 0
	withVaultPassphrase(t, func(prompt string) (string, error) {
		asked++
		if asked > 1 {
			t.Fatalf("the passphrase was asked a second time")
		}
		return "correct-horse-battery-staple", nil
	})
	withVaultConfirm(t, confirming(true))

	code, _, stderr := runWithInput(t, &Options{}, "",
		"vault", "decrypt", "--confirm", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("decrypt: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
}
