package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// The self-exec pattern below runs a real transaction from a freshly started process, so the tests
// exercise the lock across processes and not only across goroutines of one.
const (
	workerModeEnv = "QATLAS_CONFIG_TEST_WORKER"
	workerPathEnv = "QATLAS_CONFIG_TEST_PATH"
	workerNameEnv = "QATLAS_CONFIG_TEST_NAME"
)

func TestMain(m *testing.M) {
	mode := os.Getenv(workerModeEnv)
	if mode == "" {
		os.Exit(m.Run())
	}
	store := NewStore(os.Getenv(workerPathEnv), testProviders)
	name := os.Getenv(workerNameEnv)
	add := func(cfg *Config) error {
		return cfg.SetService(name, Service{Provider: "bookstack", BaseURL: "https://" + name + ".example.invalid"})
	}
	var err error
	switch mode {
	case "update":
		err = store.Update(add)
	case "cas":
		// Retry on conflict like a caller of the optimistic API would.
		for {
			var cfg *Config
			var rev Revision
			cfg, rev, err = store.LoadVersioned()
			if err != nil {
				break
			}
			if err = add(cfg); err != nil {
				break
			}
			if err = store.SaveIfUnchanged(cfg, rev); !errors.Is(err, ErrConflict) {
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runWorkers(t *testing.T, mode, path string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), workerModeEnv+"="+mode, workerPathEnv+"="+path,
				workerNameEnv+"="+fmt.Sprintf("svc%d", i))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("worker failed: %w: %s", err, out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestUpdateConcurrentProcessesKeepAllChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns subprocesses")
	}
	for _, mode := range []string{"update", "cas"} {
		t.Run(mode, func(t *testing.T) {
			store, path := newTarget(t)
			must(t, store.Save(sample(t)))
			const n = 6
			runWorkers(t, mode, path, n)

			cfg, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			for i := 0; i < n; i++ {
				if _, ok := cfg.Services[fmt.Sprintf("svc%d", i)]; !ok {
					t.Errorf("service svc%d was lost", i)
				}
			}
			if _, ok := cfg.Services["wiki"]; !ok {
				t.Error("the original service was lost")
			}
		})
	}
}

func TestUpdateCreatesMissingFile(t *testing.T) {
	store, path := newTarget(t)
	err := store.Update(func(c *Config) error {
		return c.SetService("wiki", Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"})
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	cfg, err := store.Load()
	if err != nil || len(cfg.Services) != 1 {
		t.Fatalf("Load() = %v, %v", cfg, err)
	}
	assertMode(t, path, fileMode)
	assertMode(t, path+".lock", fileMode)
	if runtime.GOOS != "windows" {
		assertMode(t, filepath.Dir(path), dirMode)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func TestUpdateErrorLeavesFileUntouched(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	before, _ := os.ReadFile(path)

	boom := errors.New("boom")
	if err := store.Update(func(c *Config) error {
		_ = c.SetService("extra", Service{Provider: "bookstack", BaseURL: "https://x.example.invalid"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Update() error = %v, want boom", err)
	}
	// An invalid result is rejected as invalid and not written.
	err := store.Update(func(c *Config) error {
		return c.SetConnection("bad", Connection{Service: "missing", Credential: "reader"})
	})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Update() error = %v, want *InvalidError", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the file changed although no transaction succeeded")
	}
	assertNoTemp(t, filepath.Dir(path))
}

func TestUpdateRefusesUnreadableConfig(t *testing.T) {
	store, path := newTarget(t)
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, []byte("not: [valid"), 0o600))
	called := false
	err := store.Update(func(*Config) error { called = true; return nil })
	var invalid *InvalidError
	if !errors.As(err, &invalid) || called {
		t.Fatalf("Update() error = %v, called = %v; want *InvalidError and no call", err, called)
	}
	if data, _ := os.ReadFile(path); string(data) != "not: [valid" {
		t.Error("an unreadable file was replaced")
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, ".qatlas-config-*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}

func TestSaveIfUnchangedDetectsStaleSnapshot(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))

	a, revA, err := store.LoadVersioned()
	if err != nil {
		t.Fatal(err)
	}
	b, revB, err := store.LoadVersioned()
	if err != nil {
		t.Fatal(err)
	}
	if revA != revB || revA == RevisionAbsent {
		t.Fatalf("revisions = %q, %q; want equal and non-empty", revA, revB)
	}
	must(t, a.SetService("first", Service{Provider: "bookstack", BaseURL: "https://a.example.invalid"}))
	must(t, b.SetService("second", Service{Provider: "bookstack", BaseURL: "https://b.example.invalid"}))

	must(t, store.SaveIfUnchanged(a, revA))
	err = store.SaveIfUnchanged(b, revB)
	var conflict *ConflictError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &conflict) {
		t.Fatalf("SaveIfUnchanged() error = %v, want a conflict", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Services["first"]; !ok {
		t.Error("the winning change was lost")
	}
	if _, ok := cfg.Services["second"]; ok {
		t.Error("the stale change was written")
	}
	assertNoTemp(t, filepath.Dir(path))
}

func TestSaveIfUnchangedAbsentRevision(t *testing.T) {
	store, _ := newTarget(t)
	cfg, rev, err := store.LoadVersioned()
	var nf *NotFoundError
	if !errors.As(err, &nf) || rev != RevisionAbsent || cfg != nil {
		t.Fatalf("LoadVersioned() = %v, %q, %v", cfg, rev, err)
	}
	must(t, store.SaveIfUnchanged(sample(t), rev))
	// A second creator based on the same absent file loses.
	if err := store.SaveIfUnchanged(sample(t), rev); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create error = %v, want a conflict", err)
	}
}

func TestSaveIfUnchangedRejectsInvalid(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	before, _ := os.ReadFile(path)
	cfg, rev, err := store.LoadVersioned()
	must(t, err)
	must(t, cfg.SetConnection("bad", Connection{Service: "missing", Credential: "reader"}))
	var invalid *InvalidError
	if err := store.SaveIfUnchanged(cfg, rev); !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidError", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("the file changed")
	}
}

func TestUpdateThroughSymlinkLocksRealTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := t.TempDir()
	realPath := filepath.Join(root, "dotfiles", "config.yaml")
	link := filepath.Join(root, "home", "config.yaml")
	real := NewStore(realPath, testProviders)
	must(t, real.Save(sample(t)))
	must(t, os.MkdirAll(filepath.Dir(link), 0o700))
	must(t, os.Symlink(realPath, link))

	viaLink := NewStore(link, testProviders)
	if viaLink.lockPath() != real.lockPath() {
		t.Fatalf("lock paths differ: %s vs %s", viaLink.lockPath(), real.lockPath())
	}
	must(t, viaLink.Update(func(c *Config) error {
		return c.SetService("extra", Service{Provider: "bookstack", BaseURL: "https://x.example.invalid"})
	}))
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v", err)
	}
	cfg, err := real.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Services["extra"]; !ok {
		t.Error("the change did not reach the real file")
	}
	if _, err := os.Stat(link + ".lock"); err == nil {
		t.Error("a lock file was created next to the symlink")
	}
}

func TestUpdateDetectsLockBypassingWriter(t *testing.T) {
	store, _ := newTarget(t)
	must(t, store.Save(sample(t)))
	err := store.Update(func(c *Config) error {
		other := sample(t)
		must(t, other.SetService("other", Service{Provider: "bookstack", BaseURL: "https://o.example.invalid"}))
		must(t, store.Save(other)) // a writer that ignores the lock
		return nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Update() error = %v, want a conflict", err)
	}
	cfg, _ := store.Load()
	if _, ok := cfg.Services["other"]; !ok {
		t.Error("the bypassing writer's change was overwritten")
	}
}

func TestConflictErrorHasNoContent(t *testing.T) {
	msg := (&ConflictError{Path: "/x/config.yaml"}).Error()
	if msg == "" {
		t.Fatal("empty message")
	}
}
