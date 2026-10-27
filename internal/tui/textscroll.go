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
