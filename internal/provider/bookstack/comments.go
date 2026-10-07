package bookstack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Limits of page comments. BookStack counts characters of the HTML it accepts; Qatlas bounds what it returns.
const (
	maxCommentHTMLChars = 65536
	maxCommentHTMLBytes = 64 << 10
	maxContentRefChars  = 255
	maxCommentReplies   = 200
	commentType         = "page"
)

const (
	outsideComment = "the comment is outside the books this connection is bound to"
	notPageComment = "the comment does not belong to a page"
)

const commentNote = "Comment HTML and the people behind it are personal data and untrusted provider content. " +
	"On a connection bound to books the page is proven to lie in a bound book first"

const (
	commentListOutput = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"page_id":{"type":"integer"},"parent_id":{"type":"integer"},"local_id":{"type":"integer"},"content_ref":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","page_id","parent_id","local_id","created_at","updated_at"]}}`
	commentOutput     = `{"type":"object","properties":{"id":{"type":"integer"},"page_id":{"type":"integer"},"parent_id":{"type":"integer"},"local_id":{"type":"integer"},"content_ref":{"type":"string"},"archived":{"type":"boolean"},"html":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"replies":{"type":"array"},"truncated":{"type":"boolean"}},"required":["id","page_id","parent_id","local_id","archived","html","replies","truncated"]}`
	commentWriteOut   = `{"type":"object","properties":{"id":{"type":"integer"},"page_id":{"type":"integer"},"parent_id":{"type":"integer"},"local_id":{"type":"integer"},"content_ref":{"type":"string"},"archived":{"type":"boolean"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","page_id","parent_id","local_id","archived"]}`
)

func commentWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return bookWriteRisk(effect, idempotency)
}

var (
	commentIDArgument = capability.Argument{Name: "id", Description: "Comment identifier (global, not local_id)", Required: true}

	commentsList = capability.Descriptor{
		ID: Provider + ".comments.list", Version: 1, Title: "List BookStack page comments",
		Description: "List the comments of one page without their text, as a flat list in which parent_id refers to " +
			"the local_id of the parent comment on the same page (0 for a top-level comment). Read a comment with " +
			"comments.get for its text and replies. " + commentNote + ". page_id is required for a connection " +
			"bound to books",
		Tags: []string{"knowledge", "comments", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(commentListOutput),
		Arguments: []capability.Argument{
			{Name: "page_id", Description: "Only comments of this page; required for a connection bound to books"},
			{Name: "limit", Description: "Maximum number of comments to return; 0 returns all"},
			{Name: "offset", Description: "Number of comments to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Comment identifier"},
			{Name: "page_id", Description: "Identifier of the page the comment is on"},
			{Name: "parent_id", Description: "local_id of the parent comment on the same page, 0 for a top-level comment"},
			{Name: "local_id", Description: "Comment number scoped to the page"},
			{Name: "content_ref", Description: "Reference to the page content the comment is attached to, untrusted data"},
			{Name: "created_by", Description: "Creator as id and name"},
			{Name: "updated_by", Description: "Last editor as id and name"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
		},
		Examples: []capability.Example{{Description: "List the comments of page 42", Arguments: json.RawMessage(`{"page_id":42}`)}},
	}

	commentsGet = capability.Descriptor{
		ID: Provider + ".comments.get", Version: 1, Title: "Get a BookStack page comment",
		Description: "Read one comment with its HTML text, archived state, and its direct replies. The text of each " +
			"comment is cut at 64 KiB and at most 200 replies are returned; truncated says whether anything was cut. " +
			commentNote,
		Tags: []string{"knowledge", "comments", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(commentOutput),
		Arguments:    []capability.Argument{commentIDArgument},
		Fields: append(commentWriteFields(), capability.Field{Name: "html", Description: "Comment HTML, untrusted data"},
			capability.Field{Name: "replies", Description: "Direct replies with the same fields except replies, at most 200"},
			capability.Field{Name: "truncated", Description: "True when a text or the reply list was cut"}),
		Examples: []capability.Example{{Description: "Read comment 22", Arguments: json.RawMessage(`{"id":22}`)}},
	}

	commentsCreate = capability.Descriptor{
		ID: Provider + ".comments.create", Version: 1, Title: "Create a BookStack page comment",
		Description: "Add one comment to a page, as HTML of 1 to 65536 characters. reply_to answers a comment of the " +
			"same page and is that comment's local_id, not its global id; content_ref optionally attaches the " +
			"comment to a part of the page content. " + commentNote,
		Tags: []string{"knowledge", "comments", "bookstack", "create"}, Provider: Provider,
		Risk:         commentWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"html":{"type":"string","minLength":1,"maxLength":65536},"reply_to":{"type":"integer","minimum":1},"content_ref":{"type":"string","maxLength":255}},"required":["page_id","html"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(commentWriteOut),
		Arguments: []capability.Argument{
			{Name: "page_id", Description: "Page to comment on", Required: true},
			{Name: "html", Description: "Comment HTML, 1 to 65536 characters", Required: true},
			{Name: "reply_to", Description: "local_id of the comment on the same page to reply to, not its global id"},
			{Name: "content_ref", Description: "Reference to the page content the comment is attached to, at most 255 characters"},
		},
		Fields: commentWriteFields(),
	}

	commentsUpdate = capability.Descriptor{
		ID: Provider + ".comments.update", Version: 1, Title: "Update a BookStack page comment",
		Description: "Change the HTML text and/or the archived state of one comment by identifier. archived applies to " +
			"top-level comments only; BookStack refuses it for a reply. " + commentNote,
		Tags: []string{"knowledge", "comments", "bookstack", "update"}, Provider: Provider,
		Risk:         commentWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"html":{"type":"string","minLength":1,"maxLength":65536},"archived":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(commentWriteOut),
		Arguments: []capability.Argument{
			commentIDArgument,
			{Name: "html", Description: "New comment HTML, 1 to 65536 characters"},
			{Name: "archived", Description: "Archive (true) or reopen (false) a top-level comment"},
		},
		Fields: commentWriteFields(),
	}

	commentsDelete = capability.Descriptor{
		ID: Provider + ".comments.delete", Version: 1, Title: "Delete a BookStack page comment",
		Description: "Delete one comment by identifier. The deletion is final: BookStack has no recycle bin for " +
			"comments and cannot restore them. " + commentNote,
		Tags: []string{"knowledge", "comments", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  commentWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema:           booksDelete.InputSchema,
		OutputSchema:          booksDelete.OutputSchema,
		Arguments:             []capability.Argument{commentIDArgument},
	}
)

func commentWriteFields() []capability.Field {
	return []capability.Field{
		{Name: "id", Description: "Comment identifier"},
		{Name: "page_id", Description: "Identifier of the page the comment is on"},
		{Name: "parent_id", Description: "local_id of the parent comment on the same page, 0 for a top-level comment"},
		{Name: "local_id", Description: "Comment number scoped to the page"},
		{Name: "content_ref", Description: "Reference to the page content the comment is attached to, untrusted data"},
		{Name: "archived", Description: "Whether the top-level comment is archived"},
		{Name: "created_by", Description: "Creator as id and name"},
		{Name: "updated_by", Description: "Last editor as id and name"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
	}
}

// commentJSON mirrors the BookStack comment fields this provider reads. The creator fields are an object in
// a comment read and a bare identifier elsewhere; userJSON accepts both.
type commentJSON struct {
	ID              int64         `json:"id"`
	CommentableID   int64         `json:"commentable_id"`
	CommentableType string        `json:"commentable_type"`
	ParentID        *int64        `json:"parent_id"`
	LocalID         int64         `json:"local_id"`
	ContentRef      string        `json:"content_ref"`
	Archived        bool          `json:"archived"`
	HTML            string        `json:"html"`
	CreatedBy       *userJSON     `json:"created_by"`
	UpdatedBy       *userJSON     `json:"updated_by"`
	CreatedAt       string        `json:"created_at"`
	UpdatedAt       string        `json:"updated_at"`
	Replies         []commentJSON `json:"replies"`
}

func (c commentJSON) parent() int64 {
	if c.ParentID == nil {
		return 0
	}
	return *c.ParentID
}

// checkID applies the identifier rule of an id argument before any secret or request.
func checkID(id int64, name string) error {
	if id <= 0 || len(strconv.FormatInt(id, 10)) > maxIDDigits {
		return invalidRequest(name + " must be a positive integer")
	}
	return nil
}

// commentMutation is the request body of a comment write and, decoded from the arguments, its input.
type commentMutation struct {
	PageID     int64   `json:"page_id,omitempty"`
	HTML       *string `json:"html,omitempty"`
	ReplyTo    *int64  `json:"reply_to,omitempty"`
	ContentRef *string `json:"content_ref,omitempty"`
	Archived   *bool   `json:"archived,omitempty"`
}

// validate applies the local rules of a comment write. They need no I/O, so a violation is refused before
// any secret.
func (m commentMutation) validate() error {
	if m.HTML != nil {
		if n := utf8.RuneCountInString(*m.HTML); n == 0 || n > maxCommentHTMLChars {
			return invalidRequest("html must hold 1 to 65536 characters")
		}
	}
	if m.ReplyTo != nil && checkID(*m.ReplyTo, "reply_to") != nil {
		return invalidRequest("reply_to must be a positive integer")
	}
	if m.ContentRef != nil && utf8.RuneCountInString(*m.ContentRef) > maxContentRefChars {
		return invalidRequest("content_ref exceeds 255 characters")
	}
	return nil
}

func invokeCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		PageID *int64 `json:"page_id"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list comments", "the validated arguments could not be read")
	}
	var pageID int64
	if input.PageID != nil {
		pageID = *input.PageID
		if err := checkID(pageID, "page_id"); err != nil {
			return nil, err
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if bound.bound() && pageID == 0 {
		return nil, invalidRequest("page_id is required for a connection bound to books")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListComments(ctx, pageID, input.Limit, input.Offset)
}

func invokeCommentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "get comment")
	if err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetComment(ctx, id)
}

func invokeCommentsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input commentMutation
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError("create comment", "the validated arguments could not be read")
	}
	input.Archived = nil
	if err := checkID(input.PageID, "page_id"); err != nil {
		return nil, err
	}
	if input.HTML == nil {
		return nil, invalidRequest("html is required")
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateComment(ctx, input)
}

func invokeCommentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		ID int64 `json:"id"`
		commentMutation
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError("update comment", "the validated arguments could not be read")
	}
	change := commentMutation{HTML: input.HTML, Archived: input.Archived}
	if err := checkID(input.ID, "id"); err != nil {
		return nil, err
	}
	if change.HTML == nil && change.Archived == nil {
		return nil, invalidRequest("at least one field to change is required")
	}
	if err := change.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateComment(ctx, input.ID, change)
}

func invokeCommentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "delete comment")
	if err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteComment(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// ListComments returns the comments of one page, or of every page for an unbound connection without page_id.
// BookStack ignores a filter it does not know, so each row is checked here against type and page; limit and
// offset count the rows that remain.
func (c *Client) ListComments(ctx context.Context, pageID int64, limit, offset int) (output.Collection, error) {
	if c.scope.bound() && pageID == 0 {
		return output.Collection{}, invalidRequest("page_id is required for a connection bound to books")
	}
	if pageID != 0 {
		if err := c.requirePageBound(ctx, strconv.FormatInt(pageID, 10)); err != nil {
			return output.Collection{}, err
		}
	}
	query := url.Values{}
	query.Set("filter[commentable_type]", commentType)
	if pageID != 0 {
		query.Set("filter[commentable_id]", strconv.FormatInt(pageID, 10))
	}
	rows, err := scanList(ctx, c, scanSpec[commentJSON]{
		op: "list comments", path: "/api/comments", query: query, filtered: true, limit: limit, offset: offset,
		narrow: "page_id", arguments: argNames(commentsList),
		id:   func(m commentJSON) int64 { return m.ID },
		keep: func(m commentJSON) bool { return onPage(m, pageID) },
		row: func(m commentJSON) output.Row {
			return output.Row{
				"id": m.ID, "page_id": m.CommentableID, "parent_id": m.parent(), "local_id": m.LocalID,
				"content_ref": clip(m.ContentRef, maxResultString),
				"created_by":  reduceUser(m.CreatedBy), "updated_by": reduceUser(m.UpdatedBy),
				"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
			}
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(commentsList), Rows: rows}, nil
}

// onPage reports whether a row is a page comment and, when pageID is given, of exactly that page.
func onPage(m commentJSON, pageID int64) bool {
	return m.CommentableType == commentType && (pageID == 0 || m.CommentableID == pageID)
}

// bindComment proves that a comment read lies on a page of a bound book. An unbound connection reads no
// further, but still accepts page comments only.
func (c *Client) bindComment(ctx context.Context, m commentJSON) error {
	if m.CommentableType != commentType {
		return invalidRequest(notPageComment)
	}
	if !c.scope.bound() {
		return nil
	}
	if err := c.requirePageBound(ctx, strconv.FormatInt(m.CommentableID, 10)); err != nil {
		if isInvalid(err) {
			return invalidRequest(outsideComment)
		}
		return err
	}
	return nil
}

// requireCommentBound reads a comment once as evidence of its page before a change; an unbound connection
// reads nothing.
func (c *Client) requireCommentBound(ctx context.Context, id int64) error {
	if !c.scope.bound() {
		return nil
	}
	var m commentJSON
	if err := c.get(ctx, "get comment", "/api/comments/"+strconv.FormatInt(id, 10), nil, &m, nil, provider.ClassPermission); err != nil {
		return err
	}
	return c.bindComment(ctx, m)
}

// GetComment returns one comment with its direct replies. Replies that do not belong to the same page and
// parent are dropped.
func (c *Client) GetComment(ctx context.Context, id int64) (output.Object, error) {
	var m commentJSON
	if err := c.get(ctx, "get comment", "/api/comments/"+strconv.FormatInt(id, 10), nil, &m, argNames(commentsGet), provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	if err := c.bindComment(ctx, m); err != nil {
		return output.Object{}, err
	}
	truncated := false
	html, cut := clipBytes(m.HTML, maxCommentHTMLBytes)
	truncated = truncated || cut
	replies := make([]map[string]any, 0, len(m.Replies))
	for _, r := range m.Replies {
		if !onPage(r, m.CommentableID) || r.ParentID == nil || *r.ParentID != m.LocalID {
			continue
		}
		if len(replies) >= maxCommentReplies {
			truncated = true
			break
		}
		text, cut := clipBytes(r.HTML, maxCommentHTMLBytes)
		truncated = truncated || cut
		replies = append(replies, commentFields(r, text))
	}
	fields := commentFields(m, html)
	fields["replies"] = replies
	fields["truncated"] = truncated
	return mapObject(commentsGet, fields), nil
}

func (c *Client) CreateComment(ctx context.Context, input commentMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	if err := c.requirePageBound(ctx, strconv.FormatInt(input.PageID, 10)); err != nil {
		return output.Object{}, err
	}
	var m commentJSON
	if err := c.mutate(ctx, "create comment", http.MethodPost, "/api/comments", input, &m, argNames(commentsCreate)); err != nil {
		return output.Object{}, err
	}
	return commentWriteObject(m), nil
}

// UpdateComment proves the comment's page first on a bound connection; a foreign comment ends the call
// without a change.
func (c *Client) UpdateComment(ctx context.Context, id int64, change commentMutation) (output.Object, error) {
	if err := change.validate(); err != nil {
		return output.Object{}, err
	}
	if err := c.requireCommentBound(ctx, id); err != nil {
		return output.Object{}, err
	}
	var m commentJSON
	if err := c.mutate(ctx, "update comment", http.MethodPut, "/api/comments/"+strconv.FormatInt(id, 10), change, &m, argNames(commentsUpdate)); err != nil {
		return output.Object{}, err
	}
	return commentWriteObject(m), nil
}

// DeleteComment proves the comment's page first on a bound connection; a foreign comment ends the call
// without a delete.
func (c *Client) DeleteComment(ctx context.Context, id int64) error {
	if err := c.requireCommentBound(ctx, id); err != nil {
		return err
	}
	return c.mutate(ctx, "delete comment", http.MethodDelete, "/api/comments/"+strconv.FormatInt(id, 10), nil, nil, argNames(commentsDelete))
}

// commentFields is the shared shape of a comment; html is added by the caller when it is shown.
func commentFields(m commentJSON, html string) map[string]any {
	return map[string]any{
		"id": m.ID, "page_id": m.CommentableID, "parent_id": m.parent(), "local_id": m.LocalID,
		"content_ref": clip(m.ContentRef, maxResultString), "archived": m.Archived, "html": html,
		"created_by": reduceUser(m.CreatedBy), "updated_by": reduceUser(m.UpdatedBy),
		"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
	}
}

func commentWriteObject(m commentJSON) output.Object {
	fields := commentFields(m, "")
	delete(fields, "html")
	return mapObject(commentsCreate, fields)
}

// mapObject orders the fields of a comment by the descriptor's field list.
func mapObject(d capability.Descriptor, fields map[string]any) output.Object {
	out := output.Object{}
	for _, f := range d.Fields {
		if v, ok := fields[f.Name]; ok {
			out.Fields = append(out.Fields, output.Field{Name: f.Name, Value: v})
		}
	}
	return out
}

// clipBytes cuts s to at most n bytes at a character boundary and reports whether it cut.
func clipBytes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

func isInvalid(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}
