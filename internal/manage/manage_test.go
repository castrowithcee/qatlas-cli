package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// fakeSecrets is an in-memory Secrets whose writes and removals can be made to fail per role.
type fakeSecrets struct {
	entries   map[string]string
	setErr    map[string]error
	deleteErr map[string]error
	vault     *vault.Vault
	sets      []string
}

func newFakeSecrets() *fakeSecrets {
	return &fakeSecrets{entries: map[string]string{}, setErr: map[string]error{}, deleteErr: map[string]error{}}
}

func (f *fakeSecrets) put(credential, role, value string) error {
	f.sets = append(f.sets, credential+"."+role)
	if err := f.setErr[role]; err != nil {
		return err
	}
	f.entries[credential+"."+role] = value
	return nil
}

func (f *fakeSecrets) remove(credential, role string) error {
	if err := f.deleteErr[role]; err != nil {
		return err
	}
	delete(f.entries, credential+"."+role)
	return nil
}

func (f *fakeSecrets) Set(credential, role, value string) error {
	return f.put(credential, role, value)
}
func (f *fakeSecrets) Delete(credential, role string) ([]secret.Source, error) {
	return nil, f.remove(credential, role)
}
func (f *fakeSecrets) Vault() *vault.Vault { return f.vault }
func (f *fakeSecrets) SetVault(credential, role, value string, _ vault.PassphraseFunc) error {
	return f.put(credential, role, value)
}
func (f *fakeSecrets) DeleteVault(credential, role string) error { return f.remove(credential, role) }

// fixture is a service over a configuration file that holds one keyring credential "base".
type fixture struct {
	svc     *Service
	store   *config.Store
	secrets *fakeSecrets
	red     *redact.Redactor
	path    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qatlas", "config.yaml")
	store := config.NewStore(path)
	cfg := store.New()
	if err := cfg.SetCredential("base", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: store, secrets: newFakeSecrets(), red: &redact.Redactor{}, path: path}
	f.svc = New(store, f.secrets, connlog.SurfaceWeb, f.red).WithVaultProcessSupport(false)
	return f
}

// candidate loads the file and adds the keyring credential name to it.
func (f *fixture) candidate(t *testing.T, name string) (*config.Config, config.Revision) {
	t.Helper()
	cfg, rev, err := f.svc.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	cand := cfg.Clone()
	if err := cand.SetCredential(name, config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	return cand, rev
}

func (f *fixture) credentialsOnDisk(t *testing.T) []string {
	t.Helper()
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range cfg.Credentials {
		names = append(names, name)
	}
	return names
}

func TestCommitSecretsStoresSecretsAndConfiguration(t *testing.T) {
	f := newFixture(t)
	cand, rev := f.candidate(t, "new")
	warning, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a", "b", "skipped"},
		map[string]string{"a": "value-a", "b": "value-b"}, nil)
	if err != nil || warning != "" {
		t.Fatalf("CommitSecrets() = %q, %v", warning, err)
	}
	if f.secrets.entries["new.a"] != "value-a" || f.secrets.entries["new.b"] != "value-b" || len(f.secrets.entries) != 2 {
		t.Errorf("entries = %v, want exactly the two given roles", f.secrets.entries)
	}
	if got := f.credentialsOnDisk(t); len(got) != 2 {
		t.Errorf("credentials on disk = %v, want base and new", got)
	}
}

func TestCommitSecretsConflictWritesNothing(t *testing.T) {
	f := newFixture(t)
	cand, rev := f.candidate(t, "new")
	// Another writer changes the file after the candidate was derived.
	other, _ := f.candidate(t, "other")
	if err := f.store.Save(other); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a"}, map[string]string{"a": "value-a"}, nil)
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("CommitSecrets() = %v, want a conflict", err)
	}
	if len(f.secrets.sets) != 0 || len(f.secrets.entries) != 0 {
		t.Errorf("a conflict wrote secrets: %v %v", f.secrets.sets, f.secrets.entries)
	}
	names := f.credentialsOnDisk(t)
	for _, n := range names {
		if n == "new" {
			t.Errorf("a conflict saved the configuration: %v", names)
		}
	}
}

func TestCommitSecretsRollsBackOnWriteError(t *testing.T) {
	f := newFixture(t)
	f.secrets.setErr["b"] = errors.New("keyring offline")
	cand, rev := f.candidate(t, "new")
	_, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a", "b"},
		map[string]string{"a": "value-a", "b": "value-b"}, nil)
	if err == nil || !strings.Contains(err.Error(), "storing the secret for new.b: keyring offline") {
		t.Fatalf("CommitSecrets() = %v, want the write error", err)
	}
	if len(f.secrets.entries) != 0 {
		t.Errorf("entries = %v, want the first secret rolled back", f.secrets.entries)
	}
	if got := f.credentialsOnDisk(t); len(got) != 1 {
		t.Errorf("credentials on disk = %v, want only base", got)
	}
}

func TestCommitSecretsRollsBackOnSaveError(t *testing.T) {
	f := newFixture(t)
	cand, rev := f.candidate(t, "new")
	// An entry that does not validate makes the save inside the transaction fail.
	cand.Credentials["new"] = config.Credential{Type: "no-such-type"}
	_, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a"}, map[string]string{"a": "value-a"}, nil)
	if err == nil {
		t.Fatal("CommitSecrets() = nil, want the save error")
	}
	if len(f.secrets.entries) != 0 {
		t.Errorf("entries = %v, want the secret rolled back", f.secrets.entries)
	}
}

func TestCommitSecretsNamesSecretsThatCannotBeRemoved(t *testing.T) {
	f := newFixture(t)
	f.secrets.setErr["c"] = errors.New("keyring offline")
	f.secrets.deleteErr["a"] = errors.New("locked")
	f.secrets.deleteErr["b"] = secret.ErrNoEntry
	cand, rev := f.candidate(t, "new")
	_, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a", "b", "c"},
		map[string]string{"a": "value-a", "b": "value-b", "c": "value-c"}, nil)
	if err == nil {
		t.Fatal("CommitSecrets() = nil, want an error")
	}
	for _, want := range []string{
		"storing the secret for new.c: keyring offline",
		"the secrets already stored for new (a) could not be removed again, remove them with 'qatlas credential delete new <role>'",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestCommitSecretsChecksForwardValuesBeforeWriting(t *testing.T) {
	f := newFixture(t)
	cfg, rev, err := f.svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	cand := cfg.Clone()
	cand.Credentials["fwd"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"user", "pass"}}
	_, err = f.svc.CommitSecrets(cand, rev, "fwd", false, []string{"user", "pass"},
		map[string]string{"user": "long-enough-user", "pass": "qzw"}, nil)
	if err == nil || !strings.Contains(err.Error(), "credential fwd:") || !strings.Contains(err.Error(), `"pass"`) {
		t.Fatalf("CommitSecrets() = %v, want the forward value refused", err)
	}
	if strings.Contains(err.Error(), "qzw") || strings.Contains(err.Error(), "long-enough-user") {
		t.Errorf("the message carries a value: %v", err)
	}
	if len(f.secrets.sets) != 0 {
		t.Errorf("a refused value still wrote %v", f.secrets.sets)
	}
}

func TestCommitSecretsVaultWithoutVaultHasNoWarning(t *testing.T) {
	f := newFixture(t)
	cand, rev := f.candidate(t, "new")
	warning, err := f.svc.CommitSecrets(cand, rev, "new", true, []string{"a"}, map[string]string{"a": "value-a"}, nil)
	if err != nil || warning != "" {
		t.Fatalf("CommitSecrets() = %q, %v", warning, err)
	}
}

func TestSaveConfig(t *testing.T) {
	f := newFixture(t)
	before, rev, err := f.svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	after := before.Clone()
	if err := after.SetCredential("second", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}

	// Another writer gets in first: nothing of this change is written.
	other := before.Clone()
	if err := other.SetCredential("other", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(other); err != nil {
		t.Fatal(err)
	}
	warning, err := f.svc.SaveConfig(before, after, rev)
	if !errors.Is(err, config.ErrConflict) || warning != "" {
		t.Fatalf("SaveConfig() = %q, %v, want a conflict and no warning", warning, err)
	}
	for _, n := range f.credentialsOnDisk(t) {
		if n == "second" {
			t.Error("a conflicting save wrote the configuration")
		}
	}

	// Based on the current file it is saved.
	before, rev, err = f.svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	after = before.Clone()
	if err := after.SetCredential("second", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatal(err)
	}
	if warning, err := f.svc.SaveConfig(before, after, rev); err != nil || warning != "" {
		t.Fatalf("SaveConfig() = %q, %v", warning, err)
	}
	if got := f.credentialsOnDisk(t); len(got) != 3 {
		t.Errorf("credentials on disk = %v, want three", got)
	}
}

func connections(names ...string) *config.Config {
	cfg := &config.Config{Connections: map[string]config.Connection{}}
	for _, name := range names {
		cfg.Connections[name] = config.Connection{Service: "wiki", Credential: "reader"}
	}
	return cfg
}

func TestRecordConnectionsWarnsRedactedAndOnlyOnChange(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.RecordConnections(connections("a"), connections("a")); got != "" {
		t.Errorf("RecordConnections() without a change = %q, want none", got)
	}
	// A file where the log directory belongs makes every append fail.
	logs := filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")
	if err := os.MkdirAll(filepath.Dir(logs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logs, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := f.svc.RecordConnections(connections(), connections("a"))
	if !strings.HasPrefix(plain, "warning: ") || !strings.Contains(plain, "the change was saved") {
		t.Fatalf("RecordConnections() = %q, want the unredacted warning", plain)
	}
	f.red.Add("the change was saved")
	redacted := f.svc.RecordConnections(connections(), connections("a"))
	if strings.Contains(redacted, "the change was saved") || !strings.Contains(redacted, redact.Marker) {
		t.Errorf("RecordConnections() = %q, want the warning redacted", redacted)
	}
}

func TestNilRedactorLeavesWarningsUnchanged(t *testing.T) {
	f := newFixture(t)
	svc := New(f.store, f.secrets, connlog.SurfaceCLI, nil)
	logs := filepath.Join(vault.New(filepath.Dir(f.path)).Dir(), "logs")
	if err := os.MkdirAll(filepath.Dir(logs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logs, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := svc.RecordConnections(connections(), connections("a")); !strings.HasPrefix(got, "warning: ") {
		t.Errorf("RecordConnections() = %q", got)
	}
}

func TestVaultProcessOperationsAreSilentWhenUnsupportedOrWithoutVault(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.SyncVaultProcess(context.Background(), nil); got != "" {
		t.Errorf("SyncVaultProcess() unsupported = %q", got)
	}
	if got := f.svc.LockVaultProcess(context.Background(), "why", "next"); got != "" {
		t.Errorf("LockVaultProcess() unsupported = %q", got)
	}
	f.secrets.vault = nil
	svc := New(f.store, f.secrets, connlog.SurfaceTUI, nil).WithVaultProcessSupport(true)
	if got := svc.SyncVaultProcess(context.Background(), nil); got != "" {
		t.Errorf("SyncVaultProcess() without a vault = %q", got)
	}
	if got := New(f.store, nil, connlog.SurfaceTUI, nil).WithVaultProcessSupport(true).
		LockVaultProcess(context.Background(), "", ""); got != "" {
		t.Errorf("LockVaultProcess() without secrets = %q", got)
	}
}

// No exported method hands out the store, the vault or the resolver the service guards.
func TestExportedMethodsDoNotReturnGuardedHandles(t *testing.T) {
	guarded := []reflect.Type{
		reflect.TypeOf((*config.Store)(nil)),
		reflect.TypeOf((*vault.Vault)(nil)),
		reflect.TypeOf((*secret.Resolver)(nil)),
	}
	typ := reflect.TypeOf(&Service{})
	if typ.NumMethod() == 0 {
		t.Fatal("no exported methods")
	}
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			for _, g := range guarded {
				if m.Type.Out(j) == g {
					t.Errorf("(*Service).%s returns %v", m.Name, g)
				}
			}
		}
	}
}
