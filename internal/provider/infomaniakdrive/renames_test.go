package infomaniakdrive

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of kDrive tool IDs.
var currentToolIDs = []string{
	"infomaniak.drive.access.get",
	"infomaniak.drive.access.grant",
	"infomaniak.drive.access.revoke",
	"infomaniak.drive.access.update",
	"infomaniak.drive.drives.list",
	"infomaniak.drive.dropbox.delete",
	"infomaniak.drive.dropbox.get",
	"infomaniak.drive.files.copy",
	"infomaniak.drive.files.download",
	"infomaniak.drive.files.get",
	"infomaniak.drive.files.list",
	"infomaniak.drive.files.move",
	"infomaniak.drive.files.rename",
	"infomaniak.drive.files.stat",
	"infomaniak.drive.files.trash",
	"infomaniak.drive.files.upload",
	"infomaniak.drive.folders.create",
	"infomaniak.drive.links.delete",
	"infomaniak.drive.links.get",
	"infomaniak.drive.links.list",
	"infomaniak.drive.trash.delete",
	"infomaniak.drive.trash.empty",
	"infomaniak.drive.trash.list",
	"infomaniak.drive.trash.restore",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 24 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 24 {
		t.Errorf("renamedTools has %d entries, want 24", len(renamedTools))
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
		if strings.TrimPrefix(old, "infomaniakdrive.") != strings.TrimPrefix(next, "infomaniak.drive.") ||
			!strings.HasPrefix(old, "infomaniakdrive.") {
			t.Errorf("rename %s -> %s changes more than the provider ID", old, next)
		}
		targets[next] = true
	}
	// Every current ID is the target of exactly one rename.
	for id := range current {
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerKDriveProviderAndToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  drive:
    provider: infomaniakdrive
credentials:
  drive-token:
    provider: infomaniakdrive
    type: env
    values:
      token: QATLAS_KDRIVE_TOKEN
connections:
  main:
    service: drive
    credential: drive-token
    targets: [account/1]
    tools: [infomaniakdrive.drives.list, infomaniakdrive.files.stat]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, old := range []string{"infomaniakdrive.drives.list", "infomaniakdrive.files.stat"} {
		next := renamedTools[old]
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	if !strings.Contains(warnings, "infomaniakdrive") || !strings.Contains(warnings, `"infomaniak.drive"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
