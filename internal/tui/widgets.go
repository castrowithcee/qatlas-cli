package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Shared building blocks of the screens. Every block returns lines of exactly the requested width in cells,
// with closed borders at any width; content that does not fit is clipped. Colours come from styles only.

// panelOpts selects the look of a panel.
type panelOpts struct {
	focused bool // frame in the focus colour (double frame without colour)
	fill    bool // content area in the panel colour
}

// frameChars returns the frame characters: rounded, or double for a focused frame without colour, where the
// colour of the frame cannot show the focus.
func frameChars(focused bool) (h, v string, corners [4]string) {
	if focused && styles.palette.borderFocus == "" {
		return "═", "║", [4]string{"╔", "╗", "╚", "╝"}
	}
	return "─", "│", [4]string{"╭", "╮", "╰", "╯"}
}

// panel draws a frame of inner content width by rows around lines (width inner+4, height rows+2), with its
// title in the top border.
func panel(title string, lines []string, inner, rows int, o panelOpts) []string {
	inner = max(inner, 0)
	out := []string{panelTop(title, "", inner, o.focused)}
	bg := colour("")
	if o.fill && styles.palette.panel != "" {
		bg = styles.palette.panel
	}
	for i := 0; i < rows; i++ {
		text := ""
		if i < len(lines) {
			text = lines[i]
		}
		out = append(out, panelSide(text, inner, o.focused, bg))
	}
	return append(out, panelBottom(inner, o.focused))
}

// panelStyle is the style of the frame lines.
func panelStyle(focused bool) lipgloss.Style {
	if focused {
		return styles.focusBorder
	}
	return styles.quietBorder
}

// panelTop is the top border of a panel of inner content width, with its title in it and, dimmed at the right
// end, an optional note.
func panelTop(title, note string, inner int, focused bool) string {
	inner = max(inner, 0)
	style := panelStyle(focused)
	h, _, c := frameChars(focused)
	label := truncateCells(" "+title+" ", inner+1)
	if title == "" {
		label = ""
	}
	tail := ""
	if note != "" {
		tail = " " + note + " "
		if room := inner + 1 - lipgloss.Width(label) - 2; lipgloss.Width(tail) > room {
			tail = ""
		}
	}
	rule := max(inner+1-lipgloss.Width(label)-lipgloss.Width(tail), 0)
	if tail != "" {
		return style.Render(c[0]+h) + styles.title.Render(label) + style.Render(strings.Repeat(h, rule-1)) +
			styles.hint.Render(tail) + style.Render(h+c[1])
	}
	return style.Render(c[0]+h) + styles.title.Render(label) + style.Render(strings.Repeat(h, rule)+c[1])
}

// panelSide is one content line of a panel: cut or padded to inner cells between the side borders. A
// background colour fills the content area.
func panelSide(text string, inner int, focused bool, bg colour) string {
	inner = max(inner, 0)
	style := panelStyle(focused)
	_, v, _ := frameChars(focused)
	body := lipgloss.NewStyle()
	if bg != "" {
		body = body.Background(bg.lip())
	}
	text = keepBackground(clipCells(text, inner), bg)
	return style.Render(v) + body.Render(" "+padLine(text, inner)+" ") + style.Render(v)
}

// panelBottom is the bottom border of a panel of inner content width.
func panelBottom(inner int, focused bool) string {
	h, _, c := frameChars(focused)
	return panelStyle(focused).Render(c[2] + strings.Repeat(h, max(inner, 0)+2) + c[3])
}

// keepBackground makes a finished line keep the background bg across the resets of its own styles, so a
// filled area stays filled behind styled text. Without a colour it returns the line as it is.
func keepBackground(text string, bg colour) string {
	if bg == "" || !strings.Contains(text, "\x1b[") {
		return text
	}
	set := lipgloss.NewStyle().Background(bg.lip()).Render("x")
	set = set[:strings.Index(set, "x")]
	if set == "" {
		return text
	}
	return set + strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+set)
}

// keepStyle makes a finished line keep style across the resets of its own styles, like keepBackground does
// for a background alone.
func keepStyle(text string, style lipgloss.Style) string {
	if !strings.Contains(text, "\x1b[") {
		return text
	}
	set := style.Render("x")
	set = set[:strings.Index(set, "x")]
	if set == "" {
		return text
	}
	return strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+set)
}

// surfaceRow draws a finished input row as a field: cut or padded to width cells on the surface colour.
// Without a colour the row is only padded.
func surfaceRow(text string, width int) string {
	text = padLine(clipCells(text, width), width)
	if styles.palette.surface == "" {
		return text
	}
	return lipgloss.NewStyle().Background(styles.palette.surface.lip()).Render(keepBackground(text, styles.palette.surface))
}

// accentFill is the surface of the tab that has the focus: bold base text on the accent colour. Without colour
// it is the bold style of the plain tabs.
func accentFill() lipgloss.Style {
	p := styles.palette
	if plainTabs() {
		return styles.active
	}
	return lipgloss.NewStyle().Bold(true).Background(p.accent.lip()).Foreground(p.base.lip())
}

// tabStrip draws a row of tabs and returns the cells each takes, counted from the first cell of the row. The
// active tab is a surface: the marked-row fill of the section lists, the accent fill while it has the focus;
// the others are dimmed. Without colour the active tab stands in brackets, in reverse video while it has the
// focus, so the colour is never the only mark. A word "|" is a separator, not a tab. Every tab takes the same
// cells whether active or not.
func tabStrip(words []string, active int, focused bool) (string, [][2]int) {
	var b strings.Builder
	spans := make([][2]int, 0, len(words))
	x := 0
	plain := plainTabs()
	for i, word := range words {
		text, style := " "+word+" ", styles.hint
		switch {
		case word == "|":
			text = word
		case i == active:
			style = styles.activeRow
			if plain {
				text, style = "["+word+"]", styles.active
			}
			if focused {
				style = accentFill()
				if plain {
					style = styles.activeRow
				}
			}
		}
		if i > 0 {
			b.WriteString(" ")
			x++
		}
		b.WriteString(style.Render(text))
		spans = append(spans, [2]int{x, x + lipgloss.Width(text)})
		x += lipgloss.Width(text)
	}
	return b.String(), spans
}

// card is a bordered figure: the label dimmed above a large value. Width is the total width in cells.
func card(label, value string, width int) []string {
	inner := max(width-4, 0)
	lines := []string{styles.hint.Render(truncateCells(label, inner)), styles.title.Render(truncateCells(value, inner))}
	return panel("", lines, inner, 2, panelOpts{})
}

type statusKind int

const (
	statusOK statusKind = iota
	statusFail
	statusWarn
	statusDim
)

// statusMark is a coloured dot followed by the word; the word stays when there is no colour.
func statusMark(kind statusKind, word string) string {
	style := styles.hint
	switch kind {
	case statusOK:
		style = styles.ok
	case statusFail:
		style = styles.fail
	case statusWarn:
		style = styles.warning
	}
	return style.Render("●") + " " + word
}

type buttonKind int

const (
	buttonNormal buttonKind = iota
	buttonPrimary
	buttonDanger
)

// button renders a label as a button. The focus shows as "> label <" and reverse video, so it reads without
// colour; the primary and danger kinds are bold, and filled where there is colour.
func button(kind buttonKind, label string, focused bool) string {
	text := "[ " + label + " ]"
	if focused {
		text = "> " + label + " <"
	}
	style := lipgloss.NewStyle()
	p := styles.palette
	switch kind {
	case buttonPrimary:
		style = style.Bold(true)
		if p.accent != "" {
			style = style.Background(p.accent.lip()).Foreground(p.base.lip())
		}
	case buttonDanger:
		style = style.Bold(true)
		if p.fail != "" {
			style = style.Background(p.fail.lip()).Foreground(p.base.lip())
		}
	default:
		if p.border != "" {
			style = style.Foreground(p.text.lip())
		}
	}
	if focused {
		style = style.Bold(true).Reverse(true)
	}
	return style.Render(text)
}

// brandColor is the stable brand colour of a provider name.
func brandColor(name string) colour {
	h := fnv.New32a()
	h.Write([]byte(name))
	return styles.palette.brand[h.Sum32()%uint32(len(styles.palette.brand))]
}

// providerMark is the square in the colour of the provider.
func providerMark(name string) string {
	style := lipgloss.NewStyle()
	if c := brandColor(name); c != "" {
		style = style.Foreground(c.lip())
	}
	return style.Render("■")
}
