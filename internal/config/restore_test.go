package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBackupsListConfigBackupsNewestFirst(t *testing.T) {
	store, _ := newTarget(t)
	dir := store.backupDir()
	must(t, os.MkdirAll(dir, 0o700))
	for _, name := range []string{
		"config-20260101T000000Z-000.yaml",
		"config-20260103T000000Z-001.yaml",
		"config-20260103T000000Z-002-hand.yaml",
		"config-first-hand-20251201T000000Z-000.yaml",
		"credentials-20260105T000000Z-000.bak", // a vault copy
		"config-notes.yaml",
		"notes.txt",
	} {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	got, err := store.Backups()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"config-20260103T000000Z-002-hand.yaml", "config-20260103T000000Z-001.yaml",
		"config-20260101T000000Z-000.yaml", "config-first-hand-20251201T000000Z-000.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("Backups() = %v, want %v", got, want)
	}
	for i, b := range got {
		if b.Name != want[i] {
			t.Errorf("Backups()[%d] = %s, want %s", i, b.Name, want[i])
		}
	}
	if y, m, d := got[1].Time.Date(); y != 2026 || m != 1 || d != 3 {
		t.Errorf("time = %v", got[1].Time)
	}
	data, err := store.ReadBackup(got[0])
	if err != nil || string(data) != want[0] {
		t.Errorf("ReadBackup() = %q, %v", data, err)
	}
}

func TestBackupsOfAMissingDirectoryAreNone(t *testing.T) {
	store, _ := newTarget(t)
	if got, err := store.Backups(); err != nil || len(got) != 0 {
		t.Fatalf("Backups() = %v, %v", got, err)
	}
}

func TestRestoreBackupSavesTheCurrentFileAndUndoPutsItBack(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	saveN(t, store, 2)
	before := mustRead(t, path)
	backups, err := store.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatalf("Backups() = %v, %v", backups, err)
	}
	older, err := store.ReadBackup(backups[len(backups)-1])
	must(t, err)
	if bytes.Equal(older, before) {
		t.Fatal("the fixture needs a backup that differs from the file")
	}
	_, rev, err := store.Snapshot()
	must(t, err)

	restored, err := store.RestoreBackup(older, rev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, path), older) {
		t.Error("the file is not the restored backup")
	}
	if !bytes.Equal(mustRead(t, restored.Previous), before) {
		t.Error("the new backup does not hold the replaced file")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", fi.Mode().Perm())
		}
	}
	count := len(backupNames(t, store))
	must(t, store.UndoRestore(restored))
	if !bytes.Equal(mustRead(t, path), before) {
		t.Error("UndoRestore did not put the file back")
	}
	if got := len(backupNames(t, store)); got != count {
		t.Errorf("UndoRestore made a backup: %d -> %d", count, got)
	}
}

func TestRestoreBackupRefusesAChangedFile(t *testing.T) {
	store, path := newTarget(t)
	must(t, store.Save(sample(t)))
	_, rev, err := store.Snapshot()
	must(t, err)
	saveN(t, store, 1)
	changed := mustRead(t, path)
	names := backupNames(t, store)
	if _, err := store.RestoreBackup([]byte("version: 4\n"), rev); !errors.Is(err, ErrConflict) {
		t.Fatalf("RestoreBackup() error = %v, want a conflict", err)
	}
	if !bytes.Equal(mustRead(t, path), changed) || len(backupNames(t, store)) != len(names) {
		t.Error("a refused restore changed something")
	}
}

func TestSnapshotOfAMissingFile(t *testing.T) {
	store, _ := newTarget(t)
	var nf *NotFoundError
	if _, _, err := store.Snapshot(); !errors.As(err, &nf) {
		t.Fatalf("Snapshot() error = %v", err)
	}
}
