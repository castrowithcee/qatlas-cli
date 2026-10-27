package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// A form with a section list puts the focus frame around the rows, where the focus is, and not around the
// list, which only shows where the rows are. Without colour the frame is double, so the two read apart.
func TestFormFocusFrameStandsAroundTheRowsNotTheSectionList(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	for _, form := range formScreens {
		for _, size := range []struct{ w, h int }{{80, 24}, {150, 42}} {
			t.Run(fmt.Sprintf("%s/%dx%d", form.name, size.w, size.h), func(t *testing.T) {
				visualSetup(t, true)
				m := visualModel(t, fixtures, size.w, size.h, uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
				form.open(t, m)
				var top string
				for _, line := range strings.Split(ansi.Strip(m.render()), "\n") {
					if strings.Contains(line, "╭") || strings.Contains(line, "╔") {
						top = line
						break
					}
				}
				if m.sectionListWidth == 0 {
					return // no list beside the rows: the framed fields carry the focus
				}
				list, rows := strings.Index(top, "╭"), strings.Index(top, "╔")
				if list < 0 || rows < list {
					t.Errorf("the list is not quiet and the rows framed in focus colour: %q", top)
				}
			})
		}
	}
}

// The Activity screen draws its tabs and filters inside the panel of the table, so the focus shows by the
// frame of that panel and by the marked tab or field; nothing outside the panel carries a focus marker, and
// without colour the marker and the double frame still tell where the focus is.
func TestActivityFocusStaysInsideItsPanel(t *testing.T) {
	withNoColor(t)
	t.Setenv("LC_ALL", "en_US.UTF-8")
	for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {80, 24}, {150, 42}} {
		for _, focus := range []logFocus{logFocusTabs, logFocusMode, logFocusRows} {
			t.Run(fmt.Sprintf("%dx%d/focus %v", size.w, size.h, focus), func(t *testing.T) {
				m := heatModel(t, activityLog(t))
				m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
				m.setLogTab(logTabTable)
				m.logs.focus = focus
				view := m.render()
				assertViewFits(t, view, size.w, size.h)
				lines := strings.Split(ansi.Strip(view), "\n")
				framed := m.logs.framed
				marked := 0
				for _, line := range lines[2:] {
					if strings.Contains(line, "> ") {
						marked++
						if framed && !strings.HasPrefix(line, "║ ") && !strings.HasPrefix(line, "│ ") {
							t.Errorf("a focus marker stands outside the panel: %q", line)
						}
					}
				}
				if marked == 0 {
					t.Errorf("nothing carries the focus marker:\n%s", ansi.Strip(view))
				}
				if framed && !strings.Contains(ansi.Strip(view), "╔═") {
					t.Errorf("the panel frame does not show the focus without colour:\n%s", ansi.Strip(view))
				}
			})
		}
	}
}
