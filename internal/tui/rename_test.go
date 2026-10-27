package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// startRename presses r on the selected entry of the list of section s and types the new name over the old
// one, up to the dry run's request.
func startRename(t *testing.T, m *Model, s section, name, to string) {
	t.Helper()
	openSectionByName(t, m, s)
	m.list.selectName(name)
	pump(t, m, "r")
	if m.screen != screenRename || m.rename == nil {
		t.Fatalf("r = screen %v, want the rename prompt:\n%s", m.screen, screenOf(m))
	}
	clearField(t, m)
	typeText(t, m, to)
}

func storedConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := loadTestConfig(t, path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	return cfg
}

func TestRenamingAServiceShowsTheDryRunAndNeedsConfirmation(t *testing.T) {
	m, _, path := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	seedConnections(t, m, "docs")

	startRename(t, m, sectionServices, "wiki", "handbook")
	pump(t, m, "enter")
	view := screenOf(m)
	for _, want := range []string{"Rename service wiki to handbook?", "services.wiki -> services.handbook",
		"rewrites", "outside", "Dry run", "y rename"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dry run does not show %q:\n%s", want, view)
		}
	}
	if _, ok := storedConfig(t, path).Services["wiki"]; !ok {
		t.Fatal("the dry run changed the configuration")
	}

	pump(t, m, "esc")
	if m.screen != screenList || m.rename != nil {
		t.Fatalf("esc = screen %v rename %v, want the list", m.screen, m.rename)
	}
	cfg := storedConfig(t, path)
	if _, ok := cfg.Services["wiki"]; !ok || len(cfg.Services) != 1 {
		t.Fatalf("cancelling changed the services: %v", cfg.Services)
	}

	startRename(t, m, sectionServices, "wiki", "handbook")
	pump(t, m, "enter", "y")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("confirming = screen %v fail %q", m.screen, m.fail)
	}
	cfg = storedConfig(t, path)
	if _, ok := cfg.Services["handbook"]; !ok || cfg.Connections["docs"].Service != "handbook" {
		t.Fatalf("the rename did not reach the file: %+v", cfg)
	}
	if name := selectedName(t, m); name != "handbook" || !strings.Contains(m.status, "Renamed service wiki to handbook") {
		t.Errorf("selected %q status %q, want the renamed entry and the result", name, m.status)
	}
}

func TestRenamingACredentialListsItsSecretsAndEnvironment(t *testing.T) {
	m, _, path := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	seedConnections(t, m, "docs")

	startRename(t, m, sectionCredentials, "reader", "viewer")
	pump(t, m, "enter")
	view := screenOf(m)
	for _, want := range []string{"Rename credential reader to viewer?", "credentials.reader -> credentials.viewer",
		"env       QATLAS_READER", "reader    docs", "outside"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dry run does not show %q:\n%s", want, view)
		}
	}
	pump(t, m, "n")
	if _, ok := storedConfig(t, path).Credentials["reader"]; !ok || m.status != "Cancelled; nothing was renamed" {
		t.Fatalf("cancelling: status %q, credentials %v", m.status, storedConfig(t, path).Credentials)
	}

	startRename(t, m, sectionCredentials, "reader", "viewer")
	pump(t, m, "enter", "y")
	if m.fail != "" {
		t.Fatalf("renaming reported %q", m.fail)
	}
	cfg := storedConfig(t, path)
	if _, ok := cfg.Credentials["viewer"]; !ok || cfg.Connections["docs"].Credential != "viewer" {
		t.Fatalf("the rename did not reach the file: %+v", cfg.Credentials)
	}
	if !strings.Contains(m.status, "environment variables are read under the new name") {
		t.Errorf("status = %q, want the environment variables named", m.status)
	}
}

func TestRenamingAConnectionWarnsOfTokensAndKeepsItsApproval(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	fixtureToken(t, v, "ci", nil, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	openSectionByName(t, m, sectionConnections)
	pump(t, m, "r")
	unlockWithAdminPassphrase(t, m, tokenPassphrase)
	if m.screen != screenRename {
		t.Fatalf("screen after the passphrase = %v, want the rename prompt", m.screen)
	}
	clearField(t, m)
	typeText(t, m, "private")
	pump(t, m, "enter")
	// The sentence on the tokens wraps with the question, so the words are compared, not the line breaks.
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	for _, want := range []string{"approval approved; carried over", "warning agent token ci names personal",
		"outside agent calls", "ends the cover of 1 agent token"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dry run does not show %q:\n%s", want, view)
		}
	}

	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("renaming reported %q", m.fail)
	}
	cfg := storedConfig(t, filepath.Join(dir, "config.yaml"))
	if _, ok := cfg.Connections["private"]; !ok || len(cfg.Connections) != 1 {
		t.Fatalf("connections = %v, want only private", cfg.Connections)
	}
	if !isApproved(t, dir, tokenPassphrase, cfg, "private") {
		t.Error("the approval did not move to the new name")
	}
	openSectionByName(t, m, sectionApprovals)
	if view := screenOf(m); strings.Contains(view, "personal") {
		t.Errorf("the approvals still show the old name:\n%s", view)
	}
}

func TestARenameRefusedByTheCoreIsShownAndChangesNothing(t *testing.T) {
	m, _, path := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	seedConnections(t, m, "docs", "notes")

	startRename(t, m, sectionConnections, "docs", "notes")
	pump(t, m, "enter")
	if m.screen != screenRename || m.rename.plan != nil || m.fail == "" {
		t.Fatalf("a taken name: screen %v plan %v fail %q, want the prompt with the error", m.screen, m.rename, m.fail)
	}
	if !strings.Contains(screenOf(m), "error:") {
		t.Errorf("the error is not shown:\n%s", screenOf(m))
	}
	if cfg := storedConfig(t, path); len(cfg.Connections) != 2 {
		t.Errorf("connections = %v, want both unchanged", cfg.Connections)
	}
}

func TestARenameWithoutAnAdminSessionIsRefused(t *testing.T) {
	store, dir, _ := newTokenFixture(t)
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	openSectionByName(t, m, sectionConnections)
	pump(t, m, "r")
	if m.screen != screenAdminAuth || m.rename != nil {
		t.Fatalf("r without a session = screen %v rename %v, want the admin dialog only", m.screen, m.rename)
	}
	pump(t, m, "esc")
	if m.screen != screenList || m.rename != nil {
		t.Fatalf("esc = screen %v rename %v, want the list", m.screen, m.rename)
	}
	if _, ok := storedConfig(t, filepath.Join(dir, "config.yaml")).Connections["personal"]; !ok {
		t.Error("the connection is gone")
	}
}
