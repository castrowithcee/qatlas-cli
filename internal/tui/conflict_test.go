package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A foreign writer stands for everything else that changes config.yaml while the editor is open. The browser
// editor commits through LoadVersioned and SaveIfUnchanged, another editor or an agent through Update.
type foreignWriter struct {
	name  string
	write func(t *testing.T, store *config.Store, service string)
}

var foreignWriters = []foreignWriter{
	{"another editor", func(t *testing.T, store *config.Store, service string) {
		t.Helper()
		cfg, rev, err := store.LoadVersioned()
		var notFound *config.NotFoundError
		if errors.As(err, &notFound) {
			cfg, err = store.New(), nil
		}
		if err != nil {
			t.Fatalf("LoadVersioned() = %v", err)
		}
		mustNoError(t, cfg.SetService(service, config.Service{Provider: "bookstack", BaseURL: "https://" + service + ".example.invalid"}))
		mustNoError(t, store.SaveIfUnchanged(cfg, rev))
	}},
	{"a parallel agent", func(t *testing.T, store *config.Store, service string) {
		t.Helper()
		mustNoError(t, store.Update(func(cfg *config.Config) error {
			return cfg.SetService(service, config.Service{Provider: "bookstack", BaseURL: "https://" + service + ".example.invalid"})
		}))
	}},
}

func secondEditor(t *testing.T, m *Model, store *config.Store) *Model {
	t.Helper()
	other, err := buildModel(store, nil, m.secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return other
}

func TestSaveConflictKeepsTheOtherChangeAndRepeatSucceeds(t *testing.T) {
	for _, writer := range foreignWriters {
		t.Run(writer.name, func(t *testing.T) {
			m, store, path := newModel(t)
			addService(t, m, "wiki", "https://wiki.example.invalid")
			if m.fail != "" {
				t.Fatalf("first save reported %q", m.fail)
			}
			// Opened on the state that has wiki only.
			openSectionByName(t, m, sectionServices)
			pressNew(t, m)
			typeText(t, m, "mine")
			press(t, m, "tab", "tab")
			typeText(t, m, "https://mine.example.invalid")

			writer.write(t, store, "theirs")

			press(t, m, "enter")
			if m.screen != screenForm || !strings.Contains(m.fail, "changed outside this editor") {
				t.Fatalf("screen %v fail %q, want the form kept with a conflict message", m.screen, m.fail)
			}
			if strings.Contains(m.fail, "example.invalid") {
				t.Errorf("the message carries configuration content: %q", m.fail)
			}
			saved := savedConfig(t, path)
			if _, ok := saved.Services["theirs"]; !ok {
				t.Fatal("the other change was lost")
			}
			if _, ok := saved.Services["mine"]; ok {
				t.Fatal("the conflicting change was written")
			}
			if _, ok := m.cfg.Services["theirs"]; !ok {
				t.Error("the editor was not reloaded")
			}
			if got := m.fieldValue("name"); got != "mine" {
				t.Errorf("the form lost its input: name = %q", got)
			}

			press(t, m, "enter")
			if m.fail != "" || !strings.HasPrefix(m.status, "Saved") {
				t.Fatalf("repeating failed: fail %q status %q", m.fail, m.status)
			}
			saved = savedConfig(t, path)
			for _, name := range []string{"wiki", "theirs", "mine"} {
				if _, ok := saved.Services[name]; !ok {
					t.Errorf("service %q is missing after the repeat", name)
				}
			}
		})
	}
}

func TestTwoEditorsSavingConflictAndTheSecondRepeats(t *testing.T) {
	m1, store, path := newModel(t)
	m2 := secondEditor(t, m1, store)
	addService(t, m1, "first", "https://first.example.invalid")
	addService(t, m2, "second", "https://second.example.invalid")
	if !strings.Contains(m2.fail, "changed outside this editor") {
		t.Fatalf("the second editor's fail = %q, want a conflict", m2.fail)
	}
	saved := savedConfig(t, path)
	if _, ok := saved.Services["first"]; !ok || len(saved.Services) != 1 {
		t.Fatalf("services = %v, want only the first editor's", saved.Services)
	}
	press(t, m2, "enter")
	if m2.fail != "" {
		t.Fatalf("repeating reported %q", m2.fail)
	}
	if saved = savedConfig(t, path); len(saved.Services) != 2 {
		t.Errorf("services = %v, want both", saved.Services)
	}
	// The first editor saved before and is conflicted now in turn: its base is the state it wrote.
	addService(t, m1, "third", "https://third.example.invalid")
	if !strings.Contains(m1.fail, "changed outside this editor") {
		t.Errorf("the first editor wrote over the second one's change: fail %q", m1.fail)
	}
}

func TestDeleteConflictKeepsTheOtherChangeAndRepeatSucceeds(t *testing.T) {
	for _, writer := range foreignWriters {
		t.Run(writer.name, func(t *testing.T) {
			m, store, path := newModel(t)
			addService(t, m, "wiki", "https://wiki.example.invalid")
			openSectionByName(t, m, sectionServices)
			press(t, m, "d")
			writer.write(t, store, "theirs")
			press(t, m, "y")
			if !strings.Contains(m.fail, "changed outside this editor") || m.screen != screenList {
				t.Fatalf("screen %v fail %q, want the list with a conflict message", m.screen, m.fail)
			}
			saved := savedConfig(t, path)
			if _, ok := saved.Services["wiki"]; !ok {
				t.Fatal("the conflicting delete was written")
			}
			if _, ok := saved.Services["theirs"]; !ok {
				t.Fatal("the other change was lost")
			}
			m.list.selectName("wiki")
			press(t, m, "d", "y")
			if m.fail != "" {
				t.Fatalf("repeating reported %q", m.fail)
			}
			saved = savedConfig(t, path)
			if _, ok := saved.Services["wiki"]; ok {
				t.Error("the repeated delete did not remove wiki")
			}
			if _, ok := saved.Services["theirs"]; !ok {
				t.Error("the repeated delete lost the other change")
			}
		})
	}
}

func TestVaultSettingsConflictKeepsTheOtherChange(t *testing.T) {
	m, store, path, _, _ := newStoreModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	openVaultSection(t, m)
	foreignWriters[1].write(t, store, "theirs")
	m.saveVault()
	if !strings.Contains(m.fail, "changed outside this editor") {
		t.Fatalf("fail = %q, want a conflict", m.fail)
	}
	if _, ok := savedConfig(t, path).Services["theirs"]; !ok {
		t.Fatal("the other change was lost")
	}
	m.clearMessages()
	m.saveVault()
	if m.fail != "" || m.status != "Saved" {
		t.Errorf("repeating: fail %q status %q", m.fail, m.status)
	}
}

func TestPayloadSaveConflictStoresNothingAndRepeatSucceeds(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "billing", storageKeyring, "")
	h.addField(t, "username", canaryPayload)
	foreignWriters[0].write(t, h.store, "theirs")
	h.press(t, "f2")
	if h.m.screen != screenForm || !strings.Contains(h.m.fail, "changed outside this editor") {
		t.Fatalf("screen %v fail %q, want the form kept with a conflict message", h.m.screen, h.m.fail)
	}
	if _, err := h.mem.Get(context.Background(), secret.StoreKey("billing", "username")); err == nil {
		t.Fatal("a secret was stored although the configuration conflicted")
	}
	h.press(t, "f2")
	if h.m.fail != "" {
		t.Fatalf("repeating reported %q", h.m.fail)
	}
	saved, err := loadTestConfig(t, h.path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := saved.Credentials["billing"]; !ok {
		t.Error("the repeat did not save the credential")
	}
	if _, ok := saved.Services["theirs"]; !ok {
		t.Error("the other change was lost")
	}
	if got, err := h.mem.Get(context.Background(), secret.StoreKey("billing", "username")); err != nil || got != canaryPayload {
		t.Errorf("the repeat did not store the secret: %v", err)
	}
	h.neverShown(t, canaryPayload)
}

func TestSetupConflictStoresNothingRebasesAndRepeatSucceeds(t *testing.T) {
	m, store, path, _, mem := newStoreModel(t)
	walkSetup(t, m, stepSummary)
	foreignWriters[0].write(t, store, "theirs")
	pump(t, m, "enter")
	if m.wizard.saved != "" || !strings.Contains(m.fail, "changed outside this editor") {
		t.Fatalf("saved %q fail %q, want a conflict", m.wizard.saved, m.fail)
	}
	assertNoStoredSecret(t, mem, filepath.Dir(path))
	if _, ok := savedConfig(t, path).Services["theirs"]; !ok {
		t.Fatal("the other change was lost")
	}
	pump(t, m, "enter")
	if m.fail != "" || m.wizard.saved == "" {
		t.Fatalf("repeating failed: %q", m.fail)
	}
	saved := savedConfig(t, path)
	if _, ok := saved.Services["theirs"]; !ok {
		t.Error("the repeat lost the other change")
	}
	if _, ok := saved.Credentials["reader"]; !ok {
		t.Error("the repeat did not save the credential")
	}
}

// failingSecrets stores the first role and refuses the second, so a commit has something to roll back.
type failingSecrets struct {
	Secrets
	sets int
}

func (f *failingSecrets) Set(credential, role, value string) error {
	f.sets++
	if f.sets > 1 {
		return errors.New("the keyring refused this value")
	}
	return f.Secrets.Set(credential, role, value)
}

func TestCommitSetupFailedSecretLeavesNothingBehind(t *testing.T) {
	_, store, path, secrets, mem := newStoreModel(t)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
	plan := setupPlan{
		credential: "reader", storage: storageKeyring, roles: []string{"token-id", "token-secret"},
		secrets: map[string]string{"token-id": "id-1", "token-secret": "sec-2"},
	}
	_, err := commitSetup(testService(store, &failingSecrets{Secrets: secrets}, nil), cfg, config.RevisionAbsent, plan, nil)
	if err == nil {
		t.Fatal("commitSetup() succeeded with a refusing keyring")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the configuration was written without its secrets")
	}
	assertNoStoredSecret(t, mem, filepath.Dir(path))
}

func TestCommitSetupConflictWritesNoSecret(t *testing.T) {
	_, store, path, secrets, mem := newStoreModel(t)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
	foreignWriters[0].write(t, store, "theirs")
	plan := setupPlan{
		credential: "reader", storage: storageKeyring, roles: []string{"token-id"},
		secrets: map[string]string{"token-id": "id-1"},
	}
	_, err := commitSetup(testService(store, secrets, nil), cfg, config.RevisionAbsent, plan, nil)
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("commitSetup() = %v, want a conflict", err)
	}
	assertNoStoredSecret(t, mem, filepath.Dir(path))
	if _, ok := savedConfig(t, path).Services["theirs"]; !ok {
		t.Error("the other change was lost")
	}
}

func TestVaultMigrateConflictKeepsTheOtherChangeAndRepeatSucceeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}))
	mustNoError(t, store.Save(cfg))
	writeLegacyCredentialsYAML(t, dir, map[string]map[string]string{"reader": {"token-id": "id-1"}})

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openVaultSection(t, m)
	focusRole(t, m, "migrate credentials.yaml")
	pump(t, m, "enter", "y")
	foreignWriters[1].write(t, store, "theirs")
	pump(t, m, "enter") // leave the first-secret passphrase empty

	if !strings.Contains(m.fail, "changed outside this editor") || m.screen != screenForm {
		t.Fatalf("screen %v fail %q, want the vault form with a conflict message", m.screen, m.fail)
	}
	saved, err := loadTestConfig(t, filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if saved.Credentials["reader"].Type != config.CredentialTypeKeyring {
		t.Error("the credential was switched although the configuration conflicted")
	}
	if _, ok := saved.Services["theirs"]; !ok {
		t.Error("the other change was lost")
	}
	if _, err := os.Stat(filepath.Join(dir, secret.FileName)); err != nil {
		t.Errorf("credentials.yaml is gone although nothing switched: %v", err)
	}

	m.clearMessages()
	focusRole(t, m, "migrate credentials.yaml")
	pump(t, m, "enter", "y")
	if m.fail != "" || !m.migrateDeleteConfirm {
		t.Fatalf("repeating: screen %v fail %q", m.screen, m.fail)
	}
	saved, err = loadTestConfig(t, filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if saved.Credentials["reader"].Type != config.CredentialTypeVault {
		t.Error("the repeat did not switch the credential")
	}
	if _, ok := saved.Services["theirs"]; !ok {
		t.Error("the repeat lost the other change")
	}
	if got, found, _, err := vault.New(dir).Get("reader", "token-id", nil); err != nil || !found || got != "id-1" {
		t.Errorf("vault Get() = %q, %v, %v", got, found, err)
	}
}
