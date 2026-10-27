package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every list screen carries its title line above the panels, with the count and the position, as Settings
// does; the frame starts under the gap, and the title line is plain text without colour as well.
func TestListScreensTitleAboveThePanels(t *testing.T) {
	before := currentProfile()
	t.Cleanup(func() { setProfile(before) })
	for _, profile := range []colorProfile{profileNone, profile16} {
		setProfile(profile)
		for _, s := range []section{sectionConnections, sectionServices, sectionCredentials} {
			m := newTableModel(t, 100)
			openSectionByName(t, m, s)
			lines := strings.Split(ansi.Strip(screenOf(m)), "\n")
			if !strings.HasPrefix(lines[0], s.title()+" (") {
				t.Errorf("%v: first line is not the title with its count:\n%s", s, strings.Join(lines, "\n"))
				continue
			}
			if lines[1] != "" || !strings.ContainsAny(lines[2], "╭╔") {
				t.Errorf("%v: the title has no gap above the frame:\n%s", s, strings.Join(lines, "\n"))
			}
			if strings.Contains(lines[2], s.title()) {
				t.Errorf("%v: the frame still carries the title:\n%s", s, lines[2])
			}
		}
	}
}

// With no height to spare the gap under the title goes, not a row of the table, and at the smallest size the
// title stands without a frame and the footer stays whole.
func TestListTitleIsCompactWhereHeightIsShort(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: tableFrameMinHeight + 4})
	if m.titleBlank() {
		t.Fatal("the gap under the title is kept at a height that only just holds the frame")
	}
	lines := strings.Split(ansi.Strip(screenOf(m)), "\n")
	if !strings.HasPrefix(lines[0], "Connections (") || !strings.ContainsAny(lines[1], "╭╔") {
		t.Errorf("compact title:\n%s", strings.Join(lines, "\n"))
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: smallestListHeight})
	view := screenOf(m)
	assertViewFits(t, view, 40, smallestListHeight+1)
	if !strings.HasPrefix(ansi.Strip(view), "Connections") || !strings.Contains(view, "? help") {
		t.Errorf("the smallest size loses its title or its footer:\n%s", view)
	}
}

// The panels of the pickers and the dialog screens stand under their title line.
func TestPickerAndDialogTitlesAboveThePanel(t *testing.T) {
	m := newTableModel(t, 100)
	p := m.dialogPanelOf()
	got := strings.Split(ansi.Strip(p.top("Export: what to take", "1/3")), "\n")
	if !strings.HasPrefix(got[0], "Export: what to take") || !strings.HasSuffix(got[0], "1/3") ||
		!strings.ContainsAny(got[m.titleRows()], "╭╔") || strings.Contains(got[m.titleRows()], "Export") {
		t.Errorf("dialog title:\n%s", strings.Join(got, "\n"))
	}
	box := strings.Split(ansi.Strip(m.boxTop("Choose a value", "")), "\n")
	if !strings.HasPrefix(box[0], "Choose a value") || !strings.ContainsAny(box[m.titleRows()], "╭╔") {
		t.Errorf("picker title:\n%s", strings.Join(box, "\n"))
	}
}

// The clicks follow the layout the title line shifts: a click on a field of the filter bar and on a row hit
// what is drawn there.
func TestClicksFollowTheTitleLine(t *testing.T) {
	m := newTableModel(t, 140)
	openSectionByName(t, m, sectionConnections)
	m.render()
	_, y := cellOf(t, m, "Sort:")
	if want := m.workspaceTop() + m.titleRows() + 1; y != want {
		t.Fatalf("the filter bar stands in row %d, want %d", y, want)
	}
	clickOn(t, m, "Sort:")
	if m.barFocus == 0 && m.dd == nil {
		t.Errorf("a click on a field of the filter bar did nothing:\n%s", ansi.Strip(m.render()))
	}
	press(t, m, "esc")
	m.render()
	clickOn(t, m, "tracker-triage")
	if got, _ := m.list.selected(); got != "tracker-triage" {
		t.Errorf("a click on a row selected %q", got)
	}
}
