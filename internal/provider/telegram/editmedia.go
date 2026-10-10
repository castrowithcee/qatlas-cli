package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// editMediaKinds is the fixed list of media types editMessageMedia accepts here.
var editMediaKinds = []string{kindPhoto, kindVideo, kindAnimation, kindDocument}

const (
	editTargetSchema = chatSchema + `,"message_id":{"type":"integer","minimum":1}`
	editResultSchema = `{"type":"object","properties":{"message_id":{"type":"integer"}},` +
		`"required":["message_id"],"additionalProperties":false}`
	editMediaResultSchema = `{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"file_ref":{"type":"string"}},"required":["message_id"],"additionalProperties":false}`
)

var messageIDArgument = capability.Argument{
	Name: "message_id", Description: "Identifier of the message in the chat", Required: true,
}

var editedFields = []capability.Field{{Name: "message_id", Description: "Edited Telegram message identifier"}}

func liveRisk() capability.Risk {
	return capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: personalDataSensitivity,
	}
}

var messagesEditCaption = capability.Descriptor{
	ID:          Provider + ".messages.editcaption",
	Version:     1,
	Title:       "Edit the caption of a Telegram message",
	Description: "Replace or remove the caption of one media message in a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "messages", "edit", "caption"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupMessages,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + editTargetSchema + `,` +
		`"caption":{"type":"string","maxLength":1024},"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]}},` +
		`"required":["message_id","caption"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(editResultSchema),
	Arguments: []capability.Argument{
		chatArgument, messageIDArgument,
		{Name: "caption", Description: "New caption, from 0 through 1024 characters; an empty caption removes it", Required: true},
		parseModeArgument,
	},
	Fields: editedFields,
	Examples: []capability.Example{{
		Description: "Replace the caption of a media message sent to this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"caption":"Quarterly report"}`),
	}},
}

var messagesEditMedia = capability.Descriptor{
	ID:      Provider + ".messages.editmedia",
	Version: 1,
	Title:   "Replace the media of a Telegram message",
	Description: "Replace the photo, video, animation, or document of one message in a bound chat of an explicit " +
		"Telegram connection, from local_path in a directory the connection releases for reading or from a signed " +
		"file_ref. A local file is sent under a neutral name without its path. An unclear outcome is never repeated",
	Tags: []string{"telegram", "messages", "edit", "media", "files"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider:   Provider,
	Group:      groupMessages,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + editTargetSchema + `,` +
		`"type":{"type":"string","enum":["photo","video","animation","document"]},` + sourceProperties + `,` +
		`"caption":{"type":"string","minLength":1,"maxLength":1024},` +
		`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]}},"required":["message_id","type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(editMediaResultSchema),
	Arguments: []capability.Argument{
		chatArgument, messageIDArgument,
		{Name: "type", Description: "Type of the new media: photo, video, animation, or document", Required: true},
		func() capability.Argument {
			a := localfile.UploadPathArgument()
			a.Description = "Local file with the new media, absolute or starting with ~/, inside a directory the " +
				"connection releases for reading, up to 10 MB for a photo and 50 MB otherwise; instead of file_ref"
			return a
		}(),
		{Name: "file_ref", Description: "Signed file reference (media.file_ref) of a file received in the same bound " +
			"chat or returned by an earlier send to it; instead of " + localfile.LocalPathArgument},
		{Name: "caption", Description: "Caption of the new media, from 1 through 1024 characters; the message has " +
			"no caption when omitted"},
		parseModeArgument,
	},
	Fields: []capability.Field{
		{Name: "message_id", Description: "Edited Telegram message identifier"},
		{Name: "file_ref", Description: "Signed reference of the new file, to send it again with the media tools"},
	},
	Examples: []capability.Example{{
		Description: "Replace the photo of a message sent to this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"type":"photo","local_path":"~/reports/chart.jpg"}`),
	}},
}

var locationsEditLive = capability.Descriptor{
	ID:          Provider + ".locations.editlive",
	Version:     1,
	Title:       "Update a Telegram live location",
	Description: "Move one live location of a bound chat of an explicit Telegram connection to new coordinates",
	Tags:        []string{"telegram", "locations", "edit", "live"},
	Risk:        liveRisk(),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + editTargetSchema + `,` + coordinateSchema + `,` +
		`"horizontal_accuracy":{"type":"number","minimum":0,"maximum":1500},` +
		`"live_period":{"type":"integer","minimum":60,"maximum":2147483647},` +
		`"heading":{"type":"integer","minimum":1,"maximum":360},` +
		`"proximity_alert_radius":{"type":"integer","minimum":1,"maximum":100000}},` +
		`"required":["message_id","latitude","longitude"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(editResultSchema),
	Arguments: []capability.Argument{
		chatArgument, messageIDArgument,
		{Name: "latitude", Description: "Latitude from -90 through 90", Required: true},
		{Name: "longitude", Description: "Longitude from -180 through 180", Required: true},
		{Name: "horizontal_accuracy", Description: "Radius of uncertainty in meters, from 0 through 1500"},
		{Name: "live_period", Description: "New period in seconds from the send date of the message, from 60 " +
			"through 86400, or 2147483647 for as long as Telegram allows; the current period is kept when omitted"},
		{Name: "heading", Description: "Direction of movement in degrees, from 1 through 360"},
		{Name: "proximity_alert_radius", Description: "Alert distance in meters, from 1 through 100000"},
	},
	Fields: editedFields,
	Examples: []capability.Example{{
		Description: "Move a live location sent to this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91,"latitude":52.52,"longitude":13.405}`),
	}},
}

var locationsStopLive = capability.Descriptor{
	ID:          Provider + ".locations.stoplive",
	Version:     1,
	Title:       "Stop a Telegram live location",
	Description: "Stop updating one live location of a bound chat of an explicit Telegram connection before it expires",
	Tags:        []string{"telegram", "locations", "stop", "live"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: personalDataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group:        groupInteractions,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{` + editTargetSchema + `},"required":["message_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(editResultSchema),
	Arguments:    []capability.Argument{chatArgument, messageIDArgument},
	Fields:       editedFields,
	Examples: []capability.Example{{
		Description: "Stop a live location sent to this connection's chat",
		Arguments:   json.RawMessage(`{"message_id":91}`),
	}},
}

func validMessageID(op string, id int64) error {
	if id < 1 {
		return providerError(op, "the message identifier must be positive")
	}
	return nil
}

func validCaptionText(op, caption, parseMode string) error {
	switch {
	case !utf8.ValidString(caption) || utf8.RuneCountInString(caption) > maxCaptionRunes:
		return providerError(op, "the caption is longer than 1024 characters")
	case !validParseMode(parseMode):
		return providerError(op, "the parse mode is not supported")
	}
	return nil
}

func validEditMediaKind(op, kind string) error {
	for _, k := range editMediaKinds {
		if k == kind {
			return nil
		}
	}
	return providerError(op, "the media type must be photo, video, animation, or document")
}

func invokeMessagesEditCaption(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "edit caption"
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		Caption   string `json:"caption"`
		ParseMode string `json:"parse_mode"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validMessageID(op, arguments.MessageID); err != nil {
		return nil, err
	}
	if err := validCaptionText(op, arguments.Caption, arguments.ParseMode); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.EditCaption(ctx, arguments.MessageID, arguments.Caption, arguments.ParseMode)
}

func invokeMessagesEditMedia(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMessagesEditMediaWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeMessagesEditMediaWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "edit media"
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		Type      string `json:"type"`
		mediaSource
		Caption   string `json:"caption"`
		ParseMode string `json:"parse_mode"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validMessageID(op, arguments.MessageID); err != nil {
		return nil, err
	}
	if err := validEditMediaKind(op, arguments.Type); err != nil {
		return nil, err
	}
	if err := validCaptionText(op, arguments.Caption, arguments.ParseMode); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	chat, err := selectChat(resolved, arguments.Chat)
	if err != nil {
		return nil, err
	}
	prepared, err := prepareSources(ctx, resolved, op, chat, []string{arguments.Type}, []mediaSource{arguments.mediaSource})
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
	return client.EditMedia(ctx, arguments.MessageID, items[0], arguments.Caption, arguments.ParseMode)
}

// LiveLocationOptions are the options of one editMessageLiveLocation request.
type LiveLocationOptions struct {
	Latitude             *float64 `json:"latitude"`
	Longitude            *float64 `json:"longitude"`
	HorizontalAccuracy   *float64 `json:"horizontal_accuracy"`
	LivePeriod           *int64   `json:"live_period"`
	Heading              *int64   `json:"heading"`
	ProximityAlertRadius *int64   `json:"proximity_alert_radius"`
}

func (o LiveLocationOptions) validate(messageID int64) error {
	const op = "edit live location"
	if err := validMessageID(op, messageID); err != nil {
		return err
	}
	if err := validCoordinates(op, o.Latitude, o.Longitude); err != nil {
		return err
	}
	if err := validAccuracyAndPeriod(op, o.HorizontalAccuracy, o.LivePeriod); err != nil {
		return err
	}
	return validHeadingAndRadius(op, o.Heading, o.ProximityAlertRadius)
}

func invokeLocationsEditLive(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
		LiveLocationOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("edit live location", "the validated arguments could not be read")
	}
	if err := arguments.LiveLocationOptions.validate(arguments.MessageID); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.EditLiveLocation(ctx, arguments.MessageID, arguments.LiveLocationOptions)
}

func invokeLocationsStopLive(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat      string `json:"chat"`
		MessageID int64  `json:"message_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("stop live location", "the validated arguments could not be read")
	}
	if err := validMessageID("stop live location", arguments.MessageID); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.StopLiveLocation(ctx, arguments.MessageID)
}

// editMessage performs exactly one request and reads only the identifier of the edited message. An inline
// message result (true) cannot occur because no inline_message_id is ever sent.
func (c *Client) editMessage(ctx context.Context, s spec, messageID int64, payload any) (json.RawMessage, error) {
	if c.target == "" {
		return nil, providerError(s.op, "no chat was selected")
	}
	raw, err := c.call(ctx, s, payload)
	if err != nil {
		return nil, err
	}
	if !editedMessageMatches(raw, messageID) {
		return nil, withUncertainty(spec{}, invalidResponse(s.op))
	}
	return raw, nil
}

func editedMessageMatches(raw json.RawMessage, messageID int64) bool {
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	return json.Unmarshal(raw, &result) == nil && result.MessageID == messageID
}

// EditCaption performs exactly one editMessageCaption request. An empty caption removes the caption; the
// parse mode is sent only together with a caption.
func (c *Client) EditCaption(ctx context.Context, messageID int64, caption, parseMode string) (map[string]any, error) {
	const op = "edit caption"
	if err := validMessageID(op, messageID); err != nil {
		return nil, err
	}
	if err := validCaptionText(op, caption, parseMode); err != nil {
		return nil, err
	}
	if caption == "" {
		parseMode = ""
	}
	_, err := c.editMessage(ctx, spec{op: op, method: "editMessageCaption", limit: defaultResponseBytes}, messageID,
		struct {
			ChatID    string `json:"chat_id"`
			MessageID int64  `json:"message_id"`
			Caption   string `json:"caption"`
			ParseMode string `json:"parse_mode,omitempty"`
		}{c.target, messageID, caption, parseMode})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": messageID}, nil
}

// EditMedia performs exactly one editMessageMedia request: JSON for a verified file identifier, multipart
// with an attach:// reference for a local file.
func (c *Client) EditMedia(ctx context.Context, messageID int64, item mediaItem, caption, parseMode string) (map[string]any, error) {
	const op = "edit media"
	if err := validMessageID(op, messageID); err != nil {
		return nil, err
	}
	if err := validEditMediaKind(op, item.kind); err != nil {
		return nil, err
	}
	if err := validCaptionText(op, caption, parseMode); err != nil {
		return nil, err
	}
	if c.target == "" {
		return nil, providerError(op, "no chat was selected")
	}
	media := inputMedia{Type: item.kind, Caption: caption}
	if caption != "" {
		media.ParseMode = parseMode
	}
	s := spec{op: op, method: "editMessageMedia", limit: defaultResponseBytes}
	var (
		raw json.RawMessage
		err error
	)
	if item.fileID != "" {
		media.Media = item.fileID
		raw, err = c.call(ctx, s, struct {
			ChatID    string     `json:"chat_id"`
			MessageID int64      `json:"message_id"`
			Media     inputMedia `json:"media"`
		}{c.target, messageID, media})
	} else {
		media.Media = "attach://file0"
		encoded, marshalErr := json.Marshal(media)
		if marshalErr != nil {
			return nil, providerError(op, "the request could not be encoded")
		}
		raw, err = c.withUploadTimeout().callMultipart(ctx, s, []field{
			{"chat_id", c.target}, {"message_id", strconv.FormatInt(messageID, 10)}, {"media", string(encoded)},
		}, []filePart{{field: "file0", name: item.name, data: item.data}})
	}
	if err != nil {
		return nil, err
	}
	sent, ok := c.parseSent(raw, item.kind)
	if !ok || sent["message_id"] != messageID {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	delete(sent, "date")
	return sent, nil
}

// EditLiveLocation performs exactly one editMessageLiveLocation request without retrying an ambiguous result.
func (c *Client) EditLiveLocation(ctx context.Context, messageID int64, o LiveLocationOptions) (map[string]any, error) {
	const op = "edit live location"
	if err := o.validate(messageID); err != nil {
		return nil, err
	}
	_, err := c.editMessage(ctx, spec{op: op, method: "editMessageLiveLocation", limit: structuredResponseOver}, messageID,
		struct {
			ChatID               string   `json:"chat_id"`
			MessageID            int64    `json:"message_id"`
			Latitude             float64  `json:"latitude"`
			Longitude            float64  `json:"longitude"`
			HorizontalAccuracy   *float64 `json:"horizontal_accuracy,omitempty"`
			LivePeriod           *int64   `json:"live_period,omitempty"`
			Heading              *int64   `json:"heading,omitempty"`
			ProximityAlertRadius *int64   `json:"proximity_alert_radius,omitempty"`
		}{c.target, messageID, *o.Latitude, *o.Longitude, o.HorizontalAccuracy, o.LivePeriod, o.Heading,
			o.ProximityAlertRadius})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": messageID}, nil
}

// StopLiveLocation performs exactly one stopMessageLiveLocation request without retrying an ambiguous result.
func (c *Client) StopLiveLocation(ctx context.Context, messageID int64) (map[string]any, error) {
	const op = "stop live location"
	if err := validMessageID(op, messageID); err != nil {
		return nil, err
	}
	_, err := c.editMessage(ctx, spec{op: op, method: "stopMessageLiveLocation", limit: structuredResponseOver}, messageID,
		struct {
			ChatID    string `json:"chat_id"`
			MessageID int64  `json:"message_id"`
		}{c.target, messageID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": messageID}, nil
}
