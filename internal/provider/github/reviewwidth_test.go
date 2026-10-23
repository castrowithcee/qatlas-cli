package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const widthReviewer = `{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=",` +
	`"avatar_url":"https://avatars.example.invalid/u/1","url":"https://api.github.com/users/octocat",` +
	`"html_url":"https://github.com/octocat","type":"User","site_admin":false}`

const widthReviewerView = `{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false,` +
	`"avatar_url":"https://avatars.example.invalid/u/1","url":"https://github.com/octocat",` +
	`"links":{"url":"https://api.github.com/users/octocat"}}`

// widthReview is a review as the List reviews example of GitHub's REST API describes it.
const widthReview = `{"id":80,"node_id":"MDE3OlB1bGxSZXF1ZXN0UmV2aWV3ODA=","user":` + widthReviewer + `,` +
	`"body":"Here is the body for the review.","state":"APPROVED",` +
	`"html_url":"https://github.com/octo-org/example/pull/7#pullrequestreview-80",` +
	`"pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7",` +
	`"_links":{"html":{"href":"https://github.com/octo-org/example/pull/7#pullrequestreview-80"},` +
	`"pull_request":{"href":"https://api.github.com/repos/octo-org/example/pulls/7"}},` +
	`"submitted_at":"2019-11-17T17:43:43Z","commit_id":"ecdd80bb57125d7ba9641ffaa4d7d2c19d3f3091",` +
	`"author_association":"COLLABORATOR"}`

const widthReviewView = `{"id":80,"author":"octocat","state":"APPROVED","body":"Here is the body for the review.",` +
	`"commit_sha":"ecdd80bb57125d7ba9641ffaa4d7d2c19d3f3091","submitted_at":"2019-11-17T17:43:43Z",` +
	`"node_id":"MDE3OlB1bGxSZXF1ZXN0UmV2aWV3ODA=","author_detail":` + widthReviewerView + `,` +
	`"author_association":"COLLABORATOR","url":"https://github.com/octo-org/example/pull/7#pullrequestreview-80",` +
	`"links":{"pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7"}}`

// widthLineComment is a line comment as the List review comments example of GitHub's REST API describes it.
const widthLineComment = `{"url":"https://api.github.com/repos/octo-org/example/pulls/comments/10",` +
	`"pull_request_review_id":42,"id":10,"node_id":"MDI0OlB1bGxSZXF1ZXN0UmV2aWV3Q29tbWVudDEw",` +
	`"diff_hunk":"@@ -16,33 +16,40 @@ public class Connection : IConnection\n {","path":"file1.txt",` +
	`"position":1,"original_position":4,"commit_id":"6dcb09b5b57875f334f61aebed695e2e4193db5e",` +
	`"original_commit_id":"9c48853fa3dc5c1c3d6f1f1cd1f2743e4c2e5e8f","in_reply_to_id":8,"user":` + widthReviewer + `,` +
	`"body":"Great stuff!","created_at":"2011-04-14T16:00:49Z","updated_at":"2011-04-14T16:00:50Z",` +
	`"html_url":"https://github.com/octo-org/example/pull/7#discussion-diff-10",` +
	`"pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7","author_association":"NONE",` +
	`"_links":{"self":{"href":"https://api.github.com/repos/octo-org/example/pulls/comments/10"}},` +
	`"body_html":"<p>Great stuff!</p>","body_text":"Great stuff!","start_line":1,"original_start_line":1,` +
	`"start_side":"RIGHT","line":2,"original_line":2,"side":"RIGHT","subject_type":"line","reactions":{` +
	`"url":"https://api.github.com/repos/octo-org/example/pulls/comments/10/reactions","total_count":1,"+1":1,` +
	`"-1":0,"laugh":0,"confused":0,"heart":0,"hooray":0,"eyes":0,"rocket":0}}`

const widthLineCommentView = `{"id":10,"path":"file1.txt","line":2,"side":"RIGHT",` +
	`"commit_sha":"6dcb09b5b57875f334f61aebed695e2e4193db5e","in_reply_to":8,"author":"octocat",` +
	`"body":"Great stuff!","created_at":"2011-04-14T16:00:49Z","updated_at":"2011-04-14T16:00:50Z",` +
	`"pull_request_review_id":42,"node_id":"MDI0OlB1bGxSZXF1ZXN0UmV2aWV3Q29tbWVudDEw",` +
	`"diff_hunk":"@@ -16,33 +16,40 @@ public class Connection : IConnection\n {","position":1,"original_position":4,` +
	`"original_commit_id":"9c48853fa3dc5c1c3d6f1f1cd1f2743e4c2e5e8f","original_line":2,"start_line":1,` +
	`"original_start_line":1,"start_side":"RIGHT","subject_type":"line","author_detail":` + widthReviewerView + `,` +
	`"author_association":"NONE","reactions":{"total_count":1,"plus_one":1,"minus_one":0,"laugh":0,"confused":0,` +
	`"heart":0,"hooray":0,"eyes":0,"rocket":0,` +
	`"links":{"url":"https://api.github.com/repos/octo-org/example/pulls/comments/10/reactions"}},` +
	`"url":"https://github.com/octo-org/example/pull/7#discussion-diff-10","links":{` +
	`"url":"https://api.github.com/repos/octo-org/example/pulls/comments/10",` +
	`"pull_request_url":"https://api.github.com/repos/octo-org/example/pulls/7"}}`

// serveWidthReviews answers the review and the line comment list of pull request 7 with the given documents.
func serveWidthReviews(t *testing.T, reviews, comments string, more bool) (*fakeGitHub, string) {
	t.Helper()
	f, _, base := serveReviews(t)
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		if more {
			w.Header().Set("Link", `<https://api.github.com/next>; rel="next"`)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			fmt.Fprint(w, reviews)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			fmt.Fprint(w, comments)
		default:
			return false
		}
		return true
	}
	return f, base
}

func TestReviewAndLineCommentListsCarryTheFullObjects(t *testing.T) {
	_, base := serveWidthReviews(t, `[`+widthReview+`]`, `[`+widthLineComment+`]`, false)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	reviews, err := invoke(t, core, pullRequestReviewsList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(reviews), `{"reviews":[`+widthReviewView+`],"has_more":false,"repository":"octo-org/example"}`)
	comments, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(comments), `{"line_comments":[`+widthLineCommentView+`],"has_more":false,`+
		`"repository":"octo-org/example"}`)

	for _, tc := range []struct {
		result json.RawMessage
		list   string
		schema json.RawMessage
	}{
		{reviews, "reviews", pullRequestReviewsList.OutputSchema},
		{comments, "line_comments", pullRequestReviewCommentsList.OutputSchema},
	} {
		entry := decodeAny(t, tc.result).(map[string]any)[tc.list].([]any)[0]
		checkSchemaCovers(t, "$."+tc.list+"[]", entry, decodeAny(t, entrySchema(t, tc.schema, tc.list)).(map[string]any))
	}
}

func TestReviewListsWithoutTheNewMembersKeepTheirOutput(t *testing.T) {
	_, base := serveWidthReviews(t,
		`[{"id":1,"user":{"login":"octocat"},"body":"ok","state":"COMMENTED","commit_id":"abc","submitted_at":"2026-01-01T00:00:00Z"}]`,
		`[{"id":2,"path":"a.go","line":3,"side":"RIGHT","commit_id":"abc","user":{"login":"octocat"},"body":"x",`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]`, false)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	reviews, err := invoke(t, core, pullRequestReviewsList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(reviews), `{"reviews":[{"id":1,"author":"octocat","state":"COMMENTED","body":"ok",`+
		`"commit_sha":"abc","submitted_at":"2026-01-01T00:00:00Z","author_detail":{"login":"octocat"}}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
	comments, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(comments), `{"line_comments":[{"id":2,"path":"a.go","line":3,"side":"RIGHT",`+
		`"commit_sha":"abc","author":"octocat","body":"x","created_at":"2026-01-01T00:00:00Z",`+
		`"updated_at":"2026-01-01T00:00:00Z","author_detail":{"login":"octocat"}}],"has_more":false,`+
		`"repository":"octo-org/example"}`)
}

func TestLineCommentFiltersReachTheQueryAndTheCursor(t *testing.T) {
	f, base := serveWidthReviews(t, `[]`, `[`+widthLineComment+`]`, true)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	filtered := `{"number":7,"limit":10,"sort":"updated","direction":"desc","since":"2026-01-01T02:00:00+02:00"}`

	first, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", filtered, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("no cursor in %s: %v", first, err)
	}
	next := `{"number":7,"limit":10,"sort":"updated","direction":"desc","since":"2026-01-01T00:00:00Z","cursor":"` +
		page.NextCursor + `"}`
	if _, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", next, false); err != nil {
		t.Fatalf("the same filters were refused: %v", err)
	}
	queries := []string{}
	for _, request := range f.recorded() {
		queries = append(queries, request.query)
	}
	want := []string{"direction=desc&page=1&per_page=10&since=2026-01-01T00%3A00%3A00Z&sort=updated",
		"direction=desc&page=2&per_page=10&since=2026-01-01T00%3A00%3A00Z&sort=updated"}
	if !reflect.DeepEqual(queries, want) {
		t.Errorf("queries = %v, want %v", queries, want)
	}
	for _, other := range []string{
		`{"number":7,"limit":10,"sort":"created","direction":"desc","since":"2026-01-01T00:00:00Z"`,
		`{"number":7,"limit":10,"sort":"updated","since":"2026-01-01T00:00:00Z"`,
		`{"number":7,"limit":10,"sort":"updated","direction":"desc"`,
		`{"number":7,"limit":10`,
	} {
		_, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", other+`,"cursor":"`+page.NextCursor+`"}`, false)
		if err == nil {
			t.Errorf("a cursor of other filters was accepted with %s", other)
		}
	}
}

// A request without the filters keeps the query and the fingerprint it had before them, so older cursors stay
// valid.
func TestLineCommentRequestWithoutFiltersIsUnchanged(t *testing.T) {
	a := &reviewCommentArguments{Number: 7}
	if err := checkReviewCommentsListArguments(a, target{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.binding, fingerprint("pullrequestreviewcomments", "list", target{}.String(), 7)) {
		t.Error("the fingerprint of an unfiltered request changed")
	}
	if got := a.query().Encode(); got != "page=1&per_page=30" {
		t.Errorf("query = %q", got)
	}
}

func TestLineCommentFiltersAreRefusedBeforeACredentialIsRead(t *testing.T) {
	f, base := serveWidthReviews(t, `[]`, `[]`, false)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)
	for _, arguments := range []string{
		`{"number":7,"sort":"created_at"}`,
		`{"number":7,"direction":"up"}`,
		`{"number":7,"since":"2026-01-01"}`,
		`{"number":7,"since":"yesterday"}`,
	} {
		if _, err := invoke(t, core, pullRequestReviewCommentsList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("a refused filter read %d credentials and sent %d requests", reads, len(f.recorded()))
	}
}
