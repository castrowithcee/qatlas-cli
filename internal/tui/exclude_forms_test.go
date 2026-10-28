package tui

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// siteRegistry registers a provider whose bare kind as a target means "all" but is no exclusion.
func siteRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "sites", Name: "Sites",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "token"}},
		Target: config.TargetMetadata{Label: "website", Multiple: true,
			Kinds: []config.TargetKind{{Name: "site", Forms: []string{"site", "site/ID"}, ExcludeForms: []string{"site/ID"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSpaceSwitchesOnlyRowsOfAnExcludableForm(t *testing.T) {
	m, _ := toolsModel(t, siteRegistry(t), map[string]config.Connection{"s": {
		Service: "wiki", Credential: "reader", Targets: []string{"site", "site/7"}}})
	openConnection(t, m, "s")
	if m.canExclude("site") || !m.canExclude("site/7") {
		t.Fatalf("canExclude(site) = %t, canExclude(site/7) = %t", m.canExclude("site"), m.canExclude("site/7"))
	}
	openTargetList(t, m)
	press(t, m, " ")
	if reach, out := exclusionState(m); len(out) != 0 || len(reach) != 2 {
		t.Fatalf("space switched the bare row: reach %v, excluded %v", reach, out)
	}
	press(t, m, "down", " ")
	if !m.targetExcluded["site/7"] {
		t.Fatal("space did not exclude site/7")
	}
}
