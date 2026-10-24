package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
var exportModeRows = []string{
	"without secrets",
	"with secrets, encrypted with an export passphrase",
	"with secrets, unencrypted (plain text)",
}

const (
	exportAll       = "all"
	exportDefaults  = "defaults"
	exportNotes     = "notes"
	exportConnPref  = "connection:"
	exportSvcPref   = "service:"
	exportCredPref  = "credential:"
	exportFileName  = "qatlas-export.yaml"
	exportVaultLock = "the vault is locked; unlock it with ctrl+l and export again"
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
	// lines and offset are the preview or the result, one line each, and the first one shown.
	lines  []string
	offset int
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
		m.status = "Wait until " + work + " finishes, then run export again."
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
		return "everything: every service, credential, and connection"
	case exportDefaults:
		return "defaults that name an exported connection"
	case exportNotes:
		return "provider notes"
	}
	for _, p := range []struct{ prefix, label string }{
		{exportConnPref, "connection  "}, {exportSvcPref, "service     "}, {exportCredPref, "credential  "},
	} {
		if name, ok := strings.CutPrefix(key, p.prefix); ok {
			return p.label + m.redactor.Apply(name)
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
	m.status = "Cancelled; nothing was exported"
}

// secretsUnavailable is why an export with secrets is not possible now, or "".
func (m *Model) secretsUnavailable() string {
	required, _, err := manage.AdminRequired(m.secrets.Vault())
	switch {
	case err != nil:
		return "the vault state cannot be read: " + m.redactor.Apply(err.Error())
	case !required:
		return "the vault has no passphrase, so there is no admin session to export secrets under"
	}
	return ""
}

func (m *Model) updateExport(key tea.KeyMsg) tea.Cmd {
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
			key = tea.KeyMsg{Type: tea.KeyEnter}
		case exportResult:
			key = tea.KeyMsg{Type: tea.KeyEnter}
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
		switch key.String() {
		case "y":
			m.clearMessages()
			m.askExportTarget()
		case "n":
			m.clearMessages()
			e.step = exportMode
		}
	case exportTarget:
		return m.updateExportTarget(key)
	case exportPreview:
		switch key.String() {
		case keyOf(actSave):
			m.clearMessages()
			return m.requireAdmin(m.runExport)
		default:
			e.scroll(key.String(), m.height)
		}
	case exportResult:
		switch key.String() {
		case "enter":
			path := e.target
			m.closeExport()
			m.status = "Exported to " + m.redactor.Apply(path)
		default:
			e.scroll(key.String(), m.height)
		}
	}
	return nil
}

func (e *exportState) scroll(key string, height int) { e.offset = scrollOffset(e.offset, key, height) }

// scrollOffset is the first shown line after a scroll key; the view clamps it to the lines there are.
func scrollOffset(offset int, key string, height int) int {
	switch key {
	case "down":
		return offset + 1
	case "up":
		return max(offset-1, 0)
	case "pgdown":
		return offset + max(height/2, 1)
	case "pgup":
		return max(offset-max(height/2, 1), 0)
	case "home":
		return 0
	case "end":
		return 1 << 20
	}
	return offset
}

func (m *Model) updateExportSelect(key tea.KeyMsg) tea.Cmd {
	e := m.export
	switch key.String() {
	case "up":
		e.rows.move(-1)
	case "down":
		e.rows.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.exportRowWindow()
		e.rows.page(key.String(), start, end)
	case " ":
		if name, ok := e.rows.selected(); ok {
			e.marks[name] = !e.marks[name]
		}
	case "enter":
		m.clearMessages()
		sel := e.selection()
		if !sel.All && !sel.Defaults && !sel.ProviderNotes &&
			len(sel.Connections)+len(sel.Services)+len(sel.Credentials) == 0 {
			m.fail = "tick what to export first (space)"
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

func (m *Model) updateExportMode(key tea.KeyMsg) tea.Cmd {
	e := m.export
	switch key.String() {
	case "up":
		e.modeCursor = wrap(e.modeCursor-1, len(exportModeRows))
	case "down":
		e.modeCursor = wrap(e.modeCursor+1, len(exportModeRows))
	case "enter":
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
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()
	return in
}

// updateExportPassphrase takes the export passphrase twice; a difference asks again from the start.
func (m *Model) updateExportPassphrase(key tea.KeyMsg) tea.Cmd {
	e := m.export
	if key.String() != "enter" {
		return e.input.update(key)
	}
	typed := e.input.Value()
	e.input.Reset()
	m.clearMessages()
	switch {
	case !e.confirming && typed == "":
		m.fail = "an export passphrase must not be empty"
	case !e.confirming:
		e.first, e.confirming = typed, true
	case typed != e.first:
		e.first, e.confirming = "", false
		m.fail = "the two passphrases did not match; type the passphrase again"
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

func (m *Model) updateExportTarget(key tea.KeyMsg) tea.Cmd {
	e := m.export
	if key.String() != "enter" {
		return e.input.update(key)
	}
	m.clearMessages()
	if !e.input.accept() {
		return nil
	}
	path := strings.TrimSpace(e.input.Value())
	if path == "" {
		m.fail = "name the file to write"
		return nil
	}
	path = absolutePath(path)
	// Nothing is read or written yet: the preview never holds a secret value.
	res, err := m.svc.ExportConfig(e.selection(), "")
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	e.target, e.offset = path, 0
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
	m.busy = "writing the export"
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
			VaultPassphrase: func(string) (string, error) { return "", errors.New(exportVaultLock) },
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
	e.offset = 0
	e.lines = m.exportLines(msg.res, false)
	e.step = exportResult
	m.clearMessages()
	return nil
}

// exportError words a refused export; the core never replaces a file.
func (m *Model) exportError(err error, path string) string {
	if errors.Is(err, fs.ErrExist) {
		return m.redactor.Apply(path) + " already exists; export never replaces a file, choose another name"
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
	lines = append(lines, "file      "+apply(e.target))
	switch {
	case e.kind == exportWithout:
		lines = append(lines, "secrets   none")
	case preview && e.kind == exportEncrypted:
		lines = append(lines, "secrets   included, encrypted with the export passphrase")
	case preview:
		lines = append(lines, "warning   secrets are included in plain text: anyone who can read the file can use them")
	case res.Encrypted:
		lines = append(lines, "secrets   encrypted with the export passphrase")
	}
	for _, line := range []struct {
		label string
		names []string
	}{
		{"services", res.Services}, {"credentials", res.Credentials}, {"connections", res.Connections},
		{"defaults", res.Defaults}, {"notes", res.ProviderNotes},
	} {
		if len(line.names) > 0 {
			lines = append(lines, fmt.Sprintf("%-9s %s", line.label, names(line.names)))
		}
	}
	for _, dep := range res.Dependencies {
		lines = append(lines, fmt.Sprintf("needed    %s %s, for %s", dep.Kind, apply(dep.Name), names(dep.For)))
	}
	if len(res.DefaultsOmitted) > 0 {
		lines = append(lines, "left out  defaults "+names(res.DefaultsOmitted)+": their connection is not exported")
	}
	if len(res.SecretsNotIncluded) > 0 {
		switch {
		case preview && e.kind != exportWithout:
			lines = append(lines, "reads     the secrets of "+names(res.SecretsNotIncluded)+
				" from the keyring or the vault; the result lists what was found")
		case e.kind != exportWithout:
			lines = append(lines, "without   "+names(res.SecretsNotIncluded)+": no secret in the file")
		default:
			lines = append(lines, "without   "+names(res.SecretsNotIncluded)+
				": set them in the target with 'qatlas credential set'")
		}
	}
	for _, s := range res.SecretsIncluded {
		lines = append(lines, "secret    "+apply(s.Credential)+": "+apply(strings.Join(s.Roles, ", ")))
	}
	if len(res.SecretsMissing) > 0 {
		lines = append(lines, "missing   "+names(res.SecretsMissing)+": asked for, but not stored")
	}
	for _, w := range res.Warnings {
		lines = append(lines, "warning   "+apply(w))
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
	key := e.rows.matches[i]
	box := "[ ] "
	if e.marks[key] {
		box = "[x] "
	}
	_, width := m.fit("  ")
	return m.listRow(i == e.rows.cursor, i%2 == 1, truncateCells(box+m.exportRowText(key), width))
}

func (m *Model) exportSelectFrame() (string, string) {
	head := m.wrapped(styles.title, "Export: what to take") + "\n" +
		m.wrapped(styles.hint, "a connection comes with its service and credentials; agent tokens, approvals, the log, and "+
			"the vault settings are never exported") + "\n"
	if m.export.marks[exportAll] {
		head += m.wrapped(styles.hint, "everything is ticked: the single entries are not needed") + "\n"
	}
	head += "\n"
	return head, "\n" + m.keyHint("up/down move · space tick · {save} or enter next · {back} cancel · {command} cmds · {help} help") + m.notes()
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
		var b strings.Builder
		b.WriteString(m.wrapped(styles.title, "Export: secrets") + "\n\n")
		for i, row := range exportModeRows {
			text := row
			blocked := i > 0 && reason != ""
			if blocked {
				text += " (not possible)"
			}
			line := m.listRow(i == e.modeCursor, false, truncateCells(text, m.usable(2)))
			if blocked && i != e.modeCursor {
				line = styles.hint.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if reason != "" {
			b.WriteString("\n" + m.indented(reason) + "\n")
		} else {
			b.WriteString("\n" + m.indented("the secrets are read under the admin session; their values are never shown") + "\n")
		}
		return b.String() + m.keyHint("up/down move · {save} or enter choose · {back} back · {command} cmds · {help} help") + m.notes()
	case exportPassphrase:
		hint, title := "it protects the file, is not the vault passphrase, and is needed to import it; type it twice", "Export passphrase"
		if e.confirming {
			title, hint = "Confirm the export passphrase", "type it again to confirm it"
		}
		return m.wrapped(styles.title, title) + "\n\n" + m.inputLine("  ", e.input.Model) + "\n" + m.indented(hint) + "\n" +
			m.keyHint("{save} or enter ok · {back} back, nothing written") + m.notes()
	case exportPlainConfirm:
		return m.wrapped(styles.title, "Write the secrets unencrypted?") + "\n\n" +
			m.indentedWith(styles.warning, "warning: the file then holds every included secret in plain text; anyone "+
				"who can read the file can use them. Delete it once it is imported.") + "\n" +
			m.keyHint("y write unencrypted · n/{back} back, nothing written · {command} cmds · {help} help") + m.notes()
	case exportTarget:
		return m.wrapped(styles.title, "Export: file") + "\n\n" + m.pathLines("  ", &e.input) +
			m.indented("the file to write; it is created for you alone and an existing file is never replaced") + "\n" +
			m.keyHint(pathKeys(&e.input, "{save} or enter preview · {back} back")) + m.notes()
	}
	title, question, keys := "Export preview", "Nothing is written until you confirm.", "{save} write · {back} back · up/down scroll · {command} cmds · {help} help"
	if e.step == exportResult {
		title, question, keys = "Export written", "", "enter or {back} done · up/down scroll · {command} cmds · {help} help"
	}
	if e.writing {
		keys = "writing, please wait"
	}
	return m.linesView(title, question, keys, e.lines, &e.offset)
}

// linesView draws a preview or a result: the lines scrolled to *offset, which it clamps, between a title
// and the question and keys of the step. A line that starts with "warning" is drawn as one.
func (m *Model) linesView(title, question, keys string, lines []string, offset *int) string {
	head := m.wrapped(styles.title, title) + "\n\n"
	foot := "\n"
	if question != "" {
		foot += m.wrapped(styles.hint, question)
	}
	foot += m.keyHint(keys) + m.notes()
	var body []string
	for _, line := range lines {
		style := styles.hint
		if strings.HasPrefix(line, "warning") {
			style = styles.warning
		}
		body = append(body, strings.Split(m.wrapped(style, line), "\n")...)
	}
	room := max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 3)
	*offset = max(min(*offset, len(body)-room), 0)
	return head + strings.Join(body[*offset:min(*offset+room, len(body))], "\n") + foot
}

// exportLeaveView is the leave question over an unfinished export.
func (m *Model) exportLeaveView() string {
	save := "{save} next step"
	if m.export != nil && m.export.step == exportPreview {
		save = "{save} write"
	}
	title, discard, why := "Leave the export?", "d discard", "Leaving drops the ticks and every answer, a typed passphrase included."
	if m.leaveQuit {
		title, discard, why = "Quit and drop the export?", "d discard and quit", "Quitting would drop them."
	}
	keys := save + " · " + discard + " · {back} keep editing · {command} cmds · {help} help"
	if m.export != nil && m.export.step == exportPlainConfirm {
		// F2 does not answer the plain text question; only y does.
		keys = discard + " · {back} keep editing · {command} cmds · {help} help"
	}
	return m.questionText(question{title: title, consequence: why,
		details: []string{m.indentedWith(styles.warning, "warning: the export is not finished"),
			m.indented("Nothing was written.")},
		keys: keys})
}
