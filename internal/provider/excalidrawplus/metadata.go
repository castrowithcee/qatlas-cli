package excalidrawplus

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxListedIDs bounds an identifier list taken from a foreign answer.
const maxListedIDs = 100

// cleanValue bounds a foreign text and replaces control characters, which could otherwise forge structure in
// the output.
func cleanValue(value string) string {
	value = boundedValue(value)
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
}

// idsOf keeps at most maxListedIDs entries with a valid identifier.
func idsOf(values []string) []string {
	var out []string
	for _, value := range values {
		if len(out) == maxListedIDs {
			break
		}
		if validID(value) {
			out = append(out, value)
		}
	}
	return out
}

type emojiJSON struct {
	ID      string `json:"id"`
	Native  string `json:"native"`
	Unified string `json:"unified"`
}

// Emoji is the emoji of a collection.
type Emoji struct {
	ID      string `json:"id,omitempty"`
	Native  string `json:"native,omitempty"`
	Unified string `json:"unified,omitempty"`
}

func emojiOf(raw *emojiJSON) *Emoji {
	if raw == nil {
		return nil
	}
	emoji := Emoji{ID: cleanValue(raw.ID), Native: cleanValue(raw.Native), Unified: cleanValue(raw.Unified)}
	if emoji == (Emoji{}) {
		return nil
	}
	return &emoji
}

const emojiSchema = `{"type":"object","properties":{"id":{"type":"string"},"native":{"type":"string"},` +
	`"unified":{"type":"string"}},"additionalProperties":false}`

// number keeps an integer exactly as Excalidraw+ sent it.
type number = json.Number
