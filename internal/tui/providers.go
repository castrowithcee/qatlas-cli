package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// providerTable is how every provider row is chosen: a table of all the providers the row offers, with
// their name and ID, and a search line that is always there. Whatever the number of providers, it is the
// same table, opened the same way, so finding it takes no knowledge of a hidden key.
//
// It holds no provider knowledge of its own: the rows are the values of the form row, and the names come
// from the provider metadata the configuration core hands out.
type providerTable struct {
	list filterList
	// names maps each offered provider ID to the name the reader knows it by.
	names map[string]string
	// current is the value of the form row while the table is open. It changes only when a row is taken.
	current string
}

// open shows the given providers, selected on the current one, with the search ready for typing.
func (t *providerTable) open(ids []string, names map[string]string, current string) {
	t.names, t.current = names, current
	t.list = newFilterList(t.text)
	t.list.input.Placeholder = "type a name or ID"
	t.list.reset(ids)
	t.list.selectName(current)
	t.list.startFilter()
}

// name is how a provider reads: its name, or the empty value that a credential without a provider holds.
func (t *providerTable) name(id string) string {
	if id == "" {
		return choiceText(id)
	}
	if name := t.names[id]; name != "" {
		return name
	}
	return id
}

// text is what the search looks at for one provider: the name and the ID, the two columns the reader sees.
func (t *providerTable) text(id string) string { return t.name(id) + " " + id }

// columns splits width into the name and ID columns: the name column is as wide as the longest name as
// long as the IDs keep room, and never narrower than half of what is left beside the marks.
func (t *providerTable) columns(width int) (int, int) {
	nameWidth, idWidth := lipgloss.Width("NAME"), lipgloss.Width("ID")
	for _, id := range t.list.all {
		nameWidth = max(nameWidth, lipgloss.Width(t.name(id)))
		idWidth = max(idWidth, lipgloss.Width(id))
	}
	room := max(width-lipgloss.Width("(*) ")-2, 2)
	if nameWidth+idWidth > room {
		nameWidth = max(room-idWidth, room/2)
	}
	return nameWidth, max(room-nameWidth, 1)
}

// header is the line that names the columns, aligned with the rows below it.
func (t *providerTable) header(width int) string {
	nameWidth, _ := t.columns(width)
	return "    " + padLine("NAME", nameWidth) + "  ID"
}

// row is one provider: whether the form row holds it now, its name and its ID, each cut to its column.
func (t *providerTable) row(id string, width int) string {
	nameWidth, idWidth := t.columns(width)
	mark := "( ) "
	if id == t.current {
		mark = "(*) "
	}
	ids := id
	if id == "" {
		ids = "-"
	}
	return mark + padLine(truncateCells(t.name(id), nameWidth), nameWidth) + "  " + truncateCells(ids, idWidth)
}

// openProviderTable opens the table of the focused provider row.
func (m *Model) openProviderTable() {
	f := m.fields[m.focus]
	if len(f.choices) == 0 {
		return
	}
	names := map[string]string{}
	for _, id := range f.choices {
		metadata, _ := m.cfg.ProviderMetadata(id)
		names[id] = metadata.Name
	}
	m.providers.open(f.choices, names, f.value())
	m.screen = screenProviders
	m.clearMessages()
}

// updateProviderTable handles the table. Every printable key belongs to the search; enter takes the
// selected provider and esc leaves the row, and every row that depends on it, as it was. In the guided setup
// the table is the whole provider step, so taking a provider goes on to the next step and esc cancels the
// setup like on every other step.
func (m *Model) updateProviderTable(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		if m.wizard != nil {
			return m.leaveSetup()
		}
		m.screen = screenForm
	case "enter":
		id, ok := m.providers.list.selected()
		if !ok {
			// Nothing matches the search, so there is nothing to take.
			return nil
		}
		m.screen = screenForm
		cmd := m.choose(id)
		if m.wizard != nil {
			m.setupNext()
		}
		return cmd
	case "up":
		m.providers.list.move(-1)
	case "down":
		m.providers.list.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.providerTableWindow()
		m.providers.list.page(key.String(), start, end)
	default:
		return m.providers.list.updateFilter(key)
	}
	return nil
}

// providerTableView draws the table like a list: its frame, and between them as many providers as fit.
func (m *Model) providerTableView() string {
	header, footer := m.providerTableFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.providers.list, header, footer, m.providerTableRow)
	for i := start; i < end; i++ {
		b.WriteString(m.providerTableRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// providerTableFrame is everything of the table except its rows: above them the position among the matches
// and the total, the search line, the current provider and the column names; below them the keys and the
// notes. Like the picker it leaves out the heading of the other screens, so the rows keep room in a small
// terminal.
func (m *Model) providerTableFrame() (string, string) {
	t := &m.providers
	shown, total := len(t.list.matches), len(t.list.all)
	title := "Choose provider"
	keys := "type to search · up/down move · enter choose · esc cancel"
	if w := m.wizard; w != nil {
		title = fmt.Sprintf("Setup step %d of %d · choose provider", w.step+1, setupSteps)
		keys = "type to search · up/down move · enter choose and continue · esc cancel setup"
	}
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, fmt.Sprintf("%s  %d/%d (%d total)",
		title, min(t.list.cursor+1, shown), shown, total)) + "\n")
	head.WriteString(m.searchLine(&t.list, "search: ") + "\n")
	current := t.name(t.current)
	if t.current != "" {
		current += " (" + t.current + ")"
	}
	head.WriteString(m.wrapped(hintStyle, "current: "+current) + "\n")
	_, width := m.fit("  ")
	head.WriteString(hintStyle.Render(clipCells("  "+t.header(width), m.usable(0))) + "\n")
	if shown == 0 {
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No provider matches %q. esc keeps the current one.", t.list.query())) + "\n")
	}
	return head.String(), m.hint(keys) + m.notes()
}

// providerTableRow draws the shown provider at index i of the table.
func (m *Model) providerTableRow(i int) string {
	_, width := m.fit("  ")
	return m.row(i == m.providers.list.cursor, m.providers.row(m.providers.list.matches[i], width))
}

func (m *Model) providerTableWindow() (int, int) {
	header, footer := m.providerTableFrame()
	return m.windowIn(&m.providers.list, header, footer, m.providerTableRow)
}

// providerValue is how a provider row of the form reads: the name and ID of the provider it holds, in a
// box that shows it is opened rather than stepped through, cut to what the terminal leaves beside the label.
func (m *Model) providerValue(f field) string {
	if len(f.choices) == 0 {
		return "(nothing to choose)"
	}
	id := f.value()
	text := choiceText(id)
	if metadata, ok := m.cfg.ProviderMetadata(id); ok && id != "" {
		text = metadata.Name + " (" + id + ")"
	}
	if m.width > 0 {
		// The label column, the focus marker and the box around the text.
		text = truncateCells(text, m.width-15-lipgloss.Width("[  ▾ ]"))
	}
	return "[ " + text + " ▾ ]"
}

// providerHint puts the way into the table in front of what the row decides, so the row says itself how
// it is chosen.
func providerHint(f field) string {
	providers := 0
	for _, choice := range f.choices {
		if choice != "" {
			providers++
		}
	}
	lead := fmt.Sprintf("enter opens the table of all %d providers, with search", providers)
	if f.hint == "" {
		return lead
	}
	return lead + "; " + f.hint
}
