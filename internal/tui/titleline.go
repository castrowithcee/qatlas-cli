package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// titleLine is the title line of a screen, above its panels: the title at the left and, dimmed, the note at
// the right edge of width cells where it fits beside the title.
func titleLine(title, note string, width int) string {
	title = truncateCells(title, max(width, 1))
	line := styles.title.Render(title)
	if note == "" {
		return line
	}
	if gap := width - lipgloss.Width(title) - lipgloss.Width(note); gap >= 2 {
		line += strings.Repeat(" ", gap) + styles.hint.Render(note)
	}
	return line
}

// titleBlank reports whether a blank line stands between the title line and the panels, as under the title
// of Settings and the forms. A terminal with no height to spare keeps the rows instead.
func (m *Model) titleBlank() bool { return m.height >= tableFrameMinHeight+2 }

// titleRows is the number of lines titled adds above a panel.
func (m *Model) titleRows() int {
	if m.titleBlank() {
		return 2
	}
	return 1
}

// titled is the title line of a framed screen of width cells with the gap under it, each ended by a line break.
func (m *Model) titled(title, note string, width int) string {
	out := titleLine(title, note, width) + "\n"
	if m.titleBlank() {
		out += "\n"
	}
	return out
}
