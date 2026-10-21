package vault

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// vaultFiles reads the vault's own files and its pending writes, so a test can prove an undo byte for byte.
func vaultFiles(t *testing.T, v *Vault) map[string][]byte {
	t.Helper()
	out := filesOf(t, v.Dir())
	pending, err := v.pendingFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) > 0 {
		for name, data := range filesOf(t, v.pendingPath()) {
			out[filepath.Join(pendingDir, name)] = data
		}
	}
	return out
}

func renamedScope(s Scope, credential string) Scope {
	s.Credential = credential
	return s
}

// renameVault is an encrypted vault unlocked in this process with the entry wiki-reader, an approval of the
// connection wiki and an open one of widened, and pending writes another process queued for wiki-reader and
// for an unrelated credential.
func renameVault(t *testing.T) (*Vault, Scope) {
	t.Helper()
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token", "synthetic-token", offering("s3cret-phrase")); err != nil {
		t.Fatal(err)
	}
	widened := wikiScope()
	widened.Connection = "widened"
	if err := v.Approve([]Scope{wikiScope(), widened}); err != nil {
		t.Fatal(err)
	}
	other := New(dir)
	if err := other.Set("wiki-reader", "user", "synthetic-user", nil); err != nil {
		t.Fatal(err)
	}
	if err := other.Set("crm-key", "token", "synthetic-crm", nil); err != nil {
		t.Fatal(err)
	}
	widened.Effects = append(widened.Effects, "delete")
	return v, widened
}

func TestRenameCredentialRenamesTheEntryAndCarriesValidApprovals(t *testing.T) {
	v, widened := renameVault(t)
	idBefore, _, err := v.CredentialID("wiki-reader")
	if err != nil {
		t.Fatal(err)
	}
	approvalsBefore, _ := v.Approvals()
	filesBefore := vaultFiles(t, v)

	carried, undo, err := v.RenameCredential(CredentialRename{
		From: "wiki-reader", To: "reader", CredentialID: "c0ffee", Entry: true,
		Carry: []ScopeRename{
			{Before: wikiScope(), After: renamedScope(wikiScope(), "reader"), ConnectionID: "conn-id"},
			{Before: widened, After: renamedScope(widened, "reader"), ConnectionID: "widened-id"},
		},
	})
	if err != nil {
		t.Fatalf("RenameCredential() = %v", err)
	}
	if !reflect.DeepEqual(carried, []string{"wiki"}) {
		t.Errorf("carried = %v, want only the valid approval", carried)
	}
	if id, held, _ := v.CredentialID("reader"); !held || id != idBefore {
		t.Errorf("entry id = %q, %v, want %q kept", id, held, idBefore)
	}
	if held, _ := v.HasEntry("wiki-reader"); held {
		t.Error("the old name still holds an entry")
	}
	if len(v.doc.Entries) != 1 {
		t.Errorf("entries = %d, want the renamed one alone, nothing copied", len(v.doc.Entries))
	}
	if value, found, _, _ := v.Get("reader", "user", nil); !found || value != "synthetic-user" {
		t.Error("the pending write for the old name was not merged into the entry")
	}
	if entry, _ := v.doc.byName("reader"); entry.CredentialID != "c0ffee" {
		t.Errorf("credential id = %q", entry.CredentialID)
	}
	if err := v.CheckApproval(renamedScope(wikiScope(), "reader")); err != nil {
		t.Errorf("the carried approval does not hold: %v", err)
	}
	approvals, _ := v.Approvals()
	if a := approvals["wiki"]; a.ConnectionID != "conn-id" || a.CredentialID != idBefore || !a.Approved.Equal(approvalsBefore["wiki"].Approved) {
		t.Errorf("carried approval = %+v", a)
	}
	if !reflect.DeepEqual(approvals["widened"], approvalsBefore["widened"]) {
		t.Error("the open approval was touched")
	}
	pending, err := v.PendingWrites()
	if err != nil || !reflect.DeepEqual(pending, map[string]int{"crm-key": 1}) {
		t.Errorf("PendingWrites() = %v, %v, want the unrelated write left alone", pending, err)
	}

	if err := undo(); err != nil {
		t.Fatalf("undo() = %v", err)
	}
	if got := vaultFiles(t, v); !reflect.DeepEqual(got, filesBefore) {
		t.Error("undo did not put the vault's files back byte for byte")
	}
	if held, _ := v.HasEntry("wiki-reader"); !held {
		t.Error("undo did not put the entry back in this process")
	}
	if approvals, _ := v.Approvals(); !reflect.DeepEqual(approvals, approvalsBefore) {
		t.Error("undo did not put the approvals back in this process")
	}
}

func TestRenameCredentialRefusesATakenNameAndALockedVault(t *testing.T) {
	v, _ := renameVault(t)
	if err := v.Set("reader", "token", "synthetic-other", nil); err != nil {
		t.Fatal(err)
	}
	before := vaultFiles(t, v)
	for _, to := range []string{"reader", "crm-key"} {
		if _, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: to, Entry: true}); !errors.Is(err, ErrEntryTaken) {
			t.Errorf("RenameCredential(%s) = %v, want ErrEntryTaken", to, err)
		}
	}
	v.Forget()
	if _, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "spare", Entry: true}); !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("RenameCredential() while locked = %v, want ErrNotUnlocked", err)
	}
	if _, err := v.PendingWrites(); !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("PendingWrites() while locked = %v, want ErrNotUnlocked", err)
	}
	if got := vaultFiles(t, v); !reflect.DeepEqual(got, before) {
		t.Error("a refused rename changed the vault")
	}
}

func TestRenameCredentialInAnUnencryptedVault(t *testing.T) {
	v := New(t.TempDir())
	if err := v.Set("wiki-reader", "token", "synthetic-token", nil); err != nil {
		t.Fatal(err)
	}
	before := vaultFiles(t, v)
	_, undo, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", Entry: true})
	if err != nil {
		t.Fatal(err)
	}
	if value, found := mustGet(t, v, "reader", "token"); !found || value != "synthetic-token" {
		t.Error("the entry was not renamed")
	}
	if held, _ := v.HasEntry("wiki-reader"); held {
		t.Error("the old name still holds an entry")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if got := vaultFiles(t, v); !reflect.DeepEqual(got, before) {
		t.Error("undo did not put the vault back byte for byte")
	}
	if _, _, err := v.RenameCredential(CredentialRename{From: "reader", To: "wiki-reader", Entry: true}); !errors.Is(err, ErrEntryTaken) {
		t.Errorf("onto a held name = %v, want ErrEntryTaken", err)
	}
}

// A rename that stopped after the vault is finished by a second one that finds the entry under the new name:
// it merges what was queued for the old name since into it and carries what is still valid, but only for the
// credential the entry records.
func TestRenameCredentialResumesAStoppedRename(t *testing.T) {
	v, _ := renameVault(t)
	idBefore, _, _ := v.CredentialID("wiki-reader")
	if _, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", CredentialID: "c0ffee", Entry: true}); err != nil {
		t.Fatal(err)
	}
	if err := New(filepath.Dir(v.Dir())).Set("wiki-reader", "late", "synthetic-late", nil); err != nil {
		t.Fatal(err)
	}
	ids, err := v.EntryCredentialIDs()
	if err != nil || ids["reader"] != "c0ffee" {
		t.Fatalf("EntryCredentialIDs() = %v, %v", ids, err)
	}
	if _, held := ids["wiki-reader"]; held {
		t.Fatal("the old name still holds an entry")
	}
	before := vaultFiles(t, v)
	for _, id := range []string{"another", ""} {
		if _, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", CredentialID: id,
			Entry: true, Resume: true}); !errors.Is(err, ErrEntryTaken) {
			t.Errorf("resuming for credential id %q = %v, want ErrEntryTaken", id, err)
		}
	}
	if got := vaultFiles(t, v); !reflect.DeepEqual(got, before) {
		t.Error("a refused resume changed the vault")
	}

	carried, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", CredentialID: "c0ffee",
		Entry: true, Resume: true, Carry: []ScopeRename{
			{Before: wikiScope(), After: renamedScope(wikiScope(), "reader"), ConnectionID: "conn-id"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(carried, []string{"wiki"}) {
		t.Errorf("carried = %v, want the approval still valid for the old name", carried)
	}
	if value, found := mustGet(t, v, "reader", "late"); !found || value != "synthetic-late" {
		t.Error("the write queued for the old name was not merged into the entry")
	}
	if id, held, _ := v.CredentialID("reader"); !held || id != idBefore {
		t.Errorf("entry id = %q, %v, want %q", id, held, idBefore)
	}
	if held, _ := v.HasEntry("wiki-reader"); held {
		t.Error("the resume created an entry under the old name")
	}
	if pending, _ := v.PendingWrites(); !reflect.DeepEqual(pending, map[string]int{"crm-key": 1}) {
		t.Errorf("PendingWrites() = %v", pending)
	}
	v.Forget()
	if _, err := v.EntryCredentialIDs(); !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("EntryCredentialIDs() while locked = %v, want ErrNotUnlocked", err)
	}
}

// KnownNames reads an unencrypted vault without a passphrase, which has entries and no approvals, and an
// absent vault, which has neither.
func TestKnownNamesOfAnUnencryptedVault(t *testing.T) {
	v := New(t.TempDir())
	names, err := v.KnownNames()
	if err != nil || len(names.Entries) != 0 || len(names.Approvals) != 0 {
		t.Fatalf("KnownNames() of an absent vault = %+v, %v", names, err)
	}
	if err := v.Set("wiki-reader", "token", "synthetic-token", nil); err != nil {
		t.Fatal(err)
	}
	if state, _ := v.State(); state != StateUnencrypted {
		t.Skipf("Set() left the vault %v, not unencrypted", state)
	}
	names, err = v.KnownNames()
	if _, held := names.Entries["wiki-reader"]; err != nil || !held || len(names.Approvals) != 0 {
		t.Errorf("KnownNames() = %+v, %v, want the entry and no approval", names, err)
	}
}

// A rename with Mark leaves the old name on the entry in the same write, undo and a rename without Mark leave
// none, ClearRenameMarker removes it, and the mark is read by name without any secret.
func TestRenameCredentialMarksTheEntryUntilCleared(t *testing.T) {
	check := func(t *testing.T, v *Vault) {
		t.Helper()
		_, undo, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", CredentialID: "c0ffee",
			Entry: true, Mark: true})
		if err != nil {
			t.Fatal(err)
		}
		names, err := v.KnownNames()
		if err != nil || !reflect.DeepEqual(names.Renamed, map[string]string{"reader": "wiki-reader"}) {
			t.Fatalf("Renamed = %v, %v, want the old name by the new one", names.Renamed, err)
		}
		if err := v.ClearRenameMarker("reader"); err != nil {
			t.Fatal(err)
		}
		if err := v.ClearRenameMarker("reader"); err != nil {
			t.Fatalf("clearing a cleared mark = %v", err)
		}
		if err := v.ClearRenameMarker("absent"); err != nil {
			t.Fatalf("clearing an absent entry = %v", err)
		}
		if names, _ := v.KnownNames(); len(names.Renamed) != 0 || names.Entries["reader"] != "c0ffee" {
			t.Errorf("names after clearing = %+v", names)
		}
		if value, found := mustGet(t, v, "reader", "token"); !found || value != "synthetic-token" {
			t.Error("clearing the mark changed the secret")
		}
		_ = undo
	}
	t.Run("encrypted", func(t *testing.T) {
		v, _ := renameVault(t)
		check(t, v)
	})
	t.Run("unencrypted", func(t *testing.T) {
		v := New(t.TempDir())
		if err := v.Set("wiki-reader", "token", "synthetic-token", nil); err != nil {
			t.Fatal(err)
		}
		check(t, v)
	})
	t.Run("undo and unmarked", func(t *testing.T) {
		v, _ := renameVault(t)
		before := vaultFiles(t, v)
		_, undo, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", Entry: true, Mark: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		if got := vaultFiles(t, v); !reflect.DeepEqual(got, before) {
			t.Error("undo did not put the vault back byte for byte")
		}
		if _, _, err := v.RenameCredential(CredentialRename{From: "wiki-reader", To: "reader", Entry: true}); err != nil {
			t.Fatal(err)
		}
		if names, _ := v.KnownNames(); len(names.Renamed) != 0 {
			t.Errorf("Renamed = %v, want none without Mark", names.Renamed)
		}
	})
	t.Run("locked", func(t *testing.T) {
		v, _ := renameVault(t)
		v.Forget()
		if err := v.ClearRenameMarker("wiki-reader"); !errors.Is(err, ErrNotUnlocked) {
			t.Errorf("ClearRenameMarker() while locked = %v, want ErrNotUnlocked", err)
		}
	})
}

// A document written before the mark existed reads without it.
func TestEntryWithoutRenameMarkerLoads(t *testing.T) {
	var entry Entry
	if err := json.Unmarshal([]byte(`{"id":"a","name":"n","roles":{},"created":"2024-01-01T00:00:00Z",`+
		`"modified":"2024-01-01T00:00:00Z"}`), &entry); err != nil || entry.RenamedFrom != "" {
		t.Errorf("entry = %+v, %v", entry, err)
	}
	data, _ := json.Marshal(Entry{ID: "a", Name: "n"})
	if strings.Contains(string(data), "renamed_from") {
		t.Errorf("an unmarked entry is written as %s", data)
	}
}
