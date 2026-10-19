package manage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// keyringService is a service over the world's configuration and vault whose system keyring is secrets.
func (w renameWorld) keyringService(secrets *fakeSecrets) *Service {
	secrets.vault = w.v
	return New(config.NewStore(w.path, setupCatalog{}), secrets, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
}

// queuePending writes a secret the way another process does while it finds the vault locked.
func (w renameWorld) queuePending(t *testing.T, credential, role, value string) {
	t.Helper()
	if err := vault.New(w.dir).Set(credential, role, value, nil); err != nil {
		t.Fatal(err)
	}
}

func readerStates(readers []RenameReader) map[string]string {
	out := map[string]string{}
	for _, r := range readers {
		state := r.Approval
		if r.Carried {
			state += ",carried"
		}
		out[r.Connection] = state
	}
	return out
}

func TestRenameVaultCredentialRenamesTheEntryAndCarriesValidApprovals(t *testing.T) {
	w := newRenameWorld(t)
	w.queuePending(t, "vault-reader", "key-id", "synthetic-queued")
	before := w.load(t)
	entryID, _, err := w.v.CredentialID("vault-reader")
	if err != nil {
		t.Fatal(err)
	}

	plan, res := w.rename(t, RenameCredential, "vault-reader", "reader", true)
	wantRefs := []string{"connections.second-wiki.credential", "connections.vault-wiki.credential", "connections.widened.credential"}
	if !reflect.DeepEqual(plan.References, wantRefs) || plan.ID != before.Credentials["vault-reader"].ID {
		t.Errorf("references = %v, id = %q", plan.References, plan.ID)
	}
	if !plan.VaultEntry || plan.PendingWrites != 1 || len(plan.KeyringRoles) != 0 || plan.VaultUnread {
		t.Errorf("plan = %+v, want the vault entry and its pending write", plan.CredentialPlan)
	}
	wantStates := map[string]string{"second-wiki": "approved,carried", "vault-wiki": "approved,carried", "widened": "open"}
	if got := readerStates(plan.Readers); !reflect.DeepEqual(got, wantStates) {
		t.Errorf("readers = %v, want %v", got, wantStates)
	}
	if len(plan.LapsedTokens) != 0 {
		t.Errorf("LapsedTokens = %v, want none: the vorbild's approval is carried", plan.LapsedTokens)
	}
	// The credential names no provider, so it accepts every role.
	wantEnv := []EnvRename{
		{Role: "api-key", Old: "QATLAS_VAULT_READER_API_KEY", New: "QATLAS_READER_API_KEY"},
		{Role: "key-id", Old: "QATLAS_VAULT_READER_KEY_ID", New: "QATLAS_READER_KEY_ID"},
		{Role: "key-secret", Old: "QATLAS_VAULT_READER_KEY_SECRET", New: "QATLAS_READER_KEY_SECRET"},
	}
	if !reflect.DeepEqual(plan.EnvVars, wantEnv) || !reflect.DeepEqual(res.EnvVars, wantEnv) {
		t.Errorf("env = %v / %v, want %v", plan.EnvVars, res.EnvVars, wantEnv)
	}
	if outside := strings.Join(plan.Outside, "\n"); !strings.Contains(outside, "environment variables named after the credential") ||
		!strings.Contains(outside, "qatlas changes no environment") {
		t.Errorf("Outside = %v", plan.Outside)
	}
	if !reflect.DeepEqual(res.Carried, []string{"second-wiki", "vault-wiki"}) || len(res.VaultCopies) == 0 {
		t.Errorf("res = %+v", res)
	}

	after := w.load(t)
	if _, ok := after.Credentials["vault-reader"]; ok || after.Credentials["reader"].ID != plan.ID ||
		after.Connections["vault-wiki"].Credential != "reader" {
		t.Errorf("credentials = %v", after.Credentials)
	}
	if id, held, _ := w.v.CredentialID("reader"); !held || id != entryID {
		t.Errorf("entry id = %q, %v, want %q kept", id, held, entryID)
	}
	if held, _ := w.v.HasEntry("vault-reader"); held {
		t.Error("the vault still holds the old name")
	}
	if value, found, _, _ := w.v.Get("reader", "key-id", nil); !found || value != "synthetic-queued" {
		t.Error("the pending write was not merged into the renamed entry")
	}
	if pending, _ := w.v.PendingWrites(); len(pending) != 0 {
		t.Errorf("pending = %v, want none left", pending)
	}
	report := w.pending(t)
	if open := w.openNames(report); !reflect.DeepEqual(open, []string{"widened"}) || len(report.Stale) != 0 {
		t.Errorf("open = %v, stale = %v, want only the connection that was open before", open, report.Stale)
	}
	tokens, _ := w.v.Tokens()
	if lapsed, _ := approval.LapsedModels(after, w.v, tokens[0]); len(lapsed) != 0 {
		t.Errorf("lapsed vorbilder = %v", lapsed)
	}
	var ops []string
	for _, e := range w.logged(t) {
		ops = append(ops, e.Operation+" "+e.Connection)
	}
	change := invokelog.OperationConnectionChange + " "
	if want := []string{change + "second-wiki", change + "vault-wiki", change + "widened"}; !reflect.DeepEqual(ops, want) {
		t.Errorf("log = %v, want %v", ops, want)
	}
}

func TestRenameVaultCredentialWithoutAdminSessionLeavesApprovals(t *testing.T) {
	w := newRenameWorld(t)
	approvalsBefore, _ := w.v.Approvals()
	plan, res := w.rename(t, RenameCredential, "vault-reader", "reader", false)
	wantStates := map[string]string{"second-wiki": "approved", "vault-wiki": "approved", "widened": "open"}
	if got := readerStates(plan.Readers); !reflect.DeepEqual(got, wantStates) || len(res.Carried) != 0 {
		t.Errorf("readers = %v, carried = %v, want nothing carried", got, res.Carried)
	}
	if !reflect.DeepEqual(plan.LapsedTokens, []string{"helper"}) {
		t.Errorf("LapsedTokens = %v, want the token whose vorbild reads the credential", plan.LapsedTokens)
	}
	if approvals, _ := w.v.Approvals(); !reflect.DeepEqual(approvals, approvalsBefore) {
		t.Error("an approval was touched without an admin session")
	}
	if held, _ := w.v.HasEntry("reader"); !held {
		t.Error("the entry was not renamed")
	}
	if open := w.openNames(w.pending(t)); !reflect.DeepEqual(open, []string{"second-wiki", "vault-wiki", "widened"}) {
		t.Errorf("open = %v, want every reader open", open)
	}
}

// A forward credential's rename carries the approval of a connection that releases it.
func TestRenameForwardCredentialCarriesTheReleasingConnection(t *testing.T) {
	text := strings.Replace(renameConfig, "    mail-key:\n", "    shared:\n        type: vault\n        forward: true\n"+
		"        fields:\n            - user\n    mail-key:\n", 1)
	text = strings.Replace(text, "        credential: vault-reader\n        effects:\n            - read\n    second-wiki:",
		"        credential: vault-reader\n        forward_secrets:\n            - shared\n        effects:\n"+
			"            - read\n    second-wiki:", 1)
	w := newRenameWorldWith(t, text)
	plan, _ := w.rename(t, RenameCredential, "shared", "login", true)
	if !reflect.DeepEqual(plan.References, []string{"connections.vault-wiki.forward_secrets"}) ||
		!reflect.DeepEqual(readerStates(plan.Readers), map[string]string{"vault-wiki": "approved,carried"}) {
		t.Errorf("plan = %v, %v", plan.References, plan.Readers)
	}
	if len(plan.EnvVars) != 0 {
		t.Errorf("EnvVars = %v, want none: a forward credential reads no environment", plan.EnvVars)
	}
	if _, open := OpenChange(w.pending(t), "vault-wiki"); open {
		t.Error("the releasing connection is open")
	}
	approvals, _ := w.v.Approvals()
	if forward := approvals["vault-wiki"].Scope.Forward; len(forward) != 1 || forward[0].Name != "login" {
		t.Errorf("forward = %v", forward)
	}
}

func TestRenameKeyringCredentialMovesItsKeys(t *testing.T) {
	w := newRenameWorld(t)
	secrets := newFakeSecrets()
	secrets.entries["mail-key.api-key"] = "syn-mail"
	secrets.entries["unrelated.api-key"] = "syn-other"
	svc := w.keyringService(secrets)
	vaultBefore := files(t, w.v.Dir())

	plan, err := svc.PlanRename(RenameCredential, "mail-key", "mail-cred", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.KeyringRoles, []string{"api-key"}) || plan.VaultEntry ||
		!reflect.DeepEqual(plan.References, []string{"connections.mail.credential"}) ||
		!reflect.DeepEqual(readerStates(plan.Readers), map[string]string{"mail": ""}) {
		t.Errorf("plan = %+v", plan)
	}
	secrets.sets = nil
	res, err := svc.Rename(context.Background(), plan)
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("Rename() = %+v, %v", res, err)
	}
	want := map[string]string{"mail-cred.api-key": "syn-mail", "unrelated.api-key": "syn-other"}
	if !reflect.DeepEqual(secrets.entries, want) {
		t.Errorf("keyring = %v, want the key moved and nothing else", secrets.entries)
	}
	if !reflect.DeepEqual(secrets.sets, []string{"mail-cred.api-key"}) {
		t.Errorf("writes = %v, want one write under the new name", secrets.sets)
	}
	if got := withoutLogs(files(t, w.v.Dir())); !reflect.DeepEqual(got, withoutLogs(vaultBefore)) {
		t.Error("the vault changed beyond its log")
	}
	if after := w.load(t); after.Connections["mail"].Credential != "mail-cred" {
		t.Errorf("connection = %+v", after.Connections["mail"])
	}
}

// withoutLogs is files without the invocation log.
func withoutLogs(files map[string]string) map[string]string {
	out := map[string]string{}
	for name, data := range files {
		if !strings.HasPrefix(name, "logs/") {
			out[name] = data
		}
	}
	return out
}

// garblingSecrets is a keyring that stores a key under the name garbled but returns something else for it.
type garblingSecrets struct {
	*fakeSecrets
	garbled string
}

func (g garblingSecrets) StoreValue(ctx context.Context, credential, role string) (string, secret.StoreState) {
	value, state := g.fakeSecrets.StoreValue(ctx, credential, role)
	if credential == g.garbled && state == secret.StoreHolds {
		return value + "-garbled", state
	}
	return value, state
}

func TestRenameKeyringCredentialReadsEachKeyBackBeforeTheCommit(t *testing.T) {
	w := newRenameWorld(t)
	fake := newFakeSecrets()
	fake.entries["mail-key.api-key"] = "syn-mail"
	fake.vault = w.v
	secrets := garblingSecrets{fakeSecrets: fake, garbled: "mail-cred"}
	svc := New(config.NewStore(w.path, setupCatalog{}), secrets, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
	plan, err := svc.PlanRename(RenameCredential, "mail-key", "mail-cred", true)
	if err != nil {
		t.Fatal(err)
	}
	before := files(t, w.dir)
	_, err = svc.Rename(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "does not return the secret just stored under mail-cred/api-key") ||
		strings.Contains(err.Error(), "syn-mail") {
		t.Fatalf("Rename() = %v, want the failed read-back without the secret", err)
	}
	if !reflect.DeepEqual(fake.sets, []string{"mail-cred.api-key"}) {
		t.Errorf("writes = %v, want the one write under the new name", fake.sets)
	}
	if !reflect.DeepEqual(fake.entries, map[string]string{"mail-key.api-key": "syn-mail"}) {
		t.Errorf("keyring = %v, want the new key removed again", fake.entries)
	}
	if got := files(t, w.dir); !reflect.DeepEqual(got, before) {
		t.Error("a failed rename changed a file")
	}
}

func TestRenameCredentialRollsBackWhenTheCommitFails(t *testing.T) {
	w := newRenameWorld(t)
	w.queuePending(t, "vault-reader", "key-id", "synthetic-queued")
	secrets := newFakeSecrets()
	secrets.entries["mail-key.api-key"] = "syn-mail"
	svc := w.keyringService(secrets)
	original := saveRename
	t.Cleanup(func() { saveRename = original })
	saveRename = func(func(*config.Config) error, *config.Config) error { return errors.New("disk full") }

	for _, c := range []struct{ old, name string }{{"vault-reader", "reader"}, {"mail-key", "mail-cred"}} {
		plan, err := svc.PlanRename(RenameCredential, c.old, c.name, true)
		if err != nil {
			t.Fatal(err)
		}
		before := files(t, w.dir)
		approvalsBefore, _ := w.v.Approvals()
		if _, err := svc.Rename(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("Rename(%s) = %v, want the write error", c.old, err)
		}
		if got := files(t, w.dir); !reflect.DeepEqual(got, before) {
			t.Errorf("%s: the configuration, the vault or the backups differ from before", c.old)
		}
		if approvals, _ := w.v.Approvals(); !reflect.DeepEqual(approvals, approvalsBefore) {
			t.Errorf("%s: approvals differ in this process", c.old)
		}
		if held, _ := w.v.HasEntry("vault-reader"); !held {
			t.Errorf("%s: the vault entry is not back in this process", c.old)
		}
	}
	if !reflect.DeepEqual(secrets.entries, map[string]string{"mail-key.api-key": "syn-mail"}) {
		t.Errorf("keyring = %v, want the new key removed again", secrets.entries)
	}
}

func TestRenameEnvCredentialChangesOnlyTheConfiguration(t *testing.T) {
	text := strings.Replace(renameConfig, "    mail-key:\n        type: keyring\n",
		"    mail-key:\n        type: env\n        provider: mail\n        values:\n            api-key: MAIL_API_KEY\n", 1)
	w := newRenameWorldWith(t, text)
	vaultBefore := files(t, w.v.Dir())
	plan, _ := w.rename(t, RenameCredential, "mail-key", "mail-env", true)
	if len(plan.EnvVars) != 0 || len(plan.KeyringRoles) != 0 || plan.VaultEntry {
		t.Errorf("plan = %+v, want nothing outside the configuration", plan.CredentialPlan)
	}
	if outside := strings.Join(plan.Outside, "\n"); !strings.Contains(outside, "MAIL_API_KEY") ||
		!strings.Contains(outside, "stay as they are") {
		t.Errorf("Outside = %v", plan.Outside)
	}
	if got := withoutLogs(files(t, w.v.Dir())); !reflect.DeepEqual(got, withoutLogs(vaultBefore)) {
		t.Error("the vault changed beyond its log")
	}
	if after := w.load(t); after.Credentials["mail-env"].Values["api-key"] != "MAIL_API_KEY" {
		t.Errorf("credential = %+v", after.Credentials["mail-env"])
	}
}

func TestRenameCredentialRefusesATakenName(t *testing.T) {
	w := newRenameWorld(t)
	secrets := newFakeSecrets()
	secrets.entries["mail-key.api-key"] = "syn-mail"
	secrets.entries["claimed.api-key"] = "synthetic-foreign"
	svc := w.keyringService(secrets)
	if err := w.v.Set("orphan", "key-id", "synthetic-orphan", nil); err != nil {
		t.Fatal(err)
	}
	w.queuePending(t, "queued", "key-id", "synthetic-queued")
	cases := []struct{ name, old, new, want string }{
		{"configuration", "mail-key", "vault-reader", "exists already"},
		{"vault entry", "vault-reader", "orphan", "vault holds secrets under the name orphan"},
		{"vault entry for a keyring credential", "mail-key", "orphan", "vault holds secrets under the name orphan"},
		{"pending write", "vault-reader", "queued", "pending writes for the name queued"},
		{"keyring key", "mail-key", "claimed", "system keyring holds a secret under claimed/api-key"},
	}
	before := files(t, w.dir)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.PlanRename(RenameCredential, c.old, c.new, true)
			var refused *RenameRefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("PlanRename() = %v, want a refusal with %q", err, c.want)
			}
		})
	}
	if got := files(t, w.dir); !reflect.DeepEqual(got, before) {
		t.Error("a refused rename changed a file")
	}
}

// Renaming back a credential whose readers were left open would approve them unseen.
func TestRenameCredentialRefusesToReviveAnOpenApproval(t *testing.T) {
	w := newRenameWorld(t)
	w.rename(t, RenameCredential, "vault-reader", "reader", false)
	_, err := w.svc.PlanRename(RenameCredential, "reader", "vault-reader", true)
	var refused *RenameRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "valid again unseen") {
		t.Errorf("PlanRename() = %v, want the revival refused", err)
	}
}

func TestRenameCredentialNeedsAnUnlockedVault(t *testing.T) {
	w := newRenameWorld(t)
	w.v.Forget()
	before := files(t, w.dir)
	plan, err := w.svc.PlanRename(RenameCredential, "vault-reader", "reader", true)
	if err != nil || !plan.VaultUnread || plan.VaultEntry || len(plan.Readers) != 3 {
		t.Fatalf("PlanRename() = %+v, %v, want the vault reported unread", plan, err)
	}
	if _, err := w.svc.Rename(context.Background(), plan); !errors.Is(err, vault.ErrNotUnlocked) {
		t.Errorf("Rename() = %v, want vault.ErrNotUnlocked", err)
	}
	if after := files(t, w.dir); !reflect.DeepEqual(after, before) {
		t.Error("a refused rename changed a file")
	}
}
