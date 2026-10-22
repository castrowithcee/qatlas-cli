package forge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// repositoryJSON is a repository object with the members of both products' swagger files.
func repositoryJSON(fullName string, id int) string {
	owner, name, _ := strings.Cut(fullName, "/")
	repo := map[string]any{
		"id": id, "name": name, "full_name": fullName, "description": "d", "private": true, "fork": true,
		"owner": map[string]any{"id": 9, "login": owner, "email": "owner@example.test", "last_login": "2026-10-01",
			"login_name": "ldap-owner", "source_id": 3, "prohibit_login": false, "pronouns": "they", "type": "organization"},
		"parent": map[string]any{"id": 77, "full_name": "upstream/project", "description": "foreign display text",
			"owner": map[string]any{"login": "upstream", "email": "upstream@example.test"}},
		"original_url":       "https://" + "user:secret" + "@mirror.example.test/repo.git",
		"topics":             []string{"go", "forge"},
		"licenses":           []string{"MIT"},
		"branch_count":       4,
		"has_code":           true,
		"wiki_branch":        "main",
		"has_wiki_contents":  true,
		"permissions":        map[string]any{"admin": false, "push": true, "pull": true},
		"internal_tracker":   map[string]any{"enable_time_tracker": true},
		"external_wiki":      map[string]any{"external_wiki_url": "https://wiki.example.test"},
		"repo_transfer":      map[string]any{"doer": map[string]any{"id": 5, "login": "bob", "email": "bob@example.test"}, "teams": []any{map[string]any{"id": 2, "name": "core", "permission": "write"}}},
		"stars_count":        12,
		"default_branch":     "main",
		"object_format_name": "sha1",
		"html_url":           "https://" + apiHost + "/" + fullName,
		"undocumented":       "x",
	}
	encoded, _ := json.Marshal(repo)
	return string(encoded)
}

func TestRepositoriesGetReadsEveryDocumentedMember(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			if c.path == "/api/v1/repos/acme/widget" {
				return jsonResponse(http.StatusOK, repositoryJSON("acme/widget", 1))
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, repositoriesGet, "repo", `{}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if result["full_name"] != "acme/widget" || result["stars_count"] != float64(12) || result["undocumented"] != nil ||
			result["original_url"] != nil {
			t.Errorf("%s: result %v", p.id, result)
		}
		owner, _ := result["owner"].(map[string]any)
		if owner["email"] != "owner@example.test" || owner["last_login"] != nil || owner["login_name"] != nil ||
			owner["source_id"] != nil {
			t.Errorf("%s: owner %v", p.id, owner)
		}
		parent, _ := result["parent"].(map[string]any)
		if len(parent) != 2 || parent["full_name"] != "upstream/project" {
			t.Errorf("%s: parent %v, want identifiers only", p.id, parent)
		}
		transfer, _ := result["repo_transfer"].(map[string]any)
		doer, _ := transfer["doer"].(map[string]any)
		if len(doer) != 2 || doer["login"] != "bob" {
			t.Errorf("%s: transfer %v, want identifiers only", p.id, transfer)
		}
		_, licenses := result["licenses"]
		_, wiki := result["wiki_branch"]
		_, pronouns := owner["pronouns"]
		if licenses != (p == gitea) || wiki != (p == forgejo) || pronouns != (p == forgejo) {
			t.Errorf("%s: product members licenses %t, wiki_branch %t, pronouns %t", p.id, licenses, wiki, pronouns)
		}
	}
}

func TestPlainAddressDropsCredentials(t *testing.T) {
	for value, want := range map[string]bool{
		"": true, "https://example.test/a.git": true, "https://u:p@example.test/a.git": false,
		"https://example.test/a.git?token=x": false, "https://u@example.test": false,
	} {
		if plainAddress(value) != want {
			t.Errorf("plainAddress(%q) = %t", value, !want)
		}
	}
}

// The argument is checked before any request, the answer by its canonical name: a repository that answers
// under another name outside the targets is refused.
func TestRepositoriesGetChecksTheTargets(t *testing.T) {
	answer := "acme/widget"
	e := newEnvironment(t, gitea, func(c call) *http.Response {
		switch c.path {
		case "/api/v1/repos/acme/widget", "/api/v1/repos/ACME/Widget", "/api/v1/repositories/7":
			return jsonResponse(http.StatusOK, repositoryJSON(answer, 7))
		}
		return notFoundResponse()
	})
	for _, tc := range []struct {
		connection, arguments string
	}{
		{"repo", `{"repository":"other/thing"}`},
		{"two", `{}`},
		{"org", `{"repository":"bob/widget"}`},
		{"repo", `{"repository":"acme/widget","id":7}`},
	} {
		*e.calls = nil
		_, err := e.invoke(t, repositoriesGet, tc.connection, tc.arguments)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 ||
			strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "bob") {
			t.Errorf("%s %s: %v after %d calls and %d reads", tc.connection, tc.arguments, err, len(*e.calls), *e.reads)
		}
	}

	for _, tc := range []struct {
		connection, arguments, answer, path string
		allowed                             bool
	}{
		{"repo", `{}`, "acme/widget", "/api/v1/repos/acme/widget", true},
		{"repo", `{"repository":"ACME/Widget"}`, "Acme/WIDGET", "/api/v1/repos/ACME/Widget", true},
		{"repo", `{"repository":"acme/widget"}`, "acme/renamed", "/api/v1/repos/acme/widget", false},
		{"org", `{"repository":"acme/widget"}`, "acme/renamed", "/api/v1/repos/acme/widget", true},
		{"org", `{"repository":"acme/widget"}`, "elsewhere/widget", "/api/v1/repos/acme/widget", false},
		{"repo", `{"id":7}`, "acme/widget", "/api/v1/repositories/7", true},
		{"repo", `{"id":7}`, "other/secret-name", "/api/v1/repositories/7", false},
		{"open", `{"id":7}`, "other/secret-name", "/api/v1/repositories/7", true},
	} {
		*e.calls = nil
		answer = tc.answer
		result, err := e.invoke(t, repositoriesGet, tc.connection, tc.arguments)
		if paths := e.paths(); !slices.Equal(paths, []string{tc.path}) {
			t.Errorf("%s %s: paths %v, want %s", tc.connection, tc.arguments, paths, tc.path)
		}
		if tc.allowed && (err != nil || result["full_name"] != tc.answer) {
			t.Errorf("%s %s: %v, %v", tc.connection, tc.arguments, result, err)
		}
		if !tc.allowed && (!isInvalidRequest(err) || strings.Contains(err.Error(), "secret-name") ||
			strings.Contains(err.Error(), "renamed") || strings.Contains(err.Error(), "elsewhere")) {
			t.Errorf("%s %s: %v, want a refusal that names nothing", tc.connection, tc.arguments, err)
		}
	}
}

func TestRepositoriesListFiltersEveryRow(t *testing.T) {
	rows := `[` + repositoryJSON("acme/widget", 1) + `,` + repositoryJSON("other/thing", 2) + `,` +
		repositoryJSON("ACME/Gadget", 3) + `,` + repositoryJSON("alice/notes", 4) + `,{"id":5}]`
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		switch c.path {
		case "/api/v1/user/repos", "/api/v1/orgs/acme/repos", "/api/v1/users/Alice/repos":
			return jsonResponse(http.StatusOK, rows)
		}
		return notFoundResponse()
	})
	names := func(result map[string]any) []string {
		var out []string
		list, _ := result["repositories"].([]any)
		for _, entry := range list {
			out = append(out, entry.(map[string]any)["full_name"].(string))
		}
		return out
	}
	for _, tc := range []struct {
		connection, arguments, path, owner string
		want                               []string
	}{
		{"open", `{}`, "/api/v1/user/repos", "", []string{"acme/widget", "other/thing", "ACME/Gadget", "alice/notes"}},
		{"org", `{}`, "/api/v1/user/repos", "", []string{"acme/widget", "ACME/Gadget"}},
		{"mixed", `{}`, "/api/v1/user/repos", "", []string{"acme/widget", "alice/notes"}},
		{"mixed", `{"owner":"orgs/acme"}`, "/api/v1/orgs/acme/repos", "orgs/acme", []string{"acme/widget"}},
		{"org", `{"owner":"orgs/acme"}`, "/api/v1/orgs/acme/repos", "orgs/acme", []string{"acme/widget", "ACME/Gadget"}},
		{"owner", `{"owner":"users/Alice"}`, "/api/v1/users/Alice/repos", "users/Alice", []string{"alice/notes"}},
	} {
		*e.calls = nil
		result, err := e.invoke(t, repositoriesList, tc.connection, tc.arguments)
		if err != nil || !slices.Equal(names(result), tc.want) || (result["owner"] != nil) != (tc.owner != "") {
			t.Errorf("%s %s: %v, %v, want %v", tc.connection, tc.arguments, names(result), err, tc.want)
		}
		if paths := e.paths(); !slices.Equal(paths, []string{tc.path}) {
			t.Errorf("%s %s: paths %v", tc.connection, tc.arguments, paths)
		}
	}
	for _, tc := range []struct{ connection, arguments string }{
		{"org", `{"owner":"orgs/other"}`},
		{"repo", `{"owner":"users/alice"}`},
		{"open", `{"owner":"orgs/acme","order_by":"name"}`},
	} {
		*e.calls = nil
		_, err := e.invoke(t, repositoriesList, tc.connection, tc.arguments)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || strings.Contains(err.Error(), "other") {
			t.Errorf("%s %s: %v after %d calls", tc.connection, tc.arguments, err, len(*e.calls))
		}
	}
	*e.calls = nil
	if _, err := e.invoke(t, repositoriesList, "open", `{"order_by":"moststars"}`); err != nil ||
		!strings.Contains((*e.calls)[0].query, "order_by=moststars") {
		t.Errorf("order_by: %v, %+v", err, *e.calls)
	}
}

// The end of a list comes from Link, not from a short batch: an instance that caps the batch at 30 still has a
// next page while Link says so, and the continuation keeps its page size.
func TestRepositoriesListPagesByLink(t *testing.T) {
	var batch []string
	for i := 0; i < 30; i++ {
		batch = append(batch, repositoryJSON("acme/r"+string(rune('a'+i%26))+string(rune('a'+i/26)), i+1))
	}
	e := newEnvironment(t, gitea, func(c call) *http.Response {
		query, _ := url.ParseQuery(c.query)
		switch query.Get("page") {
		case "1":
			return jsonResponse(http.StatusOK, `[`+strings.Join(batch, ",")+`]`, "Link",
				`<https://`+apiHost+`/api/v1/user/repos?limit=30&page=2>; rel="next",<https://`+apiHost+
					`/api/v1/user/repos?limit=30&page=3>; rel="last"`, "X-Total-Count", "61")
		case "2":
			return jsonResponse(http.StatusOK, `[`+strings.Join(batch, ",")+`]`, "Link",
				`<https://`+apiHost+`/api/v1/user/repos?limit=30&page=3>; rel="next"`)
		}
		return jsonResponse(http.StatusOK, `[`+batch[0]+`]`, "Link",
			`<https://`+apiHost+`/api/v1/user/repos?limit=30&page=1>; rel="first",<https://`+apiHost+
				`/api/v1/user/repos?limit=30&page=2>; rel="prev"`, "X-Total-Count", "61")
	})
	first, err := e.invoke(t, repositoriesList, "open", `{"limit":50}`)
	if err != nil || first["has_more"] != true || first["next_cursor"] == nil {
		t.Fatalf("first batch: %v, %v", first, err)
	}
	if query := (*e.calls)[0].query; !strings.Contains(query, "limit=50") || !strings.Contains(query, "page=1") {
		t.Errorf("first query %q", query)
	}
	cursor := first["next_cursor"].(string)
	if _, err := e.invoke(t, repositoriesList, "open", `{"limit":20,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("a continuation with another limit: %v", err)
	}
	if _, err := e.invoke(t, repositoriesList, "open", `{"owner":"orgs/acme","cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of another list: %v", err)
	}
	second, err := e.invoke(t, repositoriesList, "open", `{"cursor":"`+cursor+`"}`)
	if err != nil || second["has_more"] != true {
		t.Fatalf("second batch: %v, %v", second, err)
	}
	last := (*e.calls)[len(*e.calls)-1].query
	if !strings.Contains(last, "limit=50") || !strings.Contains(last, "page=2") {
		t.Errorf("second query %q", last)
	}
	third, err := e.invoke(t, repositoriesList, "open", `{"cursor":"`+second["next_cursor"].(string)+`"}`)
	if err != nil || third["has_more"] != false || third["next_cursor"] != nil {
		t.Fatalf("third batch: %v, %v", third, err)
	}
	if _, err := e.invoke(t, repositoriesList, "open", `{"limit":51}`); !isInvalidRequest(err) {
		t.Errorf("limit over the maximum: %v", err)
	}
}

func TestHasMoreReadsLinkThenTotalCount(t *testing.T) {
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(pairs); i += 2 {
			h.Add(pairs[i], pairs[i+1])
		}
		return h
	}
	cases := []struct {
		header            http.Header
		page, limit, rows int
		want              bool
	}{
		{header("Link", `<u?page=2>; rel="next"`), 1, 50, 3, true},
		{header("Link", `<u?page=1>; rel="first", <u?page=1>; rel="prev"`), 2, 50, 50, false},
		{header("Link", `<u?page=3>; rel="last"`, "Link", `<u?page=2>; rel=next`), 1, 50, 50, true},
		{header("X-Total-Count", "10"), 1, 50, 10, false},
		{header("X-Total-Count", "10"), 1, 5, 5, true},
		{header("X-Total-Count", "x"), 1, 5, 5, false},
		{header(), 1, 5, 5, false},
	}
	for i, tc := range cases {
		if got := hasMore(tc.header, tc.page, tc.limit, tc.rows); got != tc.want {
			t.Errorf("case %d: hasMore = %t, want %t", i, got, tc.want)
		}
	}
	binding := provider.CursorBinding("test")
	for _, cursor := range []string{"x", provider.EncodeCursor(binding, "1:30"), provider.EncodeCursor(binding, "2:99"),
		provider.EncodeCursor(binding, "2"), provider.EncodeCursor(provider.CursorBinding("other"), "2:30")} {
		if _, err := startPaging(binding, 0, cursor); err == nil {
			t.Errorf("cursor %q accepted", cursor)
		}
	}
	if page, err := startPaging(binding, 0, provider.EncodeCursor(binding, "3:20")); err != nil || page.page != 3 ||
		page.limit != 20 {
		t.Errorf("page = %+v, %v", page, err)
	}
}

func TestUnwrapListReadsTheSearchWrapper(t *testing.T) {
	entries, err := unwrapList("op", "Gitea", json.RawMessage(`{"ok":true,"data":[{"id":1},{"id":2}]}`), true)
	if err != nil || len(entries) != 2 {
		t.Fatalf("wrapped: %v, %v", entries, err)
	}
	if _, err := unwrapList("op", "Gitea", json.RawMessage(`{"ok":false,"data":[]}`), true); err == nil {
		t.Error("a wrapper without ok is accepted")
	}
	if entries, err := unwrapList("op", "Gitea", json.RawMessage(`[{"id":1}]`), false); err != nil || len(entries) != 1 {
		t.Errorf("plain: %v, %v", entries, err)
	}
	if _, err := unwrapList("op", "Gitea", json.RawMessage(`{"ok":true,"data":[]}`), false); err == nil {
		t.Error("a wrapper is accepted as a plain list")
	}
}
