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
	minAlbumItems = 2
	maxAlbumItems = 10
	// uploadTimeout replaces the short request timeout for the one request that carries file content.
	uploadTimeout = 10 * time.Minute
	// maxAlbumResponseBytes bounds the answer of sendMediaGroup, which carries up to ten messages.
	maxAlbumResponseBytes = 256 << 10
)

const (
	kindPhoto    = "photo"
	kindDocument = "document"
)

var mediaRisk = capability.Risk{
	Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const mediaOptionSchema = `"caption":{"type":"string","minLength":1,"maxLength":1024},` +
	`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]},` +
	`"reply_to_message_id":{"type":"integer","minimum":1},"message_thread_id":{"type":"integer","minimum":1},` +
	`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}`

const sourceProperties = `"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
	`"file_ref":` + fileRefSchema

func mediaArguments(captionText string) []capability.Argument {
	return []capability.Argument{
		chatArgument,
		{Name: "caption", Description: captionText + ", from 1 through 1024 characters"},
		parseModeArgument,
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
	arguments := append([]capability.Argument{
		localfile.UploadPathArgument(),
		{Name: "file_ref", Description: "Signed file reference (media.file_ref) of a file received in the same bound " +
			"chat or returned by an earlier send to it; instead of " + localfile.LocalPathArgument},
	}, mediaArguments("Caption of the "+kind)...)
	arguments[0].Description = "Local " + kind + " to send, absolute or starting with ~/, inside a directory the " +
		"connection releases for reading, up to " + limit + "; instead of file_ref"
	return capability.Descriptor{
		ID:      Provider + "." + kind + "s.send",
		Version: 1,
		Title:   title,
		Description: "Send one " + kind + " up to " + limit + " to a bound chat of an explicit Telegram connection, from " +
			"local_path in a directory the connection releases for reading or from a signed file_ref. A local file is " +
			"sent under a neutral name without its path. An unclear outcome is never repeated",
		Tags:         []string{"telegram", kind + "s", "send", "files"},
		Risk:         mediaRisk,
		Provider:     Provider,
		Group:        groupMedia,
		LocalFiles:   config.LocalFilesRead,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + sourceProperties + `,` + mediaOptionSchema + `},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(sentSchema),
		Arguments:    arguments,
		Fields:       sentFields,
		Examples: []capability.Example{{
			Description: "Send a local " + kind + " to this connection's chat",
			Arguments:   json.RawMessage(`{"local_path":"~/reports/file` + map[string]string{kindPhoto: ".jpg", kindDocument: ".pdf"}[kind] + `"}`),
		}},
	}
}

var photosSend = singleDescriptor(kindPhoto, "Send a Telegram photo", "10 MB")

var documentsSend = singleDescriptor(kindDocument, "Send a Telegram document", "50 MB")

var mediaGroupsSend = capability.Descriptor{
	ID:      Provider + ".mediagroups.send",
	Version: 1,
	Title:   "Send a Telegram album",
	Description: "Send an album of 2 through 10 photos or of 2 through 10 documents, not mixed, to a bound chat of an " +
		"explicit Telegram connection, each item from a local file in a directory the connection releases for reading " +
		"or from a signed file_ref. The files of one album together stay below 50 MB. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "mediagroups", "send", "files"},
	Risk:       mediaRisk,
	Provider:   Provider,
	Group:      groupMedia,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"type":{"type":"string","enum":["photo","document"]},` +
		`"items":{"type":"array","minItems":2,"maxItems":10,"items":{"type":"object","properties":{` +
		sourceProperties + `},"additionalProperties":false}},` + mediaOptionSchema +
		`},"required":["type","items"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"messages":{"type":"array","items":` + sentSchema +
		`}},"required":["messages"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "type", Description: "photo or document; all items of an album have the same type", Required: true},
		{Name: "items", Description: "From 2 through 10 items, each with exactly one of local_path and file_ref; " +
			"photos up to 10 MB each, documents up to 50 MB each", Required: true},
	}, mediaArguments("Caption of the album, shown on its first item")...),
	Fields: []capability.Field{{Name: "messages", Description: "One entry per sent message with message_id, date, and file_ref"}},
	Examples: []capability.Example{{
		Description: "Send two local photos as one album",
		Arguments:   json.RawMessage(`{"type":"photo","items":[{"local_path":"~/shots/a.jpg"},{"local_path":"~/shots/b.jpg"}]}`),
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
	fileID string
	name   string
	data   []byte
}

// preparedSource is a source that passed every check that needs neither the credential nor the network.
type preparedSource struct {
	ref    parsedRef
	hasRef bool
	local  *localfile.Upload
}

// prepareSources validates each source and opens each local file, so a foreign reference, a path outside the
// release, or an oversized file is refused before the credential is resolved. The caller closes the uploads.
func prepareSources(ctx context.Context, resolved *config.Resolved, op, chat, kind string,
	sources []mediaSource) ([]preparedSource, error) {
	prepared := make([]preparedSource, 0, len(sources))
	closeAll := func() {
		for _, p := range prepared {
			if p.local != nil {
				_ = p.local.Close()
			}
		}
	}
	limit := int64(maxUploadBytes)
	if kind == kindPhoto {
		limit = maxPhotoBytes
	}
	var total int64
	for _, source := range sources {
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
			prepared = append(prepared, preparedSource{ref: parsed, hasRef: true})
			continue
		}
		upload, err := localfile.OpenForUpload(ctx, resolved, *source.LocalPath)
		if err != nil {
			closeAll()
			return nil, err
		}
		prepared = append(prepared, preparedSource{local: upload})
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
			items[i] = mediaItem{fileID: id}
			continue
		}
		data, err := io.ReadAll(p.local)
		if err != nil {
			return nil, err
		}
		items[i] = mediaItem{name: neutralName(i, len(prepared), p.local.Name), data: data}
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

func invokeSingleSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client, kind string) (any, error) {
	op := "send " + kind
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
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	prepared, err := prepareSources(ctx, resolved, op, chat, kind, []mediaSource{arguments.mediaSource})
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
		Chat  string        `json:"chat"`
		Type  string        `json:"type"`
		Items []mediaSource `json:"items"`
		mediaOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.Type != kindPhoto && arguments.Type != kindDocument {
		return nil, providerError(op, "the album type must be photo or document")
	}
	if len(arguments.Items) < minAlbumItems || len(arguments.Items) > maxAlbumItems {
		return nil, providerError(op, "an album has from 2 through 10 items")
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
	prepared, err := prepareSources(ctx, resolved, op, chat, arguments.Type, arguments.Items)
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
	return client.sendAlbum(ctx, arguments.Type, items, arguments.mediaOptions)
}

// withUploadTimeout returns a copy of the client whose requests may take as long as an upload needs.
func (c *Client) withUploadTimeout() *Client {
	copied := *c
	long := *c.http
	long.Timeout = uploadTimeout
	copied.http = &long
	return &copied
}

// sendSingle performs exactly one sendPhoto or sendDocument request.
func (c *Client) sendSingle(ctx context.Context, kind string, item mediaItem, o mediaOptions) (map[string]any, error) {
	op := "send " + kind
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	method := map[string]string{kindPhoto: "sendPhoto", kindDocument: "sendDocument"}[kind]
	s := spec{op: op, method: method, limit: defaultResponseBytes}
	var (
		raw json.RawMessage
		err error
	)
	if item.fileID != "" {
		body := map[string]any{"chat_id": c.target, kind: item.fileID}
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
			[]filePart{{field: kind, name: item.name, data: item.data}})
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

// sendAlbum performs exactly one sendMediaGroup request. The caption goes on the first item only, where
// Telegram shows it for the whole album.
func (c *Client) sendAlbum(ctx context.Context, kind string, items []mediaItem, o mediaOptions) (map[string]any, error) {
	const op = "send album"
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	if len(items) < minAlbumItems || len(items) > maxAlbumItems || (kind != kindPhoto && kind != kindDocument) {
		return nil, providerError(op, "an album has from 2 through 10 photos or documents")
	}
	s := spec{op: op, method: "sendMediaGroup", limit: maxAlbumResponseBytes}
	media := make([]inputMedia, len(items))
	var files []filePart
	for i, item := range items {
		media[i] = inputMedia{Type: kind}
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
		sent, ok := c.parseSent(message, kind)
		if !ok {
			return nil, withUncertainty(spec{}, invalidResponse(op))
		}
		out = append(out, sent)
	}
	return map[string]any{"messages": out}, nil
}

// parseSent reads the allow-listed fields of one sent message; the file_ref is signed for the target chat.
func (c *Client) parseSent(raw json.RawMessage, kind string) (map[string]any, bool) {
	var message struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
		Photo     []struct {
			FileID string `json:"file_id"`
		} `json:"photo"`
		Document *struct {
			FileID string `json:"file_id"`
		} `json:"document"`
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
	}
	out := map[string]any{"message_id": message.MessageID, "date": message.Date}
	if ref := signRef(c.token, refFile, c.target, fileID); ref != "" {
		out["file_ref"] = ref
	}
	return out, true
}
