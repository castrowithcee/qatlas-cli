package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// TestInvokeLogEndToEnd drives the built binary's invocation log: several reading invokes over both the CLI
// and the MCP broker append entries to vault/logs, none of them carrying an argument or a secret, 'qatlas
// vault logs verify' reports the chain intact, and once one entry is changed by hand it reports the break
// instead.
func TestInvokeLogEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary")
	}

	dir := t.TempDir()
	bin := buildBinary(t, dir)

	server := mock(t, "Token "+canaryPrimaryID+":"+canaryPrimarySecret,
		[]map[string]any{page(1, "Runbook")}, "<p>content</p>")

	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(`version: 1
services:
  wiki:
    provider: bookstack
    base_url: %s
credentials:
  reader:
    type: env
    values:
      token-id: E2E_LOG_ID
      token-secret: E2E_LOG_SECRET
connections:
  primary:
    service: wiki
    credential: reader
defaults:
  connections:
    bookstack: primary
`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	var seen strings.Builder
	c := &runner{bin: bin, seen: &seen, env: []string{
		"HOME=" + dir,
		"PATH=" + os.Getenv("PATH"),
		"QATLAS_CONFIG=" + configPath,
		secret.StoreSelector + "=none",
		"E2E_LOG_ID=" + canaryPrimaryID,
		"E2E_LOG_SECRET=" + canaryPrimarySecret,
	}}

	// argumentCanary is a page ID no page has, distinctive enough that its digits cannot appear anywhere
	// else in the log, such as in a sequence number or a duration.
	const argumentCanary = 774411

	if code, _, stderr := c.run(t, "invoke", "bookstack.pages.list"); code != 0 {
		t.Fatalf("invoke pages.list exit=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := c.runInput(t, `{"id":1}`, "invoke", "bookstack.pages.get"); code != 0 {
		t.Fatalf("invoke pages.get(1) exit=%d stderr=%q", code, stderr)
	}
	// A page nothing serves: a completed, failed invoke, still logged like any other.
	if code, _, _ := c.runInput(t, fmt.Sprintf(`{"id":%d}`, argumentCanary), "invoke", "bookstack.pages.get"); code == 0 {
		t.Fatalf("invoke pages.get(%d) exit=0, want a provider-error", argumentCanary)
	}

	mcpInput := `{"jsonrpc":"2.0","id":"list","method":"tools/call","params":{` + mcpMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"bookstack.pages.list","connection":"primary"}}}` + "\n"
	code, stdout, stderr := c.runInput(t, mcpInput, "mcp")
	if code != 0 || stderr != "" {
		t.Fatalf("mcp exit=%d stderr=%q", code, stderr)
	}
	if responses := decodeMCP(t, stdout); responses["list"].Result.IsError {
		t.Fatalf("MCP invoke failed: %+v", responses["list"])
	}

	logsDir := filepath.Join(dir, "vault", "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", logsDir, err)
	}
	var dayFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			dayFile = filepath.Join(logsDir, e.Name())
		}
	}
	if dayFile == "" {
		t.Fatalf("no invocation log day file in %v", entries)
	}
	data, err := os.ReadFile(dayFile)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", dayFile, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("log has %d lines, want 4 (one per invoke): %s", len(lines), data)
	}
	for _, canary := range append(allCanaries(), fmt.Sprint(argumentCanary)) {
		if strings.Contains(string(data), canary) {
			t.Fatalf("invocation log contains a canary %q: %s", canary, data)
		}
	}

	if code, stdout, stderr := c.run(t, "vault", "logs", "verify"); code != 0 {
		t.Fatalf("vault logs verify (intact chain) exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// Change one entry by hand: a value that stays valid JSON but is not what was hashed when the chain
	// was written.
	tampered := strings.Replace(string(data), `"result":"success"`, `"result":"tampered"`, 1)
	if tampered == string(data) {
		t.Fatal("no successful entry found to tamper with")
	}
	if err := os.WriteFile(dayFile, []byte(tampered), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", dayFile, err)
	}

	code, stdout, stderr = c.run(t, "vault", "logs", "verify")
	if code == 0 {
		t.Fatalf("vault logs verify (tampered) exit=0, want non-zero; stdout=%q", stdout)
	}
	if stderr == "" {
		t.Error("vault logs verify (tampered): stderr is empty, want a diagnostic naming the broken chain")
	}
}
