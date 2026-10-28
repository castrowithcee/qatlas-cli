package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	backupDirName = "backups"
	// keepBackups is how many configuration backups survive a rotation. It is a constant on purpose: a
	// setting would be one more thing to misconfigure into losing the only copy.
	keepBackups = 10

	configBackupPrefix = "config-"
	// firstHandPrefix names the first hand-edited version Qatlas ever backed up. It is exempt from rotation.
	firstHandPrefix = "config-first-hand-"
	handSuffix      = "-hand"
)

// backupNow is a seam so a test controls the timestamp in a backup name.
var backupNow = time.Now

// backupDir is the directory of the configured path, not of a symlink target: a configuration linked into
// a dotfiles repository must not drag its backups there.
func (s *Store) backupDir() string { return filepath.Join(filepath.Dir(s.path), backupDirName) }

// backupBeforeReplace saves the bytes now in the configuration file as a new backup and then rotates. A
// missing file has nothing to save. Any failure is returned so the caller leaves the file unchanged.
func (s *Store) backupBeforeReplace() error {
	_, err := s.backupCurrent()
	return err
}

// backupCurrent is backupBeforeReplace that also returns the path of the new backup, or "" when there was
// nothing to save.
func (s *Store) backupCurrent() (string, error) {
	// A directory in the place of the file is not something to back up; the replacement reports it.
	if fi, err := os.Stat(s.path); err == nil && fi.IsDir() {
		return "", nil
	}
	data, err := readFile(s.path, s.observe)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("cannot read %s to back it up: %w", s.path, err)
	}

	prefix, suffix := "config", ".yaml"
	if s.isHandVersion(data) {
		if s.hasFirstHand() {
			suffix = handSuffix + suffix
		} else {
			prefix = "config-first-hand"
		}
	}
	path, err := s.writeBackup(prefix, suffix, data)
	if err != nil {
		return "", err
	}
	s.rotateBackups()
	return path, nil
}

// isHandVersion reports whether data differs from what Qatlas would write for it. Renamed provider or tool
// IDs do not count: the canonical form is computed from the file as written, before they are mapped.
func (s *Store) isHandVersion(data []byte) bool {
	raw, err := decodeRaw(bytes.NewReader(data), s.providers)
	if err != nil {
		return true
	}
	raw.ensure()
	raw.normalize()
	canonical, err := s.marshal(raw)
	return err != nil || !bytes.Equal(canonical, data)
}

func (s *Store) hasFirstHand() bool {
	entries, _ := os.ReadDir(s.backupDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), firstHandPrefix) {
			return true
		}
	}
	return false
}

// rotateBackups keeps the newest keepBackups configuration backups. Names sort by time, and the first hand
// version and copies of other files are not part of the set. Removal is best effort: a leftover file costs
// disk space, not data.
func (s *Store) rotateBackups() {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, configBackupPrefix) && strings.HasSuffix(n, ".yaml") && !strings.HasPrefix(n, firstHandPrefix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for len(names) > keepBackups {
		_ = os.Remove(filepath.Join(s.backupDir(), names[0]))
		names = names[1:]
	}
}

// BackupCopy stores a byte-for-byte copy of src in the backups directory next to the configuration, with a
// unique name and the same private permissions as a configuration backup, and returns its path. It is meant
// for files such as the vault that a change must be able to roll back. Copies never take part in the
// rotation of configuration backups: they are rare, and a rotation must not evict the only copy of a
// vault file.
func (s *Store) BackupCopy(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("cannot read %s to back it up: %w", src, err)
	}
	return s.writeBackup(filepath.Base(src), ".bak", data)
}

// writeBackup creates <prefix>-<UTC time>-<n><suffix> exclusively and fills it. n counts up past names
// that exist, so backups in the same second get distinct names that still sort in creation order.
func (s *Store) writeBackup(prefix, suffix string, data []byte) (string, error) {
	dir := s.backupDir()
	if err := makePrivateDir(dir); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	stamp := backupNow().UTC().Format("20060102T150405Z")
	for n := nextBackupSeq(dir, stamp); n < 1000; n++ {
		path := filepath.Join(dir, fmt.Sprintf("%s-%s-%03d%s", prefix, stamp, n, suffix))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot create the backup %s: %w", path, err)
		}
		err = restrictFile(path)
		if err == nil {
			err = writeAndSync(f, data)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("cannot write the backup %s: %w", path, err)
		}
		return path, nil
	}
	return "", fmt.Errorf("cannot find a free backup name in %s", dir)
}

// nextBackupSeq is one past the highest number any backup of this second has, whatever its kind, so a
// rotation that removed the oldest names cannot make a newer backup reuse a lower number and sort as older.
func nextBackupSeq(dir, stamp string) int {
	entries, _ := os.ReadDir(dir)
	next := 0
	for _, e := range entries {
		_, rest, ok := strings.Cut(e.Name(), "-"+stamp+"-")
		if ok && len(rest) >= 3 {
			if n, err := strconv.Atoi(rest[:3]); err == nil && n >= next {
				next = n + 1
			}
		}
	}
	return next
}

// makePrivateDir creates dir if it is missing. An existing directory keeps the permissions it has.
func makePrivateDir(dir string) error {
	err := os.Mkdir(dir, dirMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return restrictDir(dir)
}
