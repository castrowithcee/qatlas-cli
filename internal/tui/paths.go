package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The path list of a connection is edited in a screen of its own, opened from its row like the target list:
// a adds a path, enter or e edits the selected one, and d removes it after asking. F2 keeps the list and
// saves the form in one step, from every state of the screen, the typed path included. esc is one level back:
// it closes an unchanged list, cancels an unchanged entry, and asks about a changed list or a typed entry,
// so no change is dropped silently.
//
// The line a path is typed into is a pathField, which has the keys of every path prompt: tab takes the
// suggestion shown, and up/down switch between several. The suggestions are the subdirectories of what is
// typed up to its last separator, never files; with nothing typed yet, the directory the TUI was started in
// is the one suggestion. A path is checked by the rules of the core when it is taken, and the list as a
// whole when the form is saved. Whether the directory exists is not checked: a path is only resolved when a
// call runs, and config validate warns about one that names nothing.

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
			if m.pathTyped() {
				m.askLeave(false)
				return nil
			}
			m.pathEdit = -1
			m.pathInput.Blur()
			m.clearMessages()
		case "enter":
			if m.pathInput.accept() {
				m.takePath()
			}
		default:
			return m.pathInput.update(key)
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
	case "d":
		if selected {
			m.pathRemove = true
			m.clearMessages()
		}
	case "up":
		m.pathList.move(-1)
	case "down":
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
	m.askLeave(false)
	return nil
}

// pathTyped reports whether the path being typed differs from the one it started from.
func (m *Model) pathTyped() bool {
	original := ""
	if m.pathEdit < len(m.pathList.all) {
		original = m.pathList.all[m.pathEdit]
	}
	return m.pathInput.Value() != original
}

// pathsPending reports whether closing the screen for good would lose a changed list or a typed path.
func (m *Model) pathsPending() bool {
	if m.pathEdit >= 0 && m.pathTyped() {
		return true
	}
	return !slices.Equal(m.pathList.all, m.fields[m.focus].entries)
}

// editPath starts typing the path at index, or a new one at the end of the list.
func (m *Model) editPath(index int, value string) {
	m.pathEdit = index
	m.pathInput.open(value, m.startDir)
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
	if isFilesLabel(m.fields[m.focus].label) {
		if err := config.CheckFilesEntry(value); err != nil {
			m.fail = "directory: " + err.Error()
			return
		}
	} else if err := config.CheckPath(value); err != nil {
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
	title, hint, none := "Paths", pathsHint, "(none: the connection applies in every project; a adds one)"
	if label := m.fields[m.focus].label; isFilesLabel(label) {
		verb := "read files from"
		if label == filesWriteLabel {
			verb = "write files to"
		}
		title, hint = "Directories for "+label, fmt.Sprintf(filesHint, verb)
		none = "(none: no local files released; a adds one)"
	}
	if name := m.fieldValue("name"); name != "" {
		title += " of " + name
	}
	if total > 0 {
		title += fmt.Sprintf("  %d/%d", m.pathList.cursor+1, total)
	}
	var head strings.Builder
	head.WriteString(m.wrapped(styles.title, title) + "\n")
	head.WriteString(m.wrapped(styles.hint, hint) + "\n\n")
	if total == 0 {
		head.WriteString(m.wrapped(styles.hint, none) + "\n")
	}

	var foot strings.Builder
	keys := "a add · enter edit · d remove · up/down move · F2 save · esc close · : cmds · ? help"
	switch {
	case m.pathEdit >= 0:
		label := "add path: "
		if m.pathEdit < total {
			label = "edit path: "
		}
		foot.WriteString("\n" + m.pathLines(label, &m.pathInput))
		keys = pathKeys(&m.pathInput, "enter take · F2 save · esc cancel entry")
	case m.pathRemove:
		path, _ := m.pathList.selected()
		// %q keeps control characters visible, but it would also double every backslash of a Windows path.
		quoted := strings.ReplaceAll(fmt.Sprintf("%q", path), `\\`, `\`)
		foot.WriteString("\n" + m.wrapped(styles.warning, "Remove "+quoted+" from the list?") + "\n")
		keys = "y remove · n/esc keep · : cmds · ? help"
	}
	foot.WriteString(m.keyHint(keys))
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
