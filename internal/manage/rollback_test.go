package manage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Both stores are exercised: the keyring and the vault read, write and restore through different methods.
var rollbackStores = []struct {
	name    string
	toVault bool
}{{"keyring", false}, {"vault", true}}

func TestCommitSecretsRollbackRestoresOverwrittenValues(t *testing.T) {
	for _, st := range rollbackStores {
		t.Run(st.name, func(t *testing.T) {
			f := newFixture(t)
			f.secrets.entries["base.a"] = "old-a"
			f.secrets.setErr["c"] = errors.New("store offline")
			cfg, rev, err := f.svc.Load()
			if err != nil {
				t.Fatal(err)
			}
			// a existed and is overwritten, b did not exist, c fails.
			_, err = f.svc.CommitSecrets(cfg, rev, "base", st.toVault, []string{"a", "b", "c"},
				map[string]string{"a": "new-a", "b": "new-b", "c": "new-c"}, nil)
			if err == nil {
				t.Fatal("CommitSecrets() = nil, want the write error")
			}
			if got, ok := f.secrets.entries["base.a"]; !ok || got != "old-a" {
				t.Errorf("role a is %q (present %v), want its previous value", got, ok)
			}
			if _, ok := f.secrets.entries["base.b"]; ok {
				t.Errorf("role b without a previous value is still stored: %v", f.secrets.entries)
			}
			for _, value := range []string{"old-a", "new-a", "new-b", "new-c"} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("error %q carries a value", err)
				}
			}
		})
	}
}

func TestCommitSecretsRollbackOnSaveErrorRestoresAllPreviousValues(t *testing.T) {
	for _, st := range rollbackStores {
		t.Run(st.name, func(t *testing.T) {
			f := newFixture(t)
			f.secrets.entries["new.a"] = "old-a"
			f.secrets.entries["new.b"] = "old-b"
			cand, rev := f.candidate(t, "new")
			cand.Credentials["new"] = config.Credential{Type: "no-such-type"}
			_, err := f.svc.CommitSecrets(cand, rev, "new", st.toVault, []string{"a", "b", "c"},
				map[string]string{"a": "new-a", "b": "new-b", "c": "new-c"}, nil)
			if err == nil {
				t.Fatal("CommitSecrets() = nil, want the save error")
			}
			want := map[string]string{"new.a": "old-a", "new.b": "old-b"}
			if len(f.secrets.entries) != len(want) || f.secrets.entries["new.a"] != "old-a" ||
				f.secrets.entries["new.b"] != "old-b" {
				t.Errorf("entries = %v, want only the previous values", f.secrets.entries)
			}
		})
	}
}

func TestCommitSecretsUnreadablePreviousKeyringValueWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.secrets.entries["new.b"] = "old-b"
	f.secrets.readErr["b"] = errors.New("store is locked")
	cand, rev := f.candidate(t, "new")
	_, err := f.svc.CommitSecrets(cand, rev, "new", false, []string{"a", "b"},
		map[string]string{"a": "new-a", "b": "new-b"}, nil)
	if err == nil {
		t.Fatal("CommitSecrets() = nil, want an error")
	}
	if len(f.secrets.sets) != 0 || f.secrets.entries["new.b"] != "old-b" {
		t.Errorf("sets = %v, entries = %v, want nothing written", f.secrets.sets, f.secrets.entries)
	}
	if names := f.credentialsOnDisk(t); len(names) != 1 {
		t.Errorf("credentials on disk = %v, want only base", names)
	}
	if strings.Contains(err.Error(), "old-b") || strings.Contains(err.Error(), "new-") {
		t.Errorf("error %q carries a value", err)
	}
}

func TestCommitSecretsNamesSecretsThatCannotBeRestored(t *testing.T) {
	for _, st := range rollbackStores {
		t.Run(st.name, func(t *testing.T) {
			f := newFixture(t)
			f.secrets.entries["new.a"] = "old-a"
			f.secrets.setErr["c"] = errors.New("store offline")
			cand, rev := f.candidate(t, "new")
			// Only the second write of a, the restore, fails.
			f.secrets.failRestore = map[string]bool{"a": true}
			_, err := f.svc.CommitSecrets(cand, rev, "new", st.toVault, []string{"a", "c"},
				map[string]string{"a": "new-a", "c": "new-c"}, nil)
			if err == nil || !strings.Contains(err.Error(), "the secrets of new (a) could not be put back as they were before") {
				t.Fatalf("CommitSecrets() = %v, want the unrestored role named", err)
			}
			if strings.Contains(err.Error(), "old-a") || strings.Contains(err.Error(), "new-a") {
				t.Errorf("error %q carries a value", err)
			}
		})
	}
}

// A commit into an encrypted, locked vault needs no passphrase, and when it fails afterwards it leaves
// neither a pending entry of its own nor a change to secrets.age.
func TestCommitSecretsLockedVaultNeedsNoPassphraseAndRollsBack(t *testing.T) {
	setup := func(t *testing.T) (*fixture, *vault.Vault, string) {
		t.Helper()
		dir := t.TempDir()
		seed := vault.New(dir)
		if err := seed.Set("new", "a", "old-a", func(string) (string, error) { return "phrase", nil }); err != nil {
			t.Fatal(err)
		}
		locked := vault.New(dir)
		f := newFixture(t)
		f.svc = f.svc.WithSecrets(&vaultSecrets{fakeSecrets: *f.secrets, v: locked})
		return f, locked, filepath.Join(dir, vault.DirName, "secrets.age")
	}
	forbidden := func(string) (string, error) {
		t.Error("the passphrase was asked for")
		return "", errors.New("no passphrase")
	}

	t.Run("success", func(t *testing.T) {
		f, locked, _ := setup(t)
		cand, rev := f.candidate(t, "new")
		if _, err := f.svc.CommitSecrets(cand, rev, "new", true, []string{"a", "b"},
			map[string]string{"a": "new-a", "b": "new-b"}, forbidden); err != nil {
			t.Fatalf("CommitSecrets() = %v", err)
		}
		if status, _ := locked.Status(); status.Pending != 2 {
			t.Errorf("Pending = %d, want 2", status.Pending)
		}
	})

	t.Run("save fails", func(t *testing.T) {
		f, locked, secretsPath := setup(t)
		before, err := os.ReadFile(secretsPath)
		if err != nil {
			t.Fatal(err)
		}
		cand, rev := f.candidate(t, "new")
		cand.Credentials["new"] = config.Credential{Type: "no-such-type"}
		if _, err := f.svc.CommitSecrets(cand, rev, "new", true, []string{"a", "b"},
			map[string]string{"a": "new-a", "b": "new-b"}, forbidden); err == nil {
			t.Fatal("CommitSecrets() = nil, want the save error")
		}
		if status, _ := locked.Status(); status.Pending != 0 {
			t.Errorf("Pending = %d, want none left by the failed commit", status.Pending)
		}
		after, err := os.ReadFile(secretsPath)
		if err != nil || string(after) != string(before) {
			t.Errorf("secrets.age changed: %v", err)
		}
		if value, found, _, err := locked.Get("new", "a", func(string) (string, error) { return "phrase", nil }); err != nil ||
			!found || value != "old-a" {
			t.Errorf("a = %q, %v, %v, want its previous value", value, found, err)
		}
	})
}
