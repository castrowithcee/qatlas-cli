package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Tests here never open the real controlling terminal: doing that from 'go test' would hang whenever the
// test binary still has one, for example a developer's interactive shell. Where a test needs to simulate a
// terminal's answer, or its absence, it overrides the package-level readVaultPassphrase seam instead, and
// restores it on cleanup. The manual pseudo-terminal check this task's own instructions call for covers the
// real prompt end to end.

// withVaultPassphrase overrides readVaultPassphrase for the duration of a test.
func withVaultPassphrase(t *testing.T, ask vault.PassphraseFunc) {
	t.Helper()
	original := readVaultPassphrase
	readVaultPassphrase = ask
	t.Cleanup(func() { readVaultPassphrase = original })
}

const canaryVault = "canary-vault-token-8e5c31"

const vaultCredentialConfig = `
version: 1
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  wiki-vault:
    type: vault
connections:
  wiki:
    service: wiki
    credential: wiki-vault
defaults:
  connections:
    knowledge: wiki
`

func vaultCredentialFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(vaultCredentialConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func TestVaultStatusCLI(t *testing.T) {
	dir := vaultCredentialFixture(t)
	opts := &Options{}
	_, stdout, stderr := runWithInput(t, opts, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if stderr != "" {
		t.Fatalf("stderr = %q, want none", stderr)
	}
	if !strings.Contains(stdout, `"state":"absent"`) {
		t.Errorf("stdout = %q, want state absent", stdout)
	}
}

// 'credential set' of a second role never asks for a passphrase: only the vault's very first secret does,
// and this test pre-creates the vault unencrypted, bypassing the CLI, to keep the run free of any terminal
// interaction.
func TestCredentialSetVaultExistingUnencrypted(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", "already-there", nil); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	code, stdout, stderr := runWithInput(t, &Options{}, canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-secret", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want a silent success", stdout, stderr)
	}

	got, found, state, err := v.Get("wiki-vault", "token-secret", nil)
	if err != nil || !found || got != canaryVault {
		t.Fatalf("Get() = %q, %v, %v, %v, want %q, true, nil", got, found, err, state, canaryVault)
	}
	if state != vault.StateUnencrypted {
		t.Errorf("state = %v, want unencrypted", state)
	}

	_, stdout, stderr = runWithInput(t, &Options{}, "", "config", "validate", "--secrets",
		"--config", configIn(dir), "--output", "json")
	if stderr != "" {
		t.Fatalf("config validate --secrets stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `"source":"vault"`) {
		t.Errorf("config validate --secrets stdout = %q, want source vault", stdout)
	}
}

func TestCredentialDeleteVault(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	code, stdout, stderr := runWithInput(t, &Options{}, "",
		"credential", "delete", "wiki-vault", "token-id", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want a silent success", stdout, stderr)
	}
	if _, found, _, err := v.Get("wiki-vault", "token-id", nil); err != nil || found {
		t.Fatalf("Get() after delete = found %v, err %v, want false, nil", found, err)
	}

	code, _, stderr = runWithInput(t, &Options{}, "",
		"credential", "delete", "wiki-vault", "token-id", "--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, "no stored secret") {
		t.Errorf("second delete: code = %d, stderr = %q, want exitUsage and 'no stored secret'", code, stderr)
	}
}

// A vault unlock of anything but a locked vault reports its status instead of asking for a passphrase: a
// vault that does not exist yet needs no unlocking.
func TestVaultUnlockOfAnAbsentVault(t *testing.T) {
	dir := vaultCredentialFixture(t)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir), "--output", "json")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `"state":"absent"`) {
		t.Errorf("stdout = %q, want state absent", stdout)
	}
}

// --plaintext is a keyring-only flag; a vault credential is refused before anything is read from stdin.
func TestCredentialSetVaultRejectsPlaintextFlag(t *testing.T) {
	dir := vaultCredentialFixture(t)
	code, _, stderr := runWithInput(t, &Options{}, canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-id", "--plaintext", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--plaintext applies only to a keyring credential") {
		t.Errorf("stderr = %q", stderr)
	}
}

// A locked, encrypted vault that 'vault unlock' cannot ask a passphrase for ends with the same code and
// exit as any other locked access, never a bare, unclassified error.
func TestVaultUnlockWithoutTerminalIsVaultLocked(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, func(string) (string, error) { return "", vault.ErrNoTerminal })

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitRuntime {
		t.Fatalf("exit code = %d, want %d (stdout: %s, stderr: %s)", code, exitRuntime, stdout, stderr)
	}
	if !strings.Contains(stderr, "qatlas: vault-locked:") {
		t.Errorf("stderr = %q, want the vault-locked code", stderr)
	}
	if !strings.Contains(stderr, "run 'qatlas vault unlock' in a terminal, or press ctrl+l in 'qatlas tui'") {
		t.Errorf("stderr = %q, want the human next step", stderr)
	}
}

// With a passphrase source that can answer, 'vault unlock' opens the vault and merges what was queued
// while it was locked, all without touching a real terminal.
func TestVaultUnlockWithInjectedPassphraseMergesPending(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	// Queue a second role while the vault is locked, the way a fresh process would see it.
	locked := vault.New(dir)
	if err := locked.Set("wiki-vault", "token-secret", "second-"+canaryVault, nil); err != nil {
		t.Fatalf("locked vault Set() = %v", err)
	}

	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "merged 1 pending entry") {
		t.Errorf("stdout = %q, want the merge to be reported", stdout)
	}

	fresh := vault.New(dir)
	got, found, _, err := fresh.Get("wiki-vault", "token-secret", offeringPassphrase("s3cret-phrase"))
	if err != nil || !found || got != "second-"+canaryVault {
		t.Fatalf("Get() = %q, %v, %v, want %q, true, nil", got, found, err, "second-"+canaryVault)
	}
}

func offeringPassphrase(passphrase string) vault.PassphraseFunc {
	return func(string) (string, error) { return passphrase, nil }
}
