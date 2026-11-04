//go:build linux || darwin

package selfupdate

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// formerInstallation is a program file at <prefix>/bin/qatlas, as the former updater leaves it.
func formerInstallation(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("program"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The program moves to qconnect, keeping its file, and qatlas becomes a link to it. Once done, starting
// through either name finds nothing to do.
func TestAdoptNameMovesTheProgramAndLeavesAnAlias(t *testing.T) {
	former := formerInstallation(t)
	before, err := os.Stat(former)
	if err != nil {
		t.Fatal(err)
	}
	path, done, err := adoptName(former)
	current := filepath.Join(filepath.Dir(former), "qconnect")
	// adoptName answers with the resolved path; on macOS the temporary directory lies behind a symlink.
	want, _ := filepath.EvalSymlinks(filepath.Dir(former))
	if err != nil || !done || path != filepath.Join(want, "qconnect") {
		t.Fatalf("adoptName() = %q, %v, %v", path, done, err)
	}
	if after, err := os.Lstat(current); err != nil || !os.SameFile(before, after) {
		t.Fatalf("qconnect is %v, %v, want the program's own file", after, err)
	}
	assertAlias(t, former)
	for _, start := range []string{former, current} {
		if _, done, err := adoptName(start); done || err != nil {
			t.Errorf("adoptName(%s) again = %v, %v, want nothing to do", start, done, err)
		}
	}
}

// Of processes that start at once, one adopts the name; the others find it taken and change nothing.
func TestAdoptNameHappensOnceUnderConcurrentStarts(t *testing.T) {
	former := formerInstallation(t)
	var wg sync.WaitGroup
	var adopted atomic.Int32
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, done, _ := adoptName(former); done {
				adopted.Add(1)
			}
		}()
	}
	wg.Wait()
	if adopted.Load() != 1 {
		t.Fatalf("%d starts adopted the name, want exactly one", adopted.Load())
	}
	assertAlias(t, former)
	assertFile(t, filepath.Join(filepath.Dir(former), "qconnect"), "program")
	entries, _ := os.ReadDir(filepath.Dir(former))
	if len(entries) != 2 {
		t.Errorf("bin holds %d entries, want qatlas and qconnect", len(entries))
	}
}

// A qconnect that is there already, whoever put it there, is never replaced, and qatlas stays as it is.
func TestAdoptNameLeavesAnExistingQconnect(t *testing.T) {
	for name, make := range map[string]func(path string) error{
		"file": func(path string) error { return os.WriteFile(path, []byte("foreign"), 0o755) },
		"link": func(path string) error { return os.Symlink("/elsewhere", path) },
		"dir":  func(path string) error { return os.Mkdir(path, 0o755) },
	} {
		t.Run(name, func(t *testing.T) {
			former := formerInstallation(t)
			current := filepath.Join(filepath.Dir(former), "qconnect")
			if err := make(current); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(current)
			if _, done, _ := adoptName(former); done {
				t.Fatal("adoptName() replaced an existing qconnect")
			}
			if after, err := os.Lstat(current); err != nil || !os.SameFile(before, after) {
				t.Errorf("qconnect changed: %v", err)
			}
			if info, err := os.Lstat(former); err != nil || !info.Mode().IsRegular() {
				t.Errorf("qatlas changed: %v, %v", info, err)
			}
			assertFile(t, former, "program")
		})
	}
}

// Only an installation, <prefix>/bin/qatlas, is adopted; a program elsewhere or under another name is not.
func TestAdoptNameOnlyAdoptsAnInstallation(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{filepath.Join(dir, "qatlas"), filepath.Join(dir, "bin", "qatlas-old"), filepath.Join(dir, "bin", "qconnect")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("program"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, done, err := adoptName(path); done || err != nil {
			t.Errorf("adoptName(%s) = %v, %v", path, done, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "qconnect")); err == nil {
		t.Error("a program outside bin got a qconnect")
	}
}
