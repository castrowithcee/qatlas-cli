package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxStickerSetTitle    = 64
	maxStickersPerCreate  = 50
	maxStickerEmojiList   = 20
	maxStickerKeywords    = 20
	maxStickerKeywordsLen = 64
	ownSetSuffix          = "_by_"
)

var stickerSetMutationRisk = func(effect capability.Effect, idem capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idem, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

const stickerSetNameSchema = `"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"` + stickerSetNamePattern + `"}`

const inputStickerSchema = `{"type":"object","properties":{"file_ref":{"type":"string","minLength":1,"maxLength":2048},` +
	`"format":{"type":"string","enum":["static","animated","video"]},` +
	`"emoji_list":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"string","minLength":1,"maxLength":16}},` +
	`"keywords":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":64}}},` +
	`"required":["file_ref","format","emoji_list"],"additionalProperties":false}`

const inputStickerHelp = "an object with file_ref (from stickers.uploadfile, bound to the bot), format (static, animated, " +
	"or video, matching the uploaded file), emoji_list (1 through 20 emoji), and optional keywords (up to 20, " +
	"64 characters in total)"

var (
	stickersUploadFile = capability.Descriptor{
		ID:      Provider + ".stickers.uploadfile",
		Version: 1,
		Title:   "Upload a Telegram sticker file",
		Description: "Upload one .webp, .tgs, or .webm local_path in a directory the connection releases for reading " +
			"for a user, for use in a sticker set; the sticker format follows the extension and the file is sent " +
			"under a neutral name without its path. Returns a file_ref bound to the bot for stickersets.create and " +
			"stickersets.addsticker. Needs the bot target. An unclear outcome is never repeated",
		Tags:       []string{"telegram", "stickers", "upload", "files"},
		Risk:       stickerSetMutationRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		Provider:   Provider,
		Group:      groupStickers,
		LocalFiles: config.LocalFilesRead,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + userIDSchema + `,"` +
			localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
			`"required":["user_id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_ref":{"type":"string"},` +
			`"file_unique_id":{"type":"string"}},"required":["file_ref"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "user_id", Required: true, Description: "Telegram user identifier who will own the sticker set"},
			{Name: localfile.LocalPathArgument, Required: true, Description: "Local sticker (.webp, .tgs, or .webm), " +
				"absolute or starting with ~/, inside a directory the connection releases for reading, up to 512 KB"},
		},
		Fields: []capability.Field{
			{Name: "file_ref", Description: "Signed file reference bound to the bot, for the sticker arguments of the sticker set tools"},
			{Name: "file_unique_id", Description: "Telegram's stable identifier of the file"},
		},
		Examples: []capability.Example{{Description: "Upload a static sticker for a user",
			Arguments: json.RawMessage(`{"user_id":42,"local_path":"~/stickers/hello.webp"}`)}},
	}

	stickersetsCreate = capability.Descriptor{
		ID:      Provider + ".stickersets.create",
		Version: 1,
		Title:   "Create a Telegram sticker set",
		Description: "Create a sticker set owned by a user, from 1 through 50 uploaded stickers. The name must end in " +
			"_by_ and the bot's username. Needs the bot target. An unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "create"},
		Risk:     stickerSetMutationRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + userIDSchema + `,` + stickerSetNameSchema + `,` +
			`"title":{"type":"string","minLength":1,"maxLength":64},` +
			`"sticker_type":{"type":"string","enum":["regular","mask","custom_emoji"]},` +
			`"stickers":{"type":"array","minItems":1,"maxItems":50,"items":` + inputStickerSchema + `}},` +
			`"required":["user_id","name","title","stickers"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"created":{"type":"boolean"}},"required":["name","created"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "user_id", Required: true, Description: "Telegram user identifier who owns the sticker set"},
			{Name: "name", Required: true, Description: "Sticker set name of letters, digits, and underscores, ending in " +
				"_by_ and the bot's username"},
			{Name: "title", Required: true, Description: "Sticker set title, from 1 through 64 characters"},
			{Name: "sticker_type", Description: "regular (default), mask, or custom_emoji"},
			{Name: "stickers", Required: true, Description: "From 1 through 50 stickers, each " + inputStickerHelp},
		},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the created sticker set"},
			{Name: "created", Description: "True when Telegram accepted the sticker set"},
		},
		Examples: []capability.Example{{Description: "Create a sticker set from an uploaded file",
			Arguments: json.RawMessage(`{"user_id":42,"name":"animals_by_mybot","title":"Animals","stickers":` +
				`[{"file_ref":"<file_ref of stickers.uploadfile>","format":"static","emoji_list":["🐶"]}]}`)}},
	}

	stickersetsAddSticker = capability.Descriptor{
		ID:      Provider + ".stickersets.addsticker",
		Version: 1,
		Title:   "Add a sticker to a Telegram sticker set",
		Description: "Add one uploaded sticker to a sticker set of the bot. The name must end in _by_ and the bot's " +
			"username. Needs the bot target. An unclear outcome is never repeated",
		Tags:     []string{"telegram", "stickers", "update"},
		Risk:     stickerSetMutationRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		Provider: Provider,
		Group:    groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + userIDSchema + `,` + stickerSetNameSchema + `,` +
			`"sticker":` + inputStickerSchema + `},"required":["user_id","name","sticker"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"added":{"type":"boolean"}},"required":["name","added"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "user_id", Required: true, Description: "Telegram user identifier who owns the sticker set"},
			{Name: "name", Required: true, Description: "Name of the sticker set, ending in _by_ and the bot's username"},
			{Name: "sticker", Required: true, Description: "The sticker to add, " + inputStickerHelp},
		},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the sticker set"},
			{Name: "added", Description: "True when Telegram accepted the sticker"},
		},
		Examples: []capability.Example{{Description: "Add an uploaded sticker to a set",
			Arguments: json.RawMessage(`{"user_id":42,"name":"animals_by_mybot","sticker":` +
				`{"file_ref":"<file_ref of stickers.uploadfile>","format":"static","emoji_list":["🐱"]}}`)}},
	}

	stickersetsDelete = capability.Descriptor{
		ID:      Provider + ".stickersets.delete",
		Version: 1,
		Title:   "Delete a Telegram sticker set",
		Description: "Delete a sticker set of the bot. The name must end in _by_ and the bot's username. Needs the bot " +
			"target. An unclear outcome is never repeated",
		Tags:                  []string{"telegram", "stickers", "delete"},
		Risk:                  stickerSetMutationRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		Provider:              Provider,
		RequiresToolAllowList: true,
		Group:                 groupStickers,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + stickerSetNameSchema + `},` +
			`"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
			`"deleted":{"type":"boolean"}},"required":["name","deleted"],"additionalProperties":false}`),
		Arguments: []capability.Argument{{Name: "name", Required: true,
			Description: "Name of the sticker set, ending in _by_ and the bot's username"}},
		Fields: []capability.Field{
			{Name: "name", Description: "Name of the deleted sticker set"},
			{Name: "deleted", Description: "True when Telegram accepted the deletion"},
		},
		Examples: []capability.Example{{Description: "Delete a sticker set of the bot",
			Arguments: json.RawMessage(`{"name":"animals_by_mybot"}`)}},
	}
)

// ownStickerSet admits only a set the bot itself can own: Telegram names such sets <name>_by_<bot username>,
// so any other name belongs to somebody else. It reads the bot username with one getMe call and runs before
// any change.
func (c *Client) ownStickerSet(ctx context.Context, op, name string) error {
	if !validStickerSetName(name) {
		return providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	bot, err := c.GetBot(ctx)
	if err != nil {
		return err
	}
	if bot.Username == "" {
		return providerError(op, "the bot has no username, so no sticker set can be attributed to it")
	}
	suffix := strings.ToLower(ownSetSuffix + bot.Username)
	if lower := strings.ToLower(name); !strings.HasSuffix(lower, suffix) || len(lower) == len(suffix) {
		return providerError(op, "the sticker set name must end in "+ownSetSuffix+"<bot username>; only sets of this bot are managed")
	}
	return nil
}

// inputStickerArg is one InputSticker as the tools accept it.
type inputStickerArg struct {
	FileRef   string   `json:"file_ref"`
	Format    string   `json:"format"`
	EmojiList []string `json:"emoji_list"`
	Keywords  []string `json:"keywords"`
}

type inputStickerBody struct {
	Sticker   string   `json:"sticker"`
	Format    string   `json:"format"`
	EmojiList []string `json:"emoji_list"`
	Keywords  []string `json:"keywords,omitempty"`
}

// checkInputSticker validates one sticker object and the format of its reference without any credential. The
// reference must be bound to the bot: a chat's file is not an uploaded sticker file.
func checkInputSticker(op string, resolved *config.Resolved, s inputStickerArg) (parsedRef, error) {
	switch s.Format {
	case "static", "animated", "video":
	default:
		return parsedRef{}, providerError(op, "the sticker format must be static, animated, or video")
	}
	if len(s.EmojiList) < 1 || len(s.EmojiList) > maxStickerEmojiList {
		return parsedRef{}, providerError(op, "a sticker needs from 1 through 20 emoji")
	}
	for _, e := range s.EmojiList {
		if e == "" || !utf8.ValidString(e) || utf8.RuneCountInString(e) > maxStickerEmoji {
			return parsedRef{}, providerError(op, "the emoji is not supported")
		}
	}
	// Telegram caps the total length of the keywords, not each one.
	total := 0
	if len(s.Keywords) > maxStickerKeywords {
		return parsedRef{}, providerError(op, "a sticker takes at most 20 keywords")
	}
	for _, k := range s.Keywords {
		if k == "" || !utf8.ValidString(k) {
			return parsedRef{}, providerError(op, "a keyword must be non-empty text")
		}
		total += utf8.RuneCountInString(k)
	}
	if total > maxStickerKeywordsLen {
		return parsedRef{}, providerError(op, "the keywords of a sticker may have at most 64 characters in total")
	}
	ref, err := parseRef(resolved, refFile, s.FileRef)
	if err != nil {
		return parsedRef{}, err
	}
	if ref.binding != botTarget {
		return parsedRef{}, providerError(op, "the sticker file_ref must be bound to the bot, as stickers.uploadfile returns it")
	}
	return ref, nil
}

// inputStickerBody verifies the reference tag against the bot token before any request.
func (c *Client) inputStickerBody(s inputStickerArg, ref parsedRef) (inputStickerBody, error) {
	id, err := c.checkRef(ref)
	if err != nil {
		return inputStickerBody{}, err
	}
	return inputStickerBody{Sticker: id, Format: s.Format, EmojiList: s.EmojiList, Keywords: s.Keywords}, nil
}

// mutateStickerSet performs exactly one request of a method that answers true.
func (c *Client) mutateStickerSet(ctx context.Context, op, method string, payload any) error {
	raw, err := c.call(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, payload)
	if err != nil {
		return err
	}
	var done bool
	if json.Unmarshal(raw, &done) != nil || !done {
		return withUncertainty(spec{}, invalidResponse(op))
	}
	return nil
}

func validUserID(op string, id int64) error {
	if id <= 0 {
		return providerError(op, "user_id must be a positive integer")
	}
	return nil
}

func invokeStickersUploadFile(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersUploadFileWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersUploadFileWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "upload sticker file"
	var arguments struct {
		UserID    int64   `json:"user_id"`
		LocalPath *string `json:"local_path"`
	}
	if err := decodeStrict(raw, &arguments); err != nil || arguments.LocalPath == nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validUserID(op, arguments.UserID); err != nil {
		return nil, err
	}
	format, ok := stickerFormatOf(*arguments.LocalPath)
	if !ok {
		return nil, providerError(op, "a sticker file must end in .webp, .tgs, or .webm")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *arguments.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	if upload.Size > maxStickerBytes {
		return nil, providerError(op, "a sticker file is larger than 512 KB")
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(upload)
	if err != nil {
		return nil, err
	}
	answer, err := client.withUploadTimeout().callMultipart(ctx,
		spec{op: op, method: "uploadStickerFile", limit: defaultResponseBytes},
		[]field{{"user_id", strconv.FormatInt(arguments.UserID, 10)}, {"sticker_format", format}},
		[]filePart{{field: "sticker", name: neutralName(0, 1, upload.Name), data: data}})
	if err != nil {
		return nil, err
	}
	var file struct {
		FileID       string `json:"file_id"`
		FileUniqueID string `json:"file_unique_id"`
	}
	if json.Unmarshal(answer, &file) != nil {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	ref := signRef(client.token, refFile, botTarget, file.FileID)
	if ref == "" {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return struct {
		FileRef      string `json:"file_ref"`
		FileUniqueID string `json:"file_unique_id,omitempty"`
	}{ref, capText(file.FileUniqueID, maxNameRunes)}, nil
}

func invokeStickersetsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsCreateWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsCreateWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "create sticker set"
	var arguments struct {
		UserID      int64             `json:"user_id"`
		Name        string            `json:"name"`
		Title       string            `json:"title"`
		StickerType string            `json:"sticker_type"`
		Stickers    []inputStickerArg `json:"stickers"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validUserID(op, arguments.UserID); err != nil {
		return nil, err
	}
	if !validStickerSetName(arguments.Name) {
		return nil, providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	if n := utf8.RuneCountInString(arguments.Title); !utf8.ValidString(arguments.Title) || n < 1 || n > maxStickerSetTitle {
		return nil, providerError(op, "the title must have from 1 through 64 characters")
	}
	switch arguments.StickerType {
	case "", "regular", "mask", "custom_emoji":
	default:
		return nil, providerError(op, "sticker_type must be regular, mask, or custom_emoji")
	}
	if len(arguments.Stickers) < 1 || len(arguments.Stickers) > maxStickersPerCreate {
		return nil, providerError(op, "from 1 through 50 stickers are required")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	refs := make([]parsedRef, len(arguments.Stickers))
	for i, s := range arguments.Stickers {
		ref, err := checkInputSticker(op, resolved, s)
		if err != nil {
			return nil, err
		}
		refs[i] = ref
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	stickers := make([]inputStickerBody, len(arguments.Stickers))
	for i, s := range arguments.Stickers {
		if stickers[i], err = client.inputStickerBody(s, refs[i]); err != nil {
			return nil, err
		}
	}
	if err := client.ownStickerSet(ctx, op, arguments.Name); err != nil {
		return nil, err
	}
	body := struct {
		UserID      int64              `json:"user_id"`
		Name        string             `json:"name"`
		Title       string             `json:"title"`
		StickerType string             `json:"sticker_type,omitempty"`
		Stickers    []inputStickerBody `json:"stickers"`
	}{arguments.UserID, arguments.Name, arguments.Title, arguments.StickerType, stickers}
	if err := client.mutateStickerSet(ctx, op, "createNewStickerSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "created": true}, nil
}

func invokeStickersetsAddSticker(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsAddStickerWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsAddStickerWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "add sticker to set"
	var arguments struct {
		UserID  int64           `json:"user_id"`
		Name    string          `json:"name"`
		Sticker inputStickerArg `json:"sticker"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validUserID(op, arguments.UserID); err != nil {
		return nil, err
	}
	if !validStickerSetName(arguments.Name) {
		return nil, providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	ref, err := checkInputSticker(op, resolved, arguments.Sticker)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	sticker, err := client.inputStickerBody(arguments.Sticker, ref)
	if err != nil {
		return nil, err
	}
	if err := client.ownStickerSet(ctx, op, arguments.Name); err != nil {
		return nil, err
	}
	body := struct {
		UserID  int64            `json:"user_id"`
		Name    string           `json:"name"`
		Sticker inputStickerBody `json:"sticker"`
	}{arguments.UserID, arguments.Name, sticker}
	if err := client.mutateStickerSet(ctx, op, "addStickerToSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "added": true}, nil
}

func invokeStickersetsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStickersetsDeleteWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStickersetsDeleteWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "delete sticker set"
	var arguments struct {
		Name string `json:"name"`
	}
	if err := decodeStrict(raw, &arguments); err != nil || !validStickerSetName(arguments.Name) {
		return nil, providerError(op, "the sticker set name must have from 1 through 64 letters, digits, or underscores")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	if err := client.ownStickerSet(ctx, op, arguments.Name); err != nil {
		return nil, err
	}
	body := struct {
		Name string `json:"name"`
	}{arguments.Name}
	if err := client.mutateStickerSet(ctx, op, "deleteStickerSet", body); err != nil {
		return nil, err
	}
	return map[string]any{"name": arguments.Name, "deleted": true}, nil
}
