package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
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

const formerNamesConfig = `version: 3
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

// formerCatalog is the provider catalog of the release before the rename.
type formerCatalog struct{}

func (formerCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	if id != "acmechat" {
		return config.ProviderMetadata{}, false
	}
	return config.ProviderMetadata{ID: id, Name: "Acme Chat", SecretRoles: []config.SecretRole{{Name: "token"}},
		Tools: []config.ToolMetadata{
			{ID: "acmechat.messages.fetch", Effect: config.EffectRead},
			{ID: "acme.chat.messages.list", Effect: config.EffectRead},
		}}, true
}

func (f formerCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	metadata, _ := f.ProviderMetadata("acmechat")
	return []config.ProviderMetadata{metadata}
}

// vaultRenameFixture returns a directory whose vault holds a secret and approves connection "conn" under the
// former provider ID and the former tool ID, as an earlier release configured it. The configuration is then
// the one a user still has on disk.
func vaultRenameFixture(t *testing.T, tools string) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	dir := t.TempDir()
	body := `version: 3
services:
  svc:
    provider: acmechat
    base_url: https://example.invalid
credentials:
  cred:
    type: vault
connections:
  conn:
    service: svc
    credential: cred
    tools: ` + tools + "\n"
	if err := os.WriteFile(configIn(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	v := seedEncryptedVault(t, dir, "cred", "token", "synthetic-token", "s3cret-phrase")
	// The earlier release knew the provider under its former ID, so the file loads as written.
	old, err := config.Load(configIn(dir), formerCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := approval.Approve(context.Background(), old, v, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runRenameApprove(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	var stdout, stderr bytes.Buffer
	opts := &Options{Input: strings.NewReader(""), Redactor: &redact.Redactor{}}
	cmd := newRootCommand(opts, renameRegistry(t))
	code := run(cmd, opts, append([]string{"vault", "approve", "--config", configIn(dir)}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// pendingRenamed loads the configuration with the provider catalog and reports the connection's open change.
func pendingRenamed(t *testing.T, dir string) (approval.Change, bool) {
	t.Helper()
	cfg, err := config.Load(configIn(dir), renameRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	if _, err := v.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Open {
		if c.Connection == "conn" {
			return c, true
		}
	}
	return approval.Change{}, false
}

// A vault approval is given for the provider and the tools list a connection has. Loading a former name
// changes both, so the connection is open; the view tells it as a rename, and approving it closes it.
func TestRenamedNamesOpenAVaultConnectionAndShowAsRenamed(t *testing.T) {
	const tools = "[acmechat.messages.fetch, acme.chat.messages.list]"

	t.Run("text", func(t *testing.T) {
		dir := vaultRenameFixture(t, tools)
		change, open := pendingRenamed(t, dir)
		if !open {
			t.Fatal("the connection with renamed names is not open")
		}
		if approval.DirectApprovable(change) {
			t.Error("a renamed provider became directly approvable")
		}
		code, stdout, stderr := runRenameApprove(t, dir)
		if code != exitOK || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		for _, want := range []string{
			"  provider  renamed acmechat -> acme.chat\n", "  tools  1 renamed\n",
			"    renamed acmechat.messages.fetch -> acme.chat.messages.get\n", "    unchanged: 1\n",
			"approved 1 connection: conn",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
		if strings.Contains(stdout, "    + ") || strings.Contains(stdout, "    - ") {
			t.Errorf("a rename is shown as added and removed:\n%s", stdout)
		}
		if _, open := pendingRenamed(t, dir); open {
			t.Error("the connection is still open after vault approve")
		}
	})

	t.Run("json", func(t *testing.T) {
		dir := vaultRenameFixture(t, tools)
		code, stdout, stderr := runRenameApprove(t, dir, "--output", "json")
		if code != exitOK || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		var got struct {
			Open []struct {
				Fields []struct {
					Field   string   `json:"field"`
					Before  *string  `json:"before"`
					After   *string  `json:"after"`
					Added   []string `json:"added"`
					Removed []string `json:"removed"`
					Kept    []string `json:"kept"`
					Renamed []struct {
						Old string `json:"old"`
						New string `json:"new"`
					} `json:"renamed"`
				} `json:"fields"`
			} `json:"open"`
			Approved []string `json:"approved"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got.Open) != 1 || len(got.Open[0].Fields) != 2 {
			t.Fatalf("json = %v\n%s", err, stdout)
		}
		provider, toolsField := got.Open[0].Fields[0], got.Open[0].Fields[1]
		if provider.Field != "provider" || *provider.Before != "acmechat" || *provider.After != "acme.chat" ||
			len(provider.Renamed) != 1 || provider.Renamed[0].Old != "acmechat" || provider.Renamed[0].New != "acme.chat" {
			t.Errorf("provider = %+v", provider)
		}
		if toolsField.Field != "tools" || len(toolsField.Renamed) != 1 ||
			toolsField.Renamed[0].Old != "acmechat.messages.fetch" || toolsField.Renamed[0].New != "acme.chat.messages.get" ||
			len(toolsField.Added) != 0 || len(toolsField.Removed) != 0 || len(toolsField.Kept) != 1 {
			t.Errorf("tools = %+v", toolsField)
		}
		if len(got.Approved) != 1 || got.Approved[0] != "conn" {
			t.Errorf("approved = %v", got.Approved)
		}
	})

	// The approval stays open after the configuration was saved with the current names (as the TUI and the web
	// interface do), and the view still tells it as a rename.
	t.Run("saved with current names", func(t *testing.T) {
		dir := vaultRenameFixture(t, tools)
		saved := strings.NewReplacer("acmechat.messages.fetch", "acme.chat.messages.get",
			"provider: acmechat", "provider: acme.chat").Replace(mustRead(t, configIn(dir)))
		if err := os.WriteFile(configIn(dir), []byte(saved), 0o600); err != nil {
			t.Fatal(err)
		}
		change, open := pendingRenamed(t, dir)
		if !open {
			t.Fatal("the connection saved with current names is not open")
		}
		var provider, toolsField approval.FieldChange
		for _, f := range change.Fields {
			switch f.Field {
			case approval.FieldProvider:
				provider = f
			case approval.FieldTools:
				toolsField = f
			}
		}
		if !provider.IsRename || len(toolsField.Renamed) != 1 {
			t.Errorf("provider = %+v, tools = %+v", provider, toolsField)
		}
	})
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
