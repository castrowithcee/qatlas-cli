package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	if runtime.GOOS != "windows" {
		adoptManpages(filepath.Join(filepath.Dir(filepath.Dir(path)), "share", "man", "man1"))
	}
	return path, true
}

// subpageName is the only form of a manual page besides qconnect.1 that an archive may install.
var subpageName = regexp.MustCompile(`^qconnect-[a-z0-9-]+\.1$`)

// adoptManpages gives the manual pages of an installation whose program just took its current name the
// same step: qatlas.1, a regular file, becomes qconnect.1 and qatlas.1 a link to it, and the pages
// qatlas-*.1 go. A qconnect.1 that is there already is never replaced, and then qatlas.1 stays as it is.
// A link, not a .so redirect, because it needs no search path and mirrors the alias in bin. Best effort:
// what fails stays as it was, and man still finds a page for either name. Transition until the next major
// release.
func adoptManpages(manDir string) {
	former := filepath.Join(manDir, formerName+".1")
	current := filepath.Join(manDir, currentName+".1")
	if info, err := os.Lstat(former); err == nil && info.Mode().IsRegular() {
		// A hard link never replaces an entry, so a qconnect.1 that is there stays.
		if os.Link(former, current) == nil {
			_ = linkOver(former, currentName+".1")
		}
	}
	removeRegular(manDir, formerName+"-*.1", nil)
}

// removeRegular removes the regular files in dir whose name matches pattern, except those in keep.
func removeRegular(dir, pattern string, keep map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ok, _ := filepath.Match(pattern, entry.Name()); !ok || entry.Type() != 0 {
			continue
		}
		if _, kept := keep[entry.Name()]; !kept {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
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
	return linkOver(path, currentName)
}

// linkOver replaces path by a link to target in one rename.
func linkOver(path, target string) error {
	link := filepath.Join(filepath.Dir(path), ".qconnect-alias-"+strconv.Itoa(os.Getpid())+"-"+
		strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("make %s an alias of %s: %w", filepath.Base(path), target, err)
	}
	if err := os.Rename(link, path); err != nil {
		_ = os.Remove(link)
		return fmt.Errorf("make %s an alias of %s: %w", filepath.Base(path), target, err)
	}
	return nil
}

// dropCurrentName removes the program's current name and its manual pages once a release before the rename is
// installed under the former name: that program does not call itself qconnect, so no qconnect may start it.
// It is best effort; what may stay is the newer program, which still is what its name says.
func dropCurrentName(goos, path, manDir string) {
	if info, err := os.Lstat(path); err == nil && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		if goos == "windows" {
			retire(osFileOps, path)
		} else {
			_ = os.Remove(path)
		}
	}
	// Only regular files: the former name's own page, which may be a link to qconnect.1, is replaced by the
	// install itself.
	removeRegular(manDir, currentName+".1", nil)
	removeRegular(manDir, currentName+"-*.1", nil)
}
