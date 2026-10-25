package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The tabs of the Activity screen switch by key and by click, and the summary and the sessions stay behind s and m.
func TestLogsTabsSwitchByKeyAndClick(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	f := activityLog(t)
	m := f.model(true, f.localToday())
	if m.logs.tab() != logTabTable || !strings.Contains(m.View(), "[Table]") {
		t.Fatalf("the screen does not open on the Table tab:\n%s", m.View())
	}
	press(t, m, "tab")
	if m.logs.focus != logFocusTabs {
		t.Fatalf("tab = focus %v, want the tabs", m.logs.focus)
	}
	for _, step := range []struct {
		key  string
		want logTab
		mark string
	}{
		{"right", logTabActivity, "[Activity]"}, {"right", logTabDetails, "[Details]"},
		{"right", logTabDetails, "[Details]"}, {"left", logTabActivity, "[Activity]"}, {"left", logTabTable, "[Table]"},
	} {
		pump(t, m, step.key)
		if m.logs.tab() != step.want || !strings.Contains(m.View(), step.mark) {
			t.Fatalf("%s = tab %v, want %v with %s:\n%s", step.key, m.logs.tab(), step.want, step.mark, m.View())
		}
	}
	row := m.workspaceTop()
	clickAt(t, m, cellInRow(t, m, row, "Activity"), row)
	if m.logs.tab() != logTabActivity || !m.logs.heat {
		t.Fatalf("a click on Activity = tab %v", m.logs.tab())
	}
	clickAt(t, m, cellInRow(t, m, row, "Details"), row)
	if m.logs.tab() != logTabDetails || !m.logs.stats || m.logs.heat {
		t.Fatalf("a click on Details = tab %v", m.logs.tab())
	}
	clickAt(t, m, cellInRow(t, m, row, "Table"), row)
	if m.logs.tab() != logTabTable || m.logs.stats || m.logs.heat || !strings.Contains(m.View(), "wiki.pages") {
		t.Fatalf("a click on Table = tab %v:\n%s", m.logs.tab(), m.View())
	}
	// The keys of the summary stay: s toggles Details, m shows the sessions in it.
	press(t, m, "s")
	if m.logs.tab() != logTabDetails || m.logs.sessions {
		t.Errorf("s = tab %v sessions %v, want the summary", m.logs.tab(), m.logs.sessions)
	}
	press(t, m, "m")
	if m.logs.tab() != logTabDetails || !m.logs.sessions {
		t.Errorf("m = tab %v sessions %v, want the sessions", m.logs.tab(), m.logs.sessions)
	}
	// A filter keeps working on every tab: the result filter leaves only the denied calls in the heatmap.
	m.logs.filters[logFilterResult] = "denied"
	m.logs.applyFilters()
	deliver(m, m.setLogTab(logTabActivity))
	if view := m.View(); m.logs.tab() != logTabActivity || !strings.Contains(view, "Calls 2 · success 0% · errors 2") {
		t.Errorf("the filter does not reach the Activity tab:\n%s", view)
	}
}

// The status of the hash chain is on the screen whichever tab is shown.
func TestLogsStatusStaysVisibleOnEveryTab(t *testing.T) {
	f := activityLog(t)
	changed := localDay(time.Now().AddDate(0, 0, -4))
	d, _ := time.ParseInLocation(logDateLayout, changed, time.Local)
	f.edit(d.Add(12*time.Hour).UTC().Format(logDateLayout), func(lines [][]byte) [][]byte {
		lines[0] = []byte(strings.Replace(string(lines[0]), `"result":"success"`, `"result":"changed"`, 1))
		return lines
	})
	m := heatModel(t, f)
	for _, tab := range []logTab{logTabTable, logTabActivity, logTabDetails} {
		m.setLogTab(tab)
		if view := m.View(); !strings.Contains(view, "✗ altered") {
			t.Errorf("tab %v hides the status:\n%s", tab, view)
		}
	}
}

// The totals of the heatmap are the calls, the share that succeeded, and the failures of the demo log.
func TestActivityHeatmapTotalsMatchTheFixture(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	raw, err := os.ReadFile(filepath.Join(fixtures, "invokes.json"))
	mustNoError(t, err)
	var calls []visualCall
	mustNoError(t, json.Unmarshal(raw, &calls))
	failed := 0
	for _, c := range calls {
		if c.Result != "success" {
			failed++
		}
	}
	visualSetup(t, false)
	m := visualModel(t, fixtures, 150, 42, "dark", "terminal")
	activityHeatmap(t, m)
	want := fmt.Sprintf("Calls %d · success %d%% · errors %d", len(calls), (len(calls)-failed)*100/len(calls), failed)
	if view := ansi.Strip(m.View()); !strings.Contains(view, want) {
		t.Errorf("the heatmap totals do not match the log (%q):\n%s", want, view)
	}
}

// Every tab reads without colour and fits the smallest terminal: the active tab keeps its brackets, the result its
// word, and the status of the chain its symbol and word.
func TestLogsTabsWithoutColourAndAtTheSmallestSize(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	for _, size := range []struct{ width, height int }{{minimumWidth, minimumHeight}, {80, 24}, {150, 42}} {
		for _, tab := range []struct {
			tab  logTab
			mark string
			cell string
		}{{logTabTable, "[Table]", "ok"}, {logTabActivity, "[Activity]", "Calls"}, {logTabDetails, "[Details]", "CALLS"}} {
			m := heatModel(t, activityLog(t))
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m.setLogTab(tab.tab)
			view := m.View()
			name := fmt.Sprintf("%v at %dx%d", tab.mark, size.width, size.height)
			if strings.Contains(view, "Resize terminal") {
				// The summary needs more height than the smallest terminal gives, as it always did.
				if tab.tab == logTabDetails && size.height == minimumHeight {
					continue
				}
				t.Errorf("%s cannot be shown:\n%s", name, view)
				continue
			}
			assertViewFits(t, view, size.width, size.height)
			if hasColour(view) {
				t.Errorf("%s carries colour under NO_COLOR:\n%q", name, view)
			}
			for _, want := range []string{tab.mark, tab.cell, "unverified"} {
				if !strings.Contains(view, want) {
					t.Errorf("%s lacks %q:\n%s", name, want, view)
				}
			}
		}
	}
}

// An empty day says so in the panel, on every tab.
func TestLogsEmptyDayShowsAShortHintInThePanel(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "wiki.pages.list", "wiki", "success")
	m := f.model(true, "2001-01-01")
	view := m.View()
	if !strings.Contains(view, "No entries for 2001-01-01") || !strings.Contains(view, "╭") {
		t.Errorf("an empty day shows no hint in a panel:\n%s", view)
	}
}
