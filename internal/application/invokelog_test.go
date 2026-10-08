package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// TestInvokeWritesTheInvocationLog covers the invocation log's part that lives in Core.Invoke: every call
// appends one entry, whatever it returns, without ever writing the arguments it was called with, and a log
// failure never changes the invoke's own result.
func TestInvokeWritesTheInvocationLog(t *testing.T) {
	dir := t.TempDir()
	logger := invokelog.New(dir, 90)

	// withValue=false leaves the credential's env var unset, so the mutation below fails with missing-secret
	// before it reaches the provider: the log still gets an entry for it, unverified like any other, with the
	// connection and the effect the descriptor names, exactly as it would for a vault-locked or an
	// unencrypted-vault failure, none of which needs its own case here since Invoke never sees the difference.
	core, _ := testCore(t, []string{"primary"}, nil, false)
	core.SetInvokeLog(logger, "cli", nil)

	const canary = "canary-argument-value-must-not-be-logged"
	_, err := core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.pages.get", Connection: "primary", Arguments: json.RawMessage(`{"id":"` + canary + `"}`),
	})
	if err == nil {
		t.Fatal("Invoke() without the secret succeeded, want missing-secret")
	}

	report, err := invokelog.VerifyWith(dir, nil)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Broken {
		t.Fatalf("Verify() report = %+v, want an intact chain", report)
	}
	if len(report.Days) != 1 || report.Days[0].Entries != 1 || report.Days[0].Unverified != 1 {
		t.Fatalf("report.Days = %+v, want one unverified entry", report.Days)
	}

	files, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil || len(files) != 2 { // the day file and the lock file
		t.Fatalf("logs dir entries = %v, %v", files, err)
	}
	var dayFile string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			dayFile = f.Name()
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", dayFile))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), canary) {
		t.Fatalf("invocation log contains the argument canary: %s", data)
	}
	var entry invokelog.Entry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", data, err)
	}
	if entry.Path != "cli" || entry.Operation != "fake.pages.get" || entry.Connection != "primary" ||
		entry.Effect != "read" || entry.Result != "missing-secret" || entry.MAC != "" {
		t.Fatalf("entry = %+v", entry)
	}
}

// TestInvokeLogFailureDoesNotChangeTheOutcome is the counterpart: a log that cannot be written must not
// touch the invoke's own result, error, or exit code, only warn on the audit writer.
func TestInvokeLogFailureDoesNotChangeTheOutcome(t *testing.T) {
	dir := t.TempDir()
	// A plain file where "logs" should be a directory makes every Append fail without touching anything
	// real, such as filesystem permissions the test runner may not control.
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	logger := invokelog.New(dir, 90)

	core, calls := testCore(t, []string{"primary"}, nil, true)
	core.SetInvokeLog(logger, "cli", nil)
	var audit strings.Builder
	core.SetAudit(&audit)

	response, err := core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.pages.get", Connection: "primary", Arguments: json.RawMessage(`{"id":"7"}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want the log failure to stay invisible to the caller", err)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil || result["ok"] != true {
		t.Fatalf("result = %s (%v)", response.Result, err)
	}
	if *calls != 1 {
		t.Fatalf("provider calls = %d, want 1", *calls)
	}
	if !strings.Contains(audit.String(), "could not write the invocation log") {
		t.Fatalf("audit = %q, want a warning about the invocation log", audit.String())
	}
}

// TestInvokeLogsMCPClientInfo confirms the MCP client's name and version, once installed with
// SetInvokeLog, reach the entry.
func TestInvokeLogsMCPClientInfo(t *testing.T) {
	dir := t.TempDir()
	logger := invokelog.New(dir, 90)
	core, _ := testCore(t, []string{"primary"}, nil, true)
	client := &invokelog.ClientInfo{Name: "test-client", Version: "1.2.3"}
	core.SetInvokeLog(logger, "mcp", client)

	if _, err := core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.pages.get", Connection: "primary", Arguments: json.RawMessage(`{"id":"7"}`),
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	data, err := os.ReadFile(latestDayFile(t, dir))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var entry invokelog.Entry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", data, err)
	}
	if entry.Path != "mcp" || entry.Client == nil || *entry.Client != *client {
		t.Fatalf("entry = %+v, want path mcp and client %+v", entry, client)
	}
}

func latestDayFile(t *testing.T, vaultDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(vaultDir, "logs"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return filepath.Join(vaultDir, "logs", e.Name())
		}
	}
	t.Fatal("no day file found")
	return ""
}
