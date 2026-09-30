package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// listRegistry adds a tool with a result list, and one without, to the fake registry; calls counts how often
// a handler ran.
func listRegistry(t *testing.T, calls *int) *capability.Registry {
	t.Helper()
	registry := fakeRegistry(t)
	descriptor := func(id, output string) capability.Descriptor {
		return capability.Descriptor{
			ID: id, Version: 1, Title: id, Description: id, Provider: "bookstack",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test-data"},
			InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(output),
		}
	}
	handler := func(answer map[string]any) capability.Handler {
		return func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error) {
			*calls++
			return answer, nil
		}
	}
	err := registry.Register("bookstack",
		capability.Operation{
			Descriptor: descriptor("bookstack.things.list", `{"type":"object","properties":{"things":{"type":"array",`+
				`"items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},`+
				`"note":{"type":"string"}}}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},`+
				`"required":["things","has_more"],"additionalProperties":false}`),
			Handler: handler(map[string]any{"things": []any{map[string]any{"id": 1, "name": "a", "note": "n"}},
				"has_more": true, "next_cursor": "c1"}),
		},
		capability.Operation{
			Descriptor: descriptor("bookstack.things.get", `{"type":"object","properties":{"id":{"type":"integer"}},`+
				`"required":["id"],"additionalProperties":false}`),
			Handler: handler(map[string]any{"id": 1}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

const fieldsWant = `{"has_more":true,"next_cursor":"c1","things":[{"id":1,"name":"a"}]}`

// --fields over the CLI and fields over MCP select the same members and leave the envelope of the result.
func TestFieldsSelectAlikeOverCLIAndMCP(t *testing.T) {
	config := bookstackConfig(t, "https://example.invalid")
	calls := 0
	registry := listRegistry(t, &calls)

	for _, args := range [][]string{{"--fields", "id,name"}, {"--fields", "id", "--fields", "name"}} {
		var stdout, stderr strings.Builder
		opts := testOptions(t, nil)
		opts.Input = strings.NewReader("")
		full := append([]string{"invoke", "bookstack.things.list", "--config", config}, args...)
		if code := run(newRootCommand(opts, registry), opts, full, &stdout, &stderr); code != exitOK {
			t.Fatalf("%v: exit=%d stderr=%q", args, code, stderr.String())
		}
		if _, _, _, result := invokeResult(t, stdout.String()); string(result) != fieldsWant {
			t.Fatalf("%v: CLI result = %s, want %s", args, result, fieldsWant)
		}
	}

	input := `{"jsonrpc":"2.0","id":"ok","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke",` +
		`"arguments":{"operation":"bookstack.things.list","fields":["id","name"]}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"bad","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke",` +
		`"arguments":{"operation":"bookstack.things.list","fields":["id","nam"]}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"none","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke",` +
		`"arguments":{"operation":"bookstack.things.get","fields":["id"]}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"describe","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.describe",` +
		`"arguments":{"operation":"bookstack.things.list"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"describe-get","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.describe",` +
		`"arguments":{"operation":"bookstack.things.get"}}}` + "\n"
	responses, _ := runMCPWithOptions(t, registry, input, &Options{Config: config, Redactor: &redact.Redactor{}})

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	ok := toolResultFrom(t, responses[`"ok"`])
	if ok.IsError {
		t.Fatalf("fields call failed: %s", ok.Content[0].Text)
	}
	decodeRaw(t, ok.Structured, &envelope)
	if string(envelope.Result) != fieldsWant {
		t.Fatalf("MCP result = %s, want %s", envelope.Result, fieldsWant)
	}
	for id, want := range map[string]string{
		`"bad"`:  `invalid-request: unknown field "nam" (did you mean "name"?); valid fields: id, name, note`,
		`"none"`: "invalid-request: fields is not supported: bookstack.things.get returns no result list",
	} {
		refused := toolResultFrom(t, responses[id])
		if !refused.IsError || refused.Content[0].Text != want {
			t.Errorf("%s = %+v, want %q", id, refused.Content, want)
		}
	}
	if !strings.Contains(toolResultFrom(t, responses[`"describe"`]).Content[0].Text,
		`"selectable_fields":["id","name","note"]`) ||
		strings.Contains(toolResultFrom(t, responses[`"describe-get"`]).Content[0].Text, "selectable_fields") {
		t.Errorf("describe does not name the selectable fields of the list tool alone")
	}
	if calls != 3 {
		t.Fatalf("handler ran %d times, want 3: the refused calls must not reach the provider", calls)
	}

	// The CLI refuses the same requests, before any provider, with the same message.
	calls = 0
	for _, tc := range []struct{ tool, fields, want string }{
		{"bookstack.things.list", "id,nam", `unknown field "nam"`},
		{"bookstack.things.get", "id", "returns no result list"},
	} {
		var stdout, stderr strings.Builder
		opts := testOptions(t, nil)
		opts.Input = strings.NewReader("")
		code := run(newRootCommand(opts, registry), opts,
			[]string{"invoke", tc.tool, "--config", config, "--fields", tc.fields}, &stdout, &stderr)
		if code == exitOK || !strings.Contains(stderr.String(), "invalid-request") || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%s: exit=%d stderr=%q", tc.tool, code, stderr.String())
		}
	}
	if calls != 0 {
		t.Fatalf("handler ran %d times for refused CLI requests", calls)
	}
}
