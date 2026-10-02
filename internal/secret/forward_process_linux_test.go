//go:build linux

package secret

import (
	"context"
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A locked encrypted vault hands a forward credential out through the vault process, to the connection that
// releases it and was approved so, and to no other.
func TestResolveForwardedFromTheVaultProcess(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Set(credName, role, canaryVault, func(string) (string, error) { return processPassphrase, nil }); err != nil {
		t.Fatal(err)
	}
	for field, value := range map[string]string{"user": fwdUser, "pass": fwdPass} {
		if err := v.Set("shared", field, value, nil); err != nil {
			t.Fatal(err)
		}
	}
	resolved := releasingConnection("wiki", "user", "pass")
	ref := sharedRef(resolved)
	if err := v.Approve([]vault.Scope{ScopeOf(resolved)}); err != nil {
		t.Fatal(err)
	}
	ask := func(string) (string, error) {
		t.Errorf("the passphrase was asked for")
		return "", vault.ErrNoTerminal
	}
	r := NewWith(nil, nil, nil, &redact.Redactor{}).WithVault(vault.New(dir), ask)
	r.process = true
	r.Unattended()
	ctx := ForConnection(context.Background(), resolved)

	var locked *VaultLockedError
	if err := r.UsableForwarded(context.Background(), resolved, ref); !errors.As(err, &locked) {
		t.Fatalf("UsableForwarded() without a vault process = %v, want *VaultLockedError", err)
	}
	if _, err := r.ResolveForwarded(ctx, ref); !errors.As(err, &locked) {
		t.Fatalf("ResolveForwarded() without a vault process = %v, want *VaultLockedError", err)
	}

	s := startProcess(t, dir, nil)
	defer s.Close()
	got, err := r.ResolveForwarded(ctx, ref)
	if err != nil || got["user"] != fwdUser || got["pass"] != fwdPass {
		t.Fatalf("ResolveForwarded() through the process = %v, want both fields", err)
	}
	if err := r.UsableForwarded(context.Background(), resolved, ref); err != nil {
		t.Errorf("UsableForwarded() through the process = %v, want nil", err)
	}

	other := releasingConnection("other", "user", "pass")
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), other), ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() for another connection = %v, want ErrApprovalRequired", err)
	}
	if err := r.UsableForwarded(context.Background(), other, ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("UsableForwarded() for another connection = %v, want ErrApprovalRequired", err)
	}
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), wikiConnection()), ref); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() for a connection without the release = %v, want ErrApprovalRequired", err)
	}
	changed := releasingConnection("wiki", "user", "pass", "key")
	if _, err := r.ResolveForwarded(ForConnection(context.Background(), changed), sharedRef(changed)); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("ResolveForwarded() after the fields changed = %v, want ErrApprovalRequired", err)
	}
}
