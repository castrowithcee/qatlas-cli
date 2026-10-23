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
	// updatesResponseBytes bounds a getUpdates response; a page of 100 messages that all carry content members at
	// realistic maximum size needs just under 4 MiB.
	updatesResponseBytes = 4 << 20

	// maxConfirmUpdateID keeps update_id+1 exactly representable in JSON numbers of any client.
	maxConfirmUpdateID = 1<<53 - 1

	maxCaptionRunes  = 1024
	maxNameRunes     = 256
	maxCallbackRunes = 256
)

var updatesList = capability.Descriptor{
	ID:          Provider + ".updates.list",
	Version:     1,
	Title:       "List Telegram updates",
	Description: "Read pending incoming messages and events of the bound chats and business connections, and chatless updates such as inline, shipping, and payment queries, polls, and subscriptions with the bot target, without consuming them",
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
		`"from":` + userSchema + `,` +
		`"text":{"type":"string"},"caption":{"type":"string"},` +
		`"media":{"type":"object","properties":{"kind":{"type":"string"},"size":{"type":"integer"},` +
		`"mime_type":{"type":"string"},"file_name":{"type":"string"},"file_ref":{"type":"string"}}},` +
		`"media_items":{"type":"array","items":{"type":"object"},"description":"Media objects of the message: the Bot API types Animation, Audio, Document, LivePhoto, PhotoSize (one entry per size), Sticker, Video, VideoNote and Voice, each with kind and its members; every file_id is a file_ref"},` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}},` +
		`"callback_data":{"type":"string"},"callback_ref":{"type":"string"},"join_request_ref":{"type":"string"},` +
		`"query":{"type":"string"},"result_id":{"type":"string"},"inline_ref":{"type":"string"},` +
		`"business_chat_ref":{"type":"string"},"message_ids":{"type":"array","items":{"type":"integer"}},` +
		`"is_enabled":{"type":"boolean"}` + messageHeadSchema + updateEventSchema + `},` +
		`"required":["update_id","type","chat"]}},` +
		`"skipped":{"type":"integer"},"last_update_id":{"type":"integer"}},` +
		`"required":["updates","skipped"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Maximum number of pending updates to fetch, from 1 through 100; default 20; a Telegram answer over " + strconv.Itoa(updatesResponseBytes>>20) + " MiB fails as an invalid response, and a smaller limit avoids it"},
		{Name: "wait_seconds", Description: "Seconds to wait for a new update when none is pending, from 0 through 10; default 0"},
	},
	Fields: append([]capability.Field{
		{Name: "updates", Description: "Updates of the bound chats and business connections and, with the bot target, chatless updates (inline queries and chosen results, callbacks of inline messages, shipping and pre-checkout queries, purchased paid media, polls, poll answers, managed bots, subscriptions), oldest first; the event members are listed as fields of their own, and a location or poll is the message member of that name; business updates name their chat only as business_chat_ref; from is " + userFieldsText + "; media_items lists the media objects of a message with their Bot API members and signed file references, and omitted_fields names, as media_items.<kind> or media_items.<kind>.<member> or as a header or content member, a nested object left out for exceeding 8 levels, 100 list elements or 16 KiB; messages also carry " + messageHeadText},
		{Name: "skipped", Description: "Number of fetched updates left out because they belong to no bound chat, business connection, or bot target, or have an unsupported type"},
		{Name: "last_update_id", Description: "Highest update identifier seen, including skipped updates"},
	}, append(updateBaseFields, updateEventFields...)...),
	Examples: []capability.Example{{
		Description: "Read up to 20 pending updates of the bound chats",
		Arguments:   json.RawMessage(`{}`),
	}},
}

var updatesConfirm = capability.Descriptor{
	ID:          Provider + ".updates.confirm",
	Version:     1,
	Title:       "Confirm Telegram updates",
	Description: "Confirm all updates of the bot up to and including update_id, so telegram.updates.list no longer returns them",
	Tags:        []string{"telegram", "updates", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupUpdates,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"update_id":{"type":"integer","minimum":1,"maximum":9007199254740991}},` +
		`"required":["update_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"confirmed_through":{"type":"integer"}},` +
		`"required":["confirmed_through"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "update_id", Description: "Highest update identifier already read; this and every lower update of the whole bot is confirmed", Required: true},
	},
	Fields: []capability.Field{{Name: "confirmed_through", Description: "The update identifier up to which Telegram accepted the confirmation"}},
	Examples: []capability.Example{{
		Description: "Confirm the updates read so far",
		Arguments:   json.RawMessage(`{"update_id":10}`),
	}},
}

func invokeUpdatesConfirm(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "confirm updates"
	var arguments struct {
		UpdateID int64 `json:"update_id"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.UpdateID < 1 || arguments.UpdateID > maxConfirmUpdateID {
		return nil, providerError(op, "update_id is outside the supported range")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ConfirmUpdates(ctx, arguments.UpdateID)
}

// ConfirmUpdates confirms every update up to updateID with one getUpdates request whose offset is updateID+1.
// The update that request returns is read only far enough to see an array and is dropped unseen: it is not
// handled, so it must stay available to the next list.
func (c *Client) ConfirmUpdates(ctx context.Context, updateID int64) (map[string]any, error) {
	const op = "confirm updates"
	if updateID < 1 || updateID > maxConfirmUpdateID {
		return nil, providerError(op, "update_id is outside the supported range")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getUpdates", limit: updatesResponseBytes},
		struct {
			Offset  int64 `json:"offset"`
			Limit   int   `json:"limit"`
			Timeout int   `json:"timeout"`
		}{Offset: updateID + 1, Limit: 1, Timeout: 0})
	if err != nil {
		return nil, err
	}
	var discarded []json.RawMessage
	if json.Unmarshal(raw, &discarded) != nil {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"confirmed_through": updateID}, nil
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
	// The tool reads every bound chat and business connection; it needs one of them before any credential is
	// resolved.
	set, err := targetsOf(resolved)
	if err != nil {
		return nil, providerError("list updates", "the configured Telegram targets are unusable")
	}
	if len(set.chats) == 0 && len(set.topics) == 0 && len(set.businesses) == 0 {
		return nil, providerError("list updates", "this connection binds no chat or business target")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListUpdates(ctx, limit, wait)
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
	MediaItems       []any      `json:"media_items,omitempty"`
	OmittedFields    []string   `json:"omitted_fields,omitempty"`
	CallbackData     string     `json:"callback_data,omitempty"`
	CallbackRef      string     `json:"callback_ref,omitempty"`
	JoinRequestRef   string     `json:"join_request_ref,omitempty"`
	Query            string     `json:"query,omitempty"`
	ResultID         string     `json:"result_id,omitempty"`
	InlineRef        string     `json:"inline_ref,omitempty"`
	BusinessChatRef  string     `json:"business_chat_ref,omitempty"`
	MessageIDs       []int64    `json:"message_ids,omitempty"`
	IsEnabled        *bool      `json:"is_enabled,omitempty"`

	updateEventOut
	messageHeadOut
}

// Only the fields below are decoded, so nothing else of an update can reach the output.

// tgChat is the decoded Chat; chats named by a message are printed from it as data only.
type tgChat = chatRefOut

// tgFile is the summary behind the media member: the identifier and the few members every file kind shares.
type tgFile struct {
	FileID   string
	FileSize int64
	MimeType string
	FileName string
}

type tgMessage struct {
	MessageID int64 `json:"message_id"`
	// BusinessConnectionID is set on the messages of a business connection only.
	BusinessConnectionID string      `json:"business_connection_id"`
	MessageThreadID      int64       `json:"message_thread_id"`
	Date                 int64       `json:"date"`
	Chat                 tgChat      `json:"chat"`
	From                 *tgUser     `json:"from"`
	Text                 string      `json:"text"`
	Caption              string      `json:"caption"`
	ReplyTo              *tgMessage  `json:"reply_to_message"`
	Photo                []photoSize `json:"photo"`
	Document             *document   `json:"document"`
	Audio                *audio      `json:"audio"`
	Video                *video      `json:"video"`
	Voice                *voice      `json:"voice"`
	VideoNote            *videoNote  `json:"video_note"`
	Animation            *animation  `json:"animation"`
	Sticker              *sticker    `json:"sticker"`
	LivePhoto            *livePhoto  `json:"live_photo"`

	DirectMessagesTopic    *tgDirectTopic   `json:"direct_messages_topic"`
	SenderChat             *tgChat          `json:"sender_chat"`
	SenderBoostCount       int64            `json:"sender_boost_count"`
	SenderBusinessBot      *tgUser          `json:"sender_business_bot"`
	SenderTag              string           `json:"sender_tag"`
	ReceiverUser           *tgUser          `json:"receiver_user"`
	EphemeralMessageID     int64            `json:"ephemeral_message_id"`
	GuestQueryID           string           `json:"guest_query_id"`
	ForwardOrigin          *tgOrigin        `json:"forward_origin"`
	IsTopicMessage         bool             `json:"is_topic_message"`
	IsAutomaticForward     bool             `json:"is_automatic_forward"`
	ExternalReply          *tgExternalReply `json:"external_reply"`
	Quote                  *tgQuote         `json:"quote"`
	ReplyToStory           *tgStory         `json:"reply_to_story"`
	ReplyToChecklistTaskID int64            `json:"reply_to_checklist_task_id"`
	ReplyToPollOptionID    string           `json:"reply_to_poll_option_id"`
	ViaBot                 *tgUser          `json:"via_bot"`
	GuestBotCallerUser     *tgUser          `json:"guest_bot_caller_user"`
	GuestBotCallerChat     *tgChat          `json:"guest_bot_caller_chat"`
	EditDate               int64            `json:"edit_date"`
	HasProtectedContent    bool             `json:"has_protected_content"`
	IsFromOffline          bool             `json:"is_from_offline"`
	IsPaidPost             bool             `json:"is_paid_post"`
	MediaGroupID           string           `json:"media_group_id"`
	AuthorSignature        string           `json:"author_signature"`
	PaidStarCount          int64            `json:"paid_star_count"`

	Entities              []entityOut           `json:"entities"`
	LinkPreviewOptions    *linkPreviewOut       `json:"link_preview_options"`
	SuggestedPostInfo     *suggestedPostInfoOut `json:"suggested_post_info"`
	EffectID              string                `json:"effect_id"`
	RichMessage           json.RawMessage       `json:"rich_message"`
	PaidMedia             *paidMediaInfoOut     `json:"paid_media"`
	Story                 *tgStory              `json:"story"`
	CaptionEntities       []entityOut           `json:"caption_entities"`
	ShowCaptionAboveMedia bool                  `json:"show_caption_above_media"`
	HasMediaSpoiler       bool                  `json:"has_media_spoiler"`
	Checklist             *checklistOut         `json:"checklist"`
	Contact               *contactOut           `json:"contact"`
	Dice                  *diceOut              `json:"dice"`
	Game                  *gameOut              `json:"game"`
	Poll                  *pollOut              `json:"poll"`
	Venue                 *venueOut             `json:"venue"`
	Location              *messageLocationOut   `json:"location"`
	Invoice               *invoiceOut           `json:"invoice"`
	ReplyMarkup           *inlineKeyboardOut    `json:"reply_markup"`
	Giveaway              *giveawayOut          `json:"giveaway"`
	GiveawayWinners       *giveawayWinnersOut   `json:"giveaway_winners"`

	tgService
}

// tgEvent covers the chat events that carry a chat, an actor, and a date.
type tgEvent struct {
	Chat      tgChat  `json:"chat"`
	MessageID int64   `json:"message_id"`
	Date      int64   `json:"date"`
	From      *tgUser `json:"from"`
	User      *tgUser `json:"user"`
	// QueryID is set on chat_join_request only, and only for bots assigned to process join requests.
	QueryID string `json:"query_id"`
}

type tgCallback struct {
	ID              string     `json:"id"`
	From            *tgUser    `json:"from"`
	Data            string     `json:"data"`
	Message         *tgMessage `json:"message"`
	InlineMessageID string     `json:"inline_message_id"`
	ChatInstance    string     `json:"chat_instance"`
	GameShortName   string     `json:"game_short_name"`
}

// tgInline covers inline_query and chosen_inline_result. inline_message_id is read only to be signed.
type tgInline struct {
	ID              string              `json:"id"`
	From            *tgUser             `json:"from"`
	Query           string              `json:"query"`
	ResultID        string              `json:"result_id"`
	Offset          string              `json:"offset"`
	ChatType        string              `json:"chat_type"`
	Location        *messageLocationOut `json:"location"`
	InlineMessageID string              `json:"inline_message_id"`
}

type tgUpdate struct {
	UpdateID             int64                 `json:"update_id"`
	Message              *tgMessage            `json:"message"`
	EditedMessage        *tgMessage            `json:"edited_message"`
	ChannelPost          *tgMessage            `json:"channel_post"`
	EditedChannelPost    *tgMessage            `json:"edited_channel_post"`
	MessageReaction      *tgReactionEvent      `json:"message_reaction"`
	MessageReactionCount *tgReactionCountEvent `json:"message_reaction_count"`
	CallbackQuery        *tgCallback           `json:"callback_query"`
	InlineQuery          *tgInline             `json:"inline_query"`
	ChosenInlineResult   *tgInline             `json:"chosen_inline_result"`
	MyChatMember         *tgMemberEvent        `json:"my_chat_member"`
	ChatMember           *tgMemberEvent        `json:"chat_member"`
	ChatJoinRequest      *tgJoinEvent          `json:"chat_join_request"`
	ChatBoost            *tgBoostEvent         `json:"chat_boost"`
	RemovedChatBoost     *tgBoostRemovedEvent  `json:"removed_chat_boost"`

	GuestMessage             *tgMessage            `json:"guest_message"`
	StoppedMessageGeneration *tgStoppedGeneration  `json:"stopped_message_generation"`
	ShippingQuery            *tgShippingQuery      `json:"shipping_query"`
	PreCheckoutQuery         *tgPreCheckoutQuery   `json:"pre_checkout_query"`
	PurchasedPaidMedia       *tgPaidMediaPurchased `json:"purchased_paid_media"`
	Poll                     *pollOut              `json:"poll"`
	PollAnswer               *tgPollAnswer         `json:"poll_answer"`
	ManagedBot               *tgManagedBot         `json:"managed_bot"`
	Subscription             *tgSubscription       `json:"subscription"`

	BusinessConnection     *tgBusinessConnection `json:"business_connection"`
	BusinessMessage        *tgMessage            `json:"business_message"`
	EditedBusinessMessage  *tgMessage            `json:"edited_business_message"`
	DeletedBusinessMessage *struct {
		BusinessConnectionID string  `json:"business_connection_id"`
		Chat                 tgChat  `json:"chat"`
		MessageIDs           []int64 `json:"message_ids"`
	} `json:"deleted_business_messages"`
}

// candidate is a supported update before it is matched to a bound chat and its references are signed.
type candidate struct {
	out  updateOut
	chat int64
	// thread is the topic of a message or of the message under a callback; a topic target binds nothing else.
	thread int64
	fileID string
	// msg is the decoded message of a message update; its media objects are mapped once the binding is known.
	msg        *tgMessage
	callbackID string
	joinQuery  string
	// botOnly marks an update that belongs to no chat; only the bot target binds it.
	botOnly  bool
	inlineID string
	// The identifiers below have no chat; only the bot target signs them.
	inlineMessageID string
	shippingID      string
	preCheckoutID   string
	// poll is the decoded poll of a poll update, mapped once the bot target binds it.
	poll *pollOut
	// callbackMsg is the message a pressed button belongs to.
	callbackMsg *tgMessage
	// business is the business connection an update belongs to; only a business/<id> target binds it.
	business string
}

func capText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func (m *tgMessage) media() (string, *tgFile) {
	switch {
	case len(m.Photo) > 0:
		// The last PhotoSize is the largest.
		p := m.Photo[len(m.Photo)-1]
		return "photo", &tgFile{FileID: p.fileID, FileSize: p.FileSize}
	case m.Document != nil:
		d := m.Document
		return "document", &tgFile{d.fileID, d.FileSize, d.MimeType, d.FileName}
	case m.Audio != nil:
		a := m.Audio
		return "audio", &tgFile{a.fileID, a.FileSize, a.MimeType, a.FileName}
	case m.Video != nil:
		v := m.Video
		return "video", &tgFile{v.fileID, v.FileSize, v.MimeType, v.FileName}
	case m.Voice != nil:
		v := m.Voice
		return "voice", &tgFile{FileID: v.fileID, FileSize: v.FileSize, MimeType: v.MimeType}
	case m.VideoNote != nil:
		v := m.VideoNote
		return "video_note", &tgFile{FileID: v.fileID, FileSize: v.FileSize}
	case m.Animation != nil:
		a := m.Animation
		return "animation", &tgFile{a.fileID, a.FileSize, a.MimeType, a.FileName}
	case m.Sticker != nil:
		return "sticker", &tgFile{FileID: m.Sticker.fileID, FileSize: m.Sticker.FileSize}
	}
	return "", nil
}

// classify extracts the allow-listed fields of a supported update; ok is false for any other type.
func classify(u tgUpdate) (c candidate, ok bool) {
	c.out.UpdateID = u.UpdateID
	message := func(kind string, m *tgMessage) (candidate, bool) {
		c.out.Type, c.chat = kind, m.Chat.ID
		c.business = m.BusinessConnectionID
		c.out.MessageID, c.out.Date, c.out.MessageThreadID = m.MessageID, m.Date, m.MessageThreadID
		c.thread = m.MessageThreadID
		if m.ReplyTo != nil {
			c.out.ReplyToMessageID = m.ReplyTo.MessageID
		}
		c.out.From = sender(m.From)
		c.out.Text = capText(m.Text, maxMessageLength)
		c.out.Caption = capText(m.Caption, maxCaptionRunes)
		c.msg = m
		if kind, file := m.media(); file != nil {
			c.out.Media = &mediaOut{Kind: kind, Size: file.FileSize, MimeType: capText(file.MimeType, maxNameRunes),
				FileName: capText(file.FileName, maxNameRunes)}
			if c.business == "" {
				// The file tools address bound chats; a business file is listed but not downloadable.
				c.fileID = file.FileID
			}
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
	case u.GuestMessage != nil:
		return message("guest_message", u.GuestMessage)
	case u.MessageReaction != nil:
		c, _ = event("message_reaction", &u.MessageReaction.tgEvent)
		c.reactionEvent(u.MessageReaction)
		return c, true
	case u.MessageReactionCount != nil:
		c, _ = event("message_reaction_count", &u.MessageReactionCount.tgEvent)
		c.reactionCountEvent(u.MessageReactionCount)
		return c, true
	case u.MyChatMember != nil:
		c, _ = event("my_chat_member", &u.MyChatMember.tgEvent)
		c.memberEvent(u.MyChatMember)
		return c, true
	case u.ChatMember != nil:
		c, _ = event("chat_member", &u.ChatMember.tgEvent)
		c.memberEvent(u.ChatMember)
		return c, true
	case u.ChatBoost != nil:
		c, _ = event("chat_boost", &u.ChatBoost.tgEvent)
		c.boostEvent(u.ChatBoost)
		return c, true
	case u.RemovedChatBoost != nil:
		c, _ = event("removed_chat_boost", &u.RemovedChatBoost.tgEvent)
		c.boostRemovedEvent(u.RemovedChatBoost)
		return c, true
	case u.ChatJoinRequest != nil:
		c, _ = event("chat_join_request", &u.ChatJoinRequest.tgEvent)
		c.joinQuery = u.ChatJoinRequest.QueryID
		c.joinEvent(u.ChatJoinRequest)
		return c, true
	// A business message without a connection ID is unsupported and falls through to the skipped count.
	case u.BusinessMessage != nil && u.BusinessMessage.BusinessConnectionID != "":
		return message("business_message", u.BusinessMessage)
	case u.EditedBusinessMessage != nil && u.EditedBusinessMessage.BusinessConnectionID != "":
		return message("edited_business_message", u.EditedBusinessMessage)
	case u.DeletedBusinessMessage != nil:
		d := u.DeletedBusinessMessage
		if d.BusinessConnectionID == "" {
			return candidate{}, false
		}
		c.out.Type, c.chat, c.business = "deleted_business_messages", d.Chat.ID, d.BusinessConnectionID
		c.out.MessageIDs = d.MessageIDs[:min(len(d.MessageIDs), maxDeleteMessages)]
		return c, true
	case u.BusinessConnection != nil:
		b := u.BusinessConnection
		if b.ID == "" {
			return candidate{}, false
		}
		enabled := b.IsEnabled
		c.out.Type, c.chat, c.business = "business_connection", b.UserChatID, b.ID
		c.out.Date, c.out.From, c.out.IsEnabled = b.Date, sender(b.User), &enabled
		c.out.Rights = b.Rights
		return c, true
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		c.out.Type = "callback_query"
		c.out.From = sender(q.From)
		c.out.CallbackData = capText(q.Data, maxCallbackRunes)
		c.out.ChatInstance, c.out.GameShortName = txtName(q.ChatInstance), txtName(q.GameShortName)
		c.callbackID = q.ID
		if q.Message == nil {
			// A callback of an inline message names no chat; only the bot target shows it.
			if q.InlineMessageID == "" {
				return candidate{}, false
			}
			c.botOnly, c.inlineMessageID = true, q.InlineMessageID
			return c, true
		}
		c.chat = q.Message.Chat.ID
		c.out.MessageID, c.out.Date = q.Message.MessageID, q.Message.Date
		c.thread = q.Message.MessageThreadID
		c.callbackMsg = q.Message
		return c, true
	case u.InlineQuery != nil:
		q := u.InlineQuery
		c.out.Type, c.botOnly, c.inlineID = "inline_query", true, q.ID
		c.out.From = sender(q.From)
		c.out.Query = capText(q.Query, maxInlineQueryRunes)
		c.out.Offset, c.out.ChatType = txtName(q.Offset), txtName(q.ChatType)
		c.out.Location = cleanLocation(q.Location)
		return c, true
	case u.ChosenInlineResult != nil:
		// It carries no query identifier: the query was answered already, so there is nothing to reference.
		q := u.ChosenInlineResult
		c.out.Type, c.botOnly = "chosen_inline_result", true
		c.out.From = sender(q.From)
		c.out.Query = capText(q.Query, maxInlineQueryRunes)
		c.out.ResultID = capText(q.ResultID, maxInlineResultRunes)
		c.out.Location = cleanLocation(q.Location)
		c.inlineMessageID = q.InlineMessageID
		return c, true
	}
	return classifyEvent(u, c)
}

// ListUpdates fetches pending updates with one getUpdates request and returns only those of bound chats,
// bound business connections, and, with the bot target, chatless updates. It sends neither offset nor
// allowed_updates, so Telegram keeps every update; nothing is acknowledged.
func (c *Client) ListUpdates(ctx context.Context, limit, wait int) (map[string]any, error) {
	const op = "list updates"
	if len(c.set.chats) == 0 && len(c.set.topics) == 0 && len(c.set.businesses) == 0 {
		return nil, providerError(op, "this connection binds no chat or business target")
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
		if ok && cand.botOnly {
			if !c.set.bot {
				skipped++
				continue
			}
			candidates = append(candidates, cand)
			continue
		}
		if ok && cand.business != "" {
			// A business update is shown only for a business connection this connection binds.
			if !validBusinessID(cand.business) || !c.set.hasBusiness(cand.business) {
				skipped++
				continue
			}
			candidates = append(candidates, cand)
			continue
		}
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
		if cand.botOnly {
			c.bindBot(&cand)
			updates = append(updates, cand.out)
			continue
		}
		if cand.business != "" {
			cand.out.Chat = businessBinding(cand.business)
			cand.out.BusinessChatRef = c.businessChatRef(cand.business, cand.chat)
			if cand.msg != nil {
				c.mapMessage(&cand.out, cand.msg, messageScope{binding: cand.out.Chat, chatID: cand.chat, business: true})
			}
			updates = append(updates, cand.out)
			continue
		}
		binding, known := bound[cand.chat]
		if !known {
			// A chat that only topics bind shows only the messages and callbacks of a bound topic, under that
			// topic target.
			binding, known = c.set.topicBinding(cand.chat, cand.thread)
		}
		if !known {
			skipped++
			continue
		}
		cand.out.Chat = binding
		if cand.msg != nil {
			c.mapMessage(&cand.out, cand.msg, messageScope{binding: binding, chatID: cand.chat, refs: c.fileRefs(binding)})
		}
		if cand.fileID != "" && cand.out.Media != nil {
			cand.out.Media.FileRef = signRef(c.token, refFile, binding, cand.fileID)
		}
		if cand.callbackID != "" {
			cand.out.CallbackRef = signRef(c.token, refCallback, binding, cand.callbackID)
		}
		c.bindMessageOfCallback(&cand, messageScope{binding: binding, chatID: cand.chat, refs: c.fileRefs(binding)})
		if cand.joinQuery != "" {
			cand.out.JoinRequestRef = signRef(c.token, refJoinRequestQuery, binding, cand.joinQuery)
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

// mapMessage adds the media objects and the header members of a message to its update.
func (c *Client) mapMessage(out *updateOut, m *tgMessage, s messageScope) {
	var omitted []string
	out.MediaItems, out.OmittedFields = mediaItems(m, s.refs)
	out.messageHeadOut, omitted = c.messageHead(m, s, false)
	out.OmittedFields = append(out.OmittedFields, omitted...)
}
