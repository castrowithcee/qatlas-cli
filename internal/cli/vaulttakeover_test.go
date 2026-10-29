package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const takeoverPassphrase = "s3cret-phrase"

// takeoverFixture is an encrypted vault whose config.yaml still holds the three old fields.
func takeoverFixture(t *testing.T) string {
	t.Helper()
	dir := vaultCredentialFixture(t)
	seedEncryptedVault(t, dir, "wiki-vault", "token-id", canaryVault, takeoverPassphrase)
	f, err := os.OpenFile(configIn(dir), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("vault:\n  idle_timeout: 2h\n  admin_timeout: 5m\nlogs:\n  retention_days: 30\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func unlockSettings(t *testing.T, dir string) vault.Settings {
	t.Helper()
	v := vault.New(dir)
	if _, err := v.Unlock(takeoverPassphrase); err != nil {
		t.Fatal(err)
	}
	s, err := v.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVaultUnlockTakesOverOldValuesOnYes(t *testing.T) {
	dir := takeoverFixture(t)
	withVaultPassphrase(t, offeringPassphrase(takeoverPassphrase))
	var asked string
	withVaultConfirm(t, func(q string) (bool, error) { asked = q; return true, nil })

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"vault.idle_timeout: 2h", "vault.admin_timeout: 5m", "logs.retention_days: 30", "backups"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	}
	if asked == "" || strings.Contains(stdout, "idle_timeout") || strings.Contains(stdout, "backups") {
		t.Errorf("asked = %q, stdout = %q, want the question on stderr only", asked, stdout)
	}
	if got := unlockSettings(t, dir); got.IdleTimeout != 2*time.Hour || got.AdminTimeout != 5*time.Minute || got.RetentionDays != 30 {
		t.Errorf("Settings() = %+v, want the config values", got)
	}
	data, _ := os.ReadFile(configIn(dir))
	if strings.Contains(string(data), "idle_timeout") || strings.Contains(string(data), "retention_days") {
		t.Errorf("config.yaml = %q, want the fields removed", data)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "backups")); len(entries) == 0 {
		t.Error("no backup was made")
	}
}

func TestVaultUnlockKeepsSettingsOnNoAndAsksOnce(t *testing.T) {
	dir := takeoverFixture(t)
	withVaultPassphrase(t, offeringPassphrase(takeoverPassphrase))
	asks := 0
	withVaultConfirm(t, func(string) (bool, error) { asks++; return false, nil })

	for i := 0; i < 2; i++ {
		code, _, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
		if code != exitOK {
			t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
		}
	}
	if asks != 1 {
		t.Errorf("asked %d times, want once", asks)
	}
	if got := unlockSettings(t, dir); got != vault.DefaultSettings() {
		t.Errorf("Settings() = %+v, want the defaults", got)
	}
	data, _ := os.ReadFile(configIn(dir))
	if strings.Contains(string(data), "idle_timeout") {
		t.Errorf("config.yaml = %q, want the fields removed", data)
	}
}

func TestVaultUnlockWithoutAnswerTerminalChangesNothing(t *testing.T) {
	dir := takeoverFixture(t)
	before, _ := os.ReadFile(configIn(dir))
	withVaultPassphrase(t, offeringPassphrase(takeoverPassphrase))
	withVaultConfirm(t, func(string) (bool, error) { return false, vault.ErrNoTerminal })

	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir)); code != exitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	after, _ := os.ReadFile(configIn(dir))
	if string(before) != string(after) {
		t.Error("config.yaml changed without an answer")
	}
	if got := unlockSettings(t, dir); got != vault.DefaultSettings() {
		t.Errorf("Settings() = %+v, want the defaults", got)
	}
}

func TestVaultUnlockWarnsWhenFieldsReappear(t *testing.T) {
	dir := takeoverFixture(t)
	withVaultPassphrase(t, offeringPassphrase(takeoverPassphrase))
	withVaultConfirm(t, func(string) (bool, error) { return false, nil })
	runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))

	f, _ := os.OpenFile(configIn(dir), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("vault:\n  idle_timeout: 99h\n")
	_ = f.Close()
	withVaultConfirm(t, func(string) (bool, error) { return false, errors.New("must not ask") })
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stderr, "no effect") || strings.Contains(stderr, "must not ask") {
		t.Fatalf("exit %d, stderr = %q, want a no-effect warning and no question", code, stderr)
	}
}
