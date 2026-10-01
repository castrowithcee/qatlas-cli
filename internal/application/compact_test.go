package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestDropEmptyRemovesOnlyEmptyMembers(t *testing.T) {
	in := `{"a":null,"b":[],"c":{},"d":"","e":0,"f":false,"has_more":false,` +
		`"n":{"x":null,"y":{"z":[]}},"items":[{"k":null,"v":1},{"k":[]},null,[]],"keep":{"s":""}}`
	var value any
	if err := json.Unmarshal([]byte(in), &value); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(dropEmpty(value))
	want := `{"d":"","e":0,"f":false,"has_more":false,"items":[{"v":1},{},null,[]],"keep":{"s":""}}`
	if string(got) != want {
		t.Fatalf("dropEmpty = %s, want %s", got, want)
	}
	root, _ := json.Marshal(dropEmpty(map[string]any{"a": nil}))
	if string(root) != `{}` {
		t.Fatalf("root = %s", root)
	}
}

func TestPublishedOutputSchemaRelaxesOnlyRequiredMembersThatCanBeEmpty(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"n":{"type":"integer"},` +
		`"ok":{"type":"boolean"},"tags":{"type":"array","items":{"type":"string"}},` +
		`"min":{"type":"array","minItems":1,"items":{"type":"string"}},` +
		`"obj":{"type":"object","properties":{"a":{"type":"array"}},"required":["a"]},` +
		`"strict":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},` +
		`"rows":{"type":"array","items":{"type":"object","properties":{"x":{"type":"string"},"y":{"type":"array"}},"required":["x","y"]}}},` +
		`"required":["id","n","ok","tags","min","obj","strict","rows"],"additionalProperties":false}`)
	var got struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Required []string `json:"required"`
			Items    struct {
				Required []string `json:"required"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(PublishedOutputSchema(schema), &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "n", "ok", "min", "strict"}; !equalStrings(got.Required, want) {
		t.Fatalf("required = %v, want %v", got.Required, want)
	}
	if len(got.Properties["obj"].Required) != 0 || !equalStrings(got.Properties["strict"].Required, []string{"a"}) ||
		!equalStrings(got.Properties["rows"].Items.Required, []string{"x"}) {
		t.Fatalf("nested required = %+v", got.Properties)
	}
	// An unchanged schema is returned byte for byte, an unreadable one as it is.
	plain := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`)
	if string(PublishedOutputSchema(plain)) != string(plain) || string(PublishedOutputSchema(json.RawMessage(`{`))) != `{` {
		t.Fatal("schema changed without cause")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Invoke validates the provider's real answer against the complete schema, then drops empty values, and
// describe publishes a contract that does not require what may vanish. CLI and MCP share both paths.
func TestInvokeDropsEmptyValuesAfterValidation(t *testing.T) {
	registry := capability.NewRegistry()
	descriptor := testDescriptor("fake.pages.list", capability.EffectRead, capability.ConfirmationNone)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},` +
		`"labels":{"type":"array","items":{"type":"string"}},"has_more":{"type":"boolean"}},` +
		`"required":["title","labels","has_more"],"additionalProperties":false}`)
	answer := map[string]any{"title": "", "labels": []any{}, "has_more": false}
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		return answer, nil
	})
	if err := registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Services["fake"] = config.Service{Provider: "fake", BaseURL: "https://example.invalid"}
	cfg.Credentials["reader"] = config.Credential{Type: config.CredentialTypeKeyring}
	cfg.Connections["primary"] = config.Connection{Service: "fake", Credential: "reader"}
	core := New(registry, cfg, nil, nil)

	response, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.list",
		Arguments: json.RawMessage(`{"id":"7"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Result) != `{"has_more":false,"title":""}` {
		t.Fatalf("result = %s", response.Result)
	}
	described, err := core.Describe(DescribeRequest{Operation: "fake.pages.list"})
	if err != nil {
		t.Fatal(err)
	}
	var published struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(described.Operation.OutputSchema, &published); err != nil ||
		!equalStrings(published.Required, []string{"title", "has_more"}) {
		t.Fatalf("published required = %v (%v)", published.Required, err)
	}
	// The real answer is still validated against the complete schema: a missing required member fails.
	delete(answer, "labels")
	if _, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.list",
		Arguments: json.RawMessage(`{"id":"7"}`)}); err == nil {
		t.Fatal("an answer without a required member was accepted")
	}
}

func TestDropImpliedOnlyForAnExplicitFixingValue(t *testing.T) {
	descriptor := capability.Descriptor{
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"issues":{"type":"array","items":` +
			`{"type":"object","properties":{"state":{"type":"string"},"n":{"type":"integer"}},"required":["n","state"]}}}}`),
		ImpliedFields: []capability.ImpliedField{
			{List: "issues", Argument: "state", Values: []string{"open", "closed"}, Field: "state"},
		},
	}
	tests := []struct{ name, arguments, want string }{
		{"open", `{"state":"open"}`, `{"issues":[{"n":1}]}`},
		{"closed", `{"state":"closed"}`, `{"issues":[{"n":1}]}`},
		{"all", `{"state":"all"}`, `{"issues":[{"n":1,"state":"open"}]}`},
		{"unset", `{}`, `{"issues":[{"n":1,"state":"open"}]}`},
	}
	for _, tt := range tests {
		var value any
		_ = json.Unmarshal([]byte(`{"issues":[{"n":1,"state":"open"}]}`), &value)
		got, _ := json.Marshal(dropImplied(descriptor, json.RawMessage(tt.arguments), value))
		if string(got) != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
	if got := string(relaxImplied(descriptor)); !strings.Contains(got, `"required":["n"]`) {
		t.Errorf("published schema = %s, want state no longer required", got)
	}
}

func TestFieldsSelectOnlyEntriesOfTheResultList(t *testing.T) {
	calls := 0
	registry := capability.NewRegistry()
	descriptor := testDescriptor("fake.things.list", capability.EffectRead, capability.ConfirmationNone)
	descriptor.InputSchema = json.RawMessage(`{"type":"object"}`)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"things":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},` +
		`"labels":{"type":"array","items":{"type":"string"}},"has_more":{"type":"boolean"}}}`)
	two := descriptor
	two.ID = "fake.pairs.list"
	two.OutputSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"a":{"type":"array","items":{"type":"object","properties":{"x":{"type":"string"}}}},` +
		`"b":{"type":"array","items":{"type":"object","properties":{"y":{"type":"string"}}}}}}`)
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		calls++
		return map[string]any{"things": []any{map[string]any{"id": 1, "name": "n"}}, "labels": []any{"l"},
			"has_more": true, "a": []any{}, "b": []any{}}, nil
	})
	for _, d := range []capability.Descriptor{descriptor, two} {
		if err := registry.Register("fake", capability.Operation{Descriptor: d, Handler: handler}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.New()
	cfg.Services["fake"] = config.Service{Provider: "fake", BaseURL: "https://example.invalid"}
	cfg.Credentials["reader"] = config.Credential{Type: config.CredentialTypeKeyring}
	cfg.Connections["primary"] = config.Connection{Service: "fake", Credential: "reader"}
	core := New(registry, cfg, nil, nil)

	invoke := func(id string, fields []string) (string, error) {
		response, err := core.Invoke(context.Background(), InvokeRequest{Operation: id, Fields: fields})
		return string(response.Result), err
	}
	if got, err := invoke("fake.things.list", []string{"name"}); err != nil ||
		got != `{"has_more":true,"labels":["l"],"things":[{"name":"n"}]}` {
		t.Fatalf("selected = %s (%v)", got, err)
	}
	if got, _ := invoke("fake.things.list", nil); !strings.Contains(got, `"id":1`) {
		t.Fatalf("without fields = %s", got)
	}
	calls = 0
	for _, tt := range []struct {
		id     string
		fields []string
		want   string
	}{
		{"fake.things.list", []string{"nme"}, `unknown field "nme" (did you mean "name"?); valid fields: id, name`},
		{"fake.things.list", []string{}, "fields must name at least one field; valid fields: id, name"},
		{"fake.pairs.list", []string{"x"}, "fields is not supported: fake.pairs.list returns no result list"},
	} {
		_, err := invoke(tt.id, tt.fields)
		var invalid *InvalidRequestError
		if !errors.As(err, &invalid) || invalid.Message != tt.want {
			t.Errorf("%s %v: err = %v, want %q", tt.id, tt.fields, err, tt.want)
		}
	}
	if calls != 0 {
		t.Fatalf("provider ran %d times for refused selections", calls)
	}
	if got := Compact(descriptor).SelectableFields; !equalStrings(got, []string{"id", "name"}) ||
		Compact(two).SelectableFields != nil {
		t.Fatalf("selectable fields = %v", got)
	}
}
