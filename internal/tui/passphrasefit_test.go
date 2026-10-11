package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Every masked passphrase dialog stays usable at the smallest supported size, with the vault header on its
// own row.
func TestPassphraseDialogsFitMinimumSize(t *testing.T) {
	noop := func() tea.Cmd { return nil }
	cases := map[string]func(m *Model){
		"first entry": func(m *Model) {
			m.openVaultOffer("Set a passphrase for the vault?", vaultFirstSecretHint, true, true, nil, nil)
		},
		"confirmation":   func(m *Model) { m.openVaultOffer("x", "y", true, true, nil, nil); m.vaultOffer.confirming = true },
		"encrypt":        func(m *Model) { m.startVaultEncrypt() },
		"new passphrase": func(m *Model) { m.startVaultNewPassphrase("old") },
		"current": func(m *Model) {
			m.startVaultCurrentPassphrase("Current vault passphrase", "typed masked", func(string) tea.Cmd { return nil })
		},
		"admin":           func(m *Model) { m.openAdminPrompt(false, m.screen, noop) },
		"admin locked":    func(m *Model) { m.openAdminPrompt(true, m.screen, noop) },
		"admin timeout 0": func(m *Model) { m.cfg.Vault.AdminTimeout = "0"; m.openAdminPrompt(false, m.screen, noop) },
	}
	for name, open := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
			open(m)
			sizedModel(m, minimumWidth, minimumHeight)
			view := m.View()
			if strings.Contains(view, "Resize terminal") {
				t.Fatalf("resize hint at %dx%d:\n%s", minimumWidth, minimumHeight, view)
			}
			assertViewFits(t, view, minimumWidth, minimumHeight)
			if !strings.Contains(view, "esc cancel") {
				t.Errorf("view lacks the esc hint:\n%s", view)
			}
		})
	}
}
