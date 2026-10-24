package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/i18n"
)

// Every masked passphrase dialog stays usable at the smallest supported size, with the vault header on its
// own row.
func TestPassphraseDialogsFitMinimumSize(t *testing.T) {
	noop := func() tea.Cmd { return nil }
	cases := map[string]func(m *Model){
		"first entry": func(m *Model) {
			m.openVaultOffer("Set a passphrase for the vault?", vaultFirstSecretHint(), true, true, nil, nil)
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
			eachLanguage(t, func(t *testing.T) {
				m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
				open(m)
				sizedModel(m, minimumWidth, minimumHeight)
				view := m.View()
				if strings.Contains(view, "Resize terminal") || strings.Contains(view, "Terminal vergrößern") {
					t.Fatalf("resize hint at %dx%d:\n%s", minimumWidth, minimumHeight, view)
				}
				assertViewFits(t, view, minimumWidth, minimumHeight)
				esc := "esc cancel"
				if uiLang == i18n.German {
					esc = "esc abbrechen"
				}
				if !strings.Contains(view, esc) {
					t.Errorf("view lacks the esc hint %q:\n%s", esc, view)
				}
			})
		})
	}
}

// The masked passphrase dialogs of the export and the import stay usable at the smallest supported size.
func TestTransferPassphraseDialogsFitMinimumSize(t *testing.T) {
	cases := map[string]func(m *Model){
		"export first entry": func(m *Model) {
			m.openExport()
			m.export.step, m.export.input = exportPassphrase, pathField{Model: maskedInput()}
		},
		"export confirmation": func(m *Model) {
			m.openExport()
			m.export.step, m.export.input = exportPassphrase, pathField{Model: maskedInput()}
			m.export.confirming = true
		},
		"import": func(m *Model) {
			m.openImport()
			m.askImportPassphrase()
		},
	}
	for name, open := range cases {
		t.Run(name, func(t *testing.T) {
			eachLanguage(t, func(t *testing.T) {
				m, _, _ := newModel(t)
				open(m)
				sizedModel(m, minimumWidth, minimumHeight)
				view := m.View()
				if strings.Contains(view, "Resize terminal") || strings.Contains(view, "Terminal vergrößern") {
					t.Fatalf("resize hint at %dx%d:\n%s", minimumWidth, minimumHeight, view)
				}
				assertViewFits(t, view, minimumWidth, minimumHeight)
				back := "esc back"
				if uiLang == i18n.German {
					back = "esc zurück"
				}
				if !strings.Contains(view, back) {
					t.Errorf("view lacks the esc hint %q:\n%s", back, view)
				}
			})
		})
	}
}
