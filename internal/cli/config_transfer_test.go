package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const otherConfig = `
version: 3
services:
  other:
    provider: bookstack
    base_url: https://other.example.invalid
credentials:
  other-vault:
    type: vault
connections:
  other:
    service: other
    credential: other-vault
defaults:
  connections:
    notes: other
`

const transferConfig = `
version: 3
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  wiki-vault:
    type: vault
  env-reader:
    type: env
    values:
      token-id: TEST_ENV_TOKEN_ID
      token-secret: TEST_ENV_TOKEN_SECRET
connections:
  wiki:
    service: wiki
    credential: wiki-vault
    paths: [~/projects/kunde]
  wiki-env:
    service: wiki
    credential: env-reader
defaults:
  connections:
    knowledge: wiki
`

// transferSource is an installation with a secret in its vault and a connection that brings a path binding.
func transferSource(t *testing.T) string {
	t.Helper()
	dir := twoConnectionVaultFixture(t)
	if err := os.WriteFile(configIn(dir), []byte(transferConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.New(dir).Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// transferTarget is an installation with no configuration yet.
func transferTarget(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	return t.TempDir()
}

func runTransfer(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	return runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir))...)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exportAll(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "export.yaml")
	code, stdout, stderr := runTransfer(t, dir, "config", "export", file, "--all", "--defaults")
	if code != exitOK {
		t.Fatalf("export: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	return file
}

func TestConfigTransferRequiresATerminal(t *testing.T) {
	dir := transferSource(t)
	file := filepath.Join(t.TempDir(), "export.yaml")
	withInteractive(t, false)
	for _, args := range [][]string{
		{"config", "export", file, "--all"},
		{"config", "import", file},
		{"config", "import", file, "--yes", "--agent"},
	} {
		code, stdout, stderr := runTransfer(t, dir, args...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q, want admin-required", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("export wrote a file without a terminal")
	}
}

func TestConfigExportImportRoundTripAndModes(t *testing.T) {
	withInteractive(t, true)
	source := transferSource(t)
	file := filepath.Join(t.TempDir(), "export.yaml")
	code, stdout, stderr := runTransfer(t, source, "config", "export", file, "--connection", "wiki", "--defaults")
	if code != exitOK || stderr != "" {
		t.Fatalf("export: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"exported to " + file, "connections", "wiki", "dependency      service wiki, for wiki",
		"dependency      credential wiki-vault, for wiki", "without secret  wiki-vault"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("export stdout = %q, want %q", stdout, want)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("export file mode = %v, %v, want 0600", info, err)
		}
	}
	if text := readFile(t, file); strings.Contains(text, canaryVault) || strings.Contains(stdout+stderr, canaryVault) {
		t.Error("the export or its output holds the vault secret")
	}

	// Into an empty installation, confirmed.
	target := transferTarget(t)
	var question string
	withVaultConfirm(t, func(prompt string) (bool, error) { question = prompt; return true, nil })
	code, stdout, stderr = runTransfer(t, target, "config", "import", file)
	if code != exitOK || stderr != "" {
		t.Fatalf("import: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"mode create", "create   connections wiki", "create   services wiki",
		"brings local access from the file: paths", "without secret here until one is set: wiki-vault",
		"created 4, replaced 0, skipped 0", "qatlas credential set", "qatlas vault approve"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("import stdout = %q, want %q", stdout, want)
		}
	}
	if !strings.Contains(question, "4 to create, 0 to replace") {
		t.Errorf("question = %q", question)
	}
	cfg := loadConfig(t, target)
	if cfg.Connections["wiki"].ID == "" || cfg.Defaults.Connections["knowledge"] != "wiki" || len(cfg.Credentials) != 1 {
		t.Errorf("target configuration = %+v", cfg)
	}
	if strings.Contains(readFile(t, configIn(target)), canaryVault) {
		t.Error("the target configuration holds the secret")
	}

	// Create stops on a name that exists; nothing changes.
	before := readFile(t, configIn(target))
	code, stdout, stderr = runTransfer(t, target, "config", "import", file, "--yes")
	if code != exitUsage || !strings.Contains(stderr, "already has") || stdout != "" {
		t.Errorf("create over existing: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if readFile(t, configIn(target)) != before {
		t.Error("create over existing changed the configuration")
	}

	// Merge skips everything that is there.
	code, stdout, _ = runTransfer(t, target, "config", "import", file, "--mode", "merge", "--yes")
	if code != exitOK || !strings.Contains(stdout, "nothing to import") || readFile(t, configIn(target)) != before {
		t.Errorf("merge: exit %d, stdout %q", code, stdout)
	}

	// Overwrite replaces and keeps the target's ids.
	id := cfg.Connections["wiki"].ID
	code, stdout, stderr = runTransfer(t, target, "config", "import", file, "--mode", "overwrite", "--yes")
	if code != exitOK || !strings.Contains(stdout, "replace  connections wiki") ||
		!strings.Contains(stdout, "secret here stays as it is") || !strings.Contains(stdout, "replaced 3") {
		t.Fatalf("overwrite: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got := loadConfig(t, target).Connections["wiki"].ID; got != id {
		t.Errorf("id after overwrite = %q, want %q", got, id)
	}

	// Into a filled installation, the file's other entries are added next to the existing ones.
	filled := transferSource(t)
	code, stdout, stderr = runTransfer(t, filled, "config", "import", file, "--mode", "merge", "--dry-run")
	if code != exitOK || !strings.Contains(stdout, "skip     connections wiki") || !strings.Contains(stdout, "dry run") {
		t.Errorf("merge into filled: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestConfigImportWritesNothingWithoutConfirmation(t *testing.T) {
	withInteractive(t, true)
	file := exportAll(t, transferSource(t))
	target := transferTarget(t)
	if err := os.WriteFile(configIn(target), []byte(otherConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, configIn(target))

	asked := 0
	withVaultConfirm(t, func(string) (bool, error) { asked++; return false, nil })
	code, stdout, stderr := runTransfer(t, target, "config", "import", file)
	if code != exitOK || asked != 1 || !strings.Contains(stderr, "cancelled, nothing was changed") ||
		!strings.Contains(stdout, "create   services wiki") {
		t.Errorf("declined: exit %d, asked %d, stdout %q, stderr %q", code, asked, stdout, stderr)
	}
	code, stdout, _ = runTransfer(t, target, "config", "import", file, "--dry-run")
	if code != exitOK || asked != 1 || !strings.Contains(stdout, "dry run: nothing was changed") {
		t.Errorf("dry run: exit %d, asked %d, stdout %q", code, asked, stdout)
	}
	if readFile(t, configIn(target)) != before {
		t.Error("the configuration changed without a confirmation")
	}
}

func TestConfigImportRefusesAChangeSincePlan(t *testing.T) {
	withInteractive(t, true)
	file := exportAll(t, transferSource(t))
	target := transferTarget(t)
	if err := os.WriteFile(configIn(target), []byte(otherConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(otherConfig, "notes: other", "docs: other", 1)
	withVaultConfirm(t, func(string) (bool, error) {
		return true, os.WriteFile(configIn(target), []byte(changed), 0o600)
	})
	code, _, stderr := runTransfer(t, target, "config", "import", file)
	if code != exitUsage || !strings.Contains(stderr, "nothing was written") {
		t.Errorf("exit %d, stderr %q, want a conflict", code, stderr)
	}
	if readFile(t, configIn(target)) != changed {
		t.Error("the import overwrote the concurrent change")
	}
}

func TestConfigExportNeverReplacesAFileAndChecksNames(t *testing.T) {
	withInteractive(t, true)
	source := transferSource(t)
	file := filepath.Join(t.TempDir(), "export.yaml")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runTransfer(t, source, "config", "export", file, "--all")
	if code != exitUsage || !strings.Contains(stderr, "already exists") || readFile(t, file) != "keep" {
		t.Errorf("existing file: exit %d, stderr %q", code, stderr)
	}
	fresh := filepath.Join(t.TempDir(), "x.yaml")
	code, _, stderr = runTransfer(t, source, "config", "export", fresh, "--connection", "nope")
	if code != exitUsage || !strings.Contains(stderr, `"nope" does not exist`) {
		t.Errorf("unknown name: exit %d, stderr %q", code, stderr)
	}
	code, _, stderr = runTransfer(t, source, "config", "export", fresh)
	if code != exitUsage || !strings.Contains(stderr, "nothing is selected") {
		t.Errorf("no selection: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Error("a failed export left a file")
	}
}

func TestConfigExportDryRunWritesNothing(t *testing.T) {
	withInteractive(t, true)
	source := transferSource(t)
	file := filepath.Join(t.TempDir(), "x.yaml")
	code, stdout, _ := runTransfer(t, source, "config", "export", file, "--connection", "wiki-env", "--defaults", "--dry-run")
	if code != exitOK || !strings.Contains(stdout, "export preview") ||
		!strings.Contains(stdout, "left out        defaults knowledge") {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("dry run wrote a file")
	}
}

func TestConfigImportRefusesUnknownFormatVersion(t *testing.T) {
	withInteractive(t, true)
	file := filepath.Join(t.TempDir(), "future.yaml")
	if err := os.WriteFile(file, []byte("format: qatlas-config-export\nformat_version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := transferTarget(t)
	code, stdout, stderr := runTransfer(t, target, "config", "import", file, "--yes")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "format version 2") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(configIn(target)); err == nil {
		t.Error("a refused import created a configuration")
	}
}

func TestConfigTransferJSONOutput(t *testing.T) {
	withInteractive(t, true)
	source := transferSource(t)
	file := filepath.Join(t.TempDir(), "export.yaml")
	code, stdout, stderr := runTransfer(t, source, "config", "export", file, "--connection", "wiki", "--output", "json")
	var exported exportDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &exported) != nil || !exported.Written ||
		len(exported.Dependencies) != 2 || exported.SecretsNotIncluded[0] != "wiki-vault" {
		t.Fatalf("export: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	target := transferTarget(t)
	withVaultConfirm(t, func(string) (bool, error) { return true, nil })
	code, stdout, stderr = runTransfer(t, target, "config", "import", file, "--output", "json")
	var imported importDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &imported) != nil || !imported.Imported ||
		imported.Created != 3 || imported.CredentialsWithoutSecret[0] != "wiki-vault" ||
		imported.Entries[0].Access == nil || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("import: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = runTransfer(t, transferTarget(t), "config", "import", file, "--dry-run", "--output", "toon")
	if code != exitOK || !strings.Contains(stdout, "dry_run") || strings.Contains(stdout, "imported 3") {
		t.Errorf("toon: exit %d, stdout %q", code, stdout)
	}
	if _, err := config.Load(configIn(target), defaultRegistry()); err != nil {
		t.Errorf("target configuration: %v", err)
	}
}

func TestConfigExportHasNoSecretFlag(t *testing.T) {
	cmd, _, err := DocumentationCommand("test").Find([]string{"config", "export"})
	if err != nil {
		t.Fatal(err)
	}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		for _, word := range []string{"secret", "value", "token", "password", "passphrase"} {
			if strings.Contains(f.Name, word) {
				t.Errorf("export has the flag --%s", f.Name)
			}
		}
	})
}
