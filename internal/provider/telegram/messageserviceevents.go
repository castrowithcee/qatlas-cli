package telegram

// This file maps the payment, share, gift, giveaway, suggested post, checklist, poll option, community and
// background service members of Message; messageservice.go holds the rest and the shared rules.

type shippingAddressOut struct {
	CountryCode string `json:"country_code,omitempty"`
	State       string `json:"state,omitempty"`
	City        string `json:"city,omitempty"`
	StreetLine1 string `json:"street_line1,omitempty"`
	StreetLine2 string `json:"street_line2,omitempty"`
	PostCode    string `json:"post_code,omitempty"`
}

type orderInfoOut struct {
	Name            string              `json:"name,omitempty"`
	PhoneNumber     string              `json:"phone_number,omitempty"`
	Email           string              `json:"email,omitempty"`
	ShippingAddress *shippingAddressOut `json:"shipping_address,omitempty"`
}

// successfulPaymentOut is the Bot API SuccessfulPayment. The charge identifiers are printed: no tool refunds,
// transfers or converts with them.
type successfulPaymentOut struct {
	Currency                   string        `json:"currency"`
	TotalAmount                int64         `json:"total_amount"`
	InvoicePayload             string        `json:"invoice_payload"`
	SubscriptionExpirationDate int64         `json:"subscription_expiration_date,omitempty"`
	IsRecurring                bool          `json:"is_recurring,omitempty"`
	IsFirstRecurring           bool          `json:"is_first_recurring,omitempty"`
	ShippingOptionID           string        `json:"shipping_option_id,omitempty"`
	OrderInfo                  *orderInfoOut `json:"order_info,omitempty"`
	TelegramPaymentChargeID    string        `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID    string        `json:"provider_payment_charge_id"`
}

type refundedPaymentOut struct {
	Currency                string `json:"currency"`
	TotalAmount             int64  `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id,omitempty"`
}

// sharedUserOut is the Bot API SharedUser; the names are what the bot asked for and are untrusted text.
type sharedUserOut struct {
	UserID    int64       `json:"user_id"`
	FirstName string      `json:"first_name,omitempty"`
	LastName  string      `json:"last_name,omitempty"`
	Username  string      `json:"username,omitempty"`
	Photo     []photoSize `json:"photo,omitempty"`
}

type usersSharedOut struct {
	RequestID int64           `json:"request_id"`
	Users     []sharedUserOut `json:"users,omitempty"`
}

// chatSharedOut is the Bot API ChatShared; chat_id is data only and never a target.
type chatSharedOut struct {
	RequestID int64       `json:"request_id"`
	ChatID    int64       `json:"chat_id"`
	Title     string      `json:"title,omitempty"`
	Username  string      `json:"username,omitempty"`
	Photo     []photoSize `json:"photo,omitempty"`
}

type tgGiftInfo struct {
	Gift                    tgGift      `json:"gift"`
	OwnedGiftID             string      `json:"owned_gift_id"`
	ConvertStarCount        int64       `json:"convert_star_count"`
	PrepaidUpgradeStarCount int64       `json:"prepaid_upgrade_star_count"`
	IsUpgradeSeparate       bool        `json:"is_upgrade_separate"`
	CanBeUpgraded           bool        `json:"can_be_upgraded"`
	Text                    string      `json:"text"`
	Entities                []entityOut `json:"entities"`
	IsPrivate               bool        `json:"is_private"`
	UniqueGiftNumber        int64       `json:"unique_gift_number"`
}

type giftInfoOut struct {
	Gift                    *giftDetailOut `json:"gift,omitempty"`
	OwnedGiftID             string         `json:"owned_gift_id,omitempty"`
	ConvertStarCount        int64          `json:"convert_star_count,omitempty"`
	PrepaidUpgradeStarCount int64          `json:"prepaid_upgrade_star_count,omitempty"`
	IsUpgradeSeparate       bool           `json:"is_upgrade_separate,omitempty"`
	CanBeUpgraded           bool           `json:"can_be_upgraded,omitempty"`
	Text                    string         `json:"text,omitempty"`
	Entities                []entityOut    `json:"entities,omitempty"`
	IsPrivate               bool           `json:"is_private,omitempty"`
	UniqueGiftNumber        int64          `json:"unique_gift_number,omitempty"`
}

type tgUniqueGiftInfo struct {
	Gift               uniqueGiftOut `json:"gift"`
	Origin             string        `json:"origin"`
	Text               string        `json:"text"`
	Entities           []entityOut   `json:"entities"`
	IsPrivate          bool          `json:"is_private"`
	LastResaleCurrency string        `json:"last_resale_currency"`
	LastResaleAmount   int64         `json:"last_resale_amount"`
	OwnedGiftID        string        `json:"owned_gift_id"`
	TransferStarCount  int64         `json:"transfer_star_count"`
	NextTransferDate   int64         `json:"next_transfer_date"`
}

type uniqueGiftInfoOut struct {
	Gift               *uniqueGiftOut `json:"gift,omitempty"`
	Origin             string         `json:"origin,omitempty"`
	Text               string         `json:"text,omitempty"`
	Entities           []entityOut    `json:"entities,omitempty"`
	IsPrivate          bool           `json:"is_private,omitempty"`
	LastResaleCurrency string         `json:"last_resale_currency,omitempty"`
	LastResaleAmount   int64          `json:"last_resale_amount,omitempty"`
	OwnedGiftID        string         `json:"owned_gift_id,omitempty"`
	TransferStarCount  int64          `json:"transfer_star_count,omitempty"`
	NextTransferDate   int64          `json:"next_transfer_date,omitempty"`
}

// backgroundFillOut is the union BackgroundFill; type tells which members are present. Colors and angles keep a
// zero, which is a value.
type backgroundFillOut struct {
	Type          string  `json:"type"`
	Color         *int64  `json:"color,omitempty"`
	TopColor      *int64  `json:"top_color,omitempty"`
	BottomColor   *int64  `json:"bottom_color,omitempty"`
	RotationAngle *int64  `json:"rotation_angle,omitempty"`
	Colors        []int64 `json:"colors,omitempty"`
}

// backgroundTypeOut is the union BackgroundType; type tells which members are present.
type backgroundTypeOut struct {
	Type             string             `json:"type"`
	Fill             *backgroundFillOut `json:"fill,omitempty"`
	DarkThemeDimming *int64             `json:"dark_theme_dimming,omitempty"`
	Document         *document          `json:"document,omitempty"`
	IsBlurred        bool               `json:"is_blurred,omitempty"`
	IsMoving         bool               `json:"is_moving,omitempty"`
	Intensity        *int64             `json:"intensity,omitempty"`
	IsInverted       bool               `json:"is_inverted,omitempty"`
	ThemeName        string             `json:"theme_name,omitempty"`
}

type chatBackgroundOut struct {
	Type *backgroundTypeOut `json:"type,omitempty"`
}

type tgChecklistTasksDone struct {
	ChecklistMessage       *tgMessage `json:"checklist_message"`
	MarkedAsDoneTaskIDs    []int64    `json:"marked_as_done_task_ids"`
	MarkedAsNotDoneTaskIDs []int64    `json:"marked_as_not_done_task_ids"`
}

type checklistTasksDoneOut struct {
	ChecklistMessage       *messageOut `json:"checklist_message,omitempty"`
	MarkedAsDoneTaskIDs    []int64     `json:"marked_as_done_task_ids,omitempty"`
	MarkedAsNotDoneTaskIDs []int64     `json:"marked_as_not_done_task_ids,omitempty"`
}

type tgChecklistTasksAdded struct {
	ChecklistMessage *tgMessage         `json:"checklist_message"`
	Tasks            []checklistTaskOut `json:"tasks"`
}

type checklistTasksAddedOut struct {
	ChecklistMessage *messageOut        `json:"checklist_message,omitempty"`
	Tasks            []checklistTaskOut `json:"tasks,omitempty"`
}

// communityEventOut is CommunityChatAdded and CommunityChatJoined.
type communityEventOut struct {
	Community *communityOut `json:"community,omitempty"`
}

type giveawayCreatedOut struct {
	PrizeStarCount int64 `json:"prize_star_count,omitempty"`
}

type tgGiveawayCompleted struct {
	WinnerCount         int64      `json:"winner_count"`
	UnclaimedPrizeCount int64      `json:"unclaimed_prize_count"`
	GiveawayMessage     *tgMessage `json:"giveaway_message"`
	IsStarGiveaway      bool       `json:"is_star_giveaway"`
}

type giveawayCompletedOut struct {
	WinnerCount         int64       `json:"winner_count"`
	UnclaimedPrizeCount int64       `json:"unclaimed_prize_count,omitempty"`
	GiveawayMessage     *messageOut `json:"giveaway_message,omitempty"`
	IsStarGiveaway      bool        `json:"is_star_giveaway,omitempty"`
}

// tgPollOptionChange is PollOptionAdded and PollOptionDeleted.
type tgPollOptionChange struct {
	PollMessage        *tgMessage  `json:"poll_message"`
	OptionPersistentID string      `json:"option_persistent_id"`
	OptionText         string      `json:"option_text"`
	OptionTextEntities []entityOut `json:"option_text_entities"`
}

type pollOptionChangeOut struct {
	PollMessage        any         `json:"poll_message,omitempty"`
	OptionPersistentID string      `json:"option_persistent_id"`
	OptionText         string      `json:"option_text"`
	OptionTextEntities []entityOut `json:"option_text_entities,omitempty"`
}

type starAmountOut struct {
	Amount         int64 `json:"amount"`
	NanostarAmount int64 `json:"nanostar_amount,omitempty"`
}

// tgSuggestedPost covers SuggestedPostApproved, SuggestedPostApprovalFailed, SuggestedPostDeclined,
// SuggestedPostPaid and SuggestedPostRefunded, which share their message member.
type tgSuggestedPost struct {
	SuggestedPostMessage *tgMessage             `json:"suggested_post_message"`
	Price                *suggestedPostPriceOut `json:"price"`
	SendDate             int64                  `json:"send_date"`
	Comment              string                 `json:"comment"`
	Currency             string                 `json:"currency"`
	Amount               int64                  `json:"amount"`
	StarAmount           *starAmountOut         `json:"star_amount"`
	Reason               string                 `json:"reason"`
}

type suggestedPostOut struct {
	SuggestedPostMessage *messageOut            `json:"suggested_post_message,omitempty"`
	Price                *suggestedPostPriceOut `json:"price,omitempty"`
	SendDate             int64                  `json:"send_date,omitempty"`
	Comment              string                 `json:"comment,omitempty"`
	Currency             string                 `json:"currency,omitempty"`
	Amount               int64                  `json:"amount,omitempty"`
	StarAmount           *starAmountOut         `json:"star_amount,omitempty"`
	Reason               string                 `json:"reason,omitempty"`
}

// tgServiceMore holds the service members of this file as Telegram sends them.
type tgServiceMore struct {
	SuccessfulPayment           *successfulPaymentOut  `json:"successful_payment"`
	RefundedPayment             *refundedPaymentOut    `json:"refunded_payment"`
	UsersShared                 *usersSharedOut        `json:"users_shared"`
	ChatShared                  *chatSharedOut         `json:"chat_shared"`
	Gift                        *tgGiftInfo            `json:"gift"`
	UniqueGift                  *tgUniqueGiftInfo      `json:"unique_gift"`
	GiftUpgradeSent             *tgGiftInfo            `json:"gift_upgrade_sent"`
	ChatBackgroundSet           *chatBackgroundOut     `json:"chat_background_set"`
	ChecklistTasksDone          *tgChecklistTasksDone  `json:"checklist_tasks_done"`
	ChecklistTasksAdded         *tgChecklistTasksAdded `json:"checklist_tasks_added"`
	CommunityChatAdded          *communityEventOut     `json:"community_chat_added"`
	CommunityChatJoined         *communityEventOut     `json:"community_chat_joined"`
	CommunityChatRemoved        *emptyObject           `json:"community_chat_removed"`
	GiveawayCreated             *giveawayCreatedOut    `json:"giveaway_created"`
	GiveawayCompleted           *tgGiveawayCompleted   `json:"giveaway_completed"`
	PollOptionAdded             *tgPollOptionChange    `json:"poll_option_added"`
	PollOptionDeleted           *tgPollOptionChange    `json:"poll_option_deleted"`
	SuggestedPostApproved       *tgSuggestedPost       `json:"suggested_post_approved"`
	SuggestedPostApprovalFailed *tgSuggestedPost       `json:"suggested_post_approval_failed"`
	SuggestedPostDeclined       *tgSuggestedPost       `json:"suggested_post_declined"`
	SuggestedPostPaid           *tgSuggestedPost       `json:"suggested_post_paid"`
	SuggestedPostRefunded       *tgSuggestedPost       `json:"suggested_post_refunded"`
}

type messageServiceMoreOut struct {
	SuccessfulPayment           *successfulPaymentOut   `json:"successful_payment,omitempty"`
	RefundedPayment             *refundedPaymentOut     `json:"refunded_payment,omitempty"`
	UsersShared                 *usersSharedOut         `json:"users_shared,omitempty"`
	ChatShared                  *chatSharedOut          `json:"chat_shared,omitempty"`
	Gift                        *giftInfoOut            `json:"gift,omitempty"`
	UniqueGift                  *uniqueGiftInfoOut      `json:"unique_gift,omitempty"`
	GiftUpgradeSent             *giftInfoOut            `json:"gift_upgrade_sent,omitempty"`
	ChatBackgroundSet           *chatBackgroundOut      `json:"chat_background_set,omitempty"`
	ChecklistTasksDone          *checklistTasksDoneOut  `json:"checklist_tasks_done,omitempty"`
	ChecklistTasksAdded         *checklistTasksAddedOut `json:"checklist_tasks_added,omitempty"`
	CommunityChatAdded          *communityEventOut      `json:"community_chat_added,omitempty"`
	CommunityChatJoined         *communityEventOut      `json:"community_chat_joined,omitempty"`
	CommunityChatRemoved        *emptyObject            `json:"community_chat_removed,omitempty"`
	GiveawayCreated             *giveawayCreatedOut     `json:"giveaway_created,omitempty"`
	GiveawayCompleted           *giveawayCompletedOut   `json:"giveaway_completed,omitempty"`
	PollOptionAdded             *pollOptionChangeOut    `json:"poll_option_added,omitempty"`
	PollOptionDeleted           *pollOptionChangeOut    `json:"poll_option_deleted,omitempty"`
	SuggestedPostApproved       *suggestedPostOut       `json:"suggested_post_approved,omitempty"`
	SuggestedPostApprovalFailed *suggestedPostOut       `json:"suggested_post_approval_failed,omitempty"`
	SuggestedPostDeclined       *suggestedPostOut       `json:"suggested_post_declined,omitempty"`
	SuggestedPostPaid           *suggestedPostOut       `json:"suggested_post_paid,omitempty"`
	SuggestedPostRefunded       *suggestedPostOut       `json:"suggested_post_refunded,omitempty"`
}

// cleanAddress prints an address as untrusted, bounded text; it is never fetched or resolved.
func cleanAddress(a *shippingAddressOut) *shippingAddressOut {
	if a == nil {
		return nil
	}
	out := &shippingAddressOut{CountryCode: txtName(a.CountryCode), State: txtName(a.State), City: txtName(a.City),
		StreetLine1: txtName(a.StreetLine1), StreetLine2: txtName(a.StreetLine2), PostCode: txtName(a.PostCode)}
	if *out == (shippingAddressOut{}) {
		return nil
	}
	return out
}

func cleanOrderInfo(o *orderInfoOut) *orderInfoOut {
	if o == nil {
		return nil
	}
	out := &orderInfoOut{Name: txtName(o.Name), PhoneNumber: txtName(o.PhoneNumber), Email: txtName(o.Email),
		ShippingAddress: cleanAddress(o.ShippingAddress)}
	if *out == (orderInfoOut{}) {
		return nil
	}
	return out
}

func cleanIDs(list []int64) []int64 {
	if len(list) == 0 {
		return nil
	}
	return list[:min(len(list), maxNestedList)]
}

func (s messageScope) checklistTasks(list []checklistTaskOut) []checklistTaskOut {
	var out []checklistTaskOut
	for _, t := range list {
		out = append(out, checklistTaskOut{ID: t.ID, Text: txtBody(t.Text), TextEntities: entities(t.TextEntities),
			CompletedByUser: headUser(t.CompletedByUser), CompletedByChat: s.headChat(t.CompletedByChat),
			CompletionDate: t.CompletionDate})
	}
	return out
}

func (s messageScope) sharedPhotos(list []photoSize) []photoSize {
	return s.refs.photoSizes(list[:min(len(list), maxNestedList)])
}

func (s messageScope) giftInfo(g *tgGiftInfo, name string, omitted *[]string) *giftInfoOut {
	if g == nil {
		return nil
	}
	detail := s.refs.gift(g.Gift, name+".gift.", omitted)
	return bounded(omitted, name, &giftInfoOut{Gift: &detail, OwnedGiftID: txtName(g.OwnedGiftID),
		ConvertStarCount: g.ConvertStarCount, PrepaidUpgradeStarCount: g.PrepaidUpgradeStarCount,
		IsUpgradeSeparate: g.IsUpgradeSeparate, CanBeUpgraded: g.CanBeUpgraded, Text: txtBody(g.Text),
		Entities: entities(g.Entities), IsPrivate: g.IsPrivate, UniqueGiftNumber: g.UniqueGiftNumber})
}

func (s messageScope) background(b *chatBackgroundOut, omitted *[]string) *chatBackgroundOut {
	if b == nil {
		return nil
	}
	out := &chatBackgroundOut{}
	if t := b.Type; t != nil {
		var inner []string
		typ := *t
		typ.Type, typ.ThemeName = txtName(t.Type), txtName(t.ThemeName)
		if f := t.Fill; f != nil {
			fill := *f
			fill.Type = txtName(f.Type)
			fill.Colors = cleanIDs(f.Colors)
			typ.Fill = &fill
		}
		if t.Document != nil {
			d := s.refs.document(*t.Document, &inner)
			typ.Document = &d
		}
		scoped(omitted, "chat_background_set", inner)
		out.Type = &typ
	}
	return bounded(omitted, "chat_background_set", out)
}

// messageServiceMore maps the service members of m that messageservice.go does not. A member over the nesting
// limits is left out and named in omitted; a nested message is mapped one level.
func (c *Client) messageServiceMore(m *tgMessage, s messageScope, nested bool) (out messageServiceMoreOut, omitted []string) {
	if p := m.SuccessfulPayment; p != nil {
		out.SuccessfulPayment = bounded(&omitted, "successful_payment", &successfulPaymentOut{Currency: txtName(p.Currency),
			TotalAmount: p.TotalAmount, InvoicePayload: txtName(p.InvoicePayload),
			SubscriptionExpirationDate: p.SubscriptionExpirationDate, IsRecurring: p.IsRecurring,
			IsFirstRecurring: p.IsFirstRecurring, ShippingOptionID: txtName(p.ShippingOptionID),
			OrderInfo:               cleanOrderInfo(p.OrderInfo),
			TelegramPaymentChargeID: txtName(p.TelegramPaymentChargeID), ProviderPaymentChargeID: txtName(p.ProviderPaymentChargeID)})
	}
	if p := m.RefundedPayment; p != nil {
		out.RefundedPayment = bounded(&omitted, "refunded_payment", &refundedPaymentOut{Currency: txtName(p.Currency),
			TotalAmount: p.TotalAmount, InvoicePayload: txtName(p.InvoicePayload),
			TelegramPaymentChargeID: txtName(p.TelegramPaymentChargeID), ProviderPaymentChargeID: txtName(p.ProviderPaymentChargeID)})
	}
	if u := m.UsersShared; u != nil {
		shared := &usersSharedOut{RequestID: u.RequestID}
		for _, user := range u.Users[:min(len(u.Users), maxNestedList)] {
			shared.Users = append(shared.Users, sharedUserOut{UserID: user.UserID, FirstName: txtName(user.FirstName),
				LastName: txtName(user.LastName), Username: txtName(user.Username), Photo: s.sharedPhotos(user.Photo)})
		}
		out.UsersShared = bounded(&omitted, "users_shared", shared)
	}
	if k := m.ChatShared; k != nil {
		out.ChatShared = bounded(&omitted, "chat_shared", &chatSharedOut{RequestID: k.RequestID, ChatID: k.ChatID,
			Title: txtName(k.Title), Username: txtName(k.Username), Photo: s.sharedPhotos(k.Photo)})
	}
	out.Gift = s.giftInfo(m.Gift, "gift", &omitted)
	out.GiftUpgradeSent = s.giftInfo(m.GiftUpgradeSent, "gift_upgrade_sent", &omitted)
	if g := m.UniqueGift; g != nil {
		detail := s.refs.uniqueGift(g.Gift, "unique_gift.gift.", &omitted)
		out.UniqueGift = bounded(&omitted, "unique_gift", &uniqueGiftInfoOut{Gift: &detail, Origin: txtName(g.Origin),
			Text: txtBody(g.Text), Entities: entities(g.Entities), IsPrivate: g.IsPrivate,
			LastResaleCurrency: txtName(g.LastResaleCurrency), LastResaleAmount: g.LastResaleAmount,
			OwnedGiftID: txtName(g.OwnedGiftID), TransferStarCount: g.TransferStarCount, NextTransferDate: g.NextTransferDate})
	}
	out.ChatBackgroundSet = s.background(m.ChatBackgroundSet, &omitted)
	if d := m.ChecklistTasksDone; d != nil {
		out.ChecklistTasksDone = bounded(&omitted, "checklist_tasks_done", &checklistTasksDoneOut{
			ChecklistMessage:    c.nestedService(d.ChecklistMessage, s, nested, "checklist_tasks_done.checklist_message", &omitted),
			MarkedAsDoneTaskIDs: cleanIDs(d.MarkedAsDoneTaskIDs), MarkedAsNotDoneTaskIDs: cleanIDs(d.MarkedAsNotDoneTaskIDs)})
	}
	if a := m.ChecklistTasksAdded; a != nil {
		out.ChecklistTasksAdded = bounded(&omitted, "checklist_tasks_added", &checklistTasksAddedOut{
			ChecklistMessage: c.nestedService(a.ChecklistMessage, s, nested, "checklist_tasks_added.checklist_message", &omitted),
			Tasks:            s.checklistTasks(a.Tasks)})
	}
	for _, e := range []struct {
		in  *communityEventOut
		out **communityEventOut
	}{{m.CommunityChatAdded, &out.CommunityChatAdded}, {m.CommunityChatJoined, &out.CommunityChatJoined}} {
		if e.in != nil {
			event := &communityEventOut{}
			if k := e.in.Community; k != nil && k.ID != 0 {
				event.Community = &communityOut{ID: k.ID, Name: txtName(k.Name)}
			}
			*e.out = event
		}
	}
	out.CommunityChatRemoved = emptyOf(m.CommunityChatRemoved)
	out.GiveawayCreated = m.GiveawayCreated
	if g := m.GiveawayCompleted; g != nil {
		out.GiveawayCompleted = bounded(&omitted, "giveaway_completed", &giveawayCompletedOut{WinnerCount: g.WinnerCount,
			UnclaimedPrizeCount: g.UnclaimedPrizeCount, IsStarGiveaway: g.IsStarGiveaway,
			GiveawayMessage: c.nestedService(g.GiveawayMessage, s, nested, "giveaway_completed.giveaway_message", &omitted)})
	}
	option := func(o *tgPollOptionChange, name string) *pollOptionChangeOut {
		if o == nil {
			return nil
		}
		return bounded(&omitted, name, &pollOptionChangeOut{
			PollMessage:        c.maybeInaccessible(o.PollMessage, s, nested, name+".poll_message", &omitted),
			OptionPersistentID: txtName(o.OptionPersistentID), OptionText: txtBody(o.OptionText),
			OptionTextEntities: entities(o.OptionTextEntities)})
	}
	out.PollOptionAdded = option(m.PollOptionAdded, "poll_option_added")
	out.PollOptionDeleted = option(m.PollOptionDeleted, "poll_option_deleted")
	post := func(p *tgSuggestedPost, name string) *suggestedPostOut {
		if p == nil {
			return nil
		}
		o := &suggestedPostOut{SendDate: p.SendDate, Comment: txtBody(p.Comment), Currency: txtName(p.Currency),
			Amount: p.Amount, StarAmount: p.StarAmount, Reason: txtName(p.Reason),
			SuggestedPostMessage: c.nestedService(p.SuggestedPostMessage, s, nested, name+".suggested_post_message", &omitted)}
		if p.Price != nil {
			o.Price = &suggestedPostPriceOut{Currency: txtName(p.Price.Currency), Amount: p.Price.Amount}
		}
		return bounded(&omitted, name, o)
	}
	out.SuggestedPostApproved = post(m.SuggestedPostApproved, "suggested_post_approved")
	out.SuggestedPostApprovalFailed = post(m.SuggestedPostApprovalFailed, "suggested_post_approval_failed")
	out.SuggestedPostDeclined = post(m.SuggestedPostDeclined, "suggested_post_declined")
	out.SuggestedPostPaid = post(m.SuggestedPostPaid, "suggested_post_paid")
	out.SuggestedPostRefunded = post(m.SuggestedPostRefunded, "suggested_post_refunded")
	return out, omitted
}

var serviceEventSchema = []schemaProperty{
	{"successful_payment", nestedSchema("Telegram Bot API SuccessfulPayment with order_info; the charge identifiers are printed")},
	{"refunded_payment", nestedSchema("Telegram Bot API RefundedPayment")},
	{"users_shared", nestedSchema("Telegram Bot API UsersShared: request_id, users (user_id, first_name, last_name, username, photo); data only")},
	{"chat_shared", nestedSchema("Telegram Bot API ChatShared: request_id, chat_id, title, username, photo; data only, never a target")},
	{"gift", nestedSchema("Telegram Bot API GiftInfo with owned_gift_id; every file_id is a file_ref")},
	{"unique_gift", nestedSchema("Telegram Bot API UniqueGiftInfo with owned_gift_id; every file_id is a file_ref")},
	{"gift_upgrade_sent", nestedSchema("Telegram Bot API GiftInfo of the upgrade; every file_id is a file_ref")},
	{"chat_background_set", nestedSchema("Telegram Bot API ChatBackground: type; every file_id is a file_ref")},
	{"checklist_tasks_done", nestedSchema("Telegram Bot API ChecklistTasksDone: checklist_message (one level), marked_as_done_task_ids, marked_as_not_done_task_ids")},
	{"checklist_tasks_added", nestedSchema("Telegram Bot API ChecklistTasksAdded: checklist_message (one level), tasks")},
	{"community_chat_added", nestedSchema("Telegram Bot API CommunityChatAdded: community")},
	{"community_chat_joined", nestedSchema("Telegram Bot API CommunityChatJoined: community")},
	{"community_chat_removed", nestedSchema("Telegram Bot API CommunityChatRemoved, an empty object")},
	{"giveaway_created", nestedSchema("Telegram Bot API GiveawayCreated: prize_star_count")},
	{"giveaway_completed", nestedSchema("Telegram Bot API GiveawayCompleted: winner_count, unclaimed_prize_count, giveaway_message (one level), is_star_giveaway")},
	{"poll_option_added", nestedSchema("Telegram Bot API PollOptionAdded: poll_message (one level), option_persistent_id, option_text, option_text_entities")},
	{"poll_option_deleted", nestedSchema("Telegram Bot API PollOptionDeleted: poll_message (one level), option_persistent_id, option_text, option_text_entities")},
	{"suggested_post_approved", nestedSchema("Telegram Bot API SuggestedPostApproved: suggested_post_message (one level), price, send_date")},
	{"suggested_post_approval_failed", nestedSchema("Telegram Bot API SuggestedPostApprovalFailed: suggested_post_message (one level), price")},
	{"suggested_post_declined", nestedSchema("Telegram Bot API SuggestedPostDeclined: suggested_post_message (one level), comment")},
	{"suggested_post_paid", nestedSchema("Telegram Bot API SuggestedPostPaid: suggested_post_message (one level), currency, amount, star_amount")},
	{"suggested_post_refunded", nestedSchema("Telegram Bot API SuggestedPostRefunded: suggested_post_message (one level), reason")},
}

const serviceEventText = ", successful_payment, refunded_payment, users_shared, chat_shared, gift, unique_gift, " +
	"gift_upgrade_sent, chat_background_set, checklist_tasks_done, checklist_tasks_added, community_chat_added, " +
	"community_chat_joined, community_chat_removed, giveaway_created, giveaway_completed, poll_option_added, " +
	"poll_option_deleted, suggested_post_approved, suggested_post_approval_failed, suggested_post_declined, " +
	"suggested_post_paid and suggested_post_refunded"
