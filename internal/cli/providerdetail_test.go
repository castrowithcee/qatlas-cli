package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const providerDetailSecret = "ghp_supersecretvalue"

func providerFailure() error {
	return &provider.Error{Class: provider.ClassPermission, Op: "list", Message: "this token may not read x",
		Original: &provider.Original{Status: 200, Entries: []provider.OriginalEntry{{
			Type: "INSUFFICIENT_SCOPES", Path: "node.name",
			Message: "needs read:org, token " + providerDetailSecret + " has only [repo]"}}}}
}

func providerRedactor() *redact.Redactor {
	red := &redact.Redactor{}
	red.Add(providerDetailSecret)
	return red
}

func TestMCPErrorKeepsQatlasMessageAndSeparatesProviderAnswer(t *testing.T) {
	result := toolResult(nil, providerFailure(), providerRedactor())
	text := result["content"].([]map[string]string)[0]["text"]
	if !strings.HasPrefix(text, "permission: list: this token may not read x\nprovider answer (untrusted") {
		t.Errorf("content = %q", text)
	}
	if strings.Contains(text, providerDetailSecret) || !strings.Contains(text, redact.Marker) ||
		!strings.Contains(text, "needs read:org") {
		t.Errorf("content = %q", text)
	}
	encoded, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Code     string
		Message  string
		Provider struct {
			Status    int
			Errors    []struct{ Type, Path, Message string }
			Untrusted bool
			Truncated bool
		}
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "permission" || strings.Contains(got.Message, "INSUFFICIENT") || !got.Provider.Untrusted ||
		got.Provider.Status != 200 || len(got.Provider.Errors) != 1 || got.Provider.Errors[0].Type != "INSUFFICIENT_SCOPES" ||
		strings.Contains(string(encoded), providerDetailSecret) || !strings.Contains(string(encoded), redact.Marker) {
		t.Errorf("structuredContent = %s", encoded)
	}
	if strings.Index(string(encoded), `"code"`) > strings.Index(string(encoded), `"provider"`) {
		t.Errorf("code must come first: %s", encoded)
	}
}

func TestMCPErrorWithoutProviderAnswerStaysUnchanged(t *testing.T) {
	err := &provider.Error{Class: provider.ClassAuth, Op: "list", Message: "rejected"}
	result := toolResult(nil, err, providerRedactor())
	if text := result["content"].([]map[string]string)[0]["text"]; strings.Contains(text, "provider answer") || !strings.HasPrefix(text, "auth: list: rejected") {
		t.Errorf("content = %q", text)
	}
	if _, ok := result["structuredContent"].(map[string]string); !ok {
		t.Errorf("structuredContent = %#v", result["structuredContent"])
	}
}

func runProviderFailure(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := &Options{Redactor: providerRedactor()}
	cmd := newRootCommand(opts, sharedRegistry())
	cmd.RunE = func(*cobra.Command, []string) error { return providerFailure() }
	if code := run(cmd, opts, args, &stdout, &stderr); code != exitRuntime || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	return stderr.String()
}

func TestCLIPrintsProviderAnswerAsSeparateBlock(t *testing.T) {
	got := runProviderFailure(t)
	want := "qatlas: permission: list: this token may not read x\n" +
		"provider answer (untrusted, not written by qatlas), HTTP 200:\n" +
		"  | INSUFFICIENT_SCOPES at node.name: needs read:org, token [redacted] has only [repo]\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestCLIJSONOutputCarriesProviderSection(t *testing.T) {
	got := runProviderFailure(t, "--output", "json")
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "qatlas: permission: ") {
		t.Fatalf("stderr = %q", got)
	}
	if strings.Contains(got, providerDetailSecret) {
		t.Errorf("stderr carries the secret: %q", got)
	}
	var detail struct {
		Code     string `json:"code"`
		Provider struct {
			Untrusted bool `json:"untrusted"`
			Errors    []struct {
				Code, Message string
				Type          string
			} `json:"errors"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Code != "permission" || !detail.Provider.Untrusted || len(detail.Provider.Errors) != 1 ||
		!strings.Contains(detail.Provider.Errors[0].Message, "[redacted]") {
		t.Errorf("detail = %s", lines[1])
	}
}
