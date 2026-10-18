package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const requestEnvelopeBytes = 64 << 10

// stringMaxLengths collects every string maxLength of a schema, keyed by its path.
func stringMaxLengths(node any, path string, found map[string]int) {
	switch value := node.(type) {
	case map[string]any:
		// maxLength only constrains strings, so this also covers type unions such as ["string","null"].
		if limit, ok := value["maxLength"].(float64); ok {
			found[path] = int(limit)
		}
		for key, child := range value {
			stringMaxLengths(child, path+"/"+key, found)
		}
	case []any:
		for i, child := range value {
			stringMaxLengths(child, fmt.Sprintf("%s/%d", path, i), found)
		}
	}
}

// No schema may promise a single string the transport cannot carry.
func TestSchemaMaxLengthsFitTheRequestLimit(t *testing.T) {
	for _, d := range defaultRegistry().All() {
		var schema any
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			t.Fatalf("%s: input schema: %v", d.ID, err)
		}
		found := map[string]int{}
		stringMaxLengths(schema, "", found)
		paths := make([]string, 0, len(found))
		for path := range found {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			if found[path]+requestEnvelopeBytes >= maxRequestBytes {
				t.Errorf("%s: maxLength %d at %q plus %d envelope bytes reaches the %d byte request limit",
					d.ID, found[path], path, requestEnvelopeBytes, maxRequestBytes)
			}
		}
	}
}

// uploadFixture registers the shipped input schema of the Nextcloud file creation under a fake provider
// whose handler counts its calls.
func uploadFixture(t *testing.T, calls *int) (*capability.Registry, string) {
	t.Helper()
	shipped, _, ok := defaultRegistry().Lookup("nextcloud.files.files.create")
	if !ok {
		t.Fatal("nextcloud.files.files.create is not registered")
	}
	registry, path := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		*calls++
		return map[string]any{}, nil
	})
	d, handler, _ := registry.Lookup("fake.pages.get")
	d.ID = "fake.files.create"
	d.InputSchema = shipped.InputSchema
	registry = capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{ID: "fake", Name: "Fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("fake", capability.Operation{Descriptor: d, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	return registry, path
}

func uploadArguments(t *testing.T, length int) string {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			MaxLength int `json:"maxLength"`
		} `json:"properties"`
	}
	shipped, _, _ := defaultRegistry().Lookup("nextcloud.files.files.create")
	if err := json.Unmarshal(shipped.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties["content_base64"].MaxLength; got != 5592408 {
		t.Fatalf("content_base64 maxLength = %d, want 5592408", got)
	}
	return `{"path":"a.bin","content_base64":"` + strings.Repeat("A", length) + `"}`
}

func TestInlineContentAtSchemaLimitReachesTheHandlerOverStdinAndMCP(t *testing.T) {
	const limit = 5592408
	var calls int
	registry, path := uploadFixture(t, &calls)
	atLimit, over := uploadArguments(t, limit), uploadArguments(t, limit+1)

	invoke := func(arguments string) (int, string) {
		var stdout, stderr bytes.Buffer
		opts := &Options{Input: strings.NewReader(arguments), Redactor: &redact.Redactor{},
			Secrets: secret.NewWith(nil, nil, nil, nil)}
		code := run(newRootCommand(opts, registry), opts,
			[]string{"invoke", "fake.files.create", "--config", path}, &stdout, &stderr)
		return code, stderr.String()
	}
	if code, stderr := invoke(atLimit); code != exitOK || calls != 1 {
		t.Fatalf("stdin at limit: exit=%d calls=%d stderr=%q", code, calls, stderr)
	}
	if code, stderr := invoke(over); code != exitUsage || calls != 1 ||
		!strings.HasPrefix(stderr, "qatlas: invalid-request:") {
		t.Fatalf("stdin over limit: exit=%d calls=%d stderr=%.200q", code, calls, stderr)
	}

	call := func(id, arguments string) string {
		return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"fake.files.create","connection":"primary","arguments":` +
			arguments + `}}}` + "\n"
	}
	responses, _ := runMCPWithOptions(t, registry, call("1", atLimit)+call("2", over), &Options{
		Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil)})
	if result := toolResultFrom(t, responses["1"]); result.IsError || calls != 2 {
		t.Fatalf("MCP at limit: result=%+v calls=%d", result, calls)
	}
	if result := toolResultFrom(t, responses["2"]); !result.IsError || calls != 2 ||
		!strings.HasPrefix(result.Content[0].Text, "invalid-request:") {
		t.Fatalf("MCP over limit: result=%.200v calls=%d", result, calls)
	}
}
