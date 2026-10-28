//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// TestUpdateToAnOlderVersionRestoresABackupItReads rolls real binaries back against a local signed test
// release. The configuration carries a schema version the older program does not know, so it cannot read
// it: without --restore-backup nothing changes, with it the newest backup the older program reads takes the
// configuration's place, the current file is kept as a new backup, and the installed program is the older one.
func TestUpdateToAnOlderVersionRestoresABackupItReads(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary twice")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "prefix", "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}

	signer := releasetest.NewSigner(t)
	keyLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	var archive, checksums []byte
	asset := fmt.Sprintf("qatlas_v1.0.0_linux_%s.tar.gz", runtime.GOARCH)
	var releases *httptest.Server
	releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.0.0", "assets": []map[string]string{
				{"name": asset, "url": releases.URL + "/asset"},
				{"name": "checksums.txt", "url": releases.URL + "/checksums"},
				{"name": "checksums.txt.sig", "url": releases.URL + "/signature"},
			}})
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write(checksums)
		case "/signature":
			_, _ = w.Write(releasetest.Sign(t, signer, release.Namespace, "sha512", checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(releases.Close)

	build := func(path, version string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-tags", "e2e", "-buildvcs=false", "-ldflags",
			"-X github.com/castrowithcee/qatlas-cli/internal/cli.version="+version+
				" -X 'github.com/castrowithcee/qatlas-cli/internal/release.extraKeyLines="+keyLine+"'"+
				" -X github.com/castrowithcee/qatlas-cli/internal/selfupdate.testBaseURL="+releases.URL,
			"-o", path, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("building the binary: %v", err)
		}
	}
	build(bin, "v2.0.0")
	older := filepath.Join(dir, "older")
	build(older, "v1.0.0")
	program, err := os.ReadFile(older)
	if err != nil {
		t.Fatal(err)
	}
	var tarred bytes.Buffer
	gz := gzip.NewWriter(&tarred)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name string
		body []byte
	}{{"bin/qatlas", program}, {"share/man/man1/qatlas.1", []byte(".TH QATLAS 1\n")}} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(entry.body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive = tarred.Bytes()
	sum := sha256.Sum256(archive)
	checksums = []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")

	const goodConfig = `version: 4
services:
  wiki:
    provider: bookstack
    base_url: http://127.0.0.1:1
credentials:
  vault-reader:
    type: vault
connections:
  wiki:
    service: wiki
    credential: vault-reader
defaults:
  connections:
    bookstack: wiki
`
	const currentConfig = "version: 99\n# changes made with the newer version\n"
	configPath := filepath.Join(dir, "config.yaml")
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"config-20260201T100000Z-000.yaml": goodConfig,
		"config-20260301T100000Z-000.yaml": "version: 99\n",
	} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(configPath, []byte(currentConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: []string{
		"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "QATLAS_CONFIG=" + configPath, secret.StoreSelector + "=none",
	}}
	installed := func() []byte {
		data, _ := os.ReadFile(bin)
		return data
	}
	original := installed()

	// Without the flag the rollback offers the backup and changes nothing.
	code, _, stderr := c.run(t, "update", "--to", "v1.0.0", "--yes")
	if code == 0 || !strings.Contains(stderr, "config-20260201T100000Z-000.yaml") ||
		!strings.Contains(stderr, "--restore-backup") || !strings.Contains(stderr, "nothing was changed") {
		t.Fatalf("update without the flag: exit %d, stderr %q", code, stderr)
	}
	if !bytes.Equal(installed(), original) {
		t.Fatal("the program was replaced without the restore confirmation")
	}
	if got, _ := os.ReadFile(configPath); string(got) != currentConfig {
		t.Fatalf("the configuration changed: %q", got)
	}
	if entries, _ := os.ReadDir(backups); len(entries) != 2 {
		t.Fatalf("the backups changed: %d files", len(entries))
	}

	code, stdout, stderr := c.run(t, "update", "--to", "v1.0.0", "--yes", "--restore-backup", "--output", "json")
	if code != 0 || !strings.Contains(stdout, `"updated":true`) || !strings.Contains(stdout, `"latest":"v1.0.0"`) {
		t.Fatalf("update with the flag: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !bytes.Equal(installed(), program) {
		t.Fatal("the installed program is not the older release")
	}
	if got, _ := os.ReadFile(configPath); string(got) != goodConfig {
		t.Errorf("the configuration is not the restored backup: %q", got)
	}
	kept := false
	entries, _ := os.ReadDir(backups)
	for _, e := range entries {
		if data, _ := os.ReadFile(filepath.Join(backups, e.Name())); string(data) == currentConfig {
			kept = true
		}
	}
	if !kept {
		t.Error("the configuration that was replaced is not among the backups")
	}
	// The older program, now installed, reads the restored file.
	if code, _, stderr := c.run(t, "config", "validate"); code != 0 {
		t.Errorf("config validate with the older program: exit %d, stderr %q", code, stderr)
	}
}
