package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// hintsFit fails for every shown row of fields whose hint is longer than the line under the focused row,
// which at the width of the test is the workspace of an 80-column terminal without the info mark.
func hintsFit(t *testing.T, m *Model, what string, fields []field) {
	t.Helper()
	_, room := m.fit("    ")
	room -= lipgloss.Width(infoMark)
	for _, f := range fields {
		if f.hidden {
			continue
		}
		hint := strings.Join(strings.Fields(m.fieldHint(f)), " ")
		if width := lipgloss.Width(hint); width > room {
			t.Errorf("%s: the hint of %q is %d cells, want at most %d: %q", what, f.label, width, room, hint)
		}
	}
}

// Every hint of every form is one line at 80 columns, for each provider whose rows depend on it; the longer
// explanation is the detail behind the info key.
func TestEveryFormHintIsOneLineAt80Columns(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m, _ := providerModel(t, providerRegistry(t, 8))
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 60})

		for _, s := range []section{sectionGeneral, sectionVault, sectionTransfer, sectionUpdate, sectionSelftest} {
			m.section = s
			m.openPageForm()
			hintsFit(t, m, s.title(), m.fields)
		}

		m.section = sectionTokens
		hintsFit(t, m, "token", m.tokenFields(""))

		if len(m.cfg.Providers()) != 8 {
			t.Fatalf("providers = %v, want the registered providers to walk through", m.cfg.Providers())
		}
		for _, provider := range m.cfg.Providers() {
			m.wizard = &setup{provider: provider}
			for step := 0; step < setupSteps; step++ {
				hintsFit(t, m, provider+" setup step", m.setupPage(step))
			}
			m.wizard = nil

			for _, s := range []section{sectionServices, sectionCredentials, sectionConnections} {
				m.section = s
				m.openForm("")
				focusField(t, m, providerLabel)
				selectChoice(t, m, provider)
				hintsFit(t, m, provider+" "+s.title(), m.fields)
				press(t, m, "esc")
				if m.screen == screenLeave {
					press(t, m, "d")
				}
			}
		}

		m.section = sectionCredentials
		hintsFit(t, m, "payload secret", m.payloadFields("", ""))
		m.section = sectionDefaults
		hintsFit(t, m, "default", m.buildFields(""))
	})
}

// The tool picker names a tool by its title beside the ID and the effect; the description is the
// explanation behind the info key, not a line of the list.
func TestTheToolPickerShowsTheTitleNotTheDescription(t *testing.T) {
	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "fresh")
	focusField(t, m, accessLabel)
	selectChoice(t, m, accessTools)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	if m.screen != screenPicker {
		t.Fatalf("enter on the tool list opened screen %v, want the picker", m.screen)
	}
	typeText(t, m, "pages.list")

	metadata, _ := m.cfg.ProviderMetadata("bookstack")
	var tool config.ToolMetadata
	for _, candidate := range metadata.Tools {
		if candidate.ID == "bookstack.pages.list" {
			tool = candidate
		}
	}
	if tool.Title == "" || tool.Description == "" {
		t.Fatalf("tool %+v lacks a title or a description to compare", tool)
	}
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if want := tool.Title + " " + tool.ID + " " + string(tool.Effect); !strings.Contains(view, want) {
		t.Errorf("the picker row does not read %q:\n%s", want, view)
	}
	if strings.Contains(view, tool.Description) {
		t.Errorf("the picker shows the description %q:\n%s", tool.Description, view)
	}
	if got := m.pickerChoiceSubject(m.fields[m.focus], tool.ID).long; got != tool.Description {
		t.Errorf("the explanation of the marked tool = %q, want its description %q", got, tool.Description)
	}
}

// On a row that is not active the ID and what follows it are faint, the title is not; the active row is
// highlighted as every other picker row is, and with no colour the same text is all there is.
func TestTheToolPickerDimsTheIDOfInactiveRows(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "fresh")
	focusField(t, m, accessLabel)
	selectChoice(t, m, accessTools)
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	typeText(t, m, "pages.list")
	index := -1
	for i, match := range m.picker.matches {
		if match == "bookstack.pages.list" {
			index = i
		}
	}
	if index < 0 || m.picker.cursor == index {
		t.Fatalf("tool row at %d, cursor %d: want the tool row below the cursor", index, m.picker.cursor)
	}

	lipgloss.SetColorProfile(2) // termenv.ANSI: a colour terminal
	faint := "\x1b[" + strings.TrimSuffix(strings.TrimPrefix(styles.hint.Render("x"), "\x1b["), "mx\x1b[0m") + "m"
	row := m.pickerRowText(index)
	at := strings.Index(row, faint)
	if at < 0 || !strings.HasPrefix(row[at+len(faint):], "bookstack.pages.list") ||
		strings.Index(row, "List BookStack pages") > at {
		t.Errorf("the inactive row does not dim from the ID on: %q", row)
	}
	lipgloss.SetColorProfile(3) // termenv.Ascii: what NO_COLOR also produces
	if row := m.pickerRowText(index); strings.Contains(row, "\x1b[") ||
		!strings.Contains(row, "List BookStack pages  bookstack.pages.list  read") {
		t.Errorf("without colour the row is not plain readable text: %q", row)
	}

	for m.picker.cursor != index {
		press(t, m, "down")
	}
	lipgloss.SetColorProfile(2)
	if row := m.pickerRowText(index); !strings.Contains(row, "\x1b[") || strings.Contains(row, faint) {
		t.Errorf("the active row is not highlighted as the other picker rows are: %q", row)
	}
}
