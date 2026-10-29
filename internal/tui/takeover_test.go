package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// takeoverModel is a locked, encrypted vault whose config.yaml still holds the three old fields; the
// unlock with ctrl+l has been typed up to the question.
func takeoverModel(t *testing.T) *Model {
	t.Helper()
	m, store := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	cfg := store.New()
	cfg.Vault.IdleTimeout, cfg.Vault.AdminTimeout, cfg.Logs.RetentionDays = "2h", "5m", 30
	mustNoError(t, store.Save(cfg))
	pump(t, m, "ctrl+l")
	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	return m
}

func TestUnlockAsksSettingsTakeoverYes(t *testing.T) {
	m := takeoverModel(t)
	if m.screen != screenConfirm || m.settingsTakeover == nil {
		t.Fatalf("after the unlock screen = %v, takeover = %v, want the question", m.screen, m.settingsTakeover)
	}
	view := m.confirmView()
	for _, want := range []string{"vault.idle_timeout: 2h", "vault.admin_timeout: 5m", "logs.retention_days: 30"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question = %q, want it to show %q", view, want)
		}
	}
	pump(t, m, "y")
	if m.screen == screenConfirm || m.settingsTakeover != nil {
		t.Fatalf("after y screen = %v, takeover = %v, want the question closed", m.screen, m.settingsTakeover)
	}
	got, err := m.secrets.Vault().Settings()
	if err != nil || got.IdleTimeout != 2*time.Hour || got.AdminTimeout != 5*time.Minute || got.RetentionDays != 30 {
		t.Errorf("Settings() = %+v, %v, want the config values", got, err)
	}
	cfg, err := m.store.Load()
	if err != nil || cfg.Vault.IdleTimeout != "" || cfg.Logs.RetentionDays != 0 {
		t.Errorf("config = %+v, %v, want the fields removed", cfg, err)
	}
	if !strings.Contains(m.status, "backups") {
		t.Errorf("status = %q, want the backup path", m.status)
	}
}

func TestUnlockAsksSettingsTakeoverNo(t *testing.T) {
	for _, key := range []string{"n", "esc"} {
		t.Run(key, func(t *testing.T) {
			m := takeoverModel(t)
			if m.settingsTakeover == nil {
				t.Fatal("no question")
			}
			pump(t, m, key)
			if m.screen == screenConfirm || m.settingsTakeover != nil {
				t.Fatalf("screen = %v, takeover = %v, want the question closed", m.screen, m.settingsTakeover)
			}
			got, err := m.secrets.Vault().Settings()
			if err != nil || got != vault.DefaultSettings() {
				t.Errorf("Settings() = %+v, %v, want the defaults", got, err)
			}
			cfg, err := m.store.Load()
			if err != nil || cfg.Vault.IdleTimeout != "" || cfg.Vault.AdminTimeout != "" || cfg.Logs.RetentionDays != 0 {
				t.Errorf("config = %+v, %v, want the fields removed", cfg, err)
			}
			if !strings.Contains(m.status, "backups") {
				t.Errorf("status = %q, want the backup path", m.status)
			}
		})
	}
}

func TestUnlockWithoutFieldsAsksNothing(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	pump(t, m, "ctrl+l")
	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if m.settingsTakeover != nil || m.screen == screenConfirm {
		t.Errorf("screen = %v, takeover = %v, want no question", m.screen, m.settingsTakeover)
	}
}
