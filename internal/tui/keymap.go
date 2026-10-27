package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// action names a key-bound action of the editor. Dispatch and footers refer to the action, never to the
// key, so the binding lives in exactly one place.
type action string

const (
	actInfo      action = "info"
	actSave      action = "save"
	actHelp      action = "help"
	actCommand   action = "command"
	actLock      action = "lock"
	actBack      action = "back"
	actDelete    action = "delete"
	actForceQuit action = "forcequit"

	actUp       action = "up"
	actDown     action = "down"
	actLeft     action = "left"
	actRight    action = "right"
	actHome     action = "home"
	actEnd      action = "end"
	actPageUp   action = "pageup"
	actPageDown action = "pagedown"
	actNext     action = "next"
	actPrev     action = "prev"
	actEnter    action = "enter"
	actSpace    action = "space"
	actSearch   action = "search"
	actQuit     action = "quit"

	actVimUp    action = "vimup"
	actVimDown  action = "vimdown"
	actVimLeft  action = "vimleft"
	actVimRight action = "vimright"

	actYes     action = "yes"
	actNo      action = "no"
	actDiscard action = "discard"
)

// keyLevel is the set of actions that can be pressed in the same place. Within one level no two actions
// share a key; across levels one key may mean different things (d deletes globally and discards in a
// confirmation).
type keyLevel string

const (
	levelGlobal  keyLevel = "global"
	levelMove    keyLevel = "move"
	levelConfirm keyLevel = "confirm"
	levelVim     keyLevel = "vim"
)

// keyDefs is the key table: every action with its level, default keys, and the catalog text of its help.
// Keys are the names bubbletea reports (tea.KeyPressMsg.String). The vim letters are entries of their own
// that only moveKey reads: on a screen that reads text they are text, so the movement bindings hold arrows only.
var keyDefs = []struct {
	act   action
	level keyLevel
	keys  []string
	help  textID
}{
	{actInfo, levelGlobal, []string{"i"}, tKeyHelpInfo},
	{actSave, levelGlobal, []string{"f2"}, tKeyHelpSave},
	{actHelp, levelGlobal, []string{"?"}, tKeyHelpHelp},
	{actCommand, levelGlobal, []string{":"}, tKeyHelpCommand},
	{actLock, levelGlobal, []string{"ctrl+l"}, tKeyHelpLock},
	{actBack, levelGlobal, []string{"esc"}, tKeyHelpBack},
	{actDelete, levelGlobal, []string{"d"}, tKeyHelpDelete},
	{actForceQuit, levelGlobal, []string{"ctrl+c"}, tKeyHelpForceQuit},

	{actUp, levelMove, []string{"up"}, tKeyHelpUp},
	{actDown, levelMove, []string{"down"}, tKeyHelpDown},
	{actLeft, levelMove, []string{"left"}, tKeyHelpLeft},
	{actRight, levelMove, []string{"right"}, tKeyHelpRight},
	{actHome, levelMove, []string{"home"}, tKeyHelpHome},
	{actEnd, levelMove, []string{"end"}, tKeyHelpEnd},
	{actPageUp, levelMove, []string{"pgup"}, tKeyHelpPageUp},
	{actPageDown, levelMove, []string{"pgdown"}, tKeyHelpPageDown},
	{actNext, levelMove, []string{"tab"}, tKeyHelpNext},
	{actPrev, levelMove, []string{"shift+tab"}, tKeyHelpPrev},
	{actEnter, levelMove, []string{"enter"}, tKeyHelpEnter},
	{actSpace, levelMove, []string{"space"}, tKeyHelpSpace},
	{actSearch, levelMove, []string{"/"}, tKeyHelpSearch},
	{actQuit, levelMove, []string{"q"}, tKeyHelpQuit},

	{actVimUp, levelVim, []string{"k"}, tKeyHelpUp},
	{actVimDown, levelVim, []string{"j"}, tKeyHelpDown},
	{actVimLeft, levelVim, []string{"h"}, tKeyHelpLeft},
	{actVimRight, levelVim, []string{"l"}, tKeyHelpRight},

	{actYes, levelConfirm, []string{"y"}, tKeyHelpYes},
	{actNo, levelConfirm, []string{"n"}, tKeyHelpNo},
	{actDiscard, levelConfirm, []string{"d"}, tKeyHelpDiscard},
}

// keymap is the key table as bindings. Dispatch and footers read it alone; setKeys and refreshKeyHelp are
// the only writers.
var keymap = func() map[action]key.Binding {
	m := map[action]key.Binding{}
	for _, d := range keyDefs {
		m[d.act] = key.NewBinding(key.WithKeys(d.keys...))
	}
	return m
}()

// infoExtra is the optional second key of the info action, set in the preferences. Unlike i it is no
// letter, so it also works on text lines; "" means none.
var infoExtra string

// userActions maps the rebindable actions of the preferences to the actions of the keymap.
var userActions = map[string]action{
	uiprefs.ActionSave: actSave, uiprefs.ActionHelp: actHelp, uiprefs.ActionCommand: actCommand, uiprefs.ActionLock: actLock,
}

func init() { uiprefs.Reserved = reservedOwner }

// reservedOwner names the action of the table that owns k and that the preferences cannot rebind, so no
// binding may take it; "" means the key is free.
func reservedOwner(k string) string {
	rebindable := map[action]bool{}
	for _, a := range userActions {
		rebindable[a] = true
	}
	for _, d := range keyDefs {
		if !rebindable[d.act] && slices.Contains(keymap[d.act].Keys(), k) {
			return string(d.act)
		}
	}
	return ""
}

// setKeys replaces the keys of an action; none unbinds it.
func setKeys(a action, keys ...string) {
	b := keymap[a]
	b.SetKeys(slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return k == "" })...)
	keymap[a] = b
}

// applyKeys makes the keys of the preferences the keymap. Dispatch and footers read the keymap alone, so
// both follow at once.
func applyKeys(k uiprefs.Keys) {
	k = k.WithDefaults()
	for name, a := range userActions {
		setKeys(a, k.Get(name))
	}
	infoExtra = k.Info
	refreshKeyHelp()
}

// refreshKeyHelp sets the help of every binding from the catalog in the current language and the keys in
// force. It runs when either changes, never at package start, so no text is fixed in a language early.
func refreshKeyHelp() {
	for _, d := range keyDefs {
		b := keymap[d.act]
		b.SetHelp(keyLabel(d.act), tr(d.help))
		keymap[d.act] = b
	}
}

// keyOf is the first key bound to an action, "" when unbound. A switch can take it as a case, so one
// screen's key switch stays a switch.
func keyOf(a action) string {
	if keys := keymap[a].Keys(); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// keyIs reports whether the key press triggers the action. An unbound action never matches.
func keyIs(msg tea.KeyPressMsg, a action) bool { return key.Matches(msg, keymap[a]) }

// keyName is how a footer spells a key: function keys capitalized, everything else as bound.
func keyName(k string) string {
	if len(k) >= 2 && k[0] == 'f' && strings.Trim(k[1:], "0123456789") == "" {
		return "F" + k[1:]
	}
	return k
}

// keyLabel is how a footer names the keys of an action, joined by "/": function keys capitalized,
// everything else as bound, the second info key last.
func keyLabel(a action) string {
	var names []string
	for _, k := range keymap[a].Keys() {
		names = append(names, keyName(k))
	}
	if a == actInfo && infoExtra != "" {
		names = append(names, keyName(infoExtra))
	}
	return strings.Join(names, "/")
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
func footerKeys(text string) string { return keyPlaceholders(footerText(text)) }

// keyPlaceholders replaces the key placeholders of an already translated footer text. Translating comes
// first because the catalog is keyed by the group with its placeholders.
func keyPlaceholders(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	for _, p := range footerPlaceholders {
		text = strings.ReplaceAll(text, p.name, keyLabel(p.act))
	}
	return text
}
