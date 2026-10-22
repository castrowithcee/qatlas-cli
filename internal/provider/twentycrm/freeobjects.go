package twentycrm

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// addressInText finds scheme-qualified addresses inside a text.
var addressInText = regexp.MustCompile(`(?i)[a-z][a-z0-9+.\-]*://[^\s"'<>]*`)

// boundFreeObject decodes a free-form JSON value of a widget for output. The value is left out when it is
// empty or nothing is left after cleaning. omitted is true when it exceeds the depth or size limit; it is then
// never shortened. dropKeys names members of the top-level object that are removed before anything else.
func boundFreeObject(raw json.RawMessage, dropKeys ...string) (value any, omitted bool) {
	if isNull(raw) {
		return nil, false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || compact.Len() > freeObjectBytesMax {
		return nil, true
	}
	decoded, err := decodeValue(compact.Bytes())
	if err != nil || valueDepth(decoded) > freeObjectDepthMax {
		return nil, true
	}
	if object, ok := decoded.(map[string]any); ok {
		for _, key := range dropKeys {
			delete(object, key)
		}
	}
	cleaned, keep := cleanFreeValue(decoded)
	if !keep || emptyValue(cleaned) {
		return nil, false
	}
	return cleaned, false
}

// cleanFreeValue removes control characters from texts and drops every value that holds an address with a
// query, a fragment, or credentials, whatever the member is called. The addresses are never fetched. A
// container left empty by the removal is dropped as well.
func cleanFreeValue(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		if carriesAccess(v) {
			return nil, false
		}
		return stripControls(v), true
	case []any:
		kept := make([]any, 0, len(v))
		for _, item := range v {
			if item, ok := cleanFreeValue(item); ok {
				kept = append(kept, item)
			}
		}
		return kept, len(kept) > 0
	case map[string]any:
		kept := make(map[string]any, len(v))
		for key, item := range v {
			if item, ok := cleanFreeValue(item); ok {
				kept[stripControls(key)] = item
			}
		}
		return kept, len(kept) > 0
	}
	return value, true
}

// carriesAccess reports a text that is, or contains, an address whose query, fragment, or userinfo can grant
// access without signing in. The whole text goes, not just the address, because a text may link to it in
// forms that are hard to cut out safely.
func carriesAccess(text string) bool {
	for _, address := range addressInText.FindAllString(text, -1) {
		if strings.ContainsAny(address, "?#") {
			return true
		}
		rest := address[strings.Index(address, "://")+3:]
		if end := strings.IndexByte(rest, '/'); end >= 0 {
			rest = rest[:end]
		}
		if strings.Contains(rest, "@") {
			return true
		}
	}
	return false
}

// stripControls drops invalid UTF-8 and control or format characters except line breaks and tabs.
func stripControls(text string) string {
	if utf8.ValidString(text) && !strings.ContainsFunc(text, isStrippedRune) {
		return text
	}
	return strings.Map(func(r rune) rune {
		if isStrippedRune(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
}

func isStrippedRune(r rune) bool {
	return r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}
