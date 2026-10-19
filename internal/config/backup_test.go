package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T) {
	t.Helper()
	old := backupNow
	backupNow = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { backupNow = old })
}

func backupNames(t *testing.T, store *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(store.backupDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// saveN saves a changed configuration n times, each save backing up the previous one.
func saveN(t *testing.T, store *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		cfg, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		must(t, cfg.SetDefault("knowledge", "wiki"))
		must(t, cfg.SetProviderNote("bookstack", strings.Repeat("n", i+1)))
		if err := store.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFirstSaveHasNothingToBackUp(t *testing.T) {
	store, _ := newTarget(t)
	must(t, store.Save(sample(t)))
	if names := backupNames(t, store); len(names) != 0 {
		t.Fatalf("backups = %v, want none", names)
	}
}

func TestBackupsInTheSameSecondAreDistinctAndPrivate(t *testing.T) {
	fixedClock(t)
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	first := mustRead(t, path)
	saveN(t, store, 1)
	second := mustRead(t, path)
	saveN(t, store, 1)

	names := backupNames(t, store)
	if len(names) != 2 {
		t.Fatalf("backups = %v, want 2", names)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(store.backupDir(), names[0])), first) ||
		!bytes.Equal(mustRead(t, filepath.Join(store.backupDir(), names[1])), second) {
		t.Error("backups do not hold the bytes that were replaced, in creation order")
	}
	if runtime.GOOS != "windows" {
		for _, n := range names {
			if fi, _ := os.Stat(filepath.Join(store.backupDir(), n)); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode = %o, want 600", n, fi.Mode().Perm())
			}
		}
		if fi, _ := os.Stat(store.backupDir()); fi.Mode().Perm() != 0o700 {
			t.Errorf("backups mode = %o, want 700", fi.Mode().Perm())
		}
	}
}

func TestBackupsRotateAfterTen(t *testing.T) {
	fixedClock(t)
	store, _ := newTarget(t)
	must(t, store.Save(sample(t)))
	saveN(t, store, 15)
	names := backupNames(t, store)
	if len(names) != 10 {
		t.Fatalf("backups = %v, want 10", names)
	}
	if !strings.HasSuffix(names[9], "-014.yaml") || !strings.HasSuffix(names[0], "-005.yaml") {
		t.Errorf("rotation did not keep the newest ten: %v", names)
	}
}

func TestFirstHandVersionSurvivesRotation(t *testing.T) {
	fixedClock(t)
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	hand := append(mustRead(t, path), "# my note\n"...)
	must(t, os.WriteFile(path, hand, 0o600))
	saveN(t, store, 15)

	var firsts, hands []string
	for _, n := range backupNames(t, store) {
		switch {
		case strings.HasPrefix(n, firstHandPrefix):
			firsts = append(firsts, n)
		case strings.Contains(n, handSuffix):
			hands = append(hands, n)
		}
	}
	if len(firsts) != 1 || !bytes.Equal(mustRead(t, filepath.Join(store.backupDir(), firsts[0])), hand) {
		t.Fatalf("first hand version = %v", firsts)
	}

	// A later hand edit is kept as a normal backup, not as another exempt one.
	later := append(mustRead(t, path), "# again\n"...)
	must(t, os.WriteFile(path, later, 0o600))
	saveN(t, store, 1)
	firsts, hands = nil, nil
	for _, n := range backupNames(t, store) {
		if strings.HasPrefix(n, firstHandPrefix) {
			firsts = append(firsts, n)
		} else if strings.Contains(n, handSuffix) {
			hands = append(hands, n)
		}
	}
	if len(firsts) != 1 || len(hands) != 1 {
		t.Fatalf("firsts = %v, hands = %v, want one each", firsts, hands)
	}
	if n := len(backupNames(t, store)); n != 11 {
		t.Errorf("backups = %d, want 10 rotating plus the first hand version", n)
	}
}

func TestRenamedIDsAreNotAHandVersion(t *testing.T) {
	fixedClock(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := NewStore(path, renameProviders)
	cfg := store.New()
	must(t, cfg.SetService("svc", Service{Provider: "acme.chat", BaseURL: "https://example.invalid"}))
	must(t, cfg.SetCredential("cred", Credential{Type: CredentialTypeEnv, Provider: "acme.chat",
		Values: map[string]string{"token": "ACME_TOKEN"}}))
	must(t, cfg.SetConnection("conn", Connection{Service: "svc", Credential: "cred",
		Tools: []string{"acme.chat.messages.get"}}))
	must(t, cfg.SetProviderNote("acme.chat", "team chat"))
	must(t, store.Save(cfg))

	old := strings.NewReplacer("provider: acme.chat", "provider: acmechat",
		"acme.chat.messages.get", "acmechat.messages.fetch", "acme.chat: team chat", "acmechat: team chat").
		Replace(string(mustRead(t, path)))
	if strings.Count(old, "acmechat") != 4 {
		t.Fatalf("test file lacks the former names:\n%s", old)
	}
	must(t, os.WriteFile(path, []byte(old), 0o600))

	loaded, err := store.Load()
	if err != nil || len(loaded.Renamings()) == 0 {
		t.Fatalf("Load() = %v, %v", loaded, err)
	}
	must(t, store.Save(loaded))
	names := backupNames(t, store)
	if len(names) != 1 || strings.Contains(names[0], "hand") {
		t.Fatalf("backups = %v, want one ordinary backup", names)
	}

	// Renamed IDs plus a hand edit still count as a hand version.
	must(t, os.WriteFile(path, []byte(old+"# note\n"), 0o600))
	must(t, store.Save(loaded))
	if names := backupNames(t, store); len(names) != 2 || !strings.HasPrefix(names[1], firstHandPrefix) {
		t.Fatalf("backups = %v, want a first hand version", names)
	}
}

func TestFailedBackupLeavesConfigUntouched(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	before := mustRead(t, path)
	must(t, os.WriteFile(store.backupDir(), []byte("in the way"), 0o600))

	cfg := sample(t)
	must(t, cfg.SetDefault("other", "wiki"))
	if err := store.Save(cfg); err == nil {
		t.Fatal("Save succeeded without a backup")
	}
	if !bytes.Equal(mustRead(t, path), before) {
		t.Error("the configuration changed although the backup failed")
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestConflictMakesNoBackup(t *testing.T) {
	store, _ := newTarget(t)
	must(t, store.Save(sample(t)))
	cfg, rev, err := store.LoadVersioned()
	must(t, err)
	saveN(t, store, 1)
	before := backupNames(t, store)

	err = store.SaveIfUnchanged(cfg, rev)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if after := backupNames(t, store); len(after) != len(before) {
		t.Errorf("backups = %v, want %v", after, before)
	}
}

func TestSymlinkedConfigKeepsBackupsBesideTheConfiguredPath(t *testing.T) {
	root := t.TempDir()
	realDir, linkDir := filepath.Join(root, "dotfiles"), filepath.Join(root, "home")
	must(t, os.MkdirAll(realDir, 0o700))
	must(t, os.MkdirAll(linkDir, 0o700))
	link := filepath.Join(linkDir, "config.yaml")
	if err := os.Symlink(filepath.Join(realDir, "config.yaml"), link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	store := NewStore(link, testProviders)
	must(t, store.Save(sample(t)))
	before := mustRead(t, link)
	saveN(t, store, 1)

	names := backupNames(t, store)
	if len(names) != 1 || !bytes.Equal(mustRead(t, filepath.Join(linkDir, backupDirName, names[0])), before) {
		t.Fatalf("backups = %v", names)
	}
	if _, err := os.Stat(filepath.Join(realDir, backupDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backups were written beside the link target: %v", err)
	}
}

func TestBackupCopyIsExactPrivateAndNotRotated(t *testing.T) {
	fixedClock(t)
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	src := filepath.Join(filepath.Dir(path), "secrets.age")
	payload := []byte{0, 1, 2, 0xff, '\n'}
	must(t, os.WriteFile(src, payload, 0o600))

	a, err := store.BackupCopy(src)
	must(t, err)
	b, err := store.BackupCopy(src)
	must(t, err)
	if a == b {
		t.Fatal("two copies share a name")
	}
	if filepath.Dir(a) != store.backupDir() || !bytes.Equal(mustRead(t, a), payload) {
		t.Errorf("copy %s is not an exact copy in the backups directory", a)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(a); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 600", fi.Mode().Perm())
		}
	}
	saveN(t, store, 15)
	if _, err := os.Stat(a); err != nil {
		t.Errorf("rotation removed a copy: %v", err)
	}
	if _, err := store.BackupCopy(filepath.Join(filepath.Dir(path), "missing")); err == nil {
		t.Error("copying a missing file succeeded")
	}
}
