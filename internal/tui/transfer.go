package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Exporting the configuration is ':export'. The editor only asks what to take, how to protect the secrets,
// and where to write; the preview, the secret reading and the file are manage.Service's. It is a managing
// action like every other one, so it runs behind the admin session, and the export with secrets runs only
// where the vault has a passphrase: there is no admin session to prove otherwise.

// exportStep is the question an export is at.
type exportStep int

const (
	exportSelect exportStep = iota
	exportMode
	exportPassphrase
	exportPlainConfirm
	exportTarget
	exportPreview
	exportResult
)

// exportKind says whether and how the file holds secrets.
type exportKind int

const (
	exportWithout exportKind = iota
	exportEncrypted
	exportUnencrypted
)

// exportModeRows are the answers of the mode step, in the order of exportKind.
func exportModeRows() []string {
	return []string{tr(tExportModeNone), tr(tExportModeEncrypted), tr(tExportModePlain)}
}

const (
	exportAll      = "all"
	exportDefaults = "defaults"
	exportNotes    = "notes"
	exportConnPref = "connection:"
	exportSvcPref  = "service:"
	exportCredPref = "credential:"
	exportFileName = "qatlas-export.yaml"
)

// exportState is the export in progress, while screenTransfer shows it.
type exportState struct {
	step exportStep
	// from is the screen the export was started from and returns to.
	from screen
	// rows lists what can be ticked; marks holds the ticks.
	rows  filterList
	marks map[string]bool
	kind  exportKind
	// modeCursor is the focused row of the mode step; secretsReason is why the rows with secrets are not
	// possible ("" where they are), asked once when the step is entered because it reads the vault state.
	modeCursor    int
	secretsReason string
	// input is the masked passphrase on exportPassphrase and the file path on exportTarget.
	input pathField
	// first is the first passphrase entry while it is typed again; passphrase is the confirmed one. Both
	// only ever reach the core's ExportPassphrase and are cleared when the export ends.
	first, passphrase string
	confirming        bool
	target            string
	// lines and view are the preview or the result, one line each, and the scroll position in them.
	lines []string
	view  textScroll
	// writing is true while the core writes the file; no key acts then.
	writing bool
}

// exportDoneMsg carries the outcome of the write back into the event loop.
type exportDoneMsg struct {
	res  *manage.ExportResult
	err  error
	path string
}

// askExport starts the export, once the admin session allows it.
func (m *Model) askExport() tea.Cmd {
	if work := m.updateBlocker(); work != "" {
		m.clearMessages()
		m.status = tr(tExportWait, work)
		return nil
	}
	return m.requireAdmin(func() tea.Cmd { m.openExport(); return nil })
}

func (m *Model) openExport() {
	names := []string{exportAll, exportDefaults, exportNotes}
	for _, kind := range []struct {
		prefix string
		keys   func() []string
	}{
		{exportConnPref, func() []string { return slices.Sorted(maps.Keys(m.cfg.Connections)) }},
		{exportSvcPref, func() []string { return slices.Sorted(maps.Keys(m.cfg.Services)) }},
		{exportCredPref, func() []string { return slices.Sorted(maps.Keys(m.cfg.Credentials)) }},
	} {
		for _, name := range kind.keys() {
			names = append(names, kind.prefix+name)
		}
	}
	rows := newFilterList(m.exportRowText)
	rows.reset(names)
	m.export = &exportState{from: m.screen, rows: rows, marks: map[string]bool{}}
	m.screen = screenTransfer
	m.clearMessages()
}

// exportRowText is the line of one tickable row.
func (m *Model) exportRowText(key string) string {
	switch key {
	case exportAll:
		return tr(tExportRowAll)
	case exportDefaults:
		return tr(tExportRowDefaults)
	case exportNotes:
		return tr(tExportRowNotes)
	}
	for _, p := range []struct {
		prefix string
		label  textID
	}{
		{exportConnPref, tExportKindConnection}, {exportSvcPref, tExportKindService}, {exportCredPref, tExportKindCredential},
	} {
		if name, ok := strings.CutPrefix(key, p.prefix); ok {
			return tr(p.label) + m.redactor.Apply(name)
		}
	}
	return key
}

// selection turns the ticks into the core's selection.
func (e *exportState) selection() manage.ExportSelection {
	sel := manage.ExportSelection{All: e.marks[exportAll], Defaults: e.marks[exportDefaults],
		ProviderNotes: e.marks[exportNotes]}
	if sel.All {
		// All and named entries exclude each other in the core; everything includes what a name would take.
		return sel
	}
	for _, key := range e.rows.all {
		if !e.marks[key] {
			continue
		}
		switch {
		case strings.HasPrefix(key, exportConnPref):
			sel.Connections = append(sel.Connections, strings.TrimPrefix(key, exportConnPref))
		case strings.HasPrefix(key, exportSvcPref):
			sel.Services = append(sel.Services, strings.TrimPrefix(key, exportSvcPref))
		case strings.HasPrefix(key, exportCredPref):
			sel.Credentials = append(sel.Credentials, strings.TrimPrefix(key, exportCredPref))
		}
	}
	return sel
}

// closeExport leaves the export, dropping everything typed into it.
func (m *Model) closeExport() {
	e := m.export
	m.export = nil
	m.screen = screenList
	if e != nil {
		e.input.Reset()
		e.first, e.passphrase = "", ""
		m.screen = e.from
	}
}

// cancelExport ends the export without a file.
func (m *Model) cancelExport() {
	m.closeExport()
	m.status = tr(tExportCancelled)
}

// secretsUnavailable is why an export with secrets is not possible now, or "".
func (m *Model) secretsUnavailable() string {
	required, _, err := manage.AdminRequired(m.secrets.Vault())
	switch {
	case err != nil:
		return tr(tExportStateUnreadable, m.redactor.Apply(err.Error()))
	case !required:
		return tr(tExportNoPassphrase)
	}
	return ""
}

func (m *Model) updateExport(key tea.KeyPressMsg) tea.Cmd {
	e := m.export
	if e == nil {
		m.screen = screenList
		return nil
	}
	if e.writing {
		return nil
	}
	if key.String() == keyOf(actBack) {
		return m.exportBack()
	}
	if key.String() == keyOf(actSave) {
		switch e.step {
		case exportSelect, exportMode, exportPassphrase, exportTarget:
			// F2 goes on like enter, which also confirms the one-line dialogs.
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		case exportResult:
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
	}
	switch e.step {
	case exportSelect:
		return m.updateExportSelect(key)
	case exportMode:
		return m.updateExportMode(key)
	case exportPassphrase:
		return m.updateExportPassphrase(key)
	case exportPlainConfirm:
		switch {
		case keyIs(key, actYes):
			m.clearMessages()
			m.askExportTarget()
		case keyIs(key, actNo):
			m.clearMessages()
			e.step = exportMode
		}
	case exportTarget:
		return m.updateExportTarget(key)
	case exportPreview:
		switch {
		case keyIs(key, actSave):
			m.clearMessages()
			return m.requireAdmin(m.runExport)
		default:
			e.scroll(key, m.height)
		}
	case exportResult:
		switch {
		case keyIs(key, actEnter):
			path := e.target
			m.closeExport()
			m.status = tr(tExported, m.redactor.Apply(path))
		default:
			e.scroll(key, m.height)
		}
	}
	return nil
}

func (e *exportState) scroll(key tea.KeyPressMsg, height int) {
	e.view.scroll(key, halfPage(height))
}

func (m *Model) updateExportSelect(key tea.KeyPressMsg) tea.Cmd {
	e := m.export
	switch {
	case keyIs(key, actUp):
		e.rows.move(-1)
	case keyIs(key, actDown):
		e.rows.move(1)
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		start, end := m.exportRowWindow()
		e.rows.page(key, start, end)
	case keyIs(key, actSpace):
		if name, ok := e.rows.selected(); ok {
			e.marks[name] = !e.marks[name]
		}
	case keyIs(key, actEnter):
		m.clearMessages()
		sel := e.selection()
		if !sel.All && !sel.Defaults && !sel.ProviderNotes &&
			len(sel.Connections)+len(sel.Services)+len(sel.Credentials) == 0 {
			m.fail = tr(tExportTickFirst)
			return nil
		}
		e.modeCursor = 0
		m.enterExportMode()
	}
	return nil
}

// enterExportMode shows the secrets question.
func (m *Model) enterExportMode() {
	e := m.export
	e.step = exportMode
	e.secretsReason = m.secretsUnavailable()
}

// askExportPassphrase shows the empty masked passphrase prompt.
func (m *Model) askExportPassphrase() {
	e := m.export
	e.step = exportPassphrase
	e.input = pathField{Model: maskedInput()}
	e.first, e.passphrase, e.confirming = "", "", false
}

// ticked is the number of ticked rows.
func (e *exportState) ticked() int {
	n := 0
	for _, on := range e.marks {
		if on {
			n++
		}
	}
	return n
}

// dirty reports whether leaving now would drop ticks or answers; a written export loses nothing.
func (e *exportState) dirty() bool {
	return e != nil && e.step != exportResult && (e.ticked() > 0 || e.step != exportSelect)
}

// exportBack is esc: one step back, and on the first step the way out, asking first once something is
// ticked. Nothing is written before F2 on the preview.
func (m *Model) exportBack() tea.Cmd {
	e := m.export
	m.clearMessages()
	switch e.step {
	case exportSelect:
		if e.ticked() > 0 {
			m.askLeave(false)
			return nil
		}
		m.cancelExport()
	case exportMode:
		e.step = exportSelect
	case exportPassphrase:
		e.input.Reset()
		e.first, e.passphrase, e.confirming = "", "", false
		m.enterExportMode()
	case exportPlainConfirm:
		m.enterExportMode()
	case exportTarget:
		e.target = strings.TrimSpace(e.input.Value())
		e.input.Reset()
		switch e.kind {
		case exportEncrypted:
			m.askExportPassphrase()
		case exportUnencrypted:
			e.step = exportPlainConfirm
		default:
			m.enterExportMode()
		}
	case exportPreview:
		m.askExportTarget()
	case exportResult:
		m.closeExport()
	}
	return nil
}

func (m *Model) updateExportMode(key tea.KeyPressMsg) tea.Cmd {
	e := m.export
	switch {
	case keyIs(key, actUp):
		e.modeCursor = wrap(e.modeCursor-1, len(exportModeRows()))
	case keyIs(key, actDown):
		e.modeCursor = wrap(e.modeCursor+1, len(exportModeRows()))
	case keyIs(key, actEnter):
		m.clearMessages()
		e.kind = exportKind(e.modeCursor)
		if e.kind == exportWithout {
			m.askExportTarget()
			return nil
		}
		if e.secretsReason = m.secretsUnavailable(); e.secretsReason != "" {
			m.fail = e.secretsReason
			return nil
		}
		// The secrets are read under this window's admin session, which the vault passphrase proves.
		return m.requireAdmin(func() tea.Cmd {
			if e.kind == exportUnencrypted {
				e.step = exportPlainConfirm
				return nil
			}
			m.askExportPassphrase()
			return nil
		})
	}
	return nil
}

// maskedInput is a focused one-line input that never echoes.
func maskedInput() textinput.Model {
	in := newTextInput()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Focus()
	return in
}

// updateExportPassphrase takes the export passphrase twice; a difference asks again from the start.
func (m *Model) updateExportPassphrase(key tea.KeyPressMsg) tea.Cmd {
	e := m.export
	if !keyIs(key, actEnter) {
		return e.input.update(key)
	}
	typed := e.input.Value()
	e.input.Reset()
	m.clearMessages()
	switch {
	case !e.confirming && typed == "":
		m.fail = tr(tExportPassEmpty)
	case !e.confirming:
		e.first, e.confirming = typed, true
	case typed != e.first:
		e.first, e.confirming = "", false
		m.fail = tr(tPassphraseMismatch)
	default:
		e.passphrase, e.first, e.confirming = typed, "", false
		m.askExportTarget()
	}
	return nil
}

// askExportTarget moves to the path prompt.
func (m *Model) askExportTarget() {
	e := m.export
	e.step = exportTarget
	value := e.target
	if value == "" && m.startDir != "" {
		value = filepath.Join(m.startDir, exportFileName)
	}
	e.input = newPathField(pathFile)
	e.input.open(value, m.startDir)
}

func (m *Model) updateExportTarget(key tea.KeyPressMsg) tea.Cmd {
	e := m.export
	if !keyIs(key, actEnter) {
		return e.input.update(key)
	}
	m.clearMessages()
	if !e.input.accept() {
		return nil
	}
	path := strings.TrimSpace(e.input.Value())
	if path == "" {
		m.fail = tr(tExportNameFile)
		return nil
	}
	path = absolutePath(path)
	// Nothing is read or written yet: the preview never holds a secret value.
	res, err := m.svc.ExportConfig(e.selection(), "")
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	e.target = path
	e.view.reset()
	e.lines = m.exportLines(res, true)
	e.step = exportPreview
	return nil
}

// runExport writes the file once the preview is confirmed. It runs as a command: reading the vault and
// encrypting the file do scrypt work that must not block the event loop. The vault is unlocked by the admin
// check just before; no passphrase of it is kept anywhere.
func (m *Model) runExport() tea.Cmd {
	e := m.export
	if e == nil || e.writing {
		return nil
	}
	e.writing = true
	m.busy = tr(tBusyExporting)
	kind, sel, path, svc := e.kind, e.selection(), e.target, m.svc
	if kind != exportWithout {
		m.vaultBusy = true
	}
	passphrase := e.passphrase
	return func() tea.Msg {
		var (
			res *manage.ExportResult
			err error
		)
		if kind == exportWithout {
			res, err = svc.ExportConfig(sel, path)
			return exportDoneMsg{res: res, err: err, path: path}
		}
		opts := manage.SecretExport{
			VaultPassphrase: func(string) (string, error) { return "", errors.New(tr(tExportVaultLock)) },
		}
		if kind == exportUnencrypted {
			opts.Unencrypted = true
		} else {
			opts.ExportPassphrase = func(string) (string, error) { return passphrase, nil }
		}
		res, err = svc.ExportConfigWithSecrets(sel, path, opts)
		return exportDoneMsg{res: res, err: err, path: path}
	}
}

func (m *Model) handleExportDone(msg exportDoneMsg) tea.Cmd {
	m.busy, m.vaultBusy = "", false
	e := m.export
	if e == nil {
		return nil
	}
	e.writing = false
	if msg.err != nil {
		// Back at the path, where an existing file is answered by another name; the choices stay.
		m.askExportTarget()
		m.fail = m.exportError(msg.err, msg.path)
		return nil
	}
	e.first, e.passphrase = "", ""
	e.view.reset()
	e.lines = m.exportLines(msg.res, false)
	e.step = exportResult
	m.clearMessages()
	return nil
}

// exportError words a refused export; the core never replaces a file.
func (m *Model) exportError(err error, path string) string {
	if errors.Is(err, fs.ErrExist) {
		return tr(tExportExists, m.redactor.Apply(path))
	}
	return m.redactor.Apply(err.Error())
}

// exportLines are the preview or the result, one line each, in the order of 'qatlas config export'. A line
// that starts with "warning" is drawn as one. They name credentials and roles, never a value.
func (m *Model) exportLines(res *manage.ExportResult, preview bool) []string {
	e := m.export
	apply := m.redactor.Apply
	names := func(list []string) string { return apply(strings.Join(list, ", ")) }
	var lines []string
	lines = append(lines, tr(tExLineFile, apply(e.target)))
	switch {
	case e.kind == exportWithout:
		lines = append(lines, tr(tExLineSecretsNone))
	case preview && e.kind == exportEncrypted:
		lines = append(lines, tr(tExLineSecretsEncPreview))
	case preview:
		lines = append(lines, tr(tExLineWarnPlain))
	case res.Encrypted:
		lines = append(lines, tr(tExLineSecretsEnc))
	}
	for _, line := range []struct {
		label textID
		names []string
	}{
		{tExLineServices, res.Services}, {tExLineCredentials, res.Credentials}, {tExLineConnections, res.Connections},
		{tExLineDefaults, res.Defaults}, {tExLineNotes, res.ProviderNotes},
	} {
		if len(line.names) > 0 {
			lines = append(lines, tr(line.label, names(line.names)))
		}
	}
	for _, dep := range res.Dependencies {
		lines = append(lines, tr(tExLineNeeded, dep.Kind, apply(dep.Name), names(dep.For)))
	}
	if len(res.DefaultsOmitted) > 0 {
		lines = append(lines, tr(tExLineLeftOut, names(res.DefaultsOmitted)))
	}
	if len(res.SecretsNotIncluded) > 0 {
		switch {
		case preview && e.kind != exportWithout:
			lines = append(lines, tr(tExLineReads, names(res.SecretsNotIncluded)))
		case e.kind != exportWithout:
			lines = append(lines, tr(tExLineWithoutNone, names(res.SecretsNotIncluded)))
		default:
			lines = append(lines, tr(tExLineWithoutSet, names(res.SecretsNotIncluded)))
		}
	}
	for _, s := range res.SecretsIncluded {
		lines = append(lines, tr(tExLineSecret, apply(s.Credential), apply(strings.Join(s.Roles, ", "))))
	}
	if len(res.SecretsMissing) > 0 {
		lines = append(lines, tr(tExLineMissing, names(res.SecretsMissing)))
	}
	for _, w := range res.Warnings {
		lines = append(lines, tr(tExLineWarning, apply(w)))
	}
	return lines
}

// exportRowWindow is the range of tick rows that fit the screen.
func (m *Model) exportRowWindow() (int, int) {
	head, foot := m.exportSelectFrame()
	return m.windowIn(&m.export.rows, head, foot, m.exportRow)
}

func (m *Model) exportRow(i int) string {
	e := m.export
	p := m.dialogPanelOf()
	key := e.rows.matches[i]
	box := "[ ] "
	if e.marks[key] {
		box = "[x] "
	}
	return p.tableRow(i == e.rows.cursor, truncateCells(box+m.exportRowText(key), p.cells()))
}

func (m *Model) exportSelectFrame() (string, string) {
	p := m.dialogPanelOf()
	head := p.top(tr(tExportSelectTitle), "") + p.text(styles.hint, tr(tExportSelectHint))
	if m.export.marks[exportAll] {
		head += p.text(styles.hint, tr(tExportAllTicked))
	}
	head += p.blank()
	return head, p.closing() + m.keyHint("up/down move · space tick · {save} or enter next · {back} cancel · {command} cmds · {help} help") + m.notes()
}

// exportTyping reports whether the export reads text now.
func (m *Model) exportTyping() bool {
	return m.export != nil && !m.export.writing &&
		(m.export.step == exportPassphrase || m.export.step == exportTarget)
}

// exportView draws the step the export is at.
func (m *Model) exportView() string {
	e := m.export
	if e == nil {
		return ""
	}
	if d, ok := m.exportPrompt(); ok {
		if m.backdrop {
			// A dialog stands over the question that led to it.
			saved := e.step
			e.step = exportMode
			defer func() { e.step = saved }()
			return m.exportView()
		}
		return m.promptView(d)
	}
	switch e.step {
	case exportSelect:
		head, foot := m.exportSelectFrame()
		var b strings.Builder
		b.WriteString(head)
		start, end := m.exportRowWindow()
		for i := start; i < end; i++ {
			b.WriteString(m.exportRow(i) + "\n")
		}
		e.rows.offset = start
		return b.String() + foot
	case exportMode:
		reason := e.secretsReason
		p := m.dialogPanelOf()
		var b strings.Builder
		b.WriteString(p.top(tr(tExportSecretsTitle), "") + p.blank())
		for i, row := range exportModeRows() {
			text := row
			blocked := i > 0 && reason != ""
			if blocked {
				text += tr(tExportNotPossible)
			}
			line := p.tableRow(i == e.modeCursor, truncateCells(text, p.cells()))
			if blocked && i != e.modeCursor {
				line = styles.hint.Render(line)
			}
			b.WriteString(line + "\n")
		}
		note := p.hint(tr(tExportSecretsNote))
		if reason != "" {
			note = p.hint(reason)
		}
		b.WriteString(p.blank() + note + p.bottom())
		return b.String() + m.keyHint("up/down move · {save} or enter choose · {back} back · {command} cmds · {help} help") + m.notes()
	case exportPlainConfirm:
		p := m.dialogPanelOf()
		return p.top(tr(tExportPlainTitle), "") + p.blank() +
			p.text(styles.warning, warnMark+" "+tr(tExportPlainWarn)) + p.bottom() +
			m.keyHint("y write unencrypted · n/{back} back, nothing written · {command} cmds · {help} help") + m.notes()
	}
	title, question, keys := tr(tExportPreviewTitle), tr(tTransferPreviewQuestion), "{save} write · {back} back · up/down scroll · {command} cmds · {help} help"
	if e.step == exportResult {
		title, question, keys = tr(tExportWrittenTitle), "", "enter or {back} done · up/down scroll · {command} cmds · {help} help"
	}
	if e.writing {
		keys = tr(tWorkWritingWait)
	}
	return m.linesView(title, question, keys, e.lines, &e.view, e.step == exportResult)
}

// exportPrompt is the one-line dialog the export is at, if it is at one.
func (m *Model) exportPrompt() (promptDialog, bool) {
	e := m.export
	if e == nil {
		return promptDialog{}, false
	}
	switch e.step {
	case exportPassphrase:
		d := promptDialog{
			title: tr(tExportPassTitle),
			input: m.inputLine("  ", e.input.Model),
			field: true,
			hint:  tr(tExportPassHint),
			keys:  "{save} or enter ok · {back} back, nothing written",
		}
		if e.confirming {
			d.title, d.hint = tr(tExportPassConfirmTitle), tr(tVaultConfirmHint)
		}
		return d, true
	case exportTarget:
		return promptDialog{
			title: tr(tExportFileTitle),
			input: m.pathLines("  ", &e.input),
			field: true,
			hint:  tr(tExportFileHint),
			keys:  pathKeys(&e.input, "{save} or enter preview · {back} back"),
		}, true
	}
	return promptDialog{}, false
}

// isWarningLine reports whether a line of a preview or result is a warning, which the label of the line says.
func isWarningLine(line string) bool {
	return strings.HasPrefix(line, strings.Fields(tr(tExLineWarning))[0])
}

// linesView draws a preview or a result: the lines scrolled to the position of view, which it clamps, in a
// panel under its title and above the question and keys of the step. A line that starts with "warning" is drawn as one,
// with its mark; a finished step opens with its state.
func (m *Model) linesView(title, question, keys string, lines []string, view *textScroll, done bool) string {
	p := m.dialogPanelOf()
	var body []string
	warned := false
	for _, line := range lines {
		if isWarningLine(line) {
			warned = true
		}
		body = append(body, m.reportLines(p, line)...)
	}
	kind, word := statusOK, tr(tStatusDone)
	if warned {
		kind, word = statusWarn, tr(tStatusDoneWarn)
	}
	headOf := func(note string) string {
		switch {
		case done && p.framed:
			return p.top(title, note) + p.result(kind, word) + p.blank()
		case done:
			return m.wrapped(styles.title, title) + "  " + statusMark(kind, word) + "\n\n"
		case p.framed:
			return p.top(title, note) + p.blank()
		}
		return p.top(title, "") + "\n"
	}
	head := headOf("")
	foot := p.closing()
	if question != "" {
		foot += m.wrapped(styles.hint, question)
	}
	foot += m.keyHint(keys) + m.notes()
	room := max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 3)
	shown := view.window(body, room, false)
	if p.framed && len(body) > room {
		head = headOf(fmt.Sprintf("%d-%d/%d", view.offset()+1, view.offset()+len(shown), len(body)))
	}
	return head + strings.Join(shown, "\n") + "\n" + foot
}

// reportLines draws one line of a report: its label dimmed and its value plain, a warning with its mark and
// colour. A line that does not fit continues on the next row; the lead keeps the columns of the labels.
func (m *Model) reportLines(p dialogPanel, line string) []string {
	warn := isWarningLine(line)
	lead := "  "
	if warn {
		lead = styles.warning.Render(warnMark) + " "
	}
	var out []string
	for i, piece := range strings.Split(render(lipgloss.NewStyle(), max(p.room-2, 1), line), "\n") {
		text := piece
		switch {
		case warn:
			text = styles.warning.Render(piece)
		case i == 0 && !strings.HasPrefix(piece, " "):
			label, value, _ := strings.Cut(piece, " ")
			text = styles.hint.Render(label) + styles.cell.Render(" "+value)
		default:
			text = styles.cell.Render(piece)
		}
		prefix := lead
		if i > 0 {
			prefix = "  "
		}
		out = append(out, p.side(prefix+text))
	}
	return out
}

// exportLeaveView is the leave question over an unfinished export.
func (m *Model) exportLeaveView() string {
	save := "{save} next step"
	if m.export != nil && m.export.step == exportPreview {
		save = "{save} write"
	}
	title, discard, why := tr(tExportLeaveTitle), "d discard", tr(tExportLeaveWhy)
	if m.leaveQuit {
		title, discard, why = tr(tExportQuitTitle), "d discard and quit", tr(tTransferQuitWhy)
	}
	keys := save + " · " + discard + " · {back} keep editing · {command} cmds · {help} help"
	if m.export != nil && m.export.step == exportPlainConfirm {
		// F2 does not answer the plain text question; only y does.
		keys = discard + " · {back} keep editing · {command} cmds · {help} help"
	}
	return m.questionText(question{title: title, consequence: why, danger: true,
		details: []string{m.indentedWith(styles.warning, tr(tExportLeaveWarn)),
			m.indented(tr(tTransferNothingWritten))},
		keys: keys})
}
