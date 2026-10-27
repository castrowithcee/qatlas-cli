package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/brave"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// A provider without targets is edited and saved like any other: its target row says there is nothing to
// choose, an empty row saves, and a typed target is refused where it is added.
func TestAProviderWithoutTargetsSavesWithoutThem(t *testing.T) {
	reg := targetsRegistry(t, brave.Register)
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := config.NewStore(path, reg)
	cfg := store.New()
	mustNoError(t, cfg.SetService("brave", config.Service{Provider: brave.Provider}))
	mustNoError(t, cfg.SetCredential("key", config.Credential{Provider: brave.Provider, Type: config.CredentialTypeKeyring}))
	mustNoError(t, store.Save(cfg))
	m, err := buildModel(store, nil, nil, &redact.Redactor{})
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})

	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "search")
	row := m.field(targetsLabel)
	if row == nil || !strings.Contains(row.hint, "optional") || !strings.Contains(row.detail, "leave the target empty") {
		t.Fatalf("target row = %+v, want an optional row that says to leave it empty", row)
	}
	openTargetList(t, m)
	addTarget(t, m, "somewhere")
	if !strings.Contains(m.fail, "takes no target") || strings.Contains(m.fail, "somewhere") ||
		len(m.targetList.all) != 0 {
		t.Fatalf("a target was accepted or quoted: error %q, list %v", m.fail, m.targetList.all)
	}
	// Discarding the typed target leaves the list; the connection itself is still open.
	press(t, m, "esc", "d")
	press(t, m, "f2")
	if !hasConnection(t, path, reg, "search") {
		t.Fatalf("the connection was not saved: error %q\n%s", m.fail, screenOf(m))
	}
	if saved := savedConnection(t, path, reg, "search"); saved.Target != "" || len(saved.Targets) != 0 {
		t.Fatalf("saved %q / %v, want no target", saved.Target, saved.Targets)
	}
}
