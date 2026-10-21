package manage

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

var errStopped = errors.New("stopped before the commit")

// stopBeforeCommit runs the rename of old to name the way a crash between the secrets and the configuration
// leaves it: nothing after the commit point runs, and nothing is undone.
func stopBeforeCommit(t *testing.T, svc *Service, old, name string) {
	t.Helper()
	original := saveRename
	t.Cleanup(func() { saveRename = original })
	saveRename = func(func(*config.Config) error, *config.Config) error { panic(errStopped) }
	plan, err := svc.PlanRename(RenameCredential, old, name, true)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r != errStopped {
				t.Fatalf("Rename() did not stop at the commit: %v", r)
			}
		}()
		_, _ = svc.Rename(context.Background(), plan)
	}()
	saveRename = original
}

// approvalsWithoutIDs drops the recorded connection id, which a carried approval gains.
func approvalsWithoutIDs(approvals map[string]vault.Approval) map[string]vault.Approval {
	out := map[string]vault.Approval{}
	for name, a := range approvals {
		a.ConnectionID = ""
		out[name] = a
	}
	return out
}

func TestAStoppedCredentialRenameIsNamedAndFinished(t *testing.T) {
	w := newRenameWorld(t)
	entryID, _, _ := w.v.CredentialID("vault-reader")
	stopBeforeCommit(t, w.svc, "vault-reader", "reader")

	cfg := w.load(t)
	if _, ok := cfg.Credentials["vault-reader"]; !ok {
		t.Fatal("the configuration was saved")
	}
	names, err := w.v.KnownNames()
	if err != nil || !reflect.DeepEqual(names.Renamed, map[string]string{"reader": "vault-reader"}) {
		t.Errorf("Renamed after the stop = %v, %v, want the mark of the stopped rename", names.Renamed, err)
	}
	want := []CredentialLeftover{{Credential: "vault-reader", New: "reader", Approvals: []string{"second-wiki", "vault-wiki"}}}
	if left, err := CredentialLeftovers(cfg, w.v); err != nil || !reflect.DeepEqual(left, want) {
		t.Errorf("CredentialLeftovers() = %+v, %v, want %+v", left, err, want)
	}
	// Another run, which unlocks the vault from its files, finds the same.
	fresh := vault.New(w.dir)
	if _, err := fresh.Unlock(renamePassphrase); err != nil {
		t.Fatal(err)
	}
	if left, err := CredentialLeftovers(cfg, fresh); err != nil || !reflect.DeepEqual(left, want) {
		t.Errorf("CredentialLeftovers() from the files = %+v, %v", left, err)
	}

	plan, res := w.rename(t, RenameCredential, "vault-reader", "reader", true)
	if !plan.Resumes || plan.VaultEntry || len(plan.LapsedTokens) != 0 {
		t.Errorf("plan = %+v, want a resumed rename", plan.CredentialPlan)
	}
	wantStates := map[string]string{"second-wiki": "approved,carried", "vault-wiki": "approved,carried", "widened": "open"}
	if got := readerStates(plan.Readers); !reflect.DeepEqual(got, wantStates) {
		t.Errorf("readers = %v, want %v", got, wantStates)
	}
	if !reflect.DeepEqual(res.Carried, []string{"second-wiki", "vault-wiki"}) {
		t.Errorf("carried = %v", res.Carried)
	}

	after := w.load(t)
	if _, ok := after.Credentials["vault-reader"]; ok || after.Credentials["reader"].ID != plan.ID ||
		after.Connections["second-wiki"].Credential != "reader" {
		t.Errorf("credentials = %v", after.Credentials)
	}
	if id, held, _ := w.v.CredentialID("reader"); !held || id != entryID {
		t.Errorf("entry id = %q, %v, want %q kept", id, held, entryID)
	}
	report := w.pending(t)
	if open := w.openNames(report); !reflect.DeepEqual(open, []string{"widened"}) || len(report.Stale) != 0 {
		t.Errorf("open = %v, stale = %v, want only the connection that was open before", open, report.Stale)
	}
	if left, err := CredentialLeftovers(after, w.v); err != nil || len(left) != 0 {
		t.Errorf("CredentialLeftovers() = %+v, %v, want none", left, err)
	}
	if names, err := w.v.KnownNames(); err != nil || len(names.Renamed) != 0 {
		t.Errorf("Renamed after the resume = %v, %v, want none", names.Renamed, err)
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

func TestAStoppedCredentialRenameIsPutBack(t *testing.T) {
	w := newRenameWorld(t)
	entryID, _, _ := w.v.CredentialID("vault-reader")
	configBefore, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	approvalsBefore, _ := w.v.Approvals()
	stopBeforeCommit(t, w.svc, "vault-reader", "reader")

	if names, _ := w.v.KnownNames(); names.Renamed["reader"] != "vault-reader" {
		t.Errorf("Renamed after the stop = %v, want the mark of the stopped rename", names.Renamed)
	}
	plan, err := w.svc.PlanRevertRename("vault-reader", "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := map[string]string{"second-wiki": "approved,carried", "vault-wiki": "approved,carried", "widened": "open"}
	if !plan.Revert || !plan.VaultEntry || !reflect.DeepEqual(readerStates(plan.Readers), wantStates) {
		t.Errorf("plan = %+v, readers = %v", plan.CredentialPlan, readerStates(plan.Readers))
	}
	res, err := w.svc.Rename(context.Background(), plan)
	if err != nil || len(res.Warnings) != 0 || len(res.VaultCopies) == 0 {
		t.Fatalf("Rename() = %+v, %v", res, err)
	}
	if !reflect.DeepEqual(res.Carried, []string{"second-wiki", "vault-wiki"}) {
		t.Errorf("carried back = %v", res.Carried)
	}

	if configAfter, _ := os.ReadFile(w.path); string(configAfter) != string(configBefore) {
		t.Error("the configuration changed")
	}
	if id, held, _ := w.v.CredentialID("vault-reader"); !held || id != entryID {
		t.Errorf("entry id = %q, %v, want %q back under the old name", id, held, entryID)
	}
	if held, _ := w.v.HasEntry("reader"); held {
		t.Error("the vault still holds the new name")
	}
	if names, err := w.v.KnownNames(); err != nil || len(names.Renamed) != 0 {
		t.Errorf("Renamed after the revert = %v, %v, want none", names.Renamed, err)
	}
	approvals, _ := w.v.Approvals()
	if !reflect.DeepEqual(approvalsWithoutIDs(approvals), approvalsWithoutIDs(approvalsBefore)) {
		t.Errorf("approvals = %+v, want them as before", approvals)
	}
	report := w.pending(t)
	if open := w.openNames(report); !reflect.DeepEqual(open, []string{"widened"}) || len(report.Stale) != 0 {
		t.Errorf("open = %v, stale = %v, want only the connection that was open before", open, report.Stale)
	}
	if left, _ := CredentialLeftovers(w.load(t), w.v); len(left) != 0 {
		t.Errorf("CredentialLeftovers() = %+v, want none", left)
	}
	_, err = w.svc.PlanRevertRename("vault-reader", "reader", true)
	var refused *RenameRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "nothing is left over") {
		t.Errorf("PlanRevertRename() again = %v, want nothing left to put back", err)
	}
}

// A forward credential's rest carries the approval of the connection that releases it back.
func TestAStoppedForwardCredentialRenameIsPutBack(t *testing.T) {
	text := strings.Replace(renameConfig, "    mail-key:\n", "    shared:\n        type: vault\n        forward: true\n"+
		"        fields:\n            - user\n    mail-key:\n", 1)
	text = strings.Replace(text, "        credential: vault-reader\n        effects:\n            - read\n    second-wiki:",
		"        credential: vault-reader\n        forward_secrets:\n            - shared\n        effects:\n"+
			"            - read\n    second-wiki:", 1)
	w := newRenameWorldWith(t, text)
	if err := w.v.Set("shared", "user", "syn-user", nil); err != nil {
		t.Fatal(err)
	}
	stopBeforeCommit(t, w.svc, "shared", "login")
	want := []CredentialLeftover{{Credential: "shared", New: "login", Approvals: []string{"vault-wiki"}}}
	if left, err := CredentialLeftovers(w.load(t), w.v); err != nil || !reflect.DeepEqual(left, want) {
		t.Errorf("CredentialLeftovers() = %+v, %v, want %+v", left, err, want)
	}
	plan, err := w.svc.PlanRevertRename("shared", "login", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Rename(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	approvals, _ := w.v.Approvals()
	if forward := approvals["vault-wiki"].Scope.Forward; len(forward) != 1 || forward[0].Name != "shared" {
		t.Errorf("forward = %v, want the old name back", forward)
	}
	if _, open := OpenChange(w.pending(t), "vault-wiki"); open {
		t.Error("the releasing connection is open")
	}
	if held, _ := w.v.HasEntry("shared"); !held {
		t.Error("the entry is not back under the old name")
	}
}

func TestAStoppedKeyringRenameIsFinishedOrPutBack(t *testing.T) {
	for _, revert := range []bool{false, true} {
		w := newRenameWorld(t)
		secrets := newFakeSecrets()
		secrets.entries["mail-key.api-key"] = "syn-mail"
		svc := w.keyringService(secrets)
		stopBeforeCommit(t, svc, "mail-key", "mail-cred")
		if want := map[string]string{"mail-key.api-key": "syn-mail", "mail-cred.api-key": "syn-mail"}; !reflect.DeepEqual(secrets.entries, want) {
			t.Fatalf("keyring = %v, want the copy the stopped run wrote", secrets.entries)
		}
		configBefore, _ := os.ReadFile(w.path)
		secrets.sets = nil

		var plan *RenamePlan
		var err error
		want := map[string]string{"mail-cred.api-key": "syn-mail"}
		if revert {
			plan, err = svc.PlanRevertRename("mail-key", "mail-cred", true)
			want = map[string]string{"mail-key.api-key": "syn-mail"}
		} else {
			plan, err = svc.PlanRename(RenameCredential, "mail-key", "mail-cred", true)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(plan.KeyringRoles, []string{"api-key"}) || plan.Revert != revert || plan.Resumes == revert {
			t.Errorf("revert %v: plan = %+v", revert, plan.CredentialPlan)
		}
		res, err := svc.Rename(context.Background(), plan)
		if err != nil || len(res.Warnings) != 0 {
			t.Fatalf("revert %v: Rename() = %+v, %v", revert, res, err)
		}
		if !reflect.DeepEqual(secrets.entries, want) || len(secrets.sets) != 0 {
			t.Errorf("revert %v: keyring = %v, writes = %v, want %v without a write", revert, secrets.entries,
				secrets.sets, want)
		}
		configAfter, _ := os.ReadFile(w.path)
		if changed := string(configAfter) != string(configBefore); changed == revert {
			t.Errorf("revert %v: configuration changed = %v", revert, changed)
		}
	}
}

// Only what a rename of this very credential left is taken over or put back; anything else under the new
// name is refused by both, and is no leftover.
func TestAForeignEntryUnderTheNewNameIsNoLeftover(t *testing.T) {
	w := newRenameWorld(t)
	secrets := newFakeSecrets()
	secrets.entries["mail-key.api-key"] = "syn-mail"
	secrets.entries["mail-cred.api-key"] = "syn-other"
	svc := w.keyringService(secrets)
	id := w.load(t).Credentials["vault-reader"].ID
	if _, err := w.v.SetUndoableWithID("foreign", "another-id", "key-id", "syn-foreign", nil); err != nil {
		t.Fatal(err)
	}
	// Records the credential's id, but the vault still holds the credential's own entry.
	if _, err := w.v.SetUndoableWithID("twin", id, "key-id", "syn-twin", nil); err != nil {
		t.Fatal(err)
	}
	if left, err := CredentialLeftovers(w.load(t), w.v); err != nil || len(left) != 0 {
		t.Errorf("CredentialLeftovers() = %+v, %v, want none", left, err)
	}
	before := files(t, w.dir)
	for _, c := range []struct{ old, name, rename, revert string }{
		{"vault-reader", "foreign", "vault holds secrets under the name foreign", "not left over"},
		{"vault-reader", "twin", "vault holds secrets under the name twin", "not left over"},
		{"mail-key", "mail-cred", "system keyring holds a secret under mail-cred/api-key", "nothing is left over"},
	} {
		var refused *RenameRefusedError
		if _, err := svc.PlanRename(RenameCredential, c.old, c.name, true); !errors.As(err, &refused) ||
			!strings.Contains(err.Error(), c.rename) {
			t.Errorf("PlanRename(%s, %s) = %v, want %q", c.old, c.name, err, c.rename)
		}
		if _, err := svc.PlanRevertRename(c.old, c.name, true); !errors.As(err, &refused) ||
			!strings.Contains(err.Error(), c.revert) {
			t.Errorf("PlanRevertRename(%s, %s) = %v, want %q", c.old, c.name, err, c.revert)
		}
	}
	if got := files(t, w.dir); !reflect.DeepEqual(got, before) {
		t.Error("a refused plan changed a file")
	}
}
