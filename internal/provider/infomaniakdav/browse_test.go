package infomaniakdav

import (
	"context"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browse(t *testing.T, req config.TargetListRequest, targets ...string) (config.TargetPage, error) {
	t.Helper()
	red := &redact.Redactor{}
	return ListTargets(context.Background(), resolved(targets...), resolver(red), red, req)
}

func TestListTargetsOffersAllCollectionsOfBothKinds(t *testing.T) {
	serve(t, davFake(t))
	// A draft without targets and a bound connection list the same, unfiltered.
	for _, targets := range [][]string{nil, {"calendar/work"}} {
		cal, err := browse(t, config.TargetListRequest{Kind: "calendar"}, targets...)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range cal.Entries {
			if e.Kind != "calendar" || e.HasChildren {
				t.Fatalf("entry = %+v", e)
			}
			got = append(got, e.Target)
		}
		if !reflect.DeepEqual(got, []string{"calendar/other", "calendar/private", "calendar/work"}) || cal.Next != "" {
			t.Fatalf("calendars = %+v", cal)
		}
		books, err := browse(t, config.TargetListRequest{Kind: "addressbook"}, targets...)
		if err != nil || len(books.Entries) != 2 || books.Entries[0].Target != "addressbook/hidden" ||
			books.Entries[1].Name != "Main" {
			t.Fatalf("books = %+v, %v", books, err)
		}
	}
}

func TestListTargetsRefusesUnknownKindParentAndCursor(t *testing.T) {
	serve(t, davFake(t))
	for _, req := range []config.TargetListRequest{{Kind: "x"}, {Kind: "calendar", Parent: "a/1"}, {Kind: "calendar", Cursor: "2"}} {
		if _, err := browse(t, req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

func TestReachesFollowsTargetKinds(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, ok := reg.ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider not registered")
	}
	cases := []struct {
		targets []string
		reached map[string]bool
		hint    string
	}{
		{[]string{"calendar/work"}, map[string]bool{"calendar": true}, "needs addressbook/ID"},
		{[]string{"addressbook/main"}, map[string]bool{"addressbook": true}, "needs calendar/ID"},
		{[]string{"calendar/work", "addressbook/main"}, map[string]bool{"calendar": true, "addressbook": true}, ""},
	}
	for tool, kind := range toolKinds {
		for _, c := range cases {
			got, hint := meta.Target.Reach(tool, c.targets)
			if got != c.reached[kind] || (!got && hint != "needs "+kind+"/ID") {
				t.Errorf("%s with %v = %v, %q", tool, c.targets, got, hint)
			}
		}
	}
	for _, d := range reg.Provider(Provider) {
		if _, ok := toolKinds[d.ID]; !ok {
			t.Errorf("tool %s has no target kind", d.ID)
		}
	}
}
