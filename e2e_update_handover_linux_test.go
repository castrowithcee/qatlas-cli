//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// TestUpdateHandsTheVaultOverToTheNewProgram updates real binaries against a local signed test release while
// an MCP server runs on an unlocked vault. With the setting handover the vault stays unlocked: the server's
// next call reads the vault credential, the vault process is a new one, and the log records the handover.
// With the setting lock the same update locks the vault, and the call reports vault-locked.
func TestUpdateHandsTheVaultOverToTheNewProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary four times")
	}
	for _, behaviour := range []vault.UpdateBehaviour{vault.UpdateHandover, vault.UpdateLock} {
		t.Run(string(behaviour), func(t *testing.T) { updateHandoverCase(t, behaviour) })
	}
}

func updateHandoverCase(t *testing.T, behaviour vault.UpdateBehaviour) {
	const (
		storedID     = "canary-handover-id-5d27a1"
		storedSecret = "canary-handover-secret-93be60"
		passphrase   = "canary-passphrase-handover-8c41"
	)
	dir := t.TempDir()
	bin := filepath.Join(dir, "prefix", "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	wiki := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Handover Runbook")}, "<p>x</p>")

	// The local release server: a signed v2.0.0 whose program is built below.
	signer := releasetest.NewSigner(t)
	keyLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	var archive, checksums []byte
	asset := fmt.Sprintf("qatlas_v2.0.0_linux_%s.tar.gz", runtime.GOARCH)
	var releases *httptest.Server
	releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v2.0.0", "assets": []map[string]string{
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
		cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags",
			"-X github.com/castrowithcee/qatlas-cli/internal/cli.version="+version+
				" -X 'github.com/castrowithcee/qatlas-cli/internal/release.extraKeyLines="+keyLine+"'"+
				" -X github.com/castrowithcee/qatlas-cli/internal/selfupdate.testBaseURL="+releases.URL,
			"-o", path, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("building the binary: %v", err)
		}
	}
	build(bin, "v1.0.0")
	next := filepath.Join(dir, "next")
	build(next, "v2.0.0")
	program, err := os.ReadFile(next)
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

	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(`version: 1
services:
  wiki:
    provider: bookstack
    base_url: %s
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
`, wiki.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	offer := func(string) (string, error) { return passphrase, nil }
	for name, value := range map[string]string{"token-id": storedID, "token-secret": storedSecret} {
		if err := v.Set("vault-reader", name, value, offer); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	reg := capability.NewRegistry()
	if err := bookstack.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg, err := qconfig.Load(configPath, reg)
	if err != nil {
		t.Fatal(err)
	}
	if approved, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil || len(approved) != 1 {
		t.Fatalf("approving the connection: %v, %v", approved, err)
	}
	if behaviour != vault.UpdateHandover {
		if _, err := v.Unlock(passphrase); err != nil {
			t.Fatal(err)
		}
		if err := v.SetUpdateBehaviour(behaviour); err != nil {
			t.Fatal(err)
		}
	}
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: []string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"QATLAS_CONFIG=" + configPath,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		secret.StoreSelector + "=none",
	}}
	var vaultPIDs []int
	t.Cleanup(func() {
		_, _, _ = c.run(t, "vault", "lock")
		for _, pid := range vaultPIDs {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	statusPID := func() int {
		t.Helper()
		code, stdout, stderr := c.run(t, "vault", "status", "--output", "json")
		var doc struct {
			PID int64 `json:"pid"`
		}
		if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil {
			t.Fatalf("vault status: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		return int(doc.PID)
	}

	out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "unlock")
	match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
	if code != 0 || match == nil {
		t.Fatalf("unlock: exit %d, output %q", code, out)
	}
	oldPID, _ := strconv.Atoi(match[1])
	vaultPIDs = append(vaultPIDs, oldPID)

	broker := startBroker(t, c)
	pages := `{"operation":"bookstack.pages.list","connection":"wiki"}`
	if call := broker.call(t, pages, "qatlas.invoke"); call.Result.IsError ||
		!strings.Contains(string(call.Result.Structured), "Handover Runbook") {
		t.Fatalf("call before the update = %+v", call.Result)
	}
	mcpPID := broker.cmd.Process.Pid

	code, stdout, stderr := c.run(t, "update", "--yes", "--output", "json")
	if code != 0 || !strings.Contains(stdout, `"updated":true`) || !strings.Contains(stdout, `"signed":true`) {
		t.Fatalf("update: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got, _ := os.ReadFile(bin); !bytes.Equal(got, program) {
		t.Fatal("the installed program is not the release's")
	}

	call := broker.call(t, pages, "qatlas.invoke")
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", mcpPID)); err != nil || exe != bin {
		t.Errorf("MCP server %d runs %q (%v), want the new program at %s", mcpPID, exe, err, bin)
	}
	if _, err := io.WriteString(broker.stdin, `{"jsonrpc":"2.0","id":"discover","method":"server/discover","params":{`+mcpMeta+"}}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-broker.lines:
		seen.WriteString(line)
		if !strings.Contains(line, `"version":"v2.0.0"`) {
			t.Errorf("the MCP server does not report version v2.0.0: %s", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the MCP server did not answer server/discover")
	}

	switch behaviour {
	case vault.UpdateHandover:
		if !strings.Contains(stderr, "handed over to the new program") || strings.Contains(stderr, "qatlas vault unlock") {
			t.Errorf("update stderr = %q, want the handover and no unlock", stderr)
		}
		if call.Result.IsError || !strings.Contains(string(call.Result.Structured), "Handover Runbook") {
			t.Fatalf("call after the update = %+v, want the page without an unlock", call.Result)
		}
		newPID := statusPID()
		vaultPIDs = append(vaultPIDs, newPID)
		if newPID == oldPID {
			t.Errorf("the vault process is still %d, want a new one", oldPID)
		}
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(oldPID, 0) == nil && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(oldPID, 0) == nil {
			t.Errorf("the old vault process %d still runs", oldPID)
		}
		if !strings.Contains(readLogs(t, dir), `"operation":"vault.handover"`) {
			t.Error("the log has no vault.handover entry")
		}
	case vault.UpdateLock:
		if !strings.Contains(stderr, "locked") || !strings.Contains(stderr, "'qatlas vault unlock'") {
			t.Errorf("update stderr = %q, want the lock and the unlock hint", stderr)
		}
		if !call.Result.IsError || !strings.Contains(string(call.Result.Structured), "vault-locked") {
			t.Fatalf("call after the update = %+v, want vault-locked", call.Result)
		}
	}
	for _, canary := range []string{storedID, storedSecret, passphrase} {
		if strings.Contains(seen.String(), canary) {
			t.Errorf("a secret reached the output: %s", canary)
		}
	}
}

// readLogs returns the invocation log files of the vault in dir, joined.
func readLogs(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "vault", "logs", "*.jsonl"))
	var all strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(data)
	}
	return all.String()
}
