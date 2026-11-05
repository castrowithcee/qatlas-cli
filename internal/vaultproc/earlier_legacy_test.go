//go:build legacy_vault_names && (linux || darwin || windows)

package vaultproc

import (
	"context"
	"path/filepath"
	"testing"
)

// A build that stands in for the earlier release reaches its own vault process by the earlier names, so it
// finds no earlier process beside it, and ends none.
func TestABuildOfTheEarlierNamesFindsNoEarlierProcess(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))
	vaultDir := filepath.Join(t.TempDir(), "vault")
	if path := earlierPathOf(vaultDir); path != "" {
		t.Fatalf("earlierPathOf() = %q, want none", path)
	}
	if pid, err := EndEarlier(context.Background(), vaultDir); pid != 0 || err != nil {
		t.Fatalf("EndEarlier() = %d, %v, want none", pid, err)
	}
}
