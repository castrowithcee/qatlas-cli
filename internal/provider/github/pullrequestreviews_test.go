package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Fakes for the twelve pull request review tools: conversation comments, reviews with their line comments,
// replies to a line comment, review threads, and requested reviewers. GraphQL requests are told apart by a
// distinctive substring of their document, the way the rest of this package's fakes already do; REST
// requests are told apart by their method and path below one pull request.

type fakeReview struct {
	id                                    int64
	author, state, body, commitSHA, subAt string
}

type fakeLineComment struct {
	id                               int64
	path, side, author, body, commit string
	line                             int
	inReplyTo                        int64
}

type fakeThreadComment struct{ id, author, body string }

type fakeThread struct {
	id                     string
	isResolved, isOutdated bool
	path                   string
	line                   int
	comments               []fakeThreadComment
	// pullRequestNumber and repoOwner/repoName name what the node query answers this thread belongs to; the
	// zero value uses the map key it is stored under and octo-org/example, so only a test that deliberately
	// mismatches one of them needs to set it.
	pullRequestNumber   int
	repoOwner, repoName string
}

type fakeConvComment struct{ id, author, body string }

// fakeReviews answers the review routes of the bound repository, and the GraphQL documents the review tools
// send, through the failure hook of fakeGitHub.
type fakeReviews struct {
	f                  *fakeGitHub
	headSHA            map[int]string
	isIssue            map[int]bool
	reviews            map[int][]fakeReview
	lineComments       map[int][]fakeLineComment
	threads            map[int][]fakeThread
	convComments       map[int][]fakeConvComment
	requestedReviewers map[int][]string
	reviewSeq, lineSeq int64
	selfApprove        bool
}

func serveReviews(t *testing.T) (*fakeGitHub, *fakeReviews, string) {
	t.Helper()
	r := &fakeReviews{headSHA: map[int]string{}, isIssue: map[int]bool{}, reviews: map[int][]fakeReview{},
		lineComments: map[int][]fakeLineComment{}, threads: map[int][]fakeThread{},
		convComments: map[int][]fakeConvComment{}, requestedReviewers: map[int][]string{}}
	f := &fakeGitHub{failure: r.route}
	r.f = f
	return f, r, serve(t, f)
}

func (r *fakeReviews) lastVariables() map[string]any {
	requests := r.f.recorded()
	if len(requests) == 0 {
		return nil
	}
	return requests[len(requests)-1].variables
}

func (r *fakeReviews) lastDocument() string {
	requests := r.f.recorded()
	if len(requests) == 0 {
		return ""
	}
	return requests[len(requests)-1].document
}

func (r *fakeReviews) lastBody() map[string]any {
	requests := r.f.recorded()
	if len(requests) == 0 {
		return nil
	}
	return requests[len(requests)-1].body
}

func intVar(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func (r *fakeReviews) route(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path == "/api/graphql" {
		w.Header().Set("Content-Type", "application/json")
		r.graphql(w)
		return true
	}
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || !strings.HasPrefix(rest, "pulls/") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(rest, "/reviews"):
		r.listReviews(w, rest)
	case req.Method == http.MethodPost && strings.HasSuffix(rest, "/reviews"):
		r.createReview(w, rest)
	case req.Method == http.MethodGet && strings.HasSuffix(rest, "/comments"):
		r.listLineComments(w, rest)
	case req.Method == http.MethodPost && strings.Contains(rest, "/comments/") && strings.HasSuffix(rest, "/replies"):
		r.replyLineComment(w, rest)
	case (req.Method == http.MethodPost || req.Method == http.MethodDelete) && strings.HasSuffix(rest, "/requested_reviewers"):
		r.changeReviewers(w, rest, req.Method)
	case req.Method == http.MethodGet && plainPullPath(rest):
		r.getPull(w, rest)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}

// plainPullPath reports whether rest names one pull request directly, pulls/N, and nothing below it.
func plainPullPath(rest string) bool {
	tail, ok := strings.CutPrefix(rest, "pulls/")
	if !ok {
		return false
	}
	_, err := strconv.Atoi(tail)
	return err == nil
}

func (r *fakeReviews) graphql(w http.ResponseWriter) {
	document := r.lastDocument()
	variables := r.lastVariables()
	number := intVar(variables["number"])
	switch {
	case strings.Contains(document, "issue(number:$number){id}"):
		r.answerIssueLookup(w, number)
	case strings.Contains(document, "pullRequest(number:$number){id}"):
		r.answerPullRequestLookup(w, number)
	case strings.Contains(document, "pullRequest(number:$number){comments"):
		r.answerConversationComments(w, number)
	case strings.Contains(document, "reviewThreads(first"):
		r.answerReviewThreads(w, number)
	case strings.Contains(document, "PullRequestReviewThread{id"):
		r.answerReviewThreadNode(w, variables)
	case strings.Contains(document, "unresolveReviewThread"):
		r.answerThreadState(w, variables, false)
	case strings.Contains(document, "resolveReviewThread"):
		r.answerThreadState(w, variables, true)
	default:
		fmt.Fprint(w, `{"errors":[{"message":"unexpected document"}]}`)
	}
}

func (r *fakeReviews) answerIssueLookup(w http.ResponseWriter, number int) {
	if r.isIssue[number] {
		fmt.Fprintf(w, `{"data":{"repository":{"issue":{"id":"I_%d"}}}}`, number)
		return
	}
	fmt.Fprint(w, `{"data":{"repository":{"issue":null}}}`)
}

func (r *fakeReviews) answerPullRequestLookup(w http.ResponseWriter, number int) {
	if _, ok := r.headSHA[number]; ok {
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"id":"PR_%d"}}}}`, number)
		return
	}
	fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}}}`)
}

func (r *fakeReviews) answerConversationComments(w http.ResponseWriter, number int) {
	if _, ok := r.headSHA[number]; !ok {
		fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}}}`)
		return
	}
	nodes := make([]string, 0, len(r.convComments[number]))
	for _, c := range r.convComments[number] {
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"author":{"login":%q},"body":%q,`+
			`"createdAt":"2026-01-03T00:00:00Z","updatedAt":"2026-01-03T00:00:00Z",`+
			`"url":"https://github.com/octo-org/example/pull/%d#issuecomment"}`, c.id, c.author, c.body, number))
	}
	fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"comments":{"pageInfo":{"hasNextPage":false,`+
		`"endCursor":""},"nodes":[%s]}}}}}`, strings.Join(nodes, ","))
}

func (r *fakeReviews) answerReviewThreads(w http.ResponseWriter, number int) {
	if _, ok := r.headSHA[number]; !ok {
		fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}}}`)
		return
	}
	nodes := make([]string, 0, len(r.threads[number]))
	for _, th := range r.threads[number] {
		comments := make([]string, 0, len(th.comments))
		for _, c := range th.comments {
			comments = append(comments, fmt.Sprintf(`{"id":%q,"author":{"login":%q},"body":%q}`, c.id, c.author, c.body))
		}
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"isResolved":%v,"isOutdated":%v,"path":%q,"line":%d,`+
			`"comments":{"nodes":[%s]}}`, th.id, th.isResolved, th.isOutdated, th.path, th.line,
			strings.Join(comments, ",")))
	}
	fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,`+
		`"endCursor":""},"nodes":[%s]}}}}}`, strings.Join(nodes, ","))
}

func (r *fakeReviews) threadIndex(id string) (int, int) {
	for number, threads := range r.threads {
		for i, th := range threads {
			if th.id == id {
				return i, number
			}
		}
	}
	return -1, 0
}

// answerReviewThreadNode answers the node(id:$id){... on PullRequestReviewThread{...}} query a resolve or
// unresolve sends first, to confirm the thread's pull request and repository before mutating it.
func (r *fakeReviews) answerReviewThreadNode(w http.ResponseWriter, variables map[string]any) {
	id, _ := variables["id"].(string)
	idx, number := r.threadIndex(id)
	if idx < 0 {
		fmt.Fprint(w, `{"data":{"node":null}}`)
		return
	}
	th := r.threads[number][idx]
	prNumber := th.pullRequestNumber
	if prNumber == 0 {
		prNumber = number
	}
	owner, name := th.repoOwner, th.repoName
	if owner == "" {
		owner = "octo-org"
	}
	if name == "" {
		name = "example"
	}
	fmt.Fprintf(w, `{"data":{"node":{"id":%q,"pullRequest":{"number":%d,"repository":{"owner":{"login":%q},`+
		`"name":%q}}}}}`, th.id, prNumber, owner, name)
}

func (r *fakeReviews) answerThreadState(w http.ResponseWriter, variables map[string]any, resolve bool) {
	id, _ := variables["id"].(string)
	idx, number := r.threadIndex(id)
	if idx < 0 {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","message":"no review thread"}]}`)
		return
	}
	r.threads[number][idx].isResolved = resolve
	alias := "unresolveReviewThread"
	if resolve {
		alias = "resolveReviewThread"
	}
	fmt.Fprintf(w, `{"data":{%q:{"thread":{"id":%q,"isResolved":%v}}}}`, alias, id, resolve)
}

func (r *fakeReviews) prNumber(rest, suffix string) (int, bool) {
	tail := strings.TrimSuffix(strings.TrimPrefix(rest, "pulls/"), suffix)
	n, err := strconv.Atoi(tail)
	return n, err == nil
}

var eventToState = map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED",
	"COMMENT": "COMMENTED"}

func reviewJSONOf(rv fakeReview) string {
	return fmt.Sprintf(`{"id":%d,"user":{"login":%q},"body":%q,"state":%q,"commit_id":%q,"submitted_at":%q}`,
		rv.id, rv.author, rv.body, rv.state, rv.commitSHA, rv.subAt)
}

func (r *fakeReviews) listReviews(w http.ResponseWriter, rest string) {
	number, ok := r.prNumber(rest, "/reviews")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	entries := make([]string, 0, len(r.reviews[number]))
	for _, rv := range r.reviews[number] {
		entries = append(entries, reviewJSONOf(rv))
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (r *fakeReviews) createReview(w http.ResponseWriter, rest string) {
	number, ok := r.prNumber(rest, "/reviews")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := r.lastBody()
	commitID, _ := body["commit_id"].(string)
	event, _ := body["event"].(string)
	if r.selfApprove {
		r.selfApprove = false
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed","errors":["Can not approve your own pull request"]}`)
		return
	}
	if commitID != r.headSHA[number] {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"resource":"PullRequestReview","field":"commit_id"}]}`)
		return
	}
	r.reviewSeq++
	reviewBody, _ := body["body"].(string)
	review := fakeReview{id: r.reviewSeq, author: "octocat", state: eventToState[event], body: reviewBody,
		commitSHA: commitID, subAt: "2026-01-05T00:00:00Z"}
	r.reviews[number] = append(r.reviews[number], review)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, reviewJSONOf(review))
}

func lineCommentJSONOf(lc fakeLineComment) string {
	inReplyTo := "null"
	if lc.inReplyTo != 0 {
		inReplyTo = strconv.FormatInt(lc.inReplyTo, 10)
	}
	return fmt.Sprintf(`{"id":%d,"path":%q,"line":%d,"side":%q,"commit_id":%q,"user":{"login":%q},"body":%q,`+
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","in_reply_to_id":%s}`,
		lc.id, lc.path, lc.line, lc.side, lc.commit, lc.author, lc.body, inReplyTo)
}

func (r *fakeReviews) listLineComments(w http.ResponseWriter, rest string) {
	number, ok := r.prNumber(rest, "/comments")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	entries := make([]string, 0, len(r.lineComments[number]))
	for _, lc := range r.lineComments[number] {
		entries = append(entries, lineCommentJSONOf(lc))
	}
	fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
}

func (r *fakeReviews) replyLineComment(w http.ResponseWriter, rest string) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(rest, "pulls/"), "/replies")
	parts := strings.SplitN(trimmed, "/comments/", 2)
	if len(parts) != 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	number, err1 := strconv.Atoi(parts[0])
	commentID, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var parent *fakeLineComment
	for i := range r.lineComments[number] {
		if r.lineComments[number][i].id == commentID {
			parent = &r.lineComments[number][i]
			break
		}
	}
	if parent == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return
	}
	body := r.lastBody()
	replyBody, _ := body["body"].(string)
	r.lineSeq++
	reply := fakeLineComment{id: 1000 + r.lineSeq, path: parent.path, line: parent.line, side: parent.side,
		commit: parent.commit, author: "octocat", body: replyBody, inReplyTo: parent.id}
	r.lineComments[number] = append(r.lineComments[number], reply)
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, lineCommentJSONOf(reply))
}

func reviewerPullJSON(number int, sha string, reviewers []string) string {
	names := make([]string, len(reviewers))
	for i, login := range reviewers {
		names[i] = fmt.Sprintf(`{"login":%q}`, login)
	}
	return fmt.Sprintf(`{"number":%d,"node_id":"PR_x","title":"x","body":"","state":"open","draft":false,`+
		`"user":{"login":"octocat"},"head":{"ref":"feature","sha":%q},"base":{"ref":"main","sha":"basesha"},`+
		`"labels":[],"requested_reviewers":[%s],"merged":false,"mergeable":true,"mergeable_state":"clean",`+
		`"merge_commit_sha":"","commits":1,"changed_files":1,"created_at":"2026-01-01T00:00:00Z",`+
		`"updated_at":"2026-01-02T00:00:00Z","closed_at":null,"merged_at":null,`+
		`"html_url":"https://github.com/octo-org/example/pull/%d"}`, number, sha, strings.Join(names, ","), number)
}

func (r *fakeReviews) getPull(w http.ResponseWriter, rest string) {
	number, ok := r.prNumber(rest, "")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sha, ok := r.headSHA[number]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fmt.Fprint(w, reviewerPullJSON(number, sha, r.requestedReviewers[number]))
}

func (r *fakeReviews) changeReviewers(w http.ResponseWriter, rest, method string) {
	number, ok := r.prNumber(rest, "/requested_reviewers")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := r.lastBody()
	var logins []string
	if raw, ok := body["reviewers"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				logins = append(logins, s)
			}
		}
	}
	current := r.requestedReviewers[number]
	if method == http.MethodPost {
		for _, login := range logins {
			if !containsFold(current, login) {
				current = append(current, login)
			}
		}
	} else {
		filtered := make([]string, 0, len(current))
		for _, existing := range current {
			keep := true
			for _, login := range logins {
				if strings.EqualFold(existing, login) {
					keep = false
				}
			}
			if keep {
				filtered = append(filtered, existing)
			}
		}
		current = filtered
	}
	r.requestedReviewers[number] = current
	fmt.Fprint(w, reviewerPullJSON(number, r.headSHA[number], current))
}

// A pull request number given to a conversation comment tool works; an issue number given to either is
// refused, the mirror image of pullRequestRefusal in issues.go.
func TestPullRequestCommentToolsRefuseAnIssueNumber(t *testing.T) {
	_, r, base := serveReviews(t)
	r.headSHA[42] = "aaa"
	r.isIssue[7] = true
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	r.convComments[42] = []fakeConvComment{{id: "IC_1", author: "octocat", body: "first"}}
	listed, err := invoke(t, core, "github.pullrequestcomments.list", "repo", `{"number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(listed, &page); err != nil || len(page.Comments) != 1 || page.Comments[0].Body != "first" {
		t.Fatalf("listed comments = %s, %v", listed, err)
	}

	created, err := invoke(t, core, "github.pullrequestcomments.create", "repo", `{"number":42,"body":"lgtm"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var comment struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(created, &comment); err != nil || comment.Body != "lgtm" {
		t.Fatalf("created comment = %s, %v", created, err)
	}

	for _, tt := range []struct{ operation, arguments string }{
		{"github.pullrequestcomments.list", `{"number":7}`},
		{"github.pullrequestcomments.create", `{"number":7,"body":"x"}`},
	} {
		confirmed := strings.HasSuffix(tt.operation, "create")
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, confirmed); !isInvalidRequest(err) ||
			!strings.Contains(err.Error(), "number 7 in repository octo-org/example is an issue; pull request "+
				"tools do not handle issues") {
			t.Errorf("%s of an issue number = %v, want the mirrored refusal", tt.operation, err)
		}
	}
}

// A review is submitted with its line comments bundled into the one call; a stale sha is refused clearly
// naming both commits, and a rejection GitHub gives for another reason, such as approving one's own pull
// request, is refused clearly without a second attempt. Approving is offered only where a connection's
// tools list names it.
func TestPullRequestReviewsCreateAndApprove(t *testing.T) {
	f, r, base := serveReviews(t)
	sha := strings.Repeat("a", 40)
	r.headSHA[90] = sha
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["approver"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
		Tools:       []string{pullRequestReviewsApprove.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)

	before := len(f.recorded())
	created, err := invoke(t, core, "github.pullrequestreviews.create", "repo", fmt.Sprintf(
		`{"number":90,"sha":%q,"event":"request_changes","body":"one thing","line_notes":`+
			`[{"path":"a.go","line":10,"side":"right","body":"fix this"}]}`, sha), true)
	if err != nil {
		t.Fatal(err)
	}
	var review struct {
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(created, &review); err != nil || review.State != "CHANGES_REQUESTED" || review.Body != "one thing" {
		t.Fatalf("created review = %s, %v", created, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodPost {
		t.Errorf("create requests = %+v, want exactly one POST", requests)
	}
	if comments, ok := requests(f, before)[0].body["comments"].([]any); !ok || len(comments) != 1 {
		t.Errorf("review payload = %+v, want the line comment bundled as comments", requests(f, before)[0].body)
	}

	// Approving is unsupported for a connection that does not list it.
	if _, err := invoke(t, core, "github.pullrequestreviews.approve", "repo", fmt.Sprintf(`{"number":90,"sha":%q}`, sha), true); err == nil {
		t.Error("approve on a connection without the tool listed succeeded, want unsupported")
	} else if _, ok := err.(*capability.UnsupportedError); !ok {
		t.Errorf("approve on a connection without the tool listed = %T %v, want unsupported", err, err)
	}

	approved, err := invoke(t, core, "github.pullrequestreviews.approve", "approver", fmt.Sprintf(`{"number":90,"sha":%q}`, sha), true)
	if err != nil {
		t.Fatal(err)
	}
	var approval struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(approved, &approval); err != nil || approval.State != "APPROVED" {
		t.Fatalf("approval = %s, %v", approved, err)
	}

	// A stale sha is refused clearly naming both commits, without a second attempt.
	before = len(f.recorded())
	stale := strings.Repeat("b", 40)
	_, err = invoke(t, core, "github.pullrequestreviews.create", "repo", fmt.Sprintf(
		`{"number":90,"sha":%q,"event":"comment"}`, stale), true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), stale) || !strings.Contains(err.Error(), sha) {
		t.Errorf("a stale sha = %v, want an invalid request naming both commits", err)
	}
	if got := len(f.recorded()) - before; got != 2 {
		t.Errorf("a stale review sent %d requests, want the refused POST and one re-read, never a retry", got)
	}

	// GitHub refusing for another reason, such as approving one's own pull request, is refused clearly.
	before = len(f.recorded())
	r.selfApprove = true
	_, err = invoke(t, core, "github.pullrequestreviews.approve", "approver", fmt.Sprintf(`{"number":90,"sha":%q}`, sha), true)
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "own pull request") {
		t.Errorf("a self-approval = %v, want a clear provider refusal", err)
	}
	if got := len(f.recorded()) - before; got != 2 {
		t.Errorf("a refused approval sent %d requests, want the refused POST and one re-read, never a retry", got)
	}

	// Reviews are listed in GitHub's own order.
	listed, err := invoke(t, core, "github.pullrequestreviews.list", "repo", `{"number":90}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Reviews []struct {
			State string `json:"state"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(listed, &page); err != nil || len(page.Reviews) != 2 {
		t.Fatalf("listed reviews = %s, %v", listed, err)
	}
}

// requests slices the fake's recorded requests from before to the end, for a body assertion after an invoke.
func requests(f *fakeGitHub, before int) []recorded {
	return f.recorded()[before:]
}

// Line comments are read across every review, and a reply is refused not found by comment identifier when
// the comment does not exist.
func TestPullRequestReviewCommentsListAndReply(t *testing.T) {
	f, r, base := serveReviews(t)
	r.headSHA[91] = strings.Repeat("c", 40)
	r.lineComments[91] = []fakeLineComment{{id: 5, path: "a.go", side: "RIGHT", line: 3, commit: r.headSHA[91],
		author: "octocat", body: "why here?"}}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, "github.pullrequestreviewcomments.list", "repo", `{"number":91}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		LineComments []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"line_comments"`
	}
	if err := json.Unmarshal(listed, &page); err != nil || len(page.LineComments) != 1 || page.LineComments[0].ID != 5 {
		t.Fatalf("listed line comments = %s, %v", listed, err)
	}

	before := len(f.recorded())
	replied, err := invoke(t, core, "github.pullrequestreviewcomments.reply", "repo",
		`{"number":91,"comment_id":5,"body":"good catch"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		InReplyTo int64  `json:"in_reply_to"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(replied, &reply); err != nil || reply.InReplyTo != 5 || reply.Body != "good catch" {
		t.Fatalf("reply = %s, %v", replied, err)
	}
	if got := len(f.recorded()) - before; got != 1 {
		t.Errorf("reply sent %d requests, want exactly one POST", got)
	}

	_, err = invoke(t, core, "github.pullrequestreviewcomments.reply", "repo",
		`{"number":91,"comment_id":999,"body":"x"}`, true)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "review comment 999") {
		t.Errorf("a reply to an unknown comment = %v, want not found naming the review comment", err)
	}
}

// Review threads are read with their resolved state and comments, and resolve and unresolve are idempotent.
// A thread of another pull request or another repository is refused not found, and nothing is mutated.
func TestPullRequestReviewThreadsListResolveAndUnresolve(t *testing.T) {
	f, r, base := serveReviews(t)
	r.headSHA[92] = strings.Repeat("d", 40)
	r.threads[92] = []fakeThread{{id: "PRRT_1", path: "a.go", line: 4,
		comments: []fakeThreadComment{{id: "PRRC_1", author: "octocat", body: "please fix"}}}}
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, "github.pullrequestreviewthreads.list", "repo", `{"number":92}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Threads []struct {
			ID         string `json:"id"`
			IsResolved bool   `json:"is_resolved"`
			Comments   []struct {
				Body string `json:"body"`
			} `json:"comments"`
		} `json:"threads"`
	}
	if err := json.Unmarshal(listed, &page); err != nil || len(page.Threads) != 1 || page.Threads[0].IsResolved ||
		len(page.Threads[0].Comments) != 1 {
		t.Fatalf("listed threads = %s, %v", listed, err)
	}

	// A thread of a different pull request number is refused not found, before any mutation.
	before := len(f.recorded())
	_, err = invoke(t, core, "github.pullrequestreviewthreads.resolve", "repo", `{"number":93,"thread_id":"PRRT_1"}`, true)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "this review thread") {
		t.Errorf("a thread of another pull request = %v, want not found naming the review thread", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a refused resolve sent %d requests, want only the node read, never a mutation", len(requests))
	}

	// A thread of another repository is refused not found the same way.
	r.threads[92][0].repoOwner, r.threads[92][0].repoName = "other-org", "other-repo"
	before = len(f.recorded())
	_, err = invoke(t, core, "github.pullrequestreviewthreads.resolve", "repo", `{"number":92,"thread_id":"PRRT_1"}`, true)
	if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), "this review thread") {
		t.Errorf("a thread of another repository = %v, want not found naming the review thread", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("a refused resolve sent %d requests, want only the node read, never a mutation", len(requests))
	}
	r.threads[92][0].repoOwner, r.threads[92][0].repoName = "", ""

	resolved, err := invoke(t, core, "github.pullrequestreviewthreads.resolve", "repo", `{"number":92,"thread_id":"PRRT_1"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		IsResolved bool `json:"is_resolved"`
	}
	if err := json.Unmarshal(resolved, &state); err != nil || !state.IsResolved {
		t.Fatalf("resolved = %s, %v", resolved, err)
	}

	// Resolving an already-resolved thread is idempotent.
	resolvedAgain, err := invoke(t, core, "github.pullrequestreviewthreads.resolve", "repo", `{"number":92,"thread_id":"PRRT_1"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resolvedAgain, &state); err != nil || !state.IsResolved {
		t.Fatalf("resolved again = %s, %v", resolvedAgain, err)
	}

	unresolved, err := invoke(t, core, "github.pullrequestreviewthreads.unresolve", "repo", `{"number":92,"thread_id":"PRRT_1"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(unresolved, &state); err != nil || state.IsResolved {
		t.Fatalf("unresolved = %s, %v", unresolved, err)
	}
}

// Requesting and removing reviewers is idempotent, and the answer is the pull request with its requested
// reviewers.
func TestPullRequestReviewersRequestAndRemove(t *testing.T) {
	_, r, base := serveReviews(t)
	r.headSHA[93] = strings.Repeat("e", 40)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)

	requested, err := invoke(t, core, "github.pullrequestreviewers.request", "repo",
		`{"number":93,"reviewers":["hubot"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var pull struct {
		RequestedReviewers []string `json:"requested_reviewers"`
	}
	if err := json.Unmarshal(requested, &pull); err != nil || len(pull.RequestedReviewers) != 1 ||
		pull.RequestedReviewers[0] != "hubot" {
		t.Fatalf("requested reviewers = %s, %v", requested, err)
	}

	// Requesting the same reviewer again is idempotent.
	again, err := invoke(t, core, "github.pullrequestreviewers.request", "repo",
		`{"number":93,"reviewers":["hubot"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &pull); err != nil || len(pull.RequestedReviewers) != 1 {
		t.Fatalf("requested again = %s, %v", again, err)
	}

	removed, err := invoke(t, core, "github.pullrequestreviewers.remove", "repo",
		`{"number":93,"reviewers":["hubot"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(removed, &pull); err != nil || len(pull.RequestedReviewers) != 0 {
		t.Fatalf("removed reviewers = %s, %v", removed, err)
	}

	// Removing a reviewer that was not requested is idempotent.
	removedAgain, err := invoke(t, core, "github.pullrequestreviewers.remove", "repo",
		`{"number":93,"reviewers":["hubot"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(removedAgain, &pull); err != nil || len(pull.RequestedReviewers) != 0 {
		t.Fatalf("removed again = %s, %v", removedAgain, err)
	}
}

// The pull-request-reviews profile offers every tool of this task but approve, and is not recommended.
func TestPullRequestReviewsProfileExcludesApproveAndIsNotRecommended(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var profile config.ToolProfile
	found := false
	for _, candidate := range metadata.Profiles {
		if candidate.ID == "pull-request-reviews" {
			profile, found = candidate, true
		}
	}
	if !found || profile.Recommended {
		t.Fatalf("profile = %+v, found=%v, want an existing, not-recommended profile", profile, found)
	}
	want := []string{pullRequestCommentsList.ID, pullRequestCommentsCreate.ID, pullRequestReviewsList.ID,
		pullRequestReviewsCreate.ID, pullRequestReviewCommentsList.ID, pullRequestReviewCommentsReply.ID,
		pullRequestReviewThreadsList.ID, pullRequestReviewThreadsResolve.ID, pullRequestReviewThreadsUnresolve.ID,
		pullRequestReviewersRequest.ID, pullRequestReviewersRemove.ID}
	if strings.Join(profile.Tools, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", profile.Tools, want)
	}
	for _, id := range profile.Tools {
		if id == pullRequestReviewsApprove.ID {
			t.Error("the profile selects approve, which must stay unticked")
		}
	}
}

// Every review tool satisfies its output contract through the application core, and the token never reaches
// an answer; every change is audited as one successful change.
func TestPullRequestReviewToolsSatisfyTheirContractThroughTheApplicationCoreWithAudit(t *testing.T) {
	_, r, base := serveReviews(t)
	sha := strings.Repeat("f", 40)
	r.headSHA[95] = sha
	// The conversation comment create goes through the same Issue Comments REST route
	// github.comments.create uses, which the shared fakeGitHub only answers for issue number 42.
	r.headSHA[42] = strings.Repeat("g", 40)
	r.convComments[95] = []fakeConvComment{{id: "IC_9", author: "octocat", body: "hi"}}
	r.lineComments[95] = []fakeLineComment{{id: 8, path: "a.go", side: "RIGHT", line: 1, commit: sha, body: "note"}}
	r.threads[95] = []fakeThread{{id: "PRRT_9", path: "a.go", line: 1}}
	red := &redact.Redactor{}
	cfg := coreChangeConfig(base)
	cfg.Connections["approver"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
		Tools:       []string{pullRequestReviewsApprove.ID}}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	var audit strings.Builder
	core.SetAudit(&audit)

	for _, request := range []struct{ operation, arguments string }{
		{"github.pullrequestcomments.list", `{"number":95}`},
		{"github.pullrequestreviews.list", `{"number":95}`},
		{"github.pullrequestreviewcomments.list", `{"number":95}`},
		{"github.pullrequestreviewthreads.list", `{"number":95}`},
	} {
		result, err := invoke(t, core, request.operation, "repo", request.arguments, false)
		if err != nil {
			t.Errorf("%s %s = %v", request.operation, request.arguments, err)
			continue
		}
		if strings.Contains(string(result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.operation, result)
		}
	}

	for _, request := range []application.InvokeRequest{
		{Operation: "github.pullrequestcomments.create", Connection: "repo",
			Arguments: json.RawMessage(`{"number":42,"body":"thanks"}`)},
		{Operation: "github.pullrequestreviews.create", Connection: "repo",
			Arguments: json.RawMessage(fmt.Sprintf(`{"number":95,"sha":%q,"event":"comment"}`, sha))},
		{Operation: "github.pullrequestreviews.approve", Connection: "approver",
			Arguments: json.RawMessage(fmt.Sprintf(`{"number":95,"sha":%q}`, sha))},
		{Operation: "github.pullrequestreviewcomments.reply", Connection: "repo",
			Arguments: json.RawMessage(`{"number":95,"comment_id":8,"body":"ack"}`)},
		{Operation: "github.pullrequestreviewthreads.resolve", Connection: "repo",
			Arguments: json.RawMessage(`{"number":95,"thread_id":"PRRT_9"}`)},
		{Operation: "github.pullrequestreviewthreads.unresolve", Connection: "repo",
			Arguments: json.RawMessage(`{"number":95,"thread_id":"PRRT_9"}`)},
		{Operation: "github.pullrequestreviewers.request", Connection: "repo",
			Arguments: json.RawMessage(`{"number":95,"reviewers":["hubot"]}`)},
		{Operation: "github.pullrequestreviewers.remove", Connection: "repo",
			Arguments: json.RawMessage(`{"number":95,"reviewers":["hubot"]}`)},
	} {
		request.Confirmed = true
		response, err := core.Invoke(context.Background(), request)
		if err != nil {
			t.Errorf("%s %s = %v", request.Operation, request.Arguments, err)
			continue
		}
		if strings.Contains(string(response.Result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.Operation, response.Result)
		}
	}
	if strings.Count(audit.String(), `"result":"success"`) != 8 {
		t.Errorf("audit = %s, want one success event per confirmed change", audit.String())
	}
}
