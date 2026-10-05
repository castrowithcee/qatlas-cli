package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// fileOps are the file operations of a replacement, injectable so that the sequence and its rollback are
// testable on every platform.
type fileOps struct {
	rename func(oldPath, newPath string) error
	remove func(path string) error
}

var osFileOps = fileOps{rename: os.Rename, remove: os.Remove}

// replaceRunning puts the staged file in place of target, which may be a running Windows executable: such
// a file can be renamed but not overwritten. The running file becomes target+".old", a leftover of an
// earlier replacement is removed first or, when it cannot be, avoided by a unique name. Should the second
// step fail, the old file is renamed back, so the installation is never left without target.
func replaceRunning(ops fileOps, staged, target string) error {
	old := target + ".old"
	if err := ops.remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		old = target + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if err := ops.rename(target, old); err != nil {
		return fmt.Errorf("move the running executable aside: %w", err)
	}
	if err := ops.rename(staged, target); err != nil {
		if backErr := ops.rename(old, target); backErr != nil {
			return fmt.Errorf("replace executable: %w (restoring the previous one also failed: %v)", err, backErr)
		}
		return fmt.Errorf("replace executable: %w", err)
	}
	// The old file is still the running program, so this usually fails; the next start or update removes it.
	_ = ops.remove(old)
	return nil
}

// removeLeftovers removes the files an earlier replacement of executable left beside it: executable+".old"
// and the uniquely named ones. It is best effort and silent.
func removeLeftovers(ops fileOps, executable string) {
	_ = ops.remove(executable + ".old")
	matches, err := filepath.Glob(strings.NewReplacer("[", "[[]").Replace(executable) + ".old-*")
	if err != nil {
		return
	}
	for _, match := range matches {
		_ = ops.remove(match)
	}
}

// CleanupLeftovers removes the qatlas.exe.old that a Windows update left beside the running program. It
// does nothing elsewhere, is silent, and a failure is no error: the file is retried at the next start.
func CleanupLeftovers() {
	if runtime.GOOS != "windows" {
		return
	}
	executable, err := os.Executable()
	if err != nil || !strings.EqualFold(filepath.Base(executable), "qatlas.exe") {
		return
	}
	removeLeftovers(osFileOps, executable)
}
