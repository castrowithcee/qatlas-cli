package tui

import (
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
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
	profileNone
)

// profile and darkBackground are what Run found the terminal to show; tests set them.
var (
	profile        = profileNone
	darkBackground = true
)

// toColorprofile is the profile of the colour library. No colour is NoTTY, which drops bold, faint and
// reverse as well, as the output always did without colour; none of them carries a meaning alone.
func toColorprofile(p colorProfile) colorprofile.Profile {
	switch p {
	case profile256:
		return colorprofile.ANSI256
	case profile16:
		return colorprofile.ANSI
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
	}
	return profileNone
}

// detectProfile is the colour profile of the terminal behind out. NO_COLOR turns colour off with any value,
// not only with the values the colour library reads as true.
func detectProfile(out io.Writer) colorProfile {
	if os.Getenv("NO_COLOR") != "" {
		return profileNone
	}
	return fromColorprofile(colorprofile.Detect(out, os.Environ()))
}

func currentProfile() colorProfile { return profile }

func setProfile(p colorProfile) { profile = p }

// detectBackground asks the terminal for its background once, before the program reads the input, so a later
// switch to the automatic colour scheme needs no second question.
func detectBackground(in, out *os.File) { darkBackground = lipgloss.HasDarkBackground(in, out) }

func hasDarkBackground() bool { return darkBackground }

func setHasDarkBackground(dark bool) { darkBackground = dark }

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
