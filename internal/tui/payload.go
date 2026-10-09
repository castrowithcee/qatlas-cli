// The payload secrets of the Credentials section: a credential with forward: true holds freely named
// fields whose values a tool may pass on to a third party by reference, and a connection releases it with
// forward_secrets. Its form is one row per field with a masked value, plus a row that adds a field; the
// values leave the editor only towards the credential store, through manage.Service.CommitSecrets, and are never
// drawn, never put into a message, and never written into the configuration.
package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	// payloadNewLabel is the row that adds a field to a payload secret.
	payloadNewLabel = "new field"
	// forwardLabel is the row of a connection that ticks the payload secrets it releases (forward_secrets).
	forwardLabel = "forward"

	payloadStorageHint = "system keyring keeps the values in the keyring of this machine, vault keeps them in a " +
		"file beside the configuration; a payload secret never uses environment variables"
	payloadDescriptionHint = "optional; what these values are for, e.g. 'login of the billing portal'. discovery " +
		"publishes it, so it must never carry a secret or personal data"
	payloadNewHint = "type a field name (letters, digits, - _ .) and press enter; the row for its value follows"
	// payloadMaskHint takes the shortest value length.
	payloadMaskHint = "typed masked and never shown, at least %d characters; stored only when you save; " +
		"ctrl+d removes this field"
	forwardHint = "forward_secrets: the payload secrets a tool of this connection may pass on by reference; " +
		"enter opens them to tick, none ticked releases none"
)

// payloadSavedMsg carries the outcome of saving a payload secret back into the event loop. It carries no
// value.
type payloadSavedMsg struct {
	cfg     *config.Config
	name    string
	vault   bool
	err     error
	warning string
	// leftover names the removed fields whose stored value could not be removed again.
	leftover []string
}

// isPayloadForm reports whether the open form is that of a payload secret.
func (m *Model) isPayloadForm() bool {
	if m.wizard != nil || m.section != sectionCredentials {
		return false
	}
	for _, f := range m.fields {
		if f.kind == fieldPayloadNew {
			return true
		}
	}
	return false
}

// openPayloadNew is s on the Credentials list: the form of a new payload secret.
func (m *Model) openPayloadNew() tea.Cmd {
	m.editing, m.copyFrom = "", ""
	m.screen = screenForm
	m.clearMessages()
	m.fields = m.payloadFields("", "")
	m.focus = m.firstEditable()
	m.applyFocus()
	m.pristine = m.formState()
	return nil
}

// payloadFields builds the rows of a payload secret: the values come from the credential edit, or, for a
// new one filled from source, from source. A copy never holds a value, so every one of its fields asks for
// one.
func (m *Model) payloadFields(edit, source string) []field {
	name, shown := edit, edit
	if source != "" {
		name, shown = source, m.copyName(source)
	}
	cred := m.cfg.Credentials[name]
	key := textField("name", shown, edit != "").withHint(nameHint)
	if key.readOnly {
		key.hint = lockedHint
	}
	choice := storageKeyring
	if cred.Type == config.CredentialTypeVault ||
		(cred.Type == "" && m.cfg.SecretStore() == config.CredentialTypeVault) {
		choice = storageVault
	}
	storage := choiceField(storageLabel, []string{storageKeyring, storageVault}, choice).
		withHint(payloadStorageHint)
	// Moving the values to the other place is not offered: delete the secret and create it again.
	storage.readOnly = edit != ""
	fields := []field{key, storage,
		textField("description", cred.Description, false).withHint(payloadDescriptionHint)}
	for i, field := range cred.Fields {
		fields = append(fields, m.payloadValueRow(field, edit != "", i == 0))
	}
	add := textField(payloadNewLabel, "", false).withHint(payloadNewHint)
	add.kind = fieldPayloadNew
	return append(fields, add)
}

// payloadValueRow is the masked row of one field. stored marks a field that already has a value: left empty,
// it keeps that value.
func (m *Model) payloadValueRow(name string, stored, lead bool) field {
	f := m.maskedField("", name, lead)
	f.stored = stored
	f.hint = fmt.Sprintf(payloadMaskHint, config.MinForwardValueLength)
	if stored {
		f.hint += "; empty keeps the stored value"
	}
	return f
}

// payloadRows are the indexes of the masked value rows, in order.
func (m *Model) payloadRows() []int {
	var rows []int
	for i, f := range m.fields {
		if f.kind == fieldMasked {
			rows = append(rows, i)
		}
	}
	return rows
}

// payloadKey handles the keys that are specific to a payload form: enter on the new-field row adds a field,
// ctrl+d on a value row removes it.
func (m *Model) payloadKey(key tea.KeyMsg) bool {
	switch current := m.fields[m.focus]; {
	case current.kind == fieldPayloadNew && key.String() == "enter":
		m.addPayloadField()
		return true
	case current.kind == fieldMasked && key.String() == "ctrl+d":
		m.removePayloadField()
		return true
	}
	return false
}

// addPayloadField turns the name typed into the new-field row into a value row.
func (m *Model) addPayloadField() {
	m.clearMessages()
	at := m.focus
	name := strings.TrimSpace(m.fields[at].input.Value())
	if name == "" {
		m.fail = "type the name of the field first"
		return
	}
	for _, f := range m.fields {
		if f.kind == fieldMasked && f.label == name {
			m.fail = fmt.Sprintf("the field %q exists already", name)
			return
		}
	}
	if problem := manage.PayloadFieldProblem(name); problem != "" {
		m.fail = problem
		return
	}
	m.fields[at].input.SetValue("")
	row := m.payloadValueRow(name, false, len(m.payloadRows()) == 0)
	m.fields = slices.Insert(m.fields, at, row)
	m.focus = at
	m.applyFocus()
	m.status = "Added field " + name + "; type its value"
}

// removePayloadField drops the focused value row. A stored value of that field is removed when the form is
// saved.
func (m *Model) removePayloadField() {
	m.clearMessages()
	at := m.focus
	name, stored := m.fields[at].label, m.fields[at].stored
	m.fields = slices.Delete(m.fields, at, at+1)
	if rows := m.payloadRows(); len(rows) > 0 {
		m.fields[rows[0]].roleLead = true
	}
	m.focus = min(at, len(m.fields)-1)
	m.applyFocus()
	m.status = "Removed field " + name
	if stored {
		m.status += "; its stored value is deleted when you save"
	}
}

// savePayload checks the form, offers a passphrase when its first value would create the vault, and commits
// the values and the configuration entry together. It runs inside requireAdmin.
func (m *Model) savePayload() tea.Cmd {
	if m.payloadSaving || m.vaultBusy {
		m.status = "a write is already in progress; wait for it to finish"
		return nil
	}
	m.clearMessages()
	name := m.fields[0].value()
	credType := storageType(m.fieldValue(storageLabel))
	toVault := credType == config.CredentialTypeVault
	cred := config.Credential{Type: credType, Forward: true, Description: m.fieldValue("description")}
	rows := m.payloadRows()
	for _, i := range rows {
		cred.Fields = append(cred.Fields, m.fields[i].label)
	}
	if _, taken := m.cfg.Credentials[name]; taken && m.editing == "" {
		m.fail = fmt.Sprintf("a credential named %q already exists", name)
		return nil
	}
	base := m.rev
	candidate := m.cfg.Clone()
	if err := candidate.SetCredential(name, cred); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	if err := candidate.Validate(); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	values := map[string]string{}
	var roles []string
	for _, i := range rows {
		f := m.fields[i]
		value := f.value()
		if value == "" {
			if f.stored {
				continue
			}
			m.fail = fmt.Sprintf("the field %q needs a value", f.label)
			return nil
		}
		// The same check manage.Service.CommitSecrets repeats before it writes anything; here it answers at once.
		if err := cred.CheckForwardValue(f.label, value); err != nil {
			m.fail = fmt.Sprintf("credential %s: %v", name, err)
			return nil
		}
		values[f.label] = value
		roles = append(roles, f.label)
	}
	commit := func(offer vault.PassphraseFunc) tea.Cmd {
		return m.commitPayload(candidate, base, name, toVault, roles, values, offer)
	}
	if !toVault || len(values) == 0 {
		return commit(nil)
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	state, err := v.State()
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	if state != vault.StateAbsent {
		return commit(nil)
	}
	m.openVaultOffer(
		"Set a passphrase for the vault?",
		"this is the vault's very first secret; a passphrase encrypts it, typed masked and twice, or "+
			"leave this empty and press enter to keep the vault unencrypted",
		true, true,
		func(offer vault.PassphraseFunc) tea.Cmd { m.screen = screenForm; return commit(offer) },
		func() tea.Cmd { m.screen = screenForm; m.status = "Cancelled"; return nil },
	)
	return nil
}

// commitPayload runs the commit as a command: the store may take its time, and a vault's first secret does
// the passphrase work. The values stay in the form until it succeeds, so a failed save can be retried.
func (m *Model) commitPayload(candidate *config.Config, base config.Revision, name string, toVault bool, roles []string,
	values map[string]string, offer vault.PassphraseFunc) tea.Cmd {
	m.payloadSaving = true
	m.writes++
	m.busy = "saving " + name
	if toVault {
		m.vaultBusy = true
	}
	svc, previous, editing := m.svc, m.cfg, m.editing
	return func() tea.Msg {
		saved, err := svc.SavePayload(manage.PayloadSave{
			Previous: previous, Base: base, Candidate: candidate, Name: name, Editing: editing,
			Roles: roles, Values: values, Offer: offer,
		})
		return payloadSavedMsg{cfg: candidate, name: name, vault: toVault, err: err,
			warning: saved.Warning, leftover: saved.Leftover}
	}
}

// handlePayloadSaved applies the outcome of savePayload. A failure keeps the form with every input; a
// success drops the typed values with the form.
func (m *Model) handlePayloadSaved(msg payloadSavedMsg) tea.Cmd {
	m.payloadSaving = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}
	if msg.vault {
		m.vaultBusy = false
	}
	if msg.err != nil {
		if !m.conflicted(msg.err) {
			m.fail = m.redactor.Apply(msg.err.Error()) + "; the configuration was not changed"
		}
		return nil
	}
	m.adoptSaved(msg.cfg)
	m.fields = nil
	cmd := m.returnToList(msg.name)
	m.status = "Saved payload secret " + msg.name
	if msg.warning != "" {
		m.status += "; " + msg.warning
	}
	for _, field := range msg.leftover {
		m.status += "; " + manage.LeftoverNote(msg.name, field)
	}
	m.status += m.forwardOpenNote(msg.name)
	return cmd
}

// forwardOpenNote names the connections that release the payload secret and now wait for a person's
// approval. Saving a payload secret never approves anything: a changed release is a decision of its own,
// made in Approvals.
func (m *Model) forwardOpenNote(name string) string {
	open := m.svc.ForwardOpen(m.cfg, name)
	if len(open) == 0 {
		return ""
	}
	return fmt.Sprintf("; %s release(s) it and now wait for approval, which saving does not give: review "+
		"them in Approvals (%d)", strings.Join(open, ", "), int(sectionApprovals)+1)
}

// forwardField is the forward_secrets row of a connection form, ticked with the connection's own list. A
// listed credential that no longer exists stays visible as a choice, so the core refuses it by name instead
// of the editor dropping it unseen.
func (m *Model) forwardField(current []string) field {
	choices := manage.ForwardChoices(m.cfg)
	for _, name := range current {
		if !slices.Contains(choices, name) {
			choices = append(choices, name)
		}
	}
	f := field{label: forwardLabel, kind: fieldToolList, choices: choices, selected: map[string]bool{}, hint: forwardHint}
	for _, name := range current {
		f.selected[name] = true
	}
	return f
}

// wantsForwardRow reports whether a connection form carries the forward row: only while a payload secret
// exists or the connection lists one, so a configuration without any keeps its form as it was.
func (m *Model) wantsForwardRow(conn config.Connection) bool {
	return len(manage.ForwardChoices(m.cfg)) > 0 || len(conn.ForwardSecrets) > 0
}

// forwardBindingNote says when no approval binds the release of a payload secret, so that only the
// forward_secrets list of the configuration guards it, and "" when one does. connCredential is the credential
// of the connection the note is about, "" for the payload secret's own form, where that is not known yet.
func (m *Model) forwardBindingNote(connCredential string) string {
	const only = "only the forward_secrets list in this configuration limits which payload secrets a tool may use"
	switch m.svc.ForwardBinding(m.cfg, connCredential) {
	case manage.ForwardUnbound:
		return "no encrypted vault binds this release: " + only
	case manage.ForwardCredentialOutside:
		return "this connection's own credential is not in the encrypted vault, so no approval binds this " +
			"release: " + only
	}
	return ""
}

// payloadKeys is the key line of the focused row of a payload form, "" for the rows that use the default.
func (m *Model) payloadKeys() string {
	switch m.fields[m.focus].kind {
	case fieldPayloadNew:
		return "enter add field · tab move · " + formKeys
	case fieldMasked:
		return "ctrl+d remove field · tab move · enter next · " + formKeys
	}
	return ""
}

// payloadCells are the cells of a payload secret in the Credentials list: its fields, with where each value
// sits for a vault credential. A keyring credential names its fields only, since asking the keyring for each
// would block the list.
func (m *Model) payloadCells(name string, cred config.Credential) []string {
	place := placeKeyring
	if cred.Type == config.CredentialTypeVault {
		place = placeVault
	}
	parts := make([]string, 0, len(cred.Fields))
	for _, field := range cred.Fields {
		if cred.Type == config.CredentialTypeVault {
			parts = append(parts, field+" "+secretState(m.vaultRoleState(name, field)))
			continue
		}
		parts = append(parts, field)
	}
	return []string{name, "(payload)", place, strings.Join(parts, ", ")}
}
