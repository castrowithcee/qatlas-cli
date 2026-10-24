package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
)

// inModal reports whether text stands on a row of the modal's frame.
func inModal(view, text string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, text) && (strings.Contains(line, "║") || strings.Contains(line, "╔")) {
			return true
		}
	}
	return false
}

// The admin passphrase is asked in a modal: masked, a wrong one keeps the dialog open with the error inside
// it and an empty buffer, the right one carries on with the action, and esc drops what was typed.
func TestAdminPassphraseModal(t *testing.T) {
	const wrong, right = "wrong-canary-9x", "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, right, true)
	sizedModel(m, 100, 30)

	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki-primary")
	press(t, m, "tab", "tab")
	typeText(t, m, "https://wiki.example.invalid")
	press(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Fatalf("screen = %v, want the admin dialog", m.screen)
	}
	typeText(t, m, wrong)
	view := m.View()
	if !inModal(view, "Admin passphrase needed to save") || !inModal(view, strings.Repeat("*", len(wrong))) {
		t.Fatalf("the dialog is no modal with a masked line:\n%s", view)
	}
	if strings.Contains(view, wrong) {
		t.Fatalf("the modal shows the typed passphrase:\n%s", view)
	}
	pump(t, m, "enter")
	if m.adminAuth == nil || m.adminAuth.input.Value() != "" {
		t.Fatal("a wrong passphrase left the dialog closed or the buffer filled")
	}
	if view := m.View(); !inModal(view, "error: wrong passphrase") || strings.Contains(view, wrong) {
		t.Fatalf("the error is not in the modal:\n%s", view)
	}
	if loaded, err := store.Load(); err == nil && len(loaded.Services) != 0 {
		t.Fatalf("a wrong passphrase saved: %+v", loaded.Services)
	}

	// esc drops a typed entry and the action.
	typeText(t, m, wrong)
	auth := m.adminAuth
	press(t, m, "esc")
	if m.adminAuth != nil || auth.input.Value() != "" {
		t.Fatalf("esc kept the typed passphrase: %q", auth.input.Value())
	}
	if m.screen == screenAdminAuth {
		t.Fatal("esc left the dialog open")
	}
	if loaded, err := store.Load(); err == nil && len(loaded.Services) != 0 {
		t.Fatalf("a cancelled dialog saved: %+v", loaded.Services)
	}

	// The action carries on after the right passphrase.
	press(t, m, "f2")
	typeText(t, m, right)
	pump(t, m, "enter")
	loaded, err := store.Load()
	if err != nil || loaded.Services["wiki-primary"].BaseURL != "https://wiki.example.invalid" {
		t.Fatalf("the action did not continue: %v %+v", err, loaded.Services)
	}
}

// The unlock dialog of 'ctrl+l' is a modal as well and keeps its error inside.
func TestVaultUnlockModal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", false)
	sizedModel(m, 100, 30)
	openSectionByName(t, m, sectionServices)

	press(t, m, "ctrl+l")
	if m.screen != screenVaultUnlock {
		t.Fatalf("screen = %v, want the unlock dialog", m.screen)
	}
	typeText(t, m, "nope-canary")
	view := m.View()
	if !inModal(view, "Unlock vault") || strings.Contains(view, "nope-canary") {
		t.Fatalf("the unlock dialog is no masked modal:\n%s", view)
	}
	pump(t, m, "enter")
	if view := m.View(); !inModal(view, "error: wrong passphrase") || m.vaultUnlock.input.Value() != "" {
		t.Fatalf("the wrong passphrase is not answered in the modal:\n%s", view)
	}
}

// The secret, and the passphrase offer that can follow it, are asked in modals over the form; the value is
// masked and gone once the dialog ends.
func TestSecretAndOfferModals(t *testing.T) {
	const canary = "canary-masked-77aa"
	m, _, _, _, _ := newStoreModel(t)
	sizedModel(m, 100, 30)
	addKeyringCredential(t, m, "reader")
	editEntry(t, m, "reader")
	focusRole(t, m, "token-id")
	press(t, m, "enter")
	typeText(t, m, canary)

	view := m.View()
	if !inModal(view, "Secret for reader.token-id") || strings.Contains(view, canary) ||
		!inModal(view, strings.Repeat("*", len(canary))) {
		t.Fatalf("the secret prompt is no masked modal:\n%s", view)
	}
	press(t, m, "esc")
	if m.secretInput.Value() != "" {
		t.Fatal("esc kept the typed secret")
	}

	m.openVaultOfferFor("Set a passphrase for the vault?", vaultFirstSecretHint(), true, true,
		func(func(string) (string, error)) tea.Cmd { return nil }, func() tea.Cmd { return nil })
	typeText(t, m, canary)
	view = m.View()
	if !inModal(view, "Set a passphrase for the vault?") || strings.Contains(view, canary) {
		t.Fatalf("the offer is no masked modal:\n%s", view)
	}
	press(t, m, "enter")
	if view := m.View(); !inModal(view, "Confirm the vault passphrase") || m.vaultOffer.input.Value() != "" {
		t.Fatalf("the confirmation step is not in the modal or kept the entry:\n%s", view)
	}
}

// Every dialog fits the smallest terminal as a full screen, and fits the smallest terminal that carries a
// modal as one.
func TestPassphraseDialogsFitBothSizes(t *testing.T) {
	for _, size := range []struct{ w, h int }{{minimumWidth, minimumHeight}, {modalMinWidth, modalMinHeight}} {
		m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", false)
		m.openAdminPrompt(true, m.screen, func() tea.Cmd { return nil })
		typeText(t, m, strings.Repeat("x", 90))
		sizedModel(m, size.w, size.h)
		m.fail = "wrong passphrase"
		view := m.View()
		assertViewFits(t, view, size.w, size.h)
		if strings.Contains(view, "xxx") {
			t.Errorf("%dx%d shows the typed text:\n%s", size.w, size.h, view)
		}
		if !strings.Contains(view, "wrong passphrase") || !strings.Contains(view, "esc cancel") {
			t.Errorf("%dx%d lacks the error or the esc hint:\n%s", size.w, size.h, view)
		}
		if modal := strings.Contains(view, "╔"); modal != (size.w >= modalMinWidth && size.h >= modalMinHeight) {
			t.Errorf("%dx%d modal = %v:\n%s", size.w, size.h, modal, view)
		}
	}
}

// The result of a connection test stands in a modal until it is closed, and is the only place it shows.
func TestConnectionTestResultModal(t *testing.T) {
	m := newTestableModel(t, func(_ context.Context, _ string) (provider.Class, error) {
		return provider.ClassAuth, nil
	}, nil)
	sizedModel(m, 100, 30)
	runTest(t, m)

	view := m.View()
	if !inModal(view, "Connection test") || !inModal(view, "[failed] wiki: auth") {
		t.Fatalf("the result is no modal:\n%s", view)
	}
	if strings.Count(view, "[failed] wiki") != 1 {
		t.Errorf("the result shows more than once:\n%s", view)
	}
	press(t, m, "x")
	if m.testClass == "" {
		t.Error("an unrelated key closed the result")
	}
	press(t, m, "enter")
	if m.testClass != "" || strings.Contains(m.View(), "[failed]") {
		t.Errorf("enter did not close the result:\n%s", m.View())
	}
}

// A path is typed into a modal with its suggestions, and the keys of the path field work in it.
func TestPathModalCompletes(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, _ := toolsModel(t, reg, nil)
	sizedModel(m, 100, 30)
	root := t.TempDir()
	treeOf(t, root, "alpha/")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "gh")
	openPathList(t, m)
	press(t, m, "a")
	typeText(t, m, filepath.Join(root, "al"))

	view := m.View()
	if !inModal(view, "Add path") || !inModal(view, "alpha") || !inModal(view, "tab complete") {
		t.Fatalf("the path prompt is no modal with suggestions:\n%s", view)
	}
	press(t, m, "tab")
	if got := m.pathInput.Value(); !strings.HasSuffix(got, "alpha"+string(filepath.Separator)) {
		t.Fatalf("tab completed to %q", got)
	}
	press(t, m, "enter")
	if m.pathEdit >= 0 || len(m.pathList.all) != 1 {
		t.Fatalf("enter did not take the path: edit %d list %v fail %q", m.pathEdit, m.pathList.all, m.fail)
	}
}

// The one-line steps of the export and the import are modals; the screens around them stay screens.
func TestTransferDialogsAreModals(t *testing.T) {
	v, _ := vaultExportModel(t)
	m := v.m
	sizedModel(m, 100, 30)
	v.startExport(testExportVaultPass)
	v.advance(exportPassphrase, exportEncrypted, "")
	v.type_(testExportPass)
	view := m.View()
	if !inModal(view, "Export passphrase") || strings.Contains(view, testExportPass) ||
		!strings.Contains(view, "Export: secrets") {
		t.Fatalf("the export passphrase is no masked modal over the question:\n%s", view)
	}
	v.pump("enter")
	if view := m.View(); !inModal(view, "Confirm the export passphrase") {
		t.Fatalf("the confirmation is not in the modal:\n%s", view)
	}
	v.type_("different")
	v.pump("enter")
	if view := m.View(); !inModal(view, "error: the two passphrases did not match") || m.export.input.Value() != "" {
		t.Fatalf("the mismatch is not answered in the modal:\n%s", view)
	}
	v.assertHidden(testExportPass)

	p, _ := plainExportModel(t)
	sizedModel(p.m, 100, 30)
	p.startExport("")
	p.advance(exportTarget, exportWithout, "")
	if view := p.m.View(); !inModal(view, "Export: file") || !inModal(view, "esc back") {
		t.Fatalf("the export file prompt is no modal:\n%s", view)
	}

	i := plainImportTarget(t)
	sizedModel(i.m, 100, 30)
	i.startImport("")
	if view := i.m.View(); !inModal(view, "Import: file") {
		t.Fatalf("the import file prompt is no modal:\n%s", view)
	}
}
