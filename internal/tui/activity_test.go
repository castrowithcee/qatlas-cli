package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// activityCall is one synthetic invoke: days before today at noon local time.
type activityCall struct {
	daysAgo int
	op      string
	result  string
}

// writeActivityLog writes unsigned entries with an intact hash chain at noon of their local days.
func writeActivityLog(t *testing.T, f *logFixture, calls []activityCall) {
	t.Helper()
	mustNoError(t, os.MkdirAll(f.logsDir(), 0o700))
	now := time.Now()
	previous := sha256.Sum256(nil)
	for i, c := range calls {
		day := now.AddDate(0, 0, -c.daysAgo)
		when := time.Date(day.Year(), day.Month(), day.Day(), 12, i%50, 0, 0, time.Local)
		e := invokelog.Entry{Seq: uint64(i + 1), Time: when, Path: "cli", Operation: c.op, Connection: "wiki-main",
			Effect: "read", Result: c.result, DurationMS: 25, PrevHash: hex.EncodeToString(previous[:])}
		raw, err := json.Marshal(e)
		mustNoError(t, err)
		name := filepath.Join(f.logsDir(), when.UTC().Format(logDateLayout)+".jsonl")
		file, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		mustNoError(t, err)
		_, err = file.Write(append(raw, '\n'))
		mustNoError(t, err)
		mustNoError(t, file.Close())
		previous = sha256.Sum256(raw)
	}
}

// repeatCalls is n successful calls of op, daysAgo days back.
func repeatCalls(daysAgo, n int, op, result string) []activityCall {
	out := make([]activityCall, n)
	for i := range out {
		out[i] = activityCall{daysAgo, op, result}
	}
	return out
}

// activityLog is 1, 2, 4, and 8 calls on the four days before today; the busiest day has failed calls.
func activityLog(t *testing.T) *logFixture {
	f := newLogFixture(t)
	var calls []activityCall
	calls = append(calls, repeatCalls(8, 1, "wiki.pages.list", "success")...)
	calls = append(calls, repeatCalls(4, 2, "wiki.pages.list", "success")...)
	calls = append(calls, repeatCalls(3, 4, "wiki.pages.read", "success")...)
	calls = append(calls, repeatCalls(2, 6, "wiki.pages.read", "success")...)
	calls = append(calls, repeatCalls(2, 2, "wiki.pages.create", "denied")...)
	calls = append(calls, repeatCalls(1, 3, "wiki.pages.list", "success")...)
	writeActivityLog(t, f, calls)
	return f
}

// heatRows are the weekday rows of a view: the grid without the header, the totals, and the legend.
func heatRows(view string) string {
	var rows []string
	for _, line := range strings.Split(view, "\n") {
		for _, day := range heatWeekdays {
			if strings.HasPrefix(strings.TrimLeft(line, " "), day+" ") {
				rows = append(rows, line)
			}
		}
	}
	return strings.Join(rows, "\n")
}

// heatModel opens the Logs section of f over the last 20 days and switches to the heatmap.
func heatModel(t *testing.T, f *logFixture) *Model {
	t.Helper()
	m := f.model(false, f.localToday())
	m.logs.mode = logModeRange
	m.logs.from = localDay(time.Now().AddDate(0, 0, -20))
	m.logs.to = localDay(time.Now())
	deliver(m, m.loadLogs())
	m.logs.focus = logFocusView
	pump(t, m, "right")
	if !m.logs.heat {
		t.Fatalf("a = heat %v, want the heatmap", m.logs.heat)
	}
	return m
}

func TestLogsHeatmapShowsLevelsLegendAndTotals(t *testing.T) {
	for _, tc := range []struct {
		name, locale string
		levels       []string
	}{
		{"unicode", "en_US.UTF-8", []string{"░", "▒", "▓", "█"}},
		{"ascii", "C", []string{"-", "=", "+", "#"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			t.Setenv("LC_ALL", tc.locale)
			m := heatModel(t, activityLog(t))
			view := m.View()
			assertViewFits(t, view, 120, 30)
			grid := heatRows(view)
			// 1, 2, 3, 4, and 8 calls on five days: every level shows.
			for _, glyph := range tc.levels {
				if !strings.Contains(grid, glyph) {
					t.Errorf("the grid lacks level glyph %q:\n%s", glyph, view)
				}
			}
			for _, want := range []string{"less", "more", "outside retention", "altered", "gap in the chain",
				"Calls 18 · success 88% · errors 2"} {
				if !strings.Contains(view, want) {
					t.Errorf("the heatmap lacks %q:\n%s", want, view)
				}
			}
			if strings.Contains(view, "wiki.pages") {
				t.Errorf("the heatmap still shows rows:\n%s", view)
			}
			pump(t, m, "left")
			if v := m.View(); m.logs.heat || !strings.Contains(v, "wiki.pages.list") {
				t.Errorf("left on View = heat %v, want the rows back:\n%s", m.logs.heat, v)
			}
		})
	}
}

// The heatmap is a value of the bar's View field, reached by tab, and a command; the letter a is not bound.
func TestLogsViewFieldAndCommandsSwitchTheHeatmap(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	f := activityLog(t)
	m := f.model(false, f.localToday())
	if view := m.View(); !strings.Contains(view, "View: [Table] Heatmap") {
		t.Fatalf("the bar lacks the View field:\n%s", view)
	}
	pump(t, m, "a")
	if m.logs.heat {
		t.Fatalf("a switched to the heatmap; the key must stay unbound on Activity")
	}
	press(t, m, "tab", "tab", "tab")
	if m.logs.focus != logFocusView || !strings.Contains(m.View(), "> View:") {
		t.Fatalf("tab x3 = focus %v, want the View field:\n%s", m.logs.focus, m.View())
	}
	pump(t, m, "right")
	if !m.logs.heat || !strings.Contains(m.View(), "View: Table [Heatmap]") {
		t.Fatalf("right = heat %v, want the heatmap:\n%s", m.logs.heat, m.View())
	}
	if m.logs.mode != logModeRange {
		t.Errorf("the heatmap from a day = mode %v, want the retention range", m.logs.mode)
	}
	press(t, m, "tab")
	press(t, m, "esc")
	if m.screen != screenNav || !m.logs.heat {
		t.Errorf("esc = screen %v heat %v, want one level back with the view kept", m.screen, m.logs.heat)
	}
	m.screen = screenLogs
	press(t, m, ":")
	typeText(t, m, "view table")
	pump(t, m, "enter")
	if m.logs.heat {
		t.Errorf("view table left the heatmap on")
	}
	press(t, m, ":")
	typeText(t, m, "view heatmap")
	pump(t, m, "enter")
	if !m.logs.heat {
		t.Errorf("view heatmap did not show the heatmap")
	}
	var view command
	for _, c := range m.commands() {
		if c.name == "view heatmap" {
			view = c
		}
	}
	m.screen = screenNav
	if reason := view.unavailable(m); reason == "" {
		t.Errorf("view heatmap is available on the tab line")
	}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	m.screen = screenLogs
	assertViewFits(t, m.View(), 50, 24)
}

func TestLogsHeatmapLevelsFollowQuartiles(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	m := heatModel(t, activityLog(t))
	from, to := m.heatSpan()
	days := invokelog.DailyActivity(m.logs.heatDays(), from, to)
	levels := invokelog.ActivityLevel(days)
	got := map[string]int{}
	for i, d := range days {
		got[d.Date] = levels[i]
	}
	today := time.Now()
	for ago, want := range map[int]int{1: 2, 2: 4, 3: 3, 4: 1, 8: 1, 5: 0} {
		if level := got[localDay(today.AddDate(0, 0, -ago))]; level != want {
			t.Errorf("%d days ago = level %d, want %d", ago, level, want)
		}
	}
}

func TestLogsHeatmapFollowsTheFilters(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	m := heatModel(t, activityLog(t))
	m.logs.filters[logFilterResult] = "denied"
	m.logs.applyFilters()
	if view := m.View(); !strings.Contains(view, "Calls 2 · success 0% · errors 2") {
		t.Errorf("the heatmap ignores the result filter:\n%s", view)
	}
	m.logs.filters[logFilterResult] = ""
	m.logs.filters[logFilterTool] = "wiki.pages.read"
	m.logs.applyFilters()
	if view := m.View(); !strings.Contains(view, "Calls 10 · success 100% · errors 0") {
		t.Errorf("the heatmap ignores the tool filter:\n%s", view)
	}
}

func TestLogsHeatmapMarksDaysOutsideRetention(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	f := activityLog(t)
	m := heatModel(t, f)
	if grid := heatRows(m.View()); strings.Contains(grid, "x") {
		t.Fatalf("a retention of 90 days marks days as outside it:\n%s", grid)
	}
	m.cfg.Logs.RetentionDays = 5
	grid := heatRows(m.View())
	// The 8th day back holds a call and keeps its level; the empty ones before the 5th day back are marked.
	if !strings.Contains(grid, "x") {
		t.Errorf("no day is marked as outside the retention of 5 days:\n%s", grid)
	}
	if strings.Contains(grid, "x") && strings.Count(grid, "x") > 20 {
		t.Errorf("too many days marked:\n%s", grid)
	}
}

func TestLogsHeatmapMarksAlteredAndGapDays(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	f := activityLog(t)
	// Change the time of the first entry of the day 4 days back, and delete one entry from the day 3 days back.
	changed := localDay(time.Now().AddDate(0, 0, -4))
	removed := localDay(time.Now().AddDate(0, 0, -3))
	fileOf := func(day string) string {
		d, _ := time.ParseInLocation(logDateLayout, day, time.Local)
		return d.Add(12 * time.Hour).UTC().Format(logDateLayout)
	}
	f.edit(fileOf(changed), func(lines [][]byte) [][]byte {
		lines[0] = []byte(strings.Replace(string(lines[0]), `"result":"success"`, `"result":"changed"`, 1))
		return lines
	})
	f.edit(fileOf(removed), func(lines [][]byte) [][]byte { return append(lines[:1], lines[2:]...) })
	m := heatModel(t, f)
	grid := heatRows(m.View())
	for _, glyph := range []string{"✗", "…"} {
		if !strings.Contains(grid, glyph) {
			t.Errorf("the grid lacks the mark %q:\n%s", glyph, m.View())
		}
	}
}

func TestLogsHeatmapFitsSmallTerminals(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	m := heatModel(t, activityLog(t))
	for _, size := range []struct{ width, height int }{{80, 24}, {60, 20}, {40, minimumHeight}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.View()
		if strings.Contains(view, "Resize terminal") {
			t.Errorf("%dx%d cannot show the heatmap:\n%s", size.width, size.height, view)
			continue
		}
		assertViewFits(t, view, size.width, size.height)
	}
}

func TestOverviewShowsTheHeatmapAndLatestCallsAfterLoading(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("LC_ALL", "C")
	f := activityLog(t)
	m := overviewModel(t, f.model(false, f.localToday()))
	m.openSection(sectionOverview)
	m.focusNav()
	before := m.View()
	if m.overviewAct.loaded || strings.Contains(before, "Latest calls") || !strings.Contains(before, "Connections") {
		t.Fatalf("the Overview shows activity before it was read:\n%s", before)
	}
	start := time.Now()
	cmd := m.Init()
	if cmd == nil || time.Since(start) > time.Second {
		t.Fatalf("Init() took %v or returned no command", time.Since(start))
	}
	deliver(m, m.loadOverviewActivity())
	view := m.View()
	assertViewFits(t, view, 100, 28)
	for _, want := range []string{"Activity", "Calls 18", "Mon", "less", "Latest calls", "wiki.pages.list", "wiki-main", "ok", "25ms",
		"Connections", "Next steps"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Overview lacks %q:\n%s", want, view)
		}
	}
	if strings.Count(view, "wiki.pages") > overviewRecent {
		t.Errorf("the Overview lists more than %d latest calls:\n%s", overviewRecent, view)
	}
}

func TestOverviewActivityGivesWayToTheCounts(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	f := activityLog(t)
	m := overviewModel(t, f.model(false, f.localToday()))
	deliver(m, m.loadOverviewActivity())
	for _, size := range []struct{ width, height int }{{40, minimumHeight}, {60, 16}, {80, 24}, {200, 60}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := m.View()
		assertViewFits(t, view, size.width, size.height)
		if !strings.Contains(view, "Approvals") || !strings.Contains(view, "Connections") {
			t.Errorf("%dx%d lacks the counts:\n%s", size.width, size.height, view)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: minimumHeight})
	if view := m.View(); strings.Contains(view, "Latest calls") {
		t.Errorf("the smallest Overview still has room for the activity:\n%s", view)
	}
}
