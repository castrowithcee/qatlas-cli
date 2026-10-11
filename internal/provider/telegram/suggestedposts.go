package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupSuggestedPosts = "suggestedposts"
	// maxSendDateAhead is the Bot API limit of 2678400 seconds (30 days) for send_date.
	maxSendDateAhead = 30 * 24 * time.Hour
	maxDeclineNote   = 128
)

const suggestedMessageSchema = `"message_id":{"type":"integer","minimum":1}`

var suggestedMessageArgument = capability.Argument{
	Name: "message_id", Description: "Identifier of the suggested post message in the direct messages chat", Required: true,
}

var suggestedChatArgument = capability.Argument{
	Name: "chat", Description: "Bound direct messages chat as numeric chat ID; optional when the connection " +
		"binds exactly one chat", Target: "chat",
}

func suggestedRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{
		Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var suggestedpostsApprove = capability.Descriptor{
	ID:      Provider + ".suggestedposts.approve",
	Version: 1,
	Title:   "Approve a Telegram suggested post",
	Description: "Approve one suggested post in the bound channel direct messages chat of an explicit Telegram " +
		"connection; the target must be a numeric chat ID, and the bot needs the right to post messages in the " +
		"channel. The post is published, optionally at send_date, which must lie at most 30 days ahead",
	Tags:     []string{"telegram", "suggestedposts", "approve"},
	Risk:     suggestedRisk(capability.EffectCreate),
	Provider: Provider,
	Group:    groupSuggestedPosts,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + suggestedMessageSchema + `,` +
		`"send_date":{"type":"integer","minimum":1}},"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"approved":{"type":"boolean"}},` +
		`"required":["approved"],"additionalProperties":false}`),
	Arguments: []capability.Argument{suggestedChatArgument, suggestedMessageArgument, {
		Name: "send_date", Description: "Unix time of the publication, in the future and at most 30 days ahead; " +
			"omit when the suggestion already names a date",
	}},
	Fields: []capability.Field{{Name: "approved", Description: "True when Telegram accepted the approval"}},
	Examples: []capability.Example{{
		Description: "Approve a suggested post of this connection's direct messages chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var suggestedpostsDecline = capability.Descriptor{
	ID:      Provider + ".suggestedposts.decline",
	Version: 1,
	Title:   "Decline a Telegram suggested post",
	Description: "Decline one suggested post in the bound channel direct messages chat of an explicit Telegram " +
		"connection; the target must be a numeric chat ID, and the bot needs the right to manage direct " +
		"messages in the channel. The decision is final and the optional comment reaches the creator",
	Tags:     []string{"telegram", "suggestedposts", "decline"},
	Risk:     suggestedRisk(capability.EffectUpdate),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupSuggestedPosts,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + suggestedMessageSchema + `,` +
		`"comment":{"type":"string","maxLength":128}},"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"declined":{"type":"boolean"}},` +
		`"required":["declined"],"additionalProperties":false}`),
	Arguments: []capability.Argument{suggestedChatArgument, suggestedMessageArgument, {
		Name: "comment", Description: "Comment for the creator of the suggested post, at most 128 characters",
	}},
	Fields: []capability.Field{{Name: "declined", Description: "True when Telegram accepted the decision"}},
	Examples: []capability.Example{{
		Description: "Decline a suggested post with a comment",
		Arguments:   json.RawMessage(`{"message_id":91,"comment":"Does not fit the channel"}`),
	}},
}

// openNumericChat selects the bound chat and refuses an @username before any credential is resolved, because
// the suggested-post methods accept only an Integer chat_id.
func openNumericChat(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, requested, method string) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	chat, err := selectChat(resolved, requested)
	if err != nil {
		return nil, err
	}
	if err := requireNumericChat(method, chat); err != nil {
		return nil, err
	}
	return openChat(ctx, resolved, secrets, red, chat)
}

// checkSendDate accepts zero (omitted) or a time after now and at most 30 days ahead.
func checkSendDate(op string, sendDate int64, now time.Time) error {
	if sendDate == 0 {
		return nil
	}
	if sendDate < 0 || sendDate <= now.Unix() || sendDate > now.Add(maxSendDateAhead).Unix() {
		return providerError(op, "send_date must lie in the future and at most 30 days ahead")
	}
	return nil
}

func invokeSuggestedPostsApprove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "approve suggested post"
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		SendDate  int64  `json:"send_date"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.MessageID <= 0 {
		return nil, providerError(op, "message_id must be a positive integer")
	}
	if err := checkSendDate(op, arguments.SendDate, time.Now()); err != nil {
		return nil, err
	}
	client, err := openNumericChat(ctx, resolved, secrets, red, arguments.Chat, "approveSuggestedPost")
	if err != nil {
		return nil, err
	}
	return client.ApproveSuggestedPost(ctx, arguments.MessageID, arguments.SendDate)
}

func invokeSuggestedPostsDecline(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "decline suggested post"
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		Comment   string `json:"comment"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.MessageID <= 0 {
		return nil, providerError(op, "message_id must be a positive integer")
	}
	if err := checkDeclineComment(op, arguments.Comment); err != nil {
		return nil, err
	}
	client, err := openNumericChat(ctx, resolved, secrets, red, arguments.Chat, "declineSuggestedPost")
	if err != nil {
		return nil, err
	}
	return client.DeclineSuggestedPost(ctx, arguments.MessageID, arguments.Comment)
}

func checkDeclineComment(op, comment string) error {
	if !utf8.ValidString(comment) || utf8.RuneCountInString(comment) > maxDeclineNote {
		return providerError(op, "comment must be at most 128 characters")
	}
	return nil
}

// numericTarget returns the selected chat as the Integer the suggested-post methods require.
func (c *Client) numericTarget(op, method string) (int64, error) {
	if c.target == "" {
		return 0, providerError(op, "no chat was selected")
	}
	if err := requireNumericChat(method, c.target); err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(c.target, 10, 64)
	if err != nil {
		return 0, providerError(op, "this Telegram method needs a numeric chat ID")
	}
	return id, nil
}

// ApproveSuggestedPost performs exactly one approveSuggestedPost request. No price field is ever sent.
func (c *Client) ApproveSuggestedPost(ctx context.Context, messageID, sendDate int64) (map[string]any, error) {
	const op, method = "approve suggested post", "approveSuggestedPost"
	chatID, err := c.numericTarget(op, method)
	if err != nil {
		return nil, err
	}
	if messageID <= 0 {
		return nil, providerError(op, "message_id must be a positive integer")
	}
	if err := checkSendDate(op, sendDate, time.Now()); err != nil {
		return nil, err
	}
	err = c.confirmed(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, struct {
		ChatID    int64 `json:"chat_id"`
		MessageID int64 `json:"message_id"`
		SendDate  int64 `json:"send_date,omitempty"`
	}{chatID, messageID, sendDate})
	if err != nil {
		return nil, err
	}
	return map[string]any{"approved": true}, nil
}

// DeclineSuggestedPost performs exactly one declineSuggestedPost request.
func (c *Client) DeclineSuggestedPost(ctx context.Context, messageID int64, comment string) (map[string]any, error) {
	const op, method = "decline suggested post", "declineSuggestedPost"
	chatID, err := c.numericTarget(op, method)
	if err != nil {
		return nil, err
	}
	if messageID <= 0 {
		return nil, providerError(op, "message_id must be a positive integer")
	}
	if err := checkDeclineComment(op, comment); err != nil {
		return nil, err
	}
	err = c.confirmed(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, struct {
		ChatID    int64  `json:"chat_id"`
		MessageID int64  `json:"message_id"`
		Comment   string `json:"comment,omitempty"`
	}{chatID, messageID, comment})
	if err != nil {
		return nil, err
	}
	return map[string]any{"declined": true}, nil
}
