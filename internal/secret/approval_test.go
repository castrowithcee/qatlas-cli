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

// scopeChanges are the ways a connection can change after it was approved, each of which leaves it open.
func scopeChanges() map[string]func(*config.Resolved) {
	return map[string]func(*config.Resolved){
		"origin":      func(r *config.Resolved) { r.BaseURL = "https://moved.example.invalid" },
		"provider":    func(r *config.Resolved) { r.Provider = "nextcloud" },
		"permissions": func(r *config.Resolved) { r.Permissions = append(r.Permissions, config.PermissionDelete) },
		"target":      func(r *config.Resolved) { r.Target = "shelf-9" },
		"targets":     func(r *config.Resolved) { r.Targets = []string{"shelf-1", "shelf-2"} },
		"tools":       func(r *config.Resolved) { r.Tools = []string{"bookstack.pages.list"} },
		"empty tools": func(r *config.Resolved) { r.Tools = []string{} },
	}
}

// encryptedWiki writes an encrypted vault holding the wiki credential, approved for wikiConnection, and
// returns its directory.
func encryptedWiki(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Set(credName, role, canaryVault, offeringPassphrase("s3cret")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	approveWiki(t, v)
	return dir
}

// attended returns a resolver over the vault in dir, locked, with a terminal that answers the passphrase.
func attended(dir string) *Resolver {
	return NewWith(func(string) string { return "" }, nil, nil, &redact.Redactor{}).
		WithVault(vault.New(dir), offeringPassphrase("s3cret"))
}

func assertApprovalRequired(t *testing.T, err error, connection string) {
	t.Helper()
	var approval *ApprovalRequiredError
	if !errors.As(err, &approval) || approval.Connection != connection || !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("error = %v, want *ApprovalRequiredError for connection %q", err, connection)
	}
	if strings.Contains(err.Error(), canaryVault) || !strings.Contains(err.Error(), connection) {
		t.Fatalf("error = %q, want the connection named and no secret", err)
	}
}

// A vault unlocked in this process hands a secret only to the connection it approved, as approved: any
// change of its scope, and an access bound to no connection at all, is refused; approving the changed
// connection lets it through.
func TestResolveInProcessNeedsAnApprovedConnection(t *testing.T) {
	dir := encryptedWiki(t)
	// One resolver, unlocked by its first read, answers every change below the way a TUI that unlocked the
	// vault would.
	r := attended(dir)
	got, err := r.Resolve(throughWiki(), credName, vaultCred(), role)
	if err != nil || got.Secret != canaryVault {
		t.Fatalf("Resolve() through the approved connection = %+v, %v", got, err)
	}

	for name, change := range scopeChanges() {
		t.Run(name, func(t *testing.T) {
			changed := wikiConnection()
			change(changed)
			_, err := r.Resolve(ForConnection(context.Background(), changed), credName, vaultCred(), role)
			assertApprovalRequired(t, err, "wiki")
			var approval *ApprovalRequiredError
			if err := r.Usable(context.Background(), changed); !errors.As(err, &approval) {
				t.Errorf("Usable() of the changed connection = %v, want *ApprovalRequiredError", err)
			}
			if err := r.Usable(context.Background(), wikiConnection()); err != nil {
				t.Errorf("Usable() of the approved connection = %v", err)
			}
		})
	}

	t.Run("no connection", func(t *testing.T) {
		_, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		var approval *ApprovalRequiredError
		if !errors.As(err, &approval) || strings.Contains(err.Error(), canaryVault) {
			t.Fatalf("Resolve() without a connection = %v, want *ApprovalRequiredError", err)
		}
	})

	t.Run("another credential than the connection reads", func(t *testing.T) {
		_, err := r.Resolve(throughWiki(), "other-reader", vaultCred(), role)
		var approval *ApprovalRequiredError
		if !errors.As(err, &approval) {
			t.Fatalf("Resolve() of another credential = %v, want *ApprovalRequiredError", err)
		}
	})

	t.Run("approving the change lets it through", func(t *testing.T) {
		changed := wikiConnection()
		changed.BaseURL = "https://wiki.example.test/v2"
		if _, err := r.Resolve(ForConnection(context.Background(), changed), credName, vaultCred(), role); err == nil {
			t.Fatalf("Resolve() before the approval succeeded")
		}
		if err := r.Vault().Approve([]vault.Scope{ScopeOf(changed)}); err != nil {
			t.Fatalf("Approve() = %v", err)
		}
		got, err := attended(dir).Resolve(ForConnection(context.Background(), changed), credName, vaultCred(), role)
		if err != nil || got.Secret != canaryVault {
			t.Fatalf("Resolve() after the approval = %+v, %v", got, err)
		}
	})
}

// A credential removed from the vault and stored anew under the same name is another credential: the
// approval given for the old one does not cover it.
func TestResolveRefusesACredentialStoredAnew(t *testing.T) {
	dir := encryptedWiki(t)
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret"); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	if _, err := v.Delete(credName, role, nil); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if err := v.Set(credName, role, canaryVault, nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	_, err := attended(dir).Resolve(throughWiki(), credName, vaultCred(), role)
	assertApprovalRequired(t, err, "wiki")
}

// The binding applies to vault credentials of an encrypted vault alone: an unencrypted vault, a keyring
// credential, and an env credential deliver without any connection, the way they always did.
func TestApprovalLeavesOtherSourcesAlone(t *testing.T) {
	t.Run("unencrypted vault", func(t *testing.T) {
		r, v, _ := vaultFixture(t, nil)
		if err := v.Set(credName, role, canaryVault, nil); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
		got, err := r.Resolve(context.Background(), credName, vaultCred(), role)
		if err != nil || got.Secret != canaryVault {
			t.Fatalf("Resolve() = %+v, %v", got, err)
		}
		if err := r.Usable(context.Background(), wikiConnection()); err != nil {
			t.Errorf("Usable() = %v, want nil for an unencrypted vault", err)
		}
	})

	t.Run("keyring and env credentials beside an encrypted vault", func(t *testing.T) {
		dir := encryptedWiki(t)
		store := NewMemoryStore()
		if err := store.Set(StoreKey("keyring-reader", role), "canary-keyring-1f"); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"WIKI_TOKEN": "canary-env-2e"}
		r := NewWith(func(name string) string { return env[name] }, store, nil, &redact.Redactor{}).
			WithVault(vault.New(dir), offeringPassphrase("s3cret"))
		unapproved := wikiConnection()
		unapproved.BaseURL = "https://moved.example.invalid"
		ctx := ForConnection(context.Background(), unapproved)

		keyring := config.Credential{Type: config.CredentialTypeKeyring}
		if got, err := r.Resolve(ctx, "keyring-reader", keyring, role); err != nil || got.Secret != "canary-keyring-1f" {
			t.Errorf("Resolve() of a keyring credential = %+v, %v", got, err)
		}
		envCred := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{role: "WIKI_TOKEN"}}
		if got, err := r.Resolve(ctx, "env-reader", envCred, role); err != nil || got.Secret != "canary-env-2e" {
			t.Errorf("Resolve() of an env credential = %+v, %v", got, err)
		}
		keyringConnection := wikiConnection()
		keyringConnection.Credential, keyringConnection.Secrets = "keyring-reader", keyring
		if err := r.Usable(context.Background(), keyringConnection); err != nil {
			t.Errorf("Usable() of a keyring connection = %v", err)
		}
	})
}
