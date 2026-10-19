package vaultmigrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// fakeStore answers StoreValue from a fixed script, the way the system keyring would for a switched
// credential's roles, without touching anything of the machine.
type fakeStore map[string]struct {
	value string
	state secret.StoreState
}

func (f fakeStore) StoreValue(_ context.Context, credential, role string) (string, secret.StoreState) {
	if v, ok := f[credential+"."+role]; ok {
		return v.value, v.state
	}
	return "", secret.StoreEmpty
}

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return config.New()
}

// A credential that is still of type keyring is switched, and every role the keyring holds wins over the
// plaintext file's own copy of it.
func TestPlanSwitchesAKeyringCredentialAndPrefersTheKeyring(t *testing.T) {
	cfg := newTestConfig(t)
	if err := cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	entries := map[string]map[string]string{
		"reader": {"token-id": "legacy-id", "token-secret": "legacy-secret"},
	}
	store := fakeStore{
		"reader.token-id": {value: "keyring-id", state: secret.StoreHolds},
	}
	v := vault.New(t.TempDir())

	plan, switched, unsure, err := Plan(cfg, store, v, entries, nil)
	if err != nil {
		t.Fatalf("Plan() = %v", err)
	}
	if len(switched) != 1 || switched[0] != "reader" {
		t.Fatalf("switched = %v, want [reader]", switched)
	}
	if len(unsure) != 0 {
		t.Fatalf("storeUnsureFor = %v, want none", unsure)
	}
	got := map[string]string{}
	for _, e := range plan {
		got[e.Role] = e.Value
	}
	if got["token-id"] != "keyring-id" {
		t.Errorf("token-id = %q, want the keyring's own value to win", got["token-id"])
	}
	if got["token-secret"] != "legacy-secret" {
		t.Errorf("token-secret = %q, want the plaintext value: the keyring holds nothing for it", got["token-secret"])
	}
}

// A credential no longer of type keyring is never touched: an existing vault role keeps its value, and
// only a role still missing moves from the plaintext file.
func TestPlanNeverOverwritesAnExistingVaultRole(t *testing.T) {
	cfg := newTestConfig(t)
	if err := cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeVault}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Set("reader", "token-id", "vault-id", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	entries := map[string]map[string]string{
		"reader": {"token-id": "legacy-id-should-not-win", "token-secret": "legacy-secret"},
	}

	plan, switched, _, err := Plan(cfg, fakeStore{}, v, entries, nil)
	if err != nil {
		t.Fatalf("Plan() = %v", err)
	}
	if len(switched) != 0 {
		t.Errorf("switched = %v, want none: the credential is already of type vault", switched)
	}
	if len(plan) != 1 || plan[0].Role != "token-secret" || plan[0].Value != "legacy-secret" {
		t.Errorf("plan = %+v, want only the still-missing token-secret", plan)
	}
}

// A keyring that could not be checked at all does not block the migration: it falls back to the plaintext
// value and names the credential in storeUnsureFor.
func TestPlanFallsBackWhenTheKeyringCannotBeChecked(t *testing.T) {
	cfg := newTestConfig(t)
	if err := cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	entries := map[string]map[string]string{"reader": {"token-id": "legacy-id"}}
	store := fakeStore{"reader.token-id": {state: secret.StoreUnavailable}}
	v := vault.New(t.TempDir())

	plan, _, unsure, err := Plan(cfg, store, v, entries, nil)
	if err != nil {
		t.Fatalf("Plan() = %v", err)
	}
	if len(unsure) != 1 || unsure[0] != "reader" {
		t.Fatalf("storeUnsureFor = %v, want [reader]", unsure)
	}
	if len(plan) != 1 || plan[0].Value != "legacy-id" {
		t.Errorf("plan = %+v, want the plaintext value carried over", plan)
	}
}

// Write stores every entry with its credential's id, offering a passphrase only for the vault's very first
// secret, and Verify confirms exactly that content is there once the vault is not locked.
func TestWriteAndVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(dir)
	plan := []Entry{{Name: "reader", Role: "token-id", Value: "canary-write-4f1a"}}
	var synced []Entry
	const credID = "0123456789abcdef0123456789abcdef"
	cfg := config.New()
	cfg.Credentials["reader"] = config.Credential{ID: credID, Type: config.CredentialTypeVault}

	if err := Write(v, cfg, plan, func(string) (string, error) { return "", nil },
		func(p []Entry) { synced = p }); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if len(synced) != 1 {
		t.Errorf("sync was not called with the written plan: %v", synced)
	}
	if err := Verify(v, plan); err != nil {
		t.Errorf("Verify() = %v, want the vault to hold what was just written", err)
	}

	got, found, _, err := v.Get("reader", "token-id", nil)
	if err != nil || !found || got != "canary-write-4f1a" {
		t.Errorf("Get() = %q, %v, %v, want the written value", got, found, err)
	}
	if data, _ := os.ReadFile(filepath.Join(v.Dir(), "secrets.json")); !strings.Contains(string(data), `"credential_id": "`+credID+`"`) {
		t.Errorf("the entry lacks the credential id:\n%s", data)
	}
}

// SwitchCredentials backs up the store's file first and switches every named credential to type vault.
func TestSwitchCredentialsBacksUpAndSwitches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := config.NewStore(path)
	cfg := config.New()
	if err := cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	_, rev, err := store.LoadVersioned()
	if err != nil {
		t.Fatalf("LoadVersioned() = %v", err)
	}
	if err := SwitchCredentials(store, cfg, rev, []string{"reader"}); err != nil {
		t.Fatalf("SwitchCredentials() = %v", err)
	}
	if cfg.Credentials["reader"].Type != config.CredentialTypeVault {
		t.Errorf("type = %q, want vault", cfg.Credentials["reader"].Type)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "backups", "config-*.yaml")); len(backups) != 1 {
		t.Errorf("configuration backups = %v, want one", backups)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if reloaded.Credentials["reader"].Type != config.CredentialTypeVault {
		t.Errorf("saved type = %q, want vault", reloaded.Credentials["reader"].Type)
	}
}

// A change to the file since cfg was read makes SwitchCredentials a conflict that leaves the file and its
// directory as the other writer left them.
func TestSwitchCredentialsConflictLeavesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := config.NewStore(path)
	cfg := config.New()
	if err := cfg.SetCredential("reader", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("SetCredential() = %v", err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	_, rev, err := store.LoadVersioned()
	if err != nil {
		t.Fatalf("LoadVersioned() = %v", err)
	}
	other := config.NewStore(path)
	if err := other.Update(func(c *config.Config) error {
		return c.SetCredential("added", config.Credential{Type: config.CredentialTypeKeyring})
	}); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}

	backupsBefore, _ := filepath.Glob(filepath.Join(dir, "backups", "*"))
	err = SwitchCredentials(store, cfg, rev, []string{"reader"})
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("SwitchCredentials() = %v, want a conflict", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Errorf("the file changed: %v", err)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*")); len(backups) != len(backupsBefore) {
		t.Errorf("a backup was made for a conflicting change")
	}
}
