package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const groupBot = "bot"

const noArguments = `{"type":"object","properties":{},"additionalProperties":false}`

var botGet = capability.Descriptor{
	ID:          Provider + ".bot.get",
	Version:     1,
	Title:       "Get the Telegram bot identity",
	Description: "Read the identity of the bot behind this connection",
	Tags:        []string{"telegram", "bot", "read"},
	Risk: capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider:    Provider,
	Group:       groupBot,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"integer"},"username":{"type":"string"},"first_name":{"type":"string"},` +
		`"can_join_groups":{"type":"boolean"},"can_read_all_group_messages":{"type":"boolean"},` +
		`"supports_inline_queries":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "id", Description: "Telegram user identifier of the bot"},
		{Name: "username", Description: "Username of the bot"},
		{Name: "first_name", Description: "Display name of the bot"},
		{Name: "can_join_groups", Description: "True when the bot can be added to groups"},
		{Name: "can_read_all_group_messages", Description: "True when privacy mode is off and the bot sees all group messages"},
		{Name: "supports_inline_queries", Description: "True when inline queries are enabled"},
	},
	Examples: []capability.Example{{Description: "Read the bot identity", Arguments: json.RawMessage(`{}`)}},
}

var webhookGet = capability.Descriptor{
	ID:          Provider + ".webhook.get",
	Version:     1,
	Title:       "Get the Telegram webhook status",
	Description: "Read whether a webhook is active for the bot and how many updates wait; the webhook URL is never shown",
	Tags:        []string{"telegram", "webhook", "read"},
	Risk: capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider:    Provider,
	Group:       groupBot,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"has_webhook":{"type":"boolean"},"pending_update_count":{"type":"integer"},` +
		`"last_error_date":{"type":"integer"}},"required":["has_webhook","pending_update_count"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "has_webhook", Description: "True when a webhook is set; while it is, updates.list fails with a conflict"},
		{Name: "pending_update_count", Description: "Number of updates awaiting delivery"},
		{Name: "last_error_date", Description: "Unix time of the last webhook delivery error; absent when none occurred"},
	},
	Examples: []capability.Example{{Description: "Check whether a webhook is active", Arguments: json.RawMessage(`{}`)}},
}

type botOut struct {
	ID                      int64  `json:"id"`
	Username                string `json:"username,omitempty"`
	FirstName               string `json:"first_name,omitempty"`
	CanJoinGroups           bool   `json:"can_join_groups"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
	SupportsInlineQueries   bool   `json:"supports_inline_queries"`
}

type webhookOut struct {
	HasWebhook         bool  `json:"has_webhook"`
	PendingUpdateCount int64 `json:"pending_update_count"`
	LastErrorDate      int64 `json:"last_error_date,omitempty"`
}

func invokeBotGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if resolved == nil {
		return nil, providerError("get bot", "no connection was selected")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetBot(ctx)
}

func invokeWebhookGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if resolved == nil {
		return nil, providerError("get webhook", "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetWebhook(ctx)
}

// GetBot reads the bot identity with getMe; only the fields of botOut are decoded.
func (c *Client) GetBot(ctx context.Context) (botOut, error) {
	const op = "get bot"
	raw, err := c.call(ctx, spec{op: op, method: "getMe", limit: defaultResponseBytes, readOnly: true}, struct{}{})
	if err != nil {
		return botOut{}, err
	}
	var bot botOut
	if json.Unmarshal(raw, &bot) != nil || bot.ID == 0 {
		return botOut{}, invalidResponse(op)
	}
	bot.Username, bot.FirstName = capText(bot.Username, maxNameRunes), capText(bot.FirstName, maxNameRunes)
	return bot, nil
}

// GetWebhook reads getWebhookInfo. The URL is decoded only to derive has_webhook and is never returned.
func (c *Client) GetWebhook(ctx context.Context) (webhookOut, error) {
	const op = "get webhook"
	raw, err := c.call(ctx, spec{op: op, method: "getWebhookInfo", limit: defaultResponseBytes, readOnly: true}, struct{}{})
	if err != nil {
		return webhookOut{}, err
	}
	var info struct {
		URL                string `json:"url"`
		PendingUpdateCount int64  `json:"pending_update_count"`
		LastErrorDate      int64  `json:"last_error_date"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return webhookOut{}, invalidResponse(op)
	}
	return webhookOut{HasWebhook: info.URL != "", PendingUpdateCount: info.PendingUpdateCount,
		LastErrorDate: info.LastErrorDate}, nil
}
