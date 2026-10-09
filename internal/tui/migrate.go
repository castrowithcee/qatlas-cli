// Migrating a plaintext credentials.yaml into the vault, from the vault form's own 'migrate
// credentials.yaml' action row. It shares its planning, writing, verifying, and credential-switching logic
// with 'qatlas vault migrate' through package vaultmigrate; only asking for a passphrase, showing the
// entries decided (credential and role names only, never a value), and asking whether to delete the file
// stay here, through this editor's own masked prompts and confirm screens, never a terminal.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultActionMigrate is the vault form's action that carries credentials.yaml into the vault; see
// startVaultMigrate. It is offered only while legacyEntries reports something to carry over.
const vaultActionMigrate = "migrate"

// migratePlan holds a 'migrate credentials.yaml' action's decided plan while the vault form's several
// screens ask about it: an overview to confirm before anything is written, and afterwards whether to delete
// the file. Nothing in it is a secret value: only credential and role names ever reach a screen.
type migratePlan struct {
	file           *secret.File
	plan           []vaultmigrate.Entry
	switched       []string
	storeUnsureFor []string
}

// legacyEntries reads every entry credentials.yaml still holds, or nil where there is nothing to migrate:
// the file does not exist, or it holds no entry. It is read fresh every time the vault form is built, the
// same way the vault's own state is, so the action row reflects the file exactly as it is now.
func (m *Model) legacyEntries() map[string]map[string]string {
	file := m.secrets.Plaintext()
	if file == nil {
		return nil
	}
	entries, err := file.All()
	if err != nil || len(entries) == 0 {
		return nil
	}
	return entries
}

// countNoun is "1 entry" or "2 entries": the small pluraliser package cli keeps of its own, repeated here
// because the TUI cannot import internal/cli.
func countNoun(n int, singular, plural string) string {
	noun := plural
	if n == 1 {
		noun = singular
	}
	return fmt.Sprintf("%d %s", n, noun)
}

// sortedCopy returns names sorted, leaving the caller's own slice untouched.
func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// startVaultMigrate begins 'migrate credentials.yaml': it reads what the file still holds and, where the
// vault is encrypted and locked, asks for its passphrase first and verifies it, since planning has to read
// what the vault already holds for a credential no longer of type keyring (see vaultmigrate.Plan). A wrong
// passphrase reopens that very prompt, the same way startVaultCurrentPassphrase always does, rather than
// planning anything. Migrating credentials into the vault is a managing action too: where the
// vault is already unlocked in this process but this window has no admin session of its own yet,
// requireAdmin asks for it before planning goes ahead; a locked vault is covered by the passphrase check
// above already, which also starts the session (see handleVaultPassphraseVerified), so requireAdmin has
// nothing left to gate there.
func (m *Model) startVaultMigrate() tea.Cmd {
	entries := m.legacyEntries()
	if len(entries) == 0 {
		m.status = "nothing to migrate: credentials.yaml does not exist or holds no entry"
		return nil
	}

	if m.vaultState() == vault.StateLocked {
		return m.startVaultCurrentPassphrase(
			"Vault passphrase",
			"needed to read what the vault already holds before the migration can be planned",
			func(current string) tea.Cmd {
				return m.planVaultMigrate(entries, func(string) (string, error) { return current, nil })
			},
		)
	}
	return m.requireAdmin(func() tea.Cmd { return m.planVaultMigrate(entries, nil) })
}

// planVaultMigrateMsg carries vaultmigrate.Plan's outcome back into the event loop.
type planVaultMigrateMsg struct {
	plan           []vaultmigrate.Entry
	switched       []string
	storeUnsureFor []string
	err            error
}

// planVaultMigrate runs vaultmigrate.Plan as a command: resolving a switched credential's keyring role may
// reach the platform store, and reading what the vault already holds may have to decrypt it, neither of
// which belongs on the event loop.
func (m *Model) planVaultMigrate(entries map[string]map[string]string, readCurrent vault.PassphraseFunc) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		m.screen = screenForm
		return nil
	}
	cfg, secrets := m.cfg, m.secrets
	m.vaultBusy = true
	m.busy = "planning the migration"
	m.screen = screenForm
	return func() tea.Msg {
		plan, switched, storeUnsureFor, err := vaultmigrate.Plan(cfg, secrets, v, entries, readCurrent)
		return planVaultMigrateMsg{plan: plan, switched: switched, storeUnsureFor: storeUnsureFor, err: err}
	}
}

// handlePlannedVaultMigrate opens the overview to confirm once the plan is ready, or reports why it could
// not be built. Every entry already being in the vault is not itself a reason to stop, the same way 'qatlas
// vault migrate' keeps going to still ask about deleting the file: only an empty credentials.yaml, already
// refused in startVaultMigrate, is.
func (m *Model) handlePlannedVaultMigrate(msg planVaultMigrateMsg) tea.Cmd {
	m.vaultBusy, m.busy = false, ""
	if msg.err != nil {
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			m.fail = "wrong passphrase"
		} else {
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		return nil
	}
	m.migrate = &migratePlan{file: m.secrets.Plaintext(), plan: msg.plan, switched: msg.switched,
		storeUnsureFor: msg.storeUnsureFor}
	m.migratePlanConfirm = true
	m.screen = screenConfirm
	m.clearMessages()
	return nil
}

// migrateRows names every entry of a migrate plan by its credential and role alone, sorted, never its
// value.
func migrateRows(plan []vaultmigrate.Entry) []string {
	rows := make([]string, len(plan))
	for i, e := range plan {
		rows[i] = e.Name + "." + e.Role
	}
	sort.Strings(rows)
	return rows
}

// migratePlanView is the overview a migrate action shows before anything is written: every credential and
// role decided to move, never a value, which credentials would switch from keyring to vault, and which ones
// could not be checked in the system keyring at all.
func (m *Model) migratePlanView() string {
	p := m.migrate
	var b strings.Builder
	b.WriteString(titleStyle.Render("Migrate credentials.yaml?") + "\n\n")
	b.WriteString(m.indented(countNoun(len(p.plan), "entry", "entries")+" would move into the vault:") + "\n")
	for _, row := range migrateRows(p.plan) {
		b.WriteString("    " + row + "\n")
	}
	if len(p.switched) > 0 {
		b.WriteString(m.indented(
			"switching from the system keyring to the vault: "+strings.Join(sortedCopy(p.switched), ", ")) + "\n")
	}
	if len(p.storeUnsureFor) > 0 {
		b.WriteString(m.indentedWith(warningStyle, "warning: the system keyring could not be checked for "+
			strings.Join(sortedCopy(p.storeUnsureFor), ", ")) + "\n")
	}
	b.WriteString(m.keyHint("y migrate · n/esc cancel, nothing is written · ? help"))
	return b.String()
}

// beginVaultMigrateWrite decides whether writing the confirmed plan would create the vault's very first
// secret: only then is a passphrase offered, exactly as vault.Vault.Set and beginVaultSecret define it.
func (m *Model) beginVaultMigrateWrite() tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		m.migrate, m.screen = nil, screenForm
		return nil
	}
	state, err := v.State()
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		m.migrate, m.screen = nil, screenForm
		return nil
	}
	if state != vault.StateAbsent {
		return m.runVaultMigrateWrite(nil)
	}
	m.openVaultOffer(
		"Set a passphrase for the vault?",
		"this migration would be the vault's very first secret; a passphrase encrypts it, typed masked and "+
			"twice, or leave this empty and press enter to keep the vault unencrypted",
		true, true,
		func(offer vault.PassphraseFunc) tea.Cmd {
			m.screen = screenForm
			return m.runVaultMigrateWrite(offer)
		},
		func() tea.Cmd {
			m.migrate, m.screen = nil, screenForm
			m.status = "Cancelled; credentials.yaml was not touched"
			return nil
		},
	)
	return nil
}

// vaultMigrateWrittenMsg carries the outcome of writing, verifying, and switching a migrate plan back into
// the event loop. cfg is the configuration with every switched credential already applied, ready to replace
// the model's own once nothing failed.
type vaultMigrateWrittenMsg struct {
	cfg     *config.Config
	warning string
	err     error
}

// runVaultMigrateWrite writes the confirmed plan into the vault, hands it on to a vault process where one
// runs, verifies it back, and switches every affected credential to type vault in a clone of the current
// configuration: exactly what package vaultmigrate does for 'qatlas vault migrate' too. It runs as a
// command: writing may do the scrypt work of the vault's very first secret, and verifying may have to read
// it back.
func (m *Model) runVaultMigrateWrite(offer vault.PassphraseFunc) tea.Cmd {
	p := m.migrate
	v := m.secrets.Vault()
	cfg := m.cfg.Clone()
	base := m.rev
	store, svc, previous := m.store, m.svc, m.cfg
	m.vaultBusy = true
	m.writes++
	m.busy = "migrating credentials.yaml into the vault"
	m.screen = screenForm
	return func() tea.Msg {
		var warning string
		err := vaultmigrate.Write(v, p.plan, offer, vaultMigrateSync(svc, &warning))
		if err == nil {
			err = vaultmigrate.Verify(v, p.plan)
		}
		if err == nil {
			err = vaultmigrate.SwitchCredentials(store, cfg, base, p.switched)
			if err == nil {
				if logged := svc.RecordConnections(previous, cfg); logged != "" {
					if warning != "" {
						warning += "; "
					}
					warning += logged
				}
			}
		}
		return vaultMigrateWrittenMsg{cfg: cfg, warning: warning, err: err}
	}
}

// vaultMigrateSync hands the entries a migrate action just wrote on to a vault process that holds the
// vault unlocked outside this run, exactly the way 'qatlas vault migrate' does for the same plan, through the
// same manage.Service.SyncVaultProcess every other vault write in this editor uses: a process that
// cannot be reached at all, is not running, or this platform runs none, needs nothing said. Any other
// failure is worth a warning, written to *warning rather than printed, since this editor has no terminal of
// its own to print to: the vault already holds the change and keeps it.
func vaultMigrateSync(svc *manage.Service, warning *string) vaultmigrate.Sync {
	return func(plan []vaultmigrate.Entry) {
		*warning = svc.SyncVaultProcess(context.Background(), func(ctx context.Context, client *vaultproc.Client) error {
			for _, p := range plan {
				if err := client.Set(ctx, p.Name, p.Role, p.Value); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// handleVaultMigrateWritten applies the outcome of runVaultMigrateWrite: a failure leaves the vault form
// open, rebuilt from whatever state the vault and the file are actually in now, and a success opens the
// combined result and delete question, with the model's own configuration replaced by the one the switch
// just applied to.
func (m *Model) handleVaultMigrateWritten(msg vaultMigrateWrittenMsg) tea.Cmd {
	m.vaultBusy = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}

	p := m.migrate
	m.migrate = nil
	if msg.err != nil {
		if m.conflicted(msg.err) {
			// The entries are in the vault and credentials.yaml is untouched, so repeating the action
			// against the reloaded configuration only writes them again.
			form := m.openVaultForm()
			m.status = ""
			return form
		}
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			m.fail = "wrong passphrase"
		} else {
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		return m.openVaultForm()
	}

	m.adoptSaved(msg.cfg)

	var lines []string
	lines = append(lines, "migrated "+countNoun(len(p.plan), "entry", "entries")+" into the vault")
	if len(p.switched) > 0 {
		lines = append(lines,
			"switched to the vault: "+strings.Join(sortedCopy(p.switched), ", "))
	}
	if len(p.storeUnsureFor) > 0 {
		lines = append(lines, "warning: the system keyring could not be checked for "+
			strings.Join(sortedCopy(p.storeUnsureFor), ", ")+"; only its plaintext credentials.yaml entries "+
			"were migrated, and a secret held only in the keyring may still be there and is not deleted")
	}
	if msg.warning != "" {
		lines = append(lines, "warning: "+msg.warning)
	}
	m.migrateResult = strings.Join(lines, "; ")
	m.migrate = p
	m.migrateDeleteConfirm = true
	m.screen = screenConfirm
	m.clearMessages()
	return nil
}

// migrateResultView is the combined result and delete question a migrate action ends on: what moved, what
// switched, and any warning, followed by whether to delete credentials.yaml now.
func (m *Model) migrateResultView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Delete credentials.yaml?") + "\n\n")
	b.WriteString(m.indented(m.migrateResult) + "\n")
	b.WriteString(m.keyHint("y delete · n/esc keep the file · ? help"))
	return b.String()
}

// deleteLegacyCredentials removes credentials.yaml once its migration is confirmed done, and reports the
// combined outcome, what was migrated and that the file was removed, as one status line.
func (m *Model) deleteLegacyCredentials() tea.Cmd {
	p := m.migrate
	result := m.migrateResult
	m.migrate, m.migrateResult, m.screen = nil, "", screenForm
	if p == nil || p.file == nil {
		return m.openVaultForm()
	}
	if err := os.Remove(p.file.Path()); err != nil && !os.IsNotExist(err) {
		m.fail = m.redactor.Apply(err.Error())
		return m.openVaultForm()
	}
	m.status = result + "; removed " + p.file.Path()
	return m.openVaultForm()
}
