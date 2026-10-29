package cli

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The vault process truncates the log with the retention of settings.age and with nothing else.
func TestVaultProcessLogRetentionComesFromSettingsAge(t *testing.T) {
	dir := t.TempDir()
	v := seedEncryptedVault(t, dir, "gh", "token", "synthetic", "retention-phrase")
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got := vaultProcessLogRetention(v.Dir(), key); got != vault.DefaultRetentionDays {
		t.Errorf("retention without settings.age = %d, want %d", got, vault.DefaultRetentionDays)
	}
	settings := vault.DefaultSettings()
	settings.RetentionDays = 7
	if err := v.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := vaultProcessLogRetention(v.Dir(), key); got != 7 {
		t.Errorf("retention = %d, want the 7 days of settings.age", got)
	}
	// A configuration file that asks for one day changes nothing.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("logs:\n  retention_days: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := vaultProcessLogRetention(v.Dir(), key); got != 7 {
		t.Errorf("retention with logs.retention_days: 1 = %d, want 7", got)
	}
	if err := os.WriteFile(filepath.Join(v.Dir(), "settings.age"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := vaultProcessLogRetention(v.Dir(), key); got != 0 {
		t.Errorf("retention with an untrusted settings.age = %d, want 0 (never truncate)", got)
	}
}
