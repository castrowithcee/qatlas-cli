package tui

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The form shows no paths yet, so saving a connection through it keeps the paths it has: a save must never
// widen where a connection applies.
func TestSavingAConnectionKeepsItsPaths(t *testing.T) {
	reg := wikiRegistry(t)
	dir := t.TempDir()
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"wiki": {Service: "wiki", Credential: "reader", Paths: []string{dir}},
	})
	openEntryForm(t, m, sectionConnections, "wiki")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Connections["wiki"].Paths; !reflect.DeepEqual(got, []string{dir}) {
		t.Fatalf("paths after saving = %v, want %v", got, []string{dir})
	}
}
