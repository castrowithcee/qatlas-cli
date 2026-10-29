package manage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// takeoverFixture is an unlocked encrypted vault with a configuration file beside it that holds the three
// old fields.
func takeoverFixture(t *testing.T, fields bool) (*Service, *vault.Vault, string) {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(dir)
	if err := v.Set("c", "token", "synthetic", func(string) (string, error) { return adminPassphrase, nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	path := filepath.Join(dir, "config.yaml")
	store := config.NewStore(path)
	cfg := store.New()
	if fields {
		cfg.Vault.IdleTimeout, cfg.Vault.AdminTimeout, cfg.Logs.RetentionDays = "2h", "0", 30
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	return ForVault(store, v, connlog.SurfaceCLI, nil), v, path
}

func configFields(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.NewStore(path).Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	return cfg
}

func backupCount(t *testing.T, path string) int {
	t.Helper()
	b, _ := config.NewStore(path).Backups()
	return len(b)
}

func TestTakeoverYes(t *testing.T) {
	svc, v, path := takeoverFixture(t, true)
	take, warn, err := svc.PrepareSettingsTakeover()
	if err != nil || warn != "" || take == nil || !take.Ask() {
		t.Fatalf("PrepareSettingsTakeover() = %v, %q, %v, want a question", take, warn, err)
	}
	want := []TakeoverValue{{fieldIdleTimeout, "2h"}, {fieldAdminTimeout, "0s"}, {fieldRetentionDays, "30"}}
	if len(take.Offered) != 3 || take.Offered[0] != want[0] || take.Offered[1] != want[1] || take.Offered[2] != want[2] {
		t.Fatalf("Offered = %v, want %v", take.Offered, want)
	}
	before := backupCount(t, path)
	res, err := take.Apply(true)
	if err != nil || res.Warning != "" || res.Backup == "" {
		t.Fatalf("Apply(true) = %+v, %v, want a backup", res, err)
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("the backup %s is missing: %v", res.Backup, err)
	}
	if backupCount(t, path) != before+1 {
		t.Errorf("backups = %d, want one more than %d", backupCount(t, path), before)
	}
	got, err := v.Settings()
	if err != nil || got.IdleTimeout != 2*time.Hour || got.AdminTimeout != 0 || got.RetentionDays != 30 {
		t.Errorf("Settings() = %+v, %v, want the config values", got, err)
	}
	if cfg := configFields(t, path); len(presentFields(cfg)) != 0 {
		t.Errorf("the config still holds %v", presentFields(cfg))
	}
	if take, warn, err := svc.PrepareSettingsTakeover(); take != nil || warn != "" || err != nil {
		t.Errorf("second Prepare = %v, %q, %v, want nothing", take, warn, err)
	}
}

func TestTakeoverNoKeepsSettings(t *testing.T) {
	svc, v, path := takeoverFixture(t, true)
	take, _, _ := svc.PrepareSettingsTakeover()
	res, err := take.Apply(false)
	if err != nil || res.Backup == "" {
		t.Fatalf("Apply(false) = %+v, %v", res, err)
	}
	got, err := v.Settings()
	if err != nil || got != vault.DefaultSettings() {
		t.Errorf("Settings() = %+v, %v, want the defaults", got, err)
	}
	if current, err := v.SettingsCurrent(); err != nil || !current {
		t.Errorf("SettingsCurrent() = %v, %v, want schema 2 written", current, err)
	}
	if len(presentFields(configFields(t, path))) != 0 {
		t.Error("the fields are still in the config")
	}
}

func TestTakeoverKeepsOtherSettingsAndOverridesOnlyPresent(t *testing.T) {
	svc, v, path := takeoverFixture(t, false)
	cfg := configFields(t, path)
	cfg.Logs.RetentionDays = 7
	if err := svc.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	take, _, err := svc.PrepareSettingsTakeover()
	if err != nil || take == nil || len(take.Offered) != 1 || take.Offered[0].Field != fieldRetentionDays {
		t.Fatalf("Prepare = %v, %v, want only the retention", take, err)
	}
	if _, err := take.Apply(true); err != nil {
		t.Fatal(err)
	}
	got, _ := v.Settings()
	if got.RetentionDays != 7 || got.IdleTimeout != vault.DefaultIdleTimeout || got.AdminTimeout != vault.DefaultAdminTimeout {
		t.Errorf("Settings() = %+v, want only the retention changed", got)
	}
}

func TestNoQuestionWithoutFields(t *testing.T) {
	svc, v, _ := takeoverFixture(t, false)
	if take, warn, err := svc.PrepareSettingsTakeover(); take != nil || warn != "" || err != nil {
		t.Errorf("Prepare = %v, %q, %v, want nothing", take, warn, err)
	}
	if current, _ := v.SettingsCurrent(); current {
		t.Error("settings.age was written without a question")
	}
}

func TestNoQuestionAfterTakeoverWarnsOnReappearedFields(t *testing.T) {
	svc, v, path := takeoverFixture(t, true)
	take, _, _ := svc.PrepareSettingsTakeover()
	if _, err := take.Apply(false); err != nil {
		t.Fatal(err)
	}
	cfg := configFields(t, path)
	cfg.Vault.IdleTimeout = "99h"
	if err := svc.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	before := backupCount(t, path)
	take, warn, err := svc.PrepareSettingsTakeover()
	if take != nil || err != nil || !strings.Contains(warn, "no effect") || !strings.Contains(warn, "vault.idle_timeout") ||
		!strings.Contains(warn, "vault settings") {
		t.Fatalf("Prepare = %v, %q, %v, want a warning only", take, warn, err)
	}
	if got, _ := v.Settings(); got.IdleTimeout != vault.DefaultIdleTimeout {
		t.Errorf("IdleTimeout = %v, the config value took effect", got.IdleTimeout)
	}
	if backupCount(t, path) != before || configFields(t, path).Vault.IdleTimeout != "99h" {
		t.Error("the config was changed")
	}
}

func TestUntrustedSettingsNoTakeover(t *testing.T) {
	svc, v, path := takeoverFixture(t, true)
	if err := os.WriteFile(filepath.Join(v.Dir(), "settings.age"), []byte("not age"), 0o600); err != nil {
		t.Fatal(err)
	}
	if take, warn, err := svc.PrepareSettingsTakeover(); take != nil || warn != "" || err != nil {
		t.Errorf("Prepare = %v, %q, %v, want nothing", take, warn, err)
	}
	if len(presentFields(configFields(t, path))) != 3 {
		t.Error("the config was changed")
	}
}

func TestUnencryptedAndLockedNoTakeover(t *testing.T) {
	dir := t.TempDir()
	plain := vault.New(dir)
	if err := plain.Set("c", "token", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(filepath.Join(dir, "config.yaml"))
	cfg := store.New()
	cfg.Vault.IdleTimeout = "2h"
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if take, warn, err := ForVault(store, plain, connlog.SurfaceCLI, nil).PrepareSettingsTakeover(); take != nil || warn != "" || err != nil {
		t.Errorf("unencrypted: Prepare = %v, %q, %v, want nothing", take, warn, err)
	}

	svc, v, _ := takeoverFixture(t, true)
	locked := ForVault(svc.store, vault.New(v.Dir()), connlog.SurfaceCLI, nil)
	if take, _, _ := locked.PrepareSettingsTakeover(); take != nil {
		t.Error("a locked vault was offered a takeover")
	}
}

// A value the vault cannot store is not offered and still removed; with nothing to offer nothing is asked.
func TestInvalidValueNotOffered(t *testing.T) {
	svc, v, path := takeoverFixture(t, false)
	take := &SettingsTakeover{svc: svc, v: v, current: vault.DefaultSettings(), next: vault.DefaultSettings()}
	cfg := svc.store.New()
	cfg.Vault.IdleTimeout, cfg.Vault.AdminTimeout, cfg.Logs.RetentionDays = "-1h", "-5m", -3
	take.offer(cfg)
	if take.Ask() {
		t.Errorf("Offered = %v, want none", take.Offered)
	}
	cfg = configFields(t, path)
	cfg.Vault.IdleTimeout = "3h"
	if err := svc.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := take.Apply(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := v.Settings(); got != vault.DefaultSettings() {
		t.Errorf("Settings() = %+v, want the defaults", got)
	}
	if len(presentFields(configFields(t, path))) != 0 {
		t.Error("the field was not removed")
	}
}

// A config that cannot be saved leaves settings.age as written and warns.
func TestTakeoverConfigFailureWarns(t *testing.T) {
	svc, v, path := takeoverFixture(t, true)
	take, _, _ := svc.PrepareSettingsTakeover()
	if err := os.WriteFile(path, []byte("version: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := take.Apply(true)
	if err != nil || res.Warning == "" || res.Backup != "" {
		t.Fatalf("Apply(true) = %+v, %v, want a warning", res, err)
	}
	if got, _ := v.Settings(); got.IdleTimeout != 2*time.Hour {
		t.Errorf("IdleTimeout = %v, want the vault write kept", got.IdleTimeout)
	}
}
