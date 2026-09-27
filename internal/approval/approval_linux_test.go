//go:build linux

package approval

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// serve starts a vault process for v in this test process, with what v holds now, and returns its client.
func serve(t *testing.T, v *vault.Vault, verify vaultproc.Verifier) *vaultproc.Client {
	t.Helper()
	runtimeDir, err := os.MkdirTemp("", "qa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	client, err := vaultmigrate.ProcessClientOf(v)
	if err != nil {
		t.Fatal(err)
	}
	l, err := vaultproc.Listen(client.Path)
	if err != nil {
		t.Fatal(err)
	}
	server := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
	server.Verify = verify
	done := make(chan struct{})
	go func() { _ = server.Serve(l); close(done) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	return client
}

func scopeOf(t *testing.T, cfg *config.Config, name string) vault.Scope {
	t.Helper()
	resolved, err := cfg.Resolve(name, "")
	if err != nil {
		t.Fatal(err)
	}
	return secret.ScopeOf(resolved)
}

// Approving and revoking reach a running vault process at once, the entry ids of credentials stored while
// it ran included.
func TestApprovalsReachTheVaultProcess(t *testing.T) {
	cfg, v := fixture(t)
	client := serve(t, v, nil)
	ctx := context.Background()
	read := scopeOf(t, cfg, "wiki-read")
	if err := client.Check(ctx, read); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("Check() before Approve() = %v, want vault.ErrApprovalRequired", err)
	}
	if _, warning, err := Approve(ctx, cfg, v, nil); err != nil || warning != "" {
		t.Fatalf("Approve() = %q, %v", warning, err)
	}
	if value, found, err := client.Get(ctx, "reader", "token", read); err != nil || !found || value != "synthetic-reader" {
		t.Fatalf("Get() after Approve() = %v, %v", found, err)
	}
	if _, _, err := Revoke(ctx, v, []string{"wiki-read"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Check(ctx, read); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("Check() after Revoke() = %v, want vault.ErrApprovalRequired", err)
	}
}

// A vault process that cannot take the approvals is a warning naming the way out, never a failure of the
// approval itself and never carrying a secret.
func TestApproveWarnsWhenTheVaultProcessRefuses(t *testing.T) {
	cfg, v := fixture(t)
	serve(t, v, func(net.Conn) error {
		return fmt.Errorf("%w: its program was replaced", vaultproc.ErrRefused)
	})
	approved, warning, err := Approve(context.Background(), cfg, v, nil)
	if err != nil || len(approved) != 2 {
		t.Fatalf("Approve() = %v, %v", approved, err)
	}
	if !strings.Contains(warning, "vault process") || !strings.Contains(warning, "kill") ||
		strings.Contains(warning, "synthetic-") || strings.Contains(warning, passphrase) {
		t.Errorf("Approve() warning = %q, want the remedy and no secret", warning)
	}
}
