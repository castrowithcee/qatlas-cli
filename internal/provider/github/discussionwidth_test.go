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

const widthLabel = `{"id":"LA_1","name":"help","description":"Needs help","color":"008672","isDefault":true,` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z",` +
	`"url":"https://github.com/octo-org/example/labels/help","resourcePath":"/octo-org/example/labels/help"}`

const widthLabelView = `{"id":"LA_1","name":"help","description":"Needs help","color":"008672","is_default":true,` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
	`"url":"https://github.com/octo-org/example/labels/help","links":{"resource_path":"/octo-org/example/labels/help"}}`

const widthReactionGroups = `[{"content":"THUMBS_UP","createdAt":"2026-01-04T00:00:00Z","viewerHasReacted":true,` +
	`"reactors":{"totalCount":3}},{"content":"HEART","createdAt":null,"viewerHasReacted":false,"reactors":{"totalCount":0}}]`

const widthReactionGroupsView = `[{"content":"THUMBS_UP","created_at":"2026-01-04T00:00:00Z","viewer_has_reacted":true,"count":3}]`

// widthDiscussion is a Discussion as the GraphQL schema describes it, with every member set.
const widthDiscussion = `{"id":"D_12","number":12,"title":"How do I start?","body":"the body","author":` + widthUser + `,` +
	`"category":{"id":"DIC_1","name":"Q&A","slug":"q-a","description":"Ask away","emoji":":pray:","isAnswerable":true,` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"},"closed":true,"locked":true,` +
	`"isAnswered":true,"answerChosenAt":"2026-01-02T00:00:00Z","upvoteCount":3,"comments":{"totalCount":2},` +
	`"labels":{"totalCount":21,"pageInfo":{"hasNextPage":true},"nodes":[` + widthLabel + `]},` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","url":"https://github.com/octo-org/example/discussions/12",` +
	`"databaseId":4001,"authorAssociation":"MEMBER","editor":` + widthBot + `,"lastEditedAt":"2026-01-02T01:00:00Z",` +
	`"publishedAt":"2026-01-01T00:00:01Z","createdViaEmail":true,"includesCreatedEdit":false,` +
	`"activeLockReason":"RESOLVED","closedAt":"2026-01-03T00:00:00Z","stateReason":"RESOLVED",` +
	`"answer":{"id":"DC_a","databaseId":88,"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-88",` +
	`"author":` + widthBot + `,"createdAt":"2026-01-02T00:00:00Z","isAnswer":true},"answerChosenBy":` + widthUser + `,` +
	`"poll":{"id":"DP_1","question":"Which one?","totalVoteCount":5,"viewerCanVote":true,"viewerHasVoted":false,` +
	`"options":{"totalCount":11,"pageInfo":{"hasNextPage":true},"nodes":[` +
	`{"id":"DPO_1","option":"A","totalVoteCount":3,"viewerHasVoted":false},` +
	`{"id":"DPO_2","option":"B","totalVoteCount":0,"viewerHasVoted":true}]}},` +
	`"reactionGroups":` + widthReactionGroups + `,"reactions":{"totalCount":3},"resourcePath":"/octo-org/example/discussions/12",` +
	`"userContentEdits":{"totalCount":2},"viewerCanClose":true,"viewerCanDelete":false,"viewerCanLabel":true,` +
	`"viewerCanReact":true,"viewerCanReopen":false,"viewerCanSubscribe":true,"viewerCanUpdate":false,` +
	`"viewerCanUpvote":true,"viewerDidAuthor":false,"viewerHasUpvoted":true,"viewerSubscription":"SUBSCRIBED"}`

const widthDiscussionView = `{"id":"D_12","number":12,"title":"How do I start?","body":"the body","author":"octocat",` +
	`"category":{"id":"DIC_1","name":"Q&A","slug":"q-a","description":"Ask away","emoji":":pray:","is_answerable":true,` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"},"closed":true,"locked":true,` +
	`"answered":true,"answer_chosen_at":"2026-01-02T00:00:00Z","upvote_count":3,"comment_count":2,"labels":["help"],` +
	`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","url":"https://github.com/octo-org/example/discussions/12",` +
	`"database_id":4001,"author_detail":` + widthUserView + `,"author_association":"MEMBER","editor":` + widthBotView + `,` +
	`"last_edited_at":"2026-01-02T01:00:00Z","published_at":"2026-01-01T00:00:01Z","created_via_email":true,` +
	`"includes_created_edit":false,"active_lock_reason":"RESOLVED","closed_at":"2026-01-03T00:00:00Z",` +
	`"state_reason":"RESOLVED","answer":{"id":"DC_a","database_id":88,` +
	`"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-88","author":` + widthBotView + `,` +
	`"created_at":"2026-01-02T00:00:00Z","is_answer":true},"answer_chosen_by":` + widthUserView + `,` +
	`"poll":{"id":"DP_1","question":"Which one?","total_vote_count":5,"viewer_can_vote":true,"viewer_has_voted":false,` +
	`"options":[{"id":"DPO_1","option":"A","total_vote_count":3,"viewer_has_voted":false},` +
	`{"id":"DPO_2","option":"B","total_vote_count":0,"viewer_has_voted":true}],"options_total_count":11,` +
	`"options_truncated":true},"labels_details":[` + widthLabelView + `],"labels_total_count":21,"labels_truncated":true,` +
	`"reaction_groups":` + widthReactionGroupsView + `,"links":{"resource_path":"/octo-org/example/discussions/12"},` +
	`"reactions_total_count":3,"user_content_edits_total_count":2,"viewer_can_close":true,"viewer_can_delete":false,` +
	`"viewer_can_label":true,"viewer_can_react":true,"viewer_can_reopen":false,"viewer_can_subscribe":true,` +
	`"viewer_can_update":false,"viewer_can_upvote":true,"viewer_did_author":false,"viewer_has_upvoted":true,` +
	`"viewer_subscription":"SUBSCRIBED"}`

// widthDiscussionComment is a DiscussionComment as the GraphQL schema describes it, with every member set.
func widthDiscussionComment(id string, replyTo string, replies string) string {
	return `{"id":"` + id + `","databaseId":77,"author":` + widthUser + `,"body":"text","isAnswer":false,"upvoteCount":1,` +
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z",` +
		`"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-77",` +
		`"authorAssociation":"MEMBER","createdViaEmail":true,"editor":` + widthBot + `,"includesCreatedEdit":false,` +
		`"isMinimized":true,"lastEditedAt":"2026-01-02T01:00:00Z","minimizedReason":"off-topic",` +
		`"publishedAt":"2026-01-01T00:00:01Z","deletedAt":"2026-01-05T00:00:00Z","replyTo":` + replyTo + `,` +
		`"reactionGroups":` + widthReactionGroups + `,"reactions":{"totalCount":3},` +
		`"resourcePath":"/octo-org/example/discussions/12#discussioncomment-77","userContentEdits":{"totalCount":2},` +
		`"viewerCanDelete":true,"viewerCanMarkAsAnswer":false,"viewerCanMinimize":true,"viewerCanReact":true,` +
		`"viewerCanUnmarkAsAnswer":false,"viewerCanUnminimize":false,"viewerCanUpdate":true,"viewerCanUpvote":true,` +
		`"viewerCannotUpdateReasons":["LOCKED","VERIFIED_EMAIL_REQUIRED"],"viewerDidAuthor":true,"viewerHasUpvoted":false` +
		replies + `}`
}

func widthDiscussionCommentView(id string, replyTo string, replies string) string {
	reply := ""
	if replyTo != "" {
		reply = `"reply_to":"` + replyTo + `",`
	}
	return `{"id":"` + id + `","database_id":77,"author":"octocat","body":"text","upvote_count":1,` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-77",` +
		`"author_detail":` + widthUserView + `,"author_association":"MEMBER","editor":` + widthBotView + `,` +
		`"last_edited_at":"2026-01-02T01:00:00Z","published_at":"2026-01-01T00:00:01Z","created_via_email":true,` +
		`"includes_created_edit":false,"deleted_at":"2026-01-05T00:00:00Z","is_minimized":true,` +
		`"minimized_reason":"off-topic",` + reply + `"reaction_groups":` + widthReactionGroupsView + `,` +
		`"links":{"resource_path":"/octo-org/example/discussions/12#discussioncomment-77"},` +
		`"reactions_total_count":3,"user_content_edits_total_count":2,"viewer_can_delete":true,` +
		`"viewer_can_mark_as_answer":false,"viewer_can_minimize":true,"viewer_can_react":true,` +
		`"viewer_can_unmark_as_answer":false,"viewer_can_unminimize":false,"viewer_can_update":true,` +
		`"viewer_can_upvote":true,"viewer_cannot_update_reasons":["LOCKED","VERIFIED_EMAIL_REQUIRED"],` +
		`"viewer_did_author":true,"viewer_has_upvoted":false` + replies + `}`
}

// fakeDiscussionWidth answers the discussion queries with the given discussion and comment nodes.
func fakeDiscussionWidth(f *fakeGitHub, discussion, comments string) {
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		last := f.recorded()
		document := last[len(last)-1].document
		switch {
		case strings.Contains(document, "discussion(number:$number){comments"):
			fmt.Fprintf(w, `{"data":{"repository":{"discussion":{"comments":{"pageInfo":{"hasNextPage":false,"endCursor":""},`+
				`"nodes":[%s]}}}}}`, comments)
		case strings.Contains(document, "discussion(number:$number)"):
			fmt.Fprintf(w, `{"data":{"repository":{"discussion":%s}}}`, discussion)
		case strings.Contains(document, "discussions(first"):
			fmt.Fprintf(w, `{"data":{"repository":{"discussions":{"pageInfo":{"hasNextPage":false,"endCursor":""},`+
				`"nodes":[%s]}}}}`, discussion)
		default:
			return false
		}
		return true
	}
}

func discussionWidthCore(t *testing.T, discussion, comments string) (*fakeGitHub, *application.Core) {
	t.Helper()
	f := &fakeGitHub{}
	fakeDiscussionWidth(f, discussion, comments)
	base := serve(t, f)
	red := &redact.Redactor{}
	return f, application.New(registry(t), coreConfig(base), resolver(red, nil), red)
}

// The discussion tools carry every member of GitHub's Discussion except the backlinks, the renderings, and
// the lists GitHub serves on their own, and the query asks for exactly the members the output carries.
func TestDiscussionToolsCarryTheFullDiscussion(t *testing.T) {
	f, core := discussionWidthCore(t, widthDiscussion, "")
	for _, tc := range []struct {
		tool, arguments, want string
		descriptor            string
		options               int
	}{
		{discussionsGet.ID, `{"number":12}`, `{"discussion":` + widthDiscussionView + `,"repository":"octo-org/example"}`,
			"discussion", discussionPollOptionsGet},
		{discussionsList.ID, `{}`, `{"discussions":[` + widthDiscussionView + `],"has_more":false,"repository":"octo-org/example"}`,
			"discussions", discussionPollOptionsList},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := invoke(t, core, tc.tool, "repo", tc.arguments, false)
			if err != nil {
				t.Fatal(err)
			}
			assertJSON(t, json.RawMessage(result), tc.want)
			last := f.recorded()
			if document := last[len(last)-1].document; !strings.Contains(document, discussionSelection(tc.options)) {
				t.Errorf("%s query lacks the discussion selection for %d options", tc.tool, tc.options)
			}
		})
	}

	var value map[string]any
	if err := json.Unmarshal([]byte(widthDiscussionView), &value); err != nil {
		t.Fatal(err)
	}
	entry, _ := json.Marshal(value)
	assertSelectionMatchesOutput(t, discussionSelection(10), entry, entrySchema(t, discussionsList.OutputSchema, "discussions"),
		[]string{"author_detail", "links", "type", "count", "labels_details", "answered", "comment_count",
			"labels_total_count", "labels_truncated", "options_total_count", "options_truncated",
			"reactions_total_count", "user_content_edits_total_count", "viewer_has_reacted", "is_answerable"},
		[]string{"reactions", "userContentEdits", "totalCount", "reactors", "pageInfo", "hasNextPage", "nodes",
			"comments", "isAnswered", "viewerHasReacted", "first"})
	for _, banned := range []string{"bodyHTML", "bodyText", "emojiHTML", "repository{", "discussion{"} {
		if strings.Contains(discussionSelection(10), banned) {
			t.Errorf("selection asks for %s", banned)
		}
	}
}

// A discussion answer without the new members keeps its earlier output.
func TestDiscussionWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	const old = `{"id":"D_1","number":1,"title":"T","body":"B","author":{"login":"octocat"},` +
		`"category":{"id":"DIC_1","name":"Q&A","slug":"q-a"},"closed":false,"locked":false,"isAnswered":false,` +
		`"upvoteCount":0,"comments":{"totalCount":0},"labels":{"nodes":[{"name":"help"}]},` +
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","url":"https://x/d/1"}`
	_, core := discussionWidthCore(t, old, "")
	result, err := invoke(t, core, discussionsGet.ID, "repo", `{"number":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, json.RawMessage(result), `{"discussion":{"id":"D_1","number":1,"title":"T","body":"B","author":"octocat",`+
		`"category":{"id":"DIC_1","name":"Q&A","slug":"q-a"},"closed":false,"locked":false,"answered":false,`+
		`"upvote_count":0,"comment_count":0,"labels":["help"],"created_at":"2026-01-01T00:00:00Z",`+
		`"updated_at":"2026-01-01T00:00:00Z","url":"https://x/d/1","author_detail":{"login":"octocat"},`+
		`"labels_details":[{"name":"help"}]},"repository":"octo-org/example"}`)
}

// The comment list carries every member of GitHub's DiscussionComment for a comment and for each reply.
func TestDiscussionCommentsCarryTheFullComment(t *testing.T) {
	reply := widthDiscussionComment("DC_r", `{"id":"DC_1"}`, "")
	top := widthDiscussionComment("DC_1", "null", `,"replies":{"totalCount":150,"pageInfo":{"hasNextPage":true},"nodes":[`+reply+`]}`)
	f, core := discussionWidthCore(t, "", top)
	result, err := invoke(t, core, discussionCommentsList.ID, "repo", `{"number":12,"include_replies":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	replyView := strings.Replace(widthDiscussionCommentView("DC_r", "DC_1", ""), `"upvote_count":1,`, `"upvote_count":1,`, 1)
	assertJSON(t, json.RawMessage(result), `{"comments":[`+widthDiscussionCommentView("DC_1", "",
		`,"reply_count":150,"replies":[`+replyView+`],"replies_truncated":true`)+`],"has_more":false,"repository":"octo-org/example"}`)

	last := f.recorded()
	if document := last[len(last)-1].document; strings.Count(document, discussionCommentSelection) != 2 {
		t.Errorf("the query lacks the comment selection for a comment and a reply")
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(widthDiscussionCommentView("DC_1", "DC_0", "")), &value); err != nil {
		t.Fatal(err)
	}
	entry, _ := json.Marshal(value)
	assertSelectionMatchesOutput(t, discussionCommentSelection, entry, entrySchema(t, discussionCommentsList.OutputSchema, "comments"),
		[]string{"author_detail", "links", "type", "count", "author", "viewer_has_reacted", "reactions_total_count",
			"user_content_edits_total_count", "reply_to", "upvote_count", "is_answer", "database_id"},
		[]string{"reactions", "userContentEdits", "totalCount", "reactors", "replyTo", "isAnswer", "databaseId",
			"upvoteCount", "viewerHasReacted"})
	for _, banned := range []string{"bodyHTML", "bodyText", "discussion{"} {
		if strings.Contains(discussionCommentSelection, banned) {
			t.Errorf("selection asks for %s", banned)
		}
	}
}

// Poll options and labels past their first page are counted and marked, never read.
func TestDiscussionPollAndLabelsAreCapped(t *testing.T) {
	if !connectionTruncated(nil, true, 3) || connectionTruncated(nil, false, 3) ||
		!connectionTruncated(ptr(int64(5)), false, 3) || connectionTruncated(ptr(int64(3)), false, 3) {
		t.Error("connectionTruncated does not follow hasNextPage and the total")
	}
	if !strings.Contains(discussionSelection(discussionPollOptionsGet), "options(first:20)") ||
		!strings.Contains(discussionSelection(discussionPollOptionsList), "options(first:10)") {
		t.Error("the poll options are not capped at 20 and 10")
	}
}

func ptr[T any](v T) *T { return &v }
