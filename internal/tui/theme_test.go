package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

func TestPalettesNameEveryRole(t *testing.T) {
	for name, p := range map[string]palette{"dark": darkPalette, "light": lightPalette} {
		roles := map[string]lipgloss.Color{
			"base": p.base, "surface": p.surface, "panel": p.panel, "border": p.border,
			"borderFocus": p.borderFocus, "text": p.text, "textDim": p.textDim, "accent": p.accent,
			"ok": p.ok, "fail": p.fail, "warn": p.warn, "selection": p.selection,
		}
		for i, c := range p.heat {
			roles["heat"+string(rune('0'+i))] = c
		}
		seen := map[lipgloss.Color]bool{}
		for i, c := range p.brand {
			roles["brand"+string(rune('0'+i))] = c
			if seen[c] {
				t.Errorf("%s: provider colour %s repeats", name, c)
			}
			seen[c] = true
		}
		for role, c := range roles {
			if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(string(c)) {
				t.Errorf("%s: role %s = %q, want a hex colour", name, role, c)
			}
		}
	}
	if darkPalette == lightPalette {
		t.Error("light and dark share a palette")
	}
}

func TestColoursStepDownWithTheProfile(t *testing.T) {
	restoreRenderer(t)
	st := buildTheme(true, false, false).active
	for profile, want := range map[termenv.Profile]string{
		termenv.TrueColor: "38;2;",
		termenv.ANSI256:   "38;5;",
		termenv.ANSI:      "94",
	} {
		lipgloss.SetColorProfile(profile)
		if out := st.Render("x"); !strings.Contains(out, want) {
			t.Errorf("profile %v: %q lacks %q", profile, out, want)
		}
	}
}

func TestNoColorEmitsNoColourSequence(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "1")
	applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundQatlas)
	if styles.filled {
		t.Fatal("NO_COLOR left the background fill on")
	}
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.section = sectionOverview
	view := m.View()
	for _, seq := range []string{"\x1b[3", "\x1b[4", "38;", "48;"} {
		if strings.Contains(view, seq) {
			t.Errorf("NO_COLOR view holds %q", seq)
		}
	}
}

func TestQatlasBackgroundCoversEveryCell(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "")
	applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundQatlas)
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.section = sectionOverview
	m.Init()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 24 {
		t.Fatalf("%d lines, want 24", len(lines))
	}
	baseSeq := styles.canvasPrefix()
	if baseSeq == "" {
		t.Fatal("no canvas sequence under truecolor")
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != 80 {
			t.Errorf("line %d is %d cells wide, want 80", i, w)
		}
		if !strings.HasPrefix(line, baseSeq) {
			t.Errorf("line %d does not open with the base colours", i)
		}
		// Every reset must be followed at once by the base colours.
		for _, loc := range sgrSequence.FindAllStringIndex(line, -1) {
			seq := line[loc[0]:loc[1]]
			if resetsColour(seq) && !strings.HasPrefix(line[loc[1]:], baseSeq) && loc[1] != len(line) {
				t.Errorf("line %d: reset %q at %d is not followed by the base colours", i, seq, loc[0])
			}
		}
	}
}

func TestResetsColour(t *testing.T) {
	for seq, want := range map[string]bool{
		"\x1b[0m": true, "\x1b[m": true, "\x1b[1;49m": true, "\x1b[39m": true,
		"\x1b[1m": false, "\x1b[38;5;0m": false, "\x1b[48;2;0;0;0m": false, "\x1b[38;2;1;2;3;0m": true,
	} {
		if got := resetsColour(seq); got != want {
			t.Errorf("resetsColour(%q) = %v, want %v", seq, got, want)
		}
	}
}

func TestTerminalBackgroundAddsNoFill(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "")
	applyColorScheme(uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
	if styles.filled {
		t.Error("terminal background set the fill")
	}
}
