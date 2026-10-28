package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

func searchRequest(p *product, connection, cursor string) application.SearchRequest {
	return application.SearchRequest{Provider: p.id, Connection: connection, Limit: 50, Cursor: cursor}
}

func listOf(t *testing.T, targets, excludes []string) allowlist {
	t.Helper()
	list, err := parseAllowlist(targets, excludes)
	if err != nil {
		t.Fatalf("parseAllowlist(%v, %v) = %v", targets, excludes, err)
	}
	return list
}

func TestExclusionWinsOverATargetAndReadsWithoutCase(t *testing.T) {
	list := listOf(t, []string{"orgs/acme", "repos/bob/tool"}, []string{"repos/Acme/Gadget", "users/Carol"})
	for _, tc := range []struct {
		owner, repo string
		want        bool
	}{
		{"acme", "widget", true}, {"ACME", "gadget", false}, {"acme", "GADGET", false}, {"bob", "tool", true},
		{"carol", "x", false}, {"dave", "x", false},
	} {
		if got := list.allowsRepository(tc.owner, tc.repo); got != tc.want {
			t.Errorf("allowsRepository(%s, %s) = %t, want %t", tc.owner, tc.repo, got, tc.want)
		}
	}
	if !list.allowsOwner("acme") || list.listsOwner("Carol") || !list.listsOwner("bob") {
		t.Error("an owner with an excluded repository stays listed; an excluded owner does not")
	}
	// users/X and orgs/X are one owner.
	for _, exclude := range []string{"users/acme", "orgs/ACME"} {
		list = listOf(t, []string{"repos/acme/widget", "repos/bob/tool"}, []string{exclude})
		if list.allowsRepository("Acme", "widget") || !list.excludedOwner("acme") || !list.allowsRepository("bob", "tool") {
			t.Errorf("%s does not take the owner out", exclude)
		}
	}
	// Only exclusions: everything else stays reachable and the list is not bound.
	list = listOf(t, nil, []string{"repos/acme/gadget"})
	if list.bound() || list.open() || !list.allowsRepository("acme", "widget") || list.allowsRepository("acme", "gadget") ||
		!list.allowsOwner("acme") || !list.allowsFullName("x/y") || list.allowsFullName("acme/Gadget") {
		t.Error("an allowlist of exclusions only is wrongly bound or lets an excluded repository through")
	}
}

func TestExclusionsAreValidatedWithoutQuotingTheValue(t *testing.T) {
	for name, tc := range map[string]struct {
		targets, excludes []string
		valid             bool
	}{
		"owner":                {nil, []string{"users/a", "orgs/b", "repos/c/d"}, true},
		"hits nothing":         {[]string{"repos/a/b"}, []string{"repos/zz/yy", "orgs/nothing"}, true},
		"repository under":     {[]string{"orgs/acme"}, []string{"repos/acme/gadget"}, true},
		"owner over repo":      {[]string{"repos/acme/widget"}, []string{"orgs/acme"}, true},
		"wildcard":             {nil, []string{"*"}, false},
		"repository wildcard":  {nil, []string{"repos/acme/*"}, false},
		"empty":                {nil, []string{""}, false},
		"bad form":             {nil, []string{"acme"}, false},
		"equals owner target":  {[]string{"orgs/acme"}, []string{"orgs/acme"}, false},
		"other scope of owner": {[]string{"orgs/acme"}, []string{"users/ACME"}, false},
		"equals repo target":   {[]string{"repos/acme/widget"}, []string{"repos/ACME/Widget"}, false},
		"duplicate":            {nil, []string{"repos/a/b", "repos/A/B"}, false},
		"duplicate owner":      {nil, []string{"users/a", "orgs/a"}, false},
	} {
		_, err := parseAllowlist(tc.targets, tc.excludes)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, valid %t", name, err, tc.valid)
		}
		if err != nil {
			for _, value := range append(tc.targets, tc.excludes...) {
				if value != "" && strings.Contains(err.Error(), value) {
					t.Errorf("%s: the error quotes %q: %v", name, value, err)
				}
			}
		}
	}
}

func TestExclusionsDoNotChangeTheToolOffer(t *testing.T) {
	// accountWide and reaches read targets only.
	only := listOf(t, nil, []string{"repos/acme/gadget", "orgs/other"})
	if err := only.accountWide(); err != nil {
		t.Errorf("accountWide with exclusions only = %v", err)
	}
	if err := listOf(t, []string{"repos/acme/widget"}, []string{"orgs/other"}).accountWide(); err == nil {
		t.Error("accountWide ignores a repository target next to exclusions")
	}
	for _, p := range products {
		reach := reaches(p)
		if ok, _ := reach(p.toolID(accountsMe), nil); !ok {
			t.Errorf("%s: the account read is hidden without targets", p.id)
		}
		offered := func(connection string) []string {
			e := newEnvironment(t, p, giteaAt("28.2.0", nil))
			var ids []string
			cursor := ""
			for {
				response, err := e.core.Search(searchRequest(p, connection, cursor))
				if err != nil {
					t.Fatalf("%s %s: %v", p.id, connection, err)
				}
				for _, hit := range response.Operations {
					ids = append(ids, hit.ID)
				}
				if !response.HasMore {
					break
				}
				cursor = response.NextCursor
			}
			slices.Sort(ids)
			return ids
		}
		open := offered("open")
		if len(open) < 100 {
			t.Fatalf("%s: open connection offers only %d tools", p.id, len(open))
		}
		if got := offered("exclude"); !slices.Equal(got, open) {
			t.Errorf("%s: a connection with exclusions only offers other tools than an open one: %v", p.id,
				symmetric(got, open))
		}
	}
}

func symmetric(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, "+"+x)
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			out = append(out, "-"+x)
		}
	}
	return out
}
