package github

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
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
	if !strings.Contains(last.document, "discussion") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if d.forbidden {
		fmt.Fprint(w, `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","path":["repository"],`+
			`"message":"Resource not accessible by personal access token"}]}`)
		return true
	}
	switch {
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
		fmt.Fprint(w, `{"data":{"repository":{"discussion":{"comments":{"pageInfo":{"hasNextPage":true,`+
			`"endCursor":"CUR3"},"nodes":[{"id":"DC_1","databaseId":77,"author":{"login":"hubot"},`+
			`"body":"an answer","isAnswer":true,"upvoteCount":1,"replies":{"totalCount":4},`+
			`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",`+
			`"url":"https://github.com/octo-org/example/discussions/12#discussioncomment-77"}]}}}}}`)
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
