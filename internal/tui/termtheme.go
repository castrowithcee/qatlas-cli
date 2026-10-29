package tui

import (
	"fmt"
	"image/color"
	"math"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// rgb is a colour with 8 bits per channel.
type rgb struct{ r, g, b uint8 }

func rgbOf(c color.Color) rgb {
	r, g, b, _ := c.RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

func (c rgb) hex() colour { return colour(fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b)) }

// mix is the colour share of the way from bg to fg.
func mix(bg, fg rgb, share float64) rgb {
	step := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + (float64(b)-float64(a))*share)) }
	return rgb{step(bg.r, fg.r), step(bg.g, fg.g), step(bg.b, fg.b)}
}

// luminance is the relative luminance of a colour as WCAG defines it.
func luminance(c rgb) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.r) + 0.7152*channel(c.g) + 0.0722*channel(c.b)
}

// contrast is the WCAG contrast ratio of two colours, from 1 to 21.
func contrast(a, b rgb) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The least contrast of text and of dimmed text on the colours a mixed palette puts behind them.
const (
	textContrast = 4.5
	dimContrast  = 3.0
)

// The shares of the foreground in the mixed roles, close to the steps of the qatlas palettes. The fills are
// the most a pair with a contrast well above textContrast keeps; a tighter pair scales them down together.
const (
	sharePanel     = 0.06
	shareSurface   = 0.12
	shareSelection = 0.20
	shareBorder    = 0.28
	shareTextDim   = 0.60
)

// shareHeat are the shares of the heat levels; level 0, the empty cell, is a step of the foreground, the
// others lead to the green of the scheme where it is known and to the foreground where it is not.
var shareHeat = [5]float64{0.15, 0.35, 0.55, 0.78, 1}

// The ANSI slots of the roles a terminal theme leaves to the colours of the terminal.
const (
	slotFail, slotOK, slotWarn, slotAccent, slotDim = 1, 2, 3, 4, 8
)

// brandSlots avoid the bright yellow and green, which vanish on a light background.
var brandSlots = [8]int{3, 4, 2, 6, 5, 1, 13, 12}

// mixPalette is the palette of a scheme of foreground fg on background bg: the surfaces, the borders and the
// dimmed text are steps between the two, the accents its ANSI colours. Without ansi the accents name the ANSI
// slots, so the terminal colours them, and the text keeps the terminal's own foreground. It reports false for
// a pair whose own contrast is below textContrast, since every fill would lower it further, and for one so
// close to it that the fills could not be told apart.
func mixPalette(fg, bg rgb, ansi *[16]rgb) (palette, bool) {
	if contrast(fg, bg) < textContrast {
		return palette{}, false
	}
	fill := func(share float64) rgb { return mix(bg, fg, share) }
	var scale float64
	for step := 20; step >= 0; step-- {
		scale = float64(step) / 20
		if contrast(fg, fill(sharePanel*scale)) >= textContrast && contrast(fg, fill(shareSurface*scale)) >= textContrast &&
			contrast(fg, fill(shareSelection*scale)) >= textContrast {
			break
		}
	}
	panel, surface, selection := fill(sharePanel*scale), fill(shareSurface*scale), fill(shareSelection*scale)
	if panel == bg || surface == panel || selection == surface {
		return palette{}, false
	}
	// The dimmed text moves towards the text until it reads; at the text itself it always does.
	var dim rgb
	for step := int(shareTextDim * 100); step <= 100; step++ {
		dim = fill(float64(step) / 100)
		if contrast(dim, bg) >= dimContrast && contrast(dim, panel) >= dimContrast {
			break
		}
	}
	slot := func(i int) colour {
		if ansi == nil {
			return colour(strconv.Itoa(i))
		}
		return ansi[i].hex()
	}
	p := palette{
		base: bg.hex(), surface: surface.hex(), panel: panel.hex(),
		border: fill(shareBorder).hex(), borderFocus: slot(slotAccent),
		textDim: dim.hex(),
		accent:  slot(slotAccent), ok: slot(slotOK), fail: slot(slotFail), warn: slot(slotWarn),
		selection: selection.hex(),
	}
	green := fg
	if ansi != nil {
		p.text = fg.hex()
		green = ansi[slotOK]
	}
	p.heat[0] = fill(shareHeat[0]).hex()
	for i := 1; i < len(p.heat); i++ {
		p.heat[i] = mix(bg, green, shareHeat[i]).hex()
	}
	for i, s := range brandSlots {
		p.brand[i] = slot(s)
	}
	return p, true
}

// plainTerminalPalette is the terminal theme where its colours are unknown or cannot be mixed: no fills, panels
// stand by their frames, and the selection is reverse video (see buildTheme). The markers keep telling focus
// and selection apart, as they do without colour.
func plainTerminalPalette() palette {
	slot := func(i int) colour { return colour(strconv.Itoa(i)) }
	p := palette{
		border: slot(slotDim), borderFocus: slot(slotAccent), textDim: slot(slotDim),
		accent: slot(slotAccent), ok: slot(slotOK), fail: slot(slotFail), warn: slot(slotWarn),
	}
	for i, s := range brandSlots {
		p.brand[i] = slot(s)
	}
	return p
}

// termFg and termBg are the colours the terminal answered; nil until it has.
var termFg, termBg *rgb

// terminalPalette is the palette of the terminal theme: mixed from the colours of the terminal once both are
// known and it shows more than 16 colours, plain before and otherwise.
func terminalPalette(profile colorProfile) palette {
	if termFg == nil || termBg == nil || (profile != profileTrueColor && profile != profile256) {
		return plainTerminalPalette()
	}
	p, ok := mixPalette(*termFg, *termBg, nil)
	if ok && profile == profile256 {
		p, ok = steppedTo256(p, *termFg, *termBg)
	}
	if !ok {
		return plainTerminalPalette()
	}
	return p
}

// rgbOfHex is the colour of a "#rrggbb" role; false for an ANSI index or none.
func rgbOfHex(c colour) (rgb, bool) {
	var v rgb
	if _, err := fmt.Sscanf(string(c), "#%02x%02x%02x", &v.r, &v.g, &v.b); err != nil {
		return rgb{}, false
	}
	return v, true
}

// xterm256 is the colour of an index of the xterm cube (16 to 231) or grey ramp (232 to 255).
func xterm256(i int) rgb {
	if i >= 232 {
		v := uint8(8 + 10*(i-232))
		return rgb{v, v, v}
	}
	i -= 16
	level := func(v int) uint8 {
		if v == 0 {
			return 0
		}
		return uint8(55 + 40*v)
	}
	return rgb{level(i / 36), level(i / 6 % 6), level(i % 6)}
}

// nearest256 is the index of the cube or grey ramp closest to c. The 16 slots below are left out: the
// terminal's scheme defines them.
func nearest256(c rgb) int {
	best, bestDistance := 16, math.MaxInt
	for i := 16; i < 256; i++ {
		x := xterm256(i)
		dr, dg, db := int(c.r)-int(x.r), int(c.g)-int(x.g), int(c.b)-int(x.b)
		if d := dr*dr + dg*dg + db*db; d < bestDistance {
			best, bestDistance = i, d
		}
	}
	return best
}

// steppedTo256 names every mixed role of p by its nearest index, since the generic step-down may give a grey
// a coloured neighbour. It reports false where the steps would lose their contrast or could not be told apart.
func steppedTo256(p palette, fg, bg rgb) (palette, bool) {
	step := func(c *colour) rgb {
		v, ok := rgbOfHex(*c)
		if !ok {
			return rgb{}
		}
		i := nearest256(v)
		*c = colour(strconv.Itoa(i))
		return xterm256(i)
	}
	step(&p.base)
	step(&p.border)
	for i := range p.heat {
		step(&p.heat[i])
	}
	panel, surface, selection, dim := step(&p.panel), step(&p.surface), step(&p.selection), step(&p.textDim)
	for _, fill := range []rgb{panel, surface, selection} {
		if contrast(fg, fill) < textContrast {
			return palette{}, false
		}
	}
	if contrast(dim, bg) < dimContrast || contrast(dim, panel) < dimContrast ||
		panel == xterm256(nearest256(bg)) || surface == panel || selection == surface {
		return palette{}, false
	}
	return p, true
}

// terminalColourQuery asks the terminal for its foreground and background where the theme mixes from them.
// Nothing is asked of a terminal whose colours the theme could not use.
func terminalColourQuery(name string) tea.Cmd {
	t, _ := themeNamed(name)
	p := currentProfile()
	if t.name != uiprefs.ThemeTerminal || (p != profileTrueColor && p != profile256) {
		return nil
	}
	return tea.Batch(tea.RequestForegroundColor, tea.RequestBackgroundColor)
}

// takeTerminalColour keeps a colour the terminal answered and draws the terminal theme anew with it.
func (m *Model) takeTerminalColour(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.ForegroundColorMsg:
		if msg.Color == nil {
			return
		}
		c := rgbOf(msg.Color)
		termFg = &c
	case tea.BackgroundColorMsg:
		if msg.Color == nil {
			return
		}
		c := rgbOf(msg.Color)
		termBg = &c
	}
	if t, _ := themeNamed(m.prefs.Theme); t.name == uiprefs.ThemeTerminal {
		applyTheme(t.name)
	}
}
