package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// An area is a task the person came to do. It is the top level of the tab line; the section stays the
// object model beneath it, so every list, form and save keeps working on its own section. An area with
// several sections shows them as sub-tabs in an option bar below the tab line.
type area int

const (
	areaOverview area = iota
	areaConnections
	areaActivity
	areaAccess
	areaLibrary
	areaSettings
	areaCount
)

func (a area) title() string {
	return [...]string{"Overview", "Connections", "Activity", "Access", "Library", "Settings"}[a]
}

// areaByName finds the area whose title is name, ignoring case.
func areaByName(name string) (area, bool) {
	for a := area(0); a < areaCount; a++ {
		if strings.EqualFold(a.title(), name) {
			return a, true
		}
	}
	return areaOverview, false
}

// areaSections are the sections of each area in sub-tab order; the first one is what the area opens on.
var areaSections = [areaCount][]section{
	areaOverview:    {sectionOverview},
	areaConnections: {sectionConnections},
	areaActivity:    {sectionLogs},
	areaAccess:      {sectionTokens, sectionApprovals},
	areaLibrary:     {sectionServices, sectionCredentials},
	areaSettings:    {sectionGeneral, sectionVault, sectionDefaults, sectionTransfer, sectionUpdate, sectionSelftest},
}

// area is the area that holds s.
func (s section) area() area {
	for a := area(0); a < areaCount; a++ {
		for _, member := range areaSections[a] {
			if member == s {
				return a
			}
		}
	}
	return areaOverview
}

// subTitle is the name of s as a sub-tab.
func (s section) subTitle() string { return s.title() }

// index is the position of s among the sub-tabs of its area.
func (s section) index() int {
	for i, member := range areaSections[s.area()] {
		if member == s {
			return i
		}
	}
	return 0
}

// hasSubTabs says whether the active area shows an option bar.
func (m *Model) hasSubTabs() bool { return len(areaSections[m.section.area()]) > 1 }

// previewArea makes a the active area, on its first section, while the tab line keeps the focus.
func (m *Model) previewArea(a area) tea.Cmd {
	cmd := m.previewSection(areaSections[a][0])
	m.navSub = false
	return cmd
}

// advancedStart is the first Settings page after the device-local preferences: from it on the option bar
// shows a bar before the page, the part of Settings that manages the vault, the configuration and the
// program itself.
const advancedStart = sectionVault

// subNavLine is the option bar of the active area: "Area: [active] other", with "> " in front while it has
// the focus. A terminal too narrow for every name shows the active one alone.
func (m *Model) subNavLine() string {
	if !m.hasSubTabs() || m.wizard != nil {
		return ""
	}
	focus := "  "
	if m.screen == screenNav && m.navSub {
		focus = "> "
	}
	a := m.section.area()
	var all, active []string
	for _, s := range areaSections[a] {
		if s == advancedStart {
			// The advanced pages follow the preferences after a bar, so the split reads at any width.
			all = append(all, "|")
		}
		if s == m.section {
			all, active = append(all, "["+s.subTitle()+"]"), append(active, "["+s.subTitle()+"]")
			continue
		}
		all = append(all, s.subTitle())
	}
	prefix := focus + a.title() + ": "
	line := prefix + strings.Join(all, " ")
	if lipgloss.Width(line) > m.termWidth {
		line = prefix + strings.Join(active, " ")
	}
	return styles.active.Render(clipCells(line, m.termWidth))
}

// ref names s for a message that points at it: its sub-tab inside its area, with the digit that opens the
// area.
func (s section) ref() string {
	a := s.area()
	if len(areaSections[a]) == 1 {
		return fmt.Sprintf("%s (%d)", s.title(), int(a)+1)
	}
	return fmt.Sprintf("%s > %s (%d)", a.title(), s.title(), int(a)+1)
}
