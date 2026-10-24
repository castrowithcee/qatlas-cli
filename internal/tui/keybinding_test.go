package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// keepKeys puts the process-wide keymap back after a test that changes it.
func keepKeys(t *testing.T) {
	t.Helper()
	saved, extra := map[action]string{}, infoExtra
	for a, k := range keymap {
		saved[a] = k
	}
	t.Cleanup(func() {
		for a, k := range saved {
			keymap[a] = k
		}
		infoExtra = extra
	})
}

// focusKeyRow focuses the row of an action in the General form.
func focusKeyRow(t *testing.T, m *Model, label string) {
	t.Helper()
	for i, f := range m.fields {
		if f.label == label {
			m.focus = i
			m.applyFocus()
			return
		}
	}
	t.Fatalf("no row %q", label)
}

// bindKey presses enter on a key row and then the key.
func bindKey(t *testing.T, m *Model, label, key string) {
	t.Helper()
	focusKeyRow(t, m, label)
	press(t, m, "enter", key)
}

func TestInfoExtraKeyWorksOnTextRowsAndFooters(t *testing.T) {
	keepKeys(t)
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	applyKeys(uiprefs.Keys{Info: "f3"})
	addService(t, m, "wiki", "https://wiki.example.invalid")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	if !m.typing() {
		t.Fatalf("the focused row is not a text row (%q)", m.fields[m.focus].label)
	}
	if view := m.View(); !strings.Contains(view, "F3 info") || strings.Contains(view, "i/F3 info") {
		t.Fatalf("a text row names only the key that works there:\n%s", view)
	}
	press(t, m, "i")
	if m.infoOpen {
		t.Fatal("the letter opened the explanation on a text row")
	}
	press(t, m, "f3")
	if !m.infoOpen {
		t.Fatal("the extra key did not open the explanation on a text row")
	}
	press(t, m, "f3")
	if m.infoOpen {
		t.Fatal("the extra key does not close the explanation")
	}
	press(t, m, "tab")
	for m.typing() {
		press(t, m, "tab")
	}
	if view := m.View(); !strings.Contains(view, "i/F3 info") {
		t.Fatalf("a choice row names both keys:\n%s", view)
	}
	press(t, m, "i")
	if !m.infoOpen {
		t.Fatal("the letter no longer works beside the extra key")
	}
}

func TestSettingsBindsKeysAndFootersFollow(t *testing.T) {
	keepKeys(t)
	m, path := lockedVaultModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	openSectionByName(t, m, sectionGeneral)
	bindKey(t, m, "info key", "f3")
	if m.fail != "" || m.fieldValue("info key") != "f3" {
		t.Fatalf("info key = %q, fail %q", m.fieldValue("info key"), m.fail)
	}
	if view := m.View(); !strings.Contains(view, "i and F3") {
		t.Fatalf("the row does not name the binding:\n%s", view)
	}
	bindKey(t, m, "save key", "f4")
	pump(t, m, "f2") // the old key saves; the new one applies from then on
	if !strings.HasPrefix(m.status, "Saved") || m.fail != "" {
		t.Fatalf("the new save key did not save: status %q fail %q", m.status, m.fail)
	}
	got, warnings := uiprefs.Load(path)
	if got.Keys.Info != "f3" || got.Keys.Save != "f4" || len(warnings) != 0 {
		t.Fatalf("stored keys = %+v %v", got.Keys, warnings)
	}
	if infoExtra != "f3" || keyLabel(actSave) != "F4" {
		t.Fatalf("keymap not applied: info extra %q, save %q", infoExtra, keyLabel(actSave))
	}
	if view := m.View(); !strings.Contains(view, "F4 save") || strings.Contains(view, "F2 save") {
		t.Fatalf("the footer does not show the new save key:\n%s", view)
	}
	focusKeyRow(t, m, "save key")
	press(t, m, "r")
	if m.fieldValue("save key") != "f2" {
		t.Fatalf("r left save at %q, want the standard", m.fieldValue("save key"))
	}
}

func TestSettingsRefusesKeysWithReason(t *testing.T) {
	keepKeys(t)
	cases := []struct{ row, key, reason string }{
		{"info key", "f2", "already the key of save"},
		{"save key", "x", "acts on text lines"},
		{"info key", "!", "acts on text lines"},
		{"help key", "tab", "move between fields"},
		{"command key", "5", "digits choose an area"},
		{"lock key", "f1", "terminals take"},
		{"lock key", "!", "acts on text lines"},
	}
	for _, c := range cases {
		m, _ := lockedVaultModel(t)
		openSectionByName(t, m, sectionGeneral)
		before := m.fieldValue(c.row)
		bindKey(t, m, c.row, c.key)
		if !strings.Contains(m.fail, c.reason) || m.fieldValue(c.row) != before || m.keyCapture {
			t.Errorf("%s <- %s: fail %q value %q capture %v, want a refusal with %q", c.row, c.key, m.fail, m.fieldValue(c.row), m.keyCapture, c.reason)
		}
	}
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	bindKey(t, m, "info key", "esc")
	if m.fieldValue("info key") != "" || m.keyCapture || m.screen != screenForm {
		t.Errorf("esc did not cancel the capture: value %q screen %v", m.fieldValue("info key"), m.screen)
	}
}

func TestSettingsCaptureShadowsGlobalKeys(t *testing.T) {
	keepKeys(t)
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	focusKeyRow(t, m, "info key")
	press(t, m, "enter", "?")
	if m.screen == screenHelp {
		t.Fatal("? opened the help while a key was captured")
	}
	if m.fail == "" {
		t.Fatal("? as the info key was not refused with a reason")
	}
}

func TestStandardKeysNeedNoExtra(t *testing.T) {
	keepKeys(t)
	applyKeys(uiprefs.DefaultKeys())
	if infoExtra != "" || keyLabel(actInfo) != "i" || keyLabel(actSave) != "F2" {
		t.Fatalf("defaults: extra %q info %q save %q", infoExtra, keyLabel(actInfo), keyLabel(actSave))
	}
}

func TestHelpListsTheKeysInForce(t *testing.T) {
	keepKeys(t)
	applyKeys(uiprefs.Keys{Info: "f3", Save: "f4"})
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	text := strings.Join(m.helpLines(), "\n")
	for _, want := range []string{"info: i/F3", "save: F4", "help: ?", "lock: ctrl+l", "F-keys"} {
		if !strings.Contains(text, want) {
			t.Errorf("the help lacks %q", want)
		}
	}
}

func TestCtrlCStillQuitsDuringCapture(t *testing.T) {
	keepKeys(t)
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	focusKeyRow(t, m, "info key")
	press(t, m, "enter")
	_, cmd := m.Update(keyMsg("ctrl+c"))
	if m.keyCapture || cmd == nil {
		t.Fatalf("ctrl+c during the capture: capture %v, quit command %v", m.keyCapture, cmd != nil)
	}
}
