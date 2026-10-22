package telegram

import (
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The types below are the only mapping of the Bot API types nested in ChatFullInfo. Each decodes the members
// the Bot API documents and prints them with the same names; a value Telegram sends as 0 or false and that
// means something (a color index, a coordinate, a minute) is a pointer or has no omitempty, so it stays.

type birthdateOut struct {
	Day   int64 `json:"day,omitempty"`
	Month int64 `json:"month,omitempty"`
	Year  int64 `json:"year,omitempty"`
}

type locationOut struct {
	Latitude             *float64 `json:"latitude,omitempty"`
	Longitude            *float64 `json:"longitude,omitempty"`
	HorizontalAccuracy   *float64 `json:"horizontal_accuracy,omitempty"`
	LivePeriod           int64    `json:"live_period,omitempty"`
	Heading              int64    `json:"heading,omitempty"`
	ProximityAlertRadius int64    `json:"proximity_alert_radius,omitempty"`
}

type businessIntroOut struct {
	Title   string   `json:"title,omitempty"`
	Message string   `json:"message,omitempty"`
	Sticker *sticker `json:"sticker,omitempty"`
}

type businessLocationOut struct {
	Address  string       `json:"address,omitempty"`
	Location *locationOut `json:"location,omitempty"`
}

type openingIntervalOut struct {
	OpeningMinute int64 `json:"opening_minute"`
	ClosingMinute int64 `json:"closing_minute"`
}

type businessHoursOut struct {
	TimeZoneName string               `json:"time_zone_name,omitempty"`
	OpeningHours []openingIntervalOut `json:"opening_hours,omitempty"`
}

type chatLocationOut struct {
	Location *locationOut `json:"location,omitempty"`
	Address  string       `json:"address,omitempty"`
}

// chatRefOut is the Bot API type Chat. It names a chat outside the target binding as data only.
type chatRefOut struct {
	ID               int64  `json:"id"`
	Type             string `json:"type,omitempty"`
	Title            string `json:"title,omitempty"`
	Username         string `json:"username,omitempty"`
	FirstName        string `json:"first_name,omitempty"`
	LastName         string `json:"last_name,omitempty"`
	IsForum          bool   `json:"is_forum,omitempty"`
	IsDirectMessages bool   `json:"is_direct_messages,omitempty"`
}

type reactionOut struct {
	Type          string `json:"type,omitempty"`
	Emoji         string `json:"emoji,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

type ratingOut struct {
	Level              *int64 `json:"level,omitempty"`
	Rating             *int64 `json:"rating,omitempty"`
	CurrentLevelRating *int64 `json:"current_level_rating,omitempty"`
	NextLevelRating    *int64 `json:"next_level_rating,omitempty"`
}

type acceptedGiftTypesOut struct {
	UnlimitedGifts      *bool `json:"unlimited_gifts,omitempty"`
	LimitedGifts        *bool `json:"limited_gifts,omitempty"`
	UniqueGifts         *bool `json:"unique_gifts,omitempty"`
	PremiumSubscription *bool `json:"premium_subscription,omitempty"`
	GiftsFromChannels   *bool `json:"gifts_from_channels,omitempty"`
}

type uniqueGiftColorsOut struct {
	ModelCustomEmojiID   string  `json:"model_custom_emoji_id,omitempty"`
	SymbolCustomEmojiID  string  `json:"symbol_custom_emoji_id,omitempty"`
	LightThemeMainColor  *int64  `json:"light_theme_main_color,omitempty"`
	LightThemeOtherColor []int64 `json:"light_theme_other_colors,omitempty"`
	DarkThemeMainColor   *int64  `json:"dark_theme_main_color,omitempty"`
	DarkThemeOtherColor  []int64 `json:"dark_theme_other_colors,omitempty"`
}

type communityOut struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// chatPhotoOut is the Bot API type ChatPhoto; the two file identifiers are read apart so only references print.
type chatPhotoOut struct {
	SmallFileRef      string `json:"small_file_ref,omitempty"`
	SmallFileUniqueID string `json:"small_file_unique_id,omitempty"`
	BigFileRef        string `json:"big_file_ref,omitempty"`
	BigFileUniqueID   string `json:"big_file_unique_id,omitempty"`
	smallFileID       string
	bigFileID         string
}

func (p *chatPhotoOut) UnmarshalJSON(b []byte) error {
	var head struct {
		Small string `json:"small_file_id"`
		Big   string `json:"big_file_id"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	p.smallFileID, p.bigFileID = head.Small, head.Big
	type plain chatPhotoOut
	return json.Unmarshal(b, (*plain)(p))
}

// chatInfoOut holds the members of ChatFullInfo that chats.get prints besides the identity and settings of
// chatOut. invite_link and pinned_message are not members here, so decoding drops them.
type chatInfoOut struct {
	FirstName        string `json:"first_name,omitempty"`
	LastName         string `json:"last_name,omitempty"`
	IsDirectMessages bool   `json:"is_direct_messages,omitempty"`
	AccentColorID    *int64 `json:"accent_color_id,omitempty"`
	MaxReactionCount *int64 `json:"max_reaction_count,omitempty"`

	Photo                 *chatPhotoOut        `json:"photo,omitempty"`
	ActiveUsernames       []string             `json:"active_usernames,omitempty"`
	Birthdate             *birthdateOut        `json:"birthdate,omitempty"`
	BusinessIntro         *businessIntroOut    `json:"business_intro,omitempty"`
	BusinessLocation      *businessLocationOut `json:"business_location,omitempty"`
	BusinessOpeningHours  *businessHoursOut    `json:"business_opening_hours,omitempty"`
	PersonalChat          *chatRefOut          `json:"personal_chat,omitempty"`
	ParentChat            *chatRefOut          `json:"parent_chat,omitempty"`
	AvailableReactions    []reactionOut        `json:"available_reactions,omitempty"`
	BackgroundEmojiID     string               `json:"background_custom_emoji_id,omitempty"`
	ProfileAccentColorID  *int64               `json:"profile_accent_color_id,omitempty"`
	ProfileBackgroundID   string               `json:"profile_background_custom_emoji_id,omitempty"`
	EmojiStatusEmojiID    string               `json:"emoji_status_custom_emoji_id,omitempty"`
	EmojiStatusExpiration int64                `json:"emoji_status_expiration_date,omitempty"`
	Bio                   string               `json:"bio,omitempty"`

	HasPrivateForwards                 bool `json:"has_private_forwards,omitempty"`
	HasRestrictedVoiceAndVideoMessages bool `json:"has_restricted_voice_and_video_messages,omitempty"`
	JoinToSendMessages                 bool `json:"join_to_send_messages,omitempty"`
	JoinByRequest                      bool `json:"join_by_request,omitempty"`

	AcceptedGiftTypes    *acceptedGiftTypesOut `json:"accepted_gift_types,omitempty"`
	CanSendPaidMedia     bool                  `json:"can_send_paid_media,omitempty"`
	UnrestrictBoostCount int64                 `json:"unrestrict_boost_count,omitempty"`
	MessageAutoDelete    int64                 `json:"message_auto_delete_time,omitempty"`

	HasAggressiveAntiSpamEnabled bool `json:"has_aggressive_anti_spam_enabled,omitempty"`
	HasHiddenMembers             bool `json:"has_hidden_members,omitempty"`
	HasProtectedContent          bool `json:"has_protected_content,omitempty"`
	HasVisibleHistory            bool `json:"has_visible_history,omitempty"`

	StickerSetName        string `json:"sticker_set_name,omitempty"`
	CanSetStickerSet      bool   `json:"can_set_sticker_set,omitempty"`
	CustomEmojiStickerSet string `json:"custom_emoji_sticker_set_name,omitempty"`

	Location          *chatLocationOut     `json:"location,omitempty"`
	Rating            *ratingOut           `json:"rating,omitempty"`
	FirstProfileAudio *audio               `json:"first_profile_audio,omitempty"`
	UniqueGiftColors  *uniqueGiftColorsOut `json:"unique_gift_colors,omitempty"`
	PaidMessageStars  int64                `json:"paid_message_star_count,omitempty"`
	GuardBot          *senderOut           `json:"guard_bot,omitempty"`
	Community         *communityOut        `json:"community,omitempty"`
	OmittedFields     []string             `json:"omitted_fields,omitempty"`
}

func cleanChatRef(c *chatRefOut) *chatRefOut {
	if c == nil || c.ID == 0 {
		return nil
	}
	out := *c
	out.Type = cleanText(c.Type, maxNameRunes)
	out.Title = cleanText(c.Title, maxNameRunes)
	out.Username = cleanText(c.Username, maxNameRunes)
	out.FirstName = cleanText(c.FirstName, maxNameRunes)
	out.LastName = cleanText(c.LastName, maxNameRunes)
	return &out
}

func cleanTexts(list []string) []string {
	var out []string
	for _, s := range list {
		if len(out) == maxNestedList {
			break
		}
		if s = cleanText(s, maxNameRunes); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cleanChatInfo bounds the untrusted texts of the decoded members, replaces file identifiers with references
// of the bound chat, and drops every nested object that exceeds the nesting limits, naming it in OmittedFields.
func cleanChatInfo(in chatInfoOut, refs fileRefFunc) chatInfoOut {
	var omitted []string
	out := chatInfoOut{
		FirstName: cleanText(in.FirstName, maxNameRunes), LastName: cleanText(in.LastName, maxNameRunes),
		IsDirectMessages: in.IsDirectMessages, AccentColorID: in.AccentColorID, MaxReactionCount: in.MaxReactionCount,
		ActiveUsernames:       cleanTexts(in.ActiveUsernames),
		BackgroundEmojiID:     cleanText(in.BackgroundEmojiID, maxNameRunes),
		ProfileAccentColorID:  in.ProfileAccentColorID,
		ProfileBackgroundID:   cleanText(in.ProfileBackgroundID, maxNameRunes),
		EmojiStatusEmojiID:    cleanText(in.EmojiStatusEmojiID, maxNameRunes),
		EmojiStatusExpiration: in.EmojiStatusExpiration,
		Bio:                   cleanText(in.Bio, maxMessageLength),

		HasPrivateForwards:                 in.HasPrivateForwards,
		HasRestrictedVoiceAndVideoMessages: in.HasRestrictedVoiceAndVideoMessages,
		JoinToSendMessages:                 in.JoinToSendMessages, JoinByRequest: in.JoinByRequest,
		CanSendPaidMedia: in.CanSendPaidMedia, UnrestrictBoostCount: in.UnrestrictBoostCount,
		MessageAutoDelete:            in.MessageAutoDelete,
		HasAggressiveAntiSpamEnabled: in.HasAggressiveAntiSpamEnabled, HasHiddenMembers: in.HasHiddenMembers,
		HasProtectedContent: in.HasProtectedContent, HasVisibleHistory: in.HasVisibleHistory,
		StickerSetName:        cleanText(in.StickerSetName, maxNameRunes),
		CanSetStickerSet:      in.CanSetStickerSet,
		CustomEmojiStickerSet: cleanText(in.CustomEmojiStickerSet, maxNameRunes),
		PaidMessageStars:      in.PaidMessageStars,
	}

	if p := in.Photo; p != nil && (p.smallFileID != "" || p.bigFileID != "") {
		small, big := refs.pair(p.smallFileID, p.bigFileID)
		out.Photo = bounded(&omitted, "photo", &chatPhotoOut{SmallFileRef: small, BigFileRef: big,
			SmallFileUniqueID: cleanText(p.SmallFileUniqueID, maxNameRunes),
			BigFileUniqueID:   cleanText(p.BigFileUniqueID, maxNameRunes)})
	}
	if b := in.Birthdate; b != nil {
		out.Birthdate = bounded(&omitted, "birthdate", b)
	}
	if b := in.BusinessIntro; b != nil {
		intro := &businessIntroOut{Title: cleanText(b.Title, maxNameRunes), Message: cleanText(b.Message, maxMessageLength)}
		if b.Sticker != nil {
			var inner []string
			s := refs.sticker(*b.Sticker, &inner)
			for _, name := range inner {
				omitted = append(omitted, "business_intro.sticker."+strings.TrimPrefix(name, "media_items.sticker."))
			}
			intro.Sticker = &s
		}
		out.BusinessIntro = bounded(&omitted, "business_intro", intro)
	}
	if b := in.BusinessLocation; b != nil {
		out.BusinessLocation = bounded(&omitted, "business_location",
			&businessLocationOut{Address: cleanText(b.Address, maxNameRunes), Location: b.Location})
	}
	if h := in.BusinessOpeningHours; h != nil {
		out.BusinessOpeningHours = bounded(&omitted, "business_opening_hours",
			&businessHoursOut{TimeZoneName: cleanText(h.TimeZoneName, maxNameRunes), OpeningHours: h.OpeningHours})
	}
	out.PersonalChat = bounded(&omitted, "personal_chat", cleanChatRef(in.PersonalChat))
	out.ParentChat = bounded(&omitted, "parent_chat", cleanChatRef(in.ParentChat))
	var reactions []reactionOut
	for _, r := range in.AvailableReactions {
		reactions = append(reactions, reactionOut{Type: cleanText(r.Type, maxNameRunes),
			Emoji: cleanText(r.Emoji, maxNameRunes), CustomEmojiID: cleanText(r.CustomEmojiID, maxNameRunes)})
	}
	out.AvailableReactions = bounded(&omitted, "available_reactions", reactions)
	out.AcceptedGiftTypes = bounded(&omitted, "accepted_gift_types", in.AcceptedGiftTypes)
	if l := in.Location; l != nil {
		out.Location = bounded(&omitted, "location",
			&chatLocationOut{Location: l.Location, Address: cleanText(l.Address, maxNameRunes)})
	}
	out.Rating = bounded(&omitted, "rating", in.Rating)
	if a := in.FirstProfileAudio; a != nil {
		var inner []string
		cleaned := refs.audio(*a, &inner)
		for _, name := range inner {
			omitted = append(omitted, "first_profile_audio."+strings.TrimPrefix(name, "media_items.audio."))
		}
		out.FirstProfileAudio = bounded(&omitted, "first_profile_audio", &cleaned)
	}
	if c := in.UniqueGiftColors; c != nil {
		out.UniqueGiftColors = bounded(&omitted, "unique_gift_colors", &uniqueGiftColorsOut{
			ModelCustomEmojiID:  cleanText(c.ModelCustomEmojiID, maxNameRunes),
			SymbolCustomEmojiID: cleanText(c.SymbolCustomEmojiID, maxNameRunes),
			LightThemeMainColor: c.LightThemeMainColor, LightThemeOtherColor: c.LightThemeOtherColor,
			DarkThemeMainColor: c.DarkThemeMainColor, DarkThemeOtherColor: c.DarkThemeOtherColor})
	}
	out.GuardBot = bounded(&omitted, "guard_bot", sender(in.GuardBot))
	if c := in.Community; c != nil && c.ID != 0 {
		out.Community = bounded(&omitted, "community",
			&communityOut{ID: c.ID, Name: cleanText(c.Name, maxNameRunes)})
	}
	out.OmittedFields = omitted
	return out
}

func chatInfoProperty(name, schema string) string { return `,"` + name + `":` + schema }

func nestedSchema(description string) string {
	return `{"type":"object","description":"` + description + `"}`
}

func nestedListSchema(description string) string {
	return `{"type":"array","items":{"type":"object"},"description":"` + description + `"}`
}

var chatInfoSchema = func() string {
	var b strings.Builder
	for _, p := range []struct{ name, schema string }{
		{"first_name", `{"type":"string"}`}, {"last_name", `{"type":"string"}`},
		{"is_direct_messages", `{"type":"boolean"}`}, {"accent_color_id", `{"type":"integer"}`},
		{"max_reaction_count", `{"type":"integer"}`},
		{"photo", nestedSchema("Telegram Bot API ChatPhoto: small_file_ref, small_file_unique_id, big_file_ref, big_file_unique_id")},
		{"active_usernames", `{"type":"array","items":{"type":"string"},"maxItems":100}`},
		{"birthdate", nestedSchema("Telegram Bot API Birthdate")},
		{"business_intro", nestedSchema("Telegram Bot API BusinessIntro; its sticker is a Sticker with file_ref")},
		{"business_location", nestedSchema("Telegram Bot API BusinessLocation")},
		{"business_opening_hours", nestedSchema("Telegram Bot API BusinessOpeningHours")},
		{"personal_chat", nestedSchema("Telegram Bot API Chat of the personal channel; data only, never a target")},
		{"parent_chat", nestedSchema("Telegram Bot API Chat of the channel of a direct messages chat; data only, never a target")},
		{"available_reactions", nestedListSchema("Telegram Bot API ReactionType: type, emoji or custom_emoji_id")},
		{"background_custom_emoji_id", `{"type":"string"}`}, {"profile_accent_color_id", `{"type":"integer"}`},
		{"profile_background_custom_emoji_id", `{"type":"string"}`},
		{"emoji_status_custom_emoji_id", `{"type":"string"}`}, {"emoji_status_expiration_date", `{"type":"integer"}`},
		{"bio", `{"type":"string"}`}, {"has_private_forwards", `{"type":"boolean"}`},
		{"has_restricted_voice_and_video_messages", `{"type":"boolean"}`},
		{"join_to_send_messages", `{"type":"boolean"}`}, {"join_by_request", `{"type":"boolean"}`},
		{"accepted_gift_types", nestedSchema("Telegram Bot API AcceptedGiftTypes")},
		{"can_send_paid_media", `{"type":"boolean"}`}, {"unrestrict_boost_count", `{"type":"integer"}`},
		{"message_auto_delete_time", `{"type":"integer"}`}, {"has_aggressive_anti_spam_enabled", `{"type":"boolean"}`},
		{"has_hidden_members", `{"type":"boolean"}`}, {"has_protected_content", `{"type":"boolean"}`},
		{"has_visible_history", `{"type":"boolean"}`}, {"sticker_set_name", `{"type":"string"}`},
		{"can_set_sticker_set", `{"type":"boolean"}`}, {"custom_emoji_sticker_set_name", `{"type":"string"}`},
		{"location", nestedSchema("Telegram Bot API ChatLocation")},
		{"rating", nestedSchema("Telegram Bot API UserRating")},
		{"first_profile_audio", nestedSchema("Telegram Bot API Audio with file_ref")},
		{"unique_gift_colors", nestedSchema("Telegram Bot API UniqueGiftColors")},
		{"paid_message_star_count", `{"type":"integer"}`},
		{"guard_bot", userSchema}, {"community", nestedSchema("Telegram Bot API Community")},
		{"omitted_fields", `{"type":"array","items":{"type":"string"}}`},
	} {
		b.WriteString(chatInfoProperty(p.name, p.schema))
	}
	return b.String()
}()

var chatInfoFields = []capability.Field{
	{Name: "first_name", Description: "First name of the other party in a private chat"},
	{Name: "last_name", Description: "Last name of the other party in a private chat"},
	{Name: "is_direct_messages", Description: "True when the chat is the direct messages chat of a channel"},
	{Name: "accent_color_id", Description: "Identifier of the accent color of the chat name and backgrounds"},
	{Name: "max_reaction_count", Description: "Maximum number of reactions on one message"},
	{Name: "photo", Description: "Chat photo as ChatPhoto with small_file_ref and big_file_ref, signed for the bound chat and redeemable with telegram.files.get; no file_id"},
	{Name: "active_usernames", Description: "At most 100 active usernames"},
	{Name: "birthdate", Description: "Date of birth of the other party in a private chat: day, month, year"},
	{Name: "business_intro", Description: "Intro of a business account in a private chat: title, message, sticker with file_ref"},
	{Name: "business_location", Description: "Location of a business account: address, location"},
	{Name: "business_opening_hours", Description: "Opening hours of a business account: time_zone_name, opening_hours with opening_minute and closing_minute"},
	{Name: "personal_chat", Description: "Personal channel of the user as Chat; data only, it never becomes a target"},
	{Name: "parent_chat", Description: "Channel of a direct messages chat as Chat; data only, it never becomes a target"},
	{Name: "available_reactions", Description: "Reactions allowed in the chat as ReactionType; absent when all emoji reactions are allowed"},
	{Name: "background_custom_emoji_id", Description: "Custom emoji of the reply header and link preview background"},
	{Name: "profile_accent_color_id", Description: "Identifier of the accent color of the profile background"},
	{Name: "profile_background_custom_emoji_id", Description: "Custom emoji of the profile background"},
	{Name: "emoji_status_custom_emoji_id", Description: "Custom emoji of the emoji status"},
	{Name: "emoji_status_expiration_date", Description: "Unix time at which the emoji status expires"},
	{Name: "bio", Description: "Bio of the other party in a private chat"},
	{Name: "has_private_forwards", Description: "True when the other party allows tg://user?id links only in chats with them"},
	{Name: "has_restricted_voice_and_video_messages", Description: "True when the other party restricts voice and video note messages"},
	{Name: "join_to_send_messages", Description: "True when users must join the supergroup before they can send messages"},
	{Name: "join_by_request", Description: "True when joining the supergroup needs approval"},
	{Name: "accepted_gift_types", Description: "Gift types the chat accepts as AcceptedGiftTypes"},
	{Name: "can_send_paid_media", Description: "True when paid media can be sent to the channel"},
	{Name: "unrestrict_boost_count", Description: "Boosts a non-administrator needs to ignore slow mode and permissions"},
	{Name: "message_auto_delete_time", Description: "Seconds after which messages are deleted automatically"},
	{Name: "has_aggressive_anti_spam_enabled", Description: "True when aggressive anti-spam checks are enabled; administrators only"},
	{Name: "has_hidden_members", Description: "True when non-administrators see only bots and administrators"},
	{Name: "has_protected_content", Description: "True when messages cannot be forwarded"},
	{Name: "has_visible_history", Description: "True when new members see old messages; administrators only"},
	{Name: "sticker_set_name", Description: "Name of the group sticker set"},
	{Name: "can_set_sticker_set", Description: "True when the bot can change the group sticker set"},
	{Name: "custom_emoji_sticker_set_name", Description: "Name of the custom emoji sticker set of the group"},
	{Name: "location", Description: "Location the supergroup is connected to as ChatLocation"},
	{Name: "rating", Description: "Rating of the user in a private chat as UserRating"},
	{Name: "first_profile_audio", Description: "First audio of the profile of the user as Audio with file_ref"},
	{Name: "unique_gift_colors", Description: "Color scheme from a unique gift as UniqueGiftColors"},
	{Name: "paid_message_star_count", Description: "Telegram Stars a general user pays to send a message"},
	{Name: "guard_bot", Description: "Bot that processes join request queries as " + userFieldsText},
	{Name: "community", Description: "Community of the chat: id, name"},
	{Name: "omitted_fields", Description: "Names of the nested objects above left out for exceeding 8 levels, 100 list elements or 16 KiB, such as business_intro or business_intro.sticker.thumbnail"},
}
