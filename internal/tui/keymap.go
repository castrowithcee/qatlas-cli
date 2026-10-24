package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// action names a key-bound action of the editor. Dispatch and footers refer to the action, never to the
// key, so the binding lives in exactly one place.
type action string

const (
	actInfo    action = "info"
	actSave    action = "save"
	actHelp    action = "help"
	actCommand action = "command"
	actLock    action = "lock"
	actBack    action = "back"
	actDelete  action = "delete"
)

// keymap maps each action to the key string bubbletea reports for it (tea.KeyMsg.String); "" is unbound.
var keymap = map[action]string{
	actInfo:    "i",
	actSave:    "f2",
	actHelp:    "?",
	actCommand: ":",
	actLock:    "ctrl+l",
	actBack:    "esc",
	actDelete:  "d",
}

// keyOf is the key bound to an action. A switch can take it as a case, so one screen's key switch stays
// a switch.
func keyOf(a action) string { return keymap[a] }

// keyIs reports whether the key press triggers the action. An unbound action never matches.
func keyIs(key tea.KeyMsg, a action) bool {
	k := keymap[a]
	return k != "" && key.String() == k
}

// keyLabel is how a footer names the key of an action: function keys capitalized, everything else as bound.
func keyLabel(a action) string {
	k := keymap[a]
	if len(k) >= 2 && k[0] == 'f' && strings.Trim(k[1:], "0123456789") == "" {
		return "F" + k[1:]
	}
	return k
}

// footerPlaceholders are the names a footer text may use for a bound key, e.g. "{save} save".
var footerPlaceholders = []struct {
	name string
	act  action
}{
	{"{save}", actSave}, {"{help}", actHelp}, {"{info}", actInfo}, {"{command}", actCommand},
	{"{lock}", actLock}, {"{back}", actBack}, {"{delete}", actDelete},
}

// footerKeys replaces the key placeholders of a footer text with the current key names, so a footer
// follows the keymap while a screen-local key stays a plain literal in its text.
func footerKeys(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	for _, p := range footerPlaceholders {
		text = strings.ReplaceAll(text, p.name, keyLabel(p.act))
	}
	return text
}
