package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupUpdates = "updates"

	defaultUpdateLimit = 20
	maxUpdateLimit     = 100
	maxWaitSeconds     = 10
	// updatesResponseBytes bounds a getUpdates response; an update with media metadata is far smaller.
	updatesResponseBytes = 2 << 20

	maxCaptionRunes  = 1024
	maxNameRunes     = 256
	maxCallbackRunes = 256
)

var updatesList = capability.Descriptor{
	ID:          Provider + ".updates.list",
	Version:     1,
	Title:       "List Telegram updates",
	Description: "Read pending incoming messages and events of the bound chats without consuming them",
	Tags:        []string{"telegram", "updates", "read"},
	Risk: capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupUpdates,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"wait_seconds":{"type":"integer","minimum":0,"maximum":10}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"updates":{"type":"array","items":{"type":"object","properties":{` +
		`"update_id":{"type":"integer"},"type":{"type":"string"},"chat":{"type":"string"},` +
		`"message_id":{"type":"integer"},"date":{"type":"integer"},"message_thread_id":{"type":"integer"},` +
		`"reply_to_message_id":{"type":"integer"},` +
		`"from":{"type":"object","properties":{"id":{"type":"integer"},"is_bot":{"type":"boolean"},` +
		`"first_name":{"type":"string"},"username":{"type":"string"}}},` +
		`"text":{"type":"string"},"caption":{"type":"string"},` +
		`"media":{"type":"object","properties":{"kind":{"type":"string"},"size":{"type":"integer"},` +
		`"mime_type":{"type":"string"},"file_name":{"type":"string"},"file_ref":{"type":"string"}}},` +
		`"callback_data":{"type":"string"},"callback_ref":{"type":"string"},"join_request_ref":{"type":"string"}},` +
		`"required":["update_id","type","chat"]}},` +
		`"skipped":{"type":"integer"},"last_update_id":{"type":"integer"}},` +
		`"required":["updates","skipped"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Maximum number of pending updates to fetch, from 1 through 100; default 20"},
		{Name: "wait_seconds", Description: "Seconds to wait for a new update when none is pending, from 0 through 10; default 0"},
	},
	Fields: []capability.Field{
		{Name: "updates", Description: "Updates of the bound chats, oldest first"},
		{Name: "skipped", Description: "Number of fetched updates left out because they belong to no bound chat or have an unsupported type"},
		{Name: "last_update_id", Description: "Highest update identifier seen, including skipped updates"},
	},
	Examples: []capability.Example{{
		Description: "Read up to 20 pending updates of the bound chats",
		Arguments:   json.RawMessage(`{}`),
	}},
}

func invokeUpdatesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Limit       *int `json:"limit"`
		WaitSeconds *int `json:"wait_seconds"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("list updates", "the validated arguments could not be read")
	}
	limit, wait := defaultUpdateLimit, 0
	if arguments.Limit != nil {
		limit = *arguments.Limit
	}
	if arguments.WaitSeconds != nil {
		wait = *arguments.WaitSeconds
	}
	if limit < 1 || limit > maxUpdateLimit || wait < 0 || wait > maxWaitSeconds {
		return nil, providerError("list updates", "limit or wait_seconds is outside the supported range")
	}
	if resolved == nil {
		return nil, providerError("list updates", "no connection was selected")
	}
	// The tool reads every bound chat; it needs at least one before any credential is resolved.
	set, err := targetsOf(resolved)
	if err != nil {
		return nil, providerError("list updates", "the configured Telegram targets are unusable")
	}
	if len(set.chats) == 0 {
		return nil, providerError("list updates", "this connection binds no chat target")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListUpdates(ctx, limit, wait)
}

type senderOut struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type mediaOut struct {
	Kind     string `json:"kind"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileRef  string `json:"file_ref,omitempty"`
}

type updateOut struct {
	UpdateID         int64      `json:"update_id"`
	Type             string     `json:"type"`
	Chat             string     `json:"chat"`
	MessageID        int64      `json:"message_id,omitempty"`
	Date             int64      `json:"date,omitempty"`
	MessageThreadID  int64      `json:"message_thread_id,omitempty"`
	ReplyToMessageID int64      `json:"reply_to_message_id,omitempty"`
	From             *senderOut `json:"from,omitempty"`
	Text             string     `json:"text,omitempty"`
	Caption          string     `json:"caption,omitempty"`
	Media            *mediaOut  `json:"media,omitempty"`
	CallbackData     string     `json:"callback_data,omitempty"`
	CallbackRef      string     `json:"callback_ref,omitempty"`
	JoinRequestRef   string     `json:"join_request_ref,omitempty"`
}

// Only the fields below are decoded, so nothing else of an update can reach the output.

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
}

type tgMessage struct {
	MessageID       int64   `json:"message_id"`
	MessageThreadID int64   `json:"message_thread_id"`
	Date            int64   `json:"date"`
	Chat            tgChat  `json:"chat"`
	From            *tgUser `json:"from"`
	Text            string  `json:"text"`
	Caption         string  `json:"caption"`
	ReplyTo         *struct {
		MessageID int64 `json:"message_id"`
	} `json:"reply_to_message"`
	Photo     []tgFile `json:"photo"`
	Document  *tgFile  `json:"document"`
	Audio     *tgFile  `json:"audio"`
	Video     *tgFile  `json:"video"`
	Voice     *tgFile  `json:"voice"`
	VideoNote *tgFile  `json:"video_note"`
	Animation *tgFile  `json:"animation"`
	Sticker   *tgFile  `json:"sticker"`
}

// tgEvent covers the chat events that carry a chat, an actor, and a date.
type tgEvent struct {
	Chat      tgChat  `json:"chat"`
	MessageID int64   `json:"message_id"`
	Date      int64   `json:"date"`
	From      *tgUser `json:"from"`
	User      *tgUser `json:"user"`
}

type tgCallback struct {
	ID      string  `json:"id"`
	From    *tgUser `json:"from"`
	Data    string  `json:"data"`
	Message *struct {
		MessageID int64  `json:"message_id"`
		Date      int64  `json:"date"`
		Chat      tgChat `json:"chat"`
	} `json:"message"`
}

type tgUpdate struct {
	UpdateID             int64       `json:"update_id"`
	Message              *tgMessage  `json:"message"`
	EditedMessage        *tgMessage  `json:"edited_message"`
	ChannelPost          *tgMessage  `json:"channel_post"`
	EditedChannelPost    *tgMessage  `json:"edited_channel_post"`
	MessageReaction      *tgEvent    `json:"message_reaction"`
	MessageReactionCount *tgEvent    `json:"message_reaction_count"`
	CallbackQuery        *tgCallback `json:"callback_query"`
	MyChatMember         *tgEvent    `json:"my_chat_member"`
	ChatMember           *tgEvent    `json:"chat_member"`
	ChatJoinRequest      *tgEvent    `json:"chat_join_request"`
	ChatBoost            *tgEvent    `json:"chat_boost"`
	RemovedChatBoost     *tgEvent    `json:"removed_chat_boost"`
}

// candidate is a supported update before it is matched to a bound chat and its references are signed.
type candidate struct {
	out        updateOut
	chat       int64
	fileID     string
	callbackID string
	joinUser   int64
}

func capText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func sender(u *tgUser) *senderOut {
	if u == nil || u.ID == 0 {
		return nil
	}
	return &senderOut{ID: u.ID, IsBot: u.IsBot, FirstName: capText(u.FirstName, maxNameRunes),
		Username: capText(u.Username, maxNameRunes)}
}

func (m *tgMessage) media() (string, *tgFile) {
	switch {
	case len(m.Photo) > 0:
		// The last PhotoSize is the largest.
		return "photo", &m.Photo[len(m.Photo)-1]
	case m.Document != nil:
		return "document", m.Document
	case m.Audio != nil:
		return "audio", m.Audio
	case m.Video != nil:
		return "video", m.Video
	case m.Voice != nil:
		return "voice", m.Voice
	case m.VideoNote != nil:
		return "video_note", m.VideoNote
	case m.Animation != nil:
		return "animation", m.Animation
	case m.Sticker != nil:
		return "sticker", m.Sticker
	}
	return "", nil
}

// classify extracts the allow-listed fields of a supported update; ok is false for any other type.
func classify(u tgUpdate) (c candidate, ok bool) {
	c.out.UpdateID = u.UpdateID
	message := func(kind string, m *tgMessage) (candidate, bool) {
		c.out.Type, c.chat = kind, m.Chat.ID
		c.out.MessageID, c.out.Date, c.out.MessageThreadID = m.MessageID, m.Date, m.MessageThreadID
		if m.ReplyTo != nil {
			c.out.ReplyToMessageID = m.ReplyTo.MessageID
		}
		c.out.From = sender(m.From)
		c.out.Text = capText(m.Text, maxMessageLength)
		c.out.Caption = capText(m.Caption, maxCaptionRunes)
		if kind, file := m.media(); file != nil {
			c.out.Media = &mediaOut{Kind: kind, Size: file.FileSize, MimeType: capText(file.MimeType, maxNameRunes),
				FileName: capText(file.FileName, maxNameRunes)}
			c.fileID = file.FileID
		}
		return c, true
	}
	event := func(kind string, e *tgEvent) (candidate, bool) {
		c.out.Type, c.chat = kind, e.Chat.ID
		c.out.MessageID, c.out.Date = e.MessageID, e.Date
		c.out.From = sender(e.From)
		if c.out.From == nil {
			c.out.From = sender(e.User)
		}
		return c, true
	}
	switch {
	case u.Message != nil:
		return message("message", u.Message)
	case u.EditedMessage != nil:
		return message("edited_message", u.EditedMessage)
	case u.ChannelPost != nil:
		return message("channel_post", u.ChannelPost)
	case u.EditedChannelPost != nil:
		return message("edited_channel_post", u.EditedChannelPost)
	case u.MessageReaction != nil:
		return event("message_reaction", u.MessageReaction)
	case u.MessageReactionCount != nil:
		return event("message_reaction_count", u.MessageReactionCount)
	case u.MyChatMember != nil:
		return event("my_chat_member", u.MyChatMember)
	case u.ChatMember != nil:
		return event("chat_member", u.ChatMember)
	case u.ChatBoost != nil:
		return event("chat_boost", u.ChatBoost)
	case u.RemovedChatBoost != nil:
		return event("removed_chat_boost", u.RemovedChatBoost)
	case u.ChatJoinRequest != nil:
		c, _ = event("chat_join_request", u.ChatJoinRequest)
		if c.out.From != nil {
			c.joinUser = c.out.From.ID
		}
		return c, true
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		if q.Message == nil {
			// An inline-message callback names no chat.
			return candidate{}, false
		}
		c.out.Type, c.chat = "callback_query", q.Message.Chat.ID
		c.out.MessageID, c.out.Date = q.Message.MessageID, q.Message.Date
		c.out.From = sender(q.From)
		c.out.CallbackData = capText(q.Data, maxCallbackRunes)
		c.callbackID = q.ID
		return c, true
	}
	return candidate{}, false
}

// ListUpdates fetches pending updates with one getUpdates request and returns only those of bound chats. It
// sends neither offset nor allowed_updates, so Telegram keeps every update; nothing is acknowledged.
func (c *Client) ListUpdates(ctx context.Context, limit, wait int) (map[string]any, error) {
	const op = "list updates"
	if len(c.set.chats) == 0 {
		return nil, providerError(op, "this connection binds no chat target")
	}
	if limit < 1 || limit > maxUpdateLimit || wait < 0 || wait > maxWaitSeconds {
		return nil, providerError(op, "limit or wait_seconds is outside the supported range")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUpdates", limit: updatesResponseBytes, readOnly: true},
		struct {
			Limit   int `json:"limit"`
			Timeout int `json:"timeout"`
		}{Limit: limit, Timeout: wait})
	if err != nil {
		return nil, err
	}
	var received []tgUpdate
	if json.Unmarshal(raw, &received) != nil {
		return nil, invalidResponse(op)
	}
	bound := map[int64]string{}
	var usernames []string
	for _, chat := range c.set.chats {
		if id, err := strconv.ParseInt(chat, 10, 64); err == nil {
			if _, dup := bound[id]; !dup {
				bound[id] = chat
			}
		} else {
			usernames = append(usernames, chat)
		}
	}
	candidates := make([]candidate, 0, len(received))
	skipped, last, unresolved := 0, int64(0), false
	for _, u := range received {
		if u.UpdateID <= 0 {
			return nil, invalidResponse(op)
		}
		last = max(last, u.UpdateID)
		cand, ok := classify(u)
		if !ok || cand.chat == 0 {
			skipped++
			continue
		}
		if _, known := bound[cand.chat]; !known {
			unresolved = true
		}
		candidates = append(candidates, cand)
	}
	// An @username only matters for matching; it is resolved by the fixed getChat call, and only when an
	// update could belong to it.
	if unresolved && len(usernames) > 0 {
		for _, name := range usernames {
			id, err := c.resolveChat(ctx, name)
			if err != nil {
				return nil, err
			}
			if _, dup := bound[id]; !dup {
				bound[id] = name
			}
		}
	}
	updates := make([]updateOut, 0, len(candidates))
	for _, cand := range candidates {
		binding, known := bound[cand.chat]
		if !known {
			skipped++
			continue
		}
		cand.out.Chat = binding
		if cand.fileID != "" && cand.out.Media != nil {
			cand.out.Media.FileRef = signRef(c.token, refFile, binding, cand.fileID)
		}
		if cand.callbackID != "" {
			cand.out.CallbackRef = signRef(c.token, refCallback, binding, cand.callbackID)
		}
		if cand.joinUser != 0 {
			cand.out.JoinRequestRef = signRef(c.token, refJoinRequest, binding, strconv.FormatInt(cand.joinUser, 10))
		}
		updates = append(updates, cand.out)
	}
	result := map[string]any{"updates": updates, "skipped": skipped}
	if last > 0 {
		result["last_update_id"] = last
	}
	return result, nil
}

// resolveChat turns a configured @username target into its numeric ID with getChat. The ID is used only to
// match updates; it never becomes the target of a request.
func (c *Client) resolveChat(ctx context.Context, username string) (int64, error) {
	const op = "list updates"
	raw, err := c.call(ctx, spec{op: op, method: "getChat", limit: defaultResponseBytes, readOnly: true},
		struct {
			ChatID string `json:"chat_id"`
		}{ChatID: username})
	if err != nil {
		return 0, err
	}
	var chat tgChat
	if json.Unmarshal(raw, &chat) != nil || chat.ID == 0 {
		return 0, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Telegram returned an invalid response"}
	}
	return chat.ID, nil
}
