//go:build linux || darwin

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFiles creates the files, by path relative to dir, with their bodies.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists: %v", path, err)
	}
}

// manNames lists the entries of dir.
func manNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertManAlias(t *testing.T, dir string) {
	t.Helper()
	if target, err := os.Readlink(filepath.Join(dir, "qatlas.1")); err != nil || target != "qconnect.1" {
		t.Fatalf("qatlas.1 links to %q (%v), want qconnect.1", target, err)
	}
}

// qatlas.1 becomes qconnect.1 with qatlas.1 as a link to it; the pages qatlas-*.1 go, other files stay.
func TestAdoptManpagesMovesTheMainPageAndDropsSubpages(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"qatlas.1": "main", "qatlas-vault-status.1": "a", "qatlas-x.1": "b",
		"other.1": "foreign", "qatlas-notes.txt": "foreign", "qconnect-keep.1": "ours",
	})
	if err := os.Symlink("elsewhere", filepath.Join(dir, "qatlas-link.1")); err != nil {
		t.Fatal(err)
	}
	adoptManpages(dir)
	assertFile(t, filepath.Join(dir, "qconnect.1"), "main")
	assertManAlias(t, dir)
	assertFile(t, filepath.Join(dir, "qatlas.1"), "main")
	for _, gone := range []string{"qatlas-vault-status.1", "qatlas-x.1"} {
		assertMissing(t, filepath.Join(dir, gone))
	}
	assertFile(t, filepath.Join(dir, "other.1"), "foreign")
	assertFile(t, filepath.Join(dir, "qatlas-notes.txt"), "foreign")
	assertFile(t, filepath.Join(dir, "qconnect-keep.1"), "ours")
	if _, err := os.Readlink(filepath.Join(dir, "qatlas-link.1")); err != nil {
		t.Errorf("a link qatlas-*.1 was removed: %v", err)
	}
	// Once done, a second run changes nothing.
	adoptManpages(dir)
	assertManAlias(t, dir)
	assertFile(t, filepath.Join(dir, "qconnect.1"), "main")
}

// Without qatlas.1 there is no main page to take over; the subpages still go.
func TestAdoptManpagesWithoutAMainPage(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"qatlas-x.1": "a", "other.1": "foreign"})
	adoptManpages(dir)
	assertMissing(t, filepath.Join(dir, "qatlas-x.1"))
	assertMissing(t, filepath.Join(dir, "qconnect.1"))
	assertFile(t, filepath.Join(dir, "other.1"), "foreign")
	adoptManpages(filepath.Join(dir, "missing"))
}

// A qconnect.1 that is there already is never replaced, and qatlas.1 stays as it is.
func TestAdoptManpagesKeepsAnExistingQconnectPage(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"qatlas.1": "former", "qconnect.1": "current", "qatlas-x.1": "a"})
	adoptManpages(dir)
	assertFile(t, filepath.Join(dir, "qconnect.1"), "current")
	assertFile(t, filepath.Join(dir, "qatlas.1"), "former")
	if info, _ := os.Lstat(filepath.Join(dir, "qatlas.1")); !info.Mode().IsRegular() {
		t.Error("qatlas.1 is no regular file any more")
	}
	assertMissing(t, filepath.Join(dir, "qatlas-x.1"))
}

// A qatlas.1 that is a link already is left alone.
func TestAdoptManpagesLeavesALinkedFormerPage(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/elsewhere/qatlas.1", filepath.Join(dir, "qatlas.1")); err != nil {
		t.Fatal(err)
	}
	adoptManpages(dir)
	if target, _ := os.Readlink(filepath.Join(dir, "qatlas.1")); target != "/elsewhere/qatlas.1" {
		t.Errorf("qatlas.1 links to %q", target)
	}
	assertMissing(t, filepath.Join(dir, "qconnect.1"))
}

// archiveOf packs files, by archive path, into a release archive with the program.
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	files["bin/qconnect"] = "new-binary"
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func updateWith(t *testing.T, executable string, archive []byte) {
	t.Helper()
	server := releaseServer(t, "v3.1.0", archive, "")
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v3.0.0", SigningKeys: testKeys(t),
		GOOS: "linux", GOARCH: "amd64", Executable: executable}
	if result, err := client.Update(context.Background()); err != nil || !result.Updated {
		t.Fatalf("Update() = %+v, %v", result, err)
	}
}

// An update installs every qconnect*.1 of the archive, replaces older ones, removes a subpage the archive
// lacks, and leaves every other file, the alias qatlas.1 included, as it is.
func TestUpdateInstallsAllManpagesAndDropsGoneOnes(t *testing.T) {
	prefix, executable := installedPrefix(t)
	manDir := filepath.Join(prefix, "share", "man", "man1")
	writeFiles(t, manDir, map[string]string{
		"qconnect-a.1": "old-a", "qconnect-gone.1": "old-gone", "other.1": "foreign", "qconnect-notes.txt": "foreign",
		"qatlas-x.1": "foreign",
	})
	if err := os.Symlink("qconnect.1", filepath.Join(manDir, "qatlas.1")); err != nil {
		t.Fatal(err)
	}
	updateWith(t, executable, archiveOf(t, map[string]string{
		"share/man/man1/qconnect.1":       "new-main",
		"share/man/man1/qconnect-a.1":     "new-a",
		"share/man/man1/qconnect-b-c.1":   "new-b",
		"share/man/man1/qconnect-Bad.1":   "rejected name",
		"share/man/man1/sub/qconnect-d.1": "rejected path",
		"share/man/man1/qconnect-../x.1":  "rejected path",
		"share/man/man1/other.1":          "not ours",
		"share/doc/qconnect/qconnect-e.1": "not a manpage",
	}))
	assertFile(t, filepath.Join(manDir, "qconnect.1"), "new-main")
	assertFile(t, filepath.Join(manDir, "qconnect-a.1"), "new-a")
	assertFile(t, filepath.Join(manDir, "qconnect-b-c.1"), "new-b")
	assertFile(t, filepath.Join(manDir, "other.1"), "foreign")
	assertFile(t, filepath.Join(manDir, "qatlas-x.1"), "foreign")
	assertFile(t, filepath.Join(manDir, "qconnect-notes.txt"), "foreign")
	assertManAlias(t, manDir)
	for _, absent := range []string{"qconnect-Bad.1", "sub"} {
		assertMissing(t, filepath.Join(manDir, absent))
	}
	// An entry was rejected, so the archive cannot be taken as the full set: no installed page is removed.
	assertFile(t, filepath.Join(manDir, "qconnect-gone.1"), "old-gone")
}

// With every entry fitting the limits, a page the archive lacks goes.
func TestUpdateRemovesAGoneSubpage(t *testing.T) {
	prefix, executable := installedPrefix(t)
	manDir := filepath.Join(prefix, "share", "man", "man1")
	writeFiles(t, manDir, map[string]string{"qconnect-gone.1": "old", "qconnect-a.1": "old-a", "other.1": "foreign"})
	updateWith(t, executable, archiveOf(t, map[string]string{
		"share/man/man1/qconnect.1": "new-main", "share/man/man1/qconnect-a.1": "new-a",
	}))
	assertFile(t, filepath.Join(manDir, "qconnect-a.1"), "new-a")
	assertMissing(t, filepath.Join(manDir, "qconnect-gone.1"))
	assertFile(t, filepath.Join(manDir, "other.1"), "foreign")
	for _, name := range manNames(t, manDir) {
		if len(name) > 0 && name[0] == '.' {
			t.Errorf("staging file %s remains", name)
		}
	}
}

// An archive without subpages removes none; one with more pages than the limit, or an oversized page,
// installs the rest and removes none.
func TestUpdateKeepsPagesWhenTheArchiveCannotBeTrusted(t *testing.T) {
	over := map[string]string{"share/man/man1/qconnect.1": "new-main"}
	for i := range maxSubpages + 1 {
		over[fmt.Sprintf("share/man/man1/qconnect-p%d.1", i)] = "p"
	}
	big := map[string]string{
		"share/man/man1/qconnect.1": "new-main", "share/man/man1/qconnect-a.1": "new-a",
		"share/man/man1/qconnect-big.1": string(make([]byte, maxManpageSize+1)),
	}
	for name, files := range map[string]map[string]string{
		"none": {"share/man/man1/qconnect.1": "new-main"}, "too many": over, "oversized": big,
	} {
		t.Run(name, func(t *testing.T) {
			prefix, executable := installedPrefix(t)
			manDir := filepath.Join(prefix, "share", "man", "man1")
			writeFiles(t, manDir, map[string]string{"qconnect-old.1": "old"})
			updateWith(t, executable, archiveOf(t, files))
			assertFile(t, filepath.Join(manDir, "qconnect.1"), "new-main")
			assertFile(t, filepath.Join(manDir, "qconnect-old.1"), "old")
			if name == "too many" {
				if n := len(manNames(t, manDir)); n != maxSubpages+2 {
					t.Errorf("%d pages installed, want the limit of %d plus the main page and the old one", n, maxSubpages)
				}
			}
		})
	}
}

// A subpage that cannot be installed leaves the update done: the program and main page are replaced.
func TestUpdateSurvivesAnUninstallableSubpage(t *testing.T) {
	prefix, executable := installedPrefix(t)
	manDir := filepath.Join(prefix, "share", "man", "man1")
	// A directory of that name makes the rename of the new page fail.
	if err := os.MkdirAll(filepath.Join(manDir, "qconnect-a.1", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, manDir, map[string]string{"qconnect-gone.1": "old"})
	updateWith(t, executable, archiveOf(t, map[string]string{
		"share/man/man1/qconnect.1": "new-main", "share/man/man1/qconnect-a.1": "new-a", "share/man/man1/qconnect-b.1": "new-b",
	}))
	assertFile(t, executable, "new-binary")
	assertFile(t, filepath.Join(manDir, "qconnect.1"), "new-main")
	assertFile(t, filepath.Join(manDir, "qconnect-b.1"), "new-b")
	assertFile(t, filepath.Join(manDir, "qconnect-gone.1"), "old")
}

// The way back to a release before the rename removes qconnect.1 and every qconnect-*.1, regular files
// only, and nothing else.
func TestDropCurrentNameRemovesAllQconnectPages(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "qconnect")
	writeFiles(t, dir, map[string]string{
		"bin/qconnect":           "program",
		"man/qconnect.1":         "main",
		"man/qconnect-a.1":       "a",
		"man/qconnect-b-c.1":     "b",
		"man/qatlas.1":           "former",
		"man/qatlas-a.1":         "former sub",
		"man/other.1":            "foreign",
		"man/qconnect-notes.txt": "foreign",
	})
	if err := os.Symlink("elsewhere", filepath.Join(dir, "man", "qconnect-link.1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "man", "qconnect-dir.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	dropCurrentName("linux", bin, filepath.Join(dir, "man"))
	assertMissing(t, bin)
	for _, gone := range []string{"qconnect.1", "qconnect-a.1", "qconnect-b-c.1"} {
		assertMissing(t, filepath.Join(dir, "man", gone))
	}
	for _, kept := range []string{"qatlas.1", "qatlas-a.1", "other.1", "qconnect-notes.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, "man", kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	for _, kept := range []string{"qconnect-link.1", "qconnect-dir.1"} {
		if _, err := os.Lstat(filepath.Join(dir, "man", kept)); err != nil {
			t.Errorf("%s, no regular file, was removed: %v", kept, err)
		}
	}
}
