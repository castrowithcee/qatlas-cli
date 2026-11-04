package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

const (
	idLister = "11111111111111111111111111111111"
	idGetter = "22222222222222222222222222222222"
)

func setID(core *Core, name, id string) {
	connection := core.all.Connections[name]
	connection.ID = id
	core.all.Connections[name] = connection
}

func invokeVia(core *Core, selector string) (InvokeResponse, error) {
	return core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.pages.get", Connection: selector, Arguments: json.RawMessage(`{"id":"1"}`),
	})
}

func TestConnectionSelectorByNameIDAndBoth(t *testing.T) {
	core, calls := testCore(t, []string{"lister", "getter", "legacy"}, nil, true)
	setID(core, "lister", idLister)
	setID(core, "getter", idGetter)

	for _, selector := range []string{"lister", idLister, "lister|" + idLister, "renamed-long-ago|" + idLister} {
		response, err := invokeVia(core, selector)
		if err != nil || response.Connection != "lister" {
			t.Errorf("Invoke(%q) = %+v, %v, want connection lister", selector, response, err)
		}
		described, err := core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: selector})
		if err != nil || len(described.Connections) != 1 || described.Connections[0].Name != "lister" {
			t.Errorf("Describe(%q) = %+v, %v, want lister", selector, described.Connections, err)
		}
		found, err := core.Search(SearchRequest{Connection: selector})
		want, _ := core.Search(SearchRequest{Connection: "lister"})
		if err != nil || !reflect.DeepEqual(found, want) {
			t.Errorf("Search(%q) = %+v, %v, want the answer for the name: %+v", selector, found, err, want)
		}
	}
	if *calls != 4 {
		t.Errorf("calls = %d, want 4", *calls)
	}
	// In name|id the id decides: the name of another connection does not override it.
	if response, err := invokeVia(core, "getter|"+idLister); err != nil || response.Connection != "lister" {
		t.Errorf("Invoke(getter|idLister) = %+v, %v, want lister", response, err)
	}
	// A connection without an id stays usable by name and is not addressable by id.
	if response, err := invokeVia(core, "legacy"); err != nil || response.Connection != "legacy" {
		t.Errorf("Invoke(legacy) = %+v, %v", response, err)
	}
	for _, selector := range []string{"legacy|", "legacy|legacy", "|" + idLister[:31], ""} {
		if selector == "" {
			continue
		}
		var unknown *capability.UnknownConnectionError
		if _, err := invokeVia(core, selector); !errors.As(err, &unknown) {
			t.Errorf("Invoke(%q) = %v, want unknown-connection", selector, err)
		}
	}
	if core.all.Connections["legacy"].ID != "" {
		t.Error("reading assigned an id")
	}
}

func TestConnectionSelectorNameOfOneIDOfAnotherIsAmbiguous(t *testing.T) {
	// The name of "lister" equals the id of "getter".
	core, calls := testCore(t, []string{idGetter, "getter"}, nil, true)
	setID(core, idGetter, idLister)
	setID(core, "getter", idGetter)
	*calls = 0

	check := func(what string, err error) {
		t.Helper()
		var ambiguous *ConnectionAmbiguousError
		if !errors.As(err, &ambiguous) || ErrorCode(err) != "connection-ambiguous" {
			t.Fatalf("%s = %T %v, want connection-ambiguous", what, err, err)
		}
		if len(ambiguous.Connections) != 2 || !strings.Contains(err.Error(), idGetter+"|"+idLister) ||
			!strings.Contains(err.Error(), "getter|"+idGetter) {
			t.Errorf("%s = %q, want both name|id forms", what, err)
		}
	}
	_, err := invokeVia(core, idGetter)
	check("Invoke", err)
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: idGetter})
	check("Describe", err)
	_, err = core.Search(SearchRequest{Connection: idGetter})
	check("Search", err)
	_, err = core.Tools(SearchRequest{Connection: idGetter})
	check("Tools", err)
	if *calls != 0 {
		t.Errorf("an ambiguous selector reached the provider: calls = %d", *calls)
	}
	// name|id chooses.
	if response, err := invokeVia(core, idGetter+"|"+idLister); err != nil || response.Connection != idGetter {
		t.Errorf("Invoke(name|id) = %+v, %v", response, err)
	}
	if response, err := invokeVia(core, "getter|"+idGetter); err != nil || response.Connection != "getter" {
		t.Errorf("Invoke(getter|id) = %+v, %v", response, err)
	}
}

func TestConnectionIDOfAnotherProjectIsAsUnknownAsItsName(t *testing.T) {
	l := newBoundLayout(t)
	core, calls := boundCore(t, l, nil)
	setID(core, "kunde-b", idGetter)
	core.SetProjects([]string{mkdirAll(t, l.kundeA, "src")})

	byName := func() error { _, err := invokeVia(core, "kunde-b"); return err }()
	for _, selector := range []string{idGetter, "kunde-b|" + idGetter, "x|" + idGetter} {
		_, err := invokeVia(core, selector)
		var unknown *capability.UnknownConnectionError
		if !errors.As(err, &unknown) || ErrorCode(err) != "unknown-connection" {
			t.Fatalf("Invoke(%q) = %T %v, want unknown-connection", selector, err, err)
		}
		if strings.Contains(err.Error(), "kunde-b") && selector == idGetter {
			t.Errorf("Invoke(%q) = %q reveals the connection", selector, err)
		}
		if !strings.Contains(err.Error(), "bound to the paths of other projects") ||
			!strings.Contains(byName.Error(), "bound to the paths of other projects") ||
			strings.Contains(err.Error(), l.base) {
			t.Errorf("Invoke(%q) = %q, want the hint of the name: %q", selector, err, byName)
		}
		_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: selector})
		if !errors.As(err, &unknown) {
			t.Errorf("Describe(%q) = %v, want unknown-connection", selector, err)
		}
		_, err = core.Search(SearchRequest{Connection: selector})
		if !errors.As(err, &unknown) {
			t.Errorf("Search(%q) = %v, want unknown-connection", selector, err)
		}
	}
	if *calls != 0 {
		t.Errorf("calls = %d, want 0", *calls)
	}
}

func TestDisabledConnectionIsAsUnreachableByIDAsByName(t *testing.T) {
	core, calls := testCore(t, []string{"lister"}, nil, true)
	setID(core, "lister", idLister)
	connection := core.all.Connections["lister"]
	connection.Disabled = true
	core.all.Connections["lister"] = connection
	_, byName := invokeVia(core, "lister")
	_, byID := invokeVia(core, idLister)
	var unsupported *capability.UnsupportedError
	if !errors.As(byName, &unsupported) || !errors.As(byID, &unsupported) || byName.Error() != byID.Error() {
		t.Errorf("by name %v, by id %v, want the same refusal", byName, byID)
	}
	if *calls != 0 {
		t.Errorf("calls = %d, want 0", *calls)
	}
}

func TestConnectionsListCarriesTheID(t *testing.T) {
	core, _ := testCore(t, []string{"lister", "legacy"}, nil, true)
	setID(core, "lister", idLister)
	ids := map[string]string{}
	for _, connection := range core.Connections("", nil).Connections {
		ids[connection.Name] = connection.ID
	}
	if ids["lister"] != idLister || ids["legacy"] != "" {
		t.Errorf("ids = %v", ids)
	}
}

func TestUnknownConnectionMessageNamesNameOrID(t *testing.T) {
	core, _ := testCore(t, []string{"lister"}, nil, true)
	_, err := invokeVia(core, "nope")
	if err == nil || !strings.Contains(err.Error(), "name or id") {
		t.Errorf("err = %v, want a hint to name or id", err)
	}
}
