// Package i18n holds the language choice of the editor and the lookup of its texts. It is a leaf: it
// imports nothing of this module, so any surface can use it.
//
// A catalog maps typed keys to texts per language. English is the complete catalog; a key that another
// language lacks falls back to English, so a screen that is not translated yet keeps working.
package i18n

import "strings"

// Lang is a language of the editor's own texts.
type Lang string

// The supported languages. English is the default and the fallback.
const (
	English Lang = "en"
	German  Lang = "de"
)

// Parse reads a language from a locale name such as "de", "de_DE.UTF-8" or "en-US". It reports false for
// a name that is no supported language, including "C", "POSIX" and the empty string.
func Parse(name string) (Lang, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexAny(name, ".@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.IndexAny(name, "_-"); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "en":
		return English, true
	case "de":
		return German, true
	}
	return English, false
}

// Detect returns the language of the system, or English when it names none that is supported.
func Detect() Lang { return detect() }

// detectFromEnv picks the language of the first locale variable that is set and not empty, in the order
// LC_ALL, LC_MESSAGES, LANG, the order the C library applies. A variable that is set to a language this
// package does not support ends the search with English: a later variable does not override it.
func detectFromEnv(getenv func(string) string) Lang {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			lang, _ := Parse(v)
			return lang
		}
	}
	return English
}

// Catalogs holds the texts of every language by key.
type Catalogs[K ~string] map[Lang]map[K]string

// Lookup returns the text of key in lang, or its English text, or the key itself when neither exists.
func Lookup[K ~string](catalogs Catalogs[K], lang Lang, key K) string {
	if text, ok := catalogs[lang][key]; ok {
		return text
	}
	if text, ok := catalogs[English][key]; ok {
		return text
	}
	return string(key)
}

// Find is Lookup without the key as the last resort: it reports whether a text exists in lang or in English.
func Find[K ~string](catalogs Catalogs[K], lang Lang, key K) (string, bool) {
	if text, ok := catalogs[lang][key]; ok {
		return text, true
	}
	text, ok := catalogs[English][key]
	return text, ok
}
