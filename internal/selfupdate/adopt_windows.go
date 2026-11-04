package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// adoptName puts a copy of the program at executable, when it is <dir>\bin\qatlas.exe, beside it as
// qconnect.exe; qatlas.exe stays as the alias and an update replaces both. A copy, because a link needs
// rights or a developer setting there, and a script in its place would not start for a client that runs
// qatlas directly. The copy appears complete under its name or not at all, and never replaces a qconnect.exe.
func adoptName(executable string) (string, bool, error) {
	path, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", false, err
	}
	dir := filepath.Dir(path)
	if !strings.EqualFold(filepath.Base(path), formerName+".exe") || !strings.EqualFold(filepath.Base(dir), "bin") {
		return "", false, nil
	}
	current := filepath.Join(dir, currentName+".exe")
	if _, err := os.Lstat(current); !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	staged, err := copyProgram(path, dir)
	if err != nil {
		return "", false, err
	}
	defer os.Remove(staged)
	// A hard link never replaces an entry: of processes that start at once only one creates qconnect.exe.
	if err := os.Link(staged, current); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return current, true, nil
}

// copyProgram copies the program at path to a new file in dir and returns its path.
func copyProgram(path, dir string) (staged string, err error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".qconnect-bin-*")
	if err != nil {
		return "", err
	}
	staged = out.Name()
	defer func() {
		if closeErr := out.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(staged)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return "", fmt.Errorf("copy the program: %w", err)
	}
	return staged, out.Sync()
}
