package vault

import (
	"errors"
	"path/filepath"
	"reflect"
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
	widened.Permissions = append(widened.Permissions, "delete")
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
