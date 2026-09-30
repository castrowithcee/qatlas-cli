package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Pull request conversation comments. They are listed and written through the same Issue Comments REST
// route github.comments.* uses, since GitHub keeps a pull request's conversation comments there, but these
// two tools are bound to a pull request number: an issue number is refused, mirroring the refusal
// pullRequestRefusal in issues.go gives a pull request number at an issue tool. github.comments.* itself is
// unchanged and keeps refusing a pull request number. Line comments of a review are a separate group of
// tools bound to a review or a review thread; these two tools never see one.

var pullRequestCommentsList = capability.Descriptor{
	ID:      Provider + ".pullrequestcomments.list",
	Version: 1,
	Title:   "List GitHub pull request comments",
	Description: "List one bounded batch of the conversation comments of one pull request of a repository " +
		"a connection allows, oldest first; an issue number is refused",
	Tags:     []string{"github", "pulls", "pullrequests", "comments", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":` + cursorSchema + `},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` + commentProperties + `},` +
		commentRequired + `}},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["comments","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "limit", Description: "Comments per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch of the same pull request; the " +
			"first batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "comments", Description: "Conversation comments with author, body, and times, untrusted data"},
		commentDatabaseIDField,
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the pull request holds further comments"},
	},
	Examples: []capability.Example{{
		Description: "Read the first conversation comments of one pull request",
		Arguments:   json.RawMessage(`{"number":42,"limit":10}`),
	}},
}

var pullRequestCommentsCreate = capability.Descriptor{
	ID:      Provider + ".pullrequestcomments.create",
	Version: 1,
	Title:   "Comment on a GitHub pull request",
	Description: "Write exactly one conversation comment on one pull request of a repository an explicit " +
		"connection allows; a repeated call writes a second comment; an issue number is refused",
	Tags:     []string{"github", "pulls", "pullrequests", "comments", "create"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"body":{"type":"string","minLength":1,"maxLength":65536}},"required":["number","body"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + commentProperties + `},` + commentRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "body", Description: "Comment in Markdown, 1 to 65536 characters; stored as given", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Comment identifier"},
		commentDatabaseIDField,
		{Name: "url", Description: "Web address of the comment"},
	},
	Examples: []capability.Example{{
		Description: "Comment on a pull request",
		Arguments:   json.RawMessage(`{"number":42,"body":"Looks good to me."}`),
	}},
}

// pullRequestCommentOperations binds the two pull request conversation comment tools to their handlers.
func pullRequestCommentOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*pullRequestCommentArguments, target) error,
		call func(context.Context, *Client, *pullRequestCommentArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: pullRequestCommentsHandler(descriptor.ID, check, call)}
	}
	return []capability.Operation{
		bind(pullRequestCommentsList, checkPullRequestCommentsListArguments,
			func(ctx context.Context, c *Client, a *pullRequestCommentArguments) (any, error) {
				return c.listPullRequestComments(ctx, a)
			}),
		bind(pullRequestCommentsCreate, checkPullRequestCommentsCreateArguments,
			func(ctx context.Context, c *Client, a *pullRequestCommentArguments) (any, error) {
				return c.createPullRequestComment(ctx, a.Number, a.Body)
			}),
	}
}

// pullRequestCommentArguments holds the arguments of both pull request comment tools; the input schema of
// each tool admits only its own.
type pullRequestCommentArguments struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	after   string
	binding []byte
}

func pullRequestCommentsHandler(id string, check func(*pullRequestCommentArguments, target) error,
	call func(context.Context, *Client, *pullRequestCommentArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments pullRequestCommentArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkPullRequestCommentsListArguments(a *pullRequestCommentArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.Limit = limit
	a.binding = fingerprint("pullrequestcomments", bound.String(), a.Number)
	a.after, err = decodeCursor(a.binding, a.Cursor)
	return err
}

func checkPullRequestCommentsCreateArguments(a *pullRequestCommentArguments, _ target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	return checkCommentBody(a.Body)
}

// issueRefusal refuses an issue number at a pull request tool, the mirror image of pullRequestRefusal in
// issues.go, which refuses a pull request number at an issue tool.
func (c *Client) issueRefusal(number int) error {
	return invalidRequest(subject{in: c.target, what: "number " + strconv.Itoa(number)}.String() +
		" is an issue; pull request tools do not handle issues")
}

// issueNumberQuery asks GraphQL whether one number of the bound repository names an issue.
const issueNumberQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){` +
	`issue(number:$number){id}}}`

// issueNumber reports whether number of the bound repository names an issue. It runs only after a pull
// request comment tool found no pull request of that number, to tell the mirrored refusal from a plain
// not-found; a failed lookup reports false, so the not-found from the caller stays.
func (c *Client) issueNumber(ctx context.Context, number int) bool {
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": number}
	var page struct {
		Repository *struct {
			Issue *struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, "look up number", issueNumberQuery, variables, &page); err != nil {
		return false
	}
	return page.Repository != nil && page.Repository.Issue != nil
}

// pullRequestExistsQuery asks GraphQL whether one number of the bound repository names a pull request,
// without reading anything of it, so a pull request comment tool can confirm the number before writing.
const pullRequestExistsQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,` +
	`name:$name){pullRequest(number:$number){id}}}`

// checkPullRequestForComment confirms that number of the bound repository names a pull request, refusing an
// issue number with issueRefusal, before a pull request comment tool writes to it.
func (c *Client) checkPullRequestForComment(ctx context.Context, op string, number int) error {
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": number}
	var page struct {
		Repository *struct {
			PullRequest *struct {
				ID string `json:"id"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, op, pullRequestExistsQuery, variables, &page); err != nil {
		return err
	}
	if page.Repository == nil || page.Repository.PullRequest == nil {
		if c.issueNumber(ctx, number) {
			return c.issueRefusal(number)
		}
		return notFound(op, subject{in: c.target, what: "pull request #" + strconv.Itoa(number)})
	}
	return nil
}

// pullRequestCommentsQuery lists the conversation comments of one pull request, oldest first, the shape
// comments.go's commentsQuery reads for an issue, but through the pullRequest field instead of
// issueOrPullRequest, so it never answers for an issue number.
const pullRequestCommentsQuery = `query($owner:String!,$name:String!,$number:Int!,$first:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){pullRequest(number:$number){comments(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{id databaseId author{login} body createdAt updatedAt url}}}}}`

type pullRequestCommentsPageJSON struct {
	Repository *struct {
		PullRequest *struct {
			Comments struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					ID         string `json:"id"`
					DatabaseID int64  `json:"databaseId"`
					Author     *struct {
						Login string `json:"login"`
					} `json:"author"`
					Body      string `json:"body"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
					URL       string `json:"url"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// listPullRequestComments reads one bounded batch of the conversation comments of one pull request of the
// bound repository, oldest first.
func (c *Client) listPullRequestComments(ctx context.Context, a *pullRequestCommentArguments) (*CommentList, error) {
	const op = "list pull request comments"
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": a.Number,
		"first": a.Limit, "after": nil}
	if a.after != "" {
		variables["after"] = a.after
	}
	var page pullRequestCommentsPageJSON
	if err := c.graphql(ctx, op, pullRequestCommentsQuery, variables, &page); err != nil {
		return nil, err
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.PullRequest == nil {
		if c.issueNumber(ctx, a.Number) {
			return nil, c.issueRefusal(a.Number)
		}
		return nil, notFound(op, subject{in: c.target, what: "pull request #" + strconv.Itoa(a.Number)})
	}
	comments := page.Repository.PullRequest.Comments
	result := &CommentList{Comments: make([]Comment, 0, len(comments.Nodes))}
	for _, node := range comments.Nodes {
		if node.ID == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a comment without a usable identifier"}
		}
		comment := Comment{ID: node.ID, DatabaseID: node.DatabaseID, Body: node.Body, CreatedAt: node.CreatedAt,
			UpdatedAt: node.UpdatedAt, URL: node.URL}
		if node.Author != nil {
			comment.Author = node.Author.Login
		}
		result.Comments = append(result.Comments, comment)
	}
	if comments.PageInfo.HasNextPage {
		if comments.PageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further comments without a cursor"}
		}
		result.HasMore, result.NextCursor = true, encodeCursor(a.binding, comments.PageInfo.EndCursor)
	}
	return result, nil
}

// createPullRequestComment writes exactly one conversation comment on one pull request of the bound
// repository, through the same Issue Comments REST route github.comments.create uses, in one request that is
// never repeated. The pull request is confirmed first, so an issue number is refused.
func (c *Client) createPullRequestComment(ctx context.Context, number int, body string) (*Comment, error) {
	const op = "create pull request comment"
	if err := c.checkPullRequestForComment(ctx, op, number); err != nil {
		return nil, err
	}
	var raw restCommentJSON
	if err := c.restChange(ctx, op, http.MethodPost, issuePath(c.target, number)+"/comments",
		map[string]any{"body": body}, &raw); err != nil {
		return nil, err
	}
	if raw.NodeID == "" {
		return nil, invalidResponse(op, true)
	}
	comment := &Comment{ID: raw.NodeID, DatabaseID: raw.ID, Body: raw.Body, CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt, URL: raw.HTMLURL}
	if raw.User != nil {
		comment.Author = raw.User.Login
	}
	return comment, nil
}
