package nextcloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Writing to a conversation is bound exactly like reading it: the token must be bound by a talk target, and
// a message ID only ever reaches the path of that conversation.

const (
	// Talk accepts 32000 characters; a shorter message is easier to review before it is sent.
	maxSendText      = 4000
	maxMessageIDLen  = 18
	maxReactionBytes = 32
	referenceIDBytes = 32

	featureReferenceID = "chat-reference-id"
	featureEdit        = "edit-messages"
	featureReactions   = "reactions"
)

var ocsTalkReaction = []string{"apps", "spreed", "api", "v1", "reaction"}

const (
	uncertainTalkEdit     = "; the message may have been changed, check talkmessages.list before repeating"
	uncertainTalkDelete   = "; the message may have been deleted, check talkmessages.list before repeating"
	uncertainTalkReaction = "; the reaction may have been changed, check the message in Talk before repeating"

	messageTalkNoMessage = "this Talk conversation or message does not exist or is not accessible to this identity"
)

var (
	talkSendErrors = map[int]string{
		http.StatusBadRequest: "Talk refused this message; the text or the replied message does not fit this conversation",
		http.StatusForbidden:  "this identity may not post in this Talk conversation (for example it is read-only)",
	}
	talkEditErrors = map[int]string{
		http.StatusBadRequest: "Talk cannot edit this message (for example it is a system message or the edit period is over)",
		http.StatusForbidden:  "this identity may not edit this message; only its own messages can be edited",
	}
	talkDeleteErrors = map[int]string{
		http.StatusBadRequest: "Talk cannot delete this message (for example it is a system message or too old)",
		http.StatusForbidden:  "this identity may not delete this message",
	}
	talkReactionErrors = map[int]string{
		http.StatusBadRequest: "Talk refused this reaction (for example reactions are off in this conversation)",
		http.StatusForbidden:  "this identity may not react in this Talk conversation",
		http.StatusConflict:   "this identity already reacted to this message with this emoji",
	}
)

// talkWriteError gives a refusal a fixed message that fits Talk; it never carries provider text.
func talkWriteError(op string, err error, messages map[int]string) error {
	return talkNotFound(op, tagAdminError(op, err, messages), messageTalkNoMessage)
}

// SendResult reports a sent message with the reference ID that identifies it in talkmessages.list.
type SendResult struct {
	Sent        bool    `json:"sent"`
	ReferenceID string  `json:"reference_id"`
	Message     Message `json:"message"`
}

// EditResult reports an edited message.
type EditResult struct {
	Edited    bool   `json:"edited"`
	MessageID string `json:"message_id"`
}

// DeleteMessageResult reports a deleted message.
type DeleteMessageResult struct {
	Deleted   bool   `json:"deleted"`
	MessageID string `json:"message_id"`
}

// ReactionResult reports a set or removed reaction.
type ReactionResult struct {
	MessageID string `json:"message_id"`
	Reaction  string `json:"reaction"`
	Added     bool   `json:"added"`
}

func validMessageID(id string) bool { return digitsOnly(id) && len(id) <= maxMessageIDLen }

// validMessageText refuses an empty or oversized text.
func validMessageText(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= maxSendText
}

// validReaction accepts one emoji sequence: no letters, spaces, or controls, at least one non-ASCII
// character. Digits, # and * only appear as the base of a keycap emoji.
func validReaction(reaction string) bool {
	if reaction == "" || len(reaction) > maxReactionBytes || !utf8.ValidString(reaction) {
		return false
	}
	nonASCII := false
	for _, r := range reaction {
		switch {
		case r >= 0x80:
			if r == 0x85 || r == 0xA0 || r == 0x2028 || r == 0x2029 {
				return false
			}
			nonASCII = true
		case r >= '0' && r <= '9', r == '#', r == '*':
		default:
			return false
		}
	}
	return nonASCII
}

func newReferenceID() (string, error) {
	raw := make([]byte, referenceIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// SendMessage posts one message after a single capability read. The reference ID is generated here, sent with
// the message, and named in the hint of an unclear outcome so the message can be found again.
func (c *Client) SendMessage(ctx context.Context, token, text, replyTo string, silent bool) (*SendResult, error) {
	const op = "send talk message"
	if !validTalkToken(token) || !validMessageText(text) || replyTo != "" && !validMessageID(replyTo) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	reference, err := newReferenceID()
	if err != nil {
		return nil, providerError(op, "a reference could not be generated")
	}
	if err := c.talkFeature(ctx, op, featureChat, featureReferenceID); err != nil {
		return nil, err
	}
	form := url.Values{"message": {text}, "referenceId": {reference}}
	if replyTo != "" {
		form.Set("replyTo", replyTo)
	}
	if silent {
		form.Set("silent", "true")
	}
	hint := fmt.Sprintf("; the message may have been sent, check talkmessages.list for reference_id %s before repeating", reference)
	data, err := c.ocsSend(ctx, op, http.MethodPost, ocsRequest{app: ocsTalkChat, suffix: []string{token}}, form, hint)
	if err != nil {
		return nil, talkWriteError(op, err, talkSendErrors)
	}
	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil || !validMessageID(raw.ID.String()) {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud message could not be read"), hint)
	}
	return &SendResult{Sent: true, ReferenceID: reference, Message: messageOf(raw)}, nil
}

// EditMessage replaces the text of one message in the conversation.
func (c *Client) EditMessage(ctx context.Context, token, id, text string) (*EditResult, error) {
	const op = "edit talk message"
	if !validTalkToken(token) || !validMessageID(id) || !validMessageText(text) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureChat, featureEdit); err != nil {
		return nil, err
	}
	_, err := c.ocsSend(ctx, op, http.MethodPut, ocsRequest{app: ocsTalkChat, suffix: []string{token, id}},
		url.Values{"message": {text}}, uncertainTalkEdit)
	if err != nil {
		return nil, talkWriteError(op, err, talkEditErrors)
	}
	return &EditResult{Edited: true, MessageID: id}, nil
}

// DeleteMessage deletes one message in the conversation.
func (c *Client) DeleteMessage(ctx context.Context, token, id string) (*DeleteMessageResult, error) {
	const op = "delete talk message"
	if !validTalkToken(token) || !validMessageID(id) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureChat); err != nil {
		return nil, err
	}
	_, err := c.ocsSend(ctx, op, http.MethodDelete, ocsRequest{app: ocsTalkChat, suffix: []string{token, id}}, nil,
		uncertainTalkDelete)
	if err != nil {
		return nil, talkWriteError(op, err, talkDeleteErrors)
	}
	return &DeleteMessageResult{Deleted: true, MessageID: id}, nil
}

// SetReaction adds or removes the reaction of the identity on one message. The removal carries the emoji in
// the query, which every method reads.
func (c *Client) SetReaction(ctx context.Context, token, id, reaction string, add bool) (*ReactionResult, error) {
	const op = "set talk reaction"
	if !validTalkToken(token) || !validMessageID(id) || !validReaction(reaction) {
		return nil, providerError(op, "the reaction arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureReactions); err != nil {
		return nil, err
	}
	request := ocsRequest{app: ocsTalkReaction, suffix: []string{token, id}}
	method := http.MethodPost
	var form url.Values
	if add {
		form = url.Values{"reaction": {reaction}}
	} else {
		method = http.MethodDelete
		request.query = url.Values{"reaction": {reaction}}
	}
	if _, err := c.ocsSend(ctx, op, method, request, form, uncertainTalkReaction); err != nil {
		return nil, talkWriteError(op, err, talkReactionErrors)
	}
	return &ReactionResult{MessageID: id, Reaction: reaction, Added: add}, nil
}

const (
	messageIDSchema = `{"type":"string","pattern":"^[0-9]{1,18}$"}`
	sendSchemaProps = `"token":` + talkTokenSchema + `,"message":{"type":"string","minLength":1,"maxLength":4000}`
)

func talkWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: talkSensitivity}
}

var (
	messageIDArgument = capability.Argument{Name: "message_id", Required: true,
		Description: "Numeric ID of a message in the conversation, as reported by talkmessages.list"}
	messageTextArgument = capability.Argument{Name: "message", Required: true,
		Description: "Message text, 1 to 4000 characters"}
)

var talkMessagesSend = capability.Descriptor{
	ID: Provider + ".talkmessages.send", Version: 1, Title: "Send a Nextcloud Talk message",
	Description: "Post one message to a Talk conversation the connection binds, optionally as a reply. Qatlas adds a " +
		"random reference_id and reports it; after an unclear outcome the message may exist, so look for the " +
		"reference_id in talkmessages.list before sending again. A rate limit is reported as such and not repeated",
	Tags:     []string{"nextcloud", "talk", "chat", "send", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + sendSchemaProps + `,"reply_to":` + messageIDSchema +
		`,"silent":{"type":"boolean"}},"required":["token","message"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sent":{"type":"boolean"},"reference_id":{"type":"string"},` +
		`"message":` + messageSchema + `},"required":["sent","reference_id","message"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, messageTextArgument,
		{Name: "reply_to", Description: "ID of the message to reply to"},
		{Name: "silent", Description: "True to send without notifying the participants; false when omitted"}},
	Fields: []capability.Field{{Name: "sent", Description: "True when Nextcloud accepted the message"},
		{Name: "reference_id", Description: "Reference Qatlas sent with the message; talkmessages.list reports it"},
		{Name: "message", Description: "The posted message as Nextcloud reports it, untrusted data"}},
	Examples: []capability.Example{{Description: "Reply to a message",
		Arguments: json.RawMessage(`{"token":"abcd1234","message":"Done.","reply_to":"42"}`)}},
}

var talkMessagesEdit = capability.Descriptor{
	ID: Provider + ".talkmessages.edit", Version: 1, Title: "Edit a Nextcloud Talk message",
	Description: "Replace the text of one message in a Talk conversation the connection binds; Talk allows only " +
		"messages of this identity within the edit period of the instance",
	Tags:     []string{"nextcloud", "talk", "chat", "edit", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + sendSchemaProps + `,"message_id":` + messageIDSchema +
		`},"required":["token","message_id","message"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"edited":{"type":"boolean"},"message_id":{"type":"string"}},` +
		`"required":["edited","message_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, messageIDArgument, messageTextArgument},
	Fields: []capability.Field{{Name: "edited", Description: "True when Nextcloud applied the edit"},
		{Name: "message_id", Description: "ID of the edited message"}},
	Examples: []capability.Example{{Description: "Correct a message",
		Arguments: json.RawMessage(`{"token":"abcd1234","message_id":"42","message":"Done, thanks."}`)}},
}

var talkMessagesDelete = capability.Descriptor{
	ID: Provider + ".talkmessages.delete", Version: 1, Title: "Delete a Nextcloud Talk message",
	Description: "Delete one message in a Talk conversation the connection binds; Talk leaves a deletion notice " +
		"and decides whose messages this identity may delete",
	Tags:     []string{"nextcloud", "talk", "chat", "delete", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"message_id":` + messageIDSchema +
		`},"required":["token","message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"message_id":{"type":"string"}},` +
		`"required":["deleted","message_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, messageIDArgument},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Nextcloud deleted the message"},
		{Name: "message_id", Description: "ID of the deleted message"}},
	Examples: []capability.Example{{Description: "Delete a message",
		Arguments: json.RawMessage(`{"token":"abcd1234","message_id":"42"}`)}},
	RequiresToolAllowList: true,
}

var talkReactionsSet = capability.Descriptor{
	ID: Provider + ".talkreactions.set", Version: 1, Title: "React to a Nextcloud Talk message",
	Description: "Add or remove the reaction of this identity on one message of a Talk conversation the connection " +
		"binds; the reaction is one emoji",
	Tags:     []string{"nextcloud", "talk", "chat", "reaction", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"message_id":` + messageIDSchema +
		`,"reaction":{"type":"string","minLength":1,"maxLength":16},"add":{"type":"boolean"}},` +
		`"required":["token","message_id","reaction","add"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"string"},"reaction":{"type":"string"},` +
		`"added":{"type":"boolean"}},"required":["message_id","reaction","added"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, messageIDArgument,
		{Name: "reaction", Required: true, Description: "One emoji"},
		{Name: "add", Required: true, Description: "True to add the reaction, false to remove it"}},
	Fields: []capability.Field{{Name: "message_id", Description: "ID of the message"},
		{Name: "reaction", Description: "The emoji"}, {Name: "added", Description: "True when added, false when removed"}},
	Examples: []capability.Example{{Description: "Thumbs up",
		Arguments: json.RawMessage(`{"token":"abcd1234","message_id":"42","reaction":"👍","add":true}`)}},
}

type talkWriteArguments struct {
	Token     string `json:"token"`
	MessageID string `json:"message_id"`
	Message   string `json:"message"`
	ReplyTo   string `json:"reply_to"`
	Silent    bool   `json:"silent"`
	Reaction  string `json:"reaction"`
	Add       *bool  `json:"add"`
}

// readTalkWrite decodes strictly and binds the conversation before any credential access.
func readTalkWrite(op string, resolved *config.Resolved, raw json.RawMessage) (talkWriteArguments, error) {
	var input talkWriteArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, requireTalkToken(resolved, input.Token)
}

func invokeTalkMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "send talk message"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validMessageText(input.Message) || input.ReplyTo != "" && !validMessageID(input.ReplyTo) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, input.Token, input.Message, input.ReplyTo, input.Silent)
}

func invokeTalkMessagesEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "edit talk message"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validMessageID(input.MessageID) || !validMessageText(input.Message) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.EditMessage(ctx, input.Token, input.MessageID, input.Message)
}

func invokeTalkMessagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete talk message"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validMessageID(input.MessageID) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessage(ctx, input.Token, input.MessageID)
}

func invokeTalkReactionsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set talk reaction"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validMessageID(input.MessageID) || !validReaction(input.Reaction) || input.Add == nil {
		return nil, providerError(op, "the reaction arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.SetReaction(ctx, input.Token, input.MessageID, input.Reaction, *input.Add)
}
