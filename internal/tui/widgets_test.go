package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func withTheme(t *testing.T, th theme) {
	t.Helper()
	before, profile := styles, lipgloss.ColorProfile()
	lipgloss.SetColorProfile(3 - 3) // TrueColor
	setTheme(th)
	t.Cleanup(func() { setTheme(before); lipgloss.SetColorProfile(profile) })
}

func TestWidgetsCloseTheirFrameAtEveryWidth(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		withTheme(t, buildTheme(true, noColor, false))
		for w := 10; w <= 200; w++ {
			long := strings.Repeat("wide ", 80)
			blocks := map[string][]string{
				"panel":   panel("a long title "+long, []string{long, "", "x"}, w-4, 4, panelOpts{fill: true}),
				"focused": panel("t", []string{long}, w-4, 2, panelOpts{focused: true}),
				"card":    card("Services "+long, "10"+long, w),
			}
			for name, lines := range blocks {
				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Fatalf("%s w=%d line %d: width %d", name, w, i, got)
					}
				}
				plain := sgr.ReplaceAllString(lines[0], "")
				last := sgr.ReplaceAllString(lines[len(lines)-1], "")
				if !strings.ContainsAny(plain[:3], "╭╔") || !strings.HasSuffix(plain, "╮") && !strings.HasSuffix(plain, "╗") ||
					!strings.ContainsAny(last[:3], "╰╚") || !strings.HasSuffix(last, "╯") && !strings.HasSuffix(last, "╝") {
					t.Fatalf("%s w=%d: open corners %q / %q", name, w, plain, last)
				}
			}
		}
	}
}

func TestStatusMarkAndButtonWithoutColour(t *testing.T) {
	withTheme(t, buildTheme(true, true, false))
	for _, s := range []string{statusMark(statusOK, "active"), statusMark(statusFail, "failed"),
		button(buttonDanger, "Delete", true), button(buttonPrimary, "Save", false), button(buttonNormal, "Cancel", false)} {
		if strings.Contains(s, "\x1b[3") || strings.Contains(s, "\x1b[4") || strings.Contains(s, "38;") || strings.Contains(s, "48;") {
			t.Fatalf("colour sequence in %q", s)
		}
	}
	if !strings.Contains(statusMark(statusOK, "active"), "active") || !strings.Contains(button(buttonDanger, "Delete", true), "> Delete <") {
		t.Fatal("word or focus marker missing")
	}
}

func TestProviderMarkIsStableAndUsesBrandColours(t *testing.T) {
	withTheme(t, buildTheme(true, false, false))
	for _, name := range []string{"github", "gitlab", "mattermost", "", "x"} {
		c := brandColor(name)
		if c != brandColor(name) {
			t.Fatalf("%q unstable", name)
		}
		found := false
		for _, b := range styles.palette.brand {
			found = found || b == c
		}
		if !found {
			t.Fatalf("%q colour %q is no brand colour", name, c)
		}
		if lipgloss.Width(providerMark(name)) != 1 {
			t.Fatal("mark width")
		}
	}
}
