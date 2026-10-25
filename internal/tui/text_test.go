package tui

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// useLanguage draws the editor in the given language until the test ends.
func useLanguage(t *testing.T, lang i18n.Lang) {
	t.Helper()
	before := uiLang
	uiLang = lang
	t.Cleanup(func() { uiLang = before })
}

// eachLanguage runs body once per language of the catalog.
func eachLanguage(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	for _, lang := range []i18n.Lang{i18n.English, i18n.German} {
		t.Run(string(lang), func(t *testing.T) {
			useLanguage(t, lang)
			body(t)
		})
	}
}

var verbPattern = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]`)

func TestCatalogsHoldTheSameKeys(t *testing.T) {
	keys := func(c map[textID]string) []string {
		var out []string
		for k := range c {
			out = append(out, string(k))
		}
		sort.Strings(out)
		return out
	}
	en, de := keys(textEN), keys(textDE)
	if strings.Join(en, "\n") != strings.Join(de, "\n") {
		inEN, inDE := map[string]bool{}, map[string]bool{}
		for _, k := range en {
			inEN[k] = true
		}
		for _, k := range de {
			inDE[k] = true
			if !inEN[k] {
				t.Errorf("key %q is German only", k)
			}
		}
		for _, k := range en {
			if !inDE[k] {
				t.Errorf("key %q lacks a German text", k)
			}
		}
	}
}

func TestCatalogsUseTheSameFormatVerbs(t *testing.T) {
	for key, english := range textEN {
		german, ok := textDE[key]
		if !ok {
			continue
		}
		if en, de := verbPattern.FindAllString(english, -1), verbPattern.FindAllString(german, -1); strings.Join(en, " ") != strings.Join(de, " ") {
			t.Errorf("%q: English verbs %v, German verbs %v", key, en, de)
		}
		if strings.TrimSpace(german) == "" && !strings.HasPrefix(string(key), "footer.") {
			t.Errorf("%q has an empty German text", key)
		}
	}
}

func TestEveryTermIsExplainedInGerman(t *testing.T) {
	for _, term := range helptopics.Terms() {
		if textDE[termID(term.ID)] == "" || textDE[termTitleID(term.ID)] == "" {
			t.Errorf("term %s has no German text or title", term.ID)
		}
		if textEN[termID(term.ID)] != term.Text {
			t.Errorf("term %s: the English catalog differs from helptopics", term.ID)
		}
	}
}

func TestEnglishIsTheDefaultAndFallback(t *testing.T) {
	if uiLang != i18n.English {
		t.Fatalf("default language = %q, want English", uiLang)
	}
	if got := tr(tAreaSettings); got != "Settings" {
		t.Errorf("English area = %q", got)
	}
	useLanguage(t, i18n.German)
	if got := tr(tAreaSettings); got != "Einstellungen" {
		t.Errorf("German area = %q", got)
	}
	if got := labelText("a label nobody translated"); got != "a label nobody translated" {
		t.Errorf("an untranslated label = %q, want it as it is", got)
	}
	if got := footerText("x unknown · q quit"); got != "x unknown · q Ende" {
		t.Errorf("footerText = %q", got)
	}
}

func TestLanguageOfPreference(t *testing.T) {
	cases := map[string]i18n.Lang{
		uiprefs.LanguageEnglish: i18n.English, uiprefs.LanguageGerman: i18n.German, "": i18n.English, "x": i18n.English,
	}
	for pref, want := range cases {
		if got := languageOf(pref); got != want {
			t.Errorf("languageOf(%q) = %q, want %q", pref, got, want)
		}
	}
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	if runtime.GOOS != "windows" {
		if got := languageOf(uiprefs.LanguageSystem); got != i18n.German {
			t.Errorf("system with LC_ALL=de_DE.UTF-8 = %q, want German", got)
		}
	}
}

// Choosing the language in General rebuilds the screen in it at once, and choosing English goes back.
func TestChoosingTheLanguageRebuildsTheScreen(t *testing.T) {
	useLanguage(t, i18n.English)
	m, _ := lockedVaultModel(t)
	sizedModel(m, 100, 30)
	openSectionByName(t, m, sectionGeneral)
	focusField(t, m, langLabel)
	if view := m.View(); !strings.Contains(view, "Settings") || !strings.Contains(view, "General settings") {
		t.Fatalf("the English screen lacks its words:\n%s", view)
	}
	press(t, m, "right") // deutsch
	pump(t, m, "f2")
	if m.fail != "" || m.screen != screenForm {
		t.Fatalf("saving: fail %q screen %v", m.fail, m.screen)
	}
	view := m.View()
	for _, want := range []string{"Einstellungen", "Allgemeine Einstellungen", "Sprache", "Gespeichert", "Übersicht"} {
		if !strings.Contains(view, want) {
			t.Errorf("the German screen lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "General settings") {
		t.Errorf("the screen still has English words:\n%s", view)
	}
	if m.prefs.Language != uiprefs.LanguageGerman {
		t.Errorf("prefs language = %q", m.prefs.Language)
	}
	// Back to English.
	focusField(t, m, langLabel)
	press(t, m, "left")
	pump(t, m, "f2")
	if view := m.View(); !strings.Contains(view, "General settings") || strings.Contains(view, "Einstellungen") {
		t.Errorf("English did not come back:\n%s", view)
	}
}

// The frame texts and the labels show in German; the labels stay the internal keys of the rows.
func TestGermanFrameTexts(t *testing.T) {
	useLanguage(t, i18n.German)
	m := overviewModel(t, unencryptedVaultModel(t))
	view := m.View()
	for _, want := range []string{"Übersicht", "Zugriff", "Bibliothek", "Einstellungen", "Tresor", "Nächste Schritte", "Ende", "Befehle", "Hilfe"} {
		if !strings.Contains(view, want) {
			t.Errorf("the German Overview lacks %q:\n%s", want, view)
		}
	}
	openSectionByName(t, m, sectionGeneral)
	if got := m.fields[0].label; got != "color scheme" {
		t.Errorf("the row label = %q, want the internal key", got)
	}
	if view := m.View(); !strings.Contains(view, "Farbschema") || !strings.Contains(view, "< auto >") {
		t.Errorf("the German General page lacks its labels:\n%s", view)
	}
}

// Every Rahmen screen fits the terminal in German as it does in English.
func TestFramesFitInGerman(t *testing.T) {
	sizes := []struct{ w, h int }{{minimumWidth, minimumHeight}, {60, 16}, {modalMinWidth, modalMinHeight}, {80, 24}, {100, 30}, {140, 40}}
	eachLanguage(t, func(t *testing.T) {
		for _, size := range sizes {
			fresh := func() *Model {
				m := overviewModel(t, unencryptedVaultModel(t))
				return sizedModel(m, size.w, size.h)
			}
			assertViewFits(t, fresh().View(), size.w, size.h)
			for _, s := range []section{sectionGeneral, sectionVault, sectionTransfer, sectionUpdate, sectionSelftest, sectionApprovals, sectionServices} {
				m := fresh()
				openSectionByName(t, m, s)
				assertViewFits(t, m.View(), size.w, size.h)
			}
			// The leave question and the lock question.
			m := fresh()
			openSectionByName(t, m, sectionGeneral)
			press(t, m, "right")
			press(t, m, "esc")
			if m.screen != screenLeave {
				t.Fatalf("%dx%d: esc = screen %v, want the leave question", size.w, size.h, m.screen)
			}
			assertViewFits(t, m.View(), size.w, size.h)
			m = fresh()
			m.openVaultLockConfirm(screenNav)
			assertViewFits(t, m.View(), size.w, size.h)
		}
	})
}

// The open palette fits every size in both languages, shows the translated descriptions under the unchanged
// names, and every description is one line at 80 columns.
func TestCommandPaletteFitsInGerman(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {60, 16}, {80, 24}, {140, 40}} {
			m := overviewModel(t, unencryptedVaultModel(t))
			sizedModel(m, size.w, size.h)
			press(t, m, ":")
			if m.screen != screenCommand {
				t.Fatalf("':' opened screen %v", m.screen)
			}
			assertViewFits(t, m.View(), size.w, size.h)
		}
		m := overviewModel(t, unencryptedVaultModel(t))
		sizedModel(m, 80, 24)
		press(t, m, ":")
		view := m.View()
		want, name := "Commands", "go overview"
		if uiLang == i18n.German {
			want = "Befehle"
		}
		if !strings.Contains(view, want) || !strings.Contains(view, name) {
			t.Errorf("the palette lacks %q or %q:\n%s", want, name, view)
		}
		_, room := m.fit("  ")
		for _, c := range m.commands() {
			if strings.HasPrefix(c.name, "selftest ") {
				continue
			}
			if w := lipgloss.Width(fmt.Sprintf("%-14s %s", c.name, c.desc)); w > room {
				t.Errorf("%q: the row is %d cells, want at most %d: %q", c.name, w, room, c.desc)
			}
		}
	})
}

// The key rows of General are drawn in German with their values, hints and the capture status, and fit
// every size.
func TestGermanKeyRows(t *testing.T) {
	useLanguage(t, i18n.German)
	m, _ := lockedVaultModel(t)
	sizedModel(m, 100, 40)
	openSectionByName(t, m, sectionGeneral)
	view := m.View()
	for _, want := range []string{"Speichertaste", "Hilfetaste", "Befehlstaste", "Sperrtaste", "Info-Taste", "F2 (Standard)", "nur i (keine zweite Taste)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the key rows lack %q:\n%s", want, view)
		}
	}
	if got := m.fields[6].label; got != "save key" {
		t.Errorf("key row label = %q, want the internal key", got)
	}
	focusField(t, m, "help key")
	view = m.View()
	if !strings.Contains(view, "öffnet die Hilfe; enter belegt neu") || !strings.Contains(view, "r Standard") {
		t.Errorf("the focused key row lacks its hint or footer:\n%s", view)
	}
	press(t, m, "enter")
	view = m.View()
	if !strings.Contains(view, "Neue Taste für Hilfe drücken") || !strings.Contains(view, "neue Taste drücken") {
		t.Errorf("the capture lacks its status or footer:\n%s", view)
	}
	press(t, m, "esc")
	if !strings.Contains(m.View(), "Taste beibehalten") {
		t.Errorf("cancelling lacks its status:\n%s", m.View())
	}
	for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {60, 16}, {80, 24}, {140, 40}} {
		sizedModel(m, size.w, size.h)
		assertViewFits(t, m.View(), size.w, size.h)
		press(t, m, "enter")
		assertViewFits(t, m.View(), size.w, size.h)
		press(t, m, "esc")
	}
}
