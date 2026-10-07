// The admin session that gates every managing action of the editor behind the vault's passphrase, once the
// vault is encrypted. The editor itself always opens read-only; the first managing action of this run asks
// for the passphrase in a masked screen of its own, never on this process's terminal (see tuiSecrets in
// internal/cli), and a right answer keeps only this window in "admin mode" until vault.admin_timeout passes
// without a key press. Every other window, and the vault process a Linux, macOS, or Windows build may hold unlocked outside
// this run, never shares it: the passphrase is the proof the session rests on, not merely the vault's own
// unlocked state (see vault.VerifyPassphrase, which checks it without relying on either).
package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// adminAuth is the masked admin passphrase dialog requireAdmin opens while it is waiting for one, and nil
// otherwise. It never confirms a fresh passphrase twice, unlike vaultOffer: it only ever checks one that
// already exists.
type adminAuth struct {
	input textinput.Model
	// locked says whether the vault itself needs unlocking too ("Unlock vault to continue"), or is already
	// unlocked in this process and only lacks a session ("Admin passphrase needed to save").
	locked bool
	// back is the screen to return to on esc or a cancelled retry: whatever screen requireAdmin was called
	// from, so a cancelled check never stops on the dialog itself and never invents a different one.
	back screen
	// action is what requireAdmin was asked to gate; it runs only once the passphrase proves admin.
	action func() tea.Cmd
}

// adminVerifiedMsg carries the outcome of checking a typed admin passphrase back into the event loop: it may
// have to unlock the vault or merely verify it, either of which does the scrypt work of internal/vault, so
// it never runs on the event loop itself.
type adminVerifiedMsg struct {
	err    error
	locked bool
	back   screen
	action func() tea.Cmd
}

// requireAdmin gates a managing action behind this window's admin session. It runs action at once when
// there is nothing an admin session could apply to at all: no vault is configured for this run, or the
// vault holds no passphrase because it does not exist yet or was never encrypted, since an unencrypted
// vault has no passphrase to prove and so no admin session either. It also runs action at once, after
// renewing this window's idle deadline, while a session is already active. A vault whose state cannot even
// be read fails closed instead: action does not run, and the error is reported the same way any other
// refused save is, on whatever screen requireAdmin was called from, with the field it was called on left
// exactly as typed. Otherwise it opens the masked admin passphrase dialog first, titled "Unlock vault to
// continue" for a locked vault (which unlocks it and starts the session together, in one passphrase) or
// "Admin passphrase needed to save" for one that is already unlocked but has no session of its own yet;
// action runs only once the right passphrase proved either. Cancelling it (esc), or simply not finishing
// it, runs nothing: whatever screen opened it is left exactly as it was, with every unsaved field still
// there.
func (m *Model) requireAdmin(action func() tea.Cmd) tea.Cmd {
	required, locked, err := manage.AdminRequired(m.secrets.Vault())
	if err != nil {
		// Failing open here would let a managing action through without ever having checked whether the
		// vault is even encrypted, let alone unlocked: refuse it instead, the same way every other write
		// this editor cannot safely attempt is refused, and leave the caller's own screen and fields alone.
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	if !required {
		return action()
	}
	if m.adminSessionActive() {
		m.touchAdminSession()
		return action()
	}
	m.openAdminPrompt(locked, m.screen, action)
	return nil
}

// openAdminPrompt opens the masked admin passphrase dialog. back is the screen a cancelled or abandoned
// check returns to; it is captured once, by requireAdmin's first call, and threaded through every retry a
// wrong passphrase causes (see handleAdminVerified), so a retry never moves the fallback to the dialog
// itself.
func (m *Model) openAdminPrompt(locked bool, back screen, action func() tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()

	m.adminAuth = &adminAuth{input: in, locked: locked, back: back, action: action}
	m.screen = screenAdminAuth
	m.clearMessages()
}

// updateAdminAuth handles the masked entry of the admin passphrase dialog.
func (m *Model) updateAdminAuth(key tea.KeyMsg) tea.Cmd {
	a := m.adminAuth
	switch key.String() {
	case "ctrl+c":
		m.adminAuth = nil
		return m.quit()
	case "esc":
		back := a.back
		m.adminAuth = nil
		m.screen = back
		m.status = "Cancelled"
		return nil
	case "enter":
		if m.vaultBusy {
			// The scrypt work of an earlier attempt on this very dialog is still running: starting a second
			// one over it, on the same vault.Vault, would race it, the same reason every other vault check
			// or write in this editor is guarded the same way (see vaultBusy's own comment).
			return nil
		}
		typed := a.input.Value()
		a.input.Reset()
		m.clearMessages()
		if typed == "" {
			m.fail = "a passphrase must not be empty"
			return nil
		}
		return m.verifyAdminPassphrase(typed, a.locked, a.back, a.action)
	}

	var cmd tea.Cmd
	a.input, cmd = a.input.Update(key)
	return cmd
}

// verifyAdminPassphrase checks the typed passphrase, as a command so the scrypt work it does never runs on
// the event loop: unlocking a locked vault with vault.Vault.Unlock, the same call 'qatlas credential set'
// makes for requireAdmin's CLI counterpart (see internal/cli/admin.go), or, for one already unlocked in this
// process, verifying it without any other side effect with vault.Vault.VerifyPassphrase, the same call the
// vault form's own "change passphrase" and "turn off encryption" already use. Either is the one existing
// check the vault owns; this never repeats or reimplements it.
func (m *Model) verifyAdminPassphrase(passphrase string, locked bool, back screen, action func() tea.Cmd) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.busy = "checking the vault passphrase"
	return func() tea.Msg {
		return adminVerifiedMsg{err: manage.VerifyAdmin(v, passphrase), locked: locked, back: back, action: action}
	}
}

// handleAdminVerified decides what requireAdmin's caller asked for once the check answers. A wrong
// passphrase reopens the very same dialog with the error shown, exactly the way the vault form's own current-
// passphrase check already does; any other failure ends the check on the screen it was opened from. The
// right passphrase starts, or renews, the admin session and runs the gated action.
func (m *Model) handleAdminVerified(msg adminVerifiedMsg) tea.Cmd {
	m.vaultBusy, m.busy = false, ""
	if msg.err != nil {
		if errors.Is(msg.err, manage.ErrWrongPassphrase) {
			m.openAdminPrompt(msg.locked, msg.back, msg.action)
			m.fail = "wrong passphrase"
			return nil
		}
		m.fail = m.redactor.Apply(msg.err.Error())
		m.adminAuth = nil
		m.screen = msg.back
		return nil
	}
	m.adminAuth = nil
	// The dialog itself changed m.screen to show it; restore whatever it was when requireAdmin was called,
	// exactly as an ungated call to action would have found it, before action runs and decides for itself
	// where the screen goes from there.
	m.screen = msg.back
	m.startAdminSession()
	return msg.action()
}

// adminAuthView draws the masked admin passphrase dialog, in the pattern of the masked vault passphrase
// prompt (see vaultOfferView): a title, the masked line, an explanation, and enter/esc.
func (m *Model) adminAuthView() string {
	a := m.adminAuth
	title := "Admin passphrase needed to save"
	hint := m.adminSessionHint()
	keys := "enter continue · esc cancel, nothing is saved"
	if a.locked {
		title = "Unlock vault to continue"
		hint = "unlocks the vault and admin mode, then continues; other windows are unaffected"
		keys = "enter unlock and continue · esc cancel, nothing is saved"
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString("  " + a.input.View() + "\n")
	b.WriteString(m.indented(hint) + "\n")
	b.WriteString(m.hint(keys))
	return b.String()
}

// adminSessionHint says how long this window's admin session will stay open once the passphrase proves it,
// rounded to minutes, or, for vault.admin_timeout: 0, that it will not stay open at all.
func (m *Model) adminSessionHint() string {
	timeout := m.cfg.VaultAdminTimeout()
	if timeout <= 0 {
		return "vault.admin_timeout is 0: this asks again on the very next change; other windows are unaffected"
	}
	minutes := int(timeout.Round(time.Minute) / time.Minute)
	return fmt.Sprintf("this unlocks admin mode for this window until %dm idle; other windows are unaffected",
		minutes)
}

// startAdminSession begins, or renews, this window's admin session once a passphrase just proved it: through
// requireAdmin, or through any other flow of this editor that already verifies the vault's actual passphrase
// on its own, such as the vault form's "change passphrase", "turn off encryption", and "migrate
// credentials.yaml" (see handleVaultAction and handleVaultPassphraseVerified in vaultsettings.go): each of
// those is every bit as much a proof of admin as answering requireAdmin's own dialog, and asking twice in the
// same breath would only be worse for no more security. With vault.admin_timeout: 0 the session keeps no
// deadline, so adminSessionActive is never true and every next managing action asks again.
func (m *Model) startAdminSession() { m.admin.Grant(m.cfg.VaultAdminTimeout()) }

// touchAdminSession renews this window's idle deadline to vault.admin_timeout from now.
func (m *Model) touchAdminSession() { m.admin.Touch(m.cfg.VaultAdminTimeout()) }

// adminSessionActive reports whether this window's admin session is active right now.
func (m *Model) adminSessionActive() bool { return m.admin.Active() }

// touchAdminSessionIfActive renews the session's idle deadline on every key this editor handles, whatever
// screen it lands on: activity means any key press in this window, not only the managing actions the
// session gates, so reading around the editor keeps a session alive the same way changing something does.
// It never starts a session on its own; only requireAdmin, or one of the flows startAdminSession's own
// comment names, does that.
func (m *Model) touchAdminSessionIfActive() { m.touchAdminSession() }

// endAdminSession ends this window's admin session.
func (m *Model) endAdminSession() { m.admin.End() }

// AdminSessionActive reports whether this window's admin session is active right now: other parts of this
// editor that need to show or use that state can ask this instead of duplicating the idle-timeout
// arithmetic.
func (m *Model) AdminSessionActive() bool { return m.adminSessionActive() }

// AdminSessionRemaining reports how long this window's admin session stays active, or zero when there is
// none right now (also true for vault.admin_timeout: 0, which never keeps one at all). Rounding it to
// minutes for a header belongs to that header, not here.
func (m *Model) AdminSessionRemaining() time.Duration { return m.admin.Remaining() }
