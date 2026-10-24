package tui

import "github.com/charmbracelet/lipgloss"

// theme holds the styles of the TUI. The colours are few and taken from the terminal's own palette, so
// they follow its theme. None of them carries meaning on its own: focus has its marker and its border, a
// result its "[ok]", "[failed]", "warning:" or "error:" prefix, and a terminal without colour, or with
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
	// zebra shades every other unmarked row of a table screen as a reading aid on top of the '>' prefix
	// and the column layout, never instead of them; it is gone under NO_COLOR like every other colour
	// here, which is fine because it never was the only way to tell a row apart.
	zebra lipgloss.Style
	// key is the key of a footer hint (bold); the action after it keeps the plain hint style.
	key lipgloss.Style
}

func defaultTheme() theme {
	accent := lipgloss.Color("6")
	return theme{
		title:       lipgloss.NewStyle().Bold(true),
		active:      lipgloss.NewStyle().Bold(true).Foreground(accent),
		hint:        lipgloss.NewStyle().Faint(true),
		ok:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
		fail:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		warning:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
		focusBorder: lipgloss.NewStyle().Foreground(accent),
		quietBorder: lipgloss.NewStyle().Faint(true),
		activeRow:   lipgloss.NewStyle().Bold(true).Reverse(true),
		zebra:       lipgloss.NewStyle().Background(lipgloss.Color("8")),
		key:         lipgloss.NewStyle().Bold(true),
	}
}

// styles is the active theme.
var styles = defaultTheme()

func setTheme(t theme) { styles = t }
