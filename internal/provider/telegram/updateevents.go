package telegram

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// This file maps the event objects of updates.list: the Bot API types that are not messages. Each is decoded
// with the members the Bot API documents and printed with the same names; a member that is absent stays absent.
// A ChatInviteLink is decoded without its invite_link member, so the link never reaches an output.

// eventInviteLinkOut is a ChatInviteLink without the link itself, which grants access without a login.
type eventInviteLinkOut struct {
	Creator                 *userOut `json:"creator,omitempty"`
	CreatesJoinRequest      *bool    `json:"creates_join_request,omitempty"`
	IsPrimary               *bool    `json:"is_primary,omitempty"`
	IsRevoked               *bool    `json:"is_revoked,omitempty"`
	Name                    string   `json:"name,omitempty"`
	ExpireDate              int64    `json:"expire_date,omitempty"`
	MemberLimit             int64    `json:"member_limit,omitempty"`
	PendingJoinRequestCount int64    `json:"pending_join_request_count,omitempty"`
	SubscriptionPeriod      int64    `json:"subscription_period,omitempty"`
	SubscriptionPrice       int64    `json:"subscription_price,omitempty"`
}

func cleanEventInviteLink(l *eventInviteLinkOut) *eventInviteLinkOut {
	if l == nil {
		return nil
	}
	out := *l
	out.Creator = headUser(l.Creator)
	out.Name = txtName(l.Name)
	if out == (eventInviteLinkOut{}) {
		return nil
	}
	return &out
}

func cleanMember(m *memberOut) *memberOut {
	if m == nil || (m.Status == "" && m.User.ID == 0) {
		return nil
	}
	out := *m
	if u := headUser(&m.User); u != nil {
		out.User = *u
	}
	out.Status = txtName(m.Status)
	out.CustomTitle = txtName(m.CustomTitle)
	out.Tag = txtName(m.Tag)
	return &out
}

func cleanReactions(list []reactionOut) []reactionOut {
	var out []reactionOut
	for _, r := range list[:min(len(list), maxNestedList)] {
		out = append(out, reactionOut{Type: txtName(r.Type), Emoji: txtName(r.Emoji), CustomEmojiID: txtName(r.CustomEmojiID)})
	}
	return out
}

// reactionCountOut is a ReactionCount; its type is a ReactionType object.
type reactionCountOut struct {
	Type       reactionOut `json:"type"`
	TotalCount int64       `json:"total_count"`
}

// boostOut is a ChatBoost; its source is the Bot API ChatBoostSource shared with the user chat boost tool.
type boostOut struct {
	BoostID        string       `json:"boost_id,omitempty"`
	AddDate        int64        `json:"add_date,omitempty"`
	ExpirationDate int64        `json:"expiration_date,omitempty"`
	Source         *boostSource `json:"source,omitempty"`
}

func cleanBoostSource(s *boostSource) *boostSource {
	if s == nil {
		return nil
	}
	out := *s
	out.Source = txtName(s.Source)
	out.User = headUser(s.User)
	if out == (boostSource{}) {
		return nil
	}
	return &out
}

// Decoded event objects. Each embeds tgEvent, which carries the members they share with the other events.

type tgMemberEvent struct {
	tgEvent
	OldChatMember           *memberOut          `json:"old_chat_member"`
	NewChatMember           *memberOut          `json:"new_chat_member"`
	InviteLink              *eventInviteLinkOut `json:"invite_link"`
	ViaJoinRequest          bool                `json:"via_join_request"`
	ViaChatFolderInviteLink bool                `json:"via_chat_folder_invite_link"`
}

type tgJoinEvent struct {
	tgEvent
	UserChatID int64               `json:"user_chat_id"`
	Bio        string              `json:"bio"`
	InviteLink *eventInviteLinkOut `json:"invite_link"`
}

type tgReactionEvent struct {
	tgEvent
	ActorChat   *tgChat       `json:"actor_chat"`
	OldReaction []reactionOut `json:"old_reaction"`
	NewReaction []reactionOut `json:"new_reaction"`
}

type tgReactionCountEvent struct {
	tgEvent
	Reactions []reactionCountOut `json:"reactions"`
}

type tgBoostEvent struct {
	tgEvent
	Boost *boostOut `json:"boost"`
}

type tgBoostRemovedEvent struct {
	tgEvent
	BoostID    string       `json:"boost_id"`
	RemoveDate int64        `json:"remove_date"`
	Source     *boostSource `json:"source"`
}

type tgShippingQuery struct {
	ID              string              `json:"id"`
	From            *tgUser             `json:"from"`
	InvoicePayload  string              `json:"invoice_payload"`
	ShippingAddress *shippingAddressOut `json:"shipping_address"`
}

type tgPreCheckoutQuery struct {
	ID               string        `json:"id"`
	From             *tgUser       `json:"from"`
	Currency         string        `json:"currency"`
	TotalAmount      *int64        `json:"total_amount"`
	InvoicePayload   string        `json:"invoice_payload"`
	ShippingOptionID string        `json:"shipping_option_id"`
	OrderInfo        *orderInfoOut `json:"order_info"`
}

type tgPaidMediaPurchased struct {
	From             *tgUser `json:"from"`
	PaidMediaPayload string  `json:"paid_media_payload"`
}

type tgPollAnswer struct {
	PollID              string   `json:"poll_id"`
	VoterChat           *tgChat  `json:"voter_chat"`
	User                *tgUser  `json:"user"`
	OptionIDs           *[]int64 `json:"option_ids"`
	OptionPersistentIDs []string `json:"option_persistent_ids"`
}

type tgManagedBot struct {
	User *tgUser `json:"user"`
	Bot  *tgUser `json:"bot"`
}

type tgSubscription struct {
	User           *tgUser `json:"user"`
	InvoicePayload string  `json:"invoice_payload"`
	State          string  `json:"state"`
}

type tgStoppedGeneration struct {
	Chat            tgChat `json:"chat"`
	MessageThreadID int64  `json:"message_thread_id"`
	DraftID         int64  `json:"draft_id"`
}

// updateEventOut holds the members of the event updates that the message-shaped members of updateOut do not
// already carry. A location or a poll of an update uses the location and poll members of the message header.
type updateEventOut struct {
	User                    *userOut            `json:"user,omitempty"`
	Bot                     *userOut            `json:"bot,omitempty"`
	ActorChat               *chatRefOut         `json:"actor_chat,omitempty"`
	VoterChat               *chatRefOut         `json:"voter_chat,omitempty"`
	OldReaction             []reactionOut       `json:"old_reaction,omitempty"`
	NewReaction             []reactionOut       `json:"new_reaction,omitempty"`
	Reactions               []reactionCountOut  `json:"reactions,omitempty"`
	OldChatMember           *memberOut          `json:"old_chat_member,omitempty"`
	NewChatMember           *memberOut          `json:"new_chat_member,omitempty"`
	InviteLink              *eventInviteLinkOut `json:"invite_link,omitempty"`
	ViaJoinRequest          bool                `json:"via_join_request,omitempty"`
	ViaChatFolderInviteLink bool                `json:"via_chat_folder_invite_link,omitempty"`
	Boost                   *boostOut           `json:"boost,omitempty"`
	BoostID                 string              `json:"boost_id,omitempty"`
	RemoveDate              int64               `json:"remove_date,omitempty"`
	Source                  *boostSource        `json:"source,omitempty"`
	UserChatID              int64               `json:"user_chat_id,omitempty"`
	Bio                     string              `json:"bio,omitempty"`
	ChatInstance            string              `json:"chat_instance,omitempty"`
	GameShortName           string              `json:"game_short_name,omitempty"`
	InlineMessageRef        string              `json:"inline_message_ref,omitempty"`
	Message                 *messageOut         `json:"message,omitempty"`
	Offset                  string              `json:"offset,omitempty"`
	ChatType                string              `json:"chat_type,omitempty"`
	Rights                  *businessRights     `json:"rights,omitempty"`
	ShippingQueryRef        string              `json:"shipping_query_ref,omitempty"`
	PreCheckoutQueryRef     string              `json:"pre_checkout_query_ref,omitempty"`
	InvoicePayload          string              `json:"invoice_payload,omitempty"`
	PaidMediaPayload        string              `json:"paid_media_payload,omitempty"`
	Currency                string              `json:"currency,omitempty"`
	TotalAmount             *int64              `json:"total_amount,omitempty"`
	ShippingOptionID        string              `json:"shipping_option_id,omitempty"`
	ShippingAddress         *shippingAddressOut `json:"shipping_address,omitempty"`
	OrderInfo               *orderInfoOut       `json:"order_info,omitempty"`
	PollID                  string              `json:"poll_id,omitempty"`
	OptionIDs               *[]int64            `json:"option_ids,omitempty"`
	OptionPersistentIDs     []string            `json:"option_persistent_ids,omitempty"`
	State                   string              `json:"state,omitempty"`
	DraftID                 int64               `json:"draft_id,omitempty"`
}

// The classify helpers below fill the event members of a candidate from one decoded update.

func (c *candidate) memberEvent(e *tgMemberEvent) {
	c.out.OldChatMember, c.out.NewChatMember = cleanMember(e.OldChatMember), cleanMember(e.NewChatMember)
	c.out.InviteLink = cleanEventInviteLink(e.InviteLink)
	c.out.ViaJoinRequest, c.out.ViaChatFolderInviteLink = e.ViaJoinRequest, e.ViaChatFolderInviteLink
}

func (c *candidate) joinEvent(e *tgJoinEvent) {
	c.out.UserChatID, c.out.Bio = e.UserChatID, txtName(e.Bio)
	c.out.InviteLink = cleanEventInviteLink(e.InviteLink)
}

func (c *candidate) reactionEvent(e *tgReactionEvent) {
	c.out.User = headUser(e.User)
	c.out.ActorChat = cleanChatRef(e.ActorChat)
	c.out.OldReaction, c.out.NewReaction = cleanReactions(e.OldReaction), cleanReactions(e.NewReaction)
}

func (c *candidate) reactionCountEvent(e *tgReactionCountEvent) {
	for _, r := range e.Reactions[:min(len(e.Reactions), maxNestedList)] {
		c.out.Reactions = append(c.out.Reactions, reactionCountOut{
			Type:       reactionOut{Type: txtName(r.Type.Type), Emoji: txtName(r.Type.Emoji), CustomEmojiID: txtName(r.Type.CustomEmojiID)},
			TotalCount: r.TotalCount})
	}
}

func (c *candidate) boostEvent(e *tgBoostEvent) {
	if b := e.Boost; b != nil {
		c.out.Boost = &boostOut{BoostID: txtName(b.BoostID), AddDate: b.AddDate, ExpirationDate: b.ExpirationDate,
			Source: cleanBoostSource(b.Source)}
	}
}

func (c *candidate) boostRemovedEvent(e *tgBoostRemovedEvent) {
	c.out.BoostID, c.out.RemoveDate, c.out.Source = txtName(e.BoostID), e.RemoveDate, cleanBoostSource(e.Source)
}

// classifyEvent handles the chatless update types and the stopped generation; ok is false for any other type.
func classifyEvent(u tgUpdate, c candidate) (candidate, bool) {
	switch {
	case u.StoppedMessageGeneration != nil:
		s := u.StoppedMessageGeneration
		c.out.Type, c.chat, c.thread = "stopped_message_generation", s.Chat.ID, s.MessageThreadID
		c.out.MessageThreadID, c.out.DraftID = s.MessageThreadID, s.DraftID
		return c, true
	case u.ShippingQuery != nil:
		q := u.ShippingQuery
		c.out.Type, c.botOnly, c.shippingID = "shipping_query", true, q.ID
		c.out.From = headUser(q.From)
		c.out.InvoicePayload = txtName(q.InvoicePayload)
		c.out.ShippingAddress = cleanAddress(q.ShippingAddress)
		return c, true
	case u.PreCheckoutQuery != nil:
		q := u.PreCheckoutQuery
		c.out.Type, c.botOnly, c.preCheckoutID = "pre_checkout_query", true, q.ID
		c.out.From = headUser(q.From)
		c.out.Currency, c.out.TotalAmount = txtName(q.Currency), q.TotalAmount
		c.out.InvoicePayload, c.out.ShippingOptionID = txtName(q.InvoicePayload), txtName(q.ShippingOptionID)
		c.out.OrderInfo = cleanOrderInfo(q.OrderInfo)
		return c, true
	case u.PurchasedPaidMedia != nil:
		p := u.PurchasedPaidMedia
		c.out.Type, c.botOnly = "purchased_paid_media", true
		c.out.From = headUser(p.From)
		c.out.PaidMediaPayload = txtName(p.PaidMediaPayload)
		return c, true
	case u.Poll != nil:
		c.out.Type, c.botOnly, c.poll = "poll", true, u.Poll
		return c, true
	case u.PollAnswer != nil:
		a := u.PollAnswer
		c.out.Type, c.botOnly = "poll_answer", true
		c.out.PollID, c.out.VoterChat, c.out.User = txtName(a.PollID), cleanChatRef(a.VoterChat), headUser(a.User)
		c.out.OptionIDs = a.OptionIDs
		c.out.OptionPersistentIDs = cleanCodes(a.OptionPersistentIDs)
		return c, true
	case u.ManagedBot != nil:
		c.out.Type, c.botOnly = "managed_bot", true
		c.out.User, c.out.Bot = headUser(u.ManagedBot.User), headUser(u.ManagedBot.Bot)
		return c, true
	case u.Subscription != nil:
		s := u.Subscription
		c.out.Type, c.botOnly = "subscription", true
		c.out.User, c.out.InvoicePayload, c.out.State = headUser(s.User), txtName(s.InvoicePayload), txtName(s.State)
		return c, true
	}
	return candidate{}, false
}

// bindBot completes a chatless update once the connection is known to bind the bot target: it signs the
// identifiers that have no chat and maps the poll.
func (c *Client) bindBot(cand *candidate) {
	cand.out.Chat = botTarget
	if cand.inlineID != "" {
		cand.out.InlineRef = signRef(c.token, refInlineQuery, botTarget, cand.inlineID)
	}
	if cand.callbackID != "" {
		cand.out.CallbackRef = signRef(c.token, refCallback, botTarget, cand.callbackID)
	}
	if cand.inlineMessageID != "" {
		cand.out.InlineMessageRef = signRef(c.token, refInlineMessage, botTarget, cand.inlineMessageID)
	}
	if cand.shippingID != "" {
		cand.out.ShippingQueryRef = signRef(c.token, refShippingQuery, botTarget, cand.shippingID)
	}
	if cand.preCheckoutID != "" {
		cand.out.PreCheckoutQueryRef = signRef(c.token, refPreCheckoutQuery, botTarget, cand.preCheckoutID)
	}
	if cand.poll != nil {
		var inner, omitted []string
		scope := messageScope{binding: botTarget, refs: c.fileRefs(botTarget)}
		poll := scope.poll(cand.poll, &inner)
		scoped(&omitted, "poll", inner)
		cand.out.Poll = bounded(&omitted, "poll", poll)
		cand.out.OmittedFields = append(cand.out.OmittedFields, omitted...)
	}
}

// bindMessageOfCallback maps the message a button belongs to like a message update: its members are bounded one by
// one, it follows a reply one level, and what it leaves out is named in its own omitted_fields.
func (c *Client) bindMessageOfCallback(cand *candidate, s messageScope) {
	if cand.callbackMsg == nil {
		return
	}
	m := cand.callbackMsg
	msg := &messageOut{MessageID: m.MessageID, MessageThreadID: m.MessageThreadID, Date: m.Date,
		From: headUser(m.From), Text: capText(m.Text, maxMessageLength), Caption: capText(m.Caption, maxCaptionRunes)}
	msg.MediaItems, msg.OmittedFields = mediaItems(m, s.refs)
	head, omitted := c.messageHead(m, s, false)
	msg.messageHeadOut = head
	msg.OmittedFields = append(msg.OmittedFields, omitted...)
	cand.out.Message = msg
}

var updateEventSchema = func() string {
	var b strings.Builder
	for _, p := range updateEventProperties {
		b.WriteString(chatInfoProperty(p.name, p.schema))
	}
	return b.String()
}()

var updateEventProperties = []struct{ name, schema string }{
	{"user", userSchema}, {"bot", userSchema},
	{"actor_chat", nestedSchema("Telegram Bot API Chat that reacted anonymously; data only, never a target")},
	{"voter_chat", nestedSchema("Telegram Bot API Chat that voted anonymously; data only, never a target")},
	{"old_reaction", nestedListSchema("Telegram Bot API ReactionType list: type, emoji or custom_emoji_id")},
	{"new_reaction", nestedListSchema("Telegram Bot API ReactionType list: type, emoji or custom_emoji_id")},
	{"reactions", nestedListSchema("Telegram Bot API ReactionCount list: type (a ReactionType), total_count")},
	{"old_chat_member", nestedSchema("Telegram Bot API ChatMember before the change: status, user and the members of that status")},
	{"new_chat_member", nestedSchema("Telegram Bot API ChatMember after the change: status, user and the members of that status")},
	{"invite_link", nestedSchema("Telegram Bot API ChatInviteLink without the link itself")},
	{"via_join_request", `{"type":"boolean"}`}, {"via_chat_folder_invite_link", `{"type":"boolean"}`},
	{"boost", nestedSchema("Telegram Bot API ChatBoost: boost_id, add_date, expiration_date, source")},
	{"boost_id", `{"type":"string"}`}, {"remove_date", `{"type":"integer"}`},
	{"source", nestedSchema("Telegram Bot API ChatBoostSource: source and the members of that source")},
	{"user_chat_id", `{"type":"integer"}`}, {"bio", `{"type":"string"}`},
	{"chat_instance", `{"type":"string"}`}, {"game_short_name", `{"type":"string"}`},
	{"inline_message_ref", `{"type":"string"}`},
	{"message", nestedSchema("The message of a button press with the members of a message update")},
	{"offset", `{"type":"string"}`}, {"chat_type", `{"type":"string"}`},
	{"rights", nestedSchema("Telegram Bot API BusinessBotRights, each as a boolean")},
	{"shipping_query_ref", `{"type":"string"}`}, {"pre_checkout_query_ref", `{"type":"string"}`},
	{"invoice_payload", `{"type":"string"}`}, {"paid_media_payload", `{"type":"string"}`},
	{"currency", `{"type":"string"}`}, {"total_amount", `{"type":"integer"}`}, {"shipping_option_id", `{"type":"string"}`},
	{"shipping_address", nestedSchema("Telegram Bot API ShippingAddress; untrusted text, never fetched")},
	{"order_info", nestedSchema("Telegram Bot API OrderInfo: name, phone_number, email, shipping_address; untrusted text")},
	{"poll_id", `{"type":"string"}`},
	{"option_ids", `{"type":"array","items":{"type":"integer"}}`},
	{"option_persistent_ids", `{"type":"array","items":{"type":"string"}}`},
	{"state", `{"type":"string"}`}, {"draft_id", `{"type":"integer"}`},
}

// updateBaseFields names the members every update or several update types share.
var updateBaseFields = []capability.Field{
	{Name: "update_id", Description: "Update identifier"},
	{Name: "type", Description: "Update type of the Bot API, for example message, callback_query, or poll"},
	{Name: "chat", Description: "Bound target the update belongs to: the chat or topic target, business/<id>, or bot"},
	{Name: "message_id", Description: "Message identifier of a message, event, or button press"},
	{Name: "date", Description: "Unix time of the update"},
	{Name: "message_thread_id", Description: "Topic of a message or a stopped generation"},
	{Name: "reply_to_message_id", Description: "Identifier of the message a message replies to"},
	{Name: "from", Description: "Sender, presser, requester, or actor of the update as " + userFieldsText},
	{Name: "business_chat_ref", Description: "Signed reference of the chat of a business update, which names its chat only so"},
	{Name: "location", Description: "Location of an inline query or chosen inline result, or the location of a message"},
	{Name: "poll", Description: "Poll of a poll update, or the poll of a message, with its options; every file_id is a file_ref"},
	{Name: "text", Description: "Message text, untrusted and bounded"},
	{Name: "caption", Description: "Message caption, untrusted and bounded"},
	{Name: "media", Description: "Summary of the main media object of a message with a signed file_ref"},
	{Name: "callback_data", Description: "Data of a pressed button, untrusted and bounded"},
	{Name: "callback_ref", Description: "Signed reference to answer a callback query; the reference of an inline message callback needs the bot target"},
	{Name: "join_request_ref", Description: "Signed reference to answer a join request query"},
	{Name: "query", Description: "Text of an inline query, untrusted and bounded"},
	{Name: "result_id", Description: "Identifier of the chosen inline result"},
	{Name: "inline_ref", Description: "Signed reference to answer an inline query; it needs the bot target"},
	{Name: "message_ids", Description: "Identifiers of deleted business messages"},
	{Name: "is_enabled", Description: "Whether a business connection is enabled"},
}

// updateEventFields names the members of updateEventOut for the tool fields.
var updateEventFields = []capability.Field{
	{Name: "user", Description: "User of a reaction, poll answer, managed bot, or subscription as " + userFieldsText},
	{Name: "bot", Description: "Managed bot of a managed_bot update as " + userFieldsText},
	{Name: "actor_chat", Description: "Chat that reacted anonymously; data only, never a target"},
	{Name: "voter_chat", Description: "Chat that voted anonymously; data only, never a target"},
	{Name: "old_reaction", Description: "Reactions of the user before a message_reaction update as ReactionType"},
	{Name: "new_reaction", Description: "Reactions of the user after a message_reaction update as ReactionType"},
	{Name: "reactions", Description: "Reaction counts of a message_reaction_count update as ReactionCount"},
	{Name: "old_chat_member", Description: "ChatMember before a my_chat_member or chat_member change, with its rights"},
	{Name: "new_chat_member", Description: "ChatMember after a my_chat_member or chat_member change, with its rights"},
	{Name: "invite_link", Description: "ChatInviteLink of a member or join request event without the link itself, which grants access without a login"},
	{Name: "via_join_request", Description: "True when the user joined after a join request"},
	{Name: "via_chat_folder_invite_link", Description: "True when the user joined through a chat folder invite link"},
	{Name: "boost", Description: "ChatBoost of a chat_boost update: boost_id, add_date, expiration_date, source"},
	{Name: "boost_id", Description: "Boost identifier of a removed_chat_boost update"},
	{Name: "remove_date", Description: "Unix time a boost was removed"},
	{Name: "source", Description: "ChatBoostSource of a removed boost: source and the members of that source"},
	{Name: "user_chat_id", Description: "Identifier of the private chat with the user of a join request"},
	{Name: "bio", Description: "Bio of the user of a join request; untrusted text"},
	{Name: "chat_instance", Description: "Global identifier of the chat of a callback query"},
	{Name: "game_short_name", Description: "Short name of the game of a callback query"},
	{Name: "inline_message_ref", Description: "Signed reference of the inline message of a chosen inline result or callback query; it needs the bot target"},
	{Name: "message", Description: "Message of a callback query with the members of a message update"},
	{Name: "offset", Description: "Offset of an inline query"},
	{Name: "chat_type", Description: "Chat type of an inline query"},
	{Name: "rights", Description: "BusinessBotRights of a business_connection update, each as a boolean"},
	{Name: "shipping_query_ref", Description: "Signed reference of a shipping query; it needs the bot target"},
	{Name: "pre_checkout_query_ref", Description: "Signed reference of a pre-checkout query; it needs the bot target"},
	{Name: "invoice_payload", Description: "Bot-defined invoice payload; untrusted text"},
	{Name: "paid_media_payload", Description: "Bot-defined payload of purchased paid media; untrusted text"},
	{Name: "currency", Description: "Currency of a pre-checkout query"},
	{Name: "total_amount", Description: "Total price of a pre-checkout query in the smallest units of its currency"},
	{Name: "shipping_option_id", Description: "Identifier of the shipping option the user chose"},
	{Name: "shipping_address", Description: "ShippingAddress of a shipping query; untrusted text, never fetched"},
	{Name: "order_info", Description: "OrderInfo of a pre-checkout query; untrusted text"},
	{Name: "poll_id", Description: "Identifier of the poll of a poll_answer update"},
	{Name: "option_ids", Description: "Zero-based options a poll_answer update chose; empty when the vote was retracted"},
	{Name: "option_persistent_ids", Description: "Persistent identifiers of the options a poll_answer update chose"},
	{Name: "state", Description: "State of a subscription update"},
	{Name: "draft_id", Description: "Draft identifier of a stopped_message_generation update"},
}
