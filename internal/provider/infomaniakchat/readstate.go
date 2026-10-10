package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uncertainMarkRead   = "; the channel may have been marked as read, read its unread counts before marking it again"
	uncertainMarkUnread = "; the message may have been marked as unread, read the channel's unread counts before " +
		"marking it again"
	uncertainThreadRead = "; the thread may have been marked as read, read the thread before marking it again"
)

var channelsUnread = capability.Descriptor{
	ID:      Provider + ".channels.unread",
	Version: 1,
	Title:   "Read the unread counts of an Infomaniak kChat channel",
	Description: "Read the unread message and mention counts of exactly one channel this connection may reach, for " +
		"the token's own user",
	Tags:     []string{"infomaniak", "kchat", "channels", "unread", "read-state"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"msg_count":{"type":"integer","minimum":0},"mention_count":{"type":"integer","minimum":0}},` +
		`"required":["channel_id","msg_count","mention_count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{channelIDArgument},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel that was read"},
		{Name: "msg_count", Description: "Number of unread messages of the token's own user in the channel"},
		{Name: "mention_count", Description: "Number of unread mentions of the token's own user in the channel"},
	},
	Examples: []capability.Example{{Description: "Read the unread counts of one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000"}`)}},
}

var channelsMarkRead = capability.Descriptor{
	ID:      Provider + ".channels.markread",
	Version: 1,
	Title:   "Mark an Infomaniak kChat channel as read",
	Description: "Mark exactly one confirmed channel this connection may reach as read for the token's own user; kChat " +
		"treats this as viewing the channel, so it also clears that user's push notifications for it. Marking a read " +
		"channel changes nothing",
	Tags:     []string{"infomaniak", "kchat", "channels", "read-state", "markread"},
	Risk:     messageChangeRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,` +
		`"read":{"type":"boolean"},"last_viewed_at":{"type":"string"}},` +
		`"required":["channel_id","read"],"additionalProperties":false}`),
	Arguments: []capability.Argument{channelIDArgument},
	Fields: []capability.Field{
		{Name: "channel_id", Description: "Channel that was marked"},
		{Name: "read", Description: "True once kChat confirmed the channel as viewed"},
		{Name: "last_viewed_at", Description: "Time kChat now holds as the last view of the channel, normalised to " +
			"RFC 3339 in UTC, when kChat reports one"},
	},
	Examples: []capability.Example{{Description: "Mark one channel as read",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000"}`)}},
}

var messagesMarkUnread = capability.Descriptor{
	ID:      Provider + ".messages.markunread",
	Version: 1,
	Title:   "Mark an Infomaniak kChat message as unread",
	Description: "Mark the channel of exactly one confirmed message this connection may reach as unread from that " +
		"message on, for the token's own user; the message itself is not changed",
	Tags:     []string{"infomaniak", "kchat", "messages", "read-state", "markunread"},
	Risk:     messageChangeRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,` +
		`"channel_id":` + idSchema + `,"last_viewed_at":{"type":"string"}},` +
		`"required":["post_id","channel_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{postIDArgument},
	Fields: []capability.Field{
		{Name: "post_id", Description: "Message the channel was marked unread from"},
		{Name: "channel_id", Description: "Channel of the message"},
		{Name: "last_viewed_at", Description: "Time kChat now holds as the last view of the channel, normalised to " +
			"RFC 3339 in UTC, when kChat reports one"},
	},
	Examples: []capability.Example{{Description: "Mark a channel as unread from one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
}

var threadsMarkRead = capability.Descriptor{
	ID:      Provider + ".threads.markread",
	Version: 1,
	Title:   "Mark an Infomaniak kChat thread as read",
	Description: "Set the read state of exactly one confirmed followed thread of the token's own user, in a reachable " +
		"channel of one team this connection is bound to, to a time that is not in the future; the current time " +
		"when none is given. Marking a read thread changes nothing",
	Tags:     []string{"infomaniak", "kchat", "threads", "read-state", "markread"},
	Risk:     threadChangeRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"thread_id":` +
		idSchema + `,"timestamp":{"type":"string","minLength":1,"maxLength":64}},` +
		`"required":["team_id","thread_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"thread_id":` + idSchema + `,"last_viewed_at":{"type":"string"}},` +
		`"required":["team_id","thread_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, threadIDArgument,
		{Name: "timestamp", Description: "RFC 3339 time the thread is read up to, not in the future; the current " +
			"time when omitted"}},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Team of the thread"},
		{Name: "thread_id", Description: "Thread that was marked"},
		{Name: "last_viewed_at", Description: "Time kChat now holds as the last view of the thread, normalised to " +
			"RFC 3339 in UTC, when kChat reports one"},
	},
	Examples: []capability.Example{{Description: "Mark one thread as read up to now",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","thread_id":"abc123post00000000000000000"}`)}},
}

// ChannelUnread is the unread state of the token's own user in one channel.
type ChannelUnread struct {
	ChannelID    string `json:"channel_id"`
	MsgCount     int    `json:"msg_count"`
	MentionCount int    `json:"mention_count"`
}

// ChannelRead is the answer of one confirmed channel view.
type ChannelRead struct {
	ChannelID    string `json:"channel_id"`
	Read         bool   `json:"read"`
	LastViewedAt string `json:"last_viewed_at,omitempty"`
}

// MessageUnread is the answer of one confirmed mark-as-unread.
type MessageUnread struct {
	PostID       string `json:"post_id"`
	ChannelID    string `json:"channel_id"`
	LastViewedAt string `json:"last_viewed_at,omitempty"`
}

// ThreadRead is the answer of one confirmed thread read mark.
type ThreadRead struct {
	TeamID       string `json:"team_id"`
	ThreadID     string `json:"thread_id"`
	LastViewedAt string `json:"last_viewed_at,omitempty"`
}

func invokeChannelsUnread(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read unread counts"
	client, channelID, err := channelReadTarget(ctx, resolved, secrets, red, raw, op)
	if err != nil {
		return nil, err
	}
	var answer struct {
		ChannelID    string `json:"channel_id"`
		MsgCount     int    `json:"msg_count"`
		MentionCount int    `json:"mention_count"`
	}
	path := "/api/v4/users/me/channels/" + url.PathEscape(channelID) + "/unread"
	if err := client.do(ctx, op, http.MethodGet, path, nil, nil, &answer, false); err != nil {
		return nil, err
	}
	if answer.ChannelID != channelID || answer.MsgCount < 0 || answer.MentionCount < 0 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned unread counts of a different channel than the one requested"}
	}
	return ChannelUnread{ChannelID: channelID, MsgCount: answer.MsgCount, MentionCount: answer.MentionCount}, nil
}

// channelReadTarget checks channel_id locally before any secret is read, then live against the bound teams.
func channelReadTarget(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string) (*Client, string, error) {
	var input struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, "", providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, "", err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, "", err
	}
	if err := client.verifyChannelScope(ctx, op, input.ChannelID); err != nil {
		return nil, "", err
	}
	return client, input.ChannelID, nil
}

func invokeChannelsMarkRead(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "mark channel as read"
	client, channelID, err := channelReadTarget(ctx, resolved, secrets, red, raw, op)
	if err != nil {
		return nil, err
	}
	return client.MarkChannelRead(ctx, channelID)
}

// MarkChannelRead views one channel as the token's own user with exactly one POST and never repeats it: a
// failure after the request may have reached kChat says so instead. The body carries the channel only, never
// a previous channel, so no other channel's notifications are touched.
func (c *Client) MarkChannelRead(ctx context.Context, channelID string) (*ChannelRead, error) {
	const op = "mark channel as read"
	var answer struct {
		Status string                     `json:"status"`
		Times  map[string]json.RawMessage `json:"last_viewed_at_times"`
	}
	body := struct {
		ChannelID string `json:"channel_id"`
	}{channelID}
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/channels/members/me/view", nil, body, &answer,
		uncertainMarkRead); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainMarkRead}
	}
	var viewed int64
	if json.Unmarshal(answer.Times[channelID], &viewed) != nil {
		viewed = 0
	}
	return &ChannelRead{ChannelID: channelID, Read: true, LastViewedAt: msToRFC3339(viewed)}, nil
}

func invokeMessagesMarkUnread(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "mark message as unread"
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
	return client.MarkMessageUnread(ctx, post.ID, post.ChannelID)
}

// MarkMessageUnread marks the channel of one post as unread from that post with exactly one bodyless POST and
// never repeats it. The answer must describe the channel of the post that was bound before.
func (c *Client) MarkMessageUnread(ctx context.Context, postID, channelID string) (*MessageUnread, error) {
	const op = "mark message as unread"
	var answer struct {
		ChannelID    string `json:"channel_id"`
		LastViewedAt int64  `json:"last_viewed_at"`
	}
	path := "/api/v4/users/me/posts/" + url.PathEscape(postID) + "/set_unread"
	if err := c.doWith(ctx, op, http.MethodPost, path, nil, nil, &answer, uncertainMarkUnread); err != nil {
		return nil, err
	}
	if answer.ChannelID != channelID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainMarkUnread}
	}
	return &MessageUnread{PostID: postID, ChannelID: channelID, LastViewedAt: msToRFC3339(answer.LastViewedAt)}, nil
}

// threadReadTime turns the optional timestamp argument into the millisecond epoch time sent to kChat. It runs
// before any secret is read and refuses a time kChat could not have seen yet.
func threadReadTime(timestamp string, now time.Time) (int64, error) {
	if timestamp == "" {
		return now.UnixMilli(), nil
	}
	at, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return 0, invalidRequest("timestamp must be an RFC 3339 time")
	}
	if at.After(now) {
		return 0, invalidRequest("timestamp must not be in the future")
	}
	if at.UnixMilli() <= 0 {
		return 0, invalidRequest("timestamp must be after 1970-01-01T00:00:00Z")
	}
	return at.UnixMilli(), nil
}

func invokeThreadsMarkRead(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "mark thread as read"
	var timestamp struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &timestamp); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	at, err := threadReadTime(timestamp.Timestamp, time.Now())
	if err != nil {
		return nil, err
	}
	client, input, _, err := threadTarget(ctx, resolved, secrets, red, raw, op)
	if err != nil {
		return nil, err
	}
	return client.MarkThreadRead(ctx, input.TeamID, input.ThreadID, at)
}

// MarkThreadRead sets the read state of one followed thread with exactly one bodyless PUT and never repeats
// it. kChat answers with the thread, which must be the one that was bound.
func (c *Client) MarkThreadRead(ctx context.Context, teamID, threadID string, at int64) (*ThreadRead, error) {
	const op = "mark thread as read"
	var answer struct {
		ID           string `json:"id"`
		LastViewedAt int64  `json:"last_viewed_at"`
	}
	path := threadsPath(teamID) + "/" + url.PathEscape(threadID) + "/read/" + strconv.FormatInt(at, 10)
	if err := c.doWith(ctx, op, http.MethodPut, path, nil, nil, &answer, uncertainThreadRead); err != nil {
		return nil, err
	}
	if answer.ID != threadID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainThreadRead}
	}
	return &ThreadRead{TeamID: teamID, ThreadID: threadID, LastViewedAt: msToRFC3339(answer.LastViewedAt)}, nil
}
