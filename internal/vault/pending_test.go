package vault

import (
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
