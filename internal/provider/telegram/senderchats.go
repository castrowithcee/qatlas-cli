package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Chat identifiers of channels are negative, so these schemas only exclude zero by local check.
const senderChatIDSchema = `"sender_chat_id":{"type":"integer"}`
const actorChatIDSchema = `"actor_chat_id":{"type":"integer"}`

var senderChatIDArgument = capability.Argument{
	Name: "sender_chat_id", Description: "Telegram identifier of the channel to ban or unban as a sender", Required: true,
}

var actorChatIDArgument = capability.Argument{
	Name: "actor_chat_id", Description: "Identifier of the chat whose reactions are removed; exactly one of user_id " +
		"and actor_chat_id is required",
}

var optionalUserIDArgument = capability.Argument{
	Name: "user_id", Description: "Identifier of the user whose reactions are removed; exactly one of user_id " +
		"and actor_chat_id is required",
}

const reactionActorSchema = `"user_id":{"type":"integer","minimum":1},` + actorChatIDSchema

var senderchatsBan = capability.Descriptor{
	ID:      Provider + ".senderchats.ban",
	Version: 1,
	Title:   "Ban a Telegram sender chat",
	Description: "Ban a channel as a sender in a bound supergroup or channel of an explicit Telegram connection; " +
		"until it is unbanned the owner cannot write there on behalf of any of their channels",
	Tags:     []string{"telegram", "senderchats", "ban"},
	Risk:     memberRisk(capability.EffectDelete),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + senderChatIDSchema +
		`},"required":["sender_chat_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"banned":{"type":"boolean"}},` +
		`"required":["banned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, senderChatIDArgument},
	Fields:    []capability.Field{{Name: "banned", Description: "True when Telegram accepted the ban"}},
	Examples: []capability.Example{{
		Description: "Ban a channel as sender in this connection's chat",
		Arguments:   json.RawMessage(`{"sender_chat_id":-1002002}`),
	}},
}

var senderchatsUnban = capability.Descriptor{
	ID:          Provider + ".senderchats.unban",
	Version:     1,
	Title:       "Unban a Telegram sender chat",
	Description: "Lift the ban of a channel as a sender in a bound supergroup or channel of an explicit Telegram connection",
	Tags:        []string{"telegram", "senderchats", "unban"},
	Risk:        memberRisk(capability.EffectUpdate),
	Provider:    Provider,
	Group:       groupMembers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + senderChatIDSchema +
		`},"required":["sender_chat_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"unbanned":{"type":"boolean"}},` +
		`"required":["unbanned"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, senderChatIDArgument},
	Fields:    []capability.Field{{Name: "unbanned", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Unban a channel as sender in this connection's chat",
		Arguments:   json.RawMessage(`{"sender_chat_id":-1002002}`),
	}},
}

var reactionsRemove = capability.Descriptor{
	ID:      Provider + ".reactions.remove",
	Version: 1,
	Title:   "Remove a reaction from a Telegram message",
	Description: "Remove the reactions of one user or one chat from one message in a bound group or supergroup of an " +
		"explicit Telegram connection; the bot needs the right to delete messages",
	Tags:     []string{"telegram", "reactions", "remove"},
	Risk:     interactionRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1},` + reactionActorSchema +
		`},"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"}},` +
		`"required":["removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
		optionalUserIDArgument, actorChatIDArgument,
	},
	Fields: []capability.Field{{Name: "removed", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Remove the reaction of a user from a message in this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"user_id":42}`),
	}},
}

var reactionsRemoveAll = capability.Descriptor{
	ID:      Provider + ".reactions.removeall",
	Version: 1,
	Title:   "Remove all recent reactions of a Telegram user or chat",
	Description: "Remove up to 10000 recent reactions of one user or one chat across all messages of a bound group or " +
		"supergroup of an explicit Telegram connection; the bot needs the right to delete messages",
	Tags:     []string{"telegram", "reactions", "removeall"},
	Risk:     interactionRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + reactionActorSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"}},` +
		`"required":["removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, optionalUserIDArgument, actorChatIDArgument},
	Fields:    []capability.Field{{Name: "removed", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Remove all recent reactions of a user in this connection's chat",
		Arguments:   json.RawMessage(`{"user_id":42}`),
	}},
}

func invokeSenderChatsBan(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat         string `json:"chat"`
		SenderChatID int64  `json:"sender_chat_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("ban sender chat", "the validated arguments could not be read")
	}
	if arguments.SenderChatID == 0 {
		return nil, providerError("ban sender chat", "sender_chat_id is required")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.BanSenderChat(ctx, arguments.SenderChatID)
}

func invokeSenderChatsUnban(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat         string `json:"chat"`
		SenderChatID int64  `json:"sender_chat_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("unban sender chat", "the validated arguments could not be read")
	}
	if arguments.SenderChatID == 0 {
		return nil, providerError("unban sender chat", "sender_chat_id is required")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.UnbanSenderChat(ctx, arguments.SenderChatID)
}

func invokeReactionsRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat        string `json:"chat"`
		MessageID   int64  `json:"message_id"`
		UserID      int64  `json:"user_id"`
		ActorChatID int64  `json:"actor_chat_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("remove reaction", "the validated arguments could not be read")
	}
	if err := checkReactionActor("remove reaction", arguments.UserID, arguments.ActorChatID); err != nil {
		return nil, err
	}
	if arguments.MessageID < 1 {
		return nil, providerError("remove reaction", "the message identifier must be positive")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.RemoveReaction(ctx, arguments.MessageID, arguments.UserID, arguments.ActorChatID)
}

func invokeReactionsRemoveAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat        string `json:"chat"`
		UserID      int64  `json:"user_id"`
		ActorChatID int64  `json:"actor_chat_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("remove all reactions", "the validated arguments could not be read")
	}
	if err := checkReactionActor("remove all reactions", arguments.UserID, arguments.ActorChatID); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.RemoveAllReactions(ctx, arguments.UserID, arguments.ActorChatID)
}

// checkReactionActor requires exactly one actor: Telegram would otherwise act on an unspecified one or
// reject the call after the request.
func checkReactionActor(op string, userID, actorChatID int64) error {
	if userID < 0 {
		return providerError(op, "user_id must be a positive integer")
	}
	if (userID == 0) == (actorChatID == 0) {
		return providerError(op, "exactly one of user_id and actor_chat_id is required")
	}
	return nil
}

// BanSenderChat performs exactly one banChatSenderChat request without retrying an ambiguous result.
// sender_chat_id is only the object of the ban, never the target.
func (c *Client) BanSenderChat(ctx context.Context, senderChatID int64) (map[string]any, error) {
	const op = "ban sender chat"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if senderChatID == 0 {
		return nil, providerError(op, "sender_chat_id is required")
	}
	err := c.confirmed(ctx, spec{op: op, method: "banChatSenderChat", limit: defaultResponseBytes}, struct {
		ChatID       string `json:"chat_id"`
		SenderChatID int64  `json:"sender_chat_id"`
	}{c.target, senderChatID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"banned": true}, nil
}

// UnbanSenderChat performs exactly one unbanChatSenderChat request.
func (c *Client) UnbanSenderChat(ctx context.Context, senderChatID int64) (map[string]any, error) {
	const op = "unban sender chat"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if senderChatID == 0 {
		return nil, providerError(op, "sender_chat_id is required")
	}
	err := c.confirmed(ctx, spec{op: op, method: "unbanChatSenderChat", limit: defaultResponseBytes}, struct {
		ChatID       string `json:"chat_id"`
		SenderChatID int64  `json:"sender_chat_id"`
	}{c.target, senderChatID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"unbanned": true}, nil
}

// RemoveReaction performs exactly one deleteMessageReaction request for exactly one actor.
func (c *Client) RemoveReaction(ctx context.Context, messageID, userID, actorChatID int64) (map[string]any, error) {
	const op = "remove reaction"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if messageID < 1 {
		return nil, providerError(op, "the message identifier must be positive")
	}
	if err := checkReactionActor(op, userID, actorChatID); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "deleteMessageReaction", limit: defaultResponseBytes}, struct {
		ChatID      string `json:"chat_id"`
		MessageID   int64  `json:"message_id"`
		UserID      int64  `json:"user_id,omitempty"`
		ActorChatID int64  `json:"actor_chat_id,omitempty"`
	}{c.target, messageID, userID, actorChatID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"removed": true}, nil
}

// RemoveAllReactions performs exactly one deleteAllMessageReactions request for exactly one actor.
func (c *Client) RemoveAllReactions(ctx context.Context, userID, actorChatID int64) (map[string]any, error) {
	const op = "remove all reactions"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkReactionActor(op, userID, actorChatID); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "deleteAllMessageReactions", limit: defaultResponseBytes}, struct {
		ChatID      string `json:"chat_id"`
		UserID      int64  `json:"user_id,omitempty"`
		ActorChatID int64  `json:"actor_chat_id,omitempty"`
	}{c.target, userID, actorChatID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"removed": true}, nil
}
