package excalidrawplus

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Excalidraw+ tool IDs.
var currentToolIDs = []string{
	"excalidraw-plus.collections.delete",
	"excalidraw-plus.collections.list",
	"excalidraw-plus.content.patch",
	"excalidraw-plus.content.replace",
	"excalidraw-plus.logs.list",
	"excalidraw-plus.scenes.content",
	"excalidraw-plus.scenes.create",
	"excalidraw-plus.scenes.delete",
	"excalidraw-plus.scenes.get",
	"excalidraw-plus.scenes.list",
	"excalidraw-plus.scenes.update",
	"excalidraw-plus.users.get",
	"excalidraw-plus.users.list",
	"excalidraw-plus.users.remove",
	"excalidraw-plus.users.update",
}

func TestRenamedToolsCoverEveryToolID(t *testing.T) {
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 15 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		targets[next] = true
		if strings.Replace(old, "excalidrawplus.", "excalidraw-plus.", 1) != next {
			t.Errorf("rename %s -> %s changes more than the provider", old, next)
		}
	}
	for _, id := range currentToolIDs {
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerExcalidrawIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  board:
    provider: excalidrawplus
credentials:
  board-key:
    provider: excalidrawplus
    type: env
    values:
      api-key: QATLAS_EXCALIDRAW_KEY
connections:
  work:
    service: board
    credential: board-key
    targets: [collection/c1]
    permissions: [read]
    tools: [excalidrawplus.scenes.list, excalidrawplus.users.get]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["board"].Provider; got != Provider {
		t.Errorf("service provider = %q, want %q", got, Provider)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, want := range []string{"excalidrawplus.scenes.list", "excalidraw-plus.scenes.list"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
}
