//go:build darwin

package vaultproc

import (
	"os"
	"path/filepath"
)

// fallbackSocketDir is where the socket moves when its usual path is too long: the directory name in the
// per-user temporary directory macOS gives every session in $TMPDIR, which only this user can enter. Without
// an absolute $TMPDIR there is no such directory to move to.
func fallbackSocketDir(name string) (string, bool) {
	tmp := os.Getenv("TMPDIR")
	if !filepath.IsAbs(tmp) {
		return "", false
	}
	return filepath.Join(tmp, name), true
}

// userRuntimeDir finds no runtime directory without XDG_RUNTIME_DIR: macOS has no conventional place for it.
func userRuntimeDir() string { return "" }
