package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxStickerSetNameLen is a conservative local bound: Telegram names are letters, digits, and underscores.
const maxStickerSetNameLen = 64

var chatsSetPermissions = capability.Descriptor{
	ID:      Provider + ".chats.setpermissions",
	Version: 1,
	Title:   "Set the default permissions of a Telegram chat",
	Description: "Set the complete default member permissions of a bound group or supergroup of an explicit " +
		"Telegram connection; every permission must be given explicitly, true allows and false forbids it",
	Tags:     []string{"telegram", "chats", "permissions"},
	Risk:     chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	Group:    groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		restrictPermissionsSchema() + `},"required":["permissions"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "permissions", Required: true,
			Description: "Object with every ChatPermissions boolean of the Bot API, none omitted"},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the permissions"}},
	Examples: []capability.Example{{
		Description: "Forbid everything by default in this connection's chat",
		Arguments:   json.RawMessage(`{"permissions":` + permissionsObject("false") + `}`),
	}},
}

var chatsSetStickerSet = capability.Descriptor{
	ID:      Provider + ".chats.setstickerset",
	Version: 1,
	Title:   "Set the sticker set of a Telegram group",
	Description: "Set the group sticker set of a bound supergroup of an explicit Telegram connection by its " +
		"name, not a URL. The name is checked locally as 1 through 64 letters, digits, or underscores",
	Tags:     []string{"telegram", "chats", "stickers"},
	Risk:     chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	Group:    groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"sticker_set_name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9_]+$"}},` +
		`"required":["sticker_set_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "sticker_set_name", Required: true,
			Description: "Name of the sticker set, 1 through 64 letters, digits, or underscores; no URL"},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the sticker set"}},
	Examples: []capability.Example{{
		Description: "Set the sticker set of this connection's group",
		Arguments:   json.RawMessage(`{"sticker_set_name":"team_stickers"}`),
	}},
}

var chatsDeleteStickerSet = capability.Descriptor{
	ID:          Provider + ".chats.deletestickerset",
	Version:     1,
	Title:       "Remove the sticker set of a Telegram group",
	Description: "Remove the group sticker set of a bound supergroup of an explicit Telegram connection",
	Tags:        []string{"telegram", "chats", "stickers", "delete"},
	Risk:        chatAdminRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:    Provider, RequiresToolAllowList: true,
	Group: groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the removal"}},
	Examples: []capability.Example{{
		Description: "Remove the sticker set of this connection's group",
		Arguments:   json.RawMessage(`{}`),
	}},
}

var chatsLeave = capability.Descriptor{
	ID:      Provider + ".chats.leave",
	Version: 1,
	Title:   "Leave a Telegram chat",
	Description: "Make the bot leave a bound chat of an explicit Telegram connection. The bot loses access to the " +
		"chat and only a human can add it again",
	Tags:     []string{"telegram", "chats", "leave"},
	Risk:     chatAdminRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"left":{"type":"boolean"}},` +
		`"required":["left"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument},
	Fields:    []capability.Field{{Name: "left", Description: "True when Telegram accepted the departure"}},
	Examples: []capability.Example{{
		Description: "Leave this connection's chat",
		Arguments:   json.RawMessage(`{}`),
	}},
}

func permissionsObject(value string) string {
	out := `{`
	for i, name := range restrictPermissionNames {
		if i > 0 {
			out += ","
		}
		out += `"` + name + `":` + value
	}
	return out + `}`
}

func invokeChatsSetPermissions(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set chat permissions"
	var arguments struct {
		Chat        string            `json:"chat"`
		Permissions *permissionsInput `json:"permissions"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	permissions, ok := arguments.Permissions.complete()
	if !ok {
		return nil, providerError(op, "permissions must set every permission explicitly")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetChatPermissions(ctx, permissions)
}

func invokeChatsSetStickerSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat           string `json:"chat"`
		StickerSetName string `json:"sticker_set_name"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("set chat sticker set", "the validated arguments could not be read")
	}
	if err := checkStickerSetName(arguments.StickerSetName); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetChatStickerSet(ctx, arguments.StickerSetName)
}

func invokeChatsDeleteStickerSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("delete chat sticker set", "the validated arguments could not be read")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.DeleteChatStickerSet(ctx)
}

func invokeChatsLeave(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("leave chat", "the validated arguments could not be read")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.LeaveChat(ctx)
}

// checkStickerSetName is narrower than Telegram: 1 through 64 ASCII letters, digits, or underscores.
func checkStickerSetName(name string) error {
	if name == "" || len(name) > maxStickerSetNameLen {
		return providerError("set chat sticker set", "the sticker_set_name length is outside the allowed range")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return providerError("set chat sticker set", "the sticker_set_name must be a name of letters, digits, and underscores")
		}
	}
	return nil
}

// SetChatPermissions performs exactly one setChatPermissions request. Independent permissions are fixed so
// Telegram does not derive permissions from one another.
func (c *Client) SetChatPermissions(ctx context.Context, permissions chatPermissions) (map[string]any, error) {
	const op = "set chat permissions"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatPermissions", limit: defaultResponseBytes}, struct {
		ChatID                        string          `json:"chat_id"`
		Permissions                   chatPermissions `json:"permissions"`
		UseIndependentChatPermissions bool            `json:"use_independent_chat_permissions"`
	}{c.target, permissions, true})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// SetChatStickerSet performs exactly one setChatStickerSet request.
func (c *Client) SetChatStickerSet(ctx context.Context, name string) (map[string]any, error) {
	const op = "set chat sticker set"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkStickerSetName(name); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatStickerSet", limit: defaultResponseBytes}, struct {
		ChatID         string `json:"chat_id"`
		StickerSetName string `json:"sticker_set_name"`
	}{c.target, name})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// DeleteChatStickerSet performs exactly one deleteChatStickerSet request.
func (c *Client) DeleteChatStickerSet(ctx context.Context) (map[string]any, error) {
	const op = "delete chat sticker set"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	err := c.confirmed(ctx, spec{op: op, method: "deleteChatStickerSet", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
	}{c.target})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// LeaveChat performs exactly one leaveChat request.
func (c *Client) LeaveChat(ctx context.Context) (map[string]any, error) {
	const op = "leave chat"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	err := c.confirmed(ctx, spec{op: op, method: "leaveChat", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
	}{c.target})
	if err != nil {
		return nil, err
	}
	return map[string]any{"left": true}, nil
}
