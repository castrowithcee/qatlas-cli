package tui

import (
	"slices"
	"strconv"
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
	actArea     action = "area"

	actVimUp    action = "vimup"
	actVimDown  action = "vimdown"
	actVimLeft  action = "vimleft"
	actVimRight action = "vimright"

	actTopicNext action = "topicnext"
	actTopicPrev action = "topicprev"

	actYes     action = "yes"
	actNo      action = "no"
	actDiscard action = "discard"

	actSetup      action = "setup"
	actCopy       action = "copy"
	actNew        action = "new"
	actApproveAll action = "approveall"
	actEdit       action = "edit"
	actRename     action = "rename"
	actReveal     action = "reveal"
	actSummary    action = "summary"
	actFilter     action = "filter"
	actRevert     action = "revert"
	actSessions   action = "sessions"
	actLogs       action = "logs"
	actDuplicate  action = "duplicate"
	actTest       action = "test"
	actUpdate     action = "update"
	actWrite      action = "write"

	actRemoveField action = "removefield"
	actSuggestPrev action = "suggestprev"
	actSuggestNext action = "suggestnext"
	actNextKind    action = "nextkind"
	actReload      action = "reload"
)

// keyLevel is the set of actions that can be pressed in the same place. Within one level no two actions
// share a key; across levels one key may mean different things only where the levels never meet: the
// answers of a question (y, n, d) and the keys only the help overlay knows. Every other key has one meaning
// on every screen.
type keyLevel string

const (
	levelGlobal  keyLevel = "global"
	levelLetter  keyLevel = "letter"
	levelCtrl    keyLevel = "ctrl"
	levelMove    keyLevel = "move"
	levelConfirm keyLevel = "confirm"
	levelVim     keyLevel = "vim"
	levelHelp    keyLevel = "help"
)

// keyDefs is the key table: every action with its level, default keys, and the catalog text of its help.
// Keys are the names bubbletea reports (tea.KeyPressMsg.String). The vim letters are entries of their own
// that only moveKey reads: on a screen that reads text they are text, so the movement bindings hold arrows only.
// The help level holds the keys only the help overlay knows; tab there steps through topics, not fields.
var keyDefs = []struct {
	act   action
	level keyLevel
	keys  []string
	help  textID
}{
	{actSave, levelGlobal, []string{"f2"}, tKeyHelpSave},
	{actHelp, levelGlobal, []string{"?"}, tKeyHelpHelp},
	{actCommand, levelGlobal, []string{":"}, tKeyHelpCommand},
	{actLock, levelGlobal, []string{"ctrl+l"}, tKeyHelpLock},
	{actBack, levelGlobal, []string{"esc"}, tKeyHelpBack},
	{actForceQuit, levelGlobal, []string{"ctrl+c"}, tKeyHelpForceQuit},

	// The letter actions: each letter means one thing on every screen where nothing is typed.
	{actInfo, levelLetter, []string{"i"}, tKeyHelpInfo},
	{actDelete, levelLetter, []string{"d"}, tKeyHelpDelete},
	{actQuit, levelLetter, []string{"q"}, tKeyHelpQuit},
	{actSetup, levelLetter, []string{"g"}, tKeyHelpSetup},
	{actCopy, levelLetter, []string{"c"}, tKeyHelpCopy},
	{actNew, levelLetter, []string{"n"}, tKeyHelpNew},
	{actApproveAll, levelLetter, []string{"A"}, tKeyHelpApproveAll},
	{actEdit, levelLetter, []string{"e"}, tKeyHelpEdit},
	{actRename, levelLetter, []string{"r"}, tKeyHelpRename},
	{actReveal, levelLetter, []string{"V"}, tKeyHelpReveal},
	{actSummary, levelLetter, []string{"s"}, tKeyHelpSummary},
	{actFilter, levelLetter, []string{"f"}, tKeyHelpFilter},
	{actRevert, levelLetter, []string{"B"}, tKeyHelpRevert},
	{actSessions, levelLetter, []string{"M"}, tKeyHelpSessions},
	{actLogs, levelLetter, []string{"o"}, tKeyHelpLogs},
	{actDuplicate, levelLetter, []string{"p"}, tKeyHelpDuplicate},
	{actTest, levelLetter, []string{"t"}, tKeyHelpTest},
	{actUpdate, levelLetter, []string{"U"}, tKeyHelpUpdate},
	{actWrite, levelLetter, []string{"W"}, tKeyHelpWrite},

	// The control keys of text lines and of the target picker, and F8 of the payload form. None can be rebound to a global action.
	{actRemoveField, levelCtrl, []string{"f8"}, tKeyHelpRemoveField},
	{actSuggestPrev, levelCtrl, []string{"ctrl+p"}, tKeyHelpSuggestPrev},
	{actSuggestNext, levelCtrl, []string{"ctrl+n"}, tKeyHelpSuggestNext},
	{actNextKind, levelCtrl, []string{"ctrl+y"}, tKeyHelpNextKind},
	{actReload, levelCtrl, []string{"ctrl+r"}, tKeyHelpReload},

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
	{actArea, levelMove, areaKeys(), tKeyHelpArea},

	{actVimUp, levelVim, []string{"k"}, tKeyHelpUp},
	{actVimDown, levelVim, []string{"j"}, tKeyHelpDown},
	{actVimLeft, levelVim, []string{"h"}, tKeyHelpLeft},
	{actVimRight, levelVim, []string{"l"}, tKeyHelpRight},

	{actTopicNext, levelHelp, []string{"tab"}, tKeyHelpTopicNext},
	{actTopicPrev, levelHelp, []string{"shift+tab"}, tKeyHelpTopicPrev},

	{actYes, levelConfirm, []string{"y"}, tKeyHelpYes},
	{actNo, levelConfirm, []string{"n"}, tKeyHelpNo},
	{actDiscard, levelConfirm, []string{"d"}, tKeyHelpDiscard},
}

// areaKeys are the digits that open the areas, one per area in the order of the tab line.
func areaKeys() []string {
	var keys []string
	for a := area(0); a < areaCount; a++ {
		keys = append(keys, strconv.Itoa(int(a)+1))
	}
	return keys
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

// arrowNames are the glyphs a footer draws for the arrow keys.
var arrowNames = map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→"}

// keyName is how a footer spells a key: function keys capitalized, arrows as glyphs, everything else as bound.
func keyName(k string) string {
	if len(k) >= 2 && k[0] == 'f' && strings.Trim(k[1:], "0123456789") == "" {
		return "F" + k[1:]
	}
	if glyph, ok := arrowNames[k]; ok {
		return glyph
	}
	return k
}

// keyLabel is how a footer names the keys of an action, joined by "/": function keys capitalized,
// everything else as bound, the second info key last. A run of three or more digits is written as a range.
func keyLabel(a action) string {
	keys := keymap[a].Keys()
	if len(keys) > 2 && digitRun(keys) {
		return keys[0] + "-" + keys[len(keys)-1]
	}
	var names []string
	for _, k := range keys {
		names = append(names, keyName(k))
	}
	if a == actInfo && infoExtra != "" {
		names = append(names, keyName(infoExtra))
	}
	return strings.Join(names, "/")
}

// digitRun reports whether keys are the single digits n, n+1, n+2 and so on.
func digitRun(keys []string) bool {
	for i, k := range keys {
		if len(k) != 1 || k[0] < '0' || k[0] > '9' || k[0] != keys[0][0]+byte(i) {
			return false
		}
	}
	return true
}

// keyPress is the key press a key name of the table stands for, for the keys a footer button may send.
func keyPress(name string) (tea.KeyPressMsg, bool) {
	switch name {
	case keyOf(actEnter):
		return tea.KeyPressMsg{Code: tea.KeyEnter}, true
	case keyOf(actBack):
		return tea.KeyPressMsg{Code: tea.KeyEscape}, true
	case keyOf(actNext):
		return tea.KeyPressMsg{Code: tea.KeyTab}, true
	}
	if len(name) >= 2 && (name[0] == 'F' || name[0] == 'f') {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 {
			return tea.KeyPressMsg{Code: tea.KeyF1 + rune(n-1)}, true
		}
	}
	if runes := []rune(name); len(runes) == 1 && runes[0] > ' ' && runes[0] < 0x7f && !(runes[0] >= '0' && runes[0] <= '9') {
		return tea.KeyPressMsg{Code: runes[0], Text: name}, true
	}
	return tea.KeyPressMsg{}, false
}

// footerPlaceholder is the name a footer text uses for the keys of an action, e.g. "{save} save": the action
// in braces, so every action of the table can be named and none by hand.
func footerPlaceholder(a action) string { return "{" + string(a) + "}" }

// footerKeys replaces the key placeholders of a footer text with the current key names, so a footer
// follows the keymap while a screen-local key stays a plain literal in its text.
func footerKeys(text string) string { return keyPlaceholders(footerText(text)) }

// keyPlaceholders replaces the key placeholders of an already translated footer text. Translating comes
// first because the catalog is keyed by the group with its placeholders.
func keyPlaceholders(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	for _, d := range keyDefs {
		if p := footerPlaceholder(d.act); strings.Contains(text, p) {
			text = strings.ReplaceAll(text, p, keyLabel(d.act))
		}
	}
	return text
}
