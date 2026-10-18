package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// renameRegistry registers a two-segment provider that renamed itself and one of its tools.
func renameRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "acme.chat", Name: "Acme Chat", SecretRoles: []config.SecretRole{{Name: "token"}},
		RenamedTools: map[string]string{"acmechat.messages.fetch": "acme.chat.messages.get"},
		FormerIDs:    []string{"acmechat"},
		RemovedTools: []string{"acmechat.messages.purge"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"acme.chat.messages.get", "acme.chat.messages.list"} {
		err := reg.Register("acme.chat", capability.Operation{
			Descriptor: capability.Descriptor{
				ID: id, Version: 1, Description: "Read messages", Provider: "acme.chat",
				Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
					Confirmation: capability.ConfirmationNone, DataSensitivity: "test"},
				InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
			},
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return map[string]any{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Fatal(err)
	}
	return reg
}

const formerNamesConfig = `version: 1
services:
  svc:
    provider: acmechat
    base_url: https://example.invalid
credentials:
  cred:
    type: env
    values:
      token: ACME_RENAME_TOKEN
connections:
  conn:
    service: svc
    credential: cred
    permissions: [read]
    tools: [acmechat.messages.fetch, acme.chat.messages.list, acmechat.messages.purge]
provider_notes:
  acmechat: team chat
defaults:
  connections:
    acmechat: conn
    acmechat.messages.fetch: conn
`

func runRenameCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	redactor := &redact.Redactor{}
	opts := &Options{Input: strings.NewReader(""), Redactor: redactor,
		Secrets: secret.NewWith(func(string) string { return "" }, nil, nil, redactor)}
	code := run(newRootCommand(opts, renameRegistry(t)), opts, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func runRenameMCP(t *testing.T, path, tool, arguments string) string {
	t.Helper()
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"` + tool +
		`","arguments":` + arguments + `}}` + "\n"
	responses, _ := runMCPWithOptions(t, renameRegistry(t), input, &Options{Config: path, Redactor: &redact.Redactor{}})
	result := toolResultFrom(t, responses["1"])
	if !result.IsError {
		t.Fatalf("%s %s = %+v, want a tool error", tool, arguments, result)
	}
	return result.Content[0].Text
}

func TestFormerNamesLoadWarnAndKeepTheFileUntouched(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, formerNamesConfig)
	code, stdout, stderr := runRenameCLI(t, "config", "validate", "--config", path)
	if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{
		`qatlas: warning: services.svc.provider: provider "acmechat" was renamed to "acme.chat"`,
		`qatlas: warning: connections.conn.tools: tool "acmechat.messages.fetch" was renamed to "acme.chat.messages.get"`,
		`qatlas: warning: defaults.connections.acmechat: provider "acmechat" was renamed to "acme.chat"`,
		`qatlas: warning: defaults.connections.acmechat.messages.fetch: tool "acmechat.messages.fetch" was renamed`,
		`qatlas: warning: provider_notes.acmechat: provider "acmechat" was renamed to "acme.chat"`,
		`qatlas: warning: connections.conn.tools: tool "acmechat.messages.purge" was removed from qatlas`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != formerNamesConfig {
		t.Errorf("validate changed the file:\n%s", after)
	}
}

func TestFormerToolIDEndsAsUnknownOperationNamingTheNewOne(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, formerNamesConfig)
	const want = `unknown-operation: unknown tool "acmechat.messages.fetch": it was renamed to "acme.chat.messages.get"`
	for _, args := range [][]string{
		{"describe", "acmechat.messages.fetch"}, {"invoke", "acmechat.messages.fetch", "--confirm"},
	} {
		code, stdout, stderr := runRenameCLI(t, append(args, "--config", path)...)
		first, _, _ := strings.Cut(stderr, "\n")
		if code != exitUsage || stdout != "" || first != "qatlas: "+want {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for tool, arguments := range map[string]string{
		"qatlas.describe": `{"operation":"acmechat.messages.fetch"}`,
		"qatlas.invoke":   `{"operation":"acmechat.messages.fetch","confirm":true}`,
	} {
		if got := runRenameMCP(t, path, tool, arguments); got != want {
			t.Errorf("%s = %q, want %q", tool, got, want)
		}
	}
	// The current ID works, which shows the current name is what the connection offers.
	if code, _, stderr := runRenameCLI(t, "describe", "acme.chat.messages.get", "--config", path); code != exitOK {
		t.Errorf("describe by the current ID: exit=%d stderr=%q", code, stderr)
	}
}

func TestFormerProviderNameIsUnknownAndNamesTheNewOne(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, formerNamesConfig)
	for _, args := range [][]string{{"tools", "acmechat"}, {"connections", "acmechat"}} {
		code, stdout, stderr := runRenameCLI(t, append(args, "--config", path)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, `unknown`) ||
			!strings.Contains(stderr, `(renamed to "acme.chat")`) {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for _, arguments := range []string{`{"provider":"acmechat"}`, `{"list":"connections","provider":"acmechat"}`} {
		got := runRenameMCP(t, path, "qatlas.search", arguments)
		if !strings.HasPrefix(got, "invalid-request: unknown provider \"acmechat\" (renamed to \"acme.chat\")") {
			t.Errorf("search %s = %q", arguments, got)
		}
	}
	// The current name lists the tools through the mapped connection.
	code, stdout, stderr := runRenameCLI(t, "tools", "acme.chat", "--connection", "conn", "--config", path,
		"--output", "json")
	if code != exitOK || !strings.Contains(stdout, "acme.chat.messages.get") ||
		!strings.Contains(stdout, "acme.chat.messages.list") || strings.Contains(stdout, "acmechat") {
		t.Errorf("tools: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// A vault approval is given for the provider and the tools list a connection has. Loading a former name
// changes both, so the scope, and with it the fingerprint, no longer equals the one approved earlier.
func TestFormerNamesChangeTheApprovalFingerprint(t *testing.T) {
	path := writeConfig(t, formerNamesConfig)
	cfg, err := config.Load(path, renameRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve("conn", "")
	if err != nil {
		t.Fatal(err)
	}
	current := secret.ScopeOf(resolved)
	approved := current
	approved.Provider = "acmechat"
	approved.Tools = []string{"acmechat.messages.fetch", "acme.chat.messages.list", "acmechat.messages.purge"}
	if vault.Fingerprint(current, "id") == vault.Fingerprint(approved, "id") {
		t.Error("the fingerprint of the mapped connection equals the one given for the former names")
	}
	if current.Provider != "acme.chat" || current.Tools[0] != "acme.chat.messages.get" {
		t.Errorf("scope = %+v, want the current names", current)
	}
}
