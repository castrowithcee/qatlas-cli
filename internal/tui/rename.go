package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Renaming a service, a credential or a connection is r on its list. The editor only asks for the new
// name, shows the dry run manage.Service.PlanRename returns, and hands the confirmed plan to
// manage.Service.Rename; every check and every write is the core's. Both steps are managing actions, so
// they are gated behind the admin session like every other one.

// renameState is the rename in progress: the name being typed, then the plan awaiting confirmation.
type renameState struct {
	kind, old string
	input     textinput.Model
	// plan is nil while the new name is typed and set while the dry run waits for the answer.
	plan *manage.RenamePlan
	// view scrolls a dry run longer than the screen.
	view textScroll
	// back is the screen a cancelled dry run returns to; screenFindings for a rename found in the vault (see
	// renamefind.go), the list otherwise.
	back screen
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

// askRename opens the name prompt for the selected entry, once the admin session allows it.
func (m *Model) askRename() tea.Cmd {
	kind := m.renameKind()
	name, ok := m.selected()
	if kind == "" || !ok {
		return nil
	}
	if work := m.updateBlocker(); work != "" {
		m.clearMessages()
		m.status = tr(tRenameWait, work)
		return nil
	}
	return m.requireAdmin(func() tea.Cmd {
		in := newTextInput()
		in.Prompt = ""
		in.SetValue(name)
		in.Focus()
		m.rename = &renameState{kind: kind, old: name, input: in}
		m.screen = screenRename
		m.clearMessages()
		return nil
	})
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
	if back == screenFindings {
		m.reopenFindings()
	}
	m.status = status
}

func (m *Model) updateRename(key tea.KeyPressMsg) tea.Cmd {
	r := m.rename
	if r == nil {
		m.screen = screenList
		return nil
	}
	if r.plan != nil {
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
	switch {
	case keyIs(key, actBack):
		m.cancelRename()
		return nil
	case keyIs(key, actEnter):
		return m.requireAdmin(m.planRename)
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(inputMsg(key))
	return cmd
}

// planRename asks the core for the dry run of the typed name.
func (m *Model) planRename() tea.Cmd {
	r := m.rename
	m.clearMessages()
	plan, err := m.svc.PlanRename(r.kind, r.old, r.input.Value(), true)
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	r.plan = plan
	r.view.reset()
	return nil
}

// runRename carries out the confirmed plan. The core refuses it when the configuration changed since the
// dry run.
func (m *Model) runRename() tea.Cmd {
	r := m.rename
	res, err := m.svc.Rename(context.Background(), r.plan)
	if err != nil {
		if m.conflicted(err) {
			m.rename = nil
			m.screen = screenList
			return nil
		}
		m.fail = m.redactor.Apply(err.Error())
		m.rename = nil
		m.screen = screenList
		return nil
	}
	plan := r.plan
	m.rename = nil
	if err := m.reloadConfig(); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		m.screen = screenList
		return nil
	}
	name := plan.New
	if plan.Revert {
		name = plan.Old
	}
	cmd := m.returnToList(name)
	m.status = m.renameResult(plan, res)
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

// renameView draws the name prompt, then the dry run with the question.
func (m *Model) renameView() string {
	r := m.rename
	if r == nil {
		return ""
	}
	apply := m.redactor.Apply
	if r.plan == nil {
		p := m.dialogPanelOf()
		return p.top(tr(tRenameTitle, kindText(r.kind), apply(r.old)), "") + p.blank() +
			p.styled(m.inputLineIn(p.room, "  ", r.input)) + p.hint(tr(tRenameNewName)) + p.bottom() +
			m.keyHint("{enter} dry run · {back} cancel") + m.notes()
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

// renameTyping reports whether the name prompt reads text.
func (m *Model) renameTyping() bool { return m.rename != nil && m.rename.plan == nil }
