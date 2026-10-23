package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The pull request documented by GitHub's REST reference for "Get a pull request", its accounts shortened to
// documentedAccount and its repositories to the members the projection reads; auto_merge and stack, which the
// example leaves out, are filled with the documented shapes. The head repository is a fork outside the
// connections' targets.
const (
	pullHeadRepository = `{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","name":"Hello-World",` +
		`"full_name":"octocat/Hello-World","owner":` + documentedAccount + `,"private":false,` +
		`"html_url":"https://github.com/octocat/Hello-World","url":"https://api.github.com/repos/octocat/Hello-World",` +
		`"temp_clone_token":"ABTLWHOULUVAXGTRYU7OC2876QJ2O"}`
	pullBaseRepository = `{"id":99,"node_id":"MDEwOlJlcG9zaXRvcnk5OQ==","name":"example","full_name":"octo-org/example",` +
		`"owner":` + documentedAccount + `,"private":true,"html_url":"https://github.com/octo-org/example",` +
		`"url":"https://api.github.com/repos/octo-org/example","temp_clone_token":"SECRET"}`
	pullTeam = `{"id":1,"node_id":"MDQ6VGVhbTE=","url":"https://api.github.com/teams/1",` +
		`"html_url":"https://github.com/orgs/github/teams/justice-league","name":"Justice League",` +
		`"slug":"justice-league","description":"A great team.","privacy":"closed",` +
		`"notification_setting":"notifications_enabled","permission":"admin","type":"enterprise",` +
		`"ldap_dn":"cn=jl","organization_id":4,"enterprise_id":5,` +
		`"members_url":"https://api.github.com/teams/1/members{/member}",` +
		`"repositories_url":"https://api.github.com/teams/1/repos"}`
	pullBaseObject = `"url":"https://api.github.com/repos/octo-org/example/pulls/42","id":1,` +
		`"node_id":"MDExOlB1bGxSZXF1ZXN0MQ==","html_url":"https://github.com/octo-org/example/pull/42",` +
		`"diff_url":"https://github.com/octo-org/example/pull/42.diff",` +
		`"patch_url":"https://github.com/octo-org/example/pull/42.patch",` +
		`"issue_url":"https://api.github.com/repos/octo-org/example/issues/42",` +
		`"commits_url":"https://api.github.com/repos/octo-org/example/pulls/42/commits",` +
		`"review_comments_url":"https://api.github.com/repos/octo-org/example/pulls/42/comments",` +
		`"review_comment_url":"https://api.github.com/repos/octo-org/example/pulls/comments{/number}",` +
		`"comments_url":"https://api.github.com/repos/octo-org/example/issues/42/comments",` +
		`"statuses_url":"https://api.github.com/repos/octo-org/example/statuses/6dcb09b5",` +
		`"number":42,"state":"open","locked":true,"title":"Amazing new feature","user":` + documentedAccount + `,` +
		`"body":"Please pull these awesome changes in!","labels":[` + documentedIssueLabel + `],` +
		`"milestone":` + documentedMilestone + `,"active_lock_reason":"too heated",` +
		`"created_at":"2011-01-26T19:01:12Z","updated_at":"2011-01-27T19:01:12Z","closed_at":"2011-01-28T19:01:12Z",` +
		`"merged_at":"2011-01-29T19:01:12Z","merge_commit_sha":"e5bd3914e2e596debea16f433f57875b5b90bcd6",` +
		`"assignee":` + documentedAccount + `,"assignees":[` + documentedAccount + `],` +
		`"requested_reviewers":[` + documentedAccount + `],"requested_teams":[` + pullTeam + `],` +
		`"head":{"label":"octocat:new-topic","ref":"new-topic","sha":"6dcb09b5","user":` + documentedAccount +
		`,"repo":` + pullHeadRepository + `},` +
		`"base":{"label":"octo-org:master","ref":"master","sha":"b0b0b0b0","user":` + documentedAccount +
		`,"repo":` + pullBaseRepository + `},` +
		`"_links":{"self":{"href":"https://api.github.com/repos/octo-org/example/pulls/42"}},` +
		`"author_association":"OWNER","auto_merge":{"enabled_by":` + documentedAccount + `,"merge_method":"squash",` +
		`"commit_title":"Add the feature","commit_message":"Squashed"},` +
		`"stack":{"base":{"ref":"main","sha":"a1a1a1a1"},"size":3,"position":2,"id":7,"number":42},"draft":false`
	documentedPull = `{` + pullBaseObject + `,"merged":false,"mergeable":true,"rebaseable":true,` +
		`"mergeable_state":"clean","merged_by":` + documentedAccount + `,"comments":10,"review_comments":2,` +
		`"maintainer_can_modify":true,"commits":3,"additions":100,"deletions":3,"changed_files":5}`
)

// members decodes an answer and returns the member at path, as compact JSON; "[0]" steps into a list.
func members(t *testing.T, got json.RawMessage, path ...string) map[string]json.RawMessage {
	t.Helper()
	var current json.RawMessage = got
	for _, step := range path {
		if index, ok := strings.CutPrefix(step, "#"); ok {
			var list []json.RawMessage
			if err := json.Unmarshal(current, &list); err != nil || len(list) == 0 {
				t.Fatalf("%s: %s, %v", step, current, err)
			}
			position, _ := strconv.Atoi(index)
			current = list[position]
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			t.Fatalf("%s: %s, %v", step, current, err)
		}
		current = object[step]
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(current, &object); err != nil {
		t.Fatalf("answer %s: %v", current, err)
	}
	return object
}

// wantMembers compares the named members of an answer by value.
func wantMembers(t *testing.T, name string, object map[string]json.RawMessage, want map[string]string) {
	t.Helper()
	for key, value := range want {
		sameJSON(t, name+"."+key, object[key], value)
	}
}

// The members the width adds are the same in all three pull request reads.
func pullWidthWant(repository string) map[string]string {
	return map[string]string{
		"id": `1`, "node_id": `"MDExOlB1bGxSZXF1ZXN0MQ=="`, "locked": `true`, "active_lock_reason": `"too heated"`,
		"author_detail": accountWant, "author_association": `"OWNER"`, "milestone": `"v1.0"`,
		"milestone_detail": milestoneWant, "assignee": accountWant, "assignees": `["octocat"]`,
		"assignees_details": `[` + accountWant + `]`, "requested_reviewers_details": `[` + accountWant + `]`,
		"labels_details": `[{"color":"f29513","default":true,"description":"Something isn't working",` +
			`"id":208045946,"links":{"url":"https://api.github.com/repos/octocat/Hello-World/labels/bug"},` +
			`"name":"bug","node_id":"MDU6TGFiZWwyMDgwNDU5NDY="}]`,
		"requested_teams": `[{"description":"A great team.","enterprise_id":5,"id":1,` +
			`"ldap_dn":"cn=jl","links":{"members_url":"https://api.github.com/teams/1/members{/member}",` +
			`"repositories_url":"https://api.github.com/teams/1/repos","url":"https://api.github.com/teams/1"},` +
			`"name":"Justice League","node_id":"MDQ6VGVhbTE=","notification_setting":"notifications_enabled",` +
			`"organization_id":4,"permission":"admin","privacy":"closed","slug":"justice-league","type":"enterprise",` +
			`"url":"https://github.com/orgs/github/teams/justice-league"}]`,
		"head_detail": `{"label":"octocat:new-topic","ref":"new-topic","sha":"6dcb09b5","user":` + accountWant +
			`,"repo":` + repository + `}`,
		"auto_merge": `{"commit_message":"Squashed","commit_title":"Add the feature","enabled_by":` + accountWant +
			`,"merge_method":"squash"}`,
		"stack": `{"base":{"ref":"main","sha":"a1a1a1a1"},"id":7,"number":42,"position":2,"size":3}`,
		"links": `{"url":"https://api.github.com/repos/octo-org/example/pulls/42",` +
			`"diff_url":"https://github.com/octo-org/example/pull/42.diff",` +
			`"patch_url":"https://github.com/octo-org/example/pull/42.patch",` +
			`"issue_url":"https://api.github.com/repos/octo-org/example/issues/42",` +
			`"commits_url":"https://api.github.com/repos/octo-org/example/pulls/42/commits",` +
			`"review_comments_url":"https://api.github.com/repos/octo-org/example/pulls/42/comments",` +
			`"review_comment_url":"https://api.github.com/repos/octo-org/example/pulls/comments{/number}",` +
			`"comments_url":"https://api.github.com/repos/octo-org/example/issues/42/comments",` +
			`"statuses_url":"https://api.github.com/repos/octo-org/example/statuses/6dcb09b5"}`,
	}
}

const pullBaseRepositoryWant = `{"full_name":"octo-org/example","id":99,` +
	`"links":{"url":"https://api.github.com/repos/octo-org/example"},"name":"example",` +
	`"node_id":"MDEwOlJlcG9zaXRvcnk5OQ==","owner":` + accountWant + `,"private":true,` +
	`"url":"https://github.com/octo-org/example"}`

const (
	pullHeadFull = `{"full_name":"octocat/Hello-World","id":1296269,` +
		`"links":{"url":"https://api.github.com/repos/octocat/Hello-World"},"name":"Hello-World",` +
		`"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","owner":` + accountWant + `,"private":false,` +
		`"url":"https://github.com/octocat/Hello-World"}`
	pullHeadForeign = `{"full_name":"octocat/Hello-World","id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5"}`
)

// github.pull-requests.get reports every member GitHub documents for a pull request, keeps the flat members
// it answered before, and shows a fork outside the targets with its identifiers only.
func TestPullRequestGetReportsEveryDocumentedMember(t *testing.T) {
	for _, c := range []struct{ connection, head string }{{"open", pullHeadFull}, {"repo", pullHeadForeign}} {
		_, core := cannedCore(t, documentedPull, false, discoveryConfig)
		arguments := `{"number":42}`
		if c.connection == "open" {
			arguments = `{"repository":"octo-org/example","number":42}`
		}
		got, err := invoke(t, core, pullsGet.ID, c.connection, arguments, false)
		if err != nil {
			t.Fatalf("%s: %v", c.connection, err)
		}
		if strings.Contains(string(got), "ABTLWHOULUVAXGTRYU7OC2876QJ2O") || strings.Contains(string(got), "SECRET") ||
			strings.Contains(string(got), "_links") {
			t.Errorf("%s: the answer holds a withheld value: %s", c.connection, got)
		}
		object := members(t, got)
		want := pullWidthWant(c.head)
		want["head_detail"] = `{"label":"octocat:new-topic","ref":"new-topic","sha":"6dcb09b5","user":` + accountWant +
			`,"repo":` + c.head + `}`
		wantMembers(t, c.connection, object, want)
		wantMembers(t, c.connection, object, map[string]string{
			"base_detail": `{"label":"octo-org:master","ref":"master","sha":"b0b0b0b0","user":` + accountWant +
				`,"repo":` + pullBaseRepositoryWant + `}`,
			"rebaseable": `true`, "merged_by": accountWant, "comments": `10`, "review_comments": `2`,
			"maintainer_can_modify": `true`, "additions": `100`, "deletions": `3`,
			// The members the answer held before.
			"number": `42`, "title": `"Amazing new feature"`, "author": `"octocat"`, "labels": `["bug"]`,
			"head_branch": `"new-topic"`, "head_sha": `"6dcb09b5"`, "base_branch": `"master"`, "base_sha": `"b0b0b0b0"`,
			"requested_reviewers": `["octocat"]`, "commits": `3`, "changed_files": `5`, "mergeable": `true`,
			"url": `"https://github.com/octo-org/example/pull/42"`,
		})
	}
}

// A pull request without the new members answers what it always did.
func TestPullRequestReadsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const minimal = `{"number":42,"title":"T","state":"open","draft":false,"merged":false,` +
		`"html_url":"https://github.com/octo-org/example/pull/42","created_at":"2026-01-01T00:00:00Z"}`
	_, core := cannedCore(t, minimal, false, discoveryConfig)
	got, err := invoke(t, core, pullsGet.ID, "repo", `{"number":42}`, false)
	const wantGet = `{"body":"","changed_files":0,"commits":0,"created_at":"2026-01-01T00:00:00Z","draft":false,` +
		`"merged":false,"number":42,"repository":"octo-org/example","state":"open","title":"T",` +
		`"url":"https://github.com/octo-org/example/pull/42"}`
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "get", got, wantGet)

	_, core = cannedCore(t, `[`+minimal+`]`, false, discoveryConfig)
	got, err = invoke(t, core, pullsList.ID, "repo", `{}`, false)
	const wantList = `{"has_more":false,"pull_requests":[{"created_at":"2026-01-01T00:00:00Z","draft":false,` +
		`"merged":false,"number":42,"state":"open","title":"T",` +
		`"url":"https://github.com/octo-org/example/pull/42"}],"repository":"octo-org/example"}`
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "list", got, wantList)
}

// github.pull-requests.list reports the members of GitHub's pull-request-simple object, cuts the body, and
// leaves out the members only the single pull request carries.
func TestPullRequestListReportsEveryDocumentedMember(t *testing.T) {
	long := strings.Repeat("b", projectionBodyLimit+10)
	item := strings.Replace(documentedPull, `"body":"Please pull these awesome changes in!"`, `"body":"`+long+`"`, 1)
	_, core := cannedCore(t, `[`+item+`]`, false, discoveryConfig)
	got, err := invoke(t, core, pullsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	object := members(t, got, "pull_requests", "#0")
	wantMembers(t, "list", object, pullWidthWant(pullHeadForeign))
	wantMembers(t, "list", object, map[string]string{
		"body": `"` + long[:projectionBodyLimit] + `"`, "body_truncated": `true`,
		"closed_at": `"2011-01-28T19:01:12Z"`, "merged_at": `"2011-01-29T19:01:12Z"`,
		"merge_commit_sha": `"e5bd3914e2e596debea16f433f57875b5b90bcd6"`, "base_sha": `"b0b0b0b0"`,
		"requested_reviewers": `["octocat"]`, "author": `"octocat"`, "labels": `["bug"]`, "number": `42`,
	})
	for _, only := range []string{"rebaseable", "merged_by", "comments", "review_comments", "maintainer_can_modify",
		"additions", "deletions"} {
		if _, ok := object[only]; ok {
			t.Errorf("a list entry holds %s, which only the single pull request carries", only)
		}
	}
}

// github.pull-requests.list sends sort and direction as their own query arguments and binds the cursor to
// them; without them the request and the cursor are as before.
func TestPullRequestListSortReachesTheRequestAndTheCursor(t *testing.T) {
	f, core := cannedCore(t, `[`+documentedPull+`]`, true, discoveryConfig)
	plain, err := invoke(t, core, pullsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := url.ParseQuery(lastRequest(t, f).query)
	if values.Has("sort") || values.Has("direction") {
		t.Errorf("plain request carries a sort: %q", lastRequest(t, f).query)
	}
	const sorted = `{"sort":"long-running","direction":"asc"%s}`
	first, err := invoke(t, core, pullsList.ID, "repo", fmt.Sprintf(sorted, ""), false)
	if err != nil {
		t.Fatal(err)
	}
	values, _ = url.ParseQuery(lastRequest(t, f).query)
	if values.Get("sort") != "long-running" || values.Get("direction") != "asc" {
		t.Errorf("sorted request = %q", lastRequest(t, f).query)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	if _, err := invoke(t, core, pullsList.ID, "repo", fmt.Sprintf(sorted, `,"cursor":"`+page.NextCursor+`"`), false); err != nil {
		t.Errorf("continuation with the same sort = %v", err)
	}
	for name, arguments := range map[string]string{
		"another sort":      `{"sort":"created","direction":"asc","cursor":"%s"}`,
		"another direction": `{"sort":"long-running","direction":"desc","cursor":"%s"}`,
		"no direction":      `{"sort":"long-running","cursor":"%s"}`,
		"no sort":           `{"cursor":"%s"}`,
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, pullsList.ID, "repo", fmt.Sprintf(arguments, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s reached GitHub", name)
		}
	}
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, pullsList.ID, "repo", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	for _, arguments := range []string{`{"sort":"stars"}`, `{"direction":"up"}`} {
		before := len(f.recorded())
		if _, err := invoke(t, core, pullsList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s reached GitHub", arguments)
		}
	}
}

const documentedPullSearchPage = `{"total_count":1,"incomplete_results":false,"items":[` +
	`{"url":"https://api.github.com/repos/octo-org/example/issues/42",` +
	`"repository_url":"https://api.github.com/repos/octo-org/example",` +
	`"html_url":"https://github.com/octo-org/example/pull/42","id":35802,"node_id":"MDExOlB1bGxSZXF1ZXN0MQ==",` +
	`"number":42,"title":"Amazing new feature","user":` + documentedAccount + `,"labels":[],"state":"open",` +
	`"locked":false,"comments":3,"created_at":"2011-01-26T19:01:12Z","updated_at":"2011-01-27T19:01:12Z",` +
	`"draft":true,"repository":` + pullBaseRepository + `,"pull_request":{"merged_at":"2011-01-29T19:01:12Z",` +
	`"diff_url":"https://github.com/octo-org/example/pull/42.diff",` +
	`"html_url":"https://github.com/octo-org/example/pull/42",` +
	`"patch_url":"https://github.com/octo-org/example/pull/42.patch",` +
	`"url":"https://api.github.com/repos/octo-org/example/pulls/42"},"body":"Please pull","score":1.5,` +
	`"author_association":"OWNER","text_matches":[{"object_url":"https://api.github.com/repositories/99/issues/42",` +
	`"object_type":"Issue","property":"body","fragment":"Please pull","matches":[{"text":"pull","indices":[7,11]}]}]}]}`

// github.pull-requests.search reports the issue search's members for a match, the merge time and the pull
// request's addresses, and asks GitHub for text matches only when told to.
func TestPullRequestSearchReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedPullSearchPage, false, discoveryConfig)
	got, err := invoke(t, core, pullRequestsSearch.ID, "org", `{"terms":"feature"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if accept := lastRequest(t, f).accept; accept != "application/vnd.github+json" {
		t.Errorf("accept = %q", accept)
	}
	object := members(t, got, "pull_requests", "#0")
	wantMembers(t, "search", object, map[string]string{
		"number": `42`, "draft": `true`, "id": `35802`, "node_id": `"MDExOlB1bGxSZXF1ZXN0MQ=="`,
		"merged_at": `"2011-01-29T19:01:12Z"`, "comments": `3`, "body": `"Please pull"`, "score": `1.5`,
		"user": accountWant, "author_association": `"OWNER"`,
		"links": `{"url":"https://api.github.com/repos/octo-org/example/issues/42",` +
			`"repository_url":"https://api.github.com/repos/octo-org/example",` +
			`"diff_url":"https://github.com/octo-org/example/pull/42.diff",` +
			`"patch_url":"https://github.com/octo-org/example/pull/42.patch"}`,
		"repository_detail": pullBaseRepositoryWant,
	})

	got, err = invoke(t, core, pullRequestsSearch.ID, "org", `{"terms":"feature","text_matches":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if accept := lastRequest(t, f).accept; accept != "application/vnd.github.text-match+json" {
		t.Errorf("accept = %q", accept)
	}
	wantMembers(t, "search", members(t, got, "pull_requests", "#0"), map[string]string{
		"text_matches": `[{"fragment":"Please pull","links":{"object_url":"https://api.github.com/repositories/99/issues/42"},` +
			`"matches":[{"indices":[7,11],"text":"pull"}],"object_type":"Issue","property":"body"}]`,
	})
}

// github.pull-requests.search sends its filters as their own query arguments beside the unchanged q and
// binds the cursor to them; its forced qualifiers still bound every match.
func TestPullRequestSearchFiltersReachTheRequestAndTheCursor(t *testing.T) {
	f, core := cannedCore(t, `{"total_count":1,"incomplete_results":false,"items":[]}`, true, discoveryConfig)
	plain, err := invoke(t, core, pullRequestsSearch.ID, "org", `{"terms":"crash"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := url.ParseQuery(lastRequest(t, f).query)
	for _, key := range []string{"sort", "order", "advanced_search", "search_type"} {
		if values.Has(key) {
			t.Errorf("plain request carries %s", key)
		}
	}
	if values.Get("q") != "is:pr org:octo-org crash" {
		t.Errorf("q = %q", values.Get("q"))
	}
	const filtered = `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":true,` +
		`"search_type":"hybrid","text_matches":true%s}`
	first, err := invoke(t, core, pullRequestsSearch.ID, "org", fmt.Sprintf(filtered, ""), false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ = url.ParseQuery(request.query)
	if values.Get("q") != "is:pr org:octo-org crash" || values.Get("sort") != "reactions-+1" ||
		values.Get("order") != "asc" || values.Get("advanced_search") != "true" ||
		values.Get("search_type") != "hybrid" || request.accept != "application/vnd.github.text-match+json" {
		t.Errorf("filtered request = %q accept %q", request.query, request.accept)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	if _, err := invoke(t, core, pullRequestsSearch.ID, "org", fmt.Sprintf(filtered, `,"cursor":"`+page.NextCursor+`"`),
		false); err != nil {
		t.Errorf("continuation with the same filters = %v", err)
	}
	for name, arguments := range map[string]string{
		"another sort":         `{"terms":"crash","sort":"comments","order":"asc","advanced_search":true,"search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"another order":        `{"terms":"crash","sort":"reactions-+1","order":"desc","advanced_search":true,"search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"no text matches":      `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":true,"search_type":"hybrid","cursor":"%s"}`,
		"no filter at all":     `{"terms":"crash","cursor":"%s"}`,
		"filters in the terms": `{"terms":"crash sort:reactions-+1","cursor":"%s"}`,
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, pullRequestsSearch.ID, "org", fmt.Sprintf(arguments, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s reached GitHub", name)
		}
	}
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, pullRequestsSearch.ID, "org", `{"terms":"crash","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	for _, arguments := range []string{`{"terms":"crash","sort":"stars"}`, `{"terms":"crash","search_type":"vector"}`,
		`{"terms":"crash","order":"up"}`} {
		if _, err := invoke(t, core, pullRequestsSearch.ID, "org", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
}

// With advanced_search or search_type set, a match outside the connection's targets is refused.
func TestPullRequestSearchModesNeverReachBeyondTheTargets(t *testing.T) {
	outside := strings.ReplaceAll(documentedPullSearchPage, "octo-org/example", "other/example")
	for _, arguments := range []string{`{"terms":"x","search_type":"semantic"}`, `{"terms":"x","advanced_search":true}`} {
		_, core := cannedCore(t, outside, false, discoveryConfig)
		if _, err := invoke(t, core, pullRequestsSearch.ID, "org", arguments, false); err == nil {
			t.Errorf("%s outside the targets was answered", arguments)
		}
		if _, err := invoke(t, core, pullRequestsSearch.ID, "open", arguments, false); err != nil {
			t.Errorf("%s without targets = %v", arguments, err)
		}
	}
}
