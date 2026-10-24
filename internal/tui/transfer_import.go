package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Importing a file is ':import', on the same screen as the export. The editor only asks which file, its
// export passphrase, and the mode; the reading, the plan, the writing, and the approvals are manage.Service's.
// Like the export it is a managing action behind the admin session, and the plan is shown whole before
// anything is written.

// importStep is the question an import is at.
type importStep int

const (
	importFile importStep = iota
	importPassphrase
	importMode
	importPreview
	importResult
	importApprove
	importDelete
)

// importModeRows are the answers of the mode step.
var importModeRows = []struct {
	mode manage.ImportMode
	text string
}{
	{manage.ImportModeCreate, "create: stop if this installation already has a name from the file"},
	{manage.ImportModeMerge, "merge: add what is missing, keep the existing entries"},
	{manage.ImportModeOverwrite, "overwrite: replace the existing entries with the file's (they keep their ids)"},
}

// importPlanStage is why a plan is made.
type importPlanStage int

const (
	// planProbe reads the file with create mode to learn whether it needs a passphrase and whether it can be
	// read at all; its plan is dropped.
	planProbe importPlanStage = iota
	// planFinal makes the plan of the chosen mode that the preview shows.
	planFinal
)

// errImportNeedsPassphrase is what the probe's passphrase function answers, so an encrypted file is found
// by the core and not by a check of the editor's own.
var errImportNeedsPassphrase = errors.New("the file is encrypted and needs its export passphrase")

// importState is the import in progress, while screenTransfer shows it.
type importState struct {
	step importStep
	// from is the screen the import was started from and returns to.
	from screen
	// input is the file path on importFile and the masked export passphrase on importPassphrase.
	input pathField
	// path is the file as asked for; passphrase is the typed export passphrase. It only ever reaches the
	// core's passphrase function and is cleared when the import ends.
	path, passphrase string
	encrypted        bool
	modeCursor       int
	// plan is what the preview shows, and carries the secret values of the file until the import ends.
	plan  *manage.ImportPlan
	store string
	// lines and offset are the preview or the result, one line each, and the first one shown.
	lines  []string
	offset int
	// working says why no key acts: "reading" the file or "writing" the import or approvals.
	working string
	// res is what the write did, for the approval; the report is what the vault holds open afterwards.
	res    manage.ImportResult
	report manage.ImportApproval
	// toApprove lists the connections the question approves; the flags are the questions still to ask.
	toApprove                     []string
	hasSecrets, sourceEncrypted   bool
	pendingApprove, pendingDelete bool
}

// importPlannedMsg carries the outcome of a planning back into the event loop.
type importPlannedMsg struct {
	plan  *manage.ImportPlan
	err   error
	stage importPlanStage
}

// importDoneMsg carries the outcome of the write, and what the vault holds open for the imported connections.
type importDoneMsg struct {
	res      manage.ImportResult
	err      error
	report   manage.ImportApproval
	reported bool
}

// importApprovedMsg carries the outcome of approving the imported connections.
type importApprovedMsg struct{ report manage.ImportApproval }

// askImport starts the import, once the admin session allows it.
func (m *Model) askImport() tea.Cmd {
	if work := m.updateBlocker(); work != "" {
		m.clearMessages()
		m.status = "Wait until " + work + " finishes, then run import again."
		return nil
	}
	return m.requireAdmin(func() tea.Cmd { m.openImport(); return nil })
}

func (m *Model) openImport() {
	m.imp = &importState{from: m.screen}
	m.askImportFile()
	m.screen = screenTransfer
	m.clearMessages()
}

// askImportFile shows the path prompt, keeping what was typed.
func (m *Model) askImportFile() {
	e := m.imp
	e.step = importFile
	e.encrypted, e.passphrase = false, ""
	e.input = newPathField(pathFile)
	e.input.open(e.path, m.startDir)
}

// askImportPassphrase shows the empty masked export passphrase prompt.
func (m *Model) askImportPassphrase() {
	e := m.imp
	e.step = importPassphrase
	e.input = pathField{Model: maskedInput()}
	e.passphrase = ""
}

// closeImport leaves the import, dropping everything typed into it and the secrets of the plan.
func (m *Model) closeImport() {
	e := m.imp
	m.imp = nil
	m.screen = screenList
	if e != nil {
		e.input.Reset()
		e.passphrase, e.plan = "", nil
		m.screen = e.from
	}
}

// cancelImport ends the import without a change.
func (m *Model) cancelImport() {
	m.closeImport()
	m.status = "Cancelled; nothing was imported"
}

// dirty reports whether leaving now would drop a typed file name or answers; a written import loses nothing.
func (e *importState) dirty() bool {
	if e == nil || e.step >= importResult {
		return false
	}
	return e.step != importFile || e.input.Value() != ""
}

func (m *Model) updateTransfer(key tea.KeyMsg) tea.Cmd {
	if m.imp != nil {
		return m.updateImport(key)
	}
	return m.updateExport(key)
}

func (m *Model) updateImport(key tea.KeyMsg) tea.Cmd {
	e := m.imp
	if e == nil {
		m.screen = screenList
		return nil
	}
	if e.working != "" {
		return nil
	}
	if key.String() == keyOf(actBack) {
		return m.importBack()
	}
	if key.String() == keyOf(actSave) {
		switch e.step {
		case importFile, importPassphrase, importMode, importResult:
			// F2 goes on like enter, which also confirms the one-line dialogs.
			key = tea.KeyMsg{Type: tea.KeyEnter}
		}
	}
	switch e.step {
	case importFile:
		return m.updateImportFile(key)
	case importPassphrase:
		return m.updateImportPassphrase(key)
	case importMode:
		return m.updateImportMode(key)
	case importPreview:
		switch key.String() {
		case keyOf(actSave):
			m.clearMessages()
			return m.requireAdmin(m.beginImportWrite)
		default:
			e.offset = scrollOffset(e.offset, key.String(), m.height)
		}
	case importResult:
		if key.String() == "enter" {
			m.importNext()
			return nil
		}
		e.offset = scrollOffset(e.offset, key.String(), m.height)
	case importApprove:
		switch key.String() {
		case "y":
			return m.runImportApprove()
		case "n":
			m.declineImportApproval()
		default:
			e.offset = scrollOffset(e.offset, key.String(), m.height)
		}
	case importDelete:
		switch key.String() {
		case "y":
			m.deleteImportSource(true)
		case "n":
			m.deleteImportSource(false)
		default:
			e.offset = scrollOffset(e.offset, key.String(), m.height)
		}
	}
	return nil
}

// importBack is esc: one step back, and on the first step the way out, asking first once a file name is
// typed. Nothing is written before F2 on the preview; after the write esc answers the questions with no.
func (m *Model) importBack() tea.Cmd {
	e := m.imp
	m.clearMessages()
	switch e.step {
	case importFile:
		if e.input.Value() != "" {
			m.askLeave(false)
			return nil
		}
		m.cancelImport()
	case importPassphrase:
		e.input.Reset()
		m.askImportFile()
	case importMode:
		if e.encrypted {
			m.askImportPassphrase()
		} else {
			m.askImportFile()
		}
	case importPreview:
		e.plan, e.lines = nil, nil
		e.step = importMode
	case importResult:
		m.importNext()
	case importApprove:
		m.declineImportApproval()
	case importDelete:
		m.deleteImportSource(false)
	}
	return nil
}

func (m *Model) updateImportFile(key tea.KeyMsg) tea.Cmd {
	e := m.imp
	if key.String() != "enter" {
		return e.input.update(key)
	}
	m.clearMessages()
	if !e.input.accept() {
		return nil
	}
	path := strings.TrimSpace(e.input.Value())
	if path == "" {
		m.fail = "name the file to import"
		return nil
	}
	e.path = absolutePath(path)
	return m.planImport(manage.ImportModeCreate, planProbe)
}

func (m *Model) updateImportPassphrase(key tea.KeyMsg) tea.Cmd {
	e := m.imp
	if key.String() != "enter" {
		return e.input.update(key)
	}
	typed := e.input.Value()
	e.input.Reset()
	m.clearMessages()
	if typed == "" {
		m.fail = "an export passphrase must not be empty"
		return nil
	}
	e.passphrase = typed
	m.redactor.Add(typed)
	return m.planImport(manage.ImportModeCreate, planProbe)
}

func (m *Model) updateImportMode(key tea.KeyMsg) tea.Cmd {
	e := m.imp
	switch key.String() {
	case "up":
		e.modeCursor = wrap(e.modeCursor-1, len(importModeRows))
	case "down":
		e.modeCursor = wrap(e.modeCursor+1, len(importModeRows))
	case "enter":
		m.clearMessages()
		return m.planImport(importModeRows[e.modeCursor].mode, planFinal)
	}
	return nil
}

// planImport makes the plan as a command: an encrypted file is opened with scrypt, which must not block the
// event loop. Nothing is written.
func (m *Model) planImport(mode manage.ImportMode, stage importPlanStage) tea.Cmd {
	e := m.imp
	e.working = "reading"
	m.busy = "reading the import file"
	path, passphrase, svc := e.path, e.passphrase, m.svc
	return func() tea.Msg {
		ask := func(string) (string, error) {
			if passphrase == "" {
				return "", errImportNeedsPassphrase
			}
			return passphrase, nil
		}
		plan, err := svc.PlanImportFile(path, mode, ask)
		return importPlannedMsg{plan: plan, err: err, stage: stage}
	}
}

func (m *Model) handleImportPlanned(msg importPlannedMsg) tea.Cmd {
	m.busy = ""
	e := m.imp
	if e == nil {
		return nil
	}
	e.working = ""
	var exists *manage.ImportExistsError
	existsErr := errors.As(msg.err, &exists)
	switch {
	case msg.stage == planFinal && msg.err == nil:
		e.plan = msg.plan
		e.hasSecrets = len(msg.plan.SecretRoles) > 0
		e.sourceEncrypted = msg.plan.SourceEncrypted
		store, overSSH, err := msg.plan.SecretStoreFor("")
		if err != nil {
			e.plan = nil
			m.fail = m.redactor.Apply(err.Error())
			return nil
		}
		e.store = store
		e.offset = 0
		e.lines = m.importPlanLines(msg.plan, store, overSSH)
		e.step = importPreview
	case errors.Is(msg.err, errImportNeedsPassphrase):
		e.encrypted = true
		m.askImportPassphrase()
	case msg.stage == planProbe && (msg.err == nil || existsErr):
		// Readable; the mode decides the rest.
		e.input.Reset()
		e.step = importMode
	case e.step == importPassphrase:
		// Mostly a wrong passphrase, which the core words; the file was not read, so nothing changed.
		e.passphrase = ""
		m.askImportPassphrase()
		m.fail = m.redactor.Apply(msg.err.Error()) + "; nothing was changed"
	default:
		m.fail = m.redactor.Apply(msg.err.Error())
	}
	return nil
}

// importPlanLines are the preview, one line each, in the order of 'qatlas config import'. A line that starts
// with "warning" is drawn as one. They name credentials and roles, never a value.
func (m *Model) importPlanLines(plan *manage.ImportPlan, store string, overSSH bool) []string {
	apply := m.redactor.Apply
	names := func(list []string) string { return apply(strings.Join(list, ", ")) }
	lines := []string{"file      " + apply(plan.Source), "mode      " + string(plan.Mode)}
	for _, e := range plan.Entries {
		line := fmt.Sprintf("%-8s  %s %s", e.Action, e.Section, apply(e.Name))
		if e.ID != "" {
			line += " (id " + e.ID
			switch {
			case e.NewID && e.Action == manage.ImportActionReplace:
				line += ", kept from this installation"
			case e.NewID:
				line += ", new"
			}
			line += ")"
		}
		lines = append(lines, line)
		if len(e.UsesTarget) > 0 {
			lines = append(lines, "          uses this installation's "+names(e.UsesTarget))
		}
		if len(e.UsedBy) > 0 {
			lines = append(lines, "          changes connections of this installation: "+names(e.UsedBy))
		}
		if len(e.Access) > 0 {
			lines = append(lines, "          brings local access from the file: "+strings.Join(e.Access, ", "))
		}
	}
	if len(plan.CredentialsWithoutSecret) > 0 {
		lines = append(lines, "without   secret here until one is set: "+names(plan.CredentialsWithoutSecret))
	}
	if len(plan.CredentialsKeepSecret) > 0 {
		lines = append(lines, "keeps     secret here stays as it is, it may not fit the replacement: "+
			names(plan.CredentialsKeepSecret))
	}
	if len(plan.SecretRoles) > 0 {
		for _, name := range slices.Sorted(maps.Keys(plan.SecretRoles)) {
			lines = append(lines, "secret    credential "+apply(name)+" brings roles: "+names(plan.SecretRoles[name]))
		}
		if plan.SourceEncrypted {
			lines = append(lines, "secrets   the file was encrypted; its secrets go to the "+store)
		} else {
			lines = append(lines, "warning   the file holds secrets in plain text; its secrets go to the "+store)
		}
		if overSSH {
			lines = append(lines, "warning   this is an SSH session, where the system keyring often cannot be unlocked; "+
				"if the write fails, choose the vault as the secret store in the defaults and import again")
		}
	}
	created, replaced, skipped := plan.Count()
	lines = append(lines, fmt.Sprintf("total     %d to create, %d to replace, %d to skip", created, replaced, skipped))
	if created+replaced == 0 {
		lines = append(lines, "nothing to import: the configuration already has everything")
	}
	return lines
}

// beginImportWrite runs after the admin check of the preview's F2. A vault that would get its very first
// secret offers a passphrase first, in the dialog every other first secret uses; declining it writes the
// secrets unencrypted, and esc leaves the preview as it was.
func (m *Model) beginImportWrite() tea.Cmd {
	e := m.imp
	if e == nil || e.plan == nil || e.working != "" {
		return nil
	}
	if created, replaced, _ := e.plan.Count(); created+replaced == 0 {
		m.fail = "nothing to import: the configuration already has everything"
		return nil
	}
	if e.hasSecrets && e.store == config.CredentialTypeVault {
		offerNeeded, err := manage.NeedsPassphraseOffer(m.secrets.Vault())
		if err != nil {
			m.fail = m.redactor.Apply(err.Error())
			return nil
		}
		if offerNeeded {
			m.openVaultOfferFor(
				"Set a passphrase for the vault?",
				vaultFirstSecretHint,
				true, true,
				func(offer func(string) (string, error)) tea.Cmd { m.screen = screenTransfer; return m.runImport(offer) },
				func() tea.Cmd { m.screen = screenTransfer; m.status = "Cancelled"; return nil },
			)
			return nil
		}
	}
	return m.runImport(nil)
}

// runImport writes the plan as a command: the secrets may reach the vault, whose first one does the
// passphrase work. The approvals the vault holds open are read in the same command.
func (m *Model) runImport(offer func(string) (string, error)) tea.Cmd {
	e := m.imp
	if e == nil || e.plan == nil || e.working != "" {
		return nil
	}
	e.working = "writing"
	m.busy = "writing the import"
	m.vaultBusy = e.hasSecrets && e.store == config.CredentialTypeVault
	plan, svc := e.plan, m.svc
	return func() tea.Msg {
		res, err := svc.ApplyImportWith(plan, manage.ImportOptions{Offer: offer})
		msg := importDoneMsg{res: res, err: err}
		if err == nil && len(res.Connections)+len(res.UsedBy) > 0 {
			msg.report = svc.ApproveImport(context.Background(), res, manage.ApprovalNone)
			msg.reported = true
		}
		return msg
	}
}

func (m *Model) handleImportDone(msg importDoneMsg) tea.Cmd {
	m.busy, m.vaultBusy = "", false
	e := m.imp
	if e == nil {
		return nil
	}
	e.working = ""
	if msg.err != nil {
		if errors.Is(msg.err, config.ErrConflict) {
			if err := m.reloadConfig(); err != nil {
				m.rev = staleRevision
			}
			e.plan, e.lines = nil, nil
			e.step = importMode
			m.fail = "nothing was written: " + m.redactor.Apply(msg.err.Error()) + "; plan the import again"
			return nil
		}
		m.fail = m.redactor.Apply(msg.err.Error())
		return nil
	}
	if err := m.reloadConfig(); err != nil {
		m.rev = staleRevision
	}
	plan := e.plan
	e.plan = nil
	e.res, e.report, e.offset = msg.res, msg.report, 0
	e.lines = m.importResultLines(plan, msg.res)
	if msg.reported {
		if hasImportedOpen(msg.report) {
			for _, o := range msg.report.Open {
				if o.Imported {
					e.toApprove = append(e.toApprove, "to approve: "+m.redactor.Apply(o.Connection)+", "+m.redactor.Apply(o.Reason))
				}
			}
			e.pendingApprove = true
		} else {
			e.lines = append(e.lines, m.importApprovalLines(msg.res, msg.report, false)...)
		}
	}
	e.pendingDelete = e.hasSecrets && !e.sourceEncrypted
	e.step = importResult
	m.clearMessages()
	return nil
}

// importResultLines are what the write did, in the order of 'qatlas config import'.
func (m *Model) importResultLines(plan *manage.ImportPlan, res manage.ImportResult) []string {
	apply := m.redactor.Apply
	names := func(list []string) string { return apply(strings.Join(list, ", ")) }
	lines := []string{fmt.Sprintf("result    created %d, replaced %d, skipped %d", res.Created, res.Replaced, res.Skipped)}
	if res.SecretStore != "" {
		lines = append(lines, "secrets   written to the "+res.SecretStore+" for: "+
			names(slices.Sorted(maps.Keys(plan.SecretRoles))))
	}
	if len(res.Withdrawn) > 0 {
		lines = append(lines, "withdrawn approvals withdrawn, they covered replaced secrets: "+names(res.Withdrawn))
	}
	if len(plan.CredentialsWithoutSecret) > 0 {
		lines = append(lines, "next      set a secret with 'qatlas credential set' before use: "+
			names(plan.CredentialsWithoutSecret))
	}
	for _, w := range res.Warnings {
		lines = append(lines, "warning   "+apply(w))
	}
	return lines
}

// hasImportedOpen reports whether the vault holds an imported connection open.
func hasImportedOpen(report manage.ImportApproval) bool {
	return slices.ContainsFunc(report.Open, func(o manage.ImportOpen) bool { return o.Imported })
}

// importApprovalLines say what approving did or why it did not, in the order of 'qatlas config import'.
// asked is true once the question was answered.
func (m *Model) importApprovalLines(res manage.ImportResult, report manage.ImportApproval, asked bool) []string {
	apply := m.redactor.Apply
	var lines []string
	for _, w := range report.Warnings {
		lines = append(lines, "warning   "+apply(w))
	}
	if report.Err != nil {
		lines = append(lines, "warning   could not check the approvals: "+apply(report.Err.Error()))
	}
	switch {
	case report.VaultUnencrypted:
		lines = append(lines, "approval  the vault is not encrypted; connections are not bound to approvals, so there is nothing to approve")
	case report.VaultLocked:
		lines = append(lines, "approval  the vault is locked; approve the connections with 'qatlas vault approve' after unlocking it")
	}
	required, _, err := manage.AdminRequired(m.secrets.Vault())
	encrypted := err == nil && required
	if len(res.Connections) > 0 && report.Err == nil && len(report.Open) == 0 && len(report.Approved) == 0 &&
		!report.VaultUnencrypted && !report.VaultLocked && !encrypted {
		lines = append(lines, "approval  new connections need the usual release; where the vault is encrypted, "+
			"run 'qatlas vault approve'")
	}
	if len(report.Approved) > 0 {
		lines = append(lines, "approved  "+apply(strings.Join(report.Approved, ", ")))
	}
	for _, o := range report.Open {
		who := "imported connection"
		if !o.Imported {
			who = "connection of this installation changed by the import"
		}
		lines = append(lines, fmt.Sprintf("open      %s %s, %s", who, apply(o.Connection), apply(o.Reason)))
	}
	if len(report.Open) > 0 {
		hint := "approve with 'qatlas vault approve'"
		if asked {
			hint = "nothing else was approved; " + hint
		}
		lines = append(lines, "hint      "+hint)
	}
	return lines
}

// importNext goes to the next question after the result, or ends the import when none is left.
func (m *Model) importNext() {
	e := m.imp
	m.clearMessages()
	e.offset = 0
	switch {
	case e.pendingApprove:
		e.pendingApprove = false
		e.step = importApprove
	case e.pendingDelete:
		e.pendingDelete = false
		e.step = importDelete
	default:
		res := e.res
		m.closeImport()
		m.status = fmt.Sprintf("Imported: created %d, replaced %d, skipped %d", res.Created, res.Replaced, res.Skipped)
	}
}

// runImportApprove approves the imported connections as a command: it writes to the vault and hands the
// approvals to a running vault process.
func (m *Model) runImportApprove() tea.Cmd {
	e := m.imp
	e.working = "writing"
	m.busy = "approving the imported connections"
	m.vaultBusy = true
	res, svc := e.res, m.svc
	return func() tea.Msg {
		return importApprovedMsg{report: svc.ApproveImport(context.Background(), res, manage.ApprovalDirectOnly)}
	}
}

func (m *Model) handleImportApproved(msg importApprovedMsg) tea.Cmd {
	m.busy, m.vaultBusy = "", false
	e := m.imp
	if e == nil {
		return nil
	}
	e.working = ""
	e.report = msg.report
	e.lines = append(e.lines, m.importApprovalLines(e.res, msg.report, true)...)
	e.toApprove = nil
	e.offset = 0
	e.step = importResult
	return nil
}

// declineImportApproval leaves the imported connections open, as the answer n does.
func (m *Model) declineImportApproval() {
	e := m.imp
	m.clearMessages()
	e.lines = append(e.lines, m.importApprovalLines(e.res, e.report, true)...)
	e.toApprove = nil
	e.offset = 0
	e.step = importResult
}

// deleteImportSource answers the question about the file with plain-text secrets: only an explicit y deletes.
func (m *Model) deleteImportSource(remove bool) {
	e := m.imp
	m.clearMessages()
	shown := m.redactor.Apply(e.path)
	switch {
	case !remove:
		e.lines = append(e.lines, "kept      "+shown+" holds secrets in plain text and was kept; delete it")
	default:
		if err := os.Remove(e.path); err != nil {
			e.lines = append(e.lines, "warning   "+shown+" could not be deleted: "+m.redactor.Apply(err.Error()))
		} else {
			e.lines = append(e.lines, "deleted   "+shown)
		}
	}
	e.offset = 0
	e.step = importResult
}

// importTyping reports whether the import reads text now.
func (m *Model) importTyping() bool {
	return m.imp != nil && m.imp.working == "" && (m.imp.step == importFile || m.imp.step == importPassphrase)
}

// importView draws the step the import is at.
func (m *Model) importView() string {
	e := m.imp
	if e == nil {
		return ""
	}
	switch e.step {
	case importFile:
		return m.wrapped(styles.title, "Import: file") + "\n\n" + m.pathLines("  ", &e.input) +
			m.indented("the file to import, as an export wrote it; nothing changes before you confirm the preview") + "\n" +
			m.importKeys(pathKeys(&e.input, "{save} or enter next · {back} cancel, nothing imported")) + m.notes()
	case importPassphrase:
		return m.wrapped(styles.title, "Export passphrase") + "\n\n" + m.inputLine("  ", e.input.Model) + "\n" +
			m.indented("the file is encrypted: type the passphrase it was exported with, not the vault passphrase") + "\n" +
			m.importKeys("{save} or enter ok · {back} back, nothing imported") + m.notes()
	case importMode:
		var b strings.Builder
		b.WriteString(m.wrapped(styles.title, "Import: mode") + "\n\n")
		for i, row := range importModeRows {
			b.WriteString(m.listRow(i == e.modeCursor, false, truncateCells(row.text, m.usable(2))) + "\n")
		}
		b.WriteString("\n" + m.indented("entries are matched by name; the preview shows what the mode does") + "\n")
		return b.String() + m.importKeys("up/down move · {save} or enter preview · {back} back · {command} cmds · {help} help") + m.notes()
	}
	title, question, keys := "Import preview", "Nothing is written until you confirm.", "{save} write · {back} back · up/down scroll · {command} cmds · {help} help"
	lines := e.lines
	switch e.step {
	case importPreview:
		if e.plan != nil {
			if created, replaced, _ := e.plan.Count(); created+replaced == 0 {
				keys = "{back} back · up/down scroll · {command} cmds · {help} help"
			}
		}
	case importResult:
		title, question, keys = "Import written", "", "enter or {back} done · up/down scroll · {command} cmds · {help} help"
		if e.pendingApprove || e.pendingDelete {
			keys = "enter, {save} or {back} next question · up/down scroll · {command} cmds · {help} help"
		}
	case importApprove:
		title, question, keys = "Import written", "Approve the imported connections now?",
			"y approve · n/{back} not now, nothing is approved · up/down scroll · {command} cmds · {help} help"
		lines = append(slices.Clone(lines), e.toApprove...)
	case importDelete:
		title, question, keys = "Import written", "Delete "+m.redactor.Apply(e.path)+", which holds secrets in plain text?",
			"y delete the file · n/{back} keep it · up/down scroll · {command} cmds · {help} help"
	}
	if e.working != "" {
		keys = e.working + ", please wait"
	}
	return m.linesView(title, question, keys, lines, &e.offset)
}

// importKeys is the footer of a step that has no lines to scroll.
func (m *Model) importKeys(keys string) string {
	if m.imp != nil && m.imp.working != "" {
		keys = m.imp.working + ", please wait"
	}
	return m.keyHint(keys)
}

// importLeaveView is the leave question over an unfinished import.
func (m *Model) importLeaveView() string {
	save := "{save} next step"
	if m.imp != nil && m.imp.step == importPreview {
		save = "{save} write"
	}
	discard, why := "d discard", "Leaving drops the typed file name and every answer, a typed passphrase included. Nothing was written."
	if m.leaveQuit {
		discard, why = "d discard and quit", "Quitting would drop them. Nothing was written."
	}
	keys := save + " · " + discard + " · {back} keep editing · {command} cmds · {help} help"
	return m.wrapped(styles.warning, "warning: the import is not finished") + m.keyHint(keys) + "\n\n" +
		m.wrapped(lipgloss.NewStyle(), why)
}
