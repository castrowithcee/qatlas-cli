package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The picker screens (value picker, provider table, target picker, template picker) draw their table like a
// list: in a panel with the position in its top border and the search field as its first line. Where the
// terminal is too small for the frame (see tableFramed) the same lines stand without it, so a small terminal
// keeps its room. The helpers below return lines that end in a line feed, except boxBottom, which belongs in
// front of the footer.

// boxed reports whether a picker draws its table in a panel.
func (m *Model) boxed() bool { return m.tableFramed() }

// boxRoom is the width of a line of a picker: the terminal's, less the frame where there is one.
func (m *Model) boxRoom() int {
	if m.boxed() {
		return max(m.usable(0)-4, 1)
	}
	return m.usable(0)
}

// boxTop is the title of a picker: a title line above the panel with note dimmed at its right end, or, where
// there is no frame, a line of its own with the note behind it.
func (m *Model) boxTop(title, note string) string {
	if !m.boxed() {
		if note != "" {
			title += "  " + note
		}
		return m.wrapped(styles.title, title) + "\n"
	}
	return m.titled(title, note, m.boxRoom()+4) + panelTop("", "", m.boxRoom(), true) + "\n"
}

// boxText is a message of a picker, wrapped into the room of its lines.
func (m *Model) boxText(style lipgloss.Style, text string) string {
	if !m.boxed() {
		return m.wrapped(style, text) + "\n"
	}
	var b strings.Builder
	for _, line := range strings.Split(render(style, m.boxRoom(), text), "\n") {
		b.WriteString(panelSide(line, m.boxRoom(), true) + "\n")
	}
	return b.String()
}

// boxLine is a finished line of a picker, cut to its room.
func (m *Model) boxLine(text string) string {
	if !m.boxed() {
		return text + "\n"
	}
	return panelSide(text, m.boxRoom(), true) + "\n"
}

// boxSearch is the search line of a picker: the search field of the lists inside the panel, and the labelled
// line outside it.
func (m *Model) boxSearch(l *filterList, label string) string {
	if !m.boxed() {
		return m.searchLine(l, label) + "\n"
	}
	l.input.Placeholder = tr(tLogSearchPlaceholder)
	return m.boxLine(searchField(searchContent(l, m.boxRoom()), m.boxRoom()))
}

// boxRow is a finished row of a picker table.
func (m *Model) boxRow(row string) string {
	if !m.boxed() {
		return row
	}
	return panelSide(row, m.boxRoom(), true)
}

// boxBottom is the closing border of the panel, or nothing without one.
func (m *Model) boxBottom() string {
	if !m.boxed() {
		return ""
	}
	return panelBottom(m.boxRoom(), true)
}

// boxX is the column the content of a row starts at: behind the side border and its blank.
func (m *Model) boxX() int {
	if m.boxed() {
		return 2
	}
	return 0
}

// tickBox is the box of a ticked or unticked entry. The tick is coloured, and the characters alone tell the
// states apart.
func tickBox(ticked bool) string {
	if ticked {
		return styles.ok.Render("[x]")
	}
	return "[ ]"
}

// colourTick colours the first ticked box of a drawn row and sets the style of the cell it stands in again
// behind it.
func colourTick(row string, cell lipgloss.Style) string {
	i := strings.Index(row, "[x]")
	if i < 0 {
		return row
	}
	set := cell.Render("x")
	set = set[:strings.Index(set, "x")]
	return row[:i] + styles.ok.Render("[x]") + set + row[i+3:]
}
