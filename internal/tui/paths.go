package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// The path list of a connection is edited in a screen of its own, opened from its row like the target list:
// a adds a path, enter or e edits the selected one, and x or d removes it after asking. F2 keeps the list and
// saves the form in one step, from every state of the screen, the typed path included. esc closes an
// unchanged list at once and asks about a changed one, so no change is dropped silently.
//
// The line a path is typed into has no other field to move to, so tab keeps what the text input does with
// it: it takes the suggestion shown, and up/down switch between several. The suggestions are the
// subdirectories of what is typed up to its last separator, never files; with nothing typed yet, the
// directory the TUI was started in is the one suggestion. A path is checked by the rules of the core when it
// is taken, and the list as a whole when the form is saved. Whether the directory exists is not checked: a
// path is only resolved when a call runs, and config validate warns about one that names nothing.

// pathSuggestionsShown is how many suggestions stand under the typed path at once.
const pathSuggestionsShown = 5

// openPaths opens the entries of the focused path list on its first path.
func (m *Model) openPaths() {
	m.pathList.reset(append([]string(nil), m.fields[m.focus].entries...))
	m.pathEdit, m.pathRemove = -1, false
	m.pathInput.Blur()
	m.screen = screenPaths
	m.clearMessages()
}

// updatePaths handles the path list screen: the typed path first, then the remove question, then the list
// itself.
func (m *Model) updatePaths(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "f2":
		// F2 saves from every state of the list, the way it does from every row of the form. A typed path
		// is taken first; one the rules refuse stays typed with the reason, and nothing is saved.
		if m.pathEdit >= 0 {
			m.takePath()
			if m.pathEdit >= 0 {
				return nil
			}
		}
		// A remove question left unanswered keeps its path.
		m.pathRemove = false
		m.keepPaths()
		return m.updateForm(key)
	}
	if m.pathEdit >= 0 {
		switch key.String() {
		case "esc":
			m.pathEdit = -1
			m.pathInput.Blur()
			m.clearMessages()
		case "enter":
			m.takePath()
		default:
			if key.String() == "tab" && m.pathInput.Value() == "" && m.startDir != "" {
				// Nothing typed has no suggestion of the text input's own; the start directory is the first.
				m.pathInput.SetValue(withSeparator(m.startDir))
				m.pathInput.CursorEnd()
				m.suggestPaths()
				return nil
			}
			var cmd tea.Cmd
			m.pathInput, cmd = m.pathInput.Update(key)
			m.suggestPaths()
			return cmd
		}
		return nil
	}
	if m.pathRemove {
		switch key.String() {
		case "y":
			m.removePath()
		case "n", "esc":
			m.pathRemove = false
			m.status = "Path kept"
		}
		return nil
	}
	path, selected := m.pathList.selected()
	switch key.String() {
	case "esc":
		return m.leavePaths()
	case "a":
		m.editPath(len(m.pathList.all), "")
	case "enter", "e":
		if !selected {
			// An empty list has nothing to edit, so enter adds its first path.
			m.editPath(len(m.pathList.all), "")
			return nil
		}
		m.editPath(m.pathList.cursor, path)
	case "x", "d":
		if selected {
			m.pathRemove = true
			m.clearMessages()
		}
	case "up", "k":
		m.pathList.move(-1)
	case "down", "j":
		m.pathList.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.pathWindow()
		m.pathList.page(key.String(), start, end)
	}
	return nil
}

// keepPaths hands the list over to its row and returns to the form. Nothing is written yet.
func (m *Model) keepPaths() {
	m.fields[m.focus].entries = append([]string(nil), m.pathList.all...)
	m.screen = screenForm
	m.clearMessages()
}

// leavePaths closes the list without keeping it. An unchanged list closes at once; a changed one asks
// first, like a changed form does.
func (m *Model) leavePaths() tea.Cmd {
	m.clearMessages()
	if slices.Equal(m.pathList.all, m.fields[m.focus].entries) {
		m.screen = screenForm
		return nil
	}
	m.leaveFrom = screenPaths
	m.screen = screenLeave
	return nil
}

// answerPathsLeave answers the question a changed list asks before it closes: k keeps the list in its row,
// d drops the changes, and esc returns to the list unchanged. No other key answers it.
func (m *Model) answerPathsLeave(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.screen = screenPaths
		m.status = "Still editing the paths; nothing was kept or discarded"
	case "k":
		m.keepPaths()
		m.status = "Path list kept in the form; nothing was written yet"
	case "d":
		m.screen = screenForm
		m.clearMessages()
		m.status = "Path changes discarded; the list is as it was"
	}
	return nil
}

// editPath starts typing the path at index, or a new one at the end of the list.
func (m *Model) editPath(index int, value string) {
	m.pathEdit = index
	m.pathInput.SetValue(value)
	m.pathInput.CursorEnd()
	m.pathInput.Focus()
	m.suggestPaths()
	m.clearMessages()
}

// takePath puts the typed path into the list once the rules of the core accept it and the list does not
// name its directory yet. A refused path stays typed with the reason. A trailing separator, which every
// suggestion ends in, is not kept.
func (m *Model) takePath() {
	value := strings.TrimSpace(m.pathInput.Value())
	for len(value) > 1 && os.IsPathSeparator(value[len(value)-1]) {
		value = value[:len(value)-1]
	}
	m.status = ""
	if value == "" {
		m.fail = "a path must not be empty; esc cancels the entry"
		return
	}
	if err := config.CheckPath(value); err != nil {
		m.fail = "path: " + err.Error()
		return
	}
	entries := append([]string(nil), m.pathList.all...)
	for i, entry := range entries {
		if i != m.pathEdit && config.SamePath(entry, value) {
			m.fail = "this directory is already in the list"
			return
		}
	}
	if m.pathEdit >= len(entries) {
		entries = append(entries, value)
	} else {
		entries[m.pathEdit] = value
	}
	m.pathList.setItems(entries)
	m.pathList.selectName(value)
	m.pathEdit = -1
	m.pathInput.Blur()
	m.clearMessages()
}

// removePath takes the selected path out of the list.
func (m *Model) removePath() {
	m.pathRemove = false
	path, ok := m.pathList.selected()
	if !ok {
		return
	}
	i := m.pathList.cursor
	entries := append(append([]string(nil), m.pathList.all[:i]...), m.pathList.all[i+1:]...)
	m.pathList.setItems(entries)
	m.status = "Removed " + path
}

// suggestPaths hands the text input the directories that complete what is typed.
func (m *Model) suggestPaths() {
	m.pathInput.SetSuggestions(directorySuggestions(m.pathInput.Value()))
}

// directorySuggestions are the directories that complete typed: those inside the directory it names up to
// its last separator whose names start with the rest, each written as that directory followed by the name
// and a separator, so the next tab goes on into it. The names are matched with their case, so a completion
// never changes what is typed; a hidden directory is offered only once its dot is typed. typed is taken only
// in a form a path may have, absolute or starting with ~/.
func directorySuggestions(typed string) []string {
	cut := strings.LastIndexFunc(typed, isSeparator) + 1
	dir, rest := typed[:cut], typed[cut:]
	if dir == "" || (!projectpath.IsHomeRelative(dir) && !filepath.IsAbs(dir)) {
		return nil
	}
	expanded, err := projectpath.Expand(dir)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(expanded)
	if err != nil {
		return nil
	}
	var suggestions []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, rest) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(rest, ".")) {
			continue
		}
		isDir := entry.IsDir()
		if entry.Type()&fs.ModeSymlink != 0 {
			// A link to a directory is offered like the directory it points to.
			info, err := os.Stat(filepath.Join(expanded, name))
			isDir = err == nil && info.IsDir()
		}
		if isDir {
			suggestions = append(suggestions, dir+name+string(filepath.Separator))
		}
	}
	return suggestions
}

func isSeparator(r rune) bool {
	return r < 0x80 && os.IsPathSeparator(uint8(r))
}

// withSeparator is dir with one separator at its end.
func withSeparator(dir string) string {
	if dir != "" && os.IsPathSeparator(dir[len(dir)-1]) {
		return dir
	}
	return dir + string(filepath.Separator)
}

// shownPathSuggestions are the suggestions that stand under the typed path, the one tab takes among them:
// the start directory while nothing is typed, and otherwise the matches of the text input around the
// current one.
func (m *Model) shownPathSuggestions() ([]string, int) {
	if m.pathInput.Value() == "" {
		if m.startDir == "" {
			return nil, -1
		}
		return []string{withSeparator(m.startDir)}, 0
	}
	matches := m.pathInput.MatchedSuggestions()
	current := m.pathInput.CurrentSuggestionIndex()
	start := max(0, min(current-pathSuggestionsShown+1, len(matches)-pathSuggestionsShown))
	end := min(len(matches), start+pathSuggestionsShown)
	return matches[start:end], current - start
}

// pathsView draws the path list screen: its frame, and between them as many paths as fit.
func (m *Model) pathsView() string {
	header, footer := m.pathFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.pathList, header, footer, m.pathRow)
	for i := start; i < end; i++ {
		b.WriteString(m.pathRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// pathFrame is everything of the path list screen except the paths: above them what the list does, below
// them the path being typed with its suggestions or the remove question, the keys and the notes.
func (m *Model) pathFrame() (string, string) {
	total := len(m.pathList.all)
	title := "Paths"
	if name := m.fieldValue("name"); name != "" {
		title += " of " + name
	}
	if total > 0 {
		title += fmt.Sprintf("  %d/%d", m.pathList.cursor+1, total)
	}
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, title) + "\n")
	head.WriteString(m.wrapped(hintStyle, pathsHint) + "\n\n")
	if total == 0 {
		head.WriteString(m.wrapped(hintStyle, "(none: the connection applies in every project; a adds one)") + "\n")
	}

	var foot strings.Builder
	keys := "a add · enter edit · x remove · up/down move · F2 save · esc close"
	switch {
	case m.pathEdit >= 0:
		label := "add path: "
		if m.pathEdit < total {
			label = "edit path: "
		}
		prefix, width := m.fit(label)
		in := m.pathInput
		in.Width = max(width-1, 1)
		// The text input pads to its width without counting the completion it draws after the cursor, so
		// the line is cut back to the room there is.
		foot.WriteString("\n" + prefix + lipgloss.NewStyle().MaxWidth(width).Render(in.View()) + "\n")
		suggestions, current := m.shownPathSuggestions()
		for i, suggestion := range suggestions {
			foot.WriteString(m.row(i == current, suggestion) + "\n")
		}
		keys = "enter take · F2 save · esc cancel entry"
		if len(suggestions) > 0 {
			keys = "tab complete · up/down switch · " + keys
		}
	case m.pathRemove:
		path, _ := m.pathList.selected()
		// %q keeps control characters visible, but it would also double every backslash of a Windows path.
		quoted := strings.ReplaceAll(fmt.Sprintf("%q", path), `\\`, `\`)
		foot.WriteString("\n" + m.wrapped(warningStyle, "Remove "+quoted+" from the list?") + "\n")
		keys = "y remove · n keep"
	}
	foot.WriteString(m.hint(keys))
	return head.String(), foot.String() + m.notes()
}

// pathRow draws the path at index i of the list.
func (m *Model) pathRow(i int) string {
	return m.row(i == m.pathList.cursor, m.pathList.matches[i])
}

func (m *Model) pathWindow() (int, int) {
	header, footer := m.pathFrame()
	return m.windowIn(&m.pathList, header, footer, m.pathRow)
}
