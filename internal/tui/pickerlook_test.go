package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// lookScreen builds one picker or setup screen on the model of its test.
type lookScreen struct {
	name string
	// sizes are the terminal sizes the screen is held to; nil stands for the usual ones. A screen whose
	// content needs more room than the smallest terminal gives is shown from the sizes it fit before.
	sizes [][2]int
	// open returns a model showing the screen; it may be sized by the caller afterwards.
	open func(t *testing.T) *Model
}

func lookScreens() []lookScreen {
	return []lookScreen{
		{"value picker", nil, func(t *testing.T) *Model {
			m, _, _ := newModel(t)
			seedConnections(t, m, numberedConnections(30)...)
			openDefaultForm(t, m)
			press(t, m, "/")
			return m
		}},
		{"tool picker", nil, func(t *testing.T) *Model {
			m, _ := toolsModel(t, wikiRegistry(t), nil)
			openSectionByName(t, m, sectionConnections)
			pressNew(t, m)
			typeText(t, m, "fresh")
			focusField(t, m, accessLabel)
			selectChoice(t, m, accessTools)
			focusField(t, m, toolListLabel)
			press(t, m, "enter")
			press(t, m, " ", "down", "down")
			return m
		}},
		{"provider table", nil, func(t *testing.T) *Model {
			m, _ := providerModel(t, providerRegistry(t, 8))
			openProviderRow(t, m, sectionServices)
			press(t, m, "enter")
			return m
		}},
		{"target picker", nil, func(t *testing.T) *Model {
			src := &listSource{list: pickPage(group("a"), group("b"), item("g/a", "1"))}
			m := pickerModel(t, src)
			openPicker(t, m)
			press(t, m, "tab")
			return m
		}},
		{"template question", nil, func(t *testing.T) *Model {
			m, _ := templateModel(t, "alpha", "beta")
			openSectionByName(t, m, sectionConnections)
			m.screen = screenList
			press(t, m, "n")
			return m
		}},
		{"template picker", nil, func(t *testing.T) *Model {
			m, _ := templateModel(t, "alpha", "beta")
			openSectionByName(t, m, sectionConnections)
			m.screen = screenList
			press(t, m, "n", "down", "enter")
			return m
		}},
		{"setup summary", [][2]int{{99, 18}, {100, 20}, {60, 24}, {40, 30}, {150, 42}}, func(t *testing.T) *Model {
			m, _ := templateModel(t)
			m.screen = screenNav
			press(t, m, "c", "down", "enter", "enter", "f2", "f2", "f2", "f2")
			if m.screen != screenSummary {
				t.Fatalf("screen %v, want the summary", m.screen)
			}
			return m
		}},
		{"setup step", nil, func(t *testing.T) *Model {
			m, _ := templateModel(t)
			m.screen = screenNav
			press(t, m, "c", "down", "enter", "enter", "f2", "f2")
			return m
		}},
	}
}

// Every picker and setup screen holds its size at the smallest terminal the editor serves and at the sizes
// where its frame appears, with and without colour: nothing is wider or taller than the terminal, no
// escape sequence is left without colour, and the marked row still leads with '>'.
func TestPickerAndSetupScreensFitEverySizeAndReadWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	usual := [][2]int{{40, minimumHeight}, {59, 15}, {60, 16}, {80, 24}, {150, 42}}
	for _, screen := range lookScreens() {
		sizes := screen.sizes
		if sizes == nil {
			sizes = usual
		}
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", screen.name, size[0], size[1]), func(t *testing.T) {
				m := screen.open(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI} {
					lipgloss.SetColorProfile(profile)
					view := screenOf(m)
					if strings.Contains(view, "Resize terminal") {
						t.Fatalf("the screen is replaced by the resize notice:\n%s", view)
					}
					assertViewFits(t, view, size[0], size[1])
					if profile == termenv.Ascii {
						if strings.Contains(view, "\x1b") {
							t.Errorf("without colour the screen still emits escape sequences:\n%s", view)
						}
					}
				}
			})
		}
	}
}

// A ticked entry shows its tick in the colour of success where there is colour, and the characters alone
// tell it from an unticked one where there is none.
func TestTickedEntriesShowAColouredTickAndReadWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	m := lookScreens()[1].open(t)
	// The screen has ticked the effect row of "read"; unfold the first group to see tool rows.
	for !isGroupEntry(m.picker.matches[m.picker.cursor]) {
		press(t, m, "down")
	}
	press(t, m, "right")
	ticked, open := -1, -1
	for i, name := range m.picker.matches {
		if m.pickerGroups.isRow(name) || isEffectRow(name) {
			continue
		}
		if m.pickerMarks[name] && ticked < 0 {
			ticked = i
		} else if !m.pickerMarks[name] && open < 0 {
			open = i
		}
	}
	if ticked < 0 || open < 0 {
		t.Fatalf("no ticked and unticked tool row on screen: %v", m.picker.matches)
	}

	lipgloss.SetColorProfile(termenv.ANSI)
	if row := m.pickerRowText(ticked); !strings.Contains(row, styles.ok.Render("[x]")) {
		t.Errorf("the ticked row has no coloured tick: %q", row)
	}
	if row := m.pickerRowText(open); strings.Contains(row, styles.ok.Render("[x]")) || !strings.Contains(row, "[ ]") {
		t.Errorf("the unticked row reads like a ticked one: %q", row)
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if row := m.pickerRowText(ticked); strings.Contains(row, "\x1b") || !strings.Contains(row, "[x] ") {
		t.Errorf("without colour the tick is not plain text: %q", row)
	}
}

func isGroupEntry(name string) bool { return strings.HasPrefix(name, groupRowPrefix) }

// On a wide terminal the steps of the setup stand as the numbered list the sections of a form use, with the
// current one marked; the summary is a panel of aligned labels and values with status marks. A narrow
// terminal keeps the heading line alone.
func TestSetupStepsAndSummaryUseTheListAndPanelBlocks(t *testing.T) {
	step := lookScreens()[7].open(t)
	step.Update(tea.WindowSizeMsg{Width: 150, Height: 42})
	view := screenOf(step)
	for _, want := range []string{"1 Provider", "> 4 Scope", "6 Summary"} {
		if !strings.Contains(view, want) {
			t.Errorf("the wide setup step does not show %q:\n%s", want, view)
		}
	}
	step.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if view := screenOf(step); strings.Contains(view, "1 Provider") || !strings.Contains(view, "step 4 of 6") {
		t.Errorf("the narrow setup step shows the list or lost its heading:\n%s", view)
	}

	summary := lookScreens()[6].open(t)
	summary.Update(tea.WindowSizeMsg{Width: 150, Height: 42})
	view = screenOf(summary)
	for _, want := range []string{"> 6 Summary", "─ Summary ─", "service ", "● "} {
		if !strings.Contains(view, want) {
			t.Errorf("the wide summary does not show %q:\n%s", want, view)
		}
	}
}
