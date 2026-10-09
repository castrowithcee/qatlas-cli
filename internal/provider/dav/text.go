// Package dav holds the CalDAV and CardDAV handling that DAV providers share: bounded multi-status
// parsing, request bodies, and the iCalendar and vCard models with their parsers and builders. It knows no
// provider, origin, or tool.
package dav

import (
	"crypto/rand"
	"encoding/hex"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxETagLength = 256
	maxAddressLen = 254
)

// Clean replaces control characters and caps a provider string at max bytes on a rune boundary.
func Clean(value string, max int) string { return CleanText(value, max, false) }

// CleanText is Clean; with lines set it keeps line breaks and tabs, so a multi-line text stays readable.
func CleanText(value string, max int, lines bool) string {
	if lines {
		value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	}
	value = strings.Map(func(r rune) rune {
		if lines && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, " "))
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// ETagOf returns an entity tag without its quotes, bounded.
func ETagOf(value string) string {
	return Clean(strings.Trim(strings.TrimSpace(value), `"`), maxETagLength)
}

// NormalETag returns the entity tag in the form ETagOf reports it, or false for an unusable value. One
// surrounding pair of quotes is accepted.
func NormalETag(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	if value == "" || value == "*" || len(value) > maxETagLength {
		return "", false
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= 0x20 || value[i] >= 0x7f || value[i] == '"' {
			return "", false
		}
	}
	return value, true
}

// ValidText accepts valid UTF-8 without control characters; line breaks are allowed and end up escaped.
func ValidText(value string, max int, lines bool) (string, bool) {
	if lines {
		value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	}
	if len(value) > max || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if lines && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

// ValidAddress accepts a plain e-mail address without display name.
func ValidAddress(address string) bool {
	if len(address) < 3 || len(address) > maxAddressLen || strings.ContainsAny(address, " <>\",;:()\\") {
		return false
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && parsed.Name == ""
}

// RandomID returns a random version 4 UUID.
func RandomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}
