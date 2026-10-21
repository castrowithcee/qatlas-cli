//go:build linux || darwin

package cli

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// A vault process that holds the vault unlocked tells validate its names, so the hand rename is found
// without a passphrase; a process that cannot tell them leaves the vault unchecked.
func TestConfigValidateReadsTheNamesFromAVaultProcess(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	serveVaultInProcessWith(t, dir, func(s *vaultproc.Server) { s.ApproveWithTokens(vault.New(dir).Dir()) })

	code, stdout, stderr := validateNotes(t, dir)
	if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid: ") ||
		!strings.Contains(stderr, "qatlas: note: connection wiki was renamed to wiki-main by hand") ||
		!strings.Contains(stderr, "'qatlas config rename connection wiki wiki-main --adopt'") ||
		strings.Contains(stderr, "not checked") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestConfigValidateWithAProcessThatCannotTellTheNames(t *testing.T) {
	dir := renameVault(t)
	handRenameConnection(t, dir)
	serveVaultInProcess(t, dir)

	code, _, stderr := validateNotes(t, dir)
	if code != exitOK || strings.Count(stderr, "was not checked for renames made by hand") != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
