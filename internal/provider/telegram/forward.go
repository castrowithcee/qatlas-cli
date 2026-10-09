package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	minTransferMessages = 2
	maxTransferMessages = 100
	maxCopyCaptionRunes = 1024
	// maxForwardResponseBytes bounds forwardMessage, which answers with the whole forwarded message.
	maxForwardResponseBytes = 256 << 10
)

var transferRisk = capability.Risk{
	Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const transferSchema = `"from_chat":{"type":"string","minLength":1,"maxLength":128},` +
	`"message_id":{"type":"integer","minimum":1},` +
	`"message_ids":{"type":"array","minItems":2,"maxItems":100,"items":{"type":"integer","minimum":1}},` +
	`"message_thread_id":{"type":"integer","minimum":1},` +
	`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}`

const transferOutputSchema = `{"type":"object","properties":{"message_id":{"type":"integer"},` +
	`"message_ids":{"type":"array","items":{"type":"integer"}}},"additionalProperties":false}`

var transferFields = []capability.Field{
	{Name: "message_id", Description: "Identifier of the new message in the target chat; for one source message"},
	{Name: "message_ids", Description: "Identifiers of the new messages in the target chat, in source order; " +
		"Telegram leaves out sources it cannot find or transfer, without notice; for a list of source messages"},
}

func transferArguments() []capability.Argument {
	return []capability.Argument{
		{Name: "chat", Description: "Bound chat that receives the messages; optional when the connection binds " +
			"exactly one chat"},
		{Name: "from_chat", Description: "Bound chat that holds the source messages; may equal chat; optional when " +
			"the connection binds exactly one chat"},
		{Name: "message_id", Description: "One source message identifier; exactly one of message_id and message_ids"},
		{Name: "message_ids", Description: "From 2 through 100 distinct positive source message identifiers in " +
			"strictly ascending order; instead of message_id"},
	}
}

func transferTail() []capability.Argument {
	return []capability.Argument{
		{Name: "message_thread_id", Description: "Forum topic of the target chat to send into"},
		{Name: "disable_notification", Description: "Send silently"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the new messages"},
	}
}

var messagesForward = capability.Descriptor{
	ID:      Provider + ".messages.forward",
	Version: 1,
	Title:   "Forward Telegram messages between bound chats",
	Description: "Forward one message, or from 2 through 100 messages in one request, from a bound chat to a bound " +
		"chat of an explicit Telegram connection, keeping the reference to the original. An unclear outcome is never repeated",
	Tags:     []string{"telegram", "messages", "forward"},
	Risk:     transferRisk,
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + transferSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(transferOutputSchema),
	Arguments:    append(transferArguments(), transferTail()...),
	Fields:       transferFields,
	Examples: []capability.Example{{
		Description: "Forward one message from a bound chat to another bound chat",
		Arguments:   json.RawMessage(`{"chat":"-1001","from_chat":"-1002","message_id":91}`),
	}},
}

var messagesCopy = capability.Descriptor{
	ID:      Provider + ".messages.copy",
	Version: 1,
	Title:   "Copy Telegram messages between bound chats",
	Description: "Copy one message, or from 2 through 100 messages in one request, from a bound chat to a bound chat " +
		"of an explicit Telegram connection without a link to the original. Only a single message takes a new " +
		"caption. An unclear outcome is never repeated",
	Tags:     []string{"telegram", "messages", "copy"},
	Risk:     transferRisk,
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + transferSchema + `,` +
		`"caption":{"type":"string","minLength":1,"maxLength":1024},` +
		`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(transferOutputSchema),
	Arguments: append(append(transferArguments(), transferTail()...),
		capability.Argument{Name: "caption", Description: "New caption of a single copied message, from 1 " +
			"through 1024 characters; replaces the original caption and is not accepted with message_ids"},
		parseModeArgument),
	Fields: transferFields,
	Examples: []capability.Example{{
		Description: "Copy several messages from a bound chat to another bound chat",
		Arguments:   json.RawMessage(`{"chat":"-1001","from_chat":"-1002","message_ids":[91,92,93]}`),
	}},
}

// TransferOptions are the arguments both forward and copy share. Exactly one of MessageID and MessageIDs
// is set; a list of identifiers selects the batch method.
type TransferOptions struct {
	FromChat            string  `json:"from_chat"`
	MessageID           int64   `json:"message_id"`
	MessageIDs          []int64 `json:"message_ids"`
	MessageThreadID     int64   `json:"message_thread_id"`
	DisableNotification bool    `json:"disable_notification"`
	ProtectContent      bool    `json:"protect_content"`
}

// CopyOptions add the caption override that copyMessage offers and copyMessages lacks.
type CopyOptions struct {
	TransferOptions
	Caption   *string `json:"caption"`
	ParseMode string  `json:"parse_mode"`
}

func (o TransferOptions) validate(op string) error {
	single, batch := o.MessageID != 0, len(o.MessageIDs) > 0
	switch {
	case single == batch:
		return providerError(op, "exactly one of message_id and message_ids is required")
	case o.MessageID < 0:
		return providerError(op, "the message identifier must be positive")
	case o.MessageThreadID < 0:
		return providerError(op, "the topic identifier must be positive")
	case !batch:
		return nil
	case len(o.MessageIDs) < minTransferMessages || len(o.MessageIDs) > maxTransferMessages:
		return providerError(op, "message_ids needs from 2 through 100 identifiers; use message_id for one message")
	}
	// Telegram requires ascending identifiers for the batch methods. Qatlas refuses another order instead of
	// sorting, so the order of the new messages is never a surprise.
	for i, id := range o.MessageIDs {
		switch {
		case id < 1:
			return providerError(op, "the message identifiers must be positive")
		case i > 0 && id == o.MessageIDs[i-1]:
			return providerError(op, "the message identifiers must be distinct")
		case i > 0 && id < o.MessageIDs[i-1]:
			return providerError(op, "the message identifiers must be in ascending order")
		}
	}
	return nil
}

func (o CopyOptions) validate() error {
	const op = "copy messages"
	if err := o.TransferOptions.validate(op); err != nil {
		return err
	}
	switch {
	case o.Caption == nil && o.ParseMode != "":
		return providerError(op, "parse_mode needs a caption")
	case o.Caption != nil && len(o.MessageIDs) > 0:
		return providerError(op, "a caption is accepted only for a single message")
	case !validParseMode(o.ParseMode):
		return providerError(op, "the parse mode is not supported")
	}
	if o.Caption != nil {
		if n := utf8.RuneCountInString(*o.Caption); !utf8.ValidString(*o.Caption) || n < 1 || n > maxCopyCaptionRunes {
			return providerError(op, "the caption must be from 1 through 1024 characters")
		}
	}
	return nil
}

func invokeMessagesForward(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		TransferOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("forward messages", "the validated arguments could not be read")
	}
	if err := arguments.TransferOptions.validate("forward messages"); err != nil {
		return nil, err
	}
	client, source, err := openChatPair(ctx, resolved, secrets, red, arguments.Chat, arguments.FromChat)
	if err != nil {
		return nil, err
	}
	return client.Forward(ctx, source, arguments.TransferOptions)
}

func invokeMessagesCopy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		CopyOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("copy messages", "the validated arguments could not be read")
	}
	if err := arguments.CopyOptions.validate(); err != nil {
		return nil, err
	}
	client, source, err := openChatPair(ctx, resolved, secrets, red, arguments.Chat, arguments.FromChat)
	if err != nil {
		return nil, err
	}
	return client.Copy(ctx, source, arguments.CopyOptions)
}

// openChatPair selects the target chat and the source chat from the bound chat targets before any credential
// is resolved. Both may be the same chat.
func openChatPair(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	chat, from string) (*Client, string, error) {
	return openChatPairWithHTTP(ctx, resolved, secrets, red, chat, from, newHTTPClient())
}

func openChatPairWithHTTP(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, chat, from string, httpClient *http.Client) (*Client, string, error) {
	if resolved == nil {
		return nil, "", providerError("open", "no connection was selected")
	}
	target, err := selectChatArgument(resolved, chat, "chat")
	if err != nil {
		return nil, "", err
	}
	source, err := selectChatArgument(resolved, from, "from_chat")
	if err != nil {
		return nil, "", err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, "", err
	}
	client.target = target
	return client, source, nil
}

// transferBody is the union of the four fixed requests; omitted fields keep each request to its method's
// documented parameters.
type transferBody struct {
	ChatID              string  `json:"chat_id"`
	MessageThreadID     int64   `json:"message_thread_id,omitempty"`
	FromChatID          string  `json:"from_chat_id"`
	MessageID           int64   `json:"message_id,omitempty"`
	MessageIDs          []int64 `json:"message_ids,omitempty"`
	Caption             string  `json:"caption,omitempty"`
	ParseMode           string  `json:"parse_mode,omitempty"`
	DisableNotification bool    `json:"disable_notification,omitempty"`
	ProtectContent      bool    `json:"protect_content,omitempty"`
}

// Forward performs exactly one forwardMessage or forwardMessages request without retrying an ambiguous result.
func (c *Client) Forward(ctx context.Context, source string, o TransferOptions) (map[string]any, error) {
	const op = "forward messages"
	if err := o.validate(op); err != nil {
		return nil, err
	}
	return c.transfer(ctx, op, source, "forwardMessage", "forwardMessages", o, "", "")
}

// Copy performs exactly one copyMessage or copyMessages request without retrying an ambiguous result.
func (c *Client) Copy(ctx context.Context, source string, o CopyOptions) (map[string]any, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	caption := ""
	if o.Caption != nil {
		caption = *o.Caption
	}
	return c.transfer(ctx, "copy messages", source, "copyMessage", "copyMessages", o.TransferOptions,
		caption, o.ParseMode)
}

func (c *Client) transfer(ctx context.Context, op, source, singleMethod, batchMethod string, o TransferOptions,
	caption, parseMode string) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	// The source must be a bound chat even when a caller bypasses the invoke functions.
	bound := false
	for _, chat := range c.set.chats {
		bound = bound || chat == source
	}
	if source == "" || !bound {
		return nil, providerError(op, "the source chat is not bound to this connection")
	}
	body := transferBody{
		ChatID: c.target, MessageThreadID: o.MessageThreadID, FromChatID: source, MessageID: o.MessageID,
		MessageIDs: o.MessageIDs, Caption: caption, ParseMode: parseMode,
		DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent,
	}
	if len(o.MessageIDs) == 0 {
		limit := defaultResponseBytes
		if singleMethod == "forwardMessage" {
			limit = maxForwardResponseBytes
		}
		raw, err := c.call(ctx, spec{op: op, method: singleMethod, limit: limit}, body)
		if err != nil {
			return nil, err
		}
		var result struct {
			MessageID int64 `json:"message_id"`
		}
		if json.Unmarshal(raw, &result) != nil || result.MessageID < 1 {
			return nil, withUncertainty(spec{}, invalidResponse(op))
		}
		return map[string]any{"message_id": result.MessageID}, nil
	}
	raw, err := c.call(ctx, spec{op: op, method: batchMethod, limit: defaultResponseBytes}, body)
	if err != nil {
		return nil, err
	}
	var result []struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result) > len(o.MessageIDs) {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	ids := make([]int64, 0, len(result))
	for _, r := range result {
		if r.MessageID < 1 {
			return nil, withUncertainty(spec{}, invalidResponse(op))
		}
		ids = append(ids, r.MessageID)
	}
	return map[string]any{"message_ids": ids}, nil
}
