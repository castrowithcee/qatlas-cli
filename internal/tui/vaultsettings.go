package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Vault actions: what a fieldVaultAction row of the vault form runs on enter.
const (
	vaultActionEncrypt    = "encrypt"
	vaultActionPassphrase = "passphrase"
	vaultActionDecrypt    = "decrypt"
)

const updateBehaviourLabel = "update behaviour"

// vaultActionDone is the status line once an action finished without error.
func vaultActionDone(action string) string {
	switch action {
	case vaultActionEncrypt:
		return tr(tVaultDoneEncrypt)
	case vaultActionPassphrase:
		return tr(tVaultDonePassphrase)
	case vaultActionDecrypt:
		return tr(tVaultDoneDecrypt)
	}
	return ""
}

// vaultActionMsg carries the outcome of Encrypt, ChangePassphrase, or Decrypt back into the event loop.
// procNote is set only for ChangePassphrase and Decrypt, which lock a running vault process first (see
// manage.Service.LockVaultProcess): what that found, in the editor's own short words, "" when there was nothing to
// lock.
type vaultActionMsg struct {
	action   string
	err      error
	procNote string
}

// openVaultForm opens the vault section: a settings form without a list, over the vault's own state, built
// fresh every time so it never shows anything but what the vault and the configuration hold right now. It
// never clears status or fail itself, so a caller that just set one to report what it did is not undone by
// the rebuild; showList clears them before opening the section fresh from the tab line or a list.
func (m *Model) openVaultForm() tea.Cmd {
	m.editing = ""
	m.screen = screenForm
	m.fields = m.vaultFields()
	m.focus = m.firstEditable()
	m.applyFocus()
	m.pristine = m.formState()
	return nil
}

// vaultFields builds the rows of the vault form: the state, the actions that apply to it, and the two
// timeouts, which are config, not vault-file state, and so are offered whatever the vault's state is.
func (m *Model) vaultFields() []field {
	fields := []field{{label: "state", kind: fieldVaultState, readOnly: true}}

	switch m.vaultState() {
	case vault.StateUnencrypted:
		fields = append(fields, vaultActionField(vaultActionEncrypt, "encrypt", tr(tVaultEncryptHint), tr(tVaultEncryptDetail)))
	case vault.StateLocked, vault.StateUnlocked:
		fields = append(fields,
			vaultActionField(vaultActionPassphrase, "change passphrase", tr(tVaultPassphraseHint), tr(tVaultPassphraseDetail)),
			vaultActionField(vaultActionDecrypt, "turn off encryption", tr(tVaultDecryptHint), tr(tVaultDecryptDetail)))
	}

	if len(m.legacyEntries()) > 0 {
		fields = append(fields, vaultActionField(vaultActionMigrate, "migrate credentials.yaml", tr(tVaultMigrateHint), tr(tVaultMigrateDetail)))
	}

	fields = append(fields, m.updateBehaviourField())
	fields = append(fields,
		textField("idle timeout", m.cfg.Vault.IdleTimeout, false).withHint(tr(tVaultIdleHint)).withDetail(tr(tVaultIdleDetail)),
		textField("admin timeout", m.cfg.Vault.AdminTimeout, false).withHint(tr(tVaultAdminHint)).withDetail(tr(tVaultAdminDetail)),
	)
	return fields
}

// updateBehaviourField is the "update behaviour" row: handover or lock, stored authenticated in the encrypted
// vault's settings.age, never in config.yaml. It is a choice only while this run holds the vault unlocked,
// the only time the stored value can be read and a change written; otherwise it is a read-only row that
// names why.
func (m *Model) updateBehaviourField() field {
	m.handoverShown = ""
	reason := ""
	switch m.vaultState() {
	case vault.StateUnlocked:
		behaviour, err := m.secrets.Vault().UpdateBehaviour()
		if err != nil && !errors.Is(err, vault.ErrSettingsUntrusted) {
			reason = m.redactor.Apply(err.Error())
			break
		}
		m.handoverShown = string(behaviour)
		f := choiceField(updateBehaviourLabel, []string{string(vault.UpdateHandover), string(vault.UpdateLock)},
			string(behaviour))
		f.hint, f.detail = tr(tUpdateBehaviourHint), tr(tUpdateBehaviourDetail)
		if err != nil {
			f.hint = tr(tUpdateBehaviourUntrustedHint)
			f.detail = tr(tUpdateBehaviourUntrustedDetail, tr(tUpdateBehaviourDetail))
		}
		return f
	case vault.StateLocked:
		reason = tr(tVaultLockedUnlockKey)
	default:
		reason = tr(tVaultNeedsPassphrase)
	}
	return textField(updateBehaviourLabel, tr(tNotAvailable), true).withHint(reason)
}

// vaultActionField is one action row of the vault form: it runs at once on enter, never deferred to F2.
func vaultActionField(action, label, hint, detail string) field {
	return field{label: label, kind: fieldVaultAction, action: action, hint: hint, detail: detail}
}

// vaultState is the vault's current state, StateAbsent when this run has no vault configured at all or
// reading it failed; either way there is nothing more specific to act on.
func (m *Model) vaultState() vault.State {
	v := m.secrets.Vault()
	if v == nil {
		return vault.StateAbsent
	}
	state, err := v.State()
	if err != nil {
		return vault.StateAbsent
	}
	return state
}

// vaultStateText is what the state row of the vault form shows.
func (m *Model) vaultStateText() string {
	v := m.secrets.Vault()
	if v == nil {
		return tr(tVaultNone)
	}
	state, err := v.State()
	if err != nil {
		return m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent:
		return tr(tVaultStateNone)
	case vault.StateUnencrypted:
		return tr(tVaultStateUnencrypted)
	case vault.StateLocked:
		return tr(tVaultStateLocked)
	case vault.StateUnlocked:
		return tr(tVaultStateUnlocked)
	}
	return string(state)
}

// vaultStateMark is the state row of the vault form as a status mark: the colour of the dot follows the state,
// the word is the one vaultStateText gives.
func (m *Model) vaultStateMark() string {
	text := m.vaultStateText()
	v := m.secrets.Vault()
	if v == nil {
		return statusMark(statusDim, text)
	}
	state, err := v.State()
	switch {
	case err != nil:
		return statusMark(statusFail, text)
	case state == vault.StateUnlocked:
		return statusMark(statusOK, text)
	case state == vault.StateLocked || state == vault.StateUnencrypted:
		return statusMark(statusWarn, text)
	}
	return statusMark(statusDim, text)
}

// vaultStateWarning names the one state worth a person's attention, in the same words 'qatlas vault status'
// uses, so the two never drift apart.
func (m *Model) vaultStateWarning() string {
	v := m.secrets.Vault()
	if v == nil {
		return ""
	}
	if state, err := v.State(); err != nil || state != vault.StateUnencrypted {
		return ""
	}
	return tr(tVaultStateWarning)
}

// saveVault saves the update behaviour into the vault when its row was moved, and vault.idle_timeout and
// vault.admin_timeout into the configuration, the only rows F2 ever saves on this form; every action row
// already ran on its own enter (see runVaultActionField).
func (m *Model) saveVault() tea.Cmd {
	candidate := m.cfg.Clone()
	candidate.Vault = config.VaultSettings{
		IdleTimeout:  m.fieldValue("idle timeout"),
		AdminTimeout: m.fieldValue("admin timeout"),
	}
	timeoutsChanged := candidate.Vault != m.cfg.Vault
	if chosen := m.fieldValue(updateBehaviourLabel); m.handoverShown != "" && chosen != m.handoverShown {
		if err := m.secrets.Vault().SetUpdateBehaviour(vault.UpdateBehaviour(chosen)); err != nil {
			m.fail = m.redactor.Apply(err.Error())
			return nil
		}
		m.handoverShown = chosen
		if !timeoutsChanged {
			// Only the vault changed; config.yaml stays exactly as it is.
			m.clearMessages()
			m.status = tr(tSaved)
			return m.openVaultForm()
		}
	}
	// Only the vault settings differ, so no connection changes and nothing is logged.
	m.problems = problemState{}
	if _, err := m.svc.SaveConfig(m.cfg, candidate, m.rev); err != nil {
		if !m.conflicted(err) {
			m.reportSaveError(err, "", true)
		}
		return nil
	}
	m.adoptSaved(candidate)
	// openVaultForm never clears status or fail on its own (see its comment), so a fail left over from an
	// earlier, refused attempt on this same form has to be cleared here before it is replaced with success.
	m.clearMessages()
	m.status = tr(tSaved)
	return m.openVaultForm()
}

// runVaultActionField starts the flow of one action row.
func (m *Model) runVaultActionField(action string) tea.Cmd {
	if cmd, ok := m.runPageAction(action); ok {
		return cmd
	}
	if m.vaultBusy {
		// A vault write, or the prompt that may lead to one, is already in flight: starting a second one
		// here would race it over the same files.
		m.status = tr(tVaultWriteBusy)
		return nil
	}
	switch action {
	case vaultActionEncrypt:
		return m.startVaultEncrypt()
	case vaultActionPassphrase:
		return m.startVaultChangePassphrase()
	case vaultActionDecrypt:
		return m.startVaultDecrypt()
	case vaultActionMigrate:
		return m.startVaultMigrate()
	}
	return nil
}

// cancelVaultPrompt is the cancel callback every vault settings prompt shares: back to the form, unchanged.
func (m *Model) cancelVaultPrompt() tea.Cmd {
	m.screen = screenForm
	m.status = tr(tCancelled)
	return nil
}

// startVaultEncrypt asks for a new passphrase, typed masked and twice, and turns encryption on with it.
func (m *Model) startVaultEncrypt() tea.Cmd {
	m.openVaultOffer(
		tr(tVaultEncryptingTitle),
		tr(tVaultEncryptingHint),
		false, true,
		func(offer vault.PassphraseFunc) tea.Cmd {
			passphrase, _ := offer("")
			m.screen = screenForm
			return m.runVaultEncrypt(passphrase)
		},
		m.cancelVaultPrompt,
	)
	return nil
}

func (m *Model) runVaultEncrypt(passphrase string) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyEncrypting)
	store := m.store
	return func() tea.Msg {
		if err := v.Encrypt(passphrase); err != nil {
			return vaultActionMsg{action: vaultActionEncrypt, err: err}
		}
		return vaultActionMsg{action: vaultActionEncrypt, procNote: approveOnEncrypt(store, v)}
	}
}

// approveOnEncrypt approves every saved connection that reads a secret the vault just encrypted, the same
// way 'qatlas vault encrypt' does: the passphrase was proven a moment ago. It returns what to add to the
// outcome; a configuration that cannot be read approves nothing and says so, and the encryption stands.
func approveOnEncrypt(store *config.Store, v *vault.Vault) string {
	if store == nil {
		return ""
	}
	res := manage.ApproveOnEncrypt(context.Background(), v, store.Load)
	switch {
	case res.ConfigErr != nil:
		return tr(tApproveOnEncryptConfigErr, res.ConfigErr.Error())
	case res.ApproveErr != nil:
		return tr(tApproveOnEncryptErr, res.ApproveErr.Error())
	case res.Warning != "":
		return tr(tWarningPrefix, res.Warning)
	case len(res.Approved) == 0:
		return ""
	}
	return tr(tApproveOnEncryptDone, len(res.Approved), strings.Join(res.Approved, ", "))
}

// startVaultChangePassphrase asks for the current passphrase once, verifies it, then chains into asking for
// the new one twice, and changes the passphrase with both. See startVaultCurrentPassphrase for why the
// current passphrase is checked before anything else is asked.
func (m *Model) startVaultChangePassphrase() tea.Cmd {
	return m.startVaultCurrentPassphrase(
		tr(tVaultCurrentTitle),
		tr(tVaultCurrentHintNew),
		m.startVaultNewPassphrase,
	)
}

// vaultPassphraseVerifiedMsg carries the outcome of checking the vault's current passphrase before asking
// for anything a wrong one would make pointless: a new passphrase typed twice, or the explicit confirmation
// before turning encryption off. See startVaultCurrentPassphrase.
type vaultPassphraseVerifiedMsg struct {
	passphrase  string
	err         error
	title, hint string
	next        func(passphrase string) tea.Cmd
}

// startVaultCurrentPassphrase asks for the vault's current passphrase and verifies it asynchronously, since
// that decrypts key.age, before calling next with it. A wrong passphrase reopens this very prompt with the
// error shown, instead of moving on to ask for a new passphrase or an explicit confirmation that would only
// be typed for nothing; esc still cancels back to the vault form, unchanged, at any point. It is how
// 'change passphrase' and 'turn off encryption' both start.
func (m *Model) startVaultCurrentPassphrase(title, hint string, next func(string) tea.Cmd) tea.Cmd {
	m.openVaultOffer(title, hint, false, false,
		func(offer vault.PassphraseFunc) tea.Cmd {
			current, _ := offer("")
			// The offer's own prompt is gone the moment resume runs (see updateVaultOffer); the form is what
			// shows the check running, the same way it shows every other vault write in flight.
			m.screen = screenForm
			return m.verifyVaultPassphrase(current, title, hint, next)
		},
		m.cancelVaultPrompt,
	)
	return nil
}

// verifyVaultPassphrase checks current against the vault's key.age, without unlocking or changing anything
// else (see vault.Vault.VerifyPassphrase), as a command so the scrypt work it does never runs on the event
// loop; vaultBusy blocks a second vault write, or another such offer, from starting before it is done.
func (m *Model) verifyVaultPassphrase(current, title, hint string, next func(string) tea.Cmd) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.busy = tr(tBusyVerifying)
	return func() tea.Msg {
		return vaultPassphraseVerifiedMsg{passphrase: current, err: v.VerifyPassphrase(current),
			title: title, hint: hint, next: next}
	}
}

// handleVaultPassphraseVerified decides what startVaultCurrentPassphrase's caller asked for once the check
// answers. A wrong passphrase reopens the same prompt with the short error every wrong vault passphrase is
// reported with; any other failure, such as an unreadable key.age, ends the flow on the form the same way
// every other vault action failure does. The right passphrase starts, or renews, this window's admin session
// (see startAdminSession) and runs next with it: this check is every bit the proof of admin requireAdmin's
// own dialog would ask for, and asking twice over would only add friction, not security.
func (m *Model) handleVaultPassphraseVerified(msg vaultPassphraseVerifiedMsg) tea.Cmd {
	m.vaultBusy = false
	m.busy = ""
	if msg.err != nil {
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			cmd := m.startVaultCurrentPassphrase(msg.title, msg.hint, msg.next)
			m.fail = tr(tPassphraseWrong)
			return cmd
		}
		m.fail = m.redactor.Apply(msg.err.Error())
		return nil
	}
	m.startAdminSession()
	return msg.next(msg.passphrase)
}

func (m *Model) startVaultNewPassphrase(current string) tea.Cmd {
	m.openVaultOffer(
		tr(tVaultNewTitle),
		tr(tVaultNewHint),
		false, true,
		func(offer vault.PassphraseFunc) tea.Cmd {
			newPassphrase, _ := offer("")
			m.screen = screenForm
			return m.runVaultChangePassphrase(current, newPassphrase)
		},
		m.cancelVaultPrompt,
	)
	return nil
}

func (m *Model) runVaultChangePassphrase(current, newPassphrase string) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyChangingPassphrase)
	svc := m.svc
	return func() tea.Msg {
		// A passphrase is changed because the old one should no longer open the vault; a vault process
		// unlocked with it would keep the vault open regardless, so it is locked first, the way 'qatlas
		// vault passphrase' already does.
		note := svc.LockVaultProcess(context.Background(), "", "unlock it again with 'qatlas vault unlock'")
		return vaultActionMsg{
			action: vaultActionPassphrase, err: v.ChangePassphrase(current, newPassphrase), procNote: note,
		}
	}
}

// startVaultDecrypt asks for the current passphrase once, verifies it, then the explicit y/n confirmation,
// and turns encryption off with the passphrase once it is answered y. See startVaultCurrentPassphrase for
// why the current passphrase is checked before the confirmation is ever asked.
func (m *Model) startVaultDecrypt() tea.Cmd {
	return m.startVaultCurrentPassphrase(
		tr(tVaultCurrentTitle),
		tr(tVaultCurrentHintOff),
		func(current string) tea.Cmd {
			m.decryptPassphrase = current
			m.decryptConfirm = true
			m.screen = screenConfirm
			m.clearMessages()
			return nil
		},
	)
}

func (m *Model) runVaultDecrypt(passphrase string) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyDecrypting)
	svc := m.svc
	return func() tea.Msg {
		// Once the vault is decrypted, its recipient is gone and a vault process could not be checked, nor
		// therefore asked to lock, any more; it would hold the secrets until its idle timeout. Locking first
		// mirrors 'qatlas vault decrypt'.
		note := svc.LockVaultProcess(context.Background(), "", "the unencrypted vault needs no unlocking")
		return vaultActionMsg{action: vaultActionDecrypt, err: v.Decrypt(passphrase), procNote: note}
	}
}

// handleVaultAction applies the outcome of Encrypt, ChangePassphrase, or Decrypt. A wrong passphrase is
// reported the same short way every masked prompt of this editor would, never with the passphrase itself;
// the form stays open on every outcome, success included, freshly rebuilt from the vault's new state.
func (m *Model) handleVaultAction(msg vaultActionMsg) tea.Cmd {
	m.vaultBusy = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}

	if msg.err != nil {
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			m.fail = tr(tPassphraseWrong)
		} else {
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		if msg.procNote != "" {
			m.fail += "; " + msg.procNote
		}
	} else {
		m.status = vaultActionDone(msg.action)
		if msg.procNote != "" {
			m.status += "; " + msg.procNote
		}
		// Encrypting the vault, changing its passphrase, or verifying it before turning encryption off is
		// every bit the proof of admin requireAdmin's own dialog asks for (see startAdminSession): a person
		// who just typed the vault's passphrase for one of these must not be asked for it again a moment
		// later for an unrelated save.
		m.startAdminSession()
	}

	if m.section != sectionVault || m.screen != screenForm {
		return nil
	}
	// The action changed the vault's state; the form is rebuilt so its rows, the action rows included, show
	// what applies now. openVaultForm never clears status or fail, so the outcome just set above survives.
	return m.openVaultForm()
}
