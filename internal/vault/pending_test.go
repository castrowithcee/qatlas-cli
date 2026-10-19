package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialIDSurvivesPendingMergeAndUnlock(t *testing.T) {
	lowWorkFactor(t)
	const credID = "0123456789abcdef0123456789abcdef"
	dir := t.TempDir()
	created := New(dir)
	if err := created.Set("other", "token", "x", offering("phrase-1")); err != nil {
		t.Fatal(err)
	}

	// Two locked writes to a new credential, merged in random file order: an id-less one never clears the id.
	locked := New(dir)
	if _, err := locked.SetUndoableWithID("reader", credID, "token", "s3cret", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := locked.SetUndoableWithID("reader", "", "more", "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Unlock("phrase-1"); err != nil {
		t.Fatal(err)
	}

	entry, ok := locked.doc.byName("reader")
	if !ok || entry.CredentialID != credID {
		t.Fatalf("entry = %+v, want credential id %s", entry, credID)
	}
	if other, _ := locked.doc.byName("other"); other.CredentialID != "" {
		t.Errorf("an entry written without an id has %q", other.CredentialID)
	}
}

func TestSetRoleKeepsFirstCredentialID(t *testing.T) {
	doc := newDocument()
	doc.setRole("r", "id1", "a", "1", time.Now())
	doc.setRole("r", "", "b", "2", time.Now())
	doc.setRole("r", "id2", "c", "3", time.Now())
	if e, _ := doc.byName("r"); e.CredentialID != "id1" {
		t.Errorf("CredentialID = %q, want id1", e.CredentialID)
	}
}

func TestRecordCredentialIDsInEveryState(t *testing.T) {
	lowWorkFactor(t)
	const idReader, idOther = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
	ids := map[string]string{"reader": idReader, "missing": idOther}

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		v := New(dir)
		n, undo, err := v.RecordCredentialIDs(ids)
		if err != nil || n != 0 || undo == nil {
			t.Fatalf("RecordCredentialIDs() = %d, %v", n, err)
		}
		if _, err := os.Stat(v.Dir()); !os.IsNotExist(err) {
			t.Errorf("an absent vault was created: %v", err)
		}
	})

	t.Run("unencrypted", func(t *testing.T) {
		v := New(t.TempDir())
		if err := v.Set("reader", "token", "x", nil); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(v.plainPath())
		n, undo, err := v.RecordCredentialIDs(ids)
		if err != nil || n != 1 {
			t.Fatalf("RecordCredentialIDs() = %d, %v, want 1", n, err)
		}
		doc, _ := loadPlainDocument(v.plainPath())
		if e, _ := doc.byName("reader"); e.CredentialID != idReader || e.Roles["token"] != "x" {
			t.Errorf("entry = %+v", e)
		}
		if _, ok := doc.byName("missing"); ok {
			t.Error("an entry was created for a name the vault does not hold")
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		if after, _ := os.ReadFile(v.plainPath()); !bytes.Equal(before, after) {
			t.Errorf("undo left\n%s\nwant\n%s", after, before)
		}
	})

	t.Run("unlocked", func(t *testing.T) {
		v := New(t.TempDir())
		if err := v.Set("reader", "token", "x", offering("phrase")); err != nil {
			t.Fatal(err)
		}
		n, undo, err := v.RecordCredentialIDs(ids)
		if err != nil || n != 1 {
			t.Fatalf("RecordCredentialIDs() = %d, %v, want 1", n, err)
		}
		reopened := New(filepath.Dir(v.Dir()))
		if _, err := reopened.Unlock("phrase"); err != nil {
			t.Fatal(err)
		}
		if e, _ := reopened.doc.byName("reader"); e.CredentialID != idReader {
			t.Errorf("entry = %+v", e)
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		again := New(filepath.Dir(v.Dir()))
		if _, err := again.Unlock("phrase"); err != nil {
			t.Fatal(err)
		}
		if e, _ := again.doc.byName("reader"); e.CredentialID != "" {
			t.Errorf("undo kept %q", e.CredentialID)
		}
	})

	t.Run("locked", func(t *testing.T) {
		dir := t.TempDir()
		if err := New(dir).Set("reader", "token", "x", offering("phrase")); err != nil {
			t.Fatal(err)
		}
		secretsBefore, _ := os.ReadFile(New(dir).secretsPath())
		seen := New(dir)
		if _, err := seen.Unlock("phrase"); err != nil {
			t.Fatal(err)
		}
		modified, _ := seen.doc.byName("reader")
		locked := New(dir)
		n, undo, err := locked.RecordCredentialIDs(ids)
		if err != nil || n != 2 {
			t.Fatalf("RecordCredentialIDs() = %d, %v, want 2 pending writes", n, err)
		}
		if after, _ := os.ReadFile(locked.secretsPath()); !bytes.Equal(secretsBefore, after) {
			t.Error("a locked vault's document was written")
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		if names, _ := locked.pendingFiles(); len(names) != 0 {
			t.Fatalf("undo left pending files %v", names)
		}

		if _, _, err := locked.RecordCredentialIDs(ids); err != nil {
			t.Fatal(err)
		}
		probe := New(dir)
		if _, err := probe.Unlock("phrase"); err != nil {
			t.Fatal(err)
		}
		if e, _ := probe.doc.byName("reader"); e.CredentialID != idReader || e.Roles["token"] != "x" || !e.Modified.Equal(modified.Modified) {
			t.Errorf("entry after unlock = %+v", e)
		}
		if _, ok := probe.doc.byName("missing"); ok {
			t.Error("an id-only pending write created an entry")
		}
	})
}

// A pending secret and a pending id of one new credential merge into one entry, whatever the file order.
func TestIDOnlyPendingFindsAPendingSecretOfTheSameName(t *testing.T) {
	lowWorkFactor(t)
	const credID = "0123456789abcdef0123456789abcdef"
	dir := t.TempDir()
	if err := New(dir).Set("other", "token", "x", offering("phrase")); err != nil {
		t.Fatal(err)
	}
	locked := New(dir)
	for i := 0; i < 5; i++ {
		if _, _, err := locked.RecordCredentialIDs(map[string]string{"fresh": credID}); err != nil {
			t.Fatal(err)
		}
		if err := locked.Set("fresh", "token", "s3cret", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := locked.Unlock("phrase"); err != nil {
		t.Fatal(err)
	}
	if e, ok := locked.doc.byName("fresh"); !ok || e.CredentialID != credID || e.Roles["token"] != "s3cret" {
		t.Fatalf("entry = %+v, %v", e, ok)
	}
}
