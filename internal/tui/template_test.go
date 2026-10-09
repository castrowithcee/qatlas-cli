package tui

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// pressNew presses n and answers the template question, when it is asked, with "empty", the way a person
// starts a new entry from nothing.
func pressNew(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, "n")
	if m.screen == screenTemplate {
		press(t, m, "enter")
	}
}

// templateConnection has its own rights, tools, targets and description, none of which the recommended
// profile would have chosen.
var templateConnection = config.Connection{
	Service: "wiki", Credential: "reader", Target: "book/12", Description: "wiki for customer A",
	Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
	Tools:       []string{"bookstack.pages.list", "bookstack.pages.create"},
}

// templateModel is an editor over a service "wiki" with options, a credential "reader" and the connection
// "github-ops", with the real tool catalog of BookStack.
func templateModel(t *testing.T, more ...string) (*Model, string) {
	t.Helper()
	connections := map[string]config.Connection{"github-ops": templateConnection}
	for _, name := range more {
		connections[name] = config.Connection{Service: "wiki", Credential: "reader"}
	}
	m, path := toolsModel(t, wikiRegistry(t), connections)
	mustNoError(t, m.cfg.SetService("wiki", config.Service{
		Provider: "bookstack", BaseURL: "https://wiki.example.invalid", Options: map[string]string{"k": "v"},
	}))
	mustNoError(t, m.store.Save(m.cfg))
	syncRevision(t, m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, path
}

func savedConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path, wikiRegistry(t))
	mustNoError(t, err)
	return cfg
}

func TestCopyNameCountsUp(t *testing.T) {
	taken := map[string]bool{"a": true}
	if got := freeCopyName("a", taken); got != "a-copy" {
		t.Errorf("first copy = %q, want a-copy", got)
	}
	taken["a-copy"] = true
	if got := freeCopyName("a", taken); got != "a-copy-2" {
		t.Errorf("second copy = %q, want a-copy-2", got)
	}
	taken["a-copy-2"] = true
	if got := freeCopyName("a", taken); got != "a-copy-3" {
		t.Errorf("third copy = %q, want a-copy-3", got)
	}
}

// p duplicates a service into a new form: the name is the suggested copy, editable; everything else is the
// original's; saving leaves the original alone, and a second copy counts up.
func TestDuplicateServiceFromTheList(t *testing.T) {
	m, path := templateModel(t)
	openSectionByName(t, m, sectionServices)
	m.screen = screenList
	pump(t, m, "p")
	if m.screen != screenForm || m.editing != "" || m.copyFrom != "wiki" {
		t.Fatalf("p: screen %v editing %q copyFrom %q, want a new form copied from wiki", m.screen, m.editing,
			m.copyFrom)
	}
	if got := m.fieldValue("name"); got != "wiki-copy" || m.fields[0].readOnly {
		t.Errorf("name = %q readOnly %v, want an editable wiki-copy", got, m.fields[0].readOnly)
	}
	view := screenOf(m)
	for _, want := range []string{"New service, from wiki", "everything from wiki except the name"} {
		if !strings.Contains(view, want) {
			t.Errorf("the copied form does not show %q:\n%s", want, view)
		}
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving the copy reported %q", m.fail)
	}
	saved := savedConfig(t, path)
	if got := saved.Services["wiki-copy"]; got.BaseURL != "https://wiki.example.invalid" ||
		got.Provider != "bookstack" || !reflect.DeepEqual(got.Options, map[string]string{"k": "v"}) {
		t.Errorf("copied service = %+v", got)
	}
	if _, ok := saved.Services["wiki"]; !ok {
		t.Error("the original service is gone")
	}
	m.list.selectName("wiki")
	pump(t, m, "p")
	if got := m.fieldValue("name"); got != "wiki-copy-2" {
		t.Errorf("second copy name = %q, want wiki-copy-2", got)
	}
}

// A copied credential keeps where its secrets are kept, and stores no secret: nothing is written to the
// credential store under the new name, the original's secret stays, and the form says the value is to be set.
func TestDuplicateCredentialCopiesNoSecret(t *testing.T) {
	m, _, path, _, mem := newStoreModel(t)
	mustNoError(t, m.cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, m.cfg.SetCredential("reader", config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	mustNoError(t, m.store.Save(m.cfg))
	syncRevision(t, m)
	mustNoError(t, mem.Set(secret.StoreKey("reader", "token-id"), "canary-original-id"))
	mustNoError(t, mem.Set(secret.StoreKey("reader", "token-secret"), "canary-original-secret"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	openSectionByName(t, m, sectionCredentials)
	m.screen = screenList
	pump(t, m, "p")
	if m.screen != screenForm || m.fieldValue("name") != "reader-copy" {
		t.Fatalf("p: screen %v name %q, want the copied credential form", m.screen, m.fieldValue("name"))
	}
	if got := m.fieldValue(storageLabel); got != storageKeyring {
		t.Errorf("storage = %q, want %q", got, storageKeyring)
	}
	if view := screenOf(m); !strings.Contains(view, "never a secret value") || strings.Contains(view, "canary-") {
		t.Errorf("the copied credential form must say no value is copied and show none:\n%s", view)
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving the copy reported %q", m.fail)
	}
	saved := savedConfig(t, path)
	if got := saved.Credentials["reader-copy"]; got.Type != config.CredentialTypeKeyring || got.Provider != "bookstack" {
		t.Errorf("copied credential = %+v", got)
	}
	for _, role := range []string{"token-id", "token-secret"} {
		if value, err := mem.Get(context.Background(), secret.StoreKey("reader-copy", role)); !errors.Is(err, secret.ErrNoEntry) {
			t.Errorf("secret %s of the copy = %q, %v; want no entry", role, value, err)
		}
		if _, err := mem.Get(context.Background(), secret.StoreKey("reader", role)); err != nil {
			t.Errorf("the original's secret %s changed: %v", role, err)
		}
	}
	// The saved copy opens for its secrets to be set, each of them still missing.
	if m.screen != screenForm || m.editing != "reader-copy" {
		t.Errorf("after saving: screen %v editing %q, want the copy's form open for its secrets", m.screen,
			m.editing)
	}
}

// A copied connection keeps provider, service, credential, targets, description, rights and tools, plus
// paths and files, and is not reset to the recommended profile.
func TestDuplicateConnectionKeepsToolsAndPaths(t *testing.T) {
	m, path := templateModel(t)
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList
	pump(t, m, "p")
	if m.screen != screenForm || m.fieldValue("name") != "github-ops-copy" {
		t.Fatalf("p: screen %v name %q", m.screen, m.fieldValue("name"))
	}
	if got := m.fieldValue(toolsLabel); got != toolsSelected {
		t.Errorf("tools mode = %q, want %q kept", got, toolsSelected)
	}
	view := screenOf(m)
	for _, want := range []string{"New connection, from github-ops", "wiki for customer A"} {
		if !strings.Contains(view, want) {
			t.Errorf("the copied form does not show %q:\n%s", want, view)
		}
	}
	clearField(t, m)
	typeText(t, m, "kunde-b")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving the copy reported %q", m.fail)
	}
	saved := savedConfig(t, path)
	got, want := saved.Connections["kunde-b"], saved.Connections["github-ops"]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("copy = %+v, want everything of the original: %+v", got, want)
	}
	if _, ok := saved.Connections["github-ops"]; !ok || len(saved.Connections) != 2 {
		t.Errorf("connections = %v, want the original and the copy", slices.Sorted(mapsKeys(saved.Connections)))
	}
}

func mapsKeys(m map[string]config.Connection) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// Saving a duplicate is a managing action: against an encrypted vault without an admin session it opens the
// admin dialog first and writes nothing before the passphrase.
func TestDuplicateSavesOnlyInAnAdminSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, "hunter2", true)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, store.Save(cfg))
	m.cfg = cfg
	syncRevision(t, m)
	openSectionByName(t, m, sectionServices)
	m.screen = screenList
	pump(t, m, "p")
	if m.screen != screenForm {
		t.Fatalf("p: screen %v, want the form; opening a copy is reading", m.screen)
	}
	pump(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Fatalf("saving the copy: screen %v, want the admin dialog", m.screen)
	}
	loaded, err := loadTestConfig(t, filepath.Join(dir, "config.yaml"))
	mustNoError(t, err)
	if _, ok := loaded.Services["wiki-copy"]; ok {
		t.Fatal("the copy was written before the admin passphrase")
	}
	unlockWithAdminPassphrase(t, m, "hunter2")
	loaded, err = loadTestConfig(t, filepath.Join(dir, "config.yaml"))
	mustNoError(t, err)
	if _, ok := loaded.Services["wiki-copy"]; !ok {
		t.Errorf("the copy was not saved after the passphrase; fail %q", m.fail)
	}
}

// n asks first. Empty opens the ordinary new form; "from existing" opens a picker with search, and enter
// on a match opens the copy. esc steps back out.
func TestNewAsksEmptyOrFromExistingWithSearch(t *testing.T) {
	m, _ := templateModel(t, "alpha", "beta", "gamma")
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList

	press(t, m, "n")
	if m.screen != screenTemplate || m.template == nil || m.template.picking {
		t.Fatalf("n: screen %v, want the question", m.screen)
	}
	view := screenOf(m)
	for _, want := range []string{"New connection", "(•) empty", "( ) from existing connection...",
		"up/down move", "enter choose", "esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not show %q:\n%s", want, view)
		}
	}
	press(t, m, "esc")
	if m.screen != screenList || m.template != nil {
		t.Fatalf("esc: screen %v, want the list", m.screen)
	}

	press(t, m, "n", "enter")
	if m.screen != screenForm || m.copyFrom != "" || m.fieldValue("name") != "" {
		t.Fatalf("empty: screen %v copyFrom %q name %q, want an empty new form", m.screen, m.copyFrom,
			m.fieldValue("name"))
	}
	press(t, m, "esc")
	m.screen = screenList

	press(t, m, "n", "down", "enter")
	if !m.template.picking {
		t.Fatal("enter on from existing did not open the picker")
	}
	if view := screenOf(m); !strings.Contains(view, "4 total") || !strings.Contains(view, "search:") {
		t.Errorf("the picker does not show its count and search:\n%s", view)
	}
	typeText(t, m, "gam")
	if !slices.Equal(m.templateList.matches, []string{"gamma"}) {
		t.Fatalf("search gam matches %v, want [gamma]", m.templateList.matches)
	}
	press(t, m, "esc")
	if m.screen != screenTemplate || m.template.picking {
		t.Fatalf("esc in the picker: screen %v picking %v, want the question", m.screen, m.template.picking)
	}
	press(t, m, "enter")
	typeText(t, m, "gam")
	press(t, m, "enter")
	if m.screen != screenForm || m.copyFrom != "gamma" || m.fieldValue("name") != "gamma-copy" {
		t.Fatalf("picked: screen %v copyFrom %q name %q, want the copy of gamma", m.screen, m.copyFrom,
			m.fieldValue("name"))
	}
}

// Without an entry to copy n goes straight to the empty form.
func TestNewWithoutEntriesSkipsTheQuestion(t *testing.T) {
	m, _, _ := newModel(t)
	mustNoError(t, m.cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, m.store.Save(m.cfg))
	syncRevision(t, m)
	openSectionByName(t, m, sectionCredentials)
	m.screen = screenList
	press(t, m, "n")
	if m.screen != screenForm {
		t.Errorf("n in an empty list: screen %v, want the form", m.screen)
	}
}

// A token copy takes vorbilder and only an expiry that still lies ahead, under a new name, and creating it
// goes through the same admin-only create as a new token.
func TestDuplicateTokenTakesVorbilderAndFutureExpiry(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	future := time.Date(2099, 12, 1, 0, 0, 0, 0, time.Local)
	past := time.Now().Add(-time.Hour)
	fixtureToken(t, v, "kunde-a", &future, "personal")
	fixtureToken(t, v, "alt", &past, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	openTokensUnlocked(t, m)

	m.list.selectName("kunde-a")
	pump(t, m, "p")
	if m.screen != screenForm || m.copyFrom != "kunde-a" {
		t.Fatalf("p: screen %v copyFrom %q, want the token form copied from kunde-a", m.screen, m.copyFrom)
	}
	if m.fieldValue("name") != "kunde-a-copy" || m.fieldValue(vorbilderLabel) != "personal" ||
		m.fieldValue(expiresLabel) != "2099-12-01" {
		t.Errorf("form = %q %q %q, want kunde-a-copy, personal, 2099-12-01", m.fieldValue("name"),
			m.fieldValue(vorbilderLabel), m.fieldValue(expiresLabel))
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("creating the copy reported %q", m.fail)
	}
	assertNoTokenValue(t, m)
	press(t, m, "esc")

	m.list.selectName("alt")
	pump(t, m, "p")
	if m.screen != screenForm || m.fieldValue(expiresLabel) != "" {
		t.Errorf("copy of an expired token: screen %v expires %q, want none", m.screen, m.fieldValue(expiresLabel))
	}
	press(t, m, "esc")

	check := openApprovalModel(t, store, dir)
	_ = check
	tokens, err := v.Tokens()
	mustNoError(t, err)
	var names []string
	values := map[string]bool{}
	for _, token := range tokens {
		names = append(names, token.Name)
		values[token.Value] = true
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"alt", "kunde-a", "kunde-a-copy"}) || len(values) != 3 {
		t.Errorf("tokens = %v with %d distinct values, want three tokens with their own values", names, len(values))
	}
}

// The guided setup can start from a connection: it begins past the provider step with the service,
// credential, scope, rights and tools of that connection, keeps them instead of the recommended profile, and
// saves a new connection under a copy name.
func TestSetupStartsFromAnExistingConnection(t *testing.T) {
	m, path := templateModel(t)
	m.screen = screenNav

	press(t, m, "c")
	if m.screen != screenTemplate || !m.template.setup {
		t.Fatalf("c: screen %v, want the template question of the setup", m.screen)
	}
	press(t, m, "down", "enter", "enter")
	if m.wizard == nil || m.wizard.template != "github-ops" || m.wizard.step != stepService {
		t.Fatalf("after choosing: wizard %+v, want the service step from github-ops", m.wizard)
	}
	if m.wizard.provider != "bookstack" || m.fieldValue("service") != "wiki" {
		t.Fatalf("provider %q service %q, want bookstack and wiki", m.wizard.provider, m.fieldValue("service"))
	}
	press(t, m, "f2")
	if m.wizard.step != stepCredential || m.fieldValue("credential") != "reader" {
		t.Fatalf("step %d credential %q fail %q, want reader", m.wizard.step, m.fieldValue("credential"), m.fail)
	}
	press(t, m, "f2")
	if m.wizard.step != stepScope || m.fieldValue("name") != "github-ops-copy" ||
		m.fieldValue("description") != "wiki for customer A" ||
		!slices.Equal(targetEntries(m.fields), []string{"book/12"}) {
		t.Fatalf("scope step: name %q description %q targets %v", m.fieldValue("name"),
			m.fieldValue("description"), targetEntries(m.fields))
	}
	press(t, m, "f2")
	if m.wizard.step != stepPermissions {
		t.Fatalf("step %d fail %q, want permissions", m.wizard.step, m.fail)
	}
	if m.fieldValue("permissions") != "read, create" || m.fieldValue(toolsLabel) != toolsSelected ||
		m.fieldValue(toolListLabel) != "bookstack.pages.list, bookstack.pages.create" {
		t.Fatalf("permissions %q tools mode %q tools %q, want those of the original (no recommended profile)",
			m.fieldValue("permissions"), m.fieldValue(toolsLabel), m.fieldValue(toolListLabel))
	}
	press(t, m, "f2")
	if m.screen != screenSummary {
		t.Fatalf("screen %v fail %q, want the summary", m.screen, m.fail)
	}
	pump(t, m, "f2")
	if m.fail != "" || m.wizard == nil || m.wizard.saved != "github-ops-copy" {
		t.Fatalf("saving failed: fail %q", m.fail)
	}
	saved := savedConfig(t, path)
	got, want := saved.Connections["github-ops-copy"], saved.Connections["github-ops"]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setup copy = %+v, want %+v", got, want)
	}
}

// Without a connection c starts the setup at once, as before.
func TestSetupWithoutConnectionsSkipsTheQuestion(t *testing.T) {
	m, _, _ := newModel(t)
	m.screen = screenNav
	press(t, m, "c")
	if m.screen != screenProviders || m.wizard == nil || m.wizard.template != "" {
		t.Errorf("c: screen %v, want the provider table", m.screen)
	}
}

// Every new screen fits a narrow terminal, and the copied form names its source on its own line.
func TestTemplateScreensFitANarrowTerminal(t *testing.T) {
	m, _ := templateModel(t)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList

	press(t, m, "n")
	view := screenOf(m)
	assertViewFits(t, view, 60, 30)
	if !strings.Contains(view, "from existing") || !strings.Contains(view, "esc cancel") {
		t.Errorf("the narrow question is incomplete:\n%s", view)
	}
	press(t, m, "down", "enter")
	view = screenOf(m)
	assertViewFits(t, view, 60, 30)
	if !strings.Contains(view, "github-ops") {
		t.Errorf("the narrow picker does not list the connection:\n%s", view)
	}
	press(t, m, "enter")
	view = screenOf(m)
	assertViewFits(t, view, 60, 30)
	for _, want := range []string{"\nfrom github-ops", "github-ops-copy"} {
		if !strings.Contains(view, want) {
			t.Errorf("the narrow copied form does not show %q:\n%s", want, view)
		}
	}
}
