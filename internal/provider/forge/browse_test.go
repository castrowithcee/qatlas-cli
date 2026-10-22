package forge

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseTargets(t *testing.T, p *product, req config.TargetListRequest, handler func(call) *http.Response) (config.TargetPage, []call) {
	t.Helper()
	var calls []call
	serve(t, &calls, handler)
	reg := registry(t)
	resolved := resolvedFor(p, "repos/acme/widget")
	page, err := reg.ListTargets(context.Background(), resolved, resolver(&redact.Redactor{}, nil), &redact.Redactor{}, req)
	if err != nil {
		t.Fatalf("ListTargets(%+v) = %v", req, err)
	}
	return page, calls
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, entry := range page.Entries {
		out = append(out, entry.Target)
	}
	return out
}

// Choosing reads beyond the connection's targets: the account and its organizations as owners, and the
// repositories of an owner or of the account.
func TestBrowseListsOwnersAndRepositories(t *testing.T) {
	handler := func(c call) *http.Response {
		query, _ := url.ParseQuery(c.query)
		switch c.path {
		case "/api/v1/user":
			return jsonResponse(http.StatusOK, `{"login":"alice"}`)
		case "/api/v1/user/orgs":
			if query.Get("page") == "1" {
				return jsonResponse(http.StatusOK, `[{"name":"acme"},{"name":"bad name"}]`, "Link", `<x?page=2>; rel="next"`)
			}
			return jsonResponse(http.StatusOK, `[{"name":"zeta"}]`)
		case "/api/v1/orgs/acme/repos", "/api/v1/user/repos":
			return jsonResponse(http.StatusOK, `[{"full_name":"acme/widget"},{"full_name":"other/thing"}]`, "X-Total-Count", "2")
		}
		return notFoundResponse()
	}
	page, calls := browseTargets(t, gitea, config.TargetListRequest{Kind: kindOwner}, handler)
	if got := targetsOf(page); !slices.Equal(got, []string{"users/alice", "orgs/acme"}) || page.Next == "" {
		t.Fatalf("owners = %v, next %q", got, page.Next)
	}
	if len(calls) != 2 || calls[0].auth != "token "+tokenValue {
		t.Errorf("calls = %+v", calls)
	}
	next, _ := browseTargets(t, gitea, config.TargetListRequest{Kind: kindOwner, Cursor: page.Next}, handler)
	if got := targetsOf(next); !slices.Equal(got, []string{"orgs/zeta"}) || next.Next != "" {
		t.Errorf("second page = %v, next %q", got, next.Next)
	}
	repos, calls := browseTargets(t, forgejo, config.TargetListRequest{Kind: kindRepository, Parent: "orgs/acme"}, handler)
	if got := targetsOf(repos); !slices.Equal(got, []string{"repos/acme/widget", "repos/other/thing"}) ||
		calls[0].path != "/api/v1/orgs/acme/repos" {
		t.Errorf("repositories = %v, calls %+v", got, calls)
	}
	own, calls := browseTargets(t, forgejo, config.TargetListRequest{Kind: kindRepository}, handler)
	if len(own.Entries) != 2 || calls[0].path != "/api/v1/user/repos" {
		t.Errorf("own repositories = %v, calls %+v", own.Entries, calls)
	}
}

func TestBrowseResolvesTheDocumentedURLForms(t *testing.T) {
	handler := func(c call) *http.Response {
		switch c.path {
		case "/api/v1/orgs/acme":
			return jsonResponse(http.StatusOK, `{"name":"Acme"}`)
		case "/api/v1/users/alice":
			return jsonResponse(http.StatusOK, `{"login":"Alice"}`)
		}
		return notFoundResponse()
	}
	var calls []call
	serve(t, &calls, handler)
	reg := registry(t)
	resolve := func(raw string) []string {
		entries, err := reg.ResolveTargetURL(context.Background(), resolvedFor(gitea), resolver(&redact.Redactor{}, nil),
			&redact.Redactor{}, baseURL, raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		var out []string
		for _, entry := range entries {
			out = append(out, entry.Target)
		}
		return out
	}
	for raw, want := range map[string][]string{
		baseURL + "/acme/widget":           {"repos/acme/widget"},
		baseURL + "/acme/widget/issues/12": {"repos/acme/widget"},
		baseURL + "/acme/widget/pulls/3":   {"repos/acme/widget"},
		baseURL + "/acme/widget/src/main":  nil,
		baseURL + "/explore/repos":         nil,
		"https://other.example.test/acme":  nil,
		"http://" + apiHost + "/acme":      nil,
	} {
		calls = nil
		if got := resolve(raw); !slices.Equal(got, want) || len(calls) != 0 {
			t.Errorf("%s = %v after %d calls, want %v without a request", raw, got, len(calls), want)
		}
	}
	for raw, want := range map[string][]string{
		baseURL + "/acme":   {"orgs/Acme"},
		baseURL + "/alice/": {"users/Alice"},
		baseURL + "/nobody": nil,
	} {
		if got := resolve(raw); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", raw, got, want)
		}
	}
}
