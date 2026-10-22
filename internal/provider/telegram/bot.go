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
		`"id":{"type":"integer"},"is_bot":{"type":"boolean"},"first_name":{"type":"string"},` +
		`"last_name":{"type":"string"},"username":{"type":"string"},"language_code":{"type":"string"},` +
		`"is_premium":{"type":"boolean"},"added_to_attachment_menu":{"type":"boolean"},` +
		`"can_join_groups":{"type":"boolean"},"can_read_all_group_messages":{"type":"boolean"},` +
		`"supports_guest_queries":{"type":"boolean"},"supports_inline_queries":{"type":"boolean"},` +
		`"can_connect_to_business":{"type":"boolean"},"has_main_web_app":{"type":"boolean"},` +
		`"has_topics_enabled":{"type":"boolean"},"allows_users_to_create_topics":{"type":"boolean"},` +
		`"can_manage_bots":{"type":"boolean"},"supports_join_request_queries":{"type":"boolean"}},` +
		`"required":["id"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "id", Description: "Telegram user identifier of the bot"},
		{Name: "is_bot", Description: "True for a bot"},
		{Name: "first_name", Description: "Display name of the bot"},
		{Name: "last_name", Description: "Last name of the bot"},
		{Name: "username", Description: "Username of the bot"},
		{Name: "language_code", Description: "IETF language tag of the bot, when Telegram sent one"},
		{Name: "is_premium", Description: "True when the bot is a Telegram Premium account"},
		{Name: "added_to_attachment_menu", Description: "True when the bot is in the attachment menu"},
		{Name: "can_join_groups", Description: "True when the bot can be added to groups"},
		{Name: "can_read_all_group_messages", Description: "True when privacy mode is off and the bot sees all group messages"},
		{Name: "supports_guest_queries", Description: "True when the bot supports guest queries"},
		{Name: "supports_inline_queries", Description: "True when inline queries are enabled"},
		{Name: "can_connect_to_business", Description: "True when the bot can be connected to a business account"},
		{Name: "has_main_web_app", Description: "True when the bot has a main Web App"},
		{Name: "has_topics_enabled", Description: "True when forum topic mode is enabled in private chats"},
		{Name: "allows_users_to_create_topics", Description: "True when users may create and delete topics in private chats"},
		{Name: "can_manage_bots", Description: "True when the bot can manage other bots"},
		{Name: "supports_join_request_queries", Description: "True when the bot supports join request queries"},
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
		`"has_webhook":{"type":"boolean"},"has_custom_certificate":{"type":"boolean"},` +
		`"pending_update_count":{"type":"integer"},"ip_address":{"type":"string"},` +
		`"last_error_date":{"type":"integer"},"last_error_message":{"type":"string"},` +
		`"last_synchronization_error_date":{"type":"integer"},"max_connections":{"type":"integer"},` +
		`"allowed_updates":{"type":"array","maxItems":50,"items":{"type":"string"}}},` +
		`"required":["has_webhook","pending_update_count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "has_webhook", Description: "True when a webhook is set; while it is, updates.list fails with a conflict"},
		{Name: "has_custom_certificate", Description: "True when a custom certificate was provided for the webhook"},
		{Name: "pending_update_count", Description: "Number of updates awaiting delivery"},
		{Name: "ip_address", Description: "Webhook IP address currently in use; absent when Telegram sent none"},
		{Name: "last_error_date", Description: "Unix time of the last webhook delivery error; absent when none occurred"},
		{Name: "last_error_message", Description: "Human-readable text of the last delivery error, from Telegram and untrusted; absent when none occurred"},
		{Name: "last_synchronization_error_date", Description: "Unix time of the last error while synchronizing updates with Telegram datacenters; absent when none occurred"},
		{Name: "max_connections", Description: "Maximum number of simultaneous HTTPS connections for update delivery"},
		{Name: "allowed_updates", Description: "Update types the bot is subscribed to, at most 50 entries of 64 characters each, further entries omitted; absent when Telegram sent none (all types except chat_member, message_reaction, and message_reaction_count)"},
	},
	Examples: []capability.Example{{Description: "Check whether a webhook is active", Arguments: json.RawMessage(`{}`)}},
}

// botOut is the printed User of the bot.
type botOut = userOut

type webhookOut struct {
	HasWebhook                   bool     `json:"has_webhook"`
	HasCustomCertificate         *bool    `json:"has_custom_certificate,omitempty"`
	PendingUpdateCount           int64    `json:"pending_update_count"`
	IPAddress                    string   `json:"ip_address,omitempty"`
	LastErrorDate                int64    `json:"last_error_date,omitempty"`
	LastErrorMessage             string   `json:"last_error_message,omitempty"`
	LastSynchronizationErrorDate int64    `json:"last_synchronization_error_date,omitempty"`
	MaxConnections               int64    `json:"max_connections,omitempty"`
	AllowedUpdates               []string `json:"allowed_updates,omitempty"`
}

const (
	maxAllowedUpdates     = 50
	maxAllowedUpdateRunes = 64
)

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

// GetBot reads the bot identity with getMe through the shared User mapper. The three flags the output has
// always carried stay present as false when Telegram omits them.
func (c *Client) GetBot(ctx context.Context) (botOut, error) {
	const op = "get bot"
	raw, err := c.call(ctx, spec{op: op, method: "getMe", limit: defaultResponseBytes, readOnly: true}, struct{}{})
	if err != nil {
		return botOut{}, err
	}
	var bot userOut
	if json.Unmarshal(raw, &bot) != nil || bot.ID == 0 {
		return botOut{}, invalidResponse(op)
	}
	bot = cleanUser(bot)
	for _, flag := range []**bool{&bot.CanJoinGroups, &bot.CanReadAllGroupMessages, &bot.SupportsInlineQueries} {
		if *flag == nil {
			*flag = new(bool)
		}
	}
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
		URL                          string   `json:"url"`
		HasCustomCertificate         *bool    `json:"has_custom_certificate"`
		PendingUpdateCount           int64    `json:"pending_update_count"`
		IPAddress                    string   `json:"ip_address"`
		LastErrorDate                int64    `json:"last_error_date"`
		LastErrorMessage             string   `json:"last_error_message"`
		LastSynchronizationErrorDate int64    `json:"last_synchronization_error_date"`
		MaxConnections               int64    `json:"max_connections"`
		AllowedUpdates               []string `json:"allowed_updates"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return webhookOut{}, invalidResponse(op)
	}
	out := webhookOut{HasWebhook: info.URL != "", HasCustomCertificate: info.HasCustomCertificate,
		PendingUpdateCount: info.PendingUpdateCount, IPAddress: cleanText(info.IPAddress, maxNameRunes),
		LastErrorDate: info.LastErrorDate, LastErrorMessage: cleanText(info.LastErrorMessage, maxNameRunes),
		LastSynchronizationErrorDate: info.LastSynchronizationErrorDate, MaxConnections: info.MaxConnections}
	for _, kind := range info.AllowedUpdates {
		if len(out.AllowedUpdates) == maxAllowedUpdates {
			break
		}
		if kind != "" {
			out.AllowedUpdates = append(out.AllowedUpdates, cleanText(kind, maxAllowedUpdateRunes))
		}
	}
	return out, nil
}
