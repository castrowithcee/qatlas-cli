package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// detailPaneMinWidth is the workspace width from which the grouped lists show the marked entry in a pane
// at their right, the same threshold as the help pane of forms. Narrower, the pane is left out and the table
// gives up its unimportant columns as before.
const detailPaneMinWidth = helpPaneMinWidth

// paneGap separates the rows from the pane.
const paneGap = " | "

// paneWidth is the width of the detail pane, 0 where there is none: below detailPaneMinWidth, in a section
// without one, and while no entry is shown for it to describe.
func (m *Model) paneWidth() int {
	if m.width < detailPaneMinWidth || !groupedSection(m.section) || len(m.list.matches) == 0 {
		return 0
	}
	return min(max(m.width*2/5, 40), 60)
}

// listWidth is the room of the rows: the workspace without the pane.
func (m *Model) listWidth() int {
	if pane := m.paneWidth(); pane > 0 {
		return m.usable(0) - pane - lipgloss.Width(paneGap)
	}
	return m.usable(0)
}

// listFit is fit within listWidth.
func (m *Model) listFit(prefix string) (string, int) { return m.fitIn(m.listWidth(), prefix) }

// withPane sets the detail pane of the marked entry beside the lines of the list that stand above its
// footer, which take up to room lines. The pane starts at the first line under the title and, where it is
// longer than the rows, adds lines of its own; what still does not fit ends in "...".
func (m *Model) withPane(lines []string, room int) []string {
	pane := m.paneWidth()
	if pane == 0 {
		return lines
	}
	var detail []string
	if name, ok := m.list.selected(); ok {
		detail = m.detailLines(name, pane)
	}
	for len(lines) < min(len(detail)+1, room) {
		lines = append(lines, "")
	}
	if len(detail) > len(lines)-1 && len(lines) > 1 {
		detail = append(detail[:len(lines)-2], styles.hint.Render("..."))
	}
	width := m.listWidth()
	out := make([]string, len(lines))
	for i, line := range lines {
		if i == 0 || i-1 >= len(detail) {
			out[i] = line
			continue
		}
		out[i] = padLine(clipCells(line, width), width) + styles.quietBorder.Render(paneGap) + detail[i-1]
	}
	return out
}

// detailLines describe the marked entry or group row of the active grouped section in width cells. They
// are built from what the list already holds: the configuration and the states its cells show, never a
// secret value. Texts from the configuration are made safe to print first.
func (m *Model) detailLines(name string, width int) []string {
	var lines []string
	column := detailLabelColumn()
	add := func(label, value string) {
		if value == "" {
			return
		}
		// Labels share one column, so a value that goes on in a second line stands under its first.
		prefix := padLine(wordText(label)+":", column)
		if label == "" {
			prefix = padLine("", column)
		}
		for i, part := range wrapCellsLines(m.logText(value), max(width-lipgloss.Width(prefix), 1)) {
			if i == 0 {
				lines = append(lines, styles.hint.Render(prefix)+part)
				continue
			}
			lines = append(lines, strings.Repeat(" ", lipgloss.Width(prefix))+part)
		}
	}
	if isGroupRow(name) {
		key := strings.TrimPrefix(name, groupRowPrefix)
		title := key
		if key == "" {
			title = wordText(noProviderGroup)
		}
		add("Group", title)
		add("Entries", fmt.Sprint(len(m.groups.members[key])))
		return lines
	}
	// The name is not repeated: the marked row already carries it.
	providerKey := m.providerOf(m.section)(name)
	add("Provider", providerKey)
	switch m.section {
	case sectionServices:
		add("Base URL", m.cfg.Services[name].BaseURL)
		var users []string
		for conn, c := range m.cfg.Connections {
			if c.Service == name {
				users = append(users, conn)
			}
		}
		add("Connections", fmt.Sprint(len(users)))
	case sectionCredentials:
		cells := m.cells(name)
		add("Storage", valueText(storageLabel, cells[1]))
		add("Secrets", cells[2])
		used := 0
		for _, c := range m.cfg.Connections {
			if c.Credential == name {
				used++
			}
		}
		add("Connections", fmt.Sprint(used))
	case sectionConnections:
		conn := m.cfg.Connections[name]
		cells := m.cells(name)
		add("Service", conn.Service)
		add("Base URL", m.cfg.Services[conn.Service].BaseURL)
		add("Credential", conn.Credential)
		add("State", cells[1])
		add("Effects", cells[3])
		add("Tools", cells[4])
		values := conn.TargetValues()
		for i, target := range values {
			if i == 5 {
				add("Targets", tr(tDetailMore, len(values)-5))
				break
			}
			text := target
			if label := conn.TargetLabels[target]; label != "" {
				text += " (" + label + ")"
			}
			if i == 0 {
				add("Targets", text)
			} else {
				add("", text)
			}
		}
		add("Description", conn.Description)
		if name == m.testName {
			switch {
			case m.testing:
				add("Last test", tr(tDetailRunning))
			case m.testClass != "":
				add("Last test", string(m.testClass))
			}
		}
	}
	return lines
}

// detailLabels are the labels of the detail pane.
var detailLabels = []string{"Group", "Entries", "Provider", "Base URL", "Connections", "Storage", "Secrets", "Service",
	"Credential", "State", "Effects", "Tools", "Targets", "Description", "Last test"}

// detailLabelColumn is the width of the label column of the detail pane: 13 cells, or one more than the
// longest label where a language needs more.
func detailLabelColumn() int {
	column := 13
	for _, label := range detailLabels {
		column = max(column, lipgloss.Width(wordText(label))+2)
	}
	return column
}

// wrapCellsLines is wrapCells as lines; an empty text has none.
func wrapCellsLines(text string, width int) []string {
	wrapped := wrapCells(text, width)
	if wrapped == "" {
		return nil
	}
	return strings.Split(wrapped, "\n")
}
