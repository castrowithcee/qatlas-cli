package telegram

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupGames = "games"
	// maxHighScores bounds the shown entries; Telegram returns only a few around the user.
	maxHighScores = 50
)

var gameShortName = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

const gameUserSchema = `"user_id":{"type":"integer","minimum":1}`

var gameChatArgument = capability.Argument{
	Name: "chat", Description: "Bound chat as numeric chat ID; optional when the connection binds exactly one chat",
	Target: "chat",
}

var gameMessageArgument = capability.Argument{
	Name: "message_id", Description: "Identifier of the game message in the chat", Required: true,
}

var gameUserArgument = capability.Argument{
	Name: "user_id", Description: "Telegram user identifier of the player", Required: true,
}

var gamesSend = capability.Descriptor{
	ID:      Provider + ".games.send",
	Version: 1,
	Title:   "Send a Telegram game",
	Description: "Send one game registered with BotFather to a bound chat of an explicit Telegram connection; " +
		"the chat must be bound as a numeric chat ID",
	Tags:     []string{"telegram", "games", "send"},
	Risk:     structuredRisk(dataSensitivity),
	Provider: Provider,
	Group:    groupGames,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"game_short_name":{"type":"string","pattern":"^[A-Za-z0-9_]{1,64}$"},` +
		structuredCommonSchema + `},"required":["game_short_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(structuredOutputSchema),
	Arguments: append(append([]capability.Argument{gameChatArgument}, capability.Argument{
		Name: "game_short_name", Description: "Short name of the game as registered with BotFather", Required: true,
	}), structuredCommonArguments...),
	Fields: structuredFields,
	Examples: []capability.Example{{
		Description: "Send a game to this connection's chat",
		Arguments:   json.RawMessage(`{"game_short_name":"snake"}`),
	}},
}

var gamesSetScore = capability.Descriptor{
	ID:      Provider + ".games.set-score",
	Version: 1,
	Title:   "Set a Telegram game score",
	Description: "Set the score of one player in a game message of a bound chat of an explicit Telegram connection; " +
		"Telegram refuses a lower score unless force is true",
	Tags: []string{"telegram", "games", "score"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: memberDataSensitivity,
	},
	Provider: Provider,
	Group:    groupGames,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + suggestedMessageSchema + `,` +
		gameUserSchema + `,"score":{"type":"integer","minimum":0},"force":{"type":"boolean"},` +
		`"disable_edit_message":{"type":"boolean"}},"required":["message_id","user_id","score"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"set":{"type":"boolean"},` +
		`"message_id":{"type":"integer"}},"required":["set"],"additionalProperties":false}`),
	Arguments: []capability.Argument{gameChatArgument, gameMessageArgument, gameUserArgument,
		{Name: "score", Description: "New score, zero or more", Required: true},
		{Name: "force", Description: "Allow lowering the score or removing a player from the leaderboard; sent only when given"},
		{Name: "disable_edit_message", Description: "Leave the game message unchanged instead of showing the new scoreboard"}},
	Fields: []capability.Field{
		{Name: "set", Description: "True when Telegram accepted the score"},
		{Name: "message_id", Description: "Identifier of the edited game message, when Telegram returned one"},
	},
	Examples: []capability.Example{{
		Description: "Set a player's score in a game message",
		Arguments:   json.RawMessage(`{"message_id":91,"user_id":42,"score":120}`),
	}},
}

var gamesHighScores = capability.Descriptor{
	ID:      Provider + ".games.high-scores",
	Version: 1,
	Title:   "Get Telegram game high scores",
	Description: "Read the high score table around one player for a game message of a bound chat of an explicit " +
		"Telegram connection",
	Tags:     []string{"telegram", "games", "read"},
	Risk:     readRisk(memberDataSensitivity),
	Provider: Provider,
	Group:    groupGames,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + suggestedMessageSchema + `,` +
		gameUserSchema + `},"required":["message_id","user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"scores":{"type":"array","items":` +
		`{"type":"object","properties":{"position":{"type":"integer"},"score":{"type":"integer"},` +
		`"user":{"type":"object","properties":{"id":{"type":"integer"},"is_bot":{"type":"boolean"},` +
		`"first_name":{"type":"string"},"username":{"type":"string"}}}},"required":["position","score"]}},` +
		`"truncated":{"type":"boolean"}},"required":["scores"],"additionalProperties":false}`),
	Arguments: []capability.Argument{gameChatArgument, gameMessageArgument, gameUserArgument},
	Fields: []capability.Field{
		{Name: "scores", Description: "At most 50 entries with position, score, and user"},
		{Name: "truncated", Description: "True when Telegram returned more entries than are shown"},
	},
	Examples: []capability.Example{{
		Description: "Read the high scores around a player",
		Arguments:   json.RawMessage(`{"message_id":91,"user_id":42}`),
	}},
}

// GameOptions are the options of one sendGame request.
type GameOptions struct {
	GameShortName string `json:"game_short_name"`
	SendCommon
}

func (o GameOptions) validate() error {
	const op = "send game"
	if !gameShortName.MatchString(o.GameShortName) {
		return providerError(op, "game_short_name must be 1 to 64 letters, digits, or underscores")
	}
	return o.SendCommon.validate(op)
}

// ScoreOptions are the options of one setGameScore request. Force is sent only when given.
type ScoreOptions struct {
	MessageID          int64  `json:"message_id"`
	UserID             int64  `json:"user_id"`
	Score              *int64 `json:"score"`
	Force              *bool  `json:"force"`
	DisableEditMessage bool   `json:"disable_edit_message"`
}

func (o ScoreOptions) validate() error {
	const op = "set game score"
	if o.MessageID < 1 || o.UserID < 1 {
		return providerError(op, "message_id and user_id must be positive integers")
	}
	if o.Score == nil || *o.Score < 0 {
		return providerError(op, "score must be zero or more")
	}
	return nil
}

func invokeGamesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		GameOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send game", "the validated arguments could not be read")
	}
	if err := arguments.GameOptions.validate(); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	chat, binding, err := selectThread(resolved, arguments.Chat, "chat", &arguments.MessageThreadID,
		arguments.ReplyToMessageID)
	if err != nil {
		return nil, err
	}
	if err := requireNumericChat("sendGame", chat); err != nil {
		return nil, err
	}
	client, err := openSelected(ctx, resolved, secrets, red, chat, binding, newHTTPClient())
	if err != nil {
		return nil, err
	}
	return client.SendGame(ctx, arguments.GameOptions)
}

func invokeGamesSetScore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		ScoreOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("set game score", "the validated arguments could not be read")
	}
	if err := arguments.ScoreOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openNumericChat(ctx, resolved, secrets, red, arguments.Chat, "setGameScore")
	if err != nil {
		return nil, err
	}
	return client.SetGameScore(ctx, arguments.ScoreOptions)
}

func invokeGamesHighScores(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		UserID    int64  `json:"user_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("get game high scores", "the validated arguments could not be read")
	}
	if arguments.MessageID < 1 || arguments.UserID < 1 {
		return nil, providerError("get game high scores", "message_id and user_id must be positive integers")
	}
	client, err := openNumericChat(ctx, resolved, secrets, red, arguments.Chat, "getGameHighScores")
	if err != nil {
		return nil, err
	}
	return client.GameHighScores(ctx, arguments.MessageID, arguments.UserID)
}

// SendGame performs exactly one sendGame request without retrying an ambiguous result. No keyboard is sent:
// Telegram adds the play button itself.
func (c *Client) SendGame(ctx context.Context, o GameOptions) (map[string]any, error) {
	const op, method = "send game", "sendGame"
	chatID, err := c.numericTarget(op, method)
	if err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return c.sendStructured(ctx, op, method, struct {
		ChatID        int64  `json:"chat_id"`
		GameShortName string `json:"game_short_name"`
		commonBody
	}{chatID, o.GameShortName, o.SendCommon.body()})
}

// SetGameScore performs exactly one setGameScore request without retrying an ambiguous result.
func (c *Client) SetGameScore(ctx context.Context, o ScoreOptions) (map[string]any, error) {
	const op, method = "set game score", "setGameScore"
	chatID, err := c.numericTarget(op, method)
	if err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: method, limit: structuredResponseOver}, struct {
		UserID             int64 `json:"user_id"`
		Score              int64 `json:"score"`
		Force              *bool `json:"force,omitempty"`
		DisableEditMessage bool  `json:"disable_edit_message,omitempty"`
		ChatID             int64 `json:"chat_id"`
		MessageID          int64 `json:"message_id"`
	}{o.UserID, *o.Score, o.Force, o.DisableEditMessage, chatID, o.MessageID})
	if err != nil {
		return nil, err
	}
	// Telegram answers True, or the edited Message.
	var done bool
	if json.Unmarshal(raw, &done) == nil && done {
		return map[string]any{"set": true}, nil
	}
	var message struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(raw, &message) != nil || message.MessageID <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"set": true, "message_id": message.MessageID}, nil
}

// GameHighScores performs exactly one getGameHighScores request.
func (c *Client) GameHighScores(ctx context.Context, messageID, userID int64) (map[string]any, error) {
	const op, method = "get game high scores", "getGameHighScores"
	chatID, err := c.numericTarget(op, method)
	if err != nil {
		return nil, err
	}
	if messageID < 1 || userID < 1 {
		return nil, providerError(op, "message_id and user_id must be positive integers")
	}
	raw, err := c.call(ctx, spec{op: op, method: method, limit: defaultResponseBytes, readOnly: true}, struct {
		UserID    int64 `json:"user_id"`
		ChatID    int64 `json:"chat_id"`
		MessageID int64 `json:"message_id"`
	}{userID, chatID, messageID})
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Position int64   `json:"position"`
		Score    int64   `json:"score"`
		User     *tgUser `json:"user"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil, invalidResponse(op)
	}
	scores := make([]map[string]any, 0, len(entries))
	for i, e := range entries {
		if i == maxHighScores {
			break
		}
		item := map[string]any{"position": e.Position, "score": e.Score}
		if u := sender(e.User); u != nil {
			item["user"] = u
		}
		scores = append(scores, item)
	}
	out := map[string]any{"scores": scores}
	if len(entries) > maxHighScores {
		out["truncated"] = true
	}
	return out, nil
}
