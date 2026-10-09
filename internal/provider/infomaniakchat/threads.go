package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxThreadLimit is the largest page of followed threads a list asks kChat for.
	maxThreadLimit = 100

	uncertainFollow   = "; the thread may have been followed, read the thread before following it again"
	uncertainUnfollow = "; the thread may have been unfollowed, read the thread before unfollowing it again"

	threadOutOfTeam = "thread_id refers to a message outside the reachable channels of team_id"
)

var threadIDArgument = capability.Argument{Name: "thread_id", Required: true,
	Description: "Identifier of the thread's root message; its channel must be a reachable channel of team_id, " +
		"inside this connection's channel allow-list when it has one"}

var threadEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"channel_id":` + idSchema + `,"reply_count":{"type":"integer"},` +
	`"last_reply_at":{"type":"string"},"last_viewed_at":{"type":"string"},` +
	`"participants":{"type":"array","items":` + idSchema + `},"root":` + messageEntrySchema + `},` +
	`"required":["id","channel_id","reply_count","participants","root"],"additionalProperties":false}`

var threadEntryFields = []capability.Field{
	{Name: "id", Description: "Thread identifier, the identifier of its root message"},
	{Name: "channel_id", Description: "Channel of the thread"},
	{Name: "reply_count", Description: "Number of replies in the thread"},
	{Name: "last_reply_at", Description: "Time of the latest reply, normalised to RFC 3339 in UTC, when kChat reports one"},
	{Name: "last_viewed_at", Description: "Time the token's own user last viewed the thread, normalised to RFC 3339 in UTC, when kChat reports one"},
	{Name: "participants", Description: "User identifiers of the thread's participants; no profile data"},
	{Name: "root", Description: "Root message of the thread, untrusted data"},
}

func threadChangeRisk() capability.Risk {
	return capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var threadsList = capability.Descriptor{
	ID:      Provider + ".threads.list",
	Version: 1,
	Title:   "List followed Infomaniak kChat threads",
	Description: "List the threads the token's own user follows in one team this connection is bound to, one kChat " +
		"page at a time; only threads of team channels this connection may reach are returned, never direct or " +
		"group threads",
	Tags:     []string{"infomaniak", "kchat", "threads", "list", "following"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"page":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":` +
		itoa(maxThreadLimit) + `}},"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"threads":{"type":"array","items":` + threadEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["team_id","threads","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "limit", Description: "Threads per kChat page, 1 to " + itoa(maxThreadLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, threadEntryFields...),
		capability.Field{Name: "team_id", Description: "Team that was read"},
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may exist; " +
			"pages are never read on by themselves"},
		capability.Field{Name: "count", Description: "Number of threads reported on this page, after the reachable-channel filter"},
	),
	Examples: []capability.Example{{Description: "List the followed threads of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var threadsGet = capability.Descriptor{
	ID:      Provider + ".threads.get",
	Version: 1,
	Title:   "Get a followed Infomaniak kChat thread",
	Description: "Read the thread state of the token's own user for exactly one thread, identified by its root " +
		"message, in a reachable channel of one team this connection is bound to",
	Tags:     []string{"infomaniak", "kchat", "threads", "get"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"thread_id":` +
		idSchema + `},"required":["team_id","thread_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(threadEntrySchema),
	Arguments:    []capability.Argument{teamIDArgument, threadIDArgument},
	Fields:       threadEntryFields,
	Examples: []capability.Example{{Description: "Read one thread",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","thread_id":"abc123post00000000000000000"}`)}},
}

func threadChangeDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".threads." + action,
		Version:     1,
		Title:       title,
		Description: description,
		Tags:        []string{"infomaniak", "kchat", "threads", "following", action},
		Risk:        threadChangeRisk(),
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"thread_id":` +
			idSchema + `},"required":["team_id","thread_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"team_id":` + idSchema + `,"thread_id":` + idSchema + `,"following":{"type":"boolean"}},` +
			`"required":["team_id","thread_id","following"],"additionalProperties":false}`),
		Arguments: []capability.Argument{teamIDArgument, threadIDArgument},
		Fields: []capability.Field{
			{Name: "team_id", Description: "Team of the thread"},
			{Name: "thread_id", Description: "Thread that was changed"},
			{Name: "following", Description: "Whether the token's own user follows the thread now"},
		},
		Examples: []capability.Example{{Description: title,
			Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","thread_id":"abc123post00000000000000000"}`)}},
	}
}

var threadsFollow = threadChangeDescriptor("follow", "Follow an Infomaniak kChat thread",
	"Make the token's own user follow exactly one confirmed thread in a reachable channel of one team this "+
		"connection is bound to; following a followed thread changes nothing")

var threadsUnfollow = threadChangeDescriptor("unfollow", "Unfollow an Infomaniak kChat thread",
	"Make the token's own user stop following exactly one confirmed thread in a reachable channel of one team "+
		"this connection is bound to; the thread and its messages stay and can be followed again")

// ThreadEntry is the stable Qatlas view of one followed thread.
type ThreadEntry struct {
	ID           string       `json:"id"`
	ChannelID    string       `json:"channel_id"`
	ReplyCount   int          `json:"reply_count"`
	LastReplyAt  string       `json:"last_reply_at,omitempty"`
	LastViewedAt string       `json:"last_viewed_at,omitempty"`
	Participants []string     `json:"participants"`
	Root         MessageEntry `json:"root"`
}

// ThreadsPage is one kChat page of followed threads, reduced to the reachable channels.
type ThreadsPage struct {
	TeamID  string        `json:"team_id"`
	Threads []ThreadEntry `json:"threads"`
	Page    int           `json:"page"`
	HasMore bool          `json:"has_more"`
	Count   int           `json:"count"`
}

// ThreadFollowing is the answer of one confirmed follow or unfollow.
type ThreadFollowing struct {
	TeamID    string `json:"team_id"`
	ThreadID  string `json:"thread_id"`
	Following bool   `json:"following"`
}

// userThreadJSON is the subset of the kChat UserThread resource this provider reads. Participants are
// decoded down to their identifiers only.
type userThreadJSON struct {
	ID           string `json:"id"`
	ReplyCount   int    `json:"reply_count"`
	LastReplyAt  int64  `json:"last_reply_at"`
	LastViewedAt int64  `json:"last_viewed_at"`
	Participants []struct {
		ID string `json:"id"`
	} `json:"participants"`
	Post postJSON `json:"post"`
}

type threadArguments struct {
	TeamID   string `json:"team_id"`
	ThreadID string `json:"thread_id"`
	Page     int    `json:"page"`
	Limit    int    `json:"limit"`
}

func threadsPath(teamID string) string {
	return "/api/v4/users/me/teams/" + url.PathEscape(teamID) + "/threads"
}

// threadEntryOf reduces one thread to its stable view. It reports false for a thread whose root post is
// missing, deleted, or not the thread itself, and for one outside the reachable channels.
func threadEntryOf(t *userThreadJSON, reachable map[string]*channelJSON) (ThreadEntry, bool) {
	if !validMattermostID(t.ID) || t.Post.ID != t.ID || t.Post.DeleteAt != 0 || t.Post.RootID != "" ||
		reachable[t.Post.ChannelID] == nil {
		return ThreadEntry{}, false
	}
	participants := make([]string, 0, len(t.Participants))
	for _, p := range t.Participants {
		if validMattermostID(p.ID) {
			participants = append(participants, p.ID)
		}
	}
	return ThreadEntry{ID: t.ID, ChannelID: t.Post.ChannelID, ReplyCount: t.ReplyCount,
		LastReplyAt: msToRFC3339(t.LastReplyAt), LastViewedAt: msToRFC3339(t.LastViewedAt),
		Participants: participants, Root: messageEntryOf(t.Post)}, true
}

func invokeThreadsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list threads"
	var input threadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	if input.Page < 1 || input.Limit < 1 || input.Limit > maxThreadLimit {
		return nil, invalidRequest("page must be at least 1 and limit 1 to " + itoa(maxThreadLimit))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListThreads(ctx, input.TeamID, input.Page, input.Limit)
}

// ListThreads reads the reachable channels of the team and one kChat page of followed threads, and keeps
// only threads whose root is a live post of a reachable channel. It never reads on to a further page, and
// never asks for deleted threads or extended user data.
func (c *Client) ListThreads(ctx context.Context, teamID string, page, limit int) (*ThreadsPage, error) {
	const op = "list threads"
	reachable, err := c.reachableChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "pageSize": {strconv.Itoa(limit)}}
	var list struct {
		Threads []userThreadJSON `json:"threads"`
	}
	if err := c.do(ctx, op, http.MethodGet, threadsPath(teamID), query, nil, &list, false); err != nil {
		return nil, err
	}
	entries := make([]ThreadEntry, 0, len(list.Threads))
	for i := range list.Threads {
		if entry, ok := threadEntryOf(&list.Threads[i], reachable); ok {
			entries = append(entries, entry)
		}
	}
	return &ThreadsPage{TeamID: teamID, Threads: entries, Page: page, HasMore: len(list.Threads) >= limit,
		Count: len(entries)}, nil
}

// threadTarget validates the arguments locally, before any secret is read, then binds the thread's root
// post to its channel and the channel to the reachable channels of team_id. The channel must belong to
// that very team, not merely to some bound team.
func threadTarget(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string) (*Client, threadArguments, map[string]*channelJSON, error) {
	var input threadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, input, nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, input, nil, err
	}
	if !validMattermostID(input.ThreadID) {
		return nil, input, nil, invalidRequest("thread_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, input, nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.ThreadID)
	if err != nil {
		return nil, input, nil, err
	}
	if post.RootID != "" {
		return nil, input, nil, invalidRequest("thread_id must be the root message of a thread")
	}
	reachable, err := client.reachableChannels(ctx, op, input.TeamID)
	if err != nil {
		return nil, input, nil, err
	}
	if reachable[post.ChannelID] == nil {
		return nil, input, nil, invalidRequest(threadOutOfTeam)
	}
	return client, input, reachable, nil
}

func invokeThreadsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read thread state"
	client, input, reachable, err := threadTarget(ctx, resolved, secrets, red, raw, op)
	if err != nil {
		return nil, err
	}
	var thread userThreadJSON
	path := threadsPath(input.TeamID) + "/" + url.PathEscape(input.ThreadID)
	if err := client.do(ctx, op, http.MethodGet, path, nil, nil, &thread, false); err != nil {
		return nil, err
	}
	// The answer must describe the thread that was bound above, in a reachable channel.
	entry, ok := threadEntryOf(&thread, reachable)
	if !ok || entry.ID != input.ThreadID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned a different thread than the one requested"}
	}
	return entry, nil
}

func invokeThreadsFollow(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeThreadFollowing(ctx, resolved, secrets, red, raw, "follow thread", true)
}

func invokeThreadsUnfollow(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeThreadFollowing(ctx, resolved, secrets, red, raw, "unfollow thread", false)
}

func invokeThreadFollowing(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string, follow bool) (any, error) {
	client, input, _, err := threadTarget(ctx, resolved, secrets, red, raw, op)
	if err != nil {
		return nil, err
	}
	return client.ChangeFollowing(ctx, input.TeamID, input.ThreadID, follow)
}

// ChangeFollowing follows or unfollows one thread with exactly one request and never repeats it: a failure
// after the request may have reached kChat says so instead. kChat answers with a status only.
func (c *Client) ChangeFollowing(ctx context.Context, teamID, threadID string, follow bool) (*ThreadFollowing, error) {
	op, method, suffix := "unfollow thread", http.MethodDelete, uncertainUnfollow
	if follow {
		op, method, suffix = "follow thread", http.MethodPut, uncertainFollow
	}
	var answer struct {
		Status string `json:"status"`
	}
	path := threadsPath(teamID) + "/" + url.PathEscape(threadID) + "/following"
	if err := c.doWith(ctx, op, method, path, nil, nil, &answer, suffix); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + suffix}
	}
	return &ThreadFollowing{TeamID: teamID, ThreadID: threadID, Following: follow}, nil
}
