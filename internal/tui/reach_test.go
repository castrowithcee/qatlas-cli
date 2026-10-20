package tui

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

func reachRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	metadata := config.ProviderMetadata{ID: "fake", Name: "Fake", Target: config.TargetMetadata{
		Multiple: true,
		Kinds: []config.TargetKind{
			{Name: "drive", Forms: []string{"drive/DRIVE_ID"}},
			{Name: "folder", Forms: []string{"folder/DRIVE_ID/FOLDER_ID"}},
		},
		Reaches: func(tool string, targets []string) (bool, []string) {
			if tool != "fake.trash.list" {
				return true, nil
			}
			for _, target := range targets {
				if strings.HasPrefix(target, "drive/") {
					return true, nil
				}
			}
			return false, []string{"drive/DRIVE_ID"}
		},
	}}
	if err := reg.RegisterProvider(metadata, nil); err != nil {
		t.Fatal(err)
	}
	var operations []capability.Operation
	for _, id := range []string{"fake.files.list", "fake.trash.list"} {
		operations = append(operations, capability.Operation{
			Descriptor: capability.Descriptor{ID: id, Version: 1, Provider: "fake", Description: "List",
				Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
					Confirmation: capability.ConfirmationNone, DataSensitivity: "test"},
				InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"array"}`)},
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return []any{}, nil
			},
		})
	}
	if err := reg.Register("fake", operations...); err != nil {
		t.Fatal(err)
	}
	return reg
}

// A tool the targets of the form do not reach is marked with its reason as text and stays tickable; the mark
// follows the targets set in the form and carries no target value.
func TestThePickerMarksToolsOutsideTheTargets(t *testing.T) {
	m, _ := toolsModel(t, reachRegistry(t), map[string]config.Connection{
		"route": {Service: "wiki", Credential: "reader", Tools: []string{}},
	})
	openEntryForm(t, m, sectionConnections, "route")
	m.field(targetsLabel).entries = []string{"folder/secret-drive/secret-folder"}
	focusField(t, m, toolListLabel)
	press(t, m, "enter")
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "fake.trash.list read (outside-targets: needs drive/DRIVE_ID)") {
		t.Fatalf("picker does not mark the tool outside the targets:\n%s", view)
	}
	if strings.Contains(view, "fake.files.list read (outside-targets") {
		t.Fatalf("a tool inside the targets is marked:\n%s", view)
	}
	if strings.Contains(view, "secret-") {
		t.Fatalf("the picker shows a target value:\n%s", view)
	}
	typeText(t, m, "trash")
	press(t, m, " ")
	if !strings.Contains(screenOf(m), "[x] fake.trash.list") {
		t.Fatalf("the tool is not tickable:\n%s", screenOf(m))
	}
	press(t, m, "enter")
	if got := m.fieldValue(toolListLabel); got != "fake.trash.list" {
		t.Fatalf("tool list = %q, want the ticked tool kept", got)
	}

	m.field(targetsLabel).entries = []string{"drive/d1"}
	press(t, m, "enter")
	if view := screenOf(m); strings.Contains(view, "outside-targets") {
		t.Fatalf("the mark stays after the targets reach the tool:\n%s", view)
	}
}
