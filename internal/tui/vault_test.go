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

// The storage row of a new credential preselects defaults.secret_store, in the credential form and in the
// guided setup alike, and offers no recommendation for any of the three choices any more.
func TestStoragePreselectsTheConfiguredDefault(t *testing.T) {
	for _, tt := range []struct {
		name    string
		store   string
		want    string
		wantVia []string
	}{
		{"unset defaults to keyring", "", storageKeyring, []string{storageKeyring, storageVault, storageEnv}},
		{"explicit keyring", config.CredentialTypeKeyring, storageKeyring,
			[]string{storageKeyring, storageVault, storageEnv}},
		{"explicit vault", config.CredentialTypeVault, storageVault,
			[]string{storageKeyring, storageVault, storageEnv}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store := newTestStore(t, filepath.Join(dir, "config.yaml"))
			cfg := newTestConfig(t)
			cfg.Defaults.SecretStore = tt.store
			mustNoError(t, store.Save(cfg))
			secrets, _ := newResolver(t, dir, nil)

			// The credential form.
			e, err := buildModel(store, nil, secrets, nil)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			openSectionByName(t, e, sectionCredentials)
			press(t, e, "n")
			row := e.field(storageLabel)
			if row == nil || row.value() != tt.want || !equalChoices(row.choices, tt.wantVia) {
				t.Errorf("credential form storage = %+v, want %q among %v", row, tt.want, tt.wantVia)
			}
			if strings.Contains(screenOf(e), "(recommended)") {
				t.Errorf("the credential form still recommends a place:\n%s", screenOf(e))
			}

			// The guided setup.
			m, err := buildModel(store, nil, secrets, nil)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			m.screen = screenNav
			press(t, m, "c")
			steps := []func(){
				func() { press(t, m, "enter") },
				func() {
					press(t, m, "tab")
					typeText(t, m, "wiki")
					press(t, m, "tab")
					typeText(t, m, "https://wiki.example.invalid")
					press(t, m, "enter")
				},
			}
			for _, step := range steps {
				step()
			}
			press(t, m, "tab")
			row = m.field(storageLabel)
			if row == nil || row.value() != tt.want || !equalChoices(row.choices, tt.wantVia) {
				t.Errorf("guided setup storage = %+v, want %q among %v", row, tt.want, tt.wantVia)
			}
			if strings.Contains(screenOf(m), "(recommended)") {
				t.Errorf("the guided setup still recommends a place:\n%s", screenOf(m))
			}
		})
	}
}

func equalChoices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Storing the vault's very first secret through the credential form offers a passphrase, typed masked and
// twice; setting one really encrypts the vault, using the vault's own API and no cryptography of this
// editor's own.
func TestVaultOfferSetsAPassphrase(t *testing.T) {
	const (
		secretValue = "canary-first-vault-secret-91af"
		passphrase  = "correct horse battery staple"
	)
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addVaultCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	if m.screen != screenSecret {
		t.Fatalf("s did not open the masked prompt: screen %v", m.screen)
	}
	typeText(t, m, secretValue)

	var rendered strings.Builder
	_, cmd := m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Fatalf("submitting the very first vault secret ran a command before the offer was answered")
	}
	if m.screen != screenVaultOffer {
		t.Fatalf("the vault's first secret did not offer a passphrase: screen %v, error %q", m.screen, m.fail)
	}
	rendered.WriteString(screenOf(m))

	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.screen != screenVaultOffer || m.vaultOffer == nil || !m.vaultOffer.confirming {
		t.Fatalf("the offer did not move to its confirmation: screen %v", m.screen)
	}
	rendered.WriteString(screenOf(m))

	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("setting the passphrase reported %q", m.fail)
	}
	if m.screen != screenForm {
		t.Fatalf("screen = %v, want back on the form once the offer is answered", m.screen)
	}
	rendered.WriteString(screenOf(m))
	rendered.WriteString(m.status)

	for _, canary := range []string{secretValue, passphrase} {
		if strings.Contains(rendered.String(), canary) {
			t.Errorf("a typed value reached the screen:\n%s", rendered.String())
		}
	}

	// A fresh vault.Vault, reading only from disk, proves the encryption really happened: the API was used
	// as documented, not faked by this editor.
	fresh := vault.New(dir)
	if state, err := fresh.State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want the vault encrypted and locked for a new process", state, err)
	}
	if _, _, _, err := fresh.Get("reader", "token-id", func(string) (string, error) { return "wrong", nil }); err == nil {
		t.Error("a wrong passphrase opened the vault")
	}
	got, found, _, err := fresh.Get("reader", "token-id", func(string) (string, error) { return passphrase, nil })
	if err != nil || !found || got != secretValue {
		t.Errorf("Get() = %q, %v, %v, want the value just stored under the passphrase", got, found, err)
	}
}

// A mismatch between the two passphrase entries is an error that leaves the offer open for another try;
// nothing is written until it is answered one way or the other.
func TestVaultOfferMismatchStaysOpen(t *testing.T) {
	const secretValue = "canary-mismatch-4b19"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addVaultCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	typeText(t, m, secretValue)
	pump(t, m, "enter")
	if m.screen != screenVaultOffer {
		t.Fatalf("the offer did not open: screen %v", m.screen)
	}

	typeText(t, m, "first-typed-passphrase")
	pump(t, m, "enter")
	typeText(t, m, "a-different-passphrase")
	pump(t, m, "enter")

	if !strings.Contains(m.fail, "did not match") {
		t.Fatalf("error = %q, want the mismatch reported", m.fail)
	}
	if m.screen != screenVaultOffer || m.vaultOffer == nil || m.vaultOffer.confirming {
		t.Fatalf("the offer did not stay open at its first entry: screen %v", m.screen)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateAbsent {
		t.Fatalf("State() = %v, %v, want the vault untouched until the offer is answered", state, err)
	}
	if strings.Contains(screenOf(m), secretValue) {
		t.Errorf("the typed secret reached the screen:\n%s", screenOf(m))
	}

	// Leaving it empty from here on is answering it by skipping: the secret is still stored, unencrypted.
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("skipping after a mismatch reported %q", m.fail)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateUnencrypted {
		t.Fatalf("State() = %v, %v, want the vault unencrypted once skipped", state, err)
	}
}

// Skipping the offer, by leaving it empty, stores the secret in an unencrypted vault, the same as
// 'qatlas credential set' answered the same way on the terminal.
func TestVaultOfferSkippedLeavesTheVaultUnencrypted(t *testing.T) {
	const secretValue = "canary-skipped-2e88"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addVaultCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	typeText(t, m, secretValue)
	pump(t, m, "enter")
	if m.screen != screenVaultOffer {
		t.Fatalf("the offer did not open: screen %v", m.screen)
	}

	pump(t, m, "enter") // nothing typed: the offer is declined
	if m.fail != "" {
		t.Fatalf("skipping the offer reported %q", m.fail)
	}
	if m.screen != screenForm {
		t.Fatalf("screen = %v, want back on the form", m.screen)
	}
	if !strings.HasPrefix(m.status, "Stored") {
		t.Errorf("status = %q, want the write confirmed", m.status)
	}

	fresh := vault.New(dir)
	state, err := fresh.State()
	if err != nil || state != vault.StateUnencrypted {
		t.Fatalf("State() = %v, %v, want an unencrypted vault", state, err)
	}
	got, found, _, err := fresh.Get("reader", "token-id", nil)
	if err != nil || !found || got != secretValue {
		t.Errorf("Get() = %q, %v, %v, want the value just stored", got, found, err)
	}
}

// esc on the offer cancels the whole pending write: nothing reaches the vault, and the form is left as it
// was before s was pressed.
func TestVaultOfferCancelledWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addVaultCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	typeText(t, m, "canary-cancelled-offer")
	pump(t, m, "enter")
	if m.screen != screenVaultOffer {
		t.Fatalf("the offer did not open: screen %v", m.screen)
	}

	press(t, m, "esc")
	if m.screen != screenForm || m.vaultOffer != nil {
		t.Fatalf("esc did not cancel the offer cleanly: screen %v, offer %v", m.screen, m.vaultOffer)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateAbsent {
		t.Fatalf("State() = %v, %v, want the vault untouched", state, err)
	}
}

// vaultBlockingSecrets wraps a real vault-backed resolver and holds every vault write open until the test
// releases it, so a test can catch the exact moment one is in flight. Every other call, Vault and State
// included, goes straight to the embedded resolver: a real, already-seeded vault directory, so it answers
// genuinely and fast, never anything this wrapper fakes.
type vaultBlockingSecrets struct {
	*secret.Resolver
	gate chan struct{}
}

func (b vaultBlockingSecrets) SetVault(credential, role, value string, offer vault.PassphraseFunc) error {
	<-b.gate
	return b.Resolver.SetVault(credential, role, value, offer)
}

func (b vaultBlockingSecrets) SetVaultUndoable(credential, role, value string, offer vault.PassphraseFunc) (func() error, error) {
	<-b.gate
	return b.Resolver.SetVaultUndoable(credential, role, value, offer)
}

// The scrypt cost of a vault write, or of the offer that may lead to one, never blocks the event loop, and
// a second s or x on a vault role while one is in flight is refused rather than started.
func TestVaultWriteIsAsyncAndNotDoubleTriggered(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	// Seed an unrelated entry so the vault is already unencrypted: the write under test is not the vault's
	// first secret, and the blocking wrapper below exercises the write itself, not the offer.
	mustNoError(t, secrets.SetVault("other", "role", "canary-seed", nil))
	blocking := vaultBlockingSecrets{Resolver: secrets, gate: make(chan struct{})}

	m, err := buildModel(store, nil, blocking, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addVaultCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	typeText(t, m, "canary-async-4f10")
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("submitting the vault secret produced no command")
	}
	if !m.vaultBusy {
		t.Fatal("the write in flight does not set vaultBusy")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	if m.busy == "" || !strings.Contains(m.busy, "vault") {
		t.Errorf("busy = %q, want the vault write reported", m.busy)
	}
	if !strings.Contains(screenOf(m), "storing the secret") {
		t.Errorf("view does not show the running write:\n%s", screenOf(m))
	}

	// The event loop stays responsive while the write is in flight.
	press(t, m, "esc")
	openSectionByName(t, m, sectionServices)
	press(t, m, "down", "up")
	if m.screen != screenList || m.section != sectionServices {
		t.Fatalf("the editor stopped reacting: screen %v section %v", m.screen, m.section)
	}

	// A second attempt on another vault role, while the first write is still open, is refused rather than
	// started.
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "enter")
	focusRole(t, m, "token-secret")
	press(t, m, "s")
	if m.screen == screenSecret {
		t.Fatal("a second vault write started while the first was still in flight")
	}

	close(blocking.gate)
	select {
	case msg := <-done:
		m.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("the vault write never finished")
	}
	if m.fail != "" {
		t.Errorf("the finished write reported %q", m.fail)
	}
	if m.vaultBusy {
		t.Error("vaultBusy stays set after the write finished")
	}

	// Now that it is free, the guard lets the next write through.
	focusRole(t, m, "token-secret")
	press(t, m, "s")
	if m.screen != screenSecret {
		t.Fatalf("the guard still blocks after the write finished: screen %v", m.screen)
	}
}

// x on a vault role, while the vault is locked, opens the same combined unlock-and-admin dialog every
// managing action opens on a locked vault: a wrong passphrase reopens it with the error shown and changes
// nothing, esc cancels the whole pending delete without unlocking or removing anything, and the right
// passphrase unlocks the vault and this window's admin session together, then removes the entry at once.
func TestVaultRoleDeleteUnlocksTheVaultThenRemoves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)

	// One vault, encrypted with a real passphrase and holding one entry.
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-locked-4d21",
		func(string) (string, error) { return "hunter2", nil }))

	// The model's own resolver is a fresh vault.Vault over the same directory: nothing here ever unlocked
	// it, so it genuinely starts locked, the way a freshly started 'qatlas tui' would find one.
	locked, _ := newVaultResolver(t, dir)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader",
		config.Credential{Provider: "bookstack", Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))

	m, err := buildModel(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	editEntry(t, m, "reader")
	focusRole(t, m, "token-id")
	press(t, m, "x")
	if m.screen != screenConfirm {
		t.Fatalf("x did not ask to confirm: screen %v", m.screen)
	}
	press(t, m, "y")
	if m.screen != screenAdminAuth || m.adminAuth == nil || !m.adminAuth.locked {
		t.Fatalf("confirming a delete on a locked vault did not open the unlock dialog: screen %v", m.screen)
	}

	// A wrong passphrase reopens the same dialog with the error shown, and changes nothing.
	typeText(t, m, "wrong")
	pump(t, m, "enter")
	if !strings.Contains(m.fail, "wrong passphrase") || m.screen != screenAdminAuth {
		t.Fatalf("a wrong passphrase = screen %v fail %q, want the dialog reopened with the error",
			m.screen, m.fail)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want the vault still locked after a wrong passphrase", state, err)
	}

	// esc cancels the whole pending delete: nothing is unlocked, nothing is removed, and the confirmation
	// itself is gone too, since a cancelled or abandoned check must not leave anything behind.
	press(t, m, "esc")
	if m.screen != screenForm || m.adminAuth != nil || m.confirmRole != "" {
		t.Fatalf("esc did not cancel cleanly: screen %v, dialog %v, confirmRole %q",
			m.screen, m.adminAuth, m.confirmRole)
	}
	fresh := vault.New(dir)
	if state, err := fresh.State(); err != nil || state != vault.StateLocked {
		t.Fatalf("State() = %v, %v, want the vault untouched by the cancelled attempt", state, err)
	}
	got, found, _, err := fresh.Get("reader", "token-id", func(string) (string, error) { return "hunter2", nil })
	if err != nil || !found || got != "canary-locked-4d21" {
		t.Errorf("Get() = %q, %v, %v, want the entry untouched by the cancelled attempt", got, found, err)
	}

	// The right passphrase unlocks the vault and admin mode together, then removes the entry at once.
	press(t, m, "x")
	press(t, m, "y")
	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("removing after unlocking reported %q", m.fail)
	}
	// A new vault.Vault, reading only from disk: fresh's own in-memory copy was cached by the read just
	// above and would otherwise still show the entry it read before this removal.
	if _, found, _, err := vault.New(dir).Get("reader", "token-id",
		func(string) (string, error) { return "hunter2", nil }); err != nil || found {
		t.Errorf("Get() found = %v, %v, want the entry removed", found, err)
	}
}
