//go:build !windows

package vault

import (
	"io/fs"
	"os"
)

// checkMode refuses a vault file that others can read or write.
func checkMode(path string, info fs.FileInfo) error {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return &PermissionError{Path: path, Mode: perm}
	}
	return nil
}

// restrictFile makes a file the vault is about to fill readable and writable by its owner alone.
func restrictFile(path string) error { return os.Chmod(path, fileMode) }

// mkdirPrivate creates dir, and every parent it is missing, closed to everyone but the owner.
func mkdirPrivate(dir string) error { return os.MkdirAll(dir, dirMode) }
