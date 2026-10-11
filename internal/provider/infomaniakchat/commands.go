package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Local bounds of the slash command fields this provider writes, the refusals of a command outside the
// boundary, and the suffixes of a change whose result is unclear. The refusals name neither the command ID
// nor a foreign team.
const (
	maxCommandDisplayName = 64
	maxCommandText        = 128

	commandOutOfScope  = "command_id belongs to a team outside the targets of this connection"
	commandTeamWide    = "slash commands are team-wide and have no channel; a connection with a channel allow-list does not reach them"
	uncertainCmdUpdate = "; the command may have been changed, read it before changing it again"
	uncertainCmdDelete = "; the command may have been deleted, read the commands before deleting it again"
)

var commandIDArgument = capability.Argument{Name: "command_id", Required: true,
	Description: "Slash command identifier from commands.list"}

var commandSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"trigger":{"type":"string"},"method":{"type":"string"},` +
	`"display_name":{"type":"string"},"description":{"type":"string"},"callback_origin":{"type":"string"},` +
	`"username":{"type":"string"},"icon_url":{"type":"string"},"auto_complete":{"type":"boolean"},` +
	`"auto_complete_desc":{"type":"string"},"auto_complete_hint":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["team_id","trigger","method","display_name","description","auto_complete"],` +
	`"additionalProperties":false}`

var commandFields = []capability.Field{
	{Name: "id", Description: "Command identifier; only listed by commands.list, absent elsewhere because the " +
		"caller named it"},
	{Name: "team_id", Description: "Team of the command"},
	{Name: "trigger", Description: "Word that triggers the command, without the slash, untrusted data"},
	{Name: "method", Description: "HTTP method of the callback, P or G, untrusted data"},
	{Name: "display_name", Description: "Display name, untrusted data"},
	{Name: "description", Description: "Description, untrusted data"},
	{Name: "callback_origin", Description: "Scheme and host of the callback URL, untrusted data; path, query, and " +
		"credentials are never returned, and an unreadable URL is left out"},
	{Name: "username", Description: "Username the command posts as when it overrides the sender, untrusted data"},
	{Name: "icon_url", Description: "Profile picture URL the command posts with, untrusted data; never fetched"},
	{Name: "auto_complete", Description: "True when the command is offered while typing"},
	{Name: "auto_complete_desc", Description: "Description shown with the suggestion, untrusted data"},
	{Name: "auto_complete_hint", Description: "Argument hint shown with the suggestion, untrusted data"},
	{Name: "created_at", Description: "Creation time, RFC 3339 in UTC"},
	{Name: "updated_at", Description: "Last change time, RFC 3339 in UTC"},
}

var commandsList = capability.Descriptor{
	ID:      Provider + ".commands.list",
	Version: 1,
	Title:   "List Infomaniak kChat slash commands",
	Description: "List the custom slash commands of one team this connection is bound to, one window at a time; " +
		"only for a connection without a channel allow-list; callback URLs appear only as origins and tokens never",
	Tags:                  []string{"infomaniak", "kchat", "slash-commands", "integrations", "list"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"team_id":` + idSchema + `,"commands":{"type":"array","items":` + commandSchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},` +
		`"count":{"type":"integer"}},` +
		`"required":["team_id","commands","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "page", Description: "1-based page of this provider's own window; the first page when omitted"},
		{Name: "limit", Description: "Commands per page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, commandFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Number of pages of the filtered list"},
		capability.Field{Name: "total", Description: "Number of commands of the filtered list"},
		capability.Field{Name: "count", Description: "Number of commands reported on this page"},
	),
	Examples: []capability.Example{{Description: "List the custom slash commands of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var commandsGet = capability.Descriptor{
	ID:      Provider + ".commands.get",
	Version: 1,
	Title:   "Get an Infomaniak kChat slash command",
	Description: "Read exactly one active custom slash command of a bound team; only for a connection without a " +
		"channel allow-list; the callback URL appears only as origin and the token is never returned",
	Tags:                  []string{"infomaniak", "kchat", "slash-commands", "integrations", "get"},
	Risk:                  hookRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"command_id":` + idSchema + `},` +
		`"required":["command_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(commandSchema),
	Arguments:    []capability.Argument{commandIDArgument},
	Fields:       commandFields,
	Examples: []capability.Example{{Description: "Read one slash command",
		Arguments: json.RawMessage(`{"command_id":"abc123cmd000000000000000000"}`)}},
}

var commandsUpdate = capability.Descriptor{
	ID:      Provider + ".commands.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat slash command",
	Description: "Change the display name, description, or suggestion texts of exactly one active custom slash " +
		"command of a bound team with one confirmed request; only for a connection without a channel " +
		"allow-list; callback URL, method, trigger, and the other fields stay as read, and only the given " +
		"fields change",
	Tags:                  []string{"infomaniak", "kchat", "slash-commands", "integrations", "update"},
	Risk:                  hookRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"command_id":` + idSchema + `,` +
		`"display_name":{"type":"string","maxLength":` + itoa(maxCommandDisplayName) + `},` +
		`"description":{"type":"string","maxLength":` + itoa(maxCommandText) + `},` +
		`"auto_complete":{"type":"boolean"},` +
		`"auto_complete_desc":{"type":"string","maxLength":` + itoa(maxCommandText) + `},` +
		`"auto_complete_hint":{"type":"string","maxLength":` + itoa(maxCommandText) + `}},` +
		`"required":["command_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(commandSchema),
	Arguments: []capability.Argument{commandIDArgument,
		{Name: "display_name", Description: "New display name, up to " + itoa(maxCommandDisplayName) +
			" characters; empty clears it"},
		{Name: "description", Description: "New description, up to " + itoa(maxCommandText) +
			" characters; empty clears it"},
		{Name: "auto_complete", Description: "Whether the command is offered while typing"},
		{Name: "auto_complete_desc", Description: "New suggestion description, up to " + itoa(maxCommandText) +
			" characters; empty clears it"},
		{Name: "auto_complete_hint", Description: "New argument hint, up to " + itoa(maxCommandText) +
			" characters; empty clears it; at least one of the five fields is required"}},
	Fields: commandFields,
	Examples: []capability.Example{{Description: "Change the description of one slash command",
		Arguments: json.RawMessage(`{"command_id":"abc123cmd000000000000000000","description":"Deploy status"}`)}},
}

var commandsDelete = capability.Descriptor{
	ID:      Provider + ".commands.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat slash command",
	Description: "Delete exactly one active custom slash command of a bound team with one confirmed request; only " +
		"for a connection without a channel allow-list; it stops working and cannot be restored",
	Tags:                  []string{"infomaniak", "kchat", "slash-commands", "integrations", "delete"},
	Risk:                  hookRisk(capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"command_id":` + idSchema + `},` +
		`"required":["command_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{commandIDArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True once kChat confirmed the deletion"}},
	Examples: []capability.Example{{Description: "Delete one slash command",
		Arguments: json.RawMessage(`{"command_id":"abc123cmd000000000000000000"}`)}},
}

// commandJSON is the subset of the kChat Command resource this provider reads. The token is deliberately
// not decoded: it can reach no result.
type commandJSON struct {
	ID               string `json:"id"`
	TeamID           string `json:"team_id"`
	Trigger          string `json:"trigger"`
	Method           string `json:"method"`
	URL              string `json:"url"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
	Username         string `json:"username"`
	IconURL          string `json:"icon_url"`
	AutoComplete     bool   `json:"auto_complete"`
	AutoCompleteDesc string `json:"auto_complete_desc"`
	AutoCompleteHint string `json:"auto_complete_hint"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
}

// SlashCommand is the stable Qatlas view of one custom slash command. The ID is set only by a listing.
type SlashCommand struct {
	ID               string `json:"id,omitempty"`
	TeamID           string `json:"team_id"`
	Trigger          string `json:"trigger"`
	Method           string `json:"method"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
	CallbackOrigin   string `json:"callback_origin,omitempty"`
	Username         string `json:"username,omitempty"`
	IconURL          string `json:"icon_url,omitempty"`
	AutoComplete     bool   `json:"auto_complete"`
	AutoCompleteDesc string `json:"auto_complete_desc,omitempty"`
	AutoCompleteHint string `json:"auto_complete_hint,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// SlashCommandsPage is one window of the custom slash commands of one team.
type SlashCommandsPage struct {
	TeamID   string         `json:"team_id"`
	Commands []SlashCommand `json:"commands"`
	Page     int            `json:"page"`
	Pages    int            `json:"pages"`
	Total    int            `json:"total"`
	Count    int            `json:"count"`
}

// SlashCommandDeleted is the answer of one confirmed deletion.
type SlashCommandDeleted struct {
	Deleted bool `json:"deleted"`
}

func slashCommandOf(cmd *commandJSON, withID bool) SlashCommand {
	origin, _ := callbackOrigin(cmd.URL)
	out := SlashCommand{TeamID: cmd.TeamID, Trigger: bounded(cmd.Trigger), Method: bounded(cmd.Method),
		DisplayName: bounded(cmd.DisplayName), Description: bounded(cmd.Description), CallbackOrigin: origin,
		Username: bounded(cmd.Username), IconURL: bounded(cmd.IconURL), AutoComplete: cmd.AutoComplete,
		AutoCompleteDesc: bounded(cmd.AutoCompleteDesc), AutoCompleteHint: bounded(cmd.AutoCompleteHint),
		CreatedAt: msToRFC3339(cmd.CreateAt), UpdatedAt: msToRFC3339(cmd.UpdateAt)}
	if withID {
		out.ID = cmd.ID
	}
	return out
}

func commandPath(commandID string) string {
	return "/api/v4/commands/" + url.PathEscape(commandID)
}

// requireTeamWide refuses every command tool on a connection with a channel allow-list: a slash command
// belongs to a team and has no channel to bind it to.
func requireTeamWide(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.channels) != 0 {
		return invalidRequest(commandTeamWide)
	}
	return nil
}

type commandArguments struct {
	CommandID        string  `json:"command_id"`
	DisplayName      *string `json:"display_name"`
	Description      *string `json:"description"`
	AutoComplete     *bool   `json:"auto_complete"`
	AutoCompleteDesc *string `json:"auto_complete_desc"`
	AutoCompleteHint *string `json:"auto_complete_hint"`
}

func checkCommandTexts(in *commandArguments) error {
	if in.DisplayName == nil && in.Description == nil && in.AutoComplete == nil && in.AutoCompleteDesc == nil &&
		in.AutoCompleteHint == nil {
		return invalidRequest("give at least one of display_name, description, auto_complete, " +
			"auto_complete_desc, and auto_complete_hint")
	}
	for _, f := range []struct {
		name  string
		value *string
		max   int
	}{{"display_name", in.DisplayName, maxCommandDisplayName}, {"description", in.Description, maxCommandText},
		{"auto_complete_desc", in.AutoCompleteDesc, maxCommandText},
		{"auto_complete_hint", in.AutoCompleteHint, maxCommandText}} {
		if f.value != nil && !validChannelText(*f.value, 0, f.max, false) {
			return invalidRequest(f.name + " must be up to " + itoa(f.max) + " characters without control characters")
		}
	}
	return nil
}

// prepareCommand refuses a channel allow-list, validates the command ID, and registers it with the
// redactor before any request, then opens the client.
func prepareCommand(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, raw json.RawMessage, check func(*commandArguments) error) (*Client, *commandArguments, error) {
	var input commandArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, providerError(op, "the validated arguments could not be read")
	}
	if err := requireTeamWide(resolved); err != nil {
		return nil, nil, err
	}
	if !validMattermostID(input.CommandID) {
		return nil, nil, invalidRequest("command_id must be a kChat-style identifier")
	}
	if red != nil {
		red.Add(input.CommandID)
	}
	if check != nil {
		if err := check(&input); err != nil {
			return nil, nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	return client, &input, nil
}

type commandListArguments struct {
	TeamID string `json:"team_id"`
	Page   int    `json:"page"`
	Limit  int    `json:"limit"`
}

func invokeCommandsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input commandListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list slash commands", "the validated arguments could not be read")
	}
	if err := requireTeamWide(resolved); err != nil {
		return nil, err
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListSlashCommands(ctx, input.TeamID, input.Page, input.Limit)
}

// ListSlashCommands reads the custom commands of one bound team, which kChat answers without pagination, and
// pages the result itself. A command reported for another team, or deleted, is dropped.
func (c *Client) ListSlashCommands(ctx context.Context, teamID string, page, limit int) (*SlashCommandsPage, error) {
	const op = "list slash commands"
	query := url.Values{"team_id": {teamID}, "custom_only": {"true"}}
	var commands []commandJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/commands", query, nil, &commands, false); err != nil {
		return nil, err
	}
	entries := make([]SlashCommand, 0, len(commands))
	for i := range commands {
		cmd := &commands[i]
		if validMattermostID(cmd.ID) && cmd.TeamID == teamID && cmd.DeleteAt == 0 {
			entries = append(entries, slashCommandOf(cmd, true))
		}
	}
	window, pages, total := windowOf(entries, page, limit)
	return &SlashCommandsPage{TeamID: teamID, Commands: window, Page: page, Pages: pages, Total: total,
		Count: len(window)}, nil
}

func invokeCommandsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get slash command"
	client, input, err := prepareCommand(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	cmd, _, err := client.boundCommand(ctx, op, input.CommandID)
	if err != nil {
		return nil, err
	}
	out := slashCommandOf(cmd, false)
	return &out, nil
}

// boundCommand reads one command and binds it: it must be the one asked for and not deleted, and its team
// must be bound. It also returns the object exactly as read, so an update can write it back without
// decoding the token. It is the one gate every command tool passes before it returns or changes anything.
func (c *Client) boundCommand(ctx context.Context, op, commandID string) (*commandJSON, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, http.MethodGet, commandPath(commandID), nil, nil, &raw, false); err != nil {
		return nil, nil, err
	}
	var cmd commandJSON
	if err := json.Unmarshal(raw, &cmd); err != nil || cmd.ID != commandID {
		return nil, nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	if cmd.DeleteAt != 0 {
		return nil, nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "kChat does not hold this resource or does not show it to this token"}
	}
	if !c.scope.allowsTeam(cmd.TeamID) {
		return nil, nil, invalidRequest(commandOutOfScope)
	}
	return &cmd, raw, nil
}

func invokeCommandsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update slash command"
	client, input, err := prepareCommand(ctx, resolved, secrets, red, op, raw, checkCommandTexts)
	if err != nil {
		return nil, err
	}
	cmd, current, err := client.boundCommand(ctx, op, input.CommandID)
	if err != nil {
		return nil, err
	}
	return client.UpdateSlashCommand(ctx, cmd, current, input)
}

// UpdateSlashCommand sends exactly one PUT that writes the command back as read, with only the given text
// and auto-complete fields replaced, and never repeats it: a failure after the request may have reached
// kChat says so instead. The object is kept raw, so the URL, method, trigger, token, and any field this
// provider does not know are carried over unchanged.
func (c *Client) UpdateSlashCommand(ctx context.Context, cmd *commandJSON, current json.RawMessage,
	input *commandArguments) (*SlashCommand, error) {
	const op = "update slash command"
	body := map[string]json.RawMessage{}
	if err := json.Unmarshal(current, &body); err != nil || body == nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	for key, value := range map[string]any{"display_name": input.DisplayName, "description": input.Description,
		"auto_complete_desc": input.AutoCompleteDesc, "auto_complete_hint": input.AutoCompleteHint} {
		if text := value.(*string); text != nil {
			body[key], _ = json.Marshal(*text)
		}
	}
	if input.AutoComplete != nil {
		body["auto_complete"], _ = json.Marshal(*input.AutoComplete)
	}
	var updated commandJSON
	if err := c.doWith(ctx, op, http.MethodPut, commandPath(cmd.ID), nil, body, &updated, uncertainCmdUpdate); err != nil {
		return nil, err
	}
	if updated.ID != cmd.ID || updated.TeamID != cmd.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainCmdUpdate}
	}
	out := slashCommandOf(&updated, false)
	return &out, nil
}

func invokeCommandsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete slash command"
	client, input, err := prepareCommand(ctx, resolved, secrets, red, op, raw, nil)
	if err != nil {
		return nil, err
	}
	cmd, _, err := client.boundCommand(ctx, op, input.CommandID)
	if err != nil {
		return nil, err
	}
	return client.DeleteSlashCommand(ctx, cmd.ID)
}

// DeleteSlashCommand sends exactly one DELETE and never repeats it: a failure after the request may have
// reached kChat says so instead. kChat answers with a status only.
func (c *Client) DeleteSlashCommand(ctx context.Context, commandID string) (*SlashCommandDeleted, error) {
	const op = "delete slash command"
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodDelete, commandPath(commandID), nil, nil, &answer,
		uncertainCmdDelete); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainCmdDelete}
	}
	return &SlashCommandDeleted{Deleted: true}, nil
}
