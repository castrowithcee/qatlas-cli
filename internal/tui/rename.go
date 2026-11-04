package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-connect/internal/manage"
)

// Renaming a service, a credential or a connection happens in its form: the name row is editable, and saving
// a changed name first shows the dry run manage.Service.PlanRename returns. The confirmed plan goes to
// manage.Service.Rename; every check and every write is the core's. Both steps are managing actions, so
// they are gated behind the admin session like every other one. r on a list opens the form on the name row.

// renameState is the rename awaiting its answer: the dry run.
type renameState struct {
	kind, old string
	plan      *manage.RenamePlan
	// view scrolls a dry run longer than the screen.
	view textScroll
	// back is the screen a cancelled dry run returns to: the form it was asked from, screenFindings for a
	// rename found in the vault (see renamefind.go).
	back screen
	// rest says that the form holds changes beside the name, which are saved under the new name once the
	// rename went through.
	rest bool
}

// renameKind is the core's kind of the active section, or "" where its entries cannot be renamed.
func (m *Model) renameKind() string {
	switch m.section {
	case sectionServices:
		return manage.RenameService
	case sectionCredentials:
		return manage.RenameCredential
	case sectionConnections:
		return manage.RenameConnection
	}
	return ""
}

// openRename is r on a list: the form of the selected entry with the cursor on its name.
func (m *Model) openRename() tea.Cmd {
	if m.renameKind() == "" {
		return nil
	}
	if work := m.updateBlocker(); work != "" {
		m.clearMessages()
		m.status = tr(tRenameWait, work)
		return nil
	}
	cmd := m.editSelected()
	if m.screen != screenForm {
		return cmd
	}
	for i := range m.fields {
		if m.fields[i].label == "name" {
			m.focus = i
			m.applyFocus()
			break
		}
	}
	return cmd
}

// formRename is the new name of the open edit form when it differs from the entry's own: the rename that
// saving has to carry out first.
func (m *Model) formRename() (string, bool) {
	if m.editing == "" || m.wizard != nil || m.renameKind() == "" || len(m.fields) == 0 {
		return "", false
	}
	to := m.entryKey().value()
	return to, to != m.editing
}

// askFormRename checks the rest of the form without writing anything, then asks the core for the dry run of
// the rename and shows it. Nothing is written before it is confirmed, the other rows of the form included.
func (m *Model) askFormRename(to string) tea.Cmd {
	m.clearMessages()
	if work := m.updateBlocker(); work != "" {
		m.status = tr(tRenameWait, work)
		return nil
	}
	if !m.isPayloadForm() {
		// The payload form has checked its rows before it gets here.
		candidate := m.cfg.Clone()
		m.problems = problemState{}
		err := m.apply(candidate, m.editing)
		if err == nil {
			err = candidate.Validate()
		}
		if err != nil {
			m.reportSaveError(err, m.editing, true)
			return nil
		}
	}
	kind := m.renameKind()
	plan, err := m.svc.PlanRename(kind, m.editing, to, true)
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	name := m.entryKey().label
	m.rename = &renameState{kind: kind, old: m.editing, plan: plan, back: screenForm,
		rest: rowsOf(m.formState(), name, nil) != rowsOf(m.pristine, name, nil)}
	m.screen = screenRename
	return nil
}

// rowsOf is a form state (see formState) without the row label, or, with value set, with that row holding
// *value; a state that does not hold the row stays as it is.
func rowsOf(state, label string, value *string) string {
	parts := strings.Split(state, "\x00")
	var b strings.Builder
	for i := 0; i+1 < len(parts); i += 2 {
		switch {
		case parts[i] != label:
		case value == nil:
			continue
		default:
			parts[i+1] = *value
		}
		b.WriteString(parts[i] + "\x00" + parts[i+1] + "\x00")
	}
	return b.String()
}

// cancelRename leaves the rename screen with nothing changed.
func (m *Model) cancelRename() {
	status := tr(tRenameCancelled)
	back := screenList
	if r := m.rename; r != nil {
		back = r.back
		if r.plan != nil && r.plan.Adopt {
			status = tr(tRenameCancelledAdopt)
		} else if r.plan != nil && r.plan.Revert {
			status = tr(tRenameCancelledRevert)
		}
	}
	m.rename = nil
	m.screen = screenList
	switch back {
	case screenFindings:
		m.reopenFindings()
	case screenForm:
		// The form comes back with the typed values.
		m.screen = screenForm
	}
	m.status = status
}

func (m *Model) updateRename(key tea.KeyPressMsg) tea.Cmd {
	r := m.rename
	if r == nil || r.plan == nil {
		m.screen = screenList
		return nil
	}
	switch {
	case keyIs(key, actYes):
		return m.requireAdmin(m.runRename)
	case keyIs(key, actNo) || keyIs(key, actBack):
		m.cancelRename()
	default:
		r.view.scroll(key, halfPage(m.height))
	}
	return nil
}

// runRename carries out the confirmed plan. The core refuses it when the configuration changed since the
// dry run. Asked from a form, it then saves the other changes of that form under the new name.
func (m *Model) runRename() tea.Cmd {
	r := m.rename
	fromForm := r.back == screenForm
	failed := func() {
		m.rename = nil
		m.screen = screenList
		if fromForm {
			m.screen = screenForm
		}
	}
	res, err := m.svc.Rename(context.Background(), r.plan)
	if err != nil {
		if !m.conflicted(err) {
			m.fail = m.redactor.Apply(err.Error())
		}
		failed()
		return nil
	}
	plan, rest := r.plan, r.rest
	m.rename = nil
	if err := m.reloadConfig(); err != nil {
		m.rev = staleRevision
		m.fail = m.redactor.Apply(err.Error())
		m.screen = screenList
		if fromForm {
			return m.showList()
		}
		return nil
	}
	name := plan.New
	if plan.Revert {
		name = plan.Old
	}
	status := m.renameResult(plan, res)
	if !fromForm || !rest {
		cmd := m.returnToList(name)
		m.status = status
		return cmd
	}
	// The form is the entry under its new name now; its other rows are saved against it.
	m.editing = name
	m.screen = screenForm
	label := m.entryKey().label
	m.pristine = rowsOf(m.pristine, label, &name)
	var cmd tea.Cmd
	if m.isPayloadForm() {
		cmd = m.savePayload()
		if m.fail != "" || m.payloadSaving {
			m.status = status
		}
		return cmd
	}
	cmd = m.save(name)
	switch {
	case m.fail != "":
		// The rename stands even though the rest was refused.
		m.status = status
	case m.status != "":
		m.status = status + "; " + m.status
	}
	return cmd
}

// renameResult is the status line after a rename: what was done and what the person has to know about.
func (m *Model) renameResult(plan *manage.RenamePlan, res manage.RenameResult) string {
	apply := m.redactor.Apply
	parts := []string{tr(tRenamed, kindText(plan.Kind), apply(plan.Old), apply(plan.New))}
	carried := tRenameCarriedOver
	switch {
	case plan.Revert:
		parts = []string{tr(tRenameReverted, apply(plan.Old), apply(plan.New))}
		carried = tRenameCarriedBack
	case plan.Adopt:
		parts = []string{tr(tRenameAdopted, kindText(plan.Kind), apply(plan.Old), apply(plan.New))}
	}
	if len(res.VaultCopies) > 0 {
		parts = append(parts, tr(tRenameVaultFiles, strings.Join(res.VaultCopies, ", ")))
	}
	if len(res.Carried) > 0 {
		parts = append(parts, tr(carried, apply(strings.Join(res.Carried, ", "))))
	}
	if len(res.EnvVars) > 0 {
		pairs := make([]string, len(res.EnvVars))
		for i, env := range res.EnvVars {
			pairs[i] = env.Old + " -> " + env.New
		}
		parts = append(parts, tr(tRenameEnvVars, apply(strings.Join(pairs, ", "))))
	}
	for _, warning := range res.Warnings {
		parts = append(parts, tr(tWarningPrefix, apply(warning)))
	}
	return strings.Join(parts, "; ")
}

// renameRow is one line of the dry run: its label, padded to ten cells, then the text.
func renameRow(label textID, text string) string { return fmt.Sprintf("%-10s%s", tr(label), text) }

// renameLines are the places the dry run changes or leaves alone, one per line, in the order of
// 'qatlas config rename'. A line that starts with the warning label is drawn as one.
func (m *Model) renameLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	if plan.Revert {
		return m.revertLines(plan)
	}
	lines := []string{renameRow(tRenameLabelID, tr(tRenameIDKept, plan.ID))}
	if plan.Adopt {
		// The configuration carries the new name already and is written as it is.
		lines = append(lines, renameRow(tRenameLabelConfig, tr(tRenameConfigThere, plan.Kind, apply(plan.New))))
		for _, ref := range plan.References {
			lines = append(lines, renameRow(tRenameLabelChanged, tr(tRenameByHand, apply(ref))))
		}
	} else {
		lines = append(lines, renameRow(tRenameLabelEntry, fmt.Sprintf("%ss.%s -> %ss.%s", plan.Kind,
			apply(plan.Old), plan.Kind, apply(plan.New))))
		for _, ref := range plan.References {
			lines = append(lines, renameRow(tRenameLabelRewrites, apply(ref)))
		}
	}
	switch plan.Kind {
	case manage.RenameConnection:
		lines = append(lines, renameRow(tRenameLabelApproval, m.renameApprovalText(plan)))
	case manage.RenameCredential:
		lines = append(lines, m.renameCredentialLines(plan)...)
	}
	for _, token := range plan.LapsedTokens {
		if plan.Kind == manage.RenameCredential {
			lines = append(lines, renameRow(tRenameLabelWarning, tr(tRenameTokenCredential, apply(token), apply(plan.Old))))
			continue
		}
		lines = append(lines, renameRow(tRenameLabelWarning, tr(tRenameTokenConnection, apply(token), apply(plan.Old))))
	}
	for _, outside := range plan.Outside {
		lines = append(lines, renameRow(tRenameLabelOutside, tr(tRenameOutside, apply(outside))))
	}
	return lines
}

// revertLines are the places putting back a credential's stopped rename changes, in the order of 'qatlas
// config rename credential --revert'.
func (m *Model) revertLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	lines := []string{renameRow(tRenameLabelID, plan.ID)}
	switch {
	case plan.VaultUnread:
		lines = append(lines, renameRow(tRenameLabelVault, tr(tRenameVaultUnread)))
	case plan.VaultEntry:
		lines = append(lines, renameRow(tRenameLabelVault, tr(tRenameVaultEntry, apply(plan.New), apply(plan.Old))))
	}
	if plan.PendingWrites > 0 {
		lines = append(lines, renameRow(tRenameLabelPending, tr(tRenamePendingWrites, plan.PendingWrites, apply(plan.New))))
	}
	for _, role := range plan.KeyringRoles {
		lines = append(lines, renameRow(tRenameLabelKeyring, tr(tRenameKeyringRemoves, apply(plan.New), role,
			apply(plan.Old), role)))
	}
	for _, reader := range plan.Readers {
		text := tr(tRenameNoApproval)
		switch {
		case reader.Carried:
			text = tr(tRenameApprovalBack, apply(plan.Old))
		case plan.VaultUnread:
			text = tr(tRenameApprovalUnread)
		case reader.Approval == manage.ApprovalValid:
			text = tr(tRenameApprovedStays)
		case reader.Approval == manage.ApprovalOpen:
			text = tr(tRenameOpenStays)
		}
		lines = append(lines, renameRow(tRenameLabelReader, apply(reader.Connection)+": "+text))
	}
	return append(lines, renameRow(tRenameLabelConfig, tr(tRenameConfigUnchanged)))
}

func (m *Model) renameCredentialLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	var lines []string
	if plan.Resumes {
		lines = append(lines, renameRow(tRenameLabelResumes, tr(tRenameResumes, apply(plan.New))))
	}
	for _, role := range plan.KeyringRoles {
		lines = append(lines, renameRow(tRenameLabelKeyring, fmt.Sprintf("%s/%s -> %s/%s", apply(plan.Old), role,
			apply(plan.New), role)))
	}
	switch {
	case plan.VaultUnread:
		lines = append(lines, renameRow(tRenameLabelVault, tr(tRenameVaultUnread)))
	case plan.VaultEntry:
		lines = append(lines, renameRow(tRenameLabelVault, tr(tRenameVaultEntry, apply(plan.Old), apply(plan.New))))
	}
	if plan.PendingWrites > 0 {
		lines = append(lines, renameRow(tRenameLabelPending, tr(tRenamePendingWrites, plan.PendingWrites, apply(plan.Old))))
	}
	for _, reader := range plan.Readers {
		text := m.renameReaderText(reader)
		if plan.VaultUnread {
			text = tr(tRenameApprovalUnread)
		}
		lines = append(lines, renameRow(tRenameLabelReader, apply(reader.Connection)+": "+text))
	}
	for _, env := range plan.EnvVars {
		lines = append(lines, renameRow(tRenameLabelEnv, fmt.Sprintf("%s -> %s (%s)", apply(env.Old), apply(env.New), env.Role)))
	}
	return lines
}

func (m *Model) renameReaderText(reader manage.RenameReader) string {
	name := m.redactor.Apply(reader.Connection)
	switch {
	case reader.Approval == "":
		return tr(tRenameNoApprovalToCarry)
	case reader.Carried:
		return tr(tRenameCarriedApproved, name)
	case reader.Approval == manage.ApprovalOpen:
		return tr(tRenameOpenStays)
	}
	return tr(tRenameNotCarried, tr(tRenameApprovedWord), name)
}

// renameApprovalWord is the core's approval state in the editor's language; a state it does not know stays.
func renameApprovalWord(state string) string {
	switch state {
	case manage.ApprovalValid:
		return tr(tRenameApprovedWord)
	case manage.ApprovalOpen:
		return tr(tRenameOpenWord)
	}
	return state
}

func (m *Model) renameApprovalText(plan *manage.RenamePlan) string {
	name := m.redactor.Apply(plan.New)
	switch {
	case plan.VaultUnread:
		return tr(tRenameVaultUnread)
	case plan.Approval == "":
		return tr(tRenameNoneToCarry)
	case plan.CarryApproval && plan.Approval == manage.ApprovalOpen:
		return tr(tRenameOpenCarried, name)
	case plan.CarryApproval:
		return tr(tRenameCarriedApproved, name)
	}
	return tr(tRenameNotCarried, renameApprovalWord(plan.Approval), name)
}

// renameView draws the dry run with the question.
func (m *Model) renameView() string {
	r := m.rename
	if r == nil || r.plan == nil {
		return ""
	}
	// What does not fit scrolls between the sentence and the keys; the modal shows the whole plan or, when
	// that is too tall, leaves the room to this full screen.
	notes := m.notes()
	text := m.framedText(func() string {
		q := m.renameQuestion()
		all := q.details
		q.details = nil
		room := max(m.height-strings.Count(m.questionText(q), "\n")-strings.Count(notes, "\n")-2-m.framedChrome(), 3)
		q.details = r.view.window(all, room, false)
		return m.questionText(q)
	})
	return text + notes
}

// renameQuestion is the dry run as a question; its details are every line of the plan, one row each.
func (m *Model) renameQuestion() question {
	r := m.rename
	apply := m.redactor.Apply
	title, confirm := tr(tRenameQuestion, kindText(r.kind), apply(r.old), apply(r.plan.New)), "{yes} rename"
	switch {
	case r.plan.Revert:
		title = tr(tRenameRevertQuestion, apply(r.old), apply(r.plan.New))
		confirm = "{yes} put back"
	case r.plan.Adopt:
		title = tr(tRenameAdoptQuestion, kindText(r.kind), apply(r.old), apply(r.plan.New))
		confirm = "{yes} adopt"
	}
	consequence := tr(tRenameDryRun)
	if n := len(r.plan.LapsedTokens); n > 0 {
		consequence += tr(tRenameLapsedTokens, n)
	}
	q := question{title: title, consequence: consequence,
		keys: confirm + " · {no}/{back} cancel · {up}/{down} scroll · {command} cmds · {help} help"}
	for _, line := range m.renameLines(r.plan) {
		style := styles.hint
		if strings.HasPrefix(line, tr(tRenameLabelWarning)) {
			style = styles.warning
		}
		q.details = append(q.details, strings.Split(m.wrapped(style, line), "\n")...)
	}
	return q
}
