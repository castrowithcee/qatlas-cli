package tui

import tea "github.com/charmbracelet/bubbletea"

// typing reports whether the current screen reads typed text right now. Letters are text there, so neither
// the vim movement keys nor ? mean anything else.
func (m *Model) typing() bool {
	switch m.screen {
	case screenSecret, screenVaultOffer, screenAdminAuth, screenVaultUnlock, screenPicker, screenProviders:
		return true
	case screenList:
		return m.list.editing
	case screenForm:
		if m.payloadRemoveAsk || m.payloadSaving || m.focus >= len(m.fields) {
			return false
		}
		switch f := m.fields[m.focus]; f.kind {
		case fieldText, fieldEnvName, fieldMasked, fieldPayloadNew:
			return !f.readOnly
		}
	case screenTargets:
		return m.targetEdit >= 0 || m.targetAdd != nil
	case screenPaths:
		return m.pathEdit >= 0
	case screenTemplate:
		return m.template != nil && m.template.picking
	case screenLogs:
		return m.logs.dialog != nil || m.logs.pick != nil || m.logs.list.editing
	}
	return false
}

// helpReachable reports whether ? opens the help on the current screen: wherever nothing is typed, and on
// the screens whose search line is always there, while that line is empty. A form keeps its letters for
// its text rows, so only the question inside it is covered.
func (m *Model) helpReachable() bool {
	switch m.screen {
	case screenHelp:
		return false
	case screenForm:
		return m.payloadRemoveAsk
	case screenProviders:
		return m.providers.list.query() == ""
	case screenTargets:
		if m.targetAdd != nil {
			return m.targetAdd.choices.query() == ""
		}
	case screenTemplate:
		if m.template != nil && m.template.picking {
			return m.templateList.query() == ""
		}
		return true
	}
	return !m.typing()
}

// moveKey turns the vim movement keys into the arrow keys on a screen that reads no text, so every screen
// moves the same way and no handler needs to know the letters.
func (m *Model) moveKey(key tea.KeyMsg) tea.KeyMsg {
	if key.Type != tea.KeyRunes || key.Alt || key.Paste || len(key.Runes) != 1 || m.typing() {
		return key
	}
	switch key.Runes[0] {
	case 'h':
		return tea.KeyMsg{Type: tea.KeyLeft}
	case 'j':
		return tea.KeyMsg{Type: tea.KeyDown}
	case 'k':
		return tea.KeyMsg{Type: tea.KeyUp}
	case 'l':
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return key
}
