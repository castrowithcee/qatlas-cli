package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// configBytes is the content of the configuration file, or a marker where it does not exist yet.
func configBytes(m *Model) string {
	if m.store == nil {
		return fmt.Sprintf("%#v", *m.cfg)
	}
	b, err := os.ReadFile(m.store.Path())
	if err != nil {
		return "absent"
	}
	return string(b)
}

// enter opens the list under the field, up and down move, enter takes the value and esc drops it; left and right
// change the value without opening anything. None of it touches the configuration.
func TestDropdownKeysOpenChooseAndCancel(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)
	before := configBytes(m)

	press(t, m, "tab", "right")
	if m.dd != nil || !m.opts(sectionConnections).flat {
		t.Fatalf("right opened a list (%v) or left Group at %v", m.dd != nil, m.opts(sectionConnections).flat)
	}
	press(t, m, "left", "enter")
	if m.dd == nil || m.barFocus != 1 {
		t.Fatalf("enter did not open the list of the Group field")
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "> Provider") || !strings.Contains(view, "  None") {
		t.Fatalf("the list does not show the values:\n%s", view)
	}
	press(t, m, "esc")
	if m.dd != nil || m.opts(sectionConnections).flat || m.barFocus != 1 {
		t.Fatalf("esc did not close the list unchanged: open %v flat %v bar %d", m.dd != nil, m.opts(sectionConnections).flat, m.barFocus)
	}
	press(t, m, "enter", "down", "enter")
	if m.dd != nil || !m.opts(sectionConnections).flat || m.groups != nil {
		t.Fatalf("enter did not take None: open %v flat %v", m.dd != nil, m.opts(sectionConnections).flat)
	}
	press(t, m, "down")
	if m.barFocus != 0 {
		t.Errorf("down no longer leaves the bar for the rows")
	}
	if after := configBytes(m); after != before || m.dirty() {
		t.Error("the dropdown changed the configuration")
	}
}

// The search field shows the placeholder while idle and the typed text afterwards.
func TestSearchFieldShowsPlaceholderAndText(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := newTableModel(t, 100)
		openSectionByName(t, m, sectionConnections)
		want := tr(tSearchField)
		if !strings.Contains(want, "…") || !strings.HasPrefix(want, "/ ") {
			t.Fatalf("placeholder = %q", want)
		}
		if line := ansi.Strip(m.barLine(m.listWidth())); !strings.Contains(line, want) {
			t.Fatalf("the idle bar lacks %q: %q", want, line)
		}
		press(t, m, "/")
		typeText(t, m, "wiki")
		line := ansi.Strip(m.barLine(m.listWidth()))
		if !strings.Contains(line, "/ wiki") || strings.Contains(line, want) {
			t.Fatalf("the typed search is not in the field: %q", line)
		}
		press(t, m, "enter")
		if line := ansi.Strip(m.barLine(m.listWidth())); !strings.Contains(line, "/ wiki") || !strings.Contains(line, "Provider ▾") {
			t.Fatalf("the kept search vanished from the bar: %q", line)
		}
	})
}

// Where the width does not suffice the value is cut with an ellipsis and label and arrow stay.
func TestDropdownFieldCutsItsValue(t *testing.T) {
	got := ansi.Strip(dropdownField("Group", "Provider", false, 14))
	if lipgloss.Width(got) > 14 || !strings.Contains(got, "Group: ") || !strings.Contains(got, "…") || !strings.Contains(got, "▾") {
		t.Errorf("field = %q", got)
	}
	m := newTableModel(t, 60)
	openSectionByName(t, m, sectionConnections)
	line := ansi.Strip(m.barLine(m.listWidth()))
	if lipgloss.Width(line) > m.listWidth() || !strings.Contains(line, "Group: ") || !strings.Contains(line, "▾") {
		t.Errorf("the bar at 60 = %q", line)
	}
}

// At the smallest size the bar stays operable: the focused field shows, enter opens its list, and it fits.
func TestDropdownAtTheSmallestSize(t *testing.T) {
	m := newTableModel(t, minimumWidth)
	m.Update(tea.WindowSizeMsg{Width: minimumWidth, Height: minimumHeight})
	openSectionByName(t, m, sectionServices)
	press(t, m, "tab", "tab", "enter")
	if m.dd == nil {
		t.Fatalf("enter did not open the list at 40x13:\n%s", m.render())
	}
	view := m.render()
	assertViewFits(t, view, minimumWidth, minimumHeight)
	if plain := ansi.Strip(view); !strings.Contains(plain, "Sort: Name ▾") || !strings.Contains(plain, "Provider") {
		t.Fatalf("the open list is not usable at 40x13:\n%s", plain)
	}
	press(t, m, "down", "enter")
	if !m.opts(sectionServices).byProvider {
		t.Error("Provider was not chosen as the sort")
	}
}

// Without colour the focus reads from the marker, and the field and its list carry no colour sequence.
func TestDropdownReadsWithoutColour(t *testing.T) {
	before := currentProfile()
	prior := styles
	t.Cleanup(func() { setProfile(before); setTheme(prior) })
	t.Setenv("NO_COLOR", "1")
	applyTheme(uiprefs.ThemeTerminal)
	setProfile(profile256)

	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "tab", "enter")
	view := m.render()
	if hasColour(view) {
		t.Errorf("the view carries a colour under NO_COLOR:\n%q", view)
	}
	if plain := ansi.Strip(view); !strings.Contains(plain, ">  Group: Provider ▾") || !strings.Contains(plain, "> Provider") {
		t.Errorf("the focus is not readable without colour:\n%s", plain)
	}
}

// Activity: enter opens the list of the mode, the tabs change with left and right, the date keeps its dialog, and
// a click on a filter field opens its picker.
func TestActivityDropdowns(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
	m := f.model(true, f.localToday())

	press(t, m, "tab", "tab", "right")
	if m.dd != nil || m.logs.mode != logModeRange {
		t.Fatalf("right on Mode: open %v mode %v", m.dd != nil, m.logs.mode)
	}
	press(t, m, "enter")
	if m.dd == nil {
		t.Fatal("enter on Mode did not open its list")
	}
	press(t, m, "esc")
	if m.dd != nil || m.logs.mode != logModeRange {
		t.Fatalf("esc: open %v mode %v", m.dd != nil, m.logs.mode)
	}
	press(t, m, "enter", "down", "enter")
	if m.dd != nil || m.logs.mode != logModeAll {
		t.Fatalf("enter did not take All: mode %v", m.logs.mode)
	}
	press(t, m, "shift+tab", "right")
	if m.dd != nil || m.logs.focus != logFocusTabs || !m.logs.heat {
		t.Error("right on the tabs did not show the heatmap")
	}

	clickOn(t, m, "Client: all")
	if m.logs.pick == nil || m.logs.pick.filter != logFilterClient {
		t.Fatalf("a click on the Client field did not open its picker")
	}
}
