package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// schemaOneFixture writes the keyring fixture as a schema-1 file, the way a former build left it.
func schemaOneFixture(t *testing.T) string {
	t.Helper()
	dir := keyringFixture(t)
	if err := os.WriteFile(configIn(dir), []byte(strings.Replace(keyringConfig, "version: 3", "version: 1", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The first command of the new build migrates a schema-1 file and reports it on stderr alone; stdout keeps
// exactly the command's answer. The next command finds nothing to do.
func TestFirstCommandMigratesAndKeepsStdoutClean(t *testing.T) {
	dir := schemaOneFixture(t)
	opts := testOptionsIn(t, dir, nil)

	code, stdout, stderr := runWithInput(t, opts, "", "config", "validate", "--output", "json", "--config", configIn(dir))

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	var answer map[string]any
	if err := json.Unmarshal([]byte(stdout), &answer); err != nil || answer["valid"] != true {
		t.Fatalf("stdout = %q is not the answer alone: %v", stdout, err)
	}
	for _, want := range []string{"migrated " + configIn(dir) + " to configuration schema 3", "backed up as",
		"to go back, copy the backups back"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	cfg, err := config.Load(configIn(dir), defaultRegistry())
	if err != nil || cfg.Version != config.Version || cfg.Credentials["vault-reader"].ID == "" {
		t.Fatalf("Load() = %+v, %v", cfg, err)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "vault", "logs", "*.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	if data, _ := os.ReadFile(logs[0]); !strings.Contains(string(data), `"path":"cli","operation":"config.migrate"`) {
		t.Errorf("log = %s", data)
	}

	before, _ := os.ReadFile(configIn(dir))
	code, _, stderr = runWithInput(t, testOptionsIn(t, dir, nil), "", "config", "validate", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("second run: exit = %d, stderr = %q", code, stderr)
	}
	if after, _ := os.ReadFile(configIn(dir)); string(after) != string(before) {
		t.Error("the second run rewrote the file")
	}
}

// 'qatlas mcp' migrates too, and its stdout carries JSON-RPC and nothing else.
func TestMCPMigratesWithoutTouchingStdout(t *testing.T) {
	dir := schemaOneFixture(t)
	opts := testOptionsIn(t, dir, nil)
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + mcpTestMeta + `}}` + "\n"

	code, stdout, stderr := runWithInput(t, opts, input, "mcp", "--config", configIn(dir))

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var message struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil || message.JSONRPC != "2.0" {
			t.Errorf("stdout line %q is not JSON-RPC", line)
		}
	}
	if !strings.Contains(stderr, "migrated ") {
		t.Errorf("stderr = %q, want the report", stderr)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "vault", "logs", "*.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	if data, _ := os.ReadFile(logs[0]); !strings.Contains(string(data), `"path":"cli","operation":"config.migrate"`) {
		t.Errorf("log = %s", data)
	}
}

// An invalid schema-1 file is left byte for byte and without a backup; the command's own diagnostic, first on
// stderr, names the problem.
func TestInvalidFileIsNotMigrated(t *testing.T) {
	dir := schemaOneFixture(t)
	invalid := strings.Replace(strings.Replace(keyringConfig, "version: 3", "version: 1", 1), "credential: vault-reader",
		"credential: nobody", 1)
	if err := os.WriteFile(configIn(dir), []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, stderr := runWithInput(t, testOptionsIn(t, dir, nil), "", "config", "validate", "--config", configIn(dir))

	if !strings.HasPrefix(stderr, "qatlas: config-invalid: ") || !strings.Contains(stderr, "connections.wiki") {
		t.Errorf("stderr = %q", stderr)
	}
	if data, _ := os.ReadFile(configIn(dir)); string(data) != invalid {
		t.Error("the invalid file changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Errorf("a backup directory exists: %v", err)
	}
}

// The migration writes current provider and tool IDs for former ones, as every save does, and says so.
func TestMigrationNamesTheFormerIDsItRewrites(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, strings.Replace(formerNamesConfig, "version: 3", "version: 1", 1))

	code, _, stderr := runRenameCLI(t, "config", "validate", "--config", path)

	if code != exitOK || !strings.Contains(stderr, `qatlas: services.svc: the former provider "acmechat" is now written as "acme.chat"`) {
		t.Fatalf("exit = %d, stderr:\n%s", code, stderr)
	}
}

// A connection whose former permissions were broader than its tools needs its vault approval once more, and
// the migration says so on stderr.
func TestMigrationNamesTheApprovalsItNarrows(t *testing.T) {
	dir := t.TempDir()
	file := `version: 2
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.test
credentials:
  vault-reader:
    type: vault
connections:
  broad:
    service: wiki
    credential: vault-reader
    permissions: [read, update]
    tools: [bookstack.pages.list]
  plain:
    service: wiki
    credential: vault-reader
    permissions: [read, update]
`
	if err := os.WriteFile(configIn(dir), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	offer := func(string) (string, error) { return "synthetic-passphrase-7d21", nil }
	if err := vault.New(dir).Set("vault-reader", "token-id", "synthetic-id", offer); err != nil {
		t.Fatal(err)
	}

	opts := testOptionsIn(t, dir, nil)
	opts.Secrets = opts.Secrets.WithVault(vault.New(dir), nil)

	code, _, stderr := runWithInput(t, opts, "", "config", "validate", "--config", configIn(dir))

	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "vault approval once more ('qatlas vault approve' or 'qatlas tui'): broad\n") {
		t.Errorf("stderr does not name exactly the narrowed connection:\n%s", stderr)
	}
}

// The migration message names only the schema steps the file actually passed.
func TestMigrationChangeNamesOnlyThePassedSteps(t *testing.T) {
	if got := migrationChange(1); !strings.Contains(got, "stable ids") || !strings.Contains(got, "effects") {
		t.Errorf("v1 message = %q, want both steps", got)
	}
	if got := migrationChange(2); strings.Contains(got, "stable ids") || !strings.Contains(got, "effects") {
		t.Errorf("v2 message = %q, want the effects step alone", got)
	}
}
