package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// detailPaneMinWidth is the workspace width from which the grouped lists show the marked entry in a pane
// at their right, the same threshold as the help pane of forms. Narrower, the pane is left out and the table
// gives up its unimportant columns as before.
const detailPaneMinWidth = helpPaneMinWidth

// paneGap separates the rows from the pane.
const paneGap = " "

// detailMinHeight is the height the pane keeps while the screen has the room, so a short list does not
// squeeze it.
const detailMinHeight = 16

// detailTab is a tab of the detail pane.
type detailTab int

const (
	tabOverview detailTab = iota
	tabDetails
	tabRaw
	tabCount
)

// tabWords are the names of the tabs, in order.
var tabWords = [tabCount]string{"Overview", "Details", "Raw"}

// paneWidth is the width of the detail pane, 0 where there is none: below detailPaneMinWidth, in a section
// without one, where the table has no frame to stand beside, and while no entry is shown for it to describe.
func (m *Model) paneWidth() int {
	if m.width < detailPaneMinWidth || !groupedSection(m.section) || len(m.list.matches) == 0 || !m.tableFramed() {
		return 0
	}
	return min(max(m.width*2/5, 40), 60)
}

// listWidth is the room of the rows: the workspace without the pane.
func (m *Model) listWidth() int {
	if pane := m.paneWidth(); pane > 0 {
		return m.usable(0) - pane - lipgloss.Width(paneGap)
	}
	return m.usable(0)
}

// listFit is fit within listWidth.
func (m *Model) listFit(prefix string) (string, int) { return m.fitIn(m.listWidth(), prefix) }

// detailFocused reports whether the pane has the focus: the rows are the screen's focus, no filter is typed
// and no bar field is open.
func (m *Model) detailFocused() bool {
	return m.detailFocus && m.screen == screenList && m.barFocus == 0 && !m.list.editing && m.paneWidth() > 0
}

// shownTab is the tab the pane shows: the chosen one while the pane has the focus, the overview otherwise.
func (m *Model) shownTab() detailTab {
	if m.detailFocused() {
		return m.detailTab
	}
	return tabOverview
}

// leaveDetail takes the focus off the pane and returns it to the overview.
func (m *Model) leaveDetail() {
	m.detailFocus, m.detailTab = false, tabOverview
}

// marksEntry reports whether the marked row is an entry, which has tabs; a group row has none.
func (m *Model) marksEntry() bool {
	name, ok := m.list.selected()
	return ok && !isGroupRow(name)
}

// updateDetail is a key while the pane has the focus: left/right change the tab, tab goes on to the filter
// bar (or the tab line), shift+tab and the back key return to the rows. Every other key is the rows'.
func (m *Model) updateDetail(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "left", "right":
		if m.marksEntry() {
			by := 1
			if key.String() == "left" {
				by = -1
			}
			m.detailTab = min(max(m.detailTab+detailTab(by), 0), tabCount-1)
		}
		return nil, true
	case "tab":
		m.leaveDetail()
		if m.listBarShown() {
			m.barFocus = 1
		} else {
			m.focusNav()
		}
		return nil, true
	case "shift+tab", keyOf(actBack):
		m.leaveDetail()
		return nil, true
	}
	return nil, false
}

// clickDetailTab is a click on a tab of the pane: the pane takes the focus and shows the tab.
func (m *Model) clickDetailTab(tab detailTab) tea.Cmd {
	if m.screen == screenNav {
		if cmd := m.focusList(); m.screen != screenList {
			return cmd
		}
	}
	if m.screen != screenList {
		return nil
	}
	if m.list.editing {
		m.list.stopFilter()
	}
	m.barFocus = 0
	m.detailFocus, m.detailTab = true, tab
	return nil
}

// withPane sets the detail pane of the marked entry beside the lines of the list that stand above its
// footer, which take up to room lines. The pane is a panel as high as the table: its frame closes in the
// first line of the footer, which is the bottom of the table. A pane that needs more room than the rows
// leave stretches the table frame.
func (m *Model) withPane(lines []string, footer string, room int) string {
	width := m.listWidth()
	for len(lines) < min(room, detailMinHeight-1) {
		lines = append(lines, panelSide("", m.tableRoom(), m.tableFocused(), ""))
	}
	name, _ := m.list.selected()
	panelLines, tabRow, spans := m.detailPanel(name, len(lines)+1)
	top := m.workspaceTop()
	x := width + lipgloss.Width(paneGap)
	m.addHit(x, top, x+m.paneWidth(), top+len(panelLines), func() tea.Cmd { return m.clickDetailTab(m.shownTab()) })
	for tab, span := range spans {
		m.addHit(x+2+span[0], top+1+tabRow, x+2+span[1], top+2+tabRow, func() tea.Cmd { return m.clickDetailTab(detailTab(tab)) })
	}
	out := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		out = append(out, padLine(clipCells(line, width), width)+paneGap+panelLines[i])
	}
	first, rest, more := strings.Cut(footer, "\n")
	out = append(out, padLine(clipCells(first, width), width)+paneGap+panelLines[len(lines)])
	if more {
		return strings.Join(out, "\n") + "\n" + rest
	}
	return strings.Join(out, "\n")
}

// detailPanel is the pane of the entry name as a panel of the given total height: its name and state, its
// provider, the tabs and the lines of the shown tab. It returns the lines, the line of the tabs inside them
// (-1 where there are none) and the cells each tab takes, counted from the first cell of the content.
func (m *Model) detailPanel(name string, height int) ([]string, int, [][2]int) {
	pane := m.paneWidth()
	inner := pane - 4
	rows := max(height-2, 0)
	roomy := rows >= 16
	focused := m.detailFocused()
	var head []string
	tabRow := -1
	var spans [][2]int
	if isGroupRow(name) {
		head = []string{styles.title.Render(truncateCells(m.logText(m.groupTitle(name)), inner)), ""}
	} else {
		head = []string{m.detailHead(name, inner), styles.hint.Render(truncateCells(m.logText(m.providerOf(m.section)(name)), inner))}
		if roomy {
			head = append(head, "")
		}
		strip, cells := detailTabStrip(m.shownTab(), focused)
		tabRow = len(head)
		spans = cells
		head = append(head, strip)
		if roomy {
			head = append(head, "")
		}
	}
	var body []string
	switch {
	case isGroupRow(name):
		body = factLines(m.groupFacts(name), inner, m.logText)
	case m.shownTab() == tabRaw:
		for _, line := range m.rawLines(name) {
			body = append(body, styles.cell.Render(truncateCells(line, inner)))
		}
	default:
		body = factLines(m.detailFacts(name, m.shownTab()), inner, m.logText)
	}
	room := max(rows-len(head), 0)
	if len(body) > room {
		body = body[:room]
		if room > 0 {
			body[room-1] = styles.hint.Render("...")
		}
	}
	lines := append(head, body...)
	return panel("", lines, inner, rows, panelOpts{focused: focused}), tabRow, spans
}

// detailHead is the first line of the pane: the name at the left, the state of the entry at the right.
func (m *Model) detailHead(name string, inner int) string {
	mark := m.detailMark(name)
	room := inner - lipgloss.Width(mark) - 1
	if mark == "" {
		room = inner
	}
	title := styles.title.Render(truncateCells(m.logText(name), max(room, 1)))
	if mark == "" {
		return title
	}
	return padLine(title, max(inner-lipgloss.Width(mark), 0)) + mark
}

// detailMark is the state of the marked entry with its word: a connection's own state, a service's
// connections, a credential's place of storage.
func (m *Model) detailMark(name string) string {
	switch m.section {
	case sectionConnections:
		text := m.connectionStateText(m.cfg.Connections[name])
		kind := statusDim
		switch {
		case strings.HasPrefix(text, stateActive):
			kind = statusOK
		case text == expiredText:
			kind = statusFail
		}
		return statusMark(kind, connectionStateCell(text))
	case sectionServices:
		return statusMark(statusDim, tr(tDetailConnections, len(m.connectionsOf(name))))
	case sectionCredentials:
		return statusMark(statusDim, valueText(storageLabel, m.cells(name)[1]))
	}
	return ""
}

// detailTabStrip draws the tabs of the pane, the shown one in brackets and highlighted, and returns the cells
// each takes. Without colour the brackets still tell the shown tab.
func detailTabStrip(active detailTab, focused bool) (string, [][2]int) {
	words := make([]string, tabCount)
	for tab := range words {
		words[tab] = wordText(tabWords[tab])
	}
	return tabStrip(words, int(active), focused)
}

// detailFact is one label and value of the pane; an empty label continues the one above.
type detailFact struct{ label, value string }

// styledFact is a detailFact whose value has a style of its own.
type styledFact struct {
	detailFact
	// style draws the value; the zero style leaves it plain.
	style lipgloss.Style
	// drawn is a finished one-line value (a status mark); it is neither cleaned nor wrapped.
	drawn string
	// flush starts the value at the left edge instead of the value column, for text that must not break
	// where it could be copied whole.
	flush bool
}

// factLines draws facts as an aligned list of labels and values in width cells. Labels share one column, so
// a value that goes on in a second line stands under its first. Values are made safe by clean.
func factLines(facts []detailFact, width int, clean func(string) string) []string {
	styled := make([]styledFact, len(facts))
	for i, f := range facts {
		styled[i] = styledFact{detailFact: f}
	}
	return factLinesIn(styled, width, detailLabelColumn(), clean)
}

// factLinesIn is factLines with a label column of the given width.
func factLinesIn(facts []styledFact, width, column int, clean func(string) string) []string {
	var lines []string
	for _, f := range facts {
		if f.value == "" && f.drawn == "" {
			continue
		}
		prefix := padLine("", column)
		if f.label != "" {
			prefix = padLine(wordText(f.label)+":", column)
		}
		if f.flush {
			prefix = ""
		}
		if f.drawn != "" {
			lines = append(lines, styles.hint.Render(prefix)+f.drawn)
			continue
		}
		for i, part := range wrapCellsLines(clean(f.value), max(width-lipgloss.Width(prefix), 1)) {
			part = f.style.Render(part)
			if i == 0 {
				lines = append(lines, styles.hint.Render(prefix)+part)
				continue
			}
			lines = append(lines, strings.Repeat(" ", lipgloss.Width(prefix))+part)
		}
	}
	return lines
}

// factColumn is the width of the label column of facts drawn in width cells: the longest label, a colon and a
// gap, never more than a third of the width.
func factColumn(facts []styledFact, width int) int {
	longest := 0
	for _, f := range facts {
		if f.label != "" {
			longest = max(longest, lipgloss.Width(wordText(f.label)))
		}
	}
	return min(longest+2, max(minLabelColumn, width/3))
}

// detailInner is the width of the text of a detail screen and whether it stands in a frame: the frame needs
// the room a framed table needs, and below it the screen keeps the whole width.
func (m *Model) detailInner() (int, bool) {
	if m.tableFramed() {
		return m.usable(0) - 4, true
	}
	return m.usable(0), false
}

// detailChrome is the number of lines a detail screen spends around its text: the frame, or the title and
// the blank line under it.
const detailChrome = 2

// statusFact is the state of a detail screen as a row of its own where the frame has the room; without the
// frame frameDetail puts the mark beside the title.
func (m *Model) statusFact(mark string) []styledFact {
	if _, framed := m.detailInner(); !framed {
		return nil
	}
	return []styledFact{{detailFact: detailFact{label: columnText("status")}, drawn: mark}}
}

// frameDetail draws the lines of a detail screen under its title, in a panel where there is room for the
// frame and as plain text under the title where there is not. mark is the state of what the screen shows.
func (m *Model) frameDetail(title, mark string, lines []string) string {
	inner, framed := m.detailInner()
	if !framed {
		// Without the frame the state stands beside the title, so it costs no line.
		return m.wrapped(styles.title, title) + "  " + mark + "\n\n" + strings.Join(lines, "\n")
	}
	return strings.Join(panel(truncateCells(title, inner-2), lines, inner, len(lines), panelOpts{focused: true}), "\n")
}

// listFacts is a list of values as facts under one label, at most limit of them and then the number left.
func listFacts(label string, values []string, limit int) []detailFact {
	var facts []detailFact
	for i, v := range values {
		if i == limit {
			facts = append(facts, detailFact{"", tr(tDetailMore, len(values)-limit)})
			break
		}
		if i == 0 {
			facts = append(facts, detailFact{label, v})
		} else {
			facts = append(facts, detailFact{"", v})
		}
	}
	return facts
}

// groupTitle is the name of a group row as drawn.
func (m *Model) groupTitle(name string) string {
	key := strings.TrimPrefix(name, groupRowPrefix)
	if key == "" {
		return wordText(noProviderGroup)
	}
	return key
}

// groupFacts describe a group row.
func (m *Model) groupFacts(name string) []detailFact {
	key := strings.TrimPrefix(name, groupRowPrefix)
	return []detailFact{{"Group", m.groupTitle(name)}, {"Entries", fmt.Sprint(len(m.groups.members[key]))}}
}

// connectionsOf names the connections that use the service or credential name of the active section.
func (m *Model) connectionsOf(name string) []string {
	var users []string
	for conn, c := range m.cfg.Connections {
		if (m.section == sectionServices && c.Service == name) || (m.section == sectionCredentials && c.Credential == name) {
			users = append(users, conn)
		}
	}
	sort.Strings(users)
	return users
}

// detailFacts describe the marked entry in the overview or the details tab. They are built from what the
// list already holds: the configuration and the states its cells show, never a secret value. Texts from the
// configuration are made safe to print when they are drawn.
func (m *Model) detailFacts(name string, tab detailTab) []detailFact {
	var facts []detailFact
	add := func(label, value string) { facts = append(facts, detailFact{label, value}) }
	// The name is not repeated: the head of the pane carries it.
	if tab == tabOverview {
		add("Provider", m.providerOf(m.section)(name))
	}
	switch m.section {
	case sectionServices:
		svc := m.cfg.Services[name]
		if tab == tabOverview {
			add("Base URL", maskURL(svc.BaseURL))
			add("Connections", fmt.Sprint(len(m.connectionsOf(name))))
			break
		}
		add("Base URL", maskURL(svc.BaseURL))
		keys := make([]string, 0, len(svc.Options))
		for k := range svc.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		options := make([]string, 0, len(keys))
		for _, k := range keys {
			options = append(options, k+": "+maskOption(k, svc.Options[k]))
		}
		facts = append(facts, listFacts("Options", options, 8)...)
		facts = append(facts, listFacts("Used by", m.connectionsOf(name), 8)...)
	case sectionCredentials:
		cred := m.cfg.Credentials[name]
		cells := m.cells(name)
		if tab == tabOverview {
			add("Storage", valueText(storageLabel, cells[1]))
			add("Secrets", cells[2])
			add("Connections", fmt.Sprint(len(m.connectionsOf(name))))
			break
		}
		add("Type", cred.Type)
		add("Storage", valueText(storageLabel, cells[1]))
		add("Secrets", cells[2])
		facts = append(facts, listFacts("Fields", cred.Fields, 8)...)
		facts = append(facts, listFacts("Used by", m.connectionsOf(name), 8)...)
		add("Description", cred.Description)
	case sectionConnections:
		conn := m.cfg.Connections[name]
		cells := m.cells(name)
		if tab == tabOverview {
			add("Service", conn.Service)
			add("Base URL", maskURL(m.cfg.Services[conn.Service].BaseURL))
			add("Credential", conn.Credential)
			add("State", cells[1])
			if name == m.testName {
				switch {
				case m.testing:
					add("Last test", tr(tDetailRunning))
				case m.testClass != "":
					add("Last test", string(m.testClass))
				}
			}
			add("Effects", cells[3])
			add("Tools", cells[4])
			facts = append(facts, listFacts("Targets", m.targetTexts(conn), 5)...)
			add("Description", conn.Description)
			break
		}
		facts = append(facts, listFacts("Targets", m.targetTexts(conn), 8)...)
		add("Effects", cells[3])
		if conn.Tools != nil {
			facts = append(facts, listFacts("Tools", conn.Tools, 8)...)
			if len(conn.Tools) == 0 {
				add("Tools", cells[4])
			}
		} else {
			add("Tools", cells[4])
		}
		facts = append(facts, listFacts("Read files", conn.Files.Read, 8)...)
		facts = append(facts, listFacts("Write files", conn.Files.Write, 8)...)
		facts = append(facts, listFacts("Forwarded", conn.ForwardSecrets, 8)...)
		facts = append(facts, listFacts("Paths", conn.Paths, 8)...)
		add("Active until", conn.ActiveUntil)
	}
	return facts
}

// targetTexts are the targets of a connection, each with its label where it has one.
func (m *Model) targetTexts(conn config.Connection) []string {
	values := conn.TargetValues()
	out := make([]string, 0, len(values))
	for _, target := range values {
		text := target
		if label := conn.TargetLabels[target]; label != "" {
			text += " (" + label + ")"
		}
		out = append(out, text)
	}
	return out
}

// detailLabels are the labels of the detail pane.
var detailLabels = []string{"Group", "Entries", "Provider", "Base URL", "Connections", "Storage", "Secrets", "Service",
	"Credential", "State", "Effects", "Tools", "Targets", "Description", "Last test", "Options", "Used by", "Type", "Fields",
	"Read files", "Write files", "Forwarded", "Paths", "Active until"}

// detailLabelColumn is the width of the label column of the detail pane: 13 cells, or one more than the
// longest label where a language needs more.
func detailLabelColumn() int {
	column := 13
	for _, label := range detailLabels {
		column = max(column, lipgloss.Width(wordText(label))+2)
	}
	return column
}

// wrapCellsLines is wrapCells as lines; an empty text has none.
func wrapCellsLines(text string, width int) []string {
	wrapped := wrapCells(text, width)
	if wrapped == "" {
		return nil
	}
	return strings.Split(wrapped, "\n")
}
