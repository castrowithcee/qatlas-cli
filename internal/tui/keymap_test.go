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
		actEnter: "enter", actSpace: "space", actSearch: "/", actQuit: "q", actYes: "y", actNo: "n", actDiscard: "d",
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
	rebind(t, actHelp, "ctrl+g")
	rebind(t, actCommand, "ctrl+k")
	rebind(t, actSave, "f5")
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	view := m.render()
	if !strings.Contains(flatText(view), "ctrl+k cmds ctrl+g help") || strings.Contains(view, "? help") || strings.Contains(view, ": cmds") {
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
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.screen != screenHelp {
		t.Fatalf("the new help key left screen %v", m.screen)
	}
	if !strings.Contains(m.render(), "esc/ctrl+g close") {
		t.Errorf("the help footer does not follow the keymap:\n%s", m.render())
	}
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
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
	// d is both global (delete) and a confirmation answer (discard): different levels.
	if keyOf(actDelete) != keyOf(actDiscard) || actDelete == actDiscard {
		t.Error("delete and discard should share d on different levels")
	}
	applyKeys(uiprefs.Keys{Info: "f3", Save: "f4", Help: "ctrl+g", Command: "ctrl+o", Lock: "ctrl+r"})
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
	if h := keymap[actUp].Help(); h.Key != "up" || h.Desc != "Hoch" {
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
	if _, err := uiprefs.DefaultKeys().Check(uiprefs.ActionHelp, "ctrl+g"); err != nil {
		t.Errorf("a free key is refused: %v", err)
	}
	// The table itself, not a copy of the rules, decides: a key it newly gives away is refused.
	keyDefs = append(keyDefs, keyDefs[0])
	keyDefs[len(keyDefs)-1].act = "probe"
	keymap["probe"] = keymap[actInfo]
	setKeys("probe", "ctrl+o")
	t.Cleanup(func() { keyDefs = keyDefs[:len(keyDefs)-1]; delete(keymap, "probe") })
	if _, err := uiprefs.DefaultKeys().Check(uiprefs.ActionHelp, "ctrl+o"); err == nil || !strings.Contains(err.Error(), "probe") {
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

// keyLiteralDebt are the files that still switch on literal key values; the screens have not moved to the
// keymap yet. Each follow-up step removes files from this list; it never grows.
var keyLiteralDebt = []string{
	"admin.go",
	"commandline.go",
	"detail.go",
	"dropdown.go",
	"help.go",
	"inputmodal.go",
	"keybinding.go",
	"list.go",
	"listbar.go",
	"logs.go",
	"mouse.go",
	"overview.go",
	"pathinput.go",
	"paths.go",
	"payload.go",
	"providers.go",
	"rename.go",
	"renamefind.go",
	"restart.go",
	"secrets.go",
	"selftest.go",
	"setup.go",
	"target_builder.go",
	"targetpick.go",
	"targets.go",
	"template.go",
	"tokens.go",
	"transfer.go",
	"transfer_import.go",
	"tui.go",
	"update.go",
	"vaultheader.go",
}

// nonKeySwitches are files whose string cases are no keys (names, colour codes, footer groups); a file
// that also has key literals is on keyLiteralDebt as well.
var nonKeySwitches = []string{"canvas.go", "footer.go", "logs.go", "template.go", "tui.go"}

// Key values live in the keymap: outside it, only the files of the two exception lists spell one.
func TestKeyLiteralsStayInTheKeymap(t *testing.T) {
	for _, f := range keyLiteralFindings(t, "*.go") {
		t.Errorf("%s", f)
	}
}

// keyLiteralFindings lists the key literals of the files matching glob that no exception list covers.
func keyLiteralFindings(t *testing.T, glob string) []string {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" || slices.Contains(keyLiteralDebt, f) || slices.Contains(nonKeySwitches, f) {
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
