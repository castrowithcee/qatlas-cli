// The activity heatmap: the days of the invocation log as a grid of weeks and weekdays, one cell per local day,
// leveled by the number of invokes. The Logs section shows it for the rows its filters keep as a row per week,
// the Overview as a coloured grid of the latest weeks (heatmap.go) beside the latest invokes. The counts come from
// invokelog.DailyActivity on the lines the check read (invokelog.VerifyLines); nothing here reads a log file.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

const (
	// maxHeatWeeks bounds the weeks one grid spans, however long the chosen range is.
	maxHeatWeeks = 53
	// overviewHeatWeeks is the most weeks of the Overview's grid; overviewRecent the most invokes it lists.
	overviewHeatWeeks = 12
	overviewRecent    = 10
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

// heatWeeks keeps the latest days of days that fill at most weeks Monday-first weeks. It returns them with the
// weekday row of the first kept day and the number of weeks they span.
func heatWeeks(days []invokelog.DayActivity, weeks int) (kept []invokelog.DayActivity, offset, total int) {
	first, _ := time.ParseInLocation(invokelog.DateLayout, days[0].Date, time.Local)
	offset = (int(first.Weekday()) + 6) % 7
	total = (offset + len(days) + 6) / 7
	if total > weeks {
		// Drop the oldest weeks: the days before the Monday of the first week kept.
		skip := (total-weeks)*7 - offset
		return days[skip:], 0, weeks
	}
	return days, offset, total
}

// heatTotals is the line under a grid: the invokes, the share that succeeded, and the failed ones.
func heatTotals(days []invokelog.DayActivity) string {
	plain := func(_ lipgloss.Style, text string) string { return text }
	return heatTotalsWith(days, plain)
}

// heatTotalsStyled is heatTotals with the share that succeeded in the success colour and the failed ones in the
// failure colour; the numbers and words stay, so the line reads without colour.
func heatTotalsStyled(days []invokelog.DayActivity) string {
	return heatTotalsWith(days, func(style lipgloss.Style, text string) string { return style.Render(text) })
}

func heatTotalsWith(days []invokelog.DayActivity, paint func(lipgloss.Style, string) string) string {
	var calls, ok int
	for _, d := range days {
		calls += d.Calls
		ok += d.Succeeded()
	}
	rate, failed := "-", fmt.Sprint(calls-ok)
	if calls > 0 {
		rate = fmt.Sprintf("%d%%", ok*100/calls)
	}
	failedStyle := styles.hint
	if calls > ok {
		failedStyle = styles.fail
	}
	return tr(tHeatTotals, paint(styles.title, fmt.Sprint(calls)), paint(styles.ok, rate), paint(failedStyle, failed))
}

// heatLegendMarks explains the marks that stand in place of a level.
func heatLegendMarks() string {
	g := heatGlyphSet()
	return tr(tHeatLegend, g.outside, g.altered, g.gap)
}

// localDay is t as a local day.
func localDay(t time.Time) string { return t.In(time.Local).Format(invokelog.DateLayout) }

// retentionStart is the first local day the log still holds: a day file older than the retention window is
// removed on the next invoke (see invokelog.New). It is empty for a log that is never truncated, so no day
// lies outside it; the heatmap then reaches back as far as it draws.
func (m *Model) retentionStart(now time.Time) string {
	if m.logRetention <= 0 {
		return ""
	}
	return localDay(now.AddDate(0, 0, -m.logRetention))
}

// heatStart is the first local day a wide heatmap draws: the start of the retention window, or, for a log that
// is never truncated, as far back as the heatmap reaches at all.
func (m *Model) heatStart(now time.Time) string {
	if start := m.retentionStart(now); start != "" {
		return start
	}
	return localDay(now.AddDate(0, 0, -7*maxHeatWeeks))
}

// heatSpan is the local days the Logs heatmap draws: the selected days, or, for All, the retention window.
func (m *Model) heatSpan() (from, to string) {
	now := m.timeNow()
	from, to = m.logs.bounds()
	if m.logs.mode == logModeAll {
		from, to = m.heatStart(now), localDay(now)
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

// setHeat shows the rows (false) or the heatmap of their days (true), the tabs of the bar. A single
// day shows nothing worth a grid, so the heatmap widens it to the retention window; the filters and the other
// modes stay as they are.
func (m *Model) setHeat(on bool) tea.Cmd {
	lv := m.logs
	lv.heat, lv.stats, lv.sessions = on, false, false
	if !on || lv.mode != logModeDay {
		return nil
	}
	now := m.timeNow()
	lv.mode, lv.from, lv.to = logModeRange, m.heatStart(now), localDay(now)
	return m.loadLogs()
}

// logHeatLines draws the heatmap of the rows shown as at most room lines: the totals, the weekday header with a
// row per week, and the legend. A short room keeps the latest weeks and the totals before the legend.
func (m *Model) logHeatLines(room int) string {
	from, to := m.heatSpan()
	today := localDay(m.timeNow())
	days := invokelog.DailyActivity(m.logs.heatDays(), from, to)
	width := m.logRoom() - 2
	legend := []string{heatSquareLegend() + styles.hint.Render(tr(tHeatLevels)), styles.hint.Render(heatLegendMarks())}
	// The totals and the weekday header stand above the weeks, the legend below them.
	weeks := min(maxHeatWeeks, room-2-len(legend))
	if weeks < 1 {
		legend = legend[:0]
		weeks = min(maxHeatWeeks, room-2)
	}
	grid, drawn := heatWeekRows(days, today, m.retentionStart(m.timeNow()), width, weeks)
	totals := heatTotalsStyled(drawn)
	if len(drawn) > 0 {
		totals += styles.hint.Render(tr(tHeatSpan, drawn[0].Date, min(drawn[len(drawn)-1].Date, today)))
	}
	lines := append([]string{totals}, grid...)
	if len(grid) > 0 {
		lines = append(lines, legend...)
	}
	var b strings.Builder
	for _, line := range lines[:min(len(lines), max(room, 0))] {
		b.WriteString("  " + truncateCells(line, width) + "\n")
	}
	return m.logInside(b.String())
}

// overviewActivity is what the Overview shows of the log: the latest weeks and the latest invokes. It is
// read once at the start and again when the vault is locked or unlocked, never on the event loop.
type overviewActivity struct {
	loaded bool
	days   []invokelog.DayActivity
	recent []invokelog.Entry
}

// hasActivity reports whether the log read holds a call, or an entry the check flagged, in the days shown.
func (a overviewActivity) hasActivity() bool {
	for _, d := range a.days {
		if d.Calls > 0 || d.Altered || d.Gap {
			return true
		}
	}
	return a.loaded && len(a.recent) > 0
}

// overviewActivityMsg carries the Overview's activity back into the event loop.
type overviewActivityMsg struct {
	days   []invokelog.DayActivity
	recent []invokelog.Entry
}

// loadOverviewActivity reads and checks the latest weeks as a command. A log that cannot be read leaves the
// Overview without the block.
func (m *Model) loadOverviewActivity() tea.Cmd {
	m.refreshLogRetention()
	v := m.logVault()
	now := m.timeNow()
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
