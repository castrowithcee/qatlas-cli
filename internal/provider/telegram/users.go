package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const groupUsers = "users"

const (
	// maxUserItems bounds getUserPersonalChatMessages, which Telegram itself limits to 20, and is the default page.
	maxUserItems = 20
	// maxProfileItems is the Bot API maximum of getUserProfilePhotos and getUserProfileAudios, and the number of
	// boosts printed.
	maxProfileItems = 100
	// profileResponseMax holds 100 profile photos with every size.
	profileResponseMax = 512 << 10
)

const userInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`},"required":["user_id"],"additionalProperties":false}`
const userPageInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`,"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100}},` +
	`"required":["user_id"],"additionalProperties":false}`
const userMessagesInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`,"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["user_id"],"additionalProperties":false}`

var usersProfilePhotos = capability.Descriptor{
	ID: Provider + ".users.profile-photos", Version: 1, Title: "Read a chat member's profile photos",
	Description: "Read profile photos of a user verified as a member of a bound chat; the answer is limited to 512 KiB",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userPageInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"total_count":{"type":"integer"},"photos":{"type":"array","items":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"width":{"type":"integer"},"height":{"type":"integer"},` +
		`"file_size":{"type":"integer"},"sizes":{"type":"array","items":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"file_unique_id":{"type":"string"},"width":{"type":"integer"},` +
		`"height":{"type":"integer"},"file_size":{"type":"integer"}}}}}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "offset", Description: "First profile photo, starting at zero"},
		{Name: "limit", Description: "Maximum photos, 1 through 100; defaults to 20"}},
	Fields: []capability.Field{{Name: "total_count", Description: "Total profile photos reported by Telegram"},
		{Name: "photos", Description: "Each photo with its largest size and sizes, the list of all its sizes, as chat-bound signed file references"}},
}

var usersProfileAudios = capability.Descriptor{
	ID: Provider + ".users.profile-audios", Version: 1, Title: "Read a chat member's profile audios",
	Description: "Read profile audios of a user verified as a member of a bound chat; the answer is limited to 512 KiB",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userPageInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"total_count":{"type":"integer"},"audios":{"type":"array","items":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"file_unique_id":{"type":"string"},"duration":{"type":"integer"},` +
		`"title":{"type":"string"},"performer":{"type":"string"},"file_name":{"type":"string"},` +
		`"mime_type":{"type":"string"},"file_size":{"type":"integer"},"thumbnail":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"file_unique_id":{"type":"string"},"width":{"type":"integer"},` +
		`"height":{"type":"integer"},"file_size":{"type":"integer"}}}}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "offset", Description: "First profile audio, starting at zero"},
		{Name: "limit", Description: "Maximum audios, 1 through 100; defaults to 20"}},
	Fields: []capability.Field{{Name: "total_count", Description: "Total profile audios reported by Telegram"},
		{Name: "audios", Description: "Profile audios with their thumbnails as chat-bound signed file references"}},
}

var usersChatBoosts = capability.Descriptor{
	ID: Provider + ".users.chat-boosts", Version: 1, Title: "Read a chat member's boosts",
	Description: "Read boosts a verified member added to the bound chat; Telegram requires bot administrator rights; the answer is limited to 512 KiB",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"boosts":{"type":"array","items":{"type":"object","properties":{` +
		`"boost_id":{"type":"string"},"add_date":{"type":"integer"},` +
		`"expiration_date":{"type":"integer"},"source":{"type":"string"},"source_info":{"type":"object","properties":{` +
		`"source":{"type":"string"},"user":` + userSchema + `,"giveaway_message_id":{"type":"integer"},` +
		`"prize_star_count":{"type":"integer"},"is_unclaimed":{"type":"boolean"}}}}}},` +
		`"truncated":{"type":"boolean"}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument},
	Fields: []capability.Field{{Name: "boosts", Description: "Up to 100 boosts of the selected chat; source_info is the boost source with its user, giveaway message and prize"},
		{Name: "truncated", Description: "True if Telegram returned more than 100 boosts"}},
}

var usersPersonalChatMessages = capability.Descriptor{
	ID: Provider + ".users.personal-chat-messages", Version: 1, Title: "Read a chat member's personal channel messages",
	Description: "Read up to 20 recent messages of the personal chat of a verified member",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userMessagesInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"messages":{"type":"array","items":{"type":"object","properties":{` +
		`"message_id":{"type":"integer"},"date":{"type":"integer"},"message_thread_id":{"type":"integer"},` +
		`"from":` + userSchema + `,"text":{"type":"string"},` +
		`"caption":{"type":"string"},"media_kind":{"type":"string"},` +
		`"file_ref":{"type":"string"},` +
		`"media_items":{"type":"array","items":{"type":"object"},"description":"Media objects of the message: the Bot API types Animation, Audio, Document, LivePhoto, PhotoSize (one entry per size), Sticker, Video, VideoNote and Voice, each with kind and its members; every file_id is a file_ref"},` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}}` + messageHeadSchema + `}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "limit", Description: "Maximum recent messages, 1 through 20; defaults to 20"}},
	Fields: []capability.Field{{Name: "messages", Description: "Recent message text, captions and media objects from the user's personal chat; media_items lists the media objects with their Bot API members and chat-bound signed file references, and omitted_fields names, as media_items.<kind> or media_items.<kind>.<member>, a nested object left out for exceeding 8 levels, 100 list elements or 16 KiB; messages also carry from, message_thread_id and " + messageHeadText}},
}

type userArgs struct {
	Chat   string `json:"chat"`
	UserID int64  `json:"user_id"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func invokeUsersProfilePhotos(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read profile photos", maxProfileItems, true, (*Client).ProfilePhotos)
}
func invokeUsersProfileAudios(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read profile audios", maxProfileItems, true, (*Client).ProfileAudios)
}
func invokeUsersChatBoosts(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read chat boosts", maxUserItems, false, (*Client).ChatBoosts)
}
func invokeUsersPersonalChatMessages(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read personal chat messages", maxUserItems, false, (*Client).PersonalChatMessages)
}

func invokeUserRead(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage,
	op string, maxLimit int, page bool, read func(*Client, context.Context, int64, int, int) (any, error)) (any, error) {
	var a userArgs
	if decodeStrict(raw, &a) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if a.UserID <= 0 || a.Offset < 0 || a.Limit < 0 || a.Limit > maxLimit || (!page && a.Offset != 0) {
		return nil, providerError(op, "invalid user or page")
	}
	if a.Limit == 0 {
		a.Limit = maxUserItems // the default page stays 20 for every tool
	}
	c, err := openChat(ctx, r, s, red, a.Chat)
	if err != nil {
		return nil, err
	}
	return read(c, ctx, a.UserID, a.Offset, a.Limit)
}

func (c *Client) requireUserMember(ctx context.Context, userID int64) error {
	const op = "read member data"
	member, err := c.GetChatMember(ctx, userID)
	if err != nil {
		return err
	}
	if member.User.ID != userID {
		return providerError(op, "membership could not be verified")
	}
	switch member.Status {
	case "creator", "administrator", "member":
		return nil
	case "restricted":
		if member.IsMember != nil && *member.IsMember {
			return nil
		}
	}
	return providerError(op, "membership could not be verified")
}

type profilePhoto struct {
	FileRef  string      `json:"file_ref"`
	Width    int64       `json:"width,omitempty"`
	Height   int64       `json:"height,omitempty"`
	FileSize int64       `json:"file_size,omitempty"`
	Sizes    []photoSize `json:"sizes,omitempty"`
}

// profileAudio is the Bot API Audio of a profile with its file as file_ref.
type profileAudio struct {
	FileRef      string     `json:"file_ref"`
	FileUniqueID string     `json:"file_unique_id,omitempty"`
	Duration     int64      `json:"duration,omitempty"`
	Title        string     `json:"title,omitempty"`
	Performer    string     `json:"performer,omitempty"`
	FileName     string     `json:"file_name,omitempty"`
	MimeType     string     `json:"mime_type,omitempty"`
	FileSize     int64      `json:"file_size,omitempty"`
	Thumbnail    *photoSize `json:"thumbnail,omitempty"`
}

func (c *Client) ProfilePhotos(ctx context.Context, userID int64, offset, limit int) (any, error) {
	const op = "read profile photos"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserProfilePhotos", limit: profileResponseMax, readOnly: true},
		struct {
			UserID int64 `json:"user_id"`
			Offset int   `json:"offset"`
			Limit  int   `json:"limit"`
		}{userID, offset, limit})
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount int           `json:"total_count"`
		Photos     [][]photoSize `json:"photos"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TotalCount < 0 {
		return nil, invalidResponse(op)
	}
	refs := c.fileRefs(c.target)
	out := make([]profilePhoto, 0, min(len(result.Photos), maxProfileItems))
	for _, sizes := range result.Photos {
		if len(out) == maxProfileItems {
			break
		}
		if len(sizes) == 0 {
			continue
		}
		largest := sizes[len(sizes)-1]
		ref := refs.ref(largest.fileID)
		if ref == "" {
			continue
		}
		out = append(out, profilePhoto{ref, largest.Width, largest.Height, largest.FileSize, refs.photoSizes(sizes)})
	}
	return struct {
		TotalCount int            `json:"total_count"`
		Photos     []profilePhoto `json:"photos"`
	}{result.TotalCount, out}, nil
}

func (c *Client) ProfileAudios(ctx context.Context, userID int64, offset, limit int) (any, error) {
	const op = "read profile audios"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserProfileAudios", limit: profileResponseMax, readOnly: true},
		struct {
			UserID int64 `json:"user_id"`
			Offset int   `json:"offset"`
			Limit  int   `json:"limit"`
		}{userID, offset, limit})
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount int     `json:"total_count"`
		Audios     []audio `json:"audios"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TotalCount < 0 {
		return nil, invalidResponse(op)
	}
	refs := c.fileRefs(c.target)
	out := make([]profileAudio, 0, min(len(result.Audios), maxProfileItems))
	for _, a := range result.Audios {
		if len(out) == maxProfileItems {
			break
		}
		ref := refs.ref(a.fileID)
		if ref == "" {
			continue
		}
		out = append(out, profileAudio{ref, cleanText(a.FileUniqueID, maxNameRunes), a.Duration,
			cleanText(a.Title, maxNameRunes), cleanText(a.Performer, maxNameRunes), cleanText(a.FileName, maxNameRunes),
			cleanText(a.MimeType, maxNameRunes), a.FileSize, refs.thumbnail(a.Thumbnail)})
	}
	return struct {
		TotalCount int            `json:"total_count"`
		Audios     []profileAudio `json:"audios"`
	}{result.TotalCount, out}, nil
}

// boostSource is the Bot API ChatBoostSource of any of its three variants.
type boostSource struct {
	Source            string  `json:"source,omitempty"`
	User              *tgUser `json:"user,omitempty"`
	GiveawayMessageID int64   `json:"giveaway_message_id,omitempty"`
	PrizeStarCount    int64   `json:"prize_star_count,omitempty"`
	IsUnclaimed       bool    `json:"is_unclaimed,omitempty"`
}

func (c *Client) ChatBoosts(ctx context.Context, userID int64, _, _ int) (any, error) {
	const op = "read chat boosts"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserChatBoosts", limit: profileResponseMax, readOnly: true},
		struct {
			ChatID string `json:"chat_id"`
			UserID int64  `json:"user_id"`
		}{c.target, userID})
	if err != nil {
		return nil, err
	}
	var result struct {
		Boosts []struct {
			BoostID        string      `json:"boost_id"`
			AddDate        int64       `json:"add_date"`
			ExpirationDate int64       `json:"expiration_date"`
			Source         boostSource `json:"source"`
		} `json:"boosts"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, invalidResponse(op)
	}
	type boost struct {
		BoostID        string       `json:"boost_id"`
		AddDate        int64        `json:"add_date"`
		ExpirationDate int64        `json:"expiration_date"`
		Source         string       `json:"source"`
		SourceInfo     *boostSource `json:"source_info,omitempty"`
	}
	out := make([]boost, 0, min(len(result.Boosts), maxProfileItems))
	for _, b := range result.Boosts[:min(len(result.Boosts), maxProfileItems)] {
		info := b.Source
		info.Source = cleanText(info.Source, maxNameRunes)
		info.User = sender(info.User)
		item := boost{capText(b.BoostID, maxNameRunes), b.AddDate, b.ExpirationDate, capText(b.Source.Source, maxNameRunes), nil}
		if info != (boostSource{}) {
			item.SourceInfo = &info
		}
		out = append(out, item)
	}
	return struct {
		Boosts    []boost `json:"boosts"`
		Truncated bool    `json:"truncated,omitempty"`
	}{out, len(result.Boosts) > maxProfileItems}, nil
}

func (c *Client) PersonalChatMessages(ctx context.Context, userID int64, _, limit int) (any, error) {
	const op = "read personal chat messages"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserPersonalChatMessages", limit: defaultResponseBytes, readOnly: true},
		struct {
			UserID int64 `json:"user_id"`
			Limit  int   `json:"limit"`
		}{userID, limit})
	if err != nil {
		return nil, err
	}
	var messages []tgMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil, invalidResponse(op)
	}
	type message struct {
		MessageID int64  `json:"message_id"`
		Date      int64  `json:"date"`
		Text      string `json:"text,omitempty"`
		Caption   string `json:"caption,omitempty"`
		MediaKind string `json:"media_kind,omitempty"`
		FileRef   string `json:"file_ref,omitempty"`

		MediaItems    []any    `json:"media_items,omitempty"`
		OmittedFields []string `json:"omitted_fields,omitempty"`

		MessageThreadID int64      `json:"message_thread_id,omitempty"`
		From            *senderOut `json:"from,omitempty"`
		messageHeadOut
	}
	out := make([]message, 0, min(len(messages), maxUserItems))
	for _, m := range messages[:min(len(messages), maxUserItems)] {
		kind, file := m.media()
		ref := ""
		if file != nil {
			ref = signRef(c.token, refFile, c.target, file.FileID)
		}
		scope := messageScope{binding: c.target, chatID: m.Chat.ID, refs: c.fileRefs(c.target)}
		if m.BusinessConnectionID != "" {
			// A business message names its connection, not a bound chat: no file reference, no guest reference.
			scope = messageScope{chatID: m.Chat.ID, business: true}
		}
		items, omitted := mediaItems(&m, scope.refs)
		head, headOmitted := c.messageHead(&m, scope, false)
		out = append(out, message{MessageID: m.MessageID, Date: m.Date, Text: capText(m.Text, maxMessageLength),
			Caption: capText(m.Caption, maxCaptionRunes), MediaKind: kind, FileRef: ref, MediaItems: items,
			OmittedFields: append(omitted, headOmitted...), MessageThreadID: m.MessageThreadID,
			From: sender(m.From), messageHeadOut: head})
	}
	return struct {
		Messages []message `json:"messages"`
	}{out}, nil
}
