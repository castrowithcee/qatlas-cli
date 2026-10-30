package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// rebind replaces the key of an action for one test.
func rebind(t *testing.T, a action, key string) {
	t.Helper()
	old := keymap[a]
	setKeys(a, key)
	t.Cleanup(func() { keymap[a] = old })
}

func TestDefaultKeymapKeepsTheKnownKeys(t *testing.T) {
	want := map[action]string{
		actInfo: "i", actSave: "f2", actHelp: "?", actCommand: ":", actLock: "ctrl+l", actBack: "esc", actDelete: "d",
		actForceQuit: "ctrl+c", actUp: "up", actDown: "down", actLeft: "left", actRight: "right", actHome: "home",
		actEnd: "end", actPageUp: "pgup", actPageDown: "pgdown", actNext: "tab", actPrev: "shift+tab",
		actVimUp: "k", actVimDown: "j", actVimLeft: "h", actVimRight: "l",
		actTopicNext: "tab", actTopicPrev: "shift+tab",
		actEnter: "enter", actSpace: "space", actSearch: "/", actQuit: "q", actYes: "y", actNo: "n", actDiscard: "d",
		actSetup: "g", actCopy: "c", actNew: "n", actApproveAll: "A", actEdit: "e", actRename: "r", actReveal: "V",
		actSummary: "s", actFilter: "f", actRevert: "B", actSessions: "M", actLogs: "o", actDuplicate: "p",
		actTest: "t", actUpdate: "U", actWrite: "W",
		actRemoveField: "f8", actSuggestPrev: "ctrl+p", actSuggestNext: "ctrl+n", actNextKind: "ctrl+y",
		actReload: "ctrl+r", actArea: "1",
	}
	if len(keymap) != len(want) {
		t.Fatalf("keymap has %d actions, want %d", len(keymap), len(want))
	}
	for a, key := range want {
		if keyOf(a) != key {
			t.Errorf("action %q is bound to %q, want %q", a, keyOf(a), key)
		}
	}
	if !keyIs(tea.KeyPressMsg{Code: 'i', Text: "i"}, actInfo) || keyIs(tea.KeyPressMsg{}, actInfo) {
		t.Error("the info action does not match exactly its key")
	}
	if keyLabel(actSave) != "F2" || keyLabel(actHelp) != "?" {
		t.Errorf("labels = %q, %q", keyLabel(actSave), keyLabel(actHelp))
	}
}

func TestRebindingChangesFooterAndDispatchTogether(t *testing.T) {
	rebind(t, actHelp, "ctrl+x")
	rebind(t, actCommand, "ctrl+k")
	rebind(t, actSave, "f5")
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	view := m.render()
	if !strings.Contains(flatText(view), "ctrl+k cmds ctrl+x help") || strings.Contains(view, "? help") || strings.Contains(view, ": cmds") {
		t.Fatalf("the footer does not follow the keymap:\n%s", view)
	}
	press(t, m, "?")
	if m.screen == screenHelp {
		t.Fatal("the old help key still opens the help")
	}
	press(t, m, ":")
	if m.screen == screenCommand {
		t.Fatal("the old command key still opens the command line")
	}
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.screen != screenCommand {
		t.Fatalf("the new command key left screen %v", m.screen)
	}
	press(t, m, "esc")
	m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if m.screen != screenHelp {
		t.Fatalf("the new help key left screen %v", m.screen)
	}
	if !strings.Contains(m.render(), "esc/ctrl+x close") {
		t.Errorf("the help footer does not follow the keymap:\n%s", m.render())
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if m.screen == screenHelp {
		t.Fatal("the new help key does not close the help")
	}

	press(t, m, "5", "n")
	if view := m.render(); !strings.Contains(view, "F5 save") || strings.Contains(view, "F2 save") {
		t.Fatalf("the form footer does not follow the save key:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	if m.fail != "" || m.status != "" {
		t.Fatalf("the old save key still saves: fail %q status %q", m.fail, m.status)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	if m.fail == "" && m.status == "" {
		t.Fatal("the new save key does nothing")
	}
}

// Every key a footer names for a global action comes from the keymap: no screen file spells one as a
// literal in a footer text.
func TestFooterTextsNameGlobalKeysOnlyByPlaceholder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"? help", ": cmds", "F2 save", "F2 next", "F2 write", "F2 or enter", "esc back", "esc cancel", "esc close", "esc leave", "n/esc"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, `"`) {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) && strings.Contains(line, " · ") {
					t.Errorf("%s:%d spells %q in a footer text; use the key placeholder", f, i+1, b)
				}
			}
		}
	}
}

// assertNoCollisions fails when two actions of one level share a key.
func assertNoCollisions(t *testing.T) {
	t.Helper()
	seen := map[keyLevel]map[string]action{}
	for _, d := range keyDefs {
		if seen[d.level] == nil {
			seen[d.level] = map[string]action{}
		}
		for _, k := range keymap[d.act].Keys() {
			if other, ok := seen[d.level][k]; ok {
				t.Errorf("%s and %s share %q on level %s", other, d.act, k, d.level)
			}
			seen[d.level][k] = d.act
		}
	}
}

func TestNoTwoActionsOfALevelShareAKey(t *testing.T) {
	keepKeys(t)
	assertNoCollisions(t)
	// d is both delete and, in a question, discard; n is both new and, in a question, no: different levels.
	if keyOf(actDelete) != keyOf(actDiscard) || actDelete == actDiscard || keyOf(actNew) != keyOf(actNo) || actNew == actNo {
		t.Error("delete and discard, new and no should share a key on different levels")
	}
	applyKeys(uiprefs.Keys{Info: "f3", Save: "f4", Help: "ctrl+x", Command: "ctrl+l", Lock: "f6"})
	assertNoCollisions(t)
}

func TestEveryActionHasALevelAndHelpInBothLanguages(t *testing.T) {
	keepKeys(t)
	for _, d := range keyDefs {
		if _, ok := keymap[d.act]; !ok || d.level == "" {
			t.Errorf("action %q has no binding or level", d.act)
		}
		if textEN[d.help] == "" || textDE[d.help] == "" {
			t.Errorf("action %q lacks a help text in a language", d.act)
		}
	}
}

func TestKeyHelpFollowsTheLanguageAndTheKeys(t *testing.T) {
	keepKeys(t)
	t.Cleanup(func() { setLanguage(uiprefs.LanguageEnglish) })
	setLanguage(uiprefs.LanguageEnglish)
	if h := keymap[actSave].Help(); h.Key != "F2" || h.Desc != "save" {
		t.Errorf("English save help = %+v", h)
	}
	setLanguage(uiprefs.LanguageGerman)
	if h := keymap[actSave].Help(); h.Key != "F2" || h.Desc != "Speichern" {
		t.Errorf("German save help = %+v", h)
	}
	if h := keymap[actUp].Help(); h.Key != "↑" || h.Desc != "Hoch" {
		t.Errorf("German up help = %+v", h)
	}
	applyKeys(uiprefs.Keys{Save: "f4", Info: "f3"})
	if h := keymap[actSave].Help(); h.Key != "F4" || h.Desc != "Speichern" {
		t.Errorf("rebound save help = %+v", h)
	}
	if h := keymap[actInfo].Help(); h.Key != "i/F3" {
		t.Errorf("info help = %+v", h)
	}
	setLanguage(uiprefs.LanguageEnglish)
	if h := keymap[actSave].Help(); h.Desc != "save" {
		t.Errorf("back to English: %+v", h)
	}
}

// Check refuses exactly the keys the table gives to actions that cannot be rebound, whatever the other
// rules say, and accepts a free key.
func TestCheckRefusesTheKeysOfTheTable(t *testing.T) {
	keepKeys(t)
	rebindable := map[action]bool{}
	for _, a := range userActions {
		rebindable[a] = true
	}
	for _, d := range keyDefs {
		if rebindable[d.act] {
			continue
		}
		for _, k := range keymap[d.act].Keys() {
			for _, name := range uiprefs.Actions {
				// A table space is spelled "space", the rules know it as " ".
				if k == "space" {
					k = " "
				}
				if k == " " && name == uiprefs.ActionInfo {
					// Normalize trims a space to "", which the info action takes as "no second key".
					continue
				}
				if _, err := uiprefs.DefaultKeys().Check(name, k); err == nil {
					t.Errorf("Check(%s, %q) accepts the key of %s", name, k, d.act)
				}
			}
		}
	}
	if _, err := uiprefs.DefaultKeys().Check(uiprefs.ActionHelp, "ctrl+x"); err != nil {
		t.Errorf("a free key is refused: %v", err)
	}
	// The table itself, not a copy of the rules, decides: a key it newly gives away is refused.
	keyDefs = append(keyDefs, keyDefs[0])
	keyDefs[len(keyDefs)-1].act = "probe"
	keymap["probe"] = keymap[actInfo]
	setKeys("probe", "ctrl+x")
	t.Cleanup(func() { keyDefs = keyDefs[:len(keyDefs)-1]; delete(keymap, "probe") })
	if _, err := uiprefs.DefaultKeys().Check(uiprefs.ActionHelp, "ctrl+x"); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Errorf("Check does not consult the table: %v", err)
	}
	// A key of a rebindable action stays free for another one to take when it is not in the form.
	applyKeys(uiprefs.DefaultKeys())
	if owner := reservedOwner("f2"); owner != "" {
		t.Errorf("f2 is reserved by %s, but save can be rebound", owner)
	}
}

// keyLiteral finds the lines that spell a key value: a case item that is a string literal, or a comparison
// of a String() with one.
var keyLiteral = regexp.MustCompile(`^\s*case\s+(?:(?:"[^"]*"|[\w.]+\([^()]*\)|[\w.]+)\s*,\s*)*"[^"]*"\s*[,:]|\.String\(\)\s*[!=]=\s*"`)

// tableKeys are the key names the keymap owns: every default key of every action. No file but keymap.go
// spells one as a key value.
func tableKeys() []string {
	var keys []string
	for _, d := range keyDefs {
		for _, k := range d.keys {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// Key values live in the keymap: no file outside it switches on a string that could be a key value.
func TestKeyLiteralsStayInTheKeymap(t *testing.T) {
	for _, f := range keyLiteralFindings(t, "*.go") {
		t.Errorf("%s", f)
	}
}

// The keys of the table have no literal anywhere: a case item or a comparison that spells one fails.
func TestTableKeysAreNeverLiterals(t *testing.T) {
	for _, f := range tableKeyFindings(t, "*.go") {
		t.Errorf("%s", f)
	}
}

// tableKeyFindings lists the lines of the files matching glob that spell a key of the table as a literal.
func tableKeyFindings(t *testing.T, glob string) []string {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	type literal struct {
		key string
		re  *regexp.Regexp
	}
	var literals []literal
	for _, k := range tableKeys() {
		q := regexp.QuoteMeta(`"` + k + `"`)
		item := `^\s*case\s+(?:(?:"[^"]*"|[\w.]+\([^()]*\)|[\w.]+)\s*,\s*)*` + q + `\s*[,:]`
		literals = append(literals, literal{k, regexp.MustCompile(item + `|[!=]=\s*` + q + `|` + q + `\s*[!=]=`)})
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, l := range literals {
				if l.re.MatchString(line) {
					found = append(found, f+":"+strconv.Itoa(i+1)+" spells the table key "+l.key+"; use the keymap: "+strings.TrimSpace(line))
				}
			}
		}
	}
	return found
}

// keyLiteralFindings lists the key literals of the files matching glob.
func keyLiteralFindings(t *testing.T, glob string) []string {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if keyLiteral.MatchString(line) {
				found = append(found, f+":"+strconv.Itoa(i+1)+" spells a key value; use the keymap: "+strings.TrimSpace(line))
			}
		}
	}
	return found
}

// The vim letters move only where nothing is typed: the movement bindings do not match them, and moveKey
// leaves them alone on a screen that reads text.
func TestVimLettersMoveOnlyWhereNothingIsTyped(t *testing.T) {
	k := tea.KeyPressMsg{Code: 'k', Text: "k"}
	if keyIs(k, actUp) {
		t.Fatal("k matches the movement binding up")
	}
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.screen = screenCommand
	if !m.typing() {
		t.Fatal("the command line does not read text")
	}
	if got := m.moveKey(k); keyIs(got, actUp) || got.String() != "k" {
		t.Errorf("k became %q on a text screen", got.String())
	}
	m.screen = screenList
	if m.typing() {
		t.Fatal("the list reads text")
	}
	if got := m.moveKey(k); !keyIs(got, actUp) || got.String() != "up" {
		t.Errorf("k became %q on a screen without text, want up", got.String())
	}
}

// No default key lies on a key that tmux, GNU screen, zellij, or the VS Code terminal take. The control keys
// of text lines that sit beside an arrow key are the allowed second way.
func TestNoDefaultKeyLiesOnAMultiplexerKey(t *testing.T) {
	keepKeys(t)
	taken := map[string]bool{
		"ctrl+s": true, "ctrl+q": true, "ctrl+z": true, "ctrl+b": true, "ctrl+a": true, "ctrl+g": true,
		"ctrl+o": true, "ctrl+t": true, "ctrl+f": true, "ctrl+k": true,
		"alt+f": true, "alt+n": true, "alt+i": true, "alt+o": true, "alt+p": true, "alt+h": true,
		"alt+j": true, "alt+k": true, "alt+l": true, "alt+left": true, "alt+right": true, "alt+up": true,
		"alt+down": true, "f1": true, "f5": true, "f10": true, "f11": true, "f12": true,
		"ctrl+i": true, "ctrl+m": true, "ctrl+h": true,
	}
	secondWay := map[action]bool{actSuggestPrev: true, actSuggestNext: true}
	for _, d := range keyDefs {
		for _, k := range d.keys {
			if (taken[k] || strings.HasPrefix(k, "alt+") && len(k) == 5 && k[4] >= '0' && k[4] <= '9') && !secondWay[d.act] {
				t.Errorf("%s is bound to %s but a multiplexer or terminal takes it", k, d.act)
			}
		}
	}
}

// The shortcut column of the command line shows the binding in force, also after a rebinding, and the
// descriptions name no key.
func TestPaletteShortcutsFollowTheKeymap(t *testing.T) {
	rebind(t, actQuit, "x")
	rebind(t, actSetup, "ctrl+x")
	m := paletteModel(t, 150, 42)
	m.prefs.Icons = uiprefs.IconsNone
	view := ansi.Strip(m.render())
	for _, name := range []string{"Start the guided setup", "Open Connections"} {
		line := lineWith(view, name)
		if line == "" {
			t.Fatalf("palette lacks %q:\n%s", name, view)
		}
		if strings.Contains(line, "(") {
			t.Errorf("the description names a key: %q", line)
		}
	}
	if line := lineWith(view, "Start the guided setup"); !shortcutIs(line, "ctrl+x") {
		t.Errorf("the setup shortcut is not the bound key: %q", line)
	}
	if line := lineWith(view, "Open Connections"); !shortcutIs(line, "2") {
		t.Errorf("the area shortcut is not its digit: %q", line)
	}
	typeText(t, m, "quit")
	if line := lineWith(ansi.Strip(m.render()), "Leave the editor"); !shortcutIs(line, "x") || strings.Contains(line, "(") {
		t.Errorf("the quit row = %q", line)
	}
	if !strings.Contains(view, ": search or run a command…") {
		t.Errorf("the input line does not start with the colon:\n%s", view)
	}
}

func lineWith(view, text string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}

// A footer names every fixed key through the table: a rebound key, an arrow and a digit range show as bound.
func TestFooterNamesFixedKeysFromTheTable(t *testing.T) {
	rebind(t, actQuit, "x")
	rebind(t, actNew, "N")
	rebind(t, actUp, "ctrl+u")
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.screen, m.section, m.navSub = screenNav, sectionOverview, false
	foot := flatText(m.render())
	for _, want := range []string{"x quit", "1-6 open"} {
		if !strings.Contains(foot, want) {
			t.Errorf("the footer lacks %q:\n%s", want, foot)
		}
	}
	if strings.Contains(foot, "q quit") {
		t.Errorf("the footer still names the old quit key:\n%s", foot)
	}
	if got := keyLabel(actUp) + "/" + keyLabel(actDown); got != "ctrl+u/↓" {
		t.Errorf("label = %q", got)
	}
	if got := footerKeys("{up}/{down} move · {area} open · {pageup}/{pagedown} page"); got != "ctrl+u/↓ move · 1-6 open · pgup/pgdown page" {
		t.Errorf("footerKeys = %q", got)
	}
	if got := keyLabel(actLeft) + keyLabel(actRight); got != "←→" {
		t.Errorf("arrows = %q", got)
	}
}

// The header names help and the command line through the binding in force.
func TestHeaderNamesTheBoundCommandKey(t *testing.T) {
	rebind(t, actCommand, "ctrl+k")
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if head := ansi.Strip(m.headerHints()); !strings.Contains(head, "[ctrl+k] cmd") || strings.Contains(head, "[:]") {
		t.Errorf("header = %q", head)
	}
}

// Every key a click on a footer group may send is read from the same names the footer draws.
func TestFooterButtonsReadKeyNamesFromTheTable(t *testing.T) {
	for name, code := range map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "F2": tea.KeyF2, "F8": tea.KeyF8} {
		if k, ok := keyPress(name); !ok || k.Code != code {
			t.Errorf("keyPress(%q) = %v, %v", name, k, ok)
		}
	}
	for _, name := range []string{"↑", "←", "1", "up", "F13", "1-6"} {
		if _, ok := keyPress(name); ok {
			t.Errorf("keyPress(%q) is a button key", name)
		}
	}
}

// shortcutIs reports whether the shortcut column of a palette row, the last cell before the frame, is key.
func shortcutIs(line, key string) bool {
	fields := strings.Fields(line)
	return len(fields) >= 2 && fields[len(fields)-2] == key
}
