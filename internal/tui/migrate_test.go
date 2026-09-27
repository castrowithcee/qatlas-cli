package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// writeLegacyCredentialsYAML writes several credentials, each with several roles, directly into a plaintext
// credentials.yaml, the way an earlier version of qatlas left one behind. Nothing in this build writes such
// a file any more; this stands in for that.
func writeLegacyCredentialsYAML(t *testing.T, dir string, entries map[string]map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\nallow_plaintext: true\ncredentials:\n")
	for name, roles := range entries {
		b.WriteString("  " + name + ":\n")
		for role, value := range roles {
			b.WriteString("    " + role + ": " + value + "\n")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, secret.FileName), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write legacy credentials.yaml: %v", err)
	}
}

// The migrate action is offered only while credentials.yaml exists and holds an entry, and disappears once
// it is gone.
func TestVaultMigrateRowOffersOnlyWithLegacyEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	if strings.Contains(screenOf(m), "migrate credentials.yaml") {
		t.Fatalf("the migrate action is offered without a legacy file:\n%s", screenOf(m))
	}

	writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{"reader": {"token-id": "canary-row-1"}})
	m2, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m2)
	if !strings.Contains(screenOf(m2), "migrate credentials.yaml") {
		t.Fatalf("the migrate action is not offered with a legacy file:\n%s", screenOf(m2))
	}
}

// The overview names every entry by credential and role alone, never a value, and lists which credential
// switches to the vault; cancelling it writes nothing and keeps the file.
func TestVaultMigrateOverviewCancelledWritesNothing(t *testing.T) {
	const canaryID = "canary-migrate-cancel-71aa"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
	mustNoError(t, store.Save(cfg))
	writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{"reader": {"token-id": canaryID}})

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "migrate credentials.yaml")

	var rendered strings.Builder
	pump(t, m, "enter")
	rendered.WriteString(screenOf(m))
	if m.screen != screenConfirm || !m.migratePlanConfirm {
		t.Fatalf("enter on the migrate action did not open the overview: screen %v fail %q", m.screen, m.fail)
	}
	for _, want := range []string{"reader.token-id", "switching"} {
		if !strings.Contains(rendered.String(), want) {
			t.Errorf("overview = %q, want it to contain %q", rendered.String(), want)
		}
	}
	if strings.Contains(rendered.String(), canaryID) {
		t.Errorf("the overview shows a value:\n%s", rendered.String())
	}

	press(t, m, "n")
	if m.screen != screenForm || m.migrate != nil {
		t.Fatalf("n did not cancel: screen %v migrate %v", m.screen, m.migrate)
	}
	if _, err := os.Stat(filepath.Join(dir, secret.FileName)); err != nil {
		t.Errorf("credentials.yaml was removed although the migration was cancelled: %v", err)
	}
	if state, err := vault.New(dir).State(); err != nil || state != vault.StateAbsent {
		t.Errorf("State() = %v, %v, want no vault written", state, err)
	}
}

// Confirming the overview writes every entry into the vault, switches the affected credential, offers the
// vault's first-secret passphrase exactly as storing one directly would, and a confirmed 'y' afterwards
// deletes credentials.yaml; the combined result names what moved and what switched.
func TestVaultMigrateWritesSwitchesAndDeletes(t *testing.T) {
	const canaryID, canarySecret = "canary-migrate-id-88b2", "canary-migrate-secret-3d9f"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
	mustNoError(t, store.Save(cfg))
	writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{
		"reader": {"token-id": canaryID, "token-secret": canarySecret},
	})

	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "migrate credentials.yaml")
	pump(t, m, "enter")
	if m.screen != screenConfirm || !m.migratePlanConfirm {
		t.Fatalf("did not open the overview: screen %v fail %q", m.screen, m.fail)
	}

	pump(t, m, "y") // confirm the plan; the vault is absent, so its first-secret offer opens next
	if m.screen != screenVaultOffer {
		t.Fatalf("confirming did not offer a passphrase for the vault's first secret: screen %v fail %q",
			m.screen, m.fail)
	}
	pump(t, m, "enter") // leave it empty: the vault stays unencrypted
	if m.fail != "" {
		t.Fatalf("writing the plan reported %q", m.fail)
	}
	if m.screen != screenConfirm || !m.migrateDeleteConfirm {
		t.Fatalf("did not reach the delete question: screen %v", m.screen)
	}
	if !strings.Contains(m.migrateResult, "migrated 2 entries") ||
		!strings.Contains(m.migrateResult, "switched to the vault: reader") {
		t.Errorf("result = %q, want the outcome named", m.migrateResult)
	}

	pump(t, m, "y") // delete credentials.yaml now
	if m.fail != "" {
		t.Fatalf("deleting the file reported %q", m.fail)
	}
	if _, err := os.Stat(filepath.Join(dir, secret.FileName)); !os.IsNotExist(err) {
		t.Errorf("credentials.yaml still exists: %v", err)
	}

	v := vault.New(dir)
	for _, want := range []struct{ role, value string }{
		{"token-id", canaryID}, {"token-secret", canarySecret},
	} {
		got, found, _, err := v.Get("reader", want.role, nil)
		if err != nil || !found || got != want.value {
			t.Errorf("Get(%q) = %q, %v, %v, want %q", want.role, got, found, err, want.value)
		}
	}
	if got := m.cfg.Credentials["reader"].Type; got != config.CredentialTypeVault {
		t.Errorf("the model's own config still shows type %q, want vault", got)
	}
	saved, err := loadTestConfig(t, filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if saved.Credentials["reader"].Type != config.CredentialTypeVault {
		t.Errorf("the saved config still shows the credential as keyring")
	}
}

// A locked, encrypted vault asks for its passphrase before the migration can even be planned, since
// planning has to read what the vault already holds; a wrong one reopens the same prompt, and declining the
// delete question afterwards keeps credentials.yaml.
func TestVaultMigrateWithALockedVaultAndDeclinedDelete(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-existing-9a1b",
		func(string) (string, error) { return passphrase, nil }))

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}))
	mustNoError(t, store.Save(cfg))
	writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{
		"reader": {"token-id": "legacy-value-must-not-win", "token-secret": "canary-new-role-4c1a"},
	})

	locked, _ := newVaultResolver(t, dir)
	m, err := New(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "migrate credentials.yaml")

	press(t, m, "enter")
	if m.screen != screenVaultOffer {
		t.Fatalf("did not ask for the vault's passphrase: screen %v", m.screen)
	}
	typeText(t, m, "not the real passphrase")
	pump(t, m, "enter")
	if m.fail != "wrong passphrase" || m.screen != screenVaultOffer {
		t.Fatalf("wrong passphrase = fail %q screen %v, want the same prompt reopened", m.fail, m.screen)
	}

	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.screen != screenConfirm || !m.migratePlanConfirm {
		t.Fatalf("the right passphrase did not reach the overview: screen %v fail %q", m.screen, m.fail)
	}
	// "reader" is already of type vault and already holds token-id: only the still-missing token-secret
	// moves, and the credential is never re-switched.
	view := screenOf(m)
	if !strings.Contains(view, "reader.token-secret") || strings.Contains(view, "reader.token-id") {
		t.Errorf("overview = %q, want only the still-missing role", view)
	}
	if strings.Contains(view, "switching") {
		t.Errorf("overview = %q, want no switch offered for a credential already of type vault", view)
	}

	pump(t, m, "y") // the vault is already encrypted: no passphrase offer, straight to the write
	if m.fail != "" {
		t.Fatalf("writing the plan reported %q", m.fail)
	}
	if m.screen != screenConfirm || !m.migrateDeleteConfirm {
		t.Fatalf("did not reach the delete question: screen %v", m.screen)
	}

	pump(t, m, "n") // keep credentials.yaml
	if _, err := os.Stat(filepath.Join(dir, secret.FileName)); err != nil {
		t.Errorf("credentials.yaml was removed although deleting it was declined: %v", err)
	}
	if !strings.Contains(m.status, "kept") {
		t.Errorf("status = %q, want it to say the file was kept", m.status)
	}

	got, found, _, err := vault.New(dir).Get("reader", "token-secret", func(string) (string, error) { return passphrase, nil })
	if err != nil || !found || got != "canary-new-role-4c1a" {
		t.Errorf("Get(token-secret) = %q, %v, %v, want the migrated value", got, found, err)
	}
}
