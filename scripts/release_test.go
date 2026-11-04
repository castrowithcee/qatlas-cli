package scripts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// readArchive returns the regular files of a release archive keyed by their cleaned path.
func readArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	if strings.HasSuffix(path, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[filepath.ToSlash(filepath.Clean(f.Name))] = body
		}
		return files
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(filepath.Clean(h.Name))] = body
	}
}

func fileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestReleaseScriptBuildsBothAssetSets runs scripts/release.sh for real (five cross builds) and checks the
// qconnect archives, the archives under the former name, checksums.txt and both signature namespaces.
func TestReleaseScriptBuildsBothAssetSets(t *testing.T) {
	if testing.Short() {
		t.Skip("builds five release targets")
	}
	for _, tool := range []string{"sh", "go", "tar", "zip", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	const tag = "v9.9.9-test.1"
	dist := filepath.Join(t.TempDir(), "dist")
	cmd := exec.Command("sh", "scripts/release.sh", tag, dist)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH=1700000000")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release.sh failed: %v\n%s", err, out)
	}

	targets := [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}}
	listed := map[string]bool{}
	for _, target := range targets {
		goos, goarch := target[0], target[1]
		ext, bin, legacyBin := ".tar.gz", "qconnect", "qatlas"
		if goos == "windows" {
			ext, bin, legacyBin = ".zip", "qconnect.exe", "qatlas.exe"
		}
		newName := "qconnect_" + tag + "_" + goos + "_" + goarch + ext
		oldName := "qatlas_" + tag + "_" + goos + "_" + goarch + ext
		listed[newName], listed[oldName] = true, true

		current := readArchive(t, filepath.Join(dist, newName))
		legacy := readArchive(t, filepath.Join(dist, oldName))
		wantLegacy := []string{"bin/" + legacyBin, "share/doc/qatlas/LICENSE"}
		wantCurrent := []string{"bin/" + bin, "share/doc/qconnect/LICENSE"}
		if goos != "windows" {
			wantLegacy = append(wantLegacy, "share/man/man1/qatlas.1")
			wantCurrent = append(wantCurrent, "share/man/man1/qconnect.1")
		}
		sort.Strings(wantLegacy)
		// The qconnect archive carries every generated page; the legacy one only the page the former updater reads.
		for _, want := range wantCurrent {
			if _, ok := current[want]; !ok {
				t.Errorf("%s lacks %s", newName, want)
			}
		}
		for name := range current {
			if !strings.HasPrefix(name, "share/man/man1/qconnect") && name != "bin/"+bin && name != "share/doc/qconnect/LICENSE" {
				t.Errorf("%s contains unexpected %s", newName, name)
			}
		}
		if got := fileNames(legacy); strings.Join(got, ",") != strings.Join(wantLegacy, ",") {
			t.Errorf("%s contains %v, want %v", oldName, got, wantLegacy)
		}
		if len(current["bin/"+bin]) == 0 || !bytes.Equal(current["bin/"+bin], legacy["bin/"+legacyBin]) {
			t.Errorf("%s and %s do not carry the same program", newName, oldName)
		}
		if goos != "windows" && !bytes.Equal(current["share/man/man1/qconnect.1"], legacy["share/man/man1/qatlas.1"]) {
			t.Errorf("%s: the legacy man page differs from qconnect.1", oldName)
		}
	}
	for _, installer := range []string{"install.sh", "install.ps1"} {
		listed[installer] = true
	}

	// checksums.txt lists exactly both archive sets and the installers, with correct digests.
	sums := readFileString(t, filepath.Join(dist, "checksums.txt"))
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed checksums line %q", line)
		}
		body, err := os.ReadFile(filepath.Join(dist, strings.TrimPrefix(fields[1], "*")))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != fields[0] {
			t.Errorf("wrong digest for %s", fields[1])
		}
		seen[strings.TrimPrefix(fields[1], "*")] = true
	}
	for name := range listed {
		if !seen[name] {
			t.Errorf("checksums.txt does not list %s", name)
		}
	}
	if len(seen) != len(listed) {
		t.Errorf("checksums.txt lists %d files, want %d", len(seen), len(listed))
	}

	// Both signatures verify under their own namespace and are rejected under the other one.
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub := readFileString(t, key+".pub")
	signers := filepath.Join(keyDir, "allowed-signers")
	if err := os.WriteFile(signers, []byte("qatlas-release "+pub), 0o600); err != nil {
		t.Fatal(err)
	}
	checksums := filepath.Join(keyDir, "checksums.txt")
	if err := os.WriteFile(checksums, []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{connectNamespace, legacyNamespace} {
		if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", ns, checksums).CombinedOutput(); err != nil {
			t.Fatalf("sign %s: %v\n%s", ns, err, out)
		}
		if err := os.Rename(checksums+".sig", filepath.Join(keyDir, ns+".sig")); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(sig, ns string) error {
		c := exec.Command("ssh-keygen", "-Y", "verify", "-f", signers, "-I", "qatlas-release", "-n", ns, "-s", filepath.Join(keyDir, sig+".sig"))
		c.Stdin = strings.NewReader(sums)
		return c.Run()
	}
	for _, tc := range []struct {
		sig, ns string
		ok      bool
	}{
		{connectNamespace, connectNamespace, true},
		{legacyNamespace, legacyNamespace, true},
		{connectNamespace, legacyNamespace, false},
		{legacyNamespace, connectNamespace, false},
	} {
		if err := verify(tc.sig, tc.ns); (err == nil) != tc.ok {
			t.Errorf("signature %s verified under %s: err = %v, want ok = %v", tc.sig, tc.ns, err, tc.ok)
		}
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	return readFile(t, path)
}
