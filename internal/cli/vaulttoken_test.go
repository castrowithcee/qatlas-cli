package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// encryptedTokenFixture writes the vault fixture's configuration and an encrypted vault holding wiki-vault,
// with the connection wiki approved to read it.
func encryptedTokenFixture(t *testing.T) string {
	t.Helper()
	dir := vaultCredentialFixture(t)
	v := seedEncryptedVault(t, dir, "wiki-vault", "token-id", canaryVault, "s3cret-phrase")
	approveConnections(t, dir, v)
	return dir
}

// createAgentToken runs 'vault token create' with the passphrase and returns the token's value.
func createAgentToken(t *testing.T, dir, name string, models ...string) string {
	t.Helper()
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	args := []string{"vault", "token", "create", name, "--config", configIn(dir), "--output", "json"}
	for _, model := range models {
		args = append(args, "--model", model)
	}
	code, stdout, stderr := runWithInput(t, &Options{}, "", args...)
	if code != exitOK {
		t.Fatalf("create: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	var shown struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil || shown.Name != name ||
		!strings.HasPrefix(shown.Token, vault.TokenPrefix) {
		t.Fatalf("create: stdout %q, %v, want the token and its value", stdout, err)
	}
	return shown.Token
}

// 'vault token' creates, lists, shows, and revokes tokens with the passphrase; only create and show print a
// value, and list warns about a model that covers nothing now.
func TestVaultTokenCommands(t *testing.T) {
	dir := encryptedTokenFixture(t)
	value := createAgentToken(t, dir, "kunde-a", "wiki")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		return runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir), "--output", "json")...)
	}

	code, stdout, stderr := run("vault", "token", "list")
	if code != exitOK || !strings.Contains(stdout, `"name":"kunde-a"`) || !strings.Contains(stdout, `"models":"wiki"`) ||
		!strings.Contains(stdout, `"expires":"never"`) || strings.Contains(stdout, value) || stderr != "" {
		t.Fatalf("list: exit %d, stdout %q, stderr %q, want the token without its value", code, stdout, stderr)
	}
	code, stdout, _ = run("vault", "token", "show", "kunde-a")
	if code != exitOK || !strings.Contains(stdout, value) {
		t.Fatalf("show: exit %d, stdout %q, want the value", code, stdout)
	}

	for _, args := range [][]string{
		{"vault", "token", "create", "kunde-a", "--model", "wiki"},
		{"vault", "token", "create", "kunde-b", "--model", "unknown"},
		{"vault", "token", "create", "kunde-b"},
		{"vault", "token", "create", "kunde b", "--model", "wiki"},
		{"vault", "token", "create", "kunde-b", "--model", "wiki", "--expires", "2001-01-01"},
		{"vault", "token", "show", "unknown"},
	} {
		if code, _, stderr := run(args...); code != exitUsage || !strings.Contains(stderr, "usage") {
			t.Errorf("%v: exit %d, stderr %q, want usage", args, code, stderr)
		}
	}

	// Renaming the model leaves the token without it; list says so.
	config, _ := os.ReadFile(configIn(dir))
	renamed := strings.Replace(string(config), "  wiki:\n    service: wiki", "  wiki-renamed:\n    service: wiki", 1)
	renamed = strings.Replace(renamed, "knowledge: wiki", "knowledge: wiki-renamed", 1)
	if err := os.WriteFile(configIn(dir), []byte(renamed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run("vault", "token", "list"); !strings.Contains(stdout, "model wiki covers nothing now") {
		t.Errorf("list after renaming the model = %q, want the warning", stdout)
	}

	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "token", "revoke", "kunde-a", "--yes", "--config", configIn(dir))
	if code != exitOK || stdout != "revoked agent token kunde-a\n" {
		t.Fatalf("revoke: exit %d, stdout %q", code, stdout)
	}
	if code, _, _ := run("vault", "token", "revoke", "kunde-a", "--yes"); code != exitUsage {
		t.Errorf("revoke twice: exit %d, want usage", code)
	}

	withVaultPassphrase(t, offeringPassphrase("wrong"))
	if code, _, stderr := run("vault", "token", "list"); code != exitUsage || strings.Contains(stderr, "kunde") {
		t.Errorf("list with a wrong passphrase: exit %d, stderr %q, want usage", code, stderr)
	}
}

// Managing tokens needs a person at a terminal and an encrypted vault.
func TestVaultTokenCommandsNeedAdminAndEncryption(t *testing.T) {
	dir := encryptedTokenFixture(t)
	withInteractive(t, false)
	for _, args := range [][]string{
		{"vault", "token", "list"},
		{"vault", "token", "create", "kunde-a", "--model", "wiki"},
		{"vault", "token", "show", "kunde-a"},
		{"vault", "token", "revoke", "kunde-a"},
	} {
		code, stdout, stderr := runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir))...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
			t.Errorf("%v without a terminal: exit %d, stdout %q, stderr %q, want admin-required", args, code, stdout,
				stderr)
		}
	}

	plain := vaultCredentialFixture(t)
	if err := vault.New(plain).Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	withInteractive(t, true)
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "token", "create", "kunde-a", "--model", "wiki",
		"--config", configIn(plain))
	if code != exitUsage || !strings.Contains(stderr, "agent tokens need an encrypted vault") {
		t.Errorf("create on an unencrypted vault: exit %d, stderr %q, want the encryption named", code, stderr)
	}
}

// Without a terminal 'vault approve' needs an agent token: none found, or a vault without encryption, fails
// with admin-required, and a locked vault without a vault process with vault-locked.
func TestVaultApproveWithoutTerminalNeedsAToken(t *testing.T) {
	dir := encryptedTokenFixture(t)
	value := createAgentToken(t, dir, "kunde-a", "wiki")
	withInteractive(t, false)
	approve := func(dir string) (int, string, string) {
		t.Helper()
		return runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	}

	code, stdout, stderr := approve(dir)
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required: no agent token was found") {
		t.Errorf("without a token: exit %d, stdout %q, stderr %q, want admin-required", code, stdout, stderr)
	}

	original := lookupAgentToken
	lookupAgentToken = func() (string, string, error) { return value, vault.AgentTokenEnv, nil }
	t.Cleanup(func() { lookupAgentToken = original })
	code, _, stderr = approve(dir)
	if code != exitRuntime || !strings.Contains(stderr, "vault-locked") || strings.Contains(stderr, value) {
		t.Errorf("with a locked vault: exit %d, stderr %q, want vault-locked", code, stderr)
	}

	plain := vaultCredentialFixture(t)
	if err := vault.New(plain).Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = approve(plain)
	if code != exitUsage || !strings.Contains(stderr, "admin-required: agent tokens need an encrypted vault") {
		t.Errorf("with an unencrypted vault: exit %d, stderr %q, want admin-required", code, stderr)
	}
}

// 'vault handover' shows and sets the update behaviour with the passphrase, needs a terminal and an
// encrypted vault, and leaves config.yaml alone.
func TestVaultHandoverCommand(t *testing.T) {
	dir := encryptedTokenFixture(t)
	configBefore, err := os.ReadFile(configIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	run := func(args ...string) (int, string, string) {
		return runWithInput(t, &Options{}, "", append([]string{"vault", "handover"}, append(args, "--config", configIn(dir))...)...)
	}

	if code, stdout, stderr := run(); code != exitOK || stdout != "update behaviour: handover\n" {
		t.Errorf("show: exit %d, stdout %q, stderr %q, want the default", code, stdout, stderr)
	}
	if code, stdout, stderr := run("lock"); code != exitOK || stdout != "update behaviour: lock\n" {
		t.Errorf("set lock: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, stdout, _ := run(); code != exitOK || stdout != "update behaviour: lock\n" {
		t.Errorf("show after set: exit %d, stdout %q, want lock", code, stdout)
	}
	if code, _, stderr := run("sometimes"); code != exitUsage || !strings.Contains(stderr, "usage") {
		t.Errorf("unknown value: exit %d, stderr %q, want usage", code, stderr)
	}
	if code, stdout, _ := run("handover"); code != exitOK || stdout != "update behaviour: handover\n" {
		t.Errorf("set handover: exit %d, stdout %q", code, stdout)
	}
	if after, _ := os.ReadFile(configIn(dir)); string(after) != string(configBefore) {
		t.Error("config.yaml changed")
	}
}

func TestVaultHandoverCommandNeedsAdminAndEncryption(t *testing.T) {
	dir := encryptedTokenFixture(t)
	settings := filepath.Join(dir, vault.DirName, "settings.age")

	withInteractive(t, false)
	for _, args := range [][]string{{"vault", "handover"}, {"vault", "handover", "lock"}} {
		code, stdout, stderr := runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir))...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
			t.Errorf("%v without a terminal: exit %d, stdout %q, stderr %q, want admin-required", args, code, stdout,
				stderr)
		}
	}

	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("wrong"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "handover", "lock", "--config", configIn(dir))
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "usage") {
		t.Errorf("wrong passphrase: exit %d, stdout %q, stderr %q, want usage", code, stdout, stderr)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("a refused call wrote settings.age: %v", err)
	}

	plain := vaultCredentialFixture(t)
	if err := vault.New(plain).Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runWithInput(t, &Options{}, "", "vault", "handover", "lock", "--config", configIn(plain))
	if code != exitUsage || !strings.Contains(stderr, "run 'qatlas vault encrypt' first") {
		t.Errorf("unencrypted vault: exit %d, stderr %q, want the encryption named", code, stderr)
	}
}

// Revoking asks first: y revokes, n keeps, --yes skips the question, and no terminal stays admin-required.
func TestVaultTokenRevokeConfirmation(t *testing.T) {
	dir := encryptedTokenFixture(t)
	createAgentToken(t, dir, "kunde-a", "wiki")
	revoke := func(extra ...string) (int, string, string) {
		t.Helper()
		withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
		args := append([]string{"vault", "token", "revoke", "kunde-a", "--config", configIn(dir)}, extra...)
		return runWithInput(t, &Options{}, "", args...)
	}
	listed := func() bool {
		t.Helper()
		withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
		_, stdout, _ := runWithInput(t, &Options{}, "", "vault", "token", "list", "--config", configIn(dir))
		return strings.Contains(stdout, "kunde-a")
	}

	var asked string
	withVaultConfirm(t, func(q string) (bool, error) { asked = q; return false, nil })
	code, stdout, _ := revoke()
	if code != exitOK || !strings.Contains(stdout, "kept") || !strings.Contains(asked, "kunde-a") || !listed() {
		t.Errorf("n: exit %d, stdout %q, asked %q, listed %v, want kept", code, stdout, asked, listed())
	}

	withVaultConfirm(t, func(q string) (bool, error) { return true, nil })
	if code, _, stderr := revoke(); code != exitOK || listed() {
		t.Fatalf("y: exit %d, stderr %q, want the token revoked", code, stderr)
	}

	createAgentToken(t, dir, "kunde-a", "wiki")
	asked = ""
	withVaultConfirm(t, func(q string) (bool, error) { asked = q; return false, nil })
	if code, _, _ := revoke("--yes"); code != exitOK || asked != "" || listed() {
		t.Errorf("--yes: exit %d, asked %q, want revoked without a question", code, asked)
	}

	createAgentToken(t, dir, "kunde-a", "wiki")
	withInteractive(t, false)
	for _, extra := range [][]string{nil, {"--yes"}} {
		code, _, stderr := revoke(extra...)
		if code != exitUsage || !strings.Contains(stderr, "admin-required") {
			t.Errorf("no terminal %v: exit %d, stderr %q, want admin-required", extra, code, stderr)
		}
	}
	withInteractive(t, true)
	if !listed() {
		t.Error("no terminal: the token is gone")
	}
}

// --model and the hidden --vorbild both name a token's models, may be mixed, and output carries models.
func TestVaultTokenModelFlagAndHiddenAlias(t *testing.T) {
	dir := encryptedTokenFixture(t)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	run := func(args ...string) (int, string, string) {
		t.Helper()
		return runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir))...)
	}

	for _, flag := range []string{"--model", "--vorbild"} {
		name := map[string]string{"--model": "via-new", "--vorbild": "via-old"}[flag]
		code, stdout, stderr := run("vault", "token", "create", name, flag, "wiki")
		if code != exitOK || !strings.Contains(stdout, "models") || strings.Contains(stdout, "vorbild") {
			t.Fatalf("create %s: exit %d, stdout %q, stderr %q, want models", flag, code, stdout, stderr)
		}
	}
	code, stdout, _ := run("vault", "token", "create", "mixed", "--model", "wiki", "--vorbild", "wiki")
	if code != exitOK || !strings.Contains(stdout, "wiki") {
		t.Errorf("mixed flags: exit %d, stdout %q, want both flags to count", code, stdout)
	}

	for _, args := range [][]string{{"vault", "token", "list"}, {"vault", "token", "show", "via-new"}} {
		for _, format := range []string{"table", "json"} {
			code, stdout, _ := run(append(args, "--output", format)...)
			if code != exitOK || !strings.Contains(strings.ToLower(stdout), "models") || strings.Contains(strings.ToLower(stdout), "vorbild") {
				t.Errorf("%v --output %s: exit %d, stdout %q, want models and no vorbilder", args, format, code, stdout)
			}
		}
	}

	_, help, _ := run("vault", "token", "create", "--help")
	if !strings.Contains(help, "--model") || strings.Contains(help, "vorbild") {
		t.Errorf("create help = %q, want --model and no vorbild", help)
	}
}
