package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A list screen draws its table in a panel under a title line with the section, its entry count and the position, the column names in
// capitals above the rows, each group headed by its provider mark, and the entries below it.
func TestTablesStandInAPanelWithTitleCountAndGroupHeads(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)
	lines := strings.Split(screenOf(m), "\n")
	if !strings.HasPrefix(lines[0], "Connections (3)") || !strings.HasSuffix(strings.TrimSpace(lines[0]), "1/3") {
		t.Errorf("the table has no title line with its count and position:\n%s", screenOf(m))
	}
	if top := lines[m.titleRows()]; !strings.HasPrefix(top, "╭") && !strings.HasPrefix(top, "╔") {
		t.Errorf("the table frame does not follow the title line:\n%s", screenOf(m))
	}
	view := screenOf(m)
	for _, want := range []string{"NAME", "STATE", "▾ ■ bookstack (1)", "▾ ■ tracker (2)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the framed table lacks %q:\n%s", want, view)
		}
	}
	if !strings.ContainsAny(view, "╰╚") {
		t.Errorf("the framed table lacks its bottom edge:\n%s", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: smallestListHeight})
	if view := screenOf(m); strings.ContainsAny(view, "│╭╰║╔╚") || !strings.Contains(view, "Connections  1/3") {
		t.Errorf("a terminal without room for the frame keeps the compact table:\n%s", view)
	}
}

// The marked row has its bar and accent mark under colour and its '>' without; the state is told by its word
// and, under colour, by its colour.
func TestMarkedRowAndStateAreClearWithAndWithoutColour(t *testing.T) {
	restoreRenderer(t)
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)

	lookOf(profile16)
	coloured := screenOf(m)
	if row := findRow(coloured, "wiki-docs"); !strings.Contains(row, "▌") || !hasBackground(row) {
		t.Errorf("the marked row has no accent mark and bar: %q", row)
	}
	if row := findRow(coloured, "tracker-issues"); !strings.Contains(row, "\x1b[") || strings.Contains(row, "▌") || hasBackground(row) {
		t.Errorf("an unmarked row must carry cell colours, no mark and no bar: %q", row)
	}
	if shown(connectionStateTone("active").Render("x")) == shown(connectionStateTone("expired").Render("x")) ||
		shown(connectionStateTone("disabled").Render("x")) == shown(connectionStateTone("active").Render("x")) {
		t.Error("active, expired and disabled share one style")
	}

	lookOf(profileNone)
	plain := screenOf(m)
	if strings.Contains(plain, "\x1b") || strings.Contains(plain, "▌") {
		t.Errorf("without colour the table still emits styles or the accent mark:\n%s", plain)
	}
	if row := findRow(plain, "wiki-docs"); !strings.HasPrefix(row, "> ") || !strings.Contains(row, "active") {
		t.Errorf("the marked row reads %q without colour, want '>' and the state word", row)
	}
}

// Every table screen stays inside the smallest terminal, with and without colour, and keeps its marked row
// readable by its '>'.
func TestTableScreensFitTheSmallestTerminalWithAndWithoutColour(t *testing.T) {
	before := currentProfile()
	t.Cleanup(func() { setProfile(before) })
	for _, size := range [][2]int{{40, smallestListHeight}, {80, 24}} {
		for _, s := range []section{sectionConnections, sectionServices, sectionCredentials, sectionDefaults,
			sectionApprovals, sectionTokens} {
			for _, profile := range []colorProfile{profile16, profileNone} {
				m := newTableModel(t, size[0])
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				setProfile(profile)
				openSectionByName(t, m, s)
				view := screenOf(m)
				assertViewFits(t, view, size[0], size[1])
				if profile == profileNone && strings.Contains(view, "\x1b") {
					t.Errorf("%dx%d %s: NO_COLOR still emits styles:\n%s", size[0], size[1], s.title(), view)
				}
				if profile == profileNone && len(m.list.matches) > 0 && s != sectionTokens && s != sectionApprovals &&
					!strings.Contains(view, "> ") {
					t.Errorf("%dx%d %s: no '>' marks the selection:\n%s", size[0], size[1], s.title(), view)
				}
			}
		}
	}
}

// The picker and the provider table keep their rows inside the smallest terminal and mark the row by '>'
// when there is no colour.
func TestPickerAndProviderTableFitTheSmallestTerminal(t *testing.T) {
	before := currentProfile()
	t.Cleanup(func() { setProfile(before) })
	setProfile(profileNone)

	m, _, _ := newModel(t)
	seedConnections(t, m, numberedConnections(30)...)
	openDefaultForm(t, m)
	press(t, m, "/")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: smallestListHeight})
	view := screenOf(m)
	assertViewFits(t, view, 40, smallestListHeight+1)
	if m.screen != screenPicker || !strings.Contains(view, "> conn-000") {
		t.Errorf("the picker at 40x%d:\n%s", smallestListHeight, view)
	}

	pm, _ := providerModel(t, providerRegistry(t, 55))
	openProviderRow(t, pm, sectionServices)
	press(t, pm, "enter")
	pm.Update(tea.WindowSizeMsg{Width: 40, Height: smallestListHeight})
	view = screenOf(pm)
	assertViewFits(t, view, 40, smallestListHeight+1)
	if pm.screen != screenProviders || !strings.Contains(view, "> (*) Provider 000") {
		t.Errorf("the provider table at 40x%d:\n%s", smallestListHeight, view)
	}
}

// smallestListHeight is the least height at which a list screen is usable; below it the editor asks for a
// larger terminal.
const smallestListHeight = minimumHeight
