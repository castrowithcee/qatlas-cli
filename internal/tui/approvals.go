// The Approvals section (see the sectionApprovals comment in tui.go): the connections an encrypted vault
// has not approved as they are configured now, and the automatic side of approving them, which runs after
// every managing action that saves or removes a connection, or the credential or service it depends on.
//
// Every function here that reads or changes an approval defers to package approval, the same core
// 'qatlas vault approve' uses; nothing here reimplements what counts as a change.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// approvalsReport is the Approvals section's data: which connections still need approval, right now, in an
// encrypted and unlocked vault. unavailable names why the section cannot show that list otherwise, in the
// same words a person reads on the section itself, so a caller shows it instead of an empty list: there is
// no vault, it is unencrypted (an unencrypted vault binds no connection), or it is locked. A locked vault is
// never unlocked merely to compute this list, the same way a connection test or a secret row does not
// unlock it either; enter on the empty list is what does, through requireAdmin like any other managing
// action, and updateList's own key hint says so (see approvalsUnlockHint). ctrl+l unlocks it the same way,
// from here or any other section (see handleVaultLockKey in tui.go, which intercepts it before this list
// ever sees the key).
func (m *Model) approvalsReport() (report approval.Report, unavailable string) {
	v := m.secrets.Vault()
	if v == nil {
		return approval.Report{}, "There is no vault configured for this run."
	}
	state, err := v.State()
	if err != nil {
		return approval.Report{}, m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent, vault.StateUnencrypted:
		return approval.Report{}, fmt.Sprintf(
			"Approvals are not used while the vault is unencrypted. Open Vault (%d) to set a passphrase.",
			int(sectionVault)+1)
	case vault.StateLocked:
		return approval.Report{}, "The vault is locked, so open approvals cannot be listed; " +
			"press enter or ctrl+l to unlock it."
	}
	report, err = approval.Pending(m.cfg, v)
	if err != nil {
		return approval.Report{}, m.redactor.Apply(err.Error())
	}
	return report, ""
}

// hasOpenApprovals reports whether the sidebar's next: hint should point at the Approvals section.
func (m *Model) hasOpenApprovals() bool {
	report, unavailable := m.approvalsReport()
	return unavailable == "" && len(report.Open) > 0
}

// approvalSummary is the short reason one connection of the Approvals list needs approval: that it is new,
// or which fields of its scope changed since it was last approved.
func approvalSummary(c approval.Change) string {
	if c.New {
		return "new connection, not yet approved"
	}
	fields := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		fields[i] = f.Field
	}
	return strings.Join(fields, ", ") + " changed"
}

// approvalFields is every field of a connection's scope, in the order approval.Pending itself compares
// them, so the detail screen and the short summary above the list never disagree about what "changed"
// means.
var approvalFields = []string{
	approval.FieldCredential, approval.FieldProvider, approval.FieldOrigin,
	approval.FieldPermissions, approval.FieldTargets, approval.FieldTools, approval.FieldPaths,
	approval.FieldFiles, approval.FieldForward,
}

// approvalRow is one line of the Approvals detail screen. item rows are the entries of a list change,
// drawn under the field they belong to.
type approvalRow struct {
	text string
	item bool
}

// approvalDetailRows is the detail screen's text with unchanged entries folded away: see approvalBody.
func approvalDetailRows(c approval.Change) []string {
	rows := approvalBody(c, false)
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.text
	}
	return out
}

// approvalBody is the rows of the detail screen: one per field of c, "field   before -> after" for a field
// that changed from a single value or between a mode and a list, and for a list that stayed a list what is
// newly asked for (+) and what falls away (-), then the entries that stay as a count, spelled out only when
// expanded. A field that did not change is its current value under the same label, worded "unchanged"
// unless the connection is new, which has nothing yet to compare with and shows its whole scope.
func approvalBody(c approval.Change, expanded bool) []approvalRow {
	changed := map[string]approval.FieldChange{}
	for _, f := range c.Fields {
		changed[f.Field] = f
	}
	current := map[string]string{
		approval.FieldCredential:  choiceText(c.After.Credential),
		approval.FieldProvider:    choiceText(c.After.Provider),
		approval.FieldOrigin:      choiceText(c.After.Origin),
		approval.FieldPermissions: approvalList(c.After.Permissions),
		approval.FieldTargets:     approvalList(c.After.Targets),
		approval.FieldTools:       approvalTools(c.After.Tools),
		approval.FieldPaths:       approval.PathsText(c.After.Paths),
		approval.FieldFiles:       approval.FilesText(c.After.FilesRead, c.After.FilesWrite),
		approval.FieldForward:     approval.ForwardText(c.After.Forward),
	}
	rows := make([]approvalRow, 0, len(approvalFields))
	for _, field := range approvalFields {
		f, ok := changed[field]
		switch {
		case ok && f.IsListChange():
			rows = append(rows, approvalRow{text: fmt.Sprintf("%-11s %s", field, approvalCounts(f))})
			for _, v := range f.Added {
				rows = append(rows, approvalRow{text: "+ " + v, item: true})
			}
			for _, v := range f.Removed {
				rows = append(rows, approvalRow{text: "- " + v, item: true})
			}
			if len(f.Kept) > 0 {
				rows = append(rows, approvalRow{text: fmt.Sprintf("unchanged: %d", len(f.Kept)), item: true})
				if expanded {
					for _, v := range f.Kept {
						rows = append(rows, approvalRow{text: "  " + v, item: true})
					}
				}
			}
		case ok:
			rows = append(rows, approvalRow{text: fmt.Sprintf("%-11s %s -> %s", field, f.Before, f.After)})
		case c.New:
			rows = append(rows, approvalRow{text: fmt.Sprintf("%-11s %s", field, current[field])})
		default:
			rows = append(rows, approvalRow{text: fmt.Sprintf("%-11s unchanged", field)})
		}
	}
	return rows
}

// approvalCounts is the one-line summary of a list change: "2 new, 1 removed".
func approvalCounts(f approval.FieldChange) string {
	var parts []string
	if len(f.Added) > 0 {
		parts = append(parts, fmt.Sprintf("%d new", len(f.Added)))
	}
	if len(f.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", len(f.Removed)))
	}
	return strings.Join(parts, ", ")
}

// approvalHasKept reports whether any list change of c leaves entries unchanged, which u can then show.
func approvalHasKept(c approval.Change) bool {
	for _, f := range c.Fields {
		if f.IsListChange() && len(f.Kept) > 0 {
			return true
		}
	}
	return false
}

func approvalList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, " ")
}

func approvalTools(values []string) string {
	if values == nil {
		return "(every tool the permissions allow)"
	}
	return approvalList(values)
}

// approvalDetailView draws the Approvals detail screen: what would change if the named connection were
// approved now.
func (m *Model) approvalDetailView() string {
	name := m.approvalDetail
	title := m.wrapped(titleStyle, "Approve "+name+"?") + "\n\n"
	report, unavailable := m.approvalsReport()
	if unavailable != "" {
		// The vault changed under the open detail screen (locked, or turned unencrypted, elsewhere): there
		// is nothing left here to show or approve.
		return title + m.wrapped(failStyle, "error: "+unavailable) + m.hint("esc back")
	}
	var change *approval.Change
	for i := range report.Open {
		if report.Open[i].Connection == name {
			change = &report.Open[i]
			break
		}
	}
	if change == nil {
		// Approved or removed elsewhere while this screen was open.
		return title + m.wrapped(hintStyle, "this connection is no longer open; nothing to approve") +
			m.hint("esc back")
	}
	return m.approvalChangeView(title, *change)
}

// approvalChangeView is the title and body of the detail screen for one change. The whole body is laid out
// first and shown from m.approvalOffset on, so a scope of any length fits the workspace and only the lines
// scroll; nothing is cut.
func (m *Model) approvalChangeView(title string, change approval.Change) string {
	var lines []string
	for _, row := range approvalBody(change, m.approvalExpanded) {
		if row.item {
			lines = append(lines, strings.Split(m.indentedWith(lipgloss.NewStyle(), row.text), "\n")...)
			continue
		}
		lines = append(lines, strings.Split(m.row(false, row.text), "\n")...)
	}
	keys := "y approve · n/esc back, stays open"
	if len(lines) > 0 && approvalHasKept(change) {
		keys = "e unchanged · " + keys
		if m.approvalExpanded {
			keys = "e fold unchanged · y approve · n/esc back, stays open"
		}
	}
	room := m.approvalRoom(title, keys, len(lines))
	offset := max(min(m.approvalOffset, len(lines)-room), 0)
	m.approvalOffset, m.approvalPage = offset, room
	foot := m.hint(keys)
	if len(lines) > room {
		foot = "\n" + m.wrapped(hintStyle, fmt.Sprintf("lines %d-%d of %d", offset+1,
			min(offset+room, len(lines)), len(lines))) + m.hint("up/down scroll · "+keys)
	}
	return title + strings.Join(lines[offset:min(offset+room, len(lines))], "\n") + "\n" + foot
}

// approvalRoom is how many body lines of the detail screen fit under its title and above its keys; when the
// body is longer, one of them goes to the position line and the keys gain the scroll keys.
func (m *Model) approvalRoom(title, keys string, lines int) int {
	room := m.height - strings.Count(title, "\n") - strings.Count(m.hint(keys), "\n") - 1
	if lines > room {
		longer := m.hint("up/down scroll · " + keys)
		room = m.height - strings.Count(title, "\n") - strings.Count(longer, "\n") - 2
	}
	return max(room, 1)
}

// scrollApproval moves the detail screen's scroll position by delta lines; the view clamps it.
func (m *Model) scrollApproval(delta int) {
	m.approvalOffset = max(m.approvalOffset+delta, 0)
}

// approveAllConfirmView draws the Approvals bulk-approval confirmation, with the count of what it would
// approve and, where there is one, of the stale approvals it would also remove (see approveAll).
func (m *Model) approveAllConfirmView() string {
	report, _ := m.approvalsReport()
	var b strings.Builder
	b.WriteString(m.wrapped(titleStyle, fmt.Sprintf("Approve %d connection(s)?", len(report.Open))) + "\n\n")
	b.WriteString(m.indented("every connection currently open is approved as it is configured now") + "\n")
	if len(report.Stale) > 0 {
		b.WriteString(m.indented(fmt.Sprintf(
			"also removes %d stale approval(s) of a connection that no longer reads the vault",
			len(report.Stale))) + "\n")
	}
	b.WriteString(m.hint("y approve all · n/esc cancel"))
	return b.String()
}

// approvalActionMsg carries the outcome of approveOne or approveAll back into the event loop: the Approvals
// list's own actions, run asynchronously like every other write this editor sends to the vault (see
// runVaultActionField, writeVaultSecret). name is "" for approveAll, the one connection approveOne named;
// approved and staleRemoved are always 0 for approveOne, since it only ever approves the one name it was
// given and never touches a stale approval.
type approvalActionMsg struct {
	name         string
	approved     []string
	staleRemoved int
	warning      string
	err          error
}

// approveOne approves exactly the named connection, from the Approvals detail screen. Unlike autoApprove,
// which follows a save that already succeeded, this is the whole action, so a failure is reported as
// m.fail, never merely a warning.
func (m *Model) approveOne(name string) tea.Cmd {
	if m.vaultBusy {
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	cfg := m.cfg
	m.vaultBusy = true
	m.writes++
	m.busy = "approving " + name
	return func() tea.Msg {
		approved, warning, err := approval.Approve(context.Background(), cfg, v, []string{name})
		return approvalActionMsg{name: name, approved: approved, warning: warning, err: err}
	}
}

// approveAll approves every connection the Approvals section currently lists open, and, like
// 'qatlas vault approve' with no --connection, also removes every stale approval it finds: one left over
// from a connection that was deleted or renamed outside a delete this editor itself ran, which already
// revokes it on its own (see revokeApproval).
func (m *Model) approveAll() tea.Cmd {
	if m.vaultBusy {
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	cfg := m.cfg
	m.vaultBusy = true
	m.writes++
	m.busy = "approving every open connection"
	return func() tea.Msg {
		report, err := approval.Pending(cfg, v)
		if err != nil {
			return approvalActionMsg{err: err}
		}
		approved, warning, err := approval.Approve(context.Background(), cfg, v, nil)
		if err != nil {
			return approvalActionMsg{err: err}
		}
		var removed int
		if len(report.Stale) > 0 {
			var staleWarning string
			removed, staleWarning, err = approval.Revoke(context.Background(), v, report.Stale)
			if err != nil {
				return approvalActionMsg{err: err}
			}
			if warning == "" {
				warning = staleWarning
			}
		}
		return approvalActionMsg{approved: approved, staleRemoved: removed, warning: warning}
	}
}

// handleApprovalAction applies the outcome of approveOne or approveAll. A failure is reported the same way
// every other refused vault write in this editor is, on the screen the action was taken from; success
// returns to the Approvals list, freshly rebuilt, since approving changed what belongs on it.
func (m *Model) handleApprovalAction(msg approvalActionMsg) tea.Cmd {
	m.vaultBusy = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}
	if msg.err != nil {
		m.fail = m.redactor.Apply(msg.err.Error())
		return nil
	}
	// returnToList clears m.status on its way to the list (see showList); it runs first so the outcome set
	// below survives it, the same order every other save or delete of this editor already uses.
	cmd := m.returnToList(msg.name)
	switch {
	case msg.name != "":
		m.status = "Approved " + msg.name
	case len(msg.approved) == 0:
		m.status = "Nothing to approve"
	default:
		m.status = fmt.Sprintf("Approved %d connection(s)", len(msg.approved))
	}
	if msg.staleRemoved > 0 {
		m.status += fmt.Sprintf("; removed %d stale approval(s) no longer read from the vault",
			msg.staleRemoved)
	}
	if msg.warning != "" {
		m.status += "; warning: " + msg.warning
	}
	return cmd
}

// approvalBefore is what Pending finds about the configuration right now, in an encrypted and unlocked vault
// only, captured before a managing action changes it. ok is false when there is nothing to compare with at
// all: no vault, one that exists but is not encrypted (rule 2: it binds no connection, whatever this action
// does), or one that is locked (nothing here unlocks it merely to compare); autoApprove then runs nothing.
// ok is true, and report the zero Report, for a vault that does not exist yet: nothing is a candidate before
// it exists either, the same starting point as a candidate that is simply not open yet, so an action that
// creates and encrypts the vault in the same breath (see beginVaultSecret, commitSetup) can still tell
// autoApprove what it newly opened.
type approvalBefore struct {
	report approval.Report
	ok     bool
}

// approvalSnapshot captures approvalBefore for cfg right now.
func (m *Model) approvalSnapshot(v *vault.Vault, cfg *config.Config) approvalBefore {
	if v == nil {
		return approvalBefore{}
	}
	state, err := v.State()
	if err != nil {
		return approvalBefore{}
	}
	if state == vault.StateAbsent {
		return approvalBefore{ok: true}
	}
	if state != vault.StateUnlocked {
		return approvalBefore{}
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		return approvalBefore{}
	}
	return approvalBefore{report: report, ok: true}
}

// openSet is the connections report finds open, by name.
func openSet(report approval.Report) map[string]bool {
	open := map[string]bool{}
	for _, c := range report.Open {
		open[c.Connection] = true
	}
	return open
}

// openChange is the Change report holds for name, and whether it held one at all (name was open).
func openChange(report approval.Report, name string) (approval.Change, bool) {
	for _, c := range report.Open {
		if c.Connection == name {
			return c, true
		}
	}
	return approval.Change{}, false
}

// approvalSweepMsg carries the outcome of autoApprove or revokeApproval back into the event loop: both run
// asynchronously, like every other write this editor sends to the vault (see runVaultActionField,
// writeVaultSecret), as the automatic side effect of a save or delete that already succeeded on its own.
// note is appended to m.status once it lands, "" says nothing; neither ever sets m.fail; see their own
// comments for why.
type approvalSweepMsg struct{ note string }

// autoApprove approves, in an encrypted and unlocked vault only, exactly the connections a managing action
// that just succeeded opened: those Pending now finds open that before did not (a candidate that was fully
// approved, or held no vault entry yet, before this action ran), plus direct, when it names a connection
// that was not open before either, or was open only for fields directApprovable says its own form shows (a
// connection saved directly by name in its own form, or a new connection the guided setup just created, is
// approved unless it was already open for a reason the form never displayed, such as a base_url or provider
// changed outside it, or its credential replaced under the same name: that stays open, and the status names
// the Approvals section as the way to review and release it). before.ok is false when there was nothing to
// compare with (see approvalSnapshot); nothing runs then. A failure to approve never undoes the action that
// already succeeded; it is reported as a warning appended to m.status once the write lands, and never
// carries a secret value.
func (m *Model) autoApprove(before approvalBefore, direct string) tea.Cmd {
	if !before.ok {
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		return nil
	}
	beforeOpen := openSet(before.report)
	directChange, directWasOpen := openChange(before.report, direct)
	cfg, redactor := m.cfg, m.redactor
	m.vaultBusy = true
	m.writes++
	m.busy = "checking which connections to approve"
	return func() tea.Msg {
		report, err := approval.Pending(cfg, v)
		if err != nil {
			return approvalSweepMsg{}
		}
		var names []string
		var stayedOpen string
		forwardOpen := false
		for _, c := range report.Open {
			switch {
			case c.Connection == direct && !releaseChanged(c) &&
				(!directWasOpen || approval.DirectApprovable(directChange)):
				names = append(names, c.Connection)
			case c.Connection == direct:
				// Open for a reason the connection's own form never showed: only Approvals (6), or the form
				// itself once the hidden field is reviewed there, may release it.
				stayedOpen = c.Connection
				forwardOpen = releaseChanged(c)
			case !beforeOpen[c.Connection]:
				names = append(names, c.Connection)
			}
		}
		note := ""
		if stayedOpen != "" && forwardOpen {
			// A release of payload secrets is a decision of its own, never approved in passing.
			note = fmt.Sprintf("; %s stays open: its forward_secrets changed, which only an approval in "+
				"Approvals (%d) releases", stayedOpen, int(sectionApprovals)+1)
		} else if stayedOpen != "" {
			note = fmt.Sprintf("; %s stays open: its service or credential changed outside this form; "+
				"review it in Approvals (%d)", stayedOpen, int(sectionApprovals)+1)
		}
		if len(names) == 0 {
			return approvalSweepMsg{note: note}
		}
		approved, warning, err := approval.Approve(context.Background(), cfg, v, names)
		switch {
		case err != nil:
			return approvalSweepMsg{note: note + "; warning: no connection was approved to read from the vault: " +
				redactor.Apply(err.Error())}
		case warning != "":
			return approvalSweepMsg{note: note + "; warning: " + warning}
		case len(approved) > 0:
			return approvalSweepMsg{note: note + fmt.Sprintf("; approved %d connection(s) to read from the vault: %s",
				len(approved), strings.Join(approved, ", "))}
		}
		return approvalSweepMsg{note: note}
	}
}

// releaseChanged reports whether an approved connection's release of payload secrets differs from what was
// approved. Such a change is a decision of its own: no save approves it in passing, not even the connection's
// own form, which shows the list but not what each listed credential's fields mean for an approval. A
// connection that was never approved is new, and a person creating it has chosen its release.
func releaseChanged(c approval.Change) bool {
	if c.New {
		return false
	}
	for _, f := range c.Fields {
		if f.Field == approval.FieldForward {
			return true
		}
	}
	return false
}

// revokeApproval removes the approval of a connection just deleted, in an encrypted vault only; an
// unencrypted or missing vault binds no connection, so there is nothing to revoke there either, and this
// returns nothing to run. A failure to revoke is reported as a warning appended to m.status once the write
// lands, never m.fail, since the delete that triggered this already succeeded.
func (m *Model) revokeApproval(name string) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		return nil
	}
	redactor := m.redactor
	m.vaultBusy = true
	m.writes++
	m.busy = "removing the approval of " + name
	return func() tea.Msg {
		_, warning, err := approval.Revoke(context.Background(), v, []string{name})
		switch {
		case err != nil:
			if errors.Is(err, vault.ErrNotEncrypted) || errors.Is(err, vault.ErrNotUnlocked) {
				return approvalSweepMsg{}
			}
			return approvalSweepMsg{note: "; warning: the approval of " + name + " could not be removed: " +
				redactor.Apply(err.Error())}
		case warning != "":
			return approvalSweepMsg{note: "; warning: " + warning}
		}
		return approvalSweepMsg{}
	}
}

// handleApprovalSweep applies the outcome of autoApprove or revokeApproval: a note, if any, joins whatever
// m.status already says, since the save or delete it followed already reported its own outcome there; it
// never touches the screen, which that action already decided.
func (m *Model) handleApprovalSweep(msg approvalSweepMsg) tea.Cmd {
	m.vaultBusy = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}
	if msg.note != "" {
		m.status += msg.note
	}
	return nil
}
