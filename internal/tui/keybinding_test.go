package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// keepKeys puts the process-wide keymap back after a test that changes it.
func keepKeys(t *testing.T) {
	t.Helper()
	saved, extra := map[action]key.Binding{}, infoExtra
	for a, b := range keymap {
		saved[a] = b
	}
	t.Cleanup(func() {
		for a, b := range saved {
			keymap[a] = b
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
	if view := m.render(); !strings.Contains(view, "F3 info") || strings.Contains(view, "i/F3 info") {
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
	if view := m.render(); !strings.Contains(view, "i/F3 info") {
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
	if view := m.render(); !strings.Contains(view, "i and F3") {
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
	if view := m.render(); !strings.Contains(view, "F4 save") || strings.Contains(view, "F2 save") {
		t.Fatalf("the footer does not show the new save key:\n%s", view)
	}
	focusKeyRow(t, m, "save key")
	press(t, m, "d")
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
		{"save key", "space", "space and control characters"},
		{"info key", "space", "space and control characters"},
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

// Every key a binding may hold reaches the editor under the name it was stored under, from the sequences
// terminals send for it: the shifted F1 to F8 of xterm-like terminals as F13 to F20, and the F1 to F5 of the
// Linux console as F1 to F5.
func TestStoredKeyNamesReachTheEditorUnchanged(t *testing.T) {
	keys := []struct{ sequence, name, action string }{
		{"\x1bOQ", "f2", uiprefs.ActionSave}, {"\x1bOR", "f3", uiprefs.ActionSave}, {"\x1bOS", "f4", uiprefs.ActionSave},
		{"\x1b[17~", "f6", uiprefs.ActionSave}, {"\x1b[18~", "f7", uiprefs.ActionSave},
		{"\x1b[19~", "f8", ""}, {"\x1b[20~", "f9", uiprefs.ActionSave},
		{"\x1b[25~", "f13", uiprefs.ActionSave}, {"\x1b[1;2P", "f13", uiprefs.ActionSave},
		{"\x1b[1;2Q", "f14", uiprefs.ActionSave}, {"\x1b[1;2R", "f15", uiprefs.ActionSave},
		{"\x1b[1;2S", "f16", uiprefs.ActionSave}, {"\x1b[15;2~", "f17", uiprefs.ActionSave},
		{"\x1b[17;2~", "f18", uiprefs.ActionSave}, {"\x1b[18;2~", "f19", uiprefs.ActionSave},
		{"\x1b[19;2~", "f20", uiprefs.ActionSave}, {"\x1b[[B", "f2", uiprefs.ActionSave}, {"\x1b[[D", "f4", uiprefs.ActionSave},
		{"\x0c", "ctrl+l", uiprefs.ActionLock},
		{"\x12", "ctrl+r", ""}, {"\x19", "ctrl+y", ""}, {"\x18", "ctrl+x", uiprefs.ActionSave},
		{"?", "?", uiprefs.ActionHelp}, {":", ":", uiprefs.ActionCommand}, {"!", "!", uiprefs.ActionHelp},
	}
	var stream strings.Builder
	var want []string
	for _, k := range keys {
		// ctrl+r and ctrl+y belong to the target picker and f8 to the payload form: they reach the editor by
		// name, but no binding holds them.
		if _, err := uiprefs.DefaultKeys().Check(k.action, k.name); err != nil && k.action != "" {
			t.Errorf("%s is no key a binding may hold: %v", k.name, err)
		}
		stream.WriteString(k.sequence)
		want = append(want, k.name)
	}
	m := sizedModel(groupModel(t), 120, 40)
	if got := runInput(t, m, stream.String()); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the terminal's keys reach the editor as\n%q, want\n%q", got, want)
	}
}

func TestKeyCaptureEndsWhenTheResizeNoticeLeavesTheForm(t *testing.T) {
	keepKeys(t)
	setup := func() (*Model, string) {
		m, _ := lockedVaultModel(t)
		sizedModel(m, 100, 40)
		openSectionByName(t, m, sectionGeneral)
		focusKeyRow(t, m, "info key")
		bound := keyOf(actInfo)
		press(t, m, "enter")
		if !m.keyCapture {
			t.Fatal("enter did not start the capture")
		}
		sizedModel(m, 40, 13)
		return m, bound
	}

	m, bound := setup()
	press(t, m, "esc")
	if m.keyCapture || m.screen == screenForm || len(m.fields) != 0 {
		t.Fatalf("after esc in the notice: capture %v, screen %v, fields %d", m.keyCapture, m.screen, len(m.fields))
	}
	sizedModel(m, 100, 40)
	press(t, m, "x")
	if keyOf(actInfo) != bound {
		t.Errorf("a key after leaving rebound the info key: %q, was %q", keyOf(actInfo), bound)
	}

	m, _ = setup()
	m.fields[m.focus].bound = "y"
	press(t, m, "esc")
	if m.keyCapture || m.screen != screenLeave {
		t.Fatalf("a changed form: capture %v, screen %v", m.keyCapture, m.screen)
	}
	sizedModel(m, 100, 40)
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Errorf("the next key did not answer the question: screen %v", m.screen)
	}
}

func TestSpaceKeepsInfoExtraKeyAndDeleteRemovesIt(t *testing.T) {
	keepKeys(t)
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	bindKey(t, m, "info key", "f3")
	bindKey(t, m, "info key", "space")
	if m.fieldValue("info key") != "f3" || !strings.Contains(m.fail, "space and control characters") {
		t.Fatalf("space: value %q fail %q, want f3 kept and a refusal", m.fieldValue("info key"), m.fail)
	}
	focusKeyRow(t, m, "info key")
	press(t, m, "d")
	if m.fieldValue("info key") != "" {
		t.Errorf("d left info key %q, want it removed", m.fieldValue("info key"))
	}
}
