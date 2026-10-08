//go:build linux || darwin

package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// A vault payload credential saved while a vault process refuses to take changes reports both warnings: the
// one of handing the new value on and the one of handing the removal of a dropped field on. The second must
// not be lost because the first is already there.
func TestPayloadSaveChainsTheVaultProcessWarnings(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)
	mustNoError(t, secrets.SetVault("billing", "user", "old-user-1", func(string) (string, error) { return passphrase, nil }))
	mustNoError(t, secrets.SetVault("billing", "pass", "old-pass-1", nil))
	server, _ := serveVaultProcess(t, dir, passphrase)
	server.Verify = refuseEveryClient

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{"user", "pass"}}))
	mustNoError(t, store.Save(cfg))

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openEntryForm(t, m, sectionCredentials, "billing")
	for m.fields[m.focus].label != "user" {
		pump(t, m, "tab")
	}
	typeText(t, m, "new-user-value-2")
	pump(t, m, "tab")
	m.Update(ctrlD())
	pump(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Fatalf("the encrypted vault did not ask for the admin passphrase: screen %v", m.screen)
	}
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("saving reported %q", m.fail)
	}
	const warning = "warning: the vault holds the change"
	if got := strings.Count(m.status, warning); got != 2 {
		t.Errorf("status = %q, want the process warning of the new value and of the removal (%d found)", m.status, got)
	}
	for _, secretValue := range []string{"new-user-value-2", "old-user-1", "old-pass-1"} {
		if strings.Contains(m.status, secretValue) {
			t.Errorf("status = %q, a stored value must never appear in it", m.status)
		}
	}
}
