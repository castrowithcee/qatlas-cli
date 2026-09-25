package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestOversizedInvokeStopsBeforeHandler(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, validConfig)

	calls := 0
	descriptor := capability.Descriptor{
		ID: "bookstack.pages.get", Version: 1, Description: "Read one page", Provider: "bookstack",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
		},
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		calls++
		return map[string]any{}, nil
	})
	registry := capability.NewRegistry()
	registerBookstackTestMetadata(t, registry)
	if err := registry.Register("bookstack", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}

	base := `{"id":1}`
	input := base + strings.Repeat(" ", maxAgentRequestBytes-len(base)+1)
	var stdout, stderr bytes.Buffer
	opts := &Options{Input: strings.NewReader(input)}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "bookstack.pages.get", "--config", cfg}, &stdout, &stderr)

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "qatlas: invalid-request:") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if calls != 0 {
		t.Errorf("handler calls = %d, want 0", calls)
	}
}

// Invoke writes its result as JSON only: --output json and --agent are accepted, any other format is
// refused before the arguments are read or a provider is called, instead of being ignored.
func TestInvokeRefusesAnOutputFormatItDoesNotWrite(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, validConfig)

	calls := 0
	descriptor := capability.Descriptor{
		ID: "bookstack.pages.get", Version: 1, Description: "Read one page", Provider: "bookstack",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
		},
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		calls++
		return map[string]any{"id": 1}, nil
	})
	registry := capability.NewRegistry()
	registerBookstackTestMetadata(t, registry)
	if err := registry.Register("bookstack", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	invoke := func(flags ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		redactor := &redact.Redactor{}
		opts := &Options{Input: strings.NewReader(""), Redactor: redactor,
			Secrets: secret.NewWith(nil, nil, nil, redactor)}
		code := run(newRootCommand(opts, registry), opts,
			append([]string{"invoke", "bookstack.pages.get", "--config", cfg}, flags...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	for _, format := range []string{"table", "compact", "toon"} {
		code, stdout, stderr := invoke("--output", format)
		want := "qatlas: usage: 'qatlas invoke' writes its result as json, not " + format +
			"; omit --output or pass --output json\n"
		if code != exitUsage || stdout != "" || stderr != want {
			t.Errorf("--output %s: exit=%d stdout=%q stderr=%q, want %q", format, code, stdout, stderr, want)
		}
	}
	if calls != 0 {
		t.Fatalf("handler calls = %d, want 0", calls)
	}
	for _, flags := range [][]string{nil, {"--output", "json"}, {"--agent"}} {
		code, stdout, stderr := invoke(flags...)
		if code != exitOK || !strings.HasSuffix(stdout, `"result":{"id":1}}}`+"\n") || stderr != "" {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", flags, code, stdout, stderr)
		}
	}
}

func TestInvokeWithoutMatchingConnectionIsASelectionError(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, "version: 1\n")

	calls := 0
	descriptor := capability.Descriptor{
		ID: "bookstack.pages.get", Version: 1, Description: "Read one page", Provider: "bookstack",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
		},
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		calls++
		return map[string]any{}, nil
	})
	registry := capability.NewRegistry()
	registerBookstackTestMetadata(t, registry)
	if err := registry.Register("bookstack", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	opts := &Options{Input: strings.NewReader("")}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "bookstack.pages.get", "--config", cfg}, &stdout, &stderr)

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	first, _, _ := strings.Cut(stderr.String(), "\n")
	if !strings.HasPrefix(first, "qatlas: connection-selection: no configured connection can invoke tool") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if strings.Contains(first, "--connection") {
		t.Errorf("diagnostic contains an obsolete flag hint: %q", first)
	}
	if calls != 0 {
		t.Errorf("handler calls = %d, want 0", calls)
	}
}

func TestConfirmedMutationWritesMinimalAuditToStderr(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, `version: 1
services:
  telegram:
    provider: fake
    base_url: https://example.invalid
credentials:
  bot:
    type: keyring
connections:
  alerts:
    service: telegram
    credential: bot
    target: "-1001"
defaults: {}
`)
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{
		ID: "fake", Name: "Fake", DefaultPermissions: []config.Permission{config.PermissionCreate},
	}, nil); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: "fake.messages.send", Version: 1, Description: "Send one message", Provider: "fake",
		RequiresExplicitConnection: true,
		Risk: capability.Risk{
			Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "message",
		},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`),
	}
	if err := registry.Register("fake", capability.Operation{
		Descriptor: descriptor,
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return map[string]any{"message_id": int64(7), "date": int64(1787220000)}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	opts := &Options{Input: strings.NewReader(`{"text":"private message"}`), Redactor: &redact.Redactor{}}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "fake.messages.send", "--connection", "alerts", "--confirm", "--config", cfg},
		&stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"message_id":7`) || !strings.Contains(stdout.String(), `"date":1787220000`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &event); err != nil {
		t.Fatalf("stderr is not one audit JSON event: %q: %v", stderr.String(), err)
	}
	for _, key := range []string{"request_id", "operation", "connection", "confirmed", "result", "time"} {
		if _, ok := event[key]; !ok {
			t.Errorf("audit is missing %q: %#v", key, event)
		}
	}
	for _, forbidden := range []string{"private message", "-1001", "message_id", "arguments", "token", "header"} {
		if strings.Contains(stderr.String(), forbidden) {
			t.Errorf("audit contains forbidden value %q: %s", forbidden, stderr.String())
		}
	}
}

func TestConfirmedProviderFailureKeepsCodeFirstAndAuditLast(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, `version: 1
services:
  telegram:
    provider: fake
    base_url: https://example.invalid
credentials:
  bot:
    type: keyring
connections:
  alerts:
    service: telegram
    credential: bot
    target: "-1001"
defaults: {}
`)
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{
		ID: "fake", Name: "Fake", DefaultPermissions: []config.Permission{config.PermissionCreate},
	}, nil); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: "fake.messages.send", Version: 1, Description: "Send one message", Provider: "fake",
		RequiresExplicitConnection: true,
		Risk: capability.Risk{
			Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "message",
		},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	const canary = "provider-body-canary-8c21"
	if err := registry.Register("fake", capability.Operation{
		Descriptor: descriptor,
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return nil, &provider.Error{
				Class: provider.ClassPermission, Op: "send message", Message: "private " + canary,
			}
		},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	redactor := &redact.Redactor{}
	redactor.Add(canary)
	opts := &Options{Input: strings.NewReader(`{"text":"private message"}`), Redactor: redactor}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "fake.messages.send", "--connection", "alerts", "--confirm", "--config", cfg},
		&stdout, &stderr)
	if code != exitRuntime || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "qatlas: permission: send message: private [redacted]") {
		t.Fatalf("stderr lines = %#v", lines)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &event); err != nil {
		t.Fatalf("audit line = %q: %v", lines[1], err)
	}
	if event["result"] != string(provider.ClassPermission) || event["operation"] != descriptor.ID ||
		event["connection"] != "alerts" || event["confirmed"] != true {
		t.Fatalf("audit event = %#v", event)
	}
	for _, forbidden := range []string{"private message", "-1001", canary, "arguments", "header", "provider body"} {
		if strings.Contains(lines[1], forbidden) {
			t.Errorf("audit contains forbidden value %q: %s", forbidden, lines[1])
		}
	}
}

func TestExplicitConnectionDiagnosticStopsBeforeSecretsAndAudit(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, `version: 1
services:
  telegram:
    provider: fake
    base_url: https://example.invalid
credentials:
  bot:
    type: env
    values:
      token: FAKE_BOT_TOKEN
connections:
  alerts:
    service: telegram
    credential: bot
    target: "-1001"
defaults:
  connections:
    fake: alerts
`)
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{
		ID: "fake", Name: "Fake", SecretRoles: []config.SecretRole{{Name: "token"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: "fake.messages.send", Version: 1, Description: "Send one message", Provider: "fake",
		RequiresExplicitConnection: true,
		Risk: capability.Risk{
			Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "message",
		},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	handlerCalls := 0
	if err := registry.Register("fake", capability.Operation{
		Descriptor: descriptor,
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			handlerCalls++
			return map[string]any{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	secretReads := 0
	resolver := secret.NewWith(func(string) string { secretReads++; return "canary-token" }, nil, nil, nil)
	var stdout, stderr bytes.Buffer
	opts := &Options{
		Input: strings.NewReader(`{"text":"private message"}`), Redactor: &redact.Redactor{},
		Secrets: resolver,
	}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "fake.messages.send", "--confirm", "--config", cfg}, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	first, _, _ := strings.Cut(stderr.String(), "\n")
	want := `qatlas: connection-selection: tool "fake.messages.send" requires an explicit connection in this invoke request`
	if first != want {
		t.Fatalf("first stderr line = %q, want %q", first, want)
	}
	if handlerCalls != 0 || secretReads != 0 || strings.Contains(stderr.String(), `"request_id"`) {
		t.Fatalf("handler=%d secrets=%d stderr=%q", handlerCalls, secretReads, stderr.String())
	}
}

// --arg is the flat way to call a tool: one flag per argument, typed by the input schema, so a caller
// never has to write JSON for a plain request.
func TestArgumentFlagsAreTypedByTheSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"text":{"type":"string"},"limit":{"type":"integer"},"ratio":{"type":"number"},
		"draft":{"type":"boolean"},"tags":{"type":"array"},"maybe":{"type":["string","null"]}}}`)

	t.Run("values take the type their schema declares", func(t *testing.T) {
		got, err := argumentsFromFlags(schema, []string{
			"text=Deployment finished", "limit=10", "ratio=0.5", "draft=true", `tags=["a","b"]`,
			"maybe=7",
		})
		if err != nil {
			t.Fatalf("argumentsFromFlags() = %v", err)
		}
		// A union type has no single answer, so "maybe" stays the text that was typed.
		want := `{"draft":true,"limit":10,"maybe":"7","ratio":0.5,"tags":["a","b"],"text":"Deployment finished"}`
		if string(got) != want {
			t.Errorf("arguments = %s, want %s", got, want)
		}
	})

	t.Run("an unknown name stays a string for the core to reject", func(t *testing.T) {
		got, err := argumentsFromFlags(schema, []string{"invented=1"})
		if err != nil || string(got) != `{"invented":"1"}` {
			t.Errorf("arguments = %s, %v", got, err)
		}
	})

	refusals := []struct{ name, arg, want string }{
		{"no equals sign", "text", "must be written as name=value"},
		{"empty name", "=value", "must be written as name=value"},
		{"a word where a whole number belongs", "limit=many", "must be a whole number"},
		{"a fraction where a whole number belongs", "limit=1.5", "must be a whole number"},
		{"a word where a number belongs", "ratio=some", "must be a number"},
		{"a word where a boolean belongs", "draft=perhaps", "must be true or false"},
		{"broken JSON for a list", "tags=a,b",
			`takes JSON, for example --arg 'tags=["value"]', or pass the whole arguments object on stdin`},
	}
	for _, tt := range refusals {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			_, err := argumentsFromFlags(schema, []string{tt.arg})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("argumentsFromFlags(%q) = %v, want %q", tt.arg, err, tt.want)
			}
		})
	}

	t.Run("the same argument twice is refused", func(t *testing.T) {
		_, err := argumentsFromFlags(schema, []string{"text=one", "text=two"})
		if err == nil || !strings.Contains(err.Error(), "given more than once") {
			t.Errorf("argumentsFromFlags(repeated) = %v", err)
		}
	})
}

// --arg is the whole request: stdin is not read, so an agent that leaves its stdin open, or a shell with a
// terminal on it, never waits for an end of input nobody sends.
func TestArgumentFlagsLeaveStdinUnread(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	shortenInvokeTimeout(t, 2*time.Second)
	cfg := writeConfig(t, validConfig)

	for name, input := range map[string]io.Reader{
		"an open stdin":        blockingReader(t),
		"an object on stdin":   strings.NewReader(`{"limit":"not a number"}`),
		"a pipe nobody closes": openPipe(t),
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			redactor := &redact.Redactor{}
			opts := &Options{Input: input, Redactor: redactor, Secrets: secret.NewWith(nil, nil, nil, redactor)}
			start := time.Now()
			code := run(newRootCommand(opts, fakeRegistry(t)), opts,
				[]string{"invoke", "bookstack.pages.list", "--arg", "limit=1", "--config", cfg}, &stdout, &stderr)

			if code != exitOK || !strings.Contains(stdout.String(), `"result":[`) || stderr.Len() != 0 {
				t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("invoke took %s, want it not to wait for stdin", elapsed)
			}
		})
	}
}

// Without --arg, stdin is read, but only until the invoke's time limit: an input that never ends is a
// timeout that names the way out, not a wait without end.
func TestStdinThatNeverEndsTimesOut(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	shortenInvokeTimeout(t, 50*time.Millisecond)
	cfg := writeConfig(t, validConfig)

	for name, input := range map[string]io.Reader{
		"a reader":             blockingReader(t),
		"a pipe nobody closes": openPipe(t),
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			opts := &Options{Input: input, Redactor: &redact.Redactor{}}
			start := time.Now()
			code := run(newRootCommand(opts, fakeRegistry(t)), opts,
				[]string{"invoke", "bookstack.pages.list", "--config", cfg}, &stdout, &stderr)

			want := "qatlas: timeout: stdin did not end within 50ms; close it after the JSON arguments object, " +
				"or pass the arguments with --arg\n"
			if code != exitRuntime || stdout.Len() != 0 || stderr.String() != want {
				t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("invoke took %s, want it to end at the limit", elapsed)
			}
		})
	}
}

func TestOnlyATerminalCountsAsInteractive(t *testing.T) {
	if terminal(strings.NewReader("")) || terminal(openPipe(t)) {
		t.Error("a reader or a pipe counts as a terminal, want it read")
	}
}

// A provider that does not answer ends with the invoke's limit, as a timeout with the next step.
func TestInvokeEndsAtItsLimitWithTheNextStep(t *testing.T) {
	shortenInvokeTimeout(t, 50*time.Millisecond)
	for name, tt := range map[string]struct {
		fail func(ctx context.Context) error
		want string
	}{
		"a provider that did not answer": {
			fail: func(ctx context.Context) error { return provider.Transport("get page", "Fake", ctx.Err()) },
			want: "qatlas: timeout: get page: Fake did not answer in time; check that the service answers, " +
				"then try again\n",
		},
		"a bare context error": {
			fail: func(ctx context.Context) error { return ctx.Err() },
			want: "qatlas: timeout: the request did not finish within 50ms; check that the service answers, " +
				"then try again\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			registry, path := mcpTestRegistry(t, func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
				_ *redact.Redactor, _ json.RawMessage) (any, error) {
				<-ctx.Done()
				return nil, tt.fail(ctx)
			})
			var stdout, stderr bytes.Buffer
			opts := &Options{Input: strings.NewReader(""), Redactor: &redact.Redactor{},
				Secrets: secret.NewWith(nil, nil, nil, nil)}
			code := run(newRootCommand(opts, registry), opts,
				[]string{"invoke", "fake.pages.get", "--connection", "primary", "--config", path}, &stdout, &stderr)

			if code != exitRuntime || stdout.Len() != 0 || stderr.String() != tt.want {
				t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

// A keyring that waits on an unlock prompt until the invoke's limit ends it is a timeout in the CLI and in
// MCP alike, and both keep the message that names the way out instead of a bare deadline.
func TestKeyringThatNeverAnswersTimesOutWithTheWayOut(t *testing.T) {
	registry, path := mcpTestRegistry(t, func(ctx context.Context, resolved *config.Resolved,
		secrets *secret.Resolver, _ *redact.Redactor, _ json.RawMessage) (any, error) {
		_, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, "token")
		return nil, err
	})
	stuck := func() *secret.Resolver { return secret.NewWith(nil, stuckStore{}, nil, nil) }
	env := secret.DerivedEnvName("reader", "token")

	t.Run("CLI", func(t *testing.T) {
		shortenInvokeTimeout(t, 50*time.Millisecond)
		var stdout, stderr bytes.Buffer
		opts := &Options{Input: strings.NewReader(""), Redactor: &redact.Redactor{}, Secrets: stuck()}
		code := run(newRootCommand(opts, registry), opts,
			[]string{"invoke", "fake.pages.get", "--connection", "primary", "--config", path}, &stdout, &stderr)

		if code != exitRuntime || !strings.HasPrefix(stderr.String(), "qatlas: timeout: credentials.reader") ||
			!strings.Contains(stderr.String(), "credential store (timed out)") ||
			!strings.Contains(stderr.String(), "export "+env+" for this session") {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("MCP", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		server := newMCPServer(&Options{Config: path, Redactor: &redact.Redactor{}, Secrets: stuck()},
			registry, &stdout, &stderr)
		server.timeout = 50 * time.Millisecond
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","connection":"primary","arguments":{}}}}` + "\n"
		if err := server.serve(context.Background(), strings.NewReader(input)); err != nil {
			t.Fatal(err)
		}
		result := toolResultFrom(t, decodeMCPResponses(t, stdout.String())["1"])
		var detail struct{ Code, Message string }
		decodeRaw(t, result.Structured, &detail)
		if !result.IsError || detail.Code != "timeout" ||
			!strings.Contains(detail.Message, "credential store (timed out)") ||
			!strings.Contains(detail.Message, "export "+env+" for this session") ||
			result.Content[0].Text != "timeout: "+detail.Message {
			t.Errorf("result = %+v, detail = %+v", result, detail)
		}
	})
}

// stuckStore is a keyring waiting on an unlock prompt nobody answers: it holds every read until the
// request ends, the way the platform store's own deadline ends it.
type stuckStore struct{}

func (stuckStore) Get(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", fmt.Errorf("%w: %w", secret.ErrUnavailable, secret.ErrTimedOut)
}
func (stuckStore) Set(string, string) error { return secret.ErrUnavailable }
func (stuckStore) Delete(string) error      { return secret.ErrUnavailable }

func shortenInvokeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := invokeTimeout
	invokeTimeout = d
	t.Cleanup(func() { invokeTimeout = previous })
}

// blockingReader is a stdin that never delivers and never ends until the test is over.
func blockingReader(t *testing.T) io.Reader {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return readerFunc(func([]byte) (int, error) {
		<-release
		return 0, io.EOF
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// openPipe is an operating system pipe whose writer stays open until the test is over, as an agent's stdin
// can be.
func openPipe(t *testing.T) *os.File {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
	return reader
}
