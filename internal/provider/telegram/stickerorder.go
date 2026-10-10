package telegram

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxStickerPosition is one below Telegram's largest set, a custom emoji set of 200 stickers; the handler also
// checks the size of the set it reads.
const maxStickerPosition = 199

const stickerFileRefSchema = `{"type":"string","minLength":1,"maxLength":2048}`

const stickerRefHelp = "file_ref of the sticker as stickersets.get returns it for a connection that binds the bot target"

var (
	stickersSetPosition = capability.Descriptor{
		ID:      Provider + ".stickers.setposition",
		Version: 1,
		Title:   "Move a sticker in a Telegram sticker set",
		Description: "Move one sticker of a sticker set of the bot to a zero-based position. The name must end in _by_ " +
			"and the bot's username and the sticker must belong to that set. Needs the bot target. An unclear " +
			"outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,"file_ref":` +
			stickerFileRefSchema + `,"position":{"type":"integer","minimum":0,"maximum":199}},` +
			`"required":["name","file_ref","position"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"position":{"type":"integer"}},"required":["name","position"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "file_ref", Required: true, Description: "The sticker to move, " + stickerRefHelp},
			{Name: "position", Required: true, Description: "New zero-based position, below the number of stickers in the set"},
		},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the sticker set"},
			{Name: "position", Description: "Position Telegram accepted"},
		},
		Examples: []capability.Example{{Description: "Move a sticker to the front of its set",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","file_ref":"<file_ref of stickersets.get>","position":0}`)}},
	}

	stickersReplace = capability.Descriptor{
		ID:      Provider + ".stickers.replace",
		Version: 1,
		Title:   "Replace a sticker in a Telegram sticker set",
		Description: "Replace one sticker of a sticker set of the bot by an uploaded sticker, keeping the old one's " +
			"position. The name must end in _by_ and the bot's username and the old sticker must belong to that set. " +
			"Needs the bot target. An unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + userIDSchema + `,` + stickerSetNameSchema + `,` +
			`"old_file_ref":` + stickerFileRefSchema + `,"sticker":` + inputStickerSchema + `},` +
			`"required":["user_id","name","old_file_ref","sticker"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"replaced":{"type":"boolean"}},"required":["name","replaced"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "user_id", Required: true, Description: "Telegram user identifier who owns the sticker set"},
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "old_file_ref", Required: true, Description: "The sticker to replace, " + stickerRefHelp},
			{Name: "sticker", Required: true, Description: "The new sticker, " + inputStickerHelp},
		},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the sticker set"},
			{Name: "replaced", Description: "True when Telegram accepted the replacement"},
		},
		Examples: []capability.Example{{Description: "Replace a sticker by an uploaded one",
			Arguments: json.RawMessage(`{"user_id":42,"name":"animals_by_mybot","old_file_ref":"<file_ref of stickersets.get>",` +
				`"sticker":{"file_ref":"<file_ref of stickers.uploadfile>","format":"static","emoji_list":["🐱"]}}`)}},
	}

	stickersDelete = capability.Descriptor{
		ID:      Provider + ".stickers.delete",
		Version: 1,
		Title:   "Remove a sticker from a Telegram sticker set",
		Description: "Remove one sticker from a sticker set of the bot for good; this cannot be undone. The name must " +
			"end in _by_ and the bot's username and the sticker must belong to that set. Needs the bot target. An " +
			"unclear outcome is never repeated",
		Tags:                  []string{"telegram", "stickers", "delete"},
		Risk:                  stickerSetMutationRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		Provider:              Provider,
		RequiresToolAllowList: true,
		Group:                 groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,"file_ref":` +
			stickerFileRefSchema + `},"required":["name","file_ref"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"deleted":{"type":"boolean"}},"required":["name","deleted"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "file_ref", Required: true, Description: "The sticker to remove, " + stickerRefHelp},
		},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the sticker set"},
			{Name: "deleted", Description: "True when Telegram accepted the removal"},
		},
		Examples: []capability.Example{{Description: "Remove a sticker from a set",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","file_ref":"<file_ref of stickersets.get>"}`)}},
	}
)

// botFileRef checks format, kind, and binding of a sticker reference without any credential. A chat-bound
// reference is refused: only files of the bot's own sets are addressed.
func botFileRef(op string, resolved *config.Resolved, value string) (parsedRef, error) {
	ref, err := parseRef(resolved, refFile, value)
	if err != nil {
		return parsedRef{}, err
	}
	if ref.binding != botTarget {
		return parsedRef{}, providerError(op, "the sticker file_ref must be bound to the bot, as stickersets.get returns it")
	}
	return ref, nil
}

// ownSticker admits a sticker only when it lies in the named own set of the bot and returns the size of the
// set. The Bot API methods on a sticker do not name its set, so this read-only check is the only thing that
// ties them to it. It matches the file identifier first and falls back to the stable unique identifier, which
// costs one getFile read. Nothing is echoed on a mismatch.
func (c *Client) ownSticker(ctx context.Context, op, name, fileID string) (int, error) {
	if err := c.ownStickerSet(ctx, op, name); err != nil {
		return 0, err
	}
	answer, err := c.call(ctx, spec{op: op, method: "getStickerSet", limit: maxStickerResponseBytes, readOnly: true},
		struct {
			Name string `json:"name"`
		}{name})
	if err != nil {
		return 0, err
	}
	var set struct {
		Stickers []struct {
			FileID       string `json:"file_id"`
			FileUniqueID string `json:"file_unique_id"`
		} `json:"stickers"`
	}
	if json.Unmarshal(answer, &set) != nil {
		return 0, invalidResponse(op)
	}
	for _, s := range set.Stickers {
		if s.FileID == fileID {
			return len(set.Stickers), nil
		}
	}
	info, err := c.getFileByID(ctx, op, fileID)
	if err != nil {
		return 0, err
	}
	for _, s := range set.Stickers {
		if s.FileUniqueID != "" && s.FileUniqueID == info.UniqueID {
			return len(set.Stickers), nil
		}
	}
	return 0, providerError(op, "the sticker is not part of the sticker set")
}

// stickerTarget runs everything that needs no request for one existing sticker: the set name, the bot target,
// and the reference. The credential is read afterwards.
func stickerTarget(op string, resolved *config.Resolved, name, fileRef string) (parsedRef, error) {
	if !validStickerSetName(name) {
		return parsedRef{}, providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	if resolved == nil {
		return parsedRef{}, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return parsedRef{}, err
	}
	return botFileRef(op, resolved, fileRef)
}

func invokeStickersSetPosition(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersSetPositionWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersSetPositionWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "move sticker in set"
	var arguments struct {
		Name     string `json:"name"`
		FileRef  string `json:"file_ref"`
		Position int    `json:"position"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.Position < 0 || arguments.Position > maxStickerPosition {
		return nil, providerError(op, "the position must be from 0 through 199")
	}
	ref, err := stickerTarget(op, resolved, arguments.Name, arguments.FileRef)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	fileID, err := client.checkRef(ref)
	if err != nil {
		return nil, err
	}
	size, err := client.ownSticker(ctx, op, arguments.Name, fileID)
	if err != nil {
		return nil, err
	}
	if arguments.Position >= size {
		return nil, providerError(op, "the position must be below the number of stickers in the set")
	}
	body := struct {
		Sticker  string `json:"sticker"`
		Position int    `json:"position"`
	}{fileID, arguments.Position}
	if err := client.mutateStickerSet(ctx, op, "setStickerPositionInSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "position": arguments.Position}, nil
}

func invokeStickersReplace(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersReplaceWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersReplaceWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "replace sticker in set"
	var arguments struct {
		UserID     int64           `json:"user_id"`
		Name       string          `json:"name"`
		OldFileRef string          `json:"old_file_ref"`
		Sticker    inputStickerArg `json:"sticker"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validUserID(op, arguments.UserID); err != nil {
		return nil, err
	}
	oldRef, err := stickerTarget(op, resolved, arguments.Name, arguments.OldFileRef)
	if err != nil {
		return nil, err
	}
	newRef, err := checkInputSticker(op, resolved, arguments.Sticker)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	oldID, err := client.checkRef(oldRef)
	if err != nil {
		return nil, err
	}
	sticker, err := client.inputStickerBody(arguments.Sticker, newRef)
	if err != nil {
		return nil, err
	}
	if _, err := client.ownSticker(ctx, op, arguments.Name, oldID); err != nil {
		return nil, err
	}
	body := struct {
		UserID     int64            `json:"user_id"`
		Name       string           `json:"name"`
		OldSticker string           `json:"old_sticker"`
		Sticker    inputStickerBody `json:"sticker"`
	}{arguments.UserID, arguments.Name, oldID, sticker}
	if err := client.mutateStickerSet(ctx, op, "replaceStickerInSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "replaced": true}, nil
}

func invokeStickersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersDeleteWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersDeleteWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "remove sticker from set"
	var arguments struct {
		Name    string `json:"name"`
		FileRef string `json:"file_ref"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	ref, err := stickerTarget(op, resolved, arguments.Name, arguments.FileRef)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	fileID, err := client.checkRef(ref)
	if err != nil {
		return nil, err
	}
	if _, err := client.ownSticker(ctx, op, arguments.Name, fileID); err != nil {
		return nil, err
	}
	body := struct {
		Sticker string `json:"sticker"`
	}{fileID}
	if err := client.mutateStickerSet(ctx, op, "deleteStickerFromSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "deleted": true}, nil
}
