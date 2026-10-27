package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The copy key hands text to the terminal as an OSC 52 sequence, which also reaches the local clipboard over
// SSH. The terminal never confirms it, so the editor only says that it handed the text over. What is handed
// over is built from the same safe text the screen draws, never from the raw data behind it.

// copySource is where the copy key takes its text from right now, or nil when there is nothing to copy: the
// open explanation, the entry of a log, the detail of an approval, the pane of the marked entry, or else the
// name of the marked row. The text is built only when the key is pressed.
func (m *Model) copySource() func() string {
	switch {
	case m.infoOpen:
		return func() string { return infoText(m.infoShown) }
	case m.screen == screenLogDetail:
		lv := m.logs
		if lv.detail < 0 || lv.detail >= len(lv.rows) {
			return nil
		}
		return func() string { return m.logDetailText(lv.rows[lv.detail]) }
	case m.screen == screenConfirm && m.approvalDetail != "":
		return m.approvalDetailText
	case m.screen == screenPaths:
		if m.pathEdit >= 0 || m.pathRemove {
			return nil
		}
		if path, ok := m.pathList.selected(); ok {
			return func() string { return path }
		}
	case m.screen == screenList && m.barFocus == 0 && !m.list.editing &&
		m.section != sectionOverview && m.section != sectionLogs:
		name, ok := m.selected()
		if !ok {
			return nil
		}
		if m.detailFocused() {
			return func() string { return m.detailText(name) }
		}
		return func() string { return name }
	}
	return nil
}

// copyKeyShown reports whether a footer names the copy key: where it copies and a screen lists it itself
// nowhere else (the approval detail names it in its own key line).
func (m *Model) copyKeyShown() bool {
	if m.infoOpen || m.approvalDetail != "" || !m.helpReachable() {
		return false
	}
	return m.copySource() != nil
}

// withCopyKey adds the copy group to a key line, ahead of the groups every screen ends with.
func withCopyKey(text string) string {
	if strings.Contains(text, "{copy}") {
		return text
	}
	at := -1
	for _, marker := range []string{"{info} info", "{command} cmds", "{help} help"} {
		if i := strings.Index(text, marker); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return text + " · {copy} copy"
	}
	return text[:at] + "{copy} copy · " + text[at:]
}

// copyCurrent hands the text of the copy source to the terminal and says so; it is nil where there is
// nothing to copy.
func (m *Model) copyCurrent() tea.Cmd {
	source := m.copySource()
	if source == nil {
		return nil
	}
	text := m.copyClean(source())
	if text == "" {
		return nil
	}
	m.clearMessages()
	m.status = tr(tCopyHandedOver)
	return tea.SetClipboard(text)
}

// copyClean makes text safe to hand over: no escape sequence, no registered secret, no invalid UTF-8 and no
// control character but the line breaks. Unlike logText it never cuts, so a long text is copied whole.
func (m *Model) copyClean(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", " ")
	text = ansi.Strip(text)
	text = strings.ToValidUTF8(m.redactor.Apply(text), "�")
	text = strings.Map(func(r rune) rune {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return '�'
		}
		return r
	}, text)
	return strings.TrimSpace(text)
}

// factsText is facts as plain lines, "label: value", a value that goes on under an empty label indented.
func factsText(facts []styledFact) []string {
	var lines []string
	for _, f := range facts {
		value := f.value
		if f.drawn != "" {
			value = ansi.Strip(f.drawn)
		}
		if value == "" {
			continue
		}
		if f.label == "" {
			lines = append(lines, "  "+value)
			continue
		}
		lines = append(lines, wordText(f.label)+": "+value)
	}
	return lines
}

// infoText is the explanation as the overlay shows it, without the wrapping.
func infoText(s infoSubject) string {
	parts := append([]string{s.title}, s.paragraphs()...)
	if s.doc != "" {
		parts = append(parts, tr(tDocGuide)+" "+s.doc)
	}
	return strings.Join(parts, "\n\n")
}

// logDetailText is the detail screen of one log entry.
func (m *Model) logDetailText(row logRow) string {
	lines := []string{m.logDetailTitle(row), ""}
	for _, f := range m.logDetailFields(row) {
		lines = append(lines, f.label+": "+f.value)
	}
	return strings.Join(lines, "\n")
}

// detailText is the detail pane of the marked entry as it shows the tab in view: name, provider and every line
// of the tab, also those below the pane.
func (m *Model) detailText(name string) string {
	lines := []string{name, m.providerOf(m.section)(name), ""}
	if m.shownTab() == tabRaw {
		return strings.Join(append(lines, m.rawLines(name)...), "\n")
	}
	facts := m.detailFacts(name, m.shownTab())
	styled := make([]styledFact, len(facts))
	for i, f := range facts {
		f.value = m.logText(f.value)
		styled[i] = styledFact{detailFact: f}
	}
	return strings.Join(append(lines, factsText(styled)...), "\n")
}
