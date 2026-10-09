package tui

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
)

// startServiceForm opens a new service form with a name and a base URL typed into it.
func startServiceForm(t *testing.T) (*Model, string) {
	t.Helper()
	m, _, path := newModel(t)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "wiki")
	press(t, m, "tab", "tab")
	typeText(t, m, "https://wiki.example.invalid")
	return m, path
}

// esc is exactly one level back on every kind of screen.
func TestEscIsOneLevelBackOnEveryScreen(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	if m.screen != screenList {
		t.Fatalf("opening a section left screen %v", m.screen)
	}
	press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("? opened screen %v", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenList {
		t.Fatalf("esc on the help left screen %v, want the list", m.screen)
	}
	pressNew(t, m)
	if m.screen != screenForm {
		t.Fatalf("n opened screen %v", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenList {
		t.Fatalf("esc on an unchanged form left screen %v, want the list", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenNav {
		t.Fatalf("esc on the list left screen %v, want the sidebar", m.screen)
	}
}

func TestEscClosesAPickerAndATargetListOneLevelAtATime(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, _ := toolsModel(t, reg, map[string]config.Connection{
		"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
	openConnection(t, m, "gh")
	focusField(t, m, "permissions")
	press(t, m, "enter")
	if m.screen != screenPicker {
		t.Fatalf("enter opened screen %v, want the picker", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("esc on an unchanged picker left screen %v, want the form", m.screen)
	}
	openTargetList(t, m)
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("esc on an unchanged target list left screen %v, want the form", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenList {
		t.Fatalf("esc on an unchanged form left screen %v, want the list", m.screen)
	}
}

// A target or a path that was typed and not taken is unsaved input: esc asks, d drops only the entry, and
// an entry that still holds what it started from closes without a question.
func TestATypedTargetOrPathAsksBeforeItIsDropped(t *testing.T) {
	reg := targetsRegistry(t, github.Register)
	m, _ := toolsModel(t, reg, map[string]config.Connection{
		"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
	openConnection(t, m, "gh")
	openTargetList(t, m)
	press(t, m, "a")
	press(t, m, "esc")
	if m.screen != screenTargets || m.targetEdit >= 0 {
		t.Fatalf("esc on an empty entry asked or stayed: screen %v", m.screen)
	}
	press(t, m, "a")
	typeText(t, m, "repos/octo/b")
	press(t, m, "esc")
	if view := screenOf(m); m.screen != screenLeave || !strings.Contains(view, "typed target was not taken") {
		t.Fatalf("esc on a typed target did not ask: screen %v\n%s", m.screen, view)
	}
	press(t, m, "esc")
	if m.screen != screenTargets || m.targetAdd == nil || m.targetAdd.choices.query() != "repos/octo/b" {
		t.Fatalf("esc did not return to the typed line: screen %v", m.screen)
	}
	press(t, m, "esc", "d")
	if m.screen != screenTargets || m.targetAdd != nil || len(m.targetList.all) != 1 {
		t.Fatalf("d did not drop the typed line: screen %v, targets %v", m.screen, m.targetList.all)
	}
	press(t, m, "enter")
	typeText(t, m, "x")
	press(t, m, "esc", "esc")
	if m.screen != screenTargets || m.targetEdit != 0 || m.targetInput.Value() != "repos/octo/ax" {
		t.Fatalf("esc did not keep editing the target: screen %v, %q", m.screen, m.targetInput.Value())
	}
	press(t, m, "esc", "d")
	if m.screen != screenTargets || m.targetEdit >= 0 || m.targetList.all[0] != "repos/octo/a" {
		t.Fatalf("d did not drop the edit: screen %v, targets %v", m.screen, m.targetList.all)
	}
	press(t, m, "a")
	typeText(t, m, "repos/octo/b")
	press(t, m, "esc", "f2")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("F2 in the question did not take and save the target: screen %v, error %q", m.screen, m.fail)
	}
	if saved := savedConnection(t, m.store.Path(), reg, "gh"); len(saved.Targets) != 2 {
		t.Fatalf("saved %q / %v, want both targets", saved.Target, saved.Targets)
	}

	reg = wikiRegistry(t)
	m, _ = toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openConnection(t, m, "wiki")
	openPathList(t, m)
	press(t, m, "a")
	typeText(t, m, t.TempDir())
	press(t, m, "esc")
	if view := screenOf(m); m.screen != screenLeave || !strings.Contains(view, "typed path was not taken") {
		t.Fatalf("esc on a typed path did not ask: screen %v\n%s", m.screen, view)
	}
	press(t, m, "d")
	if m.screen != screenPaths || m.pathEdit >= 0 || len(m.pathList.all) != 0 {
		t.Fatalf("d did not drop the typed path: screen %v", m.screen)
	}
}

// A changed set of ticks, and a half-built target, ask the same question as a changed form.
func TestTicksAndAHalfBuiltTargetAskTheSameQuestion(t *testing.T) {
	m, _, _ := knownTargetsModel(t)
	openConnection(t, m, "gh-a")
	focusField(t, m, "permissions")
	press(t, m, "enter")
	typeText(t, m, "create")
	press(t, m, " ", "esc")
	if view := screenOf(m); m.screen != screenLeave ||
		!strings.Contains(view, "F2 save · d discard · esc keep editing") {
		t.Fatalf("changed ticks did not ask the common question: screen %v\n%s", m.screen, view)
	}
	press(t, m, "esc")
	if m.screen != screenPicker {
		t.Fatalf("esc did not keep editing the ticks: screen %v", m.screen)
	}
	press(t, m, "esc", "d")
	if m.screen != screenForm {
		t.Fatalf("d left screen %v, want the form", m.screen)
	}

	openTargetList(t, m)
	press(t, m, "a")
	pickRow(t, m, "new repository")
	press(t, m, "enter", "esc")
	if view := screenOf(m); m.screen != screenLeave ||
		!strings.Contains(view, "F2 save · d discard · esc keep editing") {
		t.Fatalf("a half-built target did not ask the common question: screen %v\n%s", m.screen, view)
	}
}

// A changed form, and the guided setup, ask the common question; F2 saves a form, d drops it, esc stays.
func TestTheFormQuestionHasOneSetOfKeys(t *testing.T) {
	m, path := startServiceForm(t)
	press(t, m, "esc")
	view := screenOf(m)
	if m.screen != screenLeave || !strings.Contains(view, "F2 save · d discard · esc keep editing") {
		t.Fatalf("esc on a changed form: screen %v\n%s", m.screen, view)
	}
	pump(t, m, "f2")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("F2 in the question: screen %v, error %q", m.screen, m.fail)
	}
	if _, ok := m.cfg.Services["wiki"]; !ok {
		t.Error("F2 in the question did not save the service")
	}
	if _, err := m.store.Load(); err != nil {
		t.Errorf("the saved file is unreadable: %v (%s)", err, path)
	}
}

// ctrl+c ends the editor at once while nothing is unsaved, on every kind of screen.
func TestCtrlCQuitsAtOnceWithoutChanges(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *Model){
		"sidebar": func(t *testing.T, m *Model) { m.screen = screenNav },
		"list":    func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices) },
		"help":    func(t *testing.T, m *Model) { openSectionByName(t, m, sectionServices); press(t, m, "?") },
		"unchanged form": func(t *testing.T, m *Model) {
			openSectionByName(t, m, sectionServices)
			pressNew(t, m)
		},
		"setup before a provider is chosen": func(t *testing.T, m *Model) {
			m.screen = screenNav
			press(t, m, "c")
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := newModel(t)
			setup(t, m)
			press(t, m, "ctrl+c")
			if !m.quitting {
				t.Fatalf("ctrl+c did not end the editor: screen %v", m.screen)
			}
		})
	}
}

// ctrl+c asks the common question whenever something unsaved would be lost, and d then ends the editor.
func TestCtrlCAsksWhenSomethingIsUnsaved(t *testing.T) {
	t.Run("form", func(t *testing.T) {
		m, path := startServiceForm(t)
		press(t, m, "ctrl+c")
		if m.quitting || m.screen != screenLeave || !strings.Contains(screenOf(m), "d discard and quit") {
			t.Fatalf("ctrl+c on a changed form: quitting %v, screen %v\n%s", m.quitting, m.screen, screenOf(m))
		}
		press(t, m, "esc")
		if m.screen != screenForm || m.quitting || m.fieldValue("name") != "wiki" {
			t.Fatalf("esc did not keep editing: screen %v", m.screen)
		}
		press(t, m, "ctrl+c", "d")
		if !m.quitting {
			t.Fatal("d did not end the editor")
		}
		if _, err := m.store.Load(); err == nil {
			t.Errorf("discarding wrote %s", path)
		}
	})
	t.Run("F2 saves instead of quitting", func(t *testing.T) {
		m, _ := startServiceForm(t)
		press(t, m, "ctrl+c")
		pump(t, m, "f2")
		if m.quitting || m.fail != "" || m.screen != screenList {
			t.Fatalf("F2: quitting %v, screen %v, error %q", m.quitting, m.screen, m.fail)
		}
		if _, ok := m.cfg.Services["wiki"]; !ok {
			t.Error("F2 did not save the service")
		}
	})
	t.Run("masked dialog over a changed form", func(t *testing.T) {
		m, _ := startServiceForm(t)
		m.askSecret("token")
		press(t, m, "ctrl+c")
		if m.quitting || m.screen != screenLeave || m.leaveFrom != screenForm {
			t.Fatalf("ctrl+c in the dialog: quitting %v, screen %v from %v", m.quitting, m.screen, m.leaveFrom)
		}
	})
	t.Run("typed target and changed list", func(t *testing.T) {
		reg := targetsRegistry(t, github.Register)
		m, _ := toolsModel(t, reg, map[string]config.Connection{
			"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
		openConnection(t, m, "gh")
		openTargetList(t, m)
		press(t, m, "ctrl+c")
		if !m.quitting {
			t.Fatal("ctrl+c on an unchanged list asked")
		}
		m, _ = toolsModel(t, reg, map[string]config.Connection{
			"gh": {Service: "wiki", Credential: "reader", Target: "repos/octo/a"}})
		openConnection(t, m, "gh")
		openTargetList(t, m)
		press(t, m, "a")
		typeText(t, m, "repos/octo/b")
		press(t, m, "ctrl+c")
		if m.quitting || m.screen != screenLeave {
			t.Fatalf("ctrl+c on a typed target: quitting %v, screen %v", m.quitting, m.screen)
		}
		press(t, m, "d")
		if !m.quitting {
			t.Fatal("d did not end the editor")
		}
	})
	t.Run("guided setup", func(t *testing.T) {
		m, _, path := newModel(t)
		walkSetup(t, m, 1)
		press(t, m, "ctrl+c")
		if m.quitting || m.screen != screenLeave || !strings.Contains(screenOf(m), "guided setup is not saved") {
			t.Fatalf("ctrl+c in the setup: quitting %v, screen %v", m.quitting, m.screen)
		}
		press(t, m, "d")
		if !m.quitting {
			t.Fatal("d did not end the editor")
		}
		if _, err := m.store.Load(); err == nil {
			t.Errorf("quitting wrote %s", path)
		}
	})
}

// F3 and backspace have no effect as "back", q is text or nothing outside the sidebar, lists and Logs.
func TestF3BackspaceAndQAreNotBackOrQuitInTheSetupAndForms(t *testing.T) {
	m, _, _ := newModel(t)
	walkSetup(t, m, 2)
	step := m.wizard.step
	press(t, m, "f3", "backspace")
	if m.wizard == nil || m.wizard.step != step {
		t.Fatalf("f3 or backspace moved the setup from step %d", step)
	}

	m, _ = startServiceForm(t)
	press(t, m, "q")
	if m.quitting || m.screen != screenForm || !strings.HasSuffix(m.fieldValue("base url"), "q") {
		t.Fatalf("q in a form: quitting %v, screen %v", m.quitting, m.screen)
	}
	press(t, m, "esc", "d", "?")
	press(t, m, "q")
	if m.quitting || m.screen != screenHelp {
		t.Fatalf("q in the help: quitting %v, screen %v", m.quitting, m.screen)
	}
}

// In the Logs, esc is the only way back from an entry; enter, backspace and left stay on it. Search and
// date dialogs close with esc and no question.
func TestLogDetailAndDialogsGoBackOnlyWithEsc(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "op.one", "wiki", "success")
	m := f.model(true, f.localToday())
	press(t, m, "down", "enter")
	if m.screen != screenLogDetail {
		t.Fatalf("enter = screen %v, want the detail", m.screen)
	}
	press(t, m, "enter", "backspace", "left", "h", "q")
	if m.screen != screenLogDetail || m.quitting {
		t.Fatalf("a key other than esc left the detail: screen %v, quitting %v", m.screen, m.quitting)
	}
	press(t, m, "esc")
	if m.screen != screenLogs {
		t.Fatalf("esc = screen %v, want the Logs screen", m.screen)
	}
	if !strings.Contains(m.View(), "q quit") {
		t.Errorf("the Logs footer does not name q:\n%s", m.View())
	}

	press(t, m, "/")
	typeText(t, m, "op")
	press(t, m, "esc")
	if m.screen != screenLogs || m.logs.list.editing || m.logs.list.query() != "" {
		t.Fatalf("esc did not clear the search at once: screen %v", m.screen)
	}
	press(t, m, "tab", "right")
	press(t, m, "tab", "enter")
	if m.logs.dialog == nil {
		t.Fatal("the date dialog did not open")
	}
	typeText(t, m, "x")
	press(t, m, "esc")
	if m.logs.dialog != nil || m.screen != screenLogs {
		t.Fatalf("esc did not close the date dialog without a question: screen %v", m.screen)
	}
	press(t, m, "q")
	if !m.quitting {
		t.Fatal("q did not quit from the Logs")
	}
}
