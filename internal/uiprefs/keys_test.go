package uiprefs

import (
	"os"
	"strings"
	"testing"
)

func TestCheckRejectsWithReason(t *testing.T) {
	d := DefaultKeys()
	cases := []struct {
		action, key, reason string
	}{
		{ActionInfo, "f2", "already the key of save"},
		{ActionInfo, "i", "already the key of info"},
		{ActionSave, "?", "already the key of help"},
		{ActionSave, "x", "acts on text lines"},
		{ActionLock, "!", "acts on text lines"},
		{ActionHelp, "ctrl+c", "quits"},
		{ActionHelp, "esc", "goes back"},
		{ActionHelp, "tab", "move between fields"},
		{ActionHelp, "shift+tab", "move between fields"},
		{ActionInfo, "3", "digits"},
		{ActionInfo, "f5", "terminals take"},
		{ActionInfo, "f21", "F1 to F20"},
		{ActionInfo, "ctrl+a", "text lines, the terminal, or a screen"},
		{ActionLock, "ctrl+r", "text lines, the terminal, or a screen"},
		{ActionSave, "ctrl+y", "text lines, the terminal, or a screen"},
		{ActionSave, "ctrl+g", "free are ctrl+l and x"},
		{ActionSave, "ctrl+o", "free are ctrl+l and x"},
		{ActionSave, "ctrl+b", "free are ctrl+l and x"},
		{ActionInfo, "j", "acts on text lines"},
		{ActionInfo, "!", "acts on text lines"},
		{ActionInfo, ".", "acts on text lines"},
		{ActionHelp, "j", "letters"},
		{ActionInfo, "enter", "not a single key"},
		{ActionSave, "", "needs a key"},
	}
	for _, c := range cases {
		_, err := d.Check(c.action, c.key)
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("Check(%s, %q) = %v, want an error with %q", c.action, c.key, err, c.reason)
		}
	}
}

func TestCheckAccepts(t *testing.T) {
	d := DefaultKeys()
	for _, c := range [][2]string{{ActionInfo, "F3"}, {ActionInfo, ""}, {ActionSave, "f4"}, {ActionHelp, "ctrl+x"},
		{ActionCommand, "ctrl+X"}, {ActionSave, "f13"}} {
		if _, err := d.Check(c[0], c[1]); err != nil {
			t.Errorf("Check(%s, %q) = %v, want it accepted", c[0], c[1], err)
		}
	}
	if got, _ := d.Check(ActionInfo, " F3 "); got != "f3" {
		t.Errorf("normalized = %q, want f3", got)
	}
}

func TestKeysSectionRoundTripWritesOnlyChanges(t *testing.T) {
	path := Path(t.TempDir())
	want := Default()
	want.Keys.Info, want.Keys.Save = "f3", "f4"
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "keys:") || !strings.Contains(string(data), "info: f3") || strings.Contains(string(data), "help:") {
		t.Fatalf("file:\n%s", data)
	}
	got, warnings := Load(path)
	if got != want || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want %+v", got, warnings, want)
	}
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "keys") {
		t.Fatalf("default keys were written:\n%s", data)
	}
}

func TestInvalidKeysFallBackWithWarning(t *testing.T) {
	path := Path(t.TempDir())
	data := "keys:\n  info: f5\n  save: f3\n  help: f3\n  lock: x\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	want := DefaultKeys() // save and help collide on f3, so both fall back
	if got.Keys != want {
		t.Fatalf("keys = %+v, want %+v", got.Keys, want)
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings = %v, want one per invalid key", warnings)
	}
	for _, w := range []string{"keys.info", "keys.help", "keys.save", "keys.lock"} {
		found := false
		for _, got := range warnings {
			found = found || strings.Contains(got, w)
		}
		if !found {
			t.Errorf("no warning for %s in %v", w, warnings)
		}
	}
}

func TestSaveRefusesInvalidKeys(t *testing.T) {
	p := Default()
	p.Keys.Help = "ctrl+c"
	if err := Save(Path(t.TempDir()), p); err == nil {
		t.Fatal("Save accepted ctrl+c")
	}
}

func TestFileSwapIsValidAsAWhole(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("keys:\n  save: ctrl+l\n  lock: ctrl+x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Keys.Save != "ctrl+l" || got.Keys.Lock != "ctrl+x" || len(warnings) != 0 {
		t.Fatalf("keys = %+v, warnings %v; want the swap kept", got.Keys, warnings)
	}
}

// ctrl+y and ctrl+r were free keys once, so a file can still hold them: they are the target picker's now, and the
// action keeps its standard with a warning that names the reason, as for any other key that breaks a rule.
func TestFileWithPickerKeysFallsBackToTheStandard(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("keys:\n  lock: ctrl+r\n  save: ctrl+y\n  help: ctrl+x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Keys.Lock != DefaultKeys().Lock || got.Keys.Save != DefaultKeys().Save || got.Keys.Help != "ctrl+x" {
		t.Fatalf("keys = %+v, want the standard for lock and save and the valid help key kept", got.Keys)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "keys.save") || !strings.Contains(warnings[1], "keys.lock") {
		t.Fatalf("warnings = %v, want one each for lock and save", warnings)
	}
	if err := got.Keys.Validate(); err != nil {
		t.Fatalf("the result is invalid: %v", err)
	}
}

func TestFileCollisionFallsBackConflictFree(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("keys:\n  save: f3\n  info: f3\n  lock: f2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if len(warnings) < 2 {
		t.Fatalf("warnings = %v, want the collisions named", warnings)
	}
	if err := got.Keys.Validate(); err != nil {
		t.Fatalf("the result still collides: %+v: %v", got.Keys, err)
	}
	if got.Keys.Save != "f2" && got.Keys.Save != "f3" {
		t.Fatalf("keys = %+v", got.Keys)
	}
}

// ctrl+g and ctrl+o were free keys once: an old file that holds them keeps the standard and warns.
func TestFileWithMultiplexerKeysFallsBackToTheStandard(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("keys:\n  help: ctrl+g\n  command: ctrl+o\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Keys.Help != DefaultKeys().Help || got.Keys.Command != DefaultKeys().Command || len(warnings) != 2 {
		t.Fatalf("keys = %+v, warnings %v; want the standard and two warnings", got.Keys, warnings)
	}
}
