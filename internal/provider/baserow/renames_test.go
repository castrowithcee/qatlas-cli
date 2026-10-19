package baserow

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var currentToolIDs = []string{
	"baserow.fields.list",
	"baserow.files.get",
	"baserow.files.upload",
	"baserow.rows.batch-create",
	"baserow.rows.batch-delete",
	"baserow.rows.batch-update",
	"baserow.rows.create",
	"baserow.rows.delete",
	"baserow.rows.get",
	"baserow.rows.list",
	"baserow.rows.move",
	"baserow.rows.search",
	"baserow.rows.update",
	"baserow.tables.list",
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
	if strings.Join(got, "\n") != strings.Join(currentToolIDs, "\n") || len(got) != 14 {
		t.Errorf("registry holds %d tools %v, want the %d current IDs", len(got), got, len(currentToolIDs))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 3 {
		t.Errorf("renamedTools has %d entries, want 3", len(renamedTools))
	}
	for old, next := range renamedTools {
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
}

func TestFormerBaserowToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  br:
    provider: baserow
credentials:
  br-token:
    provider: baserow
    type: env
    values:
      database-token: QATLAS_BASEROW_TOKEN
connections:
  data:
    service: br
    credential: br-token
    target: table/1
    tools: [baserow.rows.batchcreate, baserow.rows.batchupdate, baserow.rows.batchdelete]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range renamedTools {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
