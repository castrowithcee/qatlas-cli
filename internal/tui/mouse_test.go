package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// frameLines is the frame the editor draws now, without styling, one string per screen row.
func frameLines(m *Model) []string { return strings.Split(ansi.Strip(m.View()), "\n") }

// cellOf finds the first place text stands in the frame, as the column and the row of its first cell. It reads
// the drawn frame, never the click targets, so a test clicks where a person would look.
func cellOf(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	for row, line := range frameLines(m) {
		if at := strings.Index(line, text); at >= 0 {
			return ansi.StringWidth(line[:at]), row
		}
	}
	t.Fatalf("the frame shows no %q:\n%s", text, ansi.Strip(m.View()))
	return 0, 0
}

// cellInRow is cellOf within one screen row.
func cellInRow(t *testing.T, m *Model, row int, text string) int {
	t.Helper()
	lines := frameLines(m)
	if row >= len(lines) || !strings.Contains(lines[row], text) {
		t.Fatalf("row %d shows no %q:\n%s", row, text, ansi.Strip(m.View()))
	}
	return ansi.StringWidth(lines[row][:strings.Index(lines[row], text)])
}

func mouseEvent(x, y int, button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: button, Type: tea.MouseLeft}
}

// clickAt sends a left click the way a terminal would and delivers the commands it produces.
func clickAt(t *testing.T, m *Model, x, y int) {
	t.Helper()
	_, cmd := m.Update(mouseEvent(x, y, tea.MouseButtonLeft))
	deliver(m, cmd)
}

// clickOn clicks the first cell of text.
func clickOn(t *testing.T, m *Model, text string) {
	t.Helper()
	x, y := cellOf(t, m, text)
	clickAt(t, m, x, y)
}

func wheelAt(t *testing.T, m *Model, x, y int, button tea.MouseButton) {
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
			if noColor && strings.Split(m.View(), "\n")[row] != line {
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
	clickOn(t, m, "wiki-main")
	if m.screen != screenForm || m.editing != "wiki-main" {
		t.Fatalf("a click on an entry: screen %v editing %q, want its form", m.screen, m.editing)
	}
}

func TestClickOnARowFromTheTabLineTakesTheFocus(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	m.openSection(sectionConnections)
	m.screen = screenNav
	clickOn(t, m, "tracker-triage")
	if m.screen != screenForm || m.editing != "tracker-triage" {
		t.Fatalf("screen %v editing %q", m.screen, m.editing)
	}
}

func TestClickOnTheFilterBarChoosesAValue(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	clickOn(t, m, "Group: Provider")
	if m.dd == nil || m.barFocus != 1 {
		t.Fatalf("the click on Group did not open its list; the bar reads:\n%s", ansi.Strip(m.View()))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "> Provider") || !strings.Contains(view, "  None") {
		t.Fatalf("the list does not show the values:\n%s", view)
	}
	clickOn(t, m, "  None")
	if m.dd != nil || !m.opts(sectionConnections).flat {
		t.Fatalf("the click on None left the grouping on; the bar reads:\n%s", ansi.Strip(m.View()))
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
	if m.screen != screenForm {
		t.Errorf("a click on a provider: screen %v, want the form", m.screen)
	}
}

func TestClickOnASectionOfAFormMovesTheFocus(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	openSectionByName(t, m, sectionConnections)
	clickOn(t, m, "wiki-docs")
	m.View()
	if m.screen != screenForm || m.sectionListWidth == 0 {
		t.Fatalf("screen %v, section list width %d; want a connection form with its section list", m.screen, m.sectionListWidth)
	}
	entries := m.formSections()
	if len(entries) < 2 {
		t.Fatalf("sections = %v", entries)
	}
	target := entries[len(entries)-1].name
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
	clickOn(t, m, "wiki-docs")
	m.View()
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
	if !strings.Contains(ansi.Strip(m.View()), "(i)") {
		t.Fatalf("no info mark on the focused row:\n%s", ansi.Strip(m.View()))
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
	clickOn(t, m, "wiki-main")
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
	wheelAt(t, m, 5, 10, tea.MouseButtonWheelDown)
	if want := wrap(before+wheelStep, len(m.list.matches)); m.list.cursor != want {
		t.Errorf("after the wheel down the cursor is %d, want %d", m.list.cursor, want)
	}
	wheelAt(t, m, 5, 10, tea.MouseButtonWheelUp)
	if m.list.cursor != before {
		t.Errorf("after the wheel up the cursor is %d, want %d", m.list.cursor, before)
	}

	clickOn(t, m, "wiki-docs")
	m.View()
	if m.screen != screenForm {
		t.Fatalf("screen %v", m.screen)
	}
	focus := m.focus
	wheelAt(t, m, 60, 10, tea.MouseButtonWheelDown)
	if m.focus == focus {
		t.Error("the wheel over the rows of a form left the focus where it was")
	}
	// Over the list of the sections the wheel jumps from section to section.
	section := m.currentSection()
	wheelAt(t, m, 1, 10, tea.MouseButtonWheelDown)
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
	wheelAt(t, m, 60, 20, tea.MouseButtonWheelDown)
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
	wheelAt(t, m, 20, 8, tea.MouseButtonWheelDown)
	wheelAt(t, m, 20, 8, tea.MouseButtonWheelUp)
	if m.formState() != state {
		t.Errorf("form = %q, want %q", m.formState(), state)
	}
}

func TestATooSmallTerminalIgnoresTheMouse(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	x, y := cellOf(t, m, "6 Settings")
	sizedModel(m, minimumWidth-1, minimumHeight-1)
	view := m.View()
	clickAt(t, m, x%(minimumWidth-1), y)
	clickAt(t, m, 0, 0)
	if m.section.area() == areaSettings {
		t.Error("a click in a terminal that is too small switched the area")
	}
	if m.View() != view {
		t.Error("a click changed the size notice")
	}

	// A screen that does not fit the workspace shows the notice, and takes no clicks either.
	m = sizedModel(groupModel(t), 40, minimumHeight)
	openSectionByName(t, m, sectionConnections)
	m.View()
	if !m.tooSmall {
		t.Fatalf("the Connections list fits the smallest terminal; the notice is not shown:\n%s", ansi.Strip(m.View()))
	}
	cursor, area := m.list.cursor, m.section.area()
	for row := 0; row < minimumHeight; row++ {
		for _, x := range []int{0, 3, 12, 30} {
			clickAt(t, m, x, row)
			wheelAt(t, m, x, row, tea.MouseButtonWheelDown)
		}
	}
	if m.list.cursor != cursor || m.section.area() != area || m.screen != screenList {
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
	wheelAt(t, m, 5, 10, tea.MouseButtonWheelDown)
	if m.list.cursor != cursor {
		t.Error("the wheel moved the list with the mouse off")
	}
	if cmd := m.mouseSwitch(true); cmd == nil || !strings.Contains(fmt.Sprintf("%T", cmd()), "disableMouse") {
		t.Errorf("switching off sends %v, want the command that turns the mouse mode off", cmd)
	}
	m.prefs.MouseOff = false
	if cmd := m.mouseSwitch(false); cmd == nil || !strings.Contains(fmt.Sprintf("%T", cmd()), "enableMouseCellMotion") {
		t.Errorf("switching on sends %v, want the command that turns the mouse mode on", cmd)
	}
	if m.mouseSwitch(true) != nil {
		t.Error("an unchanged preference sends a command")
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
	var seen []string
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				walk(sub)
			}
		default:
			seen = append(seen, fmt.Sprintf("%T", msg))
		}
	}
	walk(cmd)
	if !strings.Contains(strings.Join(seen, " "), "disableMouse") {
		t.Errorf("saving sent %v, want the command that turns the mouse mode off", seen)
	}
	if got, _ := uiprefs.Load(path); got.MouseEnabled() {
		t.Error("tui.yaml still has the mouse on")
	}
	clickOn(t, m, "6 Settings")
	if m.screen == screenNav && m.navSub {
		t.Error("a click after switching the mouse off acted")
	}
}

func TestAFailedRestartTurnsTheMouseOnAgainOnlyWhenItIsOn(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 40)
	_, cmd := m.Update(restartFailedMsg{})
	if cmd == nil || !strings.Contains(fmt.Sprintf("%T", cmd()), "enableMouseCellMotion") {
		t.Errorf("a failed restart sends %v with the mouse on, want the command that turns it on", cmd)
	}
	m.prefs.MouseOff = true
	if _, cmd := m.Update(restartFailedMsg{}); cmd != nil {
		t.Errorf("a failed restart sends %v with the mouse off", cmd)
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
					m.Update(mouseEvent(x, y, tea.MouseButtonLeft))
					m.Update(mouseEvent(x, y, tea.MouseButtonWheelDown))
					m.View()
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
	clickOn(t, m, "wiki-main")
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
