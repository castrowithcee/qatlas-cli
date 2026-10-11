package infomaniakchat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uncertainFlag   = "; the message may have been flagged, list the flagged messages before flagging it again"
	uncertainUnflag = "; the message may have been unflagged, list the flagged messages before unflagging it again"

	// flaggedCategory is the one preference category the flag tools write; it is never taken from arguments.
	flaggedCategory = "flagged_post"
)

var flaggedList = capability.Descriptor{
	ID:      Provider + ".flagged.list",
	Version: 1,
	Title:   "List flagged Infomaniak kChat messages",
	Description: "List the messages the token's own user flagged, for exactly one bound team or one channel this " +
		"connection may reach, one kChat page at a time; flagged messages outside that reach are dropped",
	Tags:     []string{"infomaniak", "kchat", "messages", "flagged", "list", "saved"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"channel_id":` + idSchema + `,"messages":{"type":"array","items":` + messageEntrySchema + `},` +
		`"page":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["messages","page","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "team_id", Description: "Bound team whose reachable channels are read; give exactly one of team_id " +
			"and channel_id"},
		{Name: "channel_id", Description: "Channel this connection may reach, re-checked live; give exactly one of " +
			"team_id and channel_id"},
		{Name: "page", Description: "1-based page of kChat's own list; the first page when omitted"},
		{Name: "limit", Description: "Messages per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, messageEntryFields...),
		capability.Field{Name: "team_id", Description: "Team that was read, when team_id was given"},
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may " +
			"exist; pages are never read on by themselves"},
		capability.Field{Name: "count", Description: "Number of messages reported, after the reach filter"},
	),
	Examples: []capability.Example{{Description: "List the flagged messages of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

func flagChangeDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".messages." + action,
		Version:     1,
		Title:       title,
		Description: description,
		Tags:        []string{"infomaniak", "kchat", "messages", "flag", action, "saved"},
		Risk:        messageChangeRisk(capability.EffectUpdate),
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
			`"required":["post_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"post_id":` + idSchema + `,"flagged":{"type":"boolean"}},` +
			`"required":["post_id","flagged"],"additionalProperties":false}`),
		Arguments: []capability.Argument{postIDArgument},
		Fields: []capability.Field{
			{Name: "post_id", Description: "Message whose own flag was changed"},
			{Name: "flagged", Description: "Whether the message is flagged by the token's own user now"},
		},
		Examples: []capability.Example{{Description: title,
			Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
	}
}

var messagesFlag = flagChangeDescriptor("flag", "Flag an Infomaniak kChat message",
	"Flag exactly one confirmed message of a channel this connection may reach for the token's own user only; "+
		"flagging a flagged message changes nothing and nobody else sees the flag")

var messagesUnflag = flagChangeDescriptor("unflag", "Unflag an Infomaniak kChat message",
	"Remove the own flag of exactly one confirmed message of a channel this connection may reach; the message "+
		"itself stays and can be flagged again")

// FlaggedMessages is one kChat page of the own flagged messages, reduced to the requested reach.
type FlaggedMessages struct {
	TeamID    string         `json:"team_id,omitempty"`
	ChannelID string         `json:"channel_id,omitempty"`
	Messages  []MessageEntry `json:"messages"`
	Page      int            `json:"page"`
	HasMore   bool           `json:"has_more"`
	Count     int            `json:"count"`
}

// FlagState is the answer of one confirmed flag or unflag.
type FlagState struct {
	PostID  string `json:"post_id"`
	Flagged bool   `json:"flagged"`
}

type flaggedListArguments struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	Page      int    `json:"page"`
	Limit     int    `json:"limit"`
}

func invokeFlaggedList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list flagged messages"
	var input flaggedListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if (input.TeamID == "") == (input.ChannelID == "") {
		return nil, invalidRequest("give exactly one of team_id and channel_id")
	}
	if input.TeamID != "" {
		if err := selectTeam(resolved, input.TeamID); err != nil {
			return nil, err
		}
	} else if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	if input.Page < 1 || input.Limit < 1 || input.Limit > maxListLimit {
		return nil, invalidRequest("page must be at least 1 and limit 1 to " + itoa(maxListLimit))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if input.ChannelID != "" {
		if err := client.verifyChannelScope(ctx, op, input.ChannelID); err != nil {
			return nil, err
		}
	}
	return client.ListFlagged(ctx, input)
}

// ListFlagged reads one kChat page of the own flagged posts and keeps only posts of the named channel, or of
// a live, reachable channel of the named team. It never reads on to a further page.
func (c *Client) ListFlagged(ctx context.Context, input flaggedListArguments) (*FlaggedMessages, error) {
	const op = "list flagged messages"
	var reachable map[string]*channelJSON
	query := url.Values{"page": {strconv.Itoa(input.Page - 1)}, "per_page": {strconv.Itoa(input.Limit)}}
	if input.TeamID != "" {
		var err error
		if reachable, err = c.reachableChannels(ctx, op, input.TeamID); err != nil {
			return nil, err
		}
		query.Set("team_id", input.TeamID)
	} else {
		query.Set("channel_id", input.ChannelID)
	}
	var answer json.RawMessage
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/users/me/posts/flagged", query, nil, &answer, false); err != nil {
		return nil, err
	}
	lists, err := flaggedLists(answer)
	if err != nil {
		return nil, invalidAnswer(op, "")
	}
	entries, seen, received := []MessageEntry{}, map[string]bool{}, 0
	for _, list := range lists {
		received += len(list.Order)
		for _, id := range list.Order {
			post, ok := list.Posts[id]
			if !ok || post.ID != id || post.DeleteAt != 0 || seen[id] {
				continue
			}
			if input.ChannelID != "" && post.ChannelID != input.ChannelID {
				continue
			}
			if input.TeamID != "" && reachable[post.ChannelID] == nil {
				continue
			}
			seen[id] = true
			entries = append(entries, messageEntryOf(post))
		}
	}
	return &FlaggedMessages{TeamID: input.TeamID, ChannelID: input.ChannelID, Messages: entries, Page: input.Page,
		HasMore: received >= input.Limit, Count: len(entries)}, nil
}

// flaggedLists reads the answer of the flagged list in both forms: an array of post lists, as the API
// description has it, or one post list.
func flaggedLists(answer json.RawMessage) ([]postListJSON, error) {
	trimmed := bytes.TrimSpace(answer)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var lists []postListJSON
		err := json.Unmarshal(trimmed, &lists)
		return lists, err
	}
	var list postListJSON
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, err
	}
	return []postListJSON{list}, nil
}

func invokeFlag(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeFlagChange(ctx, resolved, secrets, red, raw, "flag message", true)
}

func invokeUnflag(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeFlagChange(ctx, resolved, secrets, red, raw, "unflag message", false)
}

func invokeFlagChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string, flag bool) (any, error) {
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
	own, err := client.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	return client.ChangeFlag(ctx, post.ID, own, flag)
}

// flagPreference is the one preference entry the flag tools send.
type flagPreference struct {
	UserID   string `json:"user_id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}

// ChangeFlag flags or unflags one post for the own user with exactly one request carrying exactly one fixed
// preference entry, and never repeats it: a failure after the request may have reached kChat says so
// instead. kChat answers with a status only, which must be OK.
func (c *Client) ChangeFlag(ctx context.Context, postID, own string, flag bool) (*FlagState, error) {
	op, path, suffix := "flag message", "/api/v4/users/me/preferences", uncertainFlag
	method := http.MethodPut
	if !flag {
		op, path, suffix, method = "unflag message", path+"/delete", uncertainUnflag, http.MethodPost
	}
	body := []flagPreference{{UserID: own, Category: flaggedCategory, Name: postID, Value: "true"}}
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, method, path, nil, body, &answer, suffix); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, invalidAnswer(op, suffix)
	}
	return &FlagState{PostID: postID, Flagged: flag}, nil
}
