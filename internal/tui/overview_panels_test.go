package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// demoOverview is the Overview of the snapshot setup at the given size, with its log read.
func demoOverview(t *testing.T, noColor bool, width, height int) *Model {
	t.Helper()
	fixtures := demoFixtures()
	visualSetup(t, noColor)
	return visualModel(t, fixtures, width, height, uiprefs.ThemeQatlasDark)
}

// demoFixtures is the absolute path of the snapshot fixtures, resolved before a test changes its directory.
var demoFixtures = sync.OnceValue(func() string {
	dir, err := filepath.Abs(visualDir)
	if err != nil {
		panic(err)
	}
	return dir
})

// lineOf is the index of the first line of the stripped view that holds text, or -1.
func lineOf(view, text string) int {
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, text) {
			return i
		}
	}
	return -1
}

// The squares of the heatmap carry the levels and the totals of the log of the snapshot setup.
func TestOverviewHeatmapMatchesTheLog(t *testing.T) {
	fixtures := demoFixtures()
	m := demoOverview(t, false, 150, 42)
	if !heatColoured() {
		t.Fatal("the snapshot setup has no colour")
	}
	raw, err := os.ReadFile(filepath.Join(fixtures, "invokes.json"))
	mustNoError(t, err)
	var calls []visualCall
	mustNoError(t, json.Unmarshal(raw, &calls))

	view := m.render()
	grid, drawn := heatSquares(m.overviewAct.days, localDay(m.timeNow()), m.retentionStart(m.timeNow()), 40, overviewHeatWeeks)
	levels := invokelog.ActivityLevel(drawn)
	var total, succeeded int
	want := map[int]int{}
	for i, d := range drawn {
		total += d.Calls
		succeeded += d.Succeeded()
		if d.Date <= localDay(m.timeNow()) {
			want[levels[i]]++
		}
	}
	if total != len(calls) {
		t.Errorf("the grid spans %d calls, the log holds %d", total, len(calls))
	}
	text := strings.Join(grid, "\n")
	for level := 0; level <= invokelog.ActivityLevels; level++ {
		if got := strings.Count(text, heatSquare(level)); got != want[level] {
			t.Errorf("level %d: %d squares, want %d", level, got, want[level])
		}
	}
	rate := strconv.Itoa(succeeded*100/total) + "%"
	if totals := heatTotals(drawn); !strings.Contains(ansi.Strip(view), totals) || !strings.Contains(totals, rate) {
		t.Errorf("the panel lacks the totals %q (success %s):\n%s", totals, rate, ansi.Strip(view))
	}
}

// Without a log the panels stay and say so.
func TestOverviewWithoutLogShowsEmptyPanels(t *testing.T) {
	m := overviewModel(t, mustModel(t))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	deliver(m, m.loadOverviewActivity())
	view := ansi.Strip(m.render())
	for _, want := range []string{"Recent activity", "Activity", "Connections"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Overview lacks %q:\n%s", want, view)
		}
	}
	if n := strings.Count(view, "No calls logged yet"); n != 2 {
		t.Errorf("%d hints in the panels, want 2:\n%s", n, view)
	}
}

// Without colour the marks, the result words, and the glyphs of the levels carry the meaning.
func TestOverviewWithoutColourKeepsMarksAndGlyphs(t *testing.T) {
	m := demoOverview(t, true, 150, 42)
	view := m.render()
	if hasColour(view) {
		t.Errorf("the view holds a colour sequence:\n%q", view)
	}
	for _, want := range []string{"✓", "✗", "timeout", "ok", "░░", "▓▓", "██", "less", "Calls 180"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lacks %q:\n%s", want, ansi.Strip(view))
		}
	}
	if strings.Contains(view, "■") {
		t.Errorf("the view draws coloured squares without colour:\n%s", ansi.Strip(view))
	}
}

// The smallest terminal keeps the counts and the steps selectable, as lines, inside its size.
func TestOverviewAtTheSmallestSize(t *testing.T) {
	m := demoOverview(t, false, minimumWidth, minimumHeight)
	view := m.render()
	assertViewFits(t, ansi.Strip(view), minimumWidth, minimumHeight)
	_, items, _ := m.overviewLines()
	// The five counts and the one next step that the height leaves room for.
	if len(items) != 6 {
		t.Fatalf("%d selectable items, want the 5 counts and a step:\n%s", len(items), ansi.Strip(view))
	}
	for _, label := range []string{"Connections", "Services", "Credentials", "Tokens", "Approvals"} {
		if !strings.Contains(view, label) {
			t.Errorf("the smallest Overview lacks %q:\n%s", label, ansi.Strip(view))
		}
	}
	if frame := strings.Index(view, "╭"); frame >= 0 && frame < strings.Index(view, "Approvals") {
		t.Errorf("the smallest Overview keeps frames over the counts:\n%s", ansi.Strip(view))
	}
	m.screen, m.overviewCursor = screenList, 2
	if view := ansi.Strip(m.render()); !strings.Contains(view, "> Credentials") {
		t.Errorf("the selection is not marked:\n%s", view)
	}
	press(t, m, "enter")
	if m.section != sectionCredentials {
		t.Errorf("enter = section %v, want Credentials", m.section)
	}
}

// The recent calls and the heatmap stand side by side from 120 cells on, and one above the other below.
func TestOverviewPanelsStandBesideEachOtherFromTheThreshold(t *testing.T) {
	wide := demoOverview(t, false, overviewWide, 42)
	view := wide.render()
	recent, heat := lineOf(view, "Recent activity"), lineOf(view, "Activity (")
	if recent < 0 || recent != heat {
		t.Errorf("at %d cells the panels stand on lines %d and %d:\n%s", overviewWide, recent, heat, ansi.Strip(view))
	}
	assertViewFits(t, ansi.Strip(view), overviewWide, 42)

	narrow := demoOverview(t, false, overviewWide-1, 60)
	view = narrow.render()
	recent, heat = lineOf(view, "Recent activity"), lineOf(view, "Activity (")
	if recent < 0 || heat <= recent {
		t.Errorf("at %d cells the panels stand on lines %d and %d, want the heatmap below:\n%s", overviewWide-1, recent, heat,
			ansi.Strip(view))
	}
	assertViewFits(t, ansi.Strip(view), overviewWide-1, 60)
}

// The selected card is framed and marked with ">", also without colour; the cursor leaves the card as it
// moves on, and a click on the card, label or value, opens its area.
func TestOverviewCardSelectionAndClick(t *testing.T) {
	m := demoOverview(t, true, 150, 42)
	press(t, m, "down")
	view := ansi.Strip(m.render())
	if !strings.Contains(view, "> Connections") || !strings.Contains(view, "╔") || strings.Count(view, "╔") != 1 {
		t.Errorf("the selected card is not marked:\n%s", view)
	}
	press(t, m, "down")
	if view := ansi.Strip(m.render()); strings.Contains(view, "> Connections") || !strings.Contains(view, "> Services") {
		t.Errorf("the mark did not move to Services:\n%s", view)
	}

	for _, offset := range []int{0, 1} {
		m := demoOverview(t, false, 150, 42)
		x, y := cellOf(t, m, "Credentials")
		clickAt(t, m, x, y+offset)
		if m.section != sectionOverview || m.overviewCursor < 0 {
			t.Errorf("click %d line under the label: section = %v, want the first click to only mark", offset, m.section)
		}
		m.render()
		clickAt(t, m, x, y+offset)
		if m.section != sectionCredentials {
			t.Errorf("click %d line under the label: section = %v, want Credentials", offset, m.section)
		}
	}
}

// The date of the header follows the language.
func TestOverviewStampFollowsTheLanguage(t *testing.T) {
	visualSetup(t, false)
	for lang, want := range map[i18n.Lang]string{
		i18n.English: "Wed, 18 Mar 2026  12:00",
		i18n.German:  "Mi., 18. März 2026  12:00",
	} {
		useLanguage(t, lang)
		if got := overviewStamp(visualNow); got != want {
			t.Errorf("%s: stamp = %q, want %q", lang, got, want)
		}
	}
}
