package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func withTheme(t *testing.T, th theme) {
	t.Helper()
	before, profile := styles, currentProfile()
	setProfile(profileTrueColor)
	setTheme(th)
	t.Cleanup(func() { setTheme(before); setProfile(profile) })
}

func TestWidgetsCloseTheirFrameAtEveryWidth(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		withTheme(t, buildTheme(true, noColor, false))
		for w := 10; w <= 200; w++ {
			long := strings.Repeat("wide ", 80)
			blocks := map[string][]string{
				"panel":   panel("a long title "+long, []string{long, "", "x"}, w-4, 4, panelOpts{}),
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

// The active tab is a surface under colour and brackets without it; both take the same cells, so a click
// target never moves with the profile, and the others stay unshaded.
func TestTabStripActiveTabIsASurfaceAndBracketsWithoutColour(t *testing.T) {
	words := []string{"One", "|", "Two", "Three"}
	withTheme(t, buildTheme(true, false, false))
	coloured, colourSpans := tabStrip(words, 2, false)
	focused, _ := tabStrip(words, 2, true)
	if strings.Contains(sgr.ReplaceAllString(coloured, ""), "[") {
		t.Errorf("coloured tabs carry brackets: %q", sgr.ReplaceAllString(coloured, ""))
	}
	if !hasBackground(coloured) || !hasBackground(focused) || focused == coloured {
		t.Errorf("active tab is no surface, or the focus does not show: %q / %q", coloured, focused)
	}
	withTheme(t, buildTheme(true, true, false))
	plain, plainSpans := tabStrip(words, 2, false)
	if !strings.Contains(plain, "[Two]") || hasBackground(plain) || strings.Contains(plain, "38;") {
		t.Errorf("plain active tab = %q, want brackets and no colour", plain)
	}
	if !strings.Contains(sgr.ReplaceAllString(plain, ""), "[Two]") || lipgloss.Width(plain) != lipgloss.Width(coloured) {
		t.Errorf("plain width %d, coloured %d", lipgloss.Width(plain), lipgloss.Width(coloured))
	}
	for i := range colourSpans {
		if colourSpans[i] != plainSpans[i] {
			t.Errorf("span %d = %v coloured, %v plain", i, colourSpans[i], plainSpans[i])
		}
	}
	if reverse, _ := tabStrip(words, 2, true); reverse == plain {
		t.Errorf("without colour the focus leaves the tab unchanged")
	}
}

func TestPanelAndFieldCarrySurfaces(t *testing.T) {
	withTheme(t, buildTheme(true, false, false))
	p := styles.palette
	bgOf := func(c colour) string {
		out := lipgloss.NewStyle().Background(c.lip()).Render("x")
		return out[:strings.Index(out, "x")]
	}
	panelBG, fieldBG := bgOf(p.panel), bgOf(p.surface)
	if panelBG == fieldBG || bgOf(p.base) == panelBG || bgOf(p.base) == fieldBG {
		t.Fatalf("base, panel and surface must differ: %q %q %q", bgOf(p.base), panelBG, fieldBG)
	}
	// A styled text inside a panel keeps the panel colour behind it, also after its own reset.
	lines := panel("t", []string{styles.hint.Render("dim") + " tail"}, 20, 1, panelOpts{})
	if !strings.HasPrefix(strings.TrimPrefix(lines[1], panelStyle(false).Render("│")), panelBG) {
		t.Fatalf("panel content does not open with the panel colour: %q", lines[1])
	}
	if !strings.Contains(lines[1], "\x1b[m"+panelBG+" tail") {
		t.Fatalf("panel colour lost after a styled run: %q", lines[1])
	}
	if f := surfaceRow("name", 10); !strings.Contains(f, fieldBG) || strings.Contains(f, panelBG) {
		t.Fatalf("field row not on the field surface: %q", f)
	}

	withTheme(t, buildTheme(true, true, false))
	for _, l := range append(panel("t", []string{"x"}, 10, 1, panelOpts{}), surfaceRow("x", 10)) {
		if strings.Contains(l, "48;") || strings.Contains(l, "\x1b[4") {
			t.Fatalf("background colour without colour: %q", l)
		}
	}
}
