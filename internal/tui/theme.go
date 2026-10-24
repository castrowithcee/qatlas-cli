package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// theme holds the styles of the TUI. The colours are few and are indices of the terminal's own palette,
// picked per light or dark background (see buildTheme), so they follow its theme. None of them carries
// meaning on its own: focus has its marker and its border, a result its "[ok]", "[failed]", "warning:" or "error:" prefix, and a terminal without colour, or with
// NO_COLOR set, loses nothing.
type theme struct {
	title       lipgloss.Style
	active      lipgloss.Style
	hint        lipgloss.Style
	ok          lipgloss.Style
	fail        lipgloss.Style
	warning     lipgloss.Style
	focusBorder lipgloss.Style
	quietBorder lipgloss.Style
	// activeRow highlights the marked row of a table screen (a section list, the picker, the provider
	// table) the full width of the row, reversing whatever foreground and background the terminal already
	// has instead of naming a colour of its own. It never carries the selection alone: the '>' prefix of
	// listRow still leads the row, so NO_COLOR or an ASCII terminal keeps showing which one is marked.
	activeRow lipgloss.Style
	// key is the key of a footer hint (bold); the action after it keeps the plain hint style.
	key lipgloss.Style
}

func defaultTheme() theme { return buildTheme(true, false) }

// buildTheme returns the theme for a dark or a light terminal background. With noColor it names no colour at
// all and keeps only the attributes (bold, faint, reverse). None of them is the only carrier of a meaning.
func buildTheme(dark, noColor bool) theme {
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
			key:         lipgloss.NewStyle().Bold(true),
		}
	}
	// Palette indices follow the terminal's own theme. The dark case keeps cyan, green, red and yellow. On
	// a light background cyan and yellow wash out, so the light case takes blue for the accent and magenta
	// for warning.
	accent, ok, fail, warn := lipgloss.Color("4"), lipgloss.Color("2"), lipgloss.Color("1"), lipgloss.Color("5")
	if dark {
		accent, warn = lipgloss.Color("6"), lipgloss.Color("3")
	}
	return theme{
		title:       lipgloss.NewStyle().Bold(true),
		active:      lipgloss.NewStyle().Bold(true).Foreground(accent),
		hint:        lipgloss.NewStyle().Faint(true),
		ok:          lipgloss.NewStyle().Bold(true).Foreground(ok),
		fail:        lipgloss.NewStyle().Bold(true).Foreground(fail),
		warning:     lipgloss.NewStyle().Bold(true).Foreground(warn),
		focusBorder: lipgloss.NewStyle().Foreground(accent),
		quietBorder: lipgloss.NewStyle().Faint(true),
		activeRow:   lipgloss.NewStyle().Bold(true).Reverse(true),
		key:         lipgloss.NewStyle().Bold(true),
	}
}

// styles is the active theme.
var styles = defaultTheme()

func setTheme(t theme) { styles = t }

// applyColorScheme activates the theme the preference names: light and dark force their palette, anything
// else (auto) follows the background lipgloss detects. Forcing never overwrites that detection, so a later
// switch back to auto still finds the real background. NO_COLOR wins over all of it and asks the terminal
// nothing.
func applyColorScheme(mode string) {
	if os.Getenv("NO_COLOR") != "" {
		setTheme(buildTheme(true, true))
		return
	}
	switch mode {
	case uiprefs.ThemeLight:
		setTheme(buildTheme(false, false))
	case uiprefs.ThemeDark:
		setTheme(buildTheme(true, false))
	default:
		setTheme(buildTheme(lipgloss.HasDarkBackground(), false))
	}
}
