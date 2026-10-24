package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Renames the vault shows that the configuration does not: a connection or credential renamed by hand in
// config.yaml, which leaves its approval or vault entry under the old name, and the rest of a rename that
// stopped early. Finding them is the core's (see renameFindings); acting on one is f on Connections,
// Credentials or Approvals, which lists them and offers the core's action for each: adopt a hand rename,
// finish an unfinished one, or put a credential's back. Every action shows the dry run first, on the rename
// screen, and changes nothing until it is confirmed there, in the admin session like any rename.

// renameFinding is one rename found in the vault. entry is the name in the configuration the list shows it
// at; from and to are the names the core's action takes: the old and new name of a hand rename, and for the
// rest of a rename the configured name and the name it was being renamed to.
type renameFinding struct {
	kind     string
	entry    string
	from, to string
	// leftover is the rest of a rename that stopped early; otherwise the finding is a hand rename.
	leftover bool
	// ambiguous says the vault could belong to more than one entry or the other way round; nothing is
	// suggested, so there is no action.
	ambiguous bool
	// carried names the connections whose approvals a credential's stopped rename carried over.
	carried []string
}

// canRevert reports whether the core can put the rename back: only a credential's stopped rename.
func (f renameFinding) canRevert() bool { return f.leftover && f.kind == manage.RenameCredential }

// action names what enter does for f, "" where it does nothing.
func (f renameFinding) action() string {
	switch {
	case f.ambiguous:
		return ""
	case f.leftover:
		return "finish"
	}
	return "adopt"
}

// findingsState is the open list of findings.
type findingsState struct {
	items  []renameFinding
	cursor int
}

// findingsKind is the kind of entry the active section lists, "" for Approvals, which lists every kind.
func (m *Model) findingsKind() string {
	switch m.section {
	case sectionConnections:
		return manage.RenameConnection
	case sectionCredentials:
		return manage.RenameCredential
	}
	return ""
}

// sectionFindings are the findings of the active section's kind: all of them in Approvals, none in a section
// that renames nothing.
func (m *Model) sectionFindings() []renameFinding {
	switch m.section {
	case sectionApprovals, sectionConnections, sectionCredentials:
	default:
		return nil
	}
	all := m.renameFindings()
	kind := m.findingsKind()
	if kind == "" {
		return all
	}
	var out []renameFinding
	for _, f := range all {
		if f.kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// findingsNotice is the hint a list shows about the renames found, at the foot: the finding of the selected
// entry, else the count of those of the section.
func (m *Model) findingsNotice(finds []renameFinding) string {
	if len(finds) == 0 {
		return ""
	}
	if name, ok := m.selected(); ok && m.section != sectionApprovals {
		for _, f := range finds {
			if f.entry == name {
				return m.hint(m.findingNotice(f))
			}
		}
	}
	hand, left, vague := 0, 0, 0
	for _, f := range finds {
		switch {
		case f.ambiguous:
			vague++
		case f.leftover:
			left++
		default:
			hand++
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{hand, "renamed by hand"}, {left, "unfinished"}, {vague, "ambiguous"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return m.hint(fmt.Sprintf("renames found in the vault: %s; f reviews them", strings.Join(parts, ", ")))
}

// findingNotice is the short hint at the entry a finding belongs to.
func (m *Model) findingNotice(f renameFinding) string {
	apply := m.redactor.Apply
	switch {
	case f.leftover:
		return fmt.Sprintf("%s %s has an unfinished rename to %s in the vault; f finishes it or puts it back",
			f.kind, apply(f.from), apply(f.to))
	}
	return fmt.Sprintf("%s %s was renamed by hand from %s; f adopts the rename in the vault", f.kind, apply(f.entry),
		apply(f.from))
}

// findingRow is the one line of a finding on the findings screen.
func (m *Model) findingRow(f renameFinding) string {
	apply := m.redactor.Apply
	switch {
	case f.ambiguous:
		return fmt.Sprintf("%-11s %s, ambiguous", f.kind, apply(f.from))
	case f.leftover:
		return fmt.Sprintf("%-11s %s -> %s, unfinished", f.kind, apply(f.from), apply(f.to))
	}
	return fmt.Sprintf("%-11s %s -> %s, renamed by hand", f.kind, apply(f.from), apply(f.to))
}

// findingDetail words what a finding is and what its action does, for the selected row.
func (m *Model) findingDetail(f renameFinding) string {
	apply := m.redactor.Apply
	what := "approval"
	if f.kind == manage.RenameCredential {
		what = "entry"
	}
	switch {
	case f.ambiguous:
		return fmt.Sprintf("The vault holds a %s %s under the name %s that could belong to more than one configured "+
			"%s, or the other way round; nothing is suggested, so there is no action here.", f.kind, what,
			apply(f.from), f.kind)
	case f.leftover && f.kind == manage.RenameConnection:
		return fmt.Sprintf("The approval of %s is left over from an unfinished rename of connection %s. enter "+
			"finishes the rename, so the connection takes the name %s; approving all in Approvals removes the "+
			"leftover instead and keeps the name %s.", apply(f.to), apply(f.from), apply(f.to), apply(f.from))
	case f.leftover:
		carried := ""
		if len(f.carried) > 0 {
			carried = ", with the approvals of " + apply(strings.Join(f.carried, ", "))
		}
		return fmt.Sprintf("The vault holds credential %s under the name %s%s, left over from an unfinished "+
			"rename; until it is resolved, %s is not read from the vault. enter finishes the rename to %s, b "+
			"puts it back under %s.", apply(f.from), apply(f.to), carried, apply(f.from), apply(f.to), apply(f.from))
	}
	return fmt.Sprintf("%s %s was renamed to %s by hand in the configuration, while the vault holds its %s under "+
		"the old name. enter adopts the rename: the %s follows the configuration.", f.kind, apply(f.from),
		apply(f.to), what, what)
}

// openFindings lists the renames found, from f on a list that has them. It says why when there is nothing to
// list: the vault is locked and cannot be read without a passphrase, or nothing was found.
func (m *Model) openFindings() tea.Cmd {
	switch m.section {
	case sectionApprovals, sectionConnections, sectionCredentials:
	default:
		return nil
	}
	m.clearMessages()
	if m.vaultLocked() {
		m.status = "The vault is locked; ctrl+l unlocks it."
		return nil
	}
	items := m.renameFindings()
	if len(items) == 0 {
		m.status = "No renames found in the vault."
		return nil
	}
	state := &findingsState{items: items}
	if name, ok := m.selected(); ok && m.section != sectionApprovals {
		for i, f := range items {
			if f.entry == name && f.kind == m.findingsKind() {
				state.cursor = i
				break
			}
		}
	}
	m.findings, m.screen = state, screenFindings
	return nil
}

// reopenFindings shows the findings again after a dry run was left, read anew: what was resolved elsewhere
// is gone, and with nothing left the list is shown.
func (m *Model) reopenFindings() {
	state := m.findings
	if state == nil {
		return
	}
	items := m.renameFindings()
	if len(items) == 0 {
		m.findings, m.screen = nil, screenList
		return
	}
	state.items, state.cursor = items, min(state.cursor, len(items)-1)
	m.screen = screenFindings
}

func (m *Model) closeFindings() {
	m.findings, m.screen = nil, screenList
}

func (m *Model) updateFindings(key tea.KeyMsg) tea.Cmd {
	s := m.findings
	if s == nil {
		m.screen = screenList
		return nil
	}
	switch key.String() {
	case keyOf(actBack):
		m.closeFindings()
	case "up":
		s.cursor = wrap(s.cursor-1, len(s.items))
	case "down":
		s.cursor = wrap(s.cursor+1, len(s.items))
	case "home":
		s.cursor = 0
	case "end":
		s.cursor = len(s.items) - 1
	case "enter":
		return m.askFinding(false)
	case "b":
		return m.askFinding(true)
	}
	return nil
}

// askFinding starts the action for the selected finding, once the admin session allows it: the dry run
// comes first and nothing is written before it is confirmed. A finding without an action says why.
func (m *Model) askFinding(revert bool) tea.Cmd {
	f := m.findings.items[m.findings.cursor]
	m.clearMessages()
	switch {
	case f.ambiguous:
		m.status = "Ambiguous: nothing is suggested; check by hand."
		return nil
	case revert && !f.canRevert():
		m.status = "Only the unfinished rename of a credential can be put back."
		return nil
	}
	if work := m.updateBlocker(); work != "" {
		m.status = "Wait until " + work + " finishes, then try again."
		return nil
	}
	return m.requireAdmin(func() tea.Cmd { return m.planFinding(f, revert) })
}

// planFinding asks the core for the dry run of f's action and shows it on the rename screen. approve is true
// because this runs in the admin session (or against an unencrypted vault), like the rename key.
func (m *Model) planFinding(f renameFinding, revert bool) tea.Cmd {
	m.clearMessages()
	var (
		plan *manage.RenamePlan
		err  error
	)
	switch {
	case revert:
		plan, err = m.svc.PlanRevertRename(f.from, f.to, true)
	case f.leftover:
		plan, err = m.svc.PlanRename(f.kind, f.from, f.to, true)
	default:
		plan, err = m.svc.PlanAdopt(f.kind, f.from, f.to, true)
	}
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	m.rename = &renameState{kind: f.kind, old: f.from, plan: plan, back: screenFindings}
	m.screen = screenRename
	return nil
}

// findingsView draws the findings, the selected one explained below the rows.
func (m *Model) findingsView() string {
	s := m.findings
	if s == nil {
		return ""
	}
	head := m.wrapped(styles.title, "Renames found in the vault") + "\n\n"
	f := s.items[s.cursor]
	keys := "up/down choose"
	switch {
	case f.canRevert():
		keys += " · enter finish · b put back"
	case f.action() != "":
		keys += " · enter " + f.action()
	}
	keys += " · {back} back · {command} cmds · {help} help"
	foot := "\n" + m.wrapped(styles.hint, m.findingDetail(f)) + m.keyHint(keys) + m.notes()
	room := max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 1)
	start := max(min(s.cursor-room/2, len(s.items)-room), 0)
	end := min(start+room, len(s.items))
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, m.row(i == s.cursor, m.findingRow(s.items[i])))
	}
	return head + strings.Join(rows, "\n") + foot
}
