package secret

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const canaryOptional = "canary-optional-7c03e1"

// An optional role is set, simply not set, or cannot be told; only the last one is an error.
func TestResolveOptional(t *testing.T) {
	derived := DerivedEnvName(credName, role)

	t.Run("a value found is delivered and redacted", func(t *testing.T) {
		r, store, _, red := fixture(t, nil)
		if err := store.Set(StoreKey(credName, role), canaryOptional); err != nil {
			t.Fatal(err)
		}
		got, found, err := r.ResolveOptional(context.Background(), credName, keyringCred(), role)
		if err != nil || !found || got.Secret != canaryOptional || got.Source != SourceStore {
			t.Fatalf("ResolveOptional() = %+v, %v, %v", got.Source, found, err)
		}
		if out := red.Apply("value " + canaryOptional); strings.Contains(out, canaryOptional) {
			t.Errorf("the redactor does not know the optional value: %q", out)
		}
	})

	t.Run("an env variable found is delivered and redacted", func(t *testing.T) {
		r, _, _, red := fixture(t, map[string]string{"SITE_PASSWORD": canaryOptional})
		got, found, err := r.ResolveOptional(context.Background(), credName, envCred("SITE_PASSWORD"), role)
		if err != nil || !found || got.Source != SourceEnv {
			t.Fatalf("ResolveOptional() = %+v, %v, %v", got.Source, found, err)
		}
		if strings.Contains(red.Apply(canaryOptional), canaryOptional) {
			t.Error("the redactor does not know the optional value")
		}
	})

	notSet := map[string]func(t *testing.T) (*Resolver, config.Credential){
		"env credential that names no variable": func(t *testing.T) (*Resolver, config.Credential) {
			r, _, _, _ := fixture(t, nil)
			return r, config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{"other": "X"}}
		},
		"env credential whose variable is not set": func(t *testing.T) (*Resolver, config.Credential) {
			r, _, _, _ := fixture(t, nil)
			return r, envCred("SITE_PASSWORD")
		},
		"keyring without an entry": func(t *testing.T) (*Resolver, config.Credential) {
			r, _, _, _ := fixture(t, nil)
			return r, keyringCred()
		},
		"keyring switched off": func(t *testing.T) (*Resolver, config.Credential) {
			return NewWith(nil, nil, nil, &redact.Redactor{}), keyringCred()
		},
		"vault without an entry": func(t *testing.T) (*Resolver, config.Credential) {
			r, v, _ := vaultFixture(t, nil)
			if err := v.Set(credName, "other", canaryVault, nil); err != nil {
				t.Fatal(err)
			}
			return r, vaultCred()
		},
		"resolver without a vault": func(t *testing.T) (*Resolver, config.Credential) {
			return NewWith(nil, nil, nil, &redact.Redactor{}), vaultCred()
		},
		"absent vault": func(t *testing.T) (*Resolver, config.Credential) {
			r, _, _ := vaultFixture(t, nil)
			return r, vaultCred()
		},
	}
	for name, build := range notSet {
		t.Run(name+" is not set", func(t *testing.T) {
			r, cred := build(t)
			got, found, err := r.ResolveOptional(context.Background(), credName, cred, role)
			if err != nil || found || got.Secret != "" {
				t.Fatalf("ResolveOptional() = %q, %v, %v, want not set", got.Secret, found, err)
			}
			if source, _ := r.OptionalStatusContext(context.Background(), credName, cred, role); source != SourceNotSet {
				t.Errorf("OptionalStatusContext() = %q, want %q", source, SourceNotSet)
			}
			// The same role, asked for as a required one, is still missing.
			if _, err := r.Resolve(context.Background(), credName, cred, role); !errors.As(err, new(*MissingSecretError)) {
				t.Errorf("Resolve() = %v, want *MissingSecretError", err)
			}
			if source, _ := r.StatusContext(context.Background(), credName, cred, role); source != SourceMissing {
				t.Errorf("StatusContext() = %q, want %q", source, SourceMissing)
			}
		})
	}

	t.Run("a locked keyring is an error", func(t *testing.T) {
		r, store, _, _ := fixture(t, nil)
		store.Fail(ErrLocked)
		_, found, err := r.ResolveOptional(context.Background(), credName, keyringCred(), role)
		var missing *MissingSecretError
		if found || !errors.As(err, &missing) || StoreStage(missing.Checked) != StoreLocked {
			t.Fatalf("ResolveOptional() = %v, %v, want the locked store as an error", found, err)
		}
		if source, _ := r.OptionalStatusContext(context.Background(), credName, keyringCred(), role); source != SourceMissing {
			t.Errorf("OptionalStatusContext() = %q, want %q", source, SourceMissing)
		}
	})

	t.Run("a fallback file readable by others is an error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("file modes do not carry on Windows")
		}
		r, _, file, _ := fixture(t, nil)
		setFixture(t, file, credName, "other", canaryPlaintext)
		if err := os.Chmod(file.Path(), 0o644); err != nil {
			t.Fatal(err)
		}
		_, found, err := r.ResolveOptional(context.Background(), credName, keyringCred(), role)
		if found || err == nil || strings.Contains(err.Error(), canaryPlaintext) {
			t.Fatalf("ResolveOptional() = %v, %v, want an error without the file's content", found, err)
		}
	})

	t.Run("a locked vault is an error", func(t *testing.T) {
		dir := encryptedWiki(t)
		r := NewWith(nil, nil, nil, &redact.Redactor{}).WithVault(vault.New(dir), nil)
		_, found, err := r.ResolveOptional(throughWiki(), credName, vaultCred(), "password")
		if found || !errors.As(err, new(*VaultLockedError)) {
			t.Fatalf("ResolveOptional() = %v, %v, want *VaultLockedError", found, err)
		}
	})

	t.Run("an unapproved connection is an error, not an unset role", func(t *testing.T) {
		dir := encryptedWiki(t)
		_, found, err := attended(dir).ResolveOptional(context.Background(), credName, vaultCred(), "password")
		if found || !errors.As(err, new(*ApprovalRequiredError)) {
			t.Fatalf("ResolveOptional() = %v, %v, want *ApprovalRequiredError", found, err)
		}
	})

	t.Run("an approved connection without the role is not set", func(t *testing.T) {
		dir := encryptedWiki(t)
		_, found, err := attended(dir).ResolveOptional(throughWiki(), credName, vaultCred(), "password")
		if err != nil || found {
			t.Fatalf("ResolveOptional() = %v, %v, want not set", found, err)
		}
	})

	t.Run("the derived variable answers for a keyring credential", func(t *testing.T) {
		r, _, _, _ := fixture(t, map[string]string{derived: canaryOptional})
		got, found, err := r.ResolveOptional(context.Background(), credName, keyringCred(), role)
		if err != nil || !found || got.Source != SourceEnv {
			t.Fatalf("ResolveOptional() = %+v, %v, %v", got.Source, found, err)
		}
	})
}
