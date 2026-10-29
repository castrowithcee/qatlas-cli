package tui

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// This file is the only place in the package that talks to the colour library; everything else uses
// colorProfile and these helpers. Lip Gloss renders every colour at full depth: the profile picks the palette
// (see paletteOf), and shown steps whatever else a frame holds down to it.

// colorProfile is how many colours the output can show.
type colorProfile int

const (
	profileTrueColor colorProfile = iota
	profile256
	profile16
	// profileASCII shows no colour but keeps bold, faint and reverse (NO_COLOR at a terminal).
	profileASCII
	// profileNone shows neither colour nor attributes (no terminal, TERM=dumb).
	profileNone
)

// profile is what Run found the terminal to show; tests set it.
var profile = profileNone

// colourless reports whether a profile shows no colour. Every fallback without colour (frame, selection
// mark, tab brackets, heat glyphs, canvas, terminal colour query) asks it, so none hangs on a palette field.
func (p colorProfile) colourless() bool { return p == profileASCII || p == profileNone }

// noColour reports whether the current profile shows no colour.
func noColour() bool { return currentProfile().colourless() }

// toColorprofile is the profile of the colour library. ASCII keeps bold, faint and reverse; NoTTY drops them
// as well, as a terminal that shows nothing does.
func toColorprofile(p colorProfile) colorprofile.Profile {
	switch p {
	case profile256:
		return colorprofile.ANSI256
	case profile16:
		return colorprofile.ANSI
	case profileASCII:
		return colorprofile.ASCII
	case profileNone:
		return colorprofile.NoTTY
	}
	return colorprofile.TrueColor
}

func fromColorprofile(p colorprofile.Profile) colorProfile {
	switch p {
	case colorprofile.TrueColor:
		return profileTrueColor
	case colorprofile.ANSI256:
		return profile256
	case colorprofile.ANSI:
		return profile16
	case colorprofile.ASCII:
		return profileASCII
	}
	return profileNone
}

// detectProfile is the colour profile of the terminal behind out. NO_COLOR turns colour off with any value,
// not only with the values the colour library reads as true, and keeps the attributes of a terminal that
// shows any; without a terminal or with TERM=dumb nothing is shown at all.
func detectProfile(out io.Writer) colorProfile {
	p := fromColorprofile(colorprofile.Detect(out, os.Environ()))
	if os.Getenv("NO_COLOR") != "" && p != profileNone {
		return profileASCII
	}
	return p
}

func currentProfile() colorProfile { return profile }

func setProfile(p colorProfile) { profile = p }

// shown is a frame as the terminal of the current profile is sent it, so what render returns is what the
// terminal shows. Bubble Tea steps the frame down to the same profile, which leaves it unchanged.
func shown(frame string) string {
	frame = strings.ReplaceAll(frame, cursorMark, "")
	if profile == profileTrueColor {
		return frame
	}
	var b strings.Builder
	w := colorprofile.Writer{Forward: &b, Profile: toColorprofile(profile)}
	_, _ = w.WriteString(frame)
	return b.String()
}
