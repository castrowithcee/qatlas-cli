package telegram

// userOut is the only mapping of the Bot API type User. It decodes a Telegram user and encodes the output with
// the same names; cleanUser is the single place that bounds its texts. Booleans Telegram sends only when
// true or only for bots are pointers, so a missing value stays missing.
type userOut struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`

	IsPremium                  *bool `json:"is_premium,omitempty"`
	AddedToAttachmentMenu      *bool `json:"added_to_attachment_menu,omitempty"`
	CanJoinGroups              *bool `json:"can_join_groups,omitempty"`
	CanReadAllGroupMessages    *bool `json:"can_read_all_group_messages,omitempty"`
	SupportsGuestQueries       *bool `json:"supports_guest_queries,omitempty"`
	SupportsInlineQueries      *bool `json:"supports_inline_queries,omitempty"`
	CanConnectToBusiness       *bool `json:"can_connect_to_business,omitempty"`
	HasMainWebApp              *bool `json:"has_main_web_app,omitempty"`
	HasTopicsEnabled           *bool `json:"has_topics_enabled,omitempty"`
	AllowsUsersToCreateTopics  *bool `json:"allows_users_to_create_topics,omitempty"`
	CanManageBots              *bool `json:"can_manage_bots,omitempty"`
	SupportsJoinRequestQueries *bool `json:"supports_join_request_queries,omitempty"`
}

// tgUser and senderOut are the decoded and the printed User; both are userOut.
type (
	tgUser        = userOut
	senderOut     = userOut
	memberUserOut = userOut
)

// userSchema describes any printed User; its members are not listed one by one.
const userSchema = `{"type":"object","description":"Telegram Bot API User"}`

// userFieldsText names the User members for tool field descriptions.
const userFieldsText = "Telegram User: id, is_bot, first_name, last_name, username, language_code, is_premium, " +
	"and for bots their capability flags, each only when Telegram sent it"

// cleanUser returns u with its texts bounded; the flags are shared, not copied.
func cleanUser(u userOut) userOut {
	u.FirstName = capText(u.FirstName, maxNameRunes)
	u.LastName = capText(u.LastName, maxNameRunes)
	u.Username = capText(u.Username, maxNameRunes)
	u.LanguageCode = capText(u.LanguageCode, maxNameRunes)
	return u
}

// sender maps a decoded User, or returns nil when Telegram sent none.
func sender(u *tgUser) *senderOut {
	if u == nil || u.ID == 0 {
		return nil
	}
	out := cleanUser(*u)
	return &out
}
