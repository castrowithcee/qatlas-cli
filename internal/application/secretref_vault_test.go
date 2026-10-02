package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// newVaultRefFixture is the reference fixture with the connection's own credential and the forward credential
// both in an encrypted vault unlocked in this process, and the connection not approved yet.
func newVaultRefFixture(t *testing.T) (*refFixture, *vault.Vault) {
	t.Helper()
	f := newRefFixture(t)
	f.cfg.Credentials["own"] = config.Credential{Type: config.CredentialTypeVault}
	f.cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeVault, Forward: true,
		Fields: []string{"user", "pass"}, Description: "shared login"}
	// A second connection of the same service that releases nothing, and a third that lists the credential
	// without ever being approved.
	f.cfg.Connections["bare"] = config.Connection{Service: "svc", Credential: "own", Tools: []string{"fake.accounts.create"},
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate}}
	f.cfg.Connections["late"] = config.Connection{Service: "svc", Credential: "own", Tools: []string{"fake.accounts.create"},
		Permissions:    []config.Permission{config.PermissionRead, config.PermissionCreate},
		ForwardSecrets: []string{"shared"}}

	v := vault.New(t.TempDir())
	if err := v.Set("own", "token", "synthetic-own-token", func(string) (string, error) { return "s3cret-phrase", nil }); err != nil {
		t.Fatal(err)
	}
	for field, value := range map[string]string{"user": canaryUser, "pass": canaryPass} {
		if err := v.Set("shared", field, value, nil); err != nil {
			t.Fatal(err)
		}
	}
	f.core.secrets = secret.NewWith(func(string) string { return "" }, f.store, nil, f.redactor).WithVault(v, nil)
	return f, v
}

func (f *refFixture) approve(t *testing.T, v *vault.Vault, names ...string) {
	t.Helper()
	var scopes []vault.Scope
	for _, name := range names {
		resolved, err := f.cfg.Resolve(name, "")
		if err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, secret.ScopeOf(resolved))
	}
	if err := v.Approve(scopes); err != nil {
		t.Fatal(err)
	}
}

func (f *refFixture) invokeOn(connection, secretRef string) (InvokeResponse, error) {
	arguments, _ := json.Marshal(map[string]string{"name": "acme", "secret": secretRef})
	return f.core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.accounts.create", Connection: connection, Arguments: arguments, Confirmed: true,
	})
}

func TestEncryptedVaultForwardsOnlyAfterApproval(t *testing.T) {
	f, v := newVaultRefFixture(t)

	// Before the approval nothing is read, sent, or leaked.
	response, err := f.invokeOn("primary", "shared")
	var approval *secret.ApprovalRequiredError
	if !errors.As(err, &approval) || ErrorCode(err) != output.CodeApprovalRequired {
		t.Fatalf("Invoke() before the approval = %v, want approval-required", err)
	}
	if len(f.bodies) != 0 {
		t.Fatalf("the provider got a request before the approval")
	}
	assertNoCanary(t, "the surfaces before the approval", f.everywhere(t, response, err))
	refs := f.core.Connections("", func(*config.Resolved) error { return nil }).Connections
	for _, c := range refs {
		if c.Name == "primary" && (len(c.ForwardSecrets) != 1 || c.ForwardSecrets[0].Unusable == nil ||
			*c.ForwardSecrets[0].Unusable != string(output.CodeApprovalRequired)) {
			t.Errorf("discovery before the approval = %+v, want shared unusable with approval-required", c.ForwardSecrets)
		}
	}

	f.approve(t, v, "primary")
	response, err = f.invokeOn("primary", "shared")
	if err != nil {
		t.Fatalf("Invoke() after the approval = %v", err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("the provider got %d requests, want 1", len(f.bodies))
	}
	var sent map[string]string
	if err := json.Unmarshal(f.bodies[0], &sent); err != nil || sent["user"] != canaryUser || sent["pass"] != canaryPass {
		t.Fatalf("the provider did not get both fields: %v", err)
	}
	if !strings.Contains(string(response.Result), redact.Marker) {
		t.Errorf("the mirrored body was not redacted: %s", response.Result)
	}
	assertNoCanary(t, "the surfaces after the approval", f.everywhere(t, response, nil))
	if !strings.Contains(f.audit.String(), `"secret_refs":["shared"]`) {
		t.Errorf("the audit event does not name the reference: %s", f.audit.String())
	}
	for _, c := range f.core.Connections("", func(*config.Resolved) error { return nil }).Connections {
		if c.Name == "primary" && (len(c.ForwardSecrets) != 1 || c.ForwardSecrets[0].Unusable != nil) {
			t.Errorf("discovery after the approval = %+v, want shared usable", c.ForwardSecrets)
		}
	}

	// A connection that does not release the credential is refused by the core; one that lists it but was
	// never approved is refused by the vault.
	response, err = f.invokeOn("bare", "shared")
	var refused *SecretRefNotAllowedError
	if !errors.As(err, &refused) {
		t.Fatalf("Invoke() on a connection without the release = %v, want secret-ref-not-allowed", err)
	}
	assertNoCanary(t, "the surfaces of the refusal", f.everywhere(t, response, err))
	response, err = f.invokeOn("late", "shared")
	if !errors.As(err, &approval) {
		t.Fatalf("Invoke() on an unapproved connection that lists it = %v, want approval-required", err)
	}
	assertNoCanary(t, "the surfaces of the second refusal", f.everywhere(t, response, err))
	if len(f.bodies) != 1 {
		t.Errorf("a refused invoke reached the provider")
	}
}

func TestEncryptedVaultForwardNeedsNewApprovalAfterAChange(t *testing.T) {
	f, v := newVaultRefFixture(t)
	f.approve(t, v, "primary")
	if _, err := f.invokeOn("primary", "shared"); err != nil {
		t.Fatalf("Invoke() after the approval = %v", err)
	}

	// A field added to the forward credential.
	shared := f.cfg.Credentials["shared"]
	shared.Fields = []string{"user", "pass", "token"}
	f.cfg.Credentials["shared"] = shared
	response, err := f.invokeOn("primary", "shared")
	var approval *secret.ApprovalRequiredError
	if !errors.As(err, &approval) {
		t.Fatalf("Invoke() after the fields changed = %v, want approval-required", err)
	}
	assertNoCanary(t, "the surfaces", f.everywhere(t, response, err))
	f.approve(t, v, "primary")
	// The vault holds no value for the new field, which is a missing secret and not a leak.
	if _, err := f.invokeOn("primary", "shared"); err == nil || errors.As(err, &approval) {
		t.Errorf("Invoke() with a field the vault lacks = %v, want a missing secret", err)
	}
	shared.Fields = []string{"user", "pass"}
	f.cfg.Credentials["shared"] = shared
	f.approve(t, v, "primary")
	if _, err := f.invokeOn("primary", "shared"); err != nil {
		t.Fatalf("Invoke() after restoring the fields = %v", err)
	}

	// The allowlist changed: the connection lists a second credential.
	f.cfg.Credentials["second"] = config.Credential{Type: config.CredentialTypeVault, Forward: true,
		Fields: []string{"key"}}
	primary := f.cfg.Connections["primary"]
	primary.ForwardSecrets = []string{"shared", "second"}
	f.cfg.Connections["primary"] = primary
	if _, err := f.invokeOn("primary", "shared"); !errors.As(err, &approval) {
		t.Fatalf("Invoke() after the allowlist grew = %v, want approval-required", err)
	}
}
