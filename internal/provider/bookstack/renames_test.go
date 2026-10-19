package bookstack

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, d := range reg.All() {
		have[d.ID] = true
	}
	if len(have) != 68 {
		t.Errorf("registry holds %d tools, want 68", len(have))
	}
	if len(renamedTools) != 8 {
		t.Errorf("renamedTools has %d entries, want 8", len(renamedTools))
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		targets[next] = true
		if have[old] || !have[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
	for id := range have {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestFormerBookStackToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  wiki:
    provider: bookstack
    base_url: https://wiki.example.com
credentials:
  wiki-token:
    provider: bookstack
    type: env
    values:
      token-id: QATLAS_BS_ID
      token-secret: QATLAS_BS_SECRET
connections:
  wiki:
    service: wiki
    credential: wiki-token
    tools: [bookstack.recyclebin.list, bookstack.auditlog.list, bookstack.books.setcover]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"bookstack.recyclebin.list": "bookstack.recycle-bin.list",
		"bookstack.auditlog.list":   "bookstack.audit-log.list",
		"bookstack.books.setcover":  "bookstack.books.set-cover",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
