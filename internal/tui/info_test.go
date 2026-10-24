package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// connectionForm opens the form of a new connection at the given terminal width.
func connectionForm(t *testing.T, width int) *Model {
	t.Helper()
	m, _ := toolsModel(t, wikiRegistry(t), nil)
	m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	return m
}

func TestInfoKeyIsBoundToI(t *testing.T) {
	if keyOf(actInfo) != "i" || keyLabel(actInfo) != "i" {
		t.Errorf("the info action is bound to %q, want i", keyOf(actInfo))
	}
}

// A text row keeps its letters: i is typed there and opens nothing.
func TestInfoDoesNotActOnTextRows(t *testing.T) {
	m := connectionForm(t, helpPaneMinWidth-1)
	if m.fields[m.focus].label != "name" {
		t.Fatalf("focus on %q, want the name row", m.fields[m.focus].label)
	}
	press(t, m, "i")
	if m.infoOpen || m.fieldValue("name") != "i" {
		t.Errorf("i on a text row: overlay %v, value %q, want the letter typed", m.infoOpen, m.fieldValue("name"))
	}
	if strings.Contains(screenOf(m), "(i)") || strings.Contains(screenOf(m), "i info") {
		t.Errorf("a text row promises an info key:\n%s", screenOf(m))
	}
}

// A search line takes i as text once it holds something, and as the info key while it is empty.
func TestInfoActsOnASearchOnlyWhileItIsEmpty(t *testing.T) {
	m := connectionForm(t, helpPaneMinWidth-1)
	focusField(t, m, accessLabel)
	selectChoice(t, m, accessTools)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	if m.screen != screenPicker {
		t.Fatalf("screen %v, want the tool picker", m.screen)
	}
	typeText(t, m, "p")
	press(t, m, "i")
	if m.infoOpen || m.picker.query() != "pi" {
		t.Errorf("i in a filled search: overlay %v, query %q, want the letter typed", m.infoOpen, m.picker.query())
	}
	press(t, m, "backspace", "backspace")
	press(t, m, "i")
	if !m.infoOpen {
		t.Fatalf("i in an empty search opened nothing:\n%s", m.View())
	}
	press(t, m, "esc")
	if m.infoOpen || m.screen != screenPicker {
		t.Errorf("esc left overlay %v on screen %v, want the picker alone", m.infoOpen, m.screen)
	}
}

// The overlay shows the long form and the term of the row, and esc closes it without leaving the screen.
func TestInfoOverlayShowsTheLongFormAndTheTermAndClosesWithEsc(t *testing.T) {
	m := connectionForm(t, helpPaneMinWidth-1)
	focusField(t, m, "credential")
	press(t, m, "i")
	if !m.infoOpen {
		t.Fatalf("i on the credential row opened nothing:\n%s", m.View())
	}
	view := strings.Join(strings.Fields(m.View()), " ")
	for _, want := range []string{"the provider credential this route authenticates with", "Credential: A credential says where",
		"esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, m.View())
		}
	}
	press(t, m, "x", "enter")
	if !m.infoOpen {
		t.Error("another key closed the overlay")
	}
	press(t, m, "esc")
	if m.infoOpen || m.screen != screenForm || m.fields[m.focus].label != "credential" {
		t.Errorf("after esc: overlay %v, screen %v, focus %q", m.infoOpen, m.screen, m.fields[m.focus].label)
	}
}

// Below the pane width a row with a long form ends its hint in (i); from the pane width on the pane stands
// and no row carries the mark.
func TestInfoMarkBelowThePaneWidthAndNoneAbove(t *testing.T) {
	for _, tt := range []struct {
		width int
		pane  bool
	}{{helpPaneMinWidth - 1, false}, {helpPaneMinWidth, true}, {200, true}} {
		m := connectionForm(t, tt.width)
		focusField(t, m, "credential")
		view := m.View()
		if got := strings.Contains(view, "(i)"); got == tt.pane {
			t.Errorf("width %d: mark shown %v, want %v:\n%s", tt.width, got, !tt.pane, view)
		}
		if got := strings.Contains(view, "Credential: A credential"); got != tt.pane {
			t.Errorf("width %d: pane shown %v, want %v:\n%s", tt.width, got, tt.pane, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if w := len([]rune(line)); w > 0 && strings.Contains(line, "(i)") && tt.pane {
				t.Errorf("width %d: a mark stands beside the pane: %q", tt.width, line)
			}
		}
	}
}

// The footer names the info key where it works, and not on a text row.
func TestInfoFooterNamesTheKeyWhereItWorks(t *testing.T) {
	m := connectionForm(t, 100)
	if strings.Contains(screenOf(m), "i info") {
		t.Errorf("the name row names an info key:\n%s", screenOf(m))
	}
	focusField(t, m, "credential")
	if !strings.Contains(screenOf(m), "i info") {
		t.Errorf("the credential row does not name the info key:\n%s", screenOf(m))
	}
	focusField(t, m, accessLabel)
	press(t, m, "esc")
}

// A picker explains the marked tool from its description, and marks tools with one below the pane width.
func TestInfoExplainsTheMarkedEntryOfAPicker(t *testing.T) {
	m := connectionForm(t, helpPaneMinWidth-1)
	focusField(t, m, accessLabel)
	selectChoice(t, m, accessTools)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	// Unfold the groups; the first tool is the entry to explain.
	if m.pickerGroups != nil {
		for id := range m.pickerGroups.collapsed {
			m.pickerGroups.collapsed[id] = false
		}
		m.picker.refilter()
	}
	for i, name := range m.picker.matches {
		if !strings.HasPrefix(name, "\x00") {
			m.picker.cursor = i
			break
		}
	}
	choice, _ := m.picker.selected()
	var want string
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	for _, tool := range metadata.Tools {
		if tool.ID == choice {
			want = tool.Description
		}
	}
	if want == "" {
		t.Fatalf("tool %q has no description to show", choice)
	}
	if !strings.Contains(m.View(), "(i)") {
		t.Errorf("a described tool carries no (i):\n%s", m.View())
	}
	press(t, m, "i")
	if got := strings.Join(strings.Fields(m.View()), " "); !m.infoOpen || !strings.Contains(got, strings.Fields(want)[0]) {
		t.Errorf("the overlay does not show the description %q:\n%s", want, m.View())
	}
	press(t, m, "esc")

	m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	view := m.View()
	if strings.Contains(view, "(i)") || !strings.Contains(strings.Join(strings.Fields(view), " "), strings.Fields(want)[0]) {
		t.Errorf("the pane does not replace the mark:\n%s", view)
	}
}

// A list explains the area it belongs to.
func TestInfoOnAListShowsTheTermOfTheArea(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	press(t, m, "i")
	if got := strings.Join(strings.Fields(m.View()), " "); !m.infoOpen || !strings.Contains(got, "Service: A service is one instance") {
		t.Errorf("i on the services list shows no term:\n%s", m.View())
	}
	press(t, m, "esc")
	if m.infoOpen {
		t.Error("esc did not close the overlay")
	}
}

// The tool metadata carries the description the registry holds.
func TestToolMetadataCarriesTheDescription(t *testing.T) {
	reg := wikiRegistry(t)
	for _, d := range reg.Provider("bookstack") {
		metadata, _ := reg.ProviderMetadata("bookstack")
		for _, tool := range metadata.Tools {
			if tool.ID == d.ID && tool.Description != d.Description {
				t.Errorf("tool %s: description %q, want %q", d.ID, tool.Description, d.Description)
			}
		}
	}
}
