package manage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func TestPayloadFieldProblem(t *testing.T) {
	for _, name := range []string{"user", "api-key", "a_b.c", "X1"} {
		if got := PayloadFieldProblem(name); got != "" {
			t.Errorf("PayloadFieldProblem(%q) = %q, want none", name, got)
		}
	}
	for _, name := range []string{"has space", "semi;colon", "ünï", ""} {
		got := PayloadFieldProblem(name)
		if !strings.HasPrefix(got, "the field name is refused") {
			t.Errorf("PayloadFieldProblem(%q) = %q, want a refusal", name, got)
		}
	}
}

// payloadFixture is a service over a configuration that holds the payload credential "shared" with the
// fields user and pass, both stored.
type payloadFixture struct {
	svc     *Service
	secrets *fakeSecrets
	store   *config.Store
	prev    *config.Config
	rev     config.Revision
}

func newPayloadFixture(t *testing.T, credType string) *payloadFixture {
	t.Helper()
	store := config.NewStore(filepath.Join(t.TempDir(), "qatlas", "config.yaml"))
	cfg := store.New()
	if err := cfg.SetCredential("shared", config.Credential{Type: credType, Forward: true,
		Fields: []string{"user", "pass"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &payloadFixture{store: store, secrets: newFakeSecrets()}
	f.svc = New(store, f.secrets, connlog.SurfaceWeb, nil).WithVaultProcessSupport(false)
	var err error
	if f.prev, f.rev, err = f.svc.Load(); err != nil {
		t.Fatal(err)
	}
	f.secrets.entries["shared.user"] = "old-user-1"
	f.secrets.entries["shared.pass"] = "old-pass-1"
	return f
}

// save drops pass, rotates user and adds token.
func (f *payloadFixture) save(t *testing.T, credType string) (PayloadSaved, error) {
	t.Helper()
	cand := f.prev.Clone()
	if err := cand.SetCredential("shared", config.Credential{Type: credType, Forward: true,
		Fields: []string{"user", "token"}}); err != nil {
		t.Fatal(err)
	}
	return f.svc.SavePayload(PayloadSave{
		Previous: f.prev, Base: f.rev, Candidate: cand, Name: "shared", Editing: "shared",
		Roles:  []string{"user", "token"},
		Values: map[string]string{"user": "new-user-2", "token": "new-token-3"},
	})
}

func TestSavePayloadStoresValuesAndRemovesDroppedFields(t *testing.T) {
	for _, credType := range []string{config.CredentialTypeKeyring, config.CredentialTypeVault} {
		t.Run(credType, func(t *testing.T) {
			f := newPayloadFixture(t, credType)
			saved, err := f.save(t, credType)
			if err != nil || saved.Warning != "" || len(saved.Leftover) != 0 {
				t.Fatalf("SavePayload() = %+v, %v", saved, err)
			}
			if got := f.secrets.entries; got["shared.user"] != "new-user-2" || got["shared.token"] != "new-token-3" {
				t.Errorf("stored = %v, want the new values", got)
			}
			if _, still := f.secrets.entries["shared.pass"]; still {
				t.Error("the stored value of the dropped field is still there")
			}
			loaded, err := f.store.Load()
			if err != nil || !reflect.DeepEqual(loaded.Credentials["shared"].Fields, []string{"user", "token"}) {
				t.Errorf("saved fields = %v, %v", loaded.Credentials["shared"].Fields, err)
			}
		})
	}
}

func TestSavePayloadReportsRemovalsThatFailedAndKeepsGoing(t *testing.T) {
	f := newPayloadFixture(t, config.CredentialTypeKeyring)
	f.secrets.deleteErr["pass"] = errors.New("keyring locked")
	saved, err := f.save(t, config.CredentialTypeKeyring)
	if err != nil {
		t.Fatalf("SavePayload() = %v, want the removal failure to be no error", err)
	}
	if !reflect.DeepEqual(saved.Leftover, []string{"pass"}) {
		t.Errorf("Leftover = %v, want [pass]", saved.Leftover)
	}
	if note := LeftoverNote("shared", "pass"); !strings.Contains(note, "'qatlas credential delete shared pass'") {
		t.Errorf("LeftoverNote() = %q", note)
	}
}

func TestSavePayloadIgnoresARemovalWithoutEntry(t *testing.T) {
	f := newPayloadFixture(t, config.CredentialTypeKeyring)
	f.secrets.deleteErr["pass"] = secret.ErrNoEntry
	saved, err := f.save(t, config.CredentialTypeKeyring)
	if err != nil || len(saved.Leftover) != 0 {
		t.Fatalf("SavePayload() = %+v, %v, want a missing entry to count as removed", saved, err)
	}
}

func TestSavePayloadFailureChangesNothing(t *testing.T) {
	f := newPayloadFixture(t, config.CredentialTypeKeyring)
	f.secrets.setErr["token"] = errors.New("store is locked")
	saved, err := f.save(t, config.CredentialTypeKeyring)
	if err == nil || len(saved.Leftover) != 0 {
		t.Fatalf("SavePayload() = %+v, %v, want the commit failure", saved, err)
	}
	// The field the edit drops is removed only after a successful commit.
	if f.secrets.entries["shared.pass"] != "old-pass-1" {
		t.Errorf("stored = %v, want the value of the dropped field kept", f.secrets.entries)
	}
	loaded, _ := f.store.Load()
	if !reflect.DeepEqual(loaded.Credentials["shared"].Fields, []string{"user", "pass"}) {
		t.Errorf("fields = %v, want the configuration unchanged", loaded.Credentials["shared"].Fields)
	}
}

func TestSavePayloadOfANewCredentialRemovesNothing(t *testing.T) {
	f := newPayloadFixture(t, config.CredentialTypeKeyring)
	cand := f.prev.Clone()
	if err := cand.SetCredential("fresh", config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user"}}); err != nil {
		t.Fatal(err)
	}
	saved, err := f.svc.SavePayload(PayloadSave{Previous: f.prev, Base: f.rev, Candidate: cand, Name: "fresh",
		Roles: []string{"user"}, Values: map[string]string{"user": "fresh-user-1"}})
	if err != nil || len(saved.Leftover) != 0 {
		t.Fatalf("SavePayload() = %+v, %v", saved, err)
	}
	if f.secrets.entries["shared.pass"] != "old-pass-1" {
		t.Errorf("stored = %v, want other credentials untouched", f.secrets.entries)
	}
}

func TestSavePayloadRefusesAShortValueWithoutNamingIt(t *testing.T) {
	f := newPayloadFixture(t, config.CredentialTypeKeyring)
	cand := f.prev.Clone()
	_ = cand.SetCredential("shared", config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user"}})
	_, err := f.svc.SavePayload(PayloadSave{Previous: f.prev, Base: f.rev, Candidate: cand, Name: "shared",
		Editing: "shared", Roles: []string{"user"}, Values: map[string]string{"user": "abc"}})
	if err == nil || strings.Contains(err.Error(), "abc") {
		t.Fatalf("SavePayload() = %v, want a refusal without the value", err)
	}
	if f.secrets.entries["shared.pass"] != "old-pass-1" {
		t.Error("a refused save removed a stored value")
	}
}

func TestChainWarnings(t *testing.T) {
	if got := chainWarnings("", "a", "", "b"); got != "a; b" {
		t.Errorf("chainWarnings() = %q, want %q", got, "a; b")
	}
	if got := chainWarnings("", ""); got != "" {
		t.Errorf("chainWarnings() = %q, want none", got)
	}
}

func TestForwardChoices(t *testing.T) {
	cfg := config.New()
	for name, cred := range map[string]config.Credential{
		"zeta":  {Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"a1"}},
		"alpha": {Type: config.CredentialTypeVault, Forward: true, Fields: []string{"a1"}},
		"plain": {Type: config.CredentialTypeKeyring},
	} {
		cfg.Credentials[name] = cred
	}
	if got := ForwardChoices(cfg); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Errorf("ForwardChoices() = %v, want the payload credentials in name order", got)
	}
	if got := ForwardChoices(config.New()); len(got) != 0 {
		t.Errorf("ForwardChoices() = %v, want none", got)
	}
}

func TestForwardBindingStates(t *testing.T) {
	cfg := config.New()
	cfg.Credentials["in-vault"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Credentials["in-keyring"] = config.Credential{Type: config.CredentialTypeKeyring}
	binding := func(v *vault.Vault, conn string) ForwardBinding {
		s := New(nil, &fakeSecrets{vault: v}, connlog.SurfaceWeb, nil)
		return s.ForwardBinding(cfg, conn)
	}
	if got := binding(nil, ""); got != ForwardUnbound {
		t.Errorf("no vault = %v, want unbound", got)
	}
	absent := vault.New(t.TempDir())
	if got := binding(absent, ""); got != ForwardUnbound {
		t.Errorf("absent vault = %v, want unbound", got)
	}
	plain := vault.New(t.TempDir())
	if err := plain.Set("c", "r", "v", nil); err != nil {
		t.Fatal(err)
	}
	if got := binding(plain, ""); got != ForwardUnbound {
		t.Errorf("unencrypted vault = %v, want unbound", got)
	}
	enc := vault.New(t.TempDir())
	if err := enc.Set("c", "r", "v", func(string) (string, error) { return approvalPassphrase, nil }); err != nil {
		t.Fatal(err)
	}
	for conn, want := range map[string]ForwardBinding{
		"": ForwardBound, "in-vault": ForwardBound, "in-keyring": ForwardCredentialOutside,
	} {
		if got := binding(enc, conn); got != want {
			t.Errorf("encrypted vault, credential %q = %v, want %v", conn, got, want)
		}
	}
}

func TestForwardOpenNamesOnlyConnectionsReleasingTheCredential(t *testing.T) {
	w := newApprovalWorld(t)
	cfg := w.base.Clone()
	cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeVault, Forward: true, Fields: []string{"user"}}
	for _, name := range []string{"a", "c"} {
		conn := cfg.Connections[name]
		conn.ForwardSecrets = []string{"shared"}
		cfg.Connections[name] = conn
	}
	conn := cfg.Connections["b"]
	conn.ForwardSecrets = []string{"other"}
	cfg.Connections["b"] = conn
	svc := New(nil, &fakeSecrets{vault: w.v}, connlog.SurfaceTUI, nil)

	got := svc.ForwardOpen(cfg, "shared")
	if len(got) != 2 || !(got[0] == "a" && got[1] == "c" || got[0] == "c" && got[1] == "a") {
		t.Errorf("ForwardOpen() = %v, want a and c", got)
	}
	if got := svc.ForwardOpen(cfg, "unlisted"); len(got) != 0 {
		t.Errorf("ForwardOpen(unlisted) = %v, want none", got)
	}
	if got := New(nil, &fakeSecrets{}, connlog.SurfaceTUI, nil).ForwardOpen(cfg, "shared"); len(got) != 0 {
		t.Errorf("ForwardOpen() without a vault = %v, want none", got)
	}
	if got := New(nil, &fakeSecrets{vault: vault.New(t.TempDir())}, connlog.SurfaceTUI, nil).ForwardOpen(cfg, "shared"); len(got) != 0 {
		t.Errorf("ForwardOpen() without an encrypted vault = %v, want none", got)
	}
}

// releaseFixture is a service over a saved configuration with a connection "a" and the payload credential
// "shared", over the encrypted, unlocked vault of an approval world in which a is approved.
func newReleaseFixture(t *testing.T) (*Service, *config.Config, config.Revision) {
	t.Helper()
	store := config.NewStore(filepath.Join(t.TempDir(), "qatlas", "config.yaml"), roleCatalog{})
	cfg := store.New()
	cfg.Services["s-a"] = config.Service{Provider: "wiki", BaseURL: "https://a.example.test"}
	cfg.Credentials["c-a"] = config.Credential{Type: config.CredentialTypeVault}
	cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeVault, Forward: true, Fields: []string{"user"}}
	cfg.Connections["a"] = config.Connection{Service: "s-a", Credential: "c-a",
		Permissions: []config.Permission{config.PermissionRead}}
	v := vault.New(t.TempDir())
	if err := v.Set("c-a", "token-id", "synthetic-a", func(string) (string, error) { return approvalPassphrase, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	svc := New(store, &fakeSecrets{vault: v}, connlog.SurfaceWeb, nil).WithVaultProcessSupport(false)
	loaded, rev, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	return svc, loaded, rev
}

func TestSaveForwardReleaseSavesTheListAndNeverApproves(t *testing.T) {
	svc, cfg, rev := newReleaseFixture(t)
	saved, err := svc.SaveForwardRelease(cfg, rev, "a", []string{"shared"})
	if err != nil {
		t.Fatalf("SaveForwardRelease() = %v", err)
	}
	if !reflect.DeepEqual(saved.Config.Connections["a"].ForwardSecrets, []string{"shared"}) {
		t.Errorf("saved release = %v", saved.Config.Connections["a"].ForwardSecrets)
	}
	if len(cfg.Connections["a"].ForwardSecrets) != 0 {
		t.Error("the configuration passed in was changed")
	}
	loaded, _ := svc.store.Load()
	if !reflect.DeepEqual(loaded.Connections["a"].ForwardSecrets, []string{"shared"}) {
		t.Errorf("release on disk = %v", loaded.Connections["a"].ForwardSecrets)
	}
	if saved.Check != ReleaseOpen || !strings.Contains(StaysOpenReason(saved.Change), "changed") {
		t.Errorf("Check = %v, Change = %+v, want the connection open for its changed release", saved.Check, saved.Change)
	}
	again, err := svc.SaveForwardRelease(saved.Config, mustRevision(t, svc, saved.Config), "a", nil)
	if err != nil {
		t.Fatalf("SaveForwardRelease(nil) = %v", err)
	}
	if again.Check != ReleaseSettled {
		t.Errorf("Check after taking the release back = %v, want settled", again.Check)
	}
}

func mustRevision(t *testing.T, svc *Service, cfg *config.Config) config.Revision {
	t.Helper()
	rev, err := svc.RevisionOf(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestSaveForwardReleaseRefusals(t *testing.T) {
	svc, cfg, rev := newReleaseFixture(t)
	var refused *RefusedReleaseError
	if _, err := svc.SaveForwardRelease(cfg, rev, "a", []string{"missing"}); !errors.As(err, &refused) {
		t.Errorf("an unknown credential = %v, want a *RefusedReleaseError", err)
	}
	if _, err := svc.SaveForwardRelease(cfg, rev, "a", []string{"c-a"}); !errors.As(err, &refused) {
		t.Errorf("a credential without payload = %v, want a *RefusedReleaseError", err)
	}
	var notThere *config.NotThereError
	if _, err := svc.SaveForwardRelease(cfg, rev, "nope", nil); !errors.As(err, &notThere) {
		t.Errorf("an unknown connection = %v, want a *config.NotThereError", err)
	}
	if _, err := svc.SaveForwardRelease(cfg, config.Revision("stale"), "a", []string{"shared"}); !errors.Is(err, config.ErrConflict) {
		t.Errorf("a stale revision = %v, want a conflict", err)
	}
	loaded, _ := svc.store.Load()
	if len(loaded.Connections["a"].ForwardSecrets) != 0 {
		t.Error("a refused release was saved")
	}
}

func TestReleaseCheckWithoutAnUnlockedVault(t *testing.T) {
	cfg := config.New()
	check := func(v *vault.Vault) ReleaseCheck {
		got, _, err := New(nil, &fakeSecrets{vault: v}, connlog.SurfaceWeb, nil).releaseCheck(cfg, "a")
		if err != nil {
			t.Fatalf("releaseCheck() = %v", err)
		}
		return got
	}
	if got := check(nil); got != ReleaseUnchecked {
		t.Errorf("no vault = %v, want unchecked", got)
	}
	dir := t.TempDir()
	if err := vault.New(dir).Set("c", "r", "v", func(string) (string, error) { return approvalPassphrase, nil }); err != nil {
		t.Fatal(err)
	}
	if got := check(vault.New(dir)); got != ReleaseLocked {
		t.Errorf("locked vault = %v, want locked", got)
	}
	if got := check(vault.New(t.TempDir())); got != ReleaseUnchecked {
		t.Errorf("absent vault = %v, want unchecked", got)
	}
}
