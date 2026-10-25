package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The values of the filter bar change what the list shows and in which order, and leave the configuration
// exactly as it is.
func TestFilterBarShapesTheListAndNeverTheConfiguration(t *testing.T) {
	m := newTableModel(t, 100)
	conn := m.cfg.Connections["tracker-triage"]
	conn.Disabled = true
	m.cfg.Connections["tracker-triage"] = conn
	before := fmt.Sprintf("%#v", *m.cfg)
	openSectionByName(t, m, sectionConnections)
	if got := shownEntries(m); !reflect.DeepEqual(got, []string{"wiki-docs", "tracker-issues", "tracker-triage"}) {
		t.Fatalf("default list = %v", got)
	}

	press(t, m, "tab")
	if m.barFocus != 1 || !strings.Contains(m.barLine(80), ">  Group: Provider ▾") {
		t.Fatalf("tab did not reach the Group field: bar %d, line %q", m.barFocus, m.barLine(80))
	}
	press(t, m, "right")
	if m.groups != nil || len(m.list.matches) != 3 {
		t.Fatalf("Group none: groups %v, rows %v", m.groups != nil, m.list.matches)
	}
	press(t, m, "left")
	if m.groups == nil {
		t.Fatal("Group provider did not bring the groups back")
	}

	press(t, m, "tab", "right")
	if got := shownEntries(m); !reflect.DeepEqual(got, []string{"wiki-docs", "tracker-issues"}) {
		t.Fatalf("State active = %v", got)
	}
	press(t, m, "right")
	if got := shownEntries(m); !reflect.DeepEqual(got, []string{"tracker-triage"}) {
		t.Fatalf("State inactive = %v", got)
	}
	press(t, m, "right")
	if got := shownEntries(m); len(got) != 3 {
		t.Fatalf("State all = %v", got)
	}

	// Sorted by provider and ungrouped, the providers stand in order, the names inside each of them.
	press(t, m, "tab", "right")
	press(t, m, "shift+tab", "shift+tab", "right")
	if got := shownEntries(m); !reflect.DeepEqual(got, []string{"wiki-docs", "tracker-issues", "tracker-triage"}) {
		t.Fatalf("sorted by provider, ungrouped = %v", got)
	}
	press(t, m, "tab", "tab", "left")
	press(t, m, "shift+tab", "shift+tab", "left")
	if got := shownEntries(m); !reflect.DeepEqual(got, []string{"wiki-docs", "tracker-issues", "tracker-triage"}) {
		t.Fatalf("grouped again = %v", got)
	}

	if after := fmt.Sprintf("%#v", *m.cfg); after != before {
		t.Error("the filter bar changed the configuration")
	}
	if m.dirty() {
		t.Error("the filter bar left the editor with unsaved changes")
	}
}

// Credentials and services have no state, so their bar has two fields; left/right on the rows still fold.
func TestFilterBarOfStatelessListsHasNoStateField(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionCredentials)
	if got := barKinds(m.section); len(got) != 2 {
		t.Fatalf("Credentials bar fields = %v", got)
	}
	if line := m.barLine(100); strings.Contains(line, "State") {
		t.Errorf("Credentials bar shows a state: %q", line)
	}
	m.list.selectName(groupRow("tracker"))
	press(t, m, "left")
	if !m.groups.collapsed["tracker"] {
		t.Error("left on the rows no longer folds the group")
	}
	press(t, m, "tab", "tab", "tab")
	if m.screen != screenNav {
		t.Errorf("tab after the last field = screen %v, want the tab line", m.screen)
	}
}

// A narrow terminal shows the focused field alone, and the bar without focus as its values.
func TestFilterBarNarrowShowsTheFocusedField(t *testing.T) {
	m := newTableModel(t, 40)
	openSectionByName(t, m, sectionConnections)
	if line := m.barLine(m.width); !strings.Contains(line, "group provider") {
		t.Errorf("the bar without focus = %q, want its values", line)
	}
	press(t, m, "tab", "tab")
	if line := m.barLine(m.width); !strings.Contains(line, ">  State: All ▾") ||
		strings.Contains(line, "Group") {
		t.Errorf("the focused field = %q", line)
	}
	assertViewFits(t, screenOf(m), 40, m.termHeight)
}

// The detail pane appears from detailPaneMinWidth and not below it.
func TestDetailPaneAppearsAtItsMinimumWidth(t *testing.T) {
	for _, c := range []struct {
		width int
		want  bool
	}{{detailPaneMinWidth, true}, {detailPaneMinWidth - 1, false}} {
		m := newTableModel(t, c.width)
		openSectionByName(t, m, sectionConnections)
		m.list.selectName("tracker-issues")
		view := screenOf(m)
		assertViewFits(t, view, c.width, m.termHeight)
		if has := strings.Contains(view, "Credential:") && strings.Contains(view, "Base URL:"); has != c.want {
			t.Errorf("width %d: detail pane shown = %v, want %v:\n%s", c.width, has, c.want, view)
		}
	}
}

// The pane holds the facts of the marked entry and no secret value, and text from the configuration reaches
// it without control characters.
func TestDetailPaneIsSafe(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(3)
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	m := newTableModel(t, 140)
	conn := m.cfg.Connections["tracker-issues"]
	conn.Description = "issues\x1b[31m red\x07 and\x1b]0;title\x07 more"
	conn.TargetLabels = map[string]string{"octo/test-repo": "main\x1b[2Jrepo"}
	m.cfg.Connections["tracker-issues"] = conn
	for _, s := range []section{sectionConnections, sectionCredentials, sectionServices} {
		openSectionByName(t, m, s)
		for _, name := range m.list.all {
			m.list.selectName(name)
			view := screenOf(m)
			assertNoCanary(t, "the detail pane", view)
			for _, secretValue := range []string{"canary-tracker-token", "canary-wiki-id"} {
				if strings.Contains(view, secretValue) {
					t.Errorf("%v %q: the pane shows a secret value %q", s, name, secretValue)
				}
			}
			if strings.Contains(view, "\x1b") || strings.Contains(view, "\x07") {
				t.Errorf("%v %q: a control character reached the screen: %q", s, name, view)
			}
		}
	}
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	if view := screenOf(m); !strings.Contains(view, "issues") || !strings.Contains(view, "Description:") {
		t.Errorf("the description is missing from the pane:\n%s", view)
	}
}

// The last test of a connection shows in its pane once the model holds it.
func TestDetailPaneShowsTheLastTest(t *testing.T) {
	m := newTableModel(t, 130)
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	if view := screenOf(m); strings.Contains(view, "Last test") {
		t.Errorf("a pane shows a test nobody ran:\n%s", view)
	}
	m.testName, m.testClass = "tracker-issues", "auth"
	if view := screenOf(m); !strings.Contains(view, "Last test:") {
		t.Errorf("the pane omits the last test:\n%s", view)
	}
}
