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
	// transient reports a failure that a short wait may cure, such as a sharing violation caused by a
	// scanner or loader holding the file for a moment. Nil means no failure is retried.
	transient func(error) bool
	// sleep waits between attempts; nil means time.Sleep.
	sleep func(time.Duration)
}

var osFileOps = fileOps{rename: os.Rename, remove: os.Remove, transient: isTransientLock}

// renameDelays are the waits between the attempts of one rename, 2.775 s in all at most.
var renameDelays = []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond}

// renameRetrying renames, and repeats a rename that failed with a transient lock error after a short,
// growing wait, for a bounded time. A rename that fails changed nothing, so repeating it is safe; any
// other error is returned at once.
func (o fileOps) renameRetrying(oldPath, newPath string) error {
	err := o.rename(oldPath, newPath)
	for _, delay := range renameDelays {
		if err == nil || o.transient == nil || !o.transient(err) {
			return err
		}
		if o.sleep != nil {
			o.sleep(delay)
		} else {
			time.Sleep(delay)
		}
		err = o.rename(oldPath, newPath)
	}
	return err
}

// replaceRunning puts the staged file in place of target, which may be a running Windows executable: such
// a file can be renamed but not overwritten. The running file becomes target+".old", a leftover of an
// earlier replacement is removed first or, when it cannot be, avoided by a unique name. Should the second
// step fail, the old file is renamed back, so the installation is never left without target.
func replaceRunning(ops fileOps, staged, target string) error {
	old := target + ".old"
	if err := ops.remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		old = target + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if err := ops.renameRetrying(target, old); err != nil {
		return fmt.Errorf("move the running executable aside: %w", err)
	}
	if err := ops.renameRetrying(staged, target); err != nil {
		if backErr := ops.renameRetrying(old, target); backErr != nil {
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
