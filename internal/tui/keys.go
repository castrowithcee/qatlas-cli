package tui

import (
	"os"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// typing reports whether the current screen reads typed text right now. Letters are text there, so neither
// the vim movement keys, ? nor : mean anything else.
func (m *Model) typing() bool {
	if m.keyCapture {
		return true
	}
	switch m.screen {
	case screenCommand, screenSecret, screenVaultOffer, screenAdminAuth, screenVaultUnlock, screenPicker, screenProviders:
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
		return m.targetEdit >= 0 || m.targetAdd != nil || m.targetPick != nil || m.labelEdit
	case screenPaths:
		return m.pathEdit >= 0
	case screenTemplate:
		return m.template != nil && m.template.picking
	case screenRename:
		return m.renameTyping()
	case screenTransfer:
		return m.exportTyping() || m.importTyping()
	case screenLogs:
		return m.logs.dialog != nil || m.logs.pick != nil || m.logs.list.editing
	}
	return false
}

// helpReachable reports whether ? opens the help (and : the command line) on the current screen: wherever nothing is typed, and on
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
		if m.targetPick != nil {
			return m.targetPick.top().list.query() == ""
		}
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

// moveKey turns the vim keys into the arrow keys on a screen that reads no text, so
// every screen moves the same way and no handler needs to know the letters.
func (m *Model) moveKey(key tea.KeyPressMsg) tea.KeyPressMsg {
	if m.typing() {
		return key
	}
	for _, d := range []struct {
		act  action
		code rune
	}{{actVimLeft, tea.KeyLeft}, {actVimDown, tea.KeyDown}, {actVimUp, tea.KeyUp}, {actVimRight, tea.KeyRight}} {
		if keyIs(key, d.act) {
			return tea.KeyPressMsg{Code: d.code}
		}
	}
	return key
}

// pasteCode is the code of the key a paste travels as; no key of a keyboard has it.
const pasteCode rune = -1

// pasteKey is a paste as the key it travels as through the key handlers of the screens. It is named after the
// pasted text in brackets, so no key binding matches it, and inputMsg hands it to a text field as a paste.
func pasteKey(content string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: pasteCode, Text: "[" + content + "]"}
}

// pasted returns the text of a paste key and whether key is one.
func pasted(key tea.KeyPressMsg) (string, bool) {
	if key.Code != pasteCode || len(key.Text) < 2 {
		return "", false
	}
	return key.Text[1 : len(key.Text)-1], true
}

// inputMsg is what a text field is handed for key: a paste key as the paste it stands for, and alt with a
// character the field binds nothing to as that character, which a field has always typed.
func inputMsg(key tea.KeyPressMsg) tea.Msg {
	if content, ok := pasted(key); ok {
		return tea.PasteMsg{Content: content}
	}
	if key.Text != "" || key.Mod&^tea.ModShift != tea.ModAlt || !unicode.IsPrint(key.Code) || inputBinds(key) {
		return key
	}
	if key.Mod.Contains(tea.ModShift) {
		return textKey(string(unicode.ToUpper(key.Code)))
	}
	return textKey(string(key.Code))
}

// legacyKey names a key as the terminal library always named it where its input reader now names it
// otherwise: the shifted F1 to F8 of xterm-like terminals are F13 to F20, as a stored key binding knows them.
func legacyKey(key tea.KeyPressMsg) tea.KeyPressMsg {
	if key.Mod&^tea.ModAlt == tea.ModShift && key.Code >= tea.KeyF1 && key.Code <= tea.KeyF8 {
		key.Code += tea.KeyF13 - tea.KeyF1
		key.Mod &^= tea.ModShift
	}
	return key
}

// linuxFunctionKey is F1 to F5 of the Linux console, which sends them as "\x1b[[" and a letter from A to E; the
// input reader reports the two apart.
func linuxFunctionKey(key tea.KeyPressMsg) (tea.KeyPressMsg, bool) {
	if len(key.Text) != 1 || key.Text[0] < 'A' || key.Text[0] > 'E' {
		return key, false
	}
	return tea.KeyPressMsg{Code: tea.KeyF1 + rune(key.Text[0]-'A')}, true
}

// textKey is the key press of typed text.
func textKey(text string) tea.KeyPressMsg {
	code := tea.KeyExtended
	if runes := []rune(text); len(runes) == 1 {
		code = runes[0]
	}
	return tea.KeyPressMsg{Code: code, Text: text}
}

// keyboardModes removes what Bubble Tea writes to switch the keyboard enhancements of the terminal on, off, or
// to ask about them: modifyOtherKeys and the Kitty keyboard protocol. With them a terminal reports keys such as
// ctrl+i, shift+enter or the keypad's enter under names of their own; without them every key keeps the name
// the legacy encoding gives it, the name every key binding, stored ones included, was taken under.
var keyboardModes = strings.NewReplacer(ansi.SetModifyOtherKeys2, "", ansi.ResetModifyOtherKeys, "",
	ansi.PushKittyKeyboard(1), "", ansi.PopKittyKeyboard(1), "", ansi.RequestKittyKeyboard, "")

// legacyKeys is the terminal output of the editor without the keyboard enhancements. Bubble Tea writes each
// frame, and each set of terminal modes, in one write, so no sequence is split between two.
type legacyKeys struct{ f *os.File }

func (o legacyKeys) Write(p []byte) (int, error) {
	if _, err := o.f.WriteString(keyboardModes.Replace(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o legacyKeys) Read(p []byte) (int, error) { return o.f.Read(p) }
func (o legacyKeys) Close() error               { return o.f.Close() }
func (o legacyKeys) Fd() uintptr                { return o.f.Fd() }
