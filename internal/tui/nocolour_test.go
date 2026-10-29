package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// noColourProfiles are the profiles that show no colour, with whether they keep bold, faint and reverse.
var noColourProfiles = []struct {
	name       string
	profile    colorProfile
	attributes bool
}{
	{"ascii", profileASCII, true},
	{"none", profileNone, false},
}

func TestNoColourProfilesAreColourless(t *testing.T) {
	for _, p := range []colorProfile{profileTrueColor, profile256, profile16} {
		if p.colourless() {
			t.Errorf("profile %v is colourless", p)
		}
	}
	for _, p := range noColourProfiles {
		if !p.profile.colourless() {
			t.Errorf("%s is not colourless", p.name)
		}
	}
	if toColorprofile(profileASCII) != colorprofile.ASCII || toColorprofile(profileNone) != colorprofile.NoTTY {
		t.Error("the no-colour profiles do not map to ASCII and NoTTY")
	}
}

// NO_COLOR at a terminal is the ASCII profile (attributes stay), TERM=dumb and a missing terminal stay NoTTY.
func TestDetectProfileKeepsAttributesUnderNoColourOnlyAtATerminal(t *testing.T) {
	restoreRenderer(t)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	for _, value := range []string{"1", "yes"} {
		t.Setenv("NO_COLOR", value)
		if got := detectProfile(io.Discard); got != profileASCII {
			t.Errorf("NO_COLOR=%s at a terminal: profile %v, want ascii", value, got)
		}
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("COLORTERM", "")
	if got := detectProfile(io.Discard); got != profileNone {
		t.Errorf("without a terminal: profile %v, want none", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := detectProfile(io.Discard); got != profileNone {
		t.Errorf("NO_COLOR without a terminal: profile %v, want none", got)
	}
	t.Setenv("TERM", "dumb")
	if got := detectProfile(io.Discard); got != profileNone {
		t.Errorf("TERM=dumb: profile %v, want none", got)
	}
}

// Every profile without colour takes the theme without colour, and the focus stays readable without it.
func TestNoColourProfilesKeepTheFocusReadable(t *testing.T) {
	for _, pc := range noColourProfiles {
		t.Run(pc.name, func(t *testing.T) {
			restoreRenderer(t)
			setProfile(pc.profile)
			setTheme(darkTheme())
			applyTheme("")
			if styles.palette != (palette{}) || defaultTheme().palette != (palette{}) {
				t.Fatal("a profile without colour names a palette")
			}
			setTheme(darkTheme())
			for _, name := range themeNames() {
				applyTheme(name)
				if styles.palette != (palette{}) || styles.filled {
					t.Errorf("theme %s keeps colour without colour", name)
				}
			}

			m := sizedModel(groupModel(t), 120, 30)
			openSectionByName(t, m, sectionConnections)
			view := m.render()
			plain := stripSGR(view)
			if pc.attributes {
				if !strings.Contains(view, "\x1b[1m") && !strings.Contains(view, "\x1b[7m") {
					t.Errorf("no attribute survives:\n%q", view)
				}
				for _, seq := range []string{"38;", "48;", "\x1b[3", "\x1b[4"} {
					if strings.Contains(view, seq) {
						t.Errorf("the view holds the colour sequence %q", seq)
					}
				}
			} else if strings.Contains(view, "\x1b") {
				t.Errorf("the view holds escape sequences:\n%q", view)
			}
			for _, want := range []string{"╔", "║", "╝", ">", "[2 Connections]"} {
				if !strings.Contains(plain, want) {
					t.Errorf("the view lacks %q:\n%s", want, plain)
				}
			}
			if got := stripSGR(button(buttonPrimary, "Store", true)); got != "> Store <" {
				t.Errorf("focused button = %q, want %q", got, "> Store <")
			}
			if got := stripSGR(button(buttonPrimary, "Store", false)); got != "[ Store ]" {
				t.Errorf("button = %q, want %q", got, "[ Store ]")
			}
			if h, _, c := frameChars(true); h != "═" || c[0] != "╔" {
				t.Error("a focused frame is not double")
			}
			if h, _, c := frameChars(false); h != "─" || c[0] != "╭" {
				t.Error("an unfocused frame is not rounded")
			}
		})
	}
}

func stripSGR(s string) string { return sgr.ReplaceAllString(s, "") }
