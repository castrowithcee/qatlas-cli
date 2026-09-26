package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
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

// withVaultConfirm overrides readVaultConfirm for the duration of a test.
func withVaultConfirm(t *testing.T, confirm func(string) (bool, error)) {
	t.Helper()
	original := readVaultConfirm
	readVaultConfirm = confirm
	t.Cleanup(func() { readVaultConfirm = original })
}

// sequencedPassphrases answers each successive prompt with the next value in order, the way a real
// terminal answers a passphrase and then its confirmation with two different key presses.
func sequencedPassphrases(values ...string) vault.PassphraseFunc {
	i := 0
	return func(string) (string, error) {
		if i >= len(values) {
			return "", vault.ErrNoTerminal
		}
		v := values[i]
		i++
		return v, nil
	}
}

func confirming(answer bool) func(string) (bool, error) {
	return func(string) (bool, error) { return answer, nil }
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

// --plaintext no longer exists on 'credential set': the storage a credential uses is decided by its type
// alone, and any command line still naming the flag is refused as a syntax error, the same as any other
// unknown flag.
func TestCredentialSetHasNoPlaintextFlag(t *testing.T) {
	dir := vaultCredentialFixture(t)
	code, _, stderr := runWithInput(t, &Options{}, canaryVault+"\n",
		"credential", "set", "wiki-vault", "token-id", "--plaintext", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "unknown flag: --plaintext") {
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

// TestVaultEncryptCLI switches an unencrypted vault's very first secret to encrypted, typed twice, and
// proves a fresh process needs the passphrase to reach it afterwards.
func TestVaultEncryptCLI(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, sequencedPassphrases("s3cret-phrase", "s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "encrypt", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "the vault is encrypted") {
		t.Errorf("stdout = %q, want the outcome reported", stdout)
	}

	fresh := vault.New(dir)
	if state, err := fresh.State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want locked, nil", state, err)
	}
	got, found, _, err := fresh.Get("wiki-vault", "token-id", offeringPassphrase("s3cret-phrase"))
	if err != nil || !found || got != canaryVault {
		t.Fatalf("Get() = %q, %v, %v, want %q, true, nil", got, found, err, canaryVault)
	}
}

// A mismatched confirmation aborts without encrypting anything.
func TestVaultEncryptRefusesAMismatchedConfirmation(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, sequencedPassphrases("s3cret-phrase", "different-phrase"))
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "encrypt", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "did not match") {
		t.Errorf("stderr = %q, want the mismatch reported", stderr)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateUnencrypted {
		t.Fatalf("State() = %v, %v, want unencrypted, nil", state, err)
	}
}

// Encrypting an already encrypted vault is refused and names 'vault passphrase' instead.
func TestVaultEncryptRefusesAnAlreadyEncryptedVault(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, sequencedPassphrases("another-phrase", "another-phrase"))
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "encrypt", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "vault passphrase") {
		t.Errorf("stderr = %q, want 'vault passphrase' named", stderr)
	}
}

// TestVaultPassphraseCLI changes the passphrase of an encrypted vault, and the old one no longer opens it.
func TestVaultPassphraseCLI(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("old-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, sequencedPassphrases("old-phrase", "new-phrase", "new-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "passphrase", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "passphrase is changed") {
		t.Errorf("stdout = %q, want the outcome reported", stdout)
	}

	fresh := vault.New(dir)
	if _, _, _, err := fresh.Get("wiki-vault", "token-id", offeringPassphrase("old-phrase")); err == nil {
		t.Fatal("the old passphrase still opens the vault")
	}
	got, found, _, err := fresh.Get("wiki-vault", "token-id", offeringPassphrase("new-phrase"))
	if err != nil || !found || got != canaryVault {
		t.Fatalf("Get() = %q, %v, %v, want %q, true, nil", got, found, err, canaryVault)
	}
}

// A wrong current passphrase aborts the change without touching the vault.
func TestVaultPassphraseWrongCurrentIsRefused(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("old-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, sequencedPassphrases("wrong-phrase", "new-phrase", "new-phrase"))
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "passphrase", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "the passphrase does not unlock the vault") {
		t.Errorf("stderr = %q, want the wrong-passphrase message", stderr)
	}

	fresh := vault.New(dir)
	if _, found, _, err := fresh.Get("wiki-vault", "token-id", offeringPassphrase("old-phrase")); err != nil || !found {
		t.Fatalf("Get() with the original passphrase = found %v, err %v, want true, nil", found, err)
	}
}

// TestVaultDecryptCLI switches encryption off with --confirm and the passphrase, and afterwards the vault
// is a plain document 'vault status' warns about.
func TestVaultDecryptCLI(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "decrypt", "--confirm", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "the vault is decrypted") {
		t.Errorf("stdout = %q, want the outcome reported", stdout)
	}

	fresh := vault.New(dir)
	got, found, state, err := fresh.Get("wiki-vault", "token-id", nil)
	if err != nil || !found || got != canaryVault || state != vault.StateUnencrypted {
		t.Fatalf("Get() = %q, %v, %v, %v, want %q, true, unencrypted, nil", got, found, err, state, canaryVault)
	}

	_, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if stderr != "" {
		t.Fatalf("stderr = %q, want none", stderr)
	}
	if !strings.Contains(stdout, `"state":"unencrypted"`) || !strings.Contains(stdout, "unencrypted") {
		t.Errorf("stdout = %q, want the unencrypted state and its warning", stdout)
	}
}

// Without --confirm 'vault decrypt' refuses before asking for anything, and changes nothing.
func TestVaultDecryptNeedsConfirmFlag(t *testing.T) {
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}

	withVaultPassphrase(t, func(string) (string, error) {
		t.Fatal("the passphrase was asked for without --confirm")
		return "", nil
	})
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "decrypt", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "--confirm") {
		t.Errorf("stderr = %q, want --confirm named", stderr)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want locked, nil", state, err)
	}
}

// keyringVaultMigrationConfig has one keyring credential whose secrets a legacy credentials.yaml holds.
const keyringVaultMigrationConfig = `
version: 1
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  wiki-reader:
    type: keyring
connections:
  wiki:
    service: wiki
    credential: wiki-reader
defaults:
  connections:
    knowledge: wiki
`

func migrationFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(keyringVaultMigrationConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func writeLegacyCredentials(t *testing.T, dir string, allowPlaintext bool) {
	t.Helper()
	body := fmt.Sprintf("version: 1\nallow_plaintext: %t\ncredentials:\n  wiki-reader:\n    token-id: %s\n"+
		"    token-secret: %s\n", allowPlaintext, canaryVault, "second-"+canaryVault)
	if err := os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy credentials.yaml: %v", err)
	}
}

// migrateOptions returns fresh options, the way a new process would have them, whose resolver reads from
// store and from the vault and plaintext file under dir. store stands in for the system credential store,
// so a migrate test never reaches the real one of the machine it runs on; passing the same *MemoryStore
// into several calls simulates it persisting the way a real store does across separate invocations.
func migrateOptions(t *testing.T, dir string, store *secret.MemoryStore) *Options {
	t.Helper()
	red := &redact.Redactor{}
	resolver := secret.NewWith(os.Getenv, store, secret.NewFile(filepath.Join(dir, secret.FileName)), red).
		WithVault(vault.New(dir), vault.ReadPassphrase)
	return &Options{Redactor: red, Secrets: resolver}
}

// TestVaultMigrateCLI carries every entry of credentials.yaml into the vault, switches the affected
// credential to type vault with a config.yaml.bak backup, and removes credentials.yaml once confirmed.
func TestVaultMigrateCLI(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)
	store := secret.NewMemoryStore()

	withVaultPassphrase(t, func(string) (string, error) { return "", nil }) // leave the vault unencrypted
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "migrated 2 entries") || !strings.Contains(stdout, "removed") {
		t.Errorf("stdout = %q, want the outcome reported", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no keyring warning: the store held nothing", stderr)
	}

	v := vault.New(dir)
	for _, want := range []struct{ role, value string }{
		{"token-id", canaryVault}, {"token-secret", "second-" + canaryVault},
	} {
		got, found, _, err := v.Get("wiki-reader", want.role, nil)
		if err != nil || !found || got != want.value {
			t.Errorf("vault Get(%q) = %q, %v, %v, want %q, true, nil", want.role, got, found, err, want.value)
		}
	}

	cfg, err := os.ReadFile(configIn(dir))
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	if !strings.Contains(string(cfg), "type: vault") {
		t.Errorf("config.yaml = %q, want the credential switched to type vault", cfg)
	}
	info, err := os.Stat(configIn(dir) + ".bak")
	if err != nil {
		t.Fatalf("config.yaml.bak was not written: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("config.yaml.bak mode = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); !os.IsNotExist(err) {
		t.Errorf("credentials.yaml survives a confirmed migration: %v", err)
	}

	_, stdout, stderr = runWithInput(t, migrateOptions(t, dir, store), "", "config", "validate", "--secrets",
		"--config", configIn(dir), "--output", "json")
	if stderr != "" {
		t.Fatalf("config validate --secrets stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `"source":"vault"`) {
		t.Errorf("config validate --secrets stdout = %q, want source vault", stdout)
	}
}

// Migration reads every entry regardless of allow_plaintext: the switch decided what the old build would
// deliver, never what there was to carry over.
func TestVaultMigrateIgnoresTheAllowPlaintextSwitch(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, false)

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, secret.NewMemoryStore()), "",
		"vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "migrated 2 entries") {
		t.Errorf("stdout = %q, want both entries migrated despite the switch", stdout)
	}
}

// Without a terminal to ask the deletion question on, credentials.yaml stays and the message names the
// next step; nothing already migrated is lost or duplicated on a later run.
func TestVaultMigrateWithoutTerminalKeepsTheFile(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, func(string) (bool, error) { return false, vault.ErrNoTerminal })

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, secret.NewMemoryStore()), "",
		"vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "vault migrate") {
		t.Errorf("stdout = %q, want the next step named", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); err != nil {
		t.Errorf("credentials.yaml was removed without a confirmation: %v", err)
	}
	if got, found, _, err := vault.New(dir).Get("wiki-reader", "token-id", nil); err != nil || !found || got != canaryVault {
		t.Errorf("the entries were not carried over: %q, %v, %v", got, found, err)
	}
}

// A second run after a partial or a complete migration repeats safely: nothing doubles up, and a
// credential already switched to type vault stays that way.
func TestVaultMigrateIsRepeatable(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)
	store := secret.NewMemoryStore()

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(false)) // keep the file, so the second run has something to redo

	if code, _, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate",
		"--config", configIn(dir)); code != exitOK {
		t.Fatalf("first migrate: exit code = %d (stderr: %s)", code, stderr)
	}
	// The second run finds every entry already in the vault from the first one, so it writes nothing
	// again: a repeat must never be reported, or treated, as a second migration of the same values.
	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("second migrate: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "migrated 0 entries") {
		t.Errorf("stdout = %q, want the second run to add nothing new", stdout)
	}

	v := vault.New(dir)
	got, found, _, err := v.Get("wiki-reader", "token-id", nil)
	if err != nil || !found || got != canaryVault {
		t.Errorf("Get() after two migrations = %q, %v, %v, want %q, true, nil", got, found, err, canaryVault)
	}

	// Running it a third time after the file is already gone is a no-op, not an error.
	withVaultConfirm(t, confirming(true))
	if code, _, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate",
		"--config", configIn(dir)); code != exitOK {
		t.Fatalf("third migrate: exit code = %d (stderr: %s)", code, stderr)
	}
	code, stdout, stderr = runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "nothing to migrate") {
		t.Errorf("migrate after the file is gone: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// A second run must never let the plaintext file's copy of a role win back over the keyring value an
// earlier run already carried into the vault: the effective secret must not change just because the
// deletion question was declined and migrate was run again.
func TestVaultMigrateRerunKeepsTheKeyringValueInTheVault(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true) // token-id: canaryVault in the plaintext file
	store := secret.NewMemoryStore()
	if err := store.Set(secret.StoreKey("wiki-reader", "token-id"), "keyring-wins-"+canaryVault); err != nil {
		t.Fatalf("store.Set() = %v", err)
	}

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(false)) // decline the deletion, so credentials.yaml is still there to redo

	if code, _, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate",
		"--config", configIn(dir)); code != exitOK {
		t.Fatalf("first migrate: exit code = %d (stderr: %s)", code, stderr)
	}
	if got, found, _, err := vault.New(dir).Get("wiki-reader", "token-id", nil); err != nil || !found ||
		got != "keyring-wins-"+canaryVault {
		t.Fatalf("Get() after the first migrate = %q, %v, %v, want the keyring value", got, found, err)
	}

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("second migrate: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "migrated 0 entries") {
		t.Errorf("stdout = %q, want the second run to add nothing: the role is already in the vault", stdout)
	}

	got, found, _, err := vault.New(dir).Get("wiki-reader", "token-id", nil)
	if err != nil || !found || got != "keyring-wins-"+canaryVault {
		t.Errorf("Get() after the second migrate = %q, %v, %v, want the keyring value still, not the "+
			"plaintext file's copy of it", got, found, err)
	}
}

// A second run still carries over a role the first one could not have: one that was added to
// credentials.yaml only after the first migrate already ran.
func TestVaultMigrateRerunAddsAStillMissingRole(t *testing.T) {
	dir := migrationFixture(t)
	// The first run only ever sees token-id.
	body := fmt.Sprintf("version: 1\nallow_plaintext: true\ncredentials:\n  wiki-reader:\n    token-id: %s\n",
		canaryVault)
	if err := os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy credentials.yaml: %v", err)
	}
	store := secret.NewMemoryStore()

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(false))

	if code, _, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate",
		"--config", configIn(dir)); code != exitOK {
		t.Fatalf("first migrate: exit code = %d (stderr: %s)", code, stderr)
	}

	// Before the second run, credentials.yaml gains a role the vault has never seen.
	writeLegacyCredentials(t, dir, true) // token-id and token-secret

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("second migrate: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "migrated 1 entry") {
		t.Errorf("stdout = %q, want exactly the one still-missing role added", stdout)
	}

	got, found, _, err := vault.New(dir).Get("wiki-reader", "token-secret", nil)
	if err != nil || !found || got != "second-"+canaryVault {
		t.Errorf("Get(token-secret) = %q, %v, %v, want the role added on the second run", got, found, err)
	}
	// token-id was already there from the first run and must keep its original value.
	got, found, _, err = vault.New(dir).Get("wiki-reader", "token-id", nil)
	if err != nil || !found || got != canaryVault {
		t.Errorf("Get(token-id) = %q, %v, %v, want the first run's value unchanged", got, found, err)
	}
}

// 'vault status' and 'config validate' both point at 'vault migrate' while credentials.yaml exists.
func TestVaultStatusAndConfigValidateNameMigrate(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)
	opts := migrateOptions(t, dir, secret.NewMemoryStore())

	_, stdout, stderr := runWithInput(t, opts, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if stderr != "" {
		t.Fatalf("vault status stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "vault migrate") {
		t.Errorf("vault status stdout = %q, want the migration hint", stdout)
	}

	_, _, stderr = runWithInput(t, opts, "", "config", "validate", "--config", configIn(dir))
	if !strings.Contains(stderr, "vault migrate") {
		t.Errorf("config validate stderr = %q, want the migration hint", stderr)
	}
}

// A second run that has to check an encrypted, locked vault for what it already holds, and finds no
// terminal to ask the passphrase on, fails with vault-locked before it writes anything at all: not the
// configuration, not the vault, not credentials.yaml.
func TestVaultMigrateRerunLockedVaultWithoutTerminalWritesNothing(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)
	store := secret.NewMemoryStore()

	// The first run creates the vault encrypted and declines the deletion, so credentials.yaml is still
	// there for a second run to redo, and the credential is now type vault.
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	withVaultConfirm(t, confirming(false))
	if code, _, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate",
		"--config", configIn(dir)); code != exitOK {
		t.Fatalf("first migrate: exit code = %d (stderr: %s)", code, stderr)
	}

	configBefore, err := os.ReadFile(configIn(dir))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	legacyBefore, err := os.ReadFile(filepath.Join(dir, "credentials.yaml"))
	if err != nil {
		t.Fatalf("read credentials.yaml: %v", err)
	}
	vaultBefore := vaultFiles(t, dir)

	// The second run is a fresh process, so the vault is locked again; no terminal is attached to unlock
	// it while checking what it already holds.
	withVaultPassphrase(t, func(string) (string, error) { return "", vault.ErrNoTerminal })

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitRuntime {
		t.Fatalf("exit code = %d, want %d (stdout: %s, stderr: %s)", code, exitRuntime, stdout, stderr)
	}
	if !strings.Contains(stderr, "qatlas: vault-locked:") {
		t.Errorf("stderr = %q, want the vault-locked code", stderr)
	}

	configAfter, err := os.ReadFile(configIn(dir))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	if string(configBefore) != string(configAfter) {
		t.Error("config.yaml changed although the run aborted with vault-locked")
	}
	legacyAfter, err := os.ReadFile(filepath.Join(dir, "credentials.yaml"))
	if err != nil {
		t.Fatalf("read credentials.yaml: %v", err)
	}
	if string(legacyBefore) != string(legacyAfter) {
		t.Error("credentials.yaml changed although the run aborted with vault-locked")
	}
	vaultAfter := vaultFiles(t, dir)
	if len(vaultBefore) != len(vaultAfter) {
		t.Fatalf("the vault directory changed shape: had %v, has %v", vaultBefore, vaultAfter)
	}
	for name, data := range vaultBefore {
		if string(data) != string(vaultAfter[name]) {
			t.Errorf("%s changed although the run aborted with vault-locked", name)
		}
	}
}

// vaultFiles reads every regular file directly under dir/vault, keyed by name, so a test can prove an
// aborted run changed nothing in it, byte for byte.
func vaultFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, vault.DirName))
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, vault.DirName, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile() = %v", err)
		}
		out[e.Name()] = data
	}
	return out
}

// D: a role held only in the system keyring, never in credentials.yaml, still moves into the vault when
// its credential is switched, so the type change does not orphan it.
func TestVaultMigrateCarriesOverAKeyringOnlyRole(t *testing.T) {
	dir := migrationFixture(t)
	// credentials.yaml only ever names token-id; token-secret sits in the keyring alone.
	body := fmt.Sprintf("version: 1\nallow_plaintext: true\ncredentials:\n  wiki-reader:\n    token-id: %s\n",
		canaryVault)
	if err := os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy credentials.yaml: %v", err)
	}
	store := secret.NewMemoryStore()
	if err := store.Set(secret.StoreKey("wiki-reader", "token-secret"), "keyring-only-"+canaryVault); err != nil {
		t.Fatalf("store.Set() = %v", err)
	}

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no warning: the store answered", stderr)
	}
	if !strings.Contains(stdout, "migrated 2 entries") {
		t.Errorf("stdout = %q, want both roles counted", stdout)
	}

	got, found, _, err := vault.New(dir).Get("wiki-reader", "token-secret", nil)
	if err != nil || !found || got != "keyring-only-"+canaryVault {
		t.Errorf("Get(token-secret) = %q, %v, %v, want the keyring-only value carried over", got, found, err)
	}
}

// D: when the same role sits in both places, the keyring value wins, the same priority the ordinary
// cascade always gave it over the plaintext file.
func TestVaultMigrateKeyringWinsOverPlaintextForTheSameRole(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true) // token-id: canaryVault in the plaintext file
	store := secret.NewMemoryStore()
	if err := store.Set(secret.StoreKey("wiki-reader", "token-id"), "keyring-wins-"+canaryVault); err != nil {
		t.Fatalf("store.Set() = %v", err)
	}

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no warning: the store answered", stderr)
	}
	if !strings.Contains(stdout, "migrated 2 entries") {
		t.Errorf("stdout = %q, want both roles counted", stdout)
	}

	got, found, _, err := vault.New(dir).Get("wiki-reader", "token-id", nil)
	if err != nil || !found || got != "keyring-wins-"+canaryVault {
		t.Errorf("Get(token-id) = %q, %v, %v, want the keyring value to have won", got, found, err)
	}
}

// D: an unreachable keyring does not block the migration; it proceeds with the plaintext values alone and
// names the affected credential in a warning that carries no value. The keyring entry, unreachable or not,
// is never deleted.
func TestVaultMigrateWarnsWhenTheKeyringCannotBeChecked(t *testing.T) {
	dir := migrationFixture(t)
	writeLegacyCredentials(t, dir, true)
	store := secret.NewMemoryStore()
	store.Fail(secret.ErrUnavailable)

	withVaultPassphrase(t, func(string) (string, error) { return "", nil })
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, migrateOptions(t, dir, store), "", "vault", "migrate", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stderr, "qatlas: warning:") || !strings.Contains(stderr, "wiki-reader") ||
		!strings.Contains(stderr, "keyring") {
		t.Errorf("stderr = %q, want a warning naming the credential and the keyring", stderr)
	}
	if strings.Contains(stderr, canaryVault) {
		t.Errorf("stderr = %q, want no secret value in the warning", stderr)
	}
	if !strings.Contains(stdout, "migrated 2 entries") {
		t.Errorf("stdout = %q, want the plaintext entries migrated despite the unreachable keyring", stdout)
	}

	got, found, _, err := vault.New(dir).Get("wiki-reader", "token-id", nil)
	if err != nil || !found || got != canaryVault {
		t.Errorf("Get(token-id) = %q, %v, %v, want the plaintext value carried over", got, found, err)
	}
}

// backupFile writes atomically and at mode 0600, replacing a stale .bak's wider mode rather than keeping
// it, and content that matches the source byte for byte.
func TestBackupFileIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("version: 1\ncredentials: {}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// A stale .bak with a wider mode from an earlier, differently permissioned run must not keep that
	// mode once a fresh backup replaces it.
	if err := os.WriteFile(path+".bak", []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale .bak: %v", err)
	}

	if err := backupFile(path); err != nil {
		t.Fatalf("backupFile() = %v", err)
	}

	got, err := os.ReadFile(path + ".bak")
	if err != nil || string(got) != string(content) {
		t.Fatalf("backup content = %q, %v, want %q, nil", got, err, content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path + ".bak")
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf(".bak mode = %v, %v, want 0600", info, err)
		}
	}
}

// A backup that cannot be read is reported as an error and leaves no partial or stale .bak file behind, so
// runVaultMigrate's unconditional "return err" right after calling backupFile never lets config.yaml be
// rewritten without one.
func TestBackupFileFailureLeavesNoFileBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not carry on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod() = %v", err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()

	if err := backupFile(path); err == nil {
		t.Fatal("backupFile() succeeded although the source could not be read")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf(".bak exists although the backup failed: %v", err)
	}
}
