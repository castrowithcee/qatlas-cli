package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// TestLegacyEnvironmentNamesEndToEnd runs the built binary with the variable names of the versions before
// the rename: they still work, and the warning that names the current variable stays on stderr, outside the
// JSON output and the MCP stream.
func TestLegacyEnvironmentNamesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary")
	}
	skipWithoutCertFile(t)

	dir := t.TempDir()
	bin := buildBinary(t, dir)
	server := mock(t, "Token "+canaryPrimaryID+":"+canaryPrimarySecret,
		[]map[string]any{page(1, "Runbook")}, "<p>content</p>")

	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(`version: 4
services:
  wiki:
    provider: bookstack
    base_url: %s
credentials:
  reader:
    type: keyring
connections:
  primary:
    service: wiki
    credential: reader
defaults:
  connections:
    bookstack: primary
`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	base := []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), secret.StoreSelector + "=none"}
	const warnFor = "QCONNECT_READER_TOKEN_ID"
	legacy := append(append([]string{}, base...), "QATLAS_CONFIG="+configPath,
		"QATLAS_READER_TOKEN_ID="+canaryPrimaryID, "QATLAS_READER_TOKEN_SECRET="+canaryPrimarySecret)

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: legacy}

	t.Run("json output stays valid and the warning is on stderr", func(t *testing.T) {
		code, stdout, stderr := c.run(t, "invoke", "bookstack.pages.list", "--output", "json")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if !json.Valid([]byte(stdout)) {
			t.Fatalf("stdout is not valid JSON: %q", stdout)
		}
		for _, name := range []string{"QCONNECT_CONFIG", warnFor, "QCONNECT_READER_TOKEN_SECRET"} {
			if got := strings.Count(stderr, name); got != 1 {
				t.Errorf("stderr names %s %d times, want once: %q", name, got, stderr)
			}
			if strings.Contains(stdout, name) {
				t.Errorf("stdout names %s", name)
			}
		}
		for _, secretValue := range []string{canaryPrimaryID, canaryPrimarySecret} {
			if strings.Contains(stderr, secretValue) {
				t.Error("stderr carries a secret value")
			}
		}
	})

	t.Run("the MCP stream carries no warning", func(t *testing.T) {
		input := `{"jsonrpc":"2.0","id":"list","method":"tools/call","params":{` + mcpMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"bookstack.pages.list","connection":"primary"}}}` + "\n"
		code, stdout, stderr := c.runInput(t, input, "mcp")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("stdout line is not JSON: %q", line)
			}
		}
		if strings.Contains(stdout, "deprecated") || strings.Contains(stdout, "QATLAS_") {
			t.Errorf("stdout names the legacy variables: %q", stdout)
		}
		if !strings.Contains(stderr, "QCONNECT_CONFIG") {
			t.Errorf("stderr = %q, want the warning", stderr)
		}
	})

	t.Run("the current names raise no warning", func(t *testing.T) {
		current := append(append([]string{}, base...), "QCONNECT_CONFIG="+configPath,
			"QCONNECT_READER_TOKEN_ID="+canaryPrimaryID, "QCONNECT_READER_TOKEN_SECRET="+canaryPrimarySecret,
			// Both spellings set: the current one wins, silently.
			"QATLAS_CONFIG=/nonexistent/config.yaml", "QATLAS_READER_TOKEN_ID=wrong")
		c := &runner{bin: bin, seen: &seen, env: current}
		code, _, stderr := c.run(t, "invoke", "bookstack.pages.list", "--output", "json")
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
	})
}
