package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// openHelp shows the help topics in the workspace, over the tab line or the list it was opened from.
func (m *Model) openHelp() {
	m.helpFrom = m.screen
	m.screen = screenHelp
	m.help.reset()
	m.clearMessages()
}

func (m *Model) updateHelp(key tea.KeyPressMsg) tea.Cmd {
	topics := len(helptopics.All())
	_, _, room := m.helpFrame()
	switch {
	case keyIs(key, actBack) || keyIs(key, actHelp):
		m.screen = m.helpFrom
	case keyIs(key, actRight) || keyIs(key, actTopicNext):
		m.helpTopic = wrap(m.helpTopic+1, topics)
		m.help.reset()
	case keyIs(key, actLeft) || keyIs(key, actTopicPrev):
		m.helpTopic = wrap(m.helpTopic-1, topics)
		m.help.reset()
	default:
		m.clampHelp()
		m.help.scroll(key, room)
	}
	m.clampHelp()
	return nil
}

// helpMaxInner is the widest text of the help overlay.
const helpMaxInner = 72

// helpOverlay reports whether the help stands as an overlay over the screen it was opened from: the terminal
// has the room a modal needs. Smaller, it fills the workspace.
func (m *Model) helpOverlay() bool {
	return m.termWidth >= modalMinWidth && m.termHeight >= modalMinHeight
}

// helpInner is the width the help text is wrapped into.
func (m *Model) helpInner() int {
	if m.helpOverlay() {
		return min(m.termWidth-8, helpMaxInner)
	}
	return m.usable(0)
}

// helpLines is the text of the current topic, wrapped into the width the help has.
func (m *Model) helpLines() []string {
	topic := helptopics.All()[m.helpTopic]
	text := topic.Text
	if topic.Name == helptopics.All()[0].Name {
		text += "\n\n" + keyListText()
	}
	return strings.Split(helptopics.Wrap(text, m.helpInner()), "\n")
}

// keysHeading is the first line of the list of keys; helpLine draws it as a heading.
const keysHeading = "Keys in force on this device:"

// isHeading reports whether a line of a topic is a heading: short, without indent, ending in a colon.
func isHeading(line string) bool {
	return strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " ") && lipgloss.Width(line) <= 40
}

// helpLine draws one wrapped line of a topic: a heading is bold, and in the list of keys the key is in the
// accent colour. headings are the headings of the unwrapped text, so the end of a long wrapped sentence that
// happens to be short is no heading.
func helpLine(line string, headings map[string]bool) string {
	switch {
	case strings.HasPrefix(line, "- ") && strings.Contains(line, ": "):
		i := strings.LastIndex(line, ": ") + 2
		return line[:i] + styles.active.Render(line[i:])
	case headings[line]:
		return styles.title.Render(line)
	}
	return line
}

// helpHeadings are the headings of the text of the current topic.
func (m *Model) helpHeadings() map[string]bool {
	topic := helptopics.All()[m.helpTopic]
	text := topic.Text
	if topic.Name == helptopics.All()[0].Name {
		text += "\n\n" + keyListText()
	}
	headings := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if isHeading(line) {
			headings[line] = true
		}
	}
	return headings
}

// macOSFunctionKeyNote explains why F2, used throughout the editor, might not reach it on a Mac
// keyboard without fn held or the system setting turned on; every other host reaches them directly.
const macOSFunctionKeyNote = "On macOS, hold fn for F-keys, or turn on 'Use F1, F2, etc. keys as standard " +
	"function keys' in System Settings."

// helpKeys is the key line of the help: at the foot of the overlay, with the keys in the accent colour, or, in
// the workspace, the footer of the frame.
func (m *Model) helpKeys(keys string) string {
	if !m.inModal {
		return m.keyHint(keys)
	}
	return "\n" + strings.Join(m.keyHintLinesWith(keys, keyHintGroupFoot), "\n")
}

// keyLineCount is how many lines the key line takes in the overlay, whatever is logged for the mouse.
func (m *Model) keyLineCount(keys string) int {
	log := m.footerLog
	m.footerLog = nil
	defer func() { m.footerLog = log }()
	return len(m.keyHintLines(keys))
}

// helpTopics draws the topics with the current one in brackets, like the tabs of a detail pane.
func (m *Model) helpTopics() string {
	var names []string
	for _, topic := range helptopics.All() {
		names = append(names, topic.Name)
	}
	strip, _ := tabStrip(names, m.helpTopic, true)
	return strip
}

// helpFrame is everything of the help screen except the text: above it the topics with the current one in
// brackets, and the macOS note where it fits, below it the position in the text and the keys. room is the
// number of text lines between them.
func (m *Model) helpFrame() (string, string, int) {
	strip := m.helpTopics()
	base := styles.title.Render("Help") + "  " + strip + "\n\n"
	if m.helpOverlay() {
		base = strip + "\n\n"
	}
	note := render(styles.hint, m.helpInner(), macOSFunctionKeyNote) + "\n\n"
	lines := len(m.helpLines())
	keys := "left/right topic · up/down scroll · pgup/pgdown page · {back}/{help} close"
	// layout lays the topic text out under head and reports how many lines of it fit; a topic longer than
	// that gains the position line, which itself takes one of those lines.
	layout := func(head string) (string, string, int) {
		foot := m.helpKeys(keys)
		var room int
		if m.helpOverlay() {
			// The dialog keeps two rows free around it; the key line is set off by a blank line.
			room = m.termHeight - 6 - strings.Count(head, "\n") - m.keyLineCount(keys) - 1
		} else {
			room = m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1
		}
		if lines > room {
			room--
			offset := m.help.offset()
			first := min(offset+1, lines)
			foot = "\n" + render(styles.hint, m.helpInner(), fmt.Sprintf("lines %d-%d of %d", first,
				min(offset+max(room, 1), lines), lines)) + foot
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

// clampHelp hands the text and the room it has to the scroll area, which keeps the position inside the
// text, also after a resize.
func (m *Model) clampHelp() []string {
	_, _, room := m.helpFrame()
	return m.help.window(m.helpLines(), room, false)
}

// helpView draws the current topic from its scroll position.
func (m *Model) helpView() string {
	rows := m.clampHelp()
	head, foot, _ := m.helpFrame()
	shown := make([]string, 0, len(rows))
	headings := m.helpHeadings()
	for _, line := range rows {
		shown = append(shown, helpLine(line, headings))
	}
	return head + strings.Join(shown, "\n") + "\n" + foot
}

// keyListText names the keys of the global actions as they are bound now, so the help is right whatever the
// preferences changed.
func keyListText() string {
	lines := []string{keysHeading}
	for _, r := range keyRows {
		a := userActions[r.action]
		key := keyName(keyOf(a))
		if r.action == uiprefs.ActionInfo {
			a = actInfo
			key = keyLabel(a)
		}
		lines = append(lines, "- "+strings.TrimSuffix(r.label, " key")+": "+key)
	}
	return strings.Join(lines, "\n")
}
