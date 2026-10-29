package tui

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

func TestPalettesNameEveryRole(t *testing.T) {
	for name, p := range map[string]palette{"dark": darkPalette, "light": lightPalette} {
		roles := map[string]colour{
			"base": p.base, "surface": p.surface, "panel": p.panel, "border": p.border,
			"borderFocus": p.borderFocus, "text": p.text, "textDim": p.textDim, "accent": p.accent,
			"ok": p.ok, "fail": p.fail, "warn": p.warn, "selection": p.selection,
		}
		for i, c := range p.heat {
			roles["heat"+string(rune('0'+i))] = c
		}
		seen := map[colour]bool{}
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
	st := darkTheme().active
	for profile, want := range map[colorProfile]string{
		profileTrueColor: "38;2;",
		profile256:       "38;5;",
		profile16:        "94",
	} {
		setProfile(profile)
		if out := shown(st.Render("x")); !strings.Contains(out, want) {
			t.Errorf("profile %v: %q lacks %q", profile, out, want)
		}
	}
}

func TestNoColorEmitsNoColourSequence(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileASCII)
	applyTheme(uiprefs.ThemeQatlasDark)
	if styles.filled {
		t.Fatal("NO_COLOR left the background fill on")
	}
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.section = sectionOverview
	view := m.render()
	for _, seq := range []string{"\x1b[3", "\x1b[4", "38;", "48;"} {
		if strings.Contains(view, seq) {
			t.Errorf("NO_COLOR view holds %q", seq)
		}
	}
}

func TestQatlasBackgroundCoversEveryCell(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "")
	applyTheme(uiprefs.ThemeQatlasDark)
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.section = sectionOverview
	m.Init()
	lines := strings.Split(m.render(), "\n")
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

// darkTheme is the qatlas dark palette without painting the screen, the look most tests read their colours
// from.
func darkTheme() theme { return buildTheme(paletteOf(true, currentProfile()), false) }

// useColour gives the test a profile with colour and the dark theme, restored afterwards: the look of a
// terminal that shows colour. Tests start without colour (see profile).
func useColour(t *testing.T) {
	t.Helper()
	restoreRenderer(t)
	setProfile(profileTrueColor)
	setTheme(darkTheme())
}

// lookOf switches the profile and the default theme with it, as a start does: the theme of a profile without
// colour names none.
func lookOf(p colorProfile) {
	setProfile(p)
	setTheme(defaultTheme())
}

// useDarkColours activates darkTheme, or no colour at all under a profile without colour, as applyTheme does.
func useDarkColours() {
	if noColour() {
		setTheme(noColorTheme())
		return
	}
	setTheme(darkTheme())
}

// hexRGB is the colour of a "#rrggbb" string.
func hexRGB(t *testing.T, hex string) rgb {
	t.Helper()
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		t.Fatalf("%q is not a hex colour", hex)
	}
	return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

// answerColours delivers the answers of a terminal with foreground fg and background bg to the model.
func answerColours(t *testing.T, m *Model, fg, bg string) {
	t.Helper()
	f, b := hexRGB(t, fg), hexRGB(t, bg)
	m.Update(tea.ForegroundColorMsg{Color: color.RGBA{R: f.r, G: f.g, B: f.b, A: 0xff}})
	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: b.r, G: b.g, B: b.b, A: 0xff}})
}

// Each qatlas theme paints the screen in its palette, the terminal theme never does.
func TestThemesPaintOnlyWhereTheyOwnTheBackground(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "")
	for name, want := range map[string]palette{uiprefs.ThemeQatlasDark: darkPalette, uiprefs.ThemeQatlasLight: lightPalette} {
		applyTheme(name)
		if !styles.filled || styles.palette != want {
			t.Errorf("%s: filled %v, palette base %q; want filled with base %q", name, styles.filled, styles.palette.base, want.base)
		}
	}
	termFg, termBg = &rgb{0xcd, 0xd6, 0xf4}, &rgb{0x1e, 0x1e, 0x2e}
	applyTheme(uiprefs.ThemeTerminal)
	if styles.filled {
		t.Error("the terminal theme paints the screen")
	}
	if got, _ := themeNamed("no such theme"); got.name != uiprefs.ThemeTerminal {
		t.Errorf("an unknown name gives %q, want the terminal theme", got.name)
	}
}

// The terminal theme leaves the text to the terminal and names its accents by ANSI slot, so the terminal's own
// scheme colours them; the surfaces are steps between its background and foreground, and nothing paints the
// ground of the screen.
func TestTerminalThemeTakesTheColoursOfTheTerminal(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "")
	m, _, _ := newModel(t)
	m.prefs = uiprefs.Default()
	applyTheme(m.prefs.Theme)
	if styles.palette != plainTerminalPalette() {
		t.Fatalf("before the answer the palette is %+v, want the plain one", styles.palette)
	}
	answerColours(t, m, "#cdd6f4", "#1e1e2e")
	p := styles.palette
	if styles.filled || p.text != "" || p.base != "#1e1e2e" {
		t.Errorf("filled %v, text %q, base %q; want no fill, the terminal's text and its background", styles.filled, p.text, p.base)
	}
	for role, want := range map[string]string{"accent": "\x1b[1;34m", "ok": "\x1b[1;32m", "fail": "\x1b[1;31m", "warning": "\x1b[1;33m"} {
		st := map[string]lipgloss.Style{"accent": styles.active, "ok": styles.ok, "fail": styles.fail, "warning": styles.warning}[role]
		if got := st.Render("x"); !strings.HasPrefix(got, want) {
			t.Errorf("%s = %q, want the ANSI slot %q", role, got, want)
		}
	}
	if got := styles.title.Render("x"); strings.Contains(got, "38;") || strings.Contains(got, "\x1b[3") {
		t.Errorf("the title names a text colour: %q", got)
	}
	for _, c := range []colour{p.panel, p.surface, p.selection, p.border, p.textDim} {
		if c == p.base || !strings.HasPrefix(string(c), "#") {
			t.Errorf("role %q is not a step between the colours of the terminal", c)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.section = sectionOverview
	if view := m.render(); strings.Contains(view, "48;2;30;30;46") {
		t.Error("the view paints the background of the terminal")
	}
}

// A choice of a qatlas theme keeps its palette when the terminal answers late.
func TestTerminalAnswerLeavesOtherThemes(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	t.Setenv("NO_COLOR", "")
	m, _, _ := newModel(t)
	m.prefs = uiprefs.Default()
	m.prefs.Theme = uiprefs.ThemeQatlasLight
	applyTheme(m.prefs.Theme)
	answerColours(t, m, "#cdd6f4", "#1e1e2e")
	if styles.palette != lightPalette || !styles.filled {
		t.Errorf("the answer replaced the qatlas light theme: %+v", styles.palette)
	}
}

// Without an answer, with 16 colours or with a pair that is itself too faint, the terminal theme has no fills:
// panels stand by their frames, frames and dimmed text take ANSI slot 8, and the selection is reverse video.
func TestTerminalThemeFallsBackToFrames(t *testing.T) {
	restoreRenderer(t)
	t.Setenv("NO_COLOR", "")
	for _, tc := range []struct {
		name    string
		profile colorProfile
		fg, bg  *rgb
	}{
		{"no answer", profileTrueColor, nil, nil},
		{"only a background", profileTrueColor, nil, &rgb{0, 0, 0}},
		{"16 colours", profile16, &rgb{255, 255, 255}, &rgb{0, 0, 0}},
		{"a pair below 4.5:1", profileTrueColor, &rgb{0x65, 0x7b, 0x83}, &rgb{0xfd, 0xf6, 0xe3}},
	} {
		setProfile(tc.profile)
		termFg, termBg = tc.fg, tc.bg
		applyTheme(uiprefs.ThemeTerminal)
		p := styles.palette
		if p.panel != "" || p.surface != "" || p.selection != "" || p.base != "" || p.border != "8" || p.textDim != "8" {
			t.Errorf("%s: palette %+v, want no fills and slot 8 for frames and dimmed text", tc.name, p)
		}
		if got := styles.activeRow.Render("x"); !strings.Contains(got, "7") || strings.Contains(got, "48;") {
			t.Errorf("%s: the selection %q is not reverse video", tc.name, got)
		}
		if selectionMark() != ">" {
			t.Errorf("%s: the selection lost its marker", tc.name)
		}
	}
}

// A profile without colour (NO_COLOR at a terminal included) wins over the terminal theme: no colour, and no
// question to the terminal.
func TestNoColorAsksTheTerminalNothing(t *testing.T) {
	restoreRenderer(t)
	setProfile(profileTrueColor)
	if terminalColourQuery(uiprefs.ThemeTerminal) == nil {
		t.Fatal("the terminal theme does not ask the terminal for its colours")
	}
	if terminalColourQuery(uiprefs.ThemeQatlasDark) != nil {
		t.Error("a qatlas theme asks the terminal for its colours")
	}
	setProfile(profile16)
	if terminalColourQuery(uiprefs.ThemeTerminal) != nil {
		t.Error("a 16-colour terminal is asked for colours it cannot use")
	}
	for _, p := range []colorProfile{profileASCII, profileNone} {
		setProfile(p)
		if terminalColourQuery(uiprefs.ThemeTerminal) != nil {
			t.Errorf("profile %v: the terminal is asked for its colours", p)
		}
		termFg, termBg = &rgb{255, 255, 255}, &rgb{0, 0, 0}
		applyTheme(uiprefs.ThemeTerminal)
		if styles.palette != (palette{}) || strings.Contains(styles.active.Render("x"), "\x1b[3") {
			t.Errorf("profile %v: the terminal theme still names colours", p)
		}
	}
}

// The mix keeps text readable on the ground and on every fill, and dimmed text on the ground and the panel,
// for dark and light schemes. A pair that is itself below 4.5:1, or so close above it that the fills would
// collapse into one step, gets no fills at all.
func TestMixedPaletteKeepsItsContrast(t *testing.T) {
	for _, tc := range []struct {
		bg, fg string
		mixes  bool
	}{
		{"#000000", "#ffffff", true},
		{"#ffffff", "#000000", true},
		{"#282a36", "#f8f8f2", true},
		{"#1e1e2e", "#cdd6f4", true},
		{"#2e3440", "#d8dee9", true},
		{"#002b36", "#839496", true},
		{"#fdf6e3", "#657b83", false},
		{"#eff1f5", "#4c4f69", true},
		{"#ffffff", "#24292f", true},
		{"#757575", "#000000", false},
		{"#747474", "#000000", false},
	} {
		bg, fg := hexRGB(t, tc.bg), hexRGB(t, tc.fg)
		p, ok := mixPalette(fg, bg, nil)
		if ok != tc.mixes {
			t.Errorf("%s on %s (%.2f:1): mixes %v, want %v", tc.fg, tc.bg, contrast(fg, bg), ok, tc.mixes)
			continue
		}
		if !ok {
			continue
		}
		for role, c := range map[string]colour{"ground": p.base, "surface": p.surface, "panel": p.panel, "selection": p.selection} {
			if got := contrast(fg, hexRGB(t, string(c))); got < textContrast {
				t.Errorf("%s on %s: text on the %s %s is %.2f:1", tc.fg, tc.bg, role, c, got)
			}
		}
		dim := hexRGB(t, string(p.textDim))
		for role, c := range map[string]colour{"ground": p.base, "panel": p.panel} {
			if got := contrast(dim, hexRGB(t, string(c))); got < dimContrast {
				t.Errorf("%s on %s: dimmed text on the %s is %.2f:1", tc.fg, tc.bg, role, got)
			}
		}
		if p.panel == p.base || p.surface == p.panel || p.selection == p.surface {
			t.Errorf("%s on %s: the steps collapse: base %s panel %s surface %s selection %s",
				tc.fg, tc.bg, p.base, p.panel, p.surface, p.selection)
		}
	}
}

// A scheme with known ANSI colours takes them as hex values and its foreground as the text, so it can paint
// the screen; the roles keep the meaning of their slots.
func TestMixedPaletteTakesKnownANSIColours(t *testing.T) {
	var ansi [16]rgb
	for i := range ansi {
		ansi[i] = rgb{uint8(i * 16), uint8(255 - i*16), uint8(i * 8)}
	}
	fg, bg := rgb{0xf8, 0xf8, 0xf2}, rgb{0x28, 0x2a, 0x36}
	p, ok := mixPalette(fg, bg, &ansi)
	if !ok {
		t.Fatal("the pair does not mix")
	}
	for role, c := range map[colour]rgb{p.text: fg, p.accent: ansi[4], p.ok: ansi[2], p.fail: ansi[1], p.warn: ansi[3], p.brand[0]: ansi[brandSlots[0]]} {
		if role != c.hex() {
			t.Errorf("role %q, want %q", role, c.hex())
		}
	}
	if p.heat[4] != ansi[2].hex() {
		t.Errorf("the strongest heat level is %q, want the green %q", p.heat[4], ansi[2].hex())
	}
}

// The registry offers the terminal theme first, and every name it holds is a theme of its own.
func TestThemeRegistry(t *testing.T) {
	names := themeNames()
	if len(names) < 3 || strings.Join(names[:3], ",") != "terminal,qatlas dark,qatlas light" {
		t.Errorf("themes = %v", names)
	}
	for _, name := range names {
		if got, warning := knownTheme(name); got != name || warning != "" {
			t.Errorf("knownTheme(%q) = %q, %q", name, got, warning)
		}
	}
	got, warning := knownTheme("purple")
	if got != uiprefs.ThemeTerminal || !strings.Contains(warning, `theme "purple"`) {
		t.Errorf("knownTheme(purple) = %q, %q; want terminal and a warning", got, warning)
	}
}

// With 256 colours the mixed fills take their nearest index of the cube or grey ramp, so a near grey never
// becomes a coloured neighbour, and they keep their contrast and their steps.
func TestTerminalThemeWith256ColoursKeepsItsGreys(t *testing.T) {
	restoreRenderer(t)
	t.Setenv("NO_COLOR", "")
	setProfile(profile256)
	for _, pair := range [][2]string{{"#cdd6f4", "#1e1e2e"}, {"#24292f", "#ffffff"}, {"#ffffff", "#000000"}} {
		fg, bg := hexRGB(t, pair[0]), hexRGB(t, pair[1])
		termFg, termBg = &fg, &bg
		applyTheme(uiprefs.ThemeTerminal)
		p := styles.palette
		var fills []rgb
		for _, c := range []colour{p.panel, p.surface, p.selection} {
			n, err := strconv.Atoi(string(c))
			if err != nil || n < 16 || n > 255 {
				t.Fatalf("%s on %s: fill %q is not an index of the cube or ramp", pair[0], pair[1], c)
			}
			x := xterm256(n)
			if x.r != x.g || x.g != x.b {
				t.Errorf("%s on %s: fill %d is the coloured %+v", pair[0], pair[1], n, x)
			}
			if contrast(fg, x) < textContrast {
				t.Errorf("%s on %s: text on fill %d is %.2f:1", pair[0], pair[1], n, contrast(fg, x))
			}
			fills = append(fills, x)
		}
		if fills[0] == fills[1] || fills[1] == fills[2] {
			t.Errorf("%s on %s: the fills collapse: %v", pair[0], pair[1], fills)
		}
	}
}
