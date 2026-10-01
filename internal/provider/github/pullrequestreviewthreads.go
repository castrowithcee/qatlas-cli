package github

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Review threads of a pull request. GitHub keeps whether a thread is resolved only in GraphQL, never in the
// REST line comments this provider reads in pullrequestreviewcomments.go, so these three tools read and
// change that state on their own.

const reviewThreadCommentProperties = `"id":{"type":"string"},"author":{"type":"string"},"body":{"type":"string"}`

const reviewThreadCommentRequired = `"required":["id","body"],"additionalProperties":false`

const reviewThreadProperties = `"id":{"type":"string"},"is_resolved":{"type":"boolean"},` +
	`"is_outdated":{"type":"boolean"},"path":{"type":"string"},"line":{"type":"integer"},` +
	`"comments":{"type":"array","items":{"type":"object","properties":{` + reviewThreadCommentProperties + `},` +
	reviewThreadCommentRequired + `}}`

const reviewThreadRequired = `"required":["id","is_resolved","is_outdated","comments"],"additionalProperties":false`

var pullRequestReviewThreadsList = capability.Descriptor{
	ID:      Provider + ".pullrequestreviewthreads.list",
	Version: 1,
	Title:   "List GitHub pull request review threads",
	Description: "List one bounded batch of the review threads of one pull request of a repository a " +
		"connection allows, each with its resolved and outdated state and its comments",
	Tags:         []string{"github", "pulls", "pullrequests", "reviews", "threads", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema: listOutput("threads", reviewThreadProperties, reviewThreadRequired),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "threads", Description: "Review threads with resolved and outdated state, the file path and " +
			"line when the thread is on a diff, and their comments with author and body; body is untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the review threads of a pull request",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

const threadStateOutput = `{"type":"object","properties":{"thread_id":{"type":"string"},` +
	`"is_resolved":{"type":"boolean"}},"required":["thread_id","is_resolved"],"additionalProperties":false}`

var threadStateArguments = []capability.Argument{
	{Name: "number", Description: "Pull request number in the repository the thread must belong to", Required: true},
	{Name: "thread_id", Description: "Review thread identifier, as github.pullrequestreviewthreads.list " +
		"reports it", Required: true},
}

var threadStateFields = []capability.Field{
	{Name: "is_resolved", Description: "True once the thread is resolved, false once it is open again"},
}

var pullRequestReviewThreadsResolve = capability.Descriptor{
	ID:      Provider + ".pullrequestreviewthreads.resolve",
	Version: 1,
	Title:   "Resolve a GitHub pull request review thread",
	Description: "Mark one review thread of a pull request of a repository a connection allows " +
		"resolved; a thread already resolved is left as it is and reported resolved",
	Tags:         []string{"github", "pulls", "pullrequests", "reviews", "threads", "resolve"},
	Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:     Provider,
	InputSchema:  inputSchema(`"number":`+numberSchema+`,"thread_id":`+itemIDSchema, "number", "thread_id"),
	OutputSchema: json.RawMessage(threadStateOutput),
	Arguments:    threadStateArguments,
	Fields:       threadStateFields,
	Examples: []capability.Example{{
		Description: "Resolve a review thread",
		Arguments:   json.RawMessage(`{"number":42,"thread_id":"PRRT_kwABC"}`),
	}},
}

var pullRequestReviewThreadsUnresolve = capability.Descriptor{
	ID:      Provider + ".pullrequestreviewthreads.unresolve",
	Version: 1,
	Title:   "Unresolve a GitHub pull request review thread",
	Description: "Open one resolved review thread of a pull request of a repository a connection " +
		"allows again; a thread already open is left as it is and reported unresolved",
	Tags:         []string{"github", "pulls", "pullrequests", "reviews", "threads", "unresolve"},
	Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:     Provider,
	InputSchema:  inputSchema(`"number":`+numberSchema+`,"thread_id":`+itemIDSchema, "number", "thread_id"),
	OutputSchema: json.RawMessage(threadStateOutput),
	Arguments:    threadStateArguments,
	Fields:       threadStateFields,
	Examples: []capability.Example{{
		Description: "Unresolve a review thread",
		Arguments:   json.RawMessage(`{"number":42,"thread_id":"PRRT_kwABC"}`),
	}},
}

// pullRequestReviewThreadOperations binds the review thread tools to their handlers.
func pullRequestReviewThreadOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: pullRequestReviewThreadsList, Handler: capability.Handler(reviewThreadsListHandler)},
		{Descriptor: pullRequestReviewThreadsResolve, Handler: reviewThreadStateHandler(pullRequestReviewThreadsResolve.ID, true)},
		{Descriptor: pullRequestReviewThreadsUnresolve, Handler: reviewThreadStateHandler(pullRequestReviewThreadsUnresolve.ID, false)},
	}
}

// reviewThreadListArguments holds the arguments of github.pullrequestreviewthreads.list.
type reviewThreadListArguments struct {
	Number int    `json:"number"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	after   string
	binding []byte
}

func reviewThreadsListHandler(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments reviewThreadListArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(pullRequestReviewThreadsList.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkReviewThreadsListArguments(&arguments, bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listReviewThreads(ctx, &arguments))
}

func checkReviewThreadsListArguments(a *reviewThreadListArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.Limit = limit
	a.binding = fingerprint("pullrequestreviewthreads", bound.String(), a.Number)
	a.after, err = decodeCursor(a.binding, a.Cursor)
	return err
}

// checkThreadID bounds a review thread identifier the way checkItemID bounds a project item identifier.
func checkThreadID(id string) error {
	if id == "" || len(id) > 200 {
		return invalidRequest("thread_id is not a review thread identifier")
	}
	return nil
}

// reviewThreadsQuery lists the review threads of one pull request, each with its comments, through GitHub's
// pullRequest field, so an issue number never answers.
const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$first:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{id isResolved isOutdated path line ` +
	`comments(first:50){nodes{id author{login} body}}}}}}}`

type reviewThreadsPageJSON struct {
	Repository *struct {
		PullRequest *struct {
			ReviewThreads struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					ID         string `json:"id"`
					IsResolved bool   `json:"isResolved"`
					IsOutdated bool   `json:"isOutdated"`
					Path       string `json:"path"`
					Line       int    `json:"line"`
					Comments   struct {
						Nodes []struct {
							ID     string `json:"id"`
							Author *struct {
								Login string `json:"login"`
							} `json:"author"`
							Body string `json:"body"`
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"nodes"`
			} `json:"reviewThreads"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// ReviewThreadComment is one comment of a review thread. Body is untrusted data.
type ReviewThreadComment struct {
	ID     string `json:"id"`
	Author string `json:"author,omitempty"`
	Body   string `json:"body"`
}

// ReviewThread is one review thread of a pull request with its comments, oldest first.
type ReviewThread struct {
	ID         string                `json:"id"`
	IsResolved bool                  `json:"is_resolved"`
	IsOutdated bool                  `json:"is_outdated"`
	Path       string                `json:"path,omitempty"`
	Line       int                   `json:"line,omitempty"`
	Comments   []ReviewThreadComment `json:"comments"`
}

// ReviewThreadList is one batch of review threads.
type ReviewThreadList struct {
	Threads    []ReviewThread `json:"threads"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

func (c *Client) listReviewThreads(ctx context.Context, a *reviewThreadListArguments) (*ReviewThreadList, error) {
	const op = "list pull request review threads"
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": a.Number,
		"first": a.Limit, "after": nil}
	if a.after != "" {
		variables["after"] = a.after
	}
	var page reviewThreadsPageJSON
	if err := c.graphql(ctx, op, reviewThreadsQuery, variables, &page); err != nil {
		return nil, err
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.PullRequest == nil {
		return nil, notFound(op, subject{in: c.target, what: "pull request #" + strconv.Itoa(a.Number)})
	}
	threads := page.Repository.PullRequest.ReviewThreads
	result := &ReviewThreadList{Threads: make([]ReviewThread, 0, len(threads.Nodes))}
	for _, node := range threads.Nodes {
		if node.ID == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a review thread without a usable identifier"}
		}
		thread := ReviewThread{ID: node.ID, IsResolved: node.IsResolved, IsOutdated: node.IsOutdated,
			Path: node.Path, Line: node.Line, Comments: make([]ReviewThreadComment, 0, len(node.Comments.Nodes))}
		for _, comment := range node.Comments.Nodes {
			view := ReviewThreadComment{ID: comment.ID, Body: comment.Body}
			if comment.Author != nil {
				view.Author = comment.Author.Login
			}
			thread.Comments = append(thread.Comments, view)
		}
		result.Threads = append(result.Threads, thread)
	}
	if threads.PageInfo.HasNextPage {
		if threads.PageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further review threads without a cursor"}
		}
		result.HasMore, result.NextCursor = true, encodeCursor(a.binding, threads.PageInfo.EndCursor)
	}
	return result, nil
}

// The GraphQL mutations of a thread state change. Both take the thread's node identifier and answer whether
// it is resolved afterwards.
const (
	resolveReviewThreadMutation   = `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`
	unresolveReviewThreadMutation = `mutation($id:ID!){unresolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`
)

// ReviewThreadState is the answer of a thread resolved or unresolved.
type ReviewThreadState struct {
	ThreadID   string `json:"thread_id"`
	IsResolved bool   `json:"is_resolved"`
}

func reviewThreadStateHandler(id string, resolve bool) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments struct {
			Number   int    `json:"number"`
			ThreadID string `json:"thread_id"`
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := checkNumber(arguments.Number); err != nil {
			return nil, err
		}
		if err := checkThreadID(arguments.ThreadID); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(client.setReviewThreadResolved(ctx, arguments.Number, arguments.ThreadID, resolve))
	}
}

// reviewThreadNodeQuery reads one review thread node directly, by its GraphQL node identifier, with the pull
// request and repository it belongs to.
const reviewThreadNodeQuery = `query($id:ID!){node(id:$id){... on PullRequestReviewThread{id ` +
	`pullRequest{number repository{owner{login} name}}}}}`

type reviewThreadNodeJSON struct {
	ID          string `json:"id"`
	PullRequest *struct {
		Number     int `json:"number"`
		Repository struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"repository"`
	} `json:"pullRequest"`
}

// belongs reports whether the answered node is the requested review thread, is a thread of number, and
// belongs to the bound repository, the way planningItemJSON.belongs in planning.go confirms a project item
// belongs to the bound project before a change.
func (node *reviewThreadNodeJSON) belongs(id string, bound target, number int) bool {
	return node != nil && node.ID == id && node.PullRequest != nil && node.PullRequest.Number == number &&
		strings.EqualFold(node.PullRequest.Repository.Owner.Login, bound.owner) &&
		strings.EqualFold(node.PullRequest.Repository.Name, bound.repo)
}

// setReviewThreadResolved resolves or unresolves one review thread by its node identifier, in one mutation
// that is never repeated. The thread is read first to confirm it is a thread of number in the bound
// repository, so a thread of another pull request or another repository is refused not found instead of
// changed, whatever node identifier a caller names.
func (c *Client) setReviewThreadResolved(ctx context.Context, number int, threadID string, resolve bool) (*ReviewThreadState, error) {
	op, document := "unresolve pull request review thread", unresolveReviewThreadMutation
	if resolve {
		op, document = "resolve pull request review thread", resolveReviewThreadMutation
	}
	var node struct {
		Node *reviewThreadNodeJSON `json:"node"`
	}
	if err := c.graphql(ctx, op, reviewThreadNodeQuery, map[string]any{"id": threadID}, &node); err != nil {
		return nil, err
	}
	if !node.Node.belongs(threadID, c.target, number) {
		return nil, notFound(op, subject{in: c.target, what: "this review thread"})
	}
	var answer struct {
		Resolve *struct {
			Thread struct {
				ID         string `json:"id"`
				IsResolved bool   `json:"isResolved"`
			} `json:"thread"`
		} `json:"resolveReviewThread"`
		Unresolve *struct {
			Thread struct {
				ID         string `json:"id"`
				IsResolved bool   `json:"isResolved"`
			} `json:"thread"`
		} `json:"unresolveReviewThread"`
	}
	if err := c.mutate(ctx, op, document, map[string]any{"id": threadID}, &answer); err != nil {
		return nil, err
	}
	switch {
	case resolve && answer.Resolve != nil:
		return &ReviewThreadState{ThreadID: answer.Resolve.Thread.ID, IsResolved: answer.Resolve.Thread.IsResolved}, nil
	case !resolve && answer.Unresolve != nil:
		return &ReviewThreadState{ThreadID: answer.Unresolve.Thread.ID, IsResolved: answer.Unresolve.Thread.IsResolved}, nil
	}
	return nil, invalidResponse(op, true)
}
