package telegram

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const groupInteractions = "interactions"

// Limits of the Bot API for sendPoll.
const (
	maxPollQuestionRunes    = 300
	maxPollOptionRunes      = 100
	maxPollOptions          = 12
	maxPollExplanationRunes = 200
	maxPollExplanationLines = 2
	minPollOpenPeriod       = 5
	maxPollOpenPeriod       = 2628000
	maxPollIDBytes          = 64
)

// reactionEmoji is the fixed list of ReactionTypeEmoji values of the Bot API.
var reactionEmoji = map[string]bool{}

func init() {
	for _, e := range []string{
		"\u2764", "\U0001f44d", "\U0001f44e", "\U0001f525", "\U0001f970", "\U0001f44f", "\U0001f601",
		"\U0001f914", "\U0001f92f", "\U0001f631", "\U0001f92c", "\U0001f622", "\U0001f389", "\U0001f929",
		"\U0001f92e", "\U0001f4a9", "\U0001f64f", "\U0001f44c", "\U0001f54a", "\U0001f921", "\U0001f971",
		"\U0001f974", "\U0001f60d", "\U0001f433", "\u2764\u200d\U0001f525", "\U0001f31a", "\U0001f32d",
		"\U0001f4af", "\U0001f923", "\u26a1", "\U0001f34c", "\U0001f3c6", "\U0001f494", "\U0001f928",
		"\U0001f610", "\U0001f353", "\U0001f37e", "\U0001f48b", "\U0001f595", "\U0001f608", "\U0001f634",
		"\U0001f62d", "\U0001f913", "\U0001f47b", "\U0001f468\u200d\U0001f4bb", "\U0001f440", "\U0001f383",
		"\U0001f648", "\U0001f607", "\U0001f628", "\U0001f91d", "\u270d", "\U0001f917", "\U0001fae1",
		"\U0001f385", "\U0001f384", "\u2603", "\U0001f485", "\U0001f92a", "\U0001f5ff", "\U0001f192",
		"\U0001f498", "\U0001f649", "\U0001f984", "\U0001f618", "\U0001f48a", "\U0001f64a", "\U0001f60e",
		"\U0001f47e", "\U0001f937\u200d\u2642", "\U0001f937", "\U0001f937\u200d\u2640", "\U0001f621",
	} {
		reactionEmoji[e] = true
	}
}

// chatActions is the fixed list of sendChatAction values of the Bot API.
var chatActions = []string{"typing", "upload_photo", "record_video", "upload_video", "record_voice",
	"upload_voice", "upload_document", "choose_sticker", "find_location", "record_video_note", "upload_video_note"}

var customEmojiID = regexp.MustCompile(`^[0-9]{1,20}$`)

func interactionRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{
		Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var pollsSend = capability.Descriptor{
	ID:          Provider + ".polls.send",
	Version:     1,
	Title:       "Send a Telegram poll",
	Description: "Send one poll or quiz to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "polls", "send"},
	Risk:        interactionRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"question":{"type":"string","minLength":1,"maxLength":1200},` +
		`"options":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","minLength":1,"maxLength":400}},` +
		`"type":{"type":"string","enum":["regular","quiz"]},"is_anonymous":{"type":"boolean"},` +
		`"allows_multiple_answers":{"type":"boolean"},` +
		`"correct_option_ids":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"integer","minimum":0,"maximum":11}},` +
		`"explanation":{"type":"string","minLength":1,"maxLength":800},` +
		`"open_period":{"type":"integer","minimum":5,"maximum":2628000},"close_date":{"type":"integer","minimum":1},` +
		`"reply_to_message_id":{"type":"integer","minimum":1},"message_thread_id":{"type":"integer","minimum":1},` +
		`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}},` +
		`"required":["question","options"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"date":{"type":"integer"},"poll_id":{"type":"string"}},` +
		`"required":["message_id","date","poll_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "question", Description: "Poll question, from 1 through 300 characters", Required: true},
		{Name: "options", Description: "Plain-text answer options, from 1 through 12, each from 1 through 100 characters",
			Required: true},
		{Name: "type", Description: "regular (default) or quiz"},
		{Name: "is_anonymous", Description: "Hide who voted; Telegram defaults to true"},
		{Name: "allows_multiple_answers", Description: "Allow several chosen options"},
		{Name: "correct_option_ids", Description: "Quiz only and required there: strictly increasing 0-based option indexes"},
		{Name: "explanation", Description: "Quiz only: plain text shown after a wrong answer, up to 200 characters and 2 line feeds"},
		{Name: "open_period", Description: "Seconds the poll stays open, from 5 through 2628000; not with close_date"},
		{Name: "close_date", Description: "Unix time when the poll closes; not with open_period"},
		{Name: "reply_to_message_id", Description: "Message of the same chat to reply to"},
		{Name: "message_thread_id", Description: "Forum topic to send into"},
		{Name: "disable_notification", Description: "Send silently"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the message"},
	},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Telegram message identifier of the poll"},
		{Name: "date", Description: "Telegram send time as Unix time"},
		{Name: "poll_id", Description: "Telegram poll identifier"},
	},
	Examples: []capability.Example{{
		Description: "Send a regular poll to this connection's chat",
		Arguments:   json.RawMessage(`{"question":"Lunch?","options":["Pizza","Sushi"]}`),
	}},
}

var pollsStop = capability.Descriptor{
	ID:          Provider + ".polls.stop",
	Version:     1,
	Title:       "Stop a Telegram poll",
	Description: "Close one poll sent by the bot in a bound chat; a closed poll cannot be reopened",
	Tags:        []string{"telegram", "polls", "stop"},
	Risk:        interactionRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider, RequiresToolAllowList: true,
	Group: groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1}},"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"stopped":{"type":"boolean"}},` +
		`"required":["stopped"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "message_id", Description: "Identifier of the poll message in the chat", Required: true},
	},
	Fields: []capability.Field{{Name: "stopped", Description: "True when Telegram reports the poll as closed"}},
	Examples: []capability.Example{{
		Description: "Stop a poll in this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

var reactionsSet = capability.Descriptor{
	ID:          Provider + ".reactions.set",
	Version:     1,
	Title:       "Set the Telegram bot reaction",
	Description: "Set or remove the reaction of the bot on one message in a bound chat",
	Tags:        []string{"telegram", "reactions", "set"},
	Risk:        interactionRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"message_id":{"type":"integer","minimum":1},` +
		`"reactions":{"type":"array","maxItems":1,"items":{"type":"object","properties":{` +
		`"emoji":{"type":"string","minLength":1,"maxLength":32},` +
		`"custom_emoji_id":{"type":"string","pattern":"^[0-9]{1,20}$"}},"additionalProperties":false}},` +
		`"is_big":{"type":"boolean"}},"required":["message_id","reactions"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"set":{"type":"boolean"}},` +
		`"required":["set"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "message_id", Description: "Identifier of the message in the chat", Required: true},
		{Name: "reactions", Description: "At most one reaction, each with exactly one of emoji (from Telegram's fixed " +
			"list) or custom_emoji_id (digits); an empty list removes the reaction of the bot", Required: true},
		{Name: "is_big", Description: "Use the big animation"},
	},
	Fields: []capability.Field{{Name: "set", Description: "True when Telegram accepted the reaction change"}},
	Examples: []capability.Example{{
		Description: "React to a message in this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"reactions":[{"emoji":"👍"}]}`),
	}},
}

var chatActionsSend = capability.Descriptor{
	ID:          Provider + ".chatactions.send",
	Version:     1,
	Title:       "Send a Telegram chat action",
	Description: "Show a typing or upload indicator of the bot in a bound chat",
	Tags:        []string{"telegram", "chatactions", "send"},
	Risk:        interactionRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"action":{"type":"string","enum":["typing","upload_photo","record_video","upload_video","record_voice",` +
		`"upload_voice","upload_document","choose_sticker","find_location","record_video_note","upload_video_note"]},` +
		`"message_thread_id":{"type":"integer","minimum":1}},"required":["action"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sent":{"type":"boolean"}},` +
		`"required":["sent"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "action", Description: "Indicator to show for about 5 seconds, for example typing or upload_photo", Required: true},
		{Name: "message_thread_id", Description: "Forum topic to show the indicator in"},
	},
	Fields: []capability.Field{{Name: "sent", Description: "True when Telegram accepted the chat action"}},
	Examples: []capability.Example{{
		Description: "Show a typing indicator in this connection's chat",
		Arguments:   json.RawMessage(`{"action":"typing"}`),
	}},
}

// PollOptions are the options of one sendPoll request. Questions, options, and explanations are plain text.
type PollOptions struct {
	Question              string   `json:"question"`
	Options               []string `json:"options"`
	Type                  string   `json:"type"`
	IsAnonymous           *bool    `json:"is_anonymous"`
	AllowsMultipleAnswers bool     `json:"allows_multiple_answers"`
	CorrectOptionIDs      []int    `json:"correct_option_ids"`
	Explanation           string   `json:"explanation"`
	OpenPeriod            int64    `json:"open_period"`
	CloseDate             int64    `json:"close_date"`
	ReplyToMessageID      int64    `json:"reply_to_message_id"`
	MessageThreadID       int64    `json:"message_thread_id"`
	DisableNotification   bool     `json:"disable_notification"`
	ProtectContent        bool     `json:"protect_content"`
}

func (o PollOptions) validate() error {
	const op = "send poll"
	if n := utf8.RuneCountInString(o.Question); !utf8.ValidString(o.Question) || n < 1 || n > maxPollQuestionRunes {
		return providerError(op, "the question must be from 1 through 300 characters")
	}
	if len(o.Options) < 1 || len(o.Options) > maxPollOptions {
		return providerError(op, "a poll needs from 1 through 12 options")
	}
	for _, option := range o.Options {
		if n := utf8.RuneCountInString(option); !utf8.ValidString(option) || n < 1 || n > maxPollOptionRunes {
			return providerError(op, "each option must be from 1 through 100 characters")
		}
	}
	if o.Type != "" && o.Type != "regular" && o.Type != "quiz" {
		return providerError(op, "the poll type must be regular or quiz")
	}
	if o.Type == "quiz" {
		if len(o.CorrectOptionIDs) == 0 {
			return providerError(op, "a quiz needs correct_option_ids")
		}
		for i, id := range o.CorrectOptionIDs {
			if id < 0 || id >= len(o.Options) || (i > 0 && id <= o.CorrectOptionIDs[i-1]) {
				return providerError(op, "correct_option_ids must be strictly increasing indexes of the options")
			}
		}
	} else if len(o.CorrectOptionIDs) > 0 || o.Explanation != "" {
		return providerError(op, "correct_option_ids and explanation belong to quizzes only")
	}
	if n := utf8.RuneCountInString(o.Explanation); !utf8.ValidString(o.Explanation) || n > maxPollExplanationRunes ||
		strings.Count(o.Explanation, "\n") > maxPollExplanationLines {
		return providerError(op, "the explanation must be at most 200 characters and 2 line feeds")
	}
	switch {
	case o.OpenPeriod != 0 && o.CloseDate != 0:
		return providerError(op, "open_period and close_date cannot be combined")
	case o.OpenPeriod != 0 && (o.OpenPeriod < minPollOpenPeriod || o.OpenPeriod > maxPollOpenPeriod):
		return providerError(op, "open_period must be from 5 through 2628000 seconds")
	case o.CloseDate < 0 || o.OpenPeriod < 0:
		return providerError(op, "open_period and close_date must be positive")
	case o.ReplyToMessageID < 0 || o.MessageThreadID < 0:
		return providerError(op, "the reply and topic identifiers must be positive")
	}
	return nil
}

type reactionArgument struct {
	Emoji         string `json:"emoji"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type reactionType struct {
	Type          string `json:"type"`
	Emoji         string `json:"emoji,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

func reactionTypes(reactions []reactionArgument) ([]reactionType, error) {
	if len(reactions) > 1 {
		return nil, providerError("set reaction", "a bot can set at most one reaction")
	}
	out := make([]reactionType, 0, len(reactions))
	for _, r := range reactions {
		switch {
		case r.Emoji != "" && r.CustomEmojiID == "" && reactionEmoji[r.Emoji]:
			out = append(out, reactionType{Type: "emoji", Emoji: r.Emoji})
		case r.CustomEmojiID != "" && r.Emoji == "" && customEmojiID.MatchString(r.CustomEmojiID):
			out = append(out, reactionType{Type: "custom_emoji", CustomEmojiID: r.CustomEmojiID})
		default:
			return nil, providerError("set reaction",
				"each reaction needs exactly one emoji from Telegram's list or a numeric custom_emoji_id")
		}
	}
	return out, nil
}

func validChatAction(action string) bool {
	for _, a := range chatActions {
		if a == action {
			return true
		}
	}
	return false
}

func invokePollsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		PollOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send poll", "the validated arguments could not be read")
	}
	if err := arguments.PollOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendPoll(ctx, arguments.PollOptions)
}

func invokePollsStop(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("stop poll", "the validated arguments could not be read")
	}
	if arguments.MessageID < 1 {
		return nil, providerError("stop poll", "the message identifier must be positive")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.StopPoll(ctx, arguments.MessageID)
}

func invokeReactionsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string              `json:"chat"`
		MessageID int64               `json:"message_id"`
		Reactions *[]reactionArgument `json:"reactions"`
		IsBig     bool                `json:"is_big"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("set reaction", "the validated arguments could not be read")
	}
	if arguments.MessageID < 1 {
		return nil, providerError("set reaction", "the message identifier must be positive")
	}
	if arguments.Reactions == nil {
		return nil, providerError("set reaction", "reactions are required; pass an empty list to remove the reaction")
	}
	reactions, err := reactionTypes(*arguments.Reactions)
	if err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.setReaction(ctx, arguments.MessageID, reactions, arguments.IsBig)
}

func invokeChatActionsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat            string `json:"chat"`
		Action          string `json:"action"`
		MessageThreadID int64  `json:"message_thread_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send chat action", "the validated arguments could not be read")
	}
	if !validChatAction(arguments.Action) || arguments.MessageThreadID < 0 {
		return nil, providerError("send chat action", "the chat action or topic is not supported")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendChatAction(ctx, arguments.Action, arguments.MessageThreadID)
}

// SendPoll performs exactly one sendPoll request without retrying an ambiguous result.
func (c *Client) SendPoll(ctx context.Context, o PollOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send poll", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	type pollOption struct {
		Text string `json:"text"`
	}
	options := make([]pollOption, len(o.Options))
	for i, text := range o.Options {
		options[i] = pollOption{Text: text}
	}
	body := struct {
		ChatID                string           `json:"chat_id"`
		MessageThreadID       int64            `json:"message_thread_id,omitempty"`
		Question              string           `json:"question"`
		Options               []pollOption     `json:"options"`
		IsAnonymous           *bool            `json:"is_anonymous,omitempty"`
		Type                  string           `json:"type,omitempty"`
		AllowsMultipleAnswers bool             `json:"allows_multiple_answers,omitempty"`
		CorrectOptionIDs      []int            `json:"correct_option_ids,omitempty"`
		Explanation           string           `json:"explanation,omitempty"`
		OpenPeriod            int64            `json:"open_period,omitempty"`
		CloseDate             int64            `json:"close_date,omitempty"`
		DisableNotification   bool             `json:"disable_notification,omitempty"`
		ProtectContent        bool             `json:"protect_content,omitempty"`
		ReplyParameters       *replyParameters `json:"reply_parameters,omitempty"`
	}{
		ChatID: c.target, MessageThreadID: o.MessageThreadID, Question: o.Question, Options: options,
		IsAnonymous: o.IsAnonymous, Type: o.Type, AllowsMultipleAnswers: o.AllowsMultipleAnswers,
		CorrectOptionIDs: o.CorrectOptionIDs, Explanation: o.Explanation, OpenPeriod: o.OpenPeriod,
		CloseDate: o.CloseDate, DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent,
	}
	if o.ReplyToMessageID > 0 {
		body.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
	}
	raw, err := c.call(ctx, spec{op: "send poll", method: "sendPoll", limit: defaultResponseBytes}, body)
	if err != nil {
		return nil, err
	}
	var message struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
		Poll      struct {
			ID string `json:"id"`
		} `json:"poll"`
	}
	if json.Unmarshal(raw, &message) != nil || message.MessageID <= 0 || message.Date <= 0 ||
		message.Poll.ID == "" || len(message.Poll.ID) > maxPollIDBytes {
		return nil, withUncertainty(spec{}, invalidResponse("send poll"))
	}
	return map[string]any{"message_id": message.MessageID, "date": message.Date, "poll_id": message.Poll.ID}, nil
}

// StopPoll performs exactly one stopPoll request. The stopped Poll is read only for its closed flag.
func (c *Client) StopPoll(ctx context.Context, messageID int64) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("stop poll", "no chat was selected")
	}
	if messageID < 1 {
		return nil, providerError("stop poll", "the message identifier must be positive")
	}
	raw, err := c.call(ctx, spec{op: "stop poll", method: "stopPoll", limit: defaultResponseBytes}, struct {
		ChatID    string `json:"chat_id"`
		MessageID int64  `json:"message_id"`
	}{ChatID: c.target, MessageID: messageID})
	if err != nil {
		return nil, err
	}
	var poll struct {
		IsClosed bool `json:"is_closed"`
	}
	if json.Unmarshal(raw, &poll) != nil || !poll.IsClosed {
		return nil, withUncertainty(spec{}, invalidResponse("stop poll"))
	}
	return map[string]any{"stopped": true}, nil
}

// SetReaction performs exactly one setMessageReaction request. An empty list removes the bot's reaction.
func (c *Client) SetReaction(ctx context.Context, messageID int64, reactions []reactionArgument, big bool) (map[string]any, error) {
	types, err := reactionTypes(reactions)
	if err != nil {
		return nil, err
	}
	return c.setReaction(ctx, messageID, types, big)
}

func (c *Client) setReaction(ctx context.Context, messageID int64, reactions []reactionType, big bool) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("set reaction", "no chat was selected")
	}
	if messageID < 1 {
		return nil, providerError("set reaction", "the message identifier must be positive")
	}
	if reactions == nil {
		reactions = []reactionType{}
	}
	err := c.confirmed(ctx, spec{op: "set reaction", method: "setMessageReaction", limit: defaultResponseBytes}, struct {
		ChatID    string         `json:"chat_id"`
		MessageID int64          `json:"message_id"`
		Reaction  []reactionType `json:"reaction"`
		IsBig     bool           `json:"is_big,omitempty"`
	}{ChatID: c.target, MessageID: messageID, Reaction: reactions, IsBig: big})
	if err != nil {
		return nil, err
	}
	return map[string]any{"set": true}, nil
}

// SendChatAction performs exactly one sendChatAction request.
func (c *Client) SendChatAction(ctx context.Context, action string, threadID int64) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send chat action", "no chat was selected")
	}
	if !validChatAction(action) || threadID < 0 {
		return nil, providerError("send chat action", "the chat action or topic is not supported")
	}
	err := c.confirmed(ctx, spec{op: "send chat action", method: "sendChatAction", limit: defaultResponseBytes}, struct {
		ChatID          string `json:"chat_id"`
		MessageThreadID int64  `json:"message_thread_id,omitempty"`
		Action          string `json:"action"`
	}{ChatID: c.target, MessageThreadID: threadID, Action: action})
	if err != nil {
		return nil, err
	}
	return map[string]any{"sent": true}, nil
}
