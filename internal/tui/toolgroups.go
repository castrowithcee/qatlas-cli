package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The grouped tool picker lists, beside the tool IDs, one row per group and one row for all groups. Their
// names start with a NUL byte, which no tool ID contains, so they never clash with a tool and never reach
// the ticks: groups are presentation only.
const (
	groupRowPrefix = "\x00g:"
	allGroupsRow   = "\x00all"
	unlistedGroup  = "not-registered"
)

// toolGroups is the fold state and the membership of the grouped tool picker. It is built only for a
// provider that sorts its tools into groups; a flat provider keeps the flat picker.
type toolGroups struct {
	groups    []config.ToolGroup
	members   map[string][]string
	of        map[string]string
	collapsed map[string]bool
	titleCol  int
}

// newToolGroups groups the tool choices of a picker by the groups of the provider's metadata, in the order
// of the provider. A tool the provider no longer registers, which a saved list may still hold, goes into a
// last group of its own. It returns nil for a provider without groups.
func newToolGroups(metadata config.ProviderMetadata, choices []string) *toolGroups {
	if len(metadata.Groups) == 0 {
		return nil
	}
	known := map[string]string{}
	for _, tool := range metadata.Tools {
		known[tool.ID] = tool.Group
	}
	g := &toolGroups{members: map[string][]string{}, of: map[string]string{}, collapsed: map[string]bool{}}
	for _, choice := range choices {
		id, ok := known[choice]
		if !ok {
			id = unlistedGroup
		}
		g.members[id] = append(g.members[id], choice)
		g.of[choice] = id
	}
	candidates := append(append([]config.ToolGroup{}, metadata.Groups...),
		config.ToolGroup{ID: unlistedGroup, Title: "Not registered"})
	for _, group := range candidates {
		if len(g.members[group.ID]) == 0 {
			continue
		}
		g.groups = append(g.groups, group)
		g.collapsed[group.ID] = true
		g.titleCol = max(g.titleCol, lipgloss.Width(g.title(group, false)))
	}
	return g
}

func (g *toolGroups) title(group config.ToolGroup, open bool) string {
	arrow := "▸ "
	if open {
		arrow = "▾ "
	}
	return fmt.Sprintf("%s%s (%d)", arrow, group.Title, len(g.members[group.ID]))
}

func (g *toolGroups) isRow(name string) bool { return strings.HasPrefix(name, "\x00") }

// entries is every entry of the picker in display order: all groups, then each group followed by its tools.
func (g *toolGroups) entries() []string {
	entries := []string{allGroupsRow}
	for _, group := range g.groups {
		entries = append(entries, groupRowPrefix+group.ID)
		entries = append(entries, g.members[group.ID]...)
	}
	return entries
}

// matches is the filter of the grouped picker. Without a query the groups show and their tools only while
// the group is open; with one, every tool whose text matches shows under its group, folded or not, and a
// group row shows when one of its tools does.
func (g *toolGroups) matches(text func(string) string) func(name, query string) bool {
	hit := func(tool, query string) bool { return strings.Contains(strings.ToLower(text(tool)), query) }
	return func(name, query string) bool {
		switch {
		case name == allGroupsRow:
			return query == "" || g.anyHit(g.all(), query, hit)
		case strings.HasPrefix(name, groupRowPrefix):
			return query == "" || g.anyHit(g.members[strings.TrimPrefix(name, groupRowPrefix)], query, hit)
		case query == "":
			return !g.collapsed[g.of[name]]
		}
		return hit(name, query)
	}
}

func (g *toolGroups) anyHit(tools []string, query string, hit func(string, string) bool) bool {
	for _, tool := range tools {
		if hit(tool, query) {
			return true
		}
	}
	return false
}

func (g *toolGroups) all() []string {
	var tools []string
	for _, group := range g.groups {
		tools = append(tools, g.members[group.ID]...)
	}
	return tools
}

// targets are the tools a group row or the all-groups row acts on: its tools, narrowed to the shown ones
// while a search is typed (visible is then set).
func (g *toolGroups) targets(row string, visible func(string) bool) []string {
	tools := g.all()
	if row != allGroupsRow {
		tools = g.members[strings.TrimPrefix(row, groupRowPrefix)]
	}
	if visible == nil {
		return tools
	}
	var shown []string
	for _, tool := range tools {
		if visible(tool) {
			shown = append(shown, tool)
		}
	}
	return shown
}

// toggle ticks every target of the row when not all of them are ticked, and unticks them all otherwise.
func (g *toolGroups) toggle(row string, marks map[string]bool, visible func(string) bool) {
	tools := g.targets(row, visible)
	ticked, _ := countTicks(tools, marks)
	for _, tool := range tools {
		if ticked == len(tools) {
			delete(marks, tool)
		} else {
			marks[tool] = true
		}
	}
}

// fold opens or closes the group of the row under the cursor (the all-groups row does so for every group)
// and returns the row the cursor belongs on afterwards, or "" when nothing changed.
func (g *toolGroups) fold(row string, collapse bool) string {
	switch {
	case row == allGroupsRow:
		for _, group := range g.groups {
			g.collapsed[group.ID] = collapse
		}
		return row
	case strings.HasPrefix(row, groupRowPrefix):
		id := strings.TrimPrefix(row, groupRowPrefix)
		if g.collapsed[id] == collapse {
			return ""
		}
		g.collapsed[id] = collapse
		return row
	case collapse:
		// A tool row folds its group, and the cursor follows to the group row.
		g.collapsed[g.of[row]] = true
		return groupRowPrefix + g.of[row]
	}
	return ""
}

func countTicks(tools []string, marks map[string]bool) (int, int) {
	ticked := 0
	for _, tool := range tools {
		if marks[tool] {
			ticked++
		}
	}
	return ticked, len(tools)
}

// count is the number of ticked tools of the whole list and its size.
func (g *toolGroups) count(marks map[string]bool) (int, int) { return countTicks(g.all(), marks) }

// rowText is how a group row or the all-groups row reads: the fold marker, title and tool count, and the
// ticked/total count of its tools behind them. A search shows every group open.
func (g *toolGroups) rowText(row string, marks map[string]bool, searching bool) string {
	if row == allGroupsRow {
		ticked, total := g.count(marks)
		return fmt.Sprintf("%s  %d/%d", padLine("  all groups", g.titleCol), ticked, total)
	}
	id := strings.TrimPrefix(row, groupRowPrefix)
	for _, group := range g.groups {
		if group.ID == id {
			ticked, total := countTicks(g.members[id], marks)
			return fmt.Sprintf("%s  %d/%d", padLine(g.title(group, searching || !g.collapsed[id]), g.titleCol), ticked, total)
		}
	}
	return row
}
