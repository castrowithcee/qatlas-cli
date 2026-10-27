package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// tempHome points the home directory at a new temporary one, on every platform.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// treeOf makes the directories and files below root; a name ending in a slash is a directory.
func treeOf(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		full := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			mustNoError(t, os.MkdirAll(full, 0o700))
		} else {
			mustNoError(t, os.WriteFile(full, nil, 0o600))
		}
	}
}

func suggestionsOf(mode pathMode, typed string) []string {
	p := newPathField(mode)
	p.open(typed, "")
	return p.matches
}

func TestDirectoryModeSuggestsDirectoriesWithTheirCaseAndNoHiddenOnes(t *testing.T) {
	root := t.TempDir()
	treeOf(t, root, "alpha/", "alpine/", ".alps/", "Alto/", "alfa.txt")
	typed := withSeparator(root)
	got := suggestionsOf(pathDirectory, typed+"al")
	want := []string{typed + "alpha" + string(filepath.Separator), typed + "alpine" + string(filepath.Separator)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %v, want %v", got, want)
	}
	if got := suggestionsOf(pathDirectory, typed+".a"); len(got) != 1 || !strings.HasSuffix(got[0], ".alps"+string(filepath.Separator)) {
		t.Fatalf("after the dot = %v, want the hidden directory", got)
	}
	if got := suggestionsOf(pathDirectory, typed+"AL"); len(got) != 0 {
		t.Fatalf("a different case matched: %v", got)
	}
}

func TestFileModeSuggestsDirectoriesBeforeFilesAndAcceptsANewName(t *testing.T) {
	root := t.TempDir()
	treeOf(t, root, "b-dir/", "a-file.yaml", "b-file.yaml", ".hidden.yaml")
	typed := withSeparator(root)
	got := suggestionsOf(pathFile, typed)
	want := []string{typed + "b-dir" + string(filepath.Separator), typed + "a-file.yaml", typed + "b-file.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %v, want directories first, no hidden entry: %v", got, want)
	}
	// A file that does not exist yet is typed freely; only its directory is completed.
	p := newPathField(pathFile)
	p.open(typed+"new-export.yaml", "")
	if len(p.matches) != 0 || p.Value() != typed+"new-export.yaml" {
		t.Fatalf("a new name: matches %v value %q", p.matches, p.Value())
	}
	if !p.accept() {
		t.Fatal("a typed name is not ready to be taken")
	}
	if got := suggestionsOf(pathDirectory, typed+"a-"); len(got) != 0 {
		t.Fatalf("directory mode offered a file: %v", got)
	}
}

func TestPathFieldKeepsAMissingDirectoryTypeable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "deeper") + string(filepath.Separator)
	for _, mode := range []pathMode{pathDirectory, pathFile} {
		p := newPathField(mode)
		p.open("", "")
		for _, r := range missing + "x" {
			p.update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		if p.Value() != missing+"x" || len(p.matches) != 0 {
			t.Fatalf("mode %v: value %q matches %v", mode, p.Value(), p.matches)
		}
		p.update(tea.KeyPressMsg{Code: tea.KeyTab})
		if p.Value() != missing+"x" {
			t.Fatalf("tab changed a path with nothing to complete: %q", p.Value())
		}
	}
}

func TestHomeIsResolvedInEveryMode(t *testing.T) {
	home := tempHome(t)
	treeOf(t, home, "projects/", "notes.txt")
	s := string(filepath.Separator)
	for _, mode := range []pathMode{pathDirectory, pathFile} {
		if got := suggestionsOf(mode, "~"); !reflect.DeepEqual(got, []string{"~" + s}) {
			t.Fatalf("mode %v: ~ gives %v", mode, got)
		}
		want := []string{"~" + s + "projects" + s}
		if mode == pathFile {
			want = append(want, "~"+s+"notes.txt")
		}
		if got := suggestionsOf(mode, "~"+s); !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %v: ~/ gives %v, want %v", mode, got, want)
		}
	}
	if got := absolutePath("~" + s + "x.yaml"); got != filepath.Join(home, "x.yaml") {
		t.Fatalf("absolutePath = %q, want it in the home directory", got)
	}
}

func TestRelativeFilesAreCompletedFromTheWorkingDirectoryOnlyInFileMode(t *testing.T) {
	root := t.TempDir()
	treeOf(t, root, "sub/", "export.yaml")
	t.Chdir(root)
	if got := suggestionsOf(pathFile, "ex"); !reflect.DeepEqual(got, []string{"export.yaml"}) {
		t.Fatalf("file mode: %v", got)
	}
	if got := suggestionsOf(pathFile, "sub"+string(filepath.Separator)); len(got) != 0 {
		t.Fatalf("an empty directory gave %v", got)
	}
	if got := suggestionsOf(pathDirectory, "su"); len(got) != 0 {
		t.Fatalf("directory mode completes relative paths: %v", got)
	}
}

func TestEmptyFieldSuggestsTheStartDirectoryAndTabTakesIt(t *testing.T) {
	root := t.TempDir()
	p := newPathField(pathDirectory)
	p.open("", root)
	if !reflect.DeepEqual(p.matches, []string{withSeparator(root)}) {
		t.Fatalf("matches = %v", p.matches)
	}
	p.update(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.Value() != withSeparator(root) {
		t.Fatalf("tab gave %q", p.Value())
	}
}

func TestUpDownMarkAndEnterTakesTheMarkedSuggestionOnlyAfterTheyMovedIt(t *testing.T) {
	root := t.TempDir()
	treeOf(t, root, "one/", "two/", "three.txt")
	typed := withSeparator(root)
	s := string(filepath.Separator)
	p := newPathField(pathFile)
	p.open(typed, "")
	if !p.accept() || p.Value() != typed {
		t.Fatalf("enter without a mark changed %q", p.Value())
	}
	p.update(tea.KeyPressMsg{Code: tea.KeyDown})
	p.update(tea.KeyPressMsg{Code: tea.KeyDown})
	if shown, cur := p.shown(); shown[cur] != typed+"three.txt" {
		t.Fatalf("marked %q", shown[cur])
	}
	p.update(tea.KeyPressMsg{Code: tea.KeyDown})
	if shown, cur := p.shown(); shown[cur] != typed+"one"+s {
		t.Fatalf("the mark did not wrap: %q", shown[cur])
	}
	// A directory marked for a file is entered, not taken.
	if p.accept() {
		t.Fatal("a directory was taken as the file")
	}
	if p.Value() != typed+"one"+s {
		t.Fatalf("value %q, want the directory entered", p.Value())
	}
	p.open(typed, "")
	p.update(tea.KeyPressMsg{Code: tea.KeyUp})
	if !p.accept() || p.Value() != typed+"three.txt" {
		t.Fatalf("a marked file was not taken: %q", p.Value())
	}
	d := newPathField(pathDirectory)
	d.open(typed, "")
	d.update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !d.accept() || d.Value() != typed+"two"+s {
		t.Fatalf("a marked directory was not taken in directory mode: %q", d.Value())
	}
}

func TestPlainFieldSuggestsNothingAndIgnoresTheSuggestionKeys(t *testing.T) {
	p := pathField{Model: maskedInput()}
	for _, r := range "secret" {
		p.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p.update(tea.KeyPressMsg{Code: tea.KeyTab})
	p.update(tea.KeyPressMsg{Code: tea.KeyDown})
	if p.Value() != "secret" || len(p.matches) != 0 || !p.accept() {
		t.Fatalf("value %q matches %v", p.Value(), p.matches)
	}
}

func TestDirectoriesAreReadOncePerOpening(t *testing.T) {
	root := t.TempDir()
	treeOf(t, root, "first/")
	typed := withSeparator(root)
	p := newPathField(pathDirectory)
	p.open(typed, "")
	treeOf(t, root, "second/")
	p.set(typed + "s")
	if len(p.matches) != 0 {
		t.Fatalf("the directory was read again: %v", p.matches)
	}
	p.open(typed+"s", "")
	if len(p.matches) != 1 {
		t.Fatalf("a new opening did not read it again: %v", p.matches)
	}
}

func TestMatchesAreBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < pathMatchLimit+20; i++ {
		mustNoError(t, os.Mkdir(filepath.Join(root, "d"+strings.Repeat("x", 1)+string(rune('a'+i%26))+string(rune('a'+i/26))), 0o700))
	}
	if got := suggestionsOf(pathDirectory, withSeparator(root)); len(got) != pathMatchLimit {
		t.Fatalf("%d matches, want %d", len(got), pathMatchLimit)
	}
}

func TestShownWindowFollowsTheMark(t *testing.T) {
	p := pathField{matches: []string{"a", "b", "c", "d", "e", "f", "g"}}
	for mark, want := range map[int]string{0: "a", 4: "e", 5: "f", 6: "g"} {
		p.mark = mark
		shown, cur := p.shown()
		if len(shown) != pathSuggestionsShown || shown[cur] != want {
			t.Fatalf("mark %d: shown %v at %d", mark, shown, cur)
		}
	}
}

func TestWindowsPathsAreCompletedWithEitherSeparator(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive and UNC paths exist only on Windows")
	}
	root := t.TempDir()
	treeOf(t, root, "alpha/")
	if vol := filepath.VolumeName(root); vol == "" {
		t.Fatalf("no volume in %q", root)
	}
	for _, typed := range []string{root + `\al`, strings.ReplaceAll(root, `\`, "/") + "/al"} {
		got := suggestionsOf(pathDirectory, typed)
		if len(got) != 1 || !strings.HasSuffix(got[0], `alpha\`) {
			t.Fatalf("%q gives %v", typed, got)
		}
	}
}
