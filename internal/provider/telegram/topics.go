package telegram

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupTopics = "topics"
	// maxEmojiIDLength is a deliberately narrow bound: Telegram custom emoji identifiers are decimal numbers
	// of at most 20 digits.
	maxEmojiIDLength = 20
)

// topicIconColors is the fixed list of icon colors the Bot API accepts for createForumTopic.
var topicIconColors = []int64{7322096, 16766590, 13338331, 9367192, 16749490, 16478047}

var emojiIDPattern = regexp.MustCompile(`^[0-9]+$`)

const topicSelectorSchema = `"message_thread_id":{"type":"integer","minimum":1},"general":{"type":"boolean"}`

var topicSelectorArguments = []capability.Argument{
	{Name: "message_thread_id", Description: "Thread identifier of the forum topic; exactly one of " +
		"message_thread_id and general: true is required"},
	{Name: "general", Description: "True to address the General topic instead of message_thread_id"},
}

var topicsCreate = capability.Descriptor{
	ID:      Provider + ".topics.create",
	Version: 1,
	Title:   "Create a Telegram forum topic",
	Description: "Create a forum topic in a bound forum supergroup of an explicit Telegram connection. An unclear " +
		"outcome is never repeated",
	Tags:     []string{"telegram", "topics", "create"},
	Risk:     chatAdminRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	Group:    groupTopics,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":128},` +
		`"icon_color":{"type":"integer","enum":[7322096,16766590,13338331,9367192,16749490,16478047]},` +
		`"icon_custom_emoji_id":{"type":"string","pattern":"^[0-9]+$","minLength":1,"maxLength":20}},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_thread_id":{"type":"integer"},` +
		`"name":{"type":"string"},"icon_color":{"type":"integer"},"icon_custom_emoji_id":{"type":"string"}},` +
		`"required":["message_thread_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "name", Description: "Topic name, from 1 through 128 characters", Required: true},
		{Name: "icon_color", Description: "Icon color as RGB integer; one of 7322096, 16766590, 13338331, " +
			"9367192, 16749490, 16478047"},
		{Name: "icon_custom_emoji_id", Description: "Custom emoji identifier as a string of digits, up to 20"},
	},
	Fields: []capability.Field{
		{Name: "message_thread_id", Description: "Thread identifier of the new topic"},
		{Name: "name", Description: "Name of the topic"},
		{Name: "icon_color", Description: "Icon color of the topic"},
		{Name: "icon_custom_emoji_id", Description: "Custom emoji identifier of the topic icon"},
	},
	Examples: []capability.Example{{
		Description: "Create a topic in this connection's chat",
		Arguments:   json.RawMessage(`{"name":"Releases","icon_color":7322096}`),
	}},
}

var topicsEdit = capability.Descriptor{
	ID:      Provider + ".topics.edit",
	Version: 1,
	Title:   "Edit a Telegram forum topic",
	Description: "Rename a forum topic or change its icon in a bound forum supergroup of an explicit Telegram " +
		"connection; general: true renames the General topic, which has no icon",
	Tags:     []string{"telegram", "topics", "edit"},
	Risk:     chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	Group:    groupTopics,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + topicSelectorSchema + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":128},` +
		`"icon_custom_emoji_id":{"type":"string","pattern":"^[0-9]*$","maxLength":20}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{chatArgument}, topicSelectorArguments...),
		capability.Argument{Name: "name", Description: "New name, from 1 through 128 characters; required for " +
			"the General topic"},
		capability.Argument{Name: "icon_custom_emoji_id", Description: "New custom emoji identifier as a string " +
			"of digits, up to 20; an empty string removes the icon; not for the General topic"}),
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the change"}},
	Examples: []capability.Example{{
		Description: "Rename a topic",
		Arguments:   json.RawMessage(`{"message_thread_id":12,"name":"Archive"}`),
	}},
}

var topicsClose = capability.Descriptor{
	ID:          Provider + ".topics.close",
	Version:     1,
	Title:       "Close a Telegram forum topic",
	Description: "Close a forum topic or, with general: true, the General topic of a bound forum supergroup of an explicit Telegram connection",
	Tags:        []string{"telegram", "topics", "close"},
	Risk:        chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupTopics,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + topicSelectorSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"closed":{"type":"boolean"}},` +
		`"required":["closed"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{chatArgument}, topicSelectorArguments...),
	Fields:    []capability.Field{{Name: "closed", Description: "True when Telegram accepted the change"}},
	Examples: []capability.Example{{
		Description: "Close a topic",
		Arguments:   json.RawMessage(`{"message_thread_id":12}`),
	}},
}

var topicsReopen = capability.Descriptor{
	ID:          Provider + ".topics.reopen",
	Version:     1,
	Title:       "Reopen a Telegram forum topic",
	Description: "Reopen a forum topic or, with general: true, the General topic of a bound forum supergroup of an explicit Telegram connection; reopening the General topic also unhides it",
	Tags:        []string{"telegram", "topics", "reopen"},
	Risk:        chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupTopics,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + topicSelectorSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"reopened":{"type":"boolean"}},` +
		`"required":["reopened"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{chatArgument}, topicSelectorArguments...),
	Fields:    []capability.Field{{Name: "reopened", Description: "True when Telegram accepted the change"}},
	Examples: []capability.Example{{
		Description: "Reopen the General topic",
		Arguments:   json.RawMessage(`{"general":true}`),
	}},
}

type topicSelector struct {
	Chat            string `json:"chat"`
	MessageThreadID int64  `json:"message_thread_id"`
	General         bool   `json:"general"`
}

// check requires exactly one of a positive message_thread_id and general: true.
func (s topicSelector) check(op string) error {
	if (s.MessageThreadID > 0) == s.General || s.MessageThreadID < 0 {
		return providerError(op, "exactly one of message_thread_id and general: true is required")
	}
	return nil
}

func checkEmojiID(op, id string, allowEmpty bool) error {
	if id == "" && allowEmpty {
		return nil
	}
	if len(id) == 0 || len(id) > maxEmojiIDLength || !emojiIDPattern.MatchString(id) {
		return providerError(op, "the icon_custom_emoji_id must be a string of 1 to 20 digits")
	}
	return nil
}

func invokeTopicsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat              string  `json:"chat"`
		Name              string  `json:"name"`
		IconColor         *int64  `json:"icon_color"`
		IconCustomEmojiID *string `json:"icon_custom_emoji_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("create forum topic", "the validated arguments could not be read")
	}
	options := TopicCreate{Name: arguments.Name, IconColor: arguments.IconColor, IconCustomEmojiID: arguments.IconCustomEmojiID}
	if err := options.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.CreateTopic(ctx, options)
}

func invokeTopicsEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		topicSelector
		Name              *string `json:"name"`
		IconCustomEmojiID *string `json:"icon_custom_emoji_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("edit forum topic", "the validated arguments could not be read")
	}
	options := TopicEdit{General: arguments.General, MessageThreadID: arguments.MessageThreadID,
		Name: arguments.Name, IconCustomEmojiID: arguments.IconCustomEmojiID}
	if err := options.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.EditTopic(ctx, options)
}

func invokeTopicsClose(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTopicState(ctx, resolved, secrets, red, raw, true)
}

func invokeTopicsReopen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTopicState(ctx, resolved, secrets, red, raw, false)
}

func invokeTopicState(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, closing bool) (any, error) {
	var arguments topicSelector
	op := topicStateOp(closing)
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := arguments.check(op); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	if closing {
		return client.CloseTopic(ctx, arguments.MessageThreadID, arguments.General)
	}
	return client.ReopenTopic(ctx, arguments.MessageThreadID, arguments.General)
}

func topicStateOp(closing bool) string {
	if closing {
		return "close forum topic"
	}
	return "reopen forum topic"
}

// TopicCreate holds the arguments of createForumTopic.
type TopicCreate struct {
	Name              string
	IconColor         *int64
	IconCustomEmojiID *string
}

func (o TopicCreate) validate() error {
	const op = "create forum topic"
	if err := checkRunes(op, "name", o.Name, 1, maxTitleRunes); err != nil {
		return err
	}
	if o.IconColor != nil {
		found := false
		for _, color := range topicIconColors {
			found = found || color == *o.IconColor
		}
		if !found {
			return providerError(op, "the icon_color is not one of the colors Telegram allows")
		}
	}
	if o.IconCustomEmojiID != nil {
		return checkEmojiID(op, *o.IconCustomEmojiID, false)
	}
	return nil
}

// TopicEdit holds the arguments of editForumTopic and editGeneralForumTopic.
type TopicEdit struct {
	MessageThreadID   int64
	General           bool
	Name              *string
	IconCustomEmojiID *string
}

func (o TopicEdit) validate() error {
	const op = "edit forum topic"
	if err := (topicSelector{MessageThreadID: o.MessageThreadID, General: o.General}).check(op); err != nil {
		return err
	}
	if o.Name != nil {
		if err := checkRunes(op, "name", *o.Name, 1, maxTitleRunes); err != nil {
			return err
		}
	}
	if o.General {
		// The General topic has no icon and its name is mandatory.
		if o.Name == nil || o.IconCustomEmojiID != nil {
			return providerError(op, "the General topic needs a name and has no icon")
		}
		return nil
	}
	if o.Name == nil && o.IconCustomEmojiID == nil {
		return providerError(op, "at least one of name and icon_custom_emoji_id is required")
	}
	if o.IconCustomEmojiID != nil {
		// An empty identifier removes the icon.
		return checkEmojiID(op, *o.IconCustomEmojiID, true)
	}
	return nil
}

// CreateTopic performs exactly one createForumTopic request without retrying an ambiguous result.
func (c *Client) CreateTopic(ctx context.Context, o TopicCreate) (map[string]any, error) {
	const op = "create forum topic"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "createForumTopic", limit: defaultResponseBytes}, struct {
		ChatID            string  `json:"chat_id"`
		Name              string  `json:"name"`
		IconColor         *int64  `json:"icon_color,omitempty"`
		IconCustomEmojiID *string `json:"icon_custom_emoji_id,omitempty"`
	}{c.target, o.Name, o.IconColor, o.IconCustomEmojiID})
	if err != nil {
		return nil, err
	}
	var topic struct {
		MessageThreadID   int64  `json:"message_thread_id"`
		Name              string `json:"name"`
		IconColor         int64  `json:"icon_color"`
		IconCustomEmojiID string `json:"icon_custom_emoji_id"`
	}
	if json.Unmarshal(raw, &topic) != nil || topic.MessageThreadID <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	out := map[string]any{"message_thread_id": topic.MessageThreadID}
	if topic.Name != "" {
		out["name"] = truncateRunes(topic.Name, maxTitleRunes)
	}
	if topic.IconColor != 0 {
		out["icon_color"] = topic.IconColor
	}
	if topic.IconCustomEmojiID != "" {
		out["icon_custom_emoji_id"] = truncateRunes(topic.IconCustomEmojiID, maxEmojiIDLength)
	}
	return out, nil
}

// EditTopic performs exactly one edit request; General selects editGeneralForumTopic.
func (c *Client) EditTopic(ctx context.Context, o TopicEdit) (map[string]any, error) {
	const op = "edit forum topic"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	var err error
	if o.General {
		err = c.confirmed(ctx, spec{op: op, method: "editGeneralForumTopic", limit: defaultResponseBytes}, struct {
			ChatID string `json:"chat_id"`
			Name   string `json:"name"`
		}{c.target, *o.Name})
	} else {
		err = c.confirmed(ctx, spec{op: op, method: "editForumTopic", limit: defaultResponseBytes}, struct {
			ChatID            string  `json:"chat_id"`
			MessageThreadID   int64   `json:"message_thread_id"`
			Name              *string `json:"name,omitempty"`
			IconCustomEmojiID *string `json:"icon_custom_emoji_id,omitempty"`
		}{c.target, o.MessageThreadID, o.Name, o.IconCustomEmojiID})
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// CloseTopic performs exactly one close request; general selects closeGeneralForumTopic.
func (c *Client) CloseTopic(ctx context.Context, threadID int64, general bool) (map[string]any, error) {
	method, general2 := "closeForumTopic", "closeGeneralForumTopic"
	if err := c.topicState(ctx, topicStateOp(true), method, general2, threadID, general); err != nil {
		return nil, err
	}
	return map[string]any{"closed": true}, nil
}

// ReopenTopic performs exactly one reopen request; general selects reopenGeneralForumTopic.
func (c *Client) ReopenTopic(ctx context.Context, threadID int64, general bool) (map[string]any, error) {
	method, general2 := "reopenForumTopic", "reopenGeneralForumTopic"
	if err := c.topicState(ctx, topicStateOp(false), method, general2, threadID, general); err != nil {
		return nil, err
	}
	return map[string]any{"reopened": true}, nil
}

func (c *Client) topicState(ctx context.Context, op, method, generalMethod string, threadID int64, general bool) error {
	if c.target == "" {
		return providerError(op, "no chat was selected")
	}
	if err := (topicSelector{MessageThreadID: threadID, General: general}).check(op); err != nil {
		return err
	}
	if general {
		return c.confirmed(ctx, spec{op: op, method: generalMethod, limit: defaultResponseBytes}, struct {
			ChatID string `json:"chat_id"`
		}{c.target})
	}
	return c.confirmed(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, struct {
		ChatID          string `json:"chat_id"`
		MessageThreadID int64  `json:"message_thread_id"`
	}{c.target, threadID})
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}
