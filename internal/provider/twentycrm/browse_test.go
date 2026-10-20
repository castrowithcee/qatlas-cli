package twentycrm

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestBrowseListOffersObjectsFromTheMetadataList(t *testing.T) {
	const body = `{"data":[` +
		`{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"De\u0007al\nX","fields":[]},` +
		`{"id":"` + metaOtherID + `","nameSingular":"workspaceMember","namePlural":"workspaceMembers","labelSingular":"Member"},` +
		`{"id":"` + metaFieldID + `","nameSingular":"Bad Name","namePlural":"x","labelSingular":"Bad"},` +
		`{"id":"` + metaOptID + `","nameSingular":"person","namePlural":"people","labelSingular":""}],` +
		`"pageInfo":{"hasNextPage":true,"endCursor":"` + metaOtherID + `"}}`
	var paths []string
	serve(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return jsonResponse(http.StatusOK, body), nil
	})
	red := &redact.Redactor{}
	// A bound connection still lists every object: the choice reaches beyond the targets.
	page, err := browseList(context.Background(), targetConnection("object/company"), resolver(red), red,
		config.TargetListRequest{Kind: "object"})
	if err != nil {
		t.Fatalf("browseList() = %v", err)
	}
	if len(paths) != 1 || paths[0] != metadataObjectsPath || page.ServerSearch || page.Next == "" {
		t.Errorf("paths = %v, page = %+v", paths, page)
	}
	want := []config.TargetEntry{{Target: "object/deal", Name: "DealX", Kind: "object"},
		{Target: "object/person", Name: "person", Kind: "object"}}
	if len(page.Entries) != len(want) {
		t.Fatalf("entries = %+v", page.Entries)
	}
	for i, e := range want {
		if page.Entries[i] != e {
			t.Errorf("entry %d = %+v, want %+v", i, page.Entries[i], e)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, e := range page.Entries {
		if !metadata.Target.ValidTarget(e.Kind, e.Target) {
			t.Errorf("entry %+v is not a valid target", e)
		}
	}
}

func TestBrowseListRefusesOtherRequests(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, req := range []config.TargetListRequest{{Kind: "other"}, {Kind: "object", Parent: "object/person"}} {
		if _, err := browseList(context.Background(), targetConnection(), resolver(red), red, req); err == nil {
			t.Errorf("browseList(%+v) succeeded", req)
		}
	}
	if _, err := browseList(context.Background(), nil, resolver(red), red, config.TargetListRequest{Kind: "object"}); err == nil {
		t.Error("browseList(nil) succeeded")
	}
}

func TestBrowseListPassesProviderErrorClasses(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(403, bodyCanary), nil })
	red := &redact.Redactor{}
	_, err := browseList(context.Background(), targetConnection(), resolver(red), red, config.TargetListRequest{Kind: "object"})
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v, want permission without the body", err)
	}
}

func TestBrowseResolveURLResolvesNothing(t *testing.T) {
	refuse(t)
	entries, err := browseResolveURL(context.Background(), targetConnection(), nil, nil, cloudOrigin, "https://example.twenty.com/objects/people")
	if err != nil || len(entries) != 0 {
		t.Errorf("browseResolveURL() = %v, %v", entries, err)
	}
}

func TestBrowserIsRegistered(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if !reg.HasTargetBrowser(Provider) {
		t.Error("no target browser registered")
	}
}

func TestReachesFollowsTheWorkspaceGate(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	// Tools that pass the gate may reach the transport; it answers every request with an error.
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusForbidden, "{}"), nil })
	red := &redact.Redactor{}
	for _, descriptor := range reg.Provider(Provider) {
		_, handler, _ := reg.Lookup(descriptor.ID)
		reachedUnbound, _ := metadata.Target.Reach(descriptor.ID, nil)
		if !reachedUnbound {
			t.Errorf("%s is not reached without targets", descriptor.ID)
		}
		for _, targets := range [][]string{{"object/person"}, {"object/company", "object/deal"}} {
			// The gate runs before arguments and secrets, so empty arguments tell whether it refused.
			_, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, []byte(`{}`))
			gated := err != nil && strings.Contains(err.Error(), "workspace-wide tools")
			reached, _ := metadata.Target.Reach(descriptor.ID, targets)
			if gated != workspaceTools[descriptor.ID] || reached == gated {
				t.Errorf("%s with %v: gate refused = %v, reached = %v", descriptor.ID, targets, gated, reached)
			}
		}
	}
	for id := range workspaceTools {
		if _, _, ok := reg.Lookup(id); !ok {
			t.Errorf("workspace set names unknown tool %s", id)
		}
	}
	if reached, hint := metadata.Target.Reach(metaObjectsList.ID, []string{"object/person"}); reached || hint != "" {
		t.Errorf("Reach(workspace tool, object) = %v, %q", reached, hint)
	}
	if reached, _ := metadata.Target.Reach(metaObjectsList.ID, nil); !reached {
		t.Error("workspace tool not reached without targets")
	}
	if reached, _ := metadata.Target.Reach(recordsList.ID, []string{"object/person"}); !reached {
		t.Error("record tool not reached with an object binding")
	}
}
