package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// The target picker lets a person choose targets of a saved connection from the list its provider offers,
// instead of looking an ID up. It opens from the add menu of the target list and works like a file dialog:
// a search line above a table of the entries of one level, right opens the entry under the marker to the
// level below, left goes back up. Every entry of every level is a target of its own kind, so a higher level
// takes a whole area. What is picked is taken like a typed target (takeTarget), so the provider's check
// still decides. The provider's names and paths are untrusted text and are shown without control characters.
//
// The core reads one page at a time (manage.Service.ListTargets); the picker reads off the event loop and
// asks for the next page when the marker reaches the end. Where the provider searches itself, the search
// text goes to it after a short pause; otherwise the loaded entries are filtered here.

// addPick is the row key prefix of the add menu that opens the picker on one top-level kind.
const addPick = "pick:"

// pickDelay is how long the search text rests before a searching provider is asked.
var pickDelay = 300 * time.Millisecond

// maxLabelRunes is the longest target label the configuration accepts.
const maxLabelRunes = 200

// pickLoadedMsg carries one page of a level back into the event loop. replace says that it starts the
// level's entries over (first page, or another search text).
type pickLoadedMsg struct {
	req     int
	replace bool
	page    config.TargetPage
	err     error
}

// pickSearchMsg is the pause after the last key of a search text of a level.
type pickSearchMsg struct{ level, seq int }

type pickEntry struct {
	entry config.TargetEntry
	// cells are the name, kind, ID and path as shown, already without control characters.
	cells []string
}

// pickLevel is one level of the picker: the entries of one kind under one parent.
type pickLevel struct {
	id int
	// kinds are the kinds that share this level's parent, ki the one shown.
	kinds  []string
	ki     int
	parent string
	// label is the parent as shown; empty on the top level.
	label   string
	entries map[string]*pickEntry
	list    filterList
	table   table
	// laid is the layout of table for laidWidth cells, valid while laidRows entries are in the table.
	laid      layout
	laidWidth int
	laidRows  int
	next      string
	// server says that the provider applies the search text itself; sent is the text its entries are for.
	server bool
	sent   string
	// req identifies the read in flight; a late answer of any other read is dropped.
	req     int
	loading bool
	cancel  context.CancelFunc
	err     string
	seq     int
}

func (lv *pickLevel) kind() string { return lv.kinds[lv.ki] }

// targetPick is the picker while it is open, over the add menu that opened it.
type targetPick struct {
	connection string
	levels     []*pickLevel
	nextID     int
	marks      map[string]config.TargetEntry
	markOrder  []string
}

func (p *targetPick) top() *pickLevel { return p.levels[len(p.levels)-1] }

// pickConnection is the saved connection the picker would read: the one being edited, as long as the form
// still names the service and credential it was saved with, since the core reads the saved connection.
func (m *Model) pickConnection() (string, bool) {
	if m.wizard != nil || m.editing == "" {
		return "", false
	}
	saved, ok := m.cfg.Connections[m.editing]
	if !ok || saved.Service != m.formService() || saved.Credential != m.fieldValue("credential") {
		return "", false
	}
	if !m.svc.CanBrowseTargets(m.formProvider()) {
		return "", false
	}
	return m.editing, true
}

// pickRoots are the kinds the picker can start on: those without a parent.
func (m *Model) pickRoots() []string {
	if _, ok := m.pickConnection(); !ok {
		return nil
	}
	return m.pickKindsUnder("")
}

func (m *Model) pickKindsUnder(parent string) []string {
	var kinds []string
	for _, kind := range m.targetMetadata().Kinds {
		if kind.Parent == parent {
			kinds = append(kinds, kind.Name)
		}
	}
	return kinds
}

func (m *Model) newPickLevel(p *targetPick, kinds []string, ki int, parent, label string) *pickLevel {
	p.nextID++
	lv := &pickLevel{id: p.nextID, kinds: kinds, ki: ki, parent: parent, label: label,
		entries: map[string]*pickEntry{}}
	lv.table = table{rows: map[string][]string{}, columns: []column{
		{title: "Name"}, {title: "Kind", optional: true}, {title: "ID"}, {title: "Path", optional: true},
	}}
	lv.list = newFilterList(func(target string) string {
		return strings.Join(lv.entries[target].cells, " ")
	})
	lv.list.match = func(target, query string) bool {
		// A searching provider has filtered already, and its idea of a match may differ from ours.
		return lv.server || query == "" || strings.Contains(strings.ToLower(strings.Join(lv.entries[target].cells, " ")), query)
	}
	lv.list.startFilter()
	return lv
}

// startPick opens the picker on a top-level kind and starts reading its first page.
func (m *Model) startPick(kind string) tea.Cmd {
	connection, ok := m.pickConnection()
	if !ok {
		m.fail = "choosing from the list needs the saved connection; save the form first"
		return nil
	}
	roots := m.pickKindsUnder("")
	ki := 0
	for i, root := range roots {
		if root == kind {
			ki = i
		}
	}
	p := &targetPick{connection: connection, marks: map[string]config.TargetEntry{}}
	p.levels = []*pickLevel{m.newPickLevel(p, roots, ki, "", "")}
	m.targetPick = p
	m.clearMessages()
	return m.pickLoad(p.top(), "")
}

// closePick leaves the picker and abandons every read in flight.
func (m *Model) closePick() {
	if p := m.targetPick; p != nil {
		for _, lv := range p.levels {
			if lv.cancel != nil {
				lv.cancel()
			}
		}
	}
	m.targetPick = nil
}

// pickLoad reads the page after cursor of a level, or its first page for an empty cursor. Starting a read
// abandons the one in flight.
func (m *Model) pickLoad(lv *pickLevel, cursor string) tea.Cmd {
	if lv.cancel != nil {
		lv.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	lv.cancel = cancel
	m.pickSeq++
	lv.req, lv.loading, lv.err = m.pickSeq, true, ""
	query := ""
	if lv.server {
		query = lv.list.query()
	}
	if cursor == "" {
		lv.sent = query
	}
	// The proof is taken here because the session is not safe for concurrent use. Without an active session
	// the core answers admin-required for an encrypted vault, which the picker shows.
	proof, _ := m.admin.Authorize(m.cfg.VaultAdminTimeout())
	req := config.TargetListRequest{Kind: lv.kind(), Parent: lv.parent, Query: query, Cursor: cursor}
	svc, connection, id := m.svc, m.targetPick.connection, lv.req
	return func() tea.Msg {
		page, _, err := svc.ListTargets(ctx, proof, connection, req)
		return pickLoadedMsg{req: id, replace: req.Cursor == "", page: page, err: err}
	}
}

// pickLoaded takes the answer of a read, unless the level it was for is gone or has started another read.
func (m *Model) pickLoaded(msg pickLoadedMsg) tea.Cmd {
	p := m.targetPick
	if p == nil {
		return nil
	}
	var lv *pickLevel
	for _, level := range p.levels {
		if level.req == msg.req {
			lv = level
		}
	}
	if lv == nil || !lv.loading {
		return nil
	}
	lv.loading = false
	if lv.cancel != nil {
		lv.cancel()
		lv.cancel = nil
	}
	if msg.err != nil {
		lv.err = m.pickError(msg.err)
		return nil
	}
	lv.server = msg.page.ServerSearch
	if msg.replace {
		lv.entries = map[string]*pickEntry{}
		lv.table.rows = map[string][]string{}
		lv.list.all = nil
		lv.list.cursor, lv.list.offset = 0, 0
	}
	names := lv.list.all
	for _, entry := range msg.page.Entries {
		if _, dup := lv.entries[entry.Target]; dup {
			continue
		}
		e := &pickEntry{entry: entry, cells: []string{m.pickText(entry.Name), m.pickText(entry.Kind),
			m.pickText(entry.Target), m.pickText(entry.Path)}}
		lv.entries[entry.Target] = e
		lv.table.rows[entry.Target] = m.pickCells(e)
		names = append(names, entry.Target)
	}
	lv.next = msg.page.Next
	lv.list.setItems(names)
	return m.pickSync(lv)
}

// pickError is the failure of a read as the core states it: its code and its fixed text.
func (m *Model) pickError(err error) string {
	var choice *manage.TargetChoiceError
	if errors.As(err, &choice) {
		return choice.Code + ": " + choice.Error()
	}
	return "error: " + m.pickText(m.redactor.Apply(err.Error()))
}

// pickSync asks a searching provider for the search text as it stands now, once the level shows the entries
// of another text.
func (m *Model) pickSync(lv *pickLevel) tea.Cmd {
	if lv != m.targetPick.top() || !lv.server || lv.list.query() == lv.sent {
		return nil
	}
	return m.pickLoad(lv, "")
}

// pickSearch is the end of the pause after a search text.
func (m *Model) pickSearch(msg pickSearchMsg) tea.Cmd {
	if m.targetPick == nil {
		return nil
	}
	lv := m.targetPick.top()
	if lv.id != msg.level || lv.seq != msg.seq {
		return nil
	}
	return m.pickSync(lv)
}

// pickMore reads the next page once the marker is on the last entry.
func (m *Model) pickMore(lv *pickLevel) tea.Cmd {
	if lv.next == "" || lv.loading || (len(lv.list.matches) > 0 && lv.list.cursor < len(lv.list.matches)-1) {
		return nil
	}
	return m.pickLoad(lv, lv.next)
}

// pickText makes untrusted text of a provider safe to print.
func (m *Model) pickText(s string) string { return m.logText(s) }

// pickLabel is the name of an entry as a target label: one line without control or invisible formatting
// characters, no longer than the configuration accepts.
func (m *Model) pickLabel(name string) string {
	s := strings.ToValidUTF8(m.redactor.Apply(name), "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxLabelRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxLabelRunes]))
	}
	return s
}

// pickCells are the cells of an entry in the table; a list that takes several targets has a tick in front.
func (m *Model) pickCells(e *pickEntry) []string {
	cells := append([]string(nil), e.cells...)
	if m.targetMetadata().Multiple {
		cells[0] = "[ ] " + cells[0]
	}
	return cells
}

// updatePick handles the picker. Every printable key belongs to the search.
func (m *Model) updatePick(key tea.KeyMsg) tea.Cmd {
	p := m.targetPick
	lv := p.top()
	m.clearMessages()
	switch key.String() {
	case "esc":
		if len(p.marks) > 0 {
			p.marks, p.markOrder = map[string]config.TargetEntry{}, nil
			m.status = "Marks cleared"
			return nil
		}
		m.closePick()
	case "enter":
		m.pickTake(true)
	case "right":
		return m.pickOpen(lv)
	case "left":
		return m.pickBack()
	case "tab":
		m.pickMark(lv)
	case "ctrl+t":
		return m.pickNextKind(lv)
	case "ctrl+r":
		return m.pickLoad(lv, "")
	case "up":
		lv.list.move(-1)
		return m.pickMore(lv)
	case "down":
		if lv.next != "" && len(lv.list.matches) > 0 && lv.list.cursor == len(lv.list.matches)-1 {
			// The end is not a place to wrap from while more entries are coming.
			return m.pickMore(lv)
		}
		lv.list.move(1)
		return m.pickMore(lv)
	case "pgup", "pgdown", "home", "end":
		start, end := m.pickWindow()
		lv.list.page(key.String(), start, end)
		return m.pickMore(lv)
	default:
		before := lv.list.query()
		cmd := lv.list.updateFilter(key)
		if lv.list.query() == before {
			return cmd
		}
		if lv.server {
			lv.seq++
			level, seq := lv.id, lv.seq
			return tea.Batch(cmd, tea.Tick(pickDelay, func(time.Time) tea.Msg {
				return pickSearchMsg{level: level, seq: seq}
			}))
		}
		return tea.Batch(cmd, m.pickMore(lv))
	}
	return nil
}

// current is the entry under the marker.
func (lv *pickLevel) current() (*pickEntry, bool) {
	target, ok := lv.list.selected()
	if !ok {
		return nil, false
	}
	return lv.entries[target], true
}

// pickOpen opens the entry under the marker to the level below it.
func (m *Model) pickOpen(lv *pickLevel) tea.Cmd {
	e, ok := lv.current()
	if !ok {
		return nil
	}
	kinds := m.pickKindsUnder(lv.kind())
	if !e.entry.HasChildren || len(kinds) == 0 {
		m.status = "Nothing lies below this entry"
		return nil
	}
	if lv.cancel != nil {
		// The page that was coming is read again if the person returns and reaches the end.
		lv.cancel()
		lv.cancel, lv.loading = nil, false
	}
	p := m.targetPick
	label := e.cells[2]
	if e.cells[0] != "" && e.cells[0] != label {
		label = e.cells[0] + " (" + label + ")"
	}
	child := m.newPickLevel(p, kinds, 0, e.entry.Target, label)
	p.levels = append(p.levels, child)
	return m.pickLoad(child, "")
}

// pickBack goes one level up, dropping the level it leaves.
func (m *Model) pickBack() tea.Cmd {
	p := m.targetPick
	if len(p.levels) == 1 {
		m.status = "Already on the top level"
		return nil
	}
	left := p.top()
	if left.cancel != nil {
		left.cancel()
	}
	p.levels = p.levels[:len(p.levels)-1]
	return m.pickSync(p.top())
}

// pickNextKind shows the next kind of the level's siblings, which lists other entries under the same parent.
func (m *Model) pickNextKind(lv *pickLevel) tea.Cmd {
	if len(lv.kinds) < 2 {
		return nil
	}
	lv.ki = (lv.ki + 1) % len(lv.kinds)
	lv.entries = map[string]*pickEntry{}
	lv.table.rows = map[string][]string{}
	lv.next, lv.server, lv.sent = "", false, ""
	lv.list.reset(nil)
	lv.list.startFilter()
	return m.pickLoad(lv, "")
}

// pickMark ticks or unticks the entry under the marker, where the provider takes several targets.
func (m *Model) pickMark(lv *pickLevel) {
	e, ok := lv.current()
	if !ok {
		return
	}
	if !m.targetMetadata().Multiple {
		m.fail = "this provider takes one " + m.targetMetadata().Label + " only; enter takes the entry"
		return
	}
	p := m.targetPick
	target := e.entry.Target
	if _, marked := p.marks[target]; marked {
		delete(p.marks, target)
		for i, t := range p.markOrder {
			if t == target {
				p.markOrder = append(p.markOrder[:i], p.markOrder[i+1:]...)
				break
			}
		}
		return
	}
	p.marks[target] = e.entry
	p.markOrder = append(p.markOrder, target)
}

// pickTake takes the marked entries, or the entry under the marker when none is marked and useCursor is
// set, into the list like typed targets. Nothing is taken unless every one is acceptable. A label is set
// from the provider's name only where the target has none. It reports whether the picker is done.
func (m *Model) pickTake(useCursor bool) bool {
	p := m.targetPick
	var picked []config.TargetEntry
	for _, target := range p.markOrder {
		picked = append(picked, p.marks[target])
	}
	if len(picked) == 0 && useCursor {
		if e, ok := p.top().current(); ok {
			picked = append(picked, e.entry)
		}
	}
	if len(picked) == 0 {
		return false
	}
	pending := map[string]bool{}
	for _, e := range picked {
		problem := m.targetProblem(e.Target, len(m.targetList.all))
		if problem == "" && pending[e.Target] {
			problem = "this " + m.targetMetadata().Label + " is already in the list"
		}
		if problem != "" {
			m.fail = m.pickText(e.Target) + ": " + problem
			return false
		}
		pending[e.Target] = true
	}
	for _, e := range picked {
		m.editTarget(len(m.targetList.all), e.Target)
		m.takeTarget()
		if label := m.pickLabel(e.Name); label != "" && m.targetLabels[e.Target] == "" {
			if m.targetLabels == nil {
				m.targetLabels = map[string]string{}
			}
			m.targetLabels[e.Target] = label
		}
	}
	m.closePick()
	m.targetAdd = nil
	m.clearMessages()
	m.status = fmt.Sprintf("Added %d from the list", len(picked))
	return true
}

// position is where the marker stands among the entries, as the title shows it.
func (lv *pickLevel) position() string {
	total := len(lv.list.all)
	if query := lv.list.query(); query != "" && !lv.server {
		return fmt.Sprintf("%d/%d (%d loaded)", min(lv.list.cursor+1, len(lv.list.matches)), len(lv.list.matches), total)
	}
	if total == 0 {
		return "0"
	}
	more := ""
	if lv.next != "" {
		more = "+"
	}
	return fmt.Sprintf("%d/%d%s", lv.list.cursor+1, total, more)
}

// pickFrame is everything of the picker except its rows.
func (m *Model) pickFrame() (string, string) {
	p := m.targetPick
	lv := p.top()
	title := "Choose " + lv.kind() + " from the list  " + lv.position()
	if len(p.marks) > 0 {
		title += fmt.Sprintf(" · %d marked", len(p.marks))
	}
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, title) + "\n")
	var where []string
	for i, level := range p.levels {
		if i > 0 {
			where = append(where, level.label)
		}
		where = append(where, level.kind())
	}
	head.WriteString(m.wrapped(hintStyle, "level: "+strings.Join(where, " › ")) + "\n")
	head.WriteString(m.searchLine(&lv.list, "search: ") + "\n")
	_, width := m.fit("  ")
	if len(lv.list.all) > 0 {
		head.WriteString(hintStyle.Render(clipCells("  "+lv.table.header(m.pickLayout(lv, width)), m.usable(0))) + "\n")
	}
	switch {
	case lv.err != "":
		head.WriteString(m.wrapped(failStyle, "[!] "+lv.err+" (ctrl+r reads again)") + "\n")
	case lv.loading:
		head.WriteString(m.wrapped(hintStyle, "loading ...") + "\n")
	case len(lv.list.all) == 0:
		head.WriteString(m.wrapped(hintStyle, "(the provider lists no entry here)") + "\n")
	case len(lv.list.matches) == 0 && lv.next != "":
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No loaded entry matches %q; down reads more.", lv.list.query())) + "\n")
	case len(lv.list.matches) == 0:
		head.WriteString(m.wrapped(hintStyle, fmt.Sprintf("No entry matches %q.", lv.list.query())) + "\n")
	}

	keys := "type to search · up/down move · enter take"
	if _, ok := lv.current(); ok && len(m.pickKindsUnder(lv.kind())) > 0 {
		keys += " · right open"
	}
	if len(p.levels) > 1 {
		keys += " · left back"
	}
	if m.targetMetadata().Multiple {
		keys += " · tab mark"
	}
	if len(lv.kinds) > 1 {
		keys += " · ctrl+t next kind"
	}
	keys += " · F2 save · esc "
	if len(p.marks) > 0 {
		keys += "clear marks"
	} else {
		keys += "close"
	}
	if lv.list.query() == "" {
		keys += " · ? help"
	}
	return head.String(), m.keyHint(keys) + m.notes()
}

// pickLayout is the layout of the level's table for width cells, kept while the entries stay the same.
func (m *Model) pickLayout(lv *pickLevel, width int) layout {
	if lv.laidWidth != width || lv.laidRows != len(lv.table.rows) || lv.laid.widths == nil {
		lv.laid, lv.laidWidth, lv.laidRows = lv.table.layout(width), width, len(lv.table.rows)
	}
	return lv.laid
}

// pickRow draws the shown entry at index i.
func (m *Model) pickRow(i int) string {
	p := m.targetPick
	lv := p.top()
	_, width := m.fit("  ")
	e := lv.entries[lv.list.matches[i]]
	cells := m.pickCells(e)
	if _, marked := p.marks[e.entry.Target]; marked {
		cells[0] = "[x]" + strings.TrimPrefix(cells[0], "[ ]")
	}
	return m.listRow(i == lv.list.cursor, i%2 == 1, lv.table.line(cells, m.pickLayout(lv, width)))
}

func (m *Model) pickWindow() (int, int) {
	header, footer := m.pickFrame()
	return m.windowIn(&m.targetPick.top().list, header, footer, m.pickRow)
}

// pickView draws the picker like a list: its frame, and between them as many entries as fit.
func (m *Model) pickView() string {
	header, footer := m.pickFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.pickWindow()
	for i := start; i < end; i++ {
		b.WriteString(m.pickRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}
