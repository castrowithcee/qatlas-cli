package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Backup is one backup of the configuration file in the backups directory beside it.
type Backup struct {
	// Name is the file name in the backups directory.
	Name string
	// Time is the moment the backup was made, as its name records it.
	Time time.Time
	path string
}

// Path is the full path of the backup file.
func (b Backup) Path() string { return b.path }

// backupName matches the backups of the configuration file: the plain ones, the hand-edited ones, and the
// first hand-edited one. Copies of other files (the vault) and anything else in the directory do not match.
var backupName = regexp.MustCompile(`^config-(?:first-hand-)?(\d{8}T\d{6}Z)-(\d{3})(?:-hand)?\.yaml$`)

// Backups lists the backups of the configuration file, newest first. A missing backups directory has none.
func (s *Store) Backups() ([]Backup, error) {
	entries, err := os.ReadDir(s.backupDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", s.backupDir(), err)
	}
	type item struct {
		Backup
		seq string
	}
	var items []item
	for _, e := range entries {
		m := backupName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		t, err := time.Parse("20060102T150405Z", m[1])
		if err != nil {
			continue
		}
		items = append(items, item{Backup{Name: e.Name(), Time: t, path: filepath.Join(s.backupDir(), e.Name())}, m[2]})
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Time.Equal(items[j].Time) {
			return items[i].Time.After(items[j].Time)
		}
		return items[i].seq > items[j].seq
	})
	out := make([]Backup, len(items))
	for i, it := range items {
		out[i] = it.Backup
	}
	return out, nil
}

// ReadBackup returns the bytes of b.
func (s *Store) ReadBackup(b Backup) ([]byte, error) {
	data, err := readFile(b.path, s.observe)
	if err != nil {
		return nil, fmt.Errorf("cannot read the backup %s: %w", b.Name, err)
	}
	return data, nil
}

// Snapshot returns the bytes of the configuration file as it is now and their revision. A missing file is a
// *NotFoundError.
func (s *Store) Snapshot() ([]byte, Revision, error) {
	data, err := readFile(s.path, s.observe)
	if errors.Is(err, os.ErrNotExist) {
		return nil, RevisionAbsent, &NotFoundError{Path: s.path}
	}
	if err != nil {
		return nil, RevisionAbsent, fmt.Errorf("cannot read %s: %w", s.path, err)
	}
	return data, revisionOf(data), nil
}

// Restored is what RestoreBackup replaced, for UndoRestore.
type Restored struct {
	// Previous is the path of the backup RestoreBackup made of the file it replaced.
	Previous string
}

// RestoreBackup replaces the configuration file by data, which the caller took from a backup, if the file
// still has the revision base. Check and replacement happen under the cross-process lock. The file it
// replaces is saved as a new backup first (the one way a save backs up, rotation included); if that fails,
// nothing is changed. data is not decoded here: the caller chose it for being readable, and a backup the
// running program cannot read is the very reason to restore one. The new file has the mode of every
// configuration file.
func (s *Store) RestoreBackup(data []byte, base Revision) (Restored, error) {
	unlock, err := s.lock()
	if err != nil {
		return Restored{}, err
	}
	defer unlock()
	current, err := s.currentRevision()
	if err != nil {
		return Restored{}, err
	}
	if current != base {
		return Restored{}, &ConflictError{Path: s.path}
	}
	previous, err := s.backupCurrent()
	if err != nil {
		return Restored{}, err
	}
	if err := s.replace(data); err != nil {
		return Restored{}, err
	}
	return Restored{Previous: previous}, nil
}

// UndoRestore puts back the file RestoreBackup replaced, from the backup it made of it, and takes no further
// backup. A restore that replaced no file (Previous empty) is undone by removing the file.
func (s *Store) UndoRestore(r Restored) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if r.Previous == "" {
		target := s.path
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			target = resolved
		}
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot remove %s: %w", target, err)
		}
		return nil
	}
	data, err := readFile(r.Previous, s.observe)
	if err != nil {
		return fmt.Errorf("cannot read the backup %s: %w", filepath.Base(r.Previous), err)
	}
	return s.replace(data)
}
