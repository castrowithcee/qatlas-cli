package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func outdatedModel(t *testing.T) (*Model, string) {
	t.Helper()
	return toolsModel(t, targetsRegistry(t, github.Register), map[string]config.Connection{
		"old": {Service: "wiki", Credential: "reader", Targets: []string{"repos/octo/former"}, TargetsOutdated: true},
		"ok":  {Service: "wiki", Credential: "reader", Targets: []string{"repos/octo/fine"}},
	})
}

// scopeOfSaved is the approval scope the saved file gives one connection.
func scopeOfSaved(t *testing.T, path, name string) vault.Scope {
	t.Helper()
	cfg, err := config.Load(path, targetsRegistry(t, github.Register))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve(name, "")
	if err != nil {
		t.Fatal(err)
	}
	return secret.ScopeOf(resolved)
}

func TestOutdatedConnectionRowSaysTheTargetsNeedAChoice(t *testing.T) {
	m, _ := outdatedModel(t)
	openSectionByName(t, m, sectionConnections)
	if cells := m.cells("old"); !strings.Contains(strings.Join(cells, "|"), tr(tTargetsOutdatedCell)) ||
		strings.Contains(strings.Join(cells, "|"), "former") {
		t.Errorf("list cells of the outdated connection = %v", cells)
	}
	if cells := m.cells("ok"); strings.Contains(strings.Join(cells, "|"), tr(tTargetsOutdatedCell)) {
		t.Errorf("list cells of the valid connection = %v", cells)
	}
	openConnection(t, m, "old")
	if got := m.fieldWarning(*targetRowOf(m.fields)); got != tr(tTargetsOutdatedWarning) {
		t.Errorf("warning of the outdated target row = %q", got)
	}
	focusField(t, m, targetsLabel)
	if !strings.Contains(strings.Join(strings.Fields(m.render()), " "), "warning: these targets keep a former meaning") {
		t.Errorf("the form does not show the notice:\n%s", m.render())
	}
	pump(t, m, "esc")
	openConnection(t, m, "ok")
	if got := m.fieldWarning(*targetRowOf(m.fields)); got != "" {
		t.Errorf("warning of the valid target row = %q", got)
	}
}

func TestOutdatedTargetsProblemPointsToTheTargetRow(t *testing.T) {
	m, _ := outdatedModel(t)
	openConnection(t, m, "old")
	for _, field := range []string{"target", "targets"} {
		p := config.Problem{Section: config.SectionConnections, Name: "old", Field: field, Code: config.CodeTargetsOutdated}
		if got := m.problemRow(p, "old"); got != targetsLabel {
			t.Errorf("problemRow(%s) = %q, want %q", field, got, targetsLabel)
		}
	}
}

func TestSavingTheSameTargetsKeepsTheOutdatedMark(t *testing.T) {
	m, path := outdatedModel(t)
	before := scopeOfSaved(t, path, "old")
	openConnection(t, m, "old")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if !savedConnection(t, path, targetsRegistry(t, github.Register), "old").TargetsOutdated {
		t.Error("saving without a target change removed the mark")
	}
	if !reflect.DeepEqual(scopeOfSaved(t, path, "old"), before) {
		t.Error("the scope changed without a target change")
	}
}

func TestChoosingTheTargetsAgainClearsTheMarkAndOpensTheApproval(t *testing.T) {
	m, path := outdatedModel(t)
	before := scopeOfSaved(t, path, "old")
	openConnection(t, m, "old")
	openTargetList(t, m)
	press(t, m, "d", "y")
	addTarget(t, m, "repos/octo/new")
	press(t, m, "esc")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, targetsRegistry(t, github.Register), "old")
	if saved.TargetsOutdated || !reflect.DeepEqual(saved.TargetValues(), []string{"repos/octo/new"}) {
		t.Errorf("saved targets %v outdated %v", saved.TargetValues(), saved.TargetsOutdated)
	}
	if after := scopeOfSaved(t, path, "old"); reflect.DeepEqual(after, before) {
		t.Error("the new targets left the approval scope unchanged")
	}
	cfg, err := config.Load(path, targetsRegistry(t, github.Register))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the saved file is not valid: %v", err)
	}
}
