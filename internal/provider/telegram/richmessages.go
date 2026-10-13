package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The Bot API documents no size bounds for most rich blocks; these are Qatlas bounds that keep one request small.
const (
	richOp           = "send rich message"
	maxRichTopBlocks = 100
	maxRichBlocks    = 200
	maxRichDepth     = 4
	maxRichTextDepth = 8
	maxRichTextItems = 100
	maxRichString    = 4096
	maxRichListItems = 100
	maxRichTableRows = 50
	maxRichTableCols = 20
	maxRichBodyBytes = 256 << 10
	// maxMapSide and maxMapRatio are the documented bounds of a map block.
	maxMapSide  = 10000
	maxMapRatio = 20
)

// richBlockTypes are the block types a rich message may carry; thinking is not among them because the Bot API
// allows it in drafts only.
var richBlockTypes = []string{"paragraph", "heading", "pre", "footer", "divider", "mathematical_expression",
	"anchor", "list", "blockquote", "expandable_blockquote", "pullquote", "collage", "slideshow", "table",
	"details", "map", "buttons", "photo", "video", "animation", "audio", "document", "voice_note"}

// richMediaBlocks maps a media block type to the file kind that decides its size limit.
var richMediaBlocks = map[string]string{"photo": kindPhoto, "video": kindVideo, "animation": kindAnimation,
	"audio": kindAudio, "document": kindDocument, "voice_note": kindVoice}

// richSimpleText lists the text styles that wrap exactly one nested text.
var richSimpleText = map[string]bool{"bold": true, "italic": true, "underline": true, "strikethrough": true,
	"spoiler": true, "subscript": true, "superscript": true, "marked": true, "code": true}

const richBlocksArgument = "Message content as 1 through 100 blocks, each an object with type and: paragraph and " +
	"footer {text}; heading {text, size 1-6}; pre {text, language}; divider; mathematical_expression " +
	"{expression}; anchor {name}; list {items: [{blocks, has_checkbox, is_checked, value, type}]}; blockquote " +
	"{blocks, credit}; expandable_blockquote and pullquote {text, credit}; collage and slideshow {blocks of " +
	"photo, video, or animation blocks, caption}; table {cells: rows of {text, is_header, colspan, rowspan, " +
	"align, valign}, is_bordered, is_striped, is_compact, caption}; details {summary, blocks, is_open}; map " +
	"{location: {latitude, longitude}, zoom, width, height, caption}; buttons {buttons: [{text, style, " +
	"url or callback_data}], align}; photo, video, animation, audio, document, and voice_note {local_path or " +
	"file_ref, caption: {text, credit}}. A text is a string, a list of texts, {type: bold, italic, underline, " +
	"strikethrough, spoiler, subscript, superscript, marked, or code; text}, {type: url; text; url}, or " +
	"{type: mathematical_expression; expression}"

// The blocks schema checks only the shape the shared validator can express; richBuilder checks every block type
// and field locally before any I/O.
var richBlocksSchema = func() string {
	types, _ := json.Marshal(richBlockTypes)
	return `{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":` +
		`{"type":{"type":"string","enum":` + string(types) + `}},"required":["type"]}}`
}()

var richMessagesSend = capability.Descriptor{
	ID:      Provider + ".richmessages.send",
	Version: 1,
	Title:   "Send a Telegram rich message",
	Description: "Send one structured rich message of modeled blocks (text, lists, tables, quotes, maps, buttons, " +
		"media) to a bound chat of an explicit Telegram connection. Media and documents come from local_path in a " +
		"directory the connection releases for reading or from a signed file_ref of the chat, never from a URL. " +
		"An unclear outcome is never repeated",
	Tags:       []string{"telegram", "richmessages", "send"},
	Risk:       mediaRisk,
	Provider:   Provider,
	Group:      groupMessages,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,"blocks":` + richBlocksSchema + `,` +
		`"is_rtl":{"type":"boolean"},"skip_entity_detection":{"type":"boolean"},` + sendOptionSchema +
		`},"required":["blocks"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		chatArgument,
		{Name: "blocks", Description: richBlocksArgument, Required: true},
		{Name: "is_rtl", Description: "Show the message right-to-left"},
		{Name: "skip_entity_detection", Description: "Do not detect links, mentions, and similar entities in the text"},
	}, sendArguments()...),
	Fields: []capability.Field{
		{Name: "message_id", Description: "Telegram message identifier"},
		{Name: "date", Description: "Telegram send time as Unix time"},
	},
	Examples: []capability.Example{{
		Description: "Send a heading and a paragraph to this connection's chat",
		Arguments: json.RawMessage(`{"blocks":[{"type":"heading","size":2,"text":"Release"},` +
			`{"type":"paragraph","text":["Version ",{"type":"bold","text":"1.2"}," is out"]}]}`),
	}},
}

type richSendOptions struct {
	ReplyToMessageID    int64 `json:"reply_to_message_id"`
	MessageThreadID     int64 `json:"message_thread_id"`
	DisableNotification bool  `json:"disable_notification"`
	ProtectContent      bool  `json:"protect_content"`
}

func invokeRichMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRichMessagesSendWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeRichMessagesSendWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	var arguments struct {
		Chat                string            `json:"chat"`
		Blocks              []json.RawMessage `json:"blocks"`
		IsRTL               bool              `json:"is_rtl"`
		SkipEntityDetection bool              `json:"skip_entity_detection"`
		richSendOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(richOp, "the validated arguments could not be read")
	}
	if arguments.ReplyToMessageID < 0 || arguments.MessageThreadID < 0 {
		return nil, providerError(richOp, "the reply and topic identifiers must be positive")
	}
	if resolved == nil {
		return nil, providerError(richOp, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	b := &richBuilder{}
	blocks, err := b.build(arguments.Blocks)
	if err != nil {
		return nil, err
	}
	prepared, err := prepareSources(ctx, resolved, richOp, chat, b.kinds, b.sources)
	if err != nil {
		return nil, err
	}
	defer closeUploads(prepared)
	client, err := openChatWithHTTP(ctx, resolved, secrets, red, chat, httpClient)
	if err != nil {
		return nil, err
	}
	items, err := client.load(richOp, prepared)
	if err != nil {
		return nil, err
	}
	message := richMessage{Blocks: blocks, IsRTL: arguments.IsRTL, SkipEntityDetection: arguments.SkipEntityDetection}
	return client.sendRich(ctx, message, b.slots, items, arguments.richSendOptions)
}

type richMessage struct {
	Blocks              []any `json:"blocks"`
	IsRTL               bool  `json:"is_rtl,omitempty"`
	SkipEntityDetection bool  `json:"skip_entity_detection,omitempty"`
}

// sendRich performs exactly one sendRichMessage request. A local file is uploaded as a multipart part that its
// block names with attach://; a reference contributes the verified file identifier of its own chat.
func (c *Client) sendRich(ctx context.Context, message richMessage, slots []map[string]any, items []mediaItem,
	o richSendOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError(richOp, "no chat was selected")
	}
	if len(slots) != len(items) {
		return nil, providerError(richOp, "the media of the message could not be matched")
	}
	var files []filePart
	for i, item := range items {
		if item.fileID != "" {
			slots[i]["media"] = item.fileID
			continue
		}
		part := "file" + strconv.Itoa(i)
		slots[i]["media"] = "attach://" + part
		files = append(files, filePart{field: part, name: item.name, data: item.data})
	}
	s := spec{op: richOp, method: "sendRichMessage", limit: defaultResponseBytes}
	var (
		raw json.RawMessage
		err error
	)
	if files == nil {
		body := struct {
			ChatID              string           `json:"chat_id"`
			RichMessage         richMessage      `json:"rich_message"`
			ReplyParameters     *replyParameters `json:"reply_parameters,omitempty"`
			MessageThreadID     int64            `json:"message_thread_id,omitempty"`
			DisableNotification bool             `json:"disable_notification,omitempty"`
			ProtectContent      bool             `json:"protect_content,omitempty"`
		}{ChatID: c.target, RichMessage: message, MessageThreadID: o.MessageThreadID,
			DisableNotification: o.DisableNotification, ProtectContent: o.ProtectContent}
		if o.ReplyToMessageID > 0 {
			body.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
		}
		raw, err = c.call(ctx, s, body)
	} else {
		encoded, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return nil, providerError(richOp, "the request could not be encoded")
		}
		fields := append(c.commonFields(mediaOptions{ReplyToMessageID: o.ReplyToMessageID,
			MessageThreadID: o.MessageThreadID, DisableNotification: o.DisableNotification,
			ProtectContent: o.ProtectContent}), field{"rich_message", string(encoded)})
		raw, err = c.withUploadTimeout().callMultipart(ctx, s, fields, files)
	}
	if err != nil {
		return nil, err
	}
	var result struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
	}
	if json.Unmarshal(raw, &result) != nil || result.MessageID <= 0 || result.Date <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse(richOp))
	}
	return map[string]any{"message_id": result.MessageID, "date": result.Date}, nil
}

// richBuilder rebuilds every block from validated fields; nothing of the caller's JSON is passed through.
type richBuilder struct {
	count   int
	slots   []map[string]any
	sources []mediaSource
	kinds   []string
}

func richError(message string) error { return providerError(richOp, message) }

// build validates all blocks and returns their wire form. The media entries stay without a source until the
// files are loaded.
func (b *richBuilder) build(raw []json.RawMessage) ([]any, error) {
	if len(raw) < 1 || len(raw) > maxRichTopBlocks {
		return nil, richError("a rich message has from 1 through 100 blocks")
	}
	blocks, err := b.blockList(raw, 0, nil)
	if err != nil {
		return nil, err
	}
	if encoded, err := json.Marshal(blocks); err != nil || len(encoded) > maxRichBodyBytes {
		return nil, richError("the rich message is larger than the supported size")
	}
	return blocks, nil
}

func (b *richBuilder) blockList(raw []json.RawMessage, depth int, only map[string]bool) ([]any, error) {
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		block, err := b.block(item, depth, only)
		if err != nil {
			return nil, err
		}
		out = append(out, block)
	}
	return out, nil
}

// nested validates the blocks member of a block that holds other blocks.
func (b *richBuilder) nested(o richObject, name string, depth int, only map[string]bool, max int) ([]any, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(o[name], &raw); err != nil || len(raw) < 1 || len(raw) > max {
		return nil, richError("a block holds an unsupported number of nested blocks")
	}
	return b.blockList(raw, depth+1, only)
}

var richMediaOnly = map[string]bool{"photo": true, "video": true, "animation": true}

func (b *richBuilder) block(raw json.RawMessage, depth int, only map[string]bool) (map[string]any, error) {
	if depth > maxRichDepth {
		return nil, richError("the blocks are nested too deeply")
	}
	if b.count++; b.count > maxRichBlocks {
		return nil, richError("the rich message has too many blocks")
	}
	o, ok := parseObject(raw)
	if !ok {
		return nil, richError("a block must be an object")
	}
	var kind string
	if json.Unmarshal(o["type"], &kind) != nil || !containsString(richBlockTypes, kind) {
		return nil, richError("a block has an unknown type")
	}
	if only != nil && !only[kind] {
		return nil, richError("this block type is not allowed here")
	}
	out := map[string]any{"type": kind}
	if mediaKind, isMedia := richMediaBlocks[kind]; isMedia {
		return out, b.mediaBlock(o, kind, mediaKind, out)
	}
	var err error
	switch kind {
	case "paragraph", "footer":
		if err = o.only([]string{"text"}); err == nil {
			out["text"], err = o.text("text")
		}
	case "heading":
		if err = o.only([]string{"text", "size"}); err != nil {
			break
		}
		if out["text"], err = o.text("text"); err == nil {
			out["size"], err = o.integer("size", 1, 6)
		}
	case "pre":
		if err = o.only([]string{"text"}, "language"); err != nil {
			break
		}
		if out["text"], err = o.text("text"); err == nil {
			err = o.optionalString(out, "language", 64)
		}
	case "divider":
		err = o.only(nil)
	case "mathematical_expression":
		if err = o.only([]string{"expression"}); err == nil {
			out["expression"], err = o.str("expression", 4096)
		}
	case "anchor":
		if err = o.only([]string{"name"}); err == nil {
			out["name"], err = o.str("name", 64)
		}
	case "list":
		err = b.list(o, depth, out)
	case "blockquote":
		if err = o.only([]string{"blocks"}, "credit"); err != nil {
			break
		}
		if out["blocks"], err = b.nested(o, "blocks", depth, nil, maxRichTopBlocks); err == nil {
			err = o.optionalText(out, "credit")
		}
	case "expandable_blockquote", "pullquote":
		if err = o.only([]string{"text"}, "credit"); err != nil {
			break
		}
		if out["text"], err = o.text("text"); err == nil {
			err = o.optionalText(out, "credit")
		}
	case "collage", "slideshow":
		if err = o.only([]string{"blocks"}, "caption"); err != nil {
			break
		}
		if out["blocks"], err = b.nested(o, "blocks", depth, richMediaOnly, 10); err == nil {
			err = o.optionalCaption(out)
		}
	case "table":
		err = b.table(o, out)
	case "details":
		if err = o.only([]string{"summary", "blocks"}, "is_open"); err != nil {
			break
		}
		if out["summary"], err = o.text("summary"); err != nil {
			break
		}
		if out["blocks"], err = b.nested(o, "blocks", depth, nil, maxRichTopBlocks); err == nil {
			err = o.flag(out, "is_open")
		}
	case "map":
		err = o.mapBlock(out)
	case "buttons":
		err = o.buttons(out)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mediaBlock takes the source of a media block: exactly one of local_path and a file_ref of the chat. A URL, a
// file identifier, and every Bot API media property are unknown fields.
func (b *richBuilder) mediaBlock(o richObject, kind, mediaKind string, out map[string]any) error {
	if err := o.only(nil, "local_path", "file_ref", "caption"); err != nil {
		return err
	}
	if len(b.slots) >= maxAlbumItems {
		return richError("a rich message has at most 10 files")
	}
	var source mediaSource
	if _, ok := o["local_path"]; ok {
		path, err := o.str("local_path", 4096)
		if err != nil {
			return err
		}
		source.LocalPath = &path
	}
	if _, ok := o["file_ref"]; ok {
		ref, err := o.str("file_ref", maxRefText)
		if err != nil {
			return err
		}
		source.FileRef = ref
	}
	if err := o.optionalCaption(out); err != nil {
		return err
	}
	// Telegram ignores the caption of the media itself; the block caption is the visible one.
	media := map[string]any{"type": kind, "media": ""}
	out[kind] = media
	b.slots = append(b.slots, media)
	b.sources = append(b.sources, source)
	b.kinds = append(b.kinds, mediaKind)
	return nil
}

func (b *richBuilder) list(o richObject, depth int, out map[string]any) error {
	if err := o.only([]string{"items"}); err != nil {
		return err
	}
	var raw []json.RawMessage
	if json.Unmarshal(o["items"], &raw) != nil || len(raw) < 1 || len(raw) > maxRichListItems {
		return richError("a list has from 1 through 100 items")
	}
	items := make([]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := parseObject(item)
		if !ok {
			return richError("a list item must be an object")
		}
		if err := entry.only([]string{"blocks"}, "has_checkbox", "is_checked", "value", "type"); err != nil {
			return err
		}
		wire := map[string]any{}
		blocks, err := b.nested(entry, "blocks", depth, nil, maxRichTopBlocks)
		if err != nil {
			return err
		}
		wire["blocks"] = blocks
		if err := entry.flag(wire, "has_checkbox"); err != nil {
			return err
		}
		if err := entry.flag(wire, "is_checked"); err != nil {
			return err
		}
		if wire["is_checked"] != nil && wire["has_checkbox"] == nil {
			return richError("a checked list item needs has_checkbox")
		}
		if _, ok := entry["value"]; ok {
			if wire["value"], err = entry.integer("value", 0, math.MaxInt32); err != nil {
				return err
			}
		}
		if err := entry.optionalEnum(wire, "type", "a", "A", "i", "I", "1"); err != nil {
			return err
		}
		items = append(items, wire)
	}
	out["items"] = items
	return nil
}

func (b *richBuilder) table(o richObject, out map[string]any) error {
	if err := o.only([]string{"cells"}, "is_bordered", "is_striped", "is_compact", "caption"); err != nil {
		return err
	}
	var rows []json.RawMessage
	if json.Unmarshal(o["cells"], &rows) != nil || len(rows) < 1 || len(rows) > maxRichTableRows {
		return richError("a table has from 1 through 50 rows")
	}
	wireRows := make([]any, 0, len(rows))
	for _, rawRow := range rows {
		var cells []json.RawMessage
		if json.Unmarshal(rawRow, &cells) != nil || len(cells) < 1 || len(cells) > maxRichTableCols {
			return richError("a table row has from 1 through 20 cells")
		}
		wireRow := make([]any, 0, len(cells))
		for _, rawCell := range cells {
			cell, ok := parseObject(rawCell)
			if !ok {
				return richError("a table cell must be an object")
			}
			wire, err := cell.tableCell()
			if err != nil {
				return err
			}
			wireRow = append(wireRow, wire)
		}
		wireRows = append(wireRows, wireRow)
	}
	out["cells"] = wireRows
	for _, name := range []string{"is_bordered", "is_striped", "is_compact"} {
		if err := o.flag(out, name); err != nil {
			return err
		}
	}
	return o.optionalText(out, "caption")
}

// richObject is one JSON object whose members are checked and copied by name.
type richObject map[string]json.RawMessage

func parseObject(raw json.RawMessage) (richObject, bool) {
	var o richObject
	if json.Unmarshal(raw, &o) != nil || o == nil {
		return nil, false
	}
	for _, value := range o {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return o, true
}

// only rejects every member that is not listed; type is always allowed and every required member must exist.
func (o richObject) only(required []string, optional ...string) error {
	allowed := map[string]bool{"type": true}
	for _, name := range required {
		allowed[name] = true
		if _, ok := o[name]; !ok {
			return richError("a block lacks a required field")
		}
	}
	for _, name := range optional {
		allowed[name] = true
	}
	for name := range o {
		if !allowed[name] {
			return richError("a block has an unknown field")
		}
	}
	return nil
}

func (o richObject) str(name string, max int) (string, error) {
	var value string
	if json.Unmarshal(o[name], &value) != nil || !utf8.ValidString(value) {
		return "", richError("a block field must be text")
	}
	if n := utf8.RuneCountInString(value); n < 1 || n > max {
		return "", richError("a block text field is outside the supported length")
	}
	return value, nil
}

func (o richObject) integer(name string, min, max int64) (int64, error) {
	var value int64
	if json.Unmarshal(o[name], &value) != nil || value < min || value > max {
		return 0, richError("a block number is outside the supported range")
	}
	return value, nil
}

func (o richObject) optionalString(out map[string]any, name string, max int) error {
	if _, ok := o[name]; !ok {
		return nil
	}
	value, err := o.str(name, max)
	out[name] = value
	return err
}

func (o richObject) optionalEnum(out map[string]any, name string, allowed ...string) error {
	if _, ok := o[name]; !ok {
		return nil
	}
	value, err := o.str(name, 16)
	if err != nil || !containsString(allowed, value) {
		return richError("a block field has an unsupported value")
	}
	out[name] = value
	return nil
}

// flag copies a boolean that the Bot API documents as "True only"; false is the same as absent.
func (o richObject) flag(out map[string]any, name string) error {
	raw, ok := o[name]
	if !ok {
		return nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return richError("a block flag must be true or false")
	}
	if value {
		out[name] = true
	}
	return nil
}

func (o richObject) text(name string) (any, error) {
	raw, ok := o[name]
	if !ok {
		return nil, richError("a block lacks a required field")
	}
	return richText(raw, 0)
}

func (o richObject) optionalText(out map[string]any, name string) error {
	if _, ok := o[name]; !ok {
		return nil
	}
	text, err := o.text(name)
	out[name] = text
	return err
}

// optionalCaption copies a block caption: a text and an optional credit.
func (o richObject) optionalCaption(out map[string]any) error {
	raw, ok := o["caption"]
	if !ok {
		return nil
	}
	caption, ok := parseObject(raw)
	if !ok {
		return richError("a caption must be an object")
	}
	if err := caption.onlyKeys([]string{"text"}, "credit"); err != nil {
		return err
	}
	wire := map[string]any{}
	text, err := caption.text("text")
	if err != nil {
		return err
	}
	wire["text"] = text
	if err := caption.optionalText(wire, "credit"); err != nil {
		return err
	}
	out["caption"] = wire
	return nil
}

// onlyKeys is only for an object that has no type member.
func (o richObject) onlyKeys(required []string, optional ...string) error {
	if _, ok := o["type"]; ok {
		return richError("a block has an unknown field")
	}
	return o.only(required, optional...)
}

func (o richObject) tableCell() (map[string]any, error) {
	if err := o.onlyKeys([]string{"align", "valign"}, "text", "is_header", "colspan", "rowspan"); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := o.optionalText(out, "text"); err != nil {
		return nil, err
	}
	if err := o.flag(out, "is_header"); err != nil {
		return nil, err
	}
	for _, name := range []string{"colspan", "rowspan"} {
		if _, ok := o[name]; ok {
			value, err := o.integer(name, 2, maxRichTableCols)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
	}
	for name, allowed := range map[string][]string{"align": {"left", "center", "right"},
		"valign": {"top", "middle", "bottom"}} {
		value, err := o.str(name, 16)
		if err != nil || !containsString(allowed, value) {
			return nil, richError("a table cell alignment is not supported")
		}
		out[name] = value
	}
	return out, nil
}

func (o richObject) mapBlock(out map[string]any) error {
	if err := o.only([]string{"location"}, "zoom", "width", "height", "caption"); err != nil {
		return err
	}
	location, ok := parseObject(o["location"])
	if !ok {
		return richError("a map location must be an object")
	}
	if err := location.onlyKeys([]string{"latitude", "longitude"}); err != nil {
		return err
	}
	var latitude, longitude float64
	if json.Unmarshal(location["latitude"], &latitude) != nil || json.Unmarshal(location["longitude"], &longitude) != nil ||
		math.Abs(latitude) > 90 || math.Abs(longitude) > 180 {
		return richError("a map location is outside the supported range")
	}
	out["location"] = map[string]any{"latitude": latitude, "longitude": longitude}
	for name, max := range map[string]int64{"zoom": 24, "width": maxMapSide, "height": maxMapSide} {
		if _, ok := o[name]; ok {
			value, err := o.integer(name, 0, max)
			if err != nil {
				return err
			}
			out[name] = value
		}
	}
	width, _ := out["width"].(int64)
	height, _ := out["height"].(int64)
	if width+height > maxMapSide || (width > 0 && height > 0 && (width > maxMapRatio*height || height > maxMapRatio*width)) {
		return richError("the map size is outside the supported range")
	}
	return o.optionalCaption(out)
}

// buttons rebuilds the buttons of a button row and applies the inline keyboard rules to them: a link or
// callback data only, never a web app, login, payment, or inline query target.
func (o richObject) buttons(out map[string]any) error {
	if err := o.only([]string{"buttons"}, "align"); err != nil {
		return err
	}
	var raw []json.RawMessage
	if json.Unmarshal(o["buttons"], &raw) != nil || len(raw) < 1 || len(raw) > maxKeyboardButtons {
		return richError("a button row has from 1 through 8 buttons")
	}
	row := make([]button, 0, len(raw))
	wire := make([]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := parseObject(item)
		if !ok {
			return richError("a button must be an object")
		}
		if err := entry.onlyKeys([]string{"text"}, "style", "url", "callback_data"); err != nil {
			return err
		}
		var b button
		if json.Unmarshal(entry["text"], &b.Text) != nil {
			return richError("a button text must be text")
		}
		for name, target := range map[string]*string{"url": &b.URL, "callback_data": &b.CallbackData} {
			if _, present := entry[name]; present && json.Unmarshal(entry[name], target) != nil {
				return richError("a button field must be text")
			}
		}
		out := map[string]any{"text": b.Text}
		if b.URL != "" {
			out["url"] = b.URL
		}
		if b.CallbackData != "" {
			out["callback_data"] = b.CallbackData
		}
		if err := entry.optionalEnum(out, "style", "danger", "success", "primary", "link"); err != nil {
			return err
		}
		if out["style"] == "link" && b.CallbackData == "" {
			return richError("the link button style is allowed only for callback buttons")
		}
		row = append(row, b)
		wire = append(wire, out)
	}
	if msg := validateKeyboard(keyboard{row}); msg != "" {
		return richError(msg)
	}
	out["buttons"] = wire
	return o.optionalEnum(out, "align", "left", "center", "right")
}

// richText rebuilds a text: a string, a list of texts, or one of the modeled styles.
func richText(raw json.RawMessage, depth int) (any, error) {
	if depth > maxRichTextDepth {
		return nil, richError("a text is nested too deeply")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, richError("a text is malformed")
	}
	switch raw[0] {
	case '"':
		var value string
		if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) ||
			utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > maxRichString {
			return nil, richError("a text is outside the supported length")
		}
		return value, nil
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) < 1 || len(items) > maxRichTextItems {
			return nil, richError("a text list has from 1 through 100 items")
		}
		out := make([]any, 0, len(items))
		for _, item := range items {
			part, err := richText(item, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, part)
		}
		return out, nil
	}
	o, ok := parseObject(raw)
	if !ok {
		return nil, richError("a text is malformed")
	}
	var kind string
	_ = json.Unmarshal(o["type"], &kind)
	switch {
	case richSimpleText[kind]:
		if err := o.only([]string{"text"}); err != nil {
			return nil, err
		}
		inner, err := richText(o["text"], depth+1)
		return map[string]any{"type": kind, "text": inner}, err
	case kind == "url":
		if err := o.only([]string{"text", "url"}); err != nil {
			return nil, err
		}
		inner, err := richText(o["text"], depth+1)
		if err != nil {
			return nil, err
		}
		var link string
		if json.Unmarshal(o["url"], &link) != nil || !validButtonURL(link) {
			return nil, richError("a text link must be a plain https:// or tg:// link")
		}
		return map[string]any{"type": kind, "text": inner, "url": link}, nil
	case kind == "mathematical_expression":
		if err := o.only([]string{"expression"}); err != nil {
			return nil, err
		}
		expression, err := o.str("expression", maxRichString)
		return map[string]any{"type": kind, "expression": expression}, err
	}
	return nil, richError("a text has an unknown type")
}

func containsString(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}
