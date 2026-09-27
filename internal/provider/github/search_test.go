package github

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// lastQuery returns the q argument the last recorded request sent to GitHub.
func lastQuery(t *testing.T, f *fakeGitHub) string {
	t.Helper()
	requests := f.recorded()
	if len(requests) == 0 {
		t.Fatal("no request was recorded")
	}
	values, err := url.ParseQuery(requests[len(requests)-1].query)
	if err != nil {
		t.Fatalf("query = %q: %v", requests[len(requests)-1].query, err)
	}
	return values.Get("q")
}

// Every search tool satisfies its contract: it maps GitHub's compact search result, and reports the count
// and completeness GitHub answers alongside it.
func TestSearchToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ operation, contains string }{
		{repositoriesSearch.ID, `"repository":"octo-org/example"`},
		{codeSearch.ID, `"path":"main.go"`},
		{issuesSearch.ID, `"number":42`},
		{pullRequestsSearch.ID, `"number":42`},
		{commitsSearch.ID, `"sha":"deadbeef"`},
		{usersSearch.ID, `"login":"octocat"`},
		{organizationsSearch.ID, `"login":"octocat"`},
	} {
		result, err := invoke(t, core, tt.operation, "open", `{"terms":"example","limit":5}`, false)
		if err != nil || !strings.Contains(string(result), tt.contains) ||
			!strings.Contains(string(result), `"total_count":1`) ||
			!strings.Contains(string(result), `"incomplete_results":false`) {
			t.Errorf("%s = %s, %v", tt.operation, result, err)
		}
	}
}

// A connection with a target forces the qualifier its target allows ahead of the caller's terms, in the
// exact query GitHub receives; issues.search and pull requests.search force is:issue and is:pr ahead of it,
// and users.search forces type:user, and, with an organization owner target, the organization's members as
// well.
func TestSearchToolsForceTargetQualifiersAheadOfTerms(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	for _, tt := range []struct{ name, operation, connection, wantQ string }{
		{"no targets", repositoriesSearch.ID, "open", "example"},
		{"an organization owner target", repositoriesSearch.ID, "org", "org:octo-org example"},
		{"a personal owner target", repositoriesSearch.ID, "user", "user:octocat example"},
		{"a repository target", repositoriesSearch.ID, "starrepo", "repo:octo-org/example example"},
		{"issue search ahead of the owner qualifier", issuesSearch.ID, "org", "is:issue org:octo-org example"},
		{"pull request search ahead of the owner qualifier", pullRequestsSearch.ID, "org", "is:pr org:octo-org example"},
		{"user search of an organization's members", usersSearch.ID, "org", "type:user org:octo-org example"},
		{"user search without targets", usersSearch.ID, "open", "type:user example"},
		{"organization search without targets", organizationsSearch.ID, "open", "type:org example"},
	} {
		if _, err := invoke(t, core, tt.operation, tt.connection, `{"terms":"example"}`, false); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := lastQuery(t, f); got != tt.wantQ {
			t.Errorf("%s: q = %q, want %q", tt.name, got, tt.wantQ)
		}
	}
}

// A repository pattern target names no owner scope to force org: or user: with, so the repository-family
// search tools refuse it, and code search's is:issue-free chain reports the same refusal, before a
// credential is resolved.
func TestSearchToolsRefuseARepositoryPatternTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	cfg := discoveryConfig(serve(t, f))
	cfg.Connections["pattern"] = cfg.Connections["org"]
	pattern := cfg.Connections["pattern"]
	pattern.Targets = []string{"repos/octo-org/*"}
	cfg.Connections["pattern"] = pattern
	core := application.New(registry(t), cfg, resolver(red, &reads), red)

	for _, operation := range []string{repositoriesSearch.ID, codeSearch.ID, issuesSearch.ID, pullRequestsSearch.ID, commitsSearch.ID} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, operation, "pattern", `{"terms":"example"}`, false)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "does not record whether") {
			t.Errorf("%s: err = %v, want an invalid request naming the missing owner scope", operation, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", operation)
		}
	}
}

// A project target alone is not enough to stand for its owner, exactly as chooseOwner already refuses a
// project pattern as too little to create a project in its owner: it forces no repo:, org:, or user:
// qualifier, so every repository-family search tool, and both the user and the organization search, refuse
// it rather than widen the search to the project's whole owner, before a credential is resolved.
func TestSearchToolsRefuseAProjectOnlyTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	cfg := discoveryConfig(serve(t, f))
	cfg.Connections["project"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget}, Permissions: config.Permissions()}
	core := application.New(registry(t), cfg, resolver(red, &reads), red)

	for _, operation := range []string{repositoriesSearch.ID, codeSearch.ID, issuesSearch.ID, pullRequestsSearch.ID,
		commitsSearch.ID, usersSearch.ID, organizationsSearch.ID} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, operation, "project", `{"terms":"example"}`, false)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", operation, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", operation)
		}
	}
}

// A project target beside a repository or an owner target forces only the repository's or the owner's
// qualifier; the project itself still forces nothing, so it never widens the search to its own owner.
func TestSearchScopeIgnoresAProjectBesideARepositoryTarget(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	cfg := discoveryConfig(base)
	cfg.Connections["mixed"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{projectTarget, repoTarget}, Permissions: config.Permissions()}
	core := application.New(registry(t), cfg, resolver(red, nil), red)

	if _, err := invoke(t, core, repositoriesSearch.ID, "mixed", `{"terms":"example"}`, false); err != nil {
		t.Fatalf("%v", err)
	}
	if got := lastQuery(t, f); got != "repo:octo-org/example example" {
		t.Errorf("q = %q, want only the repository qualifier the connection also names, no org:", got)
	}
}

// Whatever mix of targets a connection names, searchScope either forces a repo:, org:, or user: qualifier or
// refuses the search outright: a non-empty allowlist that would force nothing at all never reaches GitHub
// unscoped.
func TestSearchScopeNeverSendsAnUnscopedSearchWithTargets(t *testing.T) {
	for _, targets := range [][]string{
		{projectTarget},
		{userTarget},
		{projectTarget, userTarget},
		{"orgs/octo-org"},
		{"users/octocat"},
		{repoTarget},
		{projectTarget, repoTarget},
		{userTarget, repoTarget},
		{projectTarget, userTarget, repoTarget},
		{"repos/octo-org/*"},
	} {
		allowed, err := parseAllowlist(targets)
		if err != nil {
			t.Fatalf("parseAllowlist(%v) = %v", targets, err)
		}
		forced, err := searchScope(allowed)
		if err != nil {
			continue // a refusal never sends anything to GitHub
		}
		found := false
		for _, token := range strings.Fields(forced) {
			if strings.HasPrefix(token, "repo:") || strings.HasPrefix(token, "org:") || strings.HasPrefix(token, "user:") {
				found = true
			}
		}
		if !found {
			t.Errorf("searchScope(%v) = %q, want a repo:, org:, or user: qualifier or a refusal", targets, forced)
		}
	}
}

// A term list that could escape a forced qualifier is refused before a credential is resolved, whenever a
// target forces one: a positive repo:, org:, user:, or owner: qualifier, in any case, since GitHub would
// union it with the one Qatlas forces; the OR operator; and parentheses. A negated qualifier only narrows
// further and stays allowed, and the same terms are unrestricted on a connection without targets.
func TestSearchToolsRefuseEscapesOfAForcedQualifierBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, terms, message string }{
		{"a positive repo qualifier", "repo:other/example", "may not repeat repo:"},
		{"a case-varied org qualifier", "ORG:other", "may not repeat org:"},
		{"a user qualifier", "user:other", "may not repeat user:"},
		{"an owner qualifier", "owner:other", "may not repeat owner:"},
		{"the OR operator", "example OR repo:other/example", "may not use the OR operator"},
		{"parentheses", "(example)", "may not use parentheses"},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, repositoriesSearch.ID, "org", `{"terms":"`+tt.terms+`"}`, false)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message) {
			t.Errorf("%s: err = %v, want an invalid request naming %q", tt.name, err, tt.message)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}

	if _, err := invoke(t, core, repositoriesSearch.ID, "org", `{"terms":"-repo:other/example example"}`, false); err != nil {
		t.Errorf("a negated repo qualifier = %v, want it allowed", err)
	}
	if _, err := invoke(t, core, repositoriesSearch.ID, "open", `{"terms":"repo:other/example"}`, false); err != nil {
		t.Errorf("the same qualifier without targets = %v, want it allowed", err)
	}
}

// github.users.search accepts no targets or exactly one organization owner target, whose members org:
// narrows the search to; a personal owner, a repository, or a project target gives it nothing to narrow by.
// github.organizations.search accepts no targets at all, since no qualifier restricts which organizations a
// search may return to one target.
func TestUserAndOrganizationSearchAreScopedByAnOrganizationOwnerTargetOnly(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ name, operation, connection, message string }{
		{"a personal owner target gives user search nothing to narrow by", usersSearch.ID, "user",
			"nothing to narrow a user search by"},
		{"a repository target refuses user search", usersSearch.ID, "starrepo", "nothing to narrow a user search by"},
		{"a repository target refuses organization search", organizationsSearch.ID, "starrepo",
			"offered only by a connection without targets"},
		{"an organization owner target still refuses organization search", organizationsSearch.ID, "org",
			"offered only by a connection without targets"},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, tt.operation, tt.connection, `{"terms":"octocat"}`, false)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.message) {
			t.Errorf("%s: err = %v, want an invalid request naming %q", tt.name, err, tt.message)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}

	if _, err := invoke(t, core, usersSearch.ID, "org", `{"terms":"octocat"}`, false); err != nil {
		t.Errorf("user search with an organization owner target = %v, want it allowed", err)
	}
}

// GitHub's search rate limit is reported as a rate-limited refusal naming the wait, through the provider's
// existing status classification.
func TestSearchRateLimitReportsAWait(t *testing.T) {
	f := &fakeGitHub{searchRateLimited: true}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	_, err := invoke(t, core, repositoriesSearch.ID, "open", `{"terms":"example"}`, false)
	if classOf(err) != provider.ClassRateLimited || !strings.Contains(err.Error(), "retry after") {
		t.Errorf("%s = %v, want a rate-limited refusal naming a wait", repositoriesSearch.ID, err)
	}
}

// Paging follows the same opaque cursor contract as the other lists: bound to the tool, the forced
// qualifiers, and the exact terms, so a cursor of different terms is refused before a credential is
// resolved.
func TestSearchPagingFollowsTheCursorContract(t *testing.T) {
	f := &fakeGitHub{searchHasNextPage: true}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	first, err := invoke(t, core, repositoriesSearch.ID, "open", `{"terms":"example","limit":1}`, false)
	var page struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	next := `{"terms":"example","limit":1,"cursor":"` + page.NextCursor + `"}`
	if _, err := invoke(t, core, repositoriesSearch.ID, "open", next, false); err != nil {
		t.Errorf("a continuation of the same terms = %v, want the next batch", err)
	}

	reads := 0
	strict := application.New(registry(t), discoveryConfig(base), resolver(red, &reads), red)
	before := len(f.recorded())
	other := `{"terms":"different","limit":1,"cursor":"` + page.NextCursor + `"}`
	if _, err := invoke(t, strict, repositoriesSearch.ID, "open", other, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of different terms = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("a foreign cursor reached the credential or GitHub")
	}
}
