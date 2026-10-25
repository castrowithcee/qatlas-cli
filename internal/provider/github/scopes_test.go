package github

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// fullScopes holds every scope a gate names, through the broader scopes that imply them.
const fullScopes = "repo, project, admin:org, user, admin:enterprise, workflow"

// ghScopes are those of a classic token without the user and enterprise scopes.
const ghScopes = "project, read:org, repo, workflow, write:packages"

// fullDocument resolves the gates of a document for a token with every scope.
func fullDocument(document string) string { return withScopes(document, parseScopes(fullScopes)) }

// scopeVariants are a document as GitHub receives it from a token with every scope and from one without any.
func scopeVariants(document string) []string {
	return []string{fullDocument(document), withScopes(document, map[string]bool{})}
}

// setTestScopes makes the scopes of the test token known, so no test reads them first unless it clears them.
func setTestScopes(t *testing.T, header string) {
	t.Helper()
	auth := "Bearer " + tokenValue
	grantedScopes.Lock()
	grantedScopes.byAuth[auth] = parseScopes(header)
	grantedScopes.Unlock()
	t.Cleanup(func() {
		grantedScopes.Lock()
		delete(grantedScopes.byAuth, auth)
		grantedScopes.Unlock()
	})
}

func clearTestScopes() {
	grantedScopes.Lock()
	delete(grantedScopes.byAuth, "Bearer "+tokenValue)
	grantedScopes.Unlock()
}

func TestParseScopesIncludesImpliedScopes(t *testing.T) {
	granted := parseScopes("admin:org, user,admin:enterprise , write:discussion")
	for _, scope := range []string{"admin:org", "write:org", "read:org", "user", "read:user", "user:email",
		"admin:enterprise", "read:enterprise", "write:discussion", "read:discussion"} {
		if !granted[scope] {
			t.Errorf("parseScopes lacks %s", scope)
		}
	}
	if granted[""] || len(parseScopes("")) != 0 {
		t.Error("an empty header grants a scope")
	}
}

func TestWithScopesKeepsOnlyGrantedParts(t *testing.T) {
	document := `a ` + gateUserEmail + `email` + gateEnd + ` b ` + gateTeam + `... on Team{id}` + gateEnd + ` c`
	cases := []struct{ scopes, want string }{
		{"", "a  b  c"},
		{"read:user", "a email b  c"},
		{"read:discussion", "a  b ... on Team{id} c"},
		{"write:org, user", "a email b ... on Team{id} c"},
	}
	for _, tc := range cases {
		if got := withScopes(document, parseScopes(tc.scopes)); got != tc.want {
			t.Errorf("withScopes(%q) = %q, want %q", tc.scopes, got, tc.want)
		}
	}
	if got := withScopes("query{x}", nil); got != "query{x}" {
		t.Errorf("an ungated document changed: %q", got)
	}
}

// A resolved document carries no gate, and the parts each scope removes are exactly the gated ones.
func TestScopeVariantsOfTheGatedSelections(t *testing.T) {
	for _, document := range []string{itemsQuery, issuesQuery(true), gqlOwnerSelection, gqlOrganizationSelection,
		itemReviewerSelection} {
		for _, variant := range append(scopeVariants(document), withScopes(document, parseScopes(ghScopes))) {
			if strings.ContainsAny(variant, "<>|") {
				t.Errorf("a resolved document keeps a gate: %s", variant)
			}
		}
	}
	full, none := fullDocument(itemReviewerSelection), withScopes(itemReviewerSelection, nil)
	for _, part := range []string{"name email isSiteAdmin", "... on Team{", "... on EnterpriseTeam{"} {
		if !strings.Contains(full, part) || strings.Contains(none, part) {
			t.Errorf("%q is not asked for exactly with its scope", part)
		}
	}
	gh := withScopes(gqlAccountSelection, parseScopes(ghScopes))
	if !strings.Contains(gh, "... on Organization{") || strings.Contains(gh, "EnterpriseUserAccount") ||
		strings.Contains(gh, "name email isSiteAdmin") || !strings.Contains(gh, "... on Mannequin{id databaseId name email}") {
		t.Errorf("account selection for %s: %s", ghScopes, gh)
	}
	if got := withScopes(gqlOrganizationSelection, nil); got != "__typename " {
		t.Errorf("organization selection without read:org = %q", got)
	}
}

// The scopes come from the answers: before the first one the rate limit route is read once, a token whose
// answers carry no X-OAuth-Scopes holds no scope, and every later answer keeps what is known current.
func TestDocumentsFollowTheTokenScopes(t *testing.T) {
	parts := map[string]string{"email": "name email isSiteAdmin", "organization": "... on Organization{",
		"enterprise account": "... on EnterpriseUserAccount{", "team": "... on Team{",
		"enterprise team": "... on EnterpriseTeam{"}
	cases := []struct {
		name string
		f    *fakeGitHub
		kept []string
	}{
		{"every scope", &fakeGitHub{}, []string{"email", "organization", "enterprise account", "team", "enterprise team"}},
		{"gh token", &fakeGitHub{scopes: ghScopes}, []string{"organization", "team", "enterprise team"}},
		{"discussion and enterprise", &fakeGitHub{scopes: "repo, write:discussion, read:enterprise"},
			[]string{"enterprise account", "team", "enterprise team"}},
		{"no scope", &fakeGitHub{scopes: "repo"}, nil},
		{"fine-grained", &fakeGitHub{noScopes: true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.items = []fakeItem{{id: "PVTI_1", status: "Todo"}}
			tc.f.issues = []fakeIssue{{number: 1, state: "open"}}
			base := serve(t, tc.f)
			repo, project := client(t, base, repoTarget), client(t, base, projectTarget)
			clearTestScopes()
			for range 2 {
				if _, err := repo.ListIssues(context.Background(), IssueListOptions{Limit: 1}); err != nil {
					t.Fatal(err)
				}
				if _, err := project.ListItems(context.Background(), ItemListOptions{Limit: 1}); err != nil {
					t.Fatal(err)
				}
			}
			lookups, documents := 0, map[string]string{}
			for _, request := range tc.f.recorded() {
				switch {
				case strings.HasSuffix(request.path, "/rate_limit"):
					lookups++
				case strings.Contains(request.document, "issues(first:"):
					documents["issue list"] = request.document
				case strings.Contains(request.document, "items(first:"):
					documents["item list"] = request.document
				}
			}
			if lookups != 1 {
				t.Errorf("read the scopes %d times, want once", lookups)
			}
			for list, document := range documents {
				for name, part := range parts {
					if list == "issue list" && (name == "team" || name == "enterprise team") {
						continue
					}
					if want := slices.Contains(tc.kept, name); strings.Contains(document, part) != want {
						t.Errorf("the %s asks for the %s part = %t, want %t", list, name, !want, want)
					}
				}
			}
			if len(documents) != 2 {
				t.Fatalf("sent %d of the two list documents", len(documents))
			}
		})
	}
}
