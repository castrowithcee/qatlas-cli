package secretcommit

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

type recordingSecrets struct{ written []string }

func (s *recordingSecrets) Set(credential, role, _ string) error {
	s.written = append(s.written, credential+"/"+role)
	return nil
}
func (s *recordingSecrets) Delete(string, string) ([]secret.Source, error) { return nil, nil }
func (s *recordingSecrets) Vault() *vault.Vault                            { return nil }
func (s *recordingSecrets) SetVault(string, string, string, vault.PassphraseFunc) error {
	return nil
}
func (s *recordingSecrets) DeleteVault(string, string) error { return nil }

// A field value of a payload secret under four characters is refused before anything is written or saved.
func TestCommitRefusesShortForwardValues(t *testing.T) {
	cfg := config.New()
	cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user", "pass"}}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	secrets := &recordingSecrets{}

	_, err := Commit(store, secrets, cfg, config.RevisionAbsent, "shared", false, []string{"user", "pass"},
		map[string]string{"user": "long-enough", "pass": "abc"}, nil)
	if err == nil || !strings.Contains(err.Error(), "pass") || strings.Contains(err.Error(), "abc") {
		t.Fatalf("Commit() = %v, want a refusal naming the field and no value", err)
	}
	if len(secrets.written) != 0 {
		t.Errorf("Commit() wrote %v before refusing", secrets.written)
	}

	if _, err := Commit(store, secrets, cfg, config.RevisionAbsent, "shared", false, []string{"user", "pass"},
		map[string]string{"user": "long-enough", "pass": "abcd"}, nil); err != nil {
		t.Fatalf("Commit() = %v, want success for values of four characters", err)
	}
	if len(secrets.written) != 2 {
		t.Errorf("written = %v, want both fields", secrets.written)
	}
}

// mapSecrets is an in-memory secret store; onSet runs after every successful Set, failOn names a
// credential/role whose Set fails.
type mapSecrets struct {
	recordingSecrets
	values map[string]string
	failOn string
	onSet  func()
}

func (s *mapSecrets) Set(credential, role, value string) error {
	key := credential + "/" + role
	if key == s.failOn {
		return errors.New("store is locked")
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	if s.onSet != nil {
		s.onSet()
	}
	return nil
}

func (s *mapSecrets) Delete(credential, role string) ([]secret.Source, error) {
	delete(s.values, credential+"/"+role)
	return nil, nil
}

// base writes a configuration with one credential and returns the store, a second store on the same file,
// and the revision a form would have been based on.
func base(t *testing.T) (*config.Store, *config.Store, config.Revision) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := config.NewStore(path)
	cfg := config.New()
	if err := cfg.SetCredential("first", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	_, rev, err := store.LoadVersioned()
	if err != nil {
		t.Fatalf("LoadVersioned() = %v", err)
	}
	return store, config.NewStore(path), rev
}

func withCredential(t *testing.T, store *config.Store, name string) *config.Config {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if err := cfg.SetCredential(name, config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	return cfg
}

func TestCommitSavesSecretsAndConfigOnCurrentRevision(t *testing.T) {
	store, _, rev := base(t)
	secrets := &mapSecrets{}
	cfg := withCredential(t, store, "second")

	if _, err := Commit(store, secrets, cfg, rev, "second", false, []string{"token"},
		map[string]string{"token": "canary-1234"}, nil); err != nil {
		t.Fatalf("Commit() = %v", err)
	}
	if secrets.values["second/token"] != "canary-1234" {
		t.Errorf("secrets = %v, want the token stored", secrets.values)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := got.Credentials["second"]; !ok {
		t.Errorf("credentials = %v, want second saved", got.Credentials)
	}
}

// A writer that saved between the read and the commit makes the commit a conflict that wrote no secret and
// removed neither the writer's configuration nor a secret it left.
func TestCommitConflictWritesNothing(t *testing.T) {
	store, other, rev := base(t)
	cfg := withCredential(t, store, "second")

	foreign := withCredential(t, other, "foreign")
	if err := other.Update(func(c *config.Config) error { *c = *foreign; return nil }); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	secrets := &mapSecrets{values: map[string]string{"second/token": "theirs", "foreign/token": "keep"}}

	_, err := Commit(store, secrets, cfg, rev, "second", false, []string{"token"},
		map[string]string{"token": "canary-1234"}, nil)
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("Commit() = %v, want a conflict", err)
	}
	if secrets.values["second/token"] != "theirs" || secrets.values["foreign/token"] != "keep" {
		t.Errorf("secrets = %v, want them untouched", secrets.values)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := got.Credentials["foreign"]; !ok {
		t.Errorf("credentials = %v, want the other change kept", got.Credentials)
	}
	if _, ok := got.Credentials["second"]; ok {
		t.Errorf("credentials = %v, want second not saved", got.Credentials)
	}
}

// A writer that bypasses the lock and replaces the file after the secrets were written is found at the save;
// the secrets this commit wrote are removed again and the writer's file stays.
func TestCommitConflictAtSaveRollsBackOwnSecrets(t *testing.T) {
	store, other, rev := base(t)
	cfg := withCredential(t, store, "second")
	foreign := withCredential(t, other, "foreign")
	secrets := &mapSecrets{values: map[string]string{"foreign/token": "keep"}}
	secrets.onSet = func() {
		if err := other.Save(foreign); err != nil {
			t.Errorf("Save() = %v", err)
		}
		secrets.onSet = nil
	}

	_, err := Commit(store, secrets, cfg, rev, "second", false, []string{"token", "pin"},
		map[string]string{"token": "canary-1234", "pin": "canary-5678"}, nil)
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("Commit() = %v, want a conflict", err)
	}
	if len(secrets.values) != 1 || secrets.values["foreign/token"] != "keep" {
		t.Errorf("secrets = %v, want only the foreign one", secrets.values)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := got.Credentials["foreign"]; !ok {
		t.Errorf("credentials = %v, want the other writer's file kept", got.Credentials)
	}
}

func TestCommitFailedSecretWriteRollsBackAndLeavesConfig(t *testing.T) {
	store, _, rev := base(t)
	cfg := withCredential(t, store, "second")
	secrets := &mapSecrets{failOn: "second/pin"}

	_, err := Commit(store, secrets, cfg, rev, "second", false, []string{"token", "pin"},
		map[string]string{"token": "canary-1234", "pin": "canary-5678"}, nil)
	if err == nil || errors.Is(err, config.ErrConflict) {
		t.Fatalf("Commit() = %v, want the store failure", err)
	}
	if len(secrets.values) != 0 {
		t.Errorf("secrets = %v, want none left", secrets.values)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, ok := got.Credentials["second"]; ok {
		t.Errorf("credentials = %v, want second not saved", got.Credentials)
	}
}
