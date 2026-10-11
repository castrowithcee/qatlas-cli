package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/fileretry"
)

// seamFile replaces the retry seam for one test. Every failure except a missing file counts as transient,
// so a directory standing where the file belongs stands in for a lock that another process releases.
func seamFile(t *testing.T, transient bool, onSleep func(n int)) *int {
	t.Helper()
	oldTransient, oldSleep := transientFile, sleepFile
	t.Cleanup(func() { transientFile, sleepFile = oldTransient, oldSleep })
	slept := 0
	transientFile = func(error) bool { return transient }
	sleepFile = func(time.Duration) {
		slept++
		if onSleep != nil {
			onSleep(slept)
		}
	}
	return &slept
}

// blockedByDir returns a config path occupied by an empty directory and a function that frees it.
func blockedByDir(t *testing.T) (string, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(dir, ".qatlas-config-*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestReadsRetryTransientFailureUntilSuccess(t *testing.T) {
	good, _ := newTarget(t)
	if err := good.Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(good.path)
	if err != nil {
		t.Fatal(err)
	}

	path, free := blockedByDir(t)
	slept := seamFile(t, true, func(n int) {
		if n == 2 {
			free()
			if err := os.WriteFile(path, data, fileMode); err != nil {
				t.Fatal(err)
			}
		}
	})
	store := NewStore(path, testProviders)

	if _, err := Load(path, testProviders); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *slept != 2 {
		t.Errorf("Load waited %d times, want 2", *slept)
	}

	// Each read path retries on its own; block the file again for the other two.
	for name, read := range map[string]func() error{
		"LoadVersioned": func() error { _, _, err := store.LoadVersioned(); return err },
		"currentRevision": func() error {
			_, err := store.currentRevision()
			return err
		},
	} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		*slept = 0
		free = func() { _ = os.Remove(path) }
		if err := read(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if *slept != 2 {
			t.Errorf("%s waited %d times, want 2", name, *slept)
		}
	}
}

func TestSaveRetriesTransientReplaceUntilSuccess(t *testing.T) {
	path, free := blockedByDir(t)
	slept := seamFile(t, true, func(n int) {
		if n == 3 {
			free()
		}
	})
	store := NewStore(path, testProviders)
	if err := store.Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	if *slept != 3 {
		t.Errorf("waited %d times, want 3", *slept)
	}
	if _, err := Load(path, testProviders); err != nil {
		t.Fatalf("saved file does not load: %v", err)
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestSaveGivesUpAfterBudgetAndCleansUp(t *testing.T) {
	path, _ := blockedByDir(t)
	slept := seamFile(t, true, nil)
	err := NewStore(path, testProviders).Save(sample(t))
	if err == nil || !strings.Contains(err.Error(), "cannot replace "+path) {
		t.Fatalf("err = %v, want cannot replace", err)
	}
	if *slept != len(fileretry.Delays) {
		t.Errorf("waited %d times, want %d", *slept, len(fileretry.Delays))
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Errorf("target changed: %v, %v", fi, err)
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	path, _ := blockedByDir(t)
	slept := seamFile(t, false, nil)
	if err := NewStore(path, testProviders).Save(sample(t)); err == nil {
		t.Fatal("Save succeeded")
	}
	if _, err := Load(path, testProviders); err == nil || errors.As(err, new(*NotFoundError)) {
		t.Fatalf("Load err = %v, want a read error", err)
	}
	if *slept != 0 {
		t.Errorf("waited %d times, want none", *slept)
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestMissingFileIsNeverRetried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	slept := seamFile(t, true, nil)
	store := NewStore(path, testProviders)

	if _, err := Load(path, testProviders); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("Load err = %v, want NotFoundError", err)
	}
	if _, rev, err := store.LoadVersioned(); !errors.As(err, new(*NotFoundError)) || rev != RevisionAbsent {
		t.Errorf("LoadVersioned = %v, %v, want NotFoundError and RevisionAbsent", rev, err)
	}
	if rev, err := store.currentRevision(); err != nil || rev != RevisionAbsent {
		t.Errorf("currentRevision = %v, %v", rev, err)
	}
	if *slept != 0 {
		t.Errorf("waited %d times, want none", *slept)
	}
}
