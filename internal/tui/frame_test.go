package tui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// vaultCases are the vault states the header shows, each with the word it must name.
func vaultCases(t *testing.T) []struct {
	name  string
	m     *Model
	words []string
} {
	t.Helper()
	none, _, _ := newModel(t)
	locked, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	unlocked, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	return []struct {
		name  string
		m     *Model
		words []string
	}{
		{"no vault", none, nil},
		{"unencrypted", unencryptedVaultModel(t), []string{"vault unencrypted"}},
		{"locked", locked, []string{"vault locked"}},
		{"unlocked", unlocked, []string{"vault unlocked", "admin"}},
	}
}

// workspaceRows is what the frame leaves the workspace: the screen minus header, tab line, option bar, rule,
// and the footer.
func workspaceRows(m *Model) int {
	rows := m.termHeight - len(m.headerLines()) - 3
	if m.hasSubTabs() {
		rows--
	}
	return max(rows, 1)
}

// The footer is the last line of the screen at every size and in every vault state, it is one line, and it
// names the help key; the workspace height follows from the header that is actually drawn.
func TestFooterIsTheLastLineAtEverySize(t *testing.T) {
	sizes := []struct{ width, height int }{{minimumWidth, minimumHeight}, {79, 24}, {150, 42}}
	for _, vc := range vaultCases(t) {
		for _, size := range sizes {
			for _, scr := range []screen{screenNav, screenList} {
				t.Run(fmt.Sprintf("%s/%dx%d/%v", vc.name, size.width, size.height, scr), func(t *testing.T) {
					m := vc.m
					m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
					m.section, m.screen = sectionServices, scr
					view := m.View()
					assertViewFits(t, view, size.width, size.height)
					lines := strings.Split(view, "\n")
					if len(lines) != size.height {
						t.Fatalf("view has %d lines, want %d:\n%s", len(lines), size.height, view)
					}
					want := keyLabel(actHelp) + " " + tr(tHeaderHelp)
					if last := lines[len(lines)-1]; !strings.Contains(last, want) {
						t.Errorf("the last line %q does not name %q:\n%s", last, want, view)
					}
					if m.height != workspaceRows(m) {
						t.Errorf("workspace height %d, want %d", m.height, workspaceRows(m))
					}
				})
			}
		}
	}
}

// Below the smallest size the terminal shows the resize notice instead of a clipped frame.
func TestBelowTheSmallestSizeTheResizeNoticeStands(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: minimumWidth, Height: minimumHeight - 1})
	if view := m.View(); !strings.Contains(view, tr(tResizeTitle)) {
		t.Errorf("no resize notice at %dx%d:\n%s", minimumWidth, minimumHeight-1, view)
	}
}

// The header names every vault and admin state, and the workspace height follows each change of it: the
// admin text can push the header onto a second line in a narrow terminal.
func TestHeaderNamesEveryStateAndTheHeightFollows(t *testing.T) {
	for _, vc := range vaultCases(t) {
		for _, width := range []int{minimumWidth, 79, 150} {
			t.Run(fmt.Sprintf("%s/%d", vc.name, width), func(t *testing.T) {
				m := vc.m
				m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				header := strings.Join(strings.Fields(strings.Join(m.headerLines(), " ")), " ")
				for _, word := range vc.words {
					if !strings.Contains(header, word) {
						t.Errorf("header %q lacks %q", header, word)
					}
				}
				if !strings.Contains(header, "Qatlas") {
					t.Errorf("header %q lacks the name", header)
				}
				if m.height != workspaceRows(m) {
					t.Errorf("workspace height %d, want %d", m.height, workspaceRows(m))
				}
			})
		}
	}

	unlocked, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	for _, width := range []int{minimumWidth, 79} {
		unlocked.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		for _, step := range []struct {
			name string
			do   func()
			want string
		}{
			{"admin session starts", unlocked.startAdminSession, "admin 10m left"},
			{"admin session ends", unlocked.endAdminSession, "admin off"},
			{"admin asks every time", func() {
				cfg := unlocked.cfg.Clone()
				cfg.Vault.AdminTimeout = "0"
				unlocked.cfg = cfg
			}, "admin: confirm each change"},
		} {
			step.do()
			unlocked.Update(vaultTickMsg{})
			header := strings.Join(strings.Fields(strings.Join(unlocked.headerLines(), " ")), " ")
			if !strings.Contains(header, step.want) {
				t.Errorf("width %d, %s: header %q lacks %q", width, step.name, header, step.want)
			}
			if unlocked.height != workspaceRows(unlocked) {
				t.Errorf("width %d, %s: workspace height %d, want %d", width, step.name, unlocked.height, workspaceRows(unlocked))
			}
			view := unlocked.View()
			if lines := strings.Split(view, "\n"); len(lines) != 30 || !strings.Contains(lines[29], tr(tHeaderHelp)) {
				t.Errorf("width %d, %s: the footer is not the last line:\n%s", width, step.name, view)
			}
		}
		unlocked.cfg = unlocked.cfg.Clone()
		unlocked.cfg.Vault.AdminTimeout = ""
	}
}

// The header names the keys of help and the command line as they are bound.
func TestHeaderNamesTheBoundKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	if got := m.headerLines()[0]; !strings.Contains(got, "[?] help") || !strings.Contains(got, "[:] cmd") {
		t.Fatalf("header %q lacks the default keys", got)
	}
	rebind(t, actHelp, "ctrl+g")
	rebind(t, actCommand, "ctrl+k")
	got := m.headerLines()[0]
	if !strings.Contains(got, "[ctrl+g] help") || !strings.Contains(got, "[ctrl+k] cmd") || strings.Contains(got, "[?]") {
		t.Errorf("header %q does not follow the keymap", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if got := m.headerLines()[0]; strings.Contains(got, "cmd") {
		t.Errorf("header %q keeps the hints although the path needs the room", got)
	}
}

// The footer gives up groups by rank, never the help key, and never exceeds the width.
func TestFooterGivesUpGroupsByRankAndKeepsHelp(t *testing.T) {
	m, _, _ := newModel(t)
	text := "up/down move · enter edit · n new · / filter · {back} back · {command} cmds · {help} help · q quit"
	for width := 120; width >= 8; width-- {
		m.termWidth = width
		m.screenKeys = text
		line := m.footerLine()
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("width %d: footer is %d cells: %q", width, w, line)
		}
		if !strings.Contains(line, "? help") {
			t.Fatalf("width %d: footer %q lost the help key", width, line)
		}
	}
	m.termWidth, m.screenKeys = 40, text
	line := m.footerLine()
	for _, want := range []string{"esc back", "? help"} {
		if !strings.Contains(line, want) {
			t.Errorf("40 cells: footer %q lost %q", line, want)
		}
	}
	if strings.Contains(line, "up/down") {
		t.Errorf("40 cells: footer %q keeps navigation before other keys", line)
	}
}

// What the last action reports stays on screen next to the footer.
func TestStatusAndErrorStayVisible(t *testing.T) {
	for _, size := range []struct{ width, height int }{{minimumWidth, minimumHeight}, {79, 24}} {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		openSectionByName(t, m, sectionServices)
		m.status = "saved"
		if view := m.View(); !strings.Contains(view, "saved") {
			t.Errorf("%dx%d: the status is gone:\n%s", size.width, size.height, view)
		}
		m.status, m.fail = "", "something failed"
		view := m.View()
		if flat := flatText(view); !strings.Contains(flat, "something failed") {
			t.Errorf("%dx%d: the error is gone:\n%s", size.width, size.height, view)
		}
		if lines := strings.Split(view, "\n"); !strings.Contains(lines[len(lines)-1], "? help") {
			t.Errorf("%dx%d: the footer is gone under the error:\n%s", size.width, size.height, view)
		}
	}
}

var colourCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// hasColour reports whether text carries an SGR code that selects a colour.
func hasColour(text string) bool {
	for _, code := range colourCode.FindAllString(text, -1) {
		for _, p := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(code, "\x1b["), "m"), ";") {
			if p == "38" || p == "48" || (len(p) == 2 && (p[0] == '3' || p[0] == '4' || p[0] == '9') && p[1] <= '7') {
				return true
			}
		}
	}
	return false
}

// Without colour the header, the tab line and the footer read as text: the active tab stays marked, the
// vault state keeps its word, and the keys keep their names.
func TestFrameReadsWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	prior := styles
	t.Cleanup(func() { lipgloss.SetColorProfile(before); setTheme(prior) })
	t.Setenv("NO_COLOR", "1")
	applyColorScheme("", "")
	lipgloss.SetColorProfile(termenv.ANSI256)

	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	m.Update(tea.WindowSizeMsg{Width: 220, Height: 30})
	m.section, m.screen = sectionServices, screenList
	view := m.View()
	lines := strings.Split(view, "\n")
	header, tabs, footer := lines[0], lines[1], lines[len(lines)-1]
	for name, line := range map[string]string{"header": header, "tab line": tabs, "footer": footer} {
		if hasColour(line) {
			t.Errorf("%s carries a colour under NO_COLOR: %q", name, line)
		}
	}
	for _, want := range []string{"vault unlocked", "admin off", "[?] help"} {
		if !strings.Contains(ansi.Strip(header), want) {
			t.Errorf("header %q lacks %q", header, want)
		}
	}
	if !strings.Contains(ansi.Strip(tabs), "[5 Library]") {
		t.Errorf("tab line %q does not mark the active tab", tabs)
	}
	if plain := ansi.Strip(footer); !strings.Contains(plain, "? help") || !strings.Contains(plain, "/ filter") {
		t.Errorf("footer %q lacks its keys", footer)
	}
	if !strings.Contains(ansi.Strip(header), "●") {
		t.Errorf("header %q has no status mark", header)
	}
}
