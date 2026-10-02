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

const (
	fwdUser = `us<er>&"x`
	fwdPass = "pa\nss&word"
)

// releasingConnection is wikiConnection releasing the forward credential shared, whose entry lives in the
// vault, with the given fields.
func releasingConnection(name string, fields ...string) *config.Resolved {
	resolved := wikiConnection()
	resolved.Name = name
	resolved.ForwardSecrets = []string{"shared"}
	resolved.Forward = map[string]config.Credential{"shared": {
		Type: config.CredentialTypeVault, Forward: true, Fields: fields,
	}}
	return resolved
}

func sharedRef(resolved *config.Resolved) ForwardRef {
	return ForwardRef{Name: "shared", Cred: resolved.Forward["shared"]}
}

// encryptedForwardVault returns an encrypted vault unlocked in this process that holds the connection's own
// credential and the forward credential shared, and a resolver over it.
func encryptedForwardVault(t *testing.T) (*Resolver, *vault.Vault, *redact.Redactor) {
	t.Helper()
	red := &redact.Redactor{}
	v := vault.New(t.TempDir())
	if err := v.Set(credName, role, canaryVault, offeringPassphrase("s3cret")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	for field, value := range map[string]string{"user": fwdUser, "pass": fwdPass} {
		if err := v.Set("shared", field, value, nil); err != nil {
			t.Fatalf("vault Set() = %v", err)
		}
	}
	return NewWith(nil, nil, nil, red).WithVault(v, nil), v, red
}

func TestScopeOfCarriesTheForwardCredentials(t *testing.T) {
	if scope := ScopeOf(wikiConnection()); scope.Forward != nil {
		t.Errorf("ScopeOf() of a connection without forward_secrets has Forward %+v", scope.Forward)
	}
	scope := ScopeOf(releasingConnection("wiki", "user", "pass"))
	if len(scope.Forward) != 1 || scope.Forward[0].Name != "shared" ||
		strings.Join(scope.Forward[0].Fields, ",") != "pass,user" {
		t.Errorf("ScopeOf().Forward = %+v, want shared with its sorted fields", scope.Forward)
	}
}

func TestResolveForwardedFromAnEncryptedVault(t *testing.T) {
	r, v, red := encryptedForwardVault(t)
	resolved := releasingConnection("wiki", "user", "pass")
	ref := sharedRef(resolved)
	ctx := ForConnection(context.Background(), resolved)

	// Nothing is handed out to a request bound to no connection, or before the approval.
	for name, c := range map[string]context.Context{"an unbound request": context.Background(), "an unapproved one": ctx} {
		_, err := r.ResolveForwarded(c, ref)
		if !errors.Is(err, vault.ErrApprovalRequired) || strings.Contains(err.Error(), fwdUser) ||
			strings.Contains(err.Error(), fwdPass) {
			t.Fatalf("ResolveForwarded() for %s = %v, want ErrApprovalRequired and no value", name, err)
		}
	}
	if err := r.UsableForwarded(context.Background(), resolved, ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("UsableForwarded() before the approval = %v, want ErrApprovalRequired", err)
	}

	if err := v.Approve([]vault.Scope{ScopeOf(resolved)}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	got, err := r.ResolveForwarded(ctx, ref)
	if err != nil || got["user"] != fwdUser || got["pass"] != fwdPass {
		t.Fatalf("ResolveForwarded() after the approval = %v, want both fields", err)
	}
	for _, value := range []string{fwdUser, fwdPass} {
		if strings.Contains(red.Apply("x "+value+" y"), value) {
			t.Errorf("a delivered value was not registered with the redactor")
		}
	}
	if err := r.UsableForwarded(context.Background(), resolved, ref); err != nil {
		t.Errorf("UsableForwarded() after the approval = %v, want nil", err)
	}

	// Another connection that lists the credential is not approved.
	other := releasingConnection("other", "user", "pass")
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), other), ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() for another connection = %v, want ErrApprovalRequired", err)
	}
	// One that does not list it cannot read it, whatever it is approved for.
	plain := wikiConnection()
	if err := v.Approve([]vault.Scope{ScopeOf(plain)}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), plain), ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() for a connection without the release = %v, want ErrApprovalRequired", err)
	}
	if err := r.UsableForwarded(context.Background(), plain, ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("UsableForwarded() for a connection without the release = %v, want ErrApprovalRequired", err)
	}

	// A change of the release, a field added to the credential or the list widened, needs a new approval.
	wider := releasingConnection("wiki", "user", "pass", "key")
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), wider), sharedRef(wider)); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() after a field was added = %v, want ErrApprovalRequired", err)
	}
	more := releasingConnection("wiki", "user", "pass")
	more.ForwardSecrets = append(more.ForwardSecrets, "extra")
	more.Forward["extra"] = config.Credential{Type: config.CredentialTypeVault, Forward: true, Fields: []string{"k"}}
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), more), ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() after the allowlist grew = %v, want ErrApprovalRequired", err)
	}
	if err := v.Approve([]vault.Scope{ScopeOf(wider)}); err != nil {
		t.Fatal(err)
	}
	if got, err := r.ResolveForwarded(ForConnection(context.Background(), wider), sharedRef(wider)); err == nil && len(got) == 3 {
		t.Errorf("a field the vault holds no entry for was delivered")
	}
}
