//go:build linux || darwin || windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// TestSignedInvokeLogEndToEnd drives the built binary's signed invocation log: while the vault is locked
// an invoke is logged unverified; once 'vault unlock' at a pseudo-terminal started the vault process,
// invokes over the CLI and an MCP broker started before the unlock are logged signed by that process, and
// 'vault logs verify' has it check them. A line changed by hand, the last one, is reported changed. 'vault
// lock' ends the process, after which verify checks the chain alone and reports the check values as not
// checked. Neither a secret nor any key material ever reaches an output or the log.
func TestSignedInvokeLogEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary")
	}

	const (
		storedID     = "canary-log-id-5c02aa"
		storedSecret = "canary-log-secret-e41f97"
		passphrase   = "canary-log-passphrase-7d3b10"
	)

	dir := t.TempDir()
	bin := buildBinary(t, dir)
	server := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Signed Runbook")}, "<p>x</p>")

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
`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	v := vault.New(dir)
	offer := func(string) (string, error) { return passphrase, nil }
	if err := v.Set("vault-reader", "token-id", storedID, offer); err != nil {
		t.Fatalf("seed token-id: %v", err)
	}
	if err := v.Set("vault-reader", "token-secret", storedSecret, offer); err != nil {
		t.Fatalf("seed token-secret: %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := snap.Identity
	reg := capability.NewRegistry()
	if err := bookstack.Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg, err := qconfig.Load(configPath, reg)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	if approved, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil || len(approved) != 1 {
		t.Fatalf("approving the connection: %v, %v", approved, err)
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
	var processID int
	t.Cleanup(func() {
		_, _, _ = c.run(t, "vault", "lock")
		if processID != 0 {
			killProcess(processID)
		}
	})

	broker := startBroker(t, c)

	logsDir := filepath.Join(dir, "vault", "logs")
	readLog := func(t *testing.T) (string, []map[string]any) {
		t.Helper()
		files, _ := filepath.Glob(filepath.Join(logsDir, "*.jsonl"))
		if len(files) != 1 {
			t.Fatalf("day files = %v, want exactly one", files)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]any
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("log line %q: %v", line, err)
			}
			entries = append(entries, entry)
		}
		return files[0], entries
	}
	type verifyDay struct {
		Entries, Unverified, Unchecked, Changed int
		Problem                                 string
	}
	verify := func(t *testing.T) (int, string, []verifyDay, string) {
		t.Helper()
		code, stdout, stderr := c.run(t, "vault", "logs", "verify", "--output", "json")
		var doc struct {
			Broken   bool        `json:"broken"`
			MACCheck string      `json:"mac_check"`
			Days     []verifyDay `json:"days"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("verify: exit %d, stdout %q, stderr %q: %v", code, stdout, stderr, err)
		}
		return code, doc.MACCheck, doc.Days, stderr
	}

	if code, _, stderr := c.run(t, "invoke", "bookstack.pages.list"); code != 1 || !strings.Contains(stderr, "vault-locked") ||
		strings.Contains(stderr, "warning") {
		t.Fatalf("invoke while locked: exit %d, stderr %q", code, stderr)
	}

	out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "unlock")
	match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
	if code != 0 || match == nil {
		t.Fatalf("unlock: exit %d, output %q", code, out)
	}
	processID, _ = strconv.Atoi(match[1])

	if code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list"); code != 0 || !strings.Contains(stdout, "Signed Runbook") ||
		stderr != "" {
		t.Fatalf("invoke pages.list: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := c.runInput(t, `{"id":1}`, "invoke", "bookstack.pages.get"); code != 0 || stderr != "" {
		t.Fatalf("invoke pages.get: exit %d, stderr %q", code, stderr)
	}
	if invoke := broker.call(t, `{"operation":"bookstack.pages.list","connection":"wiki"}`, "qatlas.invoke"); invoke.Result.IsError {
		t.Fatalf("MCP invoke = %s", invoke.Result.Structured)
	}

	file, entries := readLog(t)
	if len(entries) != 4 {
		t.Fatalf("the log holds %d entries, want 4", len(entries))
	}
	for i, entry := range entries {
		signed := entry["mac"] != ""
		if signed != (i > 0) {
			t.Fatalf("entry %d = %v, want only the entry written while locked unsigned", i, entry)
		}
	}
	if entries[3]["path"] != "mcp" {
		t.Fatalf("last entry = %v, want the broker's invoke", entries[3])
	}

	code, macCheck, days, stderr := verify(t)
	if code != 0 || macCheck != "checked" || len(days) != 1 || days[0].Entries != 4 || days[0].Unverified != 1 ||
		days[0].Unchecked != 0 || days[0].Changed != 0 || stderr != "" {
		t.Fatalf("verify: exit %d, mac_check %q, days %+v, stderr %q", code, macCheck, days, stderr)
	}

	// The last line by hand: its chain stays intact, since nothing follows it, but its check value fails.
	original, _ := os.ReadFile(file)
	tampered := strings.Replace(string(original), `"path":"mcp"`, `"path":"cli"`, 1)
	if tampered == string(original) {
		t.Fatal("the broker's entry was not found to change")
	}
	if err := os.WriteFile(file, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, days, stderr = verify(t)
	if code == 0 || days[0].Changed != 1 || !strings.Contains(days[0].Problem, "does not match its check value") ||
		!strings.Contains(stderr, "broken") {
		t.Fatalf("verify of a changed line: exit %d, days %+v, stderr %q", code, days, stderr)
	}
	if err := os.WriteFile(file, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := c.run(t, "vault", "lock"); code != 0 || !strings.Contains(stdout, "the vault is locked") {
		t.Fatalf("lock: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for deadline := time.Now().Add(5 * time.Second); processAlive(processID); {
		if time.Now().After(deadline) {
			t.Fatalf("the vault process %d did not end when it was locked", processID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	processID = 0

	code, macCheck, days, stderr = verify(t)
	if code != 0 || macCheck != "vault-locked" || days[0].Unchecked != 3 || days[0].Unverified != 1 || stderr != "" {
		t.Fatalf("verify after lock: exit %d, mac_check %q, days %+v, stderr %q", code, macCheck, days, stderr)
	}

	broker.stop(t)
	logData, _ := os.ReadFile(file)
	for _, canary := range []string{storedID, storedSecret, passphrase, vaultKey, strings.ToLower(vaultKey), "AGE-SECRET-KEY"} {
		if strings.Contains(seen.String(), canary) || strings.Contains(string(logData), canary) {
			t.Errorf("secret or key material reached the output or the log")
		}
	}
}
