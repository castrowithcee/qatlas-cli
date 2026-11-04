package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

// treeContents maps every file below dir to its content.
func treeContents(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An installation of the version before the rename, with an encrypted vault, an approved connection, an
// agent token, and an invocation log, starts from the default location: its first run copies the directory
// to ~/.qatlas/connect, and everything works there without a new approval while the legacy directory stays
// as it was.
func TestHomeMigrationKeepsVaultApprovalsTokensAndLog(t *testing.T) {
	built := vaultCredentialFixture(t)
	v := seedEncryptedVault(t, built, "wiki-vault", "token-id", canaryVault, "s3cret-phrase")
	if err := v.Set("wiki-vault", "token-secret", "second-"+canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	approveConnections(t, built, v)
	createAgentToken(t, built, "kunde-a", "wiki")
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "logs", "verify", "--config", configIn(built)); code != exitOK {
		t.Fatalf("the fixture's log does not verify: %s", stderr)
	}

	home := t.TempDir()
	legacy := filepath.Join(home, ".qatlas", "cli")
	if err := copyTree(built, legacy); err != nil {
		t.Fatal(err)
	}
	before := treeContents(t, legacy)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("QCONNECT_CONFIG", "")
	t.Setenv("QCONNECT_HOME", "")
	t.Setenv(secret.StoreSelector, secret.StoreNone)

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "token", "list", "--output", "json")

	target := filepath.Join(home, ".qatlas", "connect")
	if code != exitOK || !strings.Contains(stdout, `"name":"kunde-a"`) {
		t.Fatalf("token list after the migration: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "copied the configuration from "+legacy+" to "+target) {
		t.Errorf("stderr = %q, want the migration named once", stderr)
	}

	// The approval holds in the copy: the invoke passes the vault and fails only at the unreachable host.
	secrets := secret.NewWith(func(string) string { return "" }, nil, nil, &redact.Redactor{}).
		WithVault(vault.New(target), offeringPassphrase("s3cret-phrase"))
	code, _, stderr = runWithInput(t, &Options{Secrets: secrets}, "", "invoke", "bookstack.pages.list")
	if code == exitOK || strings.Contains(stderr, "approval-required") || strings.Contains(stderr, "no secret") ||
		strings.Contains(stderr, "vault-locked") || strings.Contains(stderr, canaryVault) {
		t.Errorf("invoke after the migration: exit %d, stderr %q, want it past the vault", code, stderr)
	}
	if strings.Contains(stderr, "copied the configuration") {
		t.Errorf("a second run migrated again: %q", stderr)
	}
	code, _, stderr = runWithInput(t, &Options{}, "", "vault", "logs", "verify")
	if code != exitOK {
		t.Errorf("vault logs verify in the migrated directory: exit %d, stderr %q", code, stderr)
	}

	value, found, _, err := vault.New(target).Get("wiki-vault", "token-id", offeringPassphrase("s3cret-phrase"))
	if err != nil || !found || value != canaryVault {
		t.Errorf("migrated secret: found %v, %v, want the stored value", found, err)
	}

	after := treeContents(t, legacy)
	if len(after) != len(before) {
		t.Errorf("the legacy directory has %d files, want %d", len(after), len(before))
	}
	for rel, content := range before {
		if after[rel] != content {
			t.Errorf("legacy %s changed", rel)
		}
	}
}
