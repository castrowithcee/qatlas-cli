package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// The Overview is the start of the editor: how much of each kind exists, the state of the vault, and the
// next steps. It only shows state already held in memory or read the way the lists read it, and it starts no
// action of its own; enter opens the area that deals with the selected line.

// overviewItem is one selectable item of the Overview: a count or a next step, and the section it opens. A
// count also carries its label and value for the card; a value that is a word (the vault cannot say) is dim.
type overviewItem struct {
	text   string
	target section
	label  string
	value  string
	dim    bool
}

// overviewHit is the cells of the Overview that select one item: the rectangle [x0, x1) by [y0, y1) of the
// workspace.
type overviewHit struct{ x0, y0, x1, y1 int }

const (
	// overviewWide is the width from which the recent calls and the heatmap stand side by side.
	overviewWide = 120
	// overviewCardHeight is the height of a card: the label in the top border, one value line.
	overviewCardHeight = 3
	// overviewSidePanel is the outer width of the heatmap panel beside the recent calls.
	overviewSidePanel = 44
	// overviewStackedRecent is the most calls the recent calls list when it stands above the heatmap.
	overviewStackedRecent = 5
)

// overviewIcons are the Nerd Font glyphs (Font Awesome range) before the label of a count when the icons
// preference is nerdfont: link, cube, key, lock, check.
var overviewIcons = map[section]string{
	sectionConnections: "\uf0c1",
	sectionServices:    "\uf1b2",
	sectionCredentials: "\uf084",
	sectionTokens:      "\uf023",
	sectionApprovals:   "\uf058",
}

// overviewTiles are the counts, in the order of the areas that hold them.
func (m *Model) overviewTiles() []overviewItem {
	count := func(n int) string { return fmt.Sprint(n) }
	var tokens, approvals string
	var tokensDim, approvalsDim bool
	if entries, why := m.tokensReport(); why == "" {
		tokens = count(len(entries))
	} else {
		tokens, tokensDim = m.unavailableWord(), true
	}
	if report, why := m.approvalsReport(); why == "" {
		approvals = count(len(report.Open))
	} else {
		approvals, approvalsDim = m.unavailableWord(), true
	}
	tile := func(target section, value string, dim bool) overviewItem {
		name := target.title()
		return overviewItem{text: fmt.Sprintf("%-12s %s", name, value), target: target, label: name, value: value, dim: dim}
	}
	return []overviewItem{
		tile(sectionConnections, count(len(m.cfg.Connections)), false),
		tile(sectionServices, count(len(m.cfg.Services)), false),
		tile(sectionCredentials, count(len(m.cfg.Credentials)), false),
		tile(sectionTokens, tokens, tokensDim),
		tile(sectionApprovals, approvals, approvalsDim),
	}
}

// unavailableWord says in a word why the vault cannot list tokens or approvals now.
func (m *Model) unavailableWord() string {
	if m.vaultLocked() {
		return tr(tOverviewVaultLocked)
	}
	return tr(tOverviewNeedsVault)
}

// overviewSteps are the next steps as selectable lines.
func (m *Model) overviewSteps() []overviewItem {
	var items []overviewItem
	for _, s := range m.nextSteps() {
		items = append(items, overviewItem{text: s.text, target: s.target})
	}
	return items
}

// germanWeekdays and germanMonths are the short names of the date in the header, as German writes them;
// English comes from the time package.
var (
	germanWeekdays = [...]string{"So.", "Mo.", "Di.", "Mi.", "Do.", "Fr.", "Sa."}
	germanMonths   = [...]string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."}
)

// overviewStamp is the date and time of the header in the form of the language.
func overviewStamp(t time.Time) string {
	t = t.In(time.Local)
	if uiLang == i18n.German {
		return fmt.Sprintf("%s, %d. %s %d  %s", germanWeekdays[t.Weekday()], t.Day(), germanMonths[t.Month()-1], t.Year(),
			t.Format("15:04"))
	}
	return t.Format("Mon, 2 Jan 2006  15:04")
}

// overviewHeader is the title with the date at the right edge and, when there is room for it, the subtitle.
func (m *Model) overviewHeader(width, n int) []string {
	title := styles.title.Render(m.section.title())
	stamp := styles.hint.Render(overviewStamp(m.timeNow()))
	line := title
	if gap := width - lipgloss.Width(title) - lipgloss.Width(stamp); gap >= 2 {
		line += strings.Repeat(" ", gap) + stamp
	}
	lines := []string{line}
	if n > 1 {
		lines = append(lines, styles.hint.Render(truncateCells(tr(tOverviewSubtitle), width)))
	}
	return lines
}

// overviewCardLabel is the title of a card: the icon with the preference, and "> " in front of the
// selected one where it fits, so the selection shows without colour as well.
func (m *Model) overviewCardLabel(item overviewItem, inner int, selected bool) string {
	label := item.label
	if m.prefs.Icons == uiprefs.IconsNerdFont {
		label = overviewIcons[item.target] + " " + label
	}
	if selected {
		if lipgloss.Width(label)+2 <= inner {
			label = "> " + label
		} else {
			label = ">" + label
		}
	}
	return truncateCells(label, inner)
}

// overviewCards draws the counts as framed cards in as few rows as their widest text allows, and returns the
// lines and the cells of each card. A row is as wide as the workspace; the cards of a row share it.
func (m *Model) overviewCards(tiles []overviewItem, width int) (lines []string, hits []overviewHit, ok bool) {
	need := 0
	for _, t := range tiles {
		label := lipgloss.Width(t.label)
		if m.prefs.Icons == uiprefs.IconsNerdFont {
			label += 2
		}
		// The title sits in the top border with a space on each side, so it needs one cell more than the value.
		need = max(need, label+1, lipgloss.Width(t.value))
	}
	per := min(len(tiles), (width+1)/(need+4+1))
	if per < 1 {
		return nil, nil, false
	}
	rows := (len(tiles) + per - 1) / per
	per = (len(tiles) + rows - 1) / rows
	space := width - (per - 1)
	for r := 0; r < rows; r++ {
		var cards [][]string
		x := 0
		for c := 0; c < per && r*per+c < len(tiles); c++ {
			i := r*per + c
			w := space / per
			if c < space%per {
				w++
			}
			selected := m.screen == screenList && i == m.overviewCursor
			inner := w - 4
			value := styles.title.Render(truncateCells(tiles[i].value, inner))
			if tiles[i].dim {
				value = styles.hint.Render(truncateCells(tiles[i].value, inner))
			}
			cards = append(cards, panel(m.overviewCardLabel(tiles[i], inner, selected), []string{value}, inner, 1,
				panelOpts{focused: selected}))
			hits = append(hits, overviewHit{x, r * overviewCardHeight, x + w, (r + 1) * overviewCardHeight})
			x += w + 1
		}
		for line := 0; line < overviewCardHeight; line++ {
			parts := make([]string, len(cards))
			for c := range cards {
				parts[c] = cards[c][line]
			}
			lines = append(lines, strings.Join(parts, " "))
		}
	}
	return lines, hits, true
}

// recentLines are the latest invokes as table rows of inner cells: time, mark, tool, connection, result, and
// the duration at the right edge. The mark and the result word say the outcome without colour.
func (m *Model) recentLines(entries []invokelog.Entry, inner, rows int) []string {
	entries = entries[:min(len(entries), rows)]
	timeW := 5
	if inner >= 64 {
		timeW = 11
	}
	const durW, gap = 6, 2
	resW := 0
	for _, e := range entries {
		resW = max(resW, lipgloss.Width(m.recentResult(e)))
	}
	resW = min(resW, 15)
	rest := inner - timeW - 1 - durW - resW - 4*gap
	connW := 0
	if rest >= 28 {
		// Connection and tool share what the fixed columns leave, the tool the larger part.
		connW = rest * 2 / 5
		rest -= connW + gap
	}
	toolW := max(rest, 1)
	today := localDay(m.timeNow())
	pad := func(text string, w int) string { return padLine(truncateCells(text, w), w) }
	var lines []string
	for _, e := range entries {
		when := e.Time.In(time.Local).Format("15:04")
		if timeW > 5 && localDay(e.Time) != today {
			when = e.Time.In(time.Local).Format("01-02 15:04")
		}
		mark, result := styles.ok.Render("✓"), styles.ok
		if e.Result != "success" {
			mark, result = styles.fail.Render("✗"), styles.fail
		}
		sep := strings.Repeat(" ", gap)
		line := styles.hint.Render(fmt.Sprintf("%*s", timeW, when)) + sep + mark + sep + pad(orDash(m.logText(e.Operation)), toolW)
		if connW > 0 {
			line += sep + pad(orDash(m.logText(e.Connection)), connW)
		}
		dur := fmt.Sprintf("%*s", durW, formatDuration(e.DurationMS))
		line += sep + result.Render(pad(m.recentResult(e), resW)) + sep + styles.hint.Render(dur)
		lines = append(lines, line)
	}
	return lines
}

// recentResult is the result word of an invoke: "ok" for a success, else the result as logged.
func (m *Model) recentResult(e invokelog.Entry) string {
	if e.Result == "success" {
		return "ok"
	}
	return orDash(m.logText(e.Result))
}

// heatPanelLines are the heatmap with its totals and, with room for it, its legend, in at most inner cells and
// rows lines; empty when the log holds no days or the panel is too narrow for one week.
func (m *Model) heatPanelLines(inner, rows int) (lines []string, weeks int) {
	act := m.overviewAct
	if !act.hasActivity() {
		return nil, 0
	}
	now := m.timeNow()
	grid, drawn := heatSquares(act.days, localDay(now), m.retentionStart(now), inner, overviewHeatWeeks)
	if len(grid) == 0 || rows < len(grid)+1 {
		return nil, 0
	}
	lines = append(grid, styles.hint.Render(truncateCells(heatTotals(drawn), inner)))
	if rows > len(lines) {
		lines = append(lines, heatSquareLegend())
	}
	return lines, (len(drawn) + 6) / 7
}

// overviewLines builds the lines of the view and the items the selection moves over. A small terminal gives
// up the parts in this order: the subtitle, the cards (they become text lines), the vault line, the steps, the
// heatmap, and the recent calls; the counts and the steps stay selectable throughout.
func (m *Model) overviewLines() (lines []string, items []overviewItem, foot string) {
	foot = m.keyHint(m.overviewKeys()) + m.notes()
	tiles := m.overviewTiles()
	steps := m.overviewSteps()
	width := m.usable(0)
	room := m.height - (strings.Count(foot, "\n") + 1)

	var hits []overviewHit
	selected := func(i int) bool { return m.screen == screenList && i == m.overviewCursor }
	// textRow is a selectable line of text, the selected one marked with "> ".
	textRow := func(i int, text string) string {
		hits = append(hits, overviewHit{0, len(lines), width, len(lines) + 1})
		if selected(i) {
			return styles.activeRow.Render(padLine("> "+truncateCells(text, width-2), width))
		}
		return "  " + truncateCells(text, width-2)
	}

	head := 1
	if room >= 11 {
		head = 2
	}
	lines = m.overviewHeader(width, head)
	room -= head

	cards, cardHits, carded := m.overviewCards(tiles, width)
	if carded && room-len(cards) >= 4 {
		for _, h := range cardHits {
			h.y0, h.y1 = h.y0+len(lines), h.y1+len(lines)
			hits = append(hits, h)
		}
		lines = append(lines, cards...)
		room -= len(cards)
	} else {
		for i, tile := range tiles {
			lines = append(lines, textRow(i, tile.text))
		}
		room -= len(tiles)
	}
	items = append(items, tiles...)

	// Steps come before the state lines: they can be selected, the state only informs.
	panelSteps, shown := false, 0
	switch {
	case len(steps) > 0 && room >= 3:
		panelSteps, shown = true, min(len(steps), room-2)
		room -= shown + 2
	case len(steps) > 0 && room >= 2:
		shown = min(len(steps), room-1)
		room -= shown + 1
	}
	// The header already shows the vault state; only a missing vault is told here, since the header is
	// silent about it. The state lines start with the running version, which is shown even without a
	// known update, and the config path.
	state := []string{styles.hint.Render(m.overviewStateLine(width))}
	vaultNone := m.vaultHeaderText() == ""
	if banner := m.updateBanner(m.usable(0)); banner != "" {
		state = append(state, styles.ok.Render(banner))
	}
	state = state[:min(len(state), max(room, 0))]
	room -= len(state)
	for _, line := range state {
		lines = append(lines, truncateCells(line, width))
	}

	if vaultNone && room >= 3 {
		lines = append(lines, panel(sectionVault.title(), []string{styles.hint.Render(tr(tOverviewVaultNone))}, width-4, 1,
			panelOpts{})...)
		room -= 3
	}

	if shown > 0 {
		items = append(items, steps[:shown]...)
		if panelSteps {
			var body []string
			for i := 0; i < shown; i++ {
				idx := len(tiles) + i
				hits = append(hits, overviewHit{0, len(lines) + 1 + i, width, len(lines) + 2 + i})
				text := "  " + truncateCells(steps[i].text, width-6)
				if selected(idx) {
					text = styles.activeRow.Render(padLine("> "+truncateCells(steps[i].text, width-6), width-4))
				}
				body = append(body, text)
			}
			lines = append(lines, panel(tr(tOverviewNextSteps), body, width-4, shown, panelOpts{})...)
		} else {
			lines = append(lines, styles.hint.Render(tr(tOverviewNextSteps)))
			for i := 0; i < shown; i++ {
				lines = append(lines, textRow(len(tiles)+i, steps[i].text))
			}
		}
	}
	lines = append(lines, m.overviewActivityPanels(width, room)...)
	m.overviewHits = hits
	return lines, items, foot
}

// overviewActivityPanels are the Recent activity and Activity panels for room lines: side by side from
// overviewWide cells on when both fit whole, else the recent calls above the heatmap, each only if it fits.
func (m *Model) overviewActivityPanels(width, room int) []string {
	act := m.overviewAct
	var recentRows []invokelog.Entry
	if act.loaded {
		recentRows = act.recent
	}
	hint := func() []string { return []string{styles.hint.Render(tr(tOverviewNoCalls))} }
	heatTitle := func(weeks int) string {
		if weeks > 1 {
			return tr(tOverviewActivityWeeks, weeks)
		}
		return tr(tOverviewActivity)
	}
	var lines []string
	if width >= overviewWide && room >= 11 {
		rows := min(room-2, 10)
		inner := overviewSidePanel - 4
		heat, weeks := m.heatPanelLines(inner, rows)
		if heat == nil {
			heat = hint()
		}
		recent := m.recentLines(recentRows, width-overviewSidePanel-1-4, rows)
		if recent == nil {
			recent = hint()
		}
		left := panel(tr(tOverviewRecent), recent, width-overviewSidePanel-1-4, rows, panelOpts{})
		right := panel(heatTitle(weeks), heat, inner, rows, panelOpts{})
		for i := range left {
			lines = append(lines, left[i]+" "+right[i])
		}
		return lines
	}
	if room >= 3 {
		rows := max(min(len(recentRows), overviewStackedRecent, room-2), 1)
		recent := m.recentLines(recentRows, width-4, rows)
		if recent == nil {
			recent = hint()
		}
		lines = append(lines, panel(tr(tOverviewRecent), recent, width-4, rows, panelOpts{})...)
		room -= rows + 2
	}
	if room >= 3 {
		heat, weeks := m.heatPanelLines(width-4, min(room-2, 10))
		if heat == nil && !act.hasActivity() {
			heat = hint()
		}
		if heat != nil {
			lines = append(lines, panel(heatTitle(weeks), heat, width-4, len(heat), panelOpts{})...)
		}
	}
	return lines
}

// overviewView draws the Overview.
func (m *Model) overviewView() string {
	lines, items, foot := m.overviewLines()
	if m.overviewCursor >= len(items) {
		// A step that is done, or a resize, shortened the list under the selection.
		m.overviewCursor = max(len(items)-1, 0)
		lines, _, foot = m.overviewLines()
	}
	m.hitOverview()
	top := m.workspaceTop()
	m.addZone(0, top, m.usable(0), top+m.height-(strings.Count(foot, "\n")+1), m.zoneRows)
	return strings.Join(lines, "\n") + "\n" + foot
}

// overviewKeys is the key line of the Overview, with the focus on the tab line or on the lines.
func (m *Model) overviewKeys() string {
	keys := "←/→ move · down open · 1-6 open · g setup · {command} cmds · {help} help · q quit"
	if m.screen == screenList {
		keys = "up/down move · enter open · 1-6 areas · {back}/tab areas · g setup · {command} cmds · {help} help · q quit"
	}
	if hint := m.vaultLockKeyHint(); hint != "" {
		keys = strings.Replace(keys, " · {command}", " · "+hint+" · {command}", 1)
	}
	return keys
}

// updateOverview handles the keys of the Overview with the focus on its lines: up and down move over the
// counts and the steps, enter opens the area of the selected line, and up from the first line, tab, and esc
// return to the tab line.
func (m *Model) updateOverview(key tea.KeyPressMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	if m.setupUpdateKey(key) {
		return nil
	}
	_, items, _ := m.overviewLines()
	switch {
	case keyIs(key, actQuit):
		return m.quit()
	case keyIs(key, actBack) || keyIs(key, actNext) || keyIs(key, actPrev):
		m.focusNav()
	case keyIs(key, actUp):
		if m.overviewCursor <= 0 {
			m.focusNav()
			return nil
		}
		m.overviewCursor--
	case keyIs(key, actDown):
		m.overviewCursor = min(m.overviewCursor+1, len(items)-1)
	case keyIs(key, actPageUp) || keyIs(key, actHome):
		m.overviewCursor = 0
	case keyIs(key, actPageDown) || keyIs(key, actEnd):
		m.overviewCursor = len(items) - 1
	case keyIs(key, actEnter):
		if m.overviewCursor >= 0 && m.overviewCursor < len(items) {
			return m.openSection(items[m.overviewCursor].target)
		}
	}
	return nil
}

// setupUpdateKey handles g (guided setup) and u (update), which mean the same on the Overview, the tab line
// and every list. It reports whether the key was one of them.
func (m *Model) setupUpdateKey(key tea.KeyPressMsg) bool {
	switch {
	case keyIs(key, actSetup):
		m.askSetupTemplate()
	case keyIs(key, actUpdate):
		m.askUpdate()
	default:
		return false
	}
	return true
}

// overviewVersionLine is the dimmed state line naming the running version.
func (m *Model) overviewVersionLine() string {
	return "qatlas " + m.runningVersion()
}

// overviewPathRoom is the least room the config path gets beside the version; with less it is left out.
const overviewPathRoom = 8

// overviewStateLine is the version line followed by the config path, cut at its start to the room left, with
// the note that the file is still to be created when that fits too.
func (m *Model) overviewStateLine(width int) string {
	line := m.overviewVersionLine()
	suffix := ""
	if !m.configExists {
		suffix = tr(tHeaderCreated)
	}
	room := width - lipgloss.Width(line) - lipgloss.Width(headerSep)
	if room < overviewPathRoom {
		return line
	}
	return line + headerSep + fitPath(m.configPath(), suffix, room)
}

// runningVersion is the version this editor runs; models built without one report dev.
func (m *Model) runningVersion() string {
	if m.version == "" {
		return "dev"
	}
	return m.version
}
