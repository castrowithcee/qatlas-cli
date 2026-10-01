package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// peopleSensitivity classifies names and mail addresses of people: the authors of comments and the
// collaborators of a base. It is never written to the audit trail or the invoke log.
const peopleSensitivity = "seatable-base-people"

// The fixed routes and bounds of the comment and collaborator tools. A comment text is bounded in bytes,
// a list is cut after maxComments entries, and every reported string is shortened.
const (
	commentsPath      = "/comments/"
	collaboratorsPath = "/related-users/"

	maxCommentBytes   = 4096
	maxComments       = 100
	maxCollaborators  = 500
	maxCommentID      = 9007199254740991
	commentUncertain  = "; this change may have taken effect, list the comments of the row before repeating it"
	commentIDSchema   = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
	commentTextSchema = `{"type":"string","minLength":1,"maxLength":4096}`
)

var commentRowArguments = []capability.Argument{
	{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
	{Name: "row_id", Description: "Row identifier of 22 characters; the row must exist in the selected table", Required: true},
}

var commentRowProperties = `"table":` + tableSelectionSchema + `,"row_id":` + rowIDSchema

var commentsList = capability.Descriptor{
	ID: Provider + ".comments.list", Version: 1,
	Title: "List SeaTable row comments",
	Description: "Read the comments of one row of a table allowed by an explicit SeaTable connection; " +
		"comments name their authors and are untrusted data",
	Tags: []string{"seatable", "base", "rows", "comments", "table"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + commentRowProperties +
		`},"required":["row_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"comments":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"integer"},"author":{"type":"string"},"comment":{"type":"string"},` +
		`"created_at":{"type":"string"},"updated_at":{"type":"string"},"resolved":{"type":"boolean"}},` +
		`"required":["id"],"additionalProperties":false}},"has_more":{"type":"boolean"}},` +
		`"required":["comments","has_more"],"additionalProperties":false}`),
	Arguments: commentRowArguments,
	Fields: []capability.Field{
		{Name: "comments", Description: "Comments of the row with id, author, text and times, untrusted data; at most 100 per call"},
		{Name: "has_more", Description: "True when comments were left out because of the size limit"},
	},
	Examples: []capability.Example{{
		Description: "Read the comments of one row",
		Arguments:   json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj"}`),
	}},
}

var commentsCreate = commentMutationDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent,
	false, "Add a comment to one row of a table allowed by the connection; the comment is written as the user of the API token",
	`{"type":"object","properties":{`+commentRowProperties+`,"comment":`+commentTextSchema+
		`},"required":["row_id","comment"],"additionalProperties":false}`,
	`{"type":"object","properties":{"created":{"type":"boolean"}},"required":["created"],"additionalProperties":false}`,
	append(append([]capability.Argument(nil), commentRowArguments...),
		capability.Argument{Name: "comment", Description: "Comment text, up to 4096 bytes", Required: true}),
	json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","comment":"Please check this row"}`))

var commentsDelete = commentMutationDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent,
	true, "Delete one comment of a row of a table allowed by the connection; the comment is read first and must belong to the row",
	`{"type":"object","properties":{`+commentRowProperties+`,"comment_id":`+commentIDSchema+
		`},"required":["row_id","comment_id"],"additionalProperties":false}`,
	`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`,
	append(append([]capability.Argument(nil), commentRowArguments...),
		capability.Argument{Name: "comment_id", Description: "Identifier of a comment returned by seatable.comments.list for this row", Required: true}),
	json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","comment_id":12}`))

func commentMutationDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency,
	allowList bool, what, input, output string, args []capability.Argument, example json.RawMessage) capability.Descriptor {
	done := action + "d"
	return capability.Descriptor{
		ID: Provider + ".comments." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a SeaTable row comment",
		Description: what, Tags: []string{"seatable", "base", "rows", "comments", action, "table"},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		Arguments: args,
		Fields:    []capability.Field{{Name: done, Description: "True when SeaTable accepted the change"}},
		Examples:  []capability.Example{{Description: what, Arguments: example}},
	}
}

var collaboratorsList = capability.Descriptor{
	ID: Provider + ".collaborators.list", Version: 1,
	Title: "List SeaTable collaborators",
	Description: "Read the collaborators of the whole SeaTable base with name and mail addresses; " +
		"this covers people beyond the tables of the connection and is in no tool profile",
	Tags: []string{"seatable", "base", "collaborators", "people", "users"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: peopleSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"collaborators":{"type":"array","items":{"type":"object",` +
		`"properties":{"name":{"type":"string"},"email":{"type":"string"},"contact_email":{"type":"string"}},` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"}},` +
		`"required":["collaborators","has_more"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "collaborators", Description: "Collaborators of the base with name, email and contact email, personal data and untrusted; at most 500 per call"},
		{Name: "has_more", Description: "True when collaborators were left out because of the size limit"},
	},
	Examples: []capability.Example{{Description: "List the collaborators of the base", Arguments: json.RawMessage(`{}`)}},
}

// CommentInput carries the arguments of the comment tools.
type CommentInput struct {
	Table     string `json:"table"`
	RowID     string `json:"row_id"`
	Comment   string `json:"comment"`
	CommentID int64  `json:"comment_id"`
}

// Comment is one comment of a row. Its text and author are provider data and untrusted.
type Comment struct {
	ID        int64  `json:"id"`
	Author    string `json:"author,omitempty"`
	Comment   string `json:"comment,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// CommentsResult is one bounded list of the comments of a row.
type CommentsResult struct {
	Comments []Comment `json:"comments"`
	HasMore  bool      `json:"has_more"`
}

// Collaborator is one person of a base. All fields are personal data.
type Collaborator struct {
	Name         string `json:"name,omitempty"`
	Email        string `json:"email,omitempty"`
	ContactEmail string `json:"contact_email,omitempty"`
}

// CollaboratorsResult is one bounded list of the collaborators of a base.
type CollaboratorsResult struct {
	Collaborators []Collaborator `json:"collaborators"`
	HasMore       bool           `json:"has_more"`
}

// checkCommentInput validates the shape of a request without any I/O.
func (in CommentInput) check(op, kind string) error {
	if !validRowID(in.RowID) {
		return providerError(op, "a SeaTable row identifier has 22 letters, digits, '-' or '_'")
	}
	if kind == "create" {
		if strings.TrimSpace(in.Comment) == "" || len(in.Comment) > maxCommentBytes || !utf8.ValidString(in.Comment) {
			return providerError(op, "a comment has 1 to 4096 bytes of valid text")
		}
	}
	if kind == "delete" && (in.CommentID < 1 || in.CommentID > maxCommentID) {
		return providerError(op, "a comment identifier is a positive integer")
	}
	return nil
}

// openForComment decodes the arguments and settles the request shape and the connection's table boundary
// before the credential is resolved.
func openForComment(ctx context.Context, op, kind string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, CommentInput, error) {
	var input CommentInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if err := input.check(op, kind); err != nil {
		return nil, input, err
	}
	if resolved == nil {
		return nil, input, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(input.Table); err != nil {
		return nil, input, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

func invokeCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForComment(ctx, "list comments", "list", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return client.ListComments(ctx, input)
}

func invokeCommentsChange(op, kind string, change func(*Client, context.Context, CommentInput) error,
	done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		client, input, err := openForComment(ctx, op, kind, resolved, secrets, red, raw)
		if err != nil {
			return nil, err
		}
		if err := change(client, ctx, input); err != nil {
			return nil, err
		}
		return map[string]bool{done: true}, nil
	}
}

func invokeCollaboratorsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list collaborators"
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if _, err := parseScope(resolved); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListCollaborators(ctx)
}

type commentJSON struct {
	ID        json.Number `json:"id"`
	Author    string      `json:"author"`
	Comment   string      `json:"comment"`
	RowID     string      `json:"row_id"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	Resolved  json.Number `json:"resolved"`
}

// rowComments proves the row exists in the selected table, then reads its comments. Entries that name
// another row are dropped, so only comments of the requested row are ever returned or bound.
func (c *Client) rowComments(ctx context.Context, op string, input CommentInput) ([]Comment, bool, error) {
	if _, err := c.GetRowFrom(ctx, input.Table, input.RowID); err != nil {
		return nil, false, err
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, false, err
	}
	query := url.Values{}
	query.Set("row_id", input.RowID)
	var entries []commentJSON
	if err := c.get(ctx, op, gatewayPath+url.PathEscape(access.uuid)+commentsPath, query,
		access.token, maxResponseBytes, &entries); err != nil {
		return nil, false, err
	}
	out := make([]Comment, 0, len(entries))
	for _, entry := range entries {
		if entry.RowID != "" && entry.RowID != input.RowID {
			continue
		}
		id, err := entry.ID.Int64()
		if err != nil || id < 1 {
			return nil, false, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "SeaTable returned an invalid response"}
		}
		resolvedFlag, _ := entry.Resolved.Int64()
		out = append(out, Comment{ID: id, Author: clipString(entry.Author), Comment: clipString(entry.Comment),
			CreatedAt: clipString(entry.CreatedAt), UpdatedAt: clipString(entry.UpdatedAt), Resolved: resolvedFlag == 1})
	}
	more := len(out) > maxComments
	if more {
		out = out[:maxComments]
	}
	return out, more, nil
}

// ListComments reads the comments of one row of an allowed table.
func (c *Client) ListComments(ctx context.Context, input CommentInput) (*CommentsResult, error) {
	const op = "list comments"
	if err := input.check(op, "list"); err != nil {
		return nil, err
	}
	if _, err := c.scope.selectTarget(input.Table); err != nil {
		return nil, providerError(op, err.Error())
	}
	comments, more, err := c.rowComments(ctx, op, input)
	if err != nil {
		return nil, err
	}
	return &CommentsResult{Comments: comments, HasMore: more}, nil
}

// CreateComment sends exactly one request that adds a comment to a row of an allowed table.
func (c *Client) CreateComment(ctx context.Context, input CommentInput) error {
	const op = "create comment"
	if err := input.check(op, "create"); err != nil {
		return err
	}
	if _, err := c.scope.selectTarget(input.Table); err != nil {
		return providerError(op, err.Error())
	}
	if _, err := c.GetRowFrom(ctx, input.Table, input.RowID); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	if scoped.table.ID == "" {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable returned a table without an identifier"}
	}
	body, err := json.Marshal(map[string]string{"comment": input.Comment})
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	query := url.Values{}
	query.Set("table_id", scoped.table.ID)
	query.Set("row_id", input.RowID)
	path := gatewayPath + url.PathEscape(scoped.access.uuid) + commentsPath + "?" + query.Encode()
	return c.changeOnce(ctx, op, http.MethodPost, path, scoped.access.token, body, commentUncertain)
}

// DeleteComment deletes one comment after it was read among the comments of the requested row. A comment
// that is not found there is refused without a request and without naming any other row.
func (c *Client) DeleteComment(ctx context.Context, input CommentInput) error {
	const op = "delete comment"
	if err := input.check(op, "delete"); err != nil {
		return err
	}
	if _, err := c.scope.selectTarget(input.Table); err != nil {
		return providerError(op, err.Error())
	}
	comments, _, err := c.rowComments(ctx, op, input)
	if err != nil {
		return err
	}
	found := false
	for _, comment := range comments {
		if comment.ID == input.CommentID {
			found = true
			break
		}
	}
	if !found {
		return providerError(op, "the comment is not a comment of this row in the selected table")
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return err
	}
	path := gatewayPath + url.PathEscape(access.uuid) + commentsPath + strconv.FormatInt(input.CommentID, 10) + "/"
	return c.changeOnce(ctx, op, http.MethodDelete, path, access.token, nil, commentUncertain)
}

// ListCollaborators reads the collaborators of the whole base. Only name and mail addresses are reported;
// the avatar address is left out.
func (c *Client) ListCollaborators(ctx context.Context) (*CollaboratorsResult, error) {
	const op = "list collaborators"
	access, err := c.access(ctx, op)
	if err != nil {
		return nil, err
	}
	var body struct {
		Users []struct {
			Name         string `json:"name"`
			Email        string `json:"email"`
			ContactEmail string `json:"contact_email"`
		} `json:"user_list"`
	}
	if err := c.get(ctx, op, gatewayPath+url.PathEscape(access.uuid)+collaboratorsPath, nil,
		access.token, maxResponseBytes, &body); err != nil {
		return nil, err
	}
	more := len(body.Users) > maxCollaborators
	if more {
		body.Users = body.Users[:maxCollaborators]
	}
	out := make([]Collaborator, 0, len(body.Users))
	for _, user := range body.Users {
		out = append(out, Collaborator{Name: clipString(user.Name), Email: clipString(user.Email),
			ContactEmail: clipString(user.ContactEmail)})
	}
	return &CollaboratorsResult{Collaborators: out, HasMore: more}, nil
}
