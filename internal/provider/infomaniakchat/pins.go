package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uncertainPin   = "; the message may have been pinned, list the pinned messages before pinning it again"
	uncertainUnpin = "; the message may have been unpinned, list the pinned messages before unpinning it again"
)

var pinsList = capability.Descriptor{
	ID:      Provider + ".pins.list",
	Version: 1,
	Title:   "List Infomaniak kChat pinned messages",
	Description: "List the pinned messages of one channel this connection may reach, one page at a time; kChat " +
		"answers with every pinned message at once, so Qatlas slices that answer itself",
	Tags:     []string{"infomaniak", "kchat", "pins", "list", "messages"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"channel_id":` + idSchema + `,"messages":{"type":"array","items":` + messageEntrySchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["channel_id","messages","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		channelIDArgument,
		{Name: "page", Description: "1-based page of the pinned messages; the first page when omitted"},
		{Name: "limit", Description: "Messages per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, messageEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Total number of pages"},
		capability.Field{Name: "total", Description: "Number of pinned messages of the channel"},
		capability.Field{Name: "count", Description: "Number of messages reported on this page"},
	),
	Examples: []capability.Example{{Description: "List the pinned messages of one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000"}`)}},
}

func pinChangeDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".messages." + action,
		Version:     1,
		Title:       title,
		Description: description,
		Tags:        []string{"infomaniak", "kchat", "messages", "pin", action},
		Risk:        messageChangeRisk(capability.EffectUpdate),
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
			`"required":["post_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"post_id":` + idSchema + `,"pinned":{"type":"boolean"}},` +
			`"required":["post_id","pinned"],"additionalProperties":false}`),
		Arguments: []capability.Argument{postIDArgument},
		Fields: []capability.Field{
			{Name: "post_id", Description: "Message that was " + action + "ned"},
			{Name: "pinned", Description: "Whether the message is pinned now"},
		},
		Examples: []capability.Example{{Description: title,
			Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
	}
}

var messagesPin = pinChangeDescriptor("pin", "Pin an Infomaniak kChat message",
	"Pin exactly one confirmed message of a channel this connection may reach; pinning a pinned message "+
		"changes nothing. kChat decides whether the token may pin it")

var messagesUnpin = pinChangeDescriptor("unpin", "Unpin an Infomaniak kChat message",
	"Unpin exactly one confirmed message of a channel this connection may reach; the message itself stays and "+
		"can be pinned again. kChat decides whether the token may unpin it")

// PinsPage is one paginated listing of the pinned messages of one channel.
type PinsPage struct {
	ChannelID string         `json:"channel_id"`
	Messages  []MessageEntry `json:"messages"`
	Page      int            `json:"page"`
	Pages     int            `json:"pages"`
	Total     int            `json:"total"`
	Count     int            `json:"count"`
}

// PinState is the answer of one confirmed pin or unpin.
type PinState struct {
	PostID string `json:"post_id"`
	Pinned bool   `json:"pinned"`
}

func invokePinsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list pinned messages"
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
	return client.ListPins(ctx, input.ChannelID, page, limit)
}

// ListPins reads the pinned posts of one channel, which kChat answers without pagination, and pages the
// result itself. A post kChat reports for another channel is dropped.
func (c *Client) ListPins(ctx context.Context, channelID string, page, limit int) (*PinsPage, error) {
	const op = "list pinned messages"
	var list postListJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/channels/"+url.PathEscape(channelID)+"/pinned", nil, nil,
		&list, false); err != nil {
		return nil, err
	}
	all := list.ordered()
	entries := make([]MessageEntry, 0, len(all))
	for _, entry := range all {
		if entry.ChannelID == channelID {
			entries = append(entries, entry)
		}
	}
	window, pages, total := windowOf(entries, page, limit)
	return &PinsPage{ChannelID: channelID, Messages: window, Page: page, Pages: pages, Total: total,
		Count: len(window)}, nil
}

func invokePin(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokePinChange(ctx, resolved, secrets, red, raw, "pin message", "pin")
}

func invokeUnpin(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokePinChange(ctx, resolved, secrets, red, raw, "unpin message", "unpin")
}

func invokePinChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op, action string) (any, error) {
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
	return client.ChangePin(ctx, post.ID, action)
}

// ChangePin pins or unpins one post with exactly one POST and never repeats it: a failure after the request
// may have reached kChat says so instead. kChat answers with a status only.
func (c *Client) ChangePin(ctx context.Context, postID, action string) (*PinState, error) {
	op, suffix := "pin message", uncertainPin
	if action == "unpin" {
		op, suffix = "unpin message", uncertainUnpin
	}
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/posts/"+url.PathEscape(postID)+"/"+action, nil, nil,
		&answer, suffix); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + suffix}
	}
	return &PinState{PostID: postID, Pinned: action == "pin"}, nil
}
