package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// encryptedTokenFixture writes the vault fixture's configuration and an encrypted vault holding wiki-vault,
// with the connection wiki approved to read it.
func encryptedTokenFixture(t *testing.T) string {
	t.Helper()
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	approveConnections(t, dir, v)
	return dir
}

// createAgentToken runs 'vault token create' with the passphrase and returns the token's value.
func createAgentToken(t *testing.T, dir, name string, vorbilder ...string) string {
	t.Helper()
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	args := []string{"vault", "token", "create", name, "--config", configIn(dir), "--output", "json"}
	for _, vorbild := range vorbilder {
		args = append(args, "--vorbild", vorbild)
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
// value, and list warns about a vorbild that covers nothing now.
func TestVaultTokenCommands(t *testing.T) {
	dir := encryptedTokenFixture(t)
	value := createAgentToken(t, dir, "kunde-a", "wiki")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		return runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir), "--output", "json")...)
	}

	code, stdout, stderr := run("vault", "token", "list")
	if code != exitOK || !strings.Contains(stdout, `"name":"kunde-a"`) || !strings.Contains(stdout, `"vorbilder":"wiki"`) ||
		!strings.Contains(stdout, `"expires":"never"`) || strings.Contains(stdout, value) || stderr != "" {
		t.Fatalf("list: exit %d, stdout %q, stderr %q, want the token without its value", code, stdout, stderr)
	}
	code, stdout, _ = run("vault", "token", "show", "kunde-a")
	if code != exitOK || !strings.Contains(stdout, value) {
		t.Fatalf("show: exit %d, stdout %q, want the value", code, stdout)
	}

	for _, args := range [][]string{
		{"vault", "token", "create", "kunde-a", "--vorbild", "wiki"},
		{"vault", "token", "create", "kunde-b", "--vorbild", "unknown"},
		{"vault", "token", "create", "kunde-b"},
		{"vault", "token", "create", "kunde b", "--vorbild", "wiki"},
		{"vault", "token", "create", "kunde-b", "--vorbild", "wiki", "--expires", "2001-01-01"},
		{"vault", "token", "show", "unknown"},
	} {
		if code, _, stderr := run(args...); code != exitUsage || !strings.Contains(stderr, "usage") {
			t.Errorf("%v: exit %d, stderr %q, want usage", args, code, stderr)
		}
	}

	// Renaming the vorbild leaves the token without it; list says so.
	config, _ := os.ReadFile(configIn(dir))
	renamed := strings.Replace(string(config), "  wiki:\n    service: wiki", "  wiki-renamed:\n    service: wiki", 1)
	renamed = strings.Replace(renamed, "knowledge: wiki", "knowledge: wiki-renamed", 1)
	if err := os.WriteFile(configIn(dir), []byte(renamed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run("vault", "token", "list"); !strings.Contains(stdout, "vorbild wiki covers nothing now") {
		t.Errorf("list after renaming the vorbild = %q, want the warning", stdout)
	}

	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "token", "revoke", "kunde-a", "--config", configIn(dir))
	if code != exitOK || stdout != "revoked agent token kunde-a\n" {
		t.Fatalf("revoke: exit %d, stdout %q", code, stdout)
	}
	if code, _, _ := run("vault", "token", "revoke", "kunde-a"); code != exitUsage {
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
		{"vault", "token", "create", "kunde-a", "--vorbild", "wiki"},
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
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "token", "create", "kunde-a", "--vorbild", "wiki",
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
