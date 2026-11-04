package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// currentName is the program's name.
	currentName = "qconnect"
	// formerName is the program's name before the rename. An installation keeps it beside the program as an
	// alias: a link on Linux and macOS, a copy on Windows. Transition until the next major release.
	formerName = "qatlas"
)

// AdoptCurrentName gives an installation that runs as <prefix>/bin/qatlas, as an updater of a release before
// the rename leaves it, its current name: the program becomes <prefix>/bin/qconnect and qatlas an alias of
// it (see adoptName). It returns the program's new path and true only to the one process that did it. It
// does nothing for a program elsewhere, under another name, or beside an existing qconnect, which is never
// replaced, whoever put it there; and it is silent, since a failure leaves a working installation.
// Transition until the next major release.
func AdoptCurrentName() (string, bool) {
	executable, err := os.Executable()
	if err != nil {
		return "", false
	}
	path, done, err := adoptName(executable)
	if err != nil || !done {
		return "", false
	}
	return path, true
}

// followAlias returns the program a former-name alias at path stands for: qconnect beside it, where path is
// a link to exactly that. Any other path comes back as it is.
func followAlias(path, goos string) string {
	if goos == "windows" || filepath.Base(path) != formerName {
		return path
	}
	if target, err := os.Readlink(path); err == nil && target == currentName {
		return filepath.Join(filepath.Dir(path), currentName)
	}
	return path
}

// aliasFormerName makes path, the former name beside the program, a link to the program, in one rename,
// where it is a file or another link. A missing path stays missing, and anything else at it stays as it is.
func aliasFormerName(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil && target == currentName {
			return nil
		}
	} else if !info.Mode().IsRegular() {
		return nil
	}
	link := filepath.Join(filepath.Dir(path), ".qconnect-alias-"+strconv.Itoa(os.Getpid())+"-"+
		strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Symlink(currentName, link); err != nil {
		return fmt.Errorf("make %s an alias of %s: %w", formerName, currentName, err)
	}
	if err := os.Rename(link, path); err != nil {
		_ = os.Remove(link)
		return fmt.Errorf("make %s an alias of %s: %w", formerName, currentName, err)
	}
	return nil
}

// dropCurrentName removes the program's current name and manual page once a release before the rename is
// installed under the former name: that program does not call itself qconnect, so no qconnect may start it.
// It is best effort; what may stay is the newer program, which still is what its name says.
func dropCurrentName(goos, path, manpage string) {
	if info, err := os.Lstat(path); err == nil && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		if goos == "windows" {
			retire(osFileOps, path)
		} else {
			_ = os.Remove(path)
		}
	}
	if info, err := os.Lstat(manpage); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(manpage)
	}
}
