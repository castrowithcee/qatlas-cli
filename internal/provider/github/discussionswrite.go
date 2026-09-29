package github

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The discussion write tools create and change discussion comments and create discussions of one repository
// through GraphQL. Every object identifier a caller passes (a comment, or a category) is read back first and
// must belong to the bound repository, so a foreign identifier is refused before anything changes. A comment
// can be answered only at its top level, since GitHub allows one level of replies. Every mutation is sent
// once and never repeated. Verified 2026-09-29 against
// https://docs.github.com/en/graphql/reference/mutations (addDiscussionComment, updateDiscussionComment,
// deleteDiscussionComment, createDiscussion): creating adds a new object on each request and is therefore not
// idempotent; updating to a given body leaves the same state and is idempotent; deleting the same comment
// twice answers an error the second time, so its idempotency is unknown, as for issue comments. Writing needs
// repo (or public_repo for a public repository) on a classic token, or Discussions: read and write on a
// fine-grained token.

const discussionCommentSensitivity = "github-discussion-comment"

const discussionWritePermission = "GitHub refused this change of a discussion; it needs repo (or public_repo " +
	"for a public repository) on a classic token, or Discussions: read and write on a fine-grained token"

const discussionCommentWriteOutput = `{"type":"object","properties":{"id":{"type":"string"},` +
	`"database_id":{"type":"integer"},"author":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"url":{"type":"string"}},"required":["id"],"additionalProperties":false}`

var discussionCommentsCreate = capability.Descriptor{
	ID:      Provider + ".discussioncomments.create",
	Version: 1,
	Title:   "Comment on a GitHub discussion",
	Description: "Add a top-level comment to a discussion of a repository an explicit connection allows, or, " +
		"with reply_to, a reply to a top-level comment of the same discussion; the written body is not echoed",
	Tags:                       []string{"github", "discussions", "comments", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"body":{"type":"string","minLength":1,"maxLength":65536},`+
		`"reply_to":`+discussionIDSchema, "number", "body"),
	OutputSchema: json.RawMessage(discussionCommentWriteOutput),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Discussion number in the repository", Required: true},
		{Name: "body", Description: "Comment body in Markdown, 1 to 65536 characters", Required: true},
		{Name: "reply_to", Description: "Node id of a top-level comment of this discussion, as " +
			"github.discussioncomments.list reports it, to answer that comment; a new top-level comment when omitted"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Node id of the new comment"},
		{Name: "database_id", Description: "Numeric identifier of the new comment"},
		{Name: "url", Description: "Web address of the new comment"},
	},
	Examples: []capability.Example{{
		Description: "Answer a top-level comment",
		Arguments:   json.RawMessage(`{"number":12,"body":"Thanks, that worked.","reply_to":"DC_kwDOExample"}`),
	}},
}

var discussionCommentsUpdate = capability.Descriptor{
	ID:      Provider + ".discussioncomments.update",
	Version: 1,
	Title:   "Update a GitHub discussion comment",
	Description: "Replace the body of one discussion comment or reply of a repository an explicit connection " +
		"allows, by its node id; a comment of another repository is refused",
	Tags:                       []string{"github", "discussions", "comments", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"comment_id":`+discussionIDSchema+`,"body":{"type":"string","minLength":1,`+
		`"maxLength":65536}`, "comment_id", "body"),
	OutputSchema: json.RawMessage(discussionCommentWriteOutput),
	Arguments: []capability.Argument{
		{Name: "comment_id", Description: "Node id of the discussion comment or reply, as " +
			"github.discussioncomments.list reports it", Required: true},
		{Name: "body", Description: "New comment body in Markdown, 1 to 65536 characters; replaces the current " +
			"body", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Node id of the comment"},
		{Name: "url", Description: "Web address of the comment"},
	},
	Examples: []capability.Example{{
		Description: "Correct a discussion comment",
		Arguments:   json.RawMessage(`{"comment_id":"DC_kwDOExample","body":"Fixed in the latest build."}`),
	}},
}

var discussionCommentsDelete = capability.Descriptor{
	ID:      Provider + ".discussioncomments.delete",
	Version: 1,
	Title:   "Delete a GitHub discussion comment",
	Description: "Delete one discussion comment or reply of a repository an explicit connection allows " +
		"permanently, by its node id; offered only where a connection's tools list names it; a comment of " +
		"another repository is refused",
	Tags:                       []string{"github", "discussions", "comments", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, discussionCommentSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"comment_id":`+discussionIDSchema, "comment_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
		`"comment_id":{"type":"string"}},"required":["deleted","comment_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "comment_id", Description: "Node id of the discussion comment or reply to delete", Required: true},
	},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True once GitHub deleted the comment"},
		{Name: "comment_id", Description: "Node id of the deleted comment"},
	},
	Examples: []capability.Example{{
		Description: "Delete a discussion comment",
		Arguments:   json.RawMessage(`{"comment_id":"DC_kwDOExample"}`),
	}},
}

var discussionsCreate = capability.Descriptor{
	ID:      Provider + ".discussions.create",
	Version: 1,
	Title:   "Create a GitHub discussion",
	Description: "Start a discussion in a category of a repository an explicit connection allows; the category " +
		"must belong to that repository",
	Tags:                       []string{"github", "discussions", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"title":`+titleSchema+`,"body":{"type":"string","minLength":1,"maxLength":65536},`+
		`"category_id":`+discussionIDSchema, "title", "body", "category_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"number":{"type":"integer"},"url":{"type":"string"}},"required":["id","number"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "title", Description: "Discussion title, 1 to 256 characters", Required: true},
		{Name: "body", Description: "Discussion body in Markdown, 1 to 65536 characters", Required: true},
		{Name: "category_id", Description: "Category id, as github.discussioncategories.list reports it", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Node id of the new discussion"},
		{Name: "number", Description: "Number of the new discussion in the repository"},
		{Name: "url", Description: "Web address of the new discussion"},
	},
	Examples: []capability.Example{{
		Description: "Start a discussion",
		Arguments:   json.RawMessage(`{"title":"Roadmap ideas","body":"What should come next?","category_id":"DIC_kwDOExample"}`),
	}},
}

// DiscussionCommentWritten is the answer of a comment change; the written body is not echoed.
type DiscussionCommentWritten struct {
	ID         string `json:"id"`
	DatabaseID int64  `json:"database_id,omitempty"`
	Author     string `json:"author,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	URL        string `json:"url,omitempty"`
}

// DiscussionCreated is the answer of github.discussions.create.
type DiscussionCreated struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

type discussionRepositoryJSON struct {
	Name  string `json:"name"`
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
}

func (c *Client) ownsRepository(r *discussionRepositoryJSON) bool {
	return r != nil && strings.EqualFold(r.Name, c.target.repo) && strings.EqualFold(r.Owner.Login, c.target.owner)
}

const discussionCommentNodeQuery = `query($id:ID!){node(id:$id){__typename ... on DiscussionComment{id ` +
	`replyTo{id} discussion{number repository{name owner{login}}}}}}`

type discussionCommentNodeJSON struct {
	Node *struct {
		Typename string `json:"__typename"`
		ID       string `json:"id"`
		ReplyTo  *struct {
			ID string `json:"id"`
		} `json:"replyTo"`
		Discussion *struct {
			Number     int                       `json:"number"`
			Repository *discussionRepositoryJSON `json:"repository"`
		} `json:"discussion"`
	} `json:"node"`
}

// findDiscussionComment reads one discussion comment by node id and refuses it unless it belongs to the bound
// repository. It returns the discussion number and whether the comment is itself a reply.
func (c *Client) findDiscussionComment(ctx context.Context, op, id string) (int, bool, error) {
	var answer discussionCommentNodeJSON
	if err := c.graphql(ctx, op, discussionCommentNodeQuery, map[string]any{"id": id}, &answer); err != nil {
		return 0, false, actionsFailure(err, discussionWritePermission)
	}
	node := answer.Node
	if node == nil || node.Typename != "DiscussionComment" || node.Discussion == nil ||
		!c.ownsRepository(node.Discussion.Repository) {
		return 0, false, notFound(op, subject{in: c.target, what: "this discussion comment"})
	}
	return node.Discussion.Number, node.ReplyTo != nil, nil
}

const discussionCommentFields = `comment{id databaseId author{login} createdAt updatedAt url}`

type discussionCommentMutationJSON struct {
	Comment *struct {
		ID         string `json:"id"`
		DatabaseID int64  `json:"databaseId"`
		Author     *struct {
			Login string `json:"login"`
		} `json:"author"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
		URL       string `json:"url"`
	} `json:"comment"`
}

func (a discussionCommentMutationJSON) written(op string) (*DiscussionCommentWritten, error) {
	if a.Comment == nil || a.Comment.ID == "" {
		return nil, invalidResponse(op, true)
	}
	w := &DiscussionCommentWritten{ID: a.Comment.ID, DatabaseID: a.Comment.DatabaseID,
		CreatedAt: a.Comment.CreatedAt, UpdatedAt: a.Comment.UpdatedAt, URL: a.Comment.URL}
	if a.Comment.Author != nil {
		w.Author = a.Comment.Author.Login
	}
	return w, nil
}

const addDiscussionCommentMutation = `mutation($discussion:ID!,$body:String!,$replyTo:ID){` +
	`addDiscussionComment(input:{discussionId:$discussion,body:$body,replyToId:$replyTo}){` +
	discussionCommentFields + `}}`

// CreateDiscussionComment adds a top-level comment, or a reply to a top-level comment of the same discussion.
func (c *Client) CreateDiscussionComment(ctx context.Context, number int, body, replyTo string) (*DiscussionCommentWritten, error) {
	const op = "create discussion comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if err := checkNumber(number); err != nil {
		return nil, err
	}
	if err := checkCommentBody(body); err != nil {
		return nil, err
	}
	if replyTo != "" {
		commentNumber, isReply, err := c.findDiscussionComment(ctx, op, replyTo)
		if err != nil {
			return nil, err
		}
		if commentNumber != number {
			return nil, invalidRequest("reply_to names a comment of another discussion")
		}
		if isReply {
			return nil, invalidRequest("reply_to must name a top-level comment; GitHub allows one level of replies")
		}
	}
	discussion, err := c.getDiscussion(ctx, number)
	if err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	variables := map[string]any{"discussion": discussion.Discussion.ID, "body": body, "replyTo": nil}
	if replyTo != "" {
		variables["replyTo"] = replyTo
	}
	var answer struct {
		Add discussionCommentMutationJSON `json:"addDiscussionComment"`
	}
	if err := c.mutate(ctx, op, addDiscussionCommentMutation, variables, &answer); err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	return answer.Add.written(op)
}

const updateDiscussionCommentMutation = `mutation($id:ID!,$body:String!){` +
	`updateDiscussionComment(input:{commentId:$id,body:$body}){` + discussionCommentFields + `}}`

// UpdateDiscussionComment replaces the body of one discussion comment of the bound repository.
func (c *Client) UpdateDiscussionComment(ctx context.Context, id, body string) (*DiscussionCommentWritten, error) {
	const op = "update discussion comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if err := checkCommentBody(body); err != nil {
		return nil, err
	}
	if _, _, err := c.findDiscussionComment(ctx, op, id); err != nil {
		return nil, err
	}
	var answer struct {
		Update discussionCommentMutationJSON `json:"updateDiscussionComment"`
	}
	if err := c.mutate(ctx, op, updateDiscussionCommentMutation, map[string]any{"id": id, "body": body},
		&answer); err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	return answer.Update.written(op)
}

const deleteDiscussionCommentMutation = `mutation($id:ID!){deleteDiscussionComment(input:{id:$id}){` +
	`comment{id}}}`

// DeleteDiscussionComment removes one discussion comment of the bound repository permanently.
func (c *Client) DeleteDiscussionComment(ctx context.Context, id string) (any, error) {
	const op = "delete discussion comment"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if _, _, err := c.findDiscussionComment(ctx, op, id); err != nil {
		return nil, err
	}
	var answer struct {
		Delete discussionCommentMutationJSON `json:"deleteDiscussionComment"`
	}
	if err := c.mutate(ctx, op, deleteDiscussionCommentMutation, map[string]any{"id": id}, &answer); err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	return map[string]any{"deleted": true, "comment_id": id}, nil
}

const discussionCategoryNodeQuery = `query($owner:String!,$name:String!,$id:ID!){` +
	`repository(owner:$owner,name:$name){id} node(id:$id){__typename ... on DiscussionCategory{id ` +
	`repository{name owner{login}}}}}`

const createDiscussionMutation = `mutation($repository:ID!,$category:ID!,$title:String!,$body:String!){` +
	`createDiscussion(input:{repositoryId:$repository,categoryId:$category,title:$title,body:$body}){` +
	`discussion{id number url}}}`

// CreateDiscussion starts a discussion in a category that belongs to the bound repository.
func (c *Client) CreateDiscussion(ctx context.Context, title, body, category string) (*DiscussionCreated, error) {
	const op = "create discussion"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if title = strings.TrimSpace(title); title == "" || utf8.RuneCountInString(title) > 256 {
		return nil, invalidRequest("title must hold 1 to 256 characters")
	}
	if err := checkCommentBody(body); err != nil {
		return nil, err
	}
	var lookup struct {
		Repository *struct {
			ID string `json:"id"`
		} `json:"repository"`
		Node *struct {
			Typename   string                    `json:"__typename"`
			ID         string                    `json:"id"`
			Repository *discussionRepositoryJSON `json:"repository"`
		} `json:"node"`
	}
	if err := c.graphql(ctx, op, discussionCategoryNodeQuery,
		map[string]any{"owner": c.target.owner, "name": c.target.repo, "id": category}, &lookup); err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	if lookup.Repository == nil || lookup.Repository.ID == "" {
		return nil, notFound(op, subject{in: c.target})
	}
	if lookup.Node == nil || lookup.Node.Typename != "DiscussionCategory" || !c.ownsRepository(lookup.Node.Repository) {
		return nil, notFound(op, subject{in: c.target, what: "this discussion category"})
	}
	var answer struct {
		Create struct {
			Discussion *struct {
				ID     string `json:"id"`
				Number int    `json:"number"`
				URL    string `json:"url"`
			} `json:"discussion"`
		} `json:"createDiscussion"`
	}
	if err := c.mutate(ctx, op, createDiscussionMutation, map[string]any{"repository": lookup.Repository.ID,
		"category": category, "title": title, "body": body}, &answer); err != nil {
		return nil, actionsFailure(err, discussionWritePermission)
	}
	d := answer.Create.Discussion
	if d == nil || d.ID == "" || d.Number < 1 {
		return nil, invalidResponse(op, true)
	}
	return &DiscussionCreated{ID: d.ID, Number: d.Number, URL: d.URL}, nil
}

// discussionWriteHandler decodes and checks the arguments and the repository before a credential is
// resolved, so a refused request never becomes a provider call.
func discussionWriteHandler[A any](op string, check func(*A) error,
	call func(context.Context, *Client, *A) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var a A
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, unreadable(op)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&a); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &a))
	}
}

type discussionCommentCreateArguments struct {
	Number  int    `json:"number"`
	Body    string `json:"body"`
	ReplyTo string `json:"reply_to"`
}

type discussionCommentChangeArguments struct {
	CommentID string `json:"comment_id"`
	Body      string `json:"body"`
}

type discussionCreateArguments struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	CategoryID string `json:"category_id"`
}

func checkDiscussionNodeID(name, id string) error {
	if !discussionIDPattern.MatchString(id) {
		return invalidRequest(name + " must be a node id as github.discussioncomments.list or " +
			"github.discussioncategories.list reports it")
	}
	return nil
}

func discussionWriteOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: discussionCommentsCreate, Handler: discussionWriteHandler(discussionCommentsCreate.ID,
			func(a *discussionCommentCreateArguments) error {
				if err := checkNumber(a.Number); err != nil {
					return err
				}
				if a.ReplyTo != "" {
					if err := checkDiscussionNodeID("reply_to", a.ReplyTo); err != nil {
						return err
					}
				}
				return checkCommentBody(a.Body)
			},
			func(ctx context.Context, c *Client, a *discussionCommentCreateArguments) (any, error) {
				return c.CreateDiscussionComment(ctx, a.Number, a.Body, a.ReplyTo)
			})},
		{Descriptor: discussionCommentsUpdate, Handler: discussionWriteHandler(discussionCommentsUpdate.ID,
			func(a *discussionCommentChangeArguments) error {
				if err := checkDiscussionNodeID("comment_id", a.CommentID); err != nil {
					return err
				}
				return checkCommentBody(a.Body)
			},
			func(ctx context.Context, c *Client, a *discussionCommentChangeArguments) (any, error) {
				return c.UpdateDiscussionComment(ctx, a.CommentID, a.Body)
			})},
		{Descriptor: discussionCommentsDelete, Handler: discussionWriteHandler(discussionCommentsDelete.ID,
			func(a *discussionCommentChangeArguments) error {
				return checkDiscussionNodeID("comment_id", a.CommentID)
			},
			func(ctx context.Context, c *Client, a *discussionCommentChangeArguments) (any, error) {
				return c.DeleteDiscussionComment(ctx, a.CommentID)
			})},
		{Descriptor: discussionsCreate, Handler: discussionWriteHandler(discussionsCreate.ID,
			func(a *discussionCreateArguments) error {
				if err := checkDiscussionNodeID("category_id", a.CategoryID); err != nil {
					return err
				}
				if t := strings.TrimSpace(a.Title); t == "" || utf8.RuneCountInString(t) > 256 {
					return invalidRequest("title must hold 1 to 256 characters")
				}
				return checkCommentBody(a.Body)
			},
			func(ctx context.Context, c *Client, a *discussionCreateArguments) (any, error) {
				return c.CreateDiscussion(ctx, a.Title, a.Body, a.CategoryID)
			})},
	}
}
