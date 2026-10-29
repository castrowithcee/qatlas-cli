package connlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const retentionPassphrase = "retention-phrase"

// oldDay writes a day file of the log below vaultDir that is days days old, holding one entry.
func oldDay(t *testing.T, vaultDir string, days int) string {
	t.Helper()
	dir := filepath.Join(vaultDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().AddDate(0, 0, -days)
	path := filepath.Join(dir, day.Format("2006-01-02")+".jsonl")
	line := `{"seq":1,"time":"` + day.Format(time.RFC3339) + `","path":"cli","operation":"old.entry"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendVia(t *testing.T, v *vault.Vault, dir string) {
	t.Helper()
	writer := SigningWriter{Logger: invokelog.New(dir, 0), Vault: v}
	if err := writer.Append(invokelog.Fields{Path: "cli", Operation: "new.entry", Effect: "read", Result: "success"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// encryptedVault returns the directory holding an encrypted vault and a locked handle on it.
func encryptedVault(t *testing.T) (string, *vault.Vault) {
	t.Helper()
	vault.LowerWorkFactorForTests()
	root := t.TempDir()
	if err := vault.New(root).Set("gh", "token", "synthetic", func(string) (string, error) { return retentionPassphrase, nil }); err != nil {
		t.Fatal(err)
	}
	return root, vault.New(root)
}

func unlock(t *testing.T, v *vault.Vault) {
	t.Helper()
	if _, err := v.Unlock(retentionPassphrase); err != nil {
		t.Fatal(err)
	}
}

func TestLockedWriterWithoutAProcessNeverRemovesADayFile(t *testing.T) {
	_, v := encryptedVault(t)
	old := oldDay(t, v.Dir(), 1000)
	appendVia(t, v, v.Dir())
	if !exists(old) {
		t.Fatal("a writer that cannot read settings.age removed a day file")
	}
}

func TestUnlockedWriterAppliesTheRetentionOfSettingsAge(t *testing.T) {
	_, v := encryptedVault(t)
	unlock(t, v)
	settings := vault.DefaultSettings()
	settings.RetentionDays = 5
	if err := v.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	old, recent := oldDay(t, v.Dir(), 10), oldDay(t, v.Dir(), 2)
	appendVia(t, v, v.Dir())
	if exists(old) || !exists(recent) {
		t.Fatalf("old day kept = %v, recent day kept = %v, want the 5 day window of settings.age", exists(old), exists(recent))
	}
}

func TestUntrustedSettingsAgeNeverTruncates(t *testing.T) {
	_, v := encryptedVault(t)
	unlock(t, v)
	if err := os.WriteFile(filepath.Join(v.Dir(), "settings.age"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := oldDay(t, v.Dir(), 1000)
	appendVia(t, v, v.Dir())
	if !exists(old) {
		t.Fatal("an untrusted settings.age truncated the log")
	}
}

func TestUnencryptedVaultKeepsNinetyDays(t *testing.T) {
	root := t.TempDir()
	v := vault.New(root)
	if err := v.Set("gh", "token", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	old, recent := oldDay(t, v.Dir(), 100), oldDay(t, v.Dir(), 80)
	appendVia(t, v, v.Dir())
	if exists(old) || !exists(recent) {
		t.Fatalf("old day kept = %v, recent day kept = %v, want 90 days", exists(old), exists(recent))
	}
}

func TestConfigRetentionHasNoSayInTheRecorder(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("logs:\n  retention_days: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := oldDay(t, vault.New(root).Dir(), 50)
	if err := NewRecorder(SurfaceCLI, configPath, nil, false).Record(configWith(), configWith("a")); err != nil {
		t.Fatal(err)
	}
	if !exists(old) {
		t.Fatal("logs.retention_days of config.yaml shortened the log")
	}
}
