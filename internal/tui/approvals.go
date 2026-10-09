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
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
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
		return title + m.wrapped(failStyle, "error: "+unavailable) + m.keyHint("esc back · ? help")
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
			m.keyHint("esc back · ? help")
	}
	return m.approvalChangeView(title, *change)
}

// approvalChangeView is the title and body of the detail screen for one change. The whole body is laid out
// first and shown from m.approvalOffset on, so a scope of any length fits the workspace and only the lines
// scroll; nothing is cut.
func (m *Model) approvalChangeView(title string, change approval.Change) string {
	var lines []string
	for _, text := range m.approvalOriginLines(change.Connection) {
		lines = append(lines, strings.Split(m.row(false, text), "\n")...)
	}
	for _, row := range approvalBody(change, m.approvalExpanded) {
		if row.item {
			lines = append(lines, strings.Split(m.indentedWith(lipgloss.NewStyle(), row.text), "\n")...)
			continue
		}
		lines = append(lines, strings.Split(m.row(false, row.text), "\n")...)
	}
	keys := "o logs · y approve · n/esc back, stays open · ? help"
	if len(lines) > 0 && approvalHasKept(change) {
		keys = "e unchanged · " + keys
		if m.approvalExpanded {
			keys = "e fold unchanged · o logs · y approve · n/esc back, stays open · ? help"
		}
	}
	room := m.approvalRoom(title, keys, len(lines))
	offset := max(min(m.approvalOffset, len(lines)-room), 0)
	m.approvalOffset, m.approvalPage = offset, room
	foot := m.keyHint(keys)
	if len(lines) > room {
		foot = "\n" + m.wrapped(hintStyle, fmt.Sprintf("lines %d-%d of %d", offset+1,
			min(offset+room, len(lines)), len(lines))) + m.keyHint("up/down scroll · "+keys)
	}
	return title + strings.Join(lines[offset:min(offset+room, len(lines))], "\n") + "\n" + foot
}

// approvalRoom is how many body lines of the detail screen fit under its title and above its keys; when the
// body is longer, one of them goes to the position line and the keys gain the scroll keys.
func (m *Model) approvalRoom(title, keys string, lines int) int {
	room := m.height - strings.Count(title, "\n") - strings.Count(m.keyHint(keys), "\n") - 1
	if lines > room {
		longer := m.keyHint("up/down scroll · " + keys)
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
	b.WriteString(m.keyHint("y approve all · n/esc cancel · ? help"))
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

// approvalSweepMsg carries the outcome of autoApprove or revokeApproval back into the event loop: both run
// asynchronously, like every other write this editor sends to the vault (see runVaultActionField,
// writeVaultSecret), as the automatic side effect of a save or delete that already succeeded on its own.
// note is appended to m.status once it lands, "" says nothing; neither ever sets m.fail; see their own
// comments for why.
type approvalSweepMsg struct{ note string }

// autoApprove approves, in an encrypted and unlocked vault only, what the core decides a managing action
// that just succeeded opened (manage.ApprovalSweepNewlyOpened): the connections newly open compared with
// before, plus direct, a connection saved directly by name in its own form or created by the guided setup,
// unless it is open for a reason that form never displayed. That one stays open, and the status names the
// Approvals section as the way to review and release it. Nothing runs when before has nothing to compare
// with (see manage.SnapshotApprovals). A failure to approve never undoes the action that already
// succeeded; it is reported as a warning appended to m.status once the write lands, and never carries a
// secret value.
func (m *Model) autoApprove(before manage.ApprovalSnapshot, direct string) tea.Cmd {
	if !before.Valid() {
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		return nil
	}
	cfg, redactor := m.cfg, m.redactor
	m.vaultBusy = true
	m.writes++
	m.busy = "checking which connections to approve"
	return func() tea.Msg {
		res := manage.ApproveAfterChange(context.Background(), v, before, cfg, direct,
			manage.ApprovalSweepNewlyOpened)
		return approvalSweepMsg{note: approvalNote(res, redactor)}
	}
}

// approvalNote words what an approval after a change did, to join the status the change reported: "" when
// nothing is worth saying, otherwise a part that starts with "; ". It names no secret value.
func approvalNote(res manage.ApprovalResult, redactor *redact.Redactor) string {
	if res.CheckErr != nil {
		return ""
	}
	note := ""
	if res.StayedOpen != "" && res.ForwardChanged {
		// A release of payload secrets is a decision of its own, never approved in passing.
		note = fmt.Sprintf("; %s stays open: its forward_secrets changed, which only an approval in "+
			"Approvals (%d) releases", res.StayedOpen, int(sectionApprovals)+1)
	} else if res.StayedOpen != "" {
		note = fmt.Sprintf("; %s stays open: its service or credential changed outside this form; "+
			"review it in Approvals (%d)", res.StayedOpen, int(sectionApprovals)+1)
	}
	switch {
	case res.ApproveErr != nil:
		return note + "; warning: no connection was approved to read from the vault: " +
			redactor.Apply(res.ApproveErr.Error())
	case res.Warning != "":
		return note + "; warning: " + res.Warning
	case len(res.Approved) > 0:
		return note + fmt.Sprintf("; approved %d connection(s) to read from the vault: %s",
			len(res.Approved), strings.Join(res.Approved, ", "))
	}
	return note
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

// approvalOriginMsg carries the origin of one open change, read from the log as a command, back into the
// event loop.
type approvalOriginMsg struct {
	name   string
	origin approval.Origin
}

// approvalChange is the open change of name, if the Approvals section lists it right now.
func (m *Model) approvalChange(name string) (approval.Change, bool) {
	report, unavailable := m.approvalsReport()
	if unavailable != "" {
		return approval.Change{}, false
	}
	return manage.OpenChange(report, name)
}

// loadApprovalOrigin reads where the change of name came from, as a command: reading the log never runs on
// the event loop and never asks for a passphrase. Until it answers the detail screen says it is looking.
func (m *Model) loadApprovalOrigin(name string) tea.Cmd {
	m.approvalOriginFor, m.approvalOriginReady = name, false
	change, ok := m.approvalChange(name)
	if !ok {
		return nil
	}
	v := m.logVault()
	configPath := m.svc.Path()
	retention := m.cfg.LogRetentionDays()
	return func() tea.Msg {
		checker, _, done := logChecker(context.Background(), v)
		defer done()
		var modified time.Time
		if info, err := os.Stat(configPath); err == nil {
			modified = info.ModTime()
		}
		origins := approval.Origins(v.Dir(), checker, []approval.Change{change}, modified, time.Now(), retention)
		return approvalOriginMsg{name: name, origin: origins[name]}
	}
}

// handleApprovalOrigin takes the origin of the connection whose detail screen is still open; an answer for
// another one is dropped.
func (m *Model) handleApprovalOrigin(msg approvalOriginMsg) {
	if msg.name != m.approvalDetail || msg.name != m.approvalOriginFor {
		return
	}
	m.approvalOrigin, m.approvalOriginReady = msg.origin, true
}

// approvalOriginLines is the "source" row of the detail screen, made safe to print; a change made outside
// qatlas adds a second row that says what that can mean.
func (m *Model) approvalOriginLines(name string) []string {
	if m.approvalOriginFor != name {
		return nil
	}
	if !m.approvalOriginReady {
		return []string{fmt.Sprintf("%-11s looking it up in the log...", "source")}
	}
	lines := []string{fmt.Sprintf("%-11s %s", "source", m.logText(m.approvalOrigin.Text()))}
	if m.approvalOrigin.Source == approval.OriginOutside {
		lines = append(lines, fmt.Sprintf("%-11s an agent or another program may have edited the file", ""))
	}
	return lines
}

// openApprovalLogs opens the Logs section on the connection of the detail screen: every entry about it
// since it was last approved, or in the retention window for one never approved. Whether the filter finds
// anything is for the log to show.
func (m *Model) openApprovalLogs(name string) tea.Cmd {
	change, ok := m.approvalChange(name)
	if !ok {
		return nil
	}
	now := time.Now()
	lv := m.logs
	lv.mode = logModeRange
	lv.from = approval.LogWindow(change, now, m.cfg.LogRetentionDays()).Format(logDateLayout)
	lv.to = now.In(time.Local).Format(logDateLayout)
	lv.filters = [logFilterCount]string{}
	lv.filters[logFilterConnection] = m.logText(name)
	lv.list.clearFilter()
	m.approvalDetail = ""
	return m.openSection(sectionLogs)
}
