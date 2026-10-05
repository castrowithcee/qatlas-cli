package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAnInvalidHandoffIsIgnored(t *testing.T) {
	valid := `{"v":1,"from":"v0.4.0","to":"v0.5.0","unlocked":true}`
	if decodeHandoff(valid) == nil {
		t.Fatal("the valid handoff was refused")
	}
	for name, value := range map[string]string{
		"not json":       "v0.4.0",
		"unknown":        `{"v":2,"from":"a","to":"b"}`,
		"no version":     `{"from":"a","to":"b"}`,
		"no from":        `{"v":1,"to":"b"}`,
		"no to":          `{"v":1,"from":"a"}`,
		"control":        `{"v":1,"from":"a\u001b[2J","to":"b"}`,
		"long field":     `{"v":1,"from":"` + strings.Repeat("a", maxHandoffField+1) + `","to":"b"}`,
		"long handoff":   `{"v":1,"from":"a","to":"b","x":"` + strings.Repeat("a", maxHandoffBytes) + `"}`,
		"wrong type":     `{"v":1,"from":1,"to":"b"}`,
		"unlocked as no": `{"v":1,"from":"a","to":"b","unlocked":"yes"}`,
	} {
		if h := decodeHandoff(value); h != nil {
			t.Errorf("%s: %+v was accepted", name, h)
		}
	}

	t.Setenv(handoffEnv, `{"v":1,"from":"a","to":"b"}`)
	if takeHandoff() == nil {
		t.Error("the handoff in the environment was not read")
	}
	if _, ok := os.LookupEnv(handoffEnv); ok {
		t.Error("the handoff stayed in the environment")
	}
	t.Setenv(handoffEnv, "garbage")
	if takeHandoff() != nil {
		t.Error("garbage was accepted")
	}
	if _, ok := os.LookupEnv(handoffEnv); ok {
		t.Error("garbage stayed in the environment")
	}
}

func TestTheNewEditorOpensTheUnlockDialogOnlyForAVaultLockedByTheUpdate(t *testing.T) {
	h := &handoff{Version: 1, From: "v0.4.0", To: "v0.5.0", Unlocked: true}

	locked, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
	locked.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	locked.adopt(h)
	if locked.screen != screenVaultUnlock {
		t.Fatalf("screen = %v, want the unlock dialog", locked.screen)
	}
	if view := screenOf(locked); !strings.Contains(view, "Unlock vault") ||
		!strings.Contains(view, "updated from v0.4.0 to v0.5.0") {
		t.Errorf("the dialog does not name the update:\n%s", view)
	}
	press(t, locked, "esc")
	if locked.screen != screenNav {
		t.Errorf("esc left screen %v", locked.screen)
	}
	if state, _ := locked.secrets.Vault().State(); state != "locked" {
		t.Errorf("esc left the vault %v, want locked", state)
	}

	for name, build := range map[string]func() (*Model, *handoff){
		"unlocked before, still unlocked": func() (*Model, *handoff) {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
			return m, h
		},
		"locked before": func() (*Model, *handoff) {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			return m, &handoff{Version: 1, From: "v0.4.0", To: "v0.5.0"}
		},
		"unencrypted": func() (*Model, *handoff) { return unencryptedVaultModel(t), h },
		"no vault": func() (*Model, *handoff) {
			m, _, _ := newModel(t)
			return m, h
		},
	} {
		m, handoff := build()
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
		m.adopt(handoff)
		if m.screen != screenNav {
			t.Errorf("%s: screen %v, want no dialog", name, m.screen)
		}
		if !strings.Contains(m.View(), "qatlas updated from v0.4.0 to v0.5.0") {
			t.Errorf("%s: the update is not reported:\n%s", name, m.View())
		}
	}
}
