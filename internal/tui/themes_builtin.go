package tui

// builtinScheme is a scheme of a collection: its foreground, background and the 16 ANSI colours of a terminal
// that has it installed. The theme of a scheme is what the terminal theme would be in such a terminal.
type builtinScheme struct {
	name   string
	fg, bg rgb
	ansi   [16]rgb
}

// builtinThemes are the themes of the built-in schemes. They paint the screen, since their colours are known.
func builtinThemes() []themeDef {
	defs := make([]themeDef, len(builtinSchemes))
	for i, s := range builtinSchemes {
		defs[i] = themeDef{s.name, true, s.palette}
	}
	return defs
}

// palette is the palette of the scheme for the colour profile, mixed as the terminal theme mixes the colours of
// the terminal. With 16 colours, or where the mix fails, it is the plain terminal palette, which names no base
// and so leaves the ground to the terminal (see applyTheme).
func (s builtinScheme) palette(profile colorProfile) palette {
	if profile != profileTrueColor && profile != profile256 {
		return plainTerminalPalette()
	}
	p, ok := mixPalette(s.fg, s.bg, &s.ansi)
	if ok && profile == profile256 {
		p, ok = steppedTo256(p, s.fg, s.bg)
	}
	if !ok {
		return plainTerminalPalette()
	}
	return p
}
