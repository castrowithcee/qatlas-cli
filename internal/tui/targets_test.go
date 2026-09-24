package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/provider/seatable"
	"github.com/castrowithcee/qatlas-cli/internal/provider/telegram"
)

func targetsRegistry(t *testing.T, register func(*capability.Registry) error) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := register(reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

// openTargetList moves to the target row of the form and opens its list.
func openTargetList(t *testing.T, m *Model) {
	t.Helper()
	focusField(t, m, targetsLabel)
	press(t, m, "enter")
	if m.screen != screenTargets {
		t.Fatalf("enter on the target row opened screen %v, want the target list", m.screen)
	}
}

// addTarget adds one target in the open target list, the way a person does.
func addTarget(t *testing.T, m *Model, value string) {
	t.Helper()
	press(t, m, "a")
	typeText(t, m, value)
	press(t, m, "enter")
}

// savedFile is the configuration file as text, so a test can tell a missing key from an empty one.
func savedFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A GitHub connection gets several mixed targets one by one; each is edited and removed on its own, the row
// folds in the form, and the file keeps one target as target, several as targets, and none as neither.
func TestTheTargetListIsEditedEntryByEntry(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, "gh")
	openTargetList(t, m)
	if view := screenOf(m); !strings.Contains(view, "whatever its credential reaches") {
		t.Fatalf("the empty list does not say what it means:\n%s", view)
	}
	addTarget(t, m, "repos/octo/a")
	addTarget(t, m, "repos/octo/b")
	addTarget(t, m, "orgs/octo/projects/1")
	if m.fail != "" {
		t.Fatalf("adding reported %q", m.fail)
	}

	// A target that is already listed, or that GitHub cannot read, stays typed with the reason.
	addTarget(t, m, "repos/octo/a")
	if !strings.Contains(m.fail, "already in the list") || m.targetEdit < 0 {
		t.Fatalf("a duplicate was taken: error %q, editing %d", m.fail, m.targetEdit)
	}
	press(t, m, "esc")
	addTarget(t, m, "octo/a")
	if m.fail == "" || m.targetEdit < 0 {
		t.Fatalf("an invalid target was taken: %v", m.targetList.all)
	}
	press(t, m, "esc")
	press(t, m, "ctrl+s")
	if m.screen != screenForm {
		t.Fatalf("ctrl+s left screen %v, want the form", m.screen)
	}
	if got := m.field(targetsLabel).entries; !reflect.DeepEqual(got,
		[]string{"repos/octo/a", "repos/octo/b", "orgs/octo/projects/1"}) {
		t.Fatalf("targets = %v", got)
	}

	// Folded, the row says how many there are and names the first two; unfolded, it lists every one.
	if view := screenOf(m); !strings.Contains(view, "3 targets: repos/octo/a, repos/octo/b, +1") {
		t.Fatalf("the folded row lacks its short form:\n%s", view)
	}
	press(t, m, "right")
	if view := screenOf(m); !strings.Contains(view, "orgs/octo/projects/1") ||
		strings.Contains(view, ", +1") {
		t.Fatalf("the unfolded row does not list every target:\n%s", view)
	}
	press(t, m, "left")
	if view := screenOf(m); !strings.Contains(view, ", +1") {
		t.Fatalf("the row did not fold again:\n%s", view)
	}

	// esc leaves the row as it was, whatever changed in the list.
	press(t, m, "enter", "x", "y")
	addTarget(t, m, "repos/octo/c")
	press(t, m, "esc")
	if got := m.field(targetsLabel).entries; len(got) != 3 || got[0] != "repos/octo/a" {
		t.Fatalf("esc changed the targets to %v", got)
	}

	// Edit the second, remove the first.
	press(t, m, "enter", "down", "enter")
	clearField(t, m)
	typeText(t, m, "repos/octo/*")
	press(t, m, "enter", "up", "x")
	if view := screenOf(m); !strings.Contains(view, `Remove "repos/octo/a"`) {
		t.Fatalf("remove does not ask first:\n%s", view)
	}
	press(t, m, "n")
	if len(m.targetList.all) != 3 {
		t.Fatalf("n removed a target: %v", m.targetList.all)
	}
	press(t, m, "x", "y", "ctrl+s", "ctrl+s")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "gh")
	if saved.Target != "" || !reflect.DeepEqual(saved.Targets, []string{"repos/octo/*", "orgs/octo/projects/1"}) {
		t.Fatalf("saved %q / %v, want two targets", saved.Target, saved.Targets)
	}
	if got := m.describe("gh"); !strings.Contains(got, "  2 targets  ") {
		t.Fatalf("list entry = %q", got)
	}

	// One target is written as target, none as neither key.
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "x", "y", "ctrl+s", "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh"); saved.Target != "orgs/octo/projects/1" || saved.Targets != nil {
		t.Fatalf("saved %q / %v, want one target", saved.Target, saved.Targets)
	}
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "x", "y", "ctrl+s", "ctrl+s")
	if m.fail != "" {
		t.Fatalf("saving no target failed: %s", m.fail)
	}
	if file := savedFile(t, path); strings.Contains(file, "target") {
		t.Fatalf("a GitHub connection without targets still names one:\n%s", file)
	}
}

// A changed list is unsaved input like every other row: leaving the form asks first.
func TestAChangedTargetListAsksBeforeLeaving(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, _ := toolsModel(t, reg, map[string]config.Connection{"gh": {Service: "wiki", Credential: "reader"}})
	openConnection(t, m, "gh")
	openTargetList(t, m)
	addTarget(t, m, "repos/octo/a")
	press(t, m, "ctrl+s", "esc")
	if m.screen != screenLeave {
		t.Fatalf("esc left a changed form without asking: screen %v", m.screen)
	}
}

// Telegram takes exactly one chat: a second one is refused where it is added, and no chat is refused where
// the connection is saved.
func TestARequiredSingleTargetIsKeptToOne(t *testing.T) {
	reg := targetsRegistry(t, telegram.Register)
	m, path := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, "chat")
	if hint := m.field(targetsLabel).hint; !strings.Contains(hint, "one chat ID at most") ||
		!strings.Contains(hint, "an empty list cannot be saved") {
		t.Fatalf("hint = %q, want the Telegram rules", hint)
	}
	openTargetList(t, m)
	addTarget(t, m, "-1001")
	press(t, m, "a")
	if m.targetEdit >= 0 || !strings.Contains(m.fail, "one chat ID only") {
		t.Fatalf("a second chat was offered: editing %d, error %q", m.targetEdit, m.fail)
	}
	press(t, m, "x", "y", "ctrl+s", "ctrl+s")
	if !strings.Contains(m.fail, "requires chat ID") {
		t.Fatalf("a connection without a chat was saved: error %q", m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "-1001")
	press(t, m, "ctrl+s", "ctrl+s")
	if saved := savedConnection(t, path, reg, "chat"); saved.Target != "-1001" || saved.Targets != nil {
		t.Fatalf("saved %q / %v, want the one chat", saved.Target, saved.Targets)
	}
}

// SeaTable needs at least one table and takes several, a table name with a comma included.
func TestARequiredTargetListTakesSeveral(t *testing.T) {
	reg := targetsRegistry(t, seatable.Register)
	m, path := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, "rows")
	press(t, m, "ctrl+s")
	if !strings.Contains(m.fail, "requires table") {
		t.Fatalf("a connection without a table was saved: error %q", m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "Kunden, Aktive")
	addTarget(t, m, "Tickets")
	press(t, m, "ctrl+s", "ctrl+s")
	if saved := savedConnection(t, path, reg, "rows"); !reflect.DeepEqual(saved.Targets,
		[]string{"Kunden, Aktive", "Tickets"}) {
		t.Fatalf("saved %q / %v, want both tables", saved.Target, saved.Targets)
	}
}

// The guided setup edits the targets with the same row, the same list and the same keys as the editor.
func TestTheGuidedSetupEditsTargetsAlike(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, nil)
	m.screen = screenNav
	press(t, m, "c", "enter", "ctrl+s", "ctrl+s")
	if m.wizard == nil || m.wizard.step != stepScope {
		t.Fatalf("the setup did not reach its scope step: %+v, error %q", m.wizard, m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "repos/octo/a")
	addTarget(t, m, "repos/octo/b")
	addTarget(t, m, "users/octo/projects/2")
	press(t, m, "enter")
	clearField(t, m)
	typeText(t, m, "users/octo/projects/3")
	press(t, m, "enter", "up", "x", "y", "ctrl+s", "right")
	if view := screenOf(m); !strings.Contains(view, "users/octo/projects/3") {
		t.Fatalf("the unfolded setup row does not list every target:\n%s", view)
	}
	press(t, m, "ctrl+s", "ctrl+s")
	if m.fail != "" || m.screen != screenSummary {
		t.Fatalf("the setup did not reach its summary: screen %v, error %q", m.screen, m.fail)
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("the setup reported %q", m.fail)
	}
	saved := savedConnection(t, path, reg, "github")
	if !reflect.DeepEqual(saved.Targets, []string{"repos/octo/a", "users/octo/projects/3"}) {
		t.Fatalf("saved %q / %v", saved.Target, saved.Targets)
	}
}
