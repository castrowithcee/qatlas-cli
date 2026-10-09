package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupStickers = "stickers"

	// maxStickerBytes bounds a local sticker file; the formats Telegram accepts are far smaller.
	maxStickerBytes = 512 << 10
	maxStickerEmoji = 16
	// maxStickerSetName is the Bot API's limit for the name of a sticker set.
	maxStickerSetName = 64
	// maxCustomEmojiIDs is the Bot API's limit for getCustomEmojiStickers.
	maxCustomEmojiIDs = 200
	// maxCustomEmojiIDLen bounds one custom emoji identifier, a 64-bit number in decimal.
	maxCustomEmojiIDLen = 20
	// maxStickersOut caps the stickers of one answer; the response limit bounds what is decoded first.
	maxStickersOut             = 200
	maxStickerResponseBytes    = 256 << 10
	stickerSetNamePattern      = `^[A-Za-z0-9_]{1,64}$`
	customEmojiIDPattern       = `^[0-9]{1,20}$`
	stickerRefBindingErrorText = "the reference belongs to neither the selected chat nor the bot"
)

var stickerExtensions = map[string]bool{".webp": true, ".tgs": true, ".webm": true}

var stickerReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const stickerItemSchema = `{"type":"object","properties":{"emoji":{"type":"string"},"type":{"type":"string"},` +
	`"is_animated":{"type":"boolean"},"is_video":{"type":"boolean"},"custom_emoji_id":{"type":"string"},` +
	`"set_name":{"type":"string"},"file_unique_id":{"type":"string"},"file_ref":{"type":"string"}},` +
	`"additionalProperties":false}`

var stickerFields = "Each sticker has emoji, type (regular, mask, or custom_emoji), is_animated, is_video, " +
	"custom_emoji_id, set_name, file_unique_id, and, only when the connection binds the bot target, a file_ref " +
	"for stickers.send"

var stickersSend = capability.Descriptor{
	ID:      Provider + ".stickers.send",
	Version: 1,
	Title:   "Send a Telegram sticker",
	Description: "Send one sticker to a bound chat of an explicit Telegram connection, from a .webp, .tgs, or .webm " +
		"local_path in a directory the connection releases for reading or from a signed file_ref of this chat or of " +
		"the bot. A local file is sent under a neutral name without its path. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "stickers", "send"},
	Risk:       mediaRisk,
	Provider:   Provider,
	Group:      groupStickers,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + sourceProperties + `,` +
		`"emoji":{"type":"string","minLength":1,"maxLength":64},` +
		`"reply_to_message_id":{"type":"integer","minimum":1},"message_thread_id":{"type":"integer","minimum":1},` +
		`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		chatArgument,
		{Name: localfile.LocalPathArgument, Description: "Local sticker (.webp, .tgs, or .webm), absolute or starting " +
			"with ~/, inside a directory the connection releases for reading, up to 512 KB; instead of file_ref"},
		{Name: "file_ref", Description: "Signed file reference of a sticker, as the sticker read tools return it " +
			"(bound to the bot) or an earlier file of the selected chat; instead of " + localfile.LocalPathArgument},
		{Name: "emoji", Description: "Emoji associated with the sticker; only with local_path"},
		{Name: "reply_to_message_id", Description: "Message of the same chat to reply to"},
		{Name: "message_thread_id", Description: "Forum topic to send into"},
		{Name: "disable_notification", Description: "Send silently"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the message"},
	},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Telegram message identifier"},
		{Name: "date", Description: "Telegram send time as Unix time"},
	},
	Examples: []capability.Example{{
		Description: "Send a local sticker to this connection's chat",
		Arguments:   json.RawMessage(`{"local_path":"~/stickers/hello.webp"}`),
	}},
}

var stickersetsGet = capability.Descriptor{
	ID:          Provider + ".stickersets.get",
	Version:     1,
	Title:       "Read a Telegram sticker set",
	Description: "Read the name, title, type, and stickers of one public sticker set by its name",
	Tags:        []string{"telegram", "stickers", "read"},
	Risk:        stickerReadRisk,
	Provider:    Provider,
	Group:       groupStickers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,` +
		`"maxLength":64,"pattern":"` + stickerSetNamePattern + `"}},"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"title":{"type":"string"},` +
		`"sticker_type":{"type":"string"},"stickers":{"type":"array","items":` + stickerItemSchema +
		`}},"required":["name"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "name", Required: true,
		Description: "Sticker set name, from 1 through 64 letters, digits, or underscores"}},
	Fields: []capability.Field{
		{Name: "name", Description: "Name of the sticker set"},
		{Name: "title", Description: "Title of the sticker set"},
		{Name: "sticker_type", Description: "regular, mask, or custom_emoji"},
		{Name: "stickers", Description: stickerFields},
	},
	Examples: []capability.Example{{Description: "Read a sticker set", Arguments: json.RawMessage(`{"name":"animals_by_bot"}`)}},
}

var stickersCustomEmoji = capability.Descriptor{
	ID:          Provider + ".stickers.customemoji",
	Version:     1,
	Title:       "Read Telegram custom emoji stickers",
	Description: "Read up to 200 custom emoji stickers by their custom emoji identifiers",
	Tags:        []string{"telegram", "stickers", "emoji", "read"},
	Risk:        stickerReadRisk,
	Provider:    Provider,
	Group:       groupStickers,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"custom_emoji_ids":{"type":"array","minItems":1,` +
		`"maxItems":200,"items":{"type":"string","minLength":1,"maxLength":20,"pattern":"` + customEmojiIDPattern +
		`"}}},"required":["custom_emoji_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"stickers":{"type":"array","items":` +
		stickerItemSchema + `}},"required":["stickers"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "custom_emoji_ids", Required: true,
		Description: "From 1 through 200 custom emoji identifiers, each a string of digits"}},
	Fields:   []capability.Field{{Name: "stickers", Description: stickerFields}},
	Examples: []capability.Example{{Description: "Read two custom emoji", Arguments: json.RawMessage(`{"custom_emoji_ids":["5368324170671202286","5368324170671202287"]}`)}},
}

var topicsIconStickers = capability.Descriptor{
	ID:           Provider + ".topics.iconstickers",
	Version:      1,
	Title:        "Read Telegram forum topic icon stickers",
	Description:  "Read the stickers that may be used as forum topic icons",
	Tags:         []string{"telegram", "stickers", "topics", "read"},
	Risk:         stickerReadRisk,
	Provider:     Provider,
	Group:        groupStickers,
	InputSchema:  json.RawMessage(noArguments),
	OutputSchema: stickersCustomEmoji.OutputSchema,
	Fields:       []capability.Field{{Name: "stickers", Description: stickerFields}},
	Examples:     []capability.Example{{Description: "Read the topic icon stickers", Arguments: json.RawMessage(`{}`)}},
}

type stickerOptions struct {
	Emoji               string `json:"emoji"`
	ReplyToMessageID    int64  `json:"reply_to_message_id"`
	MessageThreadID     int64  `json:"message_thread_id"`
	DisableNotification bool   `json:"disable_notification"`
	ProtectContent      bool   `json:"protect_content"`
}

func (o stickerOptions) mediaOptions() mediaOptions {
	return mediaOptions{ReplyToMessageID: o.ReplyToMessageID, MessageThreadID: o.MessageThreadID,
		DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent}
}

func invokeStickersSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersSendWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersSendWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "send sticker"
	var arguments struct {
		Chat string `json:"chat"`
		mediaSource
		stickerOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	hasPath, hasRef := arguments.LocalPath != nil, arguments.FileRef != ""
	switch {
	case hasPath == hasRef:
		return nil, providerError(op, "exactly one of local_path and file_ref is required")
	case arguments.ReplyToMessageID < 0 || arguments.MessageThreadID < 0:
		return nil, providerError(op, "the reply and topic identifiers must be positive")
	case hasRef && arguments.Emoji != "":
		return nil, providerError(op, "emoji applies only to a local file")
	case arguments.Emoji != "" && (!utf8.ValidString(arguments.Emoji) ||
		utf8.RuneCountInString(arguments.Emoji) > maxStickerEmoji):
		return nil, providerError(op, "the emoji is not supported")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	var (
		ref    parsedRef
		upload *localfile.Upload
	)
	if hasRef {
		if ref, err = parseRef(resolved, refFile, arguments.FileRef); err != nil {
			return nil, err
		}
		// A sticker of the read tools is bound to the bot; anything else must come from the selected chat.
		if ref.binding != chat && ref.binding != botTarget {
			return nil, providerError(op, stickerRefBindingErrorText)
		}
	} else {
		if !stickerExtensions[strings.ToLower(filepath.Ext(*arguments.LocalPath))] {
			return nil, providerError(op, "a sticker file must end in .webp, .tgs, or .webm")
		}
		if upload, err = localfile.OpenForUpload(ctx, resolved, *arguments.LocalPath); err != nil {
			return nil, err
		}
		defer upload.Close()
		if upload.Size > maxStickerBytes {
			return nil, providerError(op, "a sticker file is larger than 512 KB")
		}
	}
	client, err := openChatWithHTTP(ctx, resolved, secrets, red, chat, httpClient)
	if err != nil {
		return nil, err
	}
	options := arguments.stickerOptions
	if hasRef {
		id, err := client.checkRef(ref)
		if err != nil {
			return nil, err
		}
		return client.sendSticker(ctx, mediaItem{fileID: id}, options)
	}
	data, err := io.ReadAll(upload)
	if err != nil {
		return nil, err
	}
	return client.sendSticker(ctx, mediaItem{name: neutralName(0, 1, upload.Name), data: data}, options)
}

// sendSticker performs exactly one sendSticker request; the answer is reduced to message_id and date.
func (c *Client) sendSticker(ctx context.Context, item mediaItem, o stickerOptions) (map[string]any, error) {
	const op = "send sticker"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	s := spec{op: op, method: "sendSticker", limit: defaultResponseBytes}
	var (
		raw json.RawMessage
		err error
	)
	if item.fileID != "" {
		body := struct {
			ChatID              string           `json:"chat_id"`
			Sticker             string           `json:"sticker"`
			ReplyParameters     *replyParameters `json:"reply_parameters,omitempty"`
			MessageThreadID     int64            `json:"message_thread_id,omitempty"`
			DisableNotification bool             `json:"disable_notification,omitempty"`
			ProtectContent      bool             `json:"protect_content,omitempty"`
		}{ChatID: c.target, Sticker: item.fileID, MessageThreadID: o.MessageThreadID,
			DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent}
		if o.ReplyToMessageID > 0 {
			body.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
		}
		raw, err = c.call(ctx, s, body)
	} else {
		fields := c.commonFields(o.mediaOptions())
		if o.Emoji != "" {
			fields = append(fields, field{"emoji", o.Emoji})
		}
		raw, err = c.withUploadTimeout().callMultipart(ctx, s, fields,
			[]filePart{{field: "sticker", name: item.name, data: item.data}})
	}
	if err != nil {
		return nil, err
	}
	sent, ok := c.parseSent(raw, "sticker")
	if !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return sent, nil
}

// tgSticker holds the allow-listed fields of a Bot API Sticker; everything else is dropped on decoding.
type tgSticker struct {
	FileID        string `json:"file_id"`
	FileUniqueID  string `json:"file_unique_id"`
	Type          string `json:"type"`
	IsAnimated    bool   `json:"is_animated"`
	IsVideo       bool   `json:"is_video"`
	Emoji         string `json:"emoji"`
	SetName       string `json:"set_name"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type stickerOut struct {
	Emoji         string `json:"emoji,omitempty"`
	Type          string `json:"type,omitempty"`
	IsAnimated    bool   `json:"is_animated"`
	IsVideo       bool   `json:"is_video"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
	SetName       string `json:"set_name,omitempty"`
	FileUniqueID  string `json:"file_unique_id,omitempty"`
	FileRef       string `json:"file_ref,omitempty"`
}

type stickersOut struct {
	Stickers []stickerOut `json:"stickers"`
}

type stickerSetOut struct {
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	StickerType string       `json:"sticker_type,omitempty"`
	Stickers    []stickerOut `json:"stickers"`
}

func digitsOnly(value string) bool {
	if value == "" || len(value) > maxCustomEmojiIDLen {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validStickerSetName(name string) bool {
	if name == "" || len(name) > maxStickerSetName {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// stickersOf reduces provider stickers to the allow-list. A file_ref exists only when the connection binds
// the bot target, because only such a connection accepts a bot-bound reference.
func (c *Client) stickersOf(in []tgSticker) []stickerOut {
	if len(in) > maxStickersOut {
		in = in[:maxStickersOut]
	}
	out := make([]stickerOut, 0, len(in))
	for _, s := range in {
		item := stickerOut{Emoji: capText(s.Emoji, maxNameRunes), Type: capText(s.Type, maxNameRunes),
			IsAnimated: s.IsAnimated, IsVideo: s.IsVideo, SetName: capText(s.SetName, maxNameRunes),
			FileUniqueID: capText(s.FileUniqueID, maxNameRunes)}
		if digitsOnly(s.CustomEmojiID) {
			item.CustomEmojiID = s.CustomEmojiID
		}
		if c.set.bot {
			item.FileRef = signRef(c.token, refFile, botTarget, s.FileID)
		}
		out = append(out, item)
	}
	return out
}

func invokeStickersetsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsGetWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsGetWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get sticker set"
	var arguments struct {
		Name string `json:"name"`
	}
	if err := decodeStrict(raw, &arguments); err != nil || !validStickerSetName(arguments.Name) {
		return nil, providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	client, err := openReader(ctx, op, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	answer, err := client.call(ctx, spec{op: op, method: "getStickerSet", limit: maxStickerResponseBytes, readOnly: true},
		struct {
			Name string `json:"name"`
		}{arguments.Name})
	if err != nil {
		return nil, err
	}
	var set struct {
		Name        string      `json:"name"`
		Title       string      `json:"title"`
		StickerType string      `json:"sticker_type"`
		Stickers    []tgSticker `json:"stickers"`
	}
	if json.Unmarshal(answer, &set) != nil || set.Name == "" {
		return nil, invalidResponse(op)
	}
	return stickerSetOut{Name: capText(set.Name, maxNameRunes), Title: capText(set.Title, maxNameRunes),
		StickerType: capText(set.StickerType, maxNameRunes), Stickers: client.stickersOf(set.Stickers)}, nil
}

func invokeStickersCustomEmoji(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersCustomEmojiWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersCustomEmojiWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get custom emoji stickers"
	var arguments struct {
		IDs []string `json:"custom_emoji_ids"`
	}
	if err := decodeStrict(raw, &arguments); err != nil || len(arguments.IDs) < 1 || len(arguments.IDs) > maxCustomEmojiIDs {
		return nil, providerError(op, "from 1 through 200 custom emoji identifiers are required")
	}
	for _, id := range arguments.IDs {
		if !digitsOnly(id) {
			return nil, providerError(op, "a custom emoji identifier must be a string of digits")
		}
	}
	client, err := openReader(ctx, op, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.readStickers(ctx, spec{op: op, method: "getCustomEmojiStickers", limit: maxStickerResponseBytes,
		readOnly: true}, struct {
		IDs []string `json:"custom_emoji_ids"`
	}{arguments.IDs})
}

func invokeTopicsIconStickers(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTopicsIconStickersWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeTopicsIconStickersWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get topic icon stickers"
	var arguments struct{}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, err := openReader(ctx, op, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.readStickers(ctx, spec{op: op, method: "getForumTopicIconStickers", limit: maxStickerResponseBytes,
		readOnly: true}, struct{}{})
}

// openReader opens a client for a catalog read, which addresses no chat.
func openReader(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, httpClient *http.Client) (*Client, error) {
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	return openWithHTTP(ctx, resolved, secrets, red, httpClient)
}

func (c *Client) readStickers(ctx context.Context, s spec, payload any) (stickersOut, error) {
	answer, err := c.call(ctx, s, payload)
	if err != nil {
		return stickersOut{}, err
	}
	var stickers []tgSticker
	if json.Unmarshal(answer, &stickers) != nil {
		return stickersOut{}, invalidResponse(s.op)
	}
	return stickersOut{Stickers: c.stickersOf(stickers)}, nil
}
