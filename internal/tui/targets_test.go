package tui

import (
	"os"
	"reflect"
	"slices"
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

// hasConnection reports whether the configuration file holds the named connection.
func hasConnection(t *testing.T, path string, reg *capability.Registry, name string) bool {
	t.Helper()
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := cfg.Connections[name]
	return ok
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
	press(t, m, "esc", "esc", "k")
	if m.screen != screenForm {
		t.Fatalf("keeping the list left screen %v, want the form", m.screen)
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

	// Discarding leaves the row as it was, whatever changed in the list.
	press(t, m, "enter", "x", "y")
	addTarget(t, m, "repos/octo/c")
	press(t, m, "esc", "d")
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
	press(t, m, "x", "y", "ctrl+s")
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
	press(t, m, "x", "y", "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh"); saved.Target != "orgs/octo/projects/1" || saved.Targets != nil {
		t.Fatalf("saved %q / %v, want one target", saved.Target, saved.Targets)
	}
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "x", "y", "ctrl+s")
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
	press(t, m, "esc", "k", "esc")
	if m.screen != screenLeave || !strings.Contains(screenOf(m), "unsaved changes in gh") {
		t.Fatalf("esc left a changed form without asking: screen %v\n%s", m.screen, screenOf(m))
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
	press(t, m, "x", "y", "ctrl+s")
	if !strings.Contains(m.fail, "requires chat ID") {
		t.Fatalf("a connection without a chat was saved: error %q", m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "-1001")
	press(t, m, "ctrl+s")
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
	press(t, m, "ctrl+s")
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
	press(t, m, "enter", "up", "x", "y", "ctrl+s")
	if m.wizard.step != stepPermissions || m.fail != "" {
		t.Fatalf("ctrl+s in the setup list did not go on to the next step: step %d, error %q", m.wizard.step, m.fail)
	}
	if hasConnection(t, path, reg, "github") {
		t.Fatal("ctrl+s in the setup list wrote the unfinished connection")
	}
	press(t, m, "ctrl+b")
	focusField(t, m, targetsLabel)
	press(t, m, "right")
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

// One ctrl+s saves the connection from every state of the list: a typed target is taken first, and one the
// provider refuses stays typed with the reason while nothing is written.
func TestOneCtrlSSavesTheTargetsFromEveryState(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, map[string]config.Connection{"gh": {Service: "wiki", Credential: "reader"}})
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "a")
	typeText(t, m, "octo/a")
	press(t, m, "ctrl+s")
	if m.fail == "" || m.targetEdit < 0 || m.screen != screenTargets {
		t.Fatalf("a refused target was saved: screen %v, editing %d, error %q", m.screen, m.targetEdit, m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh"); saved.Target != "" || saved.Targets != nil {
		t.Fatalf("a refused target reached the file: %q / %v", saved.Target, saved.Targets)
	}

	clearField(t, m)
	typeText(t, m, "repos/octo/a")
	press(t, m, "ctrl+s")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("ctrl+s while typing did not save: screen %v, error %q", m.screen, m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh"); saved.Target != "repos/octo/a" {
		t.Fatalf("saved %q / %v, want the typed target", saved.Target, saved.Targets)
	}

	// An edited target is taken the same way, and an unanswered remove question keeps its target.
	openConnection(t, m, "gh")
	openTargetList(t, m)
	addTarget(t, m, "repos/octo/b")
	press(t, m, "x", "ctrl+s")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("ctrl+s at the remove question did not save: screen %v, error %q", m.screen, m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh"); !reflect.DeepEqual(saved.Targets,
		[]string{"repos/octo/a", "repos/octo/b"}) {
		t.Fatalf("saved %q / %v, want both targets", saved.Target, saved.Targets)
	}
}

// esc closes an unchanged list at once and asks about a changed one: keep the list, discard the changes, or
// go on editing. No other key answers, and nothing is written either way.
func TestEscAsksBeforeDroppingTargetChanges(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("esc on an unchanged list opened screen %v, want the form", m.screen)
	}

	openTargetList(t, m)
	addTarget(t, m, "repos/octo/b")
	press(t, m, "esc")
	if view := screenOf(m); m.screen != screenLeave || !strings.Contains(view, "target list changed") ||
		!strings.Contains(view, "k keep the list") {
		t.Fatalf("esc on a changed list did not ask: screen %v\n%s", m.screen, view)
	}
	press(t, m, "y", "n", "s", "enter")
	if m.screen != screenLeave {
		t.Fatalf("a stray key answered the question: screen %v", m.screen)
	}

	// esc goes on editing the list as it was left.
	press(t, m, "esc")
	if m.screen != screenTargets || len(m.targetList.all) != 2 {
		t.Fatalf("esc did not return to the changed list: screen %v, targets %v", m.screen, m.targetList.all)
	}

	// d drops the changes and returns to the form.
	press(t, m, "esc", "d")
	if got := m.field(targetsLabel).entries; m.screen != screenForm ||
		!reflect.DeepEqual(got, []string{"repos/octo/a"}) {
		t.Fatalf("discarding left screen %v with targets %v", m.screen, got)
	}

	// k keeps the list in the row; the form is changed, and still asks before it is left.
	openTargetList(t, m)
	addTarget(t, m, "repos/octo/b")
	press(t, m, "esc", "k")
	if got := m.field(targetsLabel).entries; m.screen != screenForm ||
		!reflect.DeepEqual(got, []string{"repos/octo/a", "repos/octo/b"}) {
		t.Fatalf("keeping left screen %v with targets %v", m.screen, got)
	}
	if saved := savedConnection(t, path, reg, "gh"); saved.Target != "repos/octo/a" || saved.Targets != nil {
		t.Fatalf("keeping the list wrote %q / %v", saved.Target, saved.Targets)
	}
	press(t, m, "esc")
	if m.screen != screenLeave || m.leaveFrom != screenForm {
		t.Fatalf("the changed form was left without asking: screen %v", m.screen)
	}
}

// The guided setup takes a typed target with ctrl+s and goes on to its next step without writing anything,
// and asks about a changed list on esc like the editor does.
func TestTheGuidedSetupTakesTargetsWithOneCtrlS(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, nil)
	m.screen = screenNav
	press(t, m, "c", "enter", "ctrl+s", "ctrl+s")
	if m.wizard == nil || m.wizard.step != stepScope {
		t.Fatalf("the setup did not reach its scope step: %+v, error %q", m.wizard, m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "repos/octo/a")
	press(t, m, "esc")
	if view := screenOf(m); m.screen != screenLeave || !strings.Contains(view, "target list changed") {
		t.Fatalf("esc on a changed setup list did not ask: screen %v\n%s", m.screen, view)
	}
	press(t, m, "d")
	if m.wizard == nil || m.screen != screenForm || len(m.field(targetsLabel).entries) != 0 {
		t.Fatalf("discarding the list left the setup: screen %v, wizard %v", m.screen, m.wizard != nil)
	}

	openTargetList(t, m)
	if view := screenOf(m); !strings.Contains(view, "ctrl+s next step") {
		t.Fatalf("the setup list does not say what ctrl+s does:\n%s", view)
	}
	press(t, m, "a")
	typeText(t, m, "repos/octo/b")
	press(t, m, "ctrl+s")
	if m.fail != "" || m.wizard.step != stepPermissions {
		t.Fatalf("ctrl+s while typing did not go on: step %d, error %q", m.wizard.step, m.fail)
	}
	if got := pageEntries(m.wizard.pages[stepScope], targetsLabel); !reflect.DeepEqual(got, []string{"repos/octo/b"}) {
		t.Fatalf("the scope step holds %v, want the typed target", got)
	}
	if hasConnection(t, path, reg, "github") {
		t.Fatal("the setup wrote its connection before the summary")
	}
}

// pageEntries is the target list a setup page holds in the row with label.
func pageEntries(page []field, label string) []string {
	for _, f := range page {
		if f.label == label {
			return f.entries
		}
	}
	return nil
}

// knownTargetsModel is a GitHub editor whose service wiki already serves a connection with targets, and
// whose second service zeta serves one more, whose targets belong to another host.
func knownTargetsModel(t *testing.T) (*Model, string, *capability.Registry) {
	t.Helper()
	reg := targetsRegistry(t, github.Register)
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"gh-a": {Service: "wiki", Credential: "reader",
			Targets: []string{"repos/octo/a", "repos/octo/b", "users/octo/projects/2"}},
	})
	mustNoError(t, m.cfg.SetService("zeta", config.Service{Provider: github.Provider,
		BaseURL: "https://zeta.example.invalid"}))
	mustNoError(t, m.cfg.SetConnection("gh-z", config.Connection{Service: "zeta", Credential: "reader",
		Target: "repos/elsewhere/x"}))
	return m, path, reg
}

// addRows are the rows the add menu or the builder shows now, as they read.
func addRows(m *Model) []string {
	var rows []string
	for _, key := range m.targetAdd.choices.matches {
		rows = append(rows, m.targetAdd.text[key])
	}
	return rows
}

// pickRow moves to the first shown row of the add menu or the builder that contains text.
func pickRow(t *testing.T, m *Model, text string) {
	t.Helper()
	for i, row := range addRows(m) {
		if strings.Contains(row, text) {
			m.targetAdd.choices.cursor = i
			return
		}
	}
	t.Fatalf("no row contains %q: %v", text, addRows(m))
}

// build adds a target through the builder of kind: each step takes the row containing the given text, or,
// for a step written as =value, the typed value.
func build(t *testing.T, m *Model, kind string, steps ...string) {
	t.Helper()
	press(t, m, "a")
	pickRow(t, m, "new "+kind)
	press(t, m, "enter")
	for _, step := range steps {
		if m.targetAdd == nil {
			t.Fatalf("the builder closed before step %q: error %q", step, m.fail)
		}
		if value, typed := strings.CutPrefix(step, "="); typed {
			typeText(t, m, value)
		} else {
			pickRow(t, m, step)
		}
		press(t, m, "enter")
	}
}

// Adding offers the targets other connections of the same service use, and only those not listed yet;
// the targets of another service are not offered. A picked target is saved as it is.
func TestAddingOffersTheTargetsOfTheSameService(t *testing.T) {
	m, path, reg := knownTargetsModel(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, "gh")
	focusField(t, m, "service")
	if m.fieldValue("service") != "wiki" {
		t.Fatalf("service = %q, want wiki", m.fieldValue("service"))
	}
	openTargetList(t, m)
	press(t, m, "a")
	if m.targetAdd == nil {
		t.Fatalf("a did not open the add menu: editing %d", m.targetEdit)
	}
	view := screenOf(m)
	if !strings.Contains(view, "repos/octo/a  · used by gh-a") || strings.Contains(view, "elsewhere") {
		t.Fatalf("the menu does not offer the targets of wiki alone:\n%s", view)
	}
	// Typing filters; the typed line comes first and a known target is picked below it.
	typeText(t, m, "octo/b")
	if rows := addRows(m); len(rows) != 2 || !strings.HasPrefix(rows[0], `use as typed: "octo/b"`) ||
		!strings.HasPrefix(rows[1], "repos/octo/b") {
		t.Fatalf("filtered rows = %v", rows)
	}
	press(t, m, "down", "enter")
	if m.targetAdd != nil || !reflect.DeepEqual(m.targetList.all, []string{"repos/octo/b"}) {
		t.Fatalf("picking did not take the target: %v, error %q", m.targetList.all, m.fail)
	}
	press(t, m, "a")
	if rows := strings.Join(addRows(m), "\n"); strings.Contains(rows, "repos/octo/b") ||
		!strings.Contains(rows, "users/octo/projects/2") {
		t.Fatalf("the menu offers a listed target again or lost another:\n%s", rows)
	}
	press(t, m, "esc", "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh"); m.fail != "" || saved.Target != "repos/octo/b" {
		t.Fatalf("saved %q / %v, error %q", saved.Target, saved.Targets, m.fail)
	}
}

// The builder asks for the placeholders of every GitHub kind one by one, suggests the owners, repositories
// and numbers the service already uses there, offers * as all, and writes the usual path.
func TestTheBuilderWritesEveryGitHubKind(t *testing.T) {
	m, path, reg := knownTargetsModel(t)
	openConnection(t, m, "gh-a")
	openTargetList(t, m)

	press(t, m, "a")
	pickRow(t, m, "new repository")
	press(t, m, "enter")
	if view := screenOf(m); !strings.Contains(view, "New repository · OWNER") ||
		!reflect.DeepEqual(addRows(m), []string{"octo"}) {
		t.Fatalf("the owner step lacks its suggestion: %v\n%s", addRows(m), view)
	}
	press(t, m, "enter")
	if rows := addRows(m); !reflect.DeepEqual(rows, []string{"a", "b", "* (all)"}) {
		t.Fatalf("the repository step offers %v", rows)
	}
	typeText(t, m, "cli")
	press(t, m, "enter")

	build(t, m, "repository", "=new-owner", "* (all)")
	build(t, m, "project", "orgs", "=octo-org", "=7")
	build(t, m, "project", "users", "octo", "2")
	if !strings.Contains(m.fail, "already in the list") {
		t.Fatalf("a listed project was built again: %v, error %q", m.targetList.all, m.fail)
	}
	press(t, m, "esc")
	build(t, m, "project", "users", "octo", "* (all)")
	build(t, m, "owner", "users", "octo")
	if m.fail != "" {
		t.Fatalf("building reported %q", m.fail)
	}
	press(t, m, "ctrl+s")
	want := []string{"repos/octo/a", "repos/octo/b", "users/octo/projects/2", "repos/octo/cli", "repos/new-owner/*",
		"orgs/octo-org/projects/7", "users/octo/projects/*", "users/octo"}
	if saved := savedConnection(t, path, reg, "gh-a"); m.fail != "" || !reflect.DeepEqual(saved.Targets, want) {
		t.Fatalf("saved %v, error %q", saved.Targets, m.fail)
	}
}

// The builder steps back on backspace, and what the provider refuses stays typed with the reason, so it
// can be fixed or cancelled like a typed target.
func TestTheBuilderStepsBackAndHandsOverWhatIsRefused(t *testing.T) {
	m, _, _ := knownTargetsModel(t)
	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	build(t, m, "project", "orgs", "=octo-org")
	press(t, m, "backspace")
	if view := screenOf(m); !strings.Contains(view, "New project · LOGIN") {
		t.Fatalf("backspace did not step back to the login:\n%s", view)
	}
	press(t, m, "backspace", "backspace")
	if m.targetAdd == nil || m.targetAdd.kind != -1 {
		t.Fatal("backspace did not step back to the menu")
	}
	pickRow(t, m, "new project")
	press(t, m, "enter")
	pickRow(t, m, "orgs")
	press(t, m, "enter")
	typeText(t, m, "octo-org")
	press(t, m, "enter")
	typeText(t, m, "x")
	press(t, m, "enter")
	if m.targetAdd != nil || m.targetEdit < 0 || m.targetInput.Value() != "orgs/octo-org/projects/x" ||
		!strings.Contains(m.fail, "project number") {
		t.Fatalf("a refused build was not handed over: editing %d %q, error %q", m.targetEdit,
			m.targetInput.Value(), m.fail)
	}
	press(t, m, "esc")
	if len(m.targetList.all) != 3 {
		t.Fatalf("a refused build reached the list: %v", m.targetList.all)
	}
}

// ctrl+s saves from the menu and the builder too: a typed line is taken as typed, a builder completes on
// its last step, and an incomplete one says what is missing while nothing is written or dropped.
func TestCtrlSSavesFromTheMenuAndTheBuilder(t *testing.T) {
	m, path, reg := knownTargetsModel(t)
	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	press(t, m, "a")
	pickRow(t, m, "new repository")
	press(t, m, "enter")
	press(t, m, "ctrl+s")
	if m.screen != screenTargets || m.targetAdd == nil || !strings.Contains(m.fail, "not complete yet: choose OWNER") {
		t.Fatalf("an incomplete build was saved or dropped: screen %v, error %q", m.screen, m.fail)
	}
	press(t, m, "enter")
	typeText(t, m, "cli")
	press(t, m, "ctrl+s")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("ctrl+s on the last step did not save: screen %v, error %q", m.screen, m.fail)
	}
	if saved := savedConnection(t, path, reg, "gh-a"); !slices.Contains(saved.Targets, "repos/octo/cli") {
		t.Fatalf("saved %v, want the built target", saved.Targets)
	}

	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	press(t, m, "a")
	typeText(t, m, "repos/octo")
	press(t, m, "ctrl+s")
	if m.targetEdit < 0 || m.fail == "" {
		t.Fatalf("a refused typed line was saved: editing %d, error %q", m.targetEdit, m.fail)
	}
	typeText(t, m, "/d")
	press(t, m, "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh-a"); m.fail != "" || !slices.Contains(saved.Targets, "repos/octo/d") {
		t.Fatalf("saved %v, error %q", saved.Targets, m.fail)
	}
}

// A provider without kinds still offers the targets of its service, and a typed one as before.
func TestAProviderWithoutKindsOffersKnownTargets(t *testing.T) {
	reg := targetsRegistry(t, seatable.Register)
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"rows-a": {Service: "wiki", Credential: "reader", Targets: []string{"Kunden", "Tickets"}}})
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	typeText(t, m, "rows")
	openTargetList(t, m)
	press(t, m, "a")
	if rows := strings.Join(addRows(m), "\n"); strings.Contains(rows, "new ") || !strings.Contains(rows, "Kunden") {
		t.Fatalf("the menu offers %q", rows)
	}
	pickRow(t, m, "Tickets")
	press(t, m, "enter")
	addTarget(t, m, "Neu")
	press(t, m, "ctrl+s")
	if saved := savedConnection(t, path, reg, "rows"); !reflect.DeepEqual(saved.Targets, []string{"Tickets", "Neu"}) {
		t.Fatalf("saved %v, error %q", saved.Targets, m.fail)
	}
}

// The guided setup offers the known targets and the builder alike, and ctrl+s goes on to its next step.
func TestTheGuidedSetupOffersKnownTargetsAndTheBuilder(t *testing.T) {
	m, path, reg := knownTargetsModel(t)
	m.screen = screenNav
	press(t, m, "c", "enter", "ctrl+s", "ctrl+s")
	if m.wizard == nil || m.wizard.step != stepScope {
		t.Fatalf("the setup did not reach its scope step: %+v, error %q", m.wizard, m.fail)
	}
	openTargetList(t, m)
	press(t, m, "a")
	pickRow(t, m, "repos/octo/a")
	press(t, m, "enter")
	press(t, m, "a")
	pickRow(t, m, "new repository")
	press(t, m, "enter", "enter")
	typeText(t, m, "c")
	press(t, m, "ctrl+s")
	if m.fail != "" || m.wizard.step != stepPermissions {
		t.Fatalf("ctrl+s in the builder did not go on: step %d, error %q", m.wizard.step, m.fail)
	}
	if got := pageEntries(m.wizard.pages[stepScope], targetsLabel); !reflect.DeepEqual(got,
		[]string{"repos/octo/a", "repos/octo/c"}) {
		t.Fatalf("the scope step holds %v", got)
	}
	if hasConnection(t, path, reg, "github") {
		t.Fatal("the setup wrote its connection before the summary")
	}
}

// A pattern is never taken without an explicit choice: * is never the selected row when a step opens,
// enter on the resting line takes nothing, and ctrl+s takes only a typed value, never a merely selected
// row, in the builder and in the menu alike.
func TestAPatternNeedsAnExplicitChoice(t *testing.T) {
	m, path, reg := knownTargetsModel(t)
	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	unchanged := func(when string) {
		t.Helper()
		if saved := savedConnection(t, path, reg, "gh-a"); len(saved.Targets) != 3 || m.screen != screenTargets {
			t.Fatalf("%s: screen %v, saved %v", when, m.screen, saved.Targets)
		}
	}

	build(t, m, "project", "users", "=cli")
	if rows := addRows(m); !reflect.DeepEqual(rows, []string{"(type a value, or choose a row)", "* (all)"}) ||
		m.targetAdd.choices.cursor != 0 {
		t.Fatalf("the number step offers %v on row %d", rows, m.targetAdd.choices.cursor)
	}
	press(t, m, "ctrl+s")
	if m.targetAdd == nil || !strings.Contains(m.fail, "not complete yet: choose NUMBER or * (all)") {
		t.Fatalf("ctrl+s without a value did not say what is missing: error %q", m.fail)
	}
	unchanged("ctrl+s on the pattern step")
	press(t, m, "enter")
	if m.targetAdd == nil || !strings.Contains(m.fail, "type NUMBER or * (all) first") {
		t.Fatalf("enter on the resting line took something: error %q", m.fail)
	}
	press(t, m, "down", "enter")
	if !slices.Contains(m.targetList.all, "users/cli/projects/*") {
		t.Fatalf("an explicit * was not taken: %v, error %q", m.targetList.all, m.fail)
	}

	// Suggestions come before *, and a selected suggestion is no choice for ctrl+s either.
	press(t, m, "a")
	pickRow(t, m, "new repository")
	press(t, m, "enter", "enter")
	if rows := addRows(m); rows[len(rows)-1] != "* (all)" || m.targetAdd.text[m.targetAdd.choices.matches[0]] != "a" {
		t.Fatalf("the repository step offers %v", rows)
	}
	press(t, m, "ctrl+s")
	if m.targetAdd == nil || !strings.Contains(m.fail, "not complete yet") {
		t.Fatalf("ctrl+s took the selected suggestion: error %q", m.fail)
	}
	unchanged("ctrl+s on a selected suggestion")
	typeText(t, m, "*")
	press(t, m, "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh-a"); m.fail != "" || !slices.Contains(saved.Targets, "repos/octo/*") ||
		!slices.Contains(saved.Targets, "users/cli/projects/*") {
		t.Fatalf("a typed * was not saved: %v, error %q", saved.Targets, m.fail)
	}

	// In the menu, ctrl+s saves the list without the selected row.
	openConnection(t, m, "gh-a")
	openTargetList(t, m)
	press(t, m, "a")
	pickRow(t, m, "new owner")
	press(t, m, "ctrl+s")
	if saved := savedConnection(t, path, reg, "gh-a"); m.fail != "" || m.screen != screenList || len(saved.Targets) != 5 {
		t.Fatalf("ctrl+s in the menu took its selected row: screen %v, saved %v, error %q", m.screen, saved.Targets,
			m.fail)
	}
}
