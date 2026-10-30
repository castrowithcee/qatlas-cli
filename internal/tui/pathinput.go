package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// Every path the TUI asks for is typed into a pathField: the connection's paths, uploads and downloads, the
// file an export writes and the file an import reads. It is a text input with a list of what completes the
// typed text. tab takes the suggestion marked, up/down move the mark, and enter takes the typed text; only
// after up/down has marked a suggestion does enter take that one instead. Nothing else changes a key.

// pathMode says what a pathField suggests.
type pathMode int

const (
	// pathPlain suggests nothing: the field is a plain text input.
	pathPlain pathMode = iota
	// pathDirectory suggests directories only.
	pathDirectory
	// pathFile suggests directories and files. The file itself may not exist yet, so only the directory part
	// of what is typed has to name something.
	pathFile
)

const (
	// pathSuggestionsShown is how many suggestions stand under the typed path at once.
	pathSuggestionsShown = 5
	// pathMatchLimit bounds the suggestions kept for one typed text.
	pathMatchLimit = 100
)

// pathEntry is one name of a listed directory.
type pathEntry struct {
	name string
	dir  bool
}

// pathField is the input of a path with its suggestions. The zero value of mode is a plain input.
type pathField struct {
	textinput.Model
	mode pathMode
	// start is the directory suggested while nothing is typed.
	start string
	// listed holds each directory read since the field was opened, so a keystroke never reads one again; a
	// directory that appears later is seen when the field is opened the next time.
	listed  map[string][]pathEntry
	matches []string
	// mark is the suggestion tab takes; marked tells that up/down put it there.
	mark   int
	marked bool
}

// newPathField is an unfocused path input of the given mode.
func newPathField(mode pathMode) pathField {
	in := newTextInput()
	in.Prompt = ""
	return pathField{Model: in, mode: mode}
}

// open starts typing value, the cursor at its end, with start as the suggestion for an empty input.
func (p *pathField) open(value, start string) {
	p.listed, p.start = nil, start
	p.set(value)
	p.Focus()
}

// set replaces the typed text, the cursor at its end.
func (p *pathField) set(value string) {
	p.SetValue(value)
	p.CursorEnd()
	p.refresh()
}

// update handles one key. tab, up and down act on the suggestions; every other key goes to the text input,
// and the suggestions follow when the text changed.
func (p *pathField) update(key tea.KeyPressMsg) tea.Cmd {
	switch {
	case keyIs(key, actNext):
		// Completion takes the next-field key: a path line is one field, so tab never moves on from it.
		if p.mode != pathPlain {
			p.complete()
			return nil
		}
	case keyIs(key, actUp) || keyIs(key, actDown) || keyIs(key, actSuggestPrev) || keyIs(key, actSuggestNext):
		if p.mode != pathPlain {
			step := 1
			if keyIs(key, actUp) || keyIs(key, actSuggestPrev) {
				step = -1
			}
			p.move(step)
			return nil
		}
	}
	before := p.Value()
	var cmd tea.Cmd
	p.Model, cmd = p.Model.Update(inputMsg(key))
	if p.Value() != before {
		p.refresh()
	}
	return cmd
}

// accept is what enter does before the typed text is taken: it puts the marked suggestion into the text if up/down
// marked one. It reports whether the text is ready to be taken, which a directory chosen for a file still
// needs to be entered is not.
func (p *pathField) accept() bool {
	if !p.marked || p.mark >= len(p.matches) {
		return true
	}
	chosen := p.matches[p.mark]
	p.set(chosen)
	return p.mode != pathFile || !endsWithSeparator(chosen)
}

func (p *pathField) complete() {
	if p.mark < len(p.matches) && p.matches[p.mark] != p.Value() {
		p.set(p.matches[p.mark])
	}
}

func (p *pathField) move(step int) {
	if len(p.matches) == 0 {
		return
	}
	p.mark = (p.mark + step + len(p.matches)) % len(p.matches)
	p.marked = true
}

func (p *pathField) refresh() {
	p.matches, p.mark, p.marked = p.suggest(p.Value()), 0, false
}

// shown are the suggestions that stand under the typed path and the position of the marked one among them.
func (p *pathField) shown() ([]string, int) {
	start, end := p.window()
	return p.matches[start:end], p.mark - start
}

// window is the range of the suggestions that stand under the typed path.
func (p *pathField) window() (int, int) {
	start := max(0, min(p.mark-pathSuggestionsShown+1, len(p.matches)-pathSuggestionsShown))
	return start, min(len(p.matches), start+pathSuggestionsShown)
}

// click is a click on suggestion i. Only up/down mark a suggestion for enter, so the first click marks the way
// they do, and a click on the marked one takes it as tab does.
func (p *pathField) click(i int) {
	if i < 0 || i >= len(p.matches) {
		return
	}
	taken := false
	markOrAct(&p.mark, i, p.marked, func() tea.Cmd {
		taken = true
		p.complete()
		return nil
	})
	if !taken {
		p.marked = true
	}
}

// suggest are the entries that complete typed: those inside the directory it names up to its last separator
// whose names start with the rest, each written as that directory followed by the name, and a separator after
// a directory so the next tab goes on into it. Names are matched with their case, so a completion never
// changes what is typed; a hidden entry is offered only once its dot is typed. Directories come first. The
// directory part is taken in a form a path may have, absolute or starting with ~/; a file may also be
// relative, which is resolved against the working directory when it is used.
func (p *pathField) suggest(typed string) []string {
	if p.mode == pathPlain {
		return nil
	}
	if typed == "" {
		if p.start == "" {
			return nil
		}
		return []string{withSeparator(p.start)}
	}
	if typed == "~" {
		return []string{"~" + string(filepath.Separator)}
	}
	cut := strings.LastIndexFunc(typed, isSeparator) + 1
	dir, rest := typed[:cut], typed[cut:]
	lookup := dir
	switch {
	case dir == "":
		if p.mode != pathFile {
			return nil
		}
		lookup = "."
	case !projectpath.IsHomeRelative(dir) && !filepath.IsAbs(dir) && p.mode != pathFile:
		return nil
	}
	expanded, err := projectpath.Expand(lookup)
	if err != nil {
		return nil
	}
	var dirs, files []string
	for _, entry := range p.list(expanded) {
		name := entry.name
		if !strings.HasPrefix(name, rest) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(rest, ".")) {
			continue
		}
		switch {
		case entry.dir:
			dirs = append(dirs, dir+name+string(filepath.Separator))
		case p.mode == pathFile:
			files = append(files, dir+name)
		}
	}
	return append(dirs, files...)[:min(len(dirs)+len(files), pathMatchLimit)]
}

// list reads the entries of a directory once per opening of the field. One that cannot be read has none.
func (p *pathField) list(dir string) []pathEntry {
	if entries, ok := p.listed[dir]; ok {
		return entries
	}
	var entries []pathEntry
	if read, err := os.ReadDir(dir); err == nil {
		for _, e := range read {
			isDir := e.IsDir()
			if e.Type()&fs.ModeSymlink != 0 {
				// A link to a directory is offered like the directory it points to.
				info, err := os.Stat(filepath.Join(dir, e.Name()))
				isDir = err == nil && info.IsDir()
			}
			entries = append(entries, pathEntry{e.Name(), isDir})
		}
	}
	if p.listed == nil {
		p.listed = map[string][]pathEntry{}
	}
	p.listed[dir] = entries
	return entries
}

func isSeparator(r rune) bool {
	return r < 0x80 && os.IsPathSeparator(uint8(r))
}

func endsWithSeparator(s string) bool {
	return s != "" && os.IsPathSeparator(s[len(s)-1])
}

// withSeparator is dir with one separator at its end.
func withSeparator(dir string) string {
	if endsWithSeparator(dir) {
		return dir
	}
	return dir + string(filepath.Separator)
}

// pathLines draws the input behind label and its suggestions, one line each, the marked one with the focus
// marker.
func (m *Model) pathLines(label string, p *pathField) string {
	return m.pathLinesIn(m.usable(0), label, p)
}

// pathLinesIn is pathLines within the first room cells of the workspace.
func (m *Model) pathLinesIn(room int, label string, p *pathField) string {
	prefix, width := m.fitIn(room, label)
	in := fitInput(p.Model, width)
	// The text input pads to its width, so the line is cut back to the room there is.
	var b strings.Builder
	b.WriteString(prefix + lipgloss.NewStyle().MaxWidth(width).Render(inputView(in)) + "\n")
	suggestions, current := p.shown()
	shown := pathRowsShown{field: p}
	shown.first, _ = p.window()
	for i, suggestion := range suggestions {
		row := m.rowIn(room, i == current, suggestion)
		shown.rows = append(shown.rows, strings.TrimRight(ansi.Strip(row), " "))
		b.WriteString(row + "\n")
	}
	if len(suggestions) > 0 {
		// A field is drawn more than once while a frame is laid out; the last drawing counts.
		m.pathShown = slices.DeleteFunc(m.pathShown, func(s pathRowsShown) bool { return s.field == p })
		m.pathShown = append(m.pathShown, shown)
	}
	return b.String()
}

// pathRowsShown are the suggestion rows a pathField drew, as plain text, from suggestion first on.
type pathRowsShown struct {
	field *pathField
	first int
	rows  []string
}

// hitPathRows makes the suggestions drawn in lines clickable. lines stand from screen row y0 and column x0 on.
// A suggestion is found by its text, so one that a narrow room wrapped over several lines is no target.
func (m *Model) hitPathRows(lines []string, y0, x0 int) {
	for _, shown := range m.pathShown {
		at := 0
		for k, row := range shown.rows {
			for ; at < len(lines); at++ {
				plain := strings.TrimRight(ansi.Strip(lines[at]), " ")
				idx := strings.Index(plain, row)
				if idx < 0 {
					continue
				}
				field, i := shown.field, shown.first+k
				x := x0 + ansi.StringWidth(plain[:idx])
				m.addHit(x, y0+at, x+ansi.StringWidth(row), y0+at+1, func() tea.Cmd {
					field.click(i)
					return nil
				})
				at++
				break
			}
		}
	}
	m.pathShown = nil
}

// pathKeys is the key line of a path prompt: keys, with the suggestion keys in front while there are any.
func pathKeys(p *pathField, keys string) string {
	if len(p.matches) > 0 {
		return "{next} complete · {up}/{down} switch · " + keys
	}
	return keys
}

// absolutePath is path as the working directory and the home directory make it absolute: a leading ~ is the
// home directory, which the file system would otherwise take as a directory of that name.
func absolutePath(path string) string {
	if expanded, err := projectpath.Expand(path); err == nil {
		path = expanded
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
