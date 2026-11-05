//go:build windows && !legacy_vault_names

package vaultproc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The earlier release's pipe for a vault is named as that release names it, after this user and the vault,
// and is not this release's.
func TestEarlierPipePath(t *testing.T) {
	vaultDir := filepath.Join(shortTempDir(t), "vault")
	current, err := SocketPath(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	path := earlierPathOf(vaultDir)
	if path == "" || path == current || !strings.HasPrefix(path, pipePrefix+legacyPipeName) ||
		strings.TrimPrefix(path, pipePrefix+legacyPipeName) != strings.TrimPrefix(current, pipePrefix+pipeName) {
		t.Fatalf("earlierPathOf() = %q, want the earlier name of %q", path, current)
	}
}

// Nothing at the earlier pipe is nothing to end; a pipe this process serves itself is reported and left
// alone.
func TestEarlierPipeServedByThisProcessIsNotEnded(t *testing.T) {
	vaultDir := filepath.Join(shortTempDir(t), "vault")
	if pid, err := FindEarlier(vaultDir); pid != 0 || err != nil {
		t.Fatalf("FindEarlier() without a pipe = %d, %v, want none", pid, err)
	}
	l, err := Listen(earlierPathOf(vaultDir))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	if pid, err := FindEarlier(vaultDir); pid != 0 || !errors.Is(err, ErrEarlierUnknown) {
		t.Fatalf("FindEarlier() = %d, %v, want ErrEarlierUnknown", pid, err)
	}
	if pid, err := EndEarlier(context.Background(), vaultDir); pid != 0 || !errors.Is(err, ErrEarlierUnknown) {
		t.Fatalf("EndEarlier() = %d, %v, want ErrEarlierUnknown", pid, err)
	}
	if pid, err := FindEarlier(vaultDir); pid != 0 || !errors.Is(err, ErrEarlierUnknown) {
		t.Fatalf("FindEarlier() afterwards = %d, %v, want the pipe still served", pid, err)
	}
}
