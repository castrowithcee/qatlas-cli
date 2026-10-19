package twentycrm

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Twenty CRM tool IDs.
var currentToolIDs = []string{
	"twenty-crm.activity-targets.create",
	"twenty-crm.activity-targets.delete",
	"twenty-crm.activity-targets.list",
	"twenty-crm.agents.get",
	"twenty-crm.agents.list",
	"twenty-crm.attachments.get",
	"twenty-crm.attachments.list",
	"twenty-crm.attachments.upload",
	"twenty-crm.companies.create",
	"twenty-crm.companies.delete",
	"twenty-crm.companies.destroy",
	"twenty-crm.companies.get",
	"twenty-crm.companies.list",
	"twenty-crm.companies.restore",
	"twenty-crm.companies.update",
	"twenty-crm.logic-functions.get",
	"twenty-crm.logic-functions.list",
	"twenty-crm.meta-fields.create",
	"twenty-crm.meta-fields.delete",
	"twenty-crm.meta-fields.get",
	"twenty-crm.meta-fields.update",
	"twenty-crm.meta-objects.create",
	"twenty-crm.meta-objects.delete",
	"twenty-crm.meta-objects.get",
	"twenty-crm.meta-objects.list",
	"twenty-crm.meta-objects.update",
	"twenty-crm.objects.get",
	"twenty-crm.objects.list",
	"twenty-crm.page-layouts.get",
	"twenty-crm.page-layouts.list",
	"twenty-crm.page-layout-tabs.create",
	"twenty-crm.page-layout-tabs.delete",
	"twenty-crm.page-layout-widgets.create",
	"twenty-crm.page-layout-widgets.delete",
	"twenty-crm.page-layout-widgets.update",
	"twenty-crm.records.batch-create",
	"twenty-crm.records.batch-delete",
	"twenty-crm.records.batch-update",
	"twenty-crm.records.create",
	"twenty-crm.records.delete",
	"twenty-crm.records.destroy",
	"twenty-crm.records.duplicates",
	"twenty-crm.records.get",
	"twenty-crm.records.group-by",
	"twenty-crm.records.list",
	"twenty-crm.records.merge",
	"twenty-crm.records.merge-preview",
	"twenty-crm.records.restore",
	"twenty-crm.records.search",
	"twenty-crm.records.search-all",
	"twenty-crm.records.update",
	"twenty-crm.roles.create",
	"twenty-crm.roles.delete",
	"twenty-crm.roles.list",
	"twenty-crm.roles.set-field-permissions",
	"twenty-crm.roles.set-object-permissions",
	"twenty-crm.roles.set-permission-flags",
	"twenty-crm.roles.update",
	"twenty-crm.skills.get",
	"twenty-crm.skills.list",
	"twenty-crm.view-fields.create",
	"twenty-crm.view-fields.delete",
	"twenty-crm.view-fields.update",
	"twenty-crm.view-filters.create",
	"twenty-crm.view-filters.delete",
	"twenty-crm.view-filters.update",
	"twenty-crm.view-groups.delete",
	"twenty-crm.view-groups.update",
	"twenty-crm.views.create",
	"twenty-crm.views.delete",
	"twenty-crm.views.get",
	"twenty-crm.views.list",
	"twenty-crm.views.update",
	"twenty-crm.view-sorts.create",
	"twenty-crm.view-sorts.delete",
	"twenty-crm.view-sorts.update",
	"twenty-crm.webhooks.delete",
	"twenty-crm.webhooks.get",
	"twenty-crm.webhooks.list",
	"twenty-crm.webhooks.update",
	"twenty-crm.workflow-runs.list",
	"twenty-crm.workflow-runs.retry",
	"twenty-crm.workflow-runs.stop",
	"twenty-crm.workflows.get",
	"twenty-crm.workflows.list",
	"twenty-crm.workflow-versions.activate",
	"twenty-crm.workflow-versions.deactivate",
	"twenty-crm.workspace-members.get",
	"twenty-crm.workspace-members.list",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 89 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 89 {
		t.Errorf("renamedTools has %d entries, want 89", len(renamedTools))
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
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerTwentyIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  crm:
    provider: twentycrm
    base_url: https://api.twenty.com
credentials:
  crm-key:
    provider: twentycrm
    type: env
    values:
      api-key: QATLAS_TWENTY_KEY
connections:
  sales:
    service: crm
    credential: crm-key
    tools: [twentycrm.viewfields.create, twentycrm.records.mergepreview, twentycrm.companies.list]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["crm"].Provider; got != Provider {
		t.Errorf("service provider = %q, want %q", got, Provider)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"twentycrm.viewfields.create":    "twenty-crm.view-fields.create",
		"twentycrm.records.mergepreview": "twenty-crm.records.merge-preview",
		"twentycrm.companies.list":       "twenty-crm.companies.list",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	var providerRenamed bool
	for _, r := range cfg.Renamings() {
		if r.Kind == config.RenamedProvider && r.Old == "twentycrm" && r.New == Provider {
			providerRenamed = true
		}
	}
	if !providerRenamed {
		t.Errorf("renamings = %v, lack the provider rename", cfg.Renamings())
	}
}
