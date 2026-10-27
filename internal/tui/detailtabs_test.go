package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The canaries stand where secrets would; none of them may reach the raw tab.
const (
	canaryURLPass = "canary-url-pass"
	canaryQuery   = "canary-query"
	canaryOption  = "canary-option"
	canaryVault   = "canary-vault-value"
	canaryEnv     = "canary env: secret value"
	canaryUser    = "canary-user-token"
)

// rawModel is a model whose entries hold secret-like values and texts with control characters.
func rawModel(t *testing.T) *Model {
	t.Helper()
	m := newTableModel(t, 140)
	m.cfg.Services["wiki-main"] = config.Service{Provider: "bookstack",
		BaseURL: "https://user:" + canaryURLPass + "@wiki.example.invalid/api?token=" + canaryQuery + "&page=1",
		Options: map[string]string{"api_key": canaryOption, "region": "eu\x1b[31mred\x07"}}
	m.cfg.Services["token-host"] = config.Service{Provider: "bookstack",
		BaseURL: "https://" + canaryUser + "@git.example.invalid/api"}
	m.cfg.Credentials["vault-cred"] = config.Credential{Provider: "tracker", Type: config.CredentialTypeVault,
		Values: map[string]string{"token": canaryVault}}
	m.cfg.Credentials["env-cred"] = config.Credential{Provider: "tracker", Type: config.CredentialTypeEnv,
		Values: map[string]string{"token": canaryEnv}}
	conn := m.cfg.Connections["tracker-issues"]
	conn.Description = "issues\x1b[31m red\x07 and\x1b]0;title\x07 more\x00"
	conn.TargetLabels = map[string]string{"octo/test-repo": "main\x1b[2Jrepo"}
	m.cfg.Connections["tracker-issues"] = conn
	return m
}

// showRaw focuses the pane of the marked entry and switches to its raw tab.
func showRaw(t *testing.T, m *Model) string {
	t.Helper()
	press(t, m, "tab")
	press(t, m, "right")
	press(t, m, "right")
	if m.shownTab() != tabRaw {
		t.Fatalf("the shown tab = %v, want the raw tab", m.shownTab())
	}
	return screenOf(m)
}

// The services table and its filter text show a base URL without the credentials in it.
func TestServicesTableAndFilterTextHoldNoURLCredential(t *testing.T) {
	m := rawModel(t)
	openSectionByName(t, m, sectionServices)
	canaries := []string{canaryURLPass, canaryQuery, canaryUser}
	for _, name := range m.list.all {
		if isGroupRow(name) {
			continue
		}
		m.list.selectName(name)
		shown := map[string]string{
			"screen":   screenOf(m),
			"cells":    strings.Join(m.cells(name), "\n"),
			"describe": m.describe(name),
		}
		for where, text := range shown {
			for _, c := range canaries {
				if strings.Contains(text, c) {
					t.Errorf("%q: the %s shows %q:\n%s", name, where, c, text)
				}
			}
		}
	}
}

// The raw tab shows the entry as written, but never a secret value and never a control character.
func TestDetailRawHoldsNoSecretAndNoControlCharacter(t *testing.T) {
	before := currentProfile()
	setProfile(profileNone)
	t.Cleanup(func() { setProfile(before) })

	m := rawModel(t)
	canaries := []string{canaryUser, canaryURLPass, canaryQuery, canaryOption, canaryVault, "canary env", "secret value"}
	for _, s := range []section{sectionServices, sectionCredentials, sectionConnections} {
		openSectionByName(t, m, s)
		for _, name := range m.list.all {
			if isGroupRow(name) {
				continue
			}
			m.leaveDetail()
			m.screen = screenList
			m.list.selectName(name)
			view := showRaw(t, m)
			for _, c := range canaries {
				if strings.Contains(view, c) {
					t.Errorf("%v %q: the raw tab shows %q:\n%s", s, name, c, view)
				}
			}
			if strings.ContainsAny(view, "\x1b\x07\x00") {
				t.Errorf("%v %q: a control character reached the raw tab: %q", s, name, view)
			}
		}
	}
	openSectionByName(t, m, sectionCredentials)
	m.list.selectName("tracker-bot")
	if view := showRaw(t, m); !strings.Contains(view, "TRACKER_TOKEN") || !strings.Contains(view, "type: env") {
		t.Errorf("the raw tab omits the entry:\n%s", view)
	}
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	if view := showRaw(t, m); !strings.Contains(view, "tracker-main") || !strings.Contains(view, "description:") {
		t.Errorf("the raw tab omits the connection:\n%s", view)
	}
}

// tab leads from the rows through the pane and the bar to the tab line; left and right change the tab only
// while the pane has the focus, and it shows the overview whenever it has not.
func TestDetailFocusCycleAndTabs(t *testing.T) {
	m := newTableModel(t, 140)
	for _, s := range []section{sectionConnections, sectionServices, sectionCredentials} {
		openSectionByName(t, m, s)
		m.list.selectName(shownEntries(m)[0])
		press(t, m, "right")
		if m.detailFocused() || m.shownTab() != tabOverview {
			t.Fatalf("%v: right on the rows changed the pane", s)
		}
		press(t, m, "tab")
		if !m.detailFocused() || m.barFocus != 0 {
			t.Fatalf("%v: tab on the rows = pane %v bar %d, want the pane", s, m.detailFocused(), m.barFocus)
		}
		press(t, m, "right")
		if view := screenOf(m); m.shownTab() != tabDetails || !strings.Contains(view, "[Details]") {
			t.Fatalf("%v: right in the pane shows tab %v:\n%s", s, m.shownTab(), view)
		}
		press(t, m, "left")
		press(t, m, "left")
		if m.shownTab() != tabOverview {
			t.Fatalf("%v: left stops at the overview, shows %v", s, m.shownTab())
		}
		press(t, m, "right")
		press(t, m, "shift+tab")
		if m.detailFocused() || m.shownTab() != tabOverview || !strings.Contains(screenOf(m), "[Overview]") {
			t.Fatalf("%v: shift+tab did not return to the rows with the overview", s)
		}
		press(t, m, "tab")
		press(t, m, "tab")
		if m.detailFocused() || m.barFocus != 1 {
			t.Fatalf("%v: tab in the pane = pane %v bar %d, want the first bar field", s, m.detailFocused(), m.barFocus)
		}
		press(t, m, "shift+tab")
		if !m.detailFocused() {
			t.Fatalf("%v: shift+tab on the first bar field did not return to the pane", s)
		}
		press(t, m, "esc")
		if m.detailFocused() || m.screen != screenList {
			t.Fatalf("%v: esc in the pane = pane %v screen %v, want the rows", s, m.detailFocused(), m.screen)
		}
		press(t, m, "tab")
		press(t, m, "right")
		press(t, m, "down")
		if !m.detailFocused() || m.shownTab() != tabDetails {
			t.Fatalf("%v: down in the pane lost it", s)
		}
		press(t, m, "esc")
	}
}

// A click on a tab of the pane shows it and gives the pane the focus; a click on a row takes it away again.
func TestDetailTabsAreClickable(t *testing.T) {
	m := newTableModel(t, 140)
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	m.render()
	clickOn(t, m, "Raw")
	if !m.detailFocused() || m.shownTab() != tabRaw {
		t.Fatalf("click on Raw = pane %v tab %v", m.detailFocused(), m.shownTab())
	}
	m.render()
	clickOn(t, m, "Details")
	if m.shownTab() != tabDetails {
		t.Fatalf("click on Details shows %v", m.shownTab())
	}
	m.render()
	// The tab line of the areas has an Overview of its own, so the tab is found in the row of Raw.
	_, row := cellOf(t, m, "Raw")
	clickAt(t, m, cellInRow(t, m, row, "Overview"), row)
	if m.shownTab() != tabOverview || !m.detailFocused() {
		t.Fatalf("click on Overview = tab %v pane %v", m.shownTab(), m.detailFocused())
	}
	m.render()
	clickOn(t, m, "tracker-triage")
	if m.detailFocused() {
		t.Errorf("a click on a row left the focus on the pane")
	}
}

// Without colour the shown tab stands in brackets and the focused pane has the double frame; the pane fits
// at the smallest size that has one, in each of the three lists.
func TestDetailPaneReadsWithoutColourAtItsSmallestSize(t *testing.T) {
	before := currentProfile()
	setProfile(profileNone)
	saved := styles
	setTheme(buildTheme(true, true, false))
	t.Cleanup(func() { setProfile(before); setTheme(saved) })

	// The smallest height that has a pane in all three lists is the one that frames their tables.
	smallest := tableFrameMinHeight
	for ; smallest <= 40; smallest++ {
		m := newTableModel(t, detailPaneMinWidth)
		m.Update(tea.WindowSizeMsg{Width: detailPaneMinWidth, Height: smallest})
		all := true
		for _, s := range []section{sectionConnections, sectionServices, sectionCredentials} {
			openSectionByName(t, m, s)
			all = all && m.paneWidth() > 0
		}
		if all {
			break
		}
	}
	for _, size := range []struct{ w, h int }{{detailPaneMinWidth, smallest}, {detailPaneMinWidth, 30}, {200, 50}} {
		m := newTableModel(t, size.w)
		m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for _, s := range []section{sectionConnections, sectionServices, sectionCredentials} {
			openSectionByName(t, m, s)
			m.list.selectName(shownEntries(m)[0])
			m.render()
			view := screenOf(m)
			assertViewFits(t, view, size.w, size.h)
			if !strings.Contains(view, "[Overview]") || strings.Contains(view, "\x1b") {
				t.Errorf("%v %dx%d: the tabs do not read without colour:\n%s", s, size.w, size.h, view)
			}
			press(t, m, "tab")
			press(t, m, "right")
			view = screenOf(m)
			assertViewFits(t, view, size.w, size.h)
			if !strings.Contains(view, "[Details]") || !strings.Contains(view, "╔") {
				t.Errorf("%v %dx%d: the focused pane shows no double frame or tab:\n%s", s, size.w, size.h, view)
			}
			press(t, m, "right")
			assertViewFits(t, screenOf(m), size.w, size.h)
			press(t, m, "esc")
		}
	}
}

// Under the width threshold there is no pane and tab keeps going from the rows to the filter bar.
func TestDetailPaneIsLeftOutBelowItsWidth(t *testing.T) {
	m := newTableModel(t, detailPaneMinWidth-1)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "tab")
	if m.detailFocused() || m.barFocus != 1 {
		t.Errorf("tab without a pane = pane %v bar %d, want bar field 1", m.detailFocused(), m.barFocus)
	}
	if strings.Contains(ansi.Strip(screenOf(m)), "[Overview]") {
		t.Errorf("a pane shows below its width")
	}
}
