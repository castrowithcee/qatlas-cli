package tui

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
)

func labelledModel(t *testing.T) (*Model, string) {
	t.Helper()
	m, path := toolsModel(t, targetsRegistry(t, github.Register), map[string]config.Connection{"gh": {
		Service: "wiki", Credential: "reader", Targets: []string{"repos/octo/a", "repos/octo/b"},
		TargetLabels: map[string]string{"repos/octo/a": "Frontend", "repos/octo/b": "Backend"},
	}})
	return m, path
}

func TestSavingTheFormKeepsTargetLabels(t *testing.T) {
	m, path := labelledModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "gh")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	want := map[string]string{"repos/octo/a": "Frontend", "repos/octo/b": "Backend"}
	if got := savedConnection(t, path, targetsRegistry(t, github.Register), "gh").TargetLabels; !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestRemovingATargetInTheFormDropsItsLabel(t *testing.T) {
	m, path := labelledModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "d", "y", "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, targetsRegistry(t, github.Register), "gh")
	want := map[string]string{"repos/octo/b": "Backend"}
	if !reflect.DeepEqual(saved.TargetLabels, want) {
		t.Errorf("targets %v labels %v, want %v", saved.TargetValues(), saved.TargetLabels, want)
	}
}

func TestDuplicatingAConnectionKeepsTargetLabels(t *testing.T) {
	m, path := labelledModel(t)
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList
	pump(t, m, "p")
	if m.screen != screenForm {
		t.Fatalf("p opened screen %v", m.screen)
	}
	clearField(t, m)
	typeText(t, m, "gh-copy")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, targetsRegistry(t, github.Register), "gh-copy")
	want := map[string]string{"repos/octo/a": "Frontend", "repos/octo/b": "Backend"}
	if !reflect.DeepEqual(saved.TargetLabels, want) {
		t.Errorf("labels = %v, want %v", saved.TargetLabels, want)
	}
}
