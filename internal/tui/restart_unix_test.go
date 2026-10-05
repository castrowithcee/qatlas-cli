//go:build !windows

// The restart exists on Linux and macOS only, and these tests need a program file with execute bits.
package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// restartRecorder stands for the process replacement: it records what exec was asked to run, and the
// command the editor handed to the terminal, which a test runs itself.
type restartRecorder struct {
	program string
	execErr error
	calls   []execCall
	command tea.ExecCommand
	done    tea.ExecCallback
}

type execCall struct {
	path      string
	argv, env []string
}

// withRestart gives the editor a restart onto a real executable file of the test and records the rest.
func withRestart(t *testing.T, m *Model, replaced bool) *restartRecorder {
	t.Helper()
	program := filepath.Join(t.TempDir(), "qatlas")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &restartRecorder{program: program}
	m.restart = &Restart{
		Replaced: func() (string, bool) { return program, replaced },
		Exec: func(path string, argv, env []string) error {
			r.calls = append(r.calls, execCall{path, argv, env})
			return r.execErr
		},
	}
	m.execute = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
		r.command, r.done = c, fn
		return func() tea.Msg { return nil }
	}
	return r
}

// finishUpdate runs the installation the way the event loop does and returns the editor's next command.
func finishUpdate(m *Model) tea.Cmd {
	_, cmd := m.Update(updateDoneMsg{result: newerRelease().update})
	return cmd
}

func handoffOf(t *testing.T, call execCall) *handoff {
	t.Helper()
	var found []string
	for _, entry := range call.env {
		if value, ok := strings.CutPrefix(entry, handoffEnv+"="); ok {
			found = append(found, value)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the environment holds %d handoffs, want 1: %q", len(found), found)
	}
	h := decodeHandoff(found[0])
	if h == nil {
		t.Fatalf("the handoff %q does not decode", found[0])
	}
	return h
}

func TestAnUpdateRestartsTheEditorInPlace(t *testing.T) {
	t.Setenv(handoffEnv, "stale")
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	m.updater = newerRelease()
	r := withRestart(t, m, true)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m.release = newerRelease().check

	press(t, m, "u")
	_, cmd := m.Update(keyMsg("y"))
	deliver(m, cmd)
	if r.command == nil {
		t.Fatalf("the update did not restart the editor:\n%s", m.View())
	}
	if len(r.calls) != 0 {
		t.Fatalf("exec ran before the terminal was released")
	}
	if err := r.command.Run(); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("exec ran %d times, want 1", len(r.calls))
	}
	call := r.calls[0]
	if call.path != r.program || strings.Join(call.argv, "\x00") != strings.Join(os.Args, "\x00") {
		t.Errorf("exec(%q, %q), want the program %q with this process's arguments", call.path, call.argv, r.program)
	}
	h := handoffOf(t, call)
	if h.From != "v0.4.0" || h.To != "v0.5.0" || !h.Unlocked {
		t.Errorf("handoff = %+v, want v0.4.0 to v0.5.0 with the vault unlocked", h)
	}
}

func TestTheHandoffTellsWhetherTheVaultWasLocked(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	m.updater = newerRelease()
	r := withRestart(t, m, true)
	m.release = newerRelease().check
	pump(t, m, "u", "y")
	if r.command == nil {
		t.Fatal("no restart")
	}
	_ = r.command.Run()
	if h := handoffOf(t, r.calls[0]); h.Unlocked {
		t.Errorf("handoff = %+v, want a locked vault", h)
	}
}

func TestAFailedExecKeepsTheEditorRunning(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	m.updater = newerRelease()
	r := withRestart(t, m, true)
	r.execErr = errors.New("exec format error")
	m.release = newerRelease().check
	pump(t, m, "u", "y")
	if r.command == nil {
		t.Fatal("no restart")
	}
	err := r.command.Run()
	if err == nil {
		t.Fatal("the recorded exec did not fail")
	}
	_, cmd := m.Update(r.done(err))
	if cmd != nil || m.quitting {
		t.Errorf("a failed restart ended the editor")
	}
	if m.status != "Updated to v0.5.0. Restart qatlas tui to use it." || m.fail != "restart failed: exec format error" {
		t.Errorf("status %q, fail %q; want the hint and the failure", m.status, m.fail)
	}
}

func TestNoRestartWhereTheNewProgramCannotRun(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, *Model, *restartRecorder){
		"program not replaced": func(t *testing.T, m *Model, r *restartRecorder) {
			m.restart.Replaced = func() (string, bool) { return r.program, false }
		},
		"program missing": func(t *testing.T, m *Model, r *restartRecorder) {
			if err := os.Remove(r.program); err != nil {
				t.Fatal(err)
			}
		},
		"program not executable": func(t *testing.T, m *Model, r *restartRecorder) {
			if err := os.Chmod(r.program, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"no restart on this platform": func(t *testing.T, m *Model, r *restartRecorder) { m.restart = nil },
		"vault write in flight":       func(t *testing.T, m *Model, r *restartRecorder) { m.vaultBusy = true },
	} {
		t.Run(name, func(t *testing.T) {
			m := startedWith(t, newerRelease())
			r := withRestart(t, m, true)
			prepare(t, m, r)
			pump(t, m, "u", "y")
			if r.command != nil || len(r.calls) != 0 {
				t.Errorf("the editor restarted")
			}
			if !strings.Contains(m.View(), "Restart qatlas tui to use it.") {
				t.Errorf("the hint is missing:\n%s", m.View())
			}
		})
	}
}

func TestUnsavedInputBlocksTheRestartUntilItIsDiscarded(t *testing.T) {
	for _, answer := range []string{"n", "esc"} {
		m := startedWith(t, newerRelease())
		r := withRestart(t, m, true)
		press(t, m, "1", "n")
		typeText(t, m, "typed")
		m.Update(updateDoneMsg{result: newerRelease().update})
		if m.screen != screenRestart || r.command != nil {
			t.Fatalf("%s: screen %v, restart %v; want the question and no restart", answer, m.screen, r.command != nil)
		}
		if view := screenOf(m); !strings.Contains(view, "Restart qatlas tui into v0.5.0?") ||
			!strings.Contains(view, "y restart and discard") {
			t.Errorf("the question is missing:\n%s", view)
		}
		press(t, m, answer)
		if m.screen != screenForm || m.fieldValue("name") != "typed" || r.command != nil {
			t.Errorf("%s: screen %v name %q; want the form with its input and no restart", answer, m.screen, m.fieldValue("name"))
		}
		if !strings.Contains(m.View(), "Restart qatlas tui to use it.") {
			t.Errorf("%s: the hint is missing:\n%s", answer, m.View())
		}
	}

	m := startedWith(t, newerRelease())
	r := withRestart(t, m, true)
	press(t, m, "1", "n")
	typeText(t, m, "typed")
	m.Update(updateDoneMsg{result: newerRelease().update})
	_, cmd := m.Update(keyMsg("y"))
	deliver(m, cmd)
	if r.command == nil {
		t.Fatal("y did not restart")
	}
	if err := r.command.Run(); err != nil || len(r.calls) != 1 {
		t.Errorf("Run = %v, %d exec calls", err, len(r.calls))
	}

	// An untouched form drops nothing.
	m = startedWith(t, newerRelease())
	r = withRestart(t, m, true)
	press(t, m, "1", "n")
	m.Update(updateDoneMsg{result: newerRelease().update})
	if m.screen != screenForm || r.command == nil {
		t.Errorf("an unchanged form asked: screen %v", m.screen)
	}
}
