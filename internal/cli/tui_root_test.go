package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// runBare runs the root command without arguments, with the terminal check answering terminal and the
// editor start replaced by a recorder. It returns stdout, the exit code and whether the editor was started.
func runBare(t *testing.T, terminal bool, args ...string) (string, int, bool) {
	t.Helper()
	origTerminal, origStart := checkTerminal, startTUI
	t.Cleanup(func() { checkTerminal, startTUI = origTerminal, origStart })
	checkTerminal = func() bool { return terminal }
	started := false
	startTUI = func(*Options, *capability.Registry, string) error { started = true; return nil }
	var stdout, stderr bytes.Buffer
	opts := &Options{}
	code := run(newRootCommand(opts, sharedRegistry()), opts, args, &stdout, &stderr)
	return stdout.String(), code, started
}

func TestBareQatlasOpensTheEditorAtATerminal(t *testing.T) {
	out, code, started := runBare(t, true)
	if code != exitOK || !started || out != "" {
		t.Errorf("code = %d, started = %v, stdout = %q, want the editor and no overview", code, started, out)
	}
}

func TestBareQatlasWithoutATerminalPrintsTheOverview(t *testing.T) {
	out, code, started := runBare(t, false)
	helpOut, _, _ := runBare(t, false, "--help")
	if code != exitOK || started {
		t.Errorf("code = %d, started = %v, want the overview and exit 0", code, started)
	}
	if out != helpOut || !strings.Contains(out, "Usage:") {
		t.Errorf("stdout = %q, want the root help", out)
	}
}

func TestBareQatlasInAgentModePrintsTheOverviewAtATerminal(t *testing.T) {
	out, code, started := runBare(t, true, "--agent")
	if code != exitOK || started || !strings.Contains(out, "Usage:") {
		t.Errorf("code = %d, started = %v, stdout = %q, want only the overview", code, started, out)
	}
}

func TestBareQatlasStillRejectsAnUnknownArgumentAtATerminal(t *testing.T) {
	_, code, started := runBare(t, true, "bogus")
	if code != exitUsage || started {
		t.Errorf("code = %d, started = %v, want a usage error without the editor", code, started)
	}
}

func TestRootHelpNamesBothEntries(t *testing.T) {
	help, _, _ := runBare(t, false, "--help")
	for _, want := range []string{"qatlas          a person", "qatlas agents   an agent"} {
		if !strings.Contains(help, want) {
			t.Errorf("root help lacks %q:\n%s", want, help)
		}
	}
}

// The restarted program starts with the subcommand tui, and with --config only where the call had it.
func TestRestartStartsTheSuccessorWithTheTuiSubcommand(t *testing.T) {
	if got, want := restartArgs(&Options{}), []string{"tui"}; !reflect.DeepEqual(got, want) {
		t.Errorf("restartArgs = %q, want %q", got, want)
	}
	if got, want := restartArgs(&Options{Config: "/etc/q.yaml"}), []string{"tui", "--config", "/etc/q.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("restartArgs = %q, want %q", got, want)
	}
}
