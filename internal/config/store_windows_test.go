//go:build windows

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The handles below are held without FILE_SHARE_DELETE (os.Open) or without any sharing at all, which is
// what a concurrent reader or replacer of config.yaml looks like to this process.

func holdOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func holdExclusive(t *testing.T, path string) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// releaseAfter calls release after d, and again at cleanup in case the test ends first.
func releaseAfter(t *testing.T, d time.Duration, release func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		time.Sleep(d)
		release()
		close(done)
	}()
	t.Cleanup(func() { <-done })
}

func savedStore(t *testing.T) (*Store, string) {
	t.Helper()
	store, path := newTarget(t)
	if err := store.Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func TestSaveWaitsForConcurrentReader(t *testing.T) {
	store, path := savedStore(t)
	f := holdOpen(t, path)
	releaseAfter(t, 200*time.Millisecond, func() { _ = f.Close() })

	cfg := sample(t)
	if err := cfg.SetService("second", Service{Provider: "bookstack", BaseURL: "https://second.example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path, testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Services["second"]; !ok {
		t.Error("the new content was not saved")
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestReadsWaitForExclusiveHandle(t *testing.T) {
	store, path := savedStore(t)
	_, rev, err := store.LoadVersioned()
	if err != nil {
		t.Fatal(err)
	}

	hold := func(t *testing.T) {
		h := holdExclusive(t, path)
		releaseAfter(t, 200*time.Millisecond, func() { _ = windows.CloseHandle(h) })
	}

	t.Run("Load", func(t *testing.T) {
		hold(t)
		if _, err := Load(path, testProviders); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("LoadVersioned", func(t *testing.T) {
		hold(t)
		if _, _, err := store.LoadVersioned(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("SaveIfUnchanged", func(t *testing.T) {
		hold(t)
		if err := store.SaveIfUnchanged(sample(t), rev); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSaveFailsCleanlyWhenFileStaysOpen(t *testing.T) {
	store, path := savedStore(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldSleep := sleepFile
	t.Cleanup(func() { sleepFile = oldSleep })
	sleepFile = func(d time.Duration) { time.Sleep(d / 100) }

	f := holdOpen(t, path)
	cfg := sample(t)
	if err := cfg.SetService("second", Service{Provider: "bookstack", BaseURL: "https://second.example.invalid"}); err != nil {
		t.Fatal(err)
	}
	saveErr := store.Save(cfg)
	_ = f.Close()

	if saveErr == nil {
		t.Fatal("Save succeeded although the file stayed open")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the target changed")
	}
	noTempFiles(t, filepath.Dir(path))
}

func TestBackupACLAllowsOnlyTheUser(t *testing.T) {
	store, _ := savedStore(t)
	cfg := sample(t)
	if err := cfg.SetDefault("other", "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.backupDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v, %v", entries, err)
	}
	for _, path := range []string{store.backupDir(), filepath.Join(store.backupDir(), entries[0].Name())} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, _ := sd.Control()
		dacl, _, err := sd.DACL()
		if err != nil || dacl == nil {
			t.Fatalf("%s: dacl = %v, %v", path, dacl, err)
		}
		if control&windows.SE_DACL_PROTECTED == 0 || dacl.AceCount != 1 {
			t.Errorf("%s: protected = %v, aces = %d, want protected with one entry", path,
				control&windows.SE_DACL_PROTECTED != 0, dacl.AceCount)
			continue
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !sid.Equals(user.User.Sid) {
			t.Errorf("%s: entry is for %s, want the current user", path, sid)
		}
	}
}
