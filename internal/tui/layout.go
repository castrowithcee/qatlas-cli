package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// headerLines are the header rows above the workspace: always one row. It starts with the name, then the
// status marks of the vault and of the admin session (none for a run with no vault configured: a warning for
// something that does not exist yet would mislead), then the banner of a newer release. The back button and
// the help and command keys stand at the right end. When the width does not suffice, the keys go first,
// then the button; a status mark that still does not fit is cut with an ellipsis rather than
// wrapped, so the header never takes a second row.
func (m *Model) headerLines() []string {
	lines, _ := m.headerLayout()
	return lines
}

// headerLayout is headerLines and the cells of the back button in the row (empty when it is not shown). The
// banner takes its room before the right blocks, which stand only where the row still has room for them.
func (m *Model) headerLayout() ([]string, navSpan) {
	width := max(m.termWidth, 1)
	sep := lipgloss.Width(headerSep)
	line, used := fitParts(m.headerStatus(), width)

	if banner := m.updateBanner(width - used - sep); banner != "" {
		line += headerSep + styles.ok.Render(banner)
	}
	line, back := placeRight(line, m.headerRights(), width)
	return []string{line}, back
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

// fitParts lays styled parts out in one row of at most width cells, separated by headerSep, and returns the
// row and its width. The part that does not fit is cut with an ellipsis and the parts after it are dropped.
func fitParts(parts []string, width int) (string, int) {
	line, used := "", 0
	for _, part := range parts {
		if used > 0 {
			if used+lipgloss.Width(headerSep)+1 > width {
				break
			}
			line += headerSep
			used += lipgloss.Width(headerSep)
		}
		part = truncateCells(part, width-used)
		line += part
		used += lipgloss.Width(part)
	}
	return line, used
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

// headerStatus are the left parts of the header: the name and the status marks of the vault and admin.
func (m *Model) headerStatus() []string {
	name := styles.title.Render(tr(tHeaderTitle))
	if icon := m.icon(iconLogo); icon != "" {
		name = styles.active.Render(icon) + " " + name
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
// not the colour alone, say which one is active. A terminal too narrow for all names closes the gaps first,
// then drops the icons, and then shortens the others to their numbers; the active tab always stays whole.
func (m *Model) navLine() string { return m.navTabs().line }

// navBar is the tab line as drawn, and the cells of the tab of each area in it (a tab cut off by the edge of
// the terminal has none).
type navBar struct {
	line  string
	spans []navSpan
}

// plainTabs reports whether the active tab cannot be told by its fill, under a profile without colour. The
// brackets mark it then.
func plainTabs() bool {
	return noColour()
}

func (m *Model) navTabs() navBar {
	focus := "  "
	if m.screen == screenNav && !m.navSub {
		focus = "> "
	}
	plain := plainTabs()
	var spaced, compact, bare, short []string
	active := -1
	for s := area(0); s < areaCount; s++ {
		name := fmt.Sprintf("%d %s", int(s)+1, s.title())
		plainName := name
		if icon := m.icon(areaIcons[s]); icon != "" {
			// The icon comes before the name and never replaces it.
			name = fmt.Sprintf("%d %s %s", int(s)+1, icon, s.title())
		}
		if s == m.section.area() && m.wizard == nil {
			tab, bareTab := " "+name+" ", " "+plainName+" "
			if plain {
				tab, bareTab = "["+name+"]", "["+plainName+"]"
			}
			spaced, compact, bare, short = append(spaced, tab), append(compact, tab), append(bare, bareTab), append(short, tab)
			active = int(s)
			continue
		}
		spaced, compact = append(spaced, " "+name+" "), append(compact, name)
		bare, short = append(bare, plainName), append(short, fmt.Sprint(int(s)+1))
	}
	if m.wizard != nil {
		tab := tr(tTabSetup)
		spaced, compact, bare, short = append(spaced, tab), append(compact, tab), append(bare, tab), append(short, tab)
		active = len(spaced) - 1
	}
	line, items := focus+strings.Join(spaced, " "), spaced
	for _, tabs := range [][]string{compact, bare, short} {
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
