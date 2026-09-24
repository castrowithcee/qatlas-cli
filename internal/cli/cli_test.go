package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantCode     int
		wantStdout   string   // exact match when set
		wantInStdout []string // substring match
		wantStderr   bool     // true when stderr must carry a diagnostic
	}{
		{
			name:         "no arguments prints help",
			args:         nil,
			wantCode:     exitOK,
			wantInStdout: []string{"Usage:", "qatlas"},
		},
		{
			name:     "help lists all global flags",
			args:     []string{"--help"},
			wantCode: exitOK,
			wantInStdout: []string{
				"--config", "--connection", "--agent", "--output", "--version", "update",
			},
		},
		{
			name:       "version output is deterministic",
			args:       []string{"--version"},
			wantCode:   exitOK,
			wantStdout: "qatlas dev\n",
		},
		{
			name:       "dev build cannot self-update",
			args:       []string{"update", "--check"},
			wantCode:   exitUsage,
			wantStdout: "",
			wantStderr: true,
		},
		{
			name:       "unknown flag is a usage error",
			args:       []string{"--nope"},
			wantCode:   exitUsage,
			wantStdout: "",
			wantStderr: true,
		},
		{
			name:       "unknown command is a usage error",
			args:       []string{"frobnicate"},
			wantCode:   exitUsage,
			wantStdout: "",
			wantStderr: true,
		},
		{
			name:         "global flags are accepted",
			args:         []string{"--agent", "--output", "json", "--connection", "wiki", "--config", "/nonexistent.yaml", "--help"},
			wantCode:     exitOK,
			wantInStdout: []string{"Usage:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if tt.wantStdout != "" || tt.wantCode == exitUsage {
				if got := stdout.String(); got != tt.wantStdout {
					t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
				}
			}
			for _, want := range tt.wantInStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout does not contain %q:\n%s", want, stdout.String())
				}
			}
			if got := stderr.Len() > 0; got != tt.wantStderr {
				t.Errorf("stderr non-empty = %v, want %v (stderr: %s)", got, tt.wantStderr, stderr.String())
			}
		})
	}
}

// A global flag must not promise a projection or a page size the commands no longer apply. --fields now
// belongs to config validate, and --limit is gone; both must be rejected everywhere else.
func TestRemovedGlobalFlagsAreGone(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	for _, flag := range []string{"--fields", "--limit"} {
		if strings.Contains(stdout.String(), flag) {
			t.Errorf("the root help still offers %s:\n%s", flag, stdout.String())
		}
	}

	for _, args := range [][]string{
		{"tools", "--limit", "1"},
		{"tools", "--fields", "id"},
		{"describe", "bookstack.pages.list", "--limit", "1"},
		{"invoke", "bookstack.pages.list", "--limit", "1"},
	} {
		var stdout, stderr bytes.Buffer

		code := Run(args, &stdout, &stderr)

		if code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d (stderr: %s)", args, code, exitUsage, stderr.String())
		}
		if stdout.String() != "" {
			t.Errorf("%v: stdout = %q, want empty", args, stdout.String())
		}
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, exitOK},
		{"internal runtime error", errors.New("provider request failed"), exitRuntime},
		{"wrapped runtime error", errors.Join(errors.New("read config"), errors.New("io failure")), exitRuntime},
		{"usage error", &UsageError{errors.New("unknown flag")}, exitUsage},
		{"wrapped usage error", errors.Join(errors.New("context"), &UsageError{errors.New("bad value")}), exitUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// A runtime error surfacing from the command layer reaches stderr and exit code 1 without any output on
// stdout. There is no public test command, so the failing RunE is injected into a local root command and
// driven through the same run path as the binary.
func TestRunRuntimeError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	opts := &Options{}
	cmd := newRootCommand(opts, defaultRegistry())
	cmd.RunE = func(*cobra.Command, []string) error { return errors.New("synthetic internal failure") }

	code := run(cmd, opts, nil, &stdout, &stderr)

	if code != exitRuntime {
		t.Errorf("exit code = %d, want %d", code, exitRuntime)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got != "qatlas: runtime: synthetic internal failure\n" {
		t.Errorf("stderr = %q, want the diagnostic line only", got)
	}
}

func TestRunWritesUsageBeforeCarriedAudit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := &Options{Redactor: &redact.Redactor{}}
	cmd := newRootCommand(opts, defaultRegistry())
	cmd.RunE = func(*cobra.Command, []string) error {
		return withAudit(newSyntaxError(errors.New("confirmed usage failure")),
			[]byte(`{"request_id":"audit-id","result":"error"}`+"\n"))
	}

	code := run(cmd, opts, nil, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	value := stderr.String()
	diagnostic := strings.Index(value, "qatlas: usage: confirmed usage failure\n")
	usage := strings.Index(value, "Usage:")
	audit := strings.Index(value, `{"request_id":"audit-id","result":"error"}`)
	if diagnostic != 0 || usage < 0 || audit < 0 || !(diagnostic < usage && usage < audit) {
		t.Fatalf("stderr order is diagnostic, usage, audit: %q", value)
	}
}

// Only a malformed command line is followed by the usage block. A well-formed request that is refused keeps
// exit code 2 and says what is wrong and what to do, without burying that under the flag list.
func TestUsageFollowsOnlySyntaxErrors(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, "version: 1\n")
	for _, tt := range []struct {
		name  string
		args  []string
		usage bool
	}{
		{"unknown command", []string{"frobnicate"}, true},
		{"unknown flag", []string{"tools", "bookstack", "--nope"}, true},
		{"unknown output format", []string{"providers", "--output", "yaml"}, true},
		{"too many arguments", []string{"describe", "a", "b"}, true},
		{"unexpected argument", []string{"config", "validate", "surplus"}, true},
		{"missing configuration", []string{"tools", "bookstack", "--config", "/nonexistent/config.yaml"}, false},
		{"unknown namespace", []string{"tools", "absent", "--config", cfg}, false},
		{"unknown provider", []string{"connections", "absent", "--config", cfg}, false},
		{"unknown tool", []string{"describe", "absent.pages.get", "--config", cfg}, false},
		{"unknown connection", []string{"invoke", "bookstack.pages.list", "--connection", "absent", "--config", cfg}, false},
		{"invalid arguments", []string{"invoke", "bookstack.pages.get", "--arg", "id=x", "--config", cfg}, false},
		{"scalar discovery format", []string{"describe", "bookstack.pages.list", "--output", "table", "--config", cfg}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runTools(t, nil, tt.args...)
			if code != exitUsage || stdout != "" || !strings.HasPrefix(stderr, "qatlas: ") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if got := strings.Contains(stderr, "\nUsage:\n"); got != tt.usage {
				t.Errorf("usage block = %v, want %v:\n%s", got, tt.usage, stderr)
			}
		})
	}
}

// The code already says invalid-request, so the message does not repeat it.
func TestInvalidRequestIsNamedOnce(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, "version: 1\n")
	code, _, stderr := runTools(t, nil, "invoke", "bookstack.pages.get", "--arg", "id=x", "--config", cfg)
	if code != exitUsage || !strings.HasPrefix(stderr, "qatlas: invalid-request: ") ||
		strings.Contains(strings.ToLower(stderr), "invalid request") {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
}

// Options receive the parsed global flags so the application core can consume them as a value.
func TestOptionsAreParsed(t *testing.T) {
	opts := &Options{}
	cmd := newRootCommand(opts, defaultRegistry())
	cmd.SetArgs([]string{"--config", "/tmp/c.yaml", "--connection", "wiki", "--agent", "--output", "compact"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	if opts.Config != "/tmp/c.yaml" || opts.Connection != "wiki" || !opts.Agent || opts.Output != "compact" {
		t.Errorf("options = %+v", *opts)
	}
}

func TestEmitRedactsBeforeEncoding(t *testing.T) {
	const canary = `canary-"\|=value`

	tests := []struct {
		format output.Format
		want   string
	}{
		{output.FormatTable, "ID  NAME\n7   before [redacted] after\n"},
		{output.FormatJSON, "[{\"id\":7,\"name\":\"before [redacted] after\"}]\n"},
		{output.FormatCompact, "id|name\n7|before [redacted] after\n"},
	}
	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			var stdout bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&stdout)
			redactor := &redact.Redactor{}
			redactor.Add(canary)
			opts := &Options{Format: tt.format, Redactor: redactor}
			result := output.Collection{
				Columns: []string{"id", "name"},
				Rows: []output.Row{
					{"name": "before " + canary + " after", "id": int64(7)},
				},
			}

			if err := emit(cmd, opts, result); err != nil {
				t.Fatalf("emit() = %v", err)
			}
			if got := stdout.String(); got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
			if strings.Contains(stdout.String(), canary) {
				t.Errorf("stdout leaks the secret: %q", stdout.String())
			}
			if tt.format == output.FormatJSON {
				var rows []map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
					t.Fatalf("stdout is not valid JSON: %v", err)
				}
				if _, ok := rows[0]["id"].(float64); !ok {
					t.Errorf("id = %T, want a JSON number", rows[0]["id"])
				}
			}
		})
	}
}

// Nothing shortens a result on its way out, and the caller's own value stays untouched.
func TestEmitKeepsEveryRowAndTheInput(t *testing.T) {
	const canary = "complete-canary-8a14"

	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	redactor := &redact.Redactor{}
	redactor.Add(canary)
	result := output.Collection{
		Columns: []string{"name", "count"},
		Rows: []output.Row{
			{"name": canary, "count": int64(1)},
			{"name": canary, "count": int64(2)},
		},
	}

	if err := emit(cmd, &Options{Format: output.FormatCompact, Redactor: redactor}, result); err != nil {
		t.Fatalf("emit() = %v", err)
	}
	if got, want := stdout.String(), "name|count\n[redacted]|1\n[redacted]|2\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := result.Rows[0]["name"]; got != canary {
		t.Errorf("input value = %q, want it unchanged", got)
	}
}
