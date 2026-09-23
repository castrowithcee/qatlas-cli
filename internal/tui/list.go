package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
