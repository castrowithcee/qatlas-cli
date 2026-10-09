package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
)

// openPathList moves to the paths row of the form and opens its list.
func openPathList(t *testing.T, m *Model) {
	t.Helper()
	focusField(t, m, pathsLabel)
	press(t, m, "enter")
	if m.screen != screenPaths {
		t.Fatalf("enter on the paths row opened screen %v, want the path list", m.screen)
	}
}

// addPath adds one path in the open path list, the way a person does.
func addPath(t *testing.T, m *Model, value string) {
	t.Helper()
	press(t, m, "a")
	typeText(t, m, value)
	press(t, m, "enter")
}

// Saving a connection whose paths were not touched keeps them: a save must never widen where a connection
// applies.
func TestSavingAConnectionKeepsItsPaths(t *testing.T) {
	reg := wikiRegistry(t)
	dir := t.TempDir()
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"wiki": {Service: "wiki", Credential: "reader", Paths: []string{dir}},
	})
	openEntryForm(t, m, sectionConnections, "wiki")
	if got := m.field(pathsLabel).entries; !reflect.DeepEqual(got, []string{dir}) {
		t.Fatalf("the paths row holds %v, want %v", got, []string{dir})
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedConnection(t, path, reg, "wiki").Paths; !reflect.DeepEqual(got, []string{dir}) {
		t.Fatalf("paths after saving = %v, want %v", got, []string{dir})
	}
}

// A connection gets several paths one by one; each is edited and removed on its own, the rules of the core
// refuse a path as it is taken, the row folds in the form, and no paths are written as none at all.
func TestThePathListIsEditedEntryByEntry(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, nil)
	root := t.TempDir()
	wideEnoughFor(m, root)
	first, second, third := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "gh")
	if view := screenOf(m); !strings.Contains(view, "(none: every project)") {
		t.Fatalf("the empty row does not say what it means:\n%s", view)
	}
	openPathList(t, m)
	if view := screenOf(m); !strings.Contains(view, "Paths of gh") ||
		!strings.Contains(view, "applies in every project") {
		t.Fatalf("the empty list does not say what it means:\n%s", view)
	}
	addPath(t, m, first)
	addPath(t, m, "~/repos")
	if m.fail != "" {
		t.Fatalf("adding reported %q", m.fail)
	}

	// The same directory again, written with a trailing separator, stays typed with the reason.
	addPath(t, m, first+string(filepath.Separator))
	if !strings.Contains(m.fail, "already in the list") || m.pathEdit < 0 {
		t.Fatalf("a duplicate was taken: error %q, editing %d", m.fail, m.pathEdit)
	}
	press(t, m, "esc", "d")
	for _, invalid := range []string{"relative/dir", filepath.Join(root, "*")} {
		addPath(t, m, invalid)
		if !strings.Contains(m.fail, "must name a directory") || m.pathEdit < 0 {
			t.Fatalf("%q was taken: error %q, list %v", invalid, m.fail, m.pathList.all)
		}
		press(t, m, "esc", "d")
	}
	press(t, m, "esc")
	if view := screenOf(m); !strings.Contains(view, "the path list changed") {
		t.Fatalf("closing a changed list does not ask:\n%s", view)
	}
	press(t, m, "f2")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("saving from the question left screen %v, error %q", m.screen, m.fail)
	}
	openConnection(t, m, "gh")
	focusField(t, m, pathsLabel)
	if got := m.field(pathsLabel).entries; !reflect.DeepEqual(got, []string{first, "~/repos"}) {
		t.Fatalf("paths = %v", got)
	}

	// Folded, the row says how many there are and names the first two; unfolded, it lists every one.
	if view := screenOf(m); !strings.Contains(view, "2 paths:") {
		t.Fatalf("the folded row lacks its short form:\n%s", view)
	}
	press(t, m, "right")
	if view := screenOf(m); !strings.Contains(view, "2 paths\n") || strings.Contains(view, "2 paths:") {
		t.Fatalf("the unfolded row does not list every path:\n%s", view)
	}
	press(t, m, "left")
	if view := screenOf(m); !strings.Contains(view, "2 paths:") {
		t.Fatalf("the row did not fold again:\n%s", view)
	}

	// Discarding leaves the row as it was, whatever changed in the list.
	press(t, m, "enter", "x", "y")
	addPath(t, m, third)
	press(t, m, "esc", "d")
	if got := m.field(pathsLabel).entries; !reflect.DeepEqual(got, []string{first, "~/repos"}) {
		t.Fatalf("discarding changed the paths to %v", got)
	}

	// Edit the second, remove the first after asking, and save from the list.
	press(t, m, "enter", "down", "e")
	clearField(t, m)
	typeText(t, m, second)
	press(t, m, "enter", "up", "x")
	if view := screenOf(m); !strings.Contains(view, "Remove \""+first+"\"") {
		t.Fatalf("remove does not ask first:\n%s", view)
	}
	press(t, m, "n")
	if len(m.pathList.all) != 2 {
		t.Fatalf("n removed a path: %v", m.pathList.all)
	}
	press(t, m, "d", "y")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh"); !reflect.DeepEqual(saved.Paths, []string{second}) {
		t.Fatalf("saved %v, want %v", saved.Paths, []string{second})
	}

	// An emptied list is written as no paths at all, which is what makes the connection apply everywhere.
	openConnection(t, m, "gh")
	openPathList(t, m)
	press(t, m, "x", "y")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving no paths failed: %s", m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh"); saved.Paths != nil {
		t.Fatalf("saved %v, want no paths", saved.Paths)
	}
	if file := savedFile(t, path); strings.Contains(file, "paths") {
		t.Fatalf("a connection without paths still names them:\n%s", file)
	}
}

// A changed path list is unsaved input like every other row: esc asks, and ctrl+c asks the same.
func TestAChangedPathListAsksBeforeLeaving(t *testing.T) {
	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openConnection(t, m, "wiki")
	openPathList(t, m)
	addPath(t, m, t.TempDir())
	press(t, m, "esc")
	if m.screen != screenLeave || !strings.Contains(screenOf(m), "path list changed") {
		t.Fatalf("esc left a changed list without asking: screen %v\n%s", m.screen, screenOf(m))
	}
	press(t, m, "esc", "ctrl+c")
	if m.screen != screenLeave || !m.leaveQuit || m.quitting {
		t.Fatalf("ctrl+c did not ask: screen %v", m.screen)
	}
	press(t, m, "d")
	if !m.quitting {
		t.Fatal("d after ctrl+c did not quit")
	}
}

// A path that names no directory yet is taken and saved: it is only resolved when a call runs, and config
// validate warns about it. F2 takes the path still being typed before it saves.
func TestAPathThatDoesNotExistIsSaved(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	missing := filepath.Join(t.TempDir(), "not", "there")
	openConnection(t, m, "wiki")
	openPathList(t, m)
	press(t, m, "a")
	typeText(t, m, missing)
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedConnection(t, path, reg, "wiki").Paths; !reflect.DeepEqual(got, []string{missing}) {
		t.Fatalf("saved %v, want %v", got, []string{missing})
	}
}

// tab completes a path to the directories under what is typed, never to a file or a hidden directory, and
// up/down switch between several. With nothing typed, the directory the TUI started in is the suggestion.
func TestTabCompletesDirectoriesOnly(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	root := t.TempDir()
	for _, dir := range []string{"alpha", "alpine", ".alps"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "alfa.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	m.startDir = root
	wideEnoughFor(m, root)
	openConnection(t, m, "wiki")
	openPathList(t, m)
	press(t, m, "a")
	if view := screenOf(m); !strings.Contains(view, root+sep) || !strings.Contains(view, "tab complete") {
		t.Fatalf("the start directory is not suggested:\n%s", view)
	}
	press(t, m, "tab")
	if got := m.pathInput.Value(); got != root+sep {
		t.Fatalf("tab on nothing typed gave %q, want the start directory", got)
	}
	typeText(t, m, "al")
	view := screenOf(m)
	if !strings.Contains(view, root+sep+"alpha"+sep) || !strings.Contains(view, root+sep+"alpine"+sep) {
		t.Fatalf("the directories are not suggested:\n%s", view)
	}
	if strings.Contains(view, "alfa.txt") || strings.Contains(view, ".alps") {
		t.Fatalf("a file or a hidden directory is suggested:\n%s", view)
	}
	press(t, m, "down", "tab")
	if got := m.pathInput.Value(); got != root+sep+"alpine"+sep {
		t.Fatalf("down and tab gave %q, want the second directory", got)
	}
	press(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("taking the completed path reported %q", m.fail)
	}
	pump(t, m, "f2")
	want := []string{filepath.Join(root, "alpine")}
	if got := savedConnection(t, path, reg, "wiki").Paths; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved %v, want the completed directory without its trailing separator", got)
	}
}

// wideEnoughFor widens the terminal so a path under dir fits on one line: temporary directories are long on
// some systems, and the views wrap or cut what does not fit, which is not what these tests look at.
func wideEnoughFor(m *Model, dir string) {
	m.Update(tea.WindowSizeMsg{Width: max(100, 2*len(dir)+80), Height: 60})
}
