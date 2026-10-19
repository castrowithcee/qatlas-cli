package penpot

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Penpot tool IDs.
var currentToolIDs = []string{
	"penpot.comments.create",
	"penpot.comments.delete",
	"penpot.comments.list",
	"penpot.comments.threads",
	"penpot.comments.update",
	"penpot.files.create",
	"penpot.files.delete",
	"penpot.files.export",
	"penpot.files.get",
	"penpot.files.import",
	"penpot.files.list",
	"penpot.files.move",
	"penpot.files.purge",
	"penpot.files.rename",
	"penpot.files.restore",
	"penpot.libraries.link",
	"penpot.libraries.list",
	"penpot.libraries.share",
	"penpot.media.from-url",
	"penpot.media.upload",
	"penpot.members.list",
	"penpot.members.remove",
	"penpot.members.set-role",
	"penpot.projects.create",
	"penpot.projects.delete",
	"penpot.projects.list",
	"penpot.projects.rename",
	"penpot.snapshots.create",
	"penpot.snapshots.restore",
	"penpot.teams.create",
	"penpot.teams.delete",
	"penpot.teams.list",
	"penpot.webhooks.delete",
	"penpot.webhooks.list",
	"penpot.webhooks.update",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 35 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 2 {
		t.Errorf("renamedTools has %d entries, want 2", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	for old, next := range renamedTools {
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
}

func TestFormerPenpotToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  pp:
    provider: penpot
    base_url: https://design.penpot.app
credentials:
  pp-token:
    provider: penpot
    type: env
    values:
      access-token: QATLAS_PENPOT_TOKEN
connections:
  design:
    service: pp
    credential: pp-token
    target: team/11111111-1111-1111-1111-111111111111
    tools: [penpot.media.fromurl, penpot.members.setrole]
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
