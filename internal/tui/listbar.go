package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stepBar changes the value of field k by one alternative, wrapping.
func (m *Model) stepBar(k barKind, by int) {
	_, values, active := m.barField(k)
	next := wrap(active+by, len(values))
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

// barLine draws the bar on one line of width cells. Where its fields do not all fit, the focused field
// stands alone, and a bar without the focus shrinks to the values it holds.
func (m *Model) barLine(width int) string {
	var fields, summary []string
	for i, k := range barKinds(m.section) {
		label, values, active := m.barField(k)
		fields = append(fields, barMarker(m.screen == screenList && m.barFocus == i+1)+label+": "+barChips(values, active))
		summary = append(summary, strings.ToLower(label)+" "+strings.ToLower(values[active]))
	}
	if line := strings.Join(fields, "  "); lipgloss.Width(line) <= width {
		return line
	}
	if m.screen == screenList && m.barFocus > 0 {
		return truncateCells(fields[m.barFocus-1], width)
	}
	return styles.hint.Render(truncateCells("  "+strings.Join(summary, " · "), width))
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
	case "down", "enter":
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
	return "←/→ change · tab next · shift+tab previous · down rows · / filter · n new · 1-6/{back}/up areas · " +
		"{command} cmds · {help} help · q quit"
}
