//go:build darwin

package vaultproc

import (
	"os"
	"path/filepath"
)

// fallbackSocketDir is where the socket moves when its usual path is too long: qatlas in the per-user
// temporary directory macOS gives every session in $TMPDIR, which only this user can enter. Without an
// absolute $TMPDIR there is no such directory to move to.
func fallbackSocketDir() (string, bool) {
	tmp := os.Getenv("TMPDIR")
	if !filepath.IsAbs(tmp) {
		return "", false
	}
	return filepath.Join(tmp, "qatlas"), true
}
