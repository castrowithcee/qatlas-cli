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

// reachCore offers fake.pages.get everywhere and fake.pages.delete only to a connection bound to a drive.
func reachCore(t *testing.T, connections map[string][]string) *Core {
	t.Helper()
	handler := capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	metadata := config.ProviderMetadata{ID: "fake", Name: "Fake", Target: config.TargetMetadata{
		Multiple: true,
		Kinds: []config.TargetKind{
			{Name: "drive", Forms: []string{"drive/DRIVE_ID"}},
			{Name: "folder", Forms: []string{"folder/DRIVE_ID/FOLDER_ID"}},
		},
		Reaches: func(tool string, targets []string) (bool, []string) {
			if tool != "fake.pages.delete" {
				return true, nil
			}
			for _, target := range targets {
				if strings.HasPrefix(target, "drive/") {
					return true, nil
				}
			}
			// The last need is no form of a kind and a provider's mistake: it must not reach the hint.
			return false, []string{"drive/DRIVE_ID", "drive/canary-drive-77"}
		},
	}}
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(metadata, nil); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []capability.Descriptor{
		testDescriptor("fake.pages.get", capability.EffectRead, capability.ConfirmationNone),
		testDescriptor("fake.pages.delete", capability.EffectDelete, capability.ConfirmationNone),
	} {
		if err := registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.New(registry)
	cfg.Services["service"] = config.Service{Provider: "fake", BaseURL: "https://example.invalid"}
	cfg.Credentials["shared"] = config.Credential{
		Type: config.CredentialTypeEnv, Values: map[string]string{"token": "FAKE_TOKEN"},
	}
	for name, targets := range connections {
		cfg.Connections[name] = config.Connection{
			Service: "service", Credential: "shared", Targets: targets,
			Effects: []config.Effect{config.EffectRead, config.EffectDelete},
		}
	}
	env := func(name string) string {
		if name == "FAKE_TOKEN" {
			return "test-token"
		}
		return ""
	}
	redactor := &redact.Redactor{}
	return New(registry, cfg, secret.NewWith(env, nil, nil, redactor), redactor)
}

func reasonOf(t *testing.T, core *Core, request SearchRequest, id string) (config.Refusal, bool) {
	t.Helper()
	request.All = true
	got, err := core.Search(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range got.Operations {
		if hit.ID == id {
			return hit.Reason, true
		}
	}
	return "", false
}

func TestOutsideTargetsIsReportedOnEveryPath(t *testing.T) {
	core := reachCore(t, map[string][]string{"a": {"folder/d1/f1"}, "b": {"folder/d1/f2"}})
	if reason, _ := reasonOf(t, core, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalOutsideTargets {
		t.Errorf("search reason = %q, want outside-targets", reason)
	}
	if reason, _ := reasonOf(t, core, SearchRequest{Connection: "a"}, "fake.pages.delete"); reason != config.RefusalOutsideTargets {
		t.Errorf("search a reason = %q, want outside-targets", reason)
	}
	if reason, _ := reasonOf(t, core, SearchRequest{}, "fake.pages.get"); reason != "" {
		t.Errorf("get reason = %q, want offered", reason)
	}
	listed, err := core.Search(SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range listed.Operations {
		if hit.ID == "fake.pages.delete" {
			t.Errorf("delete is listed without all: %+v", hit)
		}
	}

	var unsupported *capability.UnsupportedError
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.delete", Connection: "a"})
	if !errors.As(err, &unsupported) || unsupported.Reason != config.RefusalOutsideTargets {
		t.Errorf("describe error = %v, want outside-targets", err)
	}
	_, err = core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.delete",
		Connection: "a", Arguments: json.RawMessage(`{"id":"7"}`)})
	unsupported = nil
	if !errors.As(err, &unsupported) || unsupported.Reason != config.RefusalOutsideTargets {
		t.Errorf("invoke error = %v, want unsupported-capability outside-targets", err)
	}
	// Without a connection no route is left to choose, as for every other reason.
	_, err = core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.delete",
		Arguments: json.RawMessage(`{"id":"7"}`)})
	var selection *ConnectionSelectionError
	if !errors.As(err, &selection) {
		t.Errorf("invoke without connection error = %v, want a connection selection error", err)
	}
}

func TestOutsideTargetsEndsWhenTheTargetsReachTheToolAndKeepsTheSelection(t *testing.T) {
	core := reachCore(t, map[string][]string{"a": {"folder/d1/f1"}})
	connection := core.all.Connections["a"]
	connection.Tools = []string{"fake.pages.get", "fake.pages.delete"}
	core.all.Connections["a"] = connection

	if reason, _ := reasonOf(t, core, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalOutsideTargets {
		t.Fatalf("reason = %q, want outside-targets", reason)
	}
	if got := core.all.Connections["a"].Tools; len(got) != 2 {
		t.Fatalf("tools = %v, want the selection kept", got)
	}
	connection.Targets = []string{"folder/d1/f1", "drive/d1"}
	core.all.Connections["a"] = connection
	if reason, found := reasonOf(t, core, SearchRequest{}, "fake.pages.delete"); !found || reason != "" {
		t.Fatalf("reason = %q, want delete offered once the targets reach it", reason)
	}
	if _, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.delete",
		Connection: "a", Arguments: json.RawMessage(`{"id":"7"}`)}); err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if got := core.all.Connections["a"].Tools; len(got) != 2 {
		t.Fatalf("tools = %v changed", got)
	}
}

func TestRefusalOrderAcrossConnections(t *testing.T) {
	// d: outside the targets; e: effect not permitted; off: disabled, and offering the tool otherwise.
	core := reachCore(t, map[string][]string{"d": {"folder/d1/f1"}, "e": {"drive/d1"}})
	narrow := core.all.Connections["e"]
	narrow.Effects = []config.Effect{config.EffectRead}
	core.all.Connections["e"] = narrow

	if reason, _ := reasonOf(t, core, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalEffect {
		t.Errorf("effect and outside-targets: reason = %q, want effect-not-permitted", reason)
	}
	off := core.all.Connections["d"]
	off.Targets = []string{"drive/d1"}
	core.all.Connections["off"] = off
	disable(core, "off")
	if reason, _ := reasonOf(t, core, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalDisabled {
		t.Errorf("with a disabled connection: reason = %q, want connection-disabled", reason)
	}
	// A disabled connection that the targets do not reach stays outside-targets, not disabled.
	core2 := reachCore(t, map[string][]string{"off": {"folder/d1/f1"}})
	disable(core2, "off")
	if reason, _ := reasonOf(t, core2, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalOutsideTargets {
		t.Errorf("disabled and outside: reason = %q, want outside-targets", reason)
	}
	only := reachCore(t, map[string][]string{"x": {"folder/d1/f1"}, "y": {"folder/d1/f2"}})
	if reason, _ := reasonOf(t, only, SearchRequest{}, "fake.pages.delete"); reason != config.RefusalOutsideTargets {
		t.Errorf("only outside-targets: reason = %q", reason)
	}
}

func TestOutsideTargetsCarriesNoTargetValue(t *testing.T) {
	core := reachCore(t, map[string][]string{"a": {"folder/secret-drive/secret-folder"}})
	got, err := core.Search(SearchRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	_, invokeErr := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.delete",
		Connection: "a", Arguments: json.RawMessage(`{"id":"7"}`)})
	encoded := string(mustJSON(t, got)) + invokeErr.Error()
	if strings.Contains(encoded, "secret-") || strings.Contains(encoded, "canary") {
		t.Errorf("answer carries a target value: %s", encoded)
	}
	metadata, _ := core.all.ProviderMetadata("fake")
	_, hint := metadata.Target.Reach("fake.pages.delete", []string{"folder/secret-drive/secret-folder"})
	if hint != "needs drive/DRIVE_ID" {
		t.Errorf("hint = %q, want only forms of kinds", hint)
	}
}

func TestIdleWarningNamesTargetsWhenTheyReachNoTool(t *testing.T) {
	core := reachCore(t, map[string][]string{"a": {"folder/d1/f1"}})
	connection := core.all.Connections["a"]
	connection.Effects = []config.Effect{config.EffectDelete}
	core.all.Connections["a"] = connection
	if warning := core.all.IdleWarning("a"); !strings.Contains(warning, "targets reach none") {
		t.Errorf("warning = %q", warning)
	}
}
