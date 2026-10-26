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
		return approval.Report{}, tr(tNoVaultSentence)
	}
	state, err := v.State()
	if err != nil {
		return approval.Report{}, m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent, vault.StateUnencrypted:
		return approval.Report{}, tr(tApprovalsNeedVault, sectionVault.ref())
	case vault.StateLocked:
		return approval.Report{}, tr(tApprovalsLocked)
	}
	report, err = approval.Pending(m.cfg, v)
	if err != nil {
		return approval.Report{}, m.redactor.Apply(err.Error())
	}
	return report, ""
}

// hasOpenApprovals reports whether the status line's next: hint should point at the Approvals section.
func (m *Model) hasOpenApprovals() bool {
	report, unavailable := m.approvalsReport()
	return unavailable == "" && len(report.Open) > 0
}

// approvalSummary is the short reason one connection of the Approvals list needs approval: that it is new,
// or which fields of its scope changed since it was last approved.
func approvalSummary(c approval.Change) string {
	if c.New {
		return tr(tApprovalNew)
	}
	fields := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		fields[i] = f.Field
	}
	return tr(tApprovalChanged, strings.Join(fields, ", "))
}

// approvalFields is every field of a connection's scope, in the order approval.Pending itself compares
// them, so the detail screen and the short summary above the list never disagree about what "changed"
// means.
var approvalFields = []string{
	approval.FieldCredential, approval.FieldProvider, approval.FieldOrigin,
	approval.FieldEffects, approval.FieldTargets, approval.FieldTools, approval.FieldPaths,
	approval.FieldFiles, approval.FieldForward, approval.FieldState, approval.FieldActiveUntil,
}

// approvalRow is one line of the Approvals detail screen: text as plain words, and, split for drawing, the
// label of its field and the value behind it. item rows are the entries of a list change, drawn under the
// field they belong to; tone marks what an entry does to the scope: '+' asks for it, '-' drops it, 'k' keeps it.
type approvalRow struct {
	text         string
	label, value string
	item         bool
	tone         byte
}

// fieldRow is the row of a field with its value.
func fieldRow(label, value string) approvalRow {
	return approvalRow{text: fmt.Sprintf("%-11s %s", label, value), label: label, value: value}
}

// itemRow is an entry under its field.
func itemRow(text string, tone byte) approvalRow {
	return approvalRow{text: text, value: text, item: true, tone: tone}
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
		approval.FieldOrigin:      choiceText(approval.OriginText(c.After.Origin)),
		approval.FieldEffects:     approvalList(c.After.Effects),
		approval.FieldTargets:     approvalList(c.After.Targets),
		approval.FieldTools:       approvalTools(c.After.Tools),
		approval.FieldPaths:       approval.PathsText(c.After.Paths),
		approval.FieldFiles:       approval.FilesText(c.After.FilesRead, c.After.FilesWrite),
		approval.FieldForward:     approval.ForwardText(c.After.Forward),
		approval.FieldState:       approval.StateText(c.After.Disabled),
		approval.FieldActiveUntil: approval.ActiveUntilText(c.After.ActiveUntil),
	}
	rows := make([]approvalRow, 0, len(approvalFields))
	for _, field := range approvalFields {
		f, ok := changed[field]
		switch {
		case ok && f.IsListChange():
			rows = append(rows, fieldRow(field, approvalCounts(f)))
			for _, r := range f.Renamed {
				rows = append(rows, itemRow(tr(tApprovalRenamedItem, r.Old, r.New), 0))
			}
			for _, v := range f.Added {
				rows = append(rows, itemRow("+ "+v, '+'))
			}
			for _, v := range f.Removed {
				rows = append(rows, itemRow("- "+v, '-'))
			}
			if len(f.Kept) > 0 {
				rows = append(rows, itemRow(tr(tApprovalUnchangedCount, len(f.Kept)), 'k'))
				if expanded {
					for _, v := range f.Kept {
						rows = append(rows, itemRow("  "+v, 'k'))
					}
				}
			}
		case ok && f.CredentialsChanged:
			rows = append(rows, fieldRow(field, tr(tApprovalCredentialsChanged)))
		case ok && f.IsRename:
			rows = append(rows, fieldRow(field, tr(tApprovalValueRenamed, f.Before, f.After)))
		case ok:
			rows = append(rows, fieldRow(field, f.Before+" -> "+f.After))
		case c.New:
			rows = append(rows, fieldRow(field, current[field]))
		default:
			rows = append(rows, fieldRow(field, tr(tApprovalValueUnchanged)))
		}
	}
	return rows
}

// approvalCounts is the one-line summary of a list change: "2 new, 1 removed".
func approvalCounts(f approval.FieldChange) string {
	var parts []string
	if len(f.Renamed) > 0 {
		parts = append(parts, tr(tApprovalCountRenamed, len(f.Renamed)))
	}
	if len(f.Added) > 0 {
		parts = append(parts, tr(tApprovalCountNew, len(f.Added)))
	}
	if len(f.Removed) > 0 {
		parts = append(parts, tr(tApprovalCountRemoved, len(f.Removed)))
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
		return tr(tApprovalNone)
	}
	return strings.Join(values, " ")
}

func approvalTools(values []string) string {
	if values == nil {
		return tr(tApprovalEveryTool)
	}
	return approvalList(values)
}

// approvalDetailView draws the Approvals detail screen: what would change if the named connection were
// approved now.
func (m *Model) approvalDetailView() string {
	name := m.approvalDetail
	heading := tr(tApprovalTitle, name)
	title := m.wrapped(styles.title, heading) + "\n\n"
	report, unavailable := m.approvalsReport()
	if unavailable != "" {
		// The vault changed under the open detail screen (locked, or turned unencrypted, elsewhere): there
		// is nothing left here to show or approve.
		return title + m.wrapped(styles.fail, tr(tNoteError, unavailable)) + m.keyHint("{back} back · {command} cmds · {help} help")
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
		return title + m.wrapped(styles.hint, tr(tApprovalClosed)) +
			m.keyHint("{back} back · {command} cmds · {help} help")
	}
	return m.approvalChangeView(heading, *change)
}

// approvalMark is the state of an open change: the mark with its word and, where the frame leaves room, what
// is open about it.
func (m *Model) approvalMark(change approval.Change) string {
	mark := statusMark(statusWarn, tr(tStatusOpen))
	if _, framed := m.detailInner(); !framed {
		return mark
	}
	return mark + "  " + styles.hint.Render(approvalSummary(change))
}

// approvalFacts are the rows of the detail screen as facts: the state of the change, where it came from, and
// one row per field. What a list change newly asks for stands in the colour of a gain and leads with "+", what
// falls away in the colour of a loss and leads with "-", so the change reads without colour too.
func (m *Model) approvalFacts(change approval.Change) []styledFact {
	facts := m.statusFact(m.approvalMark(change))
	facts = append(facts, m.approvalOriginFacts(change.Connection)...)
	for _, row := range approvalBody(change, m.approvalExpanded) {
		f := styledFact{detailFact: detailFact{label: row.label, value: m.logText(row.value)}}
		switch row.tone {
		case '+':
			f.style = styles.ok
		case '-':
			f.style = styles.fail
		case 'k':
			f.style = styles.hint
		}
		facts = append(facts, f)
	}
	return facts
}

// approvalChangeView is the title and body of the detail screen for one change. The whole body is laid out
// first and shown from m.approvalOffset on, so a scope of any length fits the workspace and only the lines
// scroll; nothing is cut.
func (m *Model) approvalChangeView(heading string, change approval.Change) string {
	inner, _ := m.detailInner()
	facts := m.approvalFacts(change)
	lines := factLinesIn(facts, inner, factColumn(facts, inner), func(s string) string { return s })
	keys := "o logs · y approve · n/{back} back, stays open · {command} cmds · {help} help"
	if len(lines) > 0 && approvalHasKept(change) {
		keys = "e unchanged · " + keys
		if m.approvalExpanded {
			keys = "e fold unchanged · o logs · y approve · n/{back} back, stays open · {command} cmds · {help} help"
		}
	}
	room := m.approvalRoom(keys, len(lines))
	offset := max(min(m.approvalOffset, len(lines)-room), 0)
	m.approvalOffset, m.approvalPage = offset, room
	foot := m.keyHint(keys)
	if len(lines) > room {
		foot = "\n" + m.wrapped(styles.hint, tr(tApprovalLines, offset+1,
			min(offset+room, len(lines)), len(lines))) + m.keyHint("up/down scroll · "+keys)
	}
	return m.frameDetail(heading, m.approvalMark(change), lines[offset:min(offset+room, len(lines))]) + "\n" + foot
}

// approvalRoom is how many body lines of the detail screen fit around its frame and above its keys; when the
// body is longer, one of them goes to the position line and the keys gain the scroll keys.
func (m *Model) approvalRoom(keys string, lines int) int {
	room := m.height - detailChrome - strings.Count(m.keyHint(keys), "\n") - 1
	if lines > room {
		longer := m.keyHint("up/down scroll · " + keys)
		room = m.height - detailChrome - strings.Count(longer, "\n") - 2
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
	q := question{
		title:       tr(tApproveAllTitle, len(report.Open)),
		consequence: tr(tApproveAllConsequence),
		keys:        "y approve all · n/{back} cancel · {command} cmds · {help} help",
	}
	if stale := report.RevocableStale(); len(stale) > 0 {
		q.details = append(q.details, m.indented(tr(tApproveAllStale, len(stale))))
	}
	if n := len(report.HandRenames); n > 0 {
		q.details = append(q.details, m.indented(tr(tApproveAllHand, n)))
	}
	return m.questionText(q)
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
		m.status = tr(tVaultWriteBusy)
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	cfg := m.cfg
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyApprovingOne, name)
	return func() tea.Msg {
		approved, warning, err := approval.Approve(context.Background(), cfg, v, []string{name})
		return approvalActionMsg{name: name, approved: approved, warning: warning, err: err}
	}
}

// approveAll approves every connection the Approvals section currently lists open, and, like
// 'qatlas vault approve' with no --connection, also removes every stale approval it finds: one left over
// from a connection that was deleted or renamed outside a delete this editor itself ran, which already
// revokes it on its own (see revokeApproval). An approval of a rename made by hand stays, so that adopting
// the rename still finds it (see approval.Report.RevocableStale).
func (m *Model) approveAll() tea.Cmd {
	if m.vaultBusy {
		m.status = tr(tVaultWriteBusy)
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	cfg := m.cfg
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyApprovingAll)
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
		if stale := report.RevocableStale(); len(stale) > 0 {
			var staleWarning string
			removed, staleWarning, err = approval.Revoke(context.Background(), v, stale)
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
		m.status = tr(tApprovedOne, msg.name)
	case len(msg.approved) == 0:
		m.status = tr(tApprovedNothing)
	default:
		m.status = tr(tApprovedMany, len(msg.approved))
	}
	if msg.staleRemoved > 0 {
		m.status += tr(tApprovedStaleRemoved, msg.staleRemoved)
	}
	if msg.warning != "" {
		m.status += tr(tNoteWarning, msg.warning)
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
	m.busy = tr(tBusyApprovalCheck)
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
		note = tr(tApprovalForwardOpen, res.StayedOpen, sectionApprovals.ref())
	} else if res.StayedOpen != "" {
		note = tr(tApprovalOutsideOpen, res.StayedOpen, sectionApprovals.ref())
	}
	switch {
	case res.ApproveErr != nil:
		return note + tr(tNoteApproveFailed, redactor.Apply(res.ApproveErr.Error()))
	case res.Warning != "":
		return note + tr(tNoteWarning, res.Warning)
	case len(res.Approved) > 0:
		return note + tr(tNoteApproved, len(res.Approved), strings.Join(res.Approved, ", "))
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
	m.busy = tr(tBusyRemovingApproval, name)
	return func() tea.Msg {
		_, warning, err := approval.Revoke(context.Background(), v, []string{name})
		switch {
		case err != nil:
			if errors.Is(err, vault.ErrNotEncrypted) || errors.Is(err, vault.ErrNotUnlocked) {
				return approvalSweepMsg{}
			}
			return approvalSweepMsg{note: tr(tNoteRevokeFailed, name, redactor.Apply(err.Error()))}
		case warning != "":
			return approvalSweepMsg{note: tr(tNoteWarning, warning)}
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

// approvalOriginFacts is the "source" row of the detail screen, made safe to print; a change made outside
// qatlas adds a second row that says what that can mean.
func (m *Model) approvalOriginFacts(name string) []styledFact {
	if m.approvalOriginFor != name {
		return nil
	}
	if !m.approvalOriginReady {
		return []styledFact{{detailFact: detailFact{label: tr(tApprovalSource), value: tr(tApprovalLookingUp)}}}
	}
	facts := []styledFact{{detailFact: detailFact{label: tr(tApprovalSource), value: m.logText(m.approvalOrigin.Text())}}}
	if m.approvalOrigin.Source == approval.OriginOutside {
		facts = append(facts, styledFact{detailFact: detailFact{value: tr(tApprovalOutsideEdit)}, style: styles.warning})
	}
	return facts
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

// renameFindings are the renames the vault shows that the configuration does not: a connection or credential
// renamed by hand and the rest of a rename that stopped early. Finding them is the core's
// (manage.RenameFindings), which reads the vault only where that needs no passphrase.
func (m *Model) renameFindings() []renameFinding {
	var out []renameFinding
	for _, f := range manage.RenameFindings(m.cfg, m.secrets.Vault()) {
		out = append(out, renameFinding{kind: f.Kind, entry: f.Entry, from: f.From, to: f.To,
			leftover: f.Leftover, ambiguous: f.Ambiguous, carried: f.Carried})
	}
	return out
}
