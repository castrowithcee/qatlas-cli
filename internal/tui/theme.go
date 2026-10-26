package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// palette names the colours of a theme by role. Screen code takes colours only from here or from the styles
// built on them, never as a literal. Each theme has a truecolor palette (hex) and one hand-picked for 256 and for
// 16 colours (see paletteOf), because a computed step-down picks muddy neighbours. A palette of a theme without
// colour is the zero value.
type palette struct {
	base, surface, panel   lipgloss.Color
	border, borderFocus    lipgloss.Color
	text, textDim          lipgloss.Color
	accent, ok, fail, warn lipgloss.Color
	selection              lipgloss.Color
	heat                   [5]lipgloss.Color // 0 is an empty cell, 4 the strongest
	brand                  [8]lipgloss.Color // fixed row of provider marks
}

var darkPalette = palette{
	base: "#0b141d", surface: "#101b26", panel: "#121f2e",
	border: "#2a3d52", borderFocus: "#3b82f6",
	text: "#d6e2ee", textDim: "#7f93a8",
	accent: "#4c9aff", ok: "#3fb56b", fail: "#f0525a", warn: "#e7b84c",
	selection: "#14336b",
	heat:      [5]lipgloss.Color{"#16222d", "#1f5a3a", "#2a8a50", "#35a95a", "#4fd47a"},
	brand:     [8]lipgloss.Color{"#f08c3a", "#4c9aff", "#e7b84c", "#2fb5a8", "#a35cf0", "#5fd07f", "#e0629a", "#ef5b5b"},
}

var lightPalette = palette{
	base: "#f6f8fb", surface: "#eaeff5", panel: "#ffffff",
	border: "#b8c4d2", borderFocus: "#1d5fd6",
	text: "#14202e", textDim: "#566778",
	accent: "#1d5fd6", ok: "#1a7f43", fail: "#c62828", warn: "#9a6700",
	selection: "#cfe0fb",
	heat:      [5]lipgloss.Color{"#e1e8ef", "#b7e1c4", "#7cc794", "#3fa365", "#1b7a44"},
	brand:     [8]lipgloss.Color{"#c2570c", "#1d5fd6", "#9a6700", "#0f7f79", "#7b3fc4", "#1a7f43", "#b8346f", "#c62828"},
}

// The 256-colour palettes name indexes of the xterm cube and ramp, the 16-colour ones the ANSI slots of the
// terminal's own scheme. The 16-colour surfaces stay black: a dim grey slot would fall into the text colours.
var darkPalette256 = palette{
	base: "233", surface: "234", panel: "235",
	border: "240", borderFocus: "69",
	text: "253", textDim: "246",
	accent: "75", ok: "71", fail: "203", warn: "179",
	selection: "24",
	heat:      [5]lipgloss.Color{"236", "22", "29", "35", "41"},
	brand:     [8]lipgloss.Color{"208", "75", "179", "37", "135", "78", "168", "203"},
}

var lightPalette256 = palette{
	base: "255", surface: "254", panel: "231",
	border: "250", borderFocus: "26",
	text: "234", textDim: "241",
	accent: "26", ok: "28", fail: "160", warn: "130",
	selection: "153",
	heat:      [5]lipgloss.Color{"254", "151", "114", "71", "28"},
	brand:     [8]lipgloss.Color{"166", "26", "130", "30", "91", "28", "125", "160"},
}

var darkPalette16 = palette{
	base: "0", surface: "0", panel: "0",
	border: "8", borderFocus: "12",
	text: "15", textDim: "7",
	accent: "12", ok: "10", fail: "9", warn: "11",
	selection: "4",
	heat:      [5]lipgloss.Color{"8", "2", "2", "10", "10"},
	brand:     [8]lipgloss.Color{"3", "12", "11", "6", "5", "10", "13", "9"},
}

var lightPalette16 = palette{
	base: "15", surface: "15", panel: "15",
	border: "8", borderFocus: "4",
	text: "0", textDim: "8",
	accent: "4", ok: "2", fail: "1", warn: "3",
	selection: "7",
	heat:      [5]lipgloss.Color{"7", "2", "2", "10", "10"},
	brand:     [8]lipgloss.Color{"3", "4", "3", "6", "5", "2", "5", "1"},
}

// paletteOf is the palette of a theme for the colour profile of the terminal.
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

func defaultTheme() theme { return buildTheme(true, false, false) }

// buildTheme returns the theme for a dark or a light terminal background. With noColor it names no colour at
// all and keeps only the attributes (bold, faint, reverse). None of them is the only carrier of a meaning.
// filled paints the whole screen in the base colour of the palette.
func buildTheme(dark, noColor, filled bool) theme {
	if noColor {
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
	p := paletteOf(dark, currentProfile())
	return theme{
		palette:     p,
		title:       lipgloss.NewStyle().Bold(true).Foreground(p.text),
		active:      lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		hint:        lipgloss.NewStyle().Foreground(p.textDim),
		ok:          lipgloss.NewStyle().Bold(true).Foreground(p.ok),
		fail:        lipgloss.NewStyle().Bold(true).Foreground(p.fail),
		warning:     lipgloss.NewStyle().Bold(true).Foreground(p.warn),
		focusBorder: lipgloss.NewStyle().Foreground(p.borderFocus),
		quietBorder: lipgloss.NewStyle().Foreground(p.border),
		activeRow:   lipgloss.NewStyle().Bold(true).Foreground(p.text).Background(p.selection),
		cell:        lipgloss.NewStyle().Foreground(p.text),
		selMark:     lipgloss.NewStyle().Bold(true).Foreground(p.accent).Background(p.selection),
		key:         lipgloss.NewStyle().Bold(true).Foreground(p.text),
		footKey:     lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		filled:      filled,
	}
}

// styles is the active theme.
var styles = defaultTheme()

func setTheme(t theme) { styles = t }

// applyColorScheme activates the theme the preference names: light and dark force their palette, anything
// else (auto) follows the background lipgloss detects. Forcing never overwrites that detection, so a later
// switch back to auto still finds the real background. NO_COLOR wins over all of it and asks the terminal
// nothing. background qatlas fills the screen with the palette's base colour.
func applyColorScheme(mode, background string) {
	if os.Getenv("NO_COLOR") != "" {
		setTheme(buildTheme(true, true, false))
		return
	}
	filled := background == uiprefs.BackgroundQatlas
	switch mode {
	case uiprefs.ThemeLight:
		setTheme(buildTheme(false, false, filled))
	case uiprefs.ThemeDark:
		setTheme(buildTheme(true, false, filled))
	default:
		setTheme(buildTheme(hasDarkBackground(), false, filled))
	}
}
