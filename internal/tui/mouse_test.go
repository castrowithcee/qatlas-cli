package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// frameLines is the frame the editor draws now, without styling, one string per screen row.
func frameLines(m *Model) []string { return strings.Split(ansi.Strip(m.render()), "\n") }

// cellOf finds the first place text stands in the frame, as the column and the row of its first cell. It reads
// the drawn frame, never the click targets, so a test clicks where a person would look.
func cellOf(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	for row, line := range frameLines(m) {
		if at := strings.Index(line, text); at >= 0 {
			return ansi.StringWidth(line[:at]), row
		}
	}
	t.Fatalf("the frame shows no %q:\n%s", text, ansi.Strip(m.render()))
	return 0, 0
}

// clickOnRow clicks the first cell of text that stands left of the detail pane, in the rows of a list.
func clickOnRow(t *testing.T, m *Model, text string) {
	t.Helper()
	for row, line := range frameLines(m) {
		if at := strings.Index(line, text); at >= 0 && ansi.StringWidth(line[:at]) < m.listWidth() {
			clickAt(t, m, ansi.StringWidth(line[:at]), row)
			return
		}
	}
	t.Fatalf("the rows show no %q:\n%s", text, ansi.Strip(m.render()))
}

// cellInRow is cellOf within one screen row.
func cellInRow(t *testing.T, m *Model, row int, text string) int {
	t.Helper()
	lines := frameLines(m)
	if row >= len(lines) || !strings.Contains(lines[row], text) {
		t.Fatalf("row %d shows no %q:\n%s", row, text, ansi.Strip(m.render()))
	}
	return ansi.StringWidth(lines[row][:strings.Index(lines[row], text)])
}

func mouseEvent(x, y int, button tea.MouseButton) tea.MouseMsg {
	event := tea.Mouse{X: x, Y: y, Button: button}
	if button == tea.MouseWheelUp || button == tea.MouseWheelDown {
		return tea.MouseWheelMsg(event)
	}
	return tea.MouseClickMsg(event)
}

// clickAt sends a left click the way a terminal would and delivers the commands it produces.
func clickAt(t *testing.T, m *Model, x, y int) {
	t.Helper()
	_, cmd := m.Update(mouseEvent(x, y, tea.MouseLeft))
	deliver(m, cmd)
}

// clickOn clicks the first cell of text.
func clickOn(t *testing.T, m *Model, text string) {
	t.Helper()
	x, y := cellOf(t, m, text)
	clickAt(t, m, x, y)
}

// wheelClocks are the test clocks of the models a test turns the wheel on.
var wheelClocks = map[*Model]*time.Time{}

// wheelTime returns the test clock of m, installing it on first use.
func wheelTime(t *testing.T, m *Model) *time.Time {
	t.Helper()
	if now, ok := wheelClocks[m]; ok {
		return now
	}
	now := time.Now()
	wheelClocks[m] = &now
	m.now = func() time.Time { return now }
	t.Cleanup(func() { delete(wheelClocks, m) })
	return &now
}

// wheelAt turns the wheel one notch, a second after the last one.
func wheelAt(t *testing.T, m *Model, x, y int, button tea.MouseButton) {
	t.Helper()
	now := wheelTime(t, m)
	*now = now.Add(time.Second)
	wheelEvent(t, m, x, y, button)
}

// wheelEvent sends one wheel event at the current test time.
func wheelEvent(t *testing.T, m *Model, x, y int, button tea.MouseButton) {
	t.Helper()
	_, cmd := m.Update(mouseEvent(x, y, button))
	deliver(m, cmd)
}

func TestClickOnATabAndAPartOfTheOptionBar(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	clickOn(t, m, "6 Settings")
	if m.section.area() != areaSettings || m.screen != screenNav {
		t.Fatalf("after the click on the Settings tab: section %v screen %v", m.section, m.screen)
	}
	row := len(m.headerLines()) + 1
	x := cellInRow(t, m, row, "Vault")
	clickAt(t, m, x, row)
	if m.section != sectionVault || !m.navSub {
		t.Fatalf("after the click on Vault: section %v navSub %v", m.section, m.navSub)
	}
	clickOn(t, m, "2 Connections")
	if m.section != sectionConnections {
		t.Errorf("the Connections tab opened %v", m.section)
	}
}

// The sub-tabs of Library are drawn like the tabs of the detail pane, the active one in brackets, and work by
// key and by click; without colour the brackets and the words still tell them apart, at the smallest size too.
func TestLibrarySubTabsAreTabsForKeyAndClick(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		if noColor {
			withNoColor(t)
		}
		for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {80, 24}, {130, 40}} {
			m := sizedModel(groupModel(t), size.w, size.h)
			openSectionByName(t, m, sectionServices)
			m.screen = screenNav
			row := len(m.headerLines()) + 1
			line := ansi.Strip(frameLines(m)[row])
			if !strings.Contains(line, "[Services]") || !strings.Contains(line, "Credentials") || strings.Contains(line, "Library:") {
				t.Fatalf("%dx%d: the sub-tabs read %q, want the active one in brackets", size.w, size.h, line)
			}
			if noColor && strings.Split(m.render(), "\n")[row] != line {
				t.Errorf("%dx%d: NO_COLOR still emits escape sequences in the sub-tabs", size.w, size.h)
			}
			clickAt(t, m, cellInRow(t, m, row, "Credentials"), row)
			if m.section != sectionCredentials || !m.navSub {
				t.Fatalf("%dx%d: the click on Credentials opened %v", size.w, size.h, m.section)
			}
			if line := ansi.Strip(frameLines(m)[row]); !strings.Contains(line, "[Credentials]") {
				t.Errorf("%dx%d: the sub-tabs after the click read %q", size.w, size.h, line)
			}
			press(t, m, "left")
			if m.section != sectionServices {
				t.Errorf("%dx%d: left on the sub-tabs opened %v", size.w, size.h, m.section)
			}
		}
	}
}

func TestClickOnATabAsksBeforeUnsavedInputIsDropped(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "draft")
	clickOn(t, m, "6 Settings")
	if m.screen != screenLeave || m.section != sectionServices {
		t.Fatalf("a click on a tab over a changed form: screen %v section %v, want the leave question", m.screen, m.section)
	}
	if got := m.fields[0].value(); got != "draft" {
		t.Errorf("the typed name = %q", got)
	}
}

func TestClickOnAListRowOpensItAndOnAGroupFoldsIt(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	before := len(m.list.matches)
	clickOn(t, m, "▾ ■ tracker (1)")
	if len(m.list.matches) != before-1 {
		t.Fatalf("a click on a group row left %d rows, want %d", len(m.list.matches), before-1)
	}
	clickOn(t, m, "▸ ■ tracker (1)")
	if len(m.list.matches) != before {
		t.Fatalf("a second click left %d rows, want %d", len(m.list.matches), before)
	}
	clickOnRow(t, m, "wiki-main")
	if m.screen != screenList || m.editing != "" || markedRow(m) != "wiki-main" {
		t.Fatalf("the first click on an entry: screen %v marked %q, want only the mark", m.screen, markedRow(m))
	}
	clickOnRow(t, m, "wiki-main")
	if m.screen != screenForm || m.editing != "wiki-main" {
		t.Fatalf("a click on the marked entry: screen %v editing %q, want its form", m.screen, m.editing)
	}
}

func markedRow(m *Model) string {
	name, _ := m.list.selected()
	return name
}

// The first click on a row of a list marks it, a click on another row marks that one, and only a click on the
// row that was already marked opens it.
func TestClickOnAnotherListRowOnlyMarksIt(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionConnections)
	m.render()
	first := markedRow(m)
	if first != "wiki-docs" {
		t.Fatalf("the list opens on %q, want wiki-docs", first)
	}
	other := "tracker-triage"
	clickOnRow(t, m, other)
	if m.screen != screenList || markedRow(m) != other {
		t.Fatalf("first click: screen %v marked %q, want the list with %q marked", m.screen, markedRow(m), other)
	}
	clickOnRow(t, m, first)
	if m.screen != screenList || markedRow(m) != first {
		t.Fatalf("click on another row: screen %v marked %q, want only %q marked", m.screen, markedRow(m), first)
	}
	clickOnRow(t, m, first)
	if m.screen != screenForm || m.editing != first {
		t.Fatalf("click on the marked row: screen %v editing %q, want %q open", m.screen, m.editing, first)
	}
}

// A click from the tab line or the filter bar only takes the focus to the row, even if the cursor stood on it.
func TestClickFromOutsideTheRowsOnTheMarkedRowOnlyMarks(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionConnections)
	m.render()
	name := markedRow(m)
	m.focusNav()
	clickOnRow(t, m, name)
	if m.screen != screenList || m.barFocus != 0 || m.editing != "" {
		t.Fatalf("click from the tab line: screen %v bar %d editing %q, want the list with the focus on the rows", m.screen, m.barFocus, m.editing)
	}
	clickOn(t, m, "Sort:")
	if m.barFocus == 0 && m.dd == nil {
		t.Fatal("the click on the filter bar did nothing")
	}
	press(t, m, "esc")
	m.render()
	if m.barFocus == 0 {
		m.barFocus = 1
	}
	clickOnRow(t, m, name)
	if m.screen != screenList || m.barFocus != 0 || m.editing != "" {
		t.Fatalf("click from the filter bar: screen %v bar %d editing %q, want only the focus on the row", m.screen, m.barFocus, m.editing)
	}
	clickOnRow(t, m, name)
	if m.screen != screenForm || m.editing != name {
		t.Fatalf("second click: screen %v editing %q, want %q open", m.screen, m.editing, name)
	}
}

func TestClickOnALogRowMarksItAndASecondClickOpensIt(t *testing.T) {
	m := sizedModel(heatModel(t, activityLog(t)), 120, 40)
	m.logs.heat = false
	m.logs.focus = logFocusRows
	m.screen = screenLogs
	m.setLogTab(logTabTable)
	m.render()
	clickOn(t, m, "2026-10-23 12:02")
	if m.screen != screenLogs || m.logs.list.cursor != 2 {
		t.Fatalf("first click: screen %v cursor %d, want the table with row 2 marked", m.screen, m.logs.list.cursor)
	}
	clickOn(t, m, "2026-10-23 12:01")
	if m.screen != screenLogs || m.logs.list.cursor != 1 {
		t.Fatalf("click on another row: screen %v cursor %d, want only row 1 marked", m.screen, m.logs.list.cursor)
	}
	clickOn(t, m, "2026-10-23 12:01")
	if m.screen != screenLogDetail {
		t.Fatalf("click on the marked row: screen %v, want the entry", m.screen)
	}
}

func TestClickFromTheTabLineOnTheMarkedLogRowOnlyMarks(t *testing.T) {
	m := sizedModel(heatModel(t, activityLog(t)), 120, 40)
	m.logs.heat = false
	m.logs.focus = logFocusRows
	m.screen = screenLogs
	m.setLogTab(logTabTable)
	m.render()
	m.focusNav()
	clickOn(t, m, "2026-10-19 12:00")
	if m.screen != screenLogs || m.logs.focus != logFocusRows {
		t.Fatalf("click from the tab line: screen %v focus %v, want the rows", m.screen, m.logs.focus)
	}
	m.logs.focus = logFocusTabs
	m.render()
	clickOn(t, m, "2026-10-19 12:00")
	if m.screen != screenLogs || m.logs.focus != logFocusRows {
		t.Fatalf("click from the log tabs: screen %v focus %v, want only the focus on the rows", m.screen, m.logs.focus)
	}
	clickOn(t, m, "2026-10-19 12:00")
	if m.screen != screenLogDetail {
		t.Fatalf("second click: screen %v, want the entry", m.screen)
	}
}

func TestClickOnATemplateRowMarksItAndASecondClickTakesIt(t *testing.T) {
	m, _ := templateModel(t, "alpha", "beta", "gamma")
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList
	press(t, m, "n")
	clickOn(t, m, "from")
	if m.template == nil || !m.template.picking {
		t.Fatal("the list of entries did not open")
	}
	m.render()
	clickOn(t, m, "beta")
	if m.screen != screenTemplate || selectedTemplate(m) != "beta" {
		t.Fatalf("first click: screen %v marked %q, want only beta marked", m.screen, selectedTemplate(m))
	}
	clickOn(t, m, "gamma")
	if m.screen != screenTemplate || selectedTemplate(m) != "gamma" {
		t.Fatalf("click on another row: screen %v marked %q, want only gamma marked", m.screen, selectedTemplate(m))
	}
	clickOn(t, m, "gamma")
	if m.screen == screenTemplate {
		t.Fatal("a click on the marked row left the list of entries open")
	}
}

func selectedTemplate(m *Model) string {
	name, _ := m.templateList.selected()
	return name
}

func TestClickOnAProviderMarksItAndASecondClickChoosesIt(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	clickOn(t, m, labelText(providerLabel))
	if m.screen != screenProviders {
		t.Fatalf("the provider choice is screen %v here", m.screen)
	}
	m.render()
	before, _ := m.providers.list.selected()
	x, y := cellOf(t, m, "Tracker")
	clickAt(t, m, x, y)
	if m.screen != screenProviders {
		t.Fatalf("first click: screen %v, want the table", m.screen)
	}
	marked, _ := m.providers.list.selected()
	if marked == before && before != "tracker" {
		t.Fatalf("the click did not mark the row: %q", marked)
	}
	clickAt(t, m, x, y)
	if m.screen != screenForm {
		t.Fatalf("click on the marked row: screen %v, want the form", m.screen)
	}
}

func TestClickOnAPickerValueMarksItAndASecondClickChoosesIt(t *testing.T) {
	m, _, _ := newModel(t)
	names := numberedConnections(60)
	seedConnections(t, m, names...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openDefaultForm(t, m)
	press(t, m, "/")
	if m.screen != screenPicker {
		t.Fatalf("screen %v, want the picker", m.screen)
	}
	m.render()
	clickOn(t, m, "conn-003")
	if m.screen != screenPicker || m.picker.cursor != 3 {
		t.Fatalf("first click: screen %v cursor %d, want the picker with row 3 marked", m.screen, m.picker.cursor)
	}
	clickOn(t, m, "conn-003")
	if m.screen != screenForm || m.fieldValue("connection") != "conn-003" {
		t.Fatalf("click on the marked value: screen %v value %q, want conn-003 chosen", m.screen, m.fieldValue("connection"))
	}
}

func TestClickOnATickPickerValueMarksItAndASecondClickTicksIt(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	group := m.pickerGroups.groups[0]
	m.render()
	clickOn(t, m, "Pull requests (26)")
	if len(m.pickerMarks) != 0 {
		t.Fatalf("the first click ticked %d tools", len(m.pickerMarks))
	}
	if got, _ := m.picker.selected(); got == groupRowPrefix+group.ID {
		t.Fatalf("the first click left the mark on the first group")
	}
	clickOn(t, m, "Pull requests (26)")
	if len(m.pickerMarks) != 26 {
		t.Fatalf("the click on the marked group ticked %d tools, want 26", len(m.pickerMarks))
	}
}

func TestClickOnARowFromTheTabLineTakesTheFocus(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	m.openSection(sectionConnections)
	m.screen = screenNav
	clickOnRow(t, m, "tracker-triage")
	if m.screen != screenList || markedRow(m) != "tracker-triage" || m.editing != "" {
		t.Fatalf("screen %v marked %q editing %q, want the list with the row marked", m.screen, markedRow(m), m.editing)
	}
	clickOnRow(t, m, "tracker-triage")
	if m.screen != screenForm || m.editing != "tracker-triage" {
		t.Fatalf("screen %v editing %q", m.screen, m.editing)
	}
}

func TestClickOnTheFilterBarChoosesAValue(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	clickOn(t, m, "Group: Provider")
	if m.dd == nil || m.barFocus != 1 {
		t.Fatalf("the click on Group did not open its list; the bar reads:\n%s", ansi.Strip(m.render()))
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "> Provider") || !strings.Contains(view, "  None") {
		t.Fatalf("the list does not show the values:\n%s", view)
	}
	clickOn(t, m, "  None")
	if m.dd != nil || !m.opts(sectionConnections).flat {
		t.Fatalf("the click on None left the grouping on; the bar reads:\n%s", ansi.Strip(m.render()))
	}
	clickOn(t, m, "State: All")
	clickOn(t, m, "  Inactive")
	if m.opts(sectionConnections).state != 2 {
		t.Errorf("state = %d, want Inactive", m.opts(sectionConnections).state)
	}
}

func TestClickOnAFormRowFocusesItAndTypesNothing(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "svc")
	state := m.formState()
	last := len(m.fields) - 1
	label := labelText(m.fields[last].label)
	if m.fields[last].kind == fieldText && !m.fields[last].readOnly {
		clickOn(t, m, label)
		if m.focus != last {
			t.Fatalf("focus = %d, want the clicked row %d", m.focus, last)
		}
	}
	// A click on the name row puts the focus back and changes no text.
	clickOn(t, m, labelText(m.fields[0].label))
	if m.focus != 0 || m.formState() != state {
		t.Errorf("focus %d, form %q; want focus 0 and the typed text only", m.focus, m.formState())
	}
}

func TestClickOnAChoiceRowOpensItsChoices(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	clickOn(t, m, labelText(providerLabel))
	if m.screen != screenProviders && m.screen != screenPicker {
		t.Fatalf("a click on the provider row: screen %v, want its table or picker", m.screen)
	}
	// A click on a value of the table chooses it, as enter does.
	x, y := cellOf(t, m, "Tracker")
	clickAt(t, m, x, y)
	if m.screen == screenProviders {
		clickAt(t, m, x, y)
	}
	if m.screen != screenForm {
		t.Errorf("a click on a provider: screen %v, want the form", m.screen)
	}
}

func TestClickOnASectionOfAFormMovesTheFocus(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	clickOnRow(t, m, "wiki-docs")
	m.render()
	if m.screen != screenForm || m.sectionListWidth == 0 {
		t.Fatalf("screen %v, section list width %d; want a connection form with its section list", m.screen, m.sectionListWidth)
	}
	entries := m.formSections()
	if len(entries) < 2 {
		t.Fatalf("sections = %v", entries)
	}
	target := entries[2].name
	x, y := cellOf(t, m, target)
	if x >= m.sectionListWidth {
		t.Fatalf("%q found at column %d, not in the section list", target, x)
	}
	clickAt(t, m, x, y)
	if m.currentSection() != target {
		t.Errorf("section = %q, want %q", m.currentSection(), target)
	}
}

func TestClickOnASectionHeadingInANarrowTerminal(t *testing.T) {
	m := sizedModel(groupModel(t), 90, 60)
	openSectionByName(t, m, sectionConnections)
	clickOnRow(t, m, "wiki-docs")
	m.render()
	entries := m.formSections()
	if m.sectionListWidth != 0 || len(entries) < 2 {
		t.Fatalf("list width %d, sections %v; want headings in place of the list", m.sectionListWidth, entries)
	}
	target := entries[1].name
	lines := frameLines(m)
	row := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == target || strings.HasPrefix(strings.TrimSpace(line), target+" (closed)") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no heading %q:\n%s", target, strings.Join(lines, "\n"))
	}
	clickAt(t, m, 2, row)
	if m.currentSection() != target {
		t.Errorf("section = %q, want %q", m.currentSection(), target)
	}
}

func TestClickOnAnInfoMarkOpensTheExplanation(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionGeneral)
	if !strings.Contains(ansi.Strip(m.render()), "(i)") {
		t.Fatalf("no info mark on the focused row:\n%s", ansi.Strip(m.render()))
	}
	clickOn(t, m, "(i)")
	if !m.infoOpen {
		t.Fatal("the click on (i) opened nothing")
	}
	clickAt(t, m, 3, 3)
	if m.infoOpen {
		t.Error("a click did not close the explanation")
	}
}

func TestClickOnAModalButtonAnswersTheQuestion(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "draft")
	press(t, m, "esc")
	if m.screen != screenLeave {
		t.Fatalf("screen %v, want the leave question", m.screen)
	}
	// Outside the buttons nothing happens.
	clickAt(t, m, 0, 0)
	if m.screen != screenLeave {
		t.Fatalf("a click beside the dialog answered it")
	}
	clickOn(t, m, "esc keep editing")
	if m.screen != screenForm || m.fields[0].value() != "draft" {
		t.Fatalf("keep editing: screen %v name %q", m.screen, m.fields[0].value())
	}
	press(t, m, "esc")
	clickOn(t, m, "d discard")
	if m.screen == screenLeave || m.screen == screenForm {
		t.Fatalf("discard left screen %v", m.screen)
	}
	if _, ok := m.cfg.Services["draft"]; ok {
		t.Error("discarding saved the service")
	}
}

func TestClickOnAConfirmationAnswersWithTheUsualKey(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	clickOnRow(t, m, "wiki-main")
	press(t, m, "esc")
	press(t, m, "d")
	if m.screen != screenConfirm {
		t.Fatalf("screen %v, want the confirmation", m.screen)
	}
	// The question is the only thing a click reaches while it stands; the list below does not react.
	x, y := cellOf(t, m, "tracker-main")
	clickAt(t, m, x, y)
	if m.screen != screenConfirm {
		t.Fatalf("a click on the dimmed list answered the question: screen %v", m.screen)
	}
	clickOn(t, m, "keep")
	if m.screen == screenConfirm {
		t.Fatalf("the keep button did not answer")
	}
	if _, ok := m.cfg.Services["wiki-main"]; !ok {
		t.Error("keeping deleted the service")
	}
}

func TestTheWheelScrollsAListAndAForm(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	before := m.list.cursor
	wheelAt(t, m, 5, 10, tea.MouseWheelDown)
	if want := before + 1; m.list.cursor != want {
		t.Errorf("after the wheel down the cursor is %d, want %d", m.list.cursor, want)
	}
	wheelAt(t, m, 5, 10, tea.MouseWheelUp)
	if m.list.cursor != before {
		t.Errorf("after the wheel up the cursor is %d, want %d", m.list.cursor, before)
	}

	clickOnRow(t, m, "wiki-docs")
	clickOnRow(t, m, "wiki-docs")
	m.render()
	if m.screen != screenForm {
		t.Fatalf("screen %v", m.screen)
	}
	focus := m.focus
	wheelAt(t, m, 60, 10, tea.MouseWheelDown)
	if m.focus == focus {
		t.Error("the wheel over the rows of a form left the focus where it was")
	}
	// Over the list of the sections the wheel jumps from section to section.
	section := m.currentSection()
	wheelAt(t, m, 1, 10, tea.MouseWheelDown)
	if m.currentSection() == section {
		t.Errorf("the wheel over the sections stayed in %q", section)
	}
}

func TestMouseEventsMakeNoCharacters(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	m.openVaultUnlockPrompt(screenNav)
	for _, p := range [][2]int{{0, 0}, {60, 10}, {60, 20}, {70, 21}} {
		clickAt(t, m, p[0], p[1])
	}
	wheelAt(t, m, 60, 20, tea.MouseWheelDown)
	if m.screen != screenVaultUnlock || m.vaultUnlock.input.Value() != "" {
		t.Fatalf("screen %v, input %q; want the empty passphrase prompt", m.screen, m.vaultUnlock.input.Value())
	}
	// A letter button is not passed on where the screen reads text.
	m.pressName("y")
	m.pressName("d")
	if m.vaultUnlock == nil || m.vaultUnlock.input.Value() != "" {
		t.Errorf("a letter reached the passphrase: %q", m.vaultUnlock.input.Value())
	}
	// The cancel button is a key that makes no character, and works.
	clickOn(t, m, "esc")
	if m.screen == screenVaultUnlock {
		t.Errorf("the esc button did not close the prompt")
	}
}

func TestMouseEventsMakeNoCharactersInAFormField(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "abc")
	state := m.formState()
	for _, p := range [][2]int{{0, 8}, {10, 8}, {30, 8}, {40, 9}, {5, 12}} {
		clickAt(t, m, p[0], p[1])
		if m.screen != screenForm {
			break
		}
	}
	wheelAt(t, m, 20, 8, tea.MouseWheelDown)
	wheelAt(t, m, 20, 8, tea.MouseWheelUp)
	if m.formState() != state {
		t.Errorf("form = %q, want %q", m.formState(), state)
	}
}

func TestATooSmallTerminalIgnoresTheMouse(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	x, y := cellOf(t, m, "6 Settings")
	sizedModel(m, minimumWidth-1, minimumHeight-1)
	view := m.render()
	clickAt(t, m, x%(minimumWidth-1), y)
	clickAt(t, m, 0, 0)
	if m.section.area() == areaSettings {
		t.Error("a click in a terminal that is too small switched the area")
	}
	if m.render() != view {
		t.Error("a click changed the size notice")
	}

	// A screen that does not fit the workspace shows the notice, and takes no clicks either.
	m = sizedModel(heatModel(t, activityLog(t)), 40, minimumHeight)
	m.setLogTab(logTabDetails)
	// A note of several lines takes the rows the compact summary still needs.
	m.status = strings.Repeat("a long note ", 12)
	m.render()
	if !m.tooSmall {
		t.Fatalf("the Details tab with a long note fits the smallest terminal; the notice is not shown:\n%s", ansi.Strip(m.render()))
	}
	cursor, area, screen := m.list.cursor, m.section.area(), m.screen
	for row := 0; row < minimumHeight; row++ {
		for _, x := range []int{0, 3, 12, 30} {
			clickAt(t, m, x, row)
			wheelAt(t, m, x, row, tea.MouseWheelDown)
		}
	}
	if m.list.cursor != cursor || m.section.area() != area || m.screen != screen {
		t.Errorf("cursor %d area %v screen %v after clicks on the size notice", m.list.cursor, m.section.area(), m.screen)
	}
}

func TestSwitchedOffMouseDoesNothing(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	m.prefs = uiprefs.Default()
	m.prefs.MouseOff = true
	section := m.section
	clickOn(t, m, "6 Settings")
	if m.section != section {
		t.Error("a click switched the area with the mouse off")
	}
	openSectionByName(t, m, sectionServices)
	cursor := m.list.cursor
	wheelAt(t, m, 5, 10, tea.MouseWheelDown)
	if m.list.cursor != cursor {
		t.Error("the wheel moved the list with the mouse off")
	}
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Errorf("the view asks for mouse mode %v with the mouse off, want none", mode)
	}
	m.prefs.MouseOff = false
	if mode := m.View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Errorf("the view asks for mouse mode %v with the mouse on, want cell motion", mode)
	}
}

func TestTheMouseSettingAppliesAtOnce(t *testing.T) {
	m, path := lockedVaultModel(t)
	sizedModel(m, 100, 40)
	openSectionByName(t, m, sectionGeneral)
	focusField(t, m, mouseLabel)
	press(t, m, "right")
	_, cmd := m.Update(keyMsg("f2"))
	if m.fail != "" {
		t.Fatalf("saving failed: %q", m.fail)
	}
	if m.mouseEnabled() {
		t.Error("the preference is still on after saving it off")
	}
	deliver(m, cmd)
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Errorf("after saving the view asks for mouse mode %v, want none", mode)
	}
	if got, _ := uiprefs.Load(path); got.MouseEnabled() {
		t.Error("tui.yaml still has the mouse on")
	}
	clickOn(t, m, "6 Settings")
	if m.screen == screenNav && m.navSub {
		t.Error("a click after switching the mouse off acted")
	}
}

// The terminal restored after a failed restart gets the mouse mode of the last view back, so the view keeps
// asking for what the preference says.
func TestAFailedRestartKeepsTheMouseModeOfThePreference(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	m.Update(restartFailedMsg{})
	if mode := m.View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Errorf("after a failed restart with the mouse on the view asks for mouse mode %v", mode)
	}
	m.prefs.MouseOff = true
	m.Update(restartFailedMsg{})
	if mode := m.View().MouseMode; mode != tea.MouseModeNone {
		t.Errorf("after a failed restart with the mouse off the view asks for mouse mode %v", mode)
	}
}

func TestClickOnALogRowAndTheBarOfTheLogs(t *testing.T) {
	m := sizedModel(groupModel(t), 140, 40)
	openSectionByName(t, m, sectionLogs)
	clickOn(t, m, "Mode: Day")
	if m.dd == nil || m.logs.mode != logModeDay {
		t.Fatalf("the click on Mode did not open its list: mode %v", m.logs.mode)
	}
	// A click on an entry of the list chooses it, as enter does.
	clickOn(t, m, "  Range")
	if m.dd != nil || m.logs.mode != logModeRange {
		t.Errorf("mode = %v, want the range", m.logs.mode)
	}
}

func TestClickOnAnOverviewLineOpensItsArea(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	m.openSection(sectionOverview)
	m.screen = screenNav
	clickOn(t, m, sectionServices.title())
	if m.section != sectionOverview || m.screen != screenList {
		t.Fatalf("the first click: section %v screen %v, want the Overview with the line marked", m.section, m.screen)
	}
	clickOn(t, m, sectionServices.title())
	if m.section != sectionServices {
		t.Errorf("section = %v, want Services", m.section)
	}
}

func TestEveryScreenMeetsAClickWithoutPanicking(t *testing.T) {
	for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {80, 24}, {130, 40}} {
		m := sizedModel(groupModel(t), size.w, size.h)
		for _, s := range []section{sectionOverview, sectionConnections, sectionServices, sectionCredentials,
			sectionLogs, sectionTokens, sectionApprovals, sectionGeneral, sectionVault, sectionDefaults,
			sectionTransfer, sectionUpdate, sectionSelftest} {
			m.openSection(s)
			m.screen = screenNav
			for y := 0; y < size.h; y += 4 {
				for x := 0; x < size.w; x += 13 {
					m.Update(mouseEvent(x, y, tea.MouseLeft))
					m.Update(mouseEvent(x, y, tea.MouseWheelDown))
					m.render()
					if m.screen == screenNav || m.screen == screenList || m.screen == screenLogs {
						continue
					}
					press(t, m, "esc", "esc")
					if m.screen == screenLeave {
						press(t, m, "d")
					}
				}
			}
		}
	}
}

func TestClickOnAnOptionOfTheTemplateQuestion(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionServices)
	press(t, m, "n")
	if m.screen != screenTemplate || m.template == nil || m.template.picking {
		t.Fatalf("screen %v, want the template question", m.screen)
	}
	clickOn(t, m, "from")
	if m.template == nil || !m.template.picking {
		t.Fatalf("a click on the existing-entry option did not open the list of entries")
	}
	press(t, m, "esc")
	clickOn(t, m, "empty")
	if m.screen != screenForm || m.editing != "" {
		t.Errorf("a click on the empty option: screen %v, want a new form", m.screen)
	}
}

func TestDialogButtonsWorkInTheFullScreenFallback(t *testing.T) {
	m := sizedModel(groupModel(t), 50, 16)
	if m.termWidth >= modalMinWidth {
		t.Fatal("the terminal has room for a modal")
	}
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "draft")
	press(t, m, "esc")
	if _, ok := m.modalView(); ok || m.screen != screenLeave {
		t.Fatalf("screen %v: want the leave question without a modal", m.screen)
	}
	// The tab line is drawn, and does not answer or leave the question.
	clickOn(t, m, "6")
	if m.screen != screenLeave {
		t.Fatalf("a click on a tab left the question: screen %v", m.screen)
	}
	clickOn(t, m, "esc keep")
	if m.screen != screenForm || m.fields[0].value() != "draft" {
		t.Fatalf("keep editing: screen %v name %q", m.screen, m.fields[0].value())
	}
	press(t, m, "esc")
	clickOn(t, m, "d discard")
	if m.screen == screenLeave || m.screen == screenForm {
		t.Fatalf("discard left screen %v", m.screen)
	}

	// A destructive question is still asked, and answered only by its own buttons.
	openSectionByName(t, m, sectionServices)
	clickOnRow(t, m, "wiki-main")
	press(t, m, "esc", "d")
	if m.screen != screenConfirm {
		t.Fatalf("screen %v, want the confirmation", m.screen)
	}
	clickAt(t, m, 0, 0)
	if m.screen != screenConfirm {
		t.Fatalf("a click beside the buttons answered the question")
	}
	clickOn(t, m, "keep")
	if m.screen == screenConfirm {
		t.Fatal("the keep button did not answer")
	}
	if _, ok := m.cfg.Services["wiki-main"]; !ok {
		t.Error("keeping deleted the service")
	}
}

// A terminal that reports a notch and its release delivers two presses at once; they are one step. Notches
// that follow each other at the pace of a hand are each a step.
func TestOneNotchIsOneStepWhateverTheTerminalReports(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	m.list.cursor = 0
	start := 0
	now := wheelTime(t, m)
	wheelEvent(t, m, 5, 10, tea.MouseWheelDown)
	wheelEvent(t, m, 5, 10, tea.MouseWheelDown)
	if m.list.cursor != start+1 {
		t.Fatalf("a notch with its release moved the cursor from %d to %d, want one row", start, m.list.cursor)
	}
	for range 3 {
		*now = now.Add(wheelMerge)
		wheelEvent(t, m, 5, 10, tea.MouseWheelDown)
		wheelEvent(t, m, 5, 10, tea.MouseWheelDown)
	}
	if m.list.cursor != start+4 {
		t.Errorf("three more notches ended on %d, want %d", m.list.cursor, start+4)
	}
	// Another direction or another cell is another notch, however close.
	wheelEvent(t, m, 5, 10, tea.MouseWheelUp)
	if m.list.cursor != start+3 {
		t.Errorf("the wheel up right after a wheel down ended on %d, want %d", m.list.cursor, start+3)
	}
	wheelEvent(t, m, 6, 10, tea.MouseWheelUp)
	if m.list.cursor != start+2 {
		t.Errorf("the wheel up in another cell ended on %d, want %d", m.list.cursor, start+2)
	}
}

func TestTheWheelStopsAtTheEnds(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	last := len(m.list.matches) - 1
	m.list.cursor = last
	wheelAt(t, m, 5, 10, tea.MouseWheelDown)
	if m.list.cursor != last {
		t.Errorf("the wheel down at the end of the list moved the cursor to %d", m.list.cursor)
	}

	clickOnRow(t, m, "wiki-docs")
	clickOnRow(t, m, "wiki-docs")
	m.render()
	if m.screen != screenForm {
		t.Fatalf("screen %v", m.screen)
	}
	var editable []int
	for i := range m.fields {
		if !m.fields[i].readOnly && !m.skipped(i) {
			editable = append(editable, i)
		}
	}
	m.focus = editable[len(editable)-1]
	m.applyFocus()
	wheelAt(t, m, 60, 10, tea.MouseWheelDown)
	if m.focus != editable[len(editable)-1] {
		t.Errorf("the wheel down on the last field moved the focus to %d", m.focus)
	}
	m.focus = editable[0]
	m.applyFocus()
	wheelAt(t, m, 60, 10, tea.MouseWheelUp)
	if m.focus != editable[0] {
		t.Errorf("the wheel up on the first field moved the focus to %d", m.focus)
	}
	// The keys still wrap.
	press(t, m, "shift+tab")
	if m.focus != editable[len(editable)-1] {
		t.Errorf("shift+tab on the first field went to %d, want the last", m.focus)
	}
}

func TestTheWheelStopsAtTheEndsOfTheActivityTable(t *testing.T) {
	m := sizedModel(heatModel(t, activityLog(t)), 120, 40)
	m.logs.heat = false
	m.logs.focus = logFocusRows
	m.screen = screenLogs
	m.setLogTab(logTabTable)
	m.render()
	n := len(m.logs.list.matches)
	if n < 2 {
		t.Fatalf("the log shows %d rows", n)
	}
	m.logs.list.cursor = n - 1
	wheelAt(t, m, 20, 10, tea.MouseWheelDown)
	if m.logs.list.cursor != n-1 {
		t.Errorf("the wheel down at the end moved the cursor to %d", m.logs.list.cursor)
	}
	m.logs.list.cursor = 0
	wheelAt(t, m, 20, 10, tea.MouseWheelUp)
	if m.logs.list.cursor != 0 {
		t.Errorf("the wheel up at the top moved the cursor to %d", m.logs.list.cursor)
	}
	wheelAt(t, m, 20, 10, tea.MouseWheelDown)
	if m.logs.list.cursor != 1 {
		t.Errorf("the wheel down moved the cursor to %d, want 1", m.logs.list.cursor)
	}
}

func TestTheWheelLeadsFromTheTabLineIntoTheContentOnly(t *testing.T) {
	for _, s := range []section{sectionOverview, sectionConnections, sectionServices, sectionTokens} {
		m := sizedModel(groupModel(t), 120, 40)
		openSectionByName(t, m, s)
		m.focusNav()
		wheelAt(t, m, 5, 10, tea.MouseWheelUp)
		if m.screen != screenNav {
			t.Errorf("%v: the wheel up on the tab line left it for screen %v", s, m.screen)
		}
		wheelAt(t, m, 5, 10, tea.MouseWheelDown)
		if m.screen != screenList {
			t.Errorf("%v: the wheel down on the tab line led to screen %v, want the list", s, m.screen)
		}
		// On the first row the wheel up stays in the list.
		wheelAt(t, m, 5, 10, tea.MouseWheelUp)
		if m.screen != screenList {
			t.Errorf("%v: the wheel up on the first row left the list for screen %v", s, m.screen)
		}
		wheelAt(t, m, 5, 10, tea.MouseWheelDown)
		if m.screen != screenList {
			t.Errorf("%v: the wheel down in the list left it for screen %v", s, m.screen)
		}
	}
}

func TestTheWheelScrollsATextAreaByThreeLines(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	m.openHelp()
	m.help.reset()
	m.render()
	wheelAt(t, m, 20, 10, tea.MouseWheelDown)
	if m.help.offset() != 3 {
		t.Errorf("the wheel down moved the help by %d lines, want 3", m.help.offset())
	}
	wheelAt(t, m, 20, 10, tea.MouseWheelUp)
	if m.help.offset() != 0 {
		t.Errorf("the wheel up left the help at %d, want 0", m.help.offset())
	}
}

// Events that queue up while the editor draws arrive as pairs without a gap: n notches are n rows.
func TestQueuedWheelPairsMoveOneRowEach(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	m.list.cursor = 0
	wheelTime(t, m)
	for range 4 {
		wheelEvent(t, m, 5, 10, tea.MouseWheelDown)
	}
	if m.list.cursor != 2 {
		t.Errorf("four queued events moved the cursor to %d, want 2", m.list.cursor)
	}
}

const backText = "[esc] back"

func backInHeader(m *Model) bool { return strings.Contains(frameLines(m)[0], backText) }

// backCase opens one screen and says whether esc leads one level back from it.
type backCase struct {
	name string
	open func(t *testing.T) *Model
	back bool
}

func backCases() []backCase {
	cleanForm := func(t *testing.T) *Model {
		m := sizedModel(groupModel(t), 160, 40)
		openSectionByName(t, m, sectionServices)
		pressNew(t, m)
		return m
	}
	return []backCase{
		{"tab line", func(t *testing.T) *Model {
			m := sizedModel(groupModel(t), 160, 40)
			m.screen = screenNav
			return m
		}, false},
		{"list", func(t *testing.T) *Model {
			m := sizedModel(groupModel(t), 160, 40)
			openSectionByName(t, m, sectionServices)
			return m
		}, false},
		{"settings page form", func(t *testing.T) *Model {
			m := sizedModel(groupModel(t), 160, 40)
			openSectionByName(t, m, sectionGeneral)
			return m
		}, false},
		{"entry form", cleanForm, true},
		{"provider table", func(t *testing.T) *Model {
			m := cleanForm(t)
			clickOn(t, m, labelText(providerLabel))
			return m
		}, true},
		{"help over the workspace", func(t *testing.T) *Model {
			m := sizedModel(groupModel(t), 160, 18)
			openSectionByName(t, m, sectionServices)
			press(t, m, "?")
			return m
		}, true},
		{"help as an overlay", func(t *testing.T) *Model {
			m := sizedModel(groupModel(t), 160, 40)
			openSectionByName(t, m, sectionServices)
			press(t, m, "?")
			return m
		}, false},
		{"leave question", func(t *testing.T) *Model {
			m := cleanForm(t)
			typeText(t, m, "draft")
			press(t, m, "esc")
			return m
		}, false},
		{"log detail", func(t *testing.T) *Model {
			f := newLogFixture(t)
			f.append("cli", "", "op.one", "wiki", "success")
			m := sizedModel(f.model(true, f.localToday()), 160, 40)
			press(t, m, "down", "enter")
			return m
		}, true},
		{"target list", func(t *testing.T) *Model {
			m := targetsScreen(t)
			return m
		}, true},
	}
}

func targetsScreen(t *testing.T) *Model {
	t.Helper()
	reg := targetsRegistry(t, github.Register)
	m, _ := toolsModel(t, reg, map[string]config.Connection{
		"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
	sizedModel(m, 160, 40)
	openConnection(t, m, "gh")
	openTargetList(t, m)
	return m
}

// The button stands exactly where esc leads one level back, and is a hint-styled bracketed word.
func TestBackButtonStandsWhereEscGoesOneLevelBack(t *testing.T) {
	for _, c := range backCases() {
		m := c.open(t)
		if got := m.backShown(); got != c.back {
			t.Errorf("%s: backShown = %v, want %v", c.name, got, c.back)
		}
		if got := backInHeader(m); got != c.back {
			t.Errorf("%s: button in the header = %v, want %v:\n%s", c.name, got, c.back, frameLines(m)[0])
		}
	}
}

func TestBackButtonStandsOnTheDetailScreensOfAccess(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	fixtureToken(t, v, "ci-valid", nil, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	openTokensUnlocked(t, m)
	m.tokenDetail, m.screen = "ci-valid", screenConfirm
	if !backInHeader(m) {
		t.Errorf("token detail has no back button:\n%s", frameLines(m)[0])
	}
	m.tokenRevoke = "ci-valid"
	if backInHeader(m) {
		t.Errorf("the revoke question shows a back button")
	}
	m.tokenRevoke = ""
	clickOn(t, m, backText)
	if m.screen != screenList || m.tokenDetail != "" {
		t.Errorf("click on the button in the token detail: screen %v detail %q", m.screen, m.tokenDetail)
	}
	m.approvalDetail, m.screen = "personal", screenConfirm
	if !backInHeader(m) {
		t.Errorf("approval detail has no back button:\n%s", frameLines(m)[0])
	}
	clickOn(t, m, "2 Connections")
	if m.section != sectionConnections || m.screen != screenNav || m.approvalDetail != "" {
		t.Errorf("tab click from the approval detail: section %v screen %v detail %q", m.section, m.screen, m.approvalDetail)
	}
	m.openSection(sectionTokens)
	m.tokenDetail, m.screen = "ci-valid", screenConfirm
	clickOn(t, m, "2 Connections")
	if m.section != sectionConnections || m.screen != screenNav || m.tokenDetail != "" {
		t.Errorf("tab click from the token detail: section %v screen %v detail %q", m.section, m.screen, m.tokenDetail)
	}
}

// A click on the button does what esc does, the question for unsaved input included.
func TestBackButtonClickIsEsc(t *testing.T) {
	m := sizedModel(groupModel(t), 160, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	clickOn(t, m, backText)
	if m.screen != screenList {
		t.Fatalf("click on an unchanged form: screen %v, want the list", m.screen)
	}
	pressNew(t, m)
	typeText(t, m, "draft")
	clickOn(t, m, backText)
	if m.screen != screenLeave {
		t.Fatalf("click on a changed form: screen %v, want the leave question", m.screen)
	}
	if backInHeader(m) {
		t.Errorf("the leave question shows the button")
	}
	press(t, m, "esc")
	if m.screen != screenForm || m.fields[0].value() != "draft" {
		t.Fatalf("esc on the question: screen %v value %q, want the form as it was", m.screen, m.fields[0].value())
	}
	clickOn(t, m, backText)
	press(t, m, "d")
	if m.screen != screenList {
		t.Fatalf("discarding: screen %v, want the list", m.screen)
	}
}

func TestBackButtonClickFromADeeperScreenIsOneEsc(t *testing.T) {
	m := targetsScreen(t)
	clickOn(t, m, backText)
	if m.screen != screenForm {
		t.Fatalf("click in the target list: screen %v, want the form", m.screen)
	}
}

// A click on a tab leaves every level by the way of esc and reaches the area.
func TestTabClickWorksFromEveryLevel(t *testing.T) {
	for _, c := range backCases() {
		if !c.back || c.name == "help as an overlay" {
			continue
		}
		m := c.open(t)
		clickOn(t, m, "6 Settings")
		if m.section.area() != areaSettings || m.screen != screenNav {
			t.Errorf("%s: after the click on Settings: section %v screen %v", c.name, m.section, m.screen)
		}
	}
}

func TestTabClickFromAPickerKeepsUnsavedInputBehindTheQuestion(t *testing.T) {
	open := func(typed bool) *Model {
		m, _, _ := newModel(t)
		seedConnections(t, m, numberedConnections(5)...)
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		if typed {
			openDefaultForm(t, m)
		} else {
			openSectionByName(t, m, sectionDefaults)
			press(t, m, "n")
			focusField(t, m, "connection")
		}
		press(t, m, "/")
		if m.screen != screenPicker {
			t.Fatalf("screen %v, want the picker", m.screen)
		}
		return m
	}
	m := open(false)
	clickOn(t, m, "6 Settings")
	if m.section.area() != areaSettings || m.screen != screenNav {
		t.Errorf("an unchanged form: section %v screen %v, want the Settings tab line", m.section, m.screen)
	}
	m = open(true)
	clickOn(t, m, "6 Settings")
	if m.screen != screenLeave || m.section != sectionDefaults {
		t.Errorf("a changed form: screen %v section %v, want the leave question over Defaults", m.screen, m.section)
	}
}

// A click on a tab does nothing where esc would only end a typed line, a dialog or a question.
func TestTabClickDoesNothingWhileTypingOrAsked(t *testing.T) {
	m := targetsScreen(t)
	press(t, m, "n")
	typeText(t, m, "repos/octo/x")
	clickOn(t, m, "6 Settings")
	if m.screen != screenTargets || !m.typing() {
		t.Errorf("a click while a target is typed: screen %v typing %v", m.screen, m.typing())
	}
	m = sizedModel(groupModel(t), 160, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "draft")
	press(t, m, "esc")
	clickOn(t, m, "6 Settings")
	if m.screen != screenLeave || m.section != sectionServices {
		t.Errorf("a click over the question: screen %v section %v", m.screen, m.section)
	}
}

func TestBackButtonWithoutColourIsText(t *testing.T) {
	withNoColor(t)
	m := sizedModel(groupModel(t), 160, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	row := strings.Split(m.render(), "\n")[0]
	if !strings.Contains(row, backText) || row != ansi.Strip(row) {
		t.Errorf("NO_COLOR header = %q, want the button as plain text", row)
	}
}

// At the smallest size the header keeps its rows; the button gives way to the path and the keys of the footer.
func TestBackButtonGivesWayAtTheSmallestSize(t *testing.T) {
	m := sizedModel(groupModel(t), minimumWidth, minimumHeight)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	with := len(m.headerLines())
	m.backdrop = true
	without := len(m.headerLines())
	m.backdrop = false
	if with != without {
		t.Errorf("the button changed the rows of the header: %d against %d", with, without)
	}
	if lines := frameLines(m); len(lines) != minimumHeight {
		t.Errorf("frame has %d rows, want %d", len(lines), minimumHeight)
	}
	if row := frameLines(m)[0]; ansi.StringWidth(row) > minimumWidth {
		t.Errorf("the header row is wider than the terminal: %q", row)
	}
}

// With less room the help and command keys leave before the button does.
func TestBackButtonOutlastsTheHeaderHints(t *testing.T) {
	m := sizedModel(groupModel(t), 160, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	for width := 160; width >= minimumWidth; width-- {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		row := frameLines(m)[0]
		hints := strings.Contains(row, "["+keyLabel(actHelp)+"]")
		if hints && !strings.Contains(row, backText) {
			t.Fatalf("width %d: hints without the button: %q", width, row)
		}
		// The button neither adds a row nor moves the path.
		with := len(m.headerLines())
		m.backdrop = true
		bare := ansi.Strip(m.headerLines()[0])
		without := len(m.headerLines())
		m.backdrop = false
		if with != without {
			t.Fatalf("width %d: %d header rows with the button, %d without", width, with, without)
		}
		if at := strings.Index(row, backText); at >= 0 && !strings.Contains(row, "…") && !strings.HasPrefix(bare, strings.TrimRight(row[:at], " ")) {
			t.Fatalf("width %d: the button moved the left part: %q against %q", width, row, bare)
		}
	}
}

func transferAndSelftestCases() []backCase {
	passed := func(_ *Model, _ chan struct{}) (*selftest.Report, error) { return fakeReport("alpha"), nil }
	size := func(open func(t *testing.T) *Model) func(t *testing.T) *Model {
		return func(t *testing.T) *Model { return sizedModel(open(t), 160, 40) }
	}
	return []backCase{
		{"export select", size(exportAt(exportSelect, exportWithout)), true},
		{"export mode", size(exportAt(exportMode, exportWithout)), true},
		{"export preview", size(exportAt(exportPreview, exportWithout)), true},
		{"import mode", size(importAt(importMode)), true},
		{"import preview", size(importAt(importPreview)), true},
		{"selftest running", size(selftestAt(nil)), true},
		{"selftest result", size(selftestAt(passed)), true},
	}
}

func TestBackButtonStandsInTheStepsOfTransferAndSelftest(t *testing.T) {
	for _, c := range transferAndSelftestCases() {
		m := c.open(t)
		if !m.backShown() || !backInHeader(m) {
			t.Errorf("%s: no back button (screen %v):\n%s", c.name, m.screen, frameLines(m)[0])
		}
	}
	// Where esc is locked or answers a question, there is no button.
	m := sizedModel(exportAt(exportPreview, exportWithout)(t), 160, 40)
	m.export.writing = true
	if m.backShown() {
		t.Error("the button stands while the export is written")
	}
	m = sizedModel(importAt(importMode)(t), 160, 40)
	m.imp.working = "writing"
	if m.backShown() {
		t.Error("the button stands while the import works")
	}
	m.imp.working = ""
	for _, step := range []importStep{importApprove, importDelete} {
		m.imp.step = step
		if m.backShown() {
			t.Errorf("the button stands on the import question %v", step)
		}
	}
	m = sizedModel(selftestAt(nil)(t), 160, 40)
	press(t, m, "esc")
	if !m.st.cancelling || m.backShown() {
		t.Error("the button stands while the selftest is ending")
	}
}

func TestBackButtonClickInTransferAndSelftestIsEsc(t *testing.T) {
	m := sizedModel(exportAt(exportMode, exportWithout)(t), 160, 40)
	clickOn(t, m, backText)
	if m.screen != screenTransfer || m.export == nil || m.export.step != exportSelect {
		t.Fatalf("click on the export mode: screen %v, want the selection", m.screen)
	}
	if m.export.ticked() == 0 {
		t.Fatal("the fixture ticks nothing")
	}
	clickOn(t, m, backText)
	if m.screen != screenLeave {
		t.Fatalf("click on a ticked selection: screen %v, want the question esc asks", m.screen)
	}
	m = sizedModel(selftestAt(nil)(t), 160, 40)
	clickOn(t, m, backText)
	if !m.st.cancelling {
		t.Error("click on the running selftest did not end it")
	}
}

func TestTabClickInTransferAndSelftestStopsAtTheFirstQuestion(t *testing.T) {
	m := sizedModel(exportAt(exportSelect, exportWithout)(t), 160, 40)
	clickOn(t, m, "6 Settings")
	if m.export != nil || m.section.area() != areaSettings || m.screen != screenNav {
		t.Errorf("an untouched export: export %v section %v screen %v, want the Settings tab line", m.export != nil, m.section, m.screen)
	}
	m = sizedModel(exportAt(exportPreview, exportWithout)(t), 160, 40)
	before := m.section
	clickOn(t, m, "6 Settings")
	if m.screen != screenLeave || m.section != before {
		t.Errorf("an export with ticks: screen %v section %v, want the leave question", m.screen, m.section)
	}
	m = sizedModel(exportAt(exportPreview, exportWithout)(t), 160, 40)
	m.export.writing = true
	clickOn(t, m, "6 Settings")
	if m.screen != screenTransfer || m.section != before {
		t.Errorf("a write in flight: screen %v section %v, want no effect", m.screen, m.section)
	}
	m = sizedModel(selftestAt(func(_ *Model, _ chan struct{}) (*selftest.Report, error) { return fakeReport("alpha"), nil })(t), 160, 40)
	clickOn(t, m, "6 Settings")
	if m.st != nil || m.section.area() != areaSettings || m.screen != screenNav {
		t.Errorf("a finished selftest: section %v screen %v, want the Settings tab line", m.section, m.screen)
	}
	m = sizedModel(selftestAt(nil)(t), 160, 40)
	clickOn(t, m, "6 Settings")
	if m.screen != screenSelftest || !m.st.cancelling {
		t.Errorf("a running selftest: screen %v, want it ending and still shown", m.screen)
	}
}

// A long config path (the temporary directories of some systems are) does not touch the header; where the
// banner of a release leaves too little room, the keys go before the button, and the header keeps one row.
func TestBackButtonOutranksTheKeysBesideABannerAndIgnoresALongPath(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("deep-directory-", 7))
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	// The temporary root of a test is made on first use, so the model is built in a subtest that finds the
	// long directory first.
	t.Run("long", func(t *testing.T) { backOutranksTheKeys(t, deep) })
}

func backOutranksTheKeys(t *testing.T, deep string) {
	t.Setenv("TMPDIR", deep)
	m := sizedModel(groupModel(t), 120, 40)
	fake := newerRelease()
	m.updater, m.release = fake, fake.check
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	row := frameLines(m)[0]
	if !strings.Contains(row, backText) || !strings.Contains(row, "["+keyLabel(actHelp)+"]") || strings.Contains(row, "deep-directory") {
		t.Fatalf("header = %q, want the button and the keys and no path", row)
	}
	clickOn(t, m, backText)
	if m.screen != screenList {
		t.Errorf("click on the button: screen %v", m.screen)
	}
	pressNew(t, m)
	keysGone := false
	for width := 120; width >= minimumWidth; width-- {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		row := frameLines(m)[0]
		hints := strings.Contains(row, "["+keyLabel(actHelp)+"]")
		button := strings.Contains(row, backText)
		if len(m.headerLines()) != 1 || ansi.StringWidth(row) > width {
			t.Fatalf("width %d: the header is not one row within the terminal: %q", width, row)
		}
		if hints && !button {
			t.Fatalf("width %d: keys without the button: %q", width, row)
		}
		keysGone = keysGone || (button && !hints)
	}
	if !keysGone {
		t.Error("the banner never took the room of the keys before the button's")
	}
}
