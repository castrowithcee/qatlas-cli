package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// helpRoundTrip presses ? on the current screen, expects the help, and expects esc to return to the same
// screen.
func helpRoundTrip(t *testing.T, m *Model, what string) {
	t.Helper()
	from := m.screen
	press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("? on %s opened screen %v, want the help:\n%s", what, m.screen, screenOf(m))
	}
	press(t, m, "esc")
	if m.screen != from {
		t.Fatalf("esc in the help from %s returned to screen %v, want %v", what, m.screen, from)
	}
}

func TestVimKeysMoveLikeTheArrowsOnEveryKindOfScreen(t *testing.T) {
	t.Run("sidebar and list", func(t *testing.T) {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		seedServices(t, m, "a-one", "b-two", "c-three")
		m.screen = screenNav
		press(t, m, "j")
		if m.section != sectionCredentials || m.screen != screenNav {
			t.Fatalf("j = section %v screen %v, want Credentials on the sidebar", m.section, m.screen)
		}
		press(t, m, "k", "end")
		if m.section != sectionLogs {
			t.Fatalf("end = section %v, want the last section", m.section)
		}
		press(t, m, "home")
		if m.section != sectionServices {
			t.Fatalf("home = section %v, want the first section", m.section)
		}
		press(t, m, "l")
		if m.screen != screenList {
			t.Fatalf("l = screen %v, want the list", m.screen)
		}
		press(t, m, "j")
		if name := selectedName(t, m); name != "b-two" {
			t.Fatalf("j selected %q, want b-two", name)
		}
		press(t, m, "k")
		if name := selectedName(t, m); name != "a-one" {
			t.Fatalf("k selected %q, want a-one", name)
		}
		press(t, m, "l")
		if m.screen != screenList {
			t.Fatalf("l in the list = screen %v, want the list unchanged", m.screen)
		}
		press(t, m, "h")
		if m.screen != screenNav {
			t.Fatalf("h in the list = screen %v, want the sidebar", m.screen)
		}
	})

	t.Run("list filter reads the letters as text", func(t *testing.T) {
		m, _, _ := newModel(t)
		seedServices(t, m, "a-one", "b-two")
		m.screen = screenList
		press(t, m, "/", "j", "k")
		if got := m.list.query(); got != "jk" {
			t.Fatalf("filter = %q, want the typed letters", got)
		}
	})

	t.Run("form rows", func(t *testing.T) {
		m, _, _ := newModel(t)
		addService(t, m, "wiki", "https://wiki.example.invalid")
		addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
		openSectionByName(t, m, sectionConnections)
		pressNew(t, m)
		clearField(t, m)
		typeText(t, m, "hjkl")
		if got := m.fieldValue("name"); got != "hjkl" {
			t.Fatalf("name = %q, want the letters typed as text", got)
		}
		press(t, m, "tab")
		if m.fields[m.focus].label != providerLabel {
			t.Fatalf("tab reached %q, want the provider row", m.fields[m.focus].label)
		}
		press(t, m, "j")
		if m.fields[m.focus].label == providerLabel {
			t.Fatal("j on a choice row did not move to the next row")
		}
		press(t, m, "k")
		if m.fields[m.focus].label != providerLabel {
			t.Fatalf("k reached %q, want the provider row again", m.fields[m.focus].label)
		}
	})

	t.Run("help topics and text", func(t *testing.T) {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
		press(t, m, "?", "l")
		if m.helpTopic != 1 {
			t.Fatalf("l = topic %d, want 1", m.helpTopic)
		}
		press(t, m, "h")
		if m.helpTopic != 0 {
			t.Fatalf("h = topic %d, want 0", m.helpTopic)
		}
		press(t, m, "j")
		if m.helpOffset != 1 {
			t.Fatalf("j = offset %d, want 1", m.helpOffset)
		}
		press(t, m, "k")
		if m.helpOffset != 0 {
			t.Fatalf("k = offset %d, want 0", m.helpOffset)
		}
	})

	t.Run("target and path lists", func(t *testing.T) {
		m, _, _ := knownTargetsModel(t)
		openEntryForm(t, m, sectionConnections, "gh-a")
		openTargetList(t, m)
		press(t, m, "j")
		if m.targetList.cursor != 1 {
			t.Fatalf("j = cursor %d, want 1", m.targetList.cursor)
		}
		press(t, m, "k", "l", "h")
		if m.targetList.cursor != 0 || m.screen != screenTargets {
			t.Fatalf("k, l, h = cursor %d screen %v, want the first target on the list", m.targetList.cursor, m.screen)
		}
		press(t, m, "esc")

		dir := t.TempDir()
		p, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{
			"wiki": {Service: "wiki", Credential: "reader", Paths: []string{dir, dir + "/sub"}},
		})
		openEntryForm(t, p, sectionConnections, "wiki")
		openPathList(t, p)
		press(t, p, "j")
		if p.pathList.cursor != 1 {
			t.Fatalf("j = cursor %d, want 1", p.pathList.cursor)
		}
		press(t, p, "k", "l", "h")
		if p.pathList.cursor != 0 || p.screen != screenPaths {
			t.Fatalf("k, l, h = cursor %d screen %v, want the first path on the list", p.pathList.cursor, p.screen)
		}
	})

	t.Run("typed target and path lines", func(t *testing.T) {
		m, _, _ := knownTargetsModel(t)
		openEntryForm(t, m, sectionConnections, "gh-a")
		openTargetList(t, m)
		press(t, m, "a")
		typeText(t, m, "jk")
		if got := m.targetAdd.choices.query(); got != "jk" {
			t.Fatalf("the add menu line = %q, want the letters as text", got)
		}
	})

	t.Run("template choice", func(t *testing.T) {
		m, _ := templateModel(t)
		openSectionByName(t, m, sectionConnections)
		press(t, m, "enter") // the list
		m.screen = screenList
		press(t, m, "n")
		if m.screen != screenTemplate {
			t.Fatalf("n = screen %v, want the template question", m.screen)
		}
		press(t, m, "j")
		if m.template.index != 1 {
			t.Fatalf("j = index %d, want 1", m.template.index)
		}
		press(t, m, "k")
		if m.template.index != 0 {
			t.Fatalf("k = index %d, want 0", m.template.index)
		}
		press(t, m, "end")
		if m.template.index != 1 {
			t.Fatalf("end = index %d, want 1", m.template.index)
		}
		press(t, m, "home")
		if m.template.index != 0 {
			t.Fatalf("home = index %d, want 0", m.template.index)
		}
		press(t, m, "j", "enter", "j", "k")
		if !m.template.picking || m.templateList.query() != "jk" {
			t.Fatalf("picking = %v search = %q, want the letters as search text", m.template.picking,
				m.templateList.query())
		}
	})

	t.Run("setup summary", func(t *testing.T) {
		m, _, _, _, _ := newStoreModel(t)
		walkSetup(t, m, stepSummary)
		if m.screen != screenSummary {
			t.Fatalf("screen = %v, want the summary", m.screen)
		}
		press(t, m, "j", "k", "h", "l")
		if m.screen != screenSummary || m.fail != "" {
			t.Fatalf("the movement keys changed the summary: screen %v error %q", m.screen, m.fail)
		}
		helpRoundTrip(t, m, "the setup summary")
	})
}

func TestQuestionMarkOpensTheHelpWhereNothingIsTyped(t *testing.T) {
	t.Run("provider table", func(t *testing.T) {
		m, _, _ := newModel(t)
		openSectionByName(t, m, sectionServices)
		pressNew(t, m)
		focusField(t, m, providerLabel)
		press(t, m, "enter")
		if m.screen != screenProviders {
			t.Fatalf("screen = %v, want the provider table", m.screen)
		}
		helpRoundTrip(t, m, "the provider table")
		press(t, m, "b", "?")
		if m.screen != screenProviders || m.providers.list.query() != "b?" {
			t.Fatalf("? with a search typed: screen %v search %q, want it as search text", m.screen,
				m.providers.list.query())
		}
	})

	t.Run("target list, builder and path list", func(t *testing.T) {
		m, _, _ := knownTargetsModel(t)
		openEntryForm(t, m, sectionConnections, "gh-a")
		openTargetList(t, m)
		helpRoundTrip(t, m, "the target list")
		press(t, m, "d")
		if !m.targetRemove {
			t.Fatal("d did not ask to remove")
		}
		helpRoundTrip(t, m, "the remove question")
		press(t, m, "n", "a")
		if m.targetAdd == nil {
			t.Fatal("a did not open the add menu")
		}
		helpRoundTrip(t, m, "the add menu")
		press(t, m, "x", "?")
		if m.screen != screenTargets || m.targetAdd.choices.query() != "x?" {
			t.Fatalf("? with a line typed: screen %v line %q, want it as text", m.screen, m.targetAdd.choices.query())
		}
		press(t, m, "esc", "esc") // the leave question
		m.screen = screenTargets
		m.targetAdd = nil

		dir := t.TempDir()
		p, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{
			"wiki": {Service: "wiki", Credential: "reader", Paths: []string{dir}},
		})
		openEntryForm(t, p, sectionConnections, "wiki")
		openPathList(t, p)
		helpRoundTrip(t, p, "the path list")
		press(t, p, "a", "?")
		if p.screen != screenPaths || !strings.Contains(p.pathInput.Value(), "?") {
			t.Fatalf("? in the typed path: screen %v value %q, want it as text", p.screen, p.pathInput.Value())
		}
	})

	t.Run("template question and search", func(t *testing.T) {
		m, _ := templateModel(t)
		openSectionByName(t, m, sectionConnections)
		m.screen = screenList
		press(t, m, "n")
		helpRoundTrip(t, m, "the template question")
		press(t, m, "down", "enter")
		if !m.template.picking {
			t.Fatal("enter on the second row did not open the search")
		}
		helpRoundTrip(t, m, "the empty template search")
		press(t, m, "g", "?")
		if m.templateList.query() != "g?" {
			t.Fatalf("search = %q, want ? as text once a search is typed", m.templateList.query())
		}
	})

	t.Run("confirmations and the leave question", func(t *testing.T) {
		m, _, _ := newModel(t)
		seedServices(t, m, "a-one")
		openSectionByName(t, m, sectionServices)
		m.screen = screenList
		press(t, m, "d")
		if m.screen != screenConfirm {
			t.Fatalf("d = screen %v, want the confirmation", m.screen)
		}
		helpRoundTrip(t, m, "the remove question")
		press(t, m, "n")
		if m.screen != screenList {
			t.Fatalf("n = screen %v, want the list", m.screen)
		}

		pressNew(t, m)
		typeText(t, m, "changed")
		press(t, m, "?")
		if m.screen != screenForm || !strings.HasSuffix(m.fieldValue("name"), "?") {
			t.Fatalf("? in a text row: screen %v name %q, want it as text", m.screen, m.fieldValue("name"))
		}
		press(t, m, "esc")
		if m.screen != screenLeave {
			t.Fatalf("esc = screen %v, want the leave question", m.screen)
		}
		helpRoundTrip(t, m, "the leave question")
		if m.screen != screenLeave {
			t.Fatalf("screen = %v, want the leave question again", m.screen)
		}
	})

	t.Run("pickers keep ? as search text", func(t *testing.T) {
		m, _ := toolsModel(t, wikiRegistry(t), nil)
		openSectionByName(t, m, sectionConnections)
		pressNew(t, m)
		focusField(t, m, "permissions")
		press(t, m, "enter")
		if m.screen != screenPicker {
			t.Fatalf("screen = %v, want the picker", m.screen)
		}
		press(t, m, "?")
		if m.screen != screenPicker || m.picker.query() != "?" {
			t.Fatalf("screen %v search %q, want ? as search text", m.screen, m.picker.query())
		}
	})

	t.Run("log detail", func(t *testing.T) {
		f := newLogFixture(t)
		f.append("cli", "", "op.one", "wiki", "success")
		m := f.model(true, f.localToday())
		press(t, m, "down", "enter")
		if m.screen != screenLogDetail {
			t.Fatalf("enter = screen %v, want the detail", m.screen)
		}
		helpRoundTrip(t, m, "the log detail")
	})
}

func TestSpaceOnlyTicksInMultipleChoices(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), nil)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	for _, label := range []string{"service", "permissions", toolListLabel} {
		focusField(t, m, label)
		press(t, m, " ")
		if m.screen != screenForm || m.fields[m.focus].label != label {
			t.Fatalf("space on %s = screen %v, want the form unchanged", label, m.screen)
		}
	}

	m.screen = screenNav
	press(t, m, "?", " ")
	if m.helpOffset != 0 {
		t.Fatalf("space in the help scrolled to %d", m.helpOffset)
	}

	f := newLogFixture(t)
	f.append("cli", "", "op.one", "wiki", "success")
	logs := f.model(true, f.localToday())
	press(t, logs, "down", " ")
	if logs.screen != screenLogs {
		t.Fatalf("space on a log row = screen %v, want the Logs screen unchanged", logs.screen)
	}
	press(t, logs, "enter")
	if logs.screen != screenLogDetail {
		t.Fatalf("enter on a log row = screen %v, want the detail", logs.screen)
	}
}

func TestTabAndShiftTabCycleThroughTheAreas(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	seedServices(t, m, "a-one")
	m.screen = screenNav
	for _, key := range []string{"tab", "shift+tab"} {
		m.screen = screenNav
		press(t, m, key)
		if m.screen != screenList {
			t.Fatalf("%s on the sidebar = screen %v, want the list", key, m.screen)
		}
		press(t, m, key)
		if m.screen != screenNav {
			t.Fatalf("%s in the list = screen %v, want the sidebar", key, m.screen)
		}
	}
}

func TestFootersNameTheHelpWhereItWorks(t *testing.T) {
	flat := func(m *Model) string { return strings.Join(strings.Fields(screenOf(m)), " ") }
	want := func(m *Model, what string, keys ...string) {
		t.Helper()
		view := flat(m)
		for _, key := range keys {
			if !strings.Contains(view, key) {
				t.Errorf("%s does not name %q:\n%s", what, key, screenOf(m))
			}
		}
	}

	m, _, _ := knownTargetsModel(t)
	openEntryForm(t, m, sectionConnections, "gh-a")
	openTargetList(t, m)
	want(m, "the target list", "? help")
	press(t, m, "a")
	want(m, "the add menu", "? help")
	press(t, m, "x")
	if strings.Contains(flat(m), "? help") {
		t.Errorf("the add menu names ? while a line is typed:\n%s", screenOf(m))
	}

	tm, _ := templateModel(t)
	openSectionByName(t, tm, sectionConnections)
	tm.screen = screenList
	press(t, tm, "n")
	want(tm, "the template question", "? help")

	sm, _, _, _, _ := newStoreModel(t)
	walkSetup(t, sm, stepSummary)
	want(sm, "the setup summary", "? help")

	f := newLogFixture(t)
	f.append("cli", "", "op.one", "wiki", "success")
	lm := f.model(true, f.localToday())
	lm.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	want(lm, "the Logs screen", "1-8 section", "? help", "q quit")
	press(t, lm, "down", "enter")
	want(lm, "the log detail", "1-8 section", "? help")
}
