package telegram

// This file is the only mapping of the Bot API TransactionPartner types, AffiliateInfo, and
// RevenueWithdrawalState. A partner is outside the target binding: users and chats appear with their numeric
// identifier only, and every file is bound to the bot.

// idRef is a user or chat that is named by its numeric identifier only.
type idRef struct {
	ID int64 `json:"id"`
}

type tgAffiliate struct {
	AffiliateUser      *idRef `json:"affiliate_user"`
	AffiliateChat      *idRef `json:"affiliate_chat"`
	CommissionPerMille *int64 `json:"commission_per_mille"`
	Amount             *int64 `json:"amount"`
	NanostarAmount     int64  `json:"nanostar_amount"`
}

type affiliateOut struct {
	AffiliateUser      *idRef `json:"affiliate_user,omitempty"`
	AffiliateChat      *idRef `json:"affiliate_chat,omitempty"`
	CommissionPerMille *int64 `json:"commission_per_mille,omitempty"`
	Amount             *int64 `json:"amount,omitempty"`
	NanostarAmount     int64  `json:"nanostar_amount,omitempty"`
}

type withdrawalStateOut struct {
	Type string `json:"type"`
	Date int64  `json:"date,omitempty"`
	URL  string `json:"url,omitempty"`
}

type tgPartner struct {
	Type string `json:"type"`
	User *struct {
		ID int64 `json:"id"`
	} `json:"user"`
	Chat *struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	TransactionType             string              `json:"transaction_type"`
	Affiliate                   *tgAffiliate        `json:"affiliate"`
	InvoicePayload              string              `json:"invoice_payload"`
	SubscriptionPeriod          int64               `json:"subscription_period"`
	PaidMedia                   []paidMediaOut      `json:"paid_media"`
	PaidMediaPayload            string              `json:"paid_media_payload"`
	Gift                        *tgGift             `json:"gift"`
	PremiumSubscriptionDuration int64               `json:"premium_subscription_duration"`
	SponsorUser                 *idRef              `json:"sponsor_user"`
	CommissionPerMille          *int64              `json:"commission_per_mille"`
	WithdrawalState             *withdrawalStateOut `json:"withdrawal_state"`
	RequestCount                *int64              `json:"request_count"`
}

type partnerOut struct {
	Type                        string              `json:"type"`
	ID                          int64               `json:"id,omitempty"`
	TransactionType             string              `json:"transaction_type,omitempty"`
	Affiliate                   *affiliateOut       `json:"affiliate,omitempty"`
	InvoicePayload              string              `json:"invoice_payload,omitempty"`
	SubscriptionPeriod          int64               `json:"subscription_period,omitempty"`
	PaidMedia                   []paidMediaOut      `json:"paid_media,omitempty"`
	PaidMediaPayload            string              `json:"paid_media_payload,omitempty"`
	Gift                        *giftDetailOut      `json:"gift,omitempty"`
	PremiumSubscriptionDuration int64               `json:"premium_subscription_duration,omitempty"`
	SponsorUser                 *idRef              `json:"sponsor_user,omitempty"`
	CommissionPerMille          *int64              `json:"commission_per_mille,omitempty"`
	WithdrawalState             *withdrawalStateOut `json:"withdrawal_state,omitempty"`
	RequestCount                *int64              `json:"request_count,omitempty"`
	OmittedFields               []string            `json:"omitted_fields,omitempty"`
}

// partner maps one TransactionPartner. The flat id of a user or chat stays; the payloads are the bot's own data
// and the withdrawal url is an address that is printed and never fetched. Members over the nesting limits are
// left out and named in omitted_fields.
func (r fileRefFunc) partner(p *tgPartner) *partnerOut {
	if p == nil || p.Type == "" {
		return nil
	}
	out := &partnerOut{Type: capText(p.Type, maxNameRunes)}
	switch {
	case p.Type == "user" && p.User != nil:
		out.ID = p.User.ID
	case p.Type == "chat" && p.Chat != nil:
		out.ID = p.Chat.ID
	}
	var omitted []string
	out.TransactionType = txtName(p.TransactionType)
	out.InvoicePayload = txtName(p.InvoicePayload)
	out.SubscriptionPeriod = p.SubscriptionPeriod
	out.PaidMediaPayload = txtName(p.PaidMediaPayload)
	out.PremiumSubscriptionDuration = p.PremiumSubscriptionDuration
	out.SponsorUser = p.SponsorUser
	out.CommissionPerMille = p.CommissionPerMille
	out.RequestCount = p.RequestCount
	if a := p.Affiliate; a != nil {
		out.Affiliate = &affiliateOut{AffiliateUser: a.AffiliateUser, AffiliateChat: a.AffiliateChat,
			CommissionPerMille: a.CommissionPerMille, Amount: a.Amount, NanostarAmount: a.NanostarAmount}
	}
	if len(p.PaidMedia) > 0 {
		var inner []string
		items := r.paidMediaItems(p.PaidMedia, &inner)
		scoped(&omitted, "paid_media", inner)
		out.PaidMedia = bounded(&omitted, "paid_media", items)
	}
	if p.Gift != nil {
		out.Gift = bounded(&omitted, "gift", ptr(r.gift(*p.Gift, "gift.", &omitted)))
	}
	if w := p.WithdrawalState; w != nil {
		out.WithdrawalState = &withdrawalStateOut{Type: txtName(w.Type), Date: w.Date, URL: txtLink(w.URL)}
	}
	out.OmittedFields = omitted
	return out
}

const (
	partnerIDSchema = `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],` +
		`"additionalProperties":false}`
	partnerSchema = `{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"integer"},` +
		`"transaction_type":{"type":"string"},"affiliate":{"type":"object","properties":{"affiliate_user":` +
		partnerIDSchema + `,"affiliate_chat":` + partnerIDSchema + `,"commission_per_mille":{"type":"integer"},` +
		`"amount":{"type":"integer"},"nanostar_amount":{"type":"integer"}},"additionalProperties":false},` +
		`"invoice_payload":{"type":"string"},"subscription_period":{"type":"integer"},` +
		`"paid_media":{"type":"array","items":{"type":"object"}},"paid_media_payload":{"type":"string"},` +
		`"gift":` + giftDetailsSchema + `,"premium_subscription_duration":{"type":"integer"},` +
		`"sponsor_user":` + partnerIDSchema + `,"commission_per_mille":{"type":"integer"},` +
		`"withdrawal_state":{"type":"object","properties":{"type":{"type":"string"},"date":{"type":"integer"},` +
		`"url":{"type":"string"}},"required":["type"],"additionalProperties":false},` +
		`"request_count":{"type":"integer"},"omitted_fields":{"type":"array","items":{"type":"string"}}},` +
		`"required":["type"],"additionalProperties":false}`
	partnerText = "type, id of a user or chat, transaction_type, invoice_payload and paid_media_payload (untrusted " +
		"text), subscription_period, paid_media (PaidMedia with file_ref bound to the bot), gift, " +
		"premium_subscription_duration, affiliate (affiliate_user and affiliate_chat with id only, " +
		"commission_per_mille, amount, nanostar_amount), sponsor_user (id only), commission_per_mille, " +
		"withdrawal_state (type, date, url as an address that is never fetched), request_count, and omitted_fields"
)
