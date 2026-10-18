package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupBusiness            = "business"
	businessDataSensitivity  = "telegram-business-content"
	businessConnectionSchema = `"business":{"type":"string","minLength":1,"maxLength":128}`
	businessArgument         = "Business connection ID as bound by a business/CONNECTION_ID target of this connection"
)

var businessGet = capability.Descriptor{
	ID:          Provider + ".business.get",
	Version:     1,
	Title:       "Read a Telegram business connection",
	Description: "Read the account, status, and bot rights of one bound business connection",
	Tags:        []string{"telegram", "business", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `},` +
		`"required":["business"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"user":{"type":"object","properties":{"id":{"type":"integer"},"is_bot":{"type":"boolean"},` +
		`"first_name":{"type":"string"},"username":{"type":"string"}}},` +
		`"business_chat_ref":{"type":"string"},"date":{"type":"integer"},"is_enabled":{"type":"boolean"},` +
		`"rights":{"type":"object","additionalProperties":{"type":"boolean"}}},` +
		`"required":["id","is_enabled"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "business", Description: businessArgument, Required: true}},
	Fields: []capability.Field{
		{Name: "id", Description: "Business connection ID"},
		{Name: "user", Description: "Account that owns the connection"},
		{Name: "business_chat_ref", Description: "Reference to the private chat with that account, for telegram.business.mark-read"},
		{Name: "date", Description: "Unix time the connection was established"},
		{Name: "is_enabled", Description: "Whether the connection is active"},
		{Name: "rights", Description: "Rights the account granted the bot, each as a boolean"},
	},
	Examples: []capability.Example{{
		Description: "Read a bound business connection",
		Arguments:   json.RawMessage(`{"business":"<connection id>"}`),
	}},
}

var businessMarkRead = capability.Descriptor{
	ID:          Provider + ".business.mark-read",
	Version:     1,
	Title:       "Mark a Telegram business message as read",
	Description: "Mark one incoming message of a chat of a bound business connection as read",
	Tags:        []string{"telegram", "business", "update"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` +
		`"chat":{"type":"string","minLength":1,"maxLength":2048},"message_id":{"type":"integer","minimum":1}},` +
		`"required":["business","chat","message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"read":{"type":"boolean"}},` +
		`"required":["read"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "business", Description: businessArgument, Required: true},
		{Name: "chat", Description: "business_chat_ref of the same business connection from telegram.updates.list or telegram.business.get", Required: true},
		{Name: "message_id", Description: "Positive identifier of the message in that chat", Required: true},
	},
	Fields: []capability.Field{{Name: "read", Description: "True when Telegram accepted the request"}},
	Examples: []capability.Example{{
		Description: "Mark a business message as read",
		Arguments:   json.RawMessage(`{"business":"<connection id>","chat":"<business_chat_ref>","message_id":91}`),
	}},
}

var businessDeleteMessages = capability.Descriptor{
	ID:          Provider + ".business.delete-messages",
	Version:     1,
	Title:       "Delete Telegram business messages",
	Description: "Delete from 1 through 100 messages on behalf of a bound business connection in one request",
	Tags:        []string{"telegram", "business", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` +
		`"message_ids":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1}}},` +
		`"required":["business","message_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "business", Description: businessArgument, Required: true},
		{Name: "message_ids", Description: "From 1 through 100 distinct positive message identifiers; the rights granted " +
			"to the bot decide which of them Telegram deletes", Required: true},
	},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the request; " +
		"missing or undeletable messages are skipped without notice"}},
	Examples: []capability.Example{{
		Description: "Delete several messages of a business connection",
		Arguments:   json.RawMessage(`{"business":"<connection id>","message_ids":[91,92]}`),
	}},
}

// businessRights are the BusinessBotRights flags; a flag Telegram adds later stays out of the output.
type businessRights struct {
	CanReply                   bool `json:"can_reply"`
	CanReadMessages            bool `json:"can_read_messages"`
	CanDeleteSentMessages      bool `json:"can_delete_sent_messages"`
	CanDeleteAllMessages       bool `json:"can_delete_all_messages"`
	CanEditName                bool `json:"can_edit_name"`
	CanEditBio                 bool `json:"can_edit_bio"`
	CanEditProfilePhoto        bool `json:"can_edit_profile_photo"`
	CanEditUsername            bool `json:"can_edit_username"`
	CanChangeGiftSettings      bool `json:"can_change_gift_settings"`
	CanViewGiftsAndStars       bool `json:"can_view_gifts_and_stars"`
	CanConvertGiftsToStars     bool `json:"can_convert_gifts_to_stars"`
	CanTransferAndUpgradeGifts bool `json:"can_transfer_and_upgrade_gifts"`
	CanTransferStars           bool `json:"can_transfer_stars"`
	CanManageStories           bool `json:"can_manage_stories"`
}

type tgBusinessConnection struct {
	ID         string          `json:"id"`
	User       *tgUser         `json:"user"`
	UserChatID int64           `json:"user_chat_id"`
	Date       int64           `json:"date"`
	Rights     *businessRights `json:"rights"`
	IsEnabled  bool            `json:"is_enabled"`
}

type businessOut struct {
	ID              string          `json:"id"`
	User            *senderOut      `json:"user,omitempty"`
	BusinessChatRef string          `json:"business_chat_ref,omitempty"`
	Date            int64           `json:"date,omitempty"`
	IsEnabled       bool            `json:"is_enabled"`
	Rights          *businessRights `json:"rights,omitempty"`
}

func businessBinding(id string) string { return businessPrefix + id }

// businessChatRef signs a chat of one business connection; only that connection's tools accept it.
func (c *Client) businessChatRef(connection string, chat int64) string {
	if chat == 0 {
		return ""
	}
	return signRef(c.token, refBusinessChat, businessBinding(connection), strconv.FormatInt(chat, 10))
}

// scopeBusiness is the shared pre-credential gate of every business tool.
func scopeBusiness(op string, resolved *config.Resolved, id string) error {
	if !validBusinessID(id) {
		return providerError(op, "business must be a valid business connection ID")
	}
	if resolved == nil {
		return providerError(op, "no connection was selected")
	}
	return requireBusiness(resolved, id)
}

func invokeBusinessGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessGetWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessGetWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get business connection"
	var arguments struct {
		Business string `json:"business"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.GetBusinessConnection(ctx, arguments.Business)
}

// GetBusinessConnection performs one getBusinessConnection request and returns the allow-listed fields.
func (c *Client) GetBusinessConnection(ctx context.Context, id string) (*businessOut, error) {
	got, err := c.fetchBusinessConnection(ctx, "get business connection", id)
	if err != nil {
		return nil, err
	}
	return &businessOut{ID: id, User: sender(got.User), BusinessChatRef: c.businessChatRef(id, got.UserChatID),
		Date: got.Date, IsEnabled: got.IsEnabled, Rights: got.Rights}, nil
}

// fetchBusinessConnection performs exactly one read of a bound business connection.
func (c *Client) fetchBusinessConnection(ctx context.Context, op, id string) (*tgBusinessConnection, error) {
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getBusinessConnection", limit: defaultResponseBytes, readOnly: true},
		struct {
			ID string `json:"business_connection_id"`
		}{id})
	if err != nil {
		return nil, err
	}
	var got tgBusinessConnection
	// An answer for another connection must never be shown as this one.
	if json.Unmarshal(raw, &got) != nil || got.ID != id {
		return nil, invalidResponse(op)
	}
	return &got, nil
}

func invokeBusinessMarkRead(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessMarkReadWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessMarkReadWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "mark business message read"
	var arguments struct {
		Business  string `json:"business"`
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.MessageID < 1 {
		return nil, providerError(op, "message_id must be positive")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	// Format, kind, and binding are checked before the credential is resolved.
	parsed, err := parseRef(resolved, refBusinessChat, arguments.Chat)
	if err != nil {
		return nil, err
	}
	if parsed.binding != businessBinding(arguments.Business) {
		return nil, providerError(op, "the reference does not belong to this business connection")
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.MarkBusinessRead(ctx, arguments.Business, parsed, arguments.MessageID)
}

// MarkBusinessRead verifies the reference's signature, then performs exactly one readBusinessMessage request.
func (c *Client) MarkBusinessRead(ctx context.Context, id string, parsed parsedRef, messageID int64) (map[string]any, error) {
	const op = "mark business message read"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if messageID < 1 {
		return nil, providerError(op, "message_id must be positive")
	}
	if parsed.kind != refBusinessChat || parsed.binding != businessBinding(id) {
		return nil, providerError(op, "the reference does not belong to this business connection")
	}
	value, err := c.checkRef(parsed)
	if err != nil {
		return nil, err
	}
	chat, err := strconv.ParseInt(value, 10, 64)
	if err != nil || chat == 0 {
		return nil, providerError(op, "the reference is malformed")
	}
	if err := requireNumericChat("readBusinessMessage", value); err != nil {
		return nil, err
	}
	err = c.confirmed(ctx, spec{op: op, method: "readBusinessMessage", limit: defaultResponseBytes}, struct {
		ID        string `json:"business_connection_id"`
		ChatID    int64  `json:"chat_id"`
		MessageID int64  `json:"message_id"`
	}{id, chat, messageID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"read": true}, nil
}

func invokeBusinessDeleteMessages(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessDeleteMessagesWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessDeleteMessagesWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "delete business messages"
	var arguments struct {
		Business   string  `json:"business"`
		MessageIDs []int64 `json:"message_ids"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkBusinessMessageIDs(op, arguments.MessageIDs); err != nil {
		return nil, err
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.DeleteBusinessMessages(ctx, arguments.Business, arguments.MessageIDs)
}

// checkBusinessMessageIDs refuses a repeated identifier instead of dropping it: the caller's list is sent as is.
func checkBusinessMessageIDs(op string, ids []int64) error {
	if len(ids) < 1 || len(ids) > maxDeleteMessages {
		return providerError(op, "from 1 through 100 message identifiers are required")
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return providerError(op, "the message identifiers must be positive")
		}
		if _, dup := seen[id]; dup {
			return providerError(op, "the message identifiers must be distinct")
		}
		seen[id] = struct{}{}
	}
	return nil
}

// DeleteBusinessMessages performs exactly one deleteBusinessMessages request without retrying an ambiguous
// result. Telegram skips identifiers it cannot find or delete, so success says nothing about single messages.
func (c *Client) DeleteBusinessMessages(ctx context.Context, id string, ids []int64) (map[string]any, error) {
	const op = "delete business messages"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if err := checkBusinessMessageIDs(op, ids); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "deleteBusinessMessages", limit: defaultResponseBytes}, struct {
		ID         string  `json:"business_connection_id"`
		MessageIDs []int64 `json:"message_ids"`
	}{id, ids})
	if err != nil {
		return nil, err
	}
	var deleted bool
	if json.Unmarshal(raw, &deleted) != nil || !deleted {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"deleted": true}, nil
}

// sendBusinessReply sends one message as a bound business connection. Scope, option, and reference format, kind,
// and binding are checked before the credential is resolved; the signature is checked before the request.
func sendBusinessReply(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	httpClient *http.Client, id, chat string, o SendOptions) (map[string]any, error) {
	const op = "send business message"
	if err := scopeBusiness(op, resolved, id); err != nil {
		return nil, err
	}
	// Topics and inline keyboards are not offered on behalf of a business account.
	if o.MessageThreadID != 0 || len(o.InlineKeyboard) > 0 {
		return nil, providerError(op, "message_thread_id and inline_keyboard are not available with business")
	}
	if chat == "" {
		return nil, providerError(op, "chat must be the business_chat_ref of this business connection")
	}
	parsed, err := parseRef(resolved, refBusinessChat, chat)
	if err != nil {
		return nil, err
	}
	if parsed.binding != businessBinding(id) {
		return nil, providerError(op, "the reference does not belong to this business connection")
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.SendBusiness(ctx, id, parsed, o)
}

// SendBusiness verifies the reference's signature, then performs exactly one sendMessage request on behalf of
// the business connection, without retrying an ambiguous result.
func (c *Client) SendBusiness(ctx context.Context, id string, parsed parsedRef, o SendOptions) (map[string]any, error) {
	const op = "send business message"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if parsed.kind != refBusinessChat || parsed.binding != businessBinding(id) {
		return nil, providerError(op, "the reference does not belong to this business connection")
	}
	if o.MessageThreadID != 0 || len(o.InlineKeyboard) > 0 {
		return nil, providerError(op, "message_thread_id and inline_keyboard are not available with business")
	}
	value, err := c.checkRef(parsed)
	if err != nil {
		return nil, err
	}
	chat, err := strconv.ParseInt(value, 10, 64)
	if err != nil || chat == 0 {
		return nil, providerError(op, "the reference is malformed")
	}
	return c.send(ctx, id, chat, o)
}
