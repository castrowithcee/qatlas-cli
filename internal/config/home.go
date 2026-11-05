package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/envvar"
	"github.com/castrowithcee/qatlas-connect/internal/filelock"
)

const (
	// ecosystemDirName is the directory below the home directory every Qatlas tool keeps its own folder in.
	ecosystemDirName = ".qatlas"
	// legacyHomeDirName is the folder of the versions before the rename. It is read, never written, and
	// stays in place so a rollback to such a version works on its own state. Transitional until the next
	// major release.
	legacyHomeDirName = "cli"
	// stagingPrefix names the directories a migration copies into before the result becomes visible.
	stagingPrefix = ".connect-migrating-"
	// migrationLockName serialises the migration across processes; the file is never removed.
	migrationLockName = ".connect-migrate.lock"
)

// defaultDir returns the configuration directory below home: the current one once it exists, else the
// legacy one while only that exists, so a run whose migration has not happened or failed still finds its
// state.
func defaultDir(home string) string {
	current := filepath.Join(home, ecosystemDirName, homeDirName)
	if _, err := os.Lstat(current); err == nil {
		return current
	}
	legacy := filepath.Join(home, ecosystemDirName, legacyHomeDirName)
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy
	}
	return current
}

// HomeMigration describes a completed migration of the configuration directory.
type HomeMigration struct {
	From, To string
}

// MigrateHome copies the legacy configuration directory to the current one when the configuration is
// resolved from the default location and only the legacy directory exists. It returns nil when nothing was
// to do. explicit is the --config value; QCONNECT_CONFIG or QCONNECT_HOME point to an explicit place as
// well, and an explicit place is never migrated.
func MigrateHome(explicit string) (*HomeMigration, error) {
	if explicit != "" || envvar.Set("QCONNECT_CONFIG") || envvar.Set("QCONNECT_HOME") {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	return migrateHome(home)
}

// LegacyDefault reports whether path is the default configuration file of the versions before the rename,
// compared after cleaning and without resolving symbolic links, while neither QCONNECT_CONFIG nor
// QCONNECT_HOME point to an explicit place. A vault process of those versions that serves its default
// location starts its successor with this file.
func LegacyDefault(path string) bool {
	if path == "" || envvar.Set("QCONNECT_CONFIG") || envvar.Set("QCONNECT_HOME") {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	want, err := filepath.Abs(filepath.Join(home, ecosystemDirName, legacyHomeDirName, "config.yaml"))
	if err != nil {
		return false
	}
	got, err := filepath.Abs(path)
	return err == nil && got == want
}

// LegacyDefaultDir returns the default configuration directory of the versions before the rename where the
// configuration is resolved from the default location: neither explicit, the --config value, nor
// QCONNECT_CONFIG or QCONNECT_HOME name a place. Otherwise, or without a home directory, it returns "".
func LegacyDefaultDir(explicit string) string {
	if explicit != "" || envvar.Set("QCONNECT_CONFIG") || envvar.Set("QCONNECT_HOME") {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ecosystemDirName, legacyHomeDirName)
}

// MigrateDefault migrates as a run from the default location does (see MigrateHome) and returns the
// configuration file in the current default directory. Unlike Path it never falls back to the legacy
// directory: when the current one does not exist afterwards, it fails.
func MigrateDefault() (string, error) {
	if _, err := MigrateHome(""); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine the user home directory: %w", err)
	}
	dir := filepath.Join(home, ecosystemDirName, homeDirName)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("the configuration was not migrated to %s", dir)
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// migrateHome copies home/.qatlas/cli into home/.qatlas/connect. The copy goes into a staging directory and
// becomes visible by one rename, so the target is either absent or complete. Concurrent processes serialise
// on a lock file and the first one migrates; the others find the target. A staging directory left by an
// interrupted run is removed and the copy starts over. The legacy directory is only read.
func migrateHome(home string) (*HomeMigration, error) {
	root := filepath.Join(home, ecosystemDirName)
	target := filepath.Join(root, homeDirName)
	legacy := filepath.Join(root, legacyHomeDirName)
	if _, err := os.Lstat(target); err == nil {
		return nil, nil
	}
	if info, err := os.Stat(legacy); err != nil || !info.IsDir() {
		return nil, nil
	}

	unlock, err := lockFile(filepath.Join(root, migrationLockName))
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := os.Lstat(target); err == nil {
		return nil, nil
	}
	if err := removeStaging(root); err != nil {
		return nil, err
	}

	// Processes of a version before the rename may still write the legacy directory. Holding its
	// configuration and invocation log locks, in the order those processes take them, keeps both files
	// whole while they are copied.
	for _, path := range []string{
		filepath.Join(legacy, "config.yaml.lock"),
		filepath.Join(legacy, "vault", "logs", ".lock"),
	} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		release, err := lockFile(path)
		if err != nil {
			return nil, err
		}
		defer release()
	}

	staging, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		return nil, fmt.Errorf("cannot migrate the configuration to %s: %w", target, err)
	}
	if err := copyTree(legacy, staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("cannot migrate the configuration to %s: %w", target, err)
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("cannot migrate the configuration to %s: %w", target, err)
	}
	syncDir(root)
	return &HomeMigration{From: legacy, To: target}, nil
}

// lockFile takes the cross-process lock on path and returns the function that releases it.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock file %s: %w", path, err)
	}
	if err := filelock.Lock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot lock %s: %w", path, err)
	}
	return func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}, nil
}

// removeStaging removes what an interrupted migration left in root. Only directories this code names are
// touched.
func removeStaging(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", root, err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), stagingPrefix) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return fmt.Errorf("cannot remove an interrupted migration: %w", err)
			}
		}
	}
	return nil
}

// copyTree copies the directories, regular files, and symbolic links below src into dst, which exists, with
// their permissions. Lock files are left out: they carry no content, and the legacy ones are held open
// during the copy. Sockets and other special files belong to running processes and are left out too.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.Mkdir(out, mode.Perm())
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, out)
		case mode.IsRegular():
			if strings.HasSuffix(entry.Name(), ".lock") {
				return nil
			}
			return copyFile(path, out, mode.Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

// syncDir makes a rename in dir durable where the platform supports syncing a directory.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}
