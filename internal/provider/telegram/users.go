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
const maxUserItems = 20

const userInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`},"required":["user_id"],"additionalProperties":false}`
const userPageInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`,"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":20}},` +
	`"required":["user_id"],"additionalProperties":false}`
const userMessagesInput = `{"type":"object","properties":{` + chatSchema + `,` + userIDSchema +
	`,"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["user_id"],"additionalProperties":false}`

var usersProfilePhotos = capability.Descriptor{
	ID: Provider + ".users.profile-photos", Version: 1, Title: "Read a chat member's profile photos",
	Description: "Read up to 20 profile photos of a user verified as a member of a bound chat",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userPageInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"total_count":{"type":"integer"},"photos":{"type":"array","items":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"width":{"type":"integer"},"height":{"type":"integer"},` +
		`"file_size":{"type":"integer"}}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "offset", Description: "First profile photo, starting at zero"},
		{Name: "limit", Description: "Maximum photos, 1 through 20; defaults to 20"}},
	Fields: []capability.Field{{Name: "total_count", Description: "Total profile photos reported by Telegram"},
		{Name: "photos", Description: "Largest size of each photo as a chat-bound signed file reference"}},
}

var usersProfileAudios = capability.Descriptor{
	ID: Provider + ".users.profile-audios", Version: 1, Title: "Read a chat member's profile audios",
	Description: "Read up to 20 profile audios of a user verified as a member of a bound chat",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userPageInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"total_count":{"type":"integer"},"audios":{"type":"array","items":{"type":"object","properties":{` +
		`"file_ref":{"type":"string"},"duration":{"type":"integer"},"title":{"type":"string"},` +
		`"performer":{"type":"string"},"file_size":{"type":"integer"}}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "offset", Description: "First profile audio, starting at zero"},
		{Name: "limit", Description: "Maximum audios, 1 through 20; defaults to 20"}},
	Fields: []capability.Field{{Name: "total_count", Description: "Total profile audios reported by Telegram"},
		{Name: "audios", Description: "Profile audios as chat-bound signed file references"}},
}

var usersChatBoosts = capability.Descriptor{
	ID: Provider + ".users.chat-boosts", Version: 1, Title: "Read a chat member's boosts",
	Description: "Read boosts a verified member added to the bound chat; Telegram requires bot administrator rights",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"boosts":{"type":"array","items":{"type":"object","properties":{` +
		`"boost_id":{"type":"string"},"add_date":{"type":"integer"},` +
		`"expiration_date":{"type":"integer"},"source":{"type":"string"}}}},` +
		`"truncated":{"type":"boolean"}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument},
	Fields: []capability.Field{{Name: "boosts", Description: "Up to 20 boosts of the selected chat"},
		{Name: "truncated", Description: "True if Telegram returned more than 20 boosts"}},
}

var usersPersonalChatMessages = capability.Descriptor{
	ID: Provider + ".users.personal-chat-messages", Version: 1, Title: "Read a chat member's personal channel messages",
	Description: "Read up to 20 recent messages of the personal chat of a verified member",
	Tags:        []string{"telegram", "users", "read"}, Risk: readRisk(memberDataSensitivity), Provider: Provider, Group: groupUsers,
	InputSchema: json.RawMessage(userMessagesInput), OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"messages":{"type":"array","items":{"type":"object","properties":{` +
		`"message_id":{"type":"integer"},"date":{"type":"integer"},"text":{"type":"string"},` +
		`"caption":{"type":"string"},"media_kind":{"type":"string"},` +
		`"file_ref":{"type":"string"},` +
		`"media_items":{"type":"array","items":{"type":"object"},"description":"Media objects of the message: the Bot API types Animation, Audio, Document, LivePhoto, PhotoSize (one entry per size), Sticker, Video, VideoNote and Voice, each with kind and its members; every file_id is a file_ref"},` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}}}}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{chatArgument, userIDArgument,
		{Name: "limit", Description: "Maximum recent messages, 1 through 20; defaults to 20"}},
	Fields: []capability.Field{{Name: "messages", Description: "Recent message text, captions and media objects from the user's personal chat; media_items lists the media objects with their Bot API members and chat-bound signed file references, and omitted_fields names, as media_items.<kind> or media_items.<kind>.<member>, a nested object left out for exceeding 8 levels, 100 list elements or 16 KiB"}},
}

type userArgs struct {
	Chat   string `json:"chat"`
	UserID int64  `json:"user_id"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func invokeUsersProfilePhotos(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read profile photos", true, (*Client).ProfilePhotos)
}
func invokeUsersProfileAudios(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read profile audios", true, (*Client).ProfileAudios)
}
func invokeUsersChatBoosts(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read chat boosts", false, (*Client).ChatBoosts)
}
func invokeUsersPersonalChatMessages(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUserRead(ctx, r, s, red, raw, "read personal chat messages", false, (*Client).PersonalChatMessages)
}

func invokeUserRead(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, raw json.RawMessage,
	op string, page bool, read func(*Client, context.Context, int64, int, int) (any, error)) (any, error) {
	var a userArgs
	if decodeStrict(raw, &a) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if a.UserID <= 0 || a.Offset < 0 || a.Limit < 0 || a.Limit > maxUserItems || (!page && a.Offset != 0) {
		return nil, providerError(op, "invalid user or page")
	}
	if a.Limit == 0 {
		a.Limit = maxUserItems
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
	FileRef  string `json:"file_ref"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}
type profileAudio struct {
	FileRef   string `json:"file_ref"`
	Duration  int    `json:"duration,omitempty"`
	Title     string `json:"title,omitempty"`
	Performer string `json:"performer,omitempty"`
	FileSize  int64  `json:"file_size,omitempty"`
}
type profilePhotoFile struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

func (c *Client) ProfilePhotos(ctx context.Context, userID int64, offset, limit int) (any, error) {
	const op = "read profile photos"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserProfilePhotos", limit: defaultResponseBytes, readOnly: true},
		struct {
			UserID int64 `json:"user_id"`
			Offset int   `json:"offset"`
			Limit  int   `json:"limit"`
		}{userID, offset, limit})
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount int                  `json:"total_count"`
		Photos     [][]profilePhotoFile `json:"photos"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TotalCount < 0 {
		return nil, invalidResponse(op)
	}
	out := make([]profilePhoto, 0, min(len(result.Photos), maxUserItems))
	for _, sizes := range result.Photos {
		if len(out) == maxUserItems {
			break
		}
		if len(sizes) == 0 {
			continue
		}
		file := sizes[len(sizes)-1]
		ref := signRef(c.token, refFile, c.target, file.FileID)
		if ref == "" {
			continue
		}
		out = append(out, profilePhoto{ref, file.Width, file.Height, file.FileSize})
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
	raw, err := c.call(ctx, spec{op: op, method: "getUserProfileAudios", limit: defaultResponseBytes, readOnly: true},
		struct {
			UserID int64 `json:"user_id"`
			Offset int   `json:"offset"`
			Limit  int   `json:"limit"`
		}{userID, offset, limit})
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount int `json:"total_count"`
		Audios     []struct {
			FileID    string `json:"file_id"`
			FileSize  int64  `json:"file_size"`
			Duration  int    `json:"duration"`
			Title     string `json:"title"`
			Performer string `json:"performer"`
		} `json:"audios"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TotalCount < 0 {
		return nil, invalidResponse(op)
	}
	out := make([]profileAudio, 0, min(len(result.Audios), maxUserItems))
	for _, audio := range result.Audios {
		if len(out) == maxUserItems {
			break
		}
		ref := signRef(c.token, refFile, c.target, audio.FileID)
		if ref == "" {
			continue
		}
		out = append(out, profileAudio{ref, audio.Duration, capText(audio.Title, maxNameRunes),
			capText(audio.Performer, maxNameRunes), audio.FileSize})
	}
	return struct {
		TotalCount int            `json:"total_count"`
		Audios     []profileAudio `json:"audios"`
	}{result.TotalCount, out}, nil
}

func (c *Client) ChatBoosts(ctx context.Context, userID int64, _, _ int) (any, error) {
	const op = "read chat boosts"
	if err := c.requireUserMember(ctx, userID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUserChatBoosts", limit: defaultResponseBytes, readOnly: true},
		struct {
			ChatID string `json:"chat_id"`
			UserID int64  `json:"user_id"`
		}{c.target, userID})
	if err != nil {
		return nil, err
	}
	var result struct {
		Boosts []struct {
			BoostID        string `json:"boost_id"`
			AddDate        int64  `json:"add_date"`
			ExpirationDate int64  `json:"expiration_date"`
			Source         struct {
				Source string `json:"source"`
			} `json:"source"`
		} `json:"boosts"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, invalidResponse(op)
	}
	type boost struct {
		BoostID        string `json:"boost_id"`
		AddDate        int64  `json:"add_date"`
		ExpirationDate int64  `json:"expiration_date"`
		Source         string `json:"source"`
	}
	out := make([]boost, 0, min(len(result.Boosts), maxUserItems))
	for _, b := range result.Boosts[:min(len(result.Boosts), maxUserItems)] {
		out = append(out, boost{capText(b.BoostID, maxNameRunes), b.AddDate, b.ExpirationDate,
			capText(b.Source.Source, maxNameRunes)})
	}
	return struct {
		Boosts    []boost `json:"boosts"`
		Truncated bool    `json:"truncated,omitempty"`
	}{out, len(result.Boosts) > maxUserItems}, nil
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
	}
	out := make([]message, 0, min(len(messages), maxUserItems))
	for _, m := range messages[:min(len(messages), maxUserItems)] {
		kind, file := m.media()
		ref := ""
		if file != nil {
			ref = signRef(c.token, refFile, c.target, file.FileID)
		}
		refs := c.fileRefs(c.target)
		if m.BusinessConnectionID != "" {
			refs = nil
		}
		items, omitted := mediaItems(&m, refs)
		out = append(out, message{m.MessageID, m.Date, capText(m.Text, maxMessageLength),
			capText(m.Caption, maxCaptionRunes), kind, ref, items, omitted})
	}
	return struct {
		Messages []message `json:"messages"`
	}{out}, nil
}
