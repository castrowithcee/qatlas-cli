// The vault state in the header and 'ctrl+l' (see vaultheader.go): the three states of the header, the
// admin abbreviation, the tick that refreshes both, and 'ctrl+l' itself unlocking and locking the vault from
// a list and a form. The Linux and macOS test with a real vaultproc.Server lives in vaultprocess_unix_test.go, beside
// the package's other tests of that kind.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// TestMain keeps the ordinary test suite from ever spawning a second copy of this test binary as a vault
// process: unlocking the vault, through 'ctrl+l' or otherwise, still tries to hand it to one on Linux and macOS (see
// startVaultProcess), and only a test that explicitly wants a real one, through withStartableVaultProcess in
// vaultprocess_unix_test.go, replaces this stub for its own duration. Package cli's own TestMain
// (internal/cli/admin_test.go) exists for the same reason, over 'qatlas vault unlock' itself.
//
// It also shrinks vaultTickInterval: deliver (update_test.go) calls a command's function directly, which for
// tea.Tick blocks for the interval, and never recurses past the message it produces (see deliver's own
// comment on vaultTickMsg), so no test wants the real 5 seconds.
func TestMain(m *testing.M) {
	startVaultProcessFn = func(context.Context, string, vault.Snapshot, *vaultproc.Client) (vaultproc.Status, error) {
		return vaultproc.Status{}, vaultproc.ErrUnsupported
	}
	vaultTickInterval = time.Millisecond
	os.Exit(m.Run())
}

// unencryptedVaultModel builds an editor whose vault exists but was never given a passphrase.
func unencryptedVaultModel(t *testing.T) *Model {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("other", "role", "seed", nil))
	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return m
}

// The header names all three states of an encrypted or unencrypted vault, symbol and word together, and a
// run without a vault at all keeps the plain header it always had (Vorgabe 1's own narrower reading: nothing
// instead of a warning for something that does not exist yet). Combined with the path line from
// sidebarMinWidth up, and its own row above it in a narrower terminal.
func TestVaultHeaderThreeStatesAndNoVault(t *testing.T) {
	for _, width := range []int{100, 60} {
		narrow := width < sidebarMinWidth
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			none, _, _ := newModel(t)
			none.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			if got := none.vaultHeaderText(); got != "" {
				t.Errorf("no vault: vaultHeaderText() = %q, want \"\"", got)
			}
			if lines := none.headerLines(); len(lines) != 1 {
				t.Fatalf("no vault: headerLines() = %d lines, want 1: %q", len(lines), lines)
			}

			for _, tt := range []struct {
				name  string
				build func(t *testing.T) *Model
				want  string
			}{
				{"unencrypted", unencryptedVaultModel, "vault unencrypted"},
				{"locked", func(t *testing.T) *Model {
					m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
					return m
				}, "vault locked"},
				{"unlocked", func(t *testing.T) *Model {
					m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
					return m
				}, "vault unlocked"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					m := tt.build(t)
					m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
					lines := m.headerLines()
					joined := strings.Join(lines, "\n")
					if !strings.Contains(joined, tt.want) {
						t.Fatalf("header = %q, want it to contain %q", joined, tt.want)
					}
					switch {
					case narrow && len(lines) != 2:
						t.Fatalf("narrow headerLines() = %d lines, want 2: %q", len(lines), lines)
					case narrow && (!strings.Contains(lines[0], tt.want) || strings.Contains(lines[0], "Config:")):
						t.Errorf("narrow line 1 = %q, want the vault state alone", lines[0])
					case !narrow && len(lines) != 1:
						t.Fatalf("wide headerLines() = %d lines, want 1: %q", len(lines), lines)
					case !narrow && !strings.Contains(lines[0], "Config:"):
						t.Errorf("wide header = %q, want the path still on the same line", lines[0])
					}
				})
			}
		})
	}
}

// The admin abbreviation appears only for an encrypted, unlocked vault, and says "admin off" until this
// window's own session starts, its remaining time rounded to minutes once it has, or that every change asks
// again for vault.admin_timeout: 0.
func TestVaultHeaderAdminAbbreviation(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)

	if got := m.vaultHeaderText(); !strings.Contains(got, "admin off") {
		t.Fatalf("vaultHeaderText() with no session = %q, want it to contain %q", got, "admin off")
	}

	m.startAdminSession()
	if got := m.vaultHeaderText(); !strings.Contains(got, "admin 10m left") {
		t.Fatalf("vaultHeaderText() with a fresh 10m session = %q, want it to contain %q", got, "admin 10m left")
	}

	m.adminSessionUntil = time.Now().Add(100 * time.Second)
	if got := m.vaultHeaderText(); !strings.Contains(got, "admin 2m left") {
		t.Fatalf("vaultHeaderText() with 100s left = %q, want it rounded to %q", got, "admin 2m left")
	}

	cfg := m.cfg.Clone()
	cfg.Vault.AdminTimeout = "0"
	m.cfg = cfg
	syncRevision(t, m)
	if got := m.vaultHeaderText(); !strings.Contains(got, "admin: confirm each change") {
		t.Fatalf("vaultHeaderText() with admin_timeout 0 = %q, want it to contain %q", got, "admin: confirm each change")
	}
}

// A vault process elsewhere holds the vault open while this window itself never unlocked it (locally
// locked, vaultProcessUnlocked true): the header still shows the admin abbreviation, "admin off" or, for
// vault.admin_timeout: 0, "admin: confirm each change", never a remaining time, since no session can exist
// in this window without its own proof of the passphrase.
func TestVaultHeaderAdminAbbreviationWithOnlyTheVaultProcessUnlocked(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	m.vaultProcessUnlocked = true

	if got := m.vaultHeaderText(); !strings.Contains(got, "vault unlocked") || !strings.Contains(got, "admin off") {
		t.Fatalf("vaultHeaderText() = %q, want it to contain %q and %q", got, "vault unlocked", "admin off")
	}

	cfg := m.cfg.Clone()
	cfg.Vault.AdminTimeout = "0"
	m.cfg = cfg
	syncRevision(t, m)
	if got := m.vaultHeaderText(); !strings.Contains(got, "admin: confirm each change") {
		t.Fatalf("vaultHeaderText() with admin_timeout 0 = %q, want it to contain %q", got, "admin: confirm each change")
	}
}

// Neither symbol nor word is colour alone: with NO_COLOR (termenv.Ascii, what SetColorProfile(3) also
// produces) the header still names every state in plain words, and emits no escape sequence.
func TestVaultHeaderReadsWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
	lipgloss.SetColorProfile(3) // termenv.Ascii: what NO_COLOR also produces

	for _, tt := range []struct {
		name  string
		build func(t *testing.T) *Model
		want  string
	}{
		{"unencrypted", unencryptedVaultModel, "vault unencrypted"},
		{"locked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			return m
		}, "vault locked"},
		{"unlocked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			return m
		}, "vault unlocked"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.build(t)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			text := m.vaultHeaderText()
			if strings.Contains(text, "\x1b") {
				t.Errorf("NO_COLOR still emits escape sequences: %q", text)
			}
			if !strings.Contains(text, tt.want) {
				t.Errorf("text = %q, want it to contain %q", text, tt.want)
			}
		})
	}
}

// 'ctrl+l' unlocks a locked vault from a list and from a form alike: a masked passphrase dialog opens, a
// wrong passphrase reopens it with the error shown and nothing changed, and the right one unlocks the vault
// in this process, starts this window's admin session, and returns to the very screen it was pressed from.
func TestCtrlLUnlocksInListAndForm(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, m *Model) screen
	}{
		{"list", func(t *testing.T, m *Model) screen {
			openSectionByName(t, m, sectionServices)
			return screenList
		}},
		{"form", func(t *testing.T, m *Model) screen {
			openSectionByName(t, m, sectionServices)
			pressNew(t, m)
			if m.screen != screenForm {
				t.Fatalf("'n' did not open the form: screen %v", m.screen)
			}
			return screenForm
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			want := tt.setup(t, m)

			pump(t, m, "ctrl+l")
			if m.screen != screenVaultUnlock {
				t.Fatalf("ctrl+l on a locked vault = screen %v, want the unlock dialog", m.screen)
			}

			typeText(t, m, "wrong-passphrase")
			pump(t, m, "enter")
			if m.screen != screenVaultUnlock {
				t.Fatalf("a wrong passphrase left screen %v, want to stay on the unlock dialog", m.screen)
			}
			if m.fail != "wrong passphrase" {
				t.Fatalf("fail = %q, want %q", m.fail, "wrong passphrase")
			}

			typeText(t, m, "hunter2")
			pump(t, m, "enter")
			if m.screen != want {
				t.Fatalf("unlocking returned to screen %v, want %v", m.screen, want)
			}
			if m.fail != "" {
				t.Fatalf("unlocking reported %q", m.fail)
			}
			if state, err := m.secrets.Vault().State(); err != nil || state != vault.StateUnlocked {
				t.Fatalf("Vault().State() after unlocking = %v, %v, want unlocked, nil", state, err)
			}
			if !m.AdminSessionActive() {
				t.Error("unlocking with ctrl+l did not start this window's admin session")
			}
		})
	}
}

// An empty passphrase is refused without touching the vault at all, and esc cancels back to the screen
// 'ctrl+l' was pressed from, leaving the vault locked.
func TestCtrlLUnlockEmptyPassphraseAndCancel(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	openSectionByName(t, m, sectionServices)

	pump(t, m, "ctrl+l")
	pump(t, m, "enter")
	if m.screen != screenVaultUnlock || m.fail == "" {
		t.Fatalf("an empty passphrase = screen %v, fail %q, want to stay with a complaint", m.screen, m.fail)
	}

	press(t, m, "esc")
	if m.screen != screenList {
		t.Fatalf("esc on the unlock dialog = screen %v, want the list it was opened from", m.screen)
	}
	if state, err := m.secrets.Vault().State(); err != nil || state != vault.StateLocked {
		t.Fatalf("Vault().State() after esc = %v, %v, want it still locked", state, err)
	}
}

// 'ctrl+l' on an unlocked, encrypted vault asks "Lock the vault now?"; 'y' locks the vault in this process
// (Vault.Forget) and ends this window's admin session, 'n' and esc leave everything unlocked and the
// session untouched.
func TestCtrlLLocksWithConfirmation(t *testing.T) {
	for _, key := range []string{"n", "esc"} {
		t.Run("cancel with "+key, func(t *testing.T) {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			m.startAdminSession()
			openSectionByName(t, m, sectionServices)

			pump(t, m, "ctrl+l")
			if m.screen != screenConfirm || !m.vaultLockConfirm {
				t.Fatalf("ctrl+l on an unlocked vault = screen %v, vaultLockConfirm %v, want the lock question",
					m.screen, m.vaultLockConfirm)
			}
			pump(t, m, key)
			if m.screen != screenList {
				t.Fatalf("%s on the lock question = screen %v, want the list it was opened from", key, m.screen)
			}
			if state, err := m.secrets.Vault().State(); err != nil || state != vault.StateUnlocked {
				t.Fatalf("Vault().State() after %s = %v, %v, want it still unlocked", key, state, err)
			}
			if !m.AdminSessionActive() {
				t.Errorf("%s on the lock question ended the admin session, want it untouched", key)
			}
		})
	}

	t.Run("y locks it", func(t *testing.T) {
		m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
		m.startAdminSession()
		openSectionByName(t, m, sectionServices)

		pump(t, m, "ctrl+l")
		pump(t, m, "y")
		if m.screen != screenList {
			t.Fatalf("locking returned to screen %v, want the list it was opened from", m.screen)
		}
		if m.fail != "" {
			t.Fatalf("locking reported %q", m.fail)
		}
		if state, err := m.secrets.Vault().State(); err != nil || state != vault.StateLocked {
			t.Fatalf("Vault().State() after locking = %v, %v, want locked", state, err)
		}
		if m.AdminSessionActive() {
			t.Error("locking the vault did not end this window's admin session")
		}
	})
}

// A vault process elsewhere holds the vault open while this window itself never unlocked it (locally
// locked, vaultProcessUnlocked true, the header already showing "unlocked"): 'ctrl+l' must not contradict
// its own header. It asks "Lock the vault now?" straight away, the same as a vault this window did unlock
// itself, never the passphrase dialog; 'y' needs no passphrase, since there is no local key to unlock, and
// still ends this window's admin session (none can exist here to begin with).
func TestCtrlLLocksWhenOnlyTheVaultProcessHoldsItUnlocked(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	m.vaultProcessUnlocked = true
	openSectionByName(t, m, sectionServices)

	pump(t, m, "ctrl+l")
	if m.screen != screenConfirm || !m.vaultLockConfirm {
		t.Fatalf("ctrl+l with only the vault process unlocked = screen %v, vaultLockConfirm %v, want the "+
			"lock question, not the unlock dialog", m.screen, m.vaultLockConfirm)
	}
	pump(t, m, "y")
	if m.screen != screenList {
		t.Fatalf("locking returned to screen %v, want the list it was opened from", m.screen)
	}
	if m.fail != "" {
		t.Fatalf("locking reported %q", m.fail)
	}
	if state, err := m.secrets.Vault().State(); err != nil || state != vault.StateLocked {
		t.Fatalf("Vault().State() after locking = %v, %v, want locked", state, err)
	}
	if m.vaultProcessUnlocked {
		t.Error("locking did not clear vaultProcessUnlocked")
	}
	if m.AdminSessionActive() {
		t.Error("locking left an admin session active, want none possible in this state")
	}
}

// The 'ctrl+l' unlock dialog explains that answering it also starts this window's admin session, worded for
// a plain unlock rather than admin.go's own "Admin passphrase needed to save" wording; vault.admin_timeout:
// 0 keeps the same text admin.go's own dialog uses for that one case, since neither ever keeps a session.
func TestVaultUnlockPromptExplainsTheAdminSession(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	openSectionByName(t, m, sectionServices)
	pump(t, m, "ctrl+l")

	view := screenOf(m)
	if !strings.Contains(view, "this also starts admin mode for this window, until 10m idle") {
		t.Fatalf("unlock dialog = %q, want it to say it also starts admin mode", view)
	}

	cfg := m.cfg.Clone()
	cfg.Vault.AdminTimeout = "0"
	m.cfg = cfg
	syncRevision(t, m)
	view = screenOf(m)
	if !strings.Contains(view, "vault.admin_timeout is 0: this asks again on the very next change") {
		t.Fatalf("unlock dialog with admin_timeout 0 = %q, want the same zero-text admin.go's dialog uses", view)
	}
}

// An unencrypted vault has nothing to lock: 'ctrl+l' says so and touches nothing.
func TestCtrlLOnAnUnencryptedVault(t *testing.T) {
	m := unencryptedVaultModel(t)
	openSectionByName(t, m, sectionServices)

	pump(t, m, "ctrl+l")
	if m.screen != screenList {
		t.Fatalf("ctrl+l on an unencrypted vault opened screen %v, want it to stay on the list", m.screen)
	}
	if !strings.Contains(m.status, "nothing to lock") {
		t.Fatalf("status = %q, want it to say there is nothing to lock", m.status)
	}
}

// 'ctrl+l' never reaches a screen that already asks its own question: it is harmless there, exactly the
// task's own requirement, exercised on the admin dialog itself (screenAdminAuth), a plain y/n confirmation
// (screenConfirm), and the masked secret prompt (screenSecret).
func TestCtrlLIsHarmlessDuringOtherDialogs(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki")
	press(t, m, "tab")
	press(t, m, "tab")
	typeText(t, m, "https://wiki.example.invalid")
	pump(t, m, "enter") // saving asks for the vault's passphrase: screenAdminAuth
	if m.screen != screenAdminAuth {
		t.Fatalf("saving a form on a locked vault = screen %v, want the admin dialog", m.screen)
	}

	pump(t, m, "ctrl+l")
	if m.screen != screenAdminAuth {
		t.Fatalf("ctrl+l reached the admin dialog: screen %v, want it to stay there untouched", m.screen)
	}
}

// The header's emoji, 🔓/🔒/⚠, are wide cells, not one column each; every width this editor is meant to hold
// at, the minimum 40x12, the 79/80 sidebar edge, and a normal terminal, still fits within its own bounds with
// the vault header showing, in every one of its three states, the same way TestLayoutsFitTheirTerminal
// already checks the rest of the frame.
func TestVaultHeaderFitsAtEveryLayoutBoundary(t *testing.T) {
	for _, tt := range []struct {
		name  string
		build func(t *testing.T) *Model
	}{
		{"unencrypted", unencryptedVaultModel},
		{"locked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			return m
		}},
		{"unlocked", func(t *testing.T) *Model {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			return m
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.build(t)
			m.startAdminSession()
			for _, size := range []struct{ width, height int }{
				{40, 12}, {79, 24}, {80, 24}, {120, 28},
			} {
				m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				view := m.View()
				assertViewFits(t, view, size.width, size.height)
			}
		})
	}
}
