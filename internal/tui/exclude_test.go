package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// excluderRegistry registers a provider that reads exclusions, which no real provider declares.
func excluderRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "excluder", Name: "Excluder",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "token"}},
		Target: config.TargetMetadata{Label: "repository", Required: true, Multiple: true, Wildcard: "*",
			Kinds: []config.TargetKind{{Name: "repo", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}, ExcludeForms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}}},
			Validate: func(target string) error {
				if target == "*" || strings.HasPrefix(target, "repos/") {
					return nil
				}
				return errors.New("a target must name a repository")
			}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func excludedModel(t *testing.T) (*Model, string) {
	return toolsModel(t, excluderRegistry(t), map[string]config.Connection{"ex": {
		Service: "wiki", Credential: "reader", Targets: []string{"repos/o/*"},
		Exclude:      []string{"repos/o/secret"},
		TargetLabels: map[string]string{"repos/o/*": "All", "repos/o/secret": "Hidden"},
	}})
}

// The form does not edit exclusions, so saving it, also under another name, must carry them with their labels
// over; dropping them would widen the connection.
func TestSavingTheFormKeepsExclusionsAndTheirLabels(t *testing.T) {
	m, path := excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "ex")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, excluderRegistry(t), "ex")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) || saved.TargetLabels["repos/o/secret"] != "Hidden" ||
		saved.TargetLabels["repos/o/*"] != "All" {
		t.Errorf("saved exclude %v labels %v", saved.Exclude, saved.TargetLabels)
	}

	m, path = excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	m.screen = screenList
	pump(t, m, "p")
	clearField(t, m)
	typeText(t, m, "ex-copy")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved = savedConnection(t, path, excluderRegistry(t), "ex-copy")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) || saved.TargetLabels["repos/o/secret"] != "Hidden" {
		t.Errorf("copy exclude %v labels %v", saved.Exclude, saved.TargetLabels)
	}
}

func TestApprovalDetailShowsExcludeOnlyWhenThereIsSomething(t *testing.T) {
	rows := strings.Join(approvalDetailRows(approval.Change{Fields: []approval.FieldChange{
		{Field: approval.FieldExclude, Before: "(none)", After: "a", Added: []string{"a"}},
	}}), "\n")
	if !strings.Contains(rows, "exclude") || !strings.Contains(rows, "+ a") {
		t.Errorf("rows lack the exclude change:\n%s", rows)
	}
	rows = strings.Join(approvalDetailRows(approval.Change{After: vault.Scope{Targets: []string{"a"}}}), "\n")
	if strings.Contains(rows, "exclude") {
		t.Errorf("rows show an empty exclude list:\n%s", rows)
	}
}

func TestExcludeProblemMapsToTheTargetRow(t *testing.T) {
	m, _ := excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	openConnection(t, m, "ex")
	p := config.Problem{Section: config.SectionConnections, Name: "ex", Field: "exclude", Code: config.CodeDuplicateExclude}
	if got := m.problemRow(p, "ex"); got != targetsLabel {
		t.Errorf("problemRow() = %q, want %q", got, targetsLabel)
	}
}

// oneTargetRegistry registers a provider that takes one target and reads exclusions.
func oneTargetRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "solo", Name: "Solo",
		Target: config.TargetMetadata{Label: "folder", Wildcard: "*",
			Kinds: []config.TargetKind{{Name: "folder", Forms: []string{"f/NAME", "f/NAME/PATH"}, ExcludeForms: []string{"f/NAME", "f/NAME/PATH"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// addAsExclusion adds one entry through the row of the add menu.
func addAsExclusion(t *testing.T, m *Model, value string) {
	t.Helper()
	press(t, m, "n")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	if !m.targetAsExclusion {
		t.Fatalf("the menu row did not switch to exclusions:\n%s", screenOf(m))
	}
	typeText(t, m, value)
	press(t, m, "enter")
}

func exclusionState(m *Model) (reach, out []string) {
	return reachableOf(m.targetList.all, m.targetExcluded), excludesOf(m.targetList.all, m.targetExcluded)
}

func TestSpaceSwitchesATargetRowBetweenReachableAndExcluded(t *testing.T) {
	m, path := toolsModel(t, excluderRegistry(t), map[string]config.Connection{"ex": {
		Service: "wiki", Credential: "reader", Targets: []string{"repos/o/a", "repos/o/b", "repos/o/c"}}})
	openConnection(t, m, "ex")
	openTargetList(t, m)
	if !strings.Contains(screenOf(m), "space switch") {
		t.Fatalf("the footer does not name space:\n%s", screenOf(m))
	}
	press(t, m, "down", " ")
	if !m.targetExcluded["repos/o/b"] || !strings.Contains(screenOf(m), "[excluded] repos/o/b") {
		t.Fatalf("space did not exclude the row:\n%s", screenOf(m))
	}
	press(t, m, "r")
	typeText(t, m, "Hidden")
	press(t, m, "enter", "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, excluderRegistry(t), "ex")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/b"}) ||
		!reflect.DeepEqual(saved.TargetValues(), []string{"repos/o/a", "repos/o/c"}) || saved.TargetLabels["repos/o/b"] != "Hidden" {
		t.Fatalf("saved targets %v exclude %v labels %v", saved.TargetValues(), saved.Exclude, saved.TargetLabels)
	}

	// Switched back, the row is a target again.
	m, path = toolsModel(t, excluderRegistry(t), map[string]config.Connection{"ex": {
		Service: "wiki", Credential: "reader", Targets: []string{"repos/o/a", "repos/o/c"}, Exclude: []string{"repos/o/b"}}})
	openConnection(t, m, "ex")
	openTargetList(t, m)
	press(t, m, "down", "down", " ", "f2")
	saved = savedConnection(t, path, excluderRegistry(t), "ex")
	if len(saved.Exclude) != 0 || !reflect.DeepEqual(saved.TargetValues(), []string{"repos/o/a", "repos/o/c", "repos/o/b"}) {
		t.Fatalf("saved targets %v exclude %v", saved.TargetValues(), saved.Exclude)
	}
}

// plainRegistry is excluderRegistry without any excludable kind.
func plainRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "plain", Name: "Plain",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "token"}},
		Target: config.TargetMetadata{Label: "repository", Required: true, Multiple: true,
			Kinds: []config.TargetKind{{Name: "repo", Forms: []string{"repos/OWNER/REPO"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestNoSwitchWithoutAnExclusionDeclaration(t *testing.T) {
	m, _ := toolsModel(t, plainRegistry(t), map[string]config.Connection{
		"plain": {Service: "wiki", Credential: "reader", Targets: []string{"repos/o/a", "repos/o/b"}}})
	openConnection(t, m, "plain")
	openTargetList(t, m)
	press(t, m, " ")
	if len(m.targetExcluded) != 0 || m.status != "" || strings.Contains(screenOf(m), "exclu") || strings.Contains(screenOf(m), "space switch") {
		t.Fatalf("a provider without exclusions shows the switch:\n%s", screenOf(m))
	}
	press(t, m, "n")
	for _, row := range addRows(m) {
		if strings.Contains(row, "exclusion") {
			t.Fatalf("the add menu offers exclusions: %q", row)
		}
	}
	press(t, m, "esc")
	if reason := m.addExclusionUnavailable(); reason == "" {
		t.Fatal("the command is available for a provider without exclusions")
	}
}

func TestAOneTargetProviderTakesATargetAndSeveralExclusions(t *testing.T) {
	reg := oneTargetRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"solo": {Service: "wiki", Credential: "reader", Target: "f/docs"}})
	openConnection(t, m, "solo")
	openTargetList(t, m)
	// A second target is refused, an exclusion is not.
	press(t, m, "n")
	typeText(t, m, "f/other")
	press(t, m, "enter")
	if m.fail == "" || m.reachableCount() != 1 {
		t.Fatalf("a second target was taken: %v, error %q", m.targetList.all, m.fail)
	}
	press(t, m, "esc", "d")
	// The existing target does not block adding exclusions, through the menu row or the command.
	addAsExclusion(t, m, "f/docs/private")
	pump(t, m, ":")
	typeText(t, m, "add exclusion")
	pump(t, m, "enter")
	if m.targetAdd == nil || !m.targetAsExclusion {
		t.Fatalf("the command did not open the add menu for exclusions:\n%s", screenOf(m))
	}
	typeText(t, m, "f/docs/secret")
	press(t, m, "enter")
	if reach, out := exclusionState(m); !reflect.DeepEqual(reach, []string{"f/docs"}) ||
		!reflect.DeepEqual(out, []string{"f/docs/private", "f/docs/secret"}) {
		t.Fatalf("list %v / %v, error %q", reach, out, m.fail)
	}
	press(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "solo")
	if saved.Target != "f/docs" || !reflect.DeepEqual(saved.Exclude, []string{"f/docs/private", "f/docs/secret"}) {
		t.Fatalf("saved %q / %v", saved.Target, saved.Exclude)
	}
}

func TestAWildcardTargetWithExclusionLines(t *testing.T) {
	reg := excluderRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"ex": {Service: "wiki", Credential: "reader", Target: "*"}})
	openConnection(t, m, "ex")
	openTargetList(t, m)
	addAsExclusion(t, m, "repos/o/secret")
	// The wildcard row is never excludable.
	press(t, m, "up", " ")
	if m.fail == "" || m.targetExcluded["*"] {
		t.Fatalf("the wildcard was excluded: %v", m.targetExcluded)
	}
	press(t, m, "f2")
	saved := savedConnection(t, path, reg, "ex")
	if saved.Target != "*" || !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) {
		t.Fatalf("saved %q / %v, error %q", saved.Target, saved.Exclude, m.fail)
	}
	// An exclusion above every target is valid and the row says how many there are.
	if row := m.field(targetsLabel); targetsSummary(row.entries, row.excluded) == "" {
		t.Fatal("empty summary")
	}
}

func TestExclusionsDoNotChangeWhatTheFormOffersOrReaches(t *testing.T) {
	reg := reachRegistry(t)
	m, _ := toolsModel(t, reg, map[string]config.Connection{"route": {Service: "wiki", Credential: "reader", Tools: []string{}}})
	openEntryForm(t, m, sectionConnections, "route")
	before := m.toolText("fake.trash.list")
	row := m.field(targetsLabel)
	row.entries, row.excluded = []string{"folder/secret/x"}, map[string]bool{"folder/secret/x": true}
	if got := m.toolText("fake.trash.list"); got != before {
		t.Fatalf("toolText with only exclusions = %q, without = %q", got, before)
	}
	row.entries, row.excluded = []string{"drive/d1", "folder/secret/x"}, map[string]bool{"folder/secret/x": true}
	if got := m.toolText("fake.trash.list"); strings.Contains(got, "outside-targets") {
		t.Fatalf("an exclusion hid the reach of a target: %q", got)
	}
}

func TestACopyFromATemplateKeepsItsExclusions(t *testing.T) {
	reg := excluderRegistry(t)
	m, path := excludedModel(t)
	openSectionByName(t, m, sectionConnections)
	m.screen = screenNav
	press(t, m, "g", "down", "enter", "enter")
	if m.wizard == nil || m.wizard.template != "ex" {
		t.Fatalf("no setup from the template: %q", m.fail)
	}
	for m.wizard.step < stepScope {
		press(t, m, "f2")
	}
	row := targetRowOf(m.wizard.pages[stepScope])
	if row == nil || !reflect.DeepEqual(excludesOf(row.entries, row.excluded), []string{"repos/o/secret"}) ||
		!reflect.DeepEqual(row.reachable(), []string{"repos/o/*"}) {
		t.Fatalf("the template's exclusions are not in the setup: %+v", row)
	}
	press(t, m, "f2", "f2")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("setup failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "ex-copy")
	if !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) || saved.TargetLabels["repos/o/secret"] != "Hidden" {
		t.Fatalf("copy %v labels %v", saved.Exclude, saved.TargetLabels)
	}
}

func TestTheGuidedSetupSavesExclusions(t *testing.T) {
	reg := excluderRegistry(t)
	m, path := toolsModel(t, reg, nil)
	m.screen = screenNav
	press(t, m, "g", "enter", "f2", "f2")
	if m.wizard == nil || m.wizard.step != stepScope {
		t.Fatalf("the setup did not reach its scope step: error %q", m.fail)
	}
	openTargetList(t, m)
	addTarget(t, m, "*")
	addAsExclusion(t, m, "repos/o/secret")
	press(t, m, "f2", "f2")
	if m.screen != screenSummary || !strings.Contains(screenOf(m), "repos/o/secret") {
		t.Fatalf("the summary lacks the exclusion: screen %v error %q\n%s", m.screen, m.fail, screenOf(m))
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("setup failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "excluder")
	if saved.Target != "*" || !reflect.DeepEqual(saved.Exclude, []string{"repos/o/secret"}) {
		t.Fatalf("saved %q / %v", saved.Target, saved.Exclude)
	}
}
