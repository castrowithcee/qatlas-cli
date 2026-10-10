package telegram

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Telegram documents no range for a mask position. The bounds keep a mask within about two widths of the face
	// and between a tenth and four times its default size, which is what a usable mask needs.
	maxMaskShift = 2.0
	minMaskScale = 0.1
	maxMaskScale = 4.0

	// maxStaticThumbnailBytes and maxMovingThumbnailBytes are the Bot API's limits for a set thumbnail.
	maxStaticThumbnailBytes = 128 << 10
	maxMovingThumbnailBytes = 32 << 10
)

var thumbnailExtensions = map[string][]string{
	"static": {".webp", ".png"}, "animated": {".tgs"}, "video": {".webm"},
}

const (
	stickerEmojiListSchema = `"emoji_list":{"type":"array","minItems":1,"maxItems":20,` +
		`"items":{"type":"string","minLength":1,"maxLength":16}}`
	stickerKeywordsSchema = `"keywords":{"type":"array","maxItems":20,` +
		`"items":{"type":"string","minLength":1,"maxLength":64}}`
	maskPositionSchema = `"mask_position":{"type":"object","properties":{` +
		`"point":{"type":"string","enum":["forehead","eyes","mouth","chin"]},` +
		`"x_shift":{"type":"number","minimum":-2,"maximum":2},"y_shift":{"type":"number","minimum":-2,"maximum":2},` +
		`"scale":{"type":"number","minimum":0.1,"maximum":4}},` +
		`"required":["point","x_shift","y_shift","scale"],"additionalProperties":false}`
	updatedOutputSchema = `{"type":"object","properties":{"name":{"type":"string"},"updated":{"type":"boolean"}},` +
		`"required":["name","updated"],"additionalProperties":false}`
)

var updatedFields = []capability.Field{
	{Name: "name", Description: "Name of the sticker set"},
	{Name: "updated", Description: "True when Telegram accepted the change"},
}

var (
	stickersSetEmojiList = capability.Descriptor{
		ID:      Provider + ".stickers.setemojilist",
		Version: 1,
		Title:   "Set the emoji of a Telegram sticker",
		Description: "Replace the emoji list of one sticker of a sticker set of the bot. The name must end in _by_ " +
			"and the bot's username and the sticker must belong to that set. Needs the bot target. An unclear " +
			"outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,"file_ref":` +
			stickerFileRefSchema + `,` + stickerEmojiListSchema + `},` +
			`"required":["name","file_ref","emoji_list"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "file_ref", Required: true, Description: "The sticker to change, " + stickerRefHelp},
			{Name: "emoji_list", Required: true, Description: "From 1 through 20 emoji"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Set the emoji of a sticker",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","file_ref":"<file_ref of stickersets.get>","emoji_list":["🐱"]}`)}},
	}

	stickersSetKeywords = capability.Descriptor{
		ID:      Provider + ".stickers.setkeywords",
		Version: 1,
		Title:   "Set the search keywords of a Telegram sticker",
		Description: "Replace the search keywords of one sticker of a sticker set of the bot; an empty list removes " +
			"them. The name must end in _by_ and the bot's username and the sticker must belong to that set. Needs " +
			"the bot target. An unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,"file_ref":` +
			stickerFileRefSchema + `,` + stickerKeywordsSchema + `},` +
			`"required":["name","file_ref","keywords"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "file_ref", Required: true, Description: "The sticker to change, " + stickerRefHelp},
			{Name: "keywords", Required: true, Description: "Up to 20 keywords of 64 characters in total; an empty list removes them"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Set the keywords of a sticker",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","file_ref":"<file_ref of stickersets.get>","keywords":["cat","pet"]}`)}},
	}

	stickersSetMaskPosition = capability.Descriptor{
		ID:      Provider + ".stickers.setmaskposition",
		Version: 1,
		Title:   "Set the mask position of a Telegram sticker",
		Description: "Set or remove the mask position of one mask sticker of a sticker set of the bot. The name must " +
			"end in _by_ and the bot's username and the sticker must belong to that set. Needs the bot target. An " +
			"unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,"file_ref":` +
			stickerFileRefSchema + `,` + maskPositionSchema + `},"required":["name","file_ref"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "file_ref", Required: true, Description: "The mask sticker to change, " + stickerRefHelp},
			{Name: "mask_position", Description: "An object with point (forehead, eyes, mouth, or chin), x_shift and " +
				"y_shift (-2 through 2, in widths of the mask), and scale (0.1 through 4); omit it to remove the position"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Put a mask on the eyes",
			Arguments: json.RawMessage(`{"name":"masks_by_mybot","file_ref":"<file_ref of stickersets.get>",` +
				`"mask_position":{"point":"eyes","x_shift":0,"y_shift":0,"scale":1}}`)}},
	}

	stickersetsSetTitle = capability.Descriptor{
		ID:      Provider + ".stickersets.settitle",
		Version: 1,
		Title:   "Set the title of a Telegram sticker set",
		Description: "Change the title of a sticker set of the bot. The name must end in _by_ and the bot's username. " +
			"Needs the bot target. An unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,` +
			`"title":{"type":"string","minLength":1,"maxLength":64}},"required":["name","title"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "title", Required: true, Description: "New title, from 1 through 64 characters"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Rename a sticker set",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","title":"Animals"}`)}},
	}

	stickersetsSetThumbnail = capability.Descriptor{
		ID:      Provider + ".stickersets.setthumbnail",
		Version: 1,
		Title:   "Set the thumbnail of a Telegram sticker set",
		Description: "Set the thumbnail of a sticker set of the bot from a local_path in a directory the connection " +
			"releases for reading, sent under a neutral name without its path, or remove it by omitting local_path. " +
			"The name must end in _by_ and the bot's username. Needs the bot target. An unclear outcome is never " +
			"repeated",
		Tags:       []string{"telegram", "stickers", "update", "files"},
		Risk:       stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
		Provider:   Provider,
		Group:      groupStickers,
		LocalFiles: config.LocalFilesRead,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,` + userIDSchema + `,` +
			`"format":{"type":"string","enum":["static","animated","video"]},"` +
			localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
			`"required":["name","user_id","format"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "user_id", Required: true, Description: "Telegram user identifier who owns the sticker set"},
			{Name: "format", Required: true, Description: "static, animated, or video: the format of the sticker set"},
			{Name: localfile.LocalPathArgument, Description: "Local thumbnail, absolute or starting with ~/, inside a " +
				"directory the connection releases for reading: .webp or .png up to 128 KB for static, .tgs up to " +
				"32 KB for animated, .webm up to 32 KB for video; omit it to remove the thumbnail"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Set a static thumbnail",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot","user_id":42,"format":"static","local_path":"~/stickers/thumb.webp"}`)}},
	}

	stickersetsSetCustomEmojiThumbnail = capability.Descriptor{
		ID:      Provider + ".stickersets.setcustomemojithumbnail",
		Version: 1,
		Title:   "Set the thumbnail of a Telegram custom emoji set",
		Description: "Set the thumbnail of a custom emoji sticker set of the bot to a custom emoji, or remove it by " +
			"omitting custom_emoji_id. The name must end in _by_ and the bot's username. Needs the bot target. An " +
			"unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `,` +
			`"custom_emoji_id":{"type":"string","pattern":"` + customEmojiIDPattern + `"}},` +
			`"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(updatedOutputSchema),
		Arguments: []capability.Argument{
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "custom_emoji_id", Description: "Identifier of the custom emoji, digits only; omit it to remove the thumbnail"},
		},
		Fields: updatedFields,
		Examples: []capability.Example{{Description: "Use a custom emoji as the thumbnail",
			Arguments: json.RawMessage(`{"name":"emoji_by_mybot","custom_emoji_id":"5368324170671202286"}`)}},
	}
)

// openOwnSticker opens the connection and admits the sticker only when it lies in the named own set. It
// returns the client and the file identifier of the sticker.
func openOwnSticker(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, httpClient *http.Client, ref parsedRef, name string) (*Client, string, error) {
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, "", err
	}
	fileID, err := client.checkRef(ref)
	if err != nil {
		return nil, "", err
	}
	if _, err := client.ownSticker(ctx, op, name, fileID); err != nil {
		return nil, "", err
	}
	return client, fileID, nil
}

func invokeStickersSetEmojiList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersSetEmojiListWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersSetEmojiListWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set sticker emoji"
	var arguments struct {
		Name      string   `json:"name"`
		FileRef   string   `json:"file_ref"`
		EmojiList []string `json:"emoji_list"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkEmojiList(op, arguments.EmojiList); err != nil {
		return nil, err
	}
	ref, err := stickerTarget(op, resolved, arguments.Name, arguments.FileRef)
	if err != nil {
		return nil, err
	}
	client, fileID, err := openOwnSticker(ctx, op, resolved, secrets, red, httpClient, ref, arguments.Name)
	if err != nil {
		return nil, err
	}
	body := struct {
		Sticker   string   `json:"sticker"`
		EmojiList []string `json:"emoji_list"`
	}{fileID, arguments.EmojiList}
	if err := client.mutateStickerSet(ctx, op, "setStickerEmojiList", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}

func invokeStickersSetKeywords(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersSetKeywordsWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersSetKeywordsWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set sticker keywords"
	var arguments struct {
		Name     string   `json:"name"`
		FileRef  string   `json:"file_ref"`
		Keywords []string `json:"keywords"`
	}
	// A missing list stays nil and is refused: only an explicit empty list removes the keywords.
	if err := decodeStrict(raw, &arguments); err != nil || arguments.Keywords == nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkKeywords(op, arguments.Keywords); err != nil {
		return nil, err
	}
	ref, err := stickerTarget(op, resolved, arguments.Name, arguments.FileRef)
	if err != nil {
		return nil, err
	}
	client, fileID, err := openOwnSticker(ctx, op, resolved, secrets, red, httpClient, ref, arguments.Name)
	if err != nil {
		return nil, err
	}
	body := struct {
		Sticker  string   `json:"sticker"`
		Keywords []string `json:"keywords"`
	}{fileID, arguments.Keywords}
	if err := client.mutateStickerSet(ctx, op, "setStickerKeywords", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}

type maskPosition struct {
	Point  string   `json:"point"`
	XShift *float64 `json:"x_shift"`
	YShift *float64 `json:"y_shift"`
	Scale  *float64 `json:"scale"`
}

type maskPositionBody struct {
	Point  string  `json:"point"`
	XShift float64 `json:"x_shift"`
	YShift float64 `json:"y_shift"`
	Scale  float64 `json:"scale"`
}

// inRange is false for NaN and the infinities as well.
func inRange(v *float64, lo, hi float64) bool {
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= lo && *v <= hi
}

func checkMaskPosition(op string, m *maskPosition) (*maskPositionBody, error) {
	if m == nil {
		return nil, nil
	}
	switch m.Point {
	case "forehead", "eyes", "mouth", "chin":
	default:
		return nil, providerError(op, "the mask point must be forehead, eyes, mouth, or chin")
	}
	if !inRange(m.XShift, -maxMaskShift, maxMaskShift) || !inRange(m.YShift, -maxMaskShift, maxMaskShift) {
		return nil, providerError(op, "x_shift and y_shift must be numbers from -2 through 2")
	}
	if !inRange(m.Scale, minMaskScale, maxMaskScale) {
		return nil, providerError(op, "scale must be a number from 0.1 through 4")
	}
	return &maskPositionBody{m.Point, *m.XShift, *m.YShift, *m.Scale}, nil
}

func invokeStickersSetMaskPosition(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersSetMaskPositionWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersSetMaskPositionWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set sticker mask position"
	var arguments struct {
		Name         string        `json:"name"`
		FileRef      string        `json:"file_ref"`
		MaskPosition *maskPosition `json:"mask_position"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	mask, err := checkMaskPosition(op, arguments.MaskPosition)
	if err != nil {
		return nil, err
	}
	ref, err := stickerTarget(op, resolved, arguments.Name, arguments.FileRef)
	if err != nil {
		return nil, err
	}
	client, fileID, err := openOwnSticker(ctx, op, resolved, secrets, red, httpClient, ref, arguments.Name)
	if err != nil {
		return nil, err
	}
	body := struct {
		Sticker      string            `json:"sticker"`
		MaskPosition *maskPositionBody `json:"mask_position,omitempty"`
	}{fileID, mask}
	if err := client.mutateStickerSet(ctx, op, "setStickerMaskPosition", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}

// ownSetClient runs the checks of a set tool that need no request, then opens the connection and admits only
// a set of the bot.
func ownSetClient(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, httpClient *http.Client, name string) (*Client, error) {
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	if err := client.ownStickerSet(ctx, op, name); err != nil {
		return nil, err
	}
	return client, nil
}

// botSetScope checks the set name and the bot target without any credential.
func botSetScope(op string, resolved *config.Resolved, name string) error {
	if !validStickerSetName(name) {
		return providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	if resolved == nil {
		return providerError(op, "no connection was selected")
	}
	return requireBotScope(resolved)
}

func invokeStickersetsSetTitle(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsSetTitleWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsSetTitleWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set sticker set title"
	var arguments struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkStickerSetTitle(op, arguments.Title); err != nil {
		return nil, err
	}
	if err := botSetScope(op, resolved, arguments.Name); err != nil {
		return nil, err
	}
	client, err := ownSetClient(ctx, op, resolved, secrets, red, httpClient, arguments.Name)
	if err != nil {
		return nil, err
	}
	body := struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}{arguments.Name, arguments.Title}
	if err := client.mutateStickerSet(ctx, op, "setStickerSetTitle", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}

func invokeStickersetsSetThumbnail(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsSetThumbnailWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsSetThumbnailWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set sticker set thumbnail"
	var arguments struct {
		Name      string  `json:"name"`
		UserID    int64   `json:"user_id"`
		Format    string  `json:"format"`
		LocalPath *string `json:"local_path"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validUserID(op, arguments.UserID); err != nil {
		return nil, err
	}
	extensions, ok := thumbnailExtensions[arguments.Format]
	if !ok {
		return nil, providerError(op, "the format must be static, animated, or video")
	}
	if arguments.LocalPath != nil {
		matches := false
		for _, e := range extensions {
			matches = matches || strings.EqualFold(filepath.Ext(*arguments.LocalPath), e)
		}
		if !matches {
			return nil, providerError(op, "the thumbnail file must end in "+strings.Join(extensions, " or ")+
				" for the format "+arguments.Format)
		}
	}
	if err := botSetScope(op, resolved, arguments.Name); err != nil {
		return nil, err
	}
	var upload *localfile.Upload
	if arguments.LocalPath != nil {
		limit := int64(maxMovingThumbnailBytes)
		if arguments.Format == "static" {
			limit = maxStaticThumbnailBytes
		}
		var err error
		if upload, err = localfile.OpenForUpload(ctx, resolved, *arguments.LocalPath); err != nil {
			return nil, err
		}
		defer upload.Close()
		if upload.Size > limit {
			return nil, providerError(op, "the thumbnail file is larger than Telegram accepts for this format")
		}
	}
	client, err := ownSetClient(ctx, op, resolved, secrets, red, httpClient, arguments.Name)
	if err != nil {
		return nil, err
	}
	if upload == nil {
		body := struct {
			Name   string `json:"name"`
			UserID int64  `json:"user_id"`
			Format string `json:"format"`
		}{arguments.Name, arguments.UserID, arguments.Format}
		if err := client.mutateStickerSet(ctx, op, "setStickerSetThumbnail", body); err != nil {
			return nil, err
		}
		return map[string]any{"name": arguments.Name, "updated": true}, nil
	}
	data, err := io.ReadAll(upload)
	if err != nil {
		return nil, err
	}
	answer, err := client.withUploadTimeout().callMultipart(ctx,
		spec{op: op, method: "setStickerSetThumbnail", limit: defaultResponseBytes},
		[]field{{"name", arguments.Name}, {"user_id", strconv.FormatInt(arguments.UserID, 10)},
			{"format", arguments.Format}},
		[]filePart{{field: "thumbnail", name: neutralName(0, 1, upload.Name), data: data}})
	if err != nil {
		return nil, err
	}
	if err := requireTrue(op, answer); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}

func invokeStickersetsSetCustomEmojiThumbnail(ctx context.Context, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsSetCustomEmojiThumbnailWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsSetCustomEmojiThumbnailWith(ctx context.Context, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set custom emoji set thumbnail"
	var arguments struct {
		Name          string  `json:"name"`
		CustomEmojiID *string `json:"custom_emoji_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.CustomEmojiID != nil && !digitsOnly(*arguments.CustomEmojiID) {
		return nil, providerError(op, "the custom emoji identifier must consist of digits only")
	}
	if err := botSetScope(op, resolved, arguments.Name); err != nil {
		return nil, err
	}
	client, err := ownSetClient(ctx, op, resolved, secrets, red, httpClient, arguments.Name)
	if err != nil {
		return nil, err
	}
	body := struct {
		Name          string  `json:"name"`
		CustomEmojiID *string `json:"custom_emoji_id,omitempty"`
	}{arguments.Name, arguments.CustomEmojiID}
	if err := client.mutateStickerSet(ctx, op, "setCustomEmojiStickerSetThumbnail", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "updated": true}, nil
}
