package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	tea "github.com/charmbracelet/bubbletea"
)

// listSource is a fake provider listing: list answers each request, and every request is recorded together
// with whether its context was cancelled when the read ran.
type listSource struct {
	list      func(req config.TargetListRequest) (config.TargetPage, error)
	reqs      []config.TargetListRequest
	cancelled []bool
}

func (s *listSource) targetSource() manage.TargetSource {
	return manage.TargetSource{
		Browses: func(id string) bool { return id == "lister" },
		List: func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
			req config.TargetListRequest) (config.TargetPage, error) {
			s.reqs = append(s.reqs, req)
			s.cancelled = append(s.cancelled, ctx.Err() != nil)
			return s.list(req)
		},
		ResolveURL: func(context.Context, *config.Resolved, *secret.Resolver, string, string) ([]config.TargetEntry, error) {
			return nil, nil
		},
	}
}

func listerRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Kinds: []config.TargetKind{
			{Name: "group", Forms: []string{"g/NAME"}},
			{Name: "item", Forms: []string{"g/NAME/i/ID"}, Parent: "group"},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func group(name string) config.TargetEntry {
	return config.TargetEntry{Target: "g/" + name, Name: "Group " + name, Kind: "group", HasChildren: true}
}

func item(parent, id string) config.TargetEntry {
	return config.TargetEntry{Target: parent + "/i/" + id, Name: "Item " + id, Kind: "item", Path: parent}
}

// pickerModel opens the add menu of the connection "lst" over a provider whose listing is src.
func pickerModel(t *testing.T, src *listSource, targets ...string) *Model {
	t.Helper()
	old := pickDelay
	pickDelay = time.Millisecond
	t.Cleanup(func() { pickDelay = old })
	m, _ := toolsModel(t, listerRegistry(t), map[string]config.Connection{
		"lst": {Service: "wiki", Credential: "reader", Targets: targets},
	})
	m.svc = m.svc.WithTargetSource(src.targetSource())
	openEntryForm(t, m, sectionConnections, "lst")
	openTargetList(t, m)
	press(t, m, "a")
	return m
}

// openPicker takes the "choose from the list" row and delivers the first page.
func openPicker(t *testing.T, m *Model) {
	t.Helper()
	pickRow(t, m, "from the provider's list")
	pump(t, m, "enter")
	if m.targetPick == nil {
		t.Fatalf("enter on the list row did not open the picker:\n%s", screenOf(m))
	}
}

func pickShown(m *Model) []string {
	lv := m.targetPick.top()
	return append([]string(nil), lv.list.matches...)
}

func pickPage(entries ...config.TargetEntry) func(config.TargetListRequest) (config.TargetPage, error) {
	return func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{Entries: entries}, nil
	}
}

func TestThePickerIsOfferedOnlyForASavedConnectionWithABrowser(t *testing.T) {
	src := &listSource{list: pickPage()}
	m := pickerModel(t, src)
	if rows := strings.Join(addRows(m), "|"); !strings.Contains(rows, "choose a group from the provider's list") {
		t.Fatalf("the add menu offers %q, want the picker row", rows)
	}

	// A provider without a browser, and a form that no longer matches the saved connection, offer nothing.
	m.svc = m.svc.WithTargetSource(manage.TargetSource{})
	m.refreshAdd()
	if rows := strings.Join(addRows(m), "|"); strings.Contains(rows, "provider's list") {
		t.Fatalf("a provider without a browser offers %q", rows)
	}
}

func TestThePickerLoadsAndFiltersLocally(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"), group("ab"))}
	m := pickerModel(t, src)
	openPicker(t, m)
	if got := pickShown(m); len(got) != 3 {
		t.Fatalf("shown %v, want 3 entries", got)
	}
	view := screenOf(m)
	for _, want := range []string{"Name", "Kind", "ID", "Path", "Group ab", "g/ab", "> "} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not show %q:\n%s", want, view)
		}
	}
	pump(t, m, "b")
	if got := pickShown(m); len(got) != 2 {
		t.Fatalf("filter b shows %v, want 2 entries", got)
	}
	if len(src.reqs) != 1 {
		t.Fatalf("typing in a provider without search read again: %v", src.reqs)
	}
}

func TestThePickerShowsALoadingStateUntilThePageArrives(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := pickerModel(t, src)
	pickRow(t, m, "from the provider's list")
	_, cmd := m.Update(keyMsg("enter"))
	if view := screenOf(m); !strings.Contains(view, "loading") {
		t.Fatalf("no loading state while the page is read:\n%s", view)
	}
	deliver(m, cmd)
	if view := screenOf(m); strings.Contains(view, "loading") || !strings.Contains(view, "Group a") {
		t.Fatalf("loaded page not shown:\n%s", view)
	}
}

func TestThePickerReadsTheNextPageAtTheEnd(t *testing.T) {
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		if req.Cursor == "" {
			return config.TargetPage{Entries: []config.TargetEntry{group("a"), group("b")}, Next: "p2"}, nil
		}
		return config.TargetPage{Entries: []config.TargetEntry{group("c")}}, nil
	}}
	m := pickerModel(t, src)
	openPicker(t, m)
	pump(t, m, "down")
	if len(src.reqs) != 2 || src.reqs[1].Cursor != "p2" {
		t.Fatalf("reaching the end read %v, want the second page", src.reqs)
	}
	if got := pickShown(m); len(got) != 3 || m.targetPick.top().list.cursor != 1 {
		t.Fatalf("shown %v cursor %d, want 3 entries and the marker kept", got, m.targetPick.top().list.cursor)
	}
	pump(t, m, "down", "down")
	if len(src.reqs) != 2 {
		t.Fatalf("a finished listing was read again: %v", src.reqs)
	}
}

func TestThePickerSearchesOnTheServerAfterAPause(t *testing.T) {
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		entries := []config.TargetEntry{group("a"), group("b")}
		if req.Query == "zz" {
			entries = []config.TargetEntry{group("zz")}
		}
		return config.TargetPage{Entries: entries, ServerSearch: true}, nil
	}}
	m := pickerModel(t, src)
	openPicker(t, m)

	// Two keys, two pauses: only the pause after the last key reads, and with the whole text.
	_, first := m.Update(keyMsg("z"))
	_, second := m.Update(keyMsg("z"))
	deliver(m, first)
	if len(src.reqs) != 1 {
		t.Fatalf("a superseded pause read: %v", src.reqs)
	}
	deliver(m, second)
	if len(src.reqs) != 2 || src.reqs[1].Query != "zz" || src.reqs[1].Cursor != "" {
		t.Fatalf("requests %v, want one more with the query zz", src.reqs)
	}
	if got := pickShown(m); len(got) != 1 || got[0] != "g/zz" {
		t.Fatalf("shown %v, want only the server's answer", got)
	}
}

func TestALateAnswerOfAReplacedReadChangesNothing(t *testing.T) {
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		if req.Query == "q" {
			return config.TargetPage{Entries: []config.TargetEntry{group("q")}, ServerSearch: true}, nil
		}
		return config.TargetPage{Entries: []config.TargetEntry{group("a"), group("b")}, Next: "p2",
			ServerSearch: true}, nil
	}}
	m := pickerModel(t, src)
	openPicker(t, m)
	_, more := m.Update(keyMsg("down")) // reads page 2
	_, search := m.Update(keyMsg("q"))
	deliver(m, search) // the search starts and replaces the read in flight
	stale := more()
	m.Update(stale)
	if got := pickShown(m); len(got) != 1 || got[0] != "g/q" {
		t.Fatalf("shown %v, want the search result untouched by the replaced read", got)
	}
}

func TestThePickerOpensALevelAndGoesBack(t *testing.T) {
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		if req.Kind == "item" {
			return config.TargetPage{Entries: []config.TargetEntry{item(req.Parent, "1"), item(req.Parent, "2")}}, nil
		}
		return config.TargetPage{Entries: []config.TargetEntry{group("a"), group("b")}}, nil
	}}
	m := pickerModel(t, src)
	openPicker(t, m)
	pump(t, m, "down", "b") // second group, filtered to it
	pump(t, m, "right")
	last := src.reqs[len(src.reqs)-1]
	if last.Kind != "item" || last.Parent != "g/b" {
		t.Fatalf("opening read %+v, want items under g/b", last)
	}
	view := screenOf(m)
	for _, want := range []string{"level: group › Group b (g/b) › item", "Item 2"} {
		if !strings.Contains(view, want) {
			t.Errorf("the second level does not show %q:\n%s", want, view)
		}
	}
	pump(t, m, "left")
	if len(m.targetPick.levels) != 1 || m.targetPick.top().list.query() != "b" {
		t.Fatalf("left did not return to the first level with its search: %d levels", len(m.targetPick.levels))
	}
	pump(t, m, "left")
	if m.status != "Already on the top level" {
		t.Errorf("left on the top level says %q", m.status)
	}
}

func TestAHigherLevelIsATargetOfItsOwn(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"))}
	m := pickerModel(t, src)
	openPicker(t, m)
	pump(t, m, "down", "enter")
	if m.targetPick != nil || m.targetAdd != nil {
		t.Fatalf("enter left the picker open:\n%s", screenOf(m))
	}
	if got := m.targetList.all; len(got) != 1 || got[0] != "g/b" {
		t.Fatalf("list %v, want the group g/b", got)
	}
	if got := m.targetLabels["g/b"]; got != "Group b" {
		t.Errorf("label %q, want the provider's name", got)
	}
}

func TestSeveralEntriesCanBeMarkedAndTakenTogether(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"), group("c"))}
	m := pickerModel(t, src, "g/a")
	openPicker(t, m)
	pump(t, m, "down", "tab", "down", "tab")
	if view := screenOf(m); !strings.Contains(view, "[x] Group b") || !strings.Contains(view, "2 marked") {
		t.Fatalf("marks not shown:\n%s", view)
	}
	pump(t, m, "enter")
	if got := strings.Join(m.targetList.all, ","); got != "g/a,g/b,g/c" {
		t.Fatalf("list %q, want both marked entries added after the existing one", got)
	}

	// An entry the list holds already is refused and nothing of the choice is taken.
	m2 := pickerModel(t, src, "g/a")
	openPicker(t, m2)
	pump(t, m2, "tab", "down", "tab", "enter")
	if m2.targetPick == nil || len(m2.targetList.all) != 1 || !strings.Contains(m2.fail, "already in the list") {
		t.Fatalf("a refused choice: picker open %v list %v error %q", m2.targetPick != nil, m2.targetList.all, m2.fail)
	}
}

func TestAnExistingLabelIsNotOverwrittenAndTheLabelStaysEditable(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := pickerModel(t, src)
	m.targetLabels = map[string]string{"g/a": "Mine"}
	openPicker(t, m)
	pump(t, m, "enter")
	if got := m.targetLabels["g/a"]; got != "Mine" {
		t.Errorf("label %q, want the existing one kept", got)
	}

	m = pickerModel(t, src)
	openPicker(t, m)
	pump(t, m, "enter", "i")
	if !m.labelEdit || m.labelInput.Value() != "Group a" {
		t.Errorf("label edit %v value %q, want the provider's name to edit", m.labelEdit, m.labelInput.Value())
	}
}

func TestProviderTextIsShownAndStoredWithoutControlCharacters(t *testing.T) {
	src := &listSource{list: pickPage()}
	m := pickerModel(t, src)
	openPicker(t, m)
	hostile := config.TargetEntry{Target: "g/x", Kind: "group", Name: "Evil\x1b[2J\nName\u202e", Path: "p\x07ath"}
	// The core drops such entries, so the answer is handed to the model directly.
	m.targetPick.top().loading, m.targetPick.top().req = true, 99
	m.Update(pickLoadedMsg{req: 99, replace: true, page: config.TargetPage{Entries: []config.TargetEntry{hostile}}})
	view := screenOf(m)
	for _, bad := range []string{"\x1b", "\x07", "\u202e"} {
		if strings.Contains(view, bad) {
			t.Errorf("the view holds %q:\n%q", bad, view)
		}
	}
	pump(t, m, "enter")
	label := m.targetLabels["g/x"]
	if label != "Evil [2J Name" {
		t.Errorf("label %q, want one clean line", label)
	}
	if strings.ContainsAny(label, "\x1b\n\u202e") {
		t.Errorf("label %q holds control characters", label)
	}
}

func TestAFailedReadShowsTheCodeAndTheTextOfTheCore(t *testing.T) {
	src := &listSource{list: func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{}, &provider.Error{Class: provider.ClassAuth}
	}}
	m := pickerModel(t, src)
	openPicker(t, m)
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "auth: ") || !strings.Contains(view, "ctrl+r reads again") {
		t.Fatalf("the failure is not shown with its code:\n%s", view)
	}

	lv := m.targetPick.top()
	lv.loading, lv.req = true, 77
	m.Update(pickLoadedMsg{req: 77, err: &manage.TargetChoiceError{Code: "admin-required"}})
	view = strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "admin-required: choosing targets needs the vault passphrase first") {
		t.Fatalf("admin-required not shown:\n%s", view)
	}
}

func TestLeavingThePickerCancelsTheRunningRead(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := pickerModel(t, src)
	pickRow(t, m, "from the provider's list")
	_, read := m.Update(keyMsg("enter"))
	press(t, m, "esc")
	if m.targetPick != nil || m.targetAdd == nil {
		t.Fatalf("esc did not return to the add menu: picker %v menu %v", m.targetPick != nil, m.targetAdd != nil)
	}
	deliver(m, read)
	if len(src.cancelled) != 1 || !src.cancelled[0] {
		t.Fatalf("the read ran with a live context: %v", src.cancelled)
	}
	if m.targetPick != nil || len(m.targetList.all) != 0 {
		t.Fatal("the late answer changed the state")
	}

	// Quitting cancels it too.
	_, read = m.Update(keyMsg("enter"))
	if m.targetPick == nil {
		t.Fatal("enter did not reopen the picker")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	deliver(m, read)
	if got := src.cancelled[len(src.cancelled)-1]; !got {
		t.Fatal("quitting left the read running")
	}
}

func TestTheNarrowPickerDropsTheLessImportantColumnsFirst(t *testing.T) {
	src := &listSource{list: pickPage(group("alpha"), group("beta"))}
	m := pickerModel(t, src)
	openPicker(t, m)
	long := group("alpha")
	long.Path = "a very long place that needs a lot of room in the table"
	m.targetPick.top().req, m.targetPick.top().loading = 5, true
	m.Update(pickLoadedMsg{req: 5, replace: true, page: config.TargetPage{Entries: []config.TargetEntry{long}}})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	view := screenOf(m)
	assertViewFits(t, view, 40, 20)
	if !strings.Contains(view, "g/alpha") {
		t.Errorf("the ID column went before the others:\n%s", view)
	}
	if strings.Contains(view, "Path") {
		t.Errorf("the path column stayed on a narrow terminal:\n%s", view)
	}
}

func TestF2InThePickerTakesTheMarksAndSaves(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"))}
	m := pickerModel(t, src)
	openPicker(t, m)
	pump(t, m, "tab", "f2")
	if m.targetPick != nil || m.fail != "" {
		t.Fatalf("F2 left the picker open %v or failed: %q", m.targetPick != nil, m.fail)
	}
	if got := m.cfg.Connections["lst"].TargetValues(); len(got) != 1 || got[0] != "g/a" {
		t.Fatalf("saved targets %v, want the marked entry", got)
	}
}
