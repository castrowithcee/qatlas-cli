package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dialogPanel lays the text of a dialog screen (a list of entries, a preview, a result) into one panel when
// the workspace has the room for a frame, and as plain lines under a title when it has not. Every method
// returns whole lines, each ended by a newline.
type dialogPanel struct {
	m      *Model
	framed bool
	room   int // cells of text inside the frame, or the whole width without one
}

// dialogPanelOf is the panel of the workspace as it is now.
func (m *Model) dialogPanelOf() dialogPanel {
	if m.tableFramed() {
		return dialogPanel{m: m, framed: true, room: m.usable(0) - 4}
	}
	return dialogPanel{m: m, room: m.usable(0)}
}

// chrome is the number of lines the title and the frame add around the text.
func (p dialogPanel) chrome() int {
	if p.framed {
		return p.m.titleRows() + 2
	}
	return 0
}

// top is the title line with a dimmed note at its right and, in a frame, the gap and the top border under
// it; without a frame the bold title alone.
func (p dialogPanel) top(title, note string) string {
	if p.framed {
		return p.m.titled(title, note, p.room+4) + panelTop("", "", p.room, true) + "\n"
	}
	if note != "" {
		title += "  " + note
	}
	return p.m.wrapped(styles.title, title) + "\n"
}

// bottom is the closing border, and nothing without a frame.
func (p dialogPanel) bottom() string {
	if !p.framed {
		return ""
	}
	return panelBottom(p.room, true) + "\n"
}

// side puts one finished line into the frame.
func (p dialogPanel) side(line string) string {
	if !p.framed {
		return line
	}
	return panelSide(line, p.room, true, "")
}

// blank is an empty line.
func (p dialogPanel) blank() string { return p.side("") + "\n" }

// styled puts finished lines, which may span several rows, into the frame.
func (p dialogPanel) styled(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(p.side(line) + "\n")
	}
	return b.String()
}

// text wraps text in style to the room of the frame.
func (p dialogPanel) text(style lipgloss.Style, text string) string {
	return p.styled(render(style, p.room, text))
}

// textLines is text as the separate lines it wraps into, each in the frame.
func (p dialogPanel) textLines(style lipgloss.Style, text string) []string {
	var out []string
	for _, line := range strings.Split(render(style, p.room, text), "\n") {
		out = append(out, p.side(line))
	}
	return out
}

// hint is a dimmed text indented under what it explains.
func (p dialogPanel) hint(text string) string {
	indent, width := p.m.fitIn(p.room, p.m.hintIndent())
	var b strings.Builder
	for _, line := range strings.Split(render(styles.hint, width, text), "\n") {
		b.WriteString(p.side(indent+line) + "\n")
	}
	return b.String()
}

// row is one list row, marked when active, continued under itself where it does not fit.
func (p dialogPanel) row(active bool, text string) string {
	lines := strings.Split(p.m.rowIn(p.room, active, text), "\n")
	for i := range lines {
		lines[i] = p.side(lines[i])
	}
	return strings.Join(lines, "\n")
}

// tableRow is one row of a table, already cut to the room beside the mark.
func (p dialogPanel) tableRow(active bool, text string) string {
	return p.side(p.m.listRowIn(p.room, active, text))
}

// cells is the room of the text of a table row beside its mark.
func (p dialogPanel) cells() int {
	_, width := p.m.fitIn(p.room, "  ")
	return width
}

// closing ends the body: the bottom border, or a blank line where there is no frame to set it off.
func (p dialogPanel) closing() string {
	if p.framed {
		return p.bottom()
	}
	return "\n"
}

// result is the state line of a finished step: the mark in its colour and its word.
func (p dialogPanel) result(kind statusKind, word string) string {
	return p.side(statusMark(kind, word)) + "\n"
}

// framedChrome is the number of lines framedText adds around a question: the borders and the blank line under
// the top one, which sets the screen apart from the modal of the same question.
func (m *Model) framedChrome() int {
	if m.tableFramed() {
		return 3
	}
	return 0
}

// framedText draws a question that fills the workspace, built by build for the room inside the frame, in a
// panel where the workspace has the room for one, and as build draws it elsewhere. The heading then carries
// its mark like the heading of a modal.
func (m *Model) framedText(build func() string) string { return m.framedTitled("", build) }

// framedTitled is framedText with the title in the top border of the frame, for a screen whose text does not
// carry its own heading.
func (m *Model) framedTitled(title string, build func() string) string {
	if !m.tableFramed() {
		return build()
	}
	width := m.width
	m.width, m.inPanel = width-4, true
	text := build()
	m.width, m.inPanel = width, false
	lines := append([]string{""}, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
	return strings.Join(panel(truncateCells(title, width-6), lines, width-4, len(lines), panelOpts{focused: true}), "\n") + "\n"
}

// framedQuestion is framedText for a screen that is a question only while questionScreen says so; the detail
// screens that share its screen draw their own frame.
func (m *Model) framedQuestion(build func() string) string {
	if !m.questionScreen() {
		return build()
	}
	return m.framedText(build)
}
