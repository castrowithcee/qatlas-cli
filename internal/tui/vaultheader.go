// The vault state segment of the header, and 'ctrl+l', which unlocks or locks the vault from anywhere in
// the editor. See the header itself in tui.go (headerLines, pathLine, sidebarLines) and this window's own
// admin session in admin.go, which 'ctrl+l' starts or ends alongside the vault itself but never otherwise
// touches: the two stay independent.
package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultTickInterval is how often the header's vault state is refreshed from a vault process, and this
// window's admin abbreviation re-rendered against the wall clock. It is a variable only so a test can
// shrink it: deliver, the test suite's own way of draining a command's messages, calls a tea.Tick's
// function directly, which blocks for the interval, and a test never has a reason to wait out the real 5
// seconds.
var vaultTickInterval = 5 * time.Second

// vaultTickMsg drives the periodic refresh; vaultTick reschedules itself every time it fires.
type vaultTickMsg struct{}

func vaultTick() tea.Cmd {
	return tea.Tick(vaultTickInterval, func(time.Time) tea.Msg { return vaultTickMsg{} })
}

// vaultHeaderText is the header's vault state segment: symbol and label together, so it reads without
// colour and under NO_COLOR, plus the admin abbreviation for an encrypted, unlocked vault. "" for a run with
// no vault configured at all: showing a warning for something that does not exist yet would be
// misleading.
func (m *Model) vaultHeaderText() string {
	v := m.secrets.Vault()
	if v == nil {
		return ""
	}
	state, err := v.State()
	if err != nil {
		// Neither locked nor unlocked describes a vault whose state cannot even be read; this is closer to
		// the unencrypted warning than to either of the other two, and keeps the line naming what is wrong
		// instead of silently showing nothing.
		return failStyle.Render("⚠ vault: " + m.redactor.Apply(err.Error()))
	}
	switch {
	case state == vault.StateUnencrypted:
		return failStyle.Render("⚠ vault unencrypted")
	case m.vaultDisplayUnlocked(state):
		// A vault process elsewhere may hold it open while this window itself has never unlocked it; either
		// way the admin abbreviation belongs here too, since every unlocked, encrypted vault shows one, and
		// adminHeaderText already reads "admin off" (or the vault.admin_timeout: 0 text) on its own for a
		// window that never started a session, since AdminSessionActive is false regardless of why.
		return okStyle.Render("🔓 vault unlocked") + " · " + hintStyle.Render(m.adminHeaderText())
	case state == vault.StateLocked:
		return warningStyle.Render("🔒 vault locked")
	}
	return ""
}

// vaultDisplayUnlocked reports whether the header, and 'ctrl+l', should treat the vault as unlocked: this
// process unlocked it itself, or a vault process elsewhere holds it open (see checkVaultProcess). The two
// are deliberately folded into one question here, unlike admin.go's own requireAdmin, which always asks this
// window's own state alone: a shown "unlocked" vault must behave as one for 'ctrl+l' as well, or the header
// and the key would contradict each other.
func (m *Model) vaultDisplayUnlocked(state vault.State) bool {
	return state == vault.StateUnlocked || (state == vault.StateLocked && m.vaultProcessUnlocked)
}

// adminHeaderText is the header's admin abbreviation for an encrypted, unlocked vault: "admin off" while
// this window has no session, "admin Nm left" with its remaining time rounded to minutes, or "admin: confirm
// each change" for vault.admin_timeout: 0, which never keeps one at all (see requireAdmin in admin.go).
func (m *Model) adminHeaderText() string {
	if m.cfg.VaultAdminTimeout() <= 0 {
		return "admin: confirm each change"
	}
	if !m.AdminSessionActive() {
		return "admin off"
	}
	minutes := int(m.AdminSessionRemaining().Round(time.Minute) / time.Minute)
	return fmt.Sprintf("admin %dm left", minutes)
}

// vaultLockKeyHint is the sidebar's own 'ctrl+l' footer line, added only where sidebarLines still has room
// (see there) and only for an encrypted vault, since an absent or unencrypted one has nothing to lock or
// unlock (handleVaultLockKey says so itself if it is pressed anyway).
func (m *Model) vaultLockKeyHint() string {
	v := m.secrets.Vault()
	if v == nil {
		return ""
	}
	switch state, err := v.State(); {
	case err != nil:
		return ""
	case m.vaultDisplayUnlocked(state):
		return "ctrl+l lock vault"
	case state == vault.StateLocked:
		return "ctrl+l unlock vault"
	}
	return ""
}

// vaultLockKeyAllowed reports whether 'ctrl+l' acts on the screen the editor is on right now: every
// browsing screen, never one that already asks its own question (a masked prompt, a confirmation, or the
// leave question) and never this file's own two dialogs while they are already open. It works in lists and
// forms and every other screen besides those questions.
func (m *Model) vaultLockKeyAllowed() bool {
	switch m.screen {
	case screenNav, screenList, screenForm, screenTargets, screenPaths, screenPicker, screenProviders,
		screenSummary, screenLogs, screenLogDetail:
		return true
	}
	return false
}

// handleVaultLockKey runs 'ctrl+l' with no other dialog open (see vaultLockKeyAllowed, its only caller):
// gated first behind vaultBusy, since vault work already in flight must not be raced by a second attempt,
// exactly like every other vault action of this editor, it then opens the dialog the displayed state calls
// for (see vaultDisplayUnlocked, which the header itself also follows, so the two never contradict each
// other), or says there is nothing to do for one that is absent or unencrypted.
func (m *Model) handleVaultLockKey() tea.Cmd {
	if m.vaultBusy {
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.status = "no vault is configured for this run; there is nothing to lock or unlock"
		return nil
	}
	state, err := v.State()
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	switch {
	case m.vaultDisplayUnlocked(state):
		m.openVaultLockConfirm(m.screen)
	case state == vault.StateLocked:
		m.openVaultUnlockPrompt(m.screen)
	default:
		m.status = "the vault is not encrypted, so there is nothing to lock"
	}
	return nil
}

// vaultUnlockPrompt is the masked passphrase dialog 'ctrl+l' opens for a locked vault with no other action
// pending (see handleVaultLockKey). It is deliberately its own small dialog rather than a third mode of
// admin.go's adminAuth: unlike requireAdmin's "Unlock vault to continue", answering it does nothing but the
// unlock itself, the vault process handoff on Linux, macOS, and Windows (see startVaultProcess below), and starting this
// window's admin session; back is the screen it returns to either way, whichever one 'ctrl+l' was pressed
// from.
type vaultUnlockPrompt struct {
	input textinput.Model
	back  screen
}

func (m *Model) openVaultUnlockPrompt(back screen) {
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()

	m.vaultUnlock = &vaultUnlockPrompt{input: in, back: back}
	m.screen = screenVaultUnlock
	m.clearMessages()
}

// updateVaultUnlockPrompt handles the masked entry of the 'ctrl+l' unlock dialog.
func (m *Model) updateVaultUnlockPrompt(key tea.KeyMsg) tea.Cmd {
	u := m.vaultUnlock
	switch key.String() {
	case "ctrl+c":
		m.vaultUnlock = nil
		return m.quit()
	case "esc":
		back := u.back
		m.vaultUnlock = nil
		m.screen = back
		m.status = "Cancelled"
		return nil
	case "enter":
		if m.vaultBusy {
			// The scrypt work of an earlier attempt on this very dialog is still running.
			return nil
		}
		typed := u.input.Value()
		u.input.Reset()
		m.clearMessages()
		if typed == "" {
			m.fail = "a passphrase must not be empty"
			return nil
		}
		return m.beginVaultUnlock(typed, u.back)
	}
	var cmd tea.Cmd
	u.input, cmd = u.input.Update(key)
	return cmd
}

// vaultUnlockPromptView draws the masked 'ctrl+l' unlock dialog, in the pattern of every other masked
// prompt this editor already shows (see adminAuthView, vaultOfferView): a title, the masked line, an
// explanation, and enter/esc. The explanation is its own text (see vaultUnlockHint), not admin.go's
// adminSessionHint: this dialog opens without a pending admin-gated action, so it reads as unlocking that
// also starts admin mode, not the other way round.
func (m *Model) vaultUnlockPromptView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Unlock vault") + "\n\n")
	if m.restartNote != "" {
		b.WriteString(m.indented(m.restartNote) + "\n\n")
	}
	b.WriteString("  " + m.vaultUnlock.input.View() + "\n")
	b.WriteString(m.indented(m.vaultUnlockHint()) + "\n")
	b.WriteString(m.hint("enter unlock · esc cancel"))
	return b.String()
}

// vaultUnlockHint explains what answering the 'ctrl+l' unlock dialog does besides unlocking the vault
// itself: starts this window's admin session too, until vault.admin_timeout idle, or, for
// vault.admin_timeout: 0, asks again on the very next change regardless (the same words admin.go's own
// adminSessionHint uses for that one case, since a session never starts at all either way).
func (m *Model) vaultUnlockHint() string {
	timeout := m.cfg.VaultAdminTimeout()
	if timeout <= 0 {
		return "vault.admin_timeout is 0: this asks again on the very next change; other windows are unaffected"
	}
	minutes := int(timeout.Round(time.Minute) / time.Minute)
	return fmt.Sprintf("this also starts admin mode for this window, until %dm idle", minutes)
}

// vaultUnlockedMsg carries the outcome of unlocking the vault back into the event loop: the scrypt work
// vault.Vault.Unlock does never runs on it. back is the screen to return to; merged is how many pending
// entries were merged into the vault, the same count 'qatlas vault unlock' itself reports.
type vaultUnlockedMsg struct {
	err    error
	back   screen
	merged int
}

// beginVaultUnlock unlocks the vault with passphrase, as a command so the scrypt work never runs on the
// event loop; vaultBusy blocks a second vault write, or another such dialog, from starting before it is
// done.
func (m *Model) beginVaultUnlock(passphrase string, back screen) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.busy = "unlocking the vault"
	return func() tea.Msg {
		merged, err := v.Unlock(passphrase)
		return vaultUnlockedMsg{err: err, back: back, merged: merged}
	}
}

// handleVaultUnlocked decides what unlocking the vault answers. A wrong passphrase reopens the very same
// dialog with the error shown, the same pattern every other masked prompt of this editor follows. The right
// one unlocks the vault in this process at once, starts this window's admin session too (the same passphrase
// just proved both, the same reasoning requireAdmin's own locked case already follows), and, only then, hands
// the vault to a vault process on Linux, macOS, and Windows in the background (see startVaultProcess): that hand-off can take a
// few seconds and must never delay returning control of the editor.
func (m *Model) handleVaultUnlocked(msg vaultUnlockedMsg) tea.Cmd {
	if msg.err != nil {
		m.vaultBusy, m.busy = false, ""
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			m.openVaultUnlockPrompt(msg.back)
			m.fail = "wrong passphrase"
			return nil
		}
		m.vaultUnlock = nil
		m.screen = msg.back
		m.fail = m.redactor.Apply(msg.err.Error())
		return nil
	}
	m.vaultUnlock = nil
	m.screen = msg.back
	m.startAdminSession()
	m.status = "The vault is unlocked"
	if m.section == sectionVault && m.screen == screenForm && m.formState() == m.pristine {
		// The update behaviour row can only be read now; rebuilding drops nothing, the form is unedited.
		m.openVaultForm()
	}
	m.vaultProcessUnlocked = false
	if cmd := m.startVaultProcess(); cmd != nil {
		return cmd
	}
	m.vaultBusy, m.busy = false, ""
	return nil
}

// vaultProcessStartedMsg carries the outcome of handing the vault to a vault process back into the event
// loop; see startVaultProcess.
type vaultProcessStartedMsg struct{ err error }

// startVaultProcessFn is what startVaultProcess calls to actually hand the vault to a new vault process
// once none answers yet: vaultproc.StartProcess for a real run, which starts a second copy of the running
// program. A test replaces it with one that starts an in-process vaultproc.Server instead, the same way
// package cli keeps 'vault unlock' itself from starting a second copy of its own test binary (see
// runAsQatlasEnv there); everything around this seam, checking whether a process already answers and
// building the snapshot, still runs for real.
var startVaultProcessFn = vaultproc.StartProcess

// startVaultProcess hands the just-unlocked vault to a vault process on Linux, macOS, and Windows, exactly the way 'qatlas
// vault unlock' does (see vaultproc.StartProcess, the shared core both run): nil where this platform
// runs no vault process at all, so the caller never has to ask vaultproc.Supported itself. A process already
// running elsewhere, or none running yet because this platform never starts one, stays silent; any other
// failure becomes a warning with secret.VaultProcessRemedy, never with a secret value, the same rule
// manage.Service.SyncVaultProcess and LockVaultProcess already follow.
func (m *Model) startVaultProcess() tea.Cmd {
	if !vaultproc.Supported {
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		return nil
	}
	configPath, err := filepath.Abs(m.svc.Path())
	if err != nil {
		return func() tea.Msg { return vaultProcessStartedMsg{err: err} }
	}
	m.busy = "starting the vault process"
	return func() tea.Msg {
		client, err := vaultproc.ProcessClientOf(v)
		if err != nil {
			return vaultProcessStartedMsg{err: err}
		}
		if _, err := client.Status(context.Background()); err == nil {
			// Another window, or another terminal's 'qatlas vault unlock', already started one: nothing more
			// to do.
			return vaultProcessStartedMsg{}
		} else if !errors.Is(err, vaultproc.ErrNotRunning) {
			return vaultProcessStartedMsg{err: err}
		}
		snap, err := v.Snapshot()
		if err != nil {
			return vaultProcessStartedMsg{err: err}
		}
		_, err = startVaultProcessFn(context.Background(), configPath, snap, client)
		return vaultProcessStartedMsg{err: err}
	}
}

// handleVaultProcessStarted reports a failed hand-off as a warning, never blocking anything the unlock
// itself already finished: the vault stays unlocked in this process either way.
func (m *Model) handleVaultProcessStarted(msg vaultProcessStartedMsg) tea.Cmd {
	m.vaultBusy, m.busy = false, ""
	if msg.err == nil {
		return nil
	}
	m.status = fmt.Sprintf("The vault is unlocked, but the vault process could not be started: %s; %s",
		m.redactor.Apply(msg.err.Error()), secret.VaultProcessRemedy(msg.err))
	return nil
}

// openVaultLockConfirm opens the short confirmation 'ctrl+l' asks before locking an unlocked, encrypted
// vault (see handleVaultLockKey); back is the screen it returns to either way.
func (m *Model) openVaultLockConfirm(back screen) {
	m.vaultLockConfirm, m.vaultLockBack = true, back
	m.screen = screenConfirm
	m.clearMessages()
}

// vaultLockConfirmView draws the "Lock the vault now?" question.
func (m *Model) vaultLockConfirmView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Lock the vault now?") + "\n\n")
	b.WriteString(m.indented("running invokes stop working until it is unlocked again") + "\n")
	b.WriteString(m.hint("y lock · n/esc stay unlocked"))
	return b.String()
}

// vaultLockedMsg carries the outcome of locking the vault back into the event loop.
type vaultLockedMsg struct{ err error }

// beginVaultLock runs the three things 'ctrl+l' answered "y" does: locks the vault process that
// holds it unlocked outside this run, the same way 'qatlas vault lock' does (see vaultproc.LockProcess,
// the shared core both run); forgets the key and document this process itself holds, the closest the vault
// API comes to undoing Unlock (see vault.Vault.Forget); and, once the command returns, ends this window's
// admin session (see handleVaultLocked), since managing without an unlocked vault is not possible anyway.
// Both vault actions run as a command: locking a running process is a socket round trip, never the event
// loop's job.
func (m *Model) beginVaultLock() tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		return nil
	}
	m.vaultBusy = true
	m.busy = "locking the vault"
	return func() tea.Msg {
		_, err := vaultproc.LockProcess(context.Background(), v)
		v.Forget()
		return vaultLockedMsg{err: err}
	}
}

// handleVaultLocked applies the outcome of beginVaultLock. The key is forgotten and the admin session ended
// either way: both are this window's own state, and neither depends on whether a remote vault process could
// be reached to lock as well.
func (m *Model) handleVaultLocked(msg vaultLockedMsg) tea.Cmd {
	m.vaultBusy, m.busy = false, ""
	m.endAdminSession()
	m.vaultProcessUnlocked = false
	if msg.err != nil {
		m.fail = fmt.Sprintf("the vault process could not be locked: %s; %s",
			m.redactor.Apply(msg.err.Error()), secret.VaultProcessRemedy(msg.err))
		return nil
	}
	m.status = "The vault is locked"
	return nil
}

// vaultProcessCheckMsg carries the result of asking a vault process whether it holds the locked vault open,
// purely for the header (see checkVaultProcess); it is never used to read or write a secret.
type vaultProcessCheckMsg struct{ unlocked bool }

// checkVaultProcess asks, off the event loop, whether a vault process holds the vault open right now (not where
// this platform runs no vault process), so the header can show "unlocked" for a vault this process itself has not unlocked. It asks only
// while there is something worth asking about: an encrypted, locked vault on a platform that runs
// a vault process at all; every other case has nothing to gain from a round trip and returns nil, so
// vaultTick's own tea.Batch simply carries nothing for it that tick.
func (m *Model) checkVaultProcess() tea.Cmd {
	v := m.secrets.Vault()
	if v == nil || !vaultproc.Supported {
		return nil
	}
	state, err := v.State()
	if err != nil || state != vault.StateLocked {
		return nil
	}
	return func() tea.Msg {
		client, err := vaultproc.ProcessClientOf(v)
		if err != nil {
			return vaultProcessCheckMsg{}
		}
		_, err = client.Status(context.Background())
		return vaultProcessCheckMsg{unlocked: err == nil}
	}
}
