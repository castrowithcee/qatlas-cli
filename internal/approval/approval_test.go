package approval

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const passphrase = "synthetic-passphrase-7d2a"

// fixture returns a configuration with two vault connections, one keyring connection, and one vault
// connection whose credential the vault holds no secret for, and an encrypted vault unlocked in the test
// process holding the secrets of the first two.
func fixture(t *testing.T) (*config.Config, *vault.Vault) {
	t.Helper()
	cfg := config.New()
	cfg.Services["wiki"] = config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.test"}
	cfg.Credentials["reader"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Credentials["writer"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Credentials["empty"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Credentials["desk"] = config.Credential{Type: config.CredentialTypeKeyring}
	cfg.Connections["wiki-read"] = config.Connection{Service: "wiki", Credential: "reader"}
	cfg.Connections["wiki-write"] = config.Connection{Service: "wiki", Credential: "writer",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate}}
	cfg.Connections["wiki-empty"] = config.Connection{Service: "wiki", Credential: "empty"}
	cfg.Connections["wiki-desk"] = config.Connection{Service: "wiki", Credential: "desk"}

	v := vault.New(t.TempDir())
	if err := v.Set("reader", "token", "synthetic-reader", func(string) (string, error) { return passphrase, nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := v.Set("writer", "token", "synthetic-writer", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	return cfg, v
}

func openNames(report Report) []string {
	var names []string
	for _, change := range report.Open {
		names = append(names, change.Connection)
	}
	return names
}

// A vault without approvals, such as one encrypted before connections were bound to them, leaves every
// connection that reads a stored vault secret open; approving all of them closes the list.
func TestPendingAndApproveAll(t *testing.T) {
	cfg, v := fixture(t)
	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatalf("Pending() = %v", err)
	}
	if got := openNames(report); !reflect.DeepEqual(got, []string{"wiki-read", "wiki-write"}) {
		t.Fatalf("Pending().Open = %v, want the two vault connections with a stored secret", got)
	}
	for _, change := range report.Open {
		if !change.New || change.Before != nil || len(change.Fields) != 0 {
			t.Errorf("Pending() change %+v, want a new connection", change)
		}
	}

	approved, warning, err := Approve(context.Background(), cfg, v, nil)
	if err != nil || warning != "" || !reflect.DeepEqual(approved, []string{"wiki-read", "wiki-write"}) {
		t.Fatalf("Approve() = %v, %q, %v", approved, warning, err)
	}
	if report, err := Pending(cfg, v); err != nil || len(report.Open) != 0 || len(report.Stale) != 0 {
		t.Fatalf("Pending() after Approve() = %+v, %v, want nothing open", report, err)
	}
	// The approvals are in the vault, not just in this process.
	next := vault.New(filepath.Dir(v.Dir()))
	if _, err := next.Unlock(passphrase); err != nil {
		t.Fatal(err)
	}
	if report, err := Pending(cfg, next); err != nil || len(report.Open) != 0 {
		t.Fatalf("Pending() after the next unlock = %+v, %v", report, err)
	}
}

// A changed connection is reported with every field that differs, before and after; approving it by name
// approves that one alone.
func TestPendingNamesWhatChanged(t *testing.T) {
	cfg, v := fixture(t)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	cfg.Services["wiki"] = config.Service{Provider: "bookstack", BaseURL: "http://127.0.0.1:9"}
	write := cfg.Connections["wiki-write"]
	write.Permissions = append(write.Permissions, config.PermissionDelete)
	write.Tools = []string{}
	write.Targets = []string{"shelf-2", "shelf-1"}
	write.Paths = []string{"~/repos/kunde-a"}
	write.Files = config.Files{Read: []string{"~/in"}, Write: []string{"~/out"}}
	cfg.Connections["wiki-write"] = write

	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	if got := openNames(report); !reflect.DeepEqual(got, []string{"wiki-read", "wiki-write"}) {
		t.Fatalf("Pending().Open = %v", got)
	}
	want := []FieldChange{
		{Field: FieldOrigin, Before: "https://wiki.example.test", After: "http://127.0.0.1:9"},
		{Field: FieldPermissions, Before: "create read", After: "create delete read",
			Added: []string{"delete"}, Kept: []string{"create", "read"}},
		{Field: FieldTargets, Before: "(none)", After: "shelf-1 shelf-2", Added: []string{"shelf-1", "shelf-2"}},
		{Field: FieldTools, Before: "(every tool the permissions allow)", After: "(none)"},
		{Field: FieldPaths, Before: "(every project)", After: "~/repos/kunde-a"},
		{Field: FieldFiles, Before: "(no local files)", After: "read: ~/in; write: ~/out"},
	}
	if got := report.Open[1].Fields; report.Open[1].New || !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending() fields of wiki-write = %+v, want %+v", got, want)
	}
	if report.Open[1].Before == nil || report.Open[1].Before.Origin != "https://wiki.example.test" {
		t.Errorf("Pending() before of wiki-write = %+v, want the approved scope", report.Open[1].Before)
	}

	approved, _, err := Approve(context.Background(), cfg, v, []string{"wiki-write", "wiki-write"})
	if err != nil || !reflect.DeepEqual(approved, []string{"wiki-write"}) {
		t.Fatalf("Approve(wiki-write) = %v, %v", approved, err)
	}
	if report, _ := Pending(cfg, v); !reflect.DeepEqual(openNames(report), []string{"wiki-read"}) {
		t.Errorf("Pending() after approving one = %v, want the other still open", openNames(report))
	}

	for _, name := range []string{"wiki-empty", "wiki-desk", "unknown"} {
		if _, _, err := Approve(context.Background(), cfg, v, []string{name}); !errors.Is(err, ErrUnknownConnection) {
			t.Errorf("Approve(%s) = %v, want ErrUnknownConnection", name, err)
		}
	}
}

// A credential stored anew under the same name is reported as a changed credential.
func TestPendingNamesACredentialStoredAnew(t *testing.T) {
	cfg, v := fixture(t)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Delete("reader", "token", nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("reader", "token", "synthetic-reader-2", nil); err != nil {
		t.Fatal(err)
	}
	report, err := Pending(cfg, v)
	if err != nil || len(report.Open) != 1 {
		t.Fatalf("Pending() = %+v, %v", report, err)
	}
	want := []FieldChange{{Field: FieldCredential, Before: "reader", After: "reader (stored anew)"}}
	if !reflect.DeepEqual(report.Open[0].Fields, want) {
		t.Errorf("Pending() fields = %+v, want %+v", report.Open[0].Fields, want)
	}
}

// The approval of a connection that was removed, renamed, or switched away from the vault is stale, and
// Revoke removes it.
func TestStaleApprovalsAndRevoke(t *testing.T) {
	cfg, v := fixture(t)
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	renamed := cfg.Connections["wiki-read"]
	delete(cfg.Connections, "wiki-read")
	cfg.Connections["wiki-reader"] = renamed
	cfg.Credentials["writer"] = config.Credential{Type: config.CredentialTypeKeyring}

	report, err := Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Stale, []string{"wiki-read", "wiki-write"}) ||
		!reflect.DeepEqual(openNames(report), []string{"wiki-reader"}) {
		t.Fatalf("Pending() = %+v, want the renamed connection open and both old approvals stale", report)
	}
	removed, warning, err := Revoke(context.Background(), v, report.Stale)
	if err != nil || warning != "" || removed != 2 {
		t.Fatalf("Revoke() = %d, %q, %v", removed, warning, err)
	}
	if report, _ := Pending(cfg, v); len(report.Stale) != 0 {
		t.Errorf("Pending().Stale after Revoke() = %v, want none", report.Stale)
	}
}

// Approving needs the vault unlocked in this process, and approvals exist only for an encrypted vault.
func TestApproveNeedsAnUnlockedEncryptedVault(t *testing.T) {
	cfg, v := fixture(t)
	locked := vault.New(filepath.Dir(v.Dir()))
	if _, _, err := Approve(context.Background(), cfg, locked, nil); !errors.Is(err, vault.ErrNotUnlocked) {
		t.Errorf("Approve() on a locked vault = %v, want vault.ErrNotUnlocked", err)
	}
	plain := vault.New(t.TempDir())
	if err := plain.Set("reader", "token", "synthetic-reader", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Pending(cfg, plain); !errors.Is(err, vault.ErrNotEncrypted) {
		t.Errorf("Pending() on an unencrypted vault = %v, want vault.ErrNotEncrypted", err)
	}
}

// A changed files release is never approved in passing by a save of unrelated fields.
func TestDirectApprovableRefusesFilesChange(t *testing.T) {
	if DirectApprovable(Change{Fields: []FieldChange{{Field: FieldFiles}}}) {
		t.Error("DirectApprovable() = true for a changed files release")
	}
	if !DirectApprovable(Change{Fields: []FieldChange{{Field: FieldTools}}}) {
		t.Error("DirectApprovable() = false for a changed tools list")
	}
}

// scopeFixture is a scope whose lists are long enough to tell what was added, removed, and kept.
func scopeFixture() vault.Scope {
	return vault.Scope{
		Connection: "gh", Credential: "token", Provider: "github", Origin: "https://api.example.test",
		Permissions: []string{"read"}, Targets: []string{"org"},
		Tools: []string{"github.issues.get", "github.issues.list", "github.pullrequests.list"},
	}
}

func fieldOf(t *testing.T, fields []FieldChange, name string) FieldChange {
	t.Helper()
	for _, f := range fields {
		if f.Field == name {
			return f
		}
	}
	t.Fatalf("no change of %s in %+v", name, fields)
	return FieldChange{}
}

func TestDiffListsAddedRemovedKept(t *testing.T) {
	approved := vault.Approval{Scope: scopeFixture()}
	now := scopeFixture()

	// Only added.
	now.Tools = append(now.Tools, "github.pullrequests.get", "github.pullrequestchecks.list")
	fields := diff(approved, now, "")
	if len(fields) != 1 {
		t.Fatalf("diff() = %+v, want only the tools", fields)
	}
	got := fieldOf(t, fields, FieldTools)
	if !got.IsListChange() || !reflect.DeepEqual(got.Added, []string{"github.pullrequestchecks.list", "github.pullrequests.get"}) ||
		len(got.Removed) != 0 || !reflect.DeepEqual(got.Kept, []string{"github.issues.get", "github.issues.list", "github.pullrequests.list"}) {
		t.Errorf("added only: %+v", got)
	}

	// Only removed.
	now = scopeFixture()
	now.Tools = []string{"github.issues.get", "github.issues.list"}
	got = fieldOf(t, diff(approved, now, ""), FieldTools)
	if len(got.Added) != 0 || !reflect.DeepEqual(got.Removed, []string{"github.pullrequests.list"}) || len(got.Kept) != 2 {
		t.Errorf("removed only: %+v", got)
	}

	// Both at once, and a forward credential whose fields changed counts as one removed and one added.
	now = scopeFixture()
	now.Tools = []string{"github.issues.get", "github.pullrequests.get", "github.pullrequests.list"}
	approved.Scope.Forward = []vault.ForwardSecret{{Name: "db", Fields: []string{"user", "pass"}}}
	now.Forward = []vault.ForwardSecret{{Name: "db", Fields: []string{"user"}}}
	fields = diff(approved, now, "")
	got = fieldOf(t, fields, FieldTools)
	if !reflect.DeepEqual(got.Added, []string{"github.pullrequests.get"}) ||
		!reflect.DeepEqual(got.Removed, []string{"github.issues.list"}) || len(got.Kept) != 2 {
		t.Errorf("both: %+v", got)
	}
	fwd := fieldOf(t, fields, FieldForward)
	if !reflect.DeepEqual(fwd.Added, []string{"db (fields: user)"}) || !reflect.DeepEqual(fwd.Removed, []string{"db (fields: pass user)"}) {
		t.Errorf("forward: %+v", fwd)
	}
	if got.Before == "" || got.After == "" {
		t.Errorf("Before and After stay set for a list change: %+v", got)
	}
}

func TestDiffModeSwitchesStayBeforeAndAfter(t *testing.T) {
	approved := vault.Approval{Scope: scopeFixture()}
	now := scopeFixture()
	now.Tools = nil
	now.Paths = []string{"~/a"}
	now.FilesRead = []string{"~/in"}
	fields := diff(approved, now, "")
	for _, name := range []string{FieldTools, FieldPaths, FieldFiles} {
		f := fieldOf(t, fields, name)
		if f.IsListChange() || f.Added != nil || f.Removed != nil || f.Kept != nil {
			t.Errorf("%s is a mode switch, got sets: %+v", name, f)
		}
	}
	if f := fieldOf(t, fields, FieldTools); f.After != "(every tool the permissions allow)" ||
		f.Before != "github.issues.get github.issues.list github.pullrequests.list" {
		t.Errorf("tools mode switch: %+v", f)
	}
	if f := fieldOf(t, fields, FieldPaths); f.Before != "(every project)" || f.After != "~/a" {
		t.Errorf("paths mode switch: %+v", f)
	}
	if f := fieldOf(t, fields, FieldFiles); f.Before != "(no local files)" || f.After != "read: ~/in" {
		t.Errorf("files mode switch: %+v", f)
	}

	// And back: a list that becomes the mode.
	back := diff(vault.Approval{Scope: now}, scopeFixture(), "")
	if f := fieldOf(t, back, FieldPaths); f.IsListChange() || f.After != "(every project)" {
		t.Errorf("paths back to every project: %+v", f)
	}
}

func TestDiffFilesPerDirectionAndUnchangedFields(t *testing.T) {
	base := scopeFixture()
	base.FilesRead = []string{"~/in"}
	base.FilesWrite = []string{"~/out"}
	now := base
	now.FilesRead = []string{"~/in", "~/more"}
	now.FilesWrite = nil
	now.FilesWrite = []string{"~/other"}
	fields := diff(vault.Approval{Scope: base}, now, "")
	if len(fields) != 1 {
		t.Fatalf("diff() = %+v, want only files: the rest is unchanged", fields)
	}
	f := fieldOf(t, fields, FieldFiles)
	if !reflect.DeepEqual(f.Added, []string{"read: ~/more", "write: ~/other"}) ||
		!reflect.DeepEqual(f.Removed, []string{"write: ~/out"}) || !reflect.DeepEqual(f.Kept, []string{"read: ~/in"}) {
		t.Errorf("files: %+v", f)
	}
}
