package todoist

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	reg := registry(t)
	if len(reg.Provider(Provider)) != 39 {
		t.Errorf("registry holds %d tools, want 39", len(reg.Provider(Provider)))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
	if len(renamedTools) != 1 || renamedTools["todoist.completedtasks.list"] != "todoist.completed-tasks.list" {
		t.Errorf("renamedTools = %v", renamedTools)
	}
	for _, d := range reg.Provider(Provider) {
		if _, old := renamedTools[d.ID]; old {
			t.Errorf("former ID %s is still registered", d.ID)
		}
	}
}

func TestFormerTodoistToolIDLoadsWithWarning(t *testing.T) {
	document := `version: 1
services:
  todoist:
    provider: todoist
    base_url: https://api.todoist.com/api/v1
credentials:
  reader:
    provider: todoist
    type: env
    values:
      token: QATLAS_TODOIST_TOKEN
connections:
  work:
    service: todoist
    credential: reader
    targets: ["6XGgm6PHrGgMpCFX"]
    tools: [todoist.completedtasks.list]
`
	cfg, err := config.Decode(strings.NewReader(document), registry(t))
	if err != nil {
		t.Fatal(err)
	}
	want := `tool "todoist.completedtasks.list" was renamed to "todoist.completed-tasks.list"`
	if warnings := strings.Join(cfg.ToolWarnings(), "\n"); !strings.Contains(warnings, want) {
		t.Errorf("warnings lack the rename:\n%s", warnings)
	}
}
