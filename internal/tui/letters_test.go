package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// answerLevels are the levels in which one key may mean something else: a question's answers and the keys only
// the help overlay knows. Everywhere else a key has exactly one meaning.
var answerLevels = []keyLevel{levelConfirm, levelHelp}

// Outside the answers of a question no key, letter or other, has two meanings, whatever level its actions sit in.
func TestNoKeyHasTwoMeaningsOutsideTheAnswerLevels(t *testing.T) {
	keepKeys(t)
	owner := map[string]action{}
	for _, d := range keyDefs {
		if slices.Contains(answerLevels, d.level) {
			continue
		}
		for _, k := range keymap[d.act].Keys() {
			if other, ok := owner[k]; ok {
				t.Errorf("%q means both %s and %s", k, other, d.act)
			}
			owner[k] = d.act
		}
	}
	// The answers reuse letters of actions on purpose: they are a level of their own.
	for _, a := range []action{actYes, actNo, actDiscard} {
		found := false
		for _, d := range keyDefs {
			found = found || (d.act == a && d.level == levelConfirm)
		}
		if !found {
			t.Errorf("%s is not an answer of a question", a)
		}
	}
}

// Every letter of the table is a single lower-case letter or a named key, and the letters of the actions are
// the ones the contract names.
func TestLetterActionsAreTheAgreedLetters(t *testing.T) {
	want := map[string]action{
		"g": actSetup, "c": actCopy, "n": actNew, "a": actApproveAll, "e": actEdit, "r": actRename, "d": actDelete,
		"v": actReveal, "s": actSummary, "f": actFilter, "b": actRevert, "i": actInfo, "m": actSessions, "o": actLogs,
		"p": actDuplicate, "q": actQuit, "t": actTest, "u": actUpdate, "w": actWrite,
	}
	got := map[string]action{}
	for _, d := range keyDefs {
		if d.level == levelLetter {
			got[d.keys[0]] = d.act
		}
	}
	for k, a := range want {
		if got[k] != a {
			t.Errorf("letter %q is %q, want %q", k, got[k], a)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the letter level has %d actions, want %d: %v", len(got), len(want), got)
	}
}

// helpWording is how the help names each letter of the table: the letter followed by what it does. A letter the
// table gains fails here until the help and this list both name it. c is reserved for copying, which does
// nothing yet, so the help leaves it out.
var helpWording = map[string]string{
	"g": "g starts", "n": "n creates", "e": "e edits", "r": "r renames", "d": "d deletes", "p": "p duplicates",
	"t": "t tests", "u": "u installs", "a": "a approves", "v": "v shows or hides", "s": "s shows", "f": "f their filter",
	"m": "m their MCP sessions", "o": "o opens", "w": "w saves", "b": "b puts", "i": "i opens", "q": "q quits",
	"y": "y confirms", "h": "h, j, k, and l", "j": "h, j, k, and l", "k": "h, j, k, and l", "l": "h, j, k, and l",
}

var helpReserved = []action{actCopy}

func TestHelpNamesEveryLetterOfTheTable(t *testing.T) {
	var text strings.Builder
	for _, topic := range helptopics.All() {
		text.WriteString(topic.Text + "\n")
	}
	help := text.String()
	isLetter := regexp.MustCompile(`^[a-z]$`)
	for _, d := range keyDefs {
		if slices.Contains(helpReserved, d.act) {
			if strings.Contains(help, "c copies") || strings.Contains(help, "c is reserved") {
				t.Errorf("the help names %s, which does nothing yet", d.act)
			}
			continue
		}
		for _, k := range d.keys {
			if !isLetter.MatchString(k) {
				continue
			}
			phrase, ok := helpWording[k]
			switch {
			case !ok:
				t.Errorf("letter %q of %s has no wording in helpWording", k, d.act)
			case !strings.Contains(help, phrase):
				t.Errorf("the help does not name %q (%s) as %q", k, d.act, phrase)
			}
		}
	}
}

// Letters the old layout gave other meanings do nothing of the sort now: the tests of the screens use the new
// ones, and these stay silent.
func TestOldLettersMeanNothingAnymore(t *testing.T) {
	m := sizedModel(groupModel(t), 140, 40)
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "c")
	if m.wizard != nil || m.screen != screenList {
		t.Errorf("c = screen %v wizard %v, want nothing: it is reserved", m.screen, m.wizard)
	}
	press(t, m, "s")
	if m.screen != screenList {
		t.Errorf("s in Credentials = screen %v, want the list", m.screen)
	}
	press(t, m, "f")
	if m.screen != screenList {
		t.Errorf("f in Credentials = screen %v, want the list", m.screen)
	}
	press(t, m, "g")
	if m.screen != screenTemplate || m.template == nil || !m.template.setup {
		t.Errorf("g = screen %v, want the question that starts the guided setup", m.screen)
	}
}

// n in Credentials asks for the kind first, and each answer leads to its own form.
func TestNewInCredentialsOffersBothKinds(t *testing.T) {
	for _, tt := range []struct {
		name  string
		keys  []string
		bound func(m *Model) bool
	}{
		{"credential", []string{"enter"}, func(m *Model) bool {
			return m.field(storageLabel) != nil && !slices.ContainsFunc(m.fields, func(f field) bool { return f.kind == fieldPayloadNew })
		}},
		{"payload secret", []string{"down", "enter"}, func(m *Model) bool {
			return slices.ContainsFunc(m.fields, func(f field) bool { return f.kind == fieldPayloadNew })
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _, _ := newStoreModel(t)
			sizedModel(m, 100, 30)
			openSectionByName(t, m, sectionCredentials)
			press(t, m, "n")
			if m.screen != screenTemplate || m.template == nil || !m.template.kind {
				t.Fatalf("n = screen %v, want the kind question", m.screen)
			}
			view := screenOf(m)
			for _, want := range []string{"New credential or payload secret", "credential", "payload secret"} {
				if !strings.Contains(view, want) {
					t.Errorf("the kind question does not show %q:\n%s", want, view)
				}
			}
			press(t, m, tt.keys...)
			if m.screen != screenForm || !tt.bound(m) {
				t.Errorf("the answer %q = screen %v, want its form:\n%s", tt.name, m.screen, screenOf(m))
			}
		})
	}
}

// A click on a row of the kind question chooses it, like enter on that row.
func TestClickOnTheKindQuestionChoosesIt(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	sizedModel(m, 100, 30)
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	clickOn(t, m, "( ) payload secret")
	if m.screen != screenForm || !slices.ContainsFunc(m.fields, func(f field) bool { return f.kind == fieldPayloadNew }) {
		t.Fatalf("a click on payload secret = screen %v, want the payload form:\n%s", m.screen, screenOf(m))
	}
}

// With credentials to copy the kind question is followed by the template question, for a credential only.
func TestKindCredentialThenAsksForATemplate(t *testing.T) {
	m, _, _, _, _ := newStoreModel(t)
	addKeyringCredential(t, m, "reader")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n", "enter")
	if m.screen != screenTemplate || m.template == nil || m.template.kind {
		t.Fatalf("kind credential = screen %v, want the template question", m.screen)
	}
	if !strings.Contains(screenOf(m), "from existing") {
		t.Errorf("the template question is missing:\n%s", screenOf(m))
	}
}

// The hint line about renames found is a stop below the last row: enter on it and a click both open them.
func TestRenameHintLineOpensWithEnterAndClick(t *testing.T) {
	for _, tt := range []struct {
		name string
		open func(t *testing.T, m *Model)
	}{
		{"enter", func(t *testing.T, m *Model) { openFindingsLine(t, m) }},
		{"click", func(t *testing.T, m *Model) { clickOn(t, m, "this line (enter)") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newFindScene(t)
			s.renameByHand(t, "connection", "personal", "main")
			m := s.open(t)
			openSectionByName(t, m, sectionConnections)
			wantInView(t, m, "Connections", "this line (enter)")
			tt.open(t, m)
			if m.screen != screenFindings {
				t.Fatalf("the hint line = screen %v, want the findings:\n%s", m.screen, screenOf(m))
			}
		})
	}
}

// The focus on the hint line takes no row action, and up or esc return to the rows.
func TestRenameHintLineTakesNoRowAction(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "end", "down")
	if !m.noticeFocused() {
		t.Fatal("down from the last row did not reach the hint line")
	}
	press(t, m, "d", "r", "e", "t", "p")
	if m.screen != screenList || !m.noticeFocused() {
		t.Fatalf("a row action reached past the hint line: screen %v", m.screen)
	}
	press(t, m, "up")
	if m.noticeFocused() {
		t.Error("up did not return to the rows")
	}
	press(t, m, "down", "esc")
	if m.noticeFocused() || m.screen != screenList {
		t.Errorf("esc = screen %v, focus %v, want the rows", m.screen, m.noticeFocused())
	}
}

// ctrl+t and ctrl+r belong to the target picker: no binding may take them, whatever the rules of the file say.
func TestPickerControlKeysAreNoBindings(t *testing.T) {
	keepKeys(t)
	for _, k := range []string{"ctrl+t", "ctrl+r", "ctrl+d", "ctrl+p", "ctrl+n"} {
		for _, name := range uiprefs.Actions {
			if _, err := uiprefs.DefaultKeys().Check(name, k); err == nil {
				t.Errorf("Check(%s, %s) accepts a key of a screen", name, k)
			}
		}
	}
	if _, err := uiprefs.DefaultKeys().Check(uiprefs.ActionLock, "ctrl+r"); err == nil || !strings.Contains(err.Error(), string(actReload)) {
		t.Errorf("ctrl+r is refused without naming the picker's action: %v", err)
	}
}

// Reloading the listing of the picker with ctrl+r works, and no global key can hide it.
func TestCtrlRReloadsThePickerListing(t *testing.T) {
	keepKeys(t)
	src := &listSource{list: func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{group("a")}}, nil
	}}
	m := pickerModel(t, src)
	openPicker(t, m)
	before := len(src.reqs)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	deliver(m, cmd)
	if len(src.reqs) != before+1 {
		t.Errorf("ctrl+r made %d reads, want one more than %d", len(src.reqs), before)
	}
	// Every key that can still be bound is a free one, so none of them is ctrl+r or ctrl+t.
	applyKeys(uiprefs.Keys{Help: "ctrl+g", Command: "ctrl+o", Lock: "ctrl+x"})
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	deliver(m, cmd)
	if len(src.reqs) != before+2 {
		t.Errorf("ctrl+r with other bindings made %d reads, want %d", len(src.reqs), before+2)
	}
}

// The footers of the screens whose keys changed fit the smallest terminal in both languages: the answer keys of
// the question and the help stay on the line.
func TestChangedFootersFitTheSmallestTerminalInBothLanguages(t *testing.T) {
	t.Cleanup(func() { setLanguage(uiprefs.LanguageEnglish) })
	for _, lang := range []string{uiprefs.LanguageEnglish, uiprefs.LanguageGerman} {
		setLanguage(lang)
		screens := map[string]func() *Model{
			"kind question": func() *Model {
				m, _, _, _, _ := newStoreModel(t)
				sizedModel(m, minimumWidth, minimumHeight)
				openSectionByName(t, m, sectionCredentials)
				press(t, m, "n")
				return m
			},
			"credentials list": func() *Model {
				m, _, _, _, _ := newStoreModel(t)
				sizedModel(m, minimumWidth, minimumHeight)
				openSectionByName(t, m, sectionCredentials)
				return m
			},
			"hint line": func() *Model {
				s := newFindScene(t)
				s.renameByHand(t, "connection", "personal", "main")
				m := s.open(t)
				sizedModel(m, minimumWidth, minimumHeight)
				openSectionByName(t, m, sectionConnections)
				press(t, m, "end", "down")
				return m
			},
			"key row": func() *Model {
				m, _ := lockedVaultModel(t)
				sizedModel(m, minimumWidth, minimumHeight)
				openSectionByName(t, m, sectionGeneral)
				focusKeyRow(t, m, "save key")
				return m
			},
			"target list": func() *Model {
				m, _, _ := knownTargetsModel(t)
				sizedModel(m, minimumWidth, minimumHeight)
				openConnection(t, m, "gh-a")
				openTargetList(t, m)
				return m
			},
		}
		for name, build := range screens {
			m := build()
			view := m.render()
			assertViewFits(t, view, minimumWidth, minimumHeight)
			lines := strings.Split(view, "\n")
			footer := lines[len(lines)-1]
			// The key row has no room for more than its own answer at this width.
			want := keyLabel(actHelp)
			if name == "key row" {
				want = keyOf(actEnter)
			}
			if !strings.Contains(footer, want) {
				t.Errorf("%s (%s): the footer lost %q: %q", name, lang, want, footer)
			}
		}
	}
}

// A footer group that names a letter names one the table binds, so no footer teaches a key that does nothing.
func TestFooterGroupsNameOnlyBoundLetters(t *testing.T) {
	bound := map[string]bool{}
	for _, d := range keyDefs {
		for _, k := range d.keys {
			bound[k] = true
		}
	}
	for id := range textEN {
		group, ok := strings.CutPrefix(string(id), "footer.")
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(group, " ")
		for _, k := range strings.Split(key, "/") {
			if isSingleLetter(k) && !bound[k] {
				t.Errorf("footer %q names the letter %q, which the table does not bind", group, k)
			}
		}
	}
}

func isSingleLetter(k string) bool { return len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' }
