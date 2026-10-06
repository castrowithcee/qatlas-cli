//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	qconfig "github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// TestMCPServerRestartsAfterAnUpdate replaces the program of a running MCP server the way an update does,
// locks and unlocks the vault with the new program, and calls a tool that needs the vault's secret: the
// server restarts itself between the two requests, keeps its process and its session, and needs no new
// initialize.
func TestMCPServerRestartsAfterAnUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary twice")
	}
	const (
		storedID     = "canary-restart-id-4c18d2"
		storedSecret = "canary-restart-secret-71ae05"
		passphrase   = "canary-passphrase-restart-3e9b"
	)
	dir := t.TempDir()
	prefix := filepath.Join(dir, "prefix", "bin")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(prefix, "qatlas")
	buildVersion := func(path, version string) {
		build := exec.Command("go", "build", "-tags", "e2e", "-buildvcs=false",
			"-ldflags", "-X github.com/castrowithcee/qatlas-cli/internal/cli.version="+version, "-o", path, ".")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			t.Fatalf("building the binary: %v", err)
		}
	}
	buildVersion(bin, "1.0.0")
	server := mock(t, "Token "+storedID+":"+storedSecret, []map[string]any{page(1, "Restart Runbook")}, "<p>x</p>")

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
	var vaultPID int
	t.Cleanup(func() {
		_, _, _ = c.run(t, "vault", "lock")
		if vaultPID != 0 {
			_ = syscall.Kill(vaultPID, syscall.SIGKILL)
		}
	})
	unlock := func() {
		t.Helper()
		out, code := runAtTerminal(t, c, passphrase+"\n", "vault", "unlock")
		match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
		if code != 0 || match == nil {
			t.Fatalf("unlock: exit %d, output %q", code, out)
		}
		vaultPID, _ = strconv.Atoi(match[1])
	}
	unlock()

	// The MCP client: a legacy session with initialize, and roots naming the project directory.
	cmd := exec.Command(bin, "mcp")
	cmd.Env = c.environ(t)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := make(chan map[string]json.RawMessage, 16)
	go func() {
		decoder := json.NewDecoder(stdout)
		for {
			var message map[string]json.RawMessage
			if decoder.Decode(&message) != nil {
				close(lines)
				return
			}
			lines <- message
		}
	}()
	send := func(line string) {
		t.Helper()
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	next := func() map[string]json.RawMessage {
		t.Helper()
		select {
		case message, ok := <-lines:
			if !ok {
				t.Fatal("the server ended")
			}
			return message
		case <-time.After(30 * time.Second):
			t.Fatal("no message from the server")
		}
		return nil
	}
	callID := 0
	invoke := func() string {
		t.Helper()
		callID++
		send(fmt.Sprintf(`{"jsonrpc":"2.0","id":"call-%d","method":"tools/call","params":{"name":"qatlas.invoke",`+
			`"arguments":{"operation":"bookstack.pages.list","connection":"wiki"}}}`, callID))
		for {
			message := next()
			if string(message["id"]) == fmt.Sprintf(`"call-%d"`, callID) {
				seen.Write(message["result"])
				return string(message["result"])
			}
		}
	}

	send(`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":{"roots":{}},"clientInfo":{"name":"restart-client","version":"3"}}}`)
	if message := next(); string(message["id"]) != `"init"` {
		t.Fatalf("initialize answer = %v", message)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	roots := next()
	if string(roots["method"]) != `"roots/list"` {
		t.Fatalf("message = %v, want roots/list", roots)
	}
	send(`{"jsonrpc":"2.0","id":` + string(roots["id"]) + `,"result":{"roots":[{"uri":` +
		strconv.Quote((&url.URL{Scheme: "file", Path: dir}).String()) + `}]}}`)

	if result := invoke(); !strings.Contains(result, "Restart Runbook") {
		t.Fatalf("first call = %s, want the page", result)
	}

	// The update replaces the program file; the vault process of the old program is replaced with it.
	replacement := filepath.Join(prefix, "qatlas.new")
	buildVersion(replacement, "2.0.0")
	if err := os.Rename(replacement, bin); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := c.run(t, "vault", "lock"); code != 0 {
		_ = syscall.Kill(vaultPID, syscall.SIGKILL)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(vaultPID, 0) == nil && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	unlock()

	pid := cmd.Process.Pid
	if result := invoke(); !strings.Contains(result, "Restart Runbook") {
		t.Fatalf("call after the update = %s, want the page", result)
	}
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err != nil || exe != bin {
		t.Errorf("process %d runs %q (%v), want the new program at %s", pid, exe, err, bin)
	}
	if text := stderr.String(); !strings.Contains(text, "restarted after an update, version 1.0.0 to 2.0.0") {
		t.Errorf("stderr = %q, want the note naming both versions", text)
	}
	if strings.Count(stderr.String(), "restarting from version") != 1 {
		t.Errorf("stderr = %q, want exactly one restart", stderr.String())
	}
	// The session survived: a further call needs no initialize either.
	if result := invoke(); !strings.Contains(result, "Restart Runbook") {
		t.Errorf("further call = %s, want the page", result)
	}
	for _, canary := range []string{storedID, storedSecret, passphrase} {
		if strings.Contains(seen.String()+stderr.String(), canary) {
			t.Errorf("a secret reached the output: %s", canary)
		}
	}
}
