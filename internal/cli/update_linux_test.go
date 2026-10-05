package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// updateRun runs 'qatlas update' with args against a local release and reports the exit code, stderr, the
// program afterwards, and how often the replacement was reached.
func updateRun(t *testing.T, opts *Options, args ...string) (code int, stderr, program string, replaced int) {
	t.Helper()
	executable := installedProgram(t)
	opts.Updater = releaseUpdater(t, executable, func() { replaced++ })
	code, _, stderr = runWithInput(t, opts, "", append([]string{"update", "--config", configIn(t.TempDir())}, args...)...)
	data, _ := os.ReadFile(executable)
	return code, stderr, string(data), replaced
}

func TestUpdateAsksBeforeItReplaces(t *testing.T) {
	for _, tt := range []struct {
		name    string
		answer  bool
		err     error
		updated bool
	}{
		{name: "y", answer: true, updated: true},
		{name: "n"},
		{name: "empty or unreadable answer", err: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var asked []string
			withVaultConfirm(t, func(prompt string) (bool, error) {
				asked = append(asked, prompt)
				return tt.answer, tt.err
			})
			code, stderr, program, replaced := updateRun(t, &Options{})
			if code != exitOK || len(asked) != 1 {
				t.Fatalf("exit code = %d, questions = %d, stderr = %q", code, len(asked), stderr)
			}
			for _, want := range []string{"Update qatlas to v1.1.0?", "vault process is handed over to the new version", "locked first otherwise",
				"'qatlas vault unlock'", "'qatlas mcp' servers restart themselves", "'qatlas tui' and 'qatlas web'"} {
				if !strings.Contains(asked[0], want) {
					t.Errorf("the question does not say %q: %q", want, asked[0])
				}
			}
			if tt.updated != (program == "new-program") || tt.updated != (replaced == 1) {
				t.Errorf("program = %q, replaced %d times, want updated %v", program, replaced, tt.updated)
			}
			if !tt.updated && !strings.Contains(stderr, "cancelled") {
				t.Errorf("stderr = %q does not say the update was cancelled", stderr)
			}
		})
	}
}

func TestUpdateYesSkipsTheQuestion(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("--yes asked")
		return false, nil
	})
	withInteractive(t, false)
	code, _, program, replaced := updateRun(t, &Options{}, "--yes")
	if code != exitOK || program != "new-program" || replaced != 1 {
		t.Errorf("exit code = %d, program = %q, replaced %d", code, program, replaced)
	}
}

func TestUpdateWithoutTerminalIsRefused(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("a refused update asked")
		return true, nil
	})
	for name, args := range map[string][]string{"pipe": nil, "agent": {"--agent"}} {
		withInteractive(t, name == "agent")
		code, stderr, program, replaced := updateRun(t, &Options{}, args...)
		if code != exitUsage || !strings.Contains(stderr, "--yes") || !strings.Contains(stderr, "agent") ||
			program != "old-program" || replaced != 0 {
			t.Errorf("%s: exit code = %d, stderr = %q, program = %q, replaced %d", name, code, stderr, program, replaced)
		}
	}
}

func TestUpdateCheckNeverAsks(t *testing.T) {
	withVaultConfirm(t, func(string) (bool, error) {
		t.Error("--check asked")
		return false, nil
	})
	withInteractive(t, false)
	code, _, program, replaced := updateRun(t, &Options{}, "--check")
	if code != exitOK || program != "old-program" || replaced != 0 {
		t.Errorf("exit code = %d, program = %q, replaced %d", code, program, replaced)
	}
}

// The vault process is locked after the confirmation only, never for an answer of n.
func TestUpdateLocksOnlyAfterYes(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	_, client := serveVaultInProcess(t, dir)
	running := func() bool {
		_, err := client.Status(context.Background())
		return err == nil
	}
	for _, answer := range []bool{false, true} {
		withVaultConfirm(t, func(string) (bool, error) {
			if !running() {
				t.Error("the vault process was stopped before the answer")
			}
			return answer, nil
		})
		executable := installedProgram(t)
		updater := releaseUpdater(t, executable, func() {})
		code, _, stderr := runWithInput(t, &Options{Updater: updater}, "", "update", "--config", configIn(dir))
		if code != exitOK {
			t.Fatalf("answer %v: exit code = %d, stderr = %q", answer, code, stderr)
		}
		if running() == answer {
			t.Errorf("answer %v: vault process running = %v", answer, running())
		}
	}
}

func TestConsequencesTextOfThePlatforms(t *testing.T) {
	linux := selfupdate.ConsequencesText("linux", 2)
	darwin := selfupdate.ConsequencesText("darwin", 1)
	other := selfupdate.ConsequencesText("windows", -1)
	if !strings.Contains(linux, "2 other qatlas processes of yours are running") || !strings.Contains(linux, "restart themselves") {
		t.Errorf("linux: %q", linux)
	}
	if !strings.Contains(darwin, "1 other qatlas process of yours is running") || !strings.Contains(darwin, "restart themselves") {
		t.Errorf("darwin: %q", darwin)
	}
	if strings.Contains(other, "other qatlas") || !strings.Contains(other, "old version") || strings.Contains(other, "restart themselves") {
		t.Errorf("windows: %q", other)
	}
}

// The editor's updater locks the vault process right before the program is replaced, the way the command does.
func TestTUIUpdaterLocksFirst(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	_, client := serveVaultInProcess(t, dir)
	executable := installedProgram(t)
	seam := releaseUpdater(t, executable, func() {
		if _, err := client.Status(context.Background()); err == nil {
			t.Error("the vault process still ran right before the replacement")
		}
		if data, _ := os.ReadFile(executable); string(data) != "old-program" {
			t.Error("the program was replaced before the vault process was locked")
		}
	})
	updater := tuiUpdater(&Options{Updater: seam, Config: configIn(dir)}, "v1.0.0")
	result, err := updater.Update(context.Background())
	if err != nil || !result.Updated {
		t.Fatalf("Update() = %+v, %v", result, err)
	}
	if note := updater.(interface{ Note() string }).Note(); !strings.Contains(note, "vault process was locked") {
		t.Errorf("note = %q", note)
	}
}
