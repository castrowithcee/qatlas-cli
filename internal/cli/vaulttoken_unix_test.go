//go:build linux || darwin

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Without a terminal, 'vault approve' hands the agent token to the vault process: it approves the change
// the token's vorbild covers, which reaches the running process at once, leaves the wider one open and
// fails with admin-required naming it and the field, and logs each decision with the token's name. The
// token's value appears nowhere.
func TestVaultApproveWithAnAgentToken(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	value := createAgentToken(t, dir, "kunde-a", "wiki")
	server, client := serveVaultInProcess(t, dir)
	server.ApproveWithTokens(filepath.Join(dir, vault.DirName))
	server.KeepLog(filepath.Join(dir, vault.DirName), 90)

	config, _ := os.ReadFile(configIn(dir))
	extended := strings.Replace(string(config), "defaults:", "  wiki-agent:\n    service: wiki\n"+
		"    credential: wiki-vault\n  wiki-wide:\n    service: wiki\n    credential: wiki-vault\n"+
		"    permissions: [read, create]\ndefaults:", 1)
	if err := os.WriteFile(configIn(dir), []byte(extended), 0o600); err != nil {
		t.Fatal(err)
	}
	withInteractive(t, false)
	original := lookupAgentToken
	lookupAgentToken = func() (string, string, error) { return value, vault.AgentTokenEnv, nil }
	t.Cleanup(func() { lookupAgentToken = original })

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitUsage {
		t.Fatalf("approve: exit %d, stdout %q, stderr %q, want usage for the open change", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "approved 1 connection change with agent token kunde-a: wiki-agent (create)") {
		t.Errorf("approve: stdout %q, want the covered change approved", stdout)
	}
	if !strings.Contains(stderr, "admin-required: agent token kunde-a does not cover 1 open change, which stays "+
		"open: wiki-wide (create): permissions") {
		t.Errorf("approve: stderr %q, want the open change and its field named", stderr)
	}
	if strings.Contains(stdout+stderr, value) {
		t.Fatalf("approve printed the token's value")
	}

	ctx := context.Background()
	if err := client.Check(ctx, connectionScope(t, dir, "wiki-agent")); err != nil {
		t.Errorf("Check(wiki-agent) = %v, want it approved in the running process", err)
	}
	if err := client.Check(ctx, connectionScope(t, dir, "wiki-wide")); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("Check(wiki-wide) = %v, want it still open", err)
	}

	entries, lines := logEntries(t, dir)
	results := map[string]string{}
	for _, entry := range entries {
		if entry.Token == "kunde-a" && entry.Operation == "vault.approve" {
			results[entry.Connection] = entry.Result
		}
	}
	if results["wiki-agent"] != "success" || results["wiki-wide"] != "admin-required" {
		t.Errorf("log = %v, want both decisions with the token's name", results)
	}
	for _, line := range lines {
		if strings.Contains(string(line), value) {
			t.Fatalf("the log holds the token's value")
		}
	}

	// --connection limits the approval; a name that reads no vault credential is refused first.
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "approve", "--connection", "wiki-agent",
		"--config", configIn(dir))
	if code != exitOK || stdout != "wiki-agent has no open change\n" {
		t.Errorf("approve --connection wiki-agent: exit %d, stdout %q", code, stdout)
	}
	if code, _, _ := runWithInput(t, &Options{}, "", "vault", "approve", "--connection", "unknown",
		"--config", configIn(dir)); code != exitUsage {
		t.Errorf("approve --connection unknown: exit %d, want usage", code)
	}

	// A revoked token stops at once, in the running process too.
	withInteractive(t, true)
	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "token", "revoke", "kunde-a", "--config",
		configIn(dir)); code != exitOK {
		t.Fatalf("revoke: exit %d, stderr %q", code, stderr)
	}
	withInteractive(t, false)
	code, _, stderr = runWithInput(t, &Options{}, "", "vault", "approve", "--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, "admin-required: the agent token from QATLAS_AGENT_TOKEN is refused") {
		t.Errorf("approve with a revoked token: exit %d, stderr %q, want admin-required", code, stderr)
	}
}
