package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// browseFake answers the reads of choosing targets: the account, its organizations, the repositories and
// projects of the organization octo-org, the repository search, and the owner lookup.
type browseFake struct {
	mu       sync.Mutex
	paths    []string
	queries  []string
	status   int // when set, every request answers this status
	orgs     int
	repos    int
	projects int
}

func (f *browseFake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *browseFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.queries = append(f.queries, r.URL.Query().Get("q"))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.status != 0 {
		w.WriteHeader(f.status)
		fmt.Fprint(w, `{"message":"refused"}`)
		return
	}
	switch path := strings.TrimPrefix(r.URL.Path, "/api/v3"); {
	case r.URL.Path == "/api/graphql":
		f.graphql(w, r)
	case path == "/user":
		fmt.Fprint(w, `{"login":"octocat"}`)
	case path == "/user/orgs":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		start, end := (page-1)*size, min(page*size, f.orgs)
		if end < f.orgs {
			w.Header().Set("Link", `<https://x.example/user/orgs?page=`+strconv.Itoa(page+1)+`>; rel="next"`)
		}
		items := []string{}
		for i := start; i < end; i++ {
			items = append(items, fmt.Sprintf(`{"login":"org-%03d"}`, i))
		}
		fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	case path == "/search/repositories":
		fmt.Fprint(w, `{"total_count":2,"incomplete_results":false,"items":[`+
			`{"full_name":"octo-org/match-a","visibility":"public"},{"full_name":"other/match-b","visibility":"public"}]}`)
	case path == "/users/octocat":
		fmt.Fprint(w, `{"login":"octocat","type":"User"}`)
	case path == "/users/octo-org":
		fmt.Fprint(w, `{"login":"octo-org","type":"Organization"}`)
	case path == "/users/robot":
		fmt.Fprint(w, `{"login":"robot","type":"Bot"}`)
	case path == "/users/other":
		fmt.Fprint(w, `{"login":"someone","type":"User"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

func (f *browseFake) graphql(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	data := make([]byte, 1<<16)
	n, _ := r.Body.Read(data)
	if err := json.Unmarshal(data[:n], &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if payload.Variables["owner"] != "octo-org" || !strings.Contains(payload.Query, "owner:organization(") {
		fmt.Fprint(w, `{"data":{"owner":null},"errors":[{"type":"NOT_FOUND","path":["owner"],"message":"no owner"}]}`)
		return
	}
	projects := strings.Contains(payload.Query, "projectsV2(first")
	total := f.repos
	if projects {
		total = f.projects
	}
	start := 0
	if after, ok := payload.Variables["after"].(string); ok {
		start, _ = strconv.Atoi(strings.TrimPrefix(after, "c-"))
		start++
	}
	end := min(start+int(payload.Variables["first"].(float64)), total)
	edges := []string{}
	last := ""
	for i := start; i < end; i++ {
		last = "c-" + strconv.Itoa(i)
		node := fmt.Sprintf(`{"name":"repo-%03d","visibility":"PRIVATE","isArchived":false}`, i)
		if projects {
			title := fmt.Sprintf("Plan %d", i+1)
			if i == 0 {
				title = `Roadmap\u0007\nline` // JSON escapes of control characters
			}
			node = fmt.Sprintf(`{"number":%d,"title":"%s","url":"x","closed":%t}`, i+1, title, i%2 == 1)
		}
		edges = append(edges, fmt.Sprintf(`{"cursor":%q,"node":%s}`, last, node))
	}
	field := "repositories"
	if projects {
		field = "projectsV2"
	}
	fmt.Fprintf(w, `{"data":{"owner":{%q:{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"edges":[%s]}}}}`,
		field, end < total, last, strings.Join(edges, ","))
}

func serveBrowse(t *testing.T, f *browseFake) string {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	previous := transport
	transport = server.Client().Transport
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
	return server.URL + "/api/v3"
}

func browseRead(base string, targets []string, req config.TargetListRequest) (config.TargetPage, error) {
	red := &redact.Redactor{}
	resolved := resolvedConnection("gh", base, "")
	resolved.Targets = targets
	return browseList(context.Background(), resolved, resolver(red, nil), red, req)
}

func browseAll(t *testing.T, base string, targets []string, req config.TargetListRequest) ([]config.TargetEntry, int) {
	t.Helper()
	var all []config.TargetEntry
	for pages := 1; pages <= 20; pages++ {
		page, err := browseRead(base, targets, req)
		if err != nil {
			t.Fatalf("browseList(%+v) = %v", req, err)
		}
		all = append(all, page.Entries...)
		if page.Next == "" {
			return all, pages
		}
		req.Cursor = page.Next
	}
	t.Fatal("the list did not end")
	return nil, 0
}

func TestBrowserRegistration(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if err := metadata.Target.ValidateKindParents(); err != nil {
		t.Fatal(err)
	}
	for kind, parent := range map[string]string{"owner": "", "repository": "owner", "project": "owner"} {
		got, ok := metadata.Target.KindByName(kind)
		if !ok || strings.Join(got.Parents, ",") != parent {
			t.Errorf("kind %s = %+v, %v; want parent %q", kind, got, ok, parent)
		}
	}
	if !metadata.Target.HasChildKind("owner") {
		t.Error("owner has no child kind")
	}
	_, err := reg.ListTargets(context.Background(), resolvedConnection("gh", "https://127.0.0.1:1", ""), nil, nil,
		config.TargetListRequest{Kind: "owner", Cursor: "x"})
	if errors.Is(err, config.ErrTargetBrowseUnsupported) {
		t.Error("the provider has no registered browser")
	}
}

func TestBrowseOwners(t *testing.T) {
	f := &browseFake{orgs: 120}
	base := serveBrowse(t, f)
	entries, pages := browseAll(t, base, nil, config.TargetListRequest{Kind: "owner"})
	if pages != 3 || len(entries) != 121 {
		t.Fatalf("owners = %d entries in %d pages, want 121 in 3", len(entries), pages)
	}
	if entries[0].Target != "users/octocat" || entries[1].Target != "orgs/org-000" || entries[120].Target != "orgs/org-119" {
		t.Errorf("owners = %+v ... %+v", entries[:2], entries[120])
	}
	for _, entry := range entries {
		if entry.Kind != "owner" || !entry.HasChildren || entry.Name == "" {
			t.Fatalf("entry = %+v", entry)
		}
	}
	// The account is read once, on the first page only.
	users := 0
	for _, path := range f.requests() {
		if strings.HasSuffix(path, "/user") {
			users++
		}
	}
	if users != 1 {
		t.Errorf("/user read %d times, want 1", users)
	}
}

func TestBrowseRepositoriesAndProjects(t *testing.T) {
	f := &browseFake{repos: 120, projects: 60}
	base := serveBrowse(t, f)
	// Targets that name only one repository neither filter nor narrow what choosing shows.
	targets := []string{"repos/octo-org/repo-001"}

	repos, pages := browseAll(t, base, targets,
		config.TargetListRequest{Kind: "repository", Parent: "orgs/octo-org"})
	if pages != 3 || len(repos) != 121 {
		t.Fatalf("repositories = %d in %d pages, want 121 in 3", len(repos), pages)
	}
	if repos[0].Target != "repos/octo-org/*" || repos[0].Kind != "repository" || repos[0].Path != "octo-org" ||
		repos[0].HasChildren || repos[0].Name == "" {
		t.Errorf("first entry = %+v, want the wildcard of the owner", repos[0])
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if !metadata.Target.ValidTarget("repository", repos[0].Target) {
		t.Error("the wildcard entry is no valid target of the repository kind")
	}
	repos = repos[1:]
	seen := map[string]bool{}
	for _, entry := range repos {
		if seen[entry.Target] || entry.Kind != "repository" || entry.Path != "octo-org" ||
			!strings.HasPrefix(entry.Target, "repos/octo-org/repo-") || entry.HasChildren {
			t.Fatalf("entry = %+v", entry)
		}
		seen[entry.Target] = true
	}
	if !seen["repos/octo-org/repo-119"] {
		t.Error("the last repository is missing")
	}

	projects, pages := browseAll(t, base, targets,
		config.TargetListRequest{Kind: "project", Parent: "orgs/octo-org"})
	if pages != 2 || len(projects) != 60 {
		t.Fatalf("projects = %d in %d pages, want 60 in 2", len(projects), pages)
	}
	if projects[0].Target != "orgs/octo-org/projects/1" || projects[59].Target != "orgs/octo-org/projects/60" ||
		projects[1].Name != "Plan 2" {
		t.Errorf("projects = %+v ... %+v", projects[:2], projects[59])
	}
	if strings.ContainsAny(projects[0].Name, "\a\n") || !strings.HasPrefix(projects[0].Name, "Roadmap") {
		t.Errorf("project name = %q; want control characters replaced", projects[0].Name)
	}
	page, _ := browseRead(base, nil, config.TargetListRequest{Kind: "project", Parent: "orgs/octo-org"})
	if page.ServerSearch {
		t.Error("projects claim a server search")
	}
}

func TestBrowseRepositorySearch(t *testing.T) {
	f := &browseFake{}
	base := serveBrowse(t, f)
	page, err := browseRead(base, nil, config.TargetListRequest{Kind: "repository", Parent: "orgs/octo-org",
		Query: `match" org:other OR (x)`})
	if err != nil {
		t.Fatal(err)
	}
	if !page.ServerSearch || len(page.Entries) != 1 || page.Entries[0].Target != "repos/octo-org/match-a" {
		t.Fatalf("page = %+v; want the one match of the owner", page)
	}
	query := f.queries[len(f.queries)-1]
	if !strings.HasPrefix(query, "org:octo-org ") || !strings.HasSuffix(query, " in:name") ||
		strings.Count(query, `"`) != 2 {
		t.Errorf("search query = %q; want one quoted phrase below org:octo-org", query)
	}
	if _, err := browseRead(base, nil, config.TargetListRequest{Kind: "repository", Parent: "users/octocat",
		Query: "x"}); err != nil || !strings.HasPrefix(f.queries[len(f.queries)-1], "user:octocat ") {
		t.Errorf("user search = %v, %q", err, f.queries[len(f.queries)-1])
	}
	// A query of quotes alone is no search.
	page, err = browseRead(base, nil, config.TargetListRequest{Kind: "repository", Parent: "orgs/nobody", Query: `""`})
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassNotFound || page.ServerSearch {
		t.Errorf("empty query = %+v, %v; want the owner list (not-found for an unknown owner)", page, err)
	}
}

func TestBrowseWildcardOnlyOnFirstPage(t *testing.T) {
	base := serveBrowse(t, &browseFake{repos: 120, projects: 60})
	req := config.TargetListRequest{Kind: "repository", Parent: "orgs/octo-org"}
	first, err := browseRead(base, nil, req)
	if err != nil || len(first.Entries) != 51 || first.Entries[0].Target != "repos/octo-org/*" {
		t.Fatalf("first page = %d entries, %v", len(first.Entries), err)
	}
	req.Cursor = first.Next
	second, err := browseRead(base, nil, req)
	if err != nil || len(second.Entries) != 50 {
		t.Fatalf("second page = %d entries, %v", len(second.Entries), err)
	}
	for _, entry := range second.Entries {
		if strings.Contains(entry.Target, "*") {
			t.Errorf("wildcard on the second page: %+v", entry)
		}
	}
	req = config.TargetListRequest{Kind: "repository", Parent: "orgs/octo-org", Query: "match"}
	searched, err := browseRead(base, nil, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range searched.Entries {
		if strings.Contains(entry.Target, "*") {
			t.Errorf("wildcard in a search: %+v", entry)
		}
	}
	projects, err := browseRead(base, nil, config.TargetListRequest{Kind: "project", Parent: "orgs/octo-org"})
	if err != nil || strings.Contains(projects.Entries[0].Target, "*") {
		t.Errorf("projects = %+v, %v; want no wildcard", projects.Entries[0], err)
	}
}

func TestBrowseErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   provider.Class
	}{{401, provider.ClassAuth}, {403, provider.ClassPermission}, {404, provider.ClassNotFound},
		{429, provider.ClassRateLimited}, {500, provider.ClassProviderError}} {
		base := serveBrowse(t, &browseFake{status: tc.status})
		for _, req := range []config.TargetListRequest{{Kind: "owner"},
			{Kind: "repository", Parent: "orgs/octo-org"}, {Kind: "project", Parent: "orgs/octo-org"},
			{Kind: "repository", Parent: "orgs/octo-org", Query: "x"}} {
			_, err := browseRead(base, nil, req)
			var failure *provider.Error
			if !errors.As(err, &failure) || failure.Class != tc.want {
				t.Errorf("status %d, %+v: error = %v, want class %s", tc.status, req, err, tc.want)
			}
		}
	}
	base := serveBrowse(t, &browseFake{})
	_, err := browseRead(base, nil, config.TargetListRequest{Kind: "repository", Parent: "orgs/missing"})
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassNotFound {
		t.Errorf("unknown owner = %v, want not-found", err)
	}
	for _, req := range []config.TargetListRequest{{Kind: "repository", Parent: "repos/a/b"},
		{Kind: "repository"}, {Kind: "other"}, {Kind: "owner", Cursor: "junk"}} {
		if _, err := browseRead(base, nil, req); err == nil {
			t.Errorf("%+v succeeded", req)
		}
	}
}

func TestBrowseResolveURL(t *testing.T) {
	const (
		api = "https://api.github.com"
		ghe = "https://api.acme.ghe.com"
	)
	for _, tc := range []struct {
		name, origin, raw string
		want              string // target of the one entry, or empty
		requests          int
	}{
		{"repository", api, "https://github.com/octo-org/tools", "repos/octo-org/tools", 0},
		{"repository issue", api, "https://github.com/octo-org/tools/issues/1", "repos/octo-org/tools", 0},
		{"repository pull", api, "https://github.com/octo-org/tools/pull/2#x", "repos/octo-org/tools", 0},
		{"repository tree", api, "https://github.com/octo-org/tools/tree/main/a/b?x=1", "repos/octo-org/tools", 0},
		{"http scheme", api, "http://github.com/octo-org/tools", "repos/octo-org/tools", 0},
		{"host case", api, "https://GitHub.com/octo-org/tools", "repos/octo-org/tools", 0},
		{"org project", api, "https://github.com/orgs/octo-org/projects/7", "orgs/octo-org/projects/7", 0},
		{"org project view", api, "https://github.com/orgs/octo-org/projects/7/views/1", "orgs/octo-org/projects/7", 0},
		{"user project", api, "https://github.com/users/octocat/projects/3/views/2?x=1", "users/octocat/projects/3", 0},
		{"project zero", api, "https://github.com/orgs/octo-org/projects/0", "", 0},
		{"project not a number", api, "https://github.com/orgs/octo-org/projects/new", "", 0},
		{"org page", api, "https://github.com/orgs/octo-org", "", 0},
		{"org people", api, "https://github.com/orgs/octo-org/people", "", 0},
		{"user owner", api, "https://github.com/octocat", "users/octocat", 1},
		{"user owner slash", api, "https://github.com/octocat/", "users/octocat", 1},
		{"org owner", api, "https://github.com/octo-org?tab=repositories", "orgs/octo-org", 1},
		{"unknown owner", api, "https://github.com/ghost-user", "", 1},
		{"bot owner", api, "https://github.com/robot", "", 1},
		{"owner of other login", api, "https://github.com/other", "", 1},
		{"settings", api, "https://github.com/settings/profile", "", 0},
		{"marketplace", api, "https://github.com/marketplace", "", 0},
		{"features", api, "https://github.com/features/actions", "", 0},
		{"topics", api, "https://github.com/topics/go", "", 0},
		{"search", api, "https://github.com/search?q=x", "", 0},
		{"notifications", api, "https://github.com/notifications", "", 0},
		{"login", api, "https://github.com/login", "", 0},
		{"explore", api, "https://github.com/explore", "", 0},
		{"sponsors", api, "https://github.com/sponsors/octocat", "", 0},
		{"apps", api, "https://github.com/apps/dependabot", "", 0},
		{"pulls", api, "https://github.com/pulls", "", 0},
		{"issues", api, "https://github.com/issues", "", 0},
		{"root", api, "https://github.com/", "", 0},
		{"bad repository name", api, "https://github.com/octo-org/bad%20name", "", 0},
		{"foreign host", api, "https://gitlab.com/octo-org/tools", "", 0},
		{"lookalike host", api, "https://github.com.evil.example/octo-org/tools", "", 0},
		{"subdomain host", api, "https://www.github.com/octo-org/tools", "", 0},
		{"port", api, "https://github.com:8443/octo-org/tools", "", 0},
		{"ghe host matches", ghe, "https://acme.ghe.com/octo-org/tools", "repos/octo-org/tools", 0},
		{"ghe project", ghe, "https://acme.ghe.com/orgs/octo-org/projects/4", "orgs/octo-org/projects/4", 0},
		{"ghe owner", ghe, "https://acme.ghe.com/octocat", "users/octocat", 1},
		{"ghe base, github.com url", ghe, "https://github.com/octo-org/tools", "", 0},
		{"ghe base, other subdomain", ghe, "https://other.ghe.com/octo-org/tools", "", 0},
		{"github base, ghe url", api, "https://acme.ghe.com/octo-org/tools", "", 0},
		{"nested ghe subdomain", "https://api.a.b.ghe.com", "https://a.b.ghe.com/octo-org/tools", "", 0},
		{"api host of unknown service", "https://api.example.org", "https://example.org/octo-org/tools", "", 0},
		{"enterprise server", "https://git.example.org", "https://git.example.org/octo-org/tools",
			"repos/octo-org/tools", 0},
		{"enterprise server, github.com url", "https://git.example.org", "https://github.com/octo-org/tools", "", 0},
		{"no origin", "", "https://github.com/octo-org/tools", "repos/octo-org/tools", 0},
		{"no origin, other host", "", "https://git.example.org/octo-org/tools", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &browseFake{}
			base := serveBrowse(t, f)
			red := &redact.Redactor{}
			entries, err := browseResolveURL(context.Background(), resolvedConnection("gh", base, ""),
				resolver(red, nil), red, tc.origin, tc.raw)
			if err != nil {
				t.Fatalf("browseResolveURL() = %v", err)
			}
			if got := len(f.requests()); got != tc.requests {
				t.Errorf("%d requests, want %d", got, tc.requests)
			}
			if tc.want == "" {
				if len(entries) != 0 {
					t.Fatalf("entries = %+v, want none", entries)
				}
				return
			}
			if len(entries) != 1 || entries[0].Target != tc.want {
				t.Fatalf("entries = %+v, want %s", entries, tc.want)
			}
			if entries[0].Name == "" {
				t.Error("entry without a name")
			}
		})
	}
}

func TestBrowseResolveURLErrors(t *testing.T) {
	base := serveBrowse(t, &browseFake{status: 403})
	red := &redact.Redactor{}
	_, err := browseResolveURL(context.Background(), resolvedConnection("gh", base, ""), resolver(red, nil), red,
		"https://api.github.com", "https://github.com/octocat")
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassPermission {
		t.Errorf("owner lookup = %v, want permission", err)
	}
	// A URL of a foreign host reads no secret and sends no request.
	reads := 0
	_, err = browseResolveURL(context.Background(), resolvedConnection("gh", base, ""), resolver(red, &reads), red,
		"https://api.github.com", "https://example.org/octocat")
	if err != nil || reads != 0 {
		t.Errorf("foreign host = %v, %d secret reads", err, reads)
	}
}

func TestBrowseTextBounds(t *testing.T) {
	long := strings.Repeat("ä", 300)
	if got := browseText(long); len(got) > maxBrowseText || !strings.HasPrefix(long, got) {
		t.Errorf("browseText cut to %d bytes", len(got))
	}
	if got := browseText(" a\tb\x00 "); got != "a b" {
		t.Errorf("browseText = %q", got)
	}
}
