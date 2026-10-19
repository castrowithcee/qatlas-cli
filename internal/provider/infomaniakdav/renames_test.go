package infomaniakdav

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Infomaniak DAV tool IDs.
var currentToolIDs = []string{
	"infomaniak.dav.address-books.list",
	"infomaniak.dav.calendars.list",
	"infomaniak.dav.contacts.create",
	"infomaniak.dav.contacts.delete",
	"infomaniak.dav.contacts.get",
	"infomaniak.dav.contacts.list",
	"infomaniak.dav.contacts.update",
	"infomaniak.dav.events.create",
	"infomaniak.dav.events.delete",
	"infomaniak.dav.events.get",
	"infomaniak.dav.events.list",
	"infomaniak.dav.events.update",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 12 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 12 {
		t.Errorf("renamedTools has %d entries, want 12", len(renamedTools))
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
		want := "infomaniak.dav." + strings.ReplaceAll(strings.TrimPrefix(old, "infomaniakdav."), "addressbooks", "address-books")
		if !strings.HasPrefix(old, "infomaniakdav.") || next != want {
			t.Errorf("rename %s -> %s changes more than the provider ID and the address book object", old, next)
		}
		targets[next] = true
	}
	for id := range current {
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerDAVProviderAndToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  dav:
    provider: infomaniakdav
credentials:
  dav-secret:
    provider: infomaniakdav
    type: env
    values:
      user-id: QATLAS_DAV_USER
      app-password: QATLAS_DAV_PASSWORD
connections:
  main:
    service: dav
    credential: dav-secret
    targets: [calendar/personal, addressbook/contacts]
    tools: [infomaniakdav.calendars.list, infomaniakdav.addressbooks.list]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, old := range []string{"infomaniakdav.calendars.list", "infomaniakdav.addressbooks.list"} {
		next := renamedTools[old]
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	if !strings.Contains(warnings, "infomaniakdav") || !strings.Contains(warnings, `"infomaniak.dav"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
