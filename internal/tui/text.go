package tui

import (
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// The editor's own texts live in a catalog of typed keys per language (text_keys.go, text_en.go,
// text_de.go). A screen that is not translated yet keeps its English literals; translating one means adding
// its keys to both catalogs and replacing the literals with tr. Form row labels and choice values stay the
// internal keys of the forms (field.label, field.choices); they are translated only where they are drawn
// (labelText, valueText), and a label or value without an entry is drawn as it is.

// textID is the key of one text of the catalog.
type textID string

// Keys that are built from a name instead of being declared as constants.
func labelID(english string) textID       { return textID("label." + english) }
func valueID(label, choice string) textID { return textID("value." + label + "." + choice) }
func footerID(group string) textID        { return textID("footer." + group) }
func termID(id string) textID             { return textID(id) }
func columnID(title string) textID        { return textID("column." + title) }
func termTitleID(id string) textID        { return textID(id + ".title") }
func wordID(english string) textID        { return textID("word." + english) }
func sectionID(name string) textID        { return textID("section." + name) }
func entryID(kind string) textID          { return textID("entry." + kind) }

var catalogs = i18n.Catalogs[textID]{i18n.English: textEN, i18n.German: textDE}

func init() {
	// The English words of a term are shared with the CLI and the manpage and stay in helptopics.
	for _, t := range helptopics.Terms() {
		textEN[termID(t.ID)] = t.Text
		textEN[termTitleID(t.ID)] = t.Title
	}
}

// uiLang is the language the editor draws in. Like the colour scheme it is process-wide: one editor runs per
// process, and the text helpers are called from places that hold no model.
var uiLang = i18n.English

// languageOf resolves the language preference to a language; system asks the device.
func languageOf(pref string) i18n.Lang {
	switch pref {
	case uiprefs.LanguageGerman:
		return i18n.German
	case uiprefs.LanguageSystem:
		return i18n.Detect()
	}
	return i18n.English
}

// setLanguage makes the preference the language of every text drawn from now on.
func setLanguage(pref string) { uiLang = languageOf(pref) }

// tr is the text of id in the current language; args are formatted into it.
func tr(id textID, args ...any) string {
	text := i18n.Lookup(catalogs, uiLang, id)
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// trOr is the text of id, or fallback when no catalog holds one.
func trOr(id textID, fallback string) string {
	if text, ok := i18n.Find(catalogs, uiLang, id); ok {
		return text
	}
	return fallback
}

// labelText is how a form row label is drawn.
func labelText(english string) string { return trOr(labelID(english), english) }

// valueText is how a choice value of the row with the given label is drawn.
func valueText(rowLabel, choice string) string {
	if rowLabel == startLabel {
		if a, ok := areaByName(choice); ok {
			return strings.ToLower(a.title())
		}
	}
	return trOr(valueID(rowLabel, choice), choice)
}

// footerText translates the groups of a key line, separated by " · ". A group without an entry stays as it
// is; the keys inside a translated group stay the keys.
func footerText(text string) string {
	if uiLang == i18n.English {
		return text
	}
	var out []string
	for _, group := range strings.Split(text, " · ") {
		// A group whose German text is empty is folded into a neighbour's and left out: the German words are
		// longer, and the key line of a 40-column terminal must still fit the lines English needs.
		if translated := trOr(footerID(group), group); translated != "" {
			out = append(out, translated)
		}
	}
	return strings.Join(out, " · ")
}

// termText is the explanation of a term, and termName its title.
func termText(t helptopics.Term) string { return trOr(termID(t.ID), t.Text) }
func termName(t helptopics.Term) string { return trOr(termTitleID(t.ID), t.Title) }

// wordText is how a word or short phrase is drawn that the editor takes from its own tables: the titles of
// a list bar, the labels of the detail pane, the words of an empty row. An unknown word is drawn as it is.
func wordText(english string) string { return trOr(wordID(english), english) }

// sectionText is the name of a form section as drawn; the sections themselves stay the English keys.
func sectionText(name string) string { return trOr(sectionID(name), name) }

// entryWord is what an entry of the given kind ("service", "default", ...) is called, in the language of the
// editor.
func entryWord(kind string) string { return trOr(entryID(kind), kind) }

// warningLine is a warning drawn under a row or a list.
func warningLine(text string) string { return tr(tWarningPrefix, text) }

// columnText is how a column title of a list is drawn.
func columnText(title string) string { return trOr(columnID(title), title) }

// kindText is the kind of a renamed entry ("service", "credential", "connection") as the sentence of the
// editor's language wants it: the German nouns are capitalised.
func kindText(kind string) string {
	if uiLang == i18n.English || kind == "" {
		return kind
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}
