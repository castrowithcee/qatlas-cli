package secret

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// mapStore is a credential store over a map that counts its reads.
type mapStore struct {
	entries map[string]string
	gets    int
}

func (s *mapStore) Get(_ context.Context, key string) (string, error) {
	s.gets++
	if value, ok := s.entries[key]; ok {
		return value, nil
	}
	return "", ErrNoEntry
}
func (s *mapStore) Set(key, value string) error { s.entries[key] = value; return nil }
func (s *mapStore) Delete(key string) error     { delete(s.entries, key); return nil }

func forwardCred(storage string) config.Credential {
	return config.Credential{Type: storage, Forward: true, Fields: []string{"user", "pass"}}
}

func TestResolveForwarded(t *testing.T) {
	const canaryUser, canaryPass = `us<er>&"x`, "pa\nss&word"

	t.Run("a keyring credential delivers every field and registers each value", func(t *testing.T) {
		red := &redact.Redactor{}
		store := &mapStore{entries: map[string]string{
			StoreKey("shared", "user"): canaryUser, StoreKey("shared", "pass"): canaryPass,
		}}
		r := NewWith(func(string) string { return "ignored" }, store, nil, red)
		got, err := r.ResolveForwarded(context.Background(),
			ForwardRef{Name: "shared", Cred: forwardCred(config.CredentialTypeKeyring)})
		if err != nil {
			t.Fatalf("ResolveForwarded() = %v", err)
		}
		if got["user"] != canaryUser || got["pass"] != canaryPass || len(got) != 2 {
			t.Errorf("ResolveForwarded() delivered the wrong fields")
		}
		for _, value := range []string{canaryUser, canaryPass} {
			if out := red.Apply("x " + value + " y"); strings.Contains(out, value) {
				t.Errorf("a delivered value was not registered with the redactor")
			}
		}
	})

	t.Run("a derived environment variable is not consulted", func(t *testing.T) {
		store := &mapStore{entries: map[string]string{}}
		r := NewWith(func(string) string { return "from-env-value" }, store, nil, nil)
		_, err := r.ResolveForwarded(context.Background(),
			ForwardRef{Name: "shared", Cred: forwardCred(config.CredentialTypeKeyring)})
		var missing *MissingSecretError
		if !errors.As(err, &missing) {
			t.Fatalf("ResolveForwarded() = %v, want *MissingSecretError", err)
		}
		if strings.Contains(err.Error(), "from-env-value") {
			t.Errorf("the error carries a value: %v", err)
		}
	})

	t.Run("a missing field fails without delivering the others", func(t *testing.T) {
		store := &mapStore{entries: map[string]string{StoreKey("shared", "user"): canaryUser}}
		r := NewWith(nil, store, nil, nil)
		got, err := r.ResolveForwarded(context.Background(),
			ForwardRef{Name: "shared", Cred: forwardCred(config.CredentialTypeKeyring)})
		var missing *MissingSecretError
		if !errors.As(err, &missing) || got != nil || missing.Role != "pass" {
			t.Fatalf("ResolveForwarded() = %v, %v, want a missing pass field and no values", got, err)
		}
		if strings.Contains(err.Error(), canaryUser) {
			t.Errorf("the error carries a value: %v", err)
		}
	})

	t.Run("a credential that is not a forward credential is refused", func(t *testing.T) {
		r := NewWith(nil, &mapStore{}, nil, nil)
		if _, err := r.ResolveForwarded(context.Background(),
			ForwardRef{Name: "plain", Cred: config.Credential{Type: config.CredentialTypeKeyring}}); err == nil {
			t.Fatal("ResolveForwarded() = nil, want an error")
		}
	})

	t.Run("an unencrypted vault delivers directly", func(t *testing.T) {
		red := &redact.Redactor{}
		v := vault.New(t.TempDir())
		for field, value := range map[string]string{"user": canaryUser, "pass": canaryPass} {
			if err := v.Set("shared", field, value, nil); err != nil {
				t.Fatalf("vault Set() = %v", err)
			}
		}
		r := NewWith(nil, nil, nil, red).WithVault(v, nil)
		ref := ForwardRef{Name: "shared", Cred: forwardCred(config.CredentialTypeVault)}
		got, err := r.ResolveForwarded(context.Background(), ref)
		if err != nil || got["user"] != canaryUser || got["pass"] != canaryPass {
			t.Fatalf("ResolveForwarded() = %v, want both fields", err)
		}
		if err := r.UsableForwarded(context.Background(), wikiConnection(), ref); err != nil {
			t.Errorf("UsableForwarded() = %v, want nil", err)
		}
	})
}
