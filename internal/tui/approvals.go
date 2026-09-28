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
	approval.FieldPermissions, approval.FieldTargets, approval.FieldTools,
}

// approvalDetailRows is one row per field of c: "field   before -> after" for a field that changed, or, for
// one that did not (or, for a new connection, has nothing yet to compare with), its current value under the
// same label, worded "unchanged" unless the connection is new.
func approvalDetailRows(c approval.Change) []string {
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
	}
	rows := make([]string, 0, len(approvalFields))
	for _, field := range approvalFields {
		if f, ok := changed[field]; ok {
			rows = append(rows, fmt.Sprintf("%-11s %s -> %s", field, f.Before, f.After))
			continue
		}
		value := "unchanged"
		if c.New {
			value = current[field]
		}
		rows = append(rows, fmt.Sprintf("%-11s %s", field, value))
	}
	return rows
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
	var b strings.Builder
	b.WriteString(title)
	for _, row := range approvalDetailRows(*change) {
		b.WriteString(m.row(false, row) + "\n")
	}
	b.WriteString(m.hint("y approve · n/esc back, stays open"))
	return b.String()
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

// directApprovable reports whether change - the direct connection's own open change just before it was
// saved - is made entirely of fields its own form shows: permissions, targets, tools, or a plain rename of
// its credential, never one "stored anew" (the vault holds a different entry under the same credential
// name), which the form cannot tell apart from an ordinary rename. A connection that was never approved at
// all (change.New) counts the same way: nothing in the form hid that either. Anything else - a changed
// provider, a changed origin (a service's base_url), or a credential replaced under the same name - left it
// open outside the fields the form shows, so saving unrelated fields must not approve it in passing.
func directApprovable(change approval.Change) bool {
	if change.New {
		return true
	}
	for _, f := range change.Fields {
		switch f.Field {
		case approval.FieldPermissions, approval.FieldTargets, approval.FieldTools:
		case approval.FieldCredential:
			if strings.HasSuffix(f.After, " (stored anew)") {
				return false
			}
		default:
			return false
		}
	}
	return true
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
		for _, c := range report.Open {
			switch {
			case c.Connection == direct && (!directWasOpen || directApprovable(directChange)):
				names = append(names, c.Connection)
			case c.Connection == direct:
				// Open for a reason the connection's own form never showed: only Approvals (6), or the form
				// itself once the hidden field is reviewed there, may release it.
				stayedOpen = c.Connection
			case !beforeOpen[c.Connection]:
				names = append(names, c.Connection)
			}
		}
		note := ""
		if stayedOpen != "" {
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
