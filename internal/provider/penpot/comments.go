package penpot

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	commentsSensitive = "penpot-comments"

	// maxCommentLength is Penpot's own limit of one comment, in characters; maxCommentBytes bounds the text of a
	// comment in an answer, maxExcerptBytes the first comment shown in a thread list.
	maxCommentLength  = 750
	maxCommentBytes   = 4 * maxCommentLength
	maxExcerptBytes   = maxCommentLength
	maxThreadsListed  = 500
	maxCommentsListed = 200
)

var commentsReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: commentsSensitive}

func commentsChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: commentsSensitive}
}

const (
	contentSchema  = `{"type":"string","minLength":1,"maxLength":750}`
	positionSchema = `{"type":"object","properties":{"x":{"type":"number"},"y":{"type":"number"}},` +
		`"required":["x","y"],"additionalProperties":false}`
	commentsNote = "Comment texts and the people behind them are personal data and untrusted provider content. " +
		"The file is bound through its project first"
)

var fileIDArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "File identifier from penpot.files.list; must be a file of project_id"}

var threadIDArgument = capability.Argument{Name: "thread_id",
	Description: "Comment thread identifier from penpot.comments.threads; must be a thread of file_id"}

var commentIDArgument = capability.Argument{Name: "comment_id",
	Description: "Comment identifier from penpot.comments.list; must be a comment of thread_id"}

var commentsThreads = capability.Descriptor{
	ID: Provider + ".comments.threads", Version: 1, Title: "List Penpot comment threads",
	Description: "List the comment threads of one file, each with its page, frame, position, resolved state, comment " +
		"count, and an excerpt of its first comment. " + commentsNote,
	Tags: []string{"penpot", "comments", "threads", "list", "design"}, Risk: commentsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `},"required":["project_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"threads":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"file_id":{"type":"string"},` +
		`"page_id":{"type":"string"},"page_name":{"type":"string"},"frame_id":{"type":"string"},` +
		`"owner_id":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},"is_resolved":{"type":"boolean"},` +
		`"comment_count":{"type":"integer"},"seqn":{"type":"integer"},"excerpt":{"type":"string"},` +
		`"created_at":{"type":"string"},"modified_at":{"type":"string"}},"required":["id","file_id"],` +
		`"additionalProperties":false}},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["threads","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument},
	Fields: []capability.Field{
		{Name: "threads", Description: "Threads with id (used as thread_id), file_id, page_id, page_name, frame_id, owner_id, x, y, is_resolved, comment_count, seqn, excerpt, created_at, and modified_at"},
		{Name: "count", Description: "Number of threads in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 500 threads"},
	},
	Examples: []capability.Example{{Description: "List the comment threads of a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

var commentsList = capability.Descriptor{
	ID: Provider + ".comments.list", Version: 1, Title: "List Penpot comments",
	Description: "List the comments of one comment thread of a file, oldest first. " + commentsNote,
	Tags:        []string{"penpot", "comments", "list", "design"}, Risk: commentsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `,"thread_id":` + uuidSchema + `},"required":["project_id","file_id","thread_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"comments":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"thread_id":{"type":"string"},` +
		`"owner_id":{"type":"string"},"content":{"type":"string"},"created_at":{"type":"string"},` +
		`"modified_at":{"type":"string"}},"required":["id","thread_id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["comments","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "thread_id", Required: true, Description: threadIDArgument.Description}},
	Fields: []capability.Field{
		{Name: "comments", Description: "Comments with id (used as comment_id), thread_id, owner_id, content, created_at, and modified_at"},
		{Name: "count", Description: "Number of comments in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 200 comments"},
	},
	Examples: []capability.Example{{Description: "List the comments of a thread",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004"}`)}},
}

const createdSchema = `{"type":"object","properties":{"thread_id":{"type":"string"},"comment_id":{"type":"string"},` +
	`"file_id":{"type":"string"}},"required":["thread_id","comment_id","file_id"],"additionalProperties":false}`

var commentsCreate = capability.Descriptor{
	ID: Provider + ".comments.create", Version: 1, Title: "Create a Penpot comment",
	Description: "Create either a new comment thread on a page of a file (give page_id, frame_id, and position, " +
		"without thread_id) or a new comment in an existing thread of the file (give thread_id, without page_id, " +
		"frame_id, and position). The text has at most 750 characters; no mentions are sent. Penpot may notify " +
		"members of the team by email. " + commentsNote,
	Tags: []string{"penpot", "comments", "create", "design"}, Provider: Provider,
	Risk: commentsChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `,"content":` + contentSchema + `,"thread_id":` + uuidSchema + `,"page_id":` + uuidSchema +
		`,"frame_id":` + uuidSchema + `,"position":` + positionSchema + `},` +
		`"required":["project_id","file_id","content"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(createdSchema),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "content", Required: true, Description: "Comment text, 1 to 750 characters"},
		{Name: "thread_id", Description: "Existing thread of file_id to add a comment to; omit to start a new thread"},
		{Name: "page_id", Description: "New thread: page of file_id"},
		{Name: "frame_id", Description: "New thread: frame of that page the thread is attached to"},
		{Name: "position", Description: "New thread: point {x, y} on the page"}},
	Fields: []capability.Field{
		{Name: "thread_id", Description: "Identifier of the new thread, or of the thread that received the comment"},
		{Name: "comment_id", Description: "Identifier of the new comment"},
		{Name: "file_id", Description: "Identifier of the file"},
	},
	Examples: []capability.Example{
		{Description: "Start a thread", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","page_id":"00000000-0000-0000-0000-000000000005","frame_id":"00000000-0000-0000-0000-000000000006","position":{"x":10,"y":20},"content":"Please check the spacing"}`)},
		{Description: "Answer in a thread", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004","content":"Done"}`)},
	},
}

const changedSchema = `{"type":"object","properties":{"updated":{"type":"boolean"},"thread_id":{"type":"string"},` +
	`"comment_id":{"type":"string"}},"required":["updated","thread_id"],"additionalProperties":false}`

var commentsUpdate = capability.Descriptor{
	ID: Provider + ".comments.update", Version: 1, Title: "Update a Penpot comment or thread",
	Description: "Either change the text of a comment (give thread_id, comment_id, and content; Penpot allows this " +
		"only for the token account's own comments) or mark a thread resolved or open (give thread_id and " +
		"is_resolved, without comment_id and content). " + commentsNote,
	Tags: []string{"penpot", "comments", "update", "design"}, Provider: Provider,
	Risk: commentsChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `,"thread_id":` + uuidSchema + `,"comment_id":` + uuidSchema + `,"content":` + contentSchema +
		`,"is_resolved":{"type":"boolean"}},"required":["project_id","file_id","thread_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(changedSchema),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "thread_id", Required: true, Description: threadIDArgument.Description},
		{Name: "comment_id", Description: "Comment of thread_id whose text changes; omit to change the thread's state"},
		{Name: "content", Description: "New comment text, 1 to 750 characters; only with comment_id"},
		{Name: "is_resolved", Description: "Resolved state of the thread; only without comment_id"}},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Penpot accepted the change"},
		{Name: "thread_id", Description: "Identifier of the thread"},
		{Name: "comment_id", Description: "Identifier of the changed comment, when a comment changed"},
	},
	Examples: []capability.Example{
		{Description: "Resolve a thread", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004","is_resolved":true}`)},
		{Description: "Edit a comment", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004","comment_id":"00000000-0000-0000-0000-000000000007","content":"Fixed"}`)},
	},
}

var commentsDelete = capability.Descriptor{
	ID: Provider + ".comments.delete", Version: 1, Title: "Delete a Penpot comment or thread",
	Description: "Delete one comment (give thread_id and comment_id) or a whole thread with all its comments (give " +
		"thread_id only). Penpot allows this only for the token account's own comments and threads. The deletion " +
		"is final. " + commentsNote,
	Tags: []string{"penpot", "comments", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: commentsChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `,"thread_id":` + uuidSchema + `,"comment_id":` + uuidSchema + `},` +
		`"required":["project_id","file_id","thread_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
		`"thread_id":{"type":"string"},"comment_id":{"type":"string"}},"required":["deleted","thread_id"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "thread_id", Required: true, Description: threadIDArgument.Description},
		{Name: "comment_id", Description: "Comment of thread_id to delete; omit to delete the whole thread"}},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Penpot accepted the deletion"},
		{Name: "thread_id", Description: "Identifier of the thread"},
		{Name: "comment_id", Description: "Identifier of the deleted comment, when a single comment was deleted"},
	},
	Examples: []capability.Example{
		{Description: "Delete a comment", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004","comment_id":"00000000-0000-0000-0000-000000000007"}`)},
		{Description: "Delete a thread", Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","thread_id":"00000000-0000-0000-0000-000000000004"}`)},
	},
}

// Thread is one comment thread of a file.
type Thread struct {
	ID           string   `json:"id"`
	FileID       string   `json:"file_id"`
	PageID       string   `json:"page_id,omitempty"`
	PageName     string   `json:"page_name,omitempty"`
	FrameID      string   `json:"frame_id,omitempty"`
	OwnerID      string   `json:"owner_id,omitempty"`
	X            *float64 `json:"x,omitempty"`
	Y            *float64 `json:"y,omitempty"`
	IsResolved   bool     `json:"is_resolved"`
	CommentCount int64    `json:"comment_count,omitempty"`
	Seqn         int64    `json:"seqn,omitempty"`
	Excerpt      string   `json:"excerpt,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	ModifiedAt   string   `json:"modified_at,omitempty"`
}

// ThreadsResult is the answer of comments.threads.
type ThreadsResult struct {
	Threads   []Thread `json:"threads"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated"`
}

// Comment is one comment of a thread.
type Comment struct {
	ID         string `json:"id"`
	ThreadID   string `json:"thread_id"`
	OwnerID    string `json:"owner_id,omitempty"`
	Content    string `json:"content,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// CommentsResult is the answer of comments.list.
type CommentsResult struct {
	Comments  []Comment `json:"comments"`
	Count     int       `json:"count"`
	Truncated bool      `json:"truncated"`
}

// CreatedResult is the answer of comments.create; it carries identifiers only.
type CreatedResult struct {
	ThreadID  string `json:"thread_id"`
	CommentID string `json:"comment_id"`
	FileID    string `json:"file_id"`
}

// UpdatedResult and DeletedResult are the answers of comments.update and comments.delete; they carry
// identifiers only.
type UpdatedResult struct {
	Updated   bool   `json:"updated"`
	ThreadID  string `json:"thread_id"`
	CommentID string `json:"comment_id,omitempty"`
}

type DeletedResult struct {
	Deleted   bool   `json:"deleted"`
	ThreadID  string `json:"thread_id"`
	CommentID string `json:"comment_id,omitempty"`
}

// commentArguments is the union of the arguments of the five tools.
type commentArguments struct {
	ProjectID  string  `json:"project_id"`
	FileID     string  `json:"file_id"`
	ThreadID   string  `json:"thread_id"`
	CommentID  string  `json:"comment_id"`
	PageID     string  `json:"page_id"`
	FrameID    string  `json:"frame_id"`
	Content    *string `json:"content"`
	IsResolved *bool   `json:"is_resolved"`
	Position   *struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	} `json:"position"`
}

// boundIDs are the identifiers of one request after every check that needs no provider I/O.
type boundIDs struct {
	project, file, thread, comment, page, frame string
}

// prepare decodes the arguments and settles every check that needs no I/O: the project allow-list (before any
// secret is resolved) and the form of every identifier. No message quotes an identifier.
func prepare(op string, resolved *config.Resolved, raw json.RawMessage) (commentArguments, boundIDs, error) {
	var input commentArguments
	var ids boundIDs
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, ids, providerError(op, "the validated arguments could not be read")
	}
	var err error
	if ids.project, err = selectProject(resolved, input.ProjectID); err != nil {
		return input, ids, err
	}
	for _, field := range []struct {
		name string
		raw  string
		out  *string
	}{{"file_id", input.FileID, &ids.file}, {"thread_id", input.ThreadID, &ids.thread},
		{"comment_id", input.CommentID, &ids.comment}, {"page_id", input.PageID, &ids.page},
		{"frame_id", input.FrameID, &ids.frame}} {
		if field.raw == "" {
			continue
		}
		id, ok := parseUUID(field.raw)
		if !ok {
			return input, ids, invalidRequest(field.name + " must be a UUID")
		}
		*field.out = id
	}
	if ids.file == "" {
		return input, ids, invalidRequest("file_id must be a UUID")
	}
	return input, ids, nil
}

// checkContent accepts one comment text: valid UTF-8, 1 to 750 characters, no control characters besides line
// breaks and tabs.
func checkContent(content *string) (string, error) {
	if content == nil || strings.TrimSpace(*content) == "" {
		return "", invalidRequest("content must not be empty")
	}
	text := *content
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxCommentLength {
		return "", invalidRequest("content must be valid text of at most 750 characters")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", invalidRequest("content must not contain control characters")
		}
	}
	return text, nil
}

// boundFile opens the client and binds the file through its project before anything else is read.
func boundFile(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, ids boundIDs) (*Client, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.fileOf(ctx, op, ids.project, ids.file); err != nil {
		return nil, err
	}
	return client, nil
}

// threadsOf reads the threads of one file. A thread that reports another file is dropped.
func (c *Client) threadsOf(ctx context.Context, op, fileID string) ([]Thread, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdThreads, map[string]any{"file-id": fileID}, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	threads := make([]Thread, 0, len(entries))
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if reported := entry.id("fileid"); reported != "" && reported != fileID {
			continue
		}
		thread := Thread{ID: id, FileID: fileID, PageID: entry.id("pageid"), PageName: entry.str("pagename"),
			FrameID: entry.id("frameid"), OwnerID: entry.id("ownerid"), IsResolved: entry.boolean("isresolved"),
			CommentCount: entry.integer("countcomments"), Seqn: entry.integer("seqn"),
			Excerpt: boundString(entry.text("content"), maxExcerptBytes), CreatedAt: entry.str("createdat"),
			ModifiedAt: entry.str("modifiedat")}
		if position, ok := asObj(entry["position"]); ok {
			x, y := position.number("x"), position.number("y")
			thread.X, thread.Y = &x, &y
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

// text reads a string value without the short string bound; the caller bounds it.
func (o obj) text(key string) string {
	var value string
	if err := json.Unmarshal(o[key], &value); err != nil {
		return ""
	}
	return value
}

// commentsOf reads the comments of one thread of the file. A comment that reports another thread or file is
// dropped.
func (c *Client) commentsOf(ctx context.Context, op, fileID, threadID string) ([]Comment, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdComments, map[string]any{"thread-id": threadID}, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	comments := make([]Comment, 0, len(entries))
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if reported := entry.id("threadid"); reported != "" && reported != threadID {
			continue
		}
		if reported := entry.id("fileid"); reported != "" && reported != fileID {
			continue
		}
		comments = append(comments, Comment{ID: id, ThreadID: threadID, OwnerID: entry.id("ownerid"),
			Content: boundString(entry.text("content"), maxCommentBytes), CreatedAt: entry.str("createdat"),
			ModifiedAt: entry.str("modifiedat")})
	}
	return comments, nil
}

// requireThread proves that a thread belongs to the bound file: it reads the file's threads.
func (c *Client) requireThread(ctx context.Context, op, fileID, threadID string) error {
	threads, err := c.threadsOf(ctx, op, fileID)
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if thread.ID == threadID {
			return nil
		}
	}
	return invalidRequest("thread_id is not a thread of this file")
}

// requireComment proves that a comment belongs to a thread of the bound file: it reads the thread's comments.
func (c *Client) requireComment(ctx context.Context, op, fileID, threadID, commentID string) error {
	comments, err := c.commentsOf(ctx, op, fileID, threadID)
	if err != nil {
		return err
	}
	for _, comment := range comments {
		if comment.ID == commentID {
			return nil
		}
	}
	return invalidRequest("comment_id is not a comment of this thread")
}

func invokeCommentsThreads(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list comment threads"
	_, ids, err := prepare(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := boundFile(ctx, op, resolved, secrets, red, ids)
	if err != nil {
		return nil, err
	}
	threads, err := client.threadsOf(ctx, op, ids.file)
	if err != nil {
		return nil, err
	}
	result := &ThreadsResult{Threads: []Thread{}}
	for _, thread := range threads {
		if len(result.Threads) >= maxThreadsListed {
			result.Truncated = true
			break
		}
		result.Threads = append(result.Threads, thread)
	}
	result.Count = len(result.Threads)
	return result, nil
}

func invokeCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list comments"
	_, ids, err := prepare(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if ids.thread == "" {
		return nil, invalidRequest("thread_id must be a UUID")
	}
	client, err := boundFile(ctx, op, resolved, secrets, red, ids)
	if err != nil {
		return nil, err
	}
	if err := client.requireThread(ctx, op, ids.file, ids.thread); err != nil {
		return nil, err
	}
	comments, err := client.commentsOf(ctx, op, ids.file, ids.thread)
	if err != nil {
		return nil, err
	}
	result := &CommentsResult{Comments: []Comment{}}
	for _, comment := range comments {
		if len(result.Comments) >= maxCommentsListed {
			result.Truncated = true
			break
		}
		result.Comments = append(result.Comments, comment)
	}
	result.Count = len(result.Comments)
	return result, nil
}

// checkPoint reads the position of a new thread: both coordinates, finite.
func checkPoint(input commentArguments) (float64, float64, error) {
	if input.Position == nil || input.Position.X == nil || input.Position.Y == nil ||
		math.IsNaN(*input.Position.X) || math.IsInf(*input.Position.X, 0) ||
		math.IsNaN(*input.Position.Y) || math.IsInf(*input.Position.Y, 0) {
		return 0, 0, invalidRequest("position must give finite x and y")
	}
	return *input.Position.X, *input.Position.Y, nil
}

// requireFrame proves that the page belongs to the file and the frame to the page: it reads the page.
func (c *Client) requireFrame(ctx context.Context, op, fileID, pageID, frameID string) error {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdPage, map[string]any{"file-id": fileID, "page-id": pageID}, &raw); err != nil {
		return err
	}
	page, ok := asObj(raw)
	if !ok {
		return invalidResponse(op, "Penpot returned an invalid response")
	}
	if reported := page.id("id"); reported != "" && reported != pageID {
		return invalidRequest("page_id is not a page of this file")
	}
	var shapes map[string]json.RawMessage
	if err := json.Unmarshal(page["objects"], &shapes); err != nil {
		return invalidRequest("frame_id is not a frame of this page")
	}
	for key := range shapes {
		if id, ok := parseUUID(key); ok && id == frameID {
			return nil
		}
	}
	return invalidRequest("frame_id is not a frame of this page")
}

func invokeCommentsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create comment"
	input, ids, err := prepare(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	content, err := checkContent(input.Content)
	if err != nil {
		return nil, err
	}
	newThread := ids.thread == ""
	var x, y float64
	if newThread {
		if ids.page == "" || ids.frame == "" {
			return nil, invalidRequest("a new thread needs page_id, frame_id, and position; or give thread_id")
		}
		if x, y, err = checkPoint(input); err != nil {
			return nil, err
		}
	} else if ids.page != "" || ids.frame != "" || input.Position != nil {
		return nil, invalidRequest("thread_id cannot be combined with page_id, frame_id, or position")
	}
	client, err := boundFile(ctx, op, resolved, secrets, red, ids)
	if err != nil {
		return nil, err
	}
	command := cmdCreateComment
	params := map[string]any{"thread-id": ids.thread, "content": content}
	if newThread {
		if err := client.requireFrame(ctx, op, ids.file, ids.page, ids.frame); err != nil {
			return nil, err
		}
		command = cmdCreateThread
		params = map[string]any{"file-id": ids.file, "page-id": ids.page, "frame-id": ids.frame,
			"position": map[string]float64{"x": x, "y": y}, "content": content}
	} else if err := client.requireThread(ctx, op, ids.file, ids.thread); err != nil {
		return nil, err
	}
	data, err := client.change(ctx, op, command, params)
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	result := &CreatedResult{ThreadID: ids.thread, FileID: ids.file}
	if newThread {
		result.ThreadID, result.CommentID = answer.id("id"), answer.id("commentid")
	} else {
		result.CommentID = answer.id("id")
	}
	if result.ThreadID == "" || result.CommentID == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return result, nil
}

func invokeCommentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update comment"
	input, ids, err := prepare(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if ids.thread == "" {
		return nil, invalidRequest("thread_id must be a UUID")
	}
	command := cmdUpdateThread
	var params map[string]any
	if ids.comment != "" {
		if input.IsResolved != nil {
			return nil, invalidRequest("is_resolved cannot be combined with comment_id")
		}
		content, err := checkContent(input.Content)
		if err != nil {
			return nil, err
		}
		command, params = cmdUpdateComment, map[string]any{"id": ids.comment, "content": content}
	} else {
		if input.Content != nil || input.IsResolved == nil {
			return nil, invalidRequest("give is_resolved without content, or comment_id with content")
		}
		params = map[string]any{"id": ids.thread, "is-resolved": *input.IsResolved}
	}
	client, err := boundFile(ctx, op, resolved, secrets, red, ids)
	if err != nil {
		return nil, err
	}
	if err := client.requireThread(ctx, op, ids.file, ids.thread); err != nil {
		return nil, err
	}
	if ids.comment != "" {
		if err := client.requireComment(ctx, op, ids.file, ids.thread, ids.comment); err != nil {
			return nil, err
		}
	}
	if _, err := client.change(ctx, op, command, params); err != nil {
		return nil, err
	}
	return &UpdatedResult{Updated: true, ThreadID: ids.thread, CommentID: ids.comment}, nil
}

func invokeCommentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete comment"
	input, ids, err := prepare(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if ids.thread == "" {
		return nil, invalidRequest("thread_id must be a UUID")
	}
	if input.Content != nil || input.IsResolved != nil || input.Position != nil {
		return nil, invalidRequest("a deletion takes thread_id and optionally comment_id only")
	}
	client, err := boundFile(ctx, op, resolved, secrets, red, ids)
	if err != nil {
		return nil, err
	}
	if err := client.requireThread(ctx, op, ids.file, ids.thread); err != nil {
		return nil, err
	}
	command, target := cmdDeleteThread, ids.thread
	if ids.comment != "" {
		if err := client.requireComment(ctx, op, ids.file, ids.thread, ids.comment); err != nil {
			return nil, err
		}
		command, target = cmdDeleteComment, ids.comment
	}
	if _, err := client.change(ctx, op, command, map[string]any{"id": target}); err != nil {
		return nil, err
	}
	return &DeletedResult{Deleted: true, ThreadID: ids.thread, CommentID: ids.comment}, nil
}
