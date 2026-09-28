package projectpath

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWithinComparesWholeSegments(t *testing.T) {
	cases := []struct {
		dir, base string
		want      bool
	}{
		{"/repos/kunde-a", "/repos/kunde-a", true},
		{"/repos/kunde-a/src/app", "/repos/kunde-a", true},
		{"/repos/kunde-a/", "/repos/kunde-a", true},
		{"/repos/kunde-ab", "/repos/kunde-a", false},
		{"/repos", "/repos/kunde-a", false},
		{"/repos/kunde-a/../kunde-b", "/repos/kunde-a", false},
		{"/anything", "/", true},
		{"/Repos/Kunde-A", "/repos/kunde-a", false},
	}
	for _, c := range cases {
		if got := within(c.dir, c.base, false); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.dir, c.base, got, c.want)
		}
	}
}

func TestWithinNormalizesWindowsPaths(t *testing.T) {
	cases := []struct {
		dir, base string
		want      bool
	}{
		{`C:\Repos\Kunde-A`, `c:/repos/kunde-a`, true},
		{`c:\repos\kunde-a\src`, `C:\Repos\Kunde-A\`, true},
		{`C:\repos\kunde-ab`, `C:\repos\kunde-a`, false},
		{`D:\repos\kunde-a`, `C:\repos\kunde-a`, false},
		{`\\?\C:\Repos\Kunde-A\x`, `C:\repos\kunde-a`, true},
		{`\\Server\Share\Kunde-A`, `\\server\share`, true},
		{`\\?\UNC\server\share\kunde-a`, `\\server\share\kunde-a`, true},
		{`C:\anything`, `C:\`, true},
	}
	for _, c := range cases {
		if got := within(c.dir, c.base, true); got != c.want {
			t.Errorf("within(%q, %q, windows) = %v, want %v", c.dir, c.base, got, c.want)
		}
	}
	if got := normalize(`C:\Repos\\Kunde-A\.\src\`, true); got != "c:/repos/kunde-a/src" {
		t.Errorf("normalize = %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for entry, want := range map[string]string{
		"~":              home,
		"~/repos/kunde":  filepath.Join(home, "repos", "kunde"),
		"/abs/path":      "/abs/path",
		"~other/repos":   "~other/repos",
		"repos/relative": "repos/relative",
	} {
		got, err := Expand(entry)
		if err != nil || got != want {
			t.Errorf("Expand(%q) = %q, %v; want %q", entry, got, err, want)
		}
	}
	if IsHomeRelative("~other") || !IsHomeRelative("~/x") || !IsHomeRelative("~") {
		t.Error("IsHomeRelative accepts only ~ and ~/...")
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// repository makes dir the root of a main working tree.
func repository(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(mkdir(t, dir, ".git"), "HEAD"), "ref: refs/heads/main\n")
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRootsOutsideGitIsTheDirectoryItself(t *testing.T) {
	dir := Canonical(t.TempDir())
	// An empty .git directory is no repository, whatever lies above the temporary directory.
	mkdir(t, dir, ".git")
	if got := Roots(dir); !reflect.DeepEqual(got, []string{dir}) {
		t.Fatalf("Roots = %v, want %v", got, []string{dir})
	}
}

func TestRootsOfASubdirectoryIsTheRepositoryRoot(t *testing.T) {
	repo := Canonical(t.TempDir())
	repository(t, repo)
	sub := mkdir(t, repo, "src", "pkg")
	if got := Roots(sub); !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("Roots = %v, want %v", got, []string{repo})
	}
}

func TestRootsOfAnEmbeddedRepositoryIsItsOwnRoot(t *testing.T) {
	outer := Canonical(t.TempDir())
	repository(t, outer)
	inner := mkdir(t, outer, "vendor", "inner")
	repository(t, inner)
	sub := mkdir(t, inner, "cmd")
	if got := Roots(sub); !reflect.DeepEqual(got, []string{inner}) {
		t.Fatalf("Roots = %v, want %v", got, []string{inner})
	}
}

// worktree lays out a main repository at main and a linked worktree of it at wt, as 'git worktree add'
// writes them.
func worktree(t *testing.T, main, wt string) {
	t.Helper()
	repository(t, main)
	private := mkdir(t, main, ".git", "worktrees", "wt")
	write(t, filepath.Join(private, "commondir"), "../..\n")
	write(t, filepath.Join(private, "gitdir"), filepath.Join(wt, ".git")+"\n")
	mkdir(t, wt)
	write(t, filepath.Join(wt, ".git"), "gitdir: "+private+"\n")
}

func TestRootsOfALinkedWorktreeIncludeTheMainWorkingTree(t *testing.T) {
	base := Canonical(t.TempDir())
	main := filepath.Join(base, "repos", "kunde-a")
	wt := filepath.Join(base, "elsewhere", "wt")
	worktree(t, main, wt)
	sub := mkdir(t, wt, "src")
	if got := Roots(sub); !reflect.DeepEqual(got, []string{wt, main}) {
		t.Fatalf("Roots = %v, want %v", got, []string{wt, main})
	}
}

func TestRootsIgnoreAGitFileItsGitDirectoryDoesNotPointBackTo(t *testing.T) {
	base := Canonical(t.TempDir())
	main := filepath.Join(base, "repos", "kunde-a")
	wt := filepath.Join(base, "elsewhere", "wt")
	worktree(t, main, wt)
	stray := mkdir(t, base, "stray")
	data, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stray, ".git"), string(data))
	if got := Roots(stray); !reflect.DeepEqual(got, []string{stray}) {
		t.Fatalf("Roots = %v, want only the stray directory", got)
	}
}

func TestRootsOfASubmoduleHaveNoMainWorkingTree(t *testing.T) {
	super := Canonical(t.TempDir())
	repository(t, super)
	mkdir(t, super, ".git", "modules", "sub")
	sub := mkdir(t, super, "sub")
	write(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
	if got := Roots(sub); !reflect.DeepEqual(got, []string{sub}) {
		t.Fatalf("Roots = %v, want %v", got, []string{sub})
	}
}

func TestRootsResolveSymbolicLinks(t *testing.T) {
	base := Canonical(t.TempDir())
	repo := mkdir(t, base, "repos", "kunde-a")
	repository(t, repo)
	link := filepath.Join(base, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if got := Roots(filepath.Join(link)); !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("Roots = %v, want %v", got, []string{repo})
	}
	if got := Canonical(filepath.Join(base, "missing", "dir")); got != filepath.Join(base, "missing", "dir") {
		t.Fatalf("Canonical of a missing directory = %q", got)
	}
}
