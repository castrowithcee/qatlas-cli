package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// vendorProviderRegistry holds one <vendor>.<product> provider with a hyphenated tool word.
func vendorProviderRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{ID: "acme.chat", Name: "Acme chat",
		Description: "Chat of a fake vendor"}, nil); err != nil {
		t.Fatal(err)
	}
	err := registry.Register("acme.chat", capability.Operation{
		Descriptor: capability.Descriptor{
			ID: "acme.chat.direct-messages.get", Version: 1, Description: "Read a direct message", Provider: "acme.chat",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test"},
			InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

const vendorProviderConfig = `version: 2
services:
  chat:
    provider: acme.chat
    base_url: https://example.invalid
credentials:
  reader:
    type: keyring
connections:
  primary:
    service: chat
    credential: reader
provider_notes:
  acme.chat: team chat
defaults:
  connections:
    acme.chat: primary
`

// A <vendor>.<product> provider works wherever a provider ID appears: configuration keys, discovery, describe,
// invoke, and the MCP filter.
func TestVendorProviderIsUsableEverywhere(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	registry := vendorProviderRegistry(t)
	path := writeConfig(t, vendorProviderConfig)

	runCLI := func(input string, args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		options := &Options{Input: strings.NewReader(input), Redactor: &redact.Redactor{}}
		code := run(newRootCommand(options, registry), options,
			append(args, "--config", path, "--output", "json"), &stdout, &stderr)
		if code != exitOK {
			t.Fatalf("%v: exit=%d stderr=%q", args, code, stderr.String())
		}
		return stdout.String()
	}

	providers := runCLI("", "providers")
	for _, want := range []string{`"acme.chat"`, `team chat`} {
		if !strings.Contains(providers, want) {
			t.Errorf("providers = %s, want it to contain %s", providers, want)
		}
	}
	if tools := runCLI("", "tools", "acme.chat"); !strings.Contains(tools, "acme.chat.direct-messages.get") {
		t.Errorf("tools acme.chat = %s", tools)
	}
	if connections := runCLI("", "connections", "acme.chat"); !strings.Contains(connections, "primary") {
		t.Errorf("connections acme.chat = %s", connections)
	}
	if describe := runCLI("", "describe", "acme.chat.direct-messages.get"); !strings.Contains(describe, "Read a direct message") {
		t.Errorf("describe = %s", describe)
	}
	// Without --connection the provider default of defaults.connections selects the route.
	if invoked := runCLI("{}", "invoke", "acme.chat.direct-messages.get"); !strings.Contains(invoked, `"ok":true`) {
		t.Errorf("invoke = %s", invoked)
	}

	search := func(arguments string) mcpToolResult {
		t.Helper()
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":` + arguments + `}}` + "\n"
		responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
		if stderr != "" {
			t.Fatalf("stderr = %q", stderr)
		}
		return toolResultFrom(t, responses["1"])
	}
	if result := search(`{"provider":"acme.chat"}`); result.IsError ||
		!strings.Contains(string(result.Structured), "acme.chat.direct-messages.get") {
		t.Errorf("search provider = %s", result.Structured)
	}
	if result := search(`{"list":"providers"}`); result.IsError || !strings.Contains(string(result.Structured), `"acme.chat"`) {
		t.Errorf("search list=providers = %s", result.Structured)
	}
}
