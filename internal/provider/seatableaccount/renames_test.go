package seatableaccount

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var currentToolIDs = []string{
	"seatable.account.snapshots.list",
	"seatable.account.snapshots.restore",
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
	if strings.Join(got, "\n") != strings.Join(currentToolIDs, "\n") {
		t.Errorf("registry holds %v, want %v", got, currentToolIDs)
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 2 {
		t.Errorf("renamedTools has %d entries, want 2", len(renamedTools))
	}
	for old, next := range renamedTools {
		want := "seatable.account." + strings.TrimPrefix(old, "seatableaccount.")
		if !strings.HasPrefix(old, "seatableaccount.") || next != want {
			t.Errorf("rename %s -> %s changes more than the provider ID", old, next)
		}
	}
}

func TestFormerConfigurationLoadsWithRenameWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	const document = `version: 1
services:
  acct:
    provider: seatableaccount
credentials:
  acct-token:
    provider: seatableaccount
    type: env
    values:
      account-token: QATLAS_SEATABLE_ACCOUNT_TOKEN
connections:
  main:
    service: acct
    credential: acct-token
    target: 42/Customer base
    tools: [seatableaccount.snapshots.list, seatableaccount.snapshots.restore]
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
	if !strings.Contains(warnings, "seatableaccount") || !strings.Contains(warnings, `"seatable.account"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
