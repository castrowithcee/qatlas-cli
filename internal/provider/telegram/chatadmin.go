package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxTitleRunes       = 128
	maxDescriptionRunes = 255
)

func chatAdminRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{
		Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	}
}

var chatsSetTitle = capability.Descriptor{
	ID:          Provider + ".chats.settitle",
	Version:     1,
	Title:       "Set a Telegram chat title",
	Description: "Change the title of a bound group or channel of an explicit Telegram connection",
	Tags:        []string{"telegram", "chats", "title"},
	Risk:        chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"title":{"type":"string","minLength":1,"maxLength":128}},"required":["title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "title", Description: "New chat title, from 1 through 128 characters", Required: true},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the change"}},
	Examples: []capability.Example{{
		Description: "Rename this connection's chat",
		Arguments:   json.RawMessage(`{"title":"Team updates"}`),
	}},
}

var chatsSetDescription = capability.Descriptor{
	ID:          Provider + ".chats.setdescription",
	Version:     1,
	Title:       "Set a Telegram chat description",
	Description: "Change or remove the description of a bound group or channel of an explicit Telegram connection",
	Tags:        []string{"telegram", "chats", "description"},
	Risk:        chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	Group:       groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"description":{"type":"string","maxLength":255}},"required":["description"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: "description", Description: "New description, up to 255 characters; an empty string removes it",
			Required: true},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the change"}},
	Examples: []capability.Example{{
		Description: "Set the description of this connection's chat",
		Arguments:   json.RawMessage(`{"description":"Release announcements"}`),
	}},
}

var chatsSetPhoto = capability.Descriptor{
	ID:      Provider + ".chats.setphoto",
	Version: 1,
	Title:   "Set a Telegram chat photo",
	Description: "Replace the photo of a bound group or channel of an explicit Telegram connection with a local " +
		"image up to 10 MB in a directory the connection releases for reading. The file is sent under a neutral " +
		"name without its path. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "chats", "photo", "files"},
	Risk:       chatAdminRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:   Provider,
	Group:      groupChats,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local photo, absolute or starting with " +
			"~/, inside a directory the connection releases for reading, up to 10 MB"},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the photo"}},
	Examples: []capability.Example{{
		Description: "Set the photo of this connection's chat",
		Arguments:   json.RawMessage(`{"local_path":"~/images/logo.jpg"}`),
	}},
}

var chatsDeletePhoto = capability.Descriptor{
	ID:          Provider + ".chats.deletephoto",
	Version:     1,
	Title:       "Delete a Telegram chat photo",
	Description: "Remove the photo of a bound group or channel of an explicit Telegram connection",
	Tags:        []string{"telegram", "chats", "photo", "delete"},
	Risk:        chatAdminRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:    Provider, RequiresToolAllowList: true,
	Group: groupChats,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the removal"}},
	Examples: []capability.Example{{
		Description: "Remove the photo of this connection's chat",
		Arguments:   json.RawMessage(`{}`),
	}},
}

func invokeChatsSetTitle(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat  string `json:"chat"`
		Title string `json:"title"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("set chat title", "the validated arguments could not be read")
	}
	if err := checkRunes("set chat title", "title", arguments.Title, 1, maxTitleRunes); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetChatTitle(ctx, arguments.Title)
}

func invokeChatsSetDescription(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat        string  `json:"chat"`
		Description *string `json:"description"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("set chat description", "the validated arguments could not be read")
	}
	// A missing description is refused: it would silently remove the current one.
	if arguments.Description == nil {
		return nil, providerError("set chat description", "the description is required; use an empty string to remove it")
	}
	if err := checkRunes("set chat description", "description", *arguments.Description, 0,
		maxDescriptionRunes); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SetChatDescription(ctx, *arguments.Description)
}

func invokeChatsSetPhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeChatsSetPhotoWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeChatsSetPhotoWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set chat photo"
	var arguments struct {
		Chat      string  `json:"chat"`
		LocalPath *string `json:"local_path"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.LocalPath == nil {
		return nil, providerError(op, "local_path is required")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	// The release check and the size check happen before the credential is resolved or any byte is read.
	prepared, err := prepareSources(ctx, resolved, op, chat, []string{kindPhoto}, []mediaSource{{LocalPath: arguments.LocalPath}})
	if err != nil {
		return nil, err
	}
	defer closeUploads(prepared)
	client, err := openChatWithHTTP(ctx, resolved, secrets, red, chat, httpClient)
	if err != nil {
		return nil, err
	}
	items, err := client.load(op, prepared)
	if err != nil {
		return nil, err
	}
	return client.SetChatPhoto(ctx, items[0])
}

func invokeChatsDeletePhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("delete chat photo", "the validated arguments could not be read")
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.DeleteChatPhoto(ctx)
}

func checkRunes(op, name, value string, min, max int) error {
	if n := utf8.RuneCountInString(value); !utf8.ValidString(value) || n < min || n > max {
		return providerError(op, "the "+name+" length is outside the allowed range")
	}
	return nil
}

// SetChatTitle performs exactly one setChatTitle request without retrying an ambiguous result.
func (c *Client) SetChatTitle(ctx context.Context, title string) (map[string]any, error) {
	const op = "set chat title"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkRunes(op, "title", title, 1, maxTitleRunes); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatTitle", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
		Title  string `json:"title"`
	}{ChatID: c.target, Title: title})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// SetChatDescription performs exactly one setChatDescription request. An empty description removes it, so the
// field is always sent.
func (c *Client) SetChatDescription(ctx context.Context, description string) (map[string]any, error) {
	const op = "set chat description"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if err := checkRunes(op, "description", description, 0, maxDescriptionRunes); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "setChatDescription", limit: defaultResponseBytes}, struct {
		ChatID      string `json:"chat_id"`
		Description string `json:"description"`
	}{ChatID: c.target, Description: description})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// SetChatPhoto performs exactly one multipart setChatPhoto request with the already opened local file.
func (c *Client) SetChatPhoto(ctx context.Context, item mediaItem) (map[string]any, error) {
	const op = "set chat photo"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if len(item.data) == 0 || len(item.data) > maxPhotoBytes {
		return nil, providerError(op, "the photo is empty or larger than Telegram accepts")
	}
	s := spec{op: op, method: "setChatPhoto", limit: defaultResponseBytes}
	raw, err := c.withUploadTimeout().callMultipart(ctx, s, []field{{"chat_id", c.target}},
		[]filePart{{field: kindPhoto, name: item.name, data: item.data}})
	if err != nil {
		return nil, err
	}
	var ok bool
	if json.Unmarshal(raw, &ok) != nil || !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"updated": true}, nil
}

// DeleteChatPhoto performs exactly one deleteChatPhoto request.
func (c *Client) DeleteChatPhoto(ctx context.Context) (map[string]any, error) {
	const op = "delete chat photo"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	err := c.confirmed(ctx, spec{op: op, method: "deleteChatPhoto", limit: defaultResponseBytes}, struct {
		ChatID string `json:"chat_id"`
	}{ChatID: c.target})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
