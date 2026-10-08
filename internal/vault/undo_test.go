package vault

import (
	"os"
	"testing"
)

func mustGet(t *testing.T, v *Vault, name, role string) (string, bool) {
	t.Helper()
	value, found, _, err := v.Get(name, role, nil)
	if err != nil {
		t.Fatalf("Get(%q, %q) = %v", name, role, err)
	}
	return value, found
}

func TestSetUndoableAbsentRemovesTheRole(t *testing.T) {
	v := New(t.TempDir())
	undo, err := v.SetUndoable("c", "r", "value", nil)
	if err != nil {
		t.Fatalf("SetUndoable() = %v", err)
	}
	if err := undo(); err != nil {
		t.Fatalf("undo() = %v", err)
	}
	if _, found := mustGet(t, v, "c", "r"); found {
		t.Error("the role survived the undo of the vault's first write")
	}
}

func TestSetUndoableUnencryptedRestoresOrRemoves(t *testing.T) {
	v := New(t.TempDir())
	if err := v.Set("c", "old", "old-value", nil); err != nil {
		t.Fatal(err)
	}
	undoOld, err := v.SetUndoable("c", "old", "new-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	undoNew, err := v.SetUndoable("c", "fresh", "fresh-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := undoOld(); err != nil {
		t.Fatal(err)
	}
	if err := undoNew(); err != nil {
		t.Fatal(err)
	}
	if value, found := mustGet(t, v, "c", "old"); !found || value != "old-value" {
		t.Errorf("old = %q, %v, want its previous value", value, found)
	}
	if _, found := mustGet(t, v, "c", "fresh"); found {
		t.Error("a role without a previous value survived the undo")
	}
}

func TestSetUndoableUnlockedRestoresOrRemoves(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("c", "old", "old-value", offering("phrase")); err != nil {
		t.Fatal(err)
	}
	undoOld, err := v.SetUndoable("c", "old", "new-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	undoNew, err := v.SetUndoable("c", "fresh", "fresh-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := undoOld(); err != nil {
		t.Fatal(err)
	}
	if err := undoNew(); err != nil {
		t.Fatal(err)
	}
	// A second handle on the same files sees what was written to disk.
	again := New(dir)
	if _, err := again.Unlock("phrase"); err != nil {
		t.Fatal(err)
	}
	if value, found := mustGet(t, again, "c", "old"); !found || value != "old-value" {
		t.Errorf("old = %q, %v, want its previous value", value, found)
	}
	if _, found := mustGet(t, again, "c", "fresh"); found {
		t.Error("a role without a previous value survived the undo")
	}
}

func TestSetUndoableLockedRemovesOnlyItsPendingFile(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	if err := New(dir).Set("c", "old", "old-value", offering("phrase")); err != nil {
		t.Fatal(err)
	}
	locked := New(dir)
	secretsBefore, err := os.ReadFile(locked.secretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := locked.Set("c", "other", "other-value", nil); err != nil {
		t.Fatal(err)
	}
	undo, err := locked.SetUndoable("c", "old", "new-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := locked.Status(); status.Pending != 2 {
		t.Fatalf("Pending = %d, want 2", status.Pending)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if status, _ := locked.Status(); status.Pending != 1 {
		t.Fatalf("Pending after undo = %d, want only the earlier entry", status.Pending)
	}
	secretsAfter, err := os.ReadFile(locked.secretsPath())
	if err != nil || string(secretsAfter) != string(secretsBefore) {
		t.Errorf("secrets.age changed: %v", err)
	}
	if _, err := locked.Unlock("phrase"); err != nil {
		t.Fatal(err)
	}
	if value, _ := mustGet(t, locked, "c", "old"); value != "old-value" {
		t.Errorf("old = %q, want its previous value", value)
	}
	if value, _ := mustGet(t, locked, "c", "other"); value != "other-value" {
		t.Errorf("other = %q, want the earlier pending value", value)
	}
}
