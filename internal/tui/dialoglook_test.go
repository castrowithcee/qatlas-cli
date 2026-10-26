package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// dialogScreen brings a model to one dialog screen; the model is sized by the caller afterwards.
type dialogScreen struct {
	name string
	open func(t *testing.T) *Model
}

func exportAt(step exportStep, kind exportKind) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		v, _ := vaultExportModel(t)
		v.startExport(testExportVaultPass)
		if step != exportSelect {
			v.advance(step, kind, filepath.Join(v.m.startDir, "out.yaml"))
		}
		return v.m
	}
}

func importAt(step importStep) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		path := importFixture(t, exportWithout)
		v := plainImportTarget(t)
		v.startImport("")
		v.importTo(step, path, false, 0)
		return v.m
	}
}

func selftestAt(finish func(m *Model, release chan struct{}) (*selftest.Report, error)) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		m := cmdModel(t)
		m.selftest.scenarios = fakeScenarios("alpha")
		release := make(chan struct{})
		m.selftest.run = func(ctx context.Context, o selftest.Options) (*selftest.Report, error) {
			o.Progress("step one")
			<-release
			if finish == nil {
				return nil, context.Canceled
			}
			return finish(m, release)
		}
		cmd := startSelftestCommand(t, m, "selftest alpha")
		_, cmd = m.Update(cmd())
		if finish == nil {
			t.Cleanup(func() { close(release) })
			return m
		}
		close(release)
		drainSelftest(m, cmd)
		return m
	}
}

func dialogScreens() []dialogScreen {
	failed := func(*Model, chan struct{}) (*selftest.Report, error) {
		return nil, context.DeadlineExceeded
	}
	passed := func(_ *Model, _ chan struct{}) (*selftest.Report, error) { return fakeReport("alpha"), nil }
	return []dialogScreen{
		{"export select", exportAt(exportSelect, exportWithout)},
		{"export mode", exportAt(exportMode, exportWithout)},
		{"export plain", exportAt(exportPlainConfirm, exportUnencrypted)},
		{"export preview", exportAt(exportPreview, exportWithout)},
		{"import mode", importAt(importMode)},
		{"import preview", importAt(importPreview)},
		{"paths", func(t *testing.T) *Model {
			m, _ := toolsModel(t, targetsRegistry(t, github.Register), nil)
			openSectionByName(t, m, sectionConnections)
			pressNew(t, m)
			typeText(t, m, "gh")
			focusField(t, m, pathsLabel)
			press(t, m, "enter")
			addPath(t, m, t.TempDir())
			return m
		}},
		{"targets", func(t *testing.T) *Model {
			m, _ := toolsModel(t, targetsRegistry(t, github.Register), nil)
			openSectionByName(t, m, sectionConnections)
			pressNew(t, m)
			typeText(t, m, "gh")
			openTargetList(t, m)
			addTarget(t, m, "acme/widgets")
			return m
		}},
		{"add menu", func(t *testing.T) *Model {
			m, _ := toolsModel(t, targetsRegistry(t, github.Register), nil)
			openSectionByName(t, m, sectionConnections)
			pressNew(t, m)
			typeText(t, m, "gh")
			openTargetList(t, m)
			press(t, m, "a")
			return m
		}},
		{"selftest running", selftestAt(nil)},
		{"selftest done", selftestAt(passed)},
		{"selftest failed", selftestAt(failed)},
		{"update question", func(t *testing.T) *Model {
			m := startedWith(t, newerRelease())
			press(t, m, "u")
			return m
		}},
		{"restart question", func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			m.updated, m.restartBack, m.screen = "v0.5.0", screenNav, screenRestart
			return m
		}},
	}
}

// checkFits fails for a view that is taller or wider than the terminal.
func checkFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Errorf("%d rows in a terminal of %d:\n%s", len(lines), height, view)
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("a row of %d cells in a terminal of %d: %q", w, width, ansi.Strip(line))
		}
	}
}

// The dialog screens stand in a panel where the terminal has the room and keep the plain look below it; both
// fit the terminal, and the screen reads without colour.
func TestDialogScreensFramedAtSizeAndPlainAtTheSmallest(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		for _, sc := range dialogScreens() {
			for _, size := range []struct {
				w, h   int
				framed bool
			}{{100, 30, true}, {80, 24, true}, {40, 13, false}} {
				name := fmt.Sprintf("%s %dx%d", sc.name, size.w, size.h)
				if noColor {
					name += " no colour"
				}
				t.Run(name, func(t *testing.T) {
					visualSetup(t, noColor)
					applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
					m := sc.open(t)
					m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
					view := m.View()
					checkFits(t, view, size.w, size.h)
					plain := ansi.Strip(view)
					// The placeholder of a text input carries its own grey, which the terminal drops with NO_COLOR.
					if noColor && strings.Contains(strings.ReplaceAll(view, "\x1b[38;5;240m", ""), "\x1b[3") {
						t.Errorf("a colour sequence with NO_COLOR set:\n%q", view)
					}
					framed := strings.Contains(plain, "╭") || strings.Contains(plain, "╔")
					if size.framed && !framed {
						t.Errorf("no panel:\n%s", plain)
					}
					if !size.framed && framed {
						t.Errorf("a panel at the smallest size:\n%s", plain)
					}
				})
			}
		}
	}
}

// A finished step shows its result in the colour of the outcome, always with its word.
func TestResultsCarryTheirColourAndWord(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(t *testing.T) *Model
		mark string
		word string
	}{
		{"selftest done", dialogScreens()[10].open, styles.ok.Render("●"), "done"},
		{"selftest failed", dialogScreens()[11].open, styles.fail.Render("●"), "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			visualSetup(t, false)
			applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
			setProfile(profileTrueColor)
			m := tc.open(t)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			view := m.View()
			if !strings.Contains(view, styles.ok.Render("●")+" "+tc.word) && !strings.Contains(view, styles.fail.Render("●")+" "+tc.word) {
				t.Errorf("no mark and word %q:\n%s", tc.word, view)
			}
		})
	}
}

// The size hint stays readable one cell under the smallest size, and names the way out first.
func TestTheSizeHintReadsAtTheSmallestSizeLess(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		visualSetup(t, noColor)
		applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: minimumWidth - 1, Height: minimumHeight - 2})
		view := m.View()
		checkFits(t, view, minimumWidth-1, minimumHeight-2)
		plain := ansi.Strip(view)
		for _, want := range []string{"q quit", warnMark + " Resize terminal", "Need 40x14", "Have 39x12"} {
			if !strings.Contains(plain, want) {
				t.Errorf("no %q (no colour %v):\n%s", want, noColor, plain)
			}
		}
		if noColor && strings.Contains(view, "\x1b[3") {
			t.Errorf("a colour sequence with NO_COLOR set:\n%q", view)
		}
	}
}

// A question or a prompt that has no room for a modal fills the workspace; where the workspace has the room
// for a frame it stands in a panel, every row of it inside the width.
func TestQuestionAndPromptFillTheWorkspaceInAPanel(t *testing.T) {
	visualSetup(t, false)
	applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundTerminal)

	m := startedWith(t, newerRelease())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(t, m, "u")
	view := ansi.Strip(m.framedText(m.updateView))
	if !strings.Contains(view, "╭") || !strings.Contains(view, warnMark+" Update qatlas to v0.5.0?") {
		t.Errorf("the update question is not a panel with its mark:\n%s", view)
	}
	checkFits(t, view, m.width, m.height)

	e := exportAt(exportTarget, exportWithout)(t)
	e.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	d, ok := e.exportPrompt()
	if !ok {
		t.Fatal("the export has no file prompt")
	}
	view = ansi.Strip(e.promptView(d))
	if !strings.Contains(view, "╭─ "+d.title+" ") {
		t.Errorf("the prompt is not a panel under its title:\n%s", view)
	}
	checkFits(t, view, e.width, e.height)
}
