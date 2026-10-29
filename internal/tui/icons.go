package tui

import (
	"os"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// iconID names the meaning of an icon. Every meaning has an entry of its own in iconTable, so no two things
// share a glyph.
type iconID int

const (
	// iconNone is the zero value: a thing without an icon.
	iconNone iconID = iota
	iconLogo
	iconOverview
	iconConnections
	iconActivity
	iconAccess
	iconLibrary
	iconSettings
	iconServices
	iconCredentials
	iconTokens
	iconApprovals
	iconUpdate
	iconCount
)

// iconEntry is one meaning with its glyph for each icons value. The unicode glyphs are emoji that draw two
// cells without U+FE0F, joiners, skin tones or regional indicators, so every terminal that draws emoji agrees
// on their width; the nerdfont glyphs are in the Font Awesome range and draw one.
type iconEntry struct {
	meaning string
	unicode string
	nerd    string
}

var iconTable = [iconCount]iconEntry{
	iconNone:        {},
	iconLogo:        {"logo", "\U0001F310", ""},
	iconOverview:    {"overview", "\U0001F3E0", ""},
	iconConnections: {"connections", "\U0001F517", ""},
	iconActivity:    {"activity", "\U0001F4C8", ""},
	iconAccess:      {"access", "\U0001F511", ""},
	iconLibrary:     {"library", "\U0001F4DA", ""},
	iconSettings:    {"settings", "\U0001F527", ""},
	iconServices:    {"services", "\U0001F4E6", ""},
	iconCredentials: {"credentials", "\U0001F510", ""},
	iconTokens:      {"tokens", "\U0001F3AB", ""},
	iconApprovals:   {"approvals", "✅", ""},
	iconUpdate:      {"update", "\U0001F389", ""},
}

// areaIcons is the meaning of each area's icon.
var areaIcons = [areaCount]iconID{
	areaOverview:    iconOverview,
	areaConnections: iconConnections,
	areaActivity:    iconActivity,
	areaAccess:      iconAccess,
	areaLibrary:     iconLibrary,
	areaSettings:    iconSettings,
}

// utf8Terminal reports whether the terminal is known to draw more than ASCII: not a dumb terminal, and a
// locale that names UTF-8 or none at all. Tests replace it.
var utf8Terminal = func() bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(os.Getenv(name)); v != "" {
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return true
}

// icon is the glyph of id under the icons preference, or "" where there is none: the preference is none or
// empty, or the terminal does not draw UTF-8. Callers put it beside the name, never in its place.
func (m *Model) icon(id iconID) string {
	if id == iconNone {
		return ""
	}
	switch m.prefs.Icons {
	case uiprefs.IconsUnicode:
		if utf8Terminal() {
			return iconTable[id].unicode
		}
	case uiprefs.IconsNerdFont:
		if utf8Terminal() {
			return iconTable[id].nerd
		}
	}
	return ""
}

// nerdAreaGlyphs are the area glyphs of the nerdfont value, shown as test characters.
func nerdAreaGlyphs() []string {
	glyphs := make([]string, 0, areaCount)
	for _, id := range areaIcons {
		glyphs = append(glyphs, iconTable[id].nerd)
	}
	return glyphs
}
