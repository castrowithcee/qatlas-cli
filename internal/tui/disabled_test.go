package tui

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The connection form has no row for the state: saving must carry it over, with the rest of the
// selection, and a copy starts in the state of its original.
func TestSavingADisabledConnectionKeepsItsStateAndSelection(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {
		Service: "wiki", Credential: "reader", Disabled: true,
		Permissions: []config.Permission{config.PermissionRead},
		Tools:       []string{"bookstack.pages.list"}, Paths: []string{"/work/kunde"},
	}})
	openEntryForm(t, m, sectionConnections, "wiki")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	got := savedConfig(t, path).Connections["wiki"]
	if !got.Disabled || !reflect.DeepEqual(got.Tools, []string{"bookstack.pages.list"}) ||
		!reflect.DeepEqual(got.Paths, []string{"/work/kunde"}) ||
		!reflect.DeepEqual(got.Permissions, []config.Permission{config.PermissionRead}) {
		t.Fatalf("saved connection = %+v, want state and selection kept", got)
	}

	m.screen = screenList
	m.list.selectName("wiki")
	pump(t, m, "p")
	if m.copyFrom != "wiki" {
		t.Fatalf("p: copyFrom %q, want wiki", m.copyFrom)
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving the copy failed: %s", m.fail)
	}
	if copied := savedConfig(t, path).Connections["wiki-copy"]; !copied.Disabled {
		t.Errorf("copy = %+v, want it disabled like its original", copied)
	}
}
