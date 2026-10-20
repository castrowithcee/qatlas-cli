package lexware

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestBrowseListOffersTheOrganizationOfTheKey(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgA)
	red := &redact.Redactor{}
	// A bound connection still lists the key's own organization, and nothing else.
	page, err := browseList(context.Background(), boundConnection("browse", "organization/"+orgB), resolver(red), red,
		config.TargetListRequest{Kind: "organization"})
	if err != nil {
		t.Fatalf("browseList() = %v", err)
	}
	if len(page.Entries) != 1 || page.Next != "" {
		t.Fatalf("page = %+v", page)
	}
	if e := page.Entries[0]; e.Target != "organization/"+orgA || e.Kind != "organization" || e.Name != "Example" {
		t.Errorf("entry = %+v", e)
	}
	if profiles.Load() != 1 || business.Load() != 0 {
		t.Errorf("profile requests = %d, business requests = %d, want 1 and 0", profiles.Load(), business.Load())
	}
}

func TestBrowseListRefusesOtherRequestsAndBadProfiles(t *testing.T) {
	resetOrganizations(t)
	serveOrganization(t, "not-a-uuid")
	red := &redact.Redactor{}
	for _, req := range []config.TargetListRequest{{Kind: "organization"}, {Kind: "other"}, {Kind: "organization", Cursor: "1"},
		{Kind: "organization", Parent: "organization/" + orgA}} {
		if _, err := browseList(context.Background(), resolvedConnection("browse-bad", "lexware-key", primaryEnv),
			resolver(red), red, req); err == nil {
			t.Errorf("browseList(%+v) succeeded", req)
		}
	}
}

func TestBrowseListPassesProviderErrorClasses(t *testing.T) {
	resetOrganizations(t)
	const canary = "foreign-canary-body"
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(401, canary), nil })
	red := &redact.Redactor{}
	_, err := browseList(context.Background(), resolvedConnection("browse-auth", "lexware-key", primaryEnv), resolver(red), red,
		config.TargetListRequest{Kind: "organization"})
	if classOf(err) != provider.ClassAuth || strings.Contains(err.Error(), canary) {
		t.Errorf("err = %v, want auth without the body", err)
	}
}

func TestBrowseResolveURLResolvesNothing(t *testing.T) {
	entries, err := browseResolveURL(context.Background(), resolvedConnection("u", "lexware-key", primaryEnv), nil, nil,
		"https://api.lexware.io", "https://app.lexoffice.de/")
	if err != nil || len(entries) != 0 {
		t.Errorf("browseResolveURL() = %v, %v", entries, err)
	}
}
