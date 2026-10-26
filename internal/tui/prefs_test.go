package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// restoreRenderer puts the process-wide colour state back after a test that changes it.
func restoreRenderer(t *testing.T) {
	t.Helper()
	dark, profile, saved := hasDarkBackground(), currentProfile(), styles
	t.Cleanup(func() {
		setHasDarkBackground(dark)
		setProfile(profile)
		setTheme(saved)
	})
}

func TestColorSchemeVariants(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "")
	dark, light := buildTheme(true, false, false), buildTheme(false, false, false)
	if dark.active.GetForeground() == light.active.GetForeground() {
		t.Fatal("light and dark themes share a foreground; want one per background")
	}
	cases := []struct {
		mode     string
		detected bool
		want     theme
	}{
		{uiprefs.ThemeLight, true, light},
		{uiprefs.ThemeDark, false, dark},
		{uiprefs.ThemeAuto, true, dark},
		{uiprefs.ThemeAuto, false, light},
	}
	for _, c := range cases {
		setHasDarkBackground(c.detected)
		applyColorScheme(c.mode, uiprefs.BackgroundTerminal)
		if styles.active.GetForeground() != c.want.active.GetForeground() ||
			styles.warning.GetForeground() != c.want.warning.GetForeground() {
			t.Errorf("mode %s with detected dark=%v: got the wrong palette", c.mode, c.detected)
		}
	}
}

func TestColorSchemeNoColorWins(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{uiprefs.ThemeAuto, uiprefs.ThemeLight, uiprefs.ThemeDark} {
		applyColorScheme(mode, uiprefs.BackgroundQatlas)
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
