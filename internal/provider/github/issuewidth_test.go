package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The issue documented by GitHub's REST reference for "Get an issue", its accounts shortened to
// documentedAccount and without the pull_request member the issue tools refuse; the members the example
// leaves out (type, reactions, the summaries, issue field values, the pinned comment, the app, draft, and
// the timeline and parent addresses) are filled with the documented shapes.
const (
	documentedIssueLabel = `{"id":208045946,"node_id":"MDU6TGFiZWwyMDgwNDU5NDY=",` +
		`"url":"https://api.github.com/repos/octocat/Hello-World/labels/bug","name":"bug",` +
		`"description":"Something isn't working","color":"f29513","default":true}`
	documentedIssueType = `{"id":1,"node_id":"IT_kwDNJr9H","name":"Bug","description":"An unexpected problem or behavior",` +
		`"color":"red","created_at":"2023-10-09T13:30:49Z","updated_at":"2024-05-30T18:20:53Z","is_enabled":true}`
	documentedApp = `{"id":1,"slug":"octoapp","node_id":"MDExOkludGVncmF0aW9uMQ==","client_id":"Iv1.ab1112223334445c",` +
		`"owner":` + documentedAccount + `,"name":"Octocat App","description":"","external_url":"https://example.com",` +
		`"html_url":"https://github.com/apps/octoapp","created_at":"2017-07-08T16:18:44-04:00",` +
		`"updated_at":"2017-07-08T16:18:44-04:00","permissions":{"metadata":"read","contents":"read","issues":"write"},` +
		`"events":["push","pull_request"]}`
	documentedComment = `{"id":1,"node_id":"MDEyOklzc3VlQ29tbWVudDE=",` +
		`"url":"https://api.github.com/repos/octocat/Hello-World/issues/comments/1",` +
		`"html_url":"https://github.com/octocat/Hello-World/issues/1347#issuecomment-1","body":"Me too",` +
		`"user":` + documentedAccount + `,"created_at":"2011-04-14T16:00:49Z","updated_at":"2011-04-14T16:00:49Z",` +
		`"issue_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347","author_association":"COLLABORATOR"}`
	documentedIssueBody = `"id":1,"node_id":"MDU6SXNzdWUx",` +
		`"url":"https://api.github.com/repos/octocat/Hello-World/issues/1347",` +
		`"repository_url":"https://api.github.com/repos/octocat/Hello-World",` +
		`"labels_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/labels{/name}",` +
		`"comments_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/comments",` +
		`"events_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/events",` +
		`"html_url":"https://github.com/octocat/Hello-World/issues/1347","number":1347,"state":"open",` +
		`"title":"Found a bug","body":"I'm having a problem with this.","user":` + documentedAccount + `,` +
		`"pinned_comment":` + documentedComment + `,"labels":[` + documentedIssueLabel + `],` +
		`"assignee":` + documentedAccount + `,"assignees":[` + documentedAccount + `],` +
		`"milestone":` + documentedMilestone + `,"locked":true,"active_lock_reason":"too heated","comments":0,` +
		`"closed_at":null,"created_at":"2011-04-22T13:33:48Z","updated_at":"2011-04-22T13:33:48Z",` +
		`"closed_by":` + documentedAccount + `,"author_association":"COLLABORATOR","state_reason":"completed",` +
		`"type":` + documentedIssueType + `,"draft":false,` +
		`"timeline_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/timeline",` +
		`"parent_issue_url":"https://api.github.com/repos/octocat/Hello-World/issues/1",` +
		`"performed_via_github_app":` + documentedApp + `,` +
		`"reactions":{"url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/reactions",` +
		`"total_count":3,"+1":1,"-1":0,"laugh":0,"confused":0,"heart":1,"hooray":0,"eyes":1,"rocket":0},` +
		`"sub_issues_summary":{"total":2,"completed":1,"percent_completed":50},` +
		`"issue_dependencies_summary":{"blocked_by":1,"blocking":0,"total_blocked_by":1,"total_blocking":0},` +
		`"issue_field_values":[{"issue_field_id":1,"issue_field_name":"Priority","node_id":"IFT_GDKND",` +
		`"data_type":"single_select","value":"High","single_select_option":{"id":1,"name":"High","color":"red"}},` +
		`{"issue_field_id":2,"node_id":"IFT_2","data_type":"number","value":42.5},` +
		`{"issue_field_id":3,"node_id":"IFT_3","data_type":"multi_select","value":null,` +
		`"multi_select_options":[{"id":1,"name":"High","color":"red"}]}]`
	documentedIssue = `{` + documentedIssueBody + `}`
)

// The expected projections of the documented objects above.
const (
	accountWant = `{"avatar_url":"https://github.com/images/error/octocat_happy.gif","gravatar_id":"","id":1,` +
		`"links":{"followers_url":"https://api.github.com/users/octocat/followers",` +
		`"url":"https://api.github.com/users/octocat"},"login":"octocat","node_id":"MDQ6VXNlcjE=",` +
		`"site_admin":false,"type":"User","url":"https://github.com/octocat"}`
	milestoneWant = `{"closed_at":"2013-02-12T13:22:01Z","closed_issues":8,"created_at":"2011-04-10T20:09:31Z",` +
		`"creator":` + accountWant + `,"description":"Tracking milestone for version 1.0",` +
		`"due_on":"2012-10-09T23:39:01Z","id":1002604,` +
		`"links":{"labels_url":"https://api.github.com/repos/octocat/Hello-World/milestones/1/labels",` +
		`"url":"https://api.github.com/repos/octocat/Hello-World/milestones/1"},` +
		`"node_id":"MDk6TWlsZXN0b25lMTAwMjYwNA==","number":1,"open_issues":4,"state":"open","title":"v1.0",` +
		`"updated_at":"2014-03-03T18:58:10Z","url":"https://github.com/octocat/Hello-World/milestones/v1.0"}`
	issueTypeWant = documentedIssueType
	// issueWidthWant holds every member of the issue width except draft and the links.
	issueWidthWant = `"id":1,"node_id":"MDU6SXNzdWUx","assignee":` + accountWant + `,` +
		`"assignees_details":[` + accountWant + `],` +
		`"labels_details":[{"color":"f29513","default":true,"description":"Something isn't working",` +
		`"id":208045946,"links":{"url":"https://api.github.com/repos/octocat/Hello-World/labels/bug"},` +
		`"name":"bug","node_id":"MDU6TGFiZWwyMDgwNDU5NDY="}],` +
		`"milestone_detail":` + milestoneWant + `,"issue_type_detail":` + issueTypeWant + `,` +
		`"locked":true,"active_lock_reason":"too heated","comments":0,"closed_by":` + accountWant + `,` +
		`"author_association":"COLLABORATOR",` +
		`"performed_via_github_app":{"client_id":"Iv1.ab1112223334445c","created_at":"2017-07-08T16:18:44-04:00",` +
		`"description":"","events":["push","pull_request"],"external_url":"https://example.com","id":1,` +
		`"name":"Octocat App","node_id":"MDExOkludGVncmF0aW9uMQ==","owner":` + accountWant + `,` +
		`"permissions":{"contents":"read","issues":"write","metadata":"read"},"slug":"octoapp",` +
		`"updated_at":"2017-07-08T16:18:44-04:00","url":"https://github.com/apps/octoapp"},` +
		`"reactions":{"confused":0,"eyes":1,"heart":1,"hooray":0,"laugh":0,` +
		`"links":{"url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/reactions"},` +
		`"minus_one":0,"plus_one":1,"rocket":0,"total_count":3},` +
		`"sub_issues_summary":{"total":2,"completed":1,"percent_completed":50},` +
		`"issue_dependencies_summary":{"blocked_by":1,"blocking":0,"total_blocked_by":1,"total_blocking":0},` +
		`"issue_field_values":[{"issue_field_id":1,"issue_field_name":"Priority","node_id":"IFT_GDKND",` +
		`"data_type":"single_select","value":"High","single_select_option":{"id":1,"name":"High","color":"red"}},` +
		`{"issue_field_id":2,"node_id":"IFT_2","data_type":"number","value":42.5},` +
		`{"issue_field_id":3,"node_id":"IFT_3","data_type":"multi_select",` +
		`"multi_select_options":[{"id":1,"name":"High","color":"red"}]}],` +
		`"pinned_comment":{"author_association":"COLLABORATOR","body":"Me too","created_at":"2011-04-14T16:00:49Z",` +
		`"id":1,"links":{"issue_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347",` +
		`"url":"https://api.github.com/repos/octocat/Hello-World/issues/comments/1"},` +
		`"node_id":"MDEyOklzc3VlQ29tbWVudDE=","updated_at":"2011-04-14T16:00:49Z",` +
		`"url":"https://github.com/octocat/Hello-World/issues/1347#issuecomment-1","user":` + accountWant + `}`
	issueLinksWant = `"url":"https://api.github.com/repos/octocat/Hello-World/issues/1347",` +
		`"repository_url":"https://api.github.com/repos/octocat/Hello-World",` +
		`"labels_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/labels{/name}",` +
		`"comments_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/comments",` +
		`"events_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/events",` +
		`"timeline_url":"https://api.github.com/repos/octocat/Hello-World/issues/1347/timeline"`
	parentLinkWant = `"parent_issue_url":"https://api.github.com/repos/octocat/Hello-World/issues/1"`
	// issueGetWant is github.issues.get on documentedIssue: the members it answered before, then the rest.
	issueGetWant = `{"number":1347,"title":"Found a bug","state":"open","state_reason":"completed","author":"octocat",` +
		`"assignees":["octocat"],"labels":["bug"],"milestone":"v1.0","issue_type":"Bug",` +
		`"url":"https://github.com/octocat/Hello-World/issues/1347","created_at":"2011-04-22T13:33:48Z",` +
		`"updated_at":"2011-04-22T13:33:48Z","body":"I'm having a problem with this.",` +
		`"repository":"octo-org/example","author_detail":` + accountWant + `,"draft":false,` + issueWidthWant +
		`,"links":{` + issueLinksWant + `,` + parentLinkWant + `}}`
	// issueEntryWant is one entry of the sub-issue and dependency lists on documentedIssue, without links.
	issueEntryWant = `"number":1347,"title":"Found a bug","state":"open",` +
		`"url":"https://github.com/octocat/Hello-World/issues/1347","updated_at":"2011-04-22T13:33:48Z",` +
		`"state_reason":"completed","body":"I'm having a problem with this.","user":` + accountWant + `,` +
		`"issue_type":"Bug","created_at":"2011-04-22T13:33:48Z","draft":false,` + issueWidthWant
)

// serveRoutes answers each of the given paths, with any method, with a fixed JSON body.
func serveRoutes(t *testing.T, routes map[string]string) (*fakeGitHub, string) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, req *http.Request) bool {
		body, ok := routes[req.URL.Path]
		if !ok {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
		return true
	}
	return f, serve(t, f)
}

// issueCore serves one issue body at the issue routes of octo-org/example the issue tools read and change.
func issueCore(t *testing.T, number int, body string) *application.Core {
	t.Helper()
	_, base := serveRoutes(t, map[string]string{
		fmt.Sprintf("/api/v3/repos/octo-org/example/issues/%d", number): body,
		"/api/v3/repos/octo-org/example/issues":                         body,
	})
	red := &redact.Redactor{}
	return application.New(registry(t), labelsConfig(base), resolver(red, nil), red)
}

// issueToolCalls are the issue tools that answer through issueOf, with arguments for the given number.
func issueToolCalls(number int) []struct {
	id, args string
	confirm  bool
} {
	n := fmt.Sprint(number)
	return []struct {
		id, args string
		confirm  bool
	}{
		{issuesCreate.ID, `{"title":"Found a bug"}`, true},
		{issuesGet.ID, `{"number":` + n + `}`, false},
		{issuesUpdate.ID, `{"number":` + n + `,"body":"x"}`, true},
		{issuesClose.ID, `{"number":` + n + `}`, true},
		{issuesReopen.ID, `{"number":` + n + `}`, true},
	}
}

// github.issues.get and the four issue changes report every member GitHub documents for an issue.
func TestIssueToolsReportEveryDocumentedMember(t *testing.T) {
	core := issueCore(t, 1347, documentedIssue)
	for _, call := range issueToolCalls(1347) {
		got, err := invoke(t, core, call.id, "repo", call.args, call.confirm)
		if err != nil {
			t.Fatalf("%s: %v", call.id, err)
		}
		sameJSON(t, call.id, got, issueGetWant)
	}
}

// An issue answer without the new members keeps its earlier output byte for byte, in the read and in every
// change; with the objects behind the flat members present, the flat members stay exactly as before and only
// their detail siblings join them.
func TestIssueToolsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const old = `{"number":42,"title":"T","state":"open","state_reason":null,"body":"b","user":null,` +
		`"assignees":[],"labels":[],"milestone":null,"type":null,` +
		`"html_url":"https://github.com/octo-org/example/issues/42","created_at":"2026-01-01T00:00:00Z",` +
		`"updated_at":"2026-01-02T00:00:00Z","closed_at":null}`
	// The application leaves empty lists out of every answer, as it always did.
	const oldWant = `{"body":"b","created_at":"2026-01-01T00:00:00Z","number":42,` +
		`"repository":"octo-org/example","state":"open","title":"T","updated_at":"2026-01-02T00:00:00Z",` +
		`"url":"https://github.com/octo-org/example/issues/42"}`
	core := issueCore(t, 42, old)
	// issueToolCalls names github.issues.create first, which needs a node_id in GitHub's answer.
	for _, call := range issueToolCalls(42)[1:] {
		got, err := invoke(t, core, call.id, "repo", call.args, call.confirm)
		if err != nil || string(got) != oldWant {
			t.Errorf("%s = %s, %v, want %s", call.id, got, err, oldWant)
		}
	}
	// A new issue was always refused without its node_id, which it now also reports.
	core = issueCore(t, 42, strings.Replace(old, `{"number":42,`, `{"number":42,"node_id":"I_42",`, 1))
	got, err := invoke(t, core, issuesCreate.ID, "repo", `{"title":"T"}`, true)
	if err != nil || string(got) != strings.Replace(oldWant, `"number":42,`, `"node_id":"I_42","number":42,`, 1) {
		t.Errorf("%s = %s, %v", issuesCreate.ID, got, err)
	}

	const flat = `{"number":42,"title":"T","state":"closed","state_reason":"completed","body":"b",` +
		`"user":{"login":"octocat"},"assignees":[{"login":"hubot"}],"labels":[{"name":"bug"}],` +
		`"milestone":{"title":"v1"},"type":{"name":"Bug"},"html_url":"https://github.com/octo-org/example/issues/42",` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","closed_at":"2026-01-03T00:00:00Z"}`
	const flatWant = `{"assignees":["hubot"],"author":"octocat","body":"b","closed_at":"2026-01-03T00:00:00Z",` +
		`"created_at":"2026-01-01T00:00:00Z","issue_type":"Bug","labels":["bug"],"milestone":"v1","number":42,` +
		`"repository":"octo-org/example","state":"closed","state_reason":"completed","title":"T",` +
		`"updated_at":"2026-01-02T00:00:00Z","url":"https://github.com/octo-org/example/issues/42"}`
	core = issueCore(t, 42, flat)
	for _, call := range issueToolCalls(42)[1:] {
		got, err := invoke(t, core, call.id, "repo", call.args, call.confirm)
		if err != nil {
			t.Fatalf("%s: %v", call.id, err)
		}
		var members map[string]any
		if err := json.Unmarshal(got, &members); err != nil {
			t.Fatal(err)
		}
		for _, detail := range []string{"author_detail", "assignees_details", "labels_details", "milestone_detail",
			"issue_type_detail"} {
			if _, ok := members[detail]; !ok {
				t.Errorf("%s lacks %s: %s", call.id, detail, got)
			}
			delete(members, detail)
		}
		if rest, _ := json.Marshal(members); string(rest) != flatWant {
			t.Errorf("%s flat members = %s, want %s", call.id, rest, flatWant)
		}
	}
}

// The members the issue tools answered before keep their earlier handling: no cleaning and no cut. Only the
// new members pass through the projection bounds, and a cut there carries a marker.
func TestIssueToolsKeepTheEarlierHandlingOfTheCompactMembers(t *testing.T) {
	long := longText1500 + strings.Repeat("y", projectionBodyLimit)
	raw, _ := json.Marshal(map[string]any{"number": 42, "title": long, "state": "open", "state_reason": long,
		"body": long, "user": map[string]any{"login": long}, "assignees": []any{map[string]any{"login": long}},
		"labels": []any{map[string]any{"name": long}}, "milestone": map[string]any{"title": long},
		"type": map[string]any{"name": long}, "html_url": "https://github.com/octo-org/example/issues/42",
		"issue_field_values": []any{map[string]any{"issue_field_id": 1, "value": long}}})
	core := issueCore(t, 42, string(raw))
	got, err := invoke(t, core, issuesGet.ID, "repo", `{"number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var issue Issue
	if err := json.Unmarshal(got, &issue); err != nil {
		t.Fatal(err)
	}
	if issue.Title != long || issue.StateReason != long || issue.Body != long || issue.Author != long ||
		issue.Assignees[0] != long || issue.Labels[0] != long || issue.Milestone != long || issue.IssueType != long {
		t.Error("github.issues.get changed a member it answered before")
	}
	if *issue.AuthorDetail.Login != "a b"+strings.Repeat("x", projectionNameLimit-3) ||
		*issue.LabelsDetails[0].Name != *issue.AuthorDetail.Login ||
		*issue.IssueTypeDetail.Name != *issue.AuthorDetail.Login {
		t.Errorf("new members are not cleaned and bounded: %s", got)
	}
	if len(issue.IssueFieldValues) != 1 || issue.IssueFieldValues[0].Value != nil ||
		!issue.IssueFieldValues[0].ValueOmitted {
		t.Errorf("an issue field value beyond the bounds = %+v, want it left out and marked", issue.IssueFieldValues)
	}
}

// documentedSearchItem is the item of GitHub's documented issue search answer, its accounts shortened to
// documentedAccount, with a repository and text matches added; the documented pull_request member stays,
// since the search never reads it.
const documentedSearchItem = `{"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132",` +
	`"repository_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit",` +
	`"labels_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/labels{/name}",` +
	`"comments_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/comments",` +
	`"events_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/events",` +
	`"html_url":"https://github.com/batterseapower/pinyin-toolkit/issues/132","id":35802,` +
	`"node_id":"MDU6SXNzdWUzNTgwMg==","number":132,"title":"Line Number Indexes Beyond 20 Not Displayed",` +
	`"type":` + documentedIssueType + `,"user":` + documentedAccount + `,` +
	`"labels":[{"id":4,"node_id":"MDU6TGFiZWw0",` +
	`"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/labels/bug","name":"bug","color":"ff0000"}],` +
	`"state":"open","assignee":null,"milestone":` + documentedMilestone + `,"comments":15,` +
	`"created_at":"2009-07-12T20:10:41Z","updated_at":"2009-07-19T09:23:43Z","closed_at":null,` +
	`"pull_request":{"url":"https://api/github.com/repos/octocat/Hello-World/pull/1347",` +
	`"html_url":"https://github.com/octocat/Hello-World/pull/1347"},"body":"...","score":1,"locked":true,` +
	`"author_association":"COLLABORATOR","state_reason":"completed",` +
	`"repository":{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","name":"pinyin-toolkit",` +
	`"full_name":"batterseapower/pinyin-toolkit","private":false,` +
	`"html_url":"https://github.com/batterseapower/pinyin-toolkit",` +
	`"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit"},` +
	`"text_matches":[{"object_url":"https://api.github.com/repositories/215335/issues/132","object_type":"Issue",` +
	`"property":"body","fragment":"Line Number Indexes","matches":[{"text":"Line","indices":[0,4]}]}]}`

const documentedSearchPage = `{"total_count":280,"incomplete_results":false,"search_type":"hybrid",` +
	`"items":[` + documentedSearchItem + `]}`

// github.issues.search reports every member GitHub documents for a match and for the search; a repository
// outside the connection's targets keeps only its identifiers.
func TestIssueSearchReportsEveryDocumentedMember(t *testing.T) {
	const compact = `"number":132,"title":"Line Number Indexes Beyond 20 Not Displayed",` +
		`"repository":"batterseapower/pinyin-toolkit","state":"open","labels":["bug"],` +
		`"updated_at":"2009-07-19T09:23:43Z","url":"https://github.com/batterseapower/pinyin-toolkit/issues/132"`
	const rest = `"state_reason":"completed","body":"...","user":` + accountWant + `,"issue_type":"Bug",` +
		`"created_at":"2009-07-12T20:10:41Z","id":35802,"node_id":"MDU6SXNzdWUzNTgwMg==",` +
		`"labels_details":[{"color":"ff0000","id":4,` +
		`"links":{"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/labels/bug"},"name":"bug",` +
		`"node_id":"MDU6TGFiZWw0"}],"milestone_detail":` + milestoneWant + `,"issue_type_detail":` + issueTypeWant +
		`,"locked":true,"comments":15,"author_association":"COLLABORATOR",` +
		`"links":{"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132",` +
		`"repository_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit",` +
		`"labels_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/labels{/name}",` +
		`"comments_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/comments",` +
		`"events_url":"https://api.github.com/repos/batterseapower/pinyin-toolkit/issues/132/events"},"score":1,` +
		`"text_matches":[{"object_type":"Issue","property":"body","fragment":"Line Number Indexes",` +
		`"matches":[{"text":"Line","indices":[0,4]}],` +
		`"links":{"object_url":"https://api.github.com/repositories/215335/issues/132"}}]`
	const top = `"search_type":"hybrid","total_count":280,"incomplete_results":false,"has_more":false`
	for _, c := range []struct{ connection, repository string }{
		{"open", `{"full_name":"batterseapower/pinyin-toolkit","id":1296269,` +
			`"links":{"url":"https://api.github.com/repos/batterseapower/pinyin-toolkit"},"name":"pinyin-toolkit",` +
			`"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","private":false,` +
			`"url":"https://github.com/batterseapower/pinyin-toolkit"}`},
		{"org", `{"full_name":"batterseapower/pinyin-toolkit","id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5"}`},
	} {
		_, core := cannedCore(t, documentedSearchPage, false, discoveryConfig)
		got, err := invoke(t, core, issuesSearch.ID, c.connection, `{"terms":"windows","text_matches":true}`, false)
		if err != nil {
			t.Fatalf("%s: %v", c.connection, err)
		}
		sameJSON(t, issuesSearch.ID+" "+c.connection, got, `{"issues":[{`+compact+`,`+rest+
			`,"repository_detail":`+c.repository+`}],`+top+`}`)
	}
}

// Both searches answer what they always did when GitHub sends none of the new members.
func TestSearchesWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const old = `{"total_count":1,"incomplete_results":false,"items":[{"number":42,"title":"T","state":"open",` +
		`"updated_at":"2026-01-02T00:00:00Z","html_url":"https://github.com/octo-org/example/issues/42",` +
		`"labels":[{"name":"bug"}],"assignees":[{"login":"hubot"}],` +
		`"repository_url":"https://api.github.com/repos/octo-org/example"}]}`
	for _, c := range []struct{ tool, key, want string }{
		{issuesSearch.ID, "issues", `{"has_more":false,"incomplete_results":false,"issues":[{"assignees":["hubot"],` +
			`"labels":["bug"],"number":42,"repository":"octo-org/example","state":"open","title":"T",` +
			`"updated_at":"2026-01-02T00:00:00Z","url":"https://github.com/octo-org/example/issues/42"}],` +
			`"total_count":1}`},
		{pullRequestsSearch.ID, "pull_requests", `{"has_more":false,"incomplete_results":false,"pull_requests":[` +
			`{"assignees":["hubot"],"labels":["bug"],"number":42,"repository":"octo-org/example","state":"open",` +
			`"title":"T","updated_at":"2026-01-02T00:00:00Z",` +
			`"url":"https://github.com/octo-org/example/issues/42"}],"total_count":1}`},
	} {
		_, core := cannedCore(t, old, false, discoveryConfig)
		got, err := invoke(t, core, c.tool, "open", `{"terms":"x"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		// The labels, the assignees, and repository_url are what GitHub sends for the flat members, so only
		// their details and the links join them.
		var members map[string]any
		if err := json.Unmarshal(got, &members); err != nil {
			t.Fatal(err)
		}
		entry := members[c.key].([]any)[0].(map[string]any)
		for _, added := range []string{"labels_details", "assignees_details", "links"} {
			if _, ok := entry[added]; !ok {
				t.Errorf("%s lacks %s: %s", c.tool, added, got)
			}
			delete(entry, added)
		}
		if rest, _ := json.Marshal(members); string(rest) != c.want {
			t.Errorf("%s = %s, want %s", c.tool, rest, c.want)
		}
	}
}

// The members the issue search, the sub-issue list, and the pull request search answered before keep their
// earlier handling; a new body over its bound is cut with a marker.
func TestIssueListsKeepTheEarlierHandlingOfTheCompactMembers(t *testing.T) {
	long := longText1500 + strings.Repeat("y", projectionBodyLimit)
	item, _ := json.Marshal(map[string]any{"number": 42, "title": long, "state": long, "body": long,
		"labels": []any{map[string]any{"name": long}}, "assignees": []any{map[string]any{"login": long}},
		"updated_at": long, "repository_url": "https://api.github.com/repos/octo-org/example"})
	page := `{"total_count":1,"incomplete_results":false,"items":[` + string(item) + `]}`
	for _, tool := range []string{issuesSearch.ID, pullRequestsSearch.ID} {
		_, core := cannedCore(t, page, false, discoveryConfig)
		got, err := invoke(t, core, tool, "open", `{"terms":"x"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Issues       []IssueSearchResult       `json:"issues"`
			PullRequests []PullRequestSearchResult `json:"pull_requests"`
		}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatal(err)
		}
		if tool == issuesSearch.ID {
			match := result.Issues[0]
			if match.Title != long || match.State != long || match.Labels[0] != long || match.Assignees[0] != long ||
				match.UpdatedAt != long {
				t.Errorf("%s changed a member it answered before", tool)
			}
			if !match.BodyTruncated || len([]rune(*match.Body)) != projectionBodyLimit ||
				strings.Contains(*match.Body, "\x01") {
				t.Errorf("%s body is not cleaned and cut with a marker", tool)
			}
			continue
		}
		match := result.PullRequests[0]
		if match.Title != long || match.State != long || match.Labels[0] != long || match.Assignees[0] != long {
			t.Errorf("%s changed a member it answered before", tool)
		}
	}

	sub, _ := json.Marshal([]any{map[string]any{"number": 43, "title": long, "state": long,
		"html_url": "https://github.com/octo-org/example/issues/43"}})
	_, base := serveRoutes(t, map[string]string{"/api/v3/repos/octo-org/example/issues/42/sub_issues": string(sub)})
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)
	got, err := invoke(t, core, subIssuesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var list SubIssueList
	if err := json.Unmarshal(got, &list); err != nil || len(list.SubIssues) != 1 {
		t.Fatalf("%s, %v", got, err)
	}
	if list.SubIssues[0].Title != long || list.SubIssues[0].State != long {
		t.Error("github.sub-issues.list changed a member it answered before")
	}
}

// github.issues.search sends its filters as their own query arguments beside the unchanged q, asks for text
// matches only when text_matches is set, and binds the cursor to the filters that are set.
func TestIssueSearchFiltersReachTheRequestAndTheCursor(t *testing.T) {
	f, core := cannedCore(t, `{"total_count":1,"incomplete_results":false,"items":[]}`, true, discoveryConfig)

	plain, err := invoke(t, core, issuesSearch.ID, "org", `{"terms":"crash"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ := url.ParseQuery(request.query)
	for _, key := range []string{"sort", "order", "advanced_search", "search_type"} {
		if values.Has(key) {
			t.Errorf("plain request carries %s: %q", key, request.query)
		}
	}
	if values.Get("q") != "is:issue org:octo-org crash" || request.accept != "application/vnd.github+json" {
		t.Errorf("plain request = %q accept %q", request.query, request.accept)
	}

	// A filter without text_matches keeps the default media type; advanced_search false is sent as given.
	if _, err := invoke(t, core, issuesSearch.ID, "org", `{"terms":"crash","sort":"updated","advanced_search":false}`,
		false); err != nil {
		t.Fatal(err)
	}
	request = lastRequest(t, f)
	values, _ = url.ParseQuery(request.query)
	if values.Get("sort") != "updated" || values.Get("advanced_search") != "false" ||
		request.accept != "application/vnd.github+json" {
		t.Errorf("sorted request = %q accept %q", request.query, request.accept)
	}

	const filtered = `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":true,` +
		`"search_type":"hybrid","text_matches":true%s}`
	first, err := invoke(t, core, issuesSearch.ID, "org", fmt.Sprintf(filtered, ""), false)
	if err != nil {
		t.Fatal(err)
	}
	request = lastRequest(t, f)
	values, _ = url.ParseQuery(request.query)
	if values.Get("q") != "is:issue org:octo-org crash" || values.Get("sort") != "reactions-+1" ||
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
	if _, err := invoke(t, core, issuesSearch.ID, "org", fmt.Sprintf(filtered, `,"cursor":"`+page.NextCursor+`"`),
		false); err != nil {
		t.Errorf("continuation with the same filters = %v", err)
	}
	for name, arguments := range map[string]string{
		"another sort":         `{"terms":"crash","sort":"comments","order":"asc","advanced_search":true,"search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"another order":        `{"terms":"crash","sort":"reactions-+1","order":"desc","advanced_search":true,"search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"advanced search off":  `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":false,"search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"no advanced search":   `{"terms":"crash","sort":"reactions-+1","order":"asc","search_type":"hybrid","text_matches":true,"cursor":"%s"}`,
		"another search type":  `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":true,"search_type":"semantic","text_matches":true,"cursor":"%s"}`,
		"no text matches":      `{"terms":"crash","sort":"reactions-+1","order":"asc","advanced_search":true,"search_type":"hybrid","cursor":"%s"}`,
		"no filter at all":     `{"terms":"crash","cursor":"%s"}`,
		"filters in the terms": `{"terms":"crash sort:reactions-+1","cursor":"%s"}`,
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, issuesSearch.ID, "org", fmt.Sprintf(arguments, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s reached GitHub", name)
		}
	}

	// A cursor of an unfiltered search stays valid for an unfiltered continuation.
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, issuesSearch.ID, "org", `{"terms":"crash","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}

	for _, arguments := range []string{`{"terms":"crash","sort":"stars"}`, `{"terms":"crash","search_type":"vector"}`,
		`{"terms":"crash","order":"up"}`} {
		if _, err := invoke(t, core, issuesSearch.ID, "org", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
}

// With advanced_search or search_type set, a match outside the connection's targets is refused rather than
// shown, so no filter GitHub reads differently can widen the forced qualifiers.
func TestIssueSearchModesNeverReachBeyondTheTargets(t *testing.T) {
	for _, arguments := range []string{`{"terms":"x","search_type":"semantic"}`, `{"terms":"x","advanced_search":true}`} {
		_, core := cannedCore(t, documentedSearchPage, false, discoveryConfig)
		_, err := invoke(t, core, issuesSearch.ID, "org", arguments, false)
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassInvalidResponse {
			t.Errorf("%s outside the targets = %v, want an invalid response", arguments, err)
		}
		if _, err := invoke(t, core, issuesSearch.ID, "open", arguments, false); err != nil {
			t.Errorf("%s without targets = %v", arguments, err)
		}
	}
}

// github.sub-issues.list and github.issue-dependencies.list report every member GitHub documents for each
// listed issue. A sub-issue leaves out parent_issue_url, the parent the request named; a dependency keeps it.
// An issue of another repository carries its repository in full, as the targets admit it.
func TestIssueEntriesReportEveryDocumentedMember(t *testing.T) {
	other := strings.Replace(documentedIssue, `"id":1,"node_id":"MDU6SXNzdWUx",`,
		`"id":1,"node_id":"MDU6SXNzdWUx","repository":{"id":7,"node_id":"R_7","name":"other",`+
			`"full_name":"octo-org/other","private":true},`, 1)
	_, base := serveRoutes(t, map[string]string{
		"/api/v3/repos/octo-org/example/issues/42/sub_issues":              `[` + documentedIssue + `,` + other + `]`,
		"/api/v3/repos/octo-org/example/issues/42/dependencies/blocked_by": `[` + documentedIssue + `]`,
		"/api/v3/repos/octo-org/example/issues/42/dependencies/blocking":   `[` + other + `]`,
	})
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)
	const repository = `"repository":"octo-org/other","repository_detail":{"full_name":"octo-org/other","id":7,` +
		`"name":"other","node_id":"R_7","private":true}`
	sub := `{` + issueEntryWant + `,"links":{` + issueLinksWant + `}}`
	subOther := `{` + issueEntryWant + `,` + repository + `,"links":{` + issueLinksWant + `}}`
	dep := `{` + issueEntryWant + `,"links":{` + issueLinksWant + `,` + parentLinkWant + `}}`
	depOther := `{` + issueEntryWant + `,` + repository + `,"links":{` + issueLinksWant + `,` + parentLinkWant + `}}`

	got, err := invoke(t, core, subIssuesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, subIssuesList.ID, got, `{"sub_issues":[`+sub+`,`+subOther+`],"has_more":false,`+
		`"repository":"octo-org/example"}`)
	got, err = invoke(t, core, issueDependenciesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, issueDependenciesList.ID, got, `{"blocked_by":[`+dep+`],"blocking":[`+depOther+`],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

// Without the new members the sub-issue and dependency lists answer exactly what they always answered.
func TestIssueEntriesWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const old = `[{"number":43,"title":"T","state":"open","html_url":"https://github.com/octo-org/example/issues/43",` +
		`"repository":{"full_name":"octo-org/example"}}]`
	_, base := serveRoutes(t, map[string]string{
		"/api/v3/repos/octo-org/example/issues/42/sub_issues":              old,
		"/api/v3/repos/octo-org/example/issues/42/dependencies/blocked_by": old,
		"/api/v3/repos/octo-org/example/issues/42/dependencies/blocking":   `[]`,
	})
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)
	const entry = `{"number":43,"state":"open","title":"T","url":"https://github.com/octo-org/example/issues/43"}`
	for _, c := range []struct{ id, want string }{
		{subIssuesList.ID, `{"has_more":false,"repository":"octo-org/example","sub_issues":[` + entry + `]}`},
		{issueDependenciesList.ID, `{"blocked_by":[` + entry + `],"has_more":false,` +
			`"repository":"octo-org/example"}`},
	} {
		got, err := invoke(t, core, c.id, "repo", `{"repository":"octo-org/example","number":42}`, false)
		if err != nil || string(got) != c.want {
			t.Errorf("%s = %s, %v, want %s", c.id, got, err, c.want)
		}
	}
}

// github.issues.list names the issue type of each issue from the same GraphQL query, in one request.
func TestIssuesListNamesTheIssueType(t *testing.T) {
	const page = `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[` +
		`{"number":2,"title":"B","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","assignees":{"nodes":[]},` +
		`"labels":{"nodes":[]},"issueType":{"name":"Bug"}},` +
		`{"number":1,"title":"A","state":"OPEN","updatedAt":"2026-01-01T00:00:00Z","assignees":{"nodes":[]},` +
		`"labels":{"nodes":[]},"issueType":null}]}}}}`
	f, core := cannedCore(t, page, false, func(base string) *config.Config { return coreConfig(base) })
	got, err := invoke(t, core, issuesList.ID, "repo", `{}`, false)
	const want = `{"has_more":false,"issues":[{"issue_type":"Bug","number":2,"state":"open",` +
		`"title":"B","updated_at":"2026-01-02T00:00:00Z"},{"number":1,"state":"open",` +
		`"title":"A","updated_at":"2026-01-01T00:00:00Z"}],"repository":"octo-org/example"}`
	if err != nil || string(got) != want {
		t.Errorf("issues.list = %s, %v, want %s", got, err, want)
	}
	if requests := f.recorded(); len(requests) != 1 || !strings.Contains(requests[0].document, "issueType{name}") {
		t.Errorf("requests = %+v, want one query naming issueType", requests)
	}
}
