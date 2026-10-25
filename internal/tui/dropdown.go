package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A dropdown field is one line, "Label: Value ▾", on the field surface; the focused one is drawn in the accent
// colour and, without colour, carries the "> " marker of barMarker. Its choices open as a small list directly
// under the field. Forms and bars share this block.

// dropdownArrow ends every dropdown field.
const dropdownArrow = "▾"

// fieldStyle is the surface of a field: the field colour behind the text, the accent colour and bold while
// the field has the focus. Without colour it names nothing; the marker carries the focus then.
func fieldStyle(focused bool) lipgloss.Style {
	p := styles.palette
	style := lipgloss.NewStyle()
	if p.surface != "" {
		style = style.Background(p.surface).Foreground(p.text)
	}
	if focused {
		style = style.Bold(true)
		if p.accent != "" {
			style = style.Foreground(p.accent)
		}
	}
	return style
}

// dropdownText is the plain text of a field, at most width cells wide: where the value does not fit it is
// cut with "…", and label and arrow stay.
func dropdownText(label, value string, width int) string {
	head := " " + label + ": "
	tail := " " + dropdownArrow + " "
	room := width - lipgloss.Width(head) - lipgloss.Width(tail)
	if room < 1 {
		room = 1
	}
	return head + truncateCells(value, room) + tail
}

// dropdownField draws a field of at most width cells (padded by one cell on each side).
func dropdownField(label, value string, focused bool, width int) string {
	return fieldStyle(focused).Render(dropdownText(label, value, width))
}

// dropdownWidth is the width of a field with its whole value.
func dropdownWidth(label, value string) int {
	return lipgloss.Width(dropdownText(label, value, 1<<20))
}

// searchFieldMin is the narrowest search field worth drawing.
const searchFieldMin = 14

// searchFieldMax is the widest search field.
const searchFieldMax = 30

// searchField draws the search field of width cells: the placeholder, dimmed, while content is empty, else
// content (the text after "/ ", or the input being typed, already drawn).
func searchField(content string, width int) string {
	if content == "" {
		hint := styles.hint
		if styles.palette.surface != "" {
			hint = hint.Background(styles.palette.surface)
		}
		return hint.Render(padLine(clipCells(" "+tr(tSearchField), width), width))
	}
	return fieldStyle(false).Render(padLine(clipCells(" "+content, width), width))
}

// dropdown is the open list of a field.
type dropdown struct {
	// id names the field the list belongs to; the drawing of the field says where it stands.
	id     string
	values []string
	cursor int
	active int
	choose func(i int) tea.Cmd
	// x and y are the screen cell of the field's left edge and its row; placed says the frame set them.
	x, y   int
	placed bool
	// box is where the list was drawn last: its cells and the first row of its entries.
	box struct{ x0, y0, x1, y1 int }
}

// openDropdown opens the list of a field, on the active value.
func (m *Model) openDropdown(id string, values []string, active int, choose func(i int) tea.Cmd) {
	m.dd = &dropdown{id: id, values: values, cursor: active, active: active, choose: choose}
}

// noteField tells an open list where its field stands.
func (m *Model) noteField(id string, x, y int) {
	if d := m.dd; d != nil && d.id == id {
		d.x, d.y, d.placed = x, y, true
	}
}

// updateDropdown is a key while a list is open: up and down move, enter takes the value, esc closes it.
func (m *Model) updateDropdown(key tea.KeyMsg) tea.Cmd {
	d := m.dd
	switch key.String() {
	case "up":
		d.cursor = max(d.cursor-1, 0)
	case "down":
		d.cursor = min(d.cursor+1, len(d.values)-1)
	case "home":
		d.cursor = 0
	case "end":
		d.cursor = len(d.values) - 1
	case "enter":
		return m.chooseDropdown(d.cursor)
	case keyOf(actBack):
		m.dd = nil
	}
	return nil
}

// chooseDropdown closes the list and gives the field its i-th value.
func (m *Model) chooseDropdown(i int) tea.Cmd {
	d := m.dd
	m.dd = nil
	if d == nil || i < 0 || i >= len(d.values) {
		return nil
	}
	return d.choose(i)
}

// lines draws the list as a framed block.
func (d *dropdown) lines() []string {
	inner := 0
	for _, v := range d.values {
		inner = max(inner, lipgloss.Width(v))
	}
	inner += 2
	rows := make([]string, len(d.values))
	for i, v := range d.values {
		line := padLine(barMarker(i == d.cursor)+v, inner)
		if i == d.cursor {
			line = styles.activeRow.Render(line)
		}
		rows[i] = line
	}
	return panel("", rows, inner, len(rows), panelOpts{focused: true, fill: true})
}

// overlayDropdown puts the open list over view, under its field, and registers its click targets. A list whose
// field the frame did not draw closes.
func (m *Model) overlayDropdown(view string) string {
	d := m.dd
	if d == nil {
		return view
	}
	if !d.placed || (m.screen != screenList && m.screen != screenLogs) || m.tooSmall {
		m.dd = nil
		return view
	}
	box := d.lines()
	w := ansi.StringWidth(box[0])
	x := max(min(d.x, m.termWidth-w), 0)
	y := min(d.y+1, max(m.termHeight-1-len(box), 0))
	rows := strings.Split(view, "\n")
	for i, line := range box {
		r := y + i
		if r >= len(rows) {
			break
		}
		rows[r] = ansi.Cut(rows[r], 0, x) + line + ansi.Cut(rows[r], x+w, m.termWidth)
	}
	d.box.x0, d.box.y0, d.box.x1, d.box.y1 = x, y, x+w, y+len(box)
	return strings.Join(rows, "\n")
}

// clickDropdown handles a click while a list is open: on an entry it takes that value, anywhere else it only
// closes the list.
func (m *Model) clickDropdown(x, y int) tea.Cmd {
	d := m.dd
	b := d.box
	if x > b.x0 && x < b.x1-1 && y > b.y0 && y < b.y1-1 {
		return m.chooseDropdown(y - b.y0 - 1)
	}
	m.dd = nil
	return nil
}

// fieldRows sets styled fields, each led by its own two-cell marker column and two cells apart, into lines of width
// cells, and the search field at the right end of the first line it fits (the last line otherwise). It returns
// the lines and the plain search field.
func fieldRows(fields []string, search func(room int) string, width int) ([]string, string) {
	var lines []string
	cur, used := "", 0
	flush := func() {
		lines = append(lines, cur)
		cur, used = "", 0
	}
	for _, f := range fields {
		w := lipgloss.Width(f)
		if used > 0 && used+2+w > width {
			flush()
		}
		if used > 0 {
			cur += "  "
			used += 2
		}
		cur += f
		used += w
	}
	if used > 0 || len(lines) == 0 {
		flush()
	}
	at := len(lines) - 1
	for i, line := range lines {
		if width-lipgloss.Width(line)-2 >= searchFieldMin {
			at = i
			break
		}
	}
	room := min(searchFieldMax, width-lipgloss.Width(lines[at])-2)
	if room < searchFieldMin {
		lines = append(lines, "")
		at = len(lines) - 1
		room = min(searchFieldMax, width-2)
	}
	if room < 4 {
		return lines, ""
	}
	field := searchField(search(room), room)
	lines[at] += strings.Repeat(" ", width-lipgloss.Width(lines[at])-room) + field
	return lines, ansi.Strip(field)
}
