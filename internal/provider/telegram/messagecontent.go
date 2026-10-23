package telegram

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
)

// This file maps the content members of the Bot API type Message for the shared message mapper. Types are
// decoded with the members the Bot API documents and printed with the same names. Every text is untrusted and
// bounded; every address is text and is never fetched. A file_id becomes a file_ref, and a business message
// has none. Floating-point values stay json.Number, so they print exactly as Telegram sent them.

const (
	maxEntities = 100
	// maxURLRunes bounds an address, which is untrusted text and never fetched.
	maxURLRunes = 2048
)

// cleanBody bounds a text that may span lines: it keeps line breaks and tabs and removes other control characters.
func cleanBody(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, value)
	}
	return capText(value, limit)
}

func txtName(v string) string { return cleanText(v, maxNameRunes) }
func txtBody(v string) string { return cleanBody(v, maxMessageLength) }
func txtLink(v string) string { return cleanText(v, maxURLRunes) }

type entityOut struct {
	Type           string   `json:"type"`
	Offset         int64    `json:"offset"`
	Length         int64    `json:"length"`
	URL            string   `json:"url,omitempty"`
	User           *userOut `json:"user,omitempty"`
	Language       string   `json:"language,omitempty"`
	CustomEmojiID  string   `json:"custom_emoji_id,omitempty"`
	UnixTime       *int64   `json:"unix_time,omitempty"`
	DateTimeFormat string   `json:"date_time_format,omitempty"`
}

// entities keeps the first maxEntities entries.
func entities(list []entityOut) []entityOut {
	var out []entityOut
	for _, e := range list[:min(len(list), maxEntities)] {
		out = append(out, entityOut{Type: txtName(e.Type), Offset: e.Offset, Length: e.Length, URL: txtLink(e.URL),
			User: headUser(e.User), Language: txtName(e.Language), CustomEmojiID: txtName(e.CustomEmojiID),
			UnixTime: e.UnixTime, DateTimeFormat: txtName(e.DateTimeFormat)})
	}
	return out
}

type linkPreviewOut struct {
	IsDisabled       bool   `json:"is_disabled,omitempty"`
	URL              string `json:"url,omitempty"`
	PreferSmallMedia bool   `json:"prefer_small_media,omitempty"`
	PreferLargeMedia bool   `json:"prefer_large_media,omitempty"`
	ShowAboveText    bool   `json:"show_above_text,omitempty"`
}

type suggestedPostPriceOut struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

type suggestedPostInfoOut struct {
	State    string                 `json:"state"`
	Price    *suggestedPostPriceOut `json:"price,omitempty"`
	SendDate int64                  `json:"send_date,omitempty"`
}

// paidMediaOut is the union PaidMedia; type tells which members are present.
type paidMediaOut struct {
	Type      string      `json:"type"`
	Width     int64       `json:"width,omitempty"`
	Height    int64       `json:"height,omitempty"`
	Duration  int64       `json:"duration,omitempty"`
	Photo     []photoSize `json:"photo,omitempty"`
	Video     *video      `json:"video,omitempty"`
	LivePhoto *livePhoto  `json:"live_photo,omitempty"`
}

type paidMediaInfoOut struct {
	StarCount int64          `json:"star_count"`
	PaidMedia []paidMediaOut `json:"paid_media,omitempty"`
}

type checklistTaskOut struct {
	ID              int64       `json:"id"`
	Text            string      `json:"text"`
	TextEntities    []entityOut `json:"text_entities,omitempty"`
	CompletedByUser *userOut    `json:"completed_by_user,omitempty"`
	CompletedByChat *chatRefOut `json:"completed_by_chat,omitempty"`
	CompletionDate  int64       `json:"completion_date,omitempty"`
}

type checklistOut struct {
	Title                    string             `json:"title"`
	TitleEntities            []entityOut        `json:"title_entities,omitempty"`
	Tasks                    []checklistTaskOut `json:"tasks"`
	OthersCanAddTasks        bool               `json:"others_can_add_tasks,omitempty"`
	OthersCanMarkTasksAsDone bool               `json:"others_can_mark_tasks_as_done,omitempty"`
}

type contactOut struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	VCard       string `json:"vcard,omitempty"`
}

type diceOut struct {
	Emoji string `json:"emoji"`
	Value int64  `json:"value"`
}

type gameOut struct {
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	Photo        []photoSize `json:"photo,omitempty"`
	Text         string      `json:"text,omitempty"`
	TextEntities []entityOut `json:"text_entities,omitempty"`
	Animation    *animation  `json:"animation,omitempty"`
}

type messageLocationOut struct {
	Latitude             json.Number `json:"latitude,omitempty"`
	Longitude            json.Number `json:"longitude,omitempty"`
	HorizontalAccuracy   json.Number `json:"horizontal_accuracy,omitempty"`
	LivePeriod           int64       `json:"live_period,omitempty"`
	Heading              int64       `json:"heading,omitempty"`
	ProximityAlertRadius int64       `json:"proximity_alert_radius,omitempty"`
}

type venueOut struct {
	Location        *messageLocationOut `json:"location,omitempty"`
	Title           string              `json:"title"`
	Address         string              `json:"address"`
	FoursquareID    string              `json:"foursquare_id,omitempty"`
	FoursquareType  string              `json:"foursquare_type,omitempty"`
	GooglePlaceID   string              `json:"google_place_id,omitempty"`
	GooglePlaceType string              `json:"google_place_type,omitempty"`
}

type linkOut struct {
	URL string `json:"url"`
}

// pollMediaOut is the Bot API PollMedia; its media objects use the media mapper.
type pollMediaOut struct {
	Animation *animation          `json:"animation,omitempty"`
	Audio     *audio              `json:"audio,omitempty"`
	Document  *document           `json:"document,omitempty"`
	Link      *linkOut            `json:"link,omitempty"`
	LivePhoto *livePhoto          `json:"live_photo,omitempty"`
	Location  *messageLocationOut `json:"location,omitempty"`
	Photo     []photoSize         `json:"photo,omitempty"`
	Sticker   *sticker            `json:"sticker,omitempty"`
	Venue     *venueOut           `json:"venue,omitempty"`
	Video     *video              `json:"video,omitempty"`
}

type pollOptionOut struct {
	PersistentID string        `json:"persistent_id"`
	Text         string        `json:"text"`
	TextEntities []entityOut   `json:"text_entities,omitempty"`
	Media        *pollMediaOut `json:"media,omitempty"`
	VoterCount   int64         `json:"voter_count"`
	AddedByUser  *userOut      `json:"added_by_user,omitempty"`
	AddedByChat  *chatRefOut   `json:"added_by_chat,omitempty"`
	AdditionDate int64         `json:"addition_date,omitempty"`
}

// pollOut prints the booleans Telegram always sends, so a false is a value and stays.
type pollOut struct {
	ID                    string          `json:"id"`
	Question              string          `json:"question"`
	QuestionEntities      []entityOut     `json:"question_entities,omitempty"`
	Options               []pollOptionOut `json:"options"`
	TotalVoterCount       int64           `json:"total_voter_count"`
	IsClosed              bool            `json:"is_closed"`
	IsAnonymous           bool            `json:"is_anonymous"`
	Type                  string          `json:"type"`
	AllowsMultipleAnswers bool            `json:"allows_multiple_answers"`
	AllowsRevoting        bool            `json:"allows_revoting"`
	MembersOnly           bool            `json:"members_only"`
	CountryCodes          []string        `json:"country_codes,omitempty"`
	CorrectOptionIDs      []int64         `json:"correct_option_ids,omitempty"`
	Explanation           string          `json:"explanation,omitempty"`
	ExplanationEntities   []entityOut     `json:"explanation_entities,omitempty"`
	ExplanationMedia      *pollMediaOut   `json:"explanation_media,omitempty"`
	OpenPeriod            int64           `json:"open_period,omitempty"`
	CloseDate             int64           `json:"close_date,omitempty"`
	Description           string          `json:"description,omitempty"`
	DescriptionEntities   []entityOut     `json:"description_entities,omitempty"`
	Media                 *pollMediaOut   `json:"media,omitempty"`
}

type invoiceOut struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	StartParameter string `json:"start_parameter"`
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
}

// emptyObject is a Bot API placeholder type without members; it prints as {}.
type emptyObject struct{}

type webAppOut struct {
	URL string `json:"url"`
}

type loginURLOut struct {
	URL                string `json:"url"`
	ForwardText        string `json:"forward_text,omitempty"`
	BotUsername        string `json:"bot_username,omitempty"`
	RequestWriteAccess bool   `json:"request_write_access,omitempty"`
}

type switchChosenOut struct {
	Query             string `json:"query,omitempty"`
	AllowUserChats    bool   `json:"allow_user_chats,omitempty"`
	AllowBotChats     bool   `json:"allow_bot_chats,omitempty"`
	AllowGroupChats   bool   `json:"allow_group_chats,omitempty"`
	AllowChannelChats bool   `json:"allow_channel_chats,omitempty"`
}

type copyTextOut struct {
	Text string `json:"text"`
}

// buttonOut is an InlineKeyboardButton. callback_data is printed like that of a callback query update.
type buttonOut struct {
	Text                         string           `json:"text"`
	IconCustomEmojiID            string           `json:"icon_custom_emoji_id,omitempty"`
	Style                        string           `json:"style,omitempty"`
	URL                          string           `json:"url,omitempty"`
	CallbackData                 string           `json:"callback_data,omitempty"`
	WebApp                       *webAppOut       `json:"web_app,omitempty"`
	LoginURL                     *loginURLOut     `json:"login_url,omitempty"`
	SwitchInlineQuery            *string          `json:"switch_inline_query,omitempty"`
	SwitchInlineQueryCurrentChat *string          `json:"switch_inline_query_current_chat,omitempty"`
	SwitchInlineQueryChosenChat  *switchChosenOut `json:"switch_inline_query_chosen_chat,omitempty"`
	CopyText                     *copyTextOut     `json:"copy_text,omitempty"`
	CallbackGame                 *emptyObject     `json:"callback_game,omitempty"`
	Pay                          bool             `json:"pay,omitempty"`
	Disabled                     *emptyObject     `json:"disabled,omitempty"`
}

type inlineKeyboardOut struct {
	InlineKeyboard [][]buttonOut `json:"inline_keyboard"`
	ForceReply     bool          `json:"force_reply,omitempty"`
}

type giveawayOut struct {
	Chats                         []chatRefOut `json:"chats"`
	WinnersSelectionDate          int64        `json:"winners_selection_date"`
	WinnerCount                   int64        `json:"winner_count"`
	OnlyNewMembers                bool         `json:"only_new_members,omitempty"`
	HasPublicWinners              bool         `json:"has_public_winners,omitempty"`
	PrizeDescription              string       `json:"prize_description,omitempty"`
	CountryCodes                  []string     `json:"country_codes,omitempty"`
	PrizeStarCount                int64        `json:"prize_star_count,omitempty"`
	PremiumSubscriptionMonthCount int64        `json:"premium_subscription_month_count,omitempty"`
}

type giveawayWinnersOut struct {
	Chat                          *chatRefOut `json:"chat,omitempty"`
	GiveawayMessageID             int64       `json:"giveaway_message_id"`
	WinnersSelectionDate          int64       `json:"winners_selection_date"`
	WinnerCount                   int64       `json:"winner_count"`
	Winners                       []userOut   `json:"winners"`
	AdditionalChatCount           int64       `json:"additional_chat_count,omitempty"`
	PrizeStarCount                int64       `json:"prize_star_count,omitempty"`
	PremiumSubscriptionMonthCount int64       `json:"premium_subscription_month_count,omitempty"`
	UnclaimedPrizeCount           int64       `json:"unclaimed_prize_count,omitempty"`
	OnlyNewMembers                bool        `json:"only_new_members,omitempty"`
	WasRefunded                   bool        `json:"was_refunded,omitempty"`
	PrizeDescription              string      `json:"prize_description,omitempty"`
}

// messageContentOut holds the content members of a Message that the flat fields of every message output do
// not already carry (text, caption, and the media objects of media_items).
type messageContentOut struct {
	Entities              []entityOut           `json:"entities,omitempty"`
	LinkPreviewOptions    *linkPreviewOut       `json:"link_preview_options,omitempty"`
	SuggestedPostInfo     *suggestedPostInfoOut `json:"suggested_post_info,omitempty"`
	EffectID              string                `json:"effect_id,omitempty"`
	RichMessage           map[string]any        `json:"rich_message,omitempty"`
	PaidMedia             *paidMediaInfoOut     `json:"paid_media,omitempty"`
	Story                 *storyOut             `json:"story,omitempty"`
	CaptionEntities       []entityOut           `json:"caption_entities,omitempty"`
	ShowCaptionAboveMedia bool                  `json:"show_caption_above_media,omitempty"`
	HasMediaSpoiler       bool                  `json:"has_media_spoiler,omitempty"`
	Checklist             *checklistOut         `json:"checklist,omitempty"`
	Contact               *contactOut           `json:"contact,omitempty"`
	Dice                  *diceOut              `json:"dice,omitempty"`
	Game                  *gameOut              `json:"game,omitempty"`
	Poll                  *pollOut              `json:"poll,omitempty"`
	Venue                 *venueOut             `json:"venue,omitempty"`
	Location              *messageLocationOut   `json:"location,omitempty"`
	Invoice               *invoiceOut           `json:"invoice,omitempty"`
	ReplyMarkup           *inlineKeyboardOut    `json:"reply_markup,omitempty"`
	Giveaway              *giveawayOut          `json:"giveaway,omitempty"`
	GiveawayWinners       *giveawayWinnersOut   `json:"giveaway_winners,omitempty"`
}

// paidMediaItems maps the PaidMedia list; members of a video or live photo over the bounds are named in inner.
func (r fileRefFunc) paidMediaItems(list []paidMediaOut, inner *[]string) []paidMediaOut {
	var out []paidMediaOut
	for _, item := range list {
		e := paidMediaOut{Type: txtName(item.Type), Width: item.Width, Height: item.Height, Duration: item.Duration,
			Photo: r.photoSizes(item.Photo)}
		if item.Video != nil {
			v := r.video(*item.Video, inner)
			e.Video = &v
		}
		if item.LivePhoto != nil {
			l := r.livePhoto(*item.LivePhoto, inner)
			e.LivePhoto = &l
		}
		out = append(out, e)
	}
	return out
}

// scoped names what a media mapper left out of a member, as <member>.<kind>.<member>.
func scoped(omitted *[]string, member string, inner []string) {
	for _, n := range inner {
		*omitted = append(*omitted, member+"."+strings.TrimPrefix(n, "media_items."))
	}
}

func cleanLocation(l *messageLocationOut) *messageLocationOut {
	if l == nil {
		return nil
	}
	return &messageLocationOut{Latitude: l.Latitude, Longitude: l.Longitude, HorizontalAccuracy: l.HorizontalAccuracy,
		LivePeriod: l.LivePeriod, Heading: l.Heading, ProximityAlertRadius: l.ProximityAlertRadius}
}

func cleanVenue(v *venueOut) *venueOut {
	if v == nil {
		return nil
	}
	return &venueOut{Location: cleanLocation(v.Location), Title: txtBody(v.Title), Address: txtBody(v.Address),
		FoursquareID: txtName(v.FoursquareID), FoursquareType: txtName(v.FoursquareType),
		GooglePlaceID: txtName(v.GooglePlaceID), GooglePlaceType: txtName(v.GooglePlaceType)}
}

func cleanCodes(list []string) []string {
	var out []string
	for _, c := range list[:min(len(list), maxNestedList)] {
		out = append(out, txtName(c))
	}
	return out
}

func (r fileRefFunc) pollMedia(m *pollMediaOut, inner *[]string) *pollMediaOut {
	if m == nil {
		return nil
	}
	out := &pollMediaOut{Location: cleanLocation(m.Location), Venue: cleanVenue(m.Venue)}
	if m.Animation != nil {
		a := r.animation(*m.Animation, inner)
		out.Animation = &a
	}
	if m.Audio != nil {
		a := r.audio(*m.Audio, inner)
		out.Audio = &a
	}
	if m.Document != nil {
		d := r.document(*m.Document, inner)
		out.Document = &d
	}
	if m.Link != nil {
		out.Link = &linkOut{URL: txtLink(m.Link.URL)}
	}
	if m.LivePhoto != nil {
		l := r.livePhoto(*m.LivePhoto, inner)
		out.LivePhoto = &l
	}
	out.Photo = r.photoSizes(m.Photo)
	if m.Sticker != nil {
		s := r.sticker(*m.Sticker, inner)
		out.Sticker = &s
	}
	if m.Video != nil {
		v := r.video(*m.Video, inner)
		out.Video = &v
	}
	return out
}

func (s messageScope) poll(p *pollOut, inner *[]string) *pollOut {
	out := *p
	out.ID, out.Question, out.Type = txtName(p.ID), txtBody(p.Question), txtName(p.Type)
	out.QuestionEntities = entities(p.QuestionEntities)
	out.Options = nil
	for _, o := range p.Options {
		out.Options = append(out.Options, pollOptionOut{PersistentID: txtName(o.PersistentID), Text: txtBody(o.Text),
			TextEntities: entities(o.TextEntities), Media: s.refs.pollMedia(o.Media, inner), VoterCount: o.VoterCount,
			AddedByUser: headUser(o.AddedByUser), AddedByChat: s.headChat(o.AddedByChat), AdditionDate: o.AdditionDate})
	}
	out.CountryCodes = cleanCodes(p.CountryCodes)
	out.Explanation = txtBody(p.Explanation)
	out.ExplanationEntities = entities(p.ExplanationEntities)
	out.ExplanationMedia = s.refs.pollMedia(p.ExplanationMedia, inner)
	out.Description = txtBody(p.Description)
	out.DescriptionEntities = entities(p.DescriptionEntities)
	out.Media = s.refs.pollMedia(p.Media, inner)
	return &out
}

func cleanButton(b buttonOut) buttonOut {
	out := buttonOut{Text: txtName(b.Text), IconCustomEmojiID: txtName(b.IconCustomEmojiID), Style: txtName(b.Style),
		URL: txtLink(b.URL), CallbackData: cleanText(b.CallbackData, maxCallbackRunes), CallbackGame: b.CallbackGame,
		Pay: b.Pay, Disabled: b.Disabled}
	if b.WebApp != nil {
		out.WebApp = &webAppOut{URL: txtLink(b.WebApp.URL)}
	}
	if l := b.LoginURL; l != nil {
		out.LoginURL = &loginURLOut{URL: txtLink(l.URL), ForwardText: txtName(l.ForwardText), BotUsername: txtName(l.BotUsername),
			RequestWriteAccess: l.RequestWriteAccess}
	}
	if b.SwitchInlineQuery != nil {
		v := txtBody(*b.SwitchInlineQuery)
		out.SwitchInlineQuery = &v
	}
	if b.SwitchInlineQueryCurrentChat != nil {
		v := txtBody(*b.SwitchInlineQueryCurrentChat)
		out.SwitchInlineQueryCurrentChat = &v
	}
	if c := b.SwitchInlineQueryChosenChat; c != nil {
		cc := *c
		cc.Query = txtBody(c.Query)
		out.SwitchInlineQueryChosenChat = &cc
	}
	if b.CopyText != nil {
		out.CopyText = &copyTextOut{Text: txtBody(b.CopyText.Text)}
	}
	return out
}

// richMembers are the member names the Bot API documents for RichMessage, its block and text types, and the types
// nested in them (RichMessageButton, WebAppInfo, LoginUrl, SwitchInlineQueryChosenChat, CopyTextButton, PhotoSize,
// Animation, Audio, Document, Video, VideoQuality, Voice, Location, User). file_id is handled apart. A member
// outside the set is dropped, because the kind of an undocumented value is not known and may grant access.
var richMembers = func() map[string]bool {
	set := map[string]bool{}
	for _, n := range strings.Fields(`added_to_attachment_menu align allow_bot_chats allow_channel_chats allow_group_chats
		allow_user_chats allows_users_to_create_topics alternative_text anchor_name animation audio bank_card_number blocks
		bot_command bot_username button buttons callback_data can_connect_to_business can_join_groups can_manage_bots
		can_read_all_group_messages caption cashtag cells codec colspan copy_text cover credit custom_emoji_id
		date_time_format disabled document duration email_address expression file_name file_size file_unique_id first_name
		forward_text has_checkbox has_main_web_app has_spoiler has_topics_enabled hashtag heading height
		horizontal_accuracy id is_bordered is_bot is_checked is_compact is_header is_open is_premium is_rtl is_striped
		items label language language_code last_name latitude live_period location login_url longitude mime_type name
		performer phone_number photo proximity_alert_radius qualities query reference_name request_write_access rowspan
		size start_timestamp style summary supports_guest_queries supports_inline_queries supports_join_request_queries
		switch_inline_query switch_inline_query_chosen_chat switch_inline_query_current_chat text thumbnail title type
		unix_time url user username valign value video voice_note web_app width zoom`) {
		set[n] = true
	}
	return set
}()

// scrubRich copies a decoded rich_message into a printable tree: texts are bounded, every file_id becomes a
// file_ref (none for a business message), blocked and undocumented members are dropped, and numbers keep their
// text. The type has about fifty block and text variants, so it is read by the union of their member names
// instead of one struct each; ok is false when the tree is deeper than the nesting limit.
func (r fileRefFunc) scrubRich(v any, key string, depth int) (out any, ok bool) {
	if depth > maxNestedDepth {
		return nil, false
	}
	switch t := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, child := range t {
			if isBlockedField(k) {
				continue
			}
			if k != "file_id" && !richMembers[k] {
				continue
			}
			if k == "file_id" {
				if id, _ := child.(string); r.ref(id) != "" {
					m["file_ref"] = r.ref(id)
				}
				continue
			}
			c, ok := r.scrubRich(child, k, depth+1)
			if !ok {
				return nil, false
			}
			if c != nil {
				m[k] = c
			}
		}
		return m, true
	case []any:
		list := make([]any, 0, len(t))
		for _, child := range t {
			c, ok := r.scrubRich(child, key, depth+1)
			if !ok {
				return nil, false
			}
			if c != nil {
				list = append(list, c)
			}
		}
		return list, true
	case string:
		switch {
		case strings.Contains(key, "url"):
			return txtLink(t), true
		case key == "callback_data":
			return cleanText(t, maxCallbackRunes), true
		}
		return txtBody(t), true
	case json.Number, bool:
		return t, true
	}
	return nil, true
}

func (r fileRefFunc) richMessage(raw json.RawMessage, omitted *[]string) map[string]any {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var tree map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&tree) != nil {
		return nil
	}
	out, ok := r.scrubRich(tree, "", 0)
	m, _ := out.(map[string]any)
	if !ok || len(m) == 0 {
		if !ok {
			*omitted = append(*omitted, "rich_message")
		}
		return nil
	}
	return bounded(omitted, "rich_message", m)
}

// messageContent maps the content members of m. A member over the nesting limits is left out and named in
// omitted. Chats and users named inside are data only and never targets.
func (s messageScope) messageContent(m *tgMessage) (out messageContentOut, omitted []string) {
	out.Entities = bounded(&omitted, "entities", entities(m.Entities))
	out.CaptionEntities = bounded(&omitted, "caption_entities", entities(m.CaptionEntities))
	out.EffectID = txtName(m.EffectID)
	out.ShowCaptionAboveMedia, out.HasMediaSpoiler = m.ShowCaptionAboveMedia, m.HasMediaSpoiler
	if l := m.LinkPreviewOptions; l != nil {
		out.LinkPreviewOptions = bounded(&omitted, "link_preview_options", &linkPreviewOut{IsDisabled: l.IsDisabled,
			URL: txtLink(l.URL), PreferSmallMedia: l.PreferSmallMedia, PreferLargeMedia: l.PreferLargeMedia,
			ShowAboveText: l.ShowAboveText})
	}
	if p := m.SuggestedPostInfo; p != nil {
		info := &suggestedPostInfoOut{State: txtName(p.State), SendDate: p.SendDate}
		if p.Price != nil {
			info.Price = &suggestedPostPriceOut{Currency: txtName(p.Price.Currency), Amount: p.Price.Amount}
		}
		out.SuggestedPostInfo = bounded(&omitted, "suggested_post_info", info)
	}
	out.RichMessage = s.refs.richMessage(m.RichMessage, &omitted)
	if p := m.PaidMedia; p != nil {
		info := &paidMediaInfoOut{StarCount: p.StarCount}
		var inner []string
		info.PaidMedia = s.refs.paidMediaItems(p.PaidMedia, &inner)
		scoped(&omitted, "paid_media", inner)
		out.PaidMedia = bounded(&omitted, "paid_media", info)
	}
	if st := m.Story; st != nil {
		out.Story = bounded(&omitted, "story", &storyOut{Chat: s.headChat(st.Chat), ID: st.ID})
	}
	if c := m.Checklist; c != nil {
		list := &checklistOut{Title: txtBody(c.Title), TitleEntities: entities(c.TitleEntities),
			OthersCanAddTasks: c.OthersCanAddTasks, OthersCanMarkTasksAsDone: c.OthersCanMarkTasksAsDone}
		for _, t := range c.Tasks {
			list.Tasks = append(list.Tasks, checklistTaskOut{ID: t.ID, Text: txtBody(t.Text), TextEntities: entities(t.TextEntities),
				CompletedByUser: headUser(t.CompletedByUser), CompletedByChat: s.headChat(t.CompletedByChat),
				CompletionDate: t.CompletionDate})
		}
		out.Checklist = bounded(&omitted, "checklist", list)
	}
	if c := m.Contact; c != nil {
		out.Contact = bounded(&omitted, "contact", &contactOut{PhoneNumber: txtName(c.PhoneNumber), FirstName: txtName(c.FirstName),
			LastName: txtName(c.LastName), UserID: c.UserID, VCard: txtBody(c.VCard)})
	}
	if d := m.Dice; d != nil {
		out.Dice = bounded(&omitted, "dice", &diceOut{Emoji: txtName(d.Emoji), Value: d.Value})
	}
	if g := m.Game; g != nil {
		var inner []string
		game := &gameOut{Title: txtName(g.Title), Description: txtBody(g.Description), Photo: s.refs.photoSizes(g.Photo),
			Text: txtBody(g.Text), TextEntities: entities(g.TextEntities)}
		if g.Animation != nil {
			a := s.refs.animation(*g.Animation, &inner)
			game.Animation = &a
		}
		scoped(&omitted, "game", inner)
		out.Game = bounded(&omitted, "game", game)
	}
	if p := m.Poll; p != nil {
		var inner []string
		poll := s.poll(p, &inner)
		scoped(&omitted, "poll", inner)
		out.Poll = bounded(&omitted, "poll", poll)
	}
	out.Venue = bounded(&omitted, "venue", cleanVenue(m.Venue))
	out.Location = bounded(&omitted, "location", cleanLocation(m.Location))
	if i := m.Invoice; i != nil {
		out.Invoice = bounded(&omitted, "invoice", &invoiceOut{Title: txtName(i.Title), Description: txtBody(i.Description),
			StartParameter: txtName(i.StartParameter), Currency: txtName(i.Currency), TotalAmount: i.TotalAmount})
	}
	if k := m.ReplyMarkup; k != nil {
		markup := &inlineKeyboardOut{InlineKeyboard: [][]buttonOut{}, ForceReply: k.ForceReply}
		for _, row := range k.InlineKeyboard {
			cleaned := []buttonOut{}
			for _, b := range row {
				cleaned = append(cleaned, cleanButton(b))
			}
			markup.InlineKeyboard = append(markup.InlineKeyboard, cleaned)
		}
		out.ReplyMarkup = bounded(&omitted, "reply_markup", markup)
	}
	if g := m.Giveaway; g != nil {
		giveaway := &giveawayOut{WinnersSelectionDate: g.WinnersSelectionDate, WinnerCount: g.WinnerCount,
			OnlyNewMembers: g.OnlyNewMembers, HasPublicWinners: g.HasPublicWinners, PrizeDescription: txtBody(g.PrizeDescription),
			CountryCodes: cleanCodes(g.CountryCodes), PrizeStarCount: g.PrizeStarCount,
			PremiumSubscriptionMonthCount: g.PremiumSubscriptionMonthCount}
		for _, c := range g.Chats {
			if cleaned := s.headChat(&c); cleaned != nil {
				giveaway.Chats = append(giveaway.Chats, *cleaned)
			}
		}
		out.Giveaway = bounded(&omitted, "giveaway", giveaway)
	}
	if w := m.GiveawayWinners; w != nil {
		winners := &giveawayWinnersOut{Chat: s.headChat(w.Chat), GiveawayMessageID: w.GiveawayMessageID,
			WinnersSelectionDate: w.WinnersSelectionDate, WinnerCount: w.WinnerCount,
			AdditionalChatCount: w.AdditionalChatCount, PrizeStarCount: w.PrizeStarCount,
			PremiumSubscriptionMonthCount: w.PremiumSubscriptionMonthCount, UnclaimedPrizeCount: w.UnclaimedPrizeCount,
			OnlyNewMembers: w.OnlyNewMembers, WasRefunded: w.WasRefunded, PrizeDescription: txtBody(w.PrizeDescription)}
		for _, u := range w.Winners {
			if cleaned := headUser(&u); cleaned != nil {
				winners.Winners = append(winners.Winners, *cleaned)
			}
		}
		out.GiveawayWinners = bounded(&omitted, "giveaway_winners", winners)
	}
	return out, omitted
}
