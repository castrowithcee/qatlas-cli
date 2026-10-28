package application

import (
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func markOutdated(core *Core, name string) {
	connection := core.all.Connections[name]
	connection.Targets = []string{"former/target-9c2e"}
	connection.Exclude = []string{"former/exclusion-4b7d"}
	connection.TargetsOutdated = true
	core.all.Connections[name] = connection
}

func outdatedRefusalOf(t *testing.T, err error, connection string) {
	t.Helper()
	var unsupported *capability.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Reason != config.RefusalTargetsOutdated ||
		unsupported.Connection != connection {
		t.Fatalf("error = %T %v, want unsupported-capability %s for %s", err, err, config.RefusalTargetsOutdated, connection)
	}
	if text := err.Error(); strings.Contains(text, "9c2e") || strings.Contains(text, "4b7d") {
		t.Errorf("the refusal quotes a former target or exclusion: %s", text)
	}
}

func TestOutdatedConnectionOffersNothingAndNoProviderIsCalled(t *testing.T) {
	core, calls := testCore(t, []string{"on", "old"}, nil, true)
	markOutdated(core, "old")

	searched, err := core.Search(SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range searched.Operations {
		if got := offeredBy(searched.Connections, hit); len(got) != 1 || got[0] != "on" {
			t.Errorf("%s offered by %v, want only on", hit.ID, got)
		}
	}
	if only, err := core.Search(SearchRequest{Connection: "old"}); err != nil || len(only.Operations) != 0 {
		t.Errorf("Search(old) = %+v, %v, want no tool", only, err)
	}
	all, err := core.Search(SearchRequest{All: true, Connection: "old"})
	if err != nil || len(all.Operations) == 0 {
		t.Fatalf("Search(all, old) = %+v, %v", all, err)
	}
	for _, hit := range all.Operations {
		if hit.Reason != config.RefusalTargetsOutdated {
			t.Errorf("reason of %s = %q, want %q", hit.ID, hit.Reason, config.RefusalTargetsOutdated)
		}
	}
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: "old"})
	outdatedRefusalOf(t, err, "old")
	_, err = invokeGet(core, "old")
	outdatedRefusalOf(t, err, "old")
	if *calls != 0 {
		t.Fatalf("handler called %d times through an outdated connection", *calls)
	}
	if _, err := invokeGet(core, "on"); err != nil {
		t.Fatalf("Invoke(on) = %v", err)
	}
}

func TestOutdatedConnectionIsRefusedAsDefaultAndAsTheOnlyOne(t *testing.T) {
	core, calls := testCore(t, []string{"on", "old"}, map[string]string{"fake": "old"}, true)
	markOutdated(core, "old")
	_, err := invokeGet(core, "")
	outdatedRefusalOf(t, err, "old")

	core, calls = testCore(t, []string{"old"}, nil, true)
	markOutdated(core, "old")
	_, err = invokeGet(core, "")
	outdatedRefusalOf(t, err, "old")
	if *calls != 0 {
		t.Fatalf("handler called %d times", *calls)
	}
}

func TestOutdatedReasonWinsOverDisabled(t *testing.T) {
	core, _ := testCore(t, []string{"old"}, nil, true)
	markOutdated(core, "old")
	disable(core, "old")
	_, err := invokeGet(core, "old")
	outdatedRefusalOf(t, err, "old")
}
