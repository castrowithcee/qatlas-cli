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

const canaryVault = "canary-from-vault-6e19b2"

func vaultCred() config.Credential { return config.Credential{Type: config.CredentialTypeVault} }

// vaultFixture builds a resolver whose vault is a fresh, unencrypted vault in a temporary directory. ask
// stands in for the terminal: nil means none is attached.
func vaultFixture(t *testing.T, ask vault.PassphraseFunc) (*Resolver, *vault.Vault, *redact.Redactor) {
	t.Helper()
	red := &redact.Redactor{}
	v := vault.New(t.TempDir())
	r := NewWith(func(string) string { return "" }, nil, nil, red).WithVault(v, ask)
	return r, v, red
}

// A vault credential is resolved from the vault alone: the environment variable it derives still wins, but
// nothing falls through to the system keyring or the plaintext fallback, and the vault is never asked when
// the environment already answered.
func TestResolveVault(t *testing.T) {
	t.Run("the derived environment variable wins over the vault", func(t *testing.T) {
		derived := DerivedEnvName(credName, role)
		red := &redact.Redactor{}
		v := vault.New(t.TempDir())
		if err := v.Set(credName, role, canaryVault, nil); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
		env := map[string]string{derived: canaryEnv}
		r := NewWith(func(name string) string { return env[name] }, nil, nil, red).WithVault(v, nil)

		got, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if got.Source != SourceEnv || got.Secret != canaryEnv {
			t.Errorf("Resolve() = %+v, want the environment variable to deliver %q", got, canaryEnv)
		}
	})

	t.Run("an unencrypted vault delivers without asking for a passphrase", func(t *testing.T) {
		r, v, red := vaultFixture(t, nil)
		if err := v.Set(credName, role, canaryVault, nil); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}

		got, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if got.Source != SourceVault || got.Secret != canaryVault {
			t.Errorf("Resolve() = %+v, want the vault to deliver %q", got, canaryVault)
		}
		// The redactor must have learned the value: a secret handed out is a secret that gets caught
		// wherever it is printed afterwards.
		if redacted := red.Apply("token: " + canaryVault); strings.Contains(redacted, canaryVault) {
			t.Errorf("Apply() = %q, still contains the resolved value", redacted)
		}
	})

	t.Run("no entry in the vault is a missing secret, not an error", func(t *testing.T) {
		r, _, _ := vaultFixture(t, nil)
		_, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		var missing *MissingSecretError
		if !errors.As(err, &missing) {
			t.Fatalf("Resolve() = %v, want *MissingSecretError", err)
		}
	})

	t.Run("a locked vault without a terminal is VaultLockedError, exit-relevant and distinct from missing-secret", func(t *testing.T) {
		red := &redact.Redactor{}
		dir := t.TempDir()
		v := vault.New(dir)
		if err := v.Set(credName, role, canaryVault, offeringPassphrase("s3cret")); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
		// A fresh Vault value, over the same directory, stands in for the next process: locked again.
		locked := vault.New(dir)
		r := NewWith(func(string) string { return "" }, nil, nil, red).WithVault(locked, nil)

		_, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		var lockedErr *VaultLockedError
		if !errors.As(err, &lockedErr) {
			t.Fatalf("Resolve() = %v, want *VaultLockedError", err)
		}
		var missing *MissingSecretError
		if errors.As(err, &missing) {
			t.Fatalf("Resolve() = %v, a VaultLockedError must not also be a MissingSecretError", err)
		}
	})

	t.Run("a locked vault unlocks interactively when a terminal is attached", func(t *testing.T) {
		red := &redact.Redactor{}
		dir := t.TempDir()
		v := vault.New(dir)
		if err := v.Set(credName, role, canaryVault, offeringPassphrase("s3cret")); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
		locked := vault.New(dir)
		r := NewWith(func(string) string { return "" }, nil, nil, red).WithVault(locked, offeringPassphrase("s3cret"))

		got, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if got.Secret != canaryVault {
			t.Errorf("Resolve() = %+v, want %q", got, canaryVault)
		}
	})

	t.Run("Unattended never asks the vault for a passphrase", func(t *testing.T) {
		dir := t.TempDir()
		v := vault.New(dir)
		if err := v.Set(credName, role, canaryVault, offeringPassphrase("s3cret")); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
		locked := vault.New(dir)
		red := &redact.Redactor{}
		r := NewWith(func(string) string { return "" }, nil, nil, red).WithVault(locked, offeringPassphrase("s3cret"))
		r.Unattended()

		_, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		var lockedErr *VaultLockedError
		if !errors.As(err, &lockedErr) {
			t.Fatalf("Resolve() while unattended = %v, want *VaultLockedError even though ask would succeed", err)
		}
	})

	t.Run("a resolver without a vault reports the secret missing rather than panicking", func(t *testing.T) {
		red := &redact.Redactor{}
		r := NewWith(func(string) string { return "" }, nil, nil, red)
		_, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		var missing *MissingSecretError
		if !errors.As(err, &missing) {
			t.Fatalf("Resolve() = %v, want *MissingSecretError", err)
		}
	})
}

func offeringPassphrase(passphrase string) vault.PassphraseFunc {
	return func(string) (string, error) { return passphrase, nil }
}
