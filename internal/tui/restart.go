package tui

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	// handoffEnv carries the little a restarting editor tells its successor. The successor removes it from
	// its environment as soon as it has read it, so no child inherits it.
	handoffEnv     = "QATLAS_TUI_HANDOFF"
	handoffVersion = 1
	// maxHandoffBytes bounds the encoded handoff on both sides: a value of a stranger is no more trusted
	// to stay small than any other input.
	maxHandoffBytes = 512
	maxHandoffField = 64
)

// Restart is how the editor replaces itself with the program an update installed. A nil Restart disables
// it; both fields are set together, and are fields so a test reaches every outcome without a second program.
type Restart struct {
	// Replaced returns the path the running program started from and whether its file was replaced since.
	Replaced func() (string, bool)
	// Exec replaces the process image, and returns only when it failed.
	Exec func(path string, argv, env []string) error
}

// handoff is what a restarting editor passes on: the versions it updated between and whether the vault was
// unlocked when it did. It carries no secret.
type handoff struct {
	Version  int    `json:"v"`
	From     string `json:"from"`
	To       string `json:"to"`
	Unlocked bool   `json:"unlocked,omitempty"`
}

// takeHandoff reads the handoff a predecessor left in the environment and removes it from there. It returns
// nil where there is none, an unknown version, or anything malformed: the editor then starts as without one.
func takeHandoff() *handoff {
	value, ok := os.LookupEnv(handoffEnv)
	if !ok {
		return nil
	}
	_ = os.Unsetenv(handoffEnv)
	return decodeHandoff(value)
}

func decodeHandoff(value string) *handoff {
	if len(value) > maxHandoffBytes {
		return nil
	}
	var h handoff
	if json.Unmarshal([]byte(value), &h) != nil || h.Version != handoffVersion {
		return nil
	}
	for _, version := range []string{h.From, h.To} {
		if version == "" || len(version) > maxHandoffField || strings.ContainsFunc(version, unicode.IsControl) {
			return nil
		}
	}
	return &h
}

// adopt takes the handoff of a predecessor: it says which versions the editor updated between and, when the
// vault was unlocked before and is encrypted and locked now, opens the unlock dialog. Escape leaves the
// vault locked.
func (m *Model) adopt(h *handoff) {
	m.status = "qatlas updated from " + h.From + " to " + h.To
	if !h.Unlocked {
		return
	}
	v := m.secrets.Vault()
	if v == nil {
		return
	}
	if state, err := v.State(); err != nil || state != vault.StateLocked {
		return
	}
	m.openVaultUnlockPrompt(screenNav)
	m.status = ""
	m.restartNote = "qatlas updated from " + h.From + " to " + h.To + "; the vault was unlocked before."
}

// restartAsks reports whether a restart now would lose input: a form or guided setup with unsaved changes,
// or any dialog opened over one.
func (m *Model) restartAsks() bool {
	if m.dirty() {
		return true
	}
	switch m.screen {
	case screenNav, screenList, screenForm, screenHelp, screenLogs, screenLogDetail:
		return false
	}
	return true
}

// restartTarget returns the program to run in place of this one, or false where the editor stays: no
// restart on this platform, the program is not replaced, or no executable file is there.
func (m *Model) restartTarget() (string, bool) {
	r := m.restart
	if r == nil || r.Replaced == nil || r.Exec == nil {
		return "", false
	}
	path, replaced := r.Replaced()
	if !replaced {
		return "", false
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return path, true
}

// restartAfterUpdate goes on after an installation: it restarts at once, asks first when that would drop
// input, and does nothing where the editor cannot restart or work is in flight.
func (m *Model) restartAfterUpdate(from, to string) tea.Cmd {
	path, ok := m.restartTarget()
	if !ok || m.busy != "" || m.vaultBusy {
		return nil
	}
	m.restartPath, m.restartFrom, m.restartTo = path, from, to
	if m.restartAsks() {
		m.restartBack = m.screen
		m.screen = screenRestart
		return nil
	}
	return m.restartNow()
}

// restartNow replaces this process with the new program. The terminal is released first and restored if the
// replacement fails, so the editor runs on as before.
func (m *Model) restartNow() tea.Cmd {
	m.stopTest()
	unlocked := m.updateUnlocked
	encoded, err := json.Marshal(handoff{Version: handoffVersion, From: m.restartFrom, To: m.restartTo,
		Unlocked: unlocked})
	if err != nil || len(encoded) > maxHandoffBytes {
		return nil
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, handoffEnv+"=") {
			env = append(env, entry)
		}
	}
	env = append(env, handoffEnv+"="+string(encoded))
	command := &restartCommand{run: func() error { return m.restart.Exec(m.restartPath, os.Args, env) }}
	return m.execute(command, func(err error) tea.Msg { return restartFailedMsg{err: err} })
}

// restartFailedMsg reports a replacement that did not happen.
type restartFailedMsg struct{ err error }

// restartFailed leaves the editor on the old version with the hint an update always gave.
func (m *Model) restartFailed(msg restartFailedMsg) {
	m.status = m.restartHint()
	if msg.err != nil {
		m.fail = "restart failed: " + m.redactor.Error(msg.err)
	}
}

// restartHint is what an update says where the editor does not restart itself.
func (m *Model) restartHint() string {
	status := "Updated to " + m.updated + ". Restart qatlas tui to use it."
	if noter, ok := m.updater.(updateNoter); ok && noter.Note() != "" {
		status += " " + noter.Note()
	}
	return status
}

// restartCommand is the process replacement as a command the event loop runs with the terminal released.
type restartCommand struct{ run func() error }

func (c *restartCommand) Run() error { return c.run() }

func (*restartCommand) SetStdin(io.Reader)  {}
func (*restartCommand) SetStdout(io.Writer) {}
func (*restartCommand) SetStderr(io.Writer) {}

// updateRestart answers the question that asks before a restart drops unsaved input. y restarts and drops
// it; anything that keeps the editor leaves it as it was, on the old version.
func (m *Model) updateRestartConfirm(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "y":
		m.screen = m.restartBack
		return m.restartNow()
	case "n", "esc":
		m.screen = m.restartBack
		m.status = m.restartHint()
	}
	return nil
}

// restartView asks before the restart that would drop unsaved input.
func (m *Model) restartView() string {
	return m.wrapped(titleStyle, "Restart qatlas tui into "+m.updated+"?") + "\n" +
		m.wrapped(hintStyle, "y restart and discard · n/esc not now") + "\n\n" +
		m.wrapped(lipgloss.NewStyle(), "The update is installed. Restarting replaces this editor with the new "+
			"version and drops the input that is not saved yet. Not now keeps editing on the old version.")
}
