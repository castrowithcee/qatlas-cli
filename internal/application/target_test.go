package application

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

// targetCore offers one tool whose optional argument selects a target of the repository kind, and one
// whose optional argument selects the table of a provider without kinds, to connections with the given
// targets.
func targetCore(t *testing.T, kinds bool, connections map[string][]string) *Core {
	t.Helper()
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	issues := testDescriptor("fake.issues.list", capability.EffectRead, capability.ConfirmationNone)
	issues.InputSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"repository":{"type":"string","x-form":"OWNER/REPO"}},"additionalProperties":false}`)
	issues.Arguments = []capability.Argument{{Name: "repository", Target: "repository"}}
	rows := testDescriptor("fake.rows.list", capability.EffectRead, capability.ConfirmationNone)
	rows.InputSchema = json.RawMessage(`{"type":"object","properties":{"table":{"type":"string"}},` +
		`"additionalProperties":false}`)
	rows.Arguments = []capability.Argument{{Name: "table", Target: "*"}}
	other := testDescriptor("fake.pages.get", capability.EffectRead, capability.ConfirmationNone)
	metadata := config.ProviderMetadata{ID: "fake", Name: "Fake", Target: config.TargetMetadata{
		Multiple: true, Wildcard: "*",
	}}
	descriptors := []capability.Descriptor{rows, other}
	if kinds {
		metadata.Target.Kinds = []config.TargetKind{{
			Name: "repository", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"},
		}, {
			Name: "project", Forms: []string{"orgs/LOGIN/projects/NUMBER", "orgs/LOGIN/projects/*"},
		}}
		descriptors = []capability.Descriptor{issues, other}
	}
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(metadata, nil); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		if err := registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.New()
	cfg.Services["service"] = config.Service{Provider: "fake", BaseURL: "https://example.invalid"}
	cfg.Credentials["shared"] = config.Credential{
		Type: config.CredentialTypeEnv, Values: map[string]string{"token": "FAKE_TOKEN"},
	}
	for name, targets := range connections {
		cfg.Connections[name] = config.Connection{
			Service: "service", Credential: "shared", Targets: targets,
			Permissions: []config.Permission{config.PermissionRead},
		}
	}
	return New(registry, cfg, nil, nil)
}

func targetOf(t *testing.T, core *Core, id string) string {
	t.Helper()
	got, err := core.Search(SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range got.Operations {
		if hit.ID == id {
			return hit.Target
		}
	}
	t.Fatalf("no hit %s in %+v", id, got.Operations)
	return ""
}

func TestSearchHitNamesTheTargetArgumentOnlyWhereNoConnectionFixesIt(t *testing.T) {
	const open = "repository:OWNER/REPO"
	for name, c := range map[string]struct {
		connections map[string][]string
		want        string
	}{
		"no target":                  {map[string][]string{"a": nil}, open},
		"exactly one repository":     {map[string][]string{"a": {"repos/o/r"}}, ""},
		"all repositories of owner":  {map[string][]string{"a": {"repos/o/*"}}, open},
		"one repository and project": {map[string][]string{"a": {"repos/o/r", "orgs/o/projects/1"}}, ""},
		"two repositories":           {map[string][]string{"a": {"repos/o/r", "repos/o/s"}}, open},
		"one repository and an owner wildcard": {map[string][]string{"a": {"repos/o/r", "repos/o/*"}},
			open},
		"a project only":         {map[string][]string{"a": {"orgs/o/projects/1"}}, open},
		"one fixing, one open":   {map[string][]string{"a": {"repos/o/r"}, "b": nil}, open},
		"every connection fixes": {map[string][]string{"a": {"repos/o/r"}, "b": {"repos/p/q"}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := targetOf(t, targetCore(t, true, c.connections), "fake.issues.list"); got != c.want {
				t.Errorf("target = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSearchHitTargetOfAProviderWithoutKinds(t *testing.T) {
	for name, c := range map[string]struct {
		targets []string
		want    string
	}{
		"no target":    {nil, "table"},
		"one table":    {[]string{"People"}, ""},
		"the wildcard": {[]string{"*"}, "table"},
		"two tables":   {[]string{"People", "Orders"}, "table"},
	} {
		t.Run(name, func(t *testing.T) {
			core := targetCore(t, false, map[string][]string{"a": c.targets})
			if got := targetOf(t, core, "fake.rows.list"); got != c.want {
				t.Errorf("target = %q, want %q", got, c.want)
			}
		})
	}
}

// The hit names the argument and its form, never a value a connection holds, and a tool without a target
// argument carries none.
func TestSearchHitTargetCarriesNoTargetValue(t *testing.T) {
	core := targetCore(t, true, map[string][]string{"a": {"repos/secret-owner/secret-repo", "repos/x/*"}})
	got, err := core.Search(SearchRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range [][]byte{mustJSON(t, got), mustJSON(t, got.TOONPayload())} {
		if strings.Contains(string(encoded), "secret-") || strings.Contains(string(encoded), "repos/") {
			t.Errorf("answer carries a target value: %s", encoded)
		}
	}
	for _, hit := range got.Operations {
		if hit.ID == "fake.pages.get" && hit.Target != "" {
			t.Errorf("%s target = %q, want none", hit.ID, hit.Target)
		}
	}
	// The field follows requires, and TOON prints it for every entry once any entry has it.
	if encoded := string(mustJSON(t, got.TOONPayload())); strings.Count(encoded, `"target"`) != len(got.Operations) {
		t.Errorf("table view = %s, want target in every entry", encoded)
	}
	if encoded := string(mustJSON(t, got)); strings.Count(encoded, `"target"`) != 1 {
		t.Errorf("list view = %s, want target only in the entry that has one", encoded)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// The target column sits directly after requires in the field order.
func TestSearchHitTargetFollowsRequires(t *testing.T) {
	encoded := string(mustJSON(t, Hits{{ID: "a.b", Title: "t", Effect: capability.EffectRead, Requires: "id",
		Target: "repository:OWNER/REPO", Confirm: true}}))
	if !strings.Contains(encoded, `"requires":"id","target":"repository:OWNER/REPO","confirm":true`) {
		t.Errorf("hit = %s", encoded)
	}
}
