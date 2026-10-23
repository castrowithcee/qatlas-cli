package telegram

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// This file is the only mapping of the Bot API type Message for updates.list, users.personal-chat-messages and
// chats.get. Each type is decoded with the members the Bot API documents and printed with the same names; a
// member that is absent stays absent. Members that name a chat, a user or a story outside the target binding
// are data only and never become a target.

type directMessagesTopicOut struct {
	TopicID int64    `json:"topic_id"`
	User    *userOut `json:"user,omitempty"`
}

// messageOriginOut is the union MessageOrigin; type tells which members are present.
type messageOriginOut struct {
	Type            string      `json:"type"`
	Date            int64       `json:"date,omitempty"`
	SenderUser      *userOut    `json:"sender_user,omitempty"`
	SenderUserName  string      `json:"sender_user_name,omitempty"`
	SenderChat      *chatRefOut `json:"sender_chat,omitempty"`
	Chat            *chatRefOut `json:"chat,omitempty"`
	MessageID       int64       `json:"message_id,omitempty"`
	AuthorSignature string      `json:"author_signature,omitempty"`
}

type externalReplyOut struct {
	Origin    *messageOriginOut `json:"origin,omitempty"`
	Chat      *chatRefOut       `json:"chat,omitempty"`
	MessageID int64             `json:"message_id,omitempty"`
}

// quoteOut keeps position 0: it is a value, not an absence.
type quoteOut struct {
	Text     string `json:"text"`
	Position int64  `json:"position"`
	IsManual bool   `json:"is_manual,omitempty"`
}

type storyOut struct {
	Chat *chatRefOut `json:"chat,omitempty"`
	ID   int64       `json:"id"`
}

type (
	tgDirectTopic   = directMessagesTopicOut
	tgOrigin        = messageOriginOut
	tgExternalReply = externalReplyOut
	tgQuote         = quoteOut
	tgStory         = storyOut
)

// messageHeadOut holds the header and content members of a Message that the flat fields of every message
// output do not already carry, so all three tools gain every member together.
type messageHeadOut struct {
	messageContentOut
	DirectMessagesTopic    *directMessagesTopicOut `json:"direct_messages_topic,omitempty"`
	SenderChat             *chatRefOut             `json:"sender_chat,omitempty"`
	SenderBoostCount       int64                   `json:"sender_boost_count,omitempty"`
	SenderBusinessBot      *userOut                `json:"sender_business_bot,omitempty"`
	SenderTag              string                  `json:"sender_tag,omitempty"`
	ReceiverUser           *userOut                `json:"receiver_user,omitempty"`
	EphemeralMessageID     int64                   `json:"ephemeral_message_id,omitempty"`
	GuestQueryRef          string                  `json:"guest_query_ref,omitempty"`
	ChatInfo               *chatRefOut             `json:"chat_info,omitempty"`
	ForwardOrigin          *messageOriginOut       `json:"forward_origin,omitempty"`
	IsTopicMessage         bool                    `json:"is_topic_message,omitempty"`
	IsAutomaticForward     bool                    `json:"is_automatic_forward,omitempty"`
	ReplyToMessage         *messageOut             `json:"reply_to_message,omitempty"`
	ExternalReply          *externalReplyOut       `json:"external_reply,omitempty"`
	Quote                  *quoteOut               `json:"quote,omitempty"`
	ReplyToStory           *storyOut               `json:"reply_to_story,omitempty"`
	ReplyToChecklistTaskID int64                   `json:"reply_to_checklist_task_id,omitempty"`
	ReplyToPollOptionID    string                  `json:"reply_to_poll_option_id,omitempty"`
	ViaBot                 *userOut                `json:"via_bot,omitempty"`
	GuestBotCallerUser     *userOut                `json:"guest_bot_caller_user,omitempty"`
	GuestBotCallerChat     *chatRefOut             `json:"guest_bot_caller_chat,omitempty"`
	EditDate               int64                   `json:"edit_date,omitempty"`
	HasProtectedContent    bool                    `json:"has_protected_content,omitempty"`
	IsFromOffline          bool                    `json:"is_from_offline,omitempty"`
	IsPaidPost             bool                    `json:"is_paid_post,omitempty"`
	MediaGroupID           string                  `json:"media_group_id,omitempty"`
	AuthorSignature        string                  `json:"author_signature,omitempty"`
	PaidStarCount          int64                   `json:"paid_star_count,omitempty"`
}

// messageOut is a Message nested in another output: the replied-to or the pinned message.
type messageOut struct {
	MessageID       int64      `json:"message_id,omitempty"`
	MessageThreadID int64      `json:"message_thread_id,omitempty"`
	Date            int64      `json:"date,omitempty"`
	From            *senderOut `json:"from,omitempty"`
	Text            string     `json:"text,omitempty"`
	Caption         string     `json:"caption,omitempty"`
	MediaItems      []any      `json:"media_items,omitempty"`
	OmittedFields   []string   `json:"omitted_fields,omitempty"`
	messageHeadOut
}

// messageScope is where a message was read: the binding its references are signed for, the chat it belongs to,
// and whether that chat is a business chat, which is never printed.
type messageScope struct {
	binding string
	chatID  int64
	// business messages print no file reference and no raw chat.
	business bool
	refs     fileRefFunc
}

// headUser cleans the texts of a User that is part of a header member.
func headUser(u *tgUser) *userOut {
	out := sender(u)
	if out == nil {
		return nil
	}
	out.FirstName = cleanText(out.FirstName, maxNameRunes)
	out.LastName = cleanText(out.LastName, maxNameRunes)
	out.Username = cleanText(out.Username, maxNameRunes)
	out.LanguageCode = cleanText(out.LanguageCode, maxNameRunes)
	return out
}

// headChat cleans a Chat named by a header member; the business chat of the message itself is dropped.
func (s messageScope) headChat(c *chatRefOut) *chatRefOut {
	out := cleanChatRef(c)
	if out == nil || (s.business && out.ID == s.chatID) {
		return nil
	}
	return out
}

func (s messageScope) origin(o *messageOriginOut) *messageOriginOut {
	if o == nil || o.Type == "" {
		return nil
	}
	return &messageOriginOut{Type: cleanText(o.Type, maxNameRunes), Date: o.Date, SenderUser: headUser(o.SenderUser),
		SenderUserName: cleanText(o.SenderUserName, maxNameRunes), SenderChat: s.headChat(o.SenderChat),
		Chat: s.headChat(o.Chat), MessageID: o.MessageID,
		AuthorSignature: cleanText(o.AuthorSignature, maxNameRunes)}
}

// messageHead maps the header members of m. A member over the nesting limits is left out and named in omitted.
// A nested message maps no reply of its own, so replies are followed one level only.
func (c *Client) messageHead(m *tgMessage, s messageScope, nested bool) (head messageHeadOut, omitted []string) {
	var contentOmitted []string
	head = messageHeadOut{
		SenderBoostCount: m.SenderBoostCount, SenderTag: cleanText(m.SenderTag, maxNameRunes),
		EphemeralMessageID: m.EphemeralMessageID, IsTopicMessage: m.IsTopicMessage,
		IsAutomaticForward: m.IsAutomaticForward, ReplyToChecklistTaskID: m.ReplyToChecklistTaskID,
		ReplyToPollOptionID: cleanText(m.ReplyToPollOptionID, maxNameRunes), EditDate: m.EditDate,
		HasProtectedContent: m.HasProtectedContent, IsFromOffline: m.IsFromOffline, IsPaidPost: m.IsPaidPost,
		MediaGroupID: cleanText(m.MediaGroupID, maxNameRunes), AuthorSignature: cleanText(m.AuthorSignature, maxNameRunes),
		PaidStarCount: m.PaidStarCount,
	}
	if t := m.DirectMessagesTopic; t != nil {
		head.DirectMessagesTopic = bounded(&omitted, "direct_messages_topic",
			&directMessagesTopicOut{TopicID: t.TopicID, User: headUser(t.User)})
	}
	head.messageContentOut, contentOmitted = s.messageContent(m)
	omitted = append(omitted, contentOmitted...)
	head.SenderChat = bounded(&omitted, "sender_chat", s.headChat(m.SenderChat))
	head.SenderBusinessBot = bounded(&omitted, "sender_business_bot", headUser(m.SenderBusinessBot))
	head.ReceiverUser = bounded(&omitted, "receiver_user", headUser(m.ReceiverUser))
	head.ViaBot = bounded(&omitted, "via_bot", headUser(m.ViaBot))
	head.GuestBotCallerUser = bounded(&omitted, "guest_bot_caller_user", headUser(m.GuestBotCallerUser))
	head.GuestBotCallerChat = bounded(&omitted, "guest_bot_caller_chat", s.headChat(m.GuestBotCallerChat))
	if m.GuestQueryID != "" {
		head.GuestQueryRef = signRef(c.token, refGuestQuery, s.binding, m.GuestQueryID)
	}
	// A bare chat identifier repeats what chat already says, so chat_info appears only with more of the Chat.
	if info := s.headChat(&m.Chat); info != nil && *info != (chatRefOut{ID: info.ID}) {
		head.ChatInfo = bounded(&omitted, "chat_info", info)
	}
	head.ForwardOrigin = bounded(&omitted, "forward_origin", s.origin(m.ForwardOrigin))
	if e := m.ExternalReply; e != nil {
		head.ExternalReply = bounded(&omitted, "external_reply", &externalReplyOut{
			Origin: s.origin(e.Origin), Chat: s.headChat(e.Chat), MessageID: e.MessageID})
	}
	if q := m.Quote; q != nil {
		head.Quote = bounded(&omitted, "quote", &quoteOut{Text: cleanText(q.Text, maxMessageLength),
			Position: q.Position, IsManual: q.IsManual})
	}
	if st := m.ReplyToStory; st != nil {
		head.ReplyToStory = bounded(&omitted, "reply_to_story", &storyOut{Chat: s.headChat(st.Chat), ID: st.ID})
	}
	if m.ReplyTo != nil && !nested {
		head.ReplyToMessage = bounded(&omitted, "reply_to_message", c.nestedMessage(m.ReplyTo, s))
	}
	return head, omitted
}

// nestedMessage maps a message that is part of another output, such as the replied-to or the pinned message. Its text
// and caption are bounded like those of the message that contains it.
func (c *Client) nestedMessage(m *tgMessage, s messageScope) *messageOut {
	out := &messageOut{MessageID: m.MessageID, MessageThreadID: m.MessageThreadID, Date: m.Date,
		From: headUser(m.From), Text: capText(m.Text, maxMessageLength), Caption: capText(m.Caption, maxCaptionRunes)}
	out.MediaItems, out.OmittedFields = mediaItems(m, s.refs)
	head, omitted := c.messageHead(m, s, true)
	out.messageHeadOut = head
	out.OmittedFields = append(out.OmittedFields, omitted...)
	return out
}

// messageHeadSchema is the property list of the header members, to be appended to a message object schema.
var messageHeadSchema = func() string {
	var b strings.Builder
	for _, p := range []struct{ name, schema string }{
		{"direct_messages_topic", nestedSchema("Telegram Bot API DirectMessagesTopic: topic_id, user")},
		{"sender_chat", nestedSchema("Telegram Bot API Chat of the sender; data only, never a target")},
		{"sender_boost_count", `{"type":"integer"}`},
		{"sender_business_bot", userSchema}, {"sender_tag", `{"type":"string"}`}, {"receiver_user", userSchema},
		{"ephemeral_message_id", `{"type":"integer"}`},
		{"guest_query_ref", `{"type":"string"}`},
		{"chat_info", nestedSchema("Telegram Bot API Chat of the message; data only, never a target; absent for business messages")},
		{"forward_origin", nestedSchema("Telegram Bot API MessageOrigin: type, date and the members of that type; chats in it are data only")},
		{"is_topic_message", `{"type":"boolean"}`}, {"is_automatic_forward", `{"type":"boolean"}`},
		{"reply_to_message", nestedSchema("The replied-to message with the members of this message except its own reply_to_message")},
		{"external_reply", nestedSchema("Telegram Bot API ExternalReplyInfo: origin, chat, message_id; chats in it are data only")},
		{"quote", nestedSchema("Telegram Bot API TextQuote: text, position, is_manual")},
		{"reply_to_story", nestedSchema("Telegram Bot API Story: chat, id")},
		{"reply_to_checklist_task_id", `{"type":"integer"}`}, {"reply_to_poll_option_id", `{"type":"string"}`},
		{"via_bot", userSchema}, {"guest_bot_caller_user", userSchema},
		{"guest_bot_caller_chat", nestedSchema("Telegram Bot API Chat of the guest bot caller; data only, never a target")},
		{"edit_date", `{"type":"integer"}`}, {"has_protected_content", `{"type":"boolean"}`},
		{"is_from_offline", `{"type":"boolean"}`}, {"is_paid_post", `{"type":"boolean"}`},
		{"media_group_id", `{"type":"string"}`}, {"author_signature", `{"type":"string"}`},
		{"paid_star_count", `{"type":"integer"}`},
		{"entities", nestedListSchema("Telegram Bot API MessageEntity list of text, at most 100; url is untrusted text, never fetched")},
		{"link_preview_options", nestedSchema("Telegram Bot API LinkPreviewOptions; url is untrusted text, never fetched")},
		{"suggested_post_info", nestedSchema("Telegram Bot API SuggestedPostInfo: state, price (currency, amount), send_date")},
		{"effect_id", `{"type":"string"}`},
		{"rich_message", nestedSchema("Telegram Bot API RichMessage: blocks, is_rtl; every file_id is a file_ref")},
		{"paid_media", nestedSchema("Telegram Bot API PaidMediaInfo: star_count, paid_media; every file_id is a file_ref")},
		{"story", nestedSchema("Telegram Bot API Story: chat, id; the chat is data only")},
		{"caption_entities", nestedListSchema("Telegram Bot API MessageEntity list of the caption, at most 100")},
		{"show_caption_above_media", `{"type":"boolean"}`}, {"has_media_spoiler", `{"type":"boolean"}`},
		{"checklist", nestedSchema("Telegram Bot API Checklist: title, title_entities, tasks, others_can_add_tasks, others_can_mark_tasks_as_done")},
		{"contact", nestedSchema("Telegram Bot API Contact: phone_number, first_name, last_name, user_id, vcard")},
		{"dice", nestedSchema("Telegram Bot API Dice: emoji, value")},
		{"game", nestedSchema("Telegram Bot API Game; every file_id is a file_ref")},
		{"poll", nestedSchema("Telegram Bot API Poll with its options; every file_id is a file_ref")},
		{"venue", nestedSchema("Telegram Bot API Venue")},
		{"location", nestedSchema("Telegram Bot API Location; coordinates print as Telegram sent them")},
		{"invoice", nestedSchema("Telegram Bot API Invoice")},
		{"reply_markup", nestedSchema("Telegram Bot API InlineKeyboardMarkup; addresses in buttons are untrusted text, never fetched")},
		{"giveaway", nestedSchema("Telegram Bot API Giveaway; chats in it are data only")},
		{"giveaway_winners", nestedSchema("Telegram Bot API GiveawayWinners; the chat in it is data only")},
	} {
		b.WriteString(chatInfoProperty(p.name, p.schema))
	}
	return b.String()
}()

// messageHeadText names the header members for tool field descriptions.
const messageHeadText = "the Bot API Message header members direct_messages_topic, sender_chat, sender_boost_count, " +
	"sender_business_bot, sender_tag, receiver_user, ephemeral_message_id, guest_query_ref (a signed reference of " +
	"guest_query_id, bound to the target), chat_info (the Chat of the message), forward_origin, is_topic_message, " +
	"is_automatic_forward, reply_to_message (one level), external_reply, quote, reply_to_story, " +
	"reply_to_checklist_task_id, reply_to_poll_option_id, via_bot, guest_bot_caller_user, guest_bot_caller_chat, " +
	"edit_date, has_protected_content, is_from_offline, is_paid_post, media_group_id, author_signature and " +
	"paid_star_count and the Bot API Message content members entities, link_preview_options, suggested_post_info, " +
	"effect_id, rich_message, paid_media, story, caption_entities, show_caption_above_media, has_media_spoiler, " +
	"checklist, contact, dice, game, poll, venue, location, invoice, reply_markup, giveaway and giveaway_winners " +
	"(each text bounded and entities at most 100; every file_id is a file_ref; addresses are untrusted text and " +
	"never fetched), each only when Telegram sent it; chats named there are data only and never targets, and a " +
	"business message shows no chat of its connection"

var messageHeadFields = []capability.Field{
	{Name: "pinned_message", Description: "Pinned message as an object with message_id, message_thread_id, date, from, text, caption, media_items, omitted_fields and " + messageHeadText},
}
