// The Logs section (section 8, see the sectionLogs comment in tui.go): the invocation log, read only. A bar on
// top chooses a day, a range of days, or every recorded day; f narrows the rows by way, MCP client, tool,
// connection, effect, and result in a picker; / searches the text of the rows; enter opens one entry. Every
// row and the bar carry the status the hash chain and the check values give it, found by the same walk
// 'qatlas vault logs verify' makes (invokelog.VerifyLines) with the same checker. Reading and checking run as
// a command, never on the event loop, and never ask for a passphrase: a locked vault leaves the check values
// unchecked, which shows as unverified.
package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// logDateLayout is how a local day is typed and shown.
const logDateLayout = "2006-01-02"

// logMode is what the bar's Mode field selects: one day, a range of days, or every recorded day.
type logMode int

const (
	logModeDay logMode = iota
	logModeRange
	logModeAll
	logModeCount
)

func (mode logMode) String() string { return [...]string{"Day", "Range", "All"}[mode] }

// logStatus is what the chain and the check values say about one line, ordered from best to worst so the
// bar can show the worst of what it selects.
type logStatus int

const (
	logVerified logStatus = iota
	logUnverified
	logGap
	logAltered
)

// label is the status as the screen shows it: always symbol and word together, so it reads without colour.
func (s logStatus) label() string {
	return [...]string{"✓ verified", "? unverified", "… gap", "✗ altered"}[s]
}

func (s logStatus) style() lipgloss.Style {
	return [...]lipgloss.Style{okStyle, warningStyle, warningStyle, failStyle}[s]
}

// logFocus is the part of the Logs screen the keys act on: the rows or one of the bar's two fields.
type logFocus int

const (
	logFocusRows logFocus = iota
	logFocusMode
	logFocusDate
)

// The filters f offers, in the order of the filter line.
const (
	logFilterWay = iota
	logFilterClient
	logFilterTool
	logFilterConnection
	logFilterEffect
	logFilterResult
	logFilterCount
)

var (
	logFilterNames = [logFilterCount]string{"way", "client", "tool", "connection", "effect", "result"}
	logFilterShort = [logFilterCount]string{"way", "client", "tool", "conn", "effect", "result"}
)

// logColumns are the columns of the log table. The status leads and is never cut; the tool is the free
// text; the duration goes first in a narrow terminal, then the connection, then the client.
var logColumns = []column{
	{title: "status"}, {title: "time"}, {title: "way"}, {title: "client", optional: true},
	{title: "tool", flex: true}, {title: "connection", optional: true}, {title: "result"},
	{title: "dur.", optional: true},
}

// maxLogText bounds one field of a log line on screen; a well-formed entry never comes close.
const maxLogText = 256

// logRow is one line of a day file as the table shows it. cells and values are already made safe to print.
type logRow struct {
	date   string
	line   invokelog.LineResult
	status logStatus
	cells  []string
	values [logFilterCount]string
}

// logView is the state of the Logs section.
type logView struct {
	mode logMode
	// day is the day of logModeDay, from and to the range of logModeRange, all as logDateLayout.
	day, from, to string
	focus         logFocus

	// loadID guards against an answer to a load that a later one replaced; loading is true until the latest
	// one answered, and loaded once any did.
	loadID  int
	loading bool
	loaded  bool
	// span is the from and to the rows were loaded for.
	span [2]string
	// err is why the log could not be read at all; note why check values went unchecked.
	err  string
	note string

	rows  []logRow
	worst logStatus
	// filters holds the value each filter keeps, "" for all.
	filters [logFilterCount]string
	// list holds the indexes of the rows the filters keep, as names, and the search over them.
	list filterList
	// table and its layout for layoutWidth are cached per load: laying out every row again on each frame
	// would slow a long range down.
	table       table
	layout      layout
	layoutWidth int

	dialog *logDateDialog
	pick   *logPick
	// detail is the index of the row the detail screen shows.
	detail int
}

// logDateDialog is the small dialog enter opens on the bar's date field: one day, or from and to.
type logDateDialog struct {
	inputs []textinput.Model
	focus  int
	err    string
}

// logPick is the filter dialog f opens, in the picker pattern: first the filter, then its value.
type logPick struct {
	// filter is the filter whose value is chosen, -1 while the filter itself is.
	filter int
	list   filterList
}

func newLogView() *logView {
	today := time.Now().In(time.Local)
	lv := &logView{
		day:   today.Format(logDateLayout),
		from:  today.AddDate(0, 0, -6).Format(logDateLayout),
		to:    today.Format(logDateLayout),
		table: table{columns: logColumns, rows: map[string][]string{}},
	}
	lv.list = newFilterList(lv.searchText)
	lv.list.input.Placeholder = "Type to search"
	return lv
}

// searchText is what / finds a row by: every cell of it and its effect.
func (lv *logView) searchText(name string) string {
	row := lv.row(name)
	if row == nil {
		return ""
	}
	return strings.Join(row.cells, " ") + " " + row.values[logFilterEffect]
}

func (lv *logView) row(name string) *logRow {
	i, err := strconv.Atoi(name)
	if err != nil || i < 0 || i >= len(lv.rows) {
		return nil
	}
	return &lv.rows[i]
}

// bounds is the from and to of the selected days; "" leaves an end open.
func (lv *logView) bounds() (string, string) {
	switch lv.mode {
	case logModeDay:
		return lv.day, lv.day
	case logModeRange:
		return lv.from, lv.to
	}
	return "", ""
}

// logsLoadedMsg carries the lines of the selected days and their status back into the event loop.
type logsLoadedMsg struct {
	id   int
	from string
	to   string
	days []invokelog.DayLines
	note string
	err  error
}

// logVault is the vault whose logs directory the section reads: the run's own, or, for a run without one,
// the vault beside the configuration, the same one 'qatlas vault logs verify' reads.
func (m *Model) logVault() *vault.Vault {
	if v := m.secrets.Vault(); v != nil {
		return v
	}
	return vault.New(filepath.Dir(m.svc.Path()))
}

// loadLogs reads and checks the UTC files overlapping the selected local days as a command. The walk
// reads every file for the chain, but keeps and checks only the overlapping files.
func (m *Model) loadLogs() tea.Cmd {
	lv := m.logs
	lv.loadID++
	lv.loading = true
	id := lv.loadID
	from, to := lv.bounds()
	utcFrom, utcTo := logUTCFileBounds(from, to)
	v := m.logVault()
	return func() tea.Msg {
		checker, note, done := logChecker(context.Background(), v)
		defer done()
		days, err := invokelog.VerifyLines(v.Dir(), checker, utcFrom, utcTo)
		if err != nil && checker != nil {
			// A check that cannot be made at all never passes a line as verified; the lines go unchecked.
			note = "the check values could not be checked: " + err.Error()
			days, err = invokelog.VerifyLines(v.Dir(), nil, utcFrom, utcTo)
		}
		return logsLoadedMsg{id: id, from: from, to: to, days: days, note: note, err: err}
	}
}

// logUTCFileBounds selects every UTC day file that can overlap the chosen local days.
func logUTCFileBounds(from, to string) (string, string) {
	if from != "" {
		day, _ := time.ParseInLocation(logDateLayout, from, time.Local)
		from = day.UTC().Format(logDateLayout)
	}
	if to != "" {
		day, _ := time.ParseInLocation(logDateLayout, to, time.Local)
		to = day.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC().Format(logDateLayout)
	}
	return from, to
}

// logChecker returns what checks the log's check values of v, the same way 'qatlas vault logs verify' does:
// the key of a vault unlocked in this process; else a running vault process, which only ever answers
// whether a check value matches; else nothing, since the editor never asks for the passphrase for this.
// note says why there is no checker, and done clears a key this derived.
func logChecker(ctx context.Context, v *vault.Vault) (invokelog.Checker, string, func()) {
	nothing := func() {}
	const locked = "the vault is locked, so check values are not checked; ctrl+l unlocks it"
	state, err := v.State()
	switch {
	case err != nil:
		return nil, "the vault's state cannot be read, so check values are not checked", nothing
	case state == vault.StateUnlocked:
		key, err := v.LogKey()
		if err != nil {
			return nil, locked, nothing
		}
		return key, "", key.Clear
	case state != vault.StateLocked:
		return nil, "the vault is not encrypted, so there is no key to check check values with", nothing
	case !vaultproc.Supported:
		return nil, locked, nothing
	}
	client, err := vaultproc.ProcessClientOf(v)
	if err != nil {
		return nil, locked, nothing
	}
	if _, err := client.Status(ctx); err != nil {
		if !errors.Is(err, vaultproc.ErrNotRunning) {
			return nil, "the vault process cannot check the check values: " + err.Error(), nothing
		}
		return nil, locked, nothing
	}
	return client.LogChecker(ctx), "", nothing
}

// handleLogsLoaded takes the answer of the latest load; an earlier one is dropped.
func (m *Model) handleLogsLoaded(msg logsLoadedMsg) tea.Cmd {
	lv := m.logs
	if msg.id != lv.loadID {
		return nil
	}
	lv.loading, lv.loaded = false, true
	lv.err, lv.note = "", ""
	if msg.err != nil {
		lv.err = m.redactor.Apply(msg.err.Error())
	}
	if msg.note != "" {
		lv.note = m.redactor.Apply(msg.note)
	}
	lv.rows, lv.worst = nil, logVerified
	lv.table = table{columns: logColumns, rows: map[string][]string{}}
	lv.layoutWidth = -1
	for _, day := range msg.days {
		for _, line := range day.Lines {
			// An undated line cannot be assigned to a local day. Keep it with its overlapping UTC
			// file so the selected view still shows its verification status.
			if line.Parsed && !line.Entry.Time.IsZero() &&
				((msg.from != "" && line.Entry.Time.In(time.Local).Format(logDateLayout) < msg.from) ||
					(msg.to != "" && line.Entry.Time.In(time.Local).Format(logDateLayout) > msg.to)) {
				continue
			}
			row := m.logRowOf(day.Date, line, msg.from != msg.to || msg.from == "")
			lv.worst = max(lv.worst, row.status)
			lv.table.rows[strconv.Itoa(len(lv.rows))] = row.cells
			lv.rows = append(lv.rows, row)
		}
	}
	span := [2]string{msg.from, msg.to}
	if span != lv.span {
		lv.list.cursor, lv.list.offset = 0, 0
	}
	lv.span = span
	lv.applyFilters()
	return nil
}

// statusOf is the status of one line: altered when it does not parse, fails its check value, or breaks the
// chain; a gap when entries are missing right before it; verified when its check value matched; unverified
// otherwise, which is a line without a check value or one nobody could check.
func statusOf(line invokelog.LineResult) logStatus {
	switch {
	case !line.Parsed || line.MAC == invokelog.MACInvalid || line.Chain != invokelog.ChainIntact:
		return logAltered
	case line.Missing > 0:
		return logGap
	case line.MAC == invokelog.MACValid:
		return logVerified
	}
	return logUnverified
}

// logRowOf builds the row of one line. withDate puts the local day in front of the time.
func (m *Model) logRowOf(date string, line invokelog.LineResult, withDate bool) logRow {
	row := logRow{date: date, line: line, status: statusOf(line)}
	e := line.Entry
	when, way, client, tool, conn, effect, result, dur := "UTC file "+date, "-", "-", "-", "-", "-", "-", ""
	switch {
	case !line.Parsed:
		tool = "(unreadable line)"
	case e.Kind == invokelog.KindCut:
		if !e.Time.IsZero() {
			when = logTableTime(e.Time, withDate)
		}
		tool = "retention cut"
		if e.Cut != nil {
			tool = fmt.Sprintf("retention cut through #%d", e.Cut.ThroughSeq)
		}
	default:
		if !e.Time.IsZero() {
			when = logTableTime(e.Time, withDate)
		}
		way = orDash(strings.ToUpper(m.logText(e.Path)))
		if e.Client != nil {
			client = orDash(m.logText(e.Client.Name))
		}
		tool = orDash(m.logText(e.Operation))
		conn = orDash(m.logText(e.Connection))
		effect = orDash(m.logText(e.Effect))
		result = orDash(m.logText(e.Result))
		if e.Result == "success" {
			result = "ok"
		}
		dur = formatDuration(e.DurationMS)
	}
	row.cells = []string{row.status.label(), when, way, client, tool, conn, result, dur}
	row.values = [logFilterCount]string{way, client, tool, conn, effect, result}
	return row
}

func logTableTime(t time.Time, withDate bool) string {
	local := t.In(time.Local)
	_, offset := local.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	zone := fmt.Sprintf("%s%02d", sign, offset/3600)
	if offset%3600 != 0 {
		zone += fmt.Sprintf("%02d", offset%3600/60)
	}
	if withDate {
		return local.Format(logDateLayout+" 15:04") + zone
	}
	return local.Format("15:04") + zone
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// logText makes one field of a log line safe to print. A log file is not trusted: a changed line may hold
// anything, so a registered secret is redacted, invalid UTF-8, control characters and invisible formatting
// characters (which terminal escape sequences and direction overrides are made of) are replaced, and an
// overlong value is cut.
func (m *Model) logText(s string) string {
	s = strings.ToValidUTF8(m.redactor.Apply(s), "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '�'
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > maxLogText {
		s = string([]rune(s)[:maxLogText-1]) + "…"
	}
	return s
}

// applyFilters shows the rows every filter keeps, in file order; the search then narrows them further.
func (lv *logView) applyFilters() {
	names := make([]string, 0, len(lv.rows))
	for i, row := range lv.rows {
		keep := true
		for k, want := range lv.filters {
			if want != "" && row.values[k] != want {
				keep = false
				break
			}
		}
		if keep {
			names = append(names, strconv.Itoa(i))
		}
	}
	lv.list.setItems(names)
}

// openLogs shows the Logs section with the rows in focus and loads the selected days anew.
func (m *Model) openLogs() tea.Cmd {
	m.stopTest()
	m.testName, m.editing, m.confirmRole = "", "", ""
	m.clearMessages()
	lv := m.logs
	lv.focus, lv.dialog, lv.pick = logFocusRows, nil, nil
	m.screen = screenLogs
	return m.loadLogs()
}

// reloadShownLogs loads the Logs section again when it is the active section, so a vault unlocked or locked
// from it shows the check values checked or unchecked at once.
func (m *Model) reloadShownLogs() tea.Cmd {
	if m.section != sectionLogs || !m.logs.loaded {
		return nil
	}
	return m.loadLogs()
}

// updateLogs handles the keys of the Logs screen and of the dialogs over it.
func (m *Model) updateLogs(key tea.KeyMsg) tea.Cmd {
	lv := m.logs
	switch {
	case lv.dialog != nil:
		return m.updateLogDateDialog(key)
	case lv.pick != nil:
		return m.updateLogPick(key)
	case lv.list.editing:
		return m.updateLogSearch(key)
	}
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "q":
		return m.quit()
	case "?":
		m.openHelp()
	case "esc":
		if lv.list.query() != "" {
			lv.list.clearFilter()
			return nil
		}
		m.focusNav()
	case "tab":
		lv.moveFocus(1)
	case "shift+tab":
		lv.moveFocus(-1)
	case "left", "h", "right", "l":
		by := 1
		if key.String() == "left" || key.String() == "h" {
			by = -1
		}
		switch lv.focus {
		case logFocusMode:
			lv.mode = logMode(wrap(int(lv.mode)+by, int(logModeCount)))
			return m.loadLogs()
		case logFocusDate:
			m.openLogDateDialog()
		default:
			if by < 0 {
				m.focusNav()
			}
		}
	case "enter", " ":
		switch lv.focus {
		case logFocusDate:
			m.openLogDateDialog()
		case logFocusRows:
			if row, ok := lv.list.selected(); ok {
				lv.detail, _ = strconv.Atoi(row)
				m.screen = screenLogDetail
				m.clearMessages()
			}
		}
	case "up", "k":
		lv.focus = logFocusRows
		lv.list.move(-1)
	case "down", "j":
		lv.focus = logFocusRows
		lv.list.move(1)
	case "pgup", "pgdown", "home", "end":
		lv.focus = logFocusRows
		start, end := m.logsWindow()
		lv.list.page(key.String(), start, end)
	case "f":
		m.openLogPick(-1)
	case "/":
		lv.focus = logFocusRows
		lv.list.startFilter()
	}
	return nil
}

// moveFocus steps through the bar's fields and the rows; All has no date field to stop at.
func (lv *logView) moveFocus(by int) {
	order := []logFocus{logFocusMode, logFocusDate, logFocusRows}
	if lv.mode == logModeAll {
		order = []logFocus{logFocusMode, logFocusRows}
	}
	at := len(order) - 1
	for i, f := range order {
		if f == lv.focus {
			at = i
		}
	}
	lv.focus = order[wrap(at+by, len(order))]
}

// updateLogSearch is the Logs screen while its search is typed; every printable key is search text.
func (m *Model) updateLogSearch(key tea.KeyMsg) tea.Cmd {
	lv := m.logs
	switch key.String() {
	case "esc":
		lv.list.clearFilter()
	case "enter":
		lv.list.stopFilter()
	case "up":
		lv.list.move(-1)
	case "down":
		lv.list.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.logsWindow()
		lv.list.page(key.String(), start, end)
	default:
		return lv.list.updateFilter(key)
	}
	return nil
}

// updateLogDetail handles the detail screen of one entry.
func (m *Model) updateLogDetail(key tea.KeyMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "esc":
		m.screen = screenLogs
	case "?":
		m.openHelp()
	}
	return nil
}

func newLogInput(value string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = len(logDateLayout)
	in.Placeholder = "YYYY-MM-DD"
	in.Cursor.SetMode(cursor.CursorStatic)
	in.SetValue(value)
	in.CursorEnd()
	return in
}

// openLogDateDialog opens the dialog of the bar's date field: one day, or from and to.
func (m *Model) openLogDateDialog() {
	lv := m.logs
	d := &logDateDialog{}
	switch lv.mode {
	case logModeDay:
		d.inputs = []textinput.Model{newLogInput(lv.day)}
	case logModeRange:
		d.inputs = []textinput.Model{newLogInput(lv.from), newLogInput(lv.to)}
	default:
		return
	}
	d.inputs[0].Focus()
	lv.dialog = d
	m.clearMessages()
}

func (m *Model) updateLogDateDialog(key tea.KeyMsg) tea.Cmd {
	lv, d := m.logs, m.logs.dialog
	switch key.String() {
	case "esc":
		lv.dialog = nil
		return nil
	case "tab", "shift+tab", "up", "down":
		d.inputs[d.focus].Blur()
		by := 1
		if key.String() == "shift+tab" || key.String() == "up" {
			by = -1
		}
		d.focus = wrap(d.focus+by, len(d.inputs))
		d.inputs[d.focus].Focus()
		return nil
	case "enter":
		var days []string
		for _, in := range d.inputs {
			value := strings.TrimSpace(in.Value())
			if _, err := time.Parse(logDateLayout, value); err != nil {
				d.err = "a date is typed as YYYY-MM-DD, for example " + time.Now().In(time.Local).Format(logDateLayout)
				return nil
			}
			days = append(days, value)
		}
		if len(days) == 2 && days[0] > days[1] {
			d.err = "from must not be after to"
			return nil
		}
		if len(days) == 1 {
			lv.day = days[0]
		} else {
			lv.from, lv.to = days[0], days[1]
		}
		lv.dialog = nil
		return m.loadLogs()
	}
	var cmd tea.Cmd
	d.inputs[d.focus], cmd = d.inputs[d.focus].Update(key)
	d.err = ""
	return cmd
}

// openLogPick opens the filter dialog: the filters for filter -1, else the values of that filter, which are
// all and every value the loaded rows hold for it. Typing searches at once, as in every picker.
func (m *Model) openLogPick(filter int) {
	lv := m.logs
	p := &logPick{filter: filter}
	var names []string
	if filter < 0 {
		p.list = newFilterList(func(name string) string {
			k, _ := strconv.Atoi(name)
			return fmt.Sprintf("%-11s %s", logFilterNames[k], filterText(lv.filters[k]))
		})
		for k := range logFilterNames {
			names = append(names, strconv.Itoa(k))
		}
	} else {
		p.list = newFilterList(filterText)
		seen := map[string]bool{}
		for _, row := range lv.rows {
			if v := row.values[filter]; !seen[v] {
				seen[v] = true
				names = append(names, v)
			}
		}
		sort.Strings(names)
		names = append([]string{""}, names...)
	}
	p.list.input.Placeholder = "Type to search"
	p.list.reset(names)
	p.list.startFilter()
	if filter >= 0 {
		p.list.selectName(lv.filters[filter])
	}
	lv.pick = p
	m.clearMessages()
}

// filterText shows a filter value; "" keeps every row.
func filterText(value string) string {
	if value == "" {
		return "all"
	}
	return value
}

func (m *Model) updateLogPick(key tea.KeyMsg) tea.Cmd {
	lv, p := m.logs, m.logs.pick
	switch key.String() {
	case "esc":
		if p.filter >= 0 {
			m.openLogPick(-1)
			m.logs.pick.list.selectName(strconv.Itoa(p.filter))
			return nil
		}
		lv.pick = nil
	case "enter":
		choice, ok := p.list.selected()
		if !ok {
			return nil
		}
		if p.filter < 0 {
			k, _ := strconv.Atoi(choice)
			m.openLogPick(k)
			return nil
		}
		lv.filters[p.filter] = choice
		lv.pick = nil
		lv.applyFilters()
	case "up":
		p.list.move(-1)
	case "down":
		p.list.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.logPickWindow()
		p.list.page(key.String(), start, end)
	default:
		return p.list.updateFilter(key)
	}
	return nil
}

// logsView draws the Logs section: the bar, the filter and search lines, the table, and the keys. It is also
// what the workspace shows beside the sidebar while the section is only marked there.
func (m *Model) logsView() string {
	lv := m.logs
	if m.screen == screenLogs && lv.dialog != nil {
		return m.logDateDialogView()
	}
	if m.screen == screenLogs && lv.pick != nil {
		return m.logPickView()
	}
	head, foot := m.logsFrame()
	var b strings.Builder
	b.WriteString(head)
	start, end := m.logsWindow()
	l := m.logLayout()
	for i := start; i < end; i++ {
		b.WriteString(m.logRowLine(i, l) + "\n")
	}
	b.WriteString(foot)
	return b.String()
}

// logLayout lays the table out for the workspace, once per width and load.
func (m *Model) logLayout() layout {
	lv := m.logs
	_, width := m.fit("  ")
	if lv.layoutWidth != width {
		lv.layout, lv.layoutWidth = lv.table.layout(width), width
	}
	return lv.layout
}

// logRowLine draws the shown row at index i. The marked row carries "> " and the full-width highlight while
// the rows have the focus; the time is faint on every other row, which keeps its zebra shade.
func (m *Model) logRowLine(i int, l layout) string {
	lv := m.logs
	row := lv.row(lv.list.matches[i])
	active := m.screen == screenLogs && lv.focus == logFocusRows && i == lv.list.cursor
	if active {
		return m.listRow(true, false, lv.table.line(row.cells, l))
	}
	blank, width := m.fit("  ")
	line := padLine(lv.table.line(row.cells, l), width)
	head := clipCells(line, l.widths[0]+lipgloss.Width(columnGap))
	rest := strings.TrimPrefix(line, head)
	when := clipCells(rest, l.widths[1])
	tail := strings.TrimPrefix(rest, when)
	base := lipgloss.NewStyle()
	if i%2 == 1 {
		base = zebraStyle
	}
	return base.Render(blank+head) + base.Faint(true).Render(when) + base.Render(tail)
}

// logsFrame is everything of the Logs screen except its rows. The bar stands on one line where it fits and
// field by field where it does not; a terminal too low for the rows drops the rules and the idle search line
// first.
func (m *Model) logsFrame() (string, string) {
	lv := m.logs
	focused := m.screen == screenLogs
	width := m.usable(0)
	status := hintStyle.Render("loading ...")
	if lv.loaded {
		status = lv.worst.style().Render(lv.worst.label())
	}
	marker := func(f logFocus) string {
		if focused && lv.focus == f {
			return "> "
		}
		return "  "
	}
	var chips []string
	for mode := logModeDay; mode < logModeCount; mode++ {
		if mode == lv.mode {
			chips = append(chips, activeRowStyle.Render("["+mode.String()+"]"))
			continue
		}
		chips = append(chips, mode.String())
	}
	modeField := marker(logFocusMode) + "Mode: " + strings.Join(chips, " ")
	var dateField string
	switch lv.mode {
	case logModeDay:
		dateField = marker(logFocusDate) + "Date: [" + lv.day + "]"
	case logModeRange:
		dateField = marker(logFocusDate) + "Range: [" + lv.from + ".." + lv.to + "]"
	default:
		dateField = "  " + hintStyle.Render("(all recorded days)")
	}
	var bar []string
	left := modeField + "  " + dateField
	switch {
	case lipgloss.Width(left)+2+lipgloss.Width(status) <= width:
		bar = []string{left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(status)) + status}
	case lipgloss.Width(dateField)+2+lipgloss.Width(status) <= width:
		bar = []string{modeField, dateField + "  " + status}
	default:
		bar = []string{modeField, dateField, "  " + status}
	}

	var filters []string
	for k, value := range lv.filters {
		filters = append(filters, logFilterShort[k]+"="+filterText(value))
	}
	filterLine := "  filter: " + strings.Join(filters, " ")
	if lipgloss.Width(filterLine)+len("  f change") <= width {
		filterLine += "  f change"
	}
	filterLine = hintStyle.Render(truncateCells(filterLine, width))

	var search string
	switch {
	case lv.list.editing:
		prefix, room := m.fit("  search: ")
		in := lv.list.input
		in.Width = max(room-1, 1)
		search = prefix + in.View()
	case lv.list.query() != "":
		prefix, room := m.fit("  search: ")
		search = prefix + truncateCells(lv.list.query(), room)
	}
	rule := quietBorder.Render(strings.Repeat("─", width))

	var body []string
	shown := len(lv.list.matches)
	switch {
	case lv.err != "":
		body = append(body, m.wrapped(failStyle, "error: the log cannot be read: "+lv.err))
	case !lv.loaded:
		body = append(body, m.wrapped(hintStyle, "Reading the log ..."))
	case len(lv.rows) == 0:
		body = append(body, m.wrapped(lipgloss.NewStyle(), lv.emptyText()))
	case shown == 0:
		body = append(body, m.wrapped(hintStyle,
			"No entry matches the filters and the search. f changes the filters, esc clears the search."))
	default:
		body = append(body, hintStyle.Render(clipCells("  "+lv.table.header(m.logLayout()), width)))
	}

	keys := m.keyHint(m.logKeys())

	// Every part ends its line itself, so the rows stand right above the foot; a compact frame leaves out
	// the rules, the idle search line, the position, and the note, which the detail repeats.
	build := func(compact bool) (string, string) {
		lines := append([]string{}, bar...)
		lines = append(lines, filterLine)
		if search != "" {
			lines = append(lines, search)
		} else if !compact {
			lines = append(lines, hintStyle.Render(truncateCells("  / search", width)))
		}
		if !compact {
			lines = append(lines, rule)
		}
		lines = append(lines, body...)
		if compact {
			return strings.Join(lines, "\n") + "\n", strings.TrimPrefix(keys, "\n") + m.notes()
		}
		foot := keys
		if shown > 0 && focused {
			foot = withPosition(foot, fmt.Sprintf("%d/%d · %d%%", lv.list.cursor+1, shown,
				(lv.list.cursor+1)*100/shown), width)
		}
		if lv.note != "" && lv.loaded && lv.hasUnchecked() {
			foot = "\n" + m.wrapped(hintStyle, lv.note) + foot
		}
		return strings.Join(lines, "\n") + "\n", rule + foot + m.notes()
	}
	head, f := build(false)
	if shown > 0 && m.height-strings.Count(head, "\n")-strings.Count(f, "\n")-1 < 1 {
		return build(true)
	}
	return head, f
}

// emptyText says that the selected days hold no entry.
func (lv *logView) emptyText() string {
	switch lv.mode {
	case logModeDay:
		return "No entries for " + lv.day + "."
	case logModeRange:
		return "No entries from " + lv.from + " to " + lv.to + "."
	}
	return "No entries recorded yet."
}

// hasUnchecked reports whether a row carries a check value nobody checked, the only case note explains.
func (lv *logView) hasUnchecked() bool {
	for _, row := range lv.rows {
		if row.line.MAC == invokelog.MACUnchecked {
			return true
		}
	}
	return false
}

// logKeys is the key line of the Logs screen for what has the focus; a narrow workspace gets the short form.
func (m *Model) logKeys() string {
	lv := m.logs
	if m.screen != screenNav && m.usable(0) < 60 {
		switch {
		case lv.list.editing:
			return "type to search · enter keep · esc clear"
		case lv.focus == logFocusMode:
			return "left/right mode · tab next · f filter · esc back · q quit"
		case lv.focus == logFocusDate:
			return "enter date · tab next · f filter · esc back · q quit"
		case len(lv.list.matches) == 0:
			return "tab bar · f filter · / search · esc back · q quit"
		}
		return "enter detail · tab bar · f filter · / search · esc back · q quit"
	}
	switch {
	case m.screen == screenNav:
		return m.navKeys()
	case lv.list.editing:
		return "type to search · up/down move · enter keep search · esc clear search"
	case lv.focus == logFocusMode:
		return "left/right change mode · tab next · f filter · / search · 1-8 section · esc back · ? help · q quit"
	case lv.focus == logFocusDate:
		return "enter or left/right change the date · tab next · f filter · / search · 1-8 section · esc back · ? help · q quit"
	case len(lv.list.matches) == 0:
		return "tab focus bar · left/right change · f filter · / search · 1-8 section · esc back · ? help · q quit"
	}
	keys := "enter details · tab focus bar · left/right change · f filter · / search · 1-8 section · esc back · ? help · q quit"
	if lv.list.query() != "" {
		keys = strings.Replace(keys, "esc back", "esc clear search", 1)
	}
	return keys
}

// withPosition puts pos at the right end of the first line of a key line, or on a line of its own where
// that line has no room left for it.
func withPosition(keys, pos string, width int) string {
	lines := strings.Split(keys, "\n")
	if len(lines) < 2 {
		return keys
	}
	if gap := width - lipgloss.Width(lines[1]) - lipgloss.Width(pos); gap >= 2 {
		lines[1] += strings.Repeat(" ", gap) + hintStyle.Render(pos)
		return strings.Join(lines, "\n")
	}
	return keys + "\n" + hintStyle.Render(truncateCells(pos, width))
}

// logsWindow is the range of shown rows that fits between the frame of the Logs screen; each row is one line.
func (m *Model) logsWindow() (int, int) {
	head, foot := m.logsFrame()
	return m.windowIn(&m.logs.list, head, foot, func(int) string { return "" })
}

// logDetailView shows every field of one entry, and its status with the reason.
func (m *Model) logDetailView() string {
	lv := m.logs
	if lv.detail < 0 || lv.detail >= len(lv.rows) {
		return m.hint("esc back")
	}
	row := lv.rows[lv.detail]
	e := row.line.Entry
	title := "Log entry UTC file " + row.date
	if row.line.Parsed && !e.Time.IsZero() {
		title = "Log entry " + e.Time.In(time.Local).Format(logDateLayout+" 15:04:05 -0700")
	}
	var b strings.Builder
	b.WriteString(m.wrapped(titleStyle, title) + "\n\n")
	field := func(label, value string) { b.WriteString(m.formRow(false, label, value) + "\n") }
	field("status", row.status.label()+" ("+m.logReason(row.line)+")")
	field("UTC file", row.date)
	if row.line.Parsed {
		field("sequence", strconv.FormatUint(e.Seq, 10))
	}
	switch {
	case !row.line.Parsed:
		field("day", row.date)
	case e.Kind == invokelog.KindCut:
		field("kind", "retention cut")
		if e.Cut != nil {
			field("through", fmt.Sprintf("#%d, removed on purpose by logs.retention_days", e.Cut.ThroughSeq))
		}
	default:
		field("way", row.values[logFilterWay])
		client := "-"
		if e.Client != nil {
			client = strings.TrimSpace(m.logText(e.Client.Name) + " " + m.logText(e.Client.Version))
		}
		field("client", orDash(client))
		tool := row.values[logFilterTool]
		if e.Version > 0 {
			tool += fmt.Sprintf(" (version %d)", e.Version)
		}
		field("tool", tool)
		field("connection", row.values[logFilterConnection])
		field("effect", row.values[logFilterEffect])
		field("result", orDash(m.logText(e.Result)))
		field("duration", formatDuration(e.DurationMS))
	}
	b.WriteString(m.hint("esc back · 1-8 section"))
	return b.String()
}

// logReason says why a line has its status.
func (m *Model) logReason(line invokelog.LineResult) string {
	var reasons []string
	switch line.Chain {
	case invokelog.ChainUnparsed:
		reasons = append(reasons, "the line does not parse as a log entry")
	case invokelog.ChainGenesis:
		reasons = append(reasons, "the first entry does not chain from the genesis hash")
	case invokelog.ChainNext:
		reasons = append(reasons, "hash chain mismatch after this entry")
	case invokelog.ChainPrevious:
		reasons = append(reasons, "hash chain mismatch before this entry")
	}
	if line.Missing > 0 {
		reasons = append(reasons, fmt.Sprintf("%d entries missing right before this one without a retention cut",
			line.Missing))
	}
	if line.Parsed {
		switch line.MAC {
		case invokelog.MACInvalid:
			reasons = append(reasons, "does not match its check value")
		case invokelog.MACValid:
			if len(reasons) == 0 {
				reasons = append(reasons, "check value matches, chain intact")
			}
		case invokelog.MACUnchecked:
			note := m.logs.note
			if note == "" {
				note = "check value not checked"
			}
			reasons = append(reasons, note)
		case invokelog.MACNone:
			reasons = append(reasons, "written without the log key, so it carries no check value")
		}
	}
	return strings.Join(reasons, "; ")
}

// logDateDialogView draws the dialog of the bar's date field.
func (m *Model) logDateDialogView() string {
	d := m.logs.dialog
	title, labels := "Choose a local day", []string{"date"}
	if len(d.inputs) == 2 {
		title, labels = "Choose a range of local days", []string{"from", "to"}
	}
	var b strings.Builder
	b.WriteString(m.wrapped(titleStyle, title) + "\n\n")
	for i, in := range d.inputs {
		b.WriteString(m.formRow(i == d.focus, labels[i], in.View()) + "\n")
	}
	b.WriteString(m.indented("YYYY-MM-DD in the system time zone") + "\n")
	if d.err != "" {
		b.WriteString(m.indentedWith(failStyle, "error: "+d.err) + "\n")
	}
	keys := "enter apply · esc cancel"
	if len(d.inputs) == 2 {
		keys = "enter apply · tab next field · esc cancel"
	}
	b.WriteString(m.keyHint(keys))
	return b.String()
}

// logPickView draws the filter dialog in the picker pattern: its frame, and as many values as fit.
func (m *Model) logPickView() string {
	head, foot := m.logPickFrame()
	var b strings.Builder
	b.WriteString(head)
	start, end := m.logPickWindow()
	for i := start; i < end; i++ {
		b.WriteString(m.logPickRow(i) + "\n")
	}
	b.WriteString(foot)
	return b.String()
}

func (m *Model) logPickFrame() (string, string) {
	p := m.logs.pick
	shown, total := len(p.list.matches), len(p.list.all)
	title := "Choose a filter"
	if p.filter >= 0 {
		title = "Filter by " + logFilterNames[p.filter]
	}
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, fmt.Sprintf("%s  %d/%d (%d total)", title,
		min(p.list.cursor+1, shown), shown, total)) + "\n")
	head.WriteString(m.searchLine(&p.list, "search: ") + "\n")
	if p.filter >= 0 {
		head.WriteString(m.wrapped(hintStyle, "current: "+filterText(m.logs.filters[p.filter])) + "\n")
	}
	if shown == 0 {
		head.WriteString(m.wrapped(hintStyle, fmt.Sprintf("Nothing matches %q.", p.list.query())) + "\n")
	}
	keys := "type to search · up/down move · enter choose · esc close"
	if p.filter >= 0 {
		keys = "type to search · up/down move · enter keep this value · esc back to the filters"
	}
	return head.String(), m.keyHint(keys) + m.notes()
}

func (m *Model) logPickRow(i int) string {
	p := m.logs.pick
	_, width := m.fit("  ")
	name := p.list.matches[i]
	text := p.list.text(name)
	if p.filter >= 0 && name == m.logs.filters[p.filter] {
		text += "  (current)"
	}
	return m.listRow(i == p.list.cursor, i%2 == 1, truncateCells(text, width))
}

func (m *Model) logPickWindow() (int, int) {
	head, foot := m.logPickFrame()
	return m.windowIn(&m.logs.pick.list, head, foot, m.logPickRow)
}

// keepLogsScrollPosition is keepScrollPosition for the Logs screen and the filter dialog over it.
func (m *Model) keepLogsScrollPosition() {
	lv := m.logs
	switch {
	case m.screen == screenLogs && lv.pick != nil:
		lv.pick.list.offset, _ = m.logPickWindow()
	case m.screen == screenLogs && lv.dialog != nil:
	default:
		lv.list.offset, _ = m.logsWindow()
	}
}
