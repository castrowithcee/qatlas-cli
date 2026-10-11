package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// administrationSensitivity classifies the instance-wide tools: bot accounts exist across the whole
// instance, not within one team's channels.
const administrationSensitivity = "infomaniak-kchat-administration"

// Local bounds of the bot fields this provider writes.
const (
	maxBotDisplayName = 64
	maxBotDescription = 1024
	botsAdministered  = "kChat bot administration"
	uncertainBot      = "; the bot may have been changed, read it before changing it again"
)

// botUsernamePattern is kChat's bot handle form: lowercase letters, digits, dots, hyphens, and underscores.
var botUsernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,22}$`)

func botRisk(effect capability.Effect, idempotency capability.Idempotency, confirmation capability.Confirmation) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: confirmation, OpenWorld: true,
		DataSensitivity: administrationSensitivity}
}

// botScopeNote states the shared limits of the bot tools in their descriptions.
const botScopeNote = "Instance-wide: only on a connection that binds every team of its token and has no channel " +
	"target. Bot tokens and creating bots are not offered through Qatlas."

var botSchema = `{"type":"object","properties":{` +
	`"user_id":` + idSchema + `,"username":{"type":"string"},"display_name":{"type":"string"},` +
	`"description":{"type":"string"},"owner_id":{"type":"string"},"deleted":{"type":"boolean"}},` +
	`"required":["user_id","username","display_name","description","owner_id","deleted"],"additionalProperties":false}`

var botFields = []capability.Field{
	{Name: "user_id", Description: "User identifier of the bot account"},
	{Name: "username", Description: "Bot handle, untrusted data"},
	{Name: "display_name", Description: "Display name, untrusted data"},
	{Name: "description", Description: "Description, untrusted data"},
	{Name: "owner_id", Description: "User identifier of the bot's owner"},
	{Name: "deleted", Description: "True when the bot is deactivated"},
}

var botIDArgument = capability.Argument{Name: "bot_user_id", Required: true,
	Description: "User identifier of the bot account from bots.list"}

var botsList = capability.Descriptor{
	ID:      Provider + ".bots.list",
	Version: 1,
	Title:   "List Infomaniak kChat bots",
	Description: "List the bot accounts of the kChat instance, one kChat page at a time, with user ID, handle, " +
		"display name, description, owner, and deactivation state. " + botScopeNote,
	Tags:                  []string{"infomaniak", "kchat", "bots", "administration", "list"},
	Risk:                  botRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"bots":{"type":"array","items":` + botSchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["bots","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Bots per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, botFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "per_page", Description: "Page size that was requested"},
		capability.Field{Name: "count", Description: "Number of bots reported on this page"},
		capability.Field{Name: "has_more", Description: "True when kChat's page was full, so a further page may " +
			"exist; pages are never read on by themselves"},
	),
	Examples: []capability.Example{{Description: "List the bots", Arguments: json.RawMessage(`{}`)}},
}

var botsGet = capability.Descriptor{
	ID:                    Provider + ".bots.get",
	Version:               1,
	Title:                 "Get an Infomaniak kChat bot",
	Description:           "Read exactly one bot account of the kChat instance by its user ID. " + botScopeNote,
	Tags:                  []string{"infomaniak", "kchat", "bots", "administration", "get"},
	Risk:                  botRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"bot_user_id":` + idSchema + `},` +
		`"required":["bot_user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(botSchema),
	Arguments:    []capability.Argument{botIDArgument},
	Fields:       botFields,
	Examples: []capability.Example{{Description: "Read one bot",
		Arguments: json.RawMessage(`{"bot_user_id":"abc123bot00000000000000000"}`)}},
}

var botsUpdate = capability.Descriptor{
	ID:      Provider + ".bots.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat bot",
	Description: "Change the handle, display name, or description of exactly one existing bot account with one " +
		"confirmed request; only the given fields change. " + botScopeNote,
	Tags:                  []string{"infomaniak", "kchat", "bots", "administration", "update"},
	Risk:                  botRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"bot_user_id":` + idSchema + `,` +
		`"username":{"type":"string","pattern":"^[a-z0-9._-]{3,22}$"},` +
		`"display_name":{"type":"string","maxLength":` + itoa(maxBotDisplayName) + `},` +
		`"description":{"type":"string","maxLength":` + itoa(maxBotDescription) + `}},` +
		`"required":["bot_user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(botSchema),
	Arguments: []capability.Argument{botIDArgument,
		{Name: "username", Description: "New handle, 3 to 22 lowercase letters, digits, dots, hyphens, or underscores"},
		{Name: "display_name", Description: "New display name, up to " + itoa(maxBotDisplayName) +
			" characters; empty clears it"},
		{Name: "description", Description: "New description, up to " + itoa(maxBotDescription) +
			" characters; empty clears it; at least one of username, display_name, and description is required"}},
	Fields: botFields,
	Examples: []capability.Example{{Description: "Change the description of one bot",
		Arguments: json.RawMessage(`{"bot_user_id":"abc123bot00000000000000000","description":"Build alerts"}`)}},
}

func botStateTool(name, title, verb, effectNote string) capability.Descriptor {
	return capability.Descriptor{
		ID:      Provider + ".bots." + name,
		Version: 1,
		Title:   title,
		Description: verb + " exactly one existing bot account with one confirmed request. " + effectNote + " " +
			botScopeNote,
		Tags:                  []string{"infomaniak", "kchat", "bots", "administration", name},
		Risk:                  botRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
		Provider:              Provider,
		RequiresToolAllowList: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"bot_user_id":` + idSchema + `},` +
			`"required":["bot_user_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(botSchema),
		Arguments:    []capability.Argument{botIDArgument},
		Fields:       botFields,
		Examples: []capability.Example{{Description: title,
			Arguments: json.RawMessage(`{"bot_user_id":"abc123bot00000000000000000"}`)}},
	}
}

var botsDisable = botStateTool("disable", "Disable an Infomaniak kChat bot", "Disable",
	"This stops every integration of the bot at once.")

var botsEnable = botStateTool("enable", "Enable an Infomaniak kChat bot", "Enable",
	"This reactivates a deactivated bot account and its access.")

// botJSON is the subset of the kChat Bot resource this provider reads.
type botJSON struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	DeleteAt    int64  `json:"delete_at"`
}

// Bot is the stable Qatlas view of one bot account.
type Bot struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	Deleted     bool   `json:"deleted"`
}

func botOf(b *botJSON) Bot {
	return Bot{UserID: b.UserID, Username: bounded(b.Username), DisplayName: bounded(b.DisplayName),
		Description: bounded(b.Description), OwnerID: bounded(b.OwnerID), Deleted: b.DeleteAt != 0}
}

// BotsPage is one page of the bot accounts of the instance.
type BotsPage struct {
	Bots    []Bot `json:"bots"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Count   int   `json:"count"`
	HasMore bool  `json:"has_more"`
}

func botPath(botID string) string { return "/api/v4/bots/" + url.PathEscape(botID) }

// requireInstanceScope is the local half of the instance-wide gate: it refuses a connection with a channel
// allow-list before a secret is resolved and before any request is sent. what names the refused capability,
// never a configured target. The live half, see (*Client).requireInstanceTeams, proves that the bound teams
// are every team of the token.
func requireInstanceScope(resolved *config.Resolved, what string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.channels) != 0 {
		return invalidRequest("this connection has channel targets, so it cannot use " + what +
			": it is instance-wide and only available on a connection without channel targets")
	}
	return nil
}

// requireInstanceTeams is the live half of the instance-wide gate. It reads the teams of the token on every
// call and admits the connection only when each of them is a bound team, so a token that later joins another
// team locks the gate instead of widening it. A failed or implausible read refuses; nothing is let through
// unverified. The refusal names no team.
func (c *Client) requireInstanceTeams(ctx context.Context, op, what string) error {
	var teams []teamJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false); err != nil {
		return err
	}
	refused := invalidRequest("the token of this connection reaches teams that the connection is not bound to, so it " +
		"cannot use " + what + ": bind every team of the token or use a token limited to the bound teams")
	if len(teams) == 0 {
		return refused
	}
	for _, t := range teams {
		if !validMattermostID(t.ID) || !c.scope.allowsTeam(t.ID) {
			return refused
		}
	}
	return nil
}

// openInstance applies the whole gate and returns a client. The local check runs before the secret is read.
func openInstance(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, what string) (*Client, error) {
	if err := requireInstanceScope(resolved, what); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requireInstanceTeams(ctx, op, what); err != nil {
		return nil, err
	}
	return client, nil
}

type botArguments struct {
	Page        int     `json:"page"`
	PerPage     int     `json:"per_page"`
	BotUserID   string  `json:"bot_user_id"`
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

func invokeBotsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list bots"
	var input botArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultListLimit
	}
	client, err := openInstance(ctx, resolved, secrets, red, op, botsAdministered)
	if err != nil {
		return nil, err
	}
	return client.ListBots(ctx, input.Page, input.PerPage)
}

// ListBots reads exactly one page of kChat's own listing of active bots and never reads on by itself.
func (c *Client) ListBots(ctx context.Context, page, perPage int) (*BotsPage, error) {
	const op = "list bots"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}}
	var bots []botJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/bots", query, nil, &bots, false); err != nil {
		return nil, err
	}
	entries := make([]Bot, 0, len(bots))
	for i := range bots {
		if !validMattermostID(bots[i].UserID) {
			continue
		}
		entries = append(entries, botOf(&bots[i]))
	}
	return &BotsPage{Bots: entries, Page: page, PerPage: perPage, Count: len(entries),
		HasMore: len(bots) >= perPage}, nil
}

// prepareBot reads the arguments, validates the bot ID and the optional fields before any secret or request,
// and applies the gate.
func prepareBot(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, raw json.RawMessage, check func(*botArguments) error) (*Client, *botArguments, error) {
	var input botArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.BotUserID) {
		return nil, nil, invalidRequest("bot_user_id must be a kChat-style identifier")
	}
	if check != nil {
		if err := check(&input); err != nil {
			return nil, nil, err
		}
	}
	client, err := openInstance(ctx, resolved, secrets, red, op, botsAdministered)
	if err != nil {
		return nil, nil, err
	}
	return client, &input, nil
}

func invokeBotsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get bot"
	client, input, err := prepareBot(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	var bot botJSON
	if err := client.do(ctx, op, http.MethodGet, botPath(input.BotUserID), nil, nil, &bot, false); err != nil {
		return nil, err
	}
	if bot.UserID != input.BotUserID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "kChat returned an invalid response"}
	}
	return botOf(&bot), nil
}

// checkBotFields validates the fields of a bot update before any request.
func checkBotFields(in *botArguments) error {
	if in.Username == nil && in.DisplayName == nil && in.Description == nil {
		return invalidRequest("give at least one of username, display_name, and description")
	}
	if in.Username != nil && !botUsernamePattern.MatchString(*in.Username) {
		return invalidRequest("username must be 3 to 22 lowercase letters, digits, dots, hyphens, and underscores")
	}
	if in.DisplayName != nil && !validChannelText(*in.DisplayName, 0, maxBotDisplayName, false) {
		return invalidRequest("display_name must be up to " + itoa(maxBotDisplayName) +
			" characters without control characters")
	}
	if in.Description != nil && !validChannelText(*in.Description, 0, maxBotDescription, true) {
		return invalidRequest("description must be up to " + itoa(maxBotDescription) +
			" characters without control characters")
	}
	return nil
}

func invokeBotsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := prepareBot(ctx, resolved, secrets, red, "update bot", raw, checkBotFields)
	if err != nil {
		return nil, err
	}
	return client.UpdateBot(ctx, input)
}

// UpdateBot sends exactly one PUT that carries only the given fields and never repeats it: a failure after
// the request may have reached kChat says so instead.
func (c *Client) UpdateBot(ctx context.Context, input *botArguments) (*Bot, error) {
	const op = "update bot"
	body := struct {
		Username    *string `json:"username,omitempty"`
		DisplayName *string `json:"display_name,omitempty"`
		Description *string `json:"description,omitempty"`
	}{input.Username, input.DisplayName, input.Description}
	var bot botJSON
	if err := c.doWith(ctx, op, http.MethodPut, botPath(input.BotUserID), nil, body, &bot, uncertainBot); err != nil {
		return nil, err
	}
	if bot.UserID != input.BotUserID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainBot}
	}
	out := botOf(&bot)
	return &out, nil
}

func invokeBotsDisable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return setBotState(ctx, resolved, secrets, red, "disable bot", "disable", raw)
}

func invokeBotsEnable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return setBotState(ctx, resolved, secrets, red, "enable bot", "enable", raw)
}

func setBotState(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, action string, raw json.RawMessage) (any, error) {
	client, input, err := prepareBot(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	return client.SetBotState(ctx, op, action, input.BotUserID)
}

// SetBotState sends exactly one body-less POST to the disable or enable endpoint and never repeats it: a
// failure after the request may have reached kChat says so instead.
func (c *Client) SetBotState(ctx context.Context, op, action, botID string) (*Bot, error) {
	uncertain := "; the bot may have been " + action + "d, read it before changing it again"
	var bot botJSON
	if err := c.doWith(ctx, op, http.MethodPost, botPath(botID)+"/"+action, nil, nil, &bot, uncertain); err != nil {
		return nil, err
	}
	if bot.UserID != botID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertain}
	}
	out := botOf(&bot)
	return &out, nil
}
