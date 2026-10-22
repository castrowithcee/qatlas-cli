package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxBotCommands      = 100
	maxCommandNameLen   = 32
	maxCommandDescRunes = 256
	scopeSchemaProps    = `"scope":{"type":"string","enum":["default","all_private_chats","all_group_chats",` +
		`"all_chat_administrators","chat","chat_administrators","chat_member"]},` +
		`"chat":{"type":"string","minLength":1,"maxLength":128},"user_id":{"type":"integer","minimum":1},` +
		`"language_code":{"type":"string","pattern":"^([a-z]{2})?$"}`
	commandsSchema = `"commands":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{` +
		`"command":{"type":"string","pattern":"^[a-z0-9_]{1,32}$"},` +
		`"description":{"type":"string","minLength":1,"maxLength":256}},` +
		`"required":["command","description"],"additionalProperties":false}}`
)

var commandScopeArguments = []capability.Argument{
	{Name: "scope", Description: "Command scope: default, all_private_chats, all_group_chats, " +
		"all_chat_administrators, chat, chat_administrators, or chat_member; default when omitted"},
	{Name: "chat", Description: "Bound chat for the scopes chat, chat_administrators, and chat_member; " +
		"required for them, refused for the others"},
	{Name: "user_id", Description: "Positive Telegram user identifier; required for the scope chat_member only"},
	{Name: "language_code", Description: "Two lowercase letters (ISO 639-1) or empty for all languages"},
}

func botSettingRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{
		Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var commandsGet = capability.Descriptor{
	ID:      Provider + ".commands.get",
	Version: 1,
	Title:   "Get the Telegram bot commands",
	Description: "Read the command list of the bot for one scope and language; the chat scopes address a bound " +
		"chat only. Requires the bot target",
	Tags: []string{"telegram", "bot", "commands", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + scopeSchemaProps +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"commands":{"type":"array","maxItems":100,` +
		`"items":{"type":"object","properties":{"command":{"type":"string"},"description":{"type":"string"},` +
		`"is_ephemeral":{"type":"boolean"}},` +
		`"required":["command","description"],"additionalProperties":false}}},"required":["commands"],` +
		`"additionalProperties":false}`),
	Arguments: commandScopeArguments,
	Fields: []capability.Field{{Name: "commands",
		Description: "Commands of the scope, at most 100, each with command, description, and is_ephemeral when Telegram sent it; empty when none are set"}},
	Examples: []capability.Example{{Description: "Read the default commands", Arguments: json.RawMessage(`{}`)}},
}

var commandsSet = capability.Descriptor{
	ID:      Provider + ".commands.set",
	Version: 1,
	Title:   "Set the Telegram bot commands",
	Description: "Replace the command list of the bot for one scope and language with 1 through 100 commands; " +
		"the chat scopes address a bound chat only. Requires the bot target",
	Tags:     []string{"telegram", "bot", "commands"},
	Risk:     botSettingRisk(capability.EffectUpdate),
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + scopeSchemaProps + `,` + commandsSchema +
		`},"required":["commands"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument(nil), commandScopeArguments...), capability.Argument{
		Name: "commands", Required: true,
		Description: "1 through 100 objects with command (1 through 32 of a-z, 0-9, _) and description (1 through 256 characters)"}),
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the command list"}},
	Examples: []capability.Example{{
		Description: "Set the default commands",
		Arguments:   json.RawMessage(`{"commands":[{"command":"help","description":"Show help"}]}`),
	}},
}

var commandsDelete = capability.Descriptor{
	ID:      Provider + ".commands.delete",
	Version: 1,
	Title:   "Delete the Telegram bot commands",
	Description: "Delete the command list of the bot for one scope and language so that the next broader list " +
		"applies; the chat scopes address a bound chat only. Requires the bot target",
	Tags:     []string{"telegram", "bot", "commands", "delete"},
	Risk:     botSettingRisk(capability.EffectDelete),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + scopeSchemaProps +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: commandScopeArguments,
	Fields:    []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the deletion"}},
	Examples:  []capability.Example{{Description: "Delete the default commands", Arguments: json.RawMessage(`{}`)}},
}

const menuChatArgument = "Bound private chat whose menu button is addressed, by numeric chat ID; " +
	"the default menu button of the bot when omitted"

var menubuttonGet = capability.Descriptor{
	ID:      Provider + ".menu-button.get",
	Version: 1,
	Title:   "Get the Telegram bot menu button",
	Description: "Read the menu button of the bot, the default one or that of a bound private chat; a web app URL is " +
		"shown as text and never fetched. Requires the bot target",
	Tags: []string{"telegram", "bot", "menubutton", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"chat":{"type":"string","minLength":1,"maxLength":128}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"text":{"type":"string"},` +
		`"web_app":{"type":"object","description":"Telegram Bot API WebAppInfo of a web_app button"}},` +
		`"required":["type"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "chat", Description: menuChatArgument}},
	Fields: []capability.Field{
		{Name: "type", Description: "Menu button type: commands, default, or web_app"},
		{Name: "text", Description: "Label of a web_app button; absent for the other types"},
		{Name: "web_app", Description: "Web App of a web_app button as an object with its url, which is untrusted text, " +
			"at most 2048 characters; absent for the other types"},
	},
	Examples: []capability.Example{{Description: "Read the default menu button", Arguments: json.RawMessage(`{}`)}},
}

var menubuttonSet = capability.Descriptor{
	ID:      Provider + ".menu-button.set",
	Version: 1,
	Title:   "Set the Telegram bot menu button",
	Description: "Set the menu button of the bot, the default one or that of a bound private chat, to the " +
		"command list or to the Telegram default; web app buttons are not supported. Requires the bot target",
	Tags:     []string{"telegram", "bot", "menubutton"},
	Risk:     botSettingRisk(capability.EffectUpdate),
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"chat":{"type":"string","minLength":1,"maxLength":128},` +
		`"type":{"type":"string","enum":["commands","default"]}},"required":["type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "chat", Description: menuChatArgument},
		{Name: "type", Required: true, Description: "commands or default"},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the menu button"}},
	Examples: []capability.Example{{
		Description: "Show the command list as the default menu button", Arguments: json.RawMessage(`{"type":"commands"}`),
	}},
}

// commandScope is the typed BotCommandScope sent to Telegram; it never carries free-form data.
type commandScope struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id,omitempty"`
	UserID int64  `json:"user_id,omitempty"`
}

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	// IsEphemeral is read from Telegram only; commands.set never sends it.
	IsEphemeral *bool `json:"is_ephemeral,omitempty"`
}

type commandScopeArgs struct {
	Scope        string `json:"scope"`
	Chat         string `json:"chat"`
	UserID       int64  `json:"user_id"`
	LanguageCode string `json:"language_code"`
}

// resolve checks the scope locally and binds chat scopes to a configured chat target. A chat scope never
// falls back to an implicit single chat: the chat must be named.
func (a commandScopeArgs) resolve(resolved *config.Resolved, op string) (commandScope, error) {
	scope := commandScope{Type: a.Scope}
	if scope.Type == "" {
		scope.Type = "default"
	}
	switch scope.Type {
	case "default", "all_private_chats", "all_group_chats", "all_chat_administrators":
		if a.Chat != "" || a.UserID != 0 {
			return scope, providerError(op, "chat and user_id belong to chat scopes only")
		}
	case "chat", "chat_administrators", "chat_member":
		if a.Chat == "" {
			return scope, providerError(op, "this scope needs the chat argument")
		}
		chat, err := selectChatArgument(resolved, a.Chat, "chat")
		if err != nil {
			return scope, err
		}
		scope.ChatID = chat
		if scope.Type == "chat_member" {
			if a.UserID < 1 {
				return scope, providerError(op, "the scope chat_member needs a positive user_id")
			}
			scope.UserID = a.UserID
		} else if a.UserID != 0 {
			return scope, providerError(op, "user_id belongs to the scope chat_member only")
		}
	default:
		return scope, providerError(op, "the scope is not supported")
	}
	if !validLanguageCode(a.LanguageCode) {
		return scope, providerError(op, "language_code must be empty or two lowercase letters")
	}
	return scope, nil
}

func validLanguageCode(code string) bool {
	if code == "" {
		return true
	}
	return len(code) == 2 && code[0] >= 'a' && code[0] <= 'z' && code[1] >= 'a' && code[1] <= 'z'
}

func validateCommands(op string, commands []botCommand) error {
	if len(commands) < 1 || len(commands) > maxBotCommands {
		return providerError(op, "commands needs 1 through 100 entries")
	}
	for _, c := range commands {
		if c.Command == "" || len(c.Command) > maxCommandNameLen {
			return providerError(op, "a command name needs 1 through 32 characters")
		}
		for i := 0; i < len(c.Command); i++ {
			b := c.Command[i]
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_') {
				return providerError(op, "a command name may use a-z, 0-9, and _ only")
			}
		}
		if !utf8.ValidString(c.Description) || utf8.RuneCountInString(c.Description) < 1 ||
			utf8.RuneCountInString(c.Description) > maxCommandDescRunes {
			return providerError(op, "a command description needs 1 through 256 valid characters")
		}
	}
	return nil
}

// openBot refuses before any credential access unless the bot target is bound.
func openBot(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string) (func() (*Client, error), error) {
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	return func() (*Client, error) { return Open(ctx, resolved, secrets, red) }, nil
}

type commandsSetArgs struct {
	commandScopeArgs
	Commands []botCommand `json:"commands"`
}

func invokeCommandsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get commands"
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	var a commandScopeArgs
	if err := decodeStrict(raw, &a); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	scope, err := a.resolve(resolved, op)
	if err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.GetCommands(ctx, scope, a.LanguageCode)
}

func invokeCommandsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set commands"
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	var a commandsSetArgs
	if err := decodeStrict(raw, &a); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	scope, err := a.resolve(resolved, op)
	if err != nil {
		return nil, err
	}
	if err := validateCommands(op, a.Commands); err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.SetCommands(ctx, scope, a.LanguageCode, a.Commands)
}

func invokeCommandsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete commands"
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	var a commandScopeArgs
	if err := decodeStrict(raw, &a); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	scope, err := a.resolve(resolved, op)
	if err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.DeleteCommands(ctx, scope, a.LanguageCode)
}

func invokeMenubuttonGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get menu button"
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	var a struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	chat, err := menuChat(resolved, op, a.Chat)
	if err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.GetMenuButton(ctx, chat)
}

func invokeMenubuttonSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set menu button"
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	var a struct {
		Chat string `json:"chat"`
		Type string `json:"type"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if a.Type != "commands" && a.Type != "default" {
		return nil, providerError(op, "type must be commands or default")
	}
	chat, err := menuChat(resolved, op, a.Chat)
	if err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.SetMenuButton(ctx, chat, a.Type)
}

// menuChat returns the numeric ID of the bound chat, or 0 for the bot's default menu button. The chat
// argument is never implied: an omitted chat addresses the default button.
func menuChat(resolved *config.Resolved, op, requested string) (int64, error) {
	if requested == "" {
		return 0, nil
	}
	chat, err := selectChatArgument(resolved, requested, "chat")
	if err != nil {
		return 0, err
	}
	if err := requireNumericChat("setChatMenuButton", chat); err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil || id == 0 {
		return 0, providerError(op, "this Telegram method needs a numeric chat ID")
	}
	return id, nil
}

type commandsOut struct {
	Commands []botCommand `json:"commands"`
}

// GetCommands performs exactly one getMyCommands request.
func (c *Client) GetCommands(ctx context.Context, scope commandScope, language string) (commandsOut, error) {
	const op = "get commands"
	raw, err := c.call(ctx, spec{op: op, method: "getMyCommands", limit: defaultResponseBytes, readOnly: true},
		struct {
			Scope        commandScope `json:"scope"`
			LanguageCode string       `json:"language_code,omitempty"`
		}{scope, language})
	if err != nil {
		return commandsOut{}, err
	}
	var list []botCommand
	if json.Unmarshal(raw, &list) != nil {
		return commandsOut{}, invalidResponse(op)
	}
	out := commandsOut{Commands: []botCommand{}}
	for _, item := range list {
		if len(out.Commands) == maxBotCommands {
			break
		}
		out.Commands = append(out.Commands, botCommand{
			Command: capText(item.Command, maxCommandNameLen), Description: capText(item.Description, maxCommandDescRunes),
			IsEphemeral: item.IsEphemeral})
	}
	return out, nil
}

// SetCommands performs exactly one setMyCommands request.
func (c *Client) SetCommands(ctx context.Context, scope commandScope, language string, commands []botCommand) (map[string]any, error) {
	const op = "set commands"
	if err := validateCommands(op, commands); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setMyCommands", limit: defaultResponseBytes}, struct {
		Commands     []botCommand `json:"commands"`
		Scope        commandScope `json:"scope"`
		LanguageCode string       `json:"language_code,omitempty"`
	}{commands, scope, language})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// DeleteCommands performs exactly one deleteMyCommands request.
func (c *Client) DeleteCommands(ctx context.Context, scope commandScope, language string) (map[string]any, error) {
	err := c.confirmed(ctx, spec{op: "delete commands", method: "deleteMyCommands", limit: defaultResponseBytes},
		struct {
			Scope        commandScope `json:"scope"`
			LanguageCode string       `json:"language_code,omitempty"`
		}{scope, language})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

type menuButtonOut struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	WebApp *webAppInfo `json:"web_app,omitempty"`
}

// webAppInfo is the Bot API type WebAppInfo; its URL is shown as text and never requested.
type webAppInfo struct {
	URL string `json:"url"`
}

const maxWebAppURLRunes = 2048

// GetMenuButton performs exactly one getChatMenuButton request; chat 0 addresses the default button. Only
// the web app URL is only copied into the output, never requested.
func (c *Client) GetMenuButton(ctx context.Context, chat int64) (menuButtonOut, error) {
	const op = "get menu button"
	raw, err := c.call(ctx, spec{op: op, method: "getChatMenuButton", limit: defaultResponseBytes, readOnly: true},
		struct {
			ChatID int64 `json:"chat_id,omitempty"`
		}{chat})
	if err != nil {
		return menuButtonOut{}, err
	}
	var button menuButtonOut
	if json.Unmarshal(raw, &button) != nil || button.Type == "" {
		return menuButtonOut{}, invalidResponse(op)
	}
	out := menuButtonOut{Type: capText(button.Type, maxNameRunes), Text: capText(button.Text, maxNameRunes)}
	if button.WebApp != nil && button.WebApp.URL != "" {
		out.WebApp = &webAppInfo{URL: capText(button.WebApp.URL, maxWebAppURLRunes)}
	}
	return out, nil
}

// SetMenuButton performs exactly one setChatMenuButton request; chat 0 addresses the default button.
func (c *Client) SetMenuButton(ctx context.Context, chat int64, kind string) (map[string]any, error) {
	const op = "set menu button"
	if kind != "commands" && kind != "default" {
		return nil, providerError(op, "type must be commands or default")
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatMenuButton", limit: defaultResponseBytes}, struct {
		ChatID     int64 `json:"chat_id,omitempty"`
		MenuButton struct {
			Type string `json:"type"`
		} `json:"menu_button"`
	}{chat, struct {
		Type string `json:"type"`
	}{kind}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}
