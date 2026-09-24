package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// filterList is a selection over a list of names that a typed filter can narrow and that scrolls inside
// whatever height its screen leaves it. It never removes an entry and never changes their order: the filter
// only decides which entries are shown, and clearing it brings every one of them back where it was.
//
// The selection follows a name, not a position. When the entries or the filter change, the same name stays
// selected while it is still shown; otherwise the selection is clamped into what is left.
type filterList struct {
	all     []string
	matches []string
	// text is what the filter looks at for one name: the line the reader sees, so what can be read can be
	// found. It is never a secret, because no line of the editor ever holds one.
	text    func(name string) string
	cursor  int
	offset  int
	input   textinput.Model
	editing bool
}

func newFilterList(text func(string) string) filterList {
	in := textinput.New()
	in.Prompt = ""
	// Static for the same reason as every text field of the editor: nothing threads a blink timer through.
	in.Cursor.SetMode(cursor.CursorStatic)
	return filterList{text: text, input: in}
}

// reset replaces the entries and forgets the filter, the selection and the scroll position.
func (l *filterList) reset(names []string) {
	l.stopFilter()
	l.input.SetValue("")
	l.all, l.matches, l.cursor, l.offset = names, nil, 0, 0
	l.refilter()
}

// setItems replaces the entries and keeps the filter and, where it can, the selected name.
func (l *filterList) setItems(names []string) {
	l.all = names
	l.refilter()
}

func (l *filterList) query() string { return strings.TrimSpace(l.input.Value()) }

// refilter recomputes the shown entries. Matching ignores case, so a filter finds what it reads like.
func (l *filterList) refilter() {
	previous, hadSelection := l.selected()
	query := strings.ToLower(l.query())
	l.matches = l.matches[:0]
	for _, name := range l.all {
		if query == "" || strings.Contains(strings.ToLower(l.text(name)), query) {
			l.matches = append(l.matches, name)
		}
	}
	if hadSelection && l.selectName(previous) {
		return
	}
	l.cursor = min(l.cursor, max(len(l.matches)-1, 0))
}

func (l *filterList) selected() (string, bool) {
	if l.cursor < 0 || l.cursor >= len(l.matches) {
		return "", false
	}
	return l.matches[l.cursor], true
}

// selectName selects the named entry if it is shown and reports whether it is.
func (l *filterList) selectName(name string) bool {
	for i, match := range l.matches {
		if match == name {
			l.cursor = i
			return true
		}
	}
	return false
}

// move steps through the shown entries and wraps at both ends, like every other list of the editor.
func (l *filterList) move(by int) { l.cursor = wrap(l.cursor+by, len(l.matches)) }

// jump moves by a page or to an end. It stops at the ends instead of wrapping: a page key that lands at
// the other end of a long list would lose the reader's place.
func (l *filterList) jump(by int) {
	l.cursor = min(max(l.cursor+by, 0), max(len(l.matches)-1, 0))
}

// page handles pgup/pgdown, which move by one screen of the entries the window [start, end) shows, and
// home/end, which move to either end.
func (l *filterList) page(key string, start, end int) {
	page := max(end-start-1, 1)
	switch key {
	case "pgup":
		l.jump(-page)
	case "pgdown":
		l.jump(page)
	case "home":
		l.jump(-len(l.matches))
	case "end":
		l.jump(len(l.matches))
	}
}

func (l *filterList) startFilter() {
	l.editing = true
	l.input.CursorEnd()
	l.input.Focus()
}

// stopFilter ends typing and keeps the filter.
func (l *filterList) stopFilter() {
	l.editing = false
	l.input.Blur()
}

// clearFilter ends typing and shows every entry again, still on the selected name.
func (l *filterList) clearFilter() {
	l.stopFilter()
	l.input.SetValue("")
	l.refilter()
}

// updateFilter hands one key to the filter being typed and applies the result at once.
func (l *filterList) updateFilter(key tea.KeyMsg) tea.Cmd {
	before := l.input.Value()
	var cmd tea.Cmd
	l.input, cmd = l.input.Update(key)
	if l.input.Value() != before {
		l.refilter()
	}
	return cmd
}

// window is the part of the shown entries that fits into room lines, as the range [start, end). lines
// reports how many lines one entry takes, since an entry may wrap. The selected entry is always inside
// the range. The scroll position is kept while it still shows the selection, so moving within the screen
// does not shift it; a selection outside it is brought in at the nearest edge; left-over room is filled
// from above as well as below.
func (l *filterList) window(room int, lines func(i int) int) (int, int) {
	n := len(l.matches)
	if n == 0 {
		return 0, 0
	}
	start := min(max(l.offset, 0), l.cursor)
	end, used := start, 0
	for end <= l.cursor && used <= room {
		used += lines(end)
		end++
	}
	if used > room || end <= l.cursor {
		start, end, used = l.cursor, l.cursor+1, lines(l.cursor)
		for start > 0 && used+lines(start-1) <= room {
			start--
			used += lines(start)
		}
	}
	for end < n && used+lines(end) <= room {
		used += lines(end)
		end++
	}
	for start > 0 && used+lines(start-1) <= room {
		start--
		used += lines(start)
	}
	return start, end
}

// column is one column of a list table: its heading; whether it may be left out of a terminal too narrow
// for it; whether it is the free text of the table; and the short labels it may show instead of its values
// when those do not fit.
type column struct {
	title    string
	optional bool
	flex     bool
	short    map[string]string
}

// flexFloor is the narrowest a free text column is cut to before anything else of the row gives way.
const flexFloor = 12

// table lays out the entries of a list as aligned columns under a line of headings. The first column names
// the entry and is never cut, and the other columns keep their values whole as long as they can: when the
// row does not fit, the free text gives way first, down to flexFloor; then a column with short labels shows
// them; then the optional columns are left out, from the right; and only then are the remaining values cut,
// the widest first, never below the width of their heading. The free text gets whatever room is left.
type table struct {
	columns []column
	// rows holds the cells of every entry by its name, the filtered-out ones included, so the columns keep
	// their width while a filter is typed.
	rows map[string][]string
}

// layout is how a table fits a width: the width of every column, 0 for a column left out, and whether a
// column shows its short labels.
type layout struct {
	widths []int
	short  []bool
}

// columnGap is the room between two columns.
const columnGap = "  "

// cell is what column i shows of cells under the layout.
func (t table) cell(cells []string, i int, l layout) string {
	if label, ok := t.columns[i].short[cells[i]]; ok && l.short[i] {
		return label
	}
	return cells[i]
}

// layout fits the table into width cells.
func (t table) layout(width int) layout {
	n := len(t.columns)
	l := layout{widths: make([]int, n), short: make([]bool, n)}
	natural := func(i int) int {
		w := lipgloss.Width(t.columns[i].title)
		for _, cells := range t.rows {
			w = max(w, lipgloss.Width(t.cell(cells, i, l)))
		}
		return w
	}
	flex, flexWidth := -1, 0
	for i, c := range t.columns {
		l.widths[i] = natural(i)
		if c.flex && i > 0 {
			flex, flexWidth = i, l.widths[i]
			l.widths[i] = min(flexWidth, max(lipgloss.Width(c.title), flexFloor))
		}
	}
	for i, c := range t.columns {
		if c.short != nil && tableWidth(l.widths) > width {
			l.short[i] = true
			l.widths[i] = natural(i)
		}
	}
	for i := n - 1; i > 0 && tableWidth(l.widths) > width; i-- {
		if t.columns[i].optional && i != flex {
			l.widths[i] = 0
		}
	}
	for tableWidth(l.widths) > width {
		widest := -1
		for i := 1; i < n; i++ {
			if i != flex && l.widths[i] > lipgloss.Width(t.columns[i].title) &&
				(widest < 0 || l.widths[i] >= l.widths[widest]) {
				widest = i
			}
		}
		if widest < 0 && flex >= 0 && l.widths[flex] > lipgloss.Width(t.columns[flex].title) {
			widest = flex
		}
		if widest < 0 {
			break
		}
		l.widths[widest]--
	}
	if flex >= 0 {
		l.widths[flex] = min(flexWidth, l.widths[flex]+max(width-tableWidth(l.widths), 0))
	}
	return l
}

// tableWidth is how wide a line of columns of the given widths is, the gaps between them included.
func tableWidth(widths []int) int {
	total, shown := 0, 0
	for _, w := range widths {
		if w > 0 {
			total += w
			shown++
		}
	}
	return total + lipgloss.Width(columnGap)*max(shown-1, 0)
}

// header is the line that names the columns, aligned with the rows below it.
func (t table) header(l layout) string {
	var parts []string
	for i, c := range t.columns {
		if l.widths[i] > 0 {
			parts = append(parts, padLine(truncateCells(c.title, l.widths[i]), l.widths[i]))
		}
	}
	return strings.TrimRight(strings.Join(parts, columnGap), " ")
}

// line draws cells into the columns, each cut to its width and marked where it is cut.
func (t table) line(cells []string, l layout) string {
	var parts []string
	for i := range cells {
		if l.widths[i] > 0 {
			parts = append(parts, padLine(truncateCells(t.cell(cells, i, l), l.widths[i]), l.widths[i]))
		}
	}
	return strings.TrimRight(strings.Join(parts, columnGap), " ")
}
