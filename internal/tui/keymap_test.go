package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// rebind replaces the key of an action for one test.
func rebind(t *testing.T, a action, key string) {
	t.Helper()
	old := keymap[a]
	keymap[a] = key
	t.Cleanup(func() { keymap[a] = old })
}

func TestDefaultKeymapKeepsTheKnownKeys(t *testing.T) {
	want := map[action]string{
		actInfo: "", actSave: "f2", actHelp: "?", actCommand: ":", actLock: "ctrl+l", actBack: "esc", actDelete: "d",
	}
	if len(keymap) != len(want) {
		t.Fatalf("keymap has %d actions, want %d", len(keymap), len(want))
	}
	for a, key := range want {
		if keymap[a] != key {
			t.Errorf("action %q is bound to %q, want %q", a, keymap[a], key)
		}
	}
	if keyIs(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")}, actInfo) || keyIs(tea.KeyMsg{}, actInfo) {
		t.Error("the unbound info action matched a key")
	}
	if keyLabel(actSave) != "F2" || keyLabel(actHelp) != "?" {
		t.Errorf("labels = %q, %q", keyLabel(actSave), keyLabel(actHelp))
	}
}

func TestRebindingChangesFooterAndDispatchTogether(t *testing.T) {
	rebind(t, actHelp, "ctrl+g")
	rebind(t, actCommand, "ctrl+k")
	rebind(t, actSave, "f5")
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	view := m.View()
	if !strings.Contains(view, "ctrl+k cmds · ctrl+g help") || strings.Contains(view, "? help") || strings.Contains(view, ": cmds") {
		t.Fatalf("the footer does not follow the keymap:\n%s", view)
	}
	press(t, m, "?")
	if m.screen == screenHelp {
		t.Fatal("the old help key still opens the help")
	}
	press(t, m, ":")
	if m.screen == screenCommand {
		t.Fatal("the old command key still opens the command line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.screen != screenCommand {
		t.Fatalf("the new command key left screen %v", m.screen)
	}
	press(t, m, "esc")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.screen != screenHelp {
		t.Fatalf("the new help key left screen %v", m.screen)
	}
	if !strings.Contains(m.View(), "esc/ctrl+g close") {
		t.Errorf("the help footer does not follow the keymap:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.screen == screenHelp {
		t.Fatal("the new help key does not close the help")
	}

	press(t, m, "5", "n")
	if view := m.View(); !strings.Contains(view, "F5 save") || strings.Contains(view, "F2 save") {
		t.Fatalf("the form footer does not follow the save key:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF2})
	if m.fail != "" || m.status != "" {
		t.Fatalf("the old save key still saves: fail %q status %q", m.fail, m.status)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyF5})
	if m.fail == "" && m.status == "" {
		t.Fatal("the new save key does nothing")
	}
}

// Every key a footer names for a global action comes from the keymap: no screen file spells one as a
// literal in a footer text.
func TestFooterTextsNameGlobalKeysOnlyByPlaceholder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"? help", ": cmds", "F2 save", "F2 next", "F2 write", "F2 or enter", "esc back", "esc cancel", "esc close", "esc leave", "n/esc"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "keymap.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, `"`) {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) && strings.Contains(line, " · ") {
					t.Errorf("%s:%d spells %q in a footer text; use the key placeholder", f, i+1, b)
				}
			}
		}
	}
}
