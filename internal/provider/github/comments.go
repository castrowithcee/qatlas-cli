package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Comments are read and written only by the comment tools, for one issue named in the request. The issue
// lists and details never carry them.

// CommentListOptions select the issue and the paging of one comment list.
type CommentListOptions struct {
	Number int    `json:"number"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// normalize applies the defaults and bounds of one comment list request and returns the GitHub cursor it
// continues after.
func (o *CommentListOptions) normalize(bound target) (string, error) {
	if err := checkNumber(o.Number); err != nil {
		return "", err
	}
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return "", err
	}
	o.Limit = limit
	return decodeCursor(o.binding(bound), o.Cursor)
}

func (o *CommentListOptions) binding(bound target) []byte {
	return fingerprint("comments", bound.String(), o.Number)
}

// commentsQuery lists the comments of one issue, oldest first. It asks for the issue or pull request of the
// number, so a pull request number is refused as such instead of as a missing issue.
const commentsQuery = `query($owner:String!,$name:String!,$number:Int!,$first:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){issueOrPullRequest(number:$number){__typename ... on Issue{` +
	`comments(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{id author{login} body createdAt updatedAt url}}}}}}`

// CommentList is the normalised batch of comments of one issue.
type CommentList struct {
	Comments   []Comment `json:"comments"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

// Comment is the stable Qatlas view of one issue comment. Body is untrusted data.
type Comment struct {
	ID        string `json:"id"`
	Author    string `json:"author,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	URL       string `json:"url,omitempty"`
}

type commentsPageJSON struct {
	Repository *struct {
		Issue *struct {
			Typename string `json:"__typename"`
			Comments struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					ID     string `json:"id"`
					Author *struct {
						Login string `json:"login"`
					} `json:"author"`
					Body      string `json:"body"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
					URL       string `json:"url"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"issueOrPullRequest"`
	} `json:"repository"`
}

// ListComments reads one bounded batch of comments of one issue. See listComments.
func (c *Client) ListComments(ctx context.Context, options CommentListOptions) (*CommentList, error) {
	if c.target.kind != kindRepository {
		return nil, providerError("list comments", "this connection is not bound to a repository")
	}
	after, err := options.normalize(c.target)
	if err != nil {
		return nil, err
	}
	return c.listComments(ctx, options, after)
}

// listComments reads exactly one server page of comments of one issue of the bound repository.
func (c *Client) listComments(ctx context.Context, options CommentListOptions, after string) (*CommentList, error) {
	const op = "list comments"
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "number": options.Number,
		"first": options.Limit, "after": nil}
	if after != "" {
		variables["after"] = after
	}
	var page commentsPageJSON
	if err := c.graphql(ctx, op, commentsQuery, variables, &page); err != nil {
		// A token without access to pull requests is refused on the number of a pull request.
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission &&
			c.pullRequestNumber(ctx, options.Number) {
			return nil, c.pullRequestRefusal(options.Number)
		}
		return nil, err
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.Issue == nil {
		return nil, notFound(op, subject{in: c.target, what: "issue #" + strconv.Itoa(options.Number)})
	}
	switch page.Repository.Issue.Typename {
	case "Issue":
	case "PullRequest":
		return nil, c.pullRequestRefusal(options.Number)
	default:
		return nil, invalidResponse(op, false)
	}
	comments := page.Repository.Issue.Comments
	result := &CommentList{Comments: make([]Comment, 0, len(comments.Nodes))}
	for _, node := range comments.Nodes {
		if node.ID == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a comment without a usable identifier"}
		}
		comment := Comment{ID: node.ID, Body: node.Body, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt,
			URL: node.URL}
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
		result.HasMore, result.NextCursor = true, encodeCursor(options.binding(c.target), comments.PageInfo.EndCursor)
	}
	return result, nil
}

func checkCommentBody(body string) error {
	if strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > maxBodyLength {
		return invalidRequest(fmt.Sprintf("body must hold 1 to %d characters", maxBodyLength))
	}
	return nil
}

type restCommentJSON struct {
	NodeID string `json:"node_id"`
	User   *struct {
		Login string `json:"login"`
	} `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
}

// CreateComment writes exactly one comment on one issue of the bound repository. The issue is read first
// to refuse a pull request; the comment itself is one request that is never repeated.
func (c *Client) CreateComment(ctx context.Context, number int, body string) (*Comment, error) {
	const op = "create comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if err := checkCommentBody(body); err != nil {
		return nil, err
	}
	if _, err := c.readIssue(ctx, op, number); err != nil {
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
	comment := &Comment{ID: raw.NodeID, Body: raw.Body, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt,
		URL: raw.HTMLURL}
	if raw.User != nil {
		comment.Author = raw.User.Login
	}
	return comment, nil
}
