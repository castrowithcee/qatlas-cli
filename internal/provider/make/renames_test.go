package makeapi

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Make tool IDs.
var currentToolIDs = []string{
	"make.connection-access.list",
	"make.connection-access.set",
	"make.connections.create",
	"make.connections.delete",
	"make.connections.editable-schema",
	"make.connections.get",
	"make.connections.list",
	"make.connections.rename",
	"make.connections.set-data",
	"make.connections.test",
	"make.credential-requests.create",
	"make.credential-requests.delete",
	"make.credential-requests.get",
	"make.credential-requests.list",
	"make.data-store-records.create",
	"make.data-store-records.delete",
	"make.data-store-records.list",
	"make.data-store-records.replace",
	"make.data-store-records.update",
	"make.data-stores.create",
	"make.data-stores.delete",
	"make.data-stores.get",
	"make.data-stores.list",
	"make.data-stores.update",
	"make.data-structures.get",
	"make.data-structures.list",
	"make.hook-queue.delete",
	"make.hook-queue.get",
	"make.hook-queue.list",
	"make.hook-queue.stats",
	"make.hooks.delete",
	"make.hooks.disable",
	"make.hooks.enable",
	"make.hooks.get",
	"make.hooks.list",
	"make.hooks.logs",
	"make.hooks.ping",
	"make.hooks.rename",
	"make.hooks.url",
	"make.organization-variables.create",
	"make.organization-variables.delete",
	"make.organization-variables.list",
	"make.organization-variables.update",
	"make.organization.get",
	"make.organization.members",
	"make.organization.payments",
	"make.organization.subscription",
	"make.organization.update",
	"make.organization.usage",
	"make.runs.get",
	"make.runs.list",
	"make.scenarios.blueprint",
	"make.scenarios.create",
	"make.scenarios.get",
	"make.scenarios.list",
	"make.scenarios.run",
	"make.scenarios.start",
	"make.scenarios.stop",
	"make.scenarios.update",
	"make.team-variables.create",
	"make.team-variables.delete",
	"make.team-variables.list",
	"make.team-variables.update",
	"make.team.get",
	"make.team.members",
	"make.team.usage",
	"make.teams.create",
	"make.teams.delete",
	"make.teams.list",
	"make.teams.update",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 70 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 32 {
		t.Errorf("renamedTools has %d entries, want 32", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		targets[next] = true
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
	for id := range current {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
}

func TestFormerMakeToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  make:
    provider: make
    base_url: https://eu1.make.com
credentials:
  make-token:
    provider: make
    type: env
    values:
      api-token: QATLAS_MAKE_TOKEN
connections:
  automation:
    service: make
    credential: make-token
    target: team/1
    permissions: [read, update]
    tools: [make.datastores.list, make.hookqueue.list, make.connections.setdata]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"make.datastores.list":     "make.data-stores.list",
		"make.hookqueue.list":      "make.hook-queue.list",
		"make.connections.setdata": "make.connections.set-data",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
