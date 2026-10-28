package tui

import (
	"maps"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// A target row holds targets and exclusions in one list; the excluded set tells them apart, and saving splits
// the list in its order into the targets and the exclusions of the connection. What may be excluded is the
// core's to say (TargetMetadata.CheckExcludes); the screens only ask it.

// targetsFieldOf is the target list row over the targets and exclusions a connection holds now.
func targetsFieldOf(conn config.Connection) field {
	f := targetsField(append(conn.TargetValues(), conn.Exclude...)).withLabels(conn.TargetLabels)
	if len(conn.Exclude) > 0 {
		f.excluded = map[string]bool{}
		for _, value := range conn.Exclude {
			f.excluded[value] = true
		}
	}
	return f
}

// reachable are the entries of the row that are targets, in list order.
func (f field) reachable() []string { return reachableOf(f.entries, f.excluded) }

// reachableOf are the entries not in excluded.
func reachableOf(entries []string, excluded map[string]bool) []string {
	var out []string
	for _, entry := range entries {
		if !excluded[entry] {
			out = append(out, entry)
		}
	}
	return out
}

// excludesOf are the entries in excluded, in list order.
func excludesOf(entries []string, excluded map[string]bool) []string {
	var out []string
	for _, entry := range entries {
		if excluded[entry] {
			out = append(out, entry)
		}
	}
	return out
}

// targetRowOf is the target list row among fields, or nil.
func targetRowOf(fields []field) *field {
	for i := range fields {
		if fields[i].kind == fieldTargets {
			return &fields[i]
		}
	}
	return nil
}

// excludeAvailable reports whether the provider of the form declares exclusions at all.
func (m *Model) excludeAvailable() bool { return m.targetMetadata().Excludes() }

// canExclude reports whether the core accepts value as an exclusion of the provider.
func (m *Model) canExclude(value string) bool {
	metadata := m.targetMetadata()
	return metadata.Excludes() && len(metadata.CheckExcludes([]string{value}, nil)) == 0
}

// reachableCount is the number of targets of the open list, exclusions not counted.
func (m *Model) reachableCount() int {
	return len(reachableOf(m.targetList.all, m.targetExcluded))
}

// toggleExcluded switches the selected entry between reachable and excluded.
func (m *Model) toggleExcluded() {
	target, ok := m.targetList.selected()
	if !ok || !m.excludeAvailable() {
		return
	}
	m.clearMessages()
	if m.targetExcluded[target] {
		delete(m.targetExcluded, target)
		m.status = tr(tTargetNowReachable, target)
		return
	}
	if !m.canExclude(target) {
		m.fail = tr(tTargetCannotBeExcluded, m.targetMetadata().Label)
		return
	}
	if m.targetExcluded == nil {
		m.targetExcluded = map[string]bool{}
	}
	m.targetExcluded[target] = true
	m.status = tr(tTargetNowExcluded, target)
}

// tooManyTargets reports whether one more target would break a provider that takes one target only; an
// exclusion never counts against it.
func (m *Model) tooManyTargets(extra int) bool {
	return !m.targetMetadata().Multiple && m.reachableCount()+extra > 1
}

// targetLine is a target as its row shows it: marked where it is an exclusion, with its label where it has one.
func (m *Model) targetLine(target string, labels map[string]string, excluded map[string]bool) string {
	line := m.labelled(target, labels)
	if excluded[target] {
		return tr(tTargetExcludedMark) + line
	}
	return line
}

// targetsSummary is the short form of a target row: the reachable targets, and how many exclusions there are.
func targetsSummary(entries []string, excluded map[string]bool) string {
	reach := reachableOf(entries, excluded)
	if len(reach) == len(entries) {
		return targetSummary(entries)
	}
	value := targetSummary(reach)
	if value == "" {
		value = emptyValue(wordAny)
	}
	return value + " · " + tr(tTargetExceptCount, len(entries)-len(reach))
}

// cloneExcluded copies an excluded set; an empty one is nil.
func cloneExcluded(excluded map[string]bool) map[string]bool {
	if len(excluded) == 0 {
		return nil
	}
	return maps.Clone(excluded)
}

// sameExcluded reports whether two excluded sets hold the same entries.
func sameExcluded(a, b map[string]bool) bool {
	return len(a) == len(b) && maps.Equal(a, b) || len(a) == 0 && len(b) == 0
}

// setAsExclusion makes the entries added next exclusions or targets, opening the add menu when it is not
// open. The row of the add menu and the command are two ways to this one function.
func (m *Model) setAsExclusion(on bool) {
	if !m.excludeAvailable() {
		return
	}
	if m.targetAdd == nil {
		m.startAdd()
	}
	m.targetAsExclusion = on
	m.clearMessages()
	if m.targetAdd != nil {
		m.resetAddLine()
	}
}

// addExclusionUnavailable is why the command that adds an exclusion would do nothing now.
func (m *Model) addExclusionUnavailable() string {
	if m.screen != screenTargets {
		return tr(tCmdCannot, tr(tCmdWhatAddAnExclusion))
	}
	if !m.excludeAvailable() {
		return tr(tCmdNoExclusions)
	}
	if m.targetPick != nil || m.targetEdit >= 0 || m.labelEdit || m.targetRemove || (m.targetAdd != nil && m.targetAdd.kind >= 0) {
		return tr(tCmdCannot, tr(tCmdWhatAddAnExclusion))
	}
	return ""
}
