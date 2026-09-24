package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// walkSetup walks the guided setup of an empty configuration up to, not into, the given step, the way a
// person does: a new service, a new keyring credential with both secrets typed, a connection named personal.
func walkSetup(t *testing.T, m *Model, until int) {
	t.Helper()
	m.screen = screenNav
	press(t, m, "c")
	if m.wizard == nil || m.screen != screenProviders {
		t.Fatalf("c did not open the guided setup on its provider table: screen %v, error %q", m.screen, m.fail)
	}
	steps := []func(){
		func() { press(t, m, "enter") },
		func() {
			press(t, m, "tab")
			typeText(t, m, "wiki")
			press(t, m, "tab")
			typeText(t, m, "https://wiki.example.invalid")
			press(t, m, "enter")
		},
		func() {
			press(t, m, "tab")
			typeText(t, m, "reader")
			press(t, m, "tab", "tab")
			typeText(t, m, canaryID)
			press(t, m, "tab")
			typeText(t, m, canarySecret)
			press(t, m, "enter")
		},
		func() {
			clearField(t, m)
			typeText(t, m, "personal")
			press(t, m, "tab", "tab")
			typeText(t, m, "team wiki, read only")
			press(t, m, "enter")
		},
		func() { press(t, m, "ctrl+s") },
	}
	for step := 0; step < until; step++ {
		steps[step]()
		if m.fail != "" || m.wizard.step != step+1 {
			t.Fatalf("step %s did not advance: at %s, error %q", setupTitles[step], setupTitles[m.wizard.step],
				m.fail)
		}
	}
}

func assertNoStoredSecret(t *testing.T, mem *secret.MemoryStore, dir string) {
	t.Helper()
	for _, role := range []string{"token-id", "token-secret"} {
		if _, err := mem.Get(secret.StoreKey("reader", role)); !errors.Is(err, secret.ErrNoEntry) {
			t.Errorf("the credential store holds reader/%s: %v", role, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, secret.FileName)); err == nil {
		t.Error("a plaintext credential file was written")
	}
}

func assertNoCanary(t *testing.T, what, text string) {
	t.Helper()
	for _, value := range []string{canaryID, canarySecret} {
		if strings.Contains(text, value) {
			t.Errorf("%s exposed a secret value %q:\n%s", what, value, text)
		}
	}
}

// A person reaches a working keyring connection from an empty configuration without opening a section, and
// the secrets end up in the store only, never on screen or in the file.
func TestGuidedSetupFromAnEmptyConfiguration(t *testing.T) {
	m, store, path, _, mem := newStoreModel(t)
	var views strings.Builder
	record := func() { views.WriteString(screenOf(m) + "\n") }

	record()
	walkSetup(t, m, stepCredential)
	record()
	// The secrets are typed right here, masked; the rows of a variable name stay out of sight.
	press(t, m, "tab")
	typeText(t, m, "reader")
	press(t, m, "tab")
	if m.fieldValue(storageLabel) != storageKeyring {
		t.Errorf("the recommended storage is not preselected: %q", m.fieldValue(storageLabel))
	}
	// The keyring is named as this platform calls it, so it reads as something the machine already has.
	if words := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(words, secret.StoreLabel(platform)) {
		t.Errorf("the storage row does not name the platform keyring %q:\n%s", secret.StoreLabel(platform), screenOf(m))
	}
	press(t, m, "tab")
	typeText(t, m, canaryID)
	record()
	press(t, m, "tab")
	typeText(t, m, canarySecret)
	record()
	press(t, m, "enter")
	record()
	if m.wizard.step != stepScope {
		t.Fatalf("credential step did not advance: %q", m.fail)
	}
	if m.fieldValue("name") != "bookstack" {
		t.Errorf("the connection name is not suggested: %q", m.fieldValue("name"))
	}
	press(t, m, "enter")
	record()
	press(t, m, "ctrl+s")
	record()
	if m.screen != screenSummary {
		t.Fatalf("screen = %v, want the summary", m.screen)
	}
	summary := screenOf(m)
	for _, want := range []string{"wiki (new)", "reader (new) · system keyring: token-id, token-secret",
		"default: read"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not contain %q:\n%s", want, summary)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the configuration was written before the setup was saved")
	}
	assertNoStoredSecret(t, mem, filepath.Dir(path))

	pump(t, m, "enter")
	record()
	if m.fail != "" || m.wizard.saved != "bookstack" {
		t.Fatalf("save failed: %q", m.fail)
	}
	for role, want := range map[string]string{"token-id": canaryID, "token-secret": canarySecret} {
		if got, err := mem.Get(secret.StoreKey("reader", role)); err != nil || got != want {
			t.Errorf("stored reader/%s: err %v, match %v", role, err, got == want)
		}
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	cred := saved.Credentials["reader"]
	if cred.Type != config.CredentialTypeKeyring || cred.Provider != "bookstack" || len(cred.Values) != 0 {
		t.Errorf("credential = %+v", cred)
	}
	if conn := saved.Connections["bookstack"]; conn.Service != "wiki" || conn.Credential != "reader" ||
		conn.Permissions != nil || conn.Tools != nil {
		t.Errorf("connection = %+v", conn)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	assertNoCanary(t, "the configuration file", string(raw))
	assertNoCanary(t, "the editor", views.String())

	press(t, m, "enter")
	if m.wizard != nil || m.screen != screenList || m.section != sectionConnections {
		t.Fatalf("enter after saving did not open Connections: screen %v", m.screen)
	}
	if name, _ := m.selected(); name != "bookstack" {
		t.Errorf("selected = %q, want the new connection", name)
	}
}

// A configured service and credential are offered first and reused as they are, never copied.
func TestGuidedSetupReusesAServiceAndACredential(t *testing.T) {
	m, store, path, _, mem := newStoreModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")

	m.screen = screenNav
	press(t, m, "c", "enter")
	if m.fieldValue("service") != "wiki" || !m.field("name").hidden {
		t.Fatalf("service step does not offer the configured service first: %q", m.fieldValue("service"))
	}
	press(t, m, "ctrl+s")
	if m.fieldValue("credential") != "reader" {
		t.Fatalf("credential step does not offer the configured credential first: %q",
			m.fieldValue("credential"))
	}
	for _, f := range m.fields[1:] {
		if !f.hidden {
			t.Errorf("row %q of a new credential is shown while one is reused", f.label)
		}
	}
	press(t, m, "ctrl+s", "enter", "ctrl+s")
	if !strings.Contains(screenOf(m), "reader (existing, unchanged)") {
		t.Errorf("summary does not say the credential is reused:\n%s", screenOf(m))
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %q", m.fail)
	}

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(saved.Services) != 1 || len(saved.Credentials) != 1 || len(saved.Connections) != 1 {
		t.Fatalf("saved configuration = %+v", saved)
	}
	if conn := saved.Connections["bookstack"]; conn.Service != "wiki" || conn.Credential != "reader" {
		t.Errorf("connection = %+v", conn)
	}
	assertNoStoredSecret(t, mem, filepath.Dir(path))
}

// Leaving the setup at any step, the plaintext confirmation included, writes nothing anywhere.
func TestCancellingTheGuidedSetupWritesNothing(t *testing.T) {
	for step := stepProvider; step <= stepSummary; step++ {
		t.Run(setupTitles[step], func(t *testing.T) {
			m, _, path, _, mem := newStoreModel(t)
			walkSetup(t, m, step)
			press(t, m, "esc")
			if step > stepProvider {
				// Once a provider is chosen the setup holds input, and leaving it asks first.
				if m.screen != screenLeave || m.wizard == nil {
					t.Fatalf("esc did not ask before dropping the setup: screen %v", m.screen)
				}
				press(t, m, "d")
			}
			if m.wizard != nil || m.screen != screenList {
				t.Fatalf("esc did not leave the setup: screen %v", m.screen)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("the configuration was written")
			}
			assertNoStoredSecret(t, mem, filepath.Dir(path))
		})
	}

	t.Run("plaintext confirmation", func(t *testing.T) {
		m, _, path, _, mem := newStoreModel(t)
		walkSetup(t, m, stepCredential)
		press(t, m, "tab")
		typeText(t, m, "reader")
		press(t, m, "tab")
		selectChoice(t, m, storagePlaintext)
		press(t, m, "tab")
		typeText(t, m, canaryID)
		press(t, m, "tab")
		typeText(t, m, canarySecret)
		press(t, m, "enter")
		if m.screen != screenPlaintextConfirm {
			t.Fatalf("an unencrypted file was chosen without asking: screen %v", m.screen)
		}
		assertNoCanary(t, "the confirmation", screenOf(m))
		m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
		// The first escape closes the question and keeps the step; the next one asks before it drops the
		// setup.
		press(t, m, "esc")
		if m.screen != screenForm || m.wizard == nil || m.wizard.step != stepCredential {
			t.Fatalf("esc from a question too small did not return to its step: screen %v", m.screen)
		}
		press(t, m, "esc")
		if view := m.View(); m.screen != screenLeave || !strings.Contains(view, "d discard setup") {
			t.Fatalf("esc from a screen too small did not ask: screen %v\n%s", m.screen, view)
		}
		press(t, m, "d")
		if m.wizard != nil || m.screen != screenList {
			t.Fatalf("esc from a screen too small did not leave the setup: screen %v", m.screen)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("the configuration was written")
		}
		assertNoStoredSecret(t, mem, filepath.Dir(path))
	})
}

// A step the core refuses stays open with everything typed into it, and so do the steps before it.
func TestARefusedStepKeepsItsInput(t *testing.T) {
	m, _, path, _, _ := newStoreModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	m.screen = screenNav
	press(t, m, "c", "enter")
	selectChoice(t, m, newService)
	press(t, m, "tab")
	typeText(t, m, "wiki")
	press(t, m, "tab")
	typeText(t, m, "https://archive.example.invalid")
	press(t, m, "enter")
	if m.wizard.step != stepService || !strings.Contains(m.fail, "already exists") {
		t.Fatalf("a second service named wiki was accepted: step %d, error %q", m.wizard.step, m.fail)
	}
	press(t, m, "tab", "tab")
	clearField(t, m)
	typeText(t, m, "archive")
	press(t, m, "tab")
	clearField(t, m)
	typeText(t, m, "ftp://archive.example.invalid")
	press(t, m, "enter")
	if m.wizard.step != stepService || !strings.Contains(m.fail, "base_url") {
		t.Fatalf("the core did not refuse the base url in its step: step %d, error %q", m.wizard.step, m.fail)
	}
	if m.fieldValue("name") != "archive" || m.fieldValue("base url") != "ftp://archive.example.invalid" {
		t.Errorf("the refused step lost its input: %q %q", m.fieldValue("name"), m.fieldValue("base url"))
	}
	clearField(t, m)
	typeText(t, m, "https://archive.example.invalid")
	press(t, m, "enter")

	// A new keyring credential needs every secret of its provider.
	selectChoice(t, m, newCredential)
	press(t, m, "tab")
	typeText(t, m, "reader")
	press(t, m, "tab", "tab")
	typeText(t, m, canaryID)
	press(t, m, "enter")
	if m.wizard.step != stepCredential || !strings.Contains(m.fail, "token-secret is empty") {
		t.Fatalf("a missing secret was accepted: step %d, error %q", m.wizard.step, m.fail)
	}
	assertNoCanary(t, "the error", m.fail)
	assertNoCanary(t, "the refused step", screenOf(m))
	if m.fieldValue("name") != "reader" || m.fieldValue("token-id") != canaryID {
		t.Error("the refused credential step lost its input")
	}

	// Going back shows the confirmed step as it was, and coming forward again the refused one.
	press(t, m, "ctrl+b")
	if m.wizard.step != stepService || m.fieldValue("service") != newService ||
		m.fieldValue("name") != "archive" || m.fieldValue("base url") != "https://archive.example.invalid" {
		t.Fatalf("going back lost the service step: step %d name %q", m.wizard.step, m.fieldValue("name"))
	}
	press(t, m, "ctrl+s")
	if m.wizard.step != stepCredential || m.fieldValue("name") != "reader" || m.fieldValue("token-id") != canaryID {
		t.Fatalf("coming forward lost the credential step")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a refused step changed the configuration file")
	}
}

// A keyring that cannot be used leaves nothing behind, and neither does a configuration that cannot be
// written after the secrets were: those secrets are removed again, and every input stays for a retry.
func TestAFailedSaveLeavesNothingBehind(t *testing.T) {
	t.Run("keyring unavailable", func(t *testing.T) {
		m, _, path, _, mem := newStoreModel(t)
		walkSetup(t, m, stepSummary)
		mem.Fail(secret.ErrUnavailable)
		pump(t, m, "enter")
		mem.Fail(nil)
		if m.wizard.saved != "" || !strings.Contains(m.fail, "ctrl+b") {
			t.Fatalf("a failed keyring did not say how to go on: %q", m.fail)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("the configuration was written without its secrets")
		}
		assertNoStoredSecret(t, mem, filepath.Dir(path))
	})

	t.Run("configuration not writable", func(t *testing.T) {
		m, store, path, _, mem := newStoreModel(t)
		walkSetup(t, m, stepSummary)
		// A directory where the file belongs lets the core check pass and the write fail.
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		pump(t, m, "enter")
		if m.wizard.saved != "" || !strings.Contains(m.fail, "the configuration was not changed") {
			t.Fatalf("a failed write was not reported: %q", m.fail)
		}
		assertNoCanary(t, "the error", m.fail)
		assertNoStoredSecret(t, mem, filepath.Dir(path))

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		pump(t, m, "enter")
		if m.fail != "" || m.wizard.saved != "personal" {
			t.Fatalf("retry failed: %q", m.fail)
		}
		if _, err := store.Load(); err != nil {
			t.Errorf("Load() = %v", err)
		}
		if got, err := mem.Get(secret.StoreKey("reader", "token-id")); err != nil || got != canaryID {
			t.Errorf("the retried save did not store the secret: %v", err)
		}
	})
}

// Environment variables and the unencrypted file stay reachable, the file only after explicit consent.
func TestGuidedSetupOtherSecretSources(t *testing.T) {
	t.Run("environment variables", func(t *testing.T) {
		m, store, path, _, mem := newStoreModel(t)
		walkSetup(t, m, stepCredential)
		press(t, m, "tab")
		typeText(t, m, "reader")
		press(t, m, "tab")
		selectChoice(t, m, storageEnv)
		press(t, m, "tab")
		typeText(t, m, "WIKI_ID")
		press(t, m, "tab")
		typeText(t, m, "WIKI_SECRET")
		press(t, m, "enter", "enter", "ctrl+s")
		pump(t, m, "enter")
		if m.fail != "" {
			t.Fatalf("save failed: %q", m.fail)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		cred := saved.Credentials["reader"]
		if cred.Type != config.CredentialTypeEnv || cred.Values["token-id"] != "WIKI_ID" ||
			cred.Values["token-secret"] != "WIKI_SECRET" {
			t.Errorf("credential = %+v", cred)
		}
		assertNoStoredSecret(t, mem, filepath.Dir(path))
	})

	t.Run("unencrypted file", func(t *testing.T) {
		m, _, path, secrets, mem := newStoreModel(t)
		walkSetup(t, m, stepCredential)
		press(t, m, "tab")
		typeText(t, m, "reader")
		press(t, m, "tab")
		selectChoice(t, m, storagePlaintext)
		press(t, m, "tab")
		typeText(t, m, canaryID)
		press(t, m, "tab")
		typeText(t, m, canarySecret)
		press(t, m, "enter", "n")
		if m.screen != screenForm || m.wizard.step != stepCredential || m.fieldValue("name") != "reader" {
			t.Fatalf("no did not return to the credential step: screen %v", m.screen)
		}
		press(t, m, "enter", "y")
		if m.wizard.step != stepScope {
			t.Fatalf("yes did not continue: step %d, error %q", m.wizard.step, m.fail)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), secret.FileName)); err == nil {
			t.Fatal("the unencrypted file was written before the setup was saved")
		}
		press(t, m, "enter", "ctrl+s")
		if !strings.Contains(screenOf(m), "unencrypted") {
			t.Errorf("summary does not warn about the unencrypted file:\n%s", screenOf(m))
		}
		pump(t, m, "enter")
		if m.fail != "" {
			t.Fatalf("save failed: %q", m.fail)
		}
		if source, _ := secrets.Status("reader", config.Credential{Type: config.CredentialTypeKeyring},
			"token-secret"); source != secret.SourcePlaintext {
			t.Errorf("token-secret resolves from %q, want the plaintext file", source)
		}
		if _, err := mem.Get(secret.StoreKey("reader", "token-id")); !errors.Is(err, secret.ErrNoEntry) {
			t.Errorf("the credential store was written: %v", err)
		}
	})
}

// The saved connection is tested by the very tester the Connections list uses.
func TestGuidedSetupTestsWithTheSameTester(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newResolver(t, dir, nil)
	var calls []string
	tester := func(_ context.Context, name string) (provider.Class, error) {
		calls = append(calls, name)
		return provider.ClassAuth, nil
	}
	m, err := New(store, tester, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	walkSetup(t, m, stepSummary)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %q", m.fail)
	}
	pump(t, m, "t")
	if len(calls) != 1 || calls[0] != "personal" {
		t.Fatalf("tester calls = %v", calls)
	}
	if view := screenOf(m); !strings.Contains(view, "personal: auth") {
		t.Errorf("summary does not show the test result:\n%s", view)
	}

	press(t, m, "enter")
	runTest(t, m)
	if len(calls) != 2 || calls[1] != "personal" {
		t.Errorf("the Connections list did not test through the same tester: %v", calls)
	}
}

// The guided setup and the credential form ask where the secrets go with one and the same row.
func TestSetupAndEditorOfferTheSameStorageRow(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	walkSetup(t, m, stepCredential)
	setupRow := *m.field(storageLabel)

	e, _, _, _, _ := newStoreModel(t)
	openSectionByName(t, e, sectionCredentials)
	press(t, e, "n")
	editorRow := e.field(storageLabel)
	if editorRow == nil {
		t.Fatalf("the credential form has no %q row: %v", storageLabel, labelsOf(e.fields))
	}

	want := []string{storageKeyring, storageEnv, storagePlaintext}
	for what, row := range map[string]field{"setup": setupRow, "editor": *editorRow} {
		if !reflect.DeepEqual(row.choices, want) || row.value() != storageKeyring || row.hint != storageHint {
			t.Errorf("the %s row offers %v at %q with hint %q, want %v at %q with the shared hint", what,
				row.choices, row.value(), row.hint, want, storageKeyring)
		}
	}
}

// rawStorageTerm matches the file's type names where a screen should say where the secrets are kept.
var rawStorageTerm = regexp.MustCompile(`\benv\b|\btype:? (env|keyring)\b|< ?(env|keyring) ?>|\(type |` +
	`(^|[^m]) keyring \(recommended\)|\bp unencrypted`)

func assertNoRawStorageTerm(t *testing.T, what, view string) {
	t.Helper()
	if raw := rawStorageTerm.FindString(strings.Join(strings.Fields(view), " ")); raw != "" {
		t.Errorf("%s says %q:\n%s", what, raw, view)
	}
}

// Neither way to a credential names the storage by the type the file stores.
func TestStorageScreensSayWhereNotTheType(t *testing.T) {
	// The setup of a new credential, under every storage choice.
	m, _, _, _, _ := newStoreModel(t)
	walkSetup(t, m, stepCredential)
	focusField(t, m, storageLabel)
	for _, choice := range []string{storageKeyring, storageEnv, storagePlaintext} {
		selectChoice(t, m, choice)
		assertNoRawStorageTerm(t, "the setup credential step under "+choice, screenOf(m))
	}

	// The setup reusing a credential, and its summary.
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("store",
		config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	mustNoError(t, cfg.SetCredential("vars", config.Credential{Provider: "bookstack", Type: config.CredentialTypeEnv,
		Values: map[string]string{"token-id": "WIKI_ID", "token-secret": "WIKI_SECRET"}}))
	mustNoError(t, store.Save(cfg))
	for _, credential := range []string{"store", "vars"} {
		secrets, _ := newResolver(t, dir, nil)
		m, err := New(store, nil, secrets, nil)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		m.screen = screenNav
		press(t, m, "c", "enter", "ctrl+s")
		selectChoice(t, m, credential)
		view := screenOf(m)
		assertNoRawStorageTerm(t, "the setup reusing "+credential, view)
		want := map[string]string{"store": placeKeyring, "vars": placeEnv}[credential]
		if words := strings.Join(strings.Fields(view), " "); !strings.Contains(words, "("+want+") unchanged") {
			t.Errorf("reusing %s does not say %q:\n%s", credential, want, view)
		}
		press(t, m, "ctrl+s", "enter", "ctrl+s")
		if m.screen != screenSummary {
			t.Fatalf("screen = %v, want the summary: %q", m.screen, m.fail)
		}
		assertNoRawStorageTerm(t, "the summary reusing "+credential, screenOf(m))
	}

	// The credential form of a new credential, on its storage row and on a role row, and the list.
	e, _, _, _, _ := newStoreModel(t)
	openSectionByName(t, e, sectionCredentials)
	press(t, e, "n")
	for _, choice := range []string{storageKeyring, storageEnv, storagePlaintext} {
		focusField(t, e, storageLabel)
		selectChoice(t, e, choice)
		assertNoRawStorageTerm(t, "the credential form under "+choice, screenOf(e))
		focusRole(t, e, "token-id")
		assertNoRawStorageTerm(t, "the role row under "+choice, screenOf(e))
	}
	addCredential(t, e, "vars", "WIKI_ID", "WIKI_SECRET")
	addKeyringCredential(t, e, "store")
	assertNoRawStorageTerm(t, "the saved keyring credential form", screenOf(e))
	openSectionByName(t, e, sectionCredentials)
	assertNoRawStorageTerm(t, "the credential list", screenOf(e))
	if words := strings.Join(strings.Fields(screenOf(e)), " "); !strings.Contains(words, "store (none) "+placeKeyring) ||
		!strings.Contains(words, "vars (none) "+placeEnv) {
		t.Errorf("the credential list does not say where the secrets are kept:\n%s", screenOf(e))
	}
}

// Every storage choice leaves the same credential and the same secrets behind, whichever way it was made.
func TestSetupAndEditorStoreAlike(t *testing.T) {
	type outcome struct {
		cred    config.Credential
		sources []secret.Source
	}
	read := func(t *testing.T, path string, secrets *secret.Resolver) outcome {
		t.Helper()
		saved, err := loadTestConfig(t, path)
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		out := outcome{cred: saved.Credentials["reader"]}
		for _, role := range []string{"token-id", "token-secret"} {
			source, _ := secrets.Status("reader", out.cred, role)
			out.sources = append(out.sources, source)
		}
		return out
	}
	values := func(choice string) []string {
		if choice == storageEnv {
			return []string{"WIKI_ID", "WIKI_SECRET"}
		}
		return []string{canaryID, canarySecret}
	}

	for _, choice := range []string{storageKeyring, storageEnv, storagePlaintext} {
		t.Run(choice, func(t *testing.T) {
			// The guided setup.
			m, _, path, secrets, _ := newStoreModel(t)
			walkSetup(t, m, stepCredential)
			press(t, m, "tab")
			typeText(t, m, "reader")
			press(t, m, "tab")
			selectChoice(t, m, choice)
			for _, value := range values(choice) {
				press(t, m, "tab")
				typeText(t, m, value)
			}
			press(t, m, "enter")
			if choice == storagePlaintext {
				press(t, m, "y")
			}
			press(t, m, "enter", "ctrl+s")
			pump(t, m, "enter")
			if m.fail != "" {
				t.Fatalf("the setup reported %q", m.fail)
			}
			viaSetup := read(t, path, secrets)

			// The credential form.
			e, _, path, secrets, _ := newStoreModel(t)
			openSectionByName(t, e, sectionCredentials)
			press(t, e, "n")
			typeText(t, e, "reader")
			focusField(t, e, providerLabel)
			selectChoice(t, e, "bookstack")
			focusField(t, e, storageLabel)
			selectChoice(t, e, choice)
			if choice == storageEnv {
				for i, role := range []string{"token-id", "token-secret"} {
					focusRole(t, e, role)
					typeText(t, e, values(choice)[i])
				}
				pump(t, e, "enter")
			} else {
				pump(t, e, "ctrl+s")
				if got := e.fieldValue(storageLabel); got != choice {
					t.Fatalf("the saved credential reopened on %q, want %q", got, choice)
				}
				for i, role := range []string{"token-id", "token-secret"} {
					setSecret(t, e, role, values(choice)[i], choice == storagePlaintext)
				}
			}
			if e.fail != "" {
				t.Fatalf("the credential form reported %q", e.fail)
			}
			viaEditor := read(t, path, secrets)

			if !reflect.DeepEqual(viaSetup, viaEditor) {
				t.Errorf("setup left %+v, the credential form %+v", viaSetup, viaEditor)
			}
			want := map[string]secret.Source{
				storageKeyring: secret.SourceStore, storageEnv: secret.SourceMissing,
				storagePlaintext: secret.SourcePlaintext,
			}[choice]
			if viaSetup.cred.Type != storageType(choice) || viaSetup.sources[0] != want {
				t.Errorf("%s left %+v, want type %s resolving from %s", choice, viaSetup, storageType(choice), want)
			}
		})
	}
}
