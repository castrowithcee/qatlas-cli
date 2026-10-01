package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeDiscussions answers the discussion GraphQL queries of the bound repository through the failure hook of
// fakeGitHub. Discussion 12 exists; every other number does not. missing makes the repository unknown and
// forbidden refuses every discussion query like GitHub does for a token without access.
type fakeDiscussions struct {
	f         *fakeGitHub
	forbidden bool
}

const fakeDiscussionNode = `{"id":"D_12","number":12,"title":"How do I start?","body":"BODY","author":{"login":"octocat"},` +
	`"category":{"id":"DIC_cat1","name":"Q&A","slug":"q-a"},"closed":false,"locked":false,"isAnswered":true,` +
	`"answerChosenAt":"2026-01-02T00:00:00Z","upvoteCount":3,"comments":{"totalCount":2},` +
	`"labels":{"nodes":[{"name":"help"}]},"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z",` +
	`"url":"https://github.com/octo-org/example/discussions/12"}`

func (d *fakeDiscussions) route(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path != "/api/graphql" {
		return false
	}
	requests := d.f.recorded()
	last := requests[len(requests)-1]
	if !strings.Contains(strings.ToLower(last.document), "discussion") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if d.forbidden {
		fmt.Fprint(w, `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","path":["repository"],`+
			`"message":"Resource not accessible by personal access token"}]}`)
		return true
	}
	switch {
	case strings.Contains(last.document, "mutation"):
		return d.mutation(w, last)
	case strings.Contains(last.document, "on DiscussionComment"):
		id, _ := last.variables["id"].(string)
		repo, number, reply := `{"name":"example","owner":{"login":"octo-org"}}`, 12, "null"
		switch id {
		case "DC_top":
		case "DC_reply":
			reply = `{"id":"DC_top"}`
		case "DC_other":
			number = 13
		case "DC_foreign":
			repo = `{"name":"elsewhere","owner":{"login":"someone"}}`
		default:
			fmt.Fprint(w, `{"data":{"node":null}}`)
			return true
		}
		fmt.Fprintf(w, `{"data":{"node":{"__typename":"DiscussionComment","id":%q,"replyTo":%s,`+
			`"discussion":{"number":%d,"repository":%s}}}}`, id, reply, number, repo)
	case strings.Contains(last.document, "on DiscussionCategory"):
		repo := `{"name":"example","owner":{"login":"octo-org"}}`
		switch last.variables["id"] {
		case "DIC_cat1":
		case "DIC_foreign":
			repo = `{"name":"elsewhere","owner":{"login":"someone"}}`
		default:
			fmt.Fprint(w, `{"data":{"repository":{"id":"R_1"},"node":null}}`)
			return true
		}
		fmt.Fprintf(w, `{"data":{"repository":{"id":"R_1"},"node":{"__typename":"DiscussionCategory",`+
			`"id":"x","repository":%s}}}`, repo)
	case strings.Contains(last.document, "discussionCategories("):
		fmt.Fprint(w, `{"data":{"repository":{"discussionCategories":{"pageInfo":{"hasNextPage":true,`+
			`"endCursor":"CUR1"},"nodes":[{"id":"DIC_cat1","name":"Q&A","slug":"q-a","description":"Ask",`+
			`"emoji":":pray:","isAnswerable":true,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}}}}`)
	case strings.Contains(last.document, "discussions(first"):
		body := strings.Repeat("x", discussionListBodyLimit+50)
		fmt.Fprintf(w, `{"data":{"repository":{"discussions":{"pageInfo":{"hasNextPage":true,"endCursor":"CUR2"},`+
			`"nodes":[%s]}}}}`, strings.Replace(fakeDiscussionNode, "BODY", body, 1))
	case strings.Contains(last.document, "discussion(number:$number){comments"):
		if last.variables["number"] != float64(12) {
			fmt.Fprint(w, `{"data":{"repository":{"discussion":null}}}`)
			return true
		}
		replies := `{"totalCount":4,"pageInfo":{"hasNextPage":false}}`
		if last.variables["withReplies"] == true {
			replies = `{"totalCount":150,"pageInfo":{"hasNextPage":true},"nodes":[{"id":"DC_r1","databaseId":78,` +
				`"author":{"login":"mona"},"body":"REPLYBODY","isAnswer":false,"upvoteCount":2,` +
				`"createdAt":"2026-01-02T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","url":"https://x/r"}]}`
			replies = strings.Replace(replies, "REPLYBODY", strings.Repeat("y", discussionCommentBodyLimit+5), 1)
		}
		fmt.Fprintf(w, `{"data":{"repository":{"discussion":{"comments":{"pageInfo":{"hasNextPage":true,`+
			`"endCursor":"CUR3"},"nodes":[{"id":"DC_1","databaseId":77,"author":{"login":"hubot"},`+
			`"body":"an answer","isAnswer":true,"upvoteCount":1,"replies":%s,`+
			`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",`+
			`"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-77"}]}}}}}`, replies)
	case strings.Contains(last.document, "discussion(number:$number)"):
		if last.variables["number"] != float64(12) {
			fmt.Fprint(w, `{"data":{"repository":{"discussion":null}}}`)
			return true
		}
		fmt.Fprintf(w, `{"data":{"repository":{"discussion":%s}}}`, strings.Replace(fakeDiscussionNode, "BODY", "the body", 1))
	default:
		return false
	}
	return true
}

func discussionCore(t *testing.T, d *fakeDiscussions, reads *int) *application.Core {
	t.Helper()
	f := &fakeGitHub{}
	d.f = f
	f.failure = d.route
	red := &redact.Redactor{}
	return application.New(registry(t), coreConfig(serve(t, f)), resolver(red, reads), red)
}

func TestDiscussionCategoriesListThroughTheApplicationCore(t *testing.T) {
	core := discussionCore(t, &fakeDiscussions{}, nil)
	result, err := invoke(t, core, discussionCategoriesList.ID, "repo", `{"limit":5}`, false)
	if err != nil || !strings.Contains(string(result), `"id":"DIC_cat1"`) ||
		!strings.Contains(string(result), `"is_answerable":true`) || !strings.Contains(string(result), `"has_more":true`) ||
		!strings.Contains(string(result), `"next_cursor"`) || !strings.Contains(string(result), `"repository":"octo-org/example"`) {
		t.Fatalf("%s = %s, %v", discussionCategoriesList.ID, result, err)
	}
}

func TestDiscussionsListFiltersTruncatesAndBindsItsCursor(t *testing.T) {
	d := &fakeDiscussions{}
	core := discussionCore(t, d, nil)
	result, err := invoke(t, core, discussionsList.ID, "repo",
		`{"category":"DIC_cat1","state":"open","answered":true,"order_by":"updated_at","direction":"asc"}`, false)
	if err != nil || !strings.Contains(string(result), `"number":12`) || !strings.Contains(string(result), `"body_truncated":true`) ||
		!strings.Contains(string(result), `"answered":true`) || !strings.Contains(string(result), `"slug":"q-a"`) {
		t.Fatalf("%s = %s, %v", discussionsList.ID, result, err)
	}
	last := d.f.recorded()
	variables := last[len(last)-1].variables
	if variables["category"] != "DIC_cat1" || variables["answered"] != true ||
		fmt.Sprint(variables["states"]) != "[OPEN]" || fmt.Sprint(variables["order"]) != "map[direction:ASC field:UPDATED_AT]" {
		t.Errorf("variables = %v, want the filters passed on", variables)
	}
	if strings.Contains(string(result), strings.Repeat("x", discussionListBodyLimit+1)) {
		t.Errorf("the body was not cut")
	}

	cursor := extractCursor(t, result)
	// The same filters continue; another filter refuses the cursor before any request.
	if _, err := invoke(t, core, discussionsList.ID, "repo",
		`{"category":"DIC_cat1","state":"open","answered":true,"order_by":"updated_at","direction":"asc","cursor":"`+cursor+`"}`,
		false); err != nil {
		t.Errorf("continuing = %v", err)
	}
	before := len(d.f.recorded())
	_, err = invoke(t, core, discussionsList.ID, "repo", `{"category":"DIC_other","cursor":"`+cursor+`"}`, false)
	if !isInvalidRequest(err) || len(d.f.recorded()) != before {
		t.Errorf("a cursor of another filter = %v, want invalid-request before any request", err)
	}
}

func extractCursor(t *testing.T, result []byte) string {
	t.Helper()
	_, rest, ok := strings.Cut(string(result), `"next_cursor":"`)
	if !ok {
		t.Fatalf("no next_cursor in %s", result)
	}
	cursor, _, _ := strings.Cut(rest, `"`)
	return cursor
}

func TestDiscussionsGetAndItsNotFound(t *testing.T) {
	core := discussionCore(t, &fakeDiscussions{}, nil)
	result, err := invoke(t, core, discussionsGet.ID, "repo", `{"number":12}`, false)
	if err != nil || !strings.Contains(string(result), `"body":"the body"`) ||
		!strings.Contains(string(result), `"labels":["help"]`) || !strings.Contains(string(result), `"comment_count":2`) {
		t.Fatalf("%s = %s, %v", discussionsGet.ID, result, err)
	}
	_, err = invoke(t, core, discussionsGet.ID, "repo", `{"number":99}`, false)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "discussion #99") {
		t.Errorf("unknown discussion = %v, want not-found naming discussion #99", err)
	}
}

func TestDiscussionCommentsListBindsItsCursorToTheDiscussion(t *testing.T) {
	d := &fakeDiscussions{}
	core := discussionCore(t, d, nil)
	result, err := invoke(t, core, discussionCommentsList.ID, "repo", `{"number":12}`, false)
	if err != nil || !strings.Contains(string(result), `"id":"DC_1"`) || !strings.Contains(string(result), `"reply_count":4`) ||
		!strings.Contains(string(result), `"is_answer":true`) {
		t.Fatalf("%s = %s, %v", discussionCommentsList.ID, result, err)
	}
	cursor := extractCursor(t, result)
	before := len(d.f.recorded())
	_, err = invoke(t, core, discussionCommentsList.ID, "repo", `{"number":13,"cursor":"`+cursor+`"}`, false)
	if !isInvalidRequest(err) || len(d.f.recorded()) != before {
		t.Errorf("a cursor of another discussion = %v, want invalid-request before any request", err)
	}
	_, err = invoke(t, core, discussionCommentsList.ID, "repo", `{"number":99}`, false)
	if classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown discussion = %v, want not-found", err)
	}
}

func TestDiscussionToolsReportThePermissionTheyNeed(t *testing.T) {
	core := discussionCore(t, &fakeDiscussions{forbidden: true}, nil)
	for id, args := range map[string]string{discussionCategoriesList.ID: `{}`, discussionsList.ID: `{}`,
		discussionsGet.ID: `{"number":12}`, discussionCommentsList.ID: `{"number":12}`} {
		_, err := invoke(t, core, id, "repo", args, false)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Discussions: read") {
			t.Errorf("%s = %v, want a permission refusal naming Discussions: read", id, err)
		}
	}
}

// A repository outside the allow-list, an unknown argument value, or a malformed cursor is refused before a
// secret is read and before GitHub is contacted.
func TestDiscussionToolsRefuseBeforeIO(t *testing.T) {
	d := &fakeDiscussions{}
	reads := 0
	core := discussionCore(t, d, &reads)
	for _, tt := range []struct{ id, args string }{
		{discussionCategoriesList.ID, `{"repository":"other/repo"}`},
		{discussionsList.ID, `{"repository":"other/repo"}`},
		{discussionsGet.ID, `{"repository":"other/repo","number":1}`},
		{discussionCommentsList.ID, `{"repository":"other/repo","number":1}`},
		{discussionsList.ID, `{"category":"bad id!"}`},
		{discussionsList.ID, `{"cursor":"not-a-cursor"}`},
		{discussionsGet.ID, `{"number":0}`},
	} {
		reads = 0
		before := len(d.f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.args, false); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want invalid-request", tt.id, tt.args, err)
		}
		if reads != 0 || len(d.f.recorded()) != before {
			t.Errorf("%s %s reached the credential or GitHub", tt.id, tt.args)
		}
	}
}

func (d *fakeDiscussions) mutation(w http.ResponseWriter, last recorded) bool {
	switch {
	case strings.Contains(last.document, "createDiscussion("):
		fmt.Fprint(w, `{"data":{"createDiscussion":{"discussion":{"id":"D_new","number":40,"url":"https://x/d/40"}}}}`)
	case strings.Contains(last.document, "deleteDiscussionComment("):
		fmt.Fprint(w, `{"data":{"deleteDiscussionComment":{"comment":{"id":"DC_top"}}}}`)
	default:
		fmt.Fprint(w, `{"data":{"addDiscussionComment":{"comment":{"id":"DC_new","databaseId":90,`+
			`"author":{"login":"me"},"createdAt":"2026-01-03T00:00:00Z","updatedAt":"2026-01-03T00:00:00Z",`+
			`"url":"https://x/c"}},"updateDiscussionComment":{"comment":{"id":"DC_top","databaseId":77,`+
			`"createdAt":"2026-01-03T00:00:00Z","updatedAt":"2026-01-03T00:00:00Z","url":"https://x/c"}}}}`)
	}
	return true
}

func TestDiscussionCommentsListWithRepliesIsBounded(t *testing.T) {
	core := discussionCore(t, &fakeDiscussions{}, nil)
	plain, err := invoke(t, core, discussionCommentsList.ID, "repo", `{"number":12}`, false)
	if err != nil || strings.Contains(string(plain), `"replies":`) {
		t.Fatalf("without include_replies = %s, %v", plain, err)
	}
	result, err := invoke(t, core, discussionCommentsList.ID, "repo", `{"number":12,"include_replies":true}`, false)
	if err != nil || !strings.Contains(string(result), `"id":"DC_r1"`) ||
		!strings.Contains(string(result), `"replies_truncated":true`) || !strings.Contains(string(result), `"reply_count":150`) ||
		!strings.Contains(string(result), `"body_truncated":true`) ||
		strings.Contains(string(result), strings.Repeat("y", discussionCommentBodyLimit+1)) {
		t.Fatalf("with include_replies = %.300s, %v", result, err)
	}
	// A cursor of the plain list does not continue the list with replies.
	if _, err := invoke(t, core, discussionCommentsList.ID, "repo",
		`{"number":12,"include_replies":true,"cursor":"`+extractCursor(t, plain)+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("cursor of another shape = %v, want invalid-request", err)
	}
}

func discussionWriteConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget, Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{discussionCommentsDelete.ID}}
	return cfg
}

func TestDiscussionWriteToolsBindObjectsAndConfirm(t *testing.T) {
	d := &fakeDiscussions{}
	f := &fakeGitHub{}
	d.f = f
	f.failure = d.route
	red := &redact.Redactor{}
	reads := 0
	core := application.New(registry(t), discussionWriteConfig(serve(t, f)), resolver(red, &reads), red)

	mutations := func() int {
		n := 0
		for _, r := range f.recorded() {
			if strings.Contains(r.document, "mutation") {
				n++
			}
		}
		return n
	}
	// Without --confirm nothing reaches GitHub.
	unconfirmed := &application.ConfirmationRequiredError{}
	before := len(f.recorded())
	if _, err := invoke(t, core, discussionCommentsCreate.ID, "repo", `{"number":12,"body":"hi"}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("create without --confirm = %v, want confirmation-required", err)
	}
	if len(f.recorded()) != before {
		t.Error("an unconfirmed change reached GitHub")
	}

	created, err := invoke(t, core, discussionCommentsCreate.ID, "repo", `{"number":12,"body":"secret body text"}`, true)
	if err != nil || !strings.Contains(string(created), `"id":"DC_new"`) || strings.Contains(string(created), "secret body text") {
		t.Fatalf("create = %s, %v", created, err)
	}
	if _, err := invoke(t, core, discussionCommentsCreate.ID, "repo", `{"number":12,"body":"r","reply_to":"DC_top"}`, true); err != nil {
		t.Errorf("reply to a top-level comment = %v", err)
	}
	if _, err := invoke(t, core, discussionCommentsUpdate.ID, "repo", `{"comment_id":"DC_reply","body":"edit"}`, true); err != nil {
		t.Errorf("update = %v", err)
	}
	if out, err := invoke(t, core, discussionsCreate.ID, "repo", `{"title":"T","body":"B","category_id":"DIC_cat1"}`, true); err != nil ||
		!strings.Contains(string(out), `"number":40`) {
		t.Errorf("discussions.create = %s, %v", out, err)
	}

	// Foreign or unsuitable objects are refused without a mutation.
	before = mutations()
	for _, tt := range []struct {
		id, args string
		class    string
	}{
		{discussionCommentsCreate.ID, `{"number":12,"body":"r","reply_to":"DC_reply"}`, "invalid"},
		{discussionCommentsCreate.ID, `{"number":12,"body":"r","reply_to":"DC_other"}`, "invalid"},
		{discussionCommentsCreate.ID, `{"number":12,"body":"r","reply_to":"DC_foreign"}`, "notfound"},
		{discussionCommentsUpdate.ID, `{"comment_id":"DC_foreign","body":"x"}`, "notfound"},
		{discussionCommentsUpdate.ID, `{"comment_id":"DC_unknown","body":"x"}`, "notfound"},
		{discussionsCreate.ID, `{"title":"T","body":"B","category_id":"DIC_foreign"}`, "notfound"},
		{discussionsCreate.ID, `{"title":"T","body":"B","category_id":"DIC_unknown"}`, "notfound"},
	} {
		_, err := invoke(t, core, tt.id, "repo", tt.args, true)
		if (tt.class == "invalid" && !isInvalidRequest(err)) || (tt.class == "notfound" && classOf(err) != provider.ClassNotFound) {
			t.Errorf("%s %s = %v, want %s", tt.id, tt.args, err, tt.class)
		}
	}
	if mutations() != before {
		t.Error("a refused change was sent to GitHub")
	}

	// delete is offered only where a connection lists it.
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, discussionCommentsDelete.ID, "repo", `{"comment_id":"DC_top"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("delete without a tools list = %v, want unsupported-capability", err)
	}
	if _, err := invoke(t, core, discussionCommentsDelete.ID, "repo-listed", `{"comment_id":"DC_top"}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("delete without --confirm = %v, want confirmation-required", err)
	}
	if _, err := invoke(t, core, discussionCommentsDelete.ID, "repo-listed", `{"comment_id":"DC_foreign"}`, true); classOf(err) != provider.ClassNotFound {
		t.Errorf("delete of a foreign comment = %v, want not-found", err)
	}
	if out, err := invoke(t, core, discussionCommentsDelete.ID, "repo-listed", `{"comment_id":"DC_top"}`, true); err != nil ||
		!strings.Contains(string(out), `"deleted":true`) {
		t.Errorf("delete = %s, %v", out, err)
	}

	// A repository outside the allow-list or a malformed id is refused before any secret read.
	reads = 0
	before = len(f.recorded())
	for _, tt := range []struct{ id, args string }{
		{discussionCommentsCreate.ID, `{"repository":"other/repo","number":1,"body":"x"}`},
		{discussionsCreate.ID, `{"repository":"other/repo","title":"T","body":"B","category_id":"DIC_cat1"}`},
		{discussionCommentsUpdate.ID, `{"comment_id":"bad id!","body":"x"}`},
		{discussionsCreate.ID, `{"title":"T","body":"B","category_id":"!"}`},
	} {
		if _, err := invoke(t, core, tt.id, "repo", tt.args, true); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want invalid-request", tt.id, tt.args, err)
		}
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("a refused request reached the credential or GitHub")
	}
}
