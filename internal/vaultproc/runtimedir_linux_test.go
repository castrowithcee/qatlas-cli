//go:build linux

package vaultproc

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSocketPathFindsTheUserRuntimeDirectoryWithoutXDG(t *testing.T) {
	root := userRuntimeRoot
	t.Cleanup(func() { userRuntimeRoot = root })
	// A short root keeps the socket path within the kernel's limit; t.TempDir names it after the test.
	short := func() string {
		t.Helper()
		dir, err := os.MkdirTemp("", "qrt")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		return dir
	}
	userRuntimeRoot = short()
	t.Setenv("XDG_RUNTIME_DIR", "")
	vaultDir := filepath.Join(t.TempDir(), "vault")
	runtimeDir := filepath.Join(userRuntimeRoot, strconv.Itoa(os.Getuid()))

	beside := func() string {
		t.Helper()
		path, err := SocketPath(vaultDir)
		if err != nil {
			t.Fatalf("SocketPath() error = %v", err)
		}
		return filepath.Dir(path)
	}
	if dir := beside(); dir != filepath.Join(filepath.Dir(vaultDir), "run") {
		t.Fatalf("SocketPath() without a runtime directory = %s, want run beside the vault", dir)
	}

	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir := beside(); dir != filepath.Join(filepath.Dir(vaultDir), "run") {
		t.Fatalf("SocketPath() with a runtime directory open to others = %s, want it ignored", dir)
	}

	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if dir := beside(); dir != filepath.Join(runtimeDir, "qatlas") {
		t.Fatalf("SocketPath() without XDG_RUNTIME_DIR = %s, want the user's runtime directory", dir)
	}

	explicit := short()
	t.Setenv("XDG_RUNTIME_DIR", explicit)
	if dir := beside(); dir != filepath.Join(explicit, "qatlas") {
		t.Fatalf("SocketPath() with XDG_RUNTIME_DIR = %s, want the variable to win", dir)
	}
}
