package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// waitRegistry holds one tool that waits and one that does not; the waiting one answers result and records
// the wait options its context carried.
func waitRegistry(t *testing.T, result string, got *capability.WaitOptions, carried *bool) *capability.Registry {
	t.Helper()
	risk := capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, DataSensitivity: "test"}
	schema := json.RawMessage(`{"type":"object"}`)
	registry := capability.NewRegistry()
	registerBookstackTestMetadata(t, registry)
	for _, descriptor := range []capability.Descriptor{
		{ID: "bookstack.pages.wait", Version: 1, Description: "Wait", Provider: "bookstack", Risk: risk, Waits: true,
			InputSchema: schema, OutputSchema: schema},
		{ID: "bookstack.pages.get", Version: 1, Description: "Read", Provider: "bookstack", Risk: risk,
			InputSchema: schema, OutputSchema: schema},
	} {
		handler := capability.Handler(func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
			_ *redact.Redactor, _ json.RawMessage) (any, error) {
			*got, *carried = capability.WaitFrom(ctx)
			return map[string]any{"result": result}, nil
		})
		if err := registry.Register("bookstack", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

func runWait(t *testing.T, registry *capability.Registry, flags ...string) (int, string, string) {
	t.Helper()
	cfg := writeConfig(t, validConfig)
	var stdout, stderr bytes.Buffer
	redactor := &redact.Redactor{}
	opts := &Options{Input: strings.NewReader(""), Redactor: redactor, Secrets: secret.NewWith(nil, nil, nil, redactor)}
	code := run(newRootCommand(opts, registry), opts, append([]string{"wait", "--config", cfg}, flags...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestWaitExitCodeNamesHowTheWaitEnded(t *testing.T) {
	t.Setenv("QCONNECT_CONFIG", "")
	t.Setenv("QCONNECT_HOME", "")
	for result, want := range map[string]int{"pass": exitOK, "fail": 3, "timeout": 4, "none": 5, "head-changed": 6,
		"pending": exitRuntime, "other": exitRuntime} {
		var got capability.WaitOptions
		var carried bool
		code, stdout, stderr := runWait(t, waitRegistry(t, result, &got, &carried), "bookstack.pages.wait",
			"--timeout", "30m", "--interval", "10s")
		if code != want || !strings.HasSuffix(stdout, `"result":{"result":"`+result+`"}}}`+"\n") {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q, want exit %d and the result on stdout", result, code, stdout, stderr, want)
		}
		if !carried || got.Limit != 30*time.Minute || got.Interval != 10*time.Second {
			t.Errorf("%s: the tool saw %+v (carried %v), want the flags of the command", result, got, carried)
		}
	}
}

func TestWaitKeepsTheInvokeLimitAndRefusesWhatItCannotRun(t *testing.T) {
	t.Setenv("QCONNECT_CONFIG", "")
	t.Setenv("QCONNECT_HOME", "")
	if invokeTimeout != 60*time.Second {
		t.Fatalf("invokeTimeout = %s, want 60s", invokeTimeout)
	}
	var got capability.WaitOptions
	var carried bool
	registry := waitRegistry(t, "pass", &got, &carried)

	// A tool called without the command, as the broker does, carries no wait options.
	for _, flags := range [][]string{
		{"bookstack.pages.get"},
		{"bookstack.pages.wait", "--timeout", "3h"},
		{"bookstack.pages.wait", "--timeout", "0s"},
		{"bookstack.pages.wait", "--interval", "1s"},
		{"bookstack.pages.wait", "--output", "table"},
	} {
		code, stdout, _ := runWait(t, registry, flags...)
		if code != exitUsage || stdout != "" || carried {
			t.Errorf("%v: exit=%d stdout=%q carried=%v, want a usage error before the tool runs", flags, code, stdout, carried)
		}
	}
	// The defaults are the documented ones.
	if code, _, _ := runWait(t, registry, "bookstack.pages.wait"); code != exitOK ||
		got.Limit != 45*time.Minute || got.Interval != 20*time.Second {
		t.Errorf("defaults: exit=%d options=%+v, want 45m and 20s", code, got)
	}
}
