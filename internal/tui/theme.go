package tui

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// palette names the colours of a theme by role. Screen code takes colours only from here or from the styles
// built on them, never as a literal. The qatlas themes have a truecolor palette (hex) and one hand-picked for 256
// and for 16 colours (see paletteOf), because a computed step-down picks muddy neighbours. A palette of a theme
// without colour is the zero value; an empty role is one the theme leaves to the terminal.
type palette struct {
	base, surface, panel   colour
	border, borderFocus    colour
	text, textDim          colour
	accent, ok, fail, warn colour
	selection              colour
	heat                   [5]colour // 0 is an empty cell, 4 the strongest
	brand                  [8]colour // fixed row of provider marks
}

var darkPalette = palette{
	base: "#0b141d", surface: "#1a2b3c", panel: "#14212f",
	border: "#2a3d52", borderFocus: "#3b82f6",
	text: "#d6e2ee", textDim: "#7f93a8",
	accent: "#4c9aff", ok: "#3fb56b", fail: "#f0525a", warn: "#e7b84c",
	selection: "#14336b",
	heat:      [5]colour{"#16222d", "#1f5a3a", "#2a8a50", "#35a95a", "#4fd47a"},
	brand:     [8]colour{"#f08c3a", "#4c9aff", "#e7b84c", "#2fb5a8", "#a35cf0", "#5fd07f", "#e0629a", "#ef5b5b"},
}

var lightPalette = palette{
	base: "#f6f8fb", surface: "#e0e7ef", panel: "#edf1f6",
	border: "#b8c4d2", borderFocus: "#1d5fd6",
	text: "#14202e", textDim: "#566778",
	accent: "#1d5fd6", ok: "#1a7f43", fail: "#c62828", warn: "#9a6700",
	selection: "#cfe0fb",
	heat:      [5]colour{"#e1e8ef", "#b7e1c4", "#7cc794", "#3fa365", "#1b7a44"},
	brand:     [8]colour{"#c2570c", "#1d5fd6", "#9a6700", "#0f7f79", "#7b3fc4", "#1a7f43", "#b8346f", "#c62828"},
}

// The 256-colour palettes name indexes of the xterm cube and ramp, the 16-colour ones the ANSI slots of the
// terminal's own scheme. The 16-colour surfaces stay black: a dim grey slot would fall into the text colours.
var darkPalette256 = palette{
	base: "233", surface: "236", panel: "235",
	border: "240", borderFocus: "69",
	text: "253", textDim: "246",
	accent: "75", ok: "71", fail: "203", warn: "179",
	selection: "24",
	heat:      [5]colour{"236", "22", "29", "35", "41"},
	brand:     [8]colour{"208", "75", "179", "37", "135", "78", "168", "203"},
}

var lightPalette256 = palette{
	base: "255", surface: "253", panel: "254",
	border: "250", borderFocus: "26",
	text: "234", textDim: "241",
	accent: "26", ok: "28", fail: "160", warn: "130",
	selection: "153",
	heat:      [5]colour{"254", "151", "114", "71", "28"},
	brand:     [8]colour{"166", "26", "130", "30", "91", "28", "125", "160"},
}

var darkPalette16 = palette{
	base: "0", surface: "0", panel: "0",
	border: "8", borderFocus: "12",
	text: "15", textDim: "7",
	accent: "12", ok: "10", fail: "9", warn: "11",
	selection: "4",
	heat:      [5]colour{"8", "2", "2", "10", "10"},
	brand:     [8]colour{"3", "12", "11", "6", "5", "10", "13", "9"},
}

var lightPalette16 = palette{
	base: "15", surface: "15", panel: "15",
	border: "8", borderFocus: "4",
	text: "0", textDim: "8",
	accent: "4", ok: "2", fail: "1", warn: "3",
	selection: "7",
	heat:      [5]colour{"7", "2", "2", "10", "10"},
	brand:     [8]colour{"3", "4", "3", "6", "5", "2", "5", "1"},
}

// paletteOf is the palette of a qatlas theme for the colour profile of the terminal.
func paletteOf(dark bool, profile colorProfile) palette {
	switch {
	case profile == profile256 && dark:
		return darkPalette256
	case profile == profile256:
		return lightPalette256
	case profile == profile16 && dark:
		return darkPalette16
	case profile == profile16:
		return lightPalette16
	case dark:
		return darkPalette
	}
	return lightPalette
}

// themeDef is a theme the theme preference can name: its palette for the colour profile of the terminal, and
// whether it paints the whole screen in its base colour.
type themeDef struct {
	name    string
	paints  bool
	palette func(colorProfile) palette
}

// themes are the themes the General page offers, the default first. Every theme but terminal paints the
// screen, so its colours never meet a terminal background they were not chosen for. A scheme with known
// colours joins as one more entry whose palette is mixPalette over them.
var themes = []themeDef{
	{uiprefs.ThemeTerminal, false, terminalPalette},
	{uiprefs.ThemeQatlasDark, true, func(p colorProfile) palette { return paletteOf(true, p) }},
	{uiprefs.ThemeQatlasLight, true, func(p colorProfile) palette { return paletteOf(false, p) }},
}

// themeNamed is the theme of a name; a name no theme has gives the default and false.
func themeNamed(name string) (themeDef, bool) {
	for _, t := range themes {
		if t.name == name {
			return t, true
		}
	}
	return themes[0], false
}

// themeNames are the names of the themes in the order the General page offers them.
func themeNames() []string {
	names := make([]string, len(themes))
	for i, t := range themes {
		names[i] = t.name
	}
	return names
}

// knownTheme is the theme the preference names, or the default with a warning where this version has no theme
// of that name.
func knownTheme(name string) (string, string) {
	if t, ok := themeNamed(name); ok {
		return t.name, ""
	}
	return themes[0].name, fmt.Sprintf("theme %q is not a theme of this version, using %s", name, themes[0].name)
}

// theme holds the palette and the styles of the TUI. The styles are built from the palette roles. None of
// them carries meaning on its own: focus has its marker and its border, a result its "[ok]", "[failed]",
// "warning:" or "error:" prefix, and a terminal without colour, or with NO_COLOR set, loses nothing.
type theme struct {
	palette     palette
	title       lipgloss.Style
	active      lipgloss.Style
	hint        lipgloss.Style
	ok          lipgloss.Style
	fail        lipgloss.Style
	warning     lipgloss.Style
	focusBorder lipgloss.Style
	quietBorder lipgloss.Style
	// activeRow highlights the marked row of a table screen (a section list, the picker, the provider
	// table) the full width of the row. It never carries the selection alone: the mark of listRow (an accent
	// bar under colour, '>' without) still leads the row, so NO_COLOR or an ASCII terminal keeps showing which one
	// is marked.
	activeRow lipgloss.Style
	// cell is the text of a table cell that carries content (names, urls, descriptions); the dimmed cells use
	// hint. selMark is the narrow accent bar that leads the marked row, on the colour of the bar.
	cell    lipgloss.Style
	selMark lipgloss.Style
	// key is the key of a footer hint (bold); the action after it keeps the plain hint style.
	key lipgloss.Style
	// footKey is the key of the footer line: the accent colour, bold.
	footKey lipgloss.Style
	// filled says the whole screen carries the base colour (see paintCanvas); never set without colour.
	filled bool
}

func defaultTheme() theme { return buildTheme(paletteOf(true, currentProfile()), false) }

// noColorTheme names no colour at all and keeps only the attributes (bold, faint, reverse). None of them is
// the only carrier of a meaning.
func noColorTheme() theme {
	return theme{
		title:       lipgloss.NewStyle().Bold(true),
		active:      lipgloss.NewStyle().Bold(true),
		hint:        lipgloss.NewStyle().Faint(true),
		ok:          lipgloss.NewStyle().Bold(true),
		fail:        lipgloss.NewStyle().Bold(true),
		warning:     lipgloss.NewStyle().Bold(true),
		focusBorder: lipgloss.NewStyle(),
		quietBorder: lipgloss.NewStyle().Faint(true),
		activeRow:   lipgloss.NewStyle().Bold(true).Reverse(true),
		cell:        lipgloss.NewStyle(),
		selMark:     lipgloss.NewStyle().Bold(true).Reverse(true),
		key:         lipgloss.NewStyle().Bold(true),
		footKey:     lipgloss.NewStyle().Bold(true),
	}
}

// buildTheme returns the theme of a palette; paints fills the whole screen in the base colour of the palette.
// A palette without a selection colour marks the selected row in reverse video.
func buildTheme(p palette, paints bool) theme {
	activeRow := lipgloss.NewStyle().Bold(true).Foreground(p.text.lip()).Background(p.selection.lip())
	selMark := lipgloss.NewStyle().Bold(true).Foreground(p.accent.lip()).Background(p.selection.lip())
	if p.selection == "" {
		activeRow = lipgloss.NewStyle().Bold(true).Reverse(true)
		selMark = lipgloss.NewStyle().Bold(true).Reverse(true)
	}
	return theme{
		palette:     p,
		title:       lipgloss.NewStyle().Bold(true).Foreground(p.text.lip()),
		active:      lipgloss.NewStyle().Bold(true).Foreground(p.accent.lip()),
		hint:        lipgloss.NewStyle().Foreground(p.textDim.lip()),
		ok:          lipgloss.NewStyle().Bold(true).Foreground(p.ok.lip()),
		fail:        lipgloss.NewStyle().Bold(true).Foreground(p.fail.lip()),
		warning:     lipgloss.NewStyle().Bold(true).Foreground(p.warn.lip()),
		focusBorder: lipgloss.NewStyle().Foreground(p.borderFocus.lip()),
		quietBorder: lipgloss.NewStyle().Foreground(p.border.lip()),
		activeRow:   activeRow,
		cell:        lipgloss.NewStyle().Foreground(p.text.lip()),
		selMark:     selMark,
		key:         lipgloss.NewStyle().Bold(true).Foreground(p.text.lip()),
		footKey:     lipgloss.NewStyle().Bold(true).Foreground(p.accent.lip()),
		filled:      paints,
	}
}

// styles is the active theme.
var styles = defaultTheme()

func setTheme(t theme) { styles = t }

// applyTheme activates the theme of a name; a name no theme has gives the default. NO_COLOR wins over every
// theme, and the editor asks the terminal nothing then (see terminalColourQuery).
func applyTheme(name string) {
	if os.Getenv("NO_COLOR") != "" {
		setTheme(noColorTheme())
		return
	}
	t, _ := themeNamed(name)
	setTheme(buildTheme(t.palette(currentProfile()), t.paints))
}
