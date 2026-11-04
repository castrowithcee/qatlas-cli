//go:build linux || darwin

package selfupdate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// adoptName moves the program at executable, when it is <dir>/bin/qatlas, to qconnect beside it and leaves
// qatlas as a link to it. The name qatlas never disappears and qconnect never names anything but the program
// or a placeholder, so commands and MCP servers starting meanwhile run as before. Swapping the directory
// entries keeps the program's own entry, so a running process of it, a vault process included, finds itself
// under the new name and passes for the same program as one started through either name.
func adoptName(executable string) (string, bool, error) {
	path, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", false, err
	}
	dir := filepath.Dir(path)
	if filepath.Base(path) != formerName || filepath.Base(dir) != "bin" {
		return "", false, nil
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return "", false, err
	}
	current := filepath.Join(dir, currentName)
	// A link never replaces an entry: of processes that start at once only one gets past this, and a
	// qconnect that is there already stays as it is. The placeholder points to itself until the swap.
	if err := os.Symlink(currentName, current); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if err := exchange(path, current); err != nil {
		if target, readErr := os.Readlink(current); readErr == nil && target == currentName {
			_ = os.Remove(current)
		}
		return "", false, err
	}
	return current, true, nil
}
