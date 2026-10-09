// Package telegram implements the deliberately small Telegram Bot API surface used by Qatlas: a safe
// getMe connection check, confirmed send, edit, and delete operations, and non-consuming reads of incoming
// updates.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	Provider         = "telegram"
	roleBotToken     = "bot-token"
	defaultURL       = "https://api.telegram.org"
	defaultTimeout   = 30 * time.Second
	maxMessageLength = 4096
	dataSensitivity  = "telegram-message-content"
)

var messagesSend = capability.Descriptor{
	ID:          Provider + ".messages.send",
	Version:     1,
	Title:       "Send a Telegram message",
	Description: "Send one message, optionally formatted, as a reply, in a topic, or with an inline keyboard, to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "send"},
	Risk: capability.Risk{
		Effect:          capability.EffectCreate,
		Idempotency:     capability.IdempotencyNonIdempotent,
		Confirmation:    capability.ConfirmationRequired,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(
		`{"type":"object","properties":{"chat":{"type":"string","minLength":1,"maxLength":128},"text":{"type":"string","minLength":1,"maxLength":4096},` +
			`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]},` +
			`"reply_to_message_id":{"type":"integer","minimum":1},"message_thread_id":{"type":"integer","minimum":1},` +
			`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"},` +
			`"disable_link_preview":{"type":"boolean"},"inline_keyboard":` + keyboardSchema +
			`},"required":["text"],"additionalProperties":false}`,
	),
	OutputSchema: json.RawMessage(
		`{"type":"object","properties":{"message_id":{"type":"integer"},"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`,
	),
	Arguments: []capability.Argument{
		{Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat"},
		{Name: "text", Description: "Message text, from 1 through 4096 characters", Required: true},
		parseModeArgument,
		{Name: "reply_to_message_id", Description: "Message of the same chat to reply to"},
		{Name: "message_thread_id", Description: "Forum topic to send into"},
		{Name: "disable_notification", Description: "Send silently"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the message"},
		{Name: "disable_link_preview", Description: "Do not show a link preview"},
		{Name: "inline_keyboard", Description: keyboardArgument},
	},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Telegram message identifier"},
		{Name: "date", Description: "Telegram send time as Unix time"},
	},
	Examples: []capability.Example{{
		Description: "Send one plain-text notification to the connection's chat",
		Arguments:   json.RawMessage(`{"text":"Deployment finished"}`),
	}},
}

var messagesEdit = capability.Descriptor{
	ID:          Provider + ".messages.edit",
	Version:     1,
	Title:       "Edit a Telegram message",
	Description: "Replace the plain text of one message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "edit"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"chat":{"type":"string","minLength":1,"maxLength":128},` +
		`"message_id":{"type":"integer","minimum":1},` +
		`"text":{"type":"string","minLength":1,"maxLength":4096},` +
		`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]},"disable_link_preview":{"type":"boolean"},` +
		`"inline_keyboard":` + keyboardSchema + `},` +
		`"required":["message_id","text"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"}},` +
		`"required":["message_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat"},
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
		{Name: "text", Description: "Replacement text, from 1 through 4096 characters", Required: true},
		parseModeArgument,
		{Name: "disable_link_preview", Description: "Do not show a link preview"},
		{Name: "inline_keyboard", Description: keyboardArgument + "; omitted removes an existing keyboard"},
	},
	Fields: []capability.Field{{Name: "message_id", Description: "Edited Telegram message identifier"}},
	Examples: []capability.Example{{
		Description: "Replace the text of a message sent to this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"text":"Deployment completed"}`),
	}},
}

var messagesEditReplyMarkup = capability.Descriptor{
	ID:          Provider + ".messages.editreplymarkup",
	Version:     1,
	Title:       "Edit the inline keyboard of a Telegram message",
	Description: "Set or remove the inline keyboard of one message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "edit", "keyboard"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"chat":{"type":"string","minLength":1,"maxLength":128},` +
		`"message_id":{"type":"integer","minimum":1},"inline_keyboard":` + keyboardSchema + `},` +
		`"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"updated":{"type":"boolean"}},"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat"},
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
		{Name: "inline_keyboard", Description: keyboardArgument + "; omitted or empty removes the keyboard"},
	},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Edited Telegram message identifier"},
		{Name: "updated", Description: "True when Telegram confirmed the change without returning the message"},
	},
	Examples: []capability.Example{{
		Description: "Remove the inline keyboard of a message",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var messagesDelete = capability.Descriptor{
	ID:          Provider + ".messages.delete",
	Version:     1,
	Title:       "Delete a Telegram message",
	Description: "Delete one message from a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"chat":{"type":"string","minLength":1,"maxLength":128},"message_id":{"type":"integer","minimum":1}},` +
		`"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "chat", Description: "Bound chat to address; optional when the connection binds exactly one chat"},
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
	},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the deletion"}},
	Examples: []capability.Example{{
		Description: "Delete a message from this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var toolGroups = []config.ToolGroup{
	{ID: groupMessages, Title: "Messages", Description: "Send, forward, copy, edit, and delete messages in the bound chats"},
	{ID: groupPins, Title: "Pins", Description: "Pin and unpin messages in the bound chats"},
	{ID: groupUpdates, Title: "Updates", Description: "Read incoming messages and events of the bound chats"},
	{ID: groupChats, Title: "Chats", Description: "Read master data, administrators, and members of the bound chats; change their title, description, and photo"},
	{ID: groupInviteLinks, Title: "Invite links", Description: "Read the primary invite link and revoke invite links of the bound chats"},
	{ID: groupBot, Title: "Bot", Description: "Read the identity and webhook status of the bot"},
	{ID: groupFiles, Title: "Files", Description: "Read and download files of messages in the bound chats"},
	{ID: groupMedia, Title: "Media", Description: "Send photos, documents, and albums to the bound chats"},
	{ID: groupMembers, Title: "Members", Description: "Ban, unban, and restrict members and sender chats of the bound chats"},
	{ID: groupInteractions, Title: "Interactions",
		Description: "Send and stop polls, set and remove reactions, show chat actions, and send locations, " +
			"venues, contacts, and dice in the bound chats"},
}

const groupMessages = "messages"

// Register adds Telegram metadata, its read-only connection test, and the message operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Telegram", DefaultBaseURL: defaultURL,
		ValidateBaseURL: func(raw string) error {
			_, err := parseBase(raw)
			return err
		},
		// Only groups with a registered tool are declared: the registry rejects an empty group.
		Groups:             toolGroups,
		Description:        "Cloud-based instant messaging service",
		DefaultPermissions: []config.Permission{config.PermissionCreate},
		SecretRoles: []config.SecretRole{{
			Name:        roleBotToken,
			Description: "Telegram bot token issued by BotFather",
		}},
		Target: config.TargetMetadata{
			Label: "chat ID", Required: true, Multiple: true, Kinds: targetKinds,
			Description: "one or more chat IDs or @channel usernames, plus optionally bot and business/CONNECTION_ID; " +
				"chat tools address only the bound chats",
			Validate: validateConfiguredTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "send", Title: "Send messages", Recommended: true,
			Description: "sends new messages to the bound chats; earlier messages stay as they are",
			// The read tools expose the content of incoming messages, so the recommended start is the
			// narrowest change there is rather than a read.
			MutationReason: "the read tools expose incoming message content of the bound chats, so the " +
				"recommended start is a send that reaches only a bound chat, after confirmation in its own " +
				"request; nothing already in the chat can be edited or deleted",
			Tools: []string{messagesSend.ID},
		}, {
			ID: "read", Title: "Read updates, chats, and bot identity",
			Description: "reads pending messages and events, chat information, administrators, and members of " +
				"the bound chats and the bot identity; nothing is sent or acknowledged",
			Tools: []string{updatesList.ID, botGet.ID, chatsGet.ID, chatsAdministrators.ID, chatsMemberCount.ID,
				chatsMember.ID},
		}, {
			ID: "messaging", Title: "Send, edit, and delete messages",
			Description: "also edits messages and deletes messages in the bound chats: as admin also those of " +
				"others, in private chats also incoming ones",
			Tools: []string{messagesSend.ID, messagesEdit.ID, messagesDelete.ID},
		}, {
			ID: "chat-admin", Title: "Change chat title, description, and photo",
			Description: "changes the title, the description, and the photo of the bound chats; the photo can " +
				"only be replaced, not removed",
			Tools: []string{chatsSetTitle.ID, chatsSetDescription.ID, chatsSetPhoto.ID},
		}, {
			ID: "pins", Title: "Pin and unpin messages",
			Description: "pins and unpins single messages in the bound chats; nothing is sent, edited, or deleted",
			Tools:       []string{pinsPin.ID, pinsUnpin.ID},
		}, {
			ID: "media", Title: "Send photos, documents, and albums",
			Description: "sends photos, documents, and albums from released local files or file references to the " +
				"bound chats; earlier messages stay as they are",
			Tools: []string{photosSend.ID, documentsSend.ID, mediaGroupsSend.ID},
		}, {
			ID: "moderation", Title: "Unban members",
			Description: "lifts bans in the bound chats; a user who is not banned is left untouched",
			Tools:       []string{membersUnban.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: messagesSend, Handler: capability.Handler(invokeMessagesSend)},
		capability.Operation{Descriptor: messagesEdit, Handler: capability.Handler(invokeMessagesEdit)},
		capability.Operation{Descriptor: messagesEditReplyMarkup, Handler: capability.Handler(invokeMessagesEditReplyMarkup)},
		capability.Operation{Descriptor: messagesDelete, Handler: capability.Handler(invokeMessagesDelete)},
		capability.Operation{Descriptor: messagesDeleteMany, Handler: capability.Handler(invokeMessagesDeleteMany)},
		capability.Operation{Descriptor: messagesForward, Handler: capability.Handler(invokeMessagesForward)},
		capability.Operation{Descriptor: messagesCopy, Handler: capability.Handler(invokeMessagesCopy)},
		capability.Operation{Descriptor: pinsPin, Handler: capability.Handler(invokePinsPin)},
		capability.Operation{Descriptor: pinsUnpin, Handler: capability.Handler(invokePinsUnpin)},
		capability.Operation{Descriptor: pinsUnpinAll, Handler: capability.Handler(invokePinsUnpinAll)},
		capability.Operation{Descriptor: membersBan, Handler: capability.Handler(invokeMembersBan)},
		capability.Operation{Descriptor: membersUnban, Handler: capability.Handler(invokeMembersUnban)},
		capability.Operation{Descriptor: membersRestrict, Handler: capability.Handler(invokeMembersRestrict)},

		capability.Operation{Descriptor: pollsSend, Handler: capability.Handler(invokePollsSend)},
		capability.Operation{Descriptor: pollsStop, Handler: capability.Handler(invokePollsStop)},
		capability.Operation{Descriptor: reactionsSet, Handler: capability.Handler(invokeReactionsSet)},
		capability.Operation{Descriptor: reactionsRemove, Handler: capability.Handler(invokeReactionsRemove)},
		capability.Operation{Descriptor: reactionsRemoveAll, Handler: capability.Handler(invokeReactionsRemoveAll)},
		capability.Operation{Descriptor: senderchatsBan, Handler: capability.Handler(invokeSenderChatsBan)},
		capability.Operation{Descriptor: senderchatsUnban, Handler: capability.Handler(invokeSenderChatsUnban)},
		capability.Operation{Descriptor: chatActionsSend, Handler: capability.Handler(invokeChatActionsSend)},
		capability.Operation{Descriptor: locationsSend, Handler: capability.Handler(invokeLocationsSend)},
		capability.Operation{Descriptor: venuesSend, Handler: capability.Handler(invokeVenuesSend)},
		capability.Operation{Descriptor: contactsSend, Handler: capability.Handler(invokeContactsSend)},
		capability.Operation{Descriptor: diceSend, Handler: capability.Handler(invokeDiceSend)},
		capability.Operation{Descriptor: updatesList, Handler: capability.Handler(invokeUpdatesList)},
		capability.Operation{Descriptor: botGet, Handler: capability.Handler(invokeBotGet)},
		capability.Operation{Descriptor: webhookGet, Handler: capability.Handler(invokeWebhookGet)},
		capability.Operation{Descriptor: chatsGet, Handler: capability.Handler(invokeChatsGet)},
		capability.Operation{Descriptor: chatsAdministrators, Handler: capability.Handler(invokeChatsAdministrators)},
		capability.Operation{Descriptor: chatsMemberCount, Handler: capability.Handler(invokeChatsMemberCount)},
		capability.Operation{Descriptor: chatsMember, Handler: capability.Handler(invokeChatsMember)},
		capability.Operation{Descriptor: chatsSetTitle, Handler: capability.Handler(invokeChatsSetTitle)},
		capability.Operation{Descriptor: chatsSetDescription, Handler: capability.Handler(invokeChatsSetDescription)},
		capability.Operation{Descriptor: chatsSetPhoto, Handler: capability.Handler(invokeChatsSetPhoto)},
		capability.Operation{Descriptor: chatsDeletePhoto, Handler: capability.Handler(invokeChatsDeletePhoto)},
		capability.Operation{Descriptor: invitelinksPrimary, Handler: capability.Handler(invokeInviteLinksPrimary)},
		capability.Operation{Descriptor: invitelinksRevoke, Handler: capability.Handler(invokeInviteLinksRevoke)},
		capability.Operation{Descriptor: filesGet, Handler: capability.Handler(invokeFilesGet)},
		capability.Operation{Descriptor: filesDownload, Handler: capability.Handler(invokeFilesDownload)},
		capability.Operation{Descriptor: photosSend, Handler: capability.Handler(invokePhotosSend)},
		capability.Operation{Descriptor: documentsSend, Handler: capability.Handler(invokeDocumentsSend)},
		capability.Operation{Descriptor: mediaGroupsSend, Handler: capability.Handler(invokeMediaGroupsSend)},
	)
}

func invokeMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		SendOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, &provider.Error{
			Class: provider.ClassProviderError, Op: "send message", Message: "the validated arguments could not be read",
		}
	}
	if err := arguments.SendOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.Send(ctx, arguments.SendOptions)
}

func invokeMessagesEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		EditOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("edit message", "the validated arguments could not be read")
	}
	if err := arguments.EditOptions.validate(arguments.MessageID); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.Edit(ctx, arguments.MessageID, arguments.EditOptions)
}

func invokeMessagesEditReplyMarkup(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat           string   `json:"chat"`
		MessageID      int64    `json:"message_id"`
		InlineKeyboard keyboard `json:"inline_keyboard"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("edit reply markup", "the validated arguments could not be read")
	}
	if err := validateMarkupEdit(arguments.MessageID, arguments.InlineKeyboard); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.EditReplyMarkup(ctx, arguments.MessageID, arguments.InlineKeyboard)
}

func invokeMessagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("delete message", "the validated arguments could not be read")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessage(ctx, arguments.MessageID)
}

// openChat selects the chat from the bound chat targets before any credential is resolved.
func openChat(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	requested string) (*Client, error) {
	return openChatWithHTTP(ctx, resolved, secrets, red, requested, newHTTPClient())
}

func openChatWithHTTP(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	requested string, httpClient *http.Client) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	chat, err := selectChat(resolved, requested)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	client.target = chat
	return client, nil
}

// Client binds one bot token to one resolved connection; target is the chat its chat tools address.
type Client struct {
	base   *url.URL
	token  string
	target string
	http   *http.Client
	set    targetSet
}

// Open resolves the bot token only after the application core selected and confirmed the exact request.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return openWithHTTP(ctx, resolved, secrets, red, newHTTPClient())
}

func openWithHTTP(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	httpClient *http.Client) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	base, err := parseBase(resolved.BaseURL)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	set, err := targetsOf(resolved)
	if err != nil {
		return nil, providerError("open", "the configured Telegram target is unusable")
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleBotToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the bot token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "bot"+value.Secret)
	}
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	client := &Client{base: base, token: value.Secret, http: httpClient, set: set}
	if len(set.chats) == 1 {
		client.target = set.chats[0]
	}
	return client, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultTimeout,
		// The bot token is part of Telegram's required URL path. Refusing every redirect guarantees that
		// it is never replayed to a second URL, even on the configured origin.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestConnection calls getMe, Telegram's read-only authentication check. It never sends to Target.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	return client.testConnection(ctx), nil
}

func (c *Client) testConnection(ctx context.Context) provider.Class {
	_, err := c.exchange(ctx, spec{op: "test connection", method: "getMe", limit: defaultResponseBytes, readOnly: true},
		http.MethodGet, "", nil)
	if err != nil {
		return errorClass(err)
	}
	return provider.ClassOK
}

// SendOptions are the optional features of one sendMessage request. The zero value sends plain text.
type SendOptions struct {
	Text                string   `json:"text"`
	ParseMode           string   `json:"parse_mode"`
	ReplyToMessageID    int64    `json:"reply_to_message_id"`
	MessageThreadID     int64    `json:"message_thread_id"`
	DisableNotification bool     `json:"disable_notification"`
	ProtectContent      bool     `json:"protect_content"`
	DisableLinkPreview  bool     `json:"disable_link_preview"`
	InlineKeyboard      keyboard `json:"inline_keyboard"`
}

// EditOptions are the options editMessageText accepts besides the chat and message.
type EditOptions struct {
	Text               string   `json:"text"`
	ParseMode          string   `json:"parse_mode"`
	DisableLinkPreview bool     `json:"disable_link_preview"`
	InlineKeyboard     keyboard `json:"inline_keyboard"`
}

func validText(text string) bool {
	count := utf8.RuneCountInString(text)
	return utf8.ValidString(text) && count >= 1 && count <= maxMessageLength
}

func (o SendOptions) validate() error {
	switch {
	case !validText(o.Text):
		return providerError("send message", "the message text is outside the supported length")
	case !validParseMode(o.ParseMode):
		return providerError("send message", "the parse mode is not supported")
	case o.ReplyToMessageID < 0 || o.MessageThreadID < 0:
		return providerError("send message", "the reply and topic identifiers must be positive")
	}
	if msg := validateKeyboard(o.InlineKeyboard); msg != "" {
		return providerError("send message", msg)
	}
	return nil
}

func (o EditOptions) validate(messageID int64) error {
	switch {
	case messageID < 1:
		return providerError("edit message", "the message identifier must be positive")
	case !validText(o.Text):
		return providerError("edit message", "the message text is outside the supported length")
	case !validParseMode(o.ParseMode):
		return providerError("edit message", "the parse mode is not supported")
	}
	if msg := validateKeyboard(o.InlineKeyboard); msg != "" {
		return providerError("edit message", msg)
	}
	return nil
}

func validateMarkupEdit(messageID int64, k keyboard) error {
	if messageID < 1 {
		return providerError("edit reply markup", "the message identifier must be positive")
	}
	if msg := validateKeyboard(k); msg != "" {
		return providerError("edit reply markup", msg)
	}
	return nil
}

// SendMessage sends plain text; see Send.
func (c *Client) SendMessage(ctx context.Context, text string) (map[string]any, error) {
	return c.Send(ctx, SendOptions{Text: text})
}

// Send performs exactly one sendMessage request. The target comes only from the Client's resolved
// connection and no retry is attempted after any transport or provider result.
func (c *Client) Send(ctx context.Context, o SendOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send message", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	body := struct {
		ChatID              string              `json:"chat_id"`
		Text                string              `json:"text"`
		ParseMode           string              `json:"parse_mode,omitempty"`
		ReplyParameters     *replyParameters    `json:"reply_parameters,omitempty"`
		MessageThreadID     int64               `json:"message_thread_id,omitempty"`
		DisableNotification bool                `json:"disable_notification,omitempty"`
		ProtectContent      bool                `json:"protect_content,omitempty"`
		LinkPreviewOptions  *linkPreviewOptions `json:"link_preview_options,omitempty"`
		ReplyMarkup         *replyMarkup        `json:"reply_markup,omitempty"`
	}{
		ChatID: c.target, Text: o.Text, ParseMode: o.ParseMode, MessageThreadID: o.MessageThreadID,
		DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent,
		ReplyMarkup: markupOf(o.InlineKeyboard),
	}
	if o.ReplyToMessageID > 0 {
		// No chat_id: the reply stays in the chat this request addresses.
		body.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
	}
	if o.DisableLinkPreview {
		body.LinkPreviewOptions = &linkPreviewOptions{IsDisabled: true}
	}
	raw, err := c.call(ctx, spec{op: "send message", method: "sendMessage", limit: defaultResponseBytes}, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID <= 0 || result.Date <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse("send message"))
	}
	return map[string]any{"message_id": result.MessageID, "date": result.Date}, nil
}

// EditMessage replaces one message's plain text; see Edit.
func (c *Client) EditMessage(ctx context.Context, messageID int64, text string) (map[string]any, error) {
	return c.Edit(ctx, messageID, EditOptions{Text: text})
}

// Edit replaces one message's text in the chosen chat without retrying an ambiguous result. A missing
// keyboard removes an existing one, as editMessageText does.
func (c *Client) Edit(ctx context.Context, messageID int64, o EditOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("edit message", "no chat was selected")
	}
	if err := o.validate(messageID); err != nil {
		return nil, err
	}
	body := struct {
		ChatID             string              `json:"chat_id"`
		MessageID          int64               `json:"message_id"`
		Text               string              `json:"text"`
		ParseMode          string              `json:"parse_mode,omitempty"`
		LinkPreviewOptions *linkPreviewOptions `json:"link_preview_options,omitempty"`
		ReplyMarkup        *replyMarkup        `json:"reply_markup,omitempty"`
	}{ChatID: c.target, MessageID: messageID, Text: o.Text, ParseMode: o.ParseMode,
		ReplyMarkup: markupOf(o.InlineKeyboard)}
	if o.DisableLinkPreview {
		body.LinkPreviewOptions = &linkPreviewOptions{IsDisabled: true}
	}
	raw, err := c.call(ctx, spec{op: "edit message", method: "editMessageText", limit: defaultResponseBytes}, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID != messageID {
		return nil, withUncertainty(spec{}, invalidResponse("edit message"))
	}
	return map[string]any{"message_id": result.MessageID}, nil
}

// EditReplyMarkup sets the inline keyboard of one message, or removes it when k is empty. Removal omits
// reply_markup, which the Bot API treats as "no keyboard" for edits.
func (c *Client) EditReplyMarkup(ctx context.Context, messageID int64, k keyboard) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("edit reply markup", "no chat was selected")
	}
	if err := validateMarkupEdit(messageID, k); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: "edit reply markup", method: "editMessageReplyMarkup", limit: defaultResponseBytes},
		struct {
			ChatID      string       `json:"chat_id"`
			MessageID   int64        `json:"message_id"`
			ReplyMarkup *replyMarkup `json:"reply_markup,omitempty"`
		}{ChatID: c.target, MessageID: messageID, ReplyMarkup: markupOf(k)})
	if err != nil {
		return nil, err
	}
	var confirmed bool
	if json.Unmarshal(raw, &confirmed) == nil && confirmed {
		return map[string]any{"updated": true}, nil
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID != messageID {
		return nil, withUncertainty(spec{}, invalidResponse("edit reply markup"))
	}
	return map[string]any{"message_id": result.MessageID}, nil
}

// DeleteMessage removes one message from the chosen chat without retrying an ambiguous result.
func (c *Client) DeleteMessage(ctx context.Context, messageID int64) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("delete message", "no chat was selected")
	}
	if messageID < 1 {
		return nil, providerError("delete message", "the message identifier must be positive")
	}
	raw, err := c.call(ctx, spec{op: "delete message", method: "deleteMessage", limit: defaultResponseBytes}, struct {
		ChatID    string `json:"chat_id"`
		MessageID int64  `json:"message_id"`
	}{ChatID: c.target, MessageID: messageID})
	if err != nil {
		return nil, err
	}
	var deleted bool
	if json.Unmarshal(raw, &deleted) != nil || !deleted {
		return nil, withUncertainty(spec{}, invalidResponse("delete message"))
	}
	return map[string]any{"deleted": true}, nil
}

func errorClass(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return provider.ClassProviderError
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func validToken(value string) bool {
	if len(value) < 3 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == ':' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validateTarget(target string) error {
	if target == "" || strings.TrimSpace(target) != target || len(target) > 128 {
		return errors.New("target is empty, padded, or too long")
	}
	if strings.HasPrefix(target, "@") {
		if len(target) < 2 {
			return errors.New("username is empty")
		}
		for _, r := range target[1:] {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				continue
			}
			return errors.New("username has unsupported characters")
		}
		return nil
	}
	value, err := strconv.ParseInt(target, 10, 64)
	if err != nil || value == 0 {
		return errors.New("target is neither a chat ID nor an @username")
	}
	return nil
}
