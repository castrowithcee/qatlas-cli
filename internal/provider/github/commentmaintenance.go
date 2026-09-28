package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// github.comments.update and github.comments.delete change one existing issue comment of a repository, by
// its REST comment_id rather than the node identifier github.comments.list and github.comments.create
// answer as id, since only the numeric identifier addresses GitHub's issue comment REST route; it appears
// in a comment's url after "#issuecomment-". Both tools read the comment first: the GET route already scopes
// it to the bound repository, so a comment of another repository answers not-found, and the issue it belongs
// to is read the way readIssue reads it for every other issue change, so a pull request conversation comment
// is refused the same way a pull request number is, before anything changes. github.comments.delete removes
// a comment permanently and is offered only where a connection's tools list names it, the way
// github.labels.delete is.

// commentSensitivity classifies github.comments.delete as its own guarded change, the way labelSensitivity
// classifies github.labels.delete: deleting a comment cannot be undone.
const commentSensitivity = "github-comment"

var commentsUpdate = capability.Descriptor{
	ID:      Provider + ".comments.update",
	Version: 1,
	Title:   "Update a GitHub issue comment",
	Description: "Replace the body of one comment of a repository an explicit connection allows, by its " +
		"comment_id; a comment of another repository, or a pull request conversation comment, is refused",
	Tags:                       []string{"github", "issues", "comments", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"comment_id":`+actionsIDSchema+`,"body":{"type":"string","minLength":1,`+
		`"maxLength":65536}`, "comment_id", "body"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + commentProperties + `},` + commentRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "comment_id", Description: "Numeric identifier of the issue comment, as it appears in its url " +
			"after \"#issuecomment-\"", Required: true},
		{Name: "body", Description: "New comment body in Markdown, 1 to 65536 characters; replaces the " +
			"current body, stored as given", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Comment identifier"},
		commentDatabaseIDField,
		{Name: "url", Description: "Web address of the comment"},
	},
	Examples: []capability.Example{{
		Description: "Correct a comment",
		Arguments:   json.RawMessage(`{"comment_id":123456789,"body":"Fixed in the latest build."}`),
	}},
}

const commentDeletedOutput = `{"type":"object","properties":{"deleted":{"type":"boolean"},` +
	`"comment_id":{"type":"integer"}},"required":["deleted","comment_id"],"additionalProperties":false}`

var commentsDelete = capability.Descriptor{
	ID:      Provider + ".comments.delete",
	Version: 1,
	Title:   "Delete a GitHub issue comment",
	Description: "Delete one comment of a repository an explicit connection allows permanently, by its " +
		"comment_id; offered only where a connection's tools list names it; a comment of another repository, " +
		"or a pull request conversation comment, is refused",
	Tags:                       []string{"github", "issues", "comments", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, commentSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"comment_id":`+actionsIDSchema, "comment_id"),
	OutputSchema:               json.RawMessage(commentDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "comment_id", Description: "Numeric identifier of the issue comment to delete", Required: true},
	},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True once GitHub deleted the comment"},
		{Name: "comment_id", Description: "Identifier of the deleted comment"},
	},
	Examples: []capability.Example{{
		Description: "Delete a comment",
		Arguments:   json.RawMessage(`{"comment_id":123456789}`),
	}},
}

// commentMaintenanceArguments holds the arguments of both tools; the input schema of each tool admits only
// its own.
type commentMaintenanceArguments struct {
	CommentID int64  `json:"comment_id"`
	Body      string `json:"body"`
}

func checkCommentMaintenanceID(a *commentMaintenanceArguments, _ target) error {
	if a.CommentID < 1 {
		return invalidRequest("comment_id must be a positive comment identifier")
	}
	return nil
}

func checkCommentMaintenanceUpdate(a *commentMaintenanceArguments, bound target) error {
	if err := checkCommentMaintenanceID(a, bound); err != nil {
		return err
	}
	return checkCommentBody(a.Body)
}

// commentMaintenanceHandler decodes and checks the arguments and the repository before a credential is
// resolved, so a refused request never becomes a provider call, the way labelsHandler does for the label
// tools.
func commentMaintenanceHandler(id string, check func(*commentMaintenanceArguments, target) error,
	call func(context.Context, *Client, *commentMaintenanceArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments commentMaintenanceArguments
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

func commentMaintenanceOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: commentsUpdate, Handler: commentMaintenanceHandler(commentsUpdate.ID, checkCommentMaintenanceUpdate,
			func(ctx context.Context, c *Client, a *commentMaintenanceArguments) (any, error) {
				return c.UpdateComment(ctx, a.CommentID, a.Body)
			})},
		{Descriptor: commentsDelete, Handler: commentMaintenanceHandler(commentsDelete.ID, checkCommentMaintenanceID,
			func(ctx context.Context, c *Client, a *commentMaintenanceArguments) (any, error) {
				return c.DeleteComment(ctx, a.CommentID)
			})},
	}
}

// Permission message of the comment maintenance tools; the same scopes github.comments.create needs, since
// GitHub keeps a comment under a repository's issues rather than under a scope of its own.
const commentsChangePermission = "GitHub refused this change of a comment; it needs repo on a classic " +
	"token, or Issues: read and write on a fine-grained token"

// commentPath is the REST route of one issue comment of the bound repository, addressed by its numeric
// identifier.
func (c *Client) commentPath(commentID int64) string {
	return c.repoPath("issues/comments/" + strconv.FormatInt(commentID, 10))
}

// restCommentDetailJSON adds the issue_url the create and list routes do not need, so an update or a delete
// can find the issue a comment belongs to.
type restCommentDetailJSON struct {
	restCommentJSON
	IssueURL string `json:"issue_url"`
}

// issueNumberFromCommentURL reads the issue number from the issue_url GitHub reports for one comment, of the
// form .../repos/OWNER/REPO/issues/NUMBER.
func issueNumberFromCommentURL(issueURL string) (int, error) {
	idx := strings.LastIndex(issueURL, "/")
	if idx < 0 {
		return 0, invalidResponse("read comment", false)
	}
	number, err := strconv.Atoi(issueURL[idx+1:])
	if err != nil || number < 1 {
		return 0, invalidResponse("read comment", false)
	}
	return number, nil
}

// findCommentIssue reads one issue comment of the bound repository by its numeric identifier and locates the
// issue it belongs to, refusing a pull request the way readIssue refuses a pull request number, before an
// update or a delete changes anything. The GET route already scopes the comment to the bound repository, so
// a comment of another repository answers not-found.
func (c *Client) findCommentIssue(ctx context.Context, op string, commentID int64) (*restCommentDetailJSON, error) {
	var raw restCommentDetailJSON
	if err := c.rest(ctx, op, c.commentPath(commentID), &raw); err != nil {
		return nil, actionsFailure(err, commentsChangePermission)
	}
	if raw.NodeID == "" {
		return nil, invalidEntry(op, "a comment")
	}
	number, err := issueNumberFromCommentURL(raw.IssueURL)
	if err != nil {
		return nil, err
	}
	if _, err := c.readIssue(ctx, op, number); err != nil {
		return nil, err
	}
	return &raw, nil
}

// UpdateComment replaces the body of one issue comment of the bound repository. The comment is read first to
// bind it to this repository and to refuse a pull request conversation comment; the change itself is one
// request that is never repeated.
func (c *Client) UpdateComment(ctx context.Context, commentID int64, body string) (*Comment, error) {
	const op = "update comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if err := checkCommentBody(body); err != nil {
		return nil, err
	}
	if _, err := c.findCommentIssue(ctx, op, commentID); err != nil {
		return nil, err
	}
	var raw restCommentJSON
	if err := c.restChange(ctx, op, http.MethodPatch, c.commentPath(commentID), map[string]any{"body": body},
		&raw); err != nil {
		return nil, actionsFailure(err, commentsChangePermission)
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

// DeleteComment removes one issue comment of the bound repository permanently. The comment is read first the
// same way UpdateComment reads it; the delete itself is one request that is never repeated.
func (c *Client) DeleteComment(ctx context.Context, commentID int64) (any, error) {
	const op = "delete comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if _, err := c.findCommentIssue(ctx, op, commentID); err != nil {
		return nil, err
	}
	if err := c.restChange(ctx, op, http.MethodDelete, c.commentPath(commentID), nil, nil); err != nil {
		return nil, actionsFailure(err, commentsChangePermission)
	}
	return map[string]any{"deleted": true, "comment_id": commentID}, nil
}

// commentMaintenanceSubject names the comment a repository path below /issues/comments addresses:
// "the comment identifier" for the collection route, which no tool ever calls without a comment_id, or the
// comment itself for issues/comments/ID, the way labelsSubject names a label below /labels. It is empty for
// any other path, such as an issue path restSubject already names.
func commentMaintenanceSubject(path string) string {
	if number, ok := trimNumericSuffix(path, "issues/comments/"); ok {
		return "comment " + number
	}
	return ""
}
