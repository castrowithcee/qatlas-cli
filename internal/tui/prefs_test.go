package tui

import (
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// restoreRenderer puts the process-wide colour state back after a test that changes it.
func restoreRenderer(t *testing.T) {
	t.Helper()
	fg, bg, profile, saved := termFg, termBg, currentProfile(), styles
	t.Cleanup(func() {
		termFg, termBg = fg, bg
		setProfile(profile)
		setTheme(saved)
	})
}

func TestColorSchemeNoColorWins(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileASCII)
	termFg, termBg = &rgb{255, 255, 255}, &rgb{0, 0, 0}
	for _, mode := range themeNames() {
		applyTheme(mode)
		for name, st := range map[string]lipgloss.Style{
			"title": styles.title, "active": styles.active, "ok": styles.ok, "fail": styles.fail,
			"warning": styles.warning, "focusBorder": styles.focusBorder,
			"activeRow": styles.activeRow, "key": styles.key, "hint": styles.hint,
		} {
			out := st.Render("x")
			if strings.Contains(out, "38;") || strings.Contains(out, "48;") {
				t.Errorf("mode %s, style %s: NO_COLOR still emits a colour sequence: %q", mode, name, out)
			}
		}
	}
	// The row marker survives without colour: reverse video is an attribute, not a colour.
	if got := styles.activeRow.Render("x"); !strings.Contains(got, "7") {
		t.Errorf("activeRow lost reverse video: %q", got)
	}
}

func TestStartAreaOpensChosenArea(t *testing.T) {
	m, _, _ := newModel(t)
	if got, ok := areaByName("Connections"); !ok || got != areaConnections {
		t.Fatalf("areaByName(Connections) = %v, %v", got, ok)
	}
	if _, ok := areaByName("nowhere"); ok {
		t.Fatal("areaByName(nowhere) found an area")
	}
	m.start = areaConnections
	m.status = "warning: kept"
	m.Init()
	if m.section != sectionConnections {
		t.Errorf("section = %v, want connections", m.section)
	}
	if m.status != "warning: kept" {
		t.Errorf("status = %q; the opening notice was lost", m.status)
	}
}

func TestStartDefaultStaysOnOverview(t *testing.T) {
	m, _, _ := newModel(t)
	m.section = sectionOverview // what New itself starts on; the helper starts on Services
	m.Init()
	if m.section != sectionOverview {
		t.Errorf("section = %v, want overview", m.section)
	}
}

// NO_COLOR turns colour off with any value, also with one the colour library does not read as true: at a
// terminal the editor then keeps bold, faint and reverse and draws no colour sequence.
func TestNoColorWithAnyValueDrawsWithoutColour(t *testing.T) {
	restoreRenderer(t)
	// A terminal the colour library would otherwise give every colour.
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	if got := detectProfile(io.Discard); got != profileTrueColor {
		t.Fatalf("without NO_COLOR the profile is %v, want true colour", got)
	}
	for _, value := range []string{"1", "yes"} {
		t.Setenv("NO_COLOR", value)
		setProfile(detectProfile(io.Discard))
		applyTheme(uiprefs.ThemeQatlasDark)
		if toColorprofile(currentProfile()) != colorprofile.ASCII {
			t.Errorf("NO_COLOR=%s: profile %v, want ASCII", value, currentProfile())
		}
		m := sizedModel(groupModel(t), 100, 30)
		openSectionByName(t, m, sectionConnections)
		view := m.render()
		for _, seq := range []string{"\x1b[3", "\x1b[4", "38;", "48;"} {
			if strings.Contains(view, seq) {
				t.Errorf("NO_COLOR=%s: the screen carries the colour sequence %q:\n%q", value, seq, view)
			}
		}
		if !strings.Contains(view, "\x1b[1m") {
			t.Errorf("NO_COLOR=%s: the screen lost its attributes:\n%q", value, view)
		}
	}
}
