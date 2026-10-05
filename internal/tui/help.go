package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

// openHelp shows the help topics in the workspace, over the sidebar or the list it was opened from.
func (m *Model) openHelp() {
	m.helpFrom = m.screen
	m.screen = screenHelp
	m.helpOffset = 0
	m.helpDetail = ""
	m.clearMessages()
}

// openFieldHelp shows the whole hint of the focused row in the help screen. The form shows a short form
// of it, so that a long text never crowds out the rows and the keys; this is where the rest is read.
func (m *Model) openFieldHelp() {
	f := m.fields[m.focus]
	text := m.fieldDetail(f)
	if text == "" {
		text = "this field has no further help"
	}
	m.openHelp()
	m.helpFrom = screenForm
	m.helpDetail = f.label + "\n\n" + strings.ReplaceAll(text, "; ", ";\n")
}

func (m *Model) updateHelp(key tea.KeyMsg) tea.Cmd {
	topics := len(helptopics.All())
	_, _, room := m.helpFrame()
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "?", "f1":
		m.screen, m.helpDetail = m.helpFrom, ""
	case "right", "l", "tab":
		if m.helpDetail == "" {
			m.helpTopic, m.helpOffset = wrap(m.helpTopic+1, topics), 0
		}
	case "left", "h", "shift+tab":
		if m.helpDetail == "" {
			m.helpTopic, m.helpOffset = wrap(m.helpTopic-1, topics), 0
		}
	case "down", "j":
		m.helpOffset++
	case "up", "k":
		m.helpOffset--
	case "pgdown", " ":
		m.helpOffset += room
	case "pgup":
		m.helpOffset -= room
	case "home":
		m.helpOffset = 0
	case "end":
		m.helpOffset = len(m.helpLines())
	}
	m.clampHelp()
	return nil
}

// helpLines is the text of the current topic, wrapped into the workspace.
func (m *Model) helpLines() []string {
	text := helptopics.All()[m.helpTopic].Text
	if m.helpDetail != "" {
		text = m.helpDetail
	}
	return strings.Split(helptopics.Wrap(text, m.usable(0)), "\n")
}

// macOSFunctionKeyNote explains why F2 and F3, used throughout the editor, might not reach it on a Mac
// keyboard without fn held or the system setting turned on; every other host reaches them directly.
const macOSFunctionKeyNote = "On macOS, hold fn for F-keys, or turn on 'Use F1, F2, etc. keys as standard " +
	"function keys' in System Settings."

// helpFrame is everything of the help screen except the text: above it the topics with the current one in
// brackets, and the macOS note where it fits, below it the position in the text and the keys. room is the
// number of text lines between them.
func (m *Model) helpFrame() (string, string, int) {
	var names []string
	for i, topic := range helptopics.All() {
		if i == m.helpTopic {
			names = append(names, "["+topic.Name+"]")
			continue
		}
		names = append(names, topic.Name)
	}
	title := "Help  " + strings.Join(names, "  ")
	if m.helpDetail != "" {
		title = "Help  field"
	}
	base := m.wrapped(titleStyle, title) + "\n\n"
	note := m.wrapped(hintStyle, macOSFunctionKeyNote) + "\n\n"
	lines := len(m.helpLines())
	keys := "left/right topic · up/down scroll · esc close"
	if lipgloss.Width(keys) > m.width {
		keys = "left/right topic · up/down · esc close"
	}
	if m.helpDetail != "" {
		keys = "up/down scroll · esc close"
	}
	// layout lays the topic text out under head and reports how many lines of it fit; a topic longer than
	// that gains the position line, which itself takes one of those lines.
	layout := func(head string) (string, string, int) {
		foot := m.hint(keys)
		room := m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1
		if lines > room {
			room--
			first := min(m.helpOffset+1, lines)
			foot = "\n" + m.wrapped(hintStyle, fmt.Sprintf("lines %d-%d of %d", first,
				min(m.helpOffset+max(room, 1), lines), lines)) + foot
		}
		return head, foot, room
	}
	// The macOS note is worth a line only where the terminal has room left over for it, position line
	// included; a tight window keeps every line for the topic text instead, the way the keys line above
	// already shortens itself.
	if head, foot, room := layout(base + note); room >= 1 {
		return head, foot, room
	}
	head, foot, room := layout(base)
	return head, foot, max(room, 1)
}

// clampHelp keeps the scroll position inside the text, also after a resize.
func (m *Model) clampHelp() {
	_, _, room := m.helpFrame()
	m.helpOffset = max(min(m.helpOffset, len(m.helpLines())-room), 0)
}

// helpView draws the current topic from its scroll position.
func (m *Model) helpView() string {
	m.clampHelp()
	head, foot, room := m.helpFrame()
	lines := m.helpLines()
	end := min(m.helpOffset+room, len(lines))
	return head + strings.Join(lines[m.helpOffset:end], "\n") + "\n" + foot
}
