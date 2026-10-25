package brave

import (
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Bounds of what a result shows. Brave's text is untrusted and unbounded, so every string and list is cut
// to a limit before it leaves the provider.
const (
	maxTitleRunes   = 300
	maxTextRunes    = 1500
	maxSnippetRunes = 1000
	maxShortRunes   = 200
	maxAddressBytes = 2048
	maxExtraSnips   = 5
	maxIcons        = 10
	maxSites        = 20
)

// clip cuts a string to at most limit characters and marks the cut.
func clip(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// clipAll cuts every string of a list and the list itself.
func clipAll(values []string, limit, runes int) []string {
	var out []string
	for _, value := range values {
		if len(out) == limit {
			break
		}
		out = append(out, clip(value, runes))
	}
	return out
}

// address returns a result address as bounded text, or "" when it is not an absolute http or https address
// without user information. Such an address is shown and never fetched.
func address(value string) string {
	if value == "" || len(value) > maxAddressBytes {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	return value
}

// wholeNumber returns a timestamp as a whole number, or nil when it is absent or too large for a JSON
// integer that every reader keeps exact.
func wholeNumber(value *float64) *float64 {
	if value == nil {
		return nil
	}
	if whole := math.Trunc(*value); math.Abs(whole) < 1<<53 {
		return &whole
	}
	return nil
}
