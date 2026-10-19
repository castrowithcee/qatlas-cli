package secret

import (
	"context"
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Editing only the file from disabled to active does not reach the secret: the vault approved the connection
// as disabled.
func TestSwitchingOnOutsideTheVaultNeedsAnApproval(t *testing.T) {
	r, v, _ := encryptedForwardVault(t)
	off := wikiConnection()
	off.Disabled = true
	if scope := ScopeOf(off); !scope.Disabled || ScopeOf(wikiConnection()).Disabled {
		t.Fatal("ScopeOf() does not carry exactly the disabled state")
	}
	if err := v.Approve([]vault.Scope{ScopeOf(off)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ForConnection(context.Background(), off), credName, vaultCred(), role); err != nil {
		t.Fatalf("Resolve() as approved = %v", err)
	}
	_, err := r.Resolve(ForConnection(context.Background(), wikiConnection()), credName, vaultCred(), role)
	if !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("Resolve() after switching on = %v, want ErrApprovalRequired", err)
	}
}

// Editing only the file to lengthen a limit does not reach the secret: the vault approved the shorter one.
func TestLengtheningALimitOutsideTheVaultNeedsAnApproval(t *testing.T) {
	r, v, _ := encryptedForwardVault(t)
	short, long := wikiConnection(), wikiConnection()
	short.ActiveUntil, long.ActiveUntil = "2030-01-02T12:00:00Z", "2030-01-02T18:00:00Z"
	if err := v.Approve([]vault.Scope{ScopeOf(short)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ForConnection(context.Background(), short), credName, vaultCred(), role); err != nil {
		t.Fatalf("Resolve() as approved = %v", err)
	}
	_, err := r.Resolve(ForConnection(context.Background(), long), credName, vaultCred(), role)
	if !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("Resolve() after lengthening = %v, want ErrApprovalRequired", err)
	}
}
