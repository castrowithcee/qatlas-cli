package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// storedSettings reads settings.age of the fixture vault with its passphrase.
func storedSettings(t *testing.T, dir string) vault.Settings {
	t.Helper()
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	s, err := v.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 'vault settings' shows the three values, sets exactly the given ones with the passphrase into settings.age,
// keeps the rest, accepts a large value, and leaves config.yaml alone.
func TestVaultSettingsCommand(t *testing.T) {
	dir := encryptedTokenFixture(t)
	configBefore, err := os.ReadFile(configIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	run := func(args ...string) (int, string, string) {
		return runWithInput(t, &Options{}, "", append([]string{"vault", "settings"}, append(args, "--config", configIn(dir))...)...)
	}

	if code, stdout, stderr := run(); code != exitOK || stderr != "" ||
		stdout != "idle timeout: 12h\nadmin timeout: 10m\nretention days: 90\n" {
		t.Errorf("show: exit %d, stdout %q, stderr %q, want the defaults", code, stdout, stderr)
	}
	if code, stdout, _ := run("handover-is-not-a-flag"); code == exitOK {
		t.Errorf("an argument was accepted: stdout %q", stdout)
	}
	if code, stdout, stderr := run("--idle-timeout", "90m"); code != exitOK ||
		stdout != "idle timeout: 1h30m\nadmin timeout: 10m\nretention days: 90\n" {
		t.Errorf("set idle: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got := storedSettings(t, dir); got.IdleTimeout != 90*time.Minute || got.AdminTimeout != 10*time.Minute ||
		got.RetentionDays != 90 {
		t.Errorf("stored after idle = %+v", got)
	}
	if code, stdout, stderr := run("--admin-timeout", "0"); code != exitOK ||
		stdout != "idle timeout: 1h30m\nadmin timeout: 0s\nretention days: 90\n" {
		t.Errorf("set admin: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, stdout, stderr := run("--retention-days", "30", "--idle-timeout", "2h"); code != exitOK ||
		stdout != "idle timeout: 2h\nadmin timeout: 0s\nretention days: 30\n" {
		t.Errorf("set two: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	// No upper bound.
	if code, stdout, _ := run("--idle-timeout", "87600h", "--retention-days", "1000000"); code != exitOK ||
		stdout != "idle timeout: 87600h\nadmin timeout: 0s\nretention days: 1000000\n" {
		t.Errorf("large values: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := run(); code != exitOK || stdout != "idle timeout: 87600h\nadmin timeout: 0s\nretention days: 1000000\n" {
		t.Errorf("show after set: exit %d, stdout %q", code, stdout)
	}
	if after, _ := os.ReadFile(configIn(dir)); string(after) != string(configBefore) {
		t.Error("config.yaml changed")
	}
}

// A setting keeps the update behaviour that was stored.
func TestVaultSettingsCommandKeepsTheUpdateBehaviour(t *testing.T) {
	dir := encryptedTokenFixture(t)
	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "handover", "lock", "--config", configIn(dir)); code != exitOK {
		t.Fatalf("handover lock: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "settings", "--retention-days", "5", "--config", configIn(dir)); code != exitOK {
		t.Fatalf("settings: exit %d, stderr %q", code, stderr)
	}
	if got := storedSettings(t, dir); got.Update != vault.UpdateLock || got.RetentionDays != 5 {
		t.Errorf("stored = %+v, want lock and 5 days", got)
	}
}

func TestVaultSettingsCommandNeedsAdminAndEncryption(t *testing.T) {
	dir := encryptedTokenFixture(t)
	settings := filepath.Join(dir, vault.DirName, "settings.age")

	withInteractive(t, false)
	for _, args := range [][]string{{"vault", "settings"}, {"vault", "settings", "--retention-days", "5"}} {
		code, stdout, stderr := runWithInput(t, &Options{}, "", append(args, "--config", configIn(dir))...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
			t.Errorf("%v without a terminal: exit %d, stdout %q, stderr %q, want admin-required", args, code, stdout, stderr)
		}
	}

	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("wrong"))
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "settings", "--retention-days", "5", "--config", configIn(dir))
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "usage") {
		t.Errorf("wrong passphrase: exit %d, stdout %q, stderr %q, want usage", code, stdout, stderr)
	}

	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	for _, args := range [][]string{
		{"--idle-timeout", "soon"}, {"--idle-timeout", "0"}, {"--idle-timeout", "-1h"},
		{"--admin-timeout", "-1m"}, {"--retention-days", "0"}, {"--retention-days", "-3"},
	} {
		code, stdout, stderr := runWithInput(t, &Options{}, "", append([]string{"vault", "settings"}, append(args, "--config", configIn(dir))...)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "usage") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q, want usage", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("a refused call wrote settings.age: %v", err)
	}

	plain := vaultCredentialFixture(t)
	if err := vault.New(plain).Set("wiki-vault", "token-id", canaryVault, nil); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"vault", "settings"}, {"vault", "settings", "--retention-days", "5"}} {
		code, _, stderr = runWithInput(t, &Options{}, "", append(args, "--config", configIn(plain))...)
		if code != exitUsage || !strings.Contains(stderr, "run 'qatlas vault encrypt' first") {
			t.Errorf("unencrypted vault %v: exit %d, stderr %q, want the encryption named", args, code, stderr)
		}
	}
}

// An untrusted settings.age shows the safe values with a warning on stderr; replacing it needs a retention
// and then yields a trusted file that keeps the safe update behaviour.
func TestVaultSettingsCommandUntrusted(t *testing.T) {
	dir := encryptedTokenFixture(t)
	if err := os.WriteFile(filepath.Join(dir, vault.DirName, "settings.age"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	withInteractive(t, true)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	run := func(args ...string) (int, string, string) {
		return runWithInput(t, &Options{}, "", append([]string{"vault", "settings"}, append(args, "--config", configIn(dir))...)...)
	}

	code, stdout, stderr := run()
	if code != exitOK || stdout != "idle timeout: 12h\nadmin timeout: 0s\nretention days: 0\n" ||
		!strings.Contains(stderr, "warning: settings.age was not written by this vault") {
		t.Errorf("show: exit %d, stdout %q, stderr %q, want the safe values and a warning", code, stdout, stderr)
	}
	if code, stdout, stderr := run("--idle-timeout", "1h"); code != exitUsage || stdout != "" ||
		!strings.Contains(stderr, "--retention-days") {
		t.Errorf("set without retention: exit %d, stdout %q, stderr %q, want usage naming --retention-days", code, stdout, stderr)
	}
	if code, stdout, stderr := run("--retention-days", "30"); code != exitOK || stderr != "" ||
		stdout != "idle timeout: 12h\nadmin timeout: 0s\nretention days: 30\n" {
		t.Errorf("replace: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got := storedSettings(t, dir); got.Update != vault.UpdateLock || got.AdminTimeout != 0 || got.RetentionDays != 30 {
		t.Errorf("stored = %+v, want the safe values and 30 days", got)
	}
}

func TestFormatTimeout(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12 * time.Hour: "12h", 10 * time.Minute: "10m", 0: "0s", 90 * time.Minute: "1h30m",
		time.Hour + 10*time.Minute: "1h10m", 10 * time.Second: "10s", 100 * time.Hour: "100h",
		time.Minute + 30*time.Second: "1m30s", 20 * time.Minute: "20m",
	} {
		if got := vault.FormatTimeout(d); got != want {
			t.Errorf("FormatTimeout(%v) = %q, want %q", d, got, want)
		}
		if back, err := time.ParseDuration(vault.FormatTimeout(d)); err != nil || back != d {
			t.Errorf("FormatTimeout(%v) does not parse back: %v, %v", d, back, err)
		}
	}
}
