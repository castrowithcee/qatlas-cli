package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// withoutUTF8 makes the terminal one that draws no UTF-8, through the real detection: the environment says so
// (kind is the variable set), whatever locale the machine has.
func withoutUTF8(t *testing.T, kind string) {
	t.Helper()
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	switch kind {
	case "dumb":
		t.Setenv("TERM", "dumb")
	default:
		t.Setenv("TERM", "xterm-256color")
		t.Setenv("LC_ALL", "C")
	}
	if utf8Terminal() {
		t.Fatalf("%s still counts as UTF-8", kind)
	}
}

func TestDetectUTF8(t *testing.T) {
	for _, c := range []struct {
		term, all, lang string
		want            bool
	}{
		{"xterm", "", "", true},
		{"xterm", "C.UTF-8", "", true},
		{"xterm", "C", "de_DE.UTF-8", false},
		{"xterm", "", "en_US.utf8", true},
		{"xterm", "", "C", false},
		{"dumb", "C.UTF-8", "", false},
	} {
		t.Setenv("TERM", c.term)
		t.Setenv("LC_ALL", c.all)
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", c.lang)
		if got := utf8Terminal(); got != c.want {
			t.Errorf("TERM=%s LC_ALL=%s LANG=%s: %v, want %v", c.term, c.all, c.lang, got, c.want)
		}
	}
}

// Every symbol the screens draw has an ASCII form of one cell, and the table leaves umlauts and ß alone.
func TestASCIIGlyphsKeepWidth(t *testing.T) {
	withoutUTF8(t, "c")
	for _, g := range []string{"─", "│", "╭", "╮", "╰", "╯", "▌", "▸", "▾", "■", "●", "⚠", "✓", "✗", "…", "·", "×", "•", "›",
		"←", "→", "↑", "↓", "„", "“", "░", "▒", "▓", "█", "�"} {
		got := asciiOnly(g)
		if len(got) != 1 || got[0] >= 0x80 {
			t.Errorf("%q becomes %q, not one ASCII character", g, got)
		}
	}
	if got := asciiOnly("Größe äöüÄÖÜ"); got != "Größe äöüÄÖÜ" {
		t.Errorf("umlauts changed: %q", got)
	}
	if got := asciiOnly("\x1b[38;2;1;2;3m…\x1b[0m"); got != "\x1b[38;2;1;2;3m~\x1b[0m" {
		t.Errorf("escape sequence changed: %q", got)
	}
}

func TestFrameCharsWithoutUTF8(t *testing.T) {
	withoutUTF8(t, "c")
	restoreRenderer(t)
	for _, p := range []colorProfile{profileTrueColor, profileASCII} {
		setProfile(p)
		h, v, c := frameChars(false)
		if h != "-" || v != "|" || c[0] != "+" {
			t.Errorf("plain frame = %q %q %q", h, v, c)
		}
		h, v, c = frameChars(true)
		wantFocus := p == profileASCII
		if gotFocus := h == "=" && v == "#" && c[0] == "#"; gotFocus != wantFocus {
			t.Errorf("profile %v: focused frame = %q %q %q", p, h, v, c)
		}
	}
}

// Every screen is ASCII apart from umlauts, and every line is as wide as with UTF-8, in the same state.
func TestASCIIScreensKeepWidth(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	for _, kind := range []string{"c", "dumb"} {
		for _, shot := range visualShots() {
			t.Run(kind+"/"+shot.name, func(t *testing.T) {
				visualSetup(t, shot.noColor)
				m := visualModel(t, fixtures, shot.width, shot.height, shot.theme)
				shot.show(t, m)
				// Icons vanish without UTF-8 by design; the comparison is of the other symbols.
				m.prefs.Icons = uiprefs.IconsNone
				want := strings.Split(m.render(), "\n")
				withoutUTF8(t, kind)
				got := strings.Split(m.render(), "\n")
				if len(got) != len(want) {
					t.Fatalf("%d lines, want %d", len(got), len(want))
				}
				for i, line := range got {
					if w, g := ansi.StringWidth(want[i]), ansi.StringWidth(line); w != g {
						t.Errorf("line %d is %d cells wide, want %d:\n%q\n%q", i+1, g, w, line, want[i])
					}
					for _, r := range ansi.Strip(line) {
						if r > 0x7f && !strings.ContainsRune("äöüÄÖÜß", r) {
							t.Errorf("line %d holds %q:\n%q", i+1, r, line)
							break
						}
					}
				}
			})
		}
	}
}

// asciiShot is a snapshot of a terminal without UTF-8.
type asciiShot struct {
	visualShot
	kind string
}

func TestVisualSnapshotsASCII(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	dark := uiprefs.ThemeQatlasDark
	connections := func(t *testing.T, m *Model) { pump(t, m, "3") }
	shots := []asciiShot{
		{visualShot{"90-overview-ascii", 80, 24, dark, false, func(*testing.T, *Model) {}}, "c"},
		{visualShot{"91-connections-ascii-no-color", 80, 24, dark, true, connections}, "dumb"},
		{visualShot{"92-connection-form-ascii", 80, 24, dark, false, func(t *testing.T, m *Model) {
			connections(t, m)
			pump(t, m, "enter")
		}}, "c"},
	}
	for _, shot := range shots {
		t.Run(shot.name, func(t *testing.T) {
			visualSetup(t, shot.noColor)
			m := visualModel(t, fixtures, shot.width, shot.height, shot.theme)
			shot.show(t, m)
			withoutUTF8(t, shot.kind)
			view := m.render()
			if shot.noColor && !strings.Contains(view, "#=") {
				t.Errorf("the focused zone shows no ASCII focus frame:\n%s", view)
			}
			golden := filepath.Join(fixtures, shot.name+"-80x24.ans")
			if *updateVisual {
				mustNoError(t, os.WriteFile(golden, []byte(view), 0o644))
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(want, []byte(view)) {
				t.Errorf("snapshot differs from %s%s", golden, firstDifference(string(want), view))
			}
		})
	}
}
