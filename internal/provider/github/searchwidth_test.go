package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

// The SSH address is assembled so the source holds nothing that reads as an e-mail address.
const documentedSSHURL = "git" + "@" + "github.com:octo-org/example.git"

// The repository members the search and the star list share, as GitHub documents them, with the clone token
// that must never leave.
const documentedSearchedRepository = `{"id":3081286,"node_id":"MDEwOlJlcG9zaXRvcnkzMDgxMjg2","name":"example",` +
	`"full_name":"octo-org/example","owner":` + documentedAccount + `,"private":false,` +
	`"html_url":"https://github.com/octo-org/example","description":"A C implementation of Tetris",` +
	`"fork":false,"url":"https://api.github.com/repos/octo-org/example",` +
	`"created_at":"2011-01-26T19:01:12Z","updated_at":"2011-01-26T19:14:43Z","pushed_at":"2011-01-26T19:06:43Z",` +
	`"homepage":"https://example.invalid","size":52,"stargazers_count":3,"watchers_count":4,"language":"Go",` +
	`"forks_count":5,"open_issues_count":6,"default_branch":"main","forks":5,"open_issues":6,"watchers":4,` +
	`"topics":["go","cli"],"has_issues":true,"has_projects":false,"has_pages":false,"has_wiki":true,` +
	`"has_discussions":true,"has_pull_requests":true,"pull_request_creation_policy":"all","archived":false,` +
	`"disabled":false,"visibility":"public","is_template":false,"web_commit_signoff_required":true,` +
	`"license":{"key":"mit","name":"MIT License","url":"https://api.github.com/licenses/mit","spdx_id":"MIT",` +
	`"node_id":"MDc6TGljZW5zZTEz","html_url":"https://opensource.org/licenses/MIT"},` +
	`"permissions":{"admin":false,"push":true,"pull":true},"temp_clone_token":"ABTLWHOULUVAXGTRYU7OC2876QJ2O",` +
	`"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":false,"allow_auto_merge":true,` +
	`"delete_branch_on_merge":true,"allow_forking":true,` +
	`"clone_url":"https://github.com/octo-org/example.git","ssh_url":"` + documentedSSHURL + `",` +
	`"forks_url":"https://api.github.com/repos/octo-org/example/forks"`

const searchedRepositoryTextMatch = `[{"object_url":"https://api.github.com/repositories/3081286",` +
	`"object_type":"Repository","property":"description","fragment":"A C implementation of Tetris",` +
	`"matches":[{"text":"Tetris","indices":[21,27]}]}]`

// The members of the repository beyond the compact ones, exactly as the search must report them.
func searchedRepositoryWant() map[string]string {
	return map[string]string{
		"id": `3081286`, "node_id": `"MDEwOlJlcG9zaXRvcnkzMDgxMjg2"`, "name": `"example"`, "owner": accountWant,
		"private": `false`, "homepage": `"https://example.invalid"`, "size": `52`, "watchers_count": `4`,
		"forks_count": `5`, "open_issues_count": `6`, "default_branch": `"main"`, "forks": `5`, "open_issues": `6`,
		"watchers": `4`, "topics": `["go","cli"]`, "has_issues": `true`, "has_projects": `false`,
		"has_pages": `false`, "has_wiki": `true`, "has_discussions": `true`, "has_pull_requests": `true`,
		"pull_request_creation_policy": `"all"`, "disabled": `false`, "is_template": `false`,
		"web_commit_signoff_required": `true`, "created_at": `"2011-01-26T19:01:12Z"`,
		"pushed_at": `"2011-01-26T19:06:43Z"`, "allow_merge_commit": `true`, "allow_squash_merge": `true`,
		"allow_rebase_merge": `false`, "allow_auto_merge": `true`, "delete_branch_on_merge": `true`,
		"allow_forking": `true`,
		"license": `{"key":"mit","name":"MIT License","node_id":"MDc6TGljZW5zZTEz",` +
			`"url":"https://opensource.org/licenses/MIT","links":{"url":"https://api.github.com/licenses/mit"},` +
			`"spdx_id":"MIT"}`,
		"permissions": `{"admin":false,"push":true,"pull":true}`,
		"links": `{"url":"https://api.github.com/repos/octo-org/example",` +
			`"forks_url":"https://api.github.com/repos/octo-org/example/forks",` +
			`"clone_url":"https://github.com/octo-org/example.git",` +
			`"ssh_url":"` + documentedSSHURL + `"}`,
	}
}

func searchedRepositoryItem(extra string) string {
	return `{"total_count":1,"incomplete_results":false,"items":[` + documentedSearchedRepository + extra + `}]}`
}

func noRepositoryMember(t *testing.T, name string, got json.RawMessage) {
	t.Helper()
	for _, secret := range []string{"temp_clone_token", "ABTLWHOULUVAXGTRYU7OC2876QJ2O"} {
		if strings.Contains(string(got), secret) {
			t.Errorf("%s carries %s: %s", name, secret, got)
		}
	}
}

// github.repositories.search reports every member of the repository and the search, keeps the compact ones,
// and asks for a sort, an order, and text matches only when told to.
func TestRepositorySearchReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, searchedRepositoryItem(`,"score":1.5,"text_matches":`+searchedRepositoryTextMatch),
		true, discoveryConfig)
	plain, err := invoke(t, core, repositoriesSearch.ID, "repo", `{"terms":"tetris"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ := url.ParseQuery(request.query)
	if request.accept != "application/vnd.github+json" || values.Has("sort") || values.Has("order") {
		t.Errorf("plain request = %+v", request)
	}
	noRepositoryMember(t, "search", plain)
	object := members(t, plain, "repositories", "#0")
	wantMembers(t, "search", object, searchedRepositoryWant())
	wantMembers(t, "search", object, map[string]string{
		"repository": `"octo-org/example"`, "description": `"A C implementation of Tetris"`,
		"visibility": `"public"`, "language": `"Go"`, "stars": `3`, "fork": `false`, "archived": `false`,
		"updated_at": `"2011-01-26T19:14:43Z"`, "url": `"https://github.com/octo-org/example"`, "score": `1.5`,
	})
	for _, name := range []string{"full_name", "stargazers_count"} {
		if _, ok := object[name]; ok {
			t.Errorf("the search repeats %s", name)
		}
	}

	first, err := invoke(t, core, repositoriesSearch.ID, "repo",
		`{"terms":"tetris","text_matches":true,"sort":"help-wanted-issues","order":"asc"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request = lastRequest(t, f)
	values, _ = url.ParseQuery(request.query)
	if request.accept != "application/vnd.github.text-match+json" || values.Get("sort") != "help-wanted-issues" ||
		values.Get("order") != "asc" || strings.Contains(values.Get("q"), "help-wanted") {
		t.Errorf("request = %+v", request)
	}
	sameJSON(t, "text_matches", members(t, first, "repositories", "#0")["text_matches"],
		`[{"object_type":"Repository","property":"description","fragment":"A C implementation of Tetris",`+
			`"matches":[{"text":"Tetris","indices":[21,27]}],`+
			`"links":{"object_url":"https://api.github.com/repositories/3081286"}}]`)
	for _, sort := range []string{"stars", "forks", "help-wanted-issues", "updated"} {
		if _, err := invoke(t, core, repositoriesSearch.ID, "repo", `{"terms":"tetris","sort":"`+sort+`"}`, false); err != nil {
			t.Errorf("sort %s = %v", sort, err)
		}
	}
	checkFilterCursor(t, f, core, repositoriesSearch.ID, "repo", first,
		`{"terms":"tetris","text_matches":true,"sort":"help-wanted-issues","order":"asc","cursor":%q}`,
		map[string]string{
			"another sort": `{"terms":"tetris","text_matches":true,"sort":"stars","order":"asc","cursor":%q}`,
			"no order":     `{"terms":"tetris","text_matches":true,"sort":"help-wanted-issues","cursor":%q}`,
			"no matches":   `{"terms":"tetris","sort":"help-wanted-issues","order":"asc","cursor":%q}`,
			"no filters":   `{"terms":"tetris","cursor":%q}`,
		}, `{"terms":"tetris","sort":"closed"}`, `{"terms":"tetris","order":"up"}`)
}

// checkFilterCursor checks that the cursor of first continues with the filters it was issued for, that every
// other filter set is refused before GitHub is reached, and that every bad request is refused.
func checkFilterCursor(t *testing.T, f *fakeGitHub, core *application.Core, tool, connection string,
	first json.RawMessage, same string, others map[string]string, bad ...string) {
	t.Helper()
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	if _, err := invoke(t, core, tool, connection, fmt.Sprintf(same, page.NextCursor), false); err != nil {
		t.Errorf("continuation with the same filters = %v", err)
	}
	before := len(f.recorded())
	for name, other := range others {
		if _, err := invoke(t, core, tool, connection, fmt.Sprintf(other, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
	}
	for _, arguments := range bad {
		if _, err := invoke(t, core, tool, connection, arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused request reached GitHub")
	}
}

// A repository search match outside the connection's targets keeps only its identifiers, and a match
// without the new members answers what it always did.
func TestRepositorySearchOutsideTheTargetsAndWithoutNewMembers(t *testing.T) {
	_, core := cannedCore(t, `{"total_count":1,"incomplete_results":false,"items":[`+
		`{"id":7,"node_id":"MDEwOlJlcG9zaXRvcnk3","full_name":"hubot/other","name":"other","private":false,`+
		`"owner":`+documentedAccount+`,"html_url":"https://github.com/hubot/other","stargazers_count":2,`+
		`"temp_clone_token":"SECRET","visibility":"public"}]}`, false, discoveryConfig)
	got, err := invoke(t, core, repositoriesSearch.ID, "repo", `{"terms":"other"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	noRepositoryMember(t, "outside", got)
	if strings.Contains(string(got), `"owner"`) {
		t.Errorf("a repository outside the targets carries its owner: %s", got)
	}
	wantMembers(t, "outside", members(t, got, "repositories", "#0"), map[string]string{
		"id": `7`, "node_id": `"MDEwOlJlcG9zaXRvcnk3"`, "repository": `"hubot/other"`, "stars": `2`,
	})

	const minimal = `{"total_count":1,"incomplete_results":false,"items":[{"full_name":"octo-org/example",` +
		`"description":"An example","visibility":"public","language":"Go","stargazers_count":3,"fork":false,` +
		`"archived":false,"updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/octo-org/example"}]}`
	_, core = cannedCore(t, minimal, false, discoveryConfig)
	got, err = invoke(t, core, repositoriesSearch.ID, "repo", `{"terms":"example"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "search", got, `{"repositories":[{"repository":"octo-org/example","description":"An example",`+
		`"visibility":"public","language":"Go","stars":3,"fork":false,"archived":false,`+
		`"updated_at":"2026-01-01T00:00:00Z","url":"https://github.com/octo-org/example"}],`+
		`"total_count":1,"incomplete_results":false,"has_more":false}`)
}

// The code search item documented by GitHub's REST reference, with the text match example.
const documentedCodeSearchPage = `{"total_count":1,"incomplete_results":false,"items":[` +
	`{"name":"classes.js","path":"src/attributes/classes.js","sha":"d7212f9dee2dcc18f084d7df8f417b80846ded5a",` +
	`"url":"https://api.github.com/repositories/99/contents/src/attributes/classes.js?ref=41a8",` +
	`"git_url":"https://api.github.com/repositories/99/git/blobs/d7212f9d",` +
	`"html_url":"https://github.com/octo-org/example/blob/41a8/src/attributes/classes.js",` +
	`"repository":` + pullBaseRepository + `,"score":1.5,"file_size":4096,"language":"JavaScript",` +
	`"last_modified_at":"2014-07-25T09:49:01Z","line_numbers":["73..77","77..78"],` +
	`"text_matches":[{"object_url":"https://api.github.com/repositories/99/contents/src/attributes/classes.js?ref=41a8",` +
	`"object_type":"FileContent","property":"content","fragment":"addClass: function( value ) {",` +
	`"matches":[{"text":"addClass","indices":[0,8]}]}]}]}`

// github.code.search reports every documented member, keeps the compact ones, asks for text matches only when
// told to, and takes no sort or order.
func TestCodeSearchReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedCodeSearchPage, true, discoveryConfig)
	plain, err := invoke(t, core, codeSearch.ID, "repo", `{"terms":"addClass"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if request := lastRequest(t, f); request.accept != "application/vnd.github+json" {
		t.Errorf("plain request = %+v", request)
	}
	noRepositoryMember(t, "code search", plain)
	wantMembers(t, "code search", members(t, plain, "files", "#0"), map[string]string{
		"path": `"src/attributes/classes.js"`, "repository": `"octo-org/example"`,
		"sha":  `"d7212f9dee2dcc18f084d7df8f417b80846ded5a"`,
		"url":  `"https://github.com/octo-org/example/blob/41a8/src/attributes/classes.js"`,
		"name": `"classes.js"`, "score": `1.5`, "file_size": `4096`, "language": `"JavaScript"`,
		"last_modified_at": `"2014-07-25T09:49:01Z"`, "line_numbers": `["73..77","77..78"]`,
		"links": `{"url":"https://api.github.com/repositories/99/contents/src/attributes/classes.js?ref=41a8",` +
			`"git_url":"https://api.github.com/repositories/99/git/blobs/d7212f9d"}`,
		"repository_detail": pullBaseRepositoryWant,
	})

	first, err := invoke(t, core, codeSearch.ID, "repo", `{"terms":"addClass","text_matches":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ := url.ParseQuery(request.query)
	if request.accept != "application/vnd.github.text-match+json" || values.Has("sort") || values.Has("order") {
		t.Errorf("request = %+v", request)
	}
	sameJSON(t, "text_matches", members(t, first, "files", "#0")["text_matches"],
		`[{"object_type":"FileContent","property":"content","fragment":"addClass: function( value ) {",`+
			`"matches":[{"text":"addClass","indices":[0,8]}],"links":{"object_url":`+
			`"https://api.github.com/repositories/99/contents/src/attributes/classes.js?ref=41a8"}}]`)
	checkFilterCursor(t, f, core, codeSearch.ID, "repo", first,
		`{"terms":"addClass","text_matches":true,"cursor":%q}`,
		map[string]string{"no matches": `{"terms":"addClass","cursor":%q}`},
		`{"terms":"addClass","sort":"indexed"}`, `{"terms":"addClass","order":"asc"}`)
}

// A code match without the new members answers what it always did.
func TestCodeSearchWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	const minimal = `{"total_count":1,"incomplete_results":false,"items":[{"path":"main.go","sha":"deadbeef",` +
		`"html_url":"https://github.com/octo-org/example/blob/main/main.go",` +
		`"repository":{"full_name":"octo-org/example"}}]}`
	_, core := cannedCore(t, minimal, false, discoveryConfig)
	got, err := invoke(t, core, codeSearch.ID, "repo", `{"terms":"main"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "code search", got, `{"files":[{"path":"main.go","repository":"octo-org/example","sha":"deadbeef",`+
		`"url":"https://github.com/octo-org/example/blob/main/main.go","repository_detail":{"full_name":"octo-org/example"}}],`+
		`"total_count":1,"incomplete_results":false,"has_more":false}`)
}

// github.stars.list asks for the star media type, reports the star time and the repository in full, and binds
// the cursor to its filters only while they are set.
func TestStarsListReportsEveryDocumentedMember(t *testing.T) {
	star := `[{"starred_at":"2011-01-16T19:06:43Z","repo":` + documentedSearchedRepository + `}}]`
	f, core := cannedCore(t, star, true, discoveryConfig)
	plain, err := invoke(t, core, starsList.ID, "open", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ := url.ParseQuery(request.query)
	if request.accept != "application/vnd.github.star+json" || values.Has("sort") || values.Has("direction") {
		t.Errorf("plain request = %+v", request)
	}
	noRepositoryMember(t, "stars", plain)
	object := members(t, plain, "starred", "#0")
	wantMembers(t, "stars", object, map[string]string{
		"repository": `"octo-org/example"`, "visibility": `"public"`, "archived": `false`,
		"starred_at": `"2011-01-16T19:06:43Z"`,
	})
	detail := members(t, plain, "starred", "#0", "repository_detail")
	wantMembers(t, "stars repository_detail", detail, searchedRepositoryWant())
	wantMembers(t, "stars repository_detail", detail, map[string]string{
		"full_name": `"octo-org/example"`, "stargazers_count": `3`, "url": `"https://github.com/octo-org/example"`,
		"description": `"A C implementation of Tetris"`, "language": `"Go"`, "visibility": `"public"`,
		"archived": `false`, "fork": `false`, "updated_at": `"2011-01-26T19:14:43Z"`,
	})

	first, err := invoke(t, core, starsList.ID, "open", `{"sort":"updated","direction":"asc"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	values, _ = url.ParseQuery(lastRequest(t, f).query)
	if values.Get("sort") != "updated" || values.Get("direction") != "asc" {
		t.Errorf("query = %q, want the sort and the direction", lastRequest(t, f).query)
	}
	checkFilterCursor(t, f, core, starsList.ID, "open", first,
		`{"sort":"updated","direction":"asc","cursor":%q}`,
		map[string]string{
			"another sort":      `{"sort":"created","direction":"asc","cursor":%q}`,
			"another direction": `{"sort":"updated","direction":"desc","cursor":%q}`,
			"no direction":      `{"sort":"updated","cursor":%q}`,
			"no filters":        `{"cursor":%q}`,
		}, `{"sort":"stars"}`, `{"direction":"up"}`)

	// A cursor issued without filters stays valid without them and is refused with one.
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, starsList.ID, "open", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	if _, err := invoke(t, core, starsList.ID, "open", `{"sort":"created","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a plain cursor with a sort = %v, want a refused cursor", err)
	}
}

// A starred repository without the new members answers what it always did, and one of an owner the targets
// do not name is not listed.
func TestStarsListWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	_, core := cannedCore(t, `[{"repo":{"full_name":"octo-org/example","visibility":"public","archived":true,`+
		`"owner":{"login":"octo-org"}}},{"repo":{"full_name":"hubot/other","visibility":"public","owner":{"login":"hubot"}}}]`,
		false, discoveryConfig)
	got, err := invoke(t, core, starsList.ID, "org", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "stars", got, `{"starred":[{"repository":"octo-org/example","visibility":"public","archived":true,`+
		`"repository_detail":{"full_name":"octo-org/example","owner":{"login":"octo-org"},"visibility":"public","archived":true}}],`+
		`"has_more":false}`)
}
