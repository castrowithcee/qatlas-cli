package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

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
func (m *Model) updatePaths(key tea.KeyPressMsg) tea.Cmd {
	switch {
	case keyIs(key, actSave):
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
		switch {
		case keyIs(key, actBack):
			if m.pathTyped() {
				m.askLeave(false)
				return nil
			}
			m.pathEdit = -1
			m.pathInput.Blur()
			m.clearMessages()
		case keyIs(key, actEnter):
			if m.pathInput.accept() {
				m.takePath()
			}
		default:
			return m.pathInput.update(key)
		}
		return nil
	}
	if m.pathRemove {
		switch {
		case keyIs(key, actYes):
			m.removePath()
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.pathRemove = false
			m.status = tr(tPathPathKept)
		}
		return nil
	}
	path, selected := m.pathList.selected()
	switch {
	case keyIs(key, actBack):
		return m.leavePaths()
	case keyIs(key, actNew):
		m.editPath(len(m.pathList.all), "")
	case keyIs(key, actEnter) || keyIs(key, actEdit):
		if !selected {
			// An empty list has nothing to edit, so enter adds its first path.
			m.editPath(len(m.pathList.all), "")
			return nil
		}
		m.editPath(m.pathList.cursor, path)
	case keyIs(key, actDelete):
		if selected {
			m.pathRemove = true
			m.clearMessages()
		}
	case keyIs(key, actUp):
		m.pathList.move(-1)
	case keyIs(key, actDown):
		m.pathList.move(1)
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		start, end := m.pathWindow()
		m.pathList.page(key, start, end)
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
		m.fail = tr(tPathAPathMustNotBe)
		return
	}
	if isFilesLabel(m.fields[m.focus].label) {
		if err := config.CheckFilesEntry(value); err != nil {
			m.fail = tr(tPathDirectory, err.Error())
			return
		}
	} else if err := config.CheckPath(value); err != nil {
		m.fail = tr(tPathPath, err.Error())
		return
	}
	entries := append([]string(nil), m.pathList.all...)
	for i, entry := range entries {
		if i != m.pathEdit && config.SamePath(entry, value) {
			m.fail = tr(tPathThisDirectoryIsAlreadyIn)
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
	m.status = tr(tPathRemoved, path)
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
	m.hitSelect(&m.pathList, screenPaths, header, start, end, m.pathRow)
	b.WriteString(footer)
	return b.String()
}

// pathFrame is everything of the path list screen except the paths: above them what the list does, below
// them the path being typed with its suggestions or the remove question, the keys and the notes.
func (m *Model) pathFrame() (string, string) {
	p := m.dialogPanelOf()
	total := len(m.pathList.all)
	title, hint, none := tr(tPathPaths), tr(tFormPathsHint), tr(tPathNoneEveryProject)
	if label := m.fields[m.focus].label; isFilesLabel(label) {
		hint = tr(tFormFilesReadHint)
		if label == filesWriteLabel {
			hint = tr(tFormFilesWriteHint)
		}
		title = tr(tPathDirectoriesFor, labelText(label))
		none = tr(tPathNoneReleased)
	}
	if name := m.fieldValue("name"); name != "" {
		title += tr(tPathOf, name)
	}
	position := ""
	if total > 0 {
		position = fmt.Sprintf("%d/%d", m.pathList.cursor+1, total)
	}
	var head strings.Builder
	head.WriteString(p.top(title, position))
	head.WriteString(p.text(styles.hint, hint))
	head.WriteString(p.blank())
	if total == 0 {
		head.WriteString(p.text(styles.hint, none))
	}

	var foot strings.Builder
	keys := "n new · enter/e edit · {delete} remove · up/down move · {save} save · {back} close · {command} cmds · {help} help"
	switch {
	case m.pathEdit >= 0 && m.backdrop:
		// The typed path stands in its own dialog (see pathPrompt) over this screen.
	case m.pathEdit >= 0:
		label := tr(tPathAddPath)
		if m.pathEdit < total {
			label = tr(tPathEditPath)
		}
		foot.WriteString(p.blank() + p.styled(m.pathLinesIn(p.room, label, &m.pathInput)))
		keys = pathKeys(&m.pathInput, "enter take · {save} save · {back} cancel entry")
	case m.pathRemove:
		path, _ := m.pathList.selected()
		// %q keeps control characters visible, but it would also double every backslash of a Windows path.
		quoted := strings.ReplaceAll(fmt.Sprintf("%q", path), `\\`, `\`)
		foot.WriteString(p.blank() + p.text(styles.warning, warnMark+" "+tr(tPathRemoveFromTheList, quoted)))
		keys = "y remove · n/{back} keep · {command} cmds · {help} help"
	}
	foot.WriteString(p.bottom())
	foot.WriteString(m.keyHint(keys))
	return head.String(), foot.String() + m.notes()
}

// pathPrompt is the dialog the path being added or edited is typed into.
func (m *Model) pathPrompt() promptDialog {
	noun := tr(tPathPath2)
	if isFilesLabel(m.fields[m.focus].label) {
		noun = tr(tPathDirectory2)
	}
	title := tr(tPathAdd, noun)
	if m.pathEdit < len(m.pathList.all) {
		title = tr(tFormEdit, noun)
	}
	return promptDialog{
		title: title,
		input: m.pathLines("  ", &m.pathInput),
		field: true,
		keys:  pathKeys(&m.pathInput, "enter take · {save} save · {back} cancel entry"),
	}
}

// pathRow draws the path at index i of the list.
func (m *Model) pathRow(i int) string {
	return m.dialogPanelOf().row(i == m.pathList.cursor, m.pathList.matches[i])
}

func (m *Model) pathWindow() (int, int) {
	header, footer := m.pathFrame()
	return m.windowIn(&m.pathList, header, footer, m.pathRow)
}
