package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const groupPins = "pins"

const chatSchema = `"chat":{"type":"string","minLength":1,"maxLength":128}`

var chatArgument = capability.Argument{
	Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat",
}

func pinRisk() capability.Risk {
	return capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var pinsPin = capability.Descriptor{
	ID:          Provider + ".pins.pin",
	Version:     1,
	Title:       "Pin a Telegram message",
	Description: "Pin one message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "pins", "pin"},
	Risk:        pinRisk(),
	Provider:    Provider,
	Group:       groupPins,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1},"disable_notification":{"type":"boolean"}},` +
		`"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"pinned":{"type":"boolean"}},` +
		`"required":["pinned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
		{Name: "disable_notification", Description: "Pin silently, without notifying chat members"},
	},
	Fields: []capability.Field{{Name: "pinned", Description: "True when Telegram accepted the pin"}},
	Examples: []capability.Example{{
		Description: "Pin a message in this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var pinsUnpin = capability.Descriptor{
	ID:          Provider + ".pins.unpin",
	Version:     1,
	Title:       "Unpin a Telegram message",
	Description: "Unpin one message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "pins", "unpin"},
	Risk:        pinRisk(),
	Provider:    Provider,
	Group:       groupPins,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"unpinned":{"type":"boolean"}},` +
		`"required":["unpinned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "message_id", Description: "Identifier of the message to unpin; the most recently pinned message when omitted"},
	},
	Fields: []capability.Field{{Name: "unpinned", Description: "True when Telegram accepted the unpin"}},
	Examples: []capability.Example{{
		Description: "Unpin a specific message in this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var pinsUnpinAll = capability.Descriptor{
	ID:          Provider + ".pins.unpinall",
	Version:     1,
	Title:       "Unpin all Telegram messages",
	Description: "Unpin every pinned message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "pins", "unpin"},
	Risk:        pinRisk(),
	Provider:    Provider, RequiresToolAllowList: true,
	Group: groupPins,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"unpinned":{"type":"boolean"}},` +
		`"required":["unpinned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument},
	Fields:    []capability.Field{{Name: "unpinned", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Unpin all messages in this connection's chat",
		Arguments:   json.RawMessage(`{}`),
	}},
}

func invokePinsPin(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat                string `json:"chat"`
		MessageID           int64  `json:"message_id"`
		DisableNotification bool   `json:"disable_notification"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("pin message", "the validated arguments could not be read")
	}
	if arguments.MessageID < 1 {
		return nil, providerError("pin message", "the message identifier must be positive")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.PinMessage(ctx, arguments.MessageID, arguments.DisableNotification)
}

func invokePinsUnpin(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("unpin message", "the validated arguments could not be read")
	}
	if arguments.MessageID < 0 {
		return nil, providerError("unpin message", "the message identifier must be positive")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.UnpinMessage(ctx, arguments.MessageID)
}

func invokePinsUnpinAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("unpin all messages", "the validated arguments could not be read")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.UnpinAllMessages(ctx)
}

// PinMessage performs exactly one pinChatMessage request without retrying an ambiguous result.
func (c *Client) PinMessage(ctx context.Context, messageID int64, silent bool) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("pin message", "no chat was selected")
	}
	if messageID < 1 {
		return nil, providerError("pin message", "the message identifier must be positive")
	}
	err := c.confirmed(ctx, spec{op: "pin message", method: "pinChatMessage", limit: defaultResponseBytes}, struct {
		ChatID              string `json:"chat_id"`
		MessageID           int64  `json:"message_id"`
		DisableNotification bool   `json:"disable_notification,omitempty"`
	}{ChatID: c.target, MessageID: messageID, DisableNotification: silent})
	if err != nil {
		return nil, err
	}
	return map[string]any{"pinned": true}, nil
}

// UnpinMessage performs exactly one unpinChatMessage request. Without a message identifier Telegram
// unpins the most recently pinned message.
func (c *Client) UnpinMessage(ctx context.Context, messageID int64) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("unpin message", "no chat was selected")
	}
	if messageID < 0 {
		return nil, providerError("unpin message", "the message identifier must be positive")
	}
	err := c.confirmed(ctx, spec{op: "unpin message", method: "unpinChatMessage", limit: defaultResponseBytes}, struct {
		ChatID    string `json:"chat_id"`
		MessageID int64  `json:"message_id,omitempty"`
	}{ChatID: c.target, MessageID: messageID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"unpinned": true}, nil
}

// UnpinAllMessages performs exactly one unpinAllChatMessages request.
func (c *Client) UnpinAllMessages(ctx context.Context) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("unpin all messages", "no chat was selected")
	}
	err := c.confirmed(ctx, spec{op: "unpin all messages", method: "unpinAllChatMessages", limit: defaultResponseBytes},
		struct {
			ChatID string `json:"chat_id"`
		}{ChatID: c.target})
	if err != nil {
		return nil, err
	}
	return map[string]any{"unpinned": true}, nil
}

// confirmed sends one request and accepts only Telegram's plain true result.
func (c *Client) confirmed(ctx context.Context, s spec, payload any) error {
	raw, err := c.call(ctx, s, payload)
	if err != nil {
		return err
	}
	var ok bool
	if json.Unmarshal(raw, &ok) != nil || !ok {
		return withUncertainty(spec{}, invalidResponse(s.op))
	}
	return nil
}
