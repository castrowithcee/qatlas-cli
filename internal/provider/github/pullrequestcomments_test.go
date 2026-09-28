package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// fakePRComments answers the pull request conversation comment routes of pull request 7 through the
// failure hook of fakeGitHub, both the GraphQL list and the REST create, since github.pullrequestcomments.*
// shares the REST issue comment route github.comments.* uses but lists through its own GraphQL query.
type fakePRComments struct {
	f *fakeGitHub
}

func (p *fakePRComments) route(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path == "/api/graphql" {
		requests := p.f.recorded()
		if len(requests) == 0 {
			return false
		}
		last := requests[len(requests)-1]
		switch {
		case strings.Contains(last.document, "pullRequest(number:$number){id}"):
			if last.variables["number"] == float64(7) {
				fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"id":"PR_7"}}}}`)
			} else {
				fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}}}`)
			}
			return true
		case strings.Contains(last.document, "pullRequest(number:$number){comments(first"):
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"comments":{"pageInfo":`+
				`{"hasNextPage":false,"endCursor":""},"nodes":[{"id":"PRC_1","databaseId":501,`+
				`"author":{"login":"octocat"},"body":"a conversation comment",`+
				`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",`+
				`"url":"https://github.com/octo-org/example/pull/7#issuecomment-501"}]}}}}}`)
			return true
		}
		return false
	}
	if req.Method == http.MethodPost && req.URL.Path == actionsPrefix+"issues/7/comments" {
		fmt.Fprint(w, `{"id":502,"node_id":"PRC_new","user":{"login":"octocat"},"body":"a new reply",`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z",`+
			`"html_url":"https://github.com/octo-org/example/pull/7#issuecomment-502"}`)
		return true
	}
	return false
}

// github.pullrequestcomments.list and github.pullrequestcomments.create report database_id alongside the
// node id they have always reported as id, the numeric REST identifier the reaction tools need for kind
// issue_comment on a pull request conversation comment, since github.comments.update and
// github.comments.delete refuse one.
func TestPullRequestCommentsReportTheDatabaseID(t *testing.T) {
	f := &fakeGitHub{}
	p := &fakePRComments{f: f}
	f.failure = p.route
	base := serve(t, f)
	c := client(t, base, repoTarget)

	list, err := c.listPullRequestComments(context.Background(), &pullRequestCommentArguments{Number: 7, Limit: 30})
	if err != nil || len(list.Comments) != 1 || list.Comments[0].ID != "PRC_1" || list.Comments[0].DatabaseID != 501 {
		t.Fatalf("listPullRequestComments() = %+v, %v", list, err)
	}

	created, err := c.createPullRequestComment(context.Background(), 7, "a new reply")
	if err != nil || created.ID != "PRC_new" || created.DatabaseID != 502 {
		t.Fatalf("createPullRequestComment() = %+v, %v", created, err)
	}
}
