package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxDeleteMessages = 100

var messagesDeleteMany = capability.Descriptor{
	ID:          Provider + ".messages.deletemany",
	Version:     1,
	Title:       "Delete several Telegram messages",
	Description: "Delete from 1 through 100 messages of a bound chat of an explicit Telegram connection in one request",
	Tags:        []string{"telegram", "messages", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"chat":{"type":"string","minLength":1,"maxLength":128},` +
		`"message_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"integer","minimum":1}}},` +
		`"required":["message_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat"},
		{Name: "message_ids", Description: "From 1 through 100 positive message identifiers of the chat; repeated " +
			"identifiers count once", Required: true},
	},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the request; " +
		"missing or undeletable messages are skipped without notice"}},
	Examples: []capability.Example{{
		Description: "Delete several messages from this connection's chat",
		Arguments:   json.RawMessage(`{"message_ids":[91,92,93]}`),
	}},
}

func invokeMessagesDeleteMany(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat       string  `json:"chat"`
		MessageIDs []int64 `json:"message_ids"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("delete messages", "the validated arguments could not be read")
	}
	ids, err := normalizeMessageIDs(arguments.MessageIDs)
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessages(ctx, ids)
}

// normalizeMessageIDs drops repeated identifiers, keeping the first occurrence, and then enforces the
// Bot API bound of 1 through 100 identifiers.
func normalizeMessageIDs(ids []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, providerError("delete messages", "the message identifiers must be positive")
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) < 1 || len(unique) > maxDeleteMessages {
		return nil, providerError("delete messages", "from 1 through 100 distinct message identifiers are required")
	}
	return unique, nil
}

// DeleteMessages performs exactly one deleteMessages request without retrying an ambiguous result.
// Telegram skips identifiers it cannot find or delete, so success says nothing about single messages.
func (c *Client) DeleteMessages(ctx context.Context, ids []int64) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("delete messages", "no chat was selected")
	}
	ids, err := normalizeMessageIDs(ids)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: "delete messages", method: "deleteMessages", limit: defaultResponseBytes}, struct {
		ChatID     string  `json:"chat_id"`
		MessageIDs []int64 `json:"message_ids"`
	}{ChatID: c.target, MessageIDs: ids})
	if err != nil {
		return nil, err
	}
	var deleted bool
	if json.Unmarshal(raw, &deleted) != nil || !deleted {
		return nil, withUncertainty(spec{}, invalidResponse("delete messages"))
	}
	return map[string]any{"deleted": true}, nil
}
