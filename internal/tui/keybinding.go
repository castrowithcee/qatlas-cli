package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// keyRows are the key rows of the General form: the label of a row, the action it binds, its one-line hint,
// and the explanation behind it.
var keyRows = []struct {
	label, action string
	hint, detail  textID
}{
	{"save key", uiprefs.ActionSave, tKeySaveHint, tKeySaveDetail},
	{"help key", uiprefs.ActionHelp, tKeyHelpHint, tKeyHelpDetail},
	{"command key", uiprefs.ActionCommand, tKeyCommandHint, tKeyCommandDetail},
	{"lock key", uiprefs.ActionLock, tKeyLockHint, tKeyLockDetail},
	{"info key", uiprefs.ActionInfo, tKeyInfoHint, tKeyInfoDetail},
}

// keyFields are the key rows of the General form for keys.
func keyFields(keys uiprefs.Keys) []field {
	keys = keys.WithDefaults()
	var fields []field
	for _, r := range keyRows {
		fields = append(fields, field{label: r.label, kind: fieldKey, action: r.action, bound: keys.Get(r.action),
			detail: tr(r.detail) + tr(tKeyDetailSuffix)}.withHint(tr(r.hint)))
	}
	return fields
}

// keysOfForm reads the keys the key rows of the form hold.
func (m *Model) keysOfForm() uiprefs.Keys {
	var keys uiprefs.Keys
	for _, f := range m.fields {
		if f.kind == fieldKey {
			keys = keys.With(f.action, f.bound)
		}
	}
	return keys
}

// keyRowValue is how a key row shows its binding: the key by name, and where the key is the standard or
// none, said in words, so colour carries nothing.
func keyRowValue(f field) string {
	standard := uiprefs.DefaultKeys().Get(f.action)
	switch {
	case f.action == uiprefs.ActionInfo && f.bound == "":
		return tr(tKeyInfoOnly)
	case f.action == uiprefs.ActionInfo:
		return tr(tKeyInfoAnd, keyName(f.bound))
	case f.bound == standard:
		return tr(tKeyStandard, keyName(f.bound))
	}
	return keyName(f.bound)
}

// keyRowKey handles the keys of a focused key row: enter waits for the new key, r restores the standard.
func (m *Model) keyRowKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	f := &m.fields[m.focus]
	switch key.String() {
	case "enter":
		m.clearMessages()
		m.keyCapture = true
		m.status = tr(tKeyPressStatus, trOr(textID("keyaction."+f.action), strings.TrimSuffix(f.label, " key")))
		return nil, true
	case "r":
		m.clearMessages()
		f.bound = uiprefs.DefaultKeys().Get(f.action)
		return nil, true
	}
	return nil, false
}

// captureKey takes the next key press as the binding of the focused key row. It runs ahead of every global
// key, so ?, :, F2, and ctrl+l can be bound too; only a rule keeps a key from being taken, and the refusal
// names it.
func (m *Model) captureKey(key tea.KeyPressMsg) tea.Cmd {
	if _, ok := pasted(key); ok {
		return nil
	}
	m.keyCapture = false
	m.clearMessages()
	if key.String() == keyOf(actBack) {
		m.status = tr(tKeyKept)
		return nil
	}
	f := &m.fields[m.focus]
	// The other rows of the form are what the key must not collide with, so a rebinding in several steps
	// checks against what is shown, not against what was saved.
	name := key.String()
	if name == "space" {
		// The rules name the space bar by its character, as bindings always have been.
		name = " "
	}
	chosen, err := m.keysOfForm().Check(f.action, name)
	if err != nil {
		m.fail = err.Error()
		return nil
	}
	f.bound = chosen
	return nil
}
