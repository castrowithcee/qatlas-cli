package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupStories = "stories"

	// maxStoryCaptionRunes is the Bot API's limit for a story caption.
	maxStoryCaptionRunes = 2048
	// maxStoryVideoBytes is a conservative local bound for a story video; the photo bound is maxPhotoBytes.
	maxStoryVideoBytes = 30 << 20
	// maxStoryVideoSeconds bounds the optional duration and cover frame of a story video.
	maxStoryVideoSeconds = 60
	// storyAttachName is the multipart part that the attach:// reference of the story content points to.
	storyAttachName = "story"
)

// storyPeriods are the only active periods Telegram accepts, in seconds: 6, 12, 24, and 48 hours.
var storyPeriods = [...]int64{21600, 43200, 86400, 172800}

const (
	storyPeriodSchema = `"active_period":{"type":"integer","enum":[21600,43200,86400,172800]}`
	storyIDSchema     = `"story_id":{"type":"integer","minimum":1}`
	storyContentProps = `"type":{"type":"string","enum":["photo","video"]},"` + localfile.LocalPathArgument + `":` +
		localfile.LocalPathSchema + `,` +
		`"duration":{"type":"number","minimum":0,"maximum":60},` +
		`"cover_frame_timestamp":{"type":"number","minimum":0,"maximum":60},"is_animation":{"type":"boolean"}`
	storyCaptionProps = `"caption":{"type":"string","minLength":1,"maxLength":2048},` +
		`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]}`
	storyResultSchema = `{"type":"object","properties":{"story_id":{"type":"integer"}},` +
		`"required":["story_id"],"additionalProperties":false}`
)

var storyFields = []capability.Field{{Name: "story_id", Description: "Identifier of the story in the business account"}}

func storyRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: businessDataSensitivity}
}

var storyPeriodArgument = capability.Argument{Name: "active_period", Required: true,
	Description: "How long the story stays visible, in seconds: 21600 (6 h), 43200 (12 h), 86400 (24 h), or 172800 (48 h)"}

var storyBusinessArgument = capability.Argument{Name: "business", Description: businessArgument, Required: true}

func storyContentArguments() []capability.Argument {
	return []capability.Argument{
		{Name: "type", Description: "photo or video", Required: true},
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local file, absolute or starting with ~/, " +
			"inside a directory the connection releases for reading; a photo up to 10 MB, a video up to 30 MB"},
		{Name: "duration", Description: "Video only: precise duration in seconds, from 0 through 60"},
		{Name: "cover_frame_timestamp", Description: "Video only: second of the cover frame, from 0 through 60"},
		{Name: "is_animation", Description: "Video only: whether the video is an animation without sound"},
	}
}

var storiesPost = capability.Descriptor{
	ID:      Provider + ".stories.post",
	Version: 1,
	Title:   "Post a Telegram business story",
	Description: "Post one story as a bound business connection, from a local photo or video in a directory the " +
		"connection releases for reading; no file reference, URL, or areas. The file is sent under a neutral " +
		"name without its path. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "stories", "business", "create", "files"},
	Risk:       storyRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:   Provider,
	Group:      groupStories,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + storyContentProps + `,` +
		storyPeriodSchema + `,` + storyCaptionProps + `,"post_to_chat_page":{"type":"boolean"},` +
		`"protect_content":{"type":"boolean"}},"required":["business","type","local_path","active_period"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(storyResultSchema),
	Arguments: append(append([]capability.Argument{storyBusinessArgument}, storyContentArguments()...),
		storyPeriodArgument,
		capability.Argument{Name: "caption", Description: "Caption, from 1 through 2048 characters"},
		parseModeArgument,
		capability.Argument{Name: "post_to_chat_page", Description: "Keep the story on the profile page after it expires"},
		capability.Argument{Name: "protect_content", Description: "Forbid forwarding and saving of the story"}),
	Fields: storyFields,
	Examples: []capability.Example{{
		Description: "Post a photo story for 24 hours",
		Arguments:   json.RawMessage(`{"business":"<connection id>","type":"photo","local_path":"~/stories/a.jpg","active_period":86400}`),
	}},
}

var storiesEdit = capability.Descriptor{
	ID:      Provider + ".stories.edit",
	Version: 1,
	Title:   "Edit a Telegram business story",
	Description: "Replace the content and caption of one story of a bound business connection with a local photo or " +
		"video in a directory the connection releases for reading; no file reference, URL, or areas. The file is " +
		"sent under a neutral name without its path. An unclear outcome is never repeated",
	Tags:       []string{"telegram", "stories", "business", "update", "files"},
	Risk:       storyRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:   Provider,
	Group:      groupStories,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + storyIDSchema + `,` +
		storyContentProps + `,` + storyCaptionProps + `},"required":["business","story_id","type","local_path"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(storyResultSchema),
	Arguments: append(append([]capability.Argument{storyBusinessArgument,
		{Name: "story_id", Description: "Positive identifier of the story", Required: true}}, storyContentArguments()...),
		capability.Argument{Name: "caption", Description: "New caption, from 1 through 2048 characters"},
		parseModeArgument),
	Fields: storyFields,
	Examples: []capability.Example{{
		Description: "Replace the photo of a story",
		Arguments:   json.RawMessage(`{"business":"<connection id>","story_id":12,"type":"photo","local_path":"~/stories/b.jpg"}`),
	}},
}

var storiesRepost = capability.Descriptor{
	ID:      Provider + ".stories.repost",
	Version: 1,
	Title:   "Repost a Telegram business story",
	Description: "Repost one story of a bound business connection as another, or the same, bound business " +
		"connection. The source chat is read from the source connection and never taken from the caller. " +
		"An unclear outcome is never repeated",
	Tags:     []string{"telegram", "stories", "business", "create"},
	Risk:     storyRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	Group:    groupStories,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` +
		`"from_business":{"type":"string","minLength":1,"maxLength":128},"from_story_id":{"type":"integer","minimum":1},` +
		storyPeriodSchema + `,"post_to_chat_page":{"type":"boolean"},"protect_content":{"type":"boolean"}},` +
		`"required":["business","from_business","from_story_id","active_period"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(storyResultSchema),
	Arguments: []capability.Argument{
		{Name: "business", Description: "Business connection ID that posts the repost, as bound by a business/CONNECTION_ID target of this connection", Required: true},
		{Name: "from_business", Description: "Business connection ID that owns the source story, also bound by a business/CONNECTION_ID target; may equal business", Required: true},
		{Name: "from_story_id", Description: "Positive identifier of the source story", Required: true},
		storyPeriodArgument,
		{Name: "post_to_chat_page", Description: "Keep the story on the profile page after it expires"},
		{Name: "protect_content", Description: "Forbid forwarding and saving of the story"},
	},
	Fields: storyFields,
	Examples: []capability.Example{{
		Description: "Repost a story for 24 hours",
		Arguments:   json.RawMessage(`{"business":"<connection id>","from_business":"<source connection id>","from_story_id":12,"active_period":86400}`),
	}},
}

var storiesDelete = capability.Descriptor{
	ID:          Provider + ".stories.delete",
	Version:     1,
	Title:       "Delete a Telegram business story",
	Description: "Delete one story of a bound business connection",
	Tags:        []string{"telegram", "stories", "business", "delete"},
	Risk:        storyRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:    Provider, RequiresToolAllowList: true, Group: groupStories,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + storyIDSchema + `},` +
		`"required":["business","story_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{storyBusinessArgument,
		{Name: "story_id", Description: "Positive identifier of the story", Required: true}},
	Fields: []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the deletion"}},
	Examples: []capability.Example{{
		Description: "Delete a story",
		Arguments:   json.RawMessage(`{"business":"<connection id>","story_id":12}`),
	}},
}

// storyContent is the local content of a story as the caller wrote it.
type storyContent struct {
	Type                string   `json:"type"`
	LocalPath           *string  `json:"local_path"`
	Duration            *float64 `json:"duration"`
	CoverFrameTimestamp *float64 `json:"cover_frame_timestamp"`
	IsAnimation         *bool    `json:"is_animation"`
}

func (s storyContent) validate(op string) error {
	switch s.Type {
	case kindPhoto:
		if s.Duration != nil || s.CoverFrameTimestamp != nil || s.IsAnimation != nil {
			return providerError(op, "duration, cover_frame_timestamp, and is_animation apply to a video only")
		}
	case kindVideo:
		for _, v := range []*float64{s.Duration, s.CoverFrameTimestamp} {
			if v != nil && (*v < 0 || *v > maxStoryVideoSeconds) {
				return providerError(op, "duration and cover_frame_timestamp must lie between 0 and 60 seconds")
			}
		}
	default:
		return providerError(op, "the story type must be photo or video")
	}
	if s.LocalPath == nil {
		return providerError(op, "local_path is required")
	}
	return nil
}

func validStoryCaption(op, caption, parseMode string) error {
	switch {
	case !utf8.ValidString(caption) || utf8.RuneCountInString(caption) > maxStoryCaptionRunes:
		return providerError(op, "the caption is longer than 2048 characters")
	case !validParseMode(parseMode):
		return providerError(op, "the parse mode is not supported")
	}
	return nil
}

func validStoryPeriod(op string, seconds int64) error {
	for _, p := range storyPeriods {
		if p == seconds {
			return nil
		}
	}
	return providerError(op, "active_period must be 21600, 43200, 86400, or 172800 seconds")
}

func validStoryID(op string, id int64) error {
	if id < 1 {
		return providerError(op, "the story identifier must be positive")
	}
	return nil
}

// openStoryContent opens the local file once the release and the size bound are checked, so a path outside the
// release or an oversized file is refused before the credential is resolved.
func openStoryContent(ctx context.Context, resolved *config.Resolved, op string, s storyContent) (*localfile.Upload, error) {
	upload, err := localfile.OpenForUpload(ctx, resolved, *s.LocalPath)
	if err != nil {
		return nil, err
	}
	limit := int64(maxPhotoBytes)
	if s.Type == kindVideo {
		limit = maxStoryVideoBytes
	}
	if upload.Size == 0 || upload.Size > limit {
		_ = upload.Close()
		return nil, providerError(op, "the file is empty or larger than allowed for this kind of story")
	}
	return upload, nil
}

// storyItem is the content of a story ready to send.
type storyItem struct {
	storyContent
	name string
	data []byte
}

func readStoryContent(upload *localfile.Upload, s storyContent) (storyItem, error) {
	data, err := io.ReadAll(upload)
	if err != nil {
		return storyItem{}, err
	}
	return storyItem{storyContent: s, name: neutralName(0, 1, upload.Name), data: data}, nil
}

func (i storyItem) encode(op string) (string, error) {
	type photo struct {
		Type  string `json:"type"`
		Photo string `json:"photo"`
	}
	type video struct {
		Type                string   `json:"type"`
		Video               string   `json:"video"`
		Duration            *float64 `json:"duration,omitempty"`
		CoverFrameTimestamp *float64 `json:"cover_frame_timestamp,omitempty"`
		IsAnimation         *bool    `json:"is_animation,omitempty"`
	}
	ref := "attach://" + storyAttachName
	var value any = photo{kindPhoto, ref}
	if i.Type == kindVideo {
		value = video{kindVideo, ref, i.Duration, i.CoverFrameTimestamp, i.IsAnimation}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", providerError(op, "the request could not be encoded")
	}
	return string(encoded), nil
}

// storyID accepts only a Story answer that carries a positive identifier.
func storyID(raw json.RawMessage) (int64, bool) {
	var story struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &story) != nil || story.ID < 1 {
		return 0, false
	}
	return story.ID, true
}

func invokeStoriesPost(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStoriesPostWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStoriesPostWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "post story"
	var arguments struct {
		Business string `json:"business"`
		storyContent
		ActivePeriod   int64  `json:"active_period"`
		Caption        string `json:"caption"`
		ParseMode      string `json:"parse_mode"`
		PostToChatPage bool   `json:"post_to_chat_page"`
		ProtectContent bool   `json:"protect_content"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := arguments.validate(op); err != nil {
		return nil, err
	}
	if err := validStoryPeriod(op, arguments.ActivePeriod); err != nil {
		return nil, err
	}
	if err := validStoryCaption(op, arguments.Caption, arguments.ParseMode); err != nil {
		return nil, err
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	upload, err := openStoryContent(ctx, resolved, op, arguments.storyContent)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	item, err := readStoryContent(upload, arguments.storyContent)
	if err != nil {
		return nil, err
	}
	return client.PostStory(ctx, arguments.Business, item, arguments.ActivePeriod, arguments.Caption,
		arguments.ParseMode, arguments.PostToChatPage, arguments.ProtectContent)
}

// PostStory performs exactly one multipart postStory request without retrying an ambiguous result.
func (c *Client) PostStory(ctx context.Context, id string, item storyItem, activePeriod int64, caption, parseMode string,
	postToChatPage, protectContent bool) (map[string]any, error) {
	const op = "post story"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if err := item.validate(op); err != nil {
		return nil, err
	}
	if err := validStoryPeriod(op, activePeriod); err != nil {
		return nil, err
	}
	if err := validStoryCaption(op, caption, parseMode); err != nil {
		return nil, err
	}
	content, err := item.encode(op)
	if err != nil {
		return nil, err
	}
	fields := []field{{"business_connection_id", id}, {"content", content},
		{"active_period", strconv.FormatInt(activePeriod, 10)}}
	fields = appendStoryText(fields, caption, parseMode)
	if postToChatPage {
		fields = append(fields, field{"post_to_chat_page", "true"})
	}
	if protectContent {
		fields = append(fields, field{"protect_content", "true"})
	}
	return c.sendStory(ctx, spec{op: op, method: "postStory", limit: defaultResponseBytes}, fields, item, 0)
}

func appendStoryText(fields []field, caption, parseMode string) []field {
	if caption == "" {
		return fields
	}
	fields = append(fields, field{"caption", caption})
	if parseMode != "" {
		fields = append(fields, field{"parse_mode", parseMode})
	}
	return fields
}

// sendStory sends the one multipart request of postStory or editStory; wantID is the story that an edit must answer.
func (c *Client) sendStory(ctx context.Context, s spec, fields []field, item storyItem, wantID int64) (map[string]any, error) {
	if len(item.data) == 0 {
		return nil, providerError(s.op, "the file is empty")
	}
	raw, err := c.withUploadTimeout().callMultipart(ctx, s, fields,
		[]filePart{{field: storyAttachName, name: item.name, data: item.data}})
	if err != nil {
		return nil, err
	}
	got, ok := storyID(raw)
	if !ok || (wantID != 0 && got != wantID) {
		return nil, withUncertainty(spec{}, invalidResponse(s.op))
	}
	return map[string]any{"story_id": got}, nil
}

func invokeStoriesEdit(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStoriesEditWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStoriesEditWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "edit story"
	var arguments struct {
		Business string `json:"business"`
		StoryID  int64  `json:"story_id"`
		storyContent
		Caption   string `json:"caption"`
		ParseMode string `json:"parse_mode"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validStoryID(op, arguments.StoryID); err != nil {
		return nil, err
	}
	if err := arguments.validate(op); err != nil {
		return nil, err
	}
	if err := validStoryCaption(op, arguments.Caption, arguments.ParseMode); err != nil {
		return nil, err
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	upload, err := openStoryContent(ctx, resolved, op, arguments.storyContent)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	item, err := readStoryContent(upload, arguments.storyContent)
	if err != nil {
		return nil, err
	}
	return client.EditStory(ctx, arguments.Business, arguments.StoryID, item, arguments.Caption, arguments.ParseMode)
}

// EditStory performs exactly one multipart editStory request without retrying an ambiguous result.
func (c *Client) EditStory(ctx context.Context, id string, storyID int64, item storyItem, caption, parseMode string) (map[string]any, error) {
	const op = "edit story"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if err := validStoryID(op, storyID); err != nil {
		return nil, err
	}
	if err := item.validate(op); err != nil {
		return nil, err
	}
	if err := validStoryCaption(op, caption, parseMode); err != nil {
		return nil, err
	}
	content, err := item.encode(op)
	if err != nil {
		return nil, err
	}
	fields := []field{{"business_connection_id", id}, {"story_id", strconv.FormatInt(storyID, 10)}, {"content", content}}
	fields = appendStoryText(fields, caption, parseMode)
	return c.sendStory(ctx, spec{op: op, method: "editStory", limit: defaultResponseBytes}, fields, item, storyID)
}

func invokeStoriesRepost(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStoriesRepostWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStoriesRepostWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "repost story"
	var arguments struct {
		Business       string `json:"business"`
		FromBusiness   string `json:"from_business"`
		FromStoryID    int64  `json:"from_story_id"`
		ActivePeriod   int64  `json:"active_period"`
		PostToChatPage bool   `json:"post_to_chat_page"`
		ProtectContent bool   `json:"protect_content"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validStoryID(op, arguments.FromStoryID); err != nil {
		return nil, err
	}
	if err := validStoryPeriod(op, arguments.ActivePeriod); err != nil {
		return nil, err
	}
	// Both connections must be bound before the credential is resolved.
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	if err := scopeBusiness(op, resolved, arguments.FromBusiness); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.RepostStory(ctx, arguments.Business, arguments.FromBusiness, arguments.FromStoryID,
		arguments.ActivePeriod, arguments.PostToChatPage, arguments.ProtectContent)
}

// RepostStory reads the source chat from the source connection with one getBusinessConnection request, then
// performs exactly one repostStory request without retrying an ambiguous result.
func (c *Client) RepostStory(ctx context.Context, id, fromID string, fromStoryID, activePeriod int64,
	postToChatPage, protectContent bool) (map[string]any, error) {
	const op = "repost story"
	if !validBusinessID(id) || !c.set.hasBusiness(id) || !validBusinessID(fromID) || !c.set.hasBusiness(fromID) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if err := validStoryID(op, fromStoryID); err != nil {
		return nil, err
	}
	if err := validStoryPeriod(op, activePeriod); err != nil {
		return nil, err
	}
	source, err := c.fetchBusinessConnection(ctx, op, fromID)
	if err != nil {
		return nil, err
	}
	if source.UserChatID == 0 {
		return nil, invalidResponse(op)
	}
	raw, err := c.call(ctx, spec{op: op, method: "repostStory", limit: defaultResponseBytes}, struct {
		ID             string `json:"business_connection_id"`
		FromChatID     int64  `json:"from_chat_id"`
		FromStoryID    int64  `json:"from_story_id"`
		ActivePeriod   int64  `json:"active_period"`
		PostToChatPage bool   `json:"post_to_chat_page,omitempty"`
		ProtectContent bool   `json:"protect_content,omitempty"`
	}{id, source.UserChatID, fromStoryID, activePeriod, postToChatPage, protectContent})
	if err != nil {
		return nil, err
	}
	got, ok := storyID(raw)
	if !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"story_id": got}, nil
}

func invokeStoriesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeStoriesDeleteWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeStoriesDeleteWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "delete story"
	var arguments struct {
		Business string `json:"business"`
		StoryID  int64  `json:"story_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validStoryID(op, arguments.StoryID); err != nil {
		return nil, err
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.DeleteStory(ctx, arguments.Business, arguments.StoryID)
}

// DeleteStory performs exactly one deleteStory request without retrying an ambiguous result.
func (c *Client) DeleteStory(ctx context.Context, id string, storyID int64) (map[string]any, error) {
	const op = "delete story"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if err := validStoryID(op, storyID); err != nil {
		return nil, err
	}
	err := c.confirmed(ctx, spec{op: op, method: "deleteStory", limit: defaultResponseBytes}, struct {
		ID      string `json:"business_connection_id"`
		StoryID int64  `json:"story_id"`
	}{id, storyID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
