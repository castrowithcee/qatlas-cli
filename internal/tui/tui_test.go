package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
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

// testService returns the management service the editor under test works through.
func testService(store *config.Store, secrets Secrets, redactor *redact.Redactor) *manage.Service {
	return manage.New(store, secrets, connlog.SurfaceTUI, redactor)
}

// buildModel builds the editor over store the way the command does, with a service over the same secrets.
// buildModel is New with the editor moved onto the Library's Services list, tab line in focus: the section
// most walks begin on. TestEditorStartsOnTheOverview covers what New itself opens.
func buildModel(store *config.Store, tester Tester, secrets Secrets, redactor *redact.Redactor) (*Model, error) {
	m, err := New(testService(store, secrets, redactor), store, tester, secrets, redactor)
	if err != nil {
		return nil, err
	}
	m.section = sectionServices
	m.list.reset(m.entryNames(m.section))
	m.layoutWorkspace()
	return m, nil
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
	model, err := buildModel(store, nil, secrets, nil)
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

// newVaultResolver is newResolver plus a real vault below dir, so a test can exercise an actual vault
// write and its passphrase without touching anything of the machine: everything lives under the test's own
// temporary directory. No passphrase is ever asked interactively; nothing here needs one unless the test
// itself types one into the editor.
func newVaultResolver(t *testing.T, dir string) (*secret.Resolver, *secret.MemoryStore) {
	t.Helper()
	secrets, mem := newResolver(t, dir, nil)
	return secrets.WithVault(vault.New(dir), nil), mem
}

// addVaultCredential creates a credential whose secrets live in the vault. It names nothing: the roles are
// filled afterwards, through the masked prompt, the same way addKeyringCredential does for the keyring.
func addVaultCredential(t *testing.T, m *Model, name string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	typeText(t, m, name)
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageVault)
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("creating the vault credential reported %q", m.fail)
	}
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
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
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

// pump presses keys and delivers the messages of the commands they produce, the way the event loop does,
// a tea.Batch of several commands (see autoApprove, whose approval sweep runs beside whatever else a save
// already returns) included; see deliver in update_test.go, which does the same for Init().
func pump(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, cmd := m.Update(keyMsg(k))
		deliver(m, cmd)
	}
}

// typeText enters a value into the focused field, one rune at a time.
func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// clearField empties the focused text field with the field's own delete-before and delete-after keys: two
// key presses instead of one per character, since every key press redraws the screen to check its size.
func clearField(t *testing.T, m *Model) {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
}

// openSectionByName navigates from the tab line to a section the way a person does: the digit of its area,
// then the arrows of the option bar when it is not the area's first sub-tab. It ends where opening a list
// ends: on the list, or on the form for the Vault.
func openSectionByName(t *testing.T, m *Model, s section) {
	t.Helper()
	m.screen = screenNav
	pump(t, m, string(rune('1'+s.area())))
	if m.section != s {
		if m.screen == screenForm {
			pump(t, m, "esc")
		}
		if m.screen == screenList {
			pump(t, m, "shift+tab")
		}
		if m.screen != screenNav || !m.navSub {
			t.Fatalf("after the digit of %v: screen %v, section %v", s.area().title(), m.screen, m.section)
		}
		for i := 0; i < len(areaSections[s.area()]) && m.section != s; i++ {
			pump(t, m, "right")
		}
		pump(t, m, "down")
	}
	if m.section != s {
		t.Fatalf("section = %v, want %v", m.section, s)
	}
}

// addService walks the real key sequence for creating a service.
func addService(t *testing.T, m *Model, name, baseURL string) {
	t.Helper()
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, name)
	press(t, m, "tab") // provider, the only choice is preselected
	press(t, m, "tab")
	typeText(t, m, baseURL)
	press(t, m, "f2")
}

// addCredential walks the real key sequence for a credential of type env: name, type, one variable name
// per role.
func addCredential(t *testing.T, m *Model, name string, envNames ...string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	typeText(t, m, name)
	// A credential may name its provider; these helpers leave that choice empty, which is what an entry
	// written before the field looks like, and then every compiled role is offered.
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageEnv)
	for _, env := range envNames {
		press(t, m, "tab")
		typeText(t, m, env)
	}
	press(t, m, "f2")
}

// addKeyringCredential creates a credential whose secrets live in the credential store. It names nothing:
// the roles are filled afterwards, through the masked prompt.
func addKeyringCredential(t *testing.T, m *Model, name string) {
	t.Helper()
	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	typeText(t, m, name)
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageKeyring)
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("creating the keyring credential reported %q", m.fail)
	}
}

func addConnection(t *testing.T, m *Model, name, service, credential string) {
	t.Helper()
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, name)
	// The provider row narrows the service and credential rows; the service the caller asks for names the
	// provider it belongs to.
	focusField(t, m, providerLabel)
	selectChoice(t, m, m.cfg.Services[service].Provider)
	focusField(t, m, "service")
	selectChoice(t, m, service)
	focusField(t, m, "credential")
	selectChoice(t, m, credential)
	press(t, m, "f2")
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

// selectAccess sets the access row of the open connection form to the given mode.
func selectAccess(t *testing.T, m *Model, mode string) {
	t.Helper()
	focusField(t, m, accessLabel)
	selectChoice(t, m, mode)
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
	press(t, m, "f2")

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

	t.Run("the editor opens on the tab line with the Overview shown below it", func(t *testing.T) {
		fresh, _, _ := newModel(t)
		fresh.section = sectionOverview
		if fresh.screen != screenNav || fresh.section.area() != areaOverview {
			t.Fatalf("start = screen %v section %v, want the tab line on the Overview", fresh.screen, fresh.section)
		}
	})

	t.Run("left and right wrap in both directions and show each area at once", func(t *testing.T) {
		m.screen, m.section, m.navSub = screenNav, sectionOverview, false
		press(t, m, "left")
		if m.section != sectionGeneral || m.screen != screenNav {
			t.Errorf("left = section %v screen %v, want Settings on the tab line", m.section, m.screen)
		}
		press(t, m, "right")
		if m.section != sectionOverview {
			t.Errorf("right = section %v, want the Overview", m.section)
		}
		for _, key := range []string{"l", "h"} {
			before := m.section
			press(t, m, key)
			if m.section == before || m.screen != screenNav {
				t.Errorf("%q did not move the tab line: section %v screen %v", key, m.section, m.screen)
			}
		}
	})

	t.Run("enter, down and tab hand the focus to the list and back", func(t *testing.T) {
		for _, key := range []string{"enter", "down", "j", "tab"} {
			m.screen, m.section, m.navSub = screenNav, sectionCredentials, true
			press(t, m, key)
			if m.screen != screenList || m.section != sectionCredentials {
				t.Errorf("%q = screen %v section %v, want the Credentials list", key, m.screen, m.section)
			}
		}
		for _, key := range []string{"tab", "shift+tab", "esc"} {
			m.screen = screenList
			press(t, m, key)
			if m.screen != screenNav || m.section != sectionCredentials {
				t.Errorf("%q from the list = screen %v section %v, want the tab line on Credentials",
					key, m.screen, m.section)
			}
		}
	})

	t.Run("escape on the tab line neither quits nor moves", func(t *testing.T) {
		m.screen, m.section, m.navSub = screenNav, sectionConnections, false
		press(t, m, "esc", "esc")
		if m.quitting || m.screen != screenNav || m.section != sectionConnections {
			t.Errorf("esc on the tab line = quitting %v screen %v section %v", m.quitting, m.screen, m.section)
		}
	})

	t.Run("form focus wraps in both directions", func(t *testing.T) {
		openSectionByName(t, m, sectionServices)
		pressNew(t, m)
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

// TestNarrowNavigation checks that the tab line keeps one set of keys at every width: left/right (and h/l)
// choose the section with wraparound, down (and enter/tab) focuses the content below it, and up does
// nothing there. In the list, up/down move the selection without wraparound, up at the first entry or in an
// empty list returns the focus to the tab line, down stays at the last entry, and left/right stay in the list.
func TestNarrowNavigation(t *testing.T) {
	for _, width := range []int{40, 79, 80, 160} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m, _, _ := newModel(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m.screen, m.section, m.navSub = screenNav, sectionOverview, false
			if width >= narrowWidth-1 && !strings.Contains(m.render(), "←/→ move") {
				t.Errorf("footer does not name left/right:\n%s", m.render())
			}
			press(t, m, "left")
			if m.section != sectionGeneral || m.screen != screenNav {
				t.Fatalf("left at the Overview = section %v screen %v, want Settings (wrapped back)", m.section, m.screen)
			}
			for _, key := range []string{"right", "l"} {
				m.screen, m.section, m.navSub = screenNav, sectionGeneral, false
				press(t, m, key)
				if m.section != sectionOverview || m.screen != screenNav {
					t.Errorf("%q at Settings = section %v screen %v, want the Overview (wrapped forward)",
						key, m.section, m.screen)
				}
			}
			for _, key := range []string{"up", "k"} {
				m.screen, m.section, m.navSub = screenNav, sectionServices, false
				press(t, m, key)
				if m.section != sectionServices || m.screen != screenNav || m.navSub {
					t.Errorf("%q at the tab line = section %v screen %v, want no change", key, m.section, m.screen)
				}
			}
			// The option bar sits between the tab line and the list, and its arrows wrap through the sub-tabs.
			m.screen, m.section, m.navSub = screenNav, sectionServices, false
			press(t, m, "down")
			if !m.navSub || m.screen != screenNav {
				t.Errorf("down at the tab line = screen %v option bar %v, want the option bar", m.screen, m.navSub)
			}
			for _, step := range []struct {
				key  string
				want section
			}{{"right", sectionCredentials}, {"right", sectionServices}, {"left", sectionCredentials}} {
				press(t, m, step.key)
				if m.section != step.want || m.screen != screenNav || !m.navSub {
					t.Errorf("%q on the option bar = section %v screen %v, want %v", step.key, m.section, m.screen, step.want)
				}
			}
			press(t, m, "up")
			if m.navSub || m.section != sectionCredentials {
				t.Errorf("up on the option bar = section %v bar %v, want the tab line", m.section, m.navSub)
			}
			for _, key := range []string{"down", "j", "enter", "tab"} {
				m.screen, m.section, m.navSub = screenNav, sectionCredentials, true
				press(t, m, key)
				if m.screen != screenList || m.section != sectionCredentials {
					t.Errorf("%q = screen %v section %v, want the Credentials list", key, m.screen, m.section)
				}
			}
			// Vault and Logs have no list of their own; down/enter/tab open their own content area.
			m.screen, m.section, m.navSub = screenNav, sectionVault, true
			press(t, m, "down")
			if m.screen != screenForm {
				t.Errorf("down at Vault = screen %v, want the Vault form", m.screen)
			}
			m.screen, m.section, m.navSub = screenNav, sectionLogs, false
			press(t, m, "enter")
			if m.screen != screenLogs {
				t.Errorf("enter at Logs = screen %v, want the Logs screen", m.screen)
			}
		})
	}
}

// TestNarrowListFocus checks the list's own keys at a narrow and a wide terminal.
func TestNarrowListFocus(t *testing.T) {
	for _, width := range []int{79, 160} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m, _, _ := newModel(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m.section = sectionServices
			m.list.reset([]string{"a", "b", "c"})
			m.screen = screenList

			if !strings.Contains(m.render(), "tab areas") {
				t.Errorf("footer does not name tab:\n%s", m.render())
			}

			press(t, m, "up")
			if m.screen != screenNav {
				t.Fatalf("up at the first entry = screen %v, want the tab line", m.screen)
			}

			m.screen = screenList
			press(t, m, "down", "down")
			if m.list.cursor != 2 {
				t.Fatalf("cursor after two downs = %d, want 2 (last entry)", m.list.cursor)
			}
			press(t, m, "down")
			if m.list.cursor != 2 || m.screen != screenList {
				t.Errorf("down at the last entry = cursor %d screen %v, want to stay put", m.list.cursor, m.screen)
			}

			for _, key := range []string{"left", "h", "right", "l"} {
				m.screen = screenList
				press(t, m, key)
				if m.screen != screenList {
					t.Errorf("%q in the list = screen %v, want to stay in the list", key, m.screen)
				}
			}

			press(t, m, "k", "k", "k")
			if m.screen != screenNav {
				t.Errorf("k to the first entry and past it = screen %v, want the tab line", m.screen)
			}

			m.screen = screenList
			m.list.reset(nil)
			press(t, m, "up")
			if m.screen != screenNav {
				t.Errorf("up in an empty list = screen %v, want the tab line", m.screen)
			}
		})
	}
}

// areaLabels are how the tab line names the six areas in full.
var areaLabels = []string{"1 Overview", "2 Connections", "3 Activity", "4 Access", "5 Library", "6 Settings"}

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
		// tabs are what the tab line shows, in order, with the active one in brackets.
		tabs string
	}{
		{name: "wide terminal", width: 160, height: 28,
			tabs: "1 Overview   2 Connections   3 Activity   4 Access   [5 Library]  6 Settings"},
		{name: "standard terminal", width: 100, height: 24,
			tabs: "1 Overview 2 Connections 3 Activity 4 Access [5 Library] 6 Settings"},
		{name: "names without gaps", width: 90, height: 24,
			tabs: "1 Overview 2 Connections 3 Activity 4 Access [5 Library] 6 Settings"},
		{name: "standard width", width: 80, height: 24, tabs: "1 Overview 2 Connections 3 Activity 4 Access [5 Library] 6 Settings"},
		{name: "names give way to digits", width: 60, height: 24, tabs: "1 2 3 4 [5 Library] 6"},
		{name: "compact", width: 40, height: minimumHeight, tabs: "1 2 3 4 [5 Library] 6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			view := m.render()
			assertViewFits(t, view, tt.width, tt.height)
			lines := strings.Split(view, "\n")
			tabs := 1
			if m.vaultHeaderText() != "" && tt.width < narrowWidth {
				tabs = 2
			}
			if !strings.Contains(strings.Join(strings.Fields(lines[tabs]), " "), strings.Join(strings.Fields(tt.tabs), " ")) ||
				!strings.Contains(lines[tabs+1], "[Services]  Credentials") || !strings.Contains(lines[tabs+2], "─") {
				t.Errorf("the tab line %q does not stand above the option bar, a rule and the workspace:\n%s", tt.tabs, view)
			}
			// The table stands in a frame where the terminal has room for it, and in the compact form without.
			framed := tt.width >= tableFrameMinWidth && tt.height >= tableFrameMinHeight
			title := "Services  1/1"
			if framed {
				title = "Services (1)"
			}
			for _, want := range []string{title, "wiki"} {
				if !strings.Contains(view, want) {
					t.Errorf("view does not contain %q:\n%s", want, view)
				}
			}
			if strings.ContainsAny(view, "║│╔╭") != framed {
				t.Errorf("the table is framed %v at %dx%d, want %v:\n%s", !framed, tt.width, tt.height, framed, view)
			}
			for _, secretValue := range []string{canaryID, canarySecret} {
				if strings.Contains(view, secretValue) {
					t.Errorf("view exposed a secret value %q:\n%s", secretValue, view)
				}
			}
		})
	}
}

// The tab line reads without colour: brackets mark the active tab, the focus marker the tab line itself, and
// a terminal too narrow for the names keeps the active tab whole.
func TestTabLineKeepsTheActiveTabWhole(t *testing.T) {
	before := currentProfile()
	t.Cleanup(func() { setProfile(before) })
	ascii := before
	ascii = profileNone
	setProfile(ascii)

	m, _, _ := newModel(t)
	for _, width := range []int{40, 60, 80, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		for a := area(0); a < areaCount; a++ {
			m.section, m.screen = areaSections[a][0], screenList
			line := m.navLine()
			if strings.Contains(line, "\x1b") {
				t.Fatalf("width %d: the tab line carries colour codes: %q", width, line)
			}
			if want := "[" + areaLabels[a] + "]"; !strings.Contains(line, want) {
				t.Errorf("width %d: the tab line does not show %q whole: %q", width, want, line)
			}
			if strings.Contains(line, "> ") {
				t.Errorf("width %d: the tab line is marked focused while the list has the focus: %q", width, line)
			}
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: the tab line is %d cells wide: %q", width, got, line)
			}
			m.screen = screenNav
			if line := m.navLine(); !strings.HasPrefix(line, "> ") {
				t.Errorf("width %d: the focused tab line has no marker: %q", width, line)
			}
		}
	}
}

// Only the Overview points at the next step; a list shows no line of its own for it.
func TestOnlyTheOverviewNamesTheNextStep(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	openSectionByName(t, m, sectionServices)
	if view := m.render(); strings.Contains(view, "next:") {
		t.Errorf("the list shows a next step:\n%s", view)
	}
	openSectionByName(t, m, sectionOverview)
	if view := strings.Join(strings.Fields(m.render()), " "); !strings.Contains(view, "press c to set up a connection step by step") {
		t.Errorf("the Overview shows no next step:\n%s", view)
	}
}

// Every section is one key away from the list and from an unchanged form; none of it goes through escape.
func TestDirectSectionKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	openSectionByName(t, m, sectionServices)

	for _, want := range []area{areaConnections, areaAccess, areaLibrary} {
		press(t, m, string(rune('1'+want)))
		if m.screen != screenList || m.section != areaSections[want][0] {
			t.Fatalf("%d from the list = screen %v section %v", want+1, m.screen, m.section)
		}
	}

	// In a form the digits are text, with no alt+digit section key: the global shortcut was removed.
	pressNew(t, m)
	typeText(t, m, "2")
	if m.section != sectionServices || m.fieldValue("name") != "2" {
		t.Fatalf("a digit escaped the form: section %v name %q", m.section, m.fieldValue("name"))
	}
	press(t, m, "tab")
	clearField(t, m)
	press(t, m, "shift+tab")
	clearField(t, m)
	press(t, m, "esc")

	// alt+digit does nothing in a form, changed or not.
	for _, want := range []area{areaConnections, areaAccess, areaSettings} {
		openSectionByName(t, m, sectionServices)
		press(t, m, "enter")
		if m.screen != screenForm || m.editing != "wiki" {
			t.Fatalf("enter did not open wiki: screen %v", m.screen)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + want)}, Alt: true})
		if m.screen != screenForm || m.editing != "wiki" || m.section != sectionServices {
			t.Errorf("alt+%d in a form = screen %v section %v, want no effect", want+1, m.screen, m.section)
		}
	}

	// The tab line answers the digits as well, and n there opens the form of the section it stands on.
	m.screen = screenNav
	press(t, m, "2")
	if m.screen != screenList || m.section != sectionConnections {
		t.Fatalf("2 on the tab line = screen %v section %v", m.screen, m.section)
	}
	press(t, m, "n")
	if m.screen != screenProviders || m.wizard == nil || m.fail != "" {
		t.Errorf("new without prerequisites: screen %v error %q, want the guided setup", m.screen, m.fail)
	}
	press(t, m, "esc")

	// The guided setup is one key away from the list and from the tab line, and a setup without a provider
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

// A new setup runs from the Overview over Services and Credentials to Connections with the area digits and
// the command line only: no dashboard, no tab line and no escape in between.
func TestRebuildWithoutADashboard(t *testing.T) {
	m, store, _ := newModel(t)
	m.section = sectionOverview
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

	step("5", "n")
	typeText(t, m, "wiki")
	step("tab", "tab")
	typeText(t, m, "https://wiki.example.invalid")
	step("f2", ":")
	typeText(t, m, "go credentials")
	step("enter", "n")
	typeText(t, m, "reader")
	step("tab", "tab")
	selectChoice(t, m, storageEnv)
	step("tab")
	typeText(t, m, "WIKI_ID")
	step("tab")
	typeText(t, m, "WIKI_SECRET")
	// n in Connections is the guided setup; it reuses the service and the credential made above.
	step("f2", "2", "n", "enter", "f2", "f2")
	clearField(t, m)
	typeText(t, m, "wiki")
	step("f2", "f2", "f2")
	if m.wizard == nil || m.wizard.saved != "wiki" {
		t.Fatalf("the setup did not save the connection: fail %q", m.fail)
	}
	// The saved setup is left with esc, which opens the Connections list; the walk itself used none.
	press(t, m, "esc")

	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	if m.screen != screenList || m.section != sectionConnections {
		t.Fatalf("the walk ended on screen %v section %v", m.screen, m.section)
	}
	if screens[screenNav] {
		t.Error("the walk passed through the tab line although only area keys and commands were used")
	}
	saved, err := store.Load()
	if err != nil || len(saved.Services) != 1 || len(saved.Credentials) != 1 || len(saved.Connections) != 1 {
		t.Fatalf("saved configuration = %+v, %v", saved, err)
	}
	if view := m.render(); !strings.Contains(view, "wiki  active  wiki     read") ||
		!strings.Contains(view, "[2 Connections]") {
		t.Errorf("the workspace or the tab line does not show the result:\n%s", view)
	}
}

// Unsaved input is neither lost nor saved behind the user's back when a form is left.
func TestLeavingAChangedFormAsksFirst(t *testing.T) {
	startForm := func(t *testing.T) (*Model, string) {
		t.Helper()
		m, _, path := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		openSectionByName(t, m, sectionServices)
		pressNew(t, m)
		typeText(t, m, "wiki")
		press(t, m, "tab", "tab")
		typeText(t, m, "https://wiki.example.invalid")
		return m, path
	}

	t.Run("stay keeps every input and writes nothing", func(t *testing.T) {
		m, path := startForm(t)
		press(t, m, "esc")
		if m.screen != screenLeave {
			t.Fatalf("esc on a changed form = screen %v, want the leave question", m.screen)
		}
		view := m.render()
		for _, want := range []string{
			"warning: unsaved changes", "F2 save", "d discard", "esc keep editing", "the Services list",
		} {
			if !strings.Contains(view, want) {
				t.Errorf("the leave question does not say %q:\n%s", want, view)
			}
		}
		// Keys that are no answer change nothing.
		press(t, m, "enter", "x", "2", "q", "n", "y", "s", "k", "f3", "backspace")
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
		press(t, m, "esc", "d")
		if m.screen != screenList || m.section != sectionServices {
			t.Fatalf("discard = screen %v section %v, want Services", m.screen, m.section)
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
		press(t, m, "esc")
		pump(t, m, "f2")
		if m.fail != "" || m.screen != screenList || m.section != sectionServices {
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
		press(t, m, "esc")
		pump(t, m, "f2")
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

	t.Run("an unfinished guided setup can only be kept or dropped", func(t *testing.T) {
		m, _, path := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		walkSetup(t, m, 1)
		press(t, m, "esc")
		if m.screen != screenProviders || m.wizard == nil || m.wizard.step != stepProvider {
			t.Fatalf("esc on the service step = screen %v, want the provider table", m.screen)
		}
		press(t, m, "esc")
		if m.screen != screenLeave {
			t.Fatalf("esc on the first step = screen %v, want the leave question", m.screen)
		}
		if view := m.render(); !strings.Contains(view, "F2 next step") || !strings.Contains(view, "d discard") {
			t.Errorf("the setup question offers the wrong answers:\n%s", view)
		}
		press(t, m, "s", "k")
		if m.screen != screenLeave {
			t.Fatalf("a stray key answered the setup question: screen %v", m.screen)
		}
		press(t, m, "esc")
		if m.wizard == nil || m.screen != screenProviders {
			t.Fatalf("staying dropped the setup: screen %v", m.screen)
		}
		// F2 in the question goes on like F2 in the setup and saves nothing.
		press(t, m, "esc", "f2")
		if m.wizard == nil || m.wizard.step != stepService || m.screen != screenForm {
			t.Fatalf("F2 in the question did not go on: screen %v", m.screen)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("F2 in the setup question wrote the file")
		}
		press(t, m, "esc", "esc", "d")
		if m.wizard != nil || m.screen != screenList || m.section != sectionServices {
			t.Fatalf("discard = wizard %v screen %v section %v", m.wizard != nil, m.screen, m.section)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("dropping the setup wrote the file")
		}
	})
}

// Colour only supports what markers, prefixes and borders already say.
func TestTheScreenReadsWithoutColour(t *testing.T) {
	before := currentProfile()
	t.Cleanup(func() { setProfile(before) })

	m, _, _ := newModel(t)
	m.tester = func(context.Context, string) (provider.Class, error) { return provider.ClassOK, nil }
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")
	openSectionByName(t, m, sectionConnections)

	colour := before
	colour = profile16
	setProfile(colour)
	if view := m.render(); !strings.Contains(view, "\x1b[") {
		t.Fatalf("a colour terminal got no colour:\n%s", view)
	}
	colour = profileNone
	setProfile(colour)

	view := m.render()
	if strings.Contains(view, "\x1b") {
		t.Fatalf("a terminal without colour got escape sequences:\n%q", view)
	}
	for _, want := range []string{"[2 Connections]", ">   wiki"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list in focus does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "> 1 Overview") || strings.Contains(view, "> [2 Connections]") {
		t.Errorf("the tab line is marked although the list has the focus:\n%s", view)
	}
	press(t, m, "shift+tab")
	view = m.render()
	if !strings.Contains(strings.Join(strings.Fields(view), " "), "> 1 Overview [2 Connections] 3 Activity") ||
		strings.Contains(view, "> wiki") ||
		strings.Count(view, "> ") != 1 {
		t.Errorf("the tab line in focus is not the one marked line:\n%s", view)
	}

	press(t, m, "enter")
	pump(t, m, "t")
	if view := m.render(); !strings.Contains(view, "[ok] wiki") {
		t.Errorf("success has no marker:\n%s", view)
	}
	m.testClass = provider.ClassAuth
	if view := m.render(); !strings.Contains(view, "[failed] wiki: auth") {
		t.Errorf("a failed test has no marker:\n%s", view)
	}
	m.fail = "something went wrong"
	if view := m.render(); !strings.Contains(view, "error: something went wrong") {
		t.Errorf("an error has no prefix:\n%s", view)
	}
	m.clearMessages()
	press(t, m, "enter")
	press(t, m, "enter")
	focusField(t, m, "description")
	typeText(t, m, "x")
	press(t, m, "esc")
	if view := m.render(); !strings.Contains(view, "warning: unsaved changes") {
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

	for _, size := range []struct{ width, height int }{{100, 28}, {60, 24}, {40, minimumHeight}, {20, 5}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		assertViewFits(t, m.render(), max(size.width, minimumWidth), max(size.height, minimumHeight))
		if m.screen != screenList || m.section != sectionServices || m.list.query() != "a" ||
			selectedName(t, m) != selected {
			t.Fatalf("%dx%d changed the list: screen %v section %v filter %q selected %q", size.width,
				size.height, m.screen, m.section, m.list.query(), selectedName(t, m))
		}
		if size.width >= minimumWidth && !strings.Contains(m.render(), "/ a") {
			t.Errorf("%dx%d does not show the filter:\n%s", size.width, size.height, m.render())
		}
	}

	press(t, m, "enter")
	for i := 0; i < len(m.fields) && m.fields[m.focus].label != "base url"; i++ {
		press(t, m, "tab")
	}
	typeText(t, m, "/draft")
	focus, url := m.focus, m.fieldValue("base url")
	for _, size := range []struct{ width, height int }{{60, 24}, {40, minimumHeight}, {20, 5}, {100, 28}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.render()
		assertViewFits(t, view, max(size.width, minimumWidth), max(size.height, minimumHeight))
		if m.activeScreenTooSmall() {
			// A screen that cannot be shown takes no keys that change it.
			press(t, m, "tab", "n", "5")
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
	press(t, m, "tab")

	for _, size := range []struct{ width, height int }{
		{39, 12}, {40, 11}, {12, 4}, {1, 1}, {0, 0}, {-4, -2},
	} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.render()
		width, height := max(size.width, 1), max(size.height, 1)
		if size.width == 0 {
			width, height = 12, 4 // a zero dimension keeps the last one
		}
		assertViewFits(t, view, width, height)
		if !strings.Contains(view, "q") {
			t.Errorf("%dx%d resize view has no visible q key: %q", size.width, size.height, view)
		}
		press(t, m, "5", "n", "enter")
		if m.screen != screenNav || m.section != sectionDefaults {
			t.Fatalf("%dx%d changed hidden state to screen %v section %v", size.width, size.height,
				m.screen, m.section)
		}
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if m.screen != screenNav || m.section != sectionDefaults {
		t.Fatalf("resize lost the tab line state: screen %v section %v", m.screen, m.section)
	}
	lines := strings.Split(m.render(), "\n")
	if !strings.Contains(lines[1], "[6 Settings]") || !strings.HasPrefix(lines[2], "> ") ||
		!strings.Contains(lines[2], "[Defaults]") {
		t.Errorf("restored navigation lost focus:\n%s", m.render())
	}

	// The notice is not a new editor screen. A half-completed form remains intact behind it.
	form, _, _ := newModel(t)
	press(t, form, "5", "n")
	typeText(t, form, "draft-service")
	press(t, form, "tab")
	focus := form.focus
	form.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	if view := form.render(); !strings.Contains(view, "esc leave") {
		t.Errorf("a tiny form view does not offer its way out:\n%s", view)
	}
	press(t, form, "tab", "n", "5", "q")
	form.Update(tea.WindowSizeMsg{Width: 80, Height: 100})
	if form.quitting || form.screen != screenForm || form.focus != focus || form.fieldValue("name") != "draft-service" {
		t.Errorf("resize changed form state: quitting %v screen %v focus %d name %q", form.quitting, form.screen,
			form.focus, form.fieldValue("name"))
	}
	if strings.Contains(form.render(), "Resize terminal") {
		t.Errorf("restored form still shows the resize notice:\n%s", form.render())
	}
}

// screenOf is the workspace as the user reads it, with the footer under it and without the header, tab line
// and rule above it. The frame itself is tested on its own, through View.
func screenOf(m *Model) string {
	if m.quitting || m.terminalTooSmall() {
		return m.render()
	}
	m.screenKeys, m.screenPos = "", ""
	view := leftOfPane(m, m.workspaceView())
	return view + "\n" + m.footerLine()
}

// flatText is a view as one line of words, where the spacing between groups of keys does not matter.
func flatText(view string) string { return strings.Join(strings.Fields(view), " ") }

// withFooter is a view drawn by a single screen function with the footer under it, which the keys of that
// screen went to.
func withFooter(m *Model, view string) string { return view + "\n" + m.footerLine() }

// explained is the whole explanation of the focused row as one line of words: what the pane and the overlay
// show, while the form itself shows one line of hint.
func explained(m *Model) string {
	return strings.Join(strings.Fields(m.infoSubject().long), " ")
}

// leftOfPane is a screen without the help pane at its right, which most tests do not look at; paneOf reads it.
func leftOfPane(m *Model, view string) string {
	if !m.paneShown() {
		return view
	}
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		l = ansi.Truncate(l, m.width-helpPaneWidth, "")
		lines[i] = strings.TrimRight(danglingStyle.ReplaceAllString(strings.TrimRight(l, " "), ""), " ")
	}
	return strings.Join(lines, "\n")
}

// danglingStyle matches style sequences at the end of a line that style nothing.
var danglingStyle = regexp.MustCompile(`(\x1b\[[0-9;]*m)+$`)

// noPane sets the terminal one column below the width from which the help pane stands, keeping its height.
func noPane(m *Model) { m.Update(tea.WindowSizeMsg{Width: helpPaneMinWidth - 1, Height: m.termHeight}) }

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
	pressNew(t, m)
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
				pressNew(t, m)
				press(t, m, "f2")
			},
			wantIn: "name must not be empty",
		},
		{
			name: "a missing base url is refused by the core",
			build: func(t *testing.T, m *Model) {
				openSectionByName(t, m, sectionServices)
				pressNew(t, m)
				typeText(t, m, "wiki")
				press(t, m, "f2")
			},
			wantIn: "must not be empty",
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

			if shown := refusal(m); !strings.Contains(shown, tt.wantIn) {
				t.Errorf("refusal = %q, want it to contain %q", shown, tt.wantIn)
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
	press(t, m, "f2")

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
		pressNew(t, m)
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
	if len(m.problems.rows["token-id"]) == 0 {
		t.Errorf("problems %v, want one under the role row", m.problems.rows)
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
		pressNew(t, m)
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
	if view := screenOf(m); !strings.Contains(view, "must consist of letters, digits") {
		t.Errorf("view = %q, want the name row to state the name rule", view)
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

	// The frame of the field pads its line, so the cursor cell shows only as the reverse video it is drawn in.
	restoreRenderer(t)
	setProfile(profile16)
	m, _, _ := newModel(t)
	noPane(m)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	press(t, m, "tab", "tab") // name, provider, base url
	typeText(t, m, url)

	if got := m.fields[m.focus].label; got != "base url" {
		t.Fatalf("focused field = %q, want the base url", got)
	}
	atEnd := screenOf(m)
	if !strings.Contains(atEnd, url) {
		t.Fatalf("the typed value is missing from the form:\n%s", atEnd)
	}
	// The cursor sits past the last character, so the text input draws one more cell after the value; the
	// frame of the field pads the line, so the cell is looked for as the input draws it.
	if !strings.Contains(atEnd, m.fields[m.focus].input.View()) {
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
	pressNew(t, m)
	if view := screenOf(m); !strings.Contains(view, "a key you choose, without spaces") {
		t.Errorf("the name field has no hint:\n%s", view)
	}
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageEnv)
	if words := explained(m); !strings.Contains(words, "system keyring keeps the secrets in "+secret.StoreLabel(platform)) {
		t.Errorf("the form does not say what the secrets row decides: %s", words)
	}

	// The role rows are one kind of row, so the sentence about all of them stands only under the first, and
	// each row still keeps its own, provider-defined explanation once it is the one focused.
	focusField(t, m, "token-id")
	view := screenOf(m)
	words := explained(m)
	for _, want := range []string{
		"the NAME of an environment variable", "never the secret", "BookStack token ID", "every role row",
	} {
		if !strings.Contains(words, want) {
			t.Errorf("the credential form does not explain %q: %s", want, words)
		}
	}
	// The inline hint is the short form; the whole explanation is one key away.
	detail := strings.Join(strings.Fields(m.fieldDetail(m.fields[m.focus])), " ")
	for _, want := range []string{"value labeled Token ID", "not a name you choose"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail help of token-id does not say %q: %s", want, detail)
		}
	}
	if strings.Contains(words, "value labeled Token Secret") {
		t.Errorf("the token-id hint leaks the token-secret explanation:\n%s", view)
	}

	focusField(t, m, "token-secret")
	view = screenOf(m)
	words = explained(m)
	if detail := strings.Join(strings.Fields(m.fieldDetail(m.fields[m.focus])), " "); !strings.Contains(detail,
		"value labeled Token Secret") {
		t.Errorf("the detail help of token-secret does not explain %q: %s", "value labeled Token Secret", detail)
	}
	if strings.Contains(words, "every role row") {
		t.Errorf("the token-secret hint repeats the sentence shared by every role row:\n%s", view)
	}

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	if view := screenOf(m); !strings.Contains(view, tr(tDomainHint)) {
		t.Errorf("the service form does not show %q:\n%s", tr(tDomainHint), view)
	}
	if detail := m.fieldDetail(m.fields[m.focus]); !strings.Contains(detail, "--connection") {
		t.Errorf("the name detail does not say what the field is for: %q", detail)
	}
	focusField(t, m, "base url")
	if view := screenOf(m); !strings.Contains(view, tr(tFormBaseURLHint)) || !strings.Contains(view, "http or https") {
		t.Errorf("the base url field does not explain itself:\n%s", view)
	}

	m, _, _ = newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "wiki", "wiki", "reader")
	openSectionByName(t, m, sectionDefaults)
	press(t, m, "n")
	if view := screenOf(m); !strings.Contains(view, domainHint()) {
		t.Errorf("the domain field has no hint:\n%s", view)
	}
}

// openEntryForm opens the form of the single entry of a section.
func openEntryForm(t *testing.T, m *Model, s section, name string) {
	t.Helper()
	openSectionByName(t, m, s)
	m.list.selectName(name)
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
		press(t, m, "f2")
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
	focusField(t, m, "description")
	view := explained(m)
	for _, want := range []string{"what an agent uses this route for",
		"customer records", "discovery publishes it", "never carry a secret"} {
		if !strings.Contains(view, want) {
			t.Errorf("the connection form does not explain %q: %s", want, view)
		}
	}
	if !strings.Contains(screenOf(m), "description") {
		t.Errorf("the connection form lacks the description row:\n%s", screenOf(m))
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
	press(t, m, "f2")

	if shown := refusal(m); m.fail == "" || !strings.Contains(shown, "201 characters") {
		t.Errorf("refusal = %q, want the reason the core gave", shown)
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
	if view := listed(); strings.Contains(view, tr(tFormUndescribedMarker)) {
		t.Fatalf("a single connection is marked:\n%s", screenOf(m))
	}

	addConnection(t, m, "wiki-2", "wiki", "reader")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	view := listed()
	for _, want := range []string{"wiki active wiki read all - " + tr(tFormUndescribedMarker),
		"wiki-2 active wiki read all - " + tr(tFormUndescribedMarker), "add a line on what this route is for"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not say %q:\n%s", want, screenOf(m))
		}
	}
	for _, name := range []string{"wiki", "wiki-2"} {
		if got := m.describe(name); !strings.HasSuffix(got, tr(tFormUndescribedMarker)) {
			t.Errorf("list entry = %q, want it marked", got)
		}
	}
	if saved, err := store.Load(); err != nil || len(saved.Connections) != 2 {
		t.Fatalf("Load() = %v, %v; the mark must not keep a connection from being saved", saved, err)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, "description")
	typeText(t, m, "the live team wiki")
	press(t, m, "f2")
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
	pressNew(t, m)
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
	press(t, m, "f2")

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

// The note of a provider is edited in the form of one of its services. It is saved under the provider, a
// later save of the service keeps it, and clearing the row removes it again.
func TestServiceFormEditsTheProviderNote(t *testing.T) {
	m, store, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	focusNote := func() {
		t.Helper()
		press(t, m, "enter")
		for i := 0; i < len(m.fields) && m.fields[m.focus].label != providerNoteLabel; i++ {
			press(t, m, "tab")
		}
		if got := m.fields[m.focus].label; got != providerNoteLabel {
			t.Fatalf("focused field = %q, want the provider note", got)
		}
	}
	saved := func() *config.Config {
		t.Helper()
		if m.fail != "" {
			t.Fatalf("editor reported %q", m.fail)
		}
		cfg, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		return cfg
	}
	provider := saved().Services["wiki"].Provider

	focusNote()
	if hint := m.fieldHint(m.fields[m.focus]); !strings.Contains(hint, "no secrets") ||
		!strings.Contains(m.fieldDetail(m.fields[m.focus]), "never carry a secret") {
		t.Errorf("the note row does not warn that discovery publishes it: %q", hint)
	}
	typeText(t, m, " Wiki ")
	press(t, m, "f2")
	if got := saved().ProviderNotes[provider]; got != "Wiki" {
		t.Fatalf("saved note = %q, want Wiki", got)
	}

	// A save that changes only the base url keeps the note: it is read into the form and written back.
	press(t, m, "enter")
	for i := 0; i < len(m.fields) && m.fields[m.focus].label != "base url"; i++ {
		press(t, m, "tab")
	}
	if got := m.fieldValue(providerNoteLabel); got != "Wiki" {
		t.Errorf("the reopened form shows the note %q, want Wiki", got)
	}
	typeText(t, m, "/next")
	press(t, m, "f2")
	if cfg := saved(); cfg.ProviderNotes[provider] != "Wiki" ||
		cfg.Services["wiki"].BaseURL != "https://wiki.example.invalid/next" {
		t.Errorf("after a base url change: note %q, base url %q", cfg.ProviderNotes[provider],
			cfg.Services["wiki"].BaseURL)
	}

	focusNote()
	clearField(t, m)
	press(t, m, "f2")
	if notes := saved().ProviderNotes; notes != nil {
		t.Errorf("notes = %#v, want none after clearing the row", notes)
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
	if got := m.envSource(""); got != tr(tSecretSourceUnnamed) {
		t.Errorf("envSource() = %q, want %q", got, tr(tSecretSourceUnnamed))
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
	view := m.render()
	for _, want := range []string{
		"created on first save",
		"c setup",
		"1 Overview",
		"2 Connections",
		"3 Activity",
		"4 Access",
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
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "Press n for the guided setup") {
		t.Errorf("empty Connections does not point to the guided setup:\n%s", view)
	}
	press(t, m, "n")
	if m.wizard == nil || m.screen != screenProviders || m.fail != "" {
		t.Fatalf("n without a service and a credential: screen %v error %q, want the guided setup", m.screen, m.fail)
	}
	press(t, m, "esc")

	openSectionByName(t, m, sectionDefaults)
	if view := strings.Join(strings.Fields(strings.NewReplacer("│", " ", "║", " ").Replace(screenOf(m))), " "); !strings.Contains(view, "Create a connection") ||
		!strings.Contains(view, "Defaults are optional") {
		t.Errorf("empty Defaults does not explain its prerequisite:\n%s", view)
	}
}

func TestNewKeyringCredentialContinuesWithItsSecrets(t *testing.T) {
	m, _, path := newModel(t)

	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	typeText(t, m, "wiki-reader")
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageKeyring)
	press(t, m, "tab")

	before := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(before, "F2 save credential first") {
		t.Errorf("new credential does not explain the first save:\n%s", before)
	}
	detail := strings.Join(strings.Fields(m.fieldDetail(m.fields[m.focus])), " ")
	if !strings.Contains(before, "BookStack token ID") || !strings.Contains(detail, "value labeled Token ID") ||
		!strings.Contains(detail, "not a name you choose") {
		t.Errorf("new credential does not explain token-id:\n%s\n%s", before, detail)
	}
	press(t, m, "f2")

	if m.screen != screenForm || m.editing != "wiki-reader" {
		t.Fatalf("screen = %v editing = %q, want the saved credential form", m.screen, m.editing)
	}
	if m.fields[m.focus].kind != fieldSecret {
		t.Fatalf("focused field kind = %v, want a secret role", m.fields[m.focus].kind)
	}
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	for _, want := range []string{"Credential saved", "enter on a role", "system keyring"} {
		if !strings.Contains(view, want) {
			t.Errorf("continued credential form does not contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "(recommended)") {
		t.Errorf("continued credential form still recommends a place:\n%s", view)
	}
	if strings.Contains(view, "created on first save") {
		t.Errorf("the saved file is still described as not created:\n%s", view)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("first save did not create the configuration: %v", err)
	}
}

// formLines is the view without the padding the styles add and without the frames of the panels and fields,
// so a test can look at the shape of the form (a row is its label and its value, a hint is indented under
// it) rather than at the cells that fill it out.
func formLines(m *Model) []string {
	lines := strings.Split(screenOf(m), "\n")
	entries := m.formSections()
	list := 0
	full := m.width
	if m.paneShown() {
		m.width -= helpPaneWidth
	}
	if m.sectionListFits(entries) {
		_, list = m.sectionList(entries)
		list++
	}
	m.width = full
	var out []string
	for _, l := range lines {
		if runes := []rune(l); list > 0 && len(runes) >= list {
			// The section list stands left of the section panel; the rows are read without them.
			if head := strings.TrimSpace(string(runes[:list])); head == "" || strings.HasPrefix(head, "│") ||
				strings.HasPrefix(head, "╭") || strings.HasPrefix(head, "╰") {
				l = string(runes[list:])
			}
		}
		l = strings.TrimRight(l, " ")
		if r := []rune(l); list > 0 && len(r) > 2 && r[0] == '│' && r[len(r)-1] == '│' {
			// A line of the section panel.
			l = strings.TrimRight(string(r[2:len(r)-1]), " ")
		}
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "╭") || strings.HasPrefix(t, "╰") {
			continue
		}
		if i := strings.Index(l, "│ "); i > 0 && strings.HasSuffix(l, "│") {
			// A framed field: its label, then its value without the frame and the arrow.
			l = l[:i] + strings.TrimRight(strings.TrimSuffix(strings.TrimRight(l[i+len("│ "):len(l)-len("│")], " "), "▾"), " ")
		}
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

// fieldLine is the line the given field is drawn on. Field lines carry the two-cell margin or the focus
// marker; hint lines are indented further, so they cannot be mistaken for one.
func fieldLine(t *testing.T, lines []string, label string) int {
	t.Helper()
	at, ok := findFieldLine(lines, label)
	if !ok {
		t.Fatalf("field %q is not on screen:\n%s", label, strings.Join(lines, "\n"))
	}
	return at
}

// findFieldLine is fieldLine for a field that may stand in another section than the one shown.
func findFieldLine(lines []string, label string) (int, bool) {
	for i, l := range lines {
		body := strings.TrimPrefix(l, "> ")
		if body == l {
			body = strings.TrimPrefix(l, "  ")
			if body == l {
				continue
			}
		}
		if strings.HasPrefix(body, labelText(label)) {
			return i, true
		}
	}
	return -1, false
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
	if !strings.Contains(view, "read-only") || !strings.Contains(view, "rename with r on the list") {
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
	press(t, m, "f2")

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
	press(t, m, "f2")
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

// A hint belongs to the field above it: it stands directly under its own row, only while that row is
// focused, in one line. No other field keeps a hint of its own meanwhile.
func TestAHintBelongsToTheFieldAboveIt(t *testing.T) {
	for _, width := range []int{100, 46} {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: width})
		openSectionByName(t, m, sectionCredentials)
		pressNew(t, m)
		press(t, m, "tab")
		press(t, m, "tab")
		selectChoice(t, m, storageEnv)

		for _, f := range m.fields {
			focusField(t, m, f.label)
			lines := formLines(m)
			at := fieldLine(t, lines, f.label)
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
			for j := at + 1; j < len(lines) && strings.HasPrefix(lines[j], "    "); j++ {
				height++
			}
			if height == 0 {
				t.Errorf("width %d: the hint of %q does not stand under it:\n%s",
					width, f.label, strings.Join(lines, "\n"))
			}
			if height != 1 {
				t.Errorf("width %d: the hint of %q takes %d lines, want one:\n%s",
					width, f.label, height, strings.Join(lines, "\n"))
			}
			// Every other field's row follows without a hint of its own while this one is focused.
			for _, other := range m.fields {
				if other.label == f.label || other.readOnly {
					continue
				}
				oat, shown := findFieldLine(lines, other.label)
				if shown && oat+1 < len(lines) && strings.HasPrefix(lines[oat+1], "    ") {
					t.Errorf("width %d: field %q keeps a hint while %q is focused:\n%s",
						width, other.label, f.label, strings.Join(lines, "\n"))
				}
			}
		}
	}
}

// The same sentence never stands twice under one another. What all role rows have in common is said once,
// under the first role row alone, and only while a role row is the one focused.
func TestNoHintStandsTwice(t *testing.T) {
	m, _, _ := newModel(t)

	openSectionByName(t, m, sectionCredentials)
	pressNew(t, m)
	press(t, m, "tab")
	press(t, m, "tab")
	selectChoice(t, m, storageEnv)
	assertNoRepeatedHint(t, m, "the env credential form")

	focusField(t, m, "token-id")
	assertNoRepeatedHint(t, m, "the env credential form, token-id focused")
	if got := strings.Count(explained(m), "the NAME of an environment"); got != 1 {
		t.Errorf("the env sentence stands %d times, want once: %s", got, explained(m))
	}
	focusField(t, m, "token-secret")
	assertNoRepeatedHint(t, m, "the env credential form, token-secret focused")
	if strings.Contains(explained(m), "the NAME of an environment") {
		t.Errorf("the sentence shared by every role row repeats under token-secret: %s", explained(m))
	}

	// The keyring rows build their hint themselves, out of the keys and the stages the resolver checked, and
	// each keeps its own even though only the focused row's hint is shown at a time.
	addKeyringCredential(t, m, "store")
	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "enter")
	if m.screen != screenForm {
		t.Fatalf("screen = %v, want the form of the keyring credential", m.screen)
	}
	assertNoRepeatedHint(t, m, "the keyring credential form")

	for i, role := range m.cfg.SecretRoles() {
		focusField(t, m, role)
		view := screenOf(m)
		words := explained(m)
		assertNoRepeatedHint(t, m, "the keyring credential form, "+role+" focused")
		// The keys are said once, under the first role row, like the sentence an env credential shares.
		want := 0
		if i == 0 {
			want = 1
		}
		if got := strings.Count(words, "d remove; typing is masked"); got != want {
			t.Errorf("role %q: the secret keys stand %d times, want %d:\n%s", role, got, want, view)
		}
		if got := strings.Count(words, "checked:"); got != 1 {
			t.Errorf("role %q: the checked stages appear %d times, want once: %s", role, got, words)
		}
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

// A form shows exactly the hint of the focused field, in the same place directly under it, whether the
// terminal is large enough to hold every hint at once or not: a terminal large enough is not a license to
// show them all, because that inconsistency is exactly what a fixed, size-independent hint replaces. The
// default form is used because its two rows fit even at the smallest supported size, 40x13.
func TestFormShowsOnlyTheFocusedHintAtEverySize(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 40}, {80, 24}, {40, minimumHeight}} {
		m, _, _ := newModel(t)
		addService(t, m, "wiki", "https://wiki.example.invalid")
		addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
		addConnection(t, m, "wiki", "wiki", "reader")
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		openSectionByName(t, m, sectionDefaults)
		press(t, m, "n")
		if m.activeScreenTooSmall() {
			t.Fatalf("%dx%d: the new default form does not fit", size.width, size.height)
		}

		// A fresh default form opens on "domain", so its hint is the one that should show.
		view := screenOf(m)
		words := strings.Join(strings.Fields(view), " ")
		lines := formLines(m)
		at := fieldLine(t, lines, "domain")
		if !strings.Contains(words, domainHint()[:12]) {
			t.Errorf("%dx%d: the focused field's hint is missing:\n%s", size.width, size.height, view)
		}
		if !strings.HasPrefix(lines[at+1], "    ") {
			t.Errorf("%dx%d: the hint does not stand directly under the focused field:\n%s",
				size.width, size.height, view)
		}

		// Moving focus moves the hint away: "connection" has none of its own, so none stands under it either.
		focusField(t, m, "connection")
		view = screenOf(m)
		words = strings.Join(strings.Fields(view), " ")
		if strings.Contains(words, domainHint()[:12]) {
			t.Errorf("%dx%d: the previous field's hint survived the focus change:\n%s",
				size.width, size.height, view)
		}
		lines = formLines(m)
		at = fieldLine(t, lines, "connection")
		if at+1 < len(lines) && strings.HasPrefix(lines[at+1], "    ") {
			t.Errorf("%dx%d: a field without a hint shows an indented line under it:\n%s",
				size.width, size.height, view)
		}
	}
}

// Going a step back in the guided setup used to reopen the multi-field page in whatever density the
// terminal size last produced, so a large enough terminal suddenly showed every hint at once. Coming back
// keeps the same single, focused hint every other form shows, at any size.
func TestReturningToASetupStepShowsOnlyTheFocusedHint(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	walkSetup(t, m, stepEffects)
	if m.wizard.step != stepEffects {
		t.Fatalf("walkSetup landed on step %d, want %d", m.wizard.step, stepEffects)
	}
	assertNoRepeatedHint(t, m, "the permissions step")
	if blocks := hintBlocks(formLines(m)); len(blocks) != 1 {
		t.Fatalf("the permissions step shows %d hint blocks, want exactly 1:\n%s", len(blocks), screenOf(m))
	}

	pump(t, m, "f2")
	if m.wizard.step != stepSummary {
		t.Fatalf("f2 did not reach the summary: step %d, error %q", m.wizard.step, m.fail)
	}
	pump(t, m, "esc")
	if m.wizard.step != stepEffects {
		t.Fatalf("esc did not return to the permissions step: step %d", m.wizard.step)
	}

	assertNoRepeatedHint(t, m, "the permissions step after going back")
	if blocks := hintBlocks(formLines(m)); len(blocks) != 1 {
		t.Errorf("the permissions step shows %d hint blocks after going back, want exactly 1:\n%s",
			len(blocks), screenOf(m))
	}
}

// Opening the tool picker and leaving it again must not turn the form behind it into the all-hints view
// either: the same single, focused hint is what a person returns to.
func TestReturningFromAPickerShowsOnlyTheFocusedHint(t *testing.T) {
	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, accessLabel)
	selectChoice(t, m, accessTools)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	if m.screen != screenPicker {
		t.Fatalf("enter on the tool list did not open the picker: screen %v", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("esc did not return to the form: screen %v", m.screen)
	}

	assertNoRepeatedHint(t, m, "the connection form after returning from the picker")
	if blocks := hintBlocks(formLines(m)); len(blocks) != 1 {
		t.Errorf("the connection form shows %d hint blocks after the picker, want exactly 1:\n%s",
			len(blocks), screenOf(m))
	}
}

// Naming the provider of an existing credential is one choice and one save. Replacing the role rows must
// not take the type row with them: without it the entry would be written with no type at all, and the
// core would refuse the file the editor just produced.
func TestNamingTheProviderKeepsTheRestOfTheCredential(t *testing.T) {
	m, store, path := newModel(t)

	addKeyringCredential(t, m, "bookstack-personal")
	// The name of an existing entry is read-only and the provider row is passed over, so the form opens on
	// the secrets row.
	editEntry(t, m, "bookstack-personal")
	if got := m.fields[m.focus].label; got != storageLabel {
		t.Fatalf("the form opened on %q, want the secrets row", got)
	}
	focusField(t, m, providerLabel)
	selectChoice(t, m, "bookstack")
	if got := m.credentialType(); got != config.CredentialTypeKeyring {
		t.Fatalf("type after choosing the provider = %q, want the keyring it was created as", got)
	}
	press(t, m, "tab")
	pump(t, m, "f2")
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

// formView is the form as formLines reads it: rows and hints without the frames around fields and panels.
func formView(m *Model) string { return strings.Join(formLines(m), "\n") }

// sectionViews is the form as formView reads it with every open section shown in turn; the focus ends where
// it began.
func sectionViews(m *Model) string {
	focus := m.focus
	var b strings.Builder
	for _, e := range m.formSections() {
		if !e.closed {
			m.enterSection(e.name)
			b.WriteString(formView(m) + "\n")
		}
	}
	m.focus = focus
	m.applyFocus()
	return b.String()
}

// View hands the drawing to render unchanged.
func TestViewWrapsRender(t *testing.T) {
	m, _, _ := newModel(t)
	if got, want := m.View(), m.render(); got != want {
		t.Fatalf("View differs from render:\n%q\n%q", got, want)
	}
}
