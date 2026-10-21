package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// handRenameConnection renames connection wiki to wiki-main in the configuration of dir by hand: the key
// changes, the id and the vault stay.
func handRenameConnection(t *testing.T, dir string) {
	t.Helper()
	cfg := loadConfig(t, dir)
	if _, err := cfg.RenameConnection("wiki", "wiki-main"); err != nil {
		t.Fatal(err)
	}
	if err := config.NewStore(configIn(dir), sharedRegistry()).Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// handRenameCredential records the config id in the vault entry wiki-vault, as qatlas does, then renames the
// credential to wiki-cred in the configuration by hand.
func handRenameCredential(t *testing.T, dir string, v *vault.Vault) {
	t.Helper()
	cfg := loadConfig(t, dir)
	if _, _, err := v.RecordCredentialIDs(map[string]string{"wiki-vault": cfg.Credentials["wiki-vault"].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.RenameCredential("wiki-vault", "wiki-cred"); err != nil {
		t.Fatal(err)
	}
	if err := config.NewStore(configIn(dir), sharedRegistry()).Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func approvalNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	approvals, err := v.Approvals()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for name := range approvals {
		names[name] = true
	}
	return names
}

func TestConfigRenameAdoptConnection(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	hand := treeIn(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--adopt", "--dry-run", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("dry run: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"adopt the rename of connection wiki -> wiki-main, made by hand",
		"  config    connections.wiki-main is there already; written as it is\n",
		"  approval  approved; carried over, so wiki-main stays approved\n", "dry run: nothing was changed\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry run stdout lacks %q:\n%s", want, stdout)
		}
	}
	if after := treeIn(t, dir); len(after) != len(hand) || after["config.yaml"] != hand["config.yaml"] {
		t.Errorf("a dry run changed the files")
	}
	if names := approvalNames(t, dir); !names["wiki"] || names["wiki-main"] {
		t.Errorf("approvals after a dry run = %v", names)
	}

	var question string
	withVaultConfirm(t, func(prompt string) (bool, error) { question = prompt; return true, nil })
	code, stdout, stderr = runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--adopt", "--config", configIn(dir))
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "adopted the rename of connection wiki to wiki-main\n") {
		t.Fatalf("adopt: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.HasPrefix(question, "adopt the rename of connection wiki to wiki-main") {
		t.Errorf("question = %q", question)
	}
	if names := approvalNames(t, dir); names["wiki"] || !names["wiki-main"] {
		t.Errorf("approvals after adopting = %v, want wiki-main only", names)
	}
	if cfg := loadConfig(t, dir); cfg.Connections["wiki-main"].ID == "" || cfg.Defaults.Connections["knowledge"] != "wiki-main" {
		t.Errorf("configuration = %+v", cfg)
	}
}

func TestConfigRenameAdoptCredential(t *testing.T) {
	dir := renameVault(t)
	handRenameCredential(t, dir, vault.New(dir))
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	withVaultConfirm(t, confirming(true))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "credential", "wiki-vault",
		"wiki-cred", "--adopt", "--config", configIn(dir))
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "adopted the rename of credential wiki-vault to wiki-cred\n") ||
		!strings.Contains(stdout, "  vault     entry wiki-vault -> wiki-cred") {
		t.Fatalf("adopt: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if _, held, _ := v.CredentialID("wiki-cred"); !held {
		t.Error("the vault holds no entry wiki-cred after adopting")
	}
	if _, held, _ := v.CredentialID("wiki-vault"); held {
		t.Error("the vault still holds the entry under the old name")
	}
}

func TestConfigRenameAdoptRefusals(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	base := []string{"config", "rename"}
	run := func(args ...string) (int, string, string) {
		withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
		return runWithInput(t, &Options{}, "", append(append(base, args...), "--config", configIn(dir))...)
	}

	code, stdout, stderr := run("credential", "wiki-vault", "wiki-cred", "--adopt", "--revert")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "--adopt and --revert cannot be combined") {
		t.Errorf("adopt and revert: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = run("service", "wiki", "wiki2", "--adopt")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "needs nothing adopted") {
		t.Errorf("service: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	// The old name is still configured: nothing to adopt.
	code, _, stderr = run("connection", "wiki2", "wiki-main", "--adopt")
	if code != exitUsage || !strings.Contains(stderr, "still configured") {
		t.Errorf("old still configured: exit %d, stderr %q", code, stderr)
	}

	withInteractive(t, false)
	code, stdout, stderr = run("connection", "wiki", "wiki-main", "--adopt")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
		t.Errorf("no terminal: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestConfigRenameAdoptJSON(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki", "wiki-main",
		"--adopt", "--dry-run", "--output", "json", "--config", configIn(dir))
	var doc struct {
		Adopt  bool   `json:"adopt"`
		DryRun bool   `json:"dry_run"`
		New    string `json:"new"`
	}
	if code != exitOK || json.Unmarshal([]byte(stdout), &doc) != nil || !doc.Adopt || !doc.DryRun || doc.New != "wiki-main" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	// A plain rename reports adopt false.
	code, stdout, _ = runWithInput(t, &Options{}, "", "config", "rename", "connection", "wiki2", "wiki3",
		"--dry-run", "--output", "json", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, `"adopt":false`) && !strings.Contains(stdout, `"adopt": false`) {
		t.Errorf("plain rename: exit %d, stdout %q", code, stdout)
	}
}

func TestVaultApproveNamesHandRenames(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--output", "json", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	var doc vaultApproveDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.HandRenames) != 1 || doc.HandRenames[0].Kind != "connection" || doc.HandRenames[0].Old != "wiki" ||
		doc.HandRenames[0].Command != "qatlas config rename connection wiki wiki-main --adopt" {
		t.Errorf("hand_renames = %+v", doc.HandRenames)
	}
	if len(doc.RenameLeftovers) != 0 || len(doc.RemovedStale) != 0 {
		t.Errorf("leftovers = %+v, removed = %v, want none", doc.RenameLeftovers, doc.RemovedStale)
	}
	if !approvalNames(t, dir)["wiki"] {
		t.Error("the approval of a hand rename was removed")
	}
}

func TestVaultApproveNamesAHandRenamedCredential(t *testing.T) {
	dir := renameVault(t)
	handRenameCredential(t, dir, vault.New(dir))
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "credential wiki-vault was renamed to wiki-cred by hand") ||
		!strings.Contains(stdout, "'qatlas config rename credential wiki-vault wiki-cred --adopt'") ||
		strings.Contains(stdout, "left over") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestVaultApproveHandRenameWithoutASuggestion(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	// A second approval records the same connection id: neither can be told to be the renamed one.
	ghost := connectionScope(t, dir, "wiki2")
	ghost.Connection = "ghost"
	id := loadConfig(t, dir).Connections["wiki-main"].ID
	if err := v.ApproveConnections([]vault.Scope{ghost}, map[string]string{"ghost": id}); err != nil {
		t.Fatal(err)
	}
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "could belong to more than one") || strings.Contains(stdout, "--adopt") {
		t.Errorf("stdout = %q, want an ambiguity without a command", stdout)
	}
	if names := approvalNames(t, dir); !names["wiki"] || !names["ghost"] {
		t.Errorf("approvals = %v, want the ambiguous ones kept", names)
	}
}

// validateNotes runs 'config validate' and fails if it asks for a passphrase.
func validateNotes(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	withVaultPassphrase(t, func(string) (string, error) {
		t.Error("validate asked for a passphrase")
		return "", vault.ErrNoTerminal
	})
	return runWithInput(t, &Options{}, "", append([]string{"config", "validate", "--config", configIn(dir)}, args...)...)
}

func TestConfigValidateNotesHandRenamesOfAnUnencryptedVault(t *testing.T) {
	dir := twoConnectionVaultFixture(t)
	if err := config.NewStore(configIn(dir), sharedRegistry()).Save(loadConfig(t, dir)); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	handRenameCredential(t, dir, v)

	code, stdout, stderr := validateNotes(t, dir)
	if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid: ") ||
		!strings.Contains(stderr, "qatlas: note: credential wiki-vault was renamed to wiki-cred by hand") ||
		!strings.Contains(stderr, "'qatlas config rename credential wiki-vault wiki-cred --adopt'") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = validateNotes(t, dir, "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"hand_renames"`) || !strings.Contains(stdout, "wiki-cred") ||
		strings.Contains(stdout, canaryVault) {
		t.Errorf("json: exit %d, stdout %q", code, stdout)
	}
}

func TestConfigValidateSaysAnEncryptedLockedVaultWasNotChecked(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	code, stdout, stderr := validateNotes(t, dir)
	if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid: ") ||
		strings.Count(stderr, "was not checked for renames made by hand") != 1 || strings.Contains(stderr, "--adopt") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = validateNotes(t, dir, "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"vault_checked":false`) && !strings.Contains(stdout, `"vault_checked": false`) {
		t.Errorf("json: exit %d, stdout %q", code, stdout)
	}
}

func TestConfigValidateWithoutAVaultSaysNothingOfIt(t *testing.T) {
	dir := twoConnectionVaultFixture(t)
	code, _, stderr := validateNotes(t, dir)
	if code != exitOK || strings.Contains(stderr, "vault") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}
