package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// layoutWorkspace derives the room of the workspace from the terminal. It takes the full width; above it
// stand the header, one tab line, the option bar of an area that has one, and a rule.
func (m *Model) layoutWorkspace() {
	m.width = max(m.termWidth, 1)
	m.height = max(m.termHeight-3, 1)
	if m.hasSubTabs() {
		m.height = max(m.height-1, 1)
	}
}

// frame puts the workspace under the header, the tab line, the option bar, and a rule.
func (m *Model) frame(workspace string) string {
	lines := m.headerLines()
	lines = append(lines, m.navLine())
	if m.hasSubTabs() {
		// The line stays, empty, during the guided setup so the workspace never changes height under it.
		lines = append(lines, m.subNavLine())
	}
	lines = append(lines, styles.quietBorder.Render(strings.Repeat("─", m.termWidth)))
	lines = append(lines, strings.Split(workspace, "\n")...)
	return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
}

// headerLines are the header rows above the workspace: the path line alone for a run with no vault
// configured, since showing a warning for something that does not exist yet would be misleading;
// otherwise the vault state beside the path line from narrowWidth up, or, in a narrower terminal, its
// own row above the path line, because the two together do not read well combined that narrow.
func (m *Model) headerLines() []string {
	vault := m.vaultHeaderText()
	if vault == "" {
		return []string{m.pathLine("")}
	}
	if m.termWidth >= narrowWidth {
		return []string{m.pathLine(vault)}
	}
	return []string{vault, m.pathLine("")}
}

// pathLine names the configuration file the editor works on. A path too long for the line keeps its end,
// the part that tells files apart. A newer release stands between the title and the path, in the longest
// form that still leaves the path some room. vaultText is the vault state segment headerLines puts beside
// it once the terminal has room for both on one line; "" leaves this exactly the plain path line it
// always was, which a run without a vault, and the narrow layout's own second row, both still use.
func (m *Model) pathLine(vaultText string) string {
	suffix := ""
	if !m.configExists {
		suffix = " (created on first save)"
	}
	title := "Qatlas setup  "
	if m.termWidth < 60 {
		title = ""
	}
	sep := ""
	if vaultText != "" {
		sep = "  "
	}
	reserved := lipgloss.Width(title) + lipgloss.Width(vaultText) + lipgloss.Width(sep)
	banner := m.updateBanner(m.termWidth - reserved - 2 - pathRoom)
	if banner != "" {
		banner += "  "
	}
	rest := m.termWidth - reserved - lipgloss.Width(banner)
	label := "Config: "
	if lipgloss.Width(label+suffix)+pathRoom > rest {
		// A narrow line, whatever crowds it (the banner, or now the vault state beside it), keeps the end of
		// the path and drops its label rather than the path itself.
		label, suffix = "", ""
	}
	path := truncateLeft(m.svc.Path(), rest-lipgloss.Width(label+suffix))
	return styles.title.Render(title) + vaultText + sep + styles.ok.Render(banner) + styles.hint.Render(label+path+suffix)
}

// pathRoom is what the banner of a newer release leaves the path at least.
const pathRoom = 12

// navLine is the tab line: every area by number and name, the active one in brackets, and "> " in front
// while the tabs have the focus; the brackets and the marker, not the colour, say which one is active. A
// terminal too narrow for all names closes the gaps first and then shortens the others to their numbers;
// the active tab always stays whole.
func (m *Model) navLine() string {
	focus := "  "
	if m.screen == screenNav && !m.navSub {
		focus = "> "
	}
	var spaced, compact, short []string
	for s := area(0); s < areaCount; s++ {
		name := fmt.Sprintf("%d %s", int(s)+1, s.title())
		if s == m.section.area() && m.wizard == nil {
			tab := "[" + name + "]"
			spaced, compact, short = append(spaced, tab), append(compact, tab), append(short, tab)
			continue
		}
		spaced, compact, short = append(spaced, " "+name+" "), append(compact, name), append(short, fmt.Sprint(int(s)+1))
	}
	if m.wizard != nil {
		spaced, compact, short = append(spaced, "[c setup]"), append(compact, "[c setup]"), append(short, "[c setup]")
	}
	line := focus + strings.Join(spaced, " ")
	for _, tabs := range [][]string{compact, short} {
		if lipgloss.Width(line) <= m.termWidth {
			break
		}
		line = focus + strings.Join(tabs, " ")
	}
	return styles.active.Render(clipCells(line, m.termWidth))
}

// box draws a frame of inner width by rows around lines, with its title in the top border. The frame with
// the focus is drawn double, the other one single, so focus reads without colour as well.
func box(title string, lines []string, inner, rows int, focused bool) []string {
	style, h, v, corners := styles.quietBorder, "─", "│", [4]string{"╭", "╮", "╰", "╯"}
	if focused {
		style, h, v, corners = styles.focusBorder, "═", "║", [4]string{"╔", "╗", "╚", "╝"}
	}
	label := truncateCells(" "+title+" ", inner+1)
	out := []string{style.Render(corners[0]+h) + styles.title.Render(label) +
		style.Render(strings.Repeat(h, max(inner+1-lipgloss.Width(label), 0))+corners[1])}
	for i := 0; i < rows; i++ {
		text := ""
		if i < len(lines) {
			text = lines[i]
		}
		out = append(out, style.Render(v)+" "+padLine(text, inner)+" "+style.Render(v))
	}
	return append(out, style.Render(corners[2]+strings.Repeat(h, inner+2)+corners[3]))
}
