package seatable

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of SeaTable base tool IDs.
var currentToolIDs = []string{
	"seatable.base.base.operations",
	"seatable.base.collaborators.list",
	"seatable.base.columns.create",
	"seatable.base.columns.delete",
	"seatable.base.columns.list",
	"seatable.base.columns.options-add",
	"seatable.base.columns.options-delete",
	"seatable.base.columns.options-update",
	"seatable.base.columns.update",
	"seatable.base.comments.create",
	"seatable.base.comments.delete",
	"seatable.base.comments.list",
	"seatable.base.files.delete",
	"seatable.base.files.get",
	"seatable.base.files.upload",
	"seatable.base.links.create",
	"seatable.base.links.delete",
	"seatable.base.links.list",
	"seatable.base.links.update",
	"seatable.base.rows.activities",
	"seatable.base.rows.batch-create",
	"seatable.base.rows.batch-delete",
	"seatable.base.rows.batch-update",
	"seatable.base.rows.create",
	"seatable.base.rows.delete",
	"seatable.base.rows.get",
	"seatable.base.rows.list",
	"seatable.base.rows.search",
	"seatable.base.rows.update",
	"seatable.base.snapshots.create",
	"seatable.base.tables.create",
	"seatable.base.tables.delete",
	"seatable.base.tables.duplicate",
	"seatable.base.tables.list",
	"seatable.base.tables.rename",
	"seatable.base.views.create",
	"seatable.base.views.delete",
	"seatable.base.views.get",
	"seatable.base.views.list",
	"seatable.base.views.update",
}

func TestRegistryHoldsExactlyTheCurrentToolIDs(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range reg.All() {
		got = append(got, d.ID)
	}
	sort.Strings(got)
	want := append([]string(nil), currentToolIDs...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 40 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 40 {
		t.Errorf("renamedTools has %d entries, want 40", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		rest := strings.TrimPrefix(old, "seatable.")
		rest = strings.NewReplacer("batchcreate", "batch-create", "batchupdate", "batch-update", "batchdelete", "batch-delete",
			"optionsadd", "options-add", "optionsupdate", "options-update", "optionsdelete", "options-delete").Replace(rest)
		if !strings.HasPrefix(old, "seatable.") || next != "seatable.base."+rest {
			t.Errorf("rename %s -> %s changes more than the provider ID and the multi-word actions", old, next)
		}
		targets[next] = true
	}
	for id := range current {
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerSeaTableProviderAndToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  tables:
    provider: seatable
    base_url: https://cloud.seatable.io
credentials:
  tables-secret:
    provider: seatable
    type: env
    values:
      api-token: QATLAS_SEATABLE_TOKEN
connections:
  main:
    service: tables
    credential: tables-secret
    targets: ["*"]
    tools: [seatable.tables.list, seatable.rows.batchcreate]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, old := range []string{"seatable.tables.list", "seatable.rows.batchcreate"} {
		next := renamedTools[old]
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	if !strings.Contains(warnings, "seatable") || !strings.Contains(warnings, `"seatable.base"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
