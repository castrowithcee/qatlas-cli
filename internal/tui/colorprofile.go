package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// This file is the only place in the package that talks to the colour library; everything else uses
// colorProfile and these helpers.

// colorProfile is how many colours the output can show.
type colorProfile int

const (
	profileTrueColor colorProfile = iota
	profile256
	profile16
	profileNone
)

func toTermenv(p colorProfile) termenv.Profile {
	switch p {
	case profile256:
		return termenv.ANSI256
	case profile16:
		return termenv.ANSI
	case profileNone:
		return termenv.Ascii
	}
	return termenv.TrueColor
}

func currentProfile() colorProfile {
	switch lipgloss.ColorProfile() {
	case termenv.ANSI256:
		return profile256
	case termenv.ANSI:
		return profile16
	case termenv.Ascii:
		return profileNone
	}
	return profileTrueColor
}

func setProfile(p colorProfile) { lipgloss.SetColorProfile(toTermenv(p)) }

func hasDarkBackground() bool { return lipgloss.HasDarkBackground() }

func setHasDarkBackground(dark bool) { lipgloss.SetHasDarkBackground(dark) }
