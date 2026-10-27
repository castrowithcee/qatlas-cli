package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// overviewModel is an editor as New leaves it: on the Overview, tab line in focus.
func overviewModel(t *testing.T, m *Model) *Model {
	t.Helper()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m.openSection(sectionOverview)
	m.focusNav()
	return m
}

// New opens the Overview with the tab line in focus; the Overview shows no option bar, shows the notes, and
// takes the focus into its lines.
func TestEditorStartsOnTheOverview(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newResolver(t, dir, nil)
	m, err := New(testService(store, secrets, nil), store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	if m.screen != screenNav || m.section != sectionOverview || m.section.area() != areaOverview {
		t.Fatalf("start = screen %v section %v, want the tab line on the Overview", m.screen, m.section)
	}
	m.status = "Something happened"
	view := m.render()
	for _, want := range []string{"[1 Overview]", "Something happened", "Connections", "Next steps"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Overview does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "─ Vault ") || !strings.Contains(view, "none configured") {
		t.Errorf("a missing vault is not told in a framed element:\n%s", view)
	}
	if strings.Contains(view, "Library:") {
		t.Errorf("the Overview shows an option bar:\n%s", view)
	}
	press(t, m, "down")
	if m.screen != screenList {
		t.Errorf("down = screen %v, want the lines of the Overview", m.screen)
	}
	press(t, m, "tab")
	if m.screen != screenNav {
		t.Errorf("tab = screen %v, want the tab line", m.screen)
	}
}

// The counts follow the configuration and the areas that hold them; a card shows the label in its top border and the value inside.
func TestOverviewCountsMatchTheAreas(t *testing.T) {
	m, _, _ := newEnvModel2(t)
	overviewModel(t, m)
	for _, want := range []overviewItem{{label: "Connections", value: "0"}, {label: "Services", value: "0"},
		{label: "Credentials", value: "0"}, {label: "Tokens", value: "needs encrypted vault", dim: true},
		{label: "Approvals", value: "needs encrypted vault", dim: true}} {
		assertCard(t, m, want)
	}
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	addConnection(t, m, "personal", "wiki", "reader")
	overviewModel(t, m)
	for _, want := range []overviewItem{{label: "Connections", value: "1"}, {label: "Services", value: "1"},
		{label: "Credentials", value: "1"}} {
		assertCard(t, m, want)
	}
	if len(m.cfg.Connections) != 1 || len(m.cfg.Services) != 1 || len(m.cfg.Credentials) != 1 {
		t.Fatalf("configuration = %d connections, %d services, %d credentials", len(m.cfg.Connections),
			len(m.cfg.Services), len(m.cfg.Credentials))
	}
}

// assertCard checks the tile of the label and that the view shows its value in the card under its titled border.
func assertCard(t *testing.T, m *Model, want overviewItem) {
	t.Helper()
	var got *overviewItem
	for _, tile := range m.overviewTiles() {
		if tile.label == want.label {
			got = &tile
		}
	}
	if got == nil || got.value != want.value || got.dim != want.dim {
		t.Fatalf("tile %q = %+v, want value %q (dim %v)", want.label, got, want.value, want.dim)
	}
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	for i, line := range lines {
		// The label sits in the top border: "╭─ Label ", the value on the line below, after "│ ".
		at := -1
		for _, mark := range []string{"─ " + want.label + " ", "═ " + want.label + " ", "─ > " + want.label + " ", "═ > " + want.label + " "} {
			if j := strings.Index(line, mark); j >= 0 {
				at = len([]rune(line[:j])) - 1
				break
			}
		}
		if at < 0 || i+1 >= len(lines) {
			continue
		}
		next := []rune(lines[i+1])
		if at+2 <= len(next) && (strings.HasPrefix(string(next[at:]), "│ "+want.value) || strings.HasPrefix(string(next[at:]), "║ "+want.value)) {
			return
		}
	}
	t.Errorf("no card %q over %q:\n%s", want.label, want.value, m.render())
}

// newEnvModel2 is newModel with the secrets the connection walk needs.
func newEnvModel2(t *testing.T) (*Model, *config.Store, string) {
	t.Helper()
	return newEnvModel(t, map[string]string{"WIKI_ID": canaryID, "WIKI_SECRET": canarySecret})
}

// An unencrypted vault is a next step, and enter on it opens Settings > Vault.
func TestOverviewNamesAnUnencryptedVaultAndEnterOpensIt(t *testing.T) {
	m := overviewModel(t, unencryptedVaultModel(t))
	view := strings.Join(strings.Fields(m.render()), " ")
	if !strings.Contains(view, "open Settings > Vault (6) to set a passphrase") {
		t.Fatalf("the Overview names no step for the unencrypted vault:\n%s", m.render())
	}
	if steps := m.nextSteps(); steps[0].target != sectionVault {
		t.Fatalf("first step = %+v, want the Vault", steps[0])
	}
	press(t, m, "down")
	_, items, _ := m.overviewLines()
	for i, item := range items {
		if item.target == sectionVault {
			m.overviewCursor = i
		}
	}
	if line := m.render(); !strings.Contains(line, "> open Settings > Vault") {
		t.Errorf("the selection is not marked without colour:\n%s", line)
	}
	press(t, m, "enter")
	if m.section != sectionVault || m.screen != screenForm {
		t.Errorf("enter = section %v screen %v, want the Vault form", m.section, m.screen)
	}
}

// Open approvals are a step, count as a tile, and enter on either opens Access > Approvals.
func TestOverviewNamesOpenApprovalsAndEnterOpensThem(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	m := openApprovalModel(t, store, dir)
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	overviewModel(t, m)

	view := strings.Join(strings.Fields(m.render()), " ")
	assertCard(t, m, overviewItem{label: "Approvals", value: "1"})
	if want := "open Access > Approvals (4)"; !strings.Contains(view, want) {
		t.Fatalf("the Overview does not show %q:\n%s", want, m.render())
	}
	for _, label := range []string{"Approvals", "open Access"} {
		overviewModel(t, m)
		press(t, m, "down")
		_, items, _ := m.overviewLines()
		m.overviewCursor = -1
		for i, item := range items {
			if strings.Contains(item.text, label) {
				m.overviewCursor = i
				break
			}
		}
		if m.overviewCursor < 0 {
			t.Fatalf("no line %q among %v", label, items)
		}
		press(t, m, "enter")
		if m.section != sectionApprovals || m.screen != screenList {
			t.Errorf("enter on %q = section %v screen %v, want the Approvals list", label, m.section, m.screen)
		}
	}
}

// Up and down move over the counts and the steps, up from the first line returns to the tab line, and enter on
// a count opens the area of that count.
func TestOverviewSelectionMovesAndOpensTheCountsArea(t *testing.T) {
	m := overviewModel(t, mustModel(t))
	press(t, m, "down")
	press(t, m, "down", "down")
	if m.overviewCursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.overviewCursor)
	}
	press(t, m, "enter")
	if m.section != sectionCredentials || m.screen != screenList {
		t.Errorf("enter on Credentials = section %v screen %v", m.section, m.screen)
	}
	overviewModel(t, m)
	m.overviewCursor = 0
	press(t, m, "down", "up")
	if m.screen != screenNav {
		t.Errorf("up from the first line = screen %v, want the tab line", m.screen)
	}
}

func mustModel(t *testing.T) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	return m
}

// Every terminal size keeps the counts on screen and the view inside the terminal.
func TestOverviewFitsEverySize(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := overviewModel(t, unencryptedVaultModel(t))
		for _, size := range []struct{ width, height int }{{40, minimumHeight}, {60, 16}, {80, 24}, {200, 60}} {
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			view := m.render()
			assertViewFits(t, view, size.width, size.height)
			if !strings.Contains(view, sectionApprovals.title()) || !strings.Contains(view, sectionConnections.title()) {
				t.Errorf("%dx%d lacks the counts:\n%s", size.width, size.height, view)
			}
		}
	})
}

// The Overview names the running version with and without an updater; an empty value reads as dev, and the
// vault state of the header is not repeated while the framed panel for a missing vault stays.
func TestOverviewShowsRunningVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		updater Updater
		want    string
	}{
		{"no updater", "1.2.3", nil, "qatlas 1.2.3"},
		{"updater without an update", "1.2.3", &fakeUpdater{}, "qatlas 1.2.3"},
		{"empty", "", nil, "qatlas dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store := newTestStore(t, filepath.Join(dir, "config.yaml"))
			secrets, _ := newResolver(t, dir, nil)
			m, err := New(testService(store, secrets, nil), store, nil, secrets, nil)
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			m.version, m.updater = tc.version, tc.updater
			overviewModel(t, m)
			view := m.render()
			if !strings.Contains(view, tc.want) {
				t.Errorf("the Overview does not show %q:\n%s", tc.want, view)
			}
			if !strings.Contains(view, "─ Vault ") || !strings.Contains(view, "none configured") {
				t.Errorf("the missing-vault panel is gone:\n%s", view)
			}
			if lines := strings.Count(view, "none configured"); lines != 1 {
				t.Errorf("the vault state is shown %d times, want once:\n%s", lines, view)
			}
		})
	}
}
