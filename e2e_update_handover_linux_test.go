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

	"github.com/castrowithcee/qatlas-connect/internal/approval"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-connect/internal/release"
	"github.com/castrowithcee/qatlas-connect/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

// TestUpdateHandsTheVaultOverToTheNewProgram updates real binaries against a local signed test release while
// an MCP server runs on an unlocked vault. With the setting handover the vault stays unlocked: the server's
// next call reads the vault credential, the vault process is a new one, and the log records the handover.
// With the setting lock the same update locks the vault, and the call reports vault-locked.
//
// The case from the earlier release starts from a program built with the tag legacy_vault_names, which
// reaches its vault process by the names of the release before qatlas-connect: its proof label, its runtime
// directory. Its vault process hands over to this release's program all the same, which then serves this
// release's socket alone; before the update, this release's program does not reach the earlier process.
//
// The earlier release at its default location keeps its configuration in ~/.qatlas/cli and hands its
// successor that file. The successor migrates to ~/.qatlas/connect and serves it there, where this release's
// program finds it by default, and leaves ~/.qatlas/cli as it was, but for the earlier process's own record
// of the handover.
func TestUpdateHandsTheVaultOverToTheNewProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary six times")
	}
	for _, tc := range []struct {
		name      string
		behaviour vault.UpdateBehaviour
		earlier   bool
		atDefault bool
	}{
		{string(vault.UpdateHandover), vault.UpdateHandover, false, false},
		{string(vault.UpdateLock), vault.UpdateLock, false, false},
		{"handover from the earlier release", vault.UpdateHandover, true, false},
		{"handover from the earlier release at its default location", vault.UpdateHandover, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) { updateHandoverCase(t, tc.behaviour, tc.earlier, tc.atDefault) })
	}
}

// updateHandoverCase runs one case; atDefault keeps the configuration in the earlier release's default
// location instead of naming it in QATLAS_CONFIG.
func updateHandoverCase(t *testing.T, behaviour vault.UpdateBehaviour, earlier, atDefault bool) {
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

	build := func(path, version, tags string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-tags", tags, "-buildvcs=false", "-ldflags",
			"-X github.com/castrowithcee/qatlas-connect/internal/cli.version="+version+
				" -X 'github.com/castrowithcee/qatlas-connect/internal/release.extraKeyLines="+keyLine+"'"+
				" -X github.com/castrowithcee/qatlas-connect/internal/selfupdate.testBaseURL="+releases.URL,
			"-o", path, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("building the binary: %v", err)
		}
	}
	tags := "e2e"
	if earlier {
		tags += ",legacy_vault_names"
	}
	build(bin, "v1.0.0", tags)
	next := filepath.Join(dir, "next")
	build(next, "v2.0.0", "e2e")
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

	configDir := dir
	if atDefault {
		configDir = filepath.Join(dir, ".qatlas", "cli")
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(configDir, "config.yaml")
	config := fmt.Sprintf(`version: 4
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
	v := vault.New(configDir)
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
	env := []string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"XDG_RUNTIME_DIR=" + runtimeDir,
		secret.StoreSelector + "=none",
	}
	if !atDefault {
		env = append(env, "QATLAS_CONFIG="+configPath)
	}
	c := &runner{bin: bin, seen: &seen, env: env}
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
	sockets := func(name string) []string {
		found, _ := filepath.Glob(filepath.Join(runtimeDir, name, "vault-*"))
		return found
	}
	if earlier {
		if len(sockets("qatlas")) == 0 || len(sockets("qatlas-connect")) != 0 {
			t.Fatalf("the earlier program's vault process listens at %v and %v, want the earlier runtime directory only",
				sockets("qatlas"), sockets("qatlas-connect"))
		}
	}
	// At the default location this release's program would migrate the configuration before the update.
	if earlier && !atDefault {
		nextRunner := &runner{bin: next, seen: &seen, env: c.env}
		if code, stdout, stderr := nextRunner.run(t, "vault", "status", "--output", "json"); code != 0 ||
			!strings.Contains(stdout, `"process":"none"`) {
			t.Fatalf("vault status of the new program before the update: exit %d, stdout %q, stderr %q, want no "+
				"vault process", code, stdout, stderr)
		}
	}

	broker := startBroker(t, c)
	pages := `{"operation":"bookstack.pages.list","connection":"wiki"}`
	if call := broker.call(t, pages, "qatlas.invoke"); call.Result.IsError ||
		!strings.Contains(string(call.Result.Structured), "Handover Runbook") {
		t.Fatalf("call before the update = %+v", call.Result)
	}
	mcpPID := broker.cmd.Process.Pid
	// The call's record reaches the log after its answer; the state before the update holds it.
	for deadline := time.Now().Add(5 * time.Second); atDefault && time.Now().Before(deadline) &&
		!strings.Contains(readLogs(t, configDir), `"bookstack.pages.list"`); {
		time.Sleep(20 * time.Millisecond)
	}
	before := dirState(t, configDir)

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
		if !strings.Contains(readLogs(t, configDir), `"operation":"vault.handover"`) {
			t.Error("the log has no vault.handover entry")
		}
		if len(sockets("qatlas-connect")) == 0 || len(sockets("qatlas")) != 0 {
			t.Errorf("after the handover the vault process listens at %v, and %v is left in the earlier runtime "+
				"directory, want this release's socket only", sockets("qatlas-connect"), sockets("qatlas"))
		}
		if atDefault {
			checkLegacyHomeKept(t, before, dirState(t, configDir))
			current := filepath.Join(dir, ".qatlas", "connect")
			if info, err := os.Stat(current); err != nil || !info.IsDir() {
				t.Fatalf("%s is %v, %v, want the migrated configuration", current, info, err)
			}
			// The successor records the call after the update in the migrated vault's log.
			calls := func(dir string) int { return strings.Count(readLogs(t, dir), `"bookstack.pages.list"`) }
			if got, was := calls(current), calls(configDir); got <= was {
				t.Errorf("the migrated log records %d calls, the earlier one %d, want the call after the update "+
					"in the migrated log", got, was)
			}
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

// dirState describes every entry below dir by its mode and, for a regular file, its content.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		state[rel] = info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state[rel] += " " + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// checkLegacyHomeKept checks that the earlier release's directory is what it was before the update, but for
// the record of the handover the earlier vault process appends to its own log, which may create the log.
func checkLegacyHomeKept(t *testing.T, before, after map[string]string) {
	t.Helper()
	logs := filepath.Join("vault", "logs")
	for rel, now := range after {
		was, ok := before[rel]
		switch {
		case now == was:
		case !ok && (rel == logs || rel == filepath.Join(logs, ".lock")) && !strings.Contains(strings.TrimSpace(now), " "):
		case ok && !strings.HasPrefix(now, was), !strings.HasPrefix(rel, logs+string(filepath.Separator)),
			!strings.HasSuffix(rel, ".jsonl"):
			t.Errorf("%s changed in the earlier release's directory", rel)
		default:
			added := strings.TrimPrefix(now, was)
			if !ok {
				_, added, _ = strings.Cut(now, " ")
			}
			for _, line := range strings.Split(strings.TrimSpace(added), "\n") {
				if !strings.Contains(line, `"operation":"vault.handover"`) {
					t.Errorf("%s gained an entry other than the earlier process's handover: %s", rel, line)
				}
			}
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			t.Errorf("%s disappeared from the earlier release's directory", rel)
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
