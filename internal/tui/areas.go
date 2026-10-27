package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// An area is a task the person came to do. It is the top level of the tab line; the section stays the
// object model beneath it, so every list, form and save keeps working on its own section. An area with
// several sections shows them as sub-tabs in a tab row below the tab line.
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

// key is the English name of the area: the name the start view is kept under and the commands are named
// after, whatever language the editor draws in.
func (a area) key() string {
	return [...]string{"Overview", "Connections", "Activity", "Access", "Library", "Settings"}[a]
}

// title is the name of the area as drawn.
func (a area) title() string {
	return tr([...]textID{tAreaOverview, tAreaConnections, tAreaActivity, tAreaAccess, tAreaLibrary, tAreaSettings}[a])
}

// areaByName finds the area whose English name is name, ignoring case.
func areaByName(name string) (area, bool) {
	for a := area(0); a < areaCount; a++ {
		if strings.EqualFold(a.key(), name) {
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

// hasSubTabs says whether the active area shows a tab row.
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

// subNavLine is the tab row of the active area, drawn like the tabs of the detail pane, with "> " in front
// while it has the focus. A terminal too narrow for every name shows the active one alone.
func (m *Model) subNavLine() string {
	if !m.hasSubTabs() || m.wizard != nil {
		return ""
	}
	line, _ := m.subNavBar()
	return clipCells(line, m.termWidth)
}

// subNavItem is a part on the tab row and the cells it stands in.
type subNavItem struct {
	section section
	navSpan
}

// subNavSpans are the cells of the parts on the tab row.
func (m *Model) subNavSpans() []subNavItem {
	if !m.hasSubTabs() || m.wizard != nil {
		return nil
	}
	_, items := m.subNavBar()
	return items
}

// subNavBar is the text of the tab row and the cells of its parts.
func (m *Model) subNavBar() (string, []subNavItem) {
	focus := "  "
	focused := m.screen == screenNav && m.navSub
	if focused {
		focus = "> "
	}
	var words []string
	var sections []section
	active := 0
	for _, s := range areaSections[m.section.area()] {
		if s == advancedStart {
			// The advanced pages follow the preferences after a bar, so the split reads at any width.
			words, sections = append(words, "|"), append(sections, -1)
		}
		if s == m.section {
			active = len(words)
		}
		words, sections = append(words, s.subTitle()), append(sections, s)
	}
	strip, spans := tabStrip(words, active, focused)
	if lipgloss.Width(focus)+lipgloss.Width(strip) > m.termWidth {
		words, sections = words[active:active+1], sections[active:active+1]
		strip, spans = tabStrip(words, 0, focused)
	}
	var out []subNavItem
	for i, span := range spans {
		if sections[i] >= 0 {
			out = append(out, subNavItem{sections[i], navSpan{min(span[0]+len(focus), m.termWidth), min(span[1]+len(focus), m.termWidth)}})
		}
	}
	return focus + strip, out
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
