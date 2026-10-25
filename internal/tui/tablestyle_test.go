package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// A list screen draws its table in a panel titled with the section and its entry count, the column names in
// capitals above the rows, each group headed by its provider mark, and the entries below it.
func TestTablesStandInAPanelWithTitleCountAndGroupHeads(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)
	lines := strings.Split(screenOf(m), "\n")
	if !strings.HasPrefix(lines[0], "╭") && !strings.HasPrefix(lines[0], "╔") || !strings.Contains(lines[0], "Connections (3)") {
		t.Errorf("the table has no panel titled with its count:\n%s", screenOf(m))
	}
	view := screenOf(m)
	for _, want := range []string{"NAME", "STATE", "▾ ■ bookstack (1)", "▾ ■ tracker (2)", "╰"} {
		if !strings.Contains(view, want) {
			t.Errorf("the framed table lacks %q:\n%s", want, view)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: smallestListHeight})
	if view := screenOf(m); strings.ContainsAny(view, "│╭╰") || !strings.Contains(view, "Connections  1/3") {
		t.Errorf("a terminal without room for the frame keeps the compact table:\n%s", view)
	}
}

// The marked row has its bar and accent mark under colour and its '>' without; the state is told by its word
// and, under colour, by its colour.
func TestMarkedRowAndStateAreClearWithAndWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionConnections)

	lipgloss.SetColorProfile(termenv.ANSI)
	coloured := screenOf(m)
	if row := findRow(coloured, "wiki-docs"); !strings.Contains(row, "▌") || !hasBackground(row) {
		t.Errorf("the marked row has no accent mark and bar: %q", row)
	}
	if row := findRow(coloured, "tracker-issues"); !strings.Contains(row, "\x1b[") || strings.Contains(row, "▌") || hasBackground(row) {
		t.Errorf("an unmarked row must carry cell colours, no mark and no bar: %q", row)
	}
	if connectionStateTone("active").Render("x") == connectionStateTone("expired").Render("x") ||
		connectionStateTone("disabled").Render("x") == connectionStateTone("active").Render("x") {
		t.Error("active, expired and disabled share one style")
	}

	lipgloss.SetColorProfile(termenv.Ascii)
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
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
	for _, size := range [][2]int{{40, smallestListHeight}, {80, 24}} {
		for _, s := range []section{sectionConnections, sectionServices, sectionCredentials, sectionDefaults,
			sectionApprovals, sectionTokens} {
			for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
				m := newTableModel(t, size[0])
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				lipgloss.SetColorProfile(profile)
				openSectionByName(t, m, s)
				view := screenOf(m)
				assertViewFits(t, view, size[0], size[1])
				if profile == termenv.Ascii && strings.Contains(view, "\x1b") {
					t.Errorf("%dx%d %s: NO_COLOR still emits styles:\n%s", size[0], size[1], s.title(), view)
				}
				if profile == termenv.Ascii && len(m.list.matches) > 0 && s != sectionTokens && s != sectionApprovals &&
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
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
	lipgloss.SetColorProfile(termenv.Ascii)

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
const smallestListHeight = minimumHeight + 1
