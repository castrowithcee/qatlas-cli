package tui

import (
	"fmt"
	"sort"
	"strings"
)

// noProviderGroup titles the group of entries whose provider cannot be determined. It sorts last.
const noProviderGroup = "no provider"

// groupedSection reports whether the list of s is shown grouped by provider.
func groupedSection(s section) bool {
	return s == sectionServices || s == sectionCredentials || s == sectionConnections
}

// isGroupRow reports whether a list name is a group row rather than an entry. Group rows reuse the NUL
// prefix of the tool picker, which no entry name contains.
func isGroupRow(name string) bool { return strings.HasPrefix(name, "\x00") }

// listGroups is the provider grouping of one list: presentation only, the entries keep their names and
// their order inside a group. The fold state outlives the grouping, which is rebuilt with every list.
type listGroups struct {
	keys      []string
	members   map[string][]string
	of        map[string]string
	collapsed map[string]bool
	// hits caches, for one refilter, whether an entry matches the query, so a group row and its entries
	// look at each entry once.
	hits map[string]bool
}

// newListGroups groups the sorted names by their provider key ("" for none). Groups are ordered
// alphabetically by provider, the group without provider last.
func newListGroups(names []string, provider func(name string) string, collapsed map[string]bool) *listGroups {
	g := &listGroups{members: map[string][]string{}, of: make(map[string]string, len(names)), collapsed: collapsed}
	for _, name := range names {
		key := provider(name)
		if _, ok := g.members[key]; !ok {
			g.keys = append(g.keys, key)
		}
		g.members[key] = append(g.members[key], name)
		g.of[name] = key
	}
	sort.Slice(g.keys, func(i, j int) bool {
		a, b := g.keys[i], g.keys[j]
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	return g
}

func groupRow(key string) string { return groupRowPrefix + key }

// items is every name of the list in display order: each group row followed by its entries.
func (g *listGroups) items() []string {
	items := make([]string, 0, len(g.of)+len(g.keys))
	for _, key := range g.keys {
		items = append(items, groupRow(key))
		items = append(items, g.members[key]...)
	}
	return items
}

// rowText is how a group row reads: the fold marker, the provider and the number of its entries.
func (g *listGroups) rowText(row string, open bool) string {
	key := strings.TrimPrefix(row, groupRowPrefix)
	title := key
	if key == "" {
		title = wordText(noProviderGroup)
	}
	arrow := "▸ "
	if open {
		arrow = "▾ "
	}
	return fmt.Sprintf("%s%s (%d)", arrow, title, len(g.members[key]))
}

// open reports whether the group of the row is shown unfolded.
func (g *listGroups) open(row string) bool {
	return !g.collapsed[strings.TrimPrefix(row, groupRowPrefix)]
}

// begin starts a refilter.
func (g *listGroups) begin() { g.hits = map[string]bool{} }

// matches is the filter of a grouped list. Without a query the group rows show and their entries only
// while the group is open; with one, every entry whose text matches shows under its group, folded or not,
// and a group row shows when one of its entries does. The fold state itself is not touched.
func (g *listGroups) matches(text func(string) string) func(name, query string) bool {
	hit := func(name, query string) bool {
		if v, ok := g.hits[name]; ok {
			return v
		}
		v := strings.Contains(strings.ToLower(text(name)), query)
		if g.hits != nil {
			g.hits[name] = v
		}
		return v
	}
	return func(name, query string) bool {
		switch {
		case isGroupRow(name):
			if query == "" {
				return true
			}
			for _, member := range g.members[strings.TrimPrefix(name, groupRowPrefix)] {
				if hit(member, query) {
					return true
				}
			}
			return false
		case query == "":
			return !g.collapsed[g.of[name]]
		}
		return hit(name, query)
	}
}

// fold opens or closes the group of the row (a group row or one of its entries) and returns the row the
// cursor belongs on, or "" when nothing changed. An entry only folds, and the cursor follows to its group.
func (g *listGroups) fold(row string, collapse bool) string {
	key, ok := g.of[row]
	if isGroupRow(row) {
		key, ok = strings.TrimPrefix(row, groupRowPrefix), true
	} else if !collapse {
		return ""
	}
	if !ok || g.collapsed[key] == collapse {
		return ""
	}
	g.collapsed[key] = collapse
	return groupRow(key)
}

// toggle flips the fold state of the group row.
func (g *listGroups) toggle(row string) {
	key := strings.TrimPrefix(row, groupRowPrefix)
	g.collapsed[key] = !g.collapsed[key]
}

// reveal unfolds the group of an entry and reports whether that changed anything.
func (g *listGroups) reveal(name string) bool {
	key, ok := g.of[name]
	if !ok || !g.collapsed[key] {
		return false
	}
	g.collapsed[key] = false
	return true
}

// providerOf is the provider group key of an entry of the grouped section s.
func (m *Model) providerOf(s section) func(name string) string {
	switch s {
	case sectionServices:
		return func(name string) string { return m.cfg.Services[name].Provider }
	case sectionCredentials:
		return func(name string) string { return m.credentialProvider(name, m.cfg.Credentials[name]) }
	}
	return func(name string) string { return m.cfg.Services[m.cfg.Connections[name].Service].Provider }
}

// listItems is what the list of s holds: its entry names, with a group row before each provider's entries
// in the grouped sections. It also installs the matching filter, so it is the one way to fill m.list.
func (m *Model) listItems(s section) []string {
	names := m.entryNames(s)
	if !groupedSection(s) {
		m.groups, m.list.match, m.list.begin = nil, nil, nil
		return names
	}
	names = m.barNames(s, names)
	if m.opts(s).flat {
		m.groups, m.list.match, m.list.begin = nil, nil, nil
		return names
	}
	if m.folds == nil {
		m.folds = map[section]map[string]bool{}
	}
	if m.folds[s] == nil {
		m.folds[s] = map[string]bool{}
	}
	g := newListGroups(names, m.providerOf(s), m.folds[s])
	m.groups, m.list.match, m.list.begin = g, g.matches(m.describe), g.begin
	return g.items()
}

// entryTotal is the number of entries of the list, group rows not counted.
func (m *Model) entryTotal() int {
	if m.groups == nil {
		return len(m.list.all)
	}
	return len(m.list.all) - len(m.groups.keys)
}

// entryPosition is the number of entries shown, and the place of the cursor among them (a group row counts
// as the entry above it, at least the first).
func (m *Model) entryPosition() (shown, pos int) {
	if m.groups == nil {
		return len(m.list.matches), m.list.cursor + 1
	}
	for i, name := range m.list.matches {
		if isGroupRow(name) {
			continue
		}
		shown++
		if i <= m.list.cursor {
			pos++
		}
	}
	return shown, max(pos, min(shown, 1))
}

// foldKey handles enter, left and right on a group row or, for left, on an entry. It reports whether the
// key was a fold key of this list. While a filter is set the groups show unfolded and stay as they are.
func (m *Model) foldKey(key string) bool {
	if m.groups == nil || m.list.query() != "" {
		return false
	}
	row, ok := m.list.selected()
	if !ok {
		return false
	}
	var target string
	switch {
	case key == "enter" && isGroupRow(row):
		m.groups.toggle(row)
		target = row
	case key == "left" || key == "right":
		target = m.groups.fold(row, key == "left")
	default:
		return false
	}
	if target != "" {
		m.list.refilter()
		m.list.selectName(target)
	}
	return true
}

// selectEntry selects the named entry, unfolding its group when it is folded.
func (m *Model) selectEntry(name string) {
	if m.list.selectName(name) || m.groups == nil || !m.groups.reveal(name) {
		return
	}
	m.list.refilter()
	m.list.selectName(name)
}

// resetList fills the list with the entries of s and puts the cursor on the first entry, below its group row.
func (m *Model) resetList(s section) {
	m.barFocus = 0
	m.list.reset(m.listItems(s))
	if len(m.list.matches) > 1 && isGroupRow(m.list.matches[0]) {
		m.list.cursor = 1
	}
}
