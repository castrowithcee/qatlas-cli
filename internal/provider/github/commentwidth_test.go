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

const widthUser = `{"__typename":"User","login":"octocat","id":"U_1","databaseId":1,"name":"Mona Octocat",` +
	`"email":"mona@example.com","avatarUrl":"https://avatars.example.com/u/1","url":"https://github.com/octocat",` +
	`"resourcePath":"/octocat","isSiteAdmin":false}`

const widthUserView = `{"login":"octocat","type":"User","id":"U_1","database_id":1,"name":"Mona Octocat",` +
	`"email":"mona@example.com","avatar_url":"https://avatars.example.com/u/1","url":"https://github.com/octocat",` +
	`"links":{"resource_path":"/octocat"},"is_site_admin":false}`

const widthBot = `{"__typename":"Bot","login":"helper","id":"B_1","databaseId":7,"avatarUrl":"https://avatars.example.com/b/7",` +
	`"url":"https://github.com/apps/helper","resourcePath":"/apps/helper"}`

const widthBotView = `{"login":"helper","type":"Bot","id":"B_1","database_id":7,"avatar_url":"https://avatars.example.com/b/7",` +
	`"url":"https://github.com/apps/helper","links":{"resource_path":"/apps/helper"}}`

// widthComment is an IssueComment as the GraphQL schema describes it, with every member set.
const widthComment = `{"id":"IC_1","databaseId":501,"fullDatabaseId":"9007199254740993","author":` + widthUser + `,` +
	`"authorAssociation":"MEMBER","body":"text","createdAt":"2026-01-01T00:00:00Z","createdViaEmail":true,` +
	`"editor":` + widthBot + `,"includesCreatedEdit":false,"isMinimized":true,"isPinned":true,` +
	`"lastEditedAt":"2026-01-02T00:00:00Z","minimizedReason":"off-topic","pinnedAt":"2026-01-03T00:00:00Z",` +
	`"pinnedBy":` + widthUser + `,"publishedAt":"2026-01-01T00:00:01Z","reactionGroups":[` +
	`{"content":"THUMBS_UP","createdAt":"2026-01-04T00:00:00Z","viewerHasReacted":true,"reactors":{"totalCount":3}},` +
	`{"content":"HEART","createdAt":null,"viewerHasReacted":false,"reactors":{"totalCount":0}}],` +
	`"reactions":{"totalCount":3},"resourcePath":"/octo-org/example/issues/7#issuecomment-501",` +
	`"updatedAt":"2026-01-02T00:00:00Z","url":"https://github.com/octo-org/example/issues/7#issuecomment-501",` +
	`"userContentEdits":{"totalCount":2},"viewerCanDelete":true,"viewerCanMinimize":false,"viewerCanPin":true,` +
	`"viewerCanReact":true,"viewerCanUnminimize":false,"viewerCanUnpin":true,"viewerCanUpdate":false,` +
	`"viewerCannotUpdateReasons":["LOCKED","VERIFIED_EMAIL_REQUIRED"],"viewerDidAuthor":false}`

const widthCommentView = `{"id":"IC_1","database_id":501,"author":"octocat","body":"text",` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
	`"url":"https://github.com/octo-org/example/issues/7#issuecomment-501",` +
	`"author_detail":` + widthUserView + `,"author_association":"MEMBER","editor":` + widthBotView + `,` +
	`"last_edited_at":"2026-01-02T00:00:00Z","published_at":"2026-01-01T00:00:01Z","created_via_email":true,` +
	`"includes_created_edit":false,"full_database_id":"9007199254740993","is_minimized":true,` +
	`"minimized_reason":"off-topic","is_pinned":true,"pinned_at":"2026-01-03T00:00:00Z","pinned_by":` + widthUserView + `,` +
	`"reaction_groups":[{"content":"THUMBS_UP","created_at":"2026-01-04T00:00:00Z","viewer_has_reacted":true,"count":3}],` +
	`"links":{"resource_path":"/octo-org/example/issues/7#issuecomment-501"},"viewer_can_delete":true,` +
	`"viewer_can_minimize":false,"viewer_can_pin":true,"viewer_can_react":true,"viewer_can_unminimize":false,` +
	`"viewer_can_unpin":true,"viewer_can_update":false,"viewer_cannot_update_reasons":["LOCKED","VERIFIED_EMAIL_REQUIRED"],` +
	`"viewer_did_author":false,"reactions_total_count":3,"user_content_edits_total_count":2}`

// widthCommentOnly is the answer a comment gave before the width: the compact members alone.
const widthCommentOnly = `{"id":"IC_2","databaseId":502,"author":{"login":"octocat"},"body":"plain",` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","url":"https://github.com/o/r/issues/7#issuecomment-502"}`

// fakeCommentWidth answers the issue and the pull request comment queries with one fixed list.
func fakeCommentWidth(f *fakeGitHub, nodes string) {
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		last := f.recorded()
		document := last[len(last)-1].document
		page := `{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[` + nodes + `]}`
		switch {
		case strings.Contains(document, "issueOrPullRequest(number"):
			fmt.Fprintf(w, `{"data":{"repository":{"issueOrPullRequest":{"__typename":"Issue","comments":%s}}}}`, page)
		case strings.Contains(document, "pullRequest(number:$number){comments(first"):
			fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"comments":%s}}}}`, page)
		default:
			return false
		}
		return true
	}
}

// The comment lists carry every member of GitHub's IssueComment except the backlinks and the lists GitHub
// serves on their own, and the query asks for exactly the members the output carries.
func TestCommentListsCarryTheFullComment(t *testing.T) {
	f := &fakeGitHub{}
	fakeCommentWidth(f, widthComment+","+widthCommentOnly)
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, tc := range []struct {
		tool, arguments, query string
	}{
		{commentsList.ID, `{"number":7}`, commentsQuery},
		{pullRequestCommentsList.ID, `{"number":7}`, pullRequestCommentsQuery},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := invoke(t, core, tc.tool, "repo", tc.arguments, false)
			if err != nil {
				t.Fatal(err)
			}
			assertJSON(t, json.RawMessage(result), `{"comments":[`+widthCommentView+`,`+
				`{"id":"IC_2","database_id":502,"author":"octocat","body":"plain","created_at":"2026-01-01T00:00:00Z",`+
				`"updated_at":"2026-01-01T00:00:00Z","url":"https://github.com/o/r/issues/7#issuecomment-502",`+
				`"author_detail":{"login":"octocat"}}],"has_more":false,"repository":"octo-org/example"}`)
			var descriptor = commentsList
			if tc.tool == pullRequestCommentsList.ID {
				descriptor = pullRequestCommentsList
			}
			first, _ := json.Marshal(decodeAny(t, result).(map[string]any)["comments"].([]any)[0])
			assertSelectionMatchesOutput(t, commentSelection, first, entrySchema(t, descriptor.OutputSchema, "comments"),
				[]string{"author_detail", "links", "type", "count", "author", "viewer_has_reacted", "reactions_total_count",
					"user_content_edits_total_count"},
				[]string{"reactions", "userContentEdits", "totalCount", "reactors"})
			if !strings.Contains(tc.query, commentSelection) {
				t.Errorf("%s query lacks the comment selection", tc.tool)
			}
			for _, banned := range []string{"bodyHTML", "bodyText", "issue{", "pullRequest{", "repository{"} {
				if strings.Contains(commentSelection, banned) {
					t.Errorf("selection asks for %s", banned)
				}
			}
		})
	}
}
