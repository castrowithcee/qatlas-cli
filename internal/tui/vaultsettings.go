package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// Vault actions: what a fieldVaultAction row of the vault form runs on enter.
const (
	vaultActionEncrypt    = "encrypt"
	vaultActionPassphrase = "passphrase"
	vaultActionDecrypt    = "decrypt"
)

// Hints of the two vault settings rows every state offers.
const (
	vaultIdleTimeoutHint = "how long a vault process that holds the vault unlocked does so without a read " +
		"before it locks itself, as a Go duration such as \"12h\" or \"30m\"; empty means 12h"
	// vaultAdminTimeoutHint says plainly that this setting has no effect yet: an editor that claimed
	// otherwise would be lying about behaviour it does not have.
	vaultAdminTimeoutHint = "sets vault.admin_timeout for a future admin session of this editor; that " +
		"session is not built yet, so this setting has no effect so far. A Go duration such as \"10m\", or " +
		"\"0\" to ask for the passphrase on every change once it exists; empty means 10m"
)

// vaultActionMsg carries the outcome of Encrypt, ChangePassphrase, or Decrypt back into the event loop.
// procNote is set only for ChangePassphrase and Decrypt, which lock a running vault process first (see
// lockVaultProcessForRekey): what that found, in the editor's own short words, "" when there was nothing to
// lock.
type vaultActionMsg struct {
	action   string
	err      error
	procNote string
}

// vaultActionDone is the status line once an action finished without error.
var vaultActionDone = map[string]string{
	vaultActionEncrypt:    "The vault is encrypted",
	vaultActionPassphrase: "The vault's passphrase is changed",
	vaultActionDecrypt:    "The vault is decrypted; every secret it holds is now stored unencrypted",
}

// openVaultForm opens the vault section: a settings form without a list, over the vault's own state, built
// fresh every time so it never shows anything but what the vault and the configuration hold right now. It
// never clears status or fail itself, so a caller that just set one to report what it did is not undone by
// the rebuild; showList clears them before opening the section fresh from the sidebar or a list.
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
		fields = append(fields, vaultActionField(vaultActionEncrypt, "encrypt",
			"enter turns encryption on: a new passphrase, typed masked and twice"))
	case vault.StateLocked, vault.StateUnlocked:
		fields = append(fields,
			vaultActionField(vaultActionPassphrase, "change passphrase",
				"enter changes the passphrase: the current one once, then a new one typed masked and twice"),
			vaultActionField(vaultActionDecrypt, "turn off encryption",
				"enter turns encryption off: the current passphrase, then an explicit confirmation; every "+
					"secret is then stored unencrypted"))
	}

	if len(m.legacyEntries()) > 0 {
		fields = append(fields, vaultActionField(vaultActionMigrate, "migrate credentials.yaml",
			"enter shows every entry of the plaintext credentials.yaml left over from an earlier version, "+
				"credential and role names only, before carrying it into the vault, and offers to delete the "+
				"file once that is confirmed"))
	}

	fields = append(fields,
		textField("idle timeout", m.cfg.Vault.IdleTimeout, false).withHint(vaultIdleTimeoutHint),
		textField("admin timeout", m.cfg.Vault.AdminTimeout, false).withHint(vaultAdminTimeoutHint),
	)
	return fields
}

// vaultActionField is one action row of the vault form: it runs at once on enter, never deferred to F2.
func vaultActionField(action, label, hint string) field {
	return field{label: label, kind: fieldVaultAction, action: action, hint: hint}
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
		return "no vault is configured for this run"
	}
	state, err := v.State()
	if err != nil {
		return m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent:
		return "no vault yet; it is created automatically the first time a secret is stored in it"
	case vault.StateUnencrypted:
		return "unencrypted"
	case vault.StateLocked:
		return "encrypted, locked"
	case vault.StateUnlocked:
		return "encrypted, unlocked"
	}
	return string(state)
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
	return "the vault is unencrypted: no passphrase was set when its first secret was stored"
}

// saveVault saves vault.idle_timeout and vault.admin_timeout, the only two rows F2 ever saves on this form;
// every action row already ran on its own enter (see runVaultActionField).
func (m *Model) saveVault() tea.Cmd {
	candidate := m.cfg.Clone()
	candidate.Vault = config.VaultSettings{
		IdleTimeout:  m.fieldValue("idle timeout"),
		AdminTimeout: m.fieldValue("admin timeout"),
	}
	if err := m.store.Save(candidate); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	m.cfg = candidate
	m.configExists = true
	// openVaultForm never clears status or fail on its own (see its comment), so a fail left over from an
	// earlier, refused attempt on this same form has to be cleared here before it is replaced with success.
	m.clearMessages()
	m.status = "Saved"
	return m.openVaultForm()
}

// runVaultActionField starts the flow of one action row.
func (m *Model) runVaultActionField(action string) tea.Cmd {
	if m.vaultBusy {
		// A vault write, or the prompt that may lead to one, is already in flight: starting a second one
		// here would race it over the same files.
		m.status = "a vault write is already in progress; wait for it to finish"
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
	m.status = "Cancelled"
	return nil
}

// startVaultEncrypt asks for a new passphrase, typed masked and twice, and turns encryption on with it.
func (m *Model) startVaultEncrypt() tea.Cmd {
	m.openVaultOffer(
		"Set a passphrase for the vault",
		"typed masked and twice; this switches the vault's encryption on",
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
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = "encrypting the vault"
	return func() tea.Msg {
		return vaultActionMsg{action: vaultActionEncrypt, err: v.Encrypt(passphrase)}
	}
}

// startVaultChangePassphrase asks for the current passphrase once, verifies it, then chains into asking for
// the new one twice, and changes the passphrase with both. See startVaultCurrentPassphrase for why the
// current passphrase is checked before anything else is asked.
func (m *Model) startVaultChangePassphrase() tea.Cmd {
	return m.startVaultCurrentPassphrase(
		"Current vault passphrase",
		"verified before the new passphrase is asked",
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
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.busy = "checking the current vault passphrase"
	return func() tea.Msg {
		return vaultPassphraseVerifiedMsg{passphrase: current, err: v.VerifyPassphrase(current),
			title: title, hint: hint, next: next}
	}
}

// handleVaultPassphraseVerified decides what startVaultCurrentPassphrase's caller asked for once the check
// answers. A wrong passphrase reopens the same prompt with the short error every wrong vault passphrase is
// reported with; any other failure, such as an unreadable key.age, ends the flow on the form the same way
// every other vault action failure does. The right passphrase runs next with it.
func (m *Model) handleVaultPassphraseVerified(msg vaultPassphraseVerifiedMsg) tea.Cmd {
	m.vaultBusy = false
	m.busy = ""
	if msg.err != nil {
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			cmd := m.startVaultCurrentPassphrase(msg.title, msg.hint, msg.next)
			m.fail = "wrong passphrase"
			return cmd
		}
		m.fail = m.redactor.Apply(msg.err.Error())
		return nil
	}
	return msg.next(msg.passphrase)
}

func (m *Model) startVaultNewPassphrase(current string) tea.Cmd {
	m.openVaultOffer(
		"New vault passphrase",
		"typed masked and twice",
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
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = "changing the vault's passphrase"
	return func() tea.Msg {
		// A passphrase is changed because the old one should no longer open the vault; a vault process
		// unlocked with it would keep the vault open regardless, so it is locked first, the way 'qatlas
		// vault passphrase' already does.
		note := lockVaultProcessForRekey(v, "unlock it again with 'qatlas vault unlock'")
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
		"Current vault passphrase",
		"verified before encryption is switched off",
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
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = "turning the vault's encryption off"
	return func() tea.Msg {
		// Once the vault is decrypted, its recipient is gone and a vault process could not be checked, nor
		// therefore asked to lock, any more; it would hold the secrets until its idle timeout. Locking first
		// mirrors 'qatlas vault decrypt'.
		note := lockVaultProcessForRekey(v, "the unencrypted vault needs no unlocking")
		return vaultActionMsg{action: vaultActionDecrypt, err: v.Decrypt(passphrase), procNote: note}
	}
}

// syncVaultProcess hands one change already written to the vault on to the vault process that holds it
// unlocked outside this run, exactly the way 'qatlas credential set' and 'qatlas credential delete' already
// do for the CLI (see vaultmigrate.SyncChange): an unsupported platform, an unencrypted vault, or no process
// running needs nothing said. Any other failure becomes a short warning meant for the status line, never
// printed to a terminal and never carrying the secret's own value. It serves a role row's own write or
// delete (see secrets.go), the guided setup's save (see setup.go), and, through vaultMigrateSync, a migrate
// action's whole plan (see migrate.go).
func syncVaultProcess(v *vault.Vault, change func(context.Context, *vaultproc.Client) error) string {
	if !vaultproc.Supported || v == nil {
		return ""
	}
	if err := vaultmigrate.SyncChange(context.Background(), v, change); err != nil {
		return fmt.Sprintf("warning: the vault holds the change, but the vault process that holds it "+
			"unlocked could not take it and still answers with what it held before: %s; %s",
			err, secret.VaultProcessRemedy(err))
	}
	return ""
}

// lockVaultProcessForRekey locks the vault process that holds v unlocked, ahead of ChangePassphrase or
// Decrypt, the same way the CLI's own lockVaultProcess does for 'qatlas vault passphrase' and 'qatlas vault
// decrypt' (see vaultmigrate.LockProcess): the change goes ahead either way. next is what to say the
// process was locked for, in the editor's own shorter words; it returns "" when there was nothing to lock
// (an unsupported platform, an unencrypted vault, or no process running), and a short warning, never on a
// terminal and never naming a secret, when the process refused to be locked.
func lockVaultProcessForRekey(v *vault.Vault, next string) string {
	if !vaultproc.Supported || v == nil {
		return ""
	}
	locked, err := vaultmigrate.LockProcess(context.Background(), v)
	switch {
	case err != nil:
		return fmt.Sprintf("warning: the vault process could not be locked: %s; it keeps the secrets it "+
			"holds until it locks itself, unless you %s", err, secret.EndVaultProcess(err))
	case locked:
		return "the vault process was locked; " + next
	default:
		return ""
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
			m.fail = "wrong passphrase"
		} else {
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		if msg.procNote != "" {
			m.fail += "; " + msg.procNote
		}
	} else {
		m.status = vaultActionDone[msg.action]
		if msg.procNote != "" {
			m.status += "; " + msg.procNote
		}
	}

	if m.section != sectionVault || m.screen != screenForm {
		return nil
	}
	// The action changed the vault's state; the form is rebuilt so its rows, the action rows included, show
	// what applies now. openVaultForm never clears status or fail, so the outcome just set above survives.
	return m.openVaultForm()
}
