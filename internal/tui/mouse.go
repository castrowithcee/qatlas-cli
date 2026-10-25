package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The mouse reaches the editor through a list of click targets that the drawing of a frame fills as it goes:
// every place that draws something clickable says where on the screen it stands and which key press the click
// stands for. A click then does what that key does, through the same handlers, so every check the key meets
// (the admin session, the confirmations, the question before unsaved input is dropped) stays in force, and a
// click is never stronger than its key. Nothing is drawn from the list and nothing is stored beyond one frame.

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

// mouseSwitch is the command that makes the terminal's mouse mode follow a changed preference.
func (m *Model) mouseSwitch(was bool) tea.Cmd {
	switch now := m.mouseEnabled(); {
	case now && !was:
		return tea.EnableMouseCellMotion
	case !now && was:
		return tea.DisableMouse
	}
	return nil
}

// mouseResume turns the mouse mode on again after the terminal was released and restored, which leaves it off.
func (m *Model) mouseResume() tea.Cmd {
	if m.mouseEnabled() {
		return tea.EnableMouseCellMotion
	}
	return nil
}

// wheelStep is how many rows one notch of the wheel moves.
const wheelStep = 3

// updateMouse handles one mouse event. It acts only on a press of the left button and on the wheel; motion and
// release do nothing. A terminal too small for the screen, an editor with the mouse switched off, and the
// capture of a new key take no mouse at all.
func (m *Model) updateMouse(msg tea.MouseMsg) tea.Cmd {
	event := tea.MouseEvent(msg)
	if m.quitting || !m.mouseEnabled() || m.terminalTooSmall() || m.keyCapture || event.Action != tea.MouseActionPress {
		return nil
	}
	wheel := event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown
	if !wheel && event.Button != tea.MouseButtonLeft {
		return nil
	}
	// Draw once more, so the targets are those of the state the event meets.
	m.View()
	if m.infoOpen {
		if !wheel {
			m.infoOpen = false
		}
		return nil
	}
	if m.tooSmall {
		return nil
	}
	if wheel {
		return m.wheel(event)
	}
	hits := append(append([]hit{}, m.frameHits...), m.hits...)
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
		screenTargets, screenPaths, screenTemplate, screenFindings, screenCommand:
		return true
	}
	return false
}

// wheel scrolls: a notch is a few presses of up or down, and over the list of the sections of a form it is a
// press of pgup or pgdown, which jump from section to section.
func (m *Model) wheel(event tea.MouseEvent) tea.Cmd {
	if !m.wheelScreens() {
		return nil
	}
	m.touchAdminSessionIfActive()
	up := event.Button == tea.MouseButtonWheelUp
	key, times := tea.KeyDown, wheelStep
	if up {
		key = tea.KeyUp
	}
	if m.screen == screenForm && event.X < m.sectionListWidth {
		key, times = tea.KeyPgDown, 1
		if up {
			key = tea.KeyPgUp
		}
	}
	var cmds []tea.Cmd
	for range times {
		cmds = append(cmds, m.press(tea.KeyMsg{Type: key}))
	}
	return tea.Batch(cmds...)
}

// press hands a key to the editor as if it had been typed. A mouse event never makes a character: a letter
// is not passed on where the screen reads text.
func (m *Model) press(key tea.KeyMsg) tea.Cmd {
	if key.Type == tea.KeyRunes && m.typing() {
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
func keyByName(name string) (tea.KeyMsg, bool) {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}, true
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}, true
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}, true
	}
	if key, ok := functionKeys[strings.ToUpper(name)]; ok {
		return tea.KeyMsg{Type: key}, true
	}
	if runes := []rune(name); len(runes) == 1 && runes[0] > ' ' && !(runes[0] >= '0' && runes[0] <= '9') {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}, true
	}
	return tea.KeyMsg{}, false
}

// functionKeys are the function keys a dialog may name.
var functionKeys = map[string]tea.KeyType{
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

// leaveForBar prepares the screen for a click on the tab line or the option bar: it is allowed where the keys
// reach the bars, and from a form without unsaved input, which it closes the way esc does. A form with unsaved
// input is answered with the question esc asks, and the click goes no further. It reports whether the click may
// go on.
func (m *Model) leaveForBar() (cmd tea.Cmd, ok bool) {
	if m.wizard != nil || m.infoOpen {
		return nil, false
	}
	switch m.screen {
	case screenNav:
		return nil, true
	case screenList, screenLogs:
		return nil, !m.typing()
	case screenForm:
		if m.payloadRemoveAsk || m.payloadSaving {
			return nil, false
		}
		if m.dirty() {
			return m.requestLeave(), false
		}
		return m.abandon(), true
	}
	return nil, false
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

// registerBars makes the tab line and the option bar clickable.
func (m *Model) registerBars() {
	if m.backdrop || m.tooSmall || m.wizard != nil {
		return
	}
	row := len(m.headerLines())
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

// hitList makes the rows of the list screen, and its filter bar, clickable.
func (m *Model) hitList(header string, start, end int, row func(i int) string) {
	if m.listBarShown() && (m.barFocus > 0 || !(m.list.editing || m.list.query() != "")) {
		m.hitBarFields(m.barLine(m.listWidth()), m.workspaceTop()+1)
	}
	m.hitRows(m.hitTop(header), m.listWidth(), start, end, row, m.clickListRow)
}

// clickListRow is a click on a row of the list: it takes the focus to the row and does what enter does there,
// which opens an entry and folds a group. A filter being typed is only kept.
func (m *Model) clickListRow(i int) tea.Cmd {
	if i < 0 || i >= len(m.list.matches) {
		return nil
	}
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
	m.list.cursor = i
	if m.list.editing {
		m.list.stopFilter()
		return nil
	}
	return m.press(tea.KeyMsg{Type: tea.KeyEnter})
}

// hitOverview makes the selectable items of the Overview, its cards and lines, clickable.
func (m *Model) hitOverview() {
	top := m.workspaceTop()
	for i, h := range m.overviewHits {
		m.addHit(h.x0, top+h.y0, h.x1, top+h.y1, func() tea.Cmd { return m.clickOverview(i) })
	}
}

// clickOverview is a click on an item of the Overview: it opens its area, as enter does.
func (m *Model) clickOverview(i int) tea.Cmd {
	switch m.screen {
	case screenNav:
		m.focusList()
	case screenList:
	default:
		return nil
	}
	m.overviewCursor = i
	return m.press(tea.KeyMsg{Type: tea.KeyEnter})
}

// hitPicker makes the values of the picker clickable. A picker of ticks ticks the value, as space does; any
// other chooses it, as enter does. The info mark of a value opens its explanation.
func (m *Model) hitPicker(header string, start, end int) {
	m.hitRows(m.hitTop(header), m.usable(0), start, end, m.pickerRow, func(i int) tea.Cmd {
		if m.screen != screenPicker || i >= len(m.picker.matches) {
			return nil
		}
		m.picker.cursor = i
		if m.pickerMarks != nil {
			return m.updatePicker(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		}
		return m.press(tea.KeyMsg{Type: tea.KeyEnter})
	})
	y := m.hitTop(header)
	for i := start; i < end; i++ {
		text := m.pickerRow(i)
		m.hitInfoMark(text, y, 0, func() tea.Cmd {
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

// hitTable makes the rows of a windowed list of a screen clickable, each as a press of enter on its row.
func (m *Model) hitTable(l *filterList, on screen, header string, start, end int, row func(i int) string) {
	m.hitRows(m.hitTop(header), m.usable(0), start, end, row, func(i int) tea.Cmd {
		if m.screen != on || i >= len(l.matches) {
			return nil
		}
		l.cursor = i
		return m.press(tea.KeyMsg{Type: tea.KeyEnter})
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

// logBarTargets are the fields of the bar of the Logs screen: a click on an alternative does what left and
// right do on the field, and one on the date opens the dialog enter opens.
func (m *Model) logBarTargets(modeField, dateField, viewField string, modes []string, view int) []barSpec {
	lv := m.logs
	specs := []barSpec{
		{field: ansi.Strip(modeField), prefix: ansi.StringWidth(tr(tLogBarMode)) + 2, values: modes, active: int(lv.mode),
			choose: func(i int) tea.Cmd {
				lead, ok := m.logBarGuard()
				if !ok {
					return lead
				}
				lv.focus, lv.mode = logFocusMode, logMode(i)
				return tea.Batch(lead, m.loadLogs())
			}},
		{field: ansi.Strip(viewField), prefix: ansi.StringWidth(tr(tLogBarView)) + 2,
			values: []string{tr(tLogViewTable), tr(tLogViewHeatmap)}, active: view,
			choose: func(i int) tea.Cmd {
				lead, ok := m.logBarGuard()
				if !ok {
					return lead
				}
				lv.focus = logFocusView
				return tea.Batch(lead, m.setHeat(i == 1))
			}},
	}
	if lv.mode != logModeAll {
		specs = append(specs, barSpec{field: ansi.Strip(dateField), whole: func() tea.Cmd {
			lead, ok := m.logBarGuard()
			if !ok {
				return lead
			}
			lv.focus = logFocusDate
			m.openLogDateDialog()
			return lead
		}})
	}
	return specs
}

// hitLogs makes the rows of the Logs screen clickable: a click takes the focus to the row and opens its
// entry, as enter does.
func (m *Model) hitLogs(head string, start, end int) {
	m.hitRows(m.hitTop(head), m.usable(0), start, end, func(int) string { return "" }, func(i int) tea.Cmd {
		lv := m.logs
		lead, ok := m.logBarGuard()
		if !ok || i >= len(lv.list.matches) {
			return lead
		}
		lv.focus, lv.list.cursor = logFocusRows, i
		return tea.Batch(lead, m.press(tea.KeyMsg{Type: tea.KeyEnter}))
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
		return m.press(tea.KeyMsg{Type: tea.KeyEnter})
	case fieldVaultAction, fieldKey:
		if focused {
			return m.press(tea.KeyMsg{Type: tea.KeyEnter})
		}
	}
	return nil
}
