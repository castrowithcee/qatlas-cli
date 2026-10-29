package tui

import (
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The mouse reaches the editor through a list of click targets that the drawing of a frame fills as it goes:
// every place that draws something clickable says where on the screen it stands and which key press the click
// stands for. A click then does what that key does, through the same handlers, so every check the key meets
// (the admin session, the confirmations, the question before unsaved input is dropped) stays in force, and a
// click is never stronger than its key. A row is only marked by the first click, so that a click that meant to
// scroll or to take the focus opens nothing; the click on the marked row is the press. Nothing is drawn from the
// list and nothing is stored beyond one frame.

// hit is one click target: the cells [x0, x1) by [y0, y1) of the screen and what a click on them does.
type hit struct {
	x0, y0, x1, y1 int
	act            func() tea.Cmd
}

// addHit registers a click target of the workspace. A screen drawn as the background of a dialog has none: the
// dialog is what a click reaches then.
func (m *Model) addHit(x0, y0, x1, y1 int, act func() tea.Cmd) {
	if m.backdrop || m.tooSmall || x1 <= x0 || y1 <= y0 {
		return
	}
	m.hits = append(m.hits, hit{x0, y0, x1, y1, act})
}

// addFrameHit registers a click target of the tab line or the option bar.
func (m *Model) addFrameHit(x0, y0, x1, y1 int, act func() tea.Cmd) {
	if m.backdrop || m.tooSmall || x1 <= x0 || y1 <= y0 {
		return
	}
	m.frameHits = append(m.frameHits, hit{x0, y0, x1, y1, act})
}

// addZone registers a focus zone: the free area of a part of the screen that a key press can focus. A click on
// it does only what tabbing there does, never more, and a zone ranks below every other target, so that a click
// on an element inside it stays that element's click.
func (m *Model) addZone(x0, y0, x1, y1 int, act func() tea.Cmd) {
	if m.backdrop || m.tooSmall || x1 <= x0 || y1 <= y0 {
		return
	}
	m.zones = append(m.zones, hit{x0, y0, x1, y1, act})
}

// workspaceTop is the screen row of the first line of the workspace: below the header, the tab line, the option
// bar of an area that has one, and the rule.
func (m *Model) workspaceTop() int {
	top := len(m.headerLines()) + 2
	if m.hasSubTabs() {
		top++
	}
	return top
}

// hitTop is the screen row of the first line after header, a text the workspace starts with.
func (m *Model) hitTop(header string) int { return m.workspaceTop() + strings.Count(header, "\n") }

// hitRows makes the rows [start, end) of a windowed list clickable. The rows stand one under the other from
// row top on, each as high as row draws it, in the first width cells.
func (m *Model) hitRows(top, width, start, end int, row func(i int) string, act func(i int) tea.Cmd) {
	y := top
	for i := start; i < end; i++ {
		height := strings.Count(row(i), "\n") + 1
		m.addHit(0, y, width, y+height, func() tea.Cmd { return act(i) })
		y += height
	}
}

// mouseEnabled reports whether the editor takes mouse input.
func (m *Model) mouseEnabled() bool { return m.prefs.MouseEnabled() }

// mouseMode is the mouse mode the view asks the terminal for: cell motion while the editor takes the mouse.
func (m *Model) mouseMode() tea.MouseMode {
	if m.mouseEnabled() {
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

// wheelText is how many rows one notch of the wheel moves in a text area; elsewhere it is one step.
const wheelText = 3

// wheelMerge is the longest gap between a wheel event and the event after it that can be its release half. A
// terminal that reports a notch as a press and a release delivers both as presses, back to back.
const wheelMerge = 15 * time.Millisecond

// wheelClamp makes wrap stop at the ends while the wheel presses a key: the wheel never jumps from the last
// row to the first or the other way round, the keys keep their wraparound.
var wheelClamp bool

// wheelSeen is the last wheel event that moved something.
type wheelSeen struct {
	button tea.MouseButton
	x, y   int
	at     time.Time
	ok     bool
}

// repeats reports whether the event is the release half of the notch seen last. It claims at most one event
// per notch: after it dropped one, the next equal event is a notch of its own however close it is.
func (w *wheelSeen) repeats(event tea.Mouse, now time.Time) bool {
	if w.ok && w.button == event.Button && w.x == event.X && w.y == event.Y && now.Sub(w.at) < wheelMerge {
		w.ok = false
		return true
	}
	return false
}

// updateMouse handles one mouse event. It acts only on a press of the left button and on the wheel; motion and
// release do nothing. A terminal too small for the screen, an editor with the mouse switched off, and the
// capture of a new key take no mouse at all.
func (m *Model) updateMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseWheelMsg:
	default:
		return nil
	}
	event := msg.Mouse()
	if m.quitting || !m.mouseEnabled() || m.terminalTooSmall() || m.keyCapture {
		return nil
	}
	wheel := event.Button == tea.MouseWheelUp || event.Button == tea.MouseWheelDown
	if !wheel && event.Button != tea.MouseLeft {
		return nil
	}
	if wheel && m.wheelSeen.repeats(event, m.timeNow()) {
		return nil
	}
	// The targets are those of the state the event meets: the frame drawn after the last message has them, and
	// only a state changed since then needs a new draw. The event itself may change the state.
	if !m.drawn {
		m.render()
	}
	// The frame fits (tooSmall is clear below), so the first key of the event skips the check of the size. What
	// the event does leaves the frame stale.
	m.verified = !m.tooSmall
	defer func() { m.verified, m.drawn = false, false }()
	if m.infoOpen {
		if !wheel {
			m.infoOpen = false
			return nil
		}
		return m.wheel(event)
	}
	if m.tooSmall {
		return nil
	}
	if wheel {
		if m.dd != nil {
			return nil
		}
		cmd := m.wheel(event)
		m.wheelSeen = wheelSeen{button: event.Button, x: event.X, y: event.Y, at: m.timeNow(), ok: true}
		return cmd
	}
	if m.dd != nil {
		return m.clickDropdown(event.X, event.Y)
	}
	hits := append(append(append([]hit{}, m.zones...), m.frameHits...), m.hits...)
	for i := len(hits) - 1; i >= 0; i-- {
		h := hits[i]
		if event.X >= h.x0 && event.X < h.x1 && event.Y >= h.y0 && event.Y < h.y1 {
			m.touchAdminSessionIfActive()
			return h.act()
		}
	}
	return nil
}

// wheelScreens are the screens whose up and down keys move over rows; the wheel is those keys there.
func (m *Model) wheelScreens() bool {
	switch m.screen {
	case screenList, screenForm, screenPicker, screenProviders, screenHelp, screenLogs, screenLogDetail,
		screenTargets, screenPaths, screenTemplate, screenFindings, screenCommand, screenVersions:
		return true
	}
	return m.textScreen()
}

// textScreen reports whether what the screen shows is a plain text area, which the wheel moves by wheelText
// rows. The steps of a transfer or rename that ask for input or move over rows are not.
func (m *Model) textScreen() bool {
	if m.infoOpen {
		return true
	}
	switch m.screen {
	case screenHelp, screenSelftest, screenLogDetail:
		return true
	case screenConfirm:
		return m.approvalDetail != ""
	case screenRename:
		return m.rename != nil && m.rename.plan != nil
	case screenTransfer:
		if e := m.export; e != nil {
			return e.step == exportPreview || e.step == exportResult
		}
		if e := m.imp; e != nil {
			return e.step >= importPreview
		}
	}
	return false
}

// atListTop reports whether up would leave the rows of the list for its filter bar or the tab line.
func (m *Model) atListTop() bool {
	if m.screen != screenList {
		return false
	}
	if m.barFocus != 0 {
		return true
	}
	if m.section == sectionOverview {
		return m.overviewCursor <= 0
	}
	return m.list.cursor <= 0
}

// wheel scrolls: a notch is one press of up or down (three in a text area), and over the list of the sections
// of a form it is a press of pgup or pgdown, which jump from section to section. On the tab line a notch down
// goes into the content and nothing leads back out of it.
func (m *Model) wheel(event tea.Mouse) tea.Cmd {
	up := event.Button == tea.MouseWheelUp
	if !m.infoOpen {
		// A text pane beside the screen scrolls under the wheel without taking the focus or moving the rows.
		for _, pane := range []*paneScroll{&m.detailPane, &m.infoPane} {
			if (pane == &m.detailPane && (m.screen == screenList || m.screen == screenNav) && m.paneWidth() > 0 || pane == &m.infoPane && m.paneShown()) && pane.at(event.X, event.Y) {
				m.touchAdminSessionIfActive()
				pane.wheel(up)
				return nil
			}
		}
	}
	if m.screen == screenNav && !m.infoOpen {
		if up {
			return nil
		}
		m.touchAdminSessionIfActive()
		var cmds []tea.Cmd
		// The option bar of an area is a stop before its content.
		for range 2 {
			if m.screen != screenNav {
				break
			}
			cmds = append(cmds, m.press(tea.KeyPressMsg{Code: tea.KeyDown}))
		}
		return tea.Batch(cmds...)
	}
	if !m.wheelScreens() || (up && !m.infoOpen && m.atListTop()) {
		return nil
	}
	m.touchAdminSessionIfActive()
	key, times := rune(tea.KeyDown), 1
	if up {
		key = tea.KeyUp
	}
	if m.textScreen() {
		times = wheelText
	}
	if m.screen == screenForm && !m.infoOpen && event.X < m.sectionListWidth {
		key = tea.KeyPgDown
		if up {
			key = tea.KeyPgUp
		}
	}
	wheelClamp = true
	defer func() { wheelClamp = false }()
	var cmds []tea.Cmd
	for range times {
		cmds = append(cmds, m.press(tea.KeyPressMsg{Code: key}))
	}
	return tea.Batch(cmds...)
}

// press hands a key to the editor as if it had been typed. A mouse event never makes a character: a letter
// is not passed on where the screen reads text.
func (m *Model) press(key tea.KeyPressMsg) tea.Cmd {
	if key.Text != "" && m.typing() {
		return nil
	}
	_, cmd := m.Update(key)
	return cmd
}

// pressName is press for a key named as a footer names it.
func (m *Model) pressName(name string) tea.Cmd {
	key, ok := keyByName(name)
	if !ok {
		return nil
	}
	return m.press(key)
}

// keyByName is the key press a footer's key name stands for, for the keys a dialog's buttons use.
func keyByName(name string) (tea.KeyPressMsg, bool) {
	switch name {
	case keyOf(actEnter):
		return tea.KeyPressMsg{Code: tea.KeyEnter}, true
	case keyOf(actBack):
		return tea.KeyPressMsg{Code: tea.KeyEscape}, true
	case keyOf(actNext):
		return tea.KeyPressMsg{Code: tea.KeyTab}, true
	}
	if key, ok := functionKeys[strings.ToUpper(name)]; ok {
		return tea.KeyPressMsg{Code: key}, true
	}
	if runes := []rune(name); len(runes) == 1 && runes[0] > ' ' && !(runes[0] >= '0' && runes[0] <= '9') {
		return tea.KeyPressMsg{Code: runes[0], Text: name}, true
	}
	return tea.KeyPressMsg{}, false
}

// functionKeys are the function keys a dialog may name.
var functionKeys = map[string]rune{
	"F2": tea.KeyF2, "F3": tea.KeyF3, "F4": tea.KeyF4, "F6": tea.KeyF6, "F7": tea.KeyF7, "F8": tea.KeyF8,
	"F9": tea.KeyF9,
}

// footerSpan is one key group of a footer line: the cells it stands in and the key it names.
type footerSpan struct {
	x0, x1 int
	key    string
}

// footerLine is a footer line as drawn, and where its key groups stand.
type footerLine struct {
	plain string
	spans []footerSpan
}

// footerKey is the key a footer group opens with ("y delete" names y). Of alternatives ("n/esc") the first
// stands for the group. A group that opens with anything else, such as "1-6" or "type to filter", has none.
func footerKey(group string) string {
	word, _, _ := strings.Cut(group, " ")
	word, _, _ = strings.Cut(word, "/")
	if _, ok := keyByName(word); ok {
		return word
	}
	return ""
}

// hitDialog makes the keys of the footer lines of a dialog clickable: they are its buttons. lines are the
// rows of the dialog text, drawn from column left+2 of row top+1 on, inside its frame.
func (m *Model) hitDialog(lines []string, log []footerLine, top, left int) {
	m.hitFooters(lines, log, top+1, left+2)
}

// hitFooters makes the key groups of the footer lines found among lines clickable. lines are drawn from screen
// row y0 and column x0 on.
func (m *Model) hitFooters(lines []string, log []footerLine, y0, x0 int) {
	used := make([]bool, len(lines))
	for _, footer := range log {
		for i, line := range lines {
			if used[i] || ansi.Strip(line) != footer.plain {
				continue
			}
			used[i] = true
			for _, span := range footer.spans {
				if span.key == "" {
					continue
				}
				name := span.key
				m.addHit(x0+span.x0, y0+i, x0+span.x1, y0+i+1, func() tea.Cmd { return m.pressName(name) })
			}
			break
		}
	}
}

// dialogOrigin is the top row and left column of a dialog centred in a screen of width by height.
func dialogOrigin(dialog []string, width, height int) (top, left int) {
	dialogWidth := 0
	for _, line := range dialog {
		dialogWidth = max(dialogWidth, ansi.StringWidth(line))
	}
	return max((height-len(dialog))/2, 0), max((width-dialogWidth)/2, 0)
}

// navSpan is the cells one entry of the tab line or the option bar stands in.
type navSpan struct{ x0, x1 int }

// spanItems places the items of a bar one after the other from column start on, with sep between them, and
// cuts the places to width cells.
func spanItems(items []string, start int, sep string, width int) []navSpan {
	spans := make([]navSpan, len(items))
	x := start
	for i, item := range items {
		w := ansi.StringWidth(item)
		spans[i] = navSpan{min(x, width), min(x+w, width)}
		x += w + ansi.StringWidth(sep)
	}
	return spans
}

// barLeaveSteps bounds the esc presses a click on a bar makes, so a screen whose esc changes nothing cannot
// loop; the deepest stack of screens is a few levels.
const barLeaveSteps = 6

// backShown reports whether esc leads one level back from the screen shown now, which is where the back button
// stands. It is false on the top level of an area (the tab line, a list, Logs, the page form of a setting),
// whose esc only returns to the tab line, and wherever a question, a dialog or an overlay is open and esc
// answers that instead.
func (m *Model) backShown() bool {
	if m.backdrop || m.infoOpen || m.keyCapture || m.dd != nil || m.testResultShown() {
		return false
	}
	if _, ok := keyByName(keyOf(actBack)); !ok {
		return false
	}
	switch m.screen {
	case screenForm:
		if m.payloadRemoveAsk || m.payloadSaving || (m.wizard != nil && m.wizard.saving) {
			return false
		}
		return m.wizard != nil || !m.section.isPage()
	case screenSummary:
		return m.wizard != nil && !m.wizard.saving
	case screenConfirm:
		return (m.approvalDetail != "" && !m.approveAllConfirm) || (m.tokenDetail != "" && m.tokenRevoke == "")
	case screenHelp:
		return !m.helpOverlay()
	case screenTargets:
		return !m.targetRemove
	case screenPaths:
		return !m.pathRemove
	case screenTemplate:
		return m.template != nil
	case screenRename:
		return m.rename != nil && m.rename.plan == nil
	case screenTransfer:
		// The questions about approving and deleting are answered with esc, not left; a write in flight
		// ignores it.
		if e := m.imp; e != nil {
			return e.working == "" && e.step != importApprove && e.step != importDelete
		}
		return m.export != nil && !m.export.writing
	case screenSelftest:
		// A running test is ended by esc; once it is ending, esc does nothing.
		return m.st != nil && !m.st.cancelling
	case screenPicker, screenProviders, screenLogDetail, screenFindings, screenVersions:
		return true
	}
	return false
}

// pressBack is a press of the key bound to esc; the click on the back button and the climb out of a deeper
// screen use it so they meet every rule the key meets.
func (m *Model) pressBack() tea.Cmd {
	key, ok := keyByName(keyOf(actBack))
	if !ok {
		return nil
	}
	return m.press(key)
}

// registerBack makes the back button of the header clickable.
func (m *Model) registerBack(span navSpan) {
	m.addFrameHit(span.x0, 0, span.x1, 1, m.pressBack)
}

// leaveForBar prepares the screen for a click on the tab line or the option bar: it is allowed where the keys
// reach the bars, and from a form without unsaved input, which it closes the way esc does. From a deeper
// screen it presses esc, one level after the other, up to one of those; a question that esc raises on the way
// (unsaved input) is the end of the click, as a typed search line, a dialog or a save in flight is. It reports
// whether the click may go on.
func (m *Model) leaveForBar() (cmd tea.Cmd, ok bool) {
	if m.wizard != nil || m.infoOpen {
		return nil, false
	}
	var cmds []tea.Cmd
	for range barLeaveSteps {
		switch m.screen {
		case screenNav:
			return tea.Batch(cmds...), true
		case screenList, screenLogs:
			return tea.Batch(cmds...), !m.typing()
		case screenForm:
			if m.payloadRemoveAsk || m.payloadSaving {
				return tea.Batch(cmds...), false
			}
			if m.dirty() {
				return tea.Batch(append(cmds, m.requestLeave())...), false
			}
			return tea.Batch(append(cmds, m.abandon())...), true
		}
		// A search line of a picker or table and the lines of the transfer steps are left by esc at once;
		// every other typed line first ends its own editing, which a click does not do.
		if !m.backShown() || (m.typing() && m.screen != screenPicker && m.screen != screenProviders &&
			m.screen != screenTransfer) {
			break
		}
		cmds = append(cmds, m.pressBack())
	}
	return tea.Batch(cmds...), false
}

// clickArea is a click on an area of the tab line.
func (m *Model) clickArea(a area) tea.Cmd {
	lead, ok := m.leaveForBar()
	if !ok {
		return lead
	}
	return tea.Batch(lead, m.previewArea(a))
}

// clickSubTab is a click on a part of the option bar.
func (m *Model) clickSubTab(s section) tea.Cmd {
	lead, ok := m.leaveForBar()
	if !ok {
		return lead
	}
	cmd := m.previewSection(s)
	m.navSub = true
	return tea.Batch(lead, cmd)
}

// zoneBar is the click on the free area of the tab line or the option bar: it leaves the screen the way a click
// on a tab does, and hands the focus to the bar without changing the section.
func (m *Model) zoneBar(sub bool) tea.Cmd {
	lead, ok := m.leaveForBar()
	if !ok {
		return lead
	}
	if m.screen != screenNav {
		m.focusNav()
	}
	m.navSub = sub && m.hasSubTabs()
	return lead
}

func (m *Model) zoneTabLine() tea.Cmd   { return m.zoneBar(false) }
func (m *Model) zoneOptionBar() tea.Cmd { return m.zoneBar(true) }

// zoneRows is the click on the free area of the rows of a list or the Overview: the rows take the focus, as
// tab or enter from the bars gives it. On the page of a setting that is entering the page. A typed filter is
// left alone.
func (m *Model) zoneRows() tea.Cmd {
	switch m.screen {
	case screenNav:
		return m.focusList()
	case screenList:
	default:
		return nil
	}
	if m.list.editing {
		return nil
	}
	m.barFocus, m.noticeFocus = 0, false
	m.leaveDetail()
	return nil
}

// zoneFilterBar is the click on the free area of the filter bar of a list: the first field takes the focus,
// as tab gives it, unless a field of the bar has it already.
func (m *Model) zoneFilterBar() tea.Cmd {
	if m.screen == screenNav {
		if cmd := m.focusList(); m.screen != screenList {
			return cmd
		}
	}
	if m.screen != screenList || m.list.editing || !m.listBarShown() {
		return nil
	}
	m.leaveDetail()
	m.noticeFocus = false
	if m.barFocus == 0 {
		m.barFocus = 1
	}
	return nil
}

// zoneList registers the zones of a list screen drawn as out with footer below the table: the table, from its
// title to the end of its frame, and the filter bar inside it. A filter being typed has none.
func (m *Model) zoneList(out, footer string) {
	if m.list.editing {
		return
	}
	top := m.workspaceTop()
	end := top + strings.Count(out, "\n") - strings.Count(footer, "\n")
	if m.tableFramed() {
		end++
	}
	m.addZone(0, top, m.listWidth(), end, m.zoneRows)
	if m.listBarShown() {
		row := top + m.listBarOffset()
		m.addZone(0, row, m.listWidth(), row+1, m.zoneFilterBar)
	}
}

// registerBars makes the tab line and the option bar clickable.
func (m *Model) registerBars() {
	if m.backdrop || m.tooSmall || m.wizard != nil {
		return
	}
	row := len(m.headerLines())
	m.addZone(0, row, m.termWidth, row+1, m.zoneTabLine)
	if m.hasSubTabs() {
		m.addZone(0, row+1, m.termWidth, row+2, m.zoneOptionBar)
	}
	areas := m.navTabs()
	for s, span := range areas.spans {
		a := area(s)
		m.addFrameHit(span.x0, row, span.x1, row+1, func() tea.Cmd { return m.clickArea(a) })
	}
	if !m.hasSubTabs() {
		return
	}
	for _, sub := range m.subNavSpans() {
		s := sub.section
		m.addFrameHit(sub.x0, row+1, sub.x1, row+2, func() tea.Cmd { return m.clickSubTab(s) })
	}
}

// barSpec says where one field of an option bar stands in a drawn line and what it offers: its text, the width
// of what stands before its alternatives, the alternatives with the active one drawn in brackets, and what
// choosing the i-th does.
type barSpec struct {
	field  string
	prefix int
	values []string
	active int
	choose func(i int) tea.Cmd
	// id names the field for the list that opens under it (see noteField); "" for a field with none.
	id string
	// whole, when set, makes the whole field one target; the field has no alternatives then.
	whole func() tea.Cmd
}

// hitBar makes the alternatives of the fields of a bar clickable. lines are the drawn rows of the bar from
// screen row top on; a field is found by its text, so a bar that was shortened to a summary has no targets.
func (m *Model) hitBar(lines []string, top int, specs []barSpec) {
	for _, spec := range specs {
		for row, line := range lines {
			plain := ansi.Strip(line)
			at := strings.Index(plain, spec.field)
			if at < 0 {
				continue
			}
			x := ansi.StringWidth(plain[:at]) + spec.prefix
			if spec.id != "" {
				m.noteField(spec.id, x-spec.prefix, top+row)
			}
			if spec.whole != nil {
				m.addHit(x-spec.prefix, top+row, x-spec.prefix+ansi.StringWidth(spec.field), top+row+1, spec.whole)
			}
			for i, value := range spec.values {
				w := ansi.StringWidth(value)
				if i == spec.active {
					w += 2
				}
				m.addHit(x, top+row, x+w, top+row+1, func() tea.Cmd { return spec.choose(i) })
				x += w + 1
			}
			break
		}
	}
}

// listBarOffset is the number of lines above the filter bar of the list screen: its title, the gap under it
// and the top border of the frame.
func (m *Model) listBarOffset() int {
	if m.tableFramed() {
		return m.titleRows() + 1
	}
	return 1
}

// hitList makes the rows of the list screen, and its filter bar, clickable.
func (m *Model) hitList(header string, start, end int, row func(i int) string) {
	if m.listBarShown() && !m.list.editing {
		m.hitBarFields(m.workspaceTop() + m.listBarOffset())
	}
	m.hitRows(m.hitTop(header), m.listWidth(), start, end, row, m.clickListRow)
}

// markOrAct is the click on a row of a list: it marks the row, and only a click on the row that already was
// marked, with the focus on the rows, does act. focused says the rows had the focus before the click.
func markOrAct(cursor *int, i int, focused bool, act func() tea.Cmd) tea.Cmd {
	if !focused || *cursor != i {
		*cursor = i
		return nil
	}
	return act()
}

// clickListRow is a click on a row of the list: it marks the row, and a click on the marked row does what enter
// does there, which opens an entry. A group folds at the first click, which is harmless and undone by the next.
// A filter being typed is only kept.
func (m *Model) clickListRow(i int) tea.Cmd {
	if i < 0 || i >= len(m.list.matches) {
		return nil
	}
	focused := m.screen == screenList && m.barFocus == 0 && !m.list.editing && !m.detailFocused()
	switch m.screen {
	case screenNav:
		if cmd := m.focusList(); m.screen != screenList {
			return cmd
		}
	case screenList:
	default:
		return nil
	}
	m.barFocus = 0
	m.leaveDetail()
	if m.list.editing {
		m.list.cursor = i
		m.list.stopFilter()
		return nil
	}
	enter := func() tea.Cmd { return m.press(tea.KeyPressMsg{Code: tea.KeyEnter}) }
	if isGroupRow(m.list.matches[i]) {
		m.list.cursor = i
		return enter()
	}
	return markOrAct(&m.list.cursor, i, focused, enter)
}

// hitOverview makes the selectable items of the Overview, its cards and lines, clickable.
func (m *Model) hitOverview() {
	top := m.workspaceTop()
	for i, h := range m.overviewHits {
		m.addHit(h.x0, top+h.y0, h.x1, top+h.y1, func() tea.Cmd { return m.clickOverview(i) })
	}
}

// clickOverview is a click on an item of the Overview: it marks the item, and a click on the marked one opens
// its area, as enter does.
func (m *Model) clickOverview(i int) tea.Cmd {
	focused := m.screen == screenList
	switch m.screen {
	case screenNav:
		m.focusList()
	case screenList:
	default:
		return nil
	}
	return markOrAct(&m.overviewCursor, i, focused, func() tea.Cmd { return m.press(tea.KeyPressMsg{Code: tea.KeyEnter}) })
}

// hitPicker makes the values of the picker clickable. A click marks a value; a click on the marked one ticks it
// in a picker of ticks, as space does, and chooses it in any other, as enter does. Its search line takes the
// keys of the rows, so the rows always have the focus. The info mark of a value opens its explanation.
func (m *Model) hitPicker(header string, start, end int) {
	m.hitRows(m.hitTop(header), m.usable(0), start, end, m.pickerRow, func(i int) tea.Cmd {
		if m.screen != screenPicker || i >= len(m.picker.matches) {
			return nil
		}
		return markOrAct(&m.picker.cursor, i, true, func() tea.Cmd {
			if m.pickerMarks != nil {
				return m.updatePicker(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
			}
			return m.press(tea.KeyPressMsg{Code: tea.KeyEnter})
		})
	})
	y := m.hitTop(header)
	for i := start; i < end; i++ {
		text := m.pickerRow(i)
		m.hitInfoMark(m.pickerRowText(i), y, m.boxX(), func() tea.Cmd {
			m.picker.cursor = i
			m.openInfo()
			return nil
		})
		y += strings.Count(text, "\n") + 1
	}
}

// hitInfoMark makes the info mark that ends a line of text clickable: it opens the explanation, as the info
// key does. The text starts on screen row y, in column x0.
func (m *Model) hitInfoMark(text string, y, x0 int, act func() tea.Cmd) {
	mark := strings.TrimSpace(infoMark)
	for row, line := range strings.Split(text, "\n") {
		plain := strings.TrimRight(ansi.Strip(line), " ")
		if !strings.HasSuffix(plain, mark) {
			continue
		}
		x := x0 + ansi.StringWidth(strings.TrimSuffix(plain, mark))
		m.addHit(x, y+row, x+len(mark), y+row+1, act)
	}
}

// hitTable makes the rows of a windowed list of a screen clickable: a click marks a row, and a click on the
// marked row is a press of enter on it.
func (m *Model) hitTable(l *filterList, on screen, header string, start, end int, row func(i int) string) {
	m.hitRows(m.hitTop(header), m.usable(0), start, end, row, func(i int) tea.Cmd {
		if m.screen != on || i >= len(l.matches) {
			return nil
		}
		return markOrAct(&l.cursor, i, true, func() tea.Cmd { return m.press(tea.KeyPressMsg{Code: tea.KeyEnter}) })
	})
}

// hitSelect makes the rows of a windowed list of a screen clickable by choosing the row only; what enter would
// do on it is left to the key.
func (m *Model) hitSelect(l *filterList, on screen, header string, start, end int, row func(i int) string) {
	m.hitRows(m.hitTop(header), m.usable(0), start, end, row, func(i int) tea.Cmd {
		if m.screen == on && i < len(l.matches) {
			l.cursor = i
		}
		return nil
	})
}

// logBarGuard prepares a click on the bar or the rows of the Logs screen: it is allowed where the keys reach
// them, and hands the focus to the screen first. It reports whether the click may go on, with the command the
// focus brought along.
func (m *Model) logBarGuard() (tea.Cmd, bool) {
	lv := m.logs
	if m.section != sectionLogs || lv.dialog != nil || lv.pick != nil || lv.list.editing {
		return nil, false
	}
	switch m.screen {
	case screenLogs:
		return nil, true
	case screenNav:
		cmd := m.focusList()
		return cmd, m.screen == screenLogs
	}
	return nil, false
}

// logBarTargets are the tabs, the fields of the filter line of the Logs screen, by their plain text: a click on a
// tab shows it, on the mode opens its list, on the date the dialog enter opens, on a filter the picker of its
// values, and on the search field starts the search, as the keys do.
func (m *Model) logBarTargets(mode, date string, tabs []string, filters []string, search string) []barSpec {
	lv := m.logs
	guarded := func(act func() tea.Cmd) func() tea.Cmd {
		return func() tea.Cmd {
			lead, ok := m.logBarGuard()
			if !ok {
				return lead
			}
			return tea.Batch(lead, act())
		}
	}
	var specs []barSpec
	for i, text := range tabs {
		specs = append(specs, barSpec{field: text, whole: guarded(func() tea.Cmd {
			lv.focus = logFocusTabs
			return m.setLogTab(logTab(i))
		})})
	}
	specs = append(specs, barSpec{field: mode, id: "logmode", whole: guarded(func() tea.Cmd { m.openLogBarDropdown(); return nil })})
	if lv.mode != logModeAll {
		specs = append(specs, barSpec{field: date, whole: guarded(func() tea.Cmd {
			lv.focus = logFocusDate
			m.openLogDateDialog()
			return nil
		})})
	}
	for k, text := range filters {
		specs = append(specs, barSpec{field: text, whole: guarded(func() tea.Cmd {
			m.openLogPick(k)
			return nil
		})})
	}
	if search != "" {
		specs = append(specs, barSpec{field: strings.TrimRight(search, " "), whole: guarded(func() tea.Cmd {
			lv.focus = logFocusRows
			lv.list.startFilter()
			return nil
		})})
	}
	return specs
}

// hitLogs makes the rows of the Logs screen clickable: a click takes the focus to the row and marks it, and a
// click on the marked row opens its entry, as enter does.
func (m *Model) hitLogs(head string, start, end int) {
	m.hitRows(m.hitTop(head), m.usable(0), start, end, func(int) string { return "" }, func(i int) tea.Cmd {
		lv := m.logs
		focused := m.screen == screenLogs && lv.focus == logFocusRows
		lead, ok := m.logBarGuard()
		if !ok || i >= len(lv.list.matches) {
			return lead
		}
		lv.focus = logFocusRows
		return tea.Batch(lead, markOrAct(&lv.list.cursor, i, focused, func() tea.Cmd { return m.press(tea.KeyPressMsg{Code: tea.KeyEnter}) }))
	})
}

// clickSection is a click on a section of a form, in the list of sections or on its heading: the focus goes to
// the first row of the section, as pgup and pgdown do.
func (m *Model) clickSection(name string) tea.Cmd {
	if !m.formClickable() {
		return nil
	}
	m.enterSection(name)
	m.syncProfile()
	return nil
}

// formClickable reports whether the rows of the form take clicks now: the form is the screen, and no question
// or key capture of it is open.
func (m *Model) formClickable() bool {
	return m.screen == screenForm && m.fields != nil && !m.payloadRemoveAsk && !m.payloadSaving && !m.keyCapture &&
		m.focus >= 0 && m.focus < len(m.fields)
}

// clickField is a click on a row of the form. It takes the focus to the row, which types nothing; on a row that
// opens a choice, a list or the masked prompt of a secret it also does what enter does. A row that acts at
// once (a vault action, the capture of a key) is only focused by the first click, and acts on the second.
func (m *Model) clickField(i int) tea.Cmd {
	if !m.formClickable() || i < 0 || i >= len(m.fields) {
		return nil
	}
	f := m.fields[i]
	if f.readOnly || f.hidden || m.skipped(i) {
		return nil
	}
	focused := i == m.focus
	if !focused {
		m.trimFields()
		m.focus = i
		m.applyFocus()
		m.syncProfile()
	}
	switch f.kind {
	case fieldChoice, fieldMultiChoice, fieldProvider, fieldToolList, fieldTargets, fieldPaths, fieldSecret:
		return m.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	case fieldVaultAction, fieldKey:
		if focused {
			return m.press(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}
	return nil
}

// fragmentWindow is the longest gap between the pieces of one split mouse report. Typing is slower than this.
const fragmentWindow = 200 * time.Millisecond

// bracketHold is how long a "[" that may open the rest of a report, alt+[ or a "[" after an escape, waits
// for it before it counts as the key it is. The text field types it, so it cannot be let through first and
// taken back. It is as long as fragmentWindow: a head the filter would still take as the rest must find the
// "[" still held, or the "[" is typed and the rest dropped. The input reader reports the "[" only after it
// waited for the rest itself, so the rest is later here than at the terminal.
const bracketHold = fragmentWindow

// fragmentTail matches what a mouse report of the SGR form can still lack: the rest of its parameters and the
// final M or m.
var fragmentTail = regexp.MustCompile(`^[0-9;]*[Mm]?`)

// fragmentHead matches the start of such a report that is left once its escape and "[" went into keys of their own.
var fragmentHead = regexp.MustCompile(`^<[0-9;]*[Mm]?`)

// reportStart matches the start of such a report the input reader gave up on, its rest still to come.
var reportStart = regexp.MustCompile(`^\x1b\[<[0-9;]*$`)

// keysMsg delivers keys the filter held back, in order, as one message.
type keysMsg []tea.KeyPressMsg

// heldKeyMsg is the end of the hold of the "[" number id.
type heldKeyMsg struct{ id int }

// fragmentEsc is the filter state after a lone escape; it names that state, not a key to dispatch on.
const fragmentEsc = "esc"

// fragmentFilter drops the pieces of a mouse report that the terminal delivered in more than one read with a
// pause between them longer than the input reader waits: the reader then reports the start it has as an
// unknown sequence, or as an escape or alt+[, and the rest as typed characters like "<65;12;5M", which a text
// field would take. The input reader joins pieces that follow each other within its escape wait, so the filter
// only meets those after a longer pause. A piece counts only when it follows the one before it within
// fragmentWindow and fits the report form, so the same characters typed by hand, pasted, or standing alone pass
// unchanged. Only a "[" that may open a piece is held back, briefly; a lone escape is a real key and passes at
// once, so an escape byte the reader already gave up on still reaches the model as esc.
// On the way it gives keys the names stored key bindings know (see legacyKey and linuxFunctionKey).
type fragmentFilter struct {
	now  func() time.Time
	send func(tea.Msg) // delivers heldKeyMsg to the event loop; without it nothing is held back

	state string // fragmentEsc or "[" when that key came last, "tail" while a report lacks its end
	at    time.Time
	held  *tea.KeyPressMsg
	id    int
}

func newFragmentFilter(now func() time.Time) *fragmentFilter {
	if now == nil {
		now = time.Now
	}
	return &fragmentFilter{now: now}
}

// unknownSequence returns the bytes of an input sequence the input reader could not name.
func unknownSequence(msg tea.Msg) (string, bool) {
	switch u := msg.(type) {
	case uv.UnknownEvent:
		return string(u), true
	case uv.UnknownCsiEvent:
		return string(u), true
	}
	return "", false
}

// filter is the tea.WithFilter function. It runs in the event loop, one message at a time.
func (f *fragmentFilter) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	if h, ok := msg.(heldKeyMsg); ok {
		if f.held == nil || h.id != f.id {
			return nil
		}
		key := *f.held
		f.held = nil
		// The hold is over: a head arriving now is text, whatever the window says, so no race remains
		// between this timer and the window comparison.
		if f.state == "[" {
			f.state = ""
		}
		return key
	}
	if unknown, ok := unknownSequence(msg); ok && (reportStart.MatchString(unknown) || unknown == "\x1b[[") {
		f.state, f.at = "tail", f.now()
		if unknown == "\x1b[[" {
			f.state = "[["
		}
		if held := f.held; held != nil {
			f.held = nil
			return keysMsg{*held}
		}
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		msg = pasteKey(paste.Content)
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return msg
	}
	key = legacyKey(key)
	at, last := f.now(), f.state
	f.state = ""
	if last != "" && at.Sub(f.at) > fragmentWindow {
		last = ""
	}
	if last == "[[" {
		if fn, ok := linuxFunctionKey(key); ok {
			return fn
		}
	}
	held := f.held
	f.held = nil
	_, isPaste := pasted(key)
	text, plain := "", key.Text != "" && !key.Mod.Contains(tea.ModAlt) && !isPaste
	if plain {
		text = key.Text
	}
	var n int
	switch last {
	case "[":
		if plain && strings.HasPrefix(text, "<") {
			n = len(fragmentHead.FindString(text))
		}
	case "tail":
		if plain {
			n = len(fragmentTail.FindString(text))
		}
	}
	if n > 0 {
		if piece := text[:n]; !strings.HasSuffix(piece, "M") && !strings.HasSuffix(piece, "m") {
			f.state, f.at = "tail", at
		}
		if n == len(text) {
			return nil
		}
		return textKey(text[n:])
	}
	// Not a piece: a held "[" was a key after all and goes first.
	switch {
	case isPaste:
	case key.Code == tea.KeyEscape && key.Mod == 0:
		f.state, f.at = fragmentEsc, at
	case f.send != nil && (key.Code == '[' && key.Mod == tea.ModAlt || last == fragmentEsc && text == "["):
		f.state, f.at = "[", at
		f.held = &key
		f.id++
		id, send := f.id, f.send
		time.AfterFunc(bracketHold, func() { send(heldKeyMsg{id}) })
		if held != nil {
			return keysMsg{*held}
		}
		return nil
	}
	if held != nil {
		return keysMsg{*held, key}
	}
	return key
}
