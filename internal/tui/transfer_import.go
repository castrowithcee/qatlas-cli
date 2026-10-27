package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/i18n"
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
	text textID
}{
	{manage.ImportModeCreate, tImportModeCreate},
	{manage.ImportModeMerge, tImportModeMerge},
	{manage.ImportModeOverwrite, tImportModeOverwrite},
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
		m.status = tr(tImportWait, work)
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
	m.status = tr(tImportCancelled)
}

// dirty reports whether leaving now would drop a typed file name or answers; a written import loses nothing.
func (e *importState) dirty() bool {
	if e == nil || e.step >= importResult {
		return false
	}
	return e.step != importFile || e.input.Value() != ""
}

func (m *Model) updateTransfer(key tea.KeyPressMsg) tea.Cmd {
	if m.imp != nil {
		return m.updateImport(key)
	}
	return m.updateExport(key)
}

func (m *Model) updateImport(key tea.KeyPressMsg) tea.Cmd {
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
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
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
		switch {
		case keyIs(key, actSave):
			m.clearMessages()
			return m.requireAdmin(m.beginImportWrite)
		default:
			e.offset = scrollOffset(e.offset, key, m.height)
		}
	case importResult:
		if keyIs(key, actEnter) {
			m.importNext()
			return nil
		}
		e.offset = scrollOffset(e.offset, key, m.height)
	case importApprove:
		switch {
		case keyIs(key, actYes):
			return m.runImportApprove()
		case keyIs(key, actNo):
			m.declineImportApproval()
		default:
			e.offset = scrollOffset(e.offset, key, m.height)
		}
	case importDelete:
		switch {
		case keyIs(key, actYes):
			m.deleteImportSource(true)
		case keyIs(key, actNo):
			m.deleteImportSource(false)
		default:
			e.offset = scrollOffset(e.offset, key, m.height)
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

func (m *Model) updateImportFile(key tea.KeyPressMsg) tea.Cmd {
	e := m.imp
	if !keyIs(key, actEnter) {
		return e.input.update(key)
	}
	m.clearMessages()
	if !e.input.accept() {
		return nil
	}
	path := strings.TrimSpace(e.input.Value())
	if path == "" {
		m.fail = tr(tImportNameFile)
		return nil
	}
	e.path = absolutePath(path)
	return m.planImport(manage.ImportModeCreate, planProbe)
}

func (m *Model) updateImportPassphrase(key tea.KeyPressMsg) tea.Cmd {
	e := m.imp
	if !keyIs(key, actEnter) {
		return e.input.update(key)
	}
	typed := e.input.Value()
	e.input.Reset()
	m.clearMessages()
	if typed == "" {
		m.fail = tr(tExportPassEmpty)
		return nil
	}
	e.passphrase = typed
	m.redactor.Add(typed)
	return m.planImport(manage.ImportModeCreate, planProbe)
}

func (m *Model) updateImportMode(key tea.KeyPressMsg) tea.Cmd {
	e := m.imp
	switch {
	case keyIs(key, actUp):
		e.modeCursor = wrap(e.modeCursor-1, len(importModeRows))
	case keyIs(key, actDown):
		e.modeCursor = wrap(e.modeCursor+1, len(importModeRows))
	case keyIs(key, actEnter):
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
	m.busy = tr(tBusyReadingImport)
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
		m.fail = tr(tImportNothingChanged, m.redactor.Apply(msg.err.Error()))
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
	lines := []string{tr(tExLineFile, apply(plan.Source)), tr(tImLineMode, string(plan.Mode))}
	for _, e := range plan.Entries {
		line := fmt.Sprintf("%-8s  %s %s", e.Action, e.Section, apply(e.Name))
		if e.ID != "" {
			line += " (id " + e.ID
			switch {
			case e.NewID && e.Action == manage.ImportActionReplace:
				line += tr(tImEntryKept)
			case e.NewID:
				line += tr(tImEntryNew)
			}
			line += ")"
		}
		lines = append(lines, line)
		if len(e.UsesTarget) > 0 {
			lines = append(lines, tr(tImLineUses, names(e.UsesTarget)))
		}
		if len(e.UsedBy) > 0 {
			lines = append(lines, tr(tImLineUsedBy, names(e.UsedBy)))
		}
		if len(e.Access) > 0 {
			lines = append(lines, tr(tImLineAccess, strings.Join(e.Access, ", ")))
		}
	}
	if len(plan.CredentialsWithoutSecret) > 0 {
		lines = append(lines, tr(tImLineWithoutSecret, names(plan.CredentialsWithoutSecret)))
	}
	if len(plan.CredentialsKeepSecret) > 0 {
		lines = append(lines, tr(tImLineKeeps, names(plan.CredentialsKeepSecret)))
	}
	if len(plan.SecretRoles) > 0 {
		for _, name := range slices.Sorted(maps.Keys(plan.SecretRoles)) {
			lines = append(lines, tr(tImLineSecretRoles, apply(name), names(plan.SecretRoles[name])))
		}
		if plan.SourceEncrypted {
			lines = append(lines, tr(tImLineSecretsEnc, storeText(store)))
		} else {
			lines = append(lines, tr(tImLineWarnPlain, storeText(store)))
		}
		if overSSH {
			lines = append(lines, tr(tImLineWarnSSH))
		}
	}
	created, replaced, skipped := plan.Count()
	lines = append(lines, tr(tImLineTotal, created, replaced, skipped))
	if created+replaced == 0 {
		lines = append(lines, tr(tImportNothing))
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
		m.fail = tr(tImportNothing)
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
				tr(tVaultOfferTitle),
				vaultFirstSecretHint(),
				true, true,
				func(offer func(string) (string, error)) tea.Cmd { m.screen = screenTransfer; return m.runImport(offer) },
				func() tea.Cmd { m.screen = screenTransfer; m.status = tr(tCancelled); return nil },
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
	m.busy = tr(tBusyWritingImport)
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
			m.fail = tr(tImportConflict, m.redactor.Apply(msg.err.Error()))
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
					e.toApprove = append(e.toApprove, tr(tImportToApprove, m.redactor.Apply(o.Connection), m.redactor.Apply(openReasonText(o.Reason))))
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
	lines := []string{tr(tImLineResult, res.Created, res.Replaced, res.Skipped)}
	if res.SecretStore != "" {
		lines = append(lines, tr(tImLineWritten, storeText(res.SecretStore),
			names(slices.Sorted(maps.Keys(plan.SecretRoles)))))
	}
	if len(res.Withdrawn) > 0 {
		lines = append(lines, tr(tImLineWithdrawn, names(res.Withdrawn)))
	}
	if len(plan.CredentialsWithoutSecret) > 0 {
		lines = append(lines, tr(tImLineNext, names(plan.CredentialsWithoutSecret)))
	}
	for _, w := range res.Warnings {
		lines = append(lines, tr(tExLineWarning, apply(w)))
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
		lines = append(lines, tr(tExLineWarning, apply(w)))
	}
	if report.Err != nil {
		lines = append(lines, tr(tImLineCheckFailed, apply(report.Err.Error())))
	}
	switch {
	case report.VaultUnencrypted:
		lines = append(lines, tr(tImLineUnencrypted))
	case report.VaultLocked:
		lines = append(lines, tr(tImLineLocked))
	}
	required, _, err := manage.AdminRequired(m.secrets.Vault())
	encrypted := err == nil && required
	if len(res.Connections) > 0 && report.Err == nil && len(report.Open) == 0 && len(report.Approved) == 0 &&
		!report.VaultUnencrypted && !report.VaultLocked && !encrypted {
		lines = append(lines, tr(tImLineUsual))
	}
	if len(report.Approved) > 0 {
		lines = append(lines, tr(tImLineApproved, apply(strings.Join(report.Approved, ", "))))
	}
	for _, o := range report.Open {
		who := tr(tImWhoImported)
		if !o.Imported {
			who = tr(tImWhoChanged)
		}
		lines = append(lines, tr(tImLineOpen, who, apply(o.Connection), apply(openReasonText(o.Reason))))
	}
	if len(report.Open) > 0 {
		hint := tr(tImHintApprove)
		if asked {
			hint = tr(tImHintNothingElse)
		}
		lines = append(lines, tr(tImLineHint, hint))
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
		m.status = tr(tImported, res.Created, res.Replaced, res.Skipped)
	}
}

// runImportApprove approves the imported connections as a command: it writes to the vault and hands the
// approvals to a running vault process.
func (m *Model) runImportApprove() tea.Cmd {
	e := m.imp
	e.working = "writing"
	m.busy = tr(tBusyApprovingImport)
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
		e.lines = append(e.lines, tr(tImLineKept, shown))
	default:
		if err := os.Remove(e.path); err != nil {
			e.lines = append(e.lines, tr(tImLineDeleteFailed, shown, m.redactor.Apply(err.Error())))
		} else {
			e.lines = append(e.lines, tr(tImLineDeleted, shown))
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
	if d, ok := m.importPrompt(); ok {
		return m.promptView(d)
	}
	switch e.step {
	case importMode:
		p := m.dialogPanelOf()
		var b strings.Builder
		b.WriteString(p.top(tr(tImportModeTitle), "") + p.blank())
		for i, row := range importModeRows {
			b.WriteString(p.tableRow(i == e.modeCursor, truncateCells(tr(row.text), p.cells())) + "\n")
		}
		b.WriteString(p.blank() + p.hint(tr(tImportModeNote)) + p.bottom())
		return b.String() + m.importKeys("up/down move · {save} or enter preview · {back} back · {command} cmds · {help} help") + m.notes()
	}
	title, question, keys := tr(tImportPreviewTitle), tr(tTransferPreviewQuestion), "{save} write · {back} back · up/down scroll · {command} cmds · {help} help"
	lines := e.lines
	switch e.step {
	case importPreview:
		if e.plan != nil {
			if created, replaced, _ := e.plan.Count(); created+replaced == 0 {
				keys = "{back} back · up/down scroll · {command} cmds · {help} help"
			}
		}
	case importResult:
		title, question, keys = tr(tImportWrittenTitle), "", "enter or {back} done · up/down scroll · {command} cmds · {help} help"
		if e.pendingApprove || e.pendingDelete {
			keys = "enter, {save} or {back} next question · up/down scroll · {command} cmds · {help} help"
		}
	case importApprove:
		title, question, keys = tr(tImportWrittenTitle), tr(tImportApproveQuestion),
			"y approve · n/{back} not now, nothing is approved · up/down scroll · {command} cmds · {help} help"
		lines = append(slices.Clone(lines), e.toApprove...)
	case importDelete:
		title, question, keys = tr(tImportWrittenTitle), tr(tImportDeleteQuestion, m.redactor.Apply(e.path)),
			"y delete the file · n/{back} keep it · up/down scroll · {command} cmds · {help} help"
	}
	if e.working != "" {
		keys = workingWait(e.working)
	}
	return m.linesView(title, question, keys, lines, &e.offset,
		e.step == importResult || e.step == importApprove || e.step == importDelete)
}

// importPrompt is the one-line dialog the import is at, if it is at one.
func (m *Model) importPrompt() (promptDialog, bool) {
	e := m.imp
	if e == nil {
		return promptDialog{}, false
	}
	switch e.step {
	case importFile:
		return promptDialog{
			title: tr(tImportFileTitle),
			input: m.pathLines("  ", &e.input),
			field: true,
			hint:  tr(tImportFileHint),
			keys:  m.importDialogKeys(pathKeys(&e.input, "{save} or enter next · {back} cancel, nothing imported")),
		}, true
	case importPassphrase:
		return promptDialog{
			title: tr(tExportPassTitle),
			input: m.inputLine("  ", e.input.Model),
			field: true,
			hint:  tr(tImportPassHint),
			keys:  m.importDialogKeys("{save} or enter ok · {back} back, nothing imported"),
		}, true
	}
	return promptDialog{}, false
}

// importDialogKeys is the key line of a dialog, or the wait while the import works.
func (m *Model) importDialogKeys(keys string) string {
	if m.imp != nil && m.imp.working != "" {
		return workingWait(m.imp.working)
	}
	return keys
}

// openReasonText words the reason the core gives for an open connection (manage.StaysOpenReason, in English)
// the way the Approvals section words it.
func openReasonText(reason string) string {
	if reason == textEN[tApprovalNew] {
		return tr(tApprovalNew)
	}
	if fields, ok := strings.CutSuffix(reason, " changed"); ok {
		return tr(tApprovalChanged, fields)
	}
	return reason
}

// workingWait is the key line while the import reads or writes.
func workingWait(working string) string {
	if working == "reading" {
		return tr(tWorkReadingWait)
	}
	return tr(tWorkWritingWait)
}

// storeText names a secret store of the core ("vault", "keyring") in the editor's language.
func storeText(store string) string {
	if uiLang == i18n.German && store == config.CredentialTypeVault {
		return "Tresor"
	}
	if uiLang == i18n.German && store == config.CredentialTypeKeyring {
		return "Keyring"
	}
	return store
}

// importKeys is the footer of a step that has no lines to scroll.
func (m *Model) importKeys(keys string) string {
	return m.keyHint(m.importDialogKeys(keys))
}

// importLeaveView is the leave question over an unfinished import.
func (m *Model) importLeaveView() string {
	save := "{save} next step"
	if m.imp != nil && m.imp.step == importPreview {
		save = "{save} write"
	}
	title, discard, why := tr(tImportLeaveTitle), "d discard", tr(tImportLeaveWhy)
	if m.leaveQuit {
		title, discard, why = tr(tImportQuitTitle), "d discard and quit", tr(tTransferQuitWhy)
	}
	return m.questionText(question{title: title, consequence: why, danger: true,
		details: []string{m.indentedWith(styles.warning, tr(tImportLeaveWarn)),
			m.indented(tr(tTransferNothingWritten))},
		keys: save + " · " + discard + " · {back} keep editing · {command} cmds · {help} help"})
}
