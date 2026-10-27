package tui

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// connectionFormModel opens the form of a new connection in a terminal of the given size.
func connectionFormModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func sectionNames(m *Model) []string {
	var names []string
	for _, e := range m.formSections() {
		names = append(names, e.name)
	}
	return names
}

// The sections stand as a list at the left; pgdown and pgup choose the next and the previous one, and the
// focus lands on the first row of the section chosen.
func TestSectionListAndJumpToASection(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := advancedFormModel(t, 110, 40)
		if got, want := strings.Join(sectionNames(m), ","), "General,Service,Credential,Targets,Tools,Access,Advanced"; got != want {
			t.Fatalf("sections = %s, want %s", got, want)
		}
		view := screenOf(m)
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "> 1 "+sectionText(secGeneral)) {
				goto marked
			}
		}
		t.Fatalf("the list does not mark General:\n%s", view)
	marked:
		if !strings.Contains(view, "> "+labelText("name")) {
			t.Errorf("the focused name row is not marked:\n%s", view)
		}
		if !strings.Contains(view, sectionText(secAdvanced)+tr(tSectionClosedMark)) {
			t.Errorf("the list does not say that Advanced is closed:\n%s", view)
		}
		assertViewFits(t, view, 110, 40)

		press(t, m, "pgdown")
		if got := m.fields[m.focus].label; got != "service" || m.currentSection() != secService {
			t.Fatalf("pgdown focused %q in %q, want the service row", got, m.currentSection())
		}
		press(t, m, "pgdown", "pgdown")
		if m.currentSection() != secTargets {
			t.Fatalf("section = %q, want Targets", m.currentSection())
		}
		press(t, m, "pgup")
		if m.currentSection() != secCredential || m.fields[m.focus].label != "credential" {
			t.Fatalf("pgup focused %q in %q, want the credential row", m.fields[m.focus].label, m.currentSection())
		}
		// tab stays row by row, across the border of a section.
		press(t, m, "tab")
		if m.currentSection() != secTargets || m.fields[m.focus].label != targetsLabel {
			t.Fatalf("tab focused %q in %q, want the next row", m.fields[m.focus].label, m.currentSection())
		}
		// The first row is the end: pgup on the first section stays.
		press(t, m, "pgup", "pgup", "pgup", "pgup")
		if m.currentSection() != secGeneral {
			t.Fatalf("section = %q, want General", m.currentSection())
		}
	})
}

// advancedFormModel opens the form of a new connection whose provider can read local files, so Advanced has a row.
func advancedFormModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m, _, _ := filesModel(t, config.LocalFilesSupport{Read: true, Write: true}, nil)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// Advanced is closed while none of its rows holds a value or a problem, opens when one does, and a jump into it
// opens it.
func TestAdvancedOpensOnAValueAnErrorOrAJump(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := advancedFormModel(t, 110, 40)
		if !m.sectionClosed(secAdvanced) {
			t.Fatal("Advanced is open on a new connection")
		}
		if view := screenOf(m); strings.Contains(view, labelText(filesReadLabel)) {
			t.Fatalf("a closed Advanced shows its rows:\n%s", view)
		}
		before := m.focus
		for range m.fields {
			press(t, m, "tab")
			if m.currentSection() == secAdvanced {
				t.Fatal("tab walked into the closed Advanced section")
			}
		}
		m.focus = before

		for m.currentSection() != secAdvanced {
			press(t, m, "pgdown")
		}
		if m.currentSection() != secAdvanced || m.fields[m.focus].label != filesReadLabel {
			t.Fatalf("jump focused %q in %q, want the first Advanced row", m.fields[m.focus].label, m.currentSection())
		}
		if view := screenOf(m); !strings.Contains(view, labelText(filesReadLabel)) || strings.Contains(view, sectionText(secAdvanced)+tr(tSectionClosedMark)) {
			t.Fatalf("a jump did not open Advanced:\n%s", view)
		}

		// A value keeps it open without the jump.
		m = advancedFormModel(t, 110, 40)
		focusField(t, m, filesReadLabel)
		m.field(filesReadLabel).entries = []string{"/srv/in"}
		press(t, m, "pgup", "pgup", "pgup", "pgup", "pgup", "pgup")
		if m.currentSection() == secAdvanced || m.sectionClosed(secAdvanced) {
			t.Fatalf("a value in Advanced: section %q closed %v, want it open while the focus is elsewhere",
				m.currentSection(), m.sectionClosed(secAdvanced))
		}

		// A problem opens it, and it closes again when the problem is gone.
		m = advancedFormModel(t, 110, 40)
		f := *m.field(filesReadLabel)
		m.problems = problemState{rows: map[string][]string{filesReadLabel: {"refused"}}, at: map[string]string{filesReadLabel: f.value()}}
		if m.sectionClosed(secAdvanced) {
			t.Fatal("a problem in Advanced left it closed")
		}
		if view := screenOf(m); !strings.Contains(view, sectionText(secAdvanced)+alertMark) {
			t.Fatalf("the section list does not say that a problem stands in Advanced:\n%s", view)
		}
		m.enterSection(secAdvanced)
		if view := screenOf(m); !strings.Contains(view, "[!] refused") {
			t.Fatalf("the problem is not shown under its row:\n%s", view)
		}
		m.problems, m.sectionPinned, m.focus = problemState{}, nil, 0
		m.applyFocus()
		if !m.sectionClosed(secAdvanced) {
			t.Fatal("Advanced stays open without a value or a problem")
		}
	})
}

// The value column is as wide as the longest label of the form, so a label longer than twelve cells moves no
// value and every value of a form starts in one column.
func TestLongLabelsMoveNoValues(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
		m.formCol = m.labelColumn("name", "a label of twenty-two", "state")
		short := m.formRow(false, "name", "VALUE")
		long := m.formRow(false, "a label of twenty-two", "VALUE")
		if a, b := strings.Index(short, "VALUE"), strings.Index(long, "VALUE"); a != b || a != 2+len("a label of twenty-two")+1 {
			t.Errorf("value columns %d and %d, want both at %d", a, b, 2+len("a label of twenty-two")+1)
		}
		m.formCol = 0

		// In a real form: the vault page has the label "update behaviour" (16 cells) beside shorter ones.
		m.section = sectionVault
		m.openPageForm()
		view := screenOf(m)
		columns := map[int]string{}
		for _, line := range strings.Split(view, "\n") {
			for _, label := range []string{labelText("state"), labelText("update behaviour"), labelText("idle timeout"), labelText("admin timeout")} {
				rest, ok := strings.CutPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "> "), "  "), label)
				if !ok || !strings.HasPrefix(rest, " ") {
					continue
				}
				if value := strings.TrimLeft(rest, " "); value != "" {
					columns[lipgloss.Width(line)-lipgloss.Width(value)] = label
				}
			}
		}
		if len(columns) != 1 {
			t.Errorf("the rows of the vault form start their values in columns %v, want one:\n%s", columns, view)
		}
		if !strings.Contains(view, labelText("update behaviour")) {
			t.Errorf("the vault form lacks the long label:\n%s", view)
		}
	})
}

// Below the width the list needs, each section is a heading above its rows and the list is gone.
func TestNarrowFormsHaveHeadingsInsteadOfTheList(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		for _, size := range []struct{ w, h int }{{40, 14}, {80, 30}, {sectionListMinWidth - 1, 40}} {
			m := advancedFormModel(t, size.w, size.h)
			view := screenOf(m)
			assertViewFits(t, view, size.w, size.h)
			lines := strings.Split(view, "\n")
			headings := map[string]bool{}
			for _, line := range lines {
				headings[strings.TrimRight(line, " ")] = true
				if strings.HasPrefix(line, "> "+sectionText(secGeneral)) || strings.HasPrefix(line, "  "+sectionText(secGeneral)+" ") {
					t.Errorf("%dx%d: the list is shown below the minimum width: %q", size.w, size.h, line)
				}
			}
			if size.h >= 24 && !headings[sectionText(secGeneral)] {
				t.Errorf("%dx%d: no General heading:\n%s", size.w, size.h, view)
			}
			if size.h >= 24 && (!headings[sectionText(secService)] || !headings[sectionText(secAdvanced)+tr(tSectionClosedMark)]) {
				t.Errorf("%dx%d: headings missing:\n%s", size.w, size.h, view)
			}
		}
		m := advancedFormModel(t, sectionListMinWidth, 40)
		if !m.sectionListFits(m.formSections()) {
			t.Errorf("the list does not stand at the minimum width %d", sectionListMinWidth)
		}
	})
}

// A form fits every size, with and without the list.
func TestSectionedFormsFitEverySize(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		for _, size := range []struct{ w, h int }{{40, 14}, {80, 24}, {160, 40}} {
			m := advancedFormModel(t, size.w, size.h)
			assertViewFits(t, m.render(), size.w, size.h)
			press(t, m, "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
			assertViewFits(t, m.render(), size.w, size.h)
		}
	})
}

// The empty value of every kind of row is one short word that stands without colour.
func TestEmptyValuesAreOneWord(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := connectionFormModel(t, 110, 40)
		view := sectionViews(m)
		// The labels stand in a column as wide as the longest one, description.
		row := func(label, word string) string {
			return labelText(label) + strings.Repeat(" ", lipgloss.Width(labelText("description"))-lipgloss.Width(labelText(label))) + " " + wordText(word)
		}
		for _, want := range []string{row("description", "empty"), row("targets", "any"), row("paths", "any")} {
			if !strings.Contains(view, want) {
				t.Errorf("view lacks %q:\n%s", want, view)
			}
		}
		if strings.Contains(view, "(empty)") || strings.Contains(view, "(none") {
			t.Errorf("an empty value is still a parenthesised phrase:\n%s", view)
		}
	})
}

// The guided setup places its rows in the same sections as the forms, grouped the same way.
func TestTheGuidedSetupUsesTheSections(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m, _ := toolsModel(t, wikiRegistry(t), nil)
		m.wizard = &setup{provider: "bookstack"}
		want := map[int]string{
			stepService:    "Service",
			stepCredential: "Credential",
			stepScope:      "General,Targets",
			stepEffects:    "Tools,Access",
		}
		for step, sections := range want {
			var got []string
			for _, f := range m.setupPage(step) {
				if len(got) == 0 || got[len(got)-1] != f.section {
					got = append(got, f.section)
				}
			}
			if joined := strings.Join(got, ","); joined != sections {
				t.Errorf("step %d sections = %q, want %q", step, joined, sections)
			}
		}
	})
}

// A connection form, new or saved, leads with its provider: it limits the services and credentials to one system.
func TestTheConnectionFormLeadsWithTheProvider(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m := connectionFormModel(t, 110, 40)
		if got := m.fields[0].label; got != providerLabel {
			t.Fatalf("first row of a new connection = %q, want the provider", got)
		}
		if lines := formLines(m); fieldLine(t, lines, providerLabel) > fieldLine(t, lines, labelText("name")) {
			t.Errorf("the provider row stands below the name row:\n%s", strings.Join(lines, "\n"))
		}
		typeText(t, m, "wiki")
		focusField(t, m, "service")
		selectChoice(t, m, "wiki")
		focusField(t, m, "credential")
		selectChoice(t, m, "reader")
		pump(t, m, "f2")
		if m.fail != "" {
			t.Fatalf("save failed: %s", m.fail)
		}
		openConnection(t, m, "wiki")
		if got := m.fields[0].label; got != providerLabel {
			t.Fatalf("first row of a saved connection = %q, want the provider", got)
		}
		if lines := formLines(m); fieldLine(t, lines, providerLabel) > fieldLine(t, lines, labelText("name")) {
			t.Errorf("the provider row stands below the name row:\n%s", strings.Join(lines, "\n"))
		}
	})
}

// "Active for" stands in General directly under "State", in the form and in the guided setup alike.
func TestActiveForStandsUnderStateInGeneral(t *testing.T) {
	m := advancedFormModel(t, 110, 40)
	for i, f := range m.fields {
		if f.label != stateLabel {
			continue
		}
		if i+1 >= len(m.fields) || m.fields[i+1].label != activeForLabel {
			t.Fatalf("the row after state is not active for: %v", labelsOf(m.fields))
		}
		if got := m.sectionOfRow(m.fields[i+1], -1); got != secGeneral {
			t.Errorf("active for is in %q, want General", got)
		}
		break
	}
	if got := m.sectionOfRow(field{label: activeForLabel}, stepScope); got != secGeneral {
		t.Errorf("setup step: active for is in %q, want General", got)
	}
}
