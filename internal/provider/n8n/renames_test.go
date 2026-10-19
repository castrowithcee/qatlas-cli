package n8n

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of n8n tool IDs.
var currentToolIDs = []string{
	"n8n.audit.generate",
	"n8n.credentials.create",
	"n8n.credentials.delete",
	"n8n.credentials.get",
	"n8n.credentials.list",
	"n8n.credentials.schema",
	"n8n.credentials.test",
	"n8n.credentials.transfer",
	"n8n.credentials.update",
	"n8n.data-columns.add",
	"n8n.data-columns.delete",
	"n8n.data-columns.list",
	"n8n.data-columns.update",
	"n8n.data-rows.delete",
	"n8n.data-rows.insert",
	"n8n.data-rows.list",
	"n8n.data-rows.update",
	"n8n.data-rows.upsert",
	"n8n.data-tables.create",
	"n8n.data-tables.delete",
	"n8n.data-tables.get",
	"n8n.data-tables.list",
	"n8n.data-tables.rename",
	"n8n.executions.get",
	"n8n.executions.list",
	"n8n.executions.retry",
	"n8n.executions.stop",
	"n8n.project-members.add",
	"n8n.project-members.list",
	"n8n.project-members.remove",
	"n8n.project-members.set-role",
	"n8n.projects.create",
	"n8n.projects.delete",
	"n8n.projects.list",
	"n8n.projects.update",
	"n8n.source-control.pull",
	"n8n.source-control.push",
	"n8n.source-control.status",
	"n8n.tags.create",
	"n8n.tags.delete",
	"n8n.tags.get",
	"n8n.tags.list",
	"n8n.tags.update",
	"n8n.users.delete",
	"n8n.users.get",
	"n8n.users.list",
	"n8n.users.set-role",
	"n8n.variables.create",
	"n8n.variables.delete",
	"n8n.variables.list",
	"n8n.variables.update",
	"n8n.workflows.activate",
	"n8n.workflows.create",
	"n8n.workflows.deactivate",
	"n8n.workflows.get",
	"n8n.workflows.list",
	"n8n.workflows.update",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 57 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 22 {
		t.Errorf("renamedTools has %d entries, want 22", len(renamedTools))
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
	// Every ID with a hyphen in a tool segment is the target of exactly one rename.
	targets := map[string]bool{}
	for _, next := range renamedTools {
		targets[next] = true
	}
	for id := range current {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
}

func TestFormerN8NToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  n8n-a:
    provider: n8n
    base_url: https://customer-a.app.n8n.cloud
credentials:
  n8n-a-key:
    provider: n8n
    type: env
    values:
      api-key: QATLAS_N8N_KEY
connections:
  automation:
    service: n8n-a
    credential: n8n-a-key
    tools: [n8n.datatables.list, n8n.sourcecontrol.status, n8n.users.setrole]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"n8n.datatables.list":      "n8n.data-tables.list",
		"n8n.sourcecontrol.status": "n8n.source-control.status",
		"n8n.users.setrole":        "n8n.users.set-role",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
