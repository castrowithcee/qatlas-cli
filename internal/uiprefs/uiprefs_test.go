package uiprefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileGivesDefaultsWithoutWarning(t *testing.T) {
	got, warnings := Load(Path(t.TempDir()))
	if got != Default() || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want defaults and no warning", got, warnings)
	}
}

func TestLoadDamagedFileGivesDefaultsAndWarning(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("theme: [unclosed\n\t:"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got != Default() || len(warnings) != 1 || !strings.Contains(warnings[0], "cannot parse") {
		t.Fatalf("Load() = %+v, %v; want defaults and one parse warning", got, warnings)
	}
}

func TestLoadUnreadablePathGivesDefaultsAndWarning(t *testing.T) {
	// A directory in place of the file cannot be read.
	path := Path(t.TempDir())
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got != Default() || len(warnings) != 1 {
		t.Fatalf("Load() = %+v, %v; want defaults and one warning", got, warnings)
	}
}

func TestLoadInvalidValuesFallBackPerField(t *testing.T) {
	path := Path(t.TempDir())
	data := "theme: purple\nbackground: neon\nstart: connections\nicons: emoji\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	want := Default()
	want.Start = "connections"
	if got != want || len(warnings) != 3 {
		t.Fatalf("Load() = %+v, %v; want %+v and two warnings", got, warnings, want)
	}
}

func TestLoadToleratesUnknownKeys(t *testing.T) {
	path := Path(t.TempDir())
	data := "theme: dark\nmouse: true\nfont: x\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Theme != ThemeDark || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want theme dark and no warning", got, warnings)
	}
}

func TestSaveRoundTripLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	want := Prefs{Theme: ThemeLight, Background: BackgroundQatlas, Start: "access", Icons: IconsNerdFont, Language: LanguageGerman, Keys: DefaultKeys()}
	if err := Save(Path(dir), want); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(Path(dir))
	if got != want || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want %+v", got, warnings, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Fatalf("directory holds %v; want only %s", entries, FileName)
	}
}

func TestSaveReplacesWholeFileAndLastWriterWins(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("theme: dark\nstart: library\nicons: nerdfont\nextra: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Prefs{Theme: ThemeLight, Start: "overview", Icons: IconsNone}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "theme: light\nbackground: terminal\nstart: overview\nicons: none\nlanguage: english\nmouse: true\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestSaveInvalidWritesNothing(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []Prefs{
		{Theme: "x", Start: "overview", Icons: IconsNone},
		{Theme: ThemeAuto, Start: "", Icons: IconsNone},
		{Theme: ThemeAuto, Start: "overview", Icons: "x"},
		{Theme: ThemeAuto, Background: "x", Start: "overview", Icons: IconsNone},
		{Theme: ThemeAuto, Start: "overview", Icons: IconsNone, Language: "fr"},
	} {
		if err := Save(Path(dir), bad); err == nil {
			t.Errorf("Save(%+v) = nil; want an error", bad)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("directory holds %v; want it empty", entries)
	}
}

func TestSaveFailedRenameLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory in place of the file makes the rename fail.
	target := Path(dir)
	if err := os.MkdirAll(filepath.Join(target, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(target, Default()); err == nil {
		t.Fatal("Save() = nil; want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Fatalf("directory holds %v; want no temporary file", entries)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	path := Path(filepath.Join(t.TempDir(), "new", "dir"))
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestLanguageLoadsAndFallsBack(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("language: system\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, warnings := Load(path); got.Language != LanguageSystem || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want language system", got, warnings)
	}
	if err := os.WriteFile(path, []byte("language: de\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Language != LanguageEnglish || len(warnings) != 1 {
		t.Fatalf("Load() = %+v, %v; want english and one warning", got, warnings)
	}
	if Default().Language != LanguageEnglish {
		t.Fatalf("default language = %q, want english", Default().Language)
	}
}

func TestMouseIsOnByDefaultAndSurvivesARoundTrip(t *testing.T) {
	if !Default().MouseEnabled() || !(Prefs{}).MouseEnabled() {
		t.Fatal("the mouse is not on in the defaults")
	}
	path := Path(t.TempDir())
	off := Default()
	off.MouseOff = true
	if err := Save(path, off); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "mouse: false\n") {
		t.Fatalf("file = %q, want mouse: false", data)
	}
	got, warnings := Load(path)
	if len(warnings) != 0 || got.MouseEnabled() {
		t.Fatalf("Load() = %+v, %v; want the mouse off and no warning", got, warnings)
	}
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	if got, warnings := Load(path); len(warnings) != 0 || !got.MouseEnabled() {
		t.Fatalf("Load() = %+v, %v; want the mouse on and no warning", got, warnings)
	}
}

func TestLoadWarnsForAMouseValueThatIsNoBoolean(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("mouse: sometimes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if len(warnings) != 1 || !got.MouseEnabled() {
		t.Fatalf("Load() = %+v, %v; want the mouse on and one warning", got, warnings)
	}
}

func TestBackgroundLoadsAndFallsBack(t *testing.T) {
	path := Path(t.TempDir())
	if got, _ := Load(path); got.Background != BackgroundTerminal {
		t.Fatalf("default background = %q, want terminal", got.Background)
	}
	if err := os.WriteFile(path, []byte("background: qatlas\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, warnings := Load(path); got.Background != BackgroundQatlas || len(warnings) != 0 {
		t.Fatalf("Load() = %+v, %v; want background qatlas", got, warnings)
	}
	if err := os.WriteFile(path, []byte("background: neon\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warnings := Load(path)
	if got.Background != BackgroundTerminal || len(warnings) != 1 ||
		!strings.Contains(warnings[0], `background "neon" is not terminal or qatlas`) {
		t.Fatalf("Load() = %+v, %v; want the default and one warning", got, warnings)
	}
}
