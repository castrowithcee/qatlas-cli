// The activity heatmap: the days of the invocation log as a grid of weekdays by weeks, one cell per local day,
// leveled by the number of invokes. The Logs section shows it for the rows its filters keep, the Overview as a
// small grid of the latest weeks with the latest invokes. The counts come from invokelog.DailyActivity on the
// lines the check read (invokelog.VerifyLines); nothing here reads a log file.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

const (
	// maxHeatWeeks bounds the weeks one grid spans, however long the chosen range is.
	maxHeatWeeks = 53
	// overviewHeatWeeks is the width of the Overview's grid; overviewRecent the invokes it lists.
	overviewHeatWeeks = 12
	overviewRecent    = 5
)

// heatGlyphs are the cells of a grid. Every state has a glyph of its own, so the colour only repeats what
// the glyph says.
type heatGlyphs struct {
	zero    string
	levels  [invokelog.ActivityLevels]string
	outside string
	altered string
	gap     string
}

// heatGlyphSet is the set the terminal can draw: block characters where the locale is UTF-8, else ASCII.
func heatGlyphSet() heatGlyphs {
	if heatASCII() {
		return heatGlyphs{zero: ".", levels: [...]string{"-", "=", "+", "#"}, outside: "x", altered: "!", gap: "~"}
	}
	return heatGlyphs{zero: "·", levels: [...]string{"░", "▒", "▓", "█"}, outside: "×", altered: "✗", gap: "…"}
}

// heatASCII reports whether the terminal is not known to draw block characters: a dumb terminal, or a
// locale that names a character set other than UTF-8. An unset locale counts as UTF-8.
var heatASCII = func() bool {
	if os.Getenv("TERM") == "dumb" {
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(os.Getenv(name)); v != "" {
			return !strings.Contains(v, "utf-8") && !strings.Contains(v, "utf8")
		}
	}
	return false
}

// cell is the glyph and style of one day. A day the check flagged shows that before its level.
func (g heatGlyphs) cell(d invokelog.DayActivity, level int, retentionStart string) (string, lipgloss.Style) {
	switch {
	case d.Altered:
		return g.altered, styles.fail
	case d.Gap:
		return g.gap, styles.warning
	case d.Calls == 0 && d.Date < retentionStart:
		return g.outside, styles.hint
	case level == 0:
		return g.zero, styles.hint
	}
	return g.levels[level-1], styles.ok
}

// heatWeekdayNames and heatMonthNames are the short names of the weekdays (Monday first) and of the months, as the
// language of the editor writes them; the grid reserves three cells for each.
func heatWeekdayNames() []string { return strings.Split(tr(tHeatWeekdays), ",") }
func heatMonthNames() []string   { return strings.Split(tr(tHeatMonths), ",") }

// heatWeekdays are the English names of the weekdays, which the catalog holds.
var heatWeekdays = strings.Split(textEN[tHeatWeekdays], ",")

// heatGrid draws days (consecutive local days) as seven weekday rows of weeks, Monday first, behind a month
// header when months is set, at most as wide as width. It keeps the latest weeks that fit and returns the days
// it drew. A day after today has no cell.
func heatGrid(days []invokelog.DayActivity, today, retentionStart string, width, maxWeeks int, months bool) ([]string, []invokelog.DayActivity) {
	if len(days) == 0 {
		return nil, nil
	}
	const label = 4
	weeks := min(maxWeeks, (width-label)/2)
	if weeks < 1 {
		return nil, nil
	}
	first, _ := time.ParseInLocation(invokelog.DateLayout, days[0].Date, time.Local)
	offset := (int(first.Weekday()) + 6) % 7
	total := (offset + len(days) + 6) / 7
	if total > weeks {
		// Drop the oldest weeks: the days before the Monday of the first week kept.
		skip := (total-weeks)*7 - offset
		days = days[skip:]
		offset = 0
		total = weeks
	}
	levels := invokelog.ActivityLevel(days)
	g := heatGlyphSet()
	weekdays, monthNames := heatWeekdayNames(), heatMonthNames()
	rows := make([][]string, 7)
	for r := range rows {
		rows[r] = make([]string, total)
		for c := range rows[r] {
			rows[r][c] = " "
		}
	}
	header := make([]byte, 0, label+2*total)
	header = append(header, strings.Repeat(" ", label)...)
	lastMonth := ""
	for i, d := range days {
		day, _ := time.ParseInLocation(invokelog.DateLayout, d.Date, time.Local)
		col, row := (offset+i)/7, (offset+i)%7
		if d.Date <= today {
			glyph, style := g.cell(d, levels[i], retentionStart)
			rows[row][col] = style.Render(glyph)
		}
		if months && row == 0 {
			if month := monthNames[day.Month()-1]; month != lastMonth && len(header) <= label+2*col {
				header = append(header, strings.Repeat(" ", label+2*col-len(header))...)
				header = append(header, month...)
				lastMonth = month
			}
		}
	}
	var lines []string
	if months {
		lines = append(lines, styles.hint.Render(strings.TrimRight(string(header), " ")))
	}
	for r := range rows {
		lines = append(lines, styles.hint.Render(fmt.Sprintf("%-3s", weekdays[r]))+" "+strings.Join(rows[r], " "))
	}
	return lines, days
}

// heatTotals is the line under a grid: the invokes, the share that succeeded, and the failed ones.
func heatTotals(days []invokelog.DayActivity) string {
	var calls, ok int
	for _, d := range days {
		calls += d.Calls
		ok += d.Succeeded()
	}
	rate := "-"
	if calls > 0 {
		rate = fmt.Sprintf("%d%%", ok*100/calls)
	}
	return tr(tHeatTotals, calls, rate, calls-ok)
}

// heatLegend are the two lines that explain the cells.
func heatLegend() []string {
	g := heatGlyphSet()
	return []string{
		tr(tOverviewLess) + g.zero + strings.Join(g.levels[:], "") + tr(tOverviewMore) + tr(tHeatLevels),
		tr(tHeatLegend, g.outside, g.altered, g.gap),
	}
}

// localDay is t as a local day.
func localDay(t time.Time) string { return t.In(time.Local).Format(invokelog.DateLayout) }

// retentionStart is the first local day the log still holds: a day file older than the retention window is
// removed on the next invoke (see invokelog.New).
func (m *Model) retentionStart(now time.Time) string {
	return localDay(now.AddDate(0, 0, -m.cfg.LogRetentionDays()))
}

// heatSpan is the local days the Logs heatmap draws: the selected days, or, for All, the retention window.
func (m *Model) heatSpan() (from, to string) {
	now := time.Now()
	from, to = m.logs.bounds()
	if m.logs.mode == logModeAll {
		from, to = m.retentionStart(now), localDay(now)
	}
	if last, err := time.ParseInLocation(invokelog.DateLayout, to, time.Local); err == nil {
		if oldest := localDay(last.AddDate(0, 0, -7*maxHeatWeeks)); from < oldest {
			from = oldest
		}
	}
	return from, to
}

// heatDays are the lines the filters and the search of the Logs section keep, in file order.
func (lv *logView) heatDays() []invokelog.DayLines {
	var out []invokelog.DayLines
	for _, name := range lv.list.matches {
		row := lv.row(name)
		if row == nil {
			continue
		}
		if n := len(out); n == 0 || out[n-1].Date != row.date {
			out = append(out, invokelog.DayLines{Date: row.date})
		}
		out[len(out)-1].Lines = append(out[len(out)-1].Lines, row.line)
	}
	return out
}

// setHeat shows the rows (false) or the heatmap of their days (true), the View field of the bar. A single
// day shows nothing worth a grid, so the heatmap widens it to the retention window; the filters and the other
// modes stay as they are.
func (m *Model) setHeat(on bool) tea.Cmd {
	lv := m.logs
	lv.heat, lv.stats, lv.sessions = on, false, false
	if !on || lv.mode != logModeDay {
		return nil
	}
	now := time.Now()
	lv.mode, lv.from, lv.to = logModeRange, m.retentionStart(now), localDay(now)
	return m.loadLogs()
}

// logHeatLines draws the heatmap of the rows shown as at most room lines, the totals first so that the
// smallest terminal keeps them.
func (m *Model) logHeatLines(room int) string {
	from, to := m.heatSpan()
	today := localDay(time.Now())
	days := invokelog.DailyActivity(m.logs.heatDays(), from, to)
	width := m.usable(2)
	grid, drawn := heatGrid(days, today, m.retentionStart(time.Now()), width, maxHeatWeeks, true)
	lines := []string{heatTotals(drawn)}
	if len(drawn) > 0 {
		lines[0] += tr(tHeatSpan, drawn[0].Date, min(drawn[len(drawn)-1].Date, today))
	}
	lines = append(lines, grid...)
	lines = append(lines, heatLegend()...)
	var b strings.Builder
	for _, line := range lines[:min(len(lines), max(room, 0))] {
		b.WriteString("  " + truncateCells(line, width) + "\n")
	}
	return b.String()
}

// overviewActivity is what the Overview shows of the log: the latest weeks and the latest invokes. It is
// read once at the start and again when the vault is locked or unlocked, never on the event loop.
type overviewActivity struct {
	loaded bool
	days   []invokelog.DayActivity
	recent []invokelog.Entry
}

// overviewActivityMsg carries the Overview's activity back into the event loop.
type overviewActivityMsg struct {
	days   []invokelog.DayActivity
	recent []invokelog.Entry
}

// loadOverviewActivity reads and checks the latest weeks as a command. A log that cannot be read leaves the
// Overview without the block.
func (m *Model) loadOverviewActivity() tea.Cmd {
	v := m.logVault()
	now := time.Now()
	from := localDay(now.AddDate(0, 0, -7*overviewHeatWeeks))
	utcFrom, utcTo := invokelog.UTCFileBounds(from, localDay(now))
	return func() tea.Msg {
		checker, _, done := logChecker(context.Background(), v)
		defer done()
		lines, err := invokelog.VerifyLines(v.Dir(), checker, utcFrom, utcTo)
		if err != nil && checker != nil {
			lines, err = invokelog.VerifyLines(v.Dir(), nil, utcFrom, utcTo)
		}
		if err != nil {
			return overviewActivityMsg{}
		}
		msg := overviewActivityMsg{days: invokelog.DailyActivity(lines, from, localDay(now))}
		entries := invokelog.LocalDayEntries(lines, from, localDay(now))
		for i := len(entries) - 1; i >= 0 && len(msg.recent) < overviewRecent; i-- {
			if invokelog.IsInvoke(entries[i]) {
				msg.recent = append(msg.recent, entries[i])
			}
		}
		return msg
	}
}

func (m *Model) handleOverviewActivity(msg overviewActivityMsg) {
	m.overviewAct = overviewActivity{loaded: true, days: msg.days, recent: msg.recent}
}

// overviewActivityLines are the grid and the latest invokes for room lines; each part comes whole or not at
// all, the grid first.
func (m *Model) overviewActivityLines(room, width int) []string {
	act := m.overviewAct
	if !act.loaded || len(act.days) == 0 {
		return nil
	}
	now := time.Now()
	grid, drawn := heatGrid(act.days, localDay(now), m.retentionStart(now), width, overviewHeatWeeks, false)
	var lines []string
	if len(grid) > 0 && room >= len(grid)+2 {
		lines = append(lines, "  "+styles.hint.Render(tr(tOverviewActivity))+"  "+heatTotals(drawn))
		for _, line := range grid {
			lines = append(lines, "  "+line)
		}
		g := heatGlyphSet()
		lines = append(lines, "  "+styles.hint.Render(tr(tOverviewLess)+g.zero+strings.Join(g.levels[:], "")+tr(tOverviewMore)))
		room -= len(grid) + 2
	}
	if len(act.recent) > 0 && room >= 2 {
		lines = append(lines, "  "+styles.hint.Render(tr(tOverviewLatestCalls)))
		for _, e := range act.recent[:min(len(act.recent), room-1)] {
			lines = append(lines, "  "+truncateCells(m.recentCallText(e), width))
		}
	}
	return lines
}

// recentCallText is one latest invoke: time, tool, connection, result, and duration.
func (m *Model) recentCallText(e invokelog.Entry) string {
	result := orDash(m.logText(e.Result))
	if e.Result == "success" {
		result = "ok"
	}
	return fmt.Sprintf("%s  %s  %s  %s  %s", logTableTime(e.Time, true), orDash(m.logText(e.Operation)),
		orDash(m.logText(e.Connection)), result, formatDuration(e.DurationMS))
}
