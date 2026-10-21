package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// handRename edits the configuration file the way a person renames an entry by hand: every occurrence of old
// in the text becomes name, and the ids stay. It returns the bytes written.
func (w renameWorld) handRename(t *testing.T, old, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	edited := []byte(strings.ReplaceAll(string(data), old, name))
	if err := os.WriteFile(w.path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	return edited
}

// canonical is what qatlas writes for cfg.
func canonical(t *testing.T, cfg *config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.NewStore(path, setupCatalog{}).Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// backup names the configuration backup that holds data, "" for none.
func (w renameWorld) backup(t *testing.T, data []byte) string {
	t.Helper()
	for _, name := range backups(t, w.dir) {
		if held, err := os.ReadFile(filepath.Join(w.dir, "backups", name)); err == nil && string(held) == string(data) {
			return name
		}
	}
	return ""
}

func (w renameWorld) handRenames(t *testing.T) []HandRename {
	t.Helper()
	names, err := w.v.KnownNames()
	if err != nil {
		t.Fatal(err)
	}
	return HandRenames(w.load(t), names)
}

// usable reports whether the vault hands connection, as configured now, its credential's secret.
func (w renameWorld) usable(t *testing.T, connection string) bool {
	t.Helper()
	scopes, err := approval.VaultScopes(w.load(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if scope.Connection == connection {
			err := w.v.CheckApproval(scope)
			if err != nil && !errors.Is(err, vault.ErrApprovalRequired) {
				t.Fatal(err)
			}
			return err == nil
		}
	}
	t.Fatalf("connection %s reads no vault credential", connection)
	return false
}

// A connection renamed by hand keeps its approval under the old name alone: that is a hand rename, not the
// rest of a rename, and adopting it carries the approval over, so the connection is usable without a new
// approval, and writes the configuration as it is.
func TestAdoptAConnectionRenamedByHand(t *testing.T) {
	w := newRenameWorld(t)
	id := w.load(t).Connections["vault-wiki"].ID
	edited := w.handRename(t, "vault-wiki", "wiki-main")
	current := w.load(t)
	want := canonical(t, current.Clone())

	if got := w.handRenames(t); !reflect.DeepEqual(got, []HandRename{{Kind: RenameConnection, Old: "vault-wiki",
		New: "wiki-main", ID: id}}) {
		t.Errorf("HandRenames() = %+v", got)
	}
	report := w.pending(t)
	if len(report.Leftovers) != 0 || !reflect.DeepEqual(report.HandRenames,
		[]approval.HandRename{{Approval: "vault-wiki", Connection: "wiki-main"}}) {
		t.Errorf("leftovers = %+v, hand renames = %+v", report.Leftovers, report.HandRenames)
	}
	if w.usable(t, "wiki-main") {
		t.Fatal("the renamed connection is usable before the rename was adopted")
	}

	// The plan alone changes nothing.
	before := files(t, w.dir)
	plan, err := w.svc.PlanAdopt(RenameConnection, "vault-wiki", "wiki-main", true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Adopt || plan.ID != id || plan.Approval != ApprovalValid || !plan.CarryApproval ||
		!reflect.DeepEqual(plan.References, []string{"defaults.connections.docs", "defaults.connections.knowledge"}) ||
		!reflect.DeepEqual(plan.LapsedTokens, []string{"helper"}) {
		t.Errorf("plan = %+v", plan)
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Fatal("planning changed a file")
	}

	res, err := w.svc.Rename(context.Background(), plan)
	if err != nil || len(res.Warnings) != 0 || len(res.VaultCopies) == 0 {
		t.Fatalf("Rename() = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(w.path); string(data) != want {
		t.Errorf("configuration =\n%s\nwant it as the hand rename left it, written by qatlas:\n%s", data, want)
	}
	// The edit moved the connection in the file, so the backup is kept as a hand version (adr-0004).
	if name := w.backup(t, edited); !strings.Contains(name, "-hand") {
		t.Errorf("backups = %v, want the hand version among them", backups(t, w.dir))
	}
	approvals, _ := w.v.Approvals()
	if _, ok := approvals["vault-wiki"]; ok || approvals["wiki-main"].ConnectionID != id {
		t.Errorf("approvals = %+v, want the approval under the new name alone", approvals)
	}
	report = w.pending(t)
	if len(report.Stale) != 0 || len(report.HandRenames) != 0 || !reflect.DeepEqual(w.openNames(report), []string{"widened"}) {
		t.Errorf("report = %+v, want only the connection that was open before", report)
	}
	if !w.usable(t, "wiki-main") {
		t.Error("the adopted connection is not usable")
	}
	if got := w.handRenames(t); len(got) != 0 {
		t.Errorf("HandRenames() after adopting = %+v", got)
	}
	entries := w.logged(t)
	if len(entries) != 1 || entries[0].FormerConnection != "vault-wiki" || entries[0].Connection != "wiki-main" {
		t.Errorf("log = %+v, want the rename", entries)
	}
}

// Adopting carries an approval as it is: one that was open stays open, without an admin session none is
// carried, and a connection the vault never approved has nothing to adopt.
func TestAdoptNeverApprovesAConnection(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		w := newRenameWorld(t)
		w.handRename(t, "widened", "wider")
		plan, err := w.svc.PlanAdopt(RenameConnection, "widened", "wider", true)
		if err != nil || plan.Approval != ApprovalOpen || !plan.CarryApproval {
			t.Fatalf("PlanAdopt() = %+v, %v, want the open approval carried", plan, err)
		}
		if _, err := w.svc.Rename(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		if _, open := OpenChange(w.pending(t), "wider"); !open || w.usable(t, "wider") {
			t.Error("the adopted connection was approved")
		}
	})
	t.Run("without an admin session", func(t *testing.T) {
		w := newRenameWorld(t)
		w.handRename(t, "vault-wiki", "wiki-main")
		secretsAge := filepath.Join(w.v.Dir(), "secrets.age")
		before, _ := os.ReadFile(secretsAge)
		plan, err := w.svc.PlanAdopt(RenameConnection, "vault-wiki", "wiki-main", false)
		if err != nil || plan.CarryApproval {
			t.Fatalf("PlanAdopt() = %+v, %v, want nothing carried", plan, err)
		}
		if _, err := w.svc.Rename(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		if after, _ := os.ReadFile(secretsAge); string(after) != string(before) || w.usable(t, "wiki-main") {
			t.Error("an approval was carried without an admin session")
		}
	})
	t.Run("never approved", func(t *testing.T) {
		w := newRenameWorld(t)
		if _, err := w.v.Revoke([]string{"second-wiki"}); err != nil {
			t.Fatal(err)
		}
		w.handRename(t, "second-wiki", "wiki-two")
		before := files(t, w.dir)
		_, err := w.svc.PlanAdopt(RenameConnection, "second-wiki", "wiki-two", true)
		var refused *RenameRefusedError
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), "nothing to adopt") {
			t.Errorf("PlanAdopt() = %v, want nothing to adopt", err)
		}
		if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
			t.Error("a refused plan changed a file")
		}
		if change, open := OpenChange(w.pending(t), "wiki-two"); !open || !change.New {
			t.Error("the connection is not open as a new one")
		}
	})
}

// A vault credential renamed by hand leaves its entry, without a mark, under the old name: a hand rename, not
// a stopped rename. Adopting renames the entry and carries the valid approvals of its readers.
func TestAdoptAVaultCredentialRenamedByHand(t *testing.T) {
	w := newRenameWorld(t)
	w.queuePending(t, "vault-reader", "key-id", "synthetic-queued")
	id := w.load(t).Credentials["vault-reader"].ID
	if _, err := w.v.SetUndoableWithID("vault-reader", id, "key-secret", "synthetic-key-secret", nil); err != nil {
		t.Fatal(err)
	}
	entryID, _, _ := w.v.CredentialID("vault-reader")
	edited := w.handRename(t, "vault-reader", "reader")
	want := canonical(t, w.load(t))

	if got := w.handRenames(t); !reflect.DeepEqual(got, []HandRename{{Kind: RenameCredential, Old: "vault-reader",
		New: "reader", ID: id}}) {
		t.Errorf("HandRenames() = %+v", got)
	}
	if left, err := CredentialLeftovers(w.load(t), w.v); err != nil || len(left) != 0 {
		t.Errorf("CredentialLeftovers() = %+v, %v, want none for a hand rename", left, err)
	}

	plan, err := w.svc.PlanAdopt(RenameCredential, "vault-reader", "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := map[string]string{"second-wiki": "approved,carried", "vault-wiki": "approved,carried", "widened": "open"}
	if !plan.Adopt || !plan.VaultEntry || plan.PendingWrites != 1 || plan.Resumes || plan.ID != id ||
		!reflect.DeepEqual(readerStates(plan.Readers), wantStates) {
		t.Errorf("plan = %+v, readers = %v", plan.CredentialPlan, readerStates(plan.Readers))
	}
	res, err := w.svc.Rename(context.Background(), plan)
	if err != nil || len(res.Warnings) != 0 || !reflect.DeepEqual(res.Carried, []string{"second-wiki", "vault-wiki"}) {
		t.Fatalf("Rename() = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(w.path); string(data) != want {
		t.Errorf("configuration =\n%s\nwant\n%s", data, want)
	}
	if w.backup(t, edited) == "" {
		t.Error("the hand version was not backed up")
	}
	if got, held, _ := w.v.CredentialID("reader"); !held || got != entryID {
		t.Errorf("entry = %q, %v, want %q under the new name", got, held, entryID)
	}
	if held, _ := w.v.HasEntry("vault-reader"); held {
		t.Error("the vault still holds the old name")
	}
	if value, found, _, _ := w.v.Get("reader", "key-id", nil); !found || value != "synthetic-queued" {
		t.Error("the pending write was not merged into the renamed entry")
	}
	names, _ := w.v.KnownNames()
	if len(names.Renamed) != 0 {
		t.Errorf("Renamed = %v, want the mark removed after the commit", names.Renamed)
	}
	if open := w.openNames(w.pending(t)); !reflect.DeepEqual(open, []string{"widened"}) {
		t.Errorf("open = %v, want only the connection that was open before", open)
	}
	if got := w.handRenames(t); len(got) != 0 {
		t.Errorf("HandRenames() after adopting = %+v", got)
	}
}

// A keyring credential's keys record no id, so its hand rename is not found, but adopting it with the name it
// had moves its keys.
func TestAdoptAKeyringCredentialRenamedByHand(t *testing.T) {
	w := newRenameWorld(t)
	secrets := newFakeSecrets()
	secrets.entries["mail-key.api-key"] = "syn-mail"
	svc := w.keyringService(secrets)
	w.handRename(t, "mail-key", "mail-cred")
	want := canonical(t, w.load(t))
	if got := w.handRenames(t); len(got) != 0 {
		t.Errorf("HandRenames() = %+v, want none for a keyring credential", got)
	}
	plan, err := svc.PlanAdopt(RenameCredential, "mail-key", "mail-cred", true)
	if err != nil || !reflect.DeepEqual(plan.KeyringRoles, []string{"api-key"}) || plan.VaultEntry {
		t.Fatalf("PlanAdopt() = %+v, %v", plan, err)
	}
	if res, err := svc.Rename(context.Background(), plan); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("Rename() = %+v, %v", res, err)
	}
	if !reflect.DeepEqual(secrets.entries, map[string]string{"mail-cred.api-key": "syn-mail"}) {
		t.Errorf("keyring = %v, want the key moved", secrets.entries)
	}
	if data, _ := os.ReadFile(w.path); string(data) != want {
		t.Errorf("configuration =\n%s\nwant\n%s", data, want)
	}
}

func TestAdoptRefusals(t *testing.T) {
	w := newRenameWorld(t)
	w.handRename(t, "vault-wiki", "wiki-main")
	cases := []struct{ name, kind, old, new, want string }{
		{"service", RenameService, "wiki", "wiki-eu", "service binds nothing to its name"},
		{"new name not configured", RenameConnection, "vault-wiki", "elsewhere", `connection "elsewhere" does not exist`},
		{"old name still configured", RenameConnection, "second-wiki", "wiki-main", `"second-wiki" is still configured`},
		{"credential still configured", RenameCredential, "mail-key", "vault-reader", `"mail-key" is still configured`},
		{"nothing under the old name", RenameConnection, "gone", "second-wiki", "nothing to adopt"},
		{"unknown kind", "provider", "docs", "pages", "cannot be renamed"},
	}
	before := files(t, w.dir)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := w.svc.PlanAdopt(c.kind, c.old, c.new, true)
			var refused *RenameRefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("PlanAdopt() = %v, want refused with %q", err, c.want)
			}
		})
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused plan changed a file")
	}
}

func TestAdoptNeedsAnUnlockedVault(t *testing.T) {
	w := newRenameWorld(t)
	w.handRename(t, "vault-wiki", "wiki-main")
	w.v.Forget()
	before := files(t, w.dir)
	plan, err := w.svc.PlanAdopt(RenameConnection, "vault-wiki", "wiki-main", true)
	if err != nil || !plan.VaultUnread || plan.Approval != "" {
		t.Fatalf("PlanAdopt() = %+v, %v, want the vault reported unread", plan, err)
	}
	if _, err := w.svc.Rename(context.Background(), plan); !errors.Is(err, vault.ErrNotUnlocked) {
		t.Errorf("Rename() = %v, want vault.ErrNotUnlocked", err)
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused adopt changed a file")
	}
}

// A file changed since the plan is not overwritten, and the vault stays as it was.
func TestAdoptRefusesAChangedFile(t *testing.T) {
	w := newRenameWorld(t)
	w.handRename(t, "vault-wiki", "wiki-main")
	plan, err := w.svc.PlanAdopt(RenameConnection, "vault-wiki", "wiki-main", true)
	if err != nil {
		t.Fatal(err)
	}
	vaultBefore := files(t, w.v.Dir())
	other := w.handRename(t, "https://mail.example.test", "https://mail2.example.test")
	if _, err := w.svc.Rename(context.Background(), plan); !errors.Is(err, config.ErrConflict) {
		t.Fatalf("Rename() = %v, want a conflict", err)
	}
	if data, _ := os.ReadFile(w.path); string(data) != string(other) {
		t.Error("the changed file was overwritten")
	}
	if after := files(t, w.v.Dir()); !reflect.DeepEqual(after, vaultBefore) {
		t.Error("the vault changed")
	}
}

// The entry a stopped rename left carries its mark and is no hand rename; a mark left on an entry whose name
// is configured is neither.
func TestAMarkedEntryIsNoHandRename(t *testing.T) {
	t.Run("stopped rename", func(t *testing.T) {
		w := newRenameWorld(t)
		stopBeforeCommit(t, w.svc, "vault-reader", "reader")
		if got := w.handRenames(t); len(got) != 0 {
			t.Errorf("HandRenames() = %+v, want none for a stopped rename", got)
		}
		if left, err := CredentialLeftovers(w.load(t), w.v); err != nil || len(left) != 1 {
			t.Errorf("CredentialLeftovers() = %+v, %v, want the stopped rename", left, err)
		}
	})
	t.Run("mark left behind", func(t *testing.T) {
		w := newRenameWorld(t)
		id := w.load(t).Credentials["vault-reader"].ID
		if _, _, err := w.v.RenameCredential(vault.CredentialRename{From: "vault-reader", To: "reader",
			CredentialID: id, Entry: true, Mark: true}); err != nil {
			t.Fatal(err)
		}
		w.handRename(t, "vault-reader", "reader")
		if names, _ := w.v.KnownNames(); names.Renamed["reader"] != "vault-reader" {
			t.Fatalf("Renamed = %v, want the mark kept", names.Renamed)
		}
		if got := w.handRenames(t); len(got) != 0 {
			t.Errorf("HandRenames() = %+v, want none", got)
		}
		if left, err := CredentialLeftovers(w.load(t), w.v); err != nil || len(left) != 0 {
			t.Errorf("CredentialLeftovers() = %+v, %v, want none", left, err)
		}
	})
}

// The rest of a connection's rename keeps an approval under both names and is a leftover; an approval under
// the old name alone is a hand rename. An approval that records no connection id is matched by its
// credential only where exactly one connection could be it.
func TestConnectionHandRenameOrLeftover(t *testing.T) {
	t.Run("rest of a rename", func(t *testing.T) {
		w := newRenameWorld(t)
		id := w.load(t).Connections["vault-wiki"].ID
		if _, _, err := w.v.CarryApproval("vault-wiki", "wiki-main", id); err != nil {
			t.Fatal(err)
		}
		w.handRename(t, "vault-wiki", "wiki-main")
		report := w.pending(t)
		if len(report.HandRenames) != 0 || !reflect.DeepEqual(report.Leftovers,
			[]approval.Leftover{{Approval: "vault-wiki", Connection: "wiki-main"}}) {
			t.Errorf("leftovers = %+v, hand renames = %+v", report.Leftovers, report.HandRenames)
		}
		if got := w.handRenames(t); len(got) != 0 {
			t.Errorf("HandRenames() = %+v, want none", got)
		}
	})
	for _, c := range []struct {
		name    string
		revoke  []string
		want    []HandRename
		pending []approval.HandRename
	}{
		{"unique", nil, []HandRename{{Kind: RenameConnection, Old: "vault-wiki",
			New: "wiki-main"}}, []approval.HandRename{{Approval: "vault-wiki", Connection: "wiki-main"}}},
		{"ambiguous", []string{"widened"}, []HandRename{{Kind: RenameConnection, Old: "vault-wiki", Ambiguous: true}},
			[]approval.HandRename{{Approval: "vault-wiki", Ambiguous: true}}},
	} {
		t.Run("without an id, "+c.name, func(t *testing.T) {
			w := newRenameWorld(t)
			cfg := w.load(t)
			approvals, _ := w.v.Approvals()
			// An approval of an earlier build, which records no connection id.
			if err := w.v.Approve([]vault.Scope{approvals["vault-wiki"].Scope}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.v.Revoke(c.revoke); err != nil {
				t.Fatal(err)
			}
			w.handRename(t, "vault-wiki", "wiki-main")
			want := c.want
			if !c.want[0].Ambiguous {
				want = []HandRename{{Kind: RenameConnection, Old: "vault-wiki", New: "wiki-main",
					ID: cfg.Connections["vault-wiki"].ID}}
			}
			if got := w.handRenames(t); !reflect.DeepEqual(got, want) {
				t.Errorf("HandRenames() = %+v, want %+v", got, want)
			}
			if report := w.pending(t); !reflect.DeepEqual(report.HandRenames, c.pending) || len(report.Leftovers) != 0 {
				t.Errorf("hand renames = %+v, leftovers = %+v", report.HandRenames, report.Leftovers)
			}
		})
	}
}
