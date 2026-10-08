package manage

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const canary = "canary-secret-value"

// credentialFixture is a service over a configuration with a wiki keyring credential "reader", a wiki vault
// credential "kept", an env credential "vars", a payload credential "shared" and a credential "free" that
// names no provider.
type credentialFixture struct {
	svc     *Service
	cfg     *config.Config
	rev     config.Revision
	secrets *fakeSecrets
	store   *config.Store
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	store := config.NewStore(filepath.Join(t.TempDir(), "qatlas", "config.yaml"), roleCatalog{})
	cfg := store.New()
	for name, cred := range map[string]config.Credential{
		"reader": {Provider: "wiki", Type: config.CredentialTypeKeyring},
		"kept":   {Provider: "wiki", Type: config.CredentialTypeVault},
		"vars":   {Provider: "wiki", Type: config.CredentialTypeEnv, Values: map[string]string{"token-id": "A", "token-secret": "B"}},
		"shared": {Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"user", "pass"}},
		"free":   {Type: config.CredentialTypeKeyring},
	} {
		if err := cfg.SetCredential(name, cred); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &credentialFixture{secrets: newFakeSecrets(), store: store}
	f.svc = New(store, f.secrets, connlog.SurfaceWeb, &redact.Redactor{}).WithVaultProcessSupport(false)
	var err error
	if f.cfg, f.rev, err = f.svc.Load(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *credentialFixture) write(name, role, value string, policy ApprovalPolicy) SecretWrite {
	return SecretWrite{Name: name, Entry: f.cfg.Credentials[name], Role: role, Value: value, Approval: policy}
}

func TestCheckSecretTarget(t *testing.T) {
	f := newCredentialFixture(t)
	var (
		notThere    *config.NotThereError
		notStorable *NotStorableError
		unknownRole *UnknownRoleError
		unknownFld  *UnknownFieldError
	)
	tests := []struct {
		name, credential, role string
		as                     any
	}{
		{"accepted role", "reader", "token-id", nil},
		{"accepted vault role", "kept", "token-secret", nil},
		{"no provider accepts every role", "free", "api-key", nil},
		{"forward field", "shared", "pass", nil},
		{"unknown credential", "nope", "token-id", &notThere},
		{"env credential", "vars", "token-id", &notStorable},
		{"role of another provider", "reader", "api-key", &unknownRole},
		{"unknown role", "free", "nope", &unknownRole},
		{"role is no forward field", "shared", "token-id", &unknownFld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CheckSecretTarget(f.cfg, tt.credential, tt.role)
			if tt.as == nil {
				if err != nil {
					t.Fatalf("CheckSecretTarget() = %v, want success", err)
				}
				return
			}
			if err == nil || !errors.As(err, tt.as) {
				t.Fatalf("CheckSecretTarget() = %v, want %T", err, tt.as)
			}
		})
	}
	_, err := CheckSecretTarget(f.cfg, "reader", "api-key")
	for _, want := range []string{`"api-key"`, "reader", "wiki", "token-id, token-secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// A derived provider never widens what a credential accepts.
func TestSecretWriteRoleRuleUsesTheProviderField(t *testing.T) {
	f := newCredentialFixture(t)
	f.cfg.Services = map[string]config.Service{"mail-svc": {Provider: "mail"}}
	f.cfg.Connections = map[string]config.Connection{"c": {Service: "mail-svc", Credential: "free"}}
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("free", "token-id", canary, ApprovalNone)); err != nil {
		t.Fatalf("SetSecret() = %v: a credential without the provider field accepts every role", err)
	}
	cred := f.cfg.Credentials["reader"]
	cred.Provider = "mail"
	f.cfg.Credentials["reader"] = cred
	_, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("reader", "token-id", canary, ApprovalNone))
	var unknown *UnknownRoleError
	if !errors.As(err, &unknown) {
		t.Fatalf("SetSecret() = %v, want the foreign role refused", err)
	}
}

func TestSetSecretKeyring(t *testing.T) {
	f := newCredentialFixture(t)
	out, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("reader", "token-id", canary, ApprovalSweepNewlyOpened))
	if err != nil {
		t.Fatalf("SetSecret() = %v", err)
	}
	if f.secrets.entries["reader.token-id"] != canary {
		t.Errorf("entries = %v, want the secret stored", f.secrets.entries)
	}
	if out.ProcessWarning != "" || len(out.Approval.Approved) != 0 {
		t.Errorf("outcome = %+v, want nothing to hand on or approve for a keyring secret", out)
	}
}

func TestSetSecretRefusesBeforeStoring(t *testing.T) {
	f := newCredentialFixture(t)
	for _, w := range []SecretWrite{
		f.write("reader", "api-key", canary, ApprovalNone),
		f.write("vars", "token-id", canary, ApprovalNone),
		f.write("shared", "pass", "abc", ApprovalNone),
		f.write("kept", "api-key", canary, ApprovalSweepNewlyOpened),
	} {
		_, err := f.svc.SetSecret(context.Background(), f.cfg, w)
		if err == nil {
			t.Errorf("SetSecret(%s.%s) succeeded, want a refusal", w.Name, w.Role)
		}
		if err != nil && strings.Contains(err.Error(), w.Value) && w.Value != "" {
			t.Errorf("error %q carries the secret value", err)
		}
	}
	if len(f.secrets.sets) != 0 {
		t.Errorf("stores written = %v, want none", f.secrets.sets)
	}
}

func TestSetSecretForwardValueError(t *testing.T) {
	f := newCredentialFixture(t)
	_, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("shared", "pass", "abc", ApprovalNone))
	var invalid *ValueError
	if !errors.As(err, &invalid) || !strings.HasPrefix(err.Error(), "credential shared: ") || strings.Contains(err.Error(), `"abc"`) {
		t.Fatalf("SetSecret() = %v, want a refusal naming the credential and no value", err)
	}
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("shared", "pass", "abcd", ApprovalNone)); err != nil {
		t.Fatalf("SetSecret() = %v, want success for four characters", err)
	}
}

func TestSetSecretStoreErrorIsReturnedAsIs(t *testing.T) {
	f := newCredentialFixture(t)
	want := errors.New("store is locked")
	f.secrets.setErr["token-id"] = want
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("reader", "token-id", canary, ApprovalNone)); err != want {
		t.Errorf("SetSecret() = %v, want the store's own error", err)
	}
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("kept", "token-id", canary, ApprovalNone)); err != want {
		t.Errorf("SetSecret() = %v, want the vault's own error", err)
	}
}

func TestSetSecretWithoutStore(t *testing.T) {
	f := newCredentialFixture(t)
	svc := New(f.store, nil, connlog.SurfaceCLI, nil)
	if _, err := svc.SetSecret(context.Background(), f.cfg, f.write("reader", "token-id", canary, ApprovalNone)); err == nil {
		t.Error("SetSecret() succeeded without a store")
	}
	if _, err := svc.DeleteSecret(context.Background(), f.cfg, f.write("reader", "token-id", "", ApprovalNone)); err == nil {
		t.Error("DeleteSecret() succeeded without a store")
	}
}

// A vault secret goes to the vault, not the keyring, whatever the saved type: the entry the surface names
// decides.
func TestSetSecretFollowsTheEntryType(t *testing.T) {
	f := newCredentialFixture(t)
	w := f.write("reader", "token-id", canary, ApprovalNone)
	w.Entry.Type = config.CredentialTypeVault
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, w); err != nil {
		t.Fatalf("SetSecret() = %v", err)
	}
	w.Entry.Type = config.CredentialTypeEnv
	if _, err := f.svc.SetSecret(context.Background(), f.cfg, w); err == nil {
		t.Error("SetSecret() succeeded for an env entry")
	}
}

func TestDeleteSecret(t *testing.T) {
	f := newCredentialFixture(t)
	f.secrets.entries["reader.token-id"] = canary
	f.secrets.entries["kept.token-id"] = canary
	for _, name := range []string{"reader", "kept"} {
		if _, err := f.svc.DeleteSecret(context.Background(), f.cfg, f.write(name, "token-id", "", ApprovalNone)); err != nil {
			t.Fatalf("DeleteSecret(%s) = %v", name, err)
		}
	}
	if len(f.secrets.entries) != 0 {
		t.Errorf("entries = %v, want both removed", f.secrets.entries)
	}
	if _, err := f.svc.DeleteSecret(context.Background(), f.cfg, f.write("reader", "api-key", "", ApprovalNone)); err == nil {
		t.Error("DeleteSecret() accepted a role of another provider")
	}
	if _, err := f.svc.DeleteSecret(context.Background(), f.cfg, f.write("vars", "token-id", "", ApprovalNone)); err == nil {
		t.Error("DeleteSecret() accepted an env credential")
	}
	f.secrets.deleteErr["token-id"] = secret.ErrNoEntry
	if _, err := f.svc.DeleteSecret(context.Background(), f.cfg, f.write("kept", "token-id", "", ApprovalNone)); !errors.Is(err, secret.ErrNoEntry) {
		t.Errorf("DeleteSecret() = %v, want the store's own error", err)
	}
}

// With no vault there is nothing to snapshot or approve, whatever the policy; the write still succeeds.
func TestSecretPoliciesWithoutVaultHandle(t *testing.T) {
	f := newCredentialFixture(t)
	for _, policy := range []ApprovalPolicy{ApprovalNone, ApprovalDirectOnly, ApprovalSweepNewlyOpened} {
		out, err := f.svc.SetSecret(context.Background(), f.cfg, f.write("kept", "token-id", canary, policy))
		if err != nil || len(out.Approval.Approved) != 0 || out.Approval.CheckErr != nil || out.Approval.ApproveErr != nil {
			t.Errorf("policy %d: SetSecret() = %+v, %v", policy, out, err)
		}
	}
}

func TestNeedsPassphraseOffer(t *testing.T) {
	if need, err := NeedsPassphraseOffer(nil); need || !errors.Is(err, ErrNoVault) {
		t.Errorf("nil vault: NeedsPassphraseOffer() = %v, %v, want false and ErrNoVault", need, err)
	}
	v := vault.New(t.TempDir())
	if need, err := NeedsPassphraseOffer(v); err != nil || !need {
		t.Errorf("absent vault: NeedsPassphraseOffer() = %v, %v, want true", need, err)
	}
	if err := v.Set("kept", "token-id", canary, nil); err != nil {
		t.Fatal(err)
	}
	if need, err := NeedsPassphraseOffer(v); err != nil || need {
		t.Errorf("existing vault: NeedsPassphraseOffer() = %v, %v, want false", need, err)
	}
}

func TestHeldInVault(t *testing.T) {
	f := newCredentialFixture(t)
	if got, err := HeldInVault(f.cfg, nil, "kept"); err != nil || got.Locked || len(got.Roles) != 0 {
		t.Errorf("nil vault: HeldInVault() = %+v, %v, want nothing", got, err)
	}
	v := vault.New(t.TempDir())
	if got, err := HeldInVault(f.cfg, v, "kept"); err != nil || got.Locked || len(got.Roles) != 0 {
		t.Errorf("absent vault: HeldInVault() = %+v, %v, want nothing", got, err)
	}
	if err := v.Set("kept", "token-secret", canary, nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("other", "token-id", canary, nil); err != nil {
		t.Fatal(err)
	}
	got, err := HeldInVault(f.cfg, v, "kept")
	if err != nil || got.Locked || !slices.Equal(got.Roles, []string{"token-secret"}) {
		t.Errorf("HeldInVault() = %+v, %v, want only token-secret of kept", got, err)
	}
}

func TestHeldInVaultLocked(t *testing.T) {
	f := newCredentialFixture(t)
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Set("kept", "token-id", canary, func(string) (string, error) { return "passphrase", nil }); err != nil {
		t.Fatal(err)
	}
	locked := vault.New(dir)
	if state, err := locked.State(); err != nil || state != vault.StateLocked {
		t.Skipf("vault state = %v, %v: not encrypted and locked here", state, err)
	}
	got, err := HeldInVault(f.cfg, locked, "kept")
	if err != nil || !got.Locked || len(got.Roles) != 0 {
		t.Errorf("HeldInVault() = %+v, %v, want locked without a read", got, err)
	}
}

type placementSource map[string]secret.Placement

func (p placementSource) Stored(credential, role string) secret.Placement {
	return p[credential+"."+role]
}

func TestPlacementReport(t *testing.T) {
	src := placementSource{
		"reader.token-id":     {Holding: []secret.Source{secret.SourceStore}},
		"reader.token-secret": {Unknown: []secret.Source{secret.SourcePlaintext}, Err: errors.New("cannot read\ncannot read\nfile mode 644")},
	}
	roles := []string{"token-id", "token-secret", "api-key"}
	rep := ReportPlacements(PlacementsOf(src, "reader", roles), roles)
	if rep.Settled() {
		t.Error("Settled() = true, want false")
	}
	if want := []string{"token-id: " + string(secret.SourceStore)}; !slices.Equal(rep.Held, want) {
		t.Errorf("Held = %v, want %v", rep.Held, want)
	}
	if want := []string{"token-secret: " + string(secret.SourcePlaintext)}; !slices.Equal(rep.Unsure, want) {
		t.Errorf("Unsure = %v, want %v", rep.Unsure, want)
	}
	if want := []string{"cannot read", "file mode 644"}; !slices.Equal(rep.Causes, want) {
		t.Errorf("Causes = %v, want each cause once: %v", rep.Causes, want)
	}
	if !ReportPlacements(PlacementsOf(placementSource{}, "reader", roles), roles).Settled() {
		t.Error("an empty answer is not settled")
	}
}

func (f *credentialFixture) newCredential(provider, name, kind string) NewCredential {
	n := NewCredential{Name: name, Provider: provider, Type: kind}
	switch kind {
	case config.CredentialTypeEnv:
		n.EnvNames = map[string]string{"token-id": "ID_VAR", "token-secret": "SECRET_VAR"}
	default:
		n.Secrets = map[string]string{"token-id": "id-" + canary, "token-secret": "secret-" + canary}
	}
	return n
}

func TestCreateCredential(t *testing.T) {
	for _, kind := range []string{config.CredentialTypeKeyring, config.CredentialTypeVault, config.CredentialTypeEnv} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture(t)
			created, err := f.svc.CreateCredential(f.cfg, f.rev, f.newCredential("wiki", "fresh", kind))
			if err != nil {
				t.Fatalf("CreateCredential() = %v", err)
			}
			if _, ok := f.cfg.Credentials["fresh"]; ok {
				t.Error("the configuration passed in was changed")
			}
			saved, err := f.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			got, ok := saved.Credentials["fresh"]
			if !ok || got.Type != kind || got.Provider != "wiki" || created.Config.Credentials["fresh"].Type != kind {
				t.Fatalf("saved credential = %+v, %v", got, ok)
			}
			if kind == config.CredentialTypeEnv {
				if len(f.secrets.entries) != 0 || got.Values["token-id"] != "ID_VAR" {
					t.Errorf("env credential: entries = %v, values = %v, want names only", f.secrets.entries, got.Values)
				}
				return
			}
			if len(f.secrets.entries) != 2 || len(got.Values) != 0 {
				t.Errorf("entries = %v, values = %v, want both secrets stored and none in the file", f.secrets.entries, got.Values)
			}
		})
	}
}

func TestCreateCredentialRefusesInput(t *testing.T) {
	f := newCredentialFixture(t)
	good := func() NewCredential { return f.newCredential("wiki", "fresh", config.CredentialTypeKeyring) }
	tests := []struct {
		name   string
		mutate func(*NewCredential)
		check  func(error) bool
	}{
		{"unknown provider", func(n *NewCredential) { n.Provider = "nope" }, func(e error) bool { return errors.Is(e, ErrNoProvider) }},
		{"empty name", func(n *NewCredential) { n.Name = "" }, func(e error) bool { return errors.Is(e, ErrNameEmpty) }},
		{"taken name", func(n *NewCredential) { n.Name = "reader" }, func(e error) bool {
			var x *ExistsError
			return errors.As(e, &x) && x.Name == "reader"
		}},
		{"no storage", func(n *NewCredential) { n.Type = "" }, func(e error) bool { return errors.Is(e, ErrNoStorage) }},
		{"missing secret", func(n *NewCredential) { delete(n.Secrets, "token-secret") }, func(e error) bool {
			var x *MissingValueError
			return errors.As(e, &x) && x.Role == "token-secret" && !x.Env
		}},
		{"missing variable", func(n *NewCredential) {
			n.Type, n.Secrets, n.EnvNames = config.CredentialTypeEnv, nil, map[string]string{"token-id": "X"}
		}, func(e error) bool {
			var x *MissingValueError
			return errors.As(e, &x) && x.Role == "token-secret" && x.Env
		}},
		{"role of another provider", func(n *NewCredential) { n.Secrets["api-key"] = "x" }, func(e error) bool {
			var x *UnknownRoleError
			return errors.As(e, &x) && x.Role == "api-key"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := good()
			tt.mutate(&n)
			_, err := f.svc.CreateCredential(f.cfg, f.rev, n)
			if err == nil || !tt.check(err) || !IsInputError(err) {
				t.Fatalf("CreateCredential() = %v, want a matching input error", err)
			}
			for _, v := range n.Secrets {
				if strings.Contains(err.Error(), v) {
					t.Errorf("error %q carries a secret value", err)
				}
			}
		})
	}
	if len(f.secrets.sets) != 0 {
		t.Errorf("stores written = %v, want none", f.secrets.sets)
	}
	if IsInputError(errors.New("disk full")) {
		t.Error("IsInputError() = true for a failure of something else")
	}
}

func TestCreateCredentialRollsBackAndReportsConflict(t *testing.T) {
	f := newCredentialFixture(t)
	f.secrets.setErr["token-secret"] = errors.New("store is locked")
	_, err := f.svc.CreateCredential(f.cfg, f.rev, f.newCredential("wiki", "fresh", config.CredentialTypeKeyring))
	if err == nil || IsInputError(err) || strings.Contains(err.Error(), canary) {
		t.Fatalf("CreateCredential() = %v, want the store failure without a secret", err)
	}
	if len(f.secrets.entries) != 0 {
		t.Errorf("entries = %v, want the first secret rolled back", f.secrets.entries)
	}
	if saved, _ := f.store.Load(); saved != nil {
		if _, ok := saved.Credentials["fresh"]; ok {
			t.Error("the credential entry was saved")
		}
	}

	delete(f.secrets.setErr, "token-secret")
	other := f.cfg.Clone()
	if err := other.SetCredential("elsewhere", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(other); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateCredential(f.cfg, f.rev, f.newCredential("wiki", "fresh", config.CredentialTypeKeyring))
	if !errors.Is(err, config.ErrConflict) {
		t.Errorf("CreateCredential() = %v, want a conflict", err)
	}
}

// vaultSecrets is a Secrets over a real vault, for the tests that need the vault to take the change.
type vaultSecrets struct {
	fakeSecrets
	v *vault.Vault
}

func (s *vaultSecrets) Vault() *vault.Vault { return s.v }
func (s *vaultSecrets) SetVault(credential, role, value string, offer vault.PassphraseFunc) error {
	return s.v.Set(credential, role, value, offer)
}
func (s *vaultSecrets) DeleteVault(credential, role string) error {
	_, err := s.v.Delete(credential, role, nil)
	return err
}

// Storing a vault secret approves what it newly opens exactly as the surface's policy says.
func TestSecretWriteApprovalPolicies(t *testing.T) {
	setup := func(t *testing.T) (*Service, *config.Config, *vault.Vault) {
		t.Helper()
		cfg := config.New(roleCatalog{})
		cfg.Services["s-a"] = config.Service{Provider: "wiki", BaseURL: "https://a.example.test"}
		cfg.Credentials["c-a"] = config.Credential{Type: config.CredentialTypeVault}
		cfg.Connections["a"] = config.Connection{Service: "s-a", Credential: "c-a",
			Permissions: []config.Permission{config.PermissionRead}}
		v := vault.New(t.TempDir())
		offer := func(string) (string, error) { return approvalPassphrase, nil }
		if err := v.Set("c-other", "token-id", "synthetic-old", offer); err != nil {
			t.Fatal(err)
		}
		if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
			t.Fatal(err)
		}
		secrets := &vaultSecrets{fakeSecrets: *newFakeSecrets(), v: v}
		svc := New(nil, secrets, connlog.SurfaceTUI, nil).WithVaultProcessSupport(false)
		return svc, cfg, v
	}
	open := func(t *testing.T, cfg *config.Config, v *vault.Vault) int {
		t.Helper()
		report, err := approval.Pending(cfg, v)
		if err != nil {
			t.Fatal(err)
		}
		return len(report.Open)
	}
	write := func(cfg *config.Config, policy ApprovalPolicy) SecretWrite {
		return SecretWrite{Name: "c-a", Entry: cfg.Credentials["c-a"], Role: "token-id", Value: "synthetic-new", Approval: policy}
	}

	t.Run("sweep policy approves what a stored secret opens", func(t *testing.T) {
		svc, cfg, v := setup(t)
		out, err := svc.SetSecret(context.Background(), cfg, write(cfg, ApprovalSweepNewlyOpened))
		if err != nil {
			t.Fatalf("SetSecret() = %v", err)
		}
		if got := open(t, cfg, v); got != 0 {
			t.Errorf("%d connections still open, want none (outcome %+v)", got, out.Approval)
		}
	})
	t.Run("no policy approves nothing", func(t *testing.T) {
		svc, cfg, v := setup(t)
		out, err := svc.SetSecret(context.Background(), cfg, write(cfg, ApprovalNone))
		if err != nil {
			t.Fatalf("SetSecret() = %v", err)
		}
		if len(out.Approval.Approved) != 0 {
			t.Errorf("Approved = %v, want none", out.Approval.Approved)
		}
		if got := open(t, cfg, v); got != 1 {
			t.Errorf("%d connections open, want the one the stored secret opened", got)
		}
	})
	t.Run("a refused write approves nothing", func(t *testing.T) {
		svc, cfg, v := setup(t)
		w := write(cfg, ApprovalSweepNewlyOpened)
		w.Role = "nope"
		if _, err := svc.SetSecret(context.Background(), cfg, w); err == nil {
			t.Fatal("SetSecret() accepted an unknown role")
		}
		if got := open(t, cfg, v); got != 0 {
			t.Errorf("%d connections open after a refused write, want none", got)
		}
	})
}
