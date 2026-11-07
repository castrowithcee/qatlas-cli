package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const detailRoute = "/api/v3/repos/octo-org/example"

// detailRepository is the repository example of GitHub's REST reference, renamed to the bound repository, with
// the objects a full repository links to: a parent outside the targets, a source and a template inside them.
func detailRepository(t *testing.T) string {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(exampleFullRepository), &object); err != nil {
		t.Fatal(err)
	}
	object["name"], object["full_name"] = "example", "octo-org/example"
	linked := func(name, node string, id int) map[string]any {
		return map[string]any{"id": id, "node_id": node, "name": strings.Split(name, "/")[1], "full_name": name,
			"private": false, "html_url": "https://github.com/" + name, "description": "linked", "fork": true,
			"owner": map[string]any{"login": strings.Split(name, "/")[0], "id": 9}}
	}
	object["parent"] = linked("stranger/upstream", "MDEwOlJlcG9zaXRvcnk3", 7)
	object["source"] = linked("forkuser/root", "MDEwOlJlcG9zaXRvcnk4", 8)
	object["template_repository"] = linked("octo-org/example", "MDEwOlJlcG9zaXRvcnk5", 9)
	object["organization"] = map[string]any{"login": "octo-org", "id": 4, "type": "Organization",
		"html_url": "https://github.com/octo-org", "url": "https://api.github.com/users/octo-org"}
	object["temp_clone_token"] = "ABTLWHOULUVAXGTRYU7OC2876QJ2O"
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestRepositoriesGetReportsEveryMemberAndWithholdsTheCloneToken(t *testing.T) {
	f, core := cannedCore(t, detailRepository(t), false, relationConfig)
	got, err := invoke(t, core, repositoriesGet.ID, "forkowner", `{"repository":"octo-org/example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, repositoriesGet.ID, got)
	if request := lastRequest(t, f); request.method != http.MethodGet || request.path != detailRoute {
		t.Errorf("request = %+v, want GET %s", request, detailRoute)
	}
	if strings.Contains(string(got), "temp_clone_token") || strings.Contains(string(got), "ABTLWHOULUVAX") {
		t.Errorf("the clone token reached the answer: %s", got)
	}
	object := members(t, got)
	wantMembers(t, "repository", object, map[string]string{
		"id": `1296269`, "full_name": `"octo-org/example"`, "name": `"example"`, "description": `"This your first repo!"`,
		"repository": `"octo-org/example"`,
		"organization": `{"login":"octo-org","id":4,"type":"Organization","url":"https://github.com/octo-org",` +
			`"links":{"url":"https://api.github.com/users/octo-org"}}`,
	})
	for _, name := range []string{"owner", "license", "permissions", "topics", "links"} {
		if _, ok := object[name]; !ok {
			t.Errorf("the answer lacks %s, which the example carries", name)
		}
	}
	// The parent lies outside the targets and shows identifiers only; the source's owner and the template are
	// inside them.
	sameJSON(t, "parent", object["parent"], `{"id":7,"node_id":"MDEwOlJlcG9zaXRvcnk3","full_name":"stranger/upstream"}`)
	wantMembers(t, "source", members(t, got, "source"), map[string]string{"id": `8`, "full_name": `"forkuser/root"`,
		"name": `"root"`, "description": `"linked"`})
	wantMembers(t, "template", members(t, got, "template_repository"), map[string]string{"id": `9`, "name": `"example"`})
}

func TestRepositoriesGetShowsNoLinkedRepositoryBeyondItsTargets(t *testing.T) {
	_, core := cannedCore(t, detailRepository(t), false, relationConfig)
	got, err := invoke(t, core, repositoriesGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"parent", "source"} {
		object := members(t, got, name)
		if len(object) != 3 || object["id"] == nil || object["node_id"] == nil || object["full_name"] == nil {
			t.Errorf("%s = %v, want identifiers only", name, object)
		}
	}
	if object := members(t, got, "template_repository"); object["name"] == nil {
		t.Errorf("template_repository = %v, want the repository the target names in full", object)
	}
	// An exclusion covering a linked repository takes it down to identifiers although an owner is allowed.
	cfg := relationConfig
	_, core = cannedCore(t, detailRepository(t), false, func(base string) *config.Config {
		c := cfg(base)
		conn := c.Connections["forkowner"]
		conn.Exclude = []string{"repos/forkuser/root"}
		c.Connections["forkowner"] = conn
		return c
	})
	got, err = invoke(t, core, repositoriesGet.ID, "forkowner", `{"repository":"octo-org/example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if object := members(t, got, "source"); len(object) != 3 {
		t.Errorf("excluded source = %v, want identifiers only", object)
	}
}

const languagesBody = `{"C":78769,"Python":7769,"Go":7769,"Shell":10}`

func TestLanguagesGetListsLargestFirstAndDoesNotWaitWhileComputing(t *testing.T) {
	f, core := cannedCore(t, languagesBody, false, relationConfig)
	got, err := invoke(t, core, languagesGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, languagesGet.ID, got)
	if request := lastRequest(t, f); request.path != detailRoute+"/languages" {
		t.Errorf("path = %q", request.path)
	}
	wantMembers(t, "languages", members(t, got), map[string]string{"state": `"ready"`,
		"languages": `[{"name":"C","bytes":78769},{"name":"Go","bytes":7769},{"name":"Python","bytes":7769},` +
			`{"name":"Shell","bytes":10}]`, "repository": `"octo-org/example"`})

	for _, body := range []string{``, `{}`} {
		f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, body)
			return true
		}}
		red := &redact.Redactor{}
		core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
		got, err := invoke(t, core, languagesGet.ID, "repo", `{}`, false)
		if err != nil {
			t.Fatalf("202 with body %q: %v", body, err)
		}
		coversSchema(t, languagesGet.ID, got)
		sameJSON(t, "computing", members(t, got)["state"], `"computing"`)
		if _, ok := members(t, got)["languages"]; ok {
			t.Errorf("languages while computing = %s, want none", members(t, got)["languages"])
		}
		if n := len(f.recorded()); n != 1 {
			t.Errorf("202 caused %d requests, want exactly one without a retry", n)
		}
	}
}

func TestLanguagesGetBoundsTheListAndCleansNames(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"bad\u0007name":5`)
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&body, `,"L%03d":%d`, i, 1000-i)
	}
	body.WriteString(`}`)
	_, core := cannedCore(t, body.String(), false, relationConfig)
	got, err := invoke(t, core, languagesGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var result RepositoryLanguages
	if json.Unmarshal(got, &result) != nil || len(result.Languages) != 100 || !result.LanguagesTruncated ||
		result.Languages[0].Name != "L000" {
		t.Errorf("languages = %d entries, truncated %v, want 100 and the cut visible", len(result.Languages),
			result.LanguagesTruncated)
	}
}

const contributorsBody = `[{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","user_view_type":"public",` +
	`"avatar_url":"https://github.com/images/error/octocat_happy.gif","gravatar_id":"",` +
	`"url":"https://api.github.com/users/octocat","html_url":"https://github.com/octocat",` +
	`"followers_url":"https://api.github.com/users/octocat/followers",` +
	`"following_url":"https://api.github.com/users/octocat/following{/other_user}",` +
	`"gists_url":"https://api.github.com/users/octocat/gists{/gist_id}",` +
	`"starred_url":"https://api.github.com/users/octocat/starred{/owner}{/repo}",` +
	`"subscriptions_url":"https://api.github.com/users/octocat/subscriptions",` +
	`"organizations_url":"https://api.github.com/users/octocat/orgs",` +
	`"repos_url":"https://api.github.com/users/octocat/repos",` +
	`"events_url":"https://api.github.com/users/octocat/events{/privacy}",` +
	`"received_events_url":"https://api.github.com/users/octocat/received_events",` +
	`"type":"User","site_admin":false,"contributions":32,"name":"monalisa","email":"octocat@example.com"},` +
	`{"email":"ghost@example.com","name":"Ghost\u0007Writer","type":"Anonymous","contributions":3}]`

func TestContributorsListReportsEveryMemberAndPages(t *testing.T) {
	f, core := cannedCore(t, contributorsBody, true, relationConfig)
	got, err := invoke(t, core, contributorsList.ID, "repo", `{"anon":true,"limit":2}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, contributorsList.ID, got)
	request := lastRequest(t, f)
	query, _ := url.ParseQuery(request.query)
	if request.path != detailRoute+"/contributors" || query.Get("anon") != "true" || query.Get("per_page") != "2" ||
		query.Get("page") != "1" {
		t.Errorf("request = %+v, want the fixed route with anon and GitHub's paging", request)
	}
	wantMembers(t, "contributors", members(t, got), map[string]string{"state": `"ready"`, "has_more": `true`})
	wantMembers(t, "contributor", members(t, got, "contributors", "#0"), map[string]string{
		"login": `"octocat"`, "contributions": `32`, "type": `"User"`, "name": `"monalisa"`,
		"email": `"octocat@example.com"`, "url": `"https://github.com/octocat"`, "user_view_type": `"public"`,
		"site_admin": `false`, "gravatar_id": `""`,
	})
	var list ContributorList
	if json.Unmarshal(got, &list) != nil || len(list.Contributors) != 2 {
		t.Fatalf("answer = %s", got)
	}
	anonymous := list.Contributors[1]
	if anonymous.Login != nil || valueOr(anonymous.Type) != "Anonymous" || valueOr(anonymous.Email) != "ghost@example.com" ||
		valueOr(anonymous.Name) != "Ghost Writer" || anonymous.Contributions != 3 {
		t.Errorf("anonymous contributor = %+v", anonymous)
	}

	if _, err := invoke(t, core, contributorsList.ID, "repo",
		`{"anon":true,"cursor":"`+list.NextCursor+`"}`, false); err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery(lastRequest(t, f).query)
	if query.Get("page") != "2" || query.Get("per_page") != "2" || query.Get("anon") != "true" {
		t.Errorf("continuation query = %v, want page 2 of the same batch size and filter", query)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, contributorsList.ID, "repo", `{"cursor":"`+list.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of another filter = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused cursor reached GitHub")
	}
}

func TestContributorsListOmitsAnonWhenNotAskedFor(t *testing.T) {
	f, core := cannedCore(t, `[]`, false, relationConfig)
	got, err := invoke(t, core, contributorsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if query, _ := url.ParseQuery(lastRequest(t, f).query); query.Has("anon") || query.Get("per_page") != "30" {
		t.Errorf("query = %v, want no anon and the default batch", query)
	}
	if _, ok := members(t, got)["contributors"]; ok {
		t.Errorf("an empty list = %s, want no members", got)
	}
}

func TestContributorsListReportsComputingAndEmptyStatesWithoutRetry(t *testing.T) {
	for _, tt := range []struct {
		status      int
		body, state string
	}{
		{http.StatusAccepted, ``, stateComputing}, {http.StatusAccepted, `{}`, stateComputing},
		{http.StatusNoContent, ``, stateEmpty},
	} {
		f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
			w.WriteHeader(tt.status)
			fmt.Fprint(w, tt.body)
			return true
		}}
		red := &redact.Redactor{}
		core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
		got, err := invoke(t, core, contributorsList.ID, "repo", `{}`, false)
		if err != nil {
			t.Fatalf("status %d body %q: %v", tt.status, tt.body, err)
		}
		coversSchema(t, contributorsList.ID, got)
		object := members(t, got)
		sameJSON(t, "state", object["state"], `"`+tt.state+`"`)
		if _, ok := object["contributors"]; ok {
			t.Errorf("state %s lists contributors: %s", tt.state, got)
		}
		if n := len(f.recorded()); n != 1 {
			t.Errorf("status %d caused %d requests, want one", tt.status, n)
		}
	}
}

func TestContributorsListRefusesAnEntryWithoutContributions(t *testing.T) {
	_, core := cannedCore(t, `[{"login":"octocat"}]`, false, relationConfig)
	if _, err := invoke(t, core, contributorsList.ID, "repo", `{}`, false); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("entry without contributions = %v, want an invalid response", err)
	}
}

const profileBody = `{"health_percentage":100,"description":"My first repository on GitHub!",` +
	`"documentation":"https://example.com/docs","files":{"code_of_conduct":{"name":"Contributor Covenant",` +
	`"key":"contributor_covenant","url":"https://api.github.com/codes_of_conduct/contributor_covenant",` +
	`"html_url":"https://github.com/octo-org/example/blob/master/CODE_OF_CONDUCT.md"},` +
	`"code_of_conduct_file":{"url":"https://api.github.com/repos/octo-org/example/contents/CODE_OF_CONDUCT.md",` +
	`"html_url":"https://github.com/octo-org/example/blob/master/CODE_OF_CONDUCT.md"},` +
	`"contributing":{"url":"https://api.github.com/repos/octo-org/example/contents/CONTRIBUTING","html_url":` +
	`"https://github.com/octo-org/example/blob/master/CONTRIBUTING"},` +
	`"issue_template":{"url":"https://api.github.com/repos/octo-org/example/contents/ISSUE_TEMPLATE","html_url":` +
	`"https://github.com/octo-org/example/blob/master/ISSUE_TEMPLATE"},` +
	`"pull_request_template":{"url":"https://api.github.com/repos/octo-org/example/contents/PR_TEMPLATE","html_url":` +
	`"https://github.com/octo-org/example/blob/master/PR_TEMPLATE"},` +
	`"license":{"name":"MIT License","key":"mit","spdx_id":"MIT","url":"https://api.github.com/licenses/mit",` +
	`"html_url":"https://github.com/octo-org/example/blob/master/LICENSE","node_id":"MDc6TGljZW5zZW1pdA=="},` +
	`"readme":{"url":"https://api.github.com/repos/octo-org/example/contents/README.md","html_url":` +
	`"https://github.com/octo-org/example/blob/master/README.md"}},"updated_at":"2017-02-28T19:09:29Z",` +
	`"content_reports_enabled":true}`

func TestCommunityProfileGetReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, profileBody, false, relationConfig)
	got, err := invoke(t, core, communityProfileGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, communityProfileGet.ID, got)
	if request := lastRequest(t, f); request.method != http.MethodGet || request.path != detailRoute+"/community/profile" {
		t.Errorf("request = %+v", request)
	}
	wantMembers(t, "profile", members(t, got), map[string]string{"health_percentage": `100`,
		"description": `"My first repository on GitHub!"`, "documentation": `"https://example.com/docs"`,
		"updated_at": `"2017-02-28T19:09:29Z"`, "content_reports_enabled": `true`})
	files := members(t, got, "files")
	wantMembers(t, "files", files, map[string]string{
		"code_of_conduct": `{"key":"contributor_covenant","name":"Contributor Covenant","url":` +
			`"https://github.com/octo-org/example/blob/master/CODE_OF_CONDUCT.md","links":{"url":` +
			`"https://api.github.com/codes_of_conduct/contributor_covenant"}}`,
		"readme": `{"url":"https://github.com/octo-org/example/blob/master/README.md","links":{"url":` +
			`"https://api.github.com/repos/octo-org/example/contents/README.md"}}`,
	})
	for _, role := range []string{"code_of_conduct_file", "license", "contributing", "issue_template",
		"pull_request_template"} {
		if _, ok := files[role]; !ok {
			t.Errorf("files lacks %s", role)
		}
	}
	wantMembers(t, "license", members(t, got, "files", "license"), map[string]string{"key": `"mit"`,
		"spdx_id": `"MIT"`, "node_id": `"MDc6TGljZW5zZW1pdA=="`})
}

func TestCommunityProfileGetLeavesAbsentRolesOutAndKeepsNullsOut(t *testing.T) {
	_, core := cannedCore(t, `{"health_percentage":0,"description":null,"documentation":null,"files":{`+
		`"code_of_conduct":null,"code_of_conduct_file":null,"license":null,"contributing":null,"readme":null,`+
		`"issue_template":null,"pull_request_template":null},"updated_at":null}`, false, relationConfig)
	got, err := invoke(t, core, communityProfileGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, communityProfileGet.ID, got)
	if object := members(t, got); object["files"] != nil || object["description"] != nil || object["updated_at"] != nil {
		t.Errorf("answer = %s, want only the health percentage", got)
	}
}

// Every tool names a refusal of GitHub the same way: a missing or hidden repository as not found, and a
// refused token with the right it needs.
func TestRepositoryReadsReportNotFoundAndPermissionRefusals(t *testing.T) {
	for _, tt := range []struct{ id, args, route, right string }{
		{repositoriesGet.ID, `{}`, "", "the details of this repository"},
		{languagesGet.ID, `{}`, "/languages", "the languages of this repository"},
		{contributorsList.ID, `{}`, "/contributors", "the contributors of this repository"},
		{communityProfileGet.ID, `{}`, "/community/profile", "the community profile of this repository"},
	} {
		for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
			f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"refused"}`)
				return true
			}}
			red := &redact.Redactor{}
			core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
			_, err := invoke(t, core, tt.id, "repo", tt.args, false)
			want := provider.ClassNotFound
			if status == http.StatusForbidden {
				want = provider.ClassPermission
				if err == nil || !strings.Contains(err.Error(), tt.right) || !strings.Contains(err.Error(), "Metadata: read") {
					t.Errorf("%s 403 = %v, want the right it needs naming %q", tt.id, err, tt.right)
				}
			}
			if classOf(err) != want {
				t.Errorf("%s status %d = %v, want %s", tt.id, status, err, want)
			}
			if got := lastRequest(t, f).path; got != detailRoute+tt.route {
				t.Errorf("%s path = %q", tt.id, got)
			}
		}
	}
}

// A repository outside the targets or covered by an exclusion ends before a credential is read and before
// GitHub is contacted, without naming it.
func TestRepositoryReadsRefuseForeignAndExcludedRepositoriesBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	for _, id := range []string{repositoriesGet.ID, languagesGet.ID, contributorsList.ID, communityProfileGet.ID} {
		for _, tt := range []struct{ name, connection, arguments string }{
			{"foreign repository", "repo", `{"repository":"other/secret"}`},
			{"excluded repository", "repoexcluded", `{"repository":"octo-org/example"}`},
			{"malformed repository", "repo", `{"repository":"octo-org"}`},
		} {
			*reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, id, tt.connection, tt.arguments, false)
			if !isInvalidRequest(err) {
				t.Errorf("%s %s: %v, want an invalid request", id, tt.name, err)
			}
			if *reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s %s reached the credential or GitHub", id, tt.name)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Errorf("%s %s: the refusal names the repository: %v", id, tt.name, err)
			}
		}
	}
}

func TestContributorsListRefusesBadArgumentsBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	for _, arguments := range []string{`{"anon":"yes"}`, `{"limit":0}`, `{"limit":101}`, `{"cursor":"AAAA"}`, `{"extra":1}`} {
		before := len(f.recorded())
		if _, err := invoke(t, core, contributorsList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
}
