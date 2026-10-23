package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// widthThreadComment is a PullRequestReviewComment as the GraphQL schema describes it, with every member set.
const widthThreadComment = `{"id":"PRRC_1","author":` + widthUser + `,"authorAssociation":"MEMBER","body":"Looks wrong",` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","publishedAt":"2026-01-01T00:00:01Z",` +
	`"draftedAt":"2025-12-31T23:59:00Z","lastEditedAt":"2026-01-02T00:00:00Z","editor":` + widthBot + `,` +
	`"createdViaEmail":true,"includesCreatedEdit":false,"fullDatabaseId":"9007199254740993",` +
	`"diffHunk":"@@ -1,3 +1,4 @@\n line","path":"main.go","line":12,"originalLine":10,"startLine":11,` +
	`"originalStartLine":9,"outdated":true,"state":"SUBMITTED","subjectType":"LINE",` +
	`"commit":{"oid":"6dcb09b5b57875f334f61aebed695e2e4193db5e","url":"https://github.com/octo-org/example/commit/6dcb09b5"},` +
	`"originalCommit":{"oid":"9c48853fa3dc5c1c3d6f1f1cd1f2743e4c2e5e8f","url":"https://github.com/octo-org/example/commit/9c48853f"},` +
	`"pullRequestReview":{"id":"PRR_1","fullDatabaseId":"9007199254740994","state":"COMMENTED"},` +
	`"replyTo":{"id":"PRRC_0"},"isMinimized":true,"minimizedReason":"OUTDATED","reactionGroups":[` +
	`{"content":"THUMBS_UP","createdAt":"2026-01-04T00:00:00Z","viewerHasReacted":true,"reactors":{"totalCount":3}},` +
	`{"content":"HEART","createdAt":null,"viewerHasReacted":false,"reactors":{"totalCount":0}}],` +
	`"reactions":{"totalCount":3},"url":"https://github.com/octo-org/example/pull/7#discussion_r1",` +
	`"resourcePath":"/octo-org/example/pull/7#discussion_r1","userContentEdits":{"totalCount":2},` +
	`"viewerCanDelete":true,"viewerCanMinimize":false,"viewerCanReact":true,"viewerCanUnminimize":false,` +
	`"viewerCanUpdate":false,"viewerCannotUpdateReasons":["LOCKED"],"viewerDidAuthor":false}`

const widthThreadCommentView = `{"id":"PRRC_1","author":"octocat","body":"Looks wrong","author_detail":` + widthUserView + `,` +
	`"author_association":"MEMBER","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
	`"published_at":"2026-01-01T00:00:01Z","drafted_at":"2025-12-31T23:59:00Z","last_edited_at":"2026-01-02T00:00:00Z",` +
	`"editor":` + widthBotView + `,"created_via_email":true,"includes_created_edit":false,` +
	`"full_database_id":"9007199254740993","diff_hunk":"@@ -1,3 +1,4 @@\n line","path":"main.go","line":12,` +
	`"original_line":10,"start_line":11,"original_start_line":9,"outdated":true,"state":"SUBMITTED",` +
	`"subject_type":"LINE","commit":{"oid":"6dcb09b5b57875f334f61aebed695e2e4193db5e",` +
	`"url":"https://github.com/octo-org/example/commit/6dcb09b5"},"original_commit":{` +
	`"oid":"9c48853fa3dc5c1c3d6f1f1cd1f2743e4c2e5e8f","url":"https://github.com/octo-org/example/commit/9c48853f"},` +
	`"pull_request_review":{"id":"PRR_1","full_database_id":"9007199254740994","state":"COMMENTED"},` +
	`"reply_to":"PRRC_0","is_minimized":true,"minimized_reason":"OUTDATED","reaction_groups":[` +
	`{"content":"THUMBS_UP","created_at":"2026-01-04T00:00:00Z","viewer_has_reacted":true,"count":3}],` +
	`"url":"https://github.com/octo-org/example/pull/7#discussion_r1",` +
	`"links":{"resource_path":"/octo-org/example/pull/7#discussion_r1"},"viewer_can_delete":true,` +
	`"viewer_can_minimize":false,"viewer_can_react":true,"viewer_can_unminimize":false,"viewer_can_update":false,` +
	`"viewer_cannot_update_reasons":["LOCKED"],"viewer_did_author":false,"reactions_total_count":3,` +
	`"user_content_edits_total_count":2}`

// widthThread is a PullRequestReviewThread as the GraphQL schema describes it, with every member set and a
// second page of comments GitHub holds back.
const widthThread = `{"id":"PRRT_1","isResolved":true,"isOutdated":true,"path":"main.go","line":12,` +
	`"diffSide":"RIGHT","startDiffSide":"RIGHT","startLine":11,"originalLine":10,"originalStartLine":9,` +
	`"subjectType":"LINE","isCollapsed":true,"resolvedBy":` + widthUser + `,"viewerCanReply":true,` +
	`"viewerCanResolve":false,"viewerCanUnresolve":true,"comments":{"totalCount":51,"nodes":[` +
	widthThreadComment + `]}}`

const widthThreadView = `{"id":"PRRT_1","is_resolved":true,"is_outdated":true,"path":"main.go","line":12,` +
	`"comments":[` + widthThreadCommentView + `],"diff_side":"RIGHT","start_diff_side":"RIGHT","start_line":11,` +
	`"original_line":10,"original_start_line":9,"subject_type":"LINE","is_collapsed":true,"resolved_by":` +
	widthUserView + `,"viewer_can_reply":true,"viewer_can_resolve":false,"viewer_can_unresolve":true,` +
	`"comments_total_count":51,"comments_truncated":true}`

func serveWidthThreads(t *testing.T, nodes string) (*fakeGitHub, string) {
	t.Helper()
	f, _, base := serveReviews(t)
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,`+
			`"endCursor":""},"nodes":[%s]}}}}}`, nodes)
		return true
	}
	return f, base
}

func TestReviewThreadListCarriesTheFullThread(t *testing.T) {
	f, base := serveWidthThreads(t, widthThread+`,{"id":"PRRT_2","isResolved":false,"isOutdated":false,`+
		`"comments":{"nodes":[{"id":"PRRC_2","author":{"login":"octocat"},"body":"plain"}]}}`)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	result, err := invoke(t, core, pullRequestReviewThreadsList.ID, "repo", `{"number":7}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(result), `{"threads":[`+widthThreadView+`,{"id":"PRRT_2","is_resolved":false,`+
		`"is_outdated":false,"comments":[{"id":"PRRC_2","author":"octocat","body":"plain",`+
		`"author_detail":{"login":"octocat"}}]}],"has_more":false,"repository":"octo-org/example"}`)

	// The query asks for exactly the members the output carries, and one request answers the call.
	thread := decodeAny(t, result).(map[string]any)["threads"].([]any)[0].(map[string]any)
	checkSchemaCovers(t, "$.threads[]", thread, decodeAny(t, entrySchema(t, pullRequestReviewThreadsList.OutputSchema,
		"threads")).(map[string]any))
	first, _ := json.Marshal(thread)
	assertSelectionMatchesOutput(t, threadSelection, first, entrySchema(t, pullRequestReviewThreadsList.OutputSchema, "threads"),
		[]string{"is_resolved", "is_outdated", "comments_truncated", "author", "author_detail", "links", "type", "count",
			"reaction_groups", "viewer_has_reacted", "reactions_total_count", "user_content_edits_total_count",
			"comments_total_count", "database_id", "created_at"},
		[]string{"isResolved", "isOutdated", "reactions", "userContentEdits", "totalCount", "reactors", "resourcePath",
			"avatarUrl", "login", "createdAt", "reactionGroups", "oid", "first", "nodes"})
	requests := f.recorded()
	if len(requests) != 1 {
		t.Fatalf("sent %d requests, want 1", len(requests))
	}
	document := requests[0].document
	for _, banned := range []string{"bodyHTML", "bodyText", "originalPosition", "position", "hovercard"} {
		if strings.Contains(document, banned) {
			t.Errorf("the query asks for %s", banned)
		}
	}
	if !strings.Contains(document, "comments(first:50)") || !strings.Contains(document, "resolvedBy{__typename id databaseId login") {
		t.Errorf("query = %s", document)
	}
	if strings.Contains(document, "resolvedBy{__typename login avatarUrl url resourcePath ...") {
		t.Error("resolvedBy is a User and must not use the Actor fragments")
	}
}

// The comment page stays at 50 and the threads at the page limit, far below GitHub's node limit.
func TestReviewThreadQueryStaysBelowTheNodeLimit(t *testing.T) {
	const nodeLimit = 500000
	nodes := 1 + maxLimit + maxLimit*threadCommentLimit
	if nodes > nodeLimit/10 || !strings.Contains(reviewThreadsQuery, fmt.Sprintf("comments(first:%d)", threadCommentLimit)) {
		t.Errorf("a full page asks for %d nodes", nodes)
	}
}
