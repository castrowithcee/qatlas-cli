package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// layoutWorkspace derives the room of the workspace from the terminal. Above it stands one line with the
// configuration path; beside it the sidebar with its frame, or above it one navigation line and a rule.
func (m *Model) layoutWorkspace() {
	if m.sidebarLayout() {
		m.width = max(m.termWidth-sidebarWidth-1-frameCells, 1)
		m.height = max(m.termHeight-1-2, 1)
		return
	}
	m.width = max(m.termWidth, 1)
	m.height = max(m.termHeight-3, 1)
}

// frame puts the workspace into the editor's frame.
func (m *Model) frame(workspace string) string {
	lines := m.headerLines()
	content := strings.Split(workspace, "\n")
	if !m.sidebarLayout() {
		lines = append(lines, m.navLine(), styles.quietBorder.Render(strings.Repeat("─", m.termWidth)))
		lines = append(lines, content...)
		return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
	}
	title := m.section.title()
	switch {
	case m.screen == screenHelp:
		title = "Help"
	case m.screen == screenCommand:
		title = "Commands"
	case m.screen == screenSelftest:
		title = "Selftest"
	case m.screen == screenUpdate || m.screen == screenRestart:
		title = "Update"
	case m.wizard != nil:
		title = "Guided setup"
	}
	left := box("Sections", m.sidebarLines(), sidebarWidth-frameCells, m.height, m.screen == screenNav)
	right := box(title, content, m.width, m.height, m.screen != screenNav)
	for i := range left {
		lines = append(lines, left[i]+" "+right[i])
	}
	return strings.Join(lines, "\n")
}

// headerLines are the header rows above the workspace: the path line alone for a run with no vault
// configured, since showing a warning for something that does not exist yet would be misleading;
// otherwise the vault state beside the path line from sidebarMinWidth up, or, in a narrower terminal, its
// own row above the path line, because the two together do not read well combined that narrow.
func (m *Model) headerLines() []string {
	vault := m.vaultHeaderText()
	if vault == "" {
		return []string{m.pathLine("")}
	}
	if m.sidebarLayout() {
		return []string{m.pathLine(vault)}
	}
	return []string{vault, m.pathLine("")}
}

// pathLine names the configuration file the editor works on. A path too long for the line keeps its end,
// the part that tells files apart. A newer release stands between the title and the path, in the longest
// form that still leaves the path some room. vaultText is the vault state segment headerLines puts beside
// it once the sidebar layout has room for both on one line; "" leaves this exactly the plain path line it
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

// navLine is the navigation of a narrow terminal: every section in one line, the active one in brackets,
// and "> " in front while it has the focus. A terminal too narrow for the names keeps the active name and
// the numbers of the others.
func (m *Model) navLine() string {
	focus := "  "
	if m.screen == screenNav {
		focus = "> "
	}
	full, short := []string{}, []string{}
	for s := section(0); s < sectionCount; s++ {
		name := fmt.Sprintf("%d %s", int(s)+1, s.title())
		if s == m.section && m.wizard == nil {
			full, short = append(full, "["+name+"]"), append(short, "["+name+"]")
			continue
		}
		full, short = append(full, " "+name+" "), append(short, fmt.Sprint(int(s)+1))
	}
	if m.wizard != nil {
		full, short = append(full, "[c setup]"), append(short, "[c setup]")
	}
	line := focus + strings.Join(full, " ")
	if lipgloss.Width(line) > m.termWidth {
		line = focus + strings.Join(short, " ")
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
