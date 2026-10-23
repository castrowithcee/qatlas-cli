package telegram

import (
	"cmp"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// This file is the only mapping of the Bot API types Gift and UniqueGift and of the owned gifts that carry them.
// Every sticker goes through the media mapper, so a gift sticker has a file_ref or none, never a file_id.

const (
	giftsResponseMax  = 512 << 10
	maxGiftsEntities  = 100
	maxEntityURLRunes = 2048
	maxAvailableGifts = 256
	giftDetailsName   = "gift_details"
)

// giftKindFilters are the optional gift filters every gifts tool offers. A nil value is not sent, a false one is
// sent as false, so Telegram's own defaults stay in force.
type giftKindFilters struct {
	ExcludeUnlimited            *bool `json:"exclude_unlimited,omitempty"`
	ExcludeLimitedUpgradable    *bool `json:"exclude_limited_upgradable,omitempty"`
	ExcludeLimitedNonUpgradable *bool `json:"exclude_limited_non_upgradable,omitempty"`
	ExcludeUnique               *bool `json:"exclude_unique,omitempty"`
	ExcludeFromBlockchain       *bool `json:"exclude_from_blockchain,omitempty"`
	SortByPrice                 *bool `json:"sort_by_price,omitempty"`
}

// giftSavedFilters exist only where Telegram accepts them: not for the gifts of a user.
type giftSavedFilters struct {
	ExcludeUnsaved *bool `json:"exclude_unsaved,omitempty"`
	ExcludeSaved   *bool `json:"exclude_saved,omitempty"`
}

const (
	giftKindFilterSchema = `"exclude_unlimited":{"type":"boolean"},"exclude_limited_upgradable":{"type":"boolean"},` +
		`"exclude_limited_non_upgradable":{"type":"boolean"},"exclude_unique":{"type":"boolean"},` +
		`"exclude_from_blockchain":{"type":"boolean"},"sort_by_price":{"type":"boolean"}`
	giftSavedFilterSchema = `"exclude_unsaved":{"type":"boolean"},"exclude_saved":{"type":"boolean"}`
)

const giftFilterNote = "; sent to Telegram unchanged, and every later page needs the same filter values"

var (
	giftKindFilterArguments = []capability.Argument{
		{Name: "exclude_unlimited", Description: "Exclude gifts that can be bought an unlimited number of times" + giftFilterNote},
		{Name: "exclude_limited_upgradable", Description: "Exclude limited gifts that can be upgraded to unique" + giftFilterNote},
		{Name: "exclude_limited_non_upgradable", Description: "Exclude limited gifts that cannot be upgraded" + giftFilterNote},
		{Name: "exclude_unique", Description: "Exclude unique gifts" + giftFilterNote},
		{Name: "exclude_from_blockchain", Description: "Exclude gifts assigned from the TON blockchain" + giftFilterNote},
		{Name: "sort_by_price", Description: "Sort by gift price instead of send date, before paging" + giftFilterNote},
	}
	giftSavedFilterArguments = []capability.Argument{
		{Name: "exclude_unsaved", Description: "Exclude gifts not shown on the profile page" + giftFilterNote},
		{Name: "exclude_saved", Description: "Exclude gifts shown on the profile page" + giftFilterNote},
	}
)

// giftOffsetText is the offset argument of every paged gifts tool.
const giftOffsetText = "next_offset of a previous answer of this tool, passed back unchanged with the same filter values, " +
	"up to 256 characters"

type giftBackgroundOut struct {
	CenterColor *int64 `json:"center_color,omitempty"`
	EdgeColor   *int64 `json:"edge_color,omitempty"`
	TextColor   *int64 `json:"text_color,omitempty"`
}

// giftTraits are the members of Gift that the catalog and the gift_details of a regular owned gift share.
// Counts of limited gifts are pointers because a sold-out gift has a real zero.
type giftTraits struct {
	Sticker                *sticker           `json:"sticker,omitempty"`
	UpgradeStarCount       int64              `json:"upgrade_star_count,omitempty"`
	IsPremium              bool               `json:"is_premium,omitempty"`
	HasColors              bool               `json:"has_colors,omitempty"`
	PersonalTotalCount     *int64             `json:"personal_total_count,omitempty"`
	PersonalRemainingCount *int64             `json:"personal_remaining_count,omitempty"`
	Background             *giftBackgroundOut `json:"background,omitempty"`
	UniqueGiftVariantCount int64              `json:"unique_gift_variant_count,omitempty"`
	PublisherChat          *chatRefOut        `json:"publisher_chat,omitempty"`
}

// tgGift is the decoded Bot API Gift.
type tgGift struct {
	ID             string `json:"id"`
	StarCount      int64  `json:"star_count"`
	TotalCount     *int64 `json:"total_count"`
	RemainingCount *int64 `json:"remaining_count"`
	giftTraits
}

// giftDetailOut is the printed Gift of an owned gift.
type giftDetailOut struct {
	ID             string `json:"id,omitempty"`
	StarCount      int64  `json:"star_count,omitempty"`
	TotalCount     *int64 `json:"total_count,omitempty"`
	RemainingCount *int64 `json:"remaining_count,omitempty"`
	giftTraits
}

// availableGiftOut is one catalog entry; its flat members keep their former names and zero handling.
type availableGiftOut struct {
	GiftID         string `json:"gift_id"`
	StarCount      int64  `json:"star_count"`
	TotalCount     int64  `json:"total_count,omitempty"`
	RemainingCount int64  `json:"remaining_count,omitempty"`
	giftTraits
	OmittedFields []string `json:"omitted_fields,omitempty"`
}

type uniqueGiftModelOut struct {
	Name           string   `json:"name,omitempty"`
	Sticker        *sticker `json:"sticker,omitempty"`
	RarityPerMille *int64   `json:"rarity_per_mille,omitempty"`
	Rarity         string   `json:"rarity,omitempty"`
}

type uniqueGiftSymbolOut struct {
	Name           string   `json:"name,omitempty"`
	Sticker        *sticker `json:"sticker,omitempty"`
	RarityPerMille *int64   `json:"rarity_per_mille,omitempty"`
}

type uniqueGiftBackdropColorsOut struct {
	CenterColor *int64 `json:"center_color,omitempty"`
	EdgeColor   *int64 `json:"edge_color,omitempty"`
	SymbolColor *int64 `json:"symbol_color,omitempty"`
	TextColor   *int64 `json:"text_color,omitempty"`
}

type uniqueGiftBackdropOut struct {
	Name           string                       `json:"name,omitempty"`
	Colors         *uniqueGiftBackdropColorsOut `json:"colors,omitempty"`
	RarityPerMille *int64                       `json:"rarity_per_mille,omitempty"`
}

// uniqueGiftOut is the Bot API UniqueGift, decoded and printed with the same names.
type uniqueGiftOut struct {
	GiftID           string                 `json:"gift_id,omitempty"`
	BaseName         string                 `json:"base_name,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Number           int64                  `json:"number,omitempty"`
	Model            *uniqueGiftModelOut    `json:"model,omitempty"`
	Symbol           *uniqueGiftSymbolOut   `json:"symbol,omitempty"`
	Backdrop         *uniqueGiftBackdropOut `json:"backdrop,omitempty"`
	IsPremium        bool                   `json:"is_premium,omitempty"`
	IsBurned         bool                   `json:"is_burned,omitempty"`
	IsFromBlockchain bool                   `json:"is_from_blockchain,omitempty"`
	Colors           *uniqueGiftColorsOut   `json:"colors,omitempty"`
	PublisherChat    *chatRefOut            `json:"publisher_chat,omitempty"`
}

type tgOwnedGift struct {
	Type                    string          `json:"type"`
	OwnedGiftID             string          `json:"owned_gift_id"`
	SendDate                int64           `json:"send_date"`
	IsSaved                 bool            `json:"is_saved"`
	Text                    string          `json:"text"`
	Entities                []entityOut     `json:"entities"`
	IsPrivate               bool            `json:"is_private"`
	CanBeUpgraded           bool            `json:"can_be_upgraded"`
	WasRefunded             bool            `json:"was_refunded"`
	SenderUser              *tgUser         `json:"sender_user"`
	ConvertStarCount        int64           `json:"convert_star_count"`
	PrepaidUpgradeStarCount int64           `json:"prepaid_upgrade_star_count"`
	IsUpgradeSeparate       bool            `json:"is_upgrade_separate"`
	UniqueGiftNumber        int64           `json:"unique_gift_number"`
	CanBeTransferred        bool            `json:"can_be_transferred"`
	TransferStarCount       int64           `json:"transfer_star_count"`
	NextTransferDate        int64           `json:"next_transfer_date"`
	Gift                    json.RawMessage `json:"gift"`
}

// giftOut keeps the former flat members and adds the documented ones. gift_details is the whole Gift of a
// regular gift or the whole UniqueGift of a unique one.
type giftOut struct {
	Type                    string      `json:"type"`
	GiftID                  string      `json:"gift_id,omitempty"`
	Name                    string      `json:"name,omitempty"`
	Number                  int64       `json:"number,omitempty"`
	SendDate                int64       `json:"send_date,omitempty"`
	IsSaved                 bool        `json:"is_saved"`
	Text                    string      `json:"text,omitempty"`
	Sender                  *senderOut  `json:"sender,omitempty"`
	StarCount               int64       `json:"star_count,omitempty"`
	ConvertStarCount        int64       `json:"convert_star_count,omitempty"`
	OwnedGiftID             string      `json:"owned_gift_id,omitempty"`
	Entities                []entityOut `json:"entities,omitempty"`
	IsPrivate               bool        `json:"is_private,omitempty"`
	CanBeUpgraded           bool        `json:"can_be_upgraded,omitempty"`
	WasRefunded             bool        `json:"was_refunded,omitempty"`
	PrepaidUpgradeStarCount int64       `json:"prepaid_upgrade_star_count,omitempty"`
	IsUpgradeSeparate       bool        `json:"is_upgrade_separate,omitempty"`
	UniqueGiftNumber        int64       `json:"unique_gift_number,omitempty"`
	CanBeTransferred        bool        `json:"can_be_transferred,omitempty"`
	TransferStarCount       int64       `json:"transfer_star_count,omitempty"`
	NextTransferDate        int64       `json:"next_transfer_date,omitempty"`
	GiftDetails             any         `json:"gift_details,omitempty"`
	OmittedFields           []string    `json:"omitted_fields,omitempty"`
}

type giftsOut struct {
	TotalCount int64     `json:"total_count"`
	NextOffset string    `json:"next_offset,omitempty"`
	Gifts      []giftOut `json:"gifts"`
}

// giftSticker maps a gift sticker through the media mapper. Members of the sticker over the bounds are named
// in omitted under path, and a sticker that is still too large is left out whole.
func (r fileRefFunc) giftSticker(s *sticker, path string, omitted *[]string) *sticker {
	if s == nil {
		return nil
	}
	var inner []string
	mapped := r.sticker(*s, &inner)
	for _, name := range inner {
		*omitted = append(*omitted, path+strings.TrimPrefix(name, "media_items.sticker"))
	}
	return bounded(omitted, path, &mapped)
}

func (r fileRefFunc) traits(in giftTraits, prefix string, omitted *[]string) giftTraits {
	out := in
	out.Sticker = r.giftSticker(in.Sticker, prefix+"sticker", omitted)
	out.PublisherChat = bounded(omitted, prefix+"publisher_chat", cleanChatRef(in.PublisherChat))
	return out
}

func (r fileRefFunc) gift(g tgGift, prefix string, omitted *[]string) giftDetailOut {
	return giftDetailOut{ID: capText(g.ID, maxNameRunes), StarCount: g.StarCount, TotalCount: g.TotalCount,
		RemainingCount: g.RemainingCount, giftTraits: r.traits(g.giftTraits, prefix, omitted)}
}

func (r fileRefFunc) uniqueGift(g uniqueGiftOut, prefix string, omitted *[]string) uniqueGiftOut {
	out := uniqueGiftOut{GiftID: capText(g.GiftID, maxNameRunes), BaseName: cleanText(g.BaseName, maxNameRunes),
		Name: cleanText(g.Name, maxNameRunes), Number: g.Number, IsPremium: g.IsPremium, IsBurned: g.IsBurned,
		IsFromBlockchain: g.IsFromBlockchain}
	if m := g.Model; m != nil {
		out.Model = bounded(omitted, prefix+"model", &uniqueGiftModelOut{Name: cleanText(m.Name, maxNameRunes),
			Sticker: r.giftSticker(m.Sticker, prefix+"model.sticker", omitted), RarityPerMille: m.RarityPerMille,
			Rarity: cleanText(m.Rarity, maxNameRunes)})
	}
	if s := g.Symbol; s != nil {
		out.Symbol = bounded(omitted, prefix+"symbol", &uniqueGiftSymbolOut{Name: cleanText(s.Name, maxNameRunes),
			Sticker: r.giftSticker(s.Sticker, prefix+"symbol.sticker", omitted), RarityPerMille: s.RarityPerMille})
	}
	if b := g.Backdrop; b != nil {
		out.Backdrop = bounded(omitted, prefix+"backdrop", &uniqueGiftBackdropOut{Name: cleanText(b.Name, maxNameRunes),
			Colors: b.Colors, RarityPerMille: b.RarityPerMille})
	}
	out.Colors = bounded(omitted, prefix+"colors", cleanUniqueGiftColors(g.Colors))
	out.PublisherChat = bounded(omitted, prefix+"publisher_chat", cleanChatRef(g.PublisherChat))
	return out
}

func cleanUniqueGiftColors(c *uniqueGiftColorsOut) *uniqueGiftColorsOut {
	if c == nil {
		return nil
	}
	return &uniqueGiftColorsOut{
		ModelCustomEmojiID:  cleanText(c.ModelCustomEmojiID, maxNameRunes),
		SymbolCustomEmojiID: cleanText(c.SymbolCustomEmojiID, maxNameRunes),
		LightThemeMainColor: c.LightThemeMainColor, LightThemeOtherColor: c.LightThemeOtherColor,
		DarkThemeMainColor: c.DarkThemeMainColor, DarkThemeOtherColor: c.DarkThemeOtherColor}
}

func cleanEntities(in []entityOut) []entityOut {
	if len(in) == 0 {
		return nil
	}
	out := make([]entityOut, 0, min(len(in), maxGiftsEntities+1))
	for _, e := range in {
		if len(out) > maxGiftsEntities {
			break
		}
		out = append(out, entityOut{Type: cleanText(e.Type, maxNameRunes), Offset: e.Offset, Length: e.Length,
			URL: cleanText(e.URL, maxEntityURLRunes), User: sender(e.User), Language: cleanText(e.Language, maxNameRunes),
			CustomEmojiID: cleanText(e.CustomEmojiID, maxNameRunes), UnixTime: e.UnixTime,
			DateTimeFormat: cleanText(e.DateTimeFormat, maxNameRunes)})
	}
	return out
}

// ownedGift maps one OwnedGift. The flat gift members keep their former reading of the nested gift for every
// type; gift_details is added for the two documented types.
func (r fileRefFunc) ownedGift(g tgOwnedGift) (giftOut, bool) {
	var flat struct {
		ID        string `json:"id"`
		GiftID    string `json:"gift_id"`
		BaseName  string `json:"base_name"`
		Number    int64  `json:"number"`
		StarCount int64  `json:"star_count"`
	}
	if len(g.Gift) > 0 && json.Unmarshal(g.Gift, &flat) != nil {
		return giftOut{}, false
	}
	var omitted []string
	item := giftOut{Type: capText(g.Type, maxNameRunes), SendDate: g.SendDate, IsSaved: g.IsSaved,
		Text: capText(g.Text, maxNameRunes), Sender: sender(g.SenderUser), ConvertStarCount: g.ConvertStarCount,
		GiftID: capText(cmp.Or(flat.ID, flat.GiftID), maxNameRunes), Name: capText(flat.BaseName, maxNameRunes),
		Number: flat.Number, StarCount: flat.StarCount,
		OwnedGiftID: capText(g.OwnedGiftID, maxNameRunes)}
	const prefix = giftDetailsName + "."
	var details any
	switch g.Type {
	case "regular":
		item.Entities = bounded(&omitted, "entities", cleanEntities(g.Entities))
		item.IsPrivate, item.CanBeUpgraded, item.WasRefunded = g.IsPrivate, g.CanBeUpgraded, g.WasRefunded
		item.PrepaidUpgradeStarCount, item.IsUpgradeSeparate = g.PrepaidUpgradeStarCount, g.IsUpgradeSeparate
		item.UniqueGiftNumber = g.UniqueGiftNumber
		if len(g.Gift) > 0 {
			var gift tgGift
			if json.Unmarshal(g.Gift, &gift) != nil {
				return giftOut{}, false
			}
			details = ptr(r.gift(gift, prefix, &omitted))
		}
	case "unique":
		item.CanBeTransferred, item.TransferStarCount, item.NextTransferDate = g.CanBeTransferred,
			g.TransferStarCount, g.NextTransferDate
		if len(g.Gift) > 0 {
			var gift uniqueGiftOut
			if json.Unmarshal(g.Gift, &gift) != nil {
				return giftOut{}, false
			}
			details = ptr(r.uniqueGift(gift, prefix, &omitted))
		}
	}
	item.GiftDetails = bounded(&omitted, giftDetailsName, details)
	item.OmittedFields = omitted
	return item, true
}

func ptr[T any](v T) *T { return &v }

// decodeOwnedGifts maps an OwnedGifts answer; refs signs sticker files, and nil leaves them without a file_ref.
func decodeOwnedGifts(op string, raw json.RawMessage, refs fileRefFunc) (*giftsOut, error) {
	var got struct {
		TotalCount int64         `json:"total_count"`
		Gifts      []tgOwnedGift `json:"gifts"`
		NextOffset string        `json:"next_offset"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	out := &giftsOut{TotalCount: got.TotalCount, NextOffset: capText(got.NextOffset, maxGiftsOffset), Gifts: []giftOut{}}
	for i, g := range got.Gifts {
		if i >= maxGiftsLimit {
			break
		}
		item, ok := refs.ownedGift(g)
		if !ok {
			return nil, invalidResponse(op)
		}
		out.Gifts = append(out.Gifts, item)
	}
	return out, nil
}

// decodeAvailableGifts maps a Gifts answer to at most maxAvailableGifts catalog entries.
func decodeAvailableGifts(op string, raw json.RawMessage, refs fileRefFunc) (any, error) {
	var got struct {
		Gifts []tgGift `json:"gifts"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	out := make([]availableGiftOut, 0, min(len(got.Gifts), maxAvailableGifts))
	for _, g := range got.Gifts {
		if len(out) == maxAvailableGifts {
			break
		}
		var omitted []string
		item := availableGiftOut{GiftID: capText(g.ID, maxNameRunes), StarCount: g.StarCount,
			giftTraits: refs.traits(g.giftTraits, "", &omitted), OmittedFields: omitted}
		if g.TotalCount != nil {
			item.TotalCount = *g.TotalCount
		}
		if g.RemainingCount != nil {
			item.RemainingCount = *g.RemainingCount
		}
		out = append(out, item)
	}
	return struct {
		Gifts []availableGiftOut `json:"gifts"`
	}{out}, nil
}

const (
	objectSchema = `{"type":"object"}`
	// giftTraitsSchema lists the members shared by a catalog entry and the Gift of an owned gift.
	giftTraitsSchema = `"sticker":` + objectSchema + `,"upgrade_star_count":{"type":"integer"},` +
		`"is_premium":{"type":"boolean"},"has_colors":{"type":"boolean"},"personal_total_count":{"type":"integer"},` +
		`"personal_remaining_count":{"type":"integer"},"background":` + objectSchema + `,` +
		`"unique_gift_variant_count":{"type":"integer"},"publisher_chat":` + objectSchema
	giftDetailsSchema = `{"type":"object","properties":{"id":{"type":"string"},"star_count":{"type":"integer"},` +
		`"total_count":{"type":"integer"},"remaining_count":{"type":"integer"},` + giftTraitsSchema + `,` +
		`"gift_id":{"type":"string"},"base_name":{"type":"string"},"name":{"type":"string"},"number":{"type":"integer"},` +
		`"model":` + objectSchema + `,"symbol":` + objectSchema + `,"backdrop":` + objectSchema + `,` +
		`"is_burned":{"type":"boolean"},"is_from_blockchain":{"type":"boolean"},"colors":` + objectSchema + `},` +
		`"additionalProperties":false}`
	ownedGiftsOutputSchema = `{"type":"object","properties":{"total_count":{"type":"integer"},` +
		`"next_offset":{"type":"string"},"gifts":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"gift_id":{"type":"string"},"name":{"type":"string"},"number":{"type":"integer"},` +
		`"send_date":{"type":"integer"},"is_saved":{"type":"boolean"},"text":{"type":"string"},` +
		`"sender":` + userSchema + `,"star_count":{"type":"integer"},"convert_star_count":{"type":"integer"},` +
		`"owned_gift_id":{"type":"string"},"entities":{"type":"array","items":{"type":"object"}},` +
		`"is_private":{"type":"boolean"},"can_be_upgraded":{"type":"boolean"},"was_refunded":{"type":"boolean"},` +
		`"prepaid_upgrade_star_count":{"type":"integer"},"is_upgrade_separate":{"type":"boolean"},` +
		`"unique_gift_number":{"type":"integer"},"can_be_transferred":{"type":"boolean"},` +
		`"transfer_star_count":{"type":"integer"},"next_transfer_date":{"type":"integer"},` +
		`"gift_details":` + giftDetailsSchema + `,"omitted_fields":{"type":"array","items":{"type":"string"}}},` +
		`"additionalProperties":false}}},"required":["total_count","gifts"],"additionalProperties":false}`
	availableGiftsOutputSchema = `{"type":"object","properties":{"gifts":{"type":"array","items":` +
		`{"type":"object","properties":{"gift_id":{"type":"string"},"star_count":{"type":"integer"},` +
		`"total_count":{"type":"integer"},"remaining_count":{"type":"integer"},` + giftTraitsSchema + `,` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}}},` +
		`"required":["gifts"],"additionalProperties":false}`
)

const (
	giftTraitsText = "sticker, upgrade_star_count, is_premium, has_colors, personal_total_count, personal_remaining_count, " +
		"background, unique_gift_variant_count, and publisher_chat (data only, never a target)"
	giftStickerText = "a sticker carries a file_ref bound to the target of the call"
	// giftOwnedText names every member of an owned gift, including those of gift_details.
	giftOwnedText = "Gifts with type, gift_id, name, number, send_date, is_saved, is_private, text, entities, " +
		"sender (a Telegram User), star_count, convert_star_count, prepaid_upgrade_star_count, can_be_upgraded, " +
		"was_refunded, is_upgrade_separate, unique_gift_number, owned_gift_id, can_be_transferred, " +
		"transfer_star_count, next_transfer_date, omitted_fields, and gift_details; gift_details is the Telegram Gift " +
		"(id, star_count, total_count, remaining_count, " + giftTraitsText + ") or UniqueGift (gift_id, base_name, name, " +
		"number, model, symbol, backdrop, is_premium, is_burned, is_from_blockchain, colors, publisher_chat); " +
		"star values are read-only numbers; "
)

// giftsField describes the gifts member of a gifts tool; a business sticker has no file_ref.
func giftsField(business bool) capability.Field {
	text := giftOwnedText + giftStickerText
	if business {
		text = giftOwnedText + "a sticker of a business gift has no file_ref"
	}
	return capability.Field{Name: "gifts", Description: text}
}
