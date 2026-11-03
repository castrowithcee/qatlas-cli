package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// previewPage puts the tab line on a settings page, the way the arrows of the option bar do.
func previewPage(t *testing.T, m *Model, s section) {
	t.Helper()
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 42})
	m.screen = screenNav
	pump(t, m, string(rune('1'+s.area())))
	pump(t, m, "esc")
	for i := 0; i < len(areaSections[s.area()]) && m.section != s; i++ {
		pump(t, m, "right")
	}
	if m.section != s || m.screen != screenNav {
		t.Fatalf("preview of %v: section %v screen %v", s.title(), m.section.title(), m.screen)
	}
}

func TestSettingsPageShowsFormWhenChosen(t *testing.T) {
	for _, s := range []section{sectionGeneral, sectionVault, sectionTransfer, sectionUpdate, sectionSelftest} {
		m, _ := lockedVaultModel(t)
		previewPage(t, m, s)
		view := shown(m.View().Content)
		if strings.Contains(view, "not a list") || strings.Contains(view, "press enter to open it") {
			t.Errorf("%v shows the old note:\n%s", s.title(), view)
		}
		if !m.pagePreview() || !strings.Contains(view, m.fields[0].label) && !strings.Contains(view, labelText(m.fields[0].label)) {
			t.Errorf("%v does not show its form:\n%s", s.title(), view)
		}
		if m.screen != screenNav {
			t.Errorf("%v preview took the focus: screen %v", s.title(), m.screen)
		}
	}
}

func TestSettingsPageFocusEntersTheFormAndEscReturns(t *testing.T) {
	for _, key := range []string{"enter", "down", "tab"} {
		m, _ := lockedVaultModel(t)
		previewPage(t, m, sectionGeneral)
		// The first down only leaves the tab line for the option bar.
		m.navSub = true
		pump(t, m, key)
		if m.screen != screenForm || m.section != sectionGeneral {
			t.Fatalf("%s: screen %v section %v, want the General form", key, m.screen, m.section.title())
		}
		pump(t, m, "esc")
		if m.screen != screenNav || !m.pagePreview() {
			t.Fatalf("esc after %s: screen %v, want the navigation with the form as preview", key, m.screen)
		}
	}
}

func TestSettingsPageKeysStayOnTheNavigationUntilFocused(t *testing.T) {
	m, _ := lockedVaultModel(t)
	previewPage(t, m, sectionGeneral)
	m.navSub = true
	pump(t, m, "right")
	if m.section != sectionVault || m.screen != screenNav {
		t.Fatalf("right = %v on screen %v, want Vault previewed", m.section.title(), m.screen)
	}
	pump(t, m, "n")
	if m.screen != screenNav || m.fail != "" {
		t.Errorf("n on a page: screen %v fail %q, want nothing", m.screen, m.fail)
	}
}

func TestSettingsPageClickOnTabAndField(t *testing.T) {
	m, _ := lockedVaultModel(t)
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 42})
	m.screen = screenNav
	pump(t, m, "6")
	pump(t, m, "esc")
	clickOn(t, m, "Update")
	if m.section != sectionUpdate || m.screen != screenNav || !m.pagePreview() {
		t.Fatalf("click on the tab: section %v screen %v", m.section.title(), m.screen)
	}
	clickOn(t, m, "General")
	if m.section != sectionGeneral || m.screen != screenNav || !m.pagePreview() {
		t.Fatalf("click on General: section %v screen %v", m.section.title(), m.screen)
	}
	_ = m.View()
	clickOn(t, m, labelText(m.fields[1].label))
	if m.screen != screenForm || m.focus != 1 {
		t.Fatalf("click on a row: screen %v focus %d, want the form on row 1", m.screen, m.focus)
	}
	if m.dirty() {
		t.Errorf("the click changed the form")
	}
}

func TestSettingsPageLeaveStillAsksAfterChange(t *testing.T) {
	m, _ := lockedVaultModel(t)
	previewPage(t, m, sectionGeneral)
	m.navSub = true
	pump(t, m, "enter")
	press(t, m, "right")
	pump(t, m, "esc")
	if m.screen != screenLeave {
		t.Fatalf("esc on a changed form = screen %v, want the leave question", m.screen)
	}
	pump(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("keep editing = screen %v", m.screen)
	}
}

func TestDefaultsStaysAList(t *testing.T) {
	m, _ := lockedVaultModel(t)
	previewPage(t, m, sectionDefaults)
	if m.pagePreview() || m.section.isPage() {
		t.Errorf("Defaults is a settings page")
	}
	m.navSub = true
	pump(t, m, "enter")
	if m.screen != screenList {
		t.Errorf("enter on Defaults = screen %v, want its list", m.screen)
	}
}
