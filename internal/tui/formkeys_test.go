package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// enter on a text row goes on to the next row and never saves; on the last row it does nothing.
func TestEnterOnATextRowGoesOnAndNeverSaves(t *testing.T) {
	m, _, path := newModel(t)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki")
	first := m.focus
	press(t, m, "enter")
	if m.focus == first {
		t.Fatalf("enter on a text row kept the focus on row %d", first)
	}
	if m.screen != screenForm || m.status != "" || m.fail != "" {
		t.Fatalf("enter left screen %v status %q fail %q, want the form untouched", m.screen, m.status, m.fail)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("enter wrote the configuration: %v", err)
	}
	last := len(m.fields) - 1
	m.focus = last
	m.applyFocus()
	press(t, m, "enter")
	if m.focus != last || m.screen != screenForm {
		t.Errorf("enter on the last row: focus %d screen %v, want it to stay on row %d", m.focus, m.screen, last)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("enter on the last row wrote the configuration: %v", err)
	}
}

// F2 saves from a text row, a choice row, and an action row alike.
func TestF2SavesFromTextAndChoiceRows(t *testing.T) {
	for _, row := range []string{"name", providerLabel} {
		m, _, path := newModel(t)
		openSectionByName(t, m, sectionServices)
		pressNew(t, m)
		typeText(t, m, "wiki")
		press(t, m, "tab")
		press(t, m, "tab")
		typeText(t, m, "https://wiki.example.invalid")
		focusField(t, m, row)
		pump(t, m, "f2")
		if m.fail != "" || !strings.HasPrefix(m.status, "Saved") {
			t.Errorf("F2 on the %s row: fail %q status %q, want it saved", row, m.fail, m.status)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("F2 on the %s row wrote no configuration: %v", row, err)
		}
	}
}

func TestF2SavesFromAnActionRow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	mustNoError(t, newTestStore(t, filepath.Join(dir, "config.yaml")).Save(newTestConfig(t)))
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", true)
	openVaultSection(t, m)
	for i, f := range m.fields {
		if f.kind == fieldVaultAction {
			m.focus = i
			m.applyFocus()
			break
		}
	}
	if m.fields[m.focus].kind != fieldVaultAction {
		t.Fatal("the vault form has no action row")
	}
	press(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Errorf("F2 on an action row = screen %v, want the save to ask for the admin session", m.screen)
	}
}

// The guided setup saves its summary with F2; enter does nothing there.
func TestSetupSummarySavesWithF2NotEnter(t *testing.T) {
	m, _, path, _, _ := newStoreModel(t)
	walkSetup(t, m, stepSummary)
	if !strings.Contains(screenOf(m), "F2 save") || strings.Contains(screenOf(m), "enter") {
		t.Errorf("the summary does not name F2 as its save key:\n%s", screenOf(m))
	}
	press(t, m, "enter")
	if m.wizard == nil || m.wizard.saved != "" || m.wizard.saving {
		t.Fatalf("enter on the summary started the save")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("enter on the summary wrote the configuration: %v", err)
	}
	pump(t, m, "f2")
	if m.wizard == nil || m.wizard.saved == "" {
		t.Fatalf("F2 did not save the setup: fail %q", m.fail)
	}
	view := screenOf(m)
	if !strings.Contains(view, "t test connection") || !strings.Contains(view, "esc connections list") {
		t.Errorf("the saved summary lacks its keys:\n%s", view)
	}
	for _, key := range []string{"enter", "q"} {
		press(t, m, key)
		if m.wizard == nil {
			t.Errorf("%s closed the saved summary", key)
		}
	}
	press(t, m, "esc")
	if m.wizard != nil || m.section != sectionConnections {
		t.Errorf("esc after saving: wizard %v section %v, want the Connections list", m.wizard != nil, m.section)
	}
}

// enter on a secret role row opens the masked prompt and s no longer does; enter in the prompt stores it.
func TestEnterOnASecretRowOpensThePrompt(t *testing.T) {
	m, _, _, _, mem := newStoreModel(t)
	addKeyringCredential(t, m, "reader")
	editEntry(t, m, "reader")
	focusRole(t, m, "token-id")
	press(t, m, "s")
	if m.screen != screenForm {
		t.Fatalf("s on a secret row opened screen %v, want it free", m.screen)
	}
	if view := screenOf(m); !strings.Contains(view, "enter store in") || strings.Contains(view, "s store") {
		t.Errorf("the key line does not name enter for the secret row:\n%s", view)
	}
	press(t, m, "enter")
	if m.screen != screenSecret {
		t.Fatalf("enter on a secret row opened screen %v, want the masked prompt", m.screen)
	}
	typeText(t, m, "canary-form-keys-91c")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("storing reported %q", m.fail)
	}
	if got, err := mem.Get(context.Background(), secret.StoreKey("reader", "token-id")); err != nil ||
		got != "canary-form-keys-91c" {
		t.Errorf("the secret was not stored: %q, %v", got, err)
	}
}
