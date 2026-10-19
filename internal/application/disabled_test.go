package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func disable(core *Core, names ...string) {
	for _, name := range names {
		connection := core.all.Connections[name]
		connection.Disabled = true
		core.all.Connections[name] = connection
	}
}

func invokeGet(core *Core, connection string) (InvokeResponse, error) {
	return core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.pages.get", Connection: connection, Arguments: json.RawMessage(`{"id":"7"}`),
	})
}

func refusalOf(t *testing.T, err error, connection string) {
	t.Helper()
	var unsupported *capability.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Reason != config.RefusalDisabled ||
		unsupported.Connection != connection {
		t.Fatalf("error = %T %v, want unsupported-capability connection-disabled for %s", err, err, connection)
	}
}

func TestDisabledConnectionOffersNothingAndKeepsItsSelection(t *testing.T) {
	core, calls := testCore(t, []string{"on", "off"}, nil, true)
	disable(core, "off")

	searched, err := core.Search(SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range searched.Operations {
		if got := offeredBy(searched.Connections, hit); len(got) != 1 || got[0] != "on" {
			t.Errorf("%s offered by %v, want only on", hit.ID, got)
		}
	}
	if only, err := core.Search(SearchRequest{Connection: "off"}); err != nil || len(only.Operations) != 0 {
		t.Errorf("Search(off) = %+v, %v, want no tool", only, err)
	}
	if provider := core.Providers().Providers[0]; provider.Connections != 1 || provider.Configured != 2 {
		t.Errorf("provider counts = %+v, want 1 offering and 2 configured", provider)
	}
	for _, c := range core.Connections("", nil).Connections {
		if want := map[string]string{"on": "", "off": StateDisabled}[c.Name]; c.State != want || c.Unusable != nil {
			t.Errorf("%s: state %q unusable %v, want state %q", c.Name, c.State, c.Unusable, want)
		}
		if c.Permissions != "read create update delete" {
			t.Errorf("%s: permissions %q lost", c.Name, c.Permissions)
		}
	}
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: "off"})
	refusalOf(t, err, "off")
	_, err = invokeGet(core, "off")
	refusalOf(t, err, "off")
	if *calls != 0 {
		t.Fatalf("handler called %d times through a disabled connection", *calls)
	}
	if _, err := invokeGet(core, "on"); err != nil {
		t.Fatalf("Invoke(on) = %v", err)
	}
}

func TestDisabledReasonOnlyWhereTheConnectionWouldOfferTheTool(t *testing.T) {
	core, _ := testCore(t, []string{"off"}, nil, true)
	disable(core, "off")
	connection := core.all.Connections["off"]
	connection.Permissions = []config.Permission{config.PermissionRead}
	core.all.Connections["off"] = connection

	got := map[string]config.Refusal{}
	searched, err := core.Search(SearchRequest{All: true, Connection: "off"})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range searched.Operations {
		got[hit.ID] = hit.Reason
	}
	want := map[string]config.Refusal{
		"fake.pages.get": config.RefusalDisabled, "fake.pages.delete": config.RefusalEffect,
	}
	for id, reason := range want {
		if got[id] != reason {
			t.Errorf("reason of %s = %q, want %q", id, got[id], reason)
		}
	}
}

func TestDisabledReasonRanksFirstAmongConnections(t *testing.T) {
	core, _ := testCore(t, []string{"off", "narrow"}, nil, true)
	disable(core, "off")
	narrow := core.all.Connections["narrow"]
	narrow.Tools = []string{"fake.pages.delete"}
	core.all.Connections["narrow"] = narrow

	searched, err := core.Search(SearchRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range searched.Operations {
		if hit.ID == "fake.pages.get" && hit.Reason != config.RefusalDisabled {
			t.Errorf("reason of get = %q, want connection-disabled ahead of not-in-tools-list", hit.Reason)
		}
		if hit.ID == "fake.pages.delete" && hit.Reason != "" {
			t.Errorf("delete is offered by narrow, reason = %q", hit.Reason)
		}
	}
}

func TestDefaultOnDisabledConnectionIsRefusedNeverSkipped(t *testing.T) {
	for name, defaults := range map[string]map[string]string{
		"tool default":     {"fake.pages.get": "off", "fake": "on"},
		"provider default": {"fake": "off"},
	} {
		core, calls := testCore(t, []string{"on", "off"}, defaults, true)
		disable(core, "off")
		_, err := invokeGet(core, "")
		if *calls != 0 {
			t.Fatalf("%s: handler called", name)
		}
		refusalOf(t, err, "off")
	}

	// The only connection of the provider.
	core, calls := testCore(t, []string{"off"}, nil, true)
	disable(core, "off")
	_, err := invokeGet(core, "")
	refusalOf(t, err, "off")
	if *calls != 0 {
		t.Fatal("handler called through the only, disabled connection")
	}
}
