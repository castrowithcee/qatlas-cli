package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// downloadRegistry offers one tool that needs no confirmation of its own and writes the local file named by
// local_path, and a configuration whose connection releases dir for writing.
func downloadRegistry(t *testing.T, dir string) (*capability.Registry, string) {
	t.Helper()
	registry := capability.NewRegistry()
	registerBookstackTestMetadata(t, registry)
	err := registry.Register("bookstack", capability.Operation{
		Descriptor: capability.Descriptor{
			ID: "bookstack.files.download", Version: 1, Description: "Write a local file", Provider: "bookstack",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test-data"},
			LocalFiles:   config.LocalFilesWrite,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"local_path":{"type":"string"}},"required":["local_path"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			Arguments:    []capability.Argument{localfile.DownloadPathArgument()},
		},
		Handler: capability.Handler(func(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
			_ *redact.Redactor, arguments json.RawMessage) (any, error) {
			var input struct {
				LocalPath string `json:"local_path"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			download, err := localfile.CreateForDownload(ctx, resolved, input.LocalPath)
			if err != nil {
				return nil, err
			}
			if _, err := download.Write([]byte("new")); err != nil {
				_ = download.Abort()
				return nil, err
			}
			if err := download.Commit(); err != nil {
				return nil, err
			}
			return map[string]any{"written": true}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Replace(validConfig, "    description: read-only account on the team wiki\n",
		"    description: read-only account on the team wiki\n    files:\n      write: ['"+dir+"']\n", 1)
	return registry, writeConfig(t, config)
}

func TestLocalFileOverwriteConfirmationOverCLI(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	dir := t.TempDir()
	registry, cfg := downloadRegistry(t, dir)
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (int, string, string) {
		var stdout, stderr strings.Builder
		options := &Options{Input: strings.NewReader(`{"local_path":"` + target + `"}`), Redactor: &redact.Redactor{}}
		code := run(newRootCommand(options, registry), options,
			append([]string{"invoke", "bookstack.files.download", "--connection", "wiki", "--config", cfg}, args...),
			&stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	code, _, stderr := invoke()
	if code == exitOK || !strings.Contains(stderr, "confirmation-required") {
		t.Fatalf("unconfirmed: exit=%d stderr=%q", code, stderr)
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("file = %q, want it unchanged", got)
	}
	code, _, stderr = invoke("--confirm")
	if code != exitOK || !strings.Contains(stderr, `"event":"local-file-replaced"`) || strings.Contains(stderr, dir) {
		t.Fatalf("confirmed: exit=%d stderr=%q", code, stderr)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("file = %q, want it replaced", got)
	}
}

func TestLocalFileOverwriteConfirmationOverMCP(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	dir := t.TempDir()
	registry, cfg := downloadRegistry(t, dir)
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := func(id, confirm string) mcpToolResult {
		request := `{"operation":"bookstack.files.download","connection":"wiki","arguments":{"local_path":"` +
			target + `"}` + confirm + `}`
		input := `{"jsonrpc":"2.0","id":"` + id + `","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":` + request + `}}` + "\n"
		responses, _ := runMCPWithOptions(t, registry, input, &Options{Config: cfg, Redactor: &redact.Redactor{}})
		return toolResultFrom(t, responses[`"`+id+`"`])
	}

	refused := call("a", "")
	if !refused.IsError || !strings.Contains(refused.Content[0].Text, "confirmation-required") {
		t.Fatalf("unconfirmed = %+v", refused)
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("file = %q, want it unchanged", got)
	}
	if done := call("b", `,"confirm":true`); done.IsError {
		t.Fatalf("confirmed = %+v", done)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("file = %q, want it replaced", got)
	}
}
