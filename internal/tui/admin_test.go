// The admin session (see admin.go): the editor opens read-only regardless of the vault's state, and only a
// managing action - here exercised with an ordinary service save, unrelated to the vault credential type
// used elsewhere in this package - is ever gated behind it.
package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// newEncryptedVaultModel builds an editor whose vault is already encrypted and holds one unrelated entry
// ("other"/"role"), so a save under test is never the vault's very first secret. unlockedInProcess decides
// whether the model's own resolver already unlocked it (state vault.StateUnlocked: admin session only, no
// unlocking needed) or starts genuinely locked, the way a freshly started 'qatlas tui' would (unlock and
// admin together), by building the vault through a throwaway resolver first and, for the locked case,
// handing the model a second, fresh one over the same directory.
func newEncryptedVaultModel(t *testing.T, dir, passphrase string, unlockedInProcess bool) (*Model, *config.Store) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("other", "role", "canary-seed",
		func(string) (string, error) { return passphrase, nil }))

	secrets := setup
	if !unlockedInProcess {
		secrets, _ = newVaultResolver(t, dir)
	}
	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return m, store
}

// setAdminTimeout saves vault.admin_timeout without going through the vault form, so a test can pick the
// value its own model is built with before requireAdmin ever reads it.
func setAdminTimeout(t *testing.T, store *config.Store, timeout string) {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.Vault.AdminTimeout = timeout
	mustNoError(t, store.Save(cfg))
}

// The editor opens, and stays, read-only regardless of the vault's state: browsing every section, opening
// an existing entry's form, and moving the cursor around never asks for a passphrase or touches the vault,
// whether it is locked, unlocked in this process, or unencrypted. Only an actual managing action does.
func TestAdminSessionEditorOpensReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name              string
		unlockedInProcess bool
	}{
		{"locked", false},
		{"unlocked in process", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			m, store := newEncryptedVaultModel(t, dir, "hunter2", tt.unlockedInProcess)

			cfg := newTestConfig(t)
			mustNoError(t, cfg.SetService("wiki", config.Service{
				Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
			mustNoError(t, store.Save(cfg))
			m.cfg = cfg

			for _, s := range []section{sectionServices, sectionCredentials, sectionConnections, sectionDefaults} {
				openSectionByName(t, m, s)
				press(t, m, "up", "down", "down", "up")
			}
			openSectionByName(t, m, sectionServices)
			pump(t, m, "enter") // opens the existing service's form to look at it, nothing more
			if m.screen != screenForm {
				t.Fatalf("opening an existing entry = screen %v, want the form", m.screen)
			}
			press(t, m, "tab", "shift+tab", "esc")

			if m.screen == screenAdminAuth || m.adminAuth != nil {
				t.Fatalf("merely reading around the editor opened the admin dialog: screen %v", m.screen)
			}
			if m.fail != "" {
				t.Fatalf("merely reading around the editor reported %q", m.fail)
			}
		})
	}
}

// The first managing action of a run, against a vault that is already unlocked in this process but has no
// admin session yet, asks for the passphrase in its own masked dialog; a wrong one reopens the same dialog
// with the error shown and writes nothing, and the right one saves at once and starts the session, so a
// second, unrelated managing action right after runs without asking again.
func TestAdminSessionAsksOnceThenStaysActive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, "hunter2", true)

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")

	if m.screen != screenAdminAuth || m.adminAuth == nil || m.adminAuth.locked {
		t.Fatalf("saving a new service against an unlocked, session-less vault = screen %v, want the "+
			"admin-only dialog", m.screen)
	}

	typeText(t, m, "wrong")
	pump(t, m, "enter")
	if !strings.Contains(m.fail, "wrong passphrase") || m.screen != screenAdminAuth {
		t.Fatalf("a wrong passphrase = screen %v fail %q, want the dialog reopened with the error",
			m.screen, m.fail)
	}
	if _, err := store.Load(); err == nil {
		if loaded, _ := store.Load(); len(loaded.Services) != 0 {
			t.Fatalf("a wrong passphrase still saved something: %+v", loaded.Services)
		}
	}

	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("saving with the right passphrase reported %q", m.fail)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Services["wiki-primary"].BaseURL != "https://wiki.example.invalid" {
		t.Fatalf("the service was not saved: %v, %+v", err, loaded.Services)
	}
	if !m.AdminSessionActive() {
		t.Fatal("AdminSessionActive() = false right after a successful admin check")
	}

	// A second, unrelated managing action, still within the session, runs at once.
	press(t, m, "d")
	if m.screen != screenConfirm {
		t.Fatalf("d did not ask to confirm: screen %v", m.screen)
	}
	press(t, m, "y")
	if m.screen == screenAdminAuth {
		t.Fatal("a second managing action asked again although the admin session is still active")
	}
	if m.fail != "" {
		t.Fatalf("deleting within the active session reported %q", m.fail)
	}
}

// Cancelling the admin dialog (esc) runs nothing: the vault stays exactly as it was, and every field already
// typed into the open form is still there, so the same save can be tried again without retyping anything.
func TestAdminSessionCancelPreservesFormInput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, "hunter2", true)

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")
	if m.screen != screenAdminAuth {
		t.Fatalf("screen = %v, want the admin dialog open", m.screen)
	}

	press(t, m, "esc")
	if m.screen != screenForm || m.adminAuth != nil {
		t.Fatalf("esc did not cancel cleanly: screen %v, dialog %v", m.screen, m.adminAuth)
	}
	if m.fields[0].value() != "wiki-primary" || m.fieldValue("base url") != "https://wiki.example.invalid" {
		t.Fatalf("cancelling the admin check lost typed input: name %q base_url %q",
			m.fields[0].value(), m.fieldValue("base url"))
	}
	// Nothing was ever saved before this attempt, so a still-missing configuration file is itself proof
	// enough that the cancelled save wrote nothing.
	if _, err := store.Load(); err == nil {
		t.Fatal("the cancelled save still wrote a configuration file")
	}

	// Trying again, with the form exactly as it was, saves it.
	press(t, m, "f2")
	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("saving again after the cancelled attempt reported %q", m.fail)
	}
}

// vault.admin_timeout: 0 never keeps a session at all: every single managing action asks for the passphrase
// again, immediately after the previous one succeeded.
func TestAdminTimeoutZeroAsksEveryChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, "hunter2", true)
	setAdminTimeout(t, store, "0")
	m.cfg = mustLoad(t, store)

	addKeyringCredentialGated(t, m, "reader", "hunter2")
	if m.AdminSessionActive() {
		t.Fatal("AdminSessionActive() = true with vault.admin_timeout: 0")
	}

	// A second, otherwise independent managing action asks again right away.
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")
	if m.screen != screenAdminAuth {
		t.Fatalf("a second change with admin_timeout: 0 did not ask again: screen %v", m.screen)
	}
}

// Once vault.admin_timeout passes without a key press, the session ends silently, and the next managing
// action asks again. Any key press while the session is still active renews its idle deadline instead, so
// it never expires while someone is actually using the window.
func TestAdminSessionIdlesOutAndKeysRenewIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", true)

	addKeyringCredentialGated(t, m, "reader", "hunter2")
	if !m.AdminSessionActive() {
		t.Fatal("AdminSessionActive() = false right after establishing the session")
	}

	// A key press while the session is active renews its idle deadline: simulate it having almost expired,
	// then press an unrelated key and confirm it is back to nearly the full timeout.
	m.adminSessionUntil = time.Now().Add(2 * time.Second)
	press(t, m, "down")
	if remaining := m.AdminSessionRemaining(); remaining < 5*time.Minute {
		t.Fatalf("AdminSessionRemaining() = %v after a key press, want it renewed close to the full timeout",
			remaining)
	}

	// Simulate the idle timeout having passed without any activity: the session is then simply inactive, and
	// the next managing action asks again, exactly as a fresh one would.
	m.adminSessionUntil = time.Now().Add(-time.Second)
	if m.AdminSessionActive() {
		t.Fatal("AdminSessionActive() = true past its own deadline")
	}

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")
	if m.screen != screenAdminAuth {
		t.Fatalf("a managing action after the idle timeout did not ask again: screen %v", m.screen)
	}
}

// An unencrypted vault has no passphrase and so no admin session at all: every managing action runs exactly
// as it did before this window ever existed.
func TestAdminSessionNoneForUnencryptedVault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", nil))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if state, err := secrets.Vault().State(); err != nil || state != vault.StateUnencrypted {
		t.Fatalf("test setup: vault state = %v, %v, want unencrypted", state, err)
	}

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")
	if m.screen == screenAdminAuth {
		t.Fatal("an unencrypted vault still asked for an admin passphrase")
	}
	if m.fail != "" {
		t.Fatalf("saving against an unencrypted vault reported %q", m.fail)
	}
}

// A run with no vault configured at all behaves the same way every test elsewhere in this package that
// builds a model without one already relies on: no admin session concept applies, because there is no
// passphrase anything could gate.
func TestAdminSessionNoneWithoutAVault(t *testing.T) {
	m, _, _ := newModel(t)
	if m.secrets.Vault() != nil {
		t.Fatal("test setup: this model unexpectedly has a vault")
	}

	addService(t, m, "wiki-primary", "https://wiki.example.invalid")
	if m.screen == screenAdminAuth {
		t.Fatal("a run without a vault still asked for an admin passphrase")
	}
	if m.fail != "" {
		t.Fatalf("saving without a vault reported %q", m.fail)
	}
}

// addKeyringCredentialGated is addKeyringCredential plus completing the admin dialog it now opens the first
// time in a run against an encrypted vault: it saves through the same F2 key sequence, then, once, answers
// the admin passphrase dialog.
func addKeyringCredentialGated(t *testing.T, m *Model, name, passphrase string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	typeText(t, m, name)
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageKeyring)
	press(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Fatalf("creating a credential against a session-less encrypted vault did not open the admin "+
			"dialog: screen %v", m.screen)
	}
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("creating the keyring credential reported %q", m.fail)
	}
}

// Committing the guided setup is a managing action too, gated the same way an ordinary form's save is: where
// the vault is already unlocked in this process but this window has no admin session yet, the summary's own
// save asks for it first, and the setup's typed secrets, still only held in memory, are written only once
// the right passphrase answers it.
func TestAdminSessionGatesGuidedSetupSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", true)

	walkSetup(t, m, stepCredential)
	press(t, m, "tab")
	typeText(t, m, "reader")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, canaryID)
	press(t, m, "tab")
	typeText(t, m, canarySecret)
	press(t, m, "enter")
	press(t, m, "enter") // scope step, default target
	press(t, m, "f2")
	if m.screen != screenSummary {
		t.Fatalf("did not reach the summary: screen %v fail %q", m.screen, m.fail)
	}

	press(t, m, "enter")
	if m.screen != screenAdminAuth || m.adminAuth == nil || m.adminAuth.locked {
		t.Fatalf("saving the guided setup against a session-less encrypted vault did not open the "+
			"admin-only dialog: screen %v", m.screen)
	}

	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if m.fail != "" || m.wizard == nil || m.wizard.saved == "" {
		t.Fatalf("saving with the right passphrase failed: fail %q, saved %q", m.fail, m.wizard.saved)
	}
}

// A second enter on the admin dialog, while the first passphrase is still being checked, must not start a
// second check over the same vault.Vault: neither Unlock nor VerifyPassphrase is safe to run twice at once
// on it. This is the same guard every other vault write or check in this editor already uses (vaultBusy);
// it is tested here directly, by holding the first command back instead of running it, since a real race
// only ever shows up under timing this package's tests cannot control.
func TestAdminSessionSecondEnterWhileCheckingIsIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", true)

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")
	if m.screen != screenAdminAuth {
		t.Fatalf("did not open the admin dialog: screen %v", m.screen)
	}

	typeText(t, m, "hunter2")
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil || !m.vaultBusy {
		t.Fatal("the first enter did not start the check")
	}

	_, second := m.Update(keyMsg("enter"))
	if second != nil {
		t.Fatal("a second enter while the check is still running started another one")
	}

	msg := cmd()
	m.Update(msg)
	if m.fail != "" {
		t.Fatalf("the held-back check reported %q", m.fail)
	}
}

// A vault whose state cannot even be read (here: its directory made inaccessible, so every file check inside
// it fails) must not be treated as if it had none: requireAdmin fails closed instead of letting the managing
// action through. It reports the reason the same way any other refused save does, never runs the action, and
// leaves the screen and every field of the open form exactly as they were, so the save can be retried once
// the vault is reachable again.
func TestAdminSessionFailsClosedWhenVaultStateCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not carry on Windows")
	}
	dir := filepath.Join(t.TempDir(), "qatlas")
	// unlockedInProcess: false, so this model's own vault.Vault never cached its state in memory and a
	// State() call genuinely has to read the directory below.
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", false)

	vaultDir := filepath.Join(dir, vault.DirName)
	if err := os.Chmod(vaultDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(vaultDir, 0o700) })

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "enter")

	if m.screen != screenForm {
		t.Fatalf("a vault whose state could not be read still changed the screen: %v", m.screen)
	}
	if m.fail == "" {
		t.Fatal("a vault whose state could not be read did not report a reason")
	}
	if strings.Contains(m.fail, "hunter2") {
		t.Errorf("fail = %q, a passphrase must never appear in it", m.fail)
	}
	if m.fields[0].value() != "wiki-primary" || m.fieldValue("base url") != "https://wiki.example.invalid" {
		t.Fatalf("the refused save lost typed input: name %q base_url %q",
			m.fields[0].value(), m.fieldValue("base url"))
	}

	// Once the vault is reachable again, the very same save goes through without retyping anything.
	os.Chmod(vaultDir, 0o700)
	m.fail = ""
	press(t, m, "enter")
	if m.screen != screenAdminAuth {
		t.Fatalf("the retried save did not reach the admin dialog: screen %v fail %q", m.screen, m.fail)
	}
}

func mustLoad(t *testing.T, store *config.Store) *config.Config {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	return cfg
}
