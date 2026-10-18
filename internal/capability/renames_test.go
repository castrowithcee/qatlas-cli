package capability

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// renameRegistry registers the fake wiki with a rename table and, optionally, the fake tracker with its own.
func renameRegistry(t *testing.T, wiki, tracker config.ProviderMetadata) (*Registry, error) {
	t.Helper()
	reg := NewRegistry()
	wiki.ID, wiki.Name = "fakewiki", "Fake wiki"
	if err := reg.RegisterProvider(wiki, nil); err != nil {
		return reg, err
	}
	if err := reg.Register("fakewiki", operation(pagesList), operation(pagesGet)); err != nil {
		return reg, err
	}
	if tracker.ID != "" {
		tracker.Name = "Fake tracker"
		if err := reg.RegisterProvider(tracker, nil); err != nil {
			return reg, err
		}
		if err := reg.Register("faketracker", operation(tickets)); err != nil {
			return reg, err
		}
	}
	return reg, reg.ValidateRenames()
}

func TestRenameTablesAreValidated(t *testing.T) {
	valid := map[string]string{"fakewiki.pages.fetch": pagesGet.ID}
	tests := []struct {
		name          string
		wiki, tracker config.ProviderMetadata
		want          string // empty: valid
	}{
		{name: "valid", wiki: config.ProviderMetadata{RenamedTools: valid, FormerIDs: []string{"wiki"}}},
		{name: "target not registered",
			wiki: config.ProviderMetadata{RenamedTools: map[string]string{"fakewiki.pages.fetch": "fakewiki.pages.nope"}},
			want: "not a registered tool of this provider"},
		{name: "target of another provider",
			wiki:    config.ProviderMetadata{RenamedTools: map[string]string{"fakewiki.pages.fetch": tickets.ID}},
			tracker: config.ProviderMetadata{ID: "faketracker"},
			want:    "not a registered tool of this provider"},
		{name: "former id still registered",
			wiki: config.ProviderMetadata{RenamedTools: map[string]string{pagesList.ID: pagesGet.ID}},
			want: "former tool ID"},
		{name: "former id registered by another provider",
			wiki:    config.ProviderMetadata{RenamedTools: map[string]string{tickets.ID: pagesGet.ID}},
			tracker: config.ProviderMetadata{ID: "faketracker"},
			want:    "former tool ID"},
		{name: "chain",
			wiki: config.ProviderMetadata{RenamedTools: map[string]string{
				"fakewiki.pages.fetch": "fakewiki.pages.read", "fakewiki.pages.read": pagesGet.ID}},
			want: "may not form a chain"},
		{name: "same former id twice",
			wiki: config.ProviderMetadata{RenamedTools: map[string]string{"fakewiki.pages.fetch": pagesGet.ID}},
			tracker: config.ProviderMetadata{ID: "faketracker",
				RenamedTools: map[string]string{"fakewiki.pages.fetch": tickets.ID}},
			want: "is also renamed by provider"},
		{name: "renamed and removed",
			wiki: config.ProviderMetadata{RenamedTools: valid, RemovedTools: []string{"fakewiki.pages.fetch"}},
			want: "both renamed and removed"},
		{name: "empty former id",
			wiki: config.ProviderMetadata{RenamedTools: map[string]string{"": pagesGet.ID}},
			want: "empty tool ID"},
		{name: "former provider id is a registered provider",
			wiki:    config.ProviderMetadata{FormerIDs: []string{"faketracker"}},
			tracker: config.ProviderMetadata{ID: "faketracker"},
			want:    "collides"},
		{name: "registered provider collides with an earlier former id",
			wiki:    config.ProviderMetadata{FormerIDs: []string{"faketracker"}},
			tracker: config.ProviderMetadata{ID: "faketracker"},
			want:    "collides"},
		{name: "former id of two providers",
			wiki:    config.ProviderMetadata{FormerIDs: []string{"old"}},
			tracker: config.ProviderMetadata{ID: "faketracker", FormerIDs: []string{"old"}},
			want:    "also a former ID"},
		{name: "own id as former id",
			wiki: config.ProviderMetadata{FormerIDs: []string{"fakewiki"}},
			want: "its own ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := renameRegistry(t, test.wiki, test.tracker)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("error = %v, want one containing %q", err, test.want)
			}
		})
	}
}

// A provider registered after another one's table still collides with it.
func TestFormerProviderIDCollisionDoesNotDependOnRegistrationOrder(t *testing.T) {
	reg := NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "fakewiki", Name: "W", FormerIDs: []string{"faketracker"}},
		nil); err != nil {
		t.Fatal(err)
	}
	err := reg.RegisterProvider(config.ProviderMetadata{ID: "faketracker", Name: "T"}, nil)
	if err == nil || !strings.Contains(err.Error(), "former provider ID") {
		t.Fatalf("RegisterProvider() = %v, want a former ID collision", err)
	}
}

func TestRegisterRefusesAFormerToolID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "fakewiki", Name: "W",
		RenamedTools: map[string]string{pagesList.ID: pagesGet.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	err := reg.Register("fakewiki", operation(pagesList), operation(pagesGet))
	if err == nil || !strings.Contains(err.Error(), "former tool ID") {
		t.Fatalf("Register() = %v, want a former tool ID error", err)
	}
}
