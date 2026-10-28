package tui

import (
	"context"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// listSource is a fake provider listing: list answers each request, and every request is recorded together
// with whether its context was cancelled when the read ran.
type listSource struct {
	list func(req config.TargetListRequest) (config.TargetPage, error)
	// resolve answers a web address; nil finds nothing.
	resolve   func(rawURL string) ([]config.TargetEntry, error)
	reqs      []config.TargetListRequest
	cancelled []bool
	urls      []string
	// seen are the connections the reads were for: a saved one has a name, a draft has none.
	seen []*config.Resolved
}

func (s *listSource) targetSource() manage.TargetSource {
	return manage.TargetSource{
		Browses: func(id string) bool { return id == "lister" },
		List: func(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
			req config.TargetListRequest) (config.TargetPage, error) {
			s.seen = append(s.seen, resolved)
			s.reqs = append(s.reqs, req)
			s.cancelled = append(s.cancelled, ctx.Err() != nil)
			return s.list(req)
		},
		ResolveURL: func(_ context.Context, resolved *config.Resolved, _ *secret.Resolver, _,
			rawURL string) ([]config.TargetEntry, error) {
			s.seen = append(s.seen, resolved)
			s.urls = append(s.urls, rawURL)
			if s.resolve == nil {
				return nil, nil
			}
			return s.resolve(rawURL)
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
			{Name: "item", Forms: []string{"g/NAME/i/ID"}, Parents: []string{"group"}},
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
	return pickerModelOn(t, listerRegistry(t), src, targets...)
}

func pickerModelOn(t *testing.T, reg *capability.Registry, src *listSource, targets ...string) *Model {
	t.Helper()
	old := pickDelay
	pickDelay = time.Millisecond
	t.Cleanup(func() { pickDelay = old })
	m, _ := toolsModel(t, reg, map[string]config.Connection{
		"lst": {Service: "wiki", Credential: "reader", Targets: targets},
	})
	m.svc = m.svc.WithTargetSource(src.targetSource())
	openEntryForm(t, m, sectionConnections, "lst")
	openTargetList(t, m)
	press(t, m, "n")
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

func TestPickerKindsFollowParentsAndTop(t *testing.T) {
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Kinds: []config.TargetKind{
			{Name: "group", Forms: []string{"g/NAME"}},
			{Name: "team", Forms: []string{"t/NAME"}},
			{Name: "item", Forms: []string{"g/NAME/i/ID"}, Parents: []string{"group"}, Top: true},
			{Name: "shared", Forms: []string{"s/ID"}, Parents: []string{"group", "team"}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := pickerModelOn(t, reg, &listSource{list: pickPage()})
	for _, c := range []struct{ parent, want string }{
		{"", "group,team,item"}, {"group", "item,shared"}, {"team", "shared"},
	} {
		if got := strings.Join(m.pickKindsUnder(c.parent), ","); got != c.want {
			t.Errorf("kinds under %q = %q, want %q", c.parent, got, c.want)
		}
	}
}

func TestATopKindWithAParentIsListedFromTheBoundTargets(t *testing.T) {
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Kinds: []config.TargetKind{
			{Name: "group", Forms: []string{"g/NAME"}},
			{Name: "item", Forms: []string{"g/NAME/i/ID"}, Parents: []string{"group"}, Top: true},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		if req.Kind != "item" || req.Parent != "" {
			t.Errorf("the picker read %+v, want items without a parent", req)
		}
		return config.TargetPage{Entries: []config.TargetEntry{item("g/a", "1")}}, nil
	}}
	m := pickerModelOn(t, reg, src, "g/a")
	pickRow(t, m, "choose a item from the provider's list")
	pump(t, m, "enter")
	if m.targetPick == nil || len(src.reqs) != 1 {
		t.Fatalf("the picker did not open on the items:\n%s", screenOf(m))
	}
	if got := src.reqs[0]; got.Kind != "item" || got.Parent != "" || !slices.Contains(src.seen[0].Targets, "g/a") {
		t.Fatalf("read %+v for %+v", got, src.seen[0])
	}
	if got := pickShown(m); len(got) != 1 || got[0] != "g/a/i/1" {
		t.Fatalf("shown %v", got)
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
	pump(t, m, "enter", "r")
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
	m.Update(pickLoadedMsg{req: 77, retried: true, err: &manage.TargetChoiceError{Code: "admin-required"}})
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
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
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

const wikiURL = "https://wiki.example.invalid/groups/a"

// typeAddress types an address into the add menu and presses enter, returning the command that resolves it.
func typeAddress(t *testing.T, m *Model, rawURL string) tea.Cmd {
	t.Helper()
	typeText(t, m, rawURL)
	_, cmd := m.Update(keyMsg("enter"))
	return cmd
}

func TestAnAddressWithOneMatchIsTakenLikeAListEntry(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("a")}, nil
	}}
	m := pickerModel(t, src)
	typeText(t, m, wikiURL)
	if rows := strings.Join(addRows(m), "|"); !strings.Contains(rows, "look up the address") {
		t.Fatalf("the typed row does not offer the lookup: %q", rows)
	}
	_, cmd := m.Update(keyMsg("enter"))
	if view := screenOf(m); !strings.Contains(view, "loading") {
		t.Fatalf("no loading state while the address is resolved:\n%s", view)
	}
	deliver(m, cmd)
	if len(src.urls) != 1 || src.urls[0] != wikiURL {
		t.Fatalf("resolved %v, want the typed address", src.urls)
	}
	if got := m.targetList.all; len(got) != 1 || got[0] != "g/a" || m.targetPick != nil || m.targetAdd != nil {
		t.Fatalf("list %v picker %v menu %v, want the one match taken", got, m.targetPick != nil, m.targetAdd != nil)
	}
	if got := m.targetLabels["g/a"]; got != "Group a" {
		t.Errorf("label %q, want the provider's name", got)
	}
}

func TestAnAddressWithSeveralMatchesOffersThemToChoose(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("a"), group("b")}, nil
	}}
	m := pickerModel(t, src)
	deliver(m, typeAddress(t, m, wikiURL))
	if m.targetPick == nil || len(pickShown(m)) != 2 {
		t.Fatalf("the matches are not offered:\n%s", screenOf(m))
	}
	pump(t, m, "right")
	if m.status != "Nothing lies below this entry" || len(m.targetPick.levels) != 1 {
		t.Errorf("an address result opened a level: %q", m.status)
	}
	pump(t, m, "tab", "down", "tab", "enter")
	if got := strings.Join(m.targetList.all, ","); got != "g/a,g/b" {
		t.Fatalf("list %q, want both matches", got)
	}
}

func TestAnAddressWithoutMatchKeepsTheInput(t *testing.T) {
	src := &listSource{list: pickPage()}
	m := pickerModel(t, src)
	deliver(m, typeAddress(t, m, wikiURL))
	if m.targetPick != nil || m.targetAdd == nil || m.targetAdd.choices.query() != wikiURL {
		t.Fatalf("picker %v menu %v query %q, want the input kept in the menu", m.targetPick != nil,
			m.targetAdd != nil, m.targetAdd.choices.query())
	}
	if !strings.Contains(m.fail, "found no entry") || strings.Contains(m.fail, "wiki.example") {
		t.Errorf("message %q, want a clear one without the address", m.fail)
	}
}

func TestAFailedAddressShowsCodeAndTextWithoutTheAddress(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return nil, &provider.Error{Class: provider.ClassAuth}
	}}
	m := pickerModel(t, src)
	deliver(m, typeAddress(t, m, wikiURL))
	if m.targetPick != nil || !strings.HasPrefix(m.fail, "auth: ") || strings.Contains(m.fail, "wiki.example") {
		t.Fatalf("picker %v error %q, want code and text with the input kept", m.targetPick != nil, m.fail)
	}
	if m.targetAdd == nil || m.targetAdd.choices.query() != wikiURL {
		t.Error("the typed address was lost")
	}
}

func TestAnAddressIsATypedTargetWhereTheProviderHasNoBrowser(t *testing.T) {
	src := &listSource{list: pickPage()}
	m := pickerModel(t, src)
	m.svc = m.svc.WithTargetSource(manage.TargetSource{})
	typeText(t, m, wikiURL)
	if rows := strings.Join(addRows(m), "|"); strings.Contains(rows, "look up") {
		t.Fatalf("a provider without a browser offers the lookup: %q", rows)
	}
	press(t, m, "enter")
	if len(src.urls) != 0 || m.targetPick != nil {
		t.Fatalf("the address was resolved: %v", src.urls)
	}
}

func TestANewConnectionReadsTheListAsADraft(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m, _ := toolsModel(t, listerRegistry(t), nil)
	m.svc = m.svc.WithTargetSource(src.targetSource())
	pickerSetup := func(m *Model) {
		openSectionByName(t, m, sectionConnections)
		pressNew(t, m)
		openTargetList(t, m)
		press(t, m, "n")
	}
	pickerSetup(m)
	openPicker(t, m)
	if len(src.seen) != 1 || src.seen[0].Name != "" || src.seen[0].Service != "wiki" ||
		src.seen[0].Credential != "reader" || src.seen[0].Provider != "lister" {
		t.Fatalf("the list was read for %+v, want the draft of the form", src.seen)
	}
	press(t, m, "esc", "esc")

	// The draft carries the targets the form holds so far.
	m2, _ := toolsModel(t, listerRegistry(t), nil)
	src2 := &listSource{list: pickPage(group("b")), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("c")}, nil
	}}
	m2.svc = m2.svc.WithTargetSource(src2.targetSource())
	pickerSetup(m2)
	deliver(m2, typeAddress(t, m2, wikiURL))
	if len(src2.seen) != 1 || src2.seen[0].Name != "" || len(m2.targetList.all) != 1 {
		t.Fatalf("the address was read for %+v, list %v", src2.seen, m2.targetList.all)
	}
}

func TestAChangedFormReadsTheDraftNotTheSavedConnection(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m, _ := toolsModel(t, listerRegistry(t), map[string]config.Connection{
		"lst": {Service: "wiki", Credential: "reader"},
	})
	other := config.Service{Provider: "lister", BaseURL: "https://other.example.invalid"}
	mustNoError(t, m.cfg.SetService("other", other))
	stored, err := m.store.Load()
	mustNoError(t, err)
	mustNoError(t, stored.SetService("other", other))
	mustNoError(t, m.store.Save(stored))
	m.svc = m.svc.WithTargetSource(src.targetSource())
	openEntryForm(t, m, sectionConnections, "lst")
	focusField(t, m, "service")
	press(t, m, "right")
	if m.fieldValue("service") != "other" {
		t.Fatalf("service = %q, want the form changed to the other one", m.fieldValue("service"))
	}
	openTargetList(t, m)
	press(t, m, "n")
	openPicker(t, m)
	if len(src.seen) != 1 || src.seen[0].Name != "" || src.seen[0].Service != "other" {
		t.Fatalf("read for %+v, want the draft with the changed service", src.seen)
	}
}

func TestADraftFailureIsShownWithCodeAndText(t *testing.T) {
	src := &listSource{list: func(config.TargetListRequest) (config.TargetPage, error) {
		return config.TargetPage{}, &manage.TargetChoiceError{Code: "unsaved-secret"}
	}}
	m, _ := toolsModel(t, listerRegistry(t), nil)
	m.svc = m.svc.WithTargetSource(src.targetSource())
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	openTargetList(t, m)
	press(t, m, "n")
	openPicker(t, m)
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "unsaved-secret: the credential has no saved secret yet") {
		t.Fatalf("the draft failure is not shown with code and text:\n%s", view)
	}
}

func TestTheGuidedSetupReadsTheListAsADraft(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m, _ := toolsModel(t, listerRegistry(t), nil)
	m.svc = m.svc.WithTargetSource(src.targetSource())
	m.screen = screenNav
	// Provider, existing service, existing credential, then the scope step with its targets row.
	press(t, m, "g", "enter", "f2", "f2")
	focusField(t, m, targetsLabel)
	if m.fields[m.focus].label != targetsLabel {
		t.Fatalf("focus on %q, want the targets row", m.fields[m.focus].label)
	}
	press(t, m, "enter", "n")
	openPicker(t, m)
	if len(src.seen) != 1 || src.seen[0].Name != "" || src.seen[0].Service != "wiki" ||
		src.seen[0].Credential != "reader" {
		t.Fatalf("the setup read the list for %+v, want its draft", src.seen)
	}
	pump(t, m, "enter")
	if got := m.targetList.all; len(got) != 1 || got[0] != "g/a" {
		t.Fatalf("list %v, want the chosen entry", got)
	}
	if m.wizard.step != stepScope {
		t.Errorf("the setup left its step: %d", m.wizard.step)
	}
}

// encryptedListerModel is an editor over an encrypted vault with no admin session and the saved connection
// "lst" of the lister provider, which reads its list through src.
func encryptedListerModel(t *testing.T, src *listSource, passphrase string) *Model {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := config.NewStore(filepath.Join(dir, "config.yaml"), listerRegistry(t))
	cfg := store.New()
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "lister", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Provider: "lister", Type: config.CredentialTypeKeyring}))
	mustNoError(t, cfg.SetConnection("lst", config.Connection{Service: "wiki", Credential: "reader"}))
	mustNoError(t, store.Save(cfg))
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("other", "role", "canary-seed",
		func(string) (string, error) { return passphrase, nil }))
	m, err := buildModel(store, nil, setup, nil)
	mustNoError(t, err)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.svc = m.svc.WithTargetSource(src.targetSource())
	openEntryForm(t, m, sectionConnections, "lst")
	openTargetList(t, m)
	press(t, m, "n")
	return m
}

func TestTheListOffersTheAdminLoginAndRepeatsTheRead(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := encryptedListerModel(t, src, "hunter2")
	pickRow(t, m, "from the provider's list")
	deliver(m, keyCmd(m, "enter"))
	if m.screen != screenAdminAuth || m.adminAuth == nil {
		t.Fatalf("admin-required did not offer the admin login: screen %v\n%s", m.screen, screenOf(m))
	}
	if len(src.reqs) != 0 {
		t.Fatalf("the provider was read before the login: %v", src.reqs)
	}

	typeText(t, m, "hunter2")
	if view := screenOf(m); strings.Contains(view, "hunter2") {
		t.Fatalf("the passphrase is shown:\n%s", view)
	}
	pump(t, m, "enter")
	if m.screen != screenTargets || m.targetPick == nil || m.fail != "" {
		t.Fatalf("after the login: screen %v picker %v error %q", m.screen, m.targetPick != nil, m.fail)
	}
	if len(src.reqs) != 1 || len(pickShown(m)) != 1 {
		t.Fatalf("the read was not repeated once: %d reads, shown %v", len(src.reqs), pickShown(m))
	}
	if strings.Contains(screenOf(m), "hunter2") {
		t.Error("the passphrase is shown after the login")
	}
}

func TestCancellingTheAdminLoginLeavesTheErrorInThePicker(t *testing.T) {
	src := &listSource{list: pickPage(group("a"))}
	m := encryptedListerModel(t, src, "hunter2")
	pickRow(t, m, "from the provider's list")
	deliver(m, keyCmd(m, "enter"))
	press(t, m, "esc")
	if m.screen != screenTargets || m.targetPick == nil {
		t.Fatalf("esc in the login: screen %v picker %v", m.screen, m.targetPick != nil)
	}
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	if !strings.Contains(view, "admin-required: choosing targets needs the vault passphrase first") {
		t.Fatalf("admin-required is not shown:\n%s", view)
	}
	if len(src.reqs) != 0 {
		t.Fatalf("a refused read reached the provider: %v", src.reqs)
	}
}

func TestAnAddressOffersTheAdminLoginToo(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("a")}, nil
	}}
	m := encryptedListerModel(t, src, "hunter2")
	deliver(m, typeAddress(t, m, wikiURL))
	if m.screen != screenAdminAuth {
		t.Fatalf("screen %v, want the admin login", m.screen)
	}
	typeText(t, m, "hunter2")
	pump(t, m, "enter")
	if len(src.urls) != 1 || len(m.targetList.all) != 1 {
		t.Fatalf("resolved %v, list %v, want the address resolved after the login", src.urls, m.targetList.all)
	}
}

func keyCmd(m *Model, key string) tea.Cmd {
	_, cmd := m.Update(keyMsg(key))
	return cmd
}

// pickInSetup walks the guided setup to its scope step and takes the entries named in src from the list.
func pickInSetup(t *testing.T, m *Model, take ...string) {
	t.Helper()
	m.screen = screenNav
	press(t, m, "g", "enter", "f2", "f2")
	focusField(t, m, targetsLabel)
	press(t, m, "enter", "n")
	openPicker(t, m)
	for range take {
		press(t, m, "tab", "down")
	}
	pump(t, m, "enter")
	if m.screen != screenTargets || m.targetPick != nil {
		t.Fatalf("the choice was not taken: screen %v\n%s", m.screen, screenOf(m))
	}
}

func TestTheGuidedSetupSavesTheProviderNameAsLabel(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"))}
	m, path := toolsModel(t, listerRegistry(t), nil)
	m.svc = m.svc.WithTargetSource(src.targetSource())
	pickInSetup(t, m, "g/a")
	press(t, m, "f2", "f2") // the effects step, the summary
	pump(t, m, "f2")
	if m.wizard == nil || m.wizard.saved == "" {
		t.Fatalf("the setup was not saved: screen %v error %q", m.screen, m.fail)
	}
	saved := savedConnection(t, path, listerRegistry(t), m.wizard.saved)
	if got := saved.TargetLabels; len(got) != 1 || got["g/a"] != "Group a" {
		t.Fatalf("labels %v, targets %v, want the provider's name for the chosen target", got, saved.TargetValues())
	}
}

func TestATemplateLabelOfTheSetupIsNotReplacedByTheProviderName(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"))}
	m, _ := toolsModel(t, listerRegistry(t), nil)
	m.svc = m.svc.WithTargetSource(src.targetSource())
	pickInSetup(t, m, "g/a", "g/b")
	mustNoError(t, m.cfg.SetConnection("tpl", config.Connection{Service: "wiki", Credential: "reader",
		Targets: []string{"g/a", "g/z"}, TargetLabels: map[string]string{"g/a": "Mine", "g/z": "Zed"}}))
	m.wizard.template = "tpl"
	press(t, m, "f2") // leaving the scope step keeps the row
	draft, _, _ := m.setupDraft(stepScope)
	want := map[string]string{"g/a": "Mine", "g/b": "Group b", "g/z": "Zed"}
	if !maps.Equal(draft.TargetLabels, want) {
		t.Fatalf("labels %v, want %v", draft.TargetLabels, want)
	}
}

func TestAnEntryOfANestedKindOpensALevelOfTheSameKind(t *testing.T) {
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Kinds: []config.TargetKind{
			{Name: "folder", Forms: []string{"folder", "folder/PATH"}, Nested: true},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := &listSource{list: func(req config.TargetListRequest) (config.TargetPage, error) {
		parent := req.Parent
		if parent == "" {
			parent = "folder"
		}
		return config.TargetPage{Entries: []config.TargetEntry{
			{Target: parent + "/sub", Name: "sub", Kind: "folder", HasChildren: true},
		}}, nil
	}}
	m := pickerModelOn(t, reg, src)
	openPicker(t, m)
	pump(t, m, "right")
	last := src.reqs[len(src.reqs)-1]
	if last.Kind != "folder" || last.Parent != "folder/sub" || len(m.targetPick.levels) != 2 {
		t.Fatalf("opening read %+v with %d levels, want folders under folder/sub", last, len(m.targetPick.levels))
	}
	if got := m.pickKindsUnder(""); len(got) != 1 || got[0] != "folder" {
		t.Errorf("roots %v", got)
	}
}

// urlOnlyRegistry is a provider whose kinds have no list, as a service without an index of its entries.
func urlOnlyRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Kinds: []config.TargetKind{
			{Name: "group", Forms: []string{"g/NAME"}, Unlisted: true},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestAnUnlistedKindOffersNoListButResolvesAnAddress(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("a")}, nil
	}}
	m := pickerModelOn(t, urlOnlyRegistry(t), src)
	if rows := strings.Join(addRows(m), "|"); strings.Contains(rows, "provider's list") {
		t.Fatalf("the add menu offers a list: %q", rows)
	}
	typeText(t, m, wikiURL)
	if rows := strings.Join(addRows(m), "|"); !strings.Contains(rows, "look up the address") {
		t.Fatalf("the typed row does not offer the lookup: %q", rows)
	}
	_, cmd := m.Update(keyMsg("enter"))
	deliver(m, cmd)
	if got := m.targetList.all; len(got) != 1 || got[0] != "g/a" || len(src.reqs) != 0 {
		t.Fatalf("list %v, list reads %d, want the address taken without a list read", got, len(src.reqs))
	}
}

// excludableListerRegistry is the lister whose groups and items can be excluded.
func excludableListerRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "lister", Name: "Lister",
		Target: config.TargetMetadata{Label: "target", Multiple: true, Wildcard: "*", Kinds: []config.TargetKind{
			{Name: "group", Forms: []string{"g/NAME"}, ExcludeForms: []string{"g/NAME"}},
			{Name: "item", Forms: []string{"g/NAME/i/ID"}, Parents: []string{"group"}, ExcludeForms: []string{"g/NAME/i/ID"}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestThePickerTakesListEntriesAsExclusions(t *testing.T) {
	src := &listSource{list: pickPage(group("a"), group("b"), group("c"))}
	m := pickerModelOn(t, excludableListerRegistry(t), src, "g/z")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	if !m.targetAsExclusion {
		t.Fatal("the menu row did not switch to exclusions")
	}
	openPicker(t, m)
	if view := screenOf(m); !strings.Contains(view, "as exclusions") {
		t.Fatalf("the picker does not say it takes exclusions:\n%s", view)
	}
	pump(t, m, "tab", "down", "tab", "enter")
	if reach, out := exclusionState(m); !reflect.DeepEqual(reach, []string{"g/z"}) || !reflect.DeepEqual(out, []string{"g/a", "g/b"}) {
		t.Fatalf("list %v / %v, error %q", reach, out, m.fail)
	}
	if m.targetLabels["g/a"] != "Group a" {
		t.Errorf("labels %v, want the provider's name for an exclusion too", m.targetLabels)
	}
	// The menu row switches back: the entries are then targets again.
	press(t, m, "n")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	pickRow(t, m, "Add as target again")
	press(t, m, "enter")
	if m.targetAsExclusion {
		t.Fatal("the second choice left the exclusion mode on")
	}
}

func TestAnAddressIsTakenAsAnExclusion(t *testing.T) {
	src := &listSource{list: pickPage(), resolve: func(string) ([]config.TargetEntry, error) {
		return []config.TargetEntry{group("a")}, nil
	}}
	m := pickerModelOn(t, excludableListerRegistry(t), src, "g/z")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	typeText(t, m, wikiURL)
	_, cmd := m.Update(keyMsg("enter"))
	deliver(m, cmd)
	if reach, out := exclusionState(m); !reflect.DeepEqual(reach, []string{"g/z"}) || !reflect.DeepEqual(out, []string{"g/a"}) {
		t.Fatalf("list %v / %v, error %q", reach, out, m.fail)
	}
}

func TestATypedIDAndTheBuilderTakeExclusions(t *testing.T) {
	m := pickerModelOn(t, excludableListerRegistry(t), &listSource{list: pickPage()}, "g/a")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	typeText(t, m, "g/a/i/7")
	press(t, m, "enter")
	if reach, out := exclusionState(m); !reflect.DeepEqual(out, []string{"g/a/i/7"}) || !reflect.DeepEqual(reach, []string{"g/a"}) {
		t.Fatalf("typed: list %v / %v, error %q", reach, out, m.fail)
	}
	press(t, m, "n")
	pickRow(t, m, "Add as exclusion")
	press(t, m, "enter")
	pickRow(t, m, "new group")
	press(t, m, "enter")
	typeText(t, m, "b")
	press(t, m, "enter")
	if reach, out := exclusionState(m); !reflect.DeepEqual(out, []string{"g/a/i/7", "g/b"}) || len(reach) != 1 {
		t.Fatalf("built: list %v / %v, error %q", reach, out, m.fail)
	}
	// A new target afterwards is a target again.
	press(t, m, "n")
	typeText(t, m, "g/c")
	press(t, m, "enter")
	if reach, _ := exclusionState(m); !slices.Contains(reach, "g/c") {
		t.Fatalf("the mode stayed on: %v", m.targetExcluded)
	}
}
