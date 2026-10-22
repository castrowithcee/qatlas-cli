package penpot

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxFeatures bounds the feature list of a team or a library.
const maxFeatures = 100

// Schema fragments shared by the listing tools. Every text of them is provider data.
const (
	timestampSchema = `{"type":"string"}`
	featuresSchema  = `{"type":"array","items":{"type":"string"},"maxItems":100}`
)

// label reads a string value for a field that carries a provider label rather than a name: control characters
// become spaces and the text is bounded.
func (o obj) label(key string) string {
	var value string
	if err := json.Unmarshal(o[key], &value); err != nil {
		return ""
	}
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	return bounded(value)
}

// optInteger reads a whole number that Penpot sent; anything else, including a fraction, reads as absent.
func (o obj) optInteger(key string) *int64 {
	var value int64
	if err := json.Unmarshal(o[key], &value); err != nil {
		return nil
	}
	return &value
}

// optBoolean reads a boolean that Penpot sent, so that false stays distinguishable from missing.
func (o obj) optBoolean(key string) *bool {
	var value bool
	if err := json.Unmarshal(o[key], &value); err != nil {
		return nil
	}
	return &value
}

// child reads a nested object; a missing value or another shape reads as absent.
func (o obj) child(key string) (obj, bool) {
	if len(o[key]) == 0 {
		return nil, false
	}
	return asObj(o[key])
}

// stringList reads a list of texts, at most maxFeatures of them; entries that are not texts are skipped.
func (o obj) stringList(key string) []string {
	var items []json.RawMessage
	if err := json.Unmarshal(o[key], &items); err != nil {
		return nil
	}
	var out []string
	for _, item := range items {
		if len(out) >= maxFeatures {
			break
		}
		if value := (obj{"v": item}).label("v"); value != "" {
			out = append(out, value)
		}
	}
	return out
}
