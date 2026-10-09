package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/provider/todoist"
)

// groupedPicker opens the tool picker of a new-style GitHub connection, ready for typing, at the given size.
func groupedPicker(t *testing.T, width int) (*Model, string, *capability.Registry) {
	t.Helper()
	reg := capability.NewRegistry()
	if err := github.Register(reg); err != nil {
		t.Fatal(err)
	}
	m, path := toolsModel(t, reg, map[string]config.Connection{"repo": {Service: "wiki", Credential: "reader",
		Target: "repos/octo-org/example"}})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	openEntryForm(t, m, sectionConnections, "repo")
	focusField(t, m, toolsLabel)
	selectChoice(t, m, toolsSelected)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	if m.screen != screenPicker || m.pickerGroups == nil {
		t.Fatalf("the GitHub tool list did not open the grouped picker (screen %v)", m.screen)
	}
	return m, path, reg
}

func githubToolIDs(t *testing.T, m *Model) []string {
	t.Helper()
	metadata, _ := m.cfg.ProviderMetadata("github")
	ids := make([]string, 0, len(metadata.Tools))
	for _, tool := range metadata.Tools {
		ids = append(ids, tool.ID)
	}
	return ids
}

func shownRows(m *Model) []string {
	var rows []string
	for _, line := range strings.Split(screenOf(m), "\n") {
		rows = append(rows, strings.TrimSpace(line))
	}
	return rows
}

func TestTheGroupedPickerStartsFoldedAndFoldsWithLeftRight(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	view := screenOf(m)
	if !strings.Contains(view, "0/") || !strings.Contains(view, "ticked") || !strings.Contains(view, "all groups") {
		t.Fatalf("the grouped picker lacks its head or the all-groups row:\n%s", view)
	}
	if strings.Contains(view, "github.issues.") || !strings.Contains(view, "▸ ") || strings.Contains(view, "▾ ") {
		t.Fatalf("the groups do not start folded:\n%s", view)
	}
	press(t, m, "down", "right")
	view = screenOf(m)
	if !strings.Contains(view, "▾ ") || !strings.Contains(view, "github.") {
		t.Fatalf("right did not open the group under the cursor:\n%s", view)
	}
	press(t, m, "down", "left")
	if view = screenOf(m); strings.Contains(view, "▾ ") || strings.Contains(view, "[ ] github.") {
		t.Fatalf("left on a tool did not fold its group:\n%s", view)
	}
	if choice, _ := m.picker.selected(); !strings.HasPrefix(choice, groupRowPrefix) {
		t.Fatalf("the cursor did not follow to the group row: %q", choice)
	}
}

func TestSpaceTicksAGroupAndAllGroupsAndSearchFindsFoldedTools(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	first := m.pickerGroups.groups[0]
	members := m.pickerGroups.members[first.ID]
	press(t, m, "down", " ")
	for _, tool := range members {
		if !m.pickerMarks[tool] {
			t.Fatalf("group toggle left %s unticked", tool)
		}
	}
	if ticked, _ := m.pickerGroups.count(m.pickerMarks); ticked != len(members) {
		t.Fatalf("group toggle ticked %d, want %d", ticked, len(members))
	}
	press(t, m, " ")
	if len(m.pickerMarks) != 0 {
		t.Fatalf("a second toggle left %d ticks", len(m.pickerMarks))
	}
	press(t, m, "up", " ")
	if ticked, total := m.pickerGroups.count(m.pickerMarks); ticked != total || total == 0 {
		t.Fatalf("all on gave %d/%d", ticked, total)
	}
	press(t, m, " ")
	if len(m.pickerMarks) != 0 {
		t.Fatal("all off left ticks")
	}

	// A search shows a tool of a folded group under its group row, and space then acts on the tool.
	typeText(t, m, "workflowfiles.get")
	view := screenOf(m)
	if !strings.Contains(view, "github.workflowfiles.get") || !strings.Contains(view, "Actions") || !m.pickerGroups.collapsed["actions"] {
		t.Fatalf("the search does not show the tool under its folded group:\n%s", view)
	}
	press(t, m, "down", "down", " ")
	if !m.pickerMarks["github.workflowfiles.get"] || len(m.pickerMarks) != 1 {
		t.Fatalf("ticks = %v, want only github.workflowfiles.get", m.pickerMarks)
	}
	// While searching, a group row acts on the matching tools only.
	press(t, m, "up", " ")
	if len(m.pickerMarks) != 0 {
		t.Fatalf("group toggle within a search reached %v", m.pickerMarks)
	}
	if view := screenOf(m); !strings.Contains(view, "0/") {
		t.Fatalf("head lost its count:\n%s", view)
	}
}

func TestTheGroupedPickerSavesTheSameListAsTheFlatOrder(t *testing.T) {
	m, path, reg := groupedPicker(t, 100)
	group := m.pickerGroups.groups[len(m.pickerGroups.groups)-1]
	typeText(t, m, "github.issues.get")
	press(t, m, "down", "down", " ") // the all row is shown, then the group row, then the tool
	want := map[string]bool{}
	for k, v := range m.pickerMarks {
		want[k] = v
	}
	// A whole group as well, added with the search cleared.
	for range "github.issues.get" {
		press(t, m, "backspace")
	}
	for i := 0; i < len(m.pickerGroups.groups); i++ {
		if choice, _ := m.picker.selected(); choice == groupRowPrefix+group.ID {
			break
		}
		press(t, m, "down")
	}
	press(t, m, " ")
	for _, tool := range m.pickerGroups.members[group.ID] {
		want[tool] = true
	}
	press(t, m, "enter")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	var expected []string
	for _, id := range githubToolIDs(t, m) {
		if want[id] {
			expected = append(expected, id)
		}
	}
	if got := savedTools(t, path, reg, "repo"); !reflect.DeepEqual(got, expected) || len(got) < 2 {
		t.Fatalf("saved tools = %v, want %v", got, expected)
	}
}

func TestTheGroupedPickerFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{79, 80} {
		m, _, _ := groupedPicker(t, width)
		press(t, m, "down", " ", "right")
		for _, view := range []string{screenOf(m)} {
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > width {
					t.Errorf("width %d: line of %d cells: %q", width, lipgloss.Width(line), line)
				}
			}
		}
		typeText(t, m, "issues")
		for _, line := range strings.Split(screenOf(m), "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d (search): line of %d cells: %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

// A provider without groups keeps the flat picker.
func TestAFlatProviderKeepsTheFlatPicker(t *testing.T) {
	reg := capability.NewRegistry()
	if err := todoist.Register(reg); err != nil {
		t.Fatal(err)
	}
	m, _ := toolsModel(t, reg, map[string]config.Connection{"route": {Service: "wiki", Credential: "reader", Target: "*"}})
	openEntryForm(t, m, sectionConnections, "route")
	focusField(t, m, toolsLabel)
	selectChoice(t, m, toolsSelected)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	if m.screen != screenPicker || m.pickerGroups != nil || strings.Contains(screenOf(m), "all groups") {
		t.Fatalf("a flat provider got a grouped picker:\n%s", screenOf(m))
	}
}
