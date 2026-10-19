package manage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const renamePassphrase = "synthetic-passphrase-91ab"

const renameConfig = `version: 2
services:
    wiki:
        provider: docs
        base_url: https://wiki.example.test
    mail:
        provider: mail
        base_url: https://mail.example.test
credentials:
    vault-reader:
        type: vault
    mail-key:
        type: keyring
connections:
    vault-wiki:
        service: wiki
        credential: vault-reader
        permissions:
            - read
    second-wiki:
        service: wiki
        credential: vault-reader
        permissions:
            - read
    widened:
        service: wiki
        credential: vault-reader
        permissions:
            - read
    mail:
        service: mail
        credential: mail-key
defaults:
    connections:
        docs: vault-wiki
        knowledge: vault-wiki
        mail: mail
`

// renameWorld is renameConfig beside an encrypted vault unlocked in this process that approves every vault
// connection, one of which, widened, has been widened since and is open, and an agent token whose vorbild
// is vault-wiki.
type renameWorld struct {
	dir, path string
	v         *vault.Vault
	svc       *Service
}

func newRenameWorld(t *testing.T) renameWorld {
	t.Helper()
	return newRenameWorldWith(t, renameConfig)
}

// newRenameWorldWith is newRenameWorld over the configuration text.
func newRenameWorldWith(t *testing.T, text string) renameWorld {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(path, setupCatalog{})
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	// The ids the file is written with are the ones every later step must keep.
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	offer := func(string) (string, error) { return renamePassphrase, nil }
	for _, role := range []string{"key-id", "key-secret"} {
		if err := v.Set("vault-reader", role, "synthetic-"+role, offer); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.Unlock(renamePassphrase); err != nil {
		t.Fatal(err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := v.CreateToken("helper", []string{"vault-wiki"}, nil); err != nil {
		t.Fatal(err)
	}
	widenedCfg := cfg.Clone()
	widen(widenedCfg, "widened")
	if err := store.Save(widenedCfg); err != nil {
		t.Fatal(err)
	}
	w := renameWorld{dir: dir, path: path, v: v}
	w.svc = ForVault(store, v, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
	return w
}

func (w renameWorld) load(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(w.path, setupCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func (w renameWorld) rename(t *testing.T, kind, old, name string, approve bool) (*RenamePlan, RenameResult) {
	t.Helper()
	plan, err := w.svc.PlanRename(kind, old, name, approve)
	if err != nil {
		t.Fatalf("PlanRename() = %v", err)
	}
	res, err := w.svc.Rename(context.Background(), plan)
	if err != nil {
		t.Fatalf("Rename() = %v", err)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("Rename() warnings = %v", res.Warnings)
	}
	return plan, res
}

func (w renameWorld) pending(t *testing.T) approval.Report {
	t.Helper()
	report, err := approval.Pending(w.load(t), w.v)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// logged decodes every entry of the invocation log.
func (w renameWorld) logged(t *testing.T) []invokelog.Entry {
	t.Helper()
	var entries []invokelog.Entry
	for _, line := range logEntries(t, w.dir) {
		var e invokelog.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestRenameConnectionCarriesItsApprovalAndLogsOneRename(t *testing.T) {
	w := newRenameWorld(t)
	before := w.load(t)
	id := before.Connections["vault-wiki"].ID

	plan, res := w.rename(t, RenameConnection, "vault-wiki", "wiki-main", true)
	if !reflect.DeepEqual(plan.References, []string{"defaults.connections.docs", "defaults.connections.knowledge"}) {
		t.Errorf("References = %v", plan.References)
	}
	if plan.ID != id || plan.Approval != ApprovalValid || !plan.CarryApproval || plan.VaultUnread {
		t.Errorf("plan = %+v, want the valid approval carried", plan)
	}
	if !reflect.DeepEqual(plan.LapsedTokens, []string{"helper"}) {
		t.Errorf("LapsedTokens = %v, want the token whose vorbild is renamed", plan.LapsedTokens)
	}
	if len(plan.Outside) == 0 || !strings.Contains(strings.Join(plan.Outside, "\n"), "--connection vault-wiki") {
		t.Errorf("Outside = %v, want the places qatlas cannot change", plan.Outside)
	}

	after := w.load(t)
	if _, ok := after.Connections["vault-wiki"]; ok {
		t.Error("the old name is still configured")
	}
	if got := after.Connections["wiki-main"].ID; got != id {
		t.Errorf("id = %q, want %q kept", got, id)
	}
	if after.Defaults.Connections["docs"] != "wiki-main" || after.Defaults.Connections["knowledge"] != "wiki-main" ||
		after.Defaults.Connections["mail"] != "mail" {
		t.Errorf("defaults = %v", after.Defaults.Connections)
	}
	for name, svc := range before.Services {
		if after.Services[name].ID != svc.ID {
			t.Errorf("service %s lost its id", name)
		}
	}

	report := w.pending(t)
	if len(report.Stale) != 0 || len(report.Leftovers) != 0 {
		t.Errorf("stale = %v, leftovers = %v, want none", report.Stale, report.Leftovers)
	}
	if open := w.openNames(report); !reflect.DeepEqual(open, []string{"widened"}) {
		t.Errorf("open = %v, want only the connection that was open before", open)
	}
	approvals, err := w.v.Approvals()
	if err != nil {
		t.Fatal(err)
	}
	if a := approvals["wiki-main"]; a.Scope.Connection != "wiki-main" || a.ConnectionID != id {
		t.Errorf("approval = %+v", a)
	}
	if len(res.VaultCopies) == 0 {
		t.Error("the vault files were not backed up")
	}
	for _, copied := range res.VaultCopies {
		if _, err := os.Stat(copied); err != nil {
			t.Errorf("vault copy %s: %v", copied, err)
		}
	}
	if len(backups(t, w.dir)) <= len(res.VaultCopies) {
		t.Errorf("backups = %v, want the configuration backed up too", backups(t, w.dir))
	}

	entries := w.logged(t)
	if len(entries) != 1 {
		t.Fatalf("log = %+v, want exactly the rename", entries)
	}
	e := entries[0]
	if e.Operation != invokelog.OperationConnectionRename || e.Connection != "wiki-main" ||
		e.FormerConnection != "vault-wiki" || e.ConnectionID != id || e.Effect != "update" || e.Path != "cli" ||
		e.MAC == "" {
		t.Errorf("entry = %+v, want a signed rename with both names and the id", e)
	}
	key, err := w.v.LogKey()
	if err != nil {
		t.Fatal(err)
	}
	defer key.Clear()
	verified, err := invokelog.VerifyWith(w.v.Dir(), key)
	if err != nil || verified.Broken || verified.Days[0].Changed != 0 || verified.Days[0].Unverified != 0 {
		t.Errorf("VerifyWith() = %+v, %v, want an intact, verified chain", verified, err)
	}
}

func (w renameWorld) openNames(report approval.Report) []string {
	var names []string
	for _, c := range report.Open {
		names = append(names, c.Connection)
	}
	return names
}

func TestRenameOpenConnectionStaysOpen(t *testing.T) {
	w := newRenameWorld(t)
	plan, _ := w.rename(t, RenameConnection, "widened", "wider", true)
	if plan.Approval != ApprovalOpen || !plan.CarryApproval {
		t.Errorf("plan = %+v, want the open approval carried", plan)
	}
	report := w.pending(t)
	change, open := OpenChange(report, "wider")
	if !open || change.New || len(change.Fields) != 1 || change.Fields[0].Field != approval.FieldEffects {
		t.Errorf("change = %+v, want the widened permissions still open", change)
	}
}

func TestRenameConnectionWithoutAdminSessionCarriesNothing(t *testing.T) {
	w := newRenameWorld(t)
	secretsAge := filepath.Join(w.v.Dir(), "secrets.age")
	before, err := os.ReadFile(secretsAge)
	if err != nil {
		t.Fatal(err)
	}
	plan, res := w.rename(t, RenameConnection, "vault-wiki", "wiki-main", false)
	if plan.CarryApproval || plan.Approval != ApprovalValid || len(res.VaultCopies) != 0 {
		t.Errorf("plan = %+v, res = %+v, want nothing carried", plan, res)
	}
	if after, _ := os.ReadFile(secretsAge); string(after) != string(before) {
		t.Error("the vault was written")
	}
	report := w.pending(t)
	change, open := OpenChange(report, "wiki-main")
	if !open || !change.New {
		t.Errorf("wiki-main = %+v, %v, want it new and open", change, open)
	}
	if !reflect.DeepEqual(report.Stale, []string{"vault-wiki"}) {
		t.Errorf("stale = %v, want the approval left under the old name", report.Stale)
	}
}

func TestRenameServiceChangesOnlyTheConfiguration(t *testing.T) {
	w := newRenameWorld(t)
	before := w.load(t)
	vaultBefore := files(t, w.v.Dir())

	plan, res := w.rename(t, RenameService, "wiki", "wiki-eu", true)
	want := []string{"connections.second-wiki.service", "connections.vault-wiki.service", "connections.widened.service"}
	if !reflect.DeepEqual(plan.References, want) || plan.Approval != "" || plan.CarryApproval ||
		len(plan.LapsedTokens) != 0 || len(res.VaultCopies) != 0 {
		t.Errorf("plan = %+v, res = %+v", plan, res)
	}
	after := w.load(t)
	if after.Services["wiki-eu"].ID != before.Services["wiki"].ID || after.Connections["vault-wiki"].Service != "wiki-eu" ||
		after.Connections["mail"].Service != "mail" {
		t.Errorf("services = %v, connections = %v", after.Services, after.Connections)
	}
	// Approvals name the provider and the endpoint, never the service: the rename opens nothing.
	if open := w.openNames(w.pending(t)); !reflect.DeepEqual(open, []string{"widened"}) {
		t.Errorf("open = %v, want only widened", open)
	}
	if got := files(t, w.v.Dir()); !reflect.DeepEqual(got, vaultBefore) {
		t.Error("the vault directory changed, the log included")
	}
}

func TestRenamePlanChangesNothing(t *testing.T) {
	w := newRenameWorld(t)
	before := files(t, w.dir)
	for _, kind := range []string{RenameService, RenameConnection} {
		old := map[string]string{RenameService: "wiki", RenameConnection: "vault-wiki"}[kind]
		if _, err := w.svc.PlanRename(kind, old, "renamed", true); err != nil {
			t.Fatalf("PlanRename(%s) = %v", kind, err)
		}
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("planning changed a file: configuration, vault, log, or backups")
	}
}

func TestRenameRefusesNamesTakenOrClaimed(t *testing.T) {
	w := newRenameWorld(t)
	if _, err := w.v.CreateToken("ghostly", []string{"ghost"}, nil); err != nil {
		t.Fatal(err)
	}
	gone := vault.Scope{Connection: "gone", Credential: "vault-reader", Provider: "docs", Origin: "https://x.example"}
	if err := w.v.Approve([]vault.Scope{gone}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, kind, old, new, want string
	}{
		{"taken connection", RenameConnection, "vault-wiki", "second-wiki", "exists already"},
		{"taken service", RenameService, "wiki", "mail", "exists already"},
		{"invalid name", RenameConnection, "vault-wiki", "-bad", "must consist of"},
		{"same name", RenameConnection, "vault-wiki", "vault-wiki", "already has this name"},
		{"missing entry", RenameConnection, "nothing", "something", "does not exist"},
		{"a token's vorbild", RenameConnection, "vault-wiki", "ghost", "names ghost as a vorbild"},
		{"another connection's approval", RenameConnection, "vault-wiki", "gone", "stale approval under the name gone"},
		{"unknown kind", "provider", "docs", "pages", "cannot be renamed"},
	}
	before := files(t, w.dir)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := w.svc.PlanRename(c.kind, c.old, c.new, true); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("PlanRename() = %v, want %q", err, c.want)
			}
		})
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused rename changed a file")
	}
}

func TestRenameRollsTheVaultBackWhenTheCommitFails(t *testing.T) {
	for _, failure := range []string{"write error", "conflict"} {
		t.Run(failure, func(t *testing.T) {
			w := newRenameWorld(t)
			plan, err := w.svc.PlanRename(RenameConnection, "vault-wiki", "wiki-main", true)
			if err != nil {
				t.Fatal(err)
			}
			vaultBefore := files(t, w.v.Dir())
			backupsBefore := backups(t, w.dir)
			approvalsBefore, _ := w.v.Approvals()
			var other []byte
			original := saveRename
			t.Cleanup(func() { saveRename = original })
			saveRename = func(save func(*config.Config) error, cfg *config.Config) error {
				if failure == "write error" {
					return errors.New("disk full")
				}
				// Another writer that takes no lock replaces the file between the vault and the commit.
				edited := strings.Replace(renameConfig, "base_url: https://mail.example.test", "base_url: https://mail2.example.test", 1)
				other = []byte(edited)
				if err := os.WriteFile(w.path, other, 0o600); err != nil {
					t.Fatal(err)
				}
				return save(cfg)
			}
			_, err = w.svc.Rename(context.Background(), plan)
			switch failure {
			case "write error":
				if err == nil || !strings.Contains(err.Error(), "disk full") {
					t.Fatalf("Rename() = %v, want the write error", err)
				}
			case "conflict":
				if !errors.Is(err, config.ErrConflict) {
					t.Fatalf("Rename() = %v, want a conflict", err)
				}
				if data, _ := os.ReadFile(w.path); string(data) != string(other) {
					t.Error("the other writer's file was overwritten")
				}
			}
			if after := files(t, w.v.Dir()); !reflect.DeepEqual(after, vaultBefore) {
				t.Error("the vault directory differs from before the rename")
			}
			approvalsAfter, _ := w.v.Approvals()
			if !reflect.DeepEqual(approvalsAfter, approvalsBefore) {
				t.Errorf("approvals = %v, want them as before", approvalsAfter)
			}
			// Saving the configuration never got as far as its own backup.
			if names := backups(t, w.dir); !reflect.DeepEqual(names, backupsBefore) {
				t.Errorf("backups = %v, want them as before, the vault copies discarded", names)
			}
		})
	}
}

// A rename cut short between the vault and the configuration, or after the configuration, leaves a stale
// approval that names its connection; renaming again finishes it.
func TestRenameLeftoversAreFoundAndFinished(t *testing.T) {
	t.Run("before the configuration", func(t *testing.T) {
		w := newRenameWorld(t)
		id := w.load(t).Connections["vault-wiki"].ID
		if _, _, err := w.v.CarryApproval("vault-wiki", "wiki-main", id); err != nil {
			t.Fatal(err)
		}
		report := w.pending(t)
		if want := []approval.Leftover{{Approval: "wiki-main", Connection: "vault-wiki"}}; !reflect.DeepEqual(report.Leftovers, want) {
			t.Errorf("leftovers = %+v, want %+v", report.Leftovers, want)
		}
		w.rename(t, RenameConnection, "vault-wiki", "wiki-main", true)
		if report := w.pending(t); len(report.Stale) != 0 || len(report.Leftovers) != 0 ||
			!reflect.DeepEqual(w.openNames(report), []string{"widened"}) {
			t.Errorf("report = %+v, want the rename finished", report)
		}
	})
	t.Run("after the configuration", func(t *testing.T) {
		w := newRenameWorld(t)
		cfg := w.load(t)
		id := cfg.Connections["vault-wiki"].ID
		if _, _, err := w.v.CarryApproval("vault-wiki", "wiki-main", id); err != nil {
			t.Fatal(err)
		}
		if _, err := cfg.RenameConnection("vault-wiki", "wiki-main"); err != nil {
			t.Fatal(err)
		}
		if err := config.NewStore(w.path, setupCatalog{}).Save(cfg); err != nil {
			t.Fatal(err)
		}
		report := w.pending(t)
		if want := []approval.Leftover{{Approval: "vault-wiki", Connection: "wiki-main"}}; !reflect.DeepEqual(report.Leftovers, want) {
			t.Errorf("leftovers = %+v, want %+v", report.Leftovers, want)
		}
		if _, open := OpenChange(report, "wiki-main"); open {
			t.Error("the renamed connection is open")
		}
	})
}

func TestRenameNeedsAnUnlockedVault(t *testing.T) {
	w := newRenameWorld(t)
	w.v.Forget()
	before := files(t, w.dir)
	plan, err := w.svc.PlanRename(RenameConnection, "vault-wiki", "wiki-main", true)
	if err != nil || !plan.VaultUnread || plan.Approval != "" {
		t.Fatalf("PlanRename() = %+v, %v, want the vault reported unread", plan, err)
	}
	if _, err := w.svc.Rename(context.Background(), plan); !errors.Is(err, vault.ErrNotUnlocked) {
		t.Errorf("Rename() = %v, want vault.ErrNotUnlocked", err)
	}
	plan, err = w.svc.PlanRename(RenameService, "wiki", "wiki-eu", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Rename(context.Background(), plan); !errors.Is(err, vault.ErrNotUnlocked) {
		t.Errorf("Rename(service) = %v, want vault.ErrNotUnlocked", err)
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused rename changed a file")
	}
}
