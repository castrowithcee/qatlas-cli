//go:build darwin

package vaultproc

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A socket path too long for macOS moves to the per-user temporary directory, where a vault process can
// listen on it; without a usable $TMPDIR it stays refused.
func TestSocketPathMovesAnOverlongPathToTMPDIR(t *testing.T) {
	tmp := shortTempDir(t)
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_RUNTIME_DIR", "")
	vaultDir := filepath.Join("/", strings.Repeat("d", maxSocketPath), "vault")

	path, err := SocketPath(vaultDir)
	if err != nil || filepath.Dir(path) != filepath.Join(tmp, "qatlas") {
		t.Fatalf("SocketPath() of an overlong path = %s, %v, want it in %s", path, err, filepath.Join(tmp, "qatlas"))
	}
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() in the temporary directory error = %v", err)
	}
	_ = l.Close()

	t.Setenv("TMPDIR", "relative")
	var tooLong *PathTooLongError
	if _, err := SocketPath(vaultDir); !errors.As(err, &tooLong) {
		t.Fatalf("SocketPath() without an absolute TMPDIR error = %v, want PathTooLongError", err)
	}
}
