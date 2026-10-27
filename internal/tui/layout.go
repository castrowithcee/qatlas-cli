package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// layoutWorkspace derives the room of the workspace from the terminal. It takes the full width; above it
// stand the header (one line, more only when its parts do not fit), one tab line, the option bar of an area
// that has one, and a rule; below it stands the one-line footer. The header grows and shrinks with the vault
// and admin state, so the height is derived again whenever that can have changed.
func (m *Model) layoutWorkspace() {
	m.width = max(m.termWidth, 1)
	m.height = max(m.termHeight-len(m.headerLines())-3, 1)
	if m.hasSubTabs() {
		m.height = max(m.height-1, 1)
	}
}

// frame puts the workspace under the header, the tab line, the option bar, and a rule, and the footer under
// it. The workspace is cut or padded to exactly the rows layoutWorkspace gave it, so the footer is always the
// last line of the screen.
func (m *Model) frame(workspace string) string {
	m.registerBars()
	lines, back := m.headerLayout()
	m.registerBack(back)
	lines = append(lines, m.navLine())
	if m.hasSubTabs() {
		// The line stays, empty, during the guided setup so the workspace never changes height under it.
		lines = append(lines, m.subNavLine())
	}
	lines = append(lines, styles.quietBorder.Render(strings.Repeat("─", m.termWidth)))
	body := strings.Split(workspace, "\n")
	if len(body) > m.height {
		body = body[:m.height]
	}
	for len(body) < m.height {
		body = append(body, "")
	}
	lines = append(lines, body...)
	lines = append(lines, m.footerLine())
	return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
}

// footerHits makes the key groups of the footer clickable, where the footer holds the buttons of a dialog.
func (m *Model) footerHits() {
	for _, span := range m.footerSpans {
		if span.key == "" {
			continue
		}
		name := span.key
		m.addHit(span.x0, m.termHeight-1, span.x1, m.termHeight, func() tea.Cmd { return m.pressName(name) })
	}
}

// headerSep is the gap between two parts of the header.
const headerSep = "  "

// pathRoom is what the path keeps at least on the line of the status marks, and what the banner of a newer
// release leaves it.
const pathRoom = 12

// headerLines are the header rows above the workspace. The first row starts with the name, then the status
// marks of the vault and of the admin session (none for a run with no vault configured: a warning for
// something that does not exist yet would mislead), the banner of a newer release, and the config path. The
// help and command keys stand at the right end. When the width does not suffice, those keys go first, then
// the path is shortened at its start (the end tells files apart); only when even that leaves the path less
// than pathRoom does it move to a row of its own, and a status part that does not fit wraps like text.
func (m *Model) headerLines() []string {
	lines, _ := m.headerLayout()
	return lines
}

// headerLayout is headerLines and the cells of the back button in the first row (empty when it is not shown).
// The button stands at the right end in front of the help and command keys; it gives way last, so it never
// changes the rows or the path that the keys alone would leave.
func (m *Model) headerLayout() ([]string, navSpan) {
	width := max(m.termWidth, 1)
	lines, used := flowParts(m.headerStatus(), width)
	path := m.configPath()
	suffix := ""
	if !m.configExists {
		suffix = tr(tHeaderCreated)
	}
	rights := m.headerRights()
	pathMin := min(pathRoom, lipgloss.Width(path))
	sep := lipgloss.Width(headerSep)

	// The path keeps the first claim on the row; the banner takes what is left, or the room of the status
	// row when the path has a row of its own.
	room := width - used - sep
	banner := ""
	if len(lines) == 1 {
		banner = m.updateBanner(room - sep - pathMin)
	}
	if banner == "" && !(len(lines) == 1 && room >= pathMin) {
		banner = m.updateBanner(room)
	}
	if banner != "" {
		lines[len(lines)-1] += headerSep + styles.ok.Render(banner)
		used += sep + lipgloss.Width(banner)
		room = width - used - sep
	}

	if len(lines) == 1 && room >= pathMin {
		shown := fitPath(path, suffix, room)
		line := lines[0] + headerSep + styles.hint.Render(shown)
		var back navSpan
		if shown == path || shown == path+suffix {
			line, back = placeRight(line, rights, width)
			if back.x1 > back.x0 || len(rights) == 0 || rights[len(rights)-1].back.x1 == 0 {
				return []string{line}, back
			}
		}
		// The button outranks the whole path: it takes its room first, and the path is cut to what is left
		// as long as that keeps pathRoom; the keys stand only where the whole path fits.
		if len(rights) > 0 && rights[len(rights)-1].back.x1 > 0 {
			button := rights[len(rights)-1:]
			if left := room - sep - lipgloss.Width(button[0].text); left >= pathMin {
				cut := lines[0] + headerSep + styles.hint.Render(fitPath(path, suffix, left))
				cut, back = placeRight(cut, button, width)
				return []string{cut}, back
			}
		}
		return []string{line}, navSpan{}
	}
	var back navSpan
	if len(lines) == 1 {
		lines[0], back = placeRight(lines[0], rights, width)
	}
	return append(lines, styles.hint.Render(fitPath(path, suffix, width))), back
}

// headerRight is a block at the right end of the header: the cells of the back button in it, if it holds one.
type headerRight struct {
	text string
	back navSpan
}

// placeRight puts the widest block that fits, with at least headerSep before it, at the right end of line and
// returns the line and the cells of the back button in it.
func placeRight(line string, rights []headerRight, width int) (string, navSpan) {
	for _, r := range rights {
		w := lipgloss.Width(r.text)
		if gap := width - lipgloss.Width(line) - w; gap >= lipgloss.Width(headerSep) {
			x := width - w
			back := navSpan{}
			if r.back.x1 > r.back.x0 {
				back = navSpan{x + r.back.x0, x + r.back.x1}
			}
			return line + strings.Repeat(" ", gap) + r.text, back
		}
	}
	return line, navSpan{}
}

// headerRights are the blocks the right end of the header can show, widest first. When the width is short the
// help and command keys go first, then the back button; esc stays the way back.
func (m *Model) headerRights() []headerRight {
	hints := m.headerHints()
	back := m.backButton()
	var rights []headerRight
	if back != "" {
		bw := lipgloss.Width(back)
		if hints != "" {
			rights = append(rights, headerRight{back + headerSep + hints, navSpan{0, bw}})
		}
		return append(rights, headerRight{back, navSpan{0, bw}})
	}
	if hints != "" {
		rights = append(rights, headerRight{text: hints})
	}
	return rights
}

// backButton is the clickable mark of the way back, drawn like the key hints; empty where esc leads no level back.
func (m *Model) backButton() string {
	if !m.backShown() {
		return ""
	}
	return styles.hint.Render("[" + keyLabel(actBack) + "] " + tr(tHeaderBack))
}

// flowParts lays styled parts out one after the other in rows of at most width cells, separated by
// headerSep, and returns the rows and the width of the last one. A part wider than a row is cut.
func flowParts(parts []string, width int) ([]string, int) {
	lines := []string{""}
	used := 0
	for _, part := range parts {
		w := lipgloss.Width(part)
		if used > 0 && used+lipgloss.Width(headerSep)+w > width {
			lines, used = append(lines, ""), 0
		}
		if used > 0 {
			lines[len(lines)-1] += headerSep
			used += lipgloss.Width(headerSep)
		}
		if w > width {
			part, w = clipCells(part, width), width
		}
		lines[len(lines)-1] += part
		used += w
	}
	return lines, used
}

// fitPath is the plain path text for room cells: with the note that the file is still to be created if
// that fits, else the path alone, else the end of the path.
func fitPath(path, suffix string, room int) string {
	if lipgloss.Width(path+suffix) <= room {
		return path + suffix
	}
	return truncateLeft(path, room)
}

// configPath is the path of the configuration file, with the home directory written as "~".
func (m *Model) configPath() string {
	path := m.svc.Path()
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~" + string(filepath.Separator) + rest
		}
	}
	return path
}

// headerLogo is the Nerd Font glyph in front of the name; without that preference the name stands alone.
const headerLogo = "\uf1b2"

// headerStatus are the left parts of the header: the name and the status marks of the vault and admin.
func (m *Model) headerStatus() []string {
	name := styles.title.Render(tr(tHeaderTitle))
	if m.prefs.Icons == uiprefs.IconsNerdFont {
		name = styles.active.Render(headerLogo) + " " + name
	}
	return append([]string{name}, m.vaultSegments()...)
}

// headerHints are the keys of help and of the command line, as they are bound now.
func (m *Model) headerHints() string {
	var parts []string
	for _, h := range []struct {
		act  action
		text textID
	}{{actHelp, tHeaderHelp}, {actCommand, tHeaderCmd}} {
		if key := keyLabel(h.act); key != "" {
			parts = append(parts, styles.hint.Render("["+key+"] "+tr(h.text)))
		}
	}
	return strings.Join(parts, headerSep)
}

// navLine is the tab line: every area by number and name, the active one as an accent surface (in brackets
// without colour), and "> " in front while the tabs have the focus; the fill or the brackets and the marker,
// not the colour alone, say which one is active. A terminal too narrow for all names closes the gaps first
// and then shortens the others to their numbers; the active tab always stays whole.
func (m *Model) navLine() string { return m.navTabs().line }

// navBar is the tab line as drawn, and the cells of the tab of each area in it (a tab cut off by the edge of
// the terminal has none).
type navBar struct {
	line  string
	spans []navSpan
}

// plainTabs reports whether the active tab cannot be told by its fill: no theme colours, or a terminal that
// shows none. The brackets mark it then.
func plainTabs() bool {
	return styles.palette.accent == "" || currentProfile() == profileNone
}

func (m *Model) navTabs() navBar {
	focus := "  "
	if m.screen == screenNav && !m.navSub {
		focus = "> "
	}
	plain := plainTabs()
	var spaced, compact, short []string
	active := -1
	for s := area(0); s < areaCount; s++ {
		name := fmt.Sprintf("%d %s", int(s)+1, s.title())
		if m.prefs.Icons == uiprefs.IconsNerdFont {
			// The icon comes before the name and never replaces it.
			name = fmt.Sprintf("%d %s %s", int(s)+1, areaIcons[s], s.title())
		}
		if s == m.section.area() && m.wizard == nil {
			tab := " " + name + " "
			if plain {
				tab = "[" + name + "]"
			}
			spaced, compact, short = append(spaced, tab), append(compact, tab), append(short, tab)
			active = int(s)
			continue
		}
		spaced, compact, short = append(spaced, " "+name+" "), append(compact, name), append(short, fmt.Sprint(int(s)+1))
	}
	if m.wizard != nil {
		tab := tr(tTabSetup)
		spaced, compact, short = append(spaced, tab), append(compact, tab), append(short, tab)
		active = len(spaced) - 1
	}
	line, items := focus+strings.Join(spaced, " "), spaced
	for _, tabs := range [][]string{compact, short} {
		if lipgloss.Width(line) <= m.termWidth {
			break
		}
		line, items = focus+strings.Join(tabs, " "), tabs
	}
	bar := navBar{line: clipCells(m.tabLine(focus, items, active), m.termWidth)}
	if m.wizard == nil {
		bar.spans = spanItems(items, lipgloss.Width(focus), " ", m.termWidth)
	}
	return bar
}

// tabLine styles the tabs: the active one as an accent surface, the others dimmed; without colour the
// brackets, drawn by the caller, carry the difference and the active one is bold.
func (m *Model) tabLine(focus string, items []string, active int) string {
	var b strings.Builder
	b.WriteString(styles.active.Render(focus))
	fill := accentFill()
	for i, item := range items {
		if i > 0 {
			b.WriteString(" ")
		}
		if i == active {
			b.WriteString(fill.Render(item))
		} else {
			b.WriteString(styles.hint.Render(item))
		}
	}
	return b.String()
}
