//go:build linux || darwin

package manage

import (
	"context"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A vault process that holds the vault unlocked, with the draft credential's secret in it, gives a draft
// nothing while this process holds the vault locked, whatever the admin session says.
func TestDraftTargetsNeverAskTheVaultProcess(t *testing.T) {
	dir := t.TempDir()
	offer := func(string) (string, error) { return "hunter2-phrase", nil }
	if err := vault.New(dir).Set("vault-cred", "token", canarySecret, offer); err != nil {
		t.Fatal(err)
	}
	serveVault(t, dir, false)
	f := newBrowseFixture(t, vault.New(dir))
	f.svc = f.svc.WithVaultProcessSupport(true)
	f.readSecret = true
	f.list = func(config.TargetListRequest) (config.TargetPage, error) { return config.TargetPage{}, nil }

	_, _, err := f.svc.ListDraftTargets(context.Background(), grantedProof(t), vaultDraft(), config.TargetListRequest{Kind: "space"})
	if choiceCode(err) != "vault-locked-here" || f.calls != 0 || f.seen != "" {
		t.Errorf("err = %v, calls %d, want vault-locked-here without a read", err, f.calls)
	}
}
