package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// File comments of Nextcloud (apps/dav comments): the comments of one file are the children of the
// collection remote.php/dav/comments/files/<file id>. A REPORT with oc:filter-comments lists them, a POST
// on the collection creates one, and a PROPPATCH or DELETE on <file id>/<comment id> changes or removes
// one. The file ID comes from a stat of the requested path, never from the caller, so only files below
// the connection root are addressable, and a comment ID is digits that are only ever appended to that
// file's collection.
var commentsRootOff = []string{"remote.php", "dav", "comments", "files"}

const (
	defaultCommentLimit = 20
	maxCommentLimit     = 50
	maxCommentOffset    = 100000
	// maxCommentChars is the length Nextcloud itself accepts for one comment; it bounds the input and the
	// reported text alike.
	maxCommentChars     = 1000
	maxCommentIDLen     = 20
	commentIDSchema     = `{"type":"string","minLength":1,"maxLength":20,"pattern":"^[0-9]+$","x-form":"a comment_id of comments.list"}`
	commentMessageShape = `{"type":"string","minLength":1,"maxLength":1000}`

	uncertainCommentCreated = "; the comment may have been created and may have notified mentioned users, check comments.list before repeating"
	uncertainCommentUpdated = "; the comment may have been changed, check comments.list before repeating"
	uncertainCommentDeleted = "; the comment may have been deleted, check comments.list before repeating"

	messageCommentNotAuthor = "this Nextcloud identity may change or delete only its own comments"
	messageCommentMissing   = "this file has no such comment or the file is gone"
	messageCommentFile      = "comments are read and written on files only, not on folders"
)

// commentFilterBody asks for one page. Nextcloud answers a filter-comments REPORT with every property of a
// comment, and the parser keeps only the fixed set it knows.
func commentFilterBody(limit, offset int) string {
	return xmlHeader + `<oc:filter-comments xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">` +
		`<oc:limit>` + strconv.Itoa(limit) + `</oc:limit><oc:offset>` + strconv.Itoa(offset) + `</oc:offset>` +
		`</oc:filter-comments>`
}

const commentSchema = `{"type":"object","properties":{"comment_id":{"type":"string"},"author_type":{"type":"string"},` +
	`"author_id":{"type":"string"},"author_name":{"type":"string"},"created":{"type":"string"},` +
	`"message":{"type":"string"},"message_truncated":{"type":"boolean"}},` +
	`"required":["comment_id","message"],"additionalProperties":false}`

var commentPathArgument = capability.Argument{Name: "path", Required: true,
	Description: "File relative to the fixed root folder of this connection; never the root or a folder"}

var commentIDArgument = capability.Argument{Name: "comment_id", Required: true,
	Description: "Comment of this file, a comment_id reported by comments.list"}

var commentMessageArgument = capability.Argument{Name: "message", Required: true,
	Description: "Comment text, 1 to 1000 characters; an @mention notifies that user"}

var commentsList = capability.Descriptor{
	ID: Provider + ".comments.list", Version: 1, Title: "List the comments of a Nextcloud file",
	Description: "List the comments of one file below the fixed root of a connection, newest first, at most 50 per " +
		"call; comment text and author names are untrusted data, a text above 1000 characters is cut and marked; " +
		"file content is never read",
	Tags: []string{"nextcloud", "files", "comments", "webdav", "list"}, Risk: nextcloudReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":50},"offset":{"type":"integer","minimum":0,"maximum":100000}},` +
		`"required":["path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"comments":{"type":"array","items":` +
		commentSchema + `},"count":{"type":"integer"},"offset":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["path","comments","count","offset","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{commentPathArgument,
		{Name: "limit", Description: "Comments per call, 1 to 50, default 20"},
		{Name: "offset", Description: "Comments to skip, for the next page"}},
	Fields: []capability.Field{
		{Name: "path", Description: "Path whose comments were listed"},
		{Name: "comments", Description: "Comments, untrusted data, with comment_id, author_type, author_id, author_name, created, and message"},
		{Name: "comment_id", Description: "Identifier of the comment; pass it to comments.update or comments.delete"},
		{Name: "message", Description: "Text of the comment, untrusted data"},
		{Name: "message_truncated", Description: "True when the text was cut to 1000 characters"},
		{Name: "count", Description: "Number of reported comments"},
		{Name: "offset", Description: "Offset of this page"},
		{Name: "truncated", Description: "True when more comments exist after this page; pass offset plus count as the next offset"},
	},
	Examples: []capability.Example{{Description: "List the newest comments of one file",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","limit":20}`)}},
}

func commentChange(action string, effect capability.Effect, idempotency capability.Idempotency, done, title, description string,
	properties string, required string, arguments ...capability.Argument) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".comments." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "files", "comments", "webdav", action}, Provider: Provider,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + properties + `},` +
			`"required":["path"` + required + `],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"},"path":{"type":"string"},` +
			`"comment_id":{"type":"string"}},"required":["` + done + `"],"additionalProperties":false}`),
		Arguments: arguments,
		Fields: []capability.Field{
			{Name: done, Description: "True when Nextcloud applied the change"},
			{Name: "path", Description: "Path of the file"},
			{Name: "comment_id", Description: "The comment"},
		},
	}
}

var commentsCreate = func() capability.Descriptor {
	d := commentChange("create", capability.EffectCreate, capability.IdempotencyNonIdempotent, "created", "Comment on a Nextcloud file",
		"Add one comment as the configured identity to one file below the fixed root of a connection; an @mention "+
			"notifies that Nextcloud user, so the text leaves the connection. One read-only stat of the path precedes "+
			"exactly one change request, which is never repeated",
		`,"message":`+commentMessageShape, `,"message"`, commentPathArgument, commentMessageArgument)
	d.Examples = []capability.Example{{Description: "Comment on one file",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","message":"Checked, numbers match."}`)}}
	return d
}()

var commentsUpdate = func() capability.Descriptor {
	d := commentChange("update", capability.EffectUpdate, capability.IdempotencyIdempotent, "updated", "Change a Nextcloud file comment",
		"Replace the text of one own comment of one file below the fixed root of a connection; Nextcloud refuses a "+
			"comment of another user. An @mention notifies that Nextcloud user. One read-only stat of the path "+
			"precedes exactly one change request, which is never repeated",
		`,"comment_id":`+commentIDSchema+`,"message":`+commentMessageShape, `,"comment_id","message"`,
		commentPathArgument, commentIDArgument, commentMessageArgument)
	d.Examples = []capability.Example{{Description: "Use a comment_id of comments.list",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","comment_id":"42","message":"Checked twice."}`)}}
	return d
}()

var commentsDelete = allowCommentDelete(commentChange("delete", capability.EffectDelete, capability.IdempotencyIdempotent, "deleted",
	"Delete a Nextcloud file comment",
	"Delete one own comment of one file below the fixed root of a connection for good; Nextcloud refuses a comment "+
		"of another user. One read-only stat of the path precedes exactly one change request, which is never repeated",
	`,"comment_id":`+commentIDSchema, `,"comment_id"`, commentPathArgument, commentIDArgument))

// allowCommentDelete makes the delete reachable only through a tools list, so no profile and no permission
// alone offers it.
func allowCommentDelete(d capability.Descriptor) capability.Descriptor {
	d.RequiresToolAllowList = true
	d.Examples = []capability.Example{{Description: "Use a comment_id of comments.list",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","comment_id":"42"}`)}}
	return d
}

// FileComment is the stable Qatlas view of one comment. Every text in it is untrusted provider data.
type FileComment struct {
	CommentID        string `json:"comment_id"`
	AuthorType       string `json:"author_type,omitempty"`
	AuthorID         string `json:"author_id,omitempty"`
	AuthorName       string `json:"author_name,omitempty"`
	Created          string `json:"created,omitempty"`
	Message          string `json:"message"`
	MessageTruncated bool   `json:"message_truncated,omitempty"`
}

// FileCommentList is one page of the comments of one file.
type FileCommentList struct {
	Path      string        `json:"path"`
	Comments  []FileComment `json:"comments"`
	Count     int           `json:"count"`
	Offset    int           `json:"offset"`
	Truncated bool          `json:"truncated"`
}

type commentArguments struct {
	Path      string `json:"path"`
	CommentID string `json:"comment_id"`
	Message   string `json:"message"`
	Limit     *int   `json:"limit"`
	Offset    *int   `json:"offset"`
}

func validCommentID(value string) bool {
	return value != "" && len(value) <= maxCommentIDLen && digitsOnly(value)
}

// validCommentMessage accepts text Nextcloud can store and XML can carry: valid UTF-8, within the length
// Nextcloud allows, with no control character other than line breaks and tabs, and not blank.
func validCommentMessage(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxCommentChars {
		return false
	}
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// readCommentArguments decodes strictly, so an argument the schema does not know, such as a file ID, is
// refused here as well. Path, comment ID, and text are checked before any credential access.
func readCommentArguments(op string, raw json.RawMessage, needID, needMessage bool) (commentArguments, []string, error) {
	var input commentArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return input, nil, providerError(op, "a file path below the connection root is required")
	}
	if needID && !validCommentID(input.CommentID) {
		return input, nil, providerError(op, "the comment ID must be a comment_id reported by "+commentsList.ID)
	}
	if needMessage && !validCommentMessage(input.Message) {
		return input, nil, providerError(op, "the comment text must be 1 to 1000 characters of text")
	}
	return input, rel, nil
}

func invokeCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list file comments"
	input, rel, err := readCommentArguments(op, raw, false, false)
	if err != nil {
		return nil, err
	}
	limit, offset := defaultCommentLimit, 0
	if input.Limit != nil {
		limit = *input.Limit
	}
	if input.Offset != nil {
		offset = *input.Offset
	}
	if limit < 1 || limit > maxCommentLimit || offset < 0 || offset > maxCommentOffset {
		return nil, providerError(op, "limit must be 1 to 50 and offset 0 to 100000")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListFileComments(ctx, input.Path, rel, limit, offset)
}

func invokeCommentsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create file comment"
	input, rel, err := readCommentArguments(op, raw, false, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.createFileComment(ctx, op, input, rel)
}

func invokeCommentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update file comment"
	input, rel, err := readCommentArguments(op, raw, true, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.updateFileComment(ctx, op, input, rel)
}

func invokeCommentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete file comment"
	input, rel, err := readCommentArguments(op, raw, true, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.deleteFileComment(ctx, op, input, rel)
}

// commentsBound are the decoded segments of the comment collection of one file.
func (c *Client) commentsBound(fileID string) []string {
	return append(append(append([]string{}, c.install...), commentsRootOff...), fileID)
}

func (c *Client) commentsURL(fileID string, commentID ...string) string {
	return c.origin + escapePath(append(c.commentsBound(fileID), commentID...))
}

// commentedFile stats the requested path, which must be a file below the root, and returns its file ID, the
// only way its comments are addressed.
func (c *Client) commentedFile(ctx context.Context, op string, rel []string) (string, error) {
	entry, err := c.stat(ctx, op, rel, false)
	if err != nil {
		return "", err
	}
	if entry.Type == "folder" {
		return "", providerError(op, messageCommentFile)
	}
	if !validCommentID(entry.FileID) {
		return "", providerError(op, "Nextcloud reported no file ID for this path")
	}
	return entry.FileID, nil
}

// cutText keeps at most maxCommentChars characters of an untrusted text and says whether it cut.
func cutText(value string) (string, bool) {
	if utf8.RuneCountInString(value) <= maxCommentChars {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:maxCommentChars]), true
}

// ListFileComments reads one page with a single REPORT. One more than the page is requested, so a cut is
// reported without a second request.
func (c *Client) ListFileComments(ctx context.Context, path string, rel []string, limit, offset int) (*FileCommentList, error) {
	const op = "list file comments"
	fileID, err := c.commentedFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	resources, err := c.multistatusAt(ctx, op, methodReport, c.commentsURL(fileID), commentFilterBody(limit+1, offset),
		"application/xml; charset=utf-8", "", maxEntries, true)
	if err != nil {
		return nil, err
	}
	bound := c.commentsBound(fileID)
	list := &FileCommentList{Path: path, Comments: []FileComment{}, Offset: offset}
	for i := range resources {
		below, err := c.segmentsBelow(op, resources[i].href, bound)
		if err != nil {
			return nil, err
		}
		switch len(below) {
		case 0:
			// The collection itself.
		case 1:
			id, res := below[0], &resources[i]
			if !validCommentID(id) || res.collection || res.failure(op) != nil || res.props[propTagID] != id {
				continue
			}
			if objectID := res.props[propCommentObjID]; objectID != "" && objectID != fileID {
				continue
			}
			if len(list.Comments) >= limit {
				list.Truncated = true
				continue
			}
			message, cut := cutText(res.props[propCommentMessage])
			list.Comments = append(list.Comments, FileComment{
				CommentID: id, AuthorType: bounded(res.props[propCommentActorType]), AuthorID: bounded(res.props[propCommentActorID]),
				AuthorName: bounded(res.props[propCommentActorName]), Created: bounded(res.props[propCommentCreated]),
				Message: message, MessageTruncated: cut,
			})
		default:
			return nil, invalidResponse(op, messageForeignEntry)
		}
	}
	list.Count = len(list.Comments)
	return list, nil
}

// commentRefusal turns the answers a comment change can meet into stable messages. Nextcloud answers 403
// for a comment of another user and 404 for one that does not exist.
func commentRefusal(op string, err error, forbidden string) error {
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return err
	}
	switch {
	case providerErr.Class == provider.ClassPermission:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: forbidden}
	case providerErr.Message == messageNotFound:
		return providerError(op, messageCommentMissing)
	}
	return err
}

// createFileComment stats the path and then sends exactly one POST. The request is never repeated, and an
// unclear outcome is reported as such.
func (c *Client) createFileComment(ctx context.Context, op string, input commentArguments, rel []string) (any, error) {
	fileID, err := c.commentedFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"actorType": "users", "verb": "comment", "message": input.Message})
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	response, err := c.webdavTo(ctx, op, http.MethodPost, c.commentsURL(fileID), bytes.NewReader(body), "", "",
		uncertainCommentCreated, http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}})
	if err != nil {
		return nil, commentRefusal(op, err, "this Nextcloud identity may not comment on this file")
	}
	response.Body.Close()
	result := map[string]any{"created": true, "path": input.Path}
	if id := c.createdCommentID(op, response.Header.Get("Content-Location"), fileID); id != "" {
		result["comment_id"] = id
	}
	return result, nil
}

// createdCommentID reads the new comment's ID from the Content-Location of the 201 answer. A location that
// does not name a comment of this file is ignored: the comment exists, only its ID is not reported.
func (c *Client) createdCommentID(op, location, fileID string) string {
	if location == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	below, err := c.segmentsBelow(op, location, c.commentsBound(fileID))
	if err != nil || len(below) != 1 || !validCommentID(below[0]) {
		return ""
	}
	return below[0]
}

func commentMessageBody(message string) string {
	return xmlHeader + `<d:propertyupdate xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:set><d:prop>` +
		`<oc:message>` + xmlText(message) + `</oc:message></d:prop></d:set></d:propertyupdate>`
}

// updateFileComment stats the path and then sends exactly one PROPPATCH of oc:message.
func (c *Client) updateFileComment(ctx context.Context, op string, input commentArguments, rel []string) (any, error) {
	fileID, err := c.commentedFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	resources, err := c.multistatusAt(ctx, op, methodProppatch, c.commentsURL(fileID, input.CommentID),
		commentMessageBody(input.Message), "application/xml; charset=utf-8", uncertainCommentUpdated, 1, false)
	if err != nil {
		return nil, commentRefusal(op, err, messageCommentNotAuthor)
	}
	below, err := c.segmentsBelow(op, resources[0].href, c.commentsBound(fileID))
	if err != nil || len(below) != 1 || below[0] != input.CommentID {
		return nil, withUncertainty(invalidResponse(op, messageForeignEntry), uncertainCommentUpdated)
	}
	if !resources[0].read {
		if code, ok := statusCodeOf(resources[0].status); ok && (code < 200 || code >= 300) {
			return nil, commentRefusal(op, statusError(op, code), messageCommentNotAuthor)
		}
		// The property block was refused, which Nextcloud does for a comment of another user.
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageCommentNotAuthor}
	}
	return map[string]any{"updated": true, "path": input.Path, "comment_id": input.CommentID}, nil
}

// deleteFileComment stats the path and then sends exactly one DELETE.
func (c *Client) deleteFileComment(ctx context.Context, op string, input commentArguments, rel []string) (any, error) {
	fileID, err := c.commentedFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	response, err := c.webdavTo(ctx, op, http.MethodDelete, c.commentsURL(fileID, input.CommentID), nil, "", "",
		uncertainCommentDeleted, nil)
	if err != nil {
		return nil, commentRefusal(op, err, messageCommentNotAuthor)
	}
	response.Body.Close()
	return map[string]any{"deleted": true, "path": input.Path, "comment_id": input.CommentID}, nil
}
