package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Canary values prove that a secret in the environment never reaches the editor or the file.
const (
	canaryID     = "canary-token-id-4f21"
	canarySecret = "canary-token-secret-9ab3"
)

func testCatalog(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "bookstack", Name: "BookStack",
		SecretRoles: []config.SecretRole{
			{Name: "token-id", Description: "BookStack token ID: the value labeled Token ID when you create an API token; it is not a name you choose"},
			{Name: "token-secret", Description: "BookStack token secret: the value labeled Token Secret when you create the same API token"},
		},
		Target: config.TargetMetadata{Label: "target", Description: "optional provider-specific scope inside the service"},
	}, nil); err != nil {
		t.Fatalf("register test provider: %v", err)
	}
	return reg
}

func newTestStore(t *testing.T, path string) *config.Store {
	t.Helper()
	return config.NewStore(path, testCatalog(t))
}

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return config.New(testCatalog(t))
}

func loadTestConfig(t *testing.T, path string) (*config.Config, error) {
	t.Helper()
	return config.Load(path, testCatalog(t))
}

func newModel(t *testing.T) (*Model, *config.Store, string) {
	t.Helper()
	return newEnvModel(t, nil)
}

// newEnvModel builds an editor that sees exactly the environment the test names.
func newEnvModel(t *testing.T, env map[string]string) (*Model, *config.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)

	secrets, _ := newResolver(t, dir, env)
	model, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return model, store, path
}

// newResolver builds a resolver over a credential store that lives in this process only, a plaintext
// fallback inside the test's own directory, and exactly the environment the test names.
//
// The environment is pinned rather than inherited: a variable that happens to be exported where the suite
// runs must not decide what these tests see, and the derived names of this project are guessable enough
// that someone will have one set. No test ever touches the credential store of the machine either.
func newResolver(t *testing.T, dir string, env map[string]string) (*secret.Resolver, *secret.MemoryStore) {
	t.Helper()
	store := secret.NewMemoryStore()
	file := secret.NewFile(filepath.Join(dir, secret.FileName))
	return secret.NewWith(func(name string) string { return env[name] }, store, file, nil), store
}

// keyMsg is the event a terminal sends for one key.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// press sends key events the way a terminal would.
func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		m.Update(keyMsg(k))
	}
}

// pump presses keys and delivers the messages of the commands they produce, the way the event loop does.
// It is how a test drives everything that reaches the credential store.
func pump(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, cmd := m.Update(keyMsg(k))
		for i := 0; cmd != nil && i < 8; i++ {
			msg := cmd()
			if msg == nil {
				break
			}
			_, cmd = m.Update(msg)
		}
	}
}

// typeText enters a value into the focused field, one rune at a time.
func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// clearField sends backspaces to the focused text field.
func clearField(t *testing.T, m *Model) {
	t.Helper()
	for i := 0; i < 64; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
}

// openSectionByName navigates from the sidebar to a section.
func openSectionByName(t *testing.T, m *Model, s section) {
	t.Helper()
	m.screen = screenNav
	pump(t, m, string(rune('1'+s)))
	if m.section != s {
		t.Fatalf("section = %v, want %v", m.section, s)
	}
}

// addService walks the real key sequence for creating a service.
func addService(t *testing.T, m *Model, name, baseURL string) {
	t.Helper()
	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	typeText(t, m, name)
	press(t, m, "tab") // provider, the only choice is preselected
	press(t, m, "tab")
	typeText(t, m, baseURL)
	press(t, m, "enter")
}

// addCredential walks the real key sequence for a credential of type env: name, type, one variable name
// per role.
func addCredential(t *testing.T, m *Model, name string, envNames ...string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	typeText(t, m, name)
	// A credential may name its provider; these helpers leave that choice empty, which is what an entry
	// written before the field looks like, and then every compiled role is offered.
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, config.CredentialTypeEnv)
	for _, env := range envNames {
		press(t, m, "tab")
		typeText(t, m, env)
	}
	press(t, m, "enter")
}

// addKeyringCredential creates a credential whose secrets live in the credential store. It names nothing:
// the roles are filled afterwards, through the masked prompt.
func addKeyringCredential(t *testing.T, m *Model, name string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	typeText(t, m, name)
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, config.CredentialTypeKeyring)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("creating the keyring credential reported %q", m.fail)
	}
}

func addConnection(t *testing.T, m *Model, name, service, credential string) {
	t.Helper()
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, name)
	// The provider row stands between the name and the service; it narrows both rows below it, and the
	// service the caller asks for names the provider it belongs to.
	press(t, m, "tab")
	selectChoice(t, m, m.cfg.Services[service].Provider)
	press(t, m, "tab")
	selectChoice(t, m, service)
	press(t, m, "tab")
	selectChoice(t, m, credential)
	press(t, m, "enter")
}

func selectChoice(t *testing.T, m *Model, want string) {
	t.Helper()
	f := &m.fields[m.focus]
	if f.kind == fieldProvider {
		chooseProvider(t, m, want)
		return
	}
	for i := 0; i < len(f.choices)+1; i++ {
		if f.value() == want {
			return
		}
		press(t, m, "right")
	}
	t.Fatalf("choice %q not reachable in %v", want, f.choices)
}

// chooseProvider opens the table of the focused provider row and takes the wanted provider by moving down
// to it, the way a person without a search term does.
func chooseProvider(t *testing.T, m *Model, want string) {
	t.Helper()
	press(t, m, "enter")
	if m.screen != screenProviders {
		t.Fatalf("enter on the provider row opened screen %v, want the provider table", m.screen)
	}
	press(t, m, "home")
	for range m.providers.list.matches {
		if id, _ := m.providers.list.selected(); id == want {
			press(t, m, "enter")
			return
		}
		press(t, m, "down")
	}
	t.Fatalf("provider %q not in the table %v", want, m.providers.list.matches)
}

// The whole MVP configuration flow works through key events alone.
func TestFullConfigurationFlow(t *testing.T) {
	m, store, path := newModel(t)

	addService(t, m, "wiki-primary", "https://wiki.example.invalid")
	addService(t, m, "wiki-archive", "https://archive.example.invalid")
	addCredential(t, m, "reader", "WIKI_READER_ID", "WIKI_READER_SECRET")
	addCredential(t, m, "auditor", "WIKI_AUDITOR_ID", "WIKI_AUDITOR_SECRET")
	// Two credentials on the same service.
	addConnection(t, m, "wiki", "wiki-primary", "reader")
	addConnection(t, m, "wiki-audit", "wiki-primary", "auditor")
	addConnection(t, m, "archive", "wiki-archive", "reader")

	openSectionByName(t, m, sectionDefaults)
	press(t, m, "n")
	typeText(t, m, "knowledge")
	press(t, m, "tab")
	selectChoice(t, m, "wiki")
	press(t, m, "enter")

	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(saved.Services) != 2 || len(saved.Credentials) != 2 || len(saved.Connections) != 3 {
		t.Fatalf("saved configuration = %+v", saved)
	}
	if saved.Connections["wiki"].Service != saved.Connections["wiki-audit"].Service {
		t.Error("the two connections should share one service")
	}
	if saved.Connections["wiki"].Credential == saved.Connections["wiki-audit"].Credential {
		t.Error("the two connections should use different credentials")
	}
	if saved.Defaults.Connections["knowledge"] != "wiki" {
		t.Errorf("default = %q, want wiki", saved.Defaults.Connections["knowledge"])
	}

	// The file itself must be loadable by the ordinary loader.
	if _, err := loadTestConfig(t, path); err != nil {
		t.Errorf("the saved file does not load: %v", err)
	}
}

func TestNavigation(t *testing.T) {
	m, _, _ := newModel(t)

	t.Run("the editor opens on the sidebar with Services shown beside it", func(t *testing.T) {
		if m.screen != screenNav || m.section != sectionServices {
			t.Fatalf("start = screen %v section %v, want the sidebar on Services", m.screen, m.section)
		}
	})

	t.Run("the sidebar wraps in both directions and shows each section at once", func(t *testing.T) {
		m.screen, m.section = screenNav, sectionServices
		press(t, m, "up")
		if m.section != sectionDefaults || m.screen != screenNav {
			t.Errorf("up = section %v screen %v, want Defaults on the sidebar", m.section, m.screen)
		}
		press(t, m, "down")
		if m.section != sectionServices {
			t.Errorf("down = section %v, want Services", m.section)
		}
		for _, key := range []string{"j", "k", "shift+tab"} {
			before := m.section
			press(t, m, key)
			if m.section == before || m.screen != screenNav {
				t.Errorf("%q did not move the sidebar: section %v screen %v", key, m.section, m.screen)
			}
		}
	})

	t.Run("enter, right and tab hand the focus to the list and back", func(t *testing.T) {
		for _, key := range []string{"enter", "right", "l", "tab"} {
			m.screen, m.section = screenNav, sectionCredentials
			press(t, m, key)
			if m.screen != screenList || m.section != sectionCredentials {
				t.Errorf("%q = screen %v section %v, want the Credentials list", key, m.screen, m.section)
			}
		}
		for _, key := range []string{"left", "h", "tab", "shift+tab", "esc"} {
			m.screen = screenList
			press(t, m, key)
			if m.screen != screenNav || m.section != sectionCredentials {
				t.Errorf("%q from the list = screen %v section %v, want the sidebar on Credentials",
					key, m.screen, m.section)
			}
		}
	})

	t.Run("escape on the sidebar neither quits nor moves", func(t *testing.T) {
		m.screen, m.section = screenNav, sectionConnections
		press(t, m, "esc", "esc")
		if m.quitting || m.screen != screenNav || m.section != sectionConnections {
			t.Errorf("esc on the sidebar = quitting %v screen %v section %v", m.quitting, m.screen, m.section)
		}
	})

	t.Run("form focus wraps in both directions", func(t *testing.T) {
		openSectionByName(t, m, sectionServices)
		press(t, m, "n")
		last := len(m.fields) - 1
		press(t, m, "shift+tab")
		if m.focus != last {
			t.Errorf("focus = %d, want %d", m.focus, last)
		}
		press(t, m, "tab")
		if m.focus != 0 {
			t.Errorf("focus = %d, want 0", m.focus)
		}
	})
}

// sectionLabels are how the sidebar names the four sections.
var sectionLabels = []string{"1 Services", "2 Credentials", "3 Connections", "4 Defaults"}

func TestLayoutsFitTheirTerminal(t *testing.T) {
	m, _, _ := newEnvModel(t, map[string]string{
		"WIKI_ID":     canaryID,
		"WIKI_SECRET": canarySecret,
	})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "personal", "wiki", "reader")
	openSectionByName(t, m, sectionServices)

	tests := []struct {
		name          string
		width, height int
		sidebar       bool
		nav           []string
	}{
		{name: "sidebar beside the workspace", width: 100, height: 28, sidebar: true, nav: sectionLabels},
		{name: "sidebar in a standard terminal", width: 80, height: 24, sidebar: true, nav: sectionLabels},
		{name: "one navigation line above the workspace", width: 60, height: 24,
			nav: []string{"[1 Services]", "2 Credentials", "3 Connections", "4 Defaults"}},
		{name: "compact navigation line", width: 40, height: 12, nav: []string{"[1 Services] 2 3 4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			view := m.View()
			assertViewFits(t, view, tt.width, tt.height)
			if got := len(strings.Split(view, "\n")); tt.sidebar && got != tt.height {
				t.Errorf("the frame has %d lines, want the full height %d", got, tt.height)
			}
			for _, want := range append([]string{"Services  1/1", "wiki"}, tt.nav...) {
				if !strings.Contains(view, want) {
					t.Errorf("view does not contain %q:\n%s", want, view)
				}
			}
			lines := strings.Split(view, "\n")
			if tt.sidebar {
				// The sidebar and the workspace share the lines: the row of a section is also a row of the
				// workspace frame.
				shared := false
				for _, line := range lines {
					if strings.Contains(line, "2 Credentials") && strings.Count(line, "║") == 2 {
						shared = true
					}
				}
				if !shared {
					t.Errorf("no line holds both the sidebar and the workspace:\n%s", view)
				}
			} else if !strings.Contains(lines[1], tt.nav[0]) || !strings.Contains(lines[2], "─") {
				t.Errorf("the navigation line does not stand above a rule and the workspace:\n%s", view)
			}
			for _, secretValue := range []string{canaryID, canarySecret} {
				if strings.Contains(view, secretValue) {
					t.Errorf("view exposed a secret value %q:\n%s", secretValue, view)
				}
			}
		})
	}
}

// Every section is one key away from the list and from an unchanged form; none of it goes through escape.
func TestDirectSectionKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	openSectionByName(t, m, sectionServices)

	for _, want := range []section{sectionCredentials, sectionConnections, sectionDefaults, sectionServices} {
		press(t, m, string(rune('1'+want)))
		if m.screen != screenList || m.section != want {
			t.Fatalf("%d from the list = screen %v section %v", want+1, m.screen, m.section)
		}
	}

	// In a form the digits are text; alt with a digit is the section key there.
	press(t, m, "n")
	typeText(t, m, "2")
	if m.section != sectionServices || m.fieldValue("name") != "2" {
		t.Fatalf("a digit escaped the form: section %v name %q", m.section, m.fieldValue("name"))
	}
	press(t, m, "tab")
	clearField(t, m)
	press(t, m, "shift+tab")
	clearField(t, m)

	// An unchanged form, new or opened, is left at once.
	for _, want := range []section{sectionCredentials, sectionConnections, sectionDefaults} {
		openSectionByName(t, m, sectionServices)
		press(t, m, "enter")
		if m.screen != screenForm || m.editing != "wiki" {
			t.Fatalf("enter did not open wiki: screen %v", m.screen)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + want)}, Alt: true})
		if m.screen != screenList || m.section != want {
			t.Errorf("alt+%d from an unchanged form = screen %v section %v", want+1, m.screen, m.section)
		}
	}

	// The sidebar answers the digits as well, and n there opens the form of the section it stands on.
	m.screen = screenNav
	press(t, m, "3")
	if m.screen != screenList || m.section != sectionConnections {
		t.Fatalf("3 on the sidebar = screen %v section %v", m.screen, m.section)
	}
	press(t, m, "n")
	if m.screen != screenList || !strings.Contains(m.fail, "Create a credential before adding a connection") {
		t.Errorf("new without prerequisites: screen %v error %q", m.screen, m.fail)
	}

	// The guided setup is one key away from the list and from the sidebar, and a setup without a provider
	// yet closes with one escape.
	for _, from := range []screen{screenList, screenNav} {
		m.screen = from
		press(t, m, "c")
		if m.wizard == nil || m.screen != screenProviders {
			t.Fatalf("c from screen %v opened screen %v, want the setup's provider table", from, m.screen)
		}
		press(t, m, "esc")
		if m.wizard != nil || m.screen != screenList || m.section != sectionConnections {
			t.Fatalf("esc from a fresh setup = screen %v section %v", m.screen, m.section)
		}
	}
}

// A new setup runs from Services over Credentials to Connections with section keys only: no dashboard and
// no escape in between.
func TestRebuildWithoutADashboard(t *testing.T) {
	m, store, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	screens := map[screen]bool{}
	step := func(keys ...string) {
		t.Helper()
		for _, key := range keys {
			if key == "esc" {
				t.Fatal("the walk pressed escape")
			}
			pump(t, m, key)
			screens[m.screen] = true
		}
	}

	step("n")
	typeText(t, m, "wiki")
	step("tab", "tab")
	typeText(t, m, "https://wiki.example.invalid")
	step("enter", "2", "n")
	typeText(t, m, "reader")
	step("tab", "tab")
	selectChoice(t, m, config.CredentialTypeEnv)
	step("tab")
	typeText(t, m, "WIKI_ID")
	step("tab")
	typeText(t, m, "WIKI_SECRET")
	step("enter", "3", "n")
	typeText(t, m, "wiki")
	step("enter")

	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	if m.screen != screenList || m.section != sectionConnections {
		t.Fatalf("the walk ended on screen %v section %v", m.screen, m.section)
	}
	if screens[screenNav] {
		t.Error("the walk passed through the sidebar although only section keys were pressed")
	}
	saved, err := store.Load()
	if err != nil || len(saved.Services) != 1 || len(saved.Credentials) != 1 || len(saved.Connections) != 1 {
		t.Fatalf("saved configuration = %+v, %v", saved, err)
	}
	if view := m.View(); !strings.Contains(view, "wiki  wiki / reader") ||
		!strings.Contains(view, "1 Services     1") {
		t.Errorf("the workspace or the sidebar does not show the result:\n%s", view)
	}
}

// Unsaved input is neither lost nor saved behind the user's back when a form is left.
func TestLeavingAChangedFormAsksFirst(t *testing.T) {
	startForm := func(t *testing.T) (*Model, string) {
		t.Helper()
		m, _, path := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		openSectionByName(t, m, sectionServices)
		press(t, m, "n")
		typeText(t, m, "wiki")
		press(t, m, "tab", "tab")
		typeText(t, m, "https://wiki.example.invalid")
		return m, path
	}
	altKey := func(s section) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + s)}, Alt: true}
	}

	t.Run("stay keeps every input and writes nothing", func(t *testing.T) {
		m, path := startForm(t)
		m.Update(altKey(sectionCredentials))
		if m.screen != screenLeave {
			t.Fatalf("alt+2 on a changed form = screen %v, want the leave question", m.screen)
		}
		view := m.View()
		for _, want := range []string{"warning: unsaved changes", "s save and go on", "d discard", "esc keep editing"} {
			if !strings.Contains(view, want) {
				t.Errorf("the leave question does not say %q:\n%s", want, view)
			}
		}
		// Keys that are no answer change nothing.
		press(t, m, "enter", "x", "2", "q", "n", "y")
		if m.screen != screenLeave || m.quitting {
			t.Fatalf("a stray key answered the question: screen %v quitting %v", m.screen, m.quitting)
		}
		press(t, m, "esc")
		if m.screen != screenForm || m.section != sectionServices || m.fieldValue("name") != "wiki" ||
			m.fieldValue("base url") != "https://wiki.example.invalid" {
			t.Fatalf("staying lost the form: screen %v section %v name %q", m.screen, m.section,
				m.fieldValue("name"))
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("staying wrote the file")
		}
	})

	t.Run("discard drops the input and goes on", func(t *testing.T) {
		m, path := startForm(t)
		m.Update(altKey(sectionConnections))
		press(t, m, "d")
		if m.screen != screenList || m.section != sectionConnections {
			t.Fatalf("discard = screen %v section %v, want Connections", m.screen, m.section)
		}
		if _, ok := m.cfg.Services["wiki"]; ok {
			t.Error("the discarded entry reached the model")
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("discarding wrote the file")
		}
		if !strings.Contains(m.status, "discarded") {
			t.Errorf("status = %q, want it to say the changes were discarded", m.status)
		}
	})

	t.Run("save writes through the core and goes on", func(t *testing.T) {
		m, _ := startForm(t)
		m.Update(altKey(sectionCredentials))
		pump(t, m, "s")
		if m.fail != "" || m.screen != screenList || m.section != sectionCredentials {
			t.Fatalf("save = screen %v section %v error %q", m.screen, m.section, m.fail)
		}
		if _, ok := m.cfg.Services["wiki"]; !ok || !strings.Contains(m.status, "Saved wiki") {
			t.Errorf("the service was not saved: status %q", m.status)
		}
	})

	t.Run("a refused save stays in the form", func(t *testing.T) {
		m, path := startForm(t)
		press(t, m, "shift+tab", "shift+tab")
		clearField(t, m)
		m.Update(altKey(sectionCredentials))
		pump(t, m, "s")
		if m.screen != screenForm || m.section != sectionServices || m.fail == "" {
			t.Fatalf("a refused save = screen %v section %v error %q", m.screen, m.section, m.fail)
		}
		if m.fieldValue("base url") != "https://wiki.example.invalid" {
			t.Errorf("the refused save lost the input")
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("a refused save wrote the file")
		}
	})

	t.Run("escape asks the same question", func(t *testing.T) {
		m, _ := startForm(t)
		press(t, m, "esc")
		if m.screen != screenLeave || m.leaveTo != -1 {
			t.Fatalf("esc on a changed form = screen %v", m.screen)
		}
		if view := m.View(); !strings.Contains(view, "the Services list") {
			t.Errorf("the question does not say where escape leads:\n%s", view)
		}
		press(t, m, "d")
		if m.screen != screenList || m.section != sectionServices {
			t.Errorf("discard after esc = screen %v section %v", m.screen, m.section)
		}
	})

	t.Run("an unfinished guided setup can only be kept or dropped", func(t *testing.T) {
		m, _, path := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		walkSetup(t, m, 1)
		m.Update(altKey(sectionCredentials))
		if m.screen != screenLeave {
			t.Fatalf("alt+2 in the setup = screen %v, want the leave question", m.screen)
		}
		if view := m.View(); strings.Contains(view, "s save") || !strings.Contains(view, "d discard setup") {
			t.Errorf("the setup question offers the wrong answers:\n%s", view)
		}
		press(t, m, "s")
		if m.screen != screenLeave {
			t.Fatalf("s answered the setup question: screen %v", m.screen)
		}
		press(t, m, "esc")
		if m.wizard == nil || m.screen != screenForm {
			t.Fatalf("staying dropped the setup: screen %v", m.screen)
		}
		m.Update(altKey(sectionCredentials))
		press(t, m, "d")
		if m.wizard != nil || m.screen != screenList || m.section != sectionCredentials {
			t.Fatalf("discard = wizard %v screen %v section %v", m.wizard != nil, m.screen, m.section)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("dropping the setup wrote the file")
		}
	})
}

// Colour only supports what markers, prefixes and borders already say.
func TestTheScreenReadsWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	m, _, _ := newModel(t)
	m.tester = func(context.Context, string) (provider.Class, error) { return provider.ClassOK, nil }
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")
	openSectionByName(t, m, sectionConnections)

	colour := before
	colour = 2 // termenv.ANSI
	lipgloss.SetColorProfile(colour)
	if view := m.View(); !strings.Contains(view, "\x1b[") {
		t.Fatalf("a colour terminal got no colour:\n%s", view)
	}
	colour = 3 // termenv.Ascii
	lipgloss.SetColorProfile(colour)

	view := m.View()
	if strings.Contains(view, "\x1b") {
		t.Fatalf("a terminal without colour got escape sequences:\n%q", view)
	}
	for _, want := range []string{"* 3 Connections", "> wiki", "╔", "╭"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list in focus does not show %q:\n%s", want, view)
		}
	}
	press(t, m, "left")
	view = m.View()
	if !strings.Contains(view, "> 3 Connections") || strings.Contains(view, "> wiki") ||
		strings.Count(view, "> ") != 1 {
		t.Errorf("the sidebar in focus is not the one marked line:\n%s", view)
	}

	press(t, m, "enter")
	pump(t, m, "t")
	if view := m.View(); !strings.Contains(view, "[ok] wiki") {
		t.Errorf("success has no marker:\n%s", view)
	}
	m.testClass = provider.ClassAuth
	if view := m.View(); !strings.Contains(view, "[failed] wiki: auth") {
		t.Errorf("a failed test has no marker:\n%s", view)
	}
	m.fail = "something went wrong"
	if view := m.View(); !strings.Contains(view, "error: something went wrong") {
		t.Errorf("an error has no prefix:\n%s", view)
	}
	m.clearMessages()
	press(t, m, "enter")
	focusField(t, m, "description")
	typeText(t, m, "x")
	press(t, m, "esc")
	if view := m.View(); !strings.Contains(view, "warning: unsaved changes") {
		t.Errorf("the warning has no prefix:\n%s", view)
	}
}

// Resizing changes the layout, never the state: the section, the selection, the filter and typed input
// survive every size, including one that cannot hold the screen.
func TestResizePreservesState(t *testing.T) {
	m, _, _ := newModel(t)
	for _, name := range []string{"alpha", "archive", "beta", "wiki"} {
		m.cfg.Services[name] = config.Service{Provider: "bookstack", BaseURL: "https://" + name + ".example.invalid"}
	}
	openSectionByName(t, m, sectionServices)
	press(t, m, "/")
	typeText(t, m, "a")
	press(t, m, "enter", "down")
	selected := selectedName(t, m)

	for _, size := range []struct{ width, height int }{{100, 28}, {60, 24}, {40, 12}, {20, 5}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		assertViewFits(t, m.View(), max(size.width, minimumWidth), max(size.height, minimumHeight))
		if m.screen != screenList || m.section != sectionServices || m.list.query() != "a" ||
			selectedName(t, m) != selected {
			t.Fatalf("%dx%d changed the list: screen %v section %v filter %q selected %q", size.width,
				size.height, m.screen, m.section, m.list.query(), selectedName(t, m))
		}
		if size.width >= minimumWidth && !strings.Contains(m.View(), "filter: a") {
			t.Errorf("%dx%d does not show the filter:\n%s", size.width, size.height, m.View())
		}
	}

	press(t, m, "enter")
	press(t, m, "tab", "tab")
	typeText(t, m, "/draft")
	focus, url := m.focus, m.fieldValue("base url")
	for _, size := range []struct{ width, height int }{{60, 24}, {40, 12}, {20, 5}, {100, 28}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.View()
		assertViewFits(t, view, max(size.width, minimumWidth), max(size.height, minimumHeight))
		if m.activeScreenTooSmall() {
			// A screen that cannot be shown takes no keys that change it.
			press(t, m, "tab", "n", "1")
			if !strings.Contains(view, "Resize terminal") {
				t.Errorf("%dx%d is too small for the form but shows no notice:\n%s", size.width, size.height, view)
			}
		} else if !strings.Contains(view, "/draft") {
			t.Errorf("%dx%d does not show the typed input:\n%s", size.width, size.height, view)
		}
		if m.screen != screenForm {
			t.Fatalf("%dx%d left the form: screen %v", size.width, size.height, m.screen)
		}
	}
	if m.focus != focus || m.fieldValue("base url") != url || m.section != sectionServices {
		t.Errorf("resizing changed the form: focus %d url %q section %v", m.focus, m.fieldValue("base url"),
			m.section)
	}
}

func TestTinyTerminalIsBoundedAndResizePreservesState(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionDefaults)
	press(t, m, "left")

	for _, size := range []struct{ width, height int }{
		{39, 12}, {40, 11}, {12, 4}, {1, 1}, {0, 0}, {-4, -2},
	} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.View()
		width, height := max(size.width, 1), max(size.height, 1)
		if size.width == 0 {
			width, height = 12, 4 // a zero dimension keeps the last one
		}
		assertViewFits(t, view, width, height)
		if !strings.Contains(view, "q") {
			t.Errorf("%dx%d resize view has no visible q key: %q", size.width, size.height, view)
		}
		press(t, m, "1", "n", "enter")
		if m.screen != screenNav || m.section != sectionDefaults {
			t.Fatalf("%dx%d changed hidden state to screen %v section %v", size.width, size.height,
				m.screen, m.section)
		}
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if m.screen != screenNav || m.section != sectionDefaults {
		t.Fatalf("resize lost the sidebar state: screen %v section %v", m.screen, m.section)
	}
	if nav := strings.Split(m.View(), "\n")[1]; !strings.HasPrefix(nav, "> ") || !strings.Contains(nav, "[4 Defaults]") {
		t.Errorf("restored navigation lost focus:\n%s", m.View())
	}

	// The notice is not a new editor screen. A half-completed form remains intact behind it.
	form, _, _ := newModel(t)
	press(t, form, "1", "n")
	typeText(t, form, "draft-service")
	press(t, form, "tab")
	focus := form.focus
	form.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	if view := form.View(); !strings.Contains(view, "esc leave") {
		t.Errorf("a tiny form view does not offer its way out:\n%s", view)
	}
	press(t, form, "tab", "n", "1", "q")
	form.Update(tea.WindowSizeMsg{Width: 80, Height: 100})
	if form.quitting || form.screen != screenForm || form.focus != focus || form.fieldValue("name") != "draft-service" {
		t.Errorf("resize changed form state: quitting %v screen %v focus %d name %q", form.quitting, form.screen,
			form.focus, form.fieldValue("name"))
	}
	if strings.Contains(form.View(), "Resize terminal") {
		t.Errorf("restored form still shows the resize notice:\n%s", form.View())
	}
}

// screenOf is the workspace as the user reads it, without the frame of the sidebar and the workspace
// around it, so a test can read a wrapped sentence without reading around the borders. The frame itself is
// tested on its own, through View.
func screenOf(m *Model) string {
	if m.quitting || m.terminalTooSmall() {
		return m.View()
	}
	return m.workspaceView()
}

func assertViewFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Errorf("view has %d lines, want at most %d:\n%s", len(lines), height, view)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("view line has %d cells, want at most %d: %q", got, width, line)
		}
	}
}

// Cancelling a form writes nothing.
func TestCancelWritesNothing(t *testing.T) {
	m, _, path := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	typeText(t, m, "discarded")
	// Leaving a changed form asks; discarding is the explicit answer.
	press(t, m, "esc", "d")

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Error("the file changed although the form was cancelled")
	}
	if _, ok := m.cfg.Services["discarded"]; ok {
		t.Error("the cancelled entry reached the model")
	}
	if m.status != "Changes discarded; nothing was written" {
		t.Errorf("status = %q", m.status)
	}
}

func TestValidationErrorsAreShown(t *testing.T) {
	tests := []struct {
		name   string
		build  func(t *testing.T, m *Model)
		wantIn string
	}{
		{
			name: "an empty name is refused",
			build: func(t *testing.T, m *Model) {
				openSectionByName(t, m, sectionServices)
				press(t, m, "n")
				press(t, m, "enter")
			},
			wantIn: "name must not be empty",
		},
		{
			name: "a missing base url is refused by the core",
			build: func(t *testing.T, m *Model) {
				openSectionByName(t, m, sectionServices)
				press(t, m, "n")
				typeText(t, m, "wiki")
				press(t, m, "enter")
			},
			wantIn: "base_url",
		},
		{
			name: "an unusable base url is refused by the core",
			build: func(t *testing.T, m *Model) {
				addService(t, m, "wiki", "ftp://wiki.example.invalid")
			},
			wantIn: "scheme http or https",
		},
		{
			name: "a credential without any role is refused by the core",
			build: func(t *testing.T, m *Model) {
				addCredential(t, m, "empty")
			},
			wantIn: "at least one secret role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, path := newModel(t)

			tt.build(t, m)

			if !strings.Contains(m.fail, tt.wantIn) {
				t.Errorf("error = %q, want it to contain %q", m.fail, tt.wantIn)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a file was written although the entry was invalid")
			}
			if m.screen != screenForm {
				t.Errorf("screen = %v, want to stay in the form", m.screen)
			}
		})
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")

	t.Run("answering no keeps the entry", func(t *testing.T) {
		openSectionByName(t, m, sectionServices)
		press(t, m, "d")
		if m.screen != screenConfirm {
			t.Fatalf("screen = %v, want the confirmation", m.screen)
		}
		press(t, m, "n")

		saved, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if _, ok := saved.Services["wiki"]; !ok {
			t.Error("the entry was deleted although the answer was no")
		}
	})

	t.Run("escape also keeps the entry", func(t *testing.T) {
		openSectionByName(t, m, sectionServices)
		press(t, m, "d")
		press(t, m, "esc")

		if _, ok := m.cfg.Services["wiki"]; !ok {
			t.Error("the entry was deleted although the confirmation was cancelled")
		}
	})

	t.Run("answering yes deletes the entry", func(t *testing.T) {
		openSectionByName(t, m, sectionServices)
		press(t, m, "d")
		press(t, m, "y")

		saved, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if _, ok := saved.Services["wiki"]; ok {
			t.Error("the entry survived the confirmation")
		}
		if !strings.HasPrefix(m.status, "Deleted") {
			t.Errorf("status = %q", m.status)
		}
	})
}

// A referenced entry cannot be deleted, and the reason is shown.
func TestDeleteRefusedWhileReferenced(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	openSectionByName(t, m, sectionServices)
	press(t, m, "d")
	press(t, m, "y")

	if !strings.Contains(m.fail, "still used by") {
		t.Errorf("error = %q", m.fail)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := saved.Services["wiki"]; !ok {
		t.Error("the referenced service was deleted")
	}
}

// The name of an existing entry cannot be changed, because renaming would break every reference to it.
func TestNameOfAnExistingEntryIsReadOnly(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")

	openSectionByName(t, m, sectionServices)
	press(t, m, "enter")
	if !m.fields[0].readOnly {
		t.Fatal("the name field of an existing entry should be read only")
	}
	// The field takes no editing focus either, so the keys of a rename attempt go somewhere else entirely
	// and the stored name cannot change by any route.
	if m.focus == 0 {
		t.Fatal("the form opened on the locked field")
	}
	clearField(t, m)
	typeText(t, m, "renamed")
	press(t, m, "enter")

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := saved.Services["wiki"]; !ok {
		t.Errorf("the entry lost its name: %+v", saved.Services)
	}
	if _, ok := saved.Services["renamed"]; ok {
		t.Error("a rename happened although the field is read only")
	}
}

// Neither the editor nor the file ever carries a secret value.
func TestNoSecretValues(t *testing.T) {
	m, _, path := newEnvModel(t, map[string]string{
		"WIKI_READER_ID":     canaryID,
		"WIKI_READER_SECRET": canarySecret,
	})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_READER_ID", "WIKI_READER_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	// Every screen the editor can show.
	var rendered strings.Builder
	for _, s := range []section{sectionServices, sectionCredentials, sectionConnections, sectionDefaults} {
		openSectionByName(t, m, s)
		rendered.WriteString(screenOf(m))
		press(t, m, "n")
		rendered.WriteString(screenOf(m))
		press(t, m, "esc")
	}
	openSectionByName(t, m, sectionCredentials)
	rendered.WriteString(screenOf(m))
	press(t, m, "enter")
	rendered.WriteString(screenOf(m))

	for _, canary := range []string{canaryID, canarySecret} {
		if strings.Contains(rendered.String(), canary) {
			t.Errorf("a secret value reached the screen:\n%s", rendered.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, canary := range []string{canaryID, canarySecret} {
		if strings.Contains(string(data), canary) {
			t.Errorf("a secret value reached the file:\n%s", data)
		}
	}
	// The variable names and the source that delivers are what the editor shows instead.
	if !strings.Contains(rendered.String(), "WIKI_READER_ID") ||
		!strings.Contains(rendered.String(), "("+string(secret.SourceEnv)+")") {
		t.Errorf("the editor should show the variable name and its source:\n%s", rendered.String())
	}
}

// A user may paste the secret itself into a credential field instead of the variable name. The editor
// refuses it, and the pasted value reaches no message, no other screen and no file.
func TestPastedSecretIsRefusedWithoutEchoingIt(t *testing.T) {
	const pasted = "canary-pasted-secret-6b28d4"

	m, _, path := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", pasted, "WIKI_READER_SECRET")

	if m.fail == "" {
		t.Fatal("the pasted secret was accepted, want a refusal")
	}
	if strings.Contains(m.fail, pasted) {
		t.Errorf("the error message echoes the pasted secret: %q", m.fail)
	}
	if !strings.Contains(m.fail, "token-id") {
		t.Errorf("error = %q, want it to name the role", m.fail)
	}
	if _, ok := m.cfg.Credentials["reader"]; ok {
		t.Error("the refused credential reached the model")
	}

	// Leave the form the way a user would, then render every screen the editor can show.
	press(t, m, "esc")
	var rendered strings.Builder
	for _, s := range []section{sectionServices, sectionCredentials, sectionConnections, sectionDefaults} {
		openSectionByName(t, m, s)
		rendered.WriteString(screenOf(m))
		press(t, m, "n")
		rendered.WriteString(screenOf(m))
		press(t, m, "esc")
		rendered.WriteString(screenOf(m))
	}
	if strings.Contains(rendered.String(), pasted) {
		t.Errorf("the pasted secret reached a screen:\n%s", rendered.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), pasted) {
		t.Errorf("the pasted secret reached the file:\n%s", data)
	}
}

// A name outside the allowed character set is refused, and the refusal states the rule.
func TestNameWithASpaceIsRefused(t *testing.T) {
	m, _, path := newModel(t)

	addService(t, m, "wiki primary", "https://wiki.example.invalid")

	if m.fail == "" {
		t.Fatal("a name with a space was accepted")
	}
	if !strings.Contains(m.fail, "must consist of letters, digits") {
		t.Errorf("error = %q, want it to state the name rule", m.fail)
	}
	if m.screen != screenForm {
		t.Errorf("screen = %v, want to stay in the form", m.screen)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file was written although the name was invalid")
	}
}

// The focused field is drawn by the text input, so the form shows where the cursor stands. The proof is
// that the rendering changes when nothing but the cursor moves: the plain value cannot depend on the cursor.
func TestTheFormShowsWhereTheCursorStands(t *testing.T) {
	const url = "https://wiki.example.invalid"

	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	press(t, m, "tab", "tab") // name, provider, base url
	typeText(t, m, url)

	if got := m.fields[m.focus].label; got != "base url" {
		t.Fatalf("focused field = %q, want the base url", got)
	}
	atEnd := screenOf(m)
	if !strings.Contains(atEnd, url) {
		t.Fatalf("the typed value is missing from the form:\n%s", atEnd)
	}
	// The cursor sits past the last character, so the text input draws one more cell after the value.
	if !strings.Contains(atEnd, url+" \n") {
		t.Errorf("no cursor cell behind the value, the field is drawn from the plain value:\n%s", atEnd)
	}

	press(t, m, "left", "left", "left")
	if got, want := m.fields[m.focus].input.Position(), len(url)-3; got != want {
		t.Fatalf("cursor position = %d, want %d", got, want)
	}
	inTheMiddle := screenOf(m)
	if inTheMiddle == atEnd {
		t.Errorf("the form is identical wherever the cursor stands:\n%s", inTheMiddle)
	}

	// A cursor that is only visible while a blink timer runs would vanish; this one is always drawn.
	if got := m.fields[m.focus].input.Cursor.Mode(); got != cursor.CursorStatic {
		t.Errorf("cursor mode = %v, want a static one", got)
	}
	if m.fields[m.focus].input.Cursor.Blink {
		t.Error("the cursor of the focused field is currently not drawn")
	}
	if !m.fields[0].input.Cursor.Blink {
		t.Error("an unfocused field should not draw a cursor")
	}
}

// The form shows what a field expects, above all that a credential field wants a variable name.
func TestFieldHintsSayWhatAFieldExpects(t *testing.T) {
	m, _, _ := newModel(t)

	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, config.CredentialTypeEnv)
	view := screenOf(m)
	words := strings.Join(strings.Fields(view), " ")
	for _, want := range []string{"the NAME of an environment variable", "never the secret"} {
		if !strings.Contains(words, want) {
			t.Errorf("the credential form does not say %q:\n%s", want, view)
		}
	}
	for _, want := range []string{"value labeled Token ID", "not a name you choose", "value labeled Token Secret"} {
		if !strings.Contains(words, want) {
			t.Errorf("the credential form does not explain %q:\n%s", want, view)
		}
	}
	// The sentence is about the kind of row, so it stands once and speaks of every role row. A hint is
	// wrapped into the terminal, so the test looks for a fragment that survives wrapping rather than for
	// the whole sentence.
	if got := strings.Count(view, "the NAME of an environment"); got != 1 {
		t.Errorf("hint appears %d times, want exactly once for all role rows:\n%s", got, view)
	}
	if !strings.Contains(view, "every role row") {
		t.Errorf("the hint does not say that it is about every role row:\n%s", view)
	}
	if !strings.Contains(view, "a key you choose, without spaces") {
		t.Errorf("the name field has no hint:\n%s", view)
	}
	if !strings.Contains(words, "keyring (recommended) keeps the secrets in the system keyring") {
		t.Errorf("the form does not say what the type decides:\n%s", view)
	}

	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	view = screenOf(m)
	for _, want := range []string{nameHint, baseURLHint} {
		if !strings.Contains(view, want) {
			t.Errorf("the service form does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "--connection") || !strings.Contains(view, "http or https") {
		t.Errorf("the hints do not say what name and base url are for:\n%s", view)
	}

	m, _, _ = newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")
	openSectionByName(t, m, sectionDefaults)
	press(t, m, "n")
	if view := screenOf(m); !strings.Contains(view, domainHint) {
		t.Errorf("the domain field has no hint:\n%s", view)
	}
}

// openEntryForm opens the form of the single entry of a section.
func openEntryForm(t *testing.T, m *Model, s section, name string) {
	t.Helper()
	openSectionByName(t, m, s)
	pump(t, m, "enter")
	if m.screen != screenForm || m.editing != name {
		t.Fatalf("screen = %v, editing = %q, want the form of %q", m.screen, m.editing, name)
	}
}

// A connection description is written, changed, and taken away again through the form alone, and what the
// store keeps is exactly what stood in the field. The field also says what makes it different from every
// other field of this editor: what is typed here is published by discovery.
func TestConnectionDescriptionIsEditedThroughTheForm(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Connections["wiki"].Description; got != "" {
		t.Errorf("a new connection starts with the description %q, want none", got)
	}

	for _, step := range []struct{ typed, want string }{
		{"the live team wiki, writes land here", "the live team wiki, writes land here"},
		{"the same instance, read for audits only", "the same instance, read for audits only"},
		{"", ""},
	} {
		openEntryForm(t, m, sectionConnections, "wiki")
		focusField(t, m, "description")
		clearField(t, m)
		typeText(t, m, step.typed)
		press(t, m, "enter")
		if m.fail != "" {
			t.Fatalf("editor reported %q", m.fail)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if got := saved.Connections["wiki"].Description; got != step.want {
			t.Errorf("saved description = %q, want %q", got, step.want)
		}
		// The route itself is untouched by a change to the text that describes it.
		if saved.Connections["wiki"].Service != "wiki" || saved.Connections["wiki"].Credential != "reader" {
			t.Errorf("the connection changed: %+v", saved.Connections["wiki"])
		}
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	for _, want := range []string{"description", "discovery publishes it", "never carry a secret"} {
		if !strings.Contains(view, want) {
			t.Errorf("the connection form does not say %q:\n%s", want, screenOf(m))
		}
	}
}

// A description the core refuses is reported in the editor and never reaches the file.
func TestATooLongConnectionDescriptionIsRefused(t *testing.T) {
	m, store, _ := newModel(t)
	// A line of 201 characters only fits into a terminal wide enough to draw it; the editor refuses to
	// work in one that cannot show what is being edited.
	m.Update(tea.WindowSizeMsg{Width: 400, Height: 200})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, "description")
	typeText(t, m, strings.Repeat("a", 201))
	press(t, m, "enter")

	if m.fail == "" || !strings.Contains(m.fail, "201 characters") {
		t.Errorf("fail = %q, want the reason the core gave", m.fail)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Connections["wiki"].Description; got != "" {
		t.Errorf("saved description = %q, want the refused text nowhere on disk", got)
	}
}

// A connection without a description is marked once another connection leads to the same provider, because
// an agent choosing between them sees names and descriptions only. The mark is a hint: saving, loading and
// every other action go on as before, and a description removes it.
func TestAnUndescribedConnectionBesideAnotherOfItsProviderIsMarked(t *testing.T) {
	m, store, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	listed := func() string {
		t.Helper()
		openSectionByName(t, m, sectionConnections)
		return strings.Join(strings.Fields(screenOf(m)), " ")
	}
	if view := listed(); strings.Contains(view, undescribedMarker) {
		t.Fatalf("a single connection is marked:\n%s", screenOf(m))
	}

	addConnection(t, m, "wiki-2", "wiki", "reader")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	view := listed()
	for _, want := range []string{"wiki wiki / reader " + undescribedMarker,
		"wiki-2 wiki / reader " + undescribedMarker, "sees only names and descriptions"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not say %q:\n%s", want, screenOf(m))
		}
	}
	for _, name := range []string{"wiki", "wiki-2"} {
		if got := m.describe(name); !strings.HasSuffix(got, undescribedMarker) {
			t.Errorf("list entry = %q, want it marked", got)
		}
	}
	if saved, err := store.Load(); err != nil || len(saved.Connections) != 2 {
		t.Fatalf("Load() = %v, %v; the mark must not keep a connection from being saved", saved, err)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, "description")
	typeText(t, m, "the live team wiki")
	press(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	if m.needsDescription("wiki") || !m.needsDescription("wiki-2") {
		t.Errorf("marks = wiki %v, wiki-2 %v; want only the connection without a description",
			m.needsDescription("wiki"), m.needsDescription("wiki-2"))
	}
	// A connection of another provider is no reason for a mark.
	m.cfg.Services["other"] = config.Service{Provider: "nextcloud", BaseURL: "https://files.example.invalid"}
	m.cfg.Connections["files"] = config.Connection{Service: "other", Credential: "reader"}
	if m.needsDescription("files") {
		t.Error("a connection alone with its provider is marked")
	}
}

// What the form shows is what the store gets: spaces at the edges are dropped where the user can see it.
func TestSpacesAtTheEdgesAreTrimmedVisibly(t *testing.T) {
	m, store, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	typeText(t, m, "  wiki  ")
	press(t, m, "tab")

	if got := m.fields[0].input.Value(); got != "wiki" {
		t.Errorf("the field still holds %q after leaving it", got)
	}
	if view := screenOf(m); strings.Contains(view, "  wiki  ") {
		t.Errorf("the form still shows spaces the store would drop:\n%s", view)
	}

	press(t, m, "tab")
	typeText(t, m, " https://wiki.example.invalid ")
	press(t, m, "enter")

	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Services["wiki"].BaseURL; got != "https://wiki.example.invalid" {
		t.Errorf("saved base url = %q", got)
	}
}

// A variable that carries nothing is reported as missing, again without reading any value. The answer for
// a credential of type env comes from the environment alone: its resolution ends there, so asking costs no
// store round trip.
func TestEnvSourceReportsTheStageWithoutTheValue(t *testing.T) {
	m, _, _ := newEnvModel(t, map[string]string{"QATLAS_TUI_PRESENT": "value"})

	if got, want := m.envSource("QATLAS_TUI_PRESENT"), string(secret.SourceEnv); got != want {
		t.Errorf("envSource() = %q, want %q", got, want)
	}
	if got, want := m.envSource("QATLAS_TUI_ABSENT"), string(secret.SourceMissing); got != want {
		t.Errorf("envSource() = %q, want %q", got, want)
	}
	if got := m.envSource(""); got != sourceUnnamed {
		t.Errorf("envSource() = %q, want %q", got, sourceUnnamed)
	}
}

// The editor starts on a machine that has no configuration yet.
func TestStartsWithoutAConfigurationFile(t *testing.T) {
	m, _, path := newModel(t)

	if m.cfg == nil || m.cfg.Version != config.Version {
		t.Fatalf("configuration = %+v", m.cfg)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file was created before anything was saved")
	}
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	view := m.View()
	for _, want := range []string{
		"created on first save",
		"c guided setup",
		"next: press c to",
		"1 Services",
		"2 Credentials",
		"3 Connections",
		"4 Defaults",
		"Config:",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("first-run view does not contain %q:\n%s", want, view)
		}
	}
	if compact := strings.Join(strings.Fields(view), ""); !strings.Contains(compact, path) {
		t.Errorf("first-run view does not contain the config path %q:\n%s", path, view)
	}
	if strings.Contains(view, "Loaded") {
		t.Errorf("a missing file is described as loaded:\n%s", view)
	}
}

func TestDependentSectionsExplainWhatMustBeCreatedFirst(t *testing.T) {
	m, _, _ := newModel(t)

	openSectionByName(t, m, sectionConnections)
	view := screenOf(m)
	for _, want := range []string{"Create a service and a credential", "Press 1 or 2", "c for the guided setup"} {
		if !strings.Contains(view, want) {
			t.Errorf("empty Connections does not contain %q:\n%s", want, view)
		}
	}
	press(t, m, "n")
	if m.screen != screenList {
		t.Fatalf("screen = %v, want to stay on the list without prerequisites", m.screen)
	}
	if !strings.Contains(m.fail, "Create a service and a credential") {
		t.Errorf("error = %q", m.fail)
	}

	openSectionByName(t, m, sectionDefaults)
	if view := screenOf(m); !strings.Contains(view, "Create a connection") ||
		!strings.Contains(view, "Defaults are optional") {
		t.Errorf("empty Defaults does not explain its prerequisite:\n%s", view)
	}
}

func TestNewKeyringCredentialContinuesWithItsSecrets(t *testing.T) {
	m, _, path := newModel(t)

	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	typeText(t, m, "wiki-reader")
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, config.CredentialTypeKeyring)
	press(t, m, "tab")

	before := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(before, "enter save credential first") {
		t.Errorf("new credential does not explain the first save:\n%s", before)
	}
	if !strings.Contains(before, "value labeled Token ID") || !strings.Contains(before, "not a name you choose") {
		t.Errorf("new credential does not explain token-id:\n%s", before)
	}
	press(t, m, "enter")

	if m.screen != screenForm || m.editing != "wiki-reader" {
		t.Fatalf("screen = %v editing = %q, want the saved credential form", m.screen, m.editing)
	}
	if m.fields[m.focus].kind != fieldSecret {
		t.Fatalf("focused field kind = %v, want a secret role", m.fields[m.focus].kind)
	}
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	for _, want := range []string{"Credential saved", "press s on each role", "system keyring", "(recommended)"} {
		if !strings.Contains(view, want) {
			t.Errorf("continued credential form does not contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "created on first save") {
		t.Errorf("the saved file is still described as not created:\n%s", view)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("first save did not create the configuration: %v", err)
	}
}

// formLines is the view without the padding the styles add, so a test can look at the shape of the form
// rather than at the cells that fill it out.
func formLines(m *Model) []string {
	lines := strings.Split(screenOf(m), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// fieldLine is the line the given field is drawn on. Field lines carry the two-cell margin or the focus
// marker; hint lines are indented further, so they cannot be mistaken for one.
func fieldLine(t *testing.T, lines []string, label string) int {
	t.Helper()
	for i, l := range lines {
		body := strings.TrimPrefix(l, "> ")
		if body == l {
			body = strings.TrimPrefix(l, "  ")
			if body == l {
				continue
			}
		}
		if strings.HasPrefix(body, label) {
			return i
		}
	}
	t.Fatalf("field %q is not on screen:\n%s", label, strings.Join(lines, "\n"))
	return -1
}

// hintBlocks collects every hint of the form, each one joined back into the sentence it was wrapped from.
func hintBlocks(lines []string) []string {
	var blocks []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, strings.Join(current, " "))
			current = nil
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "    ") {
			current = append(current, strings.TrimSpace(l))
			continue
		}
		flush()
	}
	flush()
	return blocks
}

// The name of an existing entry says that it is locked, keeps no hint that claims otherwise, and cannot be
// focused for editing, while every other field stays reachable and the form still saves.
func TestALockedNameSaysSoAndTakesNoEditingFocus(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")

	openSectionByName(t, m, sectionServices)
	press(t, m, "enter")

	view := screenOf(m)
	if !strings.Contains(view, "read-only") || !strings.Contains(view, "create it again to rename") {
		t.Errorf("the locked field does not say that it is locked:\n%s", view)
	}
	if strings.Contains(view, "connections and --connection refer to it") {
		t.Errorf("the locked field still claims the name can be chosen:\n%s", view)
	}
	// Locked is not hidden: the value stays on screen, it just cannot be entered.
	if !strings.Contains(view, "wiki") {
		t.Errorf("the locked field no longer shows its value:\n%s", view)
	}

	// Walking the form in both directions never stops on it, and the text input behind it stays blurred.
	visited := map[string]bool{}
	for _, key := range []string{"tab", "tab", "tab", "shift+tab", "shift+tab", "shift+tab", "down", "up"} {
		press(t, m, key)
		if m.focus == 0 {
			t.Fatalf("%q put the focus on the locked field", key)
		}
		if m.fields[0].input.Focused() {
			t.Fatalf("the locked field took editing focus after %q", key)
		}
		visited[m.fields[m.focus].label] = true
	}
	for _, f := range m.fields {
		if !f.readOnly && !visited[f.label] {
			t.Errorf("field %q cannot be reached any more", f.label)
		}
	}

	// The form is not a trap: it still saves, under the name that could not be changed.
	for i := 0; i < len(m.fields) && m.fields[m.focus].label != "base url"; i++ {
		press(t, m, "tab")
	}
	if got := m.fields[m.focus].label; got != "base url" {
		t.Fatalf("focused field = %q, want the base url", got)
	}
	clearField(t, m)
	typeText(t, m, "https://wiki.example.invalid/next")
	press(t, m, "enter")

	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := saved.Services["wiki"].BaseURL; got != "https://wiki.example.invalid/next" {
		t.Errorf("saved base url = %q, the form did not save through the locked field", got)
	}
}

// A form whose first field is locked opens on the first field that takes input, in every section that has
// one.
func TestALockedFieldDoesNotSwallowTheOpeningFocus(t *testing.T) {
	m, _, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_READER_ID", "WIKI_READER_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")

	openSectionByName(t, m, sectionDefaults)
	press(t, m, "n")
	typeText(t, m, "wiki.example.invalid")
	press(t, m, "tab")
	selectChoice(t, m, "wiki")
	press(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}

	for _, s := range []section{sectionServices, sectionCredentials, sectionConnections, sectionDefaults} {
		openSectionByName(t, m, s)
		pump(t, m, "enter")
		if m.screen != screenForm {
			t.Fatalf("%v did not open a form", s)
		}
		if m.fields[m.focus].readOnly {
			t.Errorf("%v opened its form on a locked field (%q)", s, m.fields[m.focus].label)
		}
		press(t, m, "esc")
	}
}

// A hint belongs to the field above it: it stands directly under its own row, and a blank line separates
// the block from the next field. It holds at a width that wraps the hints too.
func TestAHintBelongsToTheFieldAboveIt(t *testing.T) {
	for _, width := range []int{100, 46} {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: width})
		openSectionByName(t, m, sectionCredentials)
		press(t, m, "n")
		press(t, m, "tab")
		press(t, m, "tab")
		selectChoice(t, m, config.CredentialTypeEnv)

		lines := formLines(m)
		wrappedHints := 0
		for i, f := range m.fields {
			at := fieldLine(t, lines, f.label)
			if i > 0 && lines[at-1] != "" {
				t.Errorf("width %d: field %q is not separated from the block above it:\n%s",
					width, f.label, strings.Join(lines, "\n"))
			}
			hint := m.fieldHint(f)
			if hint == "" {
				if at+1 < len(lines) && strings.HasPrefix(lines[at+1], "    ") {
					t.Errorf("width %d: field %q has no hint but an indented line under it:\n%s",
						width, f.label, strings.Join(lines, "\n"))
				}
				continue
			}
			// The hint starts on the very next line, and every continuation line of a wrapped hint stays
			// inside the same block, on the same indent.
			height := 0
			for j := at + 1; j < len(lines) && lines[j] != ""; j++ {
				if !strings.HasPrefix(lines[j], "    ") {
					t.Errorf("width %d: the hint block of %q leaves its indent at %q:\n%s",
						width, f.label, lines[j], strings.Join(lines, "\n"))
				}
				height++
			}
			if height == 0 {
				t.Errorf("width %d: the hint of %q does not stand under it:\n%s",
					width, f.label, strings.Join(lines, "\n"))
			}
			if height > 1 {
				wrappedHints++
			}
		}
		if width == 46 && wrappedHints == 0 {
			t.Errorf("width %d: no hint wrapped, the narrow case is not being tested:\n%s",
				width, strings.Join(lines, "\n"))
		}
	}
}

// The same sentence never stands twice under one another. What all role rows have in common is said once;
// what differs per role stays on its own row.
func TestNoHintStandsTwice(t *testing.T) {
	m, _, _ := newModel(t)

	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, config.CredentialTypeEnv)
	assertNoRepeatedHint(t, m, "the env credential form")
	if got := strings.Count(screenOf(m), "the NAME of an environment"); got != 1 {
		t.Errorf("the env sentence stands %d times, want once:\n%s", got, screenOf(m))
	}

	// The keyring rows build their hint themselves, out of the keys and the stages the resolver checked.
	addKeyringCredential(t, m, "store")
	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "enter")
	if m.screen != screenForm {
		t.Fatalf("screen = %v, want the form of the keyring credential", m.screen)
	}
	assertNoRepeatedHint(t, m, "the keyring credential form")

	view := screenOf(m)
	words := strings.Join(strings.Fields(view), " ")
	if got := strings.Count(words, "p unencrypted file (asks first)"); got != 1 {
		t.Errorf("the secret keys stand %d times, want once:\n%s", got, view)
	}
	// The stages differ per role, so every role keeps its own.
	if got, want := strings.Count(view, "checked:"), len(m.cfg.SecretRoles()); got != want {
		t.Errorf("the checked stages appear %d times, want once per role (%d):\n%s", got, want, view)
	}
}

func assertNoRepeatedHint(t *testing.T, m *Model, what string) {
	t.Helper()
	blocks := hintBlocks(formLines(m))
	seen := map[string]bool{}
	for _, block := range blocks {
		if seen[block] {
			t.Errorf("%s says the same hint twice: %q\n%s", what, block, screenOf(m))
		}
		seen[block] = true
	}
	if len(blocks) == 0 {
		t.Errorf("%s shows no hint at all:\n%s", what, screenOf(m))
	}
}

// A form too tall for its terminal drops the hints it can spare instead of becoming unreachable, and the
// resize notice is never the only way out of the editor.
func TestATallFormStaysUsableInASmallTerminal(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 80})
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	full := strings.Count(screenOf(m), "\n")

	// The same form in a terminal that cannot hold it: it is still the form, only denser.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	view := screenOf(m)
	if strings.Contains(view, "Resize terminal") {
		t.Fatalf("the form gave up instead of tightening:\n%s", view)
	}
	if dense := strings.Count(view, "\n"); dense >= full {
		t.Errorf("dense form = %d lines, want fewer than the %d of the full one", dense, full)
	}
	for _, want := range []string{"name", typeLabel, "enter save"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dense form dropped %q:\n%s", want, view)
		}
	}
	// The hint of the focused field survives, wrapped to the width; the hints of the others are what paid
	// for the space.
	if !strings.Contains(view, "a key you choose") {
		t.Errorf("the dense form dropped the hint of the focused field:\n%s", view)
	}
	if strings.Contains(view, "containers") {
		t.Errorf("the dense form kept the hint of an unfocused field:\n%s", view)
	}

	// A terminal too small even for that shows the notice, and esc leaves it.
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	if !strings.Contains(screenOf(m), "Resize terminal") {
		t.Fatalf("a 20x6 terminal did not show the resize notice:\n%s", screenOf(m))
	}
	press(t, m, "esc")
	if m.screen != screenList {
		t.Errorf("screen after esc = %v, want the list the form was opened from", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenNav {
		t.Errorf("screen after the second esc = %v, want the sidebar", m.screen)
	}
	if m.quitting {
		t.Error("leaving the notice quit the editor")
	}
}

// Naming the provider of an existing credential is one choice and one save. Replacing the role rows must
// not take the type row with them: without it the entry would be written with no type at all, and the
// core would refuse the file the editor just produced.
func TestNamingTheProviderKeepsTheRestOfTheCredential(t *testing.T) {
	m, store, path := newModel(t)

	addKeyringCredential(t, m, "bookstack-personal")
	// The name of an existing entry is read-only and enter on the provider row opens its table, so the form
	// opens on the type row, where enter saves.
	editEntry(t, m, "bookstack-personal")
	if got := m.fields[m.focus].label; got != typeLabel {
		t.Fatalf("the form opened on %q, want the type row", got)
	}
	focusField(t, m, providerLabel)
	selectChoice(t, m, "bookstack")
	if got := m.credentialType(); got != config.CredentialTypeKeyring {
		t.Fatalf("type after choosing the provider = %q, want the keyring it was created as", got)
	}
	press(t, m, "tab")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("saving reported %q", m.fail)
	}

	saved, err := loadTestConfig(t, path)
	if err != nil {
		t.Fatalf("the editor wrote a file its own core refuses: %v", err)
	}
	cred := saved.Credentials["bookstack-personal"]
	if cred.Provider != "bookstack" || cred.Type != config.CredentialTypeKeyring {
		t.Errorf("stored credential = %+v, want the BookStack keyring credential", cred)
	}
	if _, err := store.Load(); err != nil {
		t.Errorf("Load() = %v", err)
	}

	// Only the two BookStack roles are asked for now.
	editEntry(t, m, "bookstack-personal")
	var roles []string
	for _, f := range m.fields {
		if f.kind == fieldEnvName || f.kind == fieldSecret {
			roles = append(roles, f.label)
		}
	}
	if strings.Join(roles, ",") != "token-id,token-secret" {
		t.Errorf("roles = %v, want only the BookStack pair", roles)
	}
}
