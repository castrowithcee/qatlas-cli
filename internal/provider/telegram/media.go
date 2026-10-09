package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupMedia = "media"

	// maxPhotoBytes is the Bot API's limit for a photo; maxUploadBytes bounds every other file and a whole album.
	maxPhotoBytes = 10 << 20
	// maxLivePhotoBytes is the Bot API's limit for the video and for the still image of a live photo.
	maxLivePhotoBytes = 10 << 20
	minAlbumItems     = 2
	maxAlbumItems     = 10
	// uploadTimeout replaces the short request timeout for the one request that carries file content.
	uploadTimeout = 10 * time.Minute
	// maxAlbumResponseBytes bounds the answer of sendMediaGroup, which carries up to ten messages.
	maxAlbumResponseBytes = 256 << 10
)

const (
	kindPhoto     = "photo"
	kindDocument  = "document"
	kindVideo     = "video"
	kindAnimation = "animation"
	kindVideoNote = "videonote"
	kindAudio     = "audio"
	kindVoice     = "voice"
	// kindLivePhoto is the video of a live photo; kindLiveStill is its still image.
	kindLivePhoto = "livephoto"
	kindLiveStill = "livephotostill"
)

// kindSpecs holds the fixed Bot API method, upload field, display noun, example extension, and tool ID segment
// of every single-file kind. The segment is spelled out because audio and voice have no plural.
var kindSpecs = map[string]struct{ method, field, noun, example, segment string }{
	kindPhoto:     {"sendPhoto", "photo", "photo", ".jpg", "photos"},
	kindDocument:  {"sendDocument", "document", "document", ".pdf", "documents"},
	kindVideo:     {"sendVideo", "video", "video", ".mp4", "videos"},
	kindAnimation: {"sendAnimation", "animation", "animation", ".mp4", "animations"},
	kindVideoNote: {"sendVideoNote", "video_note", "video note", ".mp4", "videonotes"},
	kindAudio:     {"sendAudio", "audio", "audio file", ".mp3", "audio"},
	kindVoice:     {"sendVoice", "voice", "voice message", ".ogg", "voice"},
	kindLivePhoto: {"sendLivePhoto", "live_photo", "live photo", ".mp4", "livephotos"},
	kindLiveStill: {"sendLivePhoto", "photo", "live photo still", ".jpg", "livephotos"},
}

var mediaRisk = capability.Risk{
	Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const captionOptionSchema = `"caption":{"type":"string","minLength":1,"maxLength":1024},` +
	`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]},`

const mediaOptionSchema = captionOptionSchema + sendOptionSchema

// sendOptionSchema holds the options every media send has, also one without a caption.
const sendOptionSchema = `"reply_to_message_id":{"type":"integer","minimum":1},"message_thread_id":{"type":"integer","minimum":1},` +
	`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}`

const sourceProperties = `"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
	`"file_ref":` + fileRefSchema

func mediaArguments(captionText string) []capability.Argument {
	return append([]capability.Argument{
		chatArgument,
		{Name: "caption", Description: captionText + ", from 1 through 1024 characters"},
		parseModeArgument,
	}, sendArguments()...)
}

func sendArguments() []capability.Argument {
	return []capability.Argument{
		{Name: "reply_to_message_id", Description: "Message of the same chat to reply to"},
		{Name: "message_thread_id", Description: "Forum topic to send into"},
		{Name: "disable_notification", Description: "Send silently"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the message"},
	}
}

var sentFields = []capability.Field{
	{Name: "message_id", Description: "Telegram message identifier"},
	{Name: "date", Description: "Telegram send time as Unix time"},
	{Name: "file_ref", Description: "Signed reference of the sent file, to send it again with this tool"},
}

const sentSchema = `{"type":"object","properties":{"message_id":{"type":"integer"},"date":{"type":"integer"},` +
	`"file_ref":{"type":"string"}},"required":["message_id","date"],"additionalProperties":false}`

func singleDescriptor(kind, title, limit string) capability.Descriptor {
	noun := kindSpecs[kind].noun
	// A video note has no caption in the Bot API.
	options, optionSchema := mediaArguments("Caption of the "+noun), mediaOptionSchema
	if kind == kindVideoNote {
		options, optionSchema = append([]capability.Argument{chatArgument}, sendArguments()...), sendOptionSchema
	}
	arguments := append([]capability.Argument{
		localfile.UploadPathArgument(),
		{Name: "file_ref", Description: "Signed file reference (media.file_ref) of a file received in the same bound " +
			"chat or returned by an earlier send to it; instead of " + localfile.LocalPathArgument},
	}, options...)
	arguments[0].Description = "Local " + noun + " to send, absolute or starting with ~/, inside a directory the " +
		"connection releases for reading, up to " + limit + "; instead of file_ref"
	return capability.Descriptor{
		ID:      Provider + "." + kindSpecs[kind].segment + ".send",
		Version: 1,
		Title:   title,
		Description: "Send one " + noun + " up to " + limit + " to a bound chat of an explicit Telegram connection, from " +
			"local_path in a directory the connection releases for reading or from a signed file_ref. A local file is " +
			"sent under a neutral name without its path. An unclear outcome is never repeated",
		Tags:         []string{"telegram", kindSpecs[kind].segment, "send", "files"},
		Risk:         mediaRisk,
		Provider:     Provider,
		Group:        groupMedia,
		LocalFiles:   config.LocalFilesRead,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + sourceProperties + `,` + optionSchema + `},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(sentSchema),
		Arguments:    arguments,
		Fields:       sentFields,
		Examples: []capability.Example{{
			Description: "Send a local " + noun + " to this connection's chat",
			Arguments:   json.RawMessage(`{"local_path":"~/reports/file` + kindSpecs[kind].example + `"}`),
		}},
	}
}

var photosSend = singleDescriptor(kindPhoto, "Send a Telegram photo", "10 MB")

var documentsSend = singleDescriptor(kindDocument, "Send a Telegram document", "50 MB")

var videosSend = singleDescriptor(kindVideo, "Send a Telegram video", "50 MB")

var animationsSend = singleDescriptor(kindAnimation, "Send a Telegram animation", "50 MB")

var videoNotesSend = singleDescriptor(kindVideoNote, "Send a Telegram video note", "50 MB")

var audioSend = singleDescriptor(kindAudio, "Send a Telegram audio file", "50 MB")

var voiceSend = singleDescriptor(kindVoice, "Send a Telegram voice message", "50 MB")

var livePhotosSend = func() capability.Descriptor {
	arguments := append([]capability.Argument{
		{Name: "local_path", Description: "Local live photo video to send, absolute or starting with ~/, inside a " +
			"directory the connection releases for reading, at most 10 seconds and up to 10 MB; instead of file_ref"},
		{Name: "file_ref", Description: "Signed file reference (media.file_ref) of the live photo video, received in " +
			"the same bound chat or returned by an earlier send to it; instead of local_path"},
		{Name: "photo_local_path", Description: "Local still image of the live photo, absolute or starting with ~/, " +
			"inside a directory the connection releases for reading, up to 10 MB; instead of photo_file_ref"},
		{Name: "photo_file_ref", Description: "Signed file reference of a photo received in the same bound chat or " +
			"returned by an earlier send to it; instead of photo_local_path"},
	}, mediaArguments("Caption of the live photo")...)
	return capability.Descriptor{
		ID:      Provider + ".livephotos.send",
		Version: 1,
		Title:   "Send a Telegram live photo",
		Description: "Send one live photo, a video of at most 10 seconds and 10 MB with its still image up to 10 MB, " +
			"to a bound chat of an explicit Telegram connection. The video comes from exactly one of local_path " +
			"and file_ref, the still image from exactly one of photo_local_path and photo_file_ref; a local file " +
			"must lie in a directory the connection releases for reading and is sent under a neutral name without " +
			"its path. The result file_ref is that of the video. An unclear outcome is never repeated",
		Tags:       []string{"telegram", "livephotos", "send", "files"},
		Risk:       mediaRisk,
		Provider:   Provider,
		Group:      groupMedia,
		LocalFiles: config.LocalFilesRead,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + sourceProperties + `,` +
			`"photo_local_path":` + localfile.LocalPathSchema + `,"photo_file_ref":` + fileRefSchema + `,` +
			mediaOptionSchema + `},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(sentSchema),
		Arguments:    arguments,
		Fields:       sentFields,
		Examples: []capability.Example{{
			Description: "Send a live photo from a local video and a local still image",
			Arguments:   json.RawMessage(`{"local_path":"~/shots/live.mp4","photo_local_path":"~/shots/live.jpg"}`),
		}},
	}
}()

var mediaGroupsSend = capability.Descriptor{
	ID:      Provider + ".mediagroups.send",
	Version: 1,
	Title:   "Send a Telegram album",
	Description: "Send an album of 2 through 10 photos and videos, which may be mixed, of 2 through 10 documents, or " +
		"of 2 through 10 audio files, the last two never mixed with anything, to a bound chat of an explicit " +
		"Telegram connection, each item from a local file in a directory the connection releases for reading or " +
		"from a signed file_ref. The files of one album together stay below 50 MB. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "mediagroups", "send", "files"},
	Risk:       mediaRisk,
	Provider:   Provider,
	Group:      groupMedia,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"type":{"type":"string","enum":["photo","video","document","audio"]},` +
		`"items":{"type":"array","minItems":2,"maxItems":10,"items":{"type":"object","properties":{` +
		`"type":{"type":"string","enum":["photo","video"]},` + sourceProperties + `},"additionalProperties":false}},` + mediaOptionSchema +
		`},"required":["type","items"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"messages":{"type":"array","items":` + sentSchema +
		`}},"required":["messages"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "type", Description: "photo, video, document, or audio; the type of every item without its own type. " +
			"Documents and audio files are never mixed with other types", Required: true},
		{Name: "items", Description: "From 2 through 10 items, each with exactly one of local_path and file_ref and, " +
			"in a photo or video album, an optional type photo or video; photos up to 10 MB each, videos, " +
			"documents, and audio files up to 50 MB each", Required: true},
	}, mediaArguments("Caption of the album, shown on its first item")...),
	Fields: []capability.Field{{Name: "messages", Description: "One entry per sent message with message_id, date, and file_ref"}},
	Examples: []capability.Example{{
		Description: "Send two local photos as one album",
		Arguments:   json.RawMessage(`{"type":"photo","items":[{"local_path":"~/shots/a.jpg"},{"local_path":"~/shots/b.jpg"}]}`),
	}, {
		Description: "Send two local audio files as one album",
		Arguments:   json.RawMessage(`{"type":"audio","items":[{"local_path":"~/music/a.mp3"},{"local_path":"~/music/b.mp3"}]}`),
	}, {
		Description: "Send a photo and a video as one album",
		Arguments:   json.RawMessage(`{"type":"photo","items":[{"local_path":"~/shots/a.jpg"},{"type":"video","local_path":"~/shots/b.mp4"}]}`),
	}},
}

// mediaOptions are the optional arguments the three tools share.
type mediaOptions struct {
	Caption             string `json:"caption"`
	ParseMode           string `json:"parse_mode"`
	ReplyToMessageID    int64  `json:"reply_to_message_id"`
	MessageThreadID     int64  `json:"message_thread_id"`
	DisableNotification bool   `json:"disable_notification"`
	ProtectContent      bool   `json:"protect_content"`
}

func (o mediaOptions) validate(op string) error {
	switch {
	case !utf8.ValidString(o.Caption) || utf8.RuneCountInString(o.Caption) > maxCaptionRunes:
		return providerError(op, "the caption is longer than 1024 characters")
	case !validParseMode(o.ParseMode):
		return providerError(op, "the parse mode is not supported")
	case o.ReplyToMessageID < 0 || o.MessageThreadID < 0:
		return providerError(op, "the reply and topic identifiers must be positive")
	}
	return nil
}

// mediaSource is the source of one file as the caller wrote it: exactly one of the two fields is set.
type mediaSource struct {
	LocalPath *string `json:"local_path"`
	FileRef   string  `json:"file_ref"`
}

// mediaItem is one file ready to send: a verified Telegram file identifier, or the content of a local file.
type mediaItem struct {
	kind   string
	fileID string
	name   string
	data   []byte
}

// preparedSource is a source that passed every check that needs neither the credential nor the network.
type preparedSource struct {
	kind   string
	ref    parsedRef
	hasRef bool
	local  *localfile.Upload
}

// prepareSources validates each source and opens each local file, so a foreign reference, a path outside the
// release, or an oversized file is refused before the credential is resolved. The caller closes the uploads.
func prepareSources(ctx context.Context, resolved *config.Resolved, op, chat string, kinds []string,
	sources []mediaSource) ([]preparedSource, error) {
	prepared := make([]preparedSource, 0, len(sources))
	closeAll := func() {
		for _, p := range prepared {
			if p.local != nil {
				_ = p.local.Close()
			}
		}
	}
	var total int64
	for i, source := range sources {
		kind := kinds[i]
		limit := int64(maxUploadBytes)
		switch kind {
		case kindPhoto:
			limit = maxPhotoBytes
		case kindLivePhoto, kindLiveStill:
			limit = maxLivePhotoBytes
		}
		hasPath, hasRef := source.LocalPath != nil, source.FileRef != ""
		if hasPath == hasRef {
			closeAll()
			return nil, providerError(op, "exactly one of local_path and file_ref is required for each file")
		}
		if hasRef {
			parsed, err := parseRef(resolved, refFile, source.FileRef)
			if err == nil && parsed.binding != chat {
				// A reference stays with the chat it was issued for, even if the connection binds several.
				err = providerError(op, "the reference does not belong to the selected chat")
			}
			if err != nil {
				closeAll()
				return nil, err
			}
			prepared = append(prepared, preparedSource{kind: kind, ref: parsed, hasRef: true})
			continue
		}
		upload, err := localfile.OpenForUpload(ctx, resolved, *source.LocalPath)
		if err != nil {
			closeAll()
			return nil, err
		}
		prepared = append(prepared, preparedSource{kind: kind, local: upload})
		total += upload.Size
		if upload.Size > limit || total > maxUploadBytes {
			closeAll()
			return nil, providerError(op, "a file is larger than Telegram accepts for this kind of upload")
		}
	}
	return prepared, nil
}

// load reads the content of every local file and resolves the references once the client exists.
func (c *Client) load(op string, prepared []preparedSource) ([]mediaItem, error) {
	items := make([]mediaItem, len(prepared))
	for i, p := range prepared {
		if p.hasRef {
			id, err := c.checkRef(p.ref)
			if err != nil {
				return nil, err
			}
			items[i] = mediaItem{kind: p.kind, fileID: id}
			continue
		}
		data, err := io.ReadAll(p.local)
		if err != nil {
			return nil, err
		}
		items[i] = mediaItem{kind: p.kind, name: neutralName(i, len(prepared), p.local.Name), data: data}
	}
	return items, nil
}

// neutralName hides the local name: Telegram receives only a counter and a plain extension, which it needs to
// recognize the type. The directory is never part of it, and the base name is dropped as well.
func neutralName(index, count int, local string) string {
	ext := filepath.Ext(local)
	if len(ext) < 2 || len(ext) > 9 {
		ext = ""
	}
	for _, r := range ext {
		if r != '.' && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			ext = ""
			break
		}
	}
	if count == 1 {
		return "file" + ext
	}
	return "file-" + strconv.Itoa(index+1) + ext
}

func invokePhotosSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindPhoto)
}

func invokeDocumentsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindDocument)
}

func invokeVideosSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindVideo)
}

func invokeAnimationsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindAnimation)
}

func invokeVideoNotesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindVideoNote)
}

func invokeAudioSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindAudio)
}

func invokeVoiceSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSingleSend(ctx, resolved, secrets, red, raw, newHTTPClient(), kindVoice)
}

func invokeLivePhotosSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeLivePhotosSendWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeLivePhotosSendWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "send live photo"
	var arguments struct {
		Chat string `json:"chat"`
		mediaSource
		PhotoLocalPath *string `json:"photo_local_path"`
		PhotoFileRef   string  `json:"photo_file_ref"`
		mediaOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := arguments.mediaOptions.validate(op); err != nil {
		return nil, err
	}
	if (arguments.PhotoLocalPath != nil) == (arguments.PhotoFileRef != "") {
		return nil, providerError(op, "exactly one of photo_local_path and photo_file_ref is required")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	// Both pairs are checked, each with its limit, before the credential is resolved.
	still := mediaSource{LocalPath: arguments.PhotoLocalPath, FileRef: arguments.PhotoFileRef}
	prepared, err := prepareSources(ctx, resolved, op, chat, []string{kindLivePhoto, kindLiveStill},
		[]mediaSource{arguments.mediaSource, still})
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
	return client.sendLivePhoto(ctx, items[0], items[1], arguments.mediaOptions)
}

func invokeSingleSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client, kind string) (any, error) {
	op := "send " + kindSpecs[kind].noun
	var arguments struct {
		Chat string `json:"chat"`
		mediaSource
		mediaOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := arguments.mediaOptions.validate(op); err != nil {
		return nil, err
	}
	if kind == kindVideoNote && (arguments.Caption != "" || arguments.ParseMode != "") {
		return nil, providerError(op, "a video note has no caption")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	prepared, err := prepareSources(ctx, resolved, op, chat, []string{kind}, []mediaSource{arguments.mediaSource})
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
	return client.sendSingle(ctx, kind, items[0], arguments.mediaOptions)
}

func closeUploads(prepared []preparedSource) {
	for _, p := range prepared {
		if p.local != nil {
			_ = p.local.Close()
		}
	}
}

func invokeMediaGroupsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMediaGroupsSendWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeMediaGroupsSendWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "send album"
	var arguments struct {
		Chat  string      `json:"chat"`
		Type  string      `json:"type"`
		Items []albumItem `json:"items"`
		mediaOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if len(arguments.Items) < minAlbumItems || len(arguments.Items) > maxAlbumItems {
		return nil, providerError(op, "an album has from 2 through 10 items")
	}
	kinds, err := albumKinds(op, arguments.Type, arguments.Items)
	if err != nil {
		return nil, err
	}
	sources := make([]mediaSource, len(arguments.Items))
	for i, item := range arguments.Items {
		sources[i] = item.mediaSource
	}
	if err := arguments.mediaOptions.validate(op); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	prepared, err := prepareSources(ctx, resolved, op, chat, kinds, sources)
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
	return client.sendAlbum(ctx, items, arguments.mediaOptions)
}

// albumItem is one album entry; a photo or video album lets an item override the album type with the other of
// the two, a document or audio album never mixes.
type albumItem struct {
	mediaSource
	Type string `json:"type"`
}

func albumKinds(op, albumType string, items []albumItem) ([]string, error) {
	kinds := make([]string, len(items))
	switch albumType {
	case kindDocument, kindAudio:
		for i, item := range items {
			if item.Type != "" {
				return nil, providerError(op, "documents and audio files are not mixed with other types")
			}
			kinds[i] = albumType
		}
	case kindPhoto, kindVideo:
		for i, item := range items {
			kinds[i] = albumType
			switch item.Type {
			case "":
			case kindPhoto, kindVideo:
				kinds[i] = item.Type
			default:
				return nil, providerError(op, "an item of a photo or video album must be a photo or a video")
			}
		}
	default:
		return nil, providerError(op, "the album type must be photo, video, document, or audio")
	}
	return kinds, nil
}

// withUploadTimeout returns a copy of the client whose requests may take as long as an upload needs.
func (c *Client) withUploadTimeout() *Client {
	copied := *c
	long := *c.http
	long.Timeout = uploadTimeout
	copied.http = &long
	return &copied
}

// sendSingle performs exactly one request of the fixed method of the kind.
func (c *Client) sendSingle(ctx context.Context, kind string, item mediaItem, o mediaOptions) (map[string]any, error) {
	kindSpec, known := kindSpecs[kind]
	op := "send " + kindSpec.noun
	if !known || (kind == kindVideoNote && (o.Caption != "" || o.ParseMode != "")) {
		return nil, providerError(op, "the media kind is not supported")
	}
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	s := spec{op: op, method: kindSpec.method, limit: defaultResponseBytes}
	var (
		raw json.RawMessage
		err error
	)
	if item.fileID != "" {
		body := map[string]any{"chat_id": c.target, kindSpec.field: item.fileID}
		if o.Caption != "" {
			body["caption"] = o.Caption
			if o.ParseMode != "" {
				body["parse_mode"] = o.ParseMode
			}
		}
		if o.ReplyToMessageID > 0 {
			body["reply_parameters"] = replyParameters{MessageID: o.ReplyToMessageID}
		}
		if o.MessageThreadID > 0 {
			body["message_thread_id"] = o.MessageThreadID
		}
		if o.DisableNotification {
			body["disable_notification"] = true
		}
		if o.ProtectContent {
			body["protect_content"] = true
		}
		raw, err = c.call(ctx, s, body)
	} else {
		fields := c.commonFields(o)
		if o.Caption != "" {
			fields = append(fields, field{"caption", o.Caption})
			if o.ParseMode != "" {
				fields = append(fields, field{"parse_mode", o.ParseMode})
			}
		}
		raw, err = c.withUploadTimeout().callMultipart(ctx, s, fields,
			[]filePart{{field: kindSpec.field, name: item.name, data: item.data}})
	}
	if err != nil {
		return nil, err
	}
	sent, ok := c.parseSent(raw, kind)
	if !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return sent, nil
}

// sendLivePhoto performs exactly one multipart request of sendLivePhoto: the video as live_photo and the still
// image as photo, each as a file part or, for a reference, as a file_id form field.
func (c *Client) sendLivePhoto(ctx context.Context, video, still mediaItem, o mediaOptions) (map[string]any, error) {
	const op = "send live photo"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if video.kind != kindLivePhoto || still.kind != kindLiveStill {
		return nil, providerError(op, "the media kind is not supported")
	}
	s := spec{op: op, method: kindSpecs[kindLivePhoto].method, limit: defaultResponseBytes}
	fields := c.commonFields(o)
	var files []filePart
	for _, item := range []mediaItem{video, still} {
		name := kindSpecs[item.kind].field
		if item.fileID != "" {
			fields = append(fields, field{name, item.fileID})
		} else {
			files = append(files, filePart{field: name, name: item.name, data: item.data})
		}
	}
	if o.Caption != "" {
		fields = append(fields, field{"caption", o.Caption})
		if o.ParseMode != "" {
			fields = append(fields, field{"parse_mode", o.ParseMode})
		}
	}
	client := c
	if files != nil {
		client = c.withUploadTimeout()
	}
	raw, err := client.callMultipart(ctx, s, fields, files)
	if err != nil {
		return nil, err
	}
	sent, ok := c.parseSent(raw, kindLivePhoto)
	if !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return sent, nil
}

// commonFields are the plain form fields every multipart media request carries.
func (c *Client) commonFields(o mediaOptions) []field {
	fields := []field{{"chat_id", c.target}}
	if o.ReplyToMessageID > 0 {
		reply, _ := json.Marshal(replyParameters{MessageID: o.ReplyToMessageID})
		fields = append(fields, field{"reply_parameters", string(reply)})
	}
	if o.MessageThreadID > 0 {
		fields = append(fields, field{"message_thread_id", strconv.FormatInt(o.MessageThreadID, 10)})
	}
	if o.DisableNotification {
		fields = append(fields, field{"disable_notification", "true"})
	}
	if o.ProtectContent {
		fields = append(fields, field{"protect_content", "true"})
	}
	return fields
}

// inputMedia is one element of the media array of sendMediaGroup.
type inputMedia struct {
	Type      string `json:"type"`
	Media     string `json:"media"`
	Caption   string `json:"caption,omitempty"`
	ParseMode string `json:"parse_mode,omitempty"`
}

func exclusiveKind(kind string) bool { return kind == kindDocument || kind == kindAudio }

// sendAlbum performs exactly one sendMediaGroup request. The caption goes on the first item only, where
// Telegram shows it for the whole album.
func (c *Client) sendAlbum(ctx context.Context, items []mediaItem, o mediaOptions) (map[string]any, error) {
	const op = "send album"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if len(items) < minAlbumItems || len(items) > maxAlbumItems {
		return nil, providerError(op, "an album has from 2 through 10 items")
	}
	for _, item := range items {
		switch item.kind {
		case kindDocument, kindAudio, kindPhoto, kindVideo:
		default:
			return nil, providerError(op, "an album has only photos, videos, documents, or audio files")
		}
		// Documents and audio files only group with their own kind.
		if item.kind != items[0].kind && (exclusiveKind(item.kind) || exclusiveKind(items[0].kind)) {
			return nil, providerError(op, "documents and audio files are not mixed with other types")
		}
	}
	s := spec{op: op, method: "sendMediaGroup", limit: maxAlbumResponseBytes}
	media := make([]inputMedia, len(items))
	var files []filePart
	for i, item := range items {
		media[i] = inputMedia{Type: item.kind}
		if i == 0 {
			media[i].Caption, media[i].ParseMode = o.Caption, o.ParseMode
		}
		if item.fileID != "" {
			media[i].Media = item.fileID
			continue
		}
		part := "file" + strconv.Itoa(i)
		media[i].Media = "attach://" + part
		files = append(files, filePart{field: part, name: item.name, data: item.data})
	}
	var (
		raw json.RawMessage
		err error
	)
	if files == nil {
		body := struct {
			ChatID              string           `json:"chat_id"`
			Media               []inputMedia     `json:"media"`
			ReplyParameters     *replyParameters `json:"reply_parameters,omitempty"`
			MessageThreadID     int64            `json:"message_thread_id,omitempty"`
			DisableNotification bool             `json:"disable_notification,omitempty"`
			ProtectContent      bool             `json:"protect_content,omitempty"`
		}{ChatID: c.target, Media: media, MessageThreadID: o.MessageThreadID,
			DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent}
		if o.ReplyToMessageID > 0 {
			body.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
		}
		raw, err = c.call(ctx, s, body)
	} else {
		encoded, marshalErr := json.Marshal(media)
		if marshalErr != nil {
			return nil, providerError(op, "the request could not be encoded")
		}
		fields := append(c.commonFields(o), field{"media", string(encoded)})
		raw, err = c.withUploadTimeout().callMultipart(ctx, s, fields, files)
	}
	if err != nil {
		return nil, err
	}
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) != len(items) {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		sent, ok := c.parseSent(message, items[len(out)].kind)
		if !ok {
			return nil, withUncertainty(spec{}, invalidResponse(op))
		}
		out = append(out, sent)
	}
	return map[string]any{"messages": out}, nil
}

type fileIDField struct {
	FileID string `json:"file_id"`
}

// parseSent reads the allow-listed fields of one sent message; the file_ref is signed for the target chat.
func (c *Client) parseSent(raw json.RawMessage, kind string) (map[string]any, bool) {
	var message struct {
		MessageID int64         `json:"message_id"`
		Date      int64         `json:"date"`
		Photo     []fileIDField `json:"photo"`
		Document  *fileIDField  `json:"document"`
		Video     *fileIDField  `json:"video"`
		Animation *fileIDField  `json:"animation"`
		VideoNote *fileIDField  `json:"video_note"`
		Audio     *fileIDField  `json:"audio"`
		Voice     *fileIDField  `json:"voice"`
		LivePhoto *fileIDField  `json:"live_photo"`
	}
	if json.Unmarshal(raw, &message) != nil || message.MessageID <= 0 || message.Date <= 0 {
		return nil, false
	}
	fileID := ""
	switch {
	case kind == kindPhoto && len(message.Photo) > 0:
		// The last PhotoSize is the largest.
		fileID = message.Photo[len(message.Photo)-1].FileID
	case kind == kindDocument && message.Document != nil:
		fileID = message.Document.FileID
	case kind == kindVideo && message.Video != nil:
		fileID = message.Video.FileID
	case kind == kindAnimation && message.Animation != nil:
		fileID = message.Animation.FileID
	case kind == kindVideoNote && message.VideoNote != nil:
		fileID = message.VideoNote.FileID
	case kind == kindAudio && message.Audio != nil:
		fileID = message.Audio.FileID
	case kind == kindVoice && message.Voice != nil:
		fileID = message.Voice.FileID
	case kind == kindLivePhoto && message.LivePhoto != nil:
		// The file of a live photo is its video; the still image in photo is not returned.
		fileID = message.LivePhoto.FileID
	}
	out := map[string]any{"message_id": message.MessageID, "date": message.Date}
	if ref := signRef(c.token, refFile, c.target, fileID); ref != "" {
		out["file_ref"] = ref
	}
	return out, true
}
