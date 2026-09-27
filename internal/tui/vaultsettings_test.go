package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// openVaultSection navigates to the Vault section, which opens the settings form directly, never a list.
func openVaultSection(t *testing.T, m *Model) {
	t.Helper()
	openSectionByName(t, m, sectionVault)
	if m.screen != screenForm {
		t.Fatalf("opening Vault = screen %v, want the settings form", m.screen)
	}
}

// Digit 5 and the sidebar/narrow navigation name the fifth section Vault, with no entry count of its own.
func TestVaultSectionInNavigation(t *testing.T) {
	m, _, _ := newModel(t)

	openVaultSection(t, m)
	if m.section != sectionVault {
		t.Fatalf("section = %v, want sectionVault", m.section)
	}

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	if view := m.View(); !strings.Contains(view, "5 Vault") {
		t.Errorf("sidebar does not name the Vault section:\n%s", view)
	} else if strings.Contains(view, "5 Vault    0") || strings.Contains(view, "5 Vault  0") {
		t.Errorf("sidebar shows an entry count for the settings form:\n%s", view)
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if view := m.View(); !strings.Contains(view, "5") {
		t.Errorf("narrow navigation does not offer digit 5:\n%s", view)
	}
}

// Vault has no list between the sidebar and its form: esc on the unchanged form returns directly to the
// sidebar in one step, the way esc on an unchanged list would for any other section, and 'n' is refused
// with a reason rather than opening a broken, nameless form.
func TestVaultSectionNavigatesLikeASingleLevel(t *testing.T) {
	m, _, _ := newModel(t)
	openVaultSection(t, m)

	press(t, m, "esc")
	if m.screen != screenNav || m.section != sectionVault {
		t.Fatalf("esc on the unchanged vault form = screen %v section %v, want the sidebar", m.screen, m.section)
	}

	press(t, m, "n")
	if m.screen != screenNav || m.fail == "" {
		t.Errorf("n on the Vault section = screen %v fail %q, want it refused with a reason", m.screen, m.fail)
	}

	// enter, right, and tab from the sidebar all open the form directly, the same as they open a list's own
	// entry for any other section.
	for _, key := range []string{"enter", "right", "l", "tab"} {
		m.screen, m.section = screenNav, sectionVault
		press(t, m, key)
		if m.screen != screenForm || m.section != sectionVault {
			t.Errorf("%q from the sidebar = screen %v section %v, want the vault form", key, m.screen, m.section)
		}
	}
}

// Each vault state shows exactly the actions that apply to it: no vault yet offers none, unencrypted offers
// only turning it on, and encrypted (locked or unlocked) offers changing the passphrase and turning it off,
// never turning it on again.
func TestVaultSectionShowsStateAndActions(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store := newTestStore(t, filepath.Join(dir, "config.yaml"))
		secrets, _ := newVaultResolver(t, dir)
		m, err := New(store, nil, secrets, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		openVaultSection(t, m)
		view := screenOf(m)
		if !strings.Contains(view, "no vault yet") {
			t.Errorf("absent state not shown:\n%s", view)
		}
		for _, action := range []string{"encrypt", "change passphrase", "turn off encryption"} {
			if strings.Contains(view, action) {
				t.Errorf("absent vault offers action %q:\n%s", action, view)
			}
		}
	})

	t.Run("unencrypted", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store := newTestStore(t, filepath.Join(dir, "config.yaml"))
		secrets, _ := newVaultResolver(t, dir)
		mustNoError(t, secrets.SetVault("reader", "token-id", "canary-unencrypted-88a1", nil))

		m, err := New(store, nil, secrets, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		openVaultSection(t, m)
		view := screenOf(m)
		if !strings.Contains(view, "unencrypted") {
			t.Errorf("unencrypted state not shown:\n%s", view)
		}
		if len(m.fields) < 2 || m.fields[1].action != vaultActionEncrypt {
			t.Errorf("unencrypted vault does not offer the encrypt action row: fields %+v", m.fields)
		}
		for _, action := range []string{"change passphrase", "turn off encryption"} {
			if strings.Contains(view, action) {
				t.Errorf("unencrypted vault offers action %q:\n%s", action, view)
			}
		}
	})

	t.Run("locked", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store := newTestStore(t, filepath.Join(dir, "config.yaml"))
		setup, _ := newVaultResolver(t, dir)
		mustNoError(t, setup.SetVault("reader", "token-id", "canary-locked-77c2",
			func(string) (string, error) { return "hunter2", nil }))

		// A fresh resolver over the same directory starts genuinely locked, the way a new 'qatlas tui' would.
		locked, _ := newVaultResolver(t, dir)
		m, err := New(store, nil, locked, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		openVaultSection(t, m)
		view := screenOf(m)
		if !strings.Contains(view, "encrypted, locked") {
			t.Errorf("locked state not shown:\n%s", view)
		}
		for _, action := range []string{"change passphrase", "turn off encryption"} {
			if !strings.Contains(view, action) {
				t.Errorf("locked vault does not offer %q:\n%s", action, view)
			}
		}
		// "encrypt" is a substring of "encrypted" and "encryption", both expected in this state's own text;
		// the actual row is checked on the model, not by scraping the view for a substring that overlaps.
		for _, f := range m.fields {
			if f.action == vaultActionEncrypt {
				t.Errorf("locked vault still offers the encrypt action row")
			}
		}
	})
}

// Turning encryption on takes a new passphrase, typed masked and twice, a mismatch leaves the prompt open,
// and success really encrypts the vault through its own API, never a value or passphrase reaching the
// screen, the status, or an error.
func TestVaultEncryptTurnsEncryptionOn(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("reader", "token-id", "canary-encrypt-3f9a", nil))
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.test"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, cfg.SetConnection("wiki", config.Connection{Service: "wiki", Credential: "reader"}))
	mustNoError(t, store.Save(cfg))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "encrypt")

	var rendered strings.Builder
	press(t, m, "enter")
	if m.screen != screenVaultOffer {
		t.Fatalf("enter on encrypt did not open the passphrase prompt: screen %v", m.screen)
	}
	rendered.WriteString(screenOf(m))

	// A mismatch leaves the prompt open at its first entry; nothing is written.
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	typeText(t, m, "a different one")
	pump(t, m, "enter")
	if !strings.Contains(m.fail, "did not match") {
		t.Fatalf("mismatch error = %q", m.fail)
	}
	if m.screen != screenVaultOffer || m.vaultOffer.confirming {
		t.Fatalf("mismatch did not return to the first entry: screen %v", m.screen)
	}
	rendered.WriteString(screenOf(m))

	typeText(t, m, passphrase)
	pump(t, m, "enter")
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("encrypting reported %q", m.fail)
	}
	if m.screen != screenForm {
		t.Fatalf("screen = %v, want back on the vault form", m.screen)
	}
	rendered.WriteString(screenOf(m))
	rendered.WriteString(m.status)

	if !strings.Contains(m.status, "encrypted") || !strings.Contains(m.status, "approved 1 connection(s) to read from it: wiki") {
		t.Errorf("status = %q, want the vault reported encrypted and its connection approved", m.status)
	}
	view := screenOf(m)
	if !strings.Contains(view, "encrypted, locked") && !strings.Contains(view, "encrypted, unlocked") {
		t.Errorf("form does not show the vault as encrypted:\n%s", view)
	}
	if strings.Contains(view, "encrypt") && !strings.Contains(view, "encrypted") {
		t.Errorf("form still offers to encrypt an already encrypted vault:\n%s", view)
	}

	fresh := vault.New(dir)
	if state, err := fresh.State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want the vault encrypted and locked for a new process", state, err)
	}
	if _, err := fresh.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	resolved, err := cfg.Resolve("wiki", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.CheckApproval(secret.ScopeOf(resolved)); err != nil {
		t.Errorf("CheckApproval() after encrypting = %v, want the saved connection approved", err)
	}

	for _, canary := range []string{passphrase, "a different one"} {
		if strings.Contains(rendered.String(), canary) {
			t.Errorf("a typed passphrase reached the screen:\n%s", rendered.String())
		}
	}
}

// Changing the passphrase asks for the current one, then the new one twice; a wrong current passphrase is
// reported as "error: wrong passphrase" and the form stays open, without ever naming either passphrase.
func TestVaultChangePassphrase(t *testing.T) {
	const (
		current = "hunter2"
		fresh   = "a whole new passphrase"
	)
	newLockedModel := func(t *testing.T) (*Model, string) {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store := newTestStore(t, filepath.Join(dir, "config.yaml"))
		setup, _ := newVaultResolver(t, dir)
		mustNoError(t, setup.SetVault("reader", "token-id", "canary-change-a1b2",
			func(string) (string, error) { return current, nil }))
		locked, _ := newVaultResolver(t, dir)
		m, err := New(store, nil, locked, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		return m, dir
	}

	t.Run("wrong current passphrase", func(t *testing.T) {
		m, dir := newLockedModel(t)
		openVaultSection(t, m)
		focusRole(t, m, "change passphrase")

		var rendered strings.Builder
		press(t, m, "enter")
		rendered.WriteString(screenOf(m))
		typeText(t, m, "not the real passphrase")
		// Verified asynchronously before a new passphrase is ever asked for: nothing typed afterwards would
		// be kept anyway, so this same prompt reopens instead of moving on.
		pump(t, m, "enter")
		rendered.WriteString(screenOf(m))
		rendered.WriteString(m.fail)

		if m.fail != "wrong passphrase" {
			t.Fatalf("error = %q, want the short wrong-passphrase message", m.fail)
		}
		if view := screenOf(m); strings.Count(view, "error: wrong passphrase") != 1 ||
			strings.Contains(view, "error: error:") {
			t.Fatalf("the view does not show the wrong-passphrase message exactly once, with one error: "+
				"prefix:\n%s", view)
		}
		if m.screen != screenVaultOffer {
			t.Fatalf("screen = %v, want the current passphrase prompt to stay open", m.screen)
		}
		if m.vaultOffer == nil || m.vaultOffer.confirming {
			t.Fatalf("the reopened prompt is not back at its own first entry")
		}
		if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
			t.Fatalf("State() = %v, %v, want the vault untouched", state, err)
		}
		for _, canary := range []string{"not the real passphrase", fresh, current} {
			if strings.Contains(rendered.String(), canary) {
				t.Errorf("a passphrase reached the screen:\n%s", rendered.String())
			}
		}
	})

	t.Run("success", func(t *testing.T) {
		m, dir := newLockedModel(t)
		openVaultSection(t, m)
		focusRole(t, m, "change passphrase")

		press(t, m, "enter")
		typeText(t, m, current)
		pump(t, m, "enter")
		typeText(t, m, fresh)
		pump(t, m, "enter")
		typeText(t, m, fresh)
		pump(t, m, "enter")

		if m.fail != "" {
			t.Fatalf("changing the passphrase reported %q", m.fail)
		}
		if !strings.Contains(m.status, "changed") {
			t.Errorf("status = %q, want the change confirmed", m.status)
		}

		reopened := vault.New(dir)
		if _, _, _, err := reopened.Get("reader", "token-id",
			func(string) (string, error) { return current, nil }); err == nil {
			t.Error("the old passphrase still unlocks the vault")
		}
		got, found, _, err := reopened.Get("reader", "token-id",
			func(string) (string, error) { return fresh, nil })
		if err != nil || !found || got != "canary-change-a1b2" {
			t.Errorf("Get() with the new passphrase = %q, %v, %v", got, found, err)
		}
	})
}

// Turning encryption off asks for the current passphrase and then an explicit confirmation; declining
// leaves the vault encrypted, a wrong passphrase is refused the same short way, and success writes every
// secret back unencrypted.
func TestVaultDecrypt(t *testing.T) {
	const passphrase = "hunter2"
	newLockedModel := func(t *testing.T) (*Model, string) {
		dir := filepath.Join(t.TempDir(), "qatlas")
		store := newTestStore(t, filepath.Join(dir, "config.yaml"))
		setup, _ := newVaultResolver(t, dir)
		mustNoError(t, setup.SetVault("reader", "token-id", "canary-decrypt-9c3d",
			func(string) (string, error) { return passphrase, nil }))
		locked, _ := newVaultResolver(t, dir)
		m, err := New(store, nil, locked, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		return m, dir
	}

	t.Run("declined confirmation keeps it encrypted", func(t *testing.T) {
		m, dir := newLockedModel(t)
		openVaultSection(t, m)
		focusRole(t, m, "turn off encryption")

		press(t, m, "enter")
		typeText(t, m, passphrase)
		pump(t, m, "enter")
		if m.screen != screenConfirm || !m.decryptConfirm {
			t.Fatalf("the current passphrase did not lead to the confirmation: screen %v", m.screen)
		}
		press(t, m, "n")
		if m.screen != screenForm || m.decryptConfirm {
			t.Fatalf("n did not return to the form: screen %v decryptConfirm %v", m.screen, m.decryptConfirm)
		}
		if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
			t.Fatalf("State() = %v, %v, want the vault still encrypted", state, err)
		}
	})

	t.Run("wrong passphrase", func(t *testing.T) {
		m, dir := newLockedModel(t)
		openVaultSection(t, m)
		focusRole(t, m, "turn off encryption")

		press(t, m, "enter")
		typeText(t, m, "not the real passphrase")
		// Verified asynchronously before the y/n confirmation is ever asked for: this same prompt reopens
		// instead of moving on to a confirmation that would turn off encryption for nothing.
		pump(t, m, "enter")

		if m.fail != "wrong passphrase" {
			t.Fatalf("error = %q, want the short wrong-passphrase message", m.fail)
		}
		if m.screen != screenVaultOffer || m.decryptConfirm {
			t.Fatalf("screen = %v decryptConfirm %v, want the current passphrase prompt to stay open, not "+
				"the y/n confirmation", m.screen, m.decryptConfirm)
		}
		if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
			t.Fatalf("State() = %v, %v, want the vault untouched", state, err)
		}
	})

	t.Run("success", func(t *testing.T) {
		m, dir := newLockedModel(t)
		openVaultSection(t, m)
		focusRole(t, m, "turn off encryption")

		var rendered strings.Builder
		press(t, m, "enter")
		rendered.WriteString(screenOf(m))
		typeText(t, m, passphrase)
		pump(t, m, "enter")
		rendered.WriteString(screenOf(m))
		pump(t, m, "y")
		rendered.WriteString(screenOf(m))
		rendered.WriteString(m.status)

		if m.fail != "" {
			t.Fatalf("decrypting reported %q", m.fail)
		}
		if !strings.Contains(m.status, "decrypted") {
			t.Errorf("status = %q, want the switch confirmed", m.status)
		}
		fresh := vault.New(dir)
		if state, err := fresh.State(); err != nil || state != vault.StateUnencrypted {
			t.Fatalf("State() = %v, %v, want the vault unencrypted", state, err)
		}
		got, found, _, err := fresh.Get("reader", "token-id", nil)
		if err != nil || !found || got != "canary-decrypt-9c3d" {
			t.Errorf("Get() = %q, %v, %v, want the secret intact", got, found, err)
		}
		if strings.Contains(rendered.String(), passphrase) {
			t.Errorf("the passphrase reached the screen:\n%s", rendered.String())
		}
	})
}

// The confirmation before turning encryption off warns, in words, that every secret ends up unencrypted on
// disk, so nobody agrees to it without knowing what changes.
func TestVaultDecryptConfirmationWarns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-warn-1a2b",
		func(string) (string, error) { return "hunter2", nil }))
	locked, _ := newVaultResolver(t, dir)
	m, err := New(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "turn off encryption")
	press(t, m, "enter")
	typeText(t, m, "hunter2")
	pump(t, m, "enter")

	view := screenOf(m)
	if !strings.Contains(view, "unencrypted") {
		t.Errorf("the confirmation does not warn about the consequence:\n%s", view)
	}
}

// vault.idle_timeout and vault.admin_timeout save with F2 through the same validation every other setting
// uses, survive a reload, and a refused value keeps the form open with what was typed.
func TestVaultSectionSavesTimeouts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)

	focusRole(t, m, "idle timeout")
	typeText(t, m, "not a duration")
	press(t, m, "f2")
	if !strings.Contains(m.fail, "vault.idle_timeout") {
		t.Fatalf("error = %q, want it refused by its key", m.fail)
	}
	if m.screen != screenForm || m.fieldValue("idle timeout") != "not a duration" {
		t.Fatalf("a refused value did not keep the form open with what was typed: screen %v value %q",
			m.screen, m.fieldValue("idle timeout"))
	}

	clearField(t, m)
	typeText(t, m, "45m")
	focusRole(t, m, "admin timeout")
	typeText(t, m, "0")
	press(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving valid timeouts reported %q", m.fail)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if loaded.VaultIdleTimeout() != 45*time.Minute {
		t.Errorf("VaultIdleTimeout() = %v, want 45m", loaded.VaultIdleTimeout())
	}
	if loaded.VaultAdminTimeout() != 0 {
		t.Errorf("VaultAdminTimeout() = %v, want 0, not the default", loaded.VaultAdminTimeout())
	}
}

// A change to the vault's timeouts is a change like any other: esc asks before it is lost, and saving
// through that question goes through the same validation as F2.
func TestVaultSectionLeaveAsksBeforeLosingTimeoutChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)

	focusRole(t, m, "idle timeout")
	typeText(t, m, "2h")
	press(t, m, "esc")
	if m.screen != screenLeave {
		t.Fatalf("esc with an unsaved timeout change = screen %v, want the leave question", m.screen)
	}
	press(t, m, "s")
	if m.fail != "" {
		t.Fatalf("saving from the leave question reported %q", m.fail)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if loaded.VaultIdleTimeout() != 2*time.Hour {
		t.Errorf("VaultIdleTimeout() = %v, want 2h", loaded.VaultIdleTimeout())
	}
}

// A vault write, or the prompt that may lead to one, blocks a second vault settings action from starting
// before it is done, the same guard the credential secret rows already use.
func TestVaultActionIsAsyncAndNotDoubleTriggered(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("reader", "token-id", "canary-async-5e6f", nil))

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "encrypt")
	press(t, m, "enter")
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	typeText(t, m, passphrase)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("confirming the passphrase produced no command")
	}
	if !m.vaultBusy {
		t.Fatal("the encrypt write in flight does not set vaultBusy")
	}
	if !strings.Contains(m.busy, "encrypt") {
		t.Errorf("busy = %q, want the encrypt write reported", m.busy)
	}

	// A second action, while the first is still in flight, is refused rather than started.
	press(t, m, "esc") // back to the form; esc on screenVaultOffer cancels only an unanswered prompt
	openVaultSection(t, m)
	focusRole(t, m, "encrypt")
	press(t, m, "enter")
	if m.screen == screenVaultOffer {
		t.Fatal("a second vault action started while the first was still in flight")
	}

	msg := cmd()
	m.Update(msg)
	if m.vaultBusy {
		t.Error("vaultBusy stays set after the write finished")
	}
	if m.fail != "" {
		t.Errorf("the finished write reported %q", m.fail)
	}
}
