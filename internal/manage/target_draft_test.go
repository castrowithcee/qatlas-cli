package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Every vault of these tests lives in a fresh temporary directory and every provider is a fake, so nothing
// real is read even when a protection does not hold.

const (
	canarySecret    = "CANARY-DRAFT-SECRET-7f3a"
	canaryDraftName = "CanaryDraftName"
)

func vaultDraft() ConnectionDraft {
	return ConnectionDraft{Provider: "wiki", Service: "wiki-svc", Credential: "vault-cred", Name: canaryDraftName,
		Targets: []string{"spaces/A"}}
}

// draftVault returns an encrypted vault holding canarySecret for vault-cred, unlocked in this process the
// way the surface's admin passphrase unlocks it.
func draftVault(t *testing.T) *vault.Vault {
	t.Helper()
	dir := t.TempDir()
	offer := func(string) (string, error) { return adminPassphrase, nil }
	if err := vault.New(dir).Set("vault-cred", "token", canarySecret, offer); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	if err := VerifyAdmin(v, adminPassphrase); err != nil {
		t.Fatal(err)
	}
	return v
}

func grantedProof(t *testing.T) AdminProof {
	t.Helper()
	session := NewAdminSession(false, nil)
	session.Grant(time.Minute)
	proof, ok := session.Authorize(time.Minute)
	if !ok {
		t.Fatal("an active session refused proof")
	}
	return proof
}

// vaultState is what a target choice must leave as it found it: the approvals, the encrypted document,
// the pending entries and the configuration file.
type vaultState struct {
	approvals map[string]vault.Approval
	secrets   []byte
	pending   []string
	config    []byte
}

func stateOf(t *testing.T, v *vault.Vault, configPath string) vaultState {
	t.Helper()
	approvals, err := v.Approvals()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.Dir(), "secrets.age"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var pending []string
	entries, _ := os.ReadDir(filepath.Join(v.Dir(), "pending"))
	for _, e := range entries {
		pending = append(pending, e.Name())
	}
	return vaultState{approvals: approvals, secrets: data, pending: pending, config: cfg}
}

func choiceCode(err error) string {
	var choice *TargetChoiceError
	if errors.As(err, &choice) {
		return choice.Code
	}
	return ""
}

func TestDraftTargetsReadTheVaultWithAdminProofAndApproveNothing(t *testing.T) {
	v := draftVault(t)
	f := newBrowseFixture(t, v)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{spaceEntry("A"), {Target: "bad", Name: "x", Kind: "space"}}}, nil
	}
	f.url = func(string, string) ([]config.TargetEntry, error) { return []config.TargetEntry{spaceEntry("B")}, nil }
	ctx := context.Background()
	before := stateOf(t, v, f.path)

	_, _, err := f.svc.ListDraftTargets(ctx, AdminProof{}, vaultDraft(), config.TargetListRequest{Kind: "space"})
	if choiceCode(err) != "admin-required" {
		t.Errorf("list without proof: err = %v, want admin-required", err)
	}
	_, _, err = f.svc.ResolveDraftTargetURL(ctx, AdminProof{}, vaultDraft(), "https://wiki.example.test/x")
	if choiceCode(err) != "admin-required" {
		t.Errorf("resolve without proof: err = %v, want admin-required", err)
	}
	if f.calls != 0 || f.seen != "" {
		t.Fatalf("the provider was read without proof: %d calls", f.calls)
	}

	page, _, err := f.svc.ListDraftTargets(ctx, grantedProof(t), vaultDraft(), config.TargetListRequest{Kind: "space"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Target != "spaces/A" {
		t.Fatalf("list with proof = %+v, %v", page, err)
	}
	if f.seen != canarySecret {
		t.Error("the read did not get the credential's secret")
	}
	if f.resolved.Name != "" || f.resolved.ID != "" || f.resolved.Target != "spaces/A" ||
		f.resolved.BaseURL != "https://wiki.example.test" || f.resolved.Credential != "vault-cred" {
		t.Errorf("draft resolved as %+v", f.resolved)
	}
	f.seen = ""
	entries, _, err := f.svc.ResolveDraftTargetURL(ctx, grantedProof(t), vaultDraft(), "https://wiki.example.test/x")
	if err != nil || len(entries) != 1 || f.seen != canarySecret {
		t.Fatalf("resolve with proof = %+v, %v", entries, err)
	}

	if after := stateOf(t, v, f.path); !reflect.DeepEqual(before, after) {
		t.Errorf("a draft's choice changed the vault or the configuration: approvals %v -> %v", before.approvals, after.approvals)
	}
	cfg, err := f.svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Connections[canaryDraftName]; ok {
		t.Error("the draft was saved")
	}

	// The ordinary way to the same secret is still closed: the draft as a connection is not approved.
	resolved, err := draftResolved(cfg, vaultDraft())
	if err != nil {
		t.Fatal(err)
	}
	resolved.Name = canaryDraftName
	var approval *secret.ApprovalRequiredError
	if _, err := f.own.Resolve(secret.ForConnection(ctx, resolved), "vault-cred", resolved.Secrets, "token"); !errors.As(err, &approval) {
		t.Errorf("ordinary resolve of the draft = %v, want approval-required", err)
	}
	if err := f.own.Usable(ctx, resolved); !errors.As(err, &approval) {
		t.Errorf("Usable() of the draft = %v, want approval-required", err)
	}
}

func TestDraftTargetsRefuseABadDraft(t *testing.T) {
	f := newBrowseFixture(t, nil)
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }
	ctx := context.Background()
	bad := vaultDraft()
	bad.Targets = []string{"spaces/A\n"}
	if _, _, err := f.svc.ListDraftTargets(ctx, AdminProof{}, bad, config.TargetListRequest{Kind: "space"}); choiceCode(err) != "invalid-request" {
		t.Errorf("control character in a target: err = %v, want invalid-request", err)
	}
	other := vaultDraft()
	other.Service = "no-such-service"
	var unknown *UnknownEntryError
	if _, _, err := f.svc.ListDraftTargets(ctx, AdminProof{}, other, config.TargetListRequest{Kind: "space"}); !errors.As(err, &unknown) {
		t.Errorf("unknown service: err = %v", err)
	}
	if f.calls != 0 {
		t.Error("a bad draft reached the provider")
	}
}

func TestDraftTargetsNeedSavedSecrets(t *testing.T) {
	v := draftVault(t)
	// A secret stored by another process while the vault is locked there waits as a pending entry.
	if err := vault.New(filepath.Dir(v.Dir())).Set("pending-cred", "token", canarySecret, nil); err != nil {
		t.Fatal(err)
	}
	f := newBrowseFixture(t, v)
	cfg, err := f.svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Credentials["empty-cred"] = config.Credential{Provider: "wiki", Type: config.CredentialTypeVault}
	cfg.Credentials["pending-cred"] = config.Credential{Provider: "wiki", Type: config.CredentialTypeVault}
	if err := f.svc.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }

	for name, draft := range map[string]ConnectionDraft{
		"new vault credential":   {Provider: "wiki", Service: "wiki-svc", NewCredential: true, Credential: "fresh", Storage: config.CredentialTypeVault},
		"new keyring credential": {Provider: "wiki", Service: "wiki-svc", NewCredential: true, Credential: "fresh", Storage: config.CredentialTypeKeyring},
		"vault without entry":    {Provider: "wiki", Service: "wiki-svc", Credential: "empty-cred"},
		"pending entry":          {Provider: "wiki", Service: "wiki-svc", Credential: "pending-cred"},
		"keyring without entry":  {Provider: "wiki", Service: "wiki-svc", Credential: "wiki-cred"},
	} {
		f.seen = ""
		_, _, err := f.svc.ListDraftTargets(context.Background(), grantedProof(t), draft, config.TargetListRequest{Kind: "space"})
		if choiceCode(err) != "unsaved-secret" || !strings.Contains(err.Error(), "save the credential's secrets first") {
			t.Errorf("%s: err = %v, want unsaved-secret", name, err)
		}
		if f.seen != "" || strings.Contains(err.Error(), canarySecret) {
			t.Errorf("%s: a secret was read", name)
		}
	}
}

func TestDraftTargetsWithoutEncryptedVaultReadLikeAConnectionTest(t *testing.T) {
	plain := vault.New(t.TempDir())
	if err := plain.Set("vault-cred", "token", canarySecret, nil); err != nil {
		t.Fatal(err)
	}
	f := newBrowseFixture(t, plain)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }
	if _, _, err := f.svc.ListDraftTargets(context.Background(), AdminProof{}, vaultDraft(),
		config.TargetListRequest{Kind: "space"}); err != nil || f.seen != canarySecret {
		t.Errorf("unencrypted vault: err = %v, secret read %v", err, f.seen == canarySecret)
	}
}

// A vault the surface did not unlock in its own process is never read for a draft, even while the admin
// session is active; a vault process is never asked (see target_unix_test.go for one that runs).
func TestDraftTargetsNeedTheVaultUnlockedHere(t *testing.T) {
	unlocked := draftVault(t)
	locked := vault.New(filepath.Dir(unlocked.Dir()))
	f := newBrowseFixture(t, locked)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }
	_, _, err := f.svc.ListDraftTargets(context.Background(), grantedProof(t), vaultDraft(), config.TargetListRequest{Kind: "space"})
	if choiceCode(err) != "vault-locked-here" || f.calls != 0 {
		t.Errorf("err = %v, calls %d, want vault-locked-here without a read", err, f.calls)
	}
}

// An agent token that covers the draft as a vorbild never stands in for the admin session, and does not open
// the ordinary way either.
func TestDraftTargetsAreOutOfReachOfAnAgentToken(t *testing.T) {
	v := draftVault(t)
	f := newBrowseFixture(t, v)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }
	cfg, err := f.svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	model, err := draftResolved(cfg, vaultDraft())
	if err != nil {
		t.Fatal(err)
	}
	model.Name = "model"
	if err := v.Approve([]vault.Scope{secret.ScopeOf(model)}); err != nil {
		t.Fatal(err)
	}
	token, err := v.CreateToken("agent", []string{"model"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(vault.AgentTokenEnv, token.Value)
	before := stateOf(t, v, f.path)

	_, _, err = f.svc.ListDraftTargets(context.Background(), AdminProof{}, vaultDraft(), config.TargetListRequest{Kind: "space"})
	if choiceCode(err) != "admin-required" || f.calls != 0 {
		t.Errorf("with an agent token: err = %v, calls %d, want admin-required without a read", err, f.calls)
	}
	draft, _ := draftResolved(cfg, vaultDraft())
	draft.Name = canaryDraftName
	var approval *secret.ApprovalRequiredError
	if _, err := f.own.Resolve(secret.ForConnection(context.Background(), draft), "vault-cred", draft.Secrets, "token"); !errors.As(err, &approval) {
		t.Errorf("ordinary resolve with an agent token = %v, want approval-required", err)
	}
	if after := stateOf(t, v, f.path); !reflect.DeepEqual(before, after) {
		t.Error("the vault changed")
	}
}

func TestDraftTargetChoiceLogsTheDraftOnly(t *testing.T) {
	v := draftVault(t)
	f := newBrowseFixture(t, v)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{{Target: canaryTarget, Name: canaryName, Kind: "space"}}}, nil
	}
	f.url = func(string, string) ([]config.TargetEntry, error) { return nil, nil }
	ctx := context.Background()
	draft := vaultDraft()
	draft.Targets = []string{canaryTarget}
	if _, warn, err := f.svc.ListDraftTargets(ctx, grantedProof(t), draft,
		config.TargetListRequest{Kind: "space", Query: canaryQuery}); err != nil || warn != "" {
		t.Fatalf("ListDraftTargets() = %q, %v", warn, err)
	}
	if _, warn, err := f.svc.ResolveDraftTargetURL(ctx, grantedProof(t), draft,
		"https://wiki.example.test/spaces/CANARYTARGET?q="+canaryQuery); err != nil || warn != "" {
		t.Fatalf("ResolveDraftTargetURL() = %q, %v", warn, err)
	}
	empty := draft
	empty.Credential = "wiki-cred"
	if _, _, err := f.svc.ListDraftTargets(ctx, grantedProof(t), empty, config.TargetListRequest{Kind: "space"}); choiceCode(err) != "unsaved-secret" {
		t.Fatalf("keyring without entry: %v", err)
	}

	dir := filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")
	files, err := os.ReadDir(dir)
	if err != nil || len(files) == 0 {
		t.Fatalf("no log written: %v", err)
	}
	var logs strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		logs.Write(data)
	}
	text := logs.String()
	for _, want := range []string{`"path":"tui"`, `"connection":"(draft)"`, `"operation":"config.target.draft.list:space"`,
		`"operation":"config.target.draft.resolve"`, `"effect":"read"`, `"result":"success"`, `"result":"unsaved-secret"`} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %s:\n%s", want, text)
		}
	}
	for _, leak := range []string{canarySecret, canaryDraftName, "CANARYTARGET", canaryName, canaryQuery,
		"wiki.example.test", "vault-cred", "wiki-svc", "connection_id", `"operation":"config.target.list`} {
		if strings.Contains(text, leak) {
			t.Errorf("log holds %q:\n%s", leak, text)
		}
	}
}

func TestDraftTargetsWithoutConfigurationFile(t *testing.T) {
	f := newBrowseFixture(t, nil)
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	f.list = func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: []config.TargetEntry{spaceEntry("A")}}, nil
	}
	draft := ConnectionDraft{Provider: "wiki", NewService: true, Service: "first", BaseURL: "https://wiki.example.test",
		NewCredential: true, Credential: "first", Storage: config.CredentialTypeEnv,
		EnvNames: map[string]string{"token": "QTEST_TOKEN"}}
	page, _, err := f.svc.ListDraftTargets(context.Background(), AdminProof{}, draft, config.TargetListRequest{Kind: "space"})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("ListDraftTargets() = %+v, %v", page, err)
	}
	if _, err := os.Stat(f.path); !os.IsNotExist(err) {
		t.Errorf("the configuration was written: %v", err)
	}
	if _, _, err := f.svc.ListTargets(context.Background(), AdminProof{}, "docs", config.TargetListRequest{Kind: "space"}); err == nil {
		t.Error("a saved connection was chosen without a configuration file")
	}
}
