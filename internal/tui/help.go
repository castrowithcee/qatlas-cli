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
	m.clearMessages()
}

func (m *Model) updateHelp(key tea.KeyMsg) tea.Cmd {
	topics := len(helptopics.All())
	_, _, room := m.helpFrame()
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "?":
		m.screen = m.helpFrom
	case "right", "l", "tab":
		m.helpTopic, m.helpOffset = wrap(m.helpTopic+1, topics), 0
	case "left", "h", "shift+tab":
		m.helpTopic, m.helpOffset = wrap(m.helpTopic-1, topics), 0
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
	return strings.Split(helptopics.Wrap(helptopics.All()[m.helpTopic].Text, m.usable(0)), "\n")
}

// helpFrame is everything of the help screen except the text: above it the topics with the current one in
// brackets, below it the position in the text and the keys. room is the number of text lines between them.
func (m *Model) helpFrame() (string, string, int) {
	var names []string
	for i, topic := range helptopics.All() {
		if i == m.helpTopic {
			names = append(names, "["+topic.Name+"]")
			continue
		}
		names = append(names, topic.Name)
	}
	head := m.wrapped(titleStyle, "Help  "+strings.Join(names, "  ")) + "\n\n"
	lines := len(m.helpLines())
	keys := "left/right topic · up/down scroll · esc close"
	if lipgloss.Width(keys) > m.width {
		keys = "left/right topic · up/down · esc close"
	}
	foot := m.hint(keys)
	room := m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1
	if lines > room {
		// The position line is only there when the text scrolls; it takes one line of the text's room.
		room--
		first := min(m.helpOffset+1, lines)
		foot = "\n" + m.wrapped(hintStyle, fmt.Sprintf("lines %d-%d of %d", first,
			min(m.helpOffset+max(room, 1), lines), lines)) + foot
	}
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
