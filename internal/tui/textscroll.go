package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// textScroll is the scroll position of a plain text area. The viewport keeps the position and its limits; the
// keys come from the key table and the wheel arrives as those keys (see wheel in mouse.go), so the viewport's
// own key map and wheel are never used: its zero value has none, and Update is never called. The text is
// never scrolled sideways: the screens wrap or cut their lines before they get here.
type textScroll struct {
	vp viewport.Model
}

// offset is the first shown line.
func (t *textScroll) offset() int { return t.vp.YOffset() }

// reset puts the area back to the first line of whatever it shows next.
func (t *textScroll) reset() { t.vp = viewport.Model{} }

// scroll moves by one of the scroll keys of the table, a page being page lines, and reports whether key was
// one of them.
func (t *textScroll) scroll(key tea.KeyPressMsg, page int) bool {
	switch {
	case keyIs(key, actDown):
		t.vp.ScrollDown(1)
	case keyIs(key, actUp):
		t.vp.ScrollUp(1)
	case keyIs(key, actPageDown):
		t.vp.ScrollDown(max(page, 1))
	case keyIs(key, actPageUp):
		t.vp.ScrollUp(max(page, 1))
	case keyIs(key, actHome):
		t.vp.GotoTop()
	case keyIs(key, actEnd):
		t.vp.GotoBottom()
	default:
		return false
	}
	return true
}

// halfPage is the page of the screens that scroll by half their height.
func halfPage(height int) int { return max(height/2, 1) }

// window lays lines out in an area of height rows, clamps the position to them (also after a resize), and
// returns the rows in view: as many as there are lines left, not padded to height. follow keeps the last line
// in view. Every line is one row; the viewport pads each to the widest one and the padding is cut off again,
// so the rows are the lines as they were.
func (t *textScroll) window(lines []string, height int, follow bool) []string {
	vp := &t.vp
	width := 1
	for _, line := range lines {
		width = max(width, lipgloss.Width(line))
	}
	vp.SetWidth(width)
	vp.SetHeight(height)
	vp.SetContentLines(slices.Clone(lines))
	vp.SetYOffset(vp.YOffset())
	if follow {
		vp.GotoBottom()
	}
	start := vp.YOffset()
	n := min(height, len(lines)-start)
	if n <= 0 {
		return nil
	}
	rows := strings.Split(vp.View(), "\n")[:n]
	for i, row := range rows {
		rows[i] = strings.TrimSuffix(row, strings.Repeat(" ", width-lipgloss.Width(lines[start+i])))
	}
	return rows
}

// position is the line that says which lines of total are in view, in the form of the help; it is empty
// while all of them fit.
func (t *textScroll) position(total, shown int) string {
	if shown >= total {
		return ""
	}
	first := min(t.offset()+1, total)
	return tr(tApprovalLines, first, min(t.offset()+shown, total), total)
}

// paneScroll is the scroll state of a text pane that stands beside a screen (the detail pane of a list, the
// explanation pane of a form), which has no key of its own to scroll with: only the wheel over it, and for
// the focused detail pane the page keys.
type paneScroll struct {
	view textScroll
	// key names what the pane shows; another subject starts at the first line.
	key string
	// page is the lines in view of the last drawing, more whether there were lines left out.
	page int
	more bool
	// x0, y0, x1, y1 is where the pane was drawn.
	x0, y0, x1, y1 int
}

// fit lays body out in room rows and returns the rows to draw. A body that does not fit gets room-1 rows and
// the position line below them; a pane of a single row keeps cutting, which is all it can show.
func (p *paneScroll) fit(key string, body []string, room int) (rows []string, position string) {
	if key != p.key {
		p.key = key
		p.view.reset()
	}
	p.more = false
	if len(body) <= room || room < 2 {
		p.view.reset()
		return body, ""
	}
	p.page, p.more = room-1, true
	rows = p.view.window(body, p.page, false)
	return rows, p.view.position(len(body), p.page)
}

// at reports whether the cell is in the pane that was drawn last.
func (p *paneScroll) at(x, y int) bool {
	return p.more && x >= p.x0 && x < p.x1 && y >= p.y0 && y < p.y1
}

// wheel moves the pane by the notches of the wheel.
func (p *paneScroll) wheel(up bool) {
	code := tea.KeyDown
	if up {
		code = tea.KeyUp
	}
	for range wheelText {
		p.view.scroll(tea.KeyPressMsg{Code: code}, p.page)
	}
}

// pageKey reports whether key is one of the keys that jump within a text: the page keys, home and end.
func pageKey(key tea.KeyPressMsg) bool {
	return keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd)
}
