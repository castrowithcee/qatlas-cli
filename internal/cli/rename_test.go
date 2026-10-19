package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// treeIn returns every file below dir by its relative path, with its content; the configuration's lock file
// is how a write takes its lock, never state.
func treeIn(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".lock") {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		out[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// renameVault is twoConnectionVaultFixture, written with ids, with both connections approved in an encrypted vault and an agent
// token whose vorbild is wiki.
func renameVault(t *testing.T) string {
	t.Helper()
	dir := twoConnectionVaultFixture(t)
	// The ids a rename must keep are the ones the file was written with.
	if err := config.NewStore(configIn(dir), defaultRegistry()).Save(loadConfig(t, dir)); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatal(err)
	}
	approveConnections(t, dir, v)
	if _, err := v.CreateToken("helper", []string{"wiki"}, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestConfigRenameConnectionCarriesItsApproval(t *testing.T) {
	dir := renameVault(t)
	id := loadConfig(t, dir).Connections["wiki"].ID
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	var question string
	withVaultConfirm(t, func(prompt string) (bool, error) {
		question = prompt
		return true, nil
	})

	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"rename connection wiki -> wiki-main\n", "  id        " + id + ", kept\n",
		"  rewrites  defaults.connections.knowledge\n", "  approval  approved; carried over, so wiki-main stays approved\n",
		"  warning   agent token helper names wiki as a vorbild", "  outside   agent calls and scripts",
		"the vault files are backed up as ", "renamed connection wiki to wiki-main\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if !strings.Contains(question, "rename connection wiki to wiki-main, ending the cover of agent token helper") {
		t.Errorf("question = %q, want the lapsing token named", question)
	}

	cfg := loadConfig(t, dir)
	if cfg.Connections["wiki-main"].ID != id || cfg.Defaults.Connections["knowledge"] != "wiki-main" {
		t.Errorf("configuration = %+v", cfg)
	}
	fresh := vault.New(dir)
	if _, err := fresh.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if err := fresh.CheckApproval(connectionScope(t, dir, "wiki-main")); err != nil {
		t.Errorf("CheckApproval(wiki-main) = %v, want it approved without a new approval", err)
	}
	report, err := approval.Pending(cfg, fresh)
	if err != nil || len(report.Stale) != 0 || len(report.Open) != 0 {
		t.Errorf("Pending() = %+v, %v, want nothing open or stale", report, err)
	}

	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "logs", "verify", "--output", "json",
		"--config", configIn(dir))
	var verified logsVerifyDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &verified) != nil || verified.Broken || len(verified.Days) != 1 {
		t.Fatalf("vault logs verify: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	var logged []invokelog.Entry
	matches, _ := filepath.Glob(filepath.Join(dir, vault.DirName, "logs", "*.jsonl"))
	for _, path := range matches {
		data, _ := os.ReadFile(path)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var e invokelog.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatal(err)
			}
			logged = append(logged, e)
		}
	}
	if len(logged) != 1 || logged[0].Operation != invokelog.OperationConnectionRename ||
		logged[0].FormerConnection != "wiki" || logged[0].Connection != "wiki-main" || logged[0].ConnectionID != id {
		t.Errorf("log = %+v, want exactly one rename entry", logged)
	}
}

// A dry run without a terminal needs no passphrase, reads no locked vault, and changes no file.
func TestConfigRenameDryRunChangesNothing(t *testing.T) {
	dir := renameVault(t)
	withInteractive(t, false)
	before := treeIn(t, dir)

	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--dry-run", "--output", "json", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	var doc renameDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if !doc.DryRun || doc.Renamed || !doc.VaultUnread || doc.Kind != "connection" || doc.Old != "wiki" ||
		doc.New != "wiki-main" || !reflect.DeepEqual(doc.References, []string{"defaults.connections.knowledge"}) ||
		len(doc.Outside) == 0 {
		t.Errorf("document = %+v", doc)
	}
	if after := treeIn(t, dir); !reflect.DeepEqual(after, before) {
		t.Error("a dry run changed a file")
	}

	code, stdout, _ = runWithInput(t, &Options{}, "", "config", "rename", "service", "wiki", "wiki-eu",
		"--dry-run", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "  rewrites  connections.wiki.service\n") ||
		!strings.Contains(stdout, "  rewrites  connections.wiki2.service\n") ||
		!strings.Contains(stdout, "dry run: nothing was changed") {
		t.Errorf("service dry run: exit %d, stdout %q", code, stdout)
	}
	if after := treeIn(t, dir); !reflect.DeepEqual(after, before) {
		t.Error("a dry run changed a file")
	}
}

func TestConfigRenameRefusesAndCancels(t *testing.T) {
	dir := renameVault(t)
	before := treeIn(t, dir)

	withInteractive(t, false)
	code, _, stderr := runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, "admin-required") {
		t.Errorf("without a terminal: exit %d, stderr %q, want admin-required", code, stderr)
	}

	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, _, stderr = runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki2",
		"--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, `connection "wiki2" exists already`) {
		t.Errorf("onto a taken name: exit %d, stderr %q", code, stderr)
	}
	code, _, stderr = runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "--config", configIn(dir))
	if code != exitUsage {
		t.Errorf("one argument: exit %d, stderr %q, want usage", code, stderr)
	}

	withVaultConfirm(t, confirming(false))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "service", "wiki", "wiki-eu",
		"--config", configIn(dir))
	if code != exitOK || !strings.Contains(stderr, "rename cancelled, nothing was changed") ||
		strings.Contains(stdout, "renamed") {
		t.Errorf("declined: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if after := treeIn(t, dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused or declined rename changed a file")
	}
}

func TestConfigRenameService(t *testing.T) {
	dir := vaultCredentialFixture(t)
	withVaultConfirm(t, confirming(true))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "service", "wiki", "wiki-eu",
		"--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "renamed service wiki to wiki-eu") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	cfg := loadConfig(t, dir)
	if _, ok := cfg.Services["wiki-eu"]; !ok || cfg.Connections["wiki"].Service != "wiki-eu" {
		t.Errorf("configuration = %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, vault.DirName)); !os.IsNotExist(err) {
		t.Errorf("a service rename created the vault directory or a log: %v", err)
	}
}

// A rename that stopped after the vault but before the configuration leaves an extra approval that names its
// connection; 'vault approve' says where it comes from and how to finish.
func TestVaultApproveNamesARenameLeftover(t *testing.T) {
	dir := renameVault(t)
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.CarryApproval("wiki", "wiki-main", loadConfig(t, dir).Connections["wiki"].ID); err != nil {
		t.Fatal(err)
	}
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "the approval of wiki-main is left over from an unfinished rename "+
		"of connection wiki: 'qatlas config rename connection wiki wiki-main' gives the connection that name") ||
		!strings.Contains(stdout, "removed 1 stale approval no longer read from the vault: wiki-main") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
