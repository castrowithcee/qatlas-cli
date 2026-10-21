package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	if err := config.NewStore(configIn(dir), sharedRegistry()).Save(loadConfig(t, dir)); err != nil {
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
	stopConnectionRename := func() string {
		dir := renameVault(t)
		v := vault.New(dir)
		if _, err := v.Unlock("s3cret-phrase"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := v.CarryApproval("wiki", "wiki-main", loadConfig(t, dir).Connections["wiki"].ID); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	dir := stopConnectionRename()
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--output", "json", "--config", configIn(dir))
	var doc vaultApproveDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &doc) != nil || !reflect.DeepEqual(doc.RenameLeftovers,
		[]renameLeftoverDocument{{Kind: "connection", Approval: "wiki-main", Connection: "wiki"}}) {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	dir = stopConnectionRename()
	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "the approval of wiki-main is left over from an unfinished rename "+
		"of connection wiki: 'qatlas config rename connection wiki wiki-main' gives the connection that name") ||
		!strings.Contains(stdout, "removed 1 stale approval no longer read from the vault: wiki-main") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestConfigRenameCredentialCarriesApprovalsAndNamesEnvVars(t *testing.T) {
	dir := renameVault(t)
	id := loadConfig(t, dir).Credentials["wiki-vault"].ID
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	var question string
	withVaultConfirm(t, func(prompt string) (bool, error) {
		question = prompt
		return true, nil
	})

	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "credential", "wiki-vault", "wiki-cred",
		"--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"rename credential wiki-vault -> wiki-cred\n", "  id        " + id + ", kept\n",
		"  rewrites  connections.wiki.credential\n", "  rewrites  connections.wiki2.credential\n",
		"  vault     entry wiki-vault -> wiki-cred, keeping its id and its secrets\n",
		"  reader    wiki: approved; carried over, so wiki stays approved\n",
		"  env       QATLAS_WIKI_VAULT_TOKEN_ID -> QATLAS_WIKI_CRED_TOKEN_ID (token-id)\n",
		"  outside   environment variables named after the credential, which override its stored secrets: it reads " +
			"them under its new name from now on, and qatlas changes no environment, not changed\n",
		"the vault files are backed up as ",
		"approvals carried over to wiki-cred: wiki, wiki2\n",
		"environment variables that override the stored secrets are read under the new name: ",
		", QATLAS_WIKI_VAULT_TOKEN_ID -> QATLAS_WIKI_CRED_TOKEN_ID, ", "; qatlas changed no environment\n",
		"renamed credential wiki-vault to wiki-cred\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if question != "rename credential wiki-vault to wiki-cred? [y/N] " || strings.Contains(stdout, canaryVault) {
		t.Errorf("question = %q, or a secret reached the output", question)
	}
	cfg := loadConfig(t, dir)
	if cfg.Credentials["wiki-cred"].ID != id || cfg.Connections["wiki2"].Credential != "wiki-cred" {
		t.Errorf("configuration = %+v", cfg)
	}
	fresh := vault.New(dir)
	if _, err := fresh.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if err := fresh.CheckApproval(connectionScope(t, dir, "wiki")); err != nil {
		t.Errorf("CheckApproval(wiki) = %v, want it approved without a new approval", err)
	}
	if report, err := approval.Pending(cfg, fresh); err != nil || len(report.Open) != 0 || len(report.Stale) != 0 {
		t.Errorf("Pending() = %+v, %v, want nothing open or stale", report, err)
	}
}

// A dry run lists every field of a credential's plan and changes nothing; without a terminal it reads no
// locked vault, and the rename itself is refused there.
func TestConfigRenameCredentialDryRunAndAdmin(t *testing.T) {
	dir := renameVault(t)
	before := treeIn(t, dir)

	withInteractive(t, false)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "credential", "wiki-vault", "wiki-cred",
		"--dry-run", "--output", "json", "--config", configIn(dir))
	var doc renameDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &doc) != nil {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !doc.DryRun || doc.Renamed || !doc.VaultUnread || doc.Kind != "credential" || doc.Credential == nil ||
		doc.Credential.VaultEntry || len(doc.Credential.Readers) != 2 || doc.Credential.Readers[0].Connection != "wiki" ||
		!slices.Contains(doc.Credential.EnvVars, envRenameDocument{Role: "token-id",
			Old: "QATLAS_WIKI_VAULT_TOKEN_ID", New: "QATLAS_WIKI_CRED_TOKEN_ID"}) {
		t.Errorf("document = %+v, %+v", doc, doc.Credential)
	}
	code, _, stderr = runWithInput(t, &Options{}, "", "config", "rename", "credential", "wiki-vault", "wiki-cred",
		"--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, "admin-required") {
		t.Errorf("without a terminal: exit %d, stderr %q, want admin-required", code, stderr)
	}

	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr = runWithInput(t, &Options{}, "", "config", "rename", "credential", "wiki-vault", "wiki-cred",
		"--dry-run", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "  reader    wiki2: approved; carried over") ||
		!strings.Contains(stdout, "  vault     entry wiki-vault -> wiki-cred") ||
		!strings.Contains(stdout, "dry run: nothing was changed") {
		t.Errorf("dry run at a terminal: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if after := treeIn(t, dir); !reflect.DeepEqual(after, before) {
		t.Error("a dry run or a refused rename changed a file")
	}
}

// stopCredentialRename leaves the vault in dir the way a rename of wiki-vault to wiki-cred that stopped before
// the configuration does: the entry and the approvals of both connections under the new name.
func stopCredentialRename(t *testing.T, dir string) {
	t.Helper()
	cfg := loadConfig(t, dir)
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	var carry []vault.ScopeRename
	for _, name := range []string{"wiki", "wiki2"} {
		scope := connectionScope(t, dir, name)
		renamed := scope
		renamed.Credential = "wiki-cred"
		carry = append(carry, vault.ScopeRename{Before: scope, After: renamed, ConnectionID: cfg.Connections[name].ID})
	}
	if _, _, err := v.RenameCredential(vault.CredentialRename{From: "wiki-vault", To: "wiki-cred",
		CredentialID: cfg.Credentials["wiki-vault"].ID, Entry: true, Carry: carry}); err != nil {
		t.Fatal(err)
	}
}

func TestVaultApproveNamesACredentialLeftover(t *testing.T) {
	dir := renameVault(t)
	stopCredentialRename(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "the vault holds credential wiki-vault under the name wiki-cred, "+
		"with the approvals of wiki, wiki2, left over from an unfinished rename; until it is resolved, wiki-vault is "+
		"not read from the vault: 'qatlas config rename credential wiki-vault wiki-cred' finishes the rename, and "+
		"'qatlas config rename credential wiki-vault wiki-cred --revert' puts it back under wiki-vault\n") ||
		!strings.Contains(stdout, "nothing is open") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "approve", "--output", "json", "--config", configIn(dir))
	var doc vaultApproveDocument
	if code != exitOK || json.Unmarshal([]byte(stdout), &doc) != nil {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	want := []renameLeftoverDocument{{Kind: "credential", Credential: "wiki-vault", New: "wiki-cred",
		Approvals: []string{"wiki", "wiki2"}}}
	if !reflect.DeepEqual(doc.RenameLeftovers, want) || len(doc.Approved) != 0 {
		t.Errorf("document = %+v", doc)
	}
}

// The same rename finishes what a stopped one left; --revert puts it back instead. Either way the connections
// are approved as they are configured then, without a new approval.
func TestConfigRenameCredentialResolvesALeftover(t *testing.T) {
	for _, revert := range []bool{false, true} {
		dir := renameVault(t)
		stopCredentialRename(t, dir)
		configBefore, _ := os.ReadFile(configIn(dir))
		withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
		withVaultConfirm(t, confirming(true))
		args := []string{"config", "rename", "credential", "wiki-vault", "wiki-cred", "--config", configIn(dir)}
		want := []string{"  resumes   a rename to wiki-cred that stopped", "  reader    wiki: approved; carried over",
			"approvals carried over to wiki-cred: wiki, wiki2\n", "renamed credential wiki-vault to wiki-cred\n"}
		if revert {
			args = append(args, "--revert")
			want = []string{"revert the stopped rename of credential wiki-vault -> wiki-cred\n",
				"  vault     entry wiki-cred -> wiki-vault", "  reader    wiki: approval carried back to wiki-vault\n",
				"  config    not changed\n", "approvals carried back to wiki-vault: wiki, wiki2\n",
				"put credential wiki-vault back as it was before the rename to wiki-cred\n"}
		}
		code, stdout, stderr := runWithInput(t, &Options{}, "", args...)
		if code != exitOK || stderr != "" {
			t.Fatalf("revert %v: exit %d, stdout %q, stderr %q", revert, code, stdout, stderr)
		}
		for _, w := range want {
			if !strings.Contains(stdout, w) {
				t.Errorf("revert %v: stdout = %q, want it to contain %q", revert, stdout, w)
			}
		}
		configAfter, _ := os.ReadFile(configIn(dir))
		if changed := string(configAfter) != string(configBefore); changed == revert {
			t.Errorf("revert %v: configuration changed = %v", revert, changed)
		}
		fresh := vault.New(dir)
		if _, err := fresh.Unlock("s3cret-phrase"); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(t, dir)
		if report, err := approval.Pending(cfg, fresh); err != nil || len(report.Open) != 0 || len(report.Stale) != 0 {
			t.Errorf("revert %v: Pending() = %+v, %v, want nothing open or stale", revert, report, err)
		}
		for _, name := range []string{"wiki", "wiki2"} {
			if err := fresh.CheckApproval(connectionScope(t, dir, name)); err != nil {
				t.Errorf("revert %v: CheckApproval(%s) = %v", revert, name, err)
			}
		}
	}
}
