package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
	// offset is the first shown line of a dry run longer than the screen.
	offset int
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
		m.status = "Wait until " + work + " finishes, then press r again."
		return nil
	}
	return m.requireAdmin(func() tea.Cmd {
		in := textinput.New()
		in.Prompt = ""
		in.Cursor.SetMode(cursor.CursorStatic)
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
	status := "Cancelled; nothing was renamed"
	back := screenList
	if r := m.rename; r != nil {
		back = r.back
		if r.plan != nil && r.plan.Adopt {
			status = "Cancelled; nothing was adopted"
		} else if r.plan != nil && r.plan.Revert {
			status = "Cancelled; nothing was put back"
		}
	}
	m.rename = nil
	m.screen = screenList
	if back == screenFindings {
		m.reopenFindings()
	}
	m.status = status
}

func (m *Model) updateRename(key tea.KeyMsg) tea.Cmd {
	r := m.rename
	if r == nil {
		m.screen = screenList
		return nil
	}
	if r.plan != nil {
		switch key.String() {
		case "y":
			return m.requireAdmin(m.runRename)
		case "n", "esc":
			m.cancelRename()
		case "down":
			r.offset++
		case "up":
			r.offset = max(r.offset-1, 0)
		case "pgdown":
			r.offset += max(m.height/2, 1)
		case "pgup":
			r.offset = max(r.offset-max(m.height/2, 1), 0)
		case "home":
			r.offset = 0
		case "end":
			r.offset = 1 << 20
		}
		return nil
	}
	switch key.String() {
	case "esc":
		m.cancelRename()
		return nil
	case "enter":
		return m.requireAdmin(m.planRename)
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(key)
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
	r.plan, r.offset = plan, 0
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
	parts := []string{fmt.Sprintf("Renamed %s %s to %s", plan.Kind, apply(plan.Old), apply(plan.New))}
	carried := "approvals carried over: "
	switch {
	case plan.Revert:
		parts = []string{fmt.Sprintf("Put credential %s back as it was before the rename to %s", apply(plan.Old),
			apply(plan.New))}
		carried = "approvals carried back: "
	case plan.Adopt:
		parts = []string{fmt.Sprintf("Adopted the rename of %s %s to %s", plan.Kind, apply(plan.Old), apply(plan.New))}
	}
	if len(res.VaultCopies) > 0 {
		parts = append(parts, "vault files backed up as "+strings.Join(res.VaultCopies, ", "))
	}
	if len(res.Carried) > 0 {
		parts = append(parts, carried+apply(strings.Join(res.Carried, ", ")))
	}
	if len(res.EnvVars) > 0 {
		pairs := make([]string, len(res.EnvVars))
		for i, env := range res.EnvVars {
			pairs[i] = env.Old + " -> " + env.New
		}
		parts = append(parts, "environment variables are read under the new name: "+apply(strings.Join(pairs, ", "))+
			"; qatlas changed no environment")
	}
	for _, warning := range res.Warnings {
		parts = append(parts, "warning: "+apply(warning))
	}
	return strings.Join(parts, "; ")
}

// renameLines are the places the dry run changes or leaves alone, one per line, in the order of
// 'qatlas config rename'. A line that starts with "warning" is drawn as one.
func (m *Model) renameLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	if plan.Revert {
		return m.revertLines(plan)
	}
	lines := []string{fmt.Sprintf("id        %s, kept", plan.ID)}
	if plan.Adopt {
		// The configuration carries the new name already and is written as it is.
		lines = append(lines, fmt.Sprintf("config    %ss.%s is there already; written as it is", plan.Kind,
			apply(plan.New)))
		for _, ref := range plan.References {
			lines = append(lines, "changed   "+apply(ref)+", by hand")
		}
	} else {
		lines = append(lines, fmt.Sprintf("entry     %ss.%s -> %ss.%s", plan.Kind, apply(plan.Old), plan.Kind,
			apply(plan.New)))
		for _, ref := range plan.References {
			lines = append(lines, "rewrites  "+apply(ref))
		}
	}
	switch plan.Kind {
	case manage.RenameConnection:
		lines = append(lines, "approval  "+m.renameApprovalText(plan))
	case manage.RenameCredential:
		lines = append(lines, m.renameCredentialLines(plan)...)
	}
	for _, token := range plan.LapsedTokens {
		if plan.Kind == manage.RenameCredential {
			lines = append(lines, fmt.Sprintf("warning   agent token %s has a vorbild whose approval of %s is not "+
				"carried over; it no longer covers anything through it", apply(token), apply(plan.Old)))
			continue
		}
		lines = append(lines, fmt.Sprintf("warning   agent token %s names %s as a vorbild and no longer covers "+
			"anything through it", apply(token), apply(plan.Old)))
	}
	for _, outside := range plan.Outside {
		lines = append(lines, "outside   "+apply(outside)+", not changed")
	}
	return lines
}

// revertLines are the places putting back a credential's stopped rename changes, in the order of 'qatlas
// config rename credential --revert'.
func (m *Model) revertLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	lines := []string{fmt.Sprintf("id        %s", plan.ID)}
	switch {
	case plan.VaultUnread:
		lines = append(lines, "vault     not read: the vault is locked")
	case plan.VaultEntry:
		lines = append(lines, fmt.Sprintf("vault     entry %s -> %s, keeping its id and its secrets",
			apply(plan.New), apply(plan.Old)))
	}
	if plan.PendingWrites > 0 {
		lines = append(lines, fmt.Sprintf("pending   %d pending write(s) for %s merged into the entry first",
			plan.PendingWrites, apply(plan.New)))
	}
	for _, role := range plan.KeyringRoles {
		lines = append(lines, fmt.Sprintf("keyring   removes %s/%s, a copy of %s/%s", apply(plan.New), role,
			apply(plan.Old), role))
	}
	for _, reader := range plan.Readers {
		text := "no approval in the vault"
		switch {
		case reader.Carried:
			text = "approval carried back to " + apply(plan.Old)
		case plan.VaultUnread:
			text = "approval not read"
		case reader.Approval == manage.ApprovalValid:
			text = "approved; stays"
		case reader.Approval == manage.ApprovalOpen:
			text = "open; stays open until approved"
		}
		lines = append(lines, fmt.Sprintf("reader    %s: %s", apply(reader.Connection), text))
	}
	return append(lines, "config    not changed")
}

func (m *Model) renameCredentialLines(plan *manage.RenamePlan) []string {
	apply := m.redactor.Apply
	var lines []string
	if plan.Resumes {
		lines = append(lines, fmt.Sprintf("resumes   a rename to %s that stopped before the configuration was "+
			"saved; what it already moved and carried over stays", apply(plan.New)))
	}
	for _, role := range plan.KeyringRoles {
		lines = append(lines, fmt.Sprintf("keyring   %s/%s -> %s/%s", apply(plan.Old), role, apply(plan.New), role))
	}
	switch {
	case plan.VaultUnread:
		lines = append(lines, "vault     not read: the vault is locked")
	case plan.VaultEntry:
		lines = append(lines, fmt.Sprintf("vault     entry %s -> %s, keeping its id and its secrets",
			apply(plan.Old), apply(plan.New)))
	}
	if plan.PendingWrites > 0 {
		lines = append(lines, fmt.Sprintf("pending   %d pending write(s) for %s merged into the entry first",
			plan.PendingWrites, apply(plan.Old)))
	}
	for _, reader := range plan.Readers {
		text := m.renameReaderText(reader)
		if plan.VaultUnread {
			text = "approval not read"
		}
		lines = append(lines, fmt.Sprintf("reader    %s: %s", apply(reader.Connection), text))
	}
	for _, env := range plan.EnvVars {
		lines = append(lines, fmt.Sprintf("env       %s -> %s (%s)", apply(env.Old), apply(env.New), env.Role))
	}
	return lines
}

func (m *Model) renameReaderText(reader manage.RenameReader) string {
	name := m.redactor.Apply(reader.Connection)
	switch {
	case reader.Approval == "":
		return "no approval to carry over"
	case reader.Carried:
		return "approved; carried over, so " + name + " stays approved"
	case reader.Approval == manage.ApprovalOpen:
		return "open; stays open until approved"
	}
	return "approved; not carried over, so " + name + " needs approval again"
}

func (m *Model) renameApprovalText(plan *manage.RenamePlan) string {
	name := m.redactor.Apply(plan.New)
	switch {
	case plan.VaultUnread:
		return "not read: the vault is locked"
	case plan.Approval == "":
		return "none to carry over"
	case plan.CarryApproval && plan.Approval == manage.ApprovalOpen:
		return "open; carried over, so " + name + " stays open until approved"
	case plan.CarryApproval:
		return "approved; carried over, so " + name + " stays approved"
	}
	return plan.Approval + "; not carried over, so " + name + " needs approval again"
}

// renameView draws the name prompt, then the dry run with the question.
func (m *Model) renameView() string {
	r := m.rename
	if r == nil {
		return ""
	}
	apply := m.redactor.Apply
	if r.plan == nil {
		return m.wrapped(styles.title, "Rename "+r.kind+" "+apply(r.old)) + "\n\n" +
			m.inputLine("  ", r.input) + "\n" +
			m.indented("the new name; the next step lists everything the rename touches and changes nothing") + "\n" +
			m.keyHint("enter dry run · esc cancel") + m.notes()
	}
	title, confirm := fmt.Sprintf("Rename %s %s to %s?", r.kind, apply(r.old), apply(r.plan.New)), "y rename"
	switch {
	case r.plan.Revert:
		title = fmt.Sprintf("Put credential %s back, undoing the stopped rename to %s?", apply(r.old), apply(r.plan.New))
		confirm = "y put back"
	case r.plan.Adopt:
		title = fmt.Sprintf("Adopt the rename of %s %s to %s?", r.kind, apply(r.old), apply(r.plan.New))
		confirm = "y adopt"
	}
	head := m.wrapped(styles.title, title) + "\n\n"
	question := "Dry run: nothing is changed until you confirm."
	if n := len(r.plan.LapsedTokens); n > 0 {
		question += fmt.Sprintf(" The rename ends the cover of %d agent token(s) through it.", n)
	}
	keys := confirm + " · n/esc cancel · up/down scroll · : cmds · ? help"
	foot := "\n" + m.wrapped(styles.hint, question) + m.keyHint(keys) + m.notes()
	var body []string
	for _, line := range m.renameLines(r.plan) {
		style := styles.hint
		if strings.HasPrefix(line, "warning") {
			style = styles.warning
		}
		body = append(body, strings.Split(m.wrapped(style, line), "\n")...)
	}
	room := max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 3)
	r.offset = max(min(r.offset, len(body)-room), 0)
	return head + strings.Join(body[r.offset:min(r.offset+room, len(body))], "\n") + foot
}

// renameTyping reports whether the name prompt reads text.
func (m *Model) renameTyping() bool { return m.rename != nil && m.rename.plan == nil }
