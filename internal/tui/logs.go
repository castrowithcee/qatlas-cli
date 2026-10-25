// The Logs section (the Activity area, see the sectionLogs comment in tui.go): the invocation log, read only. A bar on
// top chooses a day, a range of days, or every recorded day; f narrows the rows by way, MCP client, tool,
// connection, effect, and result in a picker; / searches the text of the rows; s swaps the rows for the
// response sizes summed up per tool and connection, m for the context cost of each MCP session; enter opens
// one entry; the View field of the bar swaps the rows for a heatmap of their days (activity.go). Every
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
	"github.com/charmbracelet/x/ansi"

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

func (mode logMode) String() string {
	return tr([...]textID{tLogModeDay, tLogModeRange, tLogModeAll}[mode])
}

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
	return tr([...]textID{tLogVerified, tLogUnverified, tLogGap, tLogAltered}[s])
}

func (s logStatus) style() lipgloss.Style {
	return [...]lipgloss.Style{styles.ok, styles.warning, styles.warning, styles.fail}[s]
}

// logFocus is the part of the Logs screen the keys act on: the rows or one of the bar's two fields.
type logFocus int

const (
	logFocusRows logFocus = iota
	logFocusMode
	logFocusDate
	logFocusView
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
	logFilterNames = [logFilterCount]textID{tLogFilterWay, tLogFilterClient, tLogFilterTool, tLogFilterConnection,
		tLogFilterEffect, tLogFilterResult}
	logFilterShort = [logFilterCount]textID{tLogFilterShortWay, tLogFilterShortClient, tLogFilterShortTool,
		tLogFilterShortConn, tLogFilterShortEffect, tLogFilterShortResult}
)

var logLabels = map[string]textID{
	"status":   tLogLabelStatus,
	"UTC file": tLogLabelUTCFile,
	"sequence": tLogLabelSequence,
	"day":      tLogLabelDay,
	"kind":     tLogLabelKind,
	"through":  tLogLabelThrough,
	"way":      tLogLabelWay,
	"client":   tLogLabelClient,
	"tool":     tLogLabelTool,
	"effect":   tLogLabelEffect,
	"result":   tLogLabelResult,
	"duration": tLogLabelDuration,
	"request":  tLogLabelRequest,
	"response": tLogLabelResponse,
	"date":     tLogLabelDate,
	"from":     tLogLabelFrom,
	"to":       tLogLabelTo,
}

// logLabel is a row label of the entry detail or the date dialog in the editor's language.
func logLabel(english string) string {
	if id, ok := logLabels[english]; ok {
		return tr(id)
	}
	return labelText(english)
}

// logColumnTitles draws the column titles of a table of the Logs section in the editor's language.
func logColumnTitles(columns []column) []column {
	out := make([]column, len(columns))
	for i, c := range columns {
		c.title = columnText(c.title)
		out[i] = c
	}
	return out
}

// logColumns are the columns of the log table. The status leads and is never cut; the tool is the free
// text; the duration goes first in a narrow terminal, then the connection, then the client.
var logColumns = []column{
	{title: "status"}, {title: "time"}, {title: "way"}, {title: "client", optional: true},
	{title: "tool", flex: true}, {title: "connection", optional: true}, {title: "result"},
	{title: "dur.", optional: true}, {title: "resp.", optional: true},
}

// logStatColumns are the columns of the summary s shows. The count leads and is never cut; the sizes are
// token estimates (invokelog.EstimateTokens), and the bytes and the missing counters go in a narrow terminal.
var logStatColumns = []column{
	{title: "calls"}, {title: "tool", flex: true}, {title: "connection", optional: true},
	{title: "~tok"}, {title: "max"}, {title: "p50", optional: true}, {title: "p95"},
	{title: "bytes", optional: true}, {title: "no size", optional: true},
}

// logSessionColumns are the columns of the MCP session summary. The first column holds the session, its
// areas, and their tools as indented text; the byte split of a request and a response goes in a narrow
// terminal before the total and the token estimate do.
var logSessionColumns = []column{
	{title: "session / area / tool"}, {title: "calls"}, {title: "req B", optional: true},
	{title: "resp B", optional: true}, {title: "total B"}, {title: "~tokens"},
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

	// stats shows the summary of the shown rows instead of the rows themselves, heat the heatmap of their days.
	stats bool
	heat  bool
	// sessions makes the summary the one of the MCP sessions, scrolled down by scroll lines.
	sessions bool
	scroll   int
	dialog   *logDateDialog
	pick     *logPick
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

func newLogView(now time.Time) *logView {
	today := now.In(time.Local)
	lv := &logView{
		day:   today.Format(logDateLayout),
		from:  today.AddDate(0, 0, -6).Format(logDateLayout),
		to:    today.Format(logDateLayout),
		table: table{columns: logColumns, rows: map[string][]string{}},
	}
	lv.list = newFilterList(lv.searchText)
	lv.list.input.Placeholder = tr(tLogSearchPlaceholder)
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
			note = tr(tLogCheckFailed, err.Error())
			days, err = invokelog.VerifyLines(v.Dir(), nil, utcFrom, utcTo)
		}
		return logsLoadedMsg{id: id, from: from, to: to, days: days, note: note, err: err}
	}
}

// logUTCFileBounds selects every UTC day file that can overlap the chosen local days.
func logUTCFileBounds(from, to string) (string, string) { return invokelog.UTCFileBounds(from, to) }

// logChecker returns what checks the log's check values of v, the same way 'qatlas vault logs verify' does:
// the key of a vault unlocked in this process; else a running vault process, which only ever answers
// whether a check value matches; else nothing, since the editor never asks for the passphrase for this.
// note says why there is no checker, and done clears a key this derived.
func logChecker(ctx context.Context, v *vault.Vault) (invokelog.Checker, string, func()) {
	nothing := func() {}
	locked := tr(tLogLocked)
	state, err := v.State()
	switch {
	case err != nil:
		return nil, tr(tLogStateUnreadable), nothing
	case state == vault.StateUnlocked:
		key, err := v.LogKey()
		if err != nil {
			return nil, locked, nothing
		}
		return key, "", key.Clear
	case state != vault.StateLocked:
		return nil, tr(tLogNotEncrypted), nothing
	case !vaultproc.Supported:
		return nil, locked, nothing
	}
	client, err := vaultproc.ProcessClientOf(v)
	if err != nil {
		return nil, locked, nothing
	}
	if _, err := client.Status(ctx); err != nil {
		if !errors.Is(err, vaultproc.ErrNotRunning) {
			return nil, tr(tLogProcessCannot, err.Error()), nothing
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
	lv.table = table{columns: logColumnTitles(logColumns), rows: map[string][]string{}}
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
	when, way, client, tool, conn, effect, result, dur, size := tr(tLogUTCFile, date), "-", "-", "-", "-", "-", "-", "", "-"
	switch {
	case !line.Parsed:
		tool = tr(tLogUnreadable)
	case e.Kind == invokelog.KindCut:
		if !e.Time.IsZero() {
			when = logTableTime(e.Time, withDate)
		}
		tool = tr(tLogRetentionCut)
		if e.Cut != nil {
			tool = tr(tLogRetentionCutThrough, e.Cut.ThroughSeq)
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
		if e.RespBytes != nil {
			size = estimateTokens(*e.RespBytes)
		}
	}
	row.cells = []string{row.status.label(), when, way, client, tool, conn, result, dur, size}
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

// estimateTokens approximates the tokens of a payload of n bytes at 3.5 bytes per token.
func estimateTokens(n int64) string {
	return fmt.Sprintf("~%d tok", n*2/7)
}

// formatBytes shows a byte counter with its token estimate; a nil counter was not recorded.
func formatBytes(n *int64) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%d B (%s)", *n, estimateTokens(*n))
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
	lv.focus, lv.dialog, lv.pick, lv.stats, lv.sessions, lv.heat = logFocusRows, nil, nil, false, false, false
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
	case keyOf(actBack):
		if lv.stats {
			lv.stats, lv.sessions = false, false
			return nil
		}
		if lv.list.query() != "" {
			lv.list.clearFilter()
			return nil
		}
		m.focusNav()
	case "tab":
		lv.moveFocus(1)
	case "shift+tab":
		lv.moveFocus(-1)
	case "left", "right":
		by := 1
		if key.String() == "left" {
			by = -1
		}
		switch lv.focus {
		case logFocusMode:
			lv.mode = logMode(wrap(int(lv.mode)+by, int(logModeCount)))
			return m.loadLogs()
		case logFocusDate:
			m.openLogDateDialog()
		case logFocusView:
			return m.setHeat(!lv.heat)
		default:
			if by < 0 {
				m.focusNav()
			}
		}
	case "s":
		lv.stats, lv.sessions, lv.heat = !lv.stats, false, false
	case "m":
		on := !(lv.stats && lv.sessions)
		lv.stats, lv.sessions, lv.scroll, lv.heat = on, on, 0, false
	case "enter":
		switch lv.focus {
		case logFocusDate:
			m.openLogDateDialog()
		case logFocusMode, logFocusView:
			m.openLogBarDropdown(lv.focus)
		case logFocusRows:
			if row, ok := lv.list.selected(); ok && !lv.stats && !lv.heat {
				lv.detail, _ = strconv.Atoi(row)
				m.screen = screenLogDetail
				m.clearMessages()
			}
		}
	case "up", "down", "pgup", "pgdown", "home", "end":
		lv.focus = logFocusRows
		if lv.heat {
			break
		}
		if lv.stats && lv.sessions {
			m.scrollSessions(key.String())
			break
		}
		switch key.String() {
		case "up":
			lv.list.move(-1)
		case "down":
			lv.list.move(1)
		default:
			start, end := m.logsWindow()
			lv.list.page(key.String(), start, end)
		}
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
	order := []logFocus{logFocusMode, logFocusDate, logFocusView, logFocusRows}
	if lv.mode == logModeAll {
		order = []logFocus{logFocusMode, logFocusView, logFocusRows}
	}
	at := len(order) - 1
	for i, f := range order {
		if f == lv.focus {
			at = i
		}
	}
	lv.focus = order[wrap(at+by, len(order))]
}

// openLogBarDropdown opens the list of the bar's mode or view field, which takes the focus.
func (m *Model) openLogBarDropdown(f logFocus) {
	lv := m.logs
	lv.focus = f
	if f == logFocusMode {
		var modes []string
		for mode := logModeDay; mode < logModeCount; mode++ {
			modes = append(modes, mode.String())
		}
		m.openDropdown("logmode", modes, int(lv.mode), func(i int) tea.Cmd {
			lv.mode = logMode(i)
			return m.loadLogs()
		})
		return
	}
	active := 0
	if lv.heat {
		active = 1
	}
	m.openDropdown("logview", []string{tr(tLogViewTable), tr(tLogViewHeatmap)}, active, func(i int) tea.Cmd {
		return m.setHeat(i == 1)
	})
}

// updateLogSearch is the Logs screen while its search is typed; every printable key is search text.
func (m *Model) updateLogSearch(key tea.KeyMsg) tea.Cmd {
	lv := m.logs
	switch key.String() {
	case keyOf(actBack):
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
	case keyOf(actBack):
		m.screen = screenLogs
	}
	return nil
}

func newLogInput(value string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = len(logDateLayout)
	in.Placeholder = tr(tLogDateFormat)
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
	case keyOf(actBack):
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
				d.err = tr(tLogDateInvalid, time.Now().In(time.Local).Format(logDateLayout))
				return nil
			}
			days = append(days, value)
		}
		if len(days) == 2 && days[0] > days[1] {
			d.err = tr(tLogDateOrder)
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
			return fmt.Sprintf("%-11s %s", tr(logFilterNames[k]), filterText(lv.filters[k]))
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
	p.list.input.Placeholder = tr(tLogSearchPlaceholder)
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
		return tr(tLogAll)
	}
	return value
}

func (m *Model) updateLogPick(key tea.KeyMsg) tea.Cmd {
	lv, p := m.logs, m.logs.pick
	switch key.String() {
	case keyOf(actBack):
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
// what the workspace shows below the tab line while the section is only marked there.
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
	m.hitBar(strings.Split(head, "\n"), m.workspaceTop(), m.logBarSpecs)
	if lv.stats && lv.loaded && lv.err == "" && len(lv.list.matches) > 0 {
		room := m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1
		if lv.sessions {
			b.WriteString(m.sessionStatLines(m.logSessionStats(), room))
		} else {
			b.WriteString(m.logStatLines(m.logStats(), room))
		}
		b.WriteString(foot)
		return b.String()
	}
	if lv.heat && lv.loaded && lv.err == "" && len(lv.list.matches) > 0 {
		b.WriteString(m.logHeatLines(m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1))
		b.WriteString(foot)
		return b.String()
	}
	start, end := m.logsWindow()
	l := m.logLayout()
	for i := start; i < end; i++ {
		b.WriteString(m.logRowLine(i, l) + "\n")
	}
	m.hitLogs(head, start, end)
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
// the rows have the focus; on the other rows the time is faint.
func (m *Model) logRowLine(i int, l layout) string {
	lv := m.logs
	row := lv.row(lv.list.matches[i])
	active := m.screen == screenLogs && lv.focus == logFocusRows && i == lv.list.cursor
	if active {
		return m.listRow(true, lv.table.line(row.cells, l))
	}
	blank, width := m.fit("  ")
	line := padLine(lv.table.line(row.cells, l), width)
	head := clipCells(line, l.widths[0]+lipgloss.Width(columnGap))
	rest := strings.TrimPrefix(line, head)
	when := clipCells(rest, l.widths[1])
	tail := strings.TrimPrefix(rest, when)
	return blank + head + styles.hint.Render(when) + tail
}

// logsFrame is everything of the Logs screen except its rows. The bar stands on one line where it fits and
// field by field where it does not; a terminal too low for the rows drops the rules and the idle search line
// first.
func (m *Model) logsFrame() (string, string) {
	lv := m.logs
	focused := m.screen == screenLogs
	width := m.usable(0)
	status := styles.hint.Render(tr(tLogLoading))
	if lv.loaded {
		status = lv.worst.style().Render(lv.worst.label())
	}
	marker := func(f logFocus) string { return barMarker(focused && lv.focus == f) }
	barLabel := func(id textID) string { return strings.TrimRight(tr(id), ": ") }
	var modes []string
	for mode := logModeDay; mode < logModeCount; mode++ {
		modes = append(modes, mode.String())
	}
	field := func(f logFocus, label, value string) (string, string) {
		text := dropdownField(label, value, focused && lv.focus == f, width-2)
		return marker(f) + text, ansi.Strip(text)
	}
	modeField, modePlain := field(logFocusMode, barLabel(tLogBarMode), modes[lv.mode])
	view := 0
	if lv.heat {
		view = 1
	}
	views := []string{tr(tLogViewTable), tr(tLogViewHeatmap)}
	viewField, viewPlain := field(logFocusView, barLabel(tLogBarView), views[view])
	var dateField, datePlain string
	switch lv.mode {
	case logModeDay, logModeRange:
		text := tr(tLogBarDate, lv.day)
		if lv.mode == logModeRange {
			text = tr(tLogBarRange, lv.from, lv.to)
		}
		label, value, _ := strings.Cut(text, ": ")
		dateField, datePlain = field(logFocusDate, label, strings.Trim(value, "[]"))
	default:
		dateField = "  " + styles.hint.Render(tr(tLogBarAll))
	}
	bar := packBar([]string{modeField, dateField, viewField}, status, width)

	var filterFields, filterPlain []string
	for k, value := range lv.filters {
		label := []rune(tr(logFilterShort[k]))
		text := dropdownField(strings.ToUpper(string(label[:1]))+string(label[1:]), filterText(value), false, min(width-2, 24))
		filterFields = append(filterFields, text)
		filterPlain = append(filterPlain, ansi.Strip(text))
	}
	filterRows, searchPlain := fieldRows(filterFields, func(room int) string { return searchContent(&lv.list, room) }, width)
	// A compact frame keeps one line of the filter line: the search while it is typed, else the first fields.
	compactRows := filterRows[:1]
	if lv.list.editing || lv.list.query() != "" {
		compactRows = []string{searchField(searchContent(&lv.list, width-2), width-2)}
	}
	m.logBarSpecs = m.logBarTargets(modePlain, datePlain, viewPlain, filterPlain, searchPlain)
	rule := styles.quietBorder.Render(strings.Repeat("─", width))

	var body []string
	shown := len(lv.list.matches)
	switch {
	case lv.err != "":
		body = append(body, m.wrapped(styles.fail, tr(tLogError, lv.err)))
	case !lv.loaded:
		body = append(body, m.wrapped(styles.hint, tr(tLogReading)))
	case len(lv.rows) == 0:
		body = append(body, m.wrapped(lipgloss.NewStyle(), lv.emptyText()))
	case shown == 0:
		body = append(body, m.wrapped(styles.hint, tr(tLogNoMatch)))
	case lv.heat:
		// The heatmap has no table header.
	case lv.stats && lv.sessions:
		body = append(body, m.wrapped(styles.hint, tr(tLogSessionsHelp)))
		if stats := m.logSessionStats(); len(stats.order) > 0 {
			body = append(body, styles.hint.Render(clipCells("  "+stats.table.header(stats.layout), width)))
		}
	case lv.stats:
		stats := m.logStats()
		body = append(body, m.wrapped(styles.hint, tr(tLogStatsHelp)))
		body = append(body, styles.hint.Render(clipCells("  "+stats.table.header(stats.layout), width)))
	default:
		body = append(body, styles.hint.Render(clipCells("  "+lv.table.header(m.logLayout()), width)))
	}

	keys := m.keyHint(m.logKeys())

	// Every part ends its line itself, so the rows stand right above the foot; a compact frame leaves out
	// the rules, the idle search line, the position, and the note, which the detail repeats.
	build := func(compact bool) (string, string) {
		lines := append([]string{}, bar...)
		if compact {
			lines = append(lines, compactRows...)
		} else {
			lines = append(lines, filterRows...)
		}
		if !compact {
			lines = append(lines, rule)
		}
		lines = append(lines, body...)
		if compact {
			return strings.Join(lines, "\n") + "\n", strings.TrimPrefix(keys, "\n") + m.notes()
		}
		foot := keys
		if shown > 0 && focused && !lv.stats && !lv.heat {
			m.screenPos = fmt.Sprintf("%d/%d · %d%%", lv.list.cursor+1, shown, (lv.list.cursor+1)*100/shown)
		}
		if lv.note != "" && lv.loaded && lv.hasUnchecked() {
			foot = "\n" + m.wrapped(styles.hint, lv.note) + foot
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
		return tr(tLogEmptyDay, lv.day)
	case logModeRange:
		return tr(tLogEmptyRange, lv.from, lv.to)
	}
	return tr(tLogEmptyAll)
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
		// The short form names the same keys with shorter words.
		switch {
		case lv.stats && lv.sessions && !lv.list.editing:
			return "up/down scroll · m or s rows · f filter · / search · 1-8 section · {command} cmds · {help} help · q quit"
		case lv.stats && !lv.list.editing:
			return "s rows · f filter · / search · 1-8 section · {command} cmds · {help} help · q quit"
		case lv.list.editing:
			return "type to search · up/down move · enter keep · {back} clear"
		case lv.focus == logFocusMode:
			return "left/right mode · enter open · tab next · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
		case lv.focus == logFocusDate:
			return "enter date · tab next · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
		case lv.focus == logFocusView:
			return "left/right view · enter open · tab next · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
		case len(lv.list.matches) == 0 || lv.heat:
			return "tab next · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
		}
		return "enter detail · tab next · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	}
	if lv.stats && lv.sessions && m.screen != screenNav && !lv.list.editing {
		return "up/down scroll · m or s or {back} back to the rows · f filter · / search · 1-8 section · {command} cmds · {help} help · q quit"
	}
	if lv.stats && m.screen != screenNav && !lv.list.editing {
		return "s or {back} back to the rows · f filter · / search · 1-8 section · {command} cmds · {help} help · q quit"
	}
	switch {
	case m.screen == screenNav:
		return m.navKeys()
	case lv.list.editing:
		return "type to search · up/down move · enter keep search · {back} clear search"
	case lv.focus == logFocusMode:
		return "left/right change mode · enter open · tab next field · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	case lv.focus == logFocusDate:
		return "enter or left/right change the date · tab next field · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	case lv.focus == logFocusView:
		return "left/right change view · enter open · tab next field · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	case len(lv.list.matches) == 0 || lv.heat:
		return "tab next field · left/right change · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	}
	keys := "enter details · tab next field · left/right change · f filter · s summary · m MCP sessions · / search · 1-8 section · {back} back · {command} cmds · {help} help · q quit"
	if lv.list.query() != "" {
		keys = strings.Replace(keys, "{back} back", "{back} clear search", 1)
	}
	return keys
}

// logStats is the summary of the rows the filters and the search keep: the table with a row per group and
// its layout for the workspace.
type logStats struct {
	table  table
	layout layout
	groups []invokelog.SizeGroup
}

func (m *Model) logStats() logStats {
	lv := m.logs
	entries := make([]invokelog.Entry, 0, len(lv.list.matches))
	for _, name := range lv.list.matches {
		if row := lv.row(name); row != nil && row.line.Parsed {
			entries = append(entries, row.line.Entry)
		}
	}
	s := logStats{groups: invokelog.Summarize(entries), table: table{columns: logColumnTitles(logStatColumns), rows: map[string][]string{}}}
	num := func(n int64) string { return strconv.FormatInt(n, 10) }
	for i, g := range s.groups {
		cells := []string{strconv.Itoa(g.Calls), m.logText(g.Operation), orDash(m.logText(g.Connection)),
			num(invokelog.EstimateTokens(g.Bytes)), num(invokelog.EstimateTokens(g.Max)),
			num(invokelog.EstimateTokens(g.P50)), num(invokelog.EstimateTokens(g.P95)), num(g.Bytes),
			strconv.Itoa(g.NoCounter)}
		if g.Counted() == 0 {
			// No invoke of the group has a recorded size; that is not a size of 0.
			for k := 3; k <= 7; k++ {
				cells[k] = "-"
			}
		}
		s.table.rows[strconv.Itoa(i)] = cells
	}
	_, width := m.fit("  ")
	s.layout = s.table.layout(width)
	return s
}

// logStatLines draws the groups of s as many lines as fit in room; a last line says how many it left out.
func (m *Model) logStatLines(s logStats, room int) string {
	if len(s.groups) > room {
		room = max(room-1, 0)
	}
	var b strings.Builder
	for i := range s.groups {
		if i == room {
			b.WriteString(styles.hint.Render(tr(tLogMore, len(s.groups)-i)) + "\n")
			break
		}
		b.WriteString(m.listRow(false, s.table.line(s.table.rows[strconv.Itoa(i)], s.layout)) + "\n")
	}
	return b.String()
}

// logSessionStats is the table of the MCP sessions among the rows the filters and the search keep: a line
// for each session, its areas, and their tools, as the cells of the table, in the order of order.
type logSessionStats struct {
	table  table
	layout layout
	order  []string
}

func (m *Model) logSessionStats() logSessionStats {
	lv := m.logs
	entries := make([]invokelog.Entry, 0, len(lv.list.matches))
	for _, name := range lv.list.matches {
		if row := lv.row(name); row != nil && row.line.Parsed {
			entries = append(entries, row.line.Entry)
		}
	}
	s := logSessionStats{table: table{columns: logColumnTitles(logSessionColumns), rows: map[string][]string{}}}
	add := func(label string, t invokelog.ToolBytes) {
		name := strconv.Itoa(len(s.order))
		s.order = append(s.order, name)
		s.table.rows[name] = []string{label, strconv.Itoa(t.Calls), strconv.FormatInt(t.ReqBytes, 10),
			strconv.FormatInt(t.RespBytes, 10), strconv.FormatInt(t.Total(), 10),
			strconv.FormatInt(invokelog.EstimateTokens(t.Total()), 10)}
	}
	for _, session := range invokelog.SummarizeSessions(entries) {
		var sum invokelog.ToolBytes
		for _, a := range session.Areas {
			sum.Calls += a.Calls
			sum.ReqBytes += a.ReqBytes
			sum.RespBytes += a.RespBytes
		}
		add(tr(tLogSessionRow, m.logText(session.Session)), sum)
		for _, a := range session.Areas {
			add("  "+string(a.Area), a.ToolBytes)
			for _, t := range a.Tools {
				add("    "+orDash(m.logText(t.Operation)), t)
			}
		}
	}
	_, width := m.fit("  ")
	s.layout = s.table.layout(width)
	return s
}

// sessionRoom is how many lines the session summary has room for between the frame of the Logs screen.
func (m *Model) sessionRoom() int {
	head, foot := m.logsFrame()
	return m.height - strings.Count(head, "\n") - strings.Count(foot, "\n") - 1
}

// scrollSessions moves the shown part of the session summary; the view clamps it again for its own room.
func (m *Model) scrollSessions(key string) {
	lv := m.logs
	room := max(m.sessionRoom()-1, 1)
	last := max(len(m.logSessionStats().order)-room, 0)
	by := map[string]int{"up": -1, "down": 1, "pgup": -room, "pgdown": room}[key]
	switch key {
	case "home":
		lv.scroll = 0
	case "end":
		lv.scroll = last
	default:
		lv.scroll = min(max(lv.scroll+by, 0), last)
	}
}

// sessionStatLines draws the lines of s from the scroll position as many as fit in room, or says that the
// rows shown hold no MCP session; a last line says how many lines it left below.
func (m *Model) sessionStatLines(s logSessionStats, room int) string {
	if len(s.order) == 0 {
		return m.wrapped(lipgloss.NewStyle(), tr(tLogNoSession)) + "\n"
	}
	start := min(max(m.logs.scroll, 0), max(len(s.order)-1, 0))
	if len(s.order)-start > room {
		room = max(room-1, 0)
	}
	var b strings.Builder
	for i := start; i < len(s.order); i++ {
		if i-start == room {
			b.WriteString(styles.hint.Render(tr(tLogMore, len(s.order)-i)) + "\n")
			break
		}
		b.WriteString(m.listRow(false, s.table.line(s.table.rows[s.order[i]], s.layout)) + "\n")
	}
	return b.String()
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
		return m.keyHint("{back} back · {command} cmds · {help} help")
	}
	row := lv.rows[lv.detail]
	e := row.line.Entry
	title := tr(tLogEntryFile, row.date)
	if row.line.Parsed && !e.Time.IsZero() {
		title = tr(tLogEntryTime, e.Time.In(time.Local).Format(logDateLayout+" 15:04:05 -0700"))
	}
	var b strings.Builder
	b.WriteString(m.wrapped(styles.title, title) + "\n\n")
	m.formCol = m.labelColumn(logLabel("status"), logLabel("UTC file"), logLabel("sequence"), logLabel("day"), logLabel("kind"),
		logLabel("through"), logLabel("way"), logLabel("client"), logLabel("tool"), logLabel("connection"),
		logLabel("effect"), logLabel("result"), logLabel("duration"), logLabel("request"), logLabel("response"))
	defer func() { m.formCol = 0 }()
	field := func(label, value string) { b.WriteString(m.formRow(false, logLabel(label), value) + "\n") }
	field("status", row.status.label()+" ("+m.logReason(row.line)+")")
	field("UTC file", row.date)
	if row.line.Parsed {
		field("sequence", strconv.FormatUint(e.Seq, 10))
	}
	switch {
	case !row.line.Parsed:
		field("day", row.date)
	case e.Kind == invokelog.KindCut:
		field("kind", tr(tLogRetentionCut))
		if e.Cut != nil {
			field("through", tr(tLogCutThrough, e.Cut.ThroughSeq))
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
			tool += tr(tLogToolVersion, e.Version)
		}
		field("tool", tool)
		field("connection", row.values[logFilterConnection])
		field("effect", row.values[logFilterEffect])
		field("result", orDash(m.logText(e.Result)))
		field("duration", formatDuration(e.DurationMS))
		field("request", formatBytes(e.ReqBytes))
		field("response", formatBytes(e.RespBytes))
	}
	b.WriteString(m.keyHint("{back} back · 1-8 section · {command} cmds · {help} help"))
	return b.String()
}

// logReason says why a line has its status.
func (m *Model) logReason(line invokelog.LineResult) string {
	var reasons []string
	switch line.Chain {
	case invokelog.ChainUnparsed:
		reasons = append(reasons, tr(tLogReasonUnparsed))
	case invokelog.ChainGenesis:
		reasons = append(reasons, tr(tLogReasonGenesis))
	case invokelog.ChainNext:
		reasons = append(reasons, tr(tLogReasonNext))
	case invokelog.ChainPrevious:
		reasons = append(reasons, tr(tLogReasonPrevious))
	}
	if line.Missing > 0 {
		reasons = append(reasons, tr(tLogReasonMissing, line.Missing))
	}
	if line.Parsed {
		switch line.MAC {
		case invokelog.MACInvalid:
			reasons = append(reasons, tr(tLogReasonInvalid))
		case invokelog.MACValid:
			if len(reasons) == 0 {
				reasons = append(reasons, tr(tLogReasonValid))
			}
		case invokelog.MACUnchecked:
			note := m.logs.note
			if note == "" {
				note = tr(tLogReasonUnchecked)
			}
			reasons = append(reasons, note)
		case invokelog.MACNone:
			reasons = append(reasons, tr(tLogReasonNoKey))
		}
	}
	return strings.Join(reasons, "; ")
}

// logDateDialogView draws the dialog of the bar's date field.
func (m *Model) logDateDialogView() string {
	d := m.logs.dialog
	title, labels := tr(tLogDayTitle), []string{"date"}
	if len(d.inputs) == 2 {
		title, labels = tr(tLogRangeTitle), []string{"from", "to"}
	}
	var b strings.Builder
	b.WriteString(m.wrapped(styles.title, title) + "\n\n")
	m.formCol = m.labelColumn(labels...)
	defer func() { m.formCol = 0 }()
	for i, in := range d.inputs {
		b.WriteString(m.formRow(i == d.focus, logLabel(labels[i]), in.View()) + "\n")
	}
	b.WriteString(m.indented(tr(tLogDateHint)) + "\n")
	if d.err != "" {
		b.WriteString(m.indentedWith(styles.fail, tr(tNoteError, d.err)) + "\n")
	}
	keys := "enter apply · {back} cancel"
	if len(d.inputs) == 2 {
		keys = "enter apply · tab next field · {back} cancel"
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
	title := tr(tLogPickChoose)
	if p.filter >= 0 {
		title = tr(tLogPickBy, tr(logFilterNames[p.filter]))
	}
	var head strings.Builder
	head.WriteString(m.wrapped(styles.title, tr(tLogPickCount, title,
		min(p.list.cursor+1, shown), shown, total)) + "\n")
	head.WriteString(m.searchLine(&p.list, tr(tLogPickSearch)) + "\n")
	if p.filter >= 0 {
		head.WriteString(m.wrapped(styles.hint, tr(tLogPickCurrent, filterText(m.logs.filters[p.filter]))) + "\n")
	}
	if shown == 0 {
		head.WriteString(m.wrapped(styles.hint, tr(tLogPickNone, p.list.query())) + "\n")
	}
	keys := "type to search · up/down move · enter choose · {back} close"
	if p.filter >= 0 {
		keys = "type to search · up/down move · enter keep this value · {back} back to the filters"
	}
	return head.String(), m.keyHint(keys) + m.notes()
}

func (m *Model) logPickRow(i int) string {
	p := m.logs.pick
	_, width := m.fit("  ")
	name := p.list.matches[i]
	text := p.list.text(name)
	if p.filter >= 0 && name == m.logs.filters[p.filter] {
		text += tr(tLogPickCurrentMark)
	}
	return m.listRow(i == p.list.cursor, truncateCells(text, width))
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
