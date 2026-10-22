package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// serveCanned answers every request with body, announcing a following page when next is set.
func serveCanned(t *testing.T, body string, next bool) (*fakeGitHub, string) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		if next {
			w.Header().Set("Link", `<https://example.invalid/next>; rel="next"`)
		}
		fmt.Fprint(w, body)
		return true
	}
	return f, serve(t, f)
}

func cannedCore(t *testing.T, body string, next bool, cfg func(string) *config.Config) (*fakeGitHub, *application.Core) {
	t.Helper()
	f, base := serveCanned(t, body, next)
	red := &redact.Redactor{}
	return f, application.New(registry(t), cfg(base), resolver(red, nil), red)
}

// sameJSON compares two JSON documents by value.
func sameJSON(t *testing.T, name string, got json.RawMessage, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("%s: answer %s: %v", name, got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("%s: want %s: %v", name, want, err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s =\n%s\nwant\n%s", name, got, want)
	}
}

func lastRequest(t *testing.T, f *fakeGitHub) recorded {
	t.Helper()
	requests := f.recorded()
	if len(requests) == 0 {
		t.Fatal("no request was recorded")
	}
	return requests[len(requests)-1]
}

const privateUserExample = `{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","user_view_type":"private",` +
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
	`"type":"User","site_admin":false,"name":"monalisa octocat","company":"GitHub",` +
	`"blog":"https://github.com/blog","location":"San Francisco","email":"octocat@example.invalid",` +
	`"notification_email":"notify@example.invalid","hireable":false,"bio":"There once was...",` +
	`"twitter_username":"monatheoctocat","public_repos":2,"public_gists":1,"followers":20,"following":0,` +
	`"created_at":"2008-01-14T04:33:35Z","updated_at":"2008-01-14T04:33:35Z","private_gists":81,` +
	`"total_private_repos":100,"owned_private_repos":100,"disk_usage":10000,"collaborators":8,` +
	`"two_factor_authentication":true,"business_plus":true,"ldap_dn":"uid=octocat,ou=users",` +
	`"plan":{"name":"Medium","space":400,"private_repos":20,"collaborators":0}}`

const accountLinks = `{"url":"https://api.github.com/users/octocat",` +
	`"followers_url":"https://api.github.com/users/octocat/followers",` +
	`"following_url":"https://api.github.com/users/octocat/following{/other_user}",` +
	`"gists_url":"https://api.github.com/users/octocat/gists{/gist_id}",` +
	`"starred_url":"https://api.github.com/users/octocat/starred{/owner}{/repo}",` +
	`"subscriptions_url":"https://api.github.com/users/octocat/subscriptions",` +
	`"organizations_url":"https://api.github.com/users/octocat/orgs",` +
	`"repos_url":"https://api.github.com/users/octocat/repos",` +
	`"events_url":"https://api.github.com/users/octocat/events{/privacy}",` +
	`"received_events_url":"https://api.github.com/users/octocat/received_events"}`

func TestAccountsMeAnswersEveryDocumentedMember(t *testing.T) {
	_, core := cannedCore(t, privateUserExample, false, discoveryConfig)
	result, err := invoke(t, core, accountsMe.ID, "open", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, accountsMe.ID, result, `{"login":"octocat","name":"monalisa octocat","type":"User","plan":"Medium",`+
		`"id":1,"node_id":"MDQ6VXNlcjE=","user_view_type":"private","site_admin":false,`+
		`"avatar_url":"https://github.com/images/error/octocat_happy.gif","url":"https://github.com/octocat",`+
		`"links":`+accountLinks+`,"company":"GitHub","blog":"https://github.com/blog","location":"San Francisco",`+
		`"email":"octocat@example.invalid","notification_email":"notify@example.invalid","hireable":false,`+
		`"bio":"There once was...","twitter_username":"monatheoctocat","public_repos":2,"public_gists":1,`+
		`"followers":20,"following":0,"created_at":"2008-01-14T04:33:35Z","updated_at":"2008-01-14T04:33:35Z",`+
		`"private_gists":81,"total_private_repos":100,"owned_private_repos":100,"disk_usage":10000,"collaborators":8,`+
		`"two_factor_authentication":true,"business_plus":true,"ldap_dn":"uid=octocat,ou=users",`+
		`"plan_detail":{"name":"Medium","space":400,"collaborators":0,"private_repos":20}}`)
}

// An answer without the new members is the answer the tool always gave, byte for byte.
func TestAccountsMeWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	_, core := cannedCore(t, `{"login":"octocat","name":"The Octocat","type":"User","plan":{"name":"pro"},`+
		`"gravatar_id":"","email":null}`, false, discoveryConfig)
	result, err := invoke(t, core, accountsMe.ID, "open", `{}`, false)
	if err != nil || string(result) != `{"login":"octocat","name":"The Octocat","plan":"pro","type":"User"}` {
		t.Fatalf("%s = %s, %v", accountsMe.ID, result, err)
	}
}

// Members the tools answered before keep their old treatment, unclean and uncut; only a new member is
// cleaned and bounded.
func TestAccountMembersKeepTheirOldTreatment(t *testing.T) {
	long := strings.Repeat("n", 300)
	name := "Octo\x07cat " + long
	body, _ := json.Marshal(map[string]any{"login": "octocat", "type": "User", "name": name,
		"company": "Acme\x07" + long})
	_, core := cannedCore(t, string(body), false, discoveryConfig)
	result, err := invoke(t, core, accountsMe.ID, "open", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != name {
		t.Errorf("name = %q, want it unchanged", got["name"])
	}
	company, _ := got["company"].(string)
	if strings.ContainsRune(company, 7) || len([]rune(company)) != projectionNameLimit || !strings.HasPrefix(company, "Acme ") {
		t.Errorf("company = %q, want it cleaned and bounded", company)
	}
}

const searchHit = `{"login":"mojombo","id":1,"node_id":"MDQ6VXNlcjE=","user_view_type":"public",` +
	`"avatar_url":"https://secure.gravatar.com/avatar/25c7","gravatar_id":"",` +
	`"url":"https://api.github.com/users/mojombo","html_url":"https://github.com/mojombo",` +
	`"followers_url":"https://api.github.com/users/mojombo/followers",` +
	`"subscriptions_url":"https://api.github.com/users/mojombo/subscriptions",` +
	`"organizations_url":"https://api.github.com/users/mojombo/orgs",` +
	`"repos_url":"https://api.github.com/users/mojombo/repos",` +
	`"received_events_url":"https://api.github.com/users/mojombo/received_events",` +
	`"type":"%s","score":1.5,"following_url":"https://api.github.com/users/mojombo/following{/other_user}",` +
	`"gists_url":"https://api.github.com/users/mojombo/gists{/gist_id}",` +
	`"starred_url":"https://api.github.com/users/mojombo/starred{/owner}{/repo}",` +
	`"events_url":"https://api.github.com/users/mojombo/events{/privacy}","site_admin":true,` +
	`"name":"Tom","email":"tom@example.invalid","bio":"Founder","company":"Acme","blog":"https://tom.example",` +
	`"location":"SF","hireable":true,"public_repos":7,"public_gists":3,"followers":40,"following":2,` +
	`"created_at":"2007-10-20T05:24:19Z","updated_at":"2024-01-01T00:00:00Z","suspended_at":"2025-01-01T00:00:00Z"%s}`

const searchLinks = `{"url":"https://api.github.com/users/mojombo",` +
	`"followers_url":"https://api.github.com/users/mojombo/followers",` +
	`"following_url":"https://api.github.com/users/mojombo/following{/other_user}",` +
	`"gists_url":"https://api.github.com/users/mojombo/gists{/gist_id}",` +
	`"starred_url":"https://api.github.com/users/mojombo/starred{/owner}{/repo}",` +
	`"subscriptions_url":"https://api.github.com/users/mojombo/subscriptions",` +
	`"organizations_url":"https://api.github.com/users/mojombo/orgs",` +
	`"repos_url":"https://api.github.com/users/mojombo/repos",` +
	`"events_url":"https://api.github.com/users/mojombo/events{/privacy}",` +
	`"received_events_url":"https://api.github.com/users/mojombo/received_events"}`

const searchProfileWant = `"id":1,"node_id":"MDQ6VXNlcjE=","user_view_type":"public","site_admin":true,` +
	`"avatar_url":"https://secure.gravatar.com/avatar/25c7","name":"Tom","email":"tom@example.invalid",` +
	`"bio":"Founder","company":"Acme","blog":"https://tom.example","location":"SF","hireable":true,` +
	`"public_repos":7,"public_gists":3,"followers":40,"following":2,"created_at":"2007-10-20T05:24:19Z",` +
	`"updated_at":"2024-01-01T00:00:00Z","suspended_at":"2025-01-01T00:00:00Z","score":1.5,` +
	`"links":` + searchLinks

const textMatchesExample = `,"text_matches":[{"object_url":"https://api.github.com/users/mojombo",` +
	`"object_type":"User","property":"bio","fragment":"Founder of\nthings","matches":[{"text":"Founder",` +
	`"indices":[0,7]}]}]`

const textMatchesWant = `,"text_matches":[{"object_type":"User","property":"bio","fragment":"Founder of\nthings",` +
	`"matches":[{"text":"Founder","indices":[0,7]}],"links":{"object_url":"https://api.github.com/users/mojombo"}}]`

func searchPage(typ, extra string) string {
	return fmt.Sprintf(`{"total_count":12,"incomplete_results":false,"items":[`+searchHit+`]}`, typ, extra)
}

func TestAccountSearchesAnswerEveryDocumentedMember(t *testing.T) {
	_, core := cannedCore(t, searchPage("User", textMatchesExample), false, discoveryConfig)
	result, err := invoke(t, core, usersSearch.ID, "open", `{"terms":"tom","text_matches":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, usersSearch.ID, result, `{"users":[{"login":"mojombo","type":"User","url":"https://github.com/mojombo",`+
		searchProfileWant+textMatchesWant+`}],"total_count":12,"incomplete_results":false,"has_more":false}`)

	_, core = cannedCore(t, searchPage("Organization", ""), false, discoveryConfig)
	result, err = invoke(t, core, organizationsSearch.ID, "open", `{"terms":"acme"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, organizationsSearch.ID, result, `{"organizations":[{"login":"mojombo","type":"Organization",`+
		`"url":"https://github.com/mojombo",`+searchProfileWant+`}],"total_count":12,"incomplete_results":false,`+
		`"has_more":false}`)
}

// Text matches are cut with a marker, and their text is cleaned.
func TestTextMatchesAreBoundedWithMarkers(t *testing.T) {
	var matches []string
	for i := 0; i < projectionListLimit+1; i++ {
		matches = append(matches, `{"property":"bio","fragment":"`+strings.Repeat("f", projectionTextLimit+1)+`",`+
			`"matches":[{"text":"a\u0007b","indices":[1,2]}]}`)
	}
	_, core := cannedCore(t, searchPage("User", `,"text_matches":[`+strings.Join(matches, ",")+`]`), false, discoveryConfig)
	result, err := invoke(t, core, usersSearch.ID, "open", `{"terms":"tom","text_matches":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Users []struct {
			TextMatches          []map[string]any `json:"text_matches"`
			TextMatchesTruncated bool             `json:"text_matches_truncated"`
		} `json:"users"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Users) != 1 {
		t.Fatalf("%s, %v", result, err)
	}
	user := got.Users[0]
	if len(user.TextMatches) != projectionListLimit || !user.TextMatchesTruncated ||
		user.TextMatches[0]["fragment_truncated"] != true {
		t.Errorf("text matches = %d, truncated %v, first %v", len(user.TextMatches), user.TextMatchesTruncated, user.TextMatches[0])
	}
	if parts, _ := user.TextMatches[0]["matches"].([]any); len(parts) != 1 ||
		parts[0].(map[string]any)["text"] != "a b" {
		t.Errorf("matched text = %v, want the control character replaced", user.TextMatches[0]["matches"])
	}
}

// Without the new members the search answers exactly what it always answered.
func TestAccountSearchesWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	body := `{"total_count":1,"incomplete_results":false,"items":[{"login":"octocat","type":"User",` +
		`"html_url":"https://github.com/octocat"}]}`
	_, core := cannedCore(t, body, false, discoveryConfig)
	result, err := invoke(t, core, usersSearch.ID, "open", `{"terms":"octo"}`, false)
	if err != nil || string(result) != `{"has_more":false,"incomplete_results":false,"total_count":1,`+
		`"users":[{"login":"octocat","type":"User","url":"https://github.com/octocat"}]}` {
		t.Fatalf("users = %s, %v", result, err)
	}
	_, core = cannedCore(t, `{"total_count":1,"incomplete_results":false,"items":[{"login":"octocat",`+
		`"html_url":"https://github.com/octocat"}]}`, false, discoveryConfig)
	result, err = invoke(t, core, organizationsSearch.ID, "open", `{"terms":"octo"}`, false)
	if err != nil || string(result) != `{"has_more":false,"incomplete_results":false,"organizations":`+
		`[{"login":"octocat","url":"https://github.com/octocat"}],"total_count":1}` {
		t.Fatalf("organizations = %s, %v", result, err)
	}
}

func TestAccountSearchFiltersReachTheRequestAndTheCursor(t *testing.T) {
	for _, tool := range []string{usersSearch.ID, organizationsSearch.ID} {
		f, core := cannedCore(t, searchPage("User", ""), true, discoveryConfig)

		// Without a sort, an order, or text matches the request is the one it always was.
		if _, err := invoke(t, core, tool, "open", `{"terms":"tom"}`, false); err != nil {
			t.Fatal(err)
		}
		request := lastRequest(t, f)
		values, _ := url.ParseQuery(request.query)
		if values.Has("sort") || values.Has("order") || request.accept != "application/vnd.github+json" {
			t.Errorf("%s plain request = %q accept %q", tool, request.query, request.accept)
		}

		first, err := invoke(t, core, tool, "open", `{"terms":"tom","sort":"followers","order":"asc","text_matches":true}`, false)
		if err != nil {
			t.Fatal(err)
		}
		request = lastRequest(t, f)
		values, _ = url.ParseQuery(request.query)
		if values.Get("sort") != "followers" || values.Get("order") != "asc" ||
			!strings.HasPrefix(values.Get("q"), "type:") || strings.Contains(values.Get("q"), "followers") ||
			request.accept != "application/vnd.github.text-match+json" {
			t.Errorf("%s filtered request = %q accept %q", tool, request.query, request.accept)
		}
		var page struct {
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
			t.Fatalf("%s first page = %s, %v", tool, first, err)
		}
		same := `{"terms":"tom","sort":"followers","order":"asc","text_matches":true,"cursor":"` + page.NextCursor + `"}`
		if _, err := invoke(t, core, tool, "open", same, false); err != nil {
			t.Errorf("%s continuation with the same filters = %v", tool, err)
		}
		for name, arguments := range map[string]string{
			"another sort":       `{"terms":"tom","sort":"joined","order":"asc","text_matches":true,"cursor":"%s"}`,
			"another order":      `{"terms":"tom","sort":"followers","order":"desc","text_matches":true,"cursor":"%s"}`,
			"no text matches":    `{"terms":"tom","sort":"followers","order":"asc","cursor":"%s"}`,
			"no filter at all":   `{"terms":"tom","cursor":"%s"}`,
			"an unsorted cursor": `{"terms":"tom","sort":"followers","cursor":"%s"}`,
		} {
			before := len(f.recorded())
			if _, err := invoke(t, core, tool, "open", fmt.Sprintf(arguments, page.NextCursor), false); !isInvalidRequest(err) {
				t.Errorf("%s with %s = %v, want a refused cursor", tool, name, err)
			}
			if len(f.recorded()) != before {
				t.Errorf("%s with %s reached GitHub", tool, name)
			}
		}

		// A cursor of an unfiltered request stays valid for an unfiltered continuation.
		plain, err := invoke(t, core, tool, "open", `{"terms":"tom"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(plain, &page); err != nil {
			t.Fatal(err)
		}
		if _, err := invoke(t, core, tool, "open", `{"terms":"tom","cursor":"`+page.NextCursor+`"}`, false); err != nil {
			t.Errorf("%s unfiltered continuation = %v", tool, err)
		}
		if _, err := invoke(t, core, tool, "open", `{"terms":"tom","sort":"stars"}`, false); err == nil {
			t.Errorf("%s accepted an unknown sort", tool)
		}
	}
}

// The binding of an unfiltered cursor is the one it had before the filters existed.
func TestUnfilteredBindingsAreUnchanged(t *testing.T) {
	a := &searchArguments{Terms: "tom"}
	if err := checkSearchArguments(usersSearch.ID, "type:user", false, a); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.binding, fingerprint("search", usersSearch.ID, "type:user", "tom")) {
		t.Error("the search binding changed")
	}
	o := &organizationArguments{Team: "design"}
	if err := checkTeamMembersListArguments(o, target{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(o.binding, fingerprint("teammembers", strings.ToLower(target{}.String()), "design")) {
		t.Error("the team members binding changed")
	}
	o = &organizationArguments{}
	if err := checkOrganizationTeamsListArguments(o, target{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(o.binding, fingerprint("teams", "organization", strings.ToLower(target{}.String()))) {
		t.Error("the teams binding changed")
	}
}

const collaboratorExample = `[{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","name":"Octo Cat",` +
	`"email":"octocat@example.invalid","user_view_type":"public",` +
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
	`"type":"User","site_admin":false,"permissions":{"pull":true,"triage":true,"push":true,"maintain":false,` +
	`"admin":false},"role_name":"write"}]`

func TestCollaboratorsAnswerEveryDocumentedMemberAndFilters(t *testing.T) {
	f, core := cannedCore(t, collaboratorExample, true, repositoriesConfig)
	result, err := invoke(t, core, collaboratorsList.ID, "repo", `{"permission":"triage_plus"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(result, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("%s, %v", result, err)
	}
	want := `{"collaborators":[{"login":"octocat","type":"User","role_name":"write",` +
		`"permissions":{"pull":true,"triage":true,"push":true,"maintain":false,"admin":false},"id":1,` +
		`"node_id":"MDQ6VXNlcjE=","name":"Octo Cat","email":"octocat@example.invalid","user_view_type":"public",` +
		`"avatar_url":"https://github.com/images/error/octocat_happy.gif","site_admin":false,` +
		`"url":"https://github.com/octocat","links":` + accountLinks + `}],"has_more":true,` +
		`"repository":"octo-org/example","next_cursor":"` + page.NextCursor + `"}`
	sameJSON(t, collaboratorsList.ID, result, want)
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Get("permission") != "triage_plus" {
		t.Errorf("query = %q, want permission=triage_plus", lastRequest(t, f).query)
	}
	if _, err := invoke(t, core, collaboratorsList.ID, "repo", `{"permission":"triage","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of another permission = %v, want it refused", err)
	}
}

func mustMap(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTeamMembersAnswerEveryDocumentedMemberAndFilter(t *testing.T) {
	body := `[` + strings.TrimSuffix(strings.TrimPrefix(collaboratorExample, "["), "]") + `]`
	body = strings.Replace(body, `"permissions":{"pull":true,"triage":true,"push":true,"maintain":false,"admin":false},"role_name":"write"`,
		`"starred_at":"2020-01-01T00:00:00Z","role":"maintainer","inherited":true`, 1)
	f, core := cannedCore(t, body, true, discoveryConfig)
	result, err := invoke(t, core, teamMembersList.ID, "org", `{"team":"design","role":"maintainer"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := mustMap(t, result)["next_cursor"].(string)
	sameJSON(t, teamMembersList.ID, result, `{"members":[{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=",`+
		`"type":"User","name":"Octo Cat","email":"octocat@example.invalid","user_view_type":"public",`+
		`"avatar_url":"https://github.com/images/error/octocat_happy.gif","site_admin":false,`+
		`"starred_at":"2020-01-01T00:00:00Z","role":"maintainer","inherited":true,`+
		`"url":"https://github.com/octocat","links":`+accountLinks+`}],"has_more":true,"owner":"orgs/octo-org",`+
		`"next_cursor":"`+cursor+`"}`)
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Get("role") != "maintainer" {
		t.Errorf("query = %q, want role=maintainer", lastRequest(t, f).query)
	}
	if _, err := invoke(t, core, teamMembersList.ID, "org", `{"team":"design","role":"maintainer","cursor":"`+cursor+`"}`, false); err != nil {
		t.Errorf("a continuation with the same role = %v", err)
	}
	for _, arguments := range []string{`{"team":"design","role":"all","cursor":"%s"}`, `{"team":"design","cursor":"%s"}`} {
		if _, err := invoke(t, core, teamMembersList.ID, "org", fmt.Sprintf(arguments, cursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", arguments, err)
		}
	}
	if _, err := invoke(t, core, teamMembersList.ID, "org", `{"team":"design","role":"owner"}`, false); err == nil {
		t.Error("an unknown role was accepted")
	}
}

const teamExample = `[{"id":1,"node_id":"MDQ6VGVhbTE=","url":"https://api.github.com/teams/1",` +
	`"html_url":"https://github.com/orgs/github/teams/justice-league","name":"Justice League",` +
	`"slug":"justice-league","description":"A great team.","privacy":"closed",` +
	`"notification_setting":"notifications_enabled","permission":"admin",` +
	`"permissions":{"pull":true,"triage":true,"push":true,"maintain":true,"admin":false},` +
	`"members_url":"https://api.github.com/teams/1/members{/member}",` +
	`"repositories_url":"https://api.github.com/teams/1/repos","type":"organization","access_source":"direct",` +
	`"organization_id":9,"enterprise_id":3,` +
	`"parent":{"id":2,"node_id":"MDQ6VGVhbTI=","url":"https://api.github.com/teams/2",` +
	`"html_url":"https://github.com/orgs/github/teams/league","name":"League","slug":"league",` +
	`"description":null,"privacy":"closed","notification_setting":"notifications_disabled",` +
	`"permission":"pull","members_url":"https://api.github.com/teams/2/members{/member}",` +
	`"repositories_url":"https://api.github.com/teams/2/repos","ldap_dn":"cn=league","type":"organization",` +
	`"organization_id":9,"enterprise_id":3}}]`

func TestTeamsAnswerEveryDocumentedMemberAndFilter(t *testing.T) {
	f, core := cannedCore(t, teamExample, true, discoveryConfig)
	result, err := invoke(t, core, organizationTeamsList.ID, "org", `{"team_type":"organization"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := mustMap(t, result)["next_cursor"].(string)
	sameJSON(t, organizationTeamsList.ID, result, `{"teams":[{"team":"justice-league","name":"Justice League",`+
		`"description":"A great team.","privacy":"closed","id":1,"node_id":"MDQ6VGVhbTE=","slug":"justice-league",`+
		`"notification_setting":"notifications_enabled","permission":"admin",`+
		`"permissions":{"pull":true,"triage":true,"push":true,"maintain":true,"admin":false},`+
		`"type":"organization","access_source":"direct","organization_id":9,"enterprise_id":3,`+
		`"parent":{"id":2,"node_id":"MDQ6VGVhbTI=","name":"League","slug":"league","privacy":"closed",`+
		`"notification_setting":"notifications_disabled","permission":"pull","type":"organization",`+
		`"organization_id":9,"enterprise_id":3,"ldap_dn":"cn=league","url":"https://github.com/orgs/github/teams/league",`+
		`"links":{"url":"https://api.github.com/teams/2","members_url":"https://api.github.com/teams/2/members{/member}",`+
		`"repositories_url":"https://api.github.com/teams/2/repos"}},`+
		`"url":"https://github.com/orgs/github/teams/justice-league",`+
		`"links":{"url":"https://api.github.com/teams/1","members_url":"https://api.github.com/teams/1/members{/member}",`+
		`"repositories_url":"https://api.github.com/teams/1/repos"}}],"has_more":true,"owner":"orgs/octo-org",`+
		`"next_cursor":"`+cursor+`"}`)
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Get("team_type") != "organization" {
		t.Errorf("query = %q, want team_type=organization", lastRequest(t, f).query)
	}
	if _, err := invoke(t, core, organizationTeamsList.ID, "org", `{"team_type":"organization","cursor":"`+cursor+`"}`, false); err != nil {
		t.Errorf("a continuation with the same team_type = %v", err)
	}
	for _, arguments := range []string{`{"team_type":"enterprise","cursor":"%s"}`, `{"cursor":"%s"}`} {
		if _, err := invoke(t, core, organizationTeamsList.ID, "org", fmt.Sprintf(arguments, cursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", arguments, err)
		}
	}
	if _, err := invoke(t, core, organizationTeamsList.ID, "org", `{"team_type":"x"}`, false); err == nil {
		t.Error("an unknown team_type was accepted")
	}
}

// Lists without the new members, and their members with control characters and excess length, answer as
// they always did.
func TestCollaboratorsTeamsAndMembersKeepTheirOldAnswer(t *testing.T) {
	_, core := cannedCore(t, `[{"login":"octocat","type":"User","role_name":"admin","gravatar_id":"",`+
		`"permissions":{"pull":true,"triage":false,"push":false,"maintain":false,"admin":false}}]`, false, repositoriesConfig)
	result, err := invoke(t, core, collaboratorsList.ID, "repo", `{}`, false)
	if err != nil || string(result) != `{"collaborators":[{"login":"octocat","permissions":{"admin":false,"maintain":false,`+
		`"pull":true,"push":false,"triage":false},"role_name":"admin","type":"User"}],"has_more":false,`+
		`"repository":"octo-org/example"}` {
		t.Fatalf("collaborators = %s, %v", result, err)
	}

	long := strings.Repeat("d", 5000)
	teams, _ := json.Marshal([]map[string]any{{"slug": "design", "name": "De\x07sign", "description": long, "privacy": "closed"}})
	_, core = cannedCore(t, string(teams), false, discoveryConfig)
	result, err = invoke(t, core, organizationTeamsList.ID, "org", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Teams []map[string]any `json:"teams"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Teams) != 1 {
		t.Fatalf("%s, %v", result, err)
	}
	if got.Teams[0]["name"] != "De\x07sign" || got.Teams[0]["description"] != long || got.Teams[0]["privacy"] != "closed" ||
		len(got.Teams[0]) != 5 {
		t.Errorf("team = %v, want the old members unchanged and only the slug added", got.Teams[0])
	}

	_, core = cannedCore(t, `[{"login":"octocat"}]`, false, discoveryConfig)
	result, err = invoke(t, core, teamMembersList.ID, "org", `{"team":"design"}`, false)
	if err != nil || string(result) != `{"has_more":false,"members":[{"login":"octocat"}],"owner":"orgs/octo-org"}` {
		t.Fatalf("members = %s, %v", result, err)
	}
}
