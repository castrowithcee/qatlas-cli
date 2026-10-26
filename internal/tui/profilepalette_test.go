package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func (p palette) roles() []lipgloss.Color {
	roles := []lipgloss.Color{p.base, p.surface, p.panel, p.border, p.borderFocus, p.text, p.textDim, p.accent,
		p.ok, p.fail, p.warn, p.selection}
	roles = append(roles, p.heat[:]...)
	return append(roles, p.brand[:]...)
}

// The palettes of the limited colour profiles name every role by an index of their own range, never a hex value
// that the terminal would have to step down by itself.
func TestPalettesOfLimitedProfilesNameEveryRoleByIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile termenv.Profile
		limit   int
	}{{"256 colours", termenv.ANSI256, 256}, {"16 colours", termenv.ANSI, 16}} {
		for _, dark := range []bool{true, false} {
			for i, c := range paletteOf(dark, tc.profile).roles() {
				n, err := strconv.Atoi(string(c))
				if err != nil || n < 0 || n >= tc.limit {
					t.Errorf("%s dark=%v role %d is %q, want an index below %d", tc.name, dark, i, c, tc.limit)
				}
			}
		}
	}
	if paletteOf(true, termenv.TrueColor).base != darkPalette.base || paletteOf(false, termenv.Ascii).base != lightPalette.base {
		t.Error("truecolor and plain profiles must keep the hex palettes")
	}
}

// With 16 colours the heat levels are shade glyphs in the success colour, so four levels stay apart.
func TestHeatWithSixteenColoursUsesShadeGlyphs(t *testing.T) {
	restoreRenderer(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	lipgloss.SetColorProfile(termenv.ANSI)
	setTheme(buildTheme(true, false, false))
	seen := map[string]bool{}
	for level := 0; level <= 4; level++ {
		mark := heatBlock(level, 2)
		if strings.Contains(mark, "■") {
			t.Errorf("level %d is a square at 16 colours: %q", level, mark)
		}
		seen[mark] = true
	}
	if len(seen) != 5 {
		t.Errorf("the five levels are not told apart: %v", seen)
	}
}
