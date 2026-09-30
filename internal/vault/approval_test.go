package vault

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func wikiScope() Scope {
	return Scope{
		Connection: "wiki", Credential: "wiki-reader", Provider: "bookstack",
		Origin: "https://wiki.example.test", Permissions: []string{"read", "create"}, Targets: []string{"b", "a"},
	}
}

// The fingerprint is stable under the order of lists that carry none, and changes with every field that
// decides where a secret goes and what it may do there, including the credential entry itself.
func TestFingerprintCoversTheScope(t *testing.T) {
	base := Fingerprint(wikiScope(), "id-1")
	if base != Fingerprint(wikiScope(), "id-1") || len(base) != 64 {
		t.Fatalf("Fingerprint() is not a stable sha256: %q", base)
	}
	reordered := wikiScope()
	reordered.Permissions = []string{"create", "read"}
	reordered.Targets = []string{"a", "b"}
	if Fingerprint(reordered, "id-1") != base {
		t.Errorf("reordering permissions and targets changed the fingerprint")
	}
	renamed := wikiScope()
	renamed.Connection = "other"
	if Fingerprint(renamed, "id-1") != base {
		t.Errorf("the connection name changed the fingerprint; approvals are looked up by it instead")
	}

	changes := map[string]func(*Scope){
		"credential":  func(s *Scope) { s.Credential = "other-reader" },
		"provider":    func(s *Scope) { s.Provider = "nextcloud" },
		"origin":      func(s *Scope) { s.Origin = "https://wiki.example.test/other" },
		"permissions": func(s *Scope) { s.Permissions = []string{"read"} },
		"targets":     func(s *Scope) { s.Targets = []string{"a"} },
		"tools empty": func(s *Scope) { s.Tools = []string{} },
		"tools named": func(s *Scope) { s.Tools = []string{"bookstack.pages.list"} },
		"paths bound": func(s *Scope) { s.Paths = []string{"~/repos/kunde-a"} },
	}
	for name, change := range changes {
		s := wikiScope()
		change(&s)
		if Fingerprint(s, "id-1") == base {
			t.Errorf("a changed %s left the fingerprint as it was", name)
		}
	}
	// A connection bound to no path keeps the fingerprint it had before paths existed, whether the list is
	// missing or empty; a bound one is stable under the order of its paths and changes with each of them.
	unbound := wikiScope()
	unbound.Paths = []string{}
	if Fingerprint(unbound, "id-1") != base {
		t.Errorf("an empty paths list changed the fingerprint of an unbound connection")
	}
	bound, reversed, moved := wikiScope(), wikiScope(), wikiScope()
	bound.Paths = []string{"/repos/a", "/repos/b"}
	reversed.Paths = []string{"/repos/b", "/repos/a"}
	moved.Paths = []string{"/repos/a", "/repos/c"}
	if Fingerprint(bound, "id-1") != Fingerprint(reversed, "id-1") {
		t.Errorf("reordering paths changed the fingerprint")
	}
	if Fingerprint(bound, "id-1") == Fingerprint(moved, "id-1") {
		t.Errorf("a changed path left the fingerprint as it was")
	}
	if Fingerprint(wikiScope(), "id-2") == base {
		t.Errorf("another credential entry left the fingerprint as it was")
	}
}

// Approvals live in the encrypted document: they survive the next unlock, reach a vault process through the
// snapshot, and stop matching once the credential entry is removed and stored anew under the same name.
func TestApproveCheckAndRevoke(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token", "synthetic-token", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	scope := wikiScope()
	if err := v.CheckApproval(scope); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("CheckApproval() before Approve() = %v, want ErrApprovalRequired", err)
	}
	if err := v.Approve([]Scope{scope}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	if err := v.CheckApproval(scope); err != nil {
		t.Fatalf("CheckApproval() after Approve() = %v", err)
	}
	orphan := Scope{Connection: "crm", Credential: "crm-key"}
	if err := v.Approve([]Scope{orphan}); err == nil {
		t.Errorf("Approve() of a credential without an entry succeeded")
	}

	next := New(dir)
	if err := next.CheckApproval(scope); !errors.Is(err, ErrNotUnlocked) {
		t.Errorf("CheckApproval() while locked = %v, want ErrNotUnlocked", err)
	}
	if _, err := next.Unlock("s3cret-phrase"); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	if err := next.CheckApproval(scope); err != nil {
		t.Errorf("CheckApproval() after the next unlock = %v", err)
	}
	snap, err := next.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if !snap.Bindings.Allows(scope, "wiki-reader") || snap.Bindings.Allows(scope, "crm-key") {
		t.Errorf("Snapshot().Bindings = %+v, want the approval of wiki for wiki-reader alone", snap.Bindings)
	}
	approvals, err := next.Approvals()
	if err != nil || approvals["wiki"].Scope.Origin != scope.Origin || approvals["wiki"].CredentialID == "" {
		t.Errorf("Approvals() = %+v, %v, want the approved scope and entry", approvals, err)
	}

	// Removing the last role removes the entry; storing it again makes a new one, with a new id.
	if _, err := next.Delete("wiki-reader", "token", nil); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if err := next.Set("wiki-reader", "token", "synthetic-token-2", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := next.CheckApproval(scope); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApproval() of a credential stored anew = %v, want ErrApprovalRequired", err)
	}

	if removed, err := next.Revoke([]string{"wiki", "unknown"}); err != nil || removed != 1 {
		t.Errorf("Revoke() = %d, %v, want 1, nil", removed, err)
	}
	if approvals, _ := next.Approvals(); len(approvals) != 0 {
		t.Errorf("Approvals() after Revoke() = %+v, want none", approvals)
	}
}

// An unencrypted vault binds no connection: it keeps no approvals, turning encryption off drops them, and a
// plaintext document that carries some is never trusted when encryption is turned on again.
func TestApprovalsExistOnlyWhileEncrypted(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token", "synthetic-token", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := v.Approve([]Scope{wikiScope()}); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("Approve() on an unencrypted vault = %v, want ErrNotEncrypted", err)
	}
	if err := v.Encrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Encrypt() = %v", err)
	}
	if err := v.Approve([]Scope{wikiScope()}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	if err := v.Decrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Decrypt() = %v", err)
	}
	plain, err := os.ReadFile(v.plainPath())
	if err != nil || strings.Contains(string(plain), "approvals") {
		t.Fatalf("secrets.json after Decrypt() = %q, %v, want no approvals", plain, err)
	}

	// A hand-written approval in the plaintext document is dropped on the way into encryption.
	id, _ := func() (string, bool) {
		doc, _ := loadPlainDocument(v.plainPath())
		e, ok := doc.byName("wiki-reader")
		return e.ID, ok
	}()
	planted := strings.Replace(string(plain), `"schema": 1,`, `"schema": 1, "approvals": {"wiki": {"fingerprint": "`+
		Fingerprint(wikiScope(), id)+`"}},`, 1)
	if planted == string(plain) {
		t.Fatalf("the approval was not planted")
	}
	if err := os.WriteFile(v.plainPath(), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.Encrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Encrypt() = %v", err)
	}
	if err := v.CheckApproval(wikiScope()); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApproval() after a planted plaintext approval = %v, want ErrApprovalRequired", err)
	}
}

// A scope without local file directories keeps the fingerprint it had before the field existed; the value
// was computed from the canonical form of that time.
func TestFingerprintWithoutFilesIsUnchanged(t *testing.T) {
	const want = "4e83b7a5c7201175c9e37bccbfb39049023b62d0e8d15ea98e0e2205ca0e928a"
	if got := Fingerprint(wikiScope(), "id-1"); got != want {
		t.Fatalf("Fingerprint() = %s, want %s", got, want)
	}
	empty := wikiScope()
	empty.FilesRead, empty.FilesWrite = []string{}, []string{}
	if got := Fingerprint(empty, "id-1"); got != want {
		t.Errorf("Fingerprint() with empty files = %s, want %s", got, want)
	}
}

func TestFingerprintCoversFiles(t *testing.T) {
	base := Fingerprint(wikiScope(), "id-1")
	with := func(read, write []string) string {
		s := wikiScope()
		s.FilesRead, s.FilesWrite = read, write
		return Fingerprint(s, "id-1")
	}
	if with([]string{"/a"}, nil) == base || with(nil, []string{"/a"}) == base {
		t.Error("a released directory does not change the fingerprint")
	}
	if with([]string{"/a"}, nil) == with(nil, []string{"/a"}) {
		t.Error("read and write are not told apart")
	}
	if with([]string{"/a", "/b"}, nil) != with([]string{"/b", "/a"}, nil) {
		t.Error("the order of directories changes the fingerprint")
	}
	if with([]string{"/a"}, nil) == with([]string{"/a", "/b"}, nil) {
		t.Error("another directory does not change the fingerprint")
	}
}
