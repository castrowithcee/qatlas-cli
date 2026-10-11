//go:build windows

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

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
