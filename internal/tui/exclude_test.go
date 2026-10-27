package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// excluderRegistry registers a provider that reads exclusions, which no real provider declares.
func excluderRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "excluder", Name: "Excluder",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "token"}},
		Target: config.TargetMetadata{Label: "repository", Required: true, Multiple: true, Wildcard: "*",
			Kinds: []config.TargetKind{{Name: "repo", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}, Excludable: true}},
			Validate: func(target string) error {
				if target == "*" || strings.HasPrefix(target, "repos/") {
					return nil
				}
				return errors.New("a target must name a repository")
			}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func excludedModel(t *testing.T) (*Model, string) {
	return toolsModel(t, excluderRegistry(t), map[string]config.Connection{"ex": {
		Service: "wiki", Credential: "reader", Targets: []string{"repos/o/*"},
		Exclude:      []string{"repos/o/secret"},
		TargetLabels: map[string]string{"repos/o/*": "All", "repos/o/secret": "Hidden"},
	}})
}

// The form does not edit exclusions, so saving it, also under another name, must carry them with their labels
// over; dropping them would widen the connection.
func TestSavingTheFormKeepsExclusionsAndTheirLabels(t *testing.T) {
	m, path := excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "ex")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, excluderRegistry(t), "ex")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) || saved.TargetLabels["repos/o/secret"] != "Hidden" ||
		saved.TargetLabels["repos/o/*"] != "All" {
		t.Errorf("saved exclude %v labels %v", saved.Exclude, saved.TargetLabels)
	}

	m, path = excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList
	pump(t, m, "p")
	clearField(t, m)
	typeText(t, m, "ex-copy")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved = savedConnection(t, path, excluderRegistry(t), "ex-copy")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) || saved.TargetLabels["repos/o/secret"] != "Hidden" {
		t.Errorf("copy exclude %v labels %v", saved.Exclude, saved.TargetLabels)
	}
}

func TestApprovalDetailShowsExcludeOnlyWhenThereIsSomething(t *testing.T) {
	rows := strings.Join(approvalDetailRows(approval.Change{Fields: []approval.FieldChange{
		{Field: approval.FieldExclude, Before: "(none)", After: "a", Added: []string{"a"}},
	}}), "\n")
	if !strings.Contains(rows, "exclude") || !strings.Contains(rows, "+ a") {
		t.Errorf("rows lack the exclude change:\n%s", rows)
	}
	rows = strings.Join(approvalDetailRows(approval.Change{After: vault.Scope{Targets: []string{"a"}}}), "\n")
	if strings.Contains(rows, "exclude") {
		t.Errorf("rows show an empty exclude list:\n%s", rows)
	}
}

func TestExcludeProblemMapsToTheTargetRow(t *testing.T) {
	m, _ := excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "ex")
	p := config.Problem{Section: config.SectionConnections, Name: "ex", Field: "exclude", Code: config.CodeDuplicateExclude}
	if got := m.problemRow(p, "ex"); got != targetsLabel {
		t.Errorf("problemRow() = %q, want %q", got, targetsLabel)
	}
}
