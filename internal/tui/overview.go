package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The Overview is the start of the editor: how much of each kind exists, the state of the vault, and the
// next steps. It only shows state already held in memory or read the way the lists read it, and it starts no
// action of its own; enter opens the area that deals with the selected line.

// overviewItem is one selectable line of the Overview: a count or a next step, and the section it opens.
type overviewItem struct {
	text   string
	target section
}

// overviewTiles are the counts, in the order of the areas that hold them.
func (m *Model) overviewTiles() []overviewItem {
	count := func(n int) string { return fmt.Sprint(n) }
	var tokens, approvals string
	if entries, why := m.tokensReport(); why == "" {
		tokens = count(len(entries))
	} else {
		tokens = m.unavailableWord()
	}
	if report, why := m.approvalsReport(); why == "" {
		approvals = count(len(report.Open))
	} else {
		approvals = m.unavailableWord()
	}
	tile := func(name, value string, target section) overviewItem {
		return overviewItem{text: fmt.Sprintf("%-12s %s", name, value), target: target}
	}
	return []overviewItem{
		tile("Connections", count(len(m.cfg.Connections)), sectionConnections),
		tile("Services", count(len(m.cfg.Services)), sectionServices),
		tile("Credentials", count(len(m.cfg.Credentials)), sectionCredentials),
		tile("Tokens", tokens, sectionTokens),
		tile("Approvals", approvals, sectionApprovals),
	}
}

// unavailableWord says in a word why the vault cannot list tokens or approvals now.
func (m *Model) unavailableWord() string {
	if m.vaultLocked() {
		return "vault locked"
	}
	return "needs encrypted vault"
}

// overviewSteps are the next steps as selectable lines.
func (m *Model) overviewSteps() []overviewItem {
	var items []overviewItem
	for _, s := range m.nextSteps() {
		items = append(items, overviewItem{text: s.text, target: s.target})
	}
	return items
}

// overviewLines builds the lines of the view and the items the selection moves over. The steps and the
// state lines give way to the room of a small terminal before the counts do.
func (m *Model) overviewLines() (lines []string, items []overviewItem, foot string) {
	foot = m.keyHint(m.overviewKeys()) + m.notes()
	tiles := m.overviewTiles()
	steps := m.overviewSteps()
	width := m.usable(2)

	room := m.height - 1 - len(tiles) - (strings.Count(foot, "\n") + 1)
	shown := 0
	if room >= 2 {
		shown = min(len(steps), room-1)
		room -= shown + 1
	}
	// The vault and the release come before the steps in the view: they are state, the steps are advice.
	var state []string
	if v := m.vaultHeaderText(); v != "" {
		state = append(state, "Vault: "+v)
	} else {
		state = append(state, styles.hint.Render("Vault: none configured"))
	}
	if banner := m.updateBanner(m.usable(0)); banner != "" {
		state = append(state, styles.ok.Render(banner))
	}
	state = state[:min(len(state), max(room, 0))]

	row := func(i int, text string) string {
		line := "  " + truncateCells(text, width)
		if m.screen == screenList && i == m.overviewCursor {
			return styles.active.Render("> " + line[2:])
		}
		return line
	}
	lines = append(lines, styles.title.Render(m.section.title()))
	lines = append(lines, state...)
	items = append(items, tiles...)
	for i, tile := range tiles {
		lines = append(lines, row(i, tile.text))
	}
	if shown > 0 {
		lines = append(lines, styles.hint.Render("Next steps"))
		for i, step := range steps[:shown] {
			lines = append(lines, row(len(tiles)+i, step.text))
			items = append(items, step)
		}
	}
	return lines, items, foot
}

// overviewView draws the Overview.
func (m *Model) overviewView() string {
	lines, items, foot := m.overviewLines()
	if m.overviewCursor >= len(items) {
		// A step that is done, or a resize, shortened the list under the selection.
		m.overviewCursor = max(len(items)-1, 0)
		lines, _, foot = m.overviewLines()
	}
	return strings.Join(lines, "\n") + "\n" + foot
}

// overviewKeys is the key line of the Overview, with the focus on the tab line or on the lines.
func (m *Model) overviewKeys() string {
	keys := "←/→ move · down open · 1-6 open · c setup · {command} cmds · {help} help · q quit"
	if m.screen == screenList {
		keys = "up/down move · enter open · 1-6 areas · {back}/tab areas · c setup · {command} cmds · {help} help · q quit"
	}
	if hint := m.vaultLockKeyHint(); hint != "" {
		keys = strings.Replace(keys, " · {command}", " · "+hint+" · {command}", 1)
	}
	return keys
}

// updateOverview handles the keys of the Overview with the focus on its lines: up and down move over the
// counts and the steps, enter opens the area of the selected line, and up from the first line, tab, and esc
// return to the tab line.
func (m *Model) updateOverview(key tea.KeyMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	_, items, _ := m.overviewLines()
	switch key.String() {
	case "q":
		return m.quit()
	case "c":
		m.askSetupTemplate()
	case "u":
		m.askUpdate()
	case keyOf(actBack), "tab", "shift+tab":
		m.focusNav()
	case "up":
		if m.overviewCursor <= 0 {
			m.focusNav()
			return nil
		}
		m.overviewCursor--
	case "down":
		m.overviewCursor = min(m.overviewCursor+1, len(items)-1)
	case "pgup", "home":
		m.overviewCursor = 0
	case "pgdown", "end":
		m.overviewCursor = len(items) - 1
	case "enter":
		if m.overviewCursor >= 0 && m.overviewCursor < len(items) {
			return m.openSection(items[m.overviewCursor].target)
		}
	}
	return nil
}
