package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The filter bar of the grouped lists (Connections, Services, Credentials) changes only what the list shows
// and in which order. It never touches the configuration, and it lives as long as the editor does.

// listOptions are the values of the filter bar of one section. The zero value is the default view: grouped
// by provider, every state, sorted by name.
type listOptions struct {
	flat       bool
	state      int
	byProvider bool
}

// barKind names a field of the filter bar.
type barKind int

const (
	barGroup barKind = iota
	barState
	barSort
)

// listStateValues are the alternatives of the state field; see connectionActive.
var listStateValues = []string{"All", "Active", "Inactive"}

// barKinds are the fields of the bar of section s: the state field only where the entries have a state.
func barKinds(s section) []barKind {
	if s == sectionConnections {
		return []barKind{barGroup, barState, barSort}
	}
	return []barKind{barGroup, barSort}
}

// listBarShown reports whether the list of the active section has a filter bar: a grouped section that
// holds any entry at all, so a bar that hides everything can still be changed back.
func (m *Model) listBarShown() bool {
	if !groupedSection(m.section) {
		return false
	}
	return len(m.entryNames(m.section)) > 0
}

// opts is the filter bar state of section s.
func (m *Model) opts(s section) *listOptions {
	if m.listOpts == nil {
		m.listOpts = map[section]*listOptions{}
	}
	if m.listOpts[s] == nil {
		m.listOpts[s] = &listOptions{}
	}
	return m.listOpts[s]
}

// barField is what one field of the bar reads: label, alternatives and the active one.
func (m *Model) barField(k barKind) (label string, values []string, active int) {
	o := m.opts(m.section)
	switch k {
	case barGroup:
		return "Group", []string{"Provider", "None"}, boolIndex(o.flat)
	case barState:
		return "State", listStateValues, o.state
	}
	return "Sort", []string{"Name", "Provider"}, boolIndex(o.byProvider)
}

// translated is the alternatives of a bar field as drawn.
func translated(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = wordText(v)
	}
	return out
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stepBar changes the value of field k by one alternative, wrapping.
func (m *Model) stepBar(k barKind, by int) {
	_, values, active := m.barField(k)
	m.setBar(k, wrap(active+by, len(values)))
}

// setBar gives field k the alternative next.
func (m *Model) setBar(k barKind, next int) {
	o := m.opts(m.section)
	switch k {
	case barGroup:
		o.flat = next == 1
	case barState:
		o.state = next
	case barSort:
		o.byProvider = next == 1
	}
	m.relist()
}

// relist rebuilds the list after the bar changed, on the entry that was selected where it is still shown.
func (m *Model) relist() {
	name, ok := m.selected()
	m.list.setItems(m.listItems(m.section))
	if ok {
		m.selectEntry(name)
	}
}

// connectionActive reports whether the connection offers its tools: not disabled and not expired.
func (m *Model) connectionActive(name string) bool {
	return strings.HasPrefix(m.connectionStateText(m.cfg.Connections[name]), stateActive)
}

// barNames applies the bar of section s to its sorted entry names: the state filter and the sort.
func (m *Model) barNames(s section, names []string) []string {
	o := m.opts(s)
	if s == sectionConnections && o.state != 0 {
		kept := names[:0:0]
		for _, name := range names {
			if m.connectionActive(name) == (o.state == 1) {
				kept = append(kept, name)
			}
		}
		names = kept
	}
	if o.byProvider {
		of := m.providerOf(s)
		sort.SliceStable(names, func(i, j int) bool {
			a, b := of(names[i]), of(names[j])
			if (a == "") != (b == "") {
				return b == ""
			}
			return a < b
		})
	}
	return names
}

// barPart is one drawn field of the bar: its plain text, without the focus marker.
type barPart struct {
	field int
	text  string
}

// searchContent is what the search field of the lists shows: "/ " and the query, or the input being typed,
// and nothing while the search is idle, which shows the placeholder.
func searchContent(l *filterList, room int) string {
	switch {
	case l.editing:
		return "/ " + fitInput(l.input, max(room-3, 1)).View()
	case l.query() != "":
		return "/ " + truncateCells(l.query(), max(room-3, 1))
	}
	return ""
}

// barLayout draws the bar on one line of width cells: the fields as dropdown fields and the search field
// right of them. Where the fields do not all fit, their values are cut; where even that does not suffice, the
// focused field stands alone, and a bar without the focus shrinks to the values it holds, or, while a search
// is typed, to the search field. It returns the line, the fields drawn and the plain search field ("" if none).
func (m *Model) barLayout(width int) (string, []barPart, string) {
	kinds := barKinds(m.section)
	focus := -1
	if m.screen == screenList && m.barFocus > 0 {
		focus = m.barFocus - 1
	}
	labels, values, summary := make([]string, len(kinds)), make([]string, len(kinds)), []string{}
	for i, k := range kinds {
		label, vals, active := m.barField(k)
		labels[i], values[i] = wordText(label), wordText(vals[active])
		summary = append(summary, strings.ToLower(labels[i])+" "+strings.ToLower(values[i]))
	}
	const gap = 2
	build := func(budget int) ([]int, bool) {
		widths := make([]int, len(kinds))
		total := gap * (len(kinds) - 1)
		for i := range kinds {
			widths[i] = dropdownWidth(labels[i], values[i])
			total += 2 + widths[i]
		}
		for total > budget {
			widest := -1
			for i := range widths {
				if widths[i] > dropdownWidth(labels[i], "") && (widest < 0 || widths[i] > widths[widest]) {
					widest = i
				}
			}
			if widest < 0 {
				return nil, false
			}
			widths[widest]--
			total--
		}
		return widths, true
	}
	idle := !(m.list.editing || m.list.query() != "")
	draw := func(widths []int, searchWidth int) (string, []barPart) {
		var parts []barPart
		var b strings.Builder
		used := 0
		for i := range kinds {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", gap))
				used += gap
			}
			field := dropdownField(labels[i], values[i], i == focus, widths[i])
			b.WriteString(barMarker(i == focus) + field)
			parts = append(parts, barPart{i, ansi.Strip(field)})
			used += 2 + lipgloss.Width(field)
		}
		if searchWidth > 0 {
			b.WriteString(strings.Repeat(" ", width-used-searchWidth) + searchField(searchContent(&m.list, searchWidth), searchWidth))
		}
		return b.String(), parts
	}
	if widths, ok := build(width - gap - searchFieldMin); ok {
		used := gap * (len(kinds) - 1)
		for i := range widths {
			used += 2 + widths[i]
		}
		sw := min(searchFieldMax, width-used-gap)
		line, parts := draw(widths, sw)
		return line, parts, ansi.Strip(searchField(searchContent(&m.list, sw), sw))
	}
	if widths, ok := build(width); ok && idle {
		line, parts := draw(widths, 0)
		return line, parts, ""
	}
	if focus >= 0 {
		field := dropdownField(labels[focus], values[focus], true, width-2)
		return barMarker(true) + field, []barPart{{focus, ansi.Strip(field)}}, ""
	}
	if !idle {
		return searchField(searchContent(&m.list, width), width), nil, ""
	}
	return styles.hint.Render(truncateCells("  "+strings.Join(summary, " · "), width)), nil, ""
}

// barLine is the bar as drawn on one line of width cells.
func (m *Model) barLine(width int) string {
	line, _, _ := m.barLayout(width)
	return line
}

// hitBarFields makes the fields of the filter bar and its search field clickable: a click on a field opens its
// list, which enter does on the focused field, and one on the search field starts the search, as / does.
// row is the screen row of the bar.
func (m *Model) hitBarFields(row int) {
	line, parts, search := m.barLayout(m.tableRoom())
	if m.tableFramed() {
		// The bar stands behind the side of the frame and its blank.
		line = "  " + line
	}
	var specs []barSpec
	for _, p := range parts {
		field := p.field
		specs = append(specs, barSpec{field: p.text, id: fmt.Sprint("bar", field), whole: func() tea.Cmd {
			if m.screen == screenNav {
				if cmd := m.focusList(); m.screen != screenList {
					return cmd
				}
			}
			m.barFocus = field + 1
			m.openBarDropdown(field)
			return nil
		}})
	}
	if search != "" {
		specs = append(specs, barSpec{field: strings.TrimRight(search, " "), whole: func() tea.Cmd {
			if m.screen == screenNav {
				if cmd := m.focusList(); m.screen != screenList {
					return cmd
				}
			}
			m.barFocus = 0
			m.list.startFilter()
			return nil
		}})
	}
	m.hitBar([]string{line}, row, specs)
}

// openBarDropdown opens the list of the bar field with the given index.
func (m *Model) openBarDropdown(field int) {
	k := barKinds(m.section)[field]
	_, values, active := m.barField(k)
	m.openDropdown(fmt.Sprint("bar", field), translated(values), active, func(j int) tea.Cmd {
		m.setBar(k, j)
		return nil
	})
}

// updateBar is a key on the focused filter bar. left/right change the value of the field, tab and shift+tab
// step through the fields, and leaving the bar at either end lands on the next stop: the tab line after the
// last field, the rows before the first.
func (m *Model) updateBar(key tea.KeyMsg) (tea.Cmd, bool) {
	kinds := barKinds(m.section)
	field := m.barFocus - 1
	switch key.String() {
	case "left", "right":
		by := 1
		if key.String() == "left" {
			by = -1
		}
		m.stepBar(kinds[field], by)
	case "tab":
		if field == len(kinds)-1 {
			m.barFocus = 0
			m.focusNav()
			return nil, true
		}
		m.barFocus++
	case "shift+tab":
		m.barFocus--
		if m.barFocus == 0 && m.paneWidth() > 0 {
			m.detailFocus, m.detailTab = true, tabOverview
		}
	case "enter":
		m.openBarDropdown(field)
	case "down":
		m.barFocus = 0
	case "up", keyOf(actBack):
		m.barFocus = 0
		m.focusNav()
	case "pgup", "pgdown", "home", "end", "/", "c", "u", "q", "n":
		// These belong to the rows or to the screen, not to a field: the rows take them over.
		m.barFocus = 0
		return nil, false
	default:
		if _, ok := sectionShortcut(key.String()); ok {
			m.barFocus = 0
			return nil, false
		}
	}
	return nil, true
}

// barKeys is the key line while the filter bar has the focus.
func (m *Model) barKeys() string {
	return "←/→ change · enter open · tab next · shift+tab previous · down rows · / filter · n new · 1-6/{back}/up areas · " +
		"{command} cmds · {help} help · q quit"
}
