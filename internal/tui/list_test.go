package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// seedServices stores services directly, so a test can start from a list longer than any terminal.
func seedServices(t *testing.T, m *Model, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := m.cfg.SetService(name, config.Service{
			Provider: "bookstack", BaseURL: "https://" + name + ".example.invalid",
		}); err != nil {
			t.Fatalf("SetService(%q) = %v", name, err)
		}
	}
	if err := m.store.Save(m.cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	m.configExists = true
}

func numberedServices(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("svc-%03d", i)
	}
	return names
}

func selectedName(t *testing.T, m *Model) string {
	t.Helper()
	name, ok := m.selected()
	if !ok {
		t.Fatalf("nothing is selected; shown %v", m.list.matches)
	}
	return name
}

// A list longer than the terminal scrolls inside it: every entry can be reached, the selection is always
// on screen, and the position says where it is.
func TestALongListScrollsInsideTheTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 16}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _, _ := newModel(t)
			names := numberedServices(120)
			seedServices(t, m, names...)
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			openSectionByName(t, m, sectionServices)

			check := func(want string, position int) {
				t.Helper()
				view := screenOf(m)
				if strings.Contains(view, "Resize terminal") {
					t.Fatalf("the list was replaced by the resize notice:\n%s", view)
				}
				assertViewFits(t, view, size.width, size.height)
				if got := selectedName(t, m); got != want {
					t.Fatalf("selected %q, want %q", got, want)
				}
				if !strings.Contains(view, "> ") || !strings.Contains(view, want) {
					t.Fatalf("the selected entry %q is not on screen:\n%s", want, view)
				}
				if counter := fmt.Sprintf("%d/%d", position, len(names)); !strings.Contains(view, counter) {
					t.Fatalf("the view does not say %q:\n%s", counter, view)
				}
			}

			check(names[0], 1)
			for i := 1; i < len(names); i++ {
				press(t, m, "down")
				check(names[i], i+1)
			}
			press(t, m, "down")
			check(names[0], 1)
			press(t, m, "up")
			check(names[119], 120)
			press(t, m, "home")
			check(names[0], 1)
			press(t, m, "pgdown")
			if got := m.list.cursor; got < 1 {
				t.Fatalf("pgdown moved to %d, want at least one entry further", got)
			}
			check(names[m.list.cursor], m.list.cursor+1)
			press(t, m, "end")
			check(names[119], 120)
			press(t, m, "pgup")
			check(names[m.list.cursor], m.list.cursor+1)
		})
	}
}

// Filtering ignores case, looks at what the row shows, and never touches the configuration.
func TestFilterIgnoresCaseAndLeavesTheConfigurationAlone(t *testing.T) {
	m, _, path := newModel(t)
	seedServices(t, m, append(numberedServices(100), "Wiki-Primary", "wiki-archive", "chat")...)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openSectionByName(t, m, sectionServices)

	press(t, m, "/")
	typeText(t, m, "WIKI")
	view := screenOf(m)
	for _, want := range []string{"Wiki-Primary", "wiki-archive", "1/2 (103 total)", "filter: WIKI"} {
		if !strings.Contains(view, want) {
			t.Errorf("the filtered list does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "chat") || strings.Contains(view, "svc-000") {
		t.Errorf("the filter shows entries that do not match:\n%s", view)
	}

	// enter keeps the filter and gives the keys back to the list.
	press(t, m, "enter", "down")
	if got := selectedName(t, m); got != "wiki-archive" {
		t.Errorf("selected %q, want wiki-archive", got)
	}
	if view := screenOf(m); !strings.Contains(view, "2/2 (103 total)") {
		t.Errorf("the position is not shown:\n%s", view)
	}

	// Digits and list keys are filter text while it is typed, not shortcuts.
	press(t, m, "/")
	clearField(t, m)
	typeText(t, m, "svc-01")
	if m.section != sectionServices || m.screen != screenList {
		t.Fatalf("typing a filter left the list: screen %v section %v", m.screen, m.section)
	}
	if got := len(m.list.matches); got != 10 {
		t.Errorf("svc-01 matches %d entries, want 10", got)
	}
	// Looking at the whole row, the filter also finds entries by what else it shows.
	clearField(t, m)
	typeText(t, m, "BookStack")
	if got := len(m.list.matches); got != 103 {
		t.Errorf("the provider column matches %d entries, want all 103", got)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Error("filtering changed the configuration file")
	}
	if len(m.cfg.Services) != 103 || len(m.list.all) != 103 {
		t.Errorf("filtering removed entries: %d configured, %d listed", len(m.cfg.Services), len(m.list.all))
	}
}

// Opening, deleting and creating act on the entry that is selected in the filtered list, and coming back
// from a form keeps the filter and the selection.
func TestActionsFollowTheFilteredSelection(t *testing.T) {
	m, store, _ := newModel(t)
	seedServices(t, m, append(numberedServices(100), "wiki-a", "wiki-b")...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openSectionByName(t, m, sectionServices)
	press(t, m, "/")
	typeText(t, m, "wiki")
	press(t, m, "enter", "down")

	press(t, m, "enter")
	if m.screen != screenForm || m.editing != "wiki-b" {
		t.Fatalf("enter opened screen %v editing %q, want the form of wiki-b", m.screen, m.editing)
	}
	press(t, m, "esc")
	if m.screen != screenList || m.list.query() != "wiki" || selectedName(t, m) != "wiki-b" {
		t.Fatalf("returning from the form lost its place: screen %v filter %q selected %v",
			m.screen, m.list.query(), m.list.matches)
	}

	press(t, m, "d")
	if view := screenOf(m); !strings.Contains(view, `Delete "wiki-b"?`) {
		t.Fatalf("the confirmation is not about the selected entry:\n%s", view)
	}
	press(t, m, "y")
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := saved.Services["wiki-b"]; ok || len(saved.Services) != 101 {
		t.Fatalf("deleting removed the wrong entries: wiki-b kept %v, %d left", ok, len(saved.Services))
	}
	// The deleted entry is replaced by the one now in its place, and the filter is still there.
	if got := selectedName(t, m); got != "wiki-a" || m.list.query() != "wiki" {
		t.Errorf("after deleting: selected %q with filter %q, want wiki-a with wiki", got, m.list.query())
	}

	press(t, m, "n")
	typeText(t, m, "wiki-c")
	press(t, m, "tab", "tab")
	typeText(t, m, "https://wiki-c.example.invalid")
	press(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("creating reported %q", m.fail)
	}
	if got := selectedName(t, m); got != "wiki-c" || m.list.query() != "wiki" {
		t.Errorf("after creating: selected %q with filter %q, want the new wiki-c", got, m.list.query())
	}
}

// The connection test runs for the connection selected in the filtered list.
func TestConnectionTestFollowsTheFilteredSelection(t *testing.T) {
	var tested []string
	m := newTestableModel(t, func(_ context.Context, name string) (provider.Class, error) {
		tested = append(tested, name)
		return provider.ClassOK, nil
	}, nil)
	addConnection(t, m, "archive", "wiki", "reader")
	addConnection(t, m, "gamma", "wiki", "reader")
	openSectionByName(t, m, sectionConnections)

	press(t, m, "/")
	typeText(t, m, "GAM")
	press(t, m, "enter")
	runTest(t, m)
	if len(tested) != 1 || tested[0] != "gamma" {
		t.Fatalf("tested %v, want only gamma", tested)
	}
	if view := screenOf(m); !strings.Contains(view, "gamma: ok") {
		t.Errorf("the result is not reported for gamma:\n%s", view)
	}
}

// Resizing in either direction keeps the filter and the selection, and the selection stays on screen.
func TestResizeKeepsTheFilterAndTheSelection(t *testing.T) {
	m, _, _ := newModel(t)
	seedServices(t, m, numberedServices(120)...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openSectionByName(t, m, sectionServices)
	press(t, m, "/")
	typeText(t, m, "svc-1")
	press(t, m, "enter")
	for i := 0; i < 15; i++ {
		press(t, m, "down")
	}
	want := selectedName(t, m)

	for _, size := range []struct{ width, height int }{{40, 12}, {20, 5}, {200, 60}, {60, 14}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		if got := selectedName(t, m); got != want || m.list.query() != "svc-1" {
			t.Fatalf("%dx%d: selected %q with filter %q, want %q with svc-1",
				size.width, size.height, got, m.list.query(), want)
		}
		view := screenOf(m)
		assertViewFits(t, view, size.width, size.height)
		if !m.terminalTooSmall() && !strings.Contains(view, want) {
			t.Errorf("%dx%d: the selection is not on screen:\n%s", size.width, size.height, view)
		}
	}
}

// An empty list and a filter without matches say different things, and escape leaves the filter before it
// leaves the list.
func TestEmptyListNoMatchesAndEscape(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	if view := screenOf(m); !strings.Contains(view, "No services yet") || strings.Contains(view, "matches") {
		t.Errorf("the empty list does not say that it is empty:\n%s", view)
	}

	seedServices(t, m, "wiki", "chat")
	openSectionByName(t, m, sectionServices)
	press(t, m, "/")
	typeText(t, m, "zzz")
	view := screenOf(m)
	for _, want := range []string{`No entry matches "zzz"`, "esc to clear", "0/0 (2 total)"} {
		if !strings.Contains(view, want) {
			t.Errorf("a filter without matches does not say %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "No services yet") {
		t.Errorf("a filter without matches claims the list is empty:\n%s", view)
	}
	// Nothing is selected, so the entry keys do nothing.
	press(t, m, "enter", "enter", "d")
	if m.screen != screenList {
		t.Fatalf("an action ran without a selection: screen %v", m.screen)
	}

	// Escape while typing clears the filter and keeps the list.
	press(t, m, "/")
	press(t, m, "esc")
	if m.screen != screenList || m.list.query() != "" || len(m.list.matches) != 2 || m.list.editing {
		t.Fatalf("escape while typing: screen %v filter %q shown %v", m.screen, m.list.query(), m.list.matches)
	}

	// Escape after a kept filter clears it first, and only the next one leaves the list.
	press(t, m, "down", "/")
	typeText(t, m, "i")
	press(t, m, "enter")
	if got := selectedName(t, m); got != "wiki" {
		t.Fatalf("selected %q, want wiki", got)
	}
	if view := screenOf(m); !strings.Contains(view, "esc clear filter") {
		t.Errorf("the keys do not say that escape clears the filter:\n%s", view)
	}
	press(t, m, "esc")
	if m.screen != screenList || m.list.query() != "" || selectedName(t, m) != "wiki" {
		t.Fatalf("the first escape: screen %v filter %q selected %q", m.screen, m.list.query(),
			selectedName(t, m))
	}
	press(t, m, "esc")
	if m.screen != screenNav || m.section != sectionServices {
		t.Fatalf("the second escape: screen %v section %v, want the sidebar on Services", m.screen, m.section)
	}

	// Opening a section again starts without the filter.
	press(t, m, "1", "/")
	typeText(t, m, "chat")
	press(t, m, "enter", "esc", "esc", "1")
	if m.list.query() != "" || len(m.list.matches) != 2 {
		t.Errorf("a reopened section kept an old filter %q", m.list.query())
	}
}

// The window always holds the selection and never more lines than it has room for, however the rows wrap.
func TestTheWindowHoldsTheSelection(t *testing.T) {
	l := newFilterList(func(name string) string { return name })
	l.reset(numberedServices(50))
	height := func(i int) int { return 1 + i%3 }
	for _, room := range []int{3, 4, 7, 20} {
		for _, by := range []int{1, 1, 1, 7, -3, 25, -40, 1, -1, -1} {
			l.move(by)
			start, end := l.window(room, height)
			if l.cursor < start || l.cursor >= end {
				t.Fatalf("room %d: cursor %d outside [%d, %d)", room, l.cursor, start, end)
			}
			used := 0
			for i := start; i < end; i++ {
				used += height(i)
			}
			if used > room && end-start > 1 {
				t.Fatalf("room %d: window [%d, %d) takes %d lines", room, start, end, used)
			}
			l.offset = start
		}
	}
}
