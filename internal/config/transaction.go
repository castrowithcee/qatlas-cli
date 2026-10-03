package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/castrowithcee/qatlas-cli/internal/filelock"
)

// Revision identifies the exact bytes of a configuration file as one LoadVersioned call read them. It is
// opaque: callers only keep it and hand it back to SaveIfUnchanged. RevisionAbsent stands for "the file did
// not exist".
type Revision string

// RevisionAbsent is the revision of a configuration file that does not exist yet.
const RevisionAbsent Revision = ""

func revisionOf(data []byte) Revision {
	sum := sha256.Sum256(data)
	return Revision(hex.EncodeToString(sum[:]))
}

// ErrConflict is matched by errors.Is for a *ConflictError.
var ErrConflict = errors.New("the configuration changed since it was read")

// ConflictError reports that the configuration file no longer has the content a change was based on, so
// the change was not written. The caller reloads and decides again; nothing is merged automatically.
type ConflictError struct {
	Path string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s changed since it was read; reload it and repeat the change", e.Path)
}

// Is makes errors.Is(err, ErrConflict) true.
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// lockPath returns the lock file for the configuration. It sits next to the resolved target, so a symlinked
// configuration is guarded by the lock of the real file. The lock file is never replaced or removed, which
// keeps the lock stable while the configuration itself is replaced by rename.
func (s *Store) lockPath() string {
	target := s.path
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	return target + ".lock"
}

// lock takes the cross-process lock and returns the function that releases it.
func (s *Store) lock() (func(), error) {
	path := s.lockPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", dir, err)
	}
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

// readVersioned reads the file once and returns the decoded configuration with the revision of the bytes
// that were decoded. A missing file yields a *NotFoundError and RevisionAbsent.
func (s *Store) readVersioned() (*Config, Revision, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, RevisionAbsent, &NotFoundError{Path: s.path}
		}
		return nil, RevisionAbsent, fmt.Errorf("cannot read %s: %w", s.path, err)
	}
	rev := revisionOf(data)
	cfg, err := Decode(bytes.NewReader(data), s.providers)
	if err != nil {
		return nil, rev, &InvalidError{Path: s.path, Err: err}
	}
	return cfg, rev, nil
}

// currentRevision returns the revision of the file as it is now.
func (s *Store) currentRevision() (Revision, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RevisionAbsent, nil
		}
		return RevisionAbsent, fmt.Errorf("cannot read %s: %w", s.path, err)
	}
	return revisionOf(data), nil
}

// LoadVersioned loads the configuration like Load and returns the revision of the bytes it decoded. It
// takes no lock: files are only ever replaced atomically, so a read sees a complete old or new file.
// A missing file returns a *NotFoundError together with RevisionAbsent, so a caller can create the file
// with SaveIfUnchanged(cfg, RevisionAbsent). An invalid file returns its revision with the *InvalidError.
//
// Transaction contract: a read-modify-write is only safe when it ends in Update or SaveIfUnchanged. Save
// takes no lock and checks no revision; it is not a transaction and must not be used for a change that
// was based on an earlier read.
func (s *Store) LoadVersioned() (*Config, Revision, error) {
	return s.readVersioned()
}

// RevisionOf returns the revision the file has right after cfg was saved to it: the revision of the bytes
// Save writes for cfg. A caller that saved cfg through SaveIfUnchanged, Update, or Transact and keeps cfg
// as its loaded state uses it as the base of its next change, without reading the file again; a re-read
// could pick up a change another writer made meanwhile and then pair it with a state that lacks it. It is
// only meaningful for a cfg the store just saved, and it reads nothing from disk.
func (s *Store) RevisionOf(cfg *Config) (Revision, error) {
	data, err := s.marshal(cfg)
	if err != nil {
		return RevisionAbsent, &InvalidError{Path: s.path, Err: fmt.Errorf("the configuration could not be encoded: %w", err)}
	}
	return revisionOf(data), nil
}

// SaveIfUnchanged writes cfg only if the file still has the revision rev, which LoadVersioned returned for
// the configuration the change was based on. Check and write happen under the cross-process lock. If the
// file changed in between, nothing is written and the error is a *ConflictError (errors.Is(err, ErrConflict)).
// Validation, the load-back check and the atomic replacement are those of Save.
func (s *Store) SaveIfUnchanged(cfg *Config, rev Revision) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	current, err := s.currentRevision()
	if err != nil {
		return err
	}
	if current != rev {
		return &ConflictError{Path: s.path}
	}
	return s.Save(cfg)
}

// Update runs one transaction: it takes the cross-process lock, loads the current configuration (an empty
// one from Store.New when the file does not exist), calls change with it, and saves the result before it
// releases the lock. Concurrent Update and SaveIfUnchanged calls therefore never lose each other's change.
//
// If change returns an error, or the result is invalid, nothing is written and the error is returned
// unchanged (an invalid result as *InvalidError). change must only edit the configuration it gets and
// must not call back into this store, because the lock is not reentrant. A file that exists but does not
// load is an error; Update never replaces a configuration it could not read. If a writer that bypasses
// the lock (plain Save) replaced the file after the read, Update returns a *ConflictError instead of
// overwriting it.
func (s *Store) Update(change func(*Config) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	cfg, rev, err := s.readVersioned()
	if err != nil {
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			return err
		}
		cfg = s.New()
	}
	if err := change(cfg); err != nil {
		return err
	}
	current, err := s.currentRevision()
	if err != nil {
		return err
	}
	if current != rev {
		return &ConflictError{Path: s.path}
	}
	return s.Save(cfg)
}

// Transact runs work under the cross-process lock if the file still has the revision rev, which
// LoadVersioned returned for the configuration the change was based on. It exists for a change that must do
// something else before it saves, such as writing the secrets a new credential names, and must not do it
// unless the configuration it is based on is still current.
//
// The revision is checked before work runs, so a conflict is reported as a *ConflictError without work
// having done anything. work gets a save function that checks the revision again, because a writer that
// bypasses the lock may have replaced the file meanwhile, and then writes like Save; if either step fails,
// nothing was written and work can undo what it did, still under the lock, before it returns. The lock is
// held while work runs, so work must be short and must not call back into this store (the lock is not
// reentrant). Whatever work returns is returned unchanged.
func (s *Store) Transact(rev Revision, work func(save func(*Config) error) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	current, err := s.currentRevision()
	if err != nil {
		return err
	}
	if current != rev {
		return &ConflictError{Path: s.path}
	}
	return work(func(cfg *Config) error {
		current, err := s.currentRevision()
		if err != nil {
			return err
		}
		if current != rev {
			return &ConflictError{Path: s.path}
		}
		return s.Save(cfg)
	})
}
