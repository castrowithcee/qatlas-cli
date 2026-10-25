package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// formScreens are the forms of the editor, each opened on a demo setup.
var formScreens = []struct {
	name string
	open func(t *testing.T, m *Model)
}{
	{"connection", func(t *testing.T, m *Model) { pump(t, m, "2", "enter") }},
	{"service", func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices); m.openForm("") }},
	{"credential", func(t *testing.T, m *Model) { openSectionByName(t, m, sectionCredentials); m.openForm("") }},
	{"token", func(t *testing.T, m *Model) { openSectionByName(t, m, sectionTokens); m.openForm("") }},
	{"settings", func(t *testing.T, m *Model) { openSectionByName(t, m, sectionGeneral) }},
	{"setup step", func(t *testing.T, m *Model) { pump(t, m, "2"); m.startSetup(); pump(t, m, "enter") }},
}

// Every form is usable at the smallest size and without colour: it fits, it is not replaced by the resize
// notice, and the focused row carries a visible marker that does not depend on colour.
func TestFormsStayUsableAtEverySizeAndWithoutColour(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	sizes := []struct{ w, h int }{{minimumWidth, minimumHeight}, {80, 24}, {150, 42}}
	for _, form := range formScreens {
		for _, noColor := range []bool{false, true} {
			for _, size := range sizes {
				name := form.name + "/" + map[bool]string{false: "colour", true: "no colour"}[noColor]
				t.Run(name+"/"+fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
					visualSetup(t, noColor)
					m := visualModel(t, fixtures, size.w, size.h, uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
					form.open(t, m)
					if m.screen != screenForm && m.screen != screenPicker {
						t.Fatalf("screen = %v, want the form", m.screen)
					}
					view := m.View()
					if strings.Contains(view, "Resize terminal") {
						t.Fatalf("the form is replaced by the resize notice:\n%s", view)
					}
					assertViewFits(t, view, size.w, size.h)
					plain := ansi.Strip(view)
					if !strings.Contains(plain, "> ") {
						t.Errorf("no row carries the focus marker:\n%s", plain)
					}
					if noColor && (strings.Contains(view, "38;") || strings.Contains(view, "48;")) {
						t.Errorf("a colour reached a screen without colour:\n%q", view)
					}
				})
			}
		}
	}
}

// A form that does not fit with framed fields falls back to one-line fields at 80x24, and the focused row
// stays on screen with its hint.
func TestAFormTooTallForFramesFallsBackWithoutLosingTheFocus(t *testing.T) {
	fixtures, err := filepath.Abs(visualDir)
	mustNoError(t, err)
	visualSetup(t, false)
	m := visualModel(t, fixtures, 80, 24, uiprefs.ThemeDark, uiprefs.BackgroundTerminal)
	pump(t, m, "2", "enter")
	for range m.fields {
		view := ansi.Strip(m.View())
		label := labelText(m.fields[m.focus].label)
		if strings.Contains(view, "╭") && strings.Contains(view, label+" ") && !strings.Contains(view, "│ ") {
			t.Fatalf("frames without their rows:\n%s", view)
		}
		if !strings.Contains(view, "> "+label) {
			t.Fatalf("the focused row %q is not marked on screen:\n%s", label, view)
		}
		if strings.Contains(view, "╭") {
			t.Fatalf("framed fields at 80x24, where the section does not fit:\n%s", view)
		}
		pump(t, m, "tab")
	}
}

// A masked payload value never reaches the screen in clear text, in a framed field or in a one-line one,
// focused or not.
func TestAMaskedFieldNeverShowsItsTextInAnyFormStyle(t *testing.T) {
	const canary = "canary-form-style-9d41"
	for _, size := range []struct{ w, h int }{{50, 16}, {80, 24}, {150, 42}} {
		h := newPayloadHarness(t, false)
		h.startPayload(t, "kept", storageKeyring, "")
		sizedModel(h.m, size.w, size.h)
		h.addField(t, "pin", canary)
		for range h.m.fields {
			h.press(t, "tab")
		}
		h.neverShown(t, canary)
	}
}
