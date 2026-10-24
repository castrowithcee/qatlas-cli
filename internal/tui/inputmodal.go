package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// promptDialog is the one shape of every short input dialog and of the result of a connection test: a
// title, an optional notice, the finished input rows, one hint under them and the key line. The modal and
// the full-screen fallback draw the same text. It carries no typed value: input holds rows the text input
// itself has drawn, masked where the input is masked.
type promptDialog struct {
	title string
	// notice is a finished text above the input.
	notice string
	// input is the finished input rows (they may span several lines, as a path with its suggestions does).
	input string
	// hint is the explanation under the input.
	hint string
	keys string
}

// promptText draws a dialog. The modal carries the title in its border, so titled is false there.
func (m *Model) promptText(d promptDialog, titled bool) string {
	var b strings.Builder
	if titled {
		// An error on a screen of the smallest size takes the blank line under the title.
		gap := "\n\n"
		if m.fail != "" && m.termHeight <= minimumHeight {
			gap = "\n"
		}
		b.WriteString(m.wrapped(styles.title, d.title) + gap)
	}
	if d.notice != "" {
		b.WriteString(d.notice + "\n\n")
	}
	if d.input != "" {
		b.WriteString(strings.TrimRight(d.input, "\n") + "\n")
	}
	if d.hint != "" {
		b.WriteString(m.indented(d.hint) + "\n")
	}
	b.WriteString(m.keyHint(d.keys))
	return b.String()
}

// promptView is a dialog as a whole screen, with the notes every editor screen ends in.
func (m *Model) promptView(d promptDialog) string {
	return m.promptText(d, true) + m.notes()
}

// promptNotes are the notes a modal shows inside itself: what is still running and the last error. Nothing
// else of the notes belongs to a dialog.
func (m *Model) promptNotes() string {
	var b strings.Builder
	if m.busy != "" {
		b.WriteString("\n" + m.wrapped(styles.hint, m.busy+" ..."))
	}
	if m.fail != "" {
		b.WriteString("\n" + m.wrapped(styles.fail, "error: "+m.fail))
	}
	return b.String()
}

// prompt is the short input dialog of the current screen, and false for a screen that is none.
func (m *Model) prompt() (promptDialog, bool) {
	switch m.screen {
	case screenSecret:
		return m.secretPrompt(), true
	case screenVaultOffer:
		return m.vaultOfferPrompt(), true
	case screenAdminAuth:
		return m.adminAuthPrompt(), true
	case screenVaultUnlock:
		return m.vaultUnlockPrompt(), true
	case screenPaths:
		if m.pathEdit >= 0 {
			return m.pathPrompt(), true
		}
	case screenTransfer:
		if m.imp != nil {
			return m.importPrompt()
		}
		return m.exportPrompt()
	case screenList, screenSummary:
		if m.testResultShown() {
			return m.testPrompt(), true
		}
	}
	return promptDialog{}, false
}

// testResultShown reports whether a finished connection test waits to be read and closed.
func (m *Model) testResultShown() bool {
	if m.testing || m.testClass == "" {
		return false
	}
	switch m.screen {
	case screenList:
		return m.section == sectionConnections
	case screenSummary:
		return m.wizard != nil && m.wizard.saved != ""
	}
	return false
}

// testResultKey closes the result of a connection test; every other key waits behind it.
func (m *Model) testResultKey(key tea.KeyMsg) {
	switch key.String() {
	case "enter", keyOf(actBack):
		m.testClass = ""
	}
}

// testPrompt is the result of a connection test.
func (m *Model) testPrompt() promptDialog {
	return promptDialog{
		title: "Connection test",
		input: strings.TrimLeft(m.testLine(), "\n"),
		keys:  "enter or {back} close",
	}
}
