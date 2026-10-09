package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// channelIDArgument names the channel a message tool addresses directly. It is checked against the
// connection's local allow-list before any request, and against the live instance by
// (*Client).verifyChannelScope before the matching endpoint is ever reached.
var channelIDArgument = capability.Argument{Name: "channel_id",
	Description: "Channel identifier; must be inside this connection's channel allow-list when it has one, " +
		"and is always re-checked live against this connection's bound teams before use", Required: true}

var messageTextSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxMessageLength) +
	`,"pattern":"^[^\\x00-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]+$"}`

var messageEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"channel_id":` + idSchema + `,"user_id":` + idSchema + `,"root_id":` + idSchema + `,` +
	`"message":{"type":"string"},"type":{"type":"string"},"created_at":{"type":"string"},"edited_at":{"type":"string"}},` +
	`"required":["id","channel_id","user_id","message","type","created_at"],"additionalProperties":false}`

var messageEntryFields = []capability.Field{
	{Name: "id", Description: "Message (post) identifier"},
	{Name: "channel_id", Description: "Channel this message belongs to"},
	{Name: "user_id", Description: "Author of the message"},
	{Name: "root_id", Description: "Identifier of the thread's root message; absent for a root message itself"},
	{Name: "message", Description: "Message text, untrusted data; never executed or rendered"},
	{Name: "type", Description: "Empty for a normal message, or kChat's own system message type"},
	{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC"},
	{Name: "edited_at", Description: "Last edit time, normalised to RFC 3339 in UTC, when the message was edited"},
}

var messagesList = capability.Descriptor{
	ID:      Provider + ".messages.list",
	Version: 1,
	Title:   "List Infomaniak kChat channel messages",
	Description: "List the messages of one channel this connection may reach, newest first, one page at a " +
		"time; never reads a whole channel at once",
	Tags:     []string{"infomaniak", "kchat", "messages", "list", "posts"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"messages":{"type":"array","items":` + messageEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["channel_id","messages","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		channelIDArgument,
		{Name: "page", Description: "1-based page of the channel's messages; the first (newest) page when omitted"},
		{Name: "limit", Description: "Messages per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, messageEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "has_more", Description: "True when an older page remains, so this page is not the whole channel"},
		capability.Field{Name: "count", Description: "Number of messages reported on this page"},
	),
	Examples: []capability.Example{{Description: "List the newest messages of one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000"}`)}},
}

var messagesThread = capability.Descriptor{
	ID:      Provider + ".messages.thread",
	Version: 1,
	Title:   "Read an Infomaniak kChat thread",
	Description: "Read one message and every reply in its thread, one page at a time, for a channel this " +
		"connection may reach",
	Tags:     []string{"infomaniak", "kchat", "messages", "thread"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"cursor":` + idSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"channel_id":` + idSchema + `,"messages":{"type":"array","items":` + messageEntrySchema + `},` +
		`"has_more":{"type":"boolean"},"cursor":{"type":"string"},"count":{"type":"integer"}},` +
		`"required":["post_id","channel_id","messages","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "post_id", Description: "Identifier of any message of the thread, typically its root", Required: true},
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Messages per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, messageEntryFields...),
		capability.Field{Name: "post_id", Description: "Message that was requested"},
		capability.Field{Name: "has_more", Description: "True when a further page of the thread remains"},
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "count", Description: "Number of messages reported on this page"},
	),
	Examples: []capability.Example{{Description: "Read the first page of one thread",
		Arguments: json.RawMessage(`{"post_id":"abc123root0000000000000000"}`)}},
}

var messagesSend = capability.Descriptor{
	ID:      Provider + ".messages.send",
	Version: 2,
	Title:   "Send an Infomaniak kChat message",
	Description: "Send exactly one confirmed message (kChat renders Markdown and mentions such as @channel) to a " +
		"channel this connection may reach, or, with root_id, one confirmed reply to an existing thread of that " +
		"channel. With file_ids it attaches own, not yet attached uploads of files.upload to the message; the text " +
		"may then be left out",
	Tags: []string{"infomaniak", "kchat", "messages", "send", "reply"},
	Risk: capability.Risk{
		Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"text":` + messageTextSchema + `,"root_id":` + idSchema + `,` +
		`"file_ids":{"type":"array","minItems":1,"maxItems":` + itoa(maxMessageFiles) + `,"uniqueItems":true,` +
		`"items":` + idSchema + `}},"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":` + idSchema + `,"channel_id":` + idSchema + `,"root_id":` + idSchema + `,"created_at":{"type":"string"}},` +
		`"required":["id","channel_id","created_at"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		channelIDArgument,
		{Name: "text", Description: "Message text, from 1 through " + itoa(maxMessageLength) +
			" characters; required unless file_ids is given"},
		{Name: "root_id", Description: "Identifier of the thread root to reply to; the root must belong to channel_id"},
		{Name: "file_ids", Description: "1 to " + itoa(maxMessageFiles) + " distinct file_id values of files.upload " +
			"to attach; each must be an unattached upload of this token's own user to channel_id"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the sent message"},
		{Name: "channel_id", Description: "Channel the message was sent to"},
		{Name: "root_id", Description: "Thread root the message replied to, when it was a reply"},
		{Name: "created_at", Description: "Send time, normalised to RFC 3339 in UTC"},
	},
	Examples: []capability.Example{{
		Description: "Send one message to a channel of a bound team",
		Arguments:   json.RawMessage(`{"channel_id":"abc123channel00000000000000","text":"Deployment finished"}`),
	}, {
		Description: "Send a message with an uploaded file",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000","text":"Report attached",` +
			`"file_ids":["abc123file00000000000000000"]}`),
	}},
}

var postIDArgument = capability.Argument{Name: "post_id", Required: true,
	Description: "Message (post) identifier; its channel must be inside this connection's channel allow-list " +
		"when it has one and inside a bound team, which is always re-checked with extra requests"}

func messageChangeRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var messagesGet = capability.Descriptor{
	ID:          Provider + ".messages.get",
	Version:     1,
	Title:       "Read one Infomaniak kChat message",
	Description: "Read exactly one message by its identifier, for a channel this connection may reach",
	Tags:        []string{"infomaniak", "kchat", "messages", "get", "post"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(messageEntrySchema),
	Arguments:    []capability.Argument{postIDArgument},
	Fields:       messageEntryFields,
	Examples: []capability.Example{{Description: "Read one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
}

var messagesUpdate = capability.Descriptor{
	ID:      Provider + ".messages.update",
	Version: 1,
	Title:   "Edit an Infomaniak kChat message",
	Description: "Replace the text of exactly one existing message of a channel this connection may reach with " +
		"one confirmed text; whether the token may edit a message of another author is kChat's decision. " +
		"kChat renders Markdown and mentions such as @channel",
	Tags:     []string{"infomaniak", "kchat", "messages", "update", "edit"},
	Risk:     messageChangeRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,"text":` +
		messageTextSchema + `},"required":["post_id","text"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":` + idSchema + `,"channel_id":` + idSchema + `,"edited_at":{"type":"string"}},` +
		`"required":["id","channel_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		postIDArgument,
		{Name: "text", Description: "New message text, from 1 through " + itoa(maxMessageLength) + " characters", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the edited message"},
		{Name: "channel_id", Description: "Channel of the edited message"},
		{Name: "edited_at", Description: "Edit time, normalised to RFC 3339 in UTC, when kChat reports one"},
	},
	Examples: []capability.Example{{Description: "Correct the text of one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000","text":"Deployment finished"}`)}},
}

var messagesDelete = capability.Descriptor{
	ID:      Provider + ".messages.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat message",
	Description: "Delete exactly one confirmed message of a channel this connection may reach; deleting a thread " +
		"root also removes its replies, and users cannot restore the result. kChat decides whether the token " +
		"may delete a message of another author",
	Tags:                  []string{"infomaniak", "kchat", "messages", "delete"},
	Risk:                  messageChangeRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":` + idSchema + `,"channel_id":` + idSchema + `,"deleted":{"type":"boolean"}},` +
		`"required":["id","channel_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{postIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted message"},
		{Name: "channel_id", Description: "Channel the message belonged to"},
		{Name: "deleted", Description: "True once kChat confirmed the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
}

// postJSON is the subset of the kChat Post resource this provider reads.
type postJSON struct {
	ID        string `json:"id"`
	CreateAt  int64  `json:"create_at"`
	EditAt    int64  `json:"edit_at"`
	DeleteAt  int64  `json:"delete_at"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id"`
	Message   string `json:"message"`
	Type      string `json:"type"`
}

// postListJSON is the cursor-ish pagination envelope kChat reports for a channel's posts and for a thread:
// order lists the message IDs in the answer's own sequence, posts holds them keyed by ID.
type postListJSON struct {
	Order      []string            `json:"order"`
	Posts      map[string]postJSON `json:"posts"`
	NextPostID string              `json:"next_post_id"`
	HasNext    bool                `json:"has_next"`
}

// ordered returns the posts of one postListJSON in its own order, normalised to the stable envelope.
func (p postListJSON) ordered() []MessageEntry {
	entries := make([]MessageEntry, 0, len(p.Order))
	for _, id := range p.Order {
		post, ok := p.Posts[id]
		if !ok {
			continue
		}
		entries = append(entries, messageEntryOf(post))
	}
	return entries
}

func messageEntryOf(p postJSON) MessageEntry {
	entry := MessageEntry{
		ID: p.ID, ChannelID: p.ChannelID, UserID: p.UserID, RootID: p.RootID,
		Message: bounded(p.Message), Type: p.Type, CreatedAt: msToRFC3339(p.CreateAt),
	}
	if p.EditAt > 0 {
		entry.EditedAt = msToRFC3339(p.EditAt)
	}
	return entry
}

// MessageEntry is the stable Qatlas view of one kChat message (post).
type MessageEntry struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	EditedAt  string `json:"edited_at,omitempty"`
}

// MessagesPage is one paginated listing of the messages of one channel.
type MessagesPage struct {
	ChannelID string         `json:"channel_id"`
	Messages  []MessageEntry `json:"messages"`
	Page      int            `json:"page"`
	HasMore   bool           `json:"has_more"`
	Count     int            `json:"count"`
}

// Thread is one paginated read of a message and the rest of its thread.
type Thread struct {
	PostID    string         `json:"post_id"`
	ChannelID string         `json:"channel_id"`
	Messages  []MessageEntry `json:"messages"`
	HasMore   bool           `json:"has_more"`
	Cursor    string         `json:"cursor,omitempty"`
	Count     int            `json:"count"`
}

// SentMessage is the answer of one confirmed send.
type SentMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

type messagesListArguments struct {
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

func invokeMessagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list messages"
	var input messagesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyChannelScope(ctx, op, input.ChannelID); err != nil {
		return nil, err
	}
	return client.ListMessages(ctx, input.ChannelID, page, limit)
}

// ListMessages reads one page of a channel's messages, newest first, exactly as kChat paginates its own
// GetPostsForChannel endpoint: it never follows a further page itself.
func (c *Client) ListMessages(ctx context.Context, channelID string, page, limit int) (*MessagesPage, error) {
	const op = "list messages"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(limit)}}
	var list postListJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID)+"/posts", query, nil,
		&list, false); err != nil {
		return nil, err
	}
	entries := list.ordered()
	return &MessagesPage{ChannelID: channelID, Messages: entries, Page: page, HasMore: list.HasNext, Count: len(entries)}, nil
}

type messagesThreadArguments struct {
	PostID string `json:"post_id"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

func invokeMessagesThread(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read thread"
	var input messagesThreadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	if input.Cursor != "" && !validMattermostID(input.Cursor) {
		return nil, invalidRequest("cursor is not a cursor of this thread; start it again without cursor")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// The channel of a thread is learned from the root post itself and verified before the thread is ever
	// read, the same guarantee messages.list and messages.send give a directly named channel_id.
	root, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return client.ReadThread(ctx, input.PostID, root.ChannelID, input.Cursor, limit)
}

// verifyPostScope binds a post_id argument to this connection through the post's channel: it reads the post,
// refuses an answer for a different post or a deleted post, then checks the channel against the local
// allow-list and live against the bound teams. Every tool that takes a foreign post_id goes through it
// before any detail is returned or any change is sent. A refusal is an invalid request that names neither
// the foreign target nor any message content.
func (c *Client) verifyPostScope(ctx context.Context, resolved *config.Resolved, op, postID string) (postJSON, error) {
	var post postJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/posts/"+url.PathEscape(postID), nil, nil, &post, false); err != nil {
		return postJSON{}, err
	}
	if post.ID != postID {
		return postJSON{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned a different post than the one requested"}
	}
	if post.DeleteAt > 0 {
		return postJSON{}, invalidRequest("post_id refers to a deleted message")
	}
	if err := selectChannel(resolved, post.ChannelID); err != nil {
		return postJSON{}, err
	}
	if err := c.verifyChannelScope(ctx, op, post.ChannelID); err != nil {
		return postJSON{}, err
	}
	return post, nil
}

// ReadThread reads one page of a thread, forward from cursor when it is given, and pages the result exactly
// as kChat answers it: it never follows a further page itself.
func (c *Client) ReadThread(ctx context.Context, postID, channelID, cursor string, limit int) (*Thread, error) {
	const op = "read thread"
	query := url.Values{"perPage": {strconv.Itoa(limit)}, "direction": {"down"}}
	if cursor != "" {
		query.Set("fromPost", cursor)
	}
	var list postListJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/posts/"+url.PathEscape(postID)+"/thread", query, nil, &list,
		false); err != nil {
		return nil, err
	}
	entries := list.ordered()
	thread := &Thread{PostID: postID, ChannelID: channelID, Messages: entries, HasMore: list.HasNext, Count: len(entries)}
	if list.HasNext && list.NextPostID != "" {
		thread.Cursor = list.NextPostID
	}
	return thread, nil
}

type messagesSendArguments struct {
	ChannelID string   `json:"channel_id"`
	Text      string   `json:"text"`
	RootID    string   `json:"root_id"`
	FileIDs   []string `json:"file_ids"`
}

func invokeMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "send message"
	var input messagesSendArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	if input.RootID != "" && !validMattermostID(input.RootID) {
		return nil, invalidRequest("root_id must be a kChat-style identifier")
	}
	if err := checkFileIDs(input.FileIDs); err != nil {
		return nil, err
	}
	if (len(input.FileIDs) == 0 || input.Text != "") && !validMessageText(input.Text) {
		return nil, invalidRequest("text must be 1 to " + itoa(maxMessageLength) +
			" characters without unsupported control characters, unless file_ids is given")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyChannelScope(ctx, op, input.ChannelID); err != nil {
		return nil, err
	}
	if input.RootID != "" {
		var root postJSON
		if err := client.do(ctx, op, http.MethodGet, "/api/v4/posts/"+url.PathEscape(input.RootID), nil, nil, &root,
			false); err != nil {
			return nil, err
		}
		if root.ChannelID != input.ChannelID {
			return nil, invalidRequest("root_id does not belong to channel_id")
		}
	}
	if err := client.verifyOwnUploads(ctx, op, input.ChannelID, input.FileIDs); err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, input.ChannelID, input.Text, input.RootID, input.FileIDs...)
}

// maxMessageFiles bounds the files attached to one message.
const maxMessageFiles = 10

// checkFileIDs validates the attachment identifiers locally: 1 to maxMessageFiles distinct kChat IDs, or none.
func checkFileIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxMessageFiles {
		return invalidRequest("file_ids takes at most " + itoa(maxMessageFiles) + " files")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validMattermostID(id) {
			return invalidRequest("file_ids must be kChat-style identifiers")
		}
		if seen[id] {
			return invalidRequest("file_ids must be distinct")
		}
		seen[id] = true
	}
	return nil
}

// verifyOwnUploads makes sure every file is an upload of the token's own user that no message holds yet and
// that, when kChat reports its channel, was made for the target channel. Any other file, even one of a
// bound channel, is refused without naming it, so a message never attaches someone else's file.
func (c *Client) verifyOwnUploads(ctx context.Context, op, channelID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var info fileJSON
		if err := c.do(ctx, op, http.MethodGet, "/api/v4/files/"+url.PathEscape(id)+"/info", nil, nil, &info,
			false); err != nil {
			return err
		}
		if info.ID != id {
			return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "kChat returned a different file than the one requested"}
		}
		if info.DeleteAt > 0 || info.UserID != own || info.PostID != "" ||
			(info.ChannelID != "" && info.ChannelID != channelID) {
			return invalidRequest("file_ids must name own, unattached uploads for channel_id")
		}
	}
	return nil
}

// SendMessage sends exactly one message, once, and never repeats it: a failure after the request may have
// reached kChat says so instead, see the uncertain suffix (*Client).do adds for a change request.
func (c *Client) SendMessage(ctx context.Context, channelID, text, rootID string, fileIDs ...string) (*SentMessage, error) {
	const op = "send message"
	body := map[string]any{"channel_id": channelID, "message": text}
	if len(fileIDs) > 0 {
		body["file_ids"] = fileIDs
	}
	if rootID != "" {
		body["root_id"] = rootID
	}
	var post postJSON
	if err := c.do(ctx, op, http.MethodPost, "/api/v4/posts", nil, body, &post, true); err != nil {
		return nil, err
	}
	if post.ID == "" || post.ChannelID != channelID || (rootID != "" && post.RootID != rootID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertain}
	}
	sent := &SentMessage{ID: post.ID, ChannelID: post.ChannelID, CreatedAt: msToRFC3339(post.CreateAt)}
	if post.RootID != "" {
		sent.RootID = post.RootID
	}
	return sent, nil
}

// validMessageText refuses control characters other than line breaks and tabs, and bounds the length. It
// never inspects the text for anything beyond its shape: content is never interpreted or executed.
func validMessageText(value string) bool {
	if count := utf8.RuneCountInString(value); !utf8.ValidString(value) || count < 1 || count > maxMessageLength {
		return false
	}
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return false
		}
	}
	return true
}

// UpdatedMessage is the answer of one confirmed edit.
type UpdatedMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	EditedAt  string `json:"edited_at,omitempty"`
}

// DeletedMessage is the answer of one confirmed delete.
type DeletedMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Deleted   bool   `json:"deleted"`
}

type postIDArguments struct {
	PostID string `json:"post_id"`
}

type messagesUpdateArguments struct {
	PostID string `json:"post_id"`
	Text   string `json:"text"`
}

func invokeMessagesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get message"
	var input postIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return messageEntryOf(post), nil
}

func invokeMessagesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update message"
	var input messagesUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	if !validMessageText(input.Text) {
		return nil, invalidRequest("text must be 1 to " + itoa(maxMessageLength) +
			" characters without unsupported control characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return client.UpdateMessage(ctx, post.ID, post.ChannelID, input.Text)
}

// UpdateMessage replaces the text of one post with exactly one PATCH carrying nothing but the message, and
// never repeats it: a failure after the request may have reached kChat says so instead.
func (c *Client) UpdateMessage(ctx context.Context, postID, channelID, text string) (*UpdatedMessage, error) {
	const op = "update message"
	var post postJSON
	if err := c.doWith(ctx, op, http.MethodPut, "/api/v4/posts/"+url.PathEscape(postID)+"/patch", nil,
		map[string]string{"message": text}, &post, uncertainUpdate); err != nil {
		return nil, err
	}
	if post.ID != postID || post.ChannelID != channelID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainUpdate}
	}
	return &UpdatedMessage{ID: post.ID, ChannelID: post.ChannelID, EditedAt: msToRFC3339(post.EditAt)}, nil
}

func invokeMessagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete message"
	var input postIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessage(ctx, post.ID, post.ChannelID)
}

// DeleteMessage deletes one post with exactly one DELETE and never repeats it. kChat soft-deletes the post,
// which users cannot restore.
func (c *Client) DeleteMessage(ctx context.Context, postID, channelID string) (*DeletedMessage, error) {
	const op = "delete message"
	if err := c.doWith(ctx, op, http.MethodDelete, "/api/v4/posts/"+url.PathEscape(postID), nil, nil, nil,
		uncertainDelete); err != nil {
		return nil, err
	}
	return &DeletedMessage{ID: postID, ChannelID: channelID, Deleted: true}, nil
}
