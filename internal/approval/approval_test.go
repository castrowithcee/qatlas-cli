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
		{Field: FieldPermissions, Before: "create read", After: "create delete read"},
		{Field: FieldTargets, Before: "(none)", After: "shelf-1 shelf-2"},
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
