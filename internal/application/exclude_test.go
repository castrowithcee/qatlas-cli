package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// excludeCore offers one tool with a target argument to a connection that carries the given exclusions.
func excludeCore(t *testing.T, targets, exclude []string) *Core {
	t.Helper()
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	issues := testDescriptor("fake.issues.list", capability.EffectRead, capability.ConfirmationNone)
	issues.InputSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"repository":{"type":"string","x-form":"OWNER/REPO"}},"additionalProperties":false}`)
	issues.Arguments = []capability.Argument{{Name: "repository", Target: "repository"}}
	metadata := config.ProviderMetadata{ID: "fake", Name: "Fake", Target: config.TargetMetadata{
		Multiple: true, Wildcard: "*",
		Kinds: []config.TargetKind{{Name: "repository", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"},
			ExcludeForms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}}},
	}}
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(metadata, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("fake", capability.Operation{Descriptor: issues, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Services["service"] = config.Service{Provider: "fake", BaseURL: "https://example.invalid"}
	cfg.Credentials["shared"] = config.Credential{
		Type: config.CredentialTypeEnv, Values: map[string]string{"token": "FAKE_TOKEN"},
	}
	cfg.Connections["a"] = config.Connection{Service: "service", Credential: "shared", Targets: targets,
		Exclude: exclude, Effects: []config.Effect{config.EffectRead}}
	return New(registry, cfg, nil, nil)
}

func TestExcludeLeavesOfferAndTargetArgumentUnchanged(t *testing.T) {
	for name, targets := range map[string][]string{
		"fixed": {"repos/o/r"}, "owner wildcard": {"repos/o/*"}, "none": nil,
	} {
		t.Run(name, func(t *testing.T) {
			plain := excludeCore(t, targets, nil)
			with := excludeCore(t, targets, []string{"repos/o/x"})
			if got, want := targetOf(t, with, "fake.issues.list"), targetOf(t, plain, "fake.issues.list"); got != want {
				t.Errorf("target = %q, want %q", got, want)
			}
			a, errA := plain.Search(SearchRequest{})
			b, errB := with.Search(SearchRequest{})
			if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
				t.Errorf("search differs: %+v / %+v (%v, %v)", a, b, errA, errB)
			}
			ta, errA := plain.Tools(SearchRequest{})
			tb, errB := with.Tools(SearchRequest{})
			if errA != nil || errB != nil || !reflect.DeepEqual(ta, tb) {
				t.Errorf("tools differ (%v, %v)", errA, errB)
			}
		})
	}
}

func TestResolvedCarriesExclude(t *testing.T) {
	core := excludeCore(t, []string{"repos/o/*"}, []string{"repos/o/x"})
	resolved, err := core.connection("a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Exclude, []string{"repos/o/x"}) {
		t.Errorf("application Exclude = %v", resolved.Exclude)
	}
	again, err := core.config.Resolve("a", "")
	if err != nil || !reflect.DeepEqual(again.Exclude, resolved.Exclude) {
		t.Errorf("config Exclude = %v (%v)", again, err)
	}
}

func TestConnectionsListingNamesNoExclusion(t *testing.T) {
	core := excludeCore(t, []string{"repos/o/*"}, []string{"repos/o/hidden-object"})
	data, err := json.Marshal(core.Connections("", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hidden-object") {
		t.Errorf("the connections listing carries an exclusion: %s", data)
	}
}
