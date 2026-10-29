package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// helpTabClick clicks the tab of the help topic at index i and checks that the topic switched.
func helpTabClick(t *testing.T, m *Model, i int) {
	t.Helper()
	m.help.scroll(keyMsg("down"), 3)
	m.render()
	topics := helptopics.All()
	x, y := cellOf(t, m, topics[i].Name)
	clickAt(t, m, x, y)
	if m.screen != screenHelp || m.helpTopic != i {
		t.Fatalf("click on the tab %q: screen %v topic %d, want topic %d", topics[i].Name, m.screen, m.helpTopic, i)
	}
	if view := strings.Join(frameLines(m), "\n"); !strings.Contains(view, "["+topics[i].Name+"]") {
		t.Errorf("the topic %q is not the active tab:\n%s", topics[i].Name, view)
	}
}

func TestClickOnAHelpTopicTabSwitchesTheTopicInTheOverlayAndTheWorkspace(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"overlay", 120, 40}, {"workspace", 50, 16}} {
		t.Run(size.name, func(t *testing.T) {
			m := sizedModel(groupModel(t), size.w, size.h)
			m.prefs.Icons = uiprefs.IconsNone
			m.openHelp()
			if m.helpOverlay() != (size.name == "overlay") {
				t.Fatalf("helpOverlay = %v at %dx%d", m.helpOverlay(), size.w, size.h)
			}
			helpTabClick(t, m, 1)
			helpTabClick(t, m, len(helptopics.All())-1)
			helpTabClick(t, m, 0)
		})
	}
}

func TestClickOnADateFieldFocusesItAndTypesNothing(t *testing.T) {
	f := activityLog(t)
	m := sizedModel(f.model(true, f.localToday()), 120, 40)
	m.prefs.Icons = uiprefs.IconsNone
	m.logs.mode = logModeRange
	m.logs.focus = logFocusDate
	m.openLogDateDialog()
	m.render()
	if d := m.logs.dialog; d == nil || len(d.inputs) != 2 || d.focus != 0 {
		t.Fatalf("dialog = %+v, want two fields with the first focused", d)
	}
	before := m.logs.dialog.inputs[1].Value()
	_, y := cellOf(t, m, "from")
	clickAt(t, m, 0, y+1)
	d := m.logs.dialog
	if d == nil || d.focus != 1 || !d.inputs[1].Focused() || d.inputs[0].Focused() || d.inputs[1].Value() != before {
		t.Fatalf("click on the to field: dialog %+v, want it focused and unchanged", d)
	}
	clickAt(t, m, 0, y)
	if d := m.logs.dialog; d == nil || d.focus != 0 || !d.inputs[0].Focused() || d.inputs[1].Focused() {
		t.Fatalf("click on the from field: dialog %+v, want the first field focused", d)
	}
}

func TestClickOnAFilterMarksItAndASecondClickChoosesIt(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
	f.append("mcp", "claude-code", "wiki.pages.create", "wiki", "policy-denied")
	m := sizedModel(f.model(true, f.localToday()), 120, 40)
	m.prefs.Icons = uiprefs.IconsNone
	press(t, m, "f")
	m.render()
	clickOn(t, m, "client")
	if m.logs.pick == nil || m.logs.pick.filter != -1 {
		t.Fatalf("first click: pick %+v, want the filters still open", m.logs.pick)
	}
	if got, _ := m.logs.pick.list.selected(); got != strconv.Itoa(int(logFilterClient)) {
		t.Fatalf("first click marked %q, want the client filter", got)
	}
	clickOn(t, m, "client")
	if m.logs.pick == nil || m.logs.pick.filter != int(logFilterClient) {
		t.Fatalf("click on the marked filter: pick %+v, want the values of the client filter", m.logs.pick)
	}
	clickOn(t, m, "claude-code")
	if m.logs.pick == nil || m.logs.filters[logFilterClient] != "" {
		t.Fatalf("first click on a value applied it: filters %v", m.logs.filters)
	}
	clickOn(t, m, "claude-code")
	if m.logs.pick != nil || m.logs.filters[logFilterClient] != "claude-code" {
		t.Fatalf("click on the marked value: pick %+v filters %v, want claude-code kept", m.logs.pick, m.logs.filters)
	}
}

func TestClickOnATargetOfThePickerMarksItAndASecondClickTakesIt(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"), group("c"))}
	m := pickerModel(t, src)
	m.prefs.Icons = uiprefs.IconsNone
	openPicker(t, m)
	m.render()
	clickOn(t, m, "Group b")
	if m.targetPick == nil || m.targetPick.top().list.cursor != 1 || len(m.targetList.all) != 0 {
		t.Fatalf("first click: picker %v cursor, targets %v, want row 1 marked and nothing taken", m.targetPick, m.targetList.all)
	}
	clickOn(t, m, "Group b")
	if m.targetPick != nil || len(m.targetList.all) != 1 || m.targetList.all[0] != "g/b" {
		t.Fatalf("click on the marked entry: picker %v targets %v, want g/b taken", m.targetPick, m.targetList.all)
	}
}

func TestClickOnARowOfTheTargetBuilderMarksItAndASecondClickChoosesIt(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := pickerModel(t, src)
	m.prefs.Icons = uiprefs.IconsNone
	m.render()
	if m.targetAdd == nil || m.targetAdd.kind >= 0 {
		t.Fatalf("add menu = %+v", m.targetAdd)
	}
	clickOn(t, m, "from the provider's list")
	if m.targetPick != nil || m.targetAdd == nil {
		t.Fatalf("first click chose the row")
	}
	clickOn(t, m, "from the provider's list")
	if m.targetPick == nil {
		t.Fatalf("click on the marked row did not open the picker:\n%s", screenOf(m))
	}
}

func TestClickOnAnExportRowTicksOnlyOnTheMarkedOne(t *testing.T) {
	v, _ := plainExportModel(t)
	m := v.m
	m.prefs.Icons = uiprefs.IconsNone
	v.startExport("")
	m.render()
	clickOn(t, m, "connection  docs")
	e := m.export
	if got, _ := e.rows.selected(); got == exportAll || e.ticked() != 0 {
		t.Fatalf("first click: marked %q ticked %d, want the row marked and nothing ticked", got, e.ticked())
	}
	clickOn(t, m, "connection  docs")
	if e.ticked() != 1 || e.step != exportSelect {
		t.Fatalf("click on the marked row: ticked %d step %v, want it ticked and the selection kept", e.ticked(), e.step)
	}
	clickOn(t, m, "connection  docs")
	if e.ticked() != 0 {
		t.Fatalf("second click on the marked row ticked %d, want it unticked", e.ticked())
	}
}

func TestClickOnAnExportModeMarksItAndASecondClickChoosesIt(t *testing.T) {
	v, _ := plainExportModel(t)
	m := v.m
	m.prefs.Icons = uiprefs.IconsNone
	v.startExport("")
	v.pump(" ", "enter")
	if m.export.step != exportMode || m.export.secretsReason == "" {
		t.Fatalf("step %v reason %q, want the mode question with secrets unavailable", m.export.step, m.export.secretsReason)
	}
	m.render()
	// A row that cannot be chosen is no target: the click falls through and marks nothing.
	clickOn(t, m, "with secrets, encrypted")
	if m.export.modeCursor != 0 {
		t.Fatalf("click on a blocked row moved the mark to %d", m.export.modeCursor)
	}
	clickOn(t, m, "without secrets")
	if m.export.step != exportTarget {
		t.Fatalf("click on the marked mode: step %v, want the file question", m.export.step)
	}
}

func TestClickOnAnExportModeNeedsTheAdminSessionLikeEnter(t *testing.T) {
	v, _ := vaultExportModel(t)
	m := v.m
	m.prefs.Icons = uiprefs.IconsNone
	v.startExport(testExportVaultPass)
	v.pump(" ", "enter")
	if m.export.step != exportMode || m.export.secretsReason != "" {
		t.Fatalf("step %v reason %q, want the mode question", m.export.step, m.export.secretsReason)
	}
	m.render()
	row := "with secrets, encrypted"
	clickOn(t, m, row)
	if m.export.modeCursor != 1 || m.export.step != exportMode {
		t.Fatalf("first click: cursor %d step %v, want only the mark", m.export.modeCursor, m.export.step)
	}
	m.endAdminSession()
	clickOn(t, m, row)
	if m.screen != screenAdminAuth {
		t.Fatalf("click on the marked mode: screen %v, want the admin question enter asks", m.screen)
	}
}

func TestClickOnAnImportModeMarksItAndASecondClickPreviews(t *testing.T) {
	v := plainImportTarget(t)
	m := v.m
	m.prefs.Icons = uiprefs.IconsNone
	path := importFixture(t, exportWithout)
	v.startImport("")
	v.typePath(path)
	v.pump("enter")
	if m.imp.step != importMode {
		t.Fatalf("step %v fail %q, want the mode question", m.imp.step, m.fail)
	}
	m.render()
	text := strings.Fields(tr(importModeRows[1].text))[0]
	clickOn(t, m, text)
	if m.imp.modeCursor != 1 || m.imp.step != importMode {
		t.Fatalf("first click: cursor %d step %v, want only the mark", m.imp.modeCursor, m.imp.step)
	}
	clickOn(t, m, text)
	if m.imp.step == importMode {
		t.Fatalf("click on the marked mode did not go on:\n%s", screenOf(m))
	}
}

func TestClickOnAFindingMarksItAndASecondClickStartsIt(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "credential", "reader", "reader2")
	m := s.open(t)
	m.prefs.Icons = uiprefs.IconsNone
	openSectionByName(t, m, sectionCredentials)
	m.openFindings()
	if m.screen != screenFindings || len(m.findings.items) != 1 {
		t.Fatalf("screen %v findings %+v, want the one hand rename", m.screen, m.findings)
	}
	// A second finding that has no action stands first, so the real one is not the marked row.
	m.findings.items = append([]renameFinding{{kind: "connection", entry: "x", from: "x", to: "y", ambiguous: true}}, m.findings.items...)
	m.findings.cursor = 0
	m.render()
	clickOn(t, m, "reader -> reader2")
	if m.screen != screenFindings || m.findings.cursor != 1 {
		t.Fatalf("first click: screen %v cursor %d, want finding 1 marked", m.screen, m.findings.cursor)
	}
	clickOn(t, m, "reader -> reader2")
	if m.screen != screenRename || m.rename == nil {
		t.Fatalf("click on the marked finding: screen %v, want the dry run enter starts", m.screen)
	}
}

func TestClickOnAPathSuggestionMarksItAndASecondClickTakesIt(t *testing.T) {
	v, _ := plainExportModel(t)
	m := v.m
	m.prefs.Icons = uiprefs.IconsNone
	// Relative names keep the rows short: a suggestion that wraps over several lines is no click target.
	dir := t.TempDir()
	for _, name := range []string{"alpha", "abeta", "agamma"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	v.startExport("")
	v.pump(" ", "enter")
	v.pump("enter")
	if m.export.step != exportTarget {
		t.Fatalf("step %v, want the file question", m.export.step)
	}
	p := &m.export.input
	p.set("a")
	m.render()
	if len(p.matches) != 3 {
		t.Fatalf("matches %v", p.matches)
	}
	clickOn(t, m, "agamma")
	if p.mark != 1 || !p.marked || p.Value() != "a" {
		t.Fatalf("first click: mark %d marked %v value %q, want only the mark", p.mark, p.marked, p.Value())
	}
	clickOn(t, m, "agamma")
	if want := "agamma" + string(filepath.Separator); p.Value() != want {
		t.Fatalf("click on the marked suggestion: value %q, want %q (tab completes)", p.Value(), want)
	}
	if m.export.step != exportTarget {
		t.Fatalf("the click went on to step %v", m.export.step)
	}
}
